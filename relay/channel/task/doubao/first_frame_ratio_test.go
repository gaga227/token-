package doubao

import (
	"encoding/json"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// 方舟硬规则：首帧/首尾帧图生视频时输出比例强制跟随首帧图，请求显式携带
// ratio 会被上游 400 拒绝（"The parameter ratio specified in the request is
// not valid. For first-frame or first-last-frame generation, the output
// ratio follows the first-frame image"）。本组测试锁定：带首帧图（含尾帧）
// 时 ratio 必须被丢弃，且两种来源（size 派生 / metadata 显式）都要覆盖；
// 纯文生视频与参考图模式不受影响。

func TestFirstFrameDropsDerivedRatio(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-5-260628",
		Prompt: "a cat",
		Images: []string{"https://example.com/first.png"},
		Size:   "1280x720", // size 派生出 ratio "16:9"
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("convertToRequestPayload: %v", err)
	}
	if payload.Ratio != "" {
		t.Fatalf("first-frame request must not carry ratio, got %q", payload.Ratio)
	}
	if payload.Resolution == "" {
		t.Fatalf("resolution must be preserved when dropping ratio")
	}
	raw, _ := json.Marshal(payload)
	if strings.Contains(string(raw), "\"ratio\"") {
		t.Fatalf("serialized payload must not contain ratio field: %s", raw)
	}
}

func TestMetadataRatioDroppedWithFirstFrame(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt:   "a cat",
		Images:   []string{"https://example.com/first.png"},
		Metadata: map[string]interface{}{"ratio": "16:9"},
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("convertToRequestPayload: %v", err)
	}
	if payload.Ratio != "" {
		t.Fatalf("metadata ratio must be dropped for first-frame requests, got %q", payload.Ratio)
	}
}

func TestFirstLastFrameDropsRatio(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt:            "a cat",
		Images:            []string{"https://example.com/first.png"},
		LastFrameImageURL: "https://example.com/last.png",
		Size:              "720x1280",
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("convertToRequestPayload: %v", err)
	}
	if payload.Ratio != "" {
		t.Fatalf("first-last-frame request must not carry ratio, got %q", payload.Ratio)
	}
}

func TestTextToVideoKeepsRatio(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt: "a cat",
		Size:   "1280x720",
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("convertToRequestPayload: %v", err)
	}
	if payload.Ratio == "" {
		t.Fatalf("text-to-video should keep derived ratio")
	}
}

func TestReferenceImageModeKeepsRatio(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt:             "a cat",
		ReferenceImageURLs: []string{"https://example.com/ref.png"},
		Size:               "1280x720",
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("convertToRequestPayload: %v", err)
	}
	if payload.Ratio == "" {
		t.Fatalf("reference-image mode should keep ratio")
	}
}
