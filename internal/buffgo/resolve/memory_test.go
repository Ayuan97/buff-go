package resolve

import (
	"context"
	"testing"

	"buff-go/internal/buffgo/source"
	"buff-go/internal/catalog"
)

func TestMemoryResolver_ExactNameIsUniqueCaseSensitiveAndAppScoped(t *testing.T) {
	r := NewMemoryResolver()
	id := r.SeedProduct(252490, "Metal Facemask")
	r.SeedProduct(730, "Metal Facemask")

	got, err := r.Resolve(context.Background(), source.RawOffer{
		Platform: source.PlatformBuff, AppID: 252490, ExactName: "Metal Facemask",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ProductID != id || got.Method != catalog.MatchMethodExactName {
		t.Fatalf("match = %+v", got)
	}

	for _, name := range []string{"metal facemask", " Metal Facemask", "Metal Facemask ", "金属面具"} {
		if _, err := r.Resolve(context.Background(), source.RawOffer{
			Platform: source.PlatformBuff, AppID: 252490, ExactName: name,
		}); err == nil {
			t.Fatalf("non-exact name %q unexpectedly matched", name)
		}
	}
}

func TestMemoryResolver_AmbiguousAndRawEvidenceDoNotResolve(t *testing.T) {
	r := NewMemoryResolver()
	r.SeedProduct(252490, "Duplicate")
	r.SeedProduct(252490, "Duplicate")

	result, err := r.Resolve(context.Background(), source.RawOffer{
		Platform: source.PlatformBuff, AppID: 252490, ExactName: "Duplicate",
	})
	if err == nil || result.Reason != catalog.MatchReasonAmbiguousExactName || result.ProductID != 0 {
		t.Fatalf("ambiguous result=%+v err=%v", result, err)
	}

	result, err = r.Resolve(context.Background(), source.RawOffer{
		Platform: source.PlatformBuff, AppID: 252490,
		NameRaw: "Duplicate", MarketHashName: "Duplicate",
	})
	if err == nil || result.ProductID != 0 {
		t.Fatalf("raw evidence unexpectedly resolved: result=%+v err=%v", result, err)
	}
}

func TestMemoryResolver_ExistingMappingIsStableAndNeverAutoCreated(t *testing.T) {
	r := NewMemoryResolver()
	id := r.SeedProduct(252490, "Metal Facemask")
	r.SeedMapping(id, 252490, source.PlatformBuff, "90001")

	beforeProducts, beforeMappings := r.ProductCount(), r.MappingCount()
	for _, exactName := range []string{"", "Changed Platform Name"} {
		got, err := r.Resolve(context.Background(), source.RawOffer{
			Platform: source.PlatformBuff, AppID: 252490,
			PlatformItemID: "90001", ExactName: exactName,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.ProductID != id || got.Method != catalog.MatchMethodExistingMapping {
			t.Fatalf("mapped result = %+v", got)
		}
	}
	if r.ProductCount() != beforeProducts || r.MappingCount() != beforeMappings {
		t.Fatalf("matching mutated resolver: products=%d mappings=%d", r.ProductCount(), r.MappingCount())
	}

	if _, err := r.Resolve(context.Background(), source.RawOffer{
		Platform: source.PlatformSteam, AppID: 252490, PlatformItemID: "90001",
	}); err == nil {
		t.Fatal("mapping crossed platform boundary")
	}
	if _, err := r.Resolve(context.Background(), source.RawOffer{
		Platform: source.PlatformBuff, AppID: 730, PlatformItemID: "90001",
	}); err == nil {
		t.Fatal("mapping crossed appid boundary")
	}

	nameOnly := NewMemoryResolver()
	nameID := nameOnly.SeedProduct(252490, "Name Fallback")
	got, err := nameOnly.Resolve(context.Background(), source.RawOffer{
		Platform: source.PlatformBuff, AppID: 252490,
		PlatformItemID: "new-platform-id", ExactName: "Name Fallback",
	})
	if err != nil || got.ProductID != nameID || got.Method != catalog.MatchMethodExactName {
		t.Fatalf("exact-name fallback result=%+v err=%v", got, err)
	}
	if nameOnly.ProductCount() != 1 || nameOnly.MappingCount() != 0 {
		t.Fatalf("name fallback created state: products=%d mappings=%d", nameOnly.ProductCount(), nameOnly.MappingCount())
	}
	if _, err := nameOnly.Resolve(context.Background(), source.RawOffer{
		Platform: source.PlatformBuff, AppID: 252490,
		PlatformItemID: "unknown-platform-id", ExactName: "Unknown",
	}); err == nil {
		t.Fatal("unknown product unexpectedly created")
	}
	if nameOnly.ProductCount() != 1 || nameOnly.MappingCount() != 0 {
		t.Fatalf("failed match created state: products=%d mappings=%d", nameOnly.ProductCount(), nameOnly.MappingCount())
	}
}

func TestMemoryResolver_ConflictingMappingFailsWithoutNameFallback(t *testing.T) {
	r := NewMemoryResolver()
	idA := r.SeedProduct(252490, "A")
	idB := r.SeedProduct(252490, "B")
	r.SeedMapping(idA, 252490, source.PlatformBuff, "x")
	r.SeedMapping(idB, 252490, source.PlatformBuff, "x")

	result, err := r.Resolve(context.Background(), source.RawOffer{
		Platform: source.PlatformBuff, AppID: 252490,
		PlatformItemID: "x", ExactName: "A",
	})
	if err == nil || result.Reason != catalog.MatchReasonMultipleMappingTargets || result.ProductID != 0 {
		t.Fatalf("conflict result=%+v err=%v", result, err)
	}
}

func TestMemoryResolver_RejectsInvalidInputAndCanceledContext(t *testing.T) {
	r := NewMemoryResolver()
	if _, err := r.Resolve(context.Background(), source.RawOffer{Platform: source.PlatformSteam}); err == nil {
		t.Fatal("invalid offer unexpectedly resolved")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Resolve(ctx, source.RawOffer{
		Platform: source.PlatformSteam, AppID: 252490, ExactName: "x",
	}); err == nil {
		t.Fatal("canceled resolve unexpectedly succeeded")
	}
}
