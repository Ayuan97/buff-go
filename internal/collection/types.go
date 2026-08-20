// Package collection defines collection targets, task queues, and state transitions.
package collection

import (
	"fmt"

	"buff-go/internal/market"
)

// QueueWatermark is the per-direction backlog. It is inventory, not concurrency.
const QueueWatermark = 100

// AskPageSize is one Steam search page.
const AskPageSize = 10

// BidBatchSize keeps one rate-limited HTTP request in each bid task.
const BidBatchSize = 1

// TargetID identifies one controllable summary target.
type TargetID int64

// Validate rejects a missing target identity.
func (id TargetID) Validate() error {
	if id < 1 {
		return fmt.Errorf("target_id must be at least 1")
	}
	return nil
}

// TaskID identifies one queued collection task.
type TaskID int64

// Validate rejects a missing task identity.
func (id TaskID) Validate() error {
	if id < 1 {
		return fmt.Errorf("task_id must be at least 1")
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

// Sequence is a positive enqueue or write sequence.
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

// Validate rejects an unknown task type. Catalog is not a schedulable task.
func (taskType TaskType) Validate() error {
	switch taskType {
	case TaskTypeSummary, TaskTypeDetail:
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

func validateSide(side market.Side) error {
	if side != market.SideBid && side != market.SideAsk {
		return fmt.Errorf("side must be bid or ask")
	}
	return nil
}
