package store

import (
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"
)

// GetOption loads an option that is not deleted.
func GetOption(id int) (*entity.PaymentOption, error) {
	var option entity.PaymentOption
	if err := model.DB.Where("id = ?", id).First(&option).Error; err != nil {
		return nil, err
	}
	return &option, nil
}

// ListOptions returns the options that are not deleted, in display order.
func ListOptions() ([]*entity.PaymentOption, error) {
	var options []*entity.PaymentOption
	err := model.DB.Order("sort desc, id asc").Find(&options).Error
	return options, err
}

// OptionNames maps option ids to names, deleted options included: orders
// keep naming the option they were paid with.
func OptionNames(ids []int) (map[int]string, error) {
	names := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	var options []*entity.PaymentOption
	if err := model.DB.Unscoped().Select("id", "name").Where("id IN ?", ids).Find(&options).Error; err != nil {
		return nil, err
	}
	for _, option := range options {
		names[option.Id] = option.Name
	}
	return names, nil
}

func CreateOption(option *entity.PaymentOption) error {
	return model.DB.Create(option).Error
}

// UpdateOption saves the editable fields of option.
func UpdateOption(option *entity.PaymentOption) error {
	return model.DB.Model(option).
		Select("name", "icon", "description", "sort", "enabled", "fee_mode", "fee_rate_bp", "fee_fixed").
		Updates(option).Error
}

func DeleteOption(option *entity.PaymentOption) error {
	return model.DB.Delete(option).Error
}

// CountOptionMethods counts the methods of an option that are not deleted.
func CountOptionMethods(optionID int) (int64, error) {
	var count int64
	err := model.DB.Model(&entity.PaymentMethod{}).Where("option_id = ?", optionID).Count(&count).Error
	return count, err
}
