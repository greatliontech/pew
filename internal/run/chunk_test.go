package run

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"golang.org/x/perf/benchfmt"
)

// The file encoding of a chunked row: every part at most ChunkBound
// bytes, the row's own line first and `<key>.N` in order, and the join
// the exact inverse over random lengths — zero, one byte, exact
// multiples, one past — while a non-chunked row never splits and every
// continuation spelling is its row for the closed-set check.
func TestChunkedRowsSplitAndJoin(t *testing.T) {
	rng := rand.New(rand.NewSource(230))
	lengths := []int{0, 1, ChunkBound - 1, ChunkBound, ChunkBound + 1, 2 * ChunkBound, 3*ChunkBound + 7}
	for i := 0; i < 20; i++ {
		lengths = append(lengths, rng.Intn(6*ChunkBound))
	}
	for _, n := range lengths {
		value := make([]byte, n)
		for j := range value {
			value[j] = byte('a' + rng.Intn(26))
		}
		cfgs := []benchfmt.Config{
			{Key: "commit", Value: []byte("abc"), File: true},
			{Key: KeyRuntimeInputs.Name, Value: value, File: true},
			{Key: KeyClosure.Name, Value: bytes.Repeat([]byte("z"), 2*ChunkBound), File: true},
		}
		split := SplitChunked(cfgs)
		want := max(1, (n+ChunkBound-1)/ChunkBound)
		parts := 0
		for k, c := range split {
			if len(c.Value) > ChunkBound && c.Key != KeyClosure.Name {
				t.Fatalf("n=%d: part %d is %d bytes", n, k, len(c.Value))
			}
			if c.Key == KeyRuntimeInputs.Name || strings.HasPrefix(c.Key, KeyRuntimeInputs.Name+".") {
				parts++
				if !IsRecordingKey(c.Key) {
					t.Fatalf("n=%d: continuation %q is not its row", n, c.Key)
				}
			}
		}
		if parts != want {
			t.Fatalf("n=%d: %d parts, want %d", n, parts, want)
		}
		if split[2].Key != KeyClosure.Name && parts == 1 {
			t.Fatalf("n=%d: the non-chunked row moved: %+v", n, split)
		}
		joined, err := JoinChunked(split)
		if err != nil {
			t.Fatalf("n=%d: join: %v", n, err)
		}
		if len(joined) != 3 || joined[1].Key != KeyRuntimeInputs.Name || !bytes.Equal(joined[1].Value, value) || !bytes.Equal(joined[2].Value, cfgs[2].Value) {
			t.Fatalf("n=%d: join is not the inverse: %d rows, value %d bytes", n, len(joined), len(joined[1].Value))
		}
	}
	// The refusals: a gap, a continuation without its row (a repeated
	// spelling is the store's raw format check's: stale (format)).
	row := func(key, v string) benchfmt.Config { return benchfmt.Config{Key: key, Value: []byte(v), File: true} }
	for name, cfgs := range map[string][]benchfmt.Config{
		"gap":    {row(KeyRuntimeInputs.Name, "a"), row(KeyRuntimeInputs.Name+".3", "c")},
		"orphan": {row(KeyRuntimeInputs.Name+".2", "b")},
	} {
		if _, err := JoinChunked(cfgs); err == nil {
			t.Fatalf("%s: joined without refusing", name)
		}
	}
	// Continuation spellings that are not the grammar are no key.
	for _, bad := range []string{KeyRuntimeInputs.Name + ".1", KeyRuntimeInputs.Name + ".02", KeyRuntimeInputs.Name + ".x", KeyClosure.Name + ".2", "pew-future.2"} {
		if IsRecordingKey(bad) {
			t.Fatalf("%q admitted as a recording key", bad)
		}
	}
}
