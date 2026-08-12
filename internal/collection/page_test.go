package collection

import (
	"bytes"
	"testing"
	"time"
)

func pageDigest() [32]byte {
	return [32]byte{1}
}

func pageInput(t *testing.T) PageInput {
	t.Helper()
	return PageInput{
		RunID:         1,
		PageSequence:  1,
		CursorBefore:  mustCursor(t, "first"),
		CursorAfter:   mustCursor(t, "second"),
		PayloadDigest: pageDigest(),
		CollectedAt:   targetTime(1),
		CommittedAt:   targetTime(2),
	}
}

func TestCursorBoundsAndCopyIsolation(t *testing.T) {
	raw := bytes.Repeat([]byte{'a'}, MaxCursorBytes)
	cursor, err := NewCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 'b'
	if cursor.Bytes()[0] != 'a' {
		t.Fatal("NewCursor retained the caller slice")
	}
	copy := cursor.Bytes()
	copy[0] = 'c'
	if cursor.Bytes()[0] != 'a' {
		t.Fatal("Cursor.Bytes exposed its internal slice")
	}
	if _, err := NewCursor(bytes.Repeat([]byte{'a'}, MaxCursorBytes+1)); err == nil {
		t.Fatal("NewCursor accepted an oversized value")
	}
	if empty, err := NewCursor(nil); err != nil || empty.Bytes() == nil || len(empty.Bytes()) != 0 {
		t.Fatalf("empty cursor = %v, %v", empty.Bytes(), err)
	}
}

func TestPageValidationAndCopyIsolation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PageInput)
	}{
		{"zero run id", func(input *PageInput) { input.RunID = 0 }},
		{"zero sequence", func(input *PageInput) { input.PageSequence = 0 }},
		{"zero digest", func(input *PageInput) { input.PayloadDigest = [32]byte{} }},
		{"commit before collect", func(input *PageInput) { input.CommittedAt = input.CollectedAt.Add(-time.Microsecond) }},
		{"non UTC collect", func(input *PageInput) { input.CollectedAt = input.CollectedAt.In(time.FixedZone("offset", 0)) }},
		{"nanosecond commit", func(input *PageInput) { input.CommittedAt = input.CommittedAt.Add(time.Nanosecond) }},
		{"out of range collect", func(input *PageInput) { input.CollectedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := pageInput(t)
			test.mutate(&input)
			if _, err := NewPage(input); err == nil {
				t.Fatal("NewPage accepted invalid input")
			}
		})
	}

	input := pageInput(t)
	page, err := NewPage(input)
	if err != nil {
		t.Fatal(err)
	}
	before := page.CursorBefore().Bytes()
	before[0] = 'x'
	if string(page.CursorBefore().Bytes()) != "first" {
		t.Fatal("Page.CursorBefore exposed internal cursor bytes")
	}
	after := page.CursorAfter().Bytes()
	after[0] = 'x'
	if string(page.CursorAfter().Bytes()) != "second" {
		t.Fatal("Page.CursorAfter exposed internal cursor bytes")
	}

	equalTimes := pageInput(t)
	equalTimes.CommittedAt = equalTimes.CollectedAt
	if _, err := NewPage(equalTimes); err != nil {
		t.Fatalf("NewPage rejected equal collection and commit times: %v", err)
	}
}

func TestRunCommitPageRequiresContiguousCausalPage(t *testing.T) {
	pendingInput := catalogRunInput(t)
	pending, err := NewCatalogRun(pendingInput)
	if err != nil {
		t.Fatal(err)
	}
	running, err := pending.Begin(targetTime(1))
	if err != nil {
		t.Fatal(err)
	}
	page, err := NewPage(pageInput(t))
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := running.CommitPage(page)
	if err != nil {
		t.Fatal(err)
	}
	if advanced.LastPageSequence() != 1 || string(advanced.CurrentCursor().Bytes()) != "second" {
		t.Fatalf("advanced run last=%d cursor=%q", advanced.LastPageSequence(), advanced.CurrentCursor().Bytes())
	}
	if running.LastPageSequence() != 0 || string(running.CurrentCursor().Bytes()) != "first" {
		t.Fatal("CommitPage mutated its receiver")
	}

	wrongRunInput := pageInput(t)
	wrongRunInput.RunID = 2
	wrongRun, _ := NewPage(wrongRunInput)
	if _, err := running.CommitPage(wrongRun); err == nil {
		t.Fatal("CommitPage accepted another run's page")
	}
	gapInput := pageInput(t)
	gapInput.PageSequence = 2
	gap, _ := NewPage(gapInput)
	if _, err := running.CommitPage(gap); err == nil {
		t.Fatal("CommitPage accepted a sequence gap")
	}
	wrongCursorInput := pageInput(t)
	wrongCursorInput.CursorBefore = mustCursor(t, "other")
	wrongCursor, _ := NewPage(wrongCursorInput)
	if _, err := running.CommitPage(wrongCursor); err == nil {
		t.Fatal("CommitPage accepted the wrong cursor_before")
	}

	lateStart, err := pending.Begin(targetTime(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lateStart.CommitPage(page); err == nil {
		t.Fatal("CommitPage accepted a page collected before run start")
	}
	if _, err := pending.CommitPage(page); err == nil {
		t.Fatal("pending run committed a page")
	}
	terminal, err := running.Stop(CompletenessPartial, RunReasonCancelled, targetTime(3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.CommitPage(page); err == nil {
		t.Fatal("terminal run committed a page")
	}
}

func TestRunCurrentCursorIsCopyIsolated(t *testing.T) {
	raw := []byte("first")
	cursor, err := NewCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	input := catalogRunInput(t)
	input.CurrentCursor = cursor
	run, err := NewCatalogRun(input)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 'x'
	copy := run.CurrentCursor().Bytes()
	copy[0] = 'y'
	if string(run.CurrentCursor().Bytes()) != "first" {
		t.Fatal("Run.CurrentCursor exposed mutable cursor bytes")
	}
}
