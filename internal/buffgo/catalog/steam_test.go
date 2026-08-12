package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSteamMarketSearchCatalog_RustFixture(t *testing.T) {
	root := findRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := ParseSteamMarketSearchCatalog(data, 252490)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("total_count: %d", total)
	}
	if len(items) != 3 {
		t.Fatalf("items: %d", len(items))
	}
	if items[0].MarketHashName != "Metal Facemask" || items[0].AppID != 252490 {
		t.Fatalf("first: %+v", items[0])
	}
	if items[0].IconURL != "icon-metal-facemask" {
		t.Fatalf("icon: %q", items[0].IconURL)
	}
	if items[0].ClassID != "1234567890" {
		t.Fatalf("classid: %q", items[0].ClassID)
	}
	if !items[0].Commodity {
		t.Fatalf("commodity: want true for fixture item 0")
	}
	if items[1].ClassID != "1234567891" || !items[1].Commodity {
		t.Fatalf("item1 classid/commodity: %+v", items[1])
	}
	// AK47 fixture row has no classid/commodity — remain empty/false.
	if items[2].ClassID != "" || items[2].Commodity {
		t.Fatalf("item2 should lack classid/commodity: %+v", items[2])
	}
	// Catalog parsing preserves raw hash evidence and does not require prices.
	for _, it := range items {
		if it.MarketHashName == "" || it.AppID != 252490 {
			t.Fatalf("bad item: %+v", it)
		}
	}
}

func TestParseSteamMarketSearchCatalog_Unsuccessful(t *testing.T) {
	_, _, err := ParseSteamMarketSearchCatalog([]byte(`{"success":false}`), 252490)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseSteamMarketSearchCatalog_DoesNotInventHashFromDisplayName(t *testing.T) {
	body := []byte(`{"success":true,"total_count":1,"results":[{"name":"Display Name","appid":252490}]}`)
	_, _, err := ParseSteamMarketSearchCatalog(body, 252490)
	if err == nil || !strings.Contains(err.Error(), "market_hash_name is required") {
		t.Fatalf("expected missing hash error, got %v", err)
	}
}

func TestImportItems_RejectsCrossAppRewriteBeforeStore(t *testing.T) {
	service := &Service{Store: &Store{}}
	_, err := service.ImportItems(context.Background(), 252490, []Item{{
		AppID: 730, MarketHashName: "AK-47 | Redline", Name: "AK-47 | Redline",
	}}, "test")
	if err == nil || !strings.Contains(err.Error(), "does not match target appid") {
		t.Fatalf("expected appid mismatch, got %v", err)
	}
}

func TestPuller_MinimalPage(t *testing.T) {
	root := findRepoRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if !strings.Contains(gotQuery, "appid=252490") {
			t.Errorf("missing appid in query: %s", gotQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	p := NewPuller(PullOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})
	res, err := p.Pull(context.Background(), PullOptions{
		AppID:    252490,
		Count:    100,
		MaxPages: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 1 || len(res.Items) != 3 {
		t.Fatalf("pull result: %+v items=%d", res, len(res.Items))
	}
	if !strings.Contains(gotQuery, "norender=1") {
		t.Fatalf("expected norender=1, query=%s", gotQuery)
	}
}

func TestPuller_HTTPErrorDoesNotExposeResponse(t *testing.T) {
	const secret = "response-secret Authorization=Bearer-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(secret))
	}))
	defer srv.Close()

	p := NewPuller(PullOptions{BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := p.Pull(context.Background(), PullOptions{AppID: 252490, Count: 1, MaxPages: 1})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "http status 502") {
		t.Fatalf("unsafe HTTP error: %v", err)
	}
}
