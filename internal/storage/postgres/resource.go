package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"buff-go/internal/market"
	"buff-go/internal/resource"
)

var (
	ErrResourceNotFound           = errors.New("resource not found")
	ErrResourceRevisionConflict   = errors.New("resource revision conflict")
	ErrNodeAssignmentConflict     = errors.New("node direction assignment conflicts")
	ErrNodeNameConflict           = errors.New("access node name already exists")
	ErrAccountConflict            = errors.New("account alias already exists")
	ErrInvalidResource            = errors.New("invalid resource input")
	ErrProxyCredentialUnavailable = errors.New("proxy credential is unavailable")
	ErrResourceIntegrity          = errors.New("stored resource is inconsistent")
	ErrResourceStorage            = errors.New("resource storage operation failed")
)

const accountReadColumns = `
account_id, platform, alias, session_state, session_revision, last_checked_at`

const nodeReadColumns = `
node_id, name, kind, region, egress_mode, state, egress_revision,
assignment_revision, appid, (proxy_plaintext IS NOT NULL), sticky_session_valid_until,
exit_verified_revision, host(exit_address), exit_verified_at, exit_valid_until`

type rowScanner interface {
	Scan(dest ...any) error
}

// CreateAccount stores a plaintext session at revision one.
func (s *Store) CreateAccount(ctx context.Context, platform resource.Platform, alias string, session []byte) (resource.PlatformAccount, error) {
	if err := s.validate(); err != nil {
		return resource.PlatformAccount{}, err
	}
	if err := validateNewAccount(platform, alias); err != nil {
		return resource.PlatformAccount{}, fmt.Errorf("%w: %s", ErrInvalidResource, err.Error())
	}
	if err := validateSessionPlaintext(session); err != nil {
		return resource.PlatformAccount{}, fmt.Errorf("%w: %s", ErrInvalidResource, err.Error())
	}
	accountID, err := s.nextIdentity(ctx, "platform_accounts", "account_id")
	if err != nil {
		return resource.PlatformAccount{}, err
	}
	account, err := scanAccount(s.db.QueryRowContext(ctx, `
INSERT INTO platform_accounts (
    account_id, platform, alias, session_plaintext
) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4)
RETURNING `+accountReadColumns,
		accountID, string(platform), alias, session,
	))
	if err != nil {
		return resource.PlatformAccount{}, mapAccountWriteError(err)
	}
	return account, nil
}

// Account returns one credential-safe account read model.
func (s *Store) Account(ctx context.Context, id resource.AccountID) (resource.PlatformAccount, bool, error) {
	if err := s.validate(); err != nil {
		return resource.PlatformAccount{}, false, err
	}
	if err := id.Validate(); err != nil {
		return resource.PlatformAccount{}, false, err
	}
	account, err := scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountReadColumns+`
FROM platform_accounts WHERE account_id = $1`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.PlatformAccount{}, false, nil
	}
	if err != nil {
		return resource.PlatformAccount{}, false, mapResourceReadError(err)
	}
	return account, true, nil
}

// ListAccounts returns credential-safe account models in stable identity order.
func (s *Store) ListAccounts(ctx context.Context) ([]resource.PlatformAccount, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+accountReadColumns+`
FROM platform_accounts ORDER BY account_id`)
	if err != nil {
		return nil, ErrResourceStorage
	}
	defer rows.Close()
	accounts := make([]resource.PlatformAccount, 0)
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, mapResourceReadError(err)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrResourceStorage
	}
	return accounts, nil
}

// ReplaceAccountSession atomically replaces a matching session revision.
func (s *Store) ReplaceAccountSession(ctx context.Context, id resource.AccountID, expectedRevision int64, session []byte) (resource.PlatformAccount, error) {
	if err := s.validate(); err != nil {
		return resource.PlatformAccount{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.PlatformAccount{}, err
	}
	if err := validateSessionPlaintext(session); err != nil {
		return resource.PlatformAccount{}, err
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT TRUE FROM platform_accounts WHERE account_id = $1`, int64(id)).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return resource.PlatformAccount{}, ErrResourceNotFound
	} else if err != nil {
		return resource.PlatformAccount{}, ErrResourceStorage
	}
	account, err := scanAccount(s.db.QueryRowContext(ctx, `
UPDATE platform_accounts
SET session_state = 'unverified', session_revision = session_revision + 1,
    last_checked_at = NULL, session_plaintext = $3
WHERE account_id = $1 AND session_revision = $2
RETURNING `+accountReadColumns,
		int64(id), expectedRevision, session,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.PlatformAccount{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.PlatformAccount{}, fmt.Errorf("replace account session: %w", ErrResourceStorage)
	}
	return account, nil
}

// RecordAccountSessionCheck records a valid or invalid result for one current revision.
func (s *Store) RecordAccountSessionCheck(ctx context.Context, id resource.AccountID, expectedRevision int64, state resource.AccountSessionState, checkedAt time.Time) (resource.PlatformAccount, error) {
	if err := s.validate(); err != nil {
		return resource.PlatformAccount{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.PlatformAccount{}, err
	}
	if state != resource.AccountSessionStateValid && state != resource.AccountSessionStateInvalid {
		return resource.PlatformAccount{}, fmt.Errorf("account check state must be valid or invalid")
	}
	checkedAt = normalizePostgresTime(checkedAt)
	if checkedAt.IsZero() {
		return resource.PlatformAccount{}, fmt.Errorf("checked_at is required")
	}
	account, err := scanAccount(s.db.QueryRowContext(ctx, `
UPDATE platform_accounts
SET session_state = $3, last_checked_at = $4
WHERE account_id = $1 AND session_revision = $2
  AND (
      last_checked_at IS NULL OR last_checked_at < $4 OR
      (last_checked_at = $4 AND session_state = $3)
  )
RETURNING `+accountReadColumns, int64(id), expectedRevision, string(state), checkedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.PlatformAccount{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.PlatformAccount{}, mapResourceReadError(err)
	}
	return account, nil
}

// OpenAccountSessionAt returns the plaintext session for the observed revision.
func (s *Store) OpenAccountSessionAt(ctx context.Context, id resource.AccountID, expectedRevision int64) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return nil, err
	}
	var plaintext []byte
	var scanned accountScanData
	destinations := append(scanned.destinations(), &plaintext)
	err := s.db.QueryRowContext(ctx, `SELECT `+accountReadColumns+`, session_plaintext
FROM platform_accounts WHERE account_id = $1`, int64(id)).Scan(destinations...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, ErrResourceStorage
	}
	account, err := scanned.result()
	if err != nil || account.ID != id {
		return nil, ErrResourceIntegrity
	}
	if account.SessionRevision != expectedRevision {
		return nil, ErrResourceRevisionConflict
	}
	if len(plaintext) == 0 {
		return nil, ErrResourceIntegrity
	}
	return append([]byte(nil), plaintext...), nil
}

// CreateNode stores a direct or proxy node at revision one.
func (s *Store) CreateNode(ctx context.Context, name string, input resource.NodeConnectionInput) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	prepared, err := prepareNodeConnection(name, input)
	if err != nil {
		return resource.AccessNode{}, err
	}
	nodeID, err := s.nextIdentity(ctx, "access_nodes", "node_id")
	if err != nil {
		return resource.AccessNode{}, err
	}
	proxyPlaintext := nodeProxyPlaintext(prepared)
	node, err := scanCompleteNode(ctx, s.db, s.db.QueryRowContext(ctx, `
INSERT INTO access_nodes (
    node_id, name, kind, region, egress_mode, proxy_plaintext, sticky_session_valid_until
) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING `+nodeReadColumns,
		nodeID, name, string(prepared.Kind), string(prepared.Region), string(prepared.EgressMode),
		proxyPlaintext, nullableTime(prepared.StickySessionValidUntil),
	))
	if err != nil {
		return resource.AccessNode{}, mapNodeWriteError(err)
	}
	return node, nil
}

// Node returns one credential-safe access node read model.
func (s *Store) Node(ctx context.Context, id resource.NodeID) (resource.AccessNode, bool, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, false, err
	}
	if err := id.Validate(); err != nil {
		return resource.AccessNode{}, false, err
	}
	node, err := scanCompleteNode(ctx, s.db, s.db.QueryRowContext(ctx, `SELECT `+nodeReadColumns+`
FROM access_nodes WHERE node_id = $1`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, false, nil
	}
	if err != nil {
		return resource.AccessNode{}, false, mapResourceReadError(err)
	}
	return node, true, nil
}

// ListNodes returns credential-safe nodes in stable identity order.
func (s *Store) ListNodes(ctx context.Context) ([]resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeReadColumns+`
FROM access_nodes ORDER BY node_id`)
	if err != nil {
		return nil, ErrResourceStorage
	}
	defer rows.Close()
	nodes := make([]resource.AccessNode, 0)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, mapResourceReadError(err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrResourceStorage
	}
	if err := attachAllNodeSides(ctx, s.db, nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

// ReplaceNodeConnection atomically starts a new validating egress revision.
func (s *Store) ReplaceNodeConnection(ctx context.Context, id resource.NodeID, expectedRevision int64, input resource.NodeConnectionInput) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM access_nodes WHERE node_id = $1`, int64(id)).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceNotFound
	} else if err != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	prepared, err := prepareNodeConnection(name, input)
	if err != nil {
		return resource.AccessNode{}, err
	}
	proxyPlaintext := nodeProxyPlaintext(prepared)
	node, err := scanCompleteNode(ctx, s.db, s.db.QueryRowContext(ctx, `
UPDATE access_nodes
SET kind = $3, region = $4, egress_mode = $5, proxy_plaintext = $6,
    sticky_session_valid_until = $7, state = 'validating',
    egress_revision = egress_revision + 1,
    exit_address = NULL, exit_verified_revision = NULL, exit_verified_at = NULL, exit_valid_until = NULL
WHERE node_id = $1 AND egress_revision = $2
RETURNING `+nodeReadColumns,
		int64(id), expectedRevision, string(prepared.Kind), string(prepared.Region), string(prepared.EgressMode),
		proxyPlaintext, nullableTime(prepared.StickySessionValidUntil),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.AccessNode{}, fmt.Errorf("replace node connection: %w", ErrResourceStorage)
	}
	return node, nil
}

// BeginNodeRevalidation clears old evidence and advances the egress revision.
func (s *Store) BeginNodeRevalidation(ctx context.Context, id resource.NodeID, expectedRevision int64) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	node, err := scanCompleteNode(ctx, s.db, s.db.QueryRowContext(ctx, `
UPDATE access_nodes
SET state = 'validating', egress_revision = egress_revision + 1,
    exit_address = NULL, exit_verified_revision = NULL, exit_verified_at = NULL, exit_valid_until = NULL
WHERE node_id = $1 AND egress_revision = $2
RETURNING `+nodeReadColumns, int64(id), expectedRevision))
	if err == nil {
		return node, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, mapResourceReadError(err)
	}
	_, found, queryErr := s.Node(ctx, id)
	if queryErr != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	if !found {
		return resource.AccessNode{}, ErrResourceNotFound
	}
	return resource.AccessNode{}, ErrResourceRevisionConflict
}

// RecordNodeExit makes current validating evidence available.
func (s *Store) RecordNodeExit(ctx context.Context, id resource.NodeID, expectedRevision int64, address netip.Addr, verifiedAt, validUntil time.Time) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	verifiedAt = normalizePostgresTime(verifiedAt)
	validUntil = normalizePostgresTime(validUntil)
	verification := resource.ExitVerification{VerifiedRevision: expectedRevision, Address: address, VerifiedAt: verifiedAt, ValidUntil: validUntil}
	if err := verification.Validate(); err != nil {
		return resource.AccessNode{}, err
	}
	node, err := scanCompleteNode(ctx, s.db, s.db.QueryRowContext(ctx, `
UPDATE access_nodes
SET state = 'available', exit_address = $3, exit_verified_revision = $2,
    exit_verified_at = $4, exit_valid_until = $5
WHERE node_id = $1 AND egress_revision = $2 AND state = 'validating'
  AND (sticky_session_valid_until IS NULL OR $5 <= sticky_session_valid_until)
RETURNING `+nodeReadColumns, int64(id), expectedRevision, address.String(), verifiedAt, validUntil))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.AccessNode{}, mapResourceReadError(err)
	}
	return node, nil
}

// MarkNodeUnavailable clears evidence for a matching egress revision.
func (s *Store) MarkNodeUnavailable(ctx context.Context, id resource.NodeID, expectedRevision int64) (resource.AccessNode, error) {
	return s.updateNodeRevision(ctx, id, expectedRevision, `
UPDATE access_nodes
SET state = 'unavailable', exit_address = NULL, exit_verified_revision = NULL,
    exit_verified_at = NULL, exit_valid_until = NULL
WHERE node_id = $1 AND egress_revision = $2 AND state IN ('validating', 'unavailable')
RETURNING `+nodeReadColumns)
}

// AssignNodeGame sets or clears the node's game using assignment revision CAS.
// appID 0 clears the game. Direction rows or combinations block the change.
func (s *Store) AssignNodeGame(ctx context.Context, id resource.NodeID, expectedRevision int64, appID int64) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	if appID < 0 {
		return resource.AccessNode{}, fmt.Errorf("appid must be non-negative")
	}
	nextRevision, err := nextResourceRevision(expectedRevision)
	if err != nil {
		return resource.AccessNode{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	defer func() { _ = tx.Rollback() }()

	node, err := scanNode(tx.QueryRowContext(ctx, `SELECT `+nodeReadColumns+`
FROM access_nodes WHERE node_id = $1 FOR UPDATE`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceNotFound
	}
	if err != nil {
		return resource.AccessNode{}, mapResourceReadError(err)
	}
	if err := attachNodeSides(ctx, tx, &node); err != nil {
		return resource.AccessNode{}, err
	}
	if node.AssignmentRevision != expectedRevision {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	if node.AppID == appID {
		if err := tx.Commit(); err != nil {
			return resource.AccessNode{}, ErrResourceStorage
		}
		return node, nil
	}

	var hasDirection bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM node_direction_assignments WHERE node_id = $1)`, int64(id)).Scan(&hasDirection); err != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	var hasCombination bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM account_node_combinations WHERE node_id = $1)`, int64(id)).Scan(&hasCombination); err != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	if hasDirection || hasCombination {
		return resource.AccessNode{}, ErrResourceDependency
	}

	var storedAppID any
	if appID > 0 {
		storedAppID = appID
	}
	node, err = scanCompleteNode(ctx, tx, tx.QueryRowContext(ctx, `
UPDATE access_nodes
SET appid = $3, assignment_revision = $4
WHERE node_id = $1 AND assignment_revision = $2
RETURNING `+nodeReadColumns, int64(id), expectedRevision, storedAppID, nextRevision))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.AccessNode{}, mapAssignmentWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	return node, nil
}

// AssignNodeSide sets or deletes one platform direction using assignment revision CAS.
// Empty side deletes that platform row. Combinations on that platform block the change.
func (s *Store) AssignNodeSide(
	ctx context.Context,
	id resource.NodeID,
	expectedRevision int64,
	platform resource.Platform,
	side market.Side,
) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	if err := platform.Validate(); err != nil {
		return resource.AccessNode{}, fmt.Errorf("%w: %s", ErrInvalidResource, err.Error())
	}
	if side != "" && side != market.SideBid && side != market.SideAsk {
		return resource.AccessNode{}, fmt.Errorf("%w: invalid assignment side %q", ErrInvalidResource, side)
	}
	nextRevision, err := nextResourceRevision(expectedRevision)
	if err != nil {
		return resource.AccessNode{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	defer func() { _ = tx.Rollback() }()

	node, err := scanNode(tx.QueryRowContext(ctx, `SELECT `+nodeReadColumns+`
FROM access_nodes WHERE node_id = $1 FOR UPDATE`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceNotFound
	}
	if err != nil {
		return resource.AccessNode{}, mapResourceReadError(err)
	}
	if err := attachNodeSides(ctx, tx, &node); err != nil {
		return resource.AccessNode{}, err
	}
	if node.AssignmentRevision != expectedRevision {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}

	current, assigned := node.SideFor(platform)
	switch {
	case side == "":
		if !assigned {
			return resource.AccessNode{}, ErrNodeAssignmentConflict
		}
		var hasCombination bool
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM account_node_combinations WHERE node_id = $1 AND platform = $2
)`, int64(id), string(platform)).Scan(&hasCombination); err != nil {
			return resource.AccessNode{}, ErrResourceStorage
		}
		if hasCombination {
			return resource.AccessNode{}, ErrResourceDependency
		}
		if _, err := tx.ExecContext(ctx, `
DELETE FROM node_direction_assignments WHERE node_id = $1 AND platform = $2`, int64(id), string(platform)); err != nil {
			return resource.AccessNode{}, mapAssignmentWriteError(err)
		}
	case assigned && current == side:
		if err := tx.Commit(); err != nil {
			return resource.AccessNode{}, ErrResourceStorage
		}
		return node, nil
	default:
		if node.AppID < 1 {
			return resource.AccessNode{}, ErrNodeAssignmentConflict
		}
		if assigned {
			var hasCombination bool
			if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM account_node_combinations WHERE node_id = $1 AND platform = $2
)`, int64(id), string(platform)).Scan(&hasCombination); err != nil {
				return resource.AccessNode{}, ErrResourceStorage
			}
			if hasCombination {
				return resource.AccessNode{}, ErrResourceDependency
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE node_direction_assignments SET side = $3
WHERE node_id = $1 AND platform = $2`, int64(id), string(platform), string(side)); err != nil {
				return resource.AccessNode{}, mapAssignmentWriteError(err)
			}
		} else if _, err := tx.ExecContext(ctx, `
INSERT INTO node_direction_assignments (node_id, platform, side)
VALUES ($1, $2, $3)`, int64(id), string(platform), string(side)); err != nil {
			return resource.AccessNode{}, mapAssignmentWriteError(err)
		}
	}

	node, err = scanCompleteNode(ctx, tx, tx.QueryRowContext(ctx, `
UPDATE access_nodes
SET assignment_revision = $3
WHERE node_id = $1 AND assignment_revision = $2
RETURNING `+nodeReadColumns, int64(id), expectedRevision, nextRevision))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.AccessNode{}, mapAssignmentWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	return node, nil
}

// OpenNodeProxyCredentialAt returns plaintext proxy material for the observed revisions.
func (s *Store) OpenNodeProxyCredentialAt(
	ctx context.Context,
	id resource.NodeID,
	expectedEgressRevision, expectedAssignmentRevision int64,
) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedEgressRevision); err != nil {
		return nil, err
	}
	if expectedAssignmentRevision < 1 {
		return nil, fmt.Errorf("expected assignment revision must be at least 1")
	}
	var plaintext []byte
	var scanned nodeScanData
	destinations := append(scanned.destinations(), &plaintext)
	err := s.db.QueryRowContext(ctx, `SELECT `+nodeReadColumns+`, proxy_plaintext
FROM access_nodes WHERE node_id = $1`, int64(id)).Scan(destinations...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, ErrResourceStorage
	}
	node, validationErr := scanned.result()
	if validationErr != nil || node.ID != id {
		return nil, ErrResourceIntegrity
	}
	if node.EgressRevision != expectedEgressRevision || node.AssignmentRevision != expectedAssignmentRevision {
		return nil, ErrResourceRevisionConflict
	}
	if node.Kind != resource.NodeKindProxy {
		return nil, ErrProxyCredentialUnavailable
	}
	if len(plaintext) == 0 {
		return nil, ErrResourceIntegrity
	}
	return append([]byte(nil), plaintext...), nil
}

func (s *Store) updateNodeRevision(ctx context.Context, id resource.NodeID, expectedRevision int64, statement string) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	node, err := scanCompleteNode(ctx, s.db, s.db.QueryRowContext(ctx, statement, int64(id), expectedRevision))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.AccessNode{}, mapResourceReadError(err)
	}
	return node, nil
}

func validateNewAccount(platform resource.Platform, alias string) error {
	account := resource.PlatformAccount{ID: 1, Platform: platform, Alias: alias, SessionState: resource.AccountSessionStateUnverified, SessionRevision: 1}
	return account.Validate()
}

func validateSessionPlaintext(session []byte) error {
	if len(session) == 0 {
		return fmt.Errorf("session is empty")
	}
	return nil
}

func prepareNodeConnection(name string, input resource.NodeConnectionInput) (resource.NodeConnectionInput, error) {
	input.StickySessionValidUntil = normalizedTimePointer(input.StickySessionValidUntil)
	node := resource.AccessNode{
		ID:                      1,
		Name:                    name,
		Kind:                    input.Kind,
		Region:                  input.Region,
		EgressMode:              input.EgressMode,
		State:                   resource.NodeStateValidating,
		EgressRevision:          1,
		AssignmentRevision:      1,
		HasProxyCredential:      len(input.ProxyCredential) > 0,
		StickySessionValidUntil: cloneTime(input.StickySessionValidUntil),
	}
	if err := node.Validate(); err != nil {
		return resource.NodeConnectionInput{}, fmt.Errorf("%w: %s", ErrInvalidResource, err.Error())
	}
	input.ProxyCredential = append([]byte(nil), input.ProxyCredential...)
	return input, nil
}

func nodeProxyPlaintext(input resource.NodeConnectionInput) any {
	if input.Kind == resource.NodeKindDirect {
		return nil
	}
	return input.ProxyCredential
}

func validateIdentityRevision(identityErr error, revision int64) error {
	if identityErr != nil {
		return identityErr
	}
	if revision < 1 {
		return fmt.Errorf("expected revision must be at least 1")
	}
	return nil
}

func nextResourceRevision(current int64) (int64, error) {
	if current == int64(^uint64(0)>>1) {
		return 0, fmt.Errorf("resource revision is exhausted")
	}
	return current + 1, nil
}

func (s *Store) nextIdentity(ctx context.Context, table, column string) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT nextval(pg_get_serial_sequence($1, $2))`, table, column).Scan(&id); err != nil {
		return 0, ErrResourceStorage
	}
	if id < 1 {
		return 0, ErrResourceIntegrity
	}
	return id, nil
}

func mapResourceReadError(err error) error {
	if errors.Is(err, ErrResourceIntegrity) {
		return ErrResourceIntegrity
	}
	return ErrResourceStorage
}

func mapAccountWriteError(err error) error {
	if postgresErrorCode(err) == "23505" {
		return ErrAccountConflict
	}
	return ErrResourceStorage
}

func mapNodeWriteError(err error) error {
	if postgresErrorCode(err) == "23505" {
		return ErrNodeNameConflict
	}
	return ErrResourceStorage
}

type accountScanData struct {
	account resource.PlatformAccount
	id      int64
	checked sql.NullTime
}

func (data *accountScanData) destinations() []any {
	return []any{
		&data.id, &data.account.Platform, &data.account.Alias,
		&data.account.SessionState, &data.account.SessionRevision, &data.checked,
	}
}

func (data *accountScanData) result() (resource.PlatformAccount, error) {
	data.account.ID = resource.AccountID(data.id)
	if data.checked.Valid {
		data.account.LastCheckedAt = cloneTime(&data.checked.Time)
	}
	if err := data.account.Validate(); err != nil {
		return resource.PlatformAccount{}, ErrResourceIntegrity
	}
	return data.account, nil
}

func scanAccount(row rowScanner) (resource.PlatformAccount, error) {
	var data accountScanData
	if err := row.Scan(data.destinations()...); err != nil {
		return resource.PlatformAccount{}, err
	}
	return data.result()
}

type nodeScanData struct {
	node                   resource.AccessNode
	id                     int64
	appid                  sql.NullInt64
	sticky                 sql.NullTime
	verifiedRevision       sql.NullInt64
	address                sql.NullString
	verifiedAt, validUntil sql.NullTime
}

func (data *nodeScanData) destinations() []any {
	return []any{
		&data.id, &data.node.Name, &data.node.Kind, &data.node.Region,
		&data.node.EgressMode, &data.node.State, &data.node.EgressRevision,
		&data.node.AssignmentRevision, &data.appid, &data.node.HasProxyCredential, &data.sticky,
		&data.verifiedRevision, &data.address, &data.verifiedAt, &data.validUntil,
	}
}

func (data *nodeScanData) result() (resource.AccessNode, error) {
	data.node.ID = resource.NodeID(data.id)
	if data.appid.Valid {
		data.node.AppID = data.appid.Int64
	}
	if data.sticky.Valid {
		data.node.StickySessionValidUntil = cloneTime(&data.sticky.Time)
	}
	if data.verifiedRevision.Valid || data.address.Valid || data.verifiedAt.Valid || data.validUntil.Valid {
		if !data.verifiedRevision.Valid || !data.address.Valid || !data.verifiedAt.Valid || !data.validUntil.Valid {
			return resource.AccessNode{}, ErrResourceIntegrity
		}
		parsed, err := netip.ParseAddr(data.address.String)
		if err != nil {
			return resource.AccessNode{}, ErrResourceIntegrity
		}
		data.node.ExitVerification = &resource.ExitVerification{
			VerifiedRevision: data.verifiedRevision.Int64,
			Address:          parsed,
			VerifiedAt:       data.verifiedAt.Time,
			ValidUntil:       data.validUntil.Time,
		}
	}
	if err := data.node.Validate(); err != nil {
		return resource.AccessNode{}, ErrResourceIntegrity
	}
	return data.node, nil
}

func scanNode(row rowScanner) (resource.AccessNode, error) {
	var data nodeScanData
	if err := row.Scan(data.destinations()...); err != nil {
		return resource.AccessNode{}, err
	}
	return data.result()
}

type sideQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func scanCompleteNode(ctx context.Context, querier sideQuerier, row rowScanner) (resource.AccessNode, error) {
	node, err := scanNode(row)
	if err != nil {
		return resource.AccessNode{}, err
	}
	if err := attachNodeSides(ctx, querier, &node); err != nil {
		return resource.AccessNode{}, err
	}
	return node, nil
}

func attachNodeSides(ctx context.Context, querier sideQuerier, node *resource.AccessNode) error {
	sides, err := listNodeSides(ctx, querier, node.ID)
	if err != nil {
		return err
	}
	node.Sides = sides
	if err := node.Validate(); err != nil {
		return ErrResourceIntegrity
	}
	return nil
}

func attachAllNodeSides(ctx context.Context, querier sideQuerier, nodes []resource.AccessNode) error {
	byNode, err := listAllNodeSides(ctx, querier)
	if err != nil {
		return err
	}
	for index := range nodes {
		nodes[index].Sides = byNode[nodes[index].ID]
		if err := nodes[index].Validate(); err != nil {
			return ErrResourceIntegrity
		}
	}
	return nil
}

func listNodeSides(ctx context.Context, querier sideQuerier, id resource.NodeID) ([]resource.NodeSideAssignment, error) {
	rows, err := querier.QueryContext(ctx, `
SELECT platform, side
FROM node_direction_assignments
WHERE node_id = $1
ORDER BY platform`, int64(id))
	if err != nil {
		return nil, ErrResourceStorage
	}
	defer rows.Close()
	return scanNodeSideRows(rows)
}

func listAllNodeSides(ctx context.Context, querier sideQuerier) (map[resource.NodeID][]resource.NodeSideAssignment, error) {
	rows, err := querier.QueryContext(ctx, `
SELECT node_id, platform, side
FROM node_direction_assignments
ORDER BY node_id, platform`)
	if err != nil {
		return nil, ErrResourceStorage
	}
	defer rows.Close()
	byNode := make(map[resource.NodeID][]resource.NodeSideAssignment)
	for rows.Next() {
		var nodeID int64
		var assignment resource.NodeSideAssignment
		if err := rows.Scan(&nodeID, &assignment.Platform, &assignment.Side); err != nil {
			return nil, ErrResourceStorage
		}
		id := resource.NodeID(nodeID)
		byNode[id] = append(byNode[id], assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrResourceStorage
	}
	return byNode, nil
}

func scanNodeSideRows(rows *sql.Rows) ([]resource.NodeSideAssignment, error) {
	sides := make([]resource.NodeSideAssignment, 0)
	for rows.Next() {
		var assignment resource.NodeSideAssignment
		if err := rows.Scan(&assignment.Platform, &assignment.Side); err != nil {
			return nil, ErrResourceStorage
		}
		sides = append(sides, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrResourceStorage
	}
	if len(sides) == 0 {
		return nil, nil
	}
	return sides, nil
}
