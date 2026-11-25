package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/parser"
)

// A call whose Option the value's subtype answers plainly is a hint
// naming the subtype's function. Unwrapped with `!`, its fix calls that
// function and drops the `!`, and the fixed program checks with no hint;
// read as an Option, it has no fix.
func TestPlainCounterpart_HintsTheSubtypesFunction(t *testing.T) {
	const names = "names: NonEmptyList<String>"
	for _, tc := range []struct {
		name, params, body, hint, fixed string
	}{
		{"List.first", names, `List.first(names)!`,
			"`names` is a NonEmptyList: `NonEmptyList.first(names)` is a String, not an Option", `NonEmptyList.first(names)`},
		{"a pipe stage", names, `names |> List.first()!`,
			"`names` is a NonEmptyList: `NonEmptyList.first(names)` is a String, not an Option", `names |> NonEmptyList.first()`},
		{"Iter.max read as an Option", names, `Iter.max(names) |> Option.with_default("")`,
			"`names` is a NonEmptyList: `NonEmptyList.max(names)` is a String, not an Option", ""},
		{"Iter.last of a vector", "v: NonEmptyVector<Int>", `Int.to_string(Iter.last(v)!)`,
			"`v` is a NonEmptyVector: `NonEmptyVector.last(v)` is an Int, not an Option", `Int.to_string(NonEmptyVector.last(v))`},
		{"Iter.min of a set", "", `Int.to_string(Iter.min(NonEmptySet.of(3, Set[1]))!)`,
			"the value is a NonEmptySet: `NonEmptySet.min(...)` is an Int, not an Option",
			`Int.to_string(NonEmptySet.min(NonEmptySet.of(3, Set[1])))`},
		{"Iter.max_by of a vector", "v: NonEmptyVector<String>", `Iter.max_by(v, String.length)!`,
			"`v` is a NonEmptyVector: `NonEmptyVector.max_by(v)` is a String, not an Option", `NonEmptyVector.max_by(v, String.length)`},
		{"Vector.pop of a vector", "v: NonEmptyVector<String>", `Vector.pop(v)!.first`,
			"`v` is a NonEmptyVector: `NonEmptyVector.pop(v)` is (String, Vector<String>), not an Option", `NonEmptyVector.pop(v).first`},
		{"Iter.first of a set", "s: NonEmptySet<String>", `Iter.first(s)!`,
			"`s` is a NonEmptySet: `NonEmptySet.first(s)` is a String, not an Option", `NonEmptySet.first(s)`},
		{"Iter.first of a string", "name: NonEmptyString", `Iter.first(name)!`,
			"`name` is a NonEmptyString: `NonEmptyString.first(name)` is a String, not an Option", `NonEmptyString.first(name)`},
		{"Range.stop of a bounded range", "r: BoundedRange<Int>", `Int.to_string(Range.stop(r)!)`,
			"`r` is a BoundedRange: `BoundedRange.stop(r)` is an Int, not an Option", `Int.to_string(BoundedRange.stop(r))`},
		{"Iter.mean_by read as an Option", names, `Iter.mean_by(names, String.length) |> Option.with_default(0.0) |> Float.to_string()`,
			"`names` is a NonEmptyList: `NonEmptyList.mean_by(names)` is a Float, not an Option", ""},
		{"a split's first piece", "", `List.first(String.split("a b", " "))!`,
			"the value is a NonEmptyList: `NonEmptyList.first(...)` is a String, not an Option", `NonEmptyList.first(String.split("a b", " "))`},
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
					t.Fatalf("a call read as an Option has a fix: %+v", fix)
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

// No hint where the subtype has no plain function of that name and
// arity, or the value is no subtype.
func TestPlainCounterpart_NoHintWithoutACounterpart(t *testing.T) {
	for _, tc := range []struct{ name, params, body string }{
		{"a plain List", "xs: List<String>", `List.first(xs) |> Option.with_default("")`},
		{"Iter.reduce takes a seed", "names: NonEmptyList<String>", `Iter.reduce(names, "", fn acc, n -> acc + n)`},
		{"no function of the name", "names: NonEmptyList<String>", `Iter.find(names, &(&1 == "a")) |> Option.with_default("")`},
		{"a bounded range has no last", "r: BoundedRange<Int>", `Iter.last(r) |> Option.with_default(0) |> Int.to_string()`},
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
