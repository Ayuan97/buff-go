package postgres

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
)

// Collection contract errors are canonical in the collection domain package so
// that the scheduler can classify storage results; the storage names remain the
// same values, and errors.Is keeps working across both packages.
var (
	ErrCollectionInvalidInput   = collection.ErrInvalidInput
	ErrCollectionNotFound       = collection.ErrNotFound
	ErrCollectionTargetDisabled = collection.ErrTargetDisabled
	ErrCollectionConflict       = collection.ErrConflict
	ErrCollectionIntegrity      = collection.ErrIntegrity
	ErrCollectionStorage        = collection.ErrStorage
)

const (
	collectionTargetColumns = `
target_id, kind, platform, appid, side, desired_state, actual_state,
reason_code, recovery_mode, next_check_at, period_microseconds, revision,
switch_version, next_run_sequence, changed_at`
	collectionRunColumns = `
run_id, target_id, kind, platform, appid, side, product_id, switch_version,
run_sequence, status, completeness, reason_code, current_cursor,
last_page_sequence, created_at, started_at, finished_at`
	maxRunListLimit = 100
)

// TargetTransition is one explicit scheduler-owned actual-state transition.
// The canonical shape lives in the collection domain next to its scheduler.
type TargetTransition = collection.TargetTransition

type targetCreate struct {
	platform collection.Platform
	appID    int64
	side     market.Side
	desired  collection.DesiredState
}

// CreateSummaryTarget creates or reads the natural (platform, appid, side) target.
func (s *Store) CreateSummaryTarget(ctx context.Context, platform collection.Platform, appID int64, side market.Side, desired collection.DesiredState) (collection.Target, error) {
	if platform.Validate() != nil || appID < 1 || !validCollectionSide(side) || desired.Validate() != nil {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	return s.createCollectionTarget(ctx, targetCreate{
		platform: platform,
		appID:    appID,
		side:     side,
		desired:  desired,
	})
}

func (s *Store) createCollectionTarget(ctx context.Context, input targetCreate) (collection.Target, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Target{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	identity := collectionTargetLockKey(input)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, identity); err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}

	existing, _, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE kind = 'summary' AND platform = $1 AND appid = $2 AND side = $3
FOR UPDATE`, string(input.platform), input.appID, string(input.side))
	if err != nil {
		return collection.Target{}, err
	}
	if found {
		if !sameTargetCreate(existing, input) {
			return collection.Target{}, ErrCollectionConflict
		}
		if err := tx.Commit(); err != nil {
			return collection.Target{}, collectionStorageError(ctx)
		}
		return existing, nil
	}

	actual := collection.ActualStarting
	if input.desired == collection.DesiredDisabled {
		actual = collection.ActualStopped
	}
	created, _, err := scanCollectionTarget(tx.QueryRowContext(ctx, `
INSERT INTO collection_targets (
    kind, platform, appid, side, desired_state, actual_state, changed_at
) VALUES ('summary', $1, $2, $3, $4, $5,
          date_trunc('microseconds', clock_timestamp()))
RETURNING `+collectionTargetColumns,
		string(input.platform), input.appID, string(input.side),
		string(input.desired), string(actual),
	))
	if err != nil {
		return collection.Target{}, mapCollectionWriteError(ctx, err)
	}
	if !sameTargetCreate(created, input) {
		return collection.Target{}, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}
	return created, nil
}

// Target returns one target by its stable identity.
func (s *Store) Target(ctx context.Context, id collection.TargetID) (collection.Target, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Target{}, false, err
	}
	if id.Validate() != nil {
		return collection.Target{}, false, ErrCollectionInvalidInput
	}
	target, _, err := scanCollectionTarget(s.db.QueryRowContext(ctx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Target{}, false, nil
	}
	if err != nil {
		return collection.Target{}, false, mapCollectionReadError(ctx, err)
	}
	return target, true, nil
}

// Targets returns all persisted targets in stable identity order for restart recovery.
func (s *Store) Targets(ctx context.Context) ([]collection.Target, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
ORDER BY target_id`)
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	targets := make([]collection.Target, 0)
	for rows.Next() {
		target, _, err := scanCollectionTarget(rows)
		if err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return targets, nil
}

// SetTargetDesired applies an enable or disable request with strict revision CAS.
func (s *Store) SetTargetDesired(ctx context.Context, id collection.TargetID, expected collection.Revision, desired collection.DesiredState) (collection.Target, error) {
	if desired.Validate() != nil {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	return s.mutateCollectionTarget(ctx, id, expected, nil, func(current collection.Target, at time.Time) (collection.Target, error) {
		if desired == collection.DesiredEnabled {
			return current.Enable(at)
		}
		return current.Disable(at)
	})
}

// TransitionTarget changes only scheduler-owned actual state. It checks both
// entity revision and switch fence so a runtime from an earlier enable cycle
// cannot publish target state after disable/re-enable.
func (s *Store) TransitionTarget(ctx context.Context, id collection.TargetID, expected collection.Revision, expectedSwitch collection.Revision, transition TargetTransition) (collection.Target, error) {
	if transition.RecheckAt != nil {
		value := *transition.RecheckAt
		transition.RecheckAt = &value
	}
	if !validTargetTransition(transition) {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	return s.mutateCollectionTarget(ctx, id, expected, &expectedSwitch, func(current collection.Target, at time.Time) (collection.Target, error) {
		if targetHasTransition(current, transition) {
			return current, nil
		}
		switch transition.State {
		case collection.ActualWaiting:
			return current.MarkWaiting(transition.Reason, *transition.RecheckAt, at)
		case collection.ActualBlocked:
			return current.MarkBlocked(transition.Reason, transition.RecheckAt, at)
		case collection.ActualRunning:
			return current.MarkRunning(at)
		case collection.ActualStopped:
			return current.MarkStopped(at)
		case collection.ActualError:
			return current.MarkError(transition.Reason, at)
		default:
			return collection.Target{}, ErrCollectionInvalidInput
		}
	})
}

// RecoverTarget records an explicit operator recovery without changing the
// enable-cycle switch fence.
func (s *Store) RecoverTarget(ctx context.Context, id collection.TargetID, expected collection.Revision, expectedSwitch collection.Revision) (collection.Target, error) {
	return s.mutateCollectionTarget(ctx, id, expected, &expectedSwitch, func(current collection.Target, at time.Time) (collection.Target, error) {
		return current.Recover(at)
	})
}

func (s *Store) mutateCollectionTarget(
	ctx context.Context,
	id collection.TargetID,
	expected collection.Revision,
	expectedSwitch *collection.Revision,
	mutate func(collection.Target, time.Time) (collection.Target, error),
) (collection.Target, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Target{}, err
	}
	if id.Validate() != nil || expected.Validate() != nil || (expectedSwitch != nil && expectedSwitch.Validate() != nil) {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	current, _, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1
FOR UPDATE`, int64(id))
	if err != nil {
		return collection.Target{}, err
	}
	if !found {
		return collection.Target{}, ErrCollectionNotFound
	}
	if current.Revision() != expected || (expectedSwitch != nil && current.SwitchVersion() != *expectedSwitch) {
		return collection.Target{}, ErrCollectionConflict
	}
	at, err := collectionDatabaseTime(ctx, tx, current.ChangedAt())
	if err != nil {
		return collection.Target{}, err
	}
	next, err := mutate(current, at)
	if err != nil {
		if errors.Is(err, ErrCollectionInvalidInput) {
			return collection.Target{}, err
		}
		return collection.Target{}, ErrCollectionConflict
	}
	if next.Revision() == current.Revision() {
		if err := tx.Commit(); err != nil {
			return collection.Target{}, collectionStorageError(ctx)
		}
		return current, nil
	}
	if !validTargetSuccessor(current, next) {
		return collection.Target{}, ErrCollectionIntegrity
	}

	stored, _, err := scanCollectionTarget(tx.QueryRowContext(ctx, `
UPDATE collection_targets
SET desired_state = $3, actual_state = $4, reason_code = $5,
    recovery_mode = $6, next_check_at = $7, period_microseconds = $8,
    revision = $9, switch_version = $10, changed_at = $11
WHERE target_id = $1 AND revision = $2
RETURNING `+collectionTargetColumns,
		int64(id), int64(expected), string(next.Desired()), string(next.Actual()), string(next.Reason()),
		string(next.Recovery()), targetRecheckValue(next), targetPeriodValue(next),
		int64(next.Revision()), int64(next.SwitchVersion()), next.ChangedAt(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Target{}, ErrCollectionConflict
	}
	if err != nil {
		return collection.Target{}, mapCollectionWriteError(ctx, err)
	}
	if !sameTargetState(stored, next) {
		return collection.Target{}, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}
	return stored, nil
}

// CreateSummaryRun creates one active summary run for the target's game, or
// returns the current-switch active run.
func (s *Store) CreateSummaryRun(ctx context.Context, targetID collection.TargetID, expectedSwitch collection.Revision, initialCursor collection.Cursor) (collection.Run, bool, error) {
	return s.createCollectionRun(ctx, targetID, expectedSwitch, initialCursor)
}

func (s *Store) createCollectionRun(ctx context.Context, targetID collection.TargetID, expectedSwitch collection.Revision, initialCursor collection.Cursor) (collection.Run, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Run{}, false, err
	}
	if targetID.Validate() != nil || expectedSwitch.Validate() != nil || initialCursor.Validate() != nil {
		return collection.Run{}, false, ErrCollectionInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Run{}, false, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	target, nextSequence, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1
FOR UPDATE`, int64(targetID))
	if err != nil {
		return collection.Run{}, false, err
	}
	if !found {
		return collection.Run{}, false, ErrCollectionNotFound
	}
	if target.TaskType() != collection.TaskTypeSummary || target.SwitchVersion() != expectedSwitch {
		return collection.Run{}, false, ErrCollectionConflict
	}
	if target.Desired() != collection.DesiredEnabled {
		return collection.Run{}, false, ErrCollectionTargetDisabled
	}
	appID, ok := target.AppID()
	if !ok || appID < 1 {
		return collection.Run{}, false, ErrCollectionIntegrity
	}

	active, activeFound, err := activeCollectionRun(ctx, tx, target)
	if err != nil {
		return collection.Run{}, false, err
	}
	if activeFound {
		switchVersion, present := active.SwitchVersion()
		if !present || switchVersion != target.SwitchVersion() {
			return collection.Run{}, false, ErrCollectionConflict
		}
		if err := tx.Commit(); err != nil {
			return collection.Run{}, false, collectionStorageError(ctx)
		}
		return active, false, nil
	}
	if nextSequence < 1 || nextSequence == math.MaxInt64 {
		return collection.Run{}, false, ErrCollectionConflict
	}
	result, err := tx.ExecContext(ctx, `
UPDATE collection_targets
SET next_run_sequence = next_run_sequence + 1
WHERE target_id = $1 AND next_run_sequence = $2`, int64(targetID), nextSequence)
	if err != nil {
		return collection.Run{}, false, mapCollectionWriteError(ctx, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return collection.Run{}, false, ErrCollectionIntegrity
	}

	side, _ := target.Side()
	created, err := scanCollectionRun(tx.QueryRowContext(ctx, `
INSERT INTO collection_runs (
    target_id, kind, platform, appid, side, switch_version, run_sequence,
    status, completeness, reason_code, current_cursor, last_page_sequence,
    created_at
) VALUES ($1, 'summary', $2, $3, $4, $5, $6, 'pending', NULL, '', $7, 0,
          date_trunc('microseconds', clock_timestamp()))
RETURNING `+collectionRunColumns,
		int64(targetID), string(target.Platform()), appID, string(side),
		int64(target.SwitchVersion()), nextSequence, collectionCursorBytes(initialCursor),
	))
	if err != nil {
		return collection.Run{}, false, mapCollectionWriteError(ctx, err)
	}
	if !runMatchesTarget(created, target) {
		return collection.Run{}, false, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return collection.Run{}, false, collectionStorageError(ctx)
	}
	return created, true, nil
}

// BeginRun moves a pending run to running after rechecking its current switch.
func (s *Store) BeginRun(ctx context.Context, id collection.RunID) (collection.Run, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Run{}, err
	}
	if id.Validate() != nil {
		return collection.Run{}, ErrCollectionInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Run{}, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	run, target, hasTarget, err := lockCollectionRun(ctx, tx, id)
	if err != nil {
		return collection.Run{}, err
	}
	if hasTarget {
		if target.Desired() != collection.DesiredEnabled {
			return collection.Run{}, ErrCollectionTargetDisabled
		}
		switchVersion, present := run.SwitchVersion()
		if !present || switchVersion != target.SwitchVersion() {
			return collection.Run{}, ErrCollectionConflict
		}
	}
	if run.State() == collection.RunRunning {
		if err := tx.Commit(); err != nil {
			return collection.Run{}, collectionStorageError(ctx)
		}
		return run, nil
	}
	if run.State() != collection.RunPending {
		return collection.Run{}, ErrCollectionConflict
	}
	at, err := collectionDatabaseTime(ctx, tx, run.CreatedAt())
	if err != nil {
		return collection.Run{}, err
	}
	next, err := run.Begin(at)
	if err != nil {
		return collection.Run{}, ErrCollectionConflict
	}
	stored, err := updateCollectionRun(ctx, tx, run, next)
	if err != nil {
		return collection.Run{}, err
	}
	if hasTarget && !runMatchesTarget(stored, target) {
		return collection.Run{}, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return collection.Run{}, collectionStorageError(ctx)
	}
	return stored, nil
}

// FinishRun records one terminal result. An exact terminal retry is idempotent.
func (s *Store) FinishRun(ctx context.Context, id collection.RunID, state collection.RunState, completeness collection.Completeness, reason collection.RunReason) (collection.Run, error) {
	if !validRunFinish(state, completeness, reason) {
		return collection.Run{}, ErrCollectionInvalidInput
	}
	if err := s.validateCollectionStore(); err != nil {
		return collection.Run{}, err
	}
	if id.Validate() != nil {
		return collection.Run{}, ErrCollectionInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Run{}, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	run, target, hasTarget, err := lockCollectionRun(ctx, tx, id)
	if err != nil {
		return collection.Run{}, err
	}
	if run.State().Terminal() {
		if run.State() != state || run.Completeness() != completeness || run.Reason() != reason {
			return collection.Run{}, ErrCollectionConflict
		}
		if err := tx.Commit(); err != nil {
			return collection.Run{}, collectionStorageError(ctx)
		}
		return run, nil
	}
	if state == collection.RunSucceeded && run.State() != collection.RunRunning {
		return collection.Run{}, ErrCollectionConflict
	}
	floor := run.CreatedAt()
	if started, ok := run.StartedAt(); ok {
		floor = started
	}
	if run.LastPageSequence() > 0 {
		lastPage, found, err := collectionPageForUpdate(ctx, tx, run.ID(), collection.Sequence(run.LastPageSequence()))
		if err != nil {
			return collection.Run{}, err
		}
		if !found || lastPage.PageSequence() != collection.Sequence(run.LastPageSequence()) {
			return collection.Run{}, ErrCollectionIntegrity
		}
		if lastPage.CommittedAt().After(floor) {
			floor = lastPage.CommittedAt()
		}
	}
	at, err := collectionDatabaseTime(ctx, tx, floor)
	if err != nil {
		return collection.Run{}, err
	}
	var next collection.Run
	switch state {
	case collection.RunSucceeded:
		next, err = run.Succeed(completeness, at)
	case collection.RunFailed:
		next, err = run.Fail(completeness, reason, at)
	case collection.RunStopped:
		next, err = run.Stop(completeness, reason, at)
	}
	if err != nil {
		return collection.Run{}, ErrCollectionConflict
	}
	stored, err := updateCollectionRun(ctx, tx, run, next)
	if err != nil {
		return collection.Run{}, err
	}
	if hasTarget && !runMatchesTarget(stored, target) {
		return collection.Run{}, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return collection.Run{}, collectionStorageError(ctx)
	}
	return stored, nil
}

// Run returns one run and verifies its immutable target shape.
func (s *Store) Run(ctx context.Context, id collection.RunID) (collection.Run, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Run{}, false, err
	}
	if id.Validate() != nil {
		return collection.Run{}, false, ErrCollectionInvalidInput
	}
	run, err := scanCollectionRun(s.db.QueryRowContext(ctx, `
SELECT `+collectionRunColumns+`
FROM collection_runs
WHERE run_id = $1`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Run{}, false, nil
	}
	if err != nil {
		return collection.Run{}, false, mapCollectionReadError(ctx, err)
	}
	if targetID, present := run.TargetID(); present {
		target, found, err := s.Target(ctx, targetID)
		if err != nil {
			return collection.Run{}, false, err
		}
		if !found || !runMatchesTarget(run, target) {
			return collection.Run{}, false, ErrCollectionIntegrity
		}
	}
	return run, true, nil
}

// RunsForTarget returns the newest runs for one target by target-global sequence.
func (s *Store) RunsForTarget(ctx context.Context, targetID collection.TargetID, limit int) ([]collection.Run, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	if targetID.Validate() != nil || limit < 1 || limit > maxRunListLimit {
		return nil, ErrCollectionInvalidInput
	}
	target, found, err := s.Target(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrCollectionNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+collectionRunColumns+`
FROM collection_runs
WHERE target_id = $1
ORDER BY run_sequence DESC
LIMIT $2`, int64(targetID), limit)
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	runs := make([]collection.Run, 0)
	for rows.Next() {
		run, err := scanCollectionRun(rows)
		if err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		if !runMatchesTarget(run, target) {
			return nil, ErrCollectionIntegrity
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return runs, nil
}

// ListRecentRuns returns the newest collection runs across all targets.
func (s *Store) ListRecentRuns(ctx context.Context, limit int) ([]collection.Run, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > maxRunListLimit {
		return nil, ErrCollectionInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+collectionRunColumns+`
FROM collection_runs
ORDER BY run_id DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	runs := make([]collection.Run, 0)
	for rows.Next() {
		run, err := scanCollectionRun(rows)
		if err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return runs, nil
}

// ActiveRuns returns every non-terminal scheduler-owned run ordered by
// identity. Detail runs are excluded because they have no target and are not
// dispatched by the resident scheduler. Summary active runs are bounded by
// their active-run unique index, so no limit is required.
func (s *Store) ActiveRuns(ctx context.Context) ([]collection.Run, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+collectionRunColumns+`
FROM collection_runs
WHERE kind = 'summary' AND status IN ('pending', 'running')
ORDER BY run_id`)
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	runs := make([]collection.Run, 0)
	for rows.Next() {
		run, err := scanCollectionRun(rows)
		if err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return runs, nil
}

// ListCombinationsFor returns pairings assigned to this game direction.
func (s *Store) ListCombinationsFor(ctx context.Context, platform collection.Platform, appID int64, side market.Side) ([]resource.AccountNodeCombination, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	if platform.Validate() != nil || appID < 1 || !validCollectionSide(side) {
		return nil, ErrCollectionInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.combination_id, c.platform, c.account_id, c.node_id
FROM account_node_combinations c
JOIN access_nodes n ON n.node_id = c.node_id
JOIN node_direction_assignments d ON d.node_id = c.node_id AND d.platform = c.platform
WHERE c.platform = $1 AND n.appid = $2 AND d.side = $3
ORDER BY c.combination_id`, string(platform), appID, string(side))
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	combinations := make([]resource.AccountNodeCombination, 0)
	for rows.Next() {
		combination, err := scanCombination(rows)
		if err != nil {
			return nil, ErrCollectionIntegrity
		}
		combinations = append(combinations, combination)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return combinations, nil
}

// collectionInstanceLockSQL scopes the resident-scheduler lock to the current
// schema so one database can host isolated collection states.
const collectionInstanceLockKeySQL = `hashtextextended('collection-daemon:' || current_schema(), 0)`

// AcquireInstanceLock takes a session-scoped advisory lock so that only one
// resident scheduler owns this schema's collection state. The lock holds one
// pooled connection until it is released. Deployments must keep a session-level
// connection to PostgreSQL: a pooler in transaction or statement mode moves the
// lock off the caller's session and voids the guarantee. A dead process only
// releases the lock once the server tears down its backend, which for a lost
// host waits out the TCP keepalive budget rather than happening immediately.
func (s *Store) AcquireInstanceLock(ctx context.Context) (collection.InstanceLock, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, false, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, collectionStorageError(ctx)
	}
	// 锁键在取锁时定格一次：此后校验与释放都用同一个键，不会因会话的
	// search_path 变化而去操作另一把锁。
	var key int64
	var acquired bool
	query := `SELECT lock_key, pg_try_advisory_lock(lock_key) FROM (SELECT ` +
		collectionInstanceLockKeySQL + ` AS lock_key) resolved`
	if err := conn.QueryRowContext(ctx, query).Scan(&key, &acquired); err != nil {
		_ = conn.Close()
		return nil, false, collectionStorageError(ctx)
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return &collectionInstanceLock{conn: conn, key: key}, true, nil
}

type collectionInstanceLock struct {
	mu   sync.Mutex
	conn *sql.Conn
	key  int64
}

// collectionInstanceLockHeldSQL asks the locked session whether it still holds
// the advisory lock. Reachability is not enough: a connection pooler resetting
// the server session drops every advisory lock while the connection stays
// usable, so ownership must be read back from pg_locks. The single-argument
// advisory lock splits its key across classid and objid with objsubid 1.
const collectionInstanceLockHeldSQL = `
SELECT EXISTS (
    SELECT 1
    FROM pg_locks
    WHERE locktype = 'advisory'
      AND granted
      AND pid = pg_backend_pid()
      AND objsubid = 1
      AND classid = $1::oid
      AND objid = $2::oid
)`

// Verify re-reads lock ownership on the locked session so a resident scheduler
// can prove it still owns the collection state before writing. Ownership is a
// plain existence check, which is exact only because each lock owns its own
// connection and takes the advisory lock once.
func (lock *collectionInstanceLock) Verify(ctx context.Context) error {
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.conn == nil {
		return ErrCollectionStorage
	}
	var held bool
	classID, objID := advisoryLockParts(lock.key)
	if err := lock.conn.QueryRowContext(ctx, collectionInstanceLockHeldSQL, classID, objID).Scan(&held); err != nil {
		return collectionStorageError(ctx)
	}
	if !held {
		return ErrCollectionStorage
	}
	return nil
}

// advisoryLockParts splits a signed advisory key into the unsigned halves
// PostgreSQL records as classid and objid.
func advisoryLockParts(key int64) (int64, int64) {
	return int64(uint32(uint64(key) >> 32)), int64(uint32(uint64(key)))
}

// Release unlocks before returning the connection to the pool: a session-level
// advisory lock outlives the pooled handle, so it must be dropped explicitly.
func (lock *collectionInstanceLock) Release(ctx context.Context) error {
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.conn == nil {
		return nil
	}
	conn := lock.conn
	lock.conn = nil
	var released bool
	unlockErr := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, lock.key).Scan(&released)
	closeErr := conn.Close()
	if unlockErr != nil || !released {
		return ErrCollectionStorage
	}
	if closeErr != nil {
		return ErrCollectionStorage
	}
	return nil
}

// Pages returns committed pages in ascending contiguous sequence order.
func (s *Store) Pages(ctx context.Context, runID collection.RunID) ([]collection.Page, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	if runID.Validate() != nil {
		return nil, ErrCollectionInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	run, found, err := readCollectionRunTx(ctx, tx, runID)
	if err != nil {
		return nil, err
	} else if !found {
		return nil, ErrCollectionNotFound
	}
	rows, err := tx.QueryContext(ctx, `
SELECT run_id, page_sequence, cursor_before, cursor_after, payload_digest,
       collected_at, committed_at
FROM collection_pages
WHERE run_id = $1
ORDER BY page_sequence`, int64(runID))
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	pages := make([]collection.Page, 0)
	var expected collection.Sequence = 1
	var previousAfter collection.Cursor
	startedAt, started := run.StartedAt()
	finishedAt, finished := run.FinishedAt()
	for rows.Next() {
		page, err := scanCollectionPage(rows)
		if err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		if page.PageSequence() != expected {
			return nil, ErrCollectionIntegrity
		}
		if !started || page.CollectedAt().Before(startedAt) {
			return nil, ErrCollectionIntegrity
		}
		if finished && page.CommittedAt().After(finishedAt) {
			return nil, ErrCollectionIntegrity
		}
		if expected > 1 && !page.CursorBefore().Equal(previousAfter) {
			return nil, ErrCollectionIntegrity
		}
		pages = append(pages, page)
		previousAfter = page.CursorAfter()
		expected++
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	if err := rows.Close(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	if int64(len(pages)) != run.LastPageSequence() ||
		(len(pages) > 0 && !previousAfter.Equal(run.CurrentCursor())) {
		return nil, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return pages, nil
}

func readCollectionRunTx(ctx context.Context, tx *sql.Tx, id collection.RunID) (collection.Run, bool, error) {
	run, err := scanCollectionRun(tx.QueryRowContext(ctx, `
SELECT `+collectionRunColumns+`
FROM collection_runs
WHERE run_id = $1`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Run{}, false, nil
	}
	if err != nil {
		return collection.Run{}, false, mapCollectionReadError(ctx, err)
	}
	if targetID, present := run.TargetID(); present {
		target, _, err := scanCollectionTarget(tx.QueryRowContext(ctx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1`, int64(targetID)))
		if errors.Is(err, sql.ErrNoRows) {
			return collection.Run{}, false, ErrCollectionIntegrity
		}
		if err != nil {
			return collection.Run{}, false, mapCollectionReadError(ctx, err)
		}
		if !runMatchesTarget(run, target) {
			return collection.Run{}, false, ErrCollectionIntegrity
		}
	}
	return run, true, nil
}

func lockCollectionRun(ctx context.Context, tx *sql.Tx, id collection.RunID) (collection.Run, collection.Target, bool, error) {
	var targetID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT target_id FROM collection_runs WHERE run_id = $1`, int64(id)).Scan(&targetID); errors.Is(err, sql.ErrNoRows) {
		return collection.Run{}, collection.Target{}, false, ErrCollectionNotFound
	} else if err != nil {
		return collection.Run{}, collection.Target{}, false, collectionStorageError(ctx)
	}
	var target collection.Target
	if targetID.Valid {
		locked, _, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1
FOR UPDATE`, targetID.Int64)
		if err != nil {
			return collection.Run{}, collection.Target{}, false, err
		}
		if !found {
			return collection.Run{}, collection.Target{}, false, ErrCollectionIntegrity
		}
		target = locked
	}
	run, err := scanCollectionRun(tx.QueryRowContext(ctx, `
SELECT `+collectionRunColumns+`
FROM collection_runs
WHERE run_id = $1
FOR UPDATE`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Run{}, collection.Target{}, false, ErrCollectionNotFound
	}
	if err != nil {
		return collection.Run{}, collection.Target{}, false, mapCollectionReadError(ctx, err)
	}
	if targetID.Valid && !runMatchesTarget(run, target) {
		return collection.Run{}, collection.Target{}, false, ErrCollectionIntegrity
	}
	return run, target, targetID.Valid, nil
}

func updateCollectionRun(ctx context.Context, tx *sql.Tx, current, next collection.Run) (collection.Run, error) {
	stored, err := scanCollectionRun(tx.QueryRowContext(ctx, `
UPDATE collection_runs
SET status = $3, completeness = $4, reason_code = $5,
    started_at = $6, finished_at = $7
WHERE run_id = $1 AND status = $2
RETURNING `+collectionRunColumns,
		int64(current.ID()), string(current.State()), string(next.State()),
		nullableCompleteness(next.Completeness()), string(next.Reason()), runStartedValue(next), runFinishedValue(next),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Run{}, ErrCollectionConflict
	}
	if err != nil {
		return collection.Run{}, mapCollectionWriteError(ctx, err)
	}
	if !sameRun(stored, next) {
		return collection.Run{}, ErrCollectionIntegrity
	}
	return stored, nil
}

func activeCollectionRun(ctx context.Context, tx *sql.Tx, target collection.Target) (collection.Run, bool, error) {
	run, err := scanCollectionRun(tx.QueryRowContext(ctx, `
SELECT `+collectionRunColumns+`
FROM collection_runs
WHERE target_id = $1 AND kind = 'summary' AND status IN ('pending', 'running')
FOR UPDATE`, int64(target.ID())))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Run{}, false, nil
	}
	if err != nil {
		return collection.Run{}, false, mapCollectionReadError(ctx, err)
	}
	if !runMatchesTarget(run, target) {
		return collection.Run{}, false, ErrCollectionIntegrity
	}
	return run, true, nil
}

func collectionDatabaseTime(ctx context.Context, tx *sql.Tx, floor time.Time) (time.Time, error) {
	var at time.Time
	if err := tx.QueryRowContext(ctx, `
SELECT date_trunc('microseconds', GREATEST(clock_timestamp(), $1::timestamptz))`, floor).Scan(&at); err != nil {
		return time.Time{}, collectionStorageError(ctx)
	}
	return normalizePostgresTime(at), nil
}

func (s *Store) validateCollectionStore() error {
	if s == nil || s.db == nil {
		return ErrCollectionStorage
	}
	return nil
}

func collectionStorageError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrCollectionStorage
}

func mapCollectionReadError(ctx context.Context, err error) error {
	if errors.Is(err, ErrCollectionIntegrity) {
		return ErrCollectionIntegrity
	}
	return collectionStorageError(ctx)
}

func mapCollectionWriteError(ctx context.Context, err error) error {
	if postgresErrorCode(err) == "23505" {
		return ErrCollectionConflict
	}
	return collectionStorageError(ctx)
}

func validCollectionSide(side market.Side) bool {
	return side == market.SideBid || side == market.SideAsk
}

func validTargetTransition(transition TargetTransition) bool {
	if transition.State.Validate() != nil || transition.Reason.Validate() != nil {
		return false
	}
	automaticTime := func() bool {
		return transition.RecheckAt != nil && validCollectionTime(*transition.RecheckAt)
	}
	manual := func() bool { return transition.RecheckAt == nil }
	switch transition.State {
	case collection.ActualWaiting:
		return (transition.Reason == collection.TargetReasonNextCycle ||
			transition.Reason == collection.TargetReasonSchedulerOpportunity ||
			transition.Reason == collection.TargetReasonTransientFailure) && automaticTime()
	case collection.ActualBlocked:
		switch transition.Reason {
		case collection.TargetReasonNoCombination, collection.TargetReasonCooldown, collection.TargetReasonEgressUnavailable:
			return automaticTime()
		case collection.TargetReasonMissingRatePolicy, collection.TargetReasonSessionInvalid,
			collection.TargetReasonInvalidConfig, collection.TargetReasonInterfaceUnverified:
			return manual()
		default:
			return false
		}
	case collection.ActualRunning, collection.ActualStopped:
		return transition.Reason == collection.TargetReasonNone && manual()
	case collection.ActualError:
		return (transition.Reason == collection.TargetReasonSchedulerFailure ||
			transition.Reason == collection.TargetReasonStateIntegrity) && manual()
	default:
		return false
	}
}

func targetHasTransition(target collection.Target, transition TargetTransition) bool {
	if target.Actual() != transition.State || target.Reason() != transition.Reason {
		return false
	}
	stored, present := target.RecheckAt()
	if transition.RecheckAt == nil {
		return !present
	}
	return present && stored.Equal(*transition.RecheckAt)
}

func validRunFinish(state collection.RunState, completeness collection.Completeness, reason collection.RunReason) bool {
	if completeness.ValidateTerminal() != nil || reason.Validate() != nil {
		return false
	}
	switch state {
	case collection.RunSucceeded:
		return reason == collection.RunReasonNone
	case collection.RunFailed:
		switch reason {
		case collection.RunReasonNetworkError, collection.RunReasonPlatformError, collection.RunReasonTimeout,
			collection.RunReasonLoginInvalid, collection.RunReasonParseError, collection.RunReasonSemanticError,
			collection.RunReasonConfigurationError, collection.RunReasonInternalError, collection.RunReasonProcessRestarted:
			return true
		}
	case collection.RunStopped:
		return reason == collection.RunReasonSwitchDisabled || reason == collection.RunReasonCancelled
	}
	return false
}

func validCollectionTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Year() >= 1 && value.Year() <= 9999 &&
		value.Nanosecond()%int(time.Microsecond) == 0
}

func collectionTargetLockKey(input targetCreate) string {
	return "collection-target:summary:" + string(input.platform) + ":" +
		strconv.FormatInt(input.appID, 10) + ":" + string(input.side)
}

func sameTargetCreate(target collection.Target, input targetCreate) bool {
	if target.TaskType() != collection.TaskTypeSummary || target.Platform() != input.platform || target.Desired() != input.desired {
		return false
	}
	appID, hasApp := target.AppID()
	side, hasSide := target.Side()
	return hasApp && hasSide && appID == input.appID && side == input.side
}

func validTargetSuccessor(current, next collection.Target) bool {
	if current.ID() != next.ID() || current.TaskType() != next.TaskType() || current.Platform() != next.Platform() ||
		next.Revision() != current.Revision()+1 || next.ChangedAt().Before(current.ChangedAt()) {
		return false
	}
	currentApp, currentHasApp := current.AppID()
	nextApp, nextHasApp := next.AppID()
	currentSide, currentHasSide := current.Side()
	nextSide, nextHasSide := next.Side()
	if currentHasApp != nextHasApp || currentApp != nextApp || currentHasSide != nextHasSide || currentSide != nextSide {
		return false
	}
	if current.Desired() != next.Desired() {
		return current.SwitchVersion() != collection.Revision(math.MaxInt64) && next.SwitchVersion() == current.SwitchVersion()+1
	}
	return next.SwitchVersion() == current.SwitchVersion()
}

func sameTargetState(left, right collection.Target) bool {
	if left.ID() != right.ID() || left.Revision() != right.Revision() || left.TaskType() != right.TaskType() ||
		left.Platform() != right.Platform() || left.Desired() != right.Desired() || left.Actual() != right.Actual() ||
		left.SwitchVersion() != right.SwitchVersion() || left.Reason() != right.Reason() || left.Recovery() != right.Recovery() ||
		!left.ChangedAt().Equal(right.ChangedAt()) {
		return false
	}
	leftApp, leftHasApp := left.AppID()
	rightApp, rightHasApp := right.AppID()
	leftSide, leftHasSide := left.Side()
	rightSide, rightHasSide := right.Side()
	leftRecheck, leftHasRecheck := left.RecheckAt()
	rightRecheck, rightHasRecheck := right.RecheckAt()
	return leftHasApp == rightHasApp && leftApp == rightApp && leftHasSide == rightHasSide && leftSide == rightSide &&
		leftHasRecheck == rightHasRecheck && leftRecheck.Equal(rightRecheck)
}

func runMatchesTarget(run collection.Run, target collection.Target) bool {
	targetID, hasTarget := run.TargetID()
	if !hasTarget || targetID != target.ID() || run.TaskType() != target.TaskType() || run.Platform() != target.Platform() {
		return false
	}
	if target.TaskType() != collection.TaskTypeSummary {
		return false
	}
	targetApp, hasTargetApp := target.AppID()
	targetSide, hasTargetSide := target.Side()
	runSide, hasRunSide := run.Side()
	return hasTargetApp && hasTargetSide && hasRunSide && run.AppID() == targetApp && runSide == targetSide
}

func sameRun(left, right collection.Run) bool {
	if left.ID() != right.ID() || left.TaskType() != right.TaskType() || left.Platform() != right.Platform() ||
		left.AppID() != right.AppID() || left.RunSequence() != right.RunSequence() || left.State() != right.State() ||
		left.Completeness() != right.Completeness() || left.Reason() != right.Reason() ||
		!left.CurrentCursor().Equal(right.CurrentCursor()) || left.LastPageSequence() != right.LastPageSequence() ||
		!left.CreatedAt().Equal(right.CreatedAt()) {
		return false
	}
	leftTarget, leftHasTarget := left.TargetID()
	rightTarget, rightHasTarget := right.TargetID()
	leftSide, leftHasSide := left.Side()
	rightSide, rightHasSide := right.Side()
	leftProduct, leftHasProduct := left.ProductID()
	rightProduct, rightHasProduct := right.ProductID()
	leftSwitch, leftHasSwitch := left.SwitchVersion()
	rightSwitch, rightHasSwitch := right.SwitchVersion()
	leftStarted, leftHasStarted := left.StartedAt()
	rightStarted, rightHasStarted := right.StartedAt()
	leftFinished, leftHasFinished := left.FinishedAt()
	rightFinished, rightHasFinished := right.FinishedAt()
	return leftHasTarget == rightHasTarget && leftTarget == rightTarget && leftHasSide == rightHasSide && leftSide == rightSide &&
		leftHasProduct == rightHasProduct && leftProduct == rightProduct && leftHasSwitch == rightHasSwitch && leftSwitch == rightSwitch &&
		leftHasStarted == rightHasStarted && leftStarted.Equal(rightStarted) && leftHasFinished == rightHasFinished && leftFinished.Equal(rightFinished)
}

func targetRecheckValue(target collection.Target) any {
	value, present := target.RecheckAt()
	if !present {
		return nil
	}
	return value
}

func targetPeriodValue(target collection.Target) any {
	return nil
}

func runStartedValue(run collection.Run) any {
	value, present := run.StartedAt()
	if !present {
		return nil
	}
	return value
}

func runFinishedValue(run collection.Run) any {
	value, present := run.FinishedAt()
	if !present {
		return nil
	}
	return value
}

func nullableCompleteness(value collection.Completeness) any {
	if value == collection.CompletenessNone {
		return nil
	}
	return string(value)
}

func collectionCursorBytes(cursor collection.Cursor) []byte {
	value := cursor.Bytes()
	if value == nil {
		return []byte{}
	}
	return value
}

type collectionTargetScan struct {
	id              int64
	kind            string
	platform        string
	appID           sql.NullInt64
	side            sql.NullString
	desired         string
	actual          string
	reason          string
	recovery        string
	recheckAt       sql.NullTime
	periodMicros    sql.NullInt64
	revision        int64
	switchVersion   int64
	nextRunSequence int64
	changedAt       time.Time
}

func (data *collectionTargetScan) destinations() []any {
	return []any{
		&data.id, &data.kind, &data.platform, &data.appID, &data.side, &data.desired, &data.actual,
		&data.reason, &data.recovery, &data.recheckAt, &data.periodMicros, &data.revision,
		&data.switchVersion, &data.nextRunSequence, &data.changedAt,
	}
}

func scanCollectionTarget(scanner rowScanner) (collection.Target, int64, error) {
	var data collectionTargetScan
	if err := scanner.Scan(data.destinations()...); err != nil {
		return collection.Target{}, 0, err
	}
	if data.nextRunSequence < 1 {
		return collection.Target{}, 0, ErrCollectionIntegrity
	}
	changedAt := normalizePostgresTime(data.changedAt)
	var recheckAt *time.Time
	if data.recheckAt.Valid {
		value := normalizePostgresTime(data.recheckAt.Time)
		recheckAt = &value
	}
	commonDesired := collection.DesiredState(data.desired)
	commonActual := collection.ActualState(data.actual)
	commonReason := collection.TargetReason(data.reason)
	commonRecovery := collection.RecoveryMode(data.recovery)
	var target collection.Target
	var err error
	switch collection.TaskType(data.kind) {
	case collection.TaskTypeSummary:
		if !data.appID.Valid || data.appID.Int64 < 1 || !data.side.Valid || data.periodMicros.Valid {
			return collection.Target{}, 0, ErrCollectionIntegrity
		}
		target, err = collection.NewSummaryTarget(collection.SummaryTargetInput{
			ID: collection.TargetID(data.id), Revision: collection.Revision(data.revision),
			Platform: collection.Platform(data.platform), AppID: data.appID.Int64, Side: market.Side(data.side.String),
			Desired: commonDesired, Actual: commonActual, SwitchVersion: collection.Revision(data.switchVersion),
			Reason: commonReason, Recovery: commonRecovery, RecheckAt: recheckAt, ChangedAt: changedAt,
		})
	default:
		return collection.Target{}, 0, ErrCollectionIntegrity
	}
	if err != nil || target.Platform() != collection.Platform(data.platform) {
		return collection.Target{}, 0, ErrCollectionIntegrity
	}
	return target, data.nextRunSequence, nil
}

func queryCollectionTarget(ctx context.Context, tx *sql.Tx, query string, args ...any) (collection.Target, int64, bool, error) {
	target, nextSequence, err := scanCollectionTarget(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Target{}, 0, false, nil
	}
	if err != nil {
		return collection.Target{}, 0, false, mapCollectionReadError(ctx, err)
	}
	return target, nextSequence, true, nil
}

type collectionRunScan struct {
	id               int64
	targetID         sql.NullInt64
	kind             string
	platform         string
	appID            int64
	side             sql.NullString
	productID        sql.NullInt64
	switchVersion    sql.NullInt64
	runSequence      int64
	state            string
	completeness     sql.NullString
	reason           string
	currentCursor    []byte
	lastPageSequence int64
	createdAt        time.Time
	startedAt        sql.NullTime
	finishedAt       sql.NullTime
}

func (data *collectionRunScan) destinations() []any {
	return []any{
		&data.id, &data.targetID, &data.kind, &data.platform, &data.appID, &data.side,
		&data.productID, &data.switchVersion, &data.runSequence, &data.state, &data.completeness,
		&data.reason, &data.currentCursor, &data.lastPageSequence, &data.createdAt, &data.startedAt, &data.finishedAt,
	}
}

func scanCollectionRun(scanner rowScanner) (collection.Run, error) {
	var data collectionRunScan
	if err := scanner.Scan(data.destinations()...); err != nil {
		return collection.Run{}, err
	}
	cursor, err := collection.NewCursor(data.currentCursor)
	if err != nil {
		return collection.Run{}, ErrCollectionIntegrity
	}
	createdAt := normalizePostgresTime(data.createdAt)
	startedAt := normalizedNullTime(data.startedAt)
	finishedAt := normalizedNullTime(data.finishedAt)
	completeness := collection.CompletenessNone
	if data.completeness.Valid {
		completeness = collection.Completeness(data.completeness.String)
	}
	commonState := collection.RunState(data.state)
	commonReason := collection.RunReason(data.reason)
	var run collection.Run
	switch collection.TaskType(data.kind) {
	case collection.TaskTypeSummary:
		if !data.targetID.Valid || !data.side.Valid || data.productID.Valid || !data.switchVersion.Valid {
			return collection.Run{}, ErrCollectionIntegrity
		}
		run, err = collection.NewSummaryRun(collection.SummaryRunInput{
			ID: collection.RunID(data.id), TargetID: collection.TargetID(data.targetID.Int64),
			Platform: collection.Platform(data.platform), AppID: data.appID, Side: market.Side(data.side.String),
			SwitchVersion: collection.Revision(data.switchVersion.Int64), RunSequence: collection.Sequence(data.runSequence),
			State: commonState, Completeness: completeness, Reason: commonReason, CurrentCursor: cursor,
			LastPageSequence: data.lastPageSequence, CreatedAt: createdAt, StartedAt: startedAt, FinishedAt: finishedAt,
		})
	case collection.TaskTypeDetail:
		if data.targetID.Valid || !data.side.Valid || !data.productID.Valid || data.switchVersion.Valid {
			return collection.Run{}, ErrCollectionIntegrity
		}
		run, err = collection.NewDetailRun(collection.DetailRunInput{
			ID: collection.RunID(data.id), Platform: collection.Platform(data.platform), AppID: data.appID,
			Side: market.Side(data.side.String), ProductID: catalog.ProductID(data.productID.Int64),
			RunSequence: collection.Sequence(data.runSequence), State: commonState, Completeness: completeness,
			Reason: commonReason, CurrentCursor: cursor, LastPageSequence: data.lastPageSequence,
			CreatedAt: createdAt, StartedAt: startedAt, FinishedAt: finishedAt,
		})
	default:
		return collection.Run{}, ErrCollectionIntegrity
	}
	if err != nil || run.Platform() != collection.Platform(data.platform) {
		return collection.Run{}, ErrCollectionIntegrity
	}
	return run, nil
}

func scanCollectionPage(scanner rowScanner) (collection.Page, error) {
	var runID, sequence int64
	var beforeBytes, afterBytes, digestBytes []byte
	var collectedAt, committedAt time.Time
	if err := scanner.Scan(&runID, &sequence, &beforeBytes, &afterBytes, &digestBytes, &collectedAt, &committedAt); err != nil {
		return collection.Page{}, err
	}
	if len(digestBytes) != 32 {
		return collection.Page{}, ErrCollectionIntegrity
	}
	before, err := collection.NewCursor(beforeBytes)
	if err != nil {
		return collection.Page{}, ErrCollectionIntegrity
	}
	after, err := collection.NewCursor(afterBytes)
	if err != nil {
		return collection.Page{}, ErrCollectionIntegrity
	}
	var digest [32]byte
	copy(digest[:], digestBytes)
	page, err := collection.NewPage(collection.PageInput{
		RunID: collection.RunID(runID), PageSequence: collection.Sequence(sequence),
		CursorBefore: before, CursorAfter: after, PayloadDigest: digest,
		CollectedAt: normalizePostgresTime(collectedAt), CommittedAt: normalizePostgresTime(committedAt),
	})
	if err != nil {
		return collection.Page{}, ErrCollectionIntegrity
	}
	return page, nil
}

func normalizedNullTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	normalized := normalizePostgresTime(value.Time)
	return &normalized
}
