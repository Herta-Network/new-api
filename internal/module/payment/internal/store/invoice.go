package store

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// ErrInvoiceNotFound reports an invoice number no invoice has.
var ErrInvoiceNotFound = errors.New("payment invoice not found")

func GetInvoiceByNo(invoiceNo string) (*entity.PaymentInvoice, error) {
	var invoice entity.PaymentInvoice
	err := model.DB.Where("invoice_no = ?", invoiceNo).First(&invoice).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvoiceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &invoice, nil
}

func GetInvoice(id int) (*entity.PaymentInvoice, error) {
	var invoice entity.PaymentInvoice
	if err := model.DB.Where("id = ?", id).First(&invoice).Error; err != nil {
		return nil, err
	}
	return &invoice, nil
}

// ListInvoices returns the invoices of an order in the order they were
// issued.
func ListInvoices(orderID int) ([]*entity.PaymentInvoice, error) {
	var invoices []*entity.PaymentInvoice
	err := model.DB.Where("order_id = ?", orderID).Order("id").Find(&invoices).Error
	return invoices, err
}

// IssueInvoice adds an invoice of a pending order through method. It is
// numbered after the order's invoices, as the order number, R and the
// invoice's place, and the order shows it as its latest invoice.
func IssueInvoice(order *entity.PaymentOrder, method *entity.PaymentMethod) (*entity.PaymentInvoice, error) {
	invoice := &entity.PaymentInvoice{
		OrderId: order.Id, PaymentMethodId: method.Id, Platform: method.Platform,
		Status: entity.PaymentInvoicePending, CreatedAt: common.GetTimestamp(),
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var current entity.PaymentOrder
		if err := model.LockForUpdate(tx).Where("id = ?", order.Id).First(&current).Error; err != nil {
			return err
		}
		if current.Status != entity.PaymentOrderPending {
			return ErrOrderNotPending
		}
		var issued int64
		if err := tx.Model(&entity.PaymentInvoice{}).Where("order_id = ?", order.Id).Count(&issued).Error; err != nil {
			return err
		}
		invoice.InvoiceNo = fmt.Sprintf("%sR%d", current.OrderNo, issued+1)
		if err := tx.Create(invoice).Error; err != nil {
			return err
		}
		return tx.Model(&entity.PaymentOrder{}).Where("id = ?", order.Id).Updates(map[string]any{
			"payment_method_id": method.Id, "platform": method.Platform, "payment_amount": 0,
			"payment_currency": "", "exchange_rate": "", "trade_no": "",
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return invoice, nil
}

// RecentUnpaidMethods returns the methods of the invoices a user's orders
// paid with an option were issued through since a time and that were not
// paid: the buyer did not get through them.
func RecentUnpaidMethods(userID, optionID int, since int64) ([]int, error) {
	var methods []int
	err := model.DB.Model(&entity.PaymentInvoice{}).
		Joins("JOIN payment_orders ON payment_orders.id = payment_invoices.order_id").
		Where("payment_orders.user_id = ? AND payment_orders.payment_option_id = ?", userID, optionID).
		Where("payment_invoices.status <> ? AND payment_invoices.created_at >= ?", entity.PaymentInvoicePaid, since).
		Distinct().Pluck("payment_invoices.payment_method_id", &methods).Error
	return methods, err
}

// lockInvoiceOrderTx locks the order of an invoice. Every transaction that
// changes an invoice and its order locks the order first, as settlement
// does, so they cannot deadlock.
func lockInvoiceOrderTx(tx *gorm.DB, invoice *entity.PaymentInvoice) error {
	return model.LockForUpdate(tx).Select("id").Where("id = ?", invoice.OrderId).First(&entity.PaymentOrder{}).Error
}

// orderMirrorTx copies fields of invoice onto its order while the order is
// pending and invoice is its latest invoice: the order shows its current
// checkout in lists.
func orderMirrorTx(tx *gorm.DB, invoice *entity.PaymentInvoice, fields map[string]any) error {
	newer := tx.Model(&entity.PaymentInvoice{}).Select("1").Where("order_id = ? AND id > ?", invoice.OrderId, invoice.Id)
	return tx.Model(&entity.PaymentOrder{}).
		Where("id = ? AND status = ?", invoice.OrderId, entity.PaymentOrderPending).
		Where("NOT EXISTS (?)", newer).
		Updates(fields).Error
}

// SetInvoiceExpectation records the payment expectation of a pending
// invoice whose checkout has not started. It reports whether this call
// recorded it; otherwise the invoice already holds one (or is no longer
// pending) and the caller reloads it.
func SetInvoiceExpectation(invoice *entity.PaymentInvoice, amount int64, currency, rate string) (bool, error) {
	recorded := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := lockInvoiceOrderTx(tx, invoice); err != nil {
			return err
		}
		fields := map[string]any{"payment_amount": amount, "payment_currency": currency, "exchange_rate": rate}
		result := tx.Model(&entity.PaymentInvoice{}).
			Where("id = ? AND status = ? AND payment_currency = ''", invoice.Id, entity.PaymentInvoicePending).
			Updates(fields)
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		recorded = true
		return orderMirrorTx(tx, invoice, fields)
	})
	return recorded, err
}

// ClaimInvoiceTrade binds tradeNo as the only gateway payment of a pending
// invoice and returns the trade the invoice holds afterwards: tradeNo when
// the claim won, the trade a concurrent checkout claimed otherwise.
func ClaimInvoiceTrade(invoice *entity.PaymentInvoice, tradeNo string) (string, error) {
	if tradeNo == "" {
		return "", errors.New("empty trade number")
	}
	claimed := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := lockInvoiceOrderTx(tx, invoice); err != nil {
			return err
		}
		result := tx.Model(&entity.PaymentInvoice{}).
			Where("id = ? AND status = ? AND trade_no = ''", invoice.Id, entity.PaymentInvoicePending).
			Update("trade_no", tradeNo)
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		claimed = true
		return orderMirrorTx(tx, invoice, map[string]any{"trade_no": tradeNo})
	})
	if err != nil {
		return "", err
	}
	if claimed {
		return tradeNo, nil
	}
	current, err := GetInvoice(invoice.Id)
	if err != nil {
		return "", err
	}
	if current.TradeNo != "" {
		return current.TradeNo, nil
	}
	return "", ErrOrderNotPending
}

// checkoutChanged reports whether the checkout of an invoice differs from
// the snapshot a gateway verdict was made on.
func checkoutChanged(current, snapshot *entity.PaymentInvoice) bool {
	return current.PaymentCurrency != snapshot.PaymentCurrency || current.PaymentAmount != snapshot.PaymentAmount ||
		current.TradeNo != snapshot.TradeNo
}

// CloseInvoice closes a pending invoice and reports whether this call
// closed it. With requireStableCheckout the invoice is locked first and must
// still have the checkout of snapshot, or ErrCheckoutChanged is returned:
// the gateway verdict the close relies on covers only that checkout.
func CloseInvoice(snapshot *entity.PaymentInvoice, requireStableCheckout bool, reason string) (bool, error) {
	closed := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if requireStableCheckout {
			var current entity.PaymentInvoice
			if err := model.LockForUpdate(tx).Where("id = ?", snapshot.Id).First(&current).Error; err != nil {
				return err
			}
			if current.Status != entity.PaymentInvoicePending {
				return nil
			}
			if checkoutChanged(&current, snapshot) {
				return ErrCheckoutChanged
			}
		}
		result := tx.Model(&entity.PaymentInvoice{}).
			Where("id = ? AND status = ?", snapshot.Id, entity.PaymentInvoicePending).
			Updates(map[string]any{"status": entity.PaymentInvoiceClosed, "closed_at": common.GetTimestamp(), "close_reason": reason})
		closed = result.RowsAffected == 1
		return result.Error
	})
	return closed, err
}
