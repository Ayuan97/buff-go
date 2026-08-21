//go:build !windows

package postgres

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	claimProcessHelperEnv = "BUFFGO_CLAIM_PROCESS_HELPER"
	claimProcessDSNEnv    = "BUFFGO_CLAIM_PROCESS_DSN"
	claimProcessSchemaEnv = "BUFFGO_CLAIM_PROCESS_SCHEMA"
	claimProcessComboEnv  = "BUFFGO_CLAIM_PROCESS_COMBINATION"
)

func TestCollectionClaimRecoversAfterProcessKill(t *testing.T) {
	dsn := os.Getenv("BUFFGO_TEST_DSN")
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN to an isolated PostgreSQL database")
	}
	store, db := migratedStore(t, dsn)
	target, combination := queueReadyFixture(t, store, "steam", 730, market.SideAsk)
	if err := store.EnqueueTasks(t.Context(), target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 1), mustCollectionCursor(t, nil), 0); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.QueryRowContext(t.Context(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestCollectionClaimProcessHelper$")
	cmd.Env = append(os.Environ(),
		claimProcessHelperEnv+"=1",
		claimProcessDSNEnv+"="+dsn,
		claimProcessSchemaEnv+"="+schema,
		claimProcessComboEnv+"="+strconv.FormatInt(int64(combination.ID), 10),
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		}
		close(ready)
	}()
	var oldEpoch collection.OwnerEpoch
	var taskID collection.TaskID
	var oldGeneration int64
	select {
	case line, ok := <-ready:
		if !ok {
			t.Fatal("claim helper exited before ready")
		}
		if _, err := fmt.Sscanf(line, "ready %d %d %d", &oldEpoch, &taskID, &oldGeneration); err != nil {
			t.Fatalf("parse helper ready %q: %v", line, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("claim helper did not become ready")
	}
	if oldEpoch < 1 || taskID < 1 || oldGeneration < 1 {
		t.Fatalf("invalid helper claim epoch=%d task=%d generation=%d", oldEpoch, taskID, oldGeneration)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		t.Fatalf("killed claim helper wait error=%v", waitErr)
	}
	waitStatus, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !waitStatus.Signaled() || waitStatus.Signal() != syscall.SIGKILL {
		t.Fatalf("claim helper exit status=%v", exitErr.Sys())
	}
	finished = true

	var state string
	var persistedGeneration int64
	if err := db.QueryRowContext(t.Context(), `
SELECT state, claim_generation FROM collection_tasks WHERE task_id = $1`, int64(taskID)).Scan(&state, &persistedGeneration); err != nil {
		t.Fatal(err)
	}
	if state != string(collection.TaskClaimed) || persistedGeneration != oldGeneration {
		t.Fatalf("claim after kill state=%s generation=%d", state, persistedGeneration)
	}

	var successor collection.InstanceLock
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		lock, acquired, err := store.AcquireInstanceLock(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if acquired {
			successor = lock
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if successor == nil {
		t.Fatal("successor did not acquire instance lock after process kill")
	}
	defer func() { _ = successor.Release(context.Background()) }()
	if successor.Epoch() != oldEpoch+1 {
		t.Fatalf("successor epoch=%d want=%d", successor.Epoch(), oldEpoch+1)
	}
	currentCtx := collection.WithOwnerEpoch(t.Context(), successor.Epoch())
	oldCtx := collection.WithOwnerEpoch(t.Context(), oldEpoch)
	if _, err := store.ReleaseAllClaims(oldCtx); !errors.Is(err, collection.ErrOwnerFence) {
		t.Fatalf("stale owner recovery error=%v", err)
	}
	released, err := store.ReleaseAllClaims(currentCtx)
	if err != nil || released != 1 {
		t.Fatalf("successor recovery released=%d err=%v", released, err)
	}

	reclaimed, claimedTarget, found, err := store.ClaimTaskForTargets(currentCtx, combination.ID, "steam", nil)
	if err != nil || !found {
		t.Fatalf("successor claim found=%v err=%v", found, err)
	}
	if reclaimed.ID() != taskID || reclaimed.ClaimGeneration() != oldGeneration+1 {
		t.Fatalf("successor claim task=%d generation=%d", reclaimed.ID(), reclaimed.ClaimGeneration())
	}
	if err := store.CompleteTask(currentCtx, taskID, combination.ID, oldGeneration); !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("old generation complete error=%v", err)
	}
	if _, _, err := store.CommitSummaryPage(currentCtx, SummaryPageCommit{
		TargetID:        claimedTarget.ID(),
		TaskID:          taskID,
		CombinationID:   combination.ID,
		ClaimGeneration: oldGeneration,
		ExpectedSwitch:  claimedTarget.SwitchVersion(),
		CursorBefore:    mustCollectionCursor(t, nil),
		CursorAfter:     mustCollectionCursor(t, nil),
		CollectedAt:     time.Now().UTC().Truncate(time.Microsecond),
	}); !errors.Is(err, ErrCollectionFence) {
		t.Fatalf("old generation commit error=%v", err)
	}
	if err := store.RequeueTask(currentCtx, taskID, combination.ID, oldGeneration); err != nil {
		t.Fatalf("old generation requeue error=%v", err)
	}
	var claimedBy sql.NullInt64
	if err := db.QueryRowContext(t.Context(), `
SELECT state, claimed_by, claim_generation FROM collection_tasks WHERE task_id = $1`, int64(taskID)).Scan(&state, &claimedBy, &persistedGeneration); err != nil {
		t.Fatal(err)
	}
	if state != string(collection.TaskClaimed) || !claimedBy.Valid || claimedBy.Int64 != int64(combination.ID) || persistedGeneration != oldGeneration+1 {
		t.Fatalf("stale writes changed successor claim state=%s owner=%v generation=%d", state, claimedBy, persistedGeneration)
	}
	if err := store.RequeueTask(currentCtx, taskID, combination.ID, oldGeneration+1); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionClaimProcessHelper(t *testing.T) {
	if os.Getenv(claimProcessHelperEnv) != "1" {
		return
	}
	dsn := os.Getenv(claimProcessDSNEnv)
	schema := os.Getenv(claimProcessSchemaEnv)
	combinationValue := os.Getenv(claimProcessComboEnv)
	if dsn == "" || schema == "" || strings.ContainsAny(schema, `"' ;`) {
		t.Fatal("invalid claim helper configuration")
	}
	combinationID, err := strconv.ParseInt(combinationValue, 10, 64)
	if err != nil || combinationID < 1 {
		t.Fatal("invalid claim helper combination")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	lock, acquired, err := store.AcquireInstanceLock(t.Context())
	if err != nil || !acquired {
		t.Fatalf("claim helper lock acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = lock.Release(context.Background()) }()
	ownerCtx := collection.WithOwnerEpoch(t.Context(), lock.Epoch())
	task, _, found, err := store.ClaimTaskForTargets(ownerCtx, resource.CombinationID(combinationID), "steam", nil)
	if err != nil || !found {
		t.Fatalf("claim helper task found=%v err=%v", found, err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "ready %d %d %d\n", lock.Epoch(), task.ID(), task.ClaimGeneration())
	for {
		time.Sleep(time.Hour)
	}
}
