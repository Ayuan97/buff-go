// Package postgres persists stable buff-go domain state in PostgreSQL.
package postgres

import (
	"database/sql"
	"fmt"
	"regexp"

	"buff-go/internal/ratelimit"
)

var (
	platformPattern   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,31}$`)
	reasonCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

// Store uses a caller-owned database handle. It never opens, pings, or closes it.
type Store struct {
	db              *sql.DB
	rateLimitSigner *ratelimit.Signer
}

// New creates a PostgreSQL store around a caller-owned database handle.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("nil postgres db")
	}
	signer, err := ratelimit.NewSigner()
	if err != nil {
		return nil, err
	}
	return &Store{db: db, rateLimitSigner: signer}, nil
}

func (s *Store) validate() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("nil postgres store")
	}
	return nil
}

func validateAppID(appid int64) error {
	if appid <= 0 {
		return fmt.Errorf("appid must be positive")
	}
	return nil
}

func validatePlatform(platform string) error {
	if !platformPattern.MatchString(platform) {
		return fmt.Errorf("invalid platform")
	}
	return nil
}
