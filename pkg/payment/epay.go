package payment

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/Calcium-Ion/go-epay/epay"
)

type EpayConfig struct {
	Endpoint    string `json:"endpoint"`
	MerchantID  string `json:"merchant_id"`
	Key         string `json:"key"`
	PaymentType string `json:"payment_type"`
}

type epayGateway struct {
	config EpayConfig
	client *epay.Client
}

var protocolIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func NewEpay(config EpayConfig) (Gateway, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || strings.ContainsAny(config.Endpoint, "&\r\n") {
		return nil, errors.New("invalid Epay endpoint")
	}
	if !protocolIdentifier.MatchString(config.MerchantID) || !protocolIdentifier.MatchString(config.PaymentType) || len(config.PaymentType) > 50 || len(config.Key) < 1 || len(config.Key) > 512 {
		return nil, errors.New("invalid Epay credentials or payment type")
	}
	client, err := epay.NewClient(&epay.Config{PartnerID: config.MerchantID, Key: config.Key}, config.Endpoint)
	if err != nil {
		return nil, err
	}
	return &epayGateway{config: config, client: client}, nil
}

func (g *epayGateway) StartPayment(_ context.Context, checkout Checkout) (*CheckoutResult, error) {
	if checkout.Money.Currency != "CNY" || checkout.Money.Amount <= 0 {
		return nil, ErrInvalidPayment
	}
	notifyURL, err := url.Parse(checkout.NotifyURL)
	if err != nil || notifyURL.Host == "" {
		return nil, errors.New("invalid callback URL")
	}
	returnURL, err := url.Parse(checkout.ReturnURL)
	if err != nil || returnURL.Host == "" {
		return nil, errors.New("invalid return URL")
	}
	uri, params, err := g.client.Purchase(&epay.PurchaseArgs{
		Type: g.config.PaymentType, ServiceTradeNo: checkout.OrderNo,
		Name: checkout.Subject, Money: checkout.Money.Decimal(), Device: epay.PC,
		NotifyUrl: notifyURL, ReturnUrl: returnURL,
	})
	if err != nil {
		return nil, err
	}
	return &CheckoutResult{URL: uri, Params: params}, nil
}

func (g *epayGateway) ParseCallback(_ context.Context, params map[string]string) (*Notice, error) {
	verified, err := g.client.Verify(params)
	if err != nil || !verified.VerifyStatus || params["sign_type"] != "MD5" || params["pid"] != g.config.MerchantID || verified.Type != g.config.PaymentType {
		return nil, ErrInvalidPayment
	}
	if !protocolIdentifier.MatchString(verified.ServiceTradeNo) || !protocolIdentifier.MatchString(verified.TradeNo) {
		return nil, ErrInvalidPayment
	}
	money, err := ParseCNY(verified.Money)
	if err != nil {
		return nil, err
	}
	// Classic Epay has no currency field: the authenticated protocol amount is CNY.
	if value := params["currency"]; value != "" && value != money.Currency {
		return nil, ErrInvalidPayment
	}
	return &Notice{OrderNo: verified.ServiceTradeNo, TradeNo: verified.TradeNo, PaymentType: verified.Type, Money: money, Paid: verified.TradeStatus == epay.StatusTradeSuccess}, nil
}
