package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashScopeVideoRequestConvertPreservesNativeBillingInputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/services/aigc/video-generation/video-synthesis", nil)
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Body = http.NoBody

	body := []byte(`{
		"model":"wan3.0-video",
		"input":{
			"prompt":"一只橘猫在窗台上晒太阳",
			"first_frame_url":"https://example.com/first.png"
		},
		"parameters":{
			"resolution":"720P",
			"ratio":"16:9",
			"duration":5
		}
	}`)
	ctx.Request.Body = io.NopCloser(bytes.NewReader(body))
	ctx.Request.ContentLength = int64(len(body))

	handler := DashScopeVideoRequestConvert()
	handler(ctx)

	var request relaycommon.TaskSubmitReq
	require.NoError(t, common.UnmarshalBodyReusable(ctx, &request))

	assert.Equal(t, constant.TaskResponseFormatDashScopeVideo, common.GetContextKeyString(ctx, constant.ContextKeyTaskResponseFormat))
	assert.Equal(t, "wan3.0-video", request.Model)
	assert.Equal(t, "一只橘猫在窗台上晒太阳", request.Prompt)
	assert.Equal(t, 5, request.Duration)

	// metadata 保留 input/parameters 原生结构（供 ali 适配器无损合并），model 剔除
	input, ok := request.Metadata["input"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://example.com/first.png", input["first_frame_url"])
	parameters, ok := request.Metadata["parameters"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "720P", parameters["resolution"])
	assert.Equal(t, "16:9", parameters["ratio"])
	assert.Equal(t, float64(5), parameters["duration"])
	_, hasModel := request.Metadata["model"]
	assert.False(t, hasModel, "model key must be stripped from metadata")
}

func TestDashScopeVideoRequestConvertSmartDurationStaysInMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/services/aigc/video-generation/video-synthesis", nil)
	ctx.Request.Header.Set("Content-Type", "application/json")

	// duration=-1 智能时长：不能进顶层（公共校验会拦），只在 metadata 透传
	body := []byte(`{
		"model":"wan3.0-video",
		"input":{"prompt":"城市夜景延时摄影"},
		"parameters":{"resolution":"480P","duration":-1}
	}`)
	ctx.Request.Body = io.NopCloser(bytes.NewReader(body))
	ctx.Request.ContentLength = int64(len(body))

	handler := DashScopeVideoRequestConvert()
	handler(ctx)

	var request relaycommon.TaskSubmitReq
	require.NoError(t, common.UnmarshalBodyReusable(ctx, &request))

	assert.Equal(t, 0, request.Duration, "smart duration -1 must not surface as top-level duration")
	parameters, ok := request.Metadata["parameters"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(-1), parameters["duration"])
}

func TestDashScopeVideoRequestConvertSkipsNonPost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/tasks/xxx", nil)

	handler := DashScopeVideoRequestConvert()
	handler(ctx)

	assert.Equal(t, constant.TaskResponseFormatDashScopeVideo, common.GetContextKeyString(ctx, constant.ContextKeyTaskResponseFormat))
}
