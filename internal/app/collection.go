package app

import (
	"fmt"
	"strconv"
	"time"

	"buff-go/internal/collection"
	steam "buff-go/internal/platform/steam"
	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
	"buff-go/internal/telemetry"
)

// platformSiteRegions 是平台站点地域的唯一来源。这是平台事实，与是否已接入采集无关：
// BUFF/IGXE 还没有调度档案，控制面仍要拦住国外节点绑国内站账号。
var platformSiteRegions = resource.PlatformTargetRegions()

// collectionProfiles 只包含真正接入采集的平台，调度器据此挑组合。
var collectionProfiles = map[collection.Platform]collection.PlatformProfile{
	collection.PlatformSteam: {
		TargetRegion:    platformSiteRegions["steam"],
		SummaryEndpoint: "market_summary",
		BidEndpoint:     "market_orderbook",
		RequestInterval: steamRequestInterval,
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
	scheduler, err := collection.NewScheduler(store, store, coordinator, store, fetcher, collection.SchedulerConfig{
		Profiles:       collectionProfiles,
		PageTimeout:    30 * time.Second,
		TransientRetry: 30 * time.Second,
		ClaimTimeout:   45 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("collection scheduler: %w", err)
	}
	cycleObserver, recoveryObserver := collectionDaemonObservers(telemetry.NewLog())
	daemon, err := collection.NewDaemon(scheduler, collection.DaemonConfig{
		Interval:                     steamRequestInterval,
		PriceTickMaintenanceInterval: 24 * time.Hour,
		ShutdownTimeout:              30 * time.Second,
		LockVerifyTimeout:            3 * time.Second,
		Guard:                        store,
		Observer:                     cycleObserver,
		RecoveryObserver:             recoveryObserver,
	})
	if err != nil {
		return nil, fmt.Errorf("collection daemon: %w", err)
	}
	return daemon, nil
}

func collectionDaemonObservers(recorder telemetry.Recorder) (func(collection.DaemonCycle), func(collection.RecoveryReport)) {
	emitFailure := func(source string, err error) {
		if err == nil {
			return
		}
		telemetry.Emit(recorder, telemetry.Event{
			Kind: telemetry.KindJobFail, Reason: telemetry.ReasonError,
			Source: source, Detail: err.Error(),
		})
	}
	cycle := func(result collection.DaemonCycle) {
		emitFailure("collection.daemon", result.Err)
		for _, target := range result.Report.Targets {
			emitFailure("collection.target", target.Err)
		}
		for _, worker := range result.Report.Workers {
			if worker.Err == nil {
				continue
			}
			source := "collection.worker"
			if worker.Endpoint != "" {
				source += "." + string(worker.Endpoint)
			}
			telemetry.Emit(recorder, telemetry.Event{
				Kind: telemetry.KindJobFail, Reason: telemetry.ReasonError,
				Platform: string(worker.Platform), Source: source, AppID: worker.AppID,
				WorkerID: strconv.FormatInt(int64(worker.CombinationID), 10),
				Account:  strconv.FormatInt(int64(worker.AccountID), 10),
				Proxy:    worker.ExitAddress.String(),
				JobKey:   strconv.FormatInt(int64(worker.TaskID), 10),
				Detail:   worker.Err.Error(),
			})
		}
	}
	recovery := func(report collection.RecoveryReport) {
		emitFailure("collection.recovery", report.Failures)
	}
	return cycle, recovery
}
