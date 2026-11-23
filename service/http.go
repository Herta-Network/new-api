package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func CloseResponseBodyGracefully(httpResponse *http.Response) {
	if httpResponse == nil || httpResponse.Body == nil {
		return
	}
	err := httpResponse.Body.Close()
	if err != nil {
		common.SysError(common.LogText("failed to close response body: %s", err.Error()))
	}
}

// clientUpstreamHeaders are the upstream response headers a client needs to
// read the body: its type, the file name of audio and file downloads, and the
// Codex turn headers. Every other header stays with the gateway, because
// headers such as OpenAI-Organization, Anthropic-Organization-Id,
// X-Ratelimit-*, or Set-Cookie name the upstream provider and account.
// Content-Length is set from the body the gateway actually writes.
var clientUpstreamHeaders = []string{
	"Content-Type",
	"Content-Disposition",
	"X-Reasoning-Included",
	"X-Codex-Turn-State",
}

// ShouldCopyUpstreamHeader reports whether an upstream response header may be
// copied to the client response: only the headers in clientUpstreamHeaders
// are. An upstream X-Oneapi-Request-Id is never copied, so the local instance
// ID is kept, and its value is captured into the Gin context for logging.
func ShouldCopyUpstreamHeader(c *gin.Context, k string, v []string) bool {
	if strings.EqualFold(k, common.RequestIdKey) {
		if c != nil && len(v) > 0 {
			c.Set(common.UpstreamRequestIdKey, v[0])
		}
		return false
	}
	return slices.ContainsFunc(clientUpstreamHeaders, func(name string) bool {
		return strings.EqualFold(name, k)
	})
}

// maxUpstreamErrorBodyBytes bounds an upstream error body: it is read only for
// its message.
const maxUpstreamErrorBodyBytes = 1 << 20

// UpstreamBodyTooLargeError is the error of a read that stopped because the
// upstream response body passed its limit.
type UpstreamBodyTooLargeError struct {
	Limit int64
}

func (e *UpstreamBodyTooLargeError) Error() string {
	return fmt.Sprintf("upstream response body exceeds the %d MiB limit", e.Limit>>20)
}

// ReadUpstreamBody reads an upstream response body that the relay buffers
// whole. It reads at most MAX_RESPONSE_BODY_MB, so a broken or hostile
// upstream cannot make the gateway buffer an unbounded body. Past the limit it
// fails with a *types.NewAPIError that wraps *UpstreamBodyTooLargeError and is
// not retried: another channel would send as much again. types.NewOpenAIError
// and types.NewError return that error itself when a handler wraps it. Streams
// are read by the stream scanner, which bounds each event instead.
func ReadUpstreamBody(body io.Reader) ([]byte, error) {
	limit := int64(constant.MaxResponseBodyMB) << 20
	if limit <= 0 {
		limit = constant.DefaultMaxResponseBodyMB << 20
	}
	data, err := readUpstreamBodyUpTo(body, limit)
	var tooLarge *UpstreamBodyTooLargeError
	if errors.As(err, &tooLarge) {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeReadResponseBodyFailed, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	return data, err
}

// ReadUpstreamErrorBody reads an upstream error body, at most 1 MiB.
func ReadUpstreamErrorBody(body io.Reader) ([]byte, error) {
	return readUpstreamBodyUpTo(body, maxUpstreamErrorBodyBytes)
}

func readUpstreamBodyUpTo(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return data, err
	}
	if int64(len(data)) > limit {
		return nil, &UpstreamBodyTooLargeError{Limit: limit}
	}
	return data, nil
}

func IOCopyBytesGracefully(c *gin.Context, src *http.Response, data []byte) {
	if c.Writer == nil {
		return
	}

	body := io.NopCloser(bytes.NewBuffer(data))

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the httpClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	if src != nil {
		for k, v := range src.Header {
			if !ShouldCopyUpstreamHeader(c, k, v) {
				continue
			}
			c.Writer.Header().Set(k, v[0])
		}
	}

	// set Content-Length header manually BEFORE calling WriteHeader
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write header with status code (this sends the headers)
	if src != nil {
		c.Writer.WriteHeader(src.StatusCode)
	} else {
		c.Writer.WriteHeader(http.StatusOK)
	}

	_, err := io.Copy(c.Writer, body)
	if err != nil {
		logger.LogError(c, common.LogText("failed to copy response body: %s", err.Error()))
	}
	c.Writer.Flush()
}
