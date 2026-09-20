package run

import "golang.org/x/perf/benchfmt"

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
	{Name: "pew-runtime-inputs", Class: KeyMandatory, Display: "runtime inputs"},
	{Name: "pew-purity", Class: KeyOmittable, Display: "purity"},
	{Name: "pew-vouches", Class: KeyOmittable, Audit: true, Display: "dynamic-state vouches"},
	{Name: "pew-dynamic-state", Class: KeyOmittable, Audit: true, Display: "dynamic-state strategies"},
	{Name: "pew-single-subject-discharges", Class: KeyOmittable, Audit: true, Display: "single-subject discharges"},
	{Name: "pew-package-process-discharges", Class: KeyOmittable, Audit: true, Display: "package-process discharges"},
	{Name: "pew-closure", Class: KeyMandatory, Display: "closure"},
	{Name: "pew-closure-strategy", Class: KeyOmittable, Audit: true, Display: "closure derivations"},
	{Name: "pew-test-variants", Class: KeyMandatory, Display: "test-variants"},
	{Name: "pew-test-variant-ledger", Class: KeyMandatory, Display: "test-variant ledger"},
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
// never define (spec §5: refused before storage).
func IsRecordingKey(name string) bool {
	_, ok := recordingKeyIndex[name]
	return ok
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
