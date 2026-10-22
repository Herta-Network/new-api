package model

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// The statistics of the admin operations overview. Every bucket is a local
// hour, day or month in the location of the time the caller passes, the
// server's own zone in production. The bucket bounds are computed here and
// handed to PostgreSQL as width_bucket thresholds, so a daylight saving day
// keeps its real length and no zone name has to be known to the database.
//
// Revenue counts orders a gateway (or an administrator, by hand) got paid
// for, at the time they were paid. What the wallet balance paid for is
// reported apart: the top-up that funded it was the revenue. Amounts are
// hundredths of a currency and are only added within one currency.

// paymentPaidTimeSQL is when an order was paid; an order that reached
// Finished without a paid_at counts from its finish.
const paymentPaidTimeSQL = "CASE WHEN paid_at > 0 THEN paid_at ELSE finished_at END"

// paymentPaidInRangeSQL selects orders paid in [start, end); it takes the
// bounds twice, and keeps paid_at alone on one side so the index on
// (status, paid_at) serves it.
const paymentPaidInRangeSQL = "((paid_at >= ? AND paid_at < ?) OR (paid_at = 0 AND finished_at >= ? AND finished_at < ?))"

// paidPaymentStatuses are the statuses of an order whose payment arrived.
var paidPaymentStatuses = []int{PaymentOrderPaid, PaymentOrderFinished}

// ConsoleOverview is the operations overview of today.
type ConsoleOverview struct {
	TimeZone  string `json:"time_zone"`
	UTCOffset int    `json:"utc_offset"`

	// Usage today, from the hourly dashboard data, which is only recorded
	// while UsageDataEnabled.
	UsageDataEnabled bool  `json:"usage_data_enabled"`
	Requests         int64 `json:"requests"`
	Quota            int64 `json:"quota"`
	Tokens           int64 `json:"tokens"`
	ActiveUsers      int64 `json:"active_users"`
	Registrations    int64 `json:"registrations"`

	// Revenue today in Currency, the site currency.
	Currency      string `json:"currency"`
	Revenue       int64  `json:"revenue"`
	RevenueOrders int64  `json:"revenue_orders"`

	Channels  ConsoleChannelCounts `json:"channels"`
	Attention ConsoleAttention     `json:"attention"`
}

type ConsoleChannelCounts struct {
	Total            int64 `json:"total"`
	Enabled          int64 `json:"enabled"`
	ManuallyDisabled int64 `json:"manually_disabled"`
	AutoDisabled     int64 `json:"auto_disabled"`
}

// ConsoleAttention is the money waiting for an operator: paid orders whose
// fulfilment failed and unresolved payments that settled no order. The
// payment record of a failed fulfilment is counted once, as its order.
type ConsoleAttention struct {
	Count             int64 `json:"count"`
	UnfulfilledOrders int64 `json:"unfulfilled_orders"`
	UnmatchedPayments int64 `json:"unmatched_payments"`
	// Amounts has one total per currency, the site currency first.
	Amounts []ConsoleAmount `json:"amounts"`
}

type ConsoleAmount struct {
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
}

// ConsoleRevenue is the revenue of today by hour, this month by day and all
// time by month.
type ConsoleRevenue struct {
	TimeZone  string               `json:"time_zone"`
	UTCOffset int                  `json:"utc_offset"`
	Currency  string               `json:"currency"`
	Today     ConsoleRevenuePeriod `json:"today"`
	Monthly   ConsoleRevenuePeriod `json:"monthly"`
	All       ConsoleRevenuePeriod `json:"all"`
}

type ConsoleRevenuePeriod struct {
	// Total is Topup plus Subscription, in the site currency.
	Total        int64 `json:"total"`
	Topup        int64 `json:"topup"`
	Subscription int64 `json:"subscription"`
	Orders       int64 `json:"orders"`
	// Manual is the part of Total that administrators completed by hand.
	Manual       int64 `json:"manual"`
	ManualOrders int64 `json:"manual_orders"`
	// BalanceSpending is what the wallet balance paid for; it is not revenue.
	BalanceSpending int64 `json:"balance_spending"`
	BalanceOrders   int64 `json:"balance_orders"`
	// OtherCurrencies holds the orders priced in another currency, such as
	// a former site currency, each currency on its own.
	OtherCurrencies []ConsoleCurrencyRevenue `json:"other_currencies"`
	List            []ConsoleRevenueBucket   `json:"list"`
}

type ConsoleCurrencyRevenue struct {
	Currency        string `json:"currency"`
	Revenue         int64  `json:"revenue"`
	Orders          int64  `json:"orders"`
	BalanceSpending int64  `json:"balance_spending"`
	BalanceOrders   int64  `json:"balance_orders"`
}

type ConsoleRevenueBucket struct {
	Date         string `json:"date"`
	Start        int64  `json:"start"`
	Total        int64  `json:"total"`
	Topup        int64  `json:"topup"`
	Subscription int64  `json:"subscription"`
}

// ConsoleUsers counts registrations and paying users over the same periods
// as ConsoleRevenue. A first-time payer made their first paid gateway order
// in the bucket; a returning payer paid in the bucket after paying before it.
type ConsoleUsers struct {
	TimeZone  string            `json:"time_zone"`
	UTCOffset int               `json:"utc_offset"`
	Today     ConsoleUserPeriod `json:"today"`
	Monthly   ConsoleUserPeriod `json:"monthly"`
	All       ConsoleUserPeriod `json:"all"`
}

type ConsoleUserPeriod struct {
	Registrations   int64               `json:"registrations"`
	FirstTimePayers int64               `json:"first_time_payers"`
	ReturningPayers int64               `json:"returning_payers"`
	List            []ConsoleUserBucket `json:"list"`
}

type ConsoleUserBucket struct {
	Date            string `json:"date"`
	Start           int64  `json:"start"`
	Registrations   int64  `json:"registrations"`
	FirstTimePayers int64  `json:"first_time_payers"`
	ReturningPayers int64  `json:"returning_payers"`
}

// ConsoleRankings are the models and users that consumed the most quota on
// one day.
type ConsoleRankings struct {
	TimeZone  string             `json:"time_zone"`
	UTCOffset int                `json:"utc_offset"`
	Period    string             `json:"period"`
	Start     int64              `json:"start"`
	End       int64              `json:"end"`
	Models    []ConsoleModelRank `json:"models"`
	Users     []ConsoleUserRank  `json:"users"`
}

type ConsoleModelRank struct {
	ModelName string `json:"model_name"`
	Quota     int64  `json:"quota"`
	Requests  int64  `json:"requests"`
	Tokens    int64  `json:"tokens"`
}

type ConsoleUserRank struct {
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
	Quota    int64  `json:"quota"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

// Ranking periods.
const (
	ConsoleRankingToday     = "today"
	ConsoleRankingYesterday = "yesterday"
)

const consoleRankingLimit = 10

// consoleBuckets is a run of consecutive local hours, days or months: bucket
// i spans [starts[i], starts[i+1]).
type consoleBuckets struct {
	starts []int64
	labels []string
}

func (b consoleBuckets) start() int64 { return b.starts[0] }
func (b consoleBuckets) end() int64   { return b.starts[len(b.starts)-1] }

// index is the SQL bucket number, from 1, of a column holding a unix time
// inside the buckets.
func (b consoleBuckets) index(column string) any {
	thresholds := make([]any, len(b.starts))
	for i, start := range b.starts {
		thresholds[i] = start
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(thresholds)), ",")
	return gorm.Expr("width_bucket("+column+", ARRAY["+placeholders+"]::bigint[])", thresholds...)
}

// consoleSpan builds the buckets from first, stepping with next, up to the
// one that holds now.
func consoleSpan(first, now time.Time, layout string, next func(time.Time) time.Time) consoleBuckets {
	var b consoleBuckets
	at := first
	for ; !at.After(now); at = next(at) {
		b.starts = append(b.starts, at.Unix())
		b.labels = append(b.labels, at.Format(layout))
	}
	b.starts = append(b.starts, at.Unix())
	return b
}

func localDayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func localMonthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func consoleTodayBuckets(now time.Time) consoleBuckets {
	return consoleSpan(localDayStart(now), now, "2006-01-02 15:04", func(t time.Time) time.Time { return t.Add(time.Hour) })
}

func consoleMonthBuckets(now time.Time) consoleBuckets {
	return consoleSpan(localMonthStart(now), now, "2006-01-02", func(t time.Time) time.Time { return t.AddDate(0, 0, 1) })
}

// consoleAllBuckets spans the months from the one holding first, a unix time
// (0 when there is no data), to the current one.
func consoleAllBuckets(now time.Time, first int64) consoleBuckets {
	from := localMonthStart(now)
	if first > 0 && first < from.Unix() {
		from = localMonthStart(time.Unix(first, 0).In(now.Location()))
	}
	return consoleSpan(from, now, "2006-01", func(t time.Time) time.Time { return t.AddDate(0, 1, 0) })
}

func consoleZone(now time.Time) (string, int) {
	_, offset := now.Zone()
	return now.Location().String(), offset
}

// gatewayPaidOrders selects the paid orders that are revenue: every
// platform but the wallet balance.
func gatewayPaidOrders() *gorm.DB {
	return DB.Model(&PaymentOrder{}).Where("status IN ? AND platform <> ?", paidPaymentStatuses, PaymentPlatformBalance)
}

// GetConsoleOverview reports today, as of now, with revenue in currency.
func GetConsoleOverview(now time.Time, currency string) (*ConsoleOverview, error) {
	dayStart := localDayStart(now)
	start, end := dayStart.Unix(), dayStart.AddDate(0, 0, 1).Unix()
	overview := &ConsoleOverview{UsageDataEnabled: common.DataExportEnabled, Currency: currency}
	overview.TimeZone, overview.UTCOffset = consoleZone(now)

	var usage struct {
		Requests    int64
		Quota       int64
		Tokens      int64
		ActiveUsers int64
	}
	if err := DB.Model(&QuotaData{}).
		Select("coalesce(sum(count), 0) AS requests, coalesce(sum(quota), 0) AS quota, coalesce(sum(token_used), 0) AS tokens, count(DISTINCT user_id) AS active_users").
		Where("created_at >= ? AND created_at < ?", start, end).
		Scan(&usage).Error; err != nil {
		return nil, err
	}
	overview.Requests, overview.Quota, overview.Tokens, overview.ActiveUsers = usage.Requests, usage.Quota, usage.Tokens, usage.ActiveUsers
	// A deleted account still registered.
	if err := DB.Unscoped().Model(&User{}).Where("created_at >= ? AND created_at < ?", start, end).
		Count(&overview.Registrations).Error; err != nil {
		return nil, err
	}
	var revenue struct {
		Amount int64
		Orders int64
	}
	if err := gatewayPaidOrders().
		Select("coalesce(sum(amount), 0) AS amount, count(*) AS orders").
		Where("upper(currency) = ?", currency).
		Where(paymentPaidInRangeSQL, start, end, start, end).
		Scan(&revenue).Error; err != nil {
		return nil, err
	}
	overview.Revenue, overview.RevenueOrders = revenue.Amount, revenue.Orders

	var channels []struct {
		Status int
		Count  int64
	}
	if err := DB.Model(&Channel{}).Select("status, count(*) AS count").Group("status").Scan(&channels).Error; err != nil {
		return nil, err
	}
	for _, row := range channels {
		overview.Channels.Total += row.Count
		switch row.Status {
		case common.ChannelStatusEnabled:
			overview.Channels.Enabled += row.Count
		case common.ChannelStatusManuallyDisabled:
			overview.Channels.ManuallyDisabled += row.Count
		case common.ChannelStatusAutoDisabled:
			overview.Channels.AutoDisabled += row.Count
		}
	}

	type currencyTotal struct {
		Currency string
		Count    int64
		Amount   int64
	}
	var unfulfilled, unmatched []currencyTotal
	if err := DB.Model(&PaymentOrder{}).Select("currency, count(*) AS count, coalesce(sum(amount), 0) AS amount").
		Where("status = ?", PaymentOrderPaid).Group("currency").Scan(&unfulfilled).Error; err != nil {
		return nil, err
	}
	if err := DB.Model(&PaymentUnmatched{}).Select("currency, count(*) AS count, coalesce(sum(amount), 0) AS amount").
		Where("resolved_at = 0").
		Where("NOT EXISTS (SELECT 1 FROM payment_orders WHERE payment_orders.order_no = payment_unmatched.order_no AND payment_orders.trade_no = payment_unmatched.trade_no AND payment_orders.status = ?)", PaymentOrderPaid).
		Group("currency").Scan(&unmatched).Error; err != nil {
		return nil, err
	}
	amounts := map[string]int64{}
	for _, row := range unfulfilled {
		overview.Attention.UnfulfilledOrders += row.Count
		amounts[strings.ToUpper(row.Currency)] += row.Amount
	}
	for _, row := range unmatched {
		overview.Attention.UnmatchedPayments += row.Count
		amounts[strings.ToUpper(row.Currency)] += row.Amount
	}
	overview.Attention.Count = overview.Attention.UnfulfilledOrders + overview.Attention.UnmatchedPayments
	overview.Attention.Amounts = []ConsoleAmount{}
	for code, amount := range amounts {
		overview.Attention.Amounts = append(overview.Attention.Amounts, ConsoleAmount{Currency: code, Amount: amount})
	}
	slices.SortFunc(overview.Attention.Amounts, func(a, b ConsoleAmount) int {
		if (a.Currency == currency) != (b.Currency == currency) {
			if a.Currency == currency {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Currency, b.Currency)
	})
	return overview, nil
}

// GetConsoleRevenue reports the revenue as of now in currency, the site
// currency, and the orders in any other currency apart.
func GetConsoleRevenue(now time.Time, currency string) (*ConsoleRevenue, error) {
	revenue := &ConsoleRevenue{Currency: currency}
	revenue.TimeZone, revenue.UTCOffset = consoleZone(now)
	var first int64
	if err := DB.Model(&PaymentOrder{}).Select("coalesce(min("+paymentPaidTimeSQL+"), 0)").
		Where("status IN ?", paidPaymentStatuses).Scan(&first).Error; err != nil {
		return nil, err
	}
	var err error
	if revenue.Today, err = consoleRevenuePeriod(consoleTodayBuckets(now), currency); err != nil {
		return nil, err
	}
	if revenue.Monthly, err = consoleRevenuePeriod(consoleMonthBuckets(now), currency); err != nil {
		return nil, err
	}
	if revenue.All, err = consoleRevenuePeriod(consoleAllBuckets(now, first), currency); err != nil {
		return nil, err
	}
	return revenue, nil
}

func consoleRevenuePeriod(buckets consoleBuckets, currency string) (ConsoleRevenuePeriod, error) {
	var rows []struct {
		Bucket   int
		Purpose  string
		Currency string
		Balance  bool
		Manual   bool
		Orders   int64
		Amount   int64
	}
	start, end := buckets.start(), buckets.end()
	err := DB.Model(&PaymentOrder{}).
		Select("? AS bucket, purpose, currency, platform = ? AS balance, trade_no LIKE ? AS manual, count(*) AS orders, coalesce(sum(amount), 0) AS amount",
			buckets.index(paymentPaidTimeSQL), PaymentPlatformBalance, ManualTradeNo("")+"%").
		Where("status IN ?", paidPaymentStatuses).
		Where(paymentPaidInRangeSQL, start, end, start, end).
		Group("bucket, purpose, currency, balance, manual").
		Scan(&rows).Error
	if err != nil {
		return ConsoleRevenuePeriod{}, err
	}

	period := ConsoleRevenuePeriod{
		OtherCurrencies: []ConsoleCurrencyRevenue{},
		List:            make([]ConsoleRevenueBucket, len(buckets.labels)),
	}
	for i, label := range buckets.labels {
		period.List[i] = ConsoleRevenueBucket{Date: label, Start: buckets.starts[i]}
	}
	others := map[string]*ConsoleCurrencyRevenue{}
	for _, row := range rows {
		code := strings.ToUpper(row.Currency)
		if code != currency {
			other := others[code]
			if other == nil {
				other = &ConsoleCurrencyRevenue{Currency: code}
				others[code] = other
			}
			if row.Balance {
				other.BalanceSpending += row.Amount
				other.BalanceOrders += row.Orders
			} else {
				other.Revenue += row.Amount
				other.Orders += row.Orders
			}
			continue
		}
		if row.Balance {
			period.BalanceSpending += row.Amount
			period.BalanceOrders += row.Orders
			continue
		}
		bucket := &period.List[row.Bucket-1]
		bucket.Total += row.Amount
		period.Total += row.Amount
		period.Orders += row.Orders
		switch row.Purpose {
		case PaymentPurposeTopup:
			bucket.Topup += row.Amount
			period.Topup += row.Amount
		case PaymentPurposeSubscription:
			bucket.Subscription += row.Amount
			period.Subscription += row.Amount
		}
		if row.Manual {
			period.Manual += row.Amount
			period.ManualOrders += row.Orders
		}
	}
	for _, other := range others {
		period.OtherCurrencies = append(period.OtherCurrencies, *other)
	}
	slices.SortFunc(period.OtherCurrencies, func(a, b ConsoleCurrencyRevenue) int { return cmp.Compare(a.Currency, b.Currency) })
	return period, nil
}

// GetConsoleUsers reports registrations and paying users as of now.
func GetConsoleUsers(now time.Time) (*ConsoleUsers, error) {
	users := &ConsoleUsers{}
	users.TimeZone, users.UTCOffset = consoleZone(now)
	var firstRegistration, firstPayment int64
	if err := DB.Unscoped().Model(&User{}).Select("coalesce(min(created_at), 0)").Where("created_at > 0").
		Scan(&firstRegistration).Error; err != nil {
		return nil, err
	}
	if err := gatewayPaidOrders().Select("coalesce(min(" + paymentPaidTimeSQL + "), 0)").Scan(&firstPayment).Error; err != nil {
		return nil, err
	}
	first := firstRegistration
	if first == 0 || (firstPayment > 0 && firstPayment < first) {
		first = firstPayment
	}
	var err error
	if users.Today, err = consoleUserPeriod(consoleTodayBuckets(now)); err != nil {
		return nil, err
	}
	if users.Monthly, err = consoleUserPeriod(consoleMonthBuckets(now)); err != nil {
		return nil, err
	}
	if users.All, err = consoleUserPeriod(consoleAllBuckets(now, first)); err != nil {
		return nil, err
	}
	return users, nil
}

func consoleUserPeriod(buckets consoleBuckets) (ConsoleUserPeriod, error) {
	type bucketCount struct {
		Bucket int
		Count  int64
	}
	start, end := buckets.start(), buckets.end()
	var registrations, payers, firstPayers []bucketCount
	if err := DB.Unscoped().Model(&User{}).Select("? AS bucket, count(*) AS count", buckets.index("created_at")).
		Where("created_at >= ? AND created_at < ?", start, end).
		Group("bucket").Scan(&registrations).Error; err != nil {
		return ConsoleUserPeriod{}, err
	}
	if err := gatewayPaidOrders().Select("? AS bucket, count(DISTINCT user_id) AS count", buckets.index(paymentPaidTimeSQL)).
		Where(paymentPaidInRangeSQL, start, end, start, end).
		Group("bucket").Scan(&payers).Error; err != nil {
		return ConsoleUserPeriod{}, err
	}
	firstPaid := gatewayPaidOrders().Select("user_id, min(" + paymentPaidTimeSQL + ") AS first_paid").Group("user_id")
	if err := DB.Table("(?) AS firsts", firstPaid).Select("? AS bucket, count(*) AS count", buckets.index("first_paid")).
		Where("first_paid >= ? AND first_paid < ?", start, end).
		Group("bucket").Scan(&firstPayers).Error; err != nil {
		return ConsoleUserPeriod{}, err
	}
	var periodPayers int64
	if err := gatewayPaidOrders().Where(paymentPaidInRangeSQL, start, end, start, end).
		Distinct("user_id").Count(&periodPayers).Error; err != nil {
		return ConsoleUserPeriod{}, err
	}

	period := ConsoleUserPeriod{List: make([]ConsoleUserBucket, len(buckets.labels))}
	for i, label := range buckets.labels {
		period.List[i] = ConsoleUserBucket{Date: label, Start: buckets.starts[i]}
	}
	for _, row := range registrations {
		period.List[row.Bucket-1].Registrations = row.Count
		period.Registrations += row.Count
	}
	// Whoever paid in a bucket for the first time is a first-time payer
	// there; the others had paid before it.
	for _, row := range payers {
		period.List[row.Bucket-1].ReturningPayers = row.Count
	}
	for _, row := range firstPayers {
		period.List[row.Bucket-1].FirstTimePayers = row.Count
		period.List[row.Bucket-1].ReturningPayers -= row.Count
		period.FirstTimePayers += row.Count
	}
	period.ReturningPayers = periodPayers - period.FirstTimePayers
	return period, nil
}

// GetConsoleRankings ranks the models and users of today, or of yesterday,
// by consumed quota.
func GetConsoleRankings(now time.Time, period string) (*ConsoleRankings, error) {
	day := localDayStart(now)
	if period == ConsoleRankingYesterday {
		day = day.AddDate(0, 0, -1)
	}
	rankings := &ConsoleRankings{Period: period, Start: day.Unix(), End: day.AddDate(0, 0, 1).Unix()}
	rankings.TimeZone, rankings.UTCOffset = consoleZone(now)
	usage := "coalesce(sum(quota), 0) AS quota, coalesce(sum(count), 0) AS requests, coalesce(sum(token_used), 0) AS tokens"
	if err := DB.Model(&QuotaData{}).Select("model_name, "+usage).
		Where("created_at >= ? AND created_at < ?", rankings.Start, rankings.End).
		Group("model_name").Order("quota DESC, model_name").Limit(consoleRankingLimit).
		Scan(&rankings.Models).Error; err != nil {
		return nil, err
	}
	if err := DB.Model(&QuotaData{}).Select("user_id, max(username) AS username, "+usage).
		Where("created_at >= ? AND created_at < ?", rankings.Start, rankings.End).
		Group("user_id").Order("quota DESC, user_id").Limit(consoleRankingLimit).
		Scan(&rankings.Users).Error; err != nil {
		return nil, err
	}
	if rankings.Models == nil {
		rankings.Models = []ConsoleModelRank{}
	}
	if rankings.Users == nil {
		rankings.Users = []ConsoleUserRank{}
	}
	return rankings, nil
}
