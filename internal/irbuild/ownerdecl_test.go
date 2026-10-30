package irbuild

import (
	"strings"
	"testing"
)

// An import at the top of a block that binds a std type's name, beside a
// module-level type of the same name: inside the block the name is std's
// type in an annotation, a lambda's parameter and an owner qualifier, and
// outside it the module's own.
func TestOwnerDecl_BlockImportRebindsAStdType(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Duration {
    ms: Int
}

impl Duration {
    fn seconds(n: Int) -> Duration {
        #Duration{ms: n * 7}
    }
}

fn std_total() -> Int {
    import std/duration.Duration
    d: Duration = Duration.seconds(2) + Duration.milliseconds(500)
    twice = fn x: Duration -> x + x
    Duration.as_millis(twice(d))
}

fn main() {
    io.inspect(std_total())
    io.inspect(Duration.seconds(2).ms)
    io.inspect(#Duration{ms: 1})
}
`, "5000\n14\n#Duration{ms: 1}\n")
	out := irTestBodyVM(t, `struct Duration {
    ms: Int
}

impl Duration {
    fn seconds(n: Int) -> Duration {
        #Duration{ms: n * 7}
    }
}

test "a block import that rebinds a module-level name" {
    import std/duration.Duration
    d: Duration = Duration.seconds(1)
    assert d == Duration.milliseconds(1000)
    assert Duration.as_millis(d) == 999
}

test "the module-level name outside the block" {
    assert Duration.seconds(1).ms == 7
}
`, 2)
	blocks := irTestReportBlocks(out)
	if b := blocks["a block import that rebinds a module-level name"]; !strings.HasPrefix(b, "FAIL ") || !strings.Contains(b, "= 1000") {
		t.Errorf("the rebinding case reports:\n%s\nwant a failure whose row shows std's 1000 milliseconds", b)
	}
	if b := blocks["the module-level name outside the block"]; !strings.HasPrefix(b, "ok ") {
		t.Errorf("the module-level case reports:\n%s", b)
	}
}

// A bare call to a selectively imported std method whose owner's name this
// file declares too: the call is std's `Date.parse`, as the checker resolved
// it, not a lookup of `Date` in this file.
func TestOwnerDecl_BareStdMethodBesideALocalOwner(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/io
    std/calendar.Date.parse
}

struct Date {
    day: Int
}

fn main() {
    io.inspect(Result.is_ok(parse("2024-01-02")))
    io.inspect(Result.is_ok(parse("not a date")))
    io.inspect(#Date{day: 1})
}
`, "True\nFalse\n#Date{day: 1}\n")
}

// A bare call to a selectively imported prelude method takes the route its
// qualified spelling takes, over the call as written: a payload-free `None`
// operand is typed by the checker's solution for this call, and inside an
// assertion the call's rows are recorded.
func TestOwnerDecl_BareImportedPreludeMethods(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/io
    std/maybe.Maybe.{with_default, to_result, is_some, map}
    std/results.Result.{map_err}
}

fn main() {
    io.inspect(with_default(Some(1), 2))
    io.inspect(with_default(None, 2))
    io.inspect(to_result(Some(3), "missing"))
    io.inspect(is_some(Some(4)))
    io.inspect(map(Some(5), fn x -> x * 2))
    r: Result<Int, Int> = Err(6)
    io.inspect(map_err(r, fn e -> "error ${e}"))
}
`, "1\n2\nOk(3)\nTrue\nSome(10)\nErr(\"error 6\")\n")
	out := irTestBodyVM(t, `import std/maybe.Maybe.{with_default}

test "bare prelude methods in an assertion" {
    assert with_default(Some(1), 2) == 1
    assert with_default(None, 2) == 3
}
`, 1)
	if b := irTestReportBlocks(out)["bare prelude methods in an assertion"]; !strings.HasPrefix(b, "FAIL ") ||
		!strings.Contains(b, "with_default(None, 2)") || !strings.Contains(b, "= 2") {
		t.Errorf("the case reports:\n%s\nwant a failure whose row shows with_default(None, 2) = 2", b)
	}
}
