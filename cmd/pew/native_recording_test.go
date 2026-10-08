package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/recordingtest"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

// Exercise the real file boundary with every native field, including proofs whose
// integrity the shared checker (not the recording decoder) is responsible for.
func TestNativeFingerprintStoreRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1907))
	for n := 0; n < 40; n++ {
		fp := recordingtest.Defaults().Fingerprint
		fill := func(v reflect.Value) {
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).Kind() == reflect.String {
					v.Field(i).SetString(strings.Repeat("\"\\\n\tλ<>&", 1+rng.Intn(30)))
				}
			}
		}
		fill(reflect.ValueOf(&fp).Elem())
		fill(reflect.ValueOf(&fp.Guards).Elem())
		fill(reflect.ValueOf(&fp.ObservationProof).Elem())
		fill(reflect.ValueOf(&fp.ObservationProof.Subject).Elem())
		fp.ObservationProof.Observable = n%2 == 0
		if fp.ObservationProof.Observable {
			fp.ObservationProof.Reason = ""
		}
		if n == 0 {
			fp.RuntimeInputs = strings.Repeat("large\nλ", runpkg.ChunkBound)
		}
		cfg, err := runpkg.FingerprintConfigs(fp, "ledger")
		if err != nil {
			t.Fatal(err)
		}
		cfg = append(runpkg.ProvenanceConfig("commit", false, runpkg.Conditions{}), cfg...)
		rows := []*benchfmt.Result{admissionRow(cfg), admissionRow(cfg)}
		st := store.New(t.TempDir())
		if err := st.Write("", "BenchmarkX", "", rows); err != nil {
			t.Fatal(err)
		}
		back, err := st.Read("", "BenchmarkX", "")
		if err != nil {
			t.Fatal(err)
		}
		adm := admitRecording(back, false)
		if !adm.ok || adm.fp != fp {
			t.Fatalf("case %d: evidence changed: %+v, want %+v", n, adm, fp)
		}
		for _, c := range back[0].Config {
			if runpkg.IsFingerprintProjection(c.Key) {
				t.Fatalf("parallel field %s persisted", c.Key)
			}
		}
		native, err := json.Marshal(fp)
		if err != nil {
			t.Fatal(err)
		}
		data, err := base64.RawURLEncoding.DecodeString(back[0].GetConfig(runpkg.KeyFingerprint.Name))
		if err != nil || !bytes.Equal(native, data) {
			t.Fatalf("not the native record bytes: %v", err)
		}
	}
}

func FuzzNativeFingerprintEnvelope(f *testing.F) {
	f.Add("plain", true)
	f.Add("\"\\\n\tλ<>&", false)
	f.Add(strings.Repeat("x", runpkg.ChunkBound), true)
	f.Fuzz(func(t *testing.T, value string, observable bool) {
		// JSON strings represent Unicode, not arbitrary invalid UTF-8 byte strings.
		if !utf8.ValidString(value) {
			return
		}
		fp := recordingtest.Defaults().Fingerprint
		fp.RuntimeInputs, fp.RuntimeDigest = value, value
		fp.ObservationAssertion = value
		fp.ObservationProof = gofresh.ObservationProof{Strategy: value, Subject: gofresh.Subject{Package: value, Symbol: value}, Observable: observable, Evidence: value}
		if !observable {
			fp.ObservationProof.Reason = value
		}
		encoded, err := runpkg.EncodeFingerprint(fp)
		if err != nil {
			t.Fatal(err)
		}
		cfg := runpkg.SplitChunked([]benchfmt.Config{runpkg.KeyFingerprint.Config(encoded)})
		joined, err := runpkg.JoinChunked(cfg)
		if err != nil {
			t.Fatal(err)
		}
		back, err := runpkg.DecodeFingerprint(string(joined[0].Value))
		if err != nil || back != fp {
			t.Fatalf("round trip: %+v, %v; want %+v", back, err, fp)
		}
	})
}

func TestNativePayloadRefusalsAndAllRowAdmission(t *testing.T) {
	rows := recordingtest.Results("X", []float64{1, 2})
	encoded := rows[0].GetConfig(runpkg.KeyFingerprint.Name)
	native, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	text := string(native)
	wrap := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	cases := map[string]string{
		"unknown":                      wrap(strings.Replace(text, "{", `{"unknown":1,`, 1)),
		"duplicate":                    wrap(strings.Replace(text, "{", `{"maximalClosure":"other",`, 1)),
		"null":                         wrap(strings.Replace(text, `"maximalClosure":"cl1"`, `"maximalClosure":null`, 1)),
		"trailing":                     wrap(text + "{}"),
		"zero kind":                    wrap(strings.Replace(text, `"resultKind":2`, `"resultKind":0`, 1)),
		"code with measurement guards": wrap(strings.Replace(text, `"resultKind":2`, `"resultKind":1`, 1)),
		"noncanonical":                 wrap(strings.Replace(text, `"maximalClosure":"cl1","testVariantClosure":"tv1"`, `"testVariantClosure":"tv1","maximalClosure":"cl1"`, 1)),
		"proof missing observable":     wrap(strings.Replace(text, `"resultKind":2`, `"observationProof":{"strategy":"x"},"resultKind":2`, 1)),
		"base64 padding":               encoded + "=",
		"base64 newline":               encoded + "\n",
		"malformed":                    wrap("{"),
	}
	validFP, err := runpkg.DecodeFingerprint(encoded)
	if err != nil {
		t.Fatal(err)
	}
	// These payloads deliberately pass native decoding. Required native keys
	// whose codec permits an empty value must exercise Pew's nonempty-value
	// check, not fail earlier on a noncanonical missing JSON key.
	for _, field := range []struct {
		name  string
		clear func(*gofresh.Fingerprint)
	}{
		{"maximalClosure", func(fp *gofresh.Fingerprint) { fp.MaximalClosure = "" }},
		{"testVariantClosure", func(fp *gofresh.Fingerprint) { fp.TestVariantClosure = "" }},
		{"toolchain", func(fp *gofresh.Fingerprint) { fp.Guards.Toolchain = "" }},
		{"buildConfig", func(fp *gofresh.Fingerprint) { fp.Guards.BuildConfig = "" }},
		{"machine", func(fp *gofresh.Fingerprint) { fp.Guards.Machine = "" }},
		{"runtimeConfig", func(fp *gofresh.Fingerprint) { fp.Guards.RuntimeConfig = "" }},
		{"runtimeInputs", func(fp *gofresh.Fingerprint) { fp.RuntimeInputs = "" }},
		{"runtimeDigest", func(fp *gofresh.Fingerprint) { fp.RuntimeDigest = "" }},
	} {
		fp := validFP
		field.clear(&fp)
		payload, err := runpkg.EncodeFingerprint(fp)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := runpkg.DecodeFingerprint(payload)
		if err != nil || decoded != fp {
			t.Fatalf("missing %s must reach Pew validation: %+v, %v", field.name, decoded, err)
		}
		cases["missing value/"+field.name] = payload
	}
	// Also exercise absent keys individually. Required native keys refuse in
	// the codec; optional native keys that Pew requires reach its admission.
	for _, field := range []string{"maximalClosure", "testVariantClosure", "toolchain", "buildConfig", "machine", "runtimeConfig", "runtimeInputs", "runtimeDigest", "resultKind"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(native, &fields); err != nil {
			t.Fatal(err)
		}
		entry := `"` + field + `":` + string(fields[field])
		missing := strings.Replace(text, entry+",", "", 1)
		if missing == text {
			missing = strings.Replace(text, ","+entry, "", 1)
		}
		if missing == text {
			t.Fatalf("missing-key fixture did not remove %s", field)
		}
		cases["missing key/"+field] = wrap(missing)
	}
	// A CodeResult with measurement guards is rejected by Gofresh itself. This
	// native-valid CodeResult instead reaches Pew's measurement-only boundary.
	code := validFP
	code.ResultKind = gofresh.CodeResult
	code.Guards.Machine, code.Guards.RuntimeConfig = "", ""
	codePayload, err := runpkg.EncodeFingerprint(code)
	if err != nil {
		t.Fatal(err)
	}
	decodedCode, err := runpkg.DecodeFingerprint(codePayload)
	if err != nil || decodedCode != code {
		t.Fatalf("code fixture must pass native decoding: %+v, %v", decodedCode, err)
	}
	cases["native-valid code result"] = codePayload
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if payload == encoded {
				t.Fatal("fixture did not change the payload")
			}
			bad := rows[1].Clone()
			for i := range bad.Config {
				if bad.Config[i].Key == runpkg.KeyFingerprint.Name {
					bad.Config[i] = runpkg.KeyFingerprint.Config(payload)
				}
			}
			for _, arrangement := range []struct {
				name string
				rows []*benchfmt.Result
			}{
				{"sole bad row", []*benchfmt.Result{bad}},
				{"identical bad rows", []*benchfmt.Result{bad, bad.Clone()}},
				{"valid then bad", []*benchfmt.Result{rows[0], bad}},
			} {
				t.Run(arrangement.name, func(t *testing.T) {
					for _, side := range []struct {
						name    string
						working bool
					}{{"ref", false}, {"working tree", true}} {
						t.Run(side.name, func(t *testing.T) {
							if adm := admitRecording(arrangement.rows, side.working); adm.ok || adm.class != "format" || adm.fp != (gofresh.Fingerprint{}) {
								t.Fatalf("invalid payload admitted: %+v", adm)
							}
						})
					}
				})
			}
		})
	}
	for _, key := range runpkg.FingerprintProjectionKeys {
		bad := rows[0].Clone()
		bad.Config = append(bad.Config, key.Config("parallel"))
		if adm := admitRecording([]*benchfmt.Result{bad}, false); adm.ok {
			t.Fatalf("parallel %s admitted", key.Name)
		}
	}
}

func TestRefreshPreservesNativeHistoricalEvidence(t *testing.T) {
	for _, proof := range []bool{false, true} {
		fp := recordingtest.Defaults().Fingerprint
		fp.ClosureStrategy = "" // No refresh can manufacture a historical derivation.
		if proof {
			fp.ObservationAssertion = "historical-attribution"
			fp.ObservationProof = gofresh.ObservationProof{Strategy: "unrecognized", Subject: gofresh.Subject{Package: "p", Symbol: "BenchmarkX"}, Evidence: "inconsistent", Reason: "unsupported"}
		}
		fp.RuntimeInputs, fp.RuntimeDigest = "original-manifest", "original-digest"
		rows := recordingtest.Results("X", []float64{1, 2}, recordingtest.Measured(fp, "old-ledger", fp.RuntimeDigest, fp.RuntimeInputs))
		st := store.New(t.TempDir())
		if err := st.Write("", "BenchmarkX", "", rows); err != nil {
			t.Fatal(err)
		}
		if err := refreshRecording(st, "", "BenchmarkX", "", "new-variant", "new-ledger"); err != nil {
			t.Fatal(err)
		}
		back, err := st.Read("", "BenchmarkX", "")
		if err != nil {
			t.Fatal(err)
		}
		fp.TestVariantClosure = "new-variant"
		adm := admitRecording(back, false)
		if !adm.ok || adm.fp != fp || adm.ledger != "new-ledger" {
			t.Fatalf("refresh changed historical evidence: %+v, want %+v", adm, fp)
		}
		for i := range rows {
			if !reflect.DeepEqual(rows[i].Values, back[i].Values) || !bytes.Equal(rows[i].Name, back[i].Name) || rows[i].Iters != back[i].Iters {
				t.Fatal("refresh changed measurement")
			}
			for _, c := range rows[i].Config {
				if c.Key != runpkg.KeyFingerprint.Name && c.Key != runpkg.KeyTestVariantLedger.Name && back[i].GetConfig(c.Key) != string(c.Value) {
					t.Fatalf("refresh changed %s", c.Key)
				}
			}
		}
		path, err := st.Path("", "BenchmarkX", "")
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, version := range []string{"1", "2", "3"} {
			legacy := bytes.Replace(data, []byte("pew-format: 4"), []byte("pew-format: "+version), 1)
			if err := os.WriteFile(path, legacy, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := refreshRecording(st, "", "BenchmarkX", "", "upgrade", "upgrade"); err == nil {
				t.Fatal("legacy refresh accepted")
			}
			untouched, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(legacy, untouched) {
				t.Fatalf("failed refresh changed old bytes: %v", err)
			}
		}
	}
}
