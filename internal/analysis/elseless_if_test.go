package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An `if` with no `else` is a statement, so a branch whose value is not
// Unit is an error at the branch's value, which names its type. For a Maybe
// branch it says to add `else None` and its fix does, after a one-line
// `if` and as an `else` branch of a block one; for any other branch it says
// to add an `else`, with no fix. Where a statement drops the value, it says
// to write `_ = ...` and its fix does, in block form when the `if` is one
// line. Each case fails if the front end starts admitting its source, and
// its fixed source checks.
func TestElselessIfValueIsRejected(t *testing.T) {
	const noneHelp = "an `if` has a value only with an `else`; `else None` gives it `None` when no branch runs"
	const elseHelp = "an `if` has a value only with an `else`, whose branch gives the value when no other branch runs"
	const dropHelp = "`_ = ...` runs the branch for its effect and drops its value"
	const stmt = "an `if` without `else` is a statement, so its branch must be `Unit`; "
	for _, tc := range []struct {
		name, src, message, help, fixed string
		line, col                       int
	}{
		{
			"a Maybe branch",
			"fn label(n: Int) -> Maybe<Int>\n    if n > 0 then Some(5)\nend\n",
			stmt + "this branch is a `Maybe<Int>`. Add `else None` to make the `if` a `Maybe<Int>`",
			noneHelp,
			"fn label(n: Int) -> Maybe<Int>\n    if n > 0 then Some(5) else None\nend\n",
			2, 19,
		},
		{
			"a Maybe branch in block form",
			"fn label(n: Int) -> Maybe<Int>\n    x = if n > 0 then\n        Some(5)\n    end\n    x\nend\n",
			stmt + "this branch is a `Maybe<Int>`. Add `else None` to make the `if` a `Maybe<Int>`",
			noneHelp,
			"fn label(n: Int) -> Maybe<Int>\n    x = if n > 0 then\n        Some(5)\n    else\n        None\n    end\n    x\nend\n",
			3, 9,
		},
		{
			"a one-line chain of Maybes",
			"fn label(n: Int) -> Maybe<String>\n    if n > 0 then Some(\"a\") else if n < 0 then Some(\"b\")\nend\n",
			stmt + "this branch is a `Maybe<String>`. Add `else None` to make the `if` a `Maybe<String>`",
			noneHelp,
			"fn label(n: Int) -> Maybe<String>\n    if n > 0 then Some(\"a\") else if n < 0 then Some(\"b\") else None\nend\n",
			2, 19,
		},
		{
			"a block chain of Maybes",
			"fn label(n: Int) -> Maybe<String>\n    if n > 0 then\n        Some(\"a\")\n    else if n < 0 then\n        Some(\"b\")\n    end\nend\n",
			stmt + "this branch is a `Maybe<String>`. Add `else None` to make the `if` a `Maybe<String>`",
			noneHelp,
			"fn label(n: Int) -> Maybe<String>\n    if n > 0 then\n        Some(\"a\")\n    else if n < 0 then\n        Some(\"b\")\n    else\n        None\n    end\nend\n",
			3, 9,
		},
		{
			"the expected Maybe reaches the branch",
			"fn first(c: Bool) -> Maybe<Ordering>\n    if c then Some(.Less)\nend\n",
			stmt + "this branch is a `Maybe<Ordering>`. Add `else None` to make the `if` a `Maybe<Ordering>`",
			noneHelp,
			"fn first(c: Bool) -> Maybe<Ordering>\n    if c then Some(.Less) else None\nend\n",
			2, 15,
		},
		{
			"a chain that mixes a Maybe with a bare value",
			"fn label(n: Int) -> Maybe<String>\n    if n > 0 then Some(\"a\") else if n < 0 then \"b\"\nend\n",
			stmt + "this branch is a `Maybe<String>`. Add `else None` to make the `if` a `Maybe<String>`",
			noneHelp,
			"",
			2, 19,
		},
		{
			"a value",
			"fn label(n: Int) -> String\n    if n > 0 then \"positive\"\nend\n",
			stmt + "this branch is a `String`. Add an `else` to give the `if` a value",
			elseHelp,
			"",
			2, 19,
		},
		{
			"binding",
			"fn label(n: Int) -> Int\n    x = if n > 0 then n\n    x\nend\n",
			stmt + "this branch is an `Int`. Add an `else` to give the `if` a value",
			elseHelp,
			"",
			2, 23,
		},
		{
			"statement",
			"fn label(n: Int) -> Int\n    if n > 0 then n + 1\n    n\nend\n",
			stmt + "this branch is an `Int`, which this statement drops. Write `_ = n + 1` in the branch, or add an `else`",
			dropHelp,
			"fn label(n: Int) -> Int\n    if n > 0 then\n        _ = n + 1\n    end\n    n\nend\n",
			2, 19,
		},
		{
			"statement in block form",
			"fn label(n: Int) -> Int\n    if n > 0 then\n        n + 1\n    end\n    n\nend\n",
			stmt + "this branch is an `Int`, which this statement drops. Write `_ = n + 1` in the branch, or add an `else`",
			dropHelp,
			"fn label(n: Int) -> Int\n    if n > 0 then\n        _ = n + 1\n    end\n    n\nend\n",
			3, 9,
		},
		{
			"a Unit function's end",
			"fn label(n: Int)\n    if n > 0 then n + 1\nend\n",
			stmt + "this branch is an `Int`, which this statement drops. Write `_ = n + 1` in the branch, or add an `else`",
			dropHelp,
			"fn label(n: Int)\n    if n > 0 then\n        _ = n + 1\n    end\nend\n",
			2, 19,
		},
		{
			"a Maybe a statement drops",
			"fn label(n: Int) -> Int\n    if n > 0 then Some(n)\n    n\nend\n",
			stmt + "this branch is a `Maybe<Int>`, which this statement drops. Write `_ = Some(n)` in the branch, or add an `else`",
			dropHelp,
			"fn label(n: Int) -> Int\n    if n > 0 then\n        _ = Some(n)\n    end\n    n\nend\n",
			2, 19,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkWithStdlib(tc.src)
			for _, e := range errs {
				if e.Message != tc.message {
					continue
				}
				if e.Line != tc.line || e.Col != tc.col {
					t.Fatalf("at %d:%d, want %d:%d", e.Line, e.Col, tc.line, tc.col)
				}
				found := false
				for _, h := range e.Hints {
					found = found || h == tc.help
				}
				if !found {
					t.Fatalf("hints %q, want %q among them", e.Hints, tc.help)
				}
				if len(errs) != 1 {
					t.Fatalf("errors %v, want this one alone", errs)
				}
				if tc.fixed == "" {
					return
				}
				if e.Fix == nil {
					t.Fatalf("no fix, want one writing\n%s", tc.fixed)
				}
				if got := applyFix(t, tc.src, e.Fix); got != tc.fixed {
					t.Fatalf("the fix writes\n%s\nwant\n%s", got, tc.fixed)
				}
				if errs := checkWithStdlib(tc.fixed); len(errs) > 0 {
					t.Fatalf("the fixed source has errors: %v", errs)
				}
				return
			}
			t.Fatalf("the front end accepts this, or reports something else: no %q error; got %v", tc.message, errs)
		})
	}
}

// The forms an `if` with no `else` takes are accepted as Unit, the type
// the `if` keyword's hover shows: a branch that is Unit, leaves, or ends in
// an `assert` or a `dbg`.
func TestElselessIfTypes(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"returning branch", "fn f(n: Int) -> Result<Int, String>\n    if n < 0 then return Err(\"neg\")\n    Ok(n)\nend\n", "Unit"},
		{"chain whose first branch returns", "fn f(n: Int) -> Result<Int, String>\n    if n > 0 then\n        return Ok(n)\n    else if n < 0 then\n        dbg n\n    end\n    Err(\"zero\")\nend\n", "Unit"},
		{"pattern if", "fn f(m: Maybe<Int>)\n    if Some(n) = m then dbg n\nend\n", "Unit"},
		{"a branch that ends in assert", "import std::assertions.AssertionFailure\n\nfn f(n: Int) -> Result<Int, AssertionFailure>\n    if n > 0 then assert n < 10\n    Ok(n)\nend\n", "Unit"},
		{"a branch that ends in dbg", "fn f(n: Int)\n    if n > 0 then dbg n\nend\n", "Unit"},
		{"Unit branch", "fn g() end\n\nfn f(c: Bool)\n    if c then g()\nend\n", "Unit"},
		{"unannotated binding", "fn g() end\n\nfn f(c: Bool)\n    x = if c then g()\n    x\nend\n", "Unit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fa, errs := checkSourceWithStdlib(tc.src)
			if len(errs) > 0 {
				t.Fatalf("the front end rejects this: %v", errs)
			}
			line := 0
			for i, l := range strings.Split(tc.src, "\n") {
				if strings.HasPrefix(strings.TrimSpace(l), "if ") || strings.Contains(l, "= if ") {
					line = i + 1
					break
				}
			}
			var got analysis.Type
			for pos, sym := range fa.References {
				if pos.Line == line && sym.ControlFlow != nil && sym.ControlFlow.Keyword == "if" {
					got = sym.ControlFlow.OutputTy
					break
				}
			}
			if got == nil || got.String() != tc.want {
				t.Fatalf("the `if` on line %d is %v, want %s", line, got, tc.want)
			}
		})
	}
}
