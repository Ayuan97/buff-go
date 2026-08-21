// Package resource defines persisted account and network resource facts.
package resource

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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

var platformTargetRegions = map[Platform]TargetRegion{
	"steam": TargetRegionForeign,
	"buff":  TargetRegionDomestic,
	"igxe":  TargetRegionDomestic,
}

// TargetRegionForPlatform returns the site region required by a known platform.
// Unknown platforms intentionally have no regional restriction.
func TargetRegionForPlatform(platform Platform) (TargetRegion, bool) {
	target, known := platformTargetRegions[platform]
	return target, known
}

// PlatformTargetRegions returns a copy of the known platform site regions.
func PlatformTargetRegions() map[Platform]TargetRegion {
	regions := make(map[Platform]TargetRegion, len(platformTargetRegions))
	for platform, target := range platformTargetRegions {
		regions[platform] = target
	}
	return regions
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

// AccessNode is the credential-safe read model of one network exit.
type AccessNode struct {
	ID                      NodeID
	Name                    string
	Kind                    NodeKind
	Region                  NodeRegion
	EgressMode              EgressMode
	State                   NodeState
	EgressRevision          int64
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

// ValidateProxyCredential checks a proxy URL supported by net/http.Transport.
// The scheme must be explicit; Transport supplies the port when it is omitted.
func ValidateProxyCredential(credential []byte) error {
	if len(credential) == 0 {
		return fmt.Errorf("proxy URL is required")
	}
	if !utf8.Valid(credential) {
		return fmt.Errorf("proxy URL must be valid UTF-8")
	}
	raw := string(credential)
	if strings.TrimSpace(raw) != raw {
		return fmt.Errorf("proxy URL must not have leading or trailing whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("proxy URL is invalid")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("proxy URL scheme must be http, https, socks5, or socks5h")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("proxy URL host is required")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return fmt.Errorf("proxy URL port must be between 1 and 65535")
	}
	if parsed.Port() == "" {
		return nil
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("proxy URL port must be between 1 and 65535")
	}
	return nil
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
