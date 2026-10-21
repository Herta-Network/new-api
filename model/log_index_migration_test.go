package model

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Every log insert maintains every index on logs. An upgraded table loses the
// indexes no query needs and indexes upstream request ids only where they are
// set, as a new table does; the stored logs survive and a second startup
// changes nothing.
func TestMigrateLogIndexes(t *testing.T) {
	tests := []struct {
		name    string
		prepare []string
		// failing runs after prepare and fails.
		failing string
	}{
		{name: "fresh"},
		{
			name: "upgrade",
			prepare: []string{
				`DROP INDEX idx_logs_upstream_request_id`,
				`CREATE INDEX idx_logs_upstream_request_id ON logs (upstream_request_id)`,
				`CREATE INDEX idx_logs_ip ON logs (ip)`,
				`CREATE INDEX idx_logs_user_id ON logs (user_id)`,
				`CREATE INDEX idx_logs_model_name ON logs (model_name)`,
			},
		},
		{
			// A failed concurrent build leaves an INVALID index behind.
			name: "invalid_leftover",
			prepare: []string{
				`DROP INDEX idx_logs_upstream_request_id`,
				`INSERT INTO logs (upstream_request_id) VALUES ('repeated'), ('repeated')`,
			},
			failing: `CREATE UNIQUE INDEX CONCURRENTLY idx_logs_upstream_request_id ON logs (upstream_request_id)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &migrationSQLRecorder{}
			db := pgtest.NewSchemaDB(t).Session(&gorm.Session{Logger: recorder})
			require.NoError(t, db.AutoMigrate(&Log{}))
			for _, statement := range tt.prepare {
				require.NoError(t, db.Exec(statement).Error)
			}
			if tt.failing != "" {
				require.Error(t, db.Exec(tt.failing).Error)
			}
			require.NoError(t, db.Create(&Log{UserId: 1, CreatedAt: 100, ModelName: "gpt-a", UpstreamRequestId: "upstream-1"}).Error)
			require.NoError(t, db.Create(&Log{UserId: 1, CreatedAt: 101, ModelName: "gpt-a"}).Error)

			for pass := range 2 {
				recorder.reset()
				require.NoError(t, migrateLogIndexes(db))
				require.NoError(t, db.AutoMigrate(&Log{}))
				if pass == 1 || tt.name == "fresh" {
					assert.Empty(t, recorder.schemaMutations(), "the schema must already match Log")
				}
			}

			var indexes []struct {
				Indexname string
				Indexdef  string
			}
			require.NoError(t, db.Raw(`SELECT indexname, indexdef FROM pg_indexes
				WHERE schemaname = current_schema() AND tablename = 'logs' ORDER BY indexname`).Scan(&indexes).Error)
			names := make([]string, len(indexes))
			for i, index := range indexes {
				names[i] = index.Indexname
			}
			assert.Equal(t, []string{
				"idx_created_at_id", "idx_created_at_type", "idx_logs_channel_id", "idx_logs_group",
				"idx_logs_request_id", "idx_logs_token_id", "idx_logs_token_name", "idx_logs_upstream_request_id",
				"idx_logs_username", "idx_user_id_id", "index_username_model_name", "logs_pkey",
			}, names)
			for _, index := range indexes {
				if index.Indexname == logUpstreamRequestIdIndex {
					assert.Contains(t, index.Indexdef, "USING btree (upstream_request_id) WHERE ((upstream_request_id)::text <> ''::text)")
				}
			}
			var valid bool
			require.NoError(t, db.Raw(`SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass(?)`, logUpstreamRequestIdIndex).Scan(&valid).Error)
			assert.True(t, valid)

			var found Log
			require.NoError(t, db.Where("upstream_request_id = ?", "upstream-1").Take(&found).Error)
			assert.EqualValues(t, 100, found.CreatedAt)
			var count int64
			require.NoError(t, db.Model(&Log{}).Where("created_at IN ?", []int64{100, 101}).Count(&count).Error)
			assert.EqualValues(t, 2, count)
		})
	}
}
