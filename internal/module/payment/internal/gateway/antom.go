package gateway

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

// Antom, Ant International's acquiring service, collects Alipay and the
// Alipay+ wallets with its One-time Payments API (product CASHIER_PAYMENT,
// https://docs.antom.com/ac/cashierpay/apm_api). Each payment method
// collects one wallet: pay creates the payment and answers with the wallet's
// payment page and, for most wallets, the code a computer shows to scan;
// notifyPayment reports its result, inquiryPayment confirms it, and cancel
// closes it. The payment request id is the invoice number, which makes
// pay idempotent: asking again returns the same payment.
//
// Every request is signed with the merchant's RSA key and every answer and
// notification with Antom's (https://docs.antom.com/ac/ams/digital_signature):
// SHA-256 with PKCS #1 v1.5 over "POST <path>\n<client id>.<time>.<body>",
// in Base64, URL-encoded, in the Signature header beside Client-Id and
// Request-Time (Response-Time in answers).

// Antom regions and their domains (https://docs.antom.com/ac/ams/api_fund).
const (
	AntomRegionAsia         = "asia"
	AntomRegionNorthAmerica = "north_america"
	AntomRegionEurope       = "europe"
	AntomRegionUS           = "us"
)

var antomDomains = map[string]string{
	AntomRegionAsia:         "https://open-sea-global.alipay.com",
	AntomRegionNorthAmerica: "https://open-na-global.alipay.com",
	AntomRegionEurope:       "https://open-de-global.alipay.com",
	AntomRegionUS:           "https://open.antglobal-us.com",
}

// antomSandboxPrefix starts a sandbox client ID. The sandbox has the same
// domains and serves the API under /ams/sandbox/api, as Antom's server SDKs
// call it.
const antomSandboxPrefix = "SANDBOX_"

// antomPaymentMethodTypes are the wallets a payment method can collect:
// Alipay and the Alipay+ wallets whose payment pages Antom documents
// (https://docs.antom.com/ac/cashierpay/urls). There is no type that lets
// the buyer choose among them.
var antomPaymentMethodTypes = []FieldOption{
	{Value: "ALIPAY_CN", Label: "Alipay"},
	{Value: "ALIPAY_HK", Label: "AlipayHK"},
	{Value: "GCASH", Label: "GCash"},
	{Value: "DANA", Label: "DANA"},
	{Value: "TNG", Label: "Touch'n Go eWallet"},
	{Value: "TRUEMONEY", Label: "TrueMoney"},
	{Value: "BOOST", Label: "Boost"},
	{Value: "KAKAOPAY", Label: "Kakao Pay"},
	{Value: "NAVERPAY", Label: "NAVER Pay"},
	{Value: "TOSSPAY", Label: "Toss Pay"},
	{Value: "RABBIT_LINE_PAY", Label: "Rabbit LINE Pay"},
	{Value: "KPLUS", Label: "K PLUS"},
}

// Antom counts amounts in the smallest unit of their currency as ISO 4217
// defines it, and lists the currencies it applies that to
// (https://docs.antom.com/ac/ref/cc); a payment is requested in one of them.
// The zero-decimal ones count whole units, the others hundredths, and IDR
// amounts must be whole rupiah.
var (
	antomCurrencies = []string{
		"AUD", "BDT", "BRL", "CAD", "CLP", "CNY", "EUR", "GBP", "HKD", "IDR", "JPY", "KRW",
		"MXN", "MYR", "NZD", "PEN", "PHP", "PKR", "PLN", "SGD", "THB", "TWD", "USD", "VND",
	}
	antomZeroDecimal = map[string]bool{"CLP": true, "JPY": true, "KRW": true, "VND": true}
)

// antomCurrencyOptions are the choices of a method's payment currency.
var antomCurrencyOptions = func() []FieldOption {
	options := make([]FieldOption, 0, len(antomCurrencies))
	for _, code := range antomCurrencies {
		options = append(options, FieldOption{Value: code, Label: code})
	}
	return options
}()

const (
	// antomClockSkew bounds how far the time Antom signs an answer with may
	// be from ours, and how far in the future a notification's may be.
	antomClockSkew = 5 * time.Minute
	// antomNotifyMaxAge is how old a notification's signing time may be.
	// Antom resends a notification for a day (after 0 s, 2 min, 10 min,
	// 10 min, 1 h, 2 h, 6 h and 15 h) and does not say whether it signs a
	// resend anew.
	antomNotifyMaxAge = 25 * time.Hour
	// antomMaxDescription is the longest order description Antom takes, in
	// characters.
	antomMaxDescription = 256
)

// Payment statuses inquiryPayment reports. PENDING is a payment the buyer
// completed whose wallet has not confirmed it yet.
const (
	antomSuccess    = "SUCCESS"
	antomFail       = "FAIL"
	antomProcessing = "PROCESSING"
	antomCancelled  = "CANCELLED"
	antomPending    = "PENDING"
)

var (
	errAntomPrivateKey error = common.NewMessage("The Antom merchant private key must be an unencrypted RSA private key of at least 2048 bits")
	errAntomPublicKey  error = common.NewMessage("The Antom public key must be an RSA public key of at least 2048 bits")
	// errAntomPaymentMissing is Antom's answer that it has no payment of
	// the request id.
	errAntomPaymentMissing = errors.New("antom has no payment of the order")
)

// AntomConfig is the stored configuration of an Antom payment method.
type AntomConfig struct {
	ClientID string `json:"client_id"`
	// PrivateKey is the merchant's RSA private key and PlatformPublicKey
	// Antom's RSA public key, both as Base64 DER.
	PrivateKey        string `json:"private_key"`
	PlatformPublicKey string `json:"platform_public_key"`
	Region            string `json:"region"`
	PaymentMethodType string `json:"payment_method_type"`
	// Currency is what the buyer is charged in, SettlementCurrency what
	// Antom settles to the merchant in.
	Currency           string `json:"currency"`
	SettlementCurrency string `json:"settlement_currency"`
}

type antomGateway struct {
	config     AntomConfig
	keys       *rsaKeys
	baseURL    string
	httpClient *http.Client
}

func openAntom(r *Registry, method Method) (Gateway, error) {
	var config AntomConfig
	if err := common.UnmarshalJsonStr(method.Config, &config); err != nil {
		return nil, fmt.Errorf("decode Antom configuration: %w", err)
	}
	baseURL := antomDomains[config.Region]
	if r.antomBaseURL != "" {
		baseURL = r.antomBaseURL
	}
	if config.ClientID == "" || baseURL == "" || config.PaymentMethodType == "" || !slices.Contains(antomCurrencies, config.Currency) || config.SettlementCurrency == "" {
		return nil, errors.New("incomplete Antom configuration")
	}
	keys, err := parseRSAKeys(config.PrivateKey, config.PlatformPublicKey, errAntomPrivateKey, errAntomPublicKey)
	if err != nil {
		return nil, err
	}
	return &antomGateway{config: config, keys: keys, baseURL: strings.TrimSuffix(baseURL, "/"), httpClient: r.httpClient}, nil
}

// normalizeAntom validates a configuration and stores the keys as Base64
// DER and the currencies in upper case.
func normalizeAntom(raw []byte) (string, error) {
	var config AntomConfig
	if err := common.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("decode Antom configuration: %w", err)
	}
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.Region = strings.TrimSpace(config.Region)
	config.PaymentMethodType = strings.TrimSpace(config.PaymentMethodType)
	config.Currency = strings.ToUpper(strings.TrimSpace(config.Currency))
	config.SettlementCurrency = strings.ToUpper(strings.TrimSpace(config.SettlementCurrency))
	if !validIdentifier(config.ClientID) || strings.ContainsAny(config.ClientID, " .") {
		return "", errors.New("the Antom client ID is required and has no spaces or dots")
	}
	keys, err := parseRSAKeys(config.PrivateKey, config.PlatformPublicKey, errAntomPrivateKey, errAntomPublicKey)
	if err != nil {
		return "", err
	}
	if err := keys.private.Validate(); err != nil {
		return "", errAntomPrivateKey
	}
	config.PrivateKey = base64.StdEncoding.EncodeToString(keys.privateDER)
	config.PlatformPublicKey = base64.StdEncoding.EncodeToString(keys.publicDER)
	if antomDomains[config.Region] == "" {
		return "", fmt.Errorf("unsupported Antom region %q", config.Region)
	}
	if !slices.ContainsFunc(antomPaymentMethodTypes, func(option FieldOption) bool { return option.Value == config.PaymentMethodType }) {
		return "", fmt.Errorf("unsupported Antom wallet %q", config.PaymentMethodType)
	}
	if !slices.Contains(antomCurrencies, config.Currency) {
		return "", fmt.Errorf("unsupported Antom payment currency %q", config.Currency)
	}
	if len(config.SettlementCurrency) != 3 || strings.Trim(config.SettlementCurrency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return "", errors.New("the Antom settlement currency must be a three-letter currency code")
	}
	content, err := common.Marshal(config)
	return string(content), err
}

func (g *antomGateway) Platform() string { return PlatformAntom }

func (g *antomGateway) ChargeCurrency() string { return g.config.Currency }

// roundCharge rounds a charge Antom collects in whole units, a
// zero-decimal currency or IDR, to whole units, halves up.
func (g *antomGateway) roundCharge(c Charge) Charge {
	if currency := strings.ToUpper(c.Currency); antomZeroDecimal[currency] || currency == "IDR" {
		c.Amount = (c.Amount + 50) / 100 * 100
	}
	return c
}

// NeedsNotifyURL is true: every payment request names the notify URL, and
// asking again for the same payment repeats the request.
func (g *antomGateway) NeedsNotifyURL(*Order) bool { return true }

func (g *antomGateway) ServerCheckout() bool { return true }

// antomValue converts hundredths of currency into Antom's amount value, in
// the currency's smallest unit. A charge that is not whole units where
// Antom needs them is refused rather than rounded: it was rounded before the
// payment expectation was recorded.
func antomValue(hundredths int64, currency string) (string, error) {
	currency = strings.ToUpper(currency)
	if !slices.Contains(antomCurrencies, currency) {
		return "", fmt.Errorf("antom does not collect %s", currency)
	}
	if hundredths <= 0 {
		return "", errors.New("invalid Antom charge")
	}
	if antomZeroDecimal[currency] || currency == "IDR" {
		if hundredths%100 != 0 {
			return "", fmt.Errorf("antom collects %s in whole units", currency)
		}
		if antomZeroDecimal[currency] {
			return strconv.FormatInt(hundredths/100, 10), nil
		}
	}
	return strconv.FormatInt(hundredths, 10), nil
}

// antomHundredths converts an Antom amount value into hundredths of its
// currency.
func antomHundredths(amount antomAmount) (int64, error) {
	currency := strings.ToUpper(amount.Currency)
	if !slices.Contains(antomCurrencies, currency) || amount.Value == "" || len(amount.Value) > 16 || !decimalDigits(amount.Value) {
		return 0, fmt.Errorf("invalid Antom amount %q %q", amount.Value, amount.Currency)
	}
	value, err := strconv.ParseInt(amount.Value, 10, 64)
	if err != nil {
		return 0, err
	}
	if antomZeroDecimal[currency] {
		if value > math.MaxInt64/100 {
			return 0, errors.New("antom amount out of range")
		}
		value *= 100
	}
	return value, nil
}

type antomAmount struct {
	Currency string `json:"currency"`
	Value    string `json:"value"`
}

// antomResult is the result object of every Antom message.
type antomResult struct {
	ResultCode    string `json:"resultCode"`
	ResultStatus  string `json:"resultStatus"`
	ResultMessage string `json:"resultMessage"`
}

func (r antomResult) describe() string {
	return fmt.Sprintf("%s %s %q", r.ResultStatus, r.ResultCode, r.ResultMessage)
}

// antomPayment is a payment as inquiryPayment reports it.
type antomPayment struct {
	PaymentStatus    string      `json:"paymentStatus"`
	PaymentRequestID string      `json:"paymentRequestId"`
	PaymentID        string      `json:"paymentId"`
	PaymentAmount    antomAmount `json:"paymentAmount"`
	// Transactions lists the refunds and captures of the payment.
	Transactions []antomTransaction `json:"transactions"`
}

type antomTransaction struct {
	TransactionType   string `json:"transactionType"`
	TransactionStatus string `json:"transactionStatus"`
}

// paid reports a successful payment that no refund has touched: a payment
// an operator refunded through Antom does not settle the order.
func (p *antomPayment) paid() bool {
	return p.PaymentStatus == antomSuccess && !slices.ContainsFunc(p.Transactions, func(t antomTransaction) bool {
		return t.TransactionType == "REFUND" && (t.TransactionStatus == antomSuccess || t.TransactionStatus == antomProcessing)
	})
}

// StartPayment creates the invoice's payment, or asks for it again, and
// returns how the buyer pays it. A computer gets the payment code to scan
// with the wallet's app, or the wallet's page when Antom has no code for
// it; a phone opens the wallet's page, which hands over to its app.
func (g *antomGateway) StartPayment(ctx context.Context, c Checkout) (*Action, error) {
	if !strings.EqualFold(c.Charge.Currency, g.config.Currency) {
		return nil, fmt.Errorf("the Antom method collects %s, not %s", g.config.Currency, c.Charge.Currency)
	}
	value, err := antomValue(c.Charge.Amount, c.Charge.Currency)
	if err != nil {
		return nil, err
	}
	if returnURL, err := url.Parse(c.ReturnURL); err != nil || (returnURL.Scheme != "https" && returnURL.Scheme != "http") || returnURL.Host == "" {
		return nil, errors.New("antom needs the console address to send the buyer back to")
	}
	description := c.Subject
	if description == "" {
		description = c.Order.OrderNo
	}
	if utf8.RuneCountInString(description) > antomMaxDescription {
		description = string([]rune(description)[:antomMaxDescription])
	}
	amount := antomAmount{Currency: strings.ToUpper(c.Charge.Currency), Value: value}
	env := map[string]string{"terminalType": "WEB"}
	if c.Mobile && (c.OS == OSiOS || c.OS == OSAndroid) {
		env = map[string]string{"terminalType": "WAP", "osType": strings.ToUpper(c.OS)}
	}
	if net.ParseIP(c.Order.ClientIP) != nil {
		env["clientIp"] = c.Order.ClientIP
	}
	request := map[string]any{
		"productCode":        "CASHIER_PAYMENT",
		"paymentRequestId":   c.Order.OrderNo,
		"paymentAmount":      amount,
		"paymentMethod":      map[string]string{"paymentMethodType": g.config.PaymentMethodType},
		"paymentRedirectUrl": c.ReturnURL,
		"settlementStrategy": map[string]string{"settlementCurrency": g.config.SettlementCurrency},
		"env":                env,
		"order": map[string]any{
			"referenceOrderId": c.Order.OrderNo,
			"orderDescription": description,
			"orderAmount":      amount,
		},
	}
	if c.NotifyURL != "" {
		request["paymentNotifyUrl"] = c.NotifyURL
	}
	var answer struct {
		PaymentRequestID string       `json:"paymentRequestId"`
		PaymentID        string       `json:"paymentId"`
		PaymentAmount    *antomAmount `json:"paymentAmount"`
		NormalURL        string       `json:"normalUrl"`
		ApplinkURL       string       `json:"applinkUrl"`
		SchemeURL        string       `json:"schemeUrl"`
		OrderCodeForm    *struct {
			CodeDetails []struct {
				CodeValue     string `json:"codeValue"`
				CodeValueType string `json:"codeValueType"`
				DisplayType   string `json:"displayType"`
			} `json:"codeDetails"`
		} `json:"orderCodeForm"`
	}
	result, err := g.call(ctx, "pay", request, &answer)
	if err != nil {
		return nil, err
	}
	// S is a payment completed at once, U with PAYMENT_IN_PROCESS one that
	// waits for the buyer; another U failed for an unknown reason, which
	// the close of the invoice asks Antom about.
	switch {
	case result.ResultStatus == "F":
		return nil, Refused(fmt.Errorf("antom refused the payment of order %s: %s", c.Order.OrderNo, result.describe()))
	case result.ResultStatus == "S", result.ResultStatus == "U" && result.ResultCode == "PAYMENT_IN_PROCESS":
	default:
		return nil, fmt.Errorf("antom could not create the payment of order %s: %s", c.Order.OrderNo, result.describe())
	}
	if ValidateTradeNo(answer.PaymentID) != nil || (answer.PaymentRequestID != "" && answer.PaymentRequestID != c.Order.OrderNo) ||
		(answer.PaymentAmount != nil && *answer.PaymentAmount != amount) {
		return nil, fmt.Errorf("the Antom payment answer does not match order %s", c.Order.OrderNo)
	}
	var action *Action
	switch {
	case result.ResultStatus == "S":
		action = &Action{Type: ActionURL, URL: c.ReturnURL}
	case !c.Mobile && answer.OrderCodeForm != nil:
		for _, code := range answer.OrderCodeForm.CodeDetails {
			if code.DisplayType == "TEXT" && (code.CodeValueType == "" || code.CodeValueType == "QRCODE") && code.CodeValue != "" {
				action = &Action{Type: ActionQRCode, QRCode: code.CodeValue}
				break
			}
		}
	}
	for _, link := range []string{answer.NormalURL, answer.ApplinkURL} {
		if target, err := url.Parse(link); action == nil && err == nil && (target.Scheme == "https" || target.Scheme == "http") && target.Host != "" {
			action = &Action{Type: ActionURL, URL: link}
		}
	}
	// Antom may answer with an app link alone, which is opened as it is
	// unless it is a script or document link.
	if target, err := url.Parse(answer.SchemeURL); action == nil && answer.SchemeURL != "" && err == nil && target.Scheme != "" &&
		!slices.Contains([]string{"http", "https", "javascript", "data", "vbscript", "file", "blob", "about"}, target.Scheme) {
		action = &Action{Type: ActionURL, URL: answer.SchemeURL}
	}
	if action == nil {
		// The invoice's close cancels the payment the buyer cannot reach.
		return nil, fmt.Errorf("the Antom payment of order %s has no payment page", c.Order.OrderNo)
	}
	if c.Order.TradeNo != "" {
		if answer.PaymentID != c.Order.TradeNo {
			return nil, fmt.Errorf("order %s holds another Antom payment", c.Order.OrderNo)
		}
		return action, nil
	}
	winner, err := c.Claim(ctx, answer.PaymentID)
	if err != nil {
		// The buyer never gets this payment; it must not be paid.
		_ = g.cancel(ctx, c.Order.OrderNo)
		return nil, err
	}
	if winner != answer.PaymentID {
		return nil, fmt.Errorf("order %s holds another Antom payment", c.Order.OrderNo)
	}
	return action, nil
}

// Reconcile asks Antom about the invoice's payment before it closes. A
// paid payment settles the order, a failed or cancelled one lets it close,
// and one that waits for the buyer is cancelled first, so it can no longer
// be paid: Antom refunds a payment made after the cancellation. A payment
// the buyer completed that the wallet has not confirmed, or one with a
// refund, keeps the order pending.
func (g *antomGateway) Reconcile(ctx context.Context, req CloseRequest) (Reconciliation, error) {
	o := req.Order
	if o.PaymentCurrency == "" {
		return Reconciliation{RequireStableCheckout: true}, nil
	}
	payment, err := g.inquire(ctx, o)
	if errors.Is(err, errAntomPaymentMissing) && o.TradeNo == "" {
		// Antom has not created the payment, or is creating it now: the
		// close holds only if no checkout claims a payment meanwhile.
		return Reconciliation{RequireStableCheckout: true}, nil
	}
	if err != nil {
		return Reconciliation{}, fmt.Errorf("cannot safely close Antom order %s: %w: %w", o.OrderNo, err, ErrUnconfirmed)
	}
	if verdict, final := antomVerdict(payment); final {
		return verdict, nil
	}
	if payment.PaymentStatus != antomProcessing {
		return Reconciliation{}, fmt.Errorf("antom order %s has payment status %s: %w", o.OrderNo, payment.PaymentStatus, ErrUnconfirmed)
	}
	cancelErr := g.cancel(ctx, o.OrderNo)
	if cancelErr == nil {
		return Reconciliation{}, nil
	}
	// The payment may complete between the inquiry and the cancellation.
	if payment, err = g.inquire(ctx, o); err == nil {
		if verdict, final := antomVerdict(payment); final {
			return verdict, nil
		}
	}
	return Reconciliation{}, fmt.Errorf("antom order %s: %w: %w", o.OrderNo, cancelErr, ErrUnconfirmed)
}

// antomVerdict maps a final payment to the close verdict: a paid one
// settles the order, a failed or cancelled one lets it close.
func antomVerdict(payment *antomPayment) (Reconciliation, bool) {
	switch {
	case payment.paid():
		return Reconciliation{TradeNo: payment.PaymentID}, true
	case payment.PaymentStatus == antomFail, payment.PaymentStatus == antomCancelled:
		return Reconciliation{}, true
	}
	return Reconciliation{}, false
}

// QueryPayment reports the invoice's payment when Antom says it is paid;
// the inquiry changes nothing at Antom.
func (g *antomGateway) QueryPayment(ctx context.Context, o *Order) (string, error) {
	if o.PaymentCurrency == "" {
		return "", nil
	}
	payment, err := g.inquire(ctx, o)
	if errors.Is(err, errAntomPaymentMissing) && o.TradeNo == "" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !payment.paid() {
		return "", nil
	}
	return payment.PaymentID, nil
}

// ParseCallback verifies a notification's signature, signing time and
// client ID, and reads the payment it reports: a payment result, S for
// paid and F for failed, or a pending payment the buyer completed. Any
// other notification Antom sends to the URL, such as a refund's, is
// ignored.
func (g *antomGateway) ParseCallback(_ context.Context, n Notification) (*Notice, error) {
	if n.HTTPMethod != http.MethodPost {
		return nil, errors.New("antom notifications are posted")
	}
	var paths []string
	if n.Path != "" {
		paths = append(paths, n.Path)
	}
	if notifyURL, err := url.Parse(n.NotifyURL); n.NotifyURL != "" && err == nil && notifyURL.RequestURI() != n.Path {
		paths = append(paths, notifyURL.RequestURI())
	}
	if err := g.verify(n.Header.Get("Client-Id"), n.Header.Get("Request-Time"), n.Signature, paths, n.Body, antomNotifyMaxAge); err != nil {
		return nil, err
	}
	var notification struct {
		NotifyType        string      `json:"notifyType"`
		Result            antomResult `json:"result"`
		PaymentRequestID  string      `json:"paymentRequestId"`
		PaymentID         string      `json:"paymentId"`
		PaymentAmount     antomAmount `json:"paymentAmount"`
		PaymentMethodType string      `json:"paymentMethodType"`
	}
	if err := common.Unmarshal(n.Body, &notification); err != nil {
		return nil, fmt.Errorf("decode Antom notification: %w", err)
	}
	var paid bool
	switch notification.NotifyType {
	case "PAYMENT_RESULT":
		if notification.Result.ResultStatus != "S" && notification.Result.ResultStatus != "F" {
			return nil, fmt.Errorf("unknown Antom payment result %s", notification.Result.describe())
		}
		paid = notification.Result.ResultStatus == "S"
	case "PAYMENT_PENDING":
	default:
		return &Notice{Ignore: true, Status: notification.NotifyType}, nil
	}
	if err := validateOrderNo(notification.PaymentRequestID); err != nil {
		return nil, err
	}
	if err := ValidateTradeNo(notification.PaymentID); err != nil {
		return nil, err
	}
	amount, err := antomHundredths(notification.PaymentAmount)
	if err != nil {
		return nil, err
	}
	return &Notice{
		OrderNo:  notification.PaymentRequestID,
		TradeNo:  notification.PaymentID,
		Amount:   amount,
		Currency: strings.ToUpper(notification.PaymentAmount.Currency),
		Paid:     paid,
		Status:   notification.NotifyType + " " + notification.Result.ResultCode,
		Details: map[string]string{
			"result_status":       notification.Result.ResultStatus,
			"result_message":      notification.Result.ResultMessage,
			"payment_method_type": notification.PaymentMethodType,
		},
	}, nil
}

// CheckOrder requires the payment to be the one the invoice claimed.
func (g *antomGateway) CheckOrder(o *Order, notice *Notice) error {
	if o.TradeNo != "" && o.TradeNo != notice.TradeNo {
		return errors.New("order trade number mismatch")
	}
	return nil
}

// ConfirmPayment asks Antom about the invoice's payment: it must be the
// notice's, paid without a refund, and of exactly the payment expectation.
func (g *antomGateway) ConfirmPayment(ctx context.Context, o *Order, notice *Notice) error {
	payment, err := g.inquire(ctx, o)
	if err != nil {
		return err
	}
	if payment.PaymentID != notice.TradeNo {
		return errors.New("the Antom payment of the order is another one")
	}
	if !payment.paid() {
		return fmt.Errorf("antom payment %s has status %s", payment.PaymentID, payment.PaymentStatus)
	}
	return nil
}

// inquire asks Antom for the invoice's payment by its request id and checks
// that it is the invoice's: the same request id, the claimed payment when
// the invoice has one, and the payment expectation. errAntomPaymentMissing
// reports that Antom has none.
func (g *antomGateway) inquire(ctx context.Context, o *Order) (*antomPayment, error) {
	var payment antomPayment
	result, err := g.call(ctx, "inquiryPayment", map[string]string{"paymentRequestId": o.OrderNo}, &payment)
	if err != nil {
		return nil, err
	}
	if result.ResultStatus == "F" && result.ResultCode == "ORDER_NOT_EXIST" {
		return nil, errAntomPaymentMissing
	}
	if result.ResultStatus != "S" {
		return nil, fmt.Errorf("antom inquiry of order %s failed: %s", o.OrderNo, result.describe())
	}
	amount, err := antomHundredths(payment.PaymentAmount)
	if err != nil || payment.PaymentRequestID != o.OrderNo || ValidateTradeNo(payment.PaymentID) != nil ||
		(o.TradeNo != "" && payment.PaymentID != o.TradeNo) ||
		amount != o.PaymentAmount || !strings.EqualFold(payment.PaymentAmount.Currency, o.PaymentCurrency) {
		return nil, fmt.Errorf("antom payment of order %s does not match the payment expectation", o.OrderNo)
	}
	return &payment, nil
}

// cancel closes the payment of the request id at Antom: an unpaid one can
// no longer be paid, and one paid anyway is refunded.
func (g *antomGateway) cancel(ctx context.Context, orderNo string) error {
	var answer struct{}
	result, err := g.call(ctx, "cancel", map[string]string{"paymentRequestId": orderNo}, &answer)
	if err != nil {
		return err
	}
	if result.ResultStatus != "S" {
		return fmt.Errorf("antom did not cancel the payment of order %s: %s", orderNo, result.describe())
	}
	return nil
}

// call posts a signed request to an Antom payments API and decodes its
// answer into answer once Antom's signature over it verifies. It returns
// the answer's result, which the caller reads. An answer that is not
// signed, as Antom answers a request whose signature it rejects, is
// reported and never trusted.
func (g *antomGateway) call(ctx context.Context, api string, request, answer any) (antomResult, error) {
	path := "/ams/api/v1/payments/" + api
	if strings.HasPrefix(g.config.ClientID, antomSandboxPrefix) {
		path = "/ams/sandbox/api/v1/payments/" + api
	}
	body, err := common.Marshal(request)
	if err != nil {
		return antomResult{}, fmt.Errorf("encode Antom %s request: %w", api, err)
	}
	stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	digest := sha256.Sum256(antomSigned(path, g.config.ClientID, stamp, body))
	signature, err := rsa.SignPKCS1v15(rand.Reader, g.keys.private, crypto.SHA256, digest[:])
	if err != nil {
		return antomResult{}, fmt.Errorf("sign the Antom %s request: %w", api, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return antomResult{}, fmt.Errorf("create Antom %s request: %w", api, err)
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("Client-Id", g.config.ClientID)
	req.Header.Set("Request-Time", stamp)
	// Key version 1 is what Antom's server SDKs send.
	req.Header.Set("Signature", "algorithm=RSA256,keyVersion=1,signature="+url.QueryEscape(base64.StdEncoding.EncodeToString(signature)))
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return antomResult{}, fmt.Errorf("antom %s request failed: %w", api, err)
	}
	defer func() { _ = resp.Body.Close() }()
	value, err := io.ReadAll(io.LimitReader(resp.Body, maxQueryResponseSize+1))
	if err != nil {
		return antomResult{}, fmt.Errorf("read Antom %s answer: %w", api, err)
	}
	if len(value) > maxQueryResponseSize {
		return antomResult{}, fmt.Errorf("antom %s answer is too large", api)
	}
	var envelope struct {
		Result antomResult `json:"result"`
	}
	decodeErr := common.Unmarshal(value, &envelope)
	if err := g.verify(resp.Header.Get("Client-Id"), resp.Header.Get("Response-Time"), resp.Header.Get("Signature"), []string{path}, value, antomClockSkew); err != nil {
		if decodeErr == nil && envelope.Result.ResultCode != "" {
			return antomResult{}, fmt.Errorf("antom %s answered HTTP %d with %s, which is not trusted: %w", api, resp.StatusCode, envelope.Result.describe(), err)
		}
		return antomResult{}, fmt.Errorf("antom %s answered HTTP %d: %w", api, resp.StatusCode, err)
	}
	if decodeErr != nil || envelope.Result.ResultStatus == "" {
		return antomResult{}, fmt.Errorf("antom %s answer has no result", api)
	}
	if err := common.Unmarshal(value, answer); err != nil {
		return antomResult{}, fmt.Errorf("decode Antom %s answer: %w", api, err)
	}
	return envelope.Result, nil
}

// antomSigned is the content of an Antom signature: the method and path of
// the request, then the client ID, the signing time and the body, joined by
// dots. Every Antom message is posted.
func antomSigned(path, clientID, stamp string, body []byte) []byte {
	content := []byte("POST " + path + "\n" + clientID + "." + stamp + ".")
	return append(content, body...)
}

// verify checks a message Antom signed, an answer or a notification, from
// its Client-Id, signing time and Signature headers: the client ID is the
// method's, the signature is RSA256 with Antom's key over one of paths, and
// the signing time is at most maxAge old and at most antomClockSkew ahead.
func (g *antomGateway) verify(clientID, stamp, signatureHeader string, paths []string, body []byte, maxAge time.Duration) error {
	if clientID != g.config.ClientID {
		return errors.New("antom client id mismatch")
	}
	var algorithm, encoded string
	for part := range strings.SplitSeq(signatureHeader, ",") {
		key, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch strings.ToLower(key) {
		case "algorithm":
			algorithm = value
		case "signature":
			encoded = value
		}
	}
	if encoded == "" {
		return errors.New("the Antom message is not signed")
	}
	if algorithm != "" && algorithm != "RSA256" {
		return fmt.Errorf("unsupported Antom signature algorithm %q", algorithm)
	}
	unescaped, err := url.PathUnescape(encoded)
	if err != nil {
		return errors.New("verify sign failed")
	}
	signature, err := base64.StdEncoding.DecodeString(unescaped)
	if err != nil || len(signature) == 0 {
		return errors.New("verify sign failed")
	}
	verified := slices.ContainsFunc(paths, func(path string) bool {
		digest := sha256.Sum256(antomSigned(path, clientID, stamp, body))
		return rsa.VerifyPKCS1v15(g.keys.public, crypto.SHA256, digest[:], signature) == nil
	})
	if !verified {
		return errors.New("verify sign failed")
	}
	// Antom signs requests with Unix milliseconds and answers and
	// notifications with ISO 8601 times.
	var signedAt time.Time
	if stamp != "" && decimalDigits(stamp) {
		milliseconds, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil {
			return errors.New("invalid Antom signing time")
		}
		signedAt = time.UnixMilli(milliseconds)
	} else if signedAt, err = time.Parse(time.RFC3339, stamp); err != nil {
		if signedAt, err = time.Parse("2006-01-02T15:04:05.999999999Z0700", stamp); err != nil {
			return errors.New("invalid Antom signing time")
		}
	}
	if age := time.Since(signedAt); age > maxAge || age < -antomClockSkew {
		return errors.New("the Antom signing time is outside the permitted window")
	}
	return nil
}
