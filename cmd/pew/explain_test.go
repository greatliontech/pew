package main

import (
	"context"
	"io"

	"github.com/greatliontech/gofresh"
)

// explainRecordAgainstCurrent supplies a fresh fixture capture to the same
// renderer used by status and stat. It implements no alternative explanation.
func explainRecordAgainstCurrent(ctx context.Context, w io.Writer, e *gofresh.Engine, moduleDir, importPath, bench string, fp gofresh.Fingerprint, env []string) error {
	current, err := e.CaptureFor(ctx, gofresh.Subject{Package: importPath, Symbol: bench}, moduleDir, gofresh.Measurement)
	if err != nil {
		return err
	}
	return explainCapturedRecord(ctx, w, moduleDir, fp, current, env)
}
