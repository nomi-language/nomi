package irbuild

// The pipe operator in the retained shape. It adds no IR node, no IR field and
// no Go spelling.
//
// `pipe.go`'s header states the whole mechanism and this reproduces it rather
// than re-deciding it: "`x |> f()` is `f(x)` and `xs |> Iter.map(g)` is
// `Iter.map(xs, g)`: the left operand becomes the FIRST argument of the right
// operand's call. So the lowering is a desugaring — a shallow copy of the
// right-hand call with the left operand spliced in at index 0, handed to the
// ordinary call path. Nothing about a pipe reaches the built IR."
//
// `bl.call` is that ordinary call path, so the splice is the whole of the
// producer's work. Every guard a pipe would need is already in `bl.call` and
// `bl.qualCall`, and each declines with its own reason: a turbofish, a named
// argument, an omitted default, a callee outside the module's `fn` table.
//
// # Retained stages
//
//	*ast.Call                  the splice, a shallow copy of the stage
//	*ast.Then                  a call of its lambda or capture with the piped value
//	*ast.Tap                   a call of its lambda or capture with the held piped value, which it answers
//	*ast.Dbg with a nil Expr   `bl.dbgOf`, whose operand is the piped value
//
// A `then` stage uses the same indirect-call path as a prefix lambda call. A
// `tap` stage holds the piped value, calls its function with it through that
// path, drops the result and answers the held value.
//
// Try stages use retained propagation. Bare if/case stages splice the piped
// operand into the condition/scrutinee and use ordinary value-region lowering.
// The region builder excludes recorded assertion contexts, where a piped
// scrutinee has different operand-history rules from a prefix case.
//
// `x |> assert` reaches nothing: the front end rejects it as "`assert` must be
// placed at the head of the pipeline".

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// pipe lowers `left |> right` by splicing the left operand in at argument 0.
//
// The grouped-expression unwrap is load-bearing: a pipe unwraps
// `GroupedExpr` before dispatching on the shape, so `5 |> (double())` is the
// same stage as `5 |> double()`.
func (bl *irScalarBuilder) pipe(t *ast.Binary) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	right := t.Right
	for {
		grouped, isGrouped := right.(*ast.GroupedExpr)
		if !isGrouped {
			break
		}
		right = grouped.Expr
	}
	defer bl.stageAfter(t.Left, right)()
	switch r := right.(type) {
	case *ast.Call:
		// The order is the divergence risk pipe.go names: a pipe evaluates
		// the LEFT operand first, and a lowering that appended it instead
		// would produce `f(g(), x)` for `x |> f(g())`, a well-typed program
		// that runs g() before x. `bl.call` lowers its arguments left to right and forces every
		// non-final one, so index 0 reproduces it.
		spliced := *r
		spliced.Args = make([]ast.Node, 0, len(r.Args)+1)
		spliced.Args = append(spliced.Args, t.Left)
		spliced.Args = append(spliced.Args, r.Args...)
		prevFrom := bl.pipedFrom
		bl.pipedFrom = t
		defer func() { bl.pipedFrom = prevFrom }()
		return bl.pipedInto(&spliced)

	case *ast.Then:
		// `x |> then fn v -> body` calls the lambda with the piped value, and
		// `x |> then &f(&1)` the capture. The synthetic call takes the
		// function's position, so it carries the stage's own position rather
		// than the pipe's.
		line, col := nodePos(r.Fn())
		return bl.pipedInto(&ast.Call{Func: r.Fn(), Args: []ast.Node{t.Left}, Line: line, Col: col})

	case *ast.Tap:
		return bl.pipeTap(t, r)

	case *ast.TryOp:
		if r.Expr != nil {
			return no()
		}
		return bl.tryValue(r, t.Left, t)
	case *ast.If:
		if r.Cond != nil || r.CondPattern != nil {
			return no()
		}
		spliced := *r
		spliced.Cond = t.Left
		return bl.lower(&spliced)
	case *ast.Case:
		if r.Value != nil {
			return no()
		}
		spliced := *r
		spliced.Value = t.Left
		// A piped scrutinee is the piped value, handed to the case without
		// recording it as a `values:` row.
		prev := bl.pipedCase
		bl.pipedCase = &spliced
		defer func() { bl.pipedCase = prev }()
		return bl.lower(&spliced)
	case *ast.Dbg:
		if r.Expr != nil {
			// `5 |> (dbg 1)`: the checker rejects a parenthesized
			// `dbg expr` stage, so no accepted program reaches this.
			return no()
		}
		return bl.dbgOf(r, t.Left)
	case *ast.Todo:
		// `x |> todo`: the piped value is computed for its effects, then the
		// stage traps (irtodo.go).
		if _, _, _, ok := bl.lower(t.Left); !ok {
			return no()
		}
		return bl.todo(r, kindInvalid)
	case *ast.Unreachable:
		// `x |> unreachable "..."`: as `x |> todo`.
		if _, _, _, ok := bl.lower(t.Left); !ok {
			return no()
		}
		return bl.unreachable(r, kindInvalid)
	}
	return no()
}

// pipeTap lowers `x |> tap fn v -> body`: x is evaluated once and held, the
// lambda is called with it and its result dropped, and the stage answers the
// held x. The call reaches the held value through a synthesized name bound to
// its temporary.
func (bl *irScalarBuilder) pipeTap(t *ast.Binary, r *ast.Tap) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	src, k, mobile, ok := bl.lower(t.Left)
	if ok {
		src, k, ok = bl.typeOpenEmpty(t.Left, src, k)
	}
	if !ok {
		return no()
	}
	// The value is read by the lambda and by whatever consumes the stage;
	// an impure one is forced here so neither re-evaluates it.
	if !mobile {
		cp := ir.NewCopy(bl.g.irNodePos(t.Left), bl.f.NewTemp(), src)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
		src = cp.Dst()
	}
	line, col := nodePos(t.Left)
	held := &ast.Ident{Name: fmt.Sprintf("|tap %d", src), Line: line, Col: col}
	bl.bound[held.Name], bl.boundK[held.Name] = src, k
	defer delete(bl.bound, held.Name)
	defer delete(bl.boundK, held.Name)
	fnLine, fnCol := nodePos(r.Fn())
	call := &ast.Call{Func: r.Fn(), Args: []ast.Node{held}, Line: fnLine, Col: fnCol}
	res, resK, _, ok := bl.pipedInto(call)
	if !ok {
		return no()
	}
	bl.irStatementDrop(r, res, resK)
	return src, k, true, true
}

// pipedInto lowers the spliced call with `bl.pipedCall` set to it, which exists
// for one reason: a piped call records no `values:` rows inside an assertion
// subject. See `bl.call`.
func (bl *irScalarBuilder) pipedInto(spliced *ast.Call) (ir.Temp, kind, bool, bool) {
	prev := bl.pipedCall
	bl.pipedCall = spliced
	defer func() { bl.pipedCall = prev }()
	// A spliced call can be a constructor as well as a function. Use the
	// ordinary expression dispatch so Some/Ok and nominal constructors retain
	// the same represented values in prefix and pipeline spellings.
	return bl.lower(spliced)
}
