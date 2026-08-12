package market

import "fmt"

// WriteOrder is the causal order of one market write. Every component is 1-based.
type WriteOrder struct {
	SwitchVersion int64
	RunSequence   int64
	PageSequence  int64
}

// Validate rejects missing or invalid ordering components.
func (o WriteOrder) Validate() error {
	if o.SwitchVersion < 1 {
		return fmt.Errorf("switch_version must be at least 1")
	}
	if o.RunSequence < 1 {
		return fmt.Errorf("run_sequence must be at least 1")
	}
	if o.PageSequence < 1 {
		return fmt.Errorf("page_sequence must be at least 1")
	}
	return nil
}

// Compare returns -1, 0, or 1 using switch, run, then page sequence order.
// Both orders must pass Validate before comparison.
func (o WriteOrder) Compare(other WriteOrder) int {
	if o.SwitchVersion < other.SwitchVersion {
		return -1
	}
	if o.SwitchVersion > other.SwitchVersion {
		return 1
	}
	if o.RunSequence < other.RunSequence {
		return -1
	}
	if o.RunSequence > other.RunSequence {
		return 1
	}
	if o.PageSequence < other.PageSequence {
		return -1
	}
	if o.PageSequence > other.PageSequence {
		return 1
	}
	return 0
}
