package resource

import (
	"fmt"
	"time"
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

// AccountNodeCombination binds one platform account to one node.
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
	return nil
}

// ValidateForUse checks whether a current combination can dispatch a request
// for one platform target region.
func (resources CombinationResources) ValidateForUse(now time.Time, target TargetRegion) error {
	if err := resources.Validate(); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	if now.IsZero() {
		return fmt.Errorf("acquisition time is required")
	}
	if resources.Account.SessionState == AccountSessionStateInvalid {
		return ErrAccountSessionUnusable
	}
	// unverified 允许占租约，由首次成功的平台请求标 valid。
	if resources.Account.SessionState != AccountSessionStateValid && resources.Account.SessionState != AccountSessionStateUnverified {
		return ErrAccountSessionUnusable
	}
	if !resources.Node.UsableAt(now) {
		return fmt.Errorf("%w: access node is not usable", ErrResourceUnusable)
	}
	if !resources.Node.Region.Allows(target) {
		return fmt.Errorf("%w: access node region does not allow target region", ErrResourceUnusable)
	}
	return nil
}
