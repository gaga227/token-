package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

type openAPIAssetLibraryBackend struct{}

type openAPIAssetGroupCreateRequest struct {
	GroupName   string  `json:"group_name"`
	Description *string `json:"description,omitempty"`
	GroupType   *int    `json:"group_type,omitempty"`
}

type openAPIAssetCreateRequest struct {
	GroupID   int64   `json:"group_id"`
	URL       string  `json:"url"`
	AssetType int     `json:"asset_type"`
	AssetName *string `json:"asset_name,omitempty"`
}

type openAPIAssetGroupUpdateRequest struct {
	ID          int64   `json:"id"`
	GroupName   *string `json:"group_name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type openAPIAssetUpdateRequest struct {
	ID        int64  `json:"id"`
	AssetName string `json:"asset_name"`
}

type openAPIAssetIDRequest struct {
	ID int64 `json:"id"`
}

type openAPIAssetData struct {
	ID          int64  `json:"id"`
	GroupID     int64  `json:"group_id"`
	AssetName   string `json:"asset_name"`
	AssetType   int    `json:"asset_type"`
	URL         string `json:"url"`
	AssetStatus int    `json:"asset_status"`
	SyncStatus  int    `json:"sync_status"`
	SyncError   string `json:"sync_error"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (openAPIAssetLibraryBackend) CreateGroup(ctx context.Context, config *model.ChannelAssetConfig, group *model.UserAssetGroup) (*assetLibraryCreateGroupResult, error) {
	groupType := 1
	request := openAPIAssetGroupCreateRequest{GroupName: group.Name, GroupType: &groupType}
	if strings.TrimSpace(group.Description) != "" {
		request.Description = &group.Description
	}
	var result struct {
		ID int64 `json:"id"`
	}
	if err := callOpenAPIAssetLibrary(ctx, config, "/openapi/v1/asset/group/create", request, &result); err != nil {
		return nil, err
	}
	if result.ID <= 0 {
		return nil, errors.New("asset OpenAPI returned an invalid group id")
	}
	return &assetLibraryCreateGroupResult{GroupID: strconv.FormatInt(result.ID, 10)}, nil
}

func (openAPIAssetLibraryBackend) CreateAsset(ctx context.Context, config *model.ChannelAssetConfig, _ *model.UserAssetGroup, groupReplica *model.UserAssetGroupReplica, asset *model.UserAsset) (*assetLibraryCreateAssetResult, error) {
	if groupReplica == nil {
		return nil, errors.New("asset OpenAPI group replica is unavailable")
	}
	groupID, err := parseOpenAPIAssetID(groupReplica.UpstreamGroupId, "group")
	if err != nil {
		return nil, err
	}
	assetType, err := openAPIAssetTypeValue(asset.AssetType)
	if err != nil {
		return nil, err
	}
	assetURL := common.AssetStorageAccessURL(asset.StorageKey, asset.SourceURL)
	// If the URL points at the local gateway (localhost / private address),
	// the upstream cannot download it. Upload the file to the upstream's
	// /api/asset/upload endpoint first and use the returned public URL.
	if !assetURLIsPubliclyReachable(assetURL) {
		uploadedURL, uploadErr := uploadAssetFileToUpstream(ctx, config, asset)
		if uploadErr != nil {
			return nil, fmt.Errorf("upload asset file to upstream: %w", uploadErr)
		}
		assetURL = uploadedURL
	}
	request := openAPIAssetCreateRequest{
		GroupID:   groupID,
		URL:       assetURL,
		AssetType: assetType,
	}
	if strings.TrimSpace(asset.Name) != "" {
		request.AssetName = &asset.Name
	}
	var result struct {
		ID int64 `json:"id"`
	}
	if err := callOpenAPIAssetLibrary(ctx, config, "/openapi/v1/asset/create", request, &result); err != nil {
		return nil, err
	}
	if result.ID <= 0 {
		return nil, errors.New("asset OpenAPI returned an invalid asset id")
	}
	return &assetLibraryCreateAssetResult{
		AssetID: strconv.FormatInt(result.ID, 10),
		GroupID: groupReplica.UpstreamGroupId,
		Status:  "Pending",
	}, nil
}

func (openAPIAssetLibraryBackend) UpdateGroup(ctx context.Context, config *model.ChannelAssetConfig, group *model.UserAssetGroup, upstreamGroupId string) error {
	groupID, err := parseOpenAPIAssetID(upstreamGroupId, "group")
	if err != nil {
		return err
	}
	request := openAPIAssetGroupUpdateRequest{
		ID:          groupID,
		GroupName:   &group.Name,
		Description: &group.Description,
	}
	return callOpenAPIAssetLibrary(ctx, config, "/openapi/v1/asset/group/update", request, nil)
}

func (openAPIAssetLibraryBackend) UpdateAsset(ctx context.Context, config *model.ChannelAssetConfig, asset *model.UserAsset, upstreamAssetId string) error {
	assetID, err := parseOpenAPIAssetID(upstreamAssetId, "asset")
	if err != nil {
		return err
	}
	request := openAPIAssetUpdateRequest{ID: assetID, AssetName: asset.Name}
	return callOpenAPIAssetLibrary(ctx, config, "/openapi/v1/asset/update", request, nil)
}

func (openAPIAssetLibraryBackend) DeleteGroup(context.Context, *model.ChannelAssetConfig, string) error {
	// Asset OpenAPI has no group-delete endpoint. Assets are deleted through
	// their own endpoint before the logical group and replica records are removed.
	return nil
}

func (openAPIAssetLibraryBackend) DeleteAsset(ctx context.Context, config *model.ChannelAssetConfig, upstreamAssetId string) error {
	assetID, err := parseOpenAPIAssetID(upstreamAssetId, "asset")
	if err != nil {
		return err
	}
	return callOpenAPIAssetLibrary(ctx, config, "/openapi/v1/asset/delete", openAPIAssetIDRequest{ID: assetID}, nil)
}

func (openAPIAssetLibraryBackend) GetAsset(ctx context.Context, config *model.ChannelAssetConfig, upstreamAssetId string) (*AssetLibraryAssetDetails, error) {
	assetID, err := parseOpenAPIAssetID(upstreamAssetId, "asset")
	if err != nil {
		return nil, err
	}
	var result struct {
		Asset openAPIAssetData `json:"asset"`
	}
	if err := callOpenAPIAssetLibrary(ctx, config, "/openapi/v1/asset/get", openAPIAssetIDRequest{ID: assetID}, &result); err != nil {
		return nil, err
	}
	details := &AssetLibraryAssetDetails{
		Id:          strconv.FormatInt(result.Asset.ID, 10),
		Name:        result.Asset.AssetName,
		URL:         result.Asset.URL,
		GroupId:     strconv.FormatInt(result.Asset.GroupID, 10),
		AssetType:   openAPIAssetTypeName(result.Asset.AssetType),
		Status:      openAPIAssetSyncStatus(result.Asset.SyncStatus),
		ProjectName: assetLibraryProject(config),
		CreateTime:  result.Asset.CreatedAt,
		UpdateTime:  result.Asset.UpdatedAt,
	}
	if result.Asset.SyncStatus == 3 {
		details.Error = &dto.AssetLibraryError{
			Code:    "AssetSyncFailed",
			Message: common.MaskSensitiveInfo(common.LocalLogPreview(result.Asset.SyncError)),
		}
	}
	return details, nil
}

func (openAPIAssetLibraryBackend) FormatAssetReference(upstreamAssetId string) string {
	return "asset:local:" + upstreamAssetId
}

type openAPIAssetResponseEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	TraceID string          `json:"trace_id"`
}

func callOpenAPIAssetLibrary(ctx context.Context, config *model.ChannelAssetConfig, path string, input any, output any) error {
	if config == nil || !config.Enabled {
		return errors.New("asset library is not enabled for channel")
	}
	if config.AuthType != AssetLibraryAuthBearer {
		return errors.New("asset OpenAPI requires Bearer authentication")
	}
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" {
		return errors.New("asset library API key is empty")
	}
	endpoint, err := openAPIAssetLibraryURL(config.BaseURL, path)
	if err != nil {
		return err
	}
	body, err := common.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
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
	var envelope openAPIAssetResponseEnvelope
	if err := common.Unmarshal(responseBody, &envelope); err != nil {
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return &AssetLibraryUpstreamError{StatusCode: response.StatusCode, Message: http.StatusText(response.StatusCode)}
		}
		return fmt.Errorf("decode asset OpenAPI response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || envelope.Code != 0 {
		message := common.MaskSensitiveInfo(common.LocalLogPreview(envelope.Message))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return &AssetLibraryUpstreamError{
			StatusCode: response.StatusCode,
			Code:       strconv.Itoa(envelope.Code),
			Message:    message,
		}
	}
	if output == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("asset OpenAPI response is missing data")
	}
	return common.Unmarshal(envelope.Data, output)
}

func openAPIAssetLibraryURL(baseURL string, path string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", errors.New("asset OpenAPI base URL is empty")
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", errors.New("invalid asset OpenAPI base URL")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.TrimLeft(path, "/")
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

func parseOpenAPIAssetID(value string, kind string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid asset OpenAPI %s id %q", kind, value)
	}
	return id, nil
}

func openAPIAssetTypeValue(assetType string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(assetType)) {
	case "image":
		return 1, nil
	case "video":
		return 2, nil
	case "audio":
		return 3, nil
	default:
		return 0, fmt.Errorf("unsupported asset OpenAPI asset type %q", assetType)
	}
}

func openAPIAssetTypeName(assetType int) string {
	switch assetType {
	case 1:
		return "Image"
	case 2:
		return "Video"
	case 3:
		return "Audio"
	default:
		return ""
	}
}

func openAPIAssetSyncStatus(syncStatus int) string {
	switch syncStatus {
	case 0:
		return "Pending"
	case 1:
		return "Syncing"
	case 2:
		return "Active"
	case 3:
		return "Failed"
	default:
		return "Processing"
	}
}

// assetURLIsPubliclyReachable reports whether an asset URL can be downloaded
// by a remote service. Localhost and loopback addresses are not reachable.
func assetURLIsPubliclyReachable(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.") {
		return false
	}
	if strings.HasPrefix(host, "192.168.") || strings.HasPrefix(host, "10.") || strings.HasPrefix(host, "172.") {
		// 172.16-31.x.x is private; 172.1-15.x.x and 172.32+.x.x are public.
		// Check more precisely for 172.16-31.
		if strings.HasPrefix(host, "172.") {
			parts := strings.SplitN(host, ".", 3)
			if len(parts) >= 2 {
				if n, err := strconv.Atoi(parts[1]); err == nil && n >= 16 && n <= 31 {
					return false
				}
			}
		} else {
			return false
		}
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// uploadAssetFileToUpstream reads the asset file from local storage (or OSS)
// and uploads it to the upstream gateway via POST /api/asset/upload. The
// returned URL is publicly reachable and can be used in CreateAsset.
func uploadAssetFileToUpstream(ctx context.Context, config *model.ChannelAssetConfig, asset *model.UserAsset) (string, error) {
	if config == nil || !config.Enabled {
		return "", errors.New("asset library is not enabled for channel")
	}
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" {
		return "", errors.New("asset library API key is empty")
	}
	reader, err := common.OpenAssetStorageReader(asset.StorageKey)
	if err != nil {
		return "", fmt.Errorf("read asset file: %w", err)
	}
	defer reader.Close()
	endpoint, err := openAPIAssetLibraryURL(config.BaseURL, "/api/asset/upload")
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileName := strings.TrimSpace(asset.Name)
	if fileName == "" {
		fileName = "asset"
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, reader); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+apiKey)
	client := GetHttpClient()
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("upstream upload returned status %d: %s", response.StatusCode, string(responseBody))
	}
	var uploadResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			URL string `json:"Url"`
		} `json:"data"`
	}
	if err := common.Unmarshal(responseBody, &uploadResp); err != nil {
		return "", fmt.Errorf("decode upstream upload response: %w", err)
	}
	if !uploadResp.Success || uploadResp.Data.URL == "" {
		msg := uploadResp.Message
		if msg == "" {
			msg = "upstream returned no URL"
		}
		return "", errors.New(msg)
	}
	return uploadResp.Data.URL, nil
}
