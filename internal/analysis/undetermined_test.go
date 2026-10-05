package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// undeterminedErrors is the "not determined" errors in errs.
func undeterminedErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if strings.Contains(e.Message, "not determined") {
			out = append(out, e)
		}
	}
	return out
}

func describeErrors(errs []analysis.TypeError) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message)
	}
	return strings.Join(msgs, "\n  ")
}

// A generic call whose callee makes values of a type argument that nothing
// fixes is an error at the argument that carries it: the empty literal when
// there is one, as the thing to annotate.
func TestUndeterminedTypeArgs_Rejected(t *testing.T) {
	cases := []struct {
		name, body string
		want       string
		col        int
	}{
		{"collect over an empty list", "dbg Result.collect([])",
			"the element type of `[]` is not determined: annotate it, as in `xs: List<Result<T, E>> = []` with T and E filled in, or pass a typed value", 22},
		{"values over an empty list", "dbg Maybe.values([])",
			"the element type of `[]` is not determined: annotate it, as in `xs: List<Maybe<T>> = []` with T filled in", 20},
		{"to_map over an empty list", "dbg Iter.to_map([])",
			"the element type of `[]` is not determined: annotate it, as in `xs: List<(K, V)> = []` with K and V filled in", 19},
		{"flatten over an empty list", "dbg Iter.flatten([])",
			"the element type of `[]` is not determined", 20},
		{"an empty vector literal", "dbg Maybe.values(#[])",
			"the element type of `#[]` is not determined: annotate it, as in `xs: Vector<Maybe<T>> = #[]`", 20},
		{"a lambda over an empty list's element", "dbg Iter.map([], |x| x)",
			"the element type of `[]` is not determined: annotate it, as in `xs: List<T> = []` with T filled in", 16},
		{"None where T is free", "dbg Maybe.values([None])",
			"the type argument T of `Maybe.values` is not determined: annotate the value passed in, or give the call its type arguments, as in `Maybe.values<T>(…)`", 20},
		{"Err whose Ok type is free", "dbg Result.collect([Err(1)])",
			"the type argument T of `Result.collect` is not determined", 22},
		{"Ok whose error type is collected", "dbg Result.partition([Ok(1)])",
			"the type argument E of `Result.partition` is not determined", 24},
		{"a nested empty list", "dbg Iter.flatten([[]])",
			"the type argument U of `Iter.flatten` is not determined", 20},
		{"a pipe", "dbg [] |> Maybe.values()",
			"the element type of `[]` is not determined", 7},
		{"inside an assert", "assert Iter.count(Maybe.values([])) == 0",
			"the element type of `[]` is not determined", 34},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "test \"t\" {\n  " + tc.body + "\n  assert True\n}\n"
			errs := checkWithStdlib(src)
			got := undeterminedErrors(errs)
			if len(got) != 1 {
				t.Fatalf("want one undetermined error, got:\n  %s", describeErrors(errs))
			}
			if !strings.Contains(got[0].Message, tc.want) {
				t.Fatalf("message = %q, want it to contain %q", got[0].Message, tc.want)
			}
			if got[0].Line != 2 || got[0].Col != tc.col {
				t.Fatalf("at %d:%d, want 2:%d", got[0].Line, got[0].Col, tc.col)
			}
		})
	}
}

// What runs today stays accepted: an unsolved variable no value has, an
// element of an empty collection or the variant a value is not.
func TestUndeterminedTypeArgs_Accepted(t *testing.T) {
	bodies := []string{
		"dbg []",
		"dbg [[]]",
		"dbg ([], 1)",
		"dbg #{}",
		"dbg None",
		"dbg Ok(1)",
		"dbg Err(\"x\")",
		"dbg Some(None)",
		"dbg [None]",
		"dbg [] == []",
		"dbg Ok(1) == Ok(1)",
		"dbg Iter.to_list([])",
		"dbg [] |> Iter.to_list()",
		"dbg Iter.count([])",
		"dbg Iter.count([None])",
		"dbg Iter.to_list([None])",
		"dbg Iter.to_set([])",
		"dbg Iter.to_vector([])",
		"dbg List.head([])",
		"dbg List.head([[]])",
		"dbg Vector.empty()",
		"dbg Vector.length(Vector.empty())",
		"dbg List.concat([], [1])",
		"dbg Result.map(Ok(3), |x| x + 1)",
		"dbg Result.collect([Ok(1)])",
		"dbg Result.collect([Ok(1), Err(\"e\")])",
		"dbg Maybe.values([Some(1)])",
		"dbg Iter.map([], |x| x + 1)",
		"dbg Iter.flatten([[1], []])",
		"dbg Maybe.values<Int>([])",
		"xs: List<Maybe<Int>> = []\n  dbg Maybe.values(xs)",
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			src := "fn main() {\n  " + body + "\n  Unit\n}\n"
			expectNoErrorsT(t, checkWithStdlib(src))
		})
	}
}

// A later statement may fix what a call left open, so the rule is applied
// once every body is checked.
func TestUndeterminedTypeArgs_FixedByALaterUse(t *testing.T) {
	src := `fn main() {
  r = (Maybe.values([None]), 1)
  s: (List<Int>, Int) = r
  dbg s
  Unit
}
`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// A binding that already says its type is not determined is the one error.
func TestUndeterminedTypeArgs_BindingErrorIsNotRepeated(t *testing.T) {
	src := `fn main() {
  xs = Maybe.values([])
  dbg xs
  Unit
}
`
	errs := checkWithStdlib(src)
	expectErrorContaining(t, errs, "binding 'xs' type is not locally determined")
	if got := undeterminedErrors(errs); len(got) != 0 {
		t.Fatalf("the binding error is repeated at the call:\n  %s", describeErrors(got))
	}
}
