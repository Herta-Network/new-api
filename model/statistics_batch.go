package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const statisticsBatchSize = 500

// StatisticsWriteCheckpoint makes retries safe even when a commit acknowledgement
// is lost. Writers keep an immutable pending batch until it is acknowledged.
// There is one row per process writer, rather than one row per flush. Checkpoints
// must not expire while a writer could still retry an older batch.
type StatisticsWriteCheckpoint struct {
	WriterID  string `gorm:"primaryKey;size:64"`
	Sequence  int64  `gorm:"not null"`
	UpdatedAt int64  `gorm:"not null"`
}

type StatisticsBatch struct {
	WriterID string
	Sequence int64
}

func (batch StatisticsBatch) Apply(write func(*gorm.DB) error) error {
	if batch.WriterID == "" || batch.Sequence <= 0 || write == nil {
		return fmt.Errorf("invalid statistics batch")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		checkpoint := StatisticsWriteCheckpoint{WriterID: batch.WriterID, Sequence: batch.Sequence, UpdatedAt: common.GetTimestamp()}
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "writer_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"sequence", "updated_at"}),
			Where: clause.Where{Exprs: []clause.Expression{clause.Lt{
				Column: clause.Column{Table: "statistics_write_checkpoints", Name: "sequence"}, Value: batch.Sequence,
			}}},
		}).Create(&checkpoint)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return write(tx)
	})
}
