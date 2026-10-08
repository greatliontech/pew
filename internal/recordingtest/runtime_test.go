package recordingtest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

func fixtureFingerprint(t *testing.T, opts ...Option) gofresh.Fingerprint {
	t.Helper()
	row := &benchfmt.Result{Config: Config(opts...)}
	fp, err := run.DecodeFingerprint(row.GetConfig(run.KeyFingerprint.Name))
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestRuntimeEvidenceOptionsUseTheFingerprint(t *testing.T) {
	supplied := Defaults().Fingerprint
	supplied.RuntimeDigest, supplied.RuntimeInputs = "captured-digest", "captured-manifest"
	supplied.ObservationAssertion = "capture-attribution"
	for _, tc := range []struct {
		name             string
		opts             []Option
		digest, manifest string
	}{
		{"supplied overrides", []Option{Measured(supplied, "ledger", "attached-digest", "attached-manifest")}, "attached-digest", "attached-manifest"},
		{"empty digest overrides", []Option{Measured(supplied, "ledger", "", "attached-manifest")}, "", "attached-manifest"},
		{"empty manifest overrides", []Option{Measured(supplied, "ledger", "attached-digest", "")}, "attached-digest", ""},
		{"both empty override", []Option{Measured(supplied, "ledger", "", "")}, "", ""},
		{"set after measured", []Option{Measured(supplied, "ledger", "attached-digest", "attached-manifest"), Set(run.KeyRuntime, "set-digest"), Set(run.KeyRuntimeInputs, "set-manifest")}, "set-digest", "set-manifest"},
		{"measured after set", []Option{Set(run.KeyRuntime, "set-digest"), Set(run.KeyRuntimeInputs, "set-manifest"), Measured(supplied, "ledger", "attached-digest", "attached-manifest")}, "attached-digest", "attached-manifest"},
		{"direct fingerprint fields", []Option{Measured(supplied, "ledger", "attached-digest", "attached-manifest"), func(r *Recording) {
			r.Fingerprint.RuntimeDigest, r.Fingerprint.RuntimeInputs = "direct-digest", "direct-manifest"
		}}, "direct-digest", "direct-manifest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := supplied
			want.RuntimeDigest, want.RuntimeInputs = tc.digest, tc.manifest
			if got := fixtureFingerprint(t, tc.opts...); got != want {
				t.Fatalf("runtime options changed evidence: %+v; want %+v", got, want)
			}
		})
	}
	if supplied.RuntimeDigest != "captured-digest" || supplied.RuntimeInputs != "captured-manifest" {
		t.Fatal("Measured mutated the caller's fingerprint")
	}
	// Options themselves, before Config, expose exactly the state they will write.
	r := Defaults()
	if r.Fingerprint.RuntimeDigest != "rt1" || r.Fingerprint.RuntimeInputs != "manifest1" {
		t.Fatalf("defaults omitted native runtime evidence: %+v", r.Fingerprint)
	}
	Measured(supplied, "ledger", "attached-digest", "attached-manifest")(&r)
	if r.Fingerprint.RuntimeDigest != "attached-digest" || r.Fingerprint.RuntimeInputs != "attached-manifest" {
		t.Fatalf("Measured did not update the authoritative fields: %+v", r.Fingerprint)
	}
	Set(run.KeyRuntime, "set-digest")(&r)
	Set(run.KeyRuntimeInputs, "set-manifest")(&r)
	if r.Fingerprint.RuntimeDigest != "set-digest" || r.Fingerprint.RuntimeInputs != "set-manifest" {
		t.Fatalf("Set did not update the authoritative fields: %+v", r.Fingerprint)
	}
}

func TestRepeatedRuntimeOmissionRefuses(t *testing.T) {
	for _, key := range []run.RecordingKey{run.KeyRuntime, run.KeyRuntimeInputs} {
		t.Run(key.Name, func(t *testing.T) {
			want := Defaults().Fingerprint
			if key == run.KeyRuntime {
				want.RuntimeDigest = ""
			} else {
				want.RuntimeInputs = ""
			}
			if got := fixtureFingerprint(t, Omit(key)); got != want {
				t.Fatalf("one omission changed other evidence: %+v; want %+v", got, want)
			}
			defer func() {
				p := recover()
				if p == nil || !strings.Contains(fmt.Sprint(p), "omitted absent fingerprint field "+key.Name) {
					t.Fatalf("repeated omission panic = %v; want absent %s", p, key.Name)
				}
			}()
			Config(Omit(key), Omit(key))
		})
	}
}
