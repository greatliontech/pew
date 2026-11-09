package run

// The direct-facade conformance fixtures deliberately construct their own
// inputs to exercise classification failures and process-coordinate boundaries.
// Production benchmark arms use Gofresh's owned session units instead.

import (
	"context"
	"path/filepath"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gotool"
)

// CaptureObservationFrame captures the pre-spawn half of the
// completed-observation conjunction (spec §7.8) through gofresh's
// producer facade: resolved tree roots and the observation bracket
// fingerprinted over the package directory (VCS bookkeeping excluded)
// immediately before the measurement invocation. Frame failures never
// abort the run: a refused frame carries its reason and the run's
// observation records incomplete. (The classification-root read is
// different: a broken `go env` breaks the measurement itself, so it
// aborts like every other toolchain probe.)
func CaptureObservationFrame(ctx context.Context, moduleDir, pkgRel string) ObservationFrame {
	return ObservationFrame{ProducerFrame: gofresh.CaptureProducerFrame(ctx, moduleDir,
		filepath.Join(moduleDir, filepath.FromSlash(pkgRel)), gofresh.FrameOptions{})}
}

// ObservationFrame holds the pre-execution frame for one measured process.
type ObservationFrame struct {
	gofresh.ProducerFrame
}

// IngestObservation completes the run's observation from the testlog
// capture and the pre-spawn frame through the facade's fold discipline
// (spec §7.8).
func IngestObservation(ctx context.Context, frame ObservationFrame, logPath, identity string, roots *gofresh.InputRoots, env gotool.Environment, scratch ...string) (gofresh.Observation, error) {
	namespaces := make([]gofresh.ScratchNamespace, 0, len(scratch))
	for _, pattern := range scratch {
		namespaces = append(namespaces, gofresh.ScratchNamespace{Dir: frame.PkgRel, Pattern: pattern})
	}
	// Environment admission already happened before preparation. Only the
	// directory-derived PWD is constructed here from the observation frame.
	ingestEnv, err := env.For(frame.PkgDir)
	if err != nil {
		return gofresh.Observation{}, err
	}
	ingest := gofresh.ProducerIngest{
		Identity: identity,
		Roots:    roots,
		Env:      ingestEnv,
		// The classification roots (toolchain, module cache, build
		// cache, the temp root) are facts of the ingested environment
		// the facade resolves; pew mints no scratch root of its own.
		ScratchNamespaces: namespaces,
	}
	// Identity guards remain useful under the ordinary freshness policy.
	// This explicit path emits no outcome claim and cannot enable a lift.
	observation, _, err := frame.ObserveInputs(ctx, logPath, ingest)
	if err != nil {
		return gofresh.Observation{}, err
	}
	if _, err := gofresh.CompletedObservationState(observation); err != nil {
		return gofresh.Observation{}, err
	}
	return observation, nil
}
