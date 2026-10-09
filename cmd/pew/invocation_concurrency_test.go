package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	gofreshtool "github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/pew/internal/gotool"
)

func TestConcurrentInvocationsKeepEvidenceIsolated(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	// Both invocations address the same module. Only their owned environment,
	// store and dependencies differ; a directory-only or process-wide memo
	// would merge their evidence. The host environment and cwd never change.
	module := t.TempDir()
	writeFile(t, filepath.Join(module, "go.mod"), "module example.com/concurrent\n\ngo 1.26\n")
	module = gotool.CommandDir(module)
	type lane struct {
		ctx                    context.Context
		inv                    *invocation
		store, marker, vouches string
		log                    bytes.Buffer
		spawns, samples, views int
		phases                 []string
	}
	lanes := []*lane{{marker: "left"}, {marker: "right"}}
	ready, release := make(chan struct{}, len(lanes)), make(chan struct{})
	for _, l := range lanes {
		l.store = t.TempDir()
		writeFile(t, filepath.Join(l.store, vouchFileName), "example.com/"+l.marker+":Standing\n")
		l.vouches = "example.com/" + l.marker + ".Flag,example.com/" + l.marker + ".Standing"
		owned, deps := testDependencies(t)
		owned = context.WithValue(ctx, dependenciesKey{}, deps)
		owned, stop := startReporter(owned, &l.log, 0)
		t.Cleanup(stop)
		var err error
		l.ctx, l.inv, err = beginInvocation(owned, l.store, []string{"example.com/" + l.marker + ":Flag"}, &l.log)
		if err != nil {
			t.Fatal(err)
		}
		// Supply distinct immutable snapshots directly, without mutating os.Environ.
		l.inv.env, err = gotool.NewEnvironment(gofreshtool.SetEnv(os.Environ(), "PEW_CONCURRENT_INVOCATION", l.marker))
		if err != nil {
			t.Fatal(err)
		}
		setPhaseHook(l.ctx, func(phase string) { l.phases = append(l.phases, phase) })
		deps.prepare = func(cmd *exec.Cmd) {
			l.spawns++
			if cmd.Dir != module || !slices.Contains(cmd.Env, "PEW_CONCURRENT_INVOCATION="+l.marker) {
				cancel()
				return
			}
			if l.spawns == 1 {
				ready <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
		}
		deps.sample = func(ctx context.Context, dir string, env gotool.Environment) (string, error) {
			l.samples++
			if dir != module || !slices.Contains(env.Values(), "PEW_CONCURRENT_INVOCATION="+l.marker) {
				return "", fmt.Errorf("%s sampled another invocation", l.marker)
			}
			return sampleGoVersion(ctx, dir, env)
		}
		deps.view = func(_ *gofresh.Engine, _ context.Context, subjects []gofresh.Subject, dir string, kind gofresh.Kind) (*gofresh.View, error) {
			l.views++
			if dir != module || kind != gofresh.Measurement || len(subjects) != 1 || subjects[0].Symbol != l.marker {
				return nil, errors.New("foreign analysis dependency")
			}
			return nil, fmt.Errorf("owned analysis %s", l.marker)
		}
	}
	finished := make(chan error, len(lanes))
	for _, l := range lanes {
		go func() {
			for range 2 {
				if _, _, err := newEngineAt(l.ctx, module, module, false, l.inv.env); err != nil {
					finished <- err
					return
				}
				vouches, err := invocationVouches(l.ctx, module)
				if err != nil || strings.Join(vouches, ",") != l.vouches {
					finished <- fmt.Errorf("%s vouches: %v, %v", l.marker, vouches, err)
					return
				}
				_, err = newViewFor(nil, l.ctx, []gofresh.Subject{{Package: "example.com/concurrent", Symbol: l.marker}}, module, gofresh.Measurement)
				if err == nil || err.Error() != "owned analysis "+l.marker {
					finished <- fmt.Errorf("%s view dependency: %v", l.marker, err)
					return
				}
				emitEngineDiagnostic(l.ctx, gofresh.Progress{Phase: "isolation", Detail: l.marker})
			}
			finished <- nil
		}()
	}
	for range lanes {
		select {
		case <-ready:
		case <-ctx.Done():
			close(release)
			t.Fatal("invocations failed to overlap during preparation")
		}
	}
	for _, l := range lanes {
		writeFile(t, filepath.Join(l.store, vouchFileName), "example.com/"+l.marker+":Changed\n")
	}
	close(release)
	for range lanes {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range lanes {
		if l.spawns != 1 || l.samples != 2 || l.views != 2 || len(l.phases) != 2 {
			t.Fatalf("%s resources: spawns=%d samples=%d views=%d phases=%v", l.marker, l.spawns, l.samples, l.views, l.phases)
		}
		if l.log.String() != strings.Repeat("gofresh: isolation — "+l.marker+"\n", 2) {
			t.Fatalf("%s diagnostics crossed invocations: %s", l.marker, &l.log)
		}
		next, _, err := beginInvocation(ctx, l.store, nil, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := invocationVouches(next, module)
		if err != nil || strings.Join(got, ",") != "example.com/"+l.marker+".Changed" {
			t.Fatalf("later %s invocation retained old vouches: %v %v", l.marker, got, err)
		}
	}
}
