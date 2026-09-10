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

// vouchStoreDir is the invocation's --bench-dir (empty selects each
// module's default store); every verb sets it from its flag before the
// first engine builds, so the store whose vouch file governs a module's
// engines is the store that module's recordings live in.
var vouchStoreDir string

var storeVouchMemo sync.Map // store root → []string or error

// storeVouches reads the standing vouch set of the store governing
// moduleDir through the engine's own grammar (gofresh.ReadVouchFile —
// one IMPORT-PATH:VARIABLE per line, comments and blank lines ignored),
// memoized per store root: an absent file is the empty set, a malformed
// line refuses naming the file and line, exactly as a malformed --vouch
// does.
func storeVouches(moduleDir string) ([]string, error) {
	dir, err := moduleBenchDir(vouchStoreDir, moduleDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, vouchFileName)
	if cached, ok := storeVouchMemo.Load(path); ok {
		switch v := cached.(type) {
		case error:
			return nil, v
		case []string:
			return v, nil
		}
	}
	identities, err := gofresh.ReadVouchFile(path)
	if err != nil {
		storeVouchMemo.Store(path, err)
		return nil, err
	}
	storeVouchMemo.Store(path, identities)
	return identities, nil
}

// engineVouches is the vouch set an engine over moduleDir judges under:
// the store's reviewed standing set extended by this invocation's
// --vouch flags — a flag adds an acceptance, never removes one, so the
// reviewed set is the floor every judged run shares and a one-off
// vouch rides on top (spec §12).
func engineVouches(moduleDir string) ([]string, error) {
	standing, err := storeVouches(moduleDir)
	if err != nil {
		return nil, err
	}
	set := append(append([]string(nil), standing...), dynamicStateVouches...)
	slices.Sort(set)
	return slices.Compact(set), nil
}
