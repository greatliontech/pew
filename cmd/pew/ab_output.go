package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/run"
)

// abOutputOwnership is the invocation's admitted artifact destination and the
// stores/source inputs its selected packages actually own. It does not infer
// ownership of arbitrary neighboring directories from filenames or prefixes.
type abOutputOwnership struct {
	path            string
	stores, sources []string
	profiles        bool
}

func prepareABOutput(ctx context.Context, out string, profiling bool, pkgs []pkgMeta, env gotool.Environment) (*abOutputOwnership, error) {
	if out == "" {
		return nil, nil
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return nil, err
	}
	o := &abOutputOwnership{path: abs, profiles: profiling}
	if err := o.includePackages(ctx, pkgs, env); err != nil {
		return nil, err
	}
	return o, nil
}

// includePackages inventories one materialized side under its own module and
// build selection. The caller unions both sides before spending any build.
func (o *abOutputOwnership) includePackages(ctx context.Context, pkgs []pkgMeta, env gotool.Environment) error {
	configured := ""
	if inv, ok := ctx.Value(invocationKey{}).(*invocation); ok {
		configured = inv.vouches.storeDir
	}
	modules := map[string][]string{}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue
		}
		if _, ok := modules[p.Module.Dir]; !ok {
			root, err := moduleBenchDir(configured, p.Module.Dir)
			if err != nil {
				return err
			}
			o.stores = append(o.stores, root)
			o.sources = append(o.sources, filepath.Join(p.Module.Dir, "go.mod"), filepath.Join(p.Module.Dir, "go.sum"))
		}
		modules[p.Module.Dir] = append(modules[p.Module.Dir], p.ImportPath)
	}
	// Cheap ownership/topology refusals precede dependency discovery and builds.
	if err := o.validate(); err != nil {
		return err
	}
	var dirs []string
	for dir := range modules {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		reader := gotool.Reader(dir, env, dependencies(ctx).prepare)
		flags, err := run.EffectiveGoflags(ctx, reader)
		if err != nil {
			return err
		}
		workspace, err := gotool.EnvValue(ctx, reader, "GOWORK")
		if err != nil {
			return err
		}
		if workspace != "" && workspace != "off" {
			o.sources = append(o.sources, workspace, workspace+".sum")
		}
		for _, p := range pkgs {
			if p.Module.Dir == dir {
				path, err := run.PGOPath(dir, p.Dir, p.Name == "main", flags)
				if err != nil {
					return err
				}
				if path != "" {
					o.sources = append(o.sources, path)
				}
			}
		}
		if err := o.validate(); err != nil {
			return err
		}
		args := append([]string{"-deps", "-test", "-json"}, modules[dir]...)
		data, err := gotool.List(ctx, dir, env, args...)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		for {
			// Go's selected build-input inventory also covers dependency and
			// embedded inputs. Cached and standard packages still name selected
			// source inputs; neither property is a reason to omit those paths.
			// Selected ForTest variants carry test sources in GoFiles and test
			// assets in EmbedFiles. TestGoFiles/XTestGoFiles and their embed
			// counterparts on ordinary entries describe unselected tests too;
			// they are not additional inputs of this binary.
			var p struct {
				Dir                                                                                  string
				GoFiles, CgoFiles                                                                    []string
				CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SwigFiles, SwigCXXFiles, SysoFiles []string
				EmbedFiles                                                                           []string
				Module                                                                               *struct{ GoMod string }
			}
			if err := decoder.Decode(&p); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return err
			}
			for _, files := range [][]string{p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SFiles, p.SwigFiles, p.SwigCXXFiles, p.SysoFiles, p.EmbedFiles} {
				for _, file := range files {
					if !filepath.IsAbs(file) {
						file = filepath.Join(p.Dir, file)
					}
					o.sources = append(o.sources, file)
				}
			}
			if p.Module != nil && p.Module.GoMod != "" {
				o.sources = append(o.sources, p.Module.GoMod)
			}
		}
	}
	return o.validate()
}

func (o *abOutputOwnership) validate() error {
	destinations := []string{o.path}
	if o.profiles {
		destinations = append(destinations, o.path+".profiles")
	}
	for _, dest := range destinations {
		for _, root := range o.stores {
			if pathsOverlap(dest, root) || pathsOverlap(resolveExistingPrefix(dest), root) {
				return fmt.Errorf("ab: output %s overlaps protected recording store %s", dest, root)
			}
		}
	}
	if err := rejectRecordingDestinations(o.sources, destinations); err != nil {
		return fmt.Errorf("ab: output ownership: %w", err)
	}
	if err := checkABOutputPath(o.path, false); err != nil {
		return err
	}
	if o.profiles {
		return checkABOutputPath(o.path+".profiles", true)
	}
	return nil
}

// The file must have an existing directory parent; only the derived companion
// directory may be absent. Symlink components are refused, never followed.
func checkABOutputPath(path string, directory bool) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && current == path {
			// Both a new artifact and a new companion home are legitimate.
		} else if err != nil {
			return fmt.Errorf("ab: output path %s: %w", current, err)
		} else if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ab: symlink output path: %s", current)
		} else if current != path || directory {
			if !info.IsDir() {
				return fmt.Errorf("ab: output path is not a directory: %s", current)
			}
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("ab: output is not a regular file: %s", current)
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}
