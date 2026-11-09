// Package pew carries the module's embedded tool-resident guidance:
// docs/guidance.md is the single home of verb-level served prose
// (what a verb does, what a knob controls, when to use which),
// embedded here because the binary travels while the repository
// stays home, and parsed once for the CLI to project from
// (REQ-pew-guidance in docs/specs/spec.md).
package pew

import (
	_ "embed"

	"github.com/greatliontech/pew/internal/guidance"
)

//go:embed docs/guidance.md
var guidanceSrc []byte

// embeddedGuidance is the source parsed once for every surface to
// project from (internal/guidance.Embedded); Document answers the parse error,
// which GuidanceDocument forwards for cmd/pew to refuse loudly.
var embeddedGuidance = guidance.Embed("pew", guidanceSrc)

// GuidanceDocument is the embedded guidance source's parse answer; a
// malformed document is a build-time defect every consumer surfaces
// loudly.
func GuidanceDocument() (*guidance.Document, error) { return embeddedGuidance.Document() }

// GuidanceRegistration is a verb's served registration on a surface —
// its description, knobless help, and whole section; a verb the
// document lacks is a build-time defect and panics at command construction.
func GuidanceRegistration(surface, verb string) guidance.Registration {
	return embeddedGuidance.MustRegistration(surface, verb)
}

// GuidanceKnob is one knob's served projection on a surface — its
// usage in pflag's grammar among them; a knob the document lacks is a
// build-time defect and panics at command construction.
func GuidanceKnob(surface, verb, name string) guidance.Knob {
	return embeddedGuidance.MustKnob(surface, verb, name)
}
