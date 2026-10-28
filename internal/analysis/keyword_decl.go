package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// The keywords a declaration by the general rule (ast.KeywordDecl) may
// name. `message` expands into the function it declares and `noun` into
// the `once` it names (ExpandDeclarations). Any other keyword is an error.
const (
	keywordMessage = "message"
	keywordNoun    = "noun"
)

// isBuiltinKeyword reports whether kd is written with a keyword Nomi
// builds in, `message` or `noun`.
func isBuiltinKeyword(kd *ast.KeywordDecl) bool {
	return kd.Keyword == keywordMessage || kd.Keyword == keywordNoun
}

// unexpandedKeywordError is the error at a keyword declaration nothing
// expanded: one whose keyword Nomi does not know.
func (c *checker) unexpandedKeywordError(n *ast.KeywordDecl) TypeError {
	e := TypeError{Line: n.Line, Col: n.Col, EndLine: n.Line, EndCol: n.KeywordEndCol()}
	switch n.Keyword {
	case keywordMessage, keywordNoun:
		// LowerDerives expands every message and noun; one that reaches the
		// checker is a defect of the path that prepared its file.
		e.Message = fmt.Sprintf("this `%s` declaration was not expanded, a defect in Nomi", n.Keyword)
		return e
	}
	e.Message = fmt.Sprintf("unknown declaration keyword `%s`", n.Keyword)
	return e.WithHint("a declaration that starts with a keyword and a name is written with a keyword Nomi knows, `message` or `noun`")
}
