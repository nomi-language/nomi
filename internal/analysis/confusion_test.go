package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/parser"
)

// A program written as another language writes it is rejected with an
// error whose hint names Nomi's spelling and whose fix rewrites it into a
// program that checks.
func TestConfusion_CheckerNamesNomisSpelling(t *testing.T) {
	for _, tc := range []struct {
		name, src, hint, fixed string
	}{
		{"method call", "fn f() -> Int\n  xs = [1, 2]\n  xs.len()\nend\n",
			"write `List.length(xs)`", "fn f() -> Int\n  xs = [1, 2]\n  List.length(xs)\nend\n"},
		{"method call with arguments", "fn f() -> String\n  s = \"a,b\"\n  s.split(\",\") |> String.join(\"-\")\nend\n",
			"write `String.split(s, ...)`", "fn f() -> String\n  s = \"a,b\"\n  String.split(s, \",\") |> String.join(\"-\")\nend\n"},
		{"camelCase method", "fn f() -> String\n  s = \"a\"\n  s.toUpperCase()\nend\n",
			"write `String.uppercase(s)`", "fn f() -> String\n  s = \"a\"\n  String.uppercase(s)\nend\n"},
		{"interface function as a method", "fn f() -> String\n  n = 1\n  n.to_string()\nend\n",
			"write `Int.to_string(n)`", "fn f() -> String\n  n = 1\n  Int.to_string(n)\nend\n"},
		{"bare interface function", "fn f() -> String\n  n = 1\n  to_string(n)\nend\n",
			"`Display.to_string(...)`", "fn f() -> String\n  n = 1\n  Display.to_string(n)\nend\n"},
		{"head as a method", "fn f() -> Option<Int>\n  xs = [1, 2]\n  xs.head()\nend\n",
			"write `List.first(xs)`", "fn f() -> Option<Int>\n  xs = [1, 2]\n  List.first(xs)\nend\n"},
		{"List.head", "fn f() -> Option<Int>\n  xs = [1, 2]\n  List.head(xs)\nend\n",
			"did you mean `List.first`?", "fn f() -> Option<Int>\n  xs = [1, 2]\n  List.first(xs)\nend\n"},
		{"nil", "fn f() -> Option<Int>\n  x: Option<Int> = nil\n  x\nend\n",
			"Nomi has no null", "fn f() -> Option<Int>\n  x: Option<Int> = None\n  x\nend\n"},
		{"true", "fn f() -> Bool\n  x: Bool = true\n  x\nend\n",
			"the Bool literal is 'True'", "fn f() -> Bool\n  x: Bool = True\n  x\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			var fix *parser.Fix
			for _, e := range errs {
				if strings.Contains(strings.Join(e.Hints, "\n"), tc.hint) {
					fix = e.Fix
					if fix == nil || len(fix.Edits) == 0 || fix.Title == "" {
						t.Fatalf("error %q carries no titled edit", e.Message)
					}
				}
			}
			if fix == nil {
				expectStdlibError(t, errs, tc.hint)
				return
			}
			got := parser.ApplyEdits(tc.src, fix.Edits)
			if got != tc.fixed {
				t.Fatalf("fixed source:\n%s\nwant:\n%s", got, tc.fixed)
			}
			_, errs = checkSourceWithStdlib(got)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// A misspelled std member is answered with the one the program can name:
// std's functions for a type are reached through the program's impl index,
// not through the file's imports. A List function that is Iter's is
// answered with Iter's.
func TestConfusion_AStdMemberTypoNamesTheMember(t *testing.T) {
	for _, tc := range []struct{ call, want string }{
		{"List.lenght(xs)", "did you mean 'length'?"},
		{"Iter.summ(xs)", "did you mean 'sum'?"},
		{"Iter.coutn(xs)", "did you mean 'count'?"},
		{"List.sum(xs)", "`sum` is an `Iter` function, and works on a List: write `Iter.sum(...)`"},
		{"List.get(xs, 0) |> Option.with_default(0)", "write `Iter.get(...)`"},
	} {
		t.Run(tc.call, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn f(xs: List<Int>) -> Int\n    " + tc.call + "\nend\n")
			for _, e := range errs {
				if strings.Contains(strings.Join(e.Hints, "\n"), tc.want) {
					return
				}
			}
			t.Fatalf("no error says %q: %v", tc.want, errs)
		})
	}
}
