package model

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ledgerEntry is the part of a ledger entry the tests compare.
type ledgerEntry struct {
	Type       string
	Quota      int64
	RefId      string
	RefName    string
	OperatorId int
}

// userLedger returns the ledger of userId, oldest first, and checks that it
// adds up to the balance: without calls, every change of it is an entry.
func userLedger(t *testing.T, userId int) []ledgerEntry {
	t.Helper()
	rows, total, err := GetUserWalletLedger(userId, WalletLedgerFilter{}, 0, 100)
	require.NoError(t, err)
	require.Equal(t, int64(len(rows)), total)
	entries := make([]ledgerEntry, 0, len(rows))
	var sum int64
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		entries = append(entries, ledgerEntry{row.Type, row.Quota, row.RefId, row.RefName, row.OperatorId})
		sum += row.Quota
	}
	balance, err := GetUserQuota(userId, true)
	require.NoError(t, err)
	assert.Equal(t, int64(balance), sum, "the ledger of user %d adds up to the balance", userId)
	return entries
}

func useWalletBonuses(t *testing.T, newUser, invitee, inviter int) {
	t.Helper()
	savedNewUser, savedInvitee, savedInviter := common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter
	savedCheckin := *operation_setting.GetCheckinSetting()
	common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter = newUser, invitee, inviter
	*operation_setting.GetCheckinSetting() = operation_setting.CheckinSetting{Enabled: true, MinQuota: 7, MaxQuota: 7}
	t.Cleanup(func() {
		common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter = savedNewUser, savedInvitee, savedInviter
		*operation_setting.GetCheckinSetting() = savedCheckin
	})
}

// Every change of a balance other than a call writes a ledger entry with its
// signed amount and reference in the same transaction.
func TestEveryWalletChangeWritesItsLedgerEntry(t *testing.T) {
	truncateTables(t)
	transfer := common.QuotaFromFloat(common.QuotaPerUnit)
	useWalletBonuses(t, 100, 30, transfer)

	inviter := &User{Username: "ledger-inviter", Password: "ledger-password", Status: common.UserStatusEnabled}
	require.NoError(t, inviter.Insert(0))
	invitee := &User{Username: "ledger-invitee", Password: "ledger-password", Status: common.UserStatusEnabled}
	require.NoError(t, invitee.Insert(inviter.Id))

	require.NoError(t, (&User{Id: inviter.Id}).TransferAffQuotaToQuota(transfer))

	redemption := &Redemption{Name: "launch week", Key: "20000000000000000000000000000002", Status: common.RedemptionCodeStatusEnabled, Quota: 500}
	require.NoError(t, DB.Create(redemption).Error)
	_, err := Redeem(redemption.Key, invitee.Id)
	require.NoError(t, err)

	_, err = UserCheckin(invitee.Id)
	require.NoError(t, err)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return CreditWalletTx(tx, invitee.Id, 2000, WalletChange{Type: WalletLedgerTopUp, RefId: "ORDER-1"})
	}))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return DebitWalletTx(tx, invitee.Id, 600, WalletChange{Type: WalletLedgerSubscriptionPurchase, RefId: "ORDER-2", RefName: "Pro"})
	}))

	_, err = AdjustUserQuota(invitee.Id, 9, common.RoleRootUser, "subtract", 37)
	require.NoError(t, err)
	_, err = AdjustUserQuota(invitee.Id, 9, common.RoleRootUser, "override", 5000)
	require.NoError(t, err)
	// An override to the balance it already has changes nothing.
	_, err = AdjustUserQuota(invitee.Id, 9, common.RoleRootUser, "override", 5000)
	require.NoError(t, err)

	assert.Equal(t, []ledgerEntry{
		{Type: WalletLedgerSignupBonus, Quota: 100},
		{Type: WalletLedgerAffTransfer, Quota: int64(transfer)},
	}, userLedger(t, inviter.Id))
	assert.Equal(t, []ledgerEntry{
		{Type: WalletLedgerSignupBonus, Quota: 100},
		{Type: WalletLedgerInviteeBonus, Quota: 30},
		{Type: WalletLedgerRedemption, Quota: 500, RefId: strconv.Itoa(redemption.Id), RefName: "launch week"},
		{Type: WalletLedgerCheckin, Quota: 7, RefId: time.Now().Format(time.DateOnly)},
		{Type: WalletLedgerTopUp, Quota: 2000, RefId: "ORDER-1"},
		{Type: WalletLedgerSubscriptionPurchase, Quota: -600, RefId: "ORDER-2", RefName: "Pro"},
		{Type: WalletLedgerAdminAdjustment, Quota: -37, OperatorId: 9},
		{Type: WalletLedgerAdminAdjustment, Quota: 5000 - 2000, OperatorId: 9},
	}, userLedger(t, invitee.Id))

	entries, total, err := GetUserWalletLedger(invitee.Id, WalletLedgerFilter{Type: WalletLedgerAdminAdjustment}, 0, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "the type filter counts every match")
	require.Len(t, entries, 1)
	assert.Equal(t, int64(3000), entries[0].Quota, "newest first")
}

// A change that fails, or whose transaction rolls back, leaves neither a
// balance change nor a ledger entry.
func TestFailedWalletChangesLeaveNoLedgerEntry(t *testing.T) {
	truncateTables(t)
	useWalletBonuses(t, 0, 0, 0)
	admin := &User{Username: "ledger-admin", AffCode: "lad1", Status: common.UserStatusEnabled, Role: common.RoleAdminUser, Quota: 300}
	require.NoError(t, DB.Create(admin).Error)

	rollback := errors.New("a later step failed")
	err := DB.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, CreditWalletTx(tx, admin.Id, 1000, WalletChange{Type: WalletLedgerTopUp, RefId: "ORDER-3"}))
		return rollback
	})
	require.ErrorIs(t, err, rollback)

	err = DB.Transaction(func(tx *gorm.DB) error {
		return DebitWalletTx(tx, admin.Id, 301, WalletChange{Type: WalletLedgerSubscriptionPurchase, RefId: "ORDER-4"})
	})
	require.ErrorIs(t, err, ErrInsufficientBalance)

	err = DB.Transaction(func(tx *gorm.DB) error {
		return CreditWalletTx(tx, admin.Id, common.MaxWalletQuota, WalletChange{Type: WalletLedgerTopUp, RefId: "ORDER-5"})
	})
	require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded)

	_, err = AdjustUserQuota(admin.Id, 9, common.RoleAdminUser, "add", 10)
	require.ErrorIs(t, err, ErrUserQuotaPermission)

	_, err = UserCheckin(admin.Id)
	require.NoError(t, err)
	_, err = UserCheckin(admin.Id)
	require.Error(t, err, "a second check-in of the day")

	// The fixture's opening balance has no entry of its own.
	require.NoError(t, recordWalletChangeTx(DB, admin.Id, 300, WalletChange{Type: WalletLedgerSignupBonus}))
	assert.Equal(t, []ledgerEntry{
		{Type: WalletLedgerCheckin, Quota: 7, RefId: time.Now().Format(time.DateOnly)},
		{Type: WalletLedgerSignupBonus, Quota: 300},
	}, userLedger(t, admin.Id))
}

// A day's snapshot run records the balances that changed since each user's
// previous snapshot, and a day is recorded once however often it runs.
func TestWalletBalanceSnapshotsRecordChangedBalancesOnce(t *testing.T) {
	truncateTables(t)
	alice := &User{Username: "snapshot-alice", AffCode: "sna1", Status: common.UserStatusEnabled, Quota: 100}
	bob := &User{Username: "snapshot-bob", AffCode: "snb2", Status: common.UserStatusEnabled, Quota: 0}
	gone := &User{Username: "snapshot-gone", AffCode: "snc3", Status: common.UserStatusEnabled, Quota: 70}
	require.NoError(t, DB.Create([]*User{alice, bob, gone}).Error)
	require.NoError(t, DB.Delete(gone).Error)

	setQuota := func(user *User, quota int) {
		require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("quota", quota).Error)
	}
	type snapshot struct {
		UserId int
		Day    string
		Quota  int64
	}
	snapshots := func() []snapshot {
		var rows []snapshot
		require.NoError(t, DB.Model(&WalletBalanceSnapshot{}).Order("day, user_id").Find(&rows).Error)
		return rows
	}

	users, recorded, err := RecordWalletBalanceSnapshots("2026-11-01")
	require.NoError(t, err)
	assert.True(t, recorded)
	assert.Equal(t, int64(2), users, "a user's first run records them; a deleted user is left out")

	setQuota(alice, 150)
	users, recorded, err = RecordWalletBalanceSnapshots("2026-11-01")
	require.NoError(t, err)
	assert.False(t, recorded, "a recorded day keeps its closing balances")
	assert.Zero(t, users)

	users, recorded, err = RecordWalletBalanceSnapshots("2026-11-02")
	require.NoError(t, err)
	assert.True(t, recorded)
	assert.Equal(t, int64(1), users, "only the balance that changed")

	setQuota(alice, 100)
	setQuota(bob, 5)
	_, _, err = RecordWalletBalanceSnapshots("2026-11-03")
	require.NoError(t, err)
	_, _, err = RecordWalletBalanceSnapshots("2026-11-04")
	require.NoError(t, err)

	assert.Equal(t, []snapshot{
		{alice.Id, "2026-11-01", 100}, {bob.Id, "2026-11-01", 0},
		{alice.Id, "2026-11-02", 150},
		{alice.Id, "2026-11-03", 100}, {bob.Id, "2026-11-03", 5},
	}, snapshots())
	var days []WalletBalanceDay
	require.NoError(t, DB.Order("day").Find(&days).Error)
	require.Len(t, days, 4)
	assert.Equal(t, []int64{2, 1, 2, 0}, []int64{days[0].Users, days[1].Users, days[2].Users, days[3].Users})
}

// The statement divides days at midnight in Beijing time: a cost or an entry
// a second before midnight belongs to the day before. A day whose closing
// balances were not recorded has none, a recorded day without a snapshot of
// the user keeps the previous one, and today has the current balance.
func TestWalletStatementDividesDaysInBeijingTime(t *testing.T) {
	truncateTables(t)
	user := &User{Username: "statement-user", AffCode: "stu1", Status: common.UserStatusEnabled, Quota: 1234}
	require.NoError(t, DB.Create(user).Error)
	other := &User{Username: "statement-other", AffCode: "sto2", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(other).Error)

	beijing := func(day, hour, minute, second int) int64 {
		return time.Date(2026, time.November, day, hour, minute, second, 0, WalletDayZone).Unix()
	}
	usage := []QuotaData{
		{UserID: user.Id, ModelName: "gpt-a", CreatedAt: beijing(0, 23, 0, 0), Count: 9, Quota: 900, TokenUsed: 9000}, // October 31
		{UserID: user.Id, ModelName: "gpt-a", CreatedAt: beijing(1, 0, 0, 0), Count: 1, Quota: 10, TokenUsed: 100},
		{UserID: user.Id, ModelName: "gpt-a", CreatedAt: beijing(1, 23, 0, 0), Count: 2, Quota: 40, TokenUsed: 400},
		{UserID: user.Id, ModelName: "gpt-b", CreatedAt: beijing(1, 23, 0, 0), Count: 0, Quota: -15},
		{UserID: user.Id, ModelName: "gpt-a", CreatedAt: beijing(2, 0, 0, 0), Count: 3, Quota: 30, TokenUsed: 300},
		{UserID: other.Id, ModelName: "gpt-a", CreatedAt: beijing(2, 0, 0, 0), Count: 5, Quota: 50, TokenUsed: 500},
	}
	require.NoError(t, DB.Create(&usage).Error)
	entries := []WalletLedgerEntry{
		{UserId: user.Id, Type: WalletLedgerTopUp, Quota: 500, RefId: "ORDER-6", CreatedAt: beijing(1, 23, 59, 59)},
		{UserId: user.Id, Type: WalletLedgerAdminAdjustment, Quota: -20, OperatorId: 9, CreatedAt: beijing(2, 0, 0, 0)},
		{UserId: other.Id, Type: WalletLedgerCheckin, Quota: 7, CreatedAt: beijing(2, 12, 0, 0)},
	}
	require.NoError(t, DB.Create(&entries).Error)
	require.NoError(t, DB.Create(&[]WalletBalanceDay{{Day: "2026-10-31", CreatedAt: 1}, {Day: "2026-11-01", CreatedAt: 1}, {Day: "2026-11-03", CreatedAt: 1}}).Error)
	require.NoError(t, DB.Create(&[]WalletBalanceSnapshot{
		{UserId: user.Id, Day: "2026-10-31", Quota: 1000, CreatedAt: 1},
		{UserId: user.Id, Day: "2026-11-01", Quota: 1465, CreatedAt: 1},
		{UserId: other.Id, Day: "2026-11-03", Quota: 9, CreatedAt: 1},
	}).Error)

	now := time.Date(2026, time.November, 4, 10, 0, 0, 0, WalletDayZone)
	statement, err := GetWalletStatement(user.Id, now, now)
	require.NoError(t, err)
	assert.Equal(t, "2026-11", statement.Month)
	assert.Equal(t, "Asia/Shanghai", statement.TimeZone)
	require.NotNil(t, statement.OpeningBalance)
	assert.Equal(t, int64(1000), *statement.OpeningBalance)

	type day struct {
		Date                            string
		Requests, Tokens, Consumed, Net int64
		Entries                         []string
		Balance                         *int64
	}
	var got []day
	for _, d := range statement.Days {
		var types []string
		for _, entry := range d.Entries {
			types = append(types, entry.Type+" "+strconv.FormatInt(entry.Quota, 10)+" "+entry.RefId)
		}
		got = append(got, day{d.Date, d.Requests, d.Tokens, d.ConsumedQuota, d.NetQuota, types, d.Balance})
	}
	assert.Equal(t, []day{
		{"2026-11-01", 3, 500, 35, 500 - 35, []string{"topup 500 ORDER-6"}, new(int64(1465))},
		{"2026-11-02", 3, 300, 30, -20 - 30, []string{"admin_adjustment -20 "}, nil},
		{"2026-11-03", 0, 0, 0, 0, nil, new(int64(1465))},
		{"2026-11-04", 0, 0, 0, 0, nil, new(int64(1234))},
	}, got)

	october, err := GetWalletStatement(user.Id, time.Date(2026, time.October, 15, 0, 0, 0, 0, WalletDayZone), now)
	require.NoError(t, err)
	require.Len(t, october.Days, 31)
	assert.Nil(t, october.OpeningBalance, "September 30 was not recorded")
	assert.Equal(t, int64(9), october.Days[30].Requests)
	require.NotNil(t, october.Days[30].Balance)
	assert.Equal(t, int64(1000), *october.Days[30].Balance)

	december, err := GetWalletStatement(user.Id, time.Date(2026, time.December, 1, 0, 0, 0, 0, WalletDayZone), now)
	require.NoError(t, err)
	assert.Empty(t, december.Days, "a month after today has no days")
}
