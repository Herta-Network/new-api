package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/payment"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// PaymentMethod controls new checkouts. Versions remain available for in-flight callbacks.
type PaymentMethod struct {
	ID        int    `json:"id"`
	Name      string `json:"name" gorm:"type:varchar(128);not null"`
	Provider  string `json:"provider" gorm:"type:varchar(32);not null"`
	Icon      string `json:"icon" gorm:"type:varchar(512)"`
	Sort      int    `json:"sort"`
	Enabled   bool   `json:"enabled"`
	Archived  bool   `json:"-"`
	VersionID int    `json:"version_id" gorm:"not null"`
}

type PaymentMethodVersion struct {
	ID        int    `json:"id"`
	MethodID  int    `json:"method_id" gorm:"index;not null"`
	Config    string `json:"-" gorm:"type:jsonb;not null"`
	CreatedAt int64  `json:"created_at"`
}

type PaymentMethodConfig struct {
	PaymentMethod
	Config payment.EpayConfig `json:"config"`
	HasKey bool               `json:"has_key"`
}

var ErrPaymentConfigChanged = errors.New("payment configuration changed")

func (m PaymentMethod) PublicType() string { return fmt.Sprintf("%s:%d", m.Provider, m.ID) }

func ListPaymentMethods(enabledOnly bool) ([]PaymentMethodConfig, error) {
	var methods []PaymentMethod
	query := DB.Where("archived = ?", false).Order("sort ASC, id ASC")
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	if err := query.Find(&methods).Error; err != nil {
		return nil, err
	}
	result := make([]PaymentMethodConfig, 0, len(methods))
	for _, method := range methods {
		item := PaymentMethodConfig{PaymentMethod: method}
		if !enabledOnly {
			var version PaymentMethodVersion
			if err := DB.First(&version, method.VersionID).Error; err != nil {
				return nil, err
			}
			if err := common.UnmarshalJsonStr(version.Config, &item.Config); err != nil {
				return nil, err
			}
			item.HasKey = item.Config.Key != ""
			item.Config.Key = ""
		}
		result = append(result, item)
	}
	return result, nil
}

// SavePaymentMethod creates a new immutable config version under the method lock.
// An empty key retains the previous secret, which is never returned by the API.
func SavePaymentMethod(method *PaymentMethod, config payment.EpayConfig) error {
	method.Name = strings.TrimSpace(method.Name)
	config.Endpoint = strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	config.MerchantID = strings.TrimSpace(config.MerchantID)
	config.PaymentType = strings.TrimSpace(config.PaymentType)
	if method.Name == "" || len(method.Name) > 128 || len(method.Icon) > 512 || method.Provider != payment.Epay || method.Sort < -100000 || method.Sort > 100000 {
		return errors.New("invalid payment method")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if method.ID != 0 {
			var previous PaymentMethod
			if err := lockForUpdate(tx).First(&previous, method.ID).Error; err != nil {
				return err
			}
			if previous.Archived || previous.Provider != method.Provider || previous.VersionID != method.VersionID {
				return ErrPaymentConfigChanged
			}
			if config.Key == "" {
				var version PaymentMethodVersion
				if err := tx.First(&version, previous.VersionID).Error; err != nil {
					return err
				}
				var previousConfig payment.EpayConfig
				if err := common.UnmarshalJsonStr(version.Config, &previousConfig); err != nil {
					return err
				}
				config.Key = previousConfig.Key
			}
		}
		if _, err := payment.NewEpay(config); err != nil {
			return err
		}
		encoded, err := common.Marshal(config)
		if err != nil {
			return err
		}
		method.Archived = false
		if method.ID == 0 {
			if err := tx.Create(method).Error; err != nil {
				return err
			}
		}
		version := PaymentMethodVersion{MethodID: method.ID, Config: string(encoded), CreatedAt: common.GetTimestamp()}
		// SQL debug logging must not print the merchant secret in this INSERT.
		if err := tx.Session(&gorm.Session{Logger: tx.Logger.LogMode(logger.Silent)}).Create(&version).Error; err != nil {
			return err
		}
		method.VersionID = version.ID
		return tx.Save(method).Error
	})
}

func ArchivePaymentMethod(id int) error {
	return DB.Model(&PaymentMethod{}).Where("id = ?", id).Updates(map[string]any{"archived": true, "enabled": false}).Error
}

func GetPaymentMethodVersion(methodID int, requireEnabled bool) (*PaymentMethod, *PaymentMethodVersion, error) {
	var method PaymentMethod
	query := DB.Where("id = ?", methodID)
	if requireEnabled {
		query = query.Where("enabled = ? AND archived = ?", true, false)
	}
	if err := query.First(&method).Error; err != nil {
		return nil, nil, err
	}
	var version PaymentMethodVersion
	if err := DB.First(&version, method.VersionID).Error; err != nil {
		return nil, nil, err
	}
	return &method, &version, nil
}
