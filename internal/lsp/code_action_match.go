package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// "Pattern match on ..." writes a `case` over a value whose type is an enum
// (a user enum, Maybe or Result, generic payloads included), with an arm
// per variant in the "Add missing arms" fix's layout and the fill
// placeholder as each arm's body:
//
//   - on a binding's name, the `case` goes on the lines after the binding;
//   - on a call, the `case` wraps the call.

func (r *refactorRequest) patternMatches() []offer {
	var out []offer
	if b := r.bindingAtName(); b != nil {
		if et := enumOf(r.fa.ExprTypes[b.Value]); et != nil && len(et.Variants) > 0 {
			start, end, ok := r.span(b)
			if ok {
				indent := r.lineIndentOf(start)
				text := "\n" + indent + "case " + b.Name + " {\n" + enumArms(et, indent+strings.Repeat(" ", format.IndentWidth)) + indent + "}"
				out = append(out, offer{title: "Pattern match on '" + b.Name + "'", edited: r.replace(end, end, text)})
			}
		}
	}
	call := r.innermost(func(n ast.Node) bool {
		return isCall(n) && !r.isPipeStage(n) && !isPipe(r.parent[n])
	})
	if call != nil {
		if et := enumOf(r.fa.ExprTypes[call]); et != nil && len(et.Variants) > 0 {
			start, end, ok := r.span(call)
			if ok {
				indent := r.lineIndentOf(start)
				text := "case " + r.content[start:end] + " {\n" + enumArms(et, indent+strings.Repeat(" ", format.IndentWidth)) + indent + "}"
				out = append(out, offer{title: "Pattern match on the result of " + calleeName(call.(*ast.Call)), edited: r.replace(start, end, text)})
			}
		}
	}
	return out
}

// bindingAtName is the `name = value` statement whose name holds the
// range's start, or nil.
func (r *refactorRequest) bindingAtName() *ast.Binding {
	for _, n := range r.all {
		b, ok := n.(*ast.Binding)
		if !ok || b.Value == nil || strings.HasPrefix(b.Name, "_") {
			continue
		}
		at := posToOffset(r.offs, b.Line, b.Col)
		if r.start >= at && r.end <= at+len(b.Name) {
			if stmt, _ := r.statementOf(b); stmt == b {
				return b
			}
		}
	}
	return nil
}

// calleeName is how a call's callee is written: `parse`, `User.load`.
func calleeName(c *ast.Call) string {
	switch f := c.Func.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.TypeIdent:
		return f.Name
	case *ast.FieldAccess:
		if o := calleeObject(f.Object); o != "" {
			return o + "." + f.Field.Name
		}
		return f.Field.Name
	}
	return "the call"
}

func calleeObject(n ast.Node) string {
	switch o := n.(type) {
	case *ast.Ident:
		return o.Name
	case *ast.TypeIdent:
		return o.Name
	}
	return ""
}

// enumOf is the enum type t is, or nil.
func enumOf(t analysis.Type) *analysis.EnumType {
	et, _ := analysis.ResolveTypeVar(t).(*analysis.EnumType)
	return et
}

// enumArms is a `case` arm for each variant of et, each arm's body the fill
// placeholder, in the fill fix's arm layout.
func enumArms(et *analysis.EnumType, indent string) string {
	var b strings.Builder
	for _, v := range et.Variants {
		pat, _ := variantPattern(v, 0, false)
		b.WriteString(indent + pat + " -> " + fillPlaceholder + "\n")
	}
	return b.String()
}
