package doubao

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNativeRequestUsesExistingDoubaoBillingAndPreservesPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyTaskResponseFormat, constant.TaskResponseFormatDoubaoVideo)
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-0-260128",
		Prompt: "Generate a tracking shot",
		Metadata: map[string]any{
			"model":          "doubao-seedance-2-0-260128",
			"resolution":     "1080p",
			"duration":       float64(5),
			"generate_audio": false,
			"content": []any{
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://example.com/input.mp4"}},
				map[string]any{"type": "text", "text": "Generate a tracking shot"},
			},
		},
	})

	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2-0-260128",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "mapped-seedance-model",
		},
	}

	ratios := adaptor.EstimateBilling(ctx, info)
	require.Contains(t, ratios, "video_input")
	assert.InDelta(t, 31.0/46.0, ratios["video_input"], 1e-12)

	requestBody, err := adaptor.BuildRequestBody(ctx, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	assert.Equal(t, "mapped-seedance-model", payload["model"])
	assert.Equal(t, "1080p", payload["resolution"])
	assert.Equal(t, false, payload["generate_audio"])
	content, ok := payload["content"].([]any)
	require.True(t, ok)
	assert.Len(t, content, 2)
}

func TestNativeRequestRewritesLogicalAssetForCurrentChannel(t *testing.T) {
	originalDB := model.DB
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Channel{}, &model.ChannelAssetConfig{}, &model.UserAsset{}, &model.UserAssetReplica{},
	))
	model.DB = db
	t.Cleanup(func() {
		model.DB = originalDB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	assetId := "asset-na-0123456789abcdef0123456789abcdef"
	require.NoError(t, db.Create(&model.Channel{
		Id: 11, Type: constant.ChannelTypeDoubaoVideo, Key: "action-key", Name: "Doubao Video",
	}).Error)
	require.NoError(t, db.Create(&model.ChannelAssetConfig{
		ChannelId: 11, Enabled: true, Backend: service.AssetLibraryBackendAction,
		BaseURL: service.DefaultAssetLibraryBaseURL, AuthType: service.AssetLibraryAuthAKSK,
		AccessKey: "access-key", SecretKey: "secret-key",
	}).Error)
	require.NoError(t, db.Create(&model.UserAsset{
		Id: assetId, UserId: 7, GroupId: "group-na-test", AssetType: "Image",
		SourceURL: "https://example.com/a.png", ProjectName: "default",
	}).Error)
	require.NoError(t, db.Create(&model.UserAssetReplica{
		AssetId: assetId, ChannelId: 11, UpstreamAssetId: "asset-upstream-11",
		State: model.AssetReplicaStateReady,
	}).Error)

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyTaskResponseFormat, constant.TaskResponseFormatDoubaoVideo)
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Model: "doubao-seedance-2-0-260128",
		Metadata: map[string]any{
			"content": []any{map[string]any{
				"type": "image_url", "image_url": map[string]any{"url": "asset://" + assetId},
			}},
		},
	})
	info := &relaycommon.RelayInfo{UserId: 7, ChannelMeta: &relaycommon.ChannelMeta{
		ChannelId: 11, UpstreamModelName: "mapped-model",
	}}

	requestBody, err := (&TaskAdaptor{}).BuildRequestBody(ctx, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"url":"asset://asset-upstream-11"`)
	assert.NotContains(t, string(encoded), assetId)
}

func TestCompatibleRequestRejectsRawAssetURI(t *testing.T) {
	for _, request := range []relaycommon.TaskSubmitReq{
		{
			Model:  "doubao-seedance-2-0-260128",
			Prompt: "Generate a tracking shot",
			Images: []string{"asset://Asset-upstream-owned-by-another-user"},
		},
		{
			Model:  "doubao-seedance-2-0-260128",
			Prompt: "Generate a tracking shot",
			Images: []string{"asset://asset-na-0123456789abcdef0123456789abcdef"},
		},
		{
			Model:  "doubao-seedance-2-0-260128",
			Prompt: "Generate a tracking shot",
			Metadata: map[string]any{"content": []any{map[string]any{
				"type": "image_url", "image_url": map[string]any{"url": "ASSET://Asset-upstream"},
			}}},
		},
	} {
		gin.SetMode(gin.TestMode)
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("task_request", request)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "mapped-model",
		}}

		_, err := (&TaskAdaptor{}).BuildRequestBody(ctx, info)

		require.ErrorContains(t, err, "only supported by the native asset-library endpoint")
	}
}

func TestNativeSubmitResponseUsesPublicTaskID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(ctx, constant.ContextKeyTaskResponseFormat, constant.TaskResponseFormatDoubaoVideo)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"id":"upstream-task-id"}`)),
	}

	adaptor := &TaskAdaptor{}
	upstreamID, taskData, taskErr := adaptor.DoResponse(ctx, response, &relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2-0-260128",
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID: "task_public",
		},
	})

	require.Nil(t, taskErr)
	assert.Equal(t, "upstream-task-id", upstreamID)
	assert.JSONEq(t, `{"id":"upstream-task-id"}`, string(taskData))
	assert.JSONEq(t, `{"id":"task_public"}`, recorder.Body.String())
}

func TestConvertToNativeVideoNormalizesOfficialResponse(t *testing.T) {
	task := &model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		CreatedAt: 100,
		UpdatedAt: 200,
		Properties: model.Properties{
			OriginModelName: "doubao-seedance-2-0-260128",
		},
		PrivateData: model.TaskPrivateData{ResultURL: "https://example.com/output.mp4"},
		Data: json.RawMessage(`{
			"id":"upstream-task-id",
			"model":"upstream-model",
			"status":"succeeded",
			"content":{
				"video_url":"https://example.com/output.mp4",
				"kz_video_url":"https://channel.example.com/output.mp4",
				"last_frame_url":"https://example.com/frame.png"
			},
			"seed":93073,
			"resolution":"480p",
			"ratio":"16:9",
			"duration":4,
			"framespersecond":24,
			"service_tier":"default",
			"execution_expires_after":172800,
			"generate_audio":true,
			"tools":[{"type":"web_search"}],
			"safety_identifier":"user-123",
			"priority":0,
			"draft":false,
			"draft_task_id":"task_draft",
			"usage":{
				"completion_tokens":35000,
				"total_tokens":35000,
				"tool_usage":{"web_search":1}
			}
		}`),
	}

	encoded, err := (&TaskAdaptor{}).ConvertToNativeVideo(task)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"id":"task_public",
		"model":"doubao-seedance-2-0-260128",
		"status":"succeeded",
		"created_at":100,
		"updated_at":200,
		"content":{
			"video_url":"https://example.com/output.mp4",
			"last_frame_url":"https://example.com/frame.png"
		},
		"seed":93073,
		"resolution":"480p",
		"ratio":"16:9",
		"duration":4,
		"framespersecond":24,
		"generate_audio":true,
		"tools":[{"type":"web_search"}],
		"safety_identifier":"user-123",
		"priority":0,
		"draft":false,
		"draft_task_id":"task_draft",
		"service_tier":"default",
		"execution_expires_after":172800,
		"usage":{
			"completion_tokens":35000,
			"total_tokens":35000,
			"tool_usage":{"web_search":1}
		}
	}`, string(encoded))
}

// 复现 HK 线上形态：上游同为 new-api，Data 存的是通用信封
// {"code":"success","data":{任务DTO...}}，Content 反序列化不到，
// 此时应从信封中提取上游真实视频地址，而不是回退本系统代理链接。
func TestConvertToNativeVideoExtractsUpstreamURLFromEnvelope(t *testing.T) {
	task := &model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		CreatedAt: 100,
		UpdatedAt: 200,
		Properties: model.Properties{
			OriginModelName: "doubao-seedance-2-0",
		},
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://gateway.example.com/v1/videos/task_public/content",
		},
		Data: json.RawMessage(`{
			"code":"success",
			"data":{
				"task_id":"task_upstream",
				"status":"SUCCESS",
				"result_url":"https://ark-acg9.tos-cn-beijing.volces.com/a.mp4?sig=2",
				"data":{
					"content":{"video_url":"https://ark-acg9.tos-cn-beijing.volces.com/a.mp4?sig=1"},
					"id":"cgt-20260915-kaocu",
					"status":"succeeded"
				}
			},
			"message":""
		}`),
	}

	encoded, err := (&TaskAdaptor{}).ConvertToNativeVideo(task)
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(encoded, &resp))
	content, ok := resp["content"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://ark-acg9.tos-cn-beijing.volces.com/a.mp4?sig=1", content["video_url"])
}

func TestConvertToNativeVideoHydratesUsageFromEnvelope(t *testing.T) {
	task := &model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		CreatedAt: 100,
		UpdatedAt: 200,
		Properties: model.Properties{
			OriginModelName: "doubao-seedance-2-0",
		},
		Data: json.RawMessage(`{
			"code":"success",
			"data":{
				"task_id":"task_upstream",
				"status":"SUCCESS",
				"data":{
					"content":{"video_url":"https://ark-acg9.tos-cn-beijing.volces.com/a.mp4?sig=1"},
					"id":"cgt-20260916-x4aoi",
					"status":"succeeded",
					"resolution":"720p",
					"duration":5,
					"usage":{"completion_tokens":108900,"total_tokens":108900}
				}
			},
			"message":""
		}`),
	}

	encoded, err := (&TaskAdaptor{}).ConvertToNativeVideo(task)
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(encoded, &resp))
	assert.Equal(t, "720p", resp["resolution"])
	assert.Equal(t, float64(5), resp["duration"])
	usage, ok := resp["usage"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(108900), usage["completion_tokens"])
	assert.Equal(t, float64(108900), usage["total_tokens"])
}

func TestConvertToOpenAIVideoHydratesUsageMetadataFromEnvelope(t *testing.T) {
	task := &model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		CreatedAt: 100,
		UpdatedAt: 200,
		Properties: model.Properties{
			OriginModelName: "doubao-seedance-2-0",
		},
		Data: json.RawMessage(`{
			"code":"success",
			"data":{
				"task_id":"task_upstream",
				"status":"SUCCESS",
				"data":{
					"content":{"video_url":"https://ark-acg9.tos-cn-beijing.volces.com/a.mp4?sig=1"},
					"status":"succeeded",
					"resolution":"720p",
					"duration":5,
					"usage":{"completion_tokens":108900,"total_tokens":108900}
				}
			},
			"message":""
		}`),
	}

	encoded, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(encoded, &resp))
	metadata, ok := resp["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "720p", metadata["resolution"])
	assert.Equal(t, float64(5), metadata["duration"])
	usage, ok := metadata["usage"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(108900), usage["total_tokens"])
}

func TestConvertToNativeVideoReturnsOfficialFailureShape(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_failed",
		Status:     model.TaskStatusFailure,
		CreatedAt:  100,
		UpdatedAt:  200,
		FailReason: "upstream generation failed",
		Properties: model.Properties{OriginModelName: "doubao-seedance-2-0-260128"},
	}

	encoded, err := (&TaskAdaptor{}).ConvertToNativeVideo(task)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"id":"task_failed",
		"model":"doubao-seedance-2-0-260128",
		"status":"failed",
		"created_at":100,
		"updated_at":200,
		"error":{"code":"","message":"upstream generation failed"}
	}`, string(encoded))
}

func TestValidateResolution(t *testing.T) {
	whitelist := modelResolutionWhitelist["doubao-seedance-2-0"]
	assert.Nil(t, validateResolution("720p", whitelist))
	assert.Nil(t, validateResolution("1080p", whitelist))
	assert.Nil(t, validateResolution(" 1080P ", whitelist))

	err := validateResolution("480p", whitelist)
	require.NotNil(t, err)
	assert.Equal(t, "unsupported_resolution", err.Code)
	assert.Equal(t, http.StatusBadRequest, err.StatusCode)

	err = validateResolution("4k", whitelist)
	require.NotNil(t, err)
	assert.Equal(t, "unsupported_resolution", err.Code)
}

// 其他模型不在白名单内（whitelist 为 nil）时不做分辨率限制，
// 分辨率限制只针对经 oinone 中转的 doubao-seedance-2-0。
func TestValidateResolutionNotRestrictedForOtherModels(t *testing.T) {
	var noWhitelist map[string]bool
	assert.Nil(t, validateResolution("480p", noWhitelist))
	assert.Nil(t, validateResolution("4k", noWhitelist))

	assert.NotContains(t, modelResolutionWhitelist, "doubao-seedance-2-0-260128")
}
