package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func (bl *irScalarBuilder) stdMethodRef(t *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	owner, ok := t.Object.(*ast.TypeIdent)
	if !ok || t.Field == nil {
		return no()
	}
	// These value categories precede methods in bareVariant. Leave their
	// construction to their own builders instead of resolving by spelling.
	if _, sym, prelude := bl.g.preludeAt(t.Field.Line, t.Field.Col); prelude && sym.Name == t.Field.Name {
		if _, _, found := variantRefAt(bl.g.fa, t.Field.Name, t.Field.Line, t.Field.Col); found {
			return no()
		}
	}
	key := owner.Name + "." + t.Field.Name
	if bl.g.stdOnces[key] != nil || (bl.g.std != nil && bl.g.std.byOnce[key] != nil) {
		return no()
	}
	f, why, _, handled := bl.g.stdMethodRefTarget(owner.Name, t.Field.Name)
	if !handled || why != "" || f == nil {
		return no()
	}
	if f.canon != nil {
		f = f.canon
	}
	if (f.rtCall == "" && f.irBody == nil) || (f.rtCall != "" && !irScalarHost(f)) {
		return no()
	}
	fk := funcKindIn(bl.g, f.params, f.result)
	if !irCallableValueKind(fk) {
		return no()
	}
	if f.rtCall != "" {
		n := ir.NewFuncValue(bl.g.irNodePos(t), bl.f.NewTemp(), bl.irHostForward(t, f))
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: fk})
		return n.Dst(), fk, true, true
	}
	n := ir.NewRefFunc(bl.g.irNodePos(t), bl.f.NewTemp(), f.irBody.Sym())
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: fk})
	return n.Dst(), fk, true, true
}

// irHostForward is the private body that forwards its parameters to an
// approved host, which is how a host becomes a function value.
func (bl *irScalarBuilder) irHostForward(t ast.Node, f *stdFunc) *ir.Func {
	at := bl.g.irNodePos(t)
	body := ir.NewFunc(at, f.key)
	entry := body.NewBlock(at, "entry")
	args := make([]ir.Temp, len(f.params))
	for i, k := range f.params {
		args[i] = bl.g.irAddParam(body, ir.NewSymbol("arg"), k)
	}
	call := ir.NewHostCall(at, body.NewTemp(), ir.OrdinaryCall, bl.g.irCalleeSym(f, f.key), args...)
	bl.g.irTypeTemp(body, call.Dst(), f.result)
	entry.Append(call)
	entry.SetTerm(ir.NewReturn(at, call.Dst()))
	return body
}
