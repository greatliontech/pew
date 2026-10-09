package main

import (
	"fmt"
	"path/filepath"

	"github.com/greatliontech/pew/internal/compare"
	"golang.org/x/mod/modfile"
	"golang.org/x/perf/benchfmt"
)

// statSubjects separates the stable storage coordinate from the two ref-local
// package labels. Both admitted and blocked reporting consume this resolution.
type statSubjects struct {
	base, newer compare.Subject
}

func (m *statModule) resolveSubjects(key statKey, bl baseline) (statSubjects, error) {
	location, err := m.store.Path(key.pkgRel, key.bench, key.label)
	if err != nil {
		return statSubjects{}, err
	}
	resolve := func(ref string) (compare.Subject, error) {
		module := m.modulePath
		if ref != "" {
			if m.refModules == nil {
				m.refModules = map[string]string{}
			}
			var cached bool
			module, cached = m.refModules[ref]
			if !cached {
				path := filepath.Join(m.moduleDir, "go.mod")
				data, exists, err := m.repo.ReadAt(ref, path)
				if err != nil {
					return compare.Subject{}, err
				}
				if exists {
					parsed, err := modfile.Parse(path, data, nil)
					if err != nil {
						return compare.Subject{}, fmt.Errorf("stat: module context at %s: %w", ref, err)
					}
					if parsed.Module == nil {
						return compare.Subject{}, fmt.Errorf("stat: module context at %s has no module declaration", ref)
					}
					module = parsed.Module.Mod.Path
				}
				m.refModules[ref] = module
			}
		}
		pkg := module
		if pkg != "" && key.pkgRel != "" && key.pkgRel != "." {
			pkg += "/" + key.pkgRel
		}
		return compare.Subject{Location: location, Package: pkg, Recording: key.bench, Label: key.label}, nil
	}
	base, err := resolve(bl.baseRef)
	if err != nil {
		return statSubjects{}, err
	}
	newer, err := resolve(bl.newRef)
	if err != nil {
		return statSubjects{}, err
	}
	return statSubjects{base, newer}, nil
}

func (s statSubjects) bind(rows map[*benchfmt.Result]compare.Subject, base, newer []*benchfmt.Result) {
	for _, r := range base {
		rows[r] = s.base
	}
	for _, r := range newer {
		rows[r] = s.newer
	}
}

func (s statSubjects) describe(d *compare.Disposition) {
	d.BasePackage, d.NewPackage = s.base.Package, s.newer.Package
	d.Package = s.base.Package
	if d.Package == "" {
		d.Package = s.newer.Package
	} else if s.newer.Package != "" && s.newer.Package != d.Package {
		d.Package = ""
	}
	d.Recording, d.Label = s.base.Recording, s.base.Label
}
