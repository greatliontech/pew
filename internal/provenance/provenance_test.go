package provenance

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestCapture exercises the full capture path in pew's own repo and cross-checks
// the commit against the git binary (go-git vs git). It enforces INV-4: every
// guard input is populated.
func TestCapture(t *testing.T) {
	p, err := Capture(".")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skipf("git rev-parse unavailable: %v", err)
	}
	if want := strings.TrimSpace(string(out)); p.Commit != want {
		t.Errorf("commit: go-git gave %q, git gave %q", p.Commit, want)
	}
	if !strings.Contains(p.Toolchain, "go1") {
		t.Errorf("toolchain looks wrong: %q", p.Toolchain)
	}
	if len(p.Machine) != 32 {
		t.Errorf("machine fingerprint: got %q (len %d), want len 32", p.Machine, len(p.Machine))
	}
	if len(p.BuildConfig) != 32 {
		t.Errorf("buildconfig: got %q (len %d), want len 32", p.BuildConfig, len(p.BuildConfig))
	}
}

// TestRuntimeConfigCapturesRuntimeEnv pins that a change to any Go runtime-config
// variable moves the runtimeconfig digest (§7 guard 6) — otherwise e.g. GOGC=off
// could reshape allocation behavior while the recording still reads valid.
func TestRuntimeConfigCapturesRuntimeEnv(t *testing.T) {
	base := runtimeConfig()
	for _, k := range runtimeConfigEnvKeys {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, "pew-runtimeconfig-test-"+k)
			if got := runtimeConfig(); got == base {
				t.Fatalf("changing %s did not move the runtimeconfig digest", k)
			}
		})
	}
}

// TestConfigKeysAndOrder enforces the §5 in-band key set and order, and dirty
// formatting (part of INV-4).
func TestConfigKeysAndOrder(t *testing.T) {
	p := Provenance{Commit: "abc", Toolchain: "go1.26.4", Machine: "m123", BuildConfig: "b456", RuntimeConfig: "r789", Dirty: true}
	cfg := p.Config()
	want := [][2]string{
		{"commit", "abc"}, {"toolchain", "go1.26.4"}, {"machine", "m123"},
		{"buildconfig", "b456"}, {"runtimeconfig", "r789"}, {"dirty", "true"},
	}
	if len(cfg) != len(want) {
		t.Fatalf("got %d config lines, want %d", len(cfg), len(want))
	}
	for i, w := range want {
		if cfg[i].Key != w[0] || string(cfg[i].Value) != w[1] {
			t.Errorf("config[%d]: got %s=%s, want %s=%s", i, cfg[i].Key, cfg[i].Value, w[0], w[1])
		}
	}
}

func TestFingerprintStableAndSensitive(t *testing.T) {
	a := MachineFacts{
		CPUModel: "Test CPU", PhysicalCores: 8, LogicalCores: 16,
		TotalRAMBytes: 1 << 34, OS: "linux", KernelVersion: "7.0", GOARCH: "amd64",
	}
	if f1, f2 := a.Fingerprint(), a.Fingerprint(); f1 != f2 {
		t.Errorf("fingerprint not deterministic: %q vs %q", f1, f2)
	}
	if len(a.Fingerprint()) != 32 {
		t.Errorf("fingerprint len: got %d", len(a.Fingerprint()))
	}
	for _, mut := range []func(*MachineFacts){
		func(f *MachineFacts) { f.CPUModel = "Other" },
		func(f *MachineFacts) { f.PhysicalCores = 4 },
		func(f *MachineFacts) { f.LogicalCores = 8 },
		func(f *MachineFacts) { f.TotalRAMBytes = 1 << 33 },
		func(f *MachineFacts) { f.KernelVersion = "7.1" },
		func(f *MachineFacts) { f.GOARCH = "arm64" },
	} {
		b := a
		mut(&b)
		if a.Fingerprint() == b.Fingerprint() {
			t.Errorf("fingerprint insensitive to a stable field change: %+v", b)
		}
	}
}

// TestMachineFactsExcludesTransient structurally enforces §8: the fingerprint
// has no field for a transient run condition, so one cannot enter it.
func TestMachineFactsExcludesTransient(t *testing.T) {
	forbidden := []string{"governor", "turbo", "boost", "thermal", "scaling", "load", "freq"}
	for _, field := range reflect.VisibleFields(reflect.TypeFor[MachineFacts]()) {
		name := strings.ToLower(field.Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Errorf("MachineFacts.%s looks like a transient run condition (§8 forbids it in the fingerprint)", field.Name)
			}
		}
	}
}

func TestGatherFacts(t *testing.T) {
	f, err := gatherFacts()
	if err != nil {
		t.Fatalf("gatherFacts: %v", err)
	}
	if f.LogicalCores < 1 {
		t.Errorf("logical cores: %d", f.LogicalCores)
	}
	if f.OS == "" || f.GOARCH == "" {
		t.Errorf("OS/GOARCH empty: %+v", f)
	}
	if runtime.GOOS == "linux" {
		if f.CPUModel == "" {
			t.Error("linux: empty CPU model")
		}
		if f.TotalRAMBytes == 0 {
			t.Error("linux: zero total RAM")
		}
		if f.PhysicalCores < 1 {
			t.Errorf("linux: physical cores %d", f.PhysicalCores)
		}
	}
}

// TestConfigSerializable: provenance config must have File==true, or
// benchfmt.Writer silently omits it (it treats File==false as internal config).
func TestConfigSerializable(t *testing.T) {
	for _, c := range (Provenance{Commit: "x"}).Config() {
		if !c.File {
			t.Errorf("config %q has File=false; benchfmt.Writer would omit it", c.Key)
		}
	}
}

func TestBuildConfigStable(t *testing.T) {
	a, err := buildConfig("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("buildconfig not stable across calls: %q vs %q", a, b)
	}
	if len(a) != 32 {
		t.Errorf("buildconfig len: got %d", len(a))
	}
}

// TestBuildConfigCapturesBuildAffectingEnv pins that a change to any hashed
// build-affecting input moves the digest (§7 guard 5) — otherwise a code-generation
// change could report valid — and that host GOOS/GOARCH, which ride the machine
// guard (§8), do not move it.
func TestBuildConfigCapturesBuildAffectingEnv(t *testing.T) {
	base, err := buildConfig("")
	if err != nil {
		t.Fatal(err)
	}
	// Each build-affecting key must move the digest.
	for _, k := range []string{
		"CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_FFLAGS", "CGO_LDFLAGS",
		"CC", "CXX", "PKG_CONFIG_PATH", "PKG_CONFIG_LIBDIR", "PKG_CONFIG_SYSROOT_DIR",
	} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, "pew-buildconfig-test-"+k)
			got, err := buildConfig("")
			if err != nil {
				t.Fatal(err)
			}
			if got == base {
				t.Fatalf("changing %s did not move the buildconfig digest", k)
			}
		})
	}
}

// TestBuildConfigExcludesHostArch pins that host GOOS/GOARCH are NOT part of the
// buildconfig digest — they ride the machine guard (§8), so a feature-level change
// moves buildconfig while a host-arch change moves machine, neither dropped. Asserted
// structurally (not behaviorally: setting GOARCH cross-compiles, which indirectly
// disables cgo and would move the digest for an unrelated reason).
func TestBuildConfigExcludesHostArch(t *testing.T) {
	all := append(append([]string{}, buildConfigGoEnvKeys...), buildConfigOSEnvKeys...)
	in := func(want string) bool {
		for _, k := range all {
			if k == want {
				return true
			}
		}
		return false
	}
	for _, k := range []string{"GOOS", "GOARCH"} {
		if in(k) {
			t.Errorf("%s is in the buildconfig digest; it must ride the machine guard (§8)", k)
		}
	}
	// The codegen feature level and cgo toolchain must be present.
	for _, k := range []string{"GOAMD64", "GOEXPERIMENT", "CGO_ENABLED", "CGO_CFLAGS", "CC"} {
		if !in(k) {
			t.Errorf("%s missing from the buildconfig digest", k)
		}
	}
}

// TestGitStateDirty validates commit + dirty semantics against a real repo built
// with the git binary: clean after commit, dirty with an untracked file.
// TestCaptureCacheMemoizes pins the per-invocation capture cache: a second call
// for the same module dir returns the first result even after the working tree
// changes underneath it — proving the second call reused the memo rather than
// re-running the git/toolchain work. A fresh direct Capture is checked to observe
// the changed state, so the cache's returned (stale) value is a real memo, not a
// coincidence.
func TestCaptureCacheMemoizes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "init")

	pc := NewCache()
	first, err := pc.Capture(dir)
	if err != nil {
		t.Fatalf("Capture (first): %v", err)
	}
	if first.Dirty {
		t.Fatal("freshly-committed repo captured dirty")
	}

	// Change the working tree: a fresh direct capture now sees it dirty.
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh, err := Capture(dir)
	if err != nil {
		t.Fatalf("direct Capture after change: %v", err)
	}
	if !fresh.Dirty {
		t.Fatal("working-tree change not observed by a fresh Capture; test cannot distinguish memo")
	}

	// The cache must return the original (clean) value — proving it did not recompute.
	second, err := pc.Capture(dir)
	if err != nil {
		t.Fatalf("Capture (second): %v", err)
	}
	if second != first {
		t.Errorf("cache recomputed: second=%+v != first=%+v", second, first)
	}
	if second.Dirty {
		t.Error("cache returned a recomputed dirty value; want the memoized clean one")
	}
}

func TestGitStateDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "init")

	commit, dirty, err := gitState(dir)
	if err != nil {
		t.Fatalf("gitState: %v", err)
	}
	if len(commit) != 40 {
		t.Errorf("commit sha: got %q (len %d), want 40 hex", commit, len(commit))
	}
	if dirty {
		t.Error("freshly-committed repo reported dirty")
	}

	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, dirty, err = gitState(dir); err != nil {
		t.Fatalf("gitState after change: %v", err)
	} else if !dirty {
		t.Error("repo with an untracked file reported clean")
	}
}
