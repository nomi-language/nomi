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
// The call is the one the checker recorded as std/testing's `check`
// (isTestingCheck): `q.check(e)` with q the module std/testing by any name
// (`testing`, or `t` after `import std::testing as t`), or a name a
// selective import bound to the function (`import std::testing.{check as
// verify}`). Either way it takes one positional argument.
//
// The subject may be a Bool, an `Option`/`Result` (judged by shape in the VM)
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
// delivery is a value, so no boundary decides it.

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// testingCheckCall reports whether t is a call of std/testing's `check`
// with one positional argument, as the checker recorded its callee
// (isTestingCheck).
func (bl *irScalarBuilder) testingCheckCall(t *ast.Call) bool {
	if len(t.Args) != 1 || len(t.TypeArgs) != 0 {
		return false
	}
	if _, named := t.Args[0].(*ast.NamedArg); named {
		return false
	}
	return isTestingCheck(bl.g.calleeOf(t))
}

// isTestingCheck reports whether c, the callee the checker recorded for a
// call, is std/testing's `check`: the function itself, which a selective
// import's name calls (`check(e)`, `verify(e)` after `import
// std::testing.{check as verify}`), or the keyword form the checker types
// `q.check(e)` as where q is the module std/testing, whose reference is
// the `check` assertion (checkTestingCheckCall). A binding named `testing`
// whose `check` field the call reads is a value callee, and a project
// module named `testing` is its own, and neither.
func isTestingCheck(c analysis.Callee) bool {
	if c.Std == testingCheck {
		return true
	}
	return c.Kind == analysis.CalleeKeyword && c.Ref != nil && c.Ref.Kind == analysis.SymbolAssertion &&
		c.Ref.Assertion != nil && c.Ref.Assertion.Keyword == "check"
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
	bl.recording++
	root := bl.assertRoot
	bl.assertRoot = subject
	defer func() { bl.assertRoot = root }()
	if prefixes := pipeStagePrefixes(subject); prefixes != nil {
		// A piped subject: each cumulative prefix is held and becomes a
		// `pipeline values:` row, which a failed check appends as one more
		// `values:` row.
		if !pipeStagesRecordable(prefixes) {
			bl.recording--
			bl.g.dc.note("a testing.check over a pipe whose stages are not its spine")
			return no()
		}
		subj, k, stages, lowered = bl.pipeStageValues(prefixes)
		mobile = true
	} else {
		subj, k, mobile, lowered = bl.lower(subject)
	}
	bl.recording--
	if !lowered {
		return no()
	}
	for _, st := range stages {
		bl.b.Append(ir.NewRecordStage(bl.g.irNodePos(subject), st.Val, st.Text))
	}
	var assertable *assertableImpl
	if k != kindBool && !irAssertShapeKind(k) {
		if assertable = bl.assertableFailure(k); assertable == nil {
			bl.g.dc.note("a testing.check subject that is not a Bool, Option, Result or retained Assertable")
			return no()
		}
	}
	// A Result's payload holds any value of the domain, and both are valid
	// kinds: the lowered subject's and std's AssertionFailure.
	result := bl.g.preludeInstanceOf(preludeSpecFor("std/results", "Result"), []kind{k, failure})
	// kindInvalid: fenced — a Result over two valid kinds always forms; the panic names the invariant.
	if result == kindInvalid {
		panic("irbuild: testing.check's Result<" + k.nomi() + ", AssertionFailure> did not form over a lowered subject")
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
