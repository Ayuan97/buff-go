package market

import "fmt"

// WriteOrder is the causal order of one market write. Every component is 1-based.
// WriteSequence is the target's monotonic page counter; there is no batch.
type WriteOrder struct {
	SwitchVersion int64
	WriteSequence int64
}

// Validate rejects missing or invalid ordering components.
func (o WriteOrder) Validate() error {
	if o.SwitchVersion < 1 {
		return fmt.Errorf("switch_version must be at least 1")
	}
	if o.WriteSequence < 1 {
		return fmt.Errorf("write_sequence must be at least 1")
	}
	return nil
}

// Compare returns -1, 0, or 1 using switch version then write sequence.
func (o WriteOrder) Compare(other WriteOrder) int {
	if o.SwitchVersion < other.SwitchVersion {
		return -1
	}
	if o.SwitchVersion > other.SwitchVersion {
		return 1
	}
	if o.WriteSequence < other.WriteSequence {
		return -1
	}
	if o.WriteSequence > other.WriteSequence {
		return 1
	}
	return 0
}
