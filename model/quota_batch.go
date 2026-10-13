package model

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const quotaBatchSize = 500

// QuotaBatch is a durable, immutable flush unit. Its receipt is committed in the
// same transaction as the balance updates and deletion of this queue row.
type QuotaBatch struct {
	ID        int64  `gorm:"primaryKey"`
	WriterID  string `gorm:"size:64;not null;uniqueIndex:idx_quota_batch_writer_sequence,priority:1"`
	Sequence  int64  `gorm:"not null;uniqueIndex:idx_quota_batch_writer_sequence,priority:2"`
	Payload   string `gorm:"type:jsonb;not null"`
	CreatedAt int64  `gorm:"not null"`
}

type QuotaBatchReceipt struct {
	WriterID  string `gorm:"size:64;primaryKey"`
	Sequence  int64  `gorm:"not null"`
	UpdatedAt int64  `gorm:"not null"`
}

type quotaUserDelta struct {
	ID       int `json:"id"`
	Quota    int `json:"quota"`
	Used     int `json:"used"`
	Requests int `json:"requests"`
}

type quotaEntityDelta struct {
	ID    int `json:"id"`
	Quota int `json:"quota"`
}
type quotaBatchPayload struct {
	Users    []quotaUserDelta   `json:"users"`
	Tokens   []quotaEntityDelta `json:"tokens"`
	Channels []quotaEntityDelta `json:"channels"`
}

var quotaBatchFlushMutex sync.Mutex
var quotaBatchWriterID = common.GetUUID()
var quotaBatchSequence int64
var pendingQuotaBatches []QuotaBatch

// FlushQuotaBatches first makes drained in-memory deltas durable, then processes
// queued batches from all writers. Failed/ambiguous writes retain the same IDs.
func FlushQuotaBatches(ctx context.Context) error {
	quotaBatchFlushMutex.Lock()
	defer quotaBatchFlushMutex.Unlock()
	// Retry the prior immutable batch, then include traffic accumulated during the failure.
	for range 2 {
		if len(pendingQuotaBatches) == 0 {
			stores := make([]map[int]int, BatchUpdateTypeCount)
			for i := range BatchUpdateTypeCount {
				batchUpdateLocks[i].Lock()
			}
			for i := range BatchUpdateTypeCount {
				stores[i] = batchUpdateStores[i]
				batchUpdateStores[i] = make(map[int]int)
			}
			for i := BatchUpdateTypeCount - 1; i >= 0; i-- {
				batchUpdateLocks[i].Unlock()
			}
			users := map[int]quotaUserDelta{}
			for _, kind := range []int{BatchUpdateTypeUserQuota, BatchUpdateTypeUsedQuota, BatchUpdateTypeRequestCount} {
				for id, value := range stores[kind] {
					row := users[id]
					row.ID = id
					switch kind {
					case BatchUpdateTypeUserQuota:
						row.Quota = value
					case BatchUpdateTypeUsedQuota:
						row.Used = value
					case BatchUpdateTypeRequestCount:
						row.Requests = value
					}
					users[id] = row
				}
			}
			payload := quotaBatchPayload{}
			for _, row := range users {
				payload.Users = append(payload.Users, row)
			}
			for id, value := range stores[BatchUpdateTypeTokenQuota] {
				payload.Tokens = append(payload.Tokens, quotaEntityDelta{ID: id, Quota: value})
			}
			for id, value := range stores[BatchUpdateTypeChannelUsedQuota] {
				payload.Channels = append(payload.Channels, quotaEntityDelta{ID: id, Quota: value})
			}
			slices.SortFunc(payload.Users, func(a, b quotaUserDelta) int { return cmp.Compare(a.ID, b.ID) })
			slices.SortFunc(payload.Tokens, func(a, b quotaEntityDelta) int { return cmp.Compare(a.ID, b.ID) })
			slices.SortFunc(payload.Channels, func(a, b quotaEntityDelta) int { return cmp.Compare(a.ID, b.ID) })
			size := max(len(payload.Users), len(payload.Tokens), len(payload.Channels))
			var batches []QuotaBatch
			for start := 0; start < size; start += quotaBatchSize {
				part := quotaBatchPayload{
					Users:    payload.Users[min(start, len(payload.Users)):min(start+quotaBatchSize, len(payload.Users))],
					Tokens:   payload.Tokens[min(start, len(payload.Tokens)):min(start+quotaBatchSize, len(payload.Tokens))],
					Channels: payload.Channels[min(start, len(payload.Channels)):min(start+quotaBatchSize, len(payload.Channels))],
				}
				encoded, err := common.Marshal(part)
				if err != nil {
					// No batch has been submitted yet; restore the drained deltas.
					for kind, records := range stores {
						for id, delta := range records {
							addNewRecord(kind, id, delta)
						}
					}
					return err
				}
				quotaBatchSequence++
				batches = append(batches, QuotaBatch{WriterID: quotaBatchWriterID, Sequence: quotaBatchSequence, Payload: string(encoded), CreatedAt: common.GetTimestamp()})
			}
			pendingQuotaBatches = batches
		}
		if len(pendingQuotaBatches) > 0 {
			if err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return tx.Omit("id").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "writer_id"}, {Name: "sequence"}}, DoNothing: true}).CreateInBatches(&pendingQuotaBatches, 100).Error
			}); err != nil {
				return err
			}
			pendingQuotaBatches = nil
		}
	}
	var failures []error
	failedIDs := make([]int64, 0)
	for range 100 {
		id, err := applyNextQuotaBatch(ctx, failedIDs)
		if err != nil {
			if ctx.Err() != nil {
				return errors.Join(append(failures, ctx.Err())...)
			}
			failures = append(failures, fmt.Errorf("quota batch %d: %w", id, err))
			if id == 0 {
				break
			}
			failedIDs = append(failedIDs, id)
		}
		if id == 0 {
			break
		}
	}
	return errors.Join(failures...)
}

func applyNextQuotaBatch(ctx context.Context, excluded []int64) (int64, error) {
	var batch QuotaBatch
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where(`NOT EXISTS (SELECT 1 FROM quota_batches AS earlier WHERE earlier.writer_id = quota_batches.writer_id AND earlier.sequence < quota_batches.sequence)`)
		if len(excluded) > 0 {
			query = query.Where("id NOT IN ?", excluded)
		}
		if err := lockForUpdateSkipLocked(query).Order("id").Take(&batch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		receipt := QuotaBatchReceipt{WriterID: batch.WriterID, Sequence: batch.Sequence, UpdatedAt: common.GetTimestamp()}
		claimed := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "writer_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"sequence", "updated_at"}),
			Where:     clause.Where{Exprs: []clause.Expression{clause.Lt{Column: clause.Column{Table: "quota_batch_receipts", Name: "sequence"}, Value: batch.Sequence}}},
		}).Create(&receipt)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected > 0 {
			var payload quotaBatchPayload
			if err := common.UnmarshalJsonStr(batch.Payload, &payload); err != nil {
				return err
			}
			if err := applyQuotaBatchDeltas(tx, payload, batch.CreatedAt); err != nil {
				return err
			}
		}
		return tx.Delete(&batch).Error
	})
	return batch.ID, err
}

func applyQuotaBatchDeltas(tx *gorm.DB, payload quotaBatchPayload, accessedAt int64) error {
	if len(payload.Users) > quotaBatchSize || len(payload.Tokens) > quotaBatchSize || len(payload.Channels) > quotaBatchSize {
		return errors.New("quota batch exceeds row limit")
	}
	if len(payload.Users) > 0 {
		ids := make([]int, len(payload.Users))
		for i, row := range payload.Users {
			if row.ID <= 0 || i > 0 && row.ID <= payload.Users[i-1].ID {
				return errors.New("invalid quota batch user IDs")
			}
			ids[i] = row.ID
		}
		var current []User
		if err := lockForUpdate(tx).Select("id", "quota").Where("id IN ?", ids).Order("id").Find(&current).Error; err != nil {
			return err
		}
		byID := make(map[int]int, len(current))
		for _, row := range current {
			byID[row.Id] = row.Quota
		}
		var values strings.Builder
		args := make([]any, 0, len(payload.Users)*4)
		for i, row := range payload.Users {
			if quota, exists := byID[row.ID]; exists && row.Quota > 0 && quota > common.MaxWalletQuota-row.Quota {
				return ErrWalletQuotaLimitExceeded
			}
			if i > 0 {
				values.WriteString(",")
			}
			values.WriteString("(?::bigint,?::bigint,?::bigint,?::bigint)")
			args = append(args, row.ID, row.Quota, row.Used, row.Requests)
		}
		if err := tx.Exec(`UPDATE users AS target SET quota=target.quota+delta.quota,used_quota=target.used_quota+delta.used,request_count=target.request_count+delta.requests FROM (VALUES `+values.String()+`) AS delta(id,quota,used,requests) WHERE target.id=delta.id AND target.deleted_at IS NULL`, args...).Error; err != nil {
			return err
		}
	}
	for _, kind := range []string{"tokens", "channels"} {
		rows := payload.Tokens
		if kind == "channels" {
			rows = payload.Channels
		}
		if len(rows) == 0 {
			continue
		}
		var values strings.Builder
		args := make([]any, 0, len(rows)*2+1)
		for i, row := range rows {
			if row.ID <= 0 || i > 0 && row.ID <= rows[i-1].ID {
				return errors.New("invalid quota batch entity IDs")
			}
			if i > 0 {
				values.WriteString(",")
			}
			values.WriteString("(?::bigint,?::bigint)")
			args = append(args, row.ID, row.Quota)
		}
		query := `UPDATE channels AS target SET used_quota=target.used_quota+delta.quota FROM (VALUES ` + values.String() + `) AS delta(id,quota) WHERE target.id=delta.id`
		if kind == "tokens" {
			query = `UPDATE tokens AS target SET remain_quota=target.remain_quota+delta.quota,used_quota=target.used_quota-delta.quota,accessed_time=GREATEST(target.accessed_time,?) FROM (VALUES ` + values.String() + `) AS delta(id,quota) WHERE target.id=delta.id AND target.deleted_at IS NULL`
			args = append([]any{accessedAt}, args...)
		}
		if err := tx.Exec(query, args...).Error; err != nil {
			return err
		}
	}
	return nil
}
