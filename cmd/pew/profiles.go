package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

// profileStatus keeps independent claims independent in both renderers.
type profileStatus struct {
	Package   string            `json:"package,omitempty"`
	Benchmark string            `json:"benchmark,omitempty"`
	Kind      string            `json:"kind,omitempty"`
	Integrity string            `json:"integrity"`
	Freshness string            `json:"freshness"`
	Relation  string            `json:"relation"`
	Outcome   string            `json:"outcome"`
	Reason    string            `json:"reason,omitempty"`
	Empty     *bool             `json:"empty,omitempty"`
	Metrics   []profiles.Metric `json:"metrics,omitempty"`
	Error     string            `json:"error,omitempty"`
}

type profileObjectReader interface {
	ReadObject(string, int64) ([]byte, error)
}

func inspectProfile(ctx context.Context, st profileObjectReader, c profiles.Capture, rows []*benchfmt.Result, view *gofresh.View) (profileStatus, error) {
	r := profileStatus{Package: c.Package, Benchmark: c.Benchmark, Kind: c.Kind, Integrity: "unavailable", Freshness: "unavailable", Relation: "unverified", Outcome: "identity-only", Reason: profiles.UnsupportedReason}
	fault := func(err error) (profileStatus, error) { r.Error = err.Error(); return r, err }
	b, err := st.ReadObject(c.SHA256, c.Size)
	if err != nil {
		if errors.Is(err, store.ErrObjectIntegrity) {
			r.Integrity = "invalid"
		}
		return fault(err)
	}
	r.Metrics, err = profiles.Analyze(b, c.Kind, c.Sources)
	if err != nil {
		r.Integrity = "invalid"
		return fault(err)
	}
	empty := profiles.Empty(r.Metrics)
	r.Integrity, r.Empty = "verified", &empty
	fp, err := run.RecordedFingerprint(rows[0].Config)
	if err != nil {
		return fault(err)
	}
	if err := profiles.CheckedRelation(ctx, c, rows, fp, view); err != nil {
		switch {
		case errors.Is(err, profiles.ErrRelationMismatch):
			r.Relation = "mismatch"
			r.Reason += "; " + err.Error()
		case errors.Is(err, profiles.ErrRelationUnproven):
			r.Reason += "; " + err.Error()
		default:
			return fault(err)
		}
	}
	if view == nil {
		return r, nil
	}
	subject := gofresh.Subject{Package: c.Package, Symbol: c.Benchmark}
	child, err := view.Sibling([]gofresh.Subject{subject})
	if err != nil {
		return fault(err)
	}
	vs, err := child.CheckBatch(ctx, map[gofresh.Subject]gofresh.Fingerprint{subject: c.Fingerprint})
	if err != nil {
		return fault(err)
	}
	if err := child.Validate(ctx); err != nil {
		return fault(err)
	}
	v := vs[subject]
	r.Freshness = string(v.Status)
	if v.Reason != "" {
		r.Reason += "; " + v.Reason
	}
	return r, nil
}

func profileStatuses(ctx context.Context, st *store.Store, adm admission, view *gofresh.View, pkg, bench string) ([]profileStatus, error) {
	if len(adm.rows) == 0 {
		return nil, nil
	}
	index, present, err := profiles.FromRows(adm.rows)
	if err != nil {
		return []profileStatus{{Integrity: "invalid", Freshness: "unavailable", Relation: "unverified", Outcome: "unavailable", Error: err.Error()}}, err
	}
	if !present {
		return nil, nil
	}
	var result []profileStatus
	var failures []error
	for _, c := range index.Captures {
		if c.Package != pkg || c.Benchmark != bench {
			err := fmt.Errorf("profile: indexed subject differs from recording")
			result = append(result, profileStatus{Kind: c.Kind, Integrity: "invalid", Freshness: "unavailable", Relation: "mismatch", Outcome: "unavailable", Error: err.Error()})
			failures = append(failures, err)
			continue
		}
		r, err := inspectProfile(ctx, st, c, adm.rows, view)
		result = append(result, r)
		if err != nil {
			failures = append(failures, err)
		}
	}
	return result, errors.Join(failures...)
}

func renderProfiles(w io.Writer, rows []profileStatus, explain bool) error {
	for _, r := range rows {
		if r.Package != "" {
			if _, err := fmt.Fprintf(w, "    subject: %s.%s\n", r.Package, r.Benchmark); err != nil {
				return err
			}
		}
		empty := "unknown"
		if r.Empty != nil {
			empty = fmt.Sprint(*r.Empty)
		}
		if _, err := fmt.Fprintf(w, "    profile %s: integrity=%s freshness=%s relation=%s outcome=%s empty=%s\n", r.Kind, r.Integrity, r.Freshness, r.Relation, r.Outcome, empty); err != nil {
			return err
		}
		if r.Error != "" {
			if _, err := fmt.Fprintf(w, "      %s\n", r.Error); err != nil {
				return err
			}
		}
		if !explain {
			continue
		}
		if _, err := fmt.Fprintf(w, "      %s; flat weights partition samples, cumulative weights overlap\n", r.Reason); err != nil {
			return err
		}
		for _, m := range r.Metrics {
			if _, err := fmt.Fprintf(w, "      %s/%s total=%d\n", m.Type, m.Unit, m.Total); err != nil {
				return err
			}
			for _, a := range m.Weights {
				if _, err := fmt.Fprintf(w, "        %s flat=%d cumulative=%d %s %s\n", a.Symbol, a.Flat, a.Cumulative, a.Attribution, a.Source); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func runProfiles(ctx context.Context, w, errw io.Writer, rc runConfig, gc *gitStateCache, prep *packagePreparation, envs environments, conditions run.Conditions, parent *gofresh.View, names []string) error {
	kinds, err := profiles.Kinds(rc.profile)
	if err != nil || len(kinds) == 0 {
		return err
	}
	budget := rc.profileBenchtime
	if budget == "" {
		budget = "1s"
	}
	if err := profiles.ValidateBudget(budget); err != nil {
		return err
	}
	var failures []error
	for _, name := range names {
		for _, kind := range kinds {
			if ctx.Err() != nil {
				return interrupted("interrupted during profiles; published measurements and diagnostic captures kept")
			}
			reportPhase(ctx, "profiling "+prep.pkg.ImportPath+"."+name+" "+kind)
			if err := captureProfile(ctx, w, errw, rc, gc, prep, envs, conditions, parent, name, kind, budget); err != nil {
				if cancelledBy(ctx, err) {
					return interrupted("interrupted during profiles; published measurements and diagnostic captures kept")
				}
				failures = append(failures, fmt.Errorf("%s %s: %w", name, kind, err))
			}
		}
	}
	if len(failures) != 0 {
		return fmt.Errorf("profiles failed; measurements kept: %w", errors.Join(failures...))
	}
	return nil
}

func captureProfile(ctx context.Context, w, errw io.Writer, rc runConfig, gc *gitStateCache, prep *packagePreparation, envs environments, conditions run.Conditions, parent *gofresh.View, name, kind, budget string) error {
	p, st := prep.pkg, prep.st
	key := store.Key{PkgRel: prep.pkgRel, Bench: name, Label: rc.label}
	expected, err := st.ReadBytes(key)
	if err != nil {
		return err
	}
	rows, err := store.Parse(bytes.NewReader(expected), name)
	if err != nil {
		return err
	}
	adm := admitRecording(rows, true)
	if !adm.ok {
		return fmt.Errorf("profile: measurement is not admitted: %s", adm.class)
	}
	index, _, indexErr := profiles.FromRows(rows)
	if indexErr != nil {
		// The explicit capture replaces unusable companion evidence without
		// interpreting it or changing the admitted measurement.
		fmt.Fprintf(errw, "pew: replacing invalid profile index for %s: %v\n", name, indexErr)
		index = profiles.Index{Version: 1}
	}
	selection, err := restrictBenchmarkPattern(rc.opts.Bench, []string{name})
	if err != nil {
		return err
	}
	if !rc.all {
		for _, old := range index.Captures {
			if old.Kind != kind || old.Package != p.ImportPath || old.Benchmark != name || old.Selection != selection || old.Budget != budget {
				continue
			}
			status, err := inspectProfile(ctx, st, old, rows, parent)
			if err == nil && status.Freshness == "valid" && status.Relation != "mismatch" && status.Empty != nil && !*status.Empty {
				_, err = fmt.Fprintf(w, "%-12s %s.%s %s\n", "profile-served", p.ImportPath, name, kind)
				return err
			}
		}
	}
	return captureDiagnostic(ctx, errw, rc, gc, prep, envs, conditions, parent, name, kind, budget, selection, rows, &adm, func(gate context.Context, c profiles.Capture, blob []byte) error {
		if err := profiles.CheckedRelation(gate, c, rows, adm.fp, parent); err != nil {
			return fmt.Errorf("profile attachment refused: %w", err)
		}
		kept := index.Captures[:0]
		for _, old := range index.Captures {
			if old.Kind != kind {
				kept = append(kept, old)
			}
		}
		index.Captures = append(kept, c)
		sort.Slice(index.Captures, func(i, j int) bool { return index.Captures[i].Kind < index.Captures[j].Kind })
		encoded, err := profiles.Encode(index)
		if err != nil {
			return err
		}
		if _, err := st.PutObject(gate, blob); err != nil {
			return err
		}
		for _, r := range rows {
			r.SetConfig(run.KeyProfiles.Name, encoded)
			i, _ := r.ConfigIndex(run.KeyProfiles.Name)
			r.Config[i].File = true
		}
		if err := st.ReplaceContext(gate, key, expected, rows); err != nil {
			return err
		}
		fmt.Fprintf(errw, "pew: profile %s.%s %s identity-only; relation unverified: %s\n", p.ImportPath, name, kind, profiles.UnsupportedReason)
		_, err = fmt.Fprintf(w, "%-12s %s.%s %s\n", "profiled", p.ImportPath, name, kind)
		return err
	})
}

// captureDiagnostic owns one independent execution. The publication callback
// receives only validated evidence, under the same bounded finalization context.
// A/B has no persisted timed fingerprint; only recording attachment supplies adm.
func captureDiagnostic(ctx context.Context, errw io.Writer, rc runConfig, gc *gitStateCache, prep *packagePreparation, envs environments, conditions run.Conditions, parent *gofresh.View, name, kind, budget, selection string, rows []*benchfmt.Result, adm *admission, publish func(context.Context, profiles.Capture, []byte) error) error {
	p := prep.pkg
	subject := gofresh.Subject{Package: p.ImportPath, Symbol: name}
	view, err := parent.Sibling([]gofresh.Subject{subject})
	if err != nil {
		return err
	}
	if err := validateProfileSnapshot(ctx, parent, subject); err != nil {
		return err
	}
	if err := sweepScratchLeftovers(errw, p.Dir, prep.scratch); err != nil {
		return err
	}
	start, err := gc.snapshot(p.Module.Dir)
	if err != nil {
		return err
	}
	if adm != nil {
		if err := profileInputRelation(ctx, adm.fp, p.Module.Dir, p.Dir, envs.measured()); err != nil {
			return err
		}
	}
	sources, err := profileSources(view.SourceFiles(), p)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pew-profile-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	bin, output, testlog := filepath.Join(dir, "testbin"), filepath.Join(dir, "capture.pprof"), filepath.Join(dir, "testlog")
	if _, err := rc.executeGo(ctx, p.Module.Dir, "", envs.analysis, run.BuildArgs(p.ImportPath, bin)); err != nil {
		return err
	}
	binary, err := readProfileFile(bin)
	if err != nil {
		return err
	}
	if err := validateProfileSnapshot(ctx, parent, subject); err != nil {
		return err
	}
	fp, err := view.CaptureObserved(ctx, subject)
	if err != nil {
		return err
	}
	ledger, err := view.TestVariantLedger(subject)
	if err != nil {
		return err
	}
	encodedLedger, err := run.EncodeLedger(run.LedgerFromGofresh(ledger))
	if err != nil {
		return err
	}
	frame := run.CaptureObservationFrame(ctx, p.Module.Dir, prep.pkgRel)
	// Deliberately no PrepareOutcomeSupport: ordinary harness support cannot
	// cover the profiler's pre-testlog startup and post-testlog flush.
	identity := "profile:" + kind + ":" + p.ImportPath + ":" + name
	args := []string{"-test.run=^$", "-test.bench=" + selection, "-test.count=1", "-test.benchmem", "-test.benchtime=" + budget, "-test.testlogfile=" + testlog}
	flag := "-test.cpuprofile="
	if kind == "alloc" {
		flag = "-test.memprofile="
	}
	args = append(args, flag+output)
	throttle := rc.snapshotThrottle()
	execute := rc.diagnostic
	if execute == nil {
		execute = run.ExecuteDiagnostic
	}
	out, err := execute(ctx, p.Dir, rc.pin.List(), envs.measured(), bin, args, errw)
	if err != nil {
		return err
	}
	conditions.Throttled = throttle.Delta(rc.snapshotThrottle())
	if conditions.Throttled != nil && *conditions.Throttled {
		fmt.Fprintf(errw, "pew: warning: diagnostic %s.%s %s throttled\n", p.ImportPath, name, kind)
		if rc.strict {
			return fmt.Errorf("profile: throttling under --strict")
		}
	}
	// Successful exit does not guarantee the profiler started or flushed. Missing
	// and unreadable files are failures; parseable zero-weight profiles are empty.
	blob, err := readProfileFile(output)
	if err != nil {
		return fmt.Errorf("profile output unavailable (see process diagnostics): %w", err)
	}
	metrics, err := profiles.Analyze(blob, kind, sources)
	if err != nil {
		return err
	}
	if profiles.Empty(metrics) {
		return fmt.Errorf("profile: empty %s capture; no attribution established", kind)
	}
	children, err := diagnosticChildren(out, name)
	if err != nil {
		return err
	}
	bound := rc.writeGateBound
	if bound == 0 {
		bound = armWriteGateBound
	}
	gate, cancel := context.WithTimeout(context.WithoutCancel(ctx), bound)
	defer cancel()
	observation, err := run.IngestObservation(gate, frame, testlog, identity, rc.roots, envs.measured(), prep.scratch...)
	if err != nil {
		return err
	}
	fp, err = view.AttachObservation(subject, fp, observation)
	if err != nil {
		return err
	}
	if err := view.Validate(gate); err != nil {
		return err
	}
	if adm != nil {
		if err := profileInputRelation(gate, adm.fp, p.Module.Dir, p.Dir, envs.measured()); err != nil {
			return err
		}
	}
	end, err := gc.snapshot(p.Module.Dir)
	if err != nil {
		return err
	}
	if !start.Equal(end) {
		return fmt.Errorf("profile: repository state moved during diagnostic")
	}
	gotBinary, err := readProfileFile(bin)
	if err != nil {
		return fmt.Errorf("profile: reading executable after diagnostic: %w", err)
	}
	if !bytes.Equal(binary, gotBinary) {
		return fmt.Errorf("profile: executable changed during diagnostic")
	}
	for _, src := range sources {
		b, err := os.ReadFile(src.Filename)
		if err != nil || !bytes.Equal(b, src.Bytes) {
			return fmt.Errorf("profile: source snapshot changed: %s", src.Filename)
		}
	}
	reader := gotool.Reader(p.Module.Dir, envs.analysis, nil)
	flags, err := run.EffectiveGoflags(gate, reader)
	if err != nil {
		return err
	}
	pgo, err := run.PGOInput(p.Module.Dir, p.Dir, p.Name == "main", flags)
	if err != nil {
		return err
	}
	if pgo != prep.pgoInput {
		return fmt.Errorf("profile: PGO build input changed")
	}
	dirty, err := sourceInputsDirty(p.Module.Dir, start.Commit, view.SourceFiles())
	if err != nil {
		return err
	}
	c := profiles.Capture{Kind: kind, Package: p.ImportPath, Benchmark: name, Selection: selection, Children: children, Budget: budget, Protocol: profiles.Protocol, Scope: profiles.Scope, Sampling: "runtime-default", Commit: start.Commit, Dirty: start.Dirty || dirty, Conditions: conditions.String(), BinarySHA256: profiles.Digest(binary), MeasurementRevision: profiles.Revision(rows), Fingerprint: fp, Ledger: encodedLedger, SHA256: profiles.Digest(blob), Size: int64(len(blob)), Sources: sources}
	return publish(gate, c, blob)
}

func profileInputRelation(ctx context.Context, fp gofresh.Fingerprint, module, directory string, env gotool.Environment) error {
	actual, err := env.For(directory)
	if err != nil {
		return err
	}
	state, err := runtimeinput.Current(ctx, fp.RuntimeInputs, module, actual)
	if err != nil {
		return err
	}
	if state.OK && state.Digest != fp.RuntimeDigest {
		return fmt.Errorf("profile: measurement runtime inputs changed")
	}
	return nil
}

func validateProfileSnapshot(ctx context.Context, parent *gofresh.View, subject gofresh.Subject) error {
	check, err := parent.Sibling([]gofresh.Subject{subject})
	if err != nil {
		return err
	}
	return check.Validate(ctx)
}

func diagnosticChildren(out []byte, name string) ([]profiles.Child, error) {
	complete := false
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if bytes.Equal(bytes.TrimSuffix(line, []byte{'\r'}), []byte("PASS")) {
			complete = true
		}
	}
	if !complete {
		return nil, fmt.Errorf("profile: diagnostic harness completion missing")
	}
	rows, corrupt, _, err := run.Parse(out)
	if err != nil {
		return nil, err
	}
	if len(corrupt) != 0 {
		return nil, fmt.Errorf("profile: corrupt diagnostic harness output")
	}
	audit := run.AuditStream(rows, corrupt, 1, []string{name})
	if audit.PackageCause != "" || len(audit.Refused[name]) != 0 {
		return nil, fmt.Errorf("profile: incomplete diagnostic rows: %s %v", audit.PackageCause, audit.Refused[name])
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("profile: diagnostic subject produced no rows")
	}
	var children []profiles.Child
	for _, r := range rows {
		if run.BenchName(string(r.Name)) != name || r.Iters <= 0 {
			return nil, fmt.Errorf("profile: unexpected diagnostic subject")
		}
		children = append(children, profiles.Child{Name: string(r.Name), Iterations: r.Iters})
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name < children[j].Name })
	return children, nil
}

func readProfileFile(path string) ([]byte, error) {
	i, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !i.Mode().IsRegular() {
		return nil, fmt.Errorf("profile: output is not a regular file: %s", path)
	}
	return os.ReadFile(path)
}

func profileSources(files []string, p pkgMeta) ([]profiles.Source, error) {
	var sources []profiles.Source
	for _, file := range files {
		if filepath.Ext(file) != ".go" {
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		if len(b) == 0 {
			continue
		}
		s := profiles.Source{Filename: file, Bytes: b}
		if rel, err := filepath.Rel(p.Module.Dir, file); err == nil && filepath.IsLocal(rel) {
			s.ModuleRelative = filepath.ToSlash(rel)
		}
		// Only the listed package's ordinary source has a known import identity
		// here. Other SourceFiles remain exact snapshots, not a guessed debug map.
		if filepath.Dir(file) == p.Dir {
			s.Package = p.ImportPath
			for _, external := range p.XTestGoFiles {
				if filepath.Base(file) == external {
					s.Package += "_test"
					break
				}
			}
		}
		sources = append(sources, s)
	}
	sort.Slice(sources, func(i, j int) bool { return strings.Compare(sources[i].Filename, sources[j].Filename) < 0 })
	return sources, nil
}
