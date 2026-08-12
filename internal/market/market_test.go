package market

import (
	"testing"
	"time"
)

func TestNewPresentObservationRequiresExplicitCNYAndWirePrice(t *testing.T) {
	collectedAt := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	zero := CNYCents(0)
	price := CNYCents(1234)

	tests := []struct {
		name     string
		currency string
		price    *CNYCents
		want     CNYCents
		wantErr  bool
	}{
		{name: "cny integer cents", currency: "CNY", price: &price, want: 1234},
		{name: "normalized currency code", currency: " cny ", price: &price, want: 1234},
		{name: "explicit zero price", currency: "CNY", price: &zero, want: 0},
		{name: "missing price", currency: "CNY", wantErr: true},
		{name: "usd rejected", currency: "USD", price: &price, wantErr: true},
		{name: "missing currency rejected", price: &price, wantErr: true},
		{name: "ambiguous yuan symbol rejected", currency: "¥", price: &price, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observation, err := NewPresentObservation(PresentInput{
				Currency: tt.currency, Side: SideAsk, PriceCents: tt.price, CollectedAt: collectedAt,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewPresentObservation() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if observation.Summary == nil || observation.Summary.PriceCents != tt.want {
				t.Fatalf("observation = %+v, want %d cents", observation, tt.want)
			}
		})
	}
}

func TestParseCNYCentsIsExact(t *testing.T) {
	tests := []struct {
		value   string
		want    CNYCents
		wantErr bool
	}{
		{value: "0", want: 0},
		{value: "0.00", want: 0},
		{value: "12.3", want: 1230},
		{value: "12.34", want: 1234},
		{value: " 001.05 ", want: 105},
		{value: "", wantErr: true},
		{value: ".50", wantErr: true},
		{value: "1.", wantErr: true},
		{value: "1.234", wantErr: true},
		{value: "-1", wantErr: true},
		{value: "+1", wantErr: true},
		{value: "1e2", wantErr: true},
		{value: "1,000.00", wantErr: true},
		{value: "92233720368547758.08", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := ParseCNYCents(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseCNYCents(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("ParseCNYCents(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestObservationValidateKeepsStatusAndPriceSeparate(t *testing.T) {
	collectedAt := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	price := CNYCents(1234)
	present, err := NewPresentObservation(PresentInput{
		Currency: CurrencyCNY, Side: SideAsk, PriceCents: &price, CollectedAt: collectedAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		observation Observation
		wantErr     bool
	}{
		{name: "present", observation: present},
		{name: "empty", observation: Observation{Side: SideAsk, Status: StatusEmpty, CollectedAt: collectedAt}},
		{name: "unavailable", observation: Observation{Side: SideBid, Status: StatusUnavailable, CollectedAt: collectedAt}},
		{name: "failed", observation: Observation{Side: SideAsk, Status: StatusFailed, CollectedAt: collectedAt}},
		{name: "buy alias rejected", observation: Observation{Side: Side("buy"), Status: StatusEmpty, CollectedAt: collectedAt}, wantErr: true},
		{name: "sell alias rejected", observation: Observation{Side: Side("sell"), Status: StatusEmpty, CollectedAt: collectedAt}, wantErr: true},
		{name: "missing collected time", observation: Observation{Side: SideAsk, Status: StatusFailed}, wantErr: true},
		{name: "present without constructor", observation: Observation{Side: SideAsk, Status: StatusPresent, Summary: present.Summary, CollectedAt: collectedAt}, wantErr: true},
		{name: "empty with price", observation: Observation{Side: SideAsk, Status: StatusEmpty, Summary: present.Summary, CollectedAt: collectedAt}, wantErr: true},
		{name: "unknown status", observation: Observation{Side: SideAsk, Status: ObservationStatus("partial"), CollectedAt: collectedAt}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.observation.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPresentCountsRemainIndependentAndOptional(t *testing.T) {
	collectedAt := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	price := CNYCents(99)
	zero := int64(0)
	orders := int64(12)
	negative := int64(-1)

	valid, err := NewPresentObservation(PresentInput{
		Currency: CurrencyCNY, Side: SideBid, PriceCents: &price,
		OrderCount: &orders, ItemCount: nil, CollectedAt: collectedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if valid.Summary.OrderCount == nil || *valid.Summary.OrderCount != 12 || valid.Summary.ItemCount != nil {
		t.Fatalf("counts were merged: %+v", valid.Summary)
	}

	if _, err := NewPresentObservation(PresentInput{
		Currency: CurrencyCNY, Side: SideAsk, PriceCents: &price,
		OrderCount: &zero, ItemCount: &negative, CollectedAt: collectedAt,
	}); err == nil {
		t.Fatal("negative item_count must fail")
	}
}

func TestObservationTimesApplyToEveryStatus(t *testing.T) {
	collectedAt := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	zeroTime := time.Time{}
	failed := Observation{
		Side: SideAsk, Status: StatusFailed, SourceTime: &zeroTime, CollectedAt: collectedAt,
	}
	if err := failed.Validate(); err == nil {
		t.Fatal("provided zero source_time must fail")
	}
}

func TestPresentObservationRejectsPriceMutationAfterVerification(t *testing.T) {
	price := CNYCents(1234)
	observation, err := NewPresentObservation(PresentInput{
		Currency:    CurrencyCNY,
		Side:        SideAsk,
		PriceCents:  &price,
		CollectedAt: time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	observation.Summary = &PresentSummary{PriceCents: 9999}
	if err := observation.Validate(); err == nil {
		t.Fatal("mutated price must not retain the original CNY verification")
	}
}
