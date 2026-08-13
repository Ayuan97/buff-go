package postgres

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"buff-go/internal/collection"
	"buff-go/internal/market"
)

func TestCollectionStoreIntegration(t *testing.T) {
	dsn := os.Getenv("BUFFGO_TEST_DSN")
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN to an isolated PostgreSQL database")
	}
	t.Run("target and run lifecycle", func(t *testing.T) { testCollectionLifecycle(t, dsn) })
	t.Run("active runs and restart recovery", func(t *testing.T) { testCollectionActiveRuns(t, dsn) })
	t.Run("resident instance lock", func(t *testing.T) { testCollectionInstanceLock(t, dsn) })
	t.Run("scope concurrency", func(t *testing.T) { testCollectionConcurrency(t, dsn) })
	t.Run("integrity and errors", func(t *testing.T) { testCollectionIntegrityErrors(t, dsn) })
}

func testCollectionLifecycle(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	empty := mustCollectionCursor(t, nil)

	target, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if target.Revision() != 1 || target.SwitchVersion() != 1 || target.Actual() != collection.ActualStarting ||
		target.ChangedAt().Location() != time.UTC || target.ChangedAt().Nanosecond()%int(time.Microsecond) != 0 {
		t.Fatalf("new summary target = %+v", target)
	}
	same, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredEnabled)
	if err != nil || same.ID() != target.ID() {
		t.Fatalf("idempotent target = %+v err=%v", same, err)
	}
	if _, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredDisabled); !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("changed create configuration error = %v", err)
	}

	restarted, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := restarted.Targets(ctx)
	if err != nil || len(targets) != 1 || targets[0].ID() != target.ID() {
		t.Fatalf("restart targets = %+v err=%v", targets, err)
	}
	read, found, err := restarted.Target(ctx, target.ID())
	if err != nil || !found || !sameTargetState(read, target) {
		t.Fatalf("target read = %+v found=%v err=%v", read, found, err)
	}

	recheck := target.ChangedAt().Add(time.Hour)
	target, err = store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{
		State: collection.ActualWaiting, Reason: collection.TargetReasonNextCycle, RecheckAt: &recheck,
	})
	if err != nil || target.Revision() != 2 || target.SwitchVersion() != 1 {
		t.Fatalf("waiting target = %+v err=%v", target, err)
	}
	retry, err := store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{
		State: collection.ActualWaiting, Reason: collection.TargetReasonNextCycle, RecheckAt: &recheck,
	})
	if err != nil || retry.Revision() != target.Revision() || !retry.ChangedAt().Equal(target.ChangedAt()) {
		t.Fatalf("exact target retry = %+v err=%v", retry, err)
	}
	target, err = store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{
		State: collection.ActualBlocked, Reason: collection.TargetReasonSessionInvalid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{State: collection.ActualRunning}); !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("manual blocker ran without recovery: %v", err)
	}
	target, err = store.RecoverTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion())
	if err != nil || target.Actual() != collection.ActualStarting || target.SwitchVersion() != 1 {
		t.Fatalf("manual recovery = %+v err=%v", target, err)
	}
	target, err = store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{State: collection.ActualRunning})
	if err != nil || target.Revision() != 5 || target.SwitchVersion() != 1 {
		t.Fatalf("running target = %+v err=%v", target, err)
	}
	run, created, err := store.CreateSummaryRun(ctx, target.ID(), target.SwitchVersion(), empty)
	if err != nil || !created || run.State() != collection.RunPending || run.RunSequence() != 1 {
		t.Fatalf("summary run = %+v created=%v err=%v", run, created, err)
	}
	var cursorIsNull bool
	if err := db.QueryRowContext(ctx, `SELECT current_cursor IS NULL FROM collection_runs WHERE run_id = $1`, int64(run.ID())).Scan(&cursorIsNull); err != nil {
		t.Fatal(err)
	}
	if cursorIsNull {
		t.Fatal("empty cursor was stored as SQL NULL")
	}
	repeated, created, err := store.CreateSummaryRun(ctx, target.ID(), target.SwitchVersion(), mustCollectionCursor(t, []byte("ignored-on-active")))
	if err != nil || created || repeated.ID() != run.ID() {
		t.Fatalf("active run retry = %+v created=%v err=%v", repeated, created, err)
	}

	run, err = store.BeginRun(ctx, run.ID())
	if err != nil || run.State() != collection.RunRunning {
		t.Fatalf("begin run = %+v err=%v", run, err)
	}
	beginRetry, err := store.BeginRun(ctx, run.ID())
	if err != nil || beginRetry.ID() != run.ID() {
		t.Fatalf("begin retry = %+v err=%v", beginRetry, err)
	}
	startedAt, _ := run.StartedAt()
	collectedAt := startedAt.Add(time.Second)
	committedAt := collectedAt.Add(time.Microsecond)
	digest := bytes.Repeat([]byte{0x5a}, 32)
	pageTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pageTx.ExecContext(ctx, `
INSERT INTO collection_pages (
    run_id, page_sequence, cursor_before, cursor_after, payload_digest, collected_at, committed_at
) VALUES ($1, 1, $2, $3, $4, $5, $6)`,
		int64(run.ID()), []byte{}, []byte("cursor-1"), digest, collectedAt, committedAt); err != nil {
		_ = pageTx.Rollback()
		t.Fatal(err)
	}
	if _, err := pageTx.ExecContext(ctx, `
UPDATE collection_runs SET current_cursor = $2, last_page_sequence = 1 WHERE run_id = $1`,
		int64(run.ID()), []byte("cursor-1")); err != nil {
		_ = pageTx.Rollback()
		t.Fatal(err)
	}
	if err := pageTx.Commit(); err != nil {
		t.Fatal(err)
	}
	pages, err := store.Pages(ctx, run.ID())
	if err != nil || len(pages) != 1 || pages[0].PageSequence() != 1 ||
		!pages[0].CursorAfter().Equal(mustCollectionCursor(t, []byte("cursor-1"))) {
		t.Fatalf("pages = %+v err=%v", pages, err)
	}

	run, err = store.FinishRun(ctx, run.ID(), collection.RunSucceeded, collection.CompletenessComplete, collection.RunReasonNone)
	if err != nil || run.State() != collection.RunSucceeded || run.LastPageSequence() != 1 {
		t.Fatalf("finish success = %+v err=%v", run, err)
	}
	finishedAt, _ := run.FinishedAt()
	finishedRetry, err := store.FinishRun(ctx, run.ID(), collection.RunSucceeded, collection.CompletenessComplete, collection.RunReasonNone)
	if err != nil {
		t.Fatal(err)
	}
	retryFinishedAt, _ := finishedRetry.FinishedAt()
	if !retryFinishedAt.Equal(finishedAt) {
		t.Fatal("exact finish retry rewrote finished_at")
	}
	if _, err := store.FinishRun(ctx, run.ID(), collection.RunFailed, collection.CompletenessPartial, collection.RunReasonTimeout); !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("changed terminal retry error = %v", err)
	}
	if _, err := store.BeginRun(ctx, run.ID()); !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("terminal begin error = %v", err)
	}

	failed, created, err := store.CreateSummaryRun(ctx, target.ID(), target.SwitchVersion(), empty)
	if err != nil || !created || failed.RunSequence() != 2 {
		t.Fatalf("second run = %+v created=%v err=%v", failed, created, err)
	}
	failed, err = store.FinishRun(ctx, failed.ID(), collection.RunFailed, collection.CompletenessPartial, collection.RunReasonNetworkError)
	if err != nil || failed.State() != collection.RunFailed {
		t.Fatalf("pending failure = %+v err=%v", failed, err)
	}

	old, created, err := store.CreateSummaryRun(ctx, target.ID(), target.SwitchVersion(), empty)
	if err != nil || !created || old.RunSequence() != 3 {
		t.Fatalf("old-switch run = %+v created=%v err=%v", old, created, err)
	}
	target, err = store.SetTargetDesired(ctx, target.ID(), target.Revision(), collection.DesiredDisabled)
	if err != nil || target.SwitchVersion() != 2 || target.Actual() != collection.ActualStopping {
		t.Fatalf("disable target = %+v err=%v", target, err)
	}
	if _, err := store.BeginRun(ctx, old.ID()); !errors.Is(err, ErrCollectionTargetDisabled) {
		t.Fatalf("disabled begin error = %v", err)
	}
	target, err = store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{State: collection.ActualStopped})
	if err != nil {
		t.Fatal(err)
	}
	target, err = store.SetTargetDesired(ctx, target.ID(), target.Revision(), collection.DesiredEnabled)
	if err != nil || target.SwitchVersion() != 3 || target.Actual() != collection.ActualStarting {
		t.Fatalf("re-enable target = %+v err=%v", target, err)
	}
	if _, _, err := store.CreateSummaryRun(ctx, target.ID(), target.SwitchVersion(), empty); !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("old active switch create error = %v", err)
	}
	if _, err := store.BeginRun(ctx, old.ID()); !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("old active switch begin error = %v", err)
	}
	if _, err := store.FinishRun(ctx, old.ID(), collection.RunStopped, collection.CompletenessPartial, collection.RunReasonSwitchDisabled); err != nil {
		t.Fatal(err)
	}
	current, created, err := store.CreateSummaryRun(ctx, target.ID(), target.SwitchVersion(), empty)
	if err != nil || !created || current.RunSequence() != 4 {
		t.Fatalf("new-switch run = %+v created=%v err=%v", current, created, err)
	}
	if _, err := store.FinishRun(ctx, current.ID(), collection.RunStopped, collection.CompletenessPartial, collection.RunReasonCancelled); err != nil {
		t.Fatal(err)
	}

	runs, err := store.RunsForTarget(ctx, target.ID(), 10)
	if err != nil || len(runs) != 4 || runs[0].RunSequence() != 4 || runs[3].RunSequence() != 1 {
		t.Fatalf("target runs = %+v err=%v", runs, err)
	}
	if _, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredEnabled); err != nil {
		t.Fatalf("create with current explicit configuration: %v", err)
	}
}

// testCollectionActiveRuns 覆盖常驻恢复依赖的活动运行读取与重启结束语义。
func testCollectionActiveRuns(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	defer func() { _ = db.Close() }()

	active, err := store.ActiveRuns(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("empty active runs = %+v err=%v", active, err)
	}

	var empty collection.Cursor
	askTarget, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	bidTarget, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideBid, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	pending, _, err := store.CreateSummaryRun(ctx, askTarget.ID(), askTarget.SwitchVersion(), empty)
	if err != nil {
		t.Fatal(err)
	}
	running, _, err := store.CreateSummaryRun(ctx, bidTarget.ID(), bidTarget.SwitchVersion(), empty)
	if err != nil {
		t.Fatal(err)
	}
	if running, err = store.BeginRun(ctx, running.ID()); err != nil {
		t.Fatal(err)
	}

	active, err = store.ActiveRuns(ctx)
	if err != nil || len(active) != 2 {
		t.Fatalf("active runs = %+v err=%v", active, err)
	}
	if active[0].ID() != pending.ID() || active[1].ID() != running.ID() {
		t.Fatalf("active runs must be ordered by identity, got %d and %d", active[0].ID(), active[1].ID())
	}
	if active[0].State() != collection.RunPending || active[1].State() != collection.RunRunning {
		t.Fatalf("active runs must keep their states, got %s and %s", active[0].State(), active[1].State())
	}

	// 重启恢复：未开始与进行中的运行都能以 process_restarted 结束。
	for _, run := range active {
		finished, err := store.FinishRun(ctx, run.ID(), collection.RunFailed,
			collection.CompletenessPartial, collection.RunReasonProcessRestarted)
		if err != nil {
			t.Fatalf("finish orphaned run %d: %v", run.ID(), err)
		}
		if finished.State() != collection.RunFailed || finished.Reason() != collection.RunReasonProcessRestarted {
			t.Fatalf("orphaned run = %s/%s", finished.State(), finished.Reason())
		}
	}

	active, err = store.ActiveRuns(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("terminal runs must leave the active set, got %+v err=%v", active, err)
	}
	// 恢复后可以重新建立运行。
	if _, created, err := store.CreateSummaryRun(ctx, askTarget.ID(), askTarget.SwitchVersion(), empty); err != nil || !created {
		t.Fatalf("run after recovery created=%v err=%v", created, err)
	}
}

// testCollectionInstanceLock 覆盖常驻单实例栅栏：同一 schema 只允许一个持有者。
func testCollectionInstanceLock(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	defer func() { _ = db.Close() }()

	lock, acquired, err := store.AcquireInstanceLock(ctx)
	if err != nil || !acquired {
		t.Fatalf("first instance lock acquired=%v err=%v", acquired, err)
	}
	// 同一 schema 的另一个进程（独立连接池）必须被拒绝。
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	other := stdlib.OpenDB(*config)
	defer func() { _ = other.Close() }()
	otherStore, err := New(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := otherStore.AcquireInstanceLock(ctx); err != nil || acquired {
		t.Fatalf("second instance lock must be refused, acquired=%v err=%v", acquired, err)
	}
	// 持有期间必须可验证，常驻循环据此确认自己仍然独占状态。
	if err := lock.Verify(ctx); err != nil {
		t.Fatalf("held instance lock must verify, got %v", err)
	}
	// 连接池重置会话会清掉会话级锁而连接依旧可用：可达性不等于仍然持有，
	// 校验必须回读真实归属。
	held := lock.(*collectionInstanceLock)
	if _, err := held.conn.ExecContext(ctx, `SELECT pg_advisory_unlock_all()`); err != nil {
		t.Fatal(err)
	}
	if err := lock.Verify(ctx); err == nil {
		t.Fatal("a lock lost on a live session must not verify")
	}
	// 锁真的丢了：接替进程可以取得，而原持有者的释放必须报错而不是假装成功。
	successor, acquired, err := otherStore.AcquireInstanceLock(ctx)
	if err != nil || !acquired {
		t.Fatalf("the lost lock must be available to a successor, acquired=%v err=%v", acquired, err)
	}
	if err := lock.Release(ctx); err == nil {
		t.Fatal("releasing a lock this session no longer holds must fail")
	}
	if err := lock.Verify(ctx); err == nil {
		t.Fatal("released instance lock must not verify")
	}
	if err := lock.Release(ctx); err != nil {
		t.Fatalf("repeated release must be idempotent, got %v", err)
	}
	if err := successor.Release(ctx); err != nil {
		t.Fatal(err)
	}
	// 释放会把连接放回池中，池化连接不能残留会话级锁。
	again, acquired, err := store.AcquireInstanceLock(ctx)
	if err != nil || !acquired {
		t.Fatalf("pooled connection must not keep the lock, acquired=%v err=%v", acquired, err)
	}
	if err := again.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

func testCollectionConcurrency(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	empty := mustCollectionCursor(t, nil)

	summary, err := store.CreateSummaryTarget(ctx, "buff", 730, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := store.CreateSummaryRun(ctx, summary.ID(), summary.SwitchVersion(), empty)
	if err != nil || !created {
		t.Fatalf("first summary run = %+v created=%v err=%v", first, created, err)
	}
	if repeated, created, err := store.CreateSummaryRun(ctx, summary.ID(), summary.SwitchVersion(), empty); err != nil || created || repeated.ID() != first.ID() {
		t.Fatalf("active run retry = %+v created=%v err=%v", repeated, created, err)
	}

	const workers = 8
	type createResult struct {
		target  collection.Target
		run     collection.Run
		created bool
		err     error
	}
	targetResults := make(chan createResult, workers)
	var group sync.WaitGroup
	start := make(chan struct{})
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			target, err := store.CreateSummaryTarget(ctx, "steam", 440, market.SideAsk, collection.DesiredEnabled)
			targetResults <- createResult{target: target, err: err}
		}()
	}
	close(start)
	group.Wait()
	close(targetResults)
	var targetID collection.TargetID
	for result := range targetResults {
		if result.err != nil {
			t.Fatalf("concurrent target create: %v", result.err)
		}
		if targetID == 0 {
			targetID = result.target.ID()
		} else if result.target.ID() != targetID {
			t.Fatalf("concurrent target ids = %d and %d", targetID, result.target.ID())
		}
	}

	concurrentTarget, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideBid, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	runResults := make(chan createResult, workers)
	start = make(chan struct{})
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			run, created, err := store.CreateSummaryRun(ctx, concurrentTarget.ID(), concurrentTarget.SwitchVersion(), empty)
			runResults <- createResult{run: run, created: created, err: err}
		}()
	}
	close(start)
	group.Wait()
	close(runResults)
	var runID collection.RunID
	createdCount := 0
	for result := range runResults {
		if result.err != nil {
			t.Fatalf("concurrent run create: %v", result.err)
		}
		if result.created {
			createdCount++
		}
		if runID == 0 {
			runID = result.run.ID()
		} else if result.run.ID() != runID {
			t.Fatalf("concurrent run ids = %d and %d", runID, result.run.ID())
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}

	casTarget, err := store.CreateSummaryTarget(ctx, "steam", 570, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	recheck := casTarget.ChangedAt().Add(time.Hour)
	casResults := make(chan error, 2)
	start = make(chan struct{})
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		_, err := store.SetTargetDesired(ctx, casTarget.ID(), casTarget.Revision(), collection.DesiredDisabled)
		casResults <- err
	}()
	go func() {
		defer group.Done()
		<-start
		_, err := store.TransitionTarget(ctx, casTarget.ID(), casTarget.Revision(), casTarget.SwitchVersion(), TargetTransition{
			State: collection.ActualWaiting, Reason: collection.TargetReasonNextCycle, RecheckAt: &recheck,
		})
		casResults <- err
	}()
	close(start)
	group.Wait()
	close(casResults)
	successes, conflicts := 0, 0
	for err := range casResults {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCollectionConflict):
			conflicts++
		default:
			t.Fatalf("CAS race error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS race successes=%d conflicts=%d", successes, conflicts)
	}
	stored, found, err := store.Target(ctx, casTarget.ID())
	if err != nil || !found || stored.Revision() != 2 {
		t.Fatalf("CAS target = %+v found=%v err=%v", stored, found, err)
	}

	disabled, err := store.CreateSummaryTarget(ctx, "igxe", 730, market.SideBid, collection.DesiredDisabled)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateSummaryRun(ctx, disabled.ID(), disabled.SwitchVersion(), empty); !errors.Is(err, ErrCollectionTargetDisabled) {
		t.Fatalf("disabled create error = %v", err)
	}

	snapshotTarget, err := store.CreateSummaryTarget(ctx, "steam", 10, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	snapshotRun, _, err := store.CreateSummaryRun(ctx, snapshotTarget.ID(), snapshotTarget.SwitchVersion(), empty)
	if err != nil {
		t.Fatal(err)
	}
	snapshotRun, err = store.BeginRun(ctx, snapshotRun.ID())
	if err != nil {
		t.Fatal(err)
	}
	startedAt, _ := snapshotRun.StartedAt()
	writerErr := make(chan error, 1)
	readerErr := make(chan error, 1)
	go func() {
		before := []byte{}
		for sequence := 1; sequence <= 24; sequence++ {
			after := []byte("snapshot-" + strconv.Itoa(sequence))
			collectedAt := startedAt.Add(time.Duration(sequence) * time.Microsecond)
			tx, err := db.BeginTx(ctx, nil)
			if err == nil {
				_, err = tx.ExecContext(ctx, `
INSERT INTO collection_pages (
    run_id, page_sequence, cursor_before, cursor_after, payload_digest, collected_at, committed_at
) VALUES ($1, $2, $3, $4, $5, $6, $6)`,
					int64(snapshotRun.ID()), sequence, before, after, bytes.Repeat([]byte{byte(sequence)}, 32), collectedAt)
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, `
UPDATE collection_runs SET current_cursor = $3, last_page_sequence = $2 WHERE run_id = $1`,
					int64(snapshotRun.ID()), sequence, after)
			}
			if err == nil {
				err = tx.Commit()
			} else if tx != nil {
				_ = tx.Rollback()
			}
			if err != nil {
				writerErr <- err
				return
			}
			before = after
		}
		writerErr <- nil
	}()
	go func() {
		for range 96 {
			if _, err := store.Pages(ctx, snapshotRun.ID()); err != nil {
				readerErr <- err
				return
			}
		}
		readerErr <- nil
	}()
	if err := <-writerErr; err != nil {
		t.Fatalf("snapshot writer: %v", err)
	}
	if err := <-readerErr; err != nil {
		t.Fatalf("snapshot reader: %v", err)
	}
	pages, err := store.Pages(ctx, snapshotRun.ID())
	if err != nil || len(pages) != 24 {
		t.Fatalf("snapshot pages=%d err=%v", len(pages), err)
	}
}

func testCollectionIntegrityErrors(t *testing.T, dsn string) {
	t.Run("run target mismatch", func(t *testing.T) {
		store, db := migratedStore(t, dsn)
		ctx := t.Context()
		target, err := store.CreateSummaryTarget(ctx, "buff", 730, market.SideBid, collection.DesiredEnabled)
		if err != nil {
			t.Fatal(err)
		}
		run, _, err := store.CreateSummaryRun(ctx, target.ID(), target.SwitchVersion(), mustCollectionCursor(t, nil))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE collection_runs SET platform = 'igxe' WHERE run_id = $1`, int64(run.ID())); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Run(ctx, run.ID()); !errors.Is(err, ErrCollectionIntegrity) {
			t.Fatalf("mismatched run read error = %v", err)
		}
	})

	t.Run("page gap", func(t *testing.T) {
		store, db := migratedStore(t, dsn)
		ctx := t.Context()
		target, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredEnabled)
		if err != nil {
			t.Fatal(err)
		}
		run, _, err := store.CreateSummaryRun(ctx, target.ID(), target.SwitchVersion(), mustCollectionCursor(t, nil))
		if err != nil {
			t.Fatal(err)
		}
		run, err = store.BeginRun(ctx, run.ID())
		if err != nil {
			t.Fatal(err)
		}
		startedAt, _ := run.StartedAt()
		digest := bytes.Repeat([]byte{0x33}, 32)
		pageTx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pageTx.ExecContext(ctx, `
INSERT INTO collection_pages (
    run_id, page_sequence, cursor_before, cursor_after, payload_digest, collected_at, committed_at
) VALUES ($1, 2, $2, $3, $4, $5, $5)`,
			int64(run.ID()), []byte{}, []byte("gap"), digest, startedAt); err != nil {
			_ = pageTx.Rollback()
			t.Fatal(err)
		}
		if _, err := pageTx.ExecContext(ctx, `
UPDATE collection_runs SET current_cursor = $2, last_page_sequence = 2 WHERE run_id = $1`,
			int64(run.ID()), []byte("gap")); err != nil {
			_ = pageTx.Rollback()
			t.Fatal(err)
		}
		if err := pageTx.Commit(); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pages(ctx, run.ID()); !errors.Is(err, ErrCollectionIntegrity) {
			t.Fatalf("page gap error = %v", err)
		}
	})

	t.Run("fixed storage errors", func(t *testing.T) {
		store, db := migratedStore(t, dsn)
		ctx := t.Context()
		const marker = "secretmarker"
		if _, err := store.CreateSummaryTarget(ctx, marker, 730, market.SideAsk, collection.DesiredEnabled); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateSummaryTarget(ctx, marker, 730, market.SideAsk, collection.DesiredDisabled); err != ErrCollectionConflict || strings.Contains(err.Error(), marker) {
			t.Fatalf("configuration conflict leaked input: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Targets(ctx); err != ErrCollectionStorage {
			t.Fatalf("closed storage error = %v", err)
		}
	})
}

func mustCollectionCursor(t *testing.T, value []byte) collection.Cursor {
	t.Helper()
	cursor, err := collection.NewCursor(value)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}
