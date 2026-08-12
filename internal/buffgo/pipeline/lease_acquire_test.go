package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"buff-go/internal/buffgo/pool"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/steam"
)

func TestIsSoftSkipJobErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"no lease", ErrNoProxyLease, true},
		{"no lease wrap", fmt.Errorf("acquire: %w", ErrNoProxyLease), true},
		{"search cooling", steam.ErrSearchCooling, true},
		{"search budget", steam.ErrSearchBudget, true},
		{"search 429 wrap", fmt.Errorf("fetch: %w", steam.ErrSearchCooling), true},
		{"buff 429", source.ErrHTTP429, true},
		{"buff 429 wrap", fmt.Errorf("fetch: %w", source.ErrHTTP429), true},
		{"generic", errors.New("postgres down"), false},
		{"empty offers", fmt.Errorf("steam.ask: 0 offers for appid=1"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSoftSkipJobErr(tc.err); got != tc.want {
				t.Fatalf("isSoftSkipJobErr(%v)=%v want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsSkippableAcquireErr(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{pool.ErrBusy, true},
		{pool.ErrCooling, true},
		{pool.ErrLineMismatch, true},
		{pool.ErrAppIDNotAllowed, true},
		{pool.ErrQuotaExceeded, true},
		{fmt.Errorf("wrap: %w", pool.ErrBusy), true},
		{pool.ErrNotFound, false},
		{errors.New("redis down"), false},
	}
	for _, tc := range cases {
		if got := isSkippableAcquireErr(tc.err); got != tc.want {
			t.Errorf("isSkippableAcquireErr(%v)=%v want %v", tc.err, got, tc.want)
		}
	}
}

func TestTryAcquireCandidates_PicksFirstSuccess(t *testing.T) {
	cands := []pool.ProxyCandidate{
		{ID: "px-a", LineType: pool.LineOversea},
		{ID: "px-b", LineType: pool.LineOversea},
		{ID: "px-c", LineType: pool.LineDual},
	}
	calls := 0
	acq := func(_ context.Context, req pool.AcquireRequest) (*pool.Lease, error) {
		calls++
		switch req.Proxy {
		case "px-a":
			return nil, pool.ErrBusy
		case "px-b":
			return nil, pool.ErrCooling
		case "px-c":
			return &pool.Lease{ID: "lease-c", Proxy: "px-c", WorkerID: req.WorkerID, Platform: req.Platform, AppID: req.AppID}, nil
		default:
			return nil, fmt.Errorf("unexpected proxy %q", req.Proxy)
		}
	}
	lease, idx, err := TryAcquireCandidates(context.Background(), acq, "w1", "steam", 252490, cands)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil || lease.Proxy != "px-c" || idx != 2 {
		t.Fatalf("lease=%+v idx=%d", lease, idx)
	}
	if calls != 3 {
		t.Fatalf("calls=%d want 3", calls)
	}
}

func TestTryAcquireCandidates_SkipsAllSentinels(t *testing.T) {
	cands := []pool.ProxyCandidate{
		{ID: "a", LineType: pool.LineOversea},
		{ID: "b", LineType: pool.LineOversea},
		{ID: "c", LineType: pool.LineOversea},
		{ID: "d", LineType: pool.LineOversea},
		{ID: "e", LineType: pool.LineOversea},
	}
	seq := []error{
		pool.ErrBusy,
		pool.ErrCooling,
		pool.ErrLineMismatch,
		pool.ErrAppIDNotAllowed,
		pool.ErrQuotaExceeded,
	}
	i := 0
	acq := func(_ context.Context, req pool.AcquireRequest) (*pool.Lease, error) {
		err := seq[i]
		i++
		return nil, err
	}
	lease, idx, err := TryAcquireCandidates(context.Background(), acq, "w1", "steam", 730, cands)
	if lease != nil || idx != -1 {
		t.Fatalf("want no lease, got lease=%v idx=%d", lease, idx)
	}
	if !errors.Is(err, ErrNoProxyLease) {
		t.Fatalf("want ErrNoProxyLease, got %v", err)
	}
}

func TestTryAcquireCandidates_EmptyAndUnexpected(t *testing.T) {
	_, _, err := TryAcquireCandidates(context.Background(), func(context.Context, pool.AcquireRequest) (*pool.Lease, error) {
		return nil, nil
	}, "w", "steam", 1, nil)
	if !errors.Is(err, ErrNoProxyLease) {
		t.Fatalf("empty: %v", err)
	}

	boom := errors.New("redis timeout")
	_, _, err = TryAcquireCandidates(context.Background(), func(context.Context, pool.AcquireRequest) (*pool.Lease, error) {
		return nil, boom
	}, "w", "steam", 1, []pool.ProxyCandidate{{ID: "x", LineType: pool.LineDual}})
	if !errors.Is(err, boom) {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestTryAcquireCandidates_PassesRequestFields(t *testing.T) {
	cands := []pool.ProxyCandidate{
		{ID: "http://p:1", LineType: pool.LineOversea, OnlyAppIDs: []int64{252490}},
	}
	acq := func(_ context.Context, req pool.AcquireRequest) (*pool.Lease, error) {
		if req.WorkerID != "worker-test" {
			t.Fatalf("worker: %q", req.WorkerID)
		}
		if req.Platform != "steam" {
			t.Fatalf("platform: %q", req.Platform)
		}
		if req.Proxy != "http://p:1" || req.LineType != pool.LineOversea {
			t.Fatalf("proxy fields: %+v", req)
		}
		if req.AppID != 252490 || len(req.OnlyAppIDs) != 1 || req.OnlyAppIDs[0] != 252490 {
			t.Fatalf("appid fields: %+v", req)
		}
		return &pool.Lease{ID: "L1", Proxy: req.Proxy, WorkerID: req.WorkerID}, nil
	}
	lease, idx, err := TryAcquireCandidates(context.Background(), acq, "worker-test", "steam", 252490, cands)
	if err != nil || idx != 0 || lease.ID != "L1" {
		t.Fatalf("lease=%+v idx=%d err=%v", lease, idx, err)
	}
}

func TestReleaseOptsForFetchErr(t *testing.T) {
	opts := releaseOptsForFetchErr(nil, time.Minute)
	if opts.SetCooldown {
		t.Fatal("nil err should not cooldown")
	}
	opts = releaseOptsForFetchErr(fmt.Errorf("network reset"), 3*time.Minute)
	if opts.SetCooldown {
		t.Fatal("generic err should not cooldown")
	}
	opts = releaseOptsForFetchErr(steam.ErrSearchCooling, 180*time.Second)
	if !opts.SetCooldown || opts.Cooldown != 180*time.Second {
		t.Fatalf("cooling: %+v", opts)
	}
	wrapped := fmt.Errorf("steam.ask http 429 proxy=x: %w", steam.ErrSearchCooling)
	opts = releaseOptsForFetchErr(wrapped, 0)
	if !opts.SetCooldown || opts.Cooldown != 3*time.Minute {
		t.Fatalf("wrapped 429 default cd: %+v", opts)
	}
	opts = releaseOptsForFetchErr(steam.ErrSearchBudget, 90*time.Second)
	if !opts.SetCooldown || opts.Cooldown != 90*time.Second {
		t.Fatalf("budget: %+v", opts)
	}
}

func TestResolveWorkerID(t *testing.T) {
	if got := resolveWorkerID("  custom-w  "); got != "custom-w" {
		t.Fatalf("override: %q", got)
	}
	got := resolveWorkerID("")
	if got == "" {
		t.Fatal("empty worker id")
	}
	// Stable within process: hostname-pid or worker-hex
	if got2 := resolveWorkerID(""); got2 != got && len(got) < 3 {
		t.Fatalf("unexpected id: %q / %q", got, got2)
	}
}

func TestLookupEndpointByProxyID_DirectAndList(t *testing.T) {
	ctx := context.Background()
	ep, err := lookupEndpointByProxyID(ctx, nil, "direct")
	if err != nil || !ep.IsDirect() {
		t.Fatalf("direct: %+v err=%v", ep, err)
	}
	prov := pool.NewStaticProvider([]pool.ProxyEndpoint{
		{Endpoint: "http://a.example:8080", Auth: "u:p", LineType: pool.LineOversea, Enabled: true},
	})
	ep, err = lookupEndpointByProxyID(ctx, prov, "http://a.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	if ep.Auth != "u:p" || ep.ProxyID() != "http://a.example:8080" {
		t.Fatalf("ep: %+v", ep)
	}
}

func TestLeaseRenewInterval(t *testing.T) {
	cases := []struct {
		ttl  time.Duration
		want time.Duration
	}{
		{0, 10 * time.Second}, // default 30s/3 = 10s, cap 10s
		{30 * time.Second, 10 * time.Second},
		{60 * time.Second, 10 * time.Second}, // min(20s, 10s)
		{15 * time.Second, 5 * time.Second},
		{3 * time.Second, time.Second}, // floor 1s (ttl/3 = 1s)
		{time.Second, time.Second},     // floor 1s
	}
	for _, tc := range cases {
		if got := leaseRenewInterval(tc.ttl); got != tc.want {
			t.Errorf("leaseRenewInterval(%v)=%v want %v", tc.ttl, got, tc.want)
		}
	}
}

func TestStartLeaseRenewer_TicksAndStop(t *testing.T) {
	var calls atomic.Int32
	renew := func(ctx context.Context, leaseID, workerID string) error {
		if leaseID != "L1" || workerID != "w1" {
			t.Errorf("renew args lease=%q worker=%q", leaseID, workerID)
		}
		calls.Add(1)
		return nil
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobCtx, stop := startLeaseRenewerFn(parent, renew, "L1", "w1", 30*time.Millisecond)
	defer stop()

	// Wait for at least two ticks.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(15 * time.Millisecond)
	}
	if n := calls.Load(); n < 2 {
		t.Fatalf("renew calls=%d want >=2", n)
	}
	// Job context still open while renew succeeds.
	select {
	case <-jobCtx.Done():
		t.Fatal("jobCtx cancelled while renew ok")
	default:
	}
	before := calls.Load()
	stop()
	time.Sleep(80 * time.Millisecond)
	after := calls.Load()
	if after > before+1 {
		t.Fatalf("renew still ticking after stop: before=%d after=%d", before, after)
	}
}

func TestStartLeaseRenewer_StopWaitsForActiveRenew(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	renew := func(context.Context, string, string) error {
		close(started)
		<-release
		return nil
	}
	_, stop := startLeaseRenewerFn(context.Background(), renew, "L1", "w1", time.Millisecond)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("renew did not start")
	}

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned while renew was still active")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not wait for renewer exit")
	}
}

func TestStartLeaseRenewer_FailClosedCancelsJob(t *testing.T) {
	var calls atomic.Int32
	renew := func(ctx context.Context, leaseID, workerID string) error {
		n := calls.Add(1)
		if n >= 2 {
			return pool.ErrNotFound
		}
		return nil
	}
	jobCtx, stop := startLeaseRenewerFn(context.Background(), renew, "L-exp", "w1", 25*time.Millisecond)
	defer stop()

	select {
	case <-jobCtx.Done():
		// fail-closed: Renew error cancels job context
	case <-time.After(500 * time.Millisecond):
		t.Fatal("jobCtx not cancelled after renew failure")
	}
	if calls.Load() < 2 {
		t.Fatalf("calls=%d want >=2", calls.Load())
	}
}

// mockLeaseReleaseAPI implements leaseReleaseAPI for release cooldown tests.
type mockLeaseReleaseAPI struct {
	mu           sync.Mutex
	releaseErr   error
	releaseCalls int
	defaultCD    time.Duration
	setCDCalls   []setCDCall
	setCDErr     error
}

type setCDCall struct {
	proxy, platform string
	d               time.Duration
}

func (m *mockLeaseReleaseAPI) Release(ctx context.Context, leaseID, workerID string, opts pool.ReleaseOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseCalls++
	return m.releaseErr
}

func (m *mockLeaseReleaseAPI) SetCooldown(ctx context.Context, proxy, platform string, d time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setCDCalls = append(m.setCDCalls, setCDCall{proxy: proxy, platform: platform, d: d})
	return m.setCDErr
}

func (m *mockLeaseReleaseAPI) DefaultCooldown() time.Duration {
	if m.defaultCD <= 0 {
		return 2 * time.Minute
	}
	return m.defaultCD
}

func TestReleaseProxyLease_CooldownFallbackOnNotFound(t *testing.T) {
	mock := &mockLeaseReleaseAPI{
		releaseErr: pool.ErrNotFound,
		defaultCD:  90 * time.Second,
	}
	lease := &pool.Lease{
		ID:       "gone",
		Proxy:    "http://px:1",
		Platform: "steam",
		WorkerID: "w1",
	}
	releaseProxyLease(context.Background(), mock, lease, "w1", pool.ReleaseOptions{
		SetCooldown: true,
		Cooldown:    3 * time.Minute,
	})
	if mock.releaseCalls != 1 {
		t.Fatalf("releaseCalls=%d", mock.releaseCalls)
	}
	if len(mock.setCDCalls) != 1 {
		t.Fatalf("setCDCalls=%d want 1", len(mock.setCDCalls))
	}
	c := mock.setCDCalls[0]
	if c.proxy != "http://px:1" || c.platform != "steam" || c.d != 3*time.Minute {
		t.Fatalf("setCD call: %+v", c)
	}
}

func TestReleaseProxyLease_CooldownFallbackUsesDefaultDuration(t *testing.T) {
	mock := &mockLeaseReleaseAPI{
		releaseErr: errors.New("redis timeout"),
		defaultCD:  45 * time.Second,
	}
	lease := &pool.Lease{ID: "x", Proxy: "p1", Platform: "buff", WorkerID: "w"}
	// Any Release error with SetCooldown triggers fallback (R2).
	releaseProxyLease(context.Background(), mock, lease, "w", pool.ReleaseOptions{SetCooldown: true})
	if len(mock.setCDCalls) != 1 {
		t.Fatalf("setCDCalls=%d", len(mock.setCDCalls))
	}
	if mock.setCDCalls[0].d != 45*time.Second {
		t.Fatalf("cooldown duration=%v want default 45s", mock.setCDCalls[0].d)
	}
	if mock.setCDCalls[0].proxy != "p1" || mock.setCDCalls[0].platform != "buff" {
		t.Fatalf("setCD: %+v", mock.setCDCalls[0])
	}
}

func TestReleaseProxyLease_NoFallbackWithoutCooldown(t *testing.T) {
	mock := &mockLeaseReleaseAPI{releaseErr: pool.ErrNotFound}
	lease := &pool.Lease{ID: "x", Proxy: "p", Platform: "steam", WorkerID: "w"}
	releaseProxyLease(context.Background(), mock, lease, "w", pool.ReleaseOptions{})
	if len(mock.setCDCalls) != 0 {
		t.Fatalf("unexpected setCD: %+v", mock.setCDCalls)
	}
}

func TestReleaseProxyLease_SuccessNoFallback(t *testing.T) {
	mock := &mockLeaseReleaseAPI{}
	lease := &pool.Lease{ID: "ok", Proxy: "p", Platform: "steam", WorkerID: "w"}
	releaseProxyLease(context.Background(), mock, lease, "w", pool.ReleaseOptions{
		SetCooldown: true,
		Cooldown:    time.Minute,
	})
	if mock.releaseCalls != 1 {
		t.Fatalf("releaseCalls=%d", mock.releaseCalls)
	}
	if len(mock.setCDCalls) != 0 {
		t.Fatalf("success path should not call SetCooldown fallback: %+v", mock.setCDCalls)
	}
}

func TestReleaseProxyLease_LogRedactsResourceAndError(t *testing.T) {
	var buf bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	const (
		proxy   = "http://proxy-user:proxy-pass@10.0.0.3:8080"
		lease   = "lease-log-secret"
		errText = "redis://user:redis-pass@db.internal/0"
	)
	mock := &mockLeaseReleaseAPI{releaseErr: errors.New(errText)}
	releaseProxyLease(context.Background(), mock, &pool.Lease{ID: lease, Proxy: proxy, Platform: "steam"}, "worker", pool.ReleaseOptions{})

	line := buf.String()
	for _, secret := range []string{proxy, lease, errText, "proxy-pass", "redis-pass"} {
		if strings.Contains(line, secret) {
			t.Fatalf("release log leaked %q: %s", secret, line)
		}
	}
	for _, want := range []string{"node_ref=node_v1_", "lease_ref=lease_v1_", "error_ref=detail_v1_"} {
		if !strings.Contains(line, want) {
			t.Fatalf("release log missing %q: %s", want, line)
		}
	}
}
