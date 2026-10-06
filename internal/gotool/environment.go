package gotool

import (
	"strconv"

	gofreshtool "github.com/greatliontech/gofresh/gotool"
)

// Environment is an owned, normalized process-environment snapshot. Its zero
// value is an explicitly empty environment, never an instruction to inherit.
type Environment struct {
	entries []string
}

// NewEnvironment admits one caller snapshot. Nil inherits the process
// environment at this boundary; an explicitly empty slice remains empty.
func NewEnvironment(env []string) (Environment, error) {
	entries, err := gofreshtool.NormalizeEnv(inherited(env))
	if err != nil {
		return Environment{}, &EnvironmentError{Err: err}
	}
	return Environment{entries: entries}, nil
}

// Values returns an owned copy for external APIs accepting exec-style entries.
func (e Environment) Values() []string {
	return append([]string{}, e.entries...)
}

// Lookup reads a setting under the Go command's platform-specific key rule.
func (e Environment) Lookup(key string) (string, bool) {
	return gofreshtool.LookupEnv(e.entries, key)
}

// WithParallelism sets the measured process's GOMAXPROCS without changing the
// analysis snapshot. The caller derives a positive width from its CPU pin.
func (e Environment) WithParallelism(width int) Environment {
	return Environment{entries: gofreshtool.SetEnv(e.entries, "GOMAXPROCS", strconv.Itoa(width))}
}

// For derives a command environment through the shared directory policy.
// Directory strings remain external input: even a normalized snapshot cannot
// establish that a supplied directory has an absolute or NUL-free spelling.
func (e Environment) For(dir string) ([]string, error) {
	entries, err := gofreshtool.EnvForCommand(e.entries, dir)
	if err != nil {
		return nil, &EnvironmentError{Err: err}
	}
	return entries, nil
}
