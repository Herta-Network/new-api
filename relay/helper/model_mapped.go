package helper

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	rootcommon "github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
)

func ModelMappedHelper(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}

	// map model name
	modelMapping := c.GetString("model_mapping")
	if modelMapping != "" && modelMapping != "{}" {
		modelMap := make(map[string]string)
		err := rootcommon.Unmarshal([]byte(modelMapping), &modelMap)
		if err != nil {
			return fmt.Errorf("unmarshal_model_mapping_failed")
		}

		// 支持链式模型重定向，最终使用链尾的模型
		currentModel := info.OriginModelName
		visitedModels := map[string]bool{
			currentModel: true,
		}
		for {
			mappedModel, exists := modelMap[currentModel]
			baseModel := hostreasoning.BaseModelName(currentModel)
			if (!exists || mappedModel == "") && baseModel != currentModel {
				mappedModel, exists = modelMap[baseModel]
			}
			if exists && mappedModel != "" {
				// 模型重定向循环检测，避免无限循环
				if visitedModels[mappedModel] {
					if mappedModel == currentModel {
						if currentModel == info.OriginModelName {
							info.IsModelMapped = false
							return nil
						}

						info.IsModelMapped = true
						break
					}
					return errors.New("model_mapping_contains_cycle")
				}
				visitedModels[mappedModel] = true
				currentModel = mappedModel
				info.IsModelMapped = true
			} else {
				break
			}
		}
		if info.IsModelMapped {
			info.UpstreamModelName = currentModel
		}
	}

	if request != nil {
		request.SetModelName(info.UpstreamModelName)
	}
	return nil
}

// responseModelMember matches a JSON "model" or "modelVersion" member whose
// value is a string.
var responseModelMember = regexp.MustCompile(`("(?:model|modelVersion)"\s*:\s*)"(?:[^"\\]|\\.)*"`)

// mappedModelWriter answers a request whose channel mapped its model with the
// name the caller asked for: every model and modelVersion member of a JSON or
// event-stream body carries that name, and an error body says it wherever it
// named the upstream model.
type mappedModelWriter struct {
	gin.ResponseWriter
	info *relaycommon.RelayInfo
}

// HideMappedModel makes c's response hide the upstream model the channel of
// each attempt maps info's model to. Model members are rewritten only while
// that attempt's mapping applies, so an unmapped response passes unchanged.
func HideMappedModel(c *gin.Context, info *relaycommon.RelayInfo) {
	c.Writer = &mappedModelWriter{ResponseWriter: c.Writer, info: info}
}

// rewrite returns the relay state when the current attempt's mapping applies
// to the body, and how: members for the model members of a JSON or
// event-stream body, failure for the upstream name in an error body. info is
// nil when the body passes as it is.
func (w *mappedModelWriter) rewrite() (info *relaycommon.RelayInfo, members, failure bool) {
	info = w.info
	if info == nil || info.ChannelMeta == nil || !info.IsModelMapped || info.UpstreamModelName == "" || info.UpstreamModelName == info.OriginModelName {
		return nil, false, false
	}
	contentType := w.Header().Get("Content-Type")
	members = strings.Contains(contentType, "json") || strings.HasPrefix(contentType, "text/event-stream")
	failure = w.Status() >= http.StatusBadRequest
	if !members && !failure {
		return nil, false, false
	}
	return info, members, failure
}

func (w *mappedModelWriter) Write(data []byte) (int, error) {
	// The headers go out with the first write, and a rewritten body need not
	// keep the length they declare, whether it comes in this write or later
	// ones. Without the length net/http sends the body chunked.
	if !w.Written() && w.Header().Get("Content-Length") != "" {
		if info, _, _ := w.rewrite(); info != nil {
			w.Header().Del("Content-Length")
		}
	}
	// Keep-alive pings write from their own goroutine while relay is still
	// setting the mapping; they declare no length and name no model, so they
	// never read it.
	if w.Status() < http.StatusBadRequest && !bytes.Contains(data, []byte(`"model`)) {
		return w.ResponseWriter.Write(data)
	}
	info, members, failure := w.rewrite()
	if info == nil {
		return w.ResponseWriter.Write(data)
	}
	body := data
	if members {
		body = HideModelMembers(body, info.OriginModelName)
	}
	if failure {
		body = []byte(rootcommon.ReplaceModelName(string(body), info.UpstreamModelName, info.OriginModelName))
	}
	if _, err := w.ResponseWriter.Write(body); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *mappedModelWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *mappedModelWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// HideModelMembers sets every model and modelVersion string member of a JSON
// or event-stream body to origin, the model the caller asked for.
func HideModelMembers(body []byte, origin string) []byte {
	quoted, err := rootcommon.Marshal(origin)
	if err != nil {
		return body
	}
	return responseModelMember.ReplaceAllFunc(body, func(member []byte) []byte {
		return slices.Concat(responseModelMember.FindSubmatch(member)[1], quoted)
	})
}
