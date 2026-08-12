// Package resolve contains temporary adapters around the catalog identity contract.
package resolve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"buff-go/internal/buffgo/source"
	"buff-go/internal/catalog"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ErrLegacyResolverUnavailable prevents the old PostgreSQL identity rules from
// remaining active while the catalog schema is replaced in Goal 4A.
var ErrLegacyResolverUnavailable = errors.New("legacy postgres resolver is unavailable until catalog storage migration")

// Resolver is the quarantined legacy PostgreSQL adapter.
type Resolver struct {
	db *sql.DB
}

// New wraps the legacy database handle. Identity resolution remains disabled.
func New(db *sql.DB) *Resolver {
	return &Resolver{db: db}
}

// Open opens a database handle for callers that still own the legacy lifecycle.
func Open(dsn string) (*Resolver, *sql.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, nil, fmt.Errorf("empty postgres dsn")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(time.Minute)
	return New(db), db, nil
}

// Resolve fails before SQL so the former fuzzy, low-confidence and auto-create
// rules cannot compete with catalog.Match.
func (r *Resolver) Resolve(_ context.Context, _ source.RawOffer) (catalog.MatchResult, error) {
	if r == nil || r.db == nil {
		return catalog.MatchResult{}, fmt.Errorf("nil resolver")
	}
	return catalog.MatchResult{}, ErrLegacyResolverUnavailable
}
