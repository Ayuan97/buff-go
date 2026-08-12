package pool

import (
	"context"
	"testing"
	"time"
)

// StaticProvider unit tests require no Redis (SLICE C).

func TestStaticProvider_EmptyIsDirectMode(t *testing.T) {
	ctx := context.Background()

	for name, p := range map[string]*StaticProvider{
		"nil_receiver_via_new": NewStaticProvider(nil),
		"empty_slice":          NewStaticProvider([]ProxyEndpoint{}),
		"all_disabled": NewStaticProvider([]ProxyEndpoint{
			{Endpoint: "http://1.1.1.1:8080", LineType: LineOversea, Enabled: false},
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if !p.IsDirectMode() {
				t.Fatal("expected direct mode")
			}
			list, err := p.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 1 {
				t.Fatalf("list len=%d want 1", len(list))
			}
			if list[0].ProxyID() != ProxyIDDirect {
				t.Fatalf("proxy_id=%q want %q", list[0].ProxyID(), ProxyIDDirect)
			}
			if !list[0].IsDirect() {
				t.Fatal("IsDirect")
			}
			if list[0].LineType != LineDual {
				t.Fatalf("direct line_type=%q want dual", list[0].LineType)
			}
		})
	}

	// nil provider pointer List path via CandidatesFromProvider
	cands, err := CandidatesFromProvider(ctx, nil, 252490)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].ID != ProxyIDDirect {
		t.Fatalf("nil provider candidates: %+v", cands)
	}
}

func TestStaticProvider_ListEnabledOnly(t *testing.T) {
	ctx := context.Background()
	p := NewStaticProvider([]ProxyEndpoint{
		{Endpoint: "http://cn.example:8080", Auth: "u:p", LineType: LineCN, Enabled: true, OnlyAppIDs: []int64{252490}},
		{Endpoint: "http://off.example:8080", LineType: LineOversea, Enabled: false},
		{Endpoint: "http://steam.example:8080", LineType: LineOversea, Enabled: true},
	})
	if p.IsDirectMode() {
		t.Fatal("should not be direct with enabled rows")
	}
	if p.LenConfigured() != 3 {
		t.Fatalf("configured=%d", p.LenConfigured())
	}
	list, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("enabled list=%d want 2", len(list))
	}
	if list[0].ProxyID() != "http://cn.example:8080" || list[0].Auth != "u:p" {
		t.Fatalf("first: %+v", list[0])
	}
	if list[0].LineType != LineCN || len(list[0].OnlyAppIDs) != 1 || list[0].OnlyAppIDs[0] != 252490 {
		t.Fatalf("first fields: %+v", list[0])
	}
	if list[1].ProxyID() != "http://steam.example:8080" {
		t.Fatalf("second: %+v", list[1])
	}
}

func TestStaticProvider_CandidatesOrderWithOnlyAppIDs(t *testing.T) {
	ctx := context.Background()
	p := NewStaticProvider([]ProxyEndpoint{
		{Endpoint: "shared", LineType: LineDual, Enabled: true},
		{Endpoint: "rust-only", LineType: LineDual, Enabled: true, OnlyAppIDs: []int64{252490}},
		{Endpoint: "cs-only", LineType: LineDual, Enabled: true, OnlyAppIDs: []int64{730}},
		{Endpoint: "prefer-rust", LineType: LineDual, Enabled: true, PreferAppIDs: []int64{252490}},
	})
	rust, err := CandidatesFromProvider(ctx, p, 252490)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"rust-only", "prefer-rust", "shared"}
	got := idsOf(rust)
	if !equalStr(got, want) {
		t.Fatalf("rust candidates: got %v want %v", got, want)
	}
	for _, c := range rust {
		if c.ID == "cs-only" {
			t.Fatal("cs-only must be filtered for rust")
		}
	}
}

func TestStaticProviderFromInput(t *testing.T) {
	ctx := context.Background()
	p := NewStaticProviderFromInput([]StaticProxyInput{
		{Endpoint: "http://a", LineType: "oversea", Enabled: true},
		{Endpoint: "http://b", LineType: "cn", Enabled: false},
	})
	list, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ProxyID() != "http://a" || list[0].LineType != LineOversea {
		t.Fatalf("list: %+v", list)
	}
}

func TestProxyEndpoint_AcquireFieldsAndCandidate(t *testing.T) {
	e := ProxyEndpoint{
		Endpoint:   "  http://x:1  ",
		LineType:   "Oversea",
		OnlyAppIDs: []int64{252490},
		Enabled:    true,
	}
	e = normalizeEndpoint(e)
	proxy, line, only := e.AcquireFields()
	if proxy != "http://x:1" || line != LineOversea || len(only) != 1 || only[0] != 252490 {
		t.Fatalf("AcquireFields: %q %q %v", proxy, line, only)
	}
	c := e.Candidate()
	if c.ID != "http://x:1" || c.LineType != LineOversea {
		t.Fatalf("Candidate: %+v", c)
	}

	// empty endpoint → direct id
	d := ProxyEndpoint{Endpoint: "", Enabled: true}
	if d.ProxyID() != ProxyIDDirect || !d.IsDirect() {
		t.Fatalf("empty endpoint id: %q", d.ProxyID())
	}
}

// TestNormalizeEndpoint_EmptyLineTypeDefaultsDual ensures misconfigured TOML
// without line_type does not permanently soft-skip via ErrLineMismatch (R10).
func TestNormalizeEndpoint_EmptyLineTypeDefaultsDual(t *testing.T) {
	// non-direct empty → dual
	e := normalizeEndpoint(ProxyEndpoint{
		Endpoint: "http://proxy.example:8080",
		Enabled:  true,
	})
	if e.LineType != LineDual {
		t.Fatalf("non-direct empty line_type: got %q want dual", e.LineType)
	}
	if !LineAllowed("buff", e.LineType, nil) || !LineAllowed("steam", e.LineType, nil) {
		t.Fatalf("default dual should pass buff and steam LineAllowed")
	}

	// direct empty still dual
	d := normalizeEndpoint(ProxyEndpoint{Endpoint: "", Enabled: true})
	if d.LineType != LineDual {
		t.Fatalf("direct empty line_type: got %q want dual", d.LineType)
	}

	// explicit line types preserved
	cn := normalizeEndpoint(ProxyEndpoint{Endpoint: "http://cn", LineType: "CN", Enabled: true})
	if cn.LineType != LineCN {
		t.Fatalf("explicit cn: got %q", cn.LineType)
	}
}

func TestOptionsFromConfig_AndManagerHook(t *testing.T) {
	// Manager construction with config-shaped options does not require a provider;
	// provider is independent. This only checks Options mapping (no Redis).
	opt := OptionsFromConfig(30*time.Second, 3*time.Minute, map[string][]string{
		"steam": {LineOversea, LineDual},
	}, map[int64]int{252490: 10})
	if opt.LeaseTTL != 30*time.Second || opt.DefaultCooldown != 3*time.Minute {
		t.Fatalf("options: %+v", opt)
	}
	if opt.MaxProxyLeases[252490] != 10 {
		t.Fatalf("quota: %v", opt.MaxProxyLeases)
	}
	if len(opt.PlatformLines["steam"]) != 2 {
		t.Fatalf("lines: %v", opt.PlatformLines)
	}

	// Static provider + candidate loop shape (documented acquire hook).
	prov := NewStaticProvider([]ProxyEndpoint{
		{Endpoint: "http://ov:8080", LineType: LineOversea, Enabled: true},
	})
	cands, err := CandidatesFromProvider(context.Background(), prov, 252490)
	if err != nil || len(cands) != 1 {
		t.Fatalf("cands: %v %v", cands, err)
	}
	req := AcquireRequest{
		WorkerID:   "w1",
		Proxy:      cands[0].ID,
		Platform:   "steam",
		LineType:   cands[0].LineType,
		AppID:      252490,
		OnlyAppIDs: cands[0].OnlyAppIDs,
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("acquire request from provider: %v", err)
	}
}

func TestDirectEndpoint_MatchesBothPlatformPrefer(t *testing.T) {
	d := DirectEndpoint()
	if !LineAllowed("buff", d.LineType, nil) {
		t.Fatal("direct dual should match buff prefer")
	}
	if !LineAllowed("steam", d.LineType, nil) {
		t.Fatal("direct dual should match steam prefer")
	}
}
