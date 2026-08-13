// Package resource defines persisted account and network resource facts.
package resource

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"buff-go/internal/market"
)

// AccountID is the stable identity of one platform account.
type AccountID int64

// Validate rejects a missing account identity.
func (id AccountID) Validate() error {
	if id < 1 {
		return fmt.Errorf("account_id must be at least 1")
	}
	return nil
}

// NodeID is the stable identity of one access node.
type NodeID int64

// Validate rejects a missing node identity.
func (id NodeID) Validate() error {
	if id < 1 {
		return fmt.Errorf("node_id must be at least 1")
	}
	return nil
}

// Platform is an open platform identifier, not a fixed platform enumeration.
// Its stable syntax is ^[a-z][a-z0-9_.-]{0,31}$.
type Platform string

// Validate rejects empty, normalized, or non-ASCII platform identifiers.
func (platform Platform) Validate() error {
	value := string(platform)
	if len(value) < 1 || len(value) > 32 {
		return fmt.Errorf("platform must contain 1 to 32 ASCII bytes")
	}
	if value[0] < 'a' || value[0] > 'z' {
		return fmt.Errorf("platform must start with a lowercase ASCII letter")
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		alphanumeric := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		separator := character == '-' || character == '_' || character == '.'
		if !alphanumeric && !separator {
			return fmt.Errorf("platform must match ^[a-z][a-z0-9_.-]{0,31}$")
		}
	}
	return nil
}

// AccountSessionState describes the last check of the current session revision.
type AccountSessionState string

const (
	AccountSessionStateUnverified AccountSessionState = "unverified"
	AccountSessionStateValid      AccountSessionState = "valid"
	AccountSessionStateInvalid    AccountSessionState = "invalid"
)

// Validate rejects an unknown session state.
func (state AccountSessionState) Validate() error {
	switch state {
	case AccountSessionStateUnverified, AccountSessionStateValid, AccountSessionStateInvalid:
		return nil
	default:
		return fmt.Errorf("invalid account session state %q", state)
	}
}

// PlatformAccount is the credential-safe read model of one login account.
// SessionRevision changes whenever its write-only session is replaced.
type PlatformAccount struct {
	ID              AccountID
	Platform        Platform
	Alias           string
	SessionState    AccountSessionState
	SessionRevision int64
	LastCheckedAt   *time.Time
}

// Validate checks the persisted account facts without accessing its credential.
func (account PlatformAccount) Validate() error {
	if err := account.ID.Validate(); err != nil {
		return err
	}
	if err := account.Platform.Validate(); err != nil {
		return err
	}
	if err := validateLabel("account alias", account.Alias); err != nil {
		return err
	}
	if err := account.SessionState.Validate(); err != nil {
		return err
	}
	if account.SessionRevision < 1 {
		return fmt.Errorf("session_revision must be at least 1")
	}
	if account.SessionState == AccountSessionStateUnverified {
		if account.LastCheckedAt != nil {
			return fmt.Errorf("unverified session must not have last_checked_at")
		}
		return nil
	}
	if account.LastCheckedAt == nil || account.LastCheckedAt.IsZero() {
		return fmt.Errorf("checked session requires a non-zero last_checked_at")
	}
	return nil
}

// NodeKind selects either local direct access or a configured proxy.
type NodeKind string

const (
	NodeKindDirect NodeKind = "direct"
	NodeKindProxy  NodeKind = "proxy"
)

// Validate rejects an unknown node kind.
func (kind NodeKind) Validate() error {
	switch kind {
	case NodeKindDirect, NodeKindProxy:
		return nil
	default:
		return fmt.Errorf("invalid node kind %q", kind)
	}
}

// NodeRegion is the declared geographic capability of an access node.
type NodeRegion string

const (
	NodeRegionDomestic NodeRegion = "domestic"
	NodeRegionForeign  NodeRegion = "foreign"
	NodeRegionHongKong NodeRegion = "hongkong"
)

// Validate rejects unknown and legacy geographic values.
func (region NodeRegion) Validate() error {
	switch region {
	case NodeRegionDomestic, NodeRegionForeign, NodeRegionHongKong:
		return nil
	default:
		return fmt.Errorf("invalid node region %q", region)
	}
}

// TargetRegion is the geographic class required by a platform endpoint.
type TargetRegion string

const (
	TargetRegionDomestic TargetRegion = "domestic"
	TargetRegionForeign  TargetRegion = "foreign"
)

// Validate rejects an unknown target geographic class.
func (region TargetRegion) Validate() error {
	switch region {
	case TargetRegionDomestic, TargetRegionForeign:
		return nil
	default:
		return fmt.Errorf("invalid target region %q", region)
	}
}

// Allows reports whether the node region can access the target region.
func (region NodeRegion) Allows(target TargetRegion) bool {
	if region.Validate() != nil || target.Validate() != nil {
		return false
	}
	if region == NodeRegionHongKong {
		return true
	}
	return region == NodeRegionDomestic && target == TargetRegionDomestic ||
		region == NodeRegionForeign && target == TargetRegionForeign
}

// EgressMode describes how a node keeps its verified exit address stable.
type EgressMode string

const (
	EgressModeStatic EgressMode = "static"
	EgressModeSticky EgressMode = "sticky"
)

// Validate rejects dynamic or unknown egress modes.
func (mode EgressMode) Validate() error {
	switch mode {
	case EgressModeStatic, EgressModeSticky:
		return nil
	default:
		return fmt.Errorf("invalid egress mode %q", mode)
	}
}

// NodeState is the latest operational state of an access node.
type NodeState string

const (
	NodeStateValidating  NodeState = "validating"
	NodeStateAvailable   NodeState = "available"
	NodeStateUnavailable NodeState = "unavailable"
)

// Validate rejects an unknown operational state.
func (state NodeState) Validate() error {
	switch state {
	case NodeStateValidating, NodeStateAvailable, NodeStateUnavailable:
		return nil
	default:
		return fmt.Errorf("invalid node state %q", state)
	}
}

// ExitVerification is evidence for the actual exit address of one node revision.
type ExitVerification struct {
	VerifiedRevision int64
	Address          netip.Addr
	VerifiedAt       time.Time
	ValidUntil       time.Time
}

// Validate checks the identity and bounded validity window of exit evidence.
func (verification ExitVerification) Validate() error {
	if verification.VerifiedRevision < 1 {
		return fmt.Errorf("verified_revision must be at least 1")
	}
	if !verification.Address.IsValid() ||
		verification.Address.Is4In6() ||
		!verification.Address.IsGlobalUnicast() ||
		verification.Address.IsPrivate() ||
		isReservedIPv4(verification.Address) ||
		verification.Address.IsLoopback() ||
		verification.Address.IsMulticast() ||
		verification.Address.IsLinkLocalUnicast() ||
		verification.Address.IsLinkLocalMulticast() ||
		verification.Address.IsUnspecified() ||
		verification.Address.Zone() != "" {
		return fmt.Errorf("exit address must be a public unicast IP address without a zone")
	}
	if verification.VerifiedAt.IsZero() {
		return fmt.Errorf("verified_at is required")
	}
	if verification.ValidUntil.IsZero() || !verification.ValidUntil.After(verification.VerifiedAt) {
		return fmt.Errorf("valid_until must be after verified_at")
	}
	return nil
}

func isReservedIPv4(address netip.Addr) bool {
	if !address.Is4() {
		return false
	}
	octets := address.As4()
	return octets[0] == 0 ||
		(octets[0] == 100 && octets[1] >= 64 && octets[1] <= 127) ||
		octets[0] >= 240
}

// NodeSideAssignment binds one node to one platform direction inside its game.
type NodeSideAssignment struct {
	Platform Platform
	Side     market.Side
}

// Validate checks one direction assignment.
func (assignment NodeSideAssignment) Validate() error {
	if err := assignment.Platform.Validate(); err != nil {
		return fmt.Errorf("assignment platform: %w", err)
	}
	switch assignment.Side {
	case market.SideBid, market.SideAsk:
		return nil
	default:
		return fmt.Errorf("invalid assignment side %q", assignment.Side)
	}
}

// AccessNode is the credential-safe read model of one network exit.
// AppID zero means the node is not yet given to a game. Sides is the
// per-platform bid/ask assignment inside that game.
type AccessNode struct {
	ID                      NodeID
	Name                    string
	Kind                    NodeKind
	Region                  NodeRegion
	EgressMode              EgressMode
	State                   NodeState
	EgressRevision          int64
	AssignmentRevision      int64
	AppID                   int64
	Sides                   []NodeSideAssignment
	HasProxyCredential      bool
	StickySessionValidUntil *time.Time
	ExitVerification        *ExitVerification
}

// NodeConnectionInput is the complete write command for one node connection
// revision. ProxyCredential is write-only material and is not part of a safe
// resource read model.
type NodeConnectionInput struct {
	Kind                    NodeKind
	Region                  NodeRegion
	EgressMode              EgressMode
	StickySessionValidUntil *time.Time
	ProxyCredential         []byte
}

// Validate checks node configuration and current exit evidence invariants.
func (node AccessNode) Validate() error {
	if err := node.ID.Validate(); err != nil {
		return err
	}
	if err := validateLabel("node name", node.Name); err != nil {
		return err
	}
	if err := node.Kind.Validate(); err != nil {
		return err
	}
	if err := node.Region.Validate(); err != nil {
		return err
	}
	if err := node.EgressMode.Validate(); err != nil {
		return err
	}
	if err := node.State.Validate(); err != nil {
		return err
	}
	if node.EgressRevision < 1 {
		return fmt.Errorf("egress_revision must be at least 1")
	}
	if node.AssignmentRevision < 1 {
		return fmt.Errorf("assignment_revision must be at least 1")
	}
	if node.AppID < 0 {
		return fmt.Errorf("appid must be non-negative")
	}
	if node.AppID == 0 && len(node.Sides) > 0 {
		return fmt.Errorf("unassigned node must not have direction assignments")
	}
	seenPlatform := make(map[Platform]struct{}, len(node.Sides))
	for _, assignment := range node.Sides {
		if err := assignment.Validate(); err != nil {
			return err
		}
		if _, exists := seenPlatform[assignment.Platform]; exists {
			return fmt.Errorf("duplicate direction assignment for platform %q", assignment.Platform)
		}
		seenPlatform[assignment.Platform] = struct{}{}
	}

	switch node.Kind {
	case NodeKindDirect:
		if node.EgressMode != EgressModeStatic {
			return fmt.Errorf("direct node must use static egress mode")
		}
		if node.HasProxyCredential {
			return fmt.Errorf("direct node must not have proxy credential")
		}
		if node.StickySessionValidUntil != nil {
			return fmt.Errorf("direct node must not have sticky session deadline")
		}
	case NodeKindProxy:
		if !node.HasProxyCredential {
			return fmt.Errorf("proxy node requires proxy credential")
		}
	}

	if node.EgressMode == EgressModeSticky {
		if node.StickySessionValidUntil == nil || node.StickySessionValidUntil.IsZero() {
			return fmt.Errorf("sticky node requires a non-zero sticky session deadline")
		}
	} else if node.StickySessionValidUntil != nil {
		return fmt.Errorf("static node must not have sticky session deadline")
	}

	if node.ExitVerification != nil {
		if err := node.ExitVerification.Validate(); err != nil {
			return fmt.Errorf("exit verification: %w", err)
		}
		if node.StickySessionValidUntil != nil && node.ExitVerification.ValidUntil.After(*node.StickySessionValidUntil) {
			return fmt.Errorf("exit verification must not outlive sticky session")
		}
	}
	if node.State == NodeStateAvailable {
		if node.ExitVerification == nil {
			return fmt.Errorf("available node requires exit verification")
		}
		if node.ExitVerification.VerifiedRevision != node.EgressRevision {
			return fmt.Errorf("available node requires verification for current egress revision")
		}
	} else if node.ExitVerification != nil {
		return fmt.Errorf("non-available node must not have exit verification")
	}
	return nil
}

func validateLabel(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(value) > 128 {
		return fmt.Errorf("%s must not exceed 128 UTF-8 bytes", field)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", field)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have leading or trailing whitespace", field)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

// UsableAt reports whether current exit evidence permits dispatch at now.
func (node AccessNode) UsableAt(now time.Time) bool {
	if now.IsZero() || node.Validate() != nil || node.State != NodeStateAvailable || node.ExitVerification == nil {
		return false
	}
	verification := node.ExitVerification
	if verification.VerifiedRevision != node.EgressRevision || now.Before(verification.VerifiedAt) || !now.Before(verification.ValidUntil) {
		return false
	}
	return node.StickySessionValidUntil == nil || now.Before(*node.StickySessionValidUntil)
}

// SideFor returns the bid/ask assignment of one platform on this node.
func (node AccessNode) SideFor(platform Platform) (market.Side, bool) {
	for _, assignment := range node.Sides {
		if assignment.Platform == platform {
			return assignment.Side, true
		}
	}
	return "", false
}

// AssignedTo reports whether the node is given to this game, platform, and side.
func (node AccessNode) AssignedTo(appID int64, platform Platform, side market.Side) bool {
	if appID < 1 || node.AppID != appID {
		return false
	}
	assigned, ok := node.SideFor(platform)
	return ok && assigned == side
}
