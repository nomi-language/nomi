package lsp

import (
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// textDocument/inlineValue (LSP 3.17), which an editor asks while a debug
// session is paused: the values to show beside the code. The answer is a
// variable lookup at each occurrence of a parameter or local binding in
// the function the program is paused in, from the function's first line to
// the line it is paused on. The editor looks each name up in the paused
// frame's variables, so a name the frame does not have shows nothing.

// InlineValueParams are textDocument/inlineValue's parameters.
type InlineValueParams struct {
	TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
	Range        protocol.Range                  `json:"range"`
	Context      InlineValueContext              `json:"context"`
}

// InlineValueContext is where the debugger is paused.
type InlineValueContext struct {
	FrameID         int            `json:"frameId"`
	StoppedLocation protocol.Range `json:"stoppedLocation"`
}

// InlineValueVariableLookup asks the editor to show a variable's value,
// found by name in the paused frame, at Range.
type InlineValueVariableLookup struct {
	Range               protocol.Range `json:"range"`
	VariableName        string         `json:"variableName,omitempty"`
	CaseSensitiveLookup bool           `json:"caseSensitiveLookup"`
}

func (s *Server) textDocumentInlineValue(_ *glsp.Context, params *InlineValueParams) ([]InlineValueVariableLookup, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil || doc.Analysis == nil {
		return []InlineValueVariableLookup{}, nil
	}
	out := inlineValues(doc.Analysis, doc.Nodes,
		int(params.Range.Start.Line)+1, int(params.Range.End.Line)+1,
		int(params.Context.StoppedLocation.Start.Line)+1)
	lines := s.lines.get(uri, doc.Content)
	for i := range out {
		out[i].Range = lines.utf16Range(out[i].Range)
	}
	return out, nil
}

// inlineValues are the lookups for the lines first to last (1-based) of a
// file paused on line stop: each parameter and local binding of the
// innermost function or test holding stop, on lines from its start to
// stop. Ranges are in byte columns.
func inlineValues(fa *analysis.FileAnalysis, nodes []ast.Node, first, last, stop int) []InlineValueVariableLookup {
	start, found := enclosingBody(nodes, stop)
	if !found {
		return []InlineValueVariableLookup{}
	}
	first, last = max(first, start), min(last, stop)
	type occ struct {
		pos  analysis.Pos
		name string
	}
	seen := map[[2]any]bool{}
	var occs []occ
	add := func(pos analysis.Pos, sym *analysis.Symbol) {
		if sym == nil || (sym.Kind != analysis.SymbolParam && sym.Kind != analysis.SymbolBinding) ||
			pos.Line < first || pos.Line > last || analysis.IsSynthesizedLine(pos.Line) ||
			ast.IsDiscardName(sym.Name) || sym.Name == "" {
			return
		}
		key := [2]any{pos.Line, sym.Name}
		if seen[key] {
			return
		}
		seen[key] = true
		occs = append(occs, occ{pos: pos, name: sym.Name})
	}
	for pos, sym := range fa.Definitions {
		add(pos, sym)
	}
	for pos, sym := range fa.References {
		add(pos, sym)
	}
	sort.Slice(occs, func(i, j int) bool {
		if occs[i].pos.Line != occs[j].pos.Line {
			return occs[i].pos.Line < occs[j].pos.Line
		}
		return occs[i].pos.Col < occs[j].pos.Col
	})
	out := make([]InlineValueVariableLookup, 0, len(occs))
	for _, o := range occs {
		out = append(out, InlineValueVariableLookup{Range: makeLocation("", o.pos, o.name).Range,
			VariableName: o.name, CaseSensitiveLookup: true})
	}
	return out
}

// enclosingBody is the first line of the innermost function or test
// declaration in nodes that holds line.
func enclosingBody(nodes []ast.Node, line int) (int, bool) {
	best := 0
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			var span ast.Span
			switch t := n.(type) {
			case *ast.FuncDef:
				span = t.Span
			case *ast.TestDecl:
				span = t.GetSpan()
			default:
				return true
			}
			if !span.IsZero() && span.StartLine <= line && line <= span.EndLine && span.StartLine > best {
				best = span.StartLine
			}
			return true
		})
	}
	return best, best > 0
}
