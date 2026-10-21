package payment

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/payment/gateway"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// maxOrderAmount bounds every price and top-up, in hundredths: 100 million
// units of the site currency, beyond what any gateway collects at once.
const maxOrderAmount = 100_000_000_00

var (
	errInvalidPurchase        error = common.NewMessage("Invalid parameters")
	errMethodUnavailable      error = common.NewMessage("This payment method is unavailable")
	errBalanceTopup           error = common.NewMessage("The balance cannot be used to top up the balance")
	errPlanUnavailable        error = common.NewMessage("Subscription plan is not enabled")
	errPlanPriceInvalid       error = common.NewMessage("The plan price must be an amount with at most two decimal places")
	errAmountOutOfRange       error = common.NewMessage("The amount is out of range")
	errBalanceNotAllowed      error = common.NewMessage("This plan does not allow balance redemption")
	errSiteCurrencyMissing    error = common.NewMessage("Unsupported site currency")
	errQuotaUnitMisconfigured error = common.NewMessage("Quota unit is misconfigured")
)

// PurchaseRequest is what a buyer asks to buy.
type PurchaseRequest struct {
	Purpose         string `json:"purpose"`
	PaymentMethodId int    `json:"payment_method_id"`
	// Amount is the top-up amount in the site currency, with at most two
	// decimal places.
	Amount json.Number `json:"amount"`
	PlanId int         `json:"plan_id"`
}

// Quote is the price of a purchase. Amounts count hundredths of Currency,
// the site currency; Amount = Price + Adjustment + FeeAmount.
type Quote struct {
	Purpose     string `json:"purpose"`
	Currency    string `json:"currency"`
	Subject     string `json:"subject"`
	TopupAmount int64  `json:"topup_amount"`
	PlanId      int    `json:"plan_id"`
	Price       int64  `json:"price"`
	// Adjustment is what the top-up group ratio and the amount discount
	// change; negative for a discount.
	Adjustment int64 `json:"adjustment"`
	FeeAmount  int64 `json:"fee_amount"`
	Amount     int64 `json:"amount"`
	// QuotaAmount is the quota a top-up credits or a balance purchase
	// deducts, converted at QuotaRate (site currency to USD).
	QuotaAmount int    `json:"quota_amount"`
	QuotaRate   string `json:"quota_rate"`
	// PaymentAmount and PaymentCurrency preview what the gateway collects;
	// empty for the balance.
	PaymentAmount   int64  `json:"payment_amount"`
	PaymentCurrency string `json:"payment_currency"`

	method *model.PaymentMethod
	plan   *model.SubscriptionPlan
}

// QuotePurchase prices a purchase for userId with the chosen payment method.
// A free plan needs no payment method: it completes like a balance purchase
// of nothing.
func QuotePurchase(ctx context.Context, userId int, req PurchaseRequest) (*Quote, error) {
	currency := operation_setting.PaymentCurrency()
	if currency == "" {
		return nil, errSiteCurrencyMissing
	}
	if common.QuotaPerUnit <= 0 {
		return nil, errQuotaUnitMisconfigured
	}
	quote := &Quote{Purpose: req.Purpose, Currency: currency}
	switch req.Purpose {
	case model.PaymentPurposeTopup:
		if err := quoteTopup(ctx, userId, req, quote); err != nil {
			return nil, err
		}
	case model.PaymentPurposeSubscription:
		if err := quoteSubscription(ctx, req, quote); err != nil {
			return nil, err
		}
	default:
		return nil, errInvalidPurchase
	}
	if quote.Amount > maxOrderAmount {
		return nil, errAmountOutOfRange
	}
	if quote.method.Platform == model.PaymentPlatformBalance {
		return quote, nil
	}
	gw, err := openGateway(quote.method)
	if err != nil {
		return nil, errMethodUnavailable
	}
	rate, err := chargeRate(ctx, gw, currency)
	if err != nil {
		return nil, err
	}
	charge, err := gateway.ChargeFor(gw, quote.Amount, currency, rate)
	if err != nil {
		return nil, err
	}
	quote.PaymentAmount, quote.PaymentCurrency = charge.Amount, charge.Currency
	return quote, nil
}

// quoteTopup prices a wallet top-up: the group ratio and the amount discount
// change the price, the method's fee is added, and the quota is the amount
// converted to USD.
func quoteTopup(ctx context.Context, userId int, req PurchaseRequest, quote *Quote) error {
	method, err := buyableMethod(req.PaymentMethodId)
	if err != nil {
		return err
	}
	if method.Platform == model.PaymentPlatformBalance {
		return errBalanceTopup
	}
	topup, err := gateway.ParseAmount(req.Amount.String())
	if err != nil || topup <= 0 || topup > maxOrderAmount {
		return common.NewMessage("Invalid top-up amount")
	}
	minimum, err := gateway.ParseAmount(operation_setting.GetPaymentSetting().MinTopup)
	if err == nil && topup < minimum {
		return common.NewMessage("Top-up amount cannot be less than {{min}}", map[string]any{"min": gateway.FormatAmount(minimum) + " " + quote.Currency})
	}
	group, err := model.GetUserGroup(userId, true)
	if err != nil {
		return err
	}
	ratio := decimal.NewFromFloat(common.GetTopupGroupRatio(group))
	if !ratio.IsPositive() {
		ratio = decimal.NewFromInt(1)
	}
	discount := decimal.NewFromInt(1)
	if topup%100 == 0 {
		if value, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(topup/100)]; ok && value > 0 {
			discount = decimal.NewFromFloat(value)
		}
	}
	due := decimal.NewFromInt(topup).Mul(ratio).Mul(discount).Round(0)
	if due.GreaterThan(decimal.NewFromInt(maxOrderAmount)) {
		return errAmountOutOfRange
	}
	quotaRate, err := Rate(ctx, quote.Currency, "USD")
	if err != nil {
		return err
	}
	quota, err := quotaFor(topup, quotaRate, false)
	if err != nil || quota <= 0 {
		return model.ErrInvalidTopUpQuota
	}
	if err := model.ValidateTopUpQuotaCapacity(userId, quota); err != nil {
		return err
	}
	fee, err := methodFee(method, due.IntPart())
	if err != nil {
		return err
	}
	quote.method = method
	quote.Subject = "Top-up " + gateway.FormatAmount(topup) + " " + quote.Currency
	quote.TopupAmount = topup
	quote.Price = topup
	quote.Adjustment = due.IntPart() - topup
	quote.FeeAmount = fee
	quote.Amount = due.IntPart() + fee
	quote.QuotaAmount, quote.QuotaRate = quota, quotaRate
	return nil
}

// quoteSubscription prices a plan: its price in the site currency, plus the
// method's fee. The balance pays the price converted to quota, rounded up,
// without a fee; a free plan costs nothing whatever the method.
func quoteSubscription(ctx context.Context, req PurchaseRequest, quote *Quote) error {
	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errPlanUnavailable
	}
	if err != nil {
		return err
	}
	if !plan.Enabled {
		return errPlanUnavailable
	}
	price, err := PlanPrice(plan.PriceAmount)
	if err != nil {
		return err
	}
	quote.plan, quote.PlanId, quote.Subject = plan, plan.Id, plan.Title
	quote.Price, quote.Amount = price, price
	if price == 0 {
		method, err := model.GetPaymentMethodIncludingDeleted(model.BalancePaymentMethodID)
		if err != nil {
			return err
		}
		quote.method = method
		return nil
	}
	method, err := buyableMethod(req.PaymentMethodId)
	if err != nil {
		return err
	}
	quote.method = method
	if method.Platform != model.PaymentPlatformBalance {
		fee, err := methodFee(method, price)
		if err != nil {
			return err
		}
		quote.FeeAmount, quote.Amount = fee, price+fee
		return nil
	}
	if plan.AllowBalancePay != nil && !*plan.AllowBalancePay {
		return errBalanceNotAllowed
	}
	quotaRate, err := Rate(ctx, quote.Currency, "USD")
	if err != nil {
		return err
	}
	quota, err := quotaFor(price, quotaRate, true)
	if err != nil {
		return errAmountOutOfRange
	}
	quote.QuotaAmount, quote.QuotaRate = quota, quotaRate
	return nil
}

// quotaFor converts hundredths of the site currency into quota at rate, the
// site currency to USD rate: rounded half away from zero, or up with roundUp.
func quotaFor(amount int64, rate string, roundUp bool) (int, error) {
	usd, err := decimal.NewFromString(rate)
	if err != nil {
		return 0, err
	}
	quota := decimal.NewFromInt(amount).Shift(-2).Mul(usd).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	if roundUp {
		quota = quota.Ceil()
	}
	return common.WalletQuotaFromDecimalStrict(quota)
}

// PlanPrice converts a plan's price into hundredths of the site currency;
// it must have at most two decimal places.
func PlanPrice(priceAmount float64) (int64, error) {
	price := decimal.NewFromFloat(priceAmount).Shift(2)
	if price.IsNegative() || !price.Equal(price.Truncate(0)) || price.GreaterThan(decimal.NewFromInt(maxOrderAmount)) {
		return 0, errPlanPriceInvalid
	}
	return price.IntPart(), nil
}

// buyableMethod loads a payment method a buyer may choose: it exists and is
// enabled.
func buyableMethod(id int) (*model.PaymentMethod, error) {
	method, err := model.GetPaymentMethod(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errMethodUnavailable
	}
	if err != nil {
		return nil, err
	}
	if !method.Enabled {
		return nil, errMethodUnavailable
	}
	return method, nil
}

// methodFee is the fee a method charges on top of due, in hundredths: a
// share of due in basis points, rounded down, a fixed amount, or both, as
// the method's fee mode selects.
func methodFee(method *model.PaymentMethod, due int64) (int64, error) {
	fee := decimal.Zero
	if method.FeeMode == model.PaymentFeeRate || method.FeeMode == model.PaymentFeeRateAndFixed {
		fee = decimal.NewFromInt(due).Mul(decimal.NewFromInt(method.FeeRateBp)).Shift(-4).Floor()
	}
	if method.FeeMode == model.PaymentFeeFixed || method.FeeMode == model.PaymentFeeRateAndFixed {
		fee = fee.Add(decimal.NewFromInt(method.FeeFixed))
	}
	if fee.IsNegative() || fee.GreaterThan(decimal.NewFromInt(maxOrderAmount)) {
		return 0, errAmountOutOfRange
	}
	return fee.IntPart(), nil
}

// chargeRate is the rate from the site currency to what gw collects; empty
// when the gateway collects the site currency.
func chargeRate(ctx context.Context, gw gateway.Gateway, site string) (string, error) {
	target := gw.ChargeCurrency()
	if target == "" || target == site {
		return "", nil
	}
	return Rate(ctx, site, target)
}
