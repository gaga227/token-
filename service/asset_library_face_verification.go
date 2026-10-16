package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// Liveness (face) verification capability.
//
// A liveness verification group is produced by the upstream, never by this
// gateway. The order is therefore the opposite of the regular asset flow:
//
//	regular group:  create locally -> replicate to every enabled channel
//	liveness group: upstream verifies a person -> produces a group -> we claim it
//
// A claimed group owns exactly one channel replica. That single replica is also
// what pins video routing: the existing replica-intersection logic resolves to
// the verifying channel on its own, so no extra routing rule is needed here.

const (
	// gatewayFaceVerificationPath is the upstream REST path of the liveness
	// workflow. It is served from the same origin as the Action asset endpoint.
	gatewayFaceVerificationPath = "/api/seedance/face-verifications"
)

// ErrFaceVerificationNotSupported reports that no enabled channel can host a
// face verification. Callers turn it into an explicit client error instead of
// a generic server failure.
var ErrFaceVerificationNotSupported = errors.New("no enabled asset library channel supports face verification")

// FaceVerificationResult is the normalised upstream verification state.
type FaceVerificationResult struct {
	VerificationId  string
	Status          string
	H5URL           string
	ExpiresAt       int64
	UpstreamGroupId string
}

// faceVerificationBackend is an optional capability an asset library backend
// may implement. Backends without it simply do not support liveness
// verification; no other backend has to change to add this capability.
type faceVerificationBackend interface {
	CreateFaceVerification(ctx context.Context, config *model.ChannelAssetConfig, returnURL string) (*FaceVerificationResult, error)
	GetFaceVerification(ctx context.Context, config *model.ChannelAssetConfig, verificationId string) (*FaceVerificationResult, error)
}

// seedanceGatewayAssetLibraryBackend speaks the Action asset protocol (shared
// with the volcengine backend, so the eight asset methods are reused verbatim)
// and additionally exposes the liveness workflow.
type seedanceGatewayAssetLibraryBackend struct {
	actionAssetLibraryBackend
}

type gatewayFaceVerificationResponse struct {
	VerificationId string `json:"verification_id"`
	Status         string `json:"status"`
	H5URL          string `json:"h5_url"`
	ExpiresAt      int64  `json:"expires_at"`
	GroupId        string `json:"group_id"`
}

type gatewayErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

func (backend seedanceGatewayAssetLibraryBackend) CreateFaceVerification(ctx context.Context, config *model.ChannelAssetConfig, returnURL string) (*FaceVerificationResult, error) {
	payload := map[string]string{}
	if trimmed := strings.TrimSpace(returnURL); trimmed != "" {
		payload["return_url"] = trimmed
	}
	response, err := callGatewayFaceVerification(ctx, config, http.MethodPost, "", payload)
	if err != nil {
		return nil, err
	}
	return response.toFaceVerificationResult()
}

func (backend seedanceGatewayAssetLibraryBackend) GetFaceVerification(ctx context.Context, config *model.ChannelAssetConfig, verificationId string) (*FaceVerificationResult, error) {
	response, err := callGatewayFaceVerification(ctx, config, http.MethodGet, verificationId, nil)
	if err != nil {
		return nil, err
	}
	return response.toFaceVerificationResult()
}

func (response *gatewayFaceVerificationResponse) toFaceVerificationResult() (*FaceVerificationResult, error) {
	if response == nil {
		return nil, errors.New("face verification response is empty")
	}
	verificationId := strings.TrimSpace(response.VerificationId)
	if verificationId == "" {
		return nil, errors.New("face verification response is missing verification_id")
	}
	status := strings.TrimSpace(response.Status)
	if status == "" {
		status = model.FaceVerificationStatusWaitingUser
	}
	return &FaceVerificationResult{
		VerificationId:  verificationId,
		Status:          status,
		H5URL:           strings.TrimSpace(response.H5URL),
		ExpiresAt:       response.ExpiresAt,
		UpstreamGroupId: strings.TrimSpace(response.GroupId),
	}, nil
}

// gatewayFaceVerificationEndpoint derives the liveness endpoint from the asset
// library base URL. The asset endpoint is a full path on its own origin, so the
// verification path is rebuilt from that origin rather than appended blindly.
func gatewayFaceVerificationEndpoint(config *model.ChannelAssetConfig, verificationId string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		return "", errors.New("asset library base URL is required for this backend")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid asset library base URL")
	}
	path := gatewayFaceVerificationPath
	if trimmed := strings.TrimSpace(verificationId); trimmed != "" {
		path += "/" + url.PathEscape(trimmed)
	}
	return parsed.Scheme + "://" + parsed.Host + path, nil
}

func callGatewayFaceVerification(ctx context.Context, config *model.ChannelAssetConfig, method string, verificationId string, payload any) (*gatewayFaceVerificationResponse, error) {
	if config == nil || !config.Enabled {
		return nil, errors.New("asset library is not enabled for channel")
	}
	if config.AuthType != AssetLibraryAuthBearer {
		return nil, fmt.Errorf("face verification requires %s authentication", AssetLibraryAuthBearer)
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("asset library API key is empty")
	}
	endpoint, err := gatewayFaceVerificationEndpoint(config, verificationId)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		encoded, marshalErr := common.Marshal(payload)
		if marshalErr != nil {
			return nil, marshalErr
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+config.APIKey)
	client := GetHttpClient()
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var errorBody gatewayErrorResponse
		if unmarshalErr := common.Unmarshal(responseBody, &errorBody); unmarshalErr == nil {
			message := errorBody.Message
			if message == "" {
				message = errorBody.Error
			}
			if errorBody.Code != "" || message != "" {
				return nil, &AssetLibraryUpstreamError{
					StatusCode: response.StatusCode,
					Code:       errorBody.Code,
					Message:    common.MaskSensitiveInfo(common.LocalLogPreview(message)),
				}
			}
		}
		return nil, &AssetLibraryUpstreamError{StatusCode: response.StatusCode, Message: http.StatusText(response.StatusCode)}
	}
	var decoded gatewayFaceVerificationResponse
	if err := common.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode face verification response: %w", err)
	}
	if strings.TrimSpace(decoded.VerificationId) == "" && strings.TrimSpace(decoded.Status) == "" {
		// Tolerate an upstream that wraps the payload in a "data" envelope.
		var wrapped struct {
			Data gatewayFaceVerificationResponse `json:"data"`
		}
		if err := common.Unmarshal(responseBody, &wrapped); err == nil {
			decoded = wrapped.Data
		}
	}
	return &decoded, nil
}

// faceVerificationBackendForConfig returns the capability for a channel, or nil
// when the channel is healthy but simply does not implement it.
func faceVerificationBackendForConfig(config *model.ChannelAssetConfig) (faceVerificationBackend, error) {
	if config == nil || !config.Enabled {
		return nil, nil
	}
	backend, err := effectiveAssetLibraryBackend(config.Backend, config.ChannelId)
	if err != nil {
		return nil, err
	}
	implementation, err := assetLibraryBackendByName(backend)
	if err != nil {
		return nil, err
	}
	capability, ok := implementation.(faceVerificationBackend)
	if !ok {
		return nil, nil
	}
	return capability, nil
}

type faceVerificationChannelCandidate struct {
	config     *model.ChannelAssetConfig
	priority   int64
	groupMatch bool
}

// SelectFaceVerificationChannel picks the channel that will host a new face
// verification. Channels without the capability are skipped silently, so the
// presence of unrelated asset library channels never surfaces as an error.
// Channels matching the caller's group win; otherwise the pool falls back to
// every capable channel, matching how asset replication already behaves.
// Selection is deterministic (highest priority, then lowest channel id) so the
// same caller keeps landing on the same upstream.
func SelectFaceVerificationChannel(userGroup string) (*model.ChannelAssetConfig, error) {
	configs, err := model.GetEnabledChannelAssetConfigs()
	if err != nil {
		return nil, err
	}
	candidates := make([]faceVerificationChannelCandidate, 0, len(configs))
	for i := range configs {
		capability, capabilityErr := faceVerificationBackendForConfig(&configs[i])
		if capabilityErr != nil || capability == nil {
			continue
		}
		channel, channelErr := model.GetChannelById(configs[i].ChannelId, false)
		if channelErr != nil || channel == nil {
			continue
		}
		if channel.Status != common.ChannelStatusEnabled {
			continue
		}
		candidates = append(candidates, faceVerificationChannelCandidate{
			config:     &configs[i],
			priority:   channelPriority(channel),
			groupMatch: channelGroupMatches(channel.Group, userGroup),
		})
	}
	if len(candidates) == 0 {
		return nil, ErrFaceVerificationNotSupported
	}
	matched := make([]faceVerificationChannelCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.groupMatch {
			matched = append(matched, candidate)
		}
	}
	pool := matched
	if len(pool) == 0 {
		pool = candidates
	}
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].priority != pool[j].priority {
			return pool[i].priority > pool[j].priority
		}
		return pool[i].config.ChannelId < pool[j].config.ChannelId
	})
	return pool[0].config, nil
}

func channelPriority(channel *model.Channel) int64 {
	if channel == nil || channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func channelGroupMatches(channelGroup string, userGroup string) bool {
	userGroup = strings.TrimSpace(userGroup)
	if userGroup == "" {
		return true
	}
	for _, group := range strings.Split(channelGroup, ",") {
		if strings.TrimSpace(group) == userGroup {
			return true
		}
	}
	return false
}

// StartFaceVerification creates an upstream verification task for a user. The
// channel is chosen by the gateway so callers never see or pass a channel id.
func StartFaceVerification(ctx context.Context, userId int, userGroup string, returnURL string) (*model.UserFaceVerification, error) {
	config, err := SelectFaceVerificationChannel(userGroup)
	if err != nil {
		return nil, err
	}
	backend, err := faceVerificationBackendForConfig(config)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, ErrFaceVerificationNotSupported
	}
	result, err := backend.CreateFaceVerification(ctx, config, returnURL)
	if err != nil {
		return nil, err
	}
	verification := &model.UserFaceVerification{
		UserId:         userId,
		ChannelId:      config.ChannelId,
		VerificationId: result.VerificationId,
		Status:         result.Status,
		H5URL:          result.H5URL,
		ExpiresAt:      result.ExpiresAt,
		ReturnURL:      strings.TrimSpace(returnURL),
	}
	if err := model.CreateUserFaceVerification(verification); err != nil {
		return nil, err
	}
	return verification, nil
}

// SyncFaceVerification refreshes the stored state from upstream and claims the
// group once the person is verified. Terminal states never hit the network
// twice.
func SyncFaceVerification(ctx context.Context, verification *model.UserFaceVerification) (*model.UserFaceVerification, error) {
	if verification == nil {
		return nil, errors.New("face verification is nil")
	}
	if model.IsFaceVerificationTerminal(verification.Status) {
		return verification, nil
	}
	config, err := model.GetChannelAssetConfig(verification.ChannelId)
	if err != nil {
		return nil, err
	}
	backend, err := faceVerificationBackendForConfig(config)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, ErrFaceVerificationNotSupported
	}
	result, err := backend.GetFaceVerification(ctx, config, verification.VerificationId)
	if err != nil {
		return nil, err
	}
	if err := ApplyFaceVerificationResult(verification, result); err != nil {
		return nil, err
	}
	return verification, nil
}

// ApplyFaceVerificationResult stores the upstream state and, on success, claims
// the upstream group as a local logical asset group.
func ApplyFaceVerificationResult(verification *model.UserFaceVerification, result *FaceVerificationResult) error {
	if verification == nil || result == nil {
		return errors.New("face verification state is nil")
	}
	verification.Status = result.Status
	if result.H5URL != "" {
		verification.H5URL = result.H5URL
	}
	if result.ExpiresAt > 0 {
		verification.ExpiresAt = result.ExpiresAt
	}
	switch result.Status {
	case model.FaceVerificationStatusVerified:
		if strings.TrimSpace(result.UpstreamGroupId) == "" {
			break
		}
		verification.UpstreamGroupId = result.UpstreamGroupId
		group, err := ClaimFaceVerificationGroup(verification, result.UpstreamGroupId)
		if err != nil {
			return err
		}
		verification.GroupId = group.Id
		verification.LastError = ""
	case model.FaceVerificationStatusFailed:
		verification.LastError = "upstream reported the face verification as failed"
	}
	return model.UpdateUserFaceVerification(verification)
}

// ClaimFaceVerificationGroup turns a verified upstream group into a local
// logical asset group that owns exactly one channel replica. Repeated calls
// return the already claimed group, so polling stays idempotent.
func ClaimFaceVerificationGroup(verification *model.UserFaceVerification, upstreamGroupId string) (*model.UserAssetGroup, error) {
	if verification == nil {
		return nil, errors.New("face verification is nil")
	}
	upstreamGroupId = strings.TrimSpace(upstreamGroupId)
	if upstreamGroupId == "" {
		return nil, errors.New("upstream group id is empty")
	}
	if claimed := strings.TrimSpace(verification.GroupId); claimed != "" {
		existing, err := model.GetUserAssetGroup(verification.UserId, claimed)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		// The claimed group was deleted afterwards; claim it again so the
		// verification remains usable.
	}
	group := &model.UserAssetGroup{
		Id:          "group-na-" + common.GetUUID(),
		UserId:      verification.UserId,
		Name:        FaceVerificationGroupName(verification.VerificationId),
		GroupType:   model.UserAssetGroupTypeLivenessFace,
		Origin:      model.UserAssetGroupOriginLiveness,
		ProjectName: DefaultAssetLibraryProject,
	}
	if err := model.CreateUserAssetGroup(group); err != nil {
		return nil, err
	}
	replica := &model.UserAssetGroupReplica{
		GroupId:         group.Id,
		ChannelId:       verification.ChannelId,
		UpstreamGroupId: upstreamGroupId,
		State:           model.AssetReplicaStateReady,
	}
	if err := model.SaveUserAssetGroupReplica(replica); err != nil {
		return nil, err
	}
	return group, nil
}

// FaceVerificationGroupName derives a stable, language-neutral group name from
// the upstream verification id. Operators can rename it afterwards.
func FaceVerificationGroupName(verificationId string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(verificationId), "fv_")
	if len(trimmed) > 8 {
		trimmed = trimmed[:8]
	}
	if trimmed == "" {
		trimmed = common.GetUUID()
		if len(trimmed) > 8 {
			trimmed = trimmed[:8]
		}
	}
	return "liveness-" + trimmed
}
