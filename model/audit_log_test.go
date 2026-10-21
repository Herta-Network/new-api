package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestAuditLogSortIndexMigration verifies audit ordering and the index
// migration on a new database and on one that lacks the ordering index.
func TestAuditLogSortIndexMigration(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade=%v", upgrade), func(t *testing.T) {
			db := pgtest.NewSchemaDB(t)
			previousLogDB := LOG_DB
			LOG_DB = db
			t.Cleanup(func() { LOG_DB = previousLogDB })
			require.NoError(t, MigrateAuditLogs())
			if upgrade {
				require.NoError(t, db.Migrator().DropIndex(&AuditLog{}, "idx_audit_time_event"))
			}
			entries := []AuditLog{
				{EventId: "event-older", CreatedAt: 100, UserId: 1, ActorRole: common.RoleCommonUser},
				{EventId: "event-a", CreatedAt: 200, UserId: 1, ActorRole: common.RoleCommonUser},
				{EventId: "event-z", CreatedAt: 200, UserId: 1, ActorRole: common.RoleCommonUser},
			}
			require.NoError(t, db.Create(&entries).Error)
			originalIndexes := auditLogIndexDefinitions(t, db)
			for range 2 {
				require.NoError(t, MigrateAuditLogs())
			}
			indexes := auditLogIndexDefinitions(t, db)
			require.Contains(t, indexes, "idx_audit_time_event", "migration must create the audit ordering index")
			assert.Contains(t, indexes["idx_audit_time_event"], "(created_at, event_id)")
			for name, definition := range originalIndexes {
				assert.Equal(t, definition, indexes[name], "existing index %s must remain", name)
			}
			var stored []AuditLog
			require.NoError(t, db.Order("id").Find(&stored).Error)
			assert.Equal(t, entries, stored, "migration must preserve existing audit records")
			logs, total, err := GetAuditLogs(AuditLogFilter{}, 0, 2, common.RoleRootUser)
			require.NoError(t, err)
			assert.EqualValues(t, 3, total)
			require.Len(t, logs, 2)
			assert.Equal(t, []string{"event-z", "event-a"}, []string{logs[0].EventId, logs[1].EventId})
			logs, total, err = GetAuditLogs(AuditLogFilter{}, 2, 2, common.RoleRootUser)
			require.NoError(t, err)
			assert.EqualValues(t, 3, total)
			require.Len(t, logs, 1)
			assert.Equal(t, "event-older", logs[0].EventId)
			assert.Error(t, db.Create(&AuditLog{EventId: "event-a"}).Error, "event IDs must remain unique")
		})
	}
}

// auditLogIndexDefinitions reads the audit_logs indexes of the current schema
// by name. GORM's PostgreSQL GetIndexes also matches same-named tables in
// other schemas, such as the test package's own schema.
func auditLogIndexDefinitions(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()
	var rows []struct {
		Name       string `gorm:"column:indexname"`
		Definition string `gorm:"column:indexdef"`
	}
	require.NoError(t, db.Raw(`
SELECT indexname, indexdef FROM pg_catalog.pg_indexes
WHERE schemaname = current_schema() AND tablename = 'audit_logs'`).Scan(&rows).Error)
	definitions := make(map[string]string, len(rows))
	for _, row := range rows {
		definitions[row.Name] = row.Definition
	}
	return definitions
}
