package run

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/perf/benchfmt"
)

// RecordingKeyClass is a recording line's presence rule (spec §5's
// `class` column).
type RecordingKeyClass int

const (
	// KeyDiscriminator is the format line: judged by the format check
	// alone — exactly once, byte-exact — never by the shape check, which
	// separates a stale pre-format recording from a current one without
	// interpreting either.
	KeyDiscriminator RecordingKeyClass = iota
	// KeyMandatory lines a current-shape recording always carries; a
	// current-format recording missing one is `stale (format)`.
	KeyMandatory
	// KeyOmittable lines are emitted exactly when non-empty, or are
	// absent from recordings predating them; absence is an encoding
	// (no attribution, no vouch, the empty strategy), never a shape
	// fault.
	KeyOmittable
)

// RecordingKey is one row of spec §5's key table: the spelling, its
// presence class, whether two sides differing in it compare with a
// note rather than grouping apart (audit provenance, §10.1), whether
// it is one of the four comparison guards two sides must agree on
// (§10.1's guard set), and the name every face renders the line by
// (the table's display column).
type RecordingKey struct {
	Name    string
	Class   RecordingKeyClass
	Audit   bool
	Guard   bool
	Display string
	// Chunked marks a row whose value grows with the package (a
	// manifest, a ledger) and is therefore written as continuation
	// lines of at most ChunkBound bytes each — `<name>: <part 1>`,
	// `<name>.2: <part 2>`, … — so no stored line exceeds benchfmt's
	// scanner bound (spec §5, REQ-pew-artifact-format). The value is
	// one in memory; SplitChunked and JoinChunked are the file
	// encoding's two ends.
	Chunked bool
}

// Config is the line as a recording emits it: File:true so
// benchfmt.Writer writes it as `key: value` (it omits File==false
// config as internal).
func (k RecordingKey) Config(value string) benchfmt.Config {
	return benchfmt.Config{Key: k.Name, Value: []byte(value), File: true}
}

// RecordingKeyNamespace is the prefix of every key pew itself mints: a
// stream line in it is refused before storage (spec §5), and a file
// carrying one is pew-marked whatever its era.
const RecordingKeyNamespace = "pew-"

// FormatInvalidAnnotation is the reader-side annotation a store's parse
// attaches (File:false, never written) to a recording whose format
// discriminator failed the byte-exact check — an annotation about the
// file, not a recording key, so it is no row and never pew-marks a
// file.
const FormatInvalidAnnotation = "pew-format-invalid"

// RecordingKeys is spec §5's key table, row for row and in its order —
// the one registry of every provenance, guard, derived, and purity
// line a pew recording may carry beyond the toolchain benchmark keys.
// Every producer line is constructed from a row, and the store's
// shape and closed-set checks, admission, compare's grouping
// projection and audit notes, and the stream's reserved-key refusal
// derive from the rows, so a key or a mark spelled anywhere else is
// unrepresentable; TestRecordingKeysMirrorSpec binds the rows to the
// table, marks included.
var RecordingKeys = []RecordingKey{
	{Name: "pew-format", Class: KeyDiscriminator, Display: "format"},
	{Name: "commit", Class: KeyMandatory, Display: "commit"},
	{Name: "toolchain", Class: KeyMandatory, Guard: true, Display: "toolchain"},
	{Name: "machine", Class: KeyMandatory, Guard: true, Display: "machine"},
	{Name: "buildconfig", Class: KeyMandatory, Guard: true, Display: "buildconfig"},
	{Name: "runtimeconfig", Class: KeyMandatory, Guard: true, Display: "runtimeconfig"},
	{Name: "dirty", Class: KeyMandatory, Display: "dirty"},
	{Name: "pew-runconditions", Class: KeyMandatory, Audit: true, Display: "run conditions"},
	{Name: "pew-runtime", Class: KeyMandatory, Display: "runtime"},
	{Name: "pew-runtime-inputs", Class: KeyMandatory, Display: "runtime inputs", Chunked: true},
	{Name: "pew-purity", Class: KeyOmittable, Display: "purity"},
	{Name: "pew-vouches", Class: KeyOmittable, Audit: true, Display: "dynamic-state vouches"},
	{Name: "pew-dynamic-state", Class: KeyOmittable, Audit: true, Display: "dynamic-state strategies"},
	{Name: "pew-single-subject-discharges", Class: KeyOmittable, Audit: true, Display: "single-subject discharges"},
	{Name: "pew-package-process-discharges", Class: KeyOmittable, Audit: true, Display: "package-process discharges"},
	{Name: "pew-closure", Class: KeyMandatory, Display: "closure"},
	{Name: "pew-closure-strategy", Class: KeyOmittable, Audit: true, Display: "closure derivations"},
	{Name: "pew-test-variants", Class: KeyMandatory, Display: "test-variants"},
	{Name: "pew-test-variant-ledger", Class: KeyMandatory, Display: "test-variant ledger", Chunked: true},
}

// The rows by name — views over RecordingKeys, never a second
// definition: a name absent from the table refuses at init.
var (
	KeyFormat                   = registered("pew-format")
	KeyCommit                   = registered("commit")
	KeyToolchain                = registered("toolchain")
	KeyMachine                  = registered("machine")
	KeyBuildConfig              = registered("buildconfig")
	KeyRuntimeConfig            = registered("runtimeconfig")
	KeyDirty                    = registered("dirty")
	KeyRunConditions            = registered("pew-runconditions")
	KeyRuntime                  = registered("pew-runtime")
	KeyRuntimeInputs            = registered("pew-runtime-inputs")
	KeyPurity                   = registered("pew-purity")
	KeyVouches                  = registered("pew-vouches")
	KeyDynamicState             = registered("pew-dynamic-state")
	KeySingleSubjectDischarges  = registered("pew-single-subject-discharges")
	KeyPackageProcessDischarges = registered("pew-package-process-discharges")
	KeyClosure                  = registered("pew-closure")
	KeyClosureStrategy          = registered("pew-closure-strategy")
	KeyTestVariants             = registered("pew-test-variants")
	KeyTestVariantLedger        = registered("pew-test-variant-ledger")
)

func registered(name string) RecordingKey {
	for _, k := range RecordingKeys {
		if k.Name == name {
			return k
		}
	}
	panic("run: recording key " + name + " is not a row of RecordingKeys")
}

// RecordingConfigKeys is the registry's spellings in table order — the
// closed key set every consumer projects from.
var RecordingConfigKeys = func() []string {
	names := make([]string, len(RecordingKeys))
	for i, k := range RecordingKeys {
		names[i] = k.Name
	}
	return names
}()

var recordingKeyIndex = func() map[string]RecordingKey {
	m := make(map[string]RecordingKey, len(RecordingKeys))
	for _, k := range RecordingKeys {
		if _, dup := m[k.Name]; dup {
			panic("run: recording key " + k.Name + " listed twice")
		}
		m[k.Name] = k
	}
	return m
}()

// IsRecordingKey reports whether name is a row of the registry — the
// closed set the store enforces, admission reads, and the stream may
// never define (spec §5: refused before storage) — a chunked row's
// continuation spelling included: it is that row on the wire. Of the
// readers, only the store's raw-byte format check meets a continuation
// spelling (the others read config the store's parse has already
// rejoined; the stream's refusal fires on the `pew-` prefix).
func IsRecordingKey(name string) bool {
	if _, ok := recordingKeyIndex[name]; ok {
		return true
	}
	_, _, ok := ChunkOf(name)
	return ok
}

// IsChunkedRow reports whether name is a chunked row's own name.
func IsChunkedRow(name string) bool {
	k, ok := recordingKeyIndex[name]
	return ok && k.Chunked
}

// ChunkBound is the most value bytes one stored line of a chunked row
// carries: half of benchfmt's 64 KiB scanner bound, so a line never
// rides the reader's cliff (REQ-pew-artifact-format).
const ChunkBound = 32 << 10

// chunkName is the continuation spelling of a chunked row's part i
// (i ≥ 2); part 1 is the row's own name.
func (k RecordingKey) chunkName(i int) string {
	if i == 1 {
		return k.Name
	}
	return k.Name + "." + strconv.Itoa(i)
}

// ChunkOf resolves a continuation spelling to its chunked row and part
// index (≥ 2); a row's own name is part 1 and resolves through the
// index, not here.
func ChunkOf(name string) (RecordingKey, int, bool) {
	dot := strings.LastIndexByte(name, '.')
	if dot < 0 {
		return RecordingKey{}, 0, false
	}
	k, ok := recordingKeyIndex[name[:dot]]
	if !ok || !k.Chunked {
		return RecordingKey{}, 0, false
	}
	i, err := strconv.Atoi(name[dot+1:])
	if err != nil || i < 2 || strconv.Itoa(i) != name[dot+1:] {
		return RecordingKey{}, 0, false
	}
	return k, i, true
}

// SplitChunked is the file encoding of one result's config: every
// chunked row's value becomes continuation lines of at most ChunkBound
// bytes, in order; every other line passes through unchanged. An empty
// chunked value stays one empty line (the row's presence is the
// encoding).
func SplitChunked(cfgs []benchfmt.Config) []benchfmt.Config {
	var out []benchfmt.Config
	for _, c := range cfgs {
		k, ok := recordingKeyIndex[c.Key]
		if !ok || !k.Chunked || len(c.Value) <= ChunkBound {
			out = append(out, c)
			continue
		}
		for i, rest := 1, c.Value; len(rest) > 0; i++ {
			n := min(len(rest), ChunkBound)
			out = append(out, benchfmt.Config{Key: k.chunkName(i), Value: append([]byte(nil), rest[:n]...), File: c.File})
			rest = rest[n:]
		}
	}
	return out
}

// JoinChunked is the file encoding's other end: every chunked row's
// continuation lines rejoin, in index order, onto the row's own line,
// which keeps its position; a continuation whose row line is absent or
// a gap in the indexes is a corrupt recording and refuses — the value
// must be exactly what the writer split. A repeated spelling never
// reaches here as two entries: benchfmt's reader keeps one per key,
// and the store's raw format check marks the file stale (format) under
// §5's duplicate rule.
func JoinChunked(cfgs []benchfmt.Config) ([]benchfmt.Config, error) {
	parts := map[string]map[int][]byte{}
	var out []benchfmt.Config
	for _, c := range cfgs {
		if k, i, ok := ChunkOf(c.Key); ok {
			if parts[k.Name] == nil {
				parts[k.Name] = map[int][]byte{}
			}
			parts[k.Name][i] = c.Value
			continue
		}
		out = append(out, c)
	}
	for idx := range out {
		k, ok := recordingKeyIndex[out[idx].Key]
		if !ok || !k.Chunked {
			continue
		}
		more := parts[k.Name]
		delete(parts, k.Name)
		value := append([]byte(nil), out[idx].Value...)
		for i := 2; i <= len(more)+1; i++ {
			part, ok := more[i]
			if !ok {
				return nil, fmt.Errorf("run: %s missing", k.chunkName(i))
			}
			value = append(value, part...)
		}
		out[idx].Value = value
	}
	if len(parts) > 0 {
		orphans := make([]string, 0, len(parts))
		for name := range parts {
			orphans = append(orphans, name)
		}
		sort.Strings(orphans)
		return nil, fmt.Errorf("run: %s continued without its own line", strings.Join(orphans, ", "))
	}
	return out, nil
}

// MandatoryRecordingKeys are the spellings a current-shape recording
// always carries (the KeyMandatory class), in table order.
var MandatoryRecordingKeys = func() []string {
	var names []string
	for _, k := range RecordingKeys {
		if k.Class == KeyMandatory {
			names = append(names, k.Name)
		}
	}
	return names
}()

// AuditRecordingKeys are the rows two sides may differ in with a note,
// never a grouping key (spec §10.1), in table order — the order the
// notes render in.
var AuditRecordingKeys = func() []RecordingKey {
	var keys []RecordingKey
	for _, k := range RecordingKeys {
		if k.Audit {
			keys = append(keys, k)
		}
	}
	return keys
}()

// GuardRecordingKeys are the four comparison-guard rows two sides must
// agree on to compare at all (spec §10.1), in table order.
var GuardRecordingKeys = func() []RecordingKey {
	var keys []RecordingKey
	for _, k := range RecordingKeys {
		if k.Guard {
			keys = append(keys, k)
		}
	}
	return keys
}()

// ToolchainKeys are the file-configuration lines `go test` itself
// emits — the only stream-derived keys a recording carries (spec §5),
// kept as grouping keys and never projected away.
var ToolchainKeys = []string{"goos", "goarch", "pkg", "cpu"}

// IsToolchainKey reports whether key is one of ToolchainKeys.
func IsToolchainKey(key string) bool {
	for _, k := range ToolchainKeys {
		if k == key {
			return true
		}
	}
	return false
}
