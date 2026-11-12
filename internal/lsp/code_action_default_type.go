package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// "Remove type annotation" and "Add type annotation" (refactor.rewrite),
// on the field or parameter under the cursor whose default can give it its
// type (analysis/default_type.go). Neither comes with a diagnostic or a
// hint, and neither is part of source.fixAll.
//
//   - "Remove type annotation" turns `limit: Int = 10` into `limit = 10`.
//     It is offered when the default, checked with no expected type as the
//     inference checks it, checks and has exactly the written type
//     (analysis.DefaultTypeAlone, analysis.SameDefaultType), written as
//     the file spells that type (not an alias of it): not for
//     `since: Maybe<Date> = None`, `[]`, `[]` or a `.Variant`, which need
//     the annotation, nor for `x: Display = 5` or a checked literal whose
//     written type is not the default's own.
//   - "Add type annotation" turns `limit = 10` into `limit: Int = 10`,
//     writing the type the inlay hint shows as the file names it
//     (analysis.SpellType).
//
// Both cover struct and record-variant fields and the parameters of
// functions, impl and namespace items, interface default functions,
// nested functions and lambdas.

// defaultTypeSite is one field or parameter written with a default.
type defaultTypeSite struct {
	name      string
	line, col int
	annotated bool
	value     ast.Node
	// owner is the DefaultOwner of the top-level node holding the site.
	owner string
	// field is true for a field, false for a parameter.
	field bool
}

// buildDefaultTypeActions answers the annotation actions for the field or
// parameter at rng's start.
func buildDefaultTypeActions(content string, nodes []ast.Node, fa *analysis.FileAnalysis, uri string, rng protocol.Range) []protocol.CodeAction {
	if fa == nil {
		return nil
	}
	lines := newLineIndex(content)
	if int(rng.Start.Line) >= len(lines.starts) {
		return nil
	}
	cursorLine := int(rng.Start.Line) + 1
	cursor := posToOffset(lines.starts, cursorLine, lines.byteCol(rng.Start.Line, rng.Start.Character))
	var site defaultTypeSite
	var nameEnd, eqStart int
	found := false
	for _, cand := range defaultTypeSitesOn(nodes, cursorLine) {
		start := posToOffset(lines.starts, cand.line, cand.col)
		end := start + len(cand.name)
		if end > len(content) || content[start:end] != cand.name {
			continue
		}
		eqS, eqE, ok := defaultEqualsSign(content, end, cand.annotated)
		if ok && start <= cursor && cursor <= eqE {
			site, nameEnd, eqStart, found = cand, end, eqS, true
			break
		}
	}
	if !found {
		return nil
	}
	sym := fa.Definitions[analysis.Pos{Line: site.line, Col: site.col}]
	wantKind := analysis.SymbolParam
	if site.field {
		wantKind = analysis.SymbolField
	}
	if sym == nil || sym.Kind != wantKind || sym.Name != site.name || sym.Type == nil {
		return nil
	}
	var title string
	var edit protocol.TextEdit
	if site.annotated {
		alone, ok := fa.DefaultTypeAlone(nodes, site.owner, site.value)
		if !ok || !analysis.SameDefaultType(sym.Type, alone) {
			return nil
		}
		// The annotation must also be the default's type as the file
		// spells it: `n: Count = 1`, with Count an alias of Int, names
		// something the bare default does not.
		spelled, ok := fa.SpellType(nodes, alone)
		written := strings.TrimPrefix(strings.TrimSpace(content[nameEnd:eqStart]), ":")
		if !ok || withoutSpaces(written) != withoutSpaces(spelled) {
			return nil
		}
		title = "Remove type annotation"
		edit = rangeEdit(content, nameEnd, eqStart, " ")
	} else {
		spelled, ok := fa.SpellType(nodes, sym.Type)
		if !ok {
			return nil
		}
		title = "Add type annotation"
		edit = rangeEdit(content, nameEnd, nameEnd, ": "+spelled)
	}
	kind := protocol.CodeActionKindRefactorRewrite
	return []protocol.CodeAction{{
		Title: title,
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentUri][]protocol.TextEdit{
				protocol.DocumentUri(uri): {edit},
			},
		},
	}}
}

// withoutSpaces is s with its spaces, tabs and line breaks removed.
func withoutSpaces(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// defaultTypeSitesOn is every field and parameter with a default whose
// name is on line.
func defaultTypeSitesOn(nodes []ast.Node, line int) []defaultTypeSite {
	if line <= 0 || analysis.IsSynthesizedLine(line) {
		return nil
	}
	var out []defaultTypeSite
	for _, top := range nodes {
		owner := analysis.DefaultOwner(top)
		fields := func(fs []ast.StructField) {
			for _, f := range fs {
				if f.Default != nil && f.Line == line {
					out = append(out, defaultTypeSite{name: f.Name, line: f.Line, col: f.Col, annotated: f.TypeAnnotation != nil, value: f.Default, owner: owner, field: true})
				}
			}
		}
		params := func(ps []ast.Param) {
			for _, p := range ps {
				if p.Default != nil && p.Line == line && p.Destructure == nil && !p.TypeOnly {
					out = append(out, defaultTypeSite{name: p.Name, line: p.Line, col: p.Col, annotated: p.TypeAnnotation != nil, value: p.Default, owner: owner})
				}
			}
		}
		ast.Inspect(top, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.StructDef:
				fields(d.Fields)
			case *ast.EnumDef:
				for _, v := range d.Variants {
					fields(v.Fields)
				}
			case *ast.FuncDef:
				params(d.Params)
			case *ast.ExternFunc:
				params(d.Params)
			case *ast.Lambda:
				params(d.Params)
			case *ast.InterfaceDef:
				for _, m := range d.Methods {
					params(m.Params)
				}
			}
			return true
		})
	}
	return out
}

// defaultEqualsSign finds the `=` that starts the default after a field's
// or parameter's name ending at nameEnd, on the name's line: the first one
// outside brackets, after `: Type` when annotated is set and right after
// the name otherwise. It answers the `=`'s byte offsets.
func defaultEqualsSign(content string, nameEnd int, annotated bool) (start, end int, ok bool) {
	lineEnd := strings.IndexByte(content[nameEnd:], '\n')
	if lineEnd < 0 {
		lineEnd = len(content) - nameEnd
	}
	rest := content[nameEnd : nameEnd+lineEnd]
	toks := lexer.Lex(rest)
	depth := 0
	for i, t := range toks {
		if t.Line != 1 {
			break
		}
		if i == 0 && annotated != (t.Type == token.COLON) {
			return 0, 0, false
		}
		switch t.Type {
		case token.LPAREN, token.LBRACKET, token.LBRACE, token.LT:
			depth++
		case token.RPAREN, token.RBRACKET, token.RBRACE, token.GT:
			depth--
		case token.EQ:
			if depth == 0 {
				off := nameEnd + t.Col - 1
				return off, off + 1, true
			}
		}
	}
	return 0, 0, false
}
