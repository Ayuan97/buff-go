package catalog

// ProductID is the stable internal identity of a Steam catalog product.
type ProductID int64

// SteamProduct is the Steam-authoritative identity used for platform matching.
type SteamProduct struct {
	ProductID ProductID
	AppID     int64
	Name      string
}

// PlatformProduct is the identity evidence returned by a trading platform.
type PlatformProduct struct {
	Platform       string
	AppID          int64
	PlatformItemID string
	ExactName      string
}

// PlatformMapping links one platform-native identity to a Steam product.
type PlatformMapping struct {
	Platform       string
	AppID          int64
	PlatformItemID string
	ProductID      ProductID
}

// MatchStatus describes whether matching succeeded or was rejected.
type MatchStatus string

const (
	MatchStatusMatched   MatchStatus = "matched"
	MatchStatusUnmatched MatchStatus = "unmatched"
	MatchStatusInvalid   MatchStatus = "invalid"
)

// MatchMethod identifies the evidence that established a match.
type MatchMethod string

const (
	MatchMethodNone            MatchMethod = ""
	MatchMethodExistingMapping MatchMethod = "existing_mapping"
	MatchMethodExactName       MatchMethod = "exact_name"
)

// MatchReason explains an unsuccessful result.
type MatchReason string

const (
	MatchReasonNone                      MatchReason = ""
	MatchReasonNoMatch                   MatchReason = "no_match"
	MatchReasonAmbiguousExactName        MatchReason = "ambiguous_exact_name"
	MatchReasonMissingIdentity           MatchReason = "missing_identity"
	MatchReasonInvalidAppID              MatchReason = "invalid_appid"
	MatchReasonInvalidCatalog            MatchReason = "invalid_catalog"
	MatchReasonInvalidProductID          MatchReason = "invalid_product_id"
	MatchReasonDuplicateCatalogProductID MatchReason = "duplicate_catalog_product_id"
	MatchReasonDuplicateMapping          MatchReason = "duplicate_mapping"
	MatchReasonMultipleMappingTargets    MatchReason = "multiple_mapping_targets"
	MatchReasonMappingProductNotFound    MatchReason = "mapping_product_not_found"
	MatchReasonMappingAppIDConflict      MatchReason = "mapping_appid_conflict"
)

// MatchResult is a read-only platform-to-Steam identity decision.
type MatchResult struct {
	ProductID ProductID
	Status    MatchStatus
	Method    MatchMethod
	Reason    MatchReason
}

// Match resolves a platform product by an existing mapping, then by a unique
// byte-for-byte name match within the same appid. It never creates a mapping.
func Match(product PlatformProduct, products []SteamProduct, mappings []PlatformMapping) MatchResult {
	if product.Platform == "" || (product.PlatformItemID == "" && product.ExactName == "") {
		return invalidMatch(MatchReasonMissingIdentity)
	}
	if product.AppID <= 0 {
		return invalidMatch(MatchReasonInvalidAppID)
	}

	productsByID := make(map[ProductID]SteamProduct, len(products))
	for _, candidate := range products {
		if candidate.ProductID <= 0 {
			return invalidMatch(MatchReasonInvalidProductID)
		}
		if candidate.AppID <= 0 || candidate.Name == "" {
			return invalidMatch(MatchReasonInvalidCatalog)
		}
		if _, exists := productsByID[candidate.ProductID]; exists {
			return invalidMatch(MatchReasonDuplicateCatalogProductID)
		}
		productsByID[candidate.ProductID] = candidate
	}

	mappingCount := 0
	var mappedProductID ProductID
	if product.PlatformItemID != "" {
		for _, mapping := range mappings {
			if mapping.Platform != product.Platform ||
				mapping.AppID != product.AppID ||
				mapping.PlatformItemID != product.PlatformItemID {
				continue
			}
			mappingCount++
			if mappingCount == 1 {
				mappedProductID = mapping.ProductID
				continue
			}
			if mapping.ProductID != mappedProductID {
				return invalidMatch(MatchReasonMultipleMappingTargets)
			}
		}
	}

	if mappingCount > 1 {
		return invalidMatch(MatchReasonDuplicateMapping)
	}
	if mappingCount == 1 {
		if mappedProductID <= 0 {
			return invalidMatch(MatchReasonInvalidProductID)
		}
		mappedProduct, exists := productsByID[mappedProductID]
		if !exists {
			return invalidMatch(MatchReasonMappingProductNotFound)
		}
		if mappedProduct.AppID != product.AppID {
			return invalidMatch(MatchReasonMappingAppIDConflict)
		}
		return MatchResult{
			ProductID: mappedProductID,
			Status:    MatchStatusMatched,
			Method:    MatchMethodExistingMapping,
			Reason:    MatchReasonNone,
		}
	}
	if product.ExactName == "" {
		return MatchResult{Status: MatchStatusUnmatched, Method: MatchMethodNone, Reason: MatchReasonNoMatch}
	}

	var matchedProductID ProductID
	matchCount := 0
	for _, candidate := range products {
		if candidate.AppID != product.AppID || candidate.Name != product.ExactName {
			continue
		}
		matchCount++
		matchedProductID = candidate.ProductID
	}

	switch matchCount {
	case 0:
		return MatchResult{Status: MatchStatusUnmatched, Method: MatchMethodNone, Reason: MatchReasonNoMatch}
	case 1:
		return MatchResult{
			ProductID: matchedProductID,
			Status:    MatchStatusMatched,
			Method:    MatchMethodExactName,
			Reason:    MatchReasonNone,
		}
	default:
		return MatchResult{Status: MatchStatusUnmatched, Method: MatchMethodNone, Reason: MatchReasonAmbiguousExactName}
	}
}

func invalidMatch(reason MatchReason) MatchResult {
	return MatchResult{Status: MatchStatusInvalid, Method: MatchMethodNone, Reason: reason}
}
