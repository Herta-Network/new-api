package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// The payment system that service/payment replaced kept its orders in these
// tables and its provider references in these columns.
var (
	retiredPaymentTables  = []string{"top_ups", "subscription_orders"}
	retiredPaymentColumns = []struct{ table, column string }{
		{"subscription_plans", "stripe_price_id"},
		{"subscription_plans", "creem_product_id"},
		{"subscription_plans", "waffo_pancake_product_id"},
		{"subscription_plans", "currency"},
		{"users", "stripe_customer"},
	}
	// Its settings were these options and every option whose key starts
	// with one of the prefixes; some of them hold gateway secrets.
	retiredPaymentOptionKeys     = []string{"Price", "MinTopUp", "PayMethods", "PayAddress", "CustomCallbackAddress"}
	retiredPaymentOptionPrefixes = []string{"Epay", "Stripe", "Creem", "Waffo"}
)

// dropRetiredPayments deletes what the replaced payment system left in the
// database. Nothing is carried over: operators back the database up before
// upgrading. Running it again changes nothing.
func dropRetiredPayments(db *gorm.DB) error {
	migrator := db.Migrator()
	for _, table := range retiredPaymentTables {
		if !migrator.HasTable(table) {
			continue
		}
		if err := migrator.DropTable(table); err != nil {
			return fmt.Errorf("drop retired payment table %s: %w", table, err)
		}
		common.SysLog(common.LogText("dropped retired payment table %s", table))
	}
	for _, retired := range retiredPaymentColumns {
		if !migrator.HasColumn(retired.table, retired.column) {
			continue
		}
		if err := migrator.DropColumn(retired.table, retired.column); err != nil {
			return fmt.Errorf("drop retired payment column %s.%s: %w", retired.table, retired.column, err)
		}
		common.SysLog(common.LogText("dropped retired payment column %s.%s", retired.table, retired.column))
	}

	query := db.Where(commonKeyCol+" IN ?", retiredPaymentOptionKeys)
	for _, prefix := range retiredPaymentOptionPrefixes {
		query = query.Or(commonKeyCol+" LIKE ?", prefix+"%")
	}
	result := query.Delete(&Option{})
	if result.Error != nil {
		return fmt.Errorf("delete retired payment options: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		common.SysLog(common.LogText("deleted %d retired payment options", result.RowsAffected))
	}
	return nil
}
