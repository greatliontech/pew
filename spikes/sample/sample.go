// Package sample is a target for the closure-analysis spike. It exercises the
// cases the spec's §7 closure model must handle: an in-package interface
// dispatch, a reflect call (A' widening trigger), a referenced const (INV-7),
// and a user type handed to a stdlib function via an interface (the std
// callback / escape-rule case).
package sample

import (
	"reflect"
	"sort"
	"strings"
)

// Prefix is a package-level const referenced by reachable code (INV-7 target).
const Prefix = "pew-"

type Greeter interface{ Greet(name string) string }

type formal struct{}

func (formal) Greet(name string) string { return Prefix + strings.ToUpper(name) }

type casual struct{}

func (casual) Greet(name string) string { return Prefix + strings.ToLower(name) }

func pickGreeter(useFormal bool) Greeter {
	if useFormal {
		return formal{}
	}
	return casual{}
}

// Run dispatches through an interface entirely within this package. CHA should
// over-approximate the call to include BOTH formal.Greet and casual.Greet.
func Run(name string, useFormal bool) string {
	return pickGreeter(useFormal).Greet(name)
}

// UsesReflect triggers the reflect (A') widening path.
func UsesReflect(v any) string { return reflect.TypeOf(v).String() }

// byLen is a user type handed to the stdlib via sort.Interface. The dispatch to
// Less happens INSIDE sort.Sort (stdlib). Whether byLen.Less shows up as
// reachable tests if we need the escape rule (§7.1) or full stdlib syntax.
type byLen []string

func (s byLen) Len() int           { return len(s) }
func (s byLen) Less(i, j int) bool { return len(s[i]) < len(s[j]) }
func (s byLen) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func SortByLen(ss []string) { sort.Sort(byLen(ss)) }
