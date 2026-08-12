package pipeline

import "fmt"

// Completeness records whether the run proved coverage of its whole target.
// It is independent from market observation status and run failure reason.
type Completeness string

const (
	CompletenessComplete Completeness = "complete"
	CompletenessPartial  Completeness = "partial"
)

// Validate rejects the zero value so an unstarted or failed run cannot be
// mistaken for complete coverage.
func (c Completeness) Validate() error {
	if c != CompletenessComplete && c != CompletenessPartial {
		return fmt.Errorf("completeness must be complete or partial")
	}
	return nil
}
