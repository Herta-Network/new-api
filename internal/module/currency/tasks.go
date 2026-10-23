package currency

import (
	"context"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	rateRefreshInterval = 24 * time.Hour
	// siteRatePublishInterval is how soon a node shows amounts at a rate
	// another node fetched or an override saved elsewhere.
	siteRatePublishInterval = time.Minute
)

var (
	rateRefreshOnce sync.Once
	siteRateOnce    sync.Once
)

// StartExchangeRateRefreshTask refreshes the exchange rates the site needs
// on the master node, at start and every day.
func StartExchangeRateRefreshTask() {
	rateRefreshOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			ticker := time.NewTicker(rateRefreshInterval)
			defer ticker.Stop()
			for ; true; <-ticker.C {
				if err := RefreshRates(context.Background()); err != nil {
					logger.LogWarn(context.Background(), common.LogText("exchange rate refresh failed: %v", err))
				}
			}
		})
	})
}

// StartSiteCurrencyRateTask publishes the rate amounts are shown at on
// every node, at start and every minute.
func StartSiteCurrencyRateTask() {
	siteRateOnce.Do(func() {
		PublishSiteCurrencyRate()
		gopool.Go(func() {
			ticker := time.NewTicker(siteRatePublishInterval)
			defer ticker.Stop()
			for range ticker.C {
				PublishSiteCurrencyRate()
			}
		})
	})
}
