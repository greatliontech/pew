package run

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GoFlagFields reads cmd/go's quoted-field grammar, not shell syntax.
func GoFlagFields(text string) ([]string, error) {
	var fields []string
	for {
		text = strings.TrimLeft(text, " \t\r\n")
		if text == "" {
			return fields, nil
		}
		if quote := text[0]; quote == '\'' || quote == '"' {
			end := strings.IndexByte(text[1:], quote)
			if end < 0 {
				return nil, fmt.Errorf("run: unterminated GOFLAGS quote")
			}
			fields = append(fields, text[1:end+1])
			text = text[end+2:]
		} else {
			end := strings.IndexAny(text, " \t\r\n")
			if end < 0 {
				return append(fields, text), nil
			}
			fields = append(fields, text[:end])
			text = text[end:]
		}
	}
}

// HarnessArgs preserves supported inherited test-binary policy for a retained
// executable. Pew's explicit selection and effort override inherited spellings.
// Unclassified or execution-changing policy refuses rather than disappearing.
func HarnessArgs(goflags string) ([]string, error) {
	fields, err := GoFlagFields(goflags)
	if err != nil {
		return nil, err
	}
	forward := map[string]string{"timeout": "10m0s", "paniconexit0": "true"}
	driverModes := map[string]bool{}
	for _, field := range fields {
		if !strings.HasPrefix(field, "-") {
			return nil, fmt.Errorf("run: malformed GOFLAGS field %q", field)
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(field, "-"), "=")
		prefixed := strings.HasPrefix(name, "test.")
		name = strings.TrimPrefix(name, "test.")
		switch name {
		case "run", "bench", "benchmem", "benchtime", "count":
		case "short", "failfast", "fullpath", "v":
			if !hasValue {
				value = "true"
			}
			boolean, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("run: invalid GOFLAGS boolean %s", field)
			}
			forward[name] = strconv.FormatBool(boolean)
		case "timeout":
			if _, err := time.ParseDuration(value); !hasValue || err != nil {
				return nil, fmt.Errorf("run: invalid GOFLAGS timeout %q", field)
			}
			forward[name] = value
		case "parallel":
			n, err := strconv.ParseInt(value, 0, strconv.IntSize)
			if !hasValue || err != nil {
				return nil, fmt.Errorf("run: invalid GOFLAGS parallelism %q", field)
			}
			forward[name] = strconv.FormatInt(n, 10)
		case "shuffle":
			if value != "on" && value != "off" {
				if _, err := strconv.ParseInt(value, 10, 64); err != nil {
					return nil, fmt.Errorf("run: invalid GOFLAGS shuffle %q", field)
				}
			}
			forward[name] = value
		case "json", "n", "c":
			// cmd/go accepts explicit false as ordinary execution. These
			// are driver flags, never test-binary arguments.
			if !hasValue {
				value = "true"
			}
			boolean, err := strconv.ParseBool(value)
			if prefixed || err != nil {
				return nil, fmt.Errorf("run: invalid GOFLAGS driver mode %q", field)
			}
			driverModes[name] = boolean
		case "cpu", "skip", "list", "fuzz", "fuzztime", "fuzzminimizetime", "artifacts", "cpuprofile", "memprofile", "memprofilerate", "blockprofile", "blockprofilerate", "mutexprofile", "mutexprofilefraction", "trace", "outputdir", "testlogfile", "paniconexit0", "exec", "o", "C":
			return nil, fmt.Errorf("run: GOFLAGS %q changes the owned execution model; use Pew's workload, pin or profile controls", field)
		case "a", "p", "race", "msan", "asan", "work", "x", "asmflags", "buildmode", "buildvcs", "compiler", "gccgoflags", "gcflags", "installsuffix", "ldflags", "linkshared", "mod", "modcacherw", "modfile", "overlay", "pgo", "pkgdir", "tags", "trimpath", "toolexec", "cover", "covermode", "coverpkg", "vet", "e":
			if prefixed {
				return nil, fmt.Errorf("run: unsupported GOFLAGS test option %q", field)
			}
		default:
			return nil, fmt.Errorf("run: unclassified GOFLAGS option %q; cannot omit it from retained-binary execution", field)
		}
	}
	for _, mode := range []string{"c", "json", "n"} {
		if driverModes[mode] {
			return nil, fmt.Errorf("run: effective GOFLAGS -%s changes the owned execution model", mode)
		}
	}
	if value, present := forward["parallel"]; present {
		n, _ := strconv.Atoi(value) // Every occurrence was parsed and normalized above.
		if n <= 0 {
			return nil, fmt.Errorf("run: effective GOFLAGS parallelism %q is not positive", value)
		}
	}
	keys := make([]string, 0, len(forward))
	for key := range forward {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys))
	for _, key := range keys {
		args = append(args, "-test."+key+"="+forward[key])
	}
	return args, nil
}
