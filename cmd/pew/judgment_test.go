package main

import (
	"context"
	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/store"
)

// storedVerdict projects one row of the production batch evaluator for assertions.
func storedVerdict(ctx context.Context, st *store.Store, e *gofresh.Session, pkgPath, pkgRel, moduleDir, bench, label string) (verdict, string, gofresh.Fingerprint, string, error) {
	rows, err := checkPackage(ctx, st, func(subjects []gofresh.Subject) (*gofresh.Snapshot, error) {
		return e.Snapshot(ctx, subjects, gofresh.Measurement)
	}, pkgPath, pkgRel, moduleDir, []string{bench}, label, nil)
	if err != nil {
		return "", "", gofresh.Fingerprint{}, "", err
	}
	r := rows[bench]
	return r.v, r.reason, r.fp, r.grownLedger, r.err
}
