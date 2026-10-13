package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"golang.org/x/perf/benchfmt"
)

// ErrChanged means the conditional publication's exact recording no longer exists.
var ErrChanged = errors.New("store: recording changed before attachment")

// ErrObjectIntegrity distinguishes readable but changed bytes from unavailable objects.
var ErrObjectIntegrity = errors.New("object integrity failure")

func publicationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}

// ReadBytes returns the exact recording revision through the store's confinement boundary.
func (s *Store) ReadBytes(k Key) ([]byte, error) {
	p, err := s.Path(k.PkgRel, k.Bench, k.Label)
	if err != nil {
		return nil, err
	}
	return s.readRegular(p)
}

func (s *Store) readRegular(p string) ([]byte, error) {
	if err := s.checkParentDirs(p); err != nil {
		return nil, err
	}
	if err := checkRegularFile(p); err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// ReplaceContext compares expected bytes and atomically replaces under the same
// lock as all store writers/removers. Nil expected requests an unconditional write.
func (s *Store) ReplaceContext(ctx context.Context, k Key, expected []byte, rows []*benchfmt.Result) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if expected != nil {
		actual, err := s.ReadBytes(k)
		if errors.Is(err, os.ErrNotExist) {
			return ErrChanged
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, expected) {
			return ErrChanged
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.writeUnlocked(ctx, k.PkgRel, k.Bench, k.Label, rows)
}

var digestName = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ObjectPath derives a profile or source object's path; serialized paths are never accepted.
func (s *Store) ObjectPath(digest string) (string, error) {
	if !digestName.MatchString(digest) {
		return "", fmt.Errorf("store: invalid object digest")
	}
	return filepath.Join(s.Root, ".profiles", "sha256", digest), nil
}

// ReadObject verifies immutable bytes, including an existing object's full length.
func (s *Store) ReadObject(digest string, size int64) ([]byte, error) {
	p, err := s.ObjectPath(digest)
	if err != nil {
		return nil, err
	}
	b, err := s.readRegular(p)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != size || fmt.Sprintf("%x", sha256.Sum256(b)) != digest {
		return nil, fmt.Errorf("store: %w: %s", ErrObjectIntegrity, digest)
	}
	return b, nil
}

// PutObject installs immutable bytes before their recording reference. Unreferenced
// objects are retained: collection must not race a publisher between these operations.
func (s *Store) PutObject(ctx context.Context, b []byte) (string, error) {
	digest := fmt.Sprintf("%x", sha256.Sum256(b))
	p, _ := s.ObjectPath(digest)
	return digest, s.putObject(ctx, p, b, 0o600)
}

// PutExecutable retains a diagnostic's binary under its content identity and
// returns the absolute path to execute. Existing objects are never repaired.
func (s *Store) PutExecutable(ctx context.Context, b []byte) (string, error) {
	digest := fmt.Sprintf("%x", sha256.Sum256(b))
	// A fixed suffix is executable by os/exec on Windows as well as Unix.
	p := filepath.Join(s.Root, ".profiles", "executables", digest+".exe")
	if err := s.putObject(ctx, p, b, 0o500); err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

func (s *Store) putObject(ctx context.Context, p string, b []byte, mode os.FileMode) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.ensureDir(filepath.Dir(p)); err != nil {
		return err
	}
	if info, err := os.Lstat(p); err == nil {
		actual, err := s.readRegular(p)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, b) {
			return fmt.Errorf("store: %w: %s", ErrObjectIntegrity, p)
		}
		if mode&0o100 != 0 && !executableMode(info.Mode()) {
			return fmt.Errorf("store: executable permissions changed: %s", p)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".object-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(b)
	modeErr := f.Chmod(mode)
	if err := errors.Join(writeErr, modeErr, f.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), p); err != nil {
		return err
	}
	return nil
}

func executableMode(mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		// Windows exposes the read-only bit through Chmod, not Unix execute bits.
		return mode.Perm()&0o200 == 0
	}
	return mode&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0o500
}
