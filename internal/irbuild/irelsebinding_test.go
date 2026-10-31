package irbuild

import (
	"strings"
	"testing"
)

// Every form of `Pattern = value else { ... }` runs on the VM
// (testdata/binding_else.nomi): a block or arms that leave, a fallback for
// the pattern's payload (a name, a tuple, a struct variant's fields or its
// whole record), arms mixing the two, a guard, a rebinding of the value's own
// name, a nested or list or map or tuple pattern whose else leaves, `?` in
// the else, and the exits of a lambda (`return`), an Iter.each callback
// (`continue`) and an inline Iter.loop (`break v`, `return v`). No function
// is BLOCKED, and the output matches the golden record.
func TestBindingElse_RunsOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("binding_else.nomi")
	want := strings.Join([]string{
		"Ok(a@b)", "Err(user 2 has no email)",
		"Ok(ada)", "Err(loading user 2: disk)", "Err(no user 3)",
		"none", "x",
		"20000", "1920",
		"Ok(8080)", "Err(bad port: x)", "Ok(9000)",
		"a3:3", "unknown:0",
		"7", "-1",
		"4", "-1",
		"hi ada", "anon",
		"3", "-1",
		"2", "1", "-9",
		"got 1", "ok 2",
		"5", "0",
		"3", "0",
		"Ok(2)", "Ok(10)", "Err(b)",
		"[10, 0, 30]",
		"#(200, 100)", "#(200, 100)",
		"6",
	}, "\n") + "\n"
	got := vmReference(path)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	golden := goldenReference(t, path)
	if got.stdout != golden.stdout || got.exit != golden.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			got.exit, got.stdout, golden.exit, golden.stdout)
	}
}

// The checker admits a binding else the builder must lower, and it does: the
// fixture's functions are all retained.
func TestBindingElse_EveryFunctionIsRetained(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `fn shown(email: Maybe<String>) -> String {
    Some(e) = email else { "none" }
    e
}

fn first_or(xs: List<Int>) -> Int {
    [first, .._rest] = xs else { return -1 }
    first
}

fn port(r: Result<Int, String>) -> Result<Int, String> {
    Ok(p) = r else {
        Err("") -> 8080
        Err(e) -> return Err(e)
    }
    Ok(p)
}

fn main() {
    _ = shown(None)
    _ = first_or([])
    _ = port(Ok(1))
}
`
	names, _ := irRetainedNames(t, src)
	for _, name := range []string{"shown", "first_or", "port"} {
		if !names[name] {
			t.Errorf("%s is not retained (retained: %v)", name, names)
		}
	}
}

// A bare `return` ending the else of a fallback-shaped binding in a lambda
// leaves the lambda. The else is a region of the payload's kind (Int, String),
// not Unit, and the arm reaches no exit, so the builder lowers it as it lowers
// `return v` there; it declined every one of these. vmhost's
// TestLambdaElseReturn_RunsInEveryCallback runs them.
func TestBindingElse_BareReturnLeavesALambda(t *testing.T) {
	src := `import std/io

fn block(xs: List<Maybe<Int>>) {
    Iter.each(xs, fn m -> {
        Some(v) = m else { return }
        io.inspect(v)
    })
}

fn statements(xs: List<Maybe<Int>>) {
    Iter.each(xs, fn m -> {
        Some(v) = m else {
            io.print("none")
            return
        }
        io.inspect(v)
    })
}

fn arms(xs: List<Result<Int, String>>) {
    Iter.each(xs, fn r -> {
        Ok(v) = r else {
            Err(e) -> {
                io.print(e)
                return
            }
        }
        io.inspect(v)
    })
}

fn named() {
    show = fn m: Maybe<String> -> {
        Some(s) = m else { return }
        io.print(s)
    }
    show(None)
}

fn nested(rows: List<List<Maybe<Int>>>) {
    Iter.each(rows, fn row -> {
        Iter.each(row, fn m -> {
            Some(v) = m else { return }
            io.inspect(v)
        })
    })
}

fn main() {
    block([Some(1), None])
    statements([None])
    arms([Err("e")])
    named()
    nested([[Some(2)]])
}
`
	if declines := irDeclinesOf(t, src); len(declines) != 0 {
		t.Fatalf("declined: %v", declines)
	}
}

// A binding whose pattern binds names no single payload holds (a list, a
// vector, a tuple inside a variant matched whole) takes the LEAVING shape,
// and its else may end in a trap: `unreachable` or `todo`. The trap ends the
// path, so the names are bound only where the pattern matched. The builder
// used to write the trap's value into the function's result and join the
// continuation from the else, so `nomi check` failed ir.Lint ("temp defined
// before use"). Covered: a function, an else with statements before the
// trap, else arms one of which traps, an inline Iter.loop body and a lambda.
func TestBindingElse_ATrapEndsALeavingElse(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std/io

fn pair(xs: List<Int>) -> Int {
    [a, b, ..] = xs else {
        io.print("short list")
        unreachable "callers pass two or more"
    }

    a * b
}

fn vec(xs: Vector<Int>) -> Int {
    #[a, b, ..] = xs else {
        todo
    }

    a + b
}

fn arms(r: Result<#(Int, Int), String>) -> Int {
    Ok(#(a, b)) = r else {
        Err("skip") -> return 0
        Err(_) -> unreachable "only skip fails"
    }

    a - b
}

fn looped(rows: List<List<Int>>) -> Int {
    #(_, sum) = Iter.loop(
        fn #(left, total) -> {
            [row, ..rest] = left else {
                break #(left, total)
            }

            [a, b] = row else {
                unreachable "rows have two cells"
            }

            return #(rest, total + a + b)
        },
        from: #(rows, 0),
    )

    sum
}

fn sums(rows: List<List<Int>>) -> List<Int> {
    rows
    |> Iter.map(fn row -> {
        [a, b] = row else {
            unreachable "rows have two cells"
        }

        a + b
    })
    |> Iter.to_list()
}

fn main() {
    io.inspect(pair([3, 4, 5]))
    io.inspect(vec(#[1, 2]))
    io.inspect(arms(Ok(#(9, 2))))
    io.inspect(arms(Err("skip")))
    io.inspect(looped([[1, 2], [3, 4]]))
    io.inspect(sums([[1, 2], [3, 4]]))
}
`, "12\n3\n7\n0\n10\n[3, 7]\n")
}

// The trap runs where the pattern does not match, after the else's own
// statements.
func TestBindingElse_ATrapInALeavingElseTraps(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyIterFault(t, `import std/io

fn pair(xs: List<Int>) -> Int {
    [a, b, ..] = xs else {
        io.print("short list")
        unreachable "callers pass two or more"
    }

    a * b
}

fn main() {
    io.inspect(pair([1]))
}
`, "line 6: unreachable: callers pass two or more", "short list\n")
}

// The same in a test body, whose else is lowered as test statements.
func TestBindingElse_ATrapEndsALeavingElseInATest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	out := irTestBodyVM(t, `test "binds two names" {
    [a, b, ..] = [1, 2, 3] else {
        unreachable "the list has three"
    }

    assert a + b == 3
}

test "traps" {
    #[a, b] = #[1] else {
        todo
    }

    assert a == b
}
`, 2)
	for _, want := range []string{"control_test.nomi :: binds two names\n", "FAIL ", "control_test.nomi :: traps\n", "todo reached at"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}
