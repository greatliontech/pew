package main

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/greatliontech/pew/internal/run"
)

// derivePin derives the CPU set a --pin switch measures on and reports
// the choice with its derivation before any listing or build (spec §9,
// REQ-pew-pin-derivation): the operator sees which CPUs and why at the
// moment the run is about to spend measurement time on them. A host the
// set cannot be derived on refuses here, never a guessed set.
func derivePin(errw io.Writer) (run.Pin, error) {
	pin, err := run.DerivePin()
	if err != nil {
		return run.Pin{}, fmt.Errorf("--pin: %w", err)
	}
	// The pin is applied through taskset: a host without it is refused
	// here, before the listing, not at the first measurement.
	if _, err := exec.LookPath("taskset"); err != nil {
		return run.Pin{}, fmt.Errorf("--pin: %w", err)
	}
	fmt.Fprintf(errw, "pew: pinning to CPUs %s (%s)\n", pin.List(), pin.Reason)
	return pin, nil
}

// environments is a run's two process environments: analysis for the
// listing, loads, and builds; runtime for the measured process, its
// observation ingest, and the engine's producer environment — nil when
// they are one (unpinned). Built once per run, so the environment the
// observation is ingested under and the one revalidation judges under
// are the same value by construction.
type environments struct {
	analysis, runtime []string
}

func newEnvironments(env []string, pin run.Pin) environments {
	e := environments{analysis: env}
	if pin.Pinned() {
		e.runtime = pinEnvironment(env, pin)
	}
	return e
}

// measured is the environment the measured process runs under.
func (e environments) measured() []string {
	if e.runtime != nil {
		return e.runtime
	}
	return e.analysis
}

// pinEnvironment is the environment the measured process runs under: the
// analysis environment plus GOMAXPROCS at the pin's width when the
// operator set none. Stating the width lets the runtime-configuration
// guard (spec §5) separate a pinned recording from an unpinned one —
// without it a baseline measured on one core would serve an unpinned run
// as valid. The width is the pin's CPU count bounded by this process's
// own default, which is what the runtime derives for an unset
// GOMAXPROCS (the affinity count, and on toolchains that honor one, a
// container's CPU quota), so the stated value is the one the measured
// process would have derived under the pin. An operator's own non-empty
// GOMAXPROCS is a deliberate configuration the guard already captures
// and is kept; an empty one is no configuration and is replaced. Only
// the measured process and the engine's producer environment see this
// environment: go's own builds and loads parallelize on GOMAXPROCS and
// are not pinned.
func pinEnvironment(env []string, pin run.Pin) []string {
	if !pin.Pinned() {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, "GOMAXPROCS="); ok {
			if value != "" {
				return env
			}
			continue
		}
		out = append(out, kv)
	}
	width := min(len(pin.CPUs), runtime.GOMAXPROCS(0))
	return append(out, "GOMAXPROCS="+strconv.Itoa(width))
}
