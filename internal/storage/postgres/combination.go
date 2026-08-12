package postgres

import (
	"context"
	"database/sql"
	"errors"

	"buff-go/internal/resource"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrCombinationIncompatible = errors.New("account and node are not compatible")
	ErrCombinationConflict     = errors.New("account-node combination conflicts with stored state")
	ErrResourceDependency      = errors.New("resource has configured combinations")
)

var _ resource.CoordinatorRepository = (*Store)(nil)

const combinationReadColumns = `combination_id, platform, account_id, node_id`

const combinationResourceReadColumns = `
c.combination_id, c.platform, c.account_id, c.node_id,
a.account_id, a.platform, a.alias, a.session_state, a.session_revision, a.last_checked_at,
n.node_id, n.name, n.kind, n.region, n.egress_mode, n.state, n.egress_revision,
n.assignment_revision, n.assigned_platform, (n.proxy_ciphertext IS NOT NULL), n.sticky_session_valid_until,
n.exit_verified_revision, host(n.exit_address), n.exit_verified_at, n.exit_valid_until`

// CreateCombination records one explicit account-node pairing. The platform is
// derived from the two resources and never accepted from the caller.
func (s *Store) CreateCombination(ctx context.Context, accountID resource.AccountID, nodeID resource.NodeID) (resource.AccountNodeCombination, error) {
	if err := s.validate(); err != nil {
		return resource.AccountNodeCombination{}, err
	}
	if err := accountID.Validate(); err != nil {
		return resource.AccountNodeCombination{}, err
	}
	if err := nodeID.Validate(); err != nil {
		return resource.AccountNodeCombination{}, err
	}

	combination, err := scanCombination(s.db.QueryRowContext(ctx, `
INSERT INTO account_node_combinations (platform, account_id, node_id)
SELECT account.platform, account.account_id, node.node_id
FROM platform_accounts account
JOIN access_nodes node ON node.assigned_platform = account.platform
WHERE account.account_id = $1 AND node.node_id = $2
ON CONFLICT (account_id, node_id) DO NOTHING
RETURNING `+combinationReadColumns, int64(accountID), int64(nodeID)))
	if err == nil {
		return combination, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		if errors.Is(err, ErrResourceIntegrity) {
			return resource.AccountNodeCombination{}, ErrResourceIntegrity
		}
		return resource.AccountNodeCombination{}, mapCombinationWriteError(err)
	}

	combination, err = scanCombination(s.db.QueryRowContext(ctx, `
SELECT `+combinationReadColumns+`
FROM account_node_combinations
WHERE account_id = $1 AND node_id = $2`, int64(accountID), int64(nodeID)))
	if err == nil {
		return combination, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		if errors.Is(err, ErrResourceIntegrity) {
			return resource.AccountNodeCombination{}, ErrResourceIntegrity
		}
		return resource.AccountNodeCombination{}, ErrResourceStorage
	}

	var accountExists, nodeExists bool
	if err := s.db.QueryRowContext(ctx, `
SELECT
    EXISTS (SELECT 1 FROM platform_accounts WHERE account_id = $1),
    EXISTS (SELECT 1 FROM access_nodes WHERE node_id = $2)`, int64(accountID), int64(nodeID)).Scan(&accountExists, &nodeExists); err != nil {
		return resource.AccountNodeCombination{}, ErrResourceStorage
	}
	if !accountExists || !nodeExists {
		return resource.AccountNodeCombination{}, ErrResourceNotFound
	}
	return resource.AccountNodeCombination{}, ErrCombinationIncompatible
}

// Combination returns one explicit pairing.
func (s *Store) Combination(ctx context.Context, id resource.CombinationID) (resource.AccountNodeCombination, bool, error) {
	if err := s.validate(); err != nil {
		return resource.AccountNodeCombination{}, false, err
	}
	if err := id.Validate(); err != nil {
		return resource.AccountNodeCombination{}, false, err
	}
	combination, err := scanCombination(s.db.QueryRowContext(ctx, `
SELECT `+combinationReadColumns+`
FROM account_node_combinations WHERE combination_id = $1`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccountNodeCombination{}, false, nil
	}
	if err != nil {
		if errors.Is(err, ErrResourceIntegrity) {
			return resource.AccountNodeCombination{}, false, ErrResourceIntegrity
		}
		return resource.AccountNodeCombination{}, false, ErrResourceStorage
	}
	return combination, true, nil
}

// ListCombinations returns explicit pairings in stable identity order.
func (s *Store) ListCombinations(ctx context.Context) ([]resource.AccountNodeCombination, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+combinationReadColumns+`
FROM account_node_combinations ORDER BY combination_id`)
	if err != nil {
		return nil, ErrResourceStorage
	}
	defer rows.Close()

	combinations := make([]resource.AccountNodeCombination, 0)
	for rows.Next() {
		combination, err := scanCombination(rows)
		if err != nil {
			return nil, ErrResourceIntegrity
		}
		combinations = append(combinations, combination)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrResourceStorage
	}
	return combinations, nil
}

// CombinationResources reads a pairing and both safe resource models in one
// statement, so all three values belong to one PostgreSQL snapshot.
func (s *Store) CombinationResources(ctx context.Context, id resource.CombinationID) (resource.CombinationResources, bool, error) {
	if err := s.validate(); err != nil {
		return resource.CombinationResources{}, false, err
	}
	if err := id.Validate(); err != nil {
		return resource.CombinationResources{}, false, err
	}

	var combinationData combinationScanData
	var accountData accountScanData
	var nodeData nodeScanData
	destinations := append(combinationData.destinations(), accountData.destinations()...)
	destinations = append(destinations, nodeData.destinations()...)
	err := s.db.QueryRowContext(ctx, `
SELECT `+combinationResourceReadColumns+`
FROM account_node_combinations c
JOIN platform_accounts a ON a.account_id = c.account_id AND a.platform = c.platform
JOIN access_nodes n ON n.node_id = c.node_id AND n.assigned_platform = c.platform
WHERE c.combination_id = $1`, int64(id)).Scan(destinations...)
	if errors.Is(err, sql.ErrNoRows) {
		return resource.CombinationResources{}, false, nil
	}
	if err != nil {
		return resource.CombinationResources{}, false, ErrResourceStorage
	}
	combination, err := combinationData.result()
	if err != nil {
		return resource.CombinationResources{}, false, ErrResourceIntegrity
	}
	account, err := accountData.result()
	if err != nil {
		return resource.CombinationResources{}, false, ErrResourceIntegrity
	}
	node, err := nodeData.result()
	if err != nil {
		return resource.CombinationResources{}, false, ErrResourceIntegrity
	}
	resources := resource.CombinationResources{Combination: combination, Account: account, Node: node}
	if err := resources.Validate(); err != nil {
		return resource.CombinationResources{}, false, ErrResourceIntegrity
	}
	return resources, true, nil
}

// DeleteCombination removes one explicit pairing.
func (s *Store) DeleteCombination(ctx context.Context, id resource.CombinationID) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return err
	}
	var deleted int64
	err := s.db.QueryRowContext(ctx, `
DELETE FROM account_node_combinations WHERE combination_id = $1
RETURNING combination_id`, int64(id)).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrResourceNotFound
	}
	if err != nil {
		return ErrResourceStorage
	}
	if deleted != int64(id) {
		return ErrResourceIntegrity
	}
	return nil
}

// DeleteAccount deletes an account only when no combination references it.
func (s *Store) DeleteAccount(ctx context.Context, id resource.AccountID) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return err
	}
	return s.deleteResource(ctx, `DELETE FROM platform_accounts WHERE account_id = $1 RETURNING account_id`, int64(id))
}

// DeleteNode deletes a node only when no combination references it.
func (s *Store) DeleteNode(ctx context.Context, id resource.NodeID) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return err
	}
	return s.deleteResource(ctx, `DELETE FROM access_nodes WHERE node_id = $1 RETURNING node_id`, int64(id))
}

func (s *Store) deleteResource(ctx context.Context, statement string, id int64) error {
	var deleted int64
	err := s.db.QueryRowContext(ctx, statement, id).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrResourceNotFound
	}
	if err != nil {
		if postgresErrorCode(err) == "23503" {
			return ErrResourceDependency
		}
		return ErrResourceStorage
	}
	if deleted != id {
		return ErrResourceIntegrity
	}
	return nil
}

type combinationScanData struct {
	combination resource.AccountNodeCombination
	id          int64
	accountID   int64
	nodeID      int64
}

func (data *combinationScanData) destinations() []any {
	return []any{&data.id, &data.combination.Platform, &data.accountID, &data.nodeID}
}

func (data *combinationScanData) result() (resource.AccountNodeCombination, error) {
	data.combination.ID = resource.CombinationID(data.id)
	data.combination.AccountID = resource.AccountID(data.accountID)
	data.combination.NodeID = resource.NodeID(data.nodeID)
	if err := data.combination.Validate(); err != nil {
		return resource.AccountNodeCombination{}, ErrResourceIntegrity
	}
	return data.combination, nil
}

func scanCombination(row rowScanner) (resource.AccountNodeCombination, error) {
	var data combinationScanData
	if err := row.Scan(data.destinations()...); err != nil {
		return resource.AccountNodeCombination{}, err
	}
	return data.result()
}

func mapCombinationWriteError(err error) error {
	switch postgresErrorCode(err) {
	case "23503":
		return ErrCombinationIncompatible
	case "23505":
		return ErrCombinationConflict
	default:
		return ErrResourceStorage
	}
}

func mapAssignmentWriteError(err error) error {
	switch postgresErrorCode(err) {
	case "23503":
		return ErrResourceDependency
	case "23505":
		return ErrNodeAssignmentConflict
	default:
		return ErrResourceStorage
	}
}

func postgresErrorCode(err error) string {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.Code
	}
	return ""
}
