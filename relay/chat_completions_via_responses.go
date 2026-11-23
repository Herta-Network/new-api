package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func applySystemPromptIfNeeded(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) {
	if info == nil || request == nil {
		return
	}
	if info.ChannelSetting.SystemPrompt == "" {
		return
	}
	info.SystemPromptApplied = true

	systemRole := request.GetSystemRoleName()

	// The request is still in chat form, so a system or developer message is
	// the system prompt whatever the model's role name: every channel's
	// conversion reads both as one, and the OpenAI adaptor renames a leading
	// system message to developer afterwards. A Kimi K3 dynamic tool loading
	// message ({"role":"system","tools":[...]}) declares tools rather than a
	// system prompt and must never receive content.
	containSystemPrompt := false
	for _, message := range request.Messages {
		if (message.Role == "system" || message.Role == "developer") && len(message.Tools) == 0 {
			containSystemPrompt = true
			break
		}
	}
	if !containSystemPrompt {
		systemMessage := dto.Message{
			Role:    systemRole,
			Content: info.ChannelSetting.SystemPrompt,
		}
		request.Messages = append([]dto.Message{systemMessage}, request.Messages...)
		return
	}

	if !info.ChannelSetting.SystemPromptOverride {
		return
	}

	common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
	for i, message := range request.Messages {
		if (message.Role != "system" && message.Role != "developer") || len(message.Tools) > 0 {
			continue
		}
		if message.IsStringContent() {
			request.Messages[i].SetStringContent(info.ChannelSetting.SystemPrompt + "\n" + message.StringContent())
			return
		}
		contents := message.ParseContent()
		contents = append([]dto.MediaContent{
			{
				Type: dto.ContentTypeText,
				Text: info.ChannelSetting.SystemPrompt,
			},
		}, contents...)
		request.Messages[i].SetMediaContent(contents)
		return
	}
}

func textRequestViaResponses(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request any) (*dto.Usage, *types.NewAPIError) {
	// A chat request takes the outbound plugins and the param override in
	// chat form, before the conversion.
	outboundApplied := false
	if chatRequest, ok := request.(*dto.GeneralOpenAIRequest); ok {
		chatJSON, err := common.Marshal(chatRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		chatJSON, err = relaycommon.RemoveDisabledFields(chatJSON, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		chatJSON, apiErr := requestplugin.Outbound(c, info, chatJSON)
		if apiErr != nil {
			return nil, apiErr
		}
		if len(info.ParamOverride) > 0 {
			chatJSON, err = relaycommon.ApplyParamOverrideWithRelayInfo(chatJSON, info)
			if err != nil {
				return nil, newAPIErrorFromParamOverride(err)
			}
		}
		outboundApplied = true

		var overriddenChatReq dto.GeneralOpenAIRequest
		if err := common.Unmarshal(chatJSON, &overriddenChatReq); err != nil {
			return nil, types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
		}
		request = &overriddenChatReq
	}

	result, err := service.ConvertRequest(c, info, types.RelayFormatOpenAIResponses, request)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	responsesReq, ok := result.Value.(*dto.OpenAIResponsesRequest)
	if !ok {
		return nil, types.NewError(fmt.Errorf("expected OpenAI responses request, got %T", result.Value), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return relayResponsesRequest(c, info, adaptor, responsesReq, outboundApplied)
}

// responsesReasoningRepairErrorLimit bounds the upstream error read for the
// log line when a request is retried without its restored reasoning.
const responsesReasoningRepairErrorLimit = 4 << 10

// doResponsesRequestWithReasoningRepair sends a Responses request converted
// from a Messages client. When it replays reasoning restored from the client's
// history and the upstream answers 400, it retries once on the same channel
// without that reasoning. Whatever the upstream objected to, the turn then
// goes out as it did before reasoning was carried, and this turn's response
// starts a fresh chain. Every other response is returned as received.
func doResponsesRequestWithReasoningRepair(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, body common.ReplayableBody) (any, error) {
	resp, err := adaptor.DoRequest(c, info, body)
	if err != nil {
		return resp, err
	}
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil || httpResp.StatusCode != http.StatusBadRequest ||
		c.Writer.Written() || c.Request.Context().Err() != nil {
		return resp, nil
	}
	sentReader, err := body.NewReader()
	if err != nil {
		return httpResp, nil
	}
	sent, err := io.ReadAll(sentReader)
	_ = sentReader.Close()
	if err != nil {
		return httpResp, nil
	}
	repaired, changed := removeReplayedResponsesReasoning(sent)
	if !changed {
		return httpResp, nil
	}
	upstreamError, _ := io.ReadAll(io.LimitReader(httpResp.Body, responsesReasoningRepairErrorLimit))
	service.CloseResponseBodyGracefully(httpResp)
	logger.LogWarn(c, common.LogText("channel #%d rejected the reasoning restored into a Responses request, retrying once without it: %s", info.ChannelId, strings.Join(strings.Fields(string(upstreamError)), " ")))
	return adaptor.DoRequest(c, info, bytes.NewReader(repaired))
}

// removeReplayedResponsesReasoning drops the reasoning input items that carry
// encrypted content from an outbound Responses body. In a request converted
// from a Messages client these can only be reasoning restored from the
// client's history. Every other value keeps its bytes.
func removeReplayedResponsesReasoning(body []byte) ([]byte, bool) {
	var document map[string]json.RawMessage
	if common.Unmarshal(body, &document) != nil {
		return nil, false
	}
	var input []json.RawMessage
	if common.Unmarshal(document["input"], &input) != nil {
		return nil, false
	}
	kept := make([]json.RawMessage, 0, len(input))
	for _, item := range input {
		var reasoning struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
		}
		if common.Unmarshal(item, &reasoning) == nil && reasoning.Type == "reasoning" && reasoning.EncryptedContent != "" {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == len(input) {
		return nil, false
	}
	encodedInput, err := common.Marshal(kept)
	if err != nil {
		return nil, false
	}
	document["input"] = encodedInput
	repaired, err := common.Marshal(document)
	if err != nil {
		return nil, false
	}
	return repaired, true
}

func relayResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, responsesReq *dto.OpenAIResponsesRequest, outboundApplied bool) (*dto.Usage, *types.NewAPIError) {
	savedRelayMode := info.RelayMode
	savedRequestURLPath := info.RequestURLPath
	defer func() {
		info.RelayMode = savedRelayMode
		info.RequestURLPath = savedRequestURLPath
	}()

	info.RelayMode = relayconstant.RelayModeResponses
	info.RequestURLPath = "/v1/responses"

	convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *responsesReq)
	if err != nil {
		return nil, newConvertRequestFailedError(c, info, err)
	}
	relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err = disableResponsesStore(info, convertedRequest, jsonData)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if !outboundApplied {
		var apiErr *types.NewAPIError
		if jsonData, apiErr = requestplugin.Outbound(c, info, jsonData); apiErr != nil {
			return nil, apiErr
		}
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
			if err != nil {
				return nil, newAPIErrorFromParamOverride(err)
			}
		}
	}
	if apiErr := requestplugin.Egress(c, info, jsonData); apiErr != nil {
		return nil, apiErr
	}

	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()
	jsonData = nil

	var httpResp *http.Response
	resp, err := doResponsesRequestWithReasoningRepair(c, info, adaptor, body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	if resp == nil {
		return nil, types.NewOpenAIError(nil, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	httpResp = resp.(*http.Response)
	clientStream := info.IsStream
	upstreamStream := isResponsesEventStreamContentType(httpResp.Header.Get("Content-Type"))
	info.IsStream = clientStream || upstreamStream
	if httpResp.StatusCode != http.StatusOK {
		newApiErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return nil, newApiErr
	}

	if upstreamStream && clientStream {
		usage, newApiErr := openaichannel.OaiResponsesToChatStreamHandler(c, info, httpResp)
		if newApiErr != nil {
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return nil, newApiErr
		}
		return usage, nil
	}
	if upstreamStream {
		info.IsStream = false
		usage, newApiErr := openaichannel.OaiResponsesToChatBufferedStreamHandler(c, info, httpResp)
		if newApiErr != nil {
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return nil, newApiErr
		}
		return usage, nil
	}

	usage, newApiErr := openaichannel.OaiResponsesToChatHandler(c, info, httpResp)
	if newApiErr != nil {
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return nil, newApiErr
	}
	return usage, nil
}

func isResponsesEventStreamContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}
