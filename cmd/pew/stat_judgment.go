package main

import (
	"context"
	"sort"

	"github.com/greatliontech/gofresh"
)

func (m *statModule) admission(ref string, key statKey) admission {
	sk := statSideKey{ref, key.pkgRel, key.bench, key.label}
	side := m.sides[sk]
	if side.admitted == nil {
		adm := admitRecording(side.recs, ref == "")
		side.admitted = &adm
		m.sides[sk] = side
	}
	return *side.admitted
}

func (m *statModule) judgePackage(ctx context.Context, engine *gofresh.Engine, current currentBench, label string) (map[string]*benchVerdict, error) {
	rows := map[string]*benchVerdict{}
	var benches []string
	for key, cur := range m.current {
		if cur.importPath != current.importPath || cur.moduleDir != current.moduleDir || key.label != label {
			continue
		}
		_, exists, err := m.readSide("", key.pkgRel, key.bench, key.label)
		if !exists || err != nil {
			continue
		}
		adm := m.admission("", key)
		rows[key.bench] = &benchVerdict{admitted: adm, fp: adm.fp, v: verdictStale, reason: adm.class}
		benches = append(benches, key.bench)
	}
	sort.Strings(benches)
	return judgeRecordings(ctx, func(subjects []gofresh.Subject) (*gofresh.View, error) {
		return newViewFor(engine, ctx, subjects, current.moduleDir, gofresh.Measurement)
	}, current.importPath, benches, rows, nil)
}
