package lsp

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// "Extract variable" binds an expression to a new name on the line before
// the statement that holds it and puts the name where the expression was.
// The expression is the selection, when one is made and it is exactly one
// expression's text, or else the innermost call under the cursor. The name
// comes from the expression: a call's function (`count(xs)` -> `count`,
// `Iter.to_list(...)` -> `list`), a field (`user.name` -> `name`), else
// `value`; a number follows it when the name is bound or read anywhere it
// could be confused (`count2`).
//
// "Inline variable" replaces the one read of a binding with the bound
// expression and deletes the binding.
//
// Both move an expression across the statement it sits in, so both refuse
// when that could change what runs or when:
//
//   - the path between the expression and its statement goes through a
//     lambda body, a case arm, an if branch, the right side of `and` or
//     `or`, or a pattern: those run the expression conditionally, later, or
//     more than once;
//   - the expression may have an effect (it calls a function, or holds
//     `try`, `dbg`, `assert`, `return`, `break` or `continue`) and the
//     statement runs a call or one of those before it;
//   - the expression takes its type from where it stands (`.Variant`,
//     `None`-like dot forms, an empty collection, an anonymous struct or a
//     lambda), which a binding without an annotation would lose;
//   - for inline, a name the expression reads means something else at the
//     read, the binding is annotated, or the read is a punned field label;
//     an expression that may have an effect inlines only into the statement
//     right after its binding.
//
// An arithmetic trap is not counted as an effect: `a / b` moved before an
// earlier call traps before that call's output rather than after it.

// extractVariable offers "Extract variable" for the range.
func (r *refactorRequest) extractVariable() (string, string, bool) {
	n := r.extractTarget()
	if n == nil {
		return "", "", false
	}
	if _, ok := r.parent[n].(*ast.Binding); ok {
		return "", "", false
	}
	stmt, block := r.statementOf(n)
	if stmt == nil || stmt == n || !r.canHoist(n, stmt, block, mayEffect(n, r.kids)) {
		return "", "", false
	}
	if es, ok := stmt.(*ast.ExprStmt); ok && es.Expr == n && block.Stmts[len(block.Stmts)-1] != stmt {
		// A statement's whole value: the binding would hold a value
		// nothing reads but the line after it.
		return "", "", false
	}
	start, end, ok := r.span(n)
	if !ok {
		return "", "", false
	}
	stmtStart, _, ok := r.span(stmt)
	if !ok || strings.TrimSpace(r.content[r.lineStart(stmtStart):stmtStart]) != "" {
		return "", "", false
	}
	sl, sc := nodePos(stmt)
	name := r.freshName(deriveName(n), analysis.Pos{Line: sl, Col: sc})
	indent := r.lineIndentOf(stmtStart)
	edited := r.content[:stmtStart] + name + " = " + r.content[start:end] + "\n" + indent +
		r.content[stmtStart:start] + name + r.content[end:]
	return fmt.Sprintf("Extract variable '%s'", name), edited, true
}

// extractTarget is the expression whose text is exactly the selection, or,
// for a cursor, the innermost call under it that is not a pipe stage.
func (r *refactorRequest) extractTarget() ast.Node {
	if r.start == r.end {
		return r.innermost(func(n ast.Node) bool { return isCall(n) && !r.isPipeStage(n) })
	}
	text := r.content[r.start:r.end]
	start := r.start + len(text) - len(strings.TrimLeft(text, " \t\r\n"))
	end := r.end - (len(text) - len(strings.TrimRight(text, " \t\r\n")))
	for _, n := range r.all {
		if !isExpression(n) {
			continue
		}
		if s, e, ok := r.span(n); ok && s == start && e == end {
			return n
		}
	}
	return nil
}

// isExpression reports whether n is a value a binding can hold.
func isExpression(n ast.Node) bool {
	switch n.(type) {
	case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.CodepointLit,
		*ast.StringLit, *ast.StringInterp, *ast.TaggedString, *ast.ListLit, *ast.VectorLit,
		*ast.SetLit, *ast.MapLit, *ast.TupleLit, *ast.Unary, *ast.Binary, *ast.GroupedExpr,
		*ast.Call, *ast.FieldAccess, *ast.StructLit, *ast.RangeLit, *ast.If, *ast.Case, *ast.TryOp:
		return true
	}
	return false
}

// canHoist reports whether n can be evaluated just before stmt, which
// holds it in block, without changing what runs or when. effectful says
// whether the expression evaluated there may have an effect.
func (r *refactorRequest) canHoist(n, stmt ast.Node, block *ast.Block, effectful bool) bool {
	if block == nil || block.EndLine == 0 {
		// A lambda's one-expression body has no line to bind on.
		return false
	}
	if r.isPipeStage(n) || needsExpected(n) || isLambda(n) {
		return false
	}
	if _, ok := n.(*ast.Placeholder); ok {
		return false
	}
	ancestors := map[ast.Node]bool{}
	for child := n; child != stmt; child = r.parent[child] {
		p := r.parent[child]
		if p == nil || !hoistsThrough(p, child) {
			return false
		}
		ancestors[p] = true
	}
	if !effectful {
		return true
	}
	start, _, _ := r.span(n)
	inside := map[ast.Node]bool{}
	var mark func(m ast.Node)
	mark = func(m ast.Node) {
		inside[m] = true
		for _, c := range r.kids[m] {
			mark(c)
		}
	}
	mark(n)
	bad := false
	var walk func(m ast.Node)
	walk = func(m ast.Node) {
		if bad || inside[m] {
			return
		}
		if !ancestors[m] && isEffect(m) {
			if s, _, ok := r.span(m); !ok || s < start {
				bad = true
				return
			}
		}
		for _, c := range r.kids[m] {
			walk(c)
		}
	}
	walk(stmt)
	return !bad
}

// hoistsThrough reports whether parent evaluates child, unconditionally
// and once, as part of evaluating parent.
func hoistsThrough(parent, child ast.Node) bool {
	switch p := parent.(type) {
	case *ast.Call, *ast.NamedArg, *ast.GroupedExpr, *ast.Unary, *ast.FieldAccess,
		*ast.ListLit, *ast.VectorLit, *ast.SetLit, *ast.TupleLit, *ast.MapLit, *ast.StructLit,
		*ast.StringInterp, *ast.TaggedString, *ast.RangeLit, *ast.TryOp, *ast.Dbg, *ast.Return,
		*ast.ExprStmt, *ast.Binding, *ast.Assertion, *ast.TupleDestructure,
		*ast.StructDestructure, *ast.DistinctDestructure, *ast.With:
		return true
	case *ast.Binary:
		return !((p.Op == "and" || p.Op == "or") && p.Right == child)
	case *ast.Case:
		return p.Value == child
	case *ast.If:
		return p.Cond == child
	case *ast.PatternBinding:
		return p.Value == child
	case *ast.PatternDestructure:
		return p.Value == child
	}
	return false
}

func isLambda(n ast.Node) bool {
	_, ok := n.(*ast.Lambda)
	return ok
}

// isEffect reports whether evaluating n itself may act outside the value it
// computes: a call, or a keyword that prints, fails or leaves.
func isEffect(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.Call, *ast.Dbg, *ast.Assertion, *ast.Return, *ast.Break, *ast.Continue, *ast.ConcurrentBlock:
		return true
	case *ast.TryOp:
		return true
	case *ast.Binary:
		// A call stage runs when the pipe does.
		return v.Op == "|>"
	}
	return false
}

func mayEffect(n ast.Node, kids map[ast.Node][]ast.Node) bool {
	if isEffect(n) {
		return true
	}
	for _, c := range kids[n] {
		if mayEffect(c, kids) {
			return true
		}
	}
	return false
}

// deriveName is a binding name for the value of n.
func deriveName(n ast.Node) string {
	name := "value"
	switch v := n.(type) {
	case *ast.Call:
		name = functionName(v.Func)
	case *ast.FieldAccess:
		if v.Field != nil {
			name = v.Field.Name
		}
	case *ast.Binary:
		if v.Op == "|>" {
			if c, ok := v.Right.(*ast.Call); ok {
				name = functionName(c.Func)
			}
		}
	case *ast.TryOp:
		if v.Expr != nil {
			return deriveName(v.Expr)
		}
	case *ast.GroupedExpr:
		return deriveName(v.Expr)
	}
	name = strings.TrimRight(name, "?!")
	if rest, ok := strings.CutPrefix(name, "to_"); ok && rest != "" {
		name = rest
	}
	if !isPlainIdent(name) || strings.HasPrefix(name, "_") {
		return "value"
	}
	return name
}

func functionName(f ast.Node) string {
	switch v := f.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.FieldAccess:
		if v.Field != nil {
			return v.Field.Name
		}
	}
	return "value"
}

// freshName is base, or base with the first number that makes it a name
// nothing at pos can see and the declaration never spells.
func (r *refactorRequest) freshName(base string, pos analysis.Pos) string {
	spelled := map[string]bool{}
	for _, n := range r.all {
		switch v := n.(type) {
		case *ast.Ident:
			if !r.isFieldName(v) {
				spelled[v.Name] = true
			}
		case *ast.Binding:
			spelled[v.Name] = true
		}
	}
	scope := r.fa.ScopeAt(pos)
	taken := func(name string) bool {
		return spelled[name] || (scope != nil && scope.Lookup(name) != nil)
	}
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		if name := fmt.Sprintf("%s%d", base, i); !taken(name) {
			return name
		}
	}
}

// inlineVariable offers "Inline variable" on a binding's name or its read.
func (r *refactorRequest) inlineVariable() (string, string, bool) {
	b := r.bindingAtName()
	if b == nil {
		b = r.bindingReadAt()
	}
	if b == nil || b.TypeAnnotation != nil {
		return "", "", false
	}
	stmt, block := r.statementOf(b)
	if stmt != b || block == nil || needsExpected(b.Value) || isLambda(b.Value) {
		return "", "", false
	}
	def := r.fa.Definitions[analysis.Pos{Line: b.Line, Col: b.Col}]
	if def == nil {
		return "", "", false
	}
	id := symbolIdentity(def)
	var reads []analysis.Pos
	for pos, sym := range r.fa.References {
		if matchesIdentity(sym, id) {
			reads = append(reads, pos)
		}
	}
	if len(reads) != 1 {
		return "", "", false
	}
	if _, punned := r.fa.PunnedFieldLabels[reads[0]]; punned {
		return "", "", false
	}
	var use *ast.Ident
	for _, n := range r.all {
		if v, ok := n.(*ast.Ident); ok && v.Name == b.Name && v.Line == reads[0].Line && v.Col == reads[0].Col {
			use = v
		}
	}
	if use == nil || !r.sameMeaning(b.Value, reads[0]) {
		return "", "", false
	}
	if mayEffect(b.Value, r.kids) {
		useStmt, useBlock := r.statementOf(use)
		if useBlock != block || !r.nextStatement(block, b, useStmt) || !r.canHoist(use, useStmt, useBlock, true) {
			return "", "", false
		}
	}
	bStart, bEnd, ok := r.span(b)
	vStart, vEnd, ok2 := r.span(b.Value)
	if !ok || !ok2 {
		return "", "", false
	}
	// The binding goes with its whole lines.
	delStart := r.lineStart(bStart)
	if strings.TrimSpace(r.content[delStart:bStart]) != "" {
		return "", "", false
	}
	delEnd := strings.IndexByte(r.content[bEnd:], '\n')
	if delEnd < 0 || strings.TrimSpace(r.content[bEnd:bEnd+delEnd]) != "" {
		return "", "", false
	}
	delEnd += bEnd + 1
	useStart := posToOffset(r.offs, use.Line, use.Col)
	if useStart < delEnd {
		return "", "", false
	}
	text := r.content[vStart:vEnd]
	if r.needsParens(b.Value, use) {
		text = "(" + text + ")"
	}
	edited := r.content[:delStart] + r.content[delEnd:useStart] + text + r.content[useStart+len(use.Name):]
	return fmt.Sprintf("Inline variable '%s'", b.Name), edited, true
}

// bindingReadAt is the binding statement the name under the cursor reads.
func (r *refactorRequest) bindingReadAt() *ast.Binding {
	line := lineIndexOf(r.offs, r.start) + 1
	col := r.start - r.offs[line-1] + 1
	var use *ast.Ident
	for _, n := range r.all {
		if v, ok := n.(*ast.Ident); ok && v.Line == line && col >= v.Col && col <= v.Col+len(v.Name) {
			use = v
		}
	}
	if use == nil {
		return nil
	}
	sym := r.fa.References[analysis.Pos{Line: use.Line, Col: use.Col}]
	if sym == nil {
		return nil
	}
	id := symbolIdentity(sym)
	for _, n := range r.all {
		if b, ok := n.(*ast.Binding); ok && b.Name == use.Name {
			if def := r.fa.Definitions[analysis.Pos{Line: b.Line, Col: b.Col}]; def != nil && matchesIdentity(def, id) {
				return b
			}
		}
	}
	return nil
}

// sameMeaning reports whether every name value reads resolves, at pos, to
// what it resolves to where value stands.
func (r *refactorRequest) sameMeaning(value ast.Node, pos analysis.Pos) bool {
	scope := r.fa.ScopeAt(pos)
	if scope == nil {
		return false
	}
	ok := true
	var walk func(m ast.Node)
	walk = func(m ast.Node) {
		if v, isIdent := m.(*ast.Ident); isIdent && !r.isFieldName(v) {
			if sym := r.fa.References[analysis.Pos{Line: v.Line, Col: v.Col}]; sym != nil {
				// A name the scopes do not hold (a type, a file) is not a
				// binding anything between can shadow; a binding is.
				at := scope.Lookup(v.Name)
				if at != nil && !matchesIdentity(at, symbolIdentity(sym)) {
					ok = false
				}
				if at == nil && r.declares(sym) {
					ok = false
				}
			}
		}
		for _, c := range r.kids[m] {
			walk(c)
		}
	}
	walk(value)
	return ok
}

// isFieldName reports whether id is the name after a `.`.
func (r *refactorRequest) isFieldName(id *ast.Ident) bool {
	fa, ok := r.parent[id].(*ast.FieldAccess)
	return ok && fa.Field == id
}

// declares reports whether sym is defined inside the declaration.
func (r *refactorRequest) declares(sym *analysis.Symbol) bool {
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	first, last := 0, 0
	if s, e, ok := r.span(r.decl); ok {
		first, last = lineIndexOf(r.offs, s)+1, lineIndexOf(r.offs, e)+1
	}
	return sym.Pos.Line >= first && sym.Pos.Line <= last && (sym.SourceFile == "" || sym.SourceFile == uriToPath(r.uri))
}

// nextStatement reports whether next directly follows stmt in block.
func (r *refactorRequest) nextStatement(block *ast.Block, stmt, next ast.Node) bool {
	for i, s := range block.Stmts {
		if s == stmt {
			return i+1 < len(block.Stmts) && block.Stmts[i+1] == next
		}
	}
	return false
}

// needsParens reports whether value, written where use stands, needs
// parentheses to keep its shape.
func (r *refactorRequest) needsParens(value ast.Node, use ast.Node) bool {
	switch value.(type) {
	case *ast.Binary, *ast.Unary, *ast.If, *ast.Case, *ast.RangeLit, *ast.TryOp, *ast.Dbg, *ast.Lambda:
	default:
		return false
	}
	switch p := r.parent[use].(type) {
	case *ast.Call:
		return p.Func == use
	case *ast.Binary:
		if p.Op == "|>" && p.Left == use && isPipe(value) {
			return false
		}
		return true
	case *ast.NamedArg, *ast.Binding, *ast.ExprStmt, *ast.Return, *ast.ListLit, *ast.VectorLit,
		*ast.SetLit, *ast.TupleLit, *ast.MapLit, *ast.StructLit, *ast.StringInterp, *ast.GroupedExpr, *ast.Block:
		return false
	}
	return true
}
