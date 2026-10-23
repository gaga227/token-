package doubao

import (
	"encoding/json"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// TestReferenceVideoCarriesRole 回归用例：参考视频条目必须带 role=reference_video。
//
// 背景：上游（方舟 / Seedance）在请求包含视频输入时进入「参考媒体模式」，
// 视频条目缺少 role 会被直接拒绝：
//
//	400 InvalidParameter: reference media mode requires video role to be reference_video
//
// 修复前 convertToRequestPayload 只写 type/video_url，不带 role，导致客户
// 「首帧图 + 参考视频」的请求 100% 失败（且上游已受理前的计费不受影响）。
func TestReferenceVideoCarriesRole(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:              "doubao-seedance-2-5-260628",
		ReferenceVideoURLs: []string{"https://example.com/ref.mp4", "   "},
	})
	if err != nil {
		t.Fatalf("convertToRequestPayload 失败: %v", err)
	}

	var videos []ContentItem
	for _, item := range payload.Content {
		if item.Type == "video_url" {
			videos = append(videos, item)
		}
	}
	if len(videos) != 1 {
		t.Fatalf("期望 1 个 video_url 条目（空串应被跳过），实际 %d 个", len(videos))
	}
	if videos[0].Role != "reference_video" {
		t.Fatalf("video_url 条目的 role 必须是 reference_video，实际为 %q", videos[0].Role)
	}
	if videos[0].VideoURL == nil || videos[0].VideoURL.URL != "https://example.com/ref.mp4" {
		t.Fatalf("video_url.url 不正确: %+v", videos[0].VideoURL)
	}

	// 序列化后必须真的带 role 字段（omitempty 不会把它吃掉）
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(raw), `"role":"reference_video"`) {
		t.Fatalf("序列化结果缺少 role:reference_video: %s", string(raw))
	}
}

// TestImageOnlyRequestHasNoVideoRole 确保只有首帧图的请求不受影响：
// 不应凭空出现 reference_video 角色。
func TestImageOnlyRequestHasNoVideoRole(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-5-260628",
		Images: []string{"https://example.com/first.png"},
	})
	if err != nil {
		t.Fatalf("convertToRequestPayload 失败: %v", err)
	}
	for _, item := range payload.Content {
		if item.Role == "reference_video" {
			t.Fatalf("纯图生视频请求不应出现 reference_video 角色")
		}
	}
}
