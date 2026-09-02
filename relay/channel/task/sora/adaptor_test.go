package sora

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSoraBuildRequestBodyReturnsReplayablePassThroughBody(t *testing.T) {
	payload := []byte("opaque-sora-request-body")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/octet-stream")
	defer common.CleanupBodyStorage(c)

	info := &relaycommon.RelayInfo{}
	body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
	replayable, ok := body.(common.ReplayableBody)
	require.True(t, ok)

	sent, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, payload, sent)
	assert.EqualValues(t, len(payload), replayable.Size())

	replayBody, err := replayable.NewReader()
	require.NoError(t, err)
	replay, err := io.ReadAll(replayBody)
	require.NoError(t, err)
	require.NoError(t, replayBody.Close())
	assert.Equal(t, payload, replay)
}

// estimateWithJSON 以 JSON 风格请求走真实校验链路（ValidateRequestAndSetAction →
// ValidateMultipartDirect 解析 TaskSubmitReq 并 storeTaskRequest），再评估计费。
// 与生产链路一致：客户端直接按 MiniMax/maitoken 文档规则传素材字段。
func estimateWithJSON(t *testing.T, body string) map[string]float64 {
	t.Helper()
	_, r := estimateWithJSONAndInfo(t, body)
	return r
}

// estimateWithJSONAndInfo 同 estimateWithJSON，但额外返回填充了拆分秒数的 info，
// 供断言 EstimateBilling 写入的输入/输出等效秒数（EstimatedInputSeconds/EstimatedOutputSeconds）。
func estimateWithJSONAndInfo(t *testing.T, body string) (*relaycommon.RelayInfo, map[string]float64) {
	t.Helper()
	adaptor := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		OriginModelName: "MiniMax-H3",
		// Action 经嵌入的 *TaskRelayInfo 提升访问，测试需初始化该嵌入（生产链路由框架构造）
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	return info, adaptor.EstimateBilling(c, info)
}

func TestEstimateBillingMiniMaxDocStyleMaterials(t *testing.T) {
	const (
		size768 = "720x1280"
		size2k  = "1440x2560"
	)

	t.Run("纯T2V回归-4s768P", func(t *testing.T) {
		r := estimateWithJSON(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`"}`)
		assert.InDelta(t, 4.0, r["seconds"], 1e-9)
	})

	t.Run("OpenAI多图6张-超额1张", func(t *testing.T) {
		r := estimateWithJSON(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`",
			"images":["https://a/1.png","https://a/2.png","https://a/3.png","https://a/4.png","https://a/5.png","https://a/6.png"]}`)
		assert.InDelta(t, 4.4, r["seconds"], 1e-9)
	})

	t.Run("文档首尾帧+参考图共5张-免费内", func(t *testing.T) {
		r := estimateWithJSON(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`",
			"image":"https://a/first.png","last_frame_image_url":"https://a/last.png",
			"reference_image_urls":["https://a/r1.png","https://a/r2.png","https://a/r3.png"]}`)
		// image(1) + last_frame(1) + 参考图(3) = 5 张 ≤ 免费 5 → 无超额
		assert.InDelta(t, 4.0, r["seconds"], 1e-9)
	})

	t.Run("文档参考图6张-超额1张", func(t *testing.T) {
		r := estimateWithJSON(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`",
			"reference_image_urls":["https://a/1.png","https://a/2.png","https://a/3.png","https://a/4.png","https://a/5.png","https://a/6.png"]}`)
		assert.InDelta(t, 4.4, r["seconds"], 1e-9)
	})

	t.Run("文档视频参考解析失败-fallback输出秒数", func(t *testing.T) {
		r := estimateWithJSON(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`",
			"reference_video_urls":["http://127.0.0.1:1/none.mp4"]}`) // 连接拒绝 → 无法解析时长 → 按输出秒数近似
		// (4 + 4) × 1.0 = 8.0
		assert.InDelta(t, 8.0, r["seconds"], 1e-9)
	})

	t.Run("文档视频参考2K分辨率", func(t *testing.T) {
		r := estimateWithJSON(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size2k+`",
			"reference_video_urls":["http://127.0.0.1:1/none.mp4"]}`)
		// (4 + 4) × 1.6 = 12.8
		assert.InDelta(t, 12.8, r["seconds"], 1e-9)
	})

	t.Run("OpenAI-input_reference视频URL解析失败-fallback", func(t *testing.T) {
		r := estimateWithJSON(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`",
			"input_reference":"http://127.0.0.1:1/none.mp4"}`)
		assert.InDelta(t, 8.0, r["seconds"], 1e-9)
	})
}

// TestEstimateBillingWritesSplitSeconds 验证 EstimateBilling 把输入/输出等效秒数
// 写入 info（供创建时持久化到 TaskBillingContext，查询接口据此拆分输入/输出消耗）。
func TestEstimateBillingWritesSplitSeconds(t *testing.T) {
	const (
		size768 = "720x1280"
		size2k  = "1440x2560"
	)

	t.Run("纯T2V-无输入", func(t *testing.T) {
		info, r := estimateWithJSONAndInfo(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`"}`)
		assert.InDelta(t, 4.0, r["seconds"], 1e-9)
		assert.InDelta(t, 4.0, info.EstimatedOutputSeconds, 1e-9)
		assert.InDelta(t, 0.0, info.EstimatedInputSeconds, 1e-9)
	})

	t.Run("超额图片1张-归输入", func(t *testing.T) {
		info, r := estimateWithJSONAndInfo(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`",
			"reference_image_urls":["https://a/1.png","https://a/2.png","https://a/3.png","https://a/4.png","https://a/5.png","https://a/6.png"]}`)
		// 总 4.4 = 输出 4.0 + 输入 0.4
		assert.InDelta(t, 4.4, r["seconds"], 1e-9)
		assert.InDelta(t, 4.0, info.EstimatedOutputSeconds, 1e-9)
		assert.InDelta(t, 0.4, info.EstimatedInputSeconds, 1e-9)
	})

	t.Run("参考视频fallback-4s输入+输出", func(t *testing.T) {
		info, r := estimateWithJSONAndInfo(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`",
			"reference_video_urls":["http://127.0.0.1:1/none.mp4"]}`)
		// 总 8.0 = 输出 4.0 + 输入 4.0（fallback 近似）
		assert.InDelta(t, 8.0, r["seconds"], 1e-9)
		assert.InDelta(t, 4.0, info.EstimatedOutputSeconds, 1e-9)
		assert.InDelta(t, 4.0, info.EstimatedInputSeconds, 1e-9)
	})

	t.Run("参考视频2K-输入输出同倍率", func(t *testing.T) {
		info, r := estimateWithJSONAndInfo(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size2k+`",
			"reference_video_urls":["http://127.0.0.1:1/none.mp4"]}`)
		// 总 12.8 = 输出 6.4 + 输入 6.4
		assert.InDelta(t, 12.8, r["seconds"], 1e-9)
		assert.InDelta(t, 6.4, info.EstimatedOutputSeconds, 1e-9)
		assert.InDelta(t, 6.4, info.EstimatedInputSeconds, 1e-9)
	})

	t.Run("输出+视频+超额图片混合", func(t *testing.T) {
		info, r := estimateWithJSONAndInfo(t, `{"model":"MiniMax-H3","prompt":"p","seconds":"4","size":"`+size768+`",
			"reference_image_urls":["https://a/1.png","https://a/2.png","https://a/3.png","https://a/4.png","https://a/5.png","https://a/6.png","https://a/7.png"],
			"reference_video_urls":["http://127.0.0.1:1/none.mp4"]}`)
		// 输出 4.0；输入 = 视频 4.0 + 超额图 2×0.4=0.8 → 4.8；总 8.8
		assert.InDelta(t, 8.8, r["seconds"], 1e-9)
		assert.InDelta(t, 4.0, info.EstimatedOutputSeconds, 1e-9)
		assert.InDelta(t, 4.8, info.EstimatedInputSeconds, 1e-9)
	})
}

// TestConvertToOpenAIVideoConsumedFields 验证查询响应注入输入/输出消耗 quota 与金额，
// 且 input + output 恒等于 task.Quota（折扣后总扣费）。
func TestConvertToOpenAIVideoConsumedFields(t *testing.T) {
	adaptor := &TaskAdaptor{}
	const totalQuota = 147492 // 4s × 768P 全价（36873/s），仅用于验证拆分比例

	// 上游轮询原始响应（ConvertToOpenAIVideo 仅替换 id 并注入金额字段）
	rawData := []byte(`{"id":"upstream-001","status":"completed","progress":100}`)

	t.Run("有拆分-输入输出按秒数比例", func(t *testing.T) {
		task := &model.Task{
			TaskID: "task_local_1",
			Quota:  totalQuota,
			Data:   rawData,
			PrivateData: model.TaskPrivateData{
				BillingContext: &model.TaskBillingContext{
					InputSeconds:  4.8, // 视频+超额图
					OutputSeconds: 4.0,
				},
			},
		}
		out, err := adaptor.ConvertToOpenAIVideo(task)
		require.NoError(t, err)
		raw := string(out)
		assert.Contains(t, raw, `"id":"task_local_1"`)
		assert.Contains(t, raw, `"consumed_input_quota"`)
		assert.Contains(t, raw, `"consumed_input_amount"`)
		assert.Contains(t, raw, `"consumed_output_quota"`)
		assert.Contains(t, raw, `"consumed_output_amount"`)

		// 解析回结构体验证数值与守恒
		var parsed struct {
			InputQuota  int `json:"consumed_input_quota"`
			OutputQuota int `json:"consumed_output_quota"`
		}
		require.NoError(t, common.Unmarshal(out, &parsed))
		assert.Equal(t, totalQuota, parsed.InputQuota+parsed.OutputQuota, "input+output 必须等于 task.Quota")
		assert.True(t, parsed.InputQuota > 0 && parsed.OutputQuota > 0, "混合任务输入输出均应有消耗")
	})

	t.Run("纯输出-无输入素材", func(t *testing.T) {
		task := &model.Task{
			TaskID: "task_local_2",
			Quota:  totalQuota,
			Data:   rawData,
			PrivateData: model.TaskPrivateData{
				BillingContext: &model.TaskBillingContext{
					OutputSeconds: 4.0,
				},
			},
		}
		out, err := adaptor.ConvertToOpenAIVideo(task)
		require.NoError(t, err)
		var parsed struct {
			InputQuota  int `json:"consumed_input_quota"`
			OutputQuota int `json:"consumed_output_quota"`
		}
		require.NoError(t, common.Unmarshal(out, &parsed))
		assert.Equal(t, 0, parsed.InputQuota)
		assert.Equal(t, totalQuota, parsed.OutputQuota)
	})

	t.Run("旧任务-无BillingContext拆分", func(t *testing.T) {
		task := &model.Task{
			TaskID: "task_local_3",
			Quota:  totalQuota,
			Data:   rawData,
		}
		out, err := adaptor.ConvertToOpenAIVideo(task)
		require.NoError(t, err)
		var parsed struct {
			InputQuota  int `json:"consumed_input_quota"`
			OutputQuota int `json:"consumed_output_quota"`
		}
		require.NoError(t, common.Unmarshal(out, &parsed))
		// 无拆分信息 → 全部归输出，保证总额可对账
		assert.Equal(t, 0, parsed.InputQuota)
		assert.Equal(t, totalQuota, parsed.OutputQuota)
	})

	t.Run("退款后Quota为0", func(t *testing.T) {
		task := &model.Task{
			TaskID: "task_local_4",
			Quota:  0,
			Data:   rawData,
			PrivateData: model.TaskPrivateData{
				BillingContext: &model.TaskBillingContext{
					InputSeconds:  4.0,
					OutputSeconds: 4.0,
				},
			},
		}
		out, err := adaptor.ConvertToOpenAIVideo(task)
		require.NoError(t, err)
		var parsed struct {
			InputQuota  int `json:"consumed_input_quota"`
			OutputQuota int `json:"consumed_output_quota"`
		}
		require.NoError(t, common.Unmarshal(out, &parsed))
		assert.Equal(t, 0, parsed.InputQuota)
		assert.Equal(t, 0, parsed.OutputQuota)
	})
}
