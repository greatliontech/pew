package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/store"
)

type profileUnfulfilledError struct{}

var errHistoricalProfileAbsent = errors.New("profile object absent")

func (*profileUnfulfilledError) Error() string {
	return "requested profile comparison unfulfilled; see per-side dispositions"
}

type historicalProfileObjects struct {
	module *statModule
	ref    string
}

func (r historicalProfileObjects) ReadObject(hash string, size int64) ([]byte, error) {
	path, err := r.module.store.ObjectPath(hash)
	if err != nil {
		return nil, err
	}
	b, ok, err := r.module.repo.ReadAt(r.ref, path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: object %s absent at %s", errHistoricalProfileAbsent, hash, r.ref)
	}
	if int64(len(b)) != size || profiles.Digest(b) != hash {
		return nil, fmt.Errorf("%w at %s: %s", store.ErrObjectIntegrity, r.ref, hash)
	}
	return b, nil
}

type profileComparison struct {
	failure     error
	Kind        string                `json:"kind"`
	Package     string                `json:"package"`
	BasePackage string                `json:"basePackage,omitempty"`
	NewPackage  string                `json:"newPackage,omitempty"`
	Benchmark   string                `json:"benchmark"`
	Profile     string                `json:"profile"`
	Base        profileStatus         `json:"base"`
	New         profileStatus         `json:"new"`
	Compared    bool                  `json:"compared"`
	Reason      string                `json:"reason,omitempty"`
	Differences []profiles.Difference `json:"differences,omitempty"`
}

// comparisonProfileObject retains the exact admitted bytes for comparison. Only
// a missing object is an unfulfilled request; other read/check failures stay errors.
type comparisonProfileObject struct {
	reader profileObjectReader
	bytes  []byte
}

func (r *comparisonProfileObject) ReadObject(hash string, size int64) ([]byte, error) {
	b, err := r.reader.ReadObject(hash, size)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %v", errHistoricalProfileAbsent, err)
	}
	r.bytes = b
	return b, err
}

func compareStatProfiles(ctx context.Context, m *statModule, key statKey, bl baseline, kinds []string, resolved statSubjects, analysis *statAnalysis) []profileComparison {
	if len(kinds) == 0 {
		return nil
	}
	var identity compare.Disposition
	resolved.describe(&identity)
	var out []profileComparison
	for _, kind := range kinds {
		r := profileComparison{Kind: "profile", Package: identity.Package, BasePackage: identity.BasePackage, NewPackage: identity.NewPackage, Benchmark: key.bench, Profile: kind}
		var captures [2]*profiles.Capture
		var blobs [2][]byte
		for i, ref := range []string{bl.baseRef, bl.newRef} {
			pkg := []string{resolved.base.Package, resolved.newer.Package}[i]
			status := profileStatus{Package: pkg, Benchmark: key.bench, Kind: kind, Integrity: "unavailable", Freshness: "unavailable", Relation: "unverified", Outcome: "unavailable", Reason: "requested capture absent"}
			adm := m.admission(ref, key)
			if !adm.ok {
				status.Reason = "measurement unavailable or not admitted: " + adm.class
			} else {
				index, _, err := profiles.FromRows(adm.rows)
				if err != nil {
					status.Integrity, status.Error = "invalid", err.Error()
					status.Reason = "profile index invalid"
					r.failure = errors.Join(r.failure, err)
				} else {
					for _, c := range index.Captures {
						if c.Kind != kind {
							continue
						}
						recordedPackage := adm.rows[0].GetConfig("pkg")
						if recordedPackage == "" {
							recordedPackage = pkg
						}
						if c.Benchmark != key.bench || c.Package != recordedPackage {
							status.Integrity, status.Error = "invalid", "profile subject differs from recording"
							status.Reason = status.Error
							r.failure = errors.Join(r.failure, errors.New(status.Error))
							break
						}
						var reader profileObjectReader = m.store
						if ref != "" {
							reader = historicalProfileObjects{m, ref}
						}
						var checking *gofresh.View
						var viewErr error
						if ref == "" {
							if cur, ok := m.current[key]; ok {
								checking, viewErr = analysis.view(ctx, m, cur, key.label)
							}
						}
						object := &comparisonProfileObject{reader: reader}
						status, err = inspectProfile(ctx, object, c, adm.rows, checking)
						if ref == "" && viewErr != nil {
							status.Error = errors.Join(err, viewErr).Error()
							r.failure = errors.Join(r.failure, viewErr)
						}
						if err != nil && !errors.Is(err, errHistoricalProfileAbsent) {
							r.failure = errors.Join(r.failure, err)
						}
						if ref != "" {
							status.Freshness = "not-applicable"
						}
						if ref != "" && (isDirty(adm.rows) || c.Dirty) {
							status.Relation = "mismatch"
							status.Reason = "dirty historical evidence"
						}
						if err == nil && status.Relation != "mismatch" {
							blobs[i] = object.bytes
							copy := c
							captures[i] = &copy
						}
						break
					}
				}
			}
			if i == 0 {
				r.Base = status
			} else {
				r.New = status
			}
		}
		if captures[0] != nil && captures[1] != nil {
			var err error
			r.Differences, err = profiles.Compare(*captures[0], *captures[1], blobs[0], blobs[1])
			if err != nil {
				r.Reason = err.Error()
			} else if *r.Base.Empty || *r.New.Empty {
				r.Reason = "empty capture; normalized shares undefined for zero totals"
			} else {
				r.Compared = true
			}
		} else {
			r.Reason = "requested profiles unavailable or incompatible with their measurements"
		}
		out = append(out, r)
	}
	return out
}

func writeProfileComparisons(w io.Writer, rows []profileComparison, jsonOut bool) error {
	for _, r := range rows {
		if jsonOut {
			if err := json.NewEncoder(w).Encode(r); err != nil {
				return err
			}
			continue
		}
		identity := r.Benchmark
		if r.Package != "" {
			identity = r.Package + "." + identity
		}
		if _, err := fmt.Fprintf(w, "profile %s %s: compared=%t %s\n", identity, r.Profile, r.Compared, r.Reason); err != nil {
			return err
		}
		if r.BasePackage != r.NewPackage {
			if _, err := fmt.Fprintf(w, "  package contexts: base=%s new=%s\n", r.BasePackage, r.NewPackage); err != nil {
				return err
			}
		}
		for _, side := range []struct {
			name   string
			status profileStatus
		}{{"base", r.Base}, {"new", r.New}} {
			if _, err := fmt.Fprintf(w, "  %s:\n", side.name); err != nil {
				return err
			}
			if err := renderProfiles(w, []profileStatus{side.status}, true); err != nil {
				return err
			}
		}
		for _, d := range r.Differences {
			if _, err := fmt.Fprintf(w, "  %s/%s raw totals base=%d new=%d; normalized flat shares (cumulative overlaps; no per-operation or causal claim)\n", d.Type, d.Unit, d.BaseTotal, d.NewTotal); err != nil {
				return err
			}
			for _, f := range d.Functions {
				share := func(p *float64) string {
					if p == nil {
						return "undefined"
					}
					return fmt.Sprintf("%.6f", *p)
				}
				if _, err := fmt.Fprintf(w, "    %s flat=%d→%d cumulative=%d→%d share=%s→%s\n", f.Symbol, f.BaseFlat, f.NewFlat, f.BaseCumulative, f.NewCumulative, share(f.BaseShare), share(f.NewShare)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
