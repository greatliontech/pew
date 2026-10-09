package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeClassifiesEveryFinalizationCause(t *testing.T) {
	unfulfilled := &profileUnfulfilledError{}
	empty := &nothingComparedError{reason: "empty"}
	cleanup := errors.New("cleanup failed")
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"profile", unfulfilled, 2},
		{"wrapped", fmt.Errorf("context: %w", unfulfilled), 2},
		{"only-unfulfilled", errors.Join(unfulfilled, fmt.Errorf("context: %w", empty)), 2},
		{"cleanup-after", errors.Join(unfulfilled, cleanup), 1},
		{"cleanup-before", errors.Join(cleanup, unfulfilled), 1},
		{"nested-cleanup", fmt.Errorf("finalizing: %w", errors.Join(errors.Join(unfulfilled, empty), cleanup)), 1},
		{"multiple-wrapped", fmt.Errorf("%w; %w", unfulfilled, cleanup), 1},
		{"interrupted", errors.Join(unfulfilled, cleanup, interrupted("signal")), 130},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Fatalf("exit=%d want=%d: %v", got, tc.want, tc.err)
			}
		})
	}
}
