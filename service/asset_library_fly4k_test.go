package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFly4kAssetBackendCreatesGroupAndAsset(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.Method+" "+request.URL.Path)
		assert.Equal(t, "Bearer fly-key", request.Header.Get("Authorization"))
		assert.Equal(t, "application/json", strings.Split(request.Header.Get("Content-Type"), ";")[0])
		var body map[string]any
		require.NoError(t, common.DecodeJson(request.Body, &body))
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/createAsset":
			assert.Equal(t, map[string]any{
				"url":       "https://example.com/character.png",
				"assetType": "Image",
				"name":      "character",
			}, body)
			_, _ = writer.Write([]byte(`{
				"code": "0",
				"msg": "success",
				"data": {"assetId": "asset-20261008-abc123", "assetUri": "asset://asset-20261008-abc123"}
			}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	backend := fly4kAssetLibraryBackend{}
	config := &model.ChannelAssetConfig{
		Enabled: true, BaseURL: server.URL, AuthType: AssetLibraryAuthBearer, APIKey: "fly-key",
	}
	group := &model.UserAssetGroup{Id: "group-na-0123456789abcdef0123456789abcdef", Name: "characters"}

	// fly4k 没有分组接口：不应产生任何上游请求，且必须立刻就绪（不能是 Deferred，
	// 否则前端会永久显示「同步中」）。
	groupResult, err := backend.CreateGroup(t.Context(), config, group)
	require.NoError(t, err)
	assert.False(t, groupResult.Deferred)
	assert.Equal(t, fly4kLocalAssetGroupPlaceholderPrefix+group.Id, groupResult.GroupID)

	assetResult, err := backend.CreateAsset(t.Context(), config, group, &model.UserAssetGroupReplica{
		UpstreamGroupId: groupResult.GroupID,
	}, &model.UserAsset{
		SourceURL: "https://example.com/character.png", AssetType: "Image", Name: "character",
	})
	require.NoError(t, err)
	assert.Equal(t, "asset-20261008-abc123", assetResult.AssetID)
	assert.Equal(t, "Processing", assetResult.Status)
	assert.Equal(t, []string{"POST /api/v1/createAsset"}, paths)
}

func TestFly4kAssetBackendQueriesAssetStatus(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/api/v1/asset/query", request.URL.Path)
		var body map[string]any
		require.NoError(t, common.DecodeJson(request.Body, &body))
		bodies = append(bodies, body)
		writer.Header().Set("Content-Type", "application/json")
		if body["assetId"] == "asset-failed" {
			_, _ = writer.Write([]byte(`{"code":"0","msg":"success","data":{"Id":"asset-failed","Status":"Failed"}}`))
			return
		}
		// 上游实际返回 PascalCase（文档与真实抓包一致）
		_, _ = writer.Write([]byte(`{
			"code": "0",
			"msg": "success",
			"data": {
				"Id": "asset-20261008-abc123",
				"Name": "character",
				"URL": "https://ark-media-asset.example.com/signed.png",
				"AssetType": "Image",
				"Status": "Active",
				"CreateTime": "2026-10-08 14:21:02",
				"UpdateTime": "2026-10-08 14:21:07"
			}
		}`))
	}))
	t.Cleanup(server.Close)

	backend := fly4kAssetLibraryBackend{}
	config := &model.ChannelAssetConfig{
		Enabled: true, BaseURL: server.URL + "/", AuthType: AssetLibraryAuthBearer, APIKey: "fly-key",
	}

	details, err := backend.GetAsset(t.Context(), config, "asset-20261008-abc123")
	require.NoError(t, err)
	assert.Equal(t, "asset-20261008-abc123", details.Id)
	assert.Equal(t, "Active", details.Status)
	assert.Equal(t, "Image", details.AssetType)
	assert.Equal(t, "https://ark-media-asset.example.com/signed.png", details.URL)
	assert.Nil(t, details.Error)
	assert.Equal(t, []map[string]any{{"assetId": "asset-20261008-abc123"}}, bodies)

	failed, err := backend.GetAsset(t.Context(), config, "asset-failed")
	require.NoError(t, err)
	assert.Equal(t, "Failed", failed.Status)
	require.NotNil(t, failed.Error)
	assert.Equal(t, "AssetSyncFailed", failed.Error.Code)
}

func TestFly4kAssetBackendSurfacesEnvelopeErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// 业务失败也走 HTTP 200，必须靠 code 判定
		_, _ = writer.Write([]byte(`{"code":"FK0019","msg":"素材入库失败","data":null}`))
	}))
	t.Cleanup(server.Close)

	backend := fly4kAssetLibraryBackend{}
	config := &model.ChannelAssetConfig{
		Enabled: true, BaseURL: server.URL, AuthType: AssetLibraryAuthBearer, APIKey: "fly-key",
	}
	_, err := backend.CreateAsset(t.Context(), config, nil, nil, &model.UserAsset{
		SourceURL: "https://example.com/character.png", AssetType: "Image",
	})
	require.Error(t, err)
	var upstreamErr *AssetLibraryUpstreamError
	require.True(t, errors.As(err, &upstreamErr), "expected AssetLibraryUpstreamError, got %v", err)
	assert.Equal(t, "FK0019", upstreamErr.Code)
	assert.Equal(t, http.StatusOK, upstreamErr.StatusCode)
}

func TestFly4kAssetBackendRejectsUnsafeInputs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":"0","msg":"success","data":{"assetId":"asset-x"}}`))
	}))
	t.Cleanup(server.Close)

	backend := fly4kAssetLibraryBackend{}

	// 非 Bearer 鉴权直接拒绝（fly4k 只支持单 API Key）
	_, err := backend.CreateAsset(t.Context(), &model.ChannelAssetConfig{
		Enabled: true, BaseURL: server.URL, AuthType: AssetLibraryAuthAKSK, AccessKey: "ak", SecretKey: "sk",
	}, nil, nil, &model.UserAsset{SourceURL: "https://example.com/a.png", AssetType: "Image"})
	require.ErrorContains(t, err, "Bearer")

	// API Key 为空
	_, err = backend.CreateAsset(t.Context(), &model.ChannelAssetConfig{
		Enabled: true, BaseURL: server.URL, AuthType: AssetLibraryAuthBearer,
	}, nil, nil, &model.UserAsset{SourceURL: "https://example.com/a.png", AssetType: "Image"})
	require.ErrorContains(t, err, "API key")

	// 内网/本机素材 URL 不可达：上游拉不到，必须提前报错
	_, err = backend.CreateAsset(t.Context(), &model.ChannelAssetConfig{
		Enabled: true, BaseURL: server.URL, AuthType: AssetLibraryAuthBearer, APIKey: "fly-key",
	}, nil, nil, &model.UserAsset{SourceURL: "http://127.0.0.1:3000/api/asset/files/ab/x.png", AssetType: "Image"})
	require.ErrorContains(t, err, "publicly reachable")

	// 未启用素材库的渠道不允许调用
	_, err = backend.CreateAsset(t.Context(), &model.ChannelAssetConfig{
		Enabled: false, BaseURL: server.URL, AuthType: AssetLibraryAuthBearer, APIKey: "fly-key",
	}, nil, nil, &model.UserAsset{SourceURL: "https://example.com/a.png", AssetType: "Image"})
	require.ErrorContains(t, err, "not enabled")

	// 空素材 ID 不查上游
	_, err = backend.GetAsset(t.Context(), &model.ChannelAssetConfig{
		Enabled: true, BaseURL: server.URL, AuthType: AssetLibraryAuthBearer, APIKey: "fly-key",
	}, "   ")
	require.ErrorContains(t, err, "asset id is empty")
}

func TestFly4kAssetBackendHasNoUpstreamUpdateOrDelete(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	backend := fly4kAssetLibraryBackend{}
	config := &model.ChannelAssetConfig{
		Enabled: true, BaseURL: server.URL, AuthType: AssetLibraryAuthBearer, APIKey: "fly-key",
	}
	// fly4k 上游没有这些接口：必须是无副作用空实现，且不应发出任何请求。
	require.NoError(t, backend.UpdateGroup(t.Context(), config, &model.UserAssetGroup{Name: "x"}, "g1"))
	require.NoError(t, backend.UpdateAsset(t.Context(), config, &model.UserAsset{Name: "x"}, "a1"))
	require.NoError(t, backend.DeleteGroup(t.Context(), config, "g1"))
	require.NoError(t, backend.DeleteAsset(t.Context(), config, "a1"))
	assert.Zero(t, requests)

	assert.Equal(t, "asset://asset-20261008-abc123", backend.FormatAssetReference("asset-20261008-abc123"))
}

// TestFly4kBackendRegistrationIsAdditive 锁定「不影响其它渠道」的关键约束：
// fly4k 只是新增一种可选模式，既不改变任何渠道类型的默认协议，也不改变既有后端的判定。
func TestFly4kBackendRegistrationIsAdditive(t *testing.T) {
	assert.True(t, IsSupportedAssetLibraryBackend(AssetLibraryBackendFly4k))
	assert.True(t, AssetLibraryBackendRequiresBearer(AssetLibraryBackendFly4k))
	assert.True(t, AssetLibraryBackendRequiresBearer(AssetLibraryBackendOinone))

	// 既有后端判定保持不变
	for _, backend := range []string{
		AssetLibraryBackendAction, AssetLibraryBackendSeedanceSLS, AssetLibraryBackendOpenAPI, AssetLibraryBackendOinone,
	} {
		assert.True(t, IsSupportedAssetLibraryBackend(backend), backend)
	}
	assert.False(t, AssetLibraryBackendRequiresBearer(AssetLibraryBackendAction))
	assert.False(t, IsSupportedAssetLibraryBackend("not-a-backend"))

	// 默认协议不变：fly4k 渠道也必须由管理员在后台手动选择模式
	assert.Equal(t, AssetLibraryBackendAction, DefaultAssetLibraryBackend(constant.ChannelTypeFly4k))
	assert.Equal(t, AssetLibraryBackendAction, DefaultAssetLibraryBackend(constant.ChannelTypeDoubaoVideo))
	assert.Equal(t, AssetLibraryBackendSeedanceSLS, DefaultAssetLibraryBackend(constant.ChannelTypeSeedanceSLS))

	// 只有显式配置 backend=fly4k 时才命中新后端实现
	db := setupAssetLibraryServiceTestDB(t)
	require.NoError(t, db.Create(&model.Channel{
		Id: 21, Type: constant.ChannelTypeFly4k, Key: "fly-key", Name: "Fly4k",
	}).Error)
	require.NoError(t, db.Create(&model.ChannelAssetConfig{
		ChannelId: 21, Enabled: true, Backend: AssetLibraryBackendFly4k,
		BaseURL: "https://api.fly4k.com", AuthType: AssetLibraryAuthBearer, APIKey: "fly-key",
	}).Error)
	backend, err := assetLibraryBackendForChannel(21)
	require.NoError(t, err)
	assert.IsType(t, fly4kAssetLibraryBackend{}, backend)
}

// TestReplicateFly4kAssetBecomesActive 覆盖完整引擎链路：分组副本立即就绪，
// 素材入库后由轮询任务转为 ready（证明共享副本引擎对 fly4k 无需任何修改）。
func TestReplicateFly4kAssetBecomesActive(t *testing.T) {
	db := setupAssetLibraryServiceTestDB(t)
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, "POST "+request.URL.Path)
		assert.Equal(t, "Bearer fly-key", request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/createAsset":
			_, _ = writer.Write([]byte(`{"code":"0","msg":"success","data":{"assetId":"asset-20261008-abc123","assetUri":"asset://asset-20261008-abc123"}}`))
		case "/api/v1/asset/query":
			_, _ = writer.Write([]byte(`{"code":"0","msg":"success","data":{"Id":"asset-20261008-abc123","Status":"Active","AssetType":"Image"}}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	channel := &model.Channel{Id: 22, Type: constant.ChannelTypeFly4k, Key: "fly-key", Name: "Fly4k"}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.ChannelAssetConfig{
		ChannelId: 22, Enabled: true, Backend: AssetLibraryBackendFly4k,
		BaseURL: server.URL, AuthType: AssetLibraryAuthBearer, APIKey: "fly-key",
	}).Error)
	group := &model.UserAssetGroup{
		Id: "group-na-0123456789abcdef0123456789abcdef", UserId: 7, Name: "portraits", GroupType: "AIGC",
	}
	asset := &model.UserAsset{
		Id: "asset-na-0123456789abcdef0123456789abcdef", UserId: 7, GroupId: group.Id,
		SourceURL: "https://example.com/portrait.png", AssetType: "Image", Name: "portrait",
	}
	require.NoError(t, db.Create(group).Error)
	require.NoError(t, db.Create(asset).Error)

	report, err := ReplicateAsset(t.Context(), asset)
	require.NoError(t, err)
	assert.Empty(t, report.Errors)
	assert.Equal(t, []string{"POST /api/v1/createAsset"}, requests)

	groupReplica, err := model.GetUserAssetGroupReplica(group.Id, 22)
	require.NoError(t, err)
	assert.Equal(t, model.AssetReplicaStateReady, groupReplica.State)

	assetReplica, err := model.GetUserAssetReplica(asset.Id, 22)
	require.NoError(t, err)
	assert.Equal(t, "asset-20261008-abc123", assetReplica.UpstreamAssetId)
	assert.Equal(t, model.AssetReplicaStateProcessing, assetReplica.State)

	details, err := RefreshAssetLibraryAsset(t.Context(), asset.Id)
	require.NoError(t, err)
	assert.Equal(t, "Active", details.Status)
	assetReplica, err = model.GetUserAssetReplica(asset.Id, 22)
	require.NoError(t, err)
	assert.Equal(t, model.AssetReplicaStateReady, assetReplica.State)

	// 生成阶段把本地引用换成上游引用
	rewritten, err := RewriteAssetReferences(7, 22, map[string]any{"image_url": "asset://" + asset.Id})
	require.NoError(t, err)
	assert.Equal(t, "asset://asset-20261008-abc123", rewritten["image_url"])
	assert.Equal(t, []string{"POST /api/v1/createAsset", "POST /api/v1/asset/query"}, requests)
}
