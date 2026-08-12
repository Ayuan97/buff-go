package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"buff-go/internal/credential"
	"buff-go/internal/resource"
)

var (
	ErrCredentialCipherUnavailable = errors.New("credential cipher is unavailable")
	ErrResourceNotFound            = errors.New("resource not found")
	ErrResourceRevisionConflict    = errors.New("resource revision conflict")
	ErrNodeAssignmentConflict      = errors.New("access node is assigned to another platform")
	ErrProxyCredentialUnavailable  = errors.New("proxy credential is unavailable")
	ErrResourceIntegrity           = errors.New("stored resource is inconsistent")
	ErrResourceStorage             = errors.New("resource storage operation failed")
)

const accountReadColumns = `
account_id, platform, alias, session_state, session_revision, last_checked_at`

const nodeReadColumns = `
node_id, name, kind, region, egress_mode, state, egress_revision,
assignment_revision, assigned_platform, (proxy_ciphertext IS NOT NULL), sticky_session_valid_until,
exit_verified_revision, host(exit_address), exit_verified_at, exit_valid_until`

type rowScanner interface {
	Scan(dest ...any) error
}

// CreateAccount stores a write-only encrypted session at revision one.
func (s *Store) CreateAccount(ctx context.Context, platform resource.Platform, alias string, session []byte) (resource.PlatformAccount, error) {
	if err := s.validate(); err != nil {
		return resource.PlatformAccount{}, err
	}
	if err := validateNewAccount(platform, alias); err != nil {
		return resource.PlatformAccount{}, err
	}
	if err := s.requireCredentialCipher(); err != nil {
		return resource.PlatformAccount{}, err
	}
	accountID, err := s.nextIdentity(ctx, "platform_accounts", "account_id")
	if err != nil {
		return resource.PlatformAccount{}, err
	}
	envelope, err := s.sealCredential(session, accountSessionAAD(resource.AccountID(accountID), platform, 1))
	if err != nil {
		return resource.PlatformAccount{}, err
	}
	account, err := scanAccount(s.db.QueryRowContext(ctx, `
INSERT INTO platform_accounts (
    account_id, platform, alias, session_envelope_version, session_key_id, session_nonce, session_ciphertext
) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING `+accountReadColumns,
		accountID, string(platform), alias, int64(envelope.Version), envelope.KeyID, envelope.Nonce, envelope.Ciphertext,
	))
	if err != nil {
		return resource.PlatformAccount{}, fmt.Errorf("create platform account: %w", ErrResourceStorage)
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
	if err := s.requireCredentialCipher(); err != nil {
		return resource.PlatformAccount{}, err
	}
	var platform resource.Platform
	if err := s.db.QueryRowContext(ctx, `SELECT platform FROM platform_accounts WHERE account_id = $1`, int64(id)).Scan(&platform); errors.Is(err, sql.ErrNoRows) {
		return resource.PlatformAccount{}, ErrResourceNotFound
	} else if err != nil {
		return resource.PlatformAccount{}, ErrResourceStorage
	}
	nextRevision, err := nextResourceRevision(expectedRevision)
	if err != nil {
		return resource.PlatformAccount{}, err
	}
	envelope, err := s.sealCredential(session, accountSessionAAD(id, platform, nextRevision))
	if err != nil {
		return resource.PlatformAccount{}, err
	}
	account, err := scanAccount(s.db.QueryRowContext(ctx, `
UPDATE platform_accounts
SET session_state = 'unverified', session_revision = session_revision + 1,
    last_checked_at = NULL, session_envelope_version = $3, session_key_id = $4,
    session_nonce = $5, session_ciphertext = $6
WHERE account_id = $1 AND session_revision = $2
RETURNING `+accountReadColumns,
		int64(id), expectedRevision, int64(envelope.Version), envelope.KeyID, envelope.Nonce, envelope.Ciphertext,
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

// OpenAccountSessionAt decrypts only the account session revision the caller
// observed when it acquired the resource.
func (s *Store) OpenAccountSessionAt(ctx context.Context, id resource.AccountID, expectedRevision int64) ([]byte, error) {
	if err := s.requireCredentialCipher(); err != nil {
		return nil, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return nil, err
	}
	var version int64
	var envelope credential.Envelope
	var scanned accountScanData
	destinations := append(scanned.destinations(), &version, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	err := s.db.QueryRowContext(ctx, `SELECT `+accountReadColumns+`,
session_envelope_version, session_key_id, session_nonce, session_ciphertext
FROM platform_accounts WHERE account_id = $1`, int64(id)).Scan(destinations...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, ErrResourceStorage
	}
	if version != int64(credential.EnvelopeVersion) {
		return nil, ErrResourceIntegrity
	}
	account, err := scanned.result()
	if err != nil || account.ID != id {
		return nil, ErrResourceIntegrity
	}
	if account.SessionRevision != expectedRevision {
		return nil, ErrResourceRevisionConflict
	}
	envelope.Version = uint8(version)
	plaintext, err := s.credentialCipher.Open(envelope, accountSessionAAD(account.ID, account.Platform, account.SessionRevision))
	if err != nil {
		return nil, fmt.Errorf("open account session: %w", err)
	}
	return plaintext, nil
}

// CreateNode stores a direct or encrypted-proxy node at revision one.
func (s *Store) CreateNode(ctx context.Context, name string, input resource.NodeConnectionInput) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	prepared, err := prepareNodeConnection(name, input)
	if err != nil {
		return resource.AccessNode{}, err
	}
	if prepared.Kind == resource.NodeKindProxy {
		if err := s.requireCredentialCipher(); err != nil {
			return resource.AccessNode{}, err
		}
	}
	nodeID, err := s.nextIdentity(ctx, "access_nodes", "node_id")
	if err != nil {
		return resource.AccessNode{}, err
	}
	envelope, hasEnvelope, err := s.sealNodeCredential(resource.NodeID(nodeID), 1, prepared)
	if err != nil {
		return resource.AccessNode{}, err
	}
	version, keyID, nonce, ciphertext := envelopeDatabaseValues(envelope, hasEnvelope)
	node, err := scanNode(s.db.QueryRowContext(ctx, `
INSERT INTO access_nodes (
    node_id, name, kind, region, egress_mode, proxy_envelope_version, proxy_key_id,
    proxy_nonce, proxy_ciphertext, sticky_session_valid_until
) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING `+nodeReadColumns,
		nodeID, name, string(prepared.Kind), string(prepared.Region), string(prepared.EgressMode),
		version, keyID, nonce, ciphertext, nullableTime(prepared.StickySessionValidUntil),
	))
	if err != nil {
		return resource.AccessNode{}, fmt.Errorf("create access node: %w", ErrResourceStorage)
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
	node, err := scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeReadColumns+`
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
	if input.Kind == resource.NodeKindProxy {
		if err := s.requireCredentialCipher(); err != nil {
			return resource.AccessNode{}, err
		}
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
	nextRevision, err := nextResourceRevision(expectedRevision)
	if err != nil {
		return resource.AccessNode{}, err
	}
	envelope, hasEnvelope, err := s.sealNodeCredential(id, nextRevision, prepared)
	if err != nil {
		return resource.AccessNode{}, err
	}
	version, keyID, nonce, ciphertext := envelopeDatabaseValues(envelope, hasEnvelope)
	node, err := scanNode(s.db.QueryRowContext(ctx, `
UPDATE access_nodes
SET kind = $3, region = $4, egress_mode = $5,
    proxy_envelope_version = $6, proxy_key_id = $7, proxy_nonce = $8, proxy_ciphertext = $9,
    sticky_session_valid_until = $10, state = 'validating',
    egress_revision = egress_revision + 1,
    exit_address = NULL, exit_verified_revision = NULL, exit_verified_at = NULL, exit_valid_until = NULL
WHERE node_id = $1 AND egress_revision = $2
RETURNING `+nodeReadColumns,
		int64(id), expectedRevision, string(prepared.Kind), string(prepared.Region), string(prepared.EgressMode),
		version, keyID, nonce, ciphertext, nullableTime(prepared.StickySessionValidUntil),
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
	nextRevision, err := nextResourceRevision(expectedRevision)
	if err != nil {
		return resource.AccessNode{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	defer func() { _ = tx.Rollback() }()

	var kind resource.NodeKind
	var storedRevision int64
	var version sql.NullInt64
	var keyID sql.NullString
	var nonce, ciphertext []byte
	err = tx.QueryRowContext(ctx, `
SELECT kind, egress_revision, proxy_envelope_version, proxy_key_id, proxy_nonce, proxy_ciphertext
FROM access_nodes WHERE node_id = $1 FOR UPDATE`, int64(id)).Scan(
		&kind, &storedRevision, &version, &keyID, &nonce, &ciphertext,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceNotFound
	}
	if err != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	if storedRevision != expectedRevision {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}

	var newEnvelope credential.Envelope
	hasEnvelope := false
	if kind == resource.NodeKindProxy {
		if err := s.requireCredentialCipher(); err != nil {
			return resource.AccessNode{}, err
		}
		if !version.Valid || version.Int64 != int64(credential.EnvelopeVersion) ||
			!keyID.Valid || len(nonce) == 0 || len(ciphertext) == 0 {
			return resource.AccessNode{}, ErrResourceIntegrity
		}
		oldEnvelope := credential.Envelope{
			Version:    credential.EnvelopeVersion,
			KeyID:      keyID.String,
			Nonce:      nonce,
			Ciphertext: ciphertext,
		}
		plaintext, err := s.credentialCipher.Open(oldEnvelope, nodeProxyAAD(id, kind, expectedRevision))
		if err != nil {
			return resource.AccessNode{}, fmt.Errorf("open node credential for revalidation: %w", err)
		}
		newEnvelope, err = s.credentialCipher.Seal(plaintext, nodeProxyAAD(id, kind, nextRevision))
		for index := range plaintext {
			plaintext[index] = 0
		}
		if err != nil {
			return resource.AccessNode{}, fmt.Errorf("reseal node credential for revalidation: %w", err)
		}
		hasEnvelope = true
	} else if kind != resource.NodeKindDirect {
		return resource.AccessNode{}, ErrResourceIntegrity
	}
	newVersion, newKeyID, newNonce, newCiphertext := envelopeDatabaseValues(newEnvelope, hasEnvelope)
	node, err := scanNode(tx.QueryRowContext(ctx, `
UPDATE access_nodes
SET state = 'validating', egress_revision = egress_revision + 1,
    proxy_envelope_version = $3, proxy_key_id = $4, proxy_nonce = $5, proxy_ciphertext = $6,
    exit_address = NULL, exit_verified_revision = NULL, exit_verified_at = NULL, exit_valid_until = NULL
WHERE node_id = $1 AND egress_revision = $2
RETURNING `+nodeReadColumns,
		int64(id), expectedRevision, newVersion, newKeyID, newNonce, newCiphertext,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.AccessNode{}, fmt.Errorf("begin node revalidation: %w", ErrResourceStorage)
	}
	if err := tx.Commit(); err != nil {
		return resource.AccessNode{}, fmt.Errorf("commit node revalidation: %w", ErrResourceStorage)
	}
	return node, nil
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
	node, err := scanNode(s.db.QueryRowContext(ctx, `
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

// AssignNodePlatform assigns an unassigned node using assignment revision CAS.
func (s *Store) AssignNodePlatform(ctx context.Context, id resource.NodeID, expectedRevision int64, platform resource.Platform) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	if err := platform.Validate(); err != nil {
		return resource.AccessNode{}, err
	}
	nextRevision, err := nextResourceRevision(expectedRevision)
	if err != nil {
		return resource.AccessNode{}, err
	}
	node, err := scanNode(s.db.QueryRowContext(ctx, `
UPDATE access_nodes
SET assigned_platform = $3, assignment_revision = $4
WHERE node_id = $1 AND assignment_revision = $2 AND assigned_platform IS NULL
RETURNING `+nodeReadColumns, int64(id), expectedRevision, string(platform), nextRevision))
	if err == nil {
		return node, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, mapAssignmentWriteError(err)
	}
	current, found, queryErr := s.Node(ctx, id)
	if queryErr != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	if !found {
		return resource.AccessNode{}, ErrResourceNotFound
	}
	if current.AssignedPlatform == platform && current.AssignmentRevision == expectedRevision {
		return current, nil
	}
	if current.AssignmentRevision != expectedRevision {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	return resource.AccessNode{}, ErrNodeAssignmentConflict
}

// UnassignNodePlatform clears only the platform and assignment revision the
// caller observed.
func (s *Store) UnassignNodePlatform(ctx context.Context, id resource.NodeID, expectedRevision int64, expectedPlatform resource.Platform) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	if err := expectedPlatform.Validate(); err != nil {
		return resource.AccessNode{}, err
	}
	nextRevision, err := nextResourceRevision(expectedRevision)
	if err != nil {
		return resource.AccessNode{}, err
	}
	node, err := scanNode(s.db.QueryRowContext(ctx, `
UPDATE access_nodes
SET assigned_platform = NULL, assignment_revision = $4
WHERE node_id = $1 AND assignment_revision = $2 AND assigned_platform = $3
RETURNING `+nodeReadColumns, int64(id), expectedRevision, string(expectedPlatform), nextRevision))
	if err == nil {
		return node, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, mapAssignmentWriteError(err)
	}
	current, found, queryErr := s.Node(ctx, id)
	if queryErr != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	if !found {
		return resource.AccessNode{}, ErrResourceNotFound
	}
	if current.AssignmentRevision != expectedRevision {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	return resource.AccessNode{}, ErrNodeAssignmentConflict
}

// ReassignNodePlatform atomically changes an observed platform assignment.
func (s *Store) ReassignNodePlatform(ctx context.Context, id resource.NodeID, expectedRevision int64, expectedPlatform, newPlatform resource.Platform) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	if err := expectedPlatform.Validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := newPlatform.Validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if expectedPlatform == newPlatform {
		current, found, err := s.Node(ctx, id)
		if err != nil {
			return resource.AccessNode{}, ErrResourceStorage
		}
		if !found {
			return resource.AccessNode{}, ErrResourceNotFound
		}
		if current.AssignmentRevision != expectedRevision {
			return resource.AccessNode{}, ErrResourceRevisionConflict
		}
		if current.AssignedPlatform != expectedPlatform {
			return resource.AccessNode{}, ErrNodeAssignmentConflict
		}
		return current, nil
	}
	nextRevision, err := nextResourceRevision(expectedRevision)
	if err != nil {
		return resource.AccessNode{}, err
	}
	node, err := scanNode(s.db.QueryRowContext(ctx, `
UPDATE access_nodes
SET assigned_platform = $4, assignment_revision = $5
WHERE node_id = $1 AND assignment_revision = $2 AND assigned_platform = $3
RETURNING `+nodeReadColumns,
		int64(id), expectedRevision, string(expectedPlatform), string(newPlatform), nextRevision))
	if err == nil {
		return node, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, mapAssignmentWriteError(err)
	}
	current, found, queryErr := s.Node(ctx, id)
	if queryErr != nil {
		return resource.AccessNode{}, ErrResourceStorage
	}
	if !found {
		return resource.AccessNode{}, ErrResourceNotFound
	}
	if current.AssignmentRevision != expectedRevision {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	return resource.AccessNode{}, ErrNodeAssignmentConflict
}

// OpenNodeProxyCredentialAt decrypts only the node egress revision the caller
// observed when it acquired the resource.
func (s *Store) OpenNodeProxyCredentialAt(
	ctx context.Context,
	id resource.NodeID,
	expectedEgressRevision, expectedAssignmentRevision int64,
	expectedPlatform resource.Platform,
) ([]byte, error) {
	if err := s.requireCredentialCipher(); err != nil {
		return nil, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedEgressRevision); err != nil {
		return nil, err
	}
	if expectedAssignmentRevision < 1 {
		return nil, fmt.Errorf("expected assignment revision must be at least 1")
	}
	if expectedPlatform != "" {
		if err := expectedPlatform.Validate(); err != nil {
			return nil, err
		}
	}
	var version sql.NullInt64
	var keyID sql.NullString
	var nonce, ciphertext []byte
	var scanned nodeScanData
	destinations := append(scanned.destinations(), &version, &keyID, &nonce, &ciphertext)
	err := s.db.QueryRowContext(ctx, `SELECT `+nodeReadColumns+`,
proxy_envelope_version, proxy_key_id, proxy_nonce, proxy_ciphertext
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
	if node.AssignedPlatform != expectedPlatform {
		return nil, ErrNodeAssignmentConflict
	}
	if node.Kind != resource.NodeKindProxy {
		return nil, ErrProxyCredentialUnavailable
	}
	if !version.Valid || version.Int64 != int64(credential.EnvelopeVersion) ||
		!keyID.Valid || len(nonce) == 0 || len(ciphertext) == 0 {
		return nil, ErrResourceIntegrity
	}
	envelope := credential.Envelope{Version: credential.EnvelopeVersion, KeyID: keyID.String, Nonce: nonce, Ciphertext: ciphertext}
	plaintext, err := s.credentialCipher.Open(envelope, nodeProxyAAD(node.ID, node.Kind, node.EgressRevision))
	if err != nil {
		return nil, fmt.Errorf("open node proxy credential: %w", err)
	}
	return plaintext, nil
}

func (s *Store) updateNodeRevision(ctx context.Context, id resource.NodeID, expectedRevision int64, statement string) (resource.AccessNode, error) {
	if err := s.validate(); err != nil {
		return resource.AccessNode{}, err
	}
	if err := validateIdentityRevision(id.Validate(), expectedRevision); err != nil {
		return resource.AccessNode{}, err
	}
	node, err := scanNode(s.db.QueryRowContext(ctx, statement, int64(id), expectedRevision))
	if errors.Is(err, sql.ErrNoRows) {
		return resource.AccessNode{}, ErrResourceRevisionConflict
	}
	if err != nil {
		return resource.AccessNode{}, mapResourceReadError(err)
	}
	return node, nil
}

func (s *Store) sealCredential(plaintext, aad []byte) (credential.Envelope, error) {
	if err := s.requireCredentialCipher(); err != nil {
		return credential.Envelope{}, err
	}
	envelope, err := s.credentialCipher.Seal(plaintext, aad)
	if err != nil {
		return credential.Envelope{}, fmt.Errorf("seal credential: %w", err)
	}
	return envelope, nil
}

func (s *Store) requireCredentialCipher() error {
	if err := s.validate(); err != nil {
		return err
	}
	if s.credentialCipher == nil {
		return ErrCredentialCipherUnavailable
	}
	return nil
}

func (s *Store) sealNodeCredential(id resource.NodeID, revision int64, input resource.NodeConnectionInput) (credential.Envelope, bool, error) {
	if input.Kind == resource.NodeKindDirect {
		return credential.Envelope{}, false, nil
	}
	envelope, err := s.sealCredential(input.ProxyCredential, nodeProxyAAD(id, input.Kind, revision))
	return envelope, err == nil, err
}

func validateNewAccount(platform resource.Platform, alias string) error {
	account := resource.PlatformAccount{ID: 1, Platform: platform, Alias: alias, SessionState: resource.AccountSessionStateUnverified, SessionRevision: 1}
	return account.Validate()
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
		return resource.NodeConnectionInput{}, err
	}
	input.ProxyCredential = append([]byte(nil), input.ProxyCredential...)
	return input, nil
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

func accountSessionAAD(id resource.AccountID, platform resource.Platform, revision int64) []byte {
	return []byte(fmt.Sprintf("buffgo.resource.account-session.v1\x00%d\x00%s\x00%d", id, platform, revision))
}

func nodeProxyAAD(id resource.NodeID, kind resource.NodeKind, revision int64) []byte {
	return []byte(fmt.Sprintf("buffgo.resource.node-proxy.v1\x00%d\x00%s\x00%d", id, kind, revision))
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

func envelopeDatabaseValues(envelope credential.Envelope, present bool) (any, any, any, any) {
	if !present {
		return nil, nil, nil, nil
	}
	return int64(envelope.Version), envelope.KeyID, envelope.Nonce, envelope.Ciphertext
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
	assigned               sql.NullString
	sticky                 sql.NullTime
	verifiedRevision       sql.NullInt64
	address                sql.NullString
	verifiedAt, validUntil sql.NullTime
}

func (data *nodeScanData) destinations() []any {
	return []any{
		&data.id, &data.node.Name, &data.node.Kind, &data.node.Region,
		&data.node.EgressMode, &data.node.State, &data.node.EgressRevision,
		&data.node.AssignmentRevision, &data.assigned, &data.node.HasProxyCredential, &data.sticky,
		&data.verifiedRevision, &data.address, &data.verifiedAt, &data.validUntil,
	}
}

func (data *nodeScanData) result() (resource.AccessNode, error) {
	data.node.ID = resource.NodeID(data.id)
	if data.assigned.Valid {
		data.node.AssignedPlatform = resource.Platform(data.assigned.String)
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
