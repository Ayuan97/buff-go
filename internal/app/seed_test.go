package app

import (
	"context"
	"testing"
	"time"

	"buff-go/internal/ratelimit"
)

type seedOperation struct {
	kind    string
	key     ratelimit.RuleKey
	enabled bool
}

type fakeRateLimitPolicyStore struct {
	policies map[ratelimit.RuleKey]ratelimit.Policy
	nextID   ratelimit.PolicyID
	changes  []seedOperation
}

func (store *fakeRateLimitPolicyStore) ListRateLimitPolicies(context.Context) ([]ratelimit.Policy, error) {
	policies := make([]ratelimit.Policy, 0, len(store.policies))
	for _, policy := range store.policies {
		policies = append(policies, policy)
	}
	return policies, nil
}

func (store *fakeRateLimitPolicyStore) CreateRateLimitPolicy(
	_ context.Context,
	spec ratelimit.PolicySpec,
	enabled bool,
) (ratelimit.Policy, error) {
	store.nextID++
	policy := ratelimit.Policy{ID: store.nextID, Revision: 1, Enabled: enabled, Spec: spec}
	store.policies[spec.RuleKey] = policy
	store.changes = append(store.changes, seedOperation{kind: "create", key: spec.RuleKey, enabled: enabled})
	return policy, nil
}

func (store *fakeRateLimitPolicyStore) ReplaceRateLimitPolicy(
	_ context.Context,
	id ratelimit.PolicyID,
	expectedRevision int64,
	spec ratelimit.PolicySpec,
	enabled bool,
) (ratelimit.Policy, error) {
	policy := store.policies[spec.RuleKey]
	policy.ID = id
	policy.Revision = expectedRevision + 1
	policy.Enabled = enabled
	policy.Spec = spec
	store.policies[spec.RuleKey] = policy
	store.changes = append(store.changes, seedOperation{kind: "replace", key: spec.RuleKey, enabled: enabled})
	return policy, nil
}

func TestSteamRateLimitSeedSpecs(t *testing.T) {
	specs := steamRateLimitSpecs()
	if len(specs) != 2 {
		t.Fatalf("len=%d", len(specs))
	}
	wantEndpoints := map[ratelimit.EndpointClass]bool{
		"market_summary":   false,
		"market_orderbook": false,
	}
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			t.Fatalf("%s: %v", spec.RuleKey, err)
		}
		if spec.Platform != "steam" {
			t.Fatalf("platform=%q", spec.Platform)
		}
		if spec.Scope != ratelimit.ScopeAccountIP || spec.Kind != ratelimit.KindMinInterval {
			t.Fatalf("scope=%q kind=%q", spec.Scope, spec.Kind)
		}
		if spec.MinInterval != steamRequestInterval || spec.DefaultCooldown != time.Minute {
			t.Fatalf("interval=%s cooldown=%s", spec.MinInterval, spec.DefaultCooldown)
		}
		if _, found := wantEndpoints[spec.EndpointClass]; !found {
			t.Fatalf("endpoint=%q", spec.EndpointClass)
		}
		wantEndpoints[spec.EndpointClass] = true
	}
	for endpoint, found := range wantEndpoints {
		if !found {
			t.Fatalf("missing endpoint %q", endpoint)
		}
	}
	newKeys := make(map[ratelimit.RuleKey]struct{}, len(specs))
	for _, spec := range specs {
		newKeys[spec.RuleKey] = struct{}{}
	}
	for _, key := range obsoleteSteamRateLimitRuleKeys {
		if _, found := newKeys[key]; found {
			t.Fatalf("obsolete key %q is still active", key)
		}
	}
}

func TestSeedSteamRateLimitsEnablesExactRulesBeforeDisablingOldBuckets(t *testing.T) {
	store := &fakeRateLimitPolicyStore{policies: make(map[ratelimit.RuleKey]ratelimit.Policy), nextID: 3}
	oldSpecs := []ratelimit.PolicySpec{
		{Platform: "steam", RuleKey: "local_pacing", Scope: ratelimit.ScopePlatform, Kind: ratelimit.KindMinInterval, MinInterval: steamRequestInterval},
		{Platform: "steam", RuleKey: "search_render_429", Scope: ratelimit.ScopeInterface, EndpointClass: "market_summary", Kind: ratelimit.KindCooldownOnly},
		{Platform: "steam", RuleKey: "orderbook_unexhausted", Scope: ratelimit.ScopeInterface, EndpointClass: "market_orderbook", Kind: ratelimit.KindCooldownOnly},
	}
	for index, spec := range oldSpecs {
		store.policies[spec.RuleKey] = ratelimit.Policy{
			ID: ratelimit.PolicyID(index + 1), Revision: 1, Enabled: true, Spec: spec,
		}
	}
	if err := seedSteamRateLimits(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	if len(store.changes) != 5 {
		t.Fatalf("changes=%+v", store.changes)
	}
	for index, spec := range steamRateLimitSpecs() {
		change := store.changes[index]
		if change.kind != "create" || change.key != spec.RuleKey || !change.enabled {
			t.Fatalf("new rule change[%d]=%+v", index, change)
		}
	}
	for index, key := range obsoleteSteamRateLimitRuleKeys {
		change := store.changes[len(steamRateLimitSpecs())+index]
		if change.kind != "replace" || change.key != key || change.enabled {
			t.Fatalf("obsolete rule change[%d]=%+v", index, change)
		}
	}
	before := len(store.changes)
	if err := seedSteamRateLimits(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	if len(store.changes) != before {
		t.Fatalf("idempotent seed added changes: %+v", store.changes[before:])
	}
}
