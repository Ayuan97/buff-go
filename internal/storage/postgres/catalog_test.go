package postgres

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"buff-go/internal/catalog"
)

func TestNewRejectsNilDatabase(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil database accepted")
	}
	if _, err := New(&sql.DB{}); err != nil {
		t.Fatalf("non-nil database rejected: %v", err)
	}
}

func TestSteamProductValidationPreservesRawName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		appid int64
		value string
		ok    bool
	}{
		{name: "valid", appid: 730, value: "  AK-47 | Redline  ", ok: true},
		{name: "whitespace is raw data", appid: 730, value: " ", ok: true},
		{name: "empty name", appid: 730, value: "", ok: false},
		{name: "zero appid", appid: 0, value: "AK-47", ok: false},
		{name: "negative appid", appid: -1, value: "AK-47", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSteamProductInput(tc.appid, tc.value)
			if (err == nil) != tc.ok {
				t.Fatalf("error = %v, ok = %v", err, tc.ok)
			}
		})
	}
}

func TestPlatformValidationIsExactCanonicalToken(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{value: "steam", ok: true},
		{value: "buff_cn-1.test", ok: true},
		{value: "a" + strings.Repeat("0", 31), ok: true},
		{value: "", ok: false},
		{value: "Steam", ok: false},
		{value: " steam", ok: false},
		{value: "steam ", ok: false},
		{value: "1steam", ok: false},
		{value: "a" + strings.Repeat("0", 32), ok: false},
	} {
		if err := validatePlatform(tc.value); (err == nil) != tc.ok {
			t.Errorf("validatePlatform(%q) error = %v, ok = %v", tc.value, err, tc.ok)
		}
	}
}

func TestPlatformMappingValidationDoesNotNormalizeIdentity(t *testing.T) {
	valid := catalog.PlatformMapping{
		Platform:       "buff",
		AppID:          730,
		PlatformItemID: "  Item-01  ",
		ProductID:      9,
	}
	if err := validatePlatformMapping(valid); err != nil {
		t.Fatalf("raw platform item id rejected: %v", err)
	}

	for _, mapping := range []catalog.PlatformMapping{
		{Platform: "", AppID: 730, PlatformItemID: "1", ProductID: 9},
		{Platform: "BUFF", AppID: 730, PlatformItemID: "1", ProductID: 9},
		{Platform: "buff", AppID: 0, PlatformItemID: "1", ProductID: 9},
		{Platform: "buff", AppID: 730, PlatformItemID: "", ProductID: 9},
		{Platform: "buff", AppID: 730, PlatformItemID: "1", ProductID: 0},
	} {
		if err := validatePlatformMapping(mapping); err == nil {
			t.Fatalf("invalid mapping accepted: %+v", mapping)
		}
	}
}

func TestCompareMappingTarget(t *testing.T) {
	if err := compareMappingTarget(7, 7); err != nil {
		t.Fatalf("same target rejected: %v", err)
	}
	if err := compareMappingTarget(7, 8); !errors.Is(err, ErrMappingConflict) {
		t.Fatalf("different target error = %v", err)
	}
}

func TestCatalogMethodsRejectInvalidInputBeforeSQL(t *testing.T) {
	store, err := New(&sql.DB{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	if _, err := store.CreateSteamProduct(ctx, 0, "name"); err == nil {
		t.Fatal("invalid create accepted")
	}
	if _, _, err := store.SteamProduct(ctx, 0); err == nil {
		t.Fatal("invalid product id accepted")
	}
	if _, err := store.ListSteamProductsByAppID(ctx, 0); err == nil {
		t.Fatal("invalid list appid accepted")
	}
	if err := store.PutPlatformMapping(ctx, catalog.PlatformMapping{}); err == nil {
		t.Fatal("invalid mapping accepted")
	}
	if _, _, err := store.PlatformMapping(ctx, "BUFF", 730, "1"); err == nil {
		t.Fatal("invalid mapping key accepted")
	}
}
