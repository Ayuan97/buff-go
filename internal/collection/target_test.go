package collection

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"buff-go/internal/market"
)

func targetTime(second int) time.Time {
	return time.Date(2026, 8, 11, 12, 0, second, 123000, time.UTC)
}

func summaryTargetInput() SummaryTargetInput {
	recheck := targetTime(2)
	return SummaryTargetInput{
		ID:            2,
		Revision:      3,
		Platform:      "steam",
		AppID:         730,
		Side:          market.SideAsk,
		Desired:       DesiredEnabled,
		Actual:        ActualWaiting,
		SwitchVersion: 2,
		Reason:        TargetReasonNextCycle,
		Recovery:      RecoveryAutomatic,
		RecheckAt:     &recheck,
		ChangedAt:     targetTime(1),
	}
}

func stoppedSummaryTargetInput() SummaryTargetInput {
	return SummaryTargetInput{
		ID:            1,
		Revision:      1,
		Platform:      "steam",
		AppID:         730,
		Side:          market.SideAsk,
		Desired:       DesiredDisabled,
		Actual:        ActualStopped,
		SwitchVersion: 1,
		ChangedAt:     targetTime(0),
	}
}

func TestTargetConstructorsRejectInvalidShapes(t *testing.T) {
	summaryCases := []struct {
		name   string
		mutate func(*SummaryTargetInput)
	}{
		{"zero id", func(input *SummaryTargetInput) { input.ID = 0 }},
		{"zero revision", func(input *SummaryTargetInput) { input.Revision = 0 }},
		{"zero switch version", func(input *SummaryTargetInput) { input.SwitchVersion = 0 }},
		{"zero appid", func(input *SummaryTargetInput) { input.AppID = 0 }},
		{"invalid platform", func(input *SummaryTargetInput) { input.Platform = "Steam" }},
		{"invalid side", func(input *SummaryTargetInput) { input.Side = "sell" }},
		{"enabled stopped", func(input *SummaryTargetInput) {
			input.Desired = DesiredEnabled
			input.Actual = ActualStopped
			input.Reason = TargetReasonNone
			input.Recovery = RecoveryNone
			input.RecheckAt = nil
		}},
		{"missing waiting reason", func(input *SummaryTargetInput) { input.Reason = TargetReasonNone }},
		{"manual waiting", func(input *SummaryTargetInput) { input.Recovery = RecoveryManual }},
		{"missing waiting recheck", func(input *SummaryTargetInput) { input.RecheckAt = nil }},
		{"equal waiting recheck", func(input *SummaryTargetInput) { input.RecheckAt = &input.ChangedAt }},
		{"past waiting recheck", func(input *SummaryTargetInput) {
			value := input.ChangedAt.Add(-time.Microsecond)
			input.RecheckAt = &value
		}},
		{"reason on stopped", func(input *SummaryTargetInput) {
			*input = stoppedSummaryTargetInput()
			input.Reason = TargetReasonCooldown
		}},
		{"non UTC time", func(input *SummaryTargetInput) { input.ChangedAt = input.ChangedAt.In(time.FixedZone("offset", 0)) }},
		{"nanosecond time", func(input *SummaryTargetInput) { input.ChangedAt = input.ChangedAt.Add(time.Nanosecond) }},
		{"cs2 steam facets", func(input *SummaryTargetInput) {
			input.SteamFacets = SteamFacets{Cats: []string{"steamcat.armor"}}
		}},
	}
	for _, test := range summaryCases {
		t.Run("summary "+test.name, func(t *testing.T) {
			input := summaryTargetInput()
			test.mutate(&input)
			if _, err := NewSummaryTarget(input); err == nil {
				t.Fatal("NewSummaryTarget() accepted invalid input")
			}
		})
	}
}

func TestTargetReasonStateMatrix(t *testing.T) {
	autoRecheck := targetTime(2)
	tests := []struct {
		name      string
		actual    ActualState
		reason    TargetReason
		recovery  RecoveryMode
		recheckAt *time.Time
		wantErr   bool
	}{
		{"waiting next cycle", ActualWaiting, TargetReasonNextCycle, RecoveryAutomatic, &autoRecheck, false},
		{"waiting opportunity", ActualWaiting, TargetReasonSchedulerOpportunity, RecoveryAutomatic, &autoRecheck, false},
		{"waiting transient failure", ActualWaiting, TargetReasonTransientFailure, RecoveryAutomatic, &autoRecheck, false},
		{"blocked combination", ActualBlocked, TargetReasonNoCombination, RecoveryAutomatic, &autoRecheck, false},
		{"blocked cooldown", ActualBlocked, TargetReasonCooldown, RecoveryAutomatic, &autoRecheck, false},
		{"blocked egress", ActualBlocked, TargetReasonEgressUnavailable, RecoveryAutomatic, &autoRecheck, false},
		{"blocked policy", ActualBlocked, TargetReasonMissingRatePolicy, RecoveryManual, nil, false},
		{"blocked session", ActualBlocked, TargetReasonSessionInvalid, RecoveryManual, nil, false},
		{"blocked config", ActualBlocked, TargetReasonInvalidConfig, RecoveryManual, nil, false},
		{"blocked interface", ActualBlocked, TargetReasonInterfaceUnverified, RecoveryManual, nil, false},
		{"scheduler error", ActualError, TargetReasonSchedulerFailure, RecoveryManual, nil, false},
		{"integrity error", ActualError, TargetReasonStateIntegrity, RecoveryManual, nil, false},
		{"manual cooldown", ActualBlocked, TargetReasonCooldown, RecoveryManual, nil, true},
		{"automatic session", ActualBlocked, TargetReasonSessionInvalid, RecoveryAutomatic, &autoRecheck, true},
		{"recheck on manual block", ActualBlocked, TargetReasonInvalidConfig, RecoveryManual, &autoRecheck, true},
		{"waiting block reason", ActualWaiting, TargetReasonCooldown, RecoveryAutomatic, &autoRecheck, true},
		{"error transient reason", ActualError, TargetReasonTransientFailure, RecoveryManual, nil, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := summaryTargetInput()
			input.Actual = test.actual
			input.Reason = test.reason
			input.Recovery = test.recovery
			input.RecheckAt = test.recheckAt
			_, err := NewSummaryTarget(input)
			if (err != nil) != test.wantErr {
				t.Fatalf("NewSummaryTarget() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestTargetTransitionsAndFences(t *testing.T) {
	target, err := NewSummaryTarget(stoppedSummaryTargetInput())
	if err != nil {
		t.Fatal(err)
	}

	enabled, err := target.Enable(targetTime(1))
	if err != nil {
		t.Fatal(err)
	}
	if enabled.Desired() != DesiredEnabled || enabled.Actual() != ActualStarting || enabled.Revision() != 2 || enabled.SwitchVersion() != 2 {
		t.Fatalf("enabled target = %+v", enabled)
	}
	if !enabled.RefillCursor().Equal(Cursor{}) || enabled.RefillTotal() != 0 {
		t.Fatal("Enable must reset refill cursor")
	}
	if appID, ok := enabled.AppID(); !ok || appID != 730 {
		t.Fatalf("summary AppID() = %d, %v", appID, ok)
	}
	if target.Desired() != DesiredDisabled || target.Revision() != 1 {
		t.Fatal("Enable mutated its receiver")
	}
	unchanged, err := enabled.Enable(targetTime(1))
	if err != nil || !reflect.DeepEqual(unchanged, enabled) {
		t.Fatalf("idempotent Enable() = %+v, %v", unchanged, err)
	}

	waiting, err := enabled.MarkWaiting(TargetReasonSchedulerOpportunity, targetTime(3), targetTime(2))
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Revision() != 3 || waiting.SwitchVersion() != 2 || waiting.Recovery() != RecoveryAutomatic {
		t.Fatalf("waiting target = %+v", waiting)
	}
	running, err := waiting.MarkRunning(targetTime(3))
	if err != nil {
		t.Fatal(err)
	}
	afterFailure, err := running.MarkWaiting(TargetReasonTransientFailure, targetTime(5), targetTime(4))
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.Actual() != ActualWaiting || afterFailure.Reason() != TargetReasonTransientFailure {
		t.Fatalf("transient failure recovery = %+v", afterFailure)
	}

	disabled, err := afterFailure.Disable(targetTime(5))
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Desired() != DesiredDisabled || disabled.Actual() != ActualStopping || disabled.Revision() != afterFailure.Revision()+1 || disabled.SwitchVersion() != afterFailure.SwitchVersion()+1 {
		t.Fatalf("disabled target = %+v", disabled)
	}
	if _, err := disabled.Enable(targetTime(5)); err == nil {
		t.Fatal("Enable accepted a stopping target")
	}
	stopped, err := disabled.MarkStopped(targetTime(6))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Actual() != ActualStopped || stopped.Revision() != disabled.Revision()+1 || stopped.SwitchVersion() != disabled.SwitchVersion() {
		t.Fatalf("stopped target = %+v", stopped)
	}
}

func TestTargetTransitionBoundaries(t *testing.T) {
	target, err := NewSummaryTarget(stoppedSummaryTargetInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Enable(targetTime(0).Add(-time.Microsecond)); err == nil {
		t.Fatal("Enable accepted a backwards timestamp")
	}

	maxRevisionInput := stoppedSummaryTargetInput()
	maxRevisionInput.Revision = Revision(math.MaxInt64)
	maxRevision, err := NewSummaryTarget(maxRevisionInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maxRevision.Enable(targetTime(1)); err == nil {
		t.Fatal("Enable overflowed revision")
	}

	maxSwitchInput := stoppedSummaryTargetInput()
	maxSwitchInput.SwitchVersion = Revision(math.MaxInt64)
	maxSwitch, err := NewSummaryTarget(maxSwitchInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maxSwitch.Enable(targetTime(1)); err == nil {
		t.Fatal("Enable overflowed switch_version")
	}

	summary, err := NewSummaryTarget(summaryTargetInput())
	if err != nil {
		t.Fatal(err)
	}
	recheck := targetTime(3)
	if _, err := summary.MarkBlocked(TargetReasonSessionInvalid, &recheck, targetTime(2)); err == nil {
		t.Fatal("manual blocker accepted recheck_at")
	}
}

func TestTargetBlockedAndErrorTransitions(t *testing.T) {
	target, err := NewSummaryTarget(stoppedSummaryTargetInput())
	if err != nil {
		t.Fatal(err)
	}
	target, err = target.Enable(targetTime(1))
	if err != nil {
		t.Fatal(err)
	}
	recheck := targetTime(3)
	automatic, err := target.MarkBlocked(TargetReasonCooldown, &recheck, targetTime(2))
	if err != nil {
		t.Fatal(err)
	}
	if automatic.Actual() != ActualBlocked || automatic.Recovery() != RecoveryAutomatic {
		t.Fatalf("automatic block = %+v", automatic)
	}
	manual, err := automatic.MarkBlocked(TargetReasonSessionInvalid, nil, targetTime(3))
	if err != nil {
		t.Fatal(err)
	}
	if manual.Recovery() != RecoveryManual {
		t.Fatalf("manual block recovery = %q", manual.Recovery())
	}
	if _, err := manual.MarkError(TargetReasonStateIntegrity, targetTime(4)); err == nil {
		t.Fatal("manual blocker changed diagnostic state without recovery")
	}
	if _, err := manual.MarkRunning(targetTime(4)); err == nil {
		t.Fatal("manual blocker entered running without recovery")
	}
	recovered, err := manual.Recover(targetTime(4))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Actual() != ActualStarting || recovered.Recovery() != RecoveryNone || recovered.SwitchVersion() != manual.SwitchVersion() {
		t.Fatalf("recovered target = %+v", recovered)
	}
	failed, err := recovered.MarkError(TargetReasonStateIntegrity, targetTime(5))
	if err != nil {
		t.Fatal(err)
	}
	if failed.Actual() != ActualError || failed.Reason() != TargetReasonStateIntegrity || failed.Recovery() != RecoveryManual {
		t.Fatalf("error target = %+v", failed)
	}
	if _, err := failed.MarkError(TargetReasonCooldown, targetTime(6)); err == nil {
		t.Fatal("MarkError accepted a blocker reason")
	}
	if _, err := failed.MarkWaiting(TargetReasonCooldown, targetTime(7), targetTime(6)); err == nil {
		t.Fatal("MarkWaiting accepted a blocker reason")
	}

	disabled, err := failed.Disable(targetTime(6))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disabled.MarkRunning(targetTime(6)); err == nil {
		t.Fatal("disabled target entered running")
	}
}

func TestTargetAutomaticRecoveryWaitsForRecheck(t *testing.T) {
	target, err := NewSummaryTarget(summaryTargetInput())
	if err != nil {
		t.Fatal(err)
	}
	recheck := targetTime(5)
	waiting, err := target.MarkWaiting(TargetReasonTransientFailure, recheck, targetTime(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := waiting.MarkRunning(targetTime(4)); err == nil {
		t.Fatal("automatic recovery ran before recheck_at")
	}
	running, err := waiting.MarkRunning(recheck)
	if err != nil {
		t.Fatal(err)
	}
	if running.Actual() != ActualRunning || running.Recovery() != RecoveryNone {
		t.Fatalf("running target = %+v", running)
	}
}

func TestTargetSwitchResetsRefillKeepsWriteSeq(t *testing.T) {
	cursor, err := EncodeAskRefill(40)
	if err != nil {
		t.Fatal(err)
	}
	input := stoppedSummaryTargetInput()
	input.WriteSeq = 9
	input.RefillCursor = cursor
	input.RefillTotal = 120
	target, err := NewSummaryTarget(input)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := target.Enable(targetTime(1))
	if err != nil {
		t.Fatal(err)
	}
	if enabled.WriteSeq() != 9 || !enabled.RefillCursor().Equal(Cursor{}) || enabled.RefillTotal() != 0 {
		t.Fatalf("enable refill write=%d cursor=%q total=%d", enabled.WriteSeq(), enabled.RefillCursor().Bytes(), enabled.RefillTotal())
	}
	sorted, err := enabled.SetSortOrder(SortOrder{Column: SortColumnQuantity, Direction: SortDescending}, targetTime(2))
	if err != nil {
		t.Fatal(err)
	}
	if sorted.WriteSeq() != 9 || !sorted.RefillCursor().Equal(Cursor{}) {
		t.Fatalf("sort kept write_seq and reset refill: write=%d", sorted.WriteSeq())
	}
	minCents := int64(1000)
	maxCents := int64(879769)
	ranged, err := sorted.SetPriceRange(PriceRange{MinCents: &minCents, MaxCents: &maxCents}, targetTime(3))
	if err != nil {
		t.Fatal(err)
	}
	if ranged.WriteSeq() != 9 || !ranged.RefillCursor().Equal(Cursor{}) {
		t.Fatalf("price range kept write_seq and reset refill: write=%d", ranged.WriteSeq())
	}
	if ranged.PriceRange().MinCents == nil || *ranged.PriceRange().MinCents != 1000 {
		t.Fatalf("price range = %+v", ranged.PriceRange())
	}
}

func TestSetSteamFacetsResetsRefill(t *testing.T) {
	input := stoppedSummaryTargetInput()
	input.AppID = 252490
	input.WriteSeq = 9
	target, err := NewSummaryTarget(input)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := target.Enable(targetTime(1))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := enabled.SetSteamFacets(SteamFacets{
		Cats:    []string{"steamcat.weapon"},
		Classes: []string{"ak47u"},
	}, targetTime(2))
	if err != nil {
		t.Fatal(err)
	}
	if changed.WriteSeq() != 9 || !changed.RefillCursor().Equal(Cursor{}) {
		t.Fatalf("steam facets kept write_seq and reset refill: write=%d", changed.WriteSeq())
	}
	if len(changed.SteamFacets().Cats) != 1 || changed.SteamFacets().Cats[0] != "steamcat.weapon" {
		t.Fatalf("steam facets = %+v", changed.SteamFacets())
	}
}

func TestSetSteamFacetsRejectsNonRust(t *testing.T) {
	target, err := NewSummaryTarget(stoppedSummaryTargetInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.SetSteamFacets(SteamFacets{Cats: []string{"steamcat.armor"}}, targetTime(1)); err == nil {
		t.Fatal("cs2 target must reject rust facets")
	}
}

func TestPriceRangeRejectsInvertedBounds(t *testing.T) {
	minCents := int64(200)
	maxCents := int64(100)
	if err := (PriceRange{MinCents: &minCents, MaxCents: &maxCents}).Validate(); err == nil {
		t.Fatal("inverted range must fail")
	}
}

func TestBidRejectsAskSearchConfiguration(t *testing.T) {
	input := stoppedSummaryTargetInput()
	input.Side = market.SideBid
	target, err := NewSummaryTarget(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.SetSortOrder(SortOrder{Column: SortColumnName, Direction: SortAscending}, targetTime(1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bid sort error = %v", err)
	}
	if _, err := target.SetPriceRange(PriceRange{}, targetTime(1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bid price range error = %v", err)
	}
	if _, err := target.SetSteamFacets(SteamFacets{}, targetTime(1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bid facets error = %v", err)
	}
}
