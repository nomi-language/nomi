package irbuild

import "testing"

// A Range, a String, Bytes and a type's own `impl Iter` entering an
// `Iter<Display>` are viewed at their own element, each element already
// the interface's value: a parameter, an annotated binding.
func TestIRViewEntersAnInterfaceIter_Runs(t *testing.T) {
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

fn show(xs: Iter<Display>) -> List<String>
    xs |> Iter.map(&Display.to_string(&1)) |> Iter.to_list()
end

fn main()
    io.inspect(show(1..3))
    xs: Iter<Display> = 1..=2
    io.inspect(Iter.count(xs))
    io.inspect(show("ab"))
    io.inspect(show(Bytes.from_list([])))
    io.inspect(show(Countdown{from: 2}))
end
`
	irRunSource(t, src, "[\"1\", \"2\"]\n2\n[\"a\", \"b\"]\n[]\n[\"2\", \"1\"]\n")
}

// An Iter whose elements are already an interface's values is that
// interface's Iter unchanged (relabels): a stage's result bound at
// `Iter<Display>`, and a function's `Iter<Int>` passed to one.
func TestIRIterRelabelsIntoAnInterfaceIter_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn nums() -> Iter<Int>
    [1, 2] |> Iter.map(&(&1 * 3))
end

fn show(xs: Iter<Display>) -> List<String>
    xs |> Iter.map(&Display.to_string(&1)) |> Iter.to_list()
end

fn main()
    s: Iter<Display> = Iter.repeat(1) |> Iter.take(2)
    io.inspect(show(s))
    io.inspect(show(nums()))
end
`
	irRunSource(t, src, "[\"1\", \"1\"]\n[\"3\", \"6\"]\n")
}

// A Set whose elements are an interface's values enters an `Iter` of that
// interface as a Vector, a List, an Option, a Map, a Range and a String do
// (viewAs asks one question for every domain): as an argument, a named
// argument, a struct field, a function value's result, a lambda's result and
// an element of a list of sources.
func TestIRSetEntersAnInterfaceIter_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

interface Named
    fn name(value: self) -> String
end

struct P
    n: Int
end

impl Named for P
    fn name(p: P) -> String
        "p${p.n}"
    end
end

struct Holder
    xs: Iter<Display>
end

fn show(xs: Iter<Display>) -> String
    xs |> Iter.map(fn x -> "${x}") |> Iter.sort() |> String.join(",")
end

fn names(xs: Iter<Named>) -> String
    xs |> Iter.map(fn x -> Named.name(x)) |> Iter.sort() |> String.join(",")
end

fn two(a: Int, xs: Iter<Display> = []) -> String
    "${a}:${show(xs)}"
end

fn make() -> Set<Int>
    Set[7, 8]
end

fn call(f: () -> Iter<Display>) -> String
    show(f())
end

fn main()
    s: Set<Int> = [1, 2]
    io.print(show(s))
    b: Set<Bool> = [True]
    io.print(show(b))
    ps: Set<P> = [P{n: 1}, P{n: 2}]
    io.print(names(ps))
    io.print(two(xs: s, a: 1))
    k = Holder{xs: s}
    io.print(show(k.xs))
    io.print(call(make))
    f: (Int) -> Iter<Display> = fn _n -> s
    io.print(show(f(1)))
    a: List<Set<Int>> = [s, Set[3]]
    bb: List<Iter<Display>> = a
    io.print(bb |> Iter.map(show) |> String.join("|"))
    io.print(show(Vector[1, 2]))
    io.print(show([1, 2]))
    io.print(show(1..3))
    io.print(show({1 => 2}))
    io.print(show(Some(1)))
    io.print(show("ab"))
    io.print(show(Set["a", "b"]))
    io.print(names(Vector[P{n: 3}]))
    io.print(names([P{n: 4}]))
    io.print(names(Some(P{n: 5})))
end
`
	irRunSource(t, src, "1,2\n"+
		"True\n"+
		"p1,p2\n"+
		"1:1,2\n"+
		"1,2\n"+
		"7,8\n"+
		"1,2\n"+
		"1,2|3\n"+
		"1,2\n"+
		"1,2\n"+
		"1,2\n"+
		"(1, 2)\n"+
		"1\n"+
		"a,b\n"+
		"a,b\n"+
		"p3\n"+
		"p4\n"+
		"p5\n")
}

// A List, a Vector, a Set and an Option of a struct enter an `Iter` of a
// conjunction both of whose interfaces the struct implements.
func TestIRCollectionsEnterAConjunctionIter_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

interface Named
    fn name(value: self) -> String
end

interface Aged
    fn age(value: self) -> Int
end

struct P
    n: Int
end

impl Named for P
    fn name(p: P) -> String
        "p${p.n}"
    end
end

impl Aged for P
    fn age(p: P) -> Int
        p.n
    end
end

fn both(xs: Iter<Named and Aged>) -> String
    xs |> Iter.map(fn x -> "${Named.name(x)}=${Aged.age(x)}") |> Iter.sort() |> String.join(",")
end

fn main()
    io.print(both([P{n: 1}]))
    io.print(both(Vector[P{n: 2}]))
    ps: Set<P> = [P{n: 3}]
    io.print(both(ps))
    io.print(both(Some(P{n: 4})))
end
`
	irRunSource(t, src, "p1=1\n"+
		"p2=2\n"+
		"p3=3\n"+
		"p4=4\n")
}

// A source below the top of a type enters an `Iter` there as one at the top
// does, its walk read from its kind: a generic struct's field, a tuple's
// part, an Option's payload, a Map's value and the error of a `?`, for a
// Vector, a Set, a List, a String, a Range, an Option and a type's own `impl
// Iter`.
func TestIRViewBelowTheTopOfAType_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn show(xs: Iter<Display>) -> String
    xs |> Iter.map(fn x -> "${x}") |> Iter.sort() |> String.join(",")
end

struct Box<T>
    v: T
end

struct Countdown
    from: Int
end

impl Iter<Int> for Countdown
    fn each_while(c: Countdown, yield: (Int) -> Bool) -> Bool
        Iter.each_while(1..c.from, yield)
    end
end

fn open(b: Box<Iter<Display>>) -> String
    show(b.v)
end

fn first(p: (Iter<Display>, Int)) -> String
    (xs, n) = p
    "${n}:${show(xs)}"
end

fn option(m: Option<Iter<Display>>) -> String
    case m
        Some(xs) -> show(xs)
        None     -> "none"
    end
end

fn each(m: Map<String, Iter<Display>>) -> String
    m |> Iter.map(fn (k, v) -> "${k}=${show(v)}") |> Iter.sort() |> String.join(";")
end

fn r(c: Bool) -> Result<Int, Iter<Display>>
    e: Result<Int, Vector<Int>> = if c then Err(Vector[1, 2]) else Ok(5)
    n = e?
    s: Result<Int, Set<Int>> = Err(Set[9])
    m = s?
    Ok(n + m)
end

fn rs() -> Result<Int, Iter<Display>>
    e: Result<Int, String> = Err("ab")
    n = e?
    Ok(n)
end

fn rr() -> Result<Int, Iter<Display>>
    e: Result<Int, Range<Int>> = Err(1..3)
    n = e?
    Ok(n)
end

fn rc() -> Result<Int, Iter<Display>>
    e: Result<Int, Countdown> = Err(Countdown{from: 2})
    n = e?
    Ok(n)
end

fn report(x: Result<Int, Iter<Display>>) -> String
    case x
        Ok(n)  -> "ok ${n}"
        Err(e) -> "err ${show(e)}"
    end
end

fn main()
    b = Box{v: Vector[1, 2]}
    io.print(open(b))
    bs = Box{v: Set[3, 4]}
    io.print(open(bs))
    bl = Box{v: [5, 6]}
    io.print(open(bl))
    bstr = Box{v: "ab"}
    io.print(open(bstr))
    br = Box{v: 1..3}
    io.print(open(br))
    bm = Box{v: Some(7)}
    io.print(open(bm))
    bc = Box{v: Countdown{from: 3}}
    io.print(open(bc))
    bb: Box<Iter<Display>> = b
    io.print(open(bb))
    t = (Vector[1], 2)
    io.print(first(t))
    ts = (Set[1], 2)
    io.print(first(ts))
    mv = Some(Vector[8])
    io.print(option(mv))
    ms = Some(Set[8, 9])
    io.print(option(ms))
    mm = {"a" => Vector[1], "b" => Vector[2, 3]}
    io.print(each(mm))
    io.print(report(r(True)))
    io.print(report(r(False)))
    io.print(report(rs()))
    io.print(report(rr()))
    io.print(report(rc()))
end
`
	irRunSource(t, src, "1,2\n"+
		"3,4\n"+
		"5,6\n"+
		"a,b\n"+
		"1,2\n"+
		"7\n"+
		"1,2\n"+
		"1,2\n"+
		"2:1\n"+
		"2:1\n"+
		"8\n"+
		"8,9\n"+
		"a=1;b=2,3\n"+
		"err 1,2\n"+
		"err 9\n"+
		"err a,b\n"+
		"err 1,2\n"+
		"err 1\n")
}
