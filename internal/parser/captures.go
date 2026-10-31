package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// parseCapture parses a capture (spec §5, *Captures*): `&` written against
// the function's body, which is one postfix-level operand: a call (`&Int.max(&1, 0)`), a parenthesized expression
// (`&(&1 > 2)`) or a literal (`&#Point{x: &1, y: 0}`, `&#(&1, 0)`, `&[&1]`,
// `&%{&1 => 0}`). So `&f(&1) + 1` adds 1 to a function, and the body of a
// function of the whole sum is parenthesized: `&(f(&1) + 1)`.
//
// Each `&N` in the body is the capture's parameter N. A capture whose body
// is a call taking each of its `&N` as one of its own arguments, numbered
// `&1`, `&2`, ... in the order they are written, each once, is a partial
// application, which takes the callee's parameter types, names and
// defaults: its `&N`s are written as the call's open arguments
// (ast.Placeholder), which the checker and the builder read as a partial.
func (p *Parser) parseCapture() (ast.Node, error) {
	amp := p.peek()
	p.advance() // consume CAPTURE
	start := p.pos
	body, err := p.parseExpr(tryOperandPrecedence)
	if err != nil {
		return nil, err
	}
	p.recordSpan(body, start)
	if !captureBody(body) {
		return nil, errorAt(amp.Line, amp.Col, "a capture is `&` and a call, a parenthesized expression or a literal, as in `&f(&1)` or `&(&1 > 2)`")
	}
	openPartialArgs(body)
	return &ast.Capture{Body: body, Line: amp.Line, Col: amp.Col}, nil
}

// captureBody reports whether n can be a capture's body: a call, a
// parenthesized expression or a literal.
func captureBody(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.Call, *ast.GroupedExpr, *ast.StructLit, *ast.TupleLit, *ast.ListLit,
		*ast.ListSpreadLit, *ast.VectorLit, *ast.SetLit, *ast.MapLit:
		return true
	case *ast.Unwrap:
		// `&Vector.get(v, &1)!`: the body unwraps a call's result, so its
		// `&1` is the capture's parameter, not a partial application's.
		return captureBody(v.Expr)
	case *ast.TryOp:
		// `&parse(&1)?`: the body propagates a call's failure out of the
		// capture's function, as a `?` in a lambda's body does.
		return v.Expr != nil && captureBody(v.Expr)
	}
	return false
}

// openPartialArgs writes the `&N`s of a partial capture's call as its open
// arguments (ast.Placeholder): each of the body's `&N` must be one of the
// call's own arguments, positional or named, numbered from 1 in the order
// they are written, each once.
func openPartialArgs(body ast.Node) {
	call, ok := body.(*ast.Call)
	if !ok {
		return
	}
	holes := (&ast.Capture{Body: body}).Holes()
	if len(holes) == 0 {
		return
	}
	own := map[ast.Node]int{}
	for i, a := range call.Args {
		if na, named := a.(*ast.NamedArg); named {
			a = na.Value
		}
		if _, ok := a.(*ast.PlaceholderParam); ok {
			own[a] = i
		}
	}
	for i, h := range holes {
		if _, isOwn := own[h]; !isOwn || ast.HoleNumber(h) != i+1 {
			return
		}
	}
	for i, a := range call.Args {
		target := &call.Args[i]
		if na, named := a.(*ast.NamedArg); named {
			target = &na.Value
		}
		if ph, ok := (*target).(*ast.PlaceholderParam); ok {
			*target = &ast.Placeholder{TriviaCarrier: ph.TriviaCarrier, SpanCarrier: ph.SpanCarrier, N: ph.N, Line: ph.Line, Col: ph.Col}
		}
	}
}
