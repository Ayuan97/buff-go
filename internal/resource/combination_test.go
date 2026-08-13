package resource

import (
	"net/netip"
	"testing"
	"time"
)

func TestCombinationResourcesValidateForUse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	resources := usableCombinationResources(now, 1, 11, 21)
	if err := resources.ValidateForUse(now, TargetRegionDomestic, 730, "ask"); err != nil {
		t.Fatalf("ValidateForUse() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*CombinationResources)
	}{
		{name: "missing combination id", mutate: func(value *CombinationResources) { value.Combination.ID = 0 }},
		{name: "wrong account id", mutate: func(value *CombinationResources) { value.Combination.AccountID++ }},
		{name: "wrong node id", mutate: func(value *CombinationResources) { value.Combination.NodeID++ }},
		{name: "wrong account platform", mutate: func(value *CombinationResources) { value.Account.Platform = "buff" }},
		{name: "unassigned node", mutate: func(value *CombinationResources) { value.Node.AppID = 0; value.Node.Sides = nil }},
		{name: "invalid account session", mutate: func(value *CombinationResources) {
			value.Account.SessionState = AccountSessionStateInvalid
		}},
		{name: "unusable node", mutate: func(value *CombinationResources) {
			value.Node.State = NodeStateUnavailable
			value.Node.ExitVerification = nil
		}},
		{name: "wrong region", mutate: func(value *CombinationResources) { value.Node.Region = NodeRegionForeign }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := cloneCombinationResources(resources)
			test.mutate(&value)
			if err := value.ValidateForUse(now, TargetRegionDomestic, 730, "ask"); err == nil {
				t.Fatal("ValidateForUse() succeeded")
			}
		})
	}

	if err := resources.ValidateForUse(time.Time{}, TargetRegionDomestic, 730, "ask"); err == nil {
		t.Fatal("ValidateForUse() accepted zero acquisition time")
	}
	if err := resources.ValidateForUse(now, TargetRegion("dual"), 730, "ask"); err == nil {
		t.Fatal("ValidateForUse() accepted invalid target region")
	}
}

func TestCombinationIDValidate(t *testing.T) {
	t.Parallel()
	if err := CombinationID(1).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, id := range []CombinationID{0, -1} {
		if err := id.Validate(); err == nil {
			t.Errorf("CombinationID(%d).Validate() succeeded", id)
		}
	}
}

func usableCombinationResources(now time.Time, combinationID CombinationID, accountID AccountID, nodeID NodeID) CombinationResources {
	checkedAt := now.Add(-time.Minute)
	return CombinationResources{
		Combination: AccountNodeCombination{ID: combinationID, Platform: "steam", AccountID: accountID, NodeID: nodeID},
		Account: PlatformAccount{
			ID: accountID, Platform: "steam", Alias: "account", SessionState: AccountSessionStateValid,
			SessionRevision: 3, LastCheckedAt: &checkedAt,
		},
		Node: AccessNode{
			ID: nodeID, Name: "node", Kind: NodeKindDirect, Region: NodeRegionDomestic,
			EgressMode: EgressModeStatic, State: NodeStateAvailable, EgressRevision: 4,
			AssignmentRevision: 2, AppID: 730,
			Sides: []NodeSideAssignment{{Platform: "steam", Side: "ask"}},
			ExitVerification: &ExitVerification{
				VerifiedRevision: 4, Address: netip.MustParseAddr("8.8.8.8"),
				VerifiedAt: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour),
			},
		},
	}
}

func cloneCombinationResources(value CombinationResources) CombinationResources {
	if value.Account.LastCheckedAt != nil {
		checked := *value.Account.LastCheckedAt
		value.Account.LastCheckedAt = &checked
	}
	if value.Node.ExitVerification != nil {
		verification := *value.Node.ExitVerification
		value.Node.ExitVerification = &verification
	}
	if value.Node.StickySessionValidUntil != nil {
		deadline := *value.Node.StickySessionValidUntil
		value.Node.StickySessionValidUntil = &deadline
	}
	return value
}
