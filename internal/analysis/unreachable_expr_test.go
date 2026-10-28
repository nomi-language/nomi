package analysis_test

import (
	"strings"
	"testing"
)

// `unreachable "reason"` takes the type its position expects, as `todo`
// does (spec §5, *Unreachable Code: `unreachable`*).
func TestUnreachable_FitsEveryTypedPosition(t *testing.T) {
	src := `struct Header {
    name: String
    size: Int
}

fn parse(_text: String) -> Header {
    unreachable "the header is parsed before"
}

fn first<T>(xs: List<T>) -> T {
    case xs {
        [x, .._] -> x
        [] -> unreachable "callers pass a non-empty list"
    }
}

fn index(n: Int, ns: List<Int>) -> Int {
    Some(i) = ns |> Iter.find_index(&(&1 == n)) else {
        unreachable "every number is listed"
    }
    i
}

fn twice(n: Int) -> Int {
    n * 2
}

fn uses(flag: Bool) -> Int {
    n: Int = unreachable "never bound"
    _ = unreachable "never discarded"
    f = fn x: Int -> if x > 1 { unreachable "x is small" } else { x }
    g: (Int) -> String = fn _ -> unreachable "never called"
    m: Int = 3 |> unreachable "never piped"
    h = #Header{name: "x", size: unreachable ` + "`raw reason`" + `}
    _ = #(g, h, parse("x"), first([1]), index(1, [1]))
    if flag {
        unreachable "flag is False"
    }
    twice(unreachable "never an argument") + f(n) + m
}
`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) > 0 {
		t.Fatalf("unreachable positions rejected: %v", errs)
	}
}

// An unannotated binding expects no type, so it asks for one, as `todo` does.
func TestUnreachable_AnUnannotatedBindingAsksForAType(t *testing.T) {
	for _, src := range []string{
		"fn f() -> Int {\n    x = unreachable \"no\"\n    x\n}\n",
		"fn f() -> Int {\n    x = 3 |> unreachable \"no\"\n    x\n}\n",
	} {
		_, errs := checkSourceWithStdlib(src)
		got := errorsAt(errs, 2, 5)
		want := "cannot infer a type for 'x' from `unreachable`: annotate the binding (`x: T = unreachable \"...\"`)"
		if len(got) != 1 || got[0] != want {
			t.Fatalf("%q: errors at 2:5 = %q, want exactly [%q]; all: %v", src, got, want, errs)
		}
	}
}

// Unlike `todo`, `unreachable` always leaves its block, so a statement after
// it is unreachable code, and so is one after an `if` or a `case` whose every
// branch is `unreachable` or another exit.
func TestUnreachable_AStatementAfterItIsUnreachableCode(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"    unreachable \"stop\"\n    io.print(\"after\")\n", "unreachable code after `unreachable`"},
		{"    if True {\n        return\n    } else {\n        unreachable \"stop\"\n    }\n    io.print(\"after\")\n",
			"unreachable code: the `if` above exits in every branch"},
		{"    case 1 {\n        1 -> unreachable \"one\"\n        _ -> unreachable \"other\"\n    }\n    io.print(\"after\")\n",
			"unreachable code: the `case` above traps in every arm"},
	} {
		_, errs := checkSourceWithStdlib("import std/io\n\nfn main() {\n" + tc.body + "}\n")
		found := false
		for _, e := range errs {
			if strings.HasPrefix(diagText(e), tc.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want %q, got %v", tc.body, tc.want, errs)
		}
	}
}

// A function's parameters are reported as never read, since `unreachable` is
// finished code: only `todo` marks a body as unwritten.
func TestUnreachable_DoesNotExcuseUnreadParameters(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f(n: Int) -> Int {\n    unreachable \"never called\"\n}\n")
	if got := errorsAt(errs, 1, 6); len(got) != 1 || !strings.Contains(got[0], "parameter 'n' is never read") {
		t.Fatalf("errors at 1:6 = %q; all: %v", got, errs)
	}
}
