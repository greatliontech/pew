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

// WriteRequest is one recording in an atomic package write.
type WriteRequest struct {
	PkgRel  string
	Bench   string
	Label   string
	Results []*benchfmt.Result
}

// Recording is one benchmark recording present in a Store.
type Recording struct {
	PkgRel string
	Bench  string
	Label  string
	Path   string
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
	if err := s.ensureDir(dir); err != nil {
		return err
	}
	var buf bytes.Buffer
	w := benchfmt.NewWriter(&buf)
	for _, result := range results {
		if err := w.Write(result); err != nil {
			return fmt.Errorf("store: encode %s: %w", bench, err)
		}
	}
	temp, err := os.CreateTemp(dir, ".pew-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(buf.Bytes()); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

// WriteBatch replaces a set of recordings transactionally for every returned
// error. Every destination is encoded and staged before any existing recording
// moves; a commit failure restores the complete prior set. Sudden process death may
// leave canonical paths absent, which is safe because absence reruns regenerable
// recordings; it never exposes a torn file as a recording.
func (s *Store) WriteBatch(requests []WriteRequest) error {
	return s.writeBatch(requests, nil)
}

func (s *Store) writeBatch(requests []WriteRequest, beforeInstall func()) error {
	type stagedWrite struct {
		path, temp, backup string
		existed            bool
		installed          bool
	}
	staged := make([]stagedWrite, 0, len(requests))
	destinations := make(map[string]bool, len(requests))
	cleanupTemps := func() {
		for _, item := range staged {
			if item.temp != "" {
				_ = os.Remove(item.temp)
			}
		}
	}
	for _, request := range requests {
		if len(request.Results) == 0 {
			cleanupTemps()
			return fmt.Errorf("store: refusing to write empty recording for %s", request.Bench)
		}
		path, err := s.Path(request.PkgRel, request.Bench, request.Label)
		if err != nil {
			cleanupTemps()
			return err
		}
		if destinations[path] {
			cleanupTemps()
			return fmt.Errorf("store: duplicate recording destination: %s", path)
		}
		destinations[path] = true
		if info, err := os.Lstat(path); err == nil {
			if !info.Mode().IsRegular() {
				cleanupTemps()
				return fmt.Errorf("store: recording destination is not a regular file: %s", path)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			cleanupTemps()
			return err
		}
		dir := filepath.Dir(path)
		if err := s.ensureDir(dir); err != nil {
			cleanupTemps()
			return err
		}
		var buf bytes.Buffer
		writer := benchfmt.NewWriter(&buf)
		for _, result := range request.Results {
			if err := writer.Write(result); err != nil {
				cleanupTemps()
				return fmt.Errorf("store: encode %s: %w", request.Bench, err)
			}
		}
		temp, err := os.CreateTemp(dir, ".pew-*.tmp")
		if err != nil {
			cleanupTemps()
			return err
		}
		name := temp.Name()
		if _, err := temp.Write(buf.Bytes()); err != nil {
			temp.Close()
			_ = os.Remove(name)
			cleanupTemps()
			return err
		}
		if err := temp.Close(); err != nil {
			_ = os.Remove(name)
			cleanupTemps()
			return err
		}
		_, err = os.Lstat(path)
		staged = append(staged, stagedWrite{path: path, temp: name, existed: err == nil})
	}

	rollback := func() error {
		var errs []error
		for i := len(staged) - 1; i >= 0; i-- {
			item := &staged[i]
			if item.installed {
				if err := os.Remove(item.path); err != nil && !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
				}
			}
			if item.backup != "" {
				if err := os.Rename(item.backup, item.path); err != nil {
					errs = append(errs, err)
				}
			}
		}
		cleanupTemps()
		return errors.Join(errs...)
	}
	for i := range staged {
		item := &staged[i]
		if !item.existed {
			continue
		}
		backup, err := os.CreateTemp(filepath.Dir(item.path), ".pew-backup-*.tmp")
		if err != nil {
			return errors.Join(err, rollback())
		}
		backupName := backup.Name()
		if err := backup.Close(); err != nil {
			_ = os.Remove(backupName)
			return errors.Join(err, rollback())
		}
		if err := os.Remove(backupName); err != nil {
			return errors.Join(err, rollback())
		}
		if err := os.Rename(item.path, backupName); err != nil {
			return errors.Join(err, rollback())
		}
		item.backup = backupName
	}
	if beforeInstall != nil {
		beforeInstall()
	}
	for i := range staged {
		item := &staged[i]
		if err := os.Rename(item.temp, item.path); err != nil {
			return errors.Join(err, rollback())
		}
		item.temp = ""
		item.installed = true
	}
	for _, item := range staged {
		if item.backup != "" {
			// The new set is already committed. Cleanup residue is ignored rather
			// than reporting a failure that cannot restore the prior set.
			_ = os.Remove(item.backup)
		}
	}
	return nil
}

// Read parses the recording for (pkgRel, bench, label). The returned results are
// cloned and owned by the caller. Returns ErrNotRecorded if none exists.
func (s *Store) Read(pkgRel, bench, label string) ([]*benchfmt.Result, error) {
	path, err := s.Path(pkgRel, bench, label)
	if err != nil {
		return nil, err
	}
	if err := s.checkParentDirs(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotRecorded
		}
		return nil, err
	}
	if err := checkRegularFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotRecorded
		}
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

// List returns the safely-addressable benchmark recordings currently present in
// the store. Files that do not match pew's storage layout are ignored: gc removes
// stored results, not arbitrary user files that happen to live under bench-dir.
func (s *Store) List() ([]Recording, error) {
	var out []Recording
	root, err := s.checkedRoot()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".txt" {
			return nil
		}
		r, ok := s.recordingFromPath(path)
		if ok {
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// KeyFromPath converts a path in this store's layout to its recording key without
// reading from disk. It is used for historical git blobs whose paths are known but
// whose files may not exist in the current worktree.
func (s *Store) KeyFromPath(path string) (Recording, bool) {
	if filepath.Ext(path) != ".txt" {
		return Recording{}, false
	}
	root := filepath.Clean(s.Root)
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return Recording{}, false
	}
	pkgRel := filepath.ToSlash(filepath.Dir(rel))
	if pkgRel == "." {
		pkgRel = ""
	}
	if !validPkgRel(pkgRel) {
		return Recording{}, false
	}
	base := strings.TrimSuffix(filepath.Base(path), ".txt")
	bench, label, _ := strings.Cut(base, ".")
	if !benchRe.MatchString(bench) {
		return Recording{}, false
	}
	if label != "" && !labelRe.MatchString(label) {
		return Recording{}, false
	}
	want, err := s.Path(pkgRel, bench, label)
	if err != nil || filepath.Clean(want) != filepath.Clean(path) {
		return Recording{}, false
	}
	return Recording{PkgRel: pkgRel, Bench: bench, Label: label, Path: path}, true
}

func (s *Store) recordingFromPath(path string) (Recording, bool) {
	r, ok := s.KeyFromPath(path)
	if !ok {
		return Recording{}, false
	}
	if err := s.checkParentDirs(path); err != nil {
		return Recording{}, false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Recording{}, false
	}
	if !isPewRecording(path) {
		return Recording{}, false
	}
	return r, true
}

func isPewRecording(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	recs, err := Parse(f, path)
	if err != nil || len(recs) == 0 {
		return false
	}
	return IsRecording(recs)
}

// IsRecording reports whether parsed results carry pew's required provenance and
// guard keys. It lets callers distinguish pew recordings from arbitrary benchmark
// files that happen to match the storage path shape.
func IsRecording(recs []*benchfmt.Result) bool {
	if len(recs) == 0 {
		return false
	}
	cfg := map[string]bool{}
	for _, c := range recs[0].Config {
		cfg[c.Key] = true
	}
	for _, key := range []string{"commit", "toolchain", "machine", "buildconfig", "dirty", "pew-closure", "pew-runtime", "pew-runtime-inputs"} {
		if !cfg[key] {
			return false
		}
	}
	return true
}

// Remove deletes a recording and prunes empty package directories up to the store
// root. The path is recomputed from the recording key rather than trusted.
func (s *Store) Remove(r Recording) error {
	path, err := s.Path(r.PkgRel, r.Bench, r.Label)
	if err != nil {
		return err
	}
	if err := s.checkParentDirs(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := checkRegularFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return s.pruneEmptyDirs(filepath.Dir(path))
}

func (s *Store) pruneEmptyDirs(dir string) error {
	root := filepath.Clean(s.Root)
	for dir = filepath.Clean(dir); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if err := checkDirNoSymlink(dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *Store) ensureDir(dir string) error {
	root := filepath.Clean(s.Root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if err := checkDirNoSymlink(root); err != nil {
		return err
	}
	rel, err := safeRel(root, dir)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	cur := root
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("store: symlink path component %s", cur)
		}
		if !info.IsDir() {
			return fmt.Errorf("store: path component is not a directory: %s", cur)
		}
	}
	return nil
}

func (s *Store) checkedRoot() (string, error) {
	root := filepath.Clean(s.Root)
	return root, checkDirNoSymlink(root)
}

func (s *Store) checkParentDirs(path string) error {
	root := filepath.Clean(s.Root)
	if err := checkDirNoSymlink(root); err != nil {
		return err
	}
	rel, err := safeRel(root, filepath.Dir(path))
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	cur := root
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, seg)
		if err := checkDirNoSymlink(cur); err != nil {
			return err
		}
	}
	return nil
}

func safeRel(root, path string) (string, error) {
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("store: path escapes root: %s", path)
	}
	return rel, nil
}

func checkDirNoSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("store: symlink path component %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("store: path component is not a directory: %s", path)
	}
	return nil
}

func checkRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("store: recording is a symlink: %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("store: recording is not a regular file: %s", path)
	}
	return nil
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
