package payment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/payment/gateway"

	"gorm.io/gorm"
)

// ErrNotifyTargetNotFound reports a notify URL whose token names no payment
// method of the platform in its path.
var ErrNotifyTargetNotFound = errors.New("payment notify target not found")

// NotifyStyle returns how callbacks of platform are delivered and answered;
// false for a platform no gateway serves.
func NotifyStyle(platform string) (gateway.CallbackStyle, bool) {
	return gatewayRegistry().CallbackStyle(platform)
}

// HandleNotify authenticates and settles a gateway callback delivered to
// /api/payment/notify/{platform}/{token}. A nil error is acknowledged;
// ErrNotifyTargetNotFound answers 404; an error wrapping
// gateway.ErrInvalidCallback answers 400 because redelivering it cannot
// succeed; any other error answers 500 so the gateway retries. A payment
// the gateway proves but that cannot settle its order (another amount, a
// second payment, another payment method type, a failed fulfilment) is
// recorded in payment_unmatched and acknowledged, so the gateway does not
// retry it forever.
func HandleNotify(ctx context.Context, platform, token string, n gateway.Notification, clientIp string) error {
	method, err := model.GetPaymentMethodByToken(token)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && method.Platform != platform) {
		return ErrNotifyTargetNotFound
	}
	if err != nil {
		return err
	}
	gw, err := openGateway(method)
	if err != nil {
		return fmt.Errorf("open the gateway of payment method %d: %w", method.Id, err)
	}
	notice, err := gw.ParseCallback(ctx, n)
	if err != nil {
		return gateway.InvalidCallback(err)
	}
	if notice.Ignore {
		return nil
	}
	err = settleNotice(ctx, gw, method, notice, clientIp)
	if err != nil {
		logger.LogWarn(ctx, common.LogText("payment callback rejected: platform=%s method=%d order=%s trade=%s error=%v",
			platform, method.Id, notice.OrderNo, notice.TradeNo, err))
	}
	return err
}

// NotifyResponse is the HTTP answer to a callback.
type NotifyResponse struct {
	Status      int
	ContentType string
	Body        string
}

// NotifyReply answers a callback of style that HandleNotify returned err
// for: 200 when it was handled, 404 for an unknown notify URL, 400 for a
// callback redelivery cannot fix, 500 for a failure the next delivery may
// get past. Gateways with TextReply read "success"; the others read the
// status.
func NotifyReply(style gateway.CallbackStyle, err error) NotifyResponse {
	status := http.StatusOK
	switch {
	case err == nil:
	case errors.Is(err, ErrNotifyTargetNotFound):
		status = http.StatusNotFound
	case errors.Is(err, gateway.ErrInvalidCallback):
		status = http.StatusBadRequest
	default:
		status = http.StatusInternalServerError
	}
	if style.TextReply {
		body := "success"
		if status != http.StatusOK {
			body = "fail"
		}
		return NotifyResponse{Status: status, ContentType: "text/plain; charset=utf-8", Body: body}
	}
	return NotifyResponse{Status: status, ContentType: "application/json", Body: fmt.Sprintf(`{"success":%t}`, status == http.StatusOK)}
}

func settleNotice(ctx context.Context, gw gateway.Gateway, method *model.PaymentMethod, notice *gateway.Notice, clientIp string) error {
	order, err := model.GetPaymentOrderByNo(notice.OrderNo)
	if errors.Is(err, model.ErrPaymentOrderNotFound) {
		return gateway.InvalidCallback(err)
	}
	if err != nil {
		return err
	}
	if order.PaymentMethodId != method.Id || order.Platform != method.Platform {
		return gateway.InvalidCallback(errors.New("the order belongs to another payment method"))
	}
	view := orderView(order)
	if err := gw.CheckOrder(view, notice); err != nil {
		return gateway.InvalidCallback(err)
	}
	if !notice.Paid {
		return acknowledgeLifecycle(ctx, order, notice)
	}
	if (order.Status == model.PaymentOrderFinished || order.Status == model.PaymentOrderPaid) && order.TradeNo == notice.TradeNo {
		return nil
	}
	if notice.Unmatched != "" {
		if err := model.RecordPaymentUnmatched(&model.PaymentUnmatched{
			Platform: order.Platform, TradeNo: notice.TradeNo, OrderNo: order.OrderNo, PaymentMethodId: order.PaymentMethodId,
			UserId: order.UserId, Amount: notice.Amount, Currency: strings.ToUpper(notice.Currency), Reason: notice.Unmatched,
		}); err != nil {
			return err
		}
		logger.LogWarn(ctx, common.LogText("unmatched payment recorded: order=%s trade=%s amount=%d %s reason=%s",
			order.OrderNo, notice.TradeNo, notice.Amount, notice.Currency, notice.Unmatched))
		return nil
	}
	// Ask the gateway itself before money is credited, when the payment can
	// settle the order at all. A payment that cannot (another amount, a
	// second trade) goes straight to settlement, which records it for an
	// operator under the order's lock.
	settleable := order.Status == model.PaymentOrderPending || order.Status == model.PaymentOrderClosed
	if settleable && (order.TradeNo == "" || order.TradeNo == notice.TradeNo) && expectationMatches(order, notice.Amount, notice.Currency) {
		if err := gw.ConfirmPayment(ctx, view, notice); err != nil {
			return fmt.Errorf("confirm payment: %w", err)
		}
	}
	result, err := model.SettlePaymentOrder(model.PaymentSettlement{
		OrderNo: order.OrderNo, TradeNo: notice.TradeNo, Amount: notice.Amount, Currency: notice.Currency, ClientIp: clientIp,
	})
	if err != nil {
		return err
	}
	switch result.Outcome {
	case model.SettleUnmatched, model.SettleFulfilFailed:
		logger.LogWarn(ctx, common.LogText("unmatched payment recorded: order=%s trade=%s amount=%d %s reason=%s",
			order.OrderNo, notice.TradeNo, notice.Amount, notice.Currency, result.Reason))
	case model.SettleFinished:
		logger.LogInfo(ctx, common.LogText("payment order %s settled with trade %s", order.OrderNo, notice.TradeNo))
	}
	return nil
}

// acknowledgeLifecycle accepts an authenticated event that announces no
// payment, such as a failed card or an active invoice, without touching the
// order. An event the gateway asks an operator to review (an underpaid,
// frozen or refunded invoice) concerns money it holds for the order, so it
// is recorded for that operator.
func acknowledgeLifecycle(ctx context.Context, order *model.PaymentOrder, notice *gateway.Notice) error {
	if !expectationMatches(order, notice.Amount, notice.Currency) {
		return gateway.InvalidCallback(errors.New("the event does not describe the order's payment"))
	}
	if !notice.ManualReview {
		return nil
	}
	if err := model.RecordPaymentUnmatched(&model.PaymentUnmatched{
		Platform: order.Platform, TradeNo: notice.TradeNo, OrderNo: order.OrderNo, PaymentMethodId: order.PaymentMethodId,
		UserId: order.UserId, Amount: notice.Amount, Currency: strings.ToUpper(notice.Currency),
		Reason: "the gateway asks for a manual review of the payment: " + notice.Status,
	}); err != nil {
		return err
	}
	logger.LogWarn(ctx, common.LogText("payment needs manual review: order=%s trade=%s status=%s", order.OrderNo, notice.TradeNo, notice.Status))
	return nil
}

// expectationMatches reports whether amount and currency are the order's
// recorded payment expectation.
func expectationMatches(order *model.PaymentOrder, amount int64, currency string) bool {
	return order.PaymentCurrency != "" && order.PaymentAmount == amount && strings.EqualFold(order.PaymentCurrency, currency)
}
