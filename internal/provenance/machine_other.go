//go:build !linux

package provenance

import "runtime"

// gatherFacts on non-Linux platforms: a best-effort fingerprint from runtime
// facts only. pew's primary target is Linux single-machine (§3); richer hardware
// probing on other OSes lands when needed.
func gatherFacts() (MachineFacts, error) {
	return MachineFacts{
		LogicalCores: runtime.NumCPU(),
		OS:           runtime.GOOS,
		GOARCH:       runtime.GOARCH,
	}, nil
}
