package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// RecursiveRenderCode tags a Display or Debug implementation that renders its
// own receiver through the function it is defining.
const RecursiveRenderCode = "recursive-render"

// UnconditionalRecursionCode tags a function every path of which calls the
// function itself with its own parameters before it can return.
const UnconditionalRecursionCode = "unconditional-recursion"

// selfRecursion checks one function body for calls back into the function
// with the arguments it was given. Such a call repeats the call that is
// running, so a body that cannot avoid one never returns.
type selfRecursion struct {
	fa *FileAnalysis
	fn *ast.FuncDef
	// iface is the interface the enclosing `impl Iface for T` block
	// implements, when fn is one of that block's functions; nil otherwise.
	iface *ast.InterfaceDef
	// owner is how the function is spelled at a call site: `Foo.to_string`
	// for an impl function, the bare name otherwise.
	owner string
	// calls and exits remember mayCall's and mayExit's answer per node.
	// mustRecurse asks both of every operand it passes, and each answer
	// walks the operand's whole subtree, so without them a chain of n
	// operators (`"a" + "a" + …`) or n nested `if`s took n² steps.
	calls, exits map[ast.Node]bool
	// walked counts the nodes mayCall and mayExit computed an answer for.
	walked int
}

// checkSelfRecursion runs both rules over fn, a function the checker has just
// checked. impl is the block fn is an item of, or nil.
//
// The render rule is an error in a Display `to_string` or Debug `inspect`:
// rendering the receiver itself (`"${foo}"`, `Display.to_string(foo)`,
// `io.print(foo)`, `dbg foo`, ...) calls the function being defined with the
// value it was given. It applies wherever the rendering appears in the body,
// conditional or not: the recursive call receives the same value and takes
// the same path.
//
// The unconditional rule reports a function each path of which reaches a
// call to itself with its parameters unchanged before it can return. It is
// conservative: a recursive call in tail position counts only when nothing
// before it on its path calls anything, because a tail call does not grow
// the stack and an effect before it can make the loop a deliberate one.
func (c *checker) checkSelfRecursion(fn *ast.FuncDef, impl *ast.ImplBlock) {
	if fn == nil || fn.Body == nil || fn.AutoSynth || IsSynthesizedLine(fn.Line) {
		return
	}
	r := &selfRecursion{fa: c.fa, fn: fn, owner: fn.Name}
	if impl != nil {
		if impl.Interface != nil {
			r.iface = r.interfaceOf(impl.Interface)
		}
		if recv := TypeExprBaseName(impl.Receiver); recv != "" {
			r.owner = recv + "." + fn.Name
		}
	}
	if errs := r.renderErrors(); len(errs) > 0 {
		c.errors = append(c.errors, errs...)
		return
	}
	if r.mustRecurse(fn.Body, true, true) {
		names := make([]string, len(fn.Params))
		for i, p := range fn.Params {
			names[i] = p.Name
		}
		c.errors = append(c.errors, TypeError{
			Line: fn.Line,
			Col:  fn.Col,
			Message: fmt.Sprintf(
				"every path through %s calls %s(%s) with the arguments it was given, so it never returns",
				fn.Name, r.owner, strings.Join(names, ", ")),
			Code: UnconditionalRecursionCode,
		})
	}
}

// ref is the symbol recorded at a position, followed through import
// re-exports to the declaration.
func (r *selfRecursion) ref(line, col int) *Symbol {
	s := r.fa.References[Pos{Line: line, Col: col}]
	for i := 0; s != nil && s.Resolved != nil && i < 16; i++ {
		s = s.Resolved
	}
	return s
}

func (r *selfRecursion) interfaceOf(t ast.TypeExpr) *ast.InterfaceDef {
	st, ok := t.(*ast.SimpleType)
	if !ok {
		return nil
	}
	s := r.ref(st.Line, st.Col)
	if s == nil {
		return nil
	}
	def, _ := s.Node.(*ast.InterfaceDef)
	return def
}

func unparen(n ast.Node) ast.Node {
	for {
		g, ok := n.(*ast.GroupedExpr)
		if !ok {
			return n
		}
		n = g.Expr
	}
}

// isParam reports whether expr is the function's parameter i, either named
// directly or through a chain of plain bindings (`x = foo`) of it.
func (r *selfRecursion) isParam(expr ast.Node, i int) bool {
	if i >= len(r.fn.Params) {
		return false
	}
	p := r.fn.Params[i]
	if p.Destructure != nil || p.Name == "" || ast.IsDiscardName(p.Name) {
		return false
	}
	for depth := 0; depth < 16; depth++ {
		id, ok := unparen(expr).(*ast.Ident)
		if !ok {
			return false
		}
		s := r.ref(id.Line, id.Col)
		if s == nil {
			return false
		}
		switch s.Kind {
		case SymbolParam:
			return s.Node == ast.Node(r.fn) && s.Name == p.Name
		case SymbolBinding:
			b, ok := s.Node.(*ast.Binding)
			if !ok || b.Name != s.Name || b.Value == nil {
				return false
			}
			expr = b.Value
		default:
			return false
		}
	}
	return false
}

// isSelfCallee reports whether callee names the function being checked:
// its own name, its owner-qualified name (`Foo.to_string`), or, in an
// interface impl, the interface-qualified name (`Display.to_string`), which
// dispatches on the receiver's type back to this impl when the receiver is
// passed unchanged.
func (r *selfRecursion) isSelfCallee(callee ast.Node) bool {
	switch x := unparen(callee).(type) {
	case *ast.Ident:
		s := r.ref(x.Line, x.Col)
		return s != nil && s.Kind == SymbolFunction && s.Node == ast.Node(r.fn)
	case *ast.FieldAccess:
		if x.Field == nil {
			return false
		}
		if s := r.ref(x.Field.Line, x.Field.Col); s != nil && s.Kind == SymbolFunction && s.Node == ast.Node(r.fn) {
			return true
		}
		if r.iface == nil || x.Field.Name != r.fn.Name {
			return false
		}
		var line, col int
		switch o := x.Object.(type) {
		case *ast.Ident:
			line, col = o.Line, o.Col
		case *ast.TypeIdent:
			line, col = o.Line, o.Col
		default:
			return false
		}
		s := r.ref(line, col)
		return s != nil && s.Node == ast.Node(r.iface)
	}
	return false
}

// call normalizes a call or a pipe into its callee and arguments. ok is
// false for anything else, for a partial application (`f(_, x)` outside a
// pipe builds a function rather than calling one), and for a call with
// explicit type arguments, which may name another instantiation.
func call(n ast.Node) (callee ast.Node, args []ast.Node, ok bool) {
	switch x := unparen(n).(type) {
	case *ast.Call:
		if len(x.TypeArgs) > 0 {
			return nil, nil, false
		}
		for _, a := range x.Args {
			if _, hole := a.(*ast.Placeholder); hole {
				return nil, nil, false
			}
		}
		return x.Func, x.Args, true
	case *ast.Binary:
		if x.Op != "|>" {
			return nil, nil, false
		}
		switch rhs := unparen(x.Right).(type) {
		case *ast.Call:
			if len(rhs.TypeArgs) > 0 {
				return nil, nil, false
			}
			filled := false
			out := make([]ast.Node, 0, len(rhs.Args)+1)
			for _, a := range rhs.Args {
				if _, hole := a.(*ast.Placeholder); hole {
					if filled {
						return nil, nil, false
					}
					filled = true
					out = append(out, x.Left)
					continue
				}
				out = append(out, a)
			}
			if !filled {
				out = append([]ast.Node{x.Left}, out...)
			}
			return rhs.Func, out, true
		}
	}
	return nil, nil, false
}

// sameArgs reports whether args pass every parameter of the function
// through unchanged, in its own position or by its own name.
func (r *selfRecursion) sameArgs(args []ast.Node) bool {
	if len(args) != len(r.fn.Params) {
		return false
	}
	seen := make([]bool, len(args))
	for i, a := range args {
		j := i
		if na, ok := a.(*ast.NamedArg); ok {
			j = -1
			for k, p := range r.fn.Params {
				if p.Name == na.Name {
					j = k
				}
			}
			if j < 0 {
				return false
			}
			a = na.Value
		}
		if seen[j] || !r.isParam(a, j) {
			return false
		}
		seen[j] = true
	}
	return true
}

// isSelfCall reports whether n calls the function being checked with the
// arguments it was given.
func (r *selfRecursion) isSelfCall(n ast.Node) bool {
	callee, args, ok := call(n)
	return ok && r.isSelfCallee(callee) && r.sameArgs(args)
}

// renderErrors applies the render rule to a Display `to_string` or Debug
// `inspect` implementation.
func (r *selfRecursion) renderErrors() []TypeError {
	if r.iface == nil || len(r.fn.Params) != 1 {
		return nil
	}
	var display bool
	switch {
	case r.iface.Name == "Display" && r.fn.Name == "to_string":
		display = true
	case r.iface.Name == "Debug" && r.fn.Name == "inspect":
	default:
		return nil
	}
	recv := r.fn.Params[0].Name
	dispatch := r.iface.Name + "." + r.fn.Name + "(" + recv + ")"
	var errs []TypeError
	report := func(line, col int, over ast.Node, form string, direct bool) {
		msg := fmt.Sprintf("%s calls %s, the function being defined, so it never returns", form, dispatch)
		if direct {
			msg = fmt.Sprintf("%s calls the function being defined with its own receiver, so it never returns", form)
		}
		e := TypeError{Line: line, Col: col, Message: msg, Code: RecursiveRenderCode}
		if sp, ok := spanOf(over); ok && sp.StartLine == line && sp.StartCol == col {
			e.EndLine, e.EndCol = sp.EndLine, sp.EndCol
		}
		if display {
			// Every type has a structural Debug, so inspecting the receiver
			// is the rendering that cannot recurse here.
			e = e.WithHint("Debug.inspect(" + recv + ") renders its fields")
		}
		errs = append(errs, e)
	}
	WalkNodes(r.fn.Body, func(n ast.Node) {
		switch x := n.(type) {
		case *ast.StringInterp:
			if !display {
				return
			}
			for _, part := range x.Parts {
				se, ok := part.(ast.StringExpr)
				if !ok || !r.isParam(se.Expr, 0) {
					continue
				}
				line, col := slotPosition(se)
				report(line, col, nil, `"${`+exprText(se.Expr)+`}"`, false)
			}
		case *ast.Dbg:
			if !display && x.Expr != nil && r.isParam(x.Expr, 0) {
				report(x.Line, x.Col, x, "dbg "+exprText(x.Expr), false)
			}
		case *ast.Call, *ast.Binary:
			callee, args, ok := call(x)
			if !ok || len(args) != 1 || !r.isParam(args[0], 0) {
				return
			}
			line, col := stmtStart(x)
			form := calleeText(callee) + "(" + exprText(args[0]) + ")"
			if r.isSelfCallee(callee) {
				report(line, col, x, form, true)
				return
			}
			if r.isStdIORender(callee, display) {
				report(line, col, x, form, false)
			}
		}
	})
	return errs
}

// isStdIORender reports whether callee is the std/io function that renders
// its argument through the interface being implemented: `io.print` and
// `io.write` through Display, `io.inspect` through Debug.
func (r *selfRecursion) isStdIORender(callee ast.Node, display bool) bool {
	fa, ok := unparen(callee).(*ast.FieldAccess)
	if !ok || fa.Field == nil {
		return false
	}
	switch fa.Field.Name {
	case "print", "write":
		if !display {
			return false
		}
	case "inspect":
		if display {
			return false
		}
	default:
		return false
	}
	obj, ok := fa.Object.(*ast.Ident)
	if !ok {
		return false
	}
	s := r.ref(obj.Line, obj.Col)
	if s == nil || s.Kind != SymbolModule {
		return false
	}
	imp, ok := s.Node.(*ast.ImportStmt)
	if !ok || len(imp.Names) != 0 || len(imp.ModulePath) != 2 {
		return false
	}
	return identText(imp.ModulePath[0]) == "std" && identText(imp.ModulePath[1]) == "io"
}

func identText(n ast.Node) string {
	switch x := n.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.TypeIdent:
		return x.Name
	}
	return ""
}

func exprText(n ast.Node) string {
	if s := identText(unparen(n)); s != "" {
		return s
	}
	return "..."
}

// mustRecurse reports whether every execution of n that does not fault
// reaches a qualifying self-call before n completes. tail is whether n's
// value is the function's result; clean is whether nothing before n on the
// current path may have called anything.
//
// A self-call outside tail position always qualifies: each one keeps its
// caller's frame, so the chain ends in a stack overflow. One in tail position
// qualifies only on a clean path, where the loop has no effect that could be
// its purpose.
func (r *selfRecursion) mustRecurse(n ast.Node, tail, clean bool) bool {
	if n == nil {
		return false
	}
	if r.isSelfCall(n) {
		return !tail || clean
	}
	switch x := n.(type) {
	case *ast.ExprStmt:
		return r.mustRecurse(x.Expr, tail, clean)
	case *ast.GroupedExpr:
		return r.mustRecurse(x.Expr, tail, clean)
	case *ast.Block:
		if x == nil {
			return false
		}
		for i, s := range x.Stmts {
			if r.mustRecurse(s, tail && i == len(x.Stmts)-1, clean) {
				return true
			}
			if r.mayExit(s) {
				return false
			}
			clean = clean && !r.mayCall(s)
		}
		return false
	case *ast.Binding:
		return r.mustRecurse(x.Value, false, clean)
	case *ast.TupleDestructure:
		return r.mustRecurse(x.Value, false, clean)
	case *ast.StructDestructure:
		return r.mustRecurse(x.Value, false, clean)
	case *ast.MapDestructure:
		return r.mustRecurse(x.Value, false, clean)
	case *ast.DistinctDestructure:
		return r.mustRecurse(x.Value, false, clean)
	case *ast.PatternBinding:
		return r.mustRecurse(x.Value, false, clean)
	case *ast.With:
		return r.mustRecurse(x.Value, false, clean)
	case *ast.Return:
		return r.mustRecurse(x.Value, true, clean)
	case *ast.TryOp:
		return r.mustRecurse(x.Expr, false, clean)
	case *ast.Dbg:
		return r.mustRecurse(x.Expr, false, clean)
	case *ast.Then:
		return r.mustRecurse(x.Lambda, false, clean)
	case *ast.Tap:
		return r.mustRecurse(x.Lambda, false, clean)
	case *ast.Unary:
		return r.mustRecurse(x.Right, false, clean)
	case *ast.FieldAccess:
		return r.mustRecurse(x.Object, false, clean)
	case *ast.NamedArg:
		return r.mustRecurse(x.Value, false, clean)
	case *ast.Call:
		if r.mustRecurse(x.Func, false, clean) {
			return true
		}
		return r.mustRecurseSeq(x.Args, clean && !r.mayCall(x.Func))
	case *ast.Binary:
		switch x.Op {
		case "and", "or":
			return r.mustRecurse(x.Left, false, clean)
		case "|>":
			if r.mustRecurse(x.Left, false, clean) {
				return true
			}
			if rc, ok := unparen(x.Right).(*ast.Call); ok && !r.mayExit(x.Left) {
				return r.mustRecurseSeq(rc.Args, clean && !r.mayCall(x.Left))
			}
			return false
		}
		return r.mustRecurseSeq([]ast.Node{x.Left, x.Right}, clean)
	case *ast.StringInterp:
		var parts []ast.Node
		for _, p := range x.Parts {
			if se, ok := p.(ast.StringExpr); ok {
				parts = append(parts, se.Expr)
			}
		}
		return r.mustRecurseSeq(parts, clean)
	case *ast.ListLit:
		return r.mustRecurseSeq(x.Items, clean)
	case *ast.VectorLit:
		return r.mustRecurseSeq(x.Items, clean)
	case *ast.SetLit:
		return r.mustRecurseSeq(x.Items, clean)
	case *ast.TupleLit:
		return r.mustRecurseSeq(x.Items, clean)
	case *ast.If:
		if x.Else == nil {
			return false
		}
		if r.mustRecurse(x.Cond, false, clean) {
			return true
		}
		if r.mayExit(x.Cond) {
			return false
		}
		clean = clean && !r.mayCall(x.Cond)
		return r.mustRecurse(x.Then, tail, clean) && r.mustRecurse(x.Else, tail, clean)
	case *ast.Case:
		if len(x.Branches) == 0 {
			return false
		}
		if r.mustRecurse(x.Value, false, clean) {
			return true
		}
		if r.mayExit(x.Value) {
			return false
		}
		clean = clean && !r.mayCall(x.Value)
		for _, br := range x.Branches {
			if br.Guard != nil && (r.mayExit(br.Guard) || r.mayCall(br.Guard)) {
				if r.mayExit(br.Guard) {
					return false
				}
				clean = false
			}
		}
		for _, br := range x.Branches {
			if !r.mustRecurse(br.Body, tail, clean) {
				return false
			}
		}
		return true
	}
	return false
}

// mustRecurseSeq is mustRecurse over operands evaluated left to right, none
// in tail position.
func (r *selfRecursion) mustRecurseSeq(ns []ast.Node, clean bool) bool {
	for _, n := range ns {
		if r.mustRecurse(n, false, clean) {
			return true
		}
		if r.mayExit(n) {
			return false
		}
		clean = clean && !r.mayCall(n)
	}
	return false
}

// mayCall over-approximates whether evaluating n may call a function: any
// call, interpolation (Display), `dbg`, an operator or a pattern on a
// non-primitive operand (an operator impl), and every node kind not listed.
// Creating a lambda calls nothing.
func (r *selfRecursion) mayCall(n ast.Node) bool {
	if v, ok := r.calls[n]; ok {
		return v
	}
	v := r.mayCallUncached(n)
	if r.calls == nil {
		r.calls = map[ast.Node]bool{}
	}
	r.calls[n] = v
	return v
}

func (r *selfRecursion) mayCallUncached(n ast.Node) bool {
	r.walked++
	switch x := n.(type) {
	case nil:
		return false
	case *ast.Ident, *ast.TypeIdent, *ast.IntLit, *ast.FloatLit, *ast.DecimalLit,
		*ast.CodepointLit, *ast.StringLit, *ast.Lambda, *ast.FuncDef, *ast.Placeholder:
		return false
	case *ast.ExprStmt:
		return r.mayCall(x.Expr)
	case *ast.GroupedExpr:
		return r.mayCall(x.Expr)
	case *ast.Binding:
		return r.mayCall(x.Value)
	case *ast.Return:
		return r.mayCall(x.Value)
	case *ast.FieldAccess:
		return r.mayCall(x.Object)
	case *ast.Unary:
		return !r.primitive(x.Right) || r.mayCall(x.Right)
	case *ast.Binary:
		if x.Op == "|>" {
			return true
		}
		if x.Op != "and" && x.Op != "or" && (!r.primitive(x.Left) || !r.primitive(x.Right)) {
			return true
		}
		return r.mayCall(x.Left) || r.mayCall(x.Right)
	case *ast.ListLit:
		return r.anyMayCall(x.Items)
	case *ast.TupleLit:
		return r.anyMayCall(x.Items)
	case *ast.Block:
		if x == nil {
			return false
		}
		return r.anyMayCall(x.Stmts)
	case *ast.If:
		if x.CondPattern != nil {
			return true
		}
		return r.mayCall(x.Cond) || r.mayCall(x.Then) || (x.Else != nil && r.mayCall(x.Else))
	}
	return true
}

func (r *selfRecursion) anyMayCall(ns []ast.Node) bool {
	for _, n := range ns {
		if r.mayCall(n) {
			return true
		}
	}
	return false
}

// primitive reports whether the checker typed n as Int, Float, Byte, String
// or Bool, whose operators call no user code.
func (r *selfRecursion) primitive(n ast.Node) bool {
	t := r.fa.ExprTypes[n]
	for i := 0; i < 16; i++ {
		tv, ok := t.(*TypeVar)
		if !ok || tv.Resolved == nil {
			break
		}
		t = tv.Resolved
	}
	if t == nil {
		return false
	}
	if _, ok := t.(*PrimitiveType); ok {
		return true
	}
	return t == TypeBool
}

// mayExit over-approximates whether evaluating n may leave the function
// early without faulting: a `return`, a `try`, a binding's `else`, and
// every node kind not listed. A lambda or nested `fn` exits only itself.
func (r *selfRecursion) mayExit(n ast.Node) bool {
	if v, ok := r.exits[n]; ok {
		return v
	}
	v := r.mayExitUncached(n)
	if r.exits == nil {
		r.exits = map[ast.Node]bool{}
	}
	r.exits[n] = v
	return v
}

func (r *selfRecursion) mayExitUncached(n ast.Node) bool {
	r.walked++
	switch x := n.(type) {
	case nil:
		return false
	case *ast.Ident, *ast.TypeIdent, *ast.IntLit, *ast.FloatLit, *ast.DecimalLit,
		*ast.CodepointLit, *ast.StringLit, *ast.Lambda, *ast.FuncDef, *ast.Placeholder:
		return false
	case *ast.Return, *ast.TryOp, *ast.Break, *ast.Continue:
		return true
	case *ast.ExprStmt:
		return r.mayExit(x.Expr)
	case *ast.GroupedExpr:
		return r.mayExit(x.Expr)
	case *ast.Binding:
		return r.mayExit(x.Value)
	case *ast.TupleDestructure:
		return r.mayExit(x.Value)
	case *ast.StructDestructure:
		return r.mayExit(x.Value)
	case *ast.PatternBinding:
		return x.Else != nil || r.mayExit(x.Value)
	case *ast.Dbg:
		return r.mayExit(x.Expr)
	case *ast.Then:
		return r.mayExit(x.Lambda)
	case *ast.Tap:
		return r.mayExit(x.Lambda)
	case *ast.FieldAccess:
		return r.mayExit(x.Object)
	case *ast.Unary:
		return r.mayExit(x.Right)
	case *ast.NamedArg:
		return r.mayExit(x.Value)
	case *ast.Binary:
		return r.mayExit(x.Left) || r.mayExit(x.Right)
	case *ast.Call:
		return r.mayExit(x.Func) || r.anyMayExit(x.Args)
	case *ast.StringInterp:
		for _, p := range x.Parts {
			if se, ok := p.(ast.StringExpr); ok && r.mayExit(se.Expr) {
				return true
			}
		}
		return false
	case *ast.ListLit:
		return r.anyMayExit(x.Items)
	case *ast.TupleLit:
		return r.anyMayExit(x.Items)
	case *ast.Block:
		if x == nil {
			return false
		}
		return r.anyMayExit(x.Stmts)
	case *ast.If:
		return r.mayExit(x.Cond) || r.mayExit(x.Then) || (x.Else != nil && r.mayExit(x.Else))
	case *ast.Case:
		if r.mayExit(x.Value) {
			return true
		}
		for _, br := range x.Branches {
			if r.mayExit(br.Guard) || r.mayExit(br.Body) {
				return true
			}
		}
		return false
	}
	return true
}

func (r *selfRecursion) anyMayExit(ns []ast.Node) bool {
	for _, n := range ns {
		if r.mayExit(n) {
			return true
		}
	}
	return false
}
