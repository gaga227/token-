package doubao

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
)

// 部分上游（如 oinone 等 new-api 网关）的任务查询响应是通用信封格式：
//
//	{"code":"success","data":{ ...任务记录DTO..., "data":{ ...上游原始任务响应(含 content/usage/resolution...)... } }}
//
// 直接反序列化到 responseTask 时，内层字段（usage/resolution/duration/content 等）全部丢失。
// unwrapEnvelopeTask 负责定位内层任务 DTO；hydrateFromEnvelope 用它补齐外层解析不到的空字段。

func unwrapEnvelopeTask(data []byte) (json.RawMessage, bool) {
	var root map[string]json.RawMessage
	if err := common.Unmarshal(data, &root); err != nil {
		return nil, false
	}
	outerRaw, ok := root["data"]
	if !ok {
		return nil, false
	}
	var outer map[string]json.RawMessage
	if err := common.Unmarshal(outerRaw, &outer); err != nil {
		return nil, false
	}
	// 标准信封：data.data 为上游原始任务响应
	if nestedRaw, ok := outer["data"]; ok {
		var nested map[string]json.RawMessage
		if err := common.Unmarshal(nestedRaw, &nested); err == nil {
			if _, hasStatus := nested["status"]; hasStatus {
				return nestedRaw, true
			}
		}
	}
	// 退化信封：data 本身就是任务 DTO
	if _, hasStatus := outer["status"]; hasStatus {
		return outerRaw, true
	}
	return nil, false
}

// hydrateFromEnvelope 在直接反序列化拿不到关键回显字段时，从信封内层任务 DTO
// 补齐空字段（不覆盖已解析到的值），保证查询响应能透出上游的 usage/规格回显。
func hydrateFromEnvelope(data []byte, response *responseTask) {
	if response == nil || len(data) == 0 {
		return
	}
	if response.Usage != nil && response.Resolution != "" && response.Duration != nil &&
		response.Content != nil && response.Content.VideoURL != "" {
		return
	}
	inner, ok := unwrapEnvelopeTask(data)
	if !ok {
		return
	}
	var env responseTask
	if err := common.Unmarshal(inner, &env); err != nil {
		return
	}
	if response.Content == nil {
		response.Content = env.Content
	} else if env.Content != nil {
		if response.Content.VideoURL == "" {
			response.Content.VideoURL = env.Content.VideoURL
		}
		if response.Content.LastFrameURL == "" {
			response.Content.LastFrameURL = env.Content.LastFrameURL
		}
	}
	if response.Usage == nil {
		response.Usage = env.Usage
	}
	if response.Resolution == "" {
		response.Resolution = env.Resolution
	}
	if response.Duration == nil {
		response.Duration = env.Duration
	}
	if response.Ratio == "" {
		response.Ratio = env.Ratio
	}
	if response.Seed == nil {
		response.Seed = env.Seed
	}
	if response.GenerateAudio == nil {
		response.GenerateAudio = env.GenerateAudio
	}
}
