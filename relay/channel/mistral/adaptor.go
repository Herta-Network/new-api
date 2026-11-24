package mistral

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/relay/channel/opencodego"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// Adaptor serves chat, Messages, Responses and Gemini requests through
// Mistral's chat completions, and embeddings, sending them as Mistral Vibe
// sends its own.
type Adaptor struct {
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	path := "/v1/chat/completions"
	if info.RelayMode == relayconstant.RelayModeEmbeddings {
		path = "/v1/embeddings"
	}
	return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, path, info.ChannelType), nil
}

// SetupRequestHeader sets the headers Vibe's Mistral requests carry: its
// User-Agent, the conversation's affinity, the content types the Mistral SDK
// sends, and the codings and connection httpx2 asks for. The channel's header
// override applies after them.
func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	req.Set("Authorization", "Bearer "+info.ApiKey)
	req.Set("Content-Type", "application/json")
	req.Set("Accept", "application/json")
	if info.IsStream {
		req.Set("Accept", "text/event-stream")
	}
	req.Set("Accept-Encoding", acceptEncoding)
	req.Set("Connection", "keep-alive")
	req.Set("User-Agent", UserAgent)
	if info.RelayMode != relayconstant.RelayModeEmbeddings {
		req.Set(affinityHeader, affinityID(c, info))
	}
	return nil
}

// affinityID is the conversation a completion belongs to, a UUID like
// Vibe's session ids so every turn of it reaches the same prompt cache. A
// client that sends Vibe's header names its conversation; the id is derived
// from it under the user, so two users never share a cache route. Otherwise
// it comes from the conversation the gateway recognized for the user and
// token.
func affinityID(c *gin.Context, info *relaycommon.RelayInfo) string {
	if c != nil && c.Request != nil {
		if id := strings.TrimSpace(c.Request.Header.Get(affinityHeader)); id != "" {
			return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "user:%d\x00affinity:%s", info.UserId, id)).String()
		}
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(opencodego.SessionID(c, info))).String()
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if info.RelayMode != relayconstant.RelayModeChatCompletions {
		return nil, clientError("Mistral does not support the endpoint %s", info.RequestURLPath)
	}
	normalized, err := (&openai.Adaptor{}).ConvertOpenAIRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	return toChatRequest(c, info, normalized)
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	converted, err := (&openai.Adaptor{}).ConvertClaudeRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	return toChatRequest(c, info, converted)
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	converted, err := (&openai.Adaptor{}).ConvertGeminiRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	return toChatRequest(c, info, converted)
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	result, err := service.ConvertRequestByID(c, info, relayconvert.ConverterOpenAIResponsesToOpenAIChat, &request)
	if err != nil {
		return nil, err
	}
	chat, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, fmt.Errorf("expected OpenAI chat completions request, got %T", result.Value)
	}
	normalized, err := (&openai.Adaptor{}).ConvertOpenAIRequest(c, info, chat)
	if err != nil {
		return nil, err
	}
	return toChatRequest(c, info, normalized)
}

// toChatRequest finishes a request the OpenAI adaptor converted to chat as a
// Mistral body, and records the chat hop: Mistral's body is not a gateway
// DTO, and settlement reads usage by the last format of the chain.
func toChatRequest(c *gin.Context, info *relaycommon.RelayInfo, converted any) (any, error) {
	request, ok := converted.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, fmt.Errorf("expected OpenAI chat completions request, got %T", converted)
	}
	out, err := convertChatRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	info.AppendRequestConversion(types.RelayFormatOpenAI)
	return out, nil
}

// embeddingRequest is the body of Mistral's POST /v1/embeddings.
type embeddingRequest struct {
	Model           string          `json:"model"`
	Input           any             `json:"input"`
	OutputDimension json.RawMessage `json:"output_dimension,omitempty"`
	OutputDtype     json.RawMessage `json:"output_dtype,omitempty"`
	EncodingFormat  string          `json:"encoding_format,omitempty"`
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	raw, err := common.Marshal(request.Input)
	if err != nil {
		return nil, err
	}
	input := gjson.ParseBytes(raw)
	valid := input.Type == gjson.String || input.IsArray() && len(input.Array()) > 0
	for _, item := range input.Array() {
		valid = valid && item.Type == gjson.String
	}
	if !valid {
		return nil, clientError("Mistral does not support the requested input: it embeds a text or a list of texts")
	}
	out := &embeddingRequest{Model: request.Model, Input: request.Input, EncodingFormat: request.EncodingFormat}
	// Mistral names the dimensions output_dimension and refuses dimensions;
	// a client of Mistral's own API may send it and output_dtype directly.
	if info.RelayFormat == types.RelayFormatEmbedding {
		native := clientMembers(c, "output_dimension", "output_dtype")
		out.OutputDimension = native["output_dimension"]
		out.OutputDtype = native["output_dtype"]
	}
	if len(out.OutputDimension) == 0 && request.Dimensions != nil {
		out.OutputDimension = json.RawMessage(strconv.Itoa(*request.Dimensions))
	}
	return out, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, clientError("Mistral does not support rerank requests")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("not implemented")
}

// DoRequest sends the body as the Mistral SDK writes it (pythonJSON), after
// the request plugins and the channel's param override had their turn, and
// decodes the answer's content coding. A passed-through body goes out as the
// client wrote it.
func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if !model_setting.GetGlobalSettings().PassThroughRequestEnabled && !info.ChannelSetting.PassThroughBodyEnabled {
		body, err := io.ReadAll(requestBody)
		if err != nil {
			return nil, err
		}
		requestBody = bytes.NewReader(pythonJSON(body))
	}
	resp, err := channel.DoApiRequest(a, c, info, requestBody)
	if err != nil {
		return nil, err
	}
	decodeContent(resp)
	return resp, nil
}

// DoResponse reads Mistral's chat answer, normalized to the OpenAI shape, with
// the handler of the client's format; embeddings need no change.
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == relayconstant.RelayModeEmbeddings {
		return openai.OpenaiHandler(c, info, resp)
	}
	if info.IsStream {
		choices := 1
		if request, ok := info.Request.(*dto.GeneralOpenAIRequest); ok && request.N != nil {
			choices = max(choices, *request.N)
		}
		resp.Body = newStreamNormalizer(resp.Body, choices)
	} else {
		body, readErr := service.ReadUpstreamBody(resp.Body)
		service.CloseResponseBodyGracefully(resp)
		if readErr != nil {
			return nil, types.NewOpenAIError(readErr, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
		}
		if normalized, _, normalizeErr := normalizeChoices(body, "message"); normalizeErr == nil {
			body = normalized
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
	}
	if info.RelayMode == relayconstant.RelayModeResponses {
		if info.IsStream {
			return openai.OaiChatToResponsesStreamHandler(c, info, resp)
		}
		return openai.OaiChatToResponsesHandler(c, info, resp)
	}
	if info.IsStream {
		return openai.OaiStreamHandler(c, info, resp)
	}
	return openai.OpenaiHandler(c, info, resp)
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
