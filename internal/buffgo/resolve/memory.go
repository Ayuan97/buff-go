package resolve

import (
	"context"
	"fmt"
	"sync"

	"buff-go/internal/buffgo/source"
	"buff-go/internal/catalog"
)

// MemoryResolver is an in-process adapter around the canonical catalog matcher.
// It is used by pure pipeline tests and never creates products or mappings.
type MemoryResolver struct {
	mu       sync.RWMutex
	nextID   catalog.ProductID
	products []catalog.SteamProduct
	mappings []catalog.PlatformMapping
}

// NewMemoryResolver builds an empty in-memory resolver.
func NewMemoryResolver() *MemoryResolver {
	return &MemoryResolver{nextID: 1}
}

// SeedProduct adds one Steam catalog product and returns its internal ID.
func (r *MemoryResolver) SeedProduct(appid int64, name string) catalog.ProductID {
	if r == nil || appid <= 0 || name == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nextID <= 0 {
		r.nextID = 1
	}
	id := r.nextID
	r.nextID++
	r.products = append(r.products, catalog.SteamProduct{ProductID: id, AppID: appid, Name: name})
	return id
}

// SeedMapping adds existing, already-verified platform mapping evidence.
// Duplicate/conflicting rows are retained so catalog.Match can reject them.
func (r *MemoryResolver) SeedMapping(productID catalog.ProductID, appid int64, platform, platformItemID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mappings = append(r.mappings, catalog.PlatformMapping{
		Platform: platform, AppID: appid, PlatformItemID: platformItemID, ProductID: productID,
	})
}

// Resolve converts only explicitly verified identity evidence into the pure
// matcher input. Raw names and raw Steam-like fields are intentionally ignored.
func (r *MemoryResolver) Resolve(ctx context.Context, offer source.RawOffer) (catalog.MatchResult, error) {
	if r == nil {
		return catalog.MatchResult{}, fmt.Errorf("nil resolver")
	}
	if err := ctx.Err(); err != nil {
		return catalog.MatchResult{}, err
	}
	if err := offer.Normalize(); err != nil {
		return catalog.MatchResult{}, err
	}

	r.mu.RLock()
	products := append([]catalog.SteamProduct(nil), r.products...)
	mappings := append([]catalog.PlatformMapping(nil), r.mappings...)
	r.mu.RUnlock()

	result := catalog.Match(catalog.PlatformProduct{
		Platform:       offer.Platform,
		AppID:          offer.AppID,
		PlatformItemID: offer.PlatformItemID,
		ExactName:      offer.ExactName,
	}, products, mappings)
	if result.Status != catalog.MatchStatusMatched {
		return result, fmt.Errorf("catalog identity match rejected: %s", result.Reason)
	}
	return result, nil
}

// ProductCount returns the number of seeded products.
func (r *MemoryResolver) ProductCount() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.products)
}

// MappingCount returns the number of explicit mappings. Matching never changes it.
func (r *MemoryResolver) MappingCount() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.mappings)
}
