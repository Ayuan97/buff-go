package app

import (
	"fmt"
	"time"

	"buff-go/internal/collection"
	steam "buff-go/internal/platform/steam"
	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

func newCollectionDaemon(store *postgres.Store, coordinator *resource.Coordinator) (*collection.Daemon, error) {
	fetcher, err := steam.NewFetcher(steam.Options{
		Opener:   steam.NewCoordinatorOpener(coordinator),
		Catalog:  store,
		Sessions: steam.NewCoordinatorRecorder(coordinator),
	})
	if err != nil {
		return nil, fmt.Errorf("steam fetcher: %w", err)
	}
	scheduler, err := collection.NewScheduler(store, coordinator, store, fetcher, collection.SchedulerConfig{
		Profiles: map[collection.Platform]collection.PlatformProfile{
			collection.PlatformSteam: {
				TargetRegion:    resource.TargetRegionForeign,
				SummaryEndpoint: "market_summary",
				BidEndpoint:     "market_orderbook",
			},
		},
		PageTimeout:     30 * time.Second,
		ResourceWait:    15 * time.Second,
		PollInterval:    500 * time.Millisecond,
		TransientRetry:  30 * time.Second,
		SummaryPeriod:   time.Hour,
		MaxParallelRuns: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("collection scheduler: %w", err)
	}
	daemon, err := collection.NewDaemon(scheduler, collection.DaemonConfig{
		Interval:          2 * time.Second,
		ShutdownTimeout:   30 * time.Second,
		LockVerifyTimeout: 3 * time.Second,
		MaxResumeAge:      24 * time.Hour,
		Guard:             store,
	})
	if err != nil {
		return nil, fmt.Errorf("collection daemon: %w", err)
	}
	return daemon, nil
}
