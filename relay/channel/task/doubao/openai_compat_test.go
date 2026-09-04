package doubao

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestConvertToRequestPayloadOpenAISizeFallback(t *testing.T) {
	cases := []struct {
		name           string
		size           string
		wantResolution string
		wantRatio      string
	}{
		{"landscape 720p", "1280x720", "720p", "16:9"},
		{"portrait 720p", "720x1280", "720p", "9:16"},
		{"landscape 480p", "854x480", "480p", "16:9"},
		{"square", "1024x1024", "1080p", "1:1"},
		{"ratio passthrough", "16:9", "", "16:9"},
		{"resolution passthrough", "1080p", "1080p", ""},
		{"garbage", "abc", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
				Model:  "doubao-seedance-2-0-260128",
				Prompt: "a cat",
				Size:   tc.size,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantResolution, payload.Resolution)
			assert.Equal(t, tc.wantRatio, payload.Ratio)
		})
	}
}

func TestConvertToRequestPayloadMetadataWinsOverSize(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:    "doubao-seedance-2-0-260128",
		Prompt:   "a cat",
		Size:     "1280x720",
		Duration: 5,
		Metadata: map[string]any{
			"resolution": "1080p",
			"ratio":      "21:9",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "1080p", payload.Resolution)
	assert.Equal(t, "21:9", payload.Ratio)
	// duration 兜底：metadata 未带 duration 时取 OpenAI 风格 duration 字段
	require.NotNil(t, payload.Duration)
	assert.Equal(t, dto.IntValue(5), *payload.Duration)
}

func TestConvertToRequestPayloadOpenAIMediaFields(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:              "doubao-seedance-2-0-260128",
		Prompt:             "a cat",
		Images:             []string{"https://e.com/first.jpg"},
		LastFrameImageURL:  "https://e.com/last.jpg",
		ReferenceImageURLs: []string{"https://e.com/ref.jpg"},
		ReferenceVideoURLs: []string{"https://e.com/ref.mp4", " "},
		ReferenceAudioURLs: []string{"https://e.com/ref.mp3"},
	})
	require.NoError(t, err)

	// 顺序：首帧 → 尾帧 → 参考图 → 参考视频（空串剔除）→ 参考音频 → text
	types := make([]string, 0, len(payload.Content))
	for _, item := range payload.Content {
		types = append(types, item.Type)
	}
	assert.Equal(t, []string{
		"image_url", "image_url", "image_url", "video_url", "audio_url", "text",
	}, types)
	assert.Equal(t, "https://e.com/ref.mp4", payload.Content[3].VideoURL.URL)
	assert.Equal(t, "a cat", payload.Content[5].Text)
}

func TestEstimateBillingOpenAIVideoReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// OpenAI 风格 reference_video_urls 应触发 v2v 档计费倍率（基准分辨率 28/46）
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Model:              "doubao-seedance-2-0-260128",
		Prompt:             "a cat",
		ReferenceVideoURLs: []string{"https://e.com/ref.mp4"},
	})
	info := &relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2-0-260128",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)
	require.NotNil(t, ratios)
	assert.InDelta(t, 28.0/46.0, ratios["video_input"], 1e-9)
}
