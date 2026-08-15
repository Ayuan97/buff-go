package catalog

import "testing"

func TestMatchRustItemClassPrefersLongerLabel(t *testing.T) {
	class, ok := MatchRustItemClass("Abyss Stone Hatchet")
	if !ok || class.Label != "Stone Hatchet" {
		t.Fatalf("got %+v ok=%v", class, ok)
	}
	class, ok = MatchRustItemClass("Roadsign Jacket")
	if !ok || class.Slug != "roadsign.jacket" {
		t.Fatalf("got %+v ok=%v", class, ok)
	}
	if _, ok := MatchRustItemClass("AK Royale"); ok {
		t.Fatal("AK Royale is not AK47u")
	}
}

func TestRustLabelsForCategories(t *testing.T) {
	labels := RustLabelsForCategories([]string{"steamcat.armor"})
	if len(labels) == 0 {
		t.Fatal("armor should have item classes")
	}
	for _, label := range labels {
		if label == "Hoodie" {
			t.Fatal("hoodie is clothing")
		}
	}
}

func TestValidRustFacets(t *testing.T) {
	if !ValidRustCategory("steamcat.armor") || ValidRustCategory("armor") {
		t.Fatal("category slug")
	}
	if !ValidRustItemClass("burlap.trousers") || ValidRustItemClass("Burlap Trousers") {
		t.Fatal("item class slug")
	}
}

func TestRustDisplayNames(t *testing.T) {
	for _, category := range RustCategories() {
		if category.Name == "" || category.Name == category.Label {
			t.Fatalf("category %s missing Chinese name", category.Slug)
		}
	}
	for _, class := range RustItemClasses() {
		if class.Name == "" {
			t.Fatalf("item class %s missing Chinese name", class.Slug)
		}
	}
	if class, ok := MatchRustItemClass("Forest Hoodie"); !ok || class.Label != "Hoodie" {
		t.Fatalf("matching must still use English label, got %+v ok=%v", class, ok)
	}
}
