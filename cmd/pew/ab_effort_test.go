package main

import (
	"io"
	"strings"
	"testing"
)

func TestABRefusesInvalidEffortBeforePreparation(t *testing.T) {
	ctx := withWorkingDir(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	for _, budget := range []string{"0", "0s", "0x", "-1s", "-2x", "wat", "9999999999999999999999x"} {
		for _, cli := range []bool{false, true} {
			var err error
			if cli {
				cmd := newABCmd()
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs([]string{"--benchtime=" + budget, "--pin", "./..."})
				err = cmd.ExecuteContext(ctx)
			} else {
				err = runAB(ctx, io.Discard, io.Discard, abConfig{count: 1, bench: ".", benchtime: budget}, []string{"./..."})
			}
			if err == nil || !strings.Contains(err.Error(), "ab: benchtime: invalid positive budget") {
				t.Fatalf("cli=%t budget=%q: %v", cli, budget, err)
			}
		}
	}
	for _, budget := range []string{"", "1x", "+2x", "01x", "1.5ms", "1µs"} {
		if err := (abConfig{count: 1, benchtime: budget}).validateEffort(); err != nil {
			t.Fatalf("valid budget %q: %v", budget, err)
		}
	}
}
