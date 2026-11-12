package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// An arm body below its `->` runs to the next arm or `end`; a one-line arm
// and an arm whose next line is another arm, however indented, are arms; a
// line that starts with `|>` continues the arm's expression; and a lambda
// below its `->` runs to `end`.
func TestArmAndLambdaBodies_Shapes(t *testing.T) {
	for _, src := range []string{
		`fn f(r: Result<Int, String>) -> Int
    case r
        Ok(n) ->
            io.print("got ${n}")
            n
        Err(msg) -> 0
    end
end
`,
		`fn f(r: Result<Int, String>) -> Int
    case r
        Ok(n) ->
            n
        Err(msg) -> 0
    end
end
`,
		`fn f(x: Int) -> Int
    case x
        0 -> 1
            1 -> 2
        n when n > 5 -> 3
          _ -> 0
    end
end
`,
		`fn f(r: Result<Int, String>) -> Int
    case r
        Ok(n) -> n
            |> Int.abs()
        Err(msg) -> 0
    end
end
`,
		`fn f(xs: List<Int>) -> Int
    xs |> Iter.reduce(0, fn acc, x ->
        acc + x
    end)
end
`,
		`fn f(xs: List<Int>) -> Int
    xs |> Iter.reduce(0, fn acc, x ->
        io.print("x")
        acc + x
    end)
end
`,
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse rejected\n%s: %v", src, err)
		}
	}
}

const (
	strayBodyDoc    = "a `///` doc comment documents the declaration after it, so it can't go inside a body; use `//` for a comment here"
	strayBodyPrompt = "a `//!` attached test goes above the declaration it tests, so it can't go before a statement in a body; use `//` for a comment here"
)

// A `///` or `//!` line in a body is an error at its marker, with a fix
// that makes each line of the run a `//` comment.
func TestStrayBodyMarker_IsAnError(t *testing.T) {
	for _, tc := range []struct {
		name, src, message string
		marks              []Pos
	}{
		{
			"doc comment run",
			"fn f(text: String) -> Int\n    /// if text == \"\" { 0 }\n    /// else\n    1\nend\n",
			strayBodyDoc, []Pos{{2, 5}, {3, 5}},
		},
		{
			"doc comment before an arm",
			"fn f(x: Int) -> Int\n    case x\n        /// one\n        1 -> 2\n        _ -> x\n    end\nend\n",
			strayBodyDoc, []Pos{{3, 9}},
		},
		{
			"prompt before a statement",
			"fn f(x: Int) -> Int\n    //! assert f(1) == 1\n    x\nend\n",
			strayBodyPrompt, []Pos{{2, 5}},
		},
		{
			"prompt at the end of a body",
			"fn f(x: Int) -> Int\n    x\n    //! assert f(1) == 1\n    //! assert f(2) == 2\nend\n",
			strayBodyPrompt, []Pos{{3, 5}, {4, 5}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(tc.src))
			if err == nil {
				t.Fatalf("Parse accepted\n%s", tc.src)
			}
			pe, ok := err.(ParseError)
			if !ok || pe.Message != tc.message {
				t.Fatalf("Parse error = %v, want %q", err, tc.message)
			}
			if pe.Line != tc.marks[0].Line || pe.Col != tc.marks[0].Col {
				t.Errorf("error at %d:%d, want %v", pe.Line, pe.Col, tc.marks[0])
			}
			if pe.Fix == nil || len(pe.Fix.Markers) != len(tc.marks) {
				t.Fatalf("fix = %+v, want markers %v", pe.Fix, tc.marks)
			}
			for i, m := range tc.marks {
				if pe.Fix.Markers[i] != m {
					t.Errorf("marker %d at %v, want %v", i, pe.Fix.Markers[i], m)
				}
			}
			if _, errs := ParseWithRecovery(lexer.Lex(tc.src)); len(errs) != 1 {
				t.Errorf("ParseWithRecovery reported %d errors, want this one alone: %v", len(errs), errs)
			}
		})
	}
}

// Doc comments on declarations and attached tests above them are what they
// were, a nested declaration's attached test included.
func TestStrayBodyMarker_DeclarationsUnaffected(t *testing.T) {
	for _, src := range []string{
		"/// Doubles n.\n//! assert double(2) == 4\nfn double(n: Int) -> Int\n    n * 2\nend\n",
		"fn f(x: Int) -> Int\n    //! assert g(1) == 2\n    fn g(y: Int) -> Int\n        y * 2\n    end\n    g(x)\nend\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse rejected\n%s: %v", src, err)
		}
	}
}
