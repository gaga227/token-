package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(name, "")
	}
}

func faceVerificationTestConfig(serverURL string) *model.ChannelAssetConfig {
	return &model.ChannelAssetConfig{
		ChannelId:   10,
		Enabled:     true,
		Backend:     AssetLibraryBackendSeedanceGateway,
		BaseURL:     serverURL + "/api/seedance/proxy/openapi",
		AuthType:    AssetLibraryAuthBearer,
		APIKey:      "sk-face-test",
		Region:      DefaultAssetLibraryRegion,
		ProjectName: DefaultAssetLibraryProject,
	}
}

func TestCreateFaceVerificationUsesGatewayOriginAndBearerAuth(t *testing.T) {
	clearProxyEnv(t)
	var capturedPath, capturedAuth string
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedPath = request.URL.Path
		capturedAuth = request.Header.Get("Authorization")
		raw, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(raw, &capturedBody))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"verification_id":"fv_test01","status":"waiting_user","h5_url":"https://h5.example.com/verify","expires_at":1783740000}`))
	}))
	defer server.Close()

	backend := seedanceGatewayAssetLibraryBackend{}
	result, err := backend.CreateFaceVerification(context.Background(), faceVerificationTestConfig(server.URL), "https://customer.example.com/back")
	require.NoError(t, err)
	// The verification path is rebuilt from the asset endpoint origin, not
	// appended to the asset path.
	assert.Equal(t, "/api/seedance/face-verifications", capturedPath)
	assert.Equal(t, "Bearer sk-face-test", capturedAuth)
	assert.Equal(t, "https://customer.example.com/back", capturedBody["return_url"])
	assert.Equal(t, "fv_test01", result.VerificationId)
	assert.Equal(t, model.FaceVerificationStatusWaitingUser, result.Status)
	assert.Equal(t, "https://h5.example.com/verify", result.H5URL)
	assert.Equal(t, int64(1783740000), result.ExpiresAt)
	assert.Empty(t, result.UpstreamGroupId)
}

func TestCreateFaceVerificationOmitsEmptyReturnURL(t *testing.T) {
	clearProxyEnv(t)
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(raw, &capturedBody))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"verification_id":"fv_test02","status":"waiting_user","h5_url":"https://h5.example.com/verify"}`))
	}))
	defer server.Close()

	backend := seedanceGatewayAssetLibraryBackend{}
	result, err := backend.CreateFaceVerification(context.Background(), faceVerificationTestConfig(server.URL), "  ")
	require.NoError(t, err)
	assert.NotContains(t, capturedBody, "return_url")
	assert.Equal(t, "fv_test02", result.VerificationId)
}

func TestGetFaceVerificationReadsUpstreamGroupWhenVerified(t *testing.T) {
	clearProxyEnv(t)
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedPath = request.URL.Path
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"verification_id":"fv_test03","status":"verified","group_id":"group-upstream-1","expires_at":1783740000}`))
	}))
	defer server.Close()

	backend := seedanceGatewayAssetLibraryBackend{}
	result, err := backend.GetFaceVerification(context.Background(), faceVerificationTestConfig(server.URL), "fv_test03")
	require.NoError(t, err)
	assert.Equal(t, "/api/seedance/face-verifications/fv_test03", capturedPath)
	assert.Equal(t, model.FaceVerificationStatusVerified, result.Status)
	assert.Equal(t, "group-upstream-1", result.UpstreamGroupId)
}

func TestGetFaceVerificationMapsUpstreamError(t *testing.T) {
	clearProxyEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"code":"not_found","message":"verification not found"}`))
	}))
	defer server.Close()

	backend := seedanceGatewayAssetLibraryBackend{}
	_, err := backend.GetFaceVerification(context.Background(), faceVerificationTestConfig(server.URL), "fv_missing")
	require.Error(t, err)
	var upstreamError *AssetLibraryUpstreamError
	require.ErrorAs(t, err, &upstreamError)
	assert.Equal(t, http.StatusNotFound, upstreamError.StatusCode)
	assert.Equal(t, "not_found", upstreamError.Code)
}

func TestFaceVerificationRequiresBearerAuthAndAPIKey(t *testing.T) {
	clearProxyEnv(t)
	backend := seedanceGatewayAssetLibraryBackend{}

	akskConfig := faceVerificationTestConfig("https://gateway.example.com")
	akskConfig.AuthType = AssetLibraryAuthAKSK
	_, err := backend.CreateFaceVerification(context.Background(), akskConfig, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), AssetLibraryAuthBearer)

	noKeyConfig := faceVerificationTestConfig("https://gateway.example.com")
	noKeyConfig.APIKey = ""
	_, err = backend.CreateFaceVerification(context.Background(), noKeyConfig, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API key")
}

func TestFaceVerificationRequiresAssetBaseURL(t *testing.T) {
	backend := seedanceGatewayAssetLibraryBackend{}
	config := faceVerificationTestConfig("https://gateway.example.com")
	config.BaseURL = ""
	_, err := backend.CreateFaceVerification(context.Background(), config, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base URL")
}

func TestApplyFaceVerificationResultClaimsGroupOnce(t *testing.T) {
	db := setupAssetLibraryServiceTestDB(t)
	verification := &model.UserFaceVerification{
		UserId:         1,
		ChannelId:      10,
		VerificationId: "fv_claim01",
		Status:         model.FaceVerificationStatusWaitingUser,
	}
	require.NoError(t, model.CreateUserFaceVerification(verification))

	require.NoError(t, ApplyFaceVerificationResult(verification, &FaceVerificationResult{
		VerificationId:  "fv_claim01",
		Status:          model.FaceVerificationStatusVerified,
		UpstreamGroupId: "group-upstream-1",
	}))
	claimedGroupId := verification.GroupId
	require.NotEmpty(t, claimedGroupId)

	group, err := model.GetUserAssetGroup(1, claimedGroupId)
	require.NoError(t, err)
	assert.Equal(t, model.UserAssetGroupTypeLivenessFace, group.GroupType)
	assert.Equal(t, model.UserAssetGroupOriginLiveness, group.Origin)

	replicas, err := model.ListUserAssetGroupReplicas(claimedGroupId)
	require.NoError(t, err)
	require.Len(t, replicas, 1)
	assert.Equal(t, 10, replicas[0].ChannelId)
	assert.Equal(t, "group-upstream-1", replicas[0].UpstreamGroupId)
	assert.Equal(t, model.AssetReplicaStateReady, replicas[0].State)

	// Polling again must not create a second group.
	require.NoError(t, ApplyFaceVerificationResult(verification, &FaceVerificationResult{
		VerificationId:  "fv_claim01",
		Status:          model.FaceVerificationStatusVerified,
		UpstreamGroupId: "group-upstream-1",
	}))
	assert.Equal(t, claimedGroupId, verification.GroupId)
	var count int64
	require.NoError(t, db.Model(&model.UserAssetGroup{}).Where("user_id = ?", 1).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestClaimedGroupIsNotReplicatedToOtherChannels(t *testing.T) {
	setupAssetLibraryServiceTestDB(t)
	verification := &model.UserFaceVerification{
		UserId:         1,
		ChannelId:      10,
		VerificationId: "fv_lock01",
		Status:         model.FaceVerificationStatusVerified,
	}
	require.NoError(t, model.CreateUserFaceVerification(verification))
	group, err := ClaimFaceVerificationGroup(verification, "group-upstream-lock")
	require.NoError(t, err)

	// Replicating a liveness group to an unrelated channel would fabricate an
	// empty upstream group, so it must be refused.
	otherConfig := &model.ChannelAssetConfig{ChannelId: 11, Enabled: true}
	_, err = replicateAssetGroupToChannelLocked(context.Background(), group, otherConfig)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be replicated")

	configs, err := assetLibraryConfigsForAssetGroup(group, []model.ChannelAssetConfig{
		{ChannelId: 10, Enabled: true},
		{ChannelId: 11, Enabled: true},
	})
	require.NoError(t, err)
	require.Len(t, configs, 1)
	assert.Equal(t, 10, configs[0].ChannelId)

	// A regular group keeps fanning out to every enabled channel.
	regularConfigs, err := assetLibraryConfigsForAssetGroup(
		&model.UserAssetGroup{Id: "group-na-0123456789abcdef0123456789abcdef", Origin: model.UserAssetGroupOriginLocal},
		[]model.ChannelAssetConfig{{ChannelId: 10, Enabled: true}, {ChannelId: 11, Enabled: true}},
	)
	require.NoError(t, err)
	assert.Len(t, regularConfigs, 2)
}

func TestSelectFaceVerificationChannelSkipsUnsupportedBackends(t *testing.T) {
	db := setupAssetLibraryServiceTestDB(t)
	require.NoError(t, db.Create(&model.Channel{
		Id: 10, Type: 1, Name: "plain-action", Status: common.ChannelStatusEnabled, Group: "default",
	}).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id: 11, Type: 1, Name: "gateway", Status: common.ChannelStatusEnabled, Group: "default",
	}).Error)
	require.NoError(t, db.Create(&model.ChannelAssetConfig{
		ChannelId: 10, Enabled: true, Backend: AssetLibraryBackendAction,
		BaseURL: "https://ark.cn-beijing.volcengineapi.com", AuthType: AssetLibraryAuthAKSK,
		Region: DefaultAssetLibraryRegion, ProjectName: DefaultAssetLibraryProject,
	}).Error)
	require.NoError(t, db.Create(&model.ChannelAssetConfig{
		ChannelId: 11, Enabled: true, Backend: AssetLibraryBackendSeedanceGateway,
		BaseURL: "https://gateway.example.com/api/seedance/proxy/openapi", AuthType: AssetLibraryAuthBearer,
		Region: DefaultAssetLibraryRegion, ProjectName: DefaultAssetLibraryProject,
	}).Error)

	config, err := SelectFaceVerificationChannel("default")
	require.NoError(t, err)
	assert.Equal(t, 11, config.ChannelId)
}

func TestSelectFaceVerificationChannelReportsUnsupportedWhenNoneQualify(t *testing.T) {
	db := setupAssetLibraryServiceTestDB(t)
	require.NoError(t, db.Create(&model.Channel{
		Id: 10, Type: 1, Name: "plain-action", Status: common.ChannelStatusEnabled, Group: "default",
	}).Error)
	require.NoError(t, db.Create(&model.ChannelAssetConfig{
		ChannelId: 10, Enabled: true, Backend: AssetLibraryBackendAction,
		BaseURL: "https://ark.cn-beijing.volcengineapi.com", AuthType: AssetLibraryAuthAKSK,
		Region: DefaultAssetLibraryRegion, ProjectName: DefaultAssetLibraryProject,
	}).Error)

	_, err := SelectFaceVerificationChannel("default")
	require.ErrorIs(t, err, ErrFaceVerificationNotSupported)
}

func TestSelectFaceVerificationChannelPrefersMatchingGroup(t *testing.T) {
	db := setupAssetLibraryServiceTestDB(t)
	require.NoError(t, db.Create(&model.Channel{
		Id: 10, Type: 1, Name: "shared", Status: common.ChannelStatusEnabled, Group: "default",
	}).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id: 11, Type: 1, Name: "vip", Status: common.ChannelStatusEnabled, Group: "vip",
	}).Error)
	for _, channelId := range []int{10, 11} {
		require.NoError(t, db.Create(&model.ChannelAssetConfig{
			ChannelId: channelId, Enabled: true, Backend: AssetLibraryBackendSeedanceGateway,
			BaseURL: "https://gateway.example.com/api/seedance/proxy/openapi", AuthType: AssetLibraryAuthBearer,
			Region: DefaultAssetLibraryRegion, ProjectName: DefaultAssetLibraryProject,
		}).Error)
	}

	config, err := SelectFaceVerificationChannel("vip")
	require.NoError(t, err)
	assert.Equal(t, 11, config.ChannelId)

	// An unknown group falls back to the whole capable pool, deterministically
	// preferring the lowest channel id.
	config, err = SelectFaceVerificationChannel("unknown")
	require.NoError(t, err)
	assert.Equal(t, 10, config.ChannelId)
}

func TestFaceVerificationGroupNameIsShortAndStable(t *testing.T) {
	assert.Equal(t, "liveness-abcdef12", FaceVerificationGroupName("fv_abcdef1234567890"))
	assert.Equal(t, "liveness-abcdef12", FaceVerificationGroupName("abcdef1234567890"))
	assert.Len(t, FaceVerificationGroupName(""), len("liveness-")+8)
}

func TestFaceVerificationCapabilityIsAdditiveToOtherBackends(t *testing.T) {
	gateway, err := assetLibraryBackendByName(AssetLibraryBackendSeedanceGateway)
	require.NoError(t, err)
	_, supported := gateway.(faceVerificationBackend)
	assert.True(t, supported, "the gateway backend must expose face verification")

	for _, backendName := range []string{
		AssetLibraryBackendAction,
		AssetLibraryBackendSeedanceSLS,
		AssetLibraryBackendOpenAPI,
		AssetLibraryBackendOinone,
		AssetLibraryBackendFly4k,
	} {
		backend, backendErr := assetLibraryBackendByName(backendName)
		require.NoError(t, backendErr)
		_, backendSupports := backend.(faceVerificationBackend)
		assert.Falsef(t, backendSupports, "%s must not claim face verification support", backendName)
	}
}

func TestSyncFaceVerificationSkipsNetworkForTerminalStates(t *testing.T) {
	setupAssetLibraryServiceTestDB(t)
	for _, status := range []string{
		model.FaceVerificationStatusFailed,
		model.FaceVerificationStatusExpired,
	} {
		verification := &model.UserFaceVerification{
			UserId:         1,
			ChannelId:      999,
			VerificationId: "fv_terminal_" + status,
			Status:         status,
		}
		// Channel 999 does not exist: a terminal state must not reach upstream.
		synced, err := SyncFaceVerification(context.Background(), verification)
		require.NoError(t, err)
		assert.Equal(t, status, synced.Status)
	}
}

// A liveness group owns one replica, so the existing replica-intersection rule
// already pins video requests to the verifying channel. This test locks that
// property in place: no extra routing code is needed, and a second asset
// library channel must never enter the intersection.
func TestLivenessAssetGroupPinsVideoRoutingToSingleChannel(t *testing.T) {
	db := setupAssetLibraryServiceTestDB(t)
	const groupId = "group-na-0123456789abcdef0123456789abcdef"
	const assetId = "asset-na-0123456789abcdef0123456789abcdef"
	// Both channels serve the asset library: channel 11 is the interesting one,
	// because it is enabled yet holds no replica of the liveness asset.
	for _, channelId := range []int{10, 11} {
		require.NoError(t, db.Create(&model.ChannelAssetConfig{
			ChannelId: channelId, Enabled: true, Backend: AssetLibraryBackendSeedanceGateway,
			BaseURL: "https://gateway.example.com/api/seedance/proxy/openapi", AuthType: AssetLibraryAuthBearer,
			Region: DefaultAssetLibraryRegion, ProjectName: DefaultAssetLibraryProject,
		}).Error)
	}
	require.NoError(t, db.Create(&model.UserAssetGroup{
		Id: groupId, UserId: 1, Name: "liveness-abc", GroupType: model.UserAssetGroupTypeLivenessFace,
		Origin: model.UserAssetGroupOriginLiveness, ProjectName: DefaultAssetLibraryProject,
	}).Error)
	require.NoError(t, db.Create(&model.UserAssetGroupReplica{
		GroupId: groupId, ChannelId: 10, UpstreamGroupId: "group-upstream-1",
		State: model.AssetReplicaStateReady,
	}).Error)
	require.NoError(t, db.Create(&model.UserAsset{
		Id: assetId, UserId: 1, GroupId: groupId, Name: "face", AssetType: "Image",
		SourceURL: "https://cdn.example.com/face.png", ProjectName: DefaultAssetLibraryProject,
	}).Error)
	require.NoError(t, db.Create(&model.UserAssetReplica{
		AssetId: assetId, ChannelId: 10, UpstreamAssetId: "asset-upstream-1",
		State: model.AssetReplicaStateReady, UpstreamStatus: "Active",
	}).Error)

	intersection, err := model.GetAssetReplicaChannelIntersection(1, []string{assetId})
	require.NoError(t, err)
	_, onVerifyingChannel := intersection[10]
	assert.True(t, onVerifyingChannel)
	assert.Len(t, intersection, 1)

	// A channel without a replica can never serve the request.
	_, onOtherChannel := intersection[11]
	assert.False(t, onOtherChannel)
}
