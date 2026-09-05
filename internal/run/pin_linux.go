//go:build linux

package run

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// pinStatusPath is the process's own status file, whose Cpus_allowed_list
// is the affinity mask a container or cgroup left this process (a pin
// outside it fails at taskset). A var so tests point it at a fixture.
var pinStatusPath = "/proc/self/status"

// DerivePin derives the CPU set a pinned measurement runs on from what
// the kernel exposes about this host (spec §9): the process's affinity
// mask intersected with the online CPUs bounds every choice; inside it,
// a non-empty isolated set (the operator's designated benchmark CPUs,
// isolcpus) is taken whole; otherwise the allowed CPUs group into
// physical cores by thread siblings and one whole core is chosen — the
// highest-ranked by the kernel's exported per-CPU performance
// indicators (cpu_capacity, cpuinfo_max_freq, amd_pstate_highest_perf;
// an indicator absent on any core or uniform across them says nothing
// and is dropped), ties broken toward a core whose every sibling is
// allowed (a sibling outside the mask is another workload's seat on the
// same execution units), away from CPU 0's core (the default interrupt
// target), then toward the highest core id. A whole core keeps the
// benchmark and the runtime's helpers on one core's cache with no
// cross-core migration. The choice is never guessed: a host
// with no readable topology, or with only one allowed core (nothing
// left free), refuses with the reason.
func DerivePin() (Pin, error) {
	allowed, err := allowedCPUs()
	if err != nil {
		return Pin{}, err
	}
	// An absent isolated file is a kernel without the attribute; a
	// present one that does not parse is a topology that cannot be read.
	isolated, err := readCPUList(sysPath("devices/system/cpu/isolated"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Pin{}, fmt.Errorf("cannot read the isolated CPU set: %w", err)
	}
	if set := intersect(isolated, allowed); len(set) > 0 {
		return Pin{CPUs: set, Reason: "the kernel's isolated CPU set"}, nil
	}
	cores, err := coresOf(allowed)
	if err != nil {
		return Pin{}, err
	}
	if len(cores) < 2 {
		return Pin{}, fmt.Errorf("pinning needs a second core to leave free; this process may use %d core(s) (CPUs %s)", len(cores), formatCPUList(allowed))
	}
	ranked, indicators := rankCores(cores)
	best := ranked[0]
	reason := fmt.Sprintf("core %d, CPUs %s", best.id, formatCPUList(best.cpus))
	if len(indicators) > 0 {
		reason += ": the highest-ranked core by " + strings.Join(indicators, ", ")
	} else {
		reason += ": every core ranks alike"
	}
	if !best.whole {
		reason += ", its allowed siblings only"
	}
	if !best.hosts0 {
		reason += ", outside CPU 0's core"
	}
	return Pin{CPUs: best.cpus, Reason: reason}, nil
}

// core is one physical core as its allowed thread siblings; id is the
// kernel's core_id when readable, else the lowest CPU.
type core struct {
	id     int
	cpus   []int   // the allowed siblings
	whole  bool    // every thread sibling is allowed
	hosts0 bool    // CPU 0 is a sibling, allowed or not
	rank   []int64 // one value per surviving indicator, in indicator order
}

// pinIndicators are the per-CPU performance indicators the kernel
// exports, in ranking precedence: the scheduler's own capacity scale
// (asymmetric arm64 and hybrid x86), then the cpufreq ceiling (hybrid
// Intel), then amd-pstate's preferred-core score (hybrid AMD, whose
// nominal ceilings read alike).
var pinIndicators = []struct{ name, rel string }{
	{"cpu_capacity", "cpu_capacity"},
	{"cpuinfo_max_freq", "cpufreq/cpuinfo_max_freq"},
	{"amd_pstate_highest_perf", "cpufreq/amd_pstate_highest_perf"},
}

// rankCores orders cores best first and names the indicators the order
// used. An indicator counts only when every core reads it and the
// values differ somewhere; the rest is the whole-core, CPU-0, and
// core-id tiebreak.
func rankCores(cores []core) ([]core, []string) {
	var used []string
	for _, ind := range pinIndicators {
		values := make([]int64, len(cores))
		readable, uniform := true, true
		for i, c := range cores {
			b, err := os.ReadFile(sysPath(fmt.Sprintf("devices/system/cpu/cpu%d/%s", c.cpus[0], ind.rel)))
			if err != nil {
				readable = false
				break
			}
			v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
			if err != nil {
				readable = false
				break
			}
			values[i] = v
			if v != values[0] {
				uniform = false
			}
		}
		if !readable || uniform {
			continue
		}
		used = append(used, ind.name)
		for i := range cores {
			cores[i].rank = append(cores[i].rank, values[i])
		}
	}
	sort.SliceStable(cores, func(i, j int) bool {
		a, b := cores[i], cores[j]
		for k := range a.rank {
			if a.rank[k] != b.rank[k] {
				return a.rank[k] > b.rank[k]
			}
		}
		if a.whole != b.whole {
			return a.whole
		}
		if a.hosts0 != b.hosts0 {
			return !a.hosts0
		}
		return a.id > b.id
	})
	return cores, used
}

// coresOf groups the allowed CPUs into physical cores by their thread
// siblings; a CPU whose topology is unreadable refuses the derivation.
func coresOf(allowed []int) ([]core, error) {
	byKey := map[string]*core{}
	var order []string
	for _, cpu := range allowed {
		base := fmt.Sprintf("devices/system/cpu/cpu%d/topology/", cpu)
		siblings, err := readCPUList(sysPath(base + "thread_siblings_list"))
		if err == nil && !slices.Contains(siblings, cpu) {
			err = errors.New("the sibling list omits the CPU itself")
		}
		if err != nil {
			return nil, fmt.Errorf("no CPU topology for cpu%d under %s: %w", cpu, sysPath("devices/system/cpu"), err)
		}
		hosts0 := slices.Contains(siblings, 0)
		whole := len(intersect(siblings, allowed)) == len(siblings)
		siblings = intersect(siblings, allowed)
		key := formatCPUList(siblings)
		if _, seen := byKey[key]; seen {
			continue
		}
		id := siblings[0]
		if b, err := os.ReadFile(sysPath(base + "core_id")); err == nil {
			if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				id = v
			}
		}
		byKey[key] = &core{id: id, cpus: siblings, whole: whole, hosts0: hosts0}
		order = append(order, key)
	}
	cores := make([]core, 0, len(order))
	for _, key := range order {
		cores = append(cores, *byKey[key])
	}
	return cores, nil
}

// allowedCPUs is the process's affinity mask intersected with the online
// CPUs; either file unreadable refuses the derivation.
func allowedCPUs() ([]int, error) {
	f, err := os.Open(pinStatusPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read the process's CPU affinity: %w", err)
	}
	defer f.Close()
	var mask []int
	found := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "Cpus_allowed_list:"); ok {
			mask, err = parseCPUList(strings.TrimSpace(rest))
			if err != nil {
				return nil, fmt.Errorf("cannot read the process's CPU affinity: %w", err)
			}
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("cannot read the process's CPU affinity: no Cpus_allowed_list in %s", pinStatusPath)
	}
	online, err := readCPUList(sysPath("devices/system/cpu/online"))
	if err != nil {
		return nil, fmt.Errorf("cannot read the online CPUs: %w", err)
	}
	allowed := intersect(mask, online)
	if len(allowed) == 0 {
		return nil, errors.New("the process's CPU affinity holds no online CPU")
	}
	return allowed, nil
}

func readCPUList(path string) ([]int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseCPUList(strings.TrimSpace(string(b)))
}

// parseCPUList parses the kernel's list format ("0-3,8,12-15"; empty is
// the empty set) into ascending distinct CPUs.
func parseCPUList(s string) ([]int, error) {
	if s == "" {
		return nil, nil
	}
	seen := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		from, err := strconv.Atoi(lo)
		if err != nil || from < 0 {
			return nil, fmt.Errorf("malformed CPU list %q", s)
		}
		to := from
		if isRange {
			if to, err = strconv.Atoi(hi); err != nil || to < from {
				return nil, fmt.Errorf("malformed CPU list %q", s)
			}
		}
		for cpu := from; cpu <= to; cpu++ {
			seen[cpu] = true
		}
	}
	out := make([]int, 0, len(seen))
	for cpu := range seen {
		out = append(out, cpu)
	}
	sort.Ints(out)
	return out, nil
}

func intersect(a, b []int) []int {
	in := make(map[int]bool, len(b))
	for _, x := range b {
		in[x] = true
	}
	var out []int
	for _, x := range a {
		if in[x] {
			out = append(out, x)
		}
	}
	return out
}
