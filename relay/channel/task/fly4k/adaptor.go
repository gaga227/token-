package fly4k

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/doubao"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// ============================
// 蝶变（fly4k）任务适配器
// 协议要点（2026-09-28 官方文档）：
//   - 提交：POST /api/v1/b/generate，信封 {code:"0", msg, data:{taskKey, creditCost}}
//   - 轮询：POST /api/v1/task/query，body {taskKey}，data:{status, errorDesc, outputUrl}
//   - 余额：POST /api/v1/balance/query → data.balanceCredits（水晶）
//   - 上游模型写死（文档无 model 参数），网关仅用模型名做路由与计费
// ============================

type flyContent struct {
	Type     string    `json:"type"` // text | image_url | video_url | audio_url
	Text     string    `json:"text,omitempty"`
	ImageURL *flyMedia `json:"image_url,omitempty"`
	VideoURL *flyMedia `json:"video_url,omitempty"`
	AudioURL *flyMedia `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"` // first_frame | last_frame | reference_image | reference_video | reference_audio
}

type flyMedia struct {
	URL string `json:"url"`
}

type submitRequest struct {
	Content         []flyContent `json:"content"`
	Duration        int          `json:"duration"`
	Ratio           string       `json:"ratio"`
	Seed            *int64       `json:"seed,omitempty"`
	GenerateAudio   *bool        `json:"generate_audio,omitempty"`
	Watermark       *bool        `json:"watermark,omitempty"`
	ReturnLastFrame *bool        `json:"return_last_frame,omitempty"`
	Remark          string       `json:"remark,omitempty"`
	// ⚠️ camera_fixed 不透传（2026-09-28 真实上游实测）：
	// model doubao-seedance-2-0 在 t2v 与 i2v 两种模式下均返回
	// InvalidParameter: the specified parameter camera_fixed is not supported ... must be empty，
	// 传了会直接导致任务创建失败，故网关侧忽略该参数。
}

// envelope 蝶变统一响应信封（提交/轮询共用）。
type envelope struct {
	Code string     `json:"code"`
	Msg  string     `json:"msg"`
	Data envelopeDd `json:"data"`
}

type envelopeDd struct {
	TaskKey      string `json:"taskKey"`
	Status       string `json:"status"`
	ErrorDesc    string `json:"errorDesc"`
	OutputUrl    string `json:"outputUrl"`
	LastFrameUrl string `json:"lastFrameUrl,omitempty"`
	// 提交响应独有
	CreditCost *int `json:"creditCost,omitempty"`
	// 尾帧字段名文档未写明，兼容多候选（实测后收敛）
	LastFrameUrlAlt string `json:"last_frame_url,omitempty"`
	OutputLastFrame string `json:"outputLastFrameUrl,omitempty"`
	BalanceCredit   string `json:"balanceCredits,omitempty"`
}

type TaskAdaptor struct {
	apiKey  string
	baseURL string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction 校验 OpenAI 风格视频请求并设置 action。
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	if err := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); err != nil {
		return err
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return &taskdto.TaskError{
			Code:       "prompt_required",
			Message:    "prompt is required by fly4k upstream",
			StatusCode: http.StatusBadRequest,
			LocalError: true,
		}
	}
	if _, rErr := buildSubmitRequest(c, &req); rErr != nil {
		return &taskdto.TaskError{
			Code:       "invalid_request",
			Message:    rErr.Error(),
			StatusCode: http.StatusBadRequest,
			LocalError: true,
		}
	}
	return nil
}

// buildSubmitRequest 将统一请求转换为蝶变提交体（提交与计费共用，保证口径一致）。
// 需要 c 是为了识别请求壳：方舟原生壳的素材在 metadata.content[]，OpenAI 壳在顶层字段。
func buildSubmitRequest(c *gin.Context, req *relaycommon.TaskSubmitReq) (*submitRequest, error) {
	r := &submitRequest{Content: []flyContent{}}

	// 时长：秒，4~15
	dur := req.Duration
	if dur <= 0 {
		dur, _ = strconv.Atoi(req.Seconds)
	}
	if dur <= 0 {
		dur, _ = req.Metadata["duration"].(int)
		if dur <= 0 {
			if f, ok := req.Metadata["duration"].(float64); ok {
				dur = int(f)
			}
		}
	}
	if dur < durationMinSec {
		dur = durationMinSec
	}
	if dur > durationMaxSec {
		dur = durationMaxSec
	}
	r.Duration = dur

	// 宽高比：metadata.ratio 优先，其次 OpenAI 风格 size（"16:9" 直用 / WxH 归一化），兜底 16:9
	ratio, _ := req.Metadata["ratio"].(string)
	ratio = strings.TrimSpace(ratio)
	if ratio == "" {
		if _, pr := deriveRatio(req.Size); pr != "" {
			ratio = pr
		}
	}
	if ratio == "" {
		ratio = "16:9"
	}
	if !supportedRatios[ratio] {
		return nil, fmt.Errorf("ratio %q is not supported: one of 16:9, 4:3, 1:1, 3:4, 9:16, 21:9", ratio)
	}
	r.Ratio = ratio

	// 可选布尔/数值参数（metadata 透传）
	boolMeta := func(key string) *bool {
		v, ok := req.Metadata[key].(bool)
		if !ok {
			return nil
		}
		return &v
	}
	r.GenerateAudio = boolMeta("generate_audio")
	r.Watermark = boolMeta("watermark")
	r.ReturnLastFrame = boolMeta("return_last_frame")
	if seed, ok := req.Metadata["seed"].(float64); ok {
		s := int64(seed)
		r.Seed = &s
	}

	// 素材 → content[]：首帧 → 尾帧 → 参考图 → 参考视频 → 参考音频 → 文本
	//
	// 两种请求壳共用这一套归一逻辑：
	//   · 方舟（Seedance）原生壳 /api/v3/contents/generations/tasks：
	//     素材全部在 metadata.content[] 里，靠 role 区分（未指定 role 的首个 image_url 视为首帧）
	//   · OpenAI 壳 /v1/videos 与 /pg/videos：
	//     image / images / last_frame_image_url / reference_*_urls
	//
	// ⚠️ 素材必须全局去重（2026-09-28 真实上游实测）：
	// relaycommon 会把单图请求的 image 归一化写入 Images（relay_utils.go），
	// 若两个字段各取一次，同一 URL 会同时以 first_frame 与 reference_image 出现，
	// 上游直接拒绝：first/last frame content cannot be mixed with reference media content。
	seen := make(map[string]bool)
	firstFrames := make([]string, 0, 2)
	lastFrame := ""
	referenceImages := make([]string, 0, 4)
	referenceVideos := make([]string, 0, 4)
	referenceAudios := make([]string, 0, 4)

	collect := func(kind, rawURL, role string) {
		url := strings.TrimSpace(rawURL)
		if url == "" {
			return
		}
		key := kind + "\x00" + url
		if seen[key] {
			return
		}
		seen[key] = true
		switch kind {
		case "image":
			switch role {
			case "first_frame":
				firstFrames = append(firstFrames, url)
			case "last_frame":
				// 上游仅支持一个尾帧，多出的降级为参考图
				if lastFrame == "" {
					lastFrame = url
				} else {
					referenceImages = append(referenceImages, url)
				}
			case "reference_image":
				referenceImages = append(referenceImages, url)
			default:
				// 未指定/未知 role：首个 image_url 按方舟语义作首帧，其余降级参考图
				if len(firstFrames) == 0 {
					firstFrames = append(firstFrames, url)
				} else {
					referenceImages = append(referenceImages, url)
				}
			}
		case "video":
			referenceVideos = append(referenceVideos, url)
		case "audio":
			referenceAudios = append(referenceAudios, url)
		}
	}

	// ① 方舟原生壳：metadata.content[]（仅原生壳下解析，避免误读 OpenAI 壳里的同名 metadata）
	if isNativeDoubaoVideoRequest(c) {
		for _, item := range nativeShellContents(req.Metadata) {
			switch item.Type {
			case "image_url":
				if item.ImageURL != nil {
					collect("image", item.ImageURL.URL, strings.ToLower(strings.TrimSpace(item.Role)))
				}
			case "video_url":
				if item.VideoURL != nil {
					collect("video", item.VideoURL.URL, "")
				}
			case "audio_url":
				if item.AudioURL != nil {
					collect("audio", item.AudioURL.URL, "")
				}
			}
		}
	}

	// ② OpenAI 壳字段
	collect("image", req.Image, "")
	for _, u := range req.Images {
		collect("image", u, "")
	}
	collect("image", req.LastFrameImageURL, "last_frame")
	for _, u := range req.ReferenceImageURLs {
		collect("image", u, "reference_image")
	}
	for _, u := range req.ReferenceVideoURLs {
		collect("video", u, "")
	}
	for _, u := range req.ReferenceAudioURLs {
		collect("audio", u, "")
	}

	for _, url := range firstFrames {
		r.Content = append(r.Content, flyContent{Type: "image_url", ImageURL: &flyMedia{URL: url}, Role: "first_frame"})
	}
	if lastFrame != "" {
		r.Content = append(r.Content, flyContent{Type: "image_url", ImageURL: &flyMedia{URL: lastFrame}, Role: "last_frame"})
	}
	for _, url := range referenceImages {
		r.Content = append(r.Content, flyContent{Type: "image_url", ImageURL: &flyMedia{URL: url}, Role: "reference_image"})
	}
	for _, url := range referenceVideos {
		r.Content = append(r.Content, flyContent{Type: "video_url", VideoURL: &flyMedia{URL: url}, Role: "reference_video"})
	}
	for _, url := range referenceAudios {
		r.Content = append(r.Content, flyContent{Type: "audio_url", AudioURL: &flyMedia{URL: url}, Role: "reference_audio"})
	}
	r.Content = append(r.Content, flyContent{Type: "text", Text: req.Prompt})

	return r, nil
}

// nativeShellRequestContextKey 由方舟原生壳中间件（middleware.DoubaoVideoRequestConvert）
// 设置的响应格式标记决定：只有该壳下 metadata 才承载整份原生 body。
func isNativeDoubaoVideoRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	return common.GetContextKeyString(c, constant.ContextKeyTaskResponseFormat) == constant.TaskResponseFormatDoubaoVideo
}

// nativeShellContents 反序列化方舟原生壳塞进 metadata 的 content[] 数组。
// 复用 flyContent 结构（字段名与方舟 content 条目一致），仅读取所需字段。
func nativeShellContents(metadata map[string]any) []flyContent {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["content"]
	if !ok || raw == nil {
		return nil
	}
	data, err := common.Marshal(raw)
	if err != nil {
		return nil
	}
	var items []flyContent
	if err := common.Unmarshal(data, &items); err != nil {
		return nil
	}
	return items
}

// deriveRatio 从 OpenAI 风格 size 推导宽高比（"16:9" 直用；WxH 归一化到最近合法比值）。
func deriveRatio(size string) (resolution, ratio string) {
	size = strings.ToLower(strings.TrimSpace(size))
	if size == "" {
		return "", ""
	}
	if strings.Contains(size, ":") {
		return "", size
	}
	sep := strings.IndexAny(size, "x*")
	if sep <= 0 {
		return "", ""
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(size[:sep]))
	h, err2 := strconv.Atoi(strings.TrimSpace(size[sep+1:]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return "", ""
	}
	return "", nearestRatio(float64(w) / float64(h))
}

func nearestRatio(quotient float64) string {
	entries := []struct {
		value string
		q     float64
	}{
		{"16:9", 16.0 / 9.0},
		{"9:16", 9.0 / 16.0},
		{"4:3", 4.0 / 3.0},
		{"3:4", 3.0 / 4.0},
		{"1:1", 1.0},
		{"21:9", 21.0 / 9.0},
	}
	const tolerance = 0.02
	best, bestDiff := "", tolerance
	for _, e := range entries {
		diff := quotient - e.q
		if diff < 0 {
			diff = -diff
		}
		if diff <= bestDiff {
			best, bestDiff = e.value, diff
		}
	}
	return best
}

// BuildRequestURL 提交端点。
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return strings.TrimRight(a.baseURL, "/") + "/api/v1/b/generate", nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// BuildRequestBody 生成蝶变提交体（上游模型写死，不透传 model）。
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	body, err := buildSubmitRequest(c, &req)
	if err != nil {
		return nil, err
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	data, err = rewriteAssetLibraryReferences(info, data)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// assetLibraryURIPrefix 网关内部素材引用前缀（与 middleware.AssetLibraryRouting 一致）。
const assetLibraryURIPrefix = "asset://asset-na-"

// rewriteAssetLibraryReferences 把报文里的本地素材引用（asset://asset-na-<32hex>）
// 映射成该渠道上游的真实素材引用（asset://asset-xxx）。
//
// 只在报文确实含素材 URI 时才解码重写，普通请求保持字节级不变；原始上游
// asset URI 早已被 AssetLibraryRouting 中间件拒绝，因此此处只处理本地引用。
// 素材库未在该渠道启用时，映射查不到副本会返回明确错误（而不是把内部 ID
// 原样透传给上游导致「素材不存在」）。
func rewriteAssetLibraryReferences(info *relaycommon.RelayInfo, payload []byte) ([]byte, error) {
	if info == nil || !bytes.Contains(payload, []byte(assetLibraryURIPrefix)) {
		return payload, nil
	}
	var decoded map[string]any
	if err := common.Unmarshal(payload, &decoded); err != nil {
		return nil, err
	}
	rewritten, err := service.RewriteAssetReferences(info.UserId, info.ChannelId, decoded)
	if err != nil {
		return nil, errors.Wrap(err, "rewrite asset references failed")
	}
	return common.Marshal(rewritten)
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse 解析信封，返回 taskKey 作为上游任务 ID。
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	var env envelope
	if err := common.Unmarshal(responseBody, &env); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}
	if env.Code != "0" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("fly4k %s: %s", env.Code, env.Msg), "upstream_submit_failed", http.StatusBadRequest)
		return
	}
	if env.Data.TaskKey == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("taskKey is empty, body: %s", responseBody), "invalid_response", http.StatusInternalServerError)
		return
	}

	publicID := info.PublicTaskID
	if info.ShouldReturnUpstreamTaskID(env.Data.TaskKey) {
		publicID = env.Data.TaskKey
	}
	// 方舟原生壳：官方客户端只解 {id}，与 doubao/seedance_sls 保持同一响应契约
	if isNativeDoubaoVideoRequest(c) {
		c.JSON(http.StatusOK, map[string]any{"id": publicID})
		return env.Data.TaskKey, responseBody, nil
	}
	ov := dto.NewOpenAIVideo()
	ov.ID = publicID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = common.GetTimestamp()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return env.Data.TaskKey, responseBody, nil
}

// EstimateBilling 计费（官方标准价，与豆包渠道同表）：
//   - video_input：参考视频档位倍率（复用豆包官方价目表，基准档=480p/720p 无视频参考）
//   - duration_factor：时长因子 = tokensPerSecond × 秒数 / 250,000（基准预扣 token 数）
//
// 上游无 usage 回显且分辨率固定，预扣即最终实扣；失败任务由通用退款机制全额退还。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	body, err := buildSubmitRequest(c, &req)
	if err != nil {
		return nil
	}
	hasVideo := len(req.ReferenceVideoURLs) > 0
	if contentRaw, ok := req.Metadata["content"].([]interface{}); ok {
		for _, item := range contentRaw {
			if m, ok := item.(map[string]interface{}); ok {
				if m["type"] == "video_url" {
					hasVideo = true
				}
			}
		}
	}
	ratios := map[string]float64{
		"duration_factor": tokensPerSecond * float64(body.Duration) / float64(common.QuotaPerUnit/2),
	}
	if r, ok := doubao.GetVideoInputRatio(info.OriginModelName, "", hasVideo); ok && r != 1.0 {
		ratios["video_input"] = r
	}
	return ratios
}

// AdjustBillingOnSubmit 上游 creditCost 是水晶口径，与网关计费无关，不做调整。
func (a *TaskAdaptor) AdjustBillingOnSubmit(_ *relaycommon.RelayInfo, _ []byte) map[string]float64 {
	return nil
}

// AdjustBillingOnComplete 上游不回显 usage（也无 token 概念），维持预扣值即官方标准价。
func (a *TaskAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

// FetchTask 轮询：POST /api/v1/task/query，body {taskKey}。
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskKey, ok := body["task_id"].(string)
	if !ok || taskKey == "" {
		return nil, fmt.Errorf("invalid task_id")
	}
	payload, err := common.Marshal(map[string]string{"taskKey": taskKey})
	if err != nil {
		return nil, err
	}
	uri := strings.TrimRight(baseUrl, "/") + "/api/v1/task/query"
	req, err := http.NewRequest(http.MethodPost, uri, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

// ParseTaskResult 解析轮询响应，映射任务状态。
func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var env envelope
	if err := common.Unmarshal(respBody, &env); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}
	if env.Code != "0" {
		// 任务不存在（FK0006）视为失败；其余错误保持失败语义并带出原因
		return &relaycommon.TaskInfo{
			Status: model.TaskStatusFailure,
			Reason: fmt.Sprintf("fly4k %s: %s", env.Code, env.Msg),
		}, nil
	}

	taskResult := &relaycommon.TaskInfo{Code: 0, TaskID: env.Data.TaskKey}
	switch env.Data.Status {
	case "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = env.Data.OutputUrl
	case "failed":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = env.Data.ErrorDesc
	case "expired":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = "任务超时（创建超过 48 小时未完成）"
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}
	return taskResult, nil
}

// ConvertToOpenAIVideo 轮询查询端点（OpenAI 风格）回显。
func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	var env envelope
	if err := common.Unmarshal(originTask.Data, &env); err == nil {
		if env.Data.OutputUrl != "" {
			openAIVideo.SetMetadata("url", env.Data.OutputUrl)
		} else if originTask.Status == model.TaskStatusSuccess {
			openAIVideo.SetMetadata("url", originTask.GetResultURL())
		}
		lastFrame := env.Data.LastFrameUrl
		if lastFrame == "" {
			lastFrame = env.Data.LastFrameUrlAlt
		}
		if lastFrame == "" {
			lastFrame = env.Data.OutputLastFrame
		}
		if lastFrame != "" {
			openAIVideo.SetMetadata("last_frame_url", lastFrame)
		}
		if env.Data.Status == "failed" && env.Data.ErrorDesc != "" {
			openAIVideo.Error = &dto.OpenAIVideoError{Message: env.Data.ErrorDesc, Code: env.Code}
		}
	} else if originTask.Status == model.TaskStatusSuccess {
		openAIVideo.SetMetadata("url", originTask.GetResultURL())
	}
	if originTask.Status == model.TaskStatusFailure && originTask.FailReason != "" && openAIVideo.Error == nil {
		openAIVideo.Error = &dto.OpenAIVideoError{Message: originTask.FailReason}
	}
	return common.Marshal(openAIVideo)
}

// ============================
// 方舟（Seedance）原生协议支持
//
// 下游客户可以直接沿用方舟官方 SDK / 官方调用方式：
//   POST /api/v3/contents/generations/tasks
//   GET  /api/v3/contents/generations/tasks/{id}
// 只需把 base_url 指向本网关、api_key 换成网关令牌。
//
// 实现要点（2026-10-08）：
//   - 提交：请求体由 middleware.DoubaoVideoRequestConvert 转成统一格式并把原生
//     body 塞进 metadata；buildSubmitRequest 从 metadata.content[] 取素材。
//   - 提交响应：方舟客户端只解 {id}，见 DoResponse。
//   - 查询响应：ConvertToNativeVideo 产出方舟 v3 契约。
//
// 上游差异（刻意丢弃，避免「客户端传了但上游不认」导致任务创建失败）：
//   - model：蝶变上游写死，网关只用于路由/计费
//   - resolution：上游无此参数（出片分辨率固定），渠道侧能力声明为准
//   - camera_fixed：2026-09-28 实测上游直接拒绝该参数
// ============================

// nativeVideoResponse 方舟 v3 任务查询响应（与 seedance_sls 适配器同一契约）。
type nativeVideoResponse struct {
	ID        string              `json:"id"`
	Model     string              `json:"model"`
	Status    string              `json:"status"`
	Error     *nativeVideoError   `json:"error,omitempty"`
	Content   *nativeVideoContent `json:"content,omitempty"`
	CreatedAt int64               `json:"created_at"`
	UpdatedAt int64               `json:"updated_at"`
}

type nativeVideoContent struct {
	VideoURL string `json:"video_url"`
}

type nativeVideoError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// ConvertToNativeVideo 以方舟 v3 契约回显任务查询结果。
// 网关只持久化任务状态与上游信封，故此处做「状态映射 + 地址提取」，而非原样透传。
func (a *TaskAdaptor) ConvertToNativeVideo(originTask *model.Task) ([]byte, error) {
	response := nativeVideoResponse{
		ID:        originTask.TaskID,
		Model:     originTask.Properties.OriginModelName,
		CreatedAt: originTask.CreatedAt,
		UpdatedAt: originTask.UpdatedAt,
	}

	// 从落库的上游信封里取视频地址与失败原因
	videoURL := ""
	errorMessage := ""
	errorCode := ""
	if len(originTask.Data) > 0 {
		var env envelope
		if err := common.Unmarshal(originTask.Data, &env); err == nil {
			videoURL = env.Data.OutputUrl
			errorMessage = env.Data.ErrorDesc
			if env.Code != "" && env.Code != "0" {
				errorCode = env.Code
			}
		}
	}
	if videoURL == "" {
		// 信封解析不到时回退通用提取（与 doubao 适配器同一兜底口径）
		videoURL = model.ExtractUpstreamVideoURL(originTask.Data)
	}

	switch originTask.Status {
	case model.TaskStatusNotStart, model.TaskStatusQueued:
		response.Status = "queued"
	case model.TaskStatusInProgress:
		response.Status = "running"
	case model.TaskStatusSuccess:
		response.Status = "succeeded"
		if videoURL == "" {
			videoURL = originTask.GetResultURL()
		}
		response.Content = &nativeVideoContent{VideoURL: videoURL}
	case model.TaskStatusFailure:
		response.Status = "failed"
		message := errorMessage
		if message == "" {
			message = originTask.FailReason
		}
		response.Error = &nativeVideoError{Code: errorCode, Message: message}
	default:
		response.Status = "queued"
	}

	return common.Marshal(response)
}
