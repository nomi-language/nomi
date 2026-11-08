package lsp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// "Use a capture" rewrites a lambda as the capture it says (spec §7,
// *Captures*): `fn s -> s > 50` becomes `&(&1 > 50)`, `fn l
// -> Set.contains(letters, l)` becomes `&Set.contains(letters, &1)`, `fn
// x -> Point{x: x, y: 0}` becomes `&Point{x: &1, y: 0}`, `fn s ->
// grade(s.score)` becomes `&grade(&1.score)`, `fn x -> "id ${x}"` becomes
// `&"id ${&1}"`. "Write as a lambda" is the way back: a capture becomes
// `fn x -> ...`, each parameter named for the callee's parameter it fills,
// else `x`, `y`, ...
//
// lambdaPlaceholder decides which lambdas the first applies to. A capture
// evaluates its body on each call as the lambda does, so the rewrite keeps
// what the lambda does. A lambda qualifies when
//
//   - it has one parameter, or two, each a plain name with no annotation
//     and no default;
//   - its body is one expression, not a block: a call, an operator other
//     than `and`, `or` and `|>`, a struct or tuple literal, or a one-line
//     string;
//   - each parameter is read in that expression, at any depth and any
//     number of times (`fn t -> t == String.reverse(t)` becomes
//     `&(&1 == String.reverse(&1))`), the first reads in the order the
//     parameters are written, and none inside a lambda or capture of its
//     own, where `&N` would not be this function's parameter; the body
//     holds no capture, since captures do not nest;
//   - the checker expected a function type of its arity where it stands,
//     which gives `&N` its type;
//   - for a partial capture (a call that takes each parameter once, whole,
//     as its own argument, in order), each parameter's type is the one the
//     callee's parameter declares, up to the callee's own type parameters:
//     a partial capture takes the callee's parameter types, so a lambda
//     over a List passed where the callee takes an Iter keeps its lambda.
//     Any other capture takes the expected function type's parameters, as
//     the lambda does.
//
// A lambda whose whole body is a field read, `fn s -> s.score`, keeps its
// form: style rule 10 writes it `.score`, and no action rewrites it.
//
// Both rewrites need an expected function type, so a lambda bound with no
// annotation, whose parameter only its body types, keeps its form.

// placeholderRewrite is one lambda's capture form: the byte range of the
// lambda's text and the text that replaces it.
type placeholderRewrite struct {
	start, end int
	text       string
}

// placeholderUse is one read of a lambda parameter in the body.
type placeholderUse struct {
	param int
	id    *ast.Ident
	// field marks the receiver of a field read, `s` in `s.score`.
	field bool
	// pun marks a punned struct field, `Point{x}`, whose one token is
	// both the field's label and the read.
	pun bool
}

// lambdaPlaceholder answers the capture form of l. When l does not qualify
// it answers why: notPlaceholderShape when the rule keeps the lambda for
// its shape (a pattern, a block, a bare field read, a read out of order),
// else what the position or the other arguments lack.
func (r *refactorRequest) lambdaPlaceholder(l *ast.Lambda) (placeholderRewrite, string) {
	var none placeholderRewrite
	if len(l.Params) == 0 || len(l.Params) > 2 || l.Body == nil || l.Body.EndLine != 0 || len(l.Body.Stmts) != 1 {
		return none, notPlaceholderShape
	}
	names := map[string]int{}
	for i, p := range l.Params {
		if p.Name == "" || strings.HasPrefix(p.Name, "_") || p.Destructure != nil || p.TypeAnnotation != nil || p.Default != nil {
			return none, notPlaceholderShape
		}
		if _, dup := names[p.Name]; dup {
			return none, notPlaceholderShape
		}
		names[p.Name] = i
	}
	stmt, ok := l.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || stmt.Expr == nil {
		return none, notPlaceholderShape
	}
	body := stmt.Expr
	var operands []ast.Node
	var callee ast.Node
	switch e := body.(type) {
	case *ast.Call:
		if len(e.TypeArgs) > 0 || !isCalleeName(e.Func) {
			return none, notPlaceholderShape
		}
		callee = e.Func
		for _, a := range e.Args {
			if na, ok := a.(*ast.NamedArg); ok {
				operands = append(operands, na.Value)
				continue
			}
			operands = append(operands, a)
		}
	case *ast.Binary:
		switch e.Op {
		case "and", "or", "|>":
			return none, notPlaceholderShape
		}
		operands = []ast.Node{e.Left, e.Right}
	case *ast.Unary:
		operands = []ast.Node{e.Right}
	case *ast.Is:
		operands = []ast.Node{e.Value}
	case *ast.Ascription:
		operands = []ast.Node{e.Value}
	case *ast.StructLit:
		if e.Spread != nil {
			return none, notPlaceholderShape
		}
		for _, f := range e.Fields {
			operands = append(operands, f.Value)
		}
	case *ast.TupleLit:
		operands = e.Items
	case *ast.StringInterp:
		if e.Triple {
			return none, notPlaceholderShape
		}
		for _, part := range e.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				operands = append(operands, se.Expr)
			}
		}
	default:
		return none, notPlaceholderShape
	}
	if callee != nil && mentions(callee, names) {
		return none, notPlaceholderShape
	}
	uses, ok := r.parameterReads(l, body, names)
	if !ok {
		return none, notPlaceholderShape
	}
	// A call that takes each parameter once, whole, as its own argument in
	// the order they are written is a partial capture (spec §7 *Captures*).
	partial := false
	if _, isCall := body.(*ast.Call); isCall && len(uses) == len(l.Params) {
		partial = true
		for i, u := range uses {
			partial = partial && u.param == i && !u.field && containsNode(operands, u.id)
		}
	}
	ft, ok := analysis.ResolveTypeVar(r.fa.ExpectedTypes[l]).(*analysis.FuncType)
	switch p := r.parent[l].(type) {
	case *ast.Then, *ast.Tap:
		// A `then` or `tap` stage's lambda takes the piped value.
		if p.(interface{ Fn() ast.Node }).Fn() == ast.Node(l) {
			ft, ok = analysis.ResolveTypeVar(r.fa.ExprTypes[l]).(*analysis.FuncType)
		}
	}
	if !ok || len(ft.Params) != len(l.Params) {
		return none, "no function type is expected where it stands"
	}
	if partial {
		for _, u := range uses {
			if !r.fillsDeclaredType(u.id) {
				return none, "the call converts the parameter to the type the callee declares"
			}
		}
	}
	return r.placeholderText(l, uses)
}

// notPlaceholderShape is lambdaPlaceholder's answer for a lambda the
// style rule keeps for its shape.
const notPlaceholderShape = "the rule keeps its shape"

// placeholderText is the rewrite of l to the capture of its body with each
// use written `&N`: `&` against a call or a literal, `&(...)` around an
// operator.
func (r *refactorRequest) placeholderText(l *ast.Lambda, uses []placeholderUse) (placeholderRewrite, string) {
	var none placeholderRewrite
	if l.Line <= 0 || l.EndLine <= 0 || analysis.IsSynthesizedLine(l.Line) {
		return none, "it has no source position"
	}
	start := posToOffset(r.offs, l.Line, l.Col)
	end := posToOffset(r.offs, l.EndLine, l.EndCol)
	arrow := posToOffset(r.offs, l.ArrowLine, l.ArrowCol)
	if !(start < arrow && arrow+2 <= end && end <= len(r.content)) || r.content[start:start+2] != "fn" || r.content[arrow:arrow+2] != "->" {
		return none, "its text is not where its positions say"
	}
	text := r.content[start:end]
	// A comment inside the lambda would be lost or moved; a lambda in a
	// `//!` prompt must stay on its line.
	if strings.Contains(text, "//") {
		return none, "it holds a comment"
	}
	if strings.HasPrefix(strings.TrimSpace(r.content[r.lineStart(start):start]), "//") && strings.Contains(text, "\n") {
		return none, "it spans lines of a prompt"
	}
	bodyStart := arrow + 2
	for bodyStart < end && strings.ContainsRune(" \t\r\n", rune(r.content[bodyStart])) {
		bodyStart++
	}
	var b strings.Builder
	_, literal := l.Body.Stmts[0].(*ast.ExprStmt).Expr.(*ast.StructLit)
	switch l.Body.Stmts[0].(*ast.ExprStmt).Expr.(type) {
	case *ast.TupleLit, *ast.StringInterp:
		literal = true
	}
	_, call := l.Body.Stmts[0].(*ast.ExprStmt).Expr.(*ast.Call)
	wrap := !literal && !call
	if wrap {
		b.WriteString("&(")
	} else {
		b.WriteString("&")
	}
	at := bodyStart
	for _, u := range uses {
		off := posToOffset(r.offs, u.id.Line, u.id.Col)
		if off < at || off+len(u.id.Name) > end || r.content[off:off+len(u.id.Name)] != u.id.Name {
			return none, "its text is not where its positions say"
		}
		b.WriteString(r.content[at:off])
		hole := fmt.Sprintf("&%d", u.param+1)
		if u.pun {
			hole = u.id.Name + ": " + hole
		}
		b.WriteString(hole)
		at = off + len(u.id.Name)
	}
	b.WriteString(strings.TrimRight(r.content[at:end], " \t\r\n"))
	if wrap {
		b.WriteString(")")
	}
	return placeholderRewrite{start: start, end: end, text: b.String()}, ""
}

// parameterReads lists every read of l's parameters in body, in source
// order. It fails when a parameter is never read, when the first reads are
// out of the parameters' order, when a read stands inside a function of its
// own (a lambda or a capture, where `&N` would not be l's parameter), when
// the body holds a capture (captures do not nest), or when a name of a
// parameter in body resolves to something else.
func (r *refactorRequest) parameterReads(l *ast.Lambda, body ast.Node, names map[string]int) ([]placeholderUse, bool) {
	defs := make([]*analysis.Symbol, len(l.Params))
	for i, p := range l.Params {
		defs[i] = r.fa.Definitions[analysis.Pos{Line: p.Line, Col: p.Col}]
		if defs[i] == nil {
			return nil, false
		}
	}
	var uses []placeholderUse
	ok := true
	ast.Inspect(body, func(n ast.Node) bool {
		if !ok {
			return false
		}
		switch v := n.(type) {
		case *ast.Capture:
			ok = false
			return false
		case *ast.Ident:
			i, isParam := names[v.Name]
			if !isParam {
				return true
			}
			pos := analysis.Pos{Line: v.Line, Col: v.Col}
			sym := r.fa.References[pos]
			if sym == nil || !matchesIdentity(sym, symbolIdentity(defs[i])) {
				ok = false
				return false
			}
			for m := r.parent[v]; m != nil && m != body; m = r.parent[m] {
				switch m.(type) {
				case *ast.Lambda, *ast.Capture:
					ok = false
					return false
				}
			}
			_, pun := r.fa.PunnedFieldLabels[pos]
			fa, inField := r.parent[v].(*ast.FieldAccess)
			uses = append(uses, placeholderUse{param: i, id: v, field: inField && fa.Object == ast.Node(v), pun: pun})
		}
		return true
	})
	if !ok {
		return nil, false
	}
	sort.SliceStable(uses, func(a, b int) bool {
		if uses[a].id.Line != uses[b].id.Line {
			return uses[a].id.Line < uses[b].id.Line
		}
		return uses[a].id.Col < uses[b].id.Col
	})
	next := 0
	for _, u := range uses {
		if u.param > next {
			return nil, false
		}
		if u.param == next {
			next++
		}
	}
	return uses, next == len(l.Params)
}

// containsNode reports whether n is one of ns.
func containsNode(ns []ast.Node, n ast.Node) bool {
	for _, m := range ns {
		if m == n {
			return true
		}
	}
	return false
}

// isFunctionSlot reports whether n stands where a lambda can take the
// place n holds, its body ending where n ends: a call's argument, a struct
// field's value, a tuple element, a binding's value, a return's value, a
// statement or a `then` or `tap` stage's function.
func (r *refactorRequest) isFunctionSlot(n ast.Node) bool {
	switch p := r.parent[n].(type) {
	case *ast.Call:
		for _, a := range p.Args {
			if a == n {
				return true
			}
		}
	case *ast.NamedArg:
		_, inCall := r.parent[p].(*ast.Call)
		return p.Value == n && inCall
	case *ast.StructLit:
		for _, f := range p.Fields {
			if f.Value == n {
				return true
			}
		}
	case *ast.TupleLit:
		for _, it := range p.Items {
			if it == n {
				return true
			}
		}
	case *ast.Binding:
		return p.Value == n
	case *ast.Return:
		return p.Value == n
	case *ast.ExprStmt:
		return p.Expr == n
	case *ast.Then:
		return p.Capture == n
	case *ast.Tap:
		return p.Capture == n
	}
	return false
}

// isCalleeName reports whether a call's callee is a name the call reads
// when it runs: `f`, `Int.max`, `io.print`.
func isCalleeName(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.Ident, *ast.TypeIdent:
		return true
	case *ast.FieldAccess:
		return isCalleeName(v.Object)
	}
	return false
}

// mentions reports whether a name of names is read anywhere in n.
func mentions(n ast.Node, names map[string]int) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok {
			if _, hit := names[id.Name]; hit {
				found = true
			}
		}
		return !found
	})
	return found
}

// fillsDeclaredType reports whether the argument id has the type the
// callee's parameter declares, up to the callee's type parameters. A
// partial capture takes the callee's parameter type, so an argument the call
// converts (a List where an Iter is declared) would change the function's
// type.
func (r *refactorRequest) fillsDeclaredType(id *ast.Ident) bool {
	want := analysis.ResolveTypeVar(r.fa.ExpectedTypes[id])
	got := analysis.ResolveTypeVar(r.fa.ExprTypes[id])
	if want == nil || got == nil {
		return false
	}
	switch want.(type) {
	case *analysis.TypeParam_, *analysis.TypeVar:
		return true
	}
	if want.String() == got.String() {
		return true
	}
	wantOwner := analysis.TypeOwnerName(want)
	return wantOwner != "" && wantOwner == analysis.TypeOwnerName(got) && analysis.ContainsTypeParam(want)
}

// useCapture offers "Use a capture" on the innermost lambda under the
// range that qualifies.
func (r *refactorRequest) useCapture() (string, string, bool) {
	var found *ast.Lambda
	for _, n := range r.all {
		l, ok := n.(*ast.Lambda)
		if !ok || l.Line <= 0 || l.EndLine <= 0 {
			continue
		}
		start := posToOffset(r.offs, l.Line, l.Col)
		end := posToOffset(r.offs, l.EndLine, l.EndCol)
		if start <= r.start && r.end <= end {
			found = l
		}
	}
	if found == nil {
		return "", "", false
	}
	rw, why := r.lambdaPlaceholder(found)
	if why != "" {
		return "", "", false
	}
	return "Use a capture", r.replace(rw.start, rw.end, rw.text), true
}

// writeAsLambda offers "Write as a lambda" on the innermost capture under
// the range: `&(&1 > 2)` becomes `fn x -> x > 2`. It needs a function type
// expected where the capture stands, which gives the lambda's parameters
// theirs, or a `then` or `tap` stage, whose piped value does.
func (r *refactorRequest) writeAsLambda() (string, string, bool) {
	found := r.innermost(func(n ast.Node) bool {
		_, ok := n.(*ast.Capture)
		return ok
	})
	target, _ := found.(*ast.Capture)
	if target == nil || !r.isFunctionSlot(target) {
		return "", "", false
	}
	ft, ok := analysis.ResolveTypeVar(r.fa.ExprTypes[target]).(*analysis.FuncType)
	if !ok {
		return "", "", false
	}
	switch r.parent[target].(type) {
	case *ast.Then, *ast.Tap:
	default:
		if _, expected := analysis.ResolveTypeVar(r.fa.ExpectedTypes[target]).(*analysis.FuncType); !expected {
			return "", "", false
		}
	}
	type hole struct {
		n         int
		line, col int
		width     int
	}
	var holes []hole
	for _, h := range target.Holes() {
		n := ast.HoleNumber(h)
		line, col := nodePos(h)
		holes = append(holes, hole{n: n, line: line, col: col, width: len(fmt.Sprintf("&%d", n))})
	}
	if len(holes) == 0 {
		return "", "", false
	}
	for _, h := range holes {
		if h.n > len(ft.Params) {
			return "", "", false
		}
	}
	capStart, capEnd, ok := r.span(target)
	if !ok {
		return "", "", false
	}
	body := target.Body
	if g, grouped := body.(*ast.GroupedExpr); grouped {
		body = g.Expr
	}
	start, end, ok := r.span(body)
	if !ok {
		return "", "", false
	}
	tl, tc := nodePos(target)
	pos := analysis.Pos{Line: tl, Col: tc}
	used := map[string]bool{}
	params := make([]string, len(ft.Params))
	for i := range params {
		n := i + 1
		read := false
		for _, h := range holes {
			read = read || h.n == n
		}
		if !read {
			params[i] = "_"
			continue
		}
		base := r.holeParamName(target, n)
		name := r.freshName(base, pos)
		for k := 2; used[name]; k++ {
			name = r.freshName(fmt.Sprintf("%s%d", base, k), pos)
		}
		used[name] = true
		params[i] = name
	}
	var b strings.Builder
	at := start
	for _, h := range holes {
		off := posToOffset(r.offs, h.line, h.col)
		if off < at || off+h.width > end {
			return "", "", false
		}
		b.WriteString(r.content[at:off])
		b.WriteString(params[h.n-1])
		at = off + h.width
	}
	b.WriteString(r.content[at:end])
	text := "fn " + strings.Join(params, ", ") + " -> " + b.String()
	return "Write as a lambda", r.replace(capStart, capEnd, text), true
}

// holeParamName is the name for parameter n of the capture target: the
// callee's name for the parameter a hole `&n` fills as a whole argument,
// else `x`, `y`, `z`.
func (r *refactorRequest) holeParamName(target ast.Node, n int) string {
	var name string
	ast.Inspect(target, func(m ast.Node) bool {
		if name != "" {
			return false
		}
		call, ok := m.(*ast.Call)
		if !ok {
			switch m.(type) {
			case *ast.Lambda:
				return false
			case *ast.Capture:
				return m == target
			}
			return true
		}
		positional := 0
		for _, a := range call.Args {
			slot, named := positional, ""
			if na, ok := a.(*ast.NamedArg); ok {
				a, named = na.Value, na.Name
			} else {
				positional++
			}
			if ast.HoleNumber(a) != n {
				continue
			}
			if named != "" {
				name = named
			} else {
				name = r.calleeParamName(call, slot)
			}
			if name == "self" || strings.HasPrefix(name, "_") {
				name = ""
			}
			if name != "" {
				return false
			}
		}
		return true
	})
	if name != "" {
		return name
	}
	switch n {
	case 1:
		return "x"
	case 2:
		return "y"
	case 3:
		return "z"
	}
	return fmt.Sprintf("x%d", n)
}

// calleeParamName is the name the declaration of call's callee gives its
// parameter at slot, or "" when the declaration is not known.
func (r *refactorRequest) calleeParamName(call *ast.Call, slot int) string {
	var id *ast.Ident
	switch f := call.Func.(type) {
	case *ast.Ident:
		id = f
	case *ast.FieldAccess:
		id = f.Field
	}
	if id == nil {
		return ""
	}
	sym := r.fa.References[analysis.Pos{Line: id.Line, Col: id.Col}]
	if sym == nil {
		return ""
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	var params []ast.Param
	switch d := sym.Node.(type) {
	case *ast.FuncDef:
		params = d.Params
	case *ast.ExternFunc:
		params = d.Params
	case *ast.InterfaceMethod:
		params = d.Params
	}
	if slot < len(params) && params[slot].Destructure == nil {
		return params[slot].Name
	}
	return ""
}

// tapByName offers "Pass `f` by name" on a `tap` stage's capture that only
// passes the piped value on, `tap &f(&1)`: the stage takes the function
// itself, `tap f` (spec §8 *The `tap` stage*).
func (r *refactorRequest) tapByName() (string, string, bool) {
	found := r.innermost(func(n ast.Node) bool {
		_, ok := n.(*ast.Capture)
		return ok
	})
	target, _ := found.(*ast.Capture)
	if target == nil {
		return "", "", false
	}
	if tap, ok := r.parent[target].(*ast.Tap); !ok || tap.Capture != target {
		return "", "", false
	}
	call := target.PartialCall()
	if call == nil || len(call.Args) != 1 || len(call.TypeArgs) > 0 || !isCalleeName(call.Func) {
		return "", "", false
	}
	if hole, ok := call.Args[0].(*ast.Placeholder); !ok || hole.N != 1 {
		return "", "", false
	}
	capStart, capEnd, ok := r.span(target)
	if !ok {
		return "", "", false
	}
	nameStart, nameEnd, ok := r.span(call.Func)
	if !ok {
		return "", "", false
	}
	name := r.content[nameStart:nameEnd]
	return "Pass `" + name + "` by name", r.replace(capStart, capEnd, name), true
}
