//go:build linux

package run

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pinHost lays out a fake sysfs host under a quiesce root: cores as
// their thread-sibling lists, online covering every CPU, the process
// affinity as given, and per-CPU indicator attributes from attrs
// (relative path under cpuN → value by CPU; an empty value omits the
// file for that CPU).
func pinHost(t *testing.T, cores [][]int, affinity, isolated, online string, attrs map[string]func(cpu int) string) {
	t.Helper()
	sysRoot, _ := withQuiesceFS(t)
	cpuRoot := filepath.Join(sysRoot, "devices", "system", "cpu")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(cpuRoot, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	maxCPU := 0
	for id, siblings := range cores {
		for _, cpu := range siblings {
			if cpu > maxCPU {
				maxCPU = cpu
			}
			write(fmt.Sprintf("cpu%d/topology/thread_siblings_list", cpu), formatCPUList(siblings))
			write(fmt.Sprintf("cpu%d/topology/core_id", cpu), fmt.Sprint(id))
			for rel, value := range attrs {
				if v := value(cpu); v != "" {
					write(fmt.Sprintf("cpu%d/%s", cpu, rel), v)
				}
			}
		}
	}
	if online == "" {
		online = fmt.Sprintf("0-%d", maxCPU)
	}
	write("online", online)
	if isolated != "" {
		write("isolated", isolated)
	}
	status := filepath.Join(t.TempDir(), "status")
	if err := os.WriteFile(status, []byte("Name:\tpew\nCpus_allowed_list:\t"+affinity+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := pinStatusPath
	pinStatusPath = status
	t.Cleanup(func() { pinStatusPath = old })
}

// fourCores is a two-way SMT host laid out as the kernel numbers it:
// cpu N and cpu N+4 share core N.
var fourCores = [][]int{{0, 4}, {1, 5}, {2, 6}, {3, 7}}

func uniform(v string) func(int) string { return func(int) string { return v } }

// A pin is derived from the host's topology, never guessed (spec §9,
// REQ-pew-pin-derivation): with every core ranking alike, the whole
// highest core outside CPU 0's is the pin; a kernel performance
// indicator that differs across cores ranks them, an indicator uniform
// across cores or missing on one says nothing; an isolated set is taken
// whole; the process affinity bounds every choice; a host with no second
// core, no topology, or no affinity refuses with the reason.
func TestDerivePinFollowsTheTopology(t *testing.T) {
	cases := []struct {
		name       string
		cores      [][]int
		affinity   string
		isolated   string
		online     string
		attrs      map[string]func(int) string
		wantCPUs   string
		wantWords  []string
		wantAbsent []string
		wantErr    string
	}{
		{
			name: "uniform host takes the highest core outside CPU 0", cores: fourCores, affinity: "0-7",
			attrs:    map[string]func(int) string{"cpu_capacity": uniform("1024"), "cpufreq/cpuinfo_max_freq": uniform("4000000")},
			wantCPUs: "3,7", wantWords: []string{"core 3", "every core ranks alike", "outside CPU 0's core"},
		},
		{
			name: "cpufreq ceiling ranks a hybrid host", cores: fourCores, affinity: "0-7",
			attrs: map[string]func(int) string{
				"cpu_capacity": uniform("1024"),
				"cpufreq/cpuinfo_max_freq": func(cpu int) string {
					if cpu%4 < 2 {
						return "5000000"
					}
					return "3500000"
				},
			},
			wantCPUs: "1,5", wantWords: []string{"highest-ranked core by cpuinfo_max_freq", "outside CPU 0's core"},
		},
		{
			name: "amd preferred-core score ranks when the ceilings read alike", cores: fourCores, affinity: "0-7",
			attrs: map[string]func(int) string{
				"cpu_capacity":             uniform("1024"),
				"cpufreq/cpuinfo_max_freq": uniform("2000000"),
				"cpufreq/amd_pstate_highest_perf": func(cpu int) string {
					if cpu%4 < 2 {
						return "196"
					}
					return "125"
				},
			},
			wantCPUs: "1,5", wantWords: []string{"by amd_pstate_highest_perf", "outside CPU 0's core"},
		},
		{
			name: "the scheduler capacity outranks the ceiling", cores: fourCores, affinity: "0-7",
			attrs: map[string]func(int) string{
				"cpu_capacity": func(cpu int) string {
					if cpu%4 == 2 {
						return "1024"
					}
					return "600"
				},
				"cpufreq/cpuinfo_max_freq": func(cpu int) string {
					if cpu%4 == 3 {
						return "5000000"
					}
					return "3000000"
				},
			},
			wantCPUs: "2,6", wantWords: []string{"by cpu_capacity, cpuinfo_max_freq"},
		},
		{
			name: "an indicator missing on one core is dropped", cores: fourCores, affinity: "0-7",
			attrs: map[string]func(int) string{
				"cpufreq/cpuinfo_max_freq": func(cpu int) string {
					if cpu%4 == 1 {
						return ""
					}
					if cpu%4 == 0 {
						return "9000000"
					}
					return "3000000"
				},
			},
			wantCPUs: "3,7", wantWords: []string{"every core ranks alike"},
		},
		{
			name: "only CPU 0's core ranks highest and is still taken", cores: fourCores, affinity: "0-7",
			attrs: map[string]func(int) string{"cpufreq/cpuinfo_max_freq": func(cpu int) string {
				if cpu%4 == 0 {
					return "5000000"
				}
				return "3000000"
			}},
			wantCPUs: "0,4", wantWords: []string{"core 0", "by cpuinfo_max_freq"}, wantAbsent: []string{"outside CPU 0's core", "allowed siblings only"},
		},
		{
			// CPU 0 sits in the highest-numbered core here, so the id
			// tiebreak alone would pick it: the CPU-0 rule steps down.
			name: "CPU 0's core is avoided even when it has the highest id", cores: [][]int{{1, 5}, {2, 6}, {3, 7}, {0, 4}}, affinity: "0-7",
			wantCPUs: "3,7", wantWords: []string{"core 2, CPUs 3,7", "outside CPU 0's core"},
		},
		{
			// The mask hides CPU 0 itself, yet CPU 4 shares its core: the
			// rule reads the full sibling list, never the allowed part.
			name: "CPU 0's core is avoided when CPU 0 is outside the mask", cores: [][]int{{1, 5}, {2, 6}, {3, 7}, {0, 4}}, affinity: "4-7",
			wantCPUs: "7", wantWords: []string{"core 2, CPUs 7", "its allowed siblings only", "outside CPU 0's core"},
		},
		{
			name: "an offline CPU is not allowed", cores: fourCores, affinity: "0-7", online: "0-6",
			wantCPUs: "2,6", wantWords: []string{"core 2"},
		},
		{
			name: "the isolated set is taken whole", cores: fourCores, affinity: "0-7", isolated: "2-3,6-7",
			wantCPUs: "2,3,6,7", wantWords: []string{"isolated CPU set"},
		},
		{
			name: "an isolated set outside the affinity is not a pin, a whole core beats a half", cores: fourCores, affinity: "0-5", isolated: "6-7",
			wantCPUs: "1,5", wantWords: []string{"core 1"},
		},
		{
			name: "the affinity bounds the core and its siblings", cores: fourCores, affinity: "0,2-3,6",
			wantCPUs: "2,6", wantWords: []string{"core 2, CPUs 2,6:"}, wantAbsent: []string{"allowed siblings only"},
		},
		{
			name: "only half cores allowed pins the allowed half", cores: fourCores, affinity: "0-3",
			wantCPUs: "3", wantWords: []string{"core 3, CPUs 3:", "its allowed siblings only"},
		},
		{
			name: "one allowed core refuses", cores: fourCores, affinity: "1,5",
			wantErr: "needs a second core",
		},
		{
			// online lists CPUs the topology never describes.
			name: "no topology refuses", cores: nil, affinity: "0-3", online: "0-3",
			wantErr: "no CPU topology",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinHost(t, tc.cores, tc.affinity, tc.isolated, tc.online, tc.attrs)
			pin, err := DerivePin()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("DerivePin = %+v, %v; want a refusal containing %q", pin, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DerivePin: %v", err)
			}
			if pin.List() != tc.wantCPUs {
				t.Errorf("pin = %s (%s), want %s", pin.List(), pin.Reason, tc.wantCPUs)
			}
			for _, w := range tc.wantWords {
				if !strings.Contains(pin.Reason, w) {
					t.Errorf("reason %q lacks %q", pin.Reason, w)
				}
			}
			for _, w := range tc.wantAbsent {
				if strings.Contains(pin.Reason, w) {
					t.Errorf("reason %q claims %q", pin.Reason, w)
				}
			}
		})
	}
}

// A topology that reads but does not describe the CPU — an empty
// sibling list, a list omitting the CPU itself, an isolated file that
// does not parse — refuses with the reason, never a panic or a guess.
func TestDerivePinRefusesUnreadableTopology(t *testing.T) {
	cpuRoot := func() string { return filepath.Join(quiesceSysRoot, "devices", "system", "cpu") }
	for name, tc := range map[string]struct{ file, content, want string }{
		"empty sibling list":         {"cpu3/topology/thread_siblings_list", "\n", "no CPU topology for cpu3"},
		"sibling list omits the CPU": {"cpu3/topology/thread_siblings_list", "7\n", "no CPU topology for cpu3"},
		"malformed isolated set":     {"isolated", "x-y\n", "isolated CPU set"},
	} {
		t.Run(name, func(t *testing.T) {
			pinHost(t, fourCores, "0-7", "", "", nil)
			if err := os.WriteFile(filepath.Join(cpuRoot(), tc.file), []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			pin, err := DerivePin()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DerivePin = %+v, %v; want a refusal containing %q", pin, err, tc.want)
			}
		})
	}
}

// The affinity file is the derivation's bound: unreadable or without
// its list, the pin refuses rather than assuming every CPU.
func TestDerivePinRefusesWithoutAffinity(t *testing.T) {
	pinHost(t, fourCores, "0-7", "", "", nil)
	if err := os.WriteFile(pinStatusPath, []byte("Name:\tpew\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DerivePin(); err == nil || !strings.Contains(err.Error(), "CPU affinity") {
		t.Fatalf("no affinity list: %v; want the affinity refusal", err)
	}
	if err := os.Remove(pinStatusPath); err != nil {
		t.Fatal(err)
	}
	if _, err := DerivePin(); err == nil || !strings.Contains(err.Error(), "CPU affinity") {
		t.Fatalf("missing status: %v; want the affinity refusal", err)
	}
}

// rankCores' tiebreak order, pinned on the comparator directly: an
// indicator outranks wholeness, wholeness outranks CPU 0's core, CPU 0's
// core (judged over its full sibling list, not the allowed part)
// outranks the core id.
func TestRankCoresTiebreaks(t *testing.T) {
	pinHost(t, nil, "0-7", "", "0-7", nil)
	ranked, used := rankCores([]core{
		{id: 0, cpus: []int{0, 4}, whole: true, hosts0: true},
		{id: 1, cpus: []int{5}, whole: false, hosts0: false},
		{id: 2, cpus: []int{2, 6}, whole: true, hosts0: false},
		{id: 3, cpus: []int{7}, whole: false, hosts0: true},
	})
	if len(used) != 0 {
		t.Fatalf("indicators used without files: %v", used)
	}
	var order []int
	for _, c := range ranked {
		order = append(order, c.id)
	}
	if want := []int{2, 0, 1, 3}; !slices.Equal(order, want) {
		t.Fatalf("rank order = %v, want %v (whole before partial, CPU 0's core last within each, then highest id)", order, want)
	}
}

func TestParseCPUList(t *testing.T) {
	for in, want := range map[string]string{"": "", "3": "3", "0-3,8,12-15": "0,1,2,3,8,12,13,14,15", "5,1,1-2": "1,2,5"} {
		got, err := parseCPUList(in)
		if err != nil || formatCPUList(got) != want {
			t.Errorf("parseCPUList(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"a", "3-1", "-1", "1-", "1,,2"} {
		if _, err := parseCPUList(in); err == nil {
			t.Errorf("parseCPUList(%q) accepted", in)
		}
	}
}
