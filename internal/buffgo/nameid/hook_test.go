package nameid

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"buff-go/internal/buffgo/catalog"
	"buff-go/internal/buffgo/steam"
)

func TestMaybeBackfillAfterSell_DisabledWhenLimitZero(t *testing.T) {
	st := newMemStore(catalog.Item{AppID: 1, MarketHashName: "X"})
	res := &fakeResolver{ids: map[string]string{"X": "1"}}
	stats := MaybeBackfillAfterSell(context.Background(), st, res, 1, 0)
	if stats.Attempted != 0 {
		t.Fatalf("expected no-op: %+v", stats)
	}
	if len(res.calls) != 0 {
		t.Fatalf("resolver should not be called")
	}
}

func TestMaybeBackfillAfterSell_RunsWhenLimitPositive(t *testing.T) {
	const appid int64 = 252490
	st := newMemStore(catalog.Item{AppID: appid, MarketHashName: "Y"})
	res := &fakeResolver{ids: map[string]string{"Y": "99"}}
	// Avoid long env delay: temporarily set delay env.
	t.Setenv(EnvBackfillDelay, "1ms")
	stats := MaybeBackfillAfterSell(context.Background(), st, res, appid, 3)
	if stats.Updated != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	if st.get(appid, "Y").SteamItemNameID != "99" {
		t.Fatal("not updated")
	}
}

func TestMaybeBackfillAfterSell_NilSafe(t *testing.T) {
	stats := MaybeBackfillAfterSell(context.Background(), nil, nil, 1, 5)
	if stats.Attempted != 0 {
		t.Fatalf("%+v", stats)
	}
	// Ensure test finishes quickly even if wiring changes.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = MaybeBackfillAfterSell(ctx, newMemStore(), &fakeResolver{}, 0, 5)
}

// roundTripFunc is an http.RoundTripper for constructor tests (no real network).
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestNewDefaultResolver_NilClient(t *testing.T) {
	c := NewDefaultResolver(nil)
	if c == nil {
		t.Fatal("expected non-nil steam.Client")
	}
	var _ Resolver = c
}

func TestNewDefaultResolver_WithClientNoNetwork(t *testing.T) {
	var hits int32
	hc := &http.Client{
		Timeout: 2 * time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			atomic.AddInt32(&hits, 1)
			return nil, errors.New("blocked: no network in unit test")
		}),
	}
	c := NewDefaultResolver(hc)
	if c == nil {
		t.Fatal("nil resolver")
	}
	// Resolve must go through the injected client, not dial steamcommunity.com.
	_, err := c.ResolveItemNameID(context.Background(), 730, "AK-47 | Redline (Field-Tested)")
	if err == nil {
		t.Fatal("expected transport error from custom client")
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("provided HTTP client was not used")
	}
}

func TestNewResolverWithClient_SetsProxyIDAndClient(t *testing.T) {
	var hits int32
	hc := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			atomic.AddInt32(&hits, 1)
			return nil, errors.New("blocked: no network")
		}),
	}
	c := NewResolverWithClient(hc, "proxy-east-1")
	if c == nil {
		t.Fatal("nil")
	}
	if c.ProxyID != "proxy-east-1" {
		t.Fatalf("ProxyID: got %q want proxy-east-1", c.ProxyID)
	}
	_, err := c.ResolveItemNameID(context.Background(), 252490, "Metal Facemask")
	if err == nil {
		t.Fatal("expected error from blocked transport")
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("HTTP client not used")
	}

	// Direct / empty proxyID + nil client: still constructs (default egress).
	d := NewResolverWithClient(nil, steam.ProxyDirect)
	if d == nil {
		t.Fatal("nil default")
	}
	if d.ProxyID != steam.ProxyDirect {
		t.Fatalf("ProxyID: got %q want %q", d.ProxyID, steam.ProxyDirect)
	}
}
