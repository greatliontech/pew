package run

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ValidateBudget accepts the Go benchmark harness's positive duration or
// positive iteration-count form. Spelling aliases preserve their Go meaning.
func ValidateBudget(value string) error {
	if count, iterations := strings.CutSuffix(value, "x"); iterations {
		if n, err := strconv.ParseInt(count, 10, 0); err == nil && n > 0 {
			return nil
		}
	} else if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return nil
	}
	return fmt.Errorf("invalid positive budget %q", value)
}

// ValidateEffort refuses an ordinary measurement that cannot produce the
// requested positive sample count and benchmark effort, before any preparation.
func (o Options) ValidateEffort() error {
	if o.Count <= 0 {
		return fmt.Errorf("run: count must be positive, got %d", o.Count)
	}
	if err := ValidateBudget(o.Benchtime); err != nil {
		return fmt.Errorf("run: benchtime: %w", err)
	}
	return nil
}
