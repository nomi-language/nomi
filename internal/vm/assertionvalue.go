package vm

// std/assertions' `AssertionFailure` as a machine value.
//
// `testing.check` answers the failure as data (`checkResult`), through rt's
// `NomiAssertionFailure`, so the rules for turning a report into a Nomi value
// (an empty `actual` is `None`, rows in recorded order) are
// `rt.AssertionFailure.NomiFailure`'s. What is here is the field-by-field
// copy from rt's Go structs into records, which a program reads by field
// name. The way back, `AssertionFailure.format`, is a generated adapter over
// `rt.FormatNomiAssertionFailure` (internal/stdlibadapters).

import (
	"github.com/nomi-language/nomi/rt"
)

func stringFields(names ...string) []rt.FieldSpec {
	out := make([]rt.FieldSpec, len(names))
	for i, n := range names {
		out[i] = rt.FieldSpec{Name: n, Type: rt.SlotString}
	}
	return out
}

var (
	assertionFailureDesc = structDesc("assertions.AssertionFailure", []rt.FieldSpec{
		{Name: "line", Type: rt.SlotInt}, {Name: "keyword", Type: rt.SlotString},
		{Name: "expression", Type: rt.SlotString}, {Name: "reason", Type: rt.SlotString},
		{Name: "actual", Type: rt.SlotRef}, {Name: "binding", Type: rt.SlotRef},
		{Name: "values", Type: rt.SlotRef}, {Name: "details", Type: rt.SlotRef}})
	assertionBindingDesc = structDesc("assertions.AssertionBinding", append(stringFields("name", "expression", "value"),
		rt.FieldSpec{Name: "pipeline", Type: rt.SlotRef}))
	assertionValueDesc = structDesc("assertions.AssertionValue", append(stringFields("expression", "value"),
		rt.FieldSpec{Name: "pipeline", Type: rt.SlotRef}))
	assertionDetailDesc = structDesc("assertions.AssertionDetail", stringFields("label", "value"))
	assertionStageDesc  = structDesc("assertions.AssertionPipelineStage", stringFields("expression", "value"))
)

// checkResult is `testing.check`'s answer: `Ok(subject)` or
// `Err(AssertionFailure)`.
func checkResult(subject any, f *rt.AssertionFailure) any {
	if f == nil {
		return okValue(subject)
	}
	return errValue(nomiFailureValue(f.NomiFailure()))
}

func nomiFailureValue(f rt.NomiAssertionFailure) any {
	actual := noneValue
	if f.Actual.Tag == rt.TagSome {
		actual = some(f.Actual.Some)
	}
	binding := noneValue
	if f.Binding.Tag == rt.TagSome {
		b := f.Binding.Some
		binding = some(assertionBindingDesc.Make(b.Name, b.Expression, b.Value, pipelineValue(b.Pipeline)))
	}
	var values []any
	for _, v := range rtListSlice(f.Values) {
		values = append(values, assertionValueDesc.Make(v.Expression, v.Value, pipelineValue(v.Pipeline)))
	}
	var details []any
	for _, d := range rtListSlice(f.Details) {
		details = append(details, assertionDetailDesc.Make(d.Label, d.Value))
	}
	return assertionFailureDesc.Make(f.Line, f.Keyword, f.Expression, f.Reason, actual, binding,
		listOf(values), listOf(details))
}

// nomiFailureOf reads an `AssertionFailure` record back into rt's Nomi shape,
// nomiFailureValue read backwards. ok is false for any other value.
func nomiFailureOf(v any) (rt.NomiAssertionFailure, bool) {
	r, ok := structRecord(v, "assertions.AssertionFailure")
	if !ok {
		return rt.NomiAssertionFailure{}, false
	}
	field := func(name string) any {
		x, _ := r.FieldNamed(name)
		return x
	}
	str := func(x any) string {
		s, _ := x.(string)
		return s
	}
	maybeStr := func(x any) rt.Maybe[string] {
		if p, some, _ := maybeParts(x); some {
			return rt.Some(str(p))
		}
		return rt.Maybe[string]{}
	}
	stages := func(x any) *rt.List[rt.NomiAssertionPipelineStage] {
		xs, _ := x.(*list)
		var out []rt.NomiAssertionPipelineStage
		for _, item := range listSlice(xs) {
			if sr, ok := structRecord(item, "assertions.AssertionPipelineStage"); ok {
				e, _ := sr.FieldNamed("expression")
				val, _ := sr.FieldNamed("value")
				out = append(out, rt.NomiAssertionPipelineStage{Expression: str(e), Value: str(val)})
			}
		}
		return rtSliceList(out)
	}
	line, _ := field("line").(int64)
	f := rt.NomiAssertionFailure{
		Line: line, Keyword: str(field("keyword")), Expression: str(field("expression")),
		Reason: str(field("reason")), Actual: maybeStr(field("actual")),
	}
	if p, some, _ := maybeParts(field("binding")); some {
		if br, ok := structRecord(p, "assertions.AssertionBinding"); ok {
			n, _ := br.FieldNamed("name")
			e, _ := br.FieldNamed("expression")
			val, _ := br.FieldNamed("value")
			pl, _ := br.FieldNamed("pipeline")
			f.Binding = rt.Some(rt.NomiAssertionBinding{Name: str(n), Expression: str(e), Value: str(val), Pipeline: stages(pl)})
		}
	}
	values, _ := field("values").(*list)
	var vs []rt.NomiAssertionValue
	for _, item := range listSlice(values) {
		if vr, ok := structRecord(item, "assertions.AssertionValue"); ok {
			e, _ := vr.FieldNamed("expression")
			val, _ := vr.FieldNamed("value")
			pl, _ := vr.FieldNamed("pipeline")
			vs = append(vs, rt.NomiAssertionValue{Expression: str(e), Value: str(val), Pipeline: stages(pl)})
		}
	}
	f.Values = rtSliceList(vs)
	details, _ := field("details").(*list)
	var ds []rt.NomiAssertionDetail
	for _, item := range listSlice(details) {
		if dr, ok := structRecord(item, "assertions.AssertionDetail"); ok {
			l, _ := dr.FieldNamed("label")
			val, _ := dr.FieldNamed("value")
			ds = append(ds, rt.NomiAssertionDetail{Label: str(l), Value: str(val)})
		}
	}
	f.Details = rtSliceList(ds)
	return f, true
}

func pipelineValue(stages *rt.List[rt.NomiAssertionPipelineStage]) any {
	var out []any
	for _, st := range rtListSlice(stages) {
		out = append(out, assertionStageDesc.Make(st.Expression, st.Value))
	}
	return listOf(out)
}

func rtListSlice[T any](xs *rt.List[T]) []T {
	var out []T
	for ; xs != nil; xs = xs.Tail {
		out = append(out, xs.Head)
	}
	return out
}

func rtSliceList[T any](xs []T) *rt.List[T] {
	var out *rt.List[T]
	for i := len(xs) - 1; i >= 0; i-- {
		out = rt.Cons(xs[i], out)
	}
	return out
}
