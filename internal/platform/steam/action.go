package steam

import (
	"encoding/json"
	"fmt"
	"strings"
)

func encodeQueryActionParams(params ...any) (string, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("steam query action params: %w", err)
	}
	return string(encoded), nil
}

func decodeQueryAction[T any](body []byte, action string) (T, error) {
	var zero T
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return zero, fmt.Errorf("steam %s json: %w", action, err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return zero, fmt.Errorf("steam %s returned no data", action)
	}
	var decoded T
	if err := json.Unmarshal(envelope.Data, &decoded); err != nil {
		return zero, fmt.Errorf("steam %s data: %w", action, err)
	}
	return decoded, nil
}

func validateItemKey(appID int64, marketHashName string) error {
	if appID < 1 {
		return fmt.Errorf("steam appid must be positive")
	}
	if strings.TrimSpace(marketHashName) == "" {
		return fmt.Errorf("steam market hash name is required")
	}
	return nil
}
