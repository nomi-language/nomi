package irbuild

import "testing"

// A user generic called at a type argument nothing determines, where no
// value of it is made (spec, "Determined type arguments"), runs at the hole
// read as Unit, or as Int when every hole is an empty list's element; and a
// value built with such a hole is shown through std's Debug instance.
func TestIRUndeterminedTypeArgument_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn ident<T>(x: T): T {
    x
}

fn count_all<T>(xs: List<T>): Int {
    Iter.count(xs)
}

fn first<T>(xs: List<T>): Maybe<T> {
    List.head(xs)
}

fn main() {
    io.inspect(Result.ok?(ident(Ok(1))))
    io.inspect(Maybe.some?(ident(None)))
    io.inspect(ident(Err("e")) |> Result.ok?())
    io.inspect(count_all([]))
    io.inspect(Maybe.some?(first([])))
    f: (Int) -> Int = |x| x + 1
    held = (1, Ok(f)).1
    io.inspect(held)
}
`
	const want = "True\n" +
		"False\n" +
		"False\n" +
		"0\n" +
		"False\n" +
		"Ok(<function>)\n"
	irRunSource(t, src, want)
}

// The std and prelude shapes of the same rule: a variant holding an empty
// collection, an empty constructor shown, passed on or iterated, and the
// Iter kernels over empty sources run, each open element read as Int and
// each other hole as Unit, as a user generic call's are. A generic nested
// fn called at an open argument runs the same way.
func TestIRUndeterminedTypeArgument_StdShapesRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    io.inspect(Some([]))
    io.inspect(Some(#{}))
    io.inspect(Ok([]))
    io.inspect(Some(Some([])))
    io.inspect(Some(Map.empty()))
    io.inspect(Some(Vector.empty()))
    io.inspect(Map.empty())
    io.inspect(Map.size(Map.empty()))
    io.inspect(Map.empty() |> Map.size())
    io.inspect(Map.keys(Map.empty()) |> Iter.to_list())
    io.inspect([Map.empty()])
    io.inspect((Map.empty(), 1))
    io.inspect(Map.empty() == Map.empty())
    io.print("${Map.empty()}")
    io.inspect(Set.size(#{}))
    io.inspect(Set.union(#{}, #{}))
    io.inspect(Vector.length(Vector.empty()))
    io.inspect(Iter.zip([], []) |> Iter.to_list())
    io.inspect(Iter.zip([], [1]) |> Iter.count())
    io.inspect(Iter.concat([], []) |> Iter.to_list())
    io.inspect(Iter.concat(#{}, []) |> Iter.to_list())
    io.inspect(Iter.to_list(Vector.empty()))
    io.inspect(Iter.count(Map.empty()))
    fn apply<T, U>(x: T, f: (T) -> U): U {
        f(x)
    }
    io.inspect(apply(1, |x| Ok(x + 1)))
}
`
	const want = "Some([])\n" +
		"Some(#{})\n" +
		"Ok([])\n" +
		"Some(Some([]))\n" +
		"Some({=>})\n" +
		"Some(#[])\n" +
		"{=>}\n" +
		"0\n" +
		"0\n" +
		"[]\n" +
		"[{=>}]\n" +
		"({=>}, 1)\n" +
		"True\n" +
		"{=>}\n" +
		"0\n" +
		"#{}\n" +
		"0\n" +
		"[]\n" +
		"0\n" +
		"[]\n" +
		"[]\n" +
		"[]\n" +
		"0\n" +
		"Ok(2)\n"
	irRunSource(t, src, want)
}

// `dbg` of an empty constructor nothing types shows the literal.
func TestIRUndeterminedTypeArgument_DbgOfAnOpenEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `fn main() {
    dbg Map.empty()
    dbg #{}
    dbg Vector.empty()
}
`
	if _, err := AnalyzeSource("entry.nomi", src); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	got := vmReference(writeTemp(t, src))
	want := "dbg line 2: Map.empty() = {=>}\ndbg line 3: #{} = #{}\ndbg line 4: Vector.empty() = #[]\n"
	if got.stdout+got.stderr != want || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}

// `Map.get(Map.empty(), 1)`, whose key fixes K and leaves V open, and
// `Iter.zip([], Map.empty())` run by the same fill rule. A Map, a Range or
// a user `Iter` as `Iter.zip`'s or `Iter.concat`'s second operand is viewed
// as it is as the first.
func TestIRUndeterminedTypeArgument_MapShapesRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Countdown {
    from: Int
}

impl Iter for Countdown {
    fn each_while(c: Countdown, yield: (Int) -> Bool): Bool {
        if c.from == 0 {
            True
        } else if yield(c.from) {
            each_while(Countdown{from: c.from - 1}, yield)
        } else {
            False
        }
    }
}

fn main() {
    io.inspect(Map.get(Map.empty(), 1))
    io.inspect(Map.get(Map.empty(), "k") == None)
    io.inspect(Iter.zip([], Map.empty()) |> Iter.to_list())
    io.inspect(Iter.zip([1], Map.empty()) |> Iter.count())
    io.inspect(Iter.zip([1], {1 => 2}) |> Iter.to_list())
    io.inspect(Iter.zip([1, 2], 5..9) |> Iter.to_list())
    io.inspect(Iter.zip(["a", "b"], Countdown{from: 3}) |> Iter.to_list())
    io.inspect(Iter.concat([(1, 2)], {3 => 4}) |> Iter.to_list())
    io.inspect(Iter.concat([9], Countdown{from: 2}) |> Iter.to_list())
}
`
	const want = "None\n" +
		"True\n" +
		"[]\n" +
		"0\n" +
		"[(1, (1, 2))]\n" +
		"[(1, 5), (2, 6)]\n" +
		"[(\"a\", 3), (\"b\", 2)]\n" +
		"[(1, 2), (3, 4)]\n" +
		"[9, 2, 1]\n"
	irRunSource(t, src, want)
}
