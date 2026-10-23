package store

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnsureBalanceMethod seeds the wallet balance method. An existing
// row keeps what the administrator set.
func EnsureBalanceMethod(db *gorm.DB) error {
	token, err := common.GenerateRandomCharsKey(40)
	if err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&entity.PaymentMethod{
		Id:       entity.BalancePaymentMethodID,
		Name:     "Balance",
		Platform: entity.PaymentPlatformBalance,
		Enabled:  true,
		Token:    token,
	}).Error
}

// EnsureStripeMethodTypes pins the Stripe methods saved before each
// Stripe method collected one payment method type to card, which every
// Stripe account offers. Deleted methods are included because late
// callbacks still open their gateway. A method that has a type keeps it, so
// running it again changes nothing.
func EnsureStripeMethodTypes(db *gorm.DB) error {
	var methods []*entity.PaymentMethod
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
		if err := db.Unscoped().Model(&entity.PaymentMethod{}).Where("id = ?", method.Id).Update("config", string(content)).Error; err != nil {
			return fmt.Errorf("set the payment method type of Stripe payment method %d: %w", method.Id, err)
		}
		common.SysLog(common.LogText("Stripe payment method %d now collects card payments", method.Id))
	}
	return nil
}

// GetMethod loads a method that is not deleted.
func GetMethod(id int) (*entity.PaymentMethod, error) {
	var method entity.PaymentMethod
	if err := model.DB.Where("id = ?", id).First(&method).Error; err != nil {
		return nil, err
	}
	return &method, nil
}

// GetMethodIncludingDeleted loads a method even when it was deleted:
// orders keep pointing at the method they were paid with.
func GetMethodIncludingDeleted(id int) (*entity.PaymentMethod, error) {
	var method entity.PaymentMethod
	if err := model.DB.Unscoped().Where("id = ?", id).First(&method).Error; err != nil {
		return nil, err
	}
	return &method, nil
}

// GetMethodByToken finds the method of a notify URL, deleted or not.
func GetMethodByToken(token string) (*entity.PaymentMethod, error) {
	var method entity.PaymentMethod
	if err := model.DB.Unscoped().Where("token = ?", token).First(&method).Error; err != nil {
		return nil, err
	}
	return &method, nil
}

// ListMethods returns the methods that are not deleted, in display
// order.
func ListMethods() ([]*entity.PaymentMethod, error) {
	var methods []*entity.PaymentMethod
	err := model.DB.Order("sort desc, id asc").Find(&methods).Error
	return methods, err
}

func CreateMethod(method *entity.PaymentMethod) error {
	return model.DB.Create(method).Error
}

// UpdateMethod saves the editable fields of method; the platform and
// the token never change.
func UpdateMethod(method *entity.PaymentMethod) error {
	return model.DB.Model(method).
		Select("name", "icon", "description", "sort", "enabled", "domain", "config", "fee_mode", "fee_rate_bp", "fee_fixed").
		Updates(method).Error
}

func DeleteMethod(method *entity.PaymentMethod) error {
	return model.DB.Delete(method).Error
}

// CountPendingOrders counts the pending orders of a method: their
// checkout and callbacks depend on its configuration.
func CountPendingOrders(methodID int) (int64, error) {
	var count int64
	err := model.DB.Model(&entity.PaymentOrder{}).
		Where("payment_method_id = ? AND status = ?", methodID, entity.PaymentOrderPending).
		Count(&count).Error
	return count, err
}
