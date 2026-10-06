package main

import (
	"testing"

	"github.com/greatliontech/pew/internal/gotool"
)

type testEnvironmentValue = gotool.Environment

func testEnvironment(t testing.TB, entries []string) gotool.Environment {
	t.Helper()
	env, err := gotool.NewEnvironment(entries)
	if err != nil {
		t.Fatal(err)
	}
	return env
}
