package resolve

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"buff-go/internal/buffgo/source"
)

func TestLegacyPostgresResolverFailsBeforeSQL(t *testing.T) {
	// A zero sql.DB would panic if QueryContext were reached. The fixed error
	// proves the legacy fuzzy/ensure rules are quarantined before database IO.
	r := New(&sql.DB{})
	_, err := r.Resolve(context.Background(), source.RawOffer{
		Platform: source.PlatformBuff, AppID: 252490,
		PlatformItemID: "90001", ExactName: "Metal Facemask",
	})
	if !errors.Is(err, ErrLegacyResolverUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

func TestLegacyPostgresResolverRejectsNilHandle(t *testing.T) {
	if _, err := New(nil).Resolve(context.Background(), source.RawOffer{}); err == nil {
		t.Fatal("nil database unexpectedly accepted")
	}
}
