package fly4k

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 本文件锁定「方舟（Seedance）原生协议」接入：下游客户沿用方舟官方调用方式，
// 只改 base_url + key 即可使用蝶变（fly4k）上游。

func newNativeShellContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	common.SetContextKey(ctx, constant.ContextKeyTaskResponseFormat, constant.TaskResponseFormatDoubaoVideo)
	return ctx, rec
}

func newOpenAIShellContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	return ctx, rec
}

// 方舟原生壳：素材从 metadata.content[] 按 role 归一，resolution/camera_fixed/model 一律不透传。
func TestBuildSubmitRequestParsesNativeShellContent(t *testing.T) {
	ctx, _ := newNativeShellContext(t)
	req := relaycommon.TaskSubmitReq{
		Prompt:   "单人跳骑马舞",
		Duration: 4,
		Metadata: map[string]any{
			"model":        "doubao-seedance-2-0-260128",
			"ratio":        "9:16",
			"resolution":   "720p",
			"camera_fixed": true,
			"content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://asset-na-first"}, "role": "first_frame"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://asset-na-ref"}, "role": "reference_image"},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://cdn.example.com/ref.mp4"}, "role": "reference_video"},
				map[string]any{"type": "text", "text": "单人跳骑马舞"},
			},
		},
	}

	body, err := buildSubmitRequest(ctx, &req)
	require.NoError(t, err)

	assert.Equal(t, 4, body.Duration)
	assert.Equal(t, "9:16", body.Ratio)
	require.Len(t, body.Content, 4)

	assert.Equal(t, flyContent{Type: "image_url", ImageURL: &flyMedia{URL: "asset://asset-na-first"}, Role: "first_frame"}, body.Content[0])
	assert.Equal(t, flyContent{Type: "image_url", ImageURL: &flyMedia{URL: "asset://asset-na-ref"}, Role: "reference_image"}, body.Content[1])
	assert.Equal(t, flyContent{Type: "video_url", VideoURL: &flyMedia{URL: "https://cdn.example.com/ref.mp4"}, Role: "reference_video"}, body.Content[2])
	assert.Equal(t, flyContent{Type: "text", Text: "单人跳骑马舞"}, body.Content[3])

	// 上游无 resolution/camera_fixed 参数，model 亦写死 → 三者绝不能出现在上游请求体里
	data, err := common.Marshal(body)
	require.NoError(t, err)
	var upstream map[string]any
	require.NoError(t, common.Unmarshal(data, &upstream))
	for _, forbidden := range []string{"model", "resolution", "camera_fixed", "content_array"} {
		_, exists := upstream[forbidden]
		assert.Falsef(t, exists, "字段 %q 不应出现在蝶变上游请求体", forbidden)
	}
}

// 方舟原生写法里 image_url 不带 role → 按拍板决策视为首帧。
func TestBuildSubmitRequestNativeShellDefaultsToFirstFrame(t *testing.T) {
	ctx, _ := newNativeShellContext(t)
	req := relaycommon.TaskSubmitReq{
		Prompt:   "走两步",
		Metadata: map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "走两步"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://asset-na-norole"}},
		}},
	}

	body, err := buildSubmitRequest(ctx, &req)
	require.NoError(t, err)
	require.Len(t, body.Content, 2)
	assert.Equal(t, "first_frame", body.Content[0].Role)
	assert.Equal(t, "asset://asset-na-norole", body.Content[0].ImageURL.URL)
	assert.Equal(t, "16:9", body.Ratio, "未指定 ratio 时按上游默认兜底")
}

// 尾帧 role 也走原生壳；多个尾帧只有第一个保留为 last_frame。
func TestBuildSubmitRequestNativeShellLastFrame(t *testing.T) {
	ctx, _ := newNativeShellContext(t)
	req := relaycommon.TaskSubmitReq{
		Prompt: "首尾帧",
		Metadata: map[string]any{"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn/a.png"}, "role": "first_frame"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn/z1.png"}, "role": "last_frame"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn/z2.png"}, "role": "last_frame"},
		}},
	}

	body, err := buildSubmitRequest(ctx, &req)
	require.NoError(t, err)
	require.Len(t, body.Content, 4, "首帧 + 尾帧 + 降级参考图 + 文本")
	assert.Equal(t, "first_frame", body.Content[0].Role)
	assert.Equal(t, "last_frame", body.Content[1].Role)
	assert.Equal(t, "https://cdn/z1.png", body.Content[1].ImageURL.URL)
	assert.Equal(t, "reference_image", body.Content[2].Role, "多出的尾帧降级为参考图")
	assert.Equal(t, "https://cdn/z2.png", body.Content[2].ImageURL.URL)
}

// 回归保护：OpenAI 壳下 metadata 里即使出现同名 content，也不得被当成方舟原生报文解析，
// 且既有 image/images 归一语义保持不变（Image 优先作首帧、Images 其余降级参考图）。
func TestBuildSubmitRequestOpenAIShellIgnoresMetadataContent(t *testing.T) {
	ctx, _ := newOpenAIShellContext(t)
	req := relaycommon.TaskSubmitReq{
		Prompt: "oai 壳",
		Image:  "https://cdn/first.png",
		Images: []string{"https://cdn/first.png", "https://cdn/extra.png"},
		Metadata: map[string]any{
			"ratio": "1:1",
			"content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn/should-be-ignored.png"}, "role": "first_frame"},
			},
		},
	}

	body, err := buildSubmitRequest(ctx, &req)
	require.NoError(t, err)
	require.Len(t, body.Content, 3)
	assert.Equal(t, "https://cdn/first.png", body.Content[0].ImageURL.URL)
	assert.Equal(t, "first_frame", body.Content[0].Role)
	assert.Equal(t, "https://cdn/extra.png", body.Content[1].ImageURL.URL)
	assert.Equal(t, "reference_image", body.Content[1].Role)
	assert.Equal(t, "1:1", body.Ratio)
}

// 同一素材出现在两种壳的字段里时必须去重，否则上游会报
// first/last frame content cannot be mixed with reference media content。
func TestBuildSubmitRequestDeduplicatesAssetsAcrossShells(t *testing.T) {
	ctx, _ := newNativeShellContext(t)
	req := relaycommon.TaskSubmitReq{
		Prompt: "去重",
		Image:  "asset://asset-na-dup",
		Images: []string{"asset://asset-na-dup"},
		Metadata: map[string]any{"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://asset-na-dup"}, "role": "first_frame"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://asset-na-dup"}, "role": "reference_image"},
		}},
	}

	body, err := buildSubmitRequest(ctx, &req)
	require.NoError(t, err)
	require.Len(t, body.Content, 2)
	assert.Equal(t, "first_frame", body.Content[0].Role)
	assert.Equal(t, "asset://asset-na-dup", body.Content[0].ImageURL.URL)
	assert.Equal(t, "text", body.Content[1].Type)
}

// 非法 ratio 仍然在本地拦下（上游只认固定白名单）。
func TestBuildSubmitRequestRejectsUnsupportedRatioInNativeShell(t *testing.T) {
	ctx, _ := newNativeShellContext(t)
	req := relaycommon.TaskSubmitReq{
		Prompt:   "x",
		Metadata: map[string]any{"ratio": "5:3"},
	}
	_, err := buildSubmitRequest(ctx, &req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ratio")
}

// 提交响应：方舟壳只回 {"id": ...}，OpenAI 壳回完整 OpenAI Video 对象。
func TestDoResponseReturnsOnlyIDForNativeShell(t *testing.T) {
	ctx, rec := newNativeShellContext(t)
	info := &relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2-0-260128",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_pub_1"},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"code":"0","msg":"success","data":{"taskKey":"UP-1","creditCost":403}}`)),
	}

	adaptor := &TaskAdaptor{}
	taskID, _, taskErr := adaptor.DoResponse(ctx, resp, info)

	require.Nil(t, taskErr)
	assert.Equal(t, "UP-1", taskID, "落库仍用上游 taskKey 轮询")
	assert.JSONEq(t, `{"id":"task_pub_1"}`, rec.Body.String())
}

func TestDoResponseKeepsOpenAIShellShape(t *testing.T) {
	ctx, rec := newOpenAIShellContext(t)
	info := &relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2-0-260128",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_pub_2"},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"code":"0","msg":"success","data":{"taskKey":"UP-2"}}`)),
	}

	adaptor := &TaskAdaptor{}
	_, _, taskErr := adaptor.DoResponse(ctx, resp, info)
	require.Nil(t, taskErr)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, "task_pub_2", payload["id"])
	assert.Equal(t, "task_pub_2", payload["task_id"])
	assert.Equal(t, "video", payload["object"])
}

// 上游信封里 code != 0 时按上游失败返回（原生壳同样）。
func TestDoResponseSurfacesUpstreamFailure(t *testing.T) {
	ctx, _ := newNativeShellContext(t)
	info := &relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2-0-260128",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_pub_3"},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"code":"FK0019","msg":"入库失败","data":null}`)),
	}

	adaptor := &TaskAdaptor{}
	_, _, taskErr := adaptor.DoResponse(ctx, resp, info)
	require.NotNil(t, taskErr)
	assert.Contains(t, taskErr.Message, "FK0019")
}

// ConvertToNativeVideo：方舟 v3 查询契约（状态映射 + 地址提取）。
func TestConvertToNativeVideoMapsTaskStatus(t *testing.T) {
	adaptor := &TaskAdaptor{}
	const upstreamBody = `{"code":"0","msg":"ok","data":{"taskKey":"UP-9","status":"succeeded","outputUrl":"https://cdn.example.com/out.mp4"}}`

	cases := []struct {
		name           string
		status         model.TaskStatus
		wantStatus     string
		wantVideoURL   string
		wantErrMessage string
	}{
		{name: "queued", status: model.TaskStatusQueued, wantStatus: "queued"},
		{name: "not_start", status: model.TaskStatusNotStart, wantStatus: "queued"},
		{name: "running", status: model.TaskStatusInProgress, wantStatus: "running"},
		{name: "succeeded", status: model.TaskStatusSuccess, wantStatus: "succeeded", wantVideoURL: "https://cdn.example.com/out.mp4"},
		{name: "failed", status: model.TaskStatusFailure, wantStatus: "failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{
				TaskID:     "task_pub_9",
				Status:     tc.status,
				CreatedAt:  1700,
				UpdatedAt:  1800,
				Properties: model.Properties{OriginModelName: "doubao-seedance-2-0-260128"},
				Data:       []byte(upstreamBody),
			}

			raw, err := adaptor.ConvertToNativeVideo(task)
			require.NoError(t, err)

			var response nativeVideoResponse
			require.NoError(t, common.Unmarshal(raw, &response))
			assert.Equal(t, "task_pub_9", response.ID)
			assert.Equal(t, "doubao-seedance-2-0-260128", response.Model)
			assert.Equal(t, tc.wantStatus, response.Status)
			assert.Equal(t, int64(1700), response.CreatedAt)
			assert.Equal(t, int64(1800), response.UpdatedAt)

			if tc.wantVideoURL != "" {
				require.NotNil(t, response.Content)
				assert.Equal(t, tc.wantVideoURL, response.Content.VideoURL)
			} else {
				assert.Nil(t, response.Content)
			}
			// 蝶变上游无 resolution/usage 回显，不得凭空编造
			assert.NotContains(t, string(raw), "resolution")
			assert.NotContains(t, string(raw), "usage")
		})
	}
}

// 失败任务需带出上游原因（优先信封 errorDesc，回退本地 fail_reason）。
func TestConvertToNativeVideoFailureCarriesReason(t *testing.T) {
	adaptor := &TaskAdaptor{}

	fromEnvelope := &model.Task{
		TaskID:     "task_pub_f1",
		Status:     model.TaskStatusFailure,
		Properties: model.Properties{OriginModelName: "doubao-seedance-2-0-260128"},
		Data:       []byte(`{"code":"0","msg":"","data":{"status":"failed","errorDesc":"内容审核未通过"}}`),
	}
	raw, err := adaptor.ConvertToNativeVideo(fromEnvelope)
	require.NoError(t, err)
	var response nativeVideoResponse
	require.NoError(t, common.Unmarshal(raw, &response))
	require.NotNil(t, response.Error)
	assert.Equal(t, "内容审核未通过", response.Error.Message)

	fromLocal := &model.Task{
		TaskID:     "task_pub_f2",
		Status:     model.TaskStatusFailure,
		FailReason: "上游超时",
		Properties: model.Properties{OriginModelName: "doubao-seedance-2-0-260128"},
	}
	raw, err = adaptor.ConvertToNativeVideo(fromLocal)
	require.NoError(t, err)
	response = nativeVideoResponse{}
	require.NoError(t, common.Unmarshal(raw, &response))
	require.NotNil(t, response.Error)
	assert.Equal(t, "上游超时", response.Error.Message)
}

// 成功但信封解析不到视频地址时，回退任务结果 URL，不能返回空 content。
func TestConvertToNativeVideoFallsBackToResultURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	task := &model.Task{
		TaskID:     "task_pub_f3",
		Status:     model.TaskStatusSuccess,
		Properties: model.Properties{OriginModelName: "doubao-seedance-2-0-260128"},
		Data:       []byte(`{"code":"0","msg":"","data":{"status":"succeeded"}}`),
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://cdn.example.com/fallback.mp4",
		},
	}

	raw, err := adaptor.ConvertToNativeVideo(task)
	require.NoError(t, err)
	var response nativeVideoResponse
	require.NoError(t, common.Unmarshal(raw, &response))
	require.NotNil(t, response.Content)
	assert.Equal(t, "https://cdn.example.com/fallback.mp4", response.Content.VideoURL)
}

// 渠道注册是追加式的：既有后端判定不受影响，fly4k 只在显式配置时生效。
func TestFly4kAdaptorRegistersNativeConverter(t *testing.T) {
	var adaptor any = &TaskAdaptor{}
	_, ok := adaptor.(interface {
		ConvertToNativeVideo(*model.Task) ([]byte, error)
	})
	assert.True(t, ok, "fly4k 适配器必须实现方舟原生协议转换接口")
}
