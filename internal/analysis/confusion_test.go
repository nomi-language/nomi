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
			"write `Iter.count(xs)`", "fn f() -> Int\n  xs = [1, 2]\n  Iter.count(xs)\nend\n"},
		{"method call with arguments", "fn f() -> String\n  s = \"a,b\"\n  s.split(\",\") |> String.join(\"-\")\nend\n",
			"write `String.split(s, ...)`", "fn f() -> String\n  s = \"a,b\"\n  String.split(s, \",\") |> String.join(\"-\")\nend\n"},
		{"camelCase method", "fn f() -> String\n  s = \"a\"\n  s.toUpperCase()\nend\n",
			"write `String.uppercase(s)`", "fn f() -> String\n  s = \"a\"\n  String.uppercase(s)\nend\n"},
		{"interface function as a method", "fn f() -> String\n  n = 1\n  n.to_string()\nend\n",
			"write `Int.to_string(n)`", "fn f() -> String\n  n = 1\n  Int.to_string(n)\nend\n"},
		{"bare interface function", "fn f() -> String\n  n = 1\n  to_string(n)\nend\n",
			"`Display.to_string(...)`", "fn f() -> String\n  n = 1\n  Display.to_string(n)\nend\n"},
		{"nil", "fn f() -> Maybe<Int>\n  x: Maybe<Int> = nil\n  x\nend\n",
			"Nomi has no null", "fn f() -> Maybe<Int>\n  x: Maybe<Int> = None\n  x\nend\n"},
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
