package pipeline

import (
	"context"
	"database/sql"
	"testing"

	"buff-go/internal/buffgo/resolve"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/store"
)

type countingSteamFetcher struct{ calls int }

func (f *countingSteamFetcher) Pull(context.Context, source.SteamSellOptions) ([]source.RawOffer, error) {
	f.calls++
	return nil, nil
}

func TestLegacyMarketStorageBlocksFetchBeforePlatformRequest(t *testing.T) {
	legacy := store.NewQuoteStore(&sql.DB{})
	resolver := resolve.NewMemoryResolver()

	steamFetcher := &countingSteamFetcher{}
	steam := NewSteamSellRunnerParts(steamFetcher, resolver, legacy)
	if _, err := steam.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490}); err == nil {
		t.Fatal("expected Steam storage preflight error")
	}
	if steamFetcher.calls != 0 {
		t.Fatalf("Steam fetch calls = %d, want 0", steamFetcher.calls)
	}

	buffFetcher := &capturingBuffFetcher{}
	buff := NewBuffSellRunnerParts(buffFetcher, resolver, legacy)
	if _, err := buff.RunJob(context.Background(), source.JobSpec{Key: "buff_ask", AppID: 252490}); err == nil {
		t.Fatal("expected BUFF storage preflight error")
	}
	if buffFetcher.calls != 0 {
		t.Fatalf("BUFF fetch calls = %d, want 0", buffFetcher.calls)
	}
}
