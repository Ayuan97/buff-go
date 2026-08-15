package collection

import (
	"bytes"
	"fmt"
	"net/netip"
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
	copy(copied, valueOrEmpty(cursor.value))
	return copied
}

func valueOrEmpty(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}

// Equal reports whether two opaque cursors contain identical bytes.
func (cursor Cursor) Equal(other Cursor) bool {
	return bytes.Equal(cursor.value, other.value)
}

func (cursor Cursor) clone() Cursor {
	return Cursor{value: cursor.Bytes()}
}

// PageInput is the latest committed page of one direction.
type PageInput struct {
	TargetID      TargetID
	WriteSeq      int64
	CursorBefore  Cursor
	CursorAfter   Cursor
	PayloadDigest [32]byte
	CollectedAt   time.Time
	CommittedAt   time.Time
	AccountID     int64
	ExitAddress   netip.Addr
}

// Page is the current page of one direction. Older pages are not kept.
type Page struct {
	targetID      TargetID
	writeSeq      int64
	cursorBefore  Cursor
	cursorAfter   Cursor
	payloadDigest [32]byte
	collectedAt   time.Time
	committedAt   time.Time
	accountID     int64
	exitAddress   netip.Addr
}

// NewPage validates and copies a latest-page record.
func NewPage(input PageInput) (Page, error) {
	page := Page{
		targetID:      input.TargetID,
		writeSeq:      input.WriteSeq,
		cursorBefore:  input.CursorBefore.clone(),
		cursorAfter:   input.CursorAfter.clone(),
		payloadDigest: input.PayloadDigest,
		collectedAt:   input.CollectedAt,
		committedAt:   input.CommittedAt,
		accountID:     input.AccountID,
		exitAddress:   input.ExitAddress,
	}
	if err := page.Validate(); err != nil {
		return Page{}, err
	}
	return page, nil
}

// Validate checks page identity, cursors, digest, and timestamp order.
func (page Page) Validate() error {
	if err := page.targetID.Validate(); err != nil {
		return err
	}
	if page.writeSeq < 1 {
		return fmt.Errorf("write_seq must be at least 1")
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
	if page.accountID < 0 {
		return fmt.Errorf("account_id cannot be negative")
	}
	return nil
}

// TargetID returns the owning direction.
func (page Page) TargetID() TargetID { return page.targetID }

// WriteSeq returns the target write counter at commit.
func (page Page) WriteSeq() int64 { return page.writeSeq }

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

// AccountID returns the worker account when recorded.
func (page Page) AccountID() int64 { return page.accountID }

// ExitAddress returns the worker exit when recorded.
func (page Page) ExitAddress() netip.Addr { return page.exitAddress }
