package run

import (
	"github.com/greatliontech/gofresh"
	"testing"
)

// Independent native evidence fields retain their own presence and projections:
// recording a vouch does not imply purity or an attestation-borne discharge.
func TestGofreshEvidenceConfigsGateIndependently(t *testing.T) {
	keys := func(purity, vouches, single, pkgProc string) []string {
		var out []string
		fp := gofresh.Fingerprint{ResultKind: gofresh.Measurement, PurityAssertion: purity, DynamicStateVouches: vouches, SingleSubjectDischarges: single, PackageProcessDischarges: pkgProc}
		encoded, err := EncodeFingerprint(fp)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeFingerprint(encoded)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []RecordingKey{KeyPurity, KeyVouches, KeySingleSubjectDischarges, KeyPackageProcessDischarges} {
			if v := FingerprintValue(decoded, k.Name); v != "" {
				out = append(out, k.Name+"="+v)
			}
		}
		return out
	}
	if got := keys("", "", "", ""); len(got) != 0 {
		t.Fatalf("empty evidence emitted lines: %v", got)
	}
	if got := keys("caller assertion", "", "", ""); len(got) != 1 || got[0] != "pew-purity=caller assertion" {
		t.Fatalf("purity-only = %v", got)
	}
	if got := keys("", "a.example/dep.Var", "", ""); len(got) != 1 || got[0] != "pew-vouches=a.example/dep.Var" {
		t.Fatalf("vouches-only = %v", got)
	}
	if got := keys("", "", "a.example/pool.Var", ""); len(got) != 1 || got[0] != "pew-single-subject-discharges=a.example/pool.Var" {
		t.Fatalf("single-subject-only = %v", got)
	}
	if got := keys("", "", "", "a.example/proc.Var"); len(got) != 1 || got[0] != "pew-package-process-discharges=a.example/proc.Var" {
		t.Fatalf("package-process-only = %v", got)
	}
	if got := keys("caller assertion", "a.example/dep.Var", "a.example/pool.Var", "a.example/proc.Var"); len(got) != 4 || got[1] != "pew-vouches=a.example/dep.Var" || got[3] != "pew-package-process-discharges=a.example/proc.Var" {
		t.Fatalf("all = %v", got)
	}
}
