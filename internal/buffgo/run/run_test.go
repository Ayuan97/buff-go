package run

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"buff-go/internal/buffgo/config"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/telemetry"

	"github.com/go-redis/redis/v8"
)

func TestScheduledJobsFromConfig_UsesPerJobIntervals(t *testing.T) {
	cfg := &config.Config{
		Games: config.GamesConfig{EnabledAppIDs: []int64{config.DefaultRustAppID}},
		Jobs: map[string]config.JobConfig{
			"steam_sell_rust": {Platform: "steam", Side: "ask", AppID: config.DefaultRustAppID, Enabled: true, Interval: "60s"},
			"buff_sell_rust":  {Platform: "buff", Side: "ask", AppID: config.DefaultRustAppID, Enabled: true, Interval: "30s"},
		},
	}
	jobs, err := scheduledJobsFromConfig(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs=%d", len(jobs))
	}
	intervals := map[string]time.Duration{}
	for _, job := range jobs {
		intervals[job.Spec.SourceName()] = job.Interval
	}
	if intervals[source.NameSteamAsk] != time.Minute || intervals[source.NameBuffAsk] != 30*time.Second {
		t.Fatalf("intervals=%v", intervals)
	}
}

func TestRunJobLoop_SerialRetryAndCursor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	var active atomic.Int32
	var maxActive atomic.Int32
	var mu sync.Mutex
	var starts []int
	execute := func(_ context.Context, spec source.JobSpec) (int, error) {
		current := active.Add(1)
		for {
			old := maxActive.Load()
			if current <= old || maxActive.CompareAndSwap(old, current) {
				break
			}
		}
		mu.Lock()
		starts = append(starts, spec.Start)
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		active.Add(-1)

		switch calls.Add(1) {
		case 1:
			return 100, errors.New("temporary")
		case 2:
			return 0, nil
		default:
			cancel()
			return 0, nil
		}
	}

	runJobLoop(ctx, scheduledJob{
		Spec:     source.JobSpec{Key: "steam", Platform: "steam", Side: "ask", AppID: config.DefaultRustAppID},
		Interval: time.Millisecond,
	}, execute)

	if maxActive.Load() != 1 {
		t.Fatalf("job overlapped: max_active=%d", maxActive.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 3 || starts[0] != 0 || starts[1] != 100 || starts[2] != 0 {
		t.Fatalf("starts=%v", starts)
	}
}

func TestRunJobLoop_CanceledBeforeStartDoesNotExecute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls atomic.Int32
	runJobLoop(ctx, scheduledJob{
		Spec:     source.JobSpec{Key: "steam", Platform: "steam", Side: "ask", AppID: config.DefaultRustAppID},
		Interval: time.Second,
	}, func(context.Context, source.JobSpec) (int, error) {
		calls.Add(1)
		return 0, nil
	})

	if calls.Load() != 0 {
		t.Fatalf("execute calls=%d want 0", calls.Load())
	}
}

func TestRunJobLoop_CancelWaitsForActiveExecutorCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	cleaned := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		runJobLoop(ctx, scheduledJob{
			Spec:     source.JobSpec{Key: "steam", Platform: "steam", Side: "ask", AppID: config.DefaultRustAppID},
			Interval: time.Second,
		}, func(runCtx context.Context, _ source.JobSpec) (int, error) {
			close(started)
			<-runCtx.Done()
			close(cleaned)
			return 0, runCtx.Err()
		})
	}()

	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("job loop did not stop")
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("job loop returned before executor cleanup")
	}
}

func TestRunStartedRuntime_WaitsThenClosesRedisAndRestoresTelemetry(t *testing.T) {
	previousLogWriter := log.Writer()
	previousLogFlags := log.Flags()
	previousLogPrefix := log.Prefix()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(previousLogWriter)
		log.SetFlags(previousLogFlags)
		log.SetPrefix(previousLogPrefix)
	}()

	previousRecorder := telemetry.Default()
	baseline := telemetry.NewMemory(8)
	telemetry.SetDefault(baseline)
	defer telemetry.SetDefault(previousRecorder)
	runtimeMemory, stopObservability := installProcessObservability()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	telemetryStopped := make(chan struct{})
	stopAfterRedis := func() {
		if err := rdb.Ping(context.Background()).Err(); !errors.Is(err, redis.ErrClosed) {
			t.Errorf("telemetry restored before Redis closed: %v", err)
		}
		stopObservability()
		close(telemetryStopped)
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCleanup) }) }
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- runStartedRuntime(
			ctx,
			Options{},
			[]int64{config.DefaultRustAppID},
			[]scheduledJob{{
				Spec: source.JobSpec{
					Key:      "steam_sell_rust",
					Platform: source.PlatformSteam,
					Side:     source.SideAsk,
					AppID:    config.DefaultRustAppID,
				},
				Interval: time.Hour,
			}},
			func(runCtx context.Context, _ source.JobSpec) (int, error) {
				close(started)
				<-runCtx.Done()
				close(cleanupStarted)
				<-releaseCleanup
				return 0, runCtx.Err()
			},
			runtimeMemory,
			rdb,
			stopAfterRedis,
		)
	}()
	defer func() {
		cancel()
		release()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runtime did not start its job")
	}
	cancel()
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("executor did not begin cleanup")
	}
	select {
	case err := <-done:
		t.Fatalf("runtime returned before executor cleanup was released: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not finish cleanup")
	}

	if err := rdb.Ping(context.Background()).Err(); !errors.Is(err, redis.ErrClosed) {
		t.Fatalf("Redis client remains open: %v", err)
	}
	select {
	case <-telemetryStopped:
	default:
		t.Fatal("telemetry was not restored")
	}
	if !strings.Contains(logs.String(), "buffgo observ summary:") {
		t.Fatalf("missing telemetry summary: %s", logs.String())
	}
	telemetry.Emit(telemetry.Default(), telemetry.Event{Kind: telemetry.KindJobOK, Reason: telemetry.ReasonOK})
	if baseline.Snapshot().JobOK != 1 || runtimeMemory.Snapshot().JobOK != 0 {
		t.Fatalf("telemetry default was not restored: baseline=%s runtime=%s", baseline.Snapshot().String(), runtimeMemory.Snapshot().String())
	}
}
