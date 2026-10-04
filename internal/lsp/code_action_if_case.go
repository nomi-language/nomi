package lsp

import (
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"
)

// The if/case toggles rewrite the `if` chain or `case` whose header holds
// the range: from its keyword (an `else if` rung's from the `}` before its
// `else`) to the `{` that opens its body.
//
//   - "Convert to case": `if a { x } else if b { y } else { z }` becomes
//     `case { a -> x  b -> y  _ -> z }`, and `if Pat = v { a } else { b }`
//     becomes `case v { Pat -> a  _ -> b }`.
//   - "Convert to case on 'n'": a chain whose every condition is
//     `n == literal` over one plain name or field read of type Int, String
//     or Codepoint becomes `case n { literal -> ...  _ -> ... }`.
//   - "Convert to if/else": the reverse of each, for a subject-less `case`
//     ending in `_`, a two-arm `case v { Pat -> a  _ -> b }`, and a `case`
//     over a plain name or field read whose arms are literals and a last
//     `_`. A `_` arm whose body is an `if` continues the chain as
//     `else if`.
//
// An `if` without `else` converts only when its type is Unit, its missing
// branch written `_ -> Unit`, and a `_ -> Unit` arm converts back to no
// `else`. A branch block holding one expression (or `return`, `break`,
// `continue`) with no comments becomes a bare arm body; any other block
// stays a block arm. Arms with a `when` guard are not converted.
//
// Conditions are evaluated in the same order in both forms, and a subject
// once: a literal case's subject is read once per rung of the chain, so it
// must be a name or field read. Comments are kept where the formatter keeps
// them; a conversion that would drop one is not offered. The rewrite is
// parsed back and must read as the node it rendered, so a condition that
// would change meaning in its new position (a struct literal in an `if`
// condition) refuses the conversion.

func (r *refactorRequest) ifCaseConversions() []offer {
	target := r.ifCaseAtHeader()
	var out []offer
	add := func(title string, old, built ast.Node) {
		if built == nil {
			return
		}
		if o, ok := r.ifCaseOffer(title, old, built); ok {
			out = append(out, o)
		}
	}
	switch n := target.(type) {
	case *ast.If:
		top := n
		for p, ok := r.parent[top].(*ast.If); ok && p.Else == top; p, ok = r.parent[p].(*ast.If) {
			top = p
		}
		if top.CondPattern != nil {
			add("Convert to case", top, r.patternIfToCase(top))
			return out
		}
		add("Convert to case", top, r.ifChainToCase(top))
		if subject, built := r.ifChainToLiteralCase(top); built != nil {
			add("Convert to case on '"+subject+"'", top, built)
		}
	case *ast.Case:
		add("Convert to if/else", n, r.caseToIf(n))
	}
	return out
}

// ifCaseAtHeader is the innermost `if` or `case` whose header holds the
// range, or nil.
func (r *refactorRequest) ifCaseAtHeader() ast.Node {
	var found ast.Node
	for _, n := range r.all {
		start, end := -1, -1
		switch v := n.(type) {
		case *ast.If:
			if v.Then == nil || v.Cond == nil {
				continue
			}
			start = posToOffset(r.offs, v.Line, v.Col)
			if p, ok := r.parent[v].(*ast.If); ok && p.Else == v && p.Then != nil && p.Then.EndLine > 0 {
				start = posToOffset(r.offs, p.Then.EndLine, p.Then.EndCol) + 1
			}
			end = posToOffset(r.offs, v.Then.Line, v.Then.Col) + 1
		case *ast.Case:
			if r.isPipeStage(v) {
				continue
			}
			start = posToOffset(r.offs, v.Line, v.Col)
			end = r.caseBodyOpen(v)
		default:
			continue
		}
		if start >= 0 && end > start && start <= r.start && r.end <= end {
			found = n // preorder: a later match is nested in an earlier one
		}
	}
	return found
}

// caseBodyOpen is the offset just past the `{` that opens c's arms, or -1.
func (r *refactorRequest) caseBodyOpen(c *ast.Case) int {
	from := posToOffset(r.offs, c.Line, c.Col) + len("case")
	if c.Value != nil {
		_, e, ok := r.span(c.Value)
		if !ok {
			return -1
		}
		from = e
	}
	for k := r.tokenAt(from); k < len(r.tokens); k++ {
		switch r.tokens[k].Type {
		case token.NEWLINE:
			continue
		case token.LBRACE:
			return r.offOf(r.tokens[k]) + 1
		}
		return -1
	}
	return -1
}

// ifCaseOffer renders built in old's place, and offers it when every
// comment of old's text survives and the edited document parses back to
// the node built renders.
func (r *refactorRequest) ifCaseOffer(title string, old, built ast.Node) (offer, bool) {
	start, end, ok := r.span(old)
	if !ok {
		return offer{}, false
	}
	text := render(built, r.lineIndentOf(start))
	if !sameComments(r.content[start:end], text) {
		return offer{}, false
	}
	edited := r.replace(start, end, text)
	if !reparsesAs(edited, lineIndexOf(r.offs, start)+1, start-r.offs[lineIndexOf(r.offs, start)]+1, built) {
		return offer{}, false
	}
	return offer{title: title, edited: edited}, true
}

// sameComments reports whether a and b hold the same comments.
func sameComments(a, b string) bool {
	ca, cb := commentTexts(a), commentTexts(b)
	if len(ca) != len(cb) {
		return false
	}
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}

func commentTexts(src string) []string {
	var out []string
	for _, t := range lexer.Lex(src) {
		if t.Type == token.COMMENT || t.Type == token.DOC_COMMENT {
			out = append(out, t.Lexeme)
		}
	}
	sort.Strings(out)
	return out
}

// reparsesAs reports whether edited parses, and the `if` or `case` at
// (line, col) renders as built does.
func reparsesAs(edited string, line, col int, built ast.Node) bool {
	nodes, err := parser.Parse(lexer.Lex(edited))
	if err != nil {
		return false
	}
	var found ast.Node
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		if found != nil {
			return
		}
		switch v := n.(type) {
		case *ast.If:
			if v.Line == line && v.Col == col {
				found = n
				return
			}
		case *ast.Case:
			if v.Line == line && v.Col == col {
				found = n
				return
			}
		}
		for _, c := range nodeChildren(n) {
			visit(c)
		}
	}
	for _, n := range nodes {
		visit(n)
	}
	return found != nil && format.RenderNode(found) == format.RenderNode(built)
}

// ifChainToCase is the subject-less case for an if chain of conditions.
func (r *refactorRequest) ifChainToCase(top *ast.If) ast.Node {
	var arms []ast.CaseBranch
	cur := top
	for {
		if cur.Cond == nil || cur.CondPattern != nil || cur.Then == nil {
			return nil
		}
		arms = append(arms, ast.CaseBranch{Pattern: cur.Cond, Body: armBody(cur.Then), Line: cur.Line, Col: cur.Col})
		next, ok := cur.Else.(*ast.If)
		if !ok {
			break
		}
		cur = next
	}
	last, ok := r.lastArm(top, cur)
	if !ok {
		return nil
	}
	return &ast.Case{Branches: append(arms, last), Line: top.Line, Col: top.Col}
}

// lastArm is the `_` arm for the chain's final rung: its `else` block, or
// `_ -> Unit` when it has none and the chain's type is Unit.
func (r *refactorRequest) lastArm(top, last *ast.If) (ast.CaseBranch, bool) {
	wild := &ast.WildcardPattern{Line: last.Line, Col: last.Col}
	switch e := last.Else.(type) {
	case *ast.Block:
		return ast.CaseBranch{Pattern: wild, Body: armBody(e), Line: e.Line, Col: e.Col}, true
	case nil:
		if !r.isUnitTyped(top) {
			return ast.CaseBranch{}, false
		}
		return ast.CaseBranch{Pattern: wild, Body: &ast.TypeIdent{Name: "Unit", Line: last.Line, Col: last.Col}, Line: last.Line, Col: last.Col}, true
	}
	return ast.CaseBranch{}, false
}

func (r *refactorRequest) isUnitTyped(n ast.Node) bool {
	t, ok := r.fa.ExprTypes[n]
	return ok && analysis.ResolveTypeVar(t) == analysis.TypeUnit
}

// ifChainToLiteralCase is `case n { lit -> ... }` for a chain whose every
// condition is `n == lit` over the same name or field read, and how n is
// written.
func (r *refactorRequest) ifChainToLiteralCase(top *ast.If) (string, ast.Node) {
	var subject ast.Node
	subjectText := ""
	seen := map[string]bool{}
	var arms []ast.CaseBranch
	cur := top
	for {
		if cur.Cond == nil || cur.CondPattern != nil || cur.Then == nil {
			return "", nil
		}
		b, ok := cur.Cond.(*ast.Binary)
		if !ok || b.Op != "==" || !plainRead(b.Left) || !r.literalSubjectType(b.Left) {
			return "", nil
		}
		text := format.RenderNode(b.Left)
		if subject == nil {
			subject, subjectText = b.Left, text
		} else if text != subjectText {
			return "", nil
		}
		pat := literalPattern(b.Right)
		if pat == nil {
			return "", nil
		}
		key := format.RenderNode(pat)
		if seen[key] {
			return "", nil
		}
		seen[key] = true
		arms = append(arms, ast.CaseBranch{Pattern: pat, Body: armBody(cur.Then), Line: cur.Line, Col: cur.Col})
		next, ok := cur.Else.(*ast.If)
		if !ok {
			break
		}
		cur = next
	}
	last, ok := r.lastArm(top, cur)
	if !ok {
		return "", nil
	}
	return subjectText, &ast.Case{Value: subject, Branches: append(arms, last), Line: top.Line, Col: top.Col}
}

// plainRead reports whether reading n twice does what reading it once
// does: a name, or a field read of one.
func plainRead(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.Ident:
		return true
	case *ast.FieldAccess:
		if _, ok := v.Object.(*ast.TypeIdent); ok {
			return true
		}
		return plainRead(v.Object)
	}
	return false
}

// literalSubjectType reports whether n's type is one whose literal pattern
// matches exactly the values `==` the literal: Int, String or Codepoint.
func (r *refactorRequest) literalSubjectType(n ast.Node) bool {
	t, ok := r.fa.ExprTypes[n]
	if !ok {
		return false
	}
	switch analysis.ResolveTypeVar(t) {
	case analysis.TypeInt, analysis.TypeString, analysis.TypeCodepoint:
		return true
	}
	return false
}

// literalPattern is the case pattern for the literal expression n, or nil.
func literalPattern(n ast.Node) ast.Node {
	switch v := n.(type) {
	case *ast.IntLit, *ast.StringLit, *ast.CodepointLit:
		return n
	case *ast.Unary:
		if lit, ok := v.Right.(*ast.IntLit); ok && v.Op == "-" && lit.Lexeme != "" {
			return &ast.IntLit{Value: -lit.Value, Lexeme: "-" + lit.Lexeme, Line: v.Line, Col: v.Col}
		}
	}
	return nil
}

// patternIfToCase is `case v { Pat -> a  _ -> b }` for `if Pat = v`.
func (r *refactorRequest) patternIfToCase(top *ast.If) ast.Node {
	if top.Then == nil || top.Cond == nil {
		return nil
	}
	first := ast.CaseBranch{Pattern: top.CondPattern, Body: armBody(top.Then), Line: top.Line, Col: top.Col}
	var last ast.CaseBranch
	if next, ok := top.Else.(*ast.If); ok {
		last = ast.CaseBranch{Pattern: &ast.WildcardPattern{Line: next.Line, Col: next.Col}, Body: next, Line: next.Line, Col: next.Col}
	} else {
		var ok bool
		if last, ok = r.lastArm(top, top); !ok {
			return nil
		}
	}
	return &ast.Case{Value: top.Cond, Branches: []ast.CaseBranch{first, last}, Line: top.Line, Col: top.Col}
}

// armBody is a branch block as an arm body: its one expression, `return`,
// `break` or `continue` when it holds nothing else and no comments, or the
// block.
func armBody(b *ast.Block) ast.Node {
	if len(b.Stmts) != 1 || hasComments(b) {
		return b
	}
	s := b.Stmts[0]
	if h, ok := s.(ast.HasTrivia); ok && hasComments(h) {
		return b
	}
	switch v := s.(type) {
	case *ast.ExprStmt:
		if _, isBlock := v.Expr.(*ast.Block); isBlock {
			return b
		}
		if h, ok := v.Expr.(ast.HasTrivia); ok && hasComments(h) {
			return b
		}
		return v.Expr
	case *ast.Return, *ast.Break, *ast.Continue:
		return s
	}
	return b
}

// caseToIf is the if chain for c, or nil.
func (r *refactorRequest) caseToIf(c *ast.Case) ast.Node {
	n := len(c.Branches)
	if n < 2 {
		return nil
	}
	for i := range c.Branches {
		if c.Branches[i].Guard != nil || c.Branches[i].Body == nil {
			return nil
		}
	}
	last := &c.Branches[n-1]
	if _, ok := last.Pattern.(*ast.WildcardPattern); !ok {
		return nil
	}
	rest := r.elseOf(c, last)
	switch {
	case c.Value == nil:
		for i := n - 2; i >= 0; i-- {
			b := &c.Branches[i]
			if _, wild := b.Pattern.(*ast.WildcardPattern); wild {
				return nil
			}
			rest = &ast.If{Cond: b.Pattern, Then: branchBlock(b), Else: rest, Line: b.Line, Col: b.Col}
		}
		return withPos(rest, c)
	case r.literalArms(c):
		for i := n - 2; i >= 0; i-- {
			b := &c.Branches[i]
			cond := &ast.Binary{Left: c.Value, Op: "==", Right: b.Pattern, Line: b.Line, Col: b.Col}
			rest = &ast.If{Cond: cond, Then: branchBlock(b), Else: rest, Line: b.Line, Col: b.Col}
		}
		return withPos(rest, c)
	case n == 2 && refutableHead(c.Branches[0].Pattern):
		b := &c.Branches[0]
		return &ast.If{CondPattern: b.Pattern, Cond: c.Value, Then: branchBlock(b), Else: rest, Line: c.Line, Col: c.Col}
	}
	return nil
}

func withPos(n ast.Node, c *ast.Case) ast.Node {
	if i, ok := n.(*ast.If); ok {
		i.Line, i.Col = c.Line, c.Col
	}
	return n
}

// literalArms reports whether c is over a name or field read of type Int,
// String or Codepoint, and every arm but the last `_` is a literal.
func (r *refactorRequest) literalArms(c *ast.Case) bool {
	if !plainRead(c.Value) || !r.literalSubjectType(c.Value) {
		return false
	}
	for _, b := range c.Branches[:len(c.Branches)-1] {
		switch b.Pattern.(type) {
		case *ast.IntLit, *ast.StringLit, *ast.CodepointLit:
		default:
			return false
		}
	}
	return true
}

// refutableHead reports whether p is a pattern an `if Pat = v` reads as a
// test: not a name, `_` or a literal (a literal case converts to `==`).
func refutableHead(p ast.Node) bool {
	switch p.(type) {
	case *ast.IdentPattern, *ast.WildcardPattern, *ast.IntLit, *ast.FloatLit, *ast.DecimalLit,
		*ast.StringLit, *ast.CodepointLit, nil:
		return false
	}
	return true
}

// elseOf is the `else` for the last `_` arm: none for `_ -> Unit` in a Unit
// case, the arm's `if` as an `else if`, or a block.
func (r *refactorRequest) elseOf(c *ast.Case, b *ast.CaseBranch) ast.Node {
	trivia := hasComments(b)
	if !trivia && isUnitValue(b.Body) && r.isUnitTyped(c) {
		return nil
	}
	if i, ok := b.Body.(*ast.If); ok && !trivia && i.Cond != nil {
		return i
	}
	return branchBlock(b)
}

func isUnitValue(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.TypeIdent:
		return v.Name == "Unit"
	case *ast.Ident:
		return v.Name == "Unit"
	}
	return false
}

// branchBlock is an arm's body as an `if` branch block, the arm's comments
// on its statement.
func branchBlock(b *ast.CaseBranch) *ast.Block {
	if blk, ok := b.Body.(*ast.Block); ok {
		return blk
	}
	line, _ := nodePos(b.Body)
	var stmt ast.Node
	switch v := b.Body.(type) {
	case *ast.Return, *ast.Break, *ast.Continue:
		if !hasComments(b) {
			stmt = v
		}
	}
	if stmt == nil {
		es := &ast.ExprStmt{Expr: b.Body, Line: line}
		es.Leading = commentsOnly(b.GetLeading())
		es.Trailing = commentsOnly(b.GetTrailing())
		stmt = es
	}
	return &ast.Block{Stmts: []ast.Node{stmt}, Line: line, EndLine: line}
}

// hasComments reports whether n carries a comment of its own.
func hasComments(n interface {
	GetLeading() []ast.Trivia
	GetTrailing() []ast.Trivia
}) bool {
	return len(commentsOnly(n.GetLeading())) > 0 || len(commentsOnly(n.GetTrailing())) > 0
}

func commentsOnly(ts []ast.Trivia) []ast.Trivia {
	var out []ast.Trivia
	for _, t := range ts {
		if t.Kind == ast.TriviaComment {
			out = append(out, t)
		}
	}
	return out
}
