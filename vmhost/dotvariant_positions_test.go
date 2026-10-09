package vmhost_test

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A dot-leading prelude variant (`.Ok(0)`, `.Some(n)`, `.None`, `.Err(e)`)
// in a position that hands down no expected kind: a nested fn's body and
// its case arms, if branches and let-else fallbacks, and an annotated
// lambda's tail. The builder took the prelude instance only from the
// expected kind, so these declined with "a callee that is not a name: a
// DotVariant" (or "a DotVariant" for a bare one); the instance is the type
// the checker gave the dot. User enums take the qualified rewrite and ran
// already; they are here to keep both routes in one place. Found by
// FuzzFrontEndAcceptsSoItLowers, which put whitespace after the dot (`. Ok(0)`);
// the whitespace parses to the same node.
func TestDotVariant_PreludeVariantsWhereNoKindIsExpected(t *testing.T) {
	got := runSourceOutput(t, `import std/io

enum Shape {
    Circle(Float)
    Dot
}

fn parse(s: String): Result<Int, String> {
    case s {
        "" -> Err("empty")
        _ -> Ok(String.length(s))
    }
}

fn main() {
    fn none(): Maybe<Int> {
        .None
    }

    fn total(xs: List<String>): Result<Int, String> {
        case xs {
            [] -> . Ok(0)
            [x, ..rest] -> {
                n = try parse(x)
                rest_total = try total(rest)
                .Ok(n + rest_total)
            }
        }
    }

    fn first(xs: List<Int>): Maybe<Int> {
        Some(x) = List.head(xs) else { return .None }
        .Some(x * 10)
    }

    fn checked(s: String): Result<Int, String> {
        Ok(n) = parse(s) else { return .Err("bad") }
        .Ok(n)
    }

    fn pick<T>(x: T, keep: Bool): Maybe<T> {
        if keep { .Some(x) } else { .None }
    }

    fn shape(n: Int): Shape {
        case n {
            0 -> .Dot
            _ -> .Circle(2.0)
        }
    }

    positive: (Int) -> Maybe<Int> = |n| if n > 0 { .Some(n) } else { .None }
    length: (String) -> Result<Int, String> = |s| case s {
        "" -> .Err("empty")
        _ -> .Ok(String.length(s))
    }

    io.inspect(none())
    io.inspect(total(["a", "bb"]))
    io.inspect(total(["a", ""]))
    io.inspect(first([4]))
    io.inspect(first([]))
    io.inspect(checked(""))
    io.inspect(checked("xyz"))
    io.inspect(pick(5, True))
    io.inspect(pick("s", False))
    io.inspect(shape(0))
    io.inspect(shape(1))
    io.inspect(positive(1))
    io.inspect(positive(0))
    io.inspect(length(""))
    io.inspect(length("ab"))
}
`)
	const want = `None
Ok(3)
Err("empty")
Some(40)
None
Err("bad")
Ok(3)
Some(5)
None
Dot
Circle(2.0)
Some(1)
None
Err("empty")
Ok(2)
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// The fuzzer's own shape: the nested fn is in a test body.
func TestDotVariant_PreludeVariantInANestedFnOfATestBody(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "dot_test.nomi"
	p, err := vmhost.LoadSource(name, `fn parse(s: String): Result<Int, String> {
    case s {
        "" -> Err("empty")
        _ -> Ok(String.length(s))
    }
}

test "a recursive nested fn with try" {
    fn total(xs: List<String>): Result<Int, String> {
        case xs {
            [] ->
                .Ok(0)
            [x, ..rest] -> {
                n = try parse(x)
                rest_total = try total(rest)
                Ok(n + rest_total)
            }
        }
    }

    assert total(["a", "bb"]) == Ok(3)
    assert total(["a", ""]) == Err("empty")
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	failed := rep.Summary()
	const want = `ok dot_test.nomi :: a recursive nested fn with try
test result: ok. 1 passed, 0 failed
`
	if got := buf.String(); failed || got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
