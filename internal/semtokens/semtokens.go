// Package semtokens derives semantic highlighting tokens from a fully-built
// analysis.FileAnalysis. It is the single source shared by the LSP's
// textDocument/semanticTokens response (lsp/semantic_tokens.go) and the
// language-tour syntax highlighter (cmd/tour-highlight), so both classify
// identifiers the same way — there is no second classifier to drift.
package semtokens

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Token is one classified identifier occurrence (a definition or a reference).
// Positions are 1-based; Length is the identifier name's length in bytes. Type
// is the LSP semantic token-type name (e.g. "function", "parameter").
type Token struct {
	Line, Col, Length int
	Type              string
	Readonly          bool
}

// Collect returns every semantic token in fa, sorted by (line, col).
//
// At most ONE token is emitted per position. A position can appear in both
// fa.References and fa.Definitions — e.g. an embedded enum variant
// (`embeds True`) records a type Reference (walkTypeExpr on the
// EmbeddedTypeExpr) AND a variant-symbol Definition at the same (line, col).
// Emitting both used to leave the winner to unstable sort order, so two
// structurally identical lines could render different colors. The Reference
// wins the tie, matching FileAnalysis.SymbolAt (references-first), so the
// color always agrees with what hover / go-to-definition resolve the name to.
func Collect(fa *analysis.FileAnalysis) []Token {
	var toks []Token
	seen := make(map[analysis.Pos]bool)
	add := func(pos analysis.Pos, sym *analysis.Symbol, reference bool) {
		if seen[pos] {
			return
		}
		// Synthesized @derive bodies sit in a line band past EOF; they have
		// no on-screen text to color.
		if analysis.IsSynthesizedLine(pos.Line) {
			return
		}
		if isHoverOnlySymbol(sym.Kind) {
			return
		}
		if isImportPathModuleSymbol(pos, sym) {
			return
		}
		if isExternalDefinitionSymbol(sym) {
			return
		}
		// `self` is always the receiver-type keyword — in an import
		// (`std/clock.{self}`), in type position (`p: self`), and as a
		// type-qualified call object (`self.method(x)`). It must never get a
		// semantic token: Zed layers semantic tokens OVER tree-sitter, so a
		// token here would override the `(self_type) @keyword` highlight and
		// render `self` as a plain variable. Leave it to the keyword layer in
		// every position. (`internal` is a keyword only as an import-path
		// segment, so its skip stays import-scoped.)
		if sym.Name == "self" {
			return
		}
		if sym.Name == "internal" {
			if _, ok := sym.Node.(*ast.ImportStmt); ok {
				return
			}
		}
		kindSym := sym
		if reference && sym.Kind == analysis.SymbolBinding {
			if def := fa.Definitions[pos]; def != nil {
				kindSym = def
			}
		}
		kind := semanticKind(kindSym)
		// Fields are fully covered by syntax queries in each editor surface
		// (`@property` in Zed/tour, `@variable.other.member` in Helix). Emitting
		// semantic field tokens repaints those editor-native captures and can
		// make fields look plain in themes that do not style LSP `property`.
		if kind == analysis.SymbolField {
			return
		}
		if ast.IsDiscardName(sym.Name) &&
			(kind == analysis.SymbolBinding || kind == analysis.SymbolParam) {
			return
		}
		if isExternPackageHandle(sym) {
			if pkg, ok := sym.Node.(*ast.ExternPackage); ok {
				if pkg.AliasLine == 0 || (pos.Line == pkg.ImportPathLine && pos.Col == pkg.ImportPathCol) {
					return
				}
			}
			if !reference {
				switch pkg := sym.Node.(type) {
				case *ast.ExternPackage:
					if pos.Line != pkg.AliasLine || pos.Col != pkg.AliasCol {
						return
					}
				case *ast.ImportStmt:
					return
				default:
					return
				}
			}
			toks = append(toks, Token{
				Line:   pos.Line,
				Col:    pos.Col,
				Length: symbolSpan(pos, sym, reference, fa.References),
				Type:   "namespace",
			})
			seen[pos] = true
			return
		}
		t, ok := kindToType(kind)
		if !ok {
			return
		}
		toks = append(toks, Token{
			Line:     pos.Line,
			Col:      pos.Col,
			Length:   symbolSpan(pos, sym, reference, fa.References),
			Type:     t,
			Readonly: kind == analysis.SymbolOnce,
		})
		seen[pos] = true
	}
	// References before Definitions — see the tie-break note above.
	for pos, sym := range fa.References {
		add(pos, sym, true)
	}
	for pos, sym := range fa.Definitions {
		add(pos, sym, false)
	}
	sort.Slice(toks, func(i, j int) bool {
		if toks[i].Line != toks[j].Line {
			return toks[i].Line < toks[j].Line
		}
		return toks[i].Col < toks[j].Col
	})
	return toks
}

func symbolSpan(pos analysis.Pos, sym *analysis.Symbol, reference bool, refs map[analysis.Pos]*analysis.Symbol) int {
	if sym.Span > 0 {
		return sym.Span
	}
	if reference {
		if span := dottedReferenceSegmentSpan(pos, sym, refs); span > 0 {
			return span
		}
	}
	return len(sym.Name)
}

func dottedReferenceSegmentSpan(pos analysis.Pos, sym *analysis.Symbol, refs map[analysis.Pos]*analysis.Symbol) int {
	parts := strings.Split(sym.Name, ".")
	if len(parts) < 2 {
		return 0
	}
	for _, part := range parts[:len(parts)-1] {
		next := analysis.Pos{File: pos.File, Line: pos.Line, Col: pos.Col + len(part) + 1}
		if sameReferenceSymbol(refs[next], sym) {
			return len(part)
		}
	}
	return len(parts[len(parts)-1])
}

func sameReferenceSymbol(a, b *analysis.Symbol) bool {
	if a == nil || b == nil {
		return false
	}
	return a == b || (a.Name == b.Name && a.Kind == b.Kind)
}

func isImportPathModuleSymbol(pos analysis.Pos, sym *analysis.Symbol) bool {
	if sym.Kind != analysis.SymbolModule {
		return false
	}
	stmt, ok := sym.Node.(*ast.ImportStmt)
	if !ok {
		return false
	}
	for _, seg := range stmt.ModulePath {
		if seg == nil || seg.LineNum() != pos.Line || importNodeCol(seg) != pos.Col {
			continue
		}
		return true
	}
	return false
}

func isExternalDefinitionSymbol(sym *analysis.Symbol) bool {
	if sym.DefinitionFile == "" {
		return false
	}
	switch sym.Node.(type) {
	case *ast.ExternFunc, *ast.ExternType:
		return true
	default:
		return false
	}
}

func isExternPackageHandle(sym *analysis.Symbol) bool {
	if sym == nil {
		return false
	}
	_, ok := sym.Node.(*ast.ExternPackage)
	return ok
}

func semanticKind(sym *analysis.Symbol) analysis.SymbolKind {
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil {
		return analysis.SymbolBinding
	}
	return sym.Kind
}

func importNodeCol(n ast.Node) int {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Col
	case *ast.TypeIdent:
		return v.Col
	default:
		return 0
	}
}

func isHoverOnlySymbol(k analysis.SymbolKind) bool {
	switch k {
	case analysis.SymbolArgHint,
		analysis.SymbolAssertion,
		analysis.SymbolTestSetup,
		analysis.SymbolTestDecl,
		analysis.SymbolTryOp,
		analysis.SymbolControlFlow,
		analysis.SymbolImplKeyword,
		analysis.SymbolLiteral:
		return true
	default:
		return false
	}
}

// kindToType maps an analysis SymbolKind to an LSP semantic token-type name,
// returning false for kinds that should not be tokenized (e.g. arg hints).
func kindToType(k analysis.SymbolKind) (string, bool) {
	switch k {
	case analysis.SymbolFunction, analysis.SymbolInterfaceMethod:
		return "function", true
	case analysis.SymbolStruct:
		return "struct", true
	case analysis.SymbolEnum:
		return "enum", true
	case analysis.SymbolEnumVariant:
		return "enumMember", true
	case analysis.SymbolType, analysis.SymbolTypeAlias:
		return "type", true
	case analysis.SymbolInterface:
		return "interface", true
	case analysis.SymbolParam:
		return "parameter", true
	case analysis.SymbolBinding, analysis.SymbolOnce:
		return "variable", true
	case analysis.SymbolField:
		return "property", true
	case analysis.SymbolModule:
		return "module", true
	default:
		return "", false
	}
}

// AttachedTestLines returns the 1-based lines that the `//!` tests attached
// to nodes' declarations occupy, impl-block items included: each test's
// first prompt line through the last line of its body.
//
// The LSP marks tokens on these lines with its attachedTest modifier, so an
// editor can draw attached tests dimmed, as it draws comments, without
// losing the token's type.
func AttachedTestLines(nodes []ast.Node) map[int]bool {
	lines := make(map[int]bool)
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		for _, t := range ast.AttachedTestsOf(n) {
			end := t.EndLine
			if end < t.Line {
				end = t.Line
			}
			for line := t.Line; line <= end; line++ {
				lines[line] = true
			}
		}
		var items []ast.Node
		switch v := n.(type) {
		case *ast.StructDef:
			items = v.Items
		case *ast.EnumDef:
			items = v.Items
		case *ast.TypeDef:
			items = v.Items
		case *ast.ExternType:
			items = v.Items
		case *ast.ImplBlock:
			items = v.Items
		}
		for _, item := range items {
			visit(item)
		}
	}
	for _, n := range nodes {
		visit(n)
	}
	return lines
}
