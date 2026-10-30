package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A postfix `!`, `expr!`: the expression, whose value is a Result or a
// Maybe, then a test of its tag. The success edge projects the Ok's or the
// Some's payload, which is the `!`'s value; the success path costs the test
// and nothing more. The failure edge traps (rt's unwrap.go has the texts),
// naming the expression as the program wrote it:
//
//   - on Err(e), `vm.bang_failed` with the expression's text and e's text,
//     which a function built once per error kind renders, through Display
//     when the kind implements it and Debug otherwise (irErrorRenderFunc, as
//     a `main` that returns Err renders its error);
//   - on None, `vm.bang_none` with the expression's text.

const (
	irBangFailedHost = "vm.bang_failed"
	irBangNoneHost   = "vm.bang_none"
)

// irBangRenderKey is the identity of the function that renders a failed
// `!`'s error of one kind.
type irBangRenderKey struct{ k kind }

// unwrap lowers `expr!`.
func (bl *irScalarBuilder) unwrap(t *ast.Unwrap) (ir.Temp, kind, bool, bool) {
	src, k, pure, ok := bl.lower(t.Expr)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.unwrapValue(t, analysis.UnwrapSource(t.Expr), src, k, pure)
}

// unwrapStage lowers a pipe whose stage is unwrapped, `x |> f()!`: the pipe
// `x |> f()`, then its result unwrapped.
func (bl *irScalarBuilder) unwrapStage(t *ast.Binary, stage *ast.Unwrap) (ir.Temp, kind, bool, bool) {
	plain := *t
	plain.Right = stage.Expr
	src, k, pure, ok := bl.pipe(&plain)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.unwrapValue(stage, analysis.UnwrapSource(&plain), src, k, pure)
}

// unwrapValue tests src, the value of the expression whose text is source,
// and answers its payload, trapping at t's line on Err or None.
func (bl *irScalarBuilder) unwrapValue(t *ast.Unwrap, source string, src ir.Temp, k kind, pure bool) (ir.Temp, kind, bool, bool) {
	no := func(why string) (ir.Temp, kind, bool, bool) {
		irDeclineNote(why)
		return ir.NoTemp, kindInvalid, false, false
	}
	d := k.def
	if d == nil || !irRetainedEnumKind(d) || d.preludeOf == nil || !d.preludeOf.spec.tryOperand || len(d.variants) != 2 || len(d.variants[0].payloads) != 1 {
		return no("a `!` over a value that is not a retained Result or Maybe: " + k.nomi())
	}
	pos := bl.g.irPos(t.Line, t.Col)
	if !pure {
		cp := ir.NewCopy(pos, bl.f.NewTemp(), src)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
		src = cp.Dst()
	}
	v, u := &d.variants[0], &d.variants[1]
	match := bl.variantTest(t, src, d, v)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	succeeded := bl.f.NewBlock(pos, "`!` succeeded")
	failed := bl.f.NewBlock(pos, "`!` failed")
	bl.b.SetTerm(ir.NewBranch(pos, match.Dst(), succeeded.ID(), failed.ID()))

	bl.b = failed
	text := ir.NewString(pos, bl.f.NewTemp(), source)
	bl.b.Append(text)
	bl.side(text.Dst(), irScalarSide{k: kindString})
	var trap *ir.Call
	switch len(u.payloads) {
	case 1:
		errK := u.payloads[0].k
		e := ir.NewProjPayload(pos, bl.f.NewTemp(), src, bl.g.irTypeSym(d), u.nomi, 0, irParamShape(errK))
		bl.b.Append(e)
		bl.side(e.Dst(), irScalarSide{k: errK})
		shown := e.Dst()
		if errK != kindString {
			render := bl.g.bangRender(t, errK)
			if render == nil {
				return no("a `!` whose error has no rendering: " + errK.nomi())
			}
			r := ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, render, e.Dst())
			bl.b.Append(r)
			bl.side(r.Dst(), irScalarSide{k: kindString})
			shown = r.Dst()
		}
		trap = ir.NewHostCall(pos, bl.f.NewTemp(), ir.OrdinaryCall,
			bl.g.irCalleeSym(irBangFailedHost, irBangFailedHost), text.Dst(), shown)
	case 0:
		trap = ir.NewHostCall(pos, bl.f.NewTemp(), ir.OrdinaryCall,
			bl.g.irCalleeSym(irBangNoneHost, irBangNoneHost), text.Dst())
	default:
		return no("a `!` whose failure carries several values")
	}
	bl.b.Append(trap)
	bl.side(trap.Dst(), irScalarSide{k: kindUnit})
	// The trap never returns; the edge joins the success block only to
	// terminate.
	bl.b.SetTerm(ir.NewJump(pos, succeeded.ID()))

	bl.b = succeeded
	part := v.payloads[0]
	pr := ir.NewProjPayload(pos, bl.f.NewTemp(), src, bl.g.irTypeSym(d), v.nomi, 0, irParamShape(part.k))
	bl.b.Append(pr)
	bl.side(pr.Dst(), irScalarSide{k: part.k})
	return pr.Dst(), part.k, true, true
}

// bangRender is the function that renders a failed `!`'s error of kind
// errK, built once per kind in this unit, or nil when errK has no rendering.
func (g *gen) bangRender(at ast.Node, errK kind) *ir.Symbol {
	if sym, built := g.bangRenders[errK]; built {
		return sym
	}
	var sym *ir.Symbol
	if irCallOperandKind(errK) {
		irDeclineAside(func() {
			name := "`!` failure " + errK.nomi()
			s := g.irCalleeSym(irBangRenderKey{errK}, name)
			f, ok := g.irErrorRenderFunc(at, name, s, errK, at, false)
			if !ok {
				f, ok = g.irErrorRenderFunc(at, name, s, errK, at, true)
			}
			if !ok {
				return
			}
			g.irModule().AddFunc(f)
			if err := g.irLintAdded(); err != nil {
				panic("irbuild: `!` failure: " + err.Error())
			}
			sym = s
		})
	}
	if g.bangRenders == nil {
		g.bangRenders = map[kind]*ir.Symbol{}
	}
	g.bangRenders[errK] = sym
	return sym
}
