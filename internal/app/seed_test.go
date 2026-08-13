package app

import (
	"testing"

	"buff-go/internal/ratelimit"
)

func TestSteamRateLimitSeedSpecs(t *testing.T) {
	specs := steamRateLimitSpecs()
	if len(specs) != 3 {
		t.Fatalf("len=%d", len(specs))
	}
	var platformRate, interfaceProfile bool
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			t.Fatalf("%s: %v", spec.RuleKey, err)
		}
		if spec.Platform != "steam" {
			t.Fatalf("platform=%q", spec.Platform)
		}
		if spec.Scope == ratelimit.ScopePlatform && spec.IsRate() {
			platformRate = true
		}
		if spec.Scope == ratelimit.ScopeInterface {
			interfaceProfile = true
			if spec.Kind != ratelimit.KindCooldownOnly {
				t.Fatalf("interface kind=%q", spec.Kind)
			}
		}
	}
	if !platformRate || !interfaceProfile {
		t.Fatal("seed must provide platform rate and interface cooldown")
	}
}
