// Package gotool runs the go command line tool, surfacing stderr on failure.
// Every invocation runs under one go-command policy: the working directory
// resolved to its one coordinate and PWD pinned to it, so the
// resolved-directory premise every consumer of go-reported paths relies on
// holds by construction — even through a symlinked checkout whose shell
// exports the alias as PWD (spec §9's environment policy). This package
// composes pew's two contracts: a directory that does not resolve degrades to
// its absolute spelling, and environment inheritance happens once at verb
// entry. Every go invocation pew makes takes one of three routes, each under
// the same composition: run here through Runner (Output), spawned through a
// pass reader built here (Reader), or through Runner.Program with explicit
// containment for measurement processes. All routes receive an owned
// Environment admitted before preparation, deriving only command-specific PWD.
package gotool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// CommandDir resolves dir ("" = current directory) to one coordinate
// (CanonicalDir: the absolute spelling with every symbolic link followed,
// `..` applied to the resolved prefix — through `deep -> real/sub`, `deep/..`
// is real, the target's parent, where a lexical clean of the spelling first
// would answer the link's parent). Resolution failure degrades to the absolute
// unresolved path, else the spelling given, so the subsequent invocation fails
// (or succeeds) on the real filesystem state rather than here.
func CommandDir(dir string) string {
	if dir == "" {
		dir = "."
	}
	if canonical, err := CanonicalDir(dir); err == nil {
		return canonical
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// EnvironmentError is a caller environment normalization refuses — a
// duplicated or malformed entry — its own class, never a fact about the
// toolchain or the tree.
type EnvironmentError struct{ Err error }

func (e *EnvironmentError) Error() string { return e.Err.Error() }
func (e *EnvironmentError) Unwrap() error { return e.Err }

// inherited is env, or the process environment when env is nil: a nil
// env inherits the process environment, as an unset exec.Cmd.Env
// would — then PWD is pinned over it. A caller passing the empty
// non-nil slice deliberately runs go with PWD alone.
func inherited(env []string) []string {
	if env == nil {
		return os.Environ()
	}
	return env
}

// Output runs `go <args>` in dir under env through the runner and
// returns stdout; on failure the error names the command and carries
// go's stderr (the runner's own wrapping).
func Output(ctx context.Context, dir string, env Environment, args ...string) ([]byte, error) {
	return run(ctx, dir, env, nil, args...)
}

// List runs go list under pew's directory and environment policy. A listing
// whose pipes cannot be drained is refused: syntactically complete output can
// still omit packages, so there is no sound partial-output salvage.
func List(ctx context.Context, dir string, env Environment, args ...string) ([]byte, error) {
	runner := NewRunner(nil)
	return runner.List(ctx, CommandDir(dir), env.Values(), args...)
}

// EnvValue is one go-env value read through a pass reader: the
// reader's one `go env -json` snapshot (taken on its first ask, served
// to every later one), the key read from the pass's document. A key
// the go command does not set reads empty; a nil reader is refused.
// Every go-env value pew wants from one pass comes from that pass's
// reader, never a per-key probe.
func EnvValue(ctx context.Context, reader *EnvReader, key string) (string, error) {
	if reader == nil {
		return "", errors.New("gotool: nil pass reader")
	}
	snap, err := reader.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	return snap.Value(key), nil
}

// NewRunner is pew's runner under the boundary hook: the one spawn
// policy every go child pew makes rides — Command's derived environment
// over a resolved directory. prepare, when set, is the hook a pin
// observes the command's directory and environment through. Query
// cancellation sweeps the process group and bounds pipe reaping;
// callers with a distinct execution policy replace Containment explicitly.
func NewRunner(prepare func(*exec.Cmd)) Runner {
	return Runner{Containment: &Containment{}, Prepare: prepare}
}

// PrepareCommand applies the runner's process-group containment and optional
// Prepare hook to a command prepared by gofresh (gofresh.SessionPolicy.PrepareCommand).
func (r Runner) PrepareCommand(cmd *exec.Cmd) {
	if r.Containment != nil {
		contain(cmd, *r.Containment)
	}
	if r.Prepare != nil {
		r.Prepare(cmd)
	}
}

// Reader is a pass's go-env reader (EnvReader: the first key
// takes the pass's one `go env -json` snapshot) over the resolved directory and
// admitted environment, its spawns under NewRunner(prepare).
func Reader(dir string, env Environment, prepare func(*exec.Cmd)) *EnvReader {
	return NewEnvReader(NewRunner(prepare), CommandDir(dir), env.Values())
}

// run is every runner invocation pew itself issues: the directory and
// admitted environment, the hook handed to the runner.
func run(ctx context.Context, dir string, env Environment, prepare func(*exec.Cmd), args ...string) ([]byte, error) {
	return NewRunner(prepare).Run(ctx, CommandDir(dir), env.Values(), args...)
}

// Runner runs the go command line tool — and prepares a consumer's own
// program the same way (Program) — under a caller-owned spawn policy.
// Containment, when set, is the one process-boundary rule applied to
// every command the runner prepares, under that command's own context;
// Prepare, when set, sees the command after that, its Dir, Env, and
// boundary already set (a resource policy of the consumer's own). The
// zero Runner is the plain spawn.
type Runner struct {
	Containment *Containment
	Prepare     func(*exec.Cmd)
}

// Command prepares `go <args>` under the policy without starting it:
// Dir set, Env the complete derived environment, Prepare applied. A
// consumer that streams the child's output to its own sinks, or applies
// a resource policy of its own once the process exists, runs the
// returned command itself; Run is the collected form. It is Program
// over the go command.
func (r Runner) Command(ctx context.Context, dir string, env []string, args ...string) (*exec.Cmd, error) {
	return r.Program(ctx, dir, env, "go", args...)
}

// Program prepares the program name with args under the policy without
// starting it — the same policy Command applies to go: Dir set, Env the
// complete derived environment (PWD derived from Dir when one is
// given), the runner's containment when it carries one, Prepare last.
func (r Runner) Program(ctx context.Context, dir string, env []string, name string, args ...string) (*exec.Cmd, error) {
	if name == "" {
		return nil, errors.New("gotool: empty program name")
	}
	if env == nil {
		// A nil environment would let the child inherit the ambient one;
		// the caller names the environment it runs under (an empty
		// non-nil slice is a deliberate empty environment).
		return nil, fmt.Errorf("%s: nil environment", name)
	}
	if ctx == nil {
		return nil, fmt.Errorf("%s: nil context", name)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	commandEnv, err := EnvForCommand(env, dir)
	if err != nil {
		return nil, fmt.Errorf("%s: environment: %w", strings.Join(append([]string{name}, args...), " "), err)
	}
	cmd.Env = commandEnv
	if r.Containment != nil {
		contain(cmd, *r.Containment)
	}
	if r.Prepare != nil {
		r.Prepare(cmd)
	}
	return cmd, nil
}

// Run is the collected form under the runner's spawn policy: it owns
// the command's streams (a Prepare hook's own are replaced). When the
// process itself exited cleanly but a descendant held its output pipe
// past the command's WaitDelay, the output read so far is returned
// BESIDE exec.ErrWaitDelay — the consumer decides whether that answer
// serves (a toolchain sample does: a wrapper's housekeeping child is not
// a failed sample) — never discarded behind the error. A failed
// command's refusal carries go's stderr bounded to its head and tail,
// so a refusal text a consumer keys a record on never grows with a
// tree's diagnostics. On Linux, a contained collected run also ends the
// remaining process group after the bounded drain, even if the leader exited
// before cancellation. Prepared commands run by consumers own that cleanup.
func (r Runner) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd, err := r.Command(ctx, dir, env, args...)
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = runCollected(cmd, r.Containment != nil)
	if err != nil {
		if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			return stdout.Bytes(), fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
		}
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil, fmt.Errorf("go %s: %w: %s",
				strings.Join(args, " "), err, boundedText(stderr.Bytes()))
		}
		return nil, fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// ErrListingRefused is List's refusal of the wait-delay form: the
// listing's output was not drained within the wait delay, and a listing
// has no wholeness test. It is its own sentinel, never exec.ErrWaitDelay,
// so Salvaged is false for a refused listing and no reader serves it.
var ErrListingRefused = errors.New("the listing was not drained within the wait delay (a descendant's hold or a stalled copier); a listing has no wholeness test and is refused")

// List runs `go list <args>` under the policy and returns its stdout,
// refusing the wait-delay form Run would serve.
func (r Runner) List(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	out, err := r.Run(ctx, dir, env, append([]string{"list"}, args...)...)
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil, fmt.Errorf("go list %s: %w: %v", strings.Join(args, " "), ErrListingRefused, err)
	}
	return out, err
}

// stderrBound is the most of a failed command's stderr a refusal
// carries: half from its head, half from its tail, the elision counted.
const stderrBound = 32 << 10

// boundedText trims text to stderrBound bytes, keeping the head and the
// tail and naming the bytes elided between them.
func boundedText(text []byte) string {
	text = bytes.TrimSpace(text)
	if len(text) <= stderrBound {
		return string(text)
	}
	half := stderrBound / 2
	return fmt.Sprintf("%s\n... %d bytes elided ...\n%s", text[:half], len(text)-2*half, text[len(text)-half:])
}

// Containment is the one process-boundary rule a Runner applies to
// every command it prepares: the child runs in its own process group,
// a cancellation sweeps the whole group (an already-empty group is the
// process-done case, never an injected error over a finished command),
// and WaitDelay bounds how long the reap waits on a descendant holding
// the pipes — zero meaning DefaultWaitDelay, never an unbounded wait.
// The platform arms live in contain_*.go.
type Containment struct {
	WaitDelay time.Duration
}

// DefaultWaitDelay bounds the reap when a Containment names no delay.
const DefaultWaitDelay = time.Second

func (c Containment) waitDelay() time.Duration {
	if c.WaitDelay <= 0 {
		return DefaultWaitDelay
	}
	return c.WaitDelay
}

// Sampler is the one toolchain sampler a consumer holds for one
// judged run: SampleGoVersion memoized by memoKey through the one
// judged-run memo.
type Sampler struct {
	Runner Runner
	memo   RunMemo[*versionReader]
}

// versionReader is one key's holder: the sample taken once, the one
// holder discipline over `go env GOVERSION`.
type versionReader struct {
	runner Runner
	dir    string
	env    []string
	once   once[string]
}

func (r *versionReader) sample(ctx context.Context) (string, error) {
	return r.once.take(ctx, func(ctx context.Context) (string, error) {
		return r.runner.SampleGoVersion(ctx, r.dir, r.env)
	})
}

// Sample is SampleGoVersion memoized by memoKey for the sampler's
// lifetime.
func (s *Sampler) Sample(ctx context.Context, dir string, env []string) (string, error) {
	reader, err := s.memo.Get(dir, env, func() *versionReader {
		// A cancelled take leaves this holder available for a retry. Its
		// configuration belongs to the original key, not the caller's slice.
		return &versionReader{runner: s.Runner, dir: dir, env: slices.Clone(env)}
	})
	if err != nil {
		return "", err
	}
	return reader.sample(ctx)
}

// EnvReader reads go's environment for one pass: the first key takes
// the pass's one `go env -json` snapshot under the runner, every later
// key reads it — no per-key probe, no snapshot-or-probe ladder.
type EnvReader struct {
	Runner Runner
	Dir    string
	Env    []string
	once   once[*EnvSnapshot]
}

// NewEnvReader is a pass's reader over the runner, directory, and
// complete environment every spawn of that pass shares. The reader owns
// a copy of the supplied environment.
func NewEnvReader(r Runner, dir string, env []string) *EnvReader {
	return &EnvReader{Runner: r, Dir: dir, Env: slices.Clone(env)}
}

// PrimedEnvReader is a reader whose snapshot is already taken — a
// later step of the pass that holds the construction snapshot hands it
// on, so no step of one pass probes twice. A nil snapshot primes
// nothing: the reader takes its own on first use, never answering
// empty values for a snapshot that was never taken.
func PrimedEnvReader(r Runner, dir string, env []string, snapshot *EnvSnapshot) *EnvReader {
	reader := NewEnvReader(r, dir, env)
	if snapshot != nil {
		reader.once.prime(snapshot)
	}
	return reader
}

// Snapshot returns the pass's snapshot, taking it on the first call. A
// snapshot that failed is the reader's answer for its lifetime — the
// pass fails closed rather than re-probing an environment that refused
// — except a cancellation, which is the caller's and never memoized.
func (r *EnvReader) Snapshot(ctx context.Context) (*EnvSnapshot, error) {
	return r.once.take(ctx, func(ctx context.Context) (*EnvSnapshot, error) {
		return r.Runner.TakeEnvSnapshot(ctx, r.Dir, r.Env)
	})
}

// Value returns one go-env setting from the pass's snapshot.
func (r *EnvReader) Value(ctx context.Context, key string) (string, error) {
	snapshot, err := r.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	return snapshot.Value(key), nil
}

// EnvSnapshot is one observation pass's single `go env -json` read.
type EnvSnapshot struct {
	// JSON is the raw `go env -json` output, byte-identical to a direct
	// probe so digests derived from it cannot drift.
	JSON []byte
	// values are the parsed settings for single-key reads.
	values map[string]string
}

// Identity renders the snapshot's settings as one canonical string —
// sorted key=value lines — leaving out GOGCCFLAGS, which embeds a
// per-invocation temporary path and selects no source.
func (s *EnvSnapshot) Identity() string {
	if s == nil {
		return ""
	}
	keys := make([]string, 0, len(s.values))
	for k := range s.values {
		if k != "GOGCCFLAGS" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(s.values[k])
		b.WriteByte('\n')
	}
	return b.String()
}

// Lookup reports key's value and whether the document carries the key
// at all — an unset setting answers "" and true, a key the document
// never names answers false.
func (s *EnvSnapshot) Lookup(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	value, ok := s.values[key]
	return value, ok
}

// Value returns one parsed go-env setting ("" when absent).
func (s *EnvSnapshot) Value(key string) string {
	if s == nil {
		return ""
	}
	return s.values[key]
}

// Values returns an owned copy of the snapshot's parsed `go env -json` settings
// (nil when s is nil).
func (s *EnvSnapshot) Values() map[string]string {
	if s == nil || len(s.values) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out
}

// EnvSnapshotFromValues constructs an EnvSnapshot from a parsed `go env -json`
// setting map, refusing an empty map as parseEnvDocument does.
func EnvSnapshotFromValues(values map[string]string) (*EnvSnapshot, error) {
	if len(values) == 0 {
		return nil, errors.New("gotool: parse go env -json: no values")
	}
	cloned := make(map[string]string, len(values))
	for k, v := range values {
		cloned[k] = v
	}
	raw, err := json.Marshal(cloned)
	if err != nil {
		return nil, fmt.Errorf("gotool: encode go env -json: %w", err)
	}
	return &EnvSnapshot{JSON: raw, values: cloned}, nil
}

// TakeEnvSnapshot performs the pass's one `go env -json` read under the
// runner's spawn policy and the caller's complete environment.
func (r Runner) TakeEnvSnapshot(ctx context.Context, dir string, env []string) (*EnvSnapshot, error) {
	out, err := r.Run(ctx, dir, env, "env", "-json")
	if err != nil && !Salvaged(ctx, err) {
		return nil, err
	}
	values, parseErr := parseEnvDocument(out)
	if parseErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, parseErr
	}
	return &EnvSnapshot{JSON: out, values: values}, nil
}

// parseEnvDocument parses a `go env -json` answer: a document with at
// least one key — `null` and an empty object are no environment, and a
// torn document does not parse.
func parseEnvDocument(out []byte) (map[string]string, error) {
	var values map[string]string
	if err := json.Unmarshal(out, &values); err != nil {
		return nil, fmt.Errorf("gotool: parse go env -json: %w", err)
	}
	if len(values) == 0 {
		return nil, errors.New("gotool: parse go env -json: no values")
	}
	return values, nil
}

// Salvaged reports whether err is the wait-delay form Run answers
// beside a cleanly exited process's output under a context that is still live.
func Salvaged(ctx context.Context, err error) bool {
	return errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil
}

// SampleGoVersion runs `go env GOVERSION` in the target module's directory.
func (r Runner) SampleGoVersion(ctx context.Context, dir string, env []string) (string, error) {
	out, err := r.Run(ctx, dir, env, "env", "GOVERSION")
	if Salvaged(ctx, err) {
		answer, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		if strings.HasPrefix(answer, "go") {
			return answer, nil
		}
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Coordinate is the degrading form of CanonicalDir every consumer
// keys a store or a memo on: the canonical coordinate, else the
// absolute spelling, else the spelling given — never an error.
func Coordinate(dir string) string {
	if canonical, err := CanonicalDir(dir); err == nil {
		return canonical
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// CanonicalDir resolves dir to one coordinate: absolute, then every
// element evaluated in turn, symlinks followed, `..` applied to the
// resolved prefix — never a lexical clean of the spelling first.
func CanonicalDir(dir string) (string, error) {
	raw := dir
	if !filepath.IsAbs(raw) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		raw = cwd + string(os.PathSeparator) + raw
	}
	return filepath.EvalSymlinks(raw)
}
