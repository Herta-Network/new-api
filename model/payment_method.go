package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PaymentPlatformBalance is the platform of the wallet balance method, the
// fixed payment method BalancePaymentMethodID that subscriptions can be paid
// with. Every other platform is served by a gateway of
// service/payment/gateway.
const (
	PaymentPlatformBalance = "balance"
	BalancePaymentMethodID = -1
)

// Fee modes of a payment method. The fee is charged on top of the order
// price and never changes the credited quota.
const (
	PaymentFeeNone         = 0
	PaymentFeeRate         = 1
	PaymentFeeFixed        = 2
	PaymentFeeRateAndFixed = 3
)

var ErrBalancePaymentMethodUndeletable = errors.New("the balance payment method cannot be deleted")

// PaymentMethod is one configured way to pay: a gateway account (an EPay
// merchant and payment type, a Stripe account, a Cryptomus merchant) or the
// wallet balance. Methods are soft-deleted so a late callback still finds the
// method its notify URL names.
type PaymentMethod struct {
	Id          int    `json:"id"`
	Name        string `json:"name" gorm:"type:varchar(100);not null;default:''"`
	Platform    string `json:"platform" gorm:"<-:create;type:varchar(32);not null"`
	Icon        string `json:"icon" gorm:"type:varchar(255);not null;default:''"`
	Description string `json:"description" gorm:"type:text;not null;default:''"`
	Sort        int    `json:"sort" gorm:"not null;default:0"`
	Enabled     bool   `json:"enabled" gorm:"not null"`
	// Domain is the base URL gateway callbacks are sent to; empty selects
	// the site address.
	Domain string `json:"domain" gorm:"type:varchar(255);not null;default:''"`
	// Config is the platform's JSON configuration. It holds secrets and is
	// never serialized as is.
	Config string `json:"-" gorm:"type:text;not null;default:''"`
	// The fee is FeeRateBp basis points of the price and/or FeeFixed
	// hundredths of the site currency, as FeeMode selects.
	FeeMode   int   `json:"fee_mode" gorm:"not null;default:0"`
	FeeRateBp int64 `json:"fee_rate_bp" gorm:"not null;default:0"`
	FeeFixed  int64 `json:"fee_fixed" gorm:"not null;default:0"`
	// Token is the secret path segment of the method's notify URL.
	Token     string         `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	CreatedAt int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

func (m *PaymentMethod) BeforeDelete(*gorm.DB) error {
	if m.Id == BalancePaymentMethodID {
		return ErrBalancePaymentMethodUndeletable
	}
	return nil
}

// ensureBalancePaymentMethod seeds the wallet balance method. An existing
// row keeps what the administrator set.
func ensureBalancePaymentMethod(db *gorm.DB) error {
	token, err := common.GenerateRandomCharsKey(40)
	if err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&PaymentMethod{
		Id:       BalancePaymentMethodID,
		Name:     "Balance",
		Platform: PaymentPlatformBalance,
		Enabled:  true,
		Token:    token,
	}).Error
}

// ensureStripePaymentMethodTypes pins the Stripe methods saved before each
// Stripe method collected one payment method type to card, which every
// Stripe account offers. Deleted methods are included because late
// callbacks still open their gateway. A method that has a type keeps it, so
// running it again changes nothing.
func ensureStripePaymentMethodTypes(db *gorm.DB) error {
	var methods []*PaymentMethod
	if err := db.Unscoped().Where("platform = ?", "stripe").Find(&methods).Error; err != nil {
		return fmt.Errorf("list Stripe payment methods: %w", err)
	}
	for _, method := range methods {
		var config map[string]any
		if err := common.UnmarshalJsonStr(method.Config, &config); err != nil || config == nil {
			common.SysError(common.LogText("Stripe payment method %d has no readable configuration; set its payment method type by hand", method.Id))
			continue
		}
		if paymentType, _ := config["payment_method_type"].(string); paymentType != "" {
			continue
		}
		config["payment_method_type"] = "card"
		content, err := common.Marshal(config)
		if err != nil {
			return err
		}
		if err := db.Unscoped().Model(&PaymentMethod{}).Where("id = ?", method.Id).Update("config", string(content)).Error; err != nil {
			return fmt.Errorf("set the payment method type of Stripe payment method %d: %w", method.Id, err)
		}
		common.SysLog(common.LogText("Stripe payment method %d now collects card payments", method.Id))
	}
	return nil
}

// GetPaymentMethod loads a method that is not deleted.
func GetPaymentMethod(id int) (*PaymentMethod, error) {
	var method PaymentMethod
	if err := DB.Where("id = ?", id).First(&method).Error; err != nil {
		return nil, err
	}
	return &method, nil
}

// GetPaymentMethodIncludingDeleted loads a method even when it was deleted:
// orders keep pointing at the method they were paid with.
func GetPaymentMethodIncludingDeleted(id int) (*PaymentMethod, error) {
	var method PaymentMethod
	if err := DB.Unscoped().Where("id = ?", id).First(&method).Error; err != nil {
		return nil, err
	}
	return &method, nil
}

// GetPaymentMethodByToken finds the method of a notify URL, deleted or not.
func GetPaymentMethodByToken(token string) (*PaymentMethod, error) {
	var method PaymentMethod
	if err := DB.Unscoped().Where("token = ?", token).First(&method).Error; err != nil {
		return nil, err
	}
	return &method, nil
}

// ListPaymentMethods returns the methods that are not deleted, in display
// order.
func ListPaymentMethods() ([]*PaymentMethod, error) {
	var methods []*PaymentMethod
	err := DB.Order("sort desc, id asc").Find(&methods).Error
	return methods, err
}

func CreatePaymentMethod(method *PaymentMethod) error {
	return DB.Create(method).Error
}

// UpdatePaymentMethod saves the editable fields of method; the platform and
// the token never change.
func UpdatePaymentMethod(method *PaymentMethod) error {
	return DB.Model(method).
		Select("name", "icon", "description", "sort", "enabled", "domain", "config", "fee_mode", "fee_rate_bp", "fee_fixed").
		Updates(method).Error
}

func DeletePaymentMethod(method *PaymentMethod) error {
	return DB.Delete(method).Error
}

// CountPendingPaymentOrders counts the pending orders of a method: their
// checkout and callbacks depend on its configuration.
func CountPendingPaymentOrders(methodID int) (int64, error) {
	var count int64
	err := DB.Model(&PaymentOrder{}).
		Where("payment_method_id = ? AND status = ?", methodID, PaymentOrderPending).
		Count(&count).Error
	return count, err
}
