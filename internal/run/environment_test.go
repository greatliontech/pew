package run

import (
	"testing"

	"github.com/greatliontech/pew/internal/gotool"
)

func testEnvironment(t testing.TB, entries []string) gotool.Environment {
	t.Helper()
	env, err := gotool.NewEnvironment(entries)
	if err != nil {
		t.Fatal(err)
	}
	return env
}
