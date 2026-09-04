package ali

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// maitoken 实际返回的成功任务响应（usage.duration 为浮点数）
const maitokenSuccessBody = `{
  "request_id": "438b942d-4a18-944f-9ed3-cfefb337984c",
  "output": {
    "task_id": "aa457787-31ff-41dc-a7da-eec21439787a",
    "task_status": "SUCCEEDED",
    "video_url": "https://example.com/video.mp4"
  },
  "usage": {
    "video_count": 1,
    "duration": 5.0,
    "SR": 720,
    "output_video_duration": 5.0,
    "input_video_duration": 0.0,
    "fps": 30,
    "ratio": "16:9"
  }
}`

func billingTask(modelName string, quota int, otherRatios map[string]float64) *model.Task {
	task := &model.Task{
		Status: model.TaskStatusInProgress,
		Quota:  quota,
		Data:   []byte(maitokenSuccessBody),
	}
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		OriginModelName: modelName,
		OtherRatios:     otherRatios,
	}
	return task
}

func TestAdjustBillingOnCompleteStandardModelKeepsExactQuota(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// 预扣：5 秒 @720P（resolution-720P=2）
	task := billingTask("wan3.0-video", 55220, map[string]float64{
		"seconds":        5,
		"resolution-720P": 2,
	})
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	// 实际输出 5 秒 @720P(SR=720) 与预估一致 → 额度不变
	require.Equal(t, 55220, quota)
}

func TestAdjustBillingOnCompleteSmartDurationRefundsOvercharge(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// 智能时长：按 30 秒预扣，实际输出 5 秒
	task := billingTask("wan3.0-video", 331320, map[string]float64{
		"seconds":        30,
		"resolution-720P": 2,
	})
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	// 331320 × (5×2)/(30×2) = 55220
	require.Equal(t, 55220, quota)
}

func TestAdjustBillingOnCompletePrimeBillsByInputDuration(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// prime：预扣按输出 seconds=5，上游按输入视频 10 秒计费
	body := `{
	  "output": {"task_id": "t1", "task_status": "SUCCEEDED", "video_url": "https://example.com/v.mp4"},
	  "usage": {"video_count": 1, "duration": 5.0, "SR": 720,
	            "output_video_duration": 5.0, "input_video_duration": 10.0}
	}`
	task := billingTask("wan3.0-video-prime", 82830, map[string]float64{
		"seconds":        5,
		"resolution-720P": 2,
	})
	task.Data = []byte(body)
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	// 82830 × (10×2)/(5×2) = 165660 —— 按输入时长补扣
	require.Equal(t, 165660, quota)
}

func TestAdjustBillingOnCompletePrimeFallbackToOutputWhenNoInput(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// prime 纯文生：无输入视频，按输出时长
	task := billingTask("wan3.0-video-prime", 82830, map[string]float64{
		"seconds":        5,
		"resolution-720P": 2,
	})
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	require.Equal(t, 82830, quota)
}

func TestAdjustBillingOnCompleteCorrectsResolution(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// 预估 720P，上游实际输出 480P（SR=480）
	body := `{
	  "output": {"task_id": "t1", "task_status": "SUCCEEDED", "video_url": "https://example.com/v.mp4"},
	  "usage": {"duration": 5.0, "SR": 480, "output_video_duration": 5.0, "input_video_duration": 0.0}
	}`
	task := billingTask("wan3.0-video", 55220, map[string]float64{
		"seconds":        5,
		"resolution-720P": 2,
	})
	task.Data = []byte(body)
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	// 55220 × (5×1)/(5×2) = 27610
	require.Equal(t, 27610, quota)
}

func TestAdjustBillingOnCompleteAliNativeStringUsage(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// 阿里原生：duration 为字符串
	body := `{
	  "output": {"task_id": "t1", "task_status": "SUCCEEDED", "video_url": "https://example.com/v.mp4"},
	  "usage": {"duration": "5", "video_count": 1}
	}`
	task := billingTask("wan2.5-t2v-preview", 100000, map[string]float64{
		"seconds":        5,
		"resolution-1080P": 1 / 0.3,
	})
	task.Data = []byte(body)
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	// 无 SR/输出时长 → 按字符串 duration 解析为 5 秒，分辨率维持预估 → 不变
	require.Equal(t, 100000, quota)
}

func TestAdjustBillingOnCompleteSkipsInvalidCases(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	// 失败任务不结算
	failedTask := billingTask("wan3.0-video", 55220, map[string]float64{"seconds": 5})
	require.Equal(t, 0, adaptor.AdjustBillingOnComplete(failedTask,
		&relaycommon.TaskInfo{Status: model.TaskStatusFailure}))

	// 按次计费跳过
	perCallTask := billingTask("wan3.0-video", 55220, map[string]float64{"seconds": 5})
	perCallTask.PrivateData.BillingContext.PerCallBilling = true
	require.Equal(t, 0, adaptor.AdjustBillingOnComplete(perCallTask, result))

	// 无 BillingContext
	noBcTask := billingTask("wan3.0-video", 55220, map[string]float64{"seconds": 5})
	noBcTask.PrivateData.BillingContext = nil
	require.Equal(t, 0, adaptor.AdjustBillingOnComplete(noBcTask, result))

	// 无秒数倍率
	noSecTask := billingTask("wan3.0-video", 55220, nil)
	require.Equal(t, 0, adaptor.AdjustBillingOnComplete(noSecTask, result))

	// task.Data 无 usage
	noUsageTask := billingTask("wan3.0-video", 55220, map[string]float64{"seconds": 5})
	noUsageTask.Data = []byte(`{"output": {"task_id": "t1", "task_status": "SUCCEEDED"}}`)
	require.Equal(t, 0, adaptor.AdjustBillingOnComplete(noUsageTask, result))
}

func TestAdjustBillingOnCompleteFractionalDuration(t *testing.T) {
	adaptor := &TaskAdaptor{}
	// 非整数时长 4.5 秒
	body := `{
	  "output": {"task_id": "t1", "task_status": "SUCCEEDED", "video_url": "https://example.com/v.mp4"},
	  "usage": {"duration": 4.5, "SR": 720, "output_video_duration": 4.5}
	}`
	task := billingTask("wan3.0-video", 55220, map[string]float64{
		"seconds":        5,
		"resolution-720P": 2,
	})
	task.Data = []byte(body)
	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	quota := adaptor.AdjustBillingOnComplete(task, result)

	// 55220 × (4.5×2)/(5×2) = 49698
	require.Equal(t, 49698, quota)
}

// 回归：maitoken 浮点 usage 必须能完整解析（曾因 IntValue 不认 5.0 导致任务卡死）
func TestAliUsageParsesMaitokenFloatUsage(t *testing.T) {
	var resp AliVideoResponse
	err := common.Unmarshal([]byte(maitokenSuccessBody), &resp)

	require.NoError(t, err)
	require.NotNil(t, resp.Usage)
	require.Equal(t, 5, int(resp.Usage.Duration))
	require.Equal(t, 720, int(resp.Usage.SR))
	require.InDelta(t, 5.0, resp.Usage.OutputVideoDuration.Float64(), 1e-9)
	require.InDelta(t, 0.0, resp.Usage.InputVideoDuration.Float64(), 1e-9)
}
