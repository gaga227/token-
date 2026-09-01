package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// oinoneAssetLibraryBackend implements the BytePlus-style asset library
// exposed by oinone (https://api.oinone.top/byteplus). Unlike the official
// Volcengine OpenAPI (Action/Version query parameters), oinone only implements
// the RESTful routes under /byteplus, so every method maps to an explicit path.
// The base URL must point at the /byteplus root (e.g.
// "https://api.oinone.top/byteplus").
type oinoneAssetLibraryBackend struct{}

type oinoneErrorEnvelope struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type oinoneGroupData struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	GroupType string        `json:"group_type"`
	Status    string        `json:"status"`
	CreatedAt flexibleTime  `json:"create_time"`
	UpdatedAt flexibleTime  `json:"update_time"`
}

type oinoneAssetData struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	URL        string       `json:"url"`
	GroupID    string       `json:"group_id"`
	AssetType  string       `json:"asset_type"`
	Status     string       `json:"status"`
	Error      string       `json:"error_message"`
	CreateTime flexibleTime `json:"create_time"`
	UpdateTime flexibleTime `json:"update_time"`
}

// flexibleTime accepts either a unix timestamp number or a string, since
// oinone may return either depending on the route.
type flexibleTime struct {
	value string
}

func (t *flexibleTime) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" || text == "" || text == `""` {
		t.value = ""
		return nil
	}
	text = strings.Trim(text, `"`)
	t.value = text
	return nil
}

func (t flexibleTime) String() string { return t.value }

type oinoneCreateGroupRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	GroupType   string  `json:"group_type"`
}

type oinoneCreateAssetRequest struct {
	Name      string `json:"name,omitempty"`
	URL       string `json:"url"`
	AssetType string `json:"asset_type"`
}

type oinoneUpdateGroupRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type oinoneUpdateAssetRequest struct {
	Name string `json:"name"`
}

func (oinoneAssetLibraryBackend) CreateGroup(ctx context.Context, config *model.ChannelAssetConfig, group *model.UserAssetGroup) (*assetLibraryCreateGroupResult, error) {
	groupType := strings.TrimSpace(group.GroupType)
	if groupType == "" {
		groupType = "AIGC"
	}
	request := oinoneCreateGroupRequest{Name: group.Name, GroupType: groupType}
	if strings.TrimSpace(group.Description) != "" {
		request.Description = &group.Description
	}
	var result oinoneGroupData
	if err := callOinoneAssetLibrary(ctx, config, http.MethodPost, "/asset-groups", request, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.ID) == "" {
		return nil, errors.New("oinone returned an empty group id")
	}
	return &assetLibraryCreateGroupResult{GroupID: result.ID}, nil
}

func (oinoneAssetLibraryBackend) CreateAsset(ctx context.Context, config *model.ChannelAssetConfig, _ *model.UserAssetGroup, groupReplica *model.UserAssetGroupReplica, asset *model.UserAsset) (*assetLibraryCreateAssetResult, error) {
	if groupReplica == nil || strings.TrimSpace(groupReplica.UpstreamGroupId) == "" {
		return nil, errors.New("asset library group replica is unavailable")
	}
	assetURL := common.AssetStorageAccessURL(asset.StorageKey, asset.SourceURL)
	if !assetURLIsPubliclyReachable(assetURL) {
		// oinone has no file upload endpoint: it fetches the asset from a
		// public URL. Local/private URLs cannot be replicated.
		return nil, fmt.Errorf("asset URL %q is not publicly reachable; oinone requires a public URL (store assets in OSS or use a public asset URL)", assetURL)
	}
	request := oinoneCreateAssetRequest{
		Name:      asset.Name,
		URL:       assetURL,
		AssetType: asset.AssetType,
	}
	path := "/asset-groups/" + url.PathEscape(groupReplica.UpstreamGroupId) + "/assets"
	var result oinoneAssetData
	if err := callOinoneAssetLibrary(ctx, config, http.MethodPost, path, request, &result); err != nil {
		return nil, err
	}
	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = "Processing"
	}
	return &assetLibraryCreateAssetResult{
		AssetID: result.ID,
		GroupID: groupReplica.UpstreamGroupId,
		Status:  status,
	}, nil
}

func (oinoneAssetLibraryBackend) UpdateGroup(ctx context.Context, config *model.ChannelAssetConfig, group *model.UserAssetGroup, upstreamGroupId string) error {
	request := oinoneUpdateGroupRequest{Name: &group.Name}
	if strings.TrimSpace(group.Description) != "" {
		request.Description = &group.Description
	}
	path := "/asset-groups/" + url.PathEscape(upstreamGroupId)
	return callOinoneAssetLibrary(ctx, config, http.MethodPut, path, request, nil)
}

func (oinoneAssetLibraryBackend) UpdateAsset(ctx context.Context, config *model.ChannelAssetConfig, asset *model.UserAsset, upstreamAssetId string) error {
	groupID, err := oinoneUpstreamGroupIDForAsset(config.ChannelId, upstreamAssetId)
	if err != nil {
		return err
	}
	path := "/asset-groups/" + url.PathEscape(groupID) + "/assets/" + url.PathEscape(upstreamAssetId)
	return callOinoneAssetLibrary(ctx, config, http.MethodPut, path, oinoneUpdateAssetRequest{Name: asset.Name}, nil)
}

func (oinoneAssetLibraryBackend) DeleteGroup(ctx context.Context, config *model.ChannelAssetConfig, upstreamGroupId string) error {
	path := "/asset-groups/" + url.PathEscape(upstreamGroupId)
	return callOinoneAssetLibrary(ctx, config, http.MethodDelete, path, nil, nil)
}

func (oinoneAssetLibraryBackend) DeleteAsset(ctx context.Context, config *model.ChannelAssetConfig, upstreamAssetId string) error {
	groupID, err := oinoneUpstreamGroupIDForAsset(config.ChannelId, upstreamAssetId)
	if err != nil {
		return err
	}
	path := "/asset-groups/" + url.PathEscape(groupID) + "/assets/" + url.PathEscape(upstreamAssetId)
	return callOinoneAssetLibrary(ctx, config, http.MethodDelete, path, nil, nil)
}

func (oinoneAssetLibraryBackend) GetAsset(ctx context.Context, config *model.ChannelAssetConfig, upstreamAssetId string) (*AssetLibraryAssetDetails, error) {
	groupID, err := oinoneUpstreamGroupIDForAsset(config.ChannelId, upstreamAssetId)
	if err != nil {
		return nil, err
	}
	path := "/asset-groups/" + url.PathEscape(groupID) + "/assets/" + url.PathEscape(upstreamAssetId)
	var result oinoneAssetData
	if err := callOinoneAssetLibrary(ctx, config, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	details := &AssetLibraryAssetDetails{
		Id:          result.ID,
		Name:        result.Name,
		URL:         result.URL,
		GroupId:     result.GroupID,
		AssetType:   result.AssetType,
		Status:      result.Status,
		ProjectName: assetLibraryProject(config),
		CreateTime:  result.CreateTime.String(),
		UpdateTime:  result.UpdateTime.String(),
	}
	if strings.TrimSpace(result.Error) != "" {
		details.Error = &dto.AssetLibraryError{
			Code:    "AssetSyncFailed",
			Message: common.MaskSensitiveInfo(common.LocalLogPreview(result.Error)),
		}
	}
	return details, nil
}

func (oinoneAssetLibraryBackend) FormatAssetReference(upstreamAssetId string) string {
	return "asset://" + upstreamAssetId
}

// oinoneUpstreamGroupIDForAsset walks from an upstream asset id back to the
// upstream group id that owns it, so that RESTful asset operations can build
// the /asset-groups/{groupId}/assets/{assetId} path.
func oinoneUpstreamGroupIDForAsset(channelId int, upstreamAssetId string) (string, error) {
	replica, err := model.GetUserAssetReplicaByUpstreamId(upstreamAssetId, channelId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("asset replica %q not found for channel %d", upstreamAssetId, channelId)
		}
		return "", err
	}
	asset, err := model.GetUserAssetById(replica.AssetId)
	if err != nil {
		return "", err
	}
	groupReplica, err := model.GetUserAssetGroupReplica(asset.GroupId, channelId)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(groupReplica.UpstreamGroupId) == "" {
		return "", fmt.Errorf("upstream group id is empty for asset %q on channel %d", upstreamAssetId, channelId)
	}
	return groupReplica.UpstreamGroupId, nil
}

func callOinoneAssetLibrary(ctx context.Context, config *model.ChannelAssetConfig, method string, path string, input any, output any) error {
	if config == nil || !config.Enabled {
		return errors.New("asset library is not enabled for channel")
	}
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" {
		return errors.New("asset library API key is empty")
	}
	endpoint, err := oinoneAssetLibraryURL(config.BaseURL, path)
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
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
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
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var envelope oinoneErrorEnvelope
		message := http.StatusText(response.StatusCode)
		if common.Unmarshal(responseBody, &envelope) == nil && envelope.Error != nil {
			message = common.MaskSensitiveInfo(common.LocalLogPreview(envelope.Error.Message))
			if message == "" {
				message = http.StatusText(response.StatusCode)
			}
			return &AssetLibraryUpstreamError{StatusCode: response.StatusCode, Code: envelope.Error.Code, Message: message}
		}
		return &AssetLibraryUpstreamError{StatusCode: response.StatusCode, Message: message}
	}
	if output == nil {
		return nil
	}
	return common.Unmarshal(responseBody, output)
}

func oinoneAssetLibraryURL(baseURL string, path string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", errors.New("oinone asset library base URL is empty")
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", errors.New("invalid oinone asset library base URL")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.TrimLeft(path, "/")
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}
