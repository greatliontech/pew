package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

// workload is Pew's requested-work account, separate from native freshness.
// Absence cannot certify a historical selection or calibration effort.
type workload struct {
	Version   int    `json:"version"`
	Selection string `json:"selection"`
	Count     int    `json:"count"`
	Budget    string `json:"budget"`
}

func requestedWorkload(options run.Options, bench string) (workload, error) {
	if err := options.ValidateEffort(); err != nil {
		return workload{}, err
	}
	selection, err := restrictBenchmarkPattern(options.Bench, []string{bench})
	if err != nil {
		return workload{}, err
	}
	if selection == "" {
		return workload{}, fmt.Errorf("requested selection contains no %s", bench)
	}
	budget, err := canonicalBudget(options.Benchtime)
	if err != nil {
		return workload{}, err
	}
	return workload{Version: 1, Selection: selection, Count: options.Count, Budget: budget}, nil
}

func canonicalBudget(budget string) (string, error) {
	if err := run.ValidateBudget(budget); err != nil {
		return "", err
	}
	if count, ok := strings.CutSuffix(budget, "x"); ok {
		n, err := strconv.ParseInt(count, 10, 0)
		if err != nil {
			return "", err
		}
		budget = strconv.FormatInt(n, 10) + "x"
	} else {
		d, err := time.ParseDuration(budget)
		if err != nil {
			return "", err
		}
		budget = d.String()
	}
	return budget, nil
}

func encodeWorkload(w workload) (string, error) {
	if w.Version != 1 || w.Selection == "" {
		return "", fmt.Errorf("unsupported or absent workload description")
	}
	if err := validateBenchmarkPattern(w.Selection); err != nil {
		return "", err
	}
	if err := (run.Options{Count: w.Count, Benchtime: w.Budget}).ValidateEffort(); err != nil {
		return "", err
	}
	if budget, err := canonicalBudget(w.Budget); err != nil || budget != w.Budget {
		return "", fmt.Errorf("noncanonical workload budget")
	}
	data, err := json.Marshal(w)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeWorkload(value string) (*workload, error) {
	if value == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, err
	}
	var w workload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&w); err != nil {
		return nil, err
	}
	canonical, err := encodeWorkload(w)
	if err != nil || canonical != value {
		return nil, fmt.Errorf("invalid or noncanonical workload description")
	}
	return &w, nil
}

func workloadAcceptsRows(w workload, rows []*benchfmt.Result) error {
	return selectionAcceptsRows(w.Selection, w.Count, rows, nil)
}

func selectionAcceptsRows(selection string, count int, rows []*benchfmt.Result, declarations []string) error {
	alternatives, err := splitBenchmarkPattern(selection)
	if err != nil {
		return err
	}
	patterns := make([][]*regexp.Regexp, len(alternatives))
	for i, alternative := range alternatives {
		for _, part := range alternative {
			patterns[i] = append(patterns[i], regexp.MustCompile(part))
		}
	}
	counts := make(map[string]int)
	for _, row := range rows {
		// At GOMAXPROCS=1 the harness omits its CPU suffix, so a
		// child's literal "-2" is indistinguishable from that suffix.
		// Judge either possible representation without rewriting names
		// or widening the separately recorded requested selection.
		names := []string{"Benchmark" + string(row.Name)}
		if at := strings.LastIndex(names[0], "-"); at >= 0 {
			suffix := names[0][at+1:]
			if cpu, err := strconv.Atoi(suffix); err == nil && cpu > 1 && strconv.Itoa(cpu) == suffix {
				names = append(names, names[0][:at])
			}
		}
		accepted := false
		for _, spelling := range names {
			name := strings.Split(spelling, "/")
			if declarations != nil && !slices.Contains(declarations, name[0]) {
				continue
			}
			for _, alternative := range patterns {
				if len(alternative) > len(name) {
					continue
				}
				matches := true
				for i, pattern := range alternative {
					if !pattern.MatchString(name[i]) {
						matches = false
						break
					}
				}
				if matches {
					accepted = true
					break
				}
			}
			if accepted {
				break
			}
		}
		if !accepted {
			return fmt.Errorf("result %s lies outside requested benchmark selection %q", row.Name, selection)
		}
		counts[string(row.Name)]++
	}
	if len(counts) == 0 {
		return fmt.Errorf("requested workload produced no samples")
	}
	for name, got := range counts {
		if got < count {
			return fmt.Errorf("requested workload %s has %d samples, want at least %d", name, got, count)
		}
	}
	return nil
}

func fulfillsWorkload(recorded *workload, requested workload, rows []*benchfmt.Result) bool {
	return recorded != nil && recorded.Version == requested.Version && recorded.Selection == requested.Selection && recorded.Budget == requested.Budget && recorded.Count >= requested.Count && workloadAcceptsRows(requested, rows) == nil
}
