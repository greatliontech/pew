package main

import (
	"bytes"
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
	ownership *abOutputOwnership
	path      string
	blocks    []abBlock
	samples   map[string]map[string]bool
}

type abBlock struct {
	pkg, ref, side string
	pair           int
	raw            []byte
	profile        *profiles.Capture
}

func (a *abArtifact) pair(pkg, ref string, pair int, rawA, rawB []byte) error {
	for side, raw := range [][]byte{rawA, rawB} {
		rows, corrupt, _, err := run.Parse(raw)
		if err != nil {
			return err
		}
		if len(rows) == 0 || len(corrupt) != 0 {
			return fmt.Errorf("ab: incomplete or corrupt measurement pair")
		}
		names := map[string]bool{}
		children := map[string]bool{}
		for _, r := range rows {
			children[string(r.Name)] = true
			if got := r.GetConfig("pkg"); got != "" && got != pkg {
				return fmt.Errorf("ab: stream package %q differs from %q", got, pkg)
			}
			names[run.BenchName(string(r.Name))] = true
		}
		var selected []string
		for name := range names {
			selected = append(selected, name)
		}
		audit := run.AuditStream(rows, corrupt, 1, selected)
		if audit.PackageCause != "" || len(audit.Refused) != 0 {
			return fmt.Errorf("ab: invalid measurement pair: %s %v", audit.PackageCause, audit.Refused)
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
	a.blocks = append(a.blocks, abBlock{pkg: pkg, ref: ref, side: "A", pair: pair, raw: bytes.Clone(rawA)}, abBlock{pkg: pkg, ref: ref, side: "B", pair: pair, raw: bytes.Clone(rawB)})
	return a.write()
}

func (a *abArtifact) write() error {
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
	if a.ownership != nil {
		if err := a.ownership.validate(); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), a.path)
}
