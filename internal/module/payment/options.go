package payment

import (
	"cmp"
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/module/currency"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/gateway"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

var (
	errOptionNotFound        error = common.NewMessage("Payment method not found")
	errOptionNameRequired    error = common.NewMessage("The payment method name is required")
	errOptionHasMethods      error = common.NewMessage("Move or delete the channels of this payment method first")
	errBalanceOptionDeletion error = common.NewMessage("The balance payment method cannot be deleted")
	errAmountNotAccepted     error = common.NewMessage("This payment method does not take this amount")
)

// routeRandom draws the weighted order of a priority's channels; tests fix
// it.
var routeRandom = rand.Int64N

// OptionInput is an administrator's payment option form.
type OptionInput struct {
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Sort        int    `json:"sort"`
	Enabled     bool   `json:"enabled"`
	FeeMode     int    `json:"fee_mode"`
	FeeRateBp   int64  `json:"fee_rate_bp"`
	FeeFixed    int64  `json:"fee_fixed"`
}

// OptionDetail is a payment option as the administrator sees it, with the
// number of its channels.
type OptionDetail struct {
	*entity.PaymentOption
	Methods int64 `json:"methods"`
}

func validateOptionInput(in *OptionInput) error {
	in.Name, in.Icon = strings.TrimSpace(in.Name), strings.TrimSpace(in.Icon)
	if in.Name == "" || len(in.Name) > 100 || len(in.Icon) > 255 {
		return errOptionNameRequired
	}
	if in.FeeMode < entity.PaymentFeeNone || in.FeeMode > entity.PaymentFeeRateAndFixed ||
		in.FeeRateBp < 0 || in.FeeRateBp > 10000 || in.FeeFixed < 0 || in.FeeFixed > maxOrderAmount {
		return errMethodFee
	}
	return nil
}

// AdminOptions lists the payment options for the administrator.
func AdminOptions() ([]*OptionDetail, error) {
	options, err := store.ListOptions()
	if err != nil {
		return nil, err
	}
	details := make([]*OptionDetail, 0, len(options))
	for _, option := range options {
		count, err := store.CountOptionMethods(option.Id)
		if err != nil {
			return nil, err
		}
		details = append(details, &OptionDetail{PaymentOption: option, Methods: count})
	}
	return details, nil
}

// CreateOption saves a new payment option. It takes orders once a channel
// collects it.
func CreateOption(in OptionInput) (*entity.PaymentOption, error) {
	if err := validateOptionInput(&in); err != nil {
		return nil, err
	}
	option := &entity.PaymentOption{
		Name: in.Name, Icon: in.Icon, Description: in.Description, Sort: in.Sort, Enabled: in.Enabled,
		FeeMode: in.FeeMode, FeeRateBp: in.FeeRateBp, FeeFixed: in.FeeFixed,
	}
	if err := store.CreateOption(option); err != nil {
		return nil, err
	}
	return option, nil
}

// UpdateOption saves an administrator's changes to a payment option. The
// balance charges no fee.
func UpdateOption(id int, in OptionInput) (*entity.PaymentOption, error) {
	option, err := store.GetOption(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errOptionNotFound
	}
	if err != nil {
		return nil, err
	}
	if option.Id == entity.BalancePaymentOptionID {
		in.FeeMode, in.FeeRateBp, in.FeeFixed = entity.PaymentFeeNone, 0, 0
	}
	if err := validateOptionInput(&in); err != nil {
		return nil, err
	}
	option.Name, option.Icon, option.Description, option.Sort, option.Enabled = in.Name, in.Icon, in.Description, in.Sort, in.Enabled
	option.FeeMode, option.FeeRateBp, option.FeeFixed = in.FeeMode, in.FeeRateBp, in.FeeFixed
	if err := store.UpdateOption(option); err != nil {
		return nil, err
	}
	return option, nil
}

// DeleteOption deletes a payment option without channels. Orders paid with
// it keep its name.
func DeleteOption(id int) error {
	option, err := store.GetOption(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errOptionNotFound
	}
	if err != nil {
		return err
	}
	if option.Id == entity.BalancePaymentOptionID {
		return errBalanceOptionDeletion
	}
	methods, err := store.CountOptionMethods(option.Id)
	if err != nil {
		return err
	}
	if methods > 0 {
		return errOptionHasMethods
	}
	return store.DeleteOption(option)
}

// buyableOption loads a payment option a buyer may choose: it exists and is
// enabled.
func buyableOption(id int) (*entity.PaymentOption, error) {
	option, err := store.GetOption(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errMethodUnavailable
	}
	if err != nil {
		return nil, err
	}
	if !option.Enabled {
		return nil, errMethodUnavailable
	}
	return option, nil
}

// channel is a payment method that can take an order now, with its gateway.
type channel struct {
	method *entity.PaymentMethod
	gw     gateway.Gateway
}

// usableChannels returns the enabled gateway channels of an option whose
// gateway opens and whose exchange rate from the site currency resolves.
// unusable is why the first channel that cannot take orders cannot.
func usableChannels(ctx context.Context, optionID int, site string) (channels []channel, unusable error, err error) {
	methods, err := store.ListOptionMethods(optionID)
	if err != nil {
		return nil, nil, err
	}
	for _, method := range methods {
		if !method.Enabled || method.Platform == entity.PaymentPlatformBalance {
			continue
		}
		gw, err := openGateway(method)
		if err == nil {
			_, err = chargeRate(ctx, gw, site)
		}
		if err != nil {
			if unusable == nil {
				unusable = err
			}
			continue
		}
		channels = append(channels, channel{method: method, gw: gw})
	}
	return channels, unusable, nil
}

// takesAmount reports whether a channel takes an order of price hundredths
// of the site currency.
func takesAmount(method *entity.PaymentMethod, price int64) bool {
	return (method.MinAmount == 0 || price >= method.MinAmount) && (method.MaxAmount == 0 || price <= method.MaxAmount)
}

// routeChannels returns the channels of an option that take an order of
// price hundredths of the site currency now, in the order to invoice them:
// the lowest priority first, and the channels of one priority in a random
// order weighted by their weight, so they share the orders. The channels to
// avoid (the buyer did not get through them) follow the others, and the
// tripped ones come last.
func routeChannels(ctx context.Context, option *entity.PaymentOption, price int64, site string, avoid []int) ([]channel, error) {
	usable, unusable, err := usableChannels(ctx, option.Id, site)
	if err != nil {
		return nil, err
	}
	if len(usable) == 0 {
		if unusable != nil {
			var message *common.Message
			if errors.As(unusable, &message) {
				return nil, unusable
			}
		}
		return nil, errMethodUnavailable
	}
	var taking []channel
	for _, candidate := range usable {
		if takesAmount(candidate.method, price) {
			taking = append(taking, candidate)
		}
	}
	if len(taking) == 0 {
		return nil, errAmountNotAccepted
	}
	// usable came by priority, so each priority is a run.
	routed := make([]channel, 0, len(taking))
	for start := 0; start < len(taking); {
		end := start + 1
		for end < len(taking) && taking[end].method.Priority == taking[start].method.Priority {
			end++
		}
		group := slices.Clone(taking[start:end])
		for len(group) > 0 {
			var total int64
			for _, candidate := range group {
				total += int64(max(candidate.method.Weight, 1))
			}
			pick := routeRandom(total)
			for i, candidate := range group {
				pick -= int64(max(candidate.method.Weight, 1))
				if pick < 0 {
					routed = append(routed, candidate)
					group = slices.Delete(group, i, i+1)
					break
				}
			}
		}
		start = end
	}
	rank := func(candidate channel) int {
		switch {
		case channelTripped(candidate.method.Id):
			return 2
		case slices.Contains(avoid, candidate.method.Id):
			return 1
		}
		return 0
	}
	slices.SortStableFunc(routed, func(a, b channel) int { return cmp.Compare(rank(a), rank(b)) })
	return routed, nil
}

// AmountRange is the prices an option takes, in hundredths of the site
// currency; a Max of 0 is open-ended.
type AmountRange struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// OptionView is a payment option as a buyer sees it. Unavailable is set
// when the option cannot take an order now, such as when the exchange rate
// of its channels is missing. AmountRanges are the prices it takes; empty
// takes any.
type OptionView struct {
	Id           int           `json:"id"`
	Name         string        `json:"name"`
	Icon         string        `json:"icon"`
	Description  string        `json:"description"`
	FeeMode      int           `json:"fee_mode"`
	FeeRateBp    int64         `json:"fee_rate_bp"`
	FeeFixed     int64         `json:"fee_fixed"`
	Balance      bool          `json:"balance"`
	AmountRanges []AmountRange `json:"amount_ranges"`
	Available    bool          `json:"available"`
	// Unavailable says why the option cannot take an order now.
	Unavailable *common.Message `json:"unavailable,omitempty"`
}

// BuyerOptions lists the enabled options a purpose can be paid with. The
// balance pays subscriptions only. An option none of whose channels can take
// orders is listed as unavailable rather than hidden; the prices it takes
// join the ranges of the channels that can.
func BuyerOptions(ctx context.Context, purpose string) ([]OptionView, error) {
	if purpose != entity.PaymentPurposeTopup && purpose != entity.PaymentPurposeSubscription {
		return nil, errInvalidPurchase
	}
	options, err := store.ListOptions()
	if err != nil {
		return nil, err
	}
	site := operation_setting.PaymentCurrency()
	// Top-ups and balance purchases convert the site currency to USD for
	// the quota.
	_, quotaRateErr := currency.ResolveRate(ctx, site, "USD")
	views := make([]OptionView, 0, len(options))
	for _, option := range options {
		balance := option.Id == entity.BalancePaymentOptionID
		if !option.Enabled || (balance && purpose != entity.PaymentPurposeSubscription) {
			continue
		}
		view := OptionView{
			Id: option.Id, Name: option.Name, Icon: option.Icon, Description: option.Description,
			FeeMode: option.FeeMode, FeeRateBp: option.FeeRateBp, FeeFixed: option.FeeFixed, Balance: balance,
			AmountRanges: []AmountRange{}, Available: true,
		}
		var unavailable error
		if balance {
			view.FeeMode, view.FeeRateBp, view.FeeFixed = entity.PaymentFeeNone, 0, 0
			unavailable = quotaRateErr
		} else {
			channels, unusable, err := usableChannels(ctx, option.Id, site)
			if err != nil {
				return nil, err
			}
			switch {
			case len(channels) == 0 && unusable != nil:
				unavailable = unusable
			case len(channels) == 0:
				unavailable = errMethodUnavailable
			case purpose == entity.PaymentPurposeTopup:
				unavailable = quotaRateErr
			}
			view.AmountRanges = channelRanges(channels)
		}
		if unavailable != nil {
			view.Available, view.Unavailable = false, common.NewMessage("This payment method is unavailable")
			var message *common.Message
			if errors.As(unavailable, &message) {
				view.Unavailable = message
			}
		}
		views = append(views, view)
	}
	return views, nil
}

// channelRanges joins the price ranges of channels; empty when one of them
// takes any price.
func channelRanges(channels []channel) []AmountRange {
	ranges := make([]AmountRange, 0, len(channels))
	for _, candidate := range channels {
		if candidate.method.MinAmount == 0 && candidate.method.MaxAmount == 0 {
			return []AmountRange{}
		}
		ranges = append(ranges, AmountRange{Min: candidate.method.MinAmount, Max: candidate.method.MaxAmount})
	}
	slices.SortFunc(ranges, func(a, b AmountRange) int { return cmp.Compare(a.Min, b.Min) })
	joined := ranges[:0]
	for _, next := range ranges {
		if len(joined) > 0 {
			last := &joined[len(joined)-1]
			if last.Max == 0 || next.Min <= last.Max+1 {
				if last.Max != 0 && (next.Max == 0 || next.Max > last.Max) {
					last.Max = next.Max
				}
				continue
			}
		}
		joined = append(joined, next)
	}
	return joined
}
