package collection

import (
	"encoding/json"
	"fmt"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/resource"
)

// TaskKind is the payload shape of one queued fetch.
type TaskKind string

const (
	TaskKindAskPage  TaskKind = "ask_page"
	TaskKindBidBatch TaskKind = "bid_batch"
)

// Validate rejects an unknown task kind.
func (kind TaskKind) Validate() error {
	switch kind {
	case TaskKindAskPage, TaskKindBidBatch:
		return nil
	default:
		return fmt.Errorf("invalid task kind %q", kind)
	}
}

// TaskState is the queue occupancy of one task.
type TaskState string

const (
	TaskQueued  TaskState = "queued"
	TaskClaimed TaskState = "claimed"
)

// Validate rejects an unknown task state.
func (state TaskState) Validate() error {
	switch state {
	case TaskQueued, TaskClaimed:
		return nil
	default:
		return fmt.Errorf("invalid task state %q", state)
	}
}

// AskPagePayload is one Steam search page.
type AskPagePayload struct {
	Start int `json:"start"`
	Count int `json:"count"`
}

// BidBatchPayload is one Steam orderbook batch.
type BidBatchPayload struct {
	AfterID catalog.ProductID `json:"after_id"`
	Limit   int               `json:"limit"`
}

// TaskInput is the persisted shape of one queue item.
type TaskInput struct {
	ID              TaskID
	TargetID        TargetID
	EnqueueSeq      int64
	Kind            TaskKind
	Payload         []byte
	State           TaskState
	ClaimedBy       *resource.CombinationID
	ClaimedAt       *time.Time
	ClaimGeneration int64
	EnqueuedAt      time.Time
}

// Task is one ordered queue item for a direction.
type Task struct {
	id              TaskID
	targetID        TargetID
	enqueueSeq      int64
	kind            TaskKind
	payload         []byte
	state           TaskState
	claimedBy       resource.CombinationID
	claimedAt       time.Time
	claimGeneration int64
	enqueuedAt      time.Time
}

// NewTask validates a task restored from persistence.
func NewTask(input TaskInput) (Task, error) {
	task := Task{
		id:              input.ID,
		targetID:        input.TargetID,
		enqueueSeq:      input.EnqueueSeq,
		kind:            input.Kind,
		payload:         append([]byte(nil), input.Payload...),
		state:           input.State,
		claimGeneration: input.ClaimGeneration,
		enqueuedAt:      input.EnqueuedAt,
	}
	if input.ClaimedBy != nil {
		task.claimedBy = *input.ClaimedBy
	}
	if input.ClaimedAt != nil {
		task.claimedAt = *input.ClaimedAt
	}
	if err := task.Validate(); err != nil {
		return Task{}, err
	}
	return task, nil
}

// Validate checks identity, payload shape, and claim pairing.
func (task Task) Validate() error {
	if err := task.id.Validate(); err != nil {
		return err
	}
	if err := task.targetID.Validate(); err != nil {
		return err
	}
	if task.enqueueSeq < 1 {
		return fmt.Errorf("enqueue_seq must be at least 1")
	}
	if err := task.kind.Validate(); err != nil {
		return err
	}
	if err := task.state.Validate(); err != nil {
		return err
	}
	if err := validateTime("enqueued_at", task.enqueuedAt); err != nil {
		return err
	}
	switch task.state {
	case TaskQueued:
		if task.claimedBy != 0 || !task.claimedAt.IsZero() {
			return fmt.Errorf("queued task must not be claimed")
		}
		if task.claimGeneration < 0 {
			return fmt.Errorf("claim_generation cannot be negative")
		}
	case TaskClaimed:
		if err := task.claimedBy.Validate(); err != nil {
			return fmt.Errorf("claimed_by: %w", err)
		}
		if err := validateTime("claimed_at", task.claimedAt); err != nil {
			return err
		}
		if task.claimGeneration < 1 {
			return fmt.Errorf("claimed task generation must be positive")
		}
	}
	switch task.kind {
	case TaskKindAskPage:
		page, err := task.AskPage()
		if err != nil {
			return err
		}
		if page.Start < 0 || page.Count < 1 {
			return fmt.Errorf("ask page start/count is invalid")
		}
	case TaskKindBidBatch:
		batch, err := task.BidBatch()
		if err != nil {
			return err
		}
		if batch.AfterID < 0 || batch.Limit != BidBatchSize {
			return fmt.Errorf("bid batch after_id/limit is invalid")
		}
	}
	return nil
}

// AskPage decodes an ask search page payload.
func (task Task) AskPage() (AskPagePayload, error) {
	if task.kind != TaskKindAskPage {
		return AskPagePayload{}, fmt.Errorf("task is not an ask page")
	}
	var page AskPagePayload
	if err := json.Unmarshal(task.payload, &page); err != nil {
		return AskPagePayload{}, fmt.Errorf("ask page payload: %w", err)
	}
	return page, nil
}

// BidBatch decodes a bid catalog batch payload.
func (task Task) BidBatch() (BidBatchPayload, error) {
	if task.kind != TaskKindBidBatch {
		return BidBatchPayload{}, fmt.Errorf("task is not a bid batch")
	}
	var batch BidBatchPayload
	if err := json.Unmarshal(task.payload, &batch); err != nil {
		return BidBatchPayload{}, fmt.Errorf("bid batch payload: %w", err)
	}
	return batch, nil
}

// EncodeAskPage serializes one search page.
func EncodeAskPage(start, count int) ([]byte, error) {
	if start < 0 || count < 1 {
		return nil, fmt.Errorf("ask page start/count is invalid")
	}
	return json.Marshal(AskPagePayload{Start: start, Count: count})
}

// EncodeBidBatch serializes one catalog batch.
func EncodeBidBatch(after catalog.ProductID, limit int) ([]byte, error) {
	if after < 0 || limit != BidBatchSize {
		return nil, fmt.Errorf("bid batch after_id/limit is invalid")
	}
	return json.Marshal(BidBatchPayload{AfterID: after, Limit: limit})
}

// EnqueueSpec is one task the planner wants appended.
type EnqueueSpec struct {
	Kind    TaskKind
	Payload []byte
}

// ID returns the task identity.
func (task Task) ID() TaskID { return task.id }

// TargetID returns the owning direction.
func (task Task) TargetID() TargetID { return task.targetID }

// EnqueueSeq returns the per-target append order.
func (task Task) EnqueueSeq() int64 { return task.enqueueSeq }

// Kind returns the payload kind.
func (task Task) Kind() TaskKind { return task.kind }

// Payload returns a copy of the raw payload.
func (task Task) Payload() []byte { return append([]byte(nil), task.payload...) }

// State returns queued or claimed.
func (task Task) State() TaskState { return task.state }

// ClaimedBy returns the occupying combination when claimed.
func (task Task) ClaimedBy() (resource.CombinationID, bool) {
	return task.claimedBy, task.state == TaskClaimed
}

// ClaimedAt returns when the task was claimed.
func (task Task) ClaimedAt() (time.Time, bool) {
	return task.claimedAt, task.state == TaskClaimed
}

// ClaimGeneration is the persistent claim epoch used to fence stale workers.
func (task Task) ClaimGeneration() int64 { return task.claimGeneration }

// EnqueuedAt returns when the task was appended.
func (task Task) EnqueuedAt() time.Time { return task.enqueuedAt }
