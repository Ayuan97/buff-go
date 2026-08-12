package migrate

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schemaSQL string

//go:embed patches_002.sql
var patches002SQL string

const (
	migrationV001 = "001_core"
	migrationV002 = "002_data_model"
)

// SchemaSQL returns the embedded base schema for inspection/tests.
func SchemaSQL() string {
	return schemaSQL
}

// MigrationVersion is the latest applied migration id.
func MigrationVersion() string {
	return migrationV002
}

// Apply opens dsn and applies embedded migrations.
func Apply(ctx context.Context, dsn string) error {
	if dsn == "" {
		return fmt.Errorf("empty postgres dsn")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	return ApplyDB(ctx, db)
}

// ApplyDB runs schema + additive patches on an open *sql.DB.
func ApplyDB(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("nil db")
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}

	has002, err := hasVersion(ctx, db, migrationV002)
	if err != nil {
		return err
	}
	if has002 {
		return nil
	}

	has001, err := hasVersion(ctx, db, migrationV001)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if !has001 {
		// Fresh install: full schema (includes L1–L5).
		if err := execStatements(tx, schemaSQL, "schema"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`,
			migrationV001,
		); err != nil {
			return fmt.Errorf("record %s: %w", migrationV001, err)
		}
	}

	// Upgrade path (and no-op extras on fresh): additive tables/columns.
	if err := execStatements(tx, patches002SQL, "patches_002"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`,
		migrationV002,
	); err != nil {
		return fmt.Errorf("record %s: %w", migrationV002, err)
	}
	return tx.Commit()
}

func hasVersion(ctx context.Context, db *sql.DB, version string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`,
		version,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check migration %s: %w", version, err)
	}
	return exists, nil
}

func execStatements(tx *sql.Tx, sqlText, label string) error {
	for i, stmt := range splitSQL(sqlText) {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%s statement %d: %w\n---\n%s", label, i+1, err, truncate(stmt, 200))
		}
	}
	return nil
}

// RequiredTables lists tables the schema must create (for structural tests).
func RequiredTables() []string {
	return []string{
		"games", "items", "platform_items", "quotes",
		"platform_listings", "order_book_levels", "price_points", "trade_events",
		"accounts", "proxies", "schema_migrations",
	}
}

// SchemaDefinesTable reports whether embedded SQL creates the table.
func SchemaDefinesTable(name string) bool {
	return strings.Contains(schemaSQL, "CREATE TABLE IF NOT EXISTS "+name) ||
		strings.Contains(schemaSQL, "CREATE TABLE IF NOT EXISTS "+name+" ") ||
		strings.Contains(patches002SQL, "CREATE TABLE IF NOT EXISTS "+name)
}

func splitSQL(s string) []string {
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		lines := strings.Split(p, "\n")
		var kept []string
		for _, line := range lines {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "--") {
				continue
			}
			kept = append(kept, line)
		}
		stmt := strings.TrimSpace(strings.Join(kept, "\n"))
		if stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
