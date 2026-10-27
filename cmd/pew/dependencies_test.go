package main

import (
	"context"
	"testing"
)

func testDependencies(t *testing.T) (context.Context, *executionDependencies) {
	t.Helper()
	d := dependencies(t.Context())
	return withDependencies(t.Context(), d), d
}
