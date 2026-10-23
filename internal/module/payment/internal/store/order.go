// Package store keeps the payment module's tables: payment methods,
// orders and their settlement, unmatched payments and exchange rates. It
// writes the shared kernel's tables (wallets, subscriptions, logs) only
// through the exported functions of package model.
package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrOrderNotFound = errors.New("payment order not found")
	// ErrOrderNotPending reports an order that left the pending state
	// before the operation could apply.
	ErrOrderNotPending = errors.New("payment order is no longer pending")
	// ErrCheckoutChanged reports that the checkout of an order
	// changed after a gateway verdict on it was made.
	ErrCheckoutChanged = errors.New("payment order checkout changed")

	ErrTooManyPendingOrders error = common.NewMessage("You have too many unpaid orders. Pay or close one first.")
	ErrPlanPurchaseLimit    error = common.NewMessage("Purchase limit for this plan has been reached")
)

// OrderLimits bounds the orders a user may hold when a new one is
// created.
type OrderLimits struct {
	// MaxPending is how many unexpired pending orders a user may hold.
	MaxPending int
	// MaxPlanPurchases is the plan's purchase limit (0 = unlimited);
	// existing subscriptions and orders still in flight both count.
	MaxPlanPurchases int
}

// CreateOrder inserts a pending order. The user row is locked so
// concurrent orders of one user are counted against the limits one by one.
func CreateOrder(order *entity.PaymentOrder, limits OrderLimits) error {
	order.Status = entity.PaymentOrderPending
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.LockUserTx(tx, order.UserId); err != nil {
			return err
		}
		if limits.MaxPending > 0 {
			var pending int64
			if err := tx.Model(&entity.PaymentOrder{}).
				Where("user_id = ? AND status = ? AND expires_at > ?", order.UserId, entity.PaymentOrderPending, common.GetTimestamp()).
				Count(&pending).Error; err != nil {
				return err
			}
			if pending >= int64(limits.MaxPending) {
				return ErrTooManyPendingOrders
			}
		}
		if order.Purpose == entity.PaymentPurposeSubscription && limits.MaxPlanPurchases > 0 {
			subscriptions, err := model.CountUserPlanSubscriptionsTx(tx, order.UserId, order.PlanId)
			if err != nil {
				return err
			}
			var inFlight int64
			if err := tx.Model(&entity.PaymentOrder{}).
				Where("user_id = ? AND plan_id = ? AND purpose = ? AND status IN ?", order.UserId, order.PlanId,
					entity.PaymentPurposeSubscription, []int{entity.PaymentOrderPending, entity.PaymentOrderPaid}).
				Count(&inFlight).Error; err != nil {
				return err
			}
			if subscriptions+inFlight >= int64(limits.MaxPlanPurchases) {
				return ErrPlanPurchaseLimit
			}
		}
		return tx.Create(order).Error
	})
}

func GetOrderByNo(orderNo string) (*entity.PaymentOrder, error) {
	var order entity.PaymentOrder
	err := model.DB.Where("order_no = ?", orderNo).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &order, nil
}

// OrderFilter selects orders in the administrator's list; zero
// fields do not filter.
type OrderFilter struct {
	UserId   int
	Status   int
	Purpose  string
	Platform string
	OrderNo  string
}

func ListOrders(filter OrderFilter, pageInfo *common.PageInfo) ([]*entity.PaymentOrder, int64, error) {
	query := model.DB.Model(&entity.PaymentOrder{})
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
	var orders []*entity.PaymentOrder
	err := query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&orders).Error
	return orders, total, err
}

// SetPaymentExpectation records the payment expectation of a pending order
// whose checkout has not started. It reports whether this call recorded it;
// otherwise the order already holds one (or is no longer pending) and the
// caller reloads it.
func SetPaymentExpectation(orderID int, amount int64, currency, rate string) (bool, error) {
	result := model.DB.Model(&entity.PaymentOrder{}).
		Where("id = ? AND status = ? AND payment_currency = ''", orderID, entity.PaymentOrderPending).
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
	result := model.DB.Model(&entity.PaymentOrder{}).
		Where("id = ? AND status = ? AND trade_no = ''", orderID, entity.PaymentOrderPending).
		Update("trade_no", tradeNo)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 1 {
		return tradeNo, nil
	}
	var order entity.PaymentOrder
	if err := model.DB.Select("trade_no", "status").Where("id = ?", orderID).First(&order).Error; err != nil {
		return "", err
	}
	if order.TradeNo != "" {
		return order.TradeNo, nil
	}
	return "", ErrOrderNotPending
}

// ListDueOrders returns pending orders past their expiry whose next
// gateway check is due.
func ListDueOrders(now int64, limit int) ([]*entity.PaymentOrder, error) {
	var orders []*entity.PaymentOrder
	err := model.DB.Where("status = ? AND expires_at <= ? AND next_check_at <= ?", entity.PaymentOrderPending, now, now).
		Order("next_check_at asc, id asc").Limit(limit).Find(&orders).Error
	return orders, err
}

// DeferOrderCheck postpones the next gateway check of a pending
// order.
func DeferOrderCheck(orderID int, attempts int, nextCheckAt int64) error {
	return model.DB.Model(&entity.PaymentOrder{}).
		Where("id = ? AND status = ?", orderID, entity.PaymentOrderPending).
		Updates(map[string]any{"check_attempts": attempts, "next_check_at": nextCheckAt}).Error
}

// checkoutChanged reports whether the checkout of an order differs
// from the snapshot a gateway verdict was made on.
func checkoutChanged(current, snapshot *entity.PaymentOrder) bool {
	return current.PaymentCurrency != snapshot.PaymentCurrency || current.PaymentAmount != snapshot.PaymentAmount ||
		current.TradeNo != snapshot.TradeNo || current.PaymentMethodId != snapshot.PaymentMethodId
}

// CloseOrder closes a pending order and reports whether this call
// closed it. With requireStableCheckout the order is locked first and must
// still have the checkout of snapshot, or ErrCheckoutChanged is
// returned: the gateway verdict the close relies on covers only that
// checkout.
func CloseOrder(snapshot *entity.PaymentOrder, requireStableCheckout bool, reason string) (bool, error) {
	closed := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if requireStableCheckout {
			var current entity.PaymentOrder
			if err := model.LockForUpdate(tx).Where("id = ?", snapshot.Id).First(&current).Error; err != nil {
				return err
			}
			if current.Status != entity.PaymentOrderPending {
				return nil
			}
			if checkoutChanged(&current, snapshot) {
				return ErrCheckoutChanged
			}
		}
		result := tx.Model(&entity.PaymentOrder{}).
			Where("id = ? AND status = ?", snapshot.Id, entity.PaymentOrderPending).
			Updates(map[string]any{"status": entity.PaymentOrderClosed, "closed_at": common.GetTimestamp(), "close_reason": reason})
		closed = result.RowsAffected == 1
		return result.Error
	})
	return closed, err
}

// Settlement is a confirmed gateway payment to settle an order with.
type Settlement struct {
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

// SettleResult reports what SettleOrder did.
type SettleResult struct {
	Outcome int
	// Reason explains SettleUnmatched and SettleFulfilFailed.
	Reason string
	Order  *entity.PaymentOrder
}

// fulfilment is what a committed fulfilment leaves for the caches and logs.
type fulfilment struct {
	creditedQuota int
	planTitle     string
	upgradedGroup bool
}

// SettleOrder settles a confirmed gateway payment in one transaction:
// the order is locked, bound to the payment, marked paid and fulfilled in a
// savepoint. A business failure of the fulfilment (wallet ceiling, plan
// purchase limit, missing plan or user) rolls back to the savepoint and
// leaves the order paid with FulfilError; any other error rolls the whole
// transaction back so the gateway retries. A payment that cannot settle the
// order is recorded in payment_unmatched once.
func SettleOrder(s Settlement) (*SettleResult, error) {
	result := &SettleResult{}
	var done *fulfilment
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var order entity.PaymentOrder
		if err := model.LockForUpdate(tx).Where("order_no = ?", s.OrderNo).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrderNotFound
			}
			return err
		}
		result.Order = &order
		if s.Manual {
			if order.Status != entity.PaymentOrderPending && order.Status != entity.PaymentOrderClosed {
				return ErrOrderNotPending
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
			return recordUnmatchedTx(tx, &order, s, reason)
		}
		switch order.Status {
		case entity.PaymentOrderFinished, entity.PaymentOrderPaid:
			if order.TradeNo == s.TradeNo {
				result.Outcome = SettleDuplicate
				return nil
			}
			return unmatched("the order was already paid with another trade")
		case entity.PaymentOrderPending:
		case entity.PaymentOrderClosed:
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
		if err := tx.Model(&entity.PaymentOrder{}).
			Where("platform = ? AND trade_no = ? AND id <> ?", order.Platform, s.TradeNo, order.Id).
			Count(&holders).Error; err != nil {
			return err
		}
		if holders > 0 {
			return unmatched("the trade already settled another order")
		}
		now := common.GetTimestamp()
		order.Status, order.TradeNo, order.PaidAt = entity.PaymentOrderPaid, s.TradeNo, now
		if err := tx.Model(&order).Updates(map[string]any{
			"status": order.Status, "trade_no": order.TradeNo, "paid_at": now,
			"payment_amount": order.PaymentAmount, "payment_currency": order.PaymentCurrency,
		}).Error; err != nil {
			return err
		}
		fulfilled, businessErr, err := fulfilOrderTx(tx, &order)
		if err != nil {
			return err
		}
		if businessErr != nil {
			result.Outcome, result.Reason = SettleFulfilFailed, businessErr.Error()
			return recordUnmatchedTx(tx, &order, s, "fulfilment failed: "+businessErr.Error())
		}
		result.Outcome = SettleFinished
		done = fulfilled
		return nil
	})
	if err != nil {
		return nil, err
	}
	if done != nil {
		afterFulfilment(result.Order, done, s.ClientIp)
	}
	return result, nil
}

// FulfilOrder retries the fulfilment of a paid order whose
// fulfilment failed. It returns the business error that still prevents it,
// if any; the order then keeps its new FulfilError.
func FulfilOrder(orderNo string, clientIp string) (*entity.PaymentOrder, error) {
	var order entity.PaymentOrder
	var done *fulfilment
	var businessErr error
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.LockForUpdate(tx).Where("order_no = ?", orderNo).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrderNotFound
			}
			return err
		}
		if order.Status != entity.PaymentOrderPaid {
			return common.NewMessage("Only a paid order that was not fulfilled can be fulfilled again")
		}
		var err error
		done, businessErr, err = fulfilOrderTx(tx, &order)
		if err != nil || businessErr != nil {
			return err
		}
		// The unmatched record of the failed fulfilment is resolved by it.
		return tx.Model(&entity.PaymentUnmatched{}).
			Where("order_no = ? AND trade_no = ? AND resolved_at = 0", order.OrderNo, order.TradeNo).
			Updates(map[string]any{"resolved_at": common.GetTimestamp(), "resolve_note": "fulfilled"}).Error
	})
	if err != nil {
		return nil, err
	}
	if businessErr != nil {
		return &order, businessErr
	}
	afterFulfilment(&order, done, clientIp)
	return &order, nil
}

// fulfilOrderTx fulfils a paid order in a savepoint: a top-up credits
// the wallet under its ceiling, a subscription order creates the
// subscription (even when the plan was disabled after the purchase). A
// business failure is returned as businessErr after rolling back to the
// savepoint and storing it as the order's FulfilError; err is any other
// failure, which must abort the transaction.
func fulfilOrderTx(tx *gorm.DB, order *entity.PaymentOrder) (done *fulfilment, businessErr error, err error) {
	if err := tx.SavePoint("payment_fulfil").Error; err != nil {
		return nil, nil, err
	}
	done, fulfilErr := fulfilPurchaseTx(tx, order)
	if fulfilErr != nil {
		if !isFulfilBusinessError(fulfilErr) {
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
	order.Status, order.FinishedAt, order.FulfilError = entity.PaymentOrderFinished, now, ""
	if err := tx.Model(order).Updates(map[string]any{
		"status": order.Status, "finished_at": now, "fulfil_error": "", "user_subscription_id": order.UserSubscriptionId,
	}).Error; err != nil {
		return nil, nil, err
	}
	return done, nil, nil
}

func fulfilPurchaseTx(tx *gorm.DB, order *entity.PaymentOrder) (*fulfilment, error) {
	switch order.Purpose {
	case entity.PaymentPurposeTopup:
		if err := model.CreditWalletTx(tx, order.UserId, order.QuotaAmount); err != nil {
			return nil, err
		}
		return &fulfilment{creditedQuota: order.QuotaAmount}, nil
	case entity.PaymentPurposeSubscription:
		// The user row lock serializes the plan purchase limit check of
		// concurrent settlements of one user.
		if err := model.LockUserTx(tx, order.UserId); err != nil {
			return nil, err
		}
		plan, err := model.GetSubscriptionPlanTx(tx, order.PlanId)
		if err != nil {
			return nil, err
		}
		subscription, err := model.CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, "order")
		if err != nil {
			return nil, err
		}
		order.UserSubscriptionId = subscription.Id
		return &fulfilment{planTitle: plan.Title, upgradedGroup: subscription.PrevUserGroup != ""}, nil
	default:
		return nil, fmt.Errorf("unknown payment purpose %q", order.Purpose)
	}
}

// isFulfilBusinessError reports a fulfilment failure that retrying
// the callback cannot fix: the wallet ceiling, the plan purchase limit, a
// plan or user that no longer exists. They are web console messages or
// record-not-found errors.
func isFulfilBusinessError(err error) bool {
	var message *common.Message
	return errors.As(err, &message) || errors.Is(err, gorm.ErrRecordNotFound)
}

// afterFulfilment updates the caches and writes the user log of a
// committed fulfilment.
func afterFulfilment(order *entity.PaymentOrder, done *fulfilment, clientIp string) {
	paid := formatAmount(order.PaymentAmount) + " " + order.PaymentCurrency
	switch order.Purpose {
	case entity.PaymentPurposeTopup:
		model.SyncWalletCredit(order.UserId, done.creditedQuota, common.LogText("payment order %s", order.OrderNo))
		model.RecordTopupLog(order.UserId, common.NewMessage("Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}",
			map[string]any{"quota": logger.FormatQuota(done.creditedQuota), "amount": paid}), clientIp, order.Platform, order.Platform)
	case entity.PaymentPurposeSubscription:
		if done.upgradedGroup {
			refreshGroupCache(order.UserId, order.OrderNo)
		}
		model.RecordLog(order.UserId, model.LogTypeTopup, common.NewMessage("Subscription purchased, plan: {{plan}}, payment amount: {{amount}}, payment method: {{method}}",
			map[string]any{"plan": done.planTitle, "amount": paid, "method": order.Platform}))
	}
}

// formatAmount renders hundredths as a two-decimal amount.
func formatAmount(minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100)
}

// PayOrderWithBalance pays a pending subscription order with the
// wallet balance in one transaction: the order and the user are locked, the
// order's QuotaAmount is deducted only if the wallet holds it, and the
// subscription is created. A free order (QuotaAmount 0) completes the same
// way without a deduction.
func PayOrderWithBalance(orderNo string, userId int) (*entity.PaymentOrder, error) {
	var order entity.PaymentOrder
	var planTitle string
	var upgradedGroup bool
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.LockForUpdate(tx).Where("order_no = ? AND user_id = ?", orderNo, userId).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrderNotFound
			}
			return err
		}
		if order.Status != entity.PaymentOrderPending {
			return ErrOrderNotPending
		}
		if order.Platform != entity.PaymentPlatformBalance || order.Purpose != entity.PaymentPurposeSubscription {
			return errors.New("the order is not a balance subscription order")
		}
		if err := model.LockUserTx(tx, userId); err != nil {
			return err
		}
		plan, err := model.GetSubscriptionPlanTx(tx, order.PlanId)
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
			if err := model.DebitWalletTx(tx, userId, order.QuotaAmount); err != nil {
				return err
			}
		}
		subscription, err := model.CreateUserSubscriptionFromPlanTx(tx, userId, plan, entity.PaymentPlatformBalance)
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		order.Status, order.PaidAt, order.FinishedAt, order.UserSubscriptionId = entity.PaymentOrderFinished, now, now, subscription.Id
		planTitle, upgradedGroup = plan.Title, subscription.PrevUserGroup != ""
		return tx.Model(&order).Updates(map[string]any{
			"status": order.Status, "paid_at": now, "finished_at": now, "user_subscription_id": subscription.Id,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	if order.QuotaAmount > 0 {
		if err := model.SyncWalletDebit(userId, order.QuotaAmount); err != nil {
			common.SysLog(common.LogText("failed to decrease user quota cache after subscription balance purchase: %s", err.Error()))
		}
	}
	if upgradedGroup {
		refreshGroupCache(userId, order.OrderNo)
	}
	model.RecordLog(userId, model.LogTypeTopup, common.NewMessage("Subscription purchased with balance, plan: {{plan}}, payment amount: {{amount}}, quota deducted: {{quota}}",
		map[string]any{"plan": planTitle, "amount": formatAmount(order.Amount) + " " + order.Currency, "quota": order.QuotaAmount}))
	return &order, nil
}

// ManualTradeNo is the trade number of an order an administrator marked as
// paid.
func ManualTradeNo(orderNo string) string {
	return "manual:" + orderNo
}

// recordUnmatchedTx keeps the durable record of a confirmed payment
// that could not settle order, once per payment.
func recordUnmatchedTx(tx *gorm.DB, order *entity.PaymentOrder, s Settlement, reason string) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entity.PaymentUnmatched{
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

// refreshGroupCache reloads the cached group of a user whose subscription
// purchase upgraded it.
func refreshGroupCache(userId int, orderNo string) {
	if err := model.RefreshUserGroupCache(userId); err != nil {
		common.SysError(common.LogText("failed to refresh user group cache after %s for user %d: %v", common.LogText("payment order %s", orderNo), userId, err))
	}
}
