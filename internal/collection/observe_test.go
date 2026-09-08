package collection

import (
	"net/netip"
	"testing"
	"time"

	"buff-go/internal/market"
	"buff-go/internal/resource"
)

func TestMapTargetReasonToStableCodes(t *testing.T) {
	cases := []struct {
		reason TargetReason
		want   BlockOrErr
	}{
		{TargetReasonSessionInvalid, BlockSessionInvalid},
		{TargetReasonEgressCNBlocked, BlockEgressCNBlocked},
		{TargetReasonEgressUnavailable, BlockEgressUnavailable},
		{TargetReasonNoCombination, BlockResourceIncomplete},
		{TargetReasonResourceIncomplete, BlockResourceIncomplete},
		{TargetReasonMissingRatePolicy, BlockResourceIncomplete},
		{TargetReasonInvalidConfig, BlockResourceIncomplete},
		{TargetReasonInterfaceUnverified, BlockResourceIncomplete},
		{TargetReasonNextCycle, ""},
		{TargetReasonCooldown, ""},
	}
	for _, test := range cases {
		if got := MapTargetReason(test.reason); got != test.want {
			t.Fatalf("MapTargetReason(%q) = %q, want %q", test.reason, got, test.want)
		}
	}
}

func TestMapWaitReasonToStableCodes(t *testing.T) {
	if got := MapWaitReason(WorkerWaitReasonRateLimit); got != BlockHTTP429 {
		t.Fatalf("rate_limit = %q", got)
	}
	if got := MapWaitReason(WorkerWaitReasonDeferred); got != BlockHTTP429 {
		t.Fatalf("deferred = %q", got)
	}
	if got := MapWaitReason(WorkerWaitReasonTimeout); got != BlockTimeout {
		t.Fatalf("timeout = %q", got)
	}
	if got := MapWaitReason(WorkerWaitReasonNetwork); got != "" {
		t.Fatalf("network should not invent a collect code, got %q", got)
	}
}

func TestRetryAfterSecAndDirection(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if got := RetryAfterSec(now.Add(1500*time.Millisecond), now); got != 2 {
		t.Fatalf("ceil 1.5s = %d", got)
	}
	if got := RetryAfterSec(now.Add(-time.Second), now); got != 0 {
		t.Fatalf("expired = %d", got)
	}
	if got := ObserveDirection(market.SideAsk); got != "sell" {
		t.Fatalf("ask = %q", got)
	}
	if got := ObserveDirection(market.SideBid); got != "buy" {
		t.Fatalf("bid = %q", got)
	}
}

func TestCombinationBlockReasonClassifiesSteamCN(t *testing.T) {
	now := scheduleNow()
	checked := now.Add(-time.Minute)
	base := resource.CombinationResources{
		Account: resource.PlatformAccount{
			ID: 1, Platform: "steam", Alias: "a",
			SessionState: resource.AccountSessionStateValid, SessionRevision: 1, LastCheckedAt: &checked,
		},
		Node: resource.AccessNode{
			ID: 2, Name: "n", Kind: resource.NodeKindDirect,
			Region: resource.NodeRegionDomestic, EgressMode: resource.EgressModeStatic,
			State: resource.NodeStateAvailable, EgressRevision: 1,
			ExitVerification: &resource.ExitVerification{
				VerifiedRevision: 1, Address: netip.MustParseAddr("8.8.8.8"),
				VerifiedAt: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour),
			},
		},
	}
	if got := combinationBlockReason(base, resource.TargetRegionForeign, now); got != TargetReasonEgressCNBlocked {
		t.Fatalf("cn steam = %q", got)
	}
	base.Node.Region = resource.NodeRegionForeign
	if got := combinationBlockReason(base, resource.TargetRegionDomestic, now); got != TargetReasonEgressUnavailable {
		t.Fatalf("foreign vs domestic = %q", got)
	}
	base.Node.State = resource.NodeStateValidating
	base.Node.ExitVerification = nil
	if got := combinationBlockReason(base, resource.TargetRegionForeign, now); got != TargetReasonResourceIncomplete {
		t.Fatalf("incomplete = %q", got)
	}
}
