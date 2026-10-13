package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/payment"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PaymentEventPending   = "pending"
	PaymentEventCompleted = "completed"
	PaymentEventRejected  = "rejected"
	PaymentEventIgnored   = "ignored"
	PaymentEventReview    = "review"
)

// Payload contains only normalized authenticated facts, never signatures or keys.
type PaymentEvent struct {
	ID                int64  `json:"id"`
	EventKey          string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	OrderNo           string `json:"order_no" gorm:"type:varchar(64);not null;index"`
	SettlementOrderNo string `json:"settlement_order_no" gorm:"type:varchar(64)"`
	MethodID          int    `json:"method_id"`
	VersionID         int    `json:"-"`
	Payload           string `json:"-" gorm:"type:jsonb"`
	TradeNo           string `json:"trade_no" gorm:"type:varchar(128);not null"`
	Amount            int64  `json:"amount"`
	Currency          string `json:"currency" gorm:"type:varchar(3)"`
	Paid              bool   `json:"paid"`
	Status            string `json:"status" gorm:"type:varchar(16);not null"`
	Attempts          int    `json:"attempts"`
	NextAttemptAt     int64  `json:"next_attempt_at" gorm:"index:idx_payment_event_retry,where:status = 'pending'"`
	LastError         string `json:"last_error" gorm:"type:varchar(64)"`
	CreatedAt         int64  `json:"created_at"`
	CompletedAt       int64  `json:"completed_at"`
	CallerIP          string `json:"-" gorm:"type:varchar(64)"`
}

func ReceivePaymentEvent(ctx context.Context, order *PaymentOrder, notice *payment.Notice, callerIP string, versionID int) (*PaymentEvent, error) {
	if notice.OrderNo != order.OrderNo {
		return nil, payment.ErrInvalidPayment
	}
	encoded, err := common.Marshal(notice)
	if err != nil {
		return nil, err
	}
	identity := string(encoded)
	if notice.EventID != "" {
		identity = order.AccountKey + "\x00" + notice.EventID
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
	event := PaymentEvent{EventKey: key, OrderNo: order.OrderNo, MethodID: order.MethodID, VersionID: versionID, Payload: string(encoded), TradeNo: notice.TradeNo, Amount: notice.Money.Amount, Currency: notice.Money.Currency, Paid: notice.Paid, Status: PaymentEventPending, CreatedAt: common.GetTimestamp(), CallerIP: callerIP}
	if err := DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&event).Error; err != nil {
		return nil, err
	}
	if err := DB.WithContext(ctx).Where("event_key = ?", key).First(&event).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func FailPaymentEvent(ctx context.Context, id int64, cause error) error {
	updates := map[string]any{"attempts": gorm.Expr("attempts + 1"), "last_error": "verification_unavailable", "next_attempt_at": common.GetTimestamp() + 30}
	if errors.Is(cause, payment.ErrInvalidPayment) {
		updates["status"] = PaymentEventRejected
		updates["last_error"] = "verification_failed"
		updates["completed_at"] = common.GetTimestamp()
	}
	if err := DB.WithContext(ctx).Model(&PaymentEvent{}).Where("id = ? AND status = ?", id, PaymentEventPending).Updates(updates).Error; err != nil {
		return err
	}
	return cause
}

// The provider confirmation happens outside the transaction. All local effects,
// including the inbox receipt, commit together under order/user row locks.
func ProcessPaymentEvent(ctx context.Context, id int64, confirmed *payment.Notice) error {
	var event PaymentEvent
	var settlement *paymentSettlement
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&event, id).Error; err != nil {
			return err
		}
		if event.Status != PaymentEventPending {
			return nil
		}
		var parent PaymentOrder
		if err := lockForUpdate(tx).Where("order_no = ?", event.OrderNo).First(&parent).Error; err != nil {
			return err
		}
		if confirmed == nil || confirmed.OrderNo != parent.OrderNo || confirmed.Subtotal != parent.Amount || confirmed.Money.Currency != parent.Currency || confirmed.Money.Amount < 0 || confirmed.Money.Amount > common.MaxWalletQuota {
			return payment.ErrInvalidPayment
		}
		event.Attempts++
		event.CompletedAt = common.GetTimestamp()
		event.TradeNo = confirmed.TradeNo
		event.Amount = confirmed.Money.Amount
		event.Currency = confirmed.Money.Currency
		event.Paid = confirmed.Paid
		order := &parent
		if !confirmed.Paid {
			event.Status = PaymentEventIgnored
			if confirmed.State == "review" {
				event.Status = PaymentEventReview
				event.LastError = "provider_review"
			}
			if parent.Status == common.TopUpStatusPending && (confirmed.State == "failed" || confirmed.State == "expired") {
				status := common.TopUpStatusFailed
				if confirmed.State == "expired" {
					status = common.TopUpStatusExpired
				}
				if err := tx.Model(&parent).Update("status", status).Error; err != nil {
					return err
				}
				target := any(&TopUp{})
				if parent.Business == PaymentBusinessSubscription {
					target = &SubscriptionOrder{}
				}
				if err := tx.Model(target).Where("trade_no = ? AND status = ?", parent.OrderNo, common.TopUpStatusPending).Update("status", status).Error; err != nil {
					return err
				}
			}
			return tx.Save(&event).Error
		}
		if confirmed.Recurring {
			if parent.Provider != payment.Stripe || parent.Business != PaymentBusinessSubscription {
				return payment.ErrInvalidPayment
			}
			if parent.Status != common.TopUpStatusSuccess || parent.ProviderSubscriptionID == "" {
				return errors.New("initial subscription payment has not settled")
			}
			if parent.ProviderSubscriptionID != confirmed.SubscriptionID {
				return payment.ErrInvalidPayment
			}
			var previous UserSubscription
			if err := tx.First(&previous, parent.UserSubscriptionID).Error; err != nil {
				return err
			}
			if previous.Status == "cancelled" {
				event.Status = PaymentEventReview
				event.LastError = "subscription_cancelled"
				return tx.Save(&event).Error
			}
			if confirmed.PeriodStart > common.GetTimestamp() {
				event.CompletedAt = 0
				event.NextAttemptAt = confirmed.PeriodStart
				return tx.Save(&event).Error
			}
			if confirmed.PeriodEnd <= common.GetTimestamp() {
				event.Status = PaymentEventReview
				event.LastError = "billing_period_elapsed"
				return tx.Save(&event).Error
			}
			renewed, err := renewalPaymentOrderTx(tx, &parent, confirmed)
			if err != nil {
				return err
			}
			order = renewed
		}
		if order.ProviderTradeNo != nil && *order.ProviderTradeNo != confirmed.TradeNo {
			return payment.ErrInvalidPayment
		}
		var count int64
		if err := tx.Model(&PaymentOrder{}).Where("account_key = ? AND provider_trade_no = ? AND order_no <> ?", order.AccountKey, confirmed.TradeNo, order.OrderNo).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return payment.ErrInvalidPayment
		}
		order.PaidAmount = confirmed.Money.Amount
		order.ProviderSubscriptionID = confirmed.SubscriptionID
		order.PeriodStart, order.PeriodEnd = confirmed.PeriodStart, confirmed.PeriodEnd
		if err := tx.Model(order).Updates(map[string]any{"provider_trade_no": confirmed.TradeNo, "paid_amount": order.PaidAmount, "provider_subscription_id": order.ProviderSubscriptionID, "period_start": order.PeriodStart, "period_end": order.PeriodEnd}).Error; err != nil {
			return err
		}
		var err error
		settlement, err = completePaymentOrderTx(tx, order)
		if err != nil {
			return err
		}
		if confirmed.CustomerID != "" {
			customer := PaymentCustomer{AccountKey: order.AccountKey, UserID: order.UserID, CustomerID: confirmed.CustomerID}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account_key"}, {Name: "user_id"}}, DoUpdates: clause.AssignmentColumns([]string{"customer_id"})}).Create(&customer).Error; err != nil {
				return err
			}
		}
		event.SettlementOrderNo = order.OrderNo
		event.Status, event.LastError = PaymentEventCompleted, ""
		return tx.Save(&event).Error
	})
	if err != nil {
		return FailPaymentEvent(ctx, id, err)
	}
	if event.Status == PaymentEventRejected {
		return payment.ErrInvalidPayment
	}
	settlement.publish(event.CallerIP)
	return nil
}

func renewalPaymentOrderTx(tx *gorm.DB, parent *PaymentOrder, notice *payment.Notice) (*PaymentOrder, error) {
	if notice.InvoiceID == "" || notice.PeriodEnd <= notice.PeriodStart {
		return nil, payment.ErrInvalidPayment
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(parent.AccountKey+"\x00"+notice.InvoiceID)))
	orderNo := "REN" + digest[:60]
	var existing PaymentOrder
	if err := lockForUpdate(tx).Where("order_no = ?", orderNo).First(&existing).Error; err == nil {
		return &existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	order := *parent
	order.OrderNo = orderNo
	order.ParentOrderNo = parent.OrderNo
	order.CheckoutID = nil
	order.ProviderTradeNo = nil
	order.Status = common.TopUpStatusPending
	order.CompletedAt = 0
	order.CreatedAt = common.GetTimestamp()
	order.UserSubscriptionID = 0
	var plan SubscriptionPlan
	if err := common.UnmarshalJsonStr(parent.PlanSnapshot, &plan); err != nil {
		return nil, err
	}
	money, err := strconv.ParseFloat(notice.Money.Decimal(), 64)
	if err != nil {
		return nil, err
	}
	sub := SubscriptionOrder{UserId: parent.UserID, PlanId: plan.Id, Money: money, TradeNo: orderNo, PaymentMethod: payment.Stripe, PaymentProvider: payment.Stripe, Status: common.TopUpStatusPending, CreateTime: order.CreatedAt}
	if err := tx.Create(&sub).Error; err != nil {
		return nil, err
	}
	if err := tx.Create(&order).Error; err != nil {
		return nil, err
	}
	return &order, nil
}
