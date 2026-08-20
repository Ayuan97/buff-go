package postgres

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
)

func TestCollectionStoreIntegration(t *testing.T) {
	dsn := os.Getenv("BUFFGO_TEST_DSN")
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN to an isolated PostgreSQL database")
	}
	t.Run("target and queue lifecycle", func(t *testing.T) { testCollectionLifecycle(t, dsn) })
	t.Run("target deletion", func(t *testing.T) { testCollectionTargetDeletion(t, dsn) })
	t.Run("sort order clears queue", func(t *testing.T) { testCollectionSortOrder(t, dsn) })
	t.Run("price range clears queue", func(t *testing.T) { testCollectionPriceRange(t, dsn) })
	t.Run("steam facets clears queue", func(t *testing.T) { testCollectionSteamFacets(t, dsn) })
	t.Run("stale claims return to queue", func(t *testing.T) { testCollectionStaleClaims(t, dsn) })
	t.Run("one claim per combination", func(t *testing.T) { testCollectionOneClaimPerCombination(t, dsn) })
	t.Run("claim generation fences stale worker", func(t *testing.T) { testCollectionClaimGeneration(t, dsn) })
	t.Run("claim filters eligible targets", func(t *testing.T) { testCollectionEligibleTargets(t, dsn) })
	t.Run("planner refill snapshot is fenced", func(t *testing.T) { testCollectionRefillFence(t, dsn) })
	t.Run("claim respects recheck", func(t *testing.T) { testCollectionClaimRespectsRecheck(t, dsn) })
	t.Run("claim rechecks target state", func(t *testing.T) { testCollectionClaimRechecksTargetState(t, dsn) })
	t.Run("resident instance lock", func(t *testing.T) { testCollectionInstanceLock(t, dsn) })
	t.Run("scope concurrency", func(t *testing.T) { testCollectionConcurrency(t, dsn) })
	t.Run("integrity and errors", func(t *testing.T) { testCollectionIntegrityErrors(t, dsn) })
}

func testCollectionClaimRechecksTargetState(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	target, combination := queueReadyFixture(t, store, "steam", 730, market.SideAsk)
	if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
		t.Fatal(err)
	}
	transition, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transition.Rollback() }()
	if _, err := transition.ExecContext(ctx, `
UPDATE collection_targets
SET actual_state = 'waiting', reason_code = 'transient_failure', recovery_mode = 'automatic',
    next_check_at = date_trunc('microseconds', clock_timestamp()) + INTERVAL '1 hour',
    revision = revision + 1,
    changed_at = GREATEST(changed_at + INTERVAL '1 microsecond', date_trunc('microseconds', clock_timestamp()))
WHERE target_id = $1`, int64(target.ID())); err != nil {
		t.Fatal(err)
	}

	type claimResult struct {
		found bool
		err   error
	}
	result := make(chan claimResult, 1)
	go func() {
		_, _, found, err := store.ClaimTask(ctx, combination.ID, target.Platform())
		result <- claimResult{found: found, err: err}
	}()
	waiting := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM pg_stat_activity
    WHERE datname = current_database()
      AND wait_event_type = 'Lock'
      AND query LIKE '%collection_targets%'
)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("claim did not wait for the target state lock")
	}
	if err := transition.Commit(); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil || got.found {
		t.Fatalf("claim after target wait found=%v err=%v", got.found, got.err)
	}
	if depth, err := store.QueueDepth(ctx, target.ID()); err != nil || depth != 1 {
		t.Fatalf("queue depth=%d err=%v", depth, err)
	}
}

func testCollectionClaimRespectsRecheck(t *testing.T, dsn string) {
	store, _ := migratedStore(t, dsn)
	ctx := t.Context()
	target, combination := queueReadyFixture(t, store, "steam", 730, market.SideAsk)
	if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
		t.Fatal(err)
	}
	recheckAt := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
	target, err := store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{
		State: collection.ActualWaiting, Reason: collection.TargetReasonTransientFailure, RecheckAt: &recheckAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := store.ClaimTask(ctx, combination.ID, target.Platform()); err != nil || found {
		t.Fatalf("waiting target claim found=%v err=%v", found, err)
	}
	if depth, err := store.QueueDepth(ctx, target.ID()); err != nil || depth != 1 {
		t.Fatalf("waiting target queue depth=%d err=%v", depth, err)
	}
}

// 删除目标只是撤销手段：写过页的目标一律拒绝，采集历史不因为想让按钮可用而被删。
func testCollectionTargetDeletion(t *testing.T, dsn string) {
	store, _ := migratedStore(t, dsn)
	ctx := t.Context()

	unused, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredDisabled)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTarget(ctx, unused.ID()); err != nil {
		t.Fatalf("delete unused target: %v", err)
	}
	if _, found, err := store.Target(ctx, unused.ID()); err != nil || found {
		t.Fatalf("deleted target found=%v err=%v", found, err)
	}
	if err := store.DeleteTarget(ctx, unused.ID()); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("repeat delete = %v", err)
	}

	enabled, err := store.CreateSummaryTarget(ctx, "steam", 252490, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTarget(ctx, enabled.ID()); !errors.Is(err, ErrTargetNotStopped) {
		t.Fatalf("delete enabled target = %v", err)
	}

	// 入过队但没写过页：停掉之后仍可删。
	queued, combination := queueReadyFixture(t, store, "steam", 440, market.SideAsk)
	if err := store.EnqueueTasks(ctx, queued.ID(), queued.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := store.ClaimTask(ctx, combination.ID, "steam"); err != nil || !ok {
		t.Fatalf("claim queued target ok=%v err=%v", ok, err)
	}
	queued, err = store.SetTargetDesired(ctx, queued.ID(), queued.Revision(), collection.DesiredDisabled)
	if err != nil {
		t.Fatal(err)
	}
	queued, err = store.TransitionTarget(ctx, queued.ID(), queued.Revision(), queued.SwitchVersion(), TargetTransition{State: collection.ActualStopped})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTarget(ctx, queued.ID()); err != nil {
		t.Fatalf("delete unused queued target: %v", err)
	}

	target, task, _ := queueCommitFixture(t, store, "buff", 730, market.SideAsk)
	collectedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
		CollectedAt: collectedAt,
		Attempts: []AttemptWrite{{
			ExactName:   "AK-47 | Redline",
			Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt},
		}},
	}); err != nil || !applied {
		t.Fatalf("commit history page applied=%v err=%v", applied, err)
	}
	target, err = store.SetTargetDesired(ctx, target.ID(), target.Revision(), collection.DesiredDisabled)
	if err != nil {
		t.Fatal(err)
	}
	target, err = store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{State: collection.ActualStopped})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTarget(ctx, target.ID()); !errors.Is(err, ErrTargetInUse) {
		t.Fatalf("delete target with write_seq = %v", err)
	}
}

// 游标是列表偏移量，换顺序必须推进 switch_version 并清队列，否则补货会
// 落到完全不同的商品上。已经写下的 write_seq 保留。
func testCollectionSortOrder(t *testing.T, dsn string) {
	store, _ := migratedStore(t, dsn)
	ctx := t.Context()

	target, task, _ := queueCommitFixture(t, store, "steam", 730, market.SideAsk)
	if target.Sort() != collection.DefaultSortOrder() {
		t.Fatalf("default sort = %+v", target.Sort())
	}
	if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1),
		mustCollectionCursor(t, []byte("off")), 99); err != nil {
		t.Fatal(err)
	}
	collectedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
		CollectedAt: collectedAt, AskTotal: 99,
		Attempts: []AttemptWrite{{
			ExactName:   "AK-47 | Redline",
			Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt},
		}},
	}); err != nil || !applied {
		t.Fatalf("commit before sort applied=%v err=%v", applied, err)
	}
	target, found, err := store.Target(ctx, target.ID())
	if err != nil || !found || target.WriteSeq() != 1 || target.RefillTotal() != 99 {
		t.Fatalf("target before sort = %+v found=%v err=%v", target, found, err)
	}
	oldSwitch := target.SwitchVersion()

	quantityDesc := collection.SortOrder{Column: collection.SortColumnQuantity, Direction: collection.SortDescending}
	changed, err := store.SetTargetSortOrder(ctx, target.ID(), target.Revision(), quantityDesc)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Sort() != quantityDesc {
		t.Fatalf("sort after change = %+v", changed.Sort())
	}
	if changed.SwitchVersion() != oldSwitch+1 {
		t.Fatalf("switch version = %d, want %d", changed.SwitchVersion(), oldSwitch+1)
	}
	if changed.Actual() != collection.ActualStarting {
		t.Fatalf("actual after change = %q", changed.Actual())
	}
	if changed.WriteSeq() != 1 {
		t.Fatalf("write_seq after sort = %d, want 1", changed.WriteSeq())
	}
	if changed.RefillTotal() != 0 || !changed.RefillCursor().Equal(mustCollectionCursor(t, nil)) {
		t.Fatalf("refill after sort = total=%d cursor=%q", changed.RefillTotal(), changed.RefillCursor().Bytes())
	}
	if depth, err := store.QueueDepth(ctx, changed.ID()); err != nil || depth != 0 {
		t.Fatalf("queue after sort depth=%d err=%v", depth, err)
	}
	if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: oldSwitch,
		CollectedAt: collectedAt.Add(time.Second),
		Attempts: []AttemptWrite{{
			ExactName:   "M4A1-S | Hot Rod",
			Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt.Add(time.Second)},
		}},
	}); !errors.Is(err, ErrCollectionFence) {
		t.Fatalf("stale switch page commit = %v", err)
	}

	same, err := store.SetTargetSortOrder(ctx, changed.ID(), changed.Revision(), quantityDesc)
	if err != nil || same.Revision() != changed.Revision() || same.SwitchVersion() != changed.SwitchVersion() {
		t.Fatalf("idempotent sort = %+v err=%v", same, err)
	}
	if _, err := store.SetTargetSortOrder(ctx, changed.ID(), changed.Revision(),
		collection.SortOrder{Column: "cheapest", Direction: "asc"}); !errors.Is(err, ErrCollectionInvalidInput) {
		t.Fatalf("invalid sort column = %v", err)
	}
}

func testCollectionPriceRange(t *testing.T, dsn string) {
	store, _ := migratedStore(t, dsn)
	ctx := t.Context()
	target, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	oldSwitch := target.SwitchVersion()
	minCents := int64(1000)
	maxCents := int64(879769)
	changed, err := store.SetTargetPriceRange(ctx, target.ID(), target.Revision(), collection.PriceRange{
		MinCents: &minCents, MaxCents: &maxCents,
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.PriceRange().MinCents == nil || *changed.PriceRange().MinCents != 1000 {
		t.Fatalf("price range = %+v", changed.PriceRange())
	}
	if changed.SwitchVersion() != oldSwitch+1 {
		t.Fatalf("switch version = %d, want %d", changed.SwitchVersion(), oldSwitch+1)
	}
	same, err := store.SetTargetPriceRange(ctx, changed.ID(), changed.Revision(), collection.PriceRange{
		MinCents: &minCents, MaxCents: &maxCents,
	})
	if err != nil || same.Revision() != changed.Revision() {
		t.Fatalf("idempotent price range = %+v err=%v", same, err)
	}
	badMax := int64(1)
	if _, err := store.SetTargetPriceRange(ctx, changed.ID(), changed.Revision(), collection.PriceRange{
		MinCents: &minCents, MaxCents: &badMax,
	}); !errors.Is(err, ErrCollectionInvalidInput) {
		t.Fatalf("inverted price range = %v", err)
	}
}

func testCollectionSteamFacets(t *testing.T, dsn string) {
	store, _ := migratedStore(t, dsn)
	ctx := t.Context()
	target, err := store.CreateSummaryTarget(ctx, "steam", 252490, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	oldSwitch := target.SwitchVersion()
	changed, err := store.SetTargetSteamFacets(ctx, target.ID(), target.Revision(), collection.SteamFacets{
		Cats:    []string{"steamcat.armor"},
		Classes: []string{"hoodie"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.SteamFacets().Cats) != 1 || changed.SteamFacets().Cats[0] != "steamcat.armor" {
		t.Fatalf("steam facets = %+v", changed.SteamFacets())
	}
	if changed.SwitchVersion() != oldSwitch+1 {
		t.Fatalf("switch version = %d, want %d", changed.SwitchVersion(), oldSwitch+1)
	}
	same, err := store.SetTargetSteamFacets(ctx, changed.ID(), changed.Revision(), collection.SteamFacets{
		Cats:    []string{"steamcat.armor"},
		Classes: []string{"hoodie"},
	})
	if err != nil || same.Revision() != changed.Revision() {
		t.Fatalf("idempotent steam facets = %+v err=%v", same, err)
	}
	cs2, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetTargetSteamFacets(ctx, cs2.ID(), cs2.Revision(), collection.SteamFacets{
		Cats: []string{"steamcat.armor"},
	}); !errors.Is(err, ErrCollectionInvalidInput) {
		t.Fatalf("cs2 steam facets = %v", err)
	}
}

func testCollectionLifecycle(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()

	target, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if target.Revision() != 1 || target.SwitchVersion() != 1 || target.Actual() != collection.ActualStarting ||
		target.WriteSeq() != 0 || target.ChangedAt().Location() != time.UTC ||
		target.ChangedAt().Nanosecond()%int(time.Microsecond) != 0 {
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

	_, combination := mustQueueCombination(t, store, "steam")
	if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 10); err != nil {
		t.Fatal(err)
	}
	var cursorIsNull bool
	if err := db.QueryRowContext(ctx, `SELECT refill_cursor IS NULL FROM collection_targets WHERE target_id = $1`, int64(target.ID())).Scan(&cursorIsNull); err != nil {
		t.Fatal(err)
	}
	if cursorIsNull {
		t.Fatal("empty refill cursor was stored as SQL NULL")
	}
	task, claimedTarget, ok, err := store.ClaimTask(ctx, combination.ID, "steam")
	if err != nil || !ok || task.TargetID() != target.ID() || claimedTarget.ID() != target.ID() {
		t.Fatalf("claim task = %+v ok=%v err=%v", task, ok, err)
	}
	if depth, err := store.QueueDepth(ctx, target.ID()); err != nil || depth != 1 {
		t.Fatalf("claimed depth=%d err=%v", depth, err)
	}

	collectedAt := time.Now().UTC().Truncate(time.Microsecond)
	page, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
		CollectedAt: collectedAt,
		Attempts: []AttemptWrite{{
			ExactName:   "AK-47 | Redline",
			Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt},
		}},
	})
	if err != nil || !applied || page.WriteSeq() != 1 {
		t.Fatalf("commit page = %+v applied=%v err=%v", page, applied, err)
	}
	target, found, err = store.Target(ctx, target.ID())
	if err != nil || !found || target.WriteSeq() != 1 {
		t.Fatalf("target after commit = %+v found=%v err=%v", target, found, err)
	}
	assertWorkerSeesPageItems(t, store, combination.ID, target.ID(), 0)
	if err := store.CompleteTask(ctx, task.ID(), combination.ID, task.ClaimGeneration()); err != nil {
		t.Fatal(err)
	}
	if depth, err := store.QueueDepth(ctx, target.ID()); err != nil || depth != 0 {
		t.Fatalf("completed depth=%d err=%v", depth, err)
	}

	oldSwitch := target.SwitchVersion()
	target, err = store.SetTargetDesired(ctx, target.ID(), target.Revision(), collection.DesiredDisabled)
	if err != nil || target.SwitchVersion() != 2 || target.Actual() != collection.ActualStopping ||
		target.WriteSeq() != 1 || target.RefillTotal() != 0 {
		t.Fatalf("disable target = %+v err=%v", target, err)
	}
	if depth, err := store.QueueDepth(ctx, target.ID()); err != nil || depth != 0 {
		t.Fatalf("disabled queue depth=%d err=%v", depth, err)
	}
	if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: oldSwitch, CollectedAt: collectedAt.Add(time.Second),
	}); !errors.Is(err, ErrCollectionFence) {
		t.Fatalf("disabled page error = %v", err)
	}
	if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); !errors.Is(err, ErrCollectionFence) {
		t.Fatalf("disabled enqueue error = %v", err)
	}
	target, err = store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{State: collection.ActualStopped})
	if err != nil {
		t.Fatal(err)
	}
	target, err = store.SetTargetDesired(ctx, target.ID(), target.Revision(), collection.DesiredEnabled)
	if err != nil || target.SwitchVersion() != 3 || target.Actual() != collection.ActualStarting || target.WriteSeq() != 1 {
		t.Fatalf("re-enable target = %+v err=%v", target, err)
	}
	if err := store.EnqueueTasks(ctx, target.ID(), oldSwitch, mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); !errors.Is(err, ErrCollectionFence) {
		t.Fatalf("old switch enqueue error = %v", err)
	}
	if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := store.ClaimTask(ctx, combination.ID, "steam"); err != nil || !ok {
		t.Fatalf("claim after re-enable ok=%v err=%v", ok, err)
	}
	if _, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideAsk, collection.DesiredEnabled); err != nil {
		t.Fatalf("create with current explicit configuration: %v", err)
	}
}

// testCollectionStaleClaims 覆盖认领超时回队：常驻恢复把过期 claimed 放回 queued。
func testCollectionStaleClaims(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	defer func() { _ = db.Close() }()

	released, err := store.ReleaseStaleClaims(ctx, time.Minute)
	if err != nil || released != 0 {
		t.Fatalf("empty stale claims released=%d err=%v", released, err)
	}

	askTarget, askTask, askCombo := queueCommitFixture(t, store, "steam", 730, market.SideAsk)
	bidTarget, _, _ := queueCommitFixture(t, store, "steam", 730, market.SideBid)
	if depth, err := store.QueueDepth(ctx, askTarget.ID()); err != nil || depth != 1 {
		t.Fatalf("ask depth=%d err=%v", depth, err)
	}
	if depth, err := store.QueueDepth(ctx, bidTarget.ID()); err != nil || depth != 1 {
		t.Fatalf("bid depth=%d err=%v", depth, err)
	}

	fresh, err := store.ReleaseStaleClaims(ctx, time.Hour)
	if err != nil || fresh != 0 {
		t.Fatalf("fresh claims must stay claimed, released=%d err=%v", fresh, err)
	}

	if _, err := db.ExecContext(ctx, `
UPDATE collection_tasks SET claimed_at = date_trunc('microseconds', clock_timestamp()) - INTERVAL '1 hour'
WHERE task_id = $1`, int64(askTask.ID())); err != nil {
		t.Fatal(err)
	}
	if released, err := store.ReleaseStaleClaimsExcept(ctx, time.Minute, []collection.TaskID{askTask.ID()}); err != nil || released != 0 {
		t.Fatalf("active stale claim released=%d err=%v", released, err)
	}
	released, err = store.ReleaseStaleClaims(ctx, time.Minute)
	if err != nil || released != 1 {
		t.Fatalf("stale claim released=%d err=%v", released, err)
	}
	if depth, err := store.QueueDepth(ctx, askTarget.ID()); err != nil || depth != 1 {
		t.Fatalf("requeued depth=%d err=%v", depth, err)
	}
	reclaimed, _, ok, err := store.ClaimTask(ctx, askCombo.ID, "steam")
	if err != nil || !ok || reclaimed.ID() != askTask.ID() || reclaimed.State() != collection.TaskClaimed {
		t.Fatalf("reclaim after release = %+v ok=%v err=%v", reclaimed, ok, err)
	}
	if err := store.RequeueTask(ctx, reclaimed.ID(), askCombo.ID, reclaimed.ClaimGeneration()); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := store.ClaimTask(ctx, askCombo.ID, "steam"); err != nil || !ok {
		t.Fatalf("claim after requeue ok=%v err=%v", ok, err)
	}
}

func testCollectionClaimGeneration(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	defer func() { _ = db.Close() }()

	target, combination := queueReadyFixture(t, store, "steam", 730, market.SideAsk)
	if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
		t.Fatal(err)
	}
	first, _, found, err := store.ClaimTask(ctx, combination.ID, "steam")
	if err != nil || !found || first.ClaimGeneration() != 1 {
		t.Fatalf("first claim=%+v found=%v err=%v", first, found, err)
	}
	firstClaimedAt, _ := first.ClaimedAt()
	if released, err := store.ReleaseAllClaims(ctx); err != nil || released != 1 {
		t.Fatalf("release claims=%d err=%v", released, err)
	}
	second, _, found, err := store.ClaimTask(ctx, combination.ID, "steam")
	if err != nil || !found || second.ID() != first.ID() || second.ClaimGeneration() != 2 {
		t.Fatalf("second claim=%+v found=%v err=%v", second, found, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE collection_tasks SET claimed_at = $2 WHERE task_id = $1`, int64(second.ID()), firstClaimedAt); err != nil {
		t.Fatal(err)
	}
	collectedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID: target.ID(), TaskID: first.ID(), CombinationID: combination.ID,
		ClaimGeneration: first.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(), CollectedAt: collectedAt,
	}); !errors.Is(err, ErrCollectionFence) {
		t.Fatalf("stale commit error=%v", err)
	}
	if err := store.CompleteTask(ctx, first.ID(), combination.ID, first.ClaimGeneration()); !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("stale complete error=%v", err)
	}
	if err := store.RequeueTask(ctx, first.ID(), combination.ID, first.ClaimGeneration()); err != nil {
		t.Fatalf("stale requeue error=%v", err)
	}
	var state string
	var generation int64
	if err := db.QueryRowContext(ctx, `SELECT state, claim_generation FROM collection_tasks WHERE task_id = $1`, int64(second.ID())).Scan(&state, &generation); err != nil {
		t.Fatal(err)
	}
	if state != "claimed" || generation != second.ClaimGeneration() {
		t.Fatalf("successor claim state=%s generation=%d", state, generation)
	}
	if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID: target.ID(), TaskID: second.ID(), CombinationID: combination.ID,
		ClaimGeneration: second.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(), CollectedAt: collectedAt,
	}); err != nil || !applied {
		t.Fatalf("successor commit applied=%v err=%v", applied, err)
	}
	if err := store.CompleteTask(ctx, second.ID(), combination.ID, second.ClaimGeneration()); err != nil {
		t.Fatal(err)
	}
}

func testCollectionEligibleTargets(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	defer func() { _ = db.Close() }()

	ask, combination := queueReadyFixture(t, store, "steam", 730, market.SideAsk)
	bid, err := store.CreateSummaryTarget(ctx, "steam", 730, market.SideBid, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []collection.Target{ask, bid} {
		if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
			t.Fatal(err)
		}
	}
	first, _, found, err := store.ClaimTaskForTargets(ctx, combination.ID, "steam", nil)
	if err != nil || !found {
		t.Fatalf("nil eligible claim=%+v found=%v err=%v", first, found, err)
	}
	if err := store.RequeueTask(ctx, first.ID(), combination.ID, first.ClaimGeneration()); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := store.ClaimTaskForTargets(ctx, combination.ID, "steam", []collection.TargetID{}); err != nil || found {
		t.Fatalf("empty eligible found=%v err=%v", found, err)
	}
	filtered, claimedTarget, found, err := store.ClaimTaskForTargets(ctx, combination.ID, "steam", []collection.TargetID{bid.ID()})
	if err != nil || !found || claimedTarget.ID() != bid.ID() || filtered.TargetID() != bid.ID() {
		t.Fatalf("filtered claim=%+v target=%+v found=%v err=%v", filtered, claimedTarget, found, err)
	}
}

func testCollectionRefillFence(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	defer func() { _ = db.Close() }()

	target, task, combination := queueCommitFixture(t, store, "steam", 730, market.SideAsk)
	stale := target
	collectedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID: target.ID(), TaskID: task.ID(), CombinationID: combination.ID,
		ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
		CollectedAt: collectedAt, AskTotal: 200,
	}); err != nil || !applied {
		t.Fatalf("worker commit applied=%v err=%v", applied, err)
	}
	if err := store.CompleteTask(ctx, task.ID(), combination.ID, task.ClaimGeneration()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueTasksFenced(ctx, stale, mustAskPageSpecs(t, 1), mustCollectionCursor(t, []byte("stale")), 0); !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("stale planner error=%v", err)
	}
	if depth, err := store.QueueDepth(ctx, target.ID()); err != nil || depth != 0 {
		t.Fatalf("stale planner depth=%d err=%v", depth, err)
	}
	current, found, err := store.Target(ctx, target.ID())
	if err != nil || !found || current.WriteSeq() != 1 || current.RefillTotal() != 200 {
		t.Fatalf("current target=%+v found=%v err=%v", current, found, err)
	}
}

// testCollectionOneClaimPerCombination 覆盖一组合一条认领、启动回队全部认领、删组合撞 claimed_by。
func testCollectionOneClaimPerCombination(t *testing.T, dsn string) {
	store, _ := migratedStore(t, dsn)
	ctx := t.Context()

	target, combination := queueReadyFixture(t, store, "steam", 730, market.SideAsk)
	if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 2), mustCollectionCursor(t, nil), 0); err != nil {
		t.Fatal(err)
	}
	first, _, ok, err := store.ClaimTask(ctx, combination.ID, "steam")
	if err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	if _, _, ok, err := store.ClaimTask(ctx, combination.ID, "steam"); err != nil || ok {
		t.Fatalf("busy combination claim ok=%v err=%v", ok, err)
	}
	if err := store.DeleteCombination(ctx, combination.ID); !errors.Is(err, ErrResourceDependency) {
		t.Fatalf("delete claimed combination = %v", err)
	}
	released, err := store.ReleaseAllClaims(ctx)
	if err != nil || released != 1 {
		t.Fatalf("release all claims released=%d err=%v", released, err)
	}
	reclaimed, _, ok, err := store.ClaimTask(ctx, combination.ID, "steam")
	if err != nil || !ok || reclaimed.ID() != first.ID() {
		t.Fatalf("reclaim after release all = %+v ok=%v err=%v", reclaimed, ok, err)
	}
	if err := store.RequeueTask(ctx, reclaimed.ID(), combination.ID, reclaimed.ClaimGeneration()); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCombination(ctx, combination.ID); err != nil {
		t.Fatalf("delete idle combination = %v", err)
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
	store, _ := migratedStore(t, dsn)
	ctx := t.Context()

	summary, combination := queueReadyFixture(t, store, "buff", 730, market.SideAsk)
	if err := store.EnqueueTasks(ctx, summary.ID(), summary.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
		t.Fatal(err)
	}
	first, _, ok, err := store.ClaimTask(ctx, combination.ID, "buff")
	if err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	if _, _, ok, err := store.ClaimTask(ctx, combination.ID, "buff"); err != nil || ok {
		t.Fatalf("empty queue claim ok=%v err=%v", ok, err)
	}
	if err := store.RequeueTask(ctx, first.ID(), combination.ID, first.ClaimGeneration()); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	type createResult struct {
		target  collection.Target
		claimed bool
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

	claimResults := make(chan createResult, workers)
	start = make(chan struct{})
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, _, claimed, err := store.ClaimTask(ctx, combination.ID, "buff")
			claimResults <- createResult{claimed: claimed, err: err}
		}()
	}
	close(start)
	group.Wait()
	close(claimResults)
	claimedCount := 0
	for result := range claimResults {
		if result.err != nil {
			t.Fatalf("concurrent claim: %v", result.err)
		}
		if result.claimed {
			claimedCount++
		}
	}
	if claimedCount != 1 {
		t.Fatalf("claimed count = %d, want 1", claimedCount)
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
	if err := store.EnqueueTasks(ctx, disabled.ID(), disabled.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); !errors.Is(err, ErrCollectionFence) {
		t.Fatalf("disabled enqueue error = %v", err)
	}

	snapshotTarget, err := store.CreateSummaryTarget(ctx, "steam", 10, market.SideAsk, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	writerErr := make(chan error, 1)
	readerErr := make(chan error, 1)
	go func() {
		for sequence := 1; sequence <= 24; sequence++ {
			cursor := mustCollectionCursor(t, []byte("snapshot-"+strconv.Itoa(sequence)))
			if err := store.EnqueueTasks(ctx, snapshotTarget.ID(), snapshotTarget.SwitchVersion(), mustAskPageSpecs(t, 1), cursor, int64(sequence)); err != nil {
				writerErr <- err
				return
			}
		}
		writerErr <- nil
	}()
	go func() {
		for range 96 {
			if _, err := store.QueueDepth(ctx, snapshotTarget.ID()); err != nil {
				readerErr <- err
				return
			}
			if _, err := store.ListWorkers(ctx); err != nil {
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
	if depth, err := store.QueueDepth(ctx, snapshotTarget.ID()); err != nil || depth != 24 {
		t.Fatalf("snapshot depth=%d err=%v", depth, err)
	}
}

func testCollectionIntegrityErrors(t *testing.T, dsn string) {
	t.Run("task payload mismatch", func(t *testing.T) {
		store, db := migratedStore(t, dsn)
		ctx := t.Context()
		target, combination := queueReadyFixture(t, store, "buff", 730, market.SideBid)
		if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE collection_tasks SET payload = '{"bad":true}'::jsonb WHERE target_id = $1`, int64(target.ID())); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := store.ClaimTask(ctx, combination.ID, "buff"); !errors.Is(err, ErrCollectionIntegrity) {
			t.Fatalf("corrupt task payload error = %v", err)
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

var queueFixtureSeq atomic.Uint64

// queueCommitFixture 建好组合、启用目标、入队并认领，提交页必须走这条路径。
func queueCommitFixture(t *testing.T, store *Store, platform collection.Platform, appID int64, side market.Side) (collection.Target, collection.Task, resource.AccountNodeCombination) {
	t.Helper()
	target, combination := queueReadyFixture(t, store, platform, appID, side)
	if err := store.EnqueueTasks(t.Context(), target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
		t.Fatal(err)
	}
	task, claimed, ok, err := store.ClaimTask(t.Context(), combination.ID, platform)
	if err != nil || !ok || task.TargetID() != target.ID() {
		t.Fatalf("ClaimTask() task=%+v ok=%v err=%v", task, ok, err)
	}
	return claimed, task, combination
}

func claimedCombination(t *testing.T, task collection.Task) resource.CombinationID {
	t.Helper()
	combinationID, ok := task.ClaimedBy()
	if !ok {
		t.Fatal("fixture task is not claimed")
	}
	return combinationID
}

func queueReadyFixture(t *testing.T, store *Store, platform collection.Platform, appID int64, side market.Side) (collection.Target, resource.AccountNodeCombination) {
	t.Helper()
	target, err := store.CreateSummaryTarget(t.Context(), platform, appID, side, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	_, combination := mustQueueCombination(t, store, platform)
	return target, combination
}

func mustQueueCombination(t *testing.T, store *Store, platform collection.Platform) (resource.PlatformAccount, resource.AccountNodeCombination) {
	t.Helper()
	ctx := t.Context()
	seq := queueFixtureSeq.Add(1)
	suffix := fmt.Sprintf("%s-%d", platform, seq)
	now := time.Now().UTC().Truncate(time.Microsecond)
	account, err := store.CreateAccount(ctx, resource.Platform(platform), "qa-"+suffix, []byte("qs-"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	account, err = store.RecordAccountSessionCheck(ctx, account.ID, account.SessionRevision, resource.AccountSessionStateValid, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateNode(ctx, "qn-"+suffix, resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionHongKong, EgressMode: resource.EgressModeStatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordNodeExit(ctx, node.ID, node.EgressRevision, netip.MustParseAddr("1.1.1.1"), now.Add(-time.Minute), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	combination, err := store.CreateCombination(ctx, account.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	return account, combination
}

func mustAskPageSpecs(t *testing.T, n int) []collection.EnqueueSpec {
	t.Helper()
	payload, err := collection.EncodeAskPage(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	specs := make([]collection.EnqueueSpec, n)
	for i := range specs {
		specs[i] = collection.EnqueueSpec{Kind: collection.TaskKindAskPage, Payload: payload}
	}
	return specs
}

func mustCollectionCursor(t *testing.T, value []byte) collection.Cursor {
	t.Helper()
	cursor, err := collection.NewCursor(value)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}
