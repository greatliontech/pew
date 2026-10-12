package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/run"
)

// fixtureSession constructs the explicit native guard domain for analysis-only
// fixtures. Producing-observation tests must execute and close a real unit.
func fixtureSession(t testing.TB, directory string) *gofresh.Session {
	t.Helper()
	s, err := staticFixtureSession(directory)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fixtureArtifact(t testing.TB, ctx context.Context, snapshot *gofresh.Snapshot, pkg string) (string, gofresh.BuildBinding) {
	t.Helper()
	description, err := snapshot.BuildDescription(pkg)
	if err != nil {
		t.Fatal(err)
	}
	env, err := gotool.NewEnvironment(description.Environment)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "fixture.test")
	args := append([]string{"test", "-c", "-o", binary}, description.Flags...)
	args = append(args, pkg)
	if err := run.BuildContext(ctx, description.Directory, env, args); err != nil {
		t.Fatal(err)
	}
	binding, err := snapshot.BindBuilt(pkg, binary)
	if err != nil {
		t.Fatal(err)
	}
	return binary, binding
}

func executeFixtureContribution(t testing.TB, ctx context.Context, contribution gofresh.Contribution, binary, bench string) []byte {
	t.Helper()
	env, err := gotool.NewEnvironment(contribution.Environment())
	if err != nil {
		t.Fatal(err)
	}
	args := append(run.BinaryArgs(run.Options{Count: 1, Benchtime: "1x", Bench: "^" + regexp.QuoteMeta(bench) + "$"}), "-test.testlogfile="+contribution.Capture())
	out, err := run.ExecuteBinaryContext(ctx, contribution.Directory(), "", env, binary, args)
	if err != nil || !run.HarnessCompleted(out) {
		t.Fatalf("actual fixture execution: %v\n%s", err, out)
	}
	return out
}

func staticFixtureSession(directory string) (*gofresh.Session, error) {
	return gofresh.NewSession(context.Background(), gofresh.BuildDescription{Directory: directory, Environment: os.Environ(), Inputs: []string{}}, gofresh.RuntimeDescription{Directory: directory}, gofresh.SessionPolicy{Execution: gofresh.SingleSubjectExecution})
}

func fixtureFacts(ctx context.Context, session *gofresh.Session, subject gofresh.Subject) (gofresh.Fingerprint, error) {
	snapshot, err := session.Snapshot(ctx, []gofresh.Subject{subject}, gofresh.Measurement)
	if err != nil {
		return gofresh.Fingerprint{}, err
	}
	return snapshot.CurrentFacts(ctx, subject)
}

func fixtureCheck(ctx context.Context, session *gofresh.Session, fp gofresh.Fingerprint, subject gofresh.Subject) (gofresh.Verdict, error) {
	snapshot, err := session.Snapshot(ctx, []gofresh.Subject{subject}, fp.ResultKind)
	if err != nil {
		return gofresh.Verdict{}, err
	}
	return snapshot.Check(ctx, subject, fp)
}
