// Package recordingtest builds test recordings through the one writer:
// run.ProvenanceConfig and run.FingerprintConfigs over the registry's
// rows, so the §5 provenance and fingerprint lines of a test recording
// are the ones `pew run` composes, in the writer's order, and a key
// added to the registry reaches every fixture without a hand edit. Set
// feeds a row's value to the writer's own input (never a line edit);
// Omit removes an emitted row afterwards — a recording predating a
// line, a malformed historical file. Outside the builder's claim: the
// toolchain's four stream keys (goos, goarch, pkg, cpu) and a result
// name's GOMAXPROCS suffix are the caller's, and Conditions takes the
// line verbatim, well-formed or not. Test support only: no production
// package imports it (the registry's source walk refuses one).
package recordingtest

import (
	"strconv"
	"strings"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

// QuietConditions is the recorded run-conditions line of an undisturbed
// machine, the default every fixture carries.
const QuietConditions = "governor=performance turbo=off load1=0.03 throttled=false battery=false"

// Recording is the writer's inputs for one test recording; Defaults
// fills placeholders every value-blind test accepts.
type Recording struct {
	Commit          string
	Dirty           bool
	Conditions      string
	Fingerprint     gofresh.Fingerprint
	Ledger          string
	RuntimeDigest   string
	RuntimeManifest string
	omitted         []run.RecordingKey
}

// omitted rows are removed after composition, in option order.

// Option adjusts a Recording before its lines are composed.
type Option func(*Recording)

// Defaults is the placeholder recording: a current-strategy measurement
// on a quiet machine, every mandatory row set.
func Defaults() Recording {
	return Recording{
		Commit:     "c1",
		Conditions: QuietConditions,
		Fingerprint: gofresh.Fingerprint{
			MaximalClosure:       "cl1",
			ClosureStrategy:      gofresh.ClosureStrategy,
			DynamicStateStrategy: gofresh.DynamicStateStrategy,
			TestVariantClosure:   "tv1",
			Guards:               guard.Guards{Toolchain: "go-test", Machine: "m1", BuildConfig: "b1", RuntimeConfig: "r1"},
		},
		Ledger:          "ledger1",
		RuntimeDigest:   "rt1",
		RuntimeManifest: "manifest1",
	}
}

// Measured records the fingerprint a capture answered, with its ledger
// and runtime-input evidence — the shape a verdict test feeds back to
// the engine that computed it.
func Measured(fp gofresh.Fingerprint, ledger, runtimeDigest, runtimeManifest string) Option {
	return func(r *Recording) {
		r.Fingerprint, r.Ledger, r.RuntimeDigest, r.RuntimeManifest = fp, ledger, runtimeDigest, runtimeManifest
	}
}

// Commit sets the recorded commit and dirty flag.
func Commit(commit string, dirty bool) Option {
	return func(r *Recording) { r.Commit, r.Dirty = commit, dirty }
}

// Conditions sets the recorded run-conditions line verbatim.
func Conditions(line string) Option {
	return func(r *Recording) { r.Conditions = line }
}

// Set feeds one row's value to the writer's own input for it — the
// fingerprint field, the ledger, the runtime evidence, the commit, the
// dirty flag, the conditions line — so the row lands where and only
// where the writer emits it: an omittable row set non-empty appears at
// the writer's position, set empty disappears. The format row is the
// writer's constant and has no input; setting it panics.
func Set(key run.RecordingKey, value string) Option {
	return func(r *Recording) {
		fp := &r.Fingerprint
		switch key.Name {
		case run.KeyCommit.Name:
			r.Commit = value
		case run.KeyDirty.Name:
			dirty, err := strconv.ParseBool(value)
			if err != nil {
				panic("recordingtest: Set(dirty, " + strconv.Quote(value) + "): " + err.Error())
			}
			r.Dirty = dirty
		case run.KeyToolchain.Name:
			fp.Guards.Toolchain = value
		case run.KeyMachine.Name:
			fp.Guards.Machine = value
		case run.KeyBuildConfig.Name:
			fp.Guards.BuildConfig = value
		case run.KeyRuntimeConfig.Name:
			fp.Guards.RuntimeConfig = value
		case run.KeyRunConditions.Name:
			r.Conditions = value
		case run.KeyRuntime.Name:
			r.RuntimeDigest = value
		case run.KeyRuntimeInputs.Name:
			r.RuntimeManifest = value
		case run.KeyPurity.Name:
			fp.PurityAssertion = value
		case run.KeyVouches.Name:
			fp.DynamicStateVouches = value
		case run.KeyDynamicState.Name:
			fp.DynamicStateStrategy = value
		case run.KeySingleSubjectDischarges.Name:
			fp.SingleSubjectDischarges = value
		case run.KeyPackageProcessDischarges.Name:
			fp.PackageProcessDischarges = value
		case run.KeyClosure.Name:
			fp.MaximalClosure = value
		case run.KeyClosureStrategy.Name:
			fp.ClosureStrategy = value
		case run.KeyTestVariants.Name:
			fp.TestVariantClosure = value
		case run.KeyTestVariantLedger.Name:
			r.Ledger = value
		default:
			panic("recordingtest: Set(" + key.Name + "): the writer has no input for that row")
		}
	}
}

// Omit removes one emitted row after composition: a recording predating
// the line, or a historical file missing a mandatory field. Omitting a
// row the writer did not emit panics — a fixture claiming to predate a
// line the writer never wrote would otherwise be a silent no-op.
func Omit(key run.RecordingKey) Option {
	return func(r *Recording) { r.omitted = append(r.omitted, key) }
}

// Config composes the recording's configuration lines exactly as the
// run verb writes them from the options' inputs, then removes the
// omitted rows.
func Config(opts ...Option) []benchfmt.Config {
	r := Defaults()
	for _, opt := range opts {
		opt(&r)
	}
	cfgs := append(run.ProvenanceConfig(r.Commit, r.Dirty, r.Fingerprint.Guards, run.Conditions{}),
		run.FingerprintConfigs(r.Fingerprint, r.Ledger, r.RuntimeDigest, r.RuntimeManifest)...)
	// The conditions line is the writer's from a Conditions value; the
	// fixture's is the caller's verbatim text in the same position.
	for i := range cfgs {
		if cfgs[i].Key == run.KeyRunConditions.Name {
			cfgs[i] = run.KeyRunConditions.Config(r.Conditions)
		}
	}
	for _, key := range r.omitted {
		cfgs = without(cfgs, key)
	}
	return cfgs
}

func without(cfgs []benchfmt.Config, key run.RecordingKey) []benchfmt.Config {
	out := cfgs[:0:0]
	found := false
	for _, c := range cfgs {
		if c.Key == key.Name {
			found = true
			continue
		}
		out = append(out, c)
	}
	if !found {
		panic("recordingtest: Omit(" + key.Name + "): the writer emitted no such row")
	}
	return out
}

// Results is one recording of bench with one row per value (sec/op,
// one iteration each), every row carrying Config(opts...).
func Results(bench string, values []float64, opts ...Option) []*benchfmt.Result {
	recs := make([]*benchfmt.Result, 0, len(values))
	for _, v := range values {
		recs = append(recs, &benchfmt.Result{
			Name:   benchfmt.Name(bench),
			Iters:  1,
			Values: []benchfmt.Value{{Value: v, Unit: "sec/op"}},
			Config: Config(opts...),
		})
	}
	return recs
}

// Text renders Config(opts...) as the recording file's `key: value`
// lines, one per row, for fixtures written as raw bytes.
func Text(opts ...Option) string {
	var b strings.Builder
	for _, c := range Config(opts...) {
		b.WriteString(c.Key)
		b.WriteString(": ")
		b.Write(c.Value)
		b.WriteByte('\n')
	}
	return b.String()
}
