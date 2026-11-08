package service

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/model"
)

const (
	// walletSnapshotWindow is how long after a midnight in Beijing time the
	// closing balances of the day before may still be recorded. The
	// scheduler records them within a pass or two; later, a balance would
	// hold too much of the new day, so a day the master node missed keeps
	// no closing balance rather than a wrong one.
	walletSnapshotWindow = time.Hour
	// walletSnapshotRetryDelay spaces the retries of a failed run.
	walletSnapshotRetryDelay = time.Minute
)

// walletBalanceSnapshotHandler records, after each midnight in Beijing time,
// the closing balances of the day that ended (see
// model.RecordWalletBalanceSnapshots), the balances the statement shows.
// The balance is users.quota as the database holds it: with batch updates
// on, a call's charge reaches it a few seconds late, about as long as the
// run waits for the scheduler after midnight, so the error stays at the
// seconds around midnight either way.
type walletBalanceSnapshotHandler struct{}

// WalletBalanceSnapshotPayload names the day, YYYY-MM-DD in Beijing time,
// whose closing balances a run records.
type WalletBalanceSnapshotPayload struct {
	Day string `json:"day"`
}

// WalletBalanceSnapshotResult reports a run: the snapshots it wrote, or that
// the day had been recorded already.
type WalletBalanceSnapshotResult struct {
	Day             string `json:"day"`
	Users           int64  `json:"users"`
	AlreadyRecorded bool   `json:"already_recorded,omitempty"`
}

func init() {
	RegisterSystemTaskHandler(walletBalanceSnapshotHandler{})
}

func (walletBalanceSnapshotHandler) Type() string {
	return model.SystemTaskTypeWalletBalanceSnapshot
}

func (walletBalanceSnapshotHandler) Enabled() bool { return true }

// Interval is unused: Due follows the calendar.
func (walletBalanceSnapshotHandler) Interval() time.Duration { return 24 * time.Hour }

func (walletBalanceSnapshotHandler) NewPayload() any {
	return WalletBalanceSnapshotPayload{Day: model.WalletDay(time.Now().In(model.WalletDayZone).AddDate(0, 0, -1))}
}

// Due reports a run in the window after midnight while no run was created
// since then, or a minute after one that failed.
func (walletBalanceSnapshotHandler) Due(latest *model.SystemTask, now time.Time) bool {
	local := now.In(model.WalletDayZone)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, model.WalletDayZone)
	if now.Sub(midnight) >= walletSnapshotWindow {
		return false
	}
	if latest == nil || latest.CreatedAt < midnight.Unix() {
		return true
	}
	return latest.Status == model.SystemTaskStatusFailed && now.Unix()-latest.UpdatedAt >= int64(walletSnapshotRetryDelay.Seconds())
}

func (walletBalanceSnapshotHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := WalletBalanceSnapshotPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	day, err := time.ParseInLocation(time.DateOnly, payload.Day, model.WalletDayZone)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if sinceClose := time.Since(day.AddDate(0, 0, 1)); sinceClose < 0 || sinceClose >= walletSnapshotWindow {
		failSystemTask(task, runnerID, fmt.Errorf("the closing balances of %s can only be recorded in the hour after it ends", payload.Day))
		return
	}
	users, recorded, err := model.RecordWalletBalanceSnapshots(payload.Day)
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	result := WalletBalanceSnapshotResult{Day: payload.Day, Users: users, AlreadyRecorded: !recorded}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}
