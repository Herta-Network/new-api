package payment

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
	"github.com/bytedance/gopkg/util/gopool"
)

const (
	reconcileTickInterval = time.Minute
	reconcileBatchSize    = 100
	// reconcileMaxDelay caps the backoff between gateway checks of an
	// expired order the gateway cannot confirm yet.
	reconcileMaxDelay = 6 * time.Hour
)

var (
	reconcileOnce    sync.Once
	reconcileRunning atomic.Bool
)

// StartPaymentReconcileTask closes the expired pending orders on the master
// node, asking each order's gateway first.
func StartPaymentReconcileTask() {
	reconcileOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), common.LogText("payment reconcile task started: tick=%s", reconcileTickInterval))
			ticker := time.NewTicker(reconcileTickInterval)
			defer ticker.Stop()
			for range ticker.C {
				runPaymentReconcileOnce(context.Background(), time.Now())
			}
		})
	})
}

// runPaymentReconcileOnce checks one batch of expired pending orders whose
// check is due. An order the gateway cannot confirm yet is checked again
// after a delay that doubles with every attempt, up to reconcileMaxDelay.
func runPaymentReconcileOnce(ctx context.Context, now time.Time) {
	if !reconcileRunning.CompareAndSwap(false, true) {
		return
	}
	defer reconcileRunning.Store(false)
	orders, err := store.ListDueOrders(now.Unix(), reconcileBatchSize)
	if err != nil {
		logger.LogWarn(ctx, common.LogText("payment reconcile task failed: %v", err))
		return
	}
	for _, order := range orders {
		_, err := CloseOrder(ctx, order, CloseBySystem)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrCloseUnconfirmed) {
			logger.LogWarn(ctx, common.LogText("payment order %s could not be reconciled: %v", order.OrderNo, err))
		}
		attempts := order.CheckAttempts + 1
		delay := reconcileMaxDelay
		if attempts < 16 {
			delay = min(time.Minute<<attempts, reconcileMaxDelay)
		}
		if err := store.DeferOrderCheck(order.Id, attempts, now.Add(delay).Unix()); err != nil {
			logger.LogWarn(ctx, common.LogText("payment order %s check could not be deferred: %v", order.OrderNo, err))
		}
	}
}
