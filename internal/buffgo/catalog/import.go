package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ImportFile is the JSON shape for offline catalog import.
//
//	{
//	  "appid": 252490,
//	  "items": [
//	    {"market_hash_name": "Metal Facemask", "name": "Metal Facemask"}
//	  ]
//	}
//
// Top-level appid is applied to items that omit appid.
type ImportFile struct {
	AppID int64  `json:"appid"`
	Items []Item `json:"items"`
}

// ParseImportJSON decodes an import document and normalizes items.
// defaultAppID is used when both file-level and item-level appid are zero.
func ParseImportJSON(data []byte, defaultAppID int64) ([]Item, int64, error) {
	var doc ImportFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, 0, fmt.Errorf("catalog import json: %w", err)
	}
	appid := doc.AppID
	if appid <= 0 {
		appid = defaultAppID
	}
	if appid <= 0 {
		return nil, 0, fmt.Errorf("appid is required (file, flag, or config enabled_appids)")
	}
	if len(doc.Items) == 0 {
		return nil, appid, fmt.Errorf("import file has no items")
	}

	out := make([]Item, 0, len(doc.Items))
	for i, it := range doc.Items {
		if it.AppID <= 0 {
			it.AppID = appid
		}
		if it.AppID != appid {
			// Allow multi-appid files only if every item carries its own appid;
			// when file has an appid, reject mismatches for safety.
			if doc.AppID > 0 {
				return nil, 0, fmt.Errorf("item[%d]: appid %d != file appid %d", i, it.AppID, doc.AppID)
			}
		}
		if err := it.Normalize(); err != nil {
			return nil, 0, fmt.Errorf("item[%d]: %w", i, err)
		}
		out = append(out, it)
	}
	return out, appid, nil
}

// LoadImportFile reads path and parses it as ImportFile JSON.
func LoadImportFile(path string, defaultAppID int64) ([]Item, int64, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, 0, fmt.Errorf("import file path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("read import file: %w", err)
	}
	return ParseImportJSON(data, defaultAppID)
}
