package collection

import (
	"bytes"
	"net/netip"
	"testing"
	"time"
)

func pageDigest() [32]byte {
	return [32]byte{1}
}

func pageInput(t *testing.T) PageInput {
	t.Helper()
	return PageInput{
		TargetID:      5,
		WriteSeq:      1,
		CursorBefore:  mustCursor(t, "first"),
		CursorAfter:   mustCursor(t, "second"),
		PayloadDigest: pageDigest(),
		CollectedAt:   targetTime(1),
		CommittedAt:   targetTime(2),
		AccountID:     3,
		ExitAddress:   netip.MustParseAddr("1.1.1.1"),
	}
}

func mustCursor(t *testing.T, value string) Cursor {
	t.Helper()
	cursor, err := NewCursor([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return cursor
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
		{"zero target id", func(input *PageInput) { input.TargetID = 0 }},
		{"zero write seq", func(input *PageInput) { input.WriteSeq = 0 }},
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
	if page.WriteSeq() != 1 || page.AccountID() != 3 {
		t.Fatalf("page = write=%d account=%d", page.WriteSeq(), page.AccountID())
	}

	equalTimes := pageInput(t)
	equalTimes.CommittedAt = equalTimes.CollectedAt
	if _, err := NewPage(equalTimes); err != nil {
		t.Fatalf("NewPage rejected equal collection and commit times: %v", err)
	}
}
