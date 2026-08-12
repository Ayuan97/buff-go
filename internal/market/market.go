package market

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Side identifies the direction of a market quote.
type Side string

const (
	SideBid Side = "bid"
	SideAsk Side = "ask"
)

// CNYCents is an amount in integer Chinese yuan cents.
type CNYCents int64

// CurrencyCNY is the only currency accepted by the unified market contract.
const CurrencyCNY = "CNY"

// ParseCNYCents converts a non-negative decimal CNY amount to integer cents.
// Exponents, signs, separators, and fractions beyond two digits are rejected.
func ParseCNYCents(value string) (CNYCents, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ".")
	if value == "" || len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid CNY amount")
	}
	for _, r := range value {
		if (r < '0' || r > '9') && r != '.' {
			return 0, fmt.Errorf("invalid CNY amount")
		}
	}
	if len(parts) == 2 && (len(parts[1]) == 0 || len(parts[1]) > 2) {
		return 0, fmt.Errorf("invalid CNY fraction")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid CNY amount")
	}
	var fraction int64
	if len(parts) == 2 {
		fraction, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid CNY fraction")
		}
		if len(parts[1]) == 1 {
			fraction *= 10
		}
	}
	if whole > (math.MaxInt64-fraction)/100 {
		return 0, fmt.Errorf("CNY amount overflows cents")
	}
	return CNYCents(whole*100 + fraction), nil
}

// ObservationStatus describes the result of one collection attempt.
type ObservationStatus string

const (
	StatusPresent     ObservationStatus = "present"
	StatusEmpty       ObservationStatus = "empty"
	StatusUnavailable ObservationStatus = "unavailable"
	StatusFailed      ObservationStatus = "failed"
)

// PresentSummary contains only the price and optional, independently verified
// quantities of a successful observation.
type PresentSummary struct {
	PriceCents CNYCents
	OrderCount *int64
	ItemCount  *int64
}

// Validate checks the invariants of a present market summary.
func (s PresentSummary) Validate() error {
	if s.PriceCents < 0 {
		return fmt.Errorf("price_cents cannot be negative")
	}
	if s.OrderCount != nil && *s.OrderCount < 0 {
		return fmt.Errorf("order_count cannot be negative")
	}
	if s.ItemCount != nil && *s.ItemCount < 0 {
		return fmt.Errorf("item_count cannot be negative")
	}
	return nil
}

// Observation records the result of one collection attempt.
type Observation struct {
	Side          Side
	Status        ObservationStatus
	Summary       *PresentSummary
	SourceTime    *time.Time
	CollectedAt   time.Time
	cnyVerified   bool
	verifiedPrice CNYCents
}

// PresentInput is the explicit boundary from a verified platform response to
// the unified CNY market model. PriceCents is a pointer so a missing wire field
// cannot silently become a valid zero-price summary.
type PresentInput struct {
	Currency    string
	Side        Side
	PriceCents  *CNYCents
	OrderCount  *int64
	ItemCount   *int64
	SourceTime  *time.Time
	CollectedAt time.Time
}

// NewPresentObservation accepts only an explicit CNY currency code and integer
// cents. Platform adapters must not infer this evidence from a currency symbol.
func NewPresentObservation(input PresentInput) (Observation, error) {
	if strings.ToUpper(strings.TrimSpace(input.Currency)) != CurrencyCNY {
		return Observation{}, fmt.Errorf("currency must be explicit CNY")
	}
	if input.PriceCents == nil {
		return Observation{}, fmt.Errorf("price_cents is required")
	}
	observation := Observation{
		Side:   input.Side,
		Status: StatusPresent,
		Summary: &PresentSummary{
			PriceCents: *input.PriceCents,
			OrderCount: input.OrderCount,
			ItemCount:  input.ItemCount,
		},
		SourceTime:    input.SourceTime,
		CollectedAt:   input.CollectedAt,
		cnyVerified:   true,
		verifiedPrice: *input.PriceCents,
	}
	if err := observation.Validate(); err != nil {
		return Observation{}, err
	}
	return observation, nil
}

// Validate keeps attempt state separate from the last present summary.
func (o Observation) Validate() error {
	if o.Side != SideBid && o.Side != SideAsk {
		return fmt.Errorf("side must be bid or ask, got %q", o.Side)
	}
	if o.SourceTime != nil && o.SourceTime.IsZero() {
		return fmt.Errorf("source_time cannot be zero when provided")
	}
	if o.CollectedAt.IsZero() {
		return fmt.Errorf("collected_at is required")
	}
	switch o.Status {
	case StatusPresent:
		if o.Summary == nil {
			return fmt.Errorf("present observation requires summary")
		}
		if !o.cnyVerified {
			return fmt.Errorf("present observation requires explicit CNY verification")
		}
		if o.Summary.PriceCents != o.verifiedPrice {
			return fmt.Errorf("present observation price no longer matches CNY verification")
		}
		return o.Summary.Validate()
	case StatusEmpty, StatusUnavailable, StatusFailed:
		if o.Summary != nil {
			return fmt.Errorf("%s observation cannot contain summary", o.Status)
		}
		if o.cnyVerified {
			return fmt.Errorf("%s observation cannot contain currency verification", o.Status)
		}
		return nil
	default:
		return fmt.Errorf("invalid observation status %q", o.Status)
	}
}
