package resource

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPlatformValidate(t *testing.T) {
	t.Parallel()

	valid := []Platform{
		"steam", "buff", "c5game", "market-1", "market_cn", "steam.api", "steam-", "steam.",
		Platform("a" + strings.Repeat("b", 31)),
	}
	for _, platform := range valid {
		platform := platform
		t.Run(string(platform), func(t *testing.T) {
			t.Parallel()
			if err := platform.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	invalid := []Platform{
		"", "Steam", " steam", "steam ", "蒸汽", "-steam", "1steam", "steam/api",
		Platform("a" + strings.Repeat("b", 32)),
	}
	for _, platform := range invalid {
		platform := platform
		t.Run("invalid_"+string(platform), func(t *testing.T) {
			t.Parallel()
			if err := platform.Validate(); err == nil {
				t.Fatalf("Validate() accepted %q", platform)
			}
		})
	}
}

func TestNodeRegionAllows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		region NodeRegion
		target TargetRegion
		want   bool
	}{
		{name: "domestic to domestic", region: NodeRegionDomestic, target: TargetRegionDomestic, want: true},
		{name: "domestic to foreign", region: NodeRegionDomestic, target: TargetRegionForeign, want: false},
		{name: "foreign to domestic", region: NodeRegionForeign, target: TargetRegionDomestic, want: false},
		{name: "foreign to foreign", region: NodeRegionForeign, target: TargetRegionForeign, want: true},
		{name: "hongkong to domestic", region: NodeRegionHongKong, target: TargetRegionDomestic, want: true},
		{name: "hongkong to foreign", region: NodeRegionHongKong, target: TargetRegionForeign, want: true},
		{name: "invalid node region", region: NodeRegion("cn"), target: TargetRegionDomestic, want: false},
		{name: "invalid target region", region: NodeRegionHongKong, target: TargetRegion("oversea"), want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.region.Allows(tt.target); got != tt.want {
				t.Fatalf("Allows(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestResourceEnumsRejectLegacyAndEmptyValues(t *testing.T) {
	t.Parallel()

	for _, value := range []NodeRegion{"", "cn", "oversea", "dual"} {
		if err := value.Validate(); err == nil {
			t.Errorf("NodeRegion(%q).Validate() succeeded", value)
		}
	}
	for _, value := range []TargetRegion{"", "cn", "oversea", "dual", "hongkong"} {
		if err := value.Validate(); err == nil {
			t.Errorf("TargetRegion(%q).Validate() succeeded", value)
		}
	}
}

func TestPlatformAccountValidate(t *testing.T) {
	t.Parallel()

	valid := PlatformAccount{
		ID:              1,
		Platform:        "steam",
		Alias:           "steam-main",
		SessionState:    AccountSessionStateValid,
		SessionRevision: 2,
		LastCheckedAt:   timePtr(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*PlatformAccount)
	}{
		{name: "invalid id", mutate: func(account *PlatformAccount) { account.ID = 0 }},
		{name: "invalid platform", mutate: func(account *PlatformAccount) { account.Platform = "Steam" }},
		{name: "empty alias", mutate: func(account *PlatformAccount) { account.Alias = "" }},
		{name: "alias with leading whitespace", mutate: func(account *PlatformAccount) { account.Alias = " steam" }},
		{name: "alias with trailing whitespace", mutate: func(account *PlatformAccount) { account.Alias = "steam " }},
		{name: "alias with control character", mutate: func(account *PlatformAccount) { account.Alias = "steam\nmain" }},
		{name: "alias with invalid utf8", mutate: func(account *PlatformAccount) { account.Alias = string([]byte{0xff}) }},
		{name: "alias too long", mutate: func(account *PlatformAccount) { account.Alias = strings.Repeat("a", 129) }},
		{name: "invalid state", mutate: func(account *PlatformAccount) { account.SessionState = "expired" }},
		{name: "missing revision", mutate: func(account *PlatformAccount) { account.SessionRevision = 0 }},
		{name: "valid without checked time", mutate: func(account *PlatformAccount) { account.LastCheckedAt = nil }},
		{name: "zero checked time", mutate: func(account *PlatformAccount) { account.LastCheckedAt = timePtr(time.Time{}) }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			account := valid
			tt.mutate(&account)
			if err := account.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}

	unverified := valid
	unverified.SessionState = AccountSessionStateUnverified
	unverified.LastCheckedAt = nil
	if err := unverified.Validate(); err != nil {
		t.Fatalf("unverified Validate() error = %v", err)
	}
	unverified.LastCheckedAt = timePtr(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	if err := unverified.Validate(); err == nil {
		t.Fatal("unverified account with checked time passed Validate()")
	}
}

func TestExitVerificationValidate(t *testing.T) {
	t.Parallel()

	verifiedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	valid := ExitVerification{
		VerifiedRevision: 3,
		Address:          netip.MustParseAddr("1.1.1.1"),
		VerifiedAt:       verifiedAt,
		ValidUntil:       verifiedAt.Add(time.Hour),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ExitVerification)
	}{
		{name: "missing revision", mutate: func(verification *ExitVerification) { verification.VerifiedRevision = 0 }},
		{name: "invalid address", mutate: func(verification *ExitVerification) { verification.Address = netip.Addr{} }},
		{name: "unspecified address", mutate: func(verification *ExitVerification) { verification.Address = netip.IPv4Unspecified() }},
		{name: "private address", mutate: func(verification *ExitVerification) { verification.Address = netip.MustParseAddr("10.0.0.1") }},
		{name: "loopback address", mutate: func(verification *ExitVerification) { verification.Address = netip.MustParseAddr("127.0.0.1") }},
		{name: "multicast address", mutate: func(verification *ExitVerification) { verification.Address = netip.MustParseAddr("224.0.0.1") }},
		{name: "link local address", mutate: func(verification *ExitVerification) { verification.Address = netip.MustParseAddr("169.254.1.1") }},
		{name: "zoned address", mutate: func(verification *ExitVerification) { verification.Address = netip.MustParseAddr("fe80::1%en0") }},
		{name: "ipv4 mapped address", mutate: func(verification *ExitVerification) { verification.Address = netip.MustParseAddr("::ffff:1.1.1.1") }},
		{name: "this network address", mutate: func(verification *ExitVerification) { verification.Address = netip.MustParseAddr("0.1.2.3") }},
		{name: "carrier grade nat address", mutate: func(verification *ExitVerification) { verification.Address = netip.MustParseAddr("100.64.0.1") }},
		{name: "reserved high address", mutate: func(verification *ExitVerification) { verification.Address = netip.MustParseAddr("240.0.0.1") }},
		{name: "zero verified time", mutate: func(verification *ExitVerification) { verification.VerifiedAt = time.Time{} }},
		{name: "zero valid until", mutate: func(verification *ExitVerification) { verification.ValidUntil = time.Time{} }},
		{name: "equal times", mutate: func(verification *ExitVerification) { verification.ValidUntil = verification.VerifiedAt }},
		{name: "reversed times", mutate: func(verification *ExitVerification) {
			verification.ValidUntil = verification.VerifiedAt.Add(-time.Second)
		}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			verification := valid
			tt.mutate(&verification)
			if err := verification.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestAccessNodeValidate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	valid := AccessNode{
		ID:                      1,
		Name:                    "hk-proxy-1",
		Kind:                    NodeKindProxy,
		Region:                  NodeRegionHongKong,
		EgressMode:              EgressModeSticky,
		State:                   NodeStateAvailable,
		EgressRevision:          4,
		AssignmentRevision:      2,
		AssignedPlatform:        "buff",
		HasProxyCredential:      true,
		StickySessionValidUntil: timePtr(now.Add(2 * time.Hour)),
		ExitVerification: &ExitVerification{
			VerifiedRevision: 4,
			Address:          netip.MustParseAddr("8.8.8.8"),
			VerifiedAt:       now,
			ValidUntil:       now.Add(time.Hour),
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*AccessNode)
	}{
		{name: "invalid id", mutate: func(node *AccessNode) { node.ID = 0 }},
		{name: "empty name", mutate: func(node *AccessNode) { node.Name = "" }},
		{name: "name with leading whitespace", mutate: func(node *AccessNode) { node.Name = " hk" }},
		{name: "name with trailing whitespace", mutate: func(node *AccessNode) { node.Name = "hk " }},
		{name: "name with control character", mutate: func(node *AccessNode) { node.Name = "hk\nproxy" }},
		{name: "name with invalid utf8", mutate: func(node *AccessNode) { node.Name = string([]byte{0xff}) }},
		{name: "name too long", mutate: func(node *AccessNode) { node.Name = strings.Repeat("a", 129) }},
		{name: "invalid kind", mutate: func(node *AccessNode) { node.Kind = "local" }},
		{name: "invalid region", mutate: func(node *AccessNode) { node.Region = "dual" }},
		{name: "invalid egress mode", mutate: func(node *AccessNode) { node.EgressMode = "dynamic" }},
		{name: "invalid state", mutate: func(node *AccessNode) { node.State = "healthy" }},
		{name: "missing revision", mutate: func(node *AccessNode) { node.EgressRevision = 0 }},
		{name: "missing assignment revision", mutate: func(node *AccessNode) { node.AssignmentRevision = 0 }},
		{name: "invalid assignment", mutate: func(node *AccessNode) { node.AssignedPlatform = "BUFF" }},
		{name: "proxy without credential", mutate: func(node *AccessNode) { node.HasProxyCredential = false }},
		{name: "sticky without deadline", mutate: func(node *AccessNode) { node.StickySessionValidUntil = nil }},
		{name: "sticky deadline before verification", mutate: func(node *AccessNode) {
			node.StickySessionValidUntil = timePtr(now.Add(30 * time.Minute))
		}},
		{name: "available without verification", mutate: func(node *AccessNode) { node.ExitVerification = nil }},
		{name: "available with stale revision", mutate: func(node *AccessNode) {
			node.ExitVerification = cloneVerification(node.ExitVerification)
			node.ExitVerification.VerifiedRevision--
		}},
		{name: "validating with current verification", mutate: func(node *AccessNode) { node.State = NodeStateValidating }},
		{name: "unavailable with current verification", mutate: func(node *AccessNode) { node.State = NodeStateUnavailable }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			node := cloneNode(valid)
			tt.mutate(&node)
			if err := node.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestAccessNodeDirectConstraints(t *testing.T) {
	t.Parallel()

	valid := AccessNode{
		ID:                 2,
		Name:               "local",
		Kind:               NodeKindDirect,
		Region:             NodeRegionDomestic,
		EgressMode:         EgressModeStatic,
		State:              NodeStateValidating,
		EgressRevision:     1,
		AssignmentRevision: 1,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*AccessNode)
	}{
		{name: "sticky direct", mutate: func(node *AccessNode) {
			node.EgressMode = EgressModeSticky
			node.StickySessionValidUntil = timePtr(time.Now().Add(time.Hour))
		}},
		{name: "direct with proxy credential", mutate: func(node *AccessNode) { node.HasProxyCredential = true }},
		{name: "direct with sticky deadline", mutate: func(node *AccessNode) {
			node.StickySessionValidUntil = timePtr(time.Now().Add(time.Hour))
		}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			node := valid
			tt.mutate(&node)
			if err := node.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestAccessNodeUsableAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	node := AccessNode{
		ID:                 1,
		Name:               "direct-1",
		Kind:               NodeKindDirect,
		Region:             NodeRegionForeign,
		EgressMode:         EgressModeStatic,
		State:              NodeStateAvailable,
		EgressRevision:     2,
		AssignmentRevision: 1,
		ExitVerification: &ExitVerification{
			VerifiedRevision: 2,
			Address:          netip.MustParseAddr("2606:4700:4700::1111"),
			VerifiedAt:       now,
			ValidUntil:       now.Add(time.Hour),
		},
	}

	if !node.UsableAt(now) {
		t.Fatal("UsableAt() = false at verified_at")
	}
	if !node.UsableAt(now.Add(30 * time.Minute)) {
		t.Fatal("UsableAt() = false during validity window")
	}
	if node.UsableAt(now.Add(-time.Nanosecond)) {
		t.Fatal("UsableAt() = true before verified_at")
	}
	if node.UsableAt(now.Add(time.Hour)) {
		t.Fatal("UsableAt() = true at valid_until")
	}

	stale := cloneNode(node)
	stale.ExitVerification.VerifiedRevision = 1
	if stale.UsableAt(now.Add(time.Minute)) {
		t.Fatal("UsableAt() = true with revision mismatch")
	}

	validating := cloneNode(node)
	validating.State = NodeStateValidating
	if validating.UsableAt(now.Add(time.Minute)) {
		t.Fatal("UsableAt() = true while validating")
	}

	unavailable := cloneNode(node)
	unavailable.State = NodeStateUnavailable
	if unavailable.UsableAt(now.Add(time.Minute)) {
		t.Fatal("UsableAt() = true while unavailable")
	}
}

func TestResourceReadModelsDoNotContainCredentialMaterial(t *testing.T) {
	t.Parallel()

	for _, model := range []reflect.Type{
		reflect.TypeOf(PlatformAccount{}),
		reflect.TypeOf(AccessNode{}),
	} {
		for _, forbidden := range []string{
			"Cookie", "Session", "SessionCiphertext", "ProxyPassword", "ProxyAuth",
			"ProxyCredential", "CredentialCiphertext", "Nonce", "EncryptionKey",
		} {
			if _, found := model.FieldByName(forbidden); found {
				t.Errorf("%s exposes forbidden field %s", model.Name(), forbidden)
			}
		}
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func cloneNode(node AccessNode) AccessNode {
	node.ExitVerification = cloneVerification(node.ExitVerification)
	if node.StickySessionValidUntil != nil {
		deadline := *node.StickySessionValidUntil
		node.StickySessionValidUntil = &deadline
	}
	return node
}

func cloneVerification(verification *ExitVerification) *ExitVerification {
	if verification == nil {
		return nil
	}
	copy := *verification
	return &copy
}
