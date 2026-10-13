package model

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id        int    `json:"id"`
	UserID    int    `json:"user_id" gorm:"index;not null;uniqueIndex:idx_quota_data_dimensions,priority:1;default:0"`
	Username  string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:'';not null;uniqueIndex:idx_quota_data_dimensions,priority:2"`
	ModelName string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:'';not null;uniqueIndex:idx_quota_data_dimensions,priority:3"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2;not null;uniqueIndex:idx_quota_data_dimensions,priority:4;default:0"`
	UseGroup  string `json:"use_group" gorm:"index;size:64;default:'';not null;uniqueIndex:idx_quota_data_dimensions,priority:5"`
	TokenID   int    `json:"token_id" gorm:"index;default:0;not null;uniqueIndex:idx_quota_data_dimensions,priority:6"`
	ChannelID int    `json:"channel_id" gorm:"index;default:0;not null;uniqueIndex:idx_quota_data_dimensions,priority:7"`
	NodeName  string `json:"node_name" gorm:"index;size:64;default:'';not null;uniqueIndex:idx_quota_data_dimensions,priority:8"`
	TokenUsed int    `json:"token_used" gorm:"not null;default:0"`
	Count     int    `json:"count" gorm:"not null;default:0"`
	Quota     int    `json:"quota" gorm:"not null;default:0"`
}

type QuotaDataLogParams struct {
	UserID    int
	Username  string
	ModelName string
	Quota     int
	CreatedAt int64
	TokenUsed int
	UseGroup  string
	TokenID   int
	ChannelID int
	NodeName  string
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
		quotaData = cachedQuotaData
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(params QuotaDataLogParams) {
	// 只精确到小时
	createdAt := params.CreatedAt - (params.CreatedAt % 3600)
	quotaData := &QuotaData{
		UserID:    params.UserID,
		Username:  params.Username,
		ModelName: params.ModelName,
		CreatedAt: createdAt,
		UseGroup:  params.UseGroup,
		TokenID:   params.TokenID,
		ChannelID: params.ChannelID,
		NodeName:  params.NodeName,
		Count:     1,
		Quota:     params.Quota,
		TokenUsed: params.TokenUsed,
	}

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(quotaData)
}

var quotaDataFlushMutex sync.Mutex
var quotaDataWriter = StatisticsBatch{WriterID: common.GetUUID()}
var pendingQuotaData []QuotaData

func SaveQuotaDataCache() {
	quotaDataFlushMutex.Lock()
	defer quotaDataFlushMutex.Unlock()
	// A retry is acknowledged first; then one current snapshot is flushed.
	for range 2 {
		if len(pendingQuotaData) == 0 {
			CacheQuotaDataLock.Lock()
			snapshot := CacheQuotaData
			CacheQuotaData = make(map[string]*QuotaData)
			CacheQuotaDataLock.Unlock()
			if len(snapshot) == 0 {
				return
			}
			keys := make([]string, 0, len(snapshot))
			for key := range snapshot {
				keys = append(keys, key)
			}
			slices.Sort(keys) // Consistent row-lock order across writers.
			pendingQuotaData = make([]QuotaData, 0, len(keys))
			for _, key := range keys {
				pendingQuotaData = append(pendingQuotaData, *snapshot[key])
			}
			quotaDataWriter.Sequence++
		}
		err := quotaDataWriter.Apply(func(tx *gorm.DB) error {
			return tx.Omit("id").Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "user_id"}, {Name: "username"}, {Name: "model_name"}, {Name: "created_at"}, {Name: "use_group"}, {Name: "token_id"}, {Name: "channel_id"}, {Name: "node_name"}},
				DoUpdates: clause.Assignments(map[string]any{
					"count":      gorm.Expr("quota_data.count + EXCLUDED.count"),
					"quota":      gorm.Expr("quota_data.quota + EXCLUDED.quota"),
					"token_used": gorm.Expr("quota_data.token_used + EXCLUDED.token_used"),
				}),
			}).CreateInBatches(&pendingQuotaData, statisticsBatchSize).Error
		})
		if err != nil {
			common.SysError(common.LogText("failed to save dashboard data: %s", err.Error()))
			return // Retain exactly this batch and sequence, including ambiguous commits.
		}
		common.SysLog(common.LogText("saved dashboard data, %d records", len(pendingQuotaData)))
		pendingQuotaData = nil
	}
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").
		Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
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
	err = DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}
