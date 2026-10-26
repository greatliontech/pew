package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gotool"
)

// builtBinary binds the caller-owned artifact bytes to the snapshot which
// supplied its build description. Endpoint checks share the same hold-still
// premise as source validation; they do not authenticate arbitrary executors.
type builtBinary struct {
	retainedExecutable
	binding gofresh.BuildBinding
}

type retainedExecutable struct {
	path, digest string
}

func compilePackageArtifact(ctx context.Context, rc runConfig, snapshot *gofresh.Snapshot, pkg string) (*builtBinary, string, error) {
	warmup, err := os.CreateTemp("", "pew-testbin-*")
	if err != nil {
		return nil, "", err
	}
	warmupPath := warmup.Name()
	_ = warmup.Close()
	build, err := snapshot.BuildDescription(pkg)
	if err != nil {
		_ = os.Remove(warmupPath)
		return nil, "", err
	}
	buildEnv, err := gotool.NewEnvironment(build.Environment)
	if err != nil {
		_ = os.Remove(warmupPath)
		return nil, "", err
	}
	if _, err := rc.executeGo(ctx, build.Directory, "", buildEnv, build.TestCompileArgs(warmupPath)); err != nil {
		_ = os.Remove(warmupPath)
		return nil, "", err
	}
	artifact, err := bindBinary(ctx, snapshot, pkg, warmupPath)
	if err != nil {
		_ = os.Remove(warmupPath)
		return nil, "", err
	}
	return artifact, warmupPath, nil
}

func bindBinary(ctx context.Context, snapshot *gofresh.Snapshot, pkg, path string) (*builtBinary, error) {
	executable, err := retainExecutable(ctx, path)
	if err != nil {
		return nil, err
	}
	binding, err := snapshot.BindBuilt(pkg, executable.digest)
	if err != nil {
		return nil, err
	}
	return &builtBinary{retainedExecutable: executable, binding: binding}, nil
}

func retainExecutable(ctx context.Context, path string) (retainedExecutable, error) {
	digest, err := executableDigest(ctx, path)
	return retainedExecutable{path: path, digest: digest}, err
}

func (b retainedExecutable) check(ctx context.Context) error {
	digest, err := executableDigest(ctx, b.path)
	if err != nil {
		return err
	}
	if digest != b.digest {
		return fmt.Errorf("retained test executable changed after its admitted build")
	}
	return nil
}

func executableDigest(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("reading retained test executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("retained test executable %q is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("reading retained test executable: %w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return "", fmt.Errorf("retained test executable %q moved while opening", path)
	}
	h := sha256.New()
	var buffer [32 << 10]byte
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buffer[:])
		_, _ = h.Write(buffer[:n])
		if err == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), ctx.Err()
		}
		if err != nil {
			return "", fmt.Errorf("reading retained test executable: %w", err)
		}
	}
}
