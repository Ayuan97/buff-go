package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/resource"
)

const collectionTaskColumns = `
task_id, target_id, enqueue_seq, enqueued_at, kind, payload, state, claimed_by, claimed_at`

// QueueDepth returns queued+claimed tasks for one direction.
func (s *Store) QueueDepth(ctx context.Context, id collection.TargetID) (int, error) {
	if err := s.validateCollectionStore(); err != nil {
		return 0, err
	}
	if id.Validate() != nil {
		return 0, ErrCollectionInvalidInput
	}
	var depth int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM collection_tasks WHERE target_id = $1`, int64(id)).Scan(&depth); err != nil {
		return 0, collectionStorageError(ctx)
	}
	return depth, nil
}

// EnqueueTasks appends planner tasks and advances the refill cursor.
func (s *Store) EnqueueTasks(
	ctx context.Context,
	id collection.TargetID,
	expectedSwitch collection.Revision,
	specs []collection.EnqueueSpec,
	cursor collection.Cursor,
	total int64,
) error {
	if err := s.validateCollectionStore(); err != nil {
		return err
	}
	if id.Validate() != nil || expectedSwitch.Validate() != nil || cursor.Validate() != nil || total < 0 {
		return ErrCollectionInvalidInput
	}
	if len(specs) == 0 {
		return nil
	}
	for _, spec := range specs {
		if spec.Kind.Validate() != nil || len(spec.Payload) == 0 || !json.Valid(spec.Payload) {
			return ErrCollectionInvalidInput
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	target, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1
FOR UPDATE`, int64(id))
	if err != nil {
		return err
	}
	if !found {
		return ErrCollectionNotFound
	}
	if target.Desired() != collection.DesiredEnabled || target.SwitchVersion() != expectedSwitch {
		return ErrCollectionFence
	}

	var nextSeq int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(enqueue_seq), 0) + 1 FROM collection_tasks WHERE target_id = $1`, int64(id)).Scan(&nextSeq); err != nil {
		return collectionStorageError(ctx)
	}
	for index, spec := range specs {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO collection_tasks (target_id, enqueue_seq, enqueued_at, kind, payload, state)
VALUES ($1, $2, date_trunc('microseconds', clock_timestamp()), $3, $4::jsonb, 'queued')`,
			int64(id), nextSeq+int64(index), string(spec.Kind), spec.Payload); err != nil {
			return mapCollectionWriteError(ctx, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE collection_targets
SET refill_cursor = $2, refill_total = $3
WHERE target_id = $1`, int64(id), collectionCursorBytes(cursor), total); err != nil {
		return mapCollectionWriteError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return collectionStorageError(ctx)
	}
	return nil
}

// ClearTargetQueue drops unfinished tasks for one direction.
func (s *Store) ClearTargetQueue(ctx context.Context, id collection.TargetID) error {
	if err := s.validateCollectionStore(); err != nil {
		return err
	}
	if id.Validate() != nil {
		return ErrCollectionInvalidInput
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM collection_tasks WHERE target_id = $1`, int64(id)); err != nil {
		return collectionStorageError(ctx)
	}
	return nil
}

// ClaimTask takes the earliest queued task this combination can run.
// 同一组合已有认领则不再领。
func (s *Store) ClaimTask(
	ctx context.Context,
	combinationID resource.CombinationID,
	platform collection.Platform,
) (collection.Task, collection.Target, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Task{}, collection.Target{}, false, err
	}
	if combinationID.Validate() != nil || platform.Validate() != nil {
		return collection.Task{}, collection.Target{}, false, ErrCollectionInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Task{}, collection.Target{}, false, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	var busy int64
	err = tx.QueryRowContext(ctx, `
SELECT task_id FROM collection_tasks
WHERE state = 'claimed' AND claimed_by = $1
LIMIT 1`, int64(combinationID)).Scan(&busy)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return collection.Task{}, collection.Target{}, false, collectionStorageError(ctx)
		}
		return collection.Task{}, collection.Target{}, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return collection.Task{}, collection.Target{}, false, collectionStorageError(ctx)
	}

	var taskID int64
	err = tx.QueryRowContext(ctx, `
SELECT t.task_id
FROM collection_tasks t
JOIN collection_targets g ON g.target_id = t.target_id
WHERE t.state = 'queued'
  AND g.desired_state = 'enabled'
  AND g.platform = $1
ORDER BY t.enqueued_at, t.task_id
FOR UPDATE OF t SKIP LOCKED
LIMIT 1`, string(platform)).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return collection.Task{}, collection.Target{}, false, collectionStorageError(ctx)
		}
		return collection.Task{}, collection.Target{}, false, nil
	}
	if err != nil {
		return collection.Task{}, collection.Target{}, false, collectionStorageError(ctx)
	}

	task, err := scanCollectionTask(tx.QueryRowContext(ctx, `
UPDATE collection_tasks
SET state = 'claimed', claimed_by = $2, claimed_at = date_trunc('microseconds', clock_timestamp())
WHERE task_id = $1 AND state = 'queued'
RETURNING `+collectionTaskColumns, taskID, int64(combinationID)))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Task{}, collection.Target{}, false, ErrCollectionConflict
	}
	if err != nil {
		if postgresErrorCode(err) == "23505" {
			return collection.Task{}, collection.Target{}, false, nil
		}
		return collection.Task{}, collection.Target{}, false, mapCollectionWriteError(ctx, err)
	}
	target, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1`, int64(task.TargetID()))
	if err != nil {
		return collection.Task{}, collection.Target{}, false, err
	}
	if !found {
		return collection.Task{}, collection.Target{}, false, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return collection.Task{}, collection.Target{}, false, collectionStorageError(ctx)
	}
	return task, target, true, nil
}

// ReleaseStaleClaims returns claimed tasks older than the timeout to queued.
func (s *Store) ReleaseStaleClaims(ctx context.Context, olderThan time.Duration) (int, error) {
	if err := s.validateCollectionStore(); err != nil {
		return 0, err
	}
	if olderThan <= 0 {
		return 0, ErrCollectionInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE collection_tasks
SET state = 'queued', claimed_by = NULL, claimed_at = NULL
WHERE state = 'claimed' AND claimed_at < date_trunc('microseconds', clock_timestamp()) - ($1 * INTERVAL '1 microsecond')`,
		olderThan.Microseconds())
	if err != nil {
		return 0, collectionStorageError(ctx)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, collectionStorageError(ctx)
	}
	return int(n), nil
}

// ReleaseAllClaims returns every claimed task to queued. 启动恢复用：进程里已经没有活租约。
func (s *Store) ReleaseAllClaims(ctx context.Context) (int, error) {
	if err := s.validateCollectionStore(); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE collection_tasks
SET state = 'queued', claimed_by = NULL, claimed_at = NULL
WHERE state = 'claimed'`)
	if err != nil {
		return 0, collectionStorageError(ctx)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, collectionStorageError(ctx)
	}
	return int(n), nil
}

// CompleteTask deletes a claimed task after a successful commit.
func (s *Store) CompleteTask(ctx context.Context, id collection.TaskID, combinationID resource.CombinationID) error {
	if err := s.validateCollectionStore(); err != nil {
		return err
	}
	if id.Validate() != nil || combinationID.Validate() != nil {
		return ErrCollectionInvalidInput
	}
	var deleted int64
	err := s.db.QueryRowContext(ctx, `
DELETE FROM collection_tasks
WHERE task_id = $1 AND state = 'claimed' AND claimed_by = $2
RETURNING task_id`, int64(id), int64(combinationID)).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCollectionConflict
	}
	if err != nil {
		return collectionStorageError(ctx)
	}
	return nil
}

// RequeueTask returns one claimed task to the queue without changing enqueue_seq.
func (s *Store) RequeueTask(ctx context.Context, id collection.TaskID, combinationID resource.CombinationID) error {
	if err := s.validateCollectionStore(); err != nil {
		return err
	}
	if id.Validate() != nil || combinationID.Validate() != nil {
		return ErrCollectionInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE collection_tasks
SET state = 'queued', claimed_by = NULL, claimed_at = NULL
WHERE task_id = $1 AND state = 'claimed' AND claimed_by = $2`, int64(id), int64(combinationID))
	if err != nil {
		return collectionStorageError(ctx)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return collectionStorageError(ctx)
	}
	if n == 0 {
		return nil
	}
	return nil
}

func scanCollectionTask(scanner rowScanner) (collection.Task, error) {
	var (
		id, targetID, enqueueSeq int64
		enqueuedAt               time.Time
		kind, state              string
		payload                  []byte
		claimedBy                sql.NullInt64
		claimedAt                sql.NullTime
	)
	if err := scanner.Scan(&id, &targetID, &enqueueSeq, &enqueuedAt, &kind, &payload, &state, &claimedBy, &claimedAt); err != nil {
		return collection.Task{}, err
	}
	input := collection.TaskInput{
		ID:         collection.TaskID(id),
		TargetID:   collection.TargetID(targetID),
		EnqueueSeq: enqueueSeq,
		Kind:       collection.TaskKind(kind),
		Payload:    payload,
		State:      collection.TaskState(state),
		EnqueuedAt: normalizePostgresTime(enqueuedAt),
	}
	if claimedBy.Valid {
		value := resource.CombinationID(claimedBy.Int64)
		input.ClaimedBy = &value
	}
	if claimedAt.Valid {
		value := normalizePostgresTime(claimedAt.Time)
		input.ClaimedAt = &value
	}
	task, err := collection.NewTask(input)
	if err != nil {
		return collection.Task{}, ErrCollectionIntegrity
	}
	return task, nil
}
