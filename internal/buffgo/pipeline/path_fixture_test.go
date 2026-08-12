package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"buff-go/internal/buffgo/catalog"
	"buff-go/internal/buffgo/resolve"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/store"
)

func rustSteamFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(findRepoRoot(t), "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
}

func rustResolver(t *testing.T) *resolve.MemoryResolver {
	t.Helper()
	resolver := resolve.NewMemoryResolver()
	data, err := os.ReadFile(filepath.Join(findRepoRoot(t), "testdata", "rust_catalog_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := catalog.ParseImportJSON(data, 252490)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		resolver.SeedProduct(item.AppID, item.Name)
	}
	return resolver
}

func TestPipelineRejectsUnverifiedSteamUSDFixture(t *testing.T) {
	server := rustSteamFixtureServer(t)
	defer server.Close()
	src := source.NewSteamSellSource(source.SteamSellOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	quotes := store.NewMemoryQuoteStore()
	runner := NewSteamSellRunnerParts(src, rustResolver(t), quotes)

	result, err := runner.RunJob(context.Background(), source.JobSpec{
		Key: "steam_ask_rust", Platform: source.PlatformSteam, Side: source.SideAsk, AppID: 252490,
	})
	if err == nil {
		t.Fatal("USD fixture unexpectedly entered unified market")
	}
	if result.Fetched != 3 || result.QuotesWritten != 0 || result.Completeness != CompletenessComplete {
		t.Fatalf("result = %+v", result)
	}
	if quotes.Len() != 0 {
		t.Fatalf("quotes = %d, want 0", quotes.Len())
	}
}

func TestPipelineUnresolvedRowsNeverWriteQuotes(t *testing.T) {
	server := rustSteamFixtureServer(t)
	defer server.Close()
	src := source.NewSteamSellSource(source.SteamSellOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	quotes := store.NewMemoryQuoteStore()
	runner := NewSteamSellRunnerParts(src, resolve.NewMemoryResolver(), quotes)

	if _, err := runner.RunJob(context.Background(), source.JobSpec{
		Key: "steam_ask_rust", Platform: source.PlatformSteam, Side: source.SideAsk, AppID: 252490,
	}); err == nil {
		t.Fatal("expected unresolved error")
	}
	if quotes.Len() != 0 {
		t.Fatalf("quotes = %d, want 0", quotes.Len())
	}
}

func TestRawFixtureIdentityIsNotPromotedIntoVerifiedMatch(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(findRepoRoot(t), "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	offers, _, err := source.ParseSteamMarketSell(data, 252490, "USD", time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	resolver := rustResolver(t)
	if _, err := resolver.Resolve(context.Background(), offers[0]); err == nil {
		t.Fatal("unverified raw identity unexpectedly resolved")
	}
}
