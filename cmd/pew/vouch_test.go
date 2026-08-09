package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	runpkg "github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

// The colon pair form parses, canonicalizes, and refuses - a bare
// package is unrepresentable, control characters and non-identifier
// variables refuse loudly (spec §12 --vouch).
func TestParseDynamicStateVouches(t *testing.T) {
	got, err := parseDynamicStateVouches([]string{"b.example/dep:Var", "a.example/dep:Var", "b.example/dep:Var"})
	if err != nil || len(got) != 2 || got[0] != "a.example/dep.Var" || got[1] != "b.example/dep.Var" {
		t.Fatalf("parse = %v, %v", got, err)
	}
	for _, bad := range []string{"a.example/dep", "", ":Var", "a.example/dep:", "a.example/dep:not-ident", "a.example/dep:9x", "a.example/dep:V.S", "a.example/dep :Var", "a.example/dep\x01x:Var"} {
		if _, err := parseDynamicStateVouches([]string{bad}); err == nil {
			t.Fatalf("malformed vouch %q accepted", bad)
		}
	}
}

// The recorded pew-vouches line crosses both directions: the composer
// emits it and the fingerprint parser restores it - audit riding the
// recording (spec §5).
func TestVouchesConfigRoundTrip(t *testing.T) {
	cfg := []benchfmt.Config{
		{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
		{Key: "toolchain", Value: []byte("go"), File: true},
		{Key: "machine", Value: []byte("m"), File: true},
		{Key: "buildconfig", Value: []byte("b"), File: true},
		{Key: "runtimeconfig", Value: []byte("r"), File: true},
		{Key: "pew-closure", Value: []byte("h"), File: true},
		runpkg.GofreshVouchesConfig("a.example/dep.Var"),
	}
	fp, _, _, ok := fingerprintFromConfig(cfg)
	if !ok || fp.DynamicStateVouches != "a.example/dep.Var" {
		t.Fatalf("round trip = %+v ok=%v", fp, ok)
	}
}

// The --vouch set reaches every engine the commands build: a real
// pinned-dependency culprit (protobuf's global registries) downgrades
// an importing benchmark's subject, and the vouched engine lifts
// exactly it, the discharge riding the fingerprint that run records
// (spec §12, §5 pew-vouches).
func TestVouchedEngineDischargesPinnedCulprit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds gofresh views over the protobuf graph")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	cached, err := filepath.Glob(filepath.Join(strings.TrimSpace(string(out)), "google.golang.org", "protobuf@v*"))
	if err != nil || len(cached) == 0 {
		t.Skipf("google.golang.org/protobuf absent from the module cache: %v %v", cached, err)
	}
	sort.Strings(cached)
	version := cached[len(cached)-1][strings.LastIndex(cached[len(cached)-1], "@")+1:]
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/vouchbench\n\ngo 1.26\n\nrequire google.golang.org/protobuf " + version + "\n",
		"reg.go": `package vouchbench

import (
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func Count() int {
	n := 0
	protoregistry.GlobalFiles.RangeFiles(func(protoreflect.FileDescriptor) bool {
		n++
		return true
	})
	return n
}
`,
		"reg_test.go": `package vouchbench

import "testing"

func BenchmarkCount(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Count()
	}
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	ctx := context.Background()
	subject := gofresh.Subject{Package: "example.com/vouchbench", Symbol: "BenchmarkCount"}
	capture := func(vouches ...string) gofresh.Fingerprint {
		t.Helper()
		dynamicStateVouches = vouches
		defer func() { dynamicStateVouches = nil }()
		e, err := buildEngine(dir, os.Environ(), "")
		if err != nil {
			t.Fatal(err)
		}
		view, err := e.NewView(ctx, []gofresh.Subject{subject}, dir)
		if err != nil {
			t.Fatal(err)
		}
		fp, err := view.Capture(ctx, subject)
		if err != nil {
			t.Fatal(err)
		}
		return fp
	}
	plainEngine, err := buildEngine(dir, os.Environ(), "")
	if err != nil {
		t.Fatal(err)
	}
	plainView, err := plainEngine.NewView(ctx, []gofresh.Subject{subject}, dir)
	if err != nil {
		t.Fatal(err)
	}
	plainFP, err := plainView.Capture(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := plainView.Check(ctx, plainFP, subject)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(verdict.Reason, "shares mutated dynamic state") {
		t.Fatalf("plain verdict = %+v, want the downgrade", verdict)
	}
	m := regexp.MustCompile(`([^\s:]+): ([^\s:]+)\.([\p{L}_][\p{L}\p{Nd}_]*) `).FindStringSubmatch(verdict.Reason + " ")
	if m == nil || m[1] != m[2] {
		t.Fatalf("no culprit parsed from %q", verdict.Reason)
	}
	culprit := m[1] + "." + m[3]

	fp := capture(culprit)
	if fp.DynamicStateVouches != culprit {
		t.Fatalf("vouched fingerprint discharge = %q, want %q", fp.DynamicStateVouches, culprit)
	}
	if plainFP.DynamicStateVouches != "" {
		t.Fatalf("plain fingerprint carries a discharge: %q", plainFP.DynamicStateVouches)
	}

	// The vouched VERDICT no longer names the culprit - the discharge is
	// load-bearing, not merely recorded.
	dynamicStateVouches = []string{culprit}
	vouchedEngine, err := buildEngine(dir, os.Environ(), "")
	dynamicStateVouches = nil
	if err != nil {
		t.Fatal(err)
	}
	vouchedView, err := vouchedEngine.NewView(ctx, []gofresh.Subject{subject}, dir)
	if err != nil {
		t.Fatal(err)
	}
	vouchedFP, err := vouchedView.Capture(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	vouchedVerdict, err := vouchedView.Check(ctx, vouchedFP, subject)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(vouchedVerdict.Reason, culprit) {
		t.Fatalf("vouched verdict still names the culprit: %+v", vouchedVerdict)
	}
}

// The flag-to-engine seam: resolveVouches parses the collected flag
// values into the process-wide set, refuses malformed entries, and
// resets the set when the flags are absent.
func TestResolveVouchesSeam(t *testing.T) {
	t.Cleanup(func() { rawVouches, dynamicStateVouches = nil, nil })
	rawVouches = []string{"a.example/dep:Var"}
	if err := resolveVouches(); err != nil {
		t.Fatal(err)
	}
	if len(dynamicStateVouches) != 1 || dynamicStateVouches[0] != "a.example/dep.Var" {
		t.Fatalf("resolved set = %v", dynamicStateVouches)
	}
	rawVouches = []string{"garbage"}
	if err := resolveVouches(); err == nil {
		t.Fatal("malformed vouch resolved silently")
	}
	rawVouches = nil
	if err := resolveVouches(); err != nil {
		t.Fatal(err)
	}
	if dynamicStateVouches != nil {
		t.Fatalf("empty flags left a stale set: %v", dynamicStateVouches)
	}
}
