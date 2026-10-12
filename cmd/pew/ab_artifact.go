package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

// abArtifact owns all completed packages and pairs of one invocation. Raw streams
// stay benchmark-format data; configuration is cleared at each section boundary.
type abArtifact struct {
	ownership    *abOutputOwnership
	path         string
	blocks       []abBlock
	samples      map[string]map[string]bool
	observeWrite func(string)
}

type abBlock struct {
	pkg, ref, side string
	pair           int
	raw            []byte
	profile        *profiles.Capture
}

func (a *abArtifact) pair(pkg, ref string, pair int, sideA, sideB abStream) error {
	for side, stream := range []abStream{sideA, sideB} {
		children := map[string]bool{}
		for _, r := range stream.rows {
			children[string(r.Name)] = true
		}
		if a.samples == nil {
			a.samples = map[string]map[string]bool{}
		}
		key := fmt.Sprintf("%s\x00%d", pkg, side)
		if prior := a.samples[key]; prior != nil {
			if len(prior) != len(children) {
				return fmt.Errorf("ab: child sample set changed between pairs")
			}
			for name := range prior {
				if !children[name] {
					return fmt.Errorf("ab: child sample set changed between pairs")
				}
			}
		} else {
			a.samples[key] = children
		}
	}
	if a.path == "" {
		return nil
	}
	a.blocks = append(a.blocks, abBlock{pkg: sideA.pkg, ref: ref, side: "A", pair: pair, raw: bytes.Clone(sideA.raw)}, abBlock{pkg: sideB.pkg, ref: ref, side: "B", pair: pair, raw: bytes.Clone(sideB.raw)})
	return a.write()
}

func (a *abArtifact) write() error {
	ctx, cancel := context.WithTimeout(context.Background(), armWriteGateBound)
	defer cancel()
	return a.writeContext(ctx)
}

func (a *abArtifact) publishProfile(ctx context.Context, block abBlock) error {
	prior := len(a.blocks)
	a.blocks = append(a.blocks, block)
	if err := a.writeContext(ctx); err != nil {
		a.blocks = a.blocks[:prior]
		return err
	}
	return nil
}

func (a *abArtifact) writeContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.path == "" {
		return nil
	}
	if a.ownership != nil {
		if err := a.ownership.validate(); err != nil {
			return err
		}
	}
	var out bytes.Buffer
	previous := map[string]bool{}
	for _, b := range a.blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		keys := make([]string, 0, len(previous))
		for k := range previous {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&out, "%s:\n", k)
		}
		previous = map[string]bool{"pew-ab": true, run.KeyDirty.Name: true, "pkg": true, "pew-ab-ref": true, "pew-ab-side": true, "pew-ab-pair": true, "pew-ab-section": true}
		section := "measurement"
		if b.profile != nil {
			section = "profile"
		}
		fmt.Fprintf(&out, "pew-ab: 2\ndirty: true\npkg: %s\npew-ab-ref: %s\npew-ab-side: %s\npew-ab-pair: %d\npew-ab-section: %s\n", b.pkg, b.ref, b.side, b.pair, section)
		if b.profile != nil {
			encoded, err := profiles.Encode(profiles.Index{Version: 1, Captures: []profiles.Capture{*b.profile}})
			if err != nil {
				return err
			}
			for _, cfg := range run.SplitChunked([]benchfmt.Config{run.KeyProfiles.Config(encoded)}) {
				fmt.Fprintf(&out, "%s: %s\n", cfg.Key, cfg.Value)
				previous[cfg.Key] = true
			}
		} else {
			r := benchfmt.NewReader(bytes.NewReader(b.raw), "ab")
			for r.Scan() {
				if err := ctx.Err(); err != nil {
					return err
				}
				if row, ok := r.Result().(*benchfmt.Result); ok {
					for _, cfg := range row.Config {
						if cfg.File {
							previous[cfg.Key] = true
						}
					}
				}
			}
			// Include trailing configuration as well as configuration on rows.
			for _, line := range strings.Split(string(b.raw), "\n") {
				if err := ctx.Err(); err != nil {
					return err
				}
				key, _, ok := strings.Cut(line, ":")
				if ok && key != "" && key[0] >= 'a' && key[0] <= 'z' && !strings.ContainsAny(key, " \t\r") {
					previous[key] = true
				}
			}
			out.Write(b.raw)
		}
		out.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(filepath.Dir(a.path), ".pew-ab-out-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if a.observeWrite != nil {
		a.observeWrite("staged")
	}
	if a.ownership != nil {
		if err := a.ownership.validate(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), a.path); err != nil {
		return err
	}
	if a.observeWrite != nil {
		a.observeWrite("committed")
	}
	return nil
}
