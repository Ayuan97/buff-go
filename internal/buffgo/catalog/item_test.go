package catalog

import "testing"

func TestItemNormalize(t *testing.T) {
	it := Item{AppID: 252490, MarketHashName: "  Metal Facemask  "}
	if err := it.Normalize(); err != nil {
		t.Fatal(err)
	}
	if it.MarketHashName != "Metal Facemask" {
		t.Fatalf("hash: %q", it.MarketHashName)
	}
	if it.Name != "Metal Facemask" {
		t.Fatalf("name filled from hash: %q", it.Name)
	}

	bad := Item{AppID: 0, MarketHashName: "x"}
	if err := bad.Normalize(); err == nil {
		t.Fatal("expected appid error")
	}
	bad2 := Item{AppID: 252490, MarketHashName: "  "}
	if err := bad2.Normalize(); err == nil {
		t.Fatal("expected market_hash_name error")
	}
}

func TestKnownGame_Rust(t *testing.T) {
	g := KnownGame(252490)
	if g.Code != "rust" || g.Name != "Rust" || !g.Enabled {
		t.Fatalf("rust meta: %+v", g)
	}
	g2 := KnownGame(999001)
	if g2.Code != "app999001" {
		t.Fatalf("generic: %+v", g2)
	}
}

func TestKnownGame_CS2_MultiGameModel(t *testing.T) {
	// Second appid for P5.1 multi-game model (not a long-term hard requirement).
	g := KnownGame(730)
	if g.Code != "cs2" || g.AppID != 730 || !g.Enabled {
		t.Fatalf("cs2 meta: %+v", g)
	}
}
