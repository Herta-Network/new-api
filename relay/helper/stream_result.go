package helper

import (
	"errors"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// StreamResult is passed to each dataHandler invocation, providing methods
// to record soft errors, signal fatal stops, or mark normal completion.
// StreamScannerHandler checks IsStopped() after each callback invocation.
type StreamResult struct {
	status  *relaycommon.StreamStatus
	stopped bool
	done    bool
}

func newStreamResult(status *relaycommon.StreamStatus) *StreamResult {
	return &StreamResult{status: status}
}

// Error records a soft error. The stream continues processing.
// Can be called multiple times per chunk.
func (r *StreamResult) Error(err error) {
	if err == nil {
		return
	}
	r.status.RecordError(err.Error())
	var apiErr *types.NewAPIError
	if errors.As(err, &apiErr) {
		r.status.MarkFailed(string(apiErr.GetErrorCode()), apiErr.ToOpenAIError().Type, apiErr.StatusCode)
	}
}

// Stop records a fatal error and marks the stream to stop after this chunk.
func (r *StreamResult) Stop(err error) {
	r.Error(err)
	r.status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, err)
	r.stopped = true
}

// Done signals that the handler has finished processing normally
// (e.g., Dify "message_end"). The stream stops after this chunk.
func (r *StreamResult) Done() {
	r.status.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	r.stopped = true
	r.done = true
}

// IsStopped returns whether Stop() or Done() was called during this chunk.
func (r *StreamResult) IsStopped() bool {
	return r.stopped
}

// reset clears the per-chunk stopped flag so the object can be reused.
func (r *StreamResult) reset() {
	r.stopped = false
}

// ErrUpstreamEndedEarly is the failure a converted stream reports to its
// client when the upstream stream stopped before its terminal event.
var ErrUpstreamEndedEarly = errors.New("upstream stream ended before the response completed")

// UpstreamEndedEarly reports whether the upstream stream of info stopped
// before the terminal event its protocol ends with (message_stop, a
// finish_reason, a Responses terminal event). A converted stream then tells
// its client the response failed instead of synthesizing a successful end,
// and the stop is recorded as a stream error, so the consume log shows the
// failure the client saw.
func UpstreamEndedEarly(info *relaycommon.RelayInfo) bool {
	if info == nil || !info.StreamStatus.EndedBeforeTerminal() {
		return false
	}
	info.StreamStatus.RecordError(ErrUpstreamEndedEarly.Error())
	return true
}
