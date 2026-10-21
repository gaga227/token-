package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func shujubaoTestConfig(serverURL string) *model.ChannelAssetConfig {
	return &model.ChannelAssetConfig{
		ChannelId:   21,
		Enabled:     true,
		Backend:     AssetLibraryBackendShujubao,
		BaseURL:     serverURL + DefaultShujubaoAssetLibraryPath,
		AuthType:    AssetLibraryAuthBearer,
		APIKey:      "sjb-test-key",
		ProjectName: DefaultAssetLibraryProject,
	}
}

// TestShujubaoCreateFaceVerificationMapsSessionResponse pins the two fields the
// liveness flow depends on: BytedToken becomes our verification id and H5Link
// becomes the scannable URL handed to the customer.
func TestShujubaoCreateFaceVerificationMapsSessionResponse(t *testing.T) {
	clearProxyEnv(t)
	var capturedAction, capturedVersion, capturedAuth string
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedAction = request.URL.Query().Get("Action")
		capturedVersion = request.URL.Query().Get("Version")
		capturedAuth = request.Header.Get("Authorization")
		raw, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(raw, &capturedBody))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ResponseMetadata":{"RequestId":"req-1","Action":"CreateVisualValidateSession","Version":"2024-01-01","Service":"ark","Region":"cn-beijing"},"Result":{"BytedToken":"vvs-20240514212750-c8f2p","H5Link":"https://ark.example.com/liveness?pl=ticket","CallbackURL":"https://your-domain.example.com/callback"}}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	started := time.Now()
	// This protocol's CallbackURL is the redirect target the end user's browser
	// lands on after finishing the check, so a caller-supplied ReturnUrl maps
	// onto it directly.
	result, err := backend.CreateFaceVerification(
		context.Background(),
		shujubaoTestConfig(server.URL),
		"https://customer.example.com/back",
		"https://gateway.example.com/api/asset-library/face-verification/callback",
	)
	require.NoError(t, err)

	assert.Equal(t, "CreateVisualValidateSession", capturedAction)
	assert.Equal(t, AssetLibraryVersion, capturedVersion)
	assert.Equal(t, "Bearer sjb-test-key", capturedAuth)
	assert.NotContains(t, capturedBody, "return_url")
	// The upstream rejects the call when CallbackURL is missing, so it must
	// always travel with the request — and it must be the caller's ReturnUrl.
	assert.Equal(t, "https://customer.example.com/back", capturedBody["CallbackURL"])

	assert.Equal(t, "vvs-20240514212750-c8f2p", result.VerificationId)
	assert.Equal(t, "https://ark.example.com/liveness?pl=ticket", result.H5URL)
	assert.Equal(t, model.FaceVerificationStatusWaitingUser, result.Status)
	assert.Empty(t, result.UpstreamGroupId)
	// The upstream reports no expiry, so the deadline is ours: without it a
	// rejected applicant would wait forever.
	assert.InDelta(t, started.Add(shujubaoFaceVerificationTTL).Unix(), result.ExpiresAt, 5)
}

// TestShujubaoCreateFaceVerificationFallsBackToGatewayCallback covers callers
// that supply no ReturnUrl: the gateway's own endpoint becomes the redirect
// target, so the landing page stays under our control.
func TestShujubaoCreateFaceVerificationFallsBackToGatewayCallback(t *testing.T) {
	clearProxyEnv(t)
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(raw, &capturedBody))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"Result":{"BytedToken":"vvs-20240514212750-c8f2p","H5Link":"https://ark.example.com/liveness?pl=ticket"}}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	_, err := backend.CreateFaceVerification(
		context.Background(),
		shujubaoTestConfig(server.URL),
		"   ",
		"https://gateway.example.com/api/asset-library/face-verification/callback",
	)
	require.NoError(t, err)
	assert.Equal(t, "https://gateway.example.com/api/asset-library/face-verification/callback", capturedBody["CallbackURL"])
}

func TestShujubaoCreateFaceVerificationRejectsEmptyToken(t *testing.T) {
	clearProxyEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ResponseMetadata":{"RequestId":"req-2"},"Result":{"H5Link":"https://ark.example.com/liveness?pl=ticket"}}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	_, err := backend.CreateFaceVerification(context.Background(), shujubaoTestConfig(server.URL), "", "https://gateway.example.com/cb")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BytedToken")
}

func TestShujubaoCreateFaceVerificationRequiresCallbackURL(t *testing.T) {
	clearProxyEnv(t)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"Result":{"BytedToken":"vvs-1"}}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	// A caller-supplied ReturnUrl now doubles as CallbackURL, so only the case
	// where both are empty fails locally — that keeps the operator from seeing
	// the opaque upstream 502 ("The required parameter CallbackURL is missing").
	_, err := backend.CreateFaceVerification(context.Background(), shujubaoTestConfig(server.URL), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "callback URL")
	assert.False(t, called, "no upstream call is attempted without a callback URL")
}

// TestShujubaoGetFaceVerificationVerifiedWhenGroupPresent pins the binary status
// rule: a GroupId is the only success signal this protocol offers.
func TestShujubaoGetFaceVerificationVerifiedWhenGroupPresent(t *testing.T) {
	clearProxyEnv(t)
	var capturedAction string
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedAction = request.URL.Query().Get("Action")
		raw, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(raw, &capturedBody))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ResponseMetadata":{"RequestId":"req-3","Action":"GetVisualValidateResult"},"Result":{"GroupId":"group-20240514212750-b5e9m"}}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	result, err := backend.GetFaceVerification(context.Background(), shujubaoTestConfig(server.URL), "vvs-20240514212750-c8f2p")
	require.NoError(t, err)

	assert.Equal(t, "GetVisualValidateResult", capturedAction)
	assert.Equal(t, "vvs-20240514212750-c8f2p", capturedBody["BytedToken"])
	assert.Equal(t, model.FaceVerificationStatusVerified, result.Status)
	assert.Equal(t, "group-20240514212750-b5e9m", result.UpstreamGroupId)
	assert.Equal(t, "vvs-20240514212750-c8f2p", result.VerificationId)
}

func TestShujubaoGetFaceVerificationStaysPendingWithoutGroup(t *testing.T) {
	clearProxyEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ResponseMetadata":{"RequestId":"req-4"},"Result":{}}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	result, err := backend.GetFaceVerification(context.Background(), shujubaoTestConfig(server.URL), "vvs-20240514212750-c8f2p")
	require.NoError(t, err)
	assert.Equal(t, model.FaceVerificationStatusWaitingUser, result.Status)
	assert.Empty(t, result.UpstreamGroupId)
}

// TestShujubaoGetFaceVerificationTreatsMissingSessionAsPending covers the
// documented "resource not found" shape: an unfinished session must not surface
// to the caller as a failure, or the customer's poll would show a hard error.
func TestShujubaoGetFaceVerificationTreatsMissingSessionAsPending(t *testing.T) {
	clearProxyEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"ResponseMetadata":{"RequestId":"req-5","Error":{"Code":"NotFound.group_id","Message":"no verified group for this token"}}}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	result, err := backend.GetFaceVerification(context.Background(), shujubaoTestConfig(server.URL), "vvs-20240514212750-c8f2p")
	require.NoError(t, err)
	assert.Equal(t, model.FaceVerificationStatusWaitingUser, result.Status)
}

// TestShujubaoGetFaceVerificationPropagatesServerError keeps genuine outages
// visible instead of masking them as a pending session.
func TestShujubaoGetFaceVerificationPropagatesServerError(t *testing.T) {
	clearProxyEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"code":1,"message":"upstream error"}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	_, err := backend.GetFaceVerification(context.Background(), shujubaoTestConfig(server.URL), "vvs-20240514212750-c8f2p")
	require.Error(t, err)
	var upstreamError *AssetLibraryUpstreamError
	require.ErrorAs(t, err, &upstreamError)
	assert.Equal(t, http.StatusInternalServerError, upstreamError.StatusCode)
}

// TestShujubaoPlatformEnvelopeReasonIsSurfaced pins the platform envelope
// handling: "real person not enabled" must reach the operator instead of
// degrading into a bare "Forbidden".
func TestShujubaoPlatformEnvelopeReasonIsSurfaced(t *testing.T) {
	clearProxyEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"code":1,"message":"real person not enabled"}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	_, err := backend.CreateFaceVerification(context.Background(), shujubaoTestConfig(server.URL), "", "https://gateway.example.com/cb")
	require.Error(t, err)
	var upstreamError *AssetLibraryUpstreamError
	require.ErrorAs(t, err, &upstreamError)
	assert.Equal(t, http.StatusForbidden, upstreamError.StatusCode)
	assert.Equal(t, "1", upstreamError.Code)
	assert.Contains(t, upstreamError.Message, "real person not enabled")
}

// TestShujubaoAssetCrudReusesActionDialect proves the embedded Action
// implementation is actually reached, so the backend only adds liveness on top.
func TestShujubaoAssetCrudReusesActionDialect(t *testing.T) {
	clearProxyEnv(t)
	var capturedAction string
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedAction = request.URL.Query().Get("Action")
		raw, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(raw, &capturedBody))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ResponseMetadata":{"RequestId":"req-6"},"Result":{"Id":"group-created-1"}}`))
	}))
	defer server.Close()

	backend := shujubaoAssetLibraryBackend{}
	group := &model.UserAssetGroup{
		Id:          "group-local-1",
		UserId:      1,
		Name:        "my-group",
		GroupType:   model.UserAssetGroupTypeAIGC,
		ProjectName: DefaultAssetLibraryProject,
	}
	result, err := backend.CreateGroup(context.Background(), shujubaoTestConfig(server.URL), group)
	require.NoError(t, err)
	assert.Equal(t, "CreateAssetGroup", capturedAction)
	assert.Equal(t, "my-group", capturedBody["Name"])
	assert.Equal(t, "group-created-1", result.GroupID)
}

func TestShujubaoBackendRegistration(t *testing.T) {
	assert.True(t, IsSupportedAssetLibraryBackend(AssetLibraryBackendShujubao))
	assert.True(t, AssetLibraryBackendRequiresBearer(AssetLibraryBackendShujubao))
	assert.Equal(t, AssetLibraryAuthBearer, DefaultAssetLibraryBackendAuthType(AssetLibraryBackendShujubao))

	backend, err := assetLibraryBackendByName(AssetLibraryBackendShujubao)
	require.NoError(t, err)
	capability, ok := backend.(faceVerificationBackend)
	require.True(t, ok, "shujubao must expose the liveness capability")
	assert.NotNil(t, capability)

	// Selecting shujubao must not leak into the volcengine backend.
	volcengine, err := assetLibraryBackendByName(AssetLibraryBackendAction)
	require.NoError(t, err)
	_, actionSupportsFace := volcengine.(faceVerificationBackend)
	assert.False(t, actionSupportsFace)

	// The advertised list must stay in sync with what actually resolves, or the
	// validation message would advertise a backend that cannot be built.
	names := SupportedAssetLibraryBackends()
	assert.Contains(t, names, AssetLibraryBackendShujubao)
	for _, name := range names {
		assert.True(t, IsSupportedAssetLibraryBackend(name), name)
		_, resolveErr := assetLibraryBackendByName(name)
		assert.NoError(t, resolveErr, name)
	}
}

func TestShujubaoDefaultBaseURLDerivesAssetPath(t *testing.T) {
	baseURL := "https://tp-api.chinadatapay.com:8000"
	channel := &model.Channel{BaseURL: &baseURL}
	assert.Equal(t,
		"https://tp-api.chinadatapay.com:8000/v1/api/assets/",
		DefaultAssetLibraryBackendBaseURL(AssetLibraryBackendShujubao, channel),
	)

	trailing := "https://tp-api.chinadatapay.com:8000/"
	trimmedChannel := &model.Channel{BaseURL: &trailing}
	assert.Equal(t,
		"https://tp-api.chinadatapay.com:8000/v1/api/assets/",
		DefaultAssetLibraryBackendBaseURL(AssetLibraryBackendShujubao, trimmedChannel),
	)

	// Without a channel base URL nothing can be derived; the operator must type
	// the endpoint instead of silently hitting an upstream default.
	assert.Empty(t, DefaultAssetLibraryBackendBaseURL(AssetLibraryBackendShujubao, &model.Channel{}))
}

func TestFaceVerificationGroupNameKeepsShujubaoTail(t *testing.T) {
	// Every session of the same day shares "vvs-<date>", so the head cannot be
	// used as the name.
	first := FaceVerificationGroupName("vvs-20240514212750-c8f2p")
	second := FaceVerificationGroupName("vvs-20240514213000-zz999")
	assert.Equal(t, "liveness-c8f2p", first)
	assert.Equal(t, "liveness-zz999", second)
	assert.NotEqual(t, first, second)

	// The legacy gateway shape keeps its previous behaviour.
	assert.Equal(t, "liveness-abcd1234", FaceVerificationGroupName("fv_abcd1234deadbeef"))
}

func TestFaceVerificationExpired(t *testing.T) {
	now := time.Now().Unix()
	pending := func(expiresAt int64) *model.UserFaceVerification {
		return &model.UserFaceVerification{
			Status:    model.FaceVerificationStatusWaitingUser,
			ExpiresAt: expiresAt,
		}
	}

	assert.True(t, faceVerificationExpired(pending(now-1)))
	assert.False(t, faceVerificationExpired(pending(now+60)))
	// No recorded deadline means "never expire locally", which keeps backends
	// that report real expiries unchanged.
	assert.False(t, faceVerificationExpired(pending(0)))
	assert.False(t, faceVerificationExpired(&model.UserFaceVerification{
		Status:    model.FaceVerificationStatusVerified,
		ExpiresAt: now - 1,
	}))
	assert.False(t, faceVerificationExpired(nil))
}
