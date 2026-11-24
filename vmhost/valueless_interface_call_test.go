package vmhost_test

import (
	"strings"
	"testing"
)

// An interface function whose signature names no `self` (`fn symbol() ->
// String`) is called through its interface with the implementor as the type
// argument, `Unit2.symbol<Meters>()` (spec §8 *Calls and qualification*).
// The checker checked such a call as one with nothing to solve, so it read
// no type argument and recorded no implementor, and the builder refused
// every one. Each runs here: at a concrete type, at a generic instance, and
// at an impl's type parameter in each instance's body, for a generic struct,
// enum and distinct, with two parameters, through a generic function,
// through an interface default, and at a generic interface whose function
// names only its own parameter.
func TestValuelessInterfaceCall_TheTypeArgumentChoosesTheImpl(t *testing.T) {
	got := runSourceOutput(t, `import std::io

interface Unit2
    fn symbol() -> String
end

struct Meters
end

impl Unit2 for Meters
    fn symbol() -> String
        "m"
    end
end

struct Feet
end

impl Unit2 for Feet
    fn symbol() -> String
        "ft"
    end
end

struct Wrap<T>
    v: T
end

impl Unit2 for Wrap<T>
    fn symbol() -> String
        "w"
    end
end

interface Named
    fn name(value: self) -> String
end

struct Box<P>
    n: Int
end

impl Named for Box<P> where P: Unit2
    fn name(b: Box<P>) -> String
        "${b.n} ${Unit2.symbol<P>()}"
    end
end

enum Amount<P>
    Some {n: Int}
    Nothing
end

impl Named for Amount<P> where P: Unit2
    fn name(a: Amount<P>) -> String
        case a
            .Some{n:} -> "${n} ${Unit2.symbol<P>()}"
            .Nothing -> "no ${Unit2.symbol<P>()}"
        end
    end
end

struct Pair<A, B>
    a: A
end

impl Named for Pair<A, B> where A: Display, B: Unit2
    fn name(p: Pair<A, B>) -> String
        "${Display.to_string(p.a)} ${Unit2.symbol<B>()}"
    end
end

type Tagged<P> Int

fn sym<P>(_t: Tagged<P>) -> String where P: Unit2
    Unit2.symbol<P>()
end

impl Named for Tagged<P> where P: Unit2
    fn name(t: Tagged<P>) -> String
        Tagged(n) = t
        "${n} ${sym<P>(t)} ${sym(t)}"
    end
end

interface Labeled
    fn tag(value: self) -> Int

    fn label(value: self) -> String where self: Unit2
        "${Labeled.tag(value)}${unit_of(value)}"
    end
end

fn unit_of<U>(_u: U) -> String where U: Unit2
    Unit2.symbol<U>()
end

impl Labeled for Meters
    fn tag(_value: Meters) -> Int
        7
    end
end

interface Measured<P>
    fn amount(value: self) -> Int

    fn show(value: self) -> String where P: Unit2
        "${Measured.amount(value)}${Unit2.symbol<P>()}"
    end
end

struct Measure<P>
    n: Int
end

impl Measured<P> for Measure<P>
    fn amount(m: Measure<P>) -> Int
        m.n
    end
end

interface Scale<N>
    fn unit() -> N
end

struct Money
end

impl Scale<Int> for Money
    fn unit() -> Int
        100
    end
end

fn main()
    io.print(Unit2.symbol<Meters>())
    io.print(Unit2.symbol<Wrap<Int>>())
    bm: Box<Meters> = Box{n: 1}
    io.print(Named.name(bm))
    bf: Box<Feet> = Box{n: 2}
    io.print(Named.name(bf))
    some: Amount<Feet> = .Some{n: 3}
    io.print(Named.name(some))
    none: Amount<Meters> = .Nothing
    io.print(Named.name(none))
    pair: Pair<Int, Feet> = Pair{a: 4}
    io.print(Named.name(pair))
    tm: Tagged<Meters> = Tagged(5)
    io.print(Named.name(tm))
    tw: Tagged<Wrap<Feet>> = Tagged(6)
    io.print(Named.name(tw))
    bw: Box<Wrap<Wrap<Meters>>> = Box{n: 7}
    io.print(Named.name(bw))
    io.print(Labeled.label(Meters{}))
    m: Measure<Feet> = Measure{n: 8}
    io.print(Measured.show(m))
    io.inspect(Scale.unit<Money>())
end
`)
	want := "m\nw\n1 m\n2 ft\n3 ft\nno m\n4 ft\n5 m m\n6 w w\n7 w\n7m\n8ft\n100\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The type argument of such a call is the implementor, so it is checked as
// one: a type with no impl of the interface is that error, a name that is
// no type is that error alone, and a call with no type argument outside the
// interface's defaults names no implementor. Each was admitted, and the
// program then stopped where it was built.
func TestValuelessInterfaceCall_TheTypeArgumentIsChecked(t *testing.T) {
	const decls = `import std::io

interface Unit2
    fn symbol() -> String
end

struct Meters
end

impl Unit2 for Meters
    fn symbol() -> String
        "m"
    end
end

fn main()
`
	src := func(call string) string { return decls + "    io.print(" + call + ")\nend\n" }
	rejects(t, src("Unit2.symbol<Int>()"),
		"main.nomi:17:31: no impl of `Unit2` for `Int`")
	rejects(t, src("Unit2.symbol()"),
		"main.nomi:17:14: `Unit2.symbol` has no `self` parameter, so this call does not say which implementor of Unit2 runs")
	rejects(t, src("Unit2.symbol<Nope>()"), `main.nomi:17:27: unknown type "Nope"`)
	if err := checkEntry(t, src("Unit2.symbol<Nope>()")); err != nil && strings.Contains(err.Error(), "has no `self` parameter") {
		t.Errorf("a type argument that names no type is reported again as naming no implementor:\n%v", err)
	}
	admits(t, src("Unit2.symbol<Meters>()"))
}

// A pipe stage binds its type arguments as the direct call does: `2 |>
// pick<String>()` was "not locally determined", since the stage ignored
// them, and `4 |> Unit2.times<Meters>()`, whose implementor only the type
// argument names, was admitted and then refused where the program was
// built.
func TestValuelessInterfaceCall_APipeStageBindsItsTypeArguments(t *testing.T) {
	got := runSourceOutput(t, `import std::io

interface Unit2
    fn times(n: Int) -> String
end

struct Meters
end

impl Unit2 for Meters
    fn times(n: Int) -> String
        "${n}m"
    end
end

fn pick<T>(_n: Int) -> Option<T>
    None
end

fn main()
    a = 2 |> pick<String>()
    io.inspect(a)
    io.print(4 |> Unit2.times<Meters>())
end
`)
	if want := "None\n4m\n"; got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A function of an interface impl on a generic type may use the block's
// type parameter though its signature does not name it, since a call
// through the interface names the implementor (spec §4 *Generics*):
// `T.symbol()` was admitted and then refused where the program was built,
// and `Unit2.symbol<T>()` was `unknown type "T"`.
func TestValuelessInterfaceCall_AnImplsTypeParameterInEveryFunction(t *testing.T) {
	got := runSourceOutput(t, `import std::io

interface Unit2
    fn symbol() -> String
end

struct Meters
end

impl Unit2 for Meters
    fn symbol() -> String
        "m"
    end
end

struct Wrap<T>
    v: T
end

impl Unit2 for Wrap<T> where T: Unit2
    fn symbol() -> String
        "w${T.symbol()}"
    end
end

struct Pack<T>
    v: T
end

impl Unit2 for Pack<T> where T: Unit2
    fn symbol() -> String
        "p${Unit2.symbol<T>()}"
    end
end

fn main()
    io.print(Unit2.symbol<Wrap<Meters>>())
    io.print(Unit2.symbol<Pack<Wrap<Meters>>>())
end
`)
	if want := "wm\npwm\n"; got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An interface function with no `self` and type parameters of its own is
// called through the interface with the implementor first, then its own
// type arguments, or the implementor alone where the arguments determine
// the rest (spec §8 *Calls and qualification*), at a concrete implementor,
// a generic one, as a pipe stage and through a bounded type parameter.
// `Conv.conv<Money, Int>(5)` was "expected 1 type argument(s), got 2".
func TestValuelessInterfaceCall_TheImplementorComesBeforeTheFunctionsOwn(t *testing.T) {
	got := runSourceOutput(t, `import std::io

interface Conv
    fn conv<U>(u: U) -> String where U: Display
end

struct Money
end

impl Conv for Money
    fn conv<U>(u: U) -> String where U: Display
        "money ${Display.to_string(u)}"
    end
end

struct Tag<P>
    n: Int
end

impl Conv for Tag<P> where P: Conv
    fn conv<U>(u: U) -> String where U: Display
        "tag ${Conv.conv<P>(u)}"
    end
end

fn via<T>(_t: T, n: Int) -> String where T: Conv
    T.conv(n)
end

fn main()
    io.print(Conv.conv<Money, Int>(5))
    io.print(Conv.conv<Money>("x"))
    io.print(5 |> Conv.conv<Money>())
    io.print(Conv.conv<Tag<Money>, Bool>(True))
    t: Tag<Money> = Tag{n: 1}
    io.print(via(t, 7))
end
`)
	if want := "money 5\nmoney x\nmoney 5\ntag money True\ntag money 7\n"; got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A call writes one type argument for each type parameter its callee
// takes, in every form, an operator interface's function by name and a
// pipe stage included, so `Int.add<Nope>(1, 2)` is two errors where it was
// admitted as `Int.add(1, 2)`.
func TestValuelessInterfaceCall_EveryCallCountsItsTypeArguments(t *testing.T) {
	src := func(call string) string {
		return "import std::io\n\nfn g() -> Int\n    1\nend\n\nfn main()\n    io.inspect(" + call + ")\nend\n"
	}
	rejects(t, src("g<Nope>()"),
		"main.nomi:8:16: expected 0 type argument(s), got 1",
		`main.nomi:8:18: unknown type "Nope"`)
	rejects(t, src("Int.add<Nope>(1, 2)"),
		"main.nomi:8:16: expected 0 type argument(s), got 1",
		`main.nomi:8:24: unknown type "Nope"`)
	rejects(t, src("1 |> Int.add<Int>(2)"),
		"main.nomi:8:21: expected 0 type argument(s), got 1")
	rejects(t, src("1 |> g<Int>()"),
		"main.nomi:8:21: expected 0 type argument(s), got 1")
	admits(t, src("g()"))
}

// A call of an interface impl's function through its generic implementor's
// name names no instance of it when the function's signature leaves one of
// the implementor's type parameters unnamed: `Wrap.symbol()` for `fn
// symbol() -> String` in `impl Unit2 for Wrap<T>`, written or as a pipe
// stage. Each was admitted and then refused where the program was built.
// The error's help writes the call through the interface, which runs, and
// an inherent function whose signature names the parameter is typed by its
// arguments as before.
func TestValuelessInterfaceCall_ATypeQualifiedCallNamesItsInstance(t *testing.T) {
	const decls = `import std::io

interface Unit2
    fn symbol() -> String
    fn of(n: Int) -> String
end

struct Meters
end

impl Unit2 for Meters
    fn symbol() -> String
        "m"
    end

    fn of(n: Int) -> String
        "${n}m"
    end
end

struct Wrap<T>
    v: T
end

impl Unit2 for Wrap<T> where T: Unit2
    fn symbol() -> String
        "w${T.symbol()}"
    end

    fn of(n: Int) -> String
        "${n}w${T.symbol()}"
    end
end

impl Wrap<T>
    fn held(w: Wrap<T>) -> T
        w.v
    end
end

fn main()
`
	src := func(call string) string { return decls + "    io.print(" + call + ")\nend\n" }
	rejects(t, src("Wrap.symbol()"),
		"main.nomi:42:14: `Wrap.symbol` names no instance of `Wrap`, and its impl is for every `Wrap<T>`, so this call does not say which one it runs for",
		"`Unit2.symbol<Wrap<_>>()`, with the type for each `_`")
	rejects(t, src("2 |> Wrap.of()"),
		"main.nomi:42:19: `Wrap.of` names no instance of `Wrap`, and its impl is for every `Wrap<T>`, so this call does not say which one it runs for",
		"`Unit2.of<Wrap<_>>(...)`, with the type for each `_`")
	got := runSourceOutput(t, src(`"${Unit2.symbol<Wrap<Meters>>()} ${2 |> Unit2.of<Wrap<Meters>>()} ${Wrap.held(Wrap{v: Meters{}}) |> Debug.inspect()}"`))
	if want := "wm 2wm Meters{}\n"; got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
