package main

import (
	"context"
	"testing"
)

func testDependencies(t *testing.T) (context.Context, *executionDependencies) {
	t.Helper()
	d := dependencies(t.Context())
	return context.WithValue(t.Context(), dependenciesKey{}, d), d
}
