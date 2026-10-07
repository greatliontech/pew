package run

import (
	"context"
	"path/filepath"

	"github.com/greatliontech/gofresh/runtimeinput"
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
	return ObservationFrame{ProducerFrame: runtimeinput.CaptureProducerFrame(ctx, moduleDir,
		filepath.Join(moduleDir, filepath.FromSlash(pkgRel)), runtimeinput.FrameOptions{})}
}

// ObservationFrame holds the pre-execution frame and independently prepared
// outcome support for one measured process. Missing support remains incomplete.
type ObservationFrame struct {
	runtimeinput.ProducerFrame
	Outcome runtimeinput.OutcomeSupport
}

// IngestObservation completes the run's observation from the testlog
// capture and the pre-spawn frame through the facade's fold discipline
// (spec §7.8): a refused frame, a missing, unreadable, or never-opened
// capture, and any ingest failure each record the canonical incomplete
// disposition with the honest reason — never an absent manifest, which
// would assert "no runtime inputs" and serve. Spawn and ingestion use
// the same environment source: Execute pins the resolved working
// directory by construction, so the go driver hands the test binary
// exactly frame.PkgDir as its PWD and the ingest mirror pins the same
// value — the posture the facade requires. scratch carries the
// package's declared run-scratch name patterns (the //pew:scratch
// directive): each becomes a gofresh scratch namespace over the
// package directory, admitting recordless only reads the engine proves
// absent at both bracket endpoints — the declaration forfeits exactly
// the appearance-pin of absence-probes matching the pattern, the
// caller-side responsibility the directive's author takes on. The
// classification roots come from the environment the process ran
// under: the ingest carries the same environment the spawn used, under
// a roots memo owned by this verb invocation, never shared across invocations.
// Root resolution inputs remain fixed for that invocation. Ingest uses
// the same go-command policy — an environment that policy refuses is
// not an ingest failure but the run's own input refusal, returned as
// the error it is.
func IngestObservation(ctx context.Context, frame ObservationFrame, logPath, identity string, roots *runtimeinput.Roots, env gotool.Environment, scratch ...string) (runtimeinput.Observation, error) {
	namespaces := make([]runtimeinput.ScratchNamespace, 0, len(scratch))
	for _, pattern := range scratch {
		namespaces = append(namespaces, runtimeinput.ScratchNamespace{Dir: frame.PkgRel, Pattern: pattern})
	}
	// Environment admission already happened before preparation. Only the
	// directory-derived PWD is constructed here from the observation frame.
	ingestEnv, err := env.For(frame.PkgDir)
	if err != nil {
		return runtimeinput.Observation{}, err
	}
	receipt, err := frame.Completion(identity, ingestEnv, "")
	if err != nil {
		return runtimeinput.Observation{}, err
	}
	ingest := runtimeinput.ProducerIngest{
		Identity:   identity,
		Roots:      roots,
		Env:        ingestEnv,
		Completion: receipt,
		Outcome:    frame.Outcome,
		// The classification roots (toolchain, module cache, build
		// cache, the temp root) are facts of the ingested environment
		// the facade resolves; pew mints no scratch root of its own.
		ScratchNamespaces: namespaces,
	}
	binding, err := frame.OutcomeBinding(identity, ingestEnv)
	if err != nil {
		return runtimeinput.Observation{}, err
	}
	var observation runtimeinput.Observation
	if frame.Outcome.Reason(binding) == "" {
		observation, _, err = frame.Observe(ctx, logPath, ingest)
	} else {
		// Identity guards remain useful under the ordinary freshness policy.
		// This explicit path emits no outcome claim and cannot enable a lift.
		observation, err = frame.ObserveInputs(ctx, logPath, ingest)
	}
	if err != nil {
		return runtimeinput.Observation{}, err
	}
	if _, err := runtimeinput.CompletedState(observation); err != nil {
		return runtimeinput.Observation{}, err
	}
	return observation, nil
}
