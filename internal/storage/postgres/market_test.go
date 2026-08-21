package postgres

import (
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/market"
)

func TestPrepareBatchCopiesAndNormalizesPresentObservation(t *testing.T) {
	price := market.CNYCents(1234)
	orders := int64(0)
	items := int64(2)
	sourceTime := time.Date(2026, 8, 11, 13, 0, 0, 123456789, time.FixedZone("test", 3600))
	collectedAt := time.Date(2026, 8, 11, 13, 1, 0, 987654321, time.FixedZone("test", 3600))
	observation, err := market.NewPresentObservation(market.PresentInput{
		Currency:    market.CurrencyCNY,
		Side:        market.SideAsk,
		PriceCents:  &price,
		OrderCount:  &orders,
		ItemCount:   &items,
		SourceTime:  &sourceTime,
		CollectedAt: collectedAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	snapshots, err := prepareBatch(observationBatch{
		AppID:    730,
		Platform: "steam",
		Side:     market.SideAsk,
		Order:    market.WriteOrder{SwitchVersion: 1, WriteSequence: 3},
		Attempts: []AttemptWrite{{ProductID: catalog.ProductID(9), Observation: observation}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].present == nil {
		t.Fatalf("snapshots = %#v", snapshots)
	}
	got := snapshots[0]
	if got.present.priceCNYCents != price || got.present.orderCount == nil || *got.present.orderCount != 0 {
		t.Fatalf("present snapshot = %#v", got.present)
	}
	if got.sourceTime == nil || got.sourceTime.Location() != time.UTC || got.sourceTime.Nanosecond() != 123456000 {
		t.Fatalf("source_time = %v", got.sourceTime)
	}
	if got.collectedAt.Location() != time.UTC || got.collectedAt.Nanosecond() != 987654000 {
		t.Fatalf("collected_at = %v", got.collectedAt)
	}

	orders = 99
	items = 99
	if *got.present.orderCount != 0 || *got.present.itemCount != 2 {
		t.Fatal("snapshot retained caller-owned count pointers")
	}
}

func TestPrepareBatchRejectsInvalidOrInferredFacts(t *testing.T) {
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	validFailed := market.Observation{
		Side:        market.SideAsk,
		Status:      market.StatusFailed,
		CollectedAt: collectedAt,
	}
	validBatch := observationBatch{
		AppID:    730,
		Platform: "steam",
		Side:     market.SideAsk,
		Order:    market.WriteOrder{SwitchVersion: 1, WriteSequence: 1},
		Attempts: []AttemptWrite{{
			ProductID:   1,
			Observation: validFailed,
			ReasonCode:  "http.timeout",
		}},
	}

	tests := map[string]func(*observationBatch){
		"invalid appid":        func(batch *observationBatch) { batch.AppID = 0 },
		"invalid platform":     func(batch *observationBatch) { batch.Platform = "Steam" },
		"invalid side":         func(batch *observationBatch) { batch.Side = market.Side("sell") },
		"missing order":        func(batch *observationBatch) { batch.Order.WriteSequence = 0 },
		"empty batch":          func(batch *observationBatch) { batch.Attempts = nil },
		"invalid product":      func(batch *observationBatch) { batch.Attempts[0].ProductID = 0 },
		"mismatched side":      func(batch *observationBatch) { batch.Attempts[0].Observation.Side = market.SideBid },
		"unsafe reason":        func(batch *observationBatch) { batch.Attempts[0].ReasonCode = "upstream body\nsecret" },
		"missing failure code": func(batch *observationBatch) { batch.Attempts[0].ReasonCode = "" },
		"duplicate product": func(batch *observationBatch) {
			batch.Attempts = append(batch.Attempts, batch.Attempts[0])
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			batch := validBatch
			batch.Attempts = append([]AttemptWrite(nil), validBatch.Attempts...)
			mutate(&batch)
			if _, err := prepareBatch(batch, false); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestPrepareBatchReasonCodeMatchesAttemptStatus(t *testing.T) {
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	for _, status := range []market.ObservationStatus{market.StatusPresent, market.StatusEmpty} {
		if status == market.StatusPresent {
			continue
		}
		batch := observationBatch{
			AppID:    730,
			Platform: "steam",
			Side:     market.SideAsk,
			Order:    market.WriteOrder{SwitchVersion: 1, WriteSequence: 1},
			Attempts: []AttemptWrite{{
				ProductID: 1,
				Observation: market.Observation{
					Side:        market.SideAsk,
					Status:      status,
					CollectedAt: collectedAt,
				},
				ReasonCode: "should_not_exist",
			}},
		}
		if _, err := prepareBatch(batch, false); err == nil {
			t.Fatalf("status %q accepted reason_code", status)
		}
	}
}

func TestPrepareBatchRejectsTimeThatNormalizesToZero(t *testing.T) {
	almostZero := time.Time{}.Add(500 * time.Nanosecond)
	batch := observationBatch{
		AppID:    730,
		Platform: "steam",
		Side:     market.SideAsk,
		Order:    market.WriteOrder{SwitchVersion: 1, WriteSequence: 1},
		Attempts: []AttemptWrite{{
			ProductID: 1,
			Observation: market.Observation{
				Side:        market.SideAsk,
				Status:      market.StatusFailed,
				SourceTime:  &almostZero,
				CollectedAt: almostZero,
			},
			ReasonCode: "time.invalid",
		}},
	}
	if _, err := prepareBatch(batch, false); err == nil {
		t.Fatal("time that becomes zero at PostgreSQL precision was accepted")
	}
}

func TestValidateLatestRowRejectsZeroSourceTimeForPresent(t *testing.T) {
	zero := time.Time{}
	row := latestRow{
		status:            market.StatusPresent,
		sourceTime:        &zero,
		collectedAt:       time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
		order:             market.WriteOrder{SwitchVersion: 1, WriteSequence: 1},
		storageConsistent: true,
	}
	if err := validateLatestRow(row); err == nil {
		t.Fatal("present latest row with zero source_time was accepted")
	}
}

func TestCompareObservationOrderUsesRequestTimeBeforeWriteSequence(t *testing.T) {
	base := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		left    market.WriteOrder
		leftAt  time.Time
		right   market.WriteOrder
		rightAt time.Time
		want    int
	}{
		{
			name: "later request beats later commit sequence",
			left: market.WriteOrder{SwitchVersion: 1, WriteSequence: 1}, leftAt: base.Add(time.Second),
			right: market.WriteOrder{SwitchVersion: 1, WriteSequence: 2}, rightAt: base,
			want: 1,
		},
		{
			name: "earlier request loses despite later commit sequence",
			left: market.WriteOrder{SwitchVersion: 1, WriteSequence: 2}, leftAt: base,
			right: market.WriteOrder{SwitchVersion: 1, WriteSequence: 1}, rightAt: base.Add(time.Second),
			want: -1,
		},
		{
			name: "write sequence breaks equal time",
			left: market.WriteOrder{SwitchVersion: 1, WriteSequence: 2}, leftAt: base,
			right: market.WriteOrder{SwitchVersion: 1, WriteSequence: 1}, rightAt: base,
			want: 1,
		},
		{
			name: "new switch remains authoritative",
			left: market.WriteOrder{SwitchVersion: 2, WriteSequence: 1}, leftAt: base.Add(-time.Hour),
			right: market.WriteOrder{SwitchVersion: 1, WriteSequence: 99}, rightAt: base,
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareObservationOrder(tt.left, tt.leftAt, tt.right, tt.rightAt); got != tt.want {
				t.Fatalf("compareObservationOrder() = %d, want %d", got, tt.want)
			}
		})
	}
}
