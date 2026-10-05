package rt

import (
	"fmt"
	"sort"
	"strings"
)

// What an `assert`/`refute` failure is, and the rules that decide one.
//
// An assertion failure's text is a hard contract: `nomi test` prints it and
// the golden files pin it byte for byte. So "which subjects hold", "what the
// reason line says" and "which operand rows are worth showing" each have one
// encoding, here.
//
// The types below carry no AST and no boxed value. Everything an assertion
// report shows is text by the time it gets here, which is what lets the whole
// contract live in rt.

// AssertionFailure is the structured value produced by a failed `assert`,
// `refute`, or `check`. `assert`/`refute` carry it through the same
// early-return channel as `try`; `check` returns it as an ordinary Result.Err.
// Command/test runners render the richer context for humans.
type AssertionFailure struct {
	Line    int
	Keyword string
	Expr    string
	Actual  string
	Reason  string
	Binding *AssertionBindingContext
	Values  []AssertionValueContext
	Details []AssertionDetailContext
}

// AssertionBindingContext explains a failed assertion whose expression was a
// binding name by showing that binding's original expression and observed
// pipeline values.
type AssertionBindingContext struct {
	Name     string
	Expr     string
	Value    string
	Pipeline []AssertionPipelineStage
}

// AssertionPipelineStage is one observed value in a pipeline expression.
type AssertionPipelineStage struct {
	Expr  string
	Value string
}

// AssertionValueContext is one expression/value pair observed while evaluating
// an assertion expression.
type AssertionValueContext struct {
	Expr     string
	Value    string
	Pipeline []AssertionPipelineStage
}

// AssertionDetailContext is one custom label/value pair supplied by an
// Assertable value.
type AssertionDetailContext struct {
	Label string
	Value string
}

// Error is the `line N: <reason>` header every report opens with. The absolute
// source line is carried on the failure, so nothing has to recover it from a
// frame.
func (e *AssertionFailure) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Reason)
}

// EarlyReturnFailure is a test that ended early instead of running to
// completion — a `try` whose operand was `Err`/`None`, or a plain `return`. It
// lives beside AssertionFailure because the reporter renders both and the
// reporter is in this module.
//
// Expr is the ending expression as the formatter renders it and Value is the
// value it produced, already rendered: for a `try` that is the row rendering
// (RowText) of the whole `Err(e)`/`None`, which is the structural rendering (sorted struct
// fields, strings quoted) and not Display and not Debug.
type EarlyReturnFailure struct {
	Line  int
	Expr  string
	Value string
}

func (e *EarlyReturnFailure) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: test returned early", e.Line)
	}
	return "test returned early"
}

// TestFailure is how a lowered test body ends when it does not pass, and
// exactly two types inhabit it: *AssertionFailure and *EarlyReturnFailure.
//
// Those are the two non-passing exits a test body has as a value — an assertion
// judged false, and an early return out of the body (`try` propagating an
// `Err`/`None`, and a plain `return`). Every other way a case can fail is a
// Nomi fault, which arrives as a panic and is recovered by runTest, so it never
// needs a return type.
//
// # Why a method and not `error`
//
// Test.Fn does not return `error` because a nil *AssertionFailure boxed into
// an error interface compares non-nil, and the case would be reported as
// failed with no failure in it. TestFailure has the same trap, and Failure()
// is what avoids it: it is declared with a nil-receiver answer, so an
// interface value holding a typed nil answers nil and the case passes. A bare
// `!= nil` on the interface cannot make that distinction, which is why runTest
// asks the method instead.
//
// TestTypedNilTestFailureIsAPass is the pin, and it fails on the answer (a case
// reported fail) rather than on a type, so removing the nil-receiver guard is
// visible as wrong output.
type TestFailure interface {
	// Failure is the error the reporter renders, or nil for a pass.
	Failure() error
}

func (e *AssertionFailure) Failure() error {
	if e == nil {
		return nil
	}
	return e
}

func (e *EarlyReturnFailure) Failure() error {
	if e == nil {
		return nil
	}
	return e
}

// AssertionSite is everything about one `assert`/`refute`/`check` that its
// source decides rather than its subject value: where it is, which keyword it
// used, and how its subject expression reads.
//
// The VM fills it in from the assertion instruction, which the IR builder built
// from the AST node, and then runs the judging rules below.
type AssertionSite struct {
	// Line is the absolute source line the report prints.
	Line   int
	Refute bool
	Check  bool
	// Expr is the subject expression as the formatter renders it.
	Expr string
}

// Keyword is the spelling the report shows.
func (s AssertionSite) Keyword() string {
	switch {
	case s.Check:
		return "check"
	case s.Refute:
		return "refute"
	default:
		return "assert"
	}
}

// FailedReason is the reason text for a subject that simply did not hold.
func (s AssertionSite) FailedReason() string {
	if s.Check {
		return "check failed"
	}
	return "assertion failed"
}

// Failure builds the failure value for this site.
func (s AssertionSite) Failure(reason, actual string, binding *AssertionBindingContext, values []AssertionValueContext) *AssertionFailure {
	return &AssertionFailure{
		Line:    s.Line,
		Keyword: s.Keyword(),
		Expr:    s.Expr,
		Actual:  actual,
		Reason:  reason,
		Binding: binding,
		Values:  values,
	}
}

// Holds answers whether this site's keyword is satisfied by a subject that did
// or did not hold on its own terms: `assert` needs the subject, `refute` needs
// its negation.
//
// Extracted so a caller can ask the question before paying to build a report
// it may not need — the same trade ShapeFailure's header describes for
// `actual`, one argument over. JudgeBool and JudgeAssertable both call it, so
// there is still exactly one statement of the rule and a caller that
// pre-checks cannot drift from the judge it then calls.
func (s AssertionSite) Holds(subjectHeld bool) bool { return subjectHeld != s.Refute }

// JudgeBool is the rule for a Bool subject: `assert` needs True, `refute` needs
// False, and a refutation that fails says so in its own words rather than
// reusing "assertion failed".
//
// nil means the assertion held. Returning the failure rather than a bool pair
// is what lets a caller be `if f := site.JudgeBool(...); f != nil { return f }`
// with no second decision of its own.
func (s AssertionSite) JudgeBool(subject bool, binding *AssertionBindingContext, values []AssertionValueContext) *AssertionFailure {
	if s.Holds(subject) {
		return nil
	}
	if s.Refute {
		return s.Failure("refute failed", "", binding, values)
	}
	return s.Failure(s.FailedReason(), "", binding, values)
}

// ShapeFailure is the failure for a `Result` or `Maybe` subject that did not
// hold: `assert` needs the carrying variant (Ok/Some) and `refute` needs the
// other one.
//
// Only the words are here, not the judgement: the caller discriminates the
// variant on its own representation. What is here is the part that has one
// answer: which reason text a failed `assert` and a failed `refute` each use.
// That is the same thing JudgeBool centralises for a Bool subject.
//
// It always returns a failure, and that is deliberate rather than an oversight
// of the JudgeBool shape beside it. `actual` is the subject as an assertion
// report renders it, which a Bool subject does not carry and this one does —
// so a Judge-shaped function taking `actual` eagerly would build and throw away
// a string on every passing shape assertion, and rt's
// TestPassingAssertionAllocatesAtMostOnce counts the allocations on that path.
// Callers therefore make the cheap variant test themselves and come here only
// when they have already lost.
func (s AssertionSite) ShapeFailure(actual string, binding *AssertionBindingContext, values []AssertionValueContext) *AssertionFailure {
	if s.Refute {
		return s.Failure("refute failed", actual, binding, values)
	}
	return s.Failure(s.FailedReason(), actual, binding, values)
}

// Binding is the "defined as:" context for an assertion whose subject is a bare
// name. expr is the defining expression's source text, rendered from the AST
// when the assertion is built; value is the subject's observed value.
func Binding(name, expr, value string) *AssertionBindingContext {
	return &AssertionBindingContext{Name: name, Expr: expr, Value: value}
}

// RecordOperand appends one observed expression/value pair to an assertion's
// value trace, applying the one suppression rule the report has: an operand
// that reads exactly like its value explains nothing, so `assert 1 == 2` does
// not print `1 = 1`.
//
// suppressRedundantLiteral is off for a predicate call whose result is a
// Bool, where the literal inputs are what the reader needs to see.
func RecordOperand(trace *[]AssertionValueContext, expr, value string, suppressRedundantLiteral bool) {
	if suppressRedundantLiteral && expr == value {
		return
	}
	*trace = append(*trace, AssertionValueContext{Expr: expr, Value: value})
}

// How a value reads in an assertion report. Distinct from Format*: a String
// shows its quotes, so `assert name == "ok"` can show `name = "no"` rather than
// an unquoted word that could be mistaken for an identifier.
//
// RowText (render.go) is these functions plus a dispatch over rt's value
// kinds; it calls down here for the scalar rows rather than restating them, so
// the quoting rule has one home.
//
// # Inspect is not `impl Display`, and it is not `Debug` either
//
// The two are different renderings of the same value. A `values:` row is
// RowText, a purely structural rendering with no Nomi dispatch in it at all. A
// user's `impl Display for Point` changes `${p}` and does not change the row,
// which stays `Point{x: 1, y: 2}`; a user's `impl Debug for Point` does not
// change it either.
//
// The consequence for a composite is that the structure is shared with Format*
// and only the element rendering differs: `FormatListCells(xs, InspectString)`
// is exactly a List's Inspect, and `FormatListCells(xs, FormatInt)` is exactly
// its Display. So there is no InspectList and there must not be one — the two
// spellings of `[a, b, c]` would be one rule in two places. What a composite
// needs beyond a renderer argument is below.
func InspectString(v string) string { return `"` + v + `"` }

// InspectUnit is how the one `Unit` value reads in a `values:` row, and it is
// the only spelling of it: RowText calls this. The text is the Unit literal,
// `Unit`, which is also what `Debug.inspect` renders for it.
func InspectUnit(Unit) string { return "Unit" }

// ChannelInspectText is how a `Sender<T>` or a `Receiver<T>` reads in a
// `values:` row, and it is the only spelling of it. The VM's channel halves
// answer it as their OpaqueText, and so do the two typed wrappers below, so no
// report can drift from another.
//
// A constant, not derived from the type's name. The failing assertion
//
//	ch = Channel.buffered<Int>(1)
//	h = Holder{s: ch.sender}
//	assert tag(h) == 2
//
// reports `h = Holder{s: <channel>}`. It is not the host-type bare name: `Sender` and
// `Receiver` are `pub host type` declarations and the auto-Debug for those is
// a bare name, which would give `Sender`. And it does not
// distinguish the two halves — a `Receiver<T>` reads `<channel>` as well —
// because both are one channel at runtime and the direction is purely static.
//
// The element type is absent for InspectSeq's reason, one degree stronger: a
// channel's contents are not merely expensive to read, reading them consumes
// them, so rendering an operand would change the program's behaviour.
func ChannelInspectText() string { return "<channel>" }

// TaskInspectText is how a `Task<T>` reads in a `values:` row, and it is the
// only spelling of it: the VM's task values answer it as their OpaqueText.
//
// A failing assertion over a task operand prints `h = <task>`. The payload is
// absent, and for a reason one step short of the channel's — reading a task's result does not consume it,
// but it does block until the task finishes, so rendering an operand would make
// a failing assertion wait on unrelated work and could deadlock a report on a
// task that never completes.
func TaskInspectText() string { return "<task>" }

// IterInspectText is how an unmaterialized `Iter<T>` (a lazy rt.Seq) reads
// in every rendering: `Debug.inspect`/`io.inspect`/`dbg` and a `values:` row,
// at the top or nested in a container. It is the only spelling of it, and it
// has the form of the other non-structural placeholders (`<function>`,
// `<task>`, `<channel>`, `<context>`).
//
// The elements are deliberately not rendered: that would consume the sequence,
// which for `Iter.from(0)` does not terminate, and infiniteness is undecidable,
// so there is nothing to detect and nothing to try. Inspecting leaves the
// iterator as it was; materialize with `Iter.to_list` to see the elements.
func IterInspectText() string { return "<iter>" }

// FunctionInspectText is the Debug text of a function value, at the top or
// nested in a tuple or record: the name-free `<function>`. Its Display and
// `values:` row name the function instead (Closure's OpaqueText).
func FunctionInspectText() string { return "<function>" }

// InspectSeq is how an `Iter<T>` reads in a `values:` row: IterInspectText.
func InspectSeq[T any](Seq[T]) string { return IterInspectText() }

// InspectStruct is how a struct-shaped value reads in a `values:` row:
// `Point{x: 1, y: 2}`, and `Empty{}` for one with no fields. typeName is the
// declared name with any module qualifier already removed — nothing user-facing
// shows the qualifier (ShortTypeName). A struct-shaped enum variant is the
// same shape under the variant's name: `Rect(Rect{height: "z", width: 1})`.
//
// fields are `name: value` pairs already rendered by the caller. They are
// ordered by field name, which is the order `Debug.inspect` renders an
// anonymous record in. Sorting the whole composed strings instead would put
// `a0: 2` before `a: 1` (the digit sorts before the `:`), so one record would
// read `{a: 1, a0: 2}` from `Debug.inspect` and `{a0: 2, a: 1}` in a `values:` row.
// RowText calls this, so a record has one rule.
//
// The slice is sorted in place. Callers build it for this call and drop it.
func InspectStruct(typeName string, fields []string) string {
	sort.SliceStable(fields, func(i, j int) bool {
		return inspectFieldName(fields[i]) < inspectFieldName(fields[j])
	})
	return typeName + "{" + strings.Join(fields, ", ") + "}"
}

// inspectFieldName is the name half of a rendered `name: value` pair. A field
// name cannot contain `:`, so the first one ends it.
func inspectFieldName(field string) string {
	if i := strings.IndexByte(field, ':'); i >= 0 {
		return field[:i]
	}
	return field
}
