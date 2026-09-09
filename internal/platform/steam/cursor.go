package steam

import (
	"encoding/json"
	"fmt"

	"buff-go/internal/collection"
)

func decodeAskPayload(request collection.PageFetch) (collection.AskPagePayload, error) {
	if request.Kind != collection.TaskKindAskPage && request.Kind != "" {
		return collection.AskPagePayload{}, fmt.Errorf("ask fetch received %s", request.Kind)
	}
	if len(request.Payload) == 0 {
		return collection.AskPagePayload{Start: 0, Count: collection.AskPageSize}, nil
	}
	var page collection.AskPagePayload
	if err := json.Unmarshal(request.Payload, &page); err != nil {
		return collection.AskPagePayload{}, fmt.Errorf("ask page payload: %w", err)
	}
	if page.Start < 0 || page.Count < 1 {
		return collection.AskPagePayload{}, fmt.Errorf("ask page start/count is invalid")
	}
	return page, nil
}

func decodeBidPayload(request collection.PageFetch) (collection.BidBatchPayload, error) {
	if request.Kind != collection.TaskKindBidBatch && request.Kind != "" {
		return collection.BidBatchPayload{}, fmt.Errorf("bid fetch received %s", request.Kind)
	}
	if len(request.Payload) == 0 {
		return collection.BidBatchPayload{AfterID: 0, Limit: defaultBidBatch}, nil
	}
	var batch collection.BidBatchPayload
	if err := json.Unmarshal(request.Payload, &batch); err != nil {
		return collection.BidBatchPayload{}, fmt.Errorf("bid batch payload: %w", err)
	}
	if batch.AfterID < 0 || batch.Limit != collection.BidBatchSize {
		return collection.BidBatchPayload{}, fmt.Errorf("bid batch after_id/limit is invalid")
	}
	return batch, nil
}
