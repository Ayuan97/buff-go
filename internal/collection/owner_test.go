package collection

import (
	"context"
	"testing"
)

func TestOwnerEpochContext(t *testing.T) {
	if _, ok := OwnerEpochFromContext(nil); ok {
		t.Fatal("nil context must not contain an owner epoch")
	}
	if _, ok := OwnerEpochFromContext(context.Background()); ok {
		t.Fatal("plain context must not contain an owner epoch")
	}

	ctx := WithOwnerEpoch(context.Background(), 7)
	epoch, ok := OwnerEpochFromContext(ctx)
	if !ok || epoch != 7 {
		t.Fatalf("owner epoch = %d, ok=%v, want 7", epoch, ok)
	}
	epoch, ok = OwnerEpochFromContext(context.WithoutCancel(ctx))
	if !ok || epoch != 7 {
		t.Fatalf("owner epoch after WithoutCancel = %d, ok=%v, want 7", epoch, ok)
	}
}

func TestWithOwnerEpochRejectsInvalidInput(t *testing.T) {
	for name, call := range map[string]func(){
		"nil context": func() { WithOwnerEpoch(nil, 1) },
		"zero epoch":  func() { WithOwnerEpoch(context.Background(), 0) },
		"negative":    func() { WithOwnerEpoch(context.Background(), -1) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("WithOwnerEpoch() did not panic")
				}
			}()
			call()
		})
	}
}
