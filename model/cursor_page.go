package model

import (
	"context"
	"encoding/base64"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// CursorPage omits an exact total: determining the next page needs only one
// extra row, independent of the number of matching records.
type CursorPage[T any] struct {
	Items          []T    `json:"items"`
	Page           int    `json:"page"`
	PageSize       int    `json:"page_size"`
	NextCursor     string `json:"next_cursor"`
	PreviousCursor string `json:"previous_cursor"`
}

type listCursor struct {
	Scope   string `json:"s"`
	Upper   int64  `json:"u"`
	ID      int64  `json:"i"`
	Time    int64  `json:"t"`
	Page    int    `json:"p"`
	Size    int    `json:"n"`
	Reverse bool   `json:"r,omitempty"`
}

func (c listCursor) encode() (string, error) {
	data, err := common.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data), err
}

// cursorPage always reapplies the caller's filters and ownership. Cursor data
// is only a position, never authorization. The upper ID excludes later inserts.
func cursorPage[T any](query *gorm.DB, raw string, size int, scope, timeColumn string, position func(T) (int64, int64)) (CursorPage[T], error) {
	page := CursorPage[T]{Items: []T{}, Page: 1, PageSize: min(100, max(1, size))}
	cursor := listCursor{Scope: scope, Page: 1, Size: page.PageSize}
	if raw != "" {
		if len(raw) > 512 {
			return page, common.NewMessage("Invalid parameters")
		}
		data, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || common.Unmarshal(data, &cursor) != nil || cursor.Scope != scope || cursor.Size != page.PageSize || cursor.Upper < 1 || cursor.ID < 1 || cursor.ID > cursor.Upper || cursor.Page < 1 || cursor.Page > 1000000 {
			return page, common.NewMessage("Invalid parameters")
		}
		page.Page = cursor.Page
	} else {
		// Unfiltered MAX(id) uses the primary-key index instead of scanning matches.
		if err := query.Session(&gorm.Session{NewDB: true}).Model(new(T)).Select("COALESCE(MAX(id), 0)").Scan(&cursor.Upper).Error; err != nil {
			return page, err
		}
		if cursor.Upper == 0 {
			return page, nil
		}
	}
	query = query.Session(&gorm.Session{}).Where("id <= ?", cursor.Upper)
	direction, comparison := "desc", "<"
	if cursor.Reverse {
		direction, comparison = "asc", ">"
	}
	if timeColumn != "" {
		query = query.Order(timeColumn + " " + direction + ", id " + direction)
		if raw != "" {
			query = query.Where("("+timeColumn+", id) "+comparison+" (?, ?)", cursor.Time, cursor.ID)
		}
	} else {
		query = query.Order("id " + direction)
		if raw != "" {
			query = query.Where("id "+comparison+" ?", cursor.ID)
		}
	}
	if err := query.Limit(page.PageSize + 1).Find(&page.Items).Error; err != nil {
		return page, err
	}
	more := len(page.Items) > page.PageSize
	if more {
		page.Items = page.Items[:page.PageSize]
	}
	if cursor.Reverse {
		slices.Reverse(page.Items)
	}
	if len(page.Items) == 0 {
		// A page may disappear while logs are being cleaned up. Allow returning.
		if !cursor.Reverse && page.Page > 1 {
			cursor.Reverse = true
			cursor.Page--
			page.PreviousCursor, _ = cursor.encode()
		}
		return page, nil
	}
	if (!cursor.Reverse && more) || cursor.Reverse {
		next := cursor
		next.ID, next.Time = position(page.Items[len(page.Items)-1])
		next.Reverse, next.Page = false, page.Page+1
		var err error
		page.NextCursor, err = next.encode()
		if err != nil {
			return page, err
		}
	}
	if page.Page > 1 && (!cursor.Reverse || more) {
		previous := cursor
		previous.ID, previous.Time = position(page.Items[0])
		previous.Reverse, previous.Page = true, page.Page-1
		var err error
		page.PreviousCursor, err = previous.encode()
		if err != nil {
			return page, err
		}
	}
	return page, nil
}

func GetLogCursorPage(ctx context.Context, userID int, all bool, filter LogFilter, raw string, size int) (CursorPage[*Log], error) {
	query := LOG_DB.WithContext(ctx).Model(&Log{})
	scope, timeColumn := "logs:all", "created_at"
	if !all {
		query = query.Where("user_id = ?", userID)
		scope, timeColumn = "logs:user:"+strconv.Itoa(userID), ""
	}
	query, err := filter.apply(query)
	if err != nil {
		return CursorPage[*Log]{}, err
	}
	page, err := cursorPage(query, raw, size, scope, timeColumn, func(row *Log) (int64, int64) { return int64(row.Id), row.CreatedAt })
	if err != nil {
		return page, err
	}
	if !all {
		formatUserLogs(page.Items, (page.Page-1)*page.PageSize)
	} else if err := fillLogChannelNames(ctx, page.Items); err != nil {
		return page, err
	}
	return page, nil
}

func GetTaskCursorPage(ctx context.Context, userID int, all bool, filter SyncTaskQueryParams, raw string, size int) (CursorPage[*Task], error) {
	query := DB.WithContext(ctx).Model(&Task{}).Omit("data")
	scope := "tasks:all"
	if !all {
		query = query.Where("user_id = ?", userID).Omit("data", "channel_id")
		scope = "tasks:user:" + strconv.Itoa(userID)
	} else if filter.ChannelID != "" {
		query = query.Where("channel_id = ?", filter.ChannelID)
	}
	if all && filter.UserID != "" {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if all && len(filter.UserIDs) > 0 {
		query = query.Where("user_id IN ?", filter.UserIDs)
	}
	if filter.Platform != "" {
		query = query.Where("platform = ?", filter.Platform)
	}
	if filter.TaskID != "" {
		query = query.Where("task_id = ?", filter.TaskID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", filter.EndTimestamp)
	}
	return cursorPage(query, raw, size, scope, "", func(row *Task) (int64, int64) { return row.ID, 0 })
}
