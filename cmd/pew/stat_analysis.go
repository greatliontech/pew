package main

import (
	"context"
	"sort"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/run"
)

type statPreparation struct {
	engine *gofresh.Engine
	err    error
	reason string
	fatal  bool
}

type statViewKey struct {
	module  *statModule
	current currentBench
	label   string
}

type statView struct {
	view *gofresh.View
	err  error
}

// statAnalysis owns immutable preparation and source views for one invocation.
// Measurement and diagnostic checks always acquire separate sibling transactions.
type statAnalysis struct {
	env      gotool.Environment
	prepared map[currentBench]statPreparation
	engines  map[[2]string]*gofresh.Engine
	flags    map[string]struct {
		value string
		err   error
	}
	views map[statViewKey]statView
}

func newStatAnalysis(env gotool.Environment) *statAnalysis {
	return &statAnalysis{env: env, prepared: map[currentBench]statPreparation{}, engines: map[[2]string]*gofresh.Engine{}, flags: map[string]struct {
		value string
		err   error
	}{}, views: map[statViewKey]statView{}}
}

func (a *statAnalysis) prepare(ctx context.Context, cur currentBench) (p statPreparation) {
	if p, ok := a.prepared[cur]; ok {
		return p
	}
	defer func() { a.prepared[cur] = p }()
	flags, ok := a.flags[cur.moduleDir]
	if !ok {
		reader, err := preparationReader(ctx, cur.moduleDir, a.env)
		flags.err = err
		if err == nil {
			flags.value, flags.err = run.EffectiveGoflags(ctx, reader)
		}
		a.flags[cur.moduleDir] = flags
	}
	if flags.err != nil {
		return statPreparation{err: flags.err, reason: "effective build flags unavailable"}
	}
	pgo, err := run.PGOInput(cur.moduleDir, cur.pkgDir, cur.mainPkg, flags.value)
	if err != nil {
		return statPreparation{err: err, reason: "PGO input unavailable"}
	}
	key := [2]string{cur.moduleDir, pgo}
	engine := a.engines[key]
	if engine == nil {
		engine, err = buildEngine(ctx, cur.moduleDir, a.env, nil, pgo)
		if err != nil {
			return statPreparation{err: err, reason: "engine unavailable", fatal: true}
		}
		a.engines[key] = engine
	}
	return statPreparation{engine: engine}
}

func (a *statAnalysis) view(ctx context.Context, m *statModule, cur currentBench, label string) (*gofresh.View, error) {
	key := statViewKey{m, cur, label}
	if result, ok := a.views[key]; ok {
		return result.view, result.err
	}
	p := a.prepare(ctx, cur)
	result := statView{err: p.err}
	if result.err == nil {
		var names []string
		for k, c := range m.current {
			if c == cur && k.label == label {
				names = append(names, k.bench)
			}
		}
		sort.Strings(names)
		subjects := make([]gofresh.Subject, 0, len(names))
		for _, name := range names {
			subjects = append(subjects, gofresh.Subject{Package: cur.importPath, Symbol: name})
		}
		result.view, result.err = newViewFor(p.engine, ctx, subjects, cur.moduleDir, gofresh.Measurement)
	}
	a.views[key] = result
	return result.view, result.err
}
