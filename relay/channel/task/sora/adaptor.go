package sora

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/tidwall/sjson"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type"`                // "text" or "image_url"
	Text     string    `json:"text,omitempty"`      // for text type
	ImageURL *ImageURL `json:"image_url,omitempty"` // for image_url type
}

type ImageURL struct {
	URL string `json:"url"`
}

type responseTask struct {
	ID                 string `json:"id"`
	TaskID             string `json:"task_id,omitempty"` //兼容旧接口
	Object             string `json:"object"`
	Model              string `json:"model"`
	Status             string `json:"status"`
	Progress           int    `json:"progress"`
	CreatedAt          int64  `json:"created_at"`
	CompletedAt        int64  `json:"completed_at,omitempty"`
	ExpiresAt          int64  `json:"expires_at,omitempty"`
	Seconds            string `json:"seconds,omitempty"`
	Size               string `json:"size,omitempty"`
	RemixedFromVideoID string `json:"remixed_from_video_id,omitempty"`
	Error              *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func validateRemixRequest(c *gin.Context) *dto.TaskError {
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("field prompt is required"), "invalid_request", http.StatusBadRequest)
	}
	// 存储原始请求到 context，与 ValidateMultipartDirect 路径保持一致
	c.Set("task_request", req)
	return nil
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	if info.Action == constant.TaskActionRemix {
		return validateRemixRequest(c)
	}
	return relaycommon.ValidateMultipartDirect(c, info)
}

// EstimateBilling 根据请求的 seconds、size 与素材（图片/视频）计算 OtherRatios。
// 输出费用与素材费用统一折算为「等效基准秒数」（基准 = 768P 每秒 0.5 元）：
//
//	等效秒数 = (输出秒数 + 视频输入实际时长) × 分辨率倍率 + max(0, 图片张数-5) × 0.4
//
// 素材识别同时支持两套字段：
//   - OpenAI 风格：image / images / input_reference（图片或视频 URL）+ multipart image*/video* 文件
//   - MiniMax/maitoken 文档风格：image(首帧) / last_frame_image_url(尾帧) /
//     reference_image_urls(参考图≤9) / reference_video_urls(参考视频≤3) / reference_audio_urls(音频，免费)
//
// 视频输入时长从 MP4/MOV 文件或 http(s) URL（HTTP Range）解析实际时长；
// 解析失败（不支持格式/网络异常/base64 直传）的视频段按「输出秒数」近似。
// 因 OtherRatios 为纯乘法叠加，素材费不能直接相乘，故折算进 seconds 而非新增倍率。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	// remix 路径的 OtherRatios 已在 ResolveOriginTask 中设置
	if info.Action == constant.TaskActionRemix {
		return nil
	}

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}

	seconds, _ := strconv.Atoi(req.Seconds)
	if seconds == 0 {
		seconds = req.Duration
	}
	if seconds <= 0 {
		seconds = 4
	}

	size := req.Size
	if size == "" {
		size = "720x1280"
	}

	// 分辨率倍率（768P=1.0、2K=1.6、480P=0.66）
	sizeRatio := GetMiniMaxResolutionRatio(info.OriginModelName, size)

	// ---- 素材识别（OpenAI 风格 + MiniMax/maitoken 文档风格统一计费）----
	// 图片计数：OpenAI image/input_reference/images（ValidateMultipartDirect 已把单张
	// image/input_reference 归入 req.Images）+ 文档 image(首帧)/last_frame_image_url(尾帧)/
	// reference_image_urls(参考图) + multipart image* 文件
	// 视频输入（按实际时长计费）：input_reference 视频 URL + reference_video_urls + multipart video* 文件
	imageCount := len(req.Images)
	inputSeconds := 0.0
	hasVideoInput := false
	videoFallbackSegments := 0 // 存在视频但无法解析实际时长的段数，按输出秒数近似

	// input_reference 为视频 URL（.mp4/.mov 等）→ 远程解析实际时长，并从图片计数中排除
	if strings.TrimSpace(req.InputReference) != "" && IsVideoReference(req.InputReference) {
		hasVideoInput = true
		imageCount--
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		if dur, derr := GetRemoteVideoDuration(ctx, req.InputReference); derr == nil && dur > 0 {
			inputSeconds += dur
		} else {
			videoFallbackSegments++
		}
		cancel()
	}

	// 文档风格参考视频（reference_video_urls，≤3 段）：http(s) URL 远程解析实际时长，
	// base64/data 直传或解析失败按输出秒数近似；参考音频（reference_audio_urls）免费不计
	for _, vu := range req.ReferenceVideoURLs {
		if strings.TrimSpace(vu) == "" {
			continue
		}
		hasVideoInput = true
		if strings.HasPrefix(vu, "http://") || strings.HasPrefix(vu, "https://") {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
			if dur, derr := GetRemoteVideoDuration(ctx, vu); derr == nil && dur > 0 {
				inputSeconds += dur
			} else {
				videoFallbackSegments++
			}
			cancel()
		} else {
			videoFallbackSegments++
		}
	}

	// 文档风格图片：参考图数组 + 尾帧；首帧 image 单张仅在未被归入 req.Images 时补计
	imageCount += len(req.ReferenceImageURLs)
	if strings.TrimSpace(req.LastFrameImageURL) != "" {
		imageCount++
	}
	if strings.TrimSpace(req.Image) != "" && len(req.Images) == 0 && strings.TrimSpace(req.InputReference) == "" {
		imageCount++
	}

	// multipart 文件字段（image* / video*）
	if form, ferr := c.MultipartForm(); ferr == nil && form != nil {
		for field, headers := range form.File {
			f := strings.ToLower(field)
			if strings.Contains(f, "image") {
				imageCount += len(headers)
			}
			if strings.Contains(f, "video") {
				hasVideoInput = true
				for _, fh := range headers {
					if dur, derr := probeVideoDuration(fh); derr == nil && dur > 0 {
						inputSeconds += dur
					} else {
						videoFallbackSegments++
					}
				}
			}
		}
	}
	// 无法解析实际时长的视频输入段，按「输入时长 = 输出秒数」近似（保持 ×2 行为），
	// 避免计费中断或漏收；纯图片/纯 T2V 请求不受影响。
	if hasVideoInput && videoFallbackSegments > 0 {
		inputSeconds += float64(videoFallbackSegments) * float64(seconds)
	}

	// 图片超额：5 张内免费，超出每张折算 0.4 基准秒（0.2 元 / 0.5 元/秒）
	extraImages := imageCount - MiniMaxFreeImageCount
	if extraImages < 0 {
		extraImages = 0
	}

	// 视频输入按实际时长 × 输出分辨率单价（同官网：输入时长 + 输出分辨率计费）
	// 拆分输出/输入等效秒数并写入 info，供任务创建时持久化到 BillingContext，
	// 查询接口据此把 task.Quota 拆成「输出消耗 + 输入消耗」两部分返回给下游对账。
	outputEffectiveSeconds := float64(seconds) * sizeRatio
	inputEffectiveSeconds := inputSeconds*sizeRatio + float64(extraImages)*MiniMaxExtraImageRatio
	effectiveSeconds := outputEffectiveSeconds + inputEffectiveSeconds
	info.EstimatedOutputSeconds = outputEffectiveSeconds
	info.EstimatedInputSeconds = inputEffectiveSeconds

	return map[string]float64{
		"seconds": effectiveSeconds,
	}
}

// probeVideoDuration 打开 multipart 视频文件并解析实际时长（秒）。
// 解析失败（非 MP4/MOV 容器等）返回错误，由 EstimateBilling fallback。
func probeVideoDuration(fh *multipart.FileHeader) (float64, error) {
	f, err := fh.Open()
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return GetInputVideoDuration(f, fh.Filename)
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.Action == constant.TaskActionRemix {
		return fmt.Sprintf("%s/v1/videos/%s/remix", a.baseURL, info.OriginTaskID), nil
	}
	return fmt.Sprintf("%s/v1/videos", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, errors.Wrap(err, "get_request_body_failed")
	}
	cachedBody, err := storage.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "read_body_bytes_failed")
	}
	contentType := c.GetHeader("Content-Type")

	if strings.HasPrefix(contentType, "application/json") {
		var bodyMap map[string]interface{}
		if err := common.Unmarshal(cachedBody, &bodyMap); err == nil {
			bodyMap["model"] = info.UpstreamModelName
			if err := sanitizeVideoRequestBody(bodyMap, info.UpstreamModelName); err != nil {
				return nil, err
			}
			if newBody, err := common.Marshal(bodyMap); err == nil {
				return bytes.NewReader(newBody), nil
			}
		}
		return bytes.NewReader(cachedBody), nil
	}

	if strings.Contains(contentType, "multipart/form-data") {
		formData, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return bytes.NewReader(cachedBody), nil
		}
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		writer.WriteField("model", info.UpstreamModelName)
		for key, values := range formData.Value {
			if key == "model" {
				continue
			}
			for _, v := range values {
				writer.WriteField(key, v)
			}
		}
		for fieldName, fileHeaders := range formData.File {
			for _, fh := range fileHeaders {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				ct := fh.Header.Get("Content-Type")
				if ct == "" || ct == "application/octet-stream" {
					buf512 := make([]byte, 512)
					n, _ := io.ReadFull(f, buf512)
					ct = http.DetectContentType(buf512[:n])
					// Re-open after sniffing so the full content is copied below
					f.Close()
					f, err = fh.Open()
					if err != nil {
						continue
					}
				}
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, fh.Filename))
				h.Set("Content-Type", ct)
				part, err := writer.CreatePart(h)
				if err != nil {
					f.Close()
					continue
				}
				io.Copy(part, f)
				f.Close()
			}
		}
		writer.Close()
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		return &buf, nil
	}

	return common.NewReplayableBodyReader(storage), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// isFlatMiniMaxVideoModel reports whether the upstream model is served by
// maitoken's flat H3 endpoint, which strictly validates request fields and
// rejects OpenAI-style video fields (duration/size) with HTTP 422.
func isFlatMiniMaxVideoModel(model string) bool {
	switch model {
	case "minimax-h3-base", "minimax-h3-base-fast", "minimax-h3-mini":
		return true
	}
	return false
}

// sanitizeVideoRequestBody adapts the client body before it is forwarded
// upstream:
//   - "group" is a gateway-only routing hint and never a valid upstream field;
//   - flat H3 models (minimax-h3-base etc.) expect seconds/resolution/
//     aspect_ratio/images/audios instead of duration/size/image/... so the
//     OpenAI-style fields are mapped onto the flat schema.
//
// It returns a descriptive error when a flat H3 value can be rejected
// locally (e.g. seconds out of the 5~15 range) so the client gets a clear
// failure instead of a nested upstream 422.
func sanitizeVideoRequestBody(body map[string]interface{}, model string) error {
	delete(body, "group")
	if !isFlatMiniMaxVideoModel(model) {
		return nil
	}
	if v, ok := body["duration"].(float64); ok {
		if _, exists := body["seconds"]; !exists {
			// The flat H3 API types seconds as a string (e.g. "5").
			body["seconds"] = strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	delete(body, "duration")
	if v, ok := body["seconds"]; ok {
		var sec float64
		switch t := v.(type) {
		case string:
			sec, _ = strconv.ParseFloat(strings.TrimSpace(t), 64)
		case float64:
			sec = t
		}
		if sec < 5 || sec > 15 {
			return fmt.Errorf("seconds must be between 5 and 15 for MiniMax H3 (got %v)", v)
		}
	}
	if size, ok := body["size"].(string); ok && size != "" {
		delete(body, "size")
		if _, exists := body["resolution"]; !exists {
			body["resolution"] = resolutionFromSize(size)
		}
		if _, exists := body["aspect_ratio"]; !exists {
			if ratio := aspectRatioFromSize(size); ratio != "" {
				body["aspect_ratio"] = ratio
			}
		}
	}
	// Per maitoken's confirmation (2026-09-04): minimax-h3-base-fast runs on
	// the newer upstream contract and expects "768P" (its sibling models
	// base/mini only accept the legacy "720p" enum). Force the value for
	// base-fast regardless of what the client sent so the request always
	// carries the correct tier.
	if model == "minimax-h3-base-fast" {
		body["resolution"] = "768P"
	}
	// Media fields: the flat API only knows images[] / audios[] and requires
	// mode ∈ {t2va, i2va, fl2va, l2va, ref2va} derived from the media mix.
	var images []interface{}
	hasFirstFrame, hasLastFrame, refCount := false, false, 0
	if v, ok := body["image"].(string); ok && v != "" {
		images = append(images, v)
		hasFirstFrame = true
	}
	delete(body, "image")
	if refs, ok := body["reference_image_urls"].([]interface{}); ok {
		images = append(images, refs...)
		refCount += len(refs)
	}
	delete(body, "reference_image_urls")
	if _, ok := body["last_frame_image_url"]; ok {
		hasLastFrame = true
	}
	delete(body, "last_frame_image_url")
	// Reference videos are not part of the flat schema; dropping them avoids
	// a hard 422 (a dedicated field may be added upstream later).
	delete(body, "reference_video_urls")
	if _, exists := body["mode"]; !exists {
		switch {
		case refCount > 0:
			body["mode"] = "ref2va"
		case hasFirstFrame && hasLastFrame:
			body["mode"] = "fl2va"
		case hasFirstFrame:
			body["mode"] = "i2va"
		default:
			body["mode"] = "t2va"
		}
	}
	if len(images) > 0 {
		if existing, ok := body["images"].([]interface{}); ok {
			body["images"] = append(existing, images...)
		} else {
			body["images"] = images
		}
	}
	if refs, ok := body["reference_audio_urls"].([]interface{}); ok {
		delete(body, "reference_audio_urls")
		if _, exists := body["audios"]; !exists {
			body["audios"] = refs
		}
	}
	return nil
}

// resolutionFromSize maps an OpenAI-style "WxH" size onto the flat H3 API's
// resolution enum ("480p" / "720p"). The long edge decides: >=1280 is 720p,
// anything smaller 480p. The 422 error from maitoken confirms these are the
// only valid values: "resolution must be 480p or 720p". Despite the rendered
// resolution being 768P-class, the API enum name is "720p" — do not change
// to "768p" (the user-supplied doc uses 768P as the *rendered* tier name,
// not the API field value).
func resolutionFromSize(size string) string {
	w, h, ok := parseSizeDimensions(size)
	if !ok {
		return "720p"
	}
	long := w
	if h > long {
		long = h
	}
	if long >= 1280 {
		return "720p"
	}
	return "480p"
}

// aspectRatioFromSize reduces a "WxH" size to a common aspect ratio accepted
// by the flat H3 API. Unknown ratios return "" so the field is omitted and
// the upstream default applies.
func aspectRatioFromSize(size string) string {
	w, h, ok := parseSizeDimensions(size)
	if !ok || w == 0 || h == 0 {
		return ""
	}
	a, b := w, h
	for b != 0 {
		a, b = b, a%b
	}
	ratio := fmt.Sprintf("%d:%d", w/a, h/a)
	switch ratio {
	case "1:1", "4:3", "3:4", "16:9", "9:16", "21:9":
		return ratio
	}
	return ""
}

func parseSizeDimensions(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Sora response
	var dResp responseTask
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	upstreamID := dResp.ID
	if upstreamID == "" {
		upstreamID = dResp.TaskID
	}
	if upstreamID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	// 使用公开 task_xxxx ID 返回给客户端
	dResp.ID = info.PublicTaskID
	dResp.TaskID = info.PublicTaskID
	c.JSON(http.StatusOK, dResp)
	return upstreamID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/v1/videos/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	switch resTask.Status {
	case "queued", "pending":
		taskResult.Status = model.TaskStatusQueued
	case "unknown", "not_started", "unstarted":
		// maitoken 等平台在任务创建早期返回 "unknown"（非标准 OpenAI 枚举），
		// 任务尚未真正进入处理队列。按排队中处理，让轮询器继续等待下一轮，
		// 避免误判为失败导致任务被标记 FAILURE 并停止轮询。
		taskResult.Status = model.TaskStatusQueued
	case "processing", "in_progress":
		taskResult.Status = model.TaskStatusInProgress
	case "completed":
		taskResult.Status = model.TaskStatusSuccess
		// Url intentionally left empty — the caller constructs the proxy URL using the public task ID
	case "failed", "cancelled":
		taskResult.Status = model.TaskStatusFailure
		if resTask.Error != nil {
			taskResult.Reason = resTask.Error.Message
		} else {
			taskResult.Reason = "task failed"
		}
	default:
	}
	if resTask.Progress > 0 && resTask.Progress < 100 {
		taskResult.Progress = fmt.Sprintf("%d%%", resTask.Progress)
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	data := task.Data
	var err error
	if data, err = sjson.SetBytes(data, "id", task.TaskID); err != nil {
		return nil, errors.Wrap(err, "set id failed")
	}
	// 注入输入/输出消耗金额（仅查询时返回，创建接口不返回），便于下游记录与对账：
	//   consumed_input_quota / consumed_input_amount   输入消耗（视频输入素材 + 超额图片）
	//   consumed_output_quota / consumed_output_amount 输出消耗（生成视频）
	// task.Quota 为折扣后的最终扣费（含分组倍率），amount = quota / QuotaPerUnit × 汇率，
	// 以站点展示币种金额返回。input+output 恒等于 task.Quota。
	inputQuota, outputQuota := splitConsumedQuota(task)
	if data, err = sjson.SetBytes(data, "consumed_input_quota", inputQuota); err != nil {
		return nil, errors.Wrap(err, "set consumed_input_quota failed")
	}
	if data, err = sjson.SetBytes(data, "consumed_input_amount", quotaToCurrencyAmount(inputQuota)); err != nil {
		return nil, errors.Wrap(err, "set consumed_input_amount failed")
	}
	if data, err = sjson.SetBytes(data, "consumed_output_quota", outputQuota); err != nil {
		return nil, errors.Wrap(err, "set consumed_output_quota failed")
	}
	if data, err = sjson.SetBytes(data, "consumed_output_amount", quotaToCurrencyAmount(outputQuota)); err != nil {
		return nil, errors.Wrap(err, "set consumed_output_amount failed")
	}
	return data, nil
}

// splitConsumedQuota 按创建时持久化的输入/输出等效基准秒数把 task.Quota 拆为两部分。
// 输入按秒数比例独立四舍五入，输出取余，保证 inputQuota + outputQuota == task.Quota
// （避免分别舍入导致的 ±1 差异，方便下游对账）。
// 旧任务/无拆分秒数时全部归入输出消耗（输入为 0）。
func splitConsumedQuota(task *model.Task) (inputQuota, outputQuota int) {
	total := task.Quota
	if total <= 0 {
		return 0, 0
	}
	inSec, outSec := 0.0, 0.0
	if bc := task.PrivateData.BillingContext; bc != nil {
		inSec, outSec = bc.InputSeconds, bc.OutputSeconds
	}
	sum := inSec + outSec
	if sum <= 0 {
		// 无拆分信息：按纯输出任务处理
		return 0, total
	}
	inputQuota = int(math.Round(float64(total) * inSec / sum))
	if inputQuota < 0 {
		inputQuota = 0
	}
	if inputQuota > total {
		inputQuota = total
	}
	return inputQuota, total - inputQuota
}

// quotaToCurrencyAmount 把额度换算为站点展示币种金额（本项目 CNY）：
// amount = quota / QuotaPerUnit × USD 汇率，保留 6 位小数避免浮点尾数影响对账。
// USDExchangeRate 来自运营设置（DB options，当前 6.78）。
func quotaToCurrencyAmount(quota int) float64 {
	usd := float64(quota) / common.QuotaPerUnit
	rate := operation_setting.GetUsdToCurrencyRate(operation_setting.USDExchangeRate)
	return math.Round(usd*rate*1e6) / 1e6
}
