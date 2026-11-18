package analysis

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
)

// DollarInterpolationCode tags the warning that a string writes `$name`
// where a value named `name` is in scope (DollarWarnings).
const DollarInterpolationCode = "dollar-interpolation"

// noteDollarRefs records, for the string literal lit, each `$name` among
// refs, the ones the lexer recorded in its text (ast.DollarRef), whose name
// is a value in scope: a parameter, a local or pattern binding, or a `once`
// (DollarRefs). A function, a type or a module of that name is no value an
// interpolation would render, so it is not recorded. Each value named is
// also kept in dollarReads, for the help on a binding nothing else reads.
func (fb *FileBodies) noteDollarRefs(lit ast.Node, scope *Scope, refs []ast.DollarRef) {
	for _, ref := range refs {
		sym := scope.Lookup(ref.Name)
		if sym == nil || (sym.Kind != SymbolParam && sym.Kind != SymbolBinding && sym.Kind != SymbolOnce) {
			continue
		}
		if fb.DollarRefs == nil {
			fb.DollarRefs = map[ast.Node][]ast.DollarRef{}
			fb.dollarReads = map[*Symbol]bool{}
		}
		fb.DollarRefs[lit] = append(fb.DollarRefs[lit], ref)
		fb.dollarReads[sym] = true
	}
}

// DollarWarnings are the warnings of the file whose analysis is fa, from
// nodes: one for each quoted or triple-quoted string literal that writes
// `$name` where a value named `name` is in scope (FileAnalysis.DollarRefs).
// Nomi interpolates with `${name}` (spec §3 *String Literals*), so the
// literal holds the text `$name`, which its writer, coming from a language
// that interpolates with `$`, likely did not mean. The program is legal and
// runs, so this is a warning and never an error. Each spans the first such
// `$name` of its literal, says what a one-line literal reads as with every
// one rewritten, and carries the Fix that rewrites them.
//
// The lexer decides what a `$name` is as it reads the literal
// (token.DollarRef): none follows a `$` or a `{`, or a backslash in a
// triple-quoted string, and `\u{24}name` writes the same text with no `$`
// in the source, so it is how a literal keeps the text without the
// warning. A raw or typed literal has none, and a std file gets no warning.
func DollarWarnings(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	if fa == nil || len(fa.DollarRefs) == 0 {
		return nil
	}
	if _, isStd := stdlibModuleForPath(fa.FilePath); isStd {
		return nil
	}
	var out []TypeError
	for _, node := range nodes {
		ast.Inspect(node, func(n ast.Node) bool {
			refs := fa.DollarRefs[n]
			if len(refs) == 0 {
				return true
			}
			var source string
			var col int
			switch lit := n.(type) {
			case *ast.StringLit:
				source, col = lit.Source, lit.Col
			case *ast.StringInterp:
				source, col = lit.Source, lit.Col
			}
			var edits []parser.Edit
			var names []string
			for _, r := range refs {
				to := "${" + r.Name + "}"
				edits = append(edits, parser.Edit{
					Start: parser.Pos{Line: r.Line, Col: r.Col},
					End:   parser.Pos{Line: r.Line, Col: r.Col + 1 + len(r.Name)},
					Text:  to,
				})
				if w := "`" + to + "`"; !slices.Contains(names, w) {
					names = append(names, w)
				}
			}
			help := "to interpolate, write " + strings.Join(names, " and ")
			if source != "" && !strings.Contains(source, "`") {
				// A one-line literal as the lexer read it, with each
				// `$name` in braces, from the last so the columns before
				// it stay where they are.
				for i := len(refs) - 1; i >= 0; i-- {
					at := refs[i].Col - col
					source = source[:at] + "${" + refs[i].Name + "}" + source[at+1+len(refs[i].Name):]
				}
				help = "to interpolate, write `" + source + "`"
			}
			title := "Write " + names[0]
			if len(names) > 1 {
				title = "Write `${...}`"
			}
			first := refs[0]
			e := TypeError{
				Line: first.Line, Col: first.Col, EndLine: first.Line, EndCol: first.Col + 1 + len(first.Name),
				Code:    DollarInterpolationCode,
				Message: fmt.Sprintf("`$%s` is text here, not the value of `%s`: Nomi interpolates with `${...}`, not `$`", first.Name, first.Name),
				Fix:     &parser.Fix{Title: title, Edits: edits},
			}
			out = append(out, e.WithHint(help))
			return true
		})
	}
	return out
}
