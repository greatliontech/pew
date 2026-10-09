package main

import (
	"path/filepath"
	"slices"
	"sync"

	gofresh "github.com/greatliontech/gofresh"
)

// vouchFileName is the reviewed standing vouch set's file, at the
// recording store's root beside the recordings it governs: one
// IMPORT-PATH:VARIABLE per line, `#` comments and blank lines ignored
// (spec §12, REQ-pew-vouch-source). The store walker ignores it (no
// .txt suffix), so it never reads as a recording.
const vouchFileName = "vouches"

// vouchSource belongs to one invocation; its store selection and flags cannot
// diverge from the memo that reads the standing acceptances.
type vouchSource struct {
	storeDir string
	flags    []string
	memo     sync.Map
}

func newVouchSource(storeDir string, entries []string) (*vouchSource, error) {
	flags, err := gofresh.ParseVouchEntries(entries)
	if err != nil {
		return nil, err
	}
	if storeDir != "" {
		storeDir, err = moduleBenchDir(storeDir, "")
		if err != nil {
			return nil, err
		}
	}
	return &vouchSource{storeDir: storeDir, flags: flags}, nil
}

// storeVouches reads the standing vouch set of the store governing
// moduleDir through the engine's own grammar (gofresh.ReadVouchFile —
// one IMPORT-PATH:VARIABLE per line, comments and blank lines ignored),
// memoized per store root: an absent file is the empty set, a malformed
// line refuses naming the file and line, exactly as a malformed --vouch
// does.
func (s *vouchSource) storeVouches(moduleDir string) ([]string, error) {
	dir, err := moduleBenchDir(s.storeDir, moduleDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, vouchFileName)
	read, _ := s.memo.LoadOrStore(path, sync.OnceValues(func() ([]string, error) { return gofresh.ReadVouchFile(path) }))
	return read.(func() ([]string, error))()
}

// engineVouches is the vouch set an engine over moduleDir judges under:
// the store's reviewed standing set extended by this invocation's
// --vouch flags — a flag adds an acceptance, never removes one, so the
// reviewed set is the floor every judged run shares and a one-off
// vouch rides on top (spec §12).
func (s *vouchSource) engineVouches(moduleDir string) ([]string, error) {
	standing, err := s.storeVouches(moduleDir)
	if err != nil {
		return nil, err
	}
	set := append(append([]string(nil), standing...), s.flags...)
	slices.Sort(set)
	return slices.Compact(set), nil
}
