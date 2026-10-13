package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExecutableObjectsAreImmutableAndConfined(t *testing.T) {
	b := []byte("retained executable bytes")
	for _, damage := range []string{"none", "bytes", "mode", "setuid", "setgid", "sticky", "file-link", "directory-link", "cancel"} {
		t.Run(damage, func(t *testing.T) {
			if runtime.GOOS == "windows" && (damage == "file-link" || damage == "directory-link") {
				t.Skip("symlinks need a Windows privilege")
			}
			if runtime.GOOS == "windows" && (damage == "setuid" || damage == "setgid" || damage == "sticky") {
				t.Skip("Unix special permission bits")
			}
			s := New(t.TempDir())
			p, err := s.PutExecutable(t.Context(), b)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(s.Root, ".profiles", "executables", fmt.Sprintf("%x.exe", sha256.Sum256(b)))
			if p != want {
				t.Fatalf("path=%s want=%s", p, want)
			}
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o500 {
				t.Fatalf("mode=%v", info.Mode())
			}
			ctx := t.Context()
			switch damage {
			case "bytes":
				if err := os.Chmod(p, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("changed"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(p, 0o500); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(p, 0o700); err != nil {
					t.Fatal(err)
				}
			case "setuid", "setgid", "sticky":
				special := map[string]os.FileMode{"setuid": os.ModeSetuid, "setgid": os.ModeSetgid, "sticky": os.ModeSticky}[damage]
				if err := os.Chmod(p, 0o500|special); err != nil {
					t.Fatal(err)
				}
			case "file-link":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "binary")
				if err := os.WriteFile(target, b, 0o500); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				if err := os.RemoveAll(filepath.Dir(p)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Dir(p)); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, err := s.PutExecutable(ctx, b)
			if damage == "none" {
				if err != nil || got != p {
					t.Fatalf("reuse=%q %v", got, err)
				}
				after, err := os.Stat(p)
				if err != nil || !os.SameFile(info, after) {
					t.Fatal("replaced a standing object")
				}
			} else if err == nil {
				t.Fatalf("accepted %s", damage)
			}
			if damage == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel=%v", err)
			}
			if damage == "bytes" {
				after, err := os.ReadFile(p)
				if err != nil || !bytes.Equal(after, []byte("changed")) {
					t.Fatal("repaired corrupted evidence")
				}
			}
		})
	}
}

func TestRetainedExecutableCanBeLaunched(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(t.TempDir()).PutExecutable(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), p, "-test.list=^TestRetainedExecutableCanBeLaunched$").CombinedOutput()
	if err != nil || string(bytes.TrimSpace(out)) != "TestRetainedExecutableCanBeLaunched" {
		t.Fatalf("launch %s: %v\n%s", p, err, out)
	}
}
