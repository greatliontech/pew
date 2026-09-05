package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// progressCadence is the one cadence pew's progress keeps: the line
// naming the stretch in flight repeats this often while a verb works,
// so no load, build, or measurement stays silent longer than this
// (spec REQ-pew-progress). Fixed rather than paced by measurement: the
// line's information changes at unit boundaries, and a reader is
// served by a bounded silence, not by more lines.
const progressCadence = 30 * time.Second

// commandContext is a verb's context: the command's own, bound to
// SIGINT and SIGTERM so an interruption cancels the work in flight
// instead of killing the process mid-unit (spec REQ-pew-interruption).
// The stop function releases the signal binding.
func commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// interruptedError is a verb cut short by its context: the message
// names what was kept and what was not done (REQ-pew-interruption);
// the process exits 130 on it.
type interruptedError struct{ msg string }

func (e *interruptedError) Error() string { return e.msg }

func interrupted(format string, args ...any) error {
	return &interruptedError{fmt.Sprintf(format, args...)}
}

// reporter is the verb's progress line: the phase in flight, repeated
// on the cadence to the operator's log with the elapsed time, so a long
// stretch is never silent. One reporter serves a process — the verbs
// are sequential — and the engine's keep-alives name the stretch too
// (emitEngineDiagnostic).
type reporter struct {
	mu      sync.Mutex
	out     io.Writer
	start   time.Time
	phase   string
	stop    chan struct{}
	done    chan struct{}
	stopped sync.Once
	cadence time.Duration
}

var (
	activeMu sync.Mutex
	active   *reporter
)

// startReporter installs the process's reporter writing to out on the
// cadence; the returned stop ends the cadence. Tests lower the cadence
// through cadence; production passes progressCadence.
func startReporter(out io.Writer, cadence time.Duration) (stop func()) {
	r := &reporter{out: out, start: time.Now(), stop: make(chan struct{}), done: make(chan struct{}), cadence: cadence}
	activeMu.Lock()
	active = r
	activeMu.Unlock()
	if cadence > 0 {
		go r.tick()
	} else {
		close(r.done)
	}
	return func() {
		r.stopped.Do(func() { close(r.stop) })
		<-r.done // no line after the stop
		activeMu.Lock()
		if active == r {
			active = nil
		}
		activeMu.Unlock()
	}
}

func (r *reporter) tick() {
	defer close(r.done)
	t := time.NewTicker(r.cadence)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.mu.Lock()
			phase := r.phase
			r.mu.Unlock()
			if phase != "" {
				fmt.Fprintf(r.out, "pew: %s (%s elapsed)\n", phase, time.Since(r.start).Round(time.Second))
			}
		}
	}
}

// reportPhase names the stretch now in flight; the next cadence line
// carries it. A verb without a reporter (a library call, a test) is
// silent.
func reportPhase(phase string) {
	activeMu.Lock()
	r := active
	activeMu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	r.phase = phase
	r.mu.Unlock()
}
