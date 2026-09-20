// Package pew carries the module's embedded tool-resident guidance:
// docs/guidance.md is the single home of verb-level served prose
// (what a verb does, what a knob controls, when to use which),
// embedded here because the binary travels while the repository
// stays home, and parsed once for the CLI to project from (gofresh
// docs/specs/guidance.md is the format contract; this module's
// serving contract is REQ-pew-guidance in docs/specs/spec.md).
package pew

import (
	_ "embed"

	"github.com/greatliontech/gofresh/guidance"
)

//go:embed docs/guidance.md
var guidanceSrc []byte

// embeddedGuidance is the source parsed once for every surface to
// project from (gofresh's Embedded); Document answers the parse error,
// which GuidanceDocument forwards for cmd/pew to refuse loudly.
var embeddedGuidance = guidance.Embed("pew", guidanceSrc)

// GuidanceDocument is the embedded guidance source's parse answer; a
// malformed document is a build-time defect every consumer surfaces
// loudly.
func GuidanceDocument() (*guidance.Document, error) { return embeddedGuidance.Document() }
