package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

const (
	AssetLibraryTaskStatePending = "pending"
	AssetLibraryTaskStateRunning = "running"
	AssetLibraryTaskStateDone    = "done"
	AssetLibraryTaskStateFailed  = "failed"
)

const (
	// AssetLibraryTaskTypeSyncChannel backfills all groups/assets onto one channel.
	AssetLibraryTaskTypeSyncChannel = "sync_channel"
	// AssetLibraryTaskTypeReplicateGroup retries group replication across channels.
	AssetLibraryTaskTypeReplicateGroup = "replicate_group"
	// AssetLibraryTaskTypeReplicateAsset retries asset replication across channels.
	AssetLibraryTaskTypeReplicateAsset = "replicate_asset"
	// AssetLibraryTaskTypeDeleteAssetReplicas retries upstream asset deletion.
	AssetLibraryTaskTypeDeleteAssetReplicas = "delete_asset_replicas"
	// AssetLibraryTaskTypeDeleteGroupReplicas retries upstream group deletion.
	AssetLibraryTaskTypeDeleteGroupReplicas = "delete_group_replicas"
	// AssetLibraryTaskTypeRefreshAssetReplica polls upstream asset status for
	// one processing replica until it becomes final or the refresh window
	// closes; after that, status updates rely on user-triggered refreshes.
	AssetLibraryTaskTypeRefreshAssetReplica = "refresh_asset_replica"
)

// AssetLibraryTask is a persistent background job for asset library
// replication/deletion work. It survives process restarts so interrupted
// backfills and failed upstream deletions are retried automatically.
type AssetLibraryTask struct {
	Id          int64  `json:"id" gorm:"primaryKey"`
	TaskType    string `json:"task_type" gorm:"type:varchar(32);not null;index:idx_asset_library_task_type"`
	ChannelId   int    `json:"channel_id" gorm:"not null;default:0"`
	TargetId    string `json:"target_id" gorm:"type:varchar(64);not null;default:''"`
	State       string `json:"state" gorm:"type:varchar(16);not null;index"`
	Attempts    int    `json:"attempts"`
	MaxAttempts int    `json:"max_attempts"`
	NextRunTime int64  `json:"next_run_time" gorm:"not null;index"`
	LastError   string `json:"last_error,omitempty" gorm:"type:text"`
	Payload     string `json:"-" gorm:"type:text"`
	CreatedTime int64  `json:"created_time" gorm:"autoCreateTime"`
	UpdatedTime int64  `json:"updated_time" gorm:"autoUpdateTime"`
}

func (AssetLibraryTask) TableName() string {
	return "asset_library_tasks"
}

func CreateAssetLibraryTask(task *AssetLibraryTask) error {
	if task.State == "" {
		task.State = AssetLibraryTaskStatePending
	}
	if task.MaxAttempts <= 0 {
		task.MaxAttempts = 8
	}
	if task.NextRunTime == 0 {
		task.NextRunTime = time.Now().Unix()
	}
	return DB.Create(task).Error
}

// EnqueueAssetLibraryTask creates a pending task unless an active (pending or
// running) task with the same type/channel/target already exists.
func EnqueueAssetLibraryTask(taskType string, channelId int, targetId string, payload string) (int64, error) {
	var existing AssetLibraryTask
	err := DB.Where(
		"task_type = ? AND channel_id = ? AND target_id = ? AND state IN ?",
		taskType, channelId, targetId, []string{AssetLibraryTaskStatePending, AssetLibraryTaskStateRunning},
	).First(&existing).Error
	if err == nil {
		return existing.Id, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	task := &AssetLibraryTask{
		TaskType:    taskType,
		ChannelId:   channelId,
		TargetId:    targetId,
		State:       AssetLibraryTaskStatePending,
		MaxAttempts: 8,
		NextRunTime: time.Now().Unix(),
		Payload:     payload,
	}
	if err := DB.Create(task).Error; err != nil {
		return 0, err
	}
	return task.Id, nil
}

// ClaimDueAssetLibraryTasks atomically moves due pending tasks to running so
// only one instance processes each task.
func ClaimDueAssetLibraryTasks(limit int) ([]AssetLibraryTask, error) {
	if limit <= 0 {
		limit = 10
	}
	var due []AssetLibraryTask
	now := time.Now().Unix()
	if err := DB.Where("state = ? AND next_run_time <= ?", AssetLibraryTaskStatePending, now).
		Order("next_run_time ASC").Limit(limit).Find(&due).Error; err != nil {
		return nil, err
	}
	claimed := make([]AssetLibraryTask, 0, len(due))
	for i := range due {
		result := DB.Model(&AssetLibraryTask{}).
			Where("id = ? AND state = ?", due[i].Id, AssetLibraryTaskStatePending).
			Updates(map[string]any{"state": AssetLibraryTaskStateRunning, "attempts": due[i].Attempts + 1, "updated_time": now})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		due[i].State = AssetLibraryTaskStateRunning
		due[i].Attempts++
		claimed = append(claimed, due[i])
	}
	return claimed, nil
}

func CompleteAssetLibraryTask(taskId int64) error {
	return DB.Model(&AssetLibraryTask{}).Where("id = ?", taskId).
		Updates(map[string]any{"state": AssetLibraryTaskStateDone, "last_error": "", "next_run_time": 0}).Error
}

// RescheduleAssetLibraryTask moves a running task back to pending with the
// given next run time. Used by tasks that poll asynchronously (e.g. replica
// status refresh) without counting the pass as a failure.
func RescheduleAssetLibraryTask(taskId int64, nextRunTime int64) error {
	return DB.Model(&AssetLibraryTask{}).Where("id = ?", taskId).
		Updates(map[string]any{"state": AssetLibraryTaskStatePending, "next_run_time": nextRunTime, "last_error": ""}).Error
}

// FailAssetLibraryTask records the failure and schedules the next retry with
// exponential backoff, or marks the task dead when attempts are exhausted.
func FailAssetLibraryTask(taskId int64, attempts int, maxAttempts int, message string) error {
	now := time.Now().Unix()
	state := AssetLibraryTaskStatePending
	nextRun := now + assetLibraryTaskBackoffSeconds(attempts)
	if attempts >= maxAttempts {
		state = AssetLibraryTaskStateFailed
		nextRun = 0
	}
	return DB.Model(&AssetLibraryTask{}).Where("id = ?", taskId).Updates(map[string]any{
		"state":        state,
		"next_run_time": nextRun,
		"last_error":   message,
	}).Error
}

func assetLibraryTaskBackoffSeconds(attempts int) int64 {
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

// RecoverStaleAssetLibraryTasks resets running tasks back to pending. Called
// at startup so tasks interrupted by a restart are retried.
func RecoverStaleAssetLibraryTasks() (int64, error) {
	result := DB.Model(&AssetLibraryTask{}).
		Where("state = ?", AssetLibraryTaskStateRunning).
		Updates(map[string]any{"state": AssetLibraryTaskStatePending, "next_run_time": time.Now().Unix()})
	return result.RowsAffected, result.Error
}

func GetAssetLibraryTaskById(taskId int64) (*AssetLibraryTask, error) {
	var task AssetLibraryTask
	if err := DB.First(&task, "id = ?", taskId).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

type AssetLibraryTaskListParams struct {
	State     string
	TaskType  string
	ChannelId int
	Page      int
	PageSize  int
}

func ListAssetLibraryTasks(params AssetLibraryTaskListParams) ([]AssetLibraryTask, int64, error) {
	query := DB.Model(&AssetLibraryTask{})
	if params.State != "" {
		query = query.Where("state = ?", params.State)
	}
	if params.TaskType != "" {
		query = query.Where("task_type = ?", params.TaskType)
	}
	if params.ChannelId > 0 {
		query = query.Where("channel_id = ?", params.ChannelId)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	pageSize := params.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	page := params.Page
	if page <= 0 {
		page = 1
	}
	var tasks []AssetLibraryTask
	err := query.Order("updated_time DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&tasks).Error
	return tasks, total, err
}

// RetryAssetLibraryTask re-arms a failed (dead) task for immediate execution.
func RetryAssetLibraryTask(taskId int64) error {
	result := DB.Model(&AssetLibraryTask{}).
		Where("id = ? AND state = ?", taskId, AssetLibraryTaskStateFailed).
		Updates(map[string]any{
			"state": AssetLibraryTaskStatePending, "attempts": 0, "next_run_time": time.Now().Unix(), "last_error": "",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// PruneFinishedAssetLibraryTasks removes done tasks older than retention.
func PruneFinishedAssetLibraryTasks(retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention).Unix()
	result := DB.Where("state = ? AND updated_time < ?", AssetLibraryTaskStateDone, cutoff).
		Delete(&AssetLibraryTask{})
	return result.RowsAffected, result.Error
}
