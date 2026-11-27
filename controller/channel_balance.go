package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel/providerplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
)

// balanceQueryTimeout bounds the balance query of one key in an update of all
// balances, and of one plugin key line anywhere.
const balanceQueryTimeout = 30 * time.Second

// balanceUpdateConcurrency is how many channels an update of all balances
// queries at once.
const balanceUpdateConcurrency = 4

// maxBalanceErrorRunes bounds the failure message a balance detail keeps.
const maxBalanceErrorRunes = 512

func usdBalance(result jsplugin.BalanceResult) (float64, bool) {
	return service.PluginBalanceUSD(result)
}

// emptyBalance reports a definite balance that is used up: a finite amount of
// 0 or less, not unlimited.
func emptyBalance(result jsplugin.BalanceResult) bool {
	return result.Amount != nil && !result.Unlimited && !math.IsNaN(*result.Amount) && !math.IsInf(*result.Amount, 0) && *result.Amount <= 0
}

// positiveBalance reports a balance that can be spent again: unlimited, or a
// finite amount above 0.
func positiveBalance(result jsplugin.BalanceResult) bool {
	return result.Unlimited || (result.Amount != nil && !math.IsInf(*result.Amount, 0) && *result.Amount > 0)
}

// insufficientBalanceReason is the status reason of a channel or key an
// update of all balances disabled. The update queries such a single-key
// channel again and enables it once its balance is back.
const insufficientBalanceReason = "Insufficient balance"

// recordBuiltinBalance stores the detail of a built-in balance query, whose
// function already wrote the USD balance: the amount in USD, or the failure
// once the channel has a detail. A channel type without a balance query thus
// keeps no detail.
func recordBuiltinBalance(channel *model.Channel, result channelBalanceResult, err error) *model.ChannelBalanceDetail {
	if err == nil && result.RawResponse != "" {
		return nil
	}
	detail, updateErr := model.UpdateChannelBalanceDetail(channel.Id, func(stored *model.ChannelBalanceDetail) (*model.ChannelBalanceDetail, *float64) {
		if err == nil {
			balance := result.Balance
			return &model.ChannelBalanceDetail{BalanceResult: jsplugin.BalanceResult{Amount: &balance, Currency: "USD"}}, nil
		}
		if stored == nil {
			return nil, nil
		}
		stored.BalanceFailure = balanceFailure(err, channel.Key)
		return stored, nil
	})
	if updateErr != nil {
		common.SysError(common.LogText("failed to store balance detail: channel_id=%d, error=%v", channel.Id, updateErr))
	}
	return detail
}

// pluginKeyBalance is the balance query of one key line of a task plugin
// channel.
type pluginKeyBalance struct {
	index  int
	key    string
	result jsplugin.BalanceResult
	err    error
}

// pluginReportsBalance reports whether a plugin exports the balance hooks.
func pluginReportsBalance(plugin *jsplugin.LoadedPlugin) bool {
	return plugin.HasProviderHook("buildBalanceRequest") && plugin.HasProviderHook("parseBalance")
}

// updatePluginChannelBalance queries the balance of a task plugin channel with
// its plugin's balance hooks, each key line of a multi-key channel on its own,
// and stores the result: the detail, and the USD balance when the result has
// one. enabledOnly skips the disabled keys of a multi-key channel. err is the
// failure when every queried key failed.
func updatePluginChannelBalance(channel *model.Channel, plugin *jsplugin.LoadedPlugin, enabledOnly bool) ([]pluginKeyBalance, *model.ChannelBalanceDetail, error) {
	keys := []string{strings.TrimSpace(channel.Key)}
	if channel.ChannelInfo.IsMultiKey {
		keys = channel.GetKeys()
	}
	balances := make([]pluginKeyBalance, 0, len(keys))
	for index, key := range keys {
		if enabledOnly && channel.ChannelInfo.IsMultiKey {
			if status, ok := channel.ChannelInfo.MultiKeyStatusList[index]; ok && status != common.ChannelStatusEnabled {
				continue
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), balanceQueryTimeout)
		result, err := queryPluginKeyBalance(ctx, channel, plugin, index, key)
		cancel()
		if err != nil {
			// The failure reaches the console and the detail without the key
			// line's secrets, as a console message when it is one.
			failure := balanceFailure(err, key)
			err = errors.New(failure.Error)
			if failure.ErrorKey != "" {
				err = common.NewMessage(failure.ErrorKey, failure.ErrorParams)
			}
		}
		balances = append(balances, pluginKeyBalance{index: index, key: key, result: result, err: err})
	}
	var failure error
	succeeded := false
	for _, balance := range balances {
		if balance.err == nil {
			succeeded = true
		} else if failure == nil {
			failure = balance.err
		}
	}
	if len(balances) == 0 {
		return nil, nil, nil
	}
	records := make(map[int]model.ChannelBalanceDetail, len(balances))
	for _, balance := range balances {
		record := model.ChannelBalanceDetail{BalanceResult: balance.result}
		if balance.err != nil {
			record.BalanceFailure = balanceFailure(balance.err, balance.key)
		}
		records[balance.index] = record
	}
	detail, err := service.UpdatePluginKeyBalances(channel, records)
	if err != nil {
		common.SysError(common.LogText("failed to store balance detail: channel_id=%d, error=%v", channel.Id, err))
	}
	if !succeeded {
		return balances, detail, failure
	}
	return balances, detail, nil
}

// queryPluginKeyBalance queries the balance of one key line of a task plugin
// channel through the channel's proxy, TLS setting and header overrides.
func queryPluginKeyBalance(ctx context.Context, channel *model.Channel, plugin *jsplugin.LoadedPlugin, index int, key string) (jsplugin.BalanceResult, error) {
	info := &relaycommon.RelayInfo{
		RelayFormat:    types.RelayFormatOpenAI,
		RelayMode:      relayconstant.RelayModeUnknown,
		RequestURLPath: dto.AdvancedCustomBalancePath,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          channel.Type,
			ChannelId:            channel.Id,
			ChannelIsMultiKey:    channel.ChannelInfo.IsMultiKey,
			ChannelMultiKeyIndex: index,
			ChannelBaseUrl:       channel.GetBaseURL(),
			ApiKey:               key,
			ChannelSetting:       channel.GetSetting(),
			ChannelOtherSettings: channel.GetOtherSettings(),
		},
	}
	return providerplugin.QueryBalance(ctx, plugin, info, providerplugin.ManagementClient{
		Override: func(headers http.Header, apiKey string) error {
			return applyFetchModelsHeaderOverrides(channel, apiKey, headers)
		},
		Send: func(request *http.Request) (*http.Response, error) {
			return sendChannelRequest(channel, request)
		},
	})
}

// balanceFailure is a failed balance query as a balance detail keeps it: its
// text, and when it is a console message the message's key and parameters,
// each text without the key line's secrets (see redactBalanceText).
func balanceFailure(err error, key string) model.BalanceFailure {
	failure := model.BalanceFailure{Error: balanceErrorText(err, key)}
	var message *common.Message
	if !errors.As(err, &message) {
		return failure
	}
	failure.ErrorKey = message.Key
	if len(message.Params) > 0 {
		failure.ErrorParams = make(map[string]any, len(message.Params))
		for name, value := range message.Params {
			if text, ok := value.(string); ok {
				value = redactBalanceText(text, key)
			}
			failure.ErrorParams[name] = value
		}
	}
	return failure
}

// balanceErrorText is the failure message a balance detail keeps: without
// the request URL and the key line's secrets, and bounded in length.
func balanceErrorText(err error, key string) string {
	return redactBalanceText(sanitizeFetchModelsError(err, key).Error(), key)
}

// redactBalanceText is text about a key line's balance without the key line,
// a credential line's header and query values in any spelling, and at most
// maxBalanceErrorRunes long.
func redactBalanceText(message string, key string) string {
	message = common.RedactSecret(message, key)
	if credential, ok := jsplugin.ParseCredential(key); ok {
		secrets := make([]string, 0, len(credential.Headers)+len(credential.Query))
		for _, value := range credential.Headers {
			secrets = append(secrets, value)
		}
		for _, value := range credential.Query {
			secrets = append(secrets, value)
		}
		for _, secret := range secrets {
			message = common.RedactSecret(message, secret)
		}
	}
	if runes := []rune(message); len(runes) > maxBalanceErrorRunes {
		message = string(runes[:maxBalanceErrorRunes]) + "..."
	}
	return message
}

// updateBalanceInRun queries one channel in an update of all balances. It
// disables what has no balance left when the channel's automatic disabling
// is on: the channel, or a multi-key plugin channel's empty keys. A
// single-key channel the update disabled for its balance is enabled again
// once a query finds a balance, when automatic enabling is on, as a passing
// channel test enables an automatically disabled channel
// (service.ShouldEnableChannel). A failed query changes neither.
func updateBalanceInRun(channel *model.Channel) {
	awaitingBalance := channel.Status == common.ChannelStatusAutoDisabled
	restore := func() {
		if awaitingBalance && service.ShouldEnableChannel(nil, channel.Status) {
			service.EnableChannel(channel.Id, "", channel.Name)
		}
	}
	if channel.Type == constant.ChannelTypeTaskPlugin {
		plugin, ok := service.PluginOfChannel(channel)
		if !ok || !pluginReportsBalance(plugin) {
			return
		}
		balances, _, _ := updatePluginChannelBalance(channel, plugin, true)
		for _, balance := range balances {
			if balance.err != nil {
				continue
			}
			if positiveBalance(balance.result) && !channel.ChannelInfo.IsMultiKey {
				restore()
				continue
			}
			if awaitingBalance || !emptyBalance(balance.result) {
				continue
			}
			usingKey := ""
			if channel.ChannelInfo.IsMultiKey {
				usingKey = balance.key
			}
			service.DisableChannel(*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, usingKey, channel.GetAutoBan()), insufficientBalanceReason)
		}
		return
	}
	if channel.ChannelInfo.IsMultiKey {
		return // built-in multi-key channels have no balance query
	}
	result, err := withBalanceTimeout(func() (channelBalanceResult, error) {
		return updateChannelBalance(channel)
	})
	if err != nil || result.RawResponse != "" {
		return
	}
	balance := jsplugin.BalanceResult{Amount: &result.Balance}
	switch {
	case positiveBalance(balance):
		restore()
	case emptyBalance(balance) && !awaitingBalance:
		service.DisableChannel(*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, "", channel.GetAutoBan()), insufficientBalanceReason)
	}
}

// withBalanceTimeout waits for a built-in balance query at most
// balanceQueryTimeout. A built-in query takes no context; one that ends later
// still writes its balance, as it always did.
func withBalanceTimeout(query func() (channelBalanceResult, error)) (channelBalanceResult, error) {
	type outcome struct {
		result channelBalanceResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := query()
		done <- outcome{result: result, err: err}
	}()
	timer := time.NewTimer(balanceQueryTimeout)
	defer timer.Stop()
	select {
	case finished := <-done:
		return finished.result, finished.err
	case <-timer.C:
		return channelBalanceResult{}, fmt.Errorf("balance query timed out after %s", balanceQueryTimeout)
	}
}
