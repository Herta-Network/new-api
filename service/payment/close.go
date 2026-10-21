package payment

import (
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/payment/gateway"
)

// Who closes a pending order. Only the system's close waits for the
// gateway to confirm that no payment is coming; a person giving the order up
// forfeits a payment a gateway without a cancellation API cannot confirm.
const (
	CloseBySystem = iota
	CloseByOwner
	CloseByAdmin
)

// Close reasons stored on closed orders.
const (
	closeReasonExpired = "expired"
	closeReasonOwner   = "user"
	closeReasonAdmin   = "admin"
)

// ErrCloseUnconfirmed reports that the gateway could not confirm the order
// safe to close; it stays pending and is checked again later.
var ErrCloseUnconfirmed error = common.NewMessage("The payment status could not be confirmed yet. The order stays open; try again later.")

// CloseOrder closes a pending order after asking its gateway: a payment the
// gateway proves settles the order instead, and a payment it cannot rule
// out keeps the order pending (ErrCloseUnconfirmed). It returns the order
// as it is afterwards.
func CloseOrder(ctx context.Context, order *model.PaymentOrder, actor int) (*model.PaymentOrder, error) {
	if order.Status != model.PaymentOrderPending {
		return order, nil
	}
	reason := map[int]string{CloseBySystem: closeReasonExpired, CloseByOwner: closeReasonOwner, CloseByAdmin: closeReasonAdmin}[actor]
	verdict := gateway.Reconciliation{}
	if order.Platform != model.PaymentPlatformBalance {
		// The method may have been disabled or deleted since; its gateway
		// still decides.
		method, err := model.GetPaymentMethodIncludingDeleted(order.PaymentMethodId)
		if err != nil {
			return nil, err
		}
		gw, err := openGateway(method)
		if err != nil {
			return nil, fmt.Errorf("open the gateway of payment method %d: %w", method.Id, err)
		}
		verdict, err = gw.Reconcile(ctx, gateway.CloseRequest{Order: orderView(order), Explicit: actor != CloseBySystem})
		if errors.Is(err, gateway.ErrUnconfirmed) {
			logger.LogDebug(ctx, "payment order %s stays pending: %v", order.OrderNo, err)
			return nil, ErrCloseUnconfirmed
		}
		if err != nil {
			return nil, err
		}
		if verdict.Note != "" {
			common.SysLog(common.LogText("payment order %s: %s", order.OrderNo, verdict.Note))
		}
	}
	if verdict.TradeNo != "" {
		return settleConfirmed(order, verdict.TradeNo, "")
	}
	_, err := model.ClosePaymentOrder(order, verdict.RequireStableCheckout, reason)
	if errors.Is(err, model.ErrPaymentCheckoutChanged) {
		logger.LogDebug(ctx, "payment order %s stays pending: its checkout changed during the close", order.OrderNo)
		return nil, ErrCloseUnconfirmed
	}
	if err != nil {
		return nil, err
	}
	return model.GetPaymentOrderByNo(order.OrderNo)
}

// SyncOrder asks the gateway whether a pending or closed order was paid
// and settles it if so; nothing changes at the gateway. It returns the
// order as it is afterwards.
func SyncOrder(ctx context.Context, order *model.PaymentOrder, clientIp string) (*model.PaymentOrder, error) {
	if (order.Status != model.PaymentOrderPending && order.Status != model.PaymentOrderClosed) || order.Platform == model.PaymentPlatformBalance {
		return order, nil
	}
	method, err := model.GetPaymentMethodIncludingDeleted(order.PaymentMethodId)
	if err != nil {
		return nil, err
	}
	gw, err := openGateway(method)
	if err != nil {
		return nil, fmt.Errorf("open the gateway of payment method %d: %w", method.Id, err)
	}
	tradeNo, err := gw.QueryPayment(ctx, orderView(order))
	if err != nil {
		return nil, err
	}
	if tradeNo == "" {
		return order, nil
	}
	return settleConfirmed(order, tradeNo, clientIp)
}

// settleConfirmed settles an order with a payment its gateway confirmed
// matches the order's expectation.
func settleConfirmed(order *model.PaymentOrder, tradeNo, clientIp string) (*model.PaymentOrder, error) {
	result, err := model.SettlePaymentOrder(model.PaymentSettlement{
		OrderNo: order.OrderNo, TradeNo: tradeNo, Amount: order.PaymentAmount, Currency: order.PaymentCurrency, ClientIp: clientIp,
	})
	if err != nil {
		return nil, err
	}
	if result.Outcome == model.SettleUnmatched || result.Outcome == model.SettleFulfilFailed {
		common.SysError(common.LogText("unmatched payment recorded: order=%s trade=%s reason=%s", order.OrderNo, tradeNo, result.Reason))
	}
	return model.GetPaymentOrderByNo(order.OrderNo)
}

// CompleteOrderManually marks an order paid on an administrator's word and
// fulfils it.
func CompleteOrderManually(orderNo string, clientIp string) (*model.PaymentOrder, error) {
	result, err := model.SettlePaymentOrder(model.PaymentSettlement{OrderNo: orderNo, Manual: true, ClientIp: clientIp})
	if err != nil {
		return nil, err
	}
	if result.Outcome != model.SettleFinished {
		return nil, common.NewMessage("The order is paid but could not be fulfilled: {{reason}}", map[string]any{"reason": result.Reason})
	}
	return model.GetPaymentOrderByNo(orderNo)
}
