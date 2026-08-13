package steam

import (
	"fmt"
	"strconv"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
)

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
