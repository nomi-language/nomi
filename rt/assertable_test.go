package rt

import (
	"reflect"
	"testing"
)

// The assertable rule, written down as absolute values.
//
// AssertionSite.JudgeAssertable is the one implementation of "what does an
// `Assertable` subject's answer mean". Every expectation below was captured
// by running internal/irbuild/testdata/assertable_subject.nomi and
// assertable_subject_report.nomi under `nomi test`.
//
// An absolute table is what can catch this function changing. Two callers of
// one function agree byte for byte however wrong that function is, so a test
// that compares them cannot see a change to the function they share.
//
// The test corpus cannot check this rule. Every corpus case passes, so its
// golden records hold only the stdout of passing runs, and an Assertable's
// contribution to a report (its authored `reason`, its `details:` rows, its
// `actual`) exists only on the failing path.
//
// One row per branch, not a sample: `assert` and `refute` against each answer,
// the three sources a `reason` can come from, and `actual` present and absent.
func TestJudgeAssertableIsPinned(t *testing.T) {
	authored := &AssertableDetails{
		Reason: "values were not equal",
		Actual: "41",
		Details: []AssertionDetailContext{
			{Label: "actual", Value: "41"},
			{Label: "expected", Value: "42"},
		},
	}
	cases := []struct {
		name   string
		site   AssertionSite
		answer *AssertableDetails
		want   *AssertionFailure
	}{{
		// `assert` holds on no answer. The polarity is the opposite of a
		// `Maybe` subject's, where `assert` needs `Some` — inverting it is
		// invisible to any comparison of passing runs, which is every
		// comparison this repository's corpus can make.
		name:   "assert holds when the subject answered nothing",
		site:   AssertionSite{Line: 110, Expr: "equals(7, 7)"},
		answer: nil,
		want:   nil,
	}, {
		// The mirror. `refute equals(1, 2)` holds because the subject did
		// report a failure.
		name:   "refute holds when the subject answered details",
		site:   AssertionSite{Line: 117, Refute: true, Expr: "equals(1, 2)"},
		answer: authored,
		want:   nil,
	}, {
		// The authored reason wins over the site's own words, and `check` is
		// where that is observable: a site-first rule would print
		// `check failed` here.
		name:   "an authored reason wins over the site, even for a check",
		site:   AssertionSite{Line: 141, Check: true, Expr: "equals(x, y)"},
		answer: authored,
		want: &AssertionFailure{
			Line: 141, Keyword: "check", Expr: "equals(x, y)",
			Reason: "values were not equal", Actual: "41",
			Details: []AssertionDetailContext{
				{Label: "actual", Value: "41"},
				{Label: "expected", Value: "42"},
			},
		},
	}, {
		// std declares `reason: String = "assertion failed"`, so an Assertable
		// that omits the field answers that string and it arrives here intact.
		// Under a check it is therefore `assertion failed` and not
		// `check failed`, and this is the one row that
		// distinguishes std's default from the site's fallback.
		name:   "std's declared default arrives as an authored reason",
		site:   AssertionSite{Line: 160, Check: true, Expr: "Defaulted{ok: False}"},
		answer: &AssertableDetails{Reason: "assertion failed"},
		want: &AssertionFailure{
			Line: 160, Keyword: "check", Expr: "Defaulted{ok: False}",
			Reason: "assertion failed",
		},
	}, {
		// An explicitly empty reason is the only route to FailedReason, and the
		// only case where the keyword decides the words.
		name:   "an empty reason falls through to the check's own words",
		site:   AssertionSite{Line: 172, Check: true, Expr: "Silent{ok: False}"},
		answer: &AssertableDetails{Reason: ""},
		want: &AssertionFailure{
			Line: 172, Keyword: "check", Expr: "Silent{ok: False}",
			Reason: "check failed",
		},
	}, {
		name:   "the same empty reason under an assert",
		site:   AssertionSite{Line: 101, Expr: "Silent{ok: False}"},
		answer: &AssertableDetails{Reason: ""},
		want: &AssertionFailure{
			Line: 101, Keyword: "assert", Expr: "Silent{ok: False}",
			Reason: "assertion failed",
		},
	}, {
		// A failing `refute` carries no details and cannot: the answer that
		// made it fail was absence. So it says so in its own words and its
		// `actual` is empty, which the renderer treats as no row at all.
		name:   "a failing refutation says so in its own words and reads no details",
		site:   AssertionSite{Line: 106, Refute: true, Expr: "equals(3, 3)"},
		answer: nil,
		want: &AssertionFailure{
			Line: 106, Keyword: "refute", Expr: "equals(3, 3)",
			Reason: "refute failed",
		},
	}, {
		// The binding and value context are carried through untouched. Passed
		// here rather than assumed, because the Assertable arm is the one arm
		// that builds a failure from a value the subject supplied, and dropping
		// the site's own context while copying the subject's is the plausible
		// mistake.
		name: "the site's binding and observed values survive",
		site: AssertionSite{Line: 196, Check: true, Expr: "comparison"},
		answer: &AssertableDetails{
			Reason: "values were not equal", Actual: "5",
		},
		want: &AssertionFailure{
			Line: 196, Keyword: "check", Expr: "comparison",
			Reason: "values were not equal", Actual: "5",
		},
	}}

	binding := &AssertionBindingContext{
		Name: "comparison", Expr: "equals(5, 6)", Value: "Equals{actual: 5, expected: 6}",
	}
	values := []AssertionValueContext{{Expr: "x", Value: "41"}, {Expr: "y", Value: "42"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Only the last row asserts about them, so the other rows carry
			// them too and prove they are not consulted by the judgement.
			var b *AssertionBindingContext
			var v []AssertionValueContext
			if c.name == "the site's binding and observed values survive" {
				b, v = binding, values
			}
			got := c.site.JudgeAssertable(c.answer, b, v)
			if c.want == nil {
				if got != nil {
					t.Fatalf("the assertion held, so no failure was wanted; got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want a failure, got nil: the assertion was judged to hold")
			}
			want := *c.want
			want.Binding, want.Values = b, v
			if !reflect.DeepEqual(*got, want) {
				t.Errorf("failure is not the pinned value.\n got %+v\nwant %+v", *got, want)
			}
		})
	}
}

// TestAssertableDetailsOfIsPinned pins the boundary conversion: the
// `Maybe<AssertionDetails>` a lowered `Assertable.failure` answers, read into
// the report's shapes.
//
// A zero `Maybe` is neither Some nor None — its Tag is 0 where TagNone is 2 —
// and it is included because it is the one input on which a tag test written the
// other way round (`!= TagNone`) reports a failure nobody asked for. std's
// `actual: Maybe<String> = None` default is exactly the field where that
// mistake would be reachable from ordinary source.
func TestAssertableDetailsOfIsPinned(t *testing.T) {
	if got := AssertableDetailsOf(None[NomiAssertionDetails]()); got != nil {
		t.Errorf("None must be absence, got %+v", got)
	}
	if got := AssertableDetailsOf(Maybe[NomiAssertionDetails]{}); got != nil {
		t.Errorf("a zero Maybe is not Some, got %+v", got)
	}
	rows := Cons(NomiAssertionDetail{Label: "actual", Value: "41"},
		Cons(NomiAssertionDetail{Label: "expected", Value: "42"}, nil))
	got := AssertableDetailsOf(Some(NomiAssertionDetails{
		Reason:  "values were not equal",
		Actual:  Some("41"),
		Details: rows,
	}))
	want := &AssertableDetails{
		Reason: "values were not equal",
		Actual: "41",
		Details: []AssertionDetailContext{
			{Label: "actual", Value: "41"},
			{Label: "expected", Value: "42"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("conversion is not the pinned value.\n got %+v\nwant %+v", got, want)
	}
	// `None` becomes "" and not `Some("")`, which is the rule nomiMaybeString
	// states in the other direction and the rule the renderer relies on to
	// print no `actual:` row at all.
	empty := AssertableDetailsOf(Some(NomiAssertionDetails{Reason: "r", Actual: None[string]()}))
	if empty.Actual != "" {
		t.Errorf("an absent actual must read as empty, got %q", empty.Actual)
	}
}

// TestAssertableReportIsPinned is the rendered text, captured by
// running
// internal/irbuild/testdata/assertable_subject_report.nomi under `nomi test`.
//
// The fields above and the text here are two independent claims. A judgement
// that built the right failure and a renderer that dropped its `details:` block
// would pass the first and fail this one.
func TestAssertableReportIsPinned(t *testing.T) {
	site := AssertionSite{Line: 66, Expr: "assert equals(left, right)"}
	failure := site.JudgeAssertable(&AssertableDetails{
		Reason: "values were not equal",
		Actual: "41",
		Details: []AssertionDetailContext{
			{Label: "actual", Value: "41"},
			{Label: "expected", Value: "42"},
		},
	}, nil, []AssertionValueContext{{Expr: "left", Value: "41"}, {Expr: "right", Value: "42"}})
	want := "line 66: values were not equal\n" +
		"  assert equals(left, right)\n" +
		"  values:\n" +
		"    left\n" +
		"      = 41\n" +
		"    right\n" +
		"      = 42\n" +
		"  details:\n" +
		"    actual\n" +
		"      = 41\n" +
		"    expected\n" +
		"      = 42\n" +
		"  actual: 41"
	if got := FormatAssertionFailure(failure); got != want {
		t.Errorf("authored report is not the pinned text.\n got %q\nwant %q", got, want)
	}

	refuted := AssertionSite{Line: 70, Refute: true, Expr: "equals(9, 9)"}.
		JudgeAssertable(nil, nil, nil)
	wantRefuted := "line 70: refute failed\n" +
		"  refute equals(9, 9)"
	if got := FormatAssertionFailure(refuted); got != wantRefuted {
		t.Errorf("refutation report is not the pinned text.\n got %q\nwant %q", got, wantRefuted)
	}
}
