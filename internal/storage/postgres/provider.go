package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"buff-go/internal/resource"
)

const providerReadColumns = `
provider_id, name, enabled, priority, revision,
octet_length(credential_plaintext) > 0`

// ListWatermarks returns the three regional short-pool minimums.
// 当前短效可用为 0：节点还没有 short 来源，拉号未接入。
func (s *Store) ListWatermarks(ctx context.Context) ([]resource.RegionWatermark, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT region, min_usable, revision
FROM short_pool_watermarks
ORDER BY CASE region
    WHEN 'domestic' THEN 1
    WHEN 'foreign' THEN 2
    ELSE 3
END`)
	if err != nil {
		return nil, ErrResourceStorage
	}
	defer rows.Close()
	out := make([]resource.RegionWatermark, 0, 3)
	for rows.Next() {
		var mark resource.RegionWatermark
		var region string
		if err := rows.Scan(&region, &mark.MinUsable, &mark.Revision); err != nil {
			return nil, mapResourceReadError(err)
		}
		mark.Region = resource.NodeRegion(region)
		if err := mark.Validate(); err != nil {
			return nil, ErrResourceIntegrity
		}
		out = append(out, mark)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrResourceStorage
	}
	if len(out) != 3 {
		return nil, ErrResourceIntegrity
	}
	return out, nil
}

// SetWatermark updates one region's minimum usable count.
func (s *Store) SetWatermark(ctx context.Context, region resource.NodeRegion, expectedRevision int64, minUsable int) (resource.RegionWatermark, error) {
	if err := s.validate(); err != nil {
		return resource.RegionWatermark{}, err
	}
	mark := resource.RegionWatermark{Region: region, MinUsable: minUsable, Revision: 1}
	if err := mark.Validate(); err != nil {
		return resource.RegionWatermark{}, fmt.Errorf("%w: %s", ErrInvalidResource, err.Error())
	}
	if expectedRevision < 1 {
		return resource.RegionWatermark{}, fmt.Errorf("%w: revision must be at least 1", ErrInvalidResource)
	}
	var stored resource.RegionWatermark
	var storedRegion string
	err := s.db.QueryRowContext(ctx, `
UPDATE short_pool_watermarks
SET min_usable = $3, revision = revision + 1
WHERE region = $1 AND revision = $2
RETURNING region, min_usable, revision`,
		string(region), expectedRevision, minUsable,
	).Scan(&storedRegion, &stored.MinUsable, &stored.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if scanErr := s.db.QueryRowContext(ctx, `SELECT TRUE FROM short_pool_watermarks WHERE region = $1`, string(region)).Scan(&exists); errors.Is(scanErr, sql.ErrNoRows) {
			return resource.RegionWatermark{}, ErrResourceNotFound
		} else if scanErr != nil {
			return resource.RegionWatermark{}, ErrResourceStorage
		}
		return resource.RegionWatermark{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.RegionWatermark{}, ErrResourceStorage
	}
	stored.Region = resource.NodeRegion(storedRegion)
	if err := stored.Validate(); err != nil {
		return resource.RegionWatermark{}, ErrResourceIntegrity
	}
	return stored, nil
}

// ListProviders returns credential-safe vendors in priority order.
func (s *Store) ListProviders(ctx context.Context) ([]resource.ProxyProvider, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+providerReadColumns+`
FROM proxy_providers
ORDER BY priority, provider_id`)
	if err != nil {
		return nil, ErrResourceStorage
	}
	defer rows.Close()
	providers := make([]resource.ProxyProvider, 0)
	for rows.Next() {
		provider, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrResourceStorage
	}
	if err := s.attachProviderRegions(ctx, providers); err != nil {
		return nil, err
	}
	for _, provider := range providers {
		if err := provider.Validate(); err != nil {
			return nil, ErrResourceIntegrity
		}
	}
	return providers, nil
}

// CreateProvider inserts one vendor and its regions.
func (s *Store) CreateProvider(ctx context.Context, name string, enabled bool, priority int, regions []resource.NodeRegion, credential []byte) (resource.ProxyProvider, error) {
	if err := s.validate(); err != nil {
		return resource.ProxyProvider{}, err
	}
	if err := validateProviderWrite(name, priority, regions, credential); err != nil {
		return resource.ProxyProvider{}, err
	}
	id, err := s.nextIdentity(ctx, "proxy_providers", "provider_id")
	if err != nil {
		return resource.ProxyProvider{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return resource.ProxyProvider{}, ErrResourceStorage
	}
	defer func() { _ = tx.Rollback() }()
	provider, err := scanProvider(tx.QueryRowContext(ctx, `
INSERT INTO proxy_providers (provider_id, name, enabled, priority, credential_plaintext)
OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5)
RETURNING `+providerReadColumns,
		id, name, enabled, priority, credential,
	))
	if err != nil {
		return resource.ProxyProvider{}, err
	}
	if err := replaceProviderRegions(ctx, tx, provider.ID, regions); err != nil {
		return resource.ProxyProvider{}, err
	}
	if err := tx.Commit(); err != nil {
		return resource.ProxyProvider{}, ErrResourceStorage
	}
	provider.Regions = append([]resource.NodeRegion(nil), regions...)
	if err := provider.Validate(); err != nil {
		return resource.ProxyProvider{}, ErrResourceIntegrity
	}
	return provider, nil
}

// UpdateProvider replaces name, enabled, priority, and regions.
func (s *Store) UpdateProvider(ctx context.Context, id resource.ProviderID, expectedRevision int64, name string, enabled bool, priority int, regions []resource.NodeRegion) (resource.ProxyProvider, error) {
	if err := s.validate(); err != nil {
		return resource.ProxyProvider{}, err
	}
	if err := id.Validate(); err != nil {
		return resource.ProxyProvider{}, fmt.Errorf("%w: %s", ErrInvalidResource, err.Error())
	}
	if expectedRevision < 1 {
		return resource.ProxyProvider{}, fmt.Errorf("%w: revision must be at least 1", ErrInvalidResource)
	}
	if err := validateProviderWrite(name, priority, regions, []byte("x")); err != nil {
		return resource.ProxyProvider{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return resource.ProxyProvider{}, ErrResourceStorage
	}
	defer func() { _ = tx.Rollback() }()
	provider, err := scanProvider(tx.QueryRowContext(ctx, `
UPDATE proxy_providers
SET name = $3, enabled = $4, priority = $5, revision = revision + 1
WHERE provider_id = $1 AND revision = $2
RETURNING `+providerReadColumns,
		int64(id), expectedRevision, name, enabled, priority,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.ProxyProvider{}, providerMissingOrConflict(ctx, tx, id)
	}
	if err != nil {
		return resource.ProxyProvider{}, err
	}
	if err := replaceProviderRegions(ctx, tx, provider.ID, regions); err != nil {
		return resource.ProxyProvider{}, err
	}
	if err := tx.Commit(); err != nil {
		return resource.ProxyProvider{}, ErrResourceStorage
	}
	provider.Regions = append([]resource.NodeRegion(nil), regions...)
	if err := provider.Validate(); err != nil {
		return resource.ProxyProvider{}, ErrResourceIntegrity
	}
	return provider, nil
}

// ReplaceProviderCredential replaces the write-only vendor credential.
func (s *Store) ReplaceProviderCredential(ctx context.Context, id resource.ProviderID, expectedRevision int64, credential []byte) (resource.ProxyProvider, error) {
	if err := s.validate(); err != nil {
		return resource.ProxyProvider{}, err
	}
	if err := id.Validate(); err != nil {
		return resource.ProxyProvider{}, fmt.Errorf("%w: %s", ErrInvalidResource, err.Error())
	}
	if expectedRevision < 1 {
		return resource.ProxyProvider{}, fmt.Errorf("%w: revision must be at least 1", ErrInvalidResource)
	}
	if len(credential) == 0 {
		return resource.ProxyProvider{}, fmt.Errorf("%w: credential is empty", ErrInvalidResource)
	}
	provider, err := scanProvider(s.db.QueryRowContext(ctx, `
UPDATE proxy_providers
SET credential_plaintext = $3, revision = revision + 1
WHERE provider_id = $1 AND revision = $2
RETURNING `+providerReadColumns,
		int64(id), expectedRevision, credential,
	))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if scanErr := s.db.QueryRowContext(ctx, `SELECT TRUE FROM proxy_providers WHERE provider_id = $1`, int64(id)).Scan(&exists); errors.Is(scanErr, sql.ErrNoRows) {
			return resource.ProxyProvider{}, ErrResourceNotFound
		} else if scanErr != nil {
			return resource.ProxyProvider{}, ErrResourceStorage
		}
		return resource.ProxyProvider{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.ProxyProvider{}, ErrResourceStorage
	}
	providers := []resource.ProxyProvider{provider}
	if err := s.attachProviderRegions(ctx, providers); err != nil {
		return resource.ProxyProvider{}, err
	}
	provider = providers[0]
	if err := provider.Validate(); err != nil {
		return resource.ProxyProvider{}, ErrResourceIntegrity
	}
	return provider, nil
}

// DeleteProvider removes one vendor.
func (s *Store) DeleteProvider(ctx context.Context, id resource.ProviderID) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidResource, err.Error())
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM proxy_providers WHERE provider_id = $1`, int64(id))
	if err != nil {
		return ErrResourceStorage
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ErrResourceStorage
	}
	if affected == 0 {
		return ErrResourceNotFound
	}
	return nil
}

func validateProviderWrite(name string, priority int, regions []resource.NodeRegion, credential []byte) error {
	probe := resource.ProxyProvider{
		ID: 1, Name: name, Priority: priority, Regions: regions,
		HasCredential: len(credential) > 0, Revision: 1,
	}
	if err := probe.Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidResource, err.Error())
	}
	return nil
}

func replaceProviderRegions(ctx context.Context, tx *sql.Tx, id resource.ProviderID, regions []resource.NodeRegion) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM proxy_provider_regions WHERE provider_id = $1`, int64(id)); err != nil {
		return ErrResourceStorage
	}
	for _, region := range regions {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO proxy_provider_regions (provider_id, region) VALUES ($1, $2)`,
			int64(id), string(region)); err != nil {
			return ErrResourceStorage
		}
	}
	return nil
}

func (s *Store) attachProviderRegions(ctx context.Context, providers []resource.ProxyProvider) error {
	if len(providers) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT provider_id, region FROM proxy_provider_regions ORDER BY provider_id, region`)
	if err != nil {
		return ErrResourceStorage
	}
	defer rows.Close()
	byID := make(map[resource.ProviderID][]resource.NodeRegion, len(providers))
	for rows.Next() {
		var id int64
		var region string
		if err := rows.Scan(&id, &region); err != nil {
			return mapResourceReadError(err)
		}
		pid := resource.ProviderID(id)
		byID[pid] = append(byID[pid], resource.NodeRegion(region))
	}
	if err := rows.Err(); err != nil {
		return ErrResourceStorage
	}
	for i := range providers {
		providers[i].Regions = append([]resource.NodeRegion(nil), byID[providers[i].ID]...)
	}
	return nil
}

func providerMissingOrConflict(ctx context.Context, tx *sql.Tx, id resource.ProviderID) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT TRUE FROM proxy_providers WHERE provider_id = $1`, int64(id)).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrResourceNotFound
	} else if err != nil {
		return ErrResourceStorage
	}
	return ErrResourceRevisionConflict
}

func scanProvider(row rowScanner) (resource.ProxyProvider, error) {
	var provider resource.ProxyProvider
	var id int64
	if err := row.Scan(&id, &provider.Name, &provider.Enabled, &provider.Priority, &provider.Revision, &provider.HasCredential); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resource.ProxyProvider{}, err
		}
		return resource.ProxyProvider{}, mapProviderWriteError(err)
	}
	provider.ID = resource.ProviderID(id)
	return provider, nil
}

func mapProviderWriteError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if postgresErrorCode(err) == "23505" {
		return ErrProviderConflict
	}
	return ErrResourceStorage
}
