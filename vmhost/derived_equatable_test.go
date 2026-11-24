package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A derived Equatable writes no function: two values compare by their
// shape, as `==` compares a type with no impl, each part through its own
// impl. Every way a program asks for the equality answers the same: `==`
// and `!=`, `Equatable.equal`, `T.equal` called and named as a value, a
// bound, an interface value, `in`, `List.contains`, a Set's elements, a
// Map's keys and `Iter.to_set`. The values hold a part with a written
// impl (Name, case-insensitive), a NaN Float (equal to itself), a Decimal
// at two scales (equal), an enum's variants and payloads, a distinct, a
// marker, an `embeds` variant and a generic type.
func TestDerivedEquatable_IsTheShape(t *testing.T) {
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

type Alias Name

derive Equatable for Alias

struct Spot
    name: Name
    at: Float
    cost: Decimal
    tag: Tag
end

derive Equatable for Spot

struct Cell<T>
    item: T
    n: Int
end

derive Equatable for Cell

type Done

derive Equatable for Done

struct Circle
    r: Int
end

derive Equatable for Circle

enum Shape
    embeds Circle
    Dot
end

derive Equatable for Shape

interface Labeled
    fn label(value: self) -> String
end

impl Labeled for Spot
    fn label(s: Spot) -> String
        s.name.text
    end
end

fn same<T>(a: T, b: T) -> Bool where T: Equatable
    a == b
end

fn row(a: Spot, b: Spot) -> String
    f = Spot.equal
    la: Labeled = a
    lb: Labeled = b
    "${a == b} ${a != b} ${Equatable.equal(a, b)} ${Spot.equal(a, b)} ${f(a, b)} ${same(a, b)} ${la == lb} ${a in [b]} ${List.contains([b], a)} ${Set.size(Set[a, b])} ${Map.size({a => 1, b => 2})} ${Iter.to_set([a, b]) |> Set.size()}"
end

fn main()
    nan = Float.nan()
    base = Spot{name: Name{text: "a"}, at: 1.0, cost: 1.5d, tag: Tag.Plain}
    io.print(row(base, Spot{name: Name{text: "A"}, at: 1.0, cost: 1.50d, tag: Tag.Plain}))
    io.print(row(base, Spot{name: Name{text: "b"}, at: 1.0, cost: 1.5d, tag: Tag.Plain}))
    io.print(row(Spot{name: Name{text: "a"}, at: nan, cost: 1.5d, tag: Tag.Measured(nan)}, Spot{name: Name{text: "a"}, at: nan, cost: 1.5d, tag: Tag.Measured(nan)}))
    io.print(row(base, Spot{name: Name{text: "a"}, at: 1.0, cost: 2d, tag: Tag.Plain}))
    io.print(row(Spot{name: Name{text: "a"}, at: 1.0, cost: 1.5d, tag: Tag.Named(Name{text: "x"})}, Spot{name: Name{text: "a"}, at: 1.0, cost: 1.5d, tag: Tag.Named(Name{text: "X"})}))
    io.print(row(base, Spot{name: Name{text: "a"}, at: 1.0, cost: 1.5d, tag: Tag.Measured(1.0)}))
    io.print("${Alias(Name{text: "a"}) == Alias(Name{text: "A"})} ${Alias.equal(Alias(Name{text: "a"}), Alias(Name{text: "b"}))}")
    io.print("${Cell{item: Name{text: "a"}, n: 1} == Cell{item: Name{text: "A"}, n: 1}} ${Cell.equal(Cell{item: 1, n: 1}, Cell{item: 2, n: 1})} ${same(Cell{item: "x", n: 2}, Cell{item: "x", n: 2})}")
    io.print("${Done == Done} ${Done.equal(Done, Done)}")
    c: Shape = Circle{r: 1}
    io.print("${c == Circle{r: 1}} ${Shape.equal(c, Shape.Dot)} ${[c] == [Circle{r: 1}]} ${Set.size(Set[c, Circle{r: 1}, Shape.Dot])}")
end
`)
	want := `True False True True True True True True True 1 1 1
False True False False False False False False False 2 2 2
True False True True True True True True True 1 1 1
False True False False False False False False False 2 2 2
True False True True True True True True True 1 1 1
False True False False False False False False False 2 2 2
True False
True False True
True True
True False True 2
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// `T.to_string(x)` and `T.equal(a, b)` on a generic type whose derived
// impl's bound the argument does not meet report the missing impl once, at
// the call, as the interface-qualified call does: the type-qualified
// demand names the interface's declaration, so the demand it carries to
// the type argument and the one the impl's bound records are one, with no
// "also required here" note at the same position.
func TestDerivedImpls_ATypeQualifiedCallReportsAMissingImplOnce(t *testing.T) {
	for _, c := range []struct{ derive, call, want string }{
		{"Display", "_ = Box.to_string(Box{v: Plain{n: 1}})", "no impl of `Display` for `Plain` (required by the call `Box.to_string`)"},
		{"Equatable", "_ = Box.equal(Box{v: Op{f: inc}}, Box{v: Op{f: inc}})", "no impl of `Equatable` for `Op` (required by the call `Box.equal`)"},
	} {
		src := "struct Box<T>\n    v: T\nend\n\nderive " + c.derive + " for Box\n\n" +
			"struct Plain\n    n: Int\nend\n\nstruct Op\n    f: (Int) -> Int\nend\n\n" +
			"fn inc(n: Int) -> Int\n    n + 1\nend\n\nfn main()\n    _ = Plain{n: 1}.n\n    _ = Op{f: inc}.f\n    " + c.call + "\nend\n"
		_, err := vmhost.LoadSource("main.nomi", src)
		if err == nil {
			t.Fatalf("%s: the front end accepts a call whose argument lacks the impl:\n%s", c.derive, src)
		}
		if msg := err.Error(); !strings.Contains(msg, c.want) || strings.Count(msg, "no impl of") != 1 || strings.Contains(msg, "also required here") {
			t.Errorf("%s: %s\nwant %q once, with no repeated position", c.derive, msg, c.want)
		}
	}
}
