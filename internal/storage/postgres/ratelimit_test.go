package postgres

import (
	"testing"
	"time"

	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

func TestHasRequiredRateLimitPolicies(t *testing.T) {
	platform := testRateLimitPolicy(1, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindMinInterval, MinInterval: time.Second,
	})
	profile := testRateLimitPolicy(2, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	if hasRequiredRateLimitPolicies([]ratelimit.Policy{platform}) {
		t.Fatal("platform budget without interface profile was accepted")
	}
	if hasRequiredRateLimitPolicies([]ratelimit.Policy{platform, profile}) {
		t.Fatal("platform budget and interface profile were accepted without an exact account-IP rate")
	}
	platform.Spec.Kind = ratelimit.KindCooldownOnly
	platform.Spec.MinInterval = 0
	if hasRequiredRateLimitPolicies([]ratelimit.Policy{platform, profile}) {
		t.Fatal("cooldown-only platform rule was treated as total rate budget")
	}
	accountIP := testRateLimitPolicy(3, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_account_ip", Scope: ratelimit.ScopeAccountIP,
		EndpointClass: "summary", Kind: ratelimit.KindMinInterval, MinInterval: time.Second,
	})
	if !hasRequiredRateLimitPolicies([]ratelimit.Policy{accountIP}) {
		t.Fatal("endpoint account-IP rate should provide both pacing and exact endpoint evidence")
	}
	accountIP.Spec.EndpointClass = ""
	if hasRequiredRateLimitPolicies([]ratelimit.Policy{accountIP}) {
		t.Fatal("generic account-IP rate without endpoint evidence was accepted")
	}
}

func TestReserveRateLimitStateStrictBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	policy := testRateLimitPolicy(1, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "window", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindRollingWindow, Window: time.Minute, MaxRequests: 2,
	})
	policy.ReadyAt = now.Add(-time.Hour)
	state := testRateLimitState(policy, now)
	state.RollingAdmittedAt = []time.Time{now.Add(-time.Minute), now.Add(-time.Minute + time.Microsecond)}
	state.LastAdmittedAt = timePointer(state.RollingAdmittedAt[1])

	candidate, blocker, err := reserveRateLimitState(policy, state, now)
	if err != nil || blocker != nil {
		t.Fatalf("boundary reserve blocker=%v err=%v", blocker, err)
	}
	want := []time.Time{now.Add(-time.Minute + time.Microsecond), now}
	if len(candidate.RollingAdmittedAt) != len(want) {
		t.Fatalf("rolling count=%d want=%d", len(candidate.RollingAdmittedAt), len(want))
	}
	for index := range want {
		if !candidate.RollingAdmittedAt[index].Equal(want[index]) {
			t.Fatalf("rolling[%d]=%s want=%s", index, candidate.RollingAdmittedAt[index], want[index])
		}
	}
	if len(state.RollingAdmittedAt) != 2 || !state.RollingAdmittedAt[0].Equal(now.Add(-time.Minute)) {
		t.Fatal("reservation mutated the input state")
	}
}

func TestReserveRateLimitStateChoosesLatestBlocker(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	policy := testRateLimitPolicy(1, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "interval", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindMinInterval, MinInterval: time.Second,
	})
	policy.ReadyAt = now.Add(2 * time.Second)
	state := testRateLimitState(policy, now)
	state.LastAdmittedAt = timePointer(now)
	state.NextAllowedAt = timePointer(now.Add(time.Second))
	state.CooldownObservedAt = timePointer(now.Add(-time.Second))
	state.CooldownUntil = timePointer(now.Add(3 * time.Second))
	state.CooldownReason = ratelimit.ReasonHTTP429

	_, blocker, err := reserveRateLimitState(policy, state, now)
	if err != nil {
		t.Fatal(err)
	}
	if blocker == nil || blocker.Reason() != ratelimit.BlockReasonCooldown ||
		!blocker.RetryAt().Equal(now.Add(3*time.Second)) {
		t.Fatalf("blocker=%v", blocker)
	}
}

func TestValidateRateLimitStateRejectsSemanticCorruption(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	policy := testRateLimitPolicy(1, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "interval", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindMinInterval, MinInterval: time.Second,
	})
	state := testRateLimitState(policy, now)
	state.LastAdmittedAt = timePointer(now)
	state.NextAllowedAt = timePointer(now.Add(2 * time.Second))
	if err := validateRateLimitState(state, policy); err == nil {
		t.Fatal("state with next_allowed_at unrelated to policy interval was accepted")
	}
}

func testRateLimitPolicy(id ratelimit.PolicyID, spec ratelimit.PolicySpec) ratelimit.Policy {
	return ratelimit.Policy{
		ID: id, Revision: 1, Enabled: true,
		ReadyAt: time.Date(2026, 8, 11, 11, 0, 0, 0, time.UTC), Spec: spec,
	}
}

func testRateLimitState(policy ratelimit.Policy, now time.Time) RateLimitState {
	return RateLimitState{
		StateID: 1, PolicyID: policy.ID, PolicyRevision: policy.Revision,
		Subject: ratelimit.Subject{Scope: policy.Spec.Scope, Platform: resource.Platform(policy.Spec.Platform)},
		Kind:    policy.Spec.Kind, ClockFloorAt: now,
	}
}
