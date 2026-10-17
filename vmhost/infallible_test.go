package vmhost_test

import (
	"strings"
	"testing"
)

// Infallible is the type with no values. Every type that contains it lowers:
// a value whose type names it is never built, and the code that would read
// one never runs. runTodo fails the test when a program is blocked.

// `Result<Int, Infallible>` is an instance like any other: its `Ok` path
// renders, compares and hashes (a map key and a set member), and a `case` on
// it reads the `Ok` payload with the `Err` arm never taken.
func TestInfallible_AResultThatCannotFailRendersComparesAndHashes(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

fn get() -> Result<Int, Infallible> {
    Ok(3)
}

fn bumped() -> Result<Int, Infallible> {
    n = try get()
    Ok(n + 1)
}

fn main() {
    r = get()
    io.print(Debug.inspect(r))
    io.print(Debug.inspect(r == Ok(3)))
    io.print(Debug.inspect(r == Ok(4)))
    s = #{r}
    io.print(Debug.inspect(Set.contains?(s, Ok(3))))
    m = %{r => "three"}
    io.print(Debug.inspect(Map.get(m, Ok(3))))
    n = case r {
        Ok(v) -> v
        Err(e) -> e
    }
    io.print(Debug.inspect(n))
    io.print(Debug.inspect(bumped()))
    io.print(Debug.inspect(Result.map(r, fn v -> v * 2)))
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "Ok(3)\nTrue\nFalse\nTrue\nSome(\"three\")\n3\nOk(4)\nOk(6)\n"
	if out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// Collections, tuples and variants over Infallible: an empty one is a value
// like any other, and one that would hold a `todo` stops at the `todo`.
func TestInfallible_EmptyCollectionsOfItAreValues(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

fn none() -> List<Infallible> {
    []
}

fn nothing() -> Maybe<Infallible> {
    None
}

fn main() {
    io.print(Debug.inspect(none()))
    io.print(Debug.inspect(nothing()))
    s: Set<Infallible> = #{}
    io.print(Debug.inspect(Set.size(s)))
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "[]\nNone\n0\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

func TestInfallible_AValueHoldingATodoStopsAtTheTodo(t *testing.T) {
	for _, tc := range []struct {
		name, expr string
	}{
		{"list", "[todo]"},
		{"tuple", "#(todo, 1)"},
		{"variant", "Some(todo)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, out, err := runTodo(t, `import std/io

fn main() {
    io.print("before")
    x = `+tc.expr+`
    io.print(Debug.inspect(x))
}
`)
			if out != "before\n" {
				t.Errorf("output %q, want only what ran before the todo", out)
			}
			if want := "todo reached at " + path + ":5"; err == nil || err.Error() != want {
				t.Fatalf("error %v, want %q", err, want)
			}
		})
	}
}

// A function declared to return Infallible lowers, and a call to it stops
// the program wherever it sits: a statement, an argument, an operand, a
// `case` subject, an interpolation's neighbour.
func TestInfallible_ACallToANeverReturningFunctionStops(t *testing.T) {
	for _, tc := range []struct {
		name, stmt string
	}{
		{"statement", "nev()"},
		{"argument", "io.print(Debug.inspect(twice(nev())))"},
		{"negation", "io.print(Debug.inspect(!nev()))"},
		{"right operand", "io.print(Debug.inspect(1 + nev()))"},
		{"case subject", "case nev() {\n        1 -> io.print(\"one\")\n        _ -> io.print(\"other\")\n    }"},
		{"todo case subject", "case todo {\n        1 -> io.print(\"one\")\n        _ -> io.print(\"other\")\n    }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, out, err := runTodo(t, `import std/io

fn nev() -> Infallible {
    todo "never"
}

fn twice(n: Int) -> Int {
    n * 2
}

fn main() {
    io.print("before")
    `+tc.stmt+`
}
`)
			if out != "before\n" {
				t.Errorf("output %q, want only what ran before the call", out)
			}
			line := "4: never"
			if strings.HasPrefix(tc.name, "todo") {
				line = "13"
			}
			if want := "todo reached at " + path + ":" + line; err == nil || err.Error() != want {
				t.Fatalf("error %v, want %q", err, want)
			}
		})
	}
}

// A branch, an arm or a lambda that would call a never-returning function
// completes when the run does not take it.
func TestInfallible_UntakenNeverReturningCallsComplete(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

fn nev() -> Infallible {
    todo "never"
}

fn absurd(x: Infallible) -> Int {
    x
}

fn pick(b: Bool) -> Int {
    if b {
        nev()
    } else {
        2
    }
}

fn main() {
    io.print(Debug.inspect(pick(False)))
    f = fn n: Int -> if n > 0 { n } else { absurd(nev()) }
    io.print(Debug.inspect(Iter.map([1, 2], f) |> Iter.to_list()))
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "2\n[1, 2]\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// A user enum with an Infallible payload is declared, built at its other
// variants, rendered, compared and hashed.
func TestInfallible_AnEnumWithAnInfalliblePayload(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

enum Shape {
    Circle(Int)
    Impossible(Infallible)
}

fn main() {
    s = Shape.Circle(2)
    io.print(Debug.inspect(s))
    io.print(Debug.inspect(s == Shape.Circle(2)))
    m = %{s => 1}
    io.print(Debug.inspect(Map.get(m, Shape.Circle(2))))
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "Circle(2)\nTrue\nSome(1)\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// `fn main() -> Result<Unit, Infallible>`, the spec's own example.
func TestInfallible_MainReturningAResultThatCannotFail(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

fn main() -> Result<Unit, Infallible> {
    io.print("ran")
    Ok(Unit)
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "ran\n" {
		t.Fatalf("output %q", out)
	}
}

// Infallible implements Display, Equatable and Hashable, so a type that
// contains it renders, compares and hashes through its own impls: the
// interface-qualified calls, interpolation, and a generic function bounded by
// the interface. The derived Hashable of `Result<Int, Infallible>` mixes its
// `Err` payload's hash into arithmetic (`1 * 31 + Hashable.hash(e)`), which
// stops at `e`.
func TestInfallible_ItsDisplayEquatableAndHashableImpls(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

fn get() -> Result<Int, Infallible> {
    Ok(3)
}

fn shown<T>(x: T) -> String where T: Display {
    "<${x}>"
}

fn hashed<T>(x: T) -> Int where T: Hashable {
    Hashable.hash(x)
}

fn main() {
    r = get()
    io.print(Display.to_string(r))
    io.print("r is ${r}")
    io.print(shown(r))
    io.print(Display.to_string(Some(r)))
    io.print(Debug.inspect(Hashable.hash(r) == Hashable.hash(get())))
    io.print(Debug.inspect(hashed(r) == Hashable.hash(r)))
    io.print(Debug.inspect(Equatable.equal?(r, Ok(3))))
    io.print(Debug.inspect(Equatable.equal?(r, Ok(4))))
    io.print(Debug.inspect(r != Ok(4)))
    failed: Result<Infallible, String> = Err("bad")
    io.print(Display.to_string(failed))
    io.print("${failed}")
    io.print(Debug.inspect(Map.get(%{failed => 1}, Err("bad"))))
    io.print(Debug.inspect(Set.contains?(#{failed}, Err("bad"))))
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "Ok(3)\nr is Ok(3)\n<Ok(3)>\nSome(Ok(3))\nTrue\nTrue\nTrue\nFalse\nTrue\nErr(bad)\nErr(bad)\nSome(1)\nTrue\n"
	if out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// A function over Infallible can be written with its impls and is never
// called: each lowers, and the arm that would call it is never taken.
func TestInfallible_FunctionsOverItLowerAndAreNeverCalled(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

fn show(x: Infallible) -> String {
    Display.to_string(x)
}

fn interpolated(x: Infallible) -> String {
    "x=${x}"
}

fn mixed(x: Infallible) -> Int {
    1 * 31 + Hashable.hash(x)
}

fn same(x: Infallible, y: Infallible) -> Bool {
    Equatable.equal?(x, y)
}

fn get() -> Result<Int, Infallible> {
    Ok(3)
}

fn main() {
    s = case get() {
        Ok(v) -> "ok ${v}"
        Err(e) -> "${show(e)} ${interpolated(e)} ${mixed(e)} ${same(e, e)}"
    }
    io.print(s)
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "ok 3\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// A collection literal mixing a `Result<Int, Infallible>` with an `Ok` whose
// error type is open solves that type to Infallible, in either order: the
// literal is a collection of one instance, which lowers.
func TestInfallible_ALiteralBesideAnOpenOkTakesItsType(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

fn main() {
    q: Result<Int, Infallible> = Ok(3)
    io.print(Debug.inspect(Set.size(#{q, Ok(4)})))
    io.print(Debug.inspect(Set.size(#{Ok(4), q, Ok(3)})))
    io.print(Debug.inspect([q, Ok(4)]))
    io.print(Debug.inspect([Ok(4), q]))
    io.print(Debug.inspect(Map.get(%{q => 1, Ok(4) => 2}, Ok(4))))
    io.print(Debug.inspect(Map.get(%{1 => q, 2 => Ok(4)}, 2)))
    io.print(Debug.inspect(Vector.length(#[q, Ok(5)])))
    io.print("${[q, Ok(4)]}")
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "2\n2\n[Ok(3), Ok(4)]\n[Ok(4), Ok(3)]\nSome(2)\nSome(Ok(4))\n2\n[Ok(3), Ok(4)]\n"
	if out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// Infallible's impl functions are std bodies like any other, so each can be
// passed as a function value. Over an empty list none is called.
func TestInfallible_ItsImplFunctionsAreValues(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

fn main() {
    xs: List<Infallible> = []
    io.print(Debug.inspect(xs |> Iter.map(Display.to_string) |> Iter.to_list()))
    io.print(Debug.inspect(xs |> Iter.map(Hashable.hash) |> Iter.to_list()))
    io.print(Debug.inspect(xs |> Iter.map(Infallible.to_string) |> Iter.to_list()))
    io.print(Debug.inspect(xs |> Iter.map(fn x -> Equatable.equal?(x, x)) |> Iter.to_list()))
    io.print(Debug.inspect(Set.size(Iter.to_set(xs))))
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "[]\n[]\n[]\n[]\n0\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}
