package rt

import (
	"context"
	"testing"
)

// What threading a Frame costs, and what an assertion costs.
//
// The frame design's one hard constraint is that threading `fr` must not
// allocate per call. Which operands an assertion records is decided when the
// IR is built (see internal/irbuild/tests.go), so the frame carries no trace and
// nothing allocates one per call. These are the numbers behind that claim.

// callDepth3 is a call chain where every function takes the frame first and
// passes the same pointer down.
func callDepth3(fr *Frame, n int64) int64 { return callDepth2(fr, n) + 1 }
func callDepth2(fr *Frame, n int64) int64 { return callDepth1(fr, n) + 1 }
func callDepth1(fr *Frame, n int64) int64 { return n + 1 }

func TestFrameThreadingAllocatesNothing(t *testing.T) {
	fr := NewFrame(context.Background())
	got := testing.AllocsPerRun(1000, func() {
		if callDepth3(fr, 1) != 4 {
			t.Fatal("wrong answer")
		}
	})
	if got != 0 {
		t.Fatalf("threading a Frame through 3 calls allocated %v times per run, want 0", got)
	}
}

// TestPassingAssertionAllocatesAtMostOnce is the measurement that decides
// whether the trace is affordable where it sits.
//
// The trace is a plain local slice appended to as the subject evaluates. A
// passing assertion therefore does the recording work and throws it away, and
// this test counts what that costs.
//
// One allocation, and only because the slice is declared with the exact
// capacity the subject needs. An ungrown nil slice costs two to reach two
// operands; a subject with nothing to record costs none.
//
// Reaching zero would need the recording moved into the failure branch and the
// trace reconstructed from the operand locals. That is a real change, not a
// tidy-up: `and`/`or` evaluate their right operand inside a short-circuit block, so
// its temporaries would have to be hoisted out to be nameable at the failure
// point. Not made, because one allocation on the test path buys nothing back —
// but this test is where the number lives if that changes.
func TestPassingAssertionAllocatesAtMostOnce(t *testing.T) {
	site := AssertionSite{Line: 7, Expr: "a == b"}
	a, b := int64(2), int64(2)
	got := testing.AllocsPerRun(1000, func() {
		trace := make([]AssertionValueContext, 0, 2)
		RecordOperand(&trace, "a", InspectInt(a), true)
		RecordOperand(&trace, "b", InspectInt(b), true)
		if site.JudgeBool(a == b, nil, trace) != nil {
			t.Fatal("the assertion should hold")
		}
	})
	if got > 1 {
		t.Fatalf("a passing assertion allocated %v times per run, want at most 1", got)
	}
	noOperands := testing.AllocsPerRun(1000, func() {
		var trace []AssertionValueContext
		if site.JudgeBool(a == b, nil, trace) != nil {
			t.Fatal("the assertion should hold")
		}
	})
	if noOperands != 0 {
		t.Fatalf("an assertion with nothing to record allocated %v times per run, want 0", noOperands)
	}
}

// TestJudgeBoolIsTheOneRule pins the four answers `assert` and `refute` give,
// because every Bool assertion reaches them through here.
func TestJudgeBoolIsTheOneRule(t *testing.T) {
	assert := AssertionSite{Line: 1, Expr: "x"}
	refute := AssertionSite{Line: 1, Expr: "x", Refute: true}

	if f := assert.JudgeBool(true, nil, nil); f != nil {
		t.Fatalf("assert on True failed: %v", f)
	}
	f := assert.JudgeBool(false, nil, nil)
	if f == nil {
		t.Fatal("assert on False held")
	}
	if f.Reason != "assertion failed" || f.Keyword != "assert" || f.Line != 1 {
		t.Fatalf("assert failure is %+v", f)
	}
	if f := refute.JudgeBool(false, nil, nil); f != nil {
		t.Fatalf("refute on False failed: %v", f)
	}
	f = refute.JudgeBool(true, nil, nil)
	if f == nil {
		t.Fatal("refute on True held")
	}
	// Not "assertion failed": a reader of a failing refutation is being told
	// the opposite thing, and the two spellings are what say which.
	if f.Reason != "refute failed" || f.Keyword != "refute" {
		t.Fatalf("refute failure is %+v", f)
	}
	if f.Error() != "line 1: refute failed" {
		t.Fatalf("header is %q", f.Error())
	}
}

// TestRecordOperandSuppressesRedundantRows pins the rule that decides whether a
// `values:` row appears at all. `assert 1 == 2` printing `1 = 1` would be noise,
// and a predicate's literal argument printing `5 = 5` is the point — which is
// why the suppression is a parameter rather than a fixed rule.
func TestRecordOperandSuppressesRedundantRows(t *testing.T) {
	var trace []AssertionValueContext
	RecordOperand(&trace, "1", "1", true)
	if len(trace) != 0 {
		t.Fatalf("a redundant row survived: %+v", trace)
	}
	RecordOperand(&trace, "5", "5", false)
	if len(trace) != 1 || trace[0].Expr != "5" || trace[0].Value != "5" {
		t.Fatalf("a predicate's literal argument was dropped: %+v", trace)
	}
	RecordOperand(&trace, "double(3)", "6", true)
	if len(trace) != 2 || trace[1].Value != "6" {
		t.Fatalf("an informative row was dropped: %+v", trace)
	}
}

// TestInspectStringQuotes pins the one rendering rule rt owns for assertion
// reports that Format* does not: a String shows its quotes, so a failing
// comparison cannot be misread as being about an identifier. RowText calls
// this rather than restating it.
func TestInspectStringQuotes(t *testing.T) {
	if got := InspectString("ok"); got != `"ok"` {
		t.Fatalf("InspectString(%q) = %q", "ok", got)
	}
}

// TestInspectStructIsValueStructValDisplay spells the expected bytes out, so
// the struct row's ordering rule is pinned as absolute values.
func TestInspectStructIsValueStructValDisplay(t *testing.T) {
	cases := []struct {
		name   string
		typ    string
		fields []string
		want   string
	}{
		{"two fields", "Point", []string{"x: 1", `y: "two"`}, `Point{x: 1, y: "two"}`},
		// Sorted by field name. Declaration order is not preserved and must
		// not be.
		{"out of order", "Point", []string{`y: "two"`, "x: 1"}, `Point{x: 1, y: "two"}`},
		// By the name, not the composed string: `a0: 2` sorts before `a: 1`
		// as a string (`0` < `:`), and Debug.inspect renders `a` first.
		{"name is a prefix of another", "", []string{"a0: 2", "a: 1"}, "{a: 1, a0: 2}"},
		{"prefix name, values reversed", "", []string{"ab: 1", "a: 9"}, "{a: 9, ab: 1}"},
		{"no fields", "Nothing", nil, "Nothing{}"},
		{"nested", "Outer", []string{"tag: 9", "p: Point{x: 1}"}, "Outer{p: Point{x: 1}, tag: 9}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InspectStruct(tc.typ, tc.fields); got != tc.want {
				t.Fatalf("InspectStruct(%q, %q) = %q, want %q", tc.typ, tc.fields, got, tc.want)
			}
		})
	}
}

// TestFormatListAndMapServeBothRenderings is the evidence for there being no
// InspectList and no InspectMap: one function, two answers, chosen entirely by
// the renderer argument. If these ever stopped differing, a `values:` row for a
// List<String> would silently lose its quotes, and only a failing assertion
// would show it.
func TestFormatListAndMapServeBothRenderings(t *testing.T) {
	xs := Cons("file.txt", Cons("b", nil))
	if got := FormatList(xs, FormatString); got != "[file.txt, b]" {
		t.Fatalf("Display rendering = %q", got)
	}
	if got := FormatList(xs, InspectString); got != `["file.txt", "b"]` {
		t.Fatalf("Inspect rendering = %q", got)
	}

	var empty Map[string, int64]
	m := MapPut(empty, HashString, Eq[string], "a", int64(1))
	if got := FormatMap(m, FormatString, FormatInt); got != "{a => 1}" {
		t.Fatalf("Display rendering = %q", got)
	}
	if got := FormatMap(m, InspectString, InspectInt); got != `{"a" => 1}` {
		t.Fatalf("Inspect rendering = %q", got)
	}
}

// TestCompositeOperandAllocatesOncePerContainer states the cost a composite
// operand adds, because the scalar numbers above are the ones people quote and
// this one is different: a scalar operand allocates nothing to render
// and a container allocates its rendered string, on every assertion rather than
// only a failing one. It is a cost, and the test path is where it lands.
func TestCompositeOperandAllocatesOncePerContainer(t *testing.T) {
	xs := Cons(int64(1), Cons(int64(2), Cons(int64(3), nil)))
	scalar := testing.AllocsPerRun(1000, func() {
		if InspectInt(3) == "" {
			t.Fatal("no")
		}
	})
	if scalar != 0 {
		t.Fatalf("a scalar operand allocated %v times per run, want 0", scalar)
	}
	list := testing.AllocsPerRun(1000, func() {
		if FormatList(xs, InspectInt) == "" {
			t.Fatal("no")
		}
	})
	t.Logf("a 3-element List<Int> operand: %v allocation(s) per render", list)
	if list > 4 {
		t.Fatalf("rendering a 3-element list allocated %v times per run; the "+
			"builder should not be growing once per element", list)
	}
}
