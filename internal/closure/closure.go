// Package closure computes the pew-closure hash (spec §7): a sound fingerprint
// of the source a benchmark's package transitively depends on.
//
// This is the Tier-1 (package-granularity) implementation — over-approximate but
// never false-valid (§7.2). Chunk 7 swaps in Tier-2 RTA precision behind the same
// Hash API. Classification per §7.1/§7.7: stdlib is cut (toolchain guard);
// module-cache deps are pinned by their immutable modpath@version; mutable-local
// packages (main module, local replace, workspace, vendor) are hashed by content.
package closure

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thegrumpylion/pew/internal/gotool"
)

// Hasher computes closure hashes. New resolves GOMODCACHE once for the
// cache-vs-mutable classification.
type Hasher struct {
	modCache string
}

func New() (*Hasher, error) {
	out, err := gotool.Run("env", "GOMODCACHE")
	if err != nil {
		return nil, err
	}
	mc := strings.TrimSpace(string(out))
	if mc == "" {
		return nil, errors.New("closure: empty GOMODCACHE")
	}
	return &Hasher{modCache: filepath.Clean(mc)}, nil
}

type listPkg struct {
	ImportPath   string
	Standard     bool
	Dir          string
	GoFiles      []string
	CgoFiles     []string
	CFiles       []string
	CXXFiles     []string
	MFiles       []string
	HFiles       []string
	FFiles       []string
	SFiles       []string
	SwigFiles    []string
	SwigCXXFiles []string
	SysoFiles    []string
	EmbedFiles   []string
	Module       *listMod
	Error        *listErr
}

type listMod struct {
	Path    string
	Version string
	Dir     string
	Main    bool
}

type listErr struct {
	Err string
}

// sourceFiles is every compiled/linked input of the package: a change to any of
// these can move the benchmark's behavior, so all must be hashed (INV-1). Keep
// this in lockstep with go list's file-kind fields (TestSourceFilesComplete).
func (p listPkg) sourceFiles() []string {
	var f []string
	for _, set := range [][]string{
		p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles,
		p.SFiles, p.SwigFiles, p.SwigCXXFiles, p.SysoFiles, p.EmbedFiles,
	} {
		f = append(f, set...)
	}
	return f
}

// Hash returns the Tier-1 closure hash for the test binary of pkgPath.
func (h *Hasher) Hash(pkgPath string) (string, error) {
	pkgs, err := h.list(pkgPath)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	var contribs []string
	for _, p := range pkgs {
		c, err := h.contribution(p)
		if err != nil {
			return "", err
		}
		if c != "" && !seen[c] {
			seen[c] = true
			contribs = append(contribs, c)
		}
	}
	if len(contribs) == 0 {
		return "", fmt.Errorf("closure: %s reaches no non-stdlib source", pkgPath)
	}
	sort.Strings(contribs)
	sum := sha256.Sum256([]byte(strings.Join(contribs, "\n")))
	return hex.EncodeToString(sum[:])[:16], nil
}

// contribution returns this package's contribution to the closure, or "" if it
// is excluded (stdlib, a pseudo-package, or the synthesized test-main).
func (h *Hasher) contribution(p listPkg) (string, error) {
	if p.Standard || p.Module == nil || strings.HasSuffix(p.ImportPath, ".test") {
		// stdlib cut (§7.1); pseudo-package ("C", whose C source rides in the
		// importing package); or the toolchain-generated test main (boilerplate
		// in a transient dir — deterministic, carries no source information).
		return "", nil
	}
	if !p.Module.Main && h.underCache(p.Dir) {
		// Immutable, version-locked cache dep (classified on the package Dir per
		// §7.7): pin by the module's content dir (modpath@version, replace-correct
		// via Module.Dir), never read its source. p.Dir and Module.Dir agree on
		// under-cache classification for every reachable config; §7.7 names the
		// package Dir, so we use it.
		rel := strings.TrimPrefix(filepath.Clean(p.Module.Dir), h.modCache+string(filepath.Separator))
		return "cache:" + p.ImportPath + "=" + filepath.ToSlash(rel), nil
	}
	// Mutable-local (main module, local replace, workspace, vendor): hash content
	// so a silent edit moves the hash (INV-8).
	fh, err := hashFiles(p.Dir, p.sourceFiles())
	if err != nil {
		return "", err
	}
	return "src:" + p.ImportPath + "=" + fh, nil
}

// underCache reports whether dir is inside the module cache (a path segment
// boundary, so "/mod" does not match "/modificator").
func (h *Hasher) underCache(dir string) bool {
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	return dir == h.modCache || strings.HasPrefix(dir, h.modCache+string(filepath.Separator))
}

func hashFiles(dir string, files []string) (string, error) {
	sort.Strings(files)
	hasher := sha256.New()
	for _, f := range files {
		path := filepath.Join(dir, f)
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("closure: read %s: %w", path, err)
		}
		fmt.Fprintf(hasher, "%s\x00%x\n", f, sha256.Sum256(content))
	}
	return hex.EncodeToString(hasher.Sum(nil))[:16], nil
}

func (h *Hasher) list(pkgPath string) ([]listPkg, error) {
	out, err := gotool.Run("list", "-json", "-deps", "-test", pkgPath)
	if err != nil {
		return nil, err
	}
	return parseList(bytes.NewReader(out))
}

func parseList(r io.Reader) ([]listPkg, error) {
	dec := json.NewDecoder(r)
	var pkgs []listPkg
	for dec.More() {
		var p listPkg
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("closure: decode go list: %w", err)
		}
		if p.Error != nil {
			// go list -deps -test exits 0 but reports an unloadable package via
			// its Error field. Hashing the surviving packages would silently
			// under-cover the closure → false-valid. Fail loud (INV-1).
			return nil, fmt.Errorf("closure: package %s failed to load: %s", p.ImportPath, p.Error.Err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}
