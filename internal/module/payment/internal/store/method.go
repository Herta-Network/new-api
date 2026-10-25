package store

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnsureBalanceMethod seeds the wallet balance option and its channel, the
// balance method. An existing row keeps what the administrator set.
func EnsureBalanceMethod(db *gorm.DB) error {
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&entity.PaymentOption{
		Id:      entity.BalancePaymentOptionID,
		Name:    "Balance",
		Icon:    "FaWallet",
		Enabled: true,
	}).Error; err != nil {
		return err
	}
	token, err := common.GenerateRandomCharsKey(40)
	if err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&entity.PaymentMethod{
		Id:       entity.BalancePaymentMethodID,
		Name:     "Balance",
		Platform: entity.PaymentPlatformBalance,
		OptionId: entity.BalancePaymentOptionID,
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

// EnsureEPayProtocols pins the EPay methods saved before EPay V2 and API
// checkout existed to what they used: V1 through the gateway's payment
// page. Deleted methods are included because late callbacks still open
// their gateway. A method that names its version and checkout mode keeps
// them, so running it again changes nothing.
func EnsureEPayProtocols(db *gorm.DB) error {
	var methods []*entity.PaymentMethod
	if err := db.Unscoped().Where("platform = ?", "epay").Find(&methods).Error; err != nil {
		return fmt.Errorf("list EPay payment methods: %w", err)
	}
	for _, method := range methods {
		var config map[string]any
		if err := common.UnmarshalJsonStr(method.Config, &config); err != nil || config == nil {
			common.SysError(common.LogText("EPay payment method %d has no readable configuration; set its protocol version by hand", method.Id))
			continue
		}
		version, _ := config["version"].(string)
		checkout, _ := config["checkout"].(string)
		if version != "" && checkout != "" {
			continue
		}
		if version == "" {
			config["version"] = "v1"
		}
		if checkout == "" {
			config["checkout"] = "page"
		}
		content, err := common.Marshal(config)
		if err != nil {
			return err
		}
		if err := db.Unscoped().Model(&entity.PaymentMethod{}).Where("id = ?", method.Id).Update("config", string(content)).Error; err != nil {
			return fmt.Errorf("pin the protocol of EPay payment method %d: %w", method.Id, err)
		}
		common.SysLog(common.LogText("EPay payment method %d keeps EPay V1 through the gateway payment page", method.Id))
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

// ListMethods returns the methods that are not deleted, by option, then in
// the order the option tries them.
func ListMethods() ([]*entity.PaymentMethod, error) {
	var methods []*entity.PaymentMethod
	err := model.DB.Order("option_id asc, priority asc, id asc").Find(&methods).Error
	return methods, err
}

// ListOptionMethods returns the methods of an option that are not deleted,
// in the order the option tries them.
func ListOptionMethods(optionID int) ([]*entity.PaymentMethod, error) {
	var methods []*entity.PaymentMethod
	err := model.DB.Where("option_id = ?", optionID).Order("priority asc, id asc").Find(&methods).Error
	return methods, err
}

func CreateMethod(method *entity.PaymentMethod) error {
	return model.DB.Create(method).Error
}

// UpdateMethod saves the editable fields of method; the platform and
// the token never change.
func UpdateMethod(method *entity.PaymentMethod) error {
	return model.DB.Model(method).
		Select("name", "option_id", "enabled", "min_amount", "max_amount", "priority", "weight", "domain", "config").
		Updates(method).Error
}

func DeleteMethod(method *entity.PaymentMethod) error {
	return model.DB.Delete(method).Error
}

// CountPendingInvoices counts the pending invoices of a method: their
// checkout and callbacks depend on its configuration.
func CountPendingInvoices(methodID int) (int64, error) {
	var count int64
	err := model.DB.Model(&entity.PaymentInvoice{}).
		Where("payment_method_id = ? AND status = ?", methodID, entity.PaymentInvoicePending).
		Count(&count).Error
	return count, err
}
