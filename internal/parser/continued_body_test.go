package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

const (
	continuedCaseArm = "this `case` arm continues on the next line; an arm with more than one statement needs braces"
	continuedElseArm = "this `else` arm continues on the next line; an arm with more than one statement needs braces"
	continuedLambda  = "this lambda's body continues on the next line; a lambda body with more than one statement needs braces"
)

// A body expanded onto several lines without braces is an error at its
// second line, with the braced form as the hint and a fix that wraps it.
func TestContinuedBody_IsAnError(t *testing.T) {
	for _, tc := range []struct {
		name, src, message, hint string
		line, col                int
		after, end               Pos
	}{
		{
			name: "case arm, body on the next line",
			src: `fn f(r: Result<Int, String>): Int {
    case r {
        Ok(n) ->
            io.print("got ${n}")
            n
        Err(msg) -> 0
    }
}
`,
			message: continuedCaseArm, hint: "put the arm's body in braces:\nOk(n) -> {\n    ...\n}",
			line: 5, col: 13, after: Pos{3, 17}, end: Pos{5, 14},
		},
		{
			name: "case arm, body on the arrow's line",
			src: `fn f(r: Result<Int, String>): Int {
    case r {
        Ok(n) -> io.print("got ${n}")
            n
        Err(msg) -> 0
    }
}
`,
			message: continuedCaseArm, hint: "put the arm's body in braces:\nOk(n) -> {\n    ...\n}",
			line: 4, col: 13, after: Pos{3, 17}, end: Pos{4, 14},
		},
		{
			name: "guarded arm",
			src: `fn f(x: Int): Int {
    case x {
        n when n > 0 ->
            io.print("pos")
            1
        _ -> 0
    }
}
`,
			message: continuedCaseArm, hint: "put the arm's body in braces:\nn when n > 0 -> {\n    ...\n}",
			line: 5, col: 13, after: Pos{3, 24}, end: Pos{5, 14},
		},
		{
			name: "string pattern",
			src: `fn f(s: String): Int {
    case s {
        "go" ->
            io.print(s)
            1
        _ -> 0
    }
}
`,
			message: continuedCaseArm, hint: "put the arm's body in braces:\n\"go\" -> {\n    ...\n}",
			line: 5, col: 13, after: Pos{3, 16}, end: Pos{5, 14},
		},
		{
			name: "conditions case",
			src: `fn f(x: Int): Int {
    case {
        x > 0 ->
            io.print("pos")
            1
        _ -> 0
    }
}
`,
			message: continuedCaseArm, hint: "put the arm's body in braces:\nx > 0 -> {\n    ...\n}",
			line: 5, col: 13, after: Pos{3, 17}, end: Pos{5, 14},
		},
		{
			name: "binding else arm",
			src: `fn f(r: Result<Int, String>): Int {
    Ok(n) = r else {
        Err(msg) ->
            io.print(msg)
            0
    }
    n
}
`,
			message: continuedElseArm, hint: "put the arm's body in braces:\nErr(msg) -> {\n    ...\n}",
			line: 5, col: 13, after: Pos{3, 20}, end: Pos{5, 14},
		},
		{
			name: "lambda argument",
			src: `fn f(xs: List<Int>): Int {
    xs |> Iter.reduce(0, |acc, x|
        io.print("x")
        acc + x)
}
`,
			message: continuedLambda, hint: "put the lambda's body in braces:\n|acc, x| {\n    ...\n}",
			line: 4, col: 9, after: Pos{2, 34}, end: Pos{4, 16},
		},
		{
			name: "lambda argument closed on its own line",
			src: `fn f(xs: List<Int>) {
    xs |> Iter.each(|x|
        io.print("x")
        io.print("y")
    )
}
`,
			message: continuedLambda, hint: "put the lambda's body in braces:\n|x| {\n    ...\n}",
			line: 4, col: 9, after: Pos{2, 24}, end: Pos{4, 22},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(tc.src))
			if err == nil {
				t.Fatalf("Parse accepted an unbraced body over several lines:\n%s", tc.src)
			}
			pe, ok := err.(ParseError)
			if !ok || pe.Message != tc.message {
				t.Fatalf("Parse error = %v, want %q", err, tc.message)
			}
			if pe.Line != tc.line || pe.Col != tc.col {
				t.Errorf("error at %d:%d, want %d:%d", pe.Line, pe.Col, tc.line, tc.col)
			}
			if len(pe.Hints) != 1 || pe.Hints[0] != tc.hint {
				t.Errorf("hints = %q, want %q", pe.Hints, tc.hint)
			}
			if pe.Fix == nil || pe.Fix.Wrap == nil {
				t.Fatalf("no wrap fix")
			}
			if w := *pe.Fix.Wrap; w.After != tc.after || w.End != tc.end {
				t.Errorf("wrap = %+v, want after %v end %v", w, tc.after, tc.end)
			}
			_, errs := ParseWithRecovery(lexer.Lex(tc.src))
			if len(errs) != 1 {
				t.Errorf("ParseWithRecovery reported %d errors, want this one alone: %v", len(errs), errs)
			}
		})
	}
}

// A braced arm, a one-line arm, and an arm whose next line is another arm,
// however indented, are what they were.
func TestContinuedBody_ValidShapesUnaffected(t *testing.T) {
	for _, src := range []string{
		`fn f(r: Result<Int, String>): Int {
    case r {
        Ok(n) -> {
            io.print("got ${n}")
            n
        }
        Err(msg) -> 0
    }
}
`,
		`fn f(r: Result<Int, String>): Int {
    case r {
        Ok(n) ->
            n
        Err(msg) -> 0
    }
}
`,
		`fn f(x: Int): Int {
    case x {
        0 -> 1
            1 -> 2
        n when n > 5 -> 3
          _ -> 0
    }
}
`,
		`fn f(r: Result<Int, String>): Int {
    case r {
        Ok(n) -> n
            |> Int.abs()
        Err(msg) -> 0
    }
}
`,
		`fn f(xs: List<Int>): Int {
    xs |> Iter.reduce(0, |acc, x|
        acc + x)
}
`,
		`fn f(xs: List<Int>): Int {
    xs |> Iter.reduce(0, |acc, x| {
        io.print("x")
        acc + x
    })
}
`,
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse rejected\n%s: %v", src, err)
		}
	}
}

// The continued arm parses as if braced: the case keeps its arms, and an
// unrelated error later in the same function and one in the next function
// are each reported once.
func TestContinuedBody_Recovers(t *testing.T) {
	src := `fn f(r: Result<Int, String>): Int {
    x = case r {
        Ok(n) ->
            io.print("got ${n}")
            n
        Err(msg) -> 0
    }
    y = (x +
}

fn g(): Int {
    1 +
}
`
	_, errs := ParseWithRecovery(lexer.Lex(src))
	if len(errs) != 3 {
		t.Fatalf("got %d errors, want 3: %v", len(errs), errs)
	}
	if errs[0].Message != continuedCaseArm || errs[0].Line != 5 {
		t.Errorf("first error = %v, want the continued arm at line 5", errs[0])
	}
	if errs[1].Line < 8 || errs[1].Line > 9 || errs[2].Line < 12 {
		t.Errorf("later errors = %v, want one in f after the case and one in g", errs[1:])
	}

	clean := strings.Replace(src, "    y = (x +\n", "    x\n", 1)
	clean = strings.Replace(clean, "    1 +\n", "    1\n", 1)
	nodes, errs := ParseWithRecovery(lexer.Lex(clean))
	if len(errs) != 1 || errs[0].Message != continuedCaseArm {
		t.Fatalf("errors = %v, want the continued arm alone", errs)
	}
	fn := nodes[0].(*ast.FuncDef)
	c := fn.Body.Stmts[0].(*ast.Binding).Value.(*ast.Case)
	if len(c.Branches) != 2 {
		t.Fatalf("case has %d arms, want 2", len(c.Branches))
	}
	body, ok := c.Branches[0].Body.(*ast.Block)
	if !ok || len(body.Stmts) != 2 {
		t.Fatalf("first arm's body = %#v, want a block of two statements", c.Branches[0].Body)
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
			"fn f(text: String): Int {\n    /// if text == \"\" { 0 }\n    /// else\n    1\n}\n",
			strayBodyDoc, []Pos{{2, 5}, {3, 5}},
		},
		{
			"doc comment before an arm",
			"fn f(x: Int): Int {\n    case x {\n        /// one\n        1 -> 2\n        _ -> x\n    }\n}\n",
			strayBodyDoc, []Pos{{3, 9}},
		},
		{
			"prompt before a statement",
			"fn f(x: Int): Int {\n    //! assert f(1) == 1\n    x\n}\n",
			strayBodyPrompt, []Pos{{2, 5}},
		},
		{
			"prompt at the end of a body",
			"fn f(x: Int): Int {\n    x\n    //! assert f(1) == 1\n    //! assert f(2) == 2\n}\n",
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
		"/// Doubles n.\n//! assert double(2) == 4\nfn double(n: Int): Int {\n    n * 2\n}\n",
		"fn f(x: Int): Int {\n    //! assert g(1) == 2\n    fn g(y: Int): Int {\n        y * 2\n    }\n    g(x)\n}\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse rejected\n%s: %v", src, err)
		}
	}
}
