package main

import (
	"context"
	"io"

	"github.com/greatliontech/gofresh"
)

// explainRecordAgainstCurrent supplies a fresh fixture capture to the same
// renderer used by status and stat. It implements no alternative explanation.
func explainRecordAgainstCurrent(ctx context.Context, w io.Writer, e *analysisSession, moduleDir, importPath, bench string, fp gofresh.Fingerprint, env []string) error {
	current, err := fixtureFacts(ctx, e, gofresh.Subject{Package: importPath, Symbol: bench})
	if err != nil {
		return err
	}
	return explainCapturedRecord(ctx, w, moduleDir, fp, current, env)
}
