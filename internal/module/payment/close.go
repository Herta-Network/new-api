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
func CloseOrder(ctx context.Context, order *entity.PaymentOrder, actor int) (*entity.PaymentOrder, error) {
	if order.Status != entity.PaymentOrderPending {
		return order, nil
	}
	reason := map[int]string{CloseBySystem: closeReasonExpired, CloseByOwner: closeReasonOwner, CloseByAdmin: closeReasonAdmin}[actor]
	verdict := gateway.Reconciliation{}
	if order.Platform != entity.PaymentPlatformBalance {
		// The method may have been disabled or deleted since; its gateway
		// still decides.
		method, err := store.GetMethodIncludingDeleted(order.PaymentMethodId)
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
	_, err := store.CloseOrder(order, verdict.RequireStableCheckout, reason)
	if errors.Is(err, store.ErrCheckoutChanged) {
		logger.LogDebug(ctx, "payment order %s stays pending: its checkout changed during the close", order.OrderNo)
		return nil, ErrCloseUnconfirmed
	}
	if err != nil {
		return nil, err
	}
	return store.GetOrderByNo(order.OrderNo)
}

// SyncOrder asks the gateway whether a pending or closed order was paid
// and settles it if so; nothing changes at the gateway. It returns the
// order as it is afterwards.
func SyncOrder(ctx context.Context, order *entity.PaymentOrder, clientIp string) (*entity.PaymentOrder, error) {
	if (order.Status != entity.PaymentOrderPending && order.Status != entity.PaymentOrderClosed) || order.Platform == entity.PaymentPlatformBalance {
		return order, nil
	}
	method, err := store.GetMethodIncludingDeleted(order.PaymentMethodId)
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
func settleConfirmed(order *entity.PaymentOrder, tradeNo, clientIp string) (*entity.PaymentOrder, error) {
	result, err := store.SettleOrder(store.Settlement{
		OrderNo: order.OrderNo, TradeNo: tradeNo, Amount: order.PaymentAmount, Currency: order.PaymentCurrency, ClientIp: clientIp,
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
	result, err := store.SettleOrder(store.Settlement{OrderNo: orderNo, Manual: true, ClientIp: clientIp})
	if err != nil {
		return nil, err
	}
	if result.Outcome != store.SettleFinished {
		return nil, common.NewMessage("The order is paid but could not be fulfilled: {{reason}}", map[string]any{"reason": result.Reason})
	}
	return store.GetOrderByNo(orderNo)
}
