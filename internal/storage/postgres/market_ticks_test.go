package postgres

import (
	"testing"

	"buff-go/internal/market"
)

func TestShouldRecordPriceTick(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		exists bool
		old    market.CNYCents
		new    market.CNYCents
		want   bool
	}{
		{name: "first present", exists: false, new: 100, want: true},
		{name: "same cents", exists: true, old: 100, new: 100, want: false},
		{name: "cents changed", exists: true, old: 100, new: 101, want: true},
		{name: "zero to value", exists: true, old: 0, new: 1, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldRecordPriceTick(test.exists, test.old, test.new); got != test.want {
				t.Fatalf("shouldRecordPriceTick(%v, %d, %d) = %v, want %v",
					test.exists, test.old, test.new, got, test.want)
			}
		})
	}
}
