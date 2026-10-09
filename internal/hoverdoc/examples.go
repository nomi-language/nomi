package hoverdoc

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	nomiformat "github.com/nomi-language/nomi/internal/format"
)

// maxHoverExamples is how many attached tests an editor hover shows before it
// says how many more the declaration has.
const maxHoverExamples = 5

// RenderForEditor is the hover an editor shows: RenderWithAnalysis's
// signature and doc, then the declaration's `//!` attached tests as an
// Examples section. The stdlib reference renders those tests itself, as
// runnable editors, so it calls RenderWithAnalysis (through Render) and not
// this.
func RenderForEditor(sym *analysis.Symbol, fa *analysis.FileAnalysis) string {
	content := RenderWithAnalysis(sym, fa)
	if content == "" {
		return ""
	}
	if examples := Examples(sym); examples != "" {
		content += "\n\n" + examples
	}
	return content
}

// Examples renders the attached tests of the declaration sym names, or "" when
// it names none or the declaration has none. Each attached test (a run of
// `//!` lines, ended by a `//` line, a blank line or the declaration) is one
// example and one code block, written as the formatter renders it, which is
// also how the stdlib reference shows it. At most maxHoverExamples are shown.
func Examples(sym *analysis.Symbol) string {
	node := exampleDecl(sym)
	if node == nil {
		return ""
	}
	var bodies []string
	for _, test := range ast.AttachedTestsOf(node) {
		if body := nomiformat.RenderAttachedTestBody(test); body != "" {
			bodies = append(bodies, body)
		}
	}
	if len(bodies) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("**Examples**")
	shown := bodies
	if len(shown) > maxHoverExamples {
		shown = shown[:maxHoverExamples]
	}
	for _, body := range shown {
		b.WriteString("\n\n```nomi\n")
		b.WriteString(body)
		b.WriteString("\n```")
	}
	if more := len(bodies) - len(shown); more > 0 {
		noun := "examples"
		if more == 1 {
			noun = "example"
		}
		fmt.Fprintf(&b, "\n\n*%d more %s at the declaration*", more, noun)
	}
	return b.String()
}

// exampleDecl is the declaration whose doc RenderWithAnalysis shows for sym,
// when that declaration can carry attached tests: the impl an
// interface-qualified call dispatches to, or the function, type, interface or
// `once` the symbol resolves to. A parameter, binding, field, enum variant,
// interface method or synthetic keyword marker has none of its own; neither
// does `self`, whose hover is its receiver type's.
func exampleDecl(sym *analysis.Symbol) ast.Node {
	if sym == nil {
		return nil
	}
	if sym.DispatchImpl != nil {
		return sym.DispatchImpl
	}
	if sym.Name == "self" {
		if _, ok := sym.Node.(*ast.ImplBlock); ok {
			return nil
		}
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	switch sym.Kind {
	case analysis.SymbolFunction, analysis.SymbolStruct, analysis.SymbolEnum,
		analysis.SymbolType, analysis.SymbolTypeAlias, analysis.SymbolInterface,
		analysis.SymbolOnce:
	default:
		return nil
	}
	switch sym.Node.(type) {
	case *ast.ImplBlock, *ast.ImplConformance:
		return nil
	}
	return sym.Node
}
