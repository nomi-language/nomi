package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// membership lowers `x in c` as the call it stands for, `Contains.contains(c,
// x)`, which the checker checked (FileAnalysis.Memberships): the same call a
// program writing it out lowers, through the same routes, with no IR node of
// its own. x is evaluated first, as written: it is lowered and held, and
// while the call lowers, its argument reads the held value (holeSubst).
func (bl *irScalarBuilder) membership(t *ast.Binary) (ir.Temp, kind, bool, bool) {
	no := func(why string) (ir.Temp, kind, bool, bool) {
		irDeclineNote(why)
		return ir.NoTemp, kindInvalid, false, false
	}
	if bl.g.fa == nil {
		return no("an `in` test with no analysis")
	}
	call := bl.g.srcFA().Memberships[t]
	if call == nil {
		return no("an `in` test the checker did not check")
	}
	x, xk, mobile, ok := bl.lower(t.Left)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	if !mobile {
		c := ir.NewCopy(bl.g.irNodePos(t.Left), bl.f.NewTemp(), x)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: xk, copy: irCopyForce})
		x = c.Dst()
	}
	name := membershipValueName(t)
	bl.bound[name], bl.boundK[name] = x, xk
	defer func() {
		delete(bl.bound, name)
		delete(bl.boundK, name)
	}()
	// x may itself be a capture's `&1`, which holeSubst already reads as
	// the capture's parameter; that reading comes back after the call.
	prev, had := bl.g.holeSubst[t.Left]
	undo := bl.g.substHoles(map[ast.Node]string{t.Left: name})
	defer func() {
		undo()
		if had {
			bl.g.holeSubst[t.Left] = prev
		}
	}()
	return bl.lower(call)
}

// isMembershipCall reports whether t is the call a membership test stands
// for.
func (bl *irScalarBuilder) isMembershipCall(t *ast.Call) bool {
	if bl.g.fa == nil || len(t.Args) != 2 {
		return false
	}
	for _, call := range bl.g.srcFA().Memberships {
		if call == t {
			return true
		}
	}
	return false
}

// membershipValueName is the name the call an `in` test stands for reads its
// held value by: one no Nomi program can spell.
func membershipValueName(t *ast.Binary) string {
	return "%in" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col)
}
