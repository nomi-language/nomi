package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func nodesToDocumentSymbols(nodes []ast.Node, file *analysis.FileAnalysis) []protocol.DocumentSymbol {
	var symbols []protocol.DocumentSymbol
	for _, node := range nodes {
		if sym := nodeToSymbol(node); sym != nil {
			symbols = append(symbols, *sym)
		}
	}
	return symbols
}

// symbol builds a document symbol whose Range is the declaration's extent
// (sp) and whose SelectionRange is its name (sel). LSP requires the name
// inside the extent, and VS Code rejects the whole response otherwise, so
// the range is widened to cover sel when they disagree.
func symbol(name string, kind protocol.SymbolKind, detail *string, sp ast.Span, sel protocol.Range) protocol.DocumentSymbol {
	return protocol.DocumentSymbol{
		Name:           name,
		Detail:         detail,
		Kind:           kind,
		Range:          coverRange(spanRange(sp), sel),
		SelectionRange: sel,
	}
}

// spanRange converts a parser extent to a zero-based range. A zero span
// gives the zero range, which coverRange replaces.
func spanRange(sp ast.Span) protocol.Range {
	if sp.IsZero() {
		return protocol.Range{}
	}
	return protocol.Range{
		Start: protocol.Position{Line: toZeroBased(sp.StartLine), Character: toZeroBased(sp.StartCol)},
		End:   protocol.Position{Line: toZeroBased(sp.EndLine), Character: toZeroBased(sp.EndCol)},
	}
}

func posBefore(a, b protocol.Position) bool {
	return a.Line < b.Line || (a.Line == b.Line && a.Character < b.Character)
}

// rangeContains reports whether inner lies within outer.
func rangeContains(outer, inner protocol.Range) bool {
	return !posBefore(inner.Start, outer.Start) && !posBefore(outer.End, inner.End)
}

// coverRange widens r to contain inner. The zero r (no recorded extent)
// becomes inner itself.
func coverRange(r, inner protocol.Range) protocol.Range {
	if r == (protocol.Range{}) {
		return inner
	}
	if posBefore(inner.Start, r.Start) {
		r.Start = inner.Start
	}
	if posBefore(r.End, inner.End) {
		r.End = inner.End
	}
	return r
}

// withChildren adds children to sym and widens sym's range to cover each
// child's, so children nest inside their parent even where an extent is
// missing.
func withChildren(sym *protocol.DocumentSymbol, children []protocol.DocumentSymbol) {
	sym.Children = append(sym.Children, children...)
	for _, c := range children {
		sym.Range = coverRange(sym.Range, c.Range)
	}
}

func nodeToSymbol(node ast.Node) *protocol.DocumentSymbol {
	var sym protocol.DocumentSymbol
	switch n := node.(type) {
	case *ast.FuncDef:
		detail := formatFuncDetail(n)
		sym = symbol(n.Name, protocol.SymbolKindFunction, &detail, n.Span, nameRange(n.Line, n.Col, len(n.Name)))
	case *ast.StructDef:
		sym = symbol(n.Name, protocol.SymbolKindStruct, nil, n.Span, nameRange(n.Line, n.Col, len(n.Name)))
		var children []protocol.DocumentSymbol
		for _, f := range n.Fields {
			detail := typeExprString(f.TypeAnnotation)
			children = append(children, symbol(f.Name, protocol.SymbolKindField, &detail, f.Span, nameRange(f.Line, f.Col, len(f.Name))))
		}
		withChildren(&sym, children)
	case *ast.EnumDef:
		sym = symbol(n.Name, protocol.SymbolKindEnum, nil, n.Span, nameRange(n.Line, n.Col, len(n.Name)))
		var children []protocol.DocumentSymbol
		for _, v := range n.Variants {
			children = append(children, symbol(v.Name, protocol.SymbolKindEnumMember, nil, v.Span, nameRange(v.Line, v.Col, len(v.Name))))
		}
		withChildren(&sym, children)
	case *ast.TypeDef:
		sym = symbol(n.Name, protocol.SymbolKindClass, nil, n.Span, nameRange(n.Line, n.Col, len(n.Name)))
	case *ast.TypeAlias:
		// A bound alias (`typealias ShowAndTag Showable and Tagged`) has no
		// single target; its right-hand side is the bound list.
		var detail string
		if n.TargetTypeExpr != nil {
			detail = n.TargetTypeExpr.TypeString()
		} else {
			parts := make([]string, len(n.Bounds))
			for i, b := range n.Bounds {
				parts[i] = b.TypeString()
			}
			detail = strings.Join(parts, " and ")
		}
		sym = symbol(n.Name, protocol.SymbolKindClass, &detail, n.Span, nameRange(n.Line, n.Col, len(n.Name)))
	case *ast.InterfaceDef:
		sym = symbol(n.Name, protocol.SymbolKindInterface, nil, n.Span, nameRange(n.Line, n.Col, len(n.Name)))
		var children []protocol.DocumentSymbol
		for _, m := range n.Methods {
			children = append(children, symbol(m.Name, protocol.SymbolKindMethod, nil, m.Span, nameRange(m.Line, m.Col, len(m.Name))))
		}
		withChildren(&sym, children)
	case *ast.ExternFunc:
		detail := formatExternFuncDetail(n)
		sym = symbol(n.Name, protocol.SymbolKindFunction, &detail, n.Span, nameRange(n.Line, n.Col, len(n.Name)))
	case *ast.ExternType:
		sym = symbol(n.Name, protocol.SymbolKindClass, nil, n.Span, nameRange(n.Line, n.Col, len(n.Name)))
	case *ast.OnceBinding:
		sym = symbol(n.Name, protocol.SymbolKindConstant, nil, n.Span, nameRange(n.Line, n.Col, len(n.Name)))
	case *ast.ImplBlock:
		// `impl Iface for Type { ... }` is a container symbol whose function
		// items are its children, so the outline surfaces implementation
		// functions (they aren't top-level FuncDefs). Its selection is the
		// `impl` keyword.
		name := "impl " + typeExprString(n.Receiver)
		if n.Interface != nil {
			name = "impl " + typeExprString(n.Interface) + " for " + typeExprString(n.Receiver)
		}
		sym = symbol(name, protocol.SymbolKindObject, nil, n.Span, nameRange(n.Line, n.Col, len("impl")))
		var children []protocol.DocumentSymbol
		for _, item := range n.Items {
			switch it := item.(type) {
			case *ast.FuncDef:
				detail := formatFuncDetail(it)
				children = append(children, symbol(it.Name, protocol.SymbolKindMethod, &detail, it.Span, nameRange(it.Line, it.Col, len(it.Name))))
			case *ast.ExternFunc:
				detail := formatExternFuncDetail(it)
				children = append(children, symbol(it.Name, protocol.SymbolKindMethod, &detail, it.Span, nameRange(it.Line, it.Col, len(it.Name))))
			}
		}
		withChildren(&sym, children)
	case *ast.TestDecl:
		sym = testSymbol(n)
	default:
		return nil
	}
	return &sym
}

// testSymbol is a `test` (a function symbol) or a `tests` group (a module
// symbol holding its tests). The selection is the quoted name.
func testSymbol(n *ast.TestDecl) protocol.DocumentSymbol {
	kind := protocol.SymbolKindFunction
	if n.Group {
		kind = protocol.SymbolKindModule
	}
	sel := nameRange(n.Line, n.Col, len("test"))
	if n.NameLine > 0 {
		sel = nameRange(n.NameLine, n.NameCol, len(n.Name)+2)
	}
	sym := symbol(n.Name, kind, nil, n.Span, sel)
	if n.Group && n.Body != nil {
		var children []protocol.DocumentSymbol
		for _, st := range n.Body.Stmts {
			if t, ok := st.(*ast.TestDecl); ok {
				children = append(children, testSymbol(t))
			}
		}
		withChildren(&sym, children)
	}
	return sym
}

// typeExprString renders a type expression for a symbol label, nil-safe.
func typeExprString(te ast.TypeExpr) string {
	if te == nil {
		return ""
	}
	return te.TypeString()
}

func nameRange(line, col, nameLen int) protocol.Range {
	l := toZeroBased(line)
	c := toZeroBased(col)
	return protocol.Range{
		Start: protocol.Position{Line: l, Character: c},
		End:   protocol.Position{Line: l, Character: c + uint32(nameLen)},
	}
}

func toZeroBased(n int) uint32 {
	if n > 0 {
		return uint32(n - 1)
	}
	return 0
}

func formatExternFuncDetail(f *ast.ExternFunc) string {
	result := "extern ("
	for i, p := range f.Params {
		if i > 0 {
			result += ", "
		}
		result += p.Name
		if p.TypeAnnotation != nil {
			result += ": " + p.TypeAnnotation.TypeString()
		}
	}
	result += ")"
	if f.ReturnTypeExpr != nil {
		result += ": " + f.ReturnTypeExpr.TypeString()
	}
	return result
}

func formatFuncDetail(f *ast.FuncDef) string {
	result := "("
	for i, p := range f.Params {
		if i > 0 {
			result += ", "
		}
		result += p.Name
		if p.TypeAnnotation != nil {
			result += ": " + p.TypeAnnotation.TypeString()
		}
	}
	result += ")"
	if f.ReturnTypeExpr != nil {
		result += ": " + f.ReturnTypeExpr.TypeString()
	}
	return result
}
