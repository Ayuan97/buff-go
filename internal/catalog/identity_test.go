package catalog

import (
	"reflect"
	"testing"
)

func TestMatchReviewMatrix(t *testing.T) {
	tests := []struct {
		name     string
		product  PlatformProduct
		products []SteamProduct
		mappings []PlatformMapping
		want     MatchResult
	}{
		{
			name:    "existing mapping has priority over a different exact name",
			product: PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "New Name"},
			products: []SteamProduct{
				{ProductID: 1, AppID: 730, Name: "Old Name"},
				{ProductID: 2, AppID: 730, Name: "New Name"},
			},
			mappings: []PlatformMapping{{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 1}},
			want:     matchedResult(1, MatchMethodExistingMapping),
		},
		{
			name:     "existing mapping does not require a name",
			product:  PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Catalog Name"}},
			mappings: []PlatformMapping{{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 1}},
			want:     matchedResult(1, MatchMethodExistingMapping),
		},
		{
			name:    "existing mapping has priority over an ambiguous exact name",
			product: PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "Same Name"},
			products: []SteamProduct{
				{ProductID: 1, AppID: 730, Name: "Mapped Name"},
				{ProductID: 2, AppID: 730, Name: "Same Name"},
				{ProductID: 3, AppID: 730, Name: "Same Name"},
			},
			mappings: []PlatformMapping{{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 1}},
			want:     matchedResult(1, MatchMethodExistingMapping),
		},
		{
			name:     "unique exact name within appid",
			product:  PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			want:     matchedResult(1, MatchMethodExactName),
		},
		{
			name:    "same name in another appid is ignored",
			product: PlatformProduct{Platform: "buff", AppID: 730, ExactName: "Shared Name"},
			products: []SteamProduct{
				{ProductID: 1, AppID: 570, Name: "Shared Name"},
				{ProductID: 2, AppID: 730, Name: "Shared Name"},
			},
			want: matchedResult(2, MatchMethodExactName),
		},
		{
			name:     "case is not normalized",
			product:  PlatformProduct{Platform: "buff", AppID: 730, ExactName: "exact name"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			want:     unmatchedResult(MatchReasonNoMatch),
		},
		{
			name:     "whitespace is not trimmed",
			product:  PlatformProduct{Platform: "buff", AppID: 730, ExactName: " Exact Name "},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			want:     unmatchedResult(MatchReasonNoMatch),
		},
		{
			name:     "unicode is not normalized",
			product:  PlatformProduct{Platform: "buff", AppID: 730, ExactName: "e\u0301"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "\u00e9"}},
			want:     unmatchedResult(MatchReasonNoMatch),
		},
		{
			name:    "mapping identity is not normalized",
			product: PlatformProduct{Platform: "BUFF", AppID: 730, PlatformItemID: " item-1 ", ExactName: "Exact Name"},
			products: []SteamProduct{
				{ProductID: 1, AppID: 730, Name: "Mapped Name"},
				{ProductID: 2, AppID: 730, Name: "Exact Name"},
			},
			mappings: []PlatformMapping{{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 1}},
			want:     matchedResult(2, MatchMethodExactName),
		},
		{
			name:    "ambiguous exact name is not mapped",
			product: PlatformProduct{Platform: "buff", AppID: 730, ExactName: "Same Name"},
			products: []SteamProduct{
				{ProductID: 1, AppID: 730, Name: "Same Name"},
				{ProductID: 2, AppID: 730, Name: "Same Name"},
			},
			want: unmatchedResult(MatchReasonAmbiguousExactName),
		},
		{
			name:     "no exact name match",
			product:  PlatformProduct{Platform: "buff", AppID: 730, ExactName: "Missing"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Other"}},
			want:     unmatchedResult(MatchReasonNoMatch),
		},
		{
			name:     "empty exact name cannot match empty catalog name",
			product:  PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1"},
			products: []SteamProduct{{ProductID: 1, AppID: 730}},
			want:     invalidResult(MatchReasonInvalidCatalog),
		},
		{
			name:    "empty platform item id cannot hit an empty id mapping",
			product: PlatformProduct{Platform: "buff", AppID: 730, ExactName: "Exact Name"},
			products: []SteamProduct{
				{ProductID: 1, AppID: 730, Name: "Exact Name"},
				{ProductID: 2, AppID: 730, Name: "Mapped Name"},
			},
			mappings: []PlatformMapping{{Platform: "buff", AppID: 730, PlatformItemID: "", ProductID: 2}},
			want:     matchedResult(1, MatchMethodExactName),
		},
		{
			name:     "empty exact name without mapping is unmatched",
			product:  PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Catalog Name"}},
			want:     unmatchedResult(MatchReasonNoMatch),
		},
		{
			name:     "platform is required",
			product:  PlatformProduct{AppID: 730, PlatformItemID: "item-1", ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			want:     invalidResult(MatchReasonMissingIdentity),
		},
		{
			name:     "platform item id and name cannot both be absent",
			product:  PlatformProduct{Platform: "buff", AppID: 730},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			want:     invalidResult(MatchReasonMissingIdentity),
		},
		{
			name:     "platform appid is required",
			product:  PlatformProduct{Platform: "buff", ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			want:     invalidResult(MatchReasonInvalidAppID),
		},
		{
			name:     "catalog product id must be positive",
			product:  PlatformProduct{Platform: "buff", AppID: 730, ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 0, AppID: 730, Name: "Exact Name"}},
			want:     invalidResult(MatchReasonInvalidProductID),
		},
		{
			name:     "catalog product id cannot be negative",
			product:  PlatformProduct{Platform: "buff", AppID: 730, ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: -1, AppID: 730, Name: "Exact Name"}},
			want:     invalidResult(MatchReasonInvalidProductID),
		},
		{
			name:    "catalog product id must be unique",
			product: PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "Exact Name"},
			products: []SteamProduct{
				{ProductID: 1, AppID: 730, Name: "Exact Name"},
				{ProductID: 1, AppID: 730, Name: "Other Name"},
			},
			mappings: []PlatformMapping{{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 1}},
			want:     invalidResult(MatchReasonDuplicateCatalogProductID),
		},
		{
			name:     "catalog appid must be valid",
			product:  PlatformProduct{Platform: "buff", AppID: 730, ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 1, AppID: 0, Name: "Exact Name"}},
			want:     invalidResult(MatchReasonInvalidCatalog),
		},
		{
			name:     "catalog name is required",
			product:  PlatformProduct{Platform: "buff", AppID: 730, ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: ""}},
			want:     invalidResult(MatchReasonInvalidCatalog),
		},
		{
			name:    "same mapping key cannot point to multiple products",
			product: PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "Exact Name"},
			products: []SteamProduct{
				{ProductID: 1, AppID: 730, Name: "Exact Name"},
				{ProductID: 2, AppID: 730, Name: "Other Name"},
			},
			mappings: []PlatformMapping{
				{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 1},
				{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 2},
			},
			want: invalidResult(MatchReasonMultipleMappingTargets),
		},
		{
			name:     "duplicate mapping is rejected",
			product:  PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			mappings: []PlatformMapping{
				{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 1},
				{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 1},
			},
			want: invalidResult(MatchReasonDuplicateMapping),
		},
		{
			name:     "mapping target must exist and cannot fall back to name",
			product:  PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			mappings: []PlatformMapping{{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 2}},
			want:     invalidResult(MatchReasonMappingProductNotFound),
		},
		{
			name:    "mapping target appid must agree and cannot fall back to name",
			product: PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "Exact Name"},
			products: []SteamProduct{
				{ProductID: 1, AppID: 570, Name: "Mapped Name"},
				{ProductID: 2, AppID: 730, Name: "Exact Name"},
			},
			mappings: []PlatformMapping{{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 1}},
			want:     invalidResult(MatchReasonMappingAppIDConflict),
		},
		{
			name:     "mapping target id must be positive and cannot fall back to name",
			product:  PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			mappings: []PlatformMapping{{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ProductID: 0}},
			want:     invalidResult(MatchReasonInvalidProductID),
		},
		{
			name:     "unrelated mapping conflicts do not affect this product",
			product:  PlatformProduct{Platform: "buff", AppID: 730, PlatformItemID: "item-1", ExactName: "Exact Name"},
			products: []SteamProduct{{ProductID: 1, AppID: 730, Name: "Exact Name"}},
			mappings: []PlatformMapping{
				{Platform: "buff", AppID: 730, PlatformItemID: "other", ProductID: 1},
				{Platform: "buff", AppID: 730, PlatformItemID: "other", ProductID: 2},
			},
			want: matchedResult(1, MatchMethodExactName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Match(tt.product, tt.products, tt.mappings); got != tt.want {
				t.Fatalf("Match() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMatchDoesNotMutateInputs(t *testing.T) {
	product := PlatformProduct{Platform: " buff ", AppID: 730, PlatformItemID: " item-1 ", ExactName: " Exact Name "}
	products := []SteamProduct{{ProductID: 1, AppID: 730, Name: " Exact Name "}}
	mappings := []PlatformMapping{{Platform: " buff ", AppID: 730, PlatformItemID: " item-1 ", ProductID: 1}}
	wantProduct := product
	wantProducts := append([]SteamProduct(nil), products...)
	wantMappings := append([]PlatformMapping(nil), mappings...)

	if got := Match(product, products, mappings); got != matchedResult(1, MatchMethodExistingMapping) {
		t.Fatalf("Match() = %+v", got)
	}
	if product != wantProduct || !reflect.DeepEqual(products, wantProducts) || !reflect.DeepEqual(mappings, wantMappings) {
		t.Fatalf("inputs mutated: product=%+v products=%+v mappings=%+v", product, products, mappings)
	}
}

func matchedResult(productID ProductID, method MatchMethod) MatchResult {
	return MatchResult{ProductID: productID, Status: MatchStatusMatched, Method: method, Reason: MatchReasonNone}
}

func unmatchedResult(reason MatchReason) MatchResult {
	return MatchResult{Status: MatchStatusUnmatched, Method: MatchMethodNone, Reason: reason}
}

func invalidResult(reason MatchReason) MatchResult {
	return MatchResult{Status: MatchStatusInvalid, Method: MatchMethodNone, Reason: reason}
}
