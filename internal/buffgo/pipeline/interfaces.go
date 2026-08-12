package pipeline

import (
	"context"
	"fmt"

	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/store"
	"buff-go/internal/catalog"
)

// ItemResolver maps verified platform identity evidence onto a catalog product.
type ItemResolver interface {
	Resolve(ctx context.Context, offer source.RawOffer) (catalog.MatchResult, error)
}

// QuoteWriter persists current-price quotes (PG or in-memory).
// *store.QuoteStore and *store.MemoryQuoteStore both implement this.
type QuoteWriter interface {
	UpsertQuotes(ctx context.Context, quotes []store.Quote) (store.UpsertResult, error)
	ListByAppIDPlatformSide(ctx context.Context, appid int64, platform, side string, limit int) ([]store.Quote, error)
}

type quoteWriterReadiness interface {
	Ready() error
}

func ensureQuoteWriterReady(writer QuoteWriter) error {
	if readiness, ok := writer.(quoteWriterReadiness); ok {
		return readiness.Ready()
	}
	return nil
}

func validateOfferSide(offer source.RawOffer, expected source.Side) error {
	if offer.Observation == nil {
		return fmt.Errorf("market observation is required")
	}
	if offer.Observation.Side != expected {
		return fmt.Errorf("market observation side must be %s", expected)
	}
	return nil
}

func validateOfferScope(offer source.RawOffer, platform string, appid int64) error {
	if offer.Platform != platform || offer.AppID != appid {
		return fmt.Errorf("market offer is outside the current job scope")
	}
	return nil
}

func validateIdentityMatch(result catalog.MatchResult) error {
	validMethod := result.Method == catalog.MatchMethodExistingMapping || result.Method == catalog.MatchMethodExactName
	if result.Status != catalog.MatchStatusMatched || result.ProductID <= 0 || !validMethod ||
		result.Reason != catalog.MatchReasonNone {
		return fmt.Errorf("catalog identity result is not a valid match")
	}
	return nil
}

// OfferFetcher pulls Steam sell offers for a job (usually *source.SteamSellSource).
type OfferFetcher interface {
	Pull(ctx context.Context, opts source.SteamSellOptions) ([]source.RawOffer, error)
}

// Note: BuffOfferFetcher is declared in buff_sell.go (BuffSellOptions).
