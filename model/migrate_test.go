package model

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"testing"
	"testing/fstest"

	currencyentity "github.com/QuantumNous/new-api/internal/module/currency/entity"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// schemaModels are the models stored in the main and log databases. Their
// GORM tags must describe the schema the migrations create.
var schemaModels = []any{
	&Channel{}, &Token{}, &User{}, &UserSession{}, &AuthFlow{}, &ExternalIdentityClaim{},
	&PasskeyCredential{}, &Option{}, &LoginEncryptionKey{}, &Redemption{}, &Ability{},
	&Midjourney{}, &QuotaData{}, &Task{}, &TaskPlugin{}, &Model{}, &Vendor{}, &PrefillGroup{},
	&Setup{}, &TwoFA{}, &TwoFABackupCode{}, &Checkin{}, &UserSubscription{},
	&SubscriptionPreConsumeRecord{}, &CustomOAuthProvider{}, &UserOAuthBinding{}, &PerfMetric{},
	&SystemInstance{}, &SystemTask{}, &SystemTaskLock{}, &CasbinRule{}, &AuthzRole{},
	&UserAccessToken{}, &SubscriptionPlan{}, &entity.PaymentOption{}, &entity.PaymentMethod{}, &entity.PaymentOrder{}, &entity.PaymentInvoice{},
	&entity.PaymentUnmatched{}, &currencyentity.ExchangeRate{},
	&Log{}, &AuditLog{},
}

// sqlOnlySchemaObjects lists objects a migration creates that no GORM tag can
// declare, keyed like describeSchema.
var sqlOnlySchemaObjects = map[string]bool{}

func describeTestSchema(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	tx, err := sqlDB.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	var schema string
	require.NoError(t, tx.QueryRow("SELECT current_schema()").Scan(&schema))
	described, err := describeSchema(tx, schema)
	require.NoError(t, err)
	return described
}

func migrateTestSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, runMigrations(db, migrationFiles, mainMigrations))
	require.NoError(t, runMigrations(db, migrationFiles, logMigrations))
}

// The migrations create exactly the schema the models declare. A model change
// without a migration, or a migration without the model change, fails here.
func TestMigrationsMatchModels(t *testing.T) {
	migrated := pgtest.NewSchemaDB(t)
	migrateTestSchema(t, migrated)
	declared := pgtest.NewSchemaDB(t)
	require.NoError(t, declared.AutoMigrate(schemaModels...))

	created := describeTestSchema(t, migrated)
	expected := describeTestSchema(t, declared)
	var differences []string
	for key, definition := range expected {
		found, ok := created[key]
		switch {
		case !ok:
			differences = append(differences, "no migration creates "+key+" "+definition)
		case found != definition:
			differences = append(differences, key+": the models declare "+definition+", the migrations create "+found)
		}
	}
	for key, definition := range created {
		if _, ok := expected[key]; !ok && !sqlOnlySchemaObjects[key] {
			differences = append(differences, "no model declares "+key+" "+definition)
		}
	}
	slices.Sort(differences)
	assert.Empty(t, differences, "add a migration under model/migrations that matches the GORM tags, or list an object no tag can declare in sqlOnlySchemaObjects")
}

// A database the last release before versioned migrations created is adopted
// at the baseline, keeps its rows, and ends up with the schema of a new
// database. One that differs from the baseline is refused and left untouched.
func TestStartupAdoptsExistingDatabase(t *testing.T) {
	createLegacy := func(t *testing.T) *gorm.DB {
		db := pgtest.NewSchemaDB(t)
		for _, set := range []migrationSet{mainMigrations, logMigrations} {
			baseline, err := fs.ReadFile(migrationFiles, set.dir+"/000001_baseline.up.sql")
			require.NoError(t, err)
			require.NoError(t, db.Exec(string(baseline)).Error)
		}
		return db
	}

	db := createLegacy(t)
	require.NoError(t, db.Create(&User{Username: "legacy", Password: "placeholder", AffCode: "legacy-aff", Quota: 42}).Error)
	require.NoError(t, db.Create(&Log{UserId: 1, Content: "historical", CreatedAt: 100}).Error)
	for range 2 {
		migrateTestSchema(t, db)
	}
	fresh := pgtest.NewSchemaDB(t)
	migrateTestSchema(t, fresh)
	assert.Equal(t, describeTestSchema(t, fresh), describeTestSchema(t, db))
	for _, table := range []string{mainMigrations.table, logMigrations.table} {
		var version, freshVersion int
		var dirty bool
		require.NoError(t, db.Raw("SELECT version, dirty FROM "+table).Row().Scan(&version, &dirty))
		require.NoError(t, fresh.Raw("SELECT version FROM "+table).Row().Scan(&freshVersion))
		assert.Equal(t, freshVersion, version, table)
		assert.False(t, dirty, table)
	}
	var user User
	require.NoError(t, db.Where("username = ?", "legacy").First(&user).Error)
	assert.Equal(t, 42, user.Quota)
	var log Log
	require.NoError(t, db.First(&log).Error)
	assert.Equal(t, "historical", log.Content)

	drifted := createLegacy(t)
	require.NoError(t, drifted.Exec("ALTER TABLE users DROP COLUMN quota").Error)
	err := runMigrations(drifted, migrationFiles, mainMigrations)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing column users.quota")
	assert.Contains(t, err.Error(), legacySchemaRelease)
	var versionTable *string
	require.NoError(t, drifted.Raw("SELECT to_regclass(?)::text", mainMigrations.table).Row().Scan(&versionTable))
	assert.Nil(t, versionTable, "a refused database is left untouched")
}

// Upgrading a database that holds payment orders gives every gateway order
// its first invoice under the order's own number, which its gateway knows,
// with the checkout, the trade and the state the order had.
func TestPaymentInvoicesMigrationKeepsCheckouts(t *testing.T) {
	db := pgtest.NewSchemaDB(t)
	baseline, err := fs.ReadFile(migrationFiles, mainMigrations.dir+"/000001_baseline.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(baseline)).Error)
	legacy := func(orderNo, platform, tradeNo string, status int, extra map[string]any) {
		row := map[string]any{
			"order_no": orderNo, "user_id": 1, "purpose": "topup", "currency": "USD", "amount": 1990, "payment_method_id": 3,
			"platform": platform, "payment_amount": 14411, "payment_currency": "CNY", "exchange_rate": "7.2419",
			"trade_no": tradeNo, "status": status, "created_at": 100,
		}
		maps.Copy(row, extra)
		require.NoError(t, db.Table("payment_orders").Create(row).Error)
	}
	legacy("P-pending", "stripe", "pi_1", entity.PaymentOrderPending, nil)
	legacy("P-unstarted", "epay", "", entity.PaymentOrderPending, map[string]any{"payment_amount": 0, "payment_currency": "", "exchange_rate": ""})
	legacy("P-finished", "epay", "T1", entity.PaymentOrderFinished, map[string]any{"paid_at": 200})
	legacy("P-manual", "epay", "manual:P-manual", entity.PaymentOrderFinished, map[string]any{"paid_at": 300})
	legacy("P-closed", "cryptomus", "", entity.PaymentOrderClosed, map[string]any{"close_reason": "expired", "closed_at": 400})
	legacy("P-balance", "balance", "", entity.PaymentOrderFinished, map[string]any{"payment_method_id": -1})
	require.NoError(t, db.Table("payment_unmatched").Create(map[string]any{
		"platform": "epay", "trade_no": "T0", "order_no": "P-finished", "user_id": 1, "amount": 100, "currency": "CNY", "created_at": 500,
	}).Error)

	for range 2 {
		migrateTestSchema(t, db)
	}
	var invoices []entity.PaymentInvoice
	require.NoError(t, db.Order("id").Find(&invoices).Error)
	type invoiceState struct {
		InvoiceNo, Platform, Currency, TradeNo, CloseReason string
		Amount                                              int64
		Status                                              int
		PaidAt, ClosedAt                                    int64
	}
	var got []invoiceState
	for _, invoice := range invoices {
		var order entity.PaymentOrder
		require.NoError(t, db.Where("id = ?", invoice.OrderId).First(&order).Error)
		assert.Equal(t, order.OrderNo, invoice.InvoiceNo, "an order's first invoice takes its number")
		got = append(got, invoiceState{invoice.InvoiceNo, invoice.Platform, invoice.PaymentCurrency, invoice.TradeNo, invoice.CloseReason,
			invoice.PaymentAmount, invoice.Status, invoice.PaidAt, invoice.ClosedAt})
	}
	assert.Equal(t, []invoiceState{
		{"P-pending", "stripe", "CNY", "pi_1", "", 14411, entity.PaymentInvoicePending, 0, 0},
		{"P-unstarted", "epay", "", "", "", 0, entity.PaymentInvoicePending, 0, 0},
		{"P-finished", "epay", "CNY", "T1", "", 14411, entity.PaymentInvoicePaid, 200, 0},
		{"P-manual", "epay", "CNY", "", "manual", 14411, entity.PaymentInvoiceClosed, 0, 300},
		{"P-closed", "cryptomus", "CNY", "", "expired", 14411, entity.PaymentInvoiceClosed, 0, 400},
	}, got, "the balance order has no invoice")
	var record entity.PaymentUnmatched
	require.NoError(t, db.First(&record).Error)
	assert.Equal(t, "P-finished", record.InvoiceNo)
}

// Upgrading a database with payment methods gives each of them a payment
// option of its own under the same id, holding the name, icon, fee and
// switch buyers saw, so they keep the same choices at the same prices; the
// method stays on as the option's channel and orders name the option.
func TestPaymentOptionsMigrationKeepsBuyersChoices(t *testing.T) {
	db := pgtest.NewSchemaDB(t)
	baseline, err := fs.ReadFile(migrationFiles, mainMigrations.dir+"/000001_baseline.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(baseline)).Error)
	method := func(id int, platform, icon, config string, enabled bool, extra map[string]any) {
		row := map[string]any{
			"id": id, "name": fmt.Sprintf("method %d", id), "platform": platform, "icon": icon, "description": "pay here",
			"sort": id, "enabled": enabled, "config": config, "token": fmt.Sprintf("token-%d", id), "created_at": 100,
		}
		maps.Copy(row, extra)
		require.NoError(t, db.Table("payment_methods").Create(row).Error)
	}
	method(-1, "balance", "", "", false, nil)
	method(2, "stripe", "", `{"payment_method_type":"alipay","public_key":"pk"}`, true, map[string]any{"fee_mode": 2, "fee_fixed": 30})
	method(3, "epay", "SiWechat", `{"url":"https://pay.example","type":"wxpay"}`, false, map[string]any{"fee_mode": 1, "fee_rate_bp": 300})
	method(4, "epay", "", `{"url":"https://pay.example","type":"wxpay"}`, true, map[string]any{"deleted_at": "2026-01-01T00:00:00Z"})
	method(5, "cryptomus", "", `{"merchant_id":"m"}`, true, nil)
	require.NoError(t, db.Table("payment_orders").Create(map[string]any{
		"order_no": "P-1", "user_id": 1, "purpose": "topup", "currency": "USD", "payment_method_id": 3, "platform": "epay", "status": 3,
	}).Error)

	for range 2 {
		migrateTestSchema(t, db)
	}
	var options []entity.PaymentOption
	require.NoError(t, db.Unscoped().Order("id").Find(&options).Error)
	type optionState struct {
		Id                      int
		Name, Icon, Description string
		Sort, FeeMode           int
		FeeRateBp, FeeFixed     int64
		Enabled, Deleted        bool
	}
	var got []optionState
	for _, option := range options {
		got = append(got, optionState{option.Id, option.Name, option.Icon, option.Description, option.Sort, option.FeeMode,
			option.FeeRateBp, option.FeeFixed, option.Enabled, option.DeletedAt.Valid})
	}
	assert.Equal(t, []optionState{
		{-1, "method -1", "FaWallet", "pay here", -1, 0, 0, 0, false, false},
		{2, "method 2", "SiAlipay", "pay here", 2, 2, 0, 30, true, false},
		{3, "method 3", "SiWechat", "pay here", 3, 1, 300, 0, false, false},
		{4, "method 4", "RiWechatPayFill", "pay here", 4, 0, 0, 0, true, true},
		{5, "method 5", "SiBitcoin", "pay here", 5, 0, 0, 0, true, false},
	}, got)
	var methods []entity.PaymentMethod
	require.NoError(t, db.Unscoped().Order("id").Find(&methods).Error)
	for _, method := range methods {
		assert.Equal(t, method.Id, method.OptionId)
		assert.True(t, method.Enabled, "the option took over the switch of method %d", method.Id)
		assert.Equal(t, 1, method.Weight)
	}
	var order entity.PaymentOrder
	require.NoError(t, db.Where("order_no = ?", "P-1").First(&order).Error)
	assert.Equal(t, 3, order.PaymentOptionId)
	created := entity.PaymentOption{Name: "new", Enabled: true}
	require.NoError(t, db.Create(&created).Error)
	assert.Greater(t, created.Id, 5, "new options take ids after the migrated ones")
}

// A failed migration is rolled back and runs again on the next start, and an
// older build leaves a database a newer one migrated alone.
func TestMigrationRetriesAfterFailure(t *testing.T) {
	db := pgtest.NewSchemaDB(t)
	set := migrationSet{name: "retry", dir: "m", table: "retry_schema_migrations", markerTable: "retry_marker"}
	files := fstest.MapFS{
		"m/000001_first.up.sql":  {Data: []byte("CREATE TABLE retry_first (id bigint);")},
		"m/000002_second.up.sql": {Data: []byte("CREATE TABLE retry_second (id bigint);\nINSERT INTO retry_missing VALUES (1);")},
	}
	state := func() (version int, dirty bool) {
		require.NoError(t, db.Raw("SELECT version, dirty FROM "+set.table).Row().Scan(&version, &dirty))
		return version, dirty
	}
	hasTable := func(name string) bool {
		var exists bool
		require.NoError(t, db.Raw("SELECT to_regclass(?) IS NOT NULL", name).Row().Scan(&exists))
		return exists
	}

	require.Error(t, runMigrations(db, files, set))
	version, dirty := state()
	assert.Equal(t, 2, version)
	assert.True(t, dirty)
	assert.True(t, hasTable("retry_first"))
	assert.False(t, hasTable("retry_second"), "a failed migration leaves nothing behind")

	files["m/000002_second.up.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE retry_second (id bigint);")}
	require.NoError(t, runMigrations(db, files, set))
	version, dirty = state()
	assert.Equal(t, 2, version)
	assert.False(t, dirty)
	assert.True(t, hasTable("retry_second"))

	delete(files, "m/000002_second.up.sql")
	require.NoError(t, runMigrations(db, files, set))
	version, _ = state()
	assert.Equal(t, 2, version)
}
