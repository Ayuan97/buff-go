package ratelimit

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"buff-go/internal/resource"
)

type leaseRepository struct {
	resource.CoordinatorRepository
	resources resource.CombinationResources
}

func (repository *leaseRepository) CombinationResources(
	_ context.Context,
	id resource.CombinationID,
) (resource.CombinationResources, bool, error) {
	if repository.resources.Combination.ID != id {
		return resource.CombinationResources{}, false, nil
	}
	return repository.resources, true, nil
}

func TestPolicySpecValidate(t *testing.T) {
	base := PolicySpec{
		Platform:    "steam",
		RuleKey:     "platform_total",
		Scope:       ScopePlatform,
		Kind:        KindMinInterval,
		MinInterval: time.Second,
	}
	for _, test := range []struct {
		name   string
		mutate func(*PolicySpec)
	}{
		{name: "minimum interval"},
		{name: "rolling window", mutate: func(value *PolicySpec) {
			value.Kind = KindRollingWindow
			value.MinInterval = 0
			value.Window = time.Minute
			value.MaxRequests = 80
		}},
		{name: "cooldown only", mutate: func(value *PolicySpec) {
			value.Kind = KindCooldownOnly
			value.MinInterval = 0
			value.DefaultCooldown = 3 * time.Minute
		}},
		{name: "interface", mutate: func(value *PolicySpec) {
			value.Scope = ScopeInterface
			value.EndpointClass = "market_summary"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := base
			if test.mutate != nil {
				test.mutate(&value)
			}
			if err := value.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*PolicySpec)
	}{
		{name: "bad platform", mutate: func(value *PolicySpec) { value.Platform = "Steam" }},
		{name: "bad rule key", mutate: func(value *PolicySpec) { value.RuleKey = "platform total" }},
		{name: "unknown scope", mutate: func(value *PolicySpec) { value.Scope = "node" }},
		{name: "interface without class", mutate: func(value *PolicySpec) { value.Scope = ScopeInterface }},
		{name: "class outside interface", mutate: func(value *PolicySpec) { value.EndpointClass = "summary" }},
		{name: "unknown kind", mutate: func(value *PolicySpec) { value.Kind = "bucket" }},
		{name: "missing interval", mutate: func(value *PolicySpec) { value.MinInterval = 0 }},
		{name: "interval with rolling fields", mutate: func(value *PolicySpec) {
			value.Window = time.Minute
			value.MaxRequests = 1
		}},
		{name: "sub-microsecond interval", mutate: func(value *PolicySpec) { value.MinInterval = time.Nanosecond }},
		{name: "overlong interval", mutate: func(value *PolicySpec) { value.MinInterval = MaxDuration + time.Microsecond }},
		{name: "rolling without count", mutate: func(value *PolicySpec) {
			value.Kind = KindRollingWindow
			value.MinInterval = 0
			value.Window = time.Minute
		}},
		{name: "rolling over implementation bound", mutate: func(value *PolicySpec) {
			value.Kind = KindRollingWindow
			value.MinInterval = 0
			value.Window = time.Minute
			value.MaxRequests = MaxRollingRequests + 1
		}},
		{name: "cooldown only with quota", mutate: func(value *PolicySpec) {
			value.Kind = KindCooldownOnly
			value.MinInterval = 0
			value.MaxRequests = 1
		}},
		{name: "negative fallback", mutate: func(value *PolicySpec) { value.DefaultCooldown = -time.Second }},
		{name: "sub-microsecond fallback", mutate: func(value *PolicySpec) { value.DefaultCooldown = time.Nanosecond }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid policy")
			}
		})
	}

	rolling := base
	rolling.Kind = KindRollingWindow
	rolling.MinInterval = 0
	rolling.Window = 5 * time.Minute
	rolling.MaxRequests = 4
	if got, err := rolling.WarmupDuration(); err != nil || got != rolling.Window {
		t.Fatalf("WarmupDuration() = %v, %v", got, err)
	}
	changedTerms := rolling
	changedTerms.Window = time.Minute
	if !rolling.SameIdentity(changedTerms) {
		t.Fatal("pacing term replacement changed policy identity")
	}
	changedScope := rolling
	changedScope.Scope = ScopeAccount
	if rolling.SameIdentity(changedScope) {
		t.Fatal("scope replacement preserved policy identity")
	}
}

func TestPolicyValidate(t *testing.T) {
	value := testPolicy(1, ScopePlatform, time.Minute)
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Policy){
		func(value *Policy) { value.ID = 0 },
		func(value *Policy) { value.Revision = 0 },
		func(value *Policy) { value.ReadyAt = time.Time{} },
		func(value *Policy) { value.ReadyAt = value.ReadyAt.Add(time.Nanosecond) },
	} {
		invalid := value
		mutate(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatal("Validate() accepted invalid persisted policy")
		}
	}
}

func TestRequestFromLeaseAndSubjects(t *testing.T) {
	lease := testCombinationLease(t, "steam", 11, 21, 31, netip.MustParseAddr("1.1.1.1"))
	request, err := RequestFromLease(lease, "market_summary")
	if err != nil {
		t.Fatal(err)
	}
	now := lease.Snapshot.ExitVerifiedAt.Add(time.Minute)
	if err := request.ValidateAt(now); err != nil {
		t.Fatal(err)
	}

	want := map[Scope]Subject{
		ScopePlatform:  {Scope: ScopePlatform, Platform: "steam"},
		ScopeInterface: {Scope: ScopeInterface, Platform: "steam", EndpointClass: "market_summary"},
		ScopeAccount:   {Scope: ScopeAccount, Platform: "steam", AccountID: 21},
		ScopeIP:        {Scope: ScopeIP, Platform: "steam", ExitAddress: netip.MustParseAddr("1.1.1.1")},
		ScopeAccountIP: {Scope: ScopeAccountIP, Platform: "steam", AccountID: 21, ExitAddress: netip.MustParseAddr("1.1.1.1")},
	}
	for scope, expected := range want {
		got, err := request.Subject(scope)
		if err != nil {
			t.Fatalf("Subject(%q) error = %v", scope, err)
		}
		if got != expected {
			t.Fatalf("Subject(%q) = %+v, want %+v", scope, got, expected)
		}
	}
	if _, err := request.Subject("node"); err == nil {
		t.Fatal("unknown subject scope was accepted")
	}

	otherLease := testCombinationLease(t, "steam", 12, 21, 32, netip.MustParseAddr("1.1.1.1"))
	otherRequest, err := RequestFromLease(otherLease, "market_summary")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []Scope{ScopePlatform, ScopeInterface, ScopeAccount, ScopeIP, ScopeAccountIP} {
		left, leftErr := request.Subject(scope)
		right, rightErr := otherRequest.Subject(scope)
		if leftErr != nil || rightErr != nil || left != right {
			t.Fatalf("node or combination identity leaked into %s rate key", scope)
		}
	}
	differentPlatform := testCombinationLease(t, "buff", 13, 21, 33, netip.MustParseAddr("1.1.1.1"))
	platformRequest, err := RequestFromLease(differentPlatform, "market_summary")
	if err != nil {
		t.Fatal(err)
	}
	if request == platformRequest {
		t.Fatal("platform namespace was omitted from rate key")
	}
}

func TestRequestFromLeaseRejectsIncompleteOrUnusableSnapshot(t *testing.T) {
	coordinator, base := testCombinationLeaseWithCoordinator(t, "steam", 11, 21, 31, netip.MustParseAddr("1.1.1.1"))
	for _, test := range []struct {
		name   string
		mutate func(*resource.Lease)
	}{
		{name: "maintenance kind", mutate: func(value *resource.Lease) { value.Kind = resource.LeaseKindNodeMaintenance }},
		{name: "missing token", mutate: func(value *resource.Lease) { value.Token = resource.LeaseToken{} }},
		{name: "missing component", mutate: func(value *resource.Lease) { value.Component = resource.ComponentID{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if _, err := RequestFromLease(value, "market_summary"); err == nil {
				t.Fatal("RequestFromLease() accepted invalid lease")
			}
		})
	}
	if _, err := RequestFromLease(resource.Lease{}, "market_summary"); err == nil {
		t.Fatal("RequestFromLease() accepted a zero lease")
	}

	mutated := base
	mutated.Snapshot.Platform = "buff"
	mutated.Snapshot.AccountID = 999
	mutated.Snapshot.ExitAddress = netip.MustParseAddr("8.8.8.8")
	request, err := RequestFromLease(mutated, "market_summary")
	if err != nil {
		t.Fatal(err)
	}
	if request.Platform() != "steam" || request.AccountID() != 21 || request.ExitAddress() != netip.MustParseAddr("1.1.1.1") {
		t.Fatal("caller-mutated safe snapshot replaced authoritative lease identity")
	}

	if _, err := RequestFromLease(base, "market summary"); err == nil {
		t.Fatal("RequestFromLease() accepted invalid endpoint class")
	}
	request, err = RequestFromLease(base, "market_summary")
	if err != nil {
		t.Fatal(err)
	}
	if err := request.ValidateAt(base.Snapshot.ExitVerifiedAt.Add(-time.Microsecond)); err == nil {
		t.Fatal("request accepted time before verification")
	}
	if err := request.ValidateAt(base.Snapshot.ExitValidUntil); err == nil {
		t.Fatal("request accepted expired verification")
	}
	if err := coordinator.Release(base.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := RequestFromLease(base, "market_summary"); err == nil {
		t.Fatal("RequestFromLease() accepted a released lease")
	}
	signer, err := NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	rule, err := AppliedRuleFromPolicy(testPolicy(99, ScopePlatform, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Issue(request, []AppliedRule{rule}, base.Snapshot.ExitVerifiedAt.Add(time.Minute)); err == nil {
		t.Fatal("Issue() accepted a cached request after lease release")
	}
}

func TestRequestFromLeaseRejectsComponentCanceledLease(t *testing.T) {
	coordinator, lease := testCombinationLeaseWithCoordinator(t, "steam", 41, 51, 61, netip.MustParseAddr("1.1.1.1"))
	canceled := make(chan error, 1)
	go func() {
		canceled <- coordinator.CancelComponent(context.Background(), lease.Component)
	}()
	select {
	case <-lease.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("component cancellation did not cancel lease context")
	}
	if _, err := RequestFromLease(lease, "market_summary"); err == nil {
		t.Fatal("RequestFromLease() accepted a component-canceled lease")
	}
	if err := coordinator.Release(lease.Token); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-canceled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("component cancellation did not finish after release")
	}
}

func TestAdmissionSignerRejectsForgeryAndMutation(t *testing.T) {
	signer, admission := testAdmission(t)
	if err := signer.Verify(admission); err != nil {
		t.Fatal(err)
	}
	otherSigner, err := NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	if err := otherSigner.Verify(admission); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("other signer Verify() error = %v", err)
	}
	if _, err := otherSigner.Allow(admission); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("other signer Allow() error = %v", err)
	}
	if _, err := new(Signer).Issue(admission.request, admission.rules, admission.admittedAt); err == nil {
		t.Fatal("zero-value signer issued a ticket")
	}

	rules := admission.Rules()
	rules[0].policyID = 999
	if err := signer.Verify(admission); err != nil {
		t.Fatalf("mutating Rules() result changed ticket: %v", err)
	}
	for _, mutate := range []func(*Admission){
		func(value *Admission) { value.request.accountID++ },
		func(value *Admission) { value.rules[0].policyID++ },
		func(value *Admission) { value.admittedAt = value.admittedAt.Add(time.Nanosecond) },
		func(value *Admission) { value.nonce[0] ^= 0xff },
		func(value *Admission) { value.seal[0] ^= 0xff },
	} {
		forged := admission
		forged.rules = append([]AppliedRule(nil), admission.rules...)
		mutate(&forged)
		if err := signer.Verify(forged); !errors.Is(err, ErrInvalidAdmission) {
			t.Fatalf("Verify(forged) error = %v", err)
		}
	}
	if _, err := signer.Issue(admission.request, []AppliedRule{admission.rules[0], admission.rules[0]}, admission.admittedAt); err == nil {
		t.Fatal("Issue() accepted duplicate policy")
	}
	inputRules := admission.Rules()
	copiedAdmission, err := signer.Issue(admission.request, inputRules, admission.admittedAt.Add(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	inputRules[0].policyID = 999
	if err := signer.Verify(copiedAdmission); err != nil {
		t.Fatalf("mutating Issue() input changed ticket: %v", err)
	}

	wrongPlatform := testPolicy(10, ScopePlatform, time.Minute)
	wrongPlatform.Spec.Platform = "buff"
	wrongPlatformRule, err := AppliedRuleFromPolicy(wrongPlatform)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Issue(admission.request, []AppliedRule{wrongPlatformRule}, admission.admittedAt); err == nil {
		t.Fatal("Issue() accepted a policy for another platform")
	}
	wrongInterface := testPolicy(11, ScopeInterface, time.Minute)
	wrongInterface.Spec.EndpointClass = "catalog"
	wrongInterfaceRule, err := AppliedRuleFromPolicy(wrongInterface)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Issue(admission.request, []AppliedRule{wrongInterfaceRule}, admission.admittedAt); err == nil {
		t.Fatal("Issue() accepted a policy for another interface")
	}
	decision, err := signer.Allow(admission)
	if err != nil {
		t.Fatal(err)
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionCopiesFreezeFeedbackConcurrently(t *testing.T) {
	signer, admission := testAdmission(t)
	const attempts = 32
	results := make(chan Feedback, attempts)
	errorsFound := make(chan error, attempts)
	for index := 0; index < attempts; index++ {
		copyOfAdmission := admission
		observedAt := admission.AdmittedAt().Add(time.Duration(index+1) * time.Microsecond)
		go func() {
			feedback, err := signer.Feedback(copyOfAdmission, []Scope{ScopeIP}, ReasonHTTP429, observedAt, time.Minute)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- feedback
		}()
	}
	var frozen time.Time
	for index := 0; index < attempts; index++ {
		select {
		case err := <-errorsFound:
			t.Fatal(err)
		case feedback := <-results:
			if err := signer.VerifyFeedback(feedback); err != nil {
				t.Fatal(err)
			}
			if frozen.IsZero() {
				frozen = feedback.ObservedAt()
			} else if !feedback.ObservedAt().Equal(frozen) {
				t.Fatalf("Admission copies froze different observations: %v != %v", feedback.ObservedAt(), frozen)
			}
		}
	}
}

func TestAdmissionConcurrentConflictingFeedbackHasOneWinner(t *testing.T) {
	signer, admission := testAdmission(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, reason := range []ReasonCode{ReasonHTTP429, ReasonRiskControl} {
		reason := reason
		go func() {
			<-start
			_, err := signer.Feedback(
				admission,
				[]Scope{ScopeIP},
				reason,
				admission.AdmittedAt().Add(time.Second),
				time.Minute,
			)
			results <- err
		}()
	}
	close(start)
	var successes, conflicts int
	for index := 0; index < 2; index++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrFeedbackConflict):
			conflicts++
		default:
			t.Fatalf("Feedback() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestCapabilitiesHaveNoExportedState(t *testing.T) {
	for _, value := range []any{Request{}, AppliedRule{}, Admission{}, Blocker{}, Decision{}, Feedback{}} {
		typeOf := reflect.TypeOf(value)
		for index := 0; index < typeOf.NumField(); index++ {
			field := typeOf.Field(index)
			if field.IsExported() {
				t.Errorf("%s.%s is exported", typeOf.Name(), field.Name)
			}
		}
	}
}

func TestFeedbackIsScopedAndReplayStable(t *testing.T) {
	signer, admission := testAdmission(t)
	observedAt := admission.AdmittedAt().Add(2 * time.Second)
	inputScopes := []Scope{ScopeIP}
	feedback, err := signer.Feedback(admission, inputScopes, ReasonHTTP429, observedAt, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	inputScopes[0] = ScopeAccount
	if err := signer.VerifyFeedback(feedback); err != nil {
		t.Fatalf("mutating Feedback() input changed feedback: %v", err)
	}
	first, err := feedback.CooldownUntil(3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := feedback.CooldownUntil(3)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(observedAt.Add(time.Minute)) || !second.Equal(first) {
		t.Fatalf("replayed feedback deadlines = %v, %v", first, second)
	}
	scopes := feedback.Scopes()
	scopes[0] = ScopeAccount
	if feedback.Scopes()[0] != ScopeIP {
		t.Fatal("mutating Scopes() result changed feedback")
	}

	retried, err := signer.Feedback(admission, []Scope{ScopeIP}, ReasonHTTP429, observedAt.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !retried.ObservedAt().Equal(observedAt) {
		t.Fatal("feedback retry replaced the first observation time")
	}
	if _, err := signer.Feedback(admission, []Scope{ScopeIP}, ReasonRiskControl, observedAt, time.Minute); !errors.Is(err, ErrFeedbackConflict) {
		t.Fatal("conflicting feedback for one admission was accepted")
	}
	if err := signer.VerifyFeedback(feedback); err != nil {
		t.Fatal(err)
	}
	otherSigner, err := NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	if err := otherSigner.VerifyFeedback(feedback); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("other signer VerifyFeedback() error = %v", err)
	}
	if _, err := otherSigner.Feedback(admission, []Scope{ScopeIP}, ReasonHTTP429, observedAt, time.Minute); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("other signer Feedback() error = %v", err)
	}
	forgedFeedback := feedback
	forgedFeedback.cooldown += time.Minute
	if err := signer.VerifyFeedback(forgedFeedback); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("VerifyFeedback(forged) error = %v", err)
	}

	for _, test := range []struct {
		name       string
		scopes     []Scope
		reason     ReasonCode
		observedAt time.Time
		cooldown   time.Duration
	}{
		{name: "missing scopes", reason: ReasonHTTP429, observedAt: observedAt, cooldown: time.Minute},
		{name: "duplicate scope", scopes: []Scope{ScopeIP, ScopeIP}, reason: ReasonHTTP429, observedAt: observedAt, cooldown: time.Minute},
		{name: "unapplied scope", scopes: []Scope{ScopeAccountIP}, reason: ReasonHTTP429, observedAt: observedAt, cooldown: time.Minute},
		{name: "arbitrary reason", scopes: []Scope{ScopeIP}, reason: "timeout", observedAt: observedAt, cooldown: time.Minute},
		{name: "before admission", scopes: []Scope{ScopeIP}, reason: ReasonHTTP429, observedAt: admission.AdmittedAt().Add(-time.Microsecond), cooldown: time.Minute},
		{name: "sub-microsecond observation", scopes: []Scope{ScopeIP}, reason: ReasonHTTP429, observedAt: observedAt.Add(time.Nanosecond), cooldown: time.Minute},
		{name: "negative duration", scopes: []Scope{ScopeIP}, reason: ReasonHTTP429, observedAt: observedAt, cooldown: -time.Second},
		{name: "sub-microsecond duration", scopes: []Scope{ScopeIP}, reason: ReasonHTTP429, observedAt: observedAt, cooldown: time.Nanosecond},
		{name: "overlong duration", scopes: []Scope{ScopeIP}, reason: ReasonHTTP429, observedAt: observedAt, cooldown: MaxDuration + time.Microsecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			freshSigner, freshAdmission := testAdmission(t)
			if _, err := freshSigner.Feedback(freshAdmission, test.scopes, test.reason, test.observedAt, test.cooldown); err == nil {
				t.Fatal("NewFeedback() accepted invalid feedback")
			}
		})
	}
	if _, err := feedback.CooldownUntil(2); err == nil {
		t.Fatal("CooldownUntil() accepted unselected scope")
	}

	fallbackSigner, fallbackAdmission := testAdmission(t)
	fallback, err := fallbackSigner.Feedback(fallbackAdmission, []Scope{ScopeIP}, ReasonRiskControl, observedAt, 0)
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := fallback.CooldownUntil(3)
	if err != nil {
		t.Fatal(err)
	}
	if !deadline.Equal(observedAt.Add(time.Minute)) {
		t.Fatalf("fallback deadline = %v", deadline)
	}

	zeroFallbackPolicy := testPolicy(9, ScopeAccount, 0)
	zeroFallbackRule, err := AppliedRuleFromPolicy(zeroFallbackPolicy)
	if err != nil {
		t.Fatal(err)
	}
	zeroFallbackSigner, err := NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	zeroFallbackAdmission, err := zeroFallbackSigner.Issue(admission.Request(), []AppliedRule{zeroFallbackRule}, admission.AdmittedAt())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zeroFallbackSigner.Feedback(zeroFallbackAdmission, []Scope{ScopeAccount}, ReasonHTTP429, observedAt, 0); err == nil {
		t.Fatal("zero cooldown without fallback was accepted")
	}
}

func TestDecisionValidateAndCopies(t *testing.T) {
	signer, admission := testAdmission(t)
	allowed, err := signer.Allow(admission)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed.Allowed() || allowed.Admission().isZero() || allowed.RetryAt() != (time.Time{}) {
		t.Fatal("allowed decision has wrong shape")
	}

	first, err := NewBlocker(1, ScopePlatform, BlockReasonBudget, time.Unix(130, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewBlocker(2, ScopeIP, BlockReasonCooldown, time.Unix(140, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	inputBlockers := []Blocker{first, second}
	blocked, err := Block(inputBlockers)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Allowed() || !blocked.RetryAt().Equal(second.RetryAt()) {
		t.Fatal("blocked decision has wrong shape")
	}
	inputBlockers[0].policyID = 99
	if blocked.Blockers()[0].PolicyID() != 1 {
		t.Fatal("mutating Block() input changed decision")
	}
	copyOfBlockers := blocked.Blockers()
	copyOfBlockers[0].policyID = 99
	if blocked.Blockers()[0].PolicyID() != 1 {
		t.Fatal("mutating Blockers() result changed decision")
	}
	if _, err := Block([]Blocker{first, first}); err == nil {
		t.Fatal("Block() accepted duplicate policy")
	}
	if _, err := NewBlocker(3, ScopeIP, BlockReasonWarmup, time.Unix(150, 1).UTC()); err == nil {
		t.Fatal("NewBlocker() accepted sub-microsecond retry time")
	}

	for _, invalid := range []Decision{
		{},
		{allowed: true, admission: admission, blockers: []Blocker{first}},
		{admission: admission, retryAt: first.RetryAt(), blockers: []Blocker{first}},
		{admission: Admission{request: admission.request}, retryAt: first.RetryAt(), blockers: []Blocker{first}},
		{retryAt: first.RetryAt().Add(time.Second), blockers: []Blocker{first}},
		{retryAt: first.RetryAt(), blockers: []Blocker{first, first}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatal("Validate() accepted malformed decision")
		}
	}
}

func testPolicy(id PolicyID, scope Scope, fallback time.Duration) Policy {
	spec := PolicySpec{
		Platform:        "steam",
		RuleKey:         RuleKey("rule_" + scope),
		Scope:           scope,
		Kind:            KindMinInterval,
		MinInterval:     time.Second,
		DefaultCooldown: fallback,
	}
	if scope == ScopeInterface {
		spec.EndpointClass = "market_summary"
	}
	return Policy{
		ID:       id,
		Revision: 2,
		Enabled:  true,
		ReadyAt:  time.Unix(90, 0).UTC(),
		Spec:     spec,
	}
}

func testAdmission(t *testing.T) (*Signer, Admission) {
	t.Helper()
	lease := testCombinationLease(t, "steam", 11, 21, 31, netip.MustParseAddr("1.1.1.1"))
	request, err := RequestFromLease(lease, "market_summary")
	if err != nil {
		t.Fatal(err)
	}
	policies := []Policy{
		testPolicy(1, ScopePlatform, 30*time.Second),
		testPolicy(2, ScopeInterface, 45*time.Second),
		testPolicy(3, ScopeIP, time.Minute),
	}
	rules := make([]AppliedRule, 0, len(policies))
	for _, policy := range policies {
		rule, err := AppliedRuleFromPolicy(policy)
		if err != nil {
			t.Fatal(err)
		}
		rules = append(rules, rule)
	}
	signer, err := NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	admittedAt := lease.Snapshot.ExitVerifiedAt.Add(2 * time.Minute)
	admission, err := signer.Issue(request, []AppliedRule{rules[2], rules[0], rules[1]}, admittedAt)
	if err != nil {
		t.Fatal(err)
	}
	return signer, admission
}

func testCombinationLease(
	t *testing.T,
	platform resource.Platform,
	combinationID resource.CombinationID,
	accountID resource.AccountID,
	nodeID resource.NodeID,
	exitAddress netip.Addr,
) resource.Lease {
	t.Helper()
	_, lease := testCombinationLeaseWithCoordinator(t, platform, combinationID, accountID, nodeID, exitAddress)
	return lease
}

func testCombinationLeaseWithCoordinator(
	t *testing.T,
	platform resource.Platform,
	combinationID resource.CombinationID,
	accountID resource.AccountID,
	nodeID resource.NodeID,
	exitAddress netip.Addr,
) (*resource.Coordinator, resource.Lease) {
	t.Helper()
	now := time.Unix(1000, 0).UTC()
	checkedAt := now.Add(-time.Minute)
	resources := resource.CombinationResources{
		Combination: resource.AccountNodeCombination{
			ID: combinationID, Platform: platform, AccountID: accountID, NodeID: nodeID,
		},
		Account: resource.PlatformAccount{
			ID: accountID, Platform: platform, Alias: "account", SessionState: resource.AccountSessionStateValid,
			SessionRevision: 3, LastCheckedAt: &checkedAt,
		},
		Node: resource.AccessNode{
			ID: nodeID, Name: "node", Kind: resource.NodeKindDirect, Region: resource.NodeRegionDomestic,
			EgressMode: resource.EgressModeStatic, State: resource.NodeStateAvailable, EgressRevision: 4,
			AssignmentRevision: 2, AppID: 730,
			Sides: []resource.NodeSideAssignment{{Platform: platform, Side: "ask"}},
			ExitVerification: &resource.ExitVerification{
				VerifiedRevision: 4, Address: exitAddress,
				VerifiedAt: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour),
			},
		},
	}
	coordinator, err := resource.NewCoordinator(&leaseRepository{resources: resources})
	if err != nil {
		t.Fatal(err)
	}
	component, err := coordinator.RegisterComponent()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := coordinator.AcquireCombination(context.Background(), component, combinationID, resource.TargetRegionDomestic, now, 730, "ask")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := coordinator.Release(lease.Token); err != nil && !errors.Is(err, resource.ErrLeaseNotHeld) {
			t.Errorf("Release() error = %v", err)
		}
	})
	return coordinator, lease
}
