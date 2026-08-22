package service

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAssetReplicaSummaryIncludesPerChannelStates(t *testing.T) {
	enabled := map[int]struct{}{1: {}, 2: {}, 3: {}}
	names := map[int]string{1: "channel-one", 2: "channel-two", 3: "channel-three"}
	replicas := []assetReplicaView{
		{ChannelId: 1, State: model.AssetReplicaStateReady, UpstreamId: "up-1", UpstreamStatus: "Active"},
		{ChannelId: 2, State: model.AssetReplicaStateFailed, UpstreamStatus: "Failed", LastError: "boom"},
		{ChannelId: 9, State: model.AssetReplicaStateReady, UpstreamId: "up-disabled"},
	}

	summary := buildAssetReplicaSummary(3, enabled, names, replicas)

	assert.Equal(t, 3, summary.Total)
	assert.Equal(t, 1, summary.Ready)
	assert.Equal(t, 1, summary.Failed)
	assert.Equal(t, 1, summary.Processing)
	require.Len(t, summary.Channels, 3)

	assert.Equal(t, 1, summary.Channels[0].ChannelId)
	assert.Equal(t, "channel-one", summary.Channels[0].Name)
	assert.Equal(t, model.AssetReplicaStateReady, summary.Channels[0].State)
	assert.Equal(t, "Active", summary.Channels[0].UpstreamStatus)

	assert.Equal(t, 2, summary.Channels[1].ChannelId)
	assert.Equal(t, "channel-two", summary.Channels[1].Name)
	assert.Equal(t, model.AssetReplicaStateFailed, summary.Channels[1].State)
	assert.Equal(t, "boom", summary.Channels[1].LastError)

	// Channel 3 is enabled but has no replica yet.
	assert.Equal(t, 3, summary.Channels[2].ChannelId)
	assert.Equal(t, "channel-three", summary.Channels[2].Name)
	assert.Equal(t, model.AssetReplicaStatePending, summary.Channels[2].State)
}

func setupRefreshReplicaTaskFixtures(t *testing.T, serverURL string) (*model.AssetLibraryTask, *model.UserAssetReplica) {
	t.Helper()
	db := setupAssetLibraryServiceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssetLibraryTask{}))
	require.NoError(t, db.Create(&model.Channel{Id: 7, Name: "refresh-channel", Type: 1, Status: 1}).Error)
	require.NoError(t, db.Create(&model.ChannelAssetConfig{
		ChannelId: 7,
		Enabled:   true,
		Backend:   AssetLibraryBackendAction,
		BaseURL:   serverURL,
		AuthType:  AssetLibraryAuthBearer,
		APIKey:    "plain-key",
	}).Error)
	replica := &model.UserAssetReplica{
		AssetId:         "asset-na-refresh",
		ChannelId:       7,
		UpstreamAssetId: "up-refresh-1",
		State:           model.AssetReplicaStateProcessing,
		UpstreamStatus:  "Processing",
	}
	require.NoError(t, db.Create(replica).Error)
	task := &model.AssetLibraryTask{
		TaskType:    model.AssetLibraryTaskTypeRefreshAssetReplica,
		ChannelId:   7,
		TargetId:    "asset-na-refresh",
		State:       model.AssetLibraryTaskStateRunning,
		MaxAttempts: 8,
		CreatedTime: time.Now().Unix(),
	}
	require.NoError(t, db.Create(task).Error)
	return task, replica
}

func TestRunRefreshAssetReplicaTaskTransitionsToReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "GetAsset", request.URL.Query().Get("Action"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"ResponseMetadata": {"RequestId": "req-1", "Action": "GetAsset"},
			"Result": {"Id": "up-refresh-1", "Status": "Active", "URL": "https://example.com/a.png"}
		}`))
	}))
	defer server.Close()

	task, _ := setupRefreshReplicaTaskFixtures(t, server.URL)

	require.NoError(t, runRefreshAssetReplicaTask(t.Context(), task))

	replica, err := model.GetUserAssetReplica("asset-na-refresh", 7)
	require.NoError(t, err)
	assert.Equal(t, model.AssetReplicaStateReady, replica.State)
	assert.Equal(t, "Active", replica.UpstreamStatus)
}

func TestRunRefreshAssetReplicaTaskRetriesWhileProcessing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"ResponseMetadata": {"RequestId": "req-1", "Action": "GetAsset"},
			"Result": {"Id": "up-refresh-1", "Status": "Processing"}
		}`))
	}))
	defer server.Close()

	task, _ := setupRefreshReplicaTaskFixtures(t, server.URL)

	err := runRefreshAssetReplicaTask(t.Context(), task)
	assert.ErrorIs(t, err, errAssetLibraryTaskRetryLater)

	replica, replicaErr := model.GetUserAssetReplica("asset-na-refresh", 7)
	require.NoError(t, replicaErr)
	assert.Equal(t, model.AssetReplicaStateProcessing, replica.State)
}

func TestRunRefreshAssetReplicaTaskGivesUpAfterWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"ResponseMetadata": {"RequestId": "req-1", "Action": "GetAsset"},
			"Result": {"Id": "up-refresh-1", "Status": "Processing"}
		}`))
	}))
	defer server.Close()

	task, _ := setupRefreshReplicaTaskFixtures(t, server.URL)
	// Simulate a task created more than the refresh window ago.
	task.CreatedTime = time.Now().Add(-2 * time.Hour).Unix()

	// Still processing upstream, but the window is closed: task completes
	// silently and the replica waits for user-triggered refreshes.
	require.NoError(t, runRefreshAssetReplicaTask(t.Context(), task))

	replica, err := model.GetUserAssetReplica("asset-na-refresh", 7)
	require.NoError(t, err)
	assert.Equal(t, model.AssetReplicaStateProcessing, replica.State)
}

func TestRunRefreshAssetReplicaTaskStopsWhenReplicaIsFinal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer server.Close()

	task, replica := setupRefreshReplicaTaskFixtures(t, server.URL)
	replica.State = model.AssetReplicaStateReady
	require.NoError(t, model.SaveUserAssetReplica(replica))

	require.NoError(t, runRefreshAssetReplicaTask(t.Context(), task))
	assert.Equal(t, int32(0), calls.Load(), "upstream must not be polled once the replica is final")
}
