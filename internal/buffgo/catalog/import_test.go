package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseImportJSON_RustFixture(t *testing.T) {
	root := findRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "testdata", "rust_catalog_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	items, appid, err := ParseImportJSON(data, 0)
	if err != nil {
		t.Fatalf("ParseImportJSON: %v", err)
	}
	if appid != 252490 {
		t.Fatalf("appid: %d", appid)
	}
	if len(items) < 5 {
		t.Fatalf("want several rust items, got %d", len(items))
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.AppID != 252490 {
			t.Fatalf("item appid: %+v", it)
		}
		if it.MarketHashName == "" {
			t.Fatal("empty market_hash_name")
		}
		if seen[it.MarketHashName] {
			t.Fatalf("duplicate hash in fixture: %s", it.MarketHashName)
		}
		seen[it.MarketHashName] = true
	}
	if !seen["Metal Facemask"] || !seen["AK47"] {
		t.Fatalf("missing expected items: %v", seen)
	}
}

func TestParseImportJSON_DefaultAppID(t *testing.T) {
	raw := []byte(`{"items":[{"market_hash_name":"Hoodie"}]}`)
	items, appid, err := ParseImportJSON(raw, 252490)
	if err != nil {
		t.Fatal(err)
	}
	if appid != 252490 || len(items) != 1 || items[0].MarketHashName != "Hoodie" {
		t.Fatalf("got appid=%d items=%+v", appid, items)
	}
}

func TestParseImportJSON_RejectsEmpty(t *testing.T) {
	_, _, err := ParseImportJSON([]byte(`{"appid":252490,"items":[]}`), 0)
	if err == nil {
		t.Fatal("expected empty items error")
	}
}

func TestLoadImportFile(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "testdata", "rust_catalog_sample.json")
	items, appid, err := LoadImportFile(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if appid != 252490 || len(items) == 0 {
		t.Fatalf("appid=%d n=%d", appid, len(items))
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("go.mod not found")
	return ""
}
