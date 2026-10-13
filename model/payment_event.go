package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

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
)

// PaymentEvent is a durable inbox of verified, normalized facts. It deliberately
// excludes signatures, secrets and unbounded provider payloads.
type PaymentEvent struct {
	ID            int64  `json:"id"`
	EventKey      string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	OrderNo       string `json:"order_no" gorm:"type:varchar(64);not null;index"`
	MethodID      int    `json:"method_id"`
	TradeNo       string `json:"trade_no" gorm:"type:varchar(128);not null"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency" gorm:"type:varchar(3)"`
	Paid          bool   `json:"paid"`
	Status        string `json:"status" gorm:"type:varchar(16);not null"`
	Attempts      int    `json:"attempts"`
	NextAttemptAt int64  `json:"next_attempt_at" gorm:"index:idx_payment_event_retry,where:status = 'pending'"`
	LastError     string `json:"last_error" gorm:"type:varchar(64)"`
	CreatedAt     int64  `json:"created_at"`
	CompletedAt   int64  `json:"completed_at"`
	CallerIP      string `json:"-" gorm:"type:varchar(64)"`
}

// ReceivePaymentEvent must only be called with facts authenticated by the
// gateway for this order's immutable config version.
func ReceivePaymentEvent(ctx context.Context, order *PaymentOrder, notice *payment.Notice, callerIP string) (*PaymentEvent, error) {
	if notice.OrderNo != order.OrderNo {
		return nil, payment.ErrInvalidPayment
	}
	encoded, err := common.Marshal(notice)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256(encoded))
	event := PaymentEvent{EventKey: key, OrderNo: order.OrderNo, MethodID: order.MethodID, TradeNo: notice.TradeNo, Amount: notice.Money.Amount, Currency: notice.Money.Currency, Paid: notice.Paid, Status: PaymentEventPending, CreatedAt: common.GetTimestamp(), CallerIP: callerIP}
	if err := DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&event).Error; err != nil {
		return nil, err
	}
	if err := DB.WithContext(ctx).Where("event_key = ?", key).First(&event).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func ProcessPaymentEvent(ctx context.Context, id int64) error {
	var event PaymentEvent
	var settlement *paymentSettlement
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&event, id).Error; err != nil {
			return err
		}
		if event.Status != PaymentEventPending {
			return nil
		}
		var order PaymentOrder
		if err := lockForUpdate(tx).Where("order_no = ?", event.OrderNo).First(&order).Error; err != nil {
			return err
		}
		event.Attempts++
		event.CompletedAt = common.GetTimestamp()
		switch {
		case event.MethodID != order.MethodID:
			event.Status, event.LastError = PaymentEventRejected, "method_mismatch"
		case event.Amount != order.Amount || event.Currency != order.Currency:
			event.Status, event.LastError = PaymentEventRejected, "amount_mismatch"
		case !event.Paid:
			event.Status = PaymentEventIgnored
		case order.ProviderTradeNo != nil && *order.ProviderTradeNo != event.TradeNo:
			event.Status, event.LastError = PaymentEventRejected, "trade_mismatch"
		case order.Status != common.TopUpStatusPending && order.Status != common.TopUpStatusSuccess:
			event.Status, event.LastError = PaymentEventRejected, "order_not_pending"
		default:
			var count int64
			if err := tx.Model(&PaymentOrder{}).Where("account_key = ? AND provider_trade_no = ? AND order_no <> ?", order.AccountKey, event.TradeNo, order.OrderNo).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				event.Status, event.LastError = PaymentEventRejected, "trade_reused"
				break
			}
			if err := tx.Model(&order).Update("provider_trade_no", event.TradeNo).Error; err != nil {
				return err
			}
			var err error
			settlement, err = completePaymentOrderTx(tx, &order)
			if err != nil {
				return err
			}
			event.Status, event.LastError = PaymentEventCompleted, ""
		}
		return tx.Save(&event).Error
	})
	if err != nil {
		// The settlement transaction rolled back. Keep the inbox item retryable;
		// even if this update fails, its original pending row survives a restart.
		_ = DB.WithContext(ctx).Model(&PaymentEvent{}).Where("id = ? AND status = ?", id, PaymentEventPending).Updates(map[string]any{
			"attempts": gorm.Expr("attempts + 1"), "last_error": "settlement_failed",
			"next_attempt_at": common.GetTimestamp() + int64(min(max(event.Attempts, 1)*30, 3600)),
		}).Error
		return err
	}
	if event.Status == PaymentEventRejected {
		return payment.ErrInvalidPayment
	}
	settlement.publish(event.CallerIP)
	return nil
}

// RetryPaymentEvents is safe to run on several instances: each event and order
// is serialized by PostgreSQL row locks, and fulfillment shares that transaction.
func RetryPaymentEvents(ctx context.Context) error {
	var ids []int64
	if err := DB.WithContext(ctx).Model(&PaymentEvent{}).Where("status = ? AND next_attempt_at <= ?", PaymentEventPending, common.GetTimestamp()).Order("next_attempt_at ASC, id ASC").Limit(100).Pluck("id", &ids).Error; err != nil {
		return err
	}
	var result error
	for _, id := range ids {
		if err := ProcessPaymentEvent(ctx, id); err != nil && !errors.Is(err, payment.ErrInvalidPayment) {
			result = errors.Join(result, err)
		}
	}
	return result
}
