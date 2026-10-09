package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/greatliontech/pew/internal/recordingtest"
)

func TestConditionalPublicationPreservesNewerBytes(t *testing.T) {
	s := New(t.TempDir())
	k := Key{Bench: "BenchmarkX"}
	old := recordingtest.Results("X", []float64{1})
	if err := s.Write("", k.Bench, "", old); err != nil {
		t.Fatal(err)
	}
	expected, err := s.ReadBytes(k)
	if err != nil {
		t.Fatal(err)
	}
	newer := recordingtest.Results("X", []float64{9})
	if err := s.WriteBatchContext(context.Background(), []WriteRequest{{Bench: k.Bench, Results: newer}}); err != nil {
		t.Fatal(err)
	}
	current, err := s.ReadBytes(k)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceContext(context.Background(), k, expected, old); !errors.Is(err, ErrChanged) {
		t.Fatalf("CAS: %v", err)
	}
	back, _ := s.ReadBytes(k)
	if !bytes.Equal(back, current) {
		t.Fatal("CAS clobbered newer bytes")
	}
	if err := s.Remove(Recording{Bench: k.Bench}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceContext(context.Background(), k, current, old); !errors.Is(err, ErrChanged) {
		t.Fatalf("CAS resurrected removed recording: %v", err)
	}
}

func TestPublicationLockCrossProcessBound(t *testing.T) {
	if os.Getenv("PEW_LOCK_CHILD") != "" {
		s := New(os.Getenv("PEW_LOCK_CHILD"))
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if err := s.ReplaceContext(ctx, Key{Bench: "BenchmarkX"}, nil, recordingtest.Results("X", []float64{2})); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("child bypassed lock: %v", err)
		}
		return
	}
	s := New(t.TempDir())
	unlock, err := s.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPublicationLockCrossProcessBound$")
	cmd.Env = append(os.Environ(), "PEW_LOCK_CHILD="+s.Root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestImmutableObjectsAndConfinedAttachment(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	b := []byte("profile bytes")
	digest, err := s.PutObject(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutObject(ctx, b); err != nil {
		t.Fatal(err)
	}
	p, _ := s.ObjectPath(digest)
	if err := os.WriteFile(p, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutObject(ctx, b); err == nil {
		t.Fatal("corrupt existing object reused")
	}
	if _, err := s.ReadObject("../../escape", 1); err == nil {
		t.Fatal("accepted path")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadObject(digest, int64(len(b))); err == nil {
		t.Fatal("followed object symlink")
	}
	if _, err := s.PutObject(ctx, b); err == nil {
		t.Fatal("reused object symlink")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutObject(ctx, b); err == nil {
		t.Fatal("followed parent symlink")
	}
}
