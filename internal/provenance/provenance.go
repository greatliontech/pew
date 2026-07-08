// Package provenance captures the run-context facts pew records in-band with
// each benchmark recording and uses as staleness guards (spec §5, §8): the
// measured commit and dirty flag, the toolchain identity, a machine fingerprint,
// and a build-config digest.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/thegrumpylion/pew/internal/gotool"
	"golang.org/x/perf/benchfmt"
)

// Provenance is the set of facts about how a recording was produced. commit and
// dirty are not derivable from the recording's later git position (§6.1); the
// other three are exact-equality staleness guards (§7). pew-closure and
// pew-runtime are added separately by their guard builders.
type Provenance struct {
	Commit      string // SHA of the code measured (not the recording's git commit, §6.1)
	Dirty       bool   // working tree had uncommitted changes at run
	Toolchain   string // `go version` identity (toolchain guard)
	Machine     string // machine fingerprint, §8 (machine guard)
	BuildConfig string // build-settings digest (buildconfig guard)
}

// Config returns the in-band provenance lines in spec §5 order. File is set so
// benchfmt.Writer emits them as `key: value` lines (it omits File==false config
// as internal).
func (p Provenance) Config() []benchfmt.Config {
	return []benchfmt.Config{
		{Key: "commit", Value: []byte(p.Commit), File: true},
		{Key: "toolchain", Value: []byte(p.Toolchain), File: true},
		{Key: "machine", Value: []byte(p.Machine), File: true},
		{Key: "buildconfig", Value: []byte(p.BuildConfig), File: true},
		{Key: "dirty", Value: []byte(strconv.FormatBool(p.Dirty)), File: true},
	}
}

// Capture gathers provenance for a run whose code lives at moduleDir. It opens
// the git repo there (pew is a pure reader) and queries the go toolchain.
func Capture(moduleDir string) (Provenance, error) {
	commit, dirty, err := gitState(moduleDir)
	if err != nil {
		return Provenance{}, err
	}
	// Resolve toolchain/buildconfig in moduleDir — the same dir `go test` runs in
	// (run.Execute) — so they describe the toolchain that actually builds the
	// benchmark, even under a go.mod toolchain directive.
	tc, err := toolchain(moduleDir)
	if err != nil {
		return Provenance{}, err
	}
	bc, err := buildConfig(moduleDir)
	if err != nil {
		return Provenance{}, err
	}
	facts, err := gatherFacts()
	if err != nil {
		return Provenance{}, err
	}
	return Provenance{
		Commit:      commit,
		Dirty:       dirty,
		Toolchain:   tc,
		Machine:     facts.Fingerprint(),
		BuildConfig: bc,
	}, nil
}

func gitState(dir string) (commit string, dirty bool, err error) {
	repo, err := gogit.PlainOpenWithOptions(dir, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", false, fmt.Errorf("provenance: open git repo at %s: %w", dir, err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", false, fmt.Errorf("provenance: resolve HEAD: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", false, fmt.Errorf("provenance: worktree: %w", err)
	}
	st, err := wt.Status()
	if err != nil {
		return "", false, fmt.Errorf("provenance: worktree status: %w", err)
	}
	return head.Hash().String(), !st.IsClean(), nil
}

// toolchain is the `go version` identity, minus the redundant leading prefix —
// e.g. "go1.26.4 linux/amd64" (incl. any custom/experiment suffix, which affects
// codegen and so must be part of the guard).
func toolchain(dir string) (string, error) {
	out, err := gotool.RunIn(dir, "version")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "go version "), nil
}

// buildConfig digests the build-affecting settings that can change generated code
// without moving the toolchain, machine, or source guards (spec §7 guard 5): the
// codegen feature level, the cgo toolchain environment (compilers plus cgo and
// pkg-config flags), and GOFLAGS. Host GOOS/GOARCH are deliberately excluded — they
// ride the machine guard (§8). Values are hashed directly, so any change moves the
// digest.
//
// Not yet digested: PGO profile content, and build-affecting CLI pass-throughs
// (e.g. -tags/-gcflags supplied outside GOFLAGS).
// buildConfigGoEnvKeys are the go-env-reported build-affecting settings hashed into
// the buildconfig guard: codegen feature level, cgo toolchain environment, and build
// flags. Host GOOS/GOARCH are deliberately absent — they ride the machine guard (§8).
var buildConfigGoEnvKeys = []string{
	"GOAMD64", "GOARM", "GOARM64", "GO386", "GOEXPERIMENT",
	"CGO_ENABLED", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_FFLAGS", "CGO_LDFLAGS",
	"CC", "CXX", "PKG_CONFIG", "GOFLAGS",
}

// buildConfigOSEnvKeys are the pkg-config search variables — plain OS env, not go env
// vars — that change which .pc files cgo resolves and thus the compiled code.
var buildConfigOSEnvKeys = []string{"PKG_CONFIG_PATH", "PKG_CONFIG_LIBDIR", "PKG_CONFIG_SYSROOT_DIR"}

func buildConfig(dir string) (string, error) {
	out, err := gotool.RunIn(dir, "env", "-json")
	if err != nil {
		return "", err
	}
	var env map[string]string
	if err := json.Unmarshal(out, &env); err != nil {
		return "", fmt.Errorf("provenance: parse go env: %w", err)
	}
	// Effective build settings `go test` uses, as reported by `go env` (which merges
	// go env defaults with OS-env overrides), plus the OS-env pkg-config search vars
	// (pew's process env is the one `go test` inherits).
	vals := map[string]string{}
	for _, k := range buildConfigGoEnvKeys {
		vals[k] = env[k]
	}
	for _, k := range buildConfigOSEnvKeys {
		vals[k] = os.Getenv(k)
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, vals[k])
	}
	return digest(b.String()), nil
}

// digest is a short stable content hash used for the machine and buildconfig
// fingerprints.
func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:32]
}
