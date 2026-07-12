package gitblob

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestReproducibleAtMatchingStates(t *testing.T) {
	repo, ref := reproducibleFixture(t)

	for _, rel := range []string{
		".",
		"root.txt",
		"run.sh",
		"root-link",
		"tree",
		"tree/nested",
		"tree/nested/deep.txt",
		"tree/direct-link",
		"missing",
	} {
		t.Run(rel, func(t *testing.T) {
			got, err := repo.ReproducibleAt(ref, filepath.Join(repo.root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("ReproducibleAt: %v", err)
			}
			if !got {
				t.Error("ReproducibleAt = false, want true")
			}
		})
	}
}

func TestReproducibleAtPresence(t *testing.T) {
	t.Run("worktree only", func(t *testing.T) {
		repo, ref := reproducibleFixture(t)
		path := filepath.Join(repo.root, "new.txt")
		writeFile(t, path, "new", 0o644)
		assertNotReproducible(t, repo, ref, path)
	})

	t.Run("commit only", func(t *testing.T) {
		repo, ref := reproducibleFixture(t)
		path := filepath.Join(repo.root, "root.txt")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		assertNotReproducible(t, repo, ref, path)
	})

	t.Run("unrepresentable empty directory", func(t *testing.T) {
		repo, ref := reproducibleFixture(t)
		path := filepath.Join(repo.root, "empty")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		assertNotReproducible(t, repo, ref, path)
	})
}

func TestReproducibleAtRegularFile(t *testing.T) {
	tests := []struct {
		name   string
		rel    string
		mutate func(*testing.T, string)
	}{
		{
			name: "content",
			rel:  "root.txt",
			mutate: func(t *testing.T, path string) {
				writeFile(t, path, "changed\n", 0o644)
			},
		},
		{
			name: "regular made executable",
			rel:  "root.txt",
			mutate: func(t *testing.T, path string) {
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "executable made regular",
			rel:  "run.sh",
			mutate: func(t *testing.T, path string) {
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, ref := reproducibleFixture(t)
			path := filepath.Join(repo.root, filepath.FromSlash(test.rel))
			test.mutate(t, path)
			assertNotReproducible(t, repo, ref, path)
		})
	}

	t.Run("non-executable permissions are not Git state", func(t *testing.T) {
		repo, ref := reproducibleFixture(t)
		path := filepath.Join(repo.root, "root.txt")
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := repo.ReproducibleAt(ref, path)
		if err != nil {
			t.Fatalf("ReproducibleAt: %v", err)
		}
		if !got {
			t.Error("ReproducibleAt = false after non-executable chmod, want true")
		}
	})
}

func TestReproducibleAtSymlink(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "target changed",
			mutate: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("run.sh", path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "replaced by regular file",
			mutate: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, "root.txt", 0o644)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, ref := reproducibleFixture(t)
			path := filepath.Join(repo.root, "root-link")
			test.mutate(t, path)
			assertNotReproducible(t, repo, ref, path)
		})
	}

	t.Run("path below symlink", func(t *testing.T) {
		repo, ref := reproducibleFixture(t)
		assertNotReproducible(t, repo, ref, filepath.Join(repo.root, "root-link", "child"))
	})

	t.Run("uncommitted resolved target", func(t *testing.T) {
		repo, _ := reproducibleFixture(t)
		link := filepath.Join(repo.root, "ignored-link")
		if err := os.Symlink("ignored.dat", link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		wt, err := repo.repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("ignored-link"); err != nil {
			t.Fatal(err)
		}
		ref, err := wt.Commit("link", &gogit.CommitOptions{Author: testSignature()})
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(repo.root, "ignored.dat"), "secret", 0o644)
		assertNotReproducible(t, repo, ref.String(), link)
	})

	t.Run("external resolved target", func(t *testing.T) {
		repo, _ := reproducibleFixture(t)
		externalDir := t.TempDir()
		writeFile(t, filepath.Join(externalDir, "input.dat"), "external", 0o644)
		target := filepath.Join("..", filepath.Base(externalDir), "input.dat")
		link := filepath.Join(repo.root, "external-link")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		wt, err := repo.repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("external-link"); err != nil {
			t.Fatal(err)
		}
		ref, err := wt.Commit("external link", &gogit.CommitOptions{Author: testSignature()})
		if err != nil {
			t.Fatal(err)
		}
		commit, err := repo.repo.CommitObject(ref)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := commit.Tree()
		if err != nil {
			t.Fatal(err)
		}
		entry, err := tree.FindEntry("external-link")
		if err != nil {
			t.Fatal(err)
		}
		if entry.Mode != filemode.Symlink {
			t.Fatalf("committed external link mode = %v, want symlink", entry.Mode)
		}
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatal(err)
		}
		mode, err := filemode.NewFromOSFileMode(info.Mode())
		if err != nil {
			t.Fatal(err)
		}
		if mode != entry.Mode {
			t.Fatalf("worktree external link mode = %v, committed = %v", mode, entry.Mode)
		}
		matches, err := repo.reproducibleBlob([]byte(target), entry.Hash, "external-link")
		if err != nil {
			t.Fatal(err)
		}
		if !matches {
			file, fileErr := commit.File("external-link")
			if fileErr != nil {
				t.Fatal(fileErr)
			}
			contents, contentsErr := file.Contents()
			if contentsErr != nil {
				t.Fatal(contentsErr)
			}
			t.Fatalf("committed external link target = %q, worktree target = %q", contents, target)
		}
		assertNotReproducible(t, repo, ref.String(), link)
	})

	t.Run("target outside module boundary", func(t *testing.T) {
		root := t.TempDir()
		moduleDir := filepath.Join(root, "mod")
		sharedDir := filepath.Join(root, "shared")
		if err := os.MkdirAll(moduleDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sharedDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(sharedDir, "input.dat"), "shared", 0o644)
		link := filepath.Join(moduleDir, "input")
		if err := os.Symlink(filepath.Join("..", "shared", "input.dat"), link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		raw, err := gogit.PlainInit(root, false)
		if err != nil {
			t.Fatal(err)
		}
		ref := commitAll(t, raw)
		repo, err := Open(root)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := repo.ReproducibleAt(ref.String(), link); err != nil || !got {
			t.Fatalf("repository-bound reproducibility = %v, %v; want true", got, err)
		}
		if got, err := repo.ReproducibleAtWithin(ref.String(), link, moduleDir); err != nil || got {
			t.Fatalf("module-bound reproducibility = %v, %v; want false", got, err)
		}
	})

	t.Run("changed intermediate symlink", func(t *testing.T) {
		repo, _ := reproducibleFixture(t)
		first := t.TempDir()
		second := t.TempDir()
		writeFile(t, filepath.Join(first, "file"), "same", 0o644)
		writeFile(t, filepath.Join(second, "file"), "same", 0o644)

		intermediate := filepath.Join(repo.root, "dir")
		input := filepath.Join(repo.root, "input")
		if err := os.Symlink(first, intermediate); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := os.Symlink(filepath.Join("dir", "file"), input); err != nil {
			t.Fatal(err)
		}
		wt, err := repo.repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("dir"); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("input"); err != nil {
			t.Fatal(err)
		}
		ref, err := wt.Commit("links", &gogit.CommitOptions{Author: testSignature()})
		if err != nil {
			t.Fatal(err)
		}

		if err := os.Remove(intermediate); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(second, intermediate); err != nil {
			t.Fatal(err)
		}
		assertNotReproducible(t, repo, ref.String(), input)
	})
}

func TestReproducibleAtDirectoryTree(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "extra direct file",
			mutate: func(t *testing.T, path string) {
				writeFile(t, filepath.Join(path, "extra.txt"), "extra", 0o644)
			},
		},
		{
			name: "missing direct symlink",
			mutate: func(t *testing.T, path string) {
				if err := os.Remove(filepath.Join(path, "direct-link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "changed nested content",
			mutate: func(t *testing.T, path string) {
				writeFile(t, filepath.Join(path, "nested", "deep.txt"), "changed", 0o644)
			},
		},
		{
			name: "extra empty subdirectory",
			mutate: func(t *testing.T, path string) {
				if err := os.Mkdir(filepath.Join(path, "empty"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "subdirectory replaced by symlink",
			mutate: func(t *testing.T, path string) {
				if err := os.RemoveAll(filepath.Join(path, "nested")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(".", filepath.Join(path, "nested")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, ref := reproducibleFixture(t)
			path := filepath.Join(repo.root, "tree")
			test.mutate(t, path)
			assertNotReproducible(t, repo, ref, path)
		})
	}

	t.Run("directory permission is not Git state", func(t *testing.T) {
		repo, ref := reproducibleFixture(t)
		path := filepath.Join(repo.root, "tree")
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatal(err)
		}
		got, err := repo.ReproducibleAt(ref, path)
		if err != nil {
			t.Fatalf("ReproducibleAt: %v", err)
		}
		if !got {
			t.Error("ReproducibleAt = false after directory chmod, want true")
		}
	})
}

func TestReproducibleAtUnsupportedWorktreeObject(t *testing.T) {
	repo, ref := reproducibleFixture(t)
	path := filepath.Join(repo.root, "root.txt")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("Unix sockets unavailable: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	assertNotReproducible(t, repo, ref, path)
}

func TestReproducibleAtSubmodule(t *testing.T) {
	dir := t.TempDir()
	raw, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	writeFile(t, filepath.Join(dir, "seed.txt"), "seed", 0o644)
	base := commitAll(t, raw)

	idx, err := raw.Storer.Index()
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	entry := idx.Add("dependency")
	entry.Mode = filemode.Submodule
	entry.Hash = base
	if err := raw.Storer.SetIndex(idx); err != nil {
		t.Fatalf("set index: %v", err)
	}
	wt, err := raw.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	ref, err := wt.Commit("gitlink", &gogit.CommitOptions{Author: testSignature()})
	if err != nil {
		t.Fatalf("gitlink commit: %v", err)
	}
	path := filepath.Join(dir, "dependency")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	assertNotReproducible(t, repo, ref.String(), path)
	assertNotReproducible(t, repo, ref.String(), filepath.Join(path, "child"))
}

func TestReproducibleAtErrors(t *testing.T) {
	t.Run("bad ref even when path is absent", func(t *testing.T) {
		repo, _ := reproducibleFixture(t)
		if _, err := repo.ReproducibleAt("no-such-ref", filepath.Join(repo.root, "missing")); err == nil {
			t.Fatal("ReproducibleAt bad ref returned nil error")
		}
	})

	t.Run("path outside repository", func(t *testing.T) {
		repo, ref := reproducibleFixture(t)
		if _, err := repo.ReproducibleAt(ref, filepath.Join(t.TempDir(), "outside")); err == nil {
			t.Fatal("ReproducibleAt outside path returned nil error")
		}
	})

	t.Run("filesystem read", func(t *testing.T) {
		repo, ref := reproducibleFixture(t)
		if _, err := repo.ReproducibleAt(ref, filepath.Join(repo.root, "bad\x00name")); err == nil {
			t.Fatal("ReproducibleAt unreadable path returned nil error")
		}
	})

	t.Run("missing Git blob", func(t *testing.T) {
		repo, ref := reproducibleFixture(t)
		h, err := repo.repo.ResolveRevision("HEAD")
		if err != nil {
			t.Fatalf("resolve HEAD: %v", err)
		}
		commit, err := repo.repo.CommitObject(*h)
		if err != nil {
			t.Fatalf("commit: %v", err)
		}
		tree, err := commit.Tree()
		if err != nil {
			t.Fatalf("tree: %v", err)
		}
		entry, err := tree.FindEntry("root.txt")
		if err != nil {
			t.Fatalf("find root.txt: %v", err)
		}
		objectPath := filepath.Join(repo.root, ".git", "objects", entry.Hash.String()[:2], entry.Hash.String()[2:])
		if err := os.Remove(objectPath); err != nil {
			t.Fatalf("remove blob object: %v", err)
		}
		repo, err = Open(repo.root)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if _, err := repo.ReproducibleAt(ref, filepath.Join(repo.root, "root.txt")); err == nil {
			t.Fatal("ReproducibleAt missing blob returned nil error")
		}
	})
}

func reproducibleFixture(t *testing.T) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tree", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "root.txt"), "root\n", 0o644)
	writeFile(t, filepath.Join(dir, "run.sh"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(dir, "tree", "direct.txt"), "direct\n", 0o644)
	writeFile(t, filepath.Join(dir, "tree", "nested", "deep.txt"), "deep\n", 0o644)
	if err := os.Symlink("root.txt", filepath.Join(dir, "root-link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.Symlink("direct.txt", filepath.Join(dir, "tree", "direct-link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	raw, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	ref := commitAll(t, raw)
	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo, ref.String()
}

func assertNotReproducible(t *testing.T, repo *Repo, ref, path string) {
	t.Helper()
	got, err := repo.ReproducibleAt(ref, path)
	if err != nil {
		t.Fatalf("ReproducibleAt: %v", err)
	}
	if got {
		t.Error("ReproducibleAt = true, want false")
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func testSignature() *object.Signature {
	return &object.Signature{Name: "pew test", Email: "pew@example.invalid", When: time.Unix(1, 0)}
}
