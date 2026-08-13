package resource

import (
	"fmt"
	"time"

	"buff-go/internal/market"
)

// CombinationID is the stable identity of one explicit account-node binding.
type CombinationID int64

// Validate rejects a missing combination identity.
func (id CombinationID) Validate() error {
	if id < 1 {
		return fmt.Errorf("combination_id must be at least 1")
	}
	return nil
}

// AccountNodeCombination binds one platform account to one assigned node.
type AccountNodeCombination struct {
	ID        CombinationID
	Platform  Platform
	AccountID AccountID
	NodeID    NodeID
}

// Validate checks the stable identity fields of a persisted combination.
func (combination AccountNodeCombination) Validate() error {
	if err := combination.ID.Validate(); err != nil {
		return err
	}
	if err := combination.Platform.Validate(); err != nil {
		return err
	}
	if err := combination.AccountID.Validate(); err != nil {
		return err
	}
	if err := combination.NodeID.Validate(); err != nil {
		return err
	}
	return nil
}

// CombinationResources is one combination joined with its current resources.
// Revisions belong to the resources and are intentionally not persisted in the
// combination itself.
type CombinationResources struct {
	Combination AccountNodeCombination
	Account     PlatformAccount
	Node        AccessNode
}

// Validate checks the relationship without requiring resources to be usable.
func (resources CombinationResources) Validate() error {
	if err := resources.Combination.Validate(); err != nil {
		return fmt.Errorf("combination: %w", err)
	}
	if err := resources.Account.Validate(); err != nil {
		return fmt.Errorf("account: %w", err)
	}
	if err := resources.Node.Validate(); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if resources.Combination.AccountID != resources.Account.ID {
		return fmt.Errorf("combination account_id does not match account")
	}
	if resources.Combination.NodeID != resources.Node.ID {
		return fmt.Errorf("combination node_id does not match node")
	}
	if resources.Combination.Platform != resources.Account.Platform {
		return fmt.Errorf("combination platform does not match account")
	}
	if resources.Node.AppID < 1 {
		return fmt.Errorf("combination node is not assigned to a game")
	}
	if _, ok := resources.Node.SideFor(resources.Combination.Platform); !ok {
		return fmt.Errorf("combination platform is not assigned on the node")
	}
	return nil
}

// ValidateForUse checks whether a current combination can dispatch a request
// for one game, platform direction, and target region.
func (resources CombinationResources) ValidateForUse(now time.Time, target TargetRegion, appID int64, side market.Side) error {
	if err := resources.Validate(); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	if now.IsZero() {
		return fmt.Errorf("acquisition time is required")
	}
	if appID < 1 {
		return fmt.Errorf("appid must be positive")
	}
	if side != market.SideBid && side != market.SideAsk {
		return fmt.Errorf("invalid side %q", side)
	}
	if !resources.Node.AssignedTo(appID, resources.Combination.Platform, side) {
		return fmt.Errorf("access node is not assigned to the requested game direction")
	}
	if resources.Account.SessionState != AccountSessionStateValid {
		return fmt.Errorf("account session is not valid")
	}
	if !resources.Node.UsableAt(now) {
		return fmt.Errorf("access node is not usable")
	}
	if !resources.Node.Region.Allows(target) {
		return fmt.Errorf("access node region does not allow target region")
	}
	return nil
}
