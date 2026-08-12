package collection

import (
	"fmt"
	"math"
	"time"
)

func validateTime(name string, value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("%s is required", name)
	}
	if value.Location() != time.UTC {
		return fmt.Errorf("%s must use UTC", name)
	}
	if value.Year() < 1 || value.Year() > 9999 {
		return fmt.Errorf("%s is outside the supported range", name)
	}
	if value.Nanosecond()%int(time.Microsecond) != 0 {
		return fmt.Errorf("%s must have microsecond precision", name)
	}
	return nil
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func nextRevision(current Revision) (Revision, error) {
	if current < 1 || current == Revision(math.MaxInt64) {
		return 0, fmt.Errorf("revision cannot advance")
	}
	return current + 1, nil
}
