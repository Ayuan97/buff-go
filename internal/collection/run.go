package collection

import (
	"fmt"
	"math"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/market"
)

// SummaryRunInput is the persisted shape of one summary run.
type SummaryRunInput struct {
	ID               RunID
	TargetID         TargetID
	Platform         Platform
	AppID            int64
	Side             market.Side
	SwitchVersion    Revision
	RunSequence      Sequence
	State            RunState
	Completeness     Completeness
	Reason           RunReason
	CurrentCursor    Cursor
	LastPageSequence int64
	CreatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
}

// DetailRunInput is the persisted shape of one detail run.
type DetailRunInput struct {
	ID               RunID
	Platform         Platform
	AppID            int64
	Side             market.Side
	ProductID        catalog.ProductID
	RunSequence      Sequence
	State            RunState
	Completeness     Completeness
	Reason           RunReason
	CurrentCursor    Cursor
	LastPageSequence int64
	CreatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
}

// Run is an immutable summary or detail execution.
type Run struct {
	id               RunID
	taskType         TaskType
	targetID         TargetID
	platform         Platform
	appID            int64
	side             market.Side
	productID        catalog.ProductID
	switchVersion    Revision
	runSequence      Sequence
	state            RunState
	completeness     Completeness
	reason           RunReason
	currentCursor    Cursor
	lastPageSequence int64
	createdAt        time.Time
	startedAt        time.Time
	finishedAt       time.Time
}

// NewSummaryRun validates a summary run restored from persistence.
func NewSummaryRun(input SummaryRunInput) (Run, error) {
	run := Run{
		id:               input.ID,
		taskType:         TaskTypeSummary,
		targetID:         input.TargetID,
		platform:         input.Platform,
		appID:            input.AppID,
		side:             input.Side,
		switchVersion:    input.SwitchVersion,
		runSequence:      input.RunSequence,
		state:            input.State,
		completeness:     input.Completeness,
		reason:           input.Reason,
		currentCursor:    input.CurrentCursor.clone(),
		lastPageSequence: input.LastPageSequence,
		createdAt:        input.CreatedAt,
		startedAt:        timeValue(input.StartedAt),
		finishedAt:       timeValue(input.FinishedAt),
	}
	return validatedRun(run)
}

// NewDetailRun validates a detail run without creating a rule or target.
func NewDetailRun(input DetailRunInput) (Run, error) {
	run := Run{
		id:               input.ID,
		taskType:         TaskTypeDetail,
		platform:         input.Platform,
		appID:            input.AppID,
		side:             input.Side,
		productID:        input.ProductID,
		runSequence:      input.RunSequence,
		state:            input.State,
		completeness:     input.Completeness,
		reason:           input.Reason,
		currentCursor:    input.CurrentCursor.clone(),
		lastPageSequence: input.LastPageSequence,
		createdAt:        input.CreatedAt,
		startedAt:        timeValue(input.StartedAt),
		finishedAt:       timeValue(input.FinishedAt),
	}
	return validatedRun(run)
}

func validatedRun(run Run) (Run, error) {
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	return run, nil
}

// Validate checks task shape, lifecycle state, cursors, and timestamps.
func (run Run) Validate() error {
	if err := run.id.Validate(); err != nil {
		return err
	}
	if err := run.taskType.Validate(); err != nil {
		return err
	}
	if err := run.platform.Validate(); err != nil {
		return err
	}
	if run.appID < 1 {
		return fmt.Errorf("run appid must be positive")
	}
	if err := run.runSequence.Validate(); err != nil {
		return fmt.Errorf("run_sequence: %w", err)
	}
	if err := run.currentCursor.Validate(); err != nil {
		return fmt.Errorf("current_cursor: %w", err)
	}
	if run.lastPageSequence < 0 {
		return fmt.Errorf("last_page_sequence cannot be negative")
	}
	switch run.taskType {
	case TaskTypeSummary:
		if err := run.targetID.Validate(); err != nil {
			return err
		}
		if err := validateSide(run.side); err != nil {
			return err
		}
		if run.productID != 0 {
			return fmt.Errorf("summary run must not have a product_id")
		}
		if err := run.switchVersion.Validate(); err != nil {
			return fmt.Errorf("switch_version: %w", err)
		}
	case TaskTypeDetail:
		if run.targetID != 0 || run.switchVersion != 0 {
			return fmt.Errorf("detail run must not have target_id or switch_version")
		}
		if err := validateSide(run.side); err != nil {
			return err
		}
		if run.productID < 1 {
			return fmt.Errorf("detail run product_id must be positive")
		}
	}
	if err := run.state.Validate(); err != nil {
		return err
	}
	if err := run.reason.Validate(); err != nil {
		return err
	}
	if err := validateTime("created_at", run.createdAt); err != nil {
		return err
	}
	if !run.startedAt.IsZero() {
		if err := validateTime("started_at", run.startedAt); err != nil {
			return err
		}
		if run.startedAt.Before(run.createdAt) {
			return fmt.Errorf("started_at cannot precede created_at")
		}
	}
	if !run.finishedAt.IsZero() {
		if err := validateTime("finished_at", run.finishedAt); err != nil {
			return err
		}
		if run.finishedAt.Before(run.createdAt) {
			return fmt.Errorf("finished_at cannot precede created_at")
		}
		if !run.startedAt.IsZero() && run.finishedAt.Before(run.startedAt) {
			return fmt.Errorf("finished_at cannot precede started_at")
		}
	}
	if run.lastPageSequence > 0 && run.startedAt.IsZero() {
		return fmt.Errorf("committed pages require started_at")
	}
	return run.validateLifecycle()
}

func (run Run) validateLifecycle() error {
	if run.completeness == CompletenessComplete && run.lastPageSequence == 0 {
		return fmt.Errorf("complete run requires a committed page")
	}
	switch run.state {
	case RunPending:
		if run.completeness != CompletenessNone || run.reason != RunReasonNone || !run.startedAt.IsZero() || !run.finishedAt.IsZero() {
			return fmt.Errorf("pending run cannot have completeness, reason, started_at, or finished_at")
		}
		if run.lastPageSequence != 0 {
			return fmt.Errorf("pending run cannot have committed pages")
		}
		return nil
	case RunRunning:
		if run.completeness != CompletenessNone || run.reason != RunReasonNone || run.startedAt.IsZero() || !run.finishedAt.IsZero() {
			return fmt.Errorf("running run requires started_at and no completeness, reason, or finished_at")
		}
		return nil
	case RunSucceeded:
		if err := run.completeness.ValidateTerminal(); err != nil {
			return err
		}
		if run.reason != RunReasonNone || run.startedAt.IsZero() || run.finishedAt.IsZero() || run.lastPageSequence == 0 {
			return fmt.Errorf("succeeded run requires a committed page, started_at, and finished_at without a reason")
		}
		return nil
	case RunFailed:
		if err := run.completeness.ValidateTerminal(); err != nil {
			return err
		}
		if !isFailureReason(run.reason) || run.finishedAt.IsZero() {
			return fmt.Errorf("failed run requires a failure reason and finished_at")
		}
		if run.startedAt.IsZero() && run.completeness != CompletenessPartial {
			return fmt.Errorf("run finished before start must be partial")
		}
		return nil
	case RunStopped:
		if err := run.completeness.ValidateTerminal(); err != nil {
			return err
		}
		if !isStopReason(run.reason) || run.finishedAt.IsZero() {
			return fmt.Errorf("stopped run requires a stop reason and finished_at")
		}
		if run.startedAt.IsZero() && run.completeness != CompletenessPartial {
			return fmt.Errorf("run finished before start must be partial")
		}
		return nil
	default:
		return fmt.Errorf("invalid run state %q", run.state)
	}
}

func isFailureReason(reason RunReason) bool {
	switch reason {
	case RunReasonNetworkError,
		RunReasonPlatformError,
		RunReasonTimeout,
		RunReasonLoginInvalid,
		RunReasonParseError,
		RunReasonSemanticError,
		RunReasonConfigurationError,
		RunReasonInternalError,
		RunReasonProcessRestarted:
		return true
	default:
		return false
	}
}

func isStopReason(reason RunReason) bool {
	return reason == RunReasonSwitchDisabled || reason == RunReasonCancelled
}

// Begin moves a pending run to running.
func (run Run) Begin(at time.Time) (Run, error) {
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	if run.state.Terminal() {
		return Run{}, fmt.Errorf("terminal run is immutable")
	}
	if run.state == RunRunning {
		return run, nil
	}
	if err := run.validateEventTime("started_at", at); err != nil {
		return Run{}, err
	}
	run.state = RunRunning
	run.startedAt = at
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	return run, nil
}

// Succeed completes a running run with explicit coverage.
func (run Run) Succeed(completeness Completeness, at time.Time) (Run, error) {
	if run.state != RunRunning {
		if run.state.Terminal() {
			return Run{}, fmt.Errorf("terminal run is immutable")
		}
		return Run{}, fmt.Errorf("only a running run can succeed")
	}
	return run.finish(RunSucceeded, completeness, RunReasonNone, at)
}

// Fail completes a pending or running run with a controlled failure reason.
func (run Run) Fail(completeness Completeness, reason RunReason, at time.Time) (Run, error) {
	if !isFailureReason(reason) {
		return Run{}, fmt.Errorf("incompatible failure reason %q", reason)
	}
	return run.finish(RunFailed, completeness, reason, at)
}

// Stop completes a pending or running run with a controlled stop reason.
func (run Run) Stop(completeness Completeness, reason RunReason, at time.Time) (Run, error) {
	if !isStopReason(reason) {
		return Run{}, fmt.Errorf("incompatible stop reason %q", reason)
	}
	return run.finish(RunStopped, completeness, reason, at)
}

func (run Run) finish(state RunState, completeness Completeness, reason RunReason, at time.Time) (Run, error) {
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	if run.state.Terminal() {
		return Run{}, fmt.Errorf("terminal run is immutable")
	}
	if err := completeness.ValidateTerminal(); err != nil {
		return Run{}, err
	}
	if err := run.validateEventTime("finished_at", at); err != nil {
		return Run{}, err
	}
	run.state = state
	run.completeness = completeness
	run.reason = reason
	run.finishedAt = at
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (run Run) validateEventTime(name string, at time.Time) error {
	if err := validateTime(name, at); err != nil {
		return err
	}
	if at.Before(run.createdAt) {
		return fmt.Errorf("%s cannot precede created_at", name)
	}
	if !run.startedAt.IsZero() && at.Before(run.startedAt) {
		return fmt.Errorf("%s cannot precede started_at", name)
	}
	return nil
}

// CommitPage advances a running run by one contiguous committed page.
func (run Run) CommitPage(page Page) (Run, error) {
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	if run.state != RunRunning {
		if run.state.Terminal() {
			return Run{}, fmt.Errorf("terminal run is immutable")
		}
		return Run{}, fmt.Errorf("only a running run can commit a page")
	}
	if err := page.Validate(); err != nil {
		return Run{}, err
	}
	if page.runID != run.id {
		return Run{}, fmt.Errorf("page run_id does not match run")
	}
	if run.lastPageSequence == math.MaxInt64 || int64(page.pageSequence) != run.lastPageSequence+1 {
		return Run{}, fmt.Errorf("page_sequence must immediately follow last_page_sequence")
	}
	if !run.currentCursor.Equal(page.cursorBefore) {
		return Run{}, fmt.Errorf("cursor_before does not match current_cursor")
	}
	if page.collectedAt.Before(run.startedAt) {
		return Run{}, fmt.Errorf("page collected_at cannot precede run started_at")
	}
	run.currentCursor = page.cursorAfter.clone()
	run.lastPageSequence = int64(page.pageSequence)
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	return run, nil
}

// ID returns the run identity.
func (run Run) ID() RunID { return run.id }

// TaskType returns the run task type.
func (run Run) TaskType() TaskType { return run.taskType }

// TargetID returns the controllable target identity when present.
func (run Run) TargetID() (TargetID, bool) { return run.targetID, run.taskType != TaskTypeDetail }

// Platform returns the run platform.
func (run Run) Platform() Platform { return run.platform }

// AppID returns the positive run appid.
func (run Run) AppID() int64 { return run.appID }

// Side returns the summary or detail side when present.
func (run Run) Side() (market.Side, bool) {
	return run.side, run.taskType == TaskTypeSummary || run.taskType == TaskTypeDetail
}

// ProductID returns the detail product identity when present.
func (run Run) ProductID() (catalog.ProductID, bool) {
	return run.productID, run.taskType == TaskTypeDetail
}

// SwitchVersion returns the target switch fence when present.
func (run Run) SwitchVersion() (Revision, bool) {
	return run.switchVersion, run.taskType != TaskTypeDetail
}

// RunSequence returns the positive run sequence.
func (run Run) RunSequence() Sequence { return run.runSequence }

// State returns the run lifecycle state.
func (run Run) State() RunState { return run.state }

// Completeness returns empty for active runs and explicit terminal coverage.
func (run Run) Completeness() Completeness { return run.completeness }

// Reason returns the controlled terminal reason.
func (run Run) Reason() RunReason { return run.reason }

// CurrentCursor returns an isolated copy of the next-page cursor.
func (run Run) CurrentCursor() Cursor { return run.currentCursor.clone() }

// LastPageSequence returns zero before the first committed page.
func (run Run) LastPageSequence() int64 { return run.lastPageSequence }

// CreatedAt returns the run creation time.
func (run Run) CreatedAt() time.Time { return run.createdAt }

// StartedAt returns the start time when present.
func (run Run) StartedAt() (time.Time, bool) { return run.startedAt, !run.startedAt.IsZero() }

// FinishedAt returns the terminal time when present.
func (run Run) FinishedAt() (time.Time, bool) { return run.finishedAt, !run.finishedAt.IsZero() }
