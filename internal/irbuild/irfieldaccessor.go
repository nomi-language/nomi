package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// fieldAccessor lowers the field accessor `.name` (or `.address.city`, or
// `.first`) to a closure of one parameter that reads the field chain off it and
// returns the last field. It captures nothing.
//
// The parameter's type is the one the checker read the accessor from
// (FileAnalysis.FieldAccessors); each read is the projection a field access
// `x.name` lowers to (fieldProject), or a tuple slot for an index. An
// accessor the checker did not accept has no entry and declines.
func (bl *irScalarBuilder) fieldAccessor(t *ast.FieldAccessor) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bl.g.fa == nil {
		return no()
	}
	ty, found := bl.g.srcFA().FieldAccessors[t]
	if !found {
		return no()
	}
	pk := bl.g.project(ty)
	if !irCallableValueKind(pk) {
		bl.g.dc.note("a field accessor over a type outside the domain: " + pk.nomi())
		return no()
	}
	at := bl.g.irNodePos(t)
	f := ir.NewFunc(at, t.Spelling())
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	child := &irScalarBuilder{g: bl.g, sh: sh, f: f, b: sh.entry, parent: bl, bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	// The parameter has no name in the source, and no Nomi code names it.
	cur := bl.g.irAddParam(f, ir.NewSymbol("arg0"), pk)
	k := pk
	for _, seg := range t.Path {
		if index, ordinal := ast.TupleOrdinalIndex(seg.Name); ordinal && irTupleOrNamedTuple(k) {
			var ok bool
			if cur, k, ok = child.tupleItem(seg, cur, k, index); !ok {
				return no()
			}
			continue
		}
		var ok bool
		cur, k, _, ok = child.fieldProject(seg, seg.Name, cur, k, false)
		if !ok {
			return no()
		}
	}
	if !irCallOperandKind(k) {
		// k is a field's or a tuple item's kind read off a value in the
		// domain, and a retained struct, record or tuple holds only
		// values of the domain.
		panic("fieldAccessor: " + t.Spelling() + " reads a " + k.nomi() + " off " + pk.nomi())
	}
	child.b.SetTerm(ir.NewReturn(bl.g.irNodePos(t), cur))
	bl.g.irRequireLint(f, "a field accessor graph")
	n := ir.NewFuncValue(at, bl.f.NewTemp(), f)
	bl.b.Append(n)
	fk := funcKindIn(bl.g, []kind{pk}, k)
	bl.side(n.Dst(), irScalarSide{k: fk})
	return n.Dst(), fk, true, true
}
