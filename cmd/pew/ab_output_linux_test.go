//go:build linux

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/pew/internal/recordingtest"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/sys/unix"
)

func TestABOutputCannotReplaceHeldRecordingLock(t *testing.T) {
	root := abFixtureRepo(t)
	st := store.New(filepath.Join(root, "benchmarks"))
	if err := st.Write("p", "BenchmarkWork", "", recordingtest.Results("Work", []float64{1})); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Root, ".pew-lock")
	held, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := unix.Flock(int(held.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(held.Fd()), unix.LOCK_UN)
	before, err := held.Stat()
	if err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, root)
	ac := abConfigStub("")
	ac.out = path
	if err := runAB(t.Context(), io.Discard, io.Discard, ac, []string{"./p"}); err == nil {
		t.Fatal("lock destination admitted")
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("permanent lock inode replaced: %v", err)
	}
	contender, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	err = unix.Flock(int(contender.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("held lock no longer serializes writers: %v", err)
	}
}
