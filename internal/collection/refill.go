package collection

import (
	"fmt"
	"strconv"

	"buff-go/internal/catalog"
)

// DecodeAskRefill reads the next search offset. Empty means start at 0.
func DecodeAskRefill(cursor Cursor) (int, error) {
	raw := cursor.Bytes()
	if len(raw) == 0 {
		return 0, nil
	}
	start, err := strconv.Atoi(string(raw))
	if err != nil || start < 0 {
		return 0, fmt.Errorf("invalid ask refill cursor")
	}
	return start, nil
}

// EncodeAskRefill stores the next search offset.
func EncodeAskRefill(start int) (Cursor, error) {
	if start < 0 {
		return Cursor{}, fmt.Errorf("negative ask refill cursor")
	}
	if start == 0 {
		return Cursor{}, nil
	}
	return NewCursor([]byte(strconv.Itoa(start)))
}

// DecodeBidRefill reads the last catalog product_id. Empty means start at 0.
func DecodeBidRefill(cursor Cursor) (catalog.ProductID, error) {
	raw := cursor.Bytes()
	if len(raw) == 0 {
		return 0, nil
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("invalid bid refill cursor")
	}
	return catalog.ProductID(id), nil
}

// EncodeBidRefill stores the last catalog product_id.
func EncodeBidRefill(id catalog.ProductID) (Cursor, error) {
	if id < 0 {
		return Cursor{}, fmt.Errorf("negative bid refill cursor")
	}
	if id == 0 {
		return Cursor{}, nil
	}
	return NewCursor([]byte(strconv.FormatInt(int64(id), 10)))
}
