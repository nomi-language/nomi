package vm

// JUDGING AN ASSERTION, AND RECORDING THE ROWS ITS FAILURE REPORT PRINTS.
//
// THE JUDGEMENT AND THE TEXT ARE `rt`'s AND ARE CALLED, NOT TRANSCRIBED. That
// is this package's standing rule for anything whose output is a contract —
// `rt/arith.go`'s overflow predicates, `rt.NoCaseMatchError`, `rt.DbgText` —
// and it is sharpest here, because `rt/assertion.go`'s own header says a
// second encoding of "which subjects hold", "what the reason line says" or
// "which operand rows are worth showing" would be "a divergence generator
// whose only pin is the cases a corpus happens to contain". So:
//
//	the verdict and the failure value   rt.AssertionSite.JudgeBool
//	the suppression rule for a row      rt.RecordOperand
//	the report's whole layout           rt.WriteAssertionFailure, via the
//	                                    reporter rt.RunTestsTo drives
//
// WHAT THIS CONSUMER OWNS is the same short list every other class leaves it:
// how a value READS in a row, and where the failure GOES.
//
//   - THE READING IS `rt.RowText`, which bottoms out in `rt.InspectString`/`InspectInt`/`InspectFloat`/
//     `InspectBool` — so the quoting of a String and the shaping of a Float
//     have one home.
//   - THE DESTINATION IS AN UNWIND. `assert`, `refute` and `testing.check`
//     are ONE judgement with THREE deliveries, and where the failure goes is
//     the consumer's (the fault/delivery division of `internal/ir`'s package
//     header): this
//     returns an error that `Machine.RunTests` converts back into the case's
//     failure. That is not in the node.
//
// # Why the trace is per ACTIVATION and consumed by the judgement
//
// A machine has no build-time count of the rows an assertion's subject will
// produce, so the frame carries one slice and the `Assert` that reads it
// TRUNCATES it, which makes the slice per-assertion in effect while
// allocating once per activation that asserts at all.
//
// A frame and not the machine, for `frame`'s own reason: two activations are
// two sets of temporaries, and an assertion inside a callee must not append to
// its caller's rows. That is the assertion TRACE BOUNDARY, and here it is a
// field on the activation. It is the reason `testdata/tests_call_boundary.nomi`'s report cannot leak a
// callee's comparison even once the callee is retained.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// assertFailure is a failed `assert`/`refute` travelling out of an activation.
//
// AN ERROR BECAUSE THAT IS THE ONLY EXIT `call` HAS, and a TYPED one because
// the runner has to tell three outcomes apart: a case that failed an
// assertion, a case that took a Nomi fault, and a machine that ran into an
// instruction it has no arm for. The first two are the case's result and the
// third means this record is not comparable at all — see `Machine.RunTests`.
//
// `Error()` is the failure's own `line N: <reason>` header, which is what
// `*rt.AssertionFailure.Error()` answers, so nothing that only prints the
// error loses information.
type assertFailure struct {
	failure *rt.AssertionFailure
}

func (e *assertFailure) Error() string { return e.failure.Error() }

// Unwrap hands back the failure so `errors.As` reaches it, which is how the
// runner converts an unwind back into the case's `rt.TestFailure`.
func (e *assertFailure) Unwrap() error { return e.failure }

// Fault is a NOMI FAULT that left an activation: an integer overflow, a
// division by zero, a `case` with no matching arm.
//
// IT EXISTS BECAUSE THE RUNNER HAS TO TELL A FAULT FROM A MACHINE LIMIT: a
// fault is the program's exit 1 with its text on stderr, and
// `testdata/tests_trap.nomi` is a record that reaches one.
//
// THE TEXT IS rt's: `Error()` delegates, so `rt.OverflowError`'s and
// `rt.NoCaseMatchError`'s bytes are what a caller sees, and the wrapper is
// visible only to a caller that asks for it with `errors.As`.
//
// THE CLASSIFICATION IS THE GRAPH'S, not a string test on the message.
// `ir.InstrFaults(in).Any()` is what `call` already consults to decide whether
// a fault edge applies; an error out of an instruction that CAN fault, with no
// edge to take, is a fault leaving the function.
type Fault struct {
	err error
}

func (f *Fault) Error() string { return f.err.Error() }

// Unwrap is the underlying error, whose text `rt` owns.
func (f *Fault) Unwrap() error { return f.err }

// assertInstr judges one assertion and either continues or unwinds.
func (m *Machine) assertInstr(fr *frame, n *ir.Assert) error {
	subj, err := fr.read(n.Subject())
	if err != nil {
		return err
	}
	binding, err := m.assertBinding(fr, n)
	if err != nil {
		return err
	}
	if n.Mismatch() {
		// `assert pattern = value`'s failure edge, which the producer reaches
		// only when the pattern did not match. No `values:` rows: the value
		// is evaluated outside a subject, so the frame's trace is empty and
		// is reset here.
		fr.trace = fr.trace[:0]
		site := rt.AssertionSite{Line: n.Pos().Line(), Expr: n.Text()}
		text, err := m.rowText(fr, subj)
		if err != nil {
			return err
		}
		return assertExit(fr, site.Failure("pattern did not match", text, binding, nil))
	}
	site := rt.AssertionSite{
		Line:   n.Pos().Line(),
		Expr:   n.Text(),
		Refute: n.Refuted(),
		Check:  n.Keyword() == ir.KeywordCheck,
	}
	// THE ROWS BELONG TO THIS ASSERTION AND ARE SPENT BY IT. The slice is
	// handed over and the frame's is truncated, which gives each assertion
	// its own rows without a build-time count.
	rows := fr.trace
	fr.trace = fr.trace[:0]
	stages := fr.stages
	fr.stages = nil
	f, err := m.judge(fr, n, site, subj, binding, rows)
	if err != nil {
		return err
	}
	if f != nil && !site.Refute && f.Diff == nil {
		// A failed `==` over two multi-line Strings shows a line diff in
		// place of its two operand rows. A failed `refute`, like a failed
		// `!=`, means the two were equal, and there is nothing to diff. An
		// Assertable that answered `expected` already has its diff.
		f.Diff = stringDiff(fr.fn, n, rows)
	}
	if f != nil && len(stages) > 0 {
		// A failed piped `check` explains its pipe: one more `values:` row
		// whose text is the whole subject, appended after the operand rows.
		text, err := m.rowText(fr, subj)
		if err != nil {
			return err
		}
		f.Values = append(append([]rt.AssertionValueContext(nil), f.Values...), rt.AssertionValueContext{
			Expr: n.Text(), Value: text, Pipeline: stages})
	}
	if site.Check {
		// `check` answers a `Result` rather than leaving the body: the one
		// delivery with a destination (`Assert.Dst()`). checkResult turns the judgement into the value.
		fr.write(n.Dst(), checkResult(subj, f))
		return nil
	}
	if f == nil {
		return nil
	}
	return assertExit(fr, f)
}

// assertExit delivers a failed `assert`/`refute` out of its activation.
//
// A TEST BODY's failure leaves as the report the runner prints. AN ORDINARY
// `fn` IS ITS OWN BOUNDARY: the failure is its return value,
// `Err(AssertionFailure)`, delivered at the innermost activation. The front end admits `assert` in a `fn` only when its result is
// `Result<_, AssertionFailure>`, and refuses it in a lambda. The unwind is the
// `try` channel's, which `fault` consumes at this activation.
func assertExit(fr *frame, f *rt.AssertionFailure) error {
	if !fr.testBody {
		return &tryReturn{value: errValue(nomiFailureValue(f.NomiFailure()))}
	}
	return &assertFailure{failure: f}
}

// judge decides one assertion over its subject: nil means it held.
//
// WHICH JUDGEMENT IS READ OFF THE VALUE: a
// `Maybe`/`Result` record is a shape subject, a subject carrying an answer is
// an `Assertable`, and a Bool is a Bool. The rules and the words are
// `rt.AssertionSite`'s (`JudgeBool`, `ShapeFailure`, `JudgeAssertable`), and
// the report is built only on the losing branch.
func (m *Machine) judge(fr *frame, n *ir.Assert, site rt.AssertionSite, subj any,
	binding *rt.AssertionBindingContext, rows []rt.AssertionValueContext) (*rt.AssertionFailure, error) {
	if carries, isShape := shapeCarries(subj); isShape {
		if site.Holds(carries) {
			return nil, nil
		}
		text, err := m.rowText(fr, subj)
		if err != nil {
			return nil, err
		}
		return site.ShapeFailure(text, binding, rows), nil
	}
	if n.Answer() != ir.NoTemp {
		answer, err := fr.read(n.Answer())
		if err != nil {
			return nil, err
		}
		details, err := assertableDetails(answer)
		if err != nil {
			return nil, fmt.Errorf("vm: %s: %w", fr.fn.Name(), err)
		}
		return site.JudgeAssertable(details, binding, rows), nil
	}
	held, isBool := asBool(subj)
	if !isBool {
		// The producer builds one of the three judgements above for every
		// subject it retains. Reported rather than guessed, because a
		// hand-built module can reach it.
		return nil, fmt.Errorf("vm: %s: this machine judges a Bool, Maybe, Result or Assertable subject and %s is %T",
			fr.fn.Name(), n.Subject(), subj)
	}
	return site.JudgeBool(held, binding, rows), nil
}

// shapeCarries reads a `Maybe` or `Result` subject: whether it is the
// carrying variant (Some, Ok), and whether it is a shape subject at all.
func shapeCarries(v any) (carries, isShape bool) {
	r, ok := enumRecord(v)
	if !ok {
		return false, false
	}
	switch r.Desc.Name {
	case "maybe.Maybe":
		return variantName(r) == "Some", true
	case "results.Result":
		return variantName(r) == "Ok", true
	}
	return false, false
}

// assertableDetails reads the `Maybe<AssertionDetails>` an `Assertable`
// answered into rt's report shape, through rt.AssertableDetailsOf: nil is
// None, the subject reporting no failure.
func assertableDetails(answer any) (*rt.AssertableDetails, error) {
	payload, isSome, ok := maybeParts(answer)
	if !ok {
		return nil, fmt.Errorf("Assertable.failure must return Maybe<AssertionDetails>, got %T", answer)
	}
	if !isSome {
		return nil, nil
	}
	r, ok := structRecord(payload, "assertions.AssertionDetails")
	if !ok {
		return nil, fmt.Errorf("Assertable.failure must return Maybe<AssertionDetails>, got Some(%T)", payload)
	}
	var d rt.NomiAssertionDetails
	if reason, ok := r.FieldNamed("reason"); ok {
		d.Reason, _ = reason.(string)
	}
	if actual, ok := r.FieldNamed("actual"); ok {
		if s, some, _ := maybeParts(actual); some {
			d.Actual = rt.Some(s.(string))
		}
	}
	d.Expected = rt.None[string]()
	if expected, ok := r.FieldNamed("expected"); ok {
		if s, some, _ := maybeParts(expected); some {
			d.Expected = rt.Some(s.(string))
		}
	}
	if details, ok := r.FieldNamed("details"); ok {
		xs, _ := details.(*list)
		var rows []rt.NomiAssertionDetail
		for _, item := range listSlice(xs) {
			dr, ok := structRecord(item, "assertions.AssertionDetail")
			if !ok {
				return nil, fmt.Errorf("an AssertionDetails row is %T, not an AssertionDetail", item)
			}
			label, _ := dr.FieldNamed("label")
			value, _ := dr.FieldNamed("value")
			ls, _ := label.(string)
			vs, _ := value.(string)
			rows = append(rows, rt.NomiAssertionDetail{Label: ls, Value: vs})
		}
		d.Details = rtSliceList(rows)
	}
	return rt.AssertableDetailsOf(rt.Maybe[rt.NomiAssertionDetails]{Tag: rt.TagSome, Some: d}), nil
}

// assertBinding is the "defined as:" block the node carries, with the value
// read as the report reads it, or nil.
func (m *Machine) assertBinding(fr *frame, n *ir.Assert) (*rt.AssertionBindingContext, error) {
	b := n.Binding()
	if b == nil {
		return nil, nil
	}
	v, err := fr.read(b.Val)
	if err != nil {
		return nil, err
	}
	text, err := m.rowText(fr, v)
	if err != nil {
		return nil, err
	}
	if len(b.Stages) == 0 {
		return rt.Binding(b.Name, b.Expr, text), nil
	}
	// The binding's `pipeline values:` block, as native's bindingContext
	// spells it for a pipe-valued initializer.
	stages := make([]rt.AssertionPipelineStage, 0, len(b.Stages))
	for _, st := range b.Stages {
		sv, err := fr.read(st.Val)
		if err != nil {
			return nil, err
		}
		stext, err := m.rowText(fr, sv)
		if err != nil {
			return nil, err
		}
		stages = append(stages, rt.AssertionPipelineStage{Expr: st.Text, Value: stext})
	}
	return &rt.AssertionBindingContext{Name: b.Name, Expr: b.Expr, Value: text, Pipeline: stages}, nil
}

// recordInstr appends one `values:` row.
func (m *Machine) recordInstr(fr *frame, n *ir.Record) error {
	v, err := fr.read(n.Val())
	if err != nil {
		return err
	}
	text, err := m.rowText(fr, v)
	if err != nil {
		return err
	}
	if n.Kind() == ir.RecordStage {
		// A `pipeline values:` row: one cumulative prefix of a piped
		// `testing.check`'s subject. Kept whatever it renders as, and spent
		// by the check (assertInstr).
		fr.stages = append(fr.stages, rt.AssertionPipelineStage{Expr: n.Text(), Value: text})
		return nil
	}
	// THE SUPPRESSION RULE IS rt's, ASKED RATHER THAN APPLIED: an operand
	// that reads exactly like its value explains nothing, so `assert 1 == 2`
	// prints no rows. `ir.Record.SuppressRedundant` is the producer's REQUEST
	// and `rt.RecordOperand` is the one place the request is answered. What
	// this consumer adds is the fact the rule reads: whether the operand is a
	// literal.
	literal := n.SuppressRedundant() && literalOperand(fr.fn, n.Val(), n.Text()) != nil
	rt.RecordOperand(&fr.trace, n.Text(), text, n.SuppressRedundant(), literal)
	return nil
}

// literalOperand is the constant a recorded operand was written as, or nil
// when it is not a literal.
//
// The graph says the value is a constant and the text says it was written as
// one: a Bool, Int, Float, Decimal or String constant whose source opens
// with a quote, or is one numeric token (after an optional `-`). The text
// test is what keeps a name bound to a constant, `limit` in `limit = 5`, a
// row. A Bool literal's text is its value, so rt drops its row already.
func literalOperand(fn *ir.Func, t ir.Temp, text string) *ir.Const {
	c, ok := fn.Def(t).(*ir.Const)
	if !ok {
		return nil
	}
	switch c.Kind() {
	case ir.ConstBool, ir.ConstInt, ir.ConstFloat, ir.ConstDecimal, ir.ConstString:
	default:
		return nil
	}
	s := strings.TrimPrefix(text, "-")
	switch {
	case s == "":
		return nil
	case s[0] == '"':
		return c
	case s[0] >= '0' && s[0] <= '9' && !strings.ContainsAny(s, " \t\n()"):
		// One numeric token: `1_000`, `1.5d`, `-3`, and not `1 + 2`.
		return c
	}
	return nil
}

// stringDiff is the line diff for a failed `assert a == b` over two Strings
// (rt.StringDiff), or nil.
//
// Read off the graph on the failing path only. The subject must be exactly
// one `==` comparison: its definition, through the copy the builder makes of
// an impure subject, is an `ir.Compare` of String shape, and the assertion's
// text is the two operands' texts around ` == `, which rules out a
// comparison that is only part of the subject (`a == b and c`). The operands'
// texts are the `ir.Record`s of the comparison's two temporaries, and their
// values are the trace's rows for those texts, or the constant itself for a
// literal operand, whose row is not kept.
func stringDiff(fn *ir.Func, n *ir.Assert, rows []rt.AssertionValueContext) *rt.AssertionStringDiff {
	def := fn.Def(n.Subject())
	if c, ok := def.(*ir.Copy); ok {
		def = fn.Def(c.Src())
	}
	cmp, ok := def.(*ir.Compare)
	if !ok || cmp.Ranked() || cmp.Op() != ir.OpEq || cmp.Shape() != ir.ValString {
		return nil
	}
	var lhsText, rhsText string
	for _, b := range fn.Blocks() {
		for _, in := range b.Instrs() {
			r, ok := in.(*ir.Record)
			if !ok || r.Kind() != ir.RecordOperand {
				continue
			}
			switch r.Val() {
			case cmp.Lhs():
				lhsText = r.Text()
			case cmp.Rhs():
				rhsText = r.Text()
			}
		}
	}
	if lhsText == "" || rhsText == "" || n.Text() != lhsText+" == "+rhsText {
		return nil
	}
	actual, ok := operandString(fn, cmp.Lhs(), lhsText, rows)
	if !ok {
		return nil
	}
	expected, ok := operandString(fn, cmp.Rhs(), rhsText, rows)
	if !ok {
		return nil
	}
	return rt.StringDiff(lhsText, actual, rhsText, expected)
}

// operandString is the String value of one compared operand.
func operandString(fn *ir.Func, t ir.Temp, text string, rows []rt.AssertionValueContext) (string, bool) {
	if c := literalOperand(fn, t, text); c != nil {
		return c.Text(), c.Kind() == ir.ConstString
	}
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Expr != text || len(row.Pipeline) > 0 {
			continue
		}
		// A String's row is rt.InspectString: quoted, not escaped.
		v := row.Value
		if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
			return "", false
		}
		return v[1 : len(v)-1], true
	}
	return "", false
}

// compareInstr answers one relation over two operands of one shape.
//
// FIVE SHAPES AND SIX OPERATORS, AND THE DISPATCH IS ON THE NODE'S SHAPE
// rather than on the operands' Go types, because the node states what the
// producer established and a disagreement is a producer bug `ir.Lint`'s
// RuleOperandShape reports. The operand read still checks, for `read`'s
// reason: a hand-built module can reach it and a silent wrong answer is worse
// than a reported one.
//
// ORDERING ON A String OR A Bool CANNOT ARRIVE — `ir.NewCompare` rejects it
// and `internal/irbuild` declines it — so the switch below has no arm for it and
// the default is the report.
func (m *Machine) compareInstr(fr *frame, n *ir.Compare) (err error) {
	defer recoverKey(&err)
	lhs, err := fr.read(n.Lhs())
	if err != nil {
		return err
	}
	if n.Ranked() {
		ord, ok := orderingOf(lhs)
		if !ok {
			return fmt.Errorf("vm: %s: ranked comparison reads %T, not an Ordering", fr.fn.Name(), lhs)
		}
		return m.compareOrdered(fr, n, int(rt.OrderingRank(ord)))
	}
	rhs, err := fr.read(n.Rhs())
	if err != nil {
		return err
	}
	switch n.Shape() {
	case ir.ValContainer:
		// A List, Vector, Map, Set or Range: rt's structural equality for
		// each, with an element of a declared type compared (and a Map key or
		// a Set element hashed) through its own impls (keys.go).
		if !isContainer(lhs) || !isContainer(rhs) {
			return compareOperandError(fr, n, lhs, rhs)
		}
		return m.compareEqual(fr, n, m.keysFor(fr).Equal(lhs, rhs))
	case ir.ValDecimal:
		a, aok := lhs.(rt.Decimal)
		b, bok := rhs.(rt.Decimal)
		if !aok || !bok {
			return compareOperandError(fr, n, lhs, rhs)
		}
		return m.compareEqual(fr, n, rt.EqDecimal(a, b))
	case ir.ValInt:
		a, aok := lhs.(int64)
		b, bok := rhs.(int64)
		if !aok || !bok {
			return compareOperandError(fr, n, lhs, rhs)
		}
		return m.compareOrdered(fr, n, orderOf(a, b))
	case ir.ValFloat:
		a, aok := lhs.(float64)
		b, bok := rhs.(float64)
		if !aok || !bok {
			return compareOperandError(fr, n, lhs, rhs)
		}
		// Float equality is reflexive for NaN; ordering remains unordered.
		// A three-way comparison would collapse unordered pairs into equality.
		if n.Op() == ir.OpEq || n.Op() == ir.OpNe {
			return m.compareEqual(fr, n, rt.EqFloat(a, b))
		}
		var held bool
		switch n.Op() {
		case ir.OpLt:
			held = a < b
		case ir.OpLe:
			held = a <= b
		case ir.OpGt:
			held = a > b
		case ir.OpGe:
			held = a >= b
		default:
			return fmt.Errorf("vm: %s: this machine runs no %s", fr.fn.Name(), n)
		}
		fr.write(n.Dst(), boolValue(held))
		return nil
	case ir.ValString:
		a, aok := lhs.(string)
		b, bok := rhs.(string)
		if !aok || !bok {
			return compareOperandError(fr, n, lhs, rhs)
		}
		// EQUALITY ONLY, which is the admitted set: a String's ordering is
		// `impl Comparable` dispatch and the producer declines it. So the
		// order is not computed and `orderOf` is not reached with two
		// Strings.
		return m.compareEqual(fr, n, a == b)
	case ir.ValVariant:
		// A value of an enum with `embeds` variants is, for those variants,
		// the bare payload record (a struct, a distinct or a marker), so the
		// operands are records of any kind; rt.Equal compares an embedded
		// variant by its payload. A value of a declared type with a
		// hand-written Equatable, at the top (a tuple's slot the producer
		// compares one by one) or nested, answers through its impl
		// (keys.go); the producer never compares inside that impl's own
		// body this way, where it would recurse.
		a, aok := lhs.(*rt.Record)
		b, bok := rhs.(*rt.Record)
		if !aok || !bok || a == nil || b == nil {
			return compareOperandError(fr, n, lhs, rhs)
		}
		return m.compareEqual(fr, n, m.keysFor(fr).Equal(lhs, rhs))
	case ir.ValStruct:
		if !isStructLike(lhs) || !isStructLike(rhs) {
			return compareOperandError(fr, n, lhs, rhs)
		}
		return m.compareEqual(fr, n, m.keysFor(fr).Equal(lhs, rhs))
	case ir.ValBool:
		a, aok := asBool(lhs)
		b, bok := asBool(rhs)
		if !aok || !bok {
			return compareOperandError(fr, n, lhs, rhs)
		}
		// False orders before True, std's derived Comparable for Bool.
		return m.compareOrdered(fr, n, orderOf(int64(b2w(a)), int64(b2w(b))))
	}
	return fmt.Errorf("vm: %s: this machine compares no %s", fr.fn.Name(), n.Shape())
}

// orderOf is the three-way answer two integers have, which is what
// makes the four ordering operators one comparison and a sign test.
func orderOf(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// compareOrdered delivers all six operators from the three-way answer.
func (m *Machine) compareOrdered(fr *frame, n *ir.Compare, order int) error {
	var held bool
	switch n.Op() {
	case ir.OpEq:
		held = order == 0
	case ir.OpNe:
		held = order != 0
	case ir.OpLt:
		held = order < 0
	case ir.OpLe:
		held = order <= 0
	case ir.OpGt:
		held = order > 0
	case ir.OpGe:
		held = order >= 0
	default:
		return fmt.Errorf("vm: %s: this machine runs no %s", fr.fn.Name(), n)
	}
	fr.write(n.Dst(), boolValue(held))
	return nil
}

// compareEqual delivers the two operators an unordered shape admits.
func (m *Machine) compareEqual(fr *frame, n *ir.Compare, equal bool) error {
	switch n.Op() {
	case ir.OpEq:
		fr.write(n.Dst(), boolValue(equal))
	case ir.OpNe:
		fr.write(n.Dst(), boolValue(!equal))
	default:
		return fmt.Errorf("vm: %s: %s over %s asks about order, which this shape has no answer for",
			fr.fn.Name(), n.Op().Symbol(), n.Shape())
	}
	return nil
}

func compareOperandError(fr *frame, n *ir.Compare, lhs, rhs any) error {
	return fmt.Errorf("vm: %s: a %s comparison's operands are %T and %T; "+
		"ir.Lint's operand-shape rule should have caught this",
		fr.fn.Name(), n.Shape(), lhs, rhs)
}

// asFault reports the Nomi fault an error carries, if it is one.
//
// Exported behaviour through `errors.As`, which is how the runner asks; the
// helper exists so the question is spelled once.
func asFault(err error) (*Fault, bool) {
	var f *Fault
	if errors.As(err, &f) {
		return f, true
	}
	return nil, false
}

// asAssertFailure reports the assertion failure an error carries, if it is one.
func asAssertFailure(err error) (*rt.AssertionFailure, bool) {
	var a *assertFailure
	if errors.As(err, &a) {
		return a.failure, true
	}
	return nil, false
}

// classifyFault marks err as a Nomi fault leaving an activation, unless it is
// already classified. See `call`'s fault arm for why both exceptions exist.
func classifyFault(err error) error {
	if _, already := asFault(err); already {
		return err
	}
	if _, assertion := asAssertFailure(err); assertion {
		return err
	}
	return &Fault{err: err}
}

// isStructLike reports whether v is a struct or an anonymous record, the
// values a ValStruct comparison reads.
func isStructLike(v any) bool {
	r, ok := v.(*rt.Record)
	return ok && r != nil && (r.Desc.Kind == rt.KindStruct || r.Desc.Kind == rt.KindAnon)
}

// isContainer reports whether v is a collection a ValContainer comparison
// reads: a List, a Vector, a Map, or std's Set or Range record. std's Byte
// and Bytes leaves are compared the same way, by rt.Equal.
func isContainer(v any) bool {
	switch x := v.(type) {
	case *list, rt.Vector[any], vmap, rt.Byte, rt.Bytes:
		return true
	case *rt.Record:
		return x != nil && (x.Desc.Name == "sets.Set" || x.Desc.Name == "ranges.Range")
	}
	return false
}
