package vmhost_test

import (
	"strings"
	"testing"
)

// A generic distinct type's value held as an interface value dispatches
// through its instance's impl, as a generic struct's does: its descriptor
// carries the instance key (`main.Wrap<Int>`) its impls are recorded
// under. Before, its values carried none, no impl of an instance was
// recorded, and the program was blocked before it ran: "Named.name
// dispatches on main.Wrap, and this program's modules did not retain an
// implementation for it". Here: a user interface, a written Hashable, a
// Comparable through a bound, and the value inside an Option and a tuple
// held as interface values.
func TestGenericInstanceDispatch_ADistinctDispatchesThroughItsInstance(t *testing.T) {
	got := runSourceOutput(t, `import std::io

type Wrap<T> T

interface Named
    fn name(value: self) -> String
end

impl Named for Wrap<T> where T: Display
    fn name(w: Wrap<T>) -> String
        Wrap(n) = w
        "wrap ${n}"
    end
end

impl Hashable for Wrap<T> where T: Hashable
    fn hash(w: Wrap<T>) -> Int
        Wrap(n) = w
        Hashable.hash(n) + 100
    end
end

impl Equatable for Wrap<T> where T: Equatable
    fn equal(a: Wrap<T>, b: Wrap<T>) -> Bool
        Wrap(x) = a
        Wrap(y) = b
        Equatable.equal(x, y)
    end
end

impl Comparable for Wrap<T> where T: Comparable
    fn compare(a: Wrap<T>, b: Wrap<T>) -> Ordering
        Wrap(x) = a
        Wrap(y) = b
        Comparable.compare(y, x)
    end
end

fn larger<T>(a: T, b: T) -> T where T: Comparable
    if T.compare(a, b) == .Greater then a else b
end

fn main()
    n: Named = Wrap(3)
    io.print(Named.name(n))
    s: Named = Wrap("x")
    io.print(Named.name(s))
    h: Hashable = Wrap(4)
    io.inspect(Hashable.hash(h) == Hashable.hash(4) + 100)
    Wrap(picked) = larger(Wrap(1), Wrap(2))
    io.inspect(picked)
    held: Option<Named> = Some(Wrap(9))
    case held
        Some(v) -> io.print(Named.name(v))
        None -> io.print("none")
    end
    pair: (Named, Named) = (Wrap(1), Wrap("y"))
    io.print(Named.name(pair.second))
end
`)
	want := "wrap 3\nwrap x\nTrue\n1\nwrap 9\nwrap y\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A phantom generic distinct's instances differ in their type arguments
// alone (`Tagged<Meters>` and `Tagged<Feet>` are both an Int), and each
// dispatches through its own instance's body: the instance key is the type
// arguments, not the inner type.
func TestGenericInstanceDispatch_PhantomInstancesDispatchApart(t *testing.T) {
	got := runSourceOutput(t, `import std::io

type Tagged<P> Int

struct Meters
end

struct Feet
end

interface Unit2
    fn symbol(of: Tagged<self>) -> String
end

impl Unit2 for Meters
    fn symbol(_of: Tagged<Meters>) -> String
        "m"
    end
end

impl Unit2 for Feet
    fn symbol(_of: Tagged<Feet>) -> String
        "ft"
    end
end

interface Named
    fn name(value: self) -> String
end

impl Named for Tagged<P> where P: Unit2
    fn name(t: Tagged<P>) -> String
        Tagged(n) = t
        "${n} ${Unit2.symbol(t)}"
    end
end

fn names(xs: List<Named>) -> List<String>
    xs |> Iter.map(&Named.name(&1)) |> Iter.to_list()
end

fn main()
    a: Tagged<Meters> = Tagged(5)
    b: Tagged<Feet> = Tagged(6)
    io.inspect(names([a, b, a]))
end
`)
	want := "[\"5 m\", \"6 ft\", \"5 m\"]\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The same phantom instances, each choosing its unit's function by the
// impl's type parameter alone: `Unit2.symbol<P>()` takes no value, so the
// type argument is the only thing that names the implementor, in each
// instance's body. The checker checked the call as one that names no
// `self`, recorded no implementor, and the builder refused it.
func TestGenericInstanceDispatch_PhantomInstancesChooseByTheTypeArgument(t *testing.T) {
	got := runSourceOutput(t, `import std::io

type Tagged<P> Int

struct Meters
end

struct Feet
end

interface Unit2
    fn symbol() -> String
end

impl Unit2 for Meters
    fn symbol() -> String
        "m"
    end
end

impl Unit2 for Feet
    fn symbol() -> String
        "ft"
    end
end

interface Named
    fn name(value: self) -> String
end

impl Named for Tagged<P> where P: Unit2
    fn name(t: Tagged<P>) -> String
        Tagged(n) = t
        "${n} ${Unit2.symbol<P>()}"
    end
end

fn names(xs: List<Named>) -> List<String>
    xs |> Iter.map(&Named.name(&1)) |> Iter.to_list()
end

fn main()
    a: Tagged<Meters> = Tagged(5)
    b: Tagged<Feet> = Tagged(6)
    io.inspect(names([a, b, a]))
end
`)
	want := "[\"5 m\", \"6 ft\", \"5 m\"]\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A Map key of a generic type with a written Hashable and Equatable hashes
// and compares through its own instance's bodies when another instance of
// the type has them too. They were recorded under the type's name alone,
// so one instance's keys ran the other's body: a struct's stopped with
// "inner: t4 holds String and received int64", and a distinct's the same
// way once its instances were recorded.
func TestGenericInstanceDispatch_MapKeysHashThroughTheirInstance(t *testing.T) {
	for _, c := range []struct{ name, decl, build, open string }{
		{"distinct", "type Wrap<T> T", "Wrap(%)", "Wrap(n) = w"},
		{"struct", "struct Wrap<T>\n    v: T\nend", "Wrap{v: %}", "n = w.v"},
	} {
		t.Run(c.name, func(t *testing.T) {
			build := func(v string) string { return strings.ReplaceAll(c.build, "%", v) }
			src := `import std::io

` + c.decl + `

interface Named
    fn name(value: self) -> String
end

fn inner<T>(w: Wrap<T>) -> T
    ` + c.open + `
    n
end

impl Named for Wrap<T> where T: Display
    fn name(w: Wrap<T>) -> String
        "wrap ${inner(w)}"
    end
end

impl Hashable for Wrap<T> where T: Hashable
    fn hash(w: Wrap<T>) -> Int
        Hashable.hash(inner(w))
    end
end

impl Equatable for Wrap<T> where T: Equatable
    fn equal(a: Wrap<T>, b: Wrap<T>) -> Bool
        Equatable.equal(inner(a), inner(b))
    end
end

fn main()
    io.print(Named.name(` + build(`"x"`) + `))
    ints = {` + build("1") + ` => "one", ` + build("2") + ` => "two"}
    io.inspect(Map.get(ints, ` + build("2") + `))
    strings = {` + build(`"a"`) + ` => 1}
    io.inspect(Map.get(strings, ` + build(`"a"`) + `))
end
`
			got := runSourceOutput(t, src)
			want := "wrap x\nSome(\"two\")\nSome(1)\n"
			if got != want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
