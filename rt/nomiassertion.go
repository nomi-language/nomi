package rt

// The assertion failure as a Nomi value, which is a different thing from the
// assertion failure as a report.
//
// # Why there are two types and not one
//
// rt.AssertionFailure (assertion.go) is the report: `error`-shaped, carrying Go
// slices and a Go pointer, consumed by WriteAssertionFailure and by the
// early-return channel a failing `assert` uses. Nothing user-written ever holds
// one.
//
// std/assertions declares a different type under the same name — an ordinary
// Nomi `pub struct` whose `actual` is `Maybe<String>`, whose `binding` is
// `Maybe<AssertionBinding>` and whose `values` is `List<AssertionValue>`. That
// is what `testing.check` hands back, what a program annotates a binding with,
// and what a field selector reads. Its layout is Nomi's, not the reporter's.
//
// This file is the conversion between the two, against Go types.
//
// Collapsing the two would mean giving the reporter's struct Nomi's field
// shapes — `Maybe[string]` where a `string` is read, `*List[…]` where a slice is
// ranged over — which would rewrite rt/testreport.go and every site that
// builds a failure, to make one type serve two readers with opposite
// convenience. Two types and one conversion is cheaper.
//
// # Why the Nomi-facing names carry a `Nomi` prefix
//
// Two of the five collide with an existing rt name — `AssertionFailure` and
// `AssertionPipelineStage` — and the other three do not
// (`AssertionValueContext`, `AssertionBindingContext`, `AssertionDetailContext`
// are the reporter's spellings). Naming three of them bare and two of them
// differently would make the prefix mean "there happened to be a collision",
// which is not a rule anybody can apply to the sixth. So the whole family
// carries it and the rule is: `Nomi`-prefixed is the type a Nomi program sees.
//
// # The layout is a contract with internal/irbuild
//
// internal/irbuild/stdstruct.go names every field of every type here, in
// declaration order, with the Nomi name it answers to — and refuses to anchor
// anything whose std declaration disagrees. A rename here is a Go compile error
// there (the spec holds reflect.TypeFor of each type), and a reorder or a
// retype produces no anchor, so every mention refuses loudly instead of
// lowering against a layout nobody wrote.

// NomiAssertionPipelineStage is std/assertions' `AssertionPipelineStage`.
type NomiAssertionPipelineStage struct {
	Expression string
	Value      string
}

// NomiAssertionValue is std/assertions' `AssertionValue`.
type NomiAssertionValue struct {
	Expression string
	Value      string
	Pipeline   *List[NomiAssertionPipelineStage]
}

// NomiAssertionBinding is std/assertions' `AssertionBinding`.
type NomiAssertionBinding struct {
	Name       string
	Expression string
	Value      string
	Pipeline   *List[NomiAssertionPipelineStage]
}

// NomiAssertionDetail is std/assertions' `AssertionDetail`.
type NomiAssertionDetail struct {
	Label string
	Value string
}

// NomiAssertionDetails is std/assertions' `AssertionDetails`.
//
// The one type in this family whose std declaration carries field defaults:
//
//	pub struct AssertionDetails {
//	  reason: String = "assertion failed"
//	  actual: Maybe<String> = None
//	  expected: Maybe<String> = None
//	  details: List<AssertionDetail> = []
//	}
//
// The defaults are not here, and that is deliberate rather than an omission.
// They live in internal/irbuild/stdstruct.go's spec row, which internal/irbuild
// applies at each construction site that omits the field, and whose shape check refuses
// to anchor this type at all if std's declared defaults stop being the ones the
// row implements. A second copy here would be a second rule, and the two would
// drift silently — a Go zero value is a plausible wrong answer for three of
// these four fields and a correct one for the fourth.
//
// Only one of the four defaults is the Go zero value: an empty `*List` is nil.
// `Reason` defaults to a non-empty string and `Actual` and `Expected` to
// `None`, whose Tag is TagNone and not 0 — a zero `Maybe` is neither Some nor None and matches no arm
// of a `case`.
type NomiAssertionDetails struct {
	Reason   string
	Actual   Maybe[string]
	Expected Maybe[string]
	Details  *List[NomiAssertionDetail]
}

// NomiAssertionFailure is std/assertions' `AssertionFailure`.
type NomiAssertionFailure struct {
	Line       int64
	Keyword    string
	Expression string
	Reason     string
	Actual     Maybe[string]
	Binding    Maybe[NomiAssertionBinding]
	Values     *List[NomiAssertionValue]
	Details    *List[NomiAssertionDetail]
}

// NomiFailure converts a report into the value a Nomi program sees.
//
// The rules: an empty `Actual` is `None` rather than `Some("")`, a nil binding
// is `None`, and each slice becomes a List in the order it was recorded. A
// second rule anywhere would be a divergence a golden file can only catch on
// the cases a corpus happens to hold.
func (e *AssertionFailure) NomiFailure() NomiAssertionFailure {
	return NomiAssertionFailure{
		Line:       int64(e.Line),
		Keyword:    e.Keyword,
		Expression: e.Expr,
		Reason:     e.Reason,
		Actual:     nomiMaybeString(e.Actual),
		Binding:    nomiMaybeBinding(e.Binding),
		Values:     nomiAssertionValues(e.Values),
		Details:    nomiAssertionDetails(e.Details),
	}
}

// nomiMaybeString reads "" as absence, not an empty string.
func nomiMaybeString(s string) Maybe[string] {
	if s == "" {
		return None[string]()
	}
	return Some(s)
}

func nomiMaybeBinding(b *AssertionBindingContext) Maybe[NomiAssertionBinding] {
	if b == nil {
		return None[NomiAssertionBinding]()
	}
	return Some(NomiAssertionBinding{
		Name:       b.Name,
		Expression: b.Expr,
		Value:      b.Value,
		Pipeline:   nomiAssertionPipeline(b.Pipeline),
	})
}

func nomiAssertionValues(vs []AssertionValueContext) *List[NomiAssertionValue] {
	var out *List[NomiAssertionValue]
	for i := len(vs) - 1; i >= 0; i-- {
		out = Cons(NomiAssertionValue{
			Expression: vs[i].Expr,
			Value:      vs[i].Value,
			Pipeline:   nomiAssertionPipeline(vs[i].Pipeline),
		}, out)
	}
	return out
}

func nomiAssertionPipeline(stages []AssertionPipelineStage) *List[NomiAssertionPipelineStage] {
	var out *List[NomiAssertionPipelineStage]
	for i := len(stages) - 1; i >= 0; i-- {
		out = Cons(NomiAssertionPipelineStage{
			Expression: stages[i].Expr,
			Value:      stages[i].Value,
		}, out)
	}
	return out
}

func nomiAssertionDetails(ds []AssertionDetailContext) *List[NomiAssertionDetail] {
	var out *List[NomiAssertionDetail]
	for i := len(ds) - 1; i >= 0; i-- {
		out = Cons(NomiAssertionDetail{Label: ds[i].Label, Value: ds[i].Value}, out)
	}
	return out
}

// FormatNomiAssertionFailure is std/assertions' `AssertionFailure.format`: the
// report as a String, from the value a Nomi program holds.
//
// It is the one renderer with the Nomi-facing type in front of it, and the two
// halves are separated on purpose. Rendering is FormatAssertionFailure, shared
// with `nomi test`'s report by construction rather than by agreement. Reading
// the Nomi value back into a report is `Report` below, which is a conversion and
// not a second rule.
//
// The registry binds `assertions.AssertionFailure.format` to this function, and
// the binding is checked by reflection against the std declaration: a rename
// here is a Go compile error in internal/irbuild/stdlib.go, and a signature that
// stopped matching `(AssertionFailure) -> String` refuses the call by name
// rather than emitting it.
func FormatNomiAssertionFailure(failure NomiAssertionFailure) string {
	return FormatAssertionFailure(failure.Report())
}

// Report is NomiFailure's inverse: the report a Nomi-held failure describes.
//
// An absent `Maybe` becomes the empty string, which is
// how the report spells absence for `Actual`, and each List becomes a slice in
// its own order. It is the same duality NomiFailure documents: collapsing the
// two types would mean giving the reporter Nomi's field shapes to save this, and
// a conversion is cheaper than one type serving two readers.
//
// A `format` call is the only caller, so it allocates on a path that is about to
// build a whole string anyway.
func (f NomiAssertionFailure) Report() *AssertionFailure {
	return &AssertionFailure{
		Line:    int(f.Line),
		Keyword: f.Keyword,
		Expr:    f.Expression,
		Reason:  f.Reason,
		Actual:  reportMaybeString(f.Actual),
		Binding: reportBinding(f.Binding),
		Values:  reportValues(f.Values),
		Details: reportDetails(f.Details),
	}
}

// reportMaybeString is nomiMaybeString read backwards: `None` is "", which is
// what the renderer already treats as absence.
func reportMaybeString(m Maybe[string]) string {
	if m.Tag != TagSome {
		return ""
	}
	return m.Some
}

func reportBinding(m Maybe[NomiAssertionBinding]) *AssertionBindingContext {
	if m.Tag != TagSome {
		return nil
	}
	return &AssertionBindingContext{
		Name:     m.Some.Name,
		Expr:     m.Some.Expression,
		Value:    m.Some.Value,
		Pipeline: reportPipeline(m.Some.Pipeline),
	}
}

func reportValues(xs *List[NomiAssertionValue]) []AssertionValueContext {
	if xs == nil {
		return nil
	}
	out := make([]AssertionValueContext, 0, xs.Len)
	for cell := xs; cell != nil; cell = cell.Tail {
		out = append(out, AssertionValueContext{
			Expr:     cell.Head.Expression,
			Value:    cell.Head.Value,
			Pipeline: reportPipeline(cell.Head.Pipeline),
		})
	}
	return out
}

func reportPipeline(xs *List[NomiAssertionPipelineStage]) []AssertionPipelineStage {
	if xs == nil {
		return nil
	}
	out := make([]AssertionPipelineStage, 0, xs.Len)
	for cell := xs; cell != nil; cell = cell.Tail {
		out = append(out, AssertionPipelineStage{Expr: cell.Head.Expression, Value: cell.Head.Value})
	}
	return out
}

func reportDetails(xs *List[NomiAssertionDetail]) []AssertionDetailContext {
	if xs == nil {
		return nil
	}
	out := make([]AssertionDetailContext, 0, xs.Len)
	for cell := xs; cell != nil; cell = cell.Tail {
		out = append(out, AssertionDetailContext{Label: cell.Head.Label, Value: cell.Head.Value})
	}
	return out
}
