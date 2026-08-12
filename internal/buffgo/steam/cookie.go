package steam

import (
	"encoding/json"
	"fmt"
	"strings"
)

type cookieObj struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

// parseCookieJSON turns a browser cookie export JSON array into a Cookie header.
func parseCookieJSON(raw []byte) (string, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return "", fmt.Errorf("steam: empty cookie json")
	}
	var arr []cookieObj
	if err := json.Unmarshal(raw, &arr); err != nil {
		return "", fmt.Errorf("steam: cookie json: %w", err)
	}
	if len(arr) == 0 {
		return "", fmt.Errorf("steam: cookie json has no entries")
	}
	parts := make([]string, 0, len(arr))
	for _, c := range arr {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		parts = append(parts, name+"="+c.Value)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("steam: cookie json has no named cookies")
	}
	return strings.Join(parts, "; "), nil
}

// CookieHeaderFromMap builds Cookie header from name→value map.
func CookieHeaderFromMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}
