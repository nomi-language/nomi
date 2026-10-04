package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func (bl *irScalarBuilder) tryValue(t *ast.TryOp, operand, whole ast.Node) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	boundary, recorded := bl.g.recordedTryBoundary(t)
	// A `concurrent` body's boundary is the block, and a lambda's is the
	// lambda's own activation; either's kind settles after its body, and
	// the builder checks each try against it then.
	concurrent := recorded && bl.concTries != nil && boundary == bl.tryBoundary
	// A try in boot's own body leaves boot with the failing value, which the
	// VM reports as the program's failure (internal/vm/boot.go). The operand is forwarded unchanged.
	boot := recorded && boundary == "fn boot" && bl.boot && bl.parent == nil
	// A try whose boundary is a VM-only TEST body ends the case with a
	// report rather than returning a value, so no boundary kind is involved:
	// the VM's runner builds the rt.EarlyReturnFailure tryTestGuard emits,
	// from the Try's own line and text. The checker's boundary decides it, for
	// tryBoundaryRefusal's reason: a try in a `concurrent` block or a lambda
	// inside a test unwinds that, not the case.
	testBoundary := recorded && bl.inTest && bl.parent == nil &&
		isTestBoundary(boundary)
	if !recorded || (bl.inTest && !testBoundary) || isNilNode(operand) {
		return no()
	}
	if !concurrent && !testBoundary && !boot && (!strings.HasPrefix(boundary, "fn ") || boundary == "fn boot" || bl.parent != nil || bl.inferReturn != nil) {
		return no()
	}
	if !concurrent && !testBoundary && !boot && !irTryBoundaryKind(bl.returnKind) {
		return no()
	}
	src, k, pure, ok := bl.lower(operand)
	d := k.def
	if !ok || d == nil || !irRetainedEnumKind(d) || d.preludeOf == nil || !d.preludeOf.spec.tryOperand || len(d.variants) != 2 || len(d.variants[0].payloads) != 1 {
		return no()
	}
	u := &d.variants[1]
	carried := make([]kind, len(u.payloads))
	for i := range u.payloads {
		carried[i] = u.payloads[i].k
	}
	if testBoundary {
		// An AssertionFailure payload (`try testing.check(e)`) reports the
		// failed check itself: the VM's runner reads the propagated failure
		// back into its report.
		if _, anchored := assertionFailureKind(); !anchored {
			return no()
		}
	} else if !concurrent && !boot && !irTryFits(bl.returnKind, d, carried) {
		return no()
	}
	if !pure {
		cp := ir.NewCopy(bl.g.irNodePos(operand), bl.f.NewTemp(), src)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
		src = cp.Dst()
	}
	tr := ir.NewTry(bl.g.irNodePos(t), src, renderNode(whole))
	bl.b.Append(tr)
	if concurrent {
		*bl.concTries = append(*bl.concTries, irPendingTry{try: tr, operand: d, carried: carried})
	}
	v := &d.variants[0]
	part := v.payloads[0]
	pr := ir.NewProjPayload(bl.g.irNodePos(whole), bl.f.NewTemp(), src, bl.g.irTypeSym(d), v.nomi, 0, irParamShape(part.k))
	bl.b.Append(pr)
	bl.side(pr.Dst(), irScalarSide{k: part.k})
	return pr.Dst(), part.k, true, true
}

// irPendingTry is a try in a `concurrent` body, checked against the block's
// kind once the body settles.
type irPendingTry struct {
	try     *ir.Try
	operand *typeDef
	carried []kind
}

// irTryBoundaryKind is a boundary a retained try can exit to.
func irTryBoundaryKind(k kind) bool {
	bd := k.def
	return bd != nil && irRetainedEnumKind(bd) && bd.preludeOf != nil && bd.preludeOf.spec.tryOperand && len(bd.variants) == 2
}

// irTryFits reports whether an operand of d exits to boundary.
func irTryFits(boundary kind, d *typeDef, carried []kind) bool {
	return irTryBoundaryKind(boundary) && d.preludeOf.spec == boundary.def.preludeOf.spec && tryBoundaryFits(boundary, d, carried)
}

// settleConcurrentTries gives each try in a `concurrent` body the block's
// settled kind as its boundary, or reports that one cannot exit to it.
func (bl *irScalarBuilder) settleConcurrentTries(pending []irPendingTry, boundary kind) bool {
	for _, p := range pending {
		if !irTryFits(boundary, p.operand, p.carried) {
			return false
		}
	}
	return true
}
