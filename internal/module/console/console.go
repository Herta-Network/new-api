// Package console is the admin operations overview: today's usage,
// revenue and channels, revenue and paying users over time, and the
// rankings of models and users. It is a read-only module.
package console

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/currency"
	"github.com/QuantumNous/new-api/internal/module/payment"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// The statistics of the admin operations overview. Every bucket is a local
// hour, day or month in the location of the time the caller passes, the
// server's own zone in production. The bucket bounds are computed here and
// handed to PostgreSQL as width_bucket thresholds, so a daylight saving day
// keeps its real length and no zone name has to be known to the database.
//
// Usage, registrations and channels are read from the shared kernel's
// tables; revenue and payers come from the payment module, which counts
// what gateways got paid for and reports balance purchases apart. Amounts
// are hundredths of a currency and are only added within one currency,
// except the overview's revenue of today, which converts other currencies
// into the site currency at the current rate.

// Overview is the operations overview of today.
type Overview struct {
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

	// Revenue today in Currency, the site currency. Revenue priced in
	// another currency, such as the site currency before a change, is
	// converted at the current rate of its pair.
	Currency      string `json:"currency"`
	Revenue       int64  `json:"revenue"`
	RevenueOrders int64  `json:"revenue_orders"`
	// OtherRevenue is the revenue today in a currency without a rate to
	// Currency, one total per currency; it is not added in.
	OtherRevenue []Amount `json:"other_revenue"`

	Channels  ChannelCounts `json:"channels"`
	Attention Attention     `json:"attention"`
}

type ChannelCounts struct {
	Total            int64 `json:"total"`
	Enabled          int64 `json:"enabled"`
	ManuallyDisabled int64 `json:"manually_disabled"`
	AutoDisabled     int64 `json:"auto_disabled"`
}

// Attention is the money waiting for an operator: paid orders whose
// fulfilment failed and unresolved payments that settled no order. The
// payment record of a failed fulfilment is counted once, as its order.
type Attention struct {
	Count             int64 `json:"count"`
	UnfulfilledOrders int64 `json:"unfulfilled_orders"`
	UnmatchedPayments int64 `json:"unmatched_payments"`
	// Amounts has one total per currency, the site currency first.
	Amounts []Amount `json:"amounts"`
}

type Amount struct {
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
}

// Revenue is the revenue of today by hour, this month by day and all
// time by month.
type Revenue struct {
	TimeZone  string        `json:"time_zone"`
	UTCOffset int           `json:"utc_offset"`
	Currency  string        `json:"currency"`
	Today     RevenuePeriod `json:"today"`
	Monthly   RevenuePeriod `json:"monthly"`
	All       RevenuePeriod `json:"all"`
}

type RevenuePeriod struct {
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
	OtherCurrencies []CurrencyRevenue `json:"other_currencies"`
	List            []RevenueBucket   `json:"list"`
}

type CurrencyRevenue struct {
	Currency        string `json:"currency"`
	Revenue         int64  `json:"revenue"`
	Orders          int64  `json:"orders"`
	BalanceSpending int64  `json:"balance_spending"`
	BalanceOrders   int64  `json:"balance_orders"`
}

type RevenueBucket struct {
	Date         string `json:"date"`
	Start        int64  `json:"start"`
	Total        int64  `json:"total"`
	Topup        int64  `json:"topup"`
	Subscription int64  `json:"subscription"`
}

// Users counts registrations and paying users over the same periods
// as Revenue. A first-time payer made their first paid gateway order
// in the bucket; a returning payer paid in the bucket after paying before it.
type Users struct {
	TimeZone  string     `json:"time_zone"`
	UTCOffset int        `json:"utc_offset"`
	Today     UserPeriod `json:"today"`
	Monthly   UserPeriod `json:"monthly"`
	All       UserPeriod `json:"all"`
}

type UserPeriod struct {
	Registrations   int64        `json:"registrations"`
	FirstTimePayers int64        `json:"first_time_payers"`
	ReturningPayers int64        `json:"returning_payers"`
	List            []UserBucket `json:"list"`
}

type UserBucket struct {
	Date            string `json:"date"`
	Start           int64  `json:"start"`
	Registrations   int64  `json:"registrations"`
	FirstTimePayers int64  `json:"first_time_payers"`
	ReturningPayers int64  `json:"returning_payers"`
}

// Rankings are the models and users that consumed the most quota on
// one day.
type Rankings struct {
	TimeZone  string      `json:"time_zone"`
	UTCOffset int         `json:"utc_offset"`
	Period    string      `json:"period"`
	Start     int64       `json:"start"`
	End       int64       `json:"end"`
	Models    []ModelRank `json:"models"`
	Users     []UserRank  `json:"users"`
}

type ModelRank struct {
	ModelName string `json:"model_name"`
	Quota     int64  `json:"quota"`
	Requests  int64  `json:"requests"`
	Tokens    int64  `json:"tokens"`
}

type UserRank struct {
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
	Quota    int64  `json:"quota"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

// Ranking periods.
const (
	RankingToday     = "today"
	RankingYesterday = "yesterday"
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

// GetOverview reports today, as of now, with revenue in site, the site
// currency.
func GetOverview(ctx context.Context, now time.Time, site string) (*Overview, error) {
	dayStart := localDayStart(now)
	start, end := dayStart.Unix(), dayStart.AddDate(0, 0, 1).Unix()
	overview := &Overview{UsageDataEnabled: common.DataExportEnabled, Currency: site}
	overview.TimeZone, overview.UTCOffset = consoleZone(now)

	var usage struct {
		Requests    int64
		Quota       int64
		Tokens      int64
		ActiveUsers int64
	}
	if err := model.DB.Model(&model.QuotaData{}).
		Select("coalesce(sum(count), 0) AS requests, coalesce(sum(quota), 0) AS quota, coalesce(sum(token_used), 0) AS tokens, count(DISTINCT user_id) AS active_users").
		Where("created_at >= ? AND created_at < ?", start, end).
		Scan(&usage).Error; err != nil {
		return nil, err
	}
	overview.Requests, overview.Quota, overview.Tokens, overview.ActiveUsers = usage.Requests, usage.Quota, usage.Tokens, usage.ActiveUsers
	// A deleted account still registered.
	if err := model.DB.Unscoped().Model(&model.User{}).Where("created_at >= ? AND created_at < ?", start, end).
		Count(&overview.Registrations).Error; err != nil {
		return nil, err
	}
	revenue, err := payment.GatewayRevenue(start, end)
	if err != nil {
		return nil, err
	}
	totals := map[string]payment.CurrencyTotal{}
	for _, row := range revenue {
		code := strings.ToUpper(row.Currency)
		total := totals[code]
		total.Count += row.Count
		total.Amount += row.Amount
		totals[code] = total
	}
	overview.OtherRevenue = []Amount{}
	for _, code := range slices.Sorted(maps.Keys(totals)) {
		total := totals[code]
		amount := total.Amount
		if code != site {
			rate, err := currency.ResolveRate(ctx, code, site)
			if err != nil {
				overview.OtherRevenue = append(overview.OtherRevenue, Amount{Currency: code, Amount: amount})
				continue
			}
			amount = rate.Convert(decimal.NewFromInt(amount)).Round(0).IntPart()
		}
		overview.Revenue += amount
		overview.RevenueOrders += total.Count
	}

	var channels []struct {
		Status int
		Count  int64
	}
	if err := model.DB.Model(&model.Channel{}).Select("status, count(*) AS count").Group("status").Scan(&channels).Error; err != nil {
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

	unfulfilled, unmatched, err := payment.Attention()
	if err != nil {
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
	overview.Attention.Amounts = []Amount{}
	for code, amount := range amounts {
		overview.Attention.Amounts = append(overview.Attention.Amounts, Amount{Currency: code, Amount: amount})
	}
	slices.SortFunc(overview.Attention.Amounts, func(a, b Amount) int {
		if (a.Currency == site) != (b.Currency == site) {
			if a.Currency == site {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Currency, b.Currency)
	})
	return overview, nil
}

// GetRevenue reports the revenue as of now in currency, the site
// currency, and the orders in any other currency apart.
func GetRevenue(now time.Time, currency string) (*Revenue, error) {
	revenue := &Revenue{Currency: currency}
	revenue.TimeZone, revenue.UTCOffset = consoleZone(now)
	first, err := payment.FirstPaidAt(false)
	if err != nil {
		return nil, err
	}
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

func consoleRevenuePeriod(buckets consoleBuckets, currency string) (RevenuePeriod, error) {
	rows, err := payment.RevenueByBucket(buckets.starts)
	if err != nil {
		return RevenuePeriod{}, err
	}

	period := RevenuePeriod{
		OtherCurrencies: []CurrencyRevenue{},
		List:            make([]RevenueBucket, len(buckets.labels)),
	}
	for i, label := range buckets.labels {
		period.List[i] = RevenueBucket{Date: label, Start: buckets.starts[i]}
	}
	others := map[string]*CurrencyRevenue{}
	for _, row := range rows {
		code := strings.ToUpper(row.Currency)
		if code != currency {
			other := others[code]
			if other == nil {
				other = &CurrencyRevenue{Currency: code}
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
		case entity.PaymentPurposeTopup:
			bucket.Topup += row.Amount
			period.Topup += row.Amount
		case entity.PaymentPurposeSubscription:
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
	slices.SortFunc(period.OtherCurrencies, func(a, b CurrencyRevenue) int { return cmp.Compare(a.Currency, b.Currency) })
	return period, nil
}

// GetUsers reports registrations and paying users as of now.
func GetUsers(now time.Time) (*Users, error) {
	users := &Users{}
	users.TimeZone, users.UTCOffset = consoleZone(now)
	var firstRegistration int64
	if err := model.DB.Unscoped().Model(&model.User{}).Select("coalesce(min(created_at), 0)").Where("created_at > 0").
		Scan(&firstRegistration).Error; err != nil {
		return nil, err
	}
	firstPayment, err := payment.FirstPaidAt(true)
	if err != nil {
		return nil, err
	}
	first := firstRegistration
	if first == 0 || (firstPayment > 0 && firstPayment < first) {
		first = firstPayment
	}
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

func consoleUserPeriod(buckets consoleBuckets) (UserPeriod, error) {
	type bucketCount struct {
		Bucket int
		Count  int64
	}
	start, end := buckets.start(), buckets.end()
	var registrations []bucketCount
	if err := model.DB.Unscoped().Model(&model.User{}).Select("? AS bucket, count(*) AS count", buckets.index("created_at")).
		Where("created_at >= ? AND created_at < ?", start, end).
		Group("bucket").Scan(&registrations).Error; err != nil {
		return UserPeriod{}, err
	}
	payers, firstPayers, periodPayers, err := payment.PayersByBucket(buckets.starts)
	if err != nil {
		return UserPeriod{}, err
	}

	period := UserPeriod{List: make([]UserBucket, len(buckets.labels))}
	for i, label := range buckets.labels {
		period.List[i] = UserBucket{Date: label, Start: buckets.starts[i]}
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

// GetRankings ranks the models and users of today, or of yesterday,
// by consumed quota.
func GetRankings(now time.Time, period string) (*Rankings, error) {
	day := localDayStart(now)
	if period == RankingYesterday {
		day = day.AddDate(0, 0, -1)
	}
	rankings := &Rankings{Period: period, Start: day.Unix(), End: day.AddDate(0, 0, 1).Unix()}
	rankings.TimeZone, rankings.UTCOffset = consoleZone(now)
	usage := "coalesce(sum(quota), 0) AS quota, coalesce(sum(count), 0) AS requests, coalesce(sum(token_used), 0) AS tokens"
	if err := model.DB.Model(&model.QuotaData{}).Select("model_name, "+usage).
		Where("created_at >= ? AND created_at < ?", rankings.Start, rankings.End).
		Group("model_name").Order("quota DESC, model_name").Limit(consoleRankingLimit).
		Scan(&rankings.Models).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Model(&model.QuotaData{}).Select("user_id, max(username) AS username, "+usage).
		Where("created_at >= ? AND created_at < ?", rankings.Start, rankings.End).
		Group("user_id").Order("quota DESC, user_id").Limit(consoleRankingLimit).
		Scan(&rankings.Users).Error; err != nil {
		return nil, err
	}
	if rankings.Models == nil {
		rankings.Models = []ModelRank{}
	}
	if rankings.Users == nil {
		rankings.Users = []UserRank{}
	}
	return rankings, nil
}
