package pipeline

import (
	"time"

	"buff-go/internal/buffgo/source"
	"buff-go/internal/market"
)

var testCollectedAt = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

func testCNYObservation(side market.Side, cents int64) *market.Observation {
	price := market.CNYCents(cents)
	observation, err := market.NewPresentObservation(market.PresentInput{
		Currency: market.CurrencyCNY, Side: side, PriceCents: &price, CollectedAt: testCollectedAt,
	})
	if err != nil {
		panic(err)
	}
	return &observation
}

func testPresentOffer(platform string, appid int64, exactName string, cents int64) source.RawOffer {
	return source.RawOffer{
		Platform: platform, AppID: appid, NameRaw: exactName, ExactName: exactName,
		Observation: testCNYObservation(market.SideAsk, cents),
	}
}
