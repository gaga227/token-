package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// fly4kAssetLibraryBackend 实现蝶变（fly4k）的私域素材库（即商家所说的「人像库」）。
//
// 协议（2026-10-08 官方文档 + 真实上游实测）：
//
//	POST /api/v1/createAsset  {url, assetType?, name?} -> data.{assetId, assetUri}
//	POST /api/v1/asset/query  {assetId}                -> data.{Id, Status, ...}
//
// 统一信封 {code, msg, data}，仅 code == "0" 表示成功。
//
// 本文件是**完全自包含**的一种后端实现，不修改任何共享代码路径；是否启用由
// 后台渠道配置的 backend（即「Upstream asset protocol」下拉）决定，其它后端
// （volcengine / seedance_sls / openapi / oinone）的接口与规则不受影响。
//
// 与其它后端的语义差异：
//   - fly4k 没有分组（group）概念，接口也不接受分组参数：CreateGroup 直接返回
//     一个本地占位 id，让分组副本立刻进入 ready（否则 UI 会永久显示「同步中」）。
//     该占位 id 永远不会发往上游（CreateAsset 忽略分组，DeleteGroup 为空实现）。
//   - fly4k 没有素材/分组的更新与删除接口：Update*/Delete* 均为空实现，
//     删除素材只会清理本地记录，上游素材会保留（已与用户确认该取舍）。
//   - 上游没有 project / region 概念：一律不参与任何上游请求。
//
// 为什么能解决人像审核：上游会把入库素材转存进火山方舟 Ark 素材库，
// 入库时过一次审，生成阶段按 asset 引用不再逐次做人脸检测（2026-10-08 实测）。
type fly4kAssetLibraryBackend struct{}

// fly4kEnvelope 蝶变统一响应信封。data 用 RawMessage 以便按接口分别解码。
type fly4kEnvelope struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type fly4kCreateAssetRequest struct {
	URL       string `json:"url"`
	AssetType string `json:"assetType,omitempty"`
	Name      string `json:"name,omitempty"`
}

type fly4kCreateAssetData struct {
	AssetId  string `json:"assetId"`
	AssetUri string `json:"assetUri"`
}

type fly4kAssetQueryRequest struct {
	AssetId string `json:"assetId"`
}

// fly4kAssetQueryData 素材详情。文档与实测均为 PascalCase；encoding/json 的键
// 匹配大小写不敏感，因此小写返回也能正确落位。
type fly4kAssetQueryData struct {
	Id          string `json:"Id"`
	Name        string `json:"Name"`
	URL         string `json:"URL"`
	AssetType   string `json:"AssetType"`
	GroupId     string `json:"GroupId"`
	Status      string `json:"Status"`
	ProjectName string `json:"ProjectName"`
	CreateTime  string `json:"CreateTime"`
	UpdateTime  string `json:"UpdateTime"`
}

// fly4kLocalAssetGroupPlaceholderPrefix 标记写入 upstream_group_id 的本地占位值，
// 便于运维一眼看出该 id 并非上游真实 id。
const fly4kLocalAssetGroupPlaceholderPrefix = "local-fly4k:"

// DefaultFly4kAssetLibraryBaseURL 素材库默认上游地址（与渠道 base_url 同源）。
const DefaultFly4kAssetLibraryBaseURL = "https://api.fly4k.com"

func (fly4kAssetLibraryBackend) CreateGroup(_ context.Context, _ *model.ChannelAssetConfig, group *model.UserAssetGroup) (*assetLibraryCreateGroupResult, error) {
	if group == nil || strings.TrimSpace(group.Id) == "" {
		return nil, errors.New("asset group id is missing")
	}
	// fly4k 无分组接口，不回上游。返回本地占位 id 让副本就绪（非 Deferred），
	// 避免前端长期显示「同步中」。
	return &assetLibraryCreateGroupResult{
		GroupID: fly4kLocalAssetGroupPlaceholderPrefix + group.Id,
	}, nil
}

func (fly4kAssetLibraryBackend) CreateAsset(ctx context.Context, config *model.ChannelAssetConfig, _ *model.UserAssetGroup, _ *model.UserAssetGroupReplica, asset *model.UserAsset) (*assetLibraryCreateAssetResult, error) {
	if asset == nil {
		return nil, errors.New("asset is required")
	}
	assetURL := common.AssetStorageAccessURL(asset.StorageKey, asset.SourceURL)
	if !assetURLIsPubliclyReachable(assetURL) {
		// fly4k 没有文件上传接口，只能从公网 URL 拉取素材。
		return nil, fmt.Errorf("asset URL %q is not publicly reachable; fly4k requires a public URL (store assets in OSS or use a public asset URL)", assetURL)
	}
	request := fly4kCreateAssetRequest{
		URL:       assetURL,
		AssetType: strings.TrimSpace(asset.AssetType),
		Name:      strings.TrimSpace(asset.Name),
	}
	var result fly4kCreateAssetData
	if err := callFly4kAssetLibrary(ctx, config, "/api/v1/createAsset", request, &result); err != nil {
		return nil, err
	}
	assetId := strings.TrimSpace(result.AssetId)
	if assetId == "" {
		return nil, errors.New("fly4k returned an empty asset id")
	}
	// 入库为异步：上游先返回 Processing，需轮询 asset/query 至 Active。
	return &assetLibraryCreateAssetResult{
		AssetID: assetId,
		Status:  "Processing",
	}, nil
}

func (fly4kAssetLibraryBackend) UpdateGroup(context.Context, *model.ChannelAssetConfig, *model.UserAssetGroup, string) error {
	// fly4k 未提供分组接口。
	return nil
}

func (fly4kAssetLibraryBackend) UpdateAsset(context.Context, *model.ChannelAssetConfig, *model.UserAsset, string) error {
	// fly4k 未提供素材更新接口。
	return nil
}

func (fly4kAssetLibraryBackend) DeleteGroup(context.Context, *model.ChannelAssetConfig, string) error {
	// fly4k 未提供删除分组接口。
	return nil
}

func (fly4kAssetLibraryBackend) DeleteAsset(context.Context, *model.ChannelAssetConfig, string) error {
	// fly4k 未提供删除素材接口：仅清理本地副本，上游素材保留。
	return nil
}

func (fly4kAssetLibraryBackend) GetAsset(ctx context.Context, config *model.ChannelAssetConfig, upstreamAssetId string) (*AssetLibraryAssetDetails, error) {
	assetId := strings.TrimSpace(upstreamAssetId)
	if assetId == "" {
		return nil, errors.New("fly4k asset id is empty")
	}
	var result fly4kAssetQueryData
	if err := callFly4kAssetLibrary(ctx, config, "/api/v1/asset/query", fly4kAssetQueryRequest{AssetId: assetId}, &result); err != nil {
		return nil, err
	}
	details := &AssetLibraryAssetDetails{
		Id:         firstNonEmptyString(result.Id, assetId),
		Name:       result.Name,
		URL:        result.URL,
		GroupId:    result.GroupId,
		AssetType:  result.AssetType,
		Status:     result.Status,
		CreateTime: result.CreateTime,
		UpdateTime: result.UpdateTime,
	}
	if strings.EqualFold(details.Status, "Failed") {
		details.Error = &dto.AssetLibraryError{
			Code:    "AssetSyncFailed",
			Message: "fly4k asset ingestion failed upstream",
		}
	}
	return details, nil
}

func (fly4kAssetLibraryBackend) FormatAssetReference(upstreamAssetId string) string {
	return "asset://" + upstreamAssetId
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// callFly4kAssetLibrary 调用蝶变素材接口：Bearer 鉴权 + 统一信封解析。
// 非 2xx 与 code != "0" 一律转换为 AssetLibraryUpstreamError，便于副本记录错误码。
func callFly4kAssetLibrary(ctx context.Context, config *model.ChannelAssetConfig, path string, input any, output any) error {
	if config == nil || !config.Enabled {
		return errors.New("asset library is not enabled for channel")
	}
	if config.AuthType != AssetLibraryAuthBearer {
		return errors.New("fly4k asset library requires Bearer authentication")
	}
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" {
		return errors.New("asset library API key is empty")
	}
	endpoint, err := fly4kAssetLibraryURL(config.BaseURL, path)
	if err != nil {
		return err
	}
	var body []byte
	if input != nil {
		body, err = common.Marshal(input)
		if err != nil {
			return err
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	client := GetHttpClient()
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	var envelope fly4kEnvelope
	decodeErr := common.Unmarshal(responseBody, &envelope)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if decodeErr == nil {
			if message := common.MaskSensitiveInfo(common.LocalLogPreview(envelope.Msg)); message != "" {
				return &AssetLibraryUpstreamError{StatusCode: response.StatusCode, Code: envelope.Code, Message: message}
			}
		}
		return &AssetLibraryUpstreamError{StatusCode: response.StatusCode, Message: http.StatusText(response.StatusCode)}
	}
	if decodeErr != nil {
		return errors.New("decode fly4k asset library response")
	}
	if envelope.Code != "0" {
		message := common.MaskSensitiveInfo(common.LocalLogPreview(envelope.Msg))
		if message == "" {
			message = "fly4k asset library request failed"
		}
		return &AssetLibraryUpstreamError{StatusCode: response.StatusCode, Code: envelope.Code, Message: message}
	}
	if output == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("fly4k asset library response is missing data")
	}
	return common.Unmarshal(envelope.Data, output)
}

func fly4kAssetLibraryURL(baseURL string, path string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultFly4kAssetLibraryBaseURL
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", errors.New("invalid fly4k asset library base URL")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.TrimLeft(path, "/")
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}
