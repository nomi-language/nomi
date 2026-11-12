package irbuild

import "testing"

// A user generic called at a type argument nothing determines, where no
// value of it is made (spec, "Determined type arguments"), runs at the type
// the checker closed the argument as (analysis's closed_vars.go); and a
// value built with such an argument is shown through std's Debug instance.
func TestIRUndeterminedTypeArgument_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn ident<T>(x: T) -> T
    x
end

fn count_all<T>(xs: List<T>) -> Int
    Iter.count(xs)
end

fn first<T>(xs: List<T>) -> Maybe<T>
    List.head(xs)
end

fn main()
    io.inspect(Result.is_ok(ident(Ok(1))))
    io.inspect(Maybe.is_some(ident(None)))
    io.inspect(ident(Err("e")) |> Result.is_ok())
    io.inspect(count_all([]))
    io.inspect(Maybe.is_some(first([])))
    f: (Int) -> Int = fn x -> x + 1
    held = (1, Ok(f)).second
    io.inspect(held)
end
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
	const src = `import std::io

fn main()
    io.inspect(Some([]))
    io.inspect(Some(Set[]))
    io.inspect(Ok([]))
    io.inspect(Some(Some([])))
    io.inspect(Some({}))
    io.inspect(Some(Vector[]))
    io.inspect({})
    io.inspect(Map.size({}))
    io.inspect({} |> Map.size())
    io.inspect(Map.keys({}) |> Iter.to_list())
    io.inspect([{}])
    io.inspect(({}, 1))
    io.inspect({} == {})
    io.inspect(Set[] != Set[])
    io.inspect(Vector[] == Vector[])
    io.print("${{}}")
    io.inspect(Set.size(Set[]))
    io.inspect(Set.union(Set[], Set[]))
    io.inspect(Vector.length(Vector[]))
    io.inspect(Iter.zip([], []) |> Iter.to_list())
    io.inspect(Iter.zip([], [1]) |> Iter.count())
    io.inspect(Iter.concat([], []) |> Iter.to_list())
    io.inspect(Iter.concat(Set[], []) |> Iter.to_list())
    io.inspect(Iter.to_list(Vector[]))
    io.inspect(Iter.count({}))
    fn apply<T, U>(x: T, f: (T) -> U) -> U
        f(x)
    end
    io.inspect(apply(1, fn x -> Ok(x + 1)))
end
`
	const want = "Some([])\n" +
		"Some(Set[])\n" +
		"Ok([])\n" +
		"Some(Some([]))\n" +
		"Some({})\n" +
		"Some(Vector[])\n" +
		"{}\n" +
		"0\n" +
		"0\n" +
		"[]\n" +
		"[{}]\n" +
		"({}, 1)\n" +
		"True\n" +
		"False\n" +
		"True\n" +
		"{}\n" +
		"0\n" +
		"Set[]\n" +
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

// `dbg` of an empty literal nothing types shows the literal.
func TestIRUndeterminedTypeArgument_DbgOfAnOpenEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `fn main()
    dbg {}
    dbg Set[]
    dbg Vector[]
end
`
	if _, err := AnalyzeSource("entry.nomi", src); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	got := vmReference(writeTemp(t, src))
	want := "dbg line 2: {} = {}\ndbg line 3: Set[] = Set[]\ndbg line 4: Vector[] = Vector[]\n"
	if got.stdout+got.stderr != want || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}

// `Map.get({}, 1)`, whose key fixes K and leaves V open, and
// `Iter.zip([], {})` run by the same fill rule. A Map, a Range or
// a user `Iter` as `Iter.zip`'s or `Iter.concat`'s second operand is viewed
// as it is as the first.
func TestIRUndeterminedTypeArgument_MapShapesRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Countdown
    from: Int
end

impl Iter for Countdown
    fn each_while(c: Countdown, yield: (Int) -> Bool) -> Bool
        if c.from == 0 then
            True
        else if yield(c.from) then
            each_while(Countdown{from: c.from - 1}, yield)
        else
            False
        end
    end
end

fn main()
    io.inspect(Map.get({}, 1))
    io.inspect(Map.get({}, "k") == None)
    io.inspect(Iter.zip([], {}) |> Iter.to_list())
    io.inspect(Iter.zip([1], {}) |> Iter.count())
    io.inspect(Iter.zip([1], {1 => 2}) |> Iter.to_list())
    io.inspect(Iter.zip([1, 2], 5..9) |> Iter.to_list())
    io.inspect(Iter.zip(["a", "b"], Countdown{from: 3}) |> Iter.to_list())
    io.inspect(Iter.concat([(1, 2)], {3 => 4}) |> Iter.to_list())
    io.inspect(Iter.concat([9], Countdown{from: 2}) |> Iter.to_list())
end
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
