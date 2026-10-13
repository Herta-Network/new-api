package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/utils/tests"
)

// PostgreSQL row locks must remain present in the generated query.
func TestLockForUpdateEmitsRowLock(t *testing.T) {
	dummyDB, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{DryRun: true})
	require.NoError(t, err)
	var rows []Redemption
	query := lockForUpdate(dummyDB).Where("id = ?", 1).Find(&rows)
	assert.Contains(t, query.Statement.SQL.String(), "FOR UPDATE")
}
