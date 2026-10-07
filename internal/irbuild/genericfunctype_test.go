package irbuild

import "testing"

// irRunSource writes src to a temp entry file, runs it on the VM and fails
// unless it prints want and exits 0.
func irRunSource(t *testing.T, src, want string) {
	t.Helper()
	if _, err := AnalyzeSource("entry.nomi", src); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	got := vmReference(writeTemp(t, src))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}

// A generic instantiated at a function type runs: a user generic struct and
// enum holding a function, the stdlib generics over one (`Maybe.with_default`,
// `Result.with_default`, `Maybe.map`, `Result.map`), the Iter family over a
// list of functions and producing one (`Iter.count`, `map`, `filter`,
// `reduce`, `each`, `any?`), `Debug.inspect` of a list of functions, a user
// generic at `List<fn>` given an empty list, and a list of functions held in
// a struct field, an enum payload and a `Result`'s `Ok`.
func TestIRGenericAtFunctionType_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Box<T> {
    v: T
}

enum Opt<T> {
    Has T
    Nothing
}

struct Ops {
    fs: List<(Int) -> Int>
}

enum Holder {
    Many List<(Int) -> Int>
}

fn first_or<T>(xs: List<T>, d: T): T {
    Maybe.with_default(List.head(xs), d)
}

fn main() {
    inc: (Int) -> Int = |x| x + 1
    dbl: (Int) -> Int = |x| x * 2
    b = Box{v: inc}.v
    io.inspect(b(1))
    case Opt.Has(dbl) {
        .Has(g) -> io.inspect(g(2))
        .Nothing -> io.inspect(0)
    }
    g = Maybe.with_default(Some(inc), dbl)
    io.inspect(g(3))
    h = Result.with_default(Ok<(Int) -> Int, String>(dbl), inc)
    io.inspect(h(4))
    io.inspect(Maybe.map(Some(inc), |f| f(5)))
    io.inspect(Result.map(Ok<(Int) -> Int, String>(dbl), |f| f(6)))
    fs = [inc, dbl]
    io.inspect(Iter.count(fs))
    io.inspect(fs)
    io.inspect(Iter.map(fs, |f| f(10)) |> Iter.to_list())
    io.inspect(Iter.filter(fs, |f| f(1) > 1) |> Iter.count())
    io.inspect(Iter.reduce(fs, |acc = 3, f| f(acc)))
    composed = Iter.reduce(fs, |acc = inc, f| |x: Int| f(acc(x)))
    io.inspect(composed(1))
    adders = Iter.map([1, 2], |n| |x: Int| x + n) |> Iter.to_list()
    io.inspect(Iter.map(adders, |f| f(1)) |> Iter.to_list())
    io.inspect(Iter.any?(fs, |f| f(0) == 1))
    Iter.each(fs, |f| io.inspect(f(100)))
    io.inspect(first_or([], inc)(7))
    io.inspect(first_or(fs, inc)(7))
    io.inspect(Iter.map(Ops{fs: fs}.fs, |f| f(5)) |> Iter.to_list())
    case Holder.Many(fs) {
        .Many(gs) -> io.inspect(Iter.map(gs, |f| f(7)) |> Iter.to_list())
    }
    held = Result.with_default(Err("no"), [|x: Int| x - 1])
    io.inspect(Iter.map(held, |f| f(1)) |> Iter.to_list())
}
`
	const want = "2\n" +
		"4\n" +
		"4\n" +
		"8\n" +
		"Some(6)\n" +
		"Ok(12)\n" +
		"2\n" +
		"[<function>, <function>]\n" +
		"[11, 20]\n" +
		"2\n" +
		"8\n" +
		"6\n" +
		"[2, 3]\n" +
		"True\n" +
		"101\n" +
		"200\n" +
		"8\n" +
		"8\n" +
		"[6, 10]\n" +
		"[8, 14]\n" +
		"[0]\n"
	irRunSource(t, src, want)
}
