package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/pgtest"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSystemTaskRegistry swaps the package registry for the given handlers for
// the duration of a test and restores the original registry afterward.
func withSystemTaskRegistry(t *testing.T, handlers ...SystemTaskHandler) {
	t.Helper()
	systemTaskHandlersMu.Lock()
	saved := systemTaskHandlers
	systemTaskHandlers = map[string]SystemTaskHandler{}
	for _, h := range handlers {
		systemTaskHandlers[h.Type()] = h
	}
	systemTaskHandlersMu.Unlock()
	t.Cleanup(func() {
		systemTaskHandlersMu.Lock()
		systemTaskHandlers = saved
		systemTaskHandlersMu.Unlock()
	})
}

type stubScheduledHandler struct {
	taskType string
	enabled  bool
	interval time.Duration
	onRun    func(ctx context.Context, task *model.SystemTask, runnerID string)
}

type stubSystemTaskRunResult struct {
	taskID   string
	taskType string
	err      error
}

func (h *stubScheduledHandler) Type() string { return h.taskType }

func (h *stubScheduledHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if h.onRun != nil {
		h.onRun(ctx, task, runnerID)
	}
}

func (h *stubScheduledHandler) Enabled() bool           { return h.enabled }
func (h *stubScheduledHandler) Interval() time.Duration { return h.interval }
func (h *stubScheduledHandler) NewPayload() any         { return nil }

func countSystemTasks(t *testing.T, taskType string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("type = ?", taskType).Count(&count).Error)
	return count
}

func TestSystemTaskSchedulerCreatesWhenDueAndDedups(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_scheduled", enabled: true, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// An active (pending) row already exists, so a second pass must not create
	// another row.
	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Finish the run; with a fresh updated_at the next run is not due yet.
	latest, err := model.GetLatestSystemTask(handler.taskType)
	require.NoError(t, err)
	require.NotNil(t, latest)
	_, claimed, err := model.ClaimSystemTask(latest.ID, handler.taskType, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(latest.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Backdate the finished row beyond the interval -> the job becomes due again.
	require.NoError(t, model.DB.Model(&model.SystemTask{}).
		Where("task_id = ?", latest.TaskID).
		Update("updated_at", common.GetTimestamp()-120).Error)

	runSystemTaskScheduler()
	require.Equal(t, int64(2), countSystemTasks(t, handler.taskType))
}

func TestSystemTaskSchedulerSkipsDisabled(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_disabled", enabled: false, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	assert.Equal(t, int64(0), countSystemTasks(t, handler.taskType))
}

type stubCalendarHandler struct {
	stubScheduledHandler
	due bool
}

func (h *stubCalendarHandler) Due(*model.SystemTask, time.Time) bool { return h.due }

// A calendar handler runs when Due says so, not when its interval elapsed.
func TestSystemTaskSchedulerAsksCalendarHandlers(t *testing.T) {
	truncate(t)
	handler := &stubCalendarHandler{stubScheduledHandler: stubScheduledHandler{taskType: "test_calendar", enabled: true, interval: time.Nanosecond}}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	assert.Equal(t, int64(0), countSystemTasks(t, handler.taskType))
	handler.due = true
	runSystemTaskScheduler()
	assert.Equal(t, int64(1), countSystemTasks(t, handler.taskType))
}

// The closing balances of a day are recorded once, in the hour after
// midnight in Beijing time, with a retry a minute after a failed run.
func TestWalletBalanceSnapshotIsDueOnceAfterMidnight(t *testing.T) {
	midnight := time.Date(2026, time.November, 8, 0, 0, 0, 0, model.WalletDayZone)
	at := func(d time.Duration) int64 { return midnight.Add(d).Unix() }
	task := func(status model.SystemTaskStatus, created, updated time.Duration) *model.SystemTask {
		return &model.SystemTask{Status: status, CreatedAt: at(created), UpdatedAt: at(updated)}
	}
	tests := []struct {
		name   string
		latest *model.SystemTask
		now    time.Duration
		due    bool
	}{
		{"first run after midnight", nil, 10 * time.Second, true},
		{"the day before was recorded", task(model.SystemTaskStatusSucceeded, -24*time.Hour+5*time.Second, -24*time.Hour+6*time.Second), 10 * time.Second, true},
		{"today's run recorded it", task(model.SystemTaskStatusSucceeded, 10*time.Second, 11*time.Second), 30 * time.Minute, false},
		{"a failed run waits a minute", task(model.SystemTaskStatusFailed, 10*time.Second, 11*time.Second), 40 * time.Second, false},
		{"a failed run is retried", task(model.SystemTaskStatusFailed, 10*time.Second, 11*time.Second), 2 * time.Minute, true},
		{"past the window", nil, time.Hour, false},
		{"before midnight", nil, -time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.due, walletBalanceSnapshotHandler{}.Due(tt.latest, midnight.Add(tt.now)))
		})
	}
}

func TestSystemTaskClaimPassDispatchesByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 1)
	handler := &stubScheduledHandler{
		taskType: "test_dispatch",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskType: task.Type,
				err:      model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handler)

	_, err := model.CreateSystemTask(handler.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	select {
	case got := <-ran:
		require.NoError(t, got.err)
		assert.Equal(t, handler.taskType, got.taskType)
	case <-time.After(2 * time.Second):
		t.Fatal("claimed task was not dispatched to its handler")
	}

	require.Eventually(t, func() bool {
		latest, err := model.GetLatestSystemTask(handler.taskType)
		return err == nil && latest != nil && latest.Status == model.SystemTaskStatusSucceeded
	}, 2*time.Second, 20*time.Millisecond)
}

func TestSystemTaskClaimPassDispatchesEarliestPendingByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 2)
	handlerA := &stubScheduledHandler{
		taskType: "test_dispatch_a",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	handlerB := &stubScheduledHandler{
		taskType: "test_dispatch_b",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handlerA, handlerB)

	firstA, err := model.CreateSystemTask(handlerA.taskType, nil, nil)
	require.NoError(t, err)
	secondTaskID, err := model.GenerateSystemTaskID()
	require.NoError(t, err)
	secondA := &model.SystemTask{
		TaskID: secondTaskID,
		Type:   handlerA.taskType,
		Status: model.SystemTaskStatusPending,
	}
	require.NoError(t, model.DB.Create(secondA).Error)
	firstB, err := model.CreateSystemTask(handlerB.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	got := map[string]bool{}
	for range 2 {
		select {
		case result := <-ran:
			require.NoError(t, result.err)
			got[result.taskID] = true
		case <-time.After(2 * time.Second):
			t.Fatal("claimed tasks were not dispatched to their handlers")
		}
	}

	assert.True(t, got[firstA.TaskID])
	assert.True(t, got[firstB.TaskID])
	assert.False(t, got[secondA.TaskID])

	require.Eventually(t, func() bool {
		reloaded, err := model.GetSystemTaskByTaskID(secondA.TaskID)
		return err == nil && reloaded != nil && reloaded.Status == model.SystemTaskStatusPending
	}, 2*time.Second, 20*time.Millisecond)
}

func TestEnqueueSystemTaskReportsCreatedAndExistingActive(t *testing.T) {
	truncate(t)

	first, created, err := EnqueueSystemTask("test_enqueue", map[string]bool{"manual": true})
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, first)

	existing, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.False(t, created)
	require.NotNil(t, existing)
	assert.Equal(t, first.TaskID, existing.TaskID)

	_, claimed, err := model.ClaimSystemTask(first.ID, first.Type, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(first.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	second, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, second)
	assert.NotEqual(t, first.TaskID, second.TaskID)
}

// Log retention, set through the option API, schedules nothing at 0. Set to N
// days, the daily log_cleanup run deletes the logs (here in a separate log
// database) and the finished task records created more than N days ago, and
// keeps the rest.
func TestLogRetentionDeletesOldLogsAndFinishedTasks(t *testing.T) {
	truncate(t)
	logDB := pgtest.NewSchemaDB(t)
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	previousLogDB := model.LOG_DB
	model.LOG_DB = logDB
	t.Cleanup(func() { model.LOG_DB = previousLogDB })
	previousOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previousOptionMap })
	logSetting := operation_setting.GetLogSetting()
	previousDays := logSetting.RetentionDays
	t.Cleanup(func() { logSetting.RetentionDays = previousDays })
	withSystemTaskRegistry(t, logCleanupHandler{})
	require.NoError(t, logDB.Create(&model.Log{UserId: 1, CreatedAt: 1, Type: model.LogTypeConsume}).Error)

	for _, invalid := range []string{"-1", "1.5", "36501", ""} {
		assert.Error(t, model.UpdateOption(operation_setting.LogRetentionDaysOptionKey, invalid), invalid)
	}
	require.NoError(t, model.UpdateOption(operation_setting.LogRetentionDaysOptionKey, "0"))
	runSystemTaskScheduler()
	assert.Zero(t, countSystemTasks(t, model.SystemTaskTypeLogCleanup))

	require.NoError(t, model.UpdateOption(operation_setting.LogRetentionDaysOptionKey, "30"))
	require.Equal(t, 30, logSetting.RetentionDays)
	before := common.GetTimestamp()
	runSystemTaskScheduler()
	after := common.GetTimestamp()
	scheduled, err := model.GetActiveSystemTask(model.SystemTaskTypeLogCleanup)
	require.NoError(t, err)
	require.NotNil(t, scheduled)
	var payload LogCleanupPayload
	require.NoError(t, scheduled.DecodePayload(&payload))
	const thirtyDays = 30 * 24 * 60 * 60
	assert.True(t, payload.IncludeTasks)
	assert.GreaterOrEqual(t, payload.TargetTimestamp, before-thirtyDays)
	assert.LessOrEqual(t, payload.TargetTimestamp, after-thirtyDays)

	target := payload.TargetTimestamp
	require.NoError(t, logDB.Create(&[]model.Log{
		{UserId: 1, CreatedAt: target - 1, Type: model.LogTypeConsume},
		{UserId: 1, CreatedAt: target, Type: model.LogTypeConsume},
	}).Error)
	var tasks []model.Task
	for _, status := range []model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailure, model.TaskStatusNotStart,
		model.TaskStatusSubmitted, model.TaskStatusQueued, model.TaskStatusInProgress, model.TaskStatusUnknown} {
		tasks = append(tasks, model.Task{TaskID: "old-" + string(status), CreatedAt: target - 1, Status: status})
	}
	tasks = append(tasks, model.Task{TaskID: "boundary", CreatedAt: target, Status: model.TaskStatusSuccess})
	require.NoError(t, model.DB.Create(&tasks).Error)
	require.NoError(t, model.DB.Create(&[]model.Midjourney{
		{MjId: "old-finished", SubmitTime: (target - 1) * 1000, Progress: "100%", Status: "SUCCESS"},
		{MjId: "old-running", SubmitTime: (target - 1) * 1000, Progress: "50%", Status: "IN_PROGRESS"},
		{MjId: "boundary", SubmitTime: target * 1000, Progress: "100%", Status: "FAILURE"},
	}).Error)

	claimed, ok, err := model.ClaimSystemTask(scheduled.ID, model.SystemTaskTypeLogCleanup, "runner-retention", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, ok)
	logCleanupHandler{}.Run(context.Background(), claimed, "runner-retention")

	finished, err := model.GetSystemTaskByTaskID(scheduled.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status, finished.Error)
	assert.JSONEq(t, `{"deleted_count":2,"task_deleted_count":3}`, finished.Result)
	var keptLogs []int64
	require.NoError(t, logDB.Model(&model.Log{}).Pluck("created_at", &keptLogs).Error)
	assert.Equal(t, []int64{target}, keptLogs)
	var keptTasks []string
	require.NoError(t, model.DB.Model(&model.Task{}).Order("task_id").Pluck("task_id", &keptTasks).Error)
	assert.Equal(t, []string{"boundary", "old-IN_PROGRESS", "old-NOT_START", "old-QUEUED", "old-SUBMITTED", "old-UNKNOWN"}, keptTasks)
	var keptMidjourneys []string
	require.NoError(t, model.DB.Model(&model.Midjourney{}).Order("mj_id").Pluck("mj_id", &keptMidjourneys).Error)
	assert.Equal(t, []string{"boundary", "old-running"}, keptMidjourneys)
}

// Task records are deleted in batches like logs: one call removes at most its
// limit of the oldest finished records, and repeated calls remove the rest.
func TestFinishedTaskRecordsAreDeletedInBatches(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.Create(&[]model.Task{
		{TaskID: "c", CreatedAt: 102, Status: model.TaskStatusSuccess},
		{TaskID: "a", CreatedAt: 100, Status: model.TaskStatusFailure},
		{TaskID: "b", CreatedAt: 101, Status: model.TaskStatusSuccess},
		{TaskID: "running", CreatedAt: 99, Status: model.TaskStatusInProgress},
	}).Error)
	require.NoError(t, model.DB.Create(&[]model.Midjourney{
		{MjId: "c", SubmitTime: 102_000, Progress: "100%"},
		{MjId: "a", SubmitTime: 100_000, Progress: "100%"},
		{MjId: "b", SubmitTime: 101_000, Progress: "100%"},
		{MjId: "running", SubmitTime: 99_000, Progress: "0%"},
	}).Error)

	for _, tc := range []struct {
		name        string
		deleteBatch func(context.Context, int64, int) (int64, error)
		remaining   func() []string
	}{
		{"tasks", model.DeleteFinishedTaskBatch, func() []string {
			var ids []string
			require.NoError(t, model.DB.Model(&model.Task{}).Order("task_id").Pluck("task_id", &ids).Error)
			return ids
		}},
		{"midjourney", model.DeleteFinishedMidjourneyBatch, func() []string {
			var ids []string
			require.NoError(t, model.DB.Model(&model.Midjourney{}).Order("mj_id").Pluck("mj_id", &ids).Error)
			return ids
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleted, err := tc.deleteBatch(context.Background(), 200, 2)
			require.NoError(t, err)
			assert.EqualValues(t, 2, deleted)
			assert.Equal(t, []string{"c", "running"}, tc.remaining())

			deleted, err = tc.deleteBatch(context.Background(), 200, 2)
			require.NoError(t, err)
			assert.EqualValues(t, 1, deleted)
			deleted, err = tc.deleteBatch(context.Background(), 200, 2)
			require.NoError(t, err)
			assert.Zero(t, deleted)
			assert.Equal(t, []string{"running"}, tc.remaining())
		})
	}
}
