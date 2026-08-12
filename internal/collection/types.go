// Package collection defines collection targets, runs, pages, and state transitions.
package collection

import (
	"fmt"

	"buff-go/internal/market"
)

// TargetID identifies one controllable catalog or summary target.
type TargetID int64

// Validate rejects a missing target identity.
func (id TargetID) Validate() error {
	if id < 1 {
		return fmt.Errorf("target_id must be at least 1")
	}
	return nil
}

// RunID identifies one collection run.
type RunID int64

// Validate rejects a missing run identity.
func (id RunID) Validate() error {
	if id < 1 {
		return fmt.Errorf("run_id must be at least 1")
	}
	return nil
}

// Revision is a positive optimistic state version.
type Revision int64

// Validate rejects a missing revision.
func (revision Revision) Validate() error {
	if revision < 1 {
		return fmt.Errorf("revision must be at least 1")
	}
	return nil
}

// Sequence is a positive run or page sequence.
type Sequence int64

// Validate rejects a missing sequence.
func (sequence Sequence) Validate() error {
	if sequence < 1 {
		return fmt.Errorf("sequence must be at least 1")
	}
	return nil
}

// Platform is an open normalized platform identifier.
type Platform string

// Validate enforces ^[a-z][a-z0-9_.-]{0,31}$.
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

// TaskType identifies the collection shape.
type TaskType string

const (
	TaskTypeCatalog TaskType = "catalog"
	TaskTypeSummary TaskType = "summary"
	TaskTypeDetail  TaskType = "detail"
)

// Validate rejects an unknown task type.
func (taskType TaskType) Validate() error {
	switch taskType {
	case TaskTypeCatalog, TaskTypeSummary, TaskTypeDetail:
		return nil
	default:
		return fmt.Errorf("invalid task type %q", taskType)
	}
}

// DesiredState records whether a controllable target should run.
type DesiredState string

const (
	DesiredEnabled  DesiredState = "enabled"
	DesiredDisabled DesiredState = "disabled"
)

// Validate rejects an unknown desired state.
func (state DesiredState) Validate() error {
	switch state {
	case DesiredEnabled, DesiredDisabled:
		return nil
	default:
		return fmt.Errorf("invalid desired state %q", state)
	}
}

// ActualState records the scheduler state of one target.
type ActualState string

const (
	ActualStarting ActualState = "starting"
	ActualWaiting  ActualState = "waiting"
	ActualRunning  ActualState = "running"
	ActualBlocked  ActualState = "blocked"
	ActualStopping ActualState = "stopping"
	ActualStopped  ActualState = "stopped"
	ActualError    ActualState = "error"
)

// Validate rejects an unknown actual state.
func (state ActualState) Validate() error {
	switch state {
	case ActualStarting, ActualWaiting, ActualRunning, ActualBlocked, ActualStopping, ActualStopped, ActualError:
		return nil
	default:
		return fmt.Errorf("invalid actual state %q", state)
	}
}

// RunState records the lifecycle of one collection run.
type RunState string

const (
	RunPending   RunState = "pending"
	RunRunning   RunState = "running"
	RunSucceeded RunState = "succeeded"
	RunFailed    RunState = "failed"
	RunStopped   RunState = "stopped"
)

// Validate rejects an unknown run state.
func (state RunState) Validate() error {
	switch state {
	case RunPending, RunRunning, RunSucceeded, RunFailed, RunStopped:
		return nil
	default:
		return fmt.Errorf("invalid run state %q", state)
	}
}

// Terminal reports whether no further run mutation is allowed.
func (state RunState) Terminal() bool {
	return state == RunSucceeded || state == RunFailed || state == RunStopped
}

// Completeness describes coverage independently from a terminal run state.
type Completeness string

const (
	CompletenessNone     Completeness = ""
	CompletenessComplete Completeness = "complete"
	CompletenessPartial  Completeness = "partial"
)

// ValidateTerminal accepts only explicit terminal coverage.
func (completeness Completeness) ValidateTerminal() error {
	switch completeness {
	case CompletenessComplete, CompletenessPartial:
		return nil
	default:
		return fmt.Errorf("terminal run requires complete or partial completeness")
	}
}

// RecoveryMode describes how a target may leave waiting, blocked, or error.
type RecoveryMode string

const (
	RecoveryNone      RecoveryMode = ""
	RecoveryAutomatic RecoveryMode = "automatic"
	RecoveryManual    RecoveryMode = "manual"
)

// Validate rejects an unknown recovery mode.
func (mode RecoveryMode) Validate() error {
	switch mode {
	case RecoveryNone, RecoveryAutomatic, RecoveryManual:
		return nil
	default:
		return fmt.Errorf("invalid recovery mode %q", mode)
	}
}

// TargetReason is a controlled scheduler-facing target reason.
type TargetReason string

const (
	TargetReasonNone                 TargetReason = ""
	TargetReasonNextCycle            TargetReason = "next_cycle"
	TargetReasonSchedulerOpportunity TargetReason = "scheduler_opportunity"
	TargetReasonTransientFailure     TargetReason = "transient_failure"
	TargetReasonNoCombination        TargetReason = "no_combination"
	TargetReasonCooldown             TargetReason = "cooldown"
	TargetReasonEgressUnavailable    TargetReason = "egress_unavailable"
	TargetReasonMissingRatePolicy    TargetReason = "missing_rate_policy"
	TargetReasonSessionInvalid       TargetReason = "session_invalid"
	TargetReasonInvalidConfig        TargetReason = "invalid_config"
	TargetReasonInterfaceUnverified  TargetReason = "interface_unverified"
	TargetReasonSchedulerFailure     TargetReason = "scheduler_failure"
	TargetReasonStateIntegrity       TargetReason = "state_integrity"
)

// Validate rejects free-form target reasons.
func (reason TargetReason) Validate() error {
	switch reason {
	case TargetReasonNone,
		TargetReasonNextCycle,
		TargetReasonSchedulerOpportunity,
		TargetReasonTransientFailure,
		TargetReasonNoCombination,
		TargetReasonCooldown,
		TargetReasonEgressUnavailable,
		TargetReasonMissingRatePolicy,
		TargetReasonSessionInvalid,
		TargetReasonInvalidConfig,
		TargetReasonInterfaceUnverified,
		TargetReasonSchedulerFailure,
		TargetReasonStateIntegrity:
		return nil
	default:
		return fmt.Errorf("invalid target reason %q", reason)
	}
}

// RunReason is a controlled terminal reason for failed or stopped runs.
type RunReason string

const (
	RunReasonNone               RunReason = ""
	RunReasonNetworkError       RunReason = "network_error"
	RunReasonPlatformError      RunReason = "platform_error"
	RunReasonTimeout            RunReason = "timeout"
	RunReasonLoginInvalid       RunReason = "login_invalid"
	RunReasonParseError         RunReason = "parse_error"
	RunReasonSemanticError      RunReason = "semantic_error"
	RunReasonConfigurationError RunReason = "configuration_error"
	RunReasonInternalError      RunReason = "internal_error"
	RunReasonProcessRestarted   RunReason = "process_restarted"
	RunReasonSwitchDisabled     RunReason = "switch_disabled"
	RunReasonCancelled          RunReason = "cancelled"
)

// Validate rejects free-form run reasons.
func (reason RunReason) Validate() error {
	switch reason {
	case RunReasonNone,
		RunReasonNetworkError,
		RunReasonPlatformError,
		RunReasonTimeout,
		RunReasonLoginInvalid,
		RunReasonParseError,
		RunReasonSemanticError,
		RunReasonConfigurationError,
		RunReasonInternalError,
		RunReasonProcessRestarted,
		RunReasonSwitchDisabled,
		RunReasonCancelled:
		return nil
	default:
		return fmt.Errorf("invalid run reason %q", reason)
	}
}

func validateSide(side market.Side) error {
	if side != market.SideBid && side != market.SideAsk {
		return fmt.Errorf("side must be bid or ask")
	}
	return nil
}
