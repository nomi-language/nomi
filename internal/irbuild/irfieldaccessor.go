package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// fieldAccessor lowers the field accessor `.name` (or `.address.city`, or
// `.0`) to a closure of one parameter that reads the field chain off it and
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
	ty, found := bl.g.fa.FieldAccessors[t]
	if !found {
		return no()
	}
	pk := bl.g.project(ty)
	if !irCallableValueKind(pk) {
		irDeclineNote("a field accessor over a type outside the domain: " + pk.nomi())
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
		if index, err := strconv.Atoi(seg.Name); err == nil {
			if !irRetainedTupleKind(k) || index < 0 || index >= len(k.comp.parts) {
				return no()
			}
			part := k.comp.parts[index]
			cur, k = child.tupleProjection(seg, cur, index, part), part
			continue
		}
		var ok bool
		cur, k, _, ok = child.fieldProject(seg, seg.Name, cur, k, false)
		if !ok {
			return no()
		}
	}
	if !irCallableValueKind(k) && k != kindUnit {
		irDeclineNote("a field accessor result outside the domain: " + k.nomi())
		return no()
	}
	child.b.SetTerm(ir.NewReturn(bl.g.irNodePos(t), cur))
	if err := ir.Lint(f); err != nil {
		irDeclineNote("a field accessor graph that does not lint: " + err.Error())
		return no()
	}
	n := ir.NewFuncValue(at, bl.f.NewTemp(), f)
	bl.b.Append(n)
	fk := funcKindIn(bl.g, []kind{pk}, k)
	bl.side(n.Dst(), irScalarSide{k: fk})
	return n.Dst(), fk, true, true
}
