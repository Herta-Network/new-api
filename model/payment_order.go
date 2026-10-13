package model

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/payment"
	"gorm.io/gorm"
)

const (
	PaymentBusinessTopUp        = "topup"
	PaymentBusinessSubscription = "subscription"
)

// PaymentOrder is the immutable checkout snapshot; only settlement fields change.
type PaymentOrder struct {
	OrderNo                string  `json:"order_no" gorm:"primaryKey;type:varchar(64)"`
	MethodID               int     `json:"method_id" gorm:"index;not null"`
	VersionID              int     `json:"-" gorm:"not null"`
	Provider               string  `json:"provider" gorm:"type:varchar(32);not null"`
	AccountKey             string  `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_payment_account_trade,priority:1"`
	ProviderTradeNo        *string `json:"provider_trade_no" gorm:"type:varchar(128);uniqueIndex:idx_payment_account_trade,priority:2"`
	Business               string  `json:"business" gorm:"type:varchar(32);not null"`
	UserID                 int     `json:"user_id" gorm:"index;not null"`
	Amount                 int64   `json:"amount" gorm:"not null;check:payment_amount_positive,amount > 0"`
	Currency               string  `json:"currency" gorm:"type:varchar(3);not null"`
	CheckoutID             *string `json:"checkout_id" gorm:"type:varchar(128)"`
	CheckoutSnapshot       string  `json:"-" gorm:"type:jsonb"`
	CheckoutResult         *string `json:"-" gorm:"type:jsonb"`
	PaidAmount             int64   `json:"paid_amount"`
	ParentOrderNo          string  `json:"parent_order_no,omitempty" gorm:"type:varchar(64);index"`
	ProviderSubscriptionID string  `json:"provider_subscription_id,omitempty" gorm:"type:varchar(128);index"`
	PeriodStart            int64   `json:"period_start"`
	PeriodEnd              int64   `json:"period_end"`
	UserSubscriptionID     int     `json:"user_subscription_id" gorm:"index"`
	RenewalStopped         bool    `json:"renewal_stopped"`
	CreditedQuota          int     `json:"credited_quota"`
	PlanSnapshot           string  `json:"-" gorm:"type:jsonb"`
	Status                 string  `json:"status" gorm:"type:varchar(16);not null"`
	CreatedAt              int64   `json:"created_at"`
	CompletedAt            int64   `json:"completed_at"`
}

func CreatePaymentOrder(ctx context.Context, order *PaymentOrder, topUp *TopUp, subscription *SubscriptionOrder) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var method PaymentMethod
		if err := lockForUpdate(tx).First(&method, order.MethodID).Error; err != nil {
			return err
		}
		if !method.Enabled || method.Archived || method.VersionID != order.VersionID || method.Provider != order.Provider {
			return ErrPaymentConfigChanged
		}
		if order.Business == PaymentBusinessTopUp {
			if topUp == nil || topUp.TradeNo != order.OrderNo || topUp.UserId != order.UserID {
				return errors.New("invalid top-up snapshot")
			}
			if _, err := topUpQuotaMaxCurrent(order.CreditedQuota); err != nil {
				return err
			}
			if err := tx.Create(topUp).Error; err != nil {
				return err
			}
		} else if order.Business == PaymentBusinessSubscription {
			if subscription == nil || subscription.TradeNo != order.OrderNo || subscription.UserId != order.UserID {
				return errors.New("invalid subscription snapshot")
			}
			var user User
			if err := lockForUpdate(tx).Select("id").First(&user, order.UserID).Error; err != nil {
				return err
			}
			var plan SubscriptionPlan
			if err := common.UnmarshalJsonStr(order.PlanSnapshot, &plan); err != nil {
				return err
			}
			if plan.Id != subscription.PlanId {
				return errors.New("invalid plan snapshot")
			}
			if plan.MaxPurchasePerUser > 0 {
				pending := tx.Model(&SubscriptionOrder{}).Select("trade_no").Where("user_id = ? AND plan_id = ? AND purchase_reserved = ? AND status = ?", order.UserID, plan.Id, true, common.TopUpStatusPending)
				var existing PaymentOrder
				err := tx.Where("order_no IN (?)", pending).Order("created_at ASC, order_no ASC").First(&existing).Error
				if err == nil {
					if existing.MethodID != order.MethodID {
						return common.NewMessage("A payment for this plan is pending. Continue with the original payment method.")
					}
					*order = existing
					return nil
				}
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				count, err := countSubscriptionPurchases(tx, order.UserID, plan.Id)
				if err != nil {
					return err
				}
				if count >= int64(plan.MaxPurchasePerUser) {
					return common.NewMessage("Purchase limit for this plan has been reached")
				}
				subscription.PurchaseReserved = true
			}
			if err := tx.Create(subscription).Error; err != nil {
				return err
			}
		} else {
			return errors.New("invalid payment business")
		}
		return tx.Create(order).Error
	})
}

func GetPaymentOrder(ctx context.Context, orderNo string) (*PaymentOrder, *PaymentMethodVersion, error) {
	var order PaymentOrder
	if err := DB.WithContext(ctx).First(&order, "order_no = ?", orderNo).Error; err != nil {
		return nil, nil, err
	}
	var version PaymentMethodVersion
	if err := DB.WithContext(ctx).First(&version, "id = ? AND method_id = ?", order.VersionID, order.MethodID).Error; err != nil {
		return nil, nil, err
	}
	return &order, &version, nil
}

type paymentSettlement struct {
	order        PaymentOrder
	quota        int
	groupChanged bool
	planTitle    string
	method       string
}

// completePaymentOrderTx shares the event transaction, so an acknowledged event
// cannot be committed without its corresponding credit/subscription grant.
func completePaymentOrderTx(tx *gorm.DB, order *PaymentOrder) (*paymentSettlement, error) {
	if order.Status == common.TopUpStatusSuccess {
		return nil, nil
	}
	if order.Status != common.TopUpStatusPending && order.Status != common.TopUpStatusFailed && order.Status != common.TopUpStatusExpired {
		return nil, ErrTopUpStatusInvalid
	}
	result := &paymentSettlement{order: *order}
	actualMoney, err := strconv.ParseFloat((payment.Money{Amount: order.PaidAmount, Currency: order.Currency}).Decimal(), 64)
	if err != nil {
		return nil, err
	}
	switch order.Business {
	case PaymentBusinessTopUp:
		var topUp TopUp
		if err := lockForUpdate(tx).Where("trade_no = ?", order.OrderNo).First(&topUp).Error; err != nil {
			return nil, err
		}
		if topUp.PaymentProvider != order.Provider || topUp.UserId != order.UserID || topUp.Status == common.TopUpStatusSuccess {
			return nil, ErrTopUpStatusInvalid
		}
		if err := creditTopUpQuota(tx, order.UserID, order.CreditedQuota, nil); err != nil {
			return nil, err
		}
		if err := tx.Model(&topUp).Updates(map[string]any{"status": common.TopUpStatusSuccess, "complete_time": common.GetTimestamp(), "money": actualMoney}).Error; err != nil {
			return nil, err
		}
		result.quota = order.CreditedQuota
		result.method = topUp.PaymentMethod
	case PaymentBusinessSubscription:
		var subscription SubscriptionOrder
		if err := lockForUpdate(tx).Where("trade_no = ?", order.OrderNo).First(&subscription).Error; err != nil {
			return nil, err
		}
		if subscription.PaymentProvider != order.Provider || subscription.UserId != order.UserID || subscription.Status == common.TopUpStatusSuccess {
			return nil, ErrSubscriptionOrderStatusInvalid
		}
		var plan SubscriptionPlan
		if err := common.UnmarshalJsonStr(order.PlanSnapshot, &plan); err != nil {
			return nil, err
		}
		if plan.Id != subscription.PlanId {
			return nil, errors.New("invalid plan snapshot")
		}
		// Capacity is allocated before checkout. Once paid, fulfill the agreed
		// snapshot, including late payments after a provider closed the checkout.
		plan.MaxPurchasePerUser = 0
		subscription.Money = actualMoney
		granted, err := completeSubscriptionOrderTx(tx, &subscription, &plan)
		if err != nil {
			return nil, err
		}
		order.UserSubscriptionID = granted.Id
		if order.ParentOrderNo != "" {
			granted.Source = "renewal"
			if err := tx.Model(granted).Update("source", granted.Source).Error; err != nil {
				return nil, err
			}
		}
		if order.PeriodEnd > order.PeriodStart && order.PeriodStart > 0 {
			granted.StartTime = order.PeriodStart
			granted.EndTime = order.PeriodEnd
			granted.LastResetTime = order.PeriodStart
			granted.NextResetTime = calcNextResetTime(time.Unix(order.PeriodStart, 0), &plan, order.PeriodEnd)
			if err := tx.Save(granted).Error; err != nil {
				return nil, err
			}
		}
		result.groupChanged, result.planTitle = granted.PrevUserGroup != "", plan.Title
		result.method = subscription.PaymentMethod
	default:
		return nil, errors.New("invalid payment business")
	}
	order.Status = common.TopUpStatusSuccess
	order.CompletedAt = common.GetTimestamp()
	if err := tx.Model(order).Updates(map[string]any{"status": order.Status, "completed_at": order.CompletedAt, "user_subscription_id": order.UserSubscriptionID}).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func (s *paymentSettlement) publish(callerIP string) {
	if s == nil {
		return
	}
	if s.quota > 0 {
		syncCreditUserQuotaCache(s.order.UserID, s.quota, "payment")
		RecordTopupLog(s.order.UserID, common.NewMessage("Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"quota": logger.FormatQuota(s.quota), "amount": (payment.Money{Amount: s.order.PaidAmount, Currency: s.order.Currency}).Decimal()}), callerIP, s.method, s.order.Provider)
	} else {
		if s.groupChanged {
			refreshSubscriptionUserGroupCache(s.order.UserID, "subscription payment")
		}
		RecordLog(s.order.UserID, LogTypeTopup, common.NewMessage("Subscription purchased, plan: {{plan}}, payment amount: {{amount}}, payment method: {{method}}", map[string]any{"plan": s.planTitle, "amount": (payment.Money{Amount: s.order.PaidAmount, Currency: s.order.Currency}).Decimal(), "method": s.method}))
	}
}

func CompleteGatewayOrderManually(orderNo, callerIP string) error {
	var result *paymentSettlement
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order PaymentOrder
		if err := lockForUpdate(tx).First(&order, "order_no = ?", orderNo).Error; err != nil {
			return err
		}
		var err error
		if order.Status != common.TopUpStatusSuccess {
			order.PaidAmount = order.Amount
			if err := tx.Model(&order).Update("paid_amount", order.PaidAmount).Error; err != nil {
				return err
			}
		}
		result, err = completePaymentOrderTx(tx, &order)
		return err
	})
	if err == nil {
		result.publish(callerIP)
	}
	return err
}

// PaymentCustomer prevents sharing Stripe customer IDs across merchant accounts.
type PaymentCustomer struct {
	AccountKey string `gorm:"primaryKey;type:varchar(64)"`
	UserID     int    `gorm:"primaryKey"`
	CustomerID string `gorm:"type:varchar(128);not null"`
}

func GetPaymentCustomer(account string, userID int) string {
	var customer PaymentCustomer
	if DB.Where("account_key = ? AND user_id = ?", account, userID).First(&customer).Error != nil {
		return ""
	}
	return customer.CustomerID
}

func BindPaymentCheckout(ctx context.Context, orderNo string, checkout *payment.CheckoutResult) error {
	encoded, err := common.Marshal(checkout)
	if err != nil {
		return err
	}
	updates := map[string]any{"checkout_result": string(encoded)}
	if checkout.CheckoutID != "" {
		updates["checkout_id"] = checkout.CheckoutID
	}
	result := DB.WithContext(ctx).Model(&PaymentOrder{}).Where("order_no = ? AND (checkout_id IS NULL OR checkout_id = ?)", orderNo, checkout.CheckoutID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return payment.ErrInvalidPayment
	}
	return nil
}
