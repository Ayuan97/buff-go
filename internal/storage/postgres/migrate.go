// Package postgres contains PostgreSQL persistence for stable catalog and market facts.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	migrationPattern = "migrations/*.sql"
	// Hex encodes "buffgo" and gives this package one stable transaction lock.
	migrationLockKey int64 = 0x62756666676f
)

const storageMigrationsTableSQL = `
CREATE TABLE IF NOT EXISTS buffgo_storage_migrations (
    version    TEXT PRIMARY KEY CHECK (version <> ''),
    checksum   TEXT NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW() CHECK (isfinite(applied_at))
)`

var migrationFilename = regexp.MustCompile(`^[0-9]{6}_[a-z0-9]+(?:_[a-z0-9]+)*\.sql$`)

// ErrMigrationChecksumDrift reports that an applied migration was edited.
var ErrMigrationChecksumDrift = errors.New("storage migration checksum drift")

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

type migration struct {
	Version  string
	Checksum string
	SQL      string
}

// ApplyMigrations applies every embedded migration in filename order.
// Each version is serialized and committed in its own transaction.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if ctx == nil {
		return fmt.Errorf("migration context is required")
	}
	if db == nil {
		return fmt.Errorf("migration database is required")
	}
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, current := range migrations {
		if err := applyMigration(ctx, db, current); err != nil {
			return err
		}
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	return loadMigrationsFrom(embeddedMigrations)
}

func loadMigrationsFrom(source fs.FS) ([]migration, error) {
	paths, err := fs.Glob(source, migrationPattern)
	if err != nil {
		return nil, fmt.Errorf("list storage migrations: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no embedded storage migrations")
	}
	sort.Strings(paths)

	seen := make(map[string]struct{}, len(paths))
	migrations := make([]migration, 0, len(paths))
	for _, filename := range paths {
		base := path.Base(filename)
		if !migrationFilename.MatchString(base) {
			return nil, fmt.Errorf("invalid storage migration filename %q", base)
		}
		version := strings.TrimSuffix(base, path.Ext(base))
		if _, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate storage migration version %q", version)
		}
		data, err := fs.ReadFile(source, filename)
		if err != nil {
			return nil, fmt.Errorf("read storage migration %s: %w", version, err)
		}
		if strings.TrimSpace(string(data)) == "" {
			return nil, fmt.Errorf("storage migration %s is empty", version)
		}
		sum := sha256.Sum256(data)
		migrations = append(migrations, migration{
			Version:  version,
			Checksum: hex.EncodeToString(sum[:]),
			SQL:      string(data),
		})
		seen[version] = struct{}{}
	}
	return migrations, nil
}

func applyMigration(ctx context.Context, db *sql.DB, current migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin storage migration %s: %w", current.Version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("lock storage migration %s: %w", current.Version, err)
	}
	if _, err := tx.ExecContext(ctx, storageMigrationsTableSQL); err != nil {
		return fmt.Errorf("create storage migration metadata: %w", err)
	}

	var storedChecksum string
	err = tx.QueryRowContext(ctx,
		`SELECT checksum FROM buffgo_storage_migrations WHERE version = $1`,
		current.Version,
	).Scan(&storedChecksum)
	switch {
	case err == nil:
		if err := validateStoredChecksum(current, storedChecksum); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit existing storage migration %s: %w", current.Version, err)
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read storage migration %s: %w", current.Version, err)
	}

	if _, err := tx.ExecContext(ctx, current.SQL); err != nil {
		return fmt.Errorf("apply storage migration %s: %w", current.Version, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO buffgo_storage_migrations (version, checksum)
VALUES ($1, $2)`, current.Version, current.Checksum); err != nil {
		return fmt.Errorf("record storage migration %s: %w", current.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit storage migration %s: %w", current.Version, err)
	}
	return nil
}

func validateStoredChecksum(current migration, stored string) error {
	if stored == current.Checksum {
		return nil
	}
	return fmt.Errorf("%w: version %s", ErrMigrationChecksumDrift, current.Version)
}
