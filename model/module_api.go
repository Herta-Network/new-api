package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// The modules under internal/module keep their own tables and reach the
// shared kernel's tables only through the exported functions of this
// package. The functions in this file export kernel operations they run
// inside their own transactions.

// ErrInsufficientBalance reports a wallet that holds less than a debit.
var ErrInsufficientBalance error = common.NewMessage("Insufficient balance")

// LockForUpdate makes the next query of tx lock the rows it reads until the
// transaction ends; see lockForUpdate.
func LockForUpdate(tx *gorm.DB) *gorm.DB {
	return lockForUpdate(tx)
}

// LockUserTx locks the row of userId until tx ends. Operations that check a
// per-user limit before writing take it first, so concurrent ones of one
// user run one by one.
func LockUserTx(tx *gorm.DB, userId int) error {
	var user User
	return lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&user).Error
}

// CreditWalletTx adds quota to the wallet of userId within the wallet
// ceiling, in tx. Call SyncWalletCredit after tx commits.
func CreditWalletTx(tx *gorm.DB, userId int, quota int) error {
	return creditTopUpQuota(tx, userId, quota, nil)
}

// DebitWalletTx takes quota from the wallet of userId in tx, only if the
// wallet holds it; otherwise it returns ErrInsufficientBalance. Call
// SyncWalletDebit after tx commits.
func DebitWalletTx(tx *gorm.DB, userId int, quota int) error {
	if quota <= 0 {
		return errors.New("invalid debit quota")
	}
	result := tx.Model(&User{}).Where("id = ? AND quota >= ?", userId, quota).
		Update("quota", gorm.Expr("quota - ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInsufficientBalance
	}
	return nil
}

// SyncWalletCredit adds a committed credit to the cached wallet balance;
// operation names it in the log of a failure.
func SyncWalletCredit(userId int, quota int, operation string) {
	syncCreditUserQuotaCache(userId, quota, operation)
}

// SyncWalletDebit takes a committed debit from the cached wallet balance.
func SyncWalletDebit(userId int, quota int) error {
	return cacheDecrUserQuota(userId, int64(quota))
}

// GetSubscriptionPlanTx reads a subscription plan in tx; a nil tx reads it
// through the plan cache.
func GetSubscriptionPlanTx(tx *gorm.DB, id int) (*SubscriptionPlan, error) {
	return getSubscriptionPlanByIdTx(tx, id)
}

// CountUserPlanSubscriptionsTx counts the subscriptions userId ever had of
// planId, in tx.
func CountUserPlanSubscriptionsTx(tx *gorm.DB, userId int, planId int) (int64, error) {
	var count int64
	err := tx.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", userId, planId).Count(&count).Error
	return count, err
}
