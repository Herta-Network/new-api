package model

import (
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Wallet ledger types. A type is stored as text, so a new kind of balance
// change (a monthly card's daily claim, a Telegram check-in) only adds a
// constant; the console translates each one as an English source string.
const (
	// WalletLedgerTopUp is a paid top-up order; ref_id is its order number.
	WalletLedgerTopUp = "topup"
	// WalletLedgerRedemption is a redemption code; ref_id is its id and
	// ref_name its name, never the code itself.
	WalletLedgerRedemption = "redemption"
	// WalletLedgerAdminAdjustment is an administrator's change; operator_id
	// is the administrator, shown to administrators only.
	WalletLedgerAdminAdjustment = "admin_adjustment"
	// WalletLedgerCheckin is a daily check-in reward; ref_id is the check-in
	// date, a day in WalletDayZone.
	WalletLedgerCheckin = "checkin"
	// WalletLedgerSignupBonus is the quota a new account starts with, the
	// root account's included.
	WalletLedgerSignupBonus = "signup_bonus"
	// WalletLedgerInviteeBonus is the bonus for signing up with an
	// invitation code.
	WalletLedgerInviteeBonus = "invitee_bonus"
	// WalletLedgerAffTransfer moves invitation rewards to the balance.
	WalletLedgerAffTransfer = "aff_transfer"
	// WalletLedgerSubscriptionPurchase is a plan bought with the balance;
	// ref_id is the order number and ref_name the plan's title.
	WalletLedgerSubscriptionPurchase = "subscription_purchase"
)

// walletLedgerRefNameLength is the length of wallet_ledger.ref_name.
const walletLedgerRefNameLength = 128

// WalletDayZone divides the site's days: statements, balance snapshots and
// day-based rewards follow Beijing time, whatever the server's or the
// reader's time zone. China has kept UTC+8 without daylight saving since
// 1991, so a fixed zone needs no time zone database.
var WalletDayZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// WalletDay is the date of t in WalletDayZone, as YYYY-MM-DD.
func WalletDay(t time.Time) string {
	return t.In(WalletDayZone).Format(time.DateOnly)
}

// WalletLedgerEntry is one change of a wallet balance, written in the
// transaction that makes it. Calls make none: their costs are the hourly
// quota_data, which the statement adds up per day.
type WalletLedgerEntry struct {
	Id     int64  `json:"id"`
	UserId int    `json:"user_id" gorm:"not null;index:idx_wallet_ledger_user_created,priority:1"`
	Type   string `json:"type" gorm:"type:varchar(32);not null"`
	// Quota is the signed change of the balance.
	Quota      int64  `json:"quota" gorm:"not null"`
	RefId      string `json:"ref_id" gorm:"type:varchar(64);not null;default:''"`
	RefName    string `json:"ref_name" gorm:"type:varchar(128);not null;default:''"`
	OperatorId int    `json:"operator_id" gorm:"not null;default:0"`
	CreatedAt  int64  `json:"created_at" gorm:"not null;index:idx_wallet_ledger_user_created,priority:2"`
}

func (WalletLedgerEntry) TableName() string {
	return "wallet_ledger"
}

// WalletChange names a balance change in its ledger entry.
type WalletChange struct {
	Type       string
	RefId      string
	RefName    string
	OperatorId int
}

// recordWalletChangeTx writes the ledger entry of a signed change of quota to
// the wallet of userId, in the transaction tx that changes the balance, so
// the entry and the change commit or roll back together.
func recordWalletChangeTx(tx *gorm.DB, userId int, quota int64, change WalletChange) error {
	if quota == 0 {
		return nil
	}
	refName := change.RefName
	if utf8.RuneCountInString(refName) > walletLedgerRefNameLength {
		refName = string([]rune(refName)[:walletLedgerRefNameLength])
	}
	return tx.Create(&WalletLedgerEntry{
		UserId:     userId,
		Type:       change.Type,
		Quota:      quota,
		RefId:      change.RefId,
		RefName:    refName,
		OperatorId: change.OperatorId,
		CreatedAt:  common.GetTimestamp(),
	}).Error
}

// CreateRootUser creates the root account of a new installation with the
// quota it starts with, and that quota's ledger entry, in one transaction.
func CreateRootUser(user *User) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		return recordWalletChangeTx(tx, user.Id, int64(user.Quota), WalletChange{Type: WalletLedgerSignupBonus})
	})
}

// WalletLedgerFilter narrows the ledger an administrator reads; zero fields
// do not filter.
type WalletLedgerFilter struct {
	Type           string
	StartTimestamp int64
	EndTimestamp   int64
}

// GetUserWalletLedger returns a page of the ledger of userId, newest first,
// and the number of entries the filter matches.
func GetUserWalletLedger(userId int, filter WalletLedgerFilter, startIdx int, num int) ([]*WalletLedgerEntry, int64, error) {
	query := DB.Model(&WalletLedgerEntry{}).Where("user_id = ?", userId)
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	if filter.StartTimestamp > 0 {
		query = query.Where("created_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp > 0 {
		query = query.Where("created_at <= ?", filter.EndTimestamp)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	entries := []*WalletLedgerEntry{}
	err := query.Order("created_at DESC, id DESC").Offset(startIdx).Limit(num).Find(&entries).Error
	return entries, total, err
}

// WalletBalanceSnapshot is a user's balance at the end of Day in
// WalletDayZone.
type WalletBalanceSnapshot struct {
	Id        int64  `json:"id"`
	UserId    int    `json:"user_id" gorm:"not null;uniqueIndex:idx_wallet_balance_snapshots_user_day,priority:1"`
	Day       string `json:"day" gorm:"type:varchar(10);not null;uniqueIndex:idx_wallet_balance_snapshots_user_day,priority:2"`
	Quota     int64  `json:"quota" gorm:"not null"`
	CreatedAt int64  `json:"created_at" gorm:"not null"`
}

// WalletBalanceDay marks a day whose closing balances were recorded; Users
// counts the snapshots the run wrote.
type WalletBalanceDay struct {
	Day       string `json:"day" gorm:"type:varchar(10);primaryKey"`
	Users     int64  `json:"users" gorm:"not null;default:0"`
	CreatedAt int64  `json:"created_at" gorm:"not null"`
}

// RecordWalletBalanceSnapshots records the closing balances of day: a
// snapshot for each user, deleted ones aside, whose balance in users.quota
// differs from their latest earlier snapshot. The day's marker is written in
// the same transaction, and a day that has one records nothing again, so a
// repeated or late run never puts a later balance in place of a closing one.
// recorded is false when day was recorded before.
func RecordWalletBalanceSnapshots(day string) (users int64, recorded bool, err error) {
	err = DB.Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		marker := WalletBalanceDay{Day: day, CreatedAt: now}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		recorded = true
		// One statement reads every balance at the same instant.
		result = tx.Exec(`INSERT INTO wallet_balance_snapshots (user_id, day, quota, created_at)
SELECT u.id, ?, u.quota, ? FROM users u
WHERE u.deleted_at IS NULL AND u.quota IS DISTINCT FROM (
	SELECT s.quota FROM wallet_balance_snapshots s
	WHERE s.user_id = u.id AND s.day < ? ORDER BY s.day DESC LIMIT 1)
ON CONFLICT (user_id, day) DO NOTHING`, day, now, day)
		if result.Error != nil {
			return result.Error
		}
		users = result.RowsAffected
		return tx.Model(&marker).Update("users", users).Error
	})
	return users, recorded, err
}

// WalletStatement is a user's month in WalletDayZone: a row for each day up
// to today with the calls, the ledger entries and the closing balance.
type WalletStatement struct {
	// Month is YYYY-MM.
	Month string `json:"month"`
	// TimeZone is the zone the days follow, Asia/Shanghai.
	TimeZone string `json:"time_zone"`
	// OpeningBalance is the closing balance of the previous month's last
	// day; null when it was not recorded.
	OpeningBalance *int64               `json:"opening_balance" extensions:"x-nullable"`
	Days           []WalletStatementDay `json:"days"`
}

// WalletStatementDay is one day of a statement.
type WalletStatementDay struct {
	// Date is YYYY-MM-DD.
	Date string `json:"date"`
	// Requests, Tokens and UsageQuota add up the day's calls, whoever paid
	// them; ConsumedQuota is the part the wallet paid, without the calls a
	// subscription paid. A refunded asynchronous task lowers both quotas.
	Requests      int64                  `json:"requests"`
	Tokens        int64                  `json:"tokens"`
	UsageQuota    int64                  `json:"usage_quota"`
	ConsumedQuota int64                  `json:"consumed_quota"`
	Entries       []WalletStatementEntry `json:"entries"`
	// NetQuota is the day's change: the entries less ConsumedQuota.
	NetQuota int64 `json:"net_quota"`
	// Balance is the balance at the end of the day, the current balance for
	// today; null for a day whose closing balances were not recorded.
	Balance *int64 `json:"balance" extensions:"x-nullable"`
}

// WalletStatementEntry is a ledger entry as its user reads it.
type WalletStatementEntry struct {
	Id        int64  `json:"id"`
	Type      string `json:"type"`
	Quota     int64  `json:"quota"`
	RefId     string `json:"ref_id"`
	RefName   string `json:"ref_name"`
	CreatedAt int64  `json:"created_at"`
}

// GetWalletStatement builds the statement of userId for month (any instant
// in it) as of now. Days follow WalletDayZone; a month after now has no days.
func GetWalletStatement(userId int, month time.Time, now time.Time) (*WalletStatement, error) {
	month = month.In(WalletDayZone)
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, WalletDayZone)
	local := now.In(WalletDayZone)
	end := min(start.AddDate(0, 1, 0).Unix(), time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, WalletDayZone).Unix())
	statement := &WalletStatement{Month: start.Format("2006-01"), TimeZone: WalletDayZone.String(), Days: []WalletStatementDay{}}
	dayIndex := map[string]int{}
	for day := start; day.Unix() < end; day = day.AddDate(0, 0, 1) {
		dayIndex[day.Format(time.DateOnly)] = len(statement.Days)
		statement.Days = append(statement.Days, WalletStatementDay{Date: day.Format(time.DateOnly), Entries: []WalletStatementEntry{}})
	}
	if len(statement.Days) == 0 {
		return statement, nil
	}
	days := statement.Days

	// quota_data rows start on UTC hours, which never straddle a Beijing
	// midnight.
	_, offset := start.Zone()
	var usage []struct {
		DayNumber   int64
		Requests    int64
		Tokens      int64
		Quota       int64
		WalletQuota int64
	}
	if err := DB.Table("quota_data").
		Select("(created_at + ?) / 86400 AS day_number, sum(count) AS requests, sum(token_used) AS tokens, sum(quota) AS quota, sum(wallet_quota) AS wallet_quota", offset).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userId, start.Unix(), end).
		Group("day_number").Scan(&usage).Error; err != nil {
		return nil, err
	}
	for _, row := range usage {
		i, ok := dayIndex[WalletDay(time.Unix(row.DayNumber*86400-int64(offset), 0))]
		if !ok {
			continue
		}
		days[i].Requests, days[i].Tokens, days[i].UsageQuota, days[i].ConsumedQuota = row.Requests, row.Tokens, row.Quota, row.WalletQuota
		days[i].NetQuota -= row.WalletQuota
	}

	var entries []WalletLedgerEntry
	if err := DB.Where("user_id = ? AND created_at >= ? AND created_at < ?", userId, start.Unix(), end).
		Order("created_at, id").Find(&entries).Error; err != nil {
		return nil, err
	}
	for _, entry := range entries {
		i, ok := dayIndex[WalletDay(time.Unix(entry.CreatedAt, 0))]
		if !ok {
			continue
		}
		days[i].Entries = append(days[i].Entries, WalletStatementEntry{
			Id: entry.Id, Type: entry.Type, Quota: entry.Quota, RefId: entry.RefId, RefName: entry.RefName, CreatedAt: entry.CreatedAt,
		})
		days[i].NetQuota += entry.Quota
	}

	// A recorded day's closing balance is the user's latest snapshot up to
	// it: the run writes none for a balance that did not change.
	previousDay := start.AddDate(0, 0, -1).Format(time.DateOnly)
	lastDay := days[len(days)-1].Date
	var recordedDays []string
	if err := DB.Model(&WalletBalanceDay{}).Where("day >= ? AND day <= ?", previousDay, lastDay).
		Pluck("day", &recordedDays).Error; err != nil {
		return nil, err
	}
	recorded := make(map[string]bool, len(recordedDays))
	for _, day := range recordedDays {
		recorded[day] = true
	}
	var snapshots []WalletBalanceSnapshot
	if err := DB.Where("user_id = ? AND day <= ?", userId, previousDay).Order("day DESC").Limit(1).
		Find(&snapshots).Error; err != nil {
		return nil, err
	}
	var monthSnapshots []WalletBalanceSnapshot
	if err := DB.Where("user_id = ? AND day > ? AND day <= ?", userId, previousDay, lastDay).Order("day").
		Find(&monthSnapshots).Error; err != nil {
		return nil, err
	}
	var balance *int64
	if len(snapshots) == 1 {
		balance = &snapshots[0].Quota
	}
	if recorded[previousDay] && balance != nil {
		statement.OpeningBalance = new(*balance)
	}
	today := WalletDay(now)
	next := 0
	for i := range days {
		for next < len(monthSnapshots) && monthSnapshots[next].Day <= days[i].Date {
			balance = &monthSnapshots[next].Quota
			next++
		}
		if days[i].Date == today {
			current, err := GetUserQuota(userId, true)
			if err != nil {
				return nil, err
			}
			days[i].Balance = new(int64(current))
			continue
		}
		if recorded[days[i].Date] && balance != nil {
			days[i].Balance = new(*balance)
		}
	}
	return statement, nil
}
