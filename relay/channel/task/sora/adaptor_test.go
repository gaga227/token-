package sora

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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
	return adaptor.EstimateBilling(c, info)
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
