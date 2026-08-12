// Package ratelimit defines platform-neutral request pacing and cooldown facts.
package ratelimit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"sync"
	"time"

	"buff-go/internal/resource"
)

const (
	// MaxRollingRequests bounds the O(N) PostgreSQL array rewrite for one rule
	// to about 8 KiB of raw timestamp values. Larger proven limits need a
	// different persistence strategy instead of an implicit approximation.
	MaxRollingRequests int64 = 1024
	// MaxDuration is a technical bound for checked timestamp arithmetic.
	MaxDuration = 365 * 24 * time.Hour
)

var tokenPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

var (
	ErrPolicyUnavailable = errors.New("required rate-limit policy is unavailable")
	ErrInvalidAdmission  = errors.New("invalid rate-limit admission")
	ErrFeedbackConflict  = errors.New("conflicting feedback for admission")
)

// Scope identifies one independently evidenced limit dimension.
type Scope string

const (
	ScopePlatform  Scope = "platform"
	ScopeInterface Scope = "interface"
	ScopeAccount   Scope = "account"
	ScopeIP        Scope = "ip"
	ScopeAccountIP Scope = "account_ip"
)

// Validate rejects limit dimensions outside the stable model.
func (scope Scope) Validate() error {
	switch scope {
	case ScopePlatform, ScopeInterface, ScopeAccount, ScopeIP, ScopeAccountIP:
		return nil
	default:
		return fmt.Errorf("invalid rate-limit scope")
	}
}

// EndpointClass is a stable platform-adapter category, never a URL or side.
type EndpointClass string

// Validate checks the controlled machine token.
func (class EndpointClass) Validate() error {
	if !tokenPattern.MatchString(string(class)) {
		return fmt.Errorf("invalid endpoint class")
	}
	return nil
}

// RuleKey is one stable platform-local policy name.
type RuleKey string

// Validate checks the controlled machine token.
func (key RuleKey) Validate() error {
	if !tokenPattern.MatchString(string(key)) {
		return fmt.Errorf("invalid rule key")
	}
	return nil
}

// Kind keeps unlike platform evidence from being silently approximated.
type Kind string

const (
	KindCooldownOnly  Kind = "cooldown_only"
	KindMinInterval   Kind = "min_interval"
	KindRollingWindow Kind = "rolling_window"
)

// Validate rejects an unknown pacing algorithm.
func (kind Kind) Validate() error {
	switch kind {
	case KindCooldownOnly, KindMinInterval, KindRollingWindow:
		return nil
	default:
		return fmt.Errorf("invalid rate-limit kind")
	}
}

// PolicyID is the stable database identity of one independent rule.
type PolicyID int64

// Validate rejects a missing policy identity.
func (id PolicyID) Validate() error {
	if id < 1 {
		return fmt.Errorf("policy_id must be at least 1")
	}
	return nil
}

// PolicySpec is immutable evidence identity plus its pacing terms.
// Multiple specs may target the same scope and all of them apply.
type PolicySpec struct {
	Platform        resource.Platform
	RuleKey         RuleKey
	Scope           Scope
	EndpointClass   EndpointClass
	Kind            Kind
	MinInterval     time.Duration
	Window          time.Duration
	MaxRequests     int64
	DefaultCooldown time.Duration
}

// Validate checks that the policy expresses exactly one proven algorithm.
func (spec PolicySpec) Validate() error {
	if err := spec.Platform.Validate(); err != nil {
		return fmt.Errorf("platform: %w", err)
	}
	if err := spec.RuleKey.Validate(); err != nil {
		return err
	}
	if err := spec.Scope.Validate(); err != nil {
		return err
	}
	if spec.Scope == ScopeInterface {
		if err := spec.EndpointClass.Validate(); err != nil {
			return err
		}
	} else if spec.EndpointClass != "" {
		return fmt.Errorf("endpoint class is only valid for interface scope")
	}
	if err := spec.Kind.Validate(); err != nil {
		return err
	}
	if err := validateOptionalDuration("default cooldown", spec.DefaultCooldown); err != nil {
		return err
	}

	switch spec.Kind {
	case KindCooldownOnly:
		if spec.MinInterval != 0 || spec.Window != 0 || spec.MaxRequests != 0 {
			return fmt.Errorf("cooldown-only policy must not contain pacing terms")
		}
	case KindMinInterval:
		if err := validateRequiredDuration("minimum interval", spec.MinInterval); err != nil {
			return err
		}
		if spec.Window != 0 || spec.MaxRequests != 0 {
			return fmt.Errorf("minimum-interval policy must not contain rolling-window terms")
		}
	case KindRollingWindow:
		if spec.MinInterval != 0 {
			return fmt.Errorf("rolling-window policy must not contain a minimum interval")
		}
		if err := validateRequiredDuration("rolling window", spec.Window); err != nil {
			return err
		}
		if spec.MaxRequests < 1 || spec.MaxRequests > MaxRollingRequests {
			return fmt.Errorf("max_requests must be between 1 and %d", MaxRollingRequests)
		}
	}
	return nil
}

// WarmupDuration is the conservative quiet period required after activation
// or a pacing-term replacement.
func (spec PolicySpec) WarmupDuration() (time.Duration, error) {
	if err := spec.Validate(); err != nil {
		return 0, err
	}
	switch spec.Kind {
	case KindMinInterval:
		return spec.MinInterval, nil
	case KindRollingWindow:
		return spec.Window, nil
	default:
		return 0, nil
	}
}

// IsRate reports whether the rule actively reserves request budget.
func (spec PolicySpec) IsRate() bool {
	return spec.Kind == KindMinInterval || spec.Kind == KindRollingWindow
}

// SameIdentity reports whether a replacement preserves the evidence identity.
func (spec PolicySpec) SameIdentity(other PolicySpec) bool {
	return spec.Platform == other.Platform &&
		spec.RuleKey == other.RuleKey &&
		spec.Scope == other.Scope &&
		spec.EndpointClass == other.EndpointClass &&
		spec.Kind == other.Kind
}

// Policy is one persisted rule revision.
type Policy struct {
	ID       PolicyID
	Revision int64
	Enabled  bool
	ReadyAt  time.Time
	Spec     PolicySpec
}

// Validate checks persisted identity and policy terms.
func (policy Policy) Validate() error {
	if err := policy.ID.Validate(); err != nil {
		return err
	}
	if policy.Revision < 1 {
		return fmt.Errorf("policy revision must be at least 1")
	}
	if policy.ReadyAt.IsZero() {
		return fmt.Errorf("policy ready_at is required")
	}
	if err := validateMicrosecondTime("policy ready_at", policy.ReadyAt); err != nil {
		return err
	}
	return policy.Spec.Validate()
}

// Request is the opaque, credential-free rate identity projected from one
// live combination lease. Node and combination identities are not rate keys.
type Request struct {
	platform       resource.Platform
	endpointClass  EndpointClass
	accountID      resource.AccountID
	exitAddress    netip.Addr
	exitVerifiedAt time.Time
	exitValidUntil time.Time
	lease          resource.Lease
}

// RequestFromLease projects only the stable rate identity from a live
// combination lease. Admission becomes authoritative only after Store reserve.
func RequestFromLease(lease resource.Lease, class EndpointClass) (Request, error) {
	snapshot, err := lease.CombinationSnapshot()
	if err != nil {
		return Request{}, fmt.Errorf("a live combination lease is required")
	}
	if err := snapshot.CombinationID.Validate(); err != nil {
		return Request{}, err
	}
	if err := snapshot.NodeID.Validate(); err != nil {
		return Request{}, err
	}
	if err := snapshot.NodeKind.Validate(); err != nil {
		return Request{}, err
	}
	if err := snapshot.NodeRegion.Validate(); err != nil {
		return Request{}, err
	}
	if err := snapshot.EgressMode.Validate(); err != nil {
		return Request{}, err
	}
	if snapshot.SessionRevision < 1 || snapshot.EgressRevision < 1 || snapshot.AssignmentRevision < 1 {
		return Request{}, fmt.Errorf("lease resource revisions must be at least 1")
	}
	request := Request{
		platform:       snapshot.Platform,
		endpointClass:  class,
		accountID:      snapshot.AccountID,
		exitAddress:    snapshot.ExitAddress,
		exitVerifiedAt: snapshot.ExitVerifiedAt,
		exitValidUntil: snapshot.ExitValidUntil,
		lease:          lease,
	}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}

// Validate checks stable identity and the recorded exit evidence interval.
func (request Request) Validate() error {
	if err := request.platform.Validate(); err != nil {
		return fmt.Errorf("platform: %w", err)
	}
	if err := request.endpointClass.Validate(); err != nil {
		return err
	}
	if err := request.accountID.Validate(); err != nil {
		return err
	}
	verification := resource.ExitVerification{
		VerifiedRevision: 1,
		Address:          request.exitAddress,
		VerifiedAt:       request.exitVerifiedAt,
		ValidUntil:       request.exitValidUntil,
	}
	if err := verification.Validate(); err != nil {
		return fmt.Errorf("exit verification: %w", err)
	}
	return nil
}

// ValidateAt additionally proves the exit evidence is current at now.
func (request Request) ValidateAt(now time.Time) error {
	if err := request.Validate(); err != nil {
		return err
	}
	snapshot, err := request.lease.CombinationSnapshot()
	if err != nil {
		return fmt.Errorf("combination lease is not active")
	}
	if snapshot.Platform != request.platform || snapshot.AccountID != request.accountID ||
		snapshot.ExitAddress != request.exitAddress ||
		!snapshot.ExitVerifiedAt.Equal(request.exitVerifiedAt) || !snapshot.ExitValidUntil.Equal(request.exitValidUntil) {
		return fmt.Errorf("combination lease identity changed")
	}
	if now.IsZero() {
		return fmt.Errorf("request time is required")
	}
	if now.Before(request.exitVerifiedAt) || !now.Before(request.exitValidUntil) {
		return fmt.Errorf("exit verification is not valid at request time")
	}
	return nil
}

func (request Request) Platform() resource.Platform   { return request.platform }
func (request Request) EndpointClass() EndpointClass  { return request.endpointClass }
func (request Request) AccountID() resource.AccountID { return request.accountID }
func (request Request) ExitAddress() netip.Addr       { return request.exitAddress }
func (request Request) ExitVerifiedAt() time.Time     { return request.exitVerifiedAt }
func (request Request) ExitValidUntil() time.Time     { return request.exitValidUntil }

func (request Request) isZero() bool {
	return request.platform == "" && request.endpointClass == "" && request.accountID == 0 &&
		!request.exitAddress.IsValid() && request.exitVerifiedAt.IsZero() && request.exitValidUntil.IsZero() &&
		request.lease.Token.String() == "" && request.lease.Component.String() == ""
}

// Subject is one concrete state key. Platform is present in every scope.
type Subject struct {
	Scope         Scope
	Platform      resource.Platform
	EndpointClass EndpointClass
	AccountID     resource.AccountID
	ExitAddress   netip.Addr
}

// Validate checks the exact field shape of one scope.
func (subject Subject) Validate() error {
	if err := subject.Scope.Validate(); err != nil {
		return err
	}
	if err := subject.Platform.Validate(); err != nil {
		return fmt.Errorf("platform: %w", err)
	}
	switch subject.Scope {
	case ScopePlatform:
		if subject.EndpointClass != "" || subject.AccountID != 0 || subject.ExitAddress.IsValid() {
			return fmt.Errorf("platform subject contains unrelated identity")
		}
	case ScopeInterface:
		if err := subject.EndpointClass.Validate(); err != nil {
			return err
		}
		if subject.AccountID != 0 || subject.ExitAddress.IsValid() {
			return fmt.Errorf("interface subject contains unrelated identity")
		}
	case ScopeAccount:
		if err := subject.AccountID.Validate(); err != nil {
			return err
		}
		if subject.EndpointClass != "" || subject.ExitAddress.IsValid() {
			return fmt.Errorf("account subject contains unrelated identity")
		}
	case ScopeIP:
		if err := validateExitAddress(subject.ExitAddress); err != nil {
			return err
		}
		if subject.EndpointClass != "" || subject.AccountID != 0 {
			return fmt.Errorf("IP subject contains unrelated identity")
		}
	case ScopeAccountIP:
		if err := subject.AccountID.Validate(); err != nil {
			return err
		}
		if err := validateExitAddress(subject.ExitAddress); err != nil {
			return err
		}
		if subject.EndpointClass != "" {
			return fmt.Errorf("account-IP subject contains unrelated identity")
		}
	}
	return nil
}

// Subject derives one concrete key without node or combination identity.
func (request Request) Subject(scope Scope) (Subject, error) {
	if err := request.Validate(); err != nil {
		return Subject{}, err
	}
	subject := Subject{Scope: scope, Platform: request.platform}
	switch scope {
	case ScopePlatform:
	case ScopeInterface:
		subject.EndpointClass = request.endpointClass
	case ScopeAccount:
		subject.AccountID = request.accountID
	case ScopeIP:
		subject.ExitAddress = request.exitAddress
	case ScopeAccountIP:
		subject.AccountID = request.accountID
		subject.ExitAddress = request.exitAddress
	default:
		return Subject{}, fmt.Errorf("invalid rate-limit scope")
	}
	if err := subject.Validate(); err != nil {
		return Subject{}, err
	}
	return subject, nil
}

// AppliedRule is an immutable snapshot of one reserved policy revision.
type AppliedRule struct {
	policyID         PolicyID
	policyRevision   int64
	platform         resource.Platform
	scope            Scope
	endpointClass    EndpointClass
	fallbackCooldown time.Duration
}

// AppliedRuleFromPolicy freezes fields needed by delayed feedback.
func AppliedRuleFromPolicy(policy Policy) (AppliedRule, error) {
	if err := policy.Validate(); err != nil {
		return AppliedRule{}, err
	}
	if !policy.Enabled {
		return AppliedRule{}, fmt.Errorf("disabled policy cannot be applied")
	}
	return AppliedRule{
		policyID:         policy.ID,
		policyRevision:   policy.Revision,
		platform:         policy.Spec.Platform,
		scope:            policy.Spec.Scope,
		endpointClass:    policy.Spec.EndpointClass,
		fallbackCooldown: policy.Spec.DefaultCooldown,
	}, nil
}

// Validate checks a ticket rule reference.
func (rule AppliedRule) Validate() error {
	if err := rule.policyID.Validate(); err != nil {
		return err
	}
	if rule.policyRevision < 1 {
		return fmt.Errorf("policy revision must be at least 1")
	}
	if err := rule.platform.Validate(); err != nil {
		return fmt.Errorf("platform: %w", err)
	}
	if err := rule.scope.Validate(); err != nil {
		return err
	}
	if rule.scope == ScopeInterface {
		if err := rule.endpointClass.Validate(); err != nil {
			return err
		}
	} else if rule.endpointClass != "" {
		return fmt.Errorf("endpoint class is only valid for interface scope")
	}
	return validateOptionalDuration("fallback cooldown", rule.fallbackCooldown)
}

func (rule AppliedRule) PolicyID() PolicyID              { return rule.policyID }
func (rule AppliedRule) PolicyRevision() int64           { return rule.policyRevision }
func (rule AppliedRule) Platform() resource.Platform     { return rule.platform }
func (rule AppliedRule) Scope() Scope                    { return rule.scope }
func (rule AppliedRule) EndpointClass() EndpointClass    { return rule.endpointClass }
func (rule AppliedRule) FallbackCooldown() time.Duration { return rule.fallbackCooldown }

// Signer is a process-local admission capability. A Store keeps its signer
// private, so a structurally valid ticket from another issuer is rejected.
type Signer struct {
	key         [32]byte
	initialized bool
}

type feedbackState struct {
	mu       sync.Mutex
	issued   bool
	feedback Feedback
}

// NewSigner creates one process-local admission issuer.
func NewSigner() (*Signer, error) {
	signer := &Signer{}
	if _, err := rand.Read(signer.key[:]); err != nil {
		return nil, fmt.Errorf("create rate-limit signer: %w", err)
	}
	signer.initialized = true
	return signer, nil
}

// Admission is an opaque capability returned only after atomic reserve.
type Admission struct {
	request    Request
	rules      []AppliedRule
	admittedAt time.Time
	nonce      [16]byte
	seal       [sha256.Size]byte
	feedback   *feedbackState
}

// Issue signs one already-reserved admission. The backing transaction must be
// committed before the value is returned to a caller.
func (signer *Signer) Issue(request Request, rules []AppliedRule, admittedAt time.Time) (Admission, error) {
	if signer == nil || !signer.initialized {
		return Admission{}, fmt.Errorf("uninitialized rate-limit signer")
	}
	if err := request.ValidateAt(admittedAt); err != nil {
		return Admission{}, err
	}
	admission := Admission{
		request: request, rules: append([]AppliedRule(nil), rules...), admittedAt: admittedAt,
		feedback: &feedbackState{},
	}
	if _, err := rand.Read(admission.nonce[:]); err != nil {
		return Admission{}, fmt.Errorf("create admission nonce: %w", err)
	}
	if admission.nonce == [16]byte{} {
		return Admission{}, fmt.Errorf("create admission nonce: empty random value")
	}
	if err := admission.validateShape(); err != nil {
		return Admission{}, err
	}
	sort.Slice(admission.rules, func(left, right int) bool {
		return admission.rules[left].policyID < admission.rules[right].policyID
	})
	admission.seal = signer.seal(admission)
	return admission, nil
}

// Verify proves that this signer issued an unchanged admission.
func (signer *Signer) Verify(admission Admission) error {
	if signer == nil || !signer.initialized {
		return fmt.Errorf("uninitialized rate-limit signer")
	}
	if err := admission.validateShape(); err != nil {
		return fmt.Errorf("%w: invalid shape", ErrInvalidAdmission)
	}
	want := signer.seal(admission)
	if !hmac.Equal(admission.seal[:], want[:]) {
		return ErrInvalidAdmission
	}
	return nil
}

func (signer *Signer) seal(admission Admission) [sha256.Size]byte {
	mac := hmac.New(sha256.New, signer.key[:])
	_, _ = mac.Write(admissionPayload(admission))
	var seal [sha256.Size]byte
	copy(seal[:], mac.Sum(nil))
	return seal
}

func (admission Admission) validateShape() error {
	if err := validateMicrosecondTime("admitted_at", admission.admittedAt); err != nil {
		return err
	}
	if err := admission.request.Validate(); err != nil {
		return err
	}
	if admission.admittedAt.Before(admission.request.exitVerifiedAt) || !admission.admittedAt.Before(admission.request.exitValidUntil) {
		return fmt.Errorf("admission time is outside exit verification")
	}
	if len(admission.rules) == 0 {
		return fmt.Errorf("admission requires applied rules")
	}
	seen := make(map[PolicyID]struct{}, len(admission.rules))
	for _, rule := range admission.rules {
		if err := rule.Validate(); err != nil {
			return err
		}
		if _, exists := seen[rule.policyID]; exists {
			return fmt.Errorf("duplicate admission policy")
		}
		if rule.platform != admission.request.platform {
			return fmt.Errorf("applied policy platform does not match request")
		}
		if rule.scope == ScopeInterface && rule.endpointClass != admission.request.endpointClass {
			return fmt.Errorf("applied interface policy does not match request")
		}
		seen[rule.policyID] = struct{}{}
	}
	if admission.nonce == [16]byte{} || admission.feedback == nil {
		return fmt.Errorf("admission capability state is required")
	}
	return nil
}

func (admission Admission) Request() Request { return admission.request }
func (admission Admission) Rules() []AppliedRule {
	return append([]AppliedRule(nil), admission.rules...)
}
func (admission Admission) AdmittedAt() time.Time { return admission.admittedAt }

// HasScope reports whether feedback may reference this evidenced scope.
func (admission Admission) HasScope(scope Scope) bool {
	for _, rule := range admission.rules {
		if rule.scope == scope {
			return true
		}
	}
	return false
}

func (admission Admission) isZero() bool {
	return admission.request.isZero() && len(admission.rules) == 0 && admission.admittedAt.IsZero() &&
		admission.nonce == [16]byte{} && admission.seal == [sha256.Size]byte{} && admission.feedback == nil
}

// BlockReason explains a non-blocking admission decision.
type BlockReason string

const (
	BlockReasonBudget   BlockReason = "budget"
	BlockReasonCooldown BlockReason = "cooldown"
	BlockReasonWarmup   BlockReason = "warmup"
)

// Validate rejects an unknown machine reason.
func (reason BlockReason) Validate() error {
	switch reason {
	case BlockReasonBudget, BlockReasonCooldown, BlockReasonWarmup:
		return nil
	default:
		return fmt.Errorf("invalid block reason")
	}
}

// Blocker identifies one applicable rule and its next eligible time.
type Blocker struct {
	policyID PolicyID
	scope    Scope
	reason   BlockReason
	retryAt  time.Time
}

// NewBlocker creates one immutable wait reason.
func NewBlocker(policyID PolicyID, scope Scope, reason BlockReason, retryAt time.Time) (Blocker, error) {
	blocker := Blocker{policyID: policyID, scope: scope, reason: reason, retryAt: retryAt}
	if err := blocker.Validate(); err != nil {
		return Blocker{}, err
	}
	return blocker, nil
}

// Validate checks one blocker.
func (blocker Blocker) Validate() error {
	if err := blocker.policyID.Validate(); err != nil {
		return err
	}
	if err := blocker.scope.Validate(); err != nil {
		return err
	}
	if err := blocker.reason.Validate(); err != nil {
		return err
	}
	return validateMicrosecondTime("blocker retry_at", blocker.retryAt)
}

func (blocker Blocker) PolicyID() PolicyID  { return blocker.policyID }
func (blocker Blocker) Scope() Scope        { return blocker.scope }
func (blocker Blocker) Reason() BlockReason { return blocker.reason }
func (blocker Blocker) RetryAt() time.Time  { return blocker.retryAt }

// Decision is an immutable immediate allow or wait result; it never sleeps.
type Decision struct {
	allowed   bool
	admission Admission
	retryAt   time.Time
	blockers  []Blocker
}

// Allow creates an allowed decision only for an admission issued by signer.
func (signer *Signer) Allow(admission Admission) (Decision, error) {
	if err := signer.Verify(admission); err != nil {
		return Decision{}, err
	}
	return Decision{allowed: true, admission: admission}, nil
}

// Block creates a denied decision and derives its latest retry time.
func Block(blockers []Blocker) (Decision, error) {
	decision := Decision{blockers: append([]Blocker(nil), blockers...)}
	if len(decision.blockers) == 0 {
		return Decision{}, fmt.Errorf("blocked decision requires blockers")
	}
	seen := make(map[PolicyID]struct{}, len(decision.blockers))
	for _, blocker := range decision.blockers {
		if err := blocker.Validate(); err != nil {
			return Decision{}, err
		}
		if _, exists := seen[blocker.policyID]; exists {
			return Decision{}, fmt.Errorf("duplicate blocker policy")
		}
		seen[blocker.policyID] = struct{}{}
		if decision.retryAt.IsZero() || blocker.retryAt.After(decision.retryAt) {
			decision.retryAt = blocker.retryAt
		}
	}
	return decision, nil
}

// Validate checks the mutually exclusive allow and blocked forms.
func (decision Decision) Validate() error {
	if decision.allowed {
		if !decision.retryAt.IsZero() || len(decision.blockers) != 0 {
			return fmt.Errorf("allowed decision must not contain blockers")
		}
		return decision.admission.validateShape()
	}
	if decision.retryAt.IsZero() || len(decision.blockers) == 0 {
		return fmt.Errorf("blocked decision requires blockers and retry_at")
	}
	if !decision.admission.isZero() {
		return fmt.Errorf("blocked decision must not contain an admission")
	}
	maxRetry := time.Time{}
	seen := make(map[PolicyID]struct{}, len(decision.blockers))
	for _, blocker := range decision.blockers {
		if err := blocker.Validate(); err != nil {
			return err
		}
		if _, exists := seen[blocker.policyID]; exists {
			return fmt.Errorf("duplicate blocker policy")
		}
		seen[blocker.policyID] = struct{}{}
		if maxRetry.IsZero() || blocker.retryAt.After(maxRetry) {
			maxRetry = blocker.retryAt
		}
	}
	if !decision.retryAt.Equal(maxRetry) {
		return fmt.Errorf("decision retry_at must equal the latest blocker")
	}
	return nil
}

func (decision Decision) Allowed() bool        { return decision.allowed }
func (decision Decision) Admission() Admission { return decision.admission }
func (decision Decision) RetryAt() time.Time   { return decision.retryAt }
func (decision Decision) Blockers() []Blocker {
	return append([]Blocker(nil), decision.blockers...)
}

// ReasonCode is a controlled platform-feedback reason, never raw response text.
type ReasonCode string

const (
	ReasonHTTP429     ReasonCode = "http_429"
	ReasonRiskControl ReasonCode = "risk_control"
)

// Validate rejects reasons that have no approved feedback semantics.
func (reason ReasonCode) Validate() error {
	switch reason {
	case ReasonHTTP429, ReasonRiskControl:
		return nil
	default:
		return fmt.Errorf("invalid cooldown reason")
	}
}

// Feedback freezes evidenced scopes and one observation time from a successful
// admission. Reapplying it derives the same deadlines instead of extending them.
type Feedback struct {
	admission  Admission
	scopes     []Scope
	reason     ReasonCode
	observedAt time.Time
	cooldown   time.Duration
}

// newFeedback creates immutable cooldown feedback for scopes reserved by the
// admission. A zero cooldown requires every selected applied rule to carry a
// positive fallback snapshot.
func newFeedback(
	admission Admission,
	scopes []Scope,
	reason ReasonCode,
	observedAt time.Time,
	cooldown time.Duration,
) (Feedback, error) {
	feedback := Feedback{
		admission:  admission,
		scopes:     append([]Scope(nil), scopes...),
		reason:     reason,
		observedAt: observedAt,
		cooldown:   cooldown,
	}
	if err := feedback.Validate(); err != nil {
		return Feedback{}, err
	}
	sort.Slice(feedback.scopes, func(left, right int) bool {
		return feedback.scopes[left] < feedback.scopes[right]
	})
	return feedback, nil
}

// Feedback freezes the first feedback for one admission. Exact retries return
// the original value; a conflicting second interpretation is rejected.
func (signer *Signer) Feedback(
	admission Admission,
	scopes []Scope,
	reason ReasonCode,
	observedAt time.Time,
	cooldown time.Duration,
) (Feedback, error) {
	if err := signer.Verify(admission); err != nil {
		return Feedback{}, err
	}
	candidate, err := newFeedback(admission, scopes, reason, observedAt, cooldown)
	if err != nil {
		return Feedback{}, err
	}
	state := admission.feedback
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.issued {
		if !sameFeedbackCommand(state.feedback, candidate) {
			return Feedback{}, ErrFeedbackConflict
		}
		return state.feedback, nil
	}
	state.issued = true
	state.feedback = candidate
	return candidate, nil
}

// VerifyFeedback proves that signer issued the admission and froze this exact
// feedback command before storage changes any cooldown state.
func (signer *Signer) VerifyFeedback(feedback Feedback) error {
	if err := signer.Verify(feedback.admission); err != nil {
		return err
	}
	if err := feedback.Validate(); err != nil {
		return err
	}
	state := feedback.admission.feedback
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.issued || !sameFeedback(state.feedback, feedback) {
		return ErrInvalidAdmission
	}
	return nil
}

// Validate rejects unsupported scopes, replay-unstable time facts, and a zero
// cooldown without complete fallback evidence.
func (feedback Feedback) Validate() error {
	if err := feedback.admission.validateShape(); err != nil {
		return err
	}
	if len(feedback.scopes) == 0 {
		return fmt.Errorf("feedback requires at least one scope")
	}
	if err := feedback.reason.Validate(); err != nil {
		return err
	}
	if err := validateMicrosecondTime("feedback observed_at", feedback.observedAt); err != nil {
		return err
	}
	if feedback.observedAt.Before(feedback.admission.admittedAt) {
		return fmt.Errorf("feedback observed_at must not precede admission")
	}
	if err := validateOptionalDuration("cooldown", feedback.cooldown); err != nil {
		return err
	}

	seen := make(map[Scope]struct{}, len(feedback.scopes))
	for _, scope := range feedback.scopes {
		if err := scope.Validate(); err != nil {
			return err
		}
		if _, exists := seen[scope]; exists {
			return fmt.Errorf("duplicate feedback scope")
		}
		if !feedback.admission.HasScope(scope) {
			return fmt.Errorf("feedback scope was not applied by admission")
		}
		seen[scope] = struct{}{}
	}

	if feedback.cooldown == 0 {
		for _, rule := range feedback.admission.rules {
			if _, selected := seen[rule.scope]; selected && rule.fallbackCooldown == 0 {
				return fmt.Errorf("selected rule has no fallback cooldown")
			}
		}
	}
	return nil
}

func (feedback Feedback) Admission() Admission    { return feedback.admission }
func (feedback Feedback) Scopes() []Scope         { return append([]Scope(nil), feedback.scopes...) }
func (feedback Feedback) Reason() ReasonCode      { return feedback.reason }
func (feedback Feedback) ObservedAt() time.Time   { return feedback.observedAt }
func (feedback Feedback) Cooldown() time.Duration { return feedback.cooldown }

// CooldownUntil derives one deterministic deadline from the signed applied
// policy snapshot. It never accepts a caller-supplied fallback.
func (feedback Feedback) CooldownUntil(policyID PolicyID) (time.Time, error) {
	if err := feedback.Validate(); err != nil {
		return time.Time{}, err
	}
	var applied AppliedRule
	found := false
	for _, rule := range feedback.admission.rules {
		if rule.policyID == policyID {
			applied = rule
			found = true
			break
		}
	}
	if !found || !feedback.hasScope(applied.scope) {
		return time.Time{}, fmt.Errorf("policy was not selected by feedback")
	}
	duration := feedback.cooldown
	if duration == 0 {
		duration = applied.fallbackCooldown
	}
	if err := validateRequiredDuration("cooldown", duration); err != nil {
		return time.Time{}, err
	}
	return checkedAdd(feedback.observedAt, duration)
}

func (feedback Feedback) hasScope(scope Scope) bool {
	for _, candidate := range feedback.scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

func sameFeedbackCommand(left, right Feedback) bool {
	return left.admission.seal == right.admission.seal && left.reason == right.reason &&
		left.cooldown == right.cooldown && equalScopes(left.scopes, right.scopes)
}

func sameFeedback(left, right Feedback) bool {
	return sameFeedbackCommand(left, right) && left.observedAt.Equal(right.observedAt)
}

func equalScopes(left, right []Scope) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func admissionPayload(admission Admission) []byte {
	payload := make([]byte, 0, 256+len(admission.rules)*40)
	payload = appendLengthPrefixed(payload, []byte("buffgo-ratelimit-admission-v1"))
	payload = appendLengthPrefixed(payload, []byte(admission.request.platform))
	payload = appendLengthPrefixed(payload, []byte(admission.request.endpointClass))
	payload = binary.BigEndian.AppendUint64(payload, uint64(admission.request.accountID))
	payload = appendLengthPrefixed(payload, []byte(admission.request.exitAddress.String()))
	payload = appendTime(payload, admission.request.exitVerifiedAt)
	payload = appendTime(payload, admission.request.exitValidUntil)
	payload = appendTime(payload, admission.admittedAt)
	payload = append(payload, admission.nonce[:]...)
	payload = binary.BigEndian.AppendUint64(payload, uint64(len(admission.rules)))
	for _, rule := range admission.rules {
		payload = binary.BigEndian.AppendUint64(payload, uint64(rule.policyID))
		payload = binary.BigEndian.AppendUint64(payload, uint64(rule.policyRevision))
		payload = appendLengthPrefixed(payload, []byte(rule.platform))
		payload = appendLengthPrefixed(payload, []byte(rule.scope))
		payload = appendLengthPrefixed(payload, []byte(rule.endpointClass))
		payload = binary.BigEndian.AppendUint64(payload, uint64(rule.fallbackCooldown/time.Microsecond))
	}
	return payload
}

func appendLengthPrefixed(target, value []byte) []byte {
	target = binary.BigEndian.AppendUint64(target, uint64(len(value)))
	return append(target, value...)
}

func appendTime(target []byte, value time.Time) []byte {
	return appendLengthPrefixed(target, []byte(value.UTC().Format(time.RFC3339Nano)))
}

func validateExitAddress(address netip.Addr) error {
	verification := resource.ExitVerification{
		VerifiedRevision: 1,
		Address:          address,
		VerifiedAt:       time.Unix(1, 0).UTC(),
		ValidUntil:       time.Unix(2, 0).UTC(),
	}
	if err := verification.Validate(); err != nil {
		return fmt.Errorf("exit address: %w", err)
	}
	return nil
}

func validateRequiredDuration(field string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	return validateDuration(field, value)
}

func validateOptionalDuration(field string, value time.Duration) error {
	if value < 0 {
		return fmt.Errorf("%s must not be negative", field)
	}
	if value == 0 {
		return nil
	}
	return validateDuration(field, value)
}

func validateDuration(field string, value time.Duration) error {
	if value%time.Microsecond != 0 {
		return fmt.Errorf("%s must use whole microseconds", field)
	}
	if value > MaxDuration {
		return fmt.Errorf("%s must not exceed %s", field, MaxDuration)
	}
	return nil
}

func validateMicrosecondTime(field string, value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("%s is required", field)
	}
	if value.Nanosecond()%int(time.Microsecond) != 0 {
		return fmt.Errorf("%s must use whole microseconds", field)
	}
	return nil
}

func checkedAdd(base time.Time, duration time.Duration) (time.Time, error) {
	if err := validateRequiredDuration("duration", duration); err != nil {
		return time.Time{}, err
	}
	result := base.Add(duration)
	if !result.After(base) {
		return time.Time{}, fmt.Errorf("cooldown deadline overflow")
	}
	if err := validateMicrosecondTime("cooldown deadline", result); err != nil {
		return time.Time{}, err
	}
	return result, nil
}
