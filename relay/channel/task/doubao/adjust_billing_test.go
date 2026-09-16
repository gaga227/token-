package doubao

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oinone 信封格式的成功任务响应（HK 实测形态：usage/resolution 藏在 data.data 内层）
const envelopeSuccessBody = `{
  "code": "success",
  "message": "",
  "data": {
    "task_id": "cgt-test",
    "status": "SUCCESS",
    "result_url": "https://ark.example.com/v.mp4?sig=2",
    "data": {
      "id": "cgt-test",
      "status": "succeeded",
      "resolution": "720p",
      "duration": 5,
      "content": {"video_url": "https://ark.example.com/v.mp4?sig=1"},
      "usage": {"completion_tokens": 108900, "total_tokens": 108900}
    }
  }
}`

// 豆包原生格式的成功任务响应
const nativeSuccessBody = `{
  "id": "cgt-test",
  "status": "succeeded",
  "resolution": "720p",
  "duration": 5,
  "usage": {"completion_tokens": 108900, "total_tokens": 108900}
}`

func billingTask(data []byte, quota int, bc *model.TaskBillingContext) *model.Task {
	task := &model.Task{
		Status: model.TaskStatusSuccess,
		Quota:  quota,
		Data:   data,
	}
	task.PrivateData.BillingContext = bc
	return task
}

func TestAdjustBillingOnCompleteBillsByActualTokens(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// 预扣 = modelRatio/2 × QuotaPerUnit = 846833（固定 250,000 token 估算）
	task := billingTask([]byte(envelopeSuccessBody), 846833, &model.TaskBillingContext{
		OriginModelName: "doubao-seedance-2-0",
		OtherRatios:     map[string]float64{"req_res_720p": 1.0},
	})
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	// 实际 108,900 token（720p 基准档）：846833 × 108900/250000 = 368880.45 → 368880
	require.Equal(t, 368880, quota)
}

func TestAdjustBillingOnCompleteCorrectsResolutionFallback(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// 请求 1080p（档位倍率 51/46），上游回退按 720p 出片
	task := billingTask([]byte(envelopeSuccessBody), 100000, &model.TaskBillingContext{
		OriginModelName: "doubao-seedance-2-0",
		OtherRatios:     map[string]float64{"video_input": 51.0 / 46.0, "req_res_1080p": 1.0},
	})
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	// 按实际 720p 基准档重算：100000 × (108900 × 1.0)/(250000 × 51/46) = 39289.41 → 39289
	require.Equal(t, 39289, quota)
}

func TestAdjustBillingOnCompleteKeepsVideoInputTier(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// 含视频输入（档位倍率 28/46），上游回显分辨率与请求一致 → 倍率不变，仅按 token 结算
	task := billingTask([]byte(nativeSuccessBody), 500000, &model.TaskBillingContext{
		OriginModelName: "doubao-seedance-2-0",
		OtherRatios:     map[string]float64{"video_input": 28.0 / 46.0, "req_has_video": 1.0, "req_res_720p": 1.0},
	})
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	// 档位倍率两侧相消：500000 × 108900/250000 = 217800
	require.Equal(t, 217800, quota)
}

func TestAdjustBillingOnCompleteFallbacksToPreConsumed(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	// 无 usage
	noUsage := billingTask([]byte(`{"id":"cgt","status":"succeeded","resolution":"720p"}`), 846833,
		&model.TaskBillingContext{OriginModelName: "doubao-seedance-2-0"})
	assert.Equal(t, 0, adaptor.AdjustBillingOnComplete(noUsage, result))

	// 非成功终态
	failed := billingTask([]byte(nativeSuccessBody), 846833,
		&model.TaskBillingContext{OriginModelName: "doubao-seedance-2-0"})
	assert.Equal(t, 0, adaptor.AdjustBillingOnComplete(failed, &relaycommon.TaskInfo{Status: model.TaskStatusFailure}))

	// 无 BillingContext
	raw := &model.Task{Status: model.TaskStatusSuccess, Quota: 846833, Data: []byte(nativeSuccessBody)}
	assert.Equal(t, 0, adaptor.AdjustBillingOnComplete(raw, result))

	// 按次计费标记
	perCall := billingTask([]byte(nativeSuccessBody), 846833, &model.TaskBillingContext{
		OriginModelName: "doubao-seedance-2-0",
		PerCallBilling:  true,
	})
	assert.Equal(t, 0, adaptor.AdjustBillingOnComplete(perCall, result))
}

func TestAdjustBillingOnCompleteUnconfiguredModelBillsByTokens(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// 不在 videoPriceTable 的模型：无档位倍率，纯按实际 token 结算
	task := billingTask([]byte(nativeSuccessBody), 846833, &model.TaskBillingContext{
		OriginModelName: "doubao-seedance-1-0-pro-250528",
	})
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	require.Equal(t, 368880, quota)
}

func estimateBillingRatios(t *testing.T, body string) map[string]float64 {
	t.Helper()
	var metadata map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(body), &metadata))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", nil)
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    "doubao-seedance-2-0",
		Metadata: metadata,
	})
	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{OriginModelName: "doubao-seedance-2-0"}
	return adaptor.EstimateBilling(c, info)
}

func TestEstimateBillingCarriesSubmitContext(t *testing.T) {
	// 1080p + 视频输入：倍率 51/46... 视频输入+1080p = 31/46
	ratios := estimateBillingRatios(t, `{"resolution":"1080p","content":[{"type":"video_url","video_url":"https://e.com/v.mp4"}]}`)
	assert.Equal(t, 31.0/46.0, ratios["video_input"])
	assert.Equal(t, 1.0, ratios["req_has_video"])
	assert.Equal(t, 1.0, ratios["req_res_1080p"])

	// 720p 纯文生视频：基准档倍率 1.0，但仍携带上下文标记
	ratios = estimateBillingRatios(t, `{"resolution":"720p","content":[{"type":"text","text":"hi"}]}`)
	assert.Equal(t, 1.0, ratios["video_input"])
	assert.NotContains(t, ratios, "req_has_video")
	assert.Equal(t, 1.0, ratios["req_res_720p"])
}
