package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const (
	assetLibraryTaskPollInterval = 5 * time.Second
	assetLibraryTaskBatchSize    = 20
	assetLibraryTaskRetention    = 7 * 24 * time.Hour
	// assetLibraryReplicaRefreshWindow bounds automatic upstream status
	// polling for one replica. After the window closes, status updates fall
	// back to user-triggered refreshes (list/detail queries).
	assetLibraryReplicaRefreshWindow = time.Hour
)

// errAssetLibraryTaskRetryLater asks the worker to reschedule the task without
// counting the pass as a failure. Used by replica status refresh polling.
var errAssetLibraryTaskRetryLater = errors.New("asset library task retry later")

// upstreamReplicaRef identifies one upstream replica inside a delete task payload.
type upstreamReplicaRef struct {
	ChannelId int    `json:"channel_id"`
	UpstreamId string `json:"upstream_id"`
}

var assetLibraryTaskWorkerOnce bool

// StartAssetLibraryTaskWorker starts the persistent task queue worker. Tasks
// survive restarts because they live in the database; interrupted running
// tasks are reset to pending on startup.
func StartAssetLibraryTaskWorker() {
	if assetLibraryTaskWorkerOnce {
		return
	}
	assetLibraryTaskWorkerOnce = true
	if recovered, err := model.RecoverStaleAssetLibraryTasks(); err != nil {
		common.SysError("asset library task recovery failed: " + err.Error())
	} else if recovered > 0 {
		common.SysLog("asset library task worker recovered " + strconv.FormatInt(recovered, 10) + " interrupted tasks")
	}
	enqueuePendingAssetReplicaRefreshTasks()
	common.RelayCtxGo(context.Background(), func() {
		ticker := time.NewTicker(assetLibraryTaskPollInterval)
		defer ticker.Stop()
		// cleanup finished tasks once a day
		cleanupTicker := time.NewTicker(24 * time.Hour)
		defer cleanupTicker.Stop()
		for {
			select {
			case <-ticker.C:
				runDueAssetLibraryTasks()
			case <-cleanupTicker.C:
				if _, err := model.PruneFinishedAssetLibraryTasks(assetLibraryTaskRetention); err != nil {
					common.SysError("asset library task cleanup failed: " + err.Error())
				}
			}
		}
	})
	common.SysLog("asset library task worker started")
}

func runDueAssetLibraryTasks() {
	tasks, err := model.ClaimDueAssetLibraryTasks(assetLibraryTaskBatchSize)
	if err != nil {
		common.SysError("asset library task claim failed: " + err.Error())
		return
	}
	for i := range tasks {
		executeAssetLibraryTask(&tasks[i])
	}
}

func executeAssetLibraryTask(task *model.AssetLibraryTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	err := runAssetLibraryTask(ctx, task)
	if err == nil {
		if completeErr := model.CompleteAssetLibraryTask(task.Id); completeErr != nil {
			common.SysError("asset library task complete update failed: " + completeErr.Error())
		}
		return
	}
	if errors.Is(err, errAssetLibraryTaskRetryLater) {
		// Polling task: reschedule without counting as a failure. Attempt
		// exhaustion still marks the task dead via the normal failure path.
		delay := assetLibraryRetryDelaySeconds(task.Attempts)
		if delay < 30 {
			delay = 30
		}
		if rescheduleErr := model.RescheduleAssetLibraryTask(task.Id, time.Now().Unix()+delay); rescheduleErr != nil {
			common.SysError("asset library task reschedule failed: " + rescheduleErr.Error())
		}
		return
	}
	message := assetLibraryStoredError(err)
	common.SysError("asset library task " + task.TaskType + " #" + strconv.FormatInt(task.Id, 10) + " attempt " +
		strconv.Itoa(task.Attempts) + " failed: " + message)
	if failErr := model.FailAssetLibraryTask(task.Id, task.Attempts, task.MaxAttempts, message); failErr != nil {
		common.SysError("asset library task failure update failed: " + failErr.Error())
	}
}

func runAssetLibraryTask(ctx context.Context, task *model.AssetLibraryTask) error {
	switch task.TaskType {
	case model.AssetLibraryTaskTypeSyncChannel:
		_, err := SyncAssetLibraryChannel(ctx, task.ChannelId)
		return err
	case model.AssetLibraryTaskTypeReplicateGroup:
		group, err := model.GetUserAssetGroupById(task.TargetId)
		if err != nil {
			// Group may have been deleted while the task was queued.
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		_, err = ReplicateAssetGroup(ctx, group)
		return err
	case model.AssetLibraryTaskTypeReplicateAsset:
		asset, err := model.GetUserAssetById(task.TargetId)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		_, err = ReplicateAsset(ctx, asset)
		return err
	case model.AssetLibraryTaskTypeDeleteAssetReplicas:
		return runDeleteAssetReplicasTask(ctx, task)
	case model.AssetLibraryTaskTypeDeleteGroupReplicas:
		return runDeleteGroupReplicasTask(ctx, task)
	case model.AssetLibraryTaskTypeRefreshAssetReplica:
		return runRefreshAssetReplicaTask(ctx, task)
	default:
		return errors.New("unknown asset library task type: " + task.TaskType)
	}
}

// EnqueueAssetLibraryRefreshReplicaTask schedules automatic upstream status
// polling for one (asset, channel) replica. Enqueue is idempotent while an
// active refresh task exists.
func EnqueueAssetLibraryRefreshReplicaTask(assetId string, channelId int) (int64, error) {
	return model.EnqueueAssetLibraryTask(model.AssetLibraryTaskTypeRefreshAssetReplica, channelId, assetId, "")
}

// enqueuePendingAssetReplicaRefreshTasks re-arms refresh tasks for replicas
// still processing after a restart, within the refresh window.
func enqueuePendingAssetReplicaRefreshTasks() {
	cutoff := time.Now().Add(-assetLibraryReplicaRefreshWindow).Unix()
	replicas, err := model.ListProcessingUserAssetReplicas(cutoff)
	if err != nil {
		common.SysError("asset library replica refresh sweep failed: " + err.Error())
		return
	}
	for i := range replicas {
		if _, err := EnqueueAssetLibraryRefreshReplicaTask(replicas[i].AssetId, replicas[i].ChannelId); err != nil {
			common.SysError("asset library replica refresh enqueue failed: " + err.Error())
		}
	}
	if len(replicas) > 0 {
		common.SysLog("asset library refresh tasks enqueued for " + strconv.Itoa(len(replicas)) + " processing replicas")
	}
}

// runRefreshAssetReplicaTask polls one upstream replica until its status
// becomes final (ready/failed) or the refresh window closes. When the window
// closes the task completes silently; later status updates happen through the
// user-triggered refresh path (asset list/detail queries).
func runRefreshAssetReplicaTask(ctx context.Context, task *model.AssetLibraryTask) error {
	replica, err := model.GetUserAssetReplica(task.TargetId, task.ChannelId)
	if err != nil {
		// Asset or replica deleted while the task was queued.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if replica.State != model.AssetReplicaStateProcessing || replica.UpstreamAssetId == "" {
		return nil
	}
	config, err := model.GetChannelAssetConfig(task.ChannelId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if !config.Enabled {
		return nil
	}
	backend, err := assetLibraryBackendForChannel(task.ChannelId)
	if err != nil {
		return err
	}
	lock := getAssetLibraryChannelLock(task.ChannelId)
	lock.Lock()
	defer lock.Unlock()
	details, err := backend.GetAsset(ctx, config, replica.UpstreamAssetId)
	if err != nil {
		if replicaRefreshWindowClosed(task) {
			return nil
		}
		return err
	}
	replica.UpstreamStatus = details.Status
	replica.State = assetReplicaStateForStatus(details.Status)
	replica.LastInferenceTime = details.LastInferenceTime
	replica.LastErrorCode = ""
	replica.LastError = ""
	if details.Error != nil {
		replica.LastErrorCode = details.Error.Code
		replica.LastError = assetLibraryStoredError(errors.New(details.Error.Message))
	}
	if err := model.SaveUserAssetReplica(replica); err != nil {
		return err
	}
	if replica.State != model.AssetReplicaStateProcessing {
		return nil
	}
	if replicaRefreshWindowClosed(task) {
		return nil
	}
	return errAssetLibraryTaskRetryLater
}

func replicaRefreshWindowClosed(task *model.AssetLibraryTask) bool {
	return time.Now().Unix()-task.CreatedTime >= int64(assetLibraryReplicaRefreshWindow/time.Second)
}

// assetLibraryRetryDelaySeconds mirrors the model backoff schedule for
// polling tasks that reschedule themselves.
func assetLibraryRetryDelaySeconds(attempts int) int64 {
	if attempts <= 1 {
		return 60
	}
	backoff := int64(60)
	for i := 1; i < attempts && backoff < 3600; i++ {
		backoff *= 2
	}
	if backoff > 3600 {
		backoff = 3600
	}
	return backoff
}

func isAssetLibraryRecordNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

func runDeleteAssetReplicasTask(ctx context.Context, task *model.AssetLibraryTask) error {
	var refs []upstreamReplicaRef
	if task.Payload != "" {
		if err := json.Unmarshal([]byte(task.Payload), &refs); err != nil {
			return err
		}
	} else {
		replicas, err := model.ListUserAssetReplicas(task.TargetId)
		if err != nil {
			return err
		}
		for _, replica := range replicas {
			if replica.UpstreamAssetId != "" {
				refs = append(refs, upstreamReplicaRef{ChannelId: replica.ChannelId, UpstreamId: replica.UpstreamAssetId})
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	var firstErr error
	for _, ref := range refs {
		if err := deleteUpstreamAssetReplica(ctx, ref.ChannelId, ref.UpstreamId); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		return firstErr
	}
	// All upstream replicas are gone; remove local replica rows if any remain.
	return model.DeleteUserAssetReplicasByAsset(task.TargetId)
}

func runDeleteGroupReplicasTask(ctx context.Context, task *model.AssetLibraryTask) error {
	var refs []upstreamReplicaRef
	if task.Payload != "" {
		if err := json.Unmarshal([]byte(task.Payload), &refs); err != nil {
			return err
		}
	} else {
		replicas, err := model.ListUserAssetGroupReplicas(task.TargetId)
		if err != nil {
			return err
		}
		for _, replica := range replicas {
			if replica.UpstreamGroupId != "" {
				refs = append(refs, upstreamReplicaRef{ChannelId: replica.ChannelId, UpstreamId: replica.UpstreamGroupId})
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	var firstErr error
	for _, ref := range refs {
		if err := deleteUpstreamGroupReplica(ctx, ref.ChannelId, ref.UpstreamId); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		return firstErr
	}
	return model.DeleteUserAssetGroupReplicasByGroup(task.TargetId)
}

// deleteUpstreamAssetReplica deletes one upstream asset replica, treating
// upstream "not found" as success.
func deleteUpstreamAssetReplica(ctx context.Context, channelId int, upstreamAssetId string) error {
	lock := getAssetLibraryChannelLock(channelId)
	lock.Lock()
	defer lock.Unlock()
	config, err := model.GetChannelAssetConfig(channelId)
	if err != nil {
		if isAssetLibraryRecordNotFound(err) {
			return nil
		}
		return err
	}
	upstreamConfig := *config
	upstreamConfig.Enabled = true
	backend, err := assetLibraryBackendForChannel(channelId)
	if err != nil {
		return err
	}
	err = backend.DeleteAsset(ctx, &upstreamConfig, upstreamAssetId)
	if isAssetLibraryNotFound(err) {
		return nil
	}
	return err
}

// deleteUpstreamGroupReplica deletes one upstream group replica, treating
// upstream "not found" as success.
func deleteUpstreamGroupReplica(ctx context.Context, channelId int, upstreamGroupId string) error {
	lock := getAssetLibraryChannelLock(channelId)
	lock.Lock()
	defer lock.Unlock()
	config, err := model.GetChannelAssetConfig(channelId)
	if err != nil {
		if isAssetLibraryRecordNotFound(err) {
			return nil
		}
		return err
	}
	upstreamConfig := *config
	upstreamConfig.Enabled = true
	backend, err := assetLibraryBackendForChannel(channelId)
	if err != nil {
		return err
	}
	err = backend.DeleteGroup(ctx, &upstreamConfig, upstreamGroupId)
	if isAssetLibraryNotFound(err) {
		return nil
	}
	return err
}

// EnqueueAssetLibrarySyncChannelTask schedules a full backfill for a channel.
func EnqueueAssetLibrarySyncChannelTask(channelId int) (int64, error) {
	return model.EnqueueAssetLibraryTask(model.AssetLibraryTaskTypeSyncChannel, channelId, "", "")
}

// EnqueueAssetLibraryReplicateAssetTask schedules a retry for an asset whose
// replication failed or was interrupted.
func EnqueueAssetLibraryReplicateAssetTask(assetId string) (int64, error) {
	return model.EnqueueAssetLibraryTask(model.AssetLibraryTaskTypeReplicateAsset, 0, assetId, "")
}

// EnqueueAssetLibraryReplicateGroupTask schedules a retry for a group whose
// replication failed or was interrupted.
func EnqueueAssetLibraryReplicateGroupTask(groupId string) (int64, error) {
	return model.EnqueueAssetLibraryTask(model.AssetLibraryTaskTypeReplicateGroup, 0, groupId, "")
}

// EnqueueAssetLibraryDeleteAssetTask schedules upstream deletion retries for
// the given replica refs after the local asset record has been removed.
func EnqueueAssetLibraryDeleteAssetTask(assetId string, refs []upstreamReplicaRef) (int64, error) {
	payload := ""
	if len(refs) > 0 {
		data, err := json.Marshal(refs)
		if err != nil {
			return 0, err
		}
		payload = string(data)
	}
	return model.EnqueueAssetLibraryTask(model.AssetLibraryTaskTypeDeleteAssetReplicas, 0, assetId, payload)
}

// EnqueueAssetLibraryDeleteGroupTask schedules upstream group deletion retries.
func EnqueueAssetLibraryDeleteGroupTask(groupId string, refs []upstreamReplicaRef) (int64, error) {
	payload := ""
	if len(refs) > 0 {
		data, err := json.Marshal(refs)
		if err != nil {
			return 0, err
		}
		payload = string(data)
	}
	return model.EnqueueAssetLibraryTask(model.AssetLibraryTaskTypeDeleteGroupReplicas, 0, groupId, payload)
}
