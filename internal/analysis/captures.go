package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// A capture is a function written as `&` and its body (ast.Capture, spec
// §5, *Captures*): `&(&1 > 2)`, `&Int.max(&1, 0)`, `&#Point{x: &1, y: 0}`.
// Each `&N` in the body is its parameter N.
//
// The function type its position expects fixes its arity and its
// parameters' types, as it gives an unannotated lambda's
// (checkLambdaExpecting): a call's parameter, an annotated binding, a field
// of function type. Each `&N` names one of those parameters; one no `&N`
// names is ignored, and one `&N` may stand several times. A partial capture
// (ast.Capture.PartialCall: `&add(1, &1)`) needs no expected type: it is
// the call, which takes its parameters from the callee. Any other capture
// with no function type expected is the error a lambda gives.

// checkCapture types n against expected and answers the function's type.
func (c *checker) checkCapture(n *ast.Capture, expected Type) Type {
	holes := n.Holes()
	for _, h := range holes {
		switch v := h.(type) {
		case *ast.PlaceholderParam:
			c.fa.recordPlaceholder(v.Line, v.Col, v.N)
		case *ast.Placeholder:
			c.fa.recordPlaceholder(v.Line, v.Col, v.N)
		}
	}
	if len(c.captures) > 0 {
		// `&f(&g(&1))`: each `&N` belongs to the one capture around it, so
		// a capture inside another would leave the reader to work out
		// which.
		c.report(errAt(n, "captures do not nest; write the inner one as `fn x -> ...`").
			WithHint(fmt.Sprintf("each `&N` belongs to the one capture around it; inside `%s`, write `%s` as a lambda",
				format.RenderNode(c.captures[len(c.captures)-1]), format.RenderNode(n))))
	}
	c.captures = append(c.captures, n)
	defer func() { c.captures = c.captures[:len(c.captures)-1] }()
	if len(holes) == 0 {
		c.report(errAt(n, "a capture uses at least `&1`").
			WithHint(fmt.Sprintf("`&` makes a function of the `&1`, `&2`, ... in its body; for a function of no arguments, write `fn -> %s`", format.RenderNode(ungroupExpr(n.Body)))))
		c.checkNode(n.Body)
		return nil
	}
	if call := n.PartialCall(); call != nil {
		return c.checkPartialCapture(n, call, len(holes), expected)
	}
	return c.checkCaptureBody(n, holes, expected)
}

// checkPartialCapture types a partial capture, whose k open arguments are
// the callee's parameters at their slots. An expected function type of
// more parameters than k gives the ones past k, which the function ignores
// (`Iter.zip_with(xs, ys, &f(&1))` is `fn a, _b -> f(a)`); one of fewer is
// the error for an `&N` past its arity.
func (c *checker) checkPartialCapture(n *ast.Capture, call *ast.Call, k int, expected Type) Type {
	ft, isFunc := resolveTypeVar(expected).(*FuncType)
	switch {
	case !isFunc:
		return c.checkNodeExpecting(call, expected)
	case len(ft.Params) < k:
		for _, h := range n.Holes() {
			if ast.HoleNumber(h) > len(ft.Params) {
				c.reportBeyondArity(n, h, len(ft.Params))
				break
			}
		}
		c.checkNode(call)
		return nil
	case len(ft.Params) > k:
		inner := &FuncType{Params: ft.Params[:k], Return: ft.Return}
		pt, ok := resolveTypeVar(c.checkNodeExpecting(call, inner)).(*FuncType)
		if !ok {
			return nil
		}
		params := append(append([]Type{}, pt.Params...), ft.Params[k:]...)
		return &FuncType{Params: params, Return: pt.Return}
	}
	// The position gives every parameter a type: the open arguments are
	// those types, as the lambda the capture stands for takes them, so a
	// callee whose slot is an interface (`Iter<Iter<U>>`) is called with
	// the concrete value (`List<List<String>>`) a direct call would pass.
	if holes := c.determinedHoles(ft); holes != nil {
		prevCall, prevHoles := c.partialCall, c.partialHoles
		c.partialCall, c.partialHoles = call, holes
		defer func() { c.partialCall, c.partialHoles = prevCall, prevHoles }()
	}
	return c.checkNodeExpecting(call, expected)
}

// determinedHoles maps each parameter number of ft to its type when every
// parameter is a type nothing is left to solve, or answers nil.
func (c *checker) determinedHoles(ft *FuncType) map[int]Type {
	holes := make(map[int]Type, len(ft.Params))
	for i, p := range ft.Params {
		if p == nil || c.openElemType(p) {
			return nil
		}
		holes[i+1] = p
	}
	return holes
}

// checkCaptureBody types a capture that is no partial: its body is checked
// with each `&N` read as parameter N (funcHoles), against the expected
// function's result.
func (c *checker) checkCaptureBody(n *ast.Capture, holes []ast.Node, expected Type) Type {
	maxN := 0
	params := make([]*ast.PlaceholderParam, 0, len(holes))
	for _, h := range holes {
		if p, ok := h.(*ast.PlaceholderParam); ok {
			params = append(params, p)
			maxN = max(maxN, p.N)
		}
	}
	ft, isFunc := resolveTypeVar(expected).(*FuncType)
	var paramTys []Type
	var ret Type
	unknown := false
	errsBefore := len(c.errors)
	if c.funcHoles == nil {
		c.funcHoles = map[*ast.PlaceholderParam]Type{}
	}
	if isFunc {
		// A callee type parameter nothing has solved yet (`U` in
		// `Iter.map(xs, &(&1 * 2))`) is solved by the body, as for a
		// lambda.
		open := map[*TypeParam_]Type{}
		paramTys = make([]Type, len(ft.Params))
		for i, p := range ft.Params {
			paramTys[i] = c.openUnsolvedCalleeParams(p, open)
		}
		ret = Substitute(ft.Return, open)
		reported := map[int]bool{}
		for _, h := range params {
			if h.N <= len(paramTys) {
				c.funcHoles[h] = paramTys[h.N-1]
				continue
			}
			c.funcHoles[h] = nil
			if !reported[h.N] {
				reported[h.N] = true
				c.reportBeyondArity(n, h, len(paramTys))
			}
		}
	} else {
		switch resolveTypeVar(expected).(type) {
		case nil, *TypeVar, *TypeParam_:
			unknown = true
		}
		paramTys = make([]Type, maxN)
		for _, h := range params {
			c.funcHoles[h] = nil
		}
	}
	body := c.checkNodeExpecting(n.Body, lambdaTailExpected(ret))
	if unknown {
		c.reportUninferredHoles(n, params, body)
		// The parameters have no type, so neither has the function: a use
		// of it draws no follow-on mismatch.
		return nil
	}
	if !isFunc {
		// A type expected here that is not a function's (`n: Int =
		// &(&1 + 1)`) is the mismatch the position reports, as for a
		// lambda.
		return &FuncType{Params: paramTys, Return: body}
	}
	if body == nil {
		if len(c.errors) > errsBefore {
			return nil
		}
		body = TypeUnit
	}
	if ft.Return != nil && ContainsTypeParam(body) {
		if _, bodyIsTP := body.(*TypeParam_); !bodyIsTP {
			subs := map[*TypeParam_]Type{}
			_ = c.unifyInto(ft.Return, body, subs)
			if len(subs) > 0 {
				body = Substitute(body, subs)
			}
		}
	}
	return &FuncType{Params: paramTys, Return: body}
}

// reportBeyondArity is the error for an `&N` past the arity of the
// function expected where the capture n stands: "`Iter.map` passes one
// argument; `&2` names a second".
func (c *checker) reportBeyondArity(n *ast.Capture, h ast.Node, arity int) {
	takes := "the function expected here takes " + countWord(arity, "argument")
	if call, ok := c.argumentHolder(n).(*ast.Call); ok {
		if callee := calleeText(call.Func); callee != "" {
			takes = "`" + callee + "` passes " + countWord(arity, "argument")
		}
	}
	num := ast.HoleNumber(h)
	c.report(errAt(h, fmt.Sprintf("%s; `&%d` names a %s", takes, num, ordinalWord(num))).
		WithHint("`&N` is the capture's parameter N, and the function expected where the capture stands fixes how many it has"))
}

// countWord is n things in words: "no argument", "one argument", "2
// arguments".
func countWord(n int, thing string) string {
	switch n {
	case 0:
		return "no " + thing
	case 1:
		return "one " + thing
	case 2:
		return "two " + thing + "s"
	case 3:
		return "three " + thing + "s"
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// ordinalWord spells n as an ordinal: first, second, ..., 11th.
func ordinalWord(n int) string {
	words := []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth", "ninth", "tenth"}
	if n >= 1 && n <= len(words) {
		return words[n-1]
	}
	return fmt.Sprintf("%dth", n)
}

// checkPlaceholderParam is the type of `&N`: the parameter of the capture
// around it it stands for. One outside every capture is an error, whose
// help writes the capture the `&N` most likely belongs to.
func (c *checker) checkPlaceholderParam(n *ast.PlaceholderParam) Type {
	ty, ok := c.funcHoles[n]
	if ok {
		return ty
	}
	stray, known := c.strayPlaceholders()[n]
	if !known || stray.reported {
		// Inside a capture this check did not reach, or already reported
		// with another `&N` of the same expression.
		return nil
	}
	for _, other := range c.strayPlaceholders() {
		if other.at == stray.at {
			other.reported = true
		}
	}
	c.report(errAt(n, fmt.Sprintf("`&%d` is a capture's parameter: write the function as `&(...)` or `&f(...)`", n.N)).WithHint(stray.hint))
	return nil
}

// strayPlaceholder is an `&N` outside every capture, with the help that
// writes the capture it most likely belongs to.
type strayPlaceholder struct {
	// at is the expression the help makes the capture's body.
	at       ast.Node
	hint     string
	reported bool
}

// strayPlaceholders is every `&N` of the file outside a capture, found once
// by walking the file's syntax.
func (c *checker) strayPlaceholders() map[*ast.PlaceholderParam]*strayPlaceholder {
	if c.strays != nil {
		return c.strays
	}
	c.strays = map[*ast.PlaceholderParam]*strayPlaceholder{}
	var walk func(n ast.Node, parents []ast.Node, inCapture bool)
	walk = func(n ast.Node, parents []ast.Node, inCapture bool) {
		switch v := n.(type) {
		case *ast.Capture:
			inCapture = true
		case *ast.Lambda:
			inCapture = false
		case *ast.PlaceholderParam:
			if !inCapture {
				c.strays[v] = strayHelp(v, parents)
			}
			return
		}
		ps := append(parents, n)
		ast.Children(n, func(child ast.Node) { walk(child, ps, inCapture) })
	}
	for _, n := range c.fileNodes {
		walk(n, nil, false)
	}
	return c.strays
}

// strayHelp is the help for the `&N` p outside a capture: the expression
// around it written as a capture. A call taking p as an argument is the
// capture's body (`&Int.max(&1, 0)`), unless it is a pipe's stage, which
// the pipe fills at its first argument (`|> then &divide(100, &1)`). An
// operator, range, field read or parentheses around p are part of the body
// (`&(&1 * 2 + 1)`), and so is a literal p is a field or element of
// (`&#Point{x: &1, y: 0}`).
func strayHelp(p *ast.PlaceholderParam, parents []ast.Node) *strayPlaceholder {
	var cur ast.Node = p
climb:
	for i := len(parents) - 1; i >= 0; i-- {
		switch v := parents[i].(type) {
		case *ast.NamedArg:
			cur = v
			continue
		case *ast.Call:
			if na, named := cur.(*ast.NamedArg); v.Func == cur || cur != ast.Node(p) && !(named && na.Value == ast.Node(p)) {
				// A callee, or an argument p is deeper in: the argument is
				// the body (`&(&1 * 2)`).
				break climb
			}
			if i > 0 {
				if pipe, ok := parents[i-1].(*ast.Binary); ok && pipe.Op == "|>" && pipe.Right == ast.Node(v) {
					return &strayPlaceholder{at: v, hint: fmt.Sprintf("a pipe fills the first argument; write `|> then &%s`", format.RenderNode(v))}
				}
			}
			return &strayPlaceholder{at: v, hint: fmt.Sprintf("write `&%s`", format.RenderNode(v))}
		case *ast.Binary:
			if v.Op == "and" || v.Op == "or" || v.Op == "|>" {
				break climb
			}
			cur = v
			continue
		case *ast.Unary, *ast.RangeLit, *ast.GroupedExpr:
			cur = v
			continue
		case *ast.FieldAccess:
			if v.Object == cur {
				cur = v
				continue
			}
		case *ast.StructLit, *ast.TupleLit, *ast.ListLit, *ast.VectorLit, *ast.SetLit, *ast.MapLit:
			return &strayPlaceholder{at: v, hint: fmt.Sprintf("write `&%s`", format.RenderNode(v))}
		}
		break climb
	}
	if cur == ast.Node(p) {
		return &strayPlaceholder{at: p, hint: "a capture is `&` and the function's body, as in `&(&1 > 2)` or `&Int.max(&1, 0)`; `&N` alone is no function, so write a lambda"}
	}
	if g, ok := cur.(*ast.GroupedExpr); ok {
		return &strayPlaceholder{at: cur, hint: fmt.Sprintf("write `&%s`", format.RenderNode(g))}
	}
	return &strayPlaceholder{at: cur, hint: fmt.Sprintf("write `&(%s)`", format.RenderNode(cur))}
}

// checkDiscardRead reports `_` read as a value. `_` is a discard name; a
// capture's parameter is written `&N`. When the `_` is an argument of a
// call (as a partial application wrote it), the help writes the call as a
// capture with each of its `_`s numbered, once, at the first.
func (c *checker) checkDiscardRead(n *ast.Ident) Type {
	if c.discardsReported[n] {
		return nil
	}
	hint := "a capture's parameter is written `&1`, as in `Iter.map(xs, &(&1 * 2))`"
	if call := c.discardHolder(n); call != nil {
		var holes []ast.Node
		for _, a := range call.Args {
			if na, named := a.(*ast.NamedArg); named {
				a = na.Value
			}
			if id, ok := a.(*ast.Ident); ok && id.Name == "_" {
				holes = append(holes, id)
			}
		}
		stage := c.isPipeStage(call)
		names := make([]string, len(holes))
		for i := range holes {
			names[i] = fmt.Sprintf("&%d", i+1)
			if stage {
				names[i] = "&1"
			}
			if id := holes[i].(*ast.Ident); id != n {
				if c.discardsReported == nil {
					c.discardsReported = map[*ast.Ident]bool{}
				}
				c.discardsReported[id] = true
			}
		}
		written := "&" + renderWithHoles(call, holes, names)
		switch {
		case stage:
			hint = "a pipe fills the first argument; write `|> then " + written + "`"
		case len(holes) == 1:
			hint = "a capture's parameter is written `&1`: `" + written + "`"
		default:
			hint = "number the capture's parameters `&1`, `&2`, ... in the order they are written: `" + written + "`"
		}
	}
	c.report(errAt(n, "`_` is a discard name, not a value").WithHint(hint))
	return nil
}

// isPipeStage reports whether call is the stage of a pipe the checker is
// inside (exprStack).
func (c *checker) isPipeStage(call *ast.Call) bool {
	for _, n := range c.exprStack {
		if b, ok := n.(*ast.Binary); ok && b.Op == "|>" && b.Right == ast.Node(call) {
			return true
		}
	}
	return false
}

// discardHolder is the call whose argument the `_` n is, when the checker
// reached n through it (exprStack).
func (c *checker) discardHolder(n *ast.Ident) *ast.Call {
	for i := len(c.exprStack) - 1; i >= 0; i-- {
		switch p := c.exprStack[i].(type) {
		case *ast.Ident:
			if p == n {
				continue
			}
			return nil
		case *ast.NamedArg:
			continue
		case *ast.Call:
			if ownsArg(p, n) {
				return p
			}
			return nil
		case *ast.Binary:
			// A pipe checks its stage's arguments itself.
			if call, ok := p.Right.(*ast.Call); ok && p.Op == "|>" && ownsArg(call, n) {
				return call
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

// ownsArg reports whether n is one of call's arguments, positional or
// named.
func ownsArg(call *ast.Call, n ast.Node) bool {
	for _, a := range call.Args {
		if na, named := a.(*ast.NamedArg); named {
			a = na.Value
		}
		if a == n {
			return true
		}
	}
	return false
}

// captureParams is the parameter list of the function n makes, by which a
// call of it bound to a name may name its arguments: a struct literal whose
// `&N`s are its whole fields, each once, takes the fields' names (`mk(x:
// 3)` after `mk: (Int) -> Point = &#Point{x: &1, y: 0}`). Any other
// capture that is no partial has no names to be called by. A field's
// default is the literal's, which fills a field the literal leaves out, so
// no parameter has one: a call passes every argument.
func captureParams(n *ast.Capture) []ast.Param {
	lit, ok := n.Body.(*ast.StructLit)
	holes := n.Holes()
	if !ok || len(holes) == 0 {
		return nil
	}
	params := make([]ast.Param, len(holes))
	seen := 0
	for _, f := range lit.Fields {
		if ph, open := f.Value.(*ast.PlaceholderParam); open {
			if ph.N > len(params) || params[ph.N-1].Name != "" {
				return nil
			}
			params[ph.N-1] = ast.Param{Name: f.Name, Line: ph.Line, Col: ph.Col}
			seen++
		}
	}
	if seen != len(holes) {
		// An `&N` deeper in a field (`&#Point{x: &1 + 1, y: &2}`), or one
		// standing twice, has no name, so neither has any.
		return nil
	}
	return params
}

// CaptureParams is captureParams, for the IR builder.
func CaptureParams(n *ast.Capture) []ast.Param { return captureParams(n) }

// numberedNames names a lambda's parameters for holes inside scope: one
// name per parameter number up to the highest, and for each hole the name
// of the parameter it is.
func numberedNames(scope ast.Node, holes []ast.Node) (params, perHole []string) {
	maxN := 0
	for _, h := range holes {
		maxN = max(maxN, ast.HoleNumber(h))
	}
	params = holeNames(scope, maxN)
	perHole = make([]string, len(holes))
	for i, h := range holes {
		if n := ast.HoleNumber(h); n >= 1 && n <= len(params) {
			perHole[i] = params[n-1]
		}
	}
	return params, perHole
}

// reportUninferredHoles is the error for a capture nothing gives a type to
// (`f = &(&1 > 2)`), at its first `&N`. It names the function type to
// annotate the binding with, as far as the body says it, and the lambda to
// write instead. A capture that is a call's argument (`Some(&(&1 + 1))`)
// says instead how to make the call the capture.
func (c *checker) reportUninferredHoles(n *ast.Capture, holes []*ast.PlaceholderParam, body Type) {
	if len(holes) == 0 {
		return
	}
	nodes := holesOf(holes)
	params, perHole := numberedNames(n.Body, nodes)
	paramTys := make([]string, len(params))
	for i := range paramTys {
		paramTys[i] = "T"
		if len(params) > 1 {
			paramTys[i] = fmt.Sprintf("T%d", i+1)
		}
	}
	for _, h := range holes {
		// A struct literal's field or a call's parameter says what it is.
		if t := c.fa.ExpectedTypes[h]; t != nil && !containsTypeVar(t) && !ContainsTypeParam(t) {
			paramTys[h.N-1] = c.typef("%s", t)
		}
	}
	inner := ungroupExpr(n.Body)
	if b, ok := inner.(*ast.Binary); ok && len(holes) == 1 {
		// `&1 > 2`: the other operand's type is the parameter's.
		if other := b.Right; b.Left == ast.Node(holes[0]) || b.Right == ast.Node(holes[0]) {
			if other == ast.Node(holes[0]) {
				other = b.Left
			}
			if t := c.fa.ExprTypes[other]; t != nil && !containsTypeVar(t) && !ContainsTypeParam(t) {
				paramTys[holes[0].N-1] = c.typef("%s", t)
			}
		}
	}
	result := "R"
	if body != nil && !containsTypeVar(body) && !ContainsTypeParam(body) && !(paramTys[0] == "T" && isArithmeticOp(inner)) {
		result = c.typef("%s", body)
	}
	src := format.RenderNode(n)
	annotated := make([]string, len(params))
	for i, name := range params {
		annotated[i] = name + ": " + paramTys[i]
	}
	lambda := "fn " + strings.Join(annotated, ", ") + " -> " + renderWithHoles(inner, nodes, perHole)
	e := errAt(holes[0], "cannot infer type for `&"+fmt.Sprint(holes[0].N)+"` in `"+src+"`")
	if outer, ok := c.argumentHolder(n).(*ast.Call); ok && constructorCallee(outer.Func) {
		// `Some(&(&1 + 1))`: a function of the whole construction is the
		// likelier intent.
		e = e.WithHint(fmt.Sprintf("for a function of the whole call, write `&%s`", format.RenderNode(replaceCapture(outer, n))))
	} else {
		e = e.WithHint(fmt.Sprintf("annotate the binding with the function's type, as in `f: (%s) -> %s = %s`, or write a lambda with an annotated parameter, as in `%s`; a later use of the function does not give it one",
			strings.Join(paramTys, ", "), result, src, lambda))
	}
	c.report(e)
}

// constructorCallee reports whether a call's callee names a variant or a
// type by its capitalized name (`Some`, `Shape.Circle`, `.Circle`), whose
// call wraps its arguments in a value.
func constructorCallee(callee ast.Node) bool {
	var name string
	switch v := callee.(type) {
	case *ast.TypeIdent:
		name = v.Name
	case *ast.Ident:
		name = v.Name
	case *ast.DotVariant:
		name = v.Name
	case *ast.FieldAccess:
		if v.Field != nil {
			name = v.Field.Name
		}
	}
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// replaceCapture is a copy of call with the argument capture written as
// its body, so `Some(&(&1 + 1))` reads `Some(&1 + 1)`.
func replaceCapture(call *ast.Call, capture *ast.Capture) ast.Node {
	cp := *call
	cp.Args = make([]ast.Node, len(call.Args))
	for i, a := range call.Args {
		switch v := a.(type) {
		case *ast.Capture:
			if v == capture {
				a = ungroupExpr(v.Body)
			}
		case *ast.NamedArg:
			if v.Value == ast.Node(capture) {
				na := *v
				na.Value = ungroupExpr(capture.Body)
				a = &na
			}
		}
		cp.Args[i] = a
	}
	return &cp
}

// argumentHolder is the call or literal n is an argument or element of,
// when the checker reached n through it (exprStack), or nil.
func (c *checker) argumentHolder(n ast.Node) ast.Node {
	for i := len(c.exprStack) - 1; i >= 0; i-- {
		switch p := c.exprStack[i].(type) {
		case *ast.Capture:
			if p == n {
				continue
			}
			return nil
		case *ast.NamedArg, *ast.GroupedExpr:
			continue
		case *ast.Call:
			for _, a := range p.Args {
				if na, named := a.(*ast.NamedArg); named {
					a = na.Value
				}
				if ungroupExpr(a) == n {
					return p
				}
			}
			return nil
		case *ast.Binary:
			// A pipe checks its stage's arguments itself.
			if call, ok := p.Right.(*ast.Call); ok && p.Op == "|>" {
				for _, a := range call.Args {
					if na, named := a.(*ast.NamedArg); named {
						a = na.Value
					}
					if ungroupExpr(a) == n {
						return call
					}
				}
			}
			return nil
		case *ast.StructLit:
			for _, f := range p.Fields {
				if ungroupExpr(f.Value) == n {
					return p
				}
			}
			return nil
		case *ast.TupleLit, *ast.ListLit, *ast.VectorLit, *ast.SetLit:
			var items []ast.Node
			switch l := p.(type) {
			case *ast.TupleLit:
				items = l.Items
			case *ast.ListLit:
				items = l.Items
			case *ast.VectorLit:
				items = l.Items
			case *ast.SetLit:
				items = l.Items
			}
			for _, item := range items {
				if ungroupExpr(item) == n {
					return p
				}
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

// isArithmeticOp reports whether n is an arithmetic operator, whose result
// type is its operands' only for the stdlib's own impls.
func isArithmeticOp(n ast.Node) bool {
	b, ok := n.(*ast.Binary)
	if !ok {
		return false
	}
	switch b.Op {
	case "+", "-", "*", "/", "%":
		return true
	}
	return false
}

// holesOf is holes as nodes.
func holesOf(holes []*ast.PlaceholderParam) []ast.Node {
	out := make([]ast.Node, len(holes))
	for i, h := range holes {
		out[i] = h
	}
	return out
}

// holeNames is a parameter name for each of count `&N`s in n, none of them
// a name n reads: `x` for one, `a`, `b`, ... for several.
func holeNames(n ast.Node, count int) []string {
	used := map[string]bool{}
	ast.Inspect(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok {
			used[id.Name] = true
		}
		return true
	})
	pool := []string{"x", "y", "z", "v", "w"}
	if count > 1 {
		pool = []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	}
	var out []string
	for _, name := range pool {
		if len(out) == count {
			break
		}
		if !used[name] {
			out = append(out, name)
		}
	}
	for i := 1; len(out) < count; i++ {
		if name := fmt.Sprintf("x%d", i); !used[name] {
			out = append(out, name)
		}
	}
	return out
}

// renderWithHoles is n as the formatter writes it, with each of holes (a
// Placeholder, a PlaceholderParam or a `_`) written as the name at its
// index.
func renderWithHoles(n ast.Node, holes []ast.Node, names []string) string {
	byHole := make(map[ast.Node]string, len(holes))
	for i, h := range holes {
		if i < len(names) {
			byHole[h] = names[i]
		}
	}
	return format.RenderNode(replaceHoles(n, byHole))
}

// replaceHoles is a copy of n with each hole in names written as its name.
// It copies only the nodes on the way to one.
func replaceHoles(n ast.Node, names map[ast.Node]string) ast.Node {
	switch v := n.(type) {
	case *ast.Placeholder:
		if name, ok := names[v]; ok {
			return &ast.Ident{Name: name, Line: v.Line, Col: v.Col}
		}
	case *ast.PlaceholderParam:
		if name, ok := names[v]; ok {
			return &ast.Ident{Name: name, Line: v.Line, Col: v.Col}
		}
	case *ast.Ident:
		if name, ok := names[v]; ok {
			return &ast.Ident{Name: name, Line: v.Line, Col: v.Col}
		}
	case *ast.GroupedExpr:
		cp := *v
		cp.Expr = replaceHoles(v.Expr, names)
		return &cp
	case *ast.Binary:
		cp := *v
		cp.Left, cp.Right = replaceHoles(v.Left, names), replaceHoles(v.Right, names)
		return &cp
	case *ast.Unary:
		cp := *v
		cp.Right = replaceHoles(v.Right, names)
		return &cp
	case *ast.RangeLit:
		cp := *v
		cp.Start, cp.End = replaceHoles(v.Start, names), replaceHoles(v.End, names)
		return &cp
	case *ast.FieldAccess:
		cp := *v
		cp.Object = replaceHoles(v.Object, names)
		return &cp
	case *ast.StructLit:
		cp := *v
		cp.Fields = make([]ast.StructFieldVal, len(v.Fields))
		for i, f := range v.Fields {
			f.Value = replaceHoles(f.Value, names)
			cp.Fields[i] = f
		}
		return &cp
	case *ast.TupleLit:
		cp := *v
		cp.Items = make([]ast.Node, len(v.Items))
		for i, item := range v.Items {
			cp.Items[i] = replaceHoles(item, names)
		}
		return &cp
	case *ast.ListLit:
		cp := *v
		cp.Items = make([]ast.Node, len(v.Items))
		for i, item := range v.Items {
			cp.Items[i] = replaceHoles(item, names)
		}
		return &cp
	case *ast.NamedArg:
		cp := *v
		cp.Value = replaceHoles(v.Value, names)
		return &cp
	case *ast.Call:
		cp := *v
		cp.Args = make([]ast.Node, len(v.Args))
		for i, a := range v.Args {
			cp.Args[i] = replaceHoles(a, names)
		}
		return &cp
	}
	return n
}

// recordPlaceholder notes the `&n` at line and col for the semantic tokens
// (FileAnalysis.Placeholders).
func (fa *FileAnalysis) recordPlaceholder(line, col, n int) {
	if fa == nil || line <= 0 || IsSynthesizedLine(line) {
		return
	}
	if fa.Placeholders == nil {
		fa.Placeholders = map[Pos]int{}
	}
	fa.Placeholders[Pos{Line: line, Col: col}] = 1 + len(fmt.Sprint(n))
}

// reportUninferredOperand is the error for a partial operator call whose
// open operand hole nothing gives a type to (`inv = &Int.divide(12, &1)`):
// no function type is expected where the capture stands, and the left
// operand's type (left, nil when it is the open one) has no impl of the
// operator or several (cands), so the right operand could be any of their
// right-hand types. Only the capture whose call n is reports it; an `&N`
// outside a capture has its own error.
func (c *checker) reportUninferredOperand(n *ast.Call, opIface operatorInterface, hole *ast.Placeholder, left Type, cands []implCandidate) {
	if len(c.captures) == 0 || c.captures[len(c.captures)-1].PartialCall() != n {
		return
	}
	capture := c.captures[len(c.captures)-1]
	src := format.RenderNode(capture)
	holes := capture.Holes()
	params, perHole := numberedNames(capture.Body, holes)
	paramTys := make([]string, len(params))
	for i := range paramTys {
		paramTys[i] = "T"
		if len(params) > 1 {
			paramTys[i] = fmt.Sprintf("T%d", i+1)
		}
	}
	if ph, isOpen := n.Args[0].(*ast.Placeholder); isOpen && left != nil && ph.N >= 1 && ph.N <= len(paramTys) {
		paramTys[ph.N-1] = c.typef("%s", left)
	}
	result := "R"
	var rights []string
	for _, cand := range cands {
		rights = append(rights, "`"+c.typef("%s", cand.args[0])+"`")
	}
	if len(cands) > 1 {
		if hole.N >= 1 && hole.N <= len(paramTys) {
			paramTys[hole.N-1] = c.typef("%s", cands[0].args[0])
		}
		result = c.typef("%s", cands[0].args[1])
	}
	annotated := make([]string, len(params))
	for i, name := range params {
		annotated[i] = name + ": " + paramTys[i]
	}
	lambda := "fn " + strings.Join(annotated, ", ") + " -> " + renderWithHoles(ungroupExpr(capture.Body), holes, perHole)
	hint := fmt.Sprintf("annotate the binding with the function's type, as in `f: (%s) -> %s = %s`, or write a lambda with an annotated parameter, as in `%s`",
		strings.Join(paramTys, ", "), result, src, lambda)
	if len(cands) > 1 {
		hint = fmt.Sprintf("`%s` has %d `%s` impls, whose right operands are %s, so only the function expected where the capture stands can pick one; %s",
			c.typef("%s", left), len(cands), opIface.Interface, joinAnd(rights), hint)
	}
	c.report(errAt(hole, "cannot infer type for `&"+fmt.Sprint(hole.N)+"` in `"+src+"`").WithHint(hint))
}
