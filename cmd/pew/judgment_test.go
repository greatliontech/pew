package main

import (
	"context"
	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/store"
)

// storedVerdict projects one row of the production batch evaluator for assertions.
func storedVerdict(ctx context.Context, st *store.Store, e *gofresh.Engine, pkgPath, pkgRel, moduleDir, bench, label string) (verdict, string, gofresh.Fingerprint, string, error) {
	rows, err := checkPackage(ctx, st, func(subjects []gofresh.Subject) (*gofresh.View, error) {
		return e.NewViewFor(ctx, subjects, moduleDir, gofresh.Measurement)
	}, pkgPath, pkgRel, moduleDir, []string{bench}, label, nil)
	if err != nil {
		return "", "", gofresh.Fingerprint{}, "", err
	}
	r := rows[bench]
	return r.v, r.reason, r.fp, r.grownLedger, r.err
}
