package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	legacycatalog "buff-go/internal/buffgo/catalog"
	"buff-go/internal/buffgo/resolve"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/store"
	"buff-go/internal/catalog"
	"buff-go/internal/telemetry"
)

// failFetcher always returns an error (simulates fetch failure / rate limit).
type failFetcher struct{ err error }

func (f failFetcher) Pull(ctx context.Context, opts source.SteamSellOptions) ([]source.RawOffer, error) {
	return nil, f.err
}

type staticSteamFetcher struct{ offers []source.RawOffer }

func (f staticSteamFetcher) Pull(context.Context, source.SteamSellOptions) ([]source.RawOffer, error) {
	return append([]source.RawOffer(nil), f.offers...), nil
}

type staticBuffFetcher struct{ offers []source.RawOffer }

func (f staticBuffFetcher) Pull(context.Context, source.BuffSellOptions) ([]source.RawOffer, error) {
	return append([]source.RawOffer(nil), f.offers...), nil
}

type fixedItemResolver struct {
	result catalog.MatchResult
	err    error
}

func (r fixedItemResolver) Resolve(context.Context, source.RawOffer) (catalog.MatchResult, error) {
	return r.result, r.err
}

type failingQuoteWriter struct{ err error }

func (w failingQuoteWriter) UpsertQuotes(context.Context, []store.Quote) (store.UpsertResult, error) {
	return store.UpsertResult{}, w.err
}

func (failingQuoteWriter) ListByAppIDPlatformSide(context.Context, int64, string, string, int) ([]store.Quote, error) {
	return nil, nil
}

// TestP53_FetchSuccessFailureObservability proves pipeline emits fetch/job
// success and failure counters so success rate is observable offline (P5.3).
func TestP53_FetchSuccessFailureObservability(t *testing.T) {
	root := findRepoRoot(t)
	mem := telemetry.NewMemory(64)

	catData, err := os.ReadFile(filepath.Join(root, "testdata", "rust_catalog_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	items, appid, err := legacycatalog.ParseImportJSON(catData, 0)
	if err != nil {
		t.Fatal(err)
	}
	if appid != 252490 {
		t.Fatalf("appid=%d", appid)
	}
	resolver := resolve.NewMemoryResolver()
	for _, it := range items {
		resolver.SeedProduct(it.AppID, it.Name)
	}

	okSrc := staticSteamFetcher{offers: []source.RawOffer{
		testPresentOffer(source.PlatformSteam, 252490, "Metal Facemask", 1250),
	}}
	quotes := store.NewMemoryQuoteStore()
	runner := NewSteamSellRunnerParts(okSrc, resolver, quotes)
	runner.Obs = mem
	runner.WorkerID = "host-worker-secret"
	runner.ProxyID = "http://proxy-user:proxy-pass@10.0.0.1:8080"
	runner.LeaseID = "lease-secret"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// success
	res, err := runner.RunJob(ctx, source.JobSpec{
		Key: "steam_sell_rust", Platform: source.PlatformSteam, Side: source.SideAsk, AppID: 252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Fetched == 0 || res.QuotesWritten == 0 {
		t.Fatalf("expected success result: %+v", res)
	}

	// fetch failure
	runner.Source = failFetcher{err: fmt.Errorf("http 429 rate limited")}
	_, err = runner.RunJob(ctx, source.JobSpec{
		Key: "steam_sell_rust", Platform: source.PlatformSteam, Side: source.SideAsk, AppID: 252490,
	})
	if err == nil {
		t.Fatal("expected fetch failure")
	}

	// another success (restore source)
	runner.Source = okSrc
	_, err = runner.RunJob(ctx, source.JobSpec{
		Key: "steam_sell_rust", Platform: source.PlatformSteam, Side: source.SideAsk, AppID: 252490,
	})
	if err != nil {
		t.Fatal(err)
	}

	snap := mem.Snapshot()
	if snap.FetchOK != 2 || snap.FetchFail != 1 {
		t.Fatalf("fetch ok=%d fail=%d want 2/1; %s", snap.FetchOK, snap.FetchFail, snap.String())
	}
	if snap.JobOK != 2 || snap.JobFail != 1 {
		t.Fatalf("job ok=%d fail=%d", snap.JobOK, snap.JobFail)
	}
	rate := snap.FetchSuccessRate()
	if rate < 0.66 || rate > 0.67 {
		t.Fatalf("fetch success rate=%v want ~0.666", rate)
	}
	if mem.CountLabeled(telemetry.KindJobOK, telemetry.ReasonOK, "steam", "steam.ask", 252490) != 2 {
		t.Fatalf("labeled job.ok missing: %s", mem.FormatSnapshot())
	}
	if mem.Count(telemetry.KindFetchFail, telemetry.ReasonError) != 1 {
		t.Fatalf("fetch.fail reason=error count=%d", mem.Count(telemetry.KindFetchFail, telemetry.ReasonError))
	}
	assertJobResourceRefs(t, mem.Events(), "host-worker-secret", "http://proxy-user:proxy-pass@10.0.0.1:8080", "", "lease-secret")
}

// TestP53_BuffJobObservability emits buff.ask success metrics offline.
func TestP53_BuffJobObservability(t *testing.T) {
	root := findRepoRoot(t)
	mem := telemetry.NewMemory(32)

	catData, err := os.ReadFile(filepath.Join(root, "testdata", "rust_catalog_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := legacycatalog.ParseImportJSON(catData, 0)
	if err != nil {
		t.Fatal(err)
	}
	resolver := resolve.NewMemoryResolver()
	for _, it := range items {
		resolver.SeedProduct(it.AppID, it.Name)
	}

	src := staticBuffFetcher{offers: []source.RawOffer{
		testPresentOffer(source.PlatformBuff, 252490, "Metal Facemask", 8850),
	}}
	quotes := store.NewMemoryQuoteStore()
	runner := NewBuffSellRunnerParts(src, resolver, quotes)
	runner.Obs = mem
	runner.Cookie = "session=test"
	runner.WorkerID = "buff-worker-secret"
	runner.ProxyID = "http://buff-user:buff-pass@10.0.0.2:8080"
	runner.LeaseID = "buff-lease-secret"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := runner.RunJob(ctx, source.JobSpec{
		Key: "buff_sell_rust", Platform: source.PlatformBuff, Side: source.SideAsk, AppID: 252490,
	})
	if err != nil {
		t.Fatalf("buff job: %v skipped=%v", err, res.Skipped)
	}
	snap := mem.Snapshot()
	if snap.JobOK != 1 || snap.FetchOK != 1 {
		t.Fatalf("buff observ: %s (fetched=%d written=%d)", snap.String(), res.Fetched, res.QuotesWritten)
	}
	if mem.CountLabeled(telemetry.KindJobOK, telemetry.ReasonOK, "buff", "buff.ask", 252490) != 1 {
		t.Fatalf("buff labeled: %s", mem.FormatSnapshot())
	}
	assertJobResourceRefs(t, mem.Events(), "buff-worker-secret", "http://buff-user:buff-pass@10.0.0.2:8080", "session=test", "buff-lease-secret")
}

func TestJobFailureBranchesKeepResourceCorrelation(t *testing.T) {
	const (
		appid   = int64(252490)
		worker  = "failure-worker-secret"
		proxy   = "http://failure-user:failure-pass@10.0.0.3:8080"
		account = "session=failure-cookie-secret"
		lease   = "failure-lease-secret"
	)
	offer := testPresentOffer(source.PlatformSteam, appid, "Failure Branch Item", 100)
	offer.PlatformItemID = "platform-item-1"
	resolved := catalog.MatchResult{
		ProductID: 1,
		Status:    catalog.MatchStatusMatched,
		Method:    catalog.MatchMethodExactName,
	}

	tests := []struct {
		name     string
		platform string
		branch   string
	}{
		{name: "steam unresolved", platform: source.PlatformSteam, branch: "unresolved"},
		{name: "steam quote write", platform: source.PlatformSteam, branch: "quote_write"},
		{name: "buff unresolved", platform: source.PlatformBuff, branch: "unresolved"},
		{name: "buff quote write", platform: source.PlatformBuff, branch: "quote_write"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mem := telemetry.NewMemory(16)
			jobKey := tc.platform + "_sell_failure"
			job := source.JobSpec{Key: jobKey, AppID: appid}
			platformOffer := offer
			platformOffer.Platform = tc.platform
			platformOffer.Source = tc.platform + ".fixture"

			var resolver ItemResolver = fixedItemResolver{result: resolved}
			var writer QuoteWriter = store.NewMemoryQuoteStore()
			if tc.branch == "unresolved" {
				resolver = fixedItemResolver{err: errors.New("resolver-cookie-secret")}
			} else {
				writer = failingQuoteWriter{err: errors.New("quote-writer-cookie-secret")}
			}

			if tc.platform == source.PlatformSteam {
				runner := NewSteamSellRunnerParts(staticSteamFetcher{offers: []source.RawOffer{platformOffer}}, resolver, writer)
				runner.Obs = mem
				runner.WorkerID = worker
				runner.ProxyID = proxy
				runner.LeaseID = lease
				if _, err := runner.RunJob(context.Background(), job); err == nil {
					t.Fatal("expected steam job failure")
				}
				assertFailureBranchResourceRefs(t, mem.Events(), worker, proxy, "", jobKey, lease)
				return
			}

			runner := NewBuffSellRunnerParts(staticBuffFetcher{offers: []source.RawOffer{platformOffer}}, resolver, writer)
			runner.Obs = mem
			runner.WorkerID = worker
			runner.ProxyID = proxy
			runner.Cookie = account
			runner.LeaseID = lease
			if _, err := runner.RunJob(context.Background(), job); err == nil {
				t.Fatal("expected buff job failure")
			}
			assertFailureBranchResourceRefs(t, mem.Events(), worker, proxy, account, jobKey, lease)
		})
	}
}

func assertFailureBranchResourceRefs(t *testing.T, events []telemetry.Event, worker, proxy, account, job, lease string) {
	t.Helper()
	seenFetchOK := false
	seenJobFail := false
	for _, event := range events {
		if event.Kind != telemetry.KindFetchOK && event.Kind != telemetry.KindJobFail {
			continue
		}
		seenFetchOK = seenFetchOK || event.Kind == telemetry.KindFetchOK
		seenJobFail = seenJobFail || event.Kind == telemetry.KindJobFail
		if event.WorkerID != telemetry.SafeWorkerRef(worker) ||
			event.Proxy != telemetry.SafeNodeRef(proxy) ||
			event.JobKey != telemetry.SafeJobRef(job) ||
			event.LeaseID != telemetry.SafeLeaseRef(lease) {
			t.Fatalf("failure event lost resource correlation: %+v", event)
		}
		if account != "" && event.Account != telemetry.SafeAccountRef(account) {
			t.Fatalf("failure event lost account correlation: %+v", event)
		}
		serialized := fmt.Sprintf("%+v", event)
		for _, secret := range []string{worker, proxy, account, job, lease, "failure-pass", "cookie-secret"} {
			if secret != "" && strings.Contains(serialized, secret) {
				t.Fatalf("failure event leaked %q: %+v", secret, event)
			}
		}
	}
	if !seenFetchOK || !seenJobFail {
		t.Fatalf("want fetch.ok and job.fail, got %+v", events)
	}
}

func assertJobResourceRefs(t *testing.T, events []telemetry.Event, worker, proxy, account, lease string) {
	t.Helper()
	var matched []telemetry.Event
	for _, event := range events {
		if event.Kind == telemetry.KindJobOK || event.Kind == telemetry.KindJobFail ||
			event.Kind == telemetry.KindFetchOK || event.Kind == telemetry.KindFetchFail {
			matched = append(matched, event)
		}
	}
	if len(matched) == 0 {
		t.Fatal("no job events")
	}
	for _, event := range matched {
		serialized := event.WorkerID + event.Proxy + event.Account + event.LeaseID
		for _, secret := range []string{worker, proxy, account, lease, "proxy-pass", "buff-pass"} {
			if secret != "" && strings.Contains(serialized, secret) {
				t.Fatalf("job event leaked %q: %+v", secret, event)
			}
		}
		if event.WorkerID != telemetry.SafeWorkerRef(worker) || event.Proxy != telemetry.SafeNodeRef(proxy) || event.LeaseID != telemetry.SafeLeaseRef(lease) {
			t.Fatalf("job event lost resource correlation: %+v", event)
		}
		if account != "" && event.Account != telemetry.SafeAccountRef(account) {
			t.Fatalf("job event lost account correlation: %+v", event)
		}
	}
}
