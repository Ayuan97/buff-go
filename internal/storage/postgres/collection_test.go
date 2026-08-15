package postgres

import (
	"bytes"
	"math"
	"testing"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
)

// advisoryLockParts 必须把有符号键拆成 PostgreSQL 记录的无符号两半：真实锁键
// 来自 hashtextextended，高半为负是常态，随机 schema 的集成测试覆盖不到它。
func TestAdvisoryLockParts(t *testing.T) {
	cases := []struct {
		key            int64
		classID, objID int64
	}{
		{0, 0, 0},
		{1, 0, 1},
		{-1, 4294967295, 4294967295},
		{-2716215821074474996, 3662549018, 328161292},
		{math.MinInt64, 2147483648, 0},
		{math.MaxInt64, 2147483647, 4294967295},
	}
	for _, testCase := range cases {
		classID, objID := advisoryLockParts(testCase.key)
		if classID != testCase.classID || objID != testCase.objID {
			t.Fatalf("key %d split to %d/%d, want %d/%d",
				testCase.key, classID, objID, testCase.classID, testCase.objID)
		}
		if classID < 0 || classID > 4294967295 || objID < 0 || objID > 4294967295 {
			t.Fatalf("key %d produced halves outside the oid range: %d/%d", testCase.key, classID, objID)
		}
	}
}

func TestCollectionStoreValidation(t *testing.T) {
	recheck := collectionStoreTestTime().Add(time.Hour)
	validTransitions := []TargetTransition{
		{State: collection.ActualWaiting, Reason: collection.TargetReasonNextCycle, RecheckAt: &recheck},
		{State: collection.ActualBlocked, Reason: collection.TargetReasonCooldown, RecheckAt: &recheck},
		{State: collection.ActualBlocked, Reason: collection.TargetReasonSessionInvalid},
		{State: collection.ActualRunning},
		{State: collection.ActualStopped},
		{State: collection.ActualError, Reason: collection.TargetReasonSchedulerFailure},
	}
	for _, transition := range validTransitions {
		if !validTargetTransition(transition) {
			t.Fatalf("valid transition was rejected: %+v", transition)
		}
	}
	invalidTransitions := []TargetTransition{
		{State: collection.ActualWaiting, Reason: collection.TargetReasonNextCycle},
		{State: collection.ActualBlocked, Reason: collection.TargetReasonSessionInvalid, RecheckAt: &recheck},
		{State: collection.ActualRunning, Reason: collection.TargetReasonCooldown},
		{State: collection.ActualStarting},
	}
	for _, transition := range invalidTransitions {
		if validTargetTransition(transition) {
			t.Fatalf("invalid transition was accepted: %+v", transition)
		}
	}

	empty, err := collection.NewCursor(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded := collectionCursorBytes(empty)
	if encoded == nil || len(encoded) != 0 {
		t.Fatalf("empty cursor encoded as %#v", encoded)
	}
	encoded = append(encoded, 1)
	if bytes.Equal(encoded, collectionCursorBytes(empty)) {
		t.Fatal("cursor database bytes alias caller memory")
	}
}

func TestCollectionStoreDomainShapes(t *testing.T) {
	now := collectionStoreTestTime()
	target, err := collection.NewSummaryTarget(collection.SummaryTargetInput{
		ID: 1, Revision: 1, Platform: "steam", AppID: 730, Side: market.SideAsk,
		Desired: collection.DesiredEnabled, Actual: collection.ActualStarting,
		SwitchVersion: 1, ChangedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitingRecheck := now.Add(time.Hour)
	waiting, err := target.MarkWaiting(collection.TargetReasonNextCycle, waitingRecheck, now.Add(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	if !validTargetSuccessor(target, waiting) || waiting.SwitchVersion() != target.SwitchVersion() {
		t.Fatal("waiting successor changed identity or switch fence")
	}

	recheck := now.Add(time.Hour)
	transition := TargetTransition{State: collection.ActualWaiting, Reason: collection.TargetReasonNextCycle, RecheckAt: &recheck}
	if !targetHasTransition(waiting, transition) {
		t.Fatal("exact target transition was not recognized")
	}
}

func collectionStoreTestTime() time.Time {
	return time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
}

func TestScanJSONStringArray(t *testing.T) {
	empty, err := scanJSONStringArray([]byte("[]"))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty = %v err=%v", empty, err)
	}
	got, err := scanJSONStringArray([]byte(`["steamcat.armor","hoodie"]`))
	if err != nil || len(got) != 2 || got[0] != "steamcat.armor" || got[1] != "hoodie" {
		t.Fatalf("got = %v err=%v", got, err)
	}
	if _, err := scanJSONStringArray([]byte("{")); err == nil {
		t.Fatal("invalid json must fail")
	}
}
