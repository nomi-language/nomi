package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// RelatedInfo is a second location a diagnostic points at: the declaration
// a missing function is required by, the first definition of a redeclared
// name.
type RelatedInfo struct {
	// File is the path of the file the location is in, as the analysis
	// knows it (Symbol.SourceFile): an absolute path for a project file. ""
	// means the file the diagnostic itself is in.
	File string
	// Line and Col start the location; EndLine and EndCol are one past its
	// last byte, zero for a point. 1-based lines, byte columns.
	Line, Col       int
	EndLine, EndCol int
	// Message says what is at the location ("declared here").
	Message string
}

// HasEnd reports whether e records the end of its span.
func (e TypeError) HasEnd() bool { return e.EndLine > 0 }

// WithHint is e with hint added to its hints. An empty hint adds nothing.
func (e TypeError) WithHint(hint string) TypeError {
	if hint != "" {
		e.Hints = append(append([]string(nil), e.Hints...), hint)
	}
	return e
}

// WithRelated is e with r added to its related locations.
func (e TypeError) WithRelated(r RelatedInfo) TypeError {
	e.Related = append(append([]RelatedInfo(nil), e.Related...), r)
	return e
}

// Spanning is e placed on n's extent: it starts where n's first token
// starts and ends after its last. A node the parser recorded no extent for
// leaves e where it is.
func (e TypeError) Spanning(n ast.Node) TypeError {
	if sp, ok := spanOf(n); ok {
		e.Line, e.Col, e.EndLine, e.EndCol = sp.StartLine, sp.StartCol, sp.EndLine, sp.EndCol
	}
	return e
}

// errAt is the error msg over n's extent, or at n's position when the
// parser recorded no extent for it (a node a lowering synthesized).
func errAt(n ast.Node, msg string) TypeError {
	e := TypeError{Message: msg}
	if sp, ok := spanOf(n); ok {
		e.Line, e.Col, e.EndLine, e.EndCol = sp.StartLine, sp.StartCol, sp.EndLine, sp.EndCol
		return e
	}
	e.Line, e.Col = nodeLineCol(n)
	if e.Line == 0 && n != nil {
		e.Line = n.LineNum()
	}
	return e
}

// relatedAt is a related location over n's extent in the diagnostic's own
// file.
func relatedAt(n ast.Node, msg string) (RelatedInfo, bool) {
	sp, ok := spanOf(n)
	if !ok {
		return RelatedInfo{}, false
	}
	return RelatedInfo{Line: sp.StartLine, Col: sp.StartCol, EndLine: sp.EndLine, EndCol: sp.EndCol, Message: msg}, true
}

// nameRelated is a related location over a declaration's name: the name
// starts at pos, and file is "" for the diagnostic's own file.
func nameRelated(file string, pos Pos, name, msg string) RelatedInfo {
	return RelatedInfo{File: file, Line: pos.Line, Col: pos.Col, EndLine: pos.Line, EndCol: pos.Col + len(name), Message: msg}
}

// spanOf is n's source extent: the span the parser recorded, or for an
// identifier built as part of another node (a field name), its name.
func spanOf(n ast.Node) (ast.Span, bool) {
	if n == nil {
		return ast.Span{}, false
	}
	if hs, ok := n.(ast.HasSpan); ok {
		if sp := hs.GetSpan(); !sp.IsZero() {
			return sp, true
		}
	}
	switch v := n.(type) {
	case *ast.Ident:
		if v != nil && v.Line > 0 && v.Col > 0 {
			return ast.Span{StartLine: v.Line, StartCol: v.Col, EndLine: v.Line, EndCol: v.Col + len(v.Name)}, true
		}
	case *ast.TypeIdent:
		if v != nil && v.Line > 0 && v.Col > 0 {
			return ast.Span{StartLine: v.Line, StartCol: v.Col, EndLine: v.Line, EndCol: v.Col + len(v.Name)}, true
		}
	}
	return ast.Span{}, false
}

// CompleteSpans gives every error in errs that names only a point the
// extent of the token that starts there, read from src, the file the errors
// are in. Most diagnostics are reported at a name or a keyword, which is the
// token. An error whose point starts no token keeps its point.
func CompleteSpans(src string, errs []TypeError) []TypeError {
	return CompleteSpansFrom(func() []token.Token { return lexer.Lex(src) }, errs)
}

// CompleteSpansFrom is CompleteSpans reading the file's tokens from tokens,
// which it calls only when some error names only a point: a caller that
// already holds the text's tokens passes them instead of lexing it again.
func CompleteSpansFrom(tokens func() []token.Token, errs []TypeError) []TypeError {
	need := false
	for _, e := range errs {
		if !e.HasEnd() && e.Line > 0 && e.Col > 0 {
			need = true
			break
		}
	}
	if !need {
		return errs
	}
	type pos struct{ line, col int }
	ends := map[pos]pos{}
	for _, t := range tokens() {
		switch t.Type {
		case token.NEWLINE, token.EOF, token.BLANK_LINE, token.COMMENT, token.DOC_COMMENT:
			continue
		}
		if t.Lexeme == "" {
			continue
		}
		line, col := tokenEnd(t)
		ends[pos{t.Line, t.Col}] = pos{line, col}
	}
	out := make([]TypeError, len(errs))
	for i, e := range errs {
		if !e.HasEnd() && e.Line > 0 && e.Col > 0 {
			if end, ok := ends[pos{e.Line, e.Col}]; ok {
				e.EndLine, e.EndCol = end.line, end.col
			}
		}
		out[i] = e
	}
	return out
}

// symbolRelated is a related location over the name sym declares, with the
// file it is in. ok is false when the analysis does not know that file (a
// standard library declaration carries no path).
func (c *checker) symbolRelated(sym *Symbol, msg string) (RelatedInfo, bool) {
	if sym == nil {
		return RelatedInfo{}, false
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym.Pos.Line <= 0 || sym.Pos.Col <= 0 || IsSynthesizedLine(sym.Pos.Line) {
		return RelatedInfo{}, false
	}
	switch {
	case c.fa != nil && sym.SourceFile != "" && sym.SourceFile != c.fa.FilePath:
		return nameRelated(sym.SourceFile, sym.Pos, sym.Name, msg), true
	case c.fa != nil && c.declaredHere(sym):
		return nameRelated("", sym.Pos, sym.Name, msg), true
	}
	return RelatedInfo{}, false
}

// declaredHere reports whether sym, or the interface or type that owns it as
// a member, is declared in the file being checked.
func (c *checker) declaredHere(sym *Symbol) bool {
	if c.fa.Definitions[sym.Pos] == sym {
		return true
	}
	for _, top := range c.fa.ModuleScope.Symbols {
		if top.SourceFile == "" && c.fa.Definitions[top.Pos] == top {
			if m, ok := top.Members[sym.Name]; ok && m == sym {
				return true
			}
		}
	}
	return false
}

// implInterfaceSymbol is the declaration of the interface impl block n
// names, or nil.
func (c *checker) implInterfaceSymbol(n *ast.ImplBlock) *Symbol {
	if n == nil || n.Interface == nil || c.fa == nil || c.fa.ModuleScope == nil {
		return nil
	}
	sym := c.fa.ModuleScope.Lookup(TypeExprBaseName(n.Interface))
	if sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil || sym.Kind != SymbolInterface {
		return nil
	}
	return sym
}

// interfaceFunctionRelated points at the declaration of the interface
// function name in the interface impl block n names, or at the interface
// when the function has no symbol of its own.
func (c *checker) interfaceFunctionRelated(n *ast.ImplBlock, name, msg string) (RelatedInfo, bool) {
	iface := c.implInterfaceSymbol(n)
	if iface == nil {
		return RelatedInfo{}, false
	}
	if m := iface.Members[name]; m != nil {
		if r, ok := c.symbolRelated(m, msg); ok {
			return r, true
		}
	}
	return c.symbolRelated(iface, "interface '"+iface.Name+"' is declared here")
}

// implHeaderError is msg over impl block n's header, `impl Iface for Type`.
func implHeaderError(n *ast.ImplBlock, msg string) TypeError {
	e := TypeError{Line: n.Line, Col: n.Col, Message: msg}
	if sp, ok := spanOf(n.Receiver); ok && sp.EndLine == n.Line && n.Col > 0 {
		e.EndLine, e.EndCol = sp.EndLine, sp.EndCol
	}
	return e
}

// tailExpr is the expression a block's value comes from: its last
// statement, unwrapped from its expression statement. nil for an empty
// block.
func tailExpr(b *ast.Block) ast.Node {
	if b == nil || len(b.Stmts) == 0 {
		return nil
	}
	last := b.Stmts[len(b.Stmts)-1]
	if es, ok := last.(*ast.ExprStmt); ok {
		return es.Expr
	}
	return last
}

// valueExpr is the expression n's value comes from: a block's tail
// expression, or n itself.
func valueExpr(n ast.Node) ast.Node {
	if b, ok := n.(*ast.Block); ok && b != nil {
		if t := tailExpr(b); t != nil {
			return t
		}
	}
	return n
}

// returnMismatchError is msg about fn's body, whose value is not fn's
// declared return type. It spans the body's tail expression when that fits
// on one line, and fn's name otherwise; the related location is the
// declared return type.
func returnMismatchError(fn *ast.FuncDef, msg string) TypeError {
	e := TypeError{Line: fn.Line, Col: fn.Col, Message: msg}
	if fn.Line > 0 && fn.Col > 0 {
		e.EndLine, e.EndCol = fn.Line, fn.Col+len(fn.Name)
	}
	if t := tailExpr(fn.Body); t != nil {
		if sp, ok := spanOf(t); ok && sp.StartLine == sp.EndLine {
			e = e.Spanning(t)
		}
	}
	if fn.ReturnTypeExpr != nil {
		if r, ok := relatedAt(fn.ReturnTypeExpr, "the return type is declared here"); ok {
			e = e.WithRelated(r)
		}
	}
	return e
}

// tokenEnd is one past a token's last byte.
func tokenEnd(t token.Token) (line, col int) {
	if t.EndCol > 0 {
		if t.EndLine > 0 {
			return t.EndLine, t.EndCol
		}
		return t.Line, t.EndCol
	}
	i := strings.LastIndexByte(t.Lexeme, '\n')
	if i < 0 {
		return t.Line, t.Col + len(t.Lexeme)
	}
	return t.Line + strings.Count(t.Lexeme, "\n"), len(t.Lexeme) - i
}
