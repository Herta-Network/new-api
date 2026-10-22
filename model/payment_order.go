package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Payment order statuses. A pending order becomes paid when its payment is
// confirmed and finished once it is fulfilled (quota credited or
// subscription created); a paid order whose fulfilment failed stays paid
// with FulfilError until an administrator fulfils it. A closed order can
// still be paid late, then it is settled like a pending one.
const (
	PaymentOrderPending  = 1
	PaymentOrderPaid     = 2
	PaymentOrderClosed   = 3
	PaymentOrderFinished = 5
)

// What a payment order buys.
const (
	PaymentPurposeTopup        = "topup"
	PaymentPurposeSubscription = "subscription"
)

var (
	ErrPaymentOrderNotFound = errors.New("payment order not found")
	// ErrPaymentOrderNotPending reports an order that left the pending state
	// before the operation could apply.
	ErrPaymentOrderNotPending = errors.New("payment order is no longer pending")
	// ErrPaymentCheckoutChanged reports that the checkout of an order
	// changed after a gateway verdict on it was made.
	ErrPaymentCheckoutChanged = errors.New("payment order checkout changed")

	ErrTooManyPendingPaymentOrders error = common.NewMessage("You have too many unpaid orders. Pay or close one first.")
	ErrPaymentPlanPurchaseLimit    error = common.NewMessage("Purchase limit for this plan has been reached")
	ErrInsufficientBalance         error = common.NewMessage("Insufficient balance")
)

// PaymentOrder is one purchase: a wallet top-up or a subscription plan.
// Amounts count hundredths of their currency.
type PaymentOrder struct {
	Id      int    `json:"id" gorm:"index:idx_payment_orders_user,priority:2"`
	OrderNo string `json:"order_no" gorm:"type:varchar(64);not null;uniqueIndex"`
	UserId  int    `json:"user_id" gorm:"not null;index:idx_payment_orders_user,priority:1"`
	Purpose string `json:"purpose" gorm:"type:varchar(16);not null"`
	PlanId  int    `json:"plan_id" gorm:"not null;default:0"`
	Subject string `json:"subject" gorm:"type:varchar(255);not null;default:''"`

	// TopupAmount is the face value bought, in the site currency. QuotaAmount
	// is the quota a top-up credits or a balance purchase deducts, converted
	// at QuotaRate, the site currency to USD rate when the order was made.
	TopupAmount int64  `json:"topup_amount" gorm:"not null;default:0"`
	QuotaAmount int    `json:"quota_amount" gorm:"type:bigint;not null;default:0"`
	QuotaRate   string `json:"quota_rate" gorm:"type:varchar(64);not null;default:''"`

	// Currency is the site currency the price is in. Amount = Price +
	// Adjustment + FeeAmount, where Adjustment is what the group ratio and
	// the amount discount change (negative for a discount).
	Currency   string `json:"currency" gorm:"type:varchar(8);not null"`
	Price      int64  `json:"price" gorm:"not null;default:0"`
	Adjustment int64  `json:"adjustment" gorm:"not null;default:0"`
	FeeAmount  int64  `json:"fee_amount" gorm:"not null;default:0"`
	Amount     int64  `json:"amount" gorm:"not null;default:0"`

	PaymentMethodId int    `json:"payment_method_id" gorm:"not null;index:idx_payment_orders_method_status,priority:1"`
	Platform        string `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex:idx_payment_orders_platform_trade,priority:1,where:trade_no <> ''"`

	// The payment expectation is what the gateway is asked to collect, set
	// once before the first checkout; an empty PaymentCurrency means no
	// checkout has started. ExchangeRate converted Amount into it.
	PaymentAmount   int64  `json:"payment_amount" gorm:"not null;default:0"`
	PaymentCurrency string `json:"payment_currency" gorm:"type:varchar(8);not null;default:''"`
	ExchangeRate    string `json:"exchange_rate" gorm:"type:varchar(64);not null;default:''"`

	// TradeNo is the gateway payment bound to the order; one payment
	// settles at most one order of its platform.
	TradeNo string `json:"trade_no" gorm:"type:varchar(255);not null;default:'';uniqueIndex:idx_payment_orders_platform_trade,priority:2"`

	Status             int    `json:"status" gorm:"not null;index:idx_payment_orders_status_check,priority:1;index:idx_payment_orders_method_status,priority:2;index:idx_payment_orders_status_paid,priority:1"`
	FulfilError        string `json:"fulfil_error" gorm:"type:text;not null;default:''"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"not null;default:0"`
	ClientIp           string `json:"-" gorm:"type:varchar(64);not null;default:''"`
	CloseReason        string `json:"close_reason" gorm:"type:varchar(32);not null;default:''"`

	CreatedAt     int64 `json:"created_at" gorm:"bigint"`
	ExpiresAt     int64 `json:"expires_at" gorm:"bigint;not null;default:0"`
	PaidAt        int64 `json:"paid_at" gorm:"bigint;not null;default:0;index:idx_payment_orders_status_paid,priority:2"`
	FinishedAt    int64 `json:"finished_at" gorm:"bigint;not null;default:0"`
	ClosedAt      int64 `json:"closed_at" gorm:"bigint;not null;default:0"`
	NextCheckAt   int64 `json:"-" gorm:"bigint;not null;default:0;index:idx_payment_orders_status_check,priority:2"`
	CheckAttempts int   `json:"-" gorm:"not null;default:0"`
}

// PaymentOrderLimits bounds the orders a user may hold when a new one is
// created.
type PaymentOrderLimits struct {
	// MaxPending is how many unexpired pending orders a user may hold.
	MaxPending int
	// MaxPlanPurchases is the plan's purchase limit (0 = unlimited);
	// existing subscriptions and orders still in flight both count.
	MaxPlanPurchases int
}

// CreatePaymentOrder inserts a pending order. The user row is locked so
// concurrent orders of one user are counted against the limits one by one.
func CreatePaymentOrder(order *PaymentOrder, limits PaymentOrderLimits) error {
	order.Status = PaymentOrderPending
	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", order.UserId).First(&user).Error; err != nil {
			return err
		}
		if limits.MaxPending > 0 {
			var pending int64
			if err := tx.Model(&PaymentOrder{}).
				Where("user_id = ? AND status = ? AND expires_at > ?", order.UserId, PaymentOrderPending, common.GetTimestamp()).
				Count(&pending).Error; err != nil {
				return err
			}
			if pending >= int64(limits.MaxPending) {
				return ErrTooManyPendingPaymentOrders
			}
		}
		if order.Purpose == PaymentPurposeSubscription && limits.MaxPlanPurchases > 0 {
			var subscriptions, inFlight int64
			if err := tx.Model(&UserSubscription{}).
				Where("user_id = ? AND plan_id = ?", order.UserId, order.PlanId).
				Count(&subscriptions).Error; err != nil {
				return err
			}
			if err := tx.Model(&PaymentOrder{}).
				Where("user_id = ? AND plan_id = ? AND purpose = ? AND status IN ?", order.UserId, order.PlanId,
					PaymentPurposeSubscription, []int{PaymentOrderPending, PaymentOrderPaid}).
				Count(&inFlight).Error; err != nil {
				return err
			}
			if subscriptions+inFlight >= int64(limits.MaxPlanPurchases) {
				return ErrPaymentPlanPurchaseLimit
			}
		}
		return tx.Create(order).Error
	})
}

func GetPaymentOrderByNo(orderNo string) (*PaymentOrder, error) {
	var order PaymentOrder
	err := DB.Where("order_no = ?", orderNo).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPaymentOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &order, nil
}

// PaymentOrderFilter selects orders in the administrator's list; zero
// fields do not filter.
type PaymentOrderFilter struct {
	UserId   int
	Status   int
	Purpose  string
	Platform string
	OrderNo  string
}

func ListPaymentOrders(filter PaymentOrderFilter, pageInfo *common.PageInfo) ([]*PaymentOrder, int64, error) {
	query := DB.Model(&PaymentOrder{})
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Status > 0 {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Purpose != "" {
		query = query.Where("purpose = ?", filter.Purpose)
	}
	if filter.Platform != "" {
		query = query.Where("platform = ?", filter.Platform)
	}
	if filter.OrderNo != "" {
		query = query.Where("order_no = ?", filter.OrderNo)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var orders []*PaymentOrder
	err := query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&orders).Error
	return orders, total, err
}

// SetPaymentExpectation records the payment expectation of a pending order
// whose checkout has not started. It reports whether this call recorded it;
// otherwise the order already holds one (or is no longer pending) and the
// caller reloads it.
func SetPaymentExpectation(orderID int, amount int64, currency, rate string) (bool, error) {
	result := DB.Model(&PaymentOrder{}).
		Where("id = ? AND status = ? AND payment_currency = ''", orderID, PaymentOrderPending).
		Updates(map[string]any{"payment_amount": amount, "payment_currency": currency, "exchange_rate": rate})
	return result.RowsAffected == 1, result.Error
}

// ClaimPaymentTrade binds tradeNo as the only gateway payment of a pending
// order and returns the trade the order holds afterwards: tradeNo when the
// claim won, the trade a concurrent checkout claimed otherwise.
func ClaimPaymentTrade(orderID int, tradeNo string) (string, error) {
	if tradeNo == "" {
		return "", errors.New("empty trade number")
	}
	result := DB.Model(&PaymentOrder{}).
		Where("id = ? AND status = ? AND trade_no = ''", orderID, PaymentOrderPending).
		Update("trade_no", tradeNo)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 1 {
		return tradeNo, nil
	}
	var order PaymentOrder
	if err := DB.Select("trade_no", "status").Where("id = ?", orderID).First(&order).Error; err != nil {
		return "", err
	}
	if order.TradeNo != "" {
		return order.TradeNo, nil
	}
	return "", ErrPaymentOrderNotPending
}

// ListDuePaymentOrders returns pending orders past their expiry whose next
// gateway check is due.
func ListDuePaymentOrders(now int64, limit int) ([]*PaymentOrder, error) {
	var orders []*PaymentOrder
	err := DB.Where("status = ? AND expires_at <= ? AND next_check_at <= ?", PaymentOrderPending, now, now).
		Order("next_check_at asc, id asc").Limit(limit).Find(&orders).Error
	return orders, err
}

// DeferPaymentOrderCheck postpones the next gateway check of a pending
// order.
func DeferPaymentOrderCheck(orderID int, attempts int, nextCheckAt int64) error {
	return DB.Model(&PaymentOrder{}).
		Where("id = ? AND status = ?", orderID, PaymentOrderPending).
		Updates(map[string]any{"check_attempts": attempts, "next_check_at": nextCheckAt}).Error
}

// paymentCheckoutChanged reports whether the checkout of an order differs
// from the snapshot a gateway verdict was made on.
func paymentCheckoutChanged(current, snapshot *PaymentOrder) bool {
	return current.PaymentCurrency != snapshot.PaymentCurrency || current.PaymentAmount != snapshot.PaymentAmount ||
		current.TradeNo != snapshot.TradeNo || current.PaymentMethodId != snapshot.PaymentMethodId
}

// ClosePaymentOrder closes a pending order and reports whether this call
// closed it. With requireStableCheckout the order is locked first and must
// still have the checkout of snapshot, or ErrPaymentCheckoutChanged is
// returned: the gateway verdict the close relies on covers only that
// checkout.
func ClosePaymentOrder(snapshot *PaymentOrder, requireStableCheckout bool, reason string) (bool, error) {
	closed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if requireStableCheckout {
			var current PaymentOrder
			if err := lockForUpdate(tx).Where("id = ?", snapshot.Id).First(&current).Error; err != nil {
				return err
			}
			if current.Status != PaymentOrderPending {
				return nil
			}
			if paymentCheckoutChanged(&current, snapshot) {
				return ErrPaymentCheckoutChanged
			}
		}
		result := tx.Model(&PaymentOrder{}).
			Where("id = ? AND status = ?", snapshot.Id, PaymentOrderPending).
			Updates(map[string]any{"status": PaymentOrderClosed, "closed_at": common.GetTimestamp(), "close_reason": reason})
		closed = result.RowsAffected == 1
		return result.Error
	})
	return closed, err
}

// PaymentSettlement is a confirmed gateway payment to settle an order with.
type PaymentSettlement struct {
	OrderNo string
	TradeNo string
	// Amount and Currency are what the gateway collected; they must equal
	// the order's payment expectation.
	Amount   int64
	Currency string
	// ClientIp is recorded in the top-up log.
	ClientIp string
	// Manual marks an administrator's completion of an order the gateway
	// cannot report as paid (a transfer, for example). It needs no TradeNo,
	// Amount or Currency: the order's own expectation, or its amount when no
	// checkout started, is taken as paid under the trade number
	// ManualTradeNo.
	Manual bool
}

// Settlement outcomes.
const (
	// SettleFinished: the order was paid and fulfilled by this call.
	SettleFinished = iota + 1
	// SettleDuplicate: the order already holds this payment.
	SettleDuplicate
	// SettleUnmatched: the payment cannot settle the order (it was paid with
	// another trade, the amount differs, ...) and was recorded in
	// payment_unmatched for an operator.
	SettleUnmatched
	// SettleFulfilFailed: the order is paid but its fulfilment failed for a
	// business reason; it keeps FulfilError and the payment was recorded in
	// payment_unmatched.
	SettleFulfilFailed
)

// PaymentSettleResult reports what SettlePaymentOrder did.
type PaymentSettleResult struct {
	Outcome int
	// Reason explains SettleUnmatched and SettleFulfilFailed.
	Reason string
	Order  *PaymentOrder
}

// fulfilment is what a committed fulfilment leaves for the caches and logs.
type fulfilment struct {
	creditedQuota int
	planTitle     string
	upgradedGroup bool
}

// SettlePaymentOrder settles a confirmed gateway payment in one transaction:
// the order is locked, bound to the payment, marked paid and fulfilled in a
// savepoint. A business failure of the fulfilment (wallet ceiling, plan
// purchase limit, missing plan or user) rolls back to the savepoint and
// leaves the order paid with FulfilError; any other error rolls the whole
// transaction back so the gateway retries. A payment that cannot settle the
// order is recorded in payment_unmatched once.
func SettlePaymentOrder(s PaymentSettlement) (*PaymentSettleResult, error) {
	result := &PaymentSettleResult{}
	var done *fulfilment
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order PaymentOrder
		if err := lockForUpdate(tx).Where("order_no = ?", s.OrderNo).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPaymentOrderNotFound
			}
			return err
		}
		result.Order = &order
		if s.Manual {
			if order.Status != PaymentOrderPending && order.Status != PaymentOrderClosed {
				return ErrPaymentOrderNotPending
			}
			if order.TradeNo != "" {
				return common.NewMessage("The order is bound to a gateway payment. Reconcile it instead.")
			}
			if order.PaymentCurrency == "" {
				order.PaymentAmount, order.PaymentCurrency = order.Amount, order.Currency
			}
			s.TradeNo, s.Amount, s.Currency = ManualTradeNo(order.OrderNo), order.PaymentAmount, order.PaymentCurrency
		}
		unmatched := func(reason string) error {
			result.Outcome, result.Reason = SettleUnmatched, reason
			return recordPaymentUnmatchedTx(tx, &order, s, reason)
		}
		switch order.Status {
		case PaymentOrderFinished, PaymentOrderPaid:
			if order.TradeNo == s.TradeNo {
				result.Outcome = SettleDuplicate
				return nil
			}
			return unmatched("the order was already paid with another trade")
		case PaymentOrderPending:
		case PaymentOrderClosed:
			// A payment that arrives after the order closed settles it when it
			// is exactly the expectation the closed checkout asked for.
			if order.PaymentCurrency == "" && !s.Manual {
				return unmatched("the order closed before its checkout started")
			}
		default:
			return unmatched(fmt.Sprintf("the order has status %d", order.Status))
		}
		if order.TradeNo != "" && order.TradeNo != s.TradeNo {
			return unmatched("the order is bound to another trade")
		}
		if order.PaymentCurrency == "" || order.PaymentAmount != s.Amount || !strings.EqualFold(order.PaymentCurrency, s.Currency) {
			return unmatched("the paid amount differs from the payment expectation")
		}
		var holders int64
		if err := tx.Model(&PaymentOrder{}).
			Where("platform = ? AND trade_no = ? AND id <> ?", order.Platform, s.TradeNo, order.Id).
			Count(&holders).Error; err != nil {
			return err
		}
		if holders > 0 {
			return unmatched("the trade already settled another order")
		}
		now := common.GetTimestamp()
		order.Status, order.TradeNo, order.PaidAt = PaymentOrderPaid, s.TradeNo, now
		if err := tx.Model(&order).Updates(map[string]any{
			"status": order.Status, "trade_no": order.TradeNo, "paid_at": now,
			"payment_amount": order.PaymentAmount, "payment_currency": order.PaymentCurrency,
		}).Error; err != nil {
			return err
		}
		fulfilled, businessErr, err := fulfilPaymentOrderTx(tx, &order)
		if err != nil {
			return err
		}
		if businessErr != nil {
			result.Outcome, result.Reason = SettleFulfilFailed, businessErr.Error()
			return recordPaymentUnmatchedTx(tx, &order, s, "fulfilment failed: "+businessErr.Error())
		}
		result.Outcome = SettleFinished
		done = fulfilled
		return nil
	})
	if err != nil {
		return nil, err
	}
	if done != nil {
		afterPaymentFulfilment(result.Order, done, s.ClientIp)
	}
	return result, nil
}

// FulfilPaymentOrder retries the fulfilment of a paid order whose
// fulfilment failed. It returns the business error that still prevents it,
// if any; the order then keeps its new FulfilError.
func FulfilPaymentOrder(orderNo string, clientIp string) (*PaymentOrder, error) {
	var order PaymentOrder
	var done *fulfilment
	var businessErr error
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("order_no = ?", orderNo).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPaymentOrderNotFound
			}
			return err
		}
		if order.Status != PaymentOrderPaid {
			return common.NewMessage("Only a paid order that was not fulfilled can be fulfilled again")
		}
		var err error
		done, businessErr, err = fulfilPaymentOrderTx(tx, &order)
		if err != nil || businessErr != nil {
			return err
		}
		// The unmatched record of the failed fulfilment is resolved by it.
		return tx.Model(&PaymentUnmatched{}).
			Where("order_no = ? AND trade_no = ? AND resolved_at = 0", order.OrderNo, order.TradeNo).
			Updates(map[string]any{"resolved_at": common.GetTimestamp(), "resolve_note": "fulfilled"}).Error
	})
	if err != nil {
		return nil, err
	}
	if businessErr != nil {
		return &order, businessErr
	}
	afterPaymentFulfilment(&order, done, clientIp)
	return &order, nil
}

// fulfilPaymentOrderTx fulfils a paid order in a savepoint: a top-up credits
// the wallet under its ceiling, a subscription order creates the
// subscription (even when the plan was disabled after the purchase). A
// business failure is returned as businessErr after rolling back to the
// savepoint and storing it as the order's FulfilError; err is any other
// failure, which must abort the transaction.
func fulfilPaymentOrderTx(tx *gorm.DB, order *PaymentOrder) (done *fulfilment, businessErr error, err error) {
	if err := tx.SavePoint("payment_fulfil").Error; err != nil {
		return nil, nil, err
	}
	done, fulfilErr := fulfilPaymentPurchaseTx(tx, order)
	if fulfilErr != nil {
		if !isPaymentFulfilBusinessError(fulfilErr) {
			return nil, nil, fulfilErr
		}
		if err := tx.RollbackTo("payment_fulfil").Error; err != nil {
			return nil, nil, err
		}
		order.FulfilError = fulfilErr.Error()
		if err := tx.Model(order).Update("fulfil_error", order.FulfilError).Error; err != nil {
			return nil, nil, err
		}
		return nil, fulfilErr, nil
	}
	now := common.GetTimestamp()
	order.Status, order.FinishedAt, order.FulfilError = PaymentOrderFinished, now, ""
	if err := tx.Model(order).Updates(map[string]any{
		"status": order.Status, "finished_at": now, "fulfil_error": "", "user_subscription_id": order.UserSubscriptionId,
	}).Error; err != nil {
		return nil, nil, err
	}
	return done, nil, nil
}

func fulfilPaymentPurchaseTx(tx *gorm.DB, order *PaymentOrder) (*fulfilment, error) {
	switch order.Purpose {
	case PaymentPurposeTopup:
		if err := creditTopUpQuota(tx, order.UserId, order.QuotaAmount, nil); err != nil {
			return nil, err
		}
		return &fulfilment{creditedQuota: order.QuotaAmount}, nil
	case PaymentPurposeSubscription:
		// The user row lock serializes the plan purchase limit check of
		// concurrent settlements of one user.
		var user User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", order.UserId).First(&user).Error; err != nil {
			return nil, err
		}
		plan, err := getSubscriptionPlanByIdTx(tx, order.PlanId)
		if err != nil {
			return nil, err
		}
		subscription, err := CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, "order")
		if err != nil {
			return nil, err
		}
		order.UserSubscriptionId = subscription.Id
		return &fulfilment{planTitle: plan.Title, upgradedGroup: subscription.PrevUserGroup != ""}, nil
	default:
		return nil, fmt.Errorf("unknown payment purpose %q", order.Purpose)
	}
}

// isPaymentFulfilBusinessError reports a fulfilment failure that retrying
// the callback cannot fix: the wallet ceiling, the plan purchase limit, a
// plan or user that no longer exists. They are web console messages or
// record-not-found errors.
func isPaymentFulfilBusinessError(err error) bool {
	var message *common.Message
	return errors.As(err, &message) || errors.Is(err, gorm.ErrRecordNotFound)
}

// afterPaymentFulfilment updates the caches and writes the user log of a
// committed fulfilment.
func afterPaymentFulfilment(order *PaymentOrder, done *fulfilment, clientIp string) {
	paid := formatPaymentAmount(order.PaymentAmount) + " " + order.PaymentCurrency
	switch order.Purpose {
	case PaymentPurposeTopup:
		syncCreditUserQuotaCache(order.UserId, done.creditedQuota, common.LogText("payment order %s", order.OrderNo))
		RecordTopupLog(order.UserId, common.NewMessage("Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}",
			map[string]any{"quota": logger.FormatQuota(done.creditedQuota), "amount": paid}), clientIp, order.Platform, order.Platform)
	case PaymentPurposeSubscription:
		if done.upgradedGroup {
			refreshSubscriptionUserGroupCache(order.UserId, common.LogText("payment order %s", order.OrderNo))
		}
		RecordLog(order.UserId, LogTypeTopup, common.NewMessage("Subscription purchased, plan: {{plan}}, payment amount: {{amount}}, payment method: {{method}}",
			map[string]any{"plan": done.planTitle, "amount": paid, "method": order.Platform}))
	}
}

// formatPaymentAmount renders hundredths as a two-decimal amount.
func formatPaymentAmount(minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100)
}

// PayPaymentOrderWithBalance pays a pending subscription order with the
// wallet balance in one transaction: the order and the user are locked, the
// order's QuotaAmount is deducted only if the wallet holds it, and the
// subscription is created. A free order (QuotaAmount 0) completes the same
// way without a deduction.
func PayPaymentOrderWithBalance(orderNo string, userId int) (*PaymentOrder, error) {
	var order PaymentOrder
	var planTitle string
	var upgradedGroup bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("order_no = ? AND user_id = ?", orderNo, userId).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPaymentOrderNotFound
			}
			return err
		}
		if order.Status != PaymentOrderPending {
			return ErrPaymentOrderNotPending
		}
		if order.Platform != PaymentPlatformBalance || order.Purpose != PaymentPurposeSubscription {
			return errors.New("the order is not a balance subscription order")
		}
		var user User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		plan, err := getSubscriptionPlanByIdTx(tx, order.PlanId)
		if err != nil {
			return err
		}
		if !plan.Enabled {
			return common.NewMessage("Subscription plan is not enabled")
		}
		if order.QuotaAmount > 0 {
			if plan.AllowBalancePay != nil && !*plan.AllowBalancePay {
				return common.NewMessage("This plan does not allow balance redemption")
			}
			result := tx.Model(&User{}).Where("id = ? AND quota >= ?", userId, order.QuotaAmount).
				Update("quota", gorm.Expr("quota - ?", order.QuotaAmount))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrInsufficientBalance
			}
		}
		subscription, err := CreateUserSubscriptionFromPlanTx(tx, userId, plan, PaymentPlatformBalance)
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		order.Status, order.PaidAt, order.FinishedAt, order.UserSubscriptionId = PaymentOrderFinished, now, now, subscription.Id
		planTitle, upgradedGroup = plan.Title, subscription.PrevUserGroup != ""
		return tx.Model(&order).Updates(map[string]any{
			"status": order.Status, "paid_at": now, "finished_at": now, "user_subscription_id": subscription.Id,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	if order.QuotaAmount > 0 {
		if err := cacheDecrUserQuota(userId, int64(order.QuotaAmount)); err != nil {
			common.SysLog(common.LogText("failed to decrease user quota cache after subscription balance purchase: %s", err.Error()))
		}
	}
	if upgradedGroup {
		refreshSubscriptionUserGroupCache(userId, common.LogText("payment order %s", order.OrderNo))
	}
	RecordLog(userId, LogTypeTopup, common.NewMessage("Subscription purchased with balance, plan: {{plan}}, payment amount: {{amount}}, quota deducted: {{quota}}",
		map[string]any{"plan": planTitle, "amount": formatPaymentAmount(order.Amount) + " " + order.Currency, "quota": order.QuotaAmount}))
	return &order, nil
}

// ManualTradeNo is the trade number of an order an administrator marked as
// paid.
func ManualTradeNo(orderNo string) string {
	return "manual:" + orderNo
}

// recordPaymentUnmatchedTx keeps the durable record of a confirmed payment
// that could not settle order, once per payment.
func recordPaymentUnmatchedTx(tx *gorm.DB, order *PaymentOrder, s PaymentSettlement, reason string) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&PaymentUnmatched{
		Platform:        order.Platform,
		TradeNo:         s.TradeNo,
		OrderNo:         order.OrderNo,
		PaymentMethodId: order.PaymentMethodId,
		UserId:          order.UserId,
		Amount:          s.Amount,
		Currency:        strings.ToUpper(s.Currency),
		Reason:          reason,
	}).Error
}
