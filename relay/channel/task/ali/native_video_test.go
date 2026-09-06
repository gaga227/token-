package ali

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 百炼原生入口 ConvertToNativeVideo：成功任务的 data 原样透出 + task_id/status 校正
func TestConvertToNativeVideoSuccess(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public123",
		Status: model.TaskStatusSuccess,
		Data: []byte(`{
			"output": {
				"task_id": "upstream-uuid-xxx",
				"task_status": "SUCCEEDED",
				"video_url": "https://oss.example.com/video.mp4",
				"submit_time": "2026-09-05 10:00:00",
				"end_time": "2026-09-05 10:02:30"
			},
			"usage": {"duration": 5, "SR": 720, "output_video_duration": 5.0},
			"request_id": "req-abc"
		}`),
	}

	adaptor := &TaskAdaptor{}
	body, err := adaptor.ConvertToNativeVideo(task)
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(body, &resp))

	output, ok := resp["output"].(map[string]any)
	require.True(t, ok)
	// 公开 task id 覆盖上游 id
	assert.Equal(t, "task_public123", output["task_id"])
	assert.Equal(t, "SUCCEEDED", output["task_status"])
	assert.Equal(t, "https://oss.example.com/video.mp4", output["video_url"])

	usage, ok := resp["usage"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(720), usage["SR"])

	assert.Equal(t, "req-abc", resp["request_id"])
}

// 客户端持上游原始 id 查询时回显原始 id
func TestConvertToNativeVideoUpstreamTaskID(t *testing.T) {
	task := &model.Task{
		TaskID:               "task_public123",
		ReturnUpstreamTaskID: true,
		Status:               model.TaskStatusInProgress,
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "upstream-uuid-xxx",
		},
		Data: []byte(`{"output":{"task_id":"upstream-uuid-xxx","task_status":"RUNNING"},"request_id":"req-abc"}`),
	}

	adaptor := &TaskAdaptor{}
	body, err := adaptor.ConvertToNativeVideo(task)
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(body, &resp))
	output := resp["output"].(map[string]any)
	assert.Equal(t, "upstream-uuid-xxx", output["task_id"])
	assert.Equal(t, "RUNNING", output["task_status"])
}

// 失败任务：data 无错误信息时用 FailReason 补充 output.code/message
func TestConvertToNativeVideoFailure(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_public123",
		Status:     model.TaskStatusFailure,
		FailReason: "upstream_exhausted",
		Data:       []byte(`{"output":{"task_id":"upstream-uuid-xxx","task_status":"FAILED"}}`),
	}

	adaptor := &TaskAdaptor{}
	body, err := adaptor.ConvertToNativeVideo(task)
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(body, &resp))
	output := resp["output"].(map[string]any)
	assert.Equal(t, "FAILED", output["task_status"])
	assert.Equal(t, "InternalError", output["code"])
	assert.Equal(t, "upstream_exhausted", output["message"])
}

// 空任务（尚未轮询到上游数据）：状态兜底 PENDING
func TestConvertToNativeVideoEmptyData(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public123",
		Status: model.TaskStatusSubmitted,
	}

	adaptor := &TaskAdaptor{}
	body, err := adaptor.ConvertToNativeVideo(task)
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(body, &resp))
	output := resp["output"].(map[string]any)
	assert.Equal(t, "task_public123", output["task_id"])
	assert.Equal(t, "PENDING", output["task_status"])
	// request_id 兜底为公开 task id
	assert.Equal(t, "task_public123", resp["request_id"])
}
