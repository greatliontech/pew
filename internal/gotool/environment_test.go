package gotool

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	gofreshtool "github.com/greatliontech/gofresh/gotool"
)

func testEnvironment(t testing.TB, entries []string) Environment {
	t.Helper()
	env, err := NewEnvironment(entries)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestEnvironmentOwnsItsSnapshot(t *testing.T) {
	input := []string{"Z=last", "PWD=/original", "A=first"}
	env, err := NewEnvironment(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = "Z=changed"
	values := env.Values()
	values[0] = "A=changed"
	pinned, err := env.For("/measured")
	if err != nil {
		t.Fatal(err)
	}
	pinned[0] = "A=changed-again"
	if got := env.Values(); !reflect.DeepEqual(got, []string{"A=first", "PWD=/original", "Z=last"}) {
		t.Fatalf("snapshot changed through an alias: %v", got)
	}
	pinned, err = env.For("/measured")
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.Abs("/measured")
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := gofreshtool.LookupEnv(pinned, "PWD"); value != wantDir {
		t.Fatalf("PWD = %q", value)
	}
	measured := env.WithParallelism(2)
	if value, _ := measured.Lookup("GOMAXPROCS"); value != "2" {
		t.Fatalf("measured parallelism = %q", value)
	}
	if _, ok := env.Lookup("GOMAXPROCS"); ok {
		t.Fatal("measured setting leaked into analysis snapshot")
	}
}

func TestCommandEnvironmentAdmitsDirectoryBeforePinning(t *testing.T) {
	want, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := (Environment{}).For(".")
	if err != nil {
		t.Fatal(err)
	}
	if pwd, _ := gofreshtool.LookupEnv(entries, "PWD"); pwd != want {
		t.Fatalf("relative directory PWD = %q, want %q", pwd, want)
	}
	for _, dir := range []string{"/tmp/\x00", "relative\x00"} {
		entries, err := (Environment{}).For(dir)
		var envErr *EnvironmentError
		if !errors.As(err, &envErr) || entries != nil {
			t.Fatalf("invalid directory %q: %v, %v", dir, entries, err)
		}
	}
}

func TestEnvironmentAdmissionPreservesEmptyAndInherited(t *testing.T) {
	t.Setenv("PEW_ENV_SNAPSHOT", "before")
	inherited, err := NewEnvironment(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PEW_ENV_SNAPSHOT", "after")
	if value, _ := inherited.Lookup("PEW_ENV_SNAPSHOT"); value != "before" {
		t.Fatalf("snapshot inherited again: %q", value)
	}
	empty, err := NewEnvironment([]string{})
	if err != nil || empty.Values() == nil || len(empty.Values()) != 0 {
		t.Fatalf("explicit empty = %v, %v", empty.Values(), err)
	}
	var zero Environment
	if zero.Values() == nil || len(zero.Values()) != 0 {
		t.Fatalf("zero environment inherited: %v", zero.Values())
	}
	for _, input := range [][]string{{"A=1", "A=2"}, {"missing-separator"}, {"A=\x00"}} {
		_, err := NewEnvironment(input)
		var envErr *EnvironmentError
		if !errors.As(err, &envErr) {
			t.Fatalf("invalid input %q: %v", input, err)
		}
	}
}
