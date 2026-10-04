package irbuild

// `testing.check(e)`: the assertion judgement delivered as a value.
//
// `assert`, `refute` and `testing.check` are one judgement with three
// deliveries (`internal/ir/assert.go`), and `check`'s is a
// `Result<T, AssertionFailure>` VALUE. So the lowering is the assertion's:
// the subject lowers with recording on, so its operand rows are built exactly
// as `assert`'s are, and one `ir.Assert` with `ir.KeywordCheck` judges it and
// writes the `Result` to its destination.
//
// THE SPELLING IS LITERAL: the callee is written `testing.check` with one
// positional argument, and this confirms the qualifier resolves to
// std/testing.
//
// The subject may be a Bool, a `Maybe`/`Result` (judged by shape in the VM)
// or a retained `Assertable`, whose `failure` impl the builder calls and hands
// to the `ir.Assert` as its answer (irassertsubject.go). A PIPE subject lowers
// one cumulative prefix at a time (pipeStageValues) and each prefix becomes an
// `ir.RecordStage` row, which a failed check appends as one more `values:` row
// whose text is the whole subject.
//
// DECLINED, each for a named reason:
//
//   - a pipe whose stage list is not a function of its spine
//     (pipeStagesRecordable).
//   - a bare-name subject bound by a pipe whose stages were not recorded
//     (assertDefinedAs).
//   - a `Result<T, AssertionFailure>` outside the retained prelude payloads.
//
// `check` is lowered anywhere it is written, test body or not: its
// delivery is a value, so no boundary decides it. The Go reader spells no
// `check`, so a test body holding one is retained for the VM only.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// testingCheckCall reports whether t is `testing.check(e)` with the
// qualifier resolving to std/testing.
func (bl *irScalarBuilder) testingCheckCall(t *ast.Call) bool {
	if !isTestingCheckCallee(t.Func) || len(t.Args) != 1 || len(t.TypeArgs) != 0 {
		return false
	}
	if _, named := t.Args[0].(*ast.NamedArg); named {
		return false
	}
	if std, ok := stdFileQualifier(bl.g.fa, "testing"); ok {
		return std == "testing"
	}
	// std/testing naming itself from a source checked on its own (a tour
	// reference editor, vmhost.StdlibReference): the qualifier resolves to
	// this file's own scope, which the stdlib's module scopes list only as
	// the cached module's.
	return bl.g.stdModule == "testing" && bl.g.fa != nil &&
		moduleScopeOf(bl.g.fa, "testing") == bl.g.fa.ModuleScope
}

// testingCheck lowers `testing.check(e)`.
func (bl *irScalarBuilder) testingCheck(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	subject := t.Args[0]
	failure, ok := assertionFailureKind()
	if !ok {
		return no()
	}
	var subj ir.Temp
	var k kind
	var mobile, lowered bool
	var stages []ir.AssertStage
	witnessRow := bl.witnessRow
	bl.witnessRow = false
	bl.recording++
	if prefixes := pipeStagePrefixes(subject); prefixes != nil {
		// A piped subject: each cumulative prefix is held and becomes a
		// `pipeline values:` row, which a failed check appends as one more
		// `values:` row.
		if !pipeStagesRecordable(prefixes) {
			bl.recording--
			irDeclineNote("a testing.check over a pipe whose stages are not its spine")
			return no()
		}
		subj, k, stages, lowered = bl.pipeStageValues(prefixes)
		mobile = true
	} else {
		subj, k, mobile, lowered = bl.lower(subject)
	}
	bl.recording--
	if bl.witnessRow {
		irDeclineNote("a testing.check row over a Type witness")
		return no()
	}
	bl.witnessRow = witnessRow
	if !lowered {
		return no()
	}
	for _, st := range stages {
		bl.b.Append(ir.NewRecordStage(bl.g.irNodePos(subject), st.Val, st.Text))
	}
	var assertable *implItem
	if k != kindBool && !irAssertShapeKind(k) {
		if assertable = bl.assertableFailure(k); assertable == nil {
			irDeclineNote("a testing.check subject that is not a Bool, Maybe, Result or retained Assertable")
			return no()
		}
	}
	result := bl.g.preludeInstanceOf(preludeSpecFor("std/results", "Result"), []kind{k, failure})
	// kindInvalid: propagates — preludeInstanceOf answers it for an argument it rejected.
	if result == kindInvalid {
		irDeclineNote("a testing.check whose Result<T, AssertionFailure> is outside the retained payloads")
		return no()
	}
	if !mobile {
		c := ir.NewCopy(bl.g.irNodePos(subject), bl.f.NewTemp(), subj)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: k, copy: irCopyForce})
		subj = c.Dst()
	}
	line, col := t.Line, t.Col
	if fa, isField := t.Func.(*ast.FieldAccess); isField && fa.Field != nil {
		line, col = fa.Field.Line, fa.Field.Col
	}
	dst := bl.f.NewTemp()
	a := ir.NewAssert(bl.g.irPos(line, col), dst, subj, ir.KeywordCheck, renderNode(subject))
	if assertable != nil {
		a.WithAnswer(bl.assertableAnswer(subject, assertable, subj))
	}
	def, ok := bl.assertDefinedAs(subject, subj)
	if !ok {
		return no()
	}
	if def != nil {
		a.WithBinding(*def)
	}
	bl.b.Append(a)
	bl.side(dst, irScalarSide{k: result})
	return dst, result, false, true
}
