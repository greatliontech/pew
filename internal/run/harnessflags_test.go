package run

import (
	"slices"
	"testing"
)

func TestInheritedHarnessArgumentsKeepTheEffectivePolicy(t *testing.T) {
	for _, spelling := range []string{"-v=0", "-v=FALSE", "-test.v=false"} {
		args, err := HarnessArgs(spelling)
		if err != nil || !slices.Contains(args, "-test.v=false") {
			t.Fatalf("%s => %v, %v", spelling, args, err)
		}
	}
	args, err := HarnessArgs(`-test.v=1 -short -failfast=0 -timeout=3s -parallel=2 -shuffle=4 -bench=Foreign -count=8 -benchtime=20s`)
	want := []string{"-test.failfast=false", "-test.paniconexit0=true", "-test.parallel=2", "-test.short=true", "-test.shuffle=4", "-test.timeout=3s", "-test.v=true"}
	if err != nil || !slices.Equal(args, want) {
		t.Fatalf("effective harness arguments = %v, %v; want %v", args, err, want)
	}
	for _, flags := range []string{"-json=false", "-n=0", "-c=false", "-e", "-json=true --json=false", "--n -n=0", "-c=true -c=false", `'-pgo=/tmp/path with space/profile.pprof' -trimpath`} {
		if args, err := HarnessArgs(flags); err != nil || !slices.Equal(args, []string{"-test.paniconexit0=true", "-test.timeout=10m0s"}) {
			t.Fatalf("driver-only %q = %v, %v", flags, args, err)
		}
	}
	for _, flags := range []string{"-json=true", "-json=false --json", "--n=false -n=1", "-n", "-c", "-cpu=1,2", "-skip=Child", "-exec=wrapper", "-v=garbage", "-test.e", "-unknown", "'-pgo=unterminated", "-parallel=0", "-parallel=-1", "-parallel=2 -test.parallel=0", "-parallel=garbage -parallel=2"} {
		if args, err := HarnessArgs(flags); err == nil {
			t.Fatalf("unowned policy %q admitted as %v", flags, args)
		}
	}
	for _, flags := range []string{"-parallel=0x2", "-test.parallel=0b10", "-parallel=02", "-parallel=0 -test.parallel=0x2", "--test.parallel=-1 -parallel=2"} {
		args, err := HarnessArgs(flags)
		if err != nil || !slices.Contains(args, "-test.parallel=2") {
			t.Fatalf("inherited integer %s = %v, %v", flags, args, err)
		}
	}
	if args, err := HarnessArgs("-timeout=-1s"); err != nil || !slices.Contains(args, "-test.timeout=-1s") {
		t.Fatalf("nonpositive timeout (harness alarm disabled) refused: %v, %v", args, err)
	}
}
