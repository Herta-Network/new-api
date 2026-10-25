package payment

import (
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/gateway"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
	"github.com/QuantumNous/new-api/logger"
)

// Who closes a pending order. Only the system's close waits for the
// gateway to confirm that no payment is coming; a person giving the order up
// forfeits a payment a gateway without a cancellation API cannot confirm.
const (
	CloseBySystem = iota
	CloseByOwner
	CloseByAdmin
)

// Close reasons stored on closed orders and invoices. An invoice also closes
// when its channel could not start the payment, or when a renewed checkout
// replaced it.
const (
	closeReasonExpired  = "expired"
	closeReasonOwner    = "user"
	closeReasonAdmin    = "admin"
	closeReasonFailed   = "failed"
	closeReasonReplaced = "replaced"
)

// ErrCloseUnconfirmed reports that the gateway could not confirm the order
// safe to close; it stays pending and is checked again later.
var ErrCloseUnconfirmed error = common.NewMessage("The payment status could not be confirmed yet. The order stays open; try again later.")

// CloseOrder closes a pending order after asking the gateway of each of
// its pending invoices: a payment a gateway proves settles the order
// instead, and a payment one cannot rule out keeps the order pending
// (ErrCloseUnconfirmed). It returns the order as it is afterwards.
func CloseOrder(ctx context.Context, order *entity.PaymentOrder, actor int) (*entity.PaymentOrder, error) {
	if order.Status != entity.PaymentOrderPending {
		return order, nil
	}
	reason := map[int]string{CloseBySystem: closeReasonExpired, CloseByOwner: closeReasonOwner, CloseByAdmin: closeReasonAdmin}[actor]
	invoices, err := store.ListInvoices(order.Id)
	if err != nil {
		return nil, err
	}
	for _, invoice := range invoices {
		if invoice.Status != entity.PaymentInvoicePending {
			continue
		}
		settled, err := closeInvoice(ctx, order, invoice, actor != CloseBySystem, reason)
		if err != nil || settled != nil {
			return settled, err
		}
	}
	_, err = store.CloseOrder(order, reason)
	if errors.Is(err, store.ErrCheckoutChanged) {
		logger.LogDebug(ctx, "payment order %s stays pending: it got another invoice during the close", order.OrderNo)
		return nil, ErrCloseUnconfirmed
	}
	if err != nil {
		return nil, err
	}
	return store.GetOrderByNo(order.OrderNo)
}

// closeInvoice closes a pending invoice after asking its gateway, the
// explicit close of a person when explicit is set. A payment the gateway
// proves settles the order instead, which is returned; a payment it cannot
// rule out keeps the invoice pending (ErrCloseUnconfirmed).
func closeInvoice(ctx context.Context, order *entity.PaymentOrder, invoice *entity.PaymentInvoice, explicit bool, reason string) (*entity.PaymentOrder, error) {
	// The method may have been disabled or deleted since; its gateway still
	// decides.
	method, err := store.GetMethodIncludingDeleted(invoice.PaymentMethodId)
	if err != nil {
		return nil, err
	}
	gw, err := openGateway(method)
	if err != nil {
		return nil, fmt.Errorf("open the gateway of payment method %d: %w", method.Id, err)
	}
	verdict, err := gw.Reconcile(ctx, gateway.CloseRequest{Order: invoiceView(order, invoice), Explicit: explicit})
	if errors.Is(err, gateway.ErrUnconfirmed) {
		logger.LogDebug(ctx, "payment invoice %s stays pending: %v", invoice.InvoiceNo, err)
		return nil, ErrCloseUnconfirmed
	}
	if err != nil {
		return nil, err
	}
	if verdict.Note != "" {
		common.SysLog(common.LogText("payment invoice %s: %s", invoice.InvoiceNo, verdict.Note))
	}
	if verdict.TradeNo != "" {
		return settleConfirmed(order, invoice, verdict.TradeNo, "")
	}
	_, err = store.CloseInvoice(invoice, verdict.RequireStableCheckout, reason)
	if errors.Is(err, store.ErrCheckoutChanged) {
		logger.LogDebug(ctx, "payment invoice %s stays pending: its checkout changed during the close", invoice.InvoiceNo)
		return nil, ErrCloseUnconfirmed
	}
	return nil, err
}

// SyncOrder asks the gateways whether an invoice of a pending or closed
// order was paid and settles the order if so; nothing changes at the
// gateways. An invoice whose channel failed to start it was never handed to
// the buyer and is not asked about, and a gateway that cannot answer does
// not keep the others from being asked; its error is returned only when no
// gateway answered. It returns the order as it is afterwards.
func SyncOrder(ctx context.Context, order *entity.PaymentOrder, clientIp string) (*entity.PaymentOrder, error) {
	if (order.Status != entity.PaymentOrderPending && order.Status != entity.PaymentOrderClosed) || order.Platform == entity.PaymentPlatformBalance {
		return order, nil
	}
	invoices, err := store.ListInvoices(order.Id)
	if err != nil {
		return nil, err
	}
	var failure error
	answered := false
	for _, invoice := range invoices {
		// Only a checkout that started and reached the buyer can have been
		// paid.
		if invoice.Status == entity.PaymentInvoicePaid || invoice.PaymentCurrency == "" || invoice.CloseReason == closeReasonFailed {
			continue
		}
		method, err := store.GetMethodIncludingDeleted(invoice.PaymentMethodId)
		if err != nil {
			return nil, err
		}
		gw, err := openGateway(method)
		if err == nil {
			var tradeNo string
			if tradeNo, err = gw.QueryPayment(ctx, invoiceView(order, invoice)); err == nil {
				if tradeNo != "" {
					return settleConfirmed(order, invoice, tradeNo, clientIp)
				}
				answered = true
				continue
			}
		}
		logger.LogDebug(ctx, "payment invoice %s could not be checked: %v", invoice.InvoiceNo, err)
		if failure == nil {
			failure = err
		}
	}
	if failure != nil && !answered {
		return nil, failure
	}
	return order, nil
}

// settleConfirmed settles an order with a payment of its invoice the
// gateway confirmed matches the invoice's expectation.
func settleConfirmed(order *entity.PaymentOrder, invoice *entity.PaymentInvoice, tradeNo, clientIp string) (*entity.PaymentOrder, error) {
	result, err := store.SettleInvoice(store.Settlement{
		InvoiceNo: invoice.InvoiceNo, TradeNo: tradeNo, Amount: invoice.PaymentAmount, Currency: invoice.PaymentCurrency, ClientIp: clientIp,
	})
	if err != nil {
		return nil, err
	}
	if result.Outcome == store.SettleUnmatched || result.Outcome == store.SettleFulfilFailed {
		common.SysError(common.LogText("unmatched payment recorded: order=%s trade=%s reason=%s", order.OrderNo, tradeNo, result.Reason))
	}
	return store.GetOrderByNo(order.OrderNo)
}

// CompleteOrderManually marks an order paid on an administrator's word and
// fulfils it.
func CompleteOrderManually(orderNo string, clientIp string) (*entity.PaymentOrder, error) {
	result, err := store.CompleteOrderManually(orderNo, clientIp)
	if err != nil {
		return nil, err
	}
	if result.Outcome != store.SettleFinished {
		return nil, common.NewMessage("The order is paid but could not be fulfilled: {{reason}}", map[string]any{"reason": result.Reason})
	}
	return store.GetOrderByNo(orderNo)
}
