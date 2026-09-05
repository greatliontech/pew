package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// Every fallback leg of the completed-observation conjunction records
// the canonical incomplete disposition with its honest reason — never a
// hard failure, never a served absence (spec §7.8).
func TestIngestObservationFallsBackIncomplete(t *testing.T) {
	dir := t.TempDir()
	env := os.Environ()

	frameless := CaptureObservationFrame(context.Background(), dir, "../elsewhere")
	if frameless.Reason() == "" {
		t.Fatal("escaping package produced a usable frame")
	}
	// The capture exists and carries the header, so the fold reaches the
	// frame's own refusal - in the facade's canonical order a missing
	// capture folds first.
	present := filepath.Join(dir, "present.testlog")
	if err := os.WriteFile(present, []byte("# test log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := IngestObservation(context.Background(), frameless, present, "package-test-binary:probe", env)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable || !strings.Contains(state.Reason, "observation bracket capture failed") {
		t.Fatalf("frameless state = %+v, want incomplete with the frame's reason", state)
	}

	frame := CaptureObservationFrame(context.Background(), dir, ".")
	if frame.Reason() != "" {
		t.Fatalf("frame capture failed: %s", frame.Reason())
	}
	state, err = IngestObservation(context.Background(), frame, filepath.Join(dir, "absent"), "package-test-binary:probe", env)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable || !strings.Contains(state.Reason, "produced no runtime-input log") {
		t.Fatalf("missing-capture state = %+v, want the facade's missing-log reason", state)
	}

	outside := t.TempDir()
	headerless := filepath.Join(outside, "headerless")
	if err := os.WriteFile(headerless, []byte("not a testlog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = IngestObservation(context.Background(), frame, headerless, "package-test-binary:probe", env)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable || !strings.Contains(state.Reason, "never opened") {
		t.Fatalf("headerless-capture state = %+v, want incomplete never-opened reason", state)
	}

	// A capture with the header but unrecognized content stays fail-closed
	// inside the ingested manifest itself: the observation completes
	// carrying its own unverifiable entries.
	garbled := filepath.Join(outside, "garbled")
	if err := os.WriteFile(garbled, []byte("# test log\nnot an op line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = IngestObservation(context.Background(), frame, garbled, "package-test-binary:probe", env)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable || !strings.Contains(state.Reason, "unrecognized testlog op") {
		t.Fatalf("garbled-capture state = %+v, want the ingest's own unverifiable entry", state)
	}
}

// A package directory outside the module tree cannot be bracketed: the
// frame carries the refusal instead of a bracket over the wrong root.
func TestCaptureObservationFrameRefusesEscapingPackage(t *testing.T) {
	frame := CaptureObservationFrame(context.Background(), t.TempDir(), "../elsewhere")
	if frame.Reason() == "" {
		t.Fatal("escaping package directory produced a frame, want a refusal reason")
	}
}

// The guard-covered classification roots are load-bearing: a toolchain
// read classifies guard-covered under WithToolchainRoot and would seal
// the observation without it — pinning the option wiring the ingest
// carries (spec §7.8).
func TestIngestObservationClassifiesToolchainReads(t *testing.T) {
	dir := t.TempDir()
	frame := CaptureObservationFrame(context.Background(), dir, ".")
	if frame.Reason() != "" {
		t.Fatalf("frame capture failed: %s", frame.Reason())
	}
	// The root must exist (guard-root resolution fails closed) and must
	// sit outside every other classification: TMPDIR moves the ephemeral
	// temp root away so it cannot swallow the toolchain read.
	toolchain := t.TempDir()
	if err := os.WriteFile(filepath.Join(toolchain, "VERSION"), []byte("go0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(capture, []byte("# test log\nopen "+toolchain+"/VERSION\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	// The roots are facts of the ingested environment: the toolchain
	// reports GOROOT for the environment the process ran under.
	t.Setenv("GOROOT", toolchain)
	state, err := IngestObservation(context.Background(), frame, capture, "package-test-binary:probe", os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if state.Unverifiable {
		t.Fatalf("toolchain read sealed the observation despite the root classification: %+v", state)
	}
	os.Unsetenv("GOROOT")
	state, err = IngestObservation(context.Background(), frame, capture, "package-test-binary:probe", os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable {
		t.Fatalf("out-of-root read completed without the classification, want sealed: %+v", state)
	}
}

// A declared scratch pattern becomes a gofresh scratch namespace over
// the package directory: an endpoint-absent read matching it records
// nothing, while the same read without the declaration records its
// identity — pinning the option wiring the ingest carries (spec §7.8).
func TestIngestObservationAppliesScratchNamespaces(t *testing.T) {
	dir := t.TempDir()
	frame := CaptureObservationFrame(context.Background(), dir, ".")
	if frame.Reason() != "" {
		t.Fatalf("frame capture failed: %s", frame.Reason())
	}
	capture := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(capture, []byte("# test log\nopen bench-xyz/out.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	env := os.Environ()

	state, err := IngestObservation(context.Background(), frame, capture, "package-test-binary:probe", env, "bench-*")
	if err != nil {
		t.Fatal(err)
	}
	if state.Unverifiable {
		t.Fatalf("scratch-declared ingest sealed: %+v", state)
	}
	paths, err := runtimeinput.Paths(state.Manifest, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("declared scratch read recorded: %v", paths)
	}

	state, err = IngestObservation(context.Background(), frame, capture, "package-test-binary:probe", env)
	if err != nil {
		t.Fatal(err)
	}
	paths, err = runtimeinput.Paths(state.Manifest, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("undeclared scratch read not recorded: %v", paths)
	}
}

// The producing environment's temp root rides the ingest as an
// ephemeral root: temp-tree creation machinery stats the root to mint
// per-run subtrees, and under the declaration that stat records
// nothing - without it the root records an existence-bound identity
// that churns evidence comparison across machines (spec §7.8).
func TestIngestObservationDeclaresEphemeralTempRoot(t *testing.T) {
	dir := t.TempDir()
	frame := CaptureObservationFrame(context.Background(), dir, ".")
	if frame.Reason() != "" {
		t.Fatalf("frame capture failed: %s", frame.Reason())
	}
	tmproot := t.TempDir()
	t.Setenv("TMPDIR", tmproot)
	capture := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(capture, []byte("# test log\nstat "+tmproot+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	state, err := IngestObservation(context.Background(), frame, capture, "package-test-binary:probe", env)
	if err != nil {
		t.Fatal(err)
	}
	if state.Unverifiable {
		t.Fatalf("temp-root stat sealed under the declaration: %+v", state)
	}
	paths, err := runtimeinput.Paths(state.Manifest, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("temp-root stat recorded despite the ephemeral declaration: %v", paths)
	}
}

// The module-cache and build-cache roots are facts of the ingested
// environment exactly as the toolchain root is: a read under the
// environment's root classifies guard-covered and the observation
// stays verifiable; under an environment naming no such root, the
// same read seals it (spec §7.8).
func TestIngestObservationClassifiesCacheReads(t *testing.T) {
	for name, tc := range map[string]struct {
		declare func(root string) string // the environment entry naming the root
		read    func(root string) string
	}{
		"module cache": {
			declare: func(root string) string { return "GOMODCACHE=" + root },
			read:    func(root string) string { return filepath.Join(root, "example.com", "dep@v1.0.0", "dep.go") },
		},
		"build cache": {
			declare: func(root string) string { return "GOCACHE=" + root },
			read:    func(root string) string { return filepath.Join(root, "aa", "object") },
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			frame := CaptureObservationFrame(context.Background(), dir, ".")
			if frame.Reason() != "" {
				t.Fatalf("frame capture failed: %s", frame.Reason())
			}
			root := t.TempDir()
			target := tc.read(root)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("cached"), 0o600); err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(t.TempDir(), "log")
			if err := os.WriteFile(capture, []byte("# test log\nopen "+target+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", t.TempDir())
			key, value, _ := strings.Cut(tc.declare(root), "=")
			t.Setenv(key, value)
			state, err := IngestObservation(context.Background(), frame, capture, "package-test-binary:probe", os.Environ())
			if err != nil {
				t.Fatal(err)
			}
			if state.Unverifiable {
				t.Fatalf("declared %s read sealed: %+v", name, state)
			}
			os.Unsetenv(key)
			state, err = IngestObservation(context.Background(), frame, capture, "package-test-binary:probe", os.Environ())
			if err != nil {
				t.Fatal(err)
			}
			if !state.Unverifiable {
				t.Fatalf("undeclared %s read completed verifiable: %+v", name, state)
			}
		})
	}
}

// The ingest carries the environment the process ran under, PWD naming
// the package directory — not the module root: a non-root package's
// observation completes and its cwd-relative read resolves under the
// package (spec §7.8; the facade refuses any other PWD as incomplete,
// which would silently blank the evidence of every non-root package).
func TestIngestObservationRunsUnderThePackageDirectory(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "sub")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "fixture.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	frame := CaptureObservationFrame(context.Background(), root, "sub")
	if frame.Reason() != "" {
		t.Fatalf("frame capture failed: %s", frame.Reason())
	}
	capture := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(capture, []byte("# test log\nopen fixture.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	state, err := IngestObservation(context.Background(), frame, capture, "package-test-binary:probe", os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if state.Unverifiable {
		t.Fatalf("a non-root package's observation did not complete: %+v", state)
	}
	paths, err := runtimeinput.Paths(state.Manifest, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join(pkg, "fixture.txt") {
		t.Fatalf("the cwd-relative read resolved as %v, want %s", paths, filepath.Join(pkg, "fixture.txt"))
	}
	// A declared scratch pattern names a namespace over the PACKAGE
	// directory: a scratch read in a non-root package records nothing
	// under the declaration (a namespace over the root would be inert
	// against the package's bracket, and every run would re-stale on
	// the minted name).
	scratch := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(scratch, []byte("# test log\nopen bench-xyz/out.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = IngestObservation(context.Background(), frame, scratch, "package-test-binary:probe", os.Environ(), "bench-*")
	if err != nil {
		t.Fatal(err)
	}
	if state.Unverifiable {
		t.Fatalf("scratch-declared non-root ingest sealed: %+v", state)
	}
	if paths, err = runtimeinput.Paths(state.Manifest, root); err != nil || len(paths) != 0 {
		t.Fatalf("declared scratch read in a non-root package recorded: %v, %v", paths, err)
	}
}
