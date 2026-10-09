package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/run"
)

type abGitControl struct{ env gotool.Environment }
type abOwner struct {
	Repository string
	PID        int
}

func (g abGitControl) command(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return run.ExecuteDiagnostic(ctx, dir, "", g.env, "git", args, nil)
}

func (g abGitControl) topLevel(ctx context.Context, dir string) (string, error) {
	b, err := g.command(ctx, dir, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(string(b)), err
}

func (g abGitControl) common(ctx context.Context, repo string) (string, error) {
	b, err := g.command(ctx, repo, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(b))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repo, dir)
	}
	return filepath.EvalSymlinks(dir)
}

func (g abGitControl) registry(ctx context.Context, repo string) (map[string]bool, error) {
	b, err := g.command(ctx, repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, field := range bytes.Split(b, []byte{0}) {
		if path, ok := strings.CutPrefix(string(field), "worktree "); ok {
			if resolved, e := filepath.EvalSymlinks(path); e == nil {
				path = resolved
			}
			paths[filepath.Clean(path)] = true
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("ab: empty or malformed worktree registration inventory")
	}
	return paths, nil
}

func readABOwner(dir string) (abOwner, []byte, error) {
	b, err := readProfileFile(dir + ".owner")
	if err != nil {
		return abOwner{}, nil, err
	}
	var owner abOwner
	err = json.Unmarshal(b, &owner)
	if err == nil && (owner.Repository == "" || owner.PID <= 0) {
		err = fmt.Errorf("invalid worktree owner")
	}
	return owner, b, err
}

func (g abGitControl) add(ctx context.Context, repo, placement, ref string) (string, func(context.Context) error, error) {
	common, err := g.common(ctx, repo)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(placement, ".pew-ab-worktree-*")
	if err != nil {
		return "", nil, err
	}
	owner, _ := json.Marshal(abOwner{Repository: common, PID: os.Getpid()})
	f, err := os.OpenFile(dir+".owner", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return dir, nil, errors.Join(err, os.Remove(dir))
	}
	_, writeErr := f.Write(owner)
	if err = errors.Join(writeErr, f.Close()); err != nil {
		return dir, nil, fmt.Errorf("ab: ownership write failed; residue %s: %w", dir, err)
	}
	cleanup := func(gate context.Context) error {
		_, actual, err := readABOwner(dir)
		if err != nil || !bytes.Equal(actual, owner) {
			return fmt.Errorf("ab: refusing cleanup of %s: ownership changed: %w", dir, err)
		}
		registered, err := g.registry(gate, repo)
		if err != nil {
			return fmt.Errorf("ab: cleanup registration unavailable; residue %s: %w", dir, err)
		}
		physical, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return fmt.Errorf("ab: uncertain worktree residue %s: %w", dir, err)
		}
		if physical != filepath.Clean(dir) {
			return fmt.Errorf("ab: cleanup path changed: %s", dir)
		}
		if registered[physical] {
			if !worktreeOwnedBy(dir, common) {
				return fmt.Errorf("ab: foreign registration; retained %s", dir)
			}
			if _, err := g.command(gate, repo, "worktree", "remove", "--force", dir); err != nil {
				return fmt.Errorf("ab: cleanup failed; retained %s: %w", dir, err)
			}
		} else {
			if !emptyDir(dir) {
				return fmt.Errorf("ab: uncertain unregistered residue retained: %s", dir)
			}
			if err := os.Remove(dir); err != nil {
				return err
			}
		}
		return os.Remove(dir + ".owner")
	}
	_, err = g.command(ctx, repo, "worktree", "add", "--detach", dir, ref)
	if err != nil {
		err = fmt.Errorf("ab: worktree creation incomplete; cleanup owns uncertain residue %s: %w", dir, err)
	}
	return dir, cleanup, err
}

func (g abGitControl) sweep(ctx context.Context, repo, placement string) ([]string, error) {
	entries, err := os.ReadDir(placement)
	if err != nil {
		return nil, err
	}
	common, err := g.common(ctx, repo)
	if err != nil {
		return nil, err
	}
	registered, err := g.registry(ctx, repo)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".pew-ab-worktree-") {
			continue
		}
		dir := filepath.Join(placement, entry.Name())
		owner, _, err := readABOwner(dir)
		if err != nil || owner.Repository != common || registered[dir] {
			continue
		}
		process, err := os.FindProcess(owner.PID)
		if err != nil {
			continue
		}
		err = process.Signal(syscall.Signal(0))
		process.Release()
		if !errors.Is(err, syscall.ESRCH) && !errors.Is(err, os.ErrProcessDone) {
			continue
		}
		if !worktreeOwnedBy(dir, common) && !emptyDir(dir) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return removed, err
		}
		if err := os.Remove(dir + ".owner"); err != nil {
			return removed, err
		}
		removed = append(removed, dir)
	}
	return removed, nil
}
