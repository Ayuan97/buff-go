package collection

import (
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/market"
)

func mustCursor(t *testing.T, value string) Cursor {
	t.Helper()
	cursor, err := NewCursor([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func catalogRunInput(t *testing.T) CatalogRunInput {
	t.Helper()
	return CatalogRunInput{
		ID:            1,
		TargetID:      2,
		AppID:         730,
		SwitchVersion: 3,
		RunSequence:   4,
		State:         RunPending,
		CurrentCursor: mustCursor(t, "first"),
		CreatedAt:     targetTime(0),
	}
}

func summaryRunInput(t *testing.T) SummaryRunInput {
	t.Helper()
	return SummaryRunInput{
		ID:            5,
		TargetID:      6,
		Platform:      "buff",
		AppID:         730,
		Side:          market.SideBid,
		SwitchVersion: 7,
		RunSequence:   8,
		State:         RunPending,
		CreatedAt:     targetTime(0),
	}
}

func detailRunInput(t *testing.T) DetailRunInput {
	t.Helper()
	return DetailRunInput{
		ID:          9,
		Platform:    "steam",
		AppID:       730,
		Side:        market.SideAsk,
		ProductID:   catalog.ProductID(10),
		RunSequence: 11,
		State:       RunPending,
		CreatedAt:   targetTime(0),
	}
}

func TestRunConstructorsEnforceTaskShapes(t *testing.T) {
	catalogCases := []struct {
		name   string
		mutate func(*CatalogRunInput)
	}{
		{"zero id", func(input *CatalogRunInput) { input.ID = 0 }},
		{"zero target", func(input *CatalogRunInput) { input.TargetID = 0 }},
		{"zero appid", func(input *CatalogRunInput) { input.AppID = 0 }},
		{"zero switch", func(input *CatalogRunInput) { input.SwitchVersion = 0 }},
		{"zero sequence", func(input *CatalogRunInput) { input.RunSequence = 0 }},
		{"negative last page", func(input *CatalogRunInput) { input.LastPageSequence = -1 }},
		{"pending page", func(input *CatalogRunInput) {
			input.LastPageSequence = 1
			started := targetTime(0)
			input.StartedAt = &started
		}},
		{"non UTC created", func(input *CatalogRunInput) { input.CreatedAt = input.CreatedAt.In(time.FixedZone("offset", 0)) }},
		{"nanosecond created", func(input *CatalogRunInput) { input.CreatedAt = input.CreatedAt.Add(time.Nanosecond) }},
	}
	for _, test := range catalogCases {
		t.Run("catalog "+test.name, func(t *testing.T) {
			input := catalogRunInput(t)
			test.mutate(&input)
			if _, err := NewCatalogRun(input); err == nil {
				t.Fatal("NewCatalogRun() accepted invalid input")
			}
		})
	}

	summaryCases := []struct {
		name   string
		mutate func(*SummaryRunInput)
	}{
		{"invalid platform", func(input *SummaryRunInput) { input.Platform = "BUFF" }},
		{"invalid side", func(input *SummaryRunInput) { input.Side = "sell" }},
		{"zero appid", func(input *SummaryRunInput) { input.AppID = 0 }},
		{"zero target", func(input *SummaryRunInput) { input.TargetID = 0 }},
	}
	for _, test := range summaryCases {
		t.Run("summary "+test.name, func(t *testing.T) {
			input := summaryRunInput(t)
			test.mutate(&input)
			if _, err := NewSummaryRun(input); err == nil {
				t.Fatal("NewSummaryRun() accepted invalid input")
			}
		})
	}

	detailCases := []struct {
		name   string
		mutate func(*DetailRunInput)
	}{
		{"invalid platform", func(input *DetailRunInput) { input.Platform = "" }},
		{"invalid side", func(input *DetailRunInput) { input.Side = "sell" }},
		{"zero appid", func(input *DetailRunInput) { input.AppID = 0 }},
		{"zero product", func(input *DetailRunInput) { input.ProductID = 0 }},
	}
	for _, test := range detailCases {
		t.Run("detail "+test.name, func(t *testing.T) {
			input := detailRunInput(t)
			test.mutate(&input)
			if _, err := NewDetailRun(input); err == nil {
				t.Fatal("NewDetailRun() accepted invalid input")
			}
		})
	}
}

func TestRunLifecycleShapeMatrix(t *testing.T) {
	started := targetTime(1)
	finished := targetTime(2)
	tests := []struct {
		name         string
		state        RunState
		completeness Completeness
		reason       RunReason
		startedAt    *time.Time
		finishedAt   *time.Time
		lastPage     int64
		wantErr      bool
	}{
		{"pending", RunPending, CompletenessNone, RunReasonNone, nil, nil, 0, false},
		{"running", RunRunning, CompletenessNone, RunReasonNone, &started, nil, 0, false},
		{"succeeded complete", RunSucceeded, CompletenessComplete, RunReasonNone, &started, &finished, 1, false},
		{"succeeded partial", RunSucceeded, CompletenessPartial, RunReasonNone, &started, &finished, 1, false},
		{"failed before start partial", RunFailed, CompletenessPartial, RunReasonTimeout, nil, &finished, 0, false},
		{"stopped before start partial", RunStopped, CompletenessPartial, RunReasonSwitchDisabled, nil, &finished, 0, false},
		{"stopped after page partial", RunStopped, CompletenessPartial, RunReasonCancelled, &started, &finished, 1, false},
		{"active completeness", RunRunning, CompletenessPartial, RunReasonNone, &started, nil, 0, true},
		{"active reason", RunRunning, CompletenessNone, RunReasonTimeout, &started, nil, 0, true},
		{"running without start", RunRunning, CompletenessNone, RunReasonNone, nil, nil, 0, true},
		{"terminal without completeness", RunFailed, CompletenessNone, RunReasonTimeout, &started, &finished, 0, true},
		{"succeeded without start", RunSucceeded, CompletenessComplete, RunReasonNone, nil, &finished, 0, true},
		{"succeeded with reason", RunSucceeded, CompletenessComplete, RunReasonTimeout, &started, &finished, 0, true},
		{"failed with stop reason", RunFailed, CompletenessPartial, RunReasonCancelled, &started, &finished, 0, true},
		{"stopped with failure reason", RunStopped, CompletenessPartial, RunReasonNetworkError, &started, &finished, 0, true},
		{"failed before start complete", RunFailed, CompletenessComplete, RunReasonTimeout, nil, &finished, 0, true},
		{"stopped before start complete", RunStopped, CompletenessComplete, RunReasonCancelled, nil, &finished, 0, true},
		{"page without start", RunFailed, CompletenessPartial, RunReasonTimeout, nil, &finished, 1, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := catalogRunInput(t)
			input.State = test.state
			input.Completeness = test.completeness
			input.Reason = test.reason
			input.StartedAt = test.startedAt
			input.FinishedAt = test.finishedAt
			input.LastPageSequence = test.lastPage
			_, err := NewCatalogRun(input)
			if (err != nil) != test.wantErr {
				t.Fatalf("NewCatalogRun() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestRunTransitionsAndTerminalImmutability(t *testing.T) {
	pending, err := NewCatalogRun(catalogRunInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pending.Succeed(CompletenessComplete, targetTime(1)); err == nil {
		t.Fatal("pending run succeeded without starting")
	}
	if _, err := pending.Fail(CompletenessComplete, RunReasonTimeout, targetTime(1)); err == nil {
		t.Fatal("pending run failed as complete")
	}
	failed, err := pending.Fail(CompletenessPartial, RunReasonTimeout, targetTime(1))
	if err != nil {
		t.Fatal(err)
	}
	if failed.State() != RunFailed || failed.Completeness() != CompletenessPartial {
		t.Fatalf("failed run = %+v", failed)
	}
	if _, present := failed.StartedAt(); present {
		t.Fatal("pending failure unexpectedly has started_at")
	}
	for _, operation := range []struct {
		name string
		call func() error
	}{
		{"begin", func() error { _, err := failed.Begin(targetTime(2)); return err }},
		{"succeed", func() error { _, err := failed.Succeed(CompletenessComplete, targetTime(2)); return err }},
		{"fail", func() error { _, err := failed.Fail(CompletenessPartial, RunReasonTimeout, targetTime(2)); return err }},
		{"stop", func() error {
			_, err := failed.Stop(CompletenessPartial, RunReasonCancelled, targetTime(2))
			return err
		}},
	} {
		t.Run("terminal "+operation.name, func(t *testing.T) {
			if err := operation.call(); err == nil {
				t.Fatal("terminal mutation succeeded")
			}
		})
	}

	running, err := pending.Begin(targetTime(1))
	if err != nil {
		t.Fatal(err)
	}
	same, err := running.Begin(targetTime(2))
	if err != nil || same.State() != RunRunning {
		t.Fatalf("idempotent Begin() = %+v, %v", same, err)
	}
	page, err := NewPage(PageInput{
		RunID: running.ID(), PageSequence: 1, CursorBefore: running.CurrentCursor(), PayloadDigest: [32]byte{1},
		CollectedAt: targetTime(2), CommittedAt: targetTime(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	runningWithPage, err := running.CommitPage(page)
	if err != nil {
		t.Fatal(err)
	}
	succeeded, err := runningWithPage.Succeed(CompletenessComplete, targetTime(3))
	if err != nil {
		t.Fatal(err)
	}
	if succeeded.State() != RunSucceeded || succeeded.Reason() != RunReasonNone {
		t.Fatalf("succeeded run = %+v", succeeded)
	}
	stopped, err := running.Stop(CompletenessPartial, RunReasonSwitchDisabled, targetTime(2))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State() != RunStopped || stopped.Completeness() != CompletenessPartial {
		t.Fatalf("stopped run = %+v", stopped)
	}
}

func TestRunRejectsTimestampRegression(t *testing.T) {
	input := catalogRunInput(t)
	started := targetTime(2)
	finished := targetTime(1)
	input.State = RunFailed
	input.Completeness = CompletenessPartial
	input.Reason = RunReasonTimeout
	input.StartedAt = &started
	input.FinishedAt = &finished
	if _, err := NewCatalogRun(input); err == nil {
		t.Fatal("NewCatalogRun accepted finished_at before started_at")
	}

	pending, err := NewCatalogRun(catalogRunInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pending.Begin(targetTime(0).Add(-time.Microsecond)); err == nil {
		t.Fatal("Begin accepted started_at before created_at")
	}
	if _, err := pending.Stop(CompletenessPartial, RunReasonCancelled, targetTime(0).Add(time.Nanosecond)); err == nil {
		t.Fatal("Stop accepted nanosecond precision")
	}
}
