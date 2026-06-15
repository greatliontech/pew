// Package store reads and writes benchmark recordings as canonical Go
// benchmark-format files (golang.org/x/perf/benchfmt) — one file per top-level
// benchmark, overwrite-in-place (spec §5, §6).
//
// It is provenance-agnostic: it faithfully round-trips whatever configuration
// lines the results carry (commit/toolchain/machine/buildconfig/pew-closure/…),
// which keeps every stored file a valid benchmark-format document readable by
// plain benchstat (INV-3) and the provenance recoverable (INV-4).
package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/perf/benchfmt"
)

// Store is a benchmark-recording directory — the configurable bench-dir (§6).
type Store struct {
	Root string
}

// New returns a Store rooted at dir (e.g. "./benchmarks").
func New(dir string) *Store { return &Store{Root: dir} }

// ErrNotRecorded is returned by Read when no recording exists for the benchmark.
var ErrNotRecorded = errors.New("benchmark not recorded")

// labelRe constrains variant labels to a safe filename component (no path
// separators, no traversal) since labels are user-supplied (§6, --label).
var labelRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// benchRe constrains a benchmark function name to the Go-identifier shape it
// always has, so it is a safe single path component.
var benchRe = regexp.MustCompile(`^Benchmark[A-Za-z0-9_]*$`)

// pkgSegRe constrains each segment of a module-relative package path (non-empty).
var pkgSegRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// validPkgRel reports whether p is a safe module-relative, slash-separated
// package path: not absolute, and no empty / "." / ".." segments — any of which
// would escape s.Root or collide under filepath.Join. The empty path (module
// root) is valid.
func validPkgRel(p string) bool {
	if p == "" {
		return true
	}
	if strings.HasPrefix(p, "/") {
		return false
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "." || seg == ".." || !pkgSegRe.MatchString(seg) {
			return false
		}
	}
	return true
}

// Path is the file path for a benchmark recording. pkgRel is the module-relative,
// slash-separated package path ("" for the module root); bench is the benchmark
// function name ("BenchmarkRun"); label is an optional variant discriminator (§6)
// or "".
func (s *Store) Path(pkgRel, bench, label string) (string, error) {
	if !validPkgRel(pkgRel) {
		return "", fmt.Errorf("store: invalid package path %q", pkgRel)
	}
	if !benchRe.MatchString(bench) {
		return "", fmt.Errorf("store: invalid benchmark name %q", bench)
	}
	name := bench
	if label != "" {
		if !labelRe.MatchString(label) {
			return "", fmt.Errorf("store: invalid label %q (want [A-Za-z0-9_-]+)", label)
		}
		name = bench + "." + label
	}
	return filepath.Join(s.Root, filepath.FromSlash(pkgRel), name+".txt"), nil
}

// Write overwrites the recording for (pkgRel, bench, label) with results, in
// canonical benchmark format. A recording always has at least one result, so an
// empty slice is rejected rather than clobbering a prior recording with an empty
// file. The write is atomic against a process crash (temp file + rename); it is
// not fsync-durable against power loss, which is acceptable since recordings are
// regenerable, git-committed artifacts (§6.1).
func (s *Store) Write(pkgRel, bench, label string, results []*benchfmt.Result) error {
	if len(results) == 0 {
		return fmt.Errorf("store: refusing to write empty recording for %s", bench)
	}
	path, err := s.Path(pkgRel, bench, label)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var buf bytes.Buffer
	w := benchfmt.NewWriter(&buf)
	for _, r := range results {
		if err := w.Write(r); err != nil {
			return fmt.Errorf("store: encode %s: %w", bench, err)
		}
	}

	tmp, err := os.CreateTemp(dir, ".pew-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Read parses the recording for (pkgRel, bench, label). The returned results are
// cloned and owned by the caller. Returns ErrNotRecorded if none exists.
func (s *Store) Read(pkgRel, bench, label string) ([]*benchfmt.Result, error) {
	path, err := s.Path(pkgRel, bench, label)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotRecorded
		}
		return nil, err
	}
	defer f.Close()
	return Parse(f, path)
}

// Parse reads canonical benchmark-format content into results, cloned and owned
// by the caller. name is purely diagnostic (used in error messages and the
// benchfmt position). It is used both for on-disk recordings (Read) and for blob
// content materialized from git at a ref (pew stat baselines, §6.1, §10). It
// rejects malformed input and unexpected record kinds rather than silently
// dropping data (INV-3), and rejects an empty recording.
func Parse(r io.Reader, name string) ([]*benchfmt.Result, error) {
	rd := benchfmt.NewReader(r, name)
	var out []*benchfmt.Result
	for rd.Scan() {
		switch rec := rd.Result().(type) {
		case *benchfmt.Result:
			out = append(out, rec.Clone())
		case *benchfmt.SyntaxError:
			return nil, fmt.Errorf("store: corrupt recording %s: %w", name, rec)
		default:
			// No silent drops: a record kind we don't round-trip is surfaced, not
			// discarded. The only such kind is Unit metadata, which `go test` does
			// not emit (even b.ReportMetric writes inline values, not Unit lines),
			// so pew-written files never contain it and this path is unreachable
			// for them — it stays as a guard against externally-edited files.
			return nil, fmt.Errorf("store: unexpected record %T in %s", rec, name)
		}
	}
	if err := rd.Err(); err != nil {
		return nil, fmt.Errorf("store: read %s: %w", name, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("store: empty recording %s", name)
	}
	return out, nil
}
