package collection

import (
	"bytes"
	"fmt"
	"time"
)

// MaxCursorBytes bounds opaque platform cursor persistence.
const MaxCursorBytes = 4096

// Cursor is an opaque, size-bounded pagination value.
type Cursor struct {
	value []byte
}

// NewCursor copies and validates an opaque cursor.
func NewCursor(value []byte) (Cursor, error) {
	copied := make([]byte, len(value))
	copy(copied, value)
	cursor := Cursor{value: copied}
	if err := cursor.Validate(); err != nil {
		return Cursor{}, err
	}
	return cursor, nil
}

// Validate rejects oversized cursor values.
func (cursor Cursor) Validate() error {
	if len(cursor.value) > MaxCursorBytes {
		return fmt.Errorf("cursor exceeds %d bytes", MaxCursorBytes)
	}
	return nil
}

// Bytes returns an isolated copy of the cursor.
func (cursor Cursor) Bytes() []byte {
	copied := make([]byte, len(cursor.value))
	copy(copied, cursor.value)
	return copied
}

// Equal reports whether two opaque cursors contain identical bytes.
func (cursor Cursor) Equal(other Cursor) bool {
	return bytes.Equal(cursor.value, other.value)
}

func (cursor Cursor) clone() Cursor {
	return Cursor{value: cursor.Bytes()}
}

// PageInput is one committed collection page.
type PageInput struct {
	RunID         RunID
	PageSequence  Sequence
	CursorBefore  Cursor
	CursorAfter   Cursor
	PayloadDigest [32]byte
	CollectedAt   time.Time
	CommittedAt   time.Time
}

// Page is an immutable page commit record linked only to its run.
type Page struct {
	runID         RunID
	pageSequence  Sequence
	cursorBefore  Cursor
	cursorAfter   Cursor
	payloadDigest [32]byte
	collectedAt   time.Time
	committedAt   time.Time
}

// NewPage validates and copies a page commit.
func NewPage(input PageInput) (Page, error) {
	page := Page{
		runID:         input.RunID,
		pageSequence:  input.PageSequence,
		cursorBefore:  input.CursorBefore.clone(),
		cursorAfter:   input.CursorAfter.clone(),
		payloadDigest: input.PayloadDigest,
		collectedAt:   input.CollectedAt,
		committedAt:   input.CommittedAt,
	}
	if err := page.Validate(); err != nil {
		return Page{}, err
	}
	return page, nil
}

// Validate checks page identity, cursors, digest, and timestamp order.
func (page Page) Validate() error {
	if err := page.runID.Validate(); err != nil {
		return err
	}
	if err := page.pageSequence.Validate(); err != nil {
		return fmt.Errorf("page_sequence: %w", err)
	}
	if err := page.cursorBefore.Validate(); err != nil {
		return fmt.Errorf("cursor_before: %w", err)
	}
	if err := page.cursorAfter.Validate(); err != nil {
		return fmt.Errorf("cursor_after: %w", err)
	}
	if page.payloadDigest == ([32]byte{}) {
		return fmt.Errorf("payload_digest is required")
	}
	if err := validateTime("collected_at", page.collectedAt); err != nil {
		return err
	}
	if err := validateTime("committed_at", page.committedAt); err != nil {
		return err
	}
	if page.committedAt.Before(page.collectedAt) {
		return fmt.Errorf("committed_at cannot precede collected_at")
	}
	return nil
}

// RunID returns the owning run identity.
func (page Page) RunID() RunID { return page.runID }

// PageSequence returns the contiguous page number.
func (page Page) PageSequence() Sequence { return page.pageSequence }

// CursorBefore returns an isolated copy of the request cursor.
func (page Page) CursorBefore() Cursor { return page.cursorBefore.clone() }

// CursorAfter returns an isolated copy of the next cursor.
func (page Page) CursorAfter() Cursor { return page.cursorAfter.clone() }

// PayloadDigest returns the page payload digest.
func (page Page) PayloadDigest() [32]byte { return page.payloadDigest }

// CollectedAt returns when collection completed.
func (page Page) CollectedAt() time.Time { return page.collectedAt }

// CommittedAt returns when the page was persisted.
func (page Page) CommittedAt() time.Time { return page.committedAt }
