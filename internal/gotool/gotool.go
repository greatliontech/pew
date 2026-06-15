// Package gotool runs the go command line tool, surfacing stderr on failure.
package gotool

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Run executes `go <args>` and returns stdout. On failure the error includes the
// command and go's stderr.
func Run(args ...string) ([]byte, error) {
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil, fmt.Errorf("go %s: %w: %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
