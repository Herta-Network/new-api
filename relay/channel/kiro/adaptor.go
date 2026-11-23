package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/tokenkit"

	"github.com/gin-gonic/gin"
)

// ChannelName names the adaptor.
const ChannelName = "kiro"

// ModelList is the AI_EDITOR catalog of a Kiro Pro account; each account's
// own catalog decides what it serves.
var ModelList = []string{
	"claude-opus-5.5", "claude-sonnet-5.5", "claude-haiku-5.5",
	"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "grok-4.7", "kimi-k3",
	"claude-opus-5", "claude-sonnet-5", "claude-opus-4.8", "claude-opus-4.7", "claude-opus-4.6", "claude-opus-4.5",
	"claude-sonnet-4.6", "claude-sonnet-4.5", "claude-haiku-4.5",
	"deepseek-3.2", "minimax-m2.5", "minimax-m2.1", "glm-5", "qwen3-coder-next", "auto",
}

// tokenRefusalLimit bounds the error body read to tell a refused access token
// from other refusals.
const tokenRefusalLimit = 64 << 10

// claudeToolUseTokens is the output a tool_use block costs beyond its name
// and input, as the Claude stream fallback counts it.
const claudeToolUseTokens = 40

// prepareTimeout bounds the catalog and profile lookups of one attempt.
const prepareTimeout = 15 * time.Second

// Adaptor serves one attempt on a Kiro channel. It renews the channel's
// credential and reads the account's catalog before converting, because the
// model's request schema and the account's features decide how a request is
// fitted.
type Adaptor struct {
	claude claude.Adaptor

	prepared   bool
	prepareErr *types.NewAPIError
	client     *http.Client
	credential Credential
	runtime    string
	model      Model
	prompt     int

	// generate reports GenerateAssistantResponse; otherwise the attempt goes
	// to CreateResponse.
	generate bool
	// outputCap is the client's output cap where Kiro cannot enforce it; the
	// relay then ends the response at it. Zero is no cap.
	outputCap int
	// renewedAfterRefusal reports an attempt that already renewed its access
	// token after the Runtime refused it.
	renewedAfterRefusal bool
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {}

// PrepareAttempt renews the credential and reads the model's catalog entry.
// Its error fails the attempt as it is, so routing moves on to another
// channel when the credential cannot be used.
func (a *Adaptor) PrepareAttempt(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if a.prepared {
		return a.prepareErr
	}
	a.prepared = true
	a.prepareErr = a.prepare(c, info)
	return a.prepareErr
}

func (a *Adaptor) prepare(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		return types.NewErrorWithStatusCode(errors.New("Kiro channels convert every request and cannot pass the request body through"), types.ErrorCodeConvertRequestFailed, http.StatusBadRequest)
	}
	if info.ChannelIsMultiKey {
		return a.fail(c, info, ErrCredential)
	}
	client, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		return types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	a.client = &noRedirect

	if f, failing := currentFailure(info.ChannelId, info.ApiKey); failing {
		return a.cooling(c, info, f)
	}
	credential, err := ParseCredential(info.ApiKey)
	if err != nil {
		return a.fail(c, info, err)
	}
	a.credential = credential
	if !credential.Usable(time.Now()) || revoked(credential.AccessToken) {
		if apiErr := a.renewCredential(c, info); apiErr != nil {
			return apiErr
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), prepareTimeout)
	defer cancel()
	for renewed := false; ; renewed = true {
		err = nil
		profile := a.credential.ProfileArn
		a.runtime = runtimeURL(info.ChannelBaseUrl, profile, a.credential.authRegion())
		if profile == "" {
			if profile, err = profileFor(ctx, a.client, a.runtime, a.credential.AccessToken); err == nil {
				a.credential.ProfileArn = profile
				a.runtime = runtimeURL(info.ChannelBaseUrl, profile, a.credential.authRegion())
			}
		}
		if err == nil {
			a.model, err = catalogs.lookup(ctx, a.client, a.runtime, a.credential.AccessToken, profile, info.UpstreamModelName)
		}
		if err == nil {
			break
		}
		if statusOf(err) != http.StatusUnauthorized {
			return a.fail(c, info, err)
		}
		// Kiro refused the access token before its expiry: renew it once. Only
		// a token nothing can renew is an unusable credential.
		revoke(a.credential.AccessToken)
		if a.credential.Method == "access_token" {
			return a.fail(c, info, ErrCredential)
		}
		if renewed {
			return a.fail(c, info, err)
		}
		if apiErr := a.renewCredential(c, info); apiErr != nil {
			return apiErr
		}
	}
	clearFailure(info.ChannelId, info.ApiKey)

	// Kiro reports no token counts, so the input is the request's estimate,
	// counted here when relay estimation is off.
	a.prompt = info.GetEstimatePromptTokens()
	if a.prompt == 0 && info.Request != nil {
		if meta := info.Request.GetTokenCountMeta(); meta != nil {
			if n, err := service.CountRequestToken(c, meta, info); err == nil {
				a.prompt = n
			}
		}
	}
	return nil
}

// renewCredential renews the attempt's credential and moves the attempt to
// the renewed line. An access_token credential cannot be renewed.
func (a *Adaptor) renewCredential(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if a.credential.Method == "access_token" {
		return a.fail(c, info, ErrCredential)
	}
	line, err := renew(c.Request.Context(), a.client, info.ChannelId, info.ChannelBaseUrl, info.ApiKey)
	if err != nil {
		if c.Request.Context().Err() == nil {
			logRenewalFailure(info.ChannelId, err)
		}
		return a.fail(c, info, err)
	}
	credential, err := ParseCredential(line)
	if err != nil {
		return a.fail(c, info, err)
	}
	a.credential = credential
	// Errors and the routing record of this attempt name the line it used.
	info.ApiKey = line
	common.SetContextKey(c, constant.ContextKeyChannelKey, line)
	return nil
}

// fail turns a preparation failure into the attempt's error and keeps the
// channel out of routing for the request's model while it lasts. A client
// that went away fails nothing.
func (a *Adaptor) fail(c *gin.Context, info *relaycommon.RelayInfo, err error) *types.NewAPIError {
	if c.Request.Context().Err() != nil {
		return types.NewErrorWithStatusCode(errors.New("the client closed the request"), types.ErrorCodeDoRequestFailed, 499, types.ErrOptionWithSkipRetry())
	}
	if errors.Is(err, errModelUnavailable) {
		model.RecordChannelRateLimit(info.ChannelId, "", info.OriginModelName, permanentCooldown)
		return types.NewErrorWithStatusCode(fmt.Errorf("%w: %s", errModelUnavailable, info.UpstreamModelName), types.ErrorCodeModelNotFound, http.StatusServiceUnavailable)
	}
	return a.cooling(c, info, recordFailure(info.ChannelId, info.ApiKey, err, permanentFailure(err)))
}

func (a *Adaptor) cooling(c *gin.Context, info *relaycommon.RelayInfo, f credentialFailure) *types.NewAPIError {
	model.RecordChannelRateLimit(info.ChannelId, "", info.OriginModelName, time.Until(f.until))
	if f.permanent {
		message := "the Kiro credential cannot be used"
		switch {
		case errors.Is(f.err, ErrRejected):
			message = "Kiro refused the channel credential"
		case errors.Is(f.err, errProfileUnresolved):
			message = errProfileUnresolved.Error()
		}
		return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeChannelInvalidKey, http.StatusUnauthorized)
	}
	return types.NewErrorWithStatusCode(errors.New("the Kiro credential could not be renewed"), types.ErrorCodeDoRequestFailed, http.StatusServiceUnavailable)
}

// scope names the account, client and model a conversation belongs to.
func (a *Adaptor) scope(info *relaycommon.RelayInfo) string {
	return strings.Join([]string{strconv.Itoa(info.ChannelId), a.credential.ProfileArn, strconv.Itoa(info.UserId), strconv.Itoa(info.TokenId), info.UpstreamModelName}, "\x00")
}

// fields lists the top-level members of a request.
func fields(request any) (map[string]json.RawMessage, error) {
	raw, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	var members map[string]json.RawMessage
	if err := common.Unmarshal(raw, &members); err != nil {
		return nil, err
	}
	return members, nil
}

// clientError is a request Kiro cannot serve whatever the channel.
func clientError(err error) error {
	var field fieldError
	if errors.As(err, &field) {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return err
}

func (a *Adaptor) generateBody(info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	members, err := fields(request)
	if err != nil {
		return nil, err
	}
	body, err := generateAssistantResponse(members, a.credential.ProfileArn, a.scope(info), a.model)
	if err != nil {
		return nil, clientError(err)
	}
	a.generate = true
	// Kiro caps output only for a model whose schema takes max_tokens, and
	// never below the schema's minimum; the relay ends the response at any
	// smaller cap the client asked for.
	a.outputCap = 0
	if request.MaxTokens != nil && *request.MaxTokens > 0 {
		requested := int64(min(*request.MaxTokens, math.MaxInt32))
		if !a.model.TakesOutputLimit() || a.model.OutputLimit(requested) > requested {
			a.outputCap = int(requested)
		}
	}
	return body, nil
}

func (a *Adaptor) responseBody(info *relaycommon.RelayInfo, request *dto.OpenAIResponsesRequest) (any, error) {
	members, err := fields(request)
	if err != nil {
		return nil, err
	}
	body, err := createResponse(members, a.credential.ProfileArn, a.scope(info), a.model)
	if err != nil {
		return nil, clientError(err)
	}
	a.generate = false
	// CreateResponse has no output cap; the relay ends the response at the
	// client's.
	a.outputCap = 0
	if request.MaxOutputTokens != nil && *request.MaxOutputTokens > 0 {
		a.outputCap = int(min(*request.MaxOutputTokens, math.MaxInt32))
	}
	return body, nil
}

// ConvertOpenAIRequest sends a Chat request to a model that takes an output
// cap (Claude 5.x) as Messages over GenerateAssistantResponse, where the cap
// holds, and to any other model as Responses over CreateResponse.
func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if err := a.PrepareAttempt(c, info); err != nil {
		return nil, err
	}
	if request.Stop != nil {
		return nil, clientError(fieldError{"stop"})
	}
	if request.ResponseFormat != nil && request.ResponseFormat.Type != "" && request.ResponseFormat.Type != "text" {
		return nil, clientError(fieldError{"response_format"})
	}
	if !a.model.TakesOutputLimit() {
		result, err := service.ConvertRequestByID(c, info, relayconvert.ConverterOpenAIChatToOpenAIResponses, request)
		if err != nil {
			return nil, err
		}
		responsesRequest, ok := result.Value.(*dto.OpenAIResponsesRequest)
		if !ok {
			return nil, fmt.Errorf("expected OpenAI Responses request, got %T", result.Value)
		}
		return a.responseBody(info, responsesRequest)
	}
	// Kiro has no sampling controls, and the Messages conversion would refuse
	// them next to the model's thinking.
	if !a.model.TakesOption("temperature") {
		request.Temperature = nil
	}
	if !a.model.TakesOption("top_p") {
		request.TopP = nil
	}
	if !a.model.TakesOption("top_k") {
		request.TopK = nil
	}
	capped := request.MaxTokens != nil && *request.MaxTokens > 0 || request.MaxCompletionTokens != nil && *request.MaxCompletionTokens > 0
	result, err := service.ConvertRequestByID(c, info, relayconvert.ConverterOpenAIChatToClaudeMessages, request)
	if err != nil {
		return nil, err
	}
	claudeRequest, ok := result.Value.(*dto.ClaudeRequest)
	if !ok {
		return nil, fmt.Errorf("expected Anthropic Messages request, got %T", result.Value)
	}
	if !capped {
		// The conversion's default cap is not the client's; the model's
		// ceiling applies.
		claudeRequest.MaxTokens = nil
	}
	return a.generateBody(info, claudeRequest)
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	if err := a.PrepareAttempt(c, info); err != nil {
		return nil, err
	}
	return a.generateBody(info, request)
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	if err := a.PrepareAttempt(c, info); err != nil {
		return nil, err
	}
	// A Chat or Messages request relayed through Responses already carries
	// the channel system prompt, applied before the conversion.
	if prompt := info.ChannelSetting.SystemPrompt; prompt != "" && !info.SystemPromptApplied {
		info.SystemPromptApplied = true
		var existing string
		_ = common.Unmarshal(request.Instructions, &existing)
		switch {
		case strings.TrimSpace(existing) == "":
			request.Instructions = marshalRaw(prompt)
		case info.ChannelSetting.SystemPromptOverride:
			request.Instructions = marshalRaw(prompt + "\n" + existing)
		}
	}
	return a.responseBody(info, &request)
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("kiro channel: Gemini requests are not supported")
}

func (a *Adaptor) ConvertRerankRequest(*gin.Context, int, dto.RerankRequest) (any, error) {
	return nil, errors.New("kiro channel: rerank is not supported")
}

func (a *Adaptor) ConvertEmbeddingRequest(*gin.Context, *relaycommon.RelayInfo, dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("kiro channel: embeddings are not supported")
}

func (a *Adaptor) ConvertAudioRequest(*gin.Context, *relaycommon.RelayInfo, dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("kiro channel: audio is not supported")
}

func (a *Adaptor) ConvertImageRequest(*gin.Context, *relaycommon.RelayInfo, dto.ImageRequest) (any, error) {
	return nil, errors.New("kiro channel: images are not supported")
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if !a.prepared || a.prepareErr != nil {
		return "", errors.New("kiro channel: the attempt was not prepared")
	}
	if a.generate {
		return a.runtime + "/", nil
	}
	return a.runtime + "/v1/responses", nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	header.Set("Authorization", "Bearer "+a.credential.AccessToken)
	header.Set("User-Agent", userAgent)
	header.Set("X-Amzn-Codewhisperer-Optout", "true")
	header.Set("X-Amzn-Kiro-Attempt", "1;max=1")
	if a.generate {
		header.Set("Content-Type", "application/x-amz-json-1.0")
		header.Set("Accept", "application/vnd.amazon.eventstream")
		header.Set("X-Amz-User-Agent", userAgent)
		header.Set("X-Amz-Target", "AmazonCodeWhispererStreamingService.GenerateAssistantResponse")
		header.Set("X-Amzn-Kiro-Agent-Mode", "vibe")
		return nil
	}
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "text/event-stream")
	header.Set("X-Amzn-Kiro-Origin", "AI_EDITOR")
	header.Set("X-Amzn-Kiro-Profile", a.credential.ProfileArn)
	return nil
}

// retryRefusedToken handles the Runtime refusing the access token (a 401, or
// Kiro's AccessDeniedException about the token): the token is dropped,
// renewed once and the request sent again. A refusal after that, or of a
// token nothing can renew, fails the attempt as a credential failure without
// passing on a status that would disable the channel.
func (a *Adaptor) retryRefusedToken(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, requestBody io.Reader) (any, error) {
	service.CloseResponseBodyGracefully(resp)
	revoke(a.credential.AccessToken)
	if a.credential.Method == "access_token" {
		return nil, a.fail(c, info, ErrCredential)
	}
	body, replayable := requestBody.(common.ReplayableBody)
	if a.renewedAfterRefusal || !replayable {
		return nil, a.fail(c, info, statusError{status: http.StatusUnauthorized})
	}
	a.renewedAfterRefusal = true
	if apiErr := a.renewCredential(c, info); apiErr != nil {
		return nil, apiErr
	}
	reader, err := body.NewReader()
	if err != nil {
		return nil, err
	}
	return a.DoRequest(c, info, reader)
}

// DoRequest sends the attempt and turns Kiro's answer into the client's
// protocol: Messages events or a message for GenerateAssistantResponse, and
// Responses events or a response for CreateResponse, which always streams.
func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	resp, err := channel.DoApiRequest(a, c, info, requestBody)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		if ms, err := strconv.ParseFloat(strings.TrimSpace(resp.Header.Get("X-Amzn-Kiro-Ratelimit-Retry-After")), 64); err == nil && ms > 0 && ms <= math.MaxInt32 {
			resp.Header.Set("Retry-After-Ms", strconv.FormatFloat(ms, 'f', -1, 64))
			if resp.StatusCode == http.StatusTooManyRequests {
				service.NoteUpstreamRateLimit(c, info.ChannelId, resp.Header)
			}
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden {
			// Inspect a bounded error, and keep every byte for the relay's error
			// handling when it does not refuse the token.
			upstream := resp.Body
			errorBody, readErr := io.ReadAll(io.LimitReader(upstream, tokenRefusalLimit+1))
			resp.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(errorBody), upstream), upstream}
			if readErr == nil && len(errorBody) <= tokenRefusalLimit && tokenRefused(resp.StatusCode, errorBody) {
				return a.retryRefusedToken(c, info, resp, requestBody)
			}
		}
		// Any other refusal, such as a 403 for the account's allowance or
		// access, leaves the token as it is.
		return resp, nil
	}
	want := "text/event-stream"
	if a.generate {
		want = "application/vnd.amazon.eventstream"
	}
	if media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); media != want {
		service.CloseResponseBodyGracefully(resp)
		return nil, types.NewErrorWithStatusCode(errors.New("Kiro returned an unexpected content type"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	resp.Header.Del("Content-Length")
	resp.Header.Del("Content-Encoding")
	resp.ContentLength = -1
	maxBytes := int(constant.MaxResponseBodyMB) << 20
	if maxBytes <= 0 {
		maxBytes = constant.DefaultMaxResponseBodyMB << 20
	}
	var body []byte
	if a.generate {
		meter := garMeter{prompt: a.prompt, limit: a.outputCap, output: func(text string, toolUses int) int {
			return service.ResponseText2Usage(c, text, info.UpstreamModelName, a.prompt).CompletionTokens + toolUses*claudeToolUseTokens
		}, count: func(text string) int {
			return tokenkit.Estimate(info.UpstreamModelName, text)
		}}
		if info.IsStream {
			resp.Body = newGARStream(resp.Body, info.UpstreamModelName, true, meter, 0)
			resp.Header.Set("Content-Type", "text/event-stream")
			return resp, nil
		}
		body, err = newGARStream(resp.Body, info.UpstreamModelName, false, meter, maxBytes).message()
	} else {
		meter := responsesMeter{prompt: a.prompt, limit: a.outputCap, output: func(text string) int {
			common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
			return tokenkit.Count(info.UpstreamModelName, text)
		}, count: func(text string) int {
			return tokenkit.Count(info.UpstreamModelName, text)
		}}
		if info.IsStream {
			resp.Body = newResponsesStream(resp.Body, true, meter, 0)
			return resp, nil
		}
		body, err = newResponsesStream(resp.Body, false, meter, maxBytes).response()
	}
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	resp.Header.Set("Content-Type", "application/json")
	resp.ContentLength = int64(len(body))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch {
	case a.generate:
		usage, err = a.claude.DoResponse(c, resp, info)
	case info.RelayFormat == types.RelayFormatOpenAIResponses:
		info.FinalRequestRelayFormat = types.RelayFormatOpenAIResponses
		if info.IsStream {
			usage, err = openai.OaiResponsesStreamHandler(c, info, resp)
		} else {
			usage, err = openai.OaiResponsesHandler(c, info, resp)
		}
	default:
		// These handlers convert the Responses upstream into info.RelayFormat.
		info.FinalRequestRelayFormat = types.RelayFormatOpenAIResponses
		if info.IsStream {
			usage, err = openai.OaiResponsesToChatStreamHandler(c, info, resp)
		} else {
			usage, err = openai.OaiResponsesToChatHandler(c, info, resp)
		}
	}
	// The Messages and native Responses relays settle a stream that stopped
	// early on what it delivered. That is right for a client that went away,
	// but a Kiro stream that ended before its stop signal or terminal event
	// failed and is not settled. (The Chat handlers check this themselves.)
	if err == nil && info.IsStream && (a.generate || info.RelayFormat == types.RelayFormatOpenAIResponses) && helper.UpstreamEndedEarly(info) {
		err = types.NewErrorWithStatusCode(errIncomplete, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if err != nil && c.Writer.Written() {
		// Part of the response reached the client; another channel cannot
		// continue it.
		types.ErrOptionWithSkipRetry()(err)
	}
	return usage, err
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
