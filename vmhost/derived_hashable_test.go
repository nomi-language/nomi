package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A type-qualified call of a derived Hashable or Equatable, which writes no
// function whose bound would check it, is refused for a type argument with
// no equality as its interface-qualified spelling is: `Option.hash(x)` as
// `Hashable.hash(x)`, and a program's generic type's too.
func TestDerivedImpls_ATypeQualifiedCallOverAFunctionIsRefused(t *testing.T) {
	for _, c := range []struct{ call, want string }{
		{"Option.hash(Some(inc))", "Option<(Int) -> Int> does not implement Hashable (required by `Option.hash`)"},
		{"Option.equal(Some(inc), None)", "Option<(Int) -> Int> does not implement Equatable (required by `Option.equal`)"},
		{"Box.hash(Box{v: inc})", "Box<(Int) -> Int> does not implement Hashable (required by `Box.hash`)"},
		{"Box.equal(Box{v: inc}, Box{v: inc})", "Box<(Int) -> Int> does not implement Equatable (required by `Box.equal`)"},
	} {
		src := "struct Box<T>\n    v: T\nend\n\nderive Equatable for Box\nderive Hashable for Box\n\n" +
			"fn inc(n: Int) -> Int\n    n + 1\nend\n\nfn main()\n    _ = " + c.call + "\nend\n"
		_, err := vmhost.LoadSource("main.nomi", src)
		if err == nil {
			t.Fatalf("%s: the front end accepts a call over a function:\n%s", c.call, src)
		}
		if msg := err.Error(); !strings.Contains(msg, c.want) || !strings.Contains(msg, "a function value has no equality") {
			t.Errorf("%s: %s\nwant %q with the help that a function has no equality", c.call, msg, c.want)
		}
	}
}

// A derived Hashable writes no function: a value hashes by its shape, as a
// Map key of its type does, each part through its own impl. Every way a
// program asks for the hash answers the same Int: `Hashable.hash`,
// `T.hash` called and named as a value, a bound, a value held as Hashable
// and one held as another interface (ways, which counts the distinct
// answers). Equal values hash alike, so a Set and a Map key them once:
// the values hold a part with a written Equatable (Name, case-insensitive,
// which hashes by its type alone), a NaN Float and both zeros, a Decimal
// at two scales, an enum's variants and payloads, a distinct, a marker
// and a generic type. A type with a written Equatable and a derived
// Hashable (Loose) hashes all its values alike, so its hash agrees with
// its equality. Unequal values of the rest do not all collide.
func TestDerivedHashable_IsTheKeyHash(t *testing.T) {
	got := runSourceOutput(t, `import std::io

struct Name
    text: String
end

impl Equatable for Name
    fn equal(a: Name, b: Name) -> Bool
        String.lowercase(a.text) == String.lowercase(b.text)
    end
end

enum Tag
    Plain
    Named(Name)
    Measured(Float)
end

derive Equatable for Tag
derive Hashable for Tag

type Alias Name

derive Equatable for Alias
derive Hashable for Alias

struct Spot
    name: Name
    at: Float
    cost: Decimal
    tag: Tag
end

derive Equatable for Spot
derive Hashable for Spot

struct Cell<T>
    item: T
    n: Int
end

derive Equatable for Cell
derive Hashable for Cell

type Done

derive Equatable for Done
derive Hashable for Done

struct Loose
    n: Int
end

impl Equatable for Loose
    fn equal(a: Loose, b: Loose) -> Bool
        a.n % 10 == b.n % 10
    end
end

derive Hashable for Loose

interface Labeled
    fn label(value: self) -> String
end

impl Labeled for Spot
    fn label(s: Spot) -> String
        s.name.text
    end
end

fn digest<T>(value: T) -> Int where T: Hashable
    Hashable.hash(value)
end

fn held(h: Hashable) -> Int
    Hashable.hash(h)
end

fn ways(s: Spot) -> Int
    f = Spot.hash
    l: Labeled = s
    Set.size(Set[Hashable.hash(s), Spot.hash(s), f(s), digest(s), held(s), Hashable.hash(l)])
end

fn row(a: Spot, b: Spot) -> String
    "${ways(a)} ${ways(b)} ${Hashable.hash(a) == Hashable.hash(b)} ${Set.size(Set[a, b])} ${Map.size({a => 1, b => 2})}"
end

fn main()
    nan = Float.nan()
    base = Spot{name: Name{text: "a"}, at: 0.0, cost: 1.5d, tag: Tag.Plain}
    io.print(row(base, Spot{name: Name{text: "A"}, at: -0.0, cost: 1.50d, tag: Tag.Plain}))
    io.print(row(base, Spot{name: Name{text: "a"}, at: 1.0, cost: 1.5d, tag: Tag.Plain}))
    io.print(row(Spot{name: Name{text: "a"}, at: nan, cost: 1.5d, tag: Tag.Measured(nan)}, Spot{name: Name{text: "a"}, at: nan, cost: 1.5d, tag: Tag.Measured(nan)}))
    io.print(row(base, Spot{name: Name{text: "a"}, at: 0.0, cost: 2d, tag: Tag.Plain}))
    io.print(row(Spot{name: Name{text: "a"}, at: 0.0, cost: 1.5d, tag: Tag.Named(Name{text: "x"})}, Spot{name: Name{text: "a"}, at: 0.0, cost: 1.5d, tag: Tag.Named(Name{text: "X"})}))
    tags = [Tag.Plain, Tag.Measured(1.0), Tag.Measured(2.0), Tag.Named(Name{text: "x"})]
    io.print("${Iter.map(tags, fn t -> Hashable.hash(t)) |> Iter.to_set() |> Set.size()}")
    io.print("${Hashable.hash(Alias(Name{text: "a"})) == Alias.hash(Alias(Name{text: "A"}))} ${digest(Cell{item: Name{text: "a"}, n: 1}) == Hashable.hash(Cell{item: Name{text: "A"}, n: 1})} ${Cell.hash(Cell{item: 1, n: 1}) == Cell.hash(Cell{item: 2, n: 1})}")
    io.print("${Hashable.hash(Done) == Done.hash(Done)} ${Set.size(Set[Done, Done])}")
    io.print("${Hashable.hash(Loose{n: 1}) == Hashable.hash(Loose{n: 11})} ${Hashable.hash(Loose{n: 1}) == Hashable.hash(Loose{n: 2})} ${Set.size(Set[Loose{n: 1}, Loose{n: 11}, Loose{n: 2}])}")
end
`)
	want := `1 1 True 1 1
1 1 False 2 2
1 1 True 1 1
1 1 False 2 2
1 1 True 1 1
4
True True False
True 1
True True 2
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
