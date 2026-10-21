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
	"time"

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
//
// callbackURL is the gateway endpoint the upstream is asked to notify. Some
// protocols require it (and reject the call without it); backends that do not
// support callbacks ignore it. The notification is never trusted as the source
// of truth: it only triggers a lookup upstream.
type faceVerificationBackend interface {
	CreateFaceVerification(ctx context.Context, config *model.ChannelAssetConfig, returnURL string, callbackURL string) (*FaceVerificationResult, error)
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

func (backend seedanceGatewayAssetLibraryBackend) CreateFaceVerification(ctx context.Context, config *model.ChannelAssetConfig, returnURL string, callbackURL string) (*FaceVerificationResult, error) {
	payload := map[string]string{}
	if trimmed := strings.TrimSpace(returnURL); trimmed != "" {
		payload["return_url"] = trimmed
	}
	// This protocol pushes its result to return_url/its own callback slot, so a
	// gateway-side callback is accepted but not forwarded.
	_ = callbackURL
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

// shujubaoAssetLibraryBackend is the 数据宝 asset library protocol. Asset CRUD
// reuses the Action implementation verbatim (embedded, never copied), while the
// liveness workflow is driven by two Action calls instead of the REST endpoints
// the seedance gateway exposes.
type shujubaoAssetLibraryBackend struct {
	actionAssetLibraryBackend
}

// shujubaoFaceVerificationTTL bounds how long a pending liveness session stays
// alive. The upstream reports no expiry and cannot separate "not scanned yet"
// from "rejected" (both simply carry no GroupId), so the gateway applies its own
// deadline. Without it a rejected applicant would wait forever.
const shujubaoFaceVerificationTTL = 15 * time.Minute

// shujubaoVisualSessionResponse is the Result payload of
// CreateVisualValidateSession.
type shujubaoVisualSessionResponse struct {
	BytedToken string `json:"BytedToken"`
	H5Link     string `json:"H5Link"`
}

// shujubaoValidateResultResponse is the Result payload of
// GetVisualValidateResult. A non-empty GroupId is the only success signal the
// protocol offers.
type shujubaoValidateResultResponse struct {
	GroupId string `json:"GroupId"`
}

func (backend shujubaoAssetLibraryBackend) CreateFaceVerification(ctx context.Context, config *model.ChannelAssetConfig, returnURL string, callbackURL string) (*FaceVerificationResult, error) {
	// returnURL is ignored: this protocol only pushes to CallbackURL, and the
	// gateway polls instead of honouring a browser redirect.
	_ = returnURL
	trimmedCallback := strings.TrimSpace(callbackURL)
	if trimmedCallback == "" {
		// The upstream rejects the call without it ("The required parameter
		// CallbackURL is missing"), so fail loudly here instead of surfacing an
		// opaque 502 upstream error.
		return nil, errors.New("face verification requires a callback URL for this backend")
	}
	var result shujubaoVisualSessionResponse
	payload := map[string]string{"CallbackURL": trimmedCallback}
	if err := CallAssetLibraryUpstream(ctx, config, "CreateVisualValidateSession", payload, &result); err != nil {
		return nil, err
	}
	verificationId := strings.TrimSpace(result.BytedToken)
	if verificationId == "" {
		return nil, errors.New("visual validate session response is missing BytedToken")
	}
	return &FaceVerificationResult{
		VerificationId: verificationId,
		Status:         model.FaceVerificationStatusWaitingUser,
		H5URL:          strings.TrimSpace(result.H5Link),
		ExpiresAt:      time.Now().Add(shujubaoFaceVerificationTTL).Unix(),
	}, nil
}

func (backend shujubaoAssetLibraryBackend) GetFaceVerification(ctx context.Context, config *model.ChannelAssetConfig, verificationId string) (*FaceVerificationResult, error) {
	trimmed := strings.TrimSpace(verificationId)
	if trimmed == "" {
		return nil, errors.New("verification id is empty")
	}
	request := map[string]string{"BytedToken": trimmed}
	var result shujubaoValidateResultResponse
	if err := CallAssetLibraryUpstream(ctx, config, "GetVisualValidateResult", request, &result); err != nil {
		// The protocol has no "pending" status: it either returns a GroupId or
		// reports the session as missing. Missing means "still waiting", so it
		// must not surface to the caller as a failure.
		if isShujubaoPendingError(err) {
			return &FaceVerificationResult{
				VerificationId: trimmed,
				Status:         model.FaceVerificationStatusWaitingUser,
			}, nil
		}
		return nil, err
	}
	groupID := strings.TrimSpace(result.GroupId)
	status := model.FaceVerificationStatusWaitingUser
	if groupID != "" {
		status = model.FaceVerificationStatusVerified
	}
	return &FaceVerificationResult{
		VerificationId:  trimmed,
		Status:          status,
		UpstreamGroupId: groupID,
	}, nil
}

// isShujubaoPendingError reports whether an upstream error means "the person has
// not finished yet" rather than a real failure. Everything else stays an error
// so genuine outages remain visible.
func isShujubaoPendingError(err error) bool {
	var upstreamErr *AssetLibraryUpstreamError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	if upstreamErr.StatusCode == http.StatusNotFound {
		return true
	}
	code := strings.ToLower(strings.TrimSpace(upstreamErr.Code))
	return strings.HasPrefix(code, "notfound.")
}

// faceVerificationExpired reports whether a still-pending session is past its
// locally recorded deadline. Sessions without a deadline are never expired
// here, so backends that do report real expiries keep their previous behaviour.
func faceVerificationExpired(verification *model.UserFaceVerification) bool {
	if verification == nil || verification.ExpiresAt <= 0 {
		return false
	}
	if verification.Status != model.FaceVerificationStatusWaitingUser {
		return false
	}
	return time.Now().Unix() >= verification.ExpiresAt
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
// callbackURL is the gateway endpoint registered with the upstream so the
// result can also push back; polling stays the primary channel.
func StartFaceVerification(ctx context.Context, userId int, userGroup string, returnURL string, callbackURL string) (*model.UserFaceVerification, error) {
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
	result, err := backend.CreateFaceVerification(ctx, config, returnURL, callbackURL)
	if err != nil {
		return nil, err
	}
	// Logged so an operator can confirm the registered callback matches the
	// public deployment (a wrong host silently loses the notification).
	common.SysLog(fmt.Sprintf(
		"face verification started: user=%d channel=%d callback=%s",
		userId,
		config.ChannelId,
		strings.TrimSpace(callbackURL),
	))
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
	// Expire locally before hitting the network: a rejected applicant is
	// indistinguishable from one who never scanned, so the deadline is what ends
	// the wait. Nothing is polled after this point.
	if faceVerificationExpired(verification) {
		verification.Status = model.FaceVerificationStatusExpired
		verification.LastError = "liveness session expired before the person completed verification"
		if err := model.UpdateUserFaceVerification(verification); err != nil {
			return nil, err
		}
		return verification, nil
	}
	return fetchFaceVerification(ctx, verification)
}

// fetchFaceVerification always asks the upstream for the current state and
// applies it. It is shared by the polling path (after the local deadline gate)
// and by the callback path, which must not be blocked by that deadline: a
// notification that arrives seconds after the window closed still carries a
// successful verification the person really completed.
func fetchFaceVerification(ctx context.Context, verification *model.UserFaceVerification) (*model.UserFaceVerification, error) {
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

// faceVerificationSuccessResultCode is the upstream resultCode that means "the
// liveness check passed". Anything else non-empty is a rejection.
const faceVerificationSuccessResultCode = "10000"

// FaceVerificationCallbackParams is the upstream notification, normalised. The
// upstream appends these as query parameters on the registered CallbackURL, so
// every field is a plain string.
type FaceVerificationCallbackParams struct {
	// VerificationId comes from bytedToken: the same handle returned by
	// CreateVisualValidateSession as BytedToken.
	VerificationId        string
	ResultCode            string
	AlgorithmBaseRespCode string
	MeasureInfoValue      string
	VerifyType            string
}

// ParseFaceVerificationCallback maps the upstream parameter names onto our own.
// Unknown extras are ignored so a future upstream field cannot break the flow.
func ParseFaceVerificationCallback(values url.Values) FaceVerificationCallbackParams {
	return FaceVerificationCallbackParams{
		VerificationId:        firstCallbackValue(values, "bytedToken", "BytedToken", "byted_token"),
		ResultCode:            firstCallbackValue(values, "resultCode", "result_code"),
		AlgorithmBaseRespCode: firstCallbackValue(values, "algorithmBaseRespCode", "algorithm_base_resp_code"),
		MeasureInfoValue:      firstCallbackValue(values, "reqMeasureInfoValue", "req_measure_info_value"),
		VerifyType:            firstCallbackValue(values, "verify_type", "verifyType"),
	}
}

func firstCallbackValue(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

// callbackJSONAliases maps our canonical parameter names onto every spelling an
// upstream variant may use in a JSON body.
var callbackJSONAliases = map[string][]string{
	"bytedToken":            {"bytedToken", "BytedToken", "byted_token"},
	"resultCode":            {"resultCode", "result_code"},
	"algorithmBaseRespCode": {"algorithmBaseRespCode", "algorithm_base_resp_code"},
	"reqMeasureInfoValue":   {"reqMeasureInfoValue", "req_measure_info_value"},
	"verify_type":           {"verify_type", "verifyType"},
}

// ParseFaceVerificationCallbackJSON recovers the notification parameters from a
// body of unknown shape. The documented transport is a query string; this is a
// safety net for a JSON variant, so an unexpected framing cannot drop the
// result on the floor. Only the handle has to be recoverable, hence the raw
// scan for the upstream token prefix.
func ParseFaceVerificationCallbackJSON(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var payload any
	if err := common.Unmarshal(raw, &payload); err != nil {
		if token := firstVerificationTokenInText(string(raw)); token != "" {
			out["bytedToken"] = token
		}
		return out
	}
	walkCallbackJSON(payload, func(key string, value string) {
		if strings.HasPrefix(value, "vvs-") && out["bytedToken"] == "" {
			out["bytedToken"] = value
		}
		for canonical, names := range callbackJSONAliases {
			if out[canonical] != "" {
				continue
			}
			for _, name := range names {
				if key == name {
					out[canonical] = value
					break
				}
			}
		}
	})
	return out
}

func walkCallbackJSON(node any, visit func(key string, value string)) {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if text, ok := value.(string); ok {
				visit(key, text)
				continue
			}
			walkCallbackJSON(value, visit)
		}
	case []any:
		for _, item := range typed {
			walkCallbackJSON(item, visit)
		}
	}
}

func firstVerificationTokenInText(text string) string {
	index := strings.Index(text, "vvs-")
	if index < 0 {
		return ""
	}
	rest := text[index:]
	end := 0
	for end < len(rest) && isVerificationTokenChar(rest[end]) {
		end++
	}
	return rest[:end]
}

func isVerificationTokenChar(c byte) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c == '-':
		return true
	default:
		return false
	}
}

// ApplyFaceVerificationCallback turns a notification into state. The payload is
// never trusted on its own: this endpoint is public, so a forged "success"
// would otherwise hand out a liveness group for free. The resultCode is only
// used to detect a rejection early (no upstream call needed); a reported
// success is always confirmed against GetVisualValidateResult.
func ApplyFaceVerificationCallback(ctx context.Context, params FaceVerificationCallbackParams) error {
	verificationId := strings.TrimSpace(params.VerificationId)
	if verificationId == "" {
		return errors.New("face verification callback is missing bytedToken")
	}
	verification, err := model.GetUserFaceVerificationByVerificationId(verificationId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Unknown handle: either a forged request or a session we already
			// dropped. Nothing to do, and nothing to leak.
			return nil
		}
		return err
	}
	if model.IsFaceVerificationTerminal(verification.Status) {
		return nil
	}
	resultCode := strings.TrimSpace(params.ResultCode)
	if resultCode != "" && resultCode != faceVerificationSuccessResultCode {
		verification.Status = model.FaceVerificationStatusFailed
		verification.LastError = fmt.Sprintf(
			"upstream reported the liveness check as failed: resultCode=%s algorithmBaseRespCode=%s",
			resultCode,
			strings.TrimSpace(params.AlgorithmBaseRespCode),
		)
		return model.UpdateUserFaceVerification(verification)
	}
	// Record the notification before resolving so a client polling right now
	// sees progress instead of a stale waiting_user.
	verification.Status = model.FaceVerificationStatusCallbackReceived
	if err := model.UpdateUserFaceVerification(verification); err != nil {
		return err
	}
	updated, err := fetchFaceVerification(ctx, verification)
	if err != nil {
		return err
	}
	if updated == nil {
		return nil
	}
	if updated.Status == model.FaceVerificationStatusWaitingUser {
		// The upstream has not published the group yet. Keep the notification
		// state; the next poll retries.
		updated.Status = model.FaceVerificationStatusCallbackReceived
		updated.LastError = ""
		return model.UpdateUserFaceVerification(updated)
	}
	return nil
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
	id := strings.TrimSpace(verificationId)
	// 数据宝 tokens look like "vvs-20240514212750-c8f2p": every session shares
	// the same timestamp head, so only the tail tells them apart. Truncating the
	// head would name every group of that day identically.
	if strings.HasPrefix(id, "vvs-") {
		if idx := strings.LastIndex(id, "-"); idx >= 0 && idx < len(id)-1 {
			if tail := strings.TrimSpace(id[idx+1:]); tail != "" {
				return "liveness-" + tail
			}
		}
	}
	trimmed := strings.TrimPrefix(id, "fv_")
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
