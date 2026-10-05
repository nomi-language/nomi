package rt

import "testing"

// The contract, written down as absolute text.
//
// FormatAssertionFailure is the text `AssertionFailure.format(failure)` returns
// to a Nomi program and the text the wasm entry point reports a failed run with.
// Every literal below was captured from a real run.
//
// # Why absolute text
//
// No corpus case fails an assertion, so assertion failure rendering is
// exercised by none of them, and a clean corpus run says nothing about the
// failing path. A renderer that dropped the `details:` block would pass every
// corpus run. Only absolute text proves what the renderer prints, which is why
// the expectations below are spelled out.
//
// One row per shape the renderer has a branch for. Not a sample of plausible
// failures: the header, the two expr-header spellings, the binding block with
// and without pipeline stages, direct values, a struct-valued operand, pipeline
// values, both together, the pipeline compaction rule, an Assertable's details,
// actual, and multi-line source in each position that splits on "\n".
func TestFormatAssertionFailureIsPinned(t *testing.T) {
	cases := []struct {
		name    string
		failure *AssertionFailure
		want    string
	}{{
		// The floor: header plus the assertion as written, keyword prefixed.
		name: "bare bool",
		failure: &AssertionFailure{
			Line: 3, Keyword: "assert", Expr: "False", Reason: "assertion failed",
		},
		want: "line 3: assertion failed\n" +
			"  assert False",
	}, {
		// `refute` says so in its own words, and the reason is the failure's,
		// not the renderer's.
		name: "refute reason",
		failure: &AssertionFailure{
			Line: 9, Keyword: "refute", Expr: "True", Reason: "refutation failed",
		},
		want: "line 9: refutation failed\n" +
			"  refute True",
	}, {
		// The expression already opens with the keyword, so the header must not
		// print it twice. This is the `assert Pattern = value` spelling, whose
		// Expr is rendered from a node carrying the pattern and the value.
		name: "expr already includes the keyword",
		failure: &AssertionFailure{
			Line: 7, Keyword: "assert", Expr: "assert [_, _] = [1]",
			Reason: "pattern did not match", Actual: "[1]",
		},
		want: "line 7: pattern did not match\n" +
			"  assert [_, _] = [1]\n" +
			"  actual: [1]",
	}, {
		// An empty keyword is "already included": nothing to prefix.
		name: "no keyword",
		failure: &AssertionFailure{
			Line: 1, Expr: "check x", Reason: "check failed",
		},
		want: "line 1: check failed\n" +
			"  check x",
	}, {
		// A pipe-stage spelling: the keyword appears as its own `|> assert`
		// line inside a multi-line expression, so it is not prefixed either.
		name: "keyword as a pipe stage line",
		failure: &AssertionFailure{
			Line: 4, Keyword: "assert", Expr: "value\n|> assert", Reason: "assertion failed",
		},
		want: "line 4: assertion failed\n" +
			"  value\n" +
			"  |> assert",
	}, {
		// Every position that shows source splits on "\n" and indents each line
		// to the same column. Here it is the expression header.
		name: "multi-line expression",
		failure: &AssertionFailure{
			Line: 12, Keyword: "check", Expr: "[1, 2, 3]\n|> Iter.any?(|n| n == 4)",
			Reason: "check failed",
		},
		want: "line 12: check failed\n" +
			"  check [1, 2, 3]\n" +
			"  |> Iter.any?(|n| n == 4)",
	}, {
		// A blank line inside the source, such as the one between two case
		// arms, is printed empty rather than as the indent alone.
		name: "blank line in multi-line expression",
		failure: &AssertionFailure{
			Line: 18, Keyword: "assert", Expr: "case n {\n    1 -> True\n\n    _ -> False\n}",
			Reason: "assertion failed",
		},
		want: "line 18: assertion failed\n" +
			"  assert case n {\n" +
			"      1 -> True\n" +
			"\n" +
			"      _ -> False\n" +
			"  }",
	}, {
		// Direct operand rows. Order is the order they were recorded, never
		// sorted: a comparison's left operand is shown before its right.
		name: "direct values",
		failure: &AssertionFailure{
			Line: 14, Keyword: "check", Expr: "actual == expected", Reason: "check failed",
			Values: []AssertionValueContext{
				{Expr: "actual", Value: `"Ada"`},
				{Expr: "expected", Value: `"Grace"`},
			},
		},
		want: "line 14: check failed\n" +
			"  check actual == expected\n" +
			"  values:\n" +
			"    actual\n" +
			`      = "Ada"` + "\n" +
			"    expected\n" +
			`      = "Grace"`,
	}, {
		// A struct-valued operand. The row is RowText, the structural
		// rendering, sorted by field name and not by declaration order, so a
		// `Point` declared `b` before `a` reads
		// with `a` first. The renderer does not compose that string and it must
		// not reformat it either; this row is what would catch one that did.
		name: "struct-valued operands",
		failure: &AssertionFailure{
			Line: 21, Keyword: "check", Expr: "same?(left, right)", Reason: "check failed",
			Values: []AssertionValueContext{
				{Expr: "left", Value: "Point{a: 1, b: 2}"},
				{Expr: "right", Value: "Point{a: 9, b: 2}"},
			},
		},
		want: "line 21: check failed\n" +
			"  check same?(left, right)\n" +
			"  values:\n" +
			"    left\n" +
			"      = Point{a: 1, b: 2}\n" +
			"    right\n" +
			"      = Point{a: 9, b: 2}",
	}, {
		// An operand whose own expression is multi-line.
		name: "multi-line operand expression",
		failure: &AssertionFailure{
			Line: 30, Keyword: "assert", Expr: "wide == 2", Reason: "assertion failed",
			Values: []AssertionValueContext{
				{Expr: "one\ntwo", Value: "1"},
			},
		},
		want: "line 30: assertion failed\n" +
			"  assert wide == 2\n" +
			"  values:\n" +
			"    one\n" +
			"    two\n" +
			"      = 1",
	}, {
		// The binding block with no pipeline: `defined as:` and the defining
		// expression, indented one level deeper than the assertion.
		name: "binding without pipeline",
		failure: &AssertionFailure{
			Line: 40, Keyword: "assert", Expr: "low", Reason: "score too low", Actual: "3",
			Binding: &AssertionBindingContext{Name: "low", Expr: "Score{points: 3}", Value: "Score{points: 3}"},
		},
		want: "line 40: score too low\n" +
			"  assert low\n" +
			"  defined as:\n" +
			"    Score{points: 3}\n" +
			"  actual: 3",
	}, {
		// The binding block with pipeline stages. The stage list is not
		// compacted here — compaction is applied to an observed value's
		// pipeline and not to a binding's, which is a difference between the two
		// blocks rather than an oversight, and this row and the next pin both
		// halves of it.
		name: "binding with pipeline",
		failure: &AssertionFailure{
			Line: 55, Keyword: "check", Expr: "has_four", Reason: "check failed",
			Binding: &AssertionBindingContext{
				Name: "has_four",
				Expr: "[1, 2, 3]\n|> Iter.any?(|n| n == 4)",
				Pipeline: []AssertionPipelineStage{
					{Expr: "[1, 2, 3]", Value: "[1, 2, 3]"},
					{Expr: "[1, 2, 3]\n|> Iter.any?(|n| n == 4)", Value: "False"},
				},
			},
		},
		want: "line 55: check failed\n" +
			"  check has_four\n" +
			"  defined as:\n" +
			"    [1, 2, 3]\n" +
			"    |> Iter.any?(|n| n == 4)\n" +
			"  pipeline values:\n" +
			"    [1, 2, 3]\n" +
			"      = [1, 2, 3]\n" +
			"    [1, 2, 3]\n" +
			"    |> Iter.any?(|n| n == 4)\n" +
			"      = False",
	}, {
		// An observed value's pipeline is compacted: a stage whose expression is
		// the previous stage's plus one `|> ` line shows only the new line, so
		// the block reads as a pipeline rather than repeating its own prefix.
		name: "observed pipeline is compacted",
		failure: &AssertionFailure{
			Line: 73, Keyword: "check", Expr: "[1, 2, 3]\n|> Iter.any?(|n| n == 4)",
			Reason: "check failed",
			Values: []AssertionValueContext{{
				Pipeline: []AssertionPipelineStage{
					{Expr: "[1, 2, 3]", Value: "[1, 2, 3]"},
					{Expr: "[1, 2, 3]\n|> Iter.any?(|n| n == 4)", Value: "False"},
				},
			}},
		},
		want: "line 73: check failed\n" +
			"  check [1, 2, 3]\n" +
			"  |> Iter.any?(|n| n == 4)\n" +
			"  pipeline values:\n" +
			"    [1, 2, 3]\n" +
			"      = [1, 2, 3]\n" +
			"    |> Iter.any?(|n| n == 4)\n" +
			"      = False",
	}, {
		// Compaction is by the immediately preceding stage only, so a stage that
		// is not an extension of its predecessor prints whole.
		name: "pipeline stage that is not an extension",
		failure: &AssertionFailure{
			Line: 80, Keyword: "check", Expr: "x", Reason: "check failed",
			Values: []AssertionValueContext{{
				Pipeline: []AssertionPipelineStage{
					{Expr: "a", Value: "1"},
					{Expr: "b\n|> f()", Value: "2"},
				},
			}},
		},
		want: "line 80: check failed\n" +
			"  check x\n" +
			"  pipeline values:\n" +
			"    a\n" +
			"      = 1\n" +
			"    b\n" +
			"    |> f()\n" +
			"      = 2",
	}, {
		// Both blocks, and the ordering rule between them: every direct row
		// under `values:` first, then every pipeline row under `pipeline
		// values:`, regardless of the order the two kinds were recorded in.
		name: "direct and pipeline values interleaved on input",
		failure: &AssertionFailure{
			Line: 90, Keyword: "assert", Expr: "f(a) == g(b)", Reason: "assertion failed",
			Values: []AssertionValueContext{
				{Expr: "a", Value: "1"},
				{Pipeline: []AssertionPipelineStage{{Expr: "b", Value: "2"}}},
				{Expr: "c", Value: "3"},
			},
		},
		want: "line 90: assertion failed\n" +
			"  assert f(a) == g(b)\n" +
			"  values:\n" +
			"    a\n" +
			"      = 1\n" +
			"    c\n" +
			"      = 3\n" +
			"  pipeline values:\n" +
			"    b\n" +
			"      = 2",
	}, {
		// The details block: an Assertable value's own explanation rows, which
		// is the one block this renderer has and the test report's does not.
		// With a failing `impl Assertable`, `format` prints
		// these rows and `nomi test` drops them. No corpus assertion fails, so
		// this row is what covers the block.
		name: "details",
		failure: &AssertionFailure{
			Line: 31, Keyword: "check", Expr: "low", Reason: "score too low", Actual: "3",
			Binding: &AssertionBindingContext{Name: "low", Expr: "Score{points: 3}", Value: "Score{points: 3}"},
			Details: []AssertionDetailContext{{Label: "threshold", Value: "10"}},
		},
		want: "line 31: score too low\n" +
			"  check low\n" +
			"  defined as:\n" +
			"    Score{points: 3}\n" +
			"  details:\n" +
			"    threshold\n" +
			"      = 10\n" +
			"  actual: 3",
	}, {
		// Every optional block at once, in the order the renderer emits them:
		// binding, binding pipeline, values, pipeline values, details, actual.
		name: "every block",
		failure: &AssertionFailure{
			Line: 100, Keyword: "assert", Expr: "subject", Reason: "everything failed",
			Actual: "Wrong{}",
			Binding: &AssertionBindingContext{
				Name: "subject", Expr: "seed\n|> grow()",
				Pipeline: []AssertionPipelineStage{{Expr: "seed", Value: "0"}},
			},
			Values: []AssertionValueContext{
				{Expr: "lhs", Value: "1"},
				{Pipeline: []AssertionPipelineStage{{Expr: "rhs", Value: "2"}}},
			},
			Details: []AssertionDetailContext{
				{Label: "expected", Value: "Right{}"},
				{Label: "note", Value: "two rows, in order"},
			},
		},
		want: "line 100: everything failed\n" +
			"  assert subject\n" +
			"  defined as:\n" +
			"    seed\n" +
			"    |> grow()\n" +
			"  pipeline values:\n" +
			"    seed\n" +
			"      = 0\n" +
			"  values:\n" +
			"    lhs\n" +
			"      = 1\n" +
			"  pipeline values:\n" +
			"    rhs\n" +
			"      = 2\n" +
			"  details:\n" +
			"    expected\n" +
			"      = Right{}\n" +
			"    note\n" +
			"      = two rows, in order\n" +
			"  actual: Wrong{}",
	}, {
		// An empty Actual is absence, not an empty row. Same rule the Nomi-value
		// conversion applies when it turns "" into `None`.
		name: "empty actual prints no row",
		failure: &AssertionFailure{
			Line: 2, Keyword: "assert", Expr: "x", Reason: "assertion failed", Actual: "",
		},
		want: "line 2: assertion failed\n" +
			"  assert x",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatAssertionFailure(tc.failure); got != tc.want {
				t.Errorf("FormatAssertionFailure mismatch\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}
