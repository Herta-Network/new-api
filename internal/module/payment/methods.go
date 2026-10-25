package payment

import (
	"context"
	"errors"
	"maps"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/gateway"
	"github.com/QuantumNous/new-api/logger"

	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
	"gorm.io/gorm"
)

var (
	errMethodNotFound        error = common.NewMessage("Payment channel not found")
	errMethodNameRequired    error = common.NewMessage("The payment channel name is required")
	errMethodPlatform        error = common.NewMessage("Unsupported payment platform")
	errMethodPlatformChanged error = common.NewMessage("The platform of a payment channel cannot be changed")
	errMethodFee             error = common.NewMessage("Invalid payment fee")
	errMethodDomain          error = common.NewMessage("The callback domain must be a public http(s) URL without a query")
	errMethodPendingOrders   error = common.NewMessage("This payment channel has pending orders. Change or delete it after they are paid or closed.")
	errMethodOption          error = common.NewMessage("Choose the payment method this channel collects")
	errMethodAmountRange     error = common.NewMessage("Enter a lowest and highest amount with at most two decimal places; the highest must not be below the lowest")
	errMethodRouting         error = common.NewMessage("Use a priority from 0 to 1000 and a weight from 1 to 1000")
	errBalanceChannel        error = common.NewMessage("The balance channel cannot be changed. Change the balance payment method instead.")
	errNotifyAddressMissing  error = common.NewMessage("Set a public callback domain for this payment channel, or a public server address, first")
)

// MethodInput is an administrator's payment channel form. In Config, a
// secret field left empty keeps its stored value and server-managed fields
// are ignored. Amounts count hundredths of the site currency; 0 leaves that
// end of the range open.
type MethodInput struct {
	Name      string         `json:"name"`
	Platform  string         `json:"platform"`
	OptionId  int            `json:"option_id"`
	Enabled   bool           `json:"enabled"`
	MinAmount int64          `json:"min_amount"`
	MaxAmount int64          `json:"max_amount"`
	Priority  int            `json:"priority"`
	Weight    int            `json:"weight"`
	Domain    string         `json:"domain"`
	Config    map[string]any `json:"config"`
}

// MethodDetail is a payment method as the administrator sees it. Secret
// fields of Config are blank; SecretsSet tells which are configured.
type MethodDetail struct {
	*entity.PaymentMethod
	Config     map[string]any  `json:"config"`
	SecretsSet map[string]bool `json:"secrets_set"`
	// NotifyURL is the callback URL gateways are given; empty for the
	// balance or when no public address is configured.
	NotifyURL string `json:"notify_url"`
	// WebhookStale reports a Stripe method whose registered webhook does not
	// point at NotifyURL; syncing the webhook fixes it.
	WebhookStale bool `json:"webhook_stale"`
	// Health is what this server saw of the channel's checkouts since the
	// last one that started; nil when none failed.
	Health *ChannelHealth `json:"health,omitempty"`
}

// PlatformInfo describes a gateway platform and its configuration fields.
type PlatformInfo = gateway.PlatformInfo

// Platforms describes the gateway platforms for the administrator's form.
func Platforms() []PlatformInfo {
	return gateway.Platforms()
}

func platformFields(platform string) []gateway.Field {
	for _, info := range gateway.Platforms() {
		if info.Platform == platform {
			return info.Fields
		}
	}
	return nil
}

// AdminMethods lists the payment methods for the administrator.
func AdminMethods() ([]*MethodDetail, error) {
	methods, err := store.ListMethods()
	if err != nil {
		return nil, err
	}
	details := make([]*MethodDetail, 0, len(methods))
	for _, method := range methods {
		details = append(details, methodDetail(method))
	}
	return details, nil
}

func methodDetail(method *entity.PaymentMethod) *MethodDetail {
	detail := &MethodDetail{PaymentMethod: method, Config: map[string]any{}, SecretsSet: map[string]bool{}, Health: channelHealth(method.Id)}
	if method.Platform == entity.PaymentPlatformBalance {
		return detail
	}
	_ = common.UnmarshalJsonStr(method.Config, &detail.Config)
	for _, field := range platformFields(method.Platform) {
		if field.Secret {
			value, _ := detail.Config[field.Key].(string)
			detail.SecretsSet[field.Key] = value != ""
			detail.Config[field.Key] = ""
		}
	}
	if notifyURL, err := gateway.NotifyURL(methodView(method), siteAddress()); err == nil {
		detail.NotifyURL = notifyURL
	}
	if method.Platform == gateway.PlatformStripe {
		var config gateway.StripeConfig
		_ = common.UnmarshalJsonStr(method.Config, &config)
		detail.WebhookStale = config.WebhookEndpointID == "" || config.WebhookURL != detail.NotifyURL
	}
	return detail
}

// mergeConfig builds the configuration to validate from the administrator's
// input and the stored configuration: managed fields keep their stored
// value (or are dropped), secret fields left empty keep theirs.
func mergeConfig(platform, stored string, input map[string]any) ([]byte, error) {
	current := map[string]any{}
	if stored != "" {
		if err := common.UnmarshalJsonStr(stored, &current); err != nil {
			return nil, err
		}
	}
	merged := make(map[string]any, len(input))
	maps.Copy(merged, input)
	for _, field := range platformFields(platform) {
		value, stored := current[field.Key]
		if field.Managed {
			delete(merged, field.Key)
			if stored {
				merged[field.Key] = value
			}
			continue
		}
		if text, _ := merged[field.Key].(string); field.Secret && strings.TrimSpace(text) == "" && stored {
			merged[field.Key] = value
		}
	}
	return common.Marshal(merged)
}

// normalizeMethodConfig validates the configuration of a gateway method and
// returns the form to store. A message the gateway words for the
// administrator is returned as it is; any other reason is quoted.
func normalizeMethodConfig(platform string, merged []byte) (string, error) {
	config, err := gatewayRegistry().NormalizeConfig(platform, merged)
	if err == nil {
		return config, nil
	}
	if errors.Is(err, gateway.ErrNotGateway) {
		return "", errMethodPlatform
	}
	var message *common.Message
	if errors.As(err, &message) {
		return "", message
	}
	return "", common.NewMessage("Invalid payment configuration: {{error}}", map[string]any{"error": err.Error()})
}

// validateMethodInput checks the fields every platform shares: the channel
// collects a payment option other than the balance, within an amount range,
// by priority and weight.
func validateMethodInput(in *MethodInput) error {
	in.Name, in.Domain = strings.TrimSpace(in.Name), strings.TrimSpace(in.Domain)
	if in.Name == "" || len(in.Name) > 100 {
		return errMethodNameRequired
	}
	if in.OptionId == entity.BalancePaymentOptionID {
		return errMethodOption
	}
	if _, err := store.GetOption(in.OptionId); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errMethodOption
		}
		return err
	}
	if in.MinAmount < 0 || in.MaxAmount < 0 || in.MinAmount > maxOrderAmount || in.MaxAmount > maxOrderAmount ||
		(in.MaxAmount != 0 && in.MaxAmount < in.MinAmount) {
		return errMethodAmountRange
	}
	if in.Priority < 0 || in.Priority > 1000 || in.Weight < 1 || in.Weight > 1000 {
		return errMethodRouting
	}
	if in.Domain != "" {
		if _, err := gateway.NotifyURL(gateway.Method{Platform: "check", Token: "check", Domain: in.Domain}, ""); err != nil || len(in.Domain) > 255 {
			return errMethodDomain
		}
	}
	return nil
}

// CreateMethod saves a new gateway payment method. A Stripe method gets its
// webhook endpoint registered first, which is removed again when the
// method cannot be saved.
func CreateMethod(ctx context.Context, in MethodInput) (*MethodDetail, error) {
	if err := validateMethodInput(&in); err != nil {
		return nil, err
	}
	merged, err := mergeConfig(in.Platform, "", in.Config)
	if err != nil {
		return nil, errInvalidPurchase
	}
	config, err := normalizeMethodConfig(in.Platform, merged)
	if err != nil {
		return nil, err
	}
	token, err := common.GenerateRandomCharsKey(40)
	if err != nil {
		return nil, err
	}
	method := &entity.PaymentMethod{
		Name: in.Name, Platform: in.Platform, OptionId: in.OptionId, Enabled: in.Enabled, MinAmount: in.MinAmount, MaxAmount: in.MaxAmount,
		Priority: in.Priority, Weight: in.Weight, Domain: in.Domain, Config: config, Token: token,
	}
	var registered *registeredWebhook
	if method.Platform == gateway.PlatformStripe {
		if registered, err = registerStripeWebhook(ctx, method); err != nil {
			return nil, err
		}
	}
	if err := store.CreateMethod(method); err != nil {
		registered.rollback(ctx)
		return nil, err
	}
	return methodDetail(method), nil
}

// UpdateMethod saves an administrator's changes to a payment method. The
// platform never changes; the configuration and the callback domain of a
// method with pending orders cannot change, because those orders' checkout
// and callbacks depend on them. A Stripe method whose secret key selects
// another account registers a webhook there and removes the old one; one
// whose callback URL moved updates its webhook in place.
func UpdateMethod(ctx context.Context, id int, in MethodInput) (*MethodDetail, error) {
	method, err := store.GetMethod(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errMethodNotFound
	}
	if err != nil {
		return nil, err
	}
	if in.Platform != "" && in.Platform != method.Platform {
		return nil, errMethodPlatformChanged
	}
	if method.Platform == entity.PaymentPlatformBalance {
		return nil, errBalanceChannel
	}
	if err := validateMethodInput(&in); err != nil {
		return nil, err
	}
	merged, err := mergeConfig(method.Platform, method.Config, in.Config)
	if err != nil {
		return nil, errInvalidPurchase
	}
	config, err := normalizeMethodConfig(method.Platform, merged)
	if err != nil {
		return nil, err
	}
	// The stored configuration is compared in its canonical form: one a
	// startup migration rewrote may order its fields differently.
	stored := method.Config
	if canonical, err := gatewayRegistry().NormalizeConfig(method.Platform, []byte(method.Config)); err == nil {
		stored = canonical
	}
	if config != stored || in.Domain != method.Domain {
		pending, err := store.CountPendingInvoices(method.Id)
		if err != nil {
			return nil, err
		}
		if pending > 0 {
			return nil, errMethodPendingOrders
		}
	}
	previous := *method
	method.Name, method.OptionId, method.Enabled, method.MinAmount, method.MaxAmount = in.Name, in.OptionId, in.Enabled, in.MinAmount, in.MaxAmount
	method.Priority, method.Weight, method.Domain, method.Config = in.Priority, in.Weight, in.Domain, config
	var registered *registeredWebhook
	if method.Platform == gateway.PlatformStripe {
		if registered, err = followStripeWebhook(ctx, &previous, method); err != nil {
			return nil, err
		}
	}
	if err := store.UpdateMethod(method); err != nil {
		registered.rollback(ctx)
		return nil, err
	}
	registered.retirePrevious(ctx)
	return methodDetail(method), nil
}

// DeleteMethod deletes a payment method without pending orders. The method
// stays in the database for late callbacks and order history; the webhook
// endpoint of a Stripe method is removed.
func DeleteMethod(ctx context.Context, id int) error {
	method, err := store.GetMethod(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errMethodNotFound
	}
	if err != nil {
		return err
	}
	if method.Id == entity.BalancePaymentMethodID {
		return errBalanceChannel
	}
	pending, err := store.CountPendingInvoices(method.Id)
	if err != nil {
		return err
	}
	if pending > 0 {
		return errMethodPendingOrders
	}
	if err := store.DeleteMethod(method); err != nil {
		return err
	}
	if method.Platform == gateway.PlatformStripe {
		var config gateway.StripeConfig
		if err := common.UnmarshalJsonStr(method.Config, &config); err == nil && config.WebhookEndpointID != "" {
			if err := gatewayRegistry().StripeWebhooks(config.SecretKey).Delete(ctx, config.WebhookEndpointID); err != nil {
				logger.LogWarn(ctx, common.LogText("remove Stripe webhook endpoint %s of deleted payment method %d by hand: %v", config.WebhookEndpointID, method.Id, err))
			}
		}
	}
	return nil
}

// SyncStripeWebhook points the webhook of a Stripe method at its current
// callback URL, registering one when it has none.
func SyncStripeWebhook(ctx context.Context, id int) (*MethodDetail, error) {
	method, err := store.GetMethod(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errMethodNotFound
	}
	if err != nil {
		return nil, err
	}
	if method.Platform != gateway.PlatformStripe {
		return nil, errMethodPlatform
	}
	previous := *method
	registered, err := followStripeWebhook(ctx, &previous, method)
	if err != nil {
		return nil, err
	}
	if method.Config != previous.Config {
		if err := store.UpdateMethod(method); err != nil {
			registered.rollback(ctx)
			return nil, err
		}
	}
	return methodDetail(method), nil
}

// SyncStripeWebhooks moves the webhook of every Stripe method whose callback
// URL changed, after the server address changed.
func SyncStripeWebhooks(ctx context.Context) error {
	methods, err := store.ListMethods()
	if err != nil {
		return err
	}
	var errs []error
	for _, method := range methods {
		if method.Platform != gateway.PlatformStripe || !methodDetail(method).WebhookStale {
			continue
		}
		if _, err := SyncStripeWebhook(ctx, method.Id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// registeredWebhook is a webhook endpoint registered while a Stripe method
// was being saved: it is removed again when the save fails, and the
// endpoint of the account the method left is removed when it succeeds.
type registeredWebhook struct {
	secretKey, endpointID string
	// previousKey and previousEndpoint name the endpoint in the account the
	// method used before.
	previousKey, previousEndpoint string
}

func (r *registeredWebhook) rollback(ctx context.Context) {
	if r == nil || r.endpointID == "" {
		return
	}
	if err := gatewayRegistry().StripeWebhooks(r.secretKey).Delete(ctx, r.endpointID); err != nil {
		logger.LogWarn(ctx, common.LogText("remove Stripe webhook endpoint %s of an unsaved payment method by hand: %v", r.endpointID, err))
	}
}

func (r *registeredWebhook) retirePrevious(ctx context.Context) {
	if r == nil || r.previousEndpoint == "" || r.previousKey == "" {
		return
	}
	if err := gatewayRegistry().StripeWebhooks(r.previousKey).Delete(ctx, r.previousEndpoint); err != nil {
		logger.LogWarn(ctx, common.LogText("remove Stripe webhook endpoint %s of the previous Stripe account by hand: %v", r.previousEndpoint, err))
	}
}

// registerStripeWebhook registers a webhook endpoint at the method's
// callback URL in the account of its secret key and stores it in the
// method's configuration.
func registerStripeWebhook(ctx context.Context, method *entity.PaymentMethod) (*registeredWebhook, error) {
	var config gateway.StripeConfig
	if err := common.UnmarshalJsonStr(method.Config, &config); err != nil {
		return nil, err
	}
	notifyURL, err := gateway.NotifyURL(methodView(method), siteAddress())
	if err != nil {
		return nil, errNotifyAddressMissing
	}
	endpoint, err := gatewayRegistry().StripeWebhooks(config.SecretKey).Create(ctx, notifyURL)
	if err != nil {
		return nil, common.NewMessage("The Stripe webhook could not be registered: {{error}}", map[string]any{"error": err.Error()})
	}
	config.WebhookEndpointID, config.WebhookURL, config.WebhookSecret = endpoint.ID, notifyURL, endpoint.Secret
	content, err := common.Marshal(config)
	if err != nil {
		return nil, err
	}
	method.Config = string(content)
	return &registeredWebhook{secretKey: config.SecretKey, endpointID: endpoint.ID}, nil
}

// followStripeWebhook keeps the webhook of an updated Stripe method in step
// with its account and callback URL, changing updated's configuration. The
// endpoint moves in place when only the URL changed; a new account, a
// method without an endpoint, or an endpoint deleted at Stripe gets a new
// one.
func followStripeWebhook(ctx context.Context, previous, updated *entity.PaymentMethod) (*registeredWebhook, error) {
	var before, after gateway.StripeConfig
	if err := common.UnmarshalJsonStr(previous.Config, &before); err != nil {
		return nil, err
	}
	if err := common.UnmarshalJsonStr(updated.Config, &after); err != nil {
		return nil, err
	}
	notifyURL, err := gateway.NotifyURL(methodView(updated), siteAddress())
	if err != nil {
		return nil, errNotifyAddressMissing
	}
	if after.SecretKey == before.SecretKey && after.WebhookEndpointID != "" {
		if notifyURL == after.WebhookURL {
			return nil, nil
		}
		err := gatewayRegistry().StripeWebhooks(after.SecretKey).UpdateURL(ctx, after.WebhookEndpointID, notifyURL)
		if err == nil {
			after.WebhookURL = notifyURL
			content, err := common.Marshal(after)
			if err != nil {
				return nil, err
			}
			updated.Config = string(content)
			return nil, nil
		}
		if !errors.Is(err, gateway.ErrStripeWebhookMissing) {
			return nil, common.NewMessage("The Stripe webhook could not be updated: {{error}}", map[string]any{"error": err.Error()})
		}
	}
	registered, err := registerStripeWebhook(ctx, updated)
	if err != nil {
		return nil, err
	}
	if after.SecretKey != before.SecretKey {
		registered.previousKey, registered.previousEndpoint = before.SecretKey, before.WebhookEndpointID
	}
	return registered, nil
}
