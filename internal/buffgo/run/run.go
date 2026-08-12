// Package run provides the single-process buffgo daemon loop.
package run

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"buff-go/internal/buffgo/config"
	"buff-go/internal/buffgo/pipeline"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/telemetry"

	"github.com/go-redis/redis/v8"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Options filters the configured jobs run by this process.
type Options struct {
	ConfigPath string
	// Sources is a comma-separated allowlist such as "steam.ask,buff.ask".
	Sources string
	// AppID restricts the process to one enabled game; zero runs all enabled games.
	AppID int64
}

type scheduledJob struct {
	Spec     source.JobSpec
	Interval time.Duration
}

type jobExecutor func(context.Context, source.JobSpec) (nextStart int, err error)

type runtimeResources struct {
	once              sync.Once
	redis             *redis.Client
	stopObservability func()
}

func (r *runtimeResources) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.redis != nil {
			_ = r.redis.Close()
		}
		if r.stopObservability != nil {
			r.stopObservability()
		}
	})
}

// Run loads config and runs every enabled job in its own fixed-delay loop.
// A job never overlaps itself: its interval starts after the previous run ends.
func Run(ctx context.Context, opt Options) error {
	cfg, err := config.Load(opt.ConfigPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if opt.AppID > 0 && !cfg.HasAppID(opt.AppID) {
		return fmt.Errorf("appid=%d not in enabled_appids=%v", opt.AppID, cfg.Games.EnabledAppIDs)
	}

	mem, stopObs := installProcessObservability()
	observabilityOwned := true
	defer func() {
		if observabilityOwned {
			stopObs()
		}
	}()

	if err := pingPostgres(ctx, cfg.Database.DSN); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if err := pingRedis(ctx, cfg.Redis); err != nil {
		return fmt.Errorf("redis: %w", err)
	}

	jobs, err := scheduledJobsFromConfig(cfg, opt)
	if err != nil {
		return err
	}
	rdb := openRedisClient(cfg.Redis)

	execute := func(runCtx context.Context, spec source.JobSpec) (int, error) {
		return executeJob(runCtx, cfg, rdb, spec)
	}
	observabilityOwned = false
	return runStartedRuntime(ctx, opt, cfg.Games.EnabledAppIDs, jobs, execute, mem, rdb, stopObs)
}

func runStartedRuntime(ctx context.Context, opt Options, enabledAppIDs []int64, jobs []scheduledJob, execute jobExecutor, mem *telemetry.Memory, rdb *redis.Client, stopObs func()) error {
	resources := &runtimeResources{redis: rdb, stopObservability: stopObs}
	defer resources.Close()

	if len(jobs) == 0 {
		log.Printf("buffgo daemon: no enabled jobs sources=%q appid=%d", opt.Sources, opt.AppID)
		<-ctx.Done()
		return nil
	}

	var wg sync.WaitGroup
	for _, job := range jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			runJobLoop(ctx, job, execute)
		}()
	}
	log.Printf("buffgo daemon: started jobs=%d enabled_appids=%v", len(jobs), enabledAppIDs)
	<-ctx.Done()
	wg.Wait()
	logObservSummary(mem)
	return nil
}

func scheduledJobsFromConfig(cfg *config.Config, opt Options) ([]scheduledJob, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	specs := pipeline.JobsFromConfigAppID(cfg, opt.Sources, opt.AppID)
	specs = append(specs, pipeline.BuffJobsFromConfigAppID(cfg, opt.Sources, opt.AppID)...)
	jobs := make([]scheduledJob, 0, len(specs))
	for _, spec := range specs {
		jobCfg, ok := cfg.Jobs[spec.Key]
		if !ok {
			return nil, fmt.Errorf("job config missing for %s", spec.Key)
		}
		interval, err := jobCfg.IntervalDuration()
		if err != nil {
			return nil, fmt.Errorf("jobs.%s: %w", spec.Key, err)
		}
		jobs = append(jobs, scheduledJob{Spec: spec, Interval: interval})
	}
	return jobs, nil
}

func runJobLoop(ctx context.Context, job scheduledJob, execute jobExecutor) {
	nextStart := job.Spec.Start
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		spec := job.Spec
		spec.Start = nextStart
		resume, err := execute(ctx, spec)
		nextStart = resume
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("buffgo daemon: job_ref=%s appid=%d error_ref=%s",
				telemetry.SafeJobRef(spec.Key), spec.AppID, telemetry.SafeErrorRef(err))
		}

		timer := time.NewTimer(job.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func executeJob(ctx context.Context, cfg *config.Config, rdb *redis.Client, spec source.JobSpec) (int, error) {
	nextStart := spec.Start
	switch spec.SourceName() {
	case source.NameSteamAsk:
		results, err := pipeline.RunSteamSellJobs(ctx, cfg, []source.JobSpec{spec}, pipeline.SteamSellRunOptions{}, pipeline.SteamSellRuntime{Redis: rdb}, nil)
		if len(results) > 0 {
			if results[0].Completeness == pipeline.CompletenessPartial {
				nextStart = results[0].NextStart
			} else {
				nextStart = 0
			}
		}
		if err != nil {
			return nextStart, err
		}
	case source.NameBuffAsk:
		results, err := pipeline.RunBuffSellJobs(ctx, cfg, []source.JobSpec{spec}, pipeline.BuffSellRuntime{Redis: rdb}, nil, source.NameBuffAsk)
		if len(results) > 0 {
			if results[0].Completeness == pipeline.CompletenessPartial {
				nextStart = results[0].NextStart
			} else {
				nextStart = 0
			}
		}
		if err != nil {
			return nextStart, err
		}
	default:
		return nextStart, fmt.Errorf("unsupported source %q", spec.SourceName())
	}

	return nextStart, nil
}

func installProcessObservability() (*telemetry.Memory, func()) {
	prev := telemetry.Default()
	mem := telemetry.NewMemory(1024)
	telemetry.SetDefault(telemetry.Multi{mem, telemetry.NewLog()})
	return mem, func() { telemetry.SetDefault(prev) }
}

func logObservSummary(mem *telemetry.Memory) {
	if mem != nil {
		log.Printf("buffgo observ summary: %s", mem.Snapshot().String())
	}
}

func openRedisClient(rc config.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: rc.Addr, Password: rc.Password, DB: rc.DB})
}

func pingPostgres(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return db.PingContext(pingCtx)
}

func pingRedis(ctx context.Context, rc config.RedisConfig) error {
	rdb := openRedisClient(rc)
	defer rdb.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return rdb.Ping(pingCtx).Err()
}
