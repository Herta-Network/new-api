package model

import (
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id                  int    `json:"id"`
	UserID              int    `json:"user_id" gorm:"index;uniqueIndex:uk_quota_data_key,priority:1"`
	Username            string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;uniqueIndex:uk_quota_data_key,priority:2;size:64;default:''"`
	ModelName           string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;uniqueIndex:uk_quota_data_key,priority:3;size:64;default:''"`
	CreatedAt           int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2;uniqueIndex:uk_quota_data_key,priority:4"`
	UseGroup            string `json:"use_group" gorm:"index;uniqueIndex:uk_quota_data_key,priority:5;size:64;default:''"`
	TokenID             int    `json:"token_id" gorm:"index;uniqueIndex:uk_quota_data_key,priority:6;default:0"`
	ChannelID           int    `json:"channel_id" gorm:"index;uniqueIndex:uk_quota_data_key,priority:7;default:0"`
	NodeName            string `json:"node_name" gorm:"index;uniqueIndex:uk_quota_data_key,priority:8;size:64;default:''"`
	TokenUsed           int    `json:"token_used" gorm:"default:0"`
	Count               int    `json:"count" gorm:"default:0"`
	Quota               int    `json:"quota" gorm:"default:0"`
	CacheReadTokens     int64  `json:"cache_read_tokens" gorm:"default:0"`
	CacheCreationTokens int64  `json:"cache_creation_tokens" gorm:"default:0"`
}

// quotaDataKeyIndex makes one row per hour for each user, model, group,
// token, channel and node, the key SaveQuotaDataCache adds counters to.
const quotaDataKeyIndex = "uk_quota_data_key"

// quotaDataUpsert adds a flushed row's counters to the stored row with the
// same key.
var quotaDataUpsert = clause.OnConflict{
	Columns: []clause.Column{
		{Name: "user_id"}, {Name: "username"}, {Name: "model_name"}, {Name: "created_at"},
		{Name: "use_group"}, {Name: "token_id"}, {Name: "channel_id"}, {Name: "node_name"},
	},
	DoUpdates: clause.Assignments(map[string]any{
		"count":                 gorm.Expr("quota_data.count + excluded.count"),
		"quota":                 gorm.Expr("quota_data.quota + excluded.quota"),
		"token_used":            gorm.Expr("quota_data.token_used + excluded.token_used"),
		"cache_read_tokens":     gorm.Expr("quota_data.cache_read_tokens + excluded.cache_read_tokens"),
		"cache_creation_tokens": gorm.Expr("quota_data.cache_creation_tokens + excluded.cache_creation_tokens"),
	}),
}

const quotaDataFlushBatchSize = 1000

// migrateQuotaDataKey adds quotaDataKeyIndex to an existing table before
// AutoMigrate would. Earlier versions looked a row up before inserting it, so
// two flushes could store one key twice; rows that share a key are merged into
// the oldest first. The table lock holds off flushes from other nodes until
// the index exists.
func migrateQuotaDataKey(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasTable(&QuotaData{}) || migrator.HasIndex(&QuotaData{}, quotaDataKeyIndex) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("LOCK TABLE quota_data IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
			return fmt.Errorf("lock quota_data: %w", err)
		}
		// An older table can lack a column the merge reads.
		migrator := tx.Migrator()
		statement := &gorm.Statement{DB: tx}
		if err := statement.Parse(&QuotaData{}); err != nil {
			return fmt.Errorf("parse quota_data schema: %w", err)
		}
		for _, column := range statement.Schema.DBNames {
			if migrator.HasColumn(&QuotaData{}, column) {
				continue
			}
			if err := migrator.AddColumn(&QuotaData{}, column); err != nil {
				return fmt.Errorf("add quota_data column %s: %w", column, err)
			}
		}
		merge := tx.Exec(`
WITH duplicates AS (
	SELECT min(id) AS keep_id,
	       array_agg(id) AS ids,
	       sum(count) AS count,
	       sum(quota) AS quota,
	       sum(token_used) AS token_used,
	       sum(cache_read_tokens) AS cache_read_tokens,
	       sum(cache_creation_tokens) AS cache_creation_tokens
	FROM quota_data
	GROUP BY user_id, username, model_name, created_at, use_group, token_id, channel_id, node_name
	HAVING count(*) > 1
), merged AS (
	UPDATE quota_data
	SET count = duplicates.count,
	    quota = duplicates.quota,
	    token_used = duplicates.token_used,
	    cache_read_tokens = duplicates.cache_read_tokens,
	    cache_creation_tokens = duplicates.cache_creation_tokens
	FROM duplicates
	WHERE quota_data.id = duplicates.keep_id
)
DELETE FROM quota_data
USING duplicates
WHERE quota_data.id = ANY (duplicates.ids)
  AND quota_data.id <> duplicates.keep_id`)
		if merge.Error != nil {
			return fmt.Errorf("merge duplicate quota_data rows: %w", merge.Error)
		}
		if merge.RowsAffected > 0 {
			common.SysLog(common.LogText("merged %d duplicate dashboard records", merge.RowsAffected))
		}
		if err := migrator.CreateIndex(&QuotaData{}, quotaDataKeyIndex); err != nil {
			return fmt.Errorf("create quota_data index %s: %w", quotaDataKeyIndex, err)
		}
		return nil
	})
}

type QuotaDataLogParams struct {
	UserID              int
	Username            string
	ModelName           string
	Quota               int
	CreatedAt           int64
	TokenUsed           int
	CacheReadTokens     int64
	CacheCreationTokens int64
	UseGroup            string
	TokenID             int
	ChannelID           int
	NodeName            string
}

func UpdateQuotaData() {
	for {
		if common.DataExportEnabled {
			common.SysLog(common.LogText("updating dashboard data..."))
			SaveQuotaDataCache()
		}
		time.Sleep(time.Duration(common.DataExportInterval) * time.Minute)
	}
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}

func logQuotaDataCache(quotaData *QuotaData) {
	key := fmt.Sprintf("%d\x00%s\x00%s\x00%d\x00%s\x00%d\x00%d\x00%s",
		quotaData.UserID,
		quotaData.Username,
		quotaData.ModelName,
		quotaData.CreatedAt,
		quotaData.UseGroup,
		quotaData.TokenID,
		quotaData.ChannelID,
		quotaData.NodeName,
	)
	count := quotaData.Count
	quota := quotaData.Quota
	tokenUsed := quotaData.TokenUsed
	cachedQuotaData, ok := CacheQuotaData[key]
	if ok {
		cachedQuotaData.Count += count
		cachedQuotaData.Quota += quota
		cachedQuotaData.TokenUsed += tokenUsed
		cachedQuotaData.CacheReadTokens += quotaData.CacheReadTokens
		cachedQuotaData.CacheCreationTokens += quotaData.CacheCreationTokens
		quotaData = cachedQuotaData
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(params QuotaDataLogParams) {
	// 只精确到小时
	createdAt := params.CreatedAt - (params.CreatedAt % 3600)
	quotaData := &QuotaData{
		UserID:              params.UserID,
		Username:            params.Username,
		ModelName:           params.ModelName,
		CreatedAt:           createdAt,
		UseGroup:            params.UseGroup,
		TokenID:             params.TokenID,
		ChannelID:           params.ChannelID,
		NodeName:            params.NodeName,
		Count:               1,
		Quota:               params.Quota,
		TokenUsed:           params.TokenUsed,
		CacheReadTokens:     max(0, params.CacheReadTokens),
		CacheCreationTokens: max(0, params.CacheCreationTokens),
	}

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(quotaData)
}

func SaveQuotaDataCache() {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	size := len(CacheQuotaData)
	if size > 0 {
		// Write in key order, so flushes from several nodes that share a key
		// lock its row in the same order and cannot deadlock.
		rows := make([]*QuotaData, 0, size)
		for _, key := range slices.Sorted(maps.Keys(CacheQuotaData)) {
			rows = append(rows, CacheQuotaData[key])
		}
		if err := DB.Clauses(quotaDataUpsert).CreateInBatches(rows, quotaDataFlushBatchSize).Error; err != nil {
			// The failed flush wrote nothing, and a row the database rejects,
			// such as one with a name longer than its column, fails every row
			// written with it. Save the rows one at a time instead.
			failed := 0
			var rowErr error
			for _, row := range rows {
				if err := DB.Clauses(quotaDataUpsert).Create(row).Error; err != nil {
					failed++
					rowErr = err
				}
			}
			if failed > 0 {
				common.SysError(common.LogText("failed to save %d dashboard records: %s", failed, rowErr.Error()))
			}
			size -= failed
		}
	}
	CacheQuotaData = make(map[string]*QuotaData)
	common.SysLog(common.LogText("saved dashboard data, %d records", size))
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").
		Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Group("username, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetAllQuotaDates(startTime int64, endTime int64, username string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsername(username, startTime, endTime)
	}
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	// only select model_name, sum(count) as count, sum(quota) as quota, model_name, created_at from quota_data group by model_name, created_at;
	//err = DB.Table("quota_data").Where("created_at >= ? and created_at <= ?", startTime, endTime).Find(&quotaDatas).Error
	err = DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}
