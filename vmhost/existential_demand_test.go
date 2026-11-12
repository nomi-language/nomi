package vmhost_test

import "testing"

// A value of interface type may hold any implementor, and an interface
// requires nothing of its implementors but its own functions. So using
// another interface on one is an error at the use, with a hint naming a type
// parameter bounded by both; before, `nomi check` accepted it and the
// program then could not run.
func TestExistentialDemand_AnotherInterfaceIsAnError(t *testing.T) {
	const scale = `interface Scale
    fn factor(s: self) -> Int
end

`
	for _, tc := range []struct {
		name, bad, good, want, help string
	}{
		{
			name: "interface call",
			bad:  "fn f(other: Scale) -> String\n    Display.to_string(other)\nend\n",
			good: "fn f<T>(other: T) -> String where T: Scale and Display\n    Display.to_string(other)\nend\n",
			want: "main.nomi:8:23: `other` is a `Scale`, and Scale does not require Display of its implementors",
			help: "take a type parameter bounded by both: `fn f<T>(other: T) -> String where T: Scale and Display`",
		},
		{
			name: "pipe",
			bad:  "fn f(other: Scale) -> String\n    other |> Display.to_string()\nend\n",
			good: "fn f<T>(other: T) -> String where T: Scale and Display\n    other |> Display.to_string()\nend\n",
			want: "main.nomi:8:5: `other` is a `Scale`, and Scale does not require Display of its implementors",
			help: "`fn f<T>(other: T) -> String where T: Scale and Display`",
		},
		{
			name: "interpolation",
			bad:  "fn f(other: Scale) -> String\n    \"<${other}>\"\nend\n",
			good: "fn f<T>(other: T) -> String where T: Scale and Display\n    \"<${other}>\"\nend\n",
			want: "main.nomi:8:9: `other` is a `Scale`, and Scale does not require Display of its implementors",
			help: "`fn f<T>(other: T) -> String where T: Scale and Display`",
		},
		{
			name: "interpolated list",
			bad:  "fn f(xs: List<Scale>) -> String\n    \"${xs}\"\nend\n",
			good: "fn f<T>(xs: List<T>) -> String where T: Scale and Display\n    \"${xs}\"\nend\n",
			want: "main.nomi:8:8: `xs` holds values of `Scale`, and Scale does not require Display of its implementors",
			help: "take a type parameter bounded by both instead of `Scale`: `where T: Scale and Display`",
		},
		{
			name: "ordering",
			bad:  "fn f(a: Scale, b: Scale) -> Bool\n    a < b\nend\n",
			good: "fn f<T>(a: T, b: T) -> Bool where T: Scale and Comparable\n    a < b\nend\n",
			want: "main.nomi:8:5: `a` is a `Scale`, and Scale does not require Comparable of its implementors: values of different types have no order",
			help: "`fn f<T>(a: T, b: T) -> Bool where T: Scale and Comparable`",
		},
		{
			name: "generic call",
			bad:  "fn shown<U>(u: U) -> String where U: Display\n    Display.to_string(u)\nend\n\nfn f(other: Scale) -> String\n    shown(other)\nend\n",
			good: "fn shown<U>(u: U) -> String where U: Display\n    Display.to_string(u)\nend\n\nfn f<T>(other: T) -> String where T: Scale and Display\n    shown(other)\nend\n",
			want: "main.nomi:12:11: `other` is a `Scale`, and Scale does not require Display of its implementors",
			help: "`fn f<T>(other: T) -> String where T: Scale and Display`",
		},
		{
			name: "piped generic call",
			bad:  "fn shown<U>(u: U) -> String where U: Display\n    Display.to_string(u)\nend\n\nfn f(other: Scale) -> String\n    other |> shown()\nend\n",
			good: "fn shown<U>(u: U) -> String where U: Display\n    Display.to_string(u)\nend\n\nfn f<T>(other: T) -> String where T: Scale and Display\n    other |> shown()\nend\n",
			want: "main.nomi:12:5: `other` is a `Scale`, and Scale does not require Display of its implementors",
			help: "`fn f<T>(other: T) -> String where T: Scale and Display`",
		},
		{
			name: "comparison through the interface",
			bad:  "fn f(a: Scale, b: Scale) -> Ordering\n    Comparable.compare(a, b)\nend\n",
			good: "fn f<T>(a: T, b: T) -> Ordering where T: Scale and Comparable\n    Comparable.compare(a, b)\nend\n",
			want: "main.nomi:8:24: `a` is a `Scale`, and Scale does not require Comparable of its implementors: values of different types have no order",
			help: "`fn f<T>(a: T, b: T) -> Ordering where T: Scale and Comparable`",
		},
		{
			name: "maximum",
			bad:  "fn f(xs: List<Scale>) -> Maybe<Scale>\n    Iter.max(xs)\nend\n",
			good: "fn f<T>(xs: List<T>) -> Maybe<T> where T: Scale and Comparable\n    Iter.max(xs)\nend\n",
			want: "main.nomi:8:14: `xs` holds values of `Scale`, and Scale does not require Comparable of its implementors: values of different types have no order",
			help: "`where T: Scale and Comparable`",
		},
		{
			name: "list elements",
			bad:  "fn f(xs: List<Scale>) -> List<Scale>\n    Iter.sort(xs)\nend\n",
			good: "fn f<T>(xs: List<T>) -> List<T> where T: Scale and Comparable\n    Iter.sort(xs)\nend\n",
			want: "main.nomi:8:15: `xs` holds values of `Scale`, and Scale does not require Comparable of its implementors: values of different types have no order",
			help: "`where T: Scale and Comparable`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := func(decl string) string {
				return "import std::io\n\n" + scale + decl + "\nfn main()\n    io.print(1)\nend\n"
			}
			rejects(t, src(tc.bad), tc.want, tc.help)
			admits(t, src(tc.good))
		})
	}
}

// Debug is universal, `==` compares the held values (unequal across concrete
// types), a Set holds them, and the interface's own functions are what it
// requires, so none of them is an error on a value of interface type; and the
// type parameter form the hint writes runs.
func TestExistentialDemand_AllowedUsesRun(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std::io

interface Scale
    fn factor(s: self) -> Int
end

struct Two
    n: Int
end

impl Scale for Two
    fn factor(t: Two) -> Int
        t.n * 2
    end
end

impl Display for Two
    fn to_string(t: Two) -> String
        "two ${t.n}"
    end
end

fn look(a: Scale, b: Scale) -> String
    "${Debug.inspect(a)} ${Scale.factor(a)} ${b |> Scale.factor()} ${a == b} ${Set.size(Set[a, b])}"
end

fn told<T>(other: T) -> String where T: Scale and Display
    "${other} x${Scale.factor(other)}"
end

fn main()
    io.print(look(Two{ n: 1 }, Two{ n: 1 }))
    io.print(told(Two{ n: 3 }))
end
`})
	if want := "Two{n: 1} 2 2 True 1\ntwo 3 x6\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// Every interface value is Equatable and Hashable, whatever it holds: a
// generic bound of either admits it, and each body instantiated at the
// interface compares and hashes the held values as `==` and a Set do
// (unequal across concrete types, through a hand-written Equatable
// otherwise). Comparable is not, so the ordering cases above stay errors.
func TestExistentialDemand_EquatableAndHashableBoundsRun(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std::io

interface Scale
    fn factor(s: self) -> Int
end

struct Two
    n: Int
end

struct Pair
    n: Int
end

// Words compare without regard to case.
struct Word
    text: String
end

impl Scale for Two
    fn factor(t: Two) -> Int
        t.n * 2
    end
end

impl Scale for Pair
    fn factor(p: Pair) -> Int
        p.n * 2
    end
end

impl Scale for Word
    fn factor(w: Word) -> Int
        String.length(w.text)
    end
end

impl Scale for Int
    fn factor(n: Int) -> Int
        n
    end
end

impl Scale for List<T>
    fn factor(xs: List<T>) -> Int
        Iter.count(xs)
    end
end

impl Equatable for Word
    fn equal(a: Word, b: Word) -> Bool
        String.lowercase(a.text) == String.lowercase(b.text)
    end
end

impl Hashable for Word
    fn hash(w: Word) -> Int
        Hashable.hash(String.lowercase(w.text))
    end
end

struct Keyed
    scale: Scale
    n: Int
end

derive Equatable for Keyed
derive Hashable for Keyed

fn has<T>(xs: List<T>, x: T) -> Bool where T: Equatable
    List.contains(xs, x)
end

fn same<T>(a: T, b: T) -> Bool where T: Equatable
    Equatable.equal(a, b)
end

fn kinds<T>(xs: List<T>) -> Int where T: Hashable
    xs |> Iter.to_set() |> Set.size()
end

fn hashes_alike<T>(a: T, b: T) -> Bool where T: Hashable
    Hashable.hash(a) == Hashable.hash(b)
end

fn main()
    scales: List<Scale> = [Two{n: 1}, Word{text: "Ann"}, 1, [1, 2]]
    pair: Scale = Pair{n: 1}
    word: Scale = Word{text: "ANN"}
    two: Scale = Two{n: 1}
    io.print("${List.contains(scales, pair)} ${List.contains(scales, word)} ${Iter.contains(scales, word)}")
    io.print("${has(scales, word)} ${same(pair, two)} ${Equatable.equal(word, word)}")
    lower: Scale = Word{text: "ann"}
    io.print("${kinds([pair, two, word, lower])} ${hashes_alike(word, lower)}")
    io.print("${scales |> Iter.chunk_by(fn s -> s) |> Iter.count()}")
    m = {Keyed{scale: word, n: 1} => "found"}
    io.print(Map.get(m, Keyed{scale: lower, n: 1}))
    io.print(Map.get(m, Keyed{scale: two, n: 1}))
end
`})
	if want := "False True True\nTrue False True\n3 True\n4\nSome(found)\nNone\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
