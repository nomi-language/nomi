package lsp

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// "Convert to pipe" rewrites a call whose first argument is a call, down
// the chain of first arguments, as style.md §1 writes it: the innermost
// call's first argument is the subject, and each call is a stage that takes
// the value as its first argument. `Iter.to_list(Iter.map(xs, f))` becomes
// `xs |> Iter.map(f) |> Iter.to_list()`. A chain of two or more stages is
// stacked; one stage stays on its line. Only first arguments are followed:
// a call in a later argument is a value the call takes, not its subject
// (style.md, "An incidental, non-subject argument stays nested").
//
// "Convert from pipe" folds a pipeline back into the nested form: a call
// stage takes the value as its first argument, or where its `_` stands; a
// bare `try`, `dbg`, `assert` or `refute` stage prefixes the value; a bare
// `case` or `if` stage takes it as its subject or condition. A pipeline
// with a lambda stage is not converted: the nested form would have to call
// a lambda literal, which reads worse than the pipe it replaces.
//
// Neither direction changes what runs or in what order: `a |> f(b)` is
// `f(a, b)`, and in both forms `a` is evaluated before `b`.

// toPipe offers "Convert to pipe" for the call chain under the range.
func (r *refactorRequest) toPipe() (string, string, bool) {
	for n := r.innermost(isCall); n != nil; n = r.enclosingCall(n) {
		top := n
		for {
			p, ok := r.parent[top].(*ast.Call)
			if !ok || firstPositional(p) != top || r.isPipeStage(p) {
				break
			}
			top = p
		}
		if title, edited, ok := r.convertToPipe(top.(*ast.Call)); ok {
			return title, edited, true
		}
	}
	return "", "", false
}

func isCall(n ast.Node) bool {
	_, ok := n.(*ast.Call)
	return ok
}

// enclosingCall is the nearest call above n, or nil.
func (r *refactorRequest) enclosingCall(n ast.Node) ast.Node {
	for p := r.parent[n]; p != nil; p = r.parent[p] {
		if _, ok := p.(*ast.Call); ok {
			return p
		}
	}
	return nil
}

// firstPositional is a call's first argument when it is positional.
func firstPositional(c *ast.Call) ast.Node {
	if len(c.Args) == 0 {
		return nil
	}
	switch c.Args[0].(type) {
	case *ast.NamedArg, *ast.Placeholder:
		return nil
	}
	return c.Args[0]
}

func (r *refactorRequest) convertToPipe(top *ast.Call) (string, string, bool) {
	if r.isPipeStage(top) {
		// The stage's first argument in the text is not its first argument.
		return "", "", false
	}
	chain := []*ast.Call{top}
	for {
		inner, ok := firstPositional(chain[len(chain)-1]).(*ast.Call)
		if !ok {
			break
		}
		chain = append(chain, inner)
	}
	if len(chain) < 2 {
		return "", "", false
	}
	for _, c := range chain {
		if !pipeableCall(c) {
			return "", "", false
		}
	}
	// The innermost call's first argument leads, when it has one.
	stages := chain
	subject := firstPositional(chain[len(chain)-1])
	if subject == nil {
		subject = chain[len(chain)-1]
		stages = chain[:len(chain)-1]
	}
	if needsExpected(subject) {
		return "", "", false
	}
	start, end, ok := r.span(top)
	if !ok {
		return "", "", false
	}
	line, _ := nodePos(subject)
	// Two or more stages are stacked, unless the pipeline is grouped where
	// it stands: an operand reads better on one line.
	grouped := !r.standsFree(top) || r.inBareLambdaBody(top)
	stacked := len(stages) >= 2 && !grouped
	var pipe ast.Node = subject
	for i := len(stages) - 1; i >= 0; i-- {
		c := *stages[i]
		c.Args = c.Args[1:]
		c.Line = line
		if stacked {
			c.Line = line + len(stages) - i
		}
		pipe = &ast.Binary{Left: pipe, Op: "|>", Right: &c, Line: c.Line, Col: c.Col}
	}
	if grouped {
		pipe = &ast.GroupedExpr{Expr: pipe, Line: line}
	}
	return "Convert to pipe", r.replace(start, end, render(pipe, r.lineIndentOf(start))), true
}

// standsFree reports whether any expression can replace n's text without
// parentheses: n is a whole argument, item, field value, binding value,
// statement or interpolation, or the head of a pipe.
func (r *refactorRequest) standsFree(n ast.Node) bool {
	switch p := r.parent[n].(type) {
	case *ast.Call:
		return p.Func != n
	case *ast.Binary:
		return p.Op == "|>" && p.Left == n
	case *ast.NamedArg, *ast.Binding, *ast.ExprStmt, *ast.Return, *ast.ListLit, *ast.VectorLit,
		*ast.SetLit, *ast.TupleLit, *ast.MapLit, *ast.StructLit, *ast.StringInterp, *ast.GroupedExpr, *ast.Block:
		return true
	}
	return false
}

// inBareLambdaBody reports whether n stands at the top of a lambda's
// one-expression body, outside any bracket, where a `|>` would end the
// lambda: `|x| f(g(x))` piped is `|x| (x |> g() |> f())`.
func (r *refactorRequest) inBareLambdaBody(n ast.Node) bool {
	for cur := n; ; {
		p := r.parent[cur]
		switch v := p.(type) {
		case *ast.Lambda:
			return true
		case *ast.Block:
			if v.EndLine != 0 {
				return false
			}
		case *ast.Call:
			if v.Func != cur {
				return false
			}
		case *ast.Binary, *ast.Unary, *ast.TryOp, *ast.Dbg, *ast.ExprStmt, *ast.FieldAccess:
		default:
			return false
		}
		cur = p
	}
}

// pipeableCall reports whether c can become a stage: no partial
// application, and a callee a stage can name (not a bare `.Variant`).
func pipeableCall(c *ast.Call) bool {
	if _, ok := c.Func.(*ast.DotVariant); ok {
		return false
	}
	for _, a := range c.Args {
		if hasPlaceholderArg(a) {
			return false
		}
	}
	return true
}

func hasPlaceholderArg(a ast.Node) bool {
	switch v := a.(type) {
	case *ast.Placeholder:
		return true
	case *ast.NamedArg:
		_, ok := v.Value.(*ast.Placeholder)
		return ok
	}
	return false
}

// needsExpected reports whether n takes its meaning from the type expected
// of it, which a pipe's head does not always pass on.
func needsExpected(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.DotVariant:
		return true
	case *ast.Call:
		_, ok := v.Func.(*ast.DotVariant)
		return ok
	case *ast.StructLit:
		return v.TypeName == nil || isDotVariantType(v.TypeName)
	case *ast.ListLit:
		return len(v.Items) == 0 || isDotVariantType(v.TypeName)
	case *ast.MapLit:
		return len(v.Entries) == 0 || isDotVariantType(v.TypeName)
	case *ast.SetLit:
		return len(v.Items) == 0
	}
	return false
}

func isDotVariantType(te ast.TypeExpr) bool {
	_, ok := te.(*ast.DotVariantType)
	return ok
}

// fromPipe offers "Convert from pipe" for the pipeline under the range.
func (r *refactorRequest) fromPipe() (string, string, bool) {
	n := r.innermost(isPipe)
	if n == nil {
		return "", "", false
	}
	for isPipe(r.parent[n]) && r.parent[n].(*ast.Binary).Left == n {
		n = r.parent[n]
	}
	var steps []ast.Node
	cur := n
	for isPipe(cur) {
		b := cur.(*ast.Binary)
		steps = append([]ast.Node{b.Right}, steps...)
		cur = b.Left
	}
	value := cur
	for i, step := range steps {
		next, ok := applyStage(step, value, i == len(steps)-1)
		if !ok {
			return "", "", false
		}
		value = next
	}
	start, end, ok := r.span(n)
	if !ok {
		return "", "", false
	}
	if _, isCall := value.(*ast.Call); !isCall && !r.standsFree(n) {
		value = &ast.GroupedExpr{Expr: value}
	}
	return "Convert from pipe", r.replace(start, end, render(value, r.lineIndentOf(start))), true
}

// applyStage is stage applied to value, written without a pipe.
func applyStage(stage, value ast.Node, last bool) (ast.Node, bool) {
	switch s := stage.(type) {
	case *ast.Call:
		if _, ok := s.Func.(*ast.DotVariant); ok {
			return nil, false
		}
		c := *s
		at := -1
		for i, a := range s.Args {
			if !hasPlaceholderArg(a) {
				continue
			}
			if at >= 0 {
				return nil, false
			}
			at = i
		}
		if at < 0 {
			c.Args = append([]ast.Node{value}, s.Args...)
			return &c, true
		}
		c.Args = append([]ast.Node(nil), s.Args...)
		if named, ok := s.Args[at].(*ast.NamedArg); ok {
			na := *named
			na.Value = value
			c.Args[at] = &na
		} else {
			c.Args[at] = value
		}
		return &c, true
	case *ast.TryOp:
		if s.Expr != nil {
			return nil, false
		}
		t := *s
		t.Expr = value
		return &t, true
	case *ast.Dbg:
		if s.Expr != nil {
			return nil, false
		}
		d := *s
		d.Expr = value
		return &d, true
	case *ast.Assertion:
		if s.Expr != nil || !last {
			return nil, false
		}
		a := *s
		a.Expr = value
		return &a, true
	case *ast.Case:
		if s.Value != nil {
			return nil, false
		}
		c := *s
		c.Value = value
		return &c, true
	case *ast.If:
		if s.Cond != nil || s.CondPattern != nil {
			return nil, false
		}
		i := *s
		i.Cond = value
		return &i, true
	}
	return nil, false
}
