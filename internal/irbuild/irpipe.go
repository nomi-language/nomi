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
//	*ast.Then                  a call of its lambda with the piped value
//	*ast.Dbg with a nil Expr   `bl.dbgOf`, whose operand is the piped value
//
// Placeholder calls evaluate the left operand first and substitute its stable
// temporary at each hole. Copy's injected delivery handles forcing and name
// allocation. A named argument beside a hole declines.
//
// A `then` stage uses the same indirect-call path as a prefix lambda call.
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
	switch r := right.(type) {
	case *ast.Call:
		if len(placeholderSlots(r.Args)) > 0 {
			return bl.pipePlaceholderCall(t, r)
		}
		// The order is the divergence risk pipe.go names: a pipe evaluates
		// the LEFT operand first, and a lowering that appended it instead
		// would produce `f(g(), x)` for `x |> f(g())`, a well-typed program
		// that runs g() before x. `bl.call` lowers its arguments left to right and forces every
		// non-final one, so index 0 reproduces it.
		spliced := *r
		spliced.Args = make([]ast.Node, 0, len(r.Args)+1)
		spliced.Args = append(spliced.Args, t.Left)
		spliced.Args = append(spliced.Args, r.Args...)
		return bl.pipedInto(&spliced)

	case *ast.Then:
		// `x |> then |v| body` calls the lambda with the piped value. The
		// synthetic call takes the lambda's position, so it carries the
		// stage's own position rather than the pipe's.
		return bl.pipedInto(&ast.Call{Func: r.Lambda, Args: []ast.Node{t.Left},
			Line: r.Lambda.Line, Col: r.Lambda.Col})

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
			// `5 |> (dbg 1)` slips the checker's `n.Right.(*ast.Dbg)` match
			// because the pipe unwraps `GroupedExpr` first, so it would trap
			// at run time. That is a
			// front-end defect, and this producer declines rather than
			// reproducing it.
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
	}
	return no()
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

func (bl *irScalarBuilder) pipePlaceholderCall(t *ast.Binary, r *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if hasNamedArg(r.Args) {
		return no()
	}
	src, k, mobile, ok := bl.lower(t.Left)
	if !ok {
		return no()
	}
	copy := ir.NewCopy(bl.g.irNodePos(t.Left), bl.f.NewTemp(), src)
	bl.b.Append(copy)
	bl.side(copy.Dst(), irScalarSide{k: k, copy: irCopyInjected, copyPure: mobile})
	line, col := nodePos(t.Left)
	hole := &ast.Ident{Name: fmt.Sprintf("|pipe %d", copy.Dst()), Line: line, Col: col}
	bl.bound[hole.Name], bl.boundK[hole.Name] = copy.Dst(), k
	defer delete(bl.bound, hole.Name)
	defer delete(bl.boundK, hole.Name)
	spliced := *r
	spliced.Args = append([]ast.Node(nil), r.Args...)
	for i, a := range spliced.Args {
		if _, placeholder := a.(*ast.Placeholder); placeholder {
			spliced.Args[i] = hole
		}
	}
	return bl.pipedInto(&spliced)
}
