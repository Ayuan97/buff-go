package postgres

import (
	"context"
	"fmt"
)

// saveObservations retains Goal 4A's low-level storage contract only inside
// tests. Production writes must pass through CommitSummaryPage.
func (s *Store) saveObservations(ctx context.Context, batch observationBatch) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("PostgreSQL store is required")
	}
	snapshots, err := prepareBatch(batch, false)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin market batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	lockKey := fmt.Sprintf("market-batch:%s:%d:%s", batch.Platform, batch.AppID, batch.Side)
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey,
	); err != nil {
		return false, fmt.Errorf("lock market batch: %w", err)
	}
	applied, err := savePreparedObservationsTx(ctx, tx, batch, snapshots)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit market batch: %w", err)
	}
	return applied, nil
}
