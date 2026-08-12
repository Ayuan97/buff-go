package pipeline

import (
	"context"
	"errors"
	"testing"

	"buff-go/internal/buffgo/resolve"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/store"
	"buff-go/internal/catalog"
	"buff-go/internal/telemetry"
)

type countingIdentityResolver struct {
	calls  int
	result catalog.MatchResult
}

func (r *countingIdentityResolver) Resolve(context.Context, source.RawOffer) (catalog.MatchResult, error) {
	r.calls++
	return r.result, nil
}

func TestCompletenessZeroValueIsNotComplete(t *testing.T) {
	if err := (Completeness("")).Validate(); err == nil {
		t.Fatal("zero completeness must be invalid")
	}
	if err := CompletenessComplete.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := CompletenessPartial.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRunResultCompletenessIsIndependentFromFailure(t *testing.T) {
	resolver := resolve.NewMemoryResolver()
	resolver.SeedProduct(252490, "A")
	writer := store.NewMemoryQuoteStore()
	runner := NewSteamSellRunnerParts(staticSteamFetcher{offers: []source.RawOffer{
		testPresentOffer(source.PlatformSteam, 252490, "A", 100),
	}}, resolver, writer)

	success, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
	if err != nil {
		t.Fatal(err)
	}
	if success.Completeness != CompletenessComplete {
		t.Fatalf("successful full run = %+v", success)
	}

	runner.Source = failFetcher{err: errors.New("network failure")}
	failed, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
	if err == nil {
		t.Fatal("expected fetch failure")
	}
	if failed.Completeness != CompletenessPartial {
		t.Fatalf("failed run = %+v", failed)
	}
}

func TestSuccessfulEmptyFetchIsComplete(t *testing.T) {
	resolver := resolve.NewMemoryResolver()

	steam := NewSteamSellRunnerParts(staticSteamFetcher{offers: []source.RawOffer{}}, resolver, store.NewMemoryQuoteStore())
	steamResult, err := steam.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
	if err != nil {
		t.Fatal(err)
	}
	if steamResult.Completeness != CompletenessComplete || steamResult.Fetched != 0 || steamResult.QuotesWritten != 0 {
		t.Fatalf("Steam empty result = %+v", steamResult)
	}

	buff := NewBuffSellRunnerParts(&capturingBuffFetcher{out: []source.RawOffer{}}, resolver, store.NewMemoryQuoteStore())
	buffResult, err := buff.RunJob(context.Background(), source.JobSpec{Key: "buff_ask", AppID: 252490})
	if err != nil {
		t.Fatal(err)
	}
	if buffResult.Completeness != CompletenessComplete || buffResult.Fetched != 0 || buffResult.QuotesWritten != 0 {
		t.Fatalf("BUFF empty result = %+v", buffResult)
	}
}

func TestAskRunnersRejectBidObservation(t *testing.T) {
	offer := testPresentOffer(source.PlatformSteam, 252490, "A", 100)
	offer.Observation = testCNYObservation(source.SideBid, 100)
	resolver := resolve.NewMemoryResolver()
	resolver.SeedProduct(252490, "A")

	t.Run("steam", func(t *testing.T) {
		writer := store.NewMemoryQuoteStore()
		runner := NewSteamSellRunnerParts(staticSteamFetcher{offers: []source.RawOffer{offer}}, resolver, writer)
		result, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
		if err == nil {
			t.Fatal("expected side mismatch")
		}
		if result.Completeness != CompletenessComplete || writer.Len() != 0 {
			t.Fatalf("result=%+v quotes=%d", result, writer.Len())
		}
	})

	t.Run("buff", func(t *testing.T) {
		buffOffer := offer
		buffOffer.Platform = source.PlatformBuff
		writer := store.NewMemoryQuoteStore()
		runner := NewBuffSellRunnerParts(&capturingBuffFetcher{out: []source.RawOffer{buffOffer}}, resolver, writer)
		result, err := runner.RunJob(context.Background(), source.JobSpec{Key: "buff_ask", AppID: 252490})
		if err == nil {
			t.Fatal("expected side mismatch")
		}
		if result.Completeness != CompletenessComplete || writer.Len() != 0 {
			t.Fatalf("result=%+v quotes=%d", result, writer.Len())
		}
	})
}

func TestAskRunnersRejectBidJobBeforeFetch(t *testing.T) {
	resolver := resolve.NewMemoryResolver()

	steamFetcher := &countingSteamFetcher{}
	steam := NewSteamSellRunnerParts(steamFetcher, resolver, store.NewMemoryQuoteStore())
	if _, err := steam.RunJob(context.Background(), source.JobSpec{Key: "steam_bid", AppID: 252490, Side: source.SideBid}); err == nil {
		t.Fatal("Steam ask runner accepted bid job")
	}
	if steamFetcher.calls != 0 {
		t.Fatalf("Steam fetch calls = %d, want 0", steamFetcher.calls)
	}

	buffFetcher := &capturingBuffFetcher{}
	buff := NewBuffSellRunnerParts(buffFetcher, resolver, store.NewMemoryQuoteStore())
	if _, err := buff.RunJob(context.Background(), source.JobSpec{Key: "buff_bid", AppID: 252490, Side: source.SideBid}); err == nil {
		t.Fatal("BUFF ask runner accepted bid job")
	}
	if buffFetcher.calls != 0 {
		t.Fatalf("BUFF fetch calls = %d, want 0", buffFetcher.calls)
	}
}

func TestFullCoverageRemainsCompleteWhenResolutionFails(t *testing.T) {
	offers := []source.RawOffer{
		testPresentOffer(source.PlatformSteam, 252490, "A", 100),
		testPresentOffer(source.PlatformSteam, 252490, "B", 200),
	}

	t.Run("all unresolved", func(t *testing.T) {
		runner := NewSteamSellRunnerParts(staticSteamFetcher{offers: offers}, resolve.NewMemoryResolver(), store.NewMemoryQuoteStore())
		result, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
		if err == nil {
			t.Fatal("expected resolution failure")
		}
		if result.Completeness != CompletenessComplete || result.Unresolved != 2 || result.QuotesWritten != 0 {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("partly unresolved", func(t *testing.T) {
		resolver := resolve.NewMemoryResolver()
		resolver.SeedProduct(252490, "A")
		writer := store.NewMemoryQuoteStore()
		runner := NewSteamSellRunnerParts(staticSteamFetcher{offers: offers}, resolver, writer)
		result, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
		if err != nil {
			t.Fatal(err)
		}
		if result.Completeness != CompletenessComplete || result.Resolved != 1 || result.Unresolved != 1 || result.QuotesWritten != 1 {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestAmbiguousExactNameNeverWritesQuote(t *testing.T) {
	resolver := resolve.NewMemoryResolver()
	resolver.SeedProduct(252490, "Duplicate")
	resolver.SeedProduct(252490, "Duplicate")
	writer := store.NewMemoryQuoteStore()
	runner := NewSteamSellRunnerParts(staticSteamFetcher{offers: []source.RawOffer{
		testPresentOffer(source.PlatformSteam, 252490, "Duplicate", 100),
	}}, resolver, writer)

	result, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
	if err == nil {
		t.Fatal("ambiguous identity unexpectedly succeeded")
	}
	if result.Completeness != CompletenessComplete || result.Resolved != 0 || result.Unresolved != 1 || writer.Len() != 0 {
		t.Fatalf("result=%+v quotes=%d", result, writer.Len())
	}
}

func TestRunnersRejectUnmatchedResultWithPositiveProductID(t *testing.T) {
	badResolver := fixedItemResolver{result: catalog.MatchResult{
		ProductID: 999,
		Status:    catalog.MatchStatusUnmatched,
		Method:    catalog.MatchMethodNone,
		Reason:    catalog.MatchReasonNoMatch,
	}}
	offer := testPresentOffer(source.PlatformSteam, 252490, "A", 100)

	t.Run("steam", func(t *testing.T) {
		writer := store.NewMemoryQuoteStore()
		runner := NewSteamSellRunnerParts(staticSteamFetcher{offers: []source.RawOffer{offer}}, badResolver, writer)
		result, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
		if err == nil || result.Resolved != 0 || result.Unresolved != 1 || writer.Len() != 0 {
			t.Fatalf("result=%+v quotes=%d err=%v", result, writer.Len(), err)
		}
	})

	t.Run("buff", func(t *testing.T) {
		buffOffer := offer
		buffOffer.Platform = source.PlatformBuff
		writer := store.NewMemoryQuoteStore()
		runner := NewBuffSellRunnerParts(&capturingBuffFetcher{out: []source.RawOffer{buffOffer}}, badResolver, writer)
		result, err := runner.RunJob(context.Background(), source.JobSpec{Key: "buff_ask", AppID: 252490})
		if err == nil || result.Resolved != 0 || result.Unresolved != 1 || writer.Len() != 0 {
			t.Fatalf("result=%+v quotes=%d err=%v", result, writer.Len(), err)
		}
	})
}

func TestValidateIdentityMatchRejectsForgedCombinations(t *testing.T) {
	valid := []catalog.MatchResult{
		{ProductID: 1, Status: catalog.MatchStatusMatched, Method: catalog.MatchMethodExistingMapping},
		{ProductID: 1, Status: catalog.MatchStatusMatched, Method: catalog.MatchMethodExactName},
	}
	for _, result := range valid {
		if err := validateIdentityMatch(result); err != nil {
			t.Fatalf("valid result rejected: %+v err=%v", result, err)
		}
	}

	invalid := []catalog.MatchResult{
		{ProductID: 1, Status: catalog.MatchStatusMatched, Method: catalog.MatchMethod("unknown")},
		{ProductID: 0, Status: catalog.MatchStatusMatched, Method: catalog.MatchMethodExactName},
		{ProductID: 1, Status: catalog.MatchStatusUnmatched, Method: catalog.MatchMethodExactName},
		{ProductID: 1, Status: catalog.MatchStatusMatched, Method: catalog.MatchMethodExactName, Reason: catalog.MatchReasonNoMatch},
	}
	for _, result := range invalid {
		if err := validateIdentityMatch(result); err == nil {
			t.Fatalf("invalid result accepted: %+v", result)
		}
	}
}

func TestRunnersRejectOffersOutsideJobScopeBeforeResolve(t *testing.T) {
	validMatch := catalog.MatchResult{
		ProductID: 1, Status: catalog.MatchStatusMatched, Method: catalog.MatchMethodExactName,
	}
	tests := []struct {
		name     string
		platform string
		appid    int64
	}{
		{name: "wrong appid", platform: source.PlatformSteam, appid: 730},
		{name: "wrong platform", platform: source.PlatformBuff, appid: 252490},
	}
	for _, tc := range tests {
		t.Run("steam "+tc.name, func(t *testing.T) {
			offer := testPresentOffer(tc.platform, tc.appid, "A", 100)
			resolver := &countingIdentityResolver{result: validMatch}
			writer := store.NewMemoryQuoteStore()
			runner := NewSteamSellRunnerParts(staticSteamFetcher{offers: []source.RawOffer{offer}}, resolver, writer)
			if _, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490}); err == nil {
				t.Fatal("out-of-scope offer unexpectedly succeeded")
			}
			if resolver.calls != 0 || writer.Len() != 0 {
				t.Fatalf("resolver calls=%d quotes=%d", resolver.calls, writer.Len())
			}
		})
	}

	for _, tc := range []struct {
		name     string
		platform string
		appid    int64
	}{
		{name: "wrong appid", platform: source.PlatformBuff, appid: 730},
		{name: "wrong platform", platform: source.PlatformSteam, appid: 252490},
	} {
		t.Run("buff "+tc.name, func(t *testing.T) {
			offer := testPresentOffer(tc.platform, tc.appid, "A", 100)
			resolver := &countingIdentityResolver{result: validMatch}
			writer := store.NewMemoryQuoteStore()
			runner := NewBuffSellRunnerParts(&capturingBuffFetcher{out: []source.RawOffer{offer}}, resolver, writer)
			if _, err := runner.RunJob(context.Background(), source.JobSpec{Key: "buff_ask", AppID: 252490}); err == nil {
				t.Fatal("out-of-scope offer unexpectedly succeeded")
			}
			if resolver.calls != 0 || writer.Len() != 0 {
				t.Fatalf("resolver calls=%d quotes=%d", resolver.calls, writer.Len())
			}
		})
	}
}

type cappedSteamFetcher struct{}

func (cappedSteamFetcher) Pull(context.Context, source.SteamSellOptions) ([]source.RawOffer, error) {
	offers := []source.RawOffer{testPresentOffer(source.PlatformSteam, 252490, "A", 100)}
	return offers, &source.PartialPullError{
		Offers: offers, Page: 1, Start: 100, Err: source.ErrPageLimit,
	}
}

func TestPageCapSucceedsButRemainsPartial(t *testing.T) {
	resolver := resolve.NewMemoryResolver()
	resolver.SeedProduct(252490, "A")
	runner := NewSteamSellRunnerParts(cappedSteamFetcher{}, resolver, store.NewMemoryQuoteStore())
	result, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
	if err != nil {
		t.Fatal(err)
	}
	if result.Completeness != CompletenessPartial || result.NextStart != 100 || result.QuotesWritten != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestLaterPageFailureIsNotRecordedAsJobSuccess(t *testing.T) {
	resolver := resolve.NewMemoryResolver()
	resolver.SeedProduct(252490, "Partial Item")
	memory := telemetry.NewMemory(8)
	runner := NewSteamSellRunnerParts(partialSteamFetcher{}, resolver, store.NewMemoryQuoteStore())
	runner.Obs = memory
	result, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
	if err == nil {
		t.Fatal("expected later-page failure")
	}
	if result.Completeness != CompletenessPartial || result.QuotesWritten != 1 {
		t.Fatalf("result = %+v", result)
	}
	snapshot := memory.Snapshot()
	if snapshot.JobFail != 1 || snapshot.JobOK != 0 || snapshot.FetchFail != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

type partialRawSteamFetcher struct{ offers []source.RawOffer }

func (f partialRawSteamFetcher) Pull(context.Context, source.SteamSellOptions) ([]source.RawOffer, error) {
	return f.offers, &source.PartialPullError{
		Offers: f.offers, Page: 1, Start: 100, Err: errors.New("later page failed"),
	}
}

type partialRawBuffFetcher struct{ offers []source.RawOffer }

func (f partialRawBuffFetcher) Pull(context.Context, source.BuffSellOptions) ([]source.RawOffer, error) {
	return f.offers, &source.BuffPartialPullError{
		Offers: f.offers, NextPage: 2, Err: errors.New("later page failed"),
	}
}

func TestPartialFetchFailureSurvivesUnverifiedAdapterRows(t *testing.T) {
	steamOffers, _, err := source.ParseSteamMarketSell([]byte(`{"success":true,"start":0,"total_count":2,"results":[{"name":"A","hash_name":"A","sell_price":100,"sell_price_text":"$1.00","sell_listings":1,"appid":252490}]}`), 252490, "USD", testCollectedAt)
	if err != nil {
		t.Fatal(err)
	}
	buffOffers, _, err := source.ParseBuffGoodsSell([]byte(`{"code":"OK","data":{"items":[{"id":1,"name":"A","market_hash_name":"A","sell_min_price":"1.00","sell_num":1,"appid":252490}],"total_page":2,"page_num":1,"page_size":1}}`), 252490, "", testCollectedAt)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  func(*telemetry.Memory) error
	}{
		{
			name: "steam",
			run: func(memory *telemetry.Memory) error {
				resolver := resolve.NewMemoryResolver()
				resolver.SeedProduct(252490, "A")
				runner := NewSteamSellRunnerParts(partialRawSteamFetcher{offers: steamOffers}, resolver, store.NewMemoryQuoteStore())
				runner.Obs = memory
				_, err := runner.RunJob(context.Background(), source.JobSpec{Key: "steam_ask", AppID: 252490})
				return err
			},
		},
		{
			name: "buff",
			run: func(memory *telemetry.Memory) error {
				resolver := resolve.NewMemoryResolver()
				resolver.SeedProduct(252490, "A")
				runner := NewBuffSellRunnerParts(partialRawBuffFetcher{offers: buffOffers}, resolver, store.NewMemoryQuoteStore())
				runner.Obs = memory
				_, err := runner.RunJob(context.Background(), source.JobSpec{Key: "buff_ask", AppID: 252490})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memory := telemetry.NewMemory(8)
			if err := tt.run(memory); err == nil {
				t.Fatal("expected unverified market row failure")
			}
			snapshot := memory.Snapshot()
			if snapshot.FetchFail != 1 || snapshot.FetchOK != 0 || snapshot.JobFail != 1 || snapshot.JobOK != 0 {
				t.Fatalf("snapshot = %+v", snapshot)
			}
		})
	}
}
