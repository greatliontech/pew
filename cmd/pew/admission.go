package main

import (
	gofresh "github.com/greatliontech/gofresh"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

// admission is one recording's standing before any guard verdict: the
// rungs every verdict surface climbs in one order — the recording's
// shape and format, decoded into its fingerprint, then the dynamic-state
// strategy it was recorded under — so a new stale class lands in one
// place and cannot land on one surface only (spec §7's prerequisites,
// REQ-pew-admission).
type admission struct {
	// ok means the recording may enter a verdict or a comparison;
	// otherwise Class is the stale class ("format", "dynamic-state
	// strategy") the surface reports.
	ok    bool
	class string
	// fp and ledger are the decoded fingerprint and test-variant ledger
	// of an admitted recording (or of one refused at the strategy rung,
	// whose fingerprint still decoded).
	fp     gofresh.Fingerprint
	ledger string
}

// admitRecording climbs the ladder over the WHOLE recording: every row
// carries the recording shape and agrees with row 0 on every key of the
// closed set — the fingerprint keys and the provenance keys the
// fingerprint does not carry (commit, dirty, the run conditions) alike
// (a recording whose rows disagree past row 0 is not one recording — it
// refuses here, never surfacing later as a mixed-provenance note), and,
// for a working-tree side only, the recorded dynamic-state strategy is
// the engine's. A ref-resolved side (a pinned tag, auto's HEAD, either
// A/B ref) enters no verdict and cannot be re-run into: it compares
// under any strategy, a difference surfacing as compare's audit note
// (spec §5's strategy row, §7's exclusion); the format rung applies
// everywhere, since an unreadable recording serves no surface.
func admitRecording(recs []*benchfmt.Result, workingTree bool) admission {
	if !store.IsRecordingShape(recs) {
		return admission{class: "format"}
	}
	var first admission
	var firstKeys map[string]string
	for i, r := range recs {
		fp, ledger, ok := fingerprintFromConfig(r.Config)
		if !ok {
			return admission{class: "format"}
		}
		keys := closedSetValues(r.Config)
		if i == 0 {
			first, firstKeys = admission{fp: fp, ledger: ledger}, keys
			continue
		}
		if fp != first.fp || ledger != first.ledger || !equalStringMaps(keys, firstKeys) {
			return admission{class: "format"}
		}
	}
	if workingTree && first.fp.DynamicStateStrategy != gofresh.DynamicStateStrategy {
		first.class = "dynamic-state strategy"
		return first
	}
	first.ok = true
	return first
}

// closedRecordingKeys is runpkg.RecordingConfigKeys as a set, built once.
var closedRecordingKeys = func() map[string]bool {
	m := make(map[string]bool, len(runpkg.RecordingConfigKeys))
	for _, k := range runpkg.RecordingConfigKeys {
		m[k] = true
	}
	return m
}()

// closedSetValues is a row's values over spec §5's closed key set — the
// per-row agreement the whole-recording rung judges.
func closedSetValues(cfg []benchfmt.Config) map[string]string {
	values := map[string]string{}
	for _, c := range cfg {
		if closedRecordingKeys[c.Key] {
			values[c.Key] = string(c.Value)
		}
	}
	return values
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
