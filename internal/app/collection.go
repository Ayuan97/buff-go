package app

import (
	"fmt"
	"time"

	"buff-go/internal/collection"
	steam "buff-go/internal/platform/steam"
	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

// platformSiteRegions 是平台站点地域的唯一来源。这是平台事实，与是否已接入采集无关：
// BUFF/IGXE 还没有调度档案，控制面仍要拦住把国外节点划给它们这类必然采不动的配置。
var platformSiteRegions = map[resource.Platform]resource.TargetRegion{
	"steam": resource.TargetRegionForeign,
	"buff":  resource.TargetRegionDomestic,
	"igxe":  resource.TargetRegionDomestic,
}

// collectionProfiles 只包含真正接入采集的平台，调度器据此挑组合。
var collectionProfiles = map[collection.Platform]collection.PlatformProfile{
	collection.PlatformSteam: {
		TargetRegion:    platformSiteRegions["steam"],
		SummaryEndpoint: "market_summary",
		BidEndpoint:     "market_orderbook",
	},
}

// platformTargetRegions 交给控制面做地域校验，副本避免被调用方改动。
func platformTargetRegions() map[resource.Platform]resource.TargetRegion {
	regions := make(map[resource.Platform]resource.TargetRegion, len(platformSiteRegions))
	for platform, region := range platformSiteRegions {
		regions[platform] = region
	}
	return regions
}

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
		Profiles:        collectionProfiles,
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
