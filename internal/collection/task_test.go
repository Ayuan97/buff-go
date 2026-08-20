package collection

import (
	"testing"

	"buff-go/internal/resource"
)

func TestTaskValidationAndPayloads(t *testing.T) {
	now := targetTime(1)
	ask, err := EncodeAskPage(10, AskPageSize)
	if err != nil {
		t.Fatal(err)
	}
	task, err := NewTask(TaskInput{
		ID: 1, TargetID: 2, EnqueueSeq: 3, Kind: TaskKindAskPage, Payload: ask,
		State: TaskQueued, EnqueuedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := task.AskPage()
	if err != nil || page.Start != 10 || page.Count != AskPageSize {
		t.Fatalf("ask page = %+v err=%v", page, err)
	}

	claimedBy := resource.CombinationID(7)
	claimedAt := targetTime(2)
	claimed, err := NewTask(TaskInput{
		ID: 1, TargetID: 2, EnqueueSeq: 3, Kind: TaskKindAskPage, Payload: ask,
		State: TaskClaimed, ClaimedBy: &claimedBy, ClaimedAt: &claimedAt, ClaimGeneration: 1, EnqueuedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := claimed.ClaimedBy(); !ok || id != 7 {
		t.Fatalf("claimed_by = %d ok=%v", id, ok)
	}
	if claimed.ClaimGeneration() != 1 {
		t.Fatalf("claim_generation = %d", claimed.ClaimGeneration())
	}

	invalid := []TaskInput{
		{ID: 0, TargetID: 2, EnqueueSeq: 1, Kind: TaskKindAskPage, Payload: ask, State: TaskQueued, EnqueuedAt: now},
		{ID: 1, TargetID: 2, EnqueueSeq: 0, Kind: TaskKindAskPage, Payload: ask, State: TaskQueued, EnqueuedAt: now},
		{ID: 1, TargetID: 2, EnqueueSeq: 1, Kind: TaskKindAskPage, Payload: ask, State: TaskClaimed, EnqueuedAt: now},
		{ID: 1, TargetID: 2, EnqueueSeq: 1, Kind: TaskKindAskPage, Payload: ask, State: TaskClaimed, ClaimedBy: &claimedBy, ClaimedAt: &claimedAt, EnqueuedAt: now},
		{ID: 1, TargetID: 2, EnqueueSeq: 1, Kind: TaskKindAskPage, Payload: ask, State: TaskQueued, ClaimGeneration: -1, EnqueuedAt: now},
		{ID: 1, TargetID: 2, EnqueueSeq: 1, Kind: TaskKindAskPage, Payload: []byte(`{"start":-1,"count":10}`), State: TaskQueued, EnqueuedAt: now},
	}
	for _, input := range invalid {
		if _, err := NewTask(input); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
	if _, err := EncodeBidBatch(0, BidBatchSize+1); err == nil {
		t.Fatal("bid task accepted more than one HTTP request")
	}
}

func TestRefillCursorRoundTrip(t *testing.T) {
	empty, err := EncodeAskRefill(0)
	if err != nil || !empty.Equal(Cursor{}) {
		t.Fatalf("zero ask refill = %+v err=%v", empty, err)
	}
	cursor, err := EncodeAskRefill(30)
	if err != nil {
		t.Fatal(err)
	}
	start, err := DecodeAskRefill(cursor)
	if err != nil || start != 30 {
		t.Fatalf("ask refill = %d err=%v", start, err)
	}
	bid, err := EncodeBidRefill(9)
	if err != nil {
		t.Fatal(err)
	}
	after, err := DecodeBidRefill(bid)
	if err != nil || after != 9 {
		t.Fatalf("bid refill = %d err=%v", after, err)
	}
}
