package collection

import (
	"fmt"
	"time"

	"buff-go/internal/market"
)

// PlatformSteam is the Steam platform identifier.
const PlatformSteam Platform = "steam"

// SummaryTargetInput is the persisted shape of one game-direction target.
type SummaryTargetInput struct {
	ID            TargetID
	Revision      Revision
	Platform      Platform
	AppID         int64
	Side          market.Side
	Desired       DesiredState
	Actual        ActualState
	SwitchVersion Revision
	Reason        TargetReason
	Recovery      RecoveryMode
	RecheckAt     *time.Time
	ChangedAt     time.Time
}

// Target is an immutable controllable summary target.
type Target struct {
	id            TargetID
	revision      Revision
	taskType      TaskType
	platform      Platform
	appID         int64
	side          market.Side
	desired       DesiredState
	actual        ActualState
	switchVersion Revision
	reason        TargetReason
	recovery      RecoveryMode
	recheckAt     time.Time
	changedAt     time.Time
}

// NewSummaryTarget validates a summary target restored from persistence.
func NewSummaryTarget(input SummaryTargetInput) (Target, error) {
	target := Target{
		id:            input.ID,
		revision:      input.Revision,
		taskType:      TaskTypeSummary,
		platform:      input.Platform,
		appID:         input.AppID,
		side:          input.Side,
		desired:       input.Desired,
		actual:        input.Actual,
		switchVersion: input.SwitchVersion,
		reason:        input.Reason,
		recovery:      input.Recovery,
		recheckAt:     timeValue(input.RecheckAt),
		changedAt:     input.ChangedAt,
	}
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

// Validate checks the target shape and state combination.
func (target Target) Validate() error {
	if err := target.id.Validate(); err != nil {
		return err
	}
	if err := target.revision.Validate(); err != nil {
		return err
	}
	if err := target.switchVersion.Validate(); err != nil {
		return fmt.Errorf("switch_version: %w", err)
	}
	if err := target.taskType.Validate(); err != nil {
		return err
	}
	if err := target.platform.Validate(); err != nil {
		return err
	}
	if target.taskType != TaskTypeSummary {
		return fmt.Errorf("detail tasks do not have collection targets")
	}
	if target.appID < 1 {
		return fmt.Errorf("summary target appid must be positive")
	}
	if err := validateSide(target.side); err != nil {
		return err
	}
	if err := target.desired.Validate(); err != nil {
		return err
	}
	if err := target.actual.Validate(); err != nil {
		return err
	}
	if err := target.reason.Validate(); err != nil {
		return err
	}
	if err := target.recovery.Validate(); err != nil {
		return err
	}
	if err := validateTime("changed_at", target.changedAt); err != nil {
		return err
	}
	if !target.recheckAt.IsZero() {
		if err := validateTime("recheck_at", target.recheckAt); err != nil {
			return err
		}
	}
	if err := validateDesiredActual(target.desired, target.actual); err != nil {
		return err
	}
	if err := validateActualContext(target.actual, target.reason, target.recovery, target.recheckAt); err != nil {
		return err
	}
	if target.recovery == RecoveryAutomatic && !target.recheckAt.After(target.changedAt) {
		return fmt.Errorf("recheck_at must be after changed_at")
	}
	return nil
}

func validateDesiredActual(desired DesiredState, actual ActualState) error {
	if desired == DesiredEnabled {
		switch actual {
		case ActualStarting, ActualWaiting, ActualRunning, ActualBlocked, ActualError:
			return nil
		default:
			return fmt.Errorf("enabled target cannot be %s", actual)
		}
	}
	switch actual {
	case ActualStopping, ActualStopped:
		return nil
	default:
		return fmt.Errorf("disabled target cannot be %s", actual)
	}
}

func validateActualContext(actual ActualState, reason TargetReason, recovery RecoveryMode, recheckAt time.Time) error {
	switch actual {
	case ActualWaiting:
		if !isWaitingReason(reason) {
			return fmt.Errorf("waiting target has incompatible reason %q", reason)
		}
		if recovery != RecoveryAutomatic || recheckAt.IsZero() {
			return fmt.Errorf("waiting target requires automatic recovery and recheck_at")
		}
		return nil
	case ActualBlocked:
		expected, ok := blockedRecovery(reason)
		if !ok {
			return fmt.Errorf("blocked target has incompatible reason %q", reason)
		}
		if recovery != expected {
			return fmt.Errorf("blocked target reason %q requires %s recovery", reason, expected)
		}
		if expected == RecoveryAutomatic && recheckAt.IsZero() {
			return fmt.Errorf("automatically blocked target requires recheck_at")
		}
		if expected == RecoveryManual && !recheckAt.IsZero() {
			return fmt.Errorf("manually blocked target must not have recheck_at")
		}
		return nil
	case ActualError:
		if reason != TargetReasonSchedulerFailure && reason != TargetReasonStateIntegrity {
			return fmt.Errorf("error target has incompatible reason %q", reason)
		}
		if recovery != RecoveryManual || !recheckAt.IsZero() {
			return fmt.Errorf("error target requires manual recovery without recheck_at")
		}
		return nil
	default:
		if reason != TargetReasonNone || recovery != RecoveryNone || !recheckAt.IsZero() {
			return fmt.Errorf("%s target must not have reason, recovery, or recheck_at", actual)
		}
		return nil
	}
}

func isWaitingReason(reason TargetReason) bool {
	return reason == TargetReasonNextCycle ||
		reason == TargetReasonSchedulerOpportunity ||
		reason == TargetReasonTransientFailure
}

func blockedRecovery(reason TargetReason) (RecoveryMode, bool) {
	switch reason {
	case TargetReasonNoCombination, TargetReasonCooldown, TargetReasonEgressUnavailable:
		return RecoveryAutomatic, true
	case TargetReasonMissingRatePolicy,
		TargetReasonSessionInvalid,
		TargetReasonInvalidConfig,
		TargetReasonInterfaceUnverified:
		return RecoveryManual, true
	default:
		return RecoveryNone, false
	}
}

// Enable accepts an enable request after the previous stop completed.
func (target Target) Enable(at time.Time) (Target, error) {
	if err := target.validateChangeTime(at); err != nil {
		return Target{}, err
	}
	if target.desired == DesiredEnabled {
		return target, nil
	}
	if target.actual != ActualStopped {
		return Target{}, fmt.Errorf("target cannot be enabled before it is stopped")
	}
	nextSwitch, err := nextRevision(target.switchVersion)
	if err != nil {
		return Target{}, fmt.Errorf("switch_version cannot advance")
	}
	target.switchVersion = nextSwitch
	target.desired = DesiredEnabled
	return target.changeActual(ActualStarting, TargetReasonNone, RecoveryNone, time.Time{}, at)
}

// Disable accepts a disable request and begins stopping the target.
func (target Target) Disable(at time.Time) (Target, error) {
	if err := target.validateChangeTime(at); err != nil {
		return Target{}, err
	}
	if target.desired == DesiredDisabled {
		return target, nil
	}
	nextSwitch, err := nextRevision(target.switchVersion)
	if err != nil {
		return Target{}, fmt.Errorf("switch_version cannot advance")
	}
	target.switchVersion = nextSwitch
	target.desired = DesiredDisabled
	return target.changeActual(ActualStopping, TargetReasonNone, RecoveryNone, time.Time{}, at)
}

// MarkWaiting records a scheduled automatic recheck.
func (target Target) MarkWaiting(reason TargetReason, recheckAt, at time.Time) (Target, error) {
	if !isWaitingReason(reason) {
		return Target{}, fmt.Errorf("incompatible waiting reason %q", reason)
	}
	if err := validateTime("recheck_at", recheckAt); err != nil {
		return Target{}, err
	}
	if !recheckAt.After(at) {
		return Target{}, fmt.Errorf("recheck_at must be after changed_at")
	}
	if err := target.requireSchedulerTransition(at); err != nil {
		return Target{}, err
	}
	return target.changeActual(ActualWaiting, reason, RecoveryAutomatic, recheckAt, at)
}

// MarkBlocked records a controlled automatic or manual blocker.
func (target Target) MarkBlocked(reason TargetReason, recheckAt *time.Time, at time.Time) (Target, error) {
	recovery, ok := blockedRecovery(reason)
	if !ok {
		return Target{}, fmt.Errorf("incompatible blocked reason %q", reason)
	}
	recheck := timeValue(recheckAt)
	if recovery == RecoveryAutomatic {
		if err := validateTime("recheck_at", recheck); err != nil {
			return Target{}, err
		}
		if !recheck.After(at) {
			return Target{}, fmt.Errorf("recheck_at must be after changed_at")
		}
	} else if recheckAt != nil {
		return Target{}, fmt.Errorf("manual blocker must not have recheck_at")
	}
	if target.actual == ActualBlocked && target.reason == reason && target.recovery == recovery && target.recheckAt.Equal(recheck) {
		if err := target.validateChangeTime(at); err != nil {
			return Target{}, err
		}
		return target, nil
	}
	if err := target.requireSchedulerTransition(at); err != nil {
		return Target{}, err
	}
	return target.changeActual(ActualBlocked, reason, recovery, recheck, at)
}

// MarkRunning records that the scheduler dispatched a run.
func (target Target) MarkRunning(at time.Time) (Target, error) {
	if err := target.requireSchedulerTransition(at); err != nil {
		return Target{}, err
	}
	switch target.actual {
	case ActualWaiting, ActualBlocked:
		if target.recovery != RecoveryAutomatic || at.Before(target.recheckAt) {
			return Target{}, fmt.Errorf("target is not ready for automatic recovery")
		}
		return target.changeActual(ActualRunning, TargetReasonNone, RecoveryNone, time.Time{}, at)
	case ActualStarting, ActualRunning:
		return target.changeActual(ActualRunning, TargetReasonNone, RecoveryNone, time.Time{}, at)
	default:
		return Target{}, fmt.Errorf("target cannot run from %s", target.actual)
	}
}

// MarkStopped completes a previously accepted disable request.
func (target Target) MarkStopped(at time.Time) (Target, error) {
	if err := target.validateChangeTime(at); err != nil {
		return Target{}, err
	}
	if target.desired != DesiredDisabled {
		return Target{}, fmt.Errorf("enabled target cannot be stopped")
	}
	if target.actual == ActualStopped {
		return target, nil
	}
	if target.actual != ActualStopping {
		return Target{}, fmt.Errorf("target cannot stop from %s", target.actual)
	}
	return target.changeActual(ActualStopped, TargetReasonNone, RecoveryNone, time.Time{}, at)
}

// MarkError records a scheduler failure that requires manual recovery.
func (target Target) MarkError(reason TargetReason, at time.Time) (Target, error) {
	if reason != TargetReasonSchedulerFailure && reason != TargetReasonStateIntegrity {
		return Target{}, fmt.Errorf("incompatible error reason %q", reason)
	}
	if target.actual == ActualError && target.reason == reason && target.recovery == RecoveryManual {
		if err := target.validateChangeTime(at); err != nil {
			return Target{}, err
		}
		return target, nil
	}
	if err := target.requireSchedulerTransition(at); err != nil {
		return Target{}, err
	}
	return target.changeActual(ActualError, reason, RecoveryManual, time.Time{}, at)
}

// Recover records an explicit operator recovery from a manual blocker or
// scheduler error. The scheduler must subsequently re-evaluate the target.
func (target Target) Recover(at time.Time) (Target, error) {
	if err := target.requireEnabledTransition(at); err != nil {
		return Target{}, err
	}
	if target.recovery != RecoveryManual || (target.actual != ActualBlocked && target.actual != ActualError) {
		return Target{}, fmt.Errorf("target does not require manual recovery")
	}
	return target.changeActual(ActualStarting, TargetReasonNone, RecoveryNone, time.Time{}, at)
}

func (target Target) requireSchedulerTransition(at time.Time) error {
	if err := target.requireEnabledTransition(at); err != nil {
		return err
	}
	if target.recovery == RecoveryManual {
		return fmt.Errorf("target requires explicit manual recovery")
	}
	return nil
}

func (target Target) requireEnabledTransition(at time.Time) error {
	if err := target.validateChangeTime(at); err != nil {
		return err
	}
	if target.desired != DesiredEnabled {
		return fmt.Errorf("disabled target cannot enter an active state")
	}
	return nil
}

func (target Target) validateChangeTime(at time.Time) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if err := validateTime("changed_at", at); err != nil {
		return err
	}
	if at.Before(target.changedAt) {
		return fmt.Errorf("changed_at cannot move backwards")
	}
	return nil
}

func (target Target) changeActual(actual ActualState, reason TargetReason, recovery RecoveryMode, recheckAt, at time.Time) (Target, error) {
	if target.actual == actual && target.reason == reason && target.recovery == recovery && target.recheckAt.Equal(recheckAt) {
		return target, nil
	}
	next, err := nextRevision(target.revision)
	if err != nil {
		return Target{}, err
	}
	target.revision = next
	target.actual = actual
	target.reason = reason
	target.recovery = recovery
	target.recheckAt = recheckAt
	target.changedAt = at
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

// ID returns the target identity.
func (target Target) ID() TargetID { return target.id }

// Revision returns the target state revision.
func (target Target) Revision() Revision { return target.revision }

// TaskType returns summary.
func (target Target) TaskType() TaskType { return target.taskType }

// Platform returns the target platform.
func (target Target) Platform() Platform { return target.platform }

// AppID returns the summary game identity.
func (target Target) AppID() (int64, bool) { return target.appID, target.taskType == TaskTypeSummary }

// Side returns the summary side when present.
func (target Target) Side() (market.Side, bool) {
	return target.side, target.taskType == TaskTypeSummary
}

// Desired returns the accepted desired state.
func (target Target) Desired() DesiredState { return target.desired }

// Actual returns the current scheduler state.
func (target Target) Actual() ActualState { return target.actual }

// SwitchVersion returns the enable or disable fence.
func (target Target) SwitchVersion() Revision { return target.switchVersion }

// Reason returns the controlled target reason.
func (target Target) Reason() TargetReason { return target.reason }

// Recovery returns how the target can be reconsidered.
func (target Target) Recovery() RecoveryMode { return target.recovery }

// RecheckAt returns the next automatic reconsideration time when present.
func (target Target) RecheckAt() (time.Time, bool) {
	return target.recheckAt, !target.recheckAt.IsZero()
}

// ChangedAt returns the time of the latest target state change.
func (target Target) ChangedAt() time.Time { return target.changedAt }
