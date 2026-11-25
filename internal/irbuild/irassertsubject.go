package irbuild

// The two assertion subjects that are not a Bool.
//
// The judgement is read off the value: an `Option`/`Result`
// is a SHAPE subject (`assert` needs Some/Ok, `refute` the other variant, and
// the assertion's value is the subject either way), and any other non-Bool is
// an `Assertable` whose own `failure` impl answers the verdict. The VM reads
// the shape off the value (internal/vm's judge). What it cannot do is
// select the `Assertable.failure` impl, because that is this builder's static
// dispatch; so the builder calls the impl after the subject, and the
// `ir.Assert` carries the
// answer (`ir.Assert.WithAnswer`).

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// fnAssertion reports whether the assertion keyword at line:col is inside an
// ordinary `fn` or a `concurrent` block the checker names as its boundary,
// where a failure is that activation's `Err(AssertionFailure)`: a
// `concurrent` body is its own ir.Func, so the failure ends the block as a
// `?` does and the block's scope exit cancels its in-flight tasks. The
// classification is the checker's (`assertionBoundary`); a lambda boundary
// declines rather than being lowered as though the enclosing function were
// the boundary.
//
// Inside a test body's own activation (an arm or block of a region the body
// binds or returns), the boundary is the case, and a failure ends it as a
// statement assertion there does.
func (bl *irScalarBuilder) fnAssertion(line, col int) bool {
	boundary, ok := bl.g.assertionBoundary(line, col)
	inBlock := boundary == "concurrent" && bl.tryBoundary == "concurrent"
	inCase := ok && bl.inTest && bl.parent == nil && isTestBoundary(boundary)
	inRow := ok && bl.subtestsRow && isSubtestsRowBoundary(boundary)
	if inCase || inRow {
		return true
	}
	if !ok || bl.inTest || !(strings.HasPrefix(boundary, "fn ") || inBlock) {
		bl.g.dc.note("an assertion whose boundary is not an ordinary fn, a concurrent block or a test case")
		return false
	}
	return true
}

// assertionExpr lowers an assertion the checker admits in a value position:
// a binding's value (`ok = assert x`, `ok: Bool = assert x`, `_ = assert x`,
// `Some(n) = assert m else ... end`), a block's final statement, or an `if`
// or `case` arm whose value is used. Its value is the judged subject (spec
// §17): True for `assert` of a Bool, False for `refute`, and the subject
// itself for an Option, Result or Assertable. A failure leaves through the
// same boundary a statement assertion's does.
func (bl *irScalarBuilder) assertionExpr(t *ast.Assertion) (ir.Temp, kind, bool, bool) {
	if !bl.fnAssertion(t.Line, t.Col) {
		return ir.NoTemp, kindInvalid, false, false
	}
	v, k, ok := bl.assertionValue(t)
	// The subject is held in a temporary before the judgement
	// (assertionValue), so reading it again evaluates nothing.
	return v, k, true, ok
}

// irAssertShapeKind reports whether k is a retained prelude Option or Result,
// the kinds an assertion judges by shape.
func irAssertShapeKind(k kind) bool {
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf == nil || !irRetainedEnumKind(k.def) {
		return false
	}
	spec := k.def.preludeOf.spec
	return spec == preludeSpecFor("std/option", "Option") || spec == preludeSpecFor("std/results", "Result")
}

// assertableImpl is the `Assertable.failure` a subject kind selects: this
// module's retained impl (it), or a std type's (std, with the call planned
// over the subject kind, as `io.Replayed`'s is).
type assertableImpl struct {
	it   *implItem
	std  *irQualPlan
	call *ast.Call
	k    kind
}

// assertableFailure is the retained `Assertable.failure` impl for subject
// kind k, or nil: an impl of std's interface, which a file's own
// `interface Assertable` does not take (stdIfaceImplKey).
func (bl *irScalarBuilder) assertableFailure(k kind) *assertableImpl {
	if k.tag != tagNamed || k.def == nil {
		return nil
	}
	d := bl.g.implAt(stdIfaceImplKey(stdAssertable), k)
	if d == nil || !d.lowerable {
		return bl.stdAssertableFailure(k)
	}
	it := d.items["failure"]
	if it == nil || !it.lowerable || irImplSource(it) == nil || len(it.params) != 1 || it.params[0] != k {
		return nil
	}
	if !irOptionKind(it.result) {
		return nil
	}
	return &assertableImpl{it: it}
}

// stdAssertableFailure is a std type's `impl Assertable`, selected by the
// subject kind (stdlibImplOf), or nil.
func (bl *irScalarBuilder) stdAssertableFailure(k kind) *assertableImpl {
	f := bl.g.stdlibImplOf(stdAssertable.Func("failure"), k, k)
	if f == nil || !irOptionKind(f.result) {
		return nil
	}
	// The operand is the subject, which assertableAnswer places.
	call := &ast.Call{Args: []ast.Node{&ast.Ident{}}}
	args := irQualArgs{temps: []ir.Temp{ir.NoTemp}, kinds: []kind{k}, mobile: []bool{true}, ok: true}
	p := bl.stdFuncPlan(call, args, f)
	if p == nil {
		return nil
	}
	return &assertableImpl{std: p, call: call, k: k}
}

// irOptionKind reports whether r is a prelude Option, what `failure` returns.
func irOptionKind(r kind) bool {
	return r.tag == tagNamed && r.def != nil && r.def.preludeOf != nil &&
		r.def.preludeOf.spec == preludeSpecFor("std/option", "Option")
}

// assertableAnswer calls the subject's `Assertable.failure` impl and answers
// the `Option<AssertionDetails>` it returns. It runs after the subject and its
// rows, with recording off, so the impl's own operands are never rows.
func (bl *irScalarBuilder) assertableAnswer(at ast.Node, a *assertableImpl, subj ir.Temp) ir.Temp {
	if a.std != nil {
		call := *a.call
		call.Line, call.Col = nodePos(at)
		call.Args = []ast.Node{at}
		v, _, _, _ := bl.qualEmit(&call,
			irQualArgs{temps: []ir.Temp{subj}, kinds: []kind{a.k}, mobile: []bool{true}, ok: true}, a.std)
		return v
	}
	c := ir.NewCall(bl.g.irNodePos(at), bl.f.NewTemp(), ir.OrdinaryCall,
		bl.g.irImplSym(a.it), subj)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: a.it.result, deferrable: true})
	return c.Dst()
}
