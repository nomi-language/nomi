package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// The keywords a declaration by the general rule (ast.KeywordDecl) may
// name. `phrase` expands into the function it declares and `noun` into
// the `once` it names (ExpandDeclarations). Any other keyword is an error.
const (
	keywordPhrase = "phrase"
	keywordNoun   = "noun"
)

// isBuiltinKeyword reports whether kd is written with a keyword Nomi
// builds in, `phrase` or `noun`.
func isBuiltinKeyword(kd *ast.KeywordDecl) bool {
	return kd.Keyword == keywordPhrase || kd.Keyword == keywordNoun
}

// unexpandedKeywordError is the error at a keyword declaration nothing
// expanded: one whose keyword Nomi does not know.
func (c *checker) unexpandedKeywordError(n *ast.KeywordDecl) TypeError {
	e := TypeError{Line: n.Line, Col: n.Col, EndLine: n.Line, EndCol: n.KeywordEndCol()}
	switch n.Keyword {
	case keywordPhrase, keywordNoun:
		// LowerDerives expands every phrase and noun; one that reaches the
		// checker is a defect of the path that prepared its file.
		e.Message = fmt.Sprintf("this `%s` declaration was not expanded, a defect in Nomi", n.Keyword)
		return e
	}
	e.Message = fmt.Sprintf("unknown declaration keyword `%s`", n.Keyword)
	if n.Keyword == "message" {
		return e.WithHint("text a person reads is declared with `phrase`: `phrase " + n.Name + "(...)`")
	}
	return e.WithHint("a declaration that starts with a keyword and a name is written with a keyword Nomi knows: `phrase`, `noun` or `embed`")
}
