package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A non-stream answer carries only the upstream headers a client needs to
// read it. Headers that name the upstream provider or account stay with the
// gateway, the gateway's own request id is kept, and the upstream request id
// is still recorded for the log.
func TestIOCopyBytesGracefullyForwardsOnlyClientHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Writer.Header().Set(common.RequestIdKey, "local-1")
	body := []byte(`{"ok":true}`)

	IOCopyBytesGracefully(c, &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   {"audio/mpeg"},
			"Content-Disposition":            {`attachment; filename="speech.mp3"`},
			"Content-Length":                 {"999"},
			"X-Codex-Turn-State":             {"turn-1"},
			"Openai-Organization":            {"org-upstream"},
			"Openai-Project":                 {"proj_upstream"},
			"Anthropic-Organization-Id":      {"anthropic-org"},
			"X-Ratelimit-Remaining-Requests": {"42"},
			"Set-Cookie":                     {"__cf_bm=upstream"},
			"Server":                         {"upstream-server"},
			common.RequestIdKey:              {"upstream-1"},
		},
	}, body)

	assert.Equal(t, http.Header{
		"Content-Type":        {"audio/mpeg"},
		"Content-Disposition": {`attachment; filename="speech.mp3"`},
		"Content-Length":      {strconv.Itoa(len(body))},
		"X-Codex-Turn-State":  {"turn-1"},
		common.RequestIdKey:   {"local-1"},
	}, recorder.Header())
	assert.Equal(t, "upstream-1", c.GetString(common.UpstreamRequestIdKey))
	assert.Equal(t, string(body), recorder.Body.String())
}

// Upstream bodies the relay reads whole are bounded: a body at its limit is
// read, a larger one fails with an error that names the limit. The limit of a
// response body is MAX_RESPONSE_BODY_MB; an error body stops at 1 MiB.
func TestUpstreamBodyReadsAreBounded(t *testing.T) {
	previous := constant.MaxResponseBodyMB
	constant.MaxResponseBodyMB = 2
	t.Cleanup(func() { constant.MaxResponseBodyMB = previous })
	const mib = 1 << 20
	for _, tc := range []struct {
		name    string
		read    func(io.Reader) ([]byte, error)
		size    int
		wantErr string
	}{
		{"response body at the limit", ReadUpstreamBody, 2 * mib, ""},
		{"response body past the limit", ReadUpstreamBody, 2*mib + 1, "upstream response body exceeds the 2 MiB limit"},
		{"error body at the limit", ReadUpstreamErrorBody, mib, ""},
		{"error body past the limit", ReadUpstreamErrorBody, mib + 1, "upstream response body exceeds the 1 MiB limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.read(bytes.NewReader(bytes.Repeat([]byte("x"), tc.size)))
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				assert.Nil(t, data)
				return
			}
			require.NoError(t, err)
			assert.Len(t, data, tc.size)
		})
	}
}
