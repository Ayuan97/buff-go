package steam

import (
	"encoding/json"
	"fmt"
	"strconv"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
)

func decodeAskPayload(request collection.PageFetch) (collection.AskPagePayload, error) {
	if request.Kind != collection.TaskKindAskPage && request.Kind != "" {
		return collection.AskPagePayload{}, fmt.Errorf("ask fetch received %s", request.Kind)
	}
	if len(request.Payload) == 0 {
		return collection.AskPagePayload{Start: 0, Count: defaultPageSize}, nil
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
	if batch.AfterID < 0 || batch.Limit < 1 {
		return collection.BidBatchPayload{}, fmt.Errorf("bid batch after_id/limit is invalid")
	}
	return batch, nil
}

func cursorOffset(cursor collection.Cursor) (int, error) {
	raw := cursor.Bytes()
	if len(raw) == 0 {
		return 0, nil
	}
	start, err := strconv.Atoi(string(raw))
	if err != nil || start < 0 {
		return 0, fmt.Errorf("invalid search cursor")
	}
	return start, nil
}

func encodeOffset(start int) (collection.Cursor, error) {
	if start < 0 {
		return collection.Cursor{}, fmt.Errorf("negative cursor")
	}
	return collection.NewCursor([]byte(strconv.Itoa(start)))
}

func cursorProductID(cursor collection.Cursor) (catalog.ProductID, error) {
	raw := cursor.Bytes()
	if len(raw) == 0 {
		return 0, nil
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("invalid catalog cursor")
	}
	return catalog.ProductID(id), nil
}

func encodeProductID(id catalog.ProductID) (collection.Cursor, error) {
	if id < 0 {
		return collection.Cursor{}, fmt.Errorf("negative product cursor")
	}
	return collection.NewCursor([]byte(strconv.FormatInt(int64(id), 10)))
}
