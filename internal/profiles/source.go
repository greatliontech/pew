package profiles

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/google/pprof/profile"
)

func sampleFrames(s *profile.Sample, sources []Source, resolved map[profile.Line]Weight) []Weight {
	var frames []Weight
	for _, loc := range s.Location {
		if len(loc.Line) == 0 {
			frames = append(frames, Weight{Symbol: "[unknown]", Attribution: "unresolved"})
			continue
		}
		for _, line := range loc.Line {
			if w, ok := resolved[line]; ok {
				frames = append(frames, w)
				continue
			}
			w := Weight{Symbol: "[unknown]", Attribution: "unresolved"}
			if f := line.Function; f != nil {
				if f.Name != "" {
					w.Symbol = f.Name
				}
				for _, src := range sources {
					if exactSource(src, f, line.Line) {
						w.Source, w.Attribution = src.ModuleRelative, "exact-source"
						break
					}
				}
			}
			resolved[line] = w
			frames = append(frames, w)
		}
	}
	if len(frames) == 0 {
		frames = append(frames, Weight{Symbol: "[unknown]", Attribution: "unresolved"})
	}
	return frames
}

// Only ordinary Go declarations with an exact physical filename and physical
// line range are admitted. No suffix stripping, trimpath search or external reads.
func exactSource(s Source, f *profile.Function, line int64) bool {
	if s.Package == "" || s.ModuleRelative == "" || s.Filename != f.Filename || line <= 0 || f.StartLine <= 0 || bytes.Contains(s.Bytes, []byte("//line ")) || bytes.Contains(s.Bytes, []byte("/*line ")) {
		return false
	}
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, s.Filename, s.Bytes, parser.ParseComments)
	if err != nil || ast.IsGenerated(file) {
		return false
	}
	for _, imp := range file.Imports {
		if imp.Path.Value == `"C"` {
			return false
		}
	}
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Type.TypeParams != nil {
			continue
		}
		symbol := s.Package + "." + fn.Name.Name
		if fn.Recv != nil {
			r := fn.Recv.List[0].Type
			prefix := ""
			if star, ok := r.(*ast.StarExpr); ok {
				prefix = "*"
				r = star.X
			}
			id, ok := r.(*ast.Ident)
			if !ok {
				continue
			}
			recv := prefix + id.Name
			if prefix != "" {
				recv = "(" + recv + ")"
			}
			symbol = s.Package + "." + recv + "." + fn.Name.Name
		}
		start, end := int64(fs.PositionFor(fn.Pos(), false).Line), int64(fs.PositionFor(fn.End(), false).Line)
		if f.Name == symbol && f.StartLine == start && line >= start && line <= end && !strings.Contains(f.Name, "[go.shape.") {
			return true
		}
	}
	return false
}
