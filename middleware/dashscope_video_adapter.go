package middleware

import (
	"bytes"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// DashScopeVideoRequestConvert converts the Aliyun DashScope (Bailian) native
// video request — POST /api/v1/services/aigc/video-generation/video-synthesis —
// into the common task request used by routing, validation, and billing.
//
// The native body is {"model": "...", "input": {...}, "parameters": {...}}.
// The input/parameters blocks are retained in Metadata so the ali task
// adaptor can merge them losslessly into the upstream request (field-level
// unmarshal onto AliVideoRequest). The "model" key is stripped from the
// metadata copy so channel-level model mapping stays authoritative.
func DashScopeVideoRequestConvert() gin.HandlerFunc {
	return func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTaskResponseFormat, constant.TaskResponseFormatDashScopeVideo)
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		var nativeRequest map[string]any
		if err := common.UnmarshalBodyReusable(c, &nativeRequest); err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "Invalid request body")
			return
		}

		modelName, _ := nativeRequest["model"].(string)
		prompt := ""
		if input, ok := nativeRequest["input"].(map[string]any); ok {
			prompt, _ = input["prompt"].(string)
		}

		metadata := make(map[string]any, len(nativeRequest))
		for key, value := range nativeRequest {
			if key == "model" {
				continue // model 由统一请求携带，避免覆盖模型映射结果
			}
			metadata[key] = value
		}

		unifiedRequest := map[string]any{
			"model":    modelName,
			"prompt":   prompt,
			"metadata": metadata,
		}
		// parameters.duration 驱动计费倍率。正值同步到顶层供标准校验与
		// 预扣估算使用；-1（wan3.0 智能时长）会触发公共校验
		// "seconds must be between 1 and 3600"，只保留在 metadata 中透传，
		// 由适配器按 30 秒上限估算、完成后按实际时长差额结算。
		if parameters, ok := nativeRequest["parameters"].(map[string]any); ok {
			if duration, ok := parameters["duration"].(float64); ok && duration > 0 {
				unifiedRequest["duration"] = int(duration)
			}
		}

		body, err := common.Marshal(unifiedRequest)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "Failed to convert request body")
			return
		}
		common.CleanupBodyStorage(c)
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Request.ContentLength = int64(len(body))
		c.Set(common.KeyRequestBody, body)
		c.Next()
	}
}
