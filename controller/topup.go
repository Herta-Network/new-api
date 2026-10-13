package controller

import (
	"errors"
	"maps"
	"math"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/QuantumNous/new-api/pkg/payment"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func GetTopUpInfo(c *gin.Context) {
	methods, err := model.ListPaymentMethods(true)
	if err != nil {
		common.ApiErrorT(c, "Failed to load payment methods")
		return
	}
	confirmed := operation_setting.IsPaymentComplianceConfirmed()
	available := make([]map[string]any, 0, len(methods))
	if confirmed {
		for _, method := range methods {
			available = append(available, map[string]any{"name": method.Name, "type": method.PublicType(), "provider": method.Provider, "icon": method.Icon, "min_topup": paymentMinimum(method.Config.MinTopUp), "topup_enabled": method.Provider != payment.Stripe || method.Config.PriceID != ""})
		}
	}
	common.ApiSuccess(c, gin.H{
		"enable_online_topup": len(available) > 0, "pay_methods": available, "min_topup": getMinTopup(),
		"enable_redemption": confirmed, "payment_compliance_confirmed": confirmed, "payment_compliance_terms_version": operation_setting.CurrentComplianceTermsVersion,
		"epay_subscription_exchange_rate": operation_setting.USDExchangeRate,
		"amount_options":                  operation_setting.GetPaymentSetting().AmountOptions, "discount": operation_setting.GetPaymentSetting().AmountDiscount, "topup_link": common.TopUpLink,
	})
}

type PaymentRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
	SuccessURL    string `json:"success_url,omitempty"`
	CancelURL     string `json:"cancel_url,omitempty"`
}

func getPayMoneyDecimal(amount int64, group string, price decimal.Decimal) decimal.Decimal {
	if !price.IsPositive() {
		return decimal.Zero
	}
	dAmount := decimal.NewFromInt(amount)
	// 充值金额以“展示类型”为准：
	// - USD/CNY: 前端传 amount 为金额单位；TOKENS: 前端传 tokens，需要换成 USD 金额
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		dAmount = dAmount.Div(dQuotaPerUnit)
	}

	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}
	if math.IsNaN(topupGroupRatio) || math.IsInf(topupGroupRatio, 0) || topupGroupRatio < 0 {
		return decimal.Zero
	}

	dTopupGroupRatio := decimal.NewFromFloat(topupGroupRatio)
	dPrice := price
	// apply optional preset discount by the original request amount (if configured), default 1.0
	discount := 1.0
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	if math.IsNaN(discount) || math.IsInf(discount, 0) {
		return decimal.Zero
	}
	dDiscount := decimal.NewFromFloat(discount)

	payMoney := dAmount.Mul(dPrice).Mul(dTopupGroupRatio).Mul(dDiscount)

	return payMoney
}

func getMinTopup() int64 {
	minTopup := operation_setting.MinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dMinTopup := decimal.NewFromInt(int64(minTopup))
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quota, err := common.WalletQuotaFromDecimalStrict(dMinTopup.Mul(dQuotaPerUnit))
		if err != nil {
			return common.MaxWalletQuota
		}
		minTopup = quota
	}
	return int64(minTopup)
}

func getTopUpQuota(amount int64) (int, error) {
	quota := decimal.NewFromInt(amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quota = decimal.NewFromInt(quota.Div(quotaPerUnit).IntPart()).Mul(quotaPerUnit)
	} else {
		quota = quota.Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	return common.WalletQuotaFromDecimalStrict(quota)
}

func getMaxTopUpAmount() int64 {
	if common.QuotaPerUnit <= 0 {
		return 0
	}
	quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	maxStoredAmount := decimal.NewFromInt(common.MaxWalletQuota).
		Div(quotaPerUnit).
		Floor()
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return maxStoredAmount.Add(decimal.NewFromInt(1)).
			Mul(quotaPerUnit).
			Ceil().
			Sub(decimal.NewFromInt(1)).
			IntPart()
	}
	return maxStoredAmount.IntPart()
}

func validateCreditedQuota(quota decimal.Decimal) (int, error) {
	value, err := common.WalletQuotaFromDecimalStrict(quota)
	if err != nil {
		return 0, common.NewMessage("Top-up quota exceeds the range the system can represent")
	}
	if value <= 0 {
		return 0, common.NewMessage("Top-up quota must be greater than 0")
	}
	return value, nil
}

func validateTopUpQuota(amount int64) (int, error) {
	quota, err := getTopUpQuota(amount)
	if err == nil && quota > 0 {
		return quota, nil
	}
	maxAmount := getMaxTopUpAmount()
	if maxAmount > 0 && amount > maxAmount {
		return 0, common.NewMessage("Top-up amount cannot exceed {{max}}", map[string]any{"max": maxAmount})
	}
	return 0, common.NewMessage("Invalid top-up amount")
}

// paymentError answers payment endpoints in their {"message": "error",
// "data": text} shape. When err is or wraps a *common.Message, the response
// also carries message_key and message_params for the web console.
func paymentError(c *gin.Context, err error) {
	body := gin.H{"message": "error", "data": err.Error()}
	var message *common.Message
	if errors.As(err, &message) {
		maps.Copy(body, message.Fields())
	}
	c.JSON(http.StatusOK, body)
}

func rejectInvalidCreditedQuota(c *gin.Context, userId int, quota decimal.Decimal) bool {
	creditedQuota, err := validateCreditedQuota(quota)
	if err == nil {
		err = model.ValidateTopUpQuotaCapacity(userId, creditedQuota)
	}
	if err != nil {
		paymentError(c, err)
		return true
	}
	return false
}

func rejectInvalidTopUpQuota(c *gin.Context, userId int, amount int64) bool {
	creditedQuota, err := validateTopUpQuota(amount)
	if err == nil {
		err = model.ValidateTopUpQuotaCapacity(userId, creditedQuota)
	}
	if err != nil {
		paymentError(c, err)
		return true
	}
	return false
}

func prepareTopupPayment(c *gin.Context, req PaymentRequest) (service.PaymentCheckout, error) {
	var input service.PaymentCheckout
	method, version, config, err := service.LoadPaymentConfiguration(req.PaymentMethod)
	if err != nil {
		return input, err
	}
	minimum := paymentMinimum(config.MinTopUp)
	if req.Amount < minimum {
		return input, common.NewMessage("Top-up amount cannot be less than {{min}}", map[string]any{"min": minimum})
	}
	quota, err := validateTopUpQuota(req.Amount)
	if err != nil {
		return input, err
	}
	group, err := model.GetUserGroup(c.GetInt("id"), true)
	if err != nil {
		return input, err
	}
	units := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		units = decimal.NewFromInt(units).Div(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart()
	}
	if method.Provider == payment.Stripe {
		if units > 10000 {
			return input, common.NewMessage("Top-up amount cannot exceed {{max}}", map[string]any{"max": 10000})
		}
		ratio := common.GetTopupGroupRatio(group)
		if ratio == 0 {
			ratio = 1
		}
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
			return input, model.ErrInvalidTopUpQuota
		}
		quota, err = validateCreditedQuota(getStripeCreditedQuota(units, group))
		if err != nil {
			return input, err
		}
	}
	if err := model.ValidateTopUpQuotaCapacity(c.GetInt("id"), quota); err != nil {
		return input, err
	}
	if req.SuccessURL != "" && common.ValidateRedirectURL(req.SuccessURL) != nil {
		return input, common.NewMessage("Payment success redirect URL is not in the trusted domain list")
	}
	if req.CancelURL != "" && common.ValidateRedirectURL(req.CancelURL) != nil {
		return input, common.NewMessage("Payment cancel redirect URL is not in the trusted domain list")
	}
	unitPrice := decimal.NewFromInt(1)
	currency := "USD"
	if method.Provider == payment.Epay {
		if math.IsNaN(operation_setting.Price) || math.IsInf(operation_setting.Price, 0) {
			return input, model.ErrInvalidTopUpQuota
		}
		unitPrice = decimal.NewFromFloat(operation_setting.Price)
		currency = "CNY"
	}
	if method.Provider == payment.Cryptomus {
		unitPrice, err = decimal.NewFromString(config.UnitPrice)
		if err != nil {
			return input, err
		}
	}
	money, err := payment.RoundMoney(getPayMoneyDecimal(req.Amount, group, unitPrice), currency)
	if err != nil {
		return input, common.NewMessage("Payment amount is too low")
	}
	input = service.PaymentCheckout{Method: req.PaymentMethod, VersionID: version.ID, UserID: c.GetInt("id"), Money: money, CreditedQuota: quota, TopUpAmount: units, SuccessURL: req.SuccessURL, CancelURL: req.CancelURL}
	input.Money, err = service.QuotePayment(c.Request.Context(), input)
	if err != nil {
		return input, common.NewMessage("Failed to calculate payment amount")
	}
	return input, nil
}

func RequestPayment(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	var req PaymentRequest
	if c.ShouldBindJSON(&req) != nil {
		paymentError(c, common.NewMessage("Invalid parameters"))
		return
	}
	input, err := prepareTopupPayment(c, req)
	if err != nil {
		paymentError(c, err)
		return
	}
	result, err := service.StartPayment(c.Request.Context(), input)
	if err != nil {
		paymentError(c, common.NewMessage("Failed to start payment"))
		return
	}
	paymentResult(c, result)
}
func paymentResult(c *gin.Context, result *payment.CheckoutResult) {
	kind := "url"
	if len(result.Params) > 0 {
		kind = "form"
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": result.Params, "url": result.URL, "type": kind})
}

// tradeNo lock
var orderLocks sync.Map
var createLock sync.Mutex

// refCountedMutex 带引用计数的互斥锁，确保最后一个使用者才从 map 中删除
type refCountedMutex struct {
	mu       sync.Mutex
	refCount int
}

// LockOrder 尝试对给定订单号加锁
func LockOrder(tradeNo string) {
	createLock.Lock()
	var rcm *refCountedMutex
	if v, ok := orderLocks.Load(tradeNo); ok {
		rcm = v.(*refCountedMutex)
	} else {
		rcm = &refCountedMutex{}
		orderLocks.Store(tradeNo, rcm)
	}
	rcm.refCount++
	createLock.Unlock()
	rcm.mu.Lock()
}

// UnlockOrder 释放给定订单号的锁
func UnlockOrder(tradeNo string) {
	v, ok := orderLocks.Load(tradeNo)
	if !ok {
		return
	}
	rcm := v.(*refCountedMutex)
	rcm.mu.Unlock()

	createLock.Lock()
	rcm.refCount--
	if rcm.refCount == 0 {
		orderLocks.Delete(tradeNo)
	}
	createLock.Unlock()
}

func RequestAmount(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	var req PaymentRequest
	if c.ShouldBindJSON(&req) != nil {
		paymentError(c, common.NewMessage("Invalid parameters"))
		return
	}
	input, err := prepareTopupPayment(c, req)
	if err != nil {
		paymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": gin.H{"amount": input.Money.Decimal(), "currency": input.Money.Currency}})
}

func GetUserTopUps(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" {
		topups, total, err = model.SearchUserTopUps(userId, keyword, pageInfo)
	} else {
		topups, total, err = model.GetUserTopUps(userId, pageInfo)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

// GetAllTopUps 管理员获取全平台充值记录
func GetAllTopUps(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" {
		topups, total, err = model.SearchAllTopUps(keyword, pageInfo)
	} else {
		topups, total, err = model.GetAllTopUps(pageInfo)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

type AdminCompleteTopupRequest struct {
	TradeNo string `json:"trade_no"`
}

// AdminCompleteTopUp 管理员补单接口
func AdminCompleteTopUp(c *gin.Context) {
	var req AdminCompleteTopupRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TradeNo == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	// 订单级互斥，防止并发补单
	LockOrder(req.TradeNo)
	defer UnlockOrder(req.TradeNo)

	if err := model.ManualCompleteTopUp(req.TradeNo, c.ClientIP()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func getStripeCreditedQuota(amount int64, group string) decimal.Decimal {
	ratio := common.GetTopupGroupRatio(group)
	if ratio == 0 {
		ratio = 1
	}
	return decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(ratio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
}

func paymentMinimum(methodMinimum int64) int64 {
	minimum := max(int64(operation_setting.MinTopUp), methodMinimum)
	if operation_setting.GetQuotaDisplayType() != operation_setting.QuotaDisplayTypeTokens {
		return minimum
	}
	quota, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(minimum).Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
	if err != nil {
		return common.MaxWalletQuota
	}
	return int64(quota)
}
