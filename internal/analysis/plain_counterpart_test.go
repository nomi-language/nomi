package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/parser"
)

// A call whose Maybe the value's refinement answers plainly is a hint
// naming the refinement's function. Unwrapped with `!`, its fix calls that
// function and drops the `!`, and the fixed program checks with no hint;
// read as a Maybe, it has no fix.
func TestPlainCounterpart_HintsTheRefinementsFunction(t *testing.T) {
	const names = "names: NonEmptyList<String>"
	for _, tc := range []struct {
		name, params, body, hint, fixed string
	}{
		{"List.first", names, `List.first(names)!`,
			"`names` is a NonEmptyList: `NonEmptyList.first(names)` is a String, not a Maybe", `NonEmptyList.first(names)`},
		{"a pipe stage", names, `names |> List.first()!`,
			"`names` is a NonEmptyList: `NonEmptyList.first(names)` is a String, not a Maybe", `names |> NonEmptyList.first()`},
		{"Iter.max read as a Maybe", names, `Iter.max(names) |> Maybe.with_default("")`,
			"`names` is a NonEmptyList: `NonEmptyList.max(names)` is a String, not a Maybe", ""},
		{"Iter.last of a vector", "v: NonEmptyVector<Int>", `Int.to_string(Iter.last(v)!)`,
			"`v` is a NonEmptyVector: `NonEmptyVector.last(v)` is an Int, not a Maybe", `Int.to_string(NonEmptyVector.last(v))`},
		{"Iter.min of a set", "", `Int.to_string(Iter.min(NonEmptySet.of(3, Set[1]))!)`,
			"the value is a NonEmptySet: `NonEmptySet.min(...)` is an Int, not a Maybe",
			`Int.to_string(NonEmptySet.min(NonEmptySet.of(3, Set[1])))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn f(" + tc.params + ") -> String\n    " + tc.body + "\nend\n"
			fa, nodes, errs := checkSourceNodesWithStdlib(src)
			if len(errs) > 0 {
				t.Fatalf("the source does not check: %v", errs)
			}
			hints := analysis.FileHints(fa, nodes)
			if len(hints) != 1 || hints[0].Message != tc.hint || hints[0].Code != analysis.HintPlainCounterpart {
				t.Fatalf("hints = %+v, want one: %q", hints, tc.hint)
			}
			fix := hints[0].Fix
			if tc.fixed == "" {
				if fix != nil {
					t.Fatalf("a call read as a Maybe has a fix: %+v", fix)
				}
				return
			}
			if fix == nil || fix.Title == "" {
				t.Fatalf("the hint carries no titled fix")
			}
			full := src
			got := parser.ApplyEdits(full, fix.Edits)
			if want := strings.Replace(full, tc.body, tc.fixed, 1); got != want {
				t.Fatalf("fixed source:\n%s\nwant:\n%s", got, want)
			}
			fa, nodes, errs = checkSourceNodesWithStdlib(strings.Replace(src, tc.body, tc.fixed, 1))
			expectNoStdlibErrors(t, errs)
			if hints := analysis.FileHints(fa, nodes); len(hints) > 0 {
				t.Fatalf("the fixed program still has hints: %+v", hints)
			}
		})
	}
}

// No hint where the refinement has no plain function of that name and
// arity, or the value is no refinement.
func TestPlainCounterpart_NoHintWithoutACounterpart(t *testing.T) {
	for _, tc := range []struct{ name, params, body string }{
		{"a plain List", "xs: List<String>", `List.first(xs) |> Maybe.with_default("")`},
		{"Iter.reduce takes a seed", "names: NonEmptyList<String>", `Iter.reduce(names, "", fn acc, n -> acc + n)`},
		{"no function of the name", "names: NonEmptyList<String>", `Iter.find(names, &(&1 == "a")) |> Maybe.with_default("")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fa, nodes, errs := checkSourceNodesWithStdlib("fn f(" + tc.params + ") -> String\n    " + tc.body + "\nend\n")
			if len(errs) > 0 {
				t.Fatalf("the source does not check: %v", errs)
			}
			if hints := analysis.FileHints(fa, nodes); len(hints) > 0 {
				t.Fatalf("hints = %+v, want none", hints)
			}
		})
	}
}
