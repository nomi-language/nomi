package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// writtenEqualityMeasure declares Op, which holds a function and
// implements Unit2, and Measure<P>, whose written Equatable and Hashable
// ask only `P: Unit2` and compare and hash the amount alone. Bare
// implements nothing.
const writtenEqualityMeasure = `import std::io

interface Unit2
    fn symbol(unit: self) -> String
end

struct Op
    run: (Int) -> Int
end

impl Unit2 for Op
    fn symbol(_unit: Op) -> String
        "op"
    end
end

struct Bare
    n: Int
end

struct Measure<P>
    amount: Int
    unit: P
end

impl Equatable for Measure<P> where P: Unit2
    fn equal(a: Measure<P>, b: Measure<P>) -> Bool
        a.amount == b.amount
    end
end

impl Hashable for Measure<P> where P: Unit2
    fn hash(m: Measure<P>) -> Int
        m.amount
    end
end

struct Box<T>
    value: T
end
`

// A hand-written Equatable and Hashable decide what is compared, so a
// Measure<Op>, whose Op holds a function, is compared and hashed through
// them wherever it sits: `==`, `in`, a List and an Option compared with
// `==`, a type with no impl over it, a Map key and a Set element. The
// written bodies ignore the function, which nothing compares: the two
// Measures of amount 3 are equal though their functions differ.
func TestWrittenImplEquality_RunsThroughTheWrittenBody(t *testing.T) {
	body := `a = Measure{amount: 3, unit: Op{run: fn x -> x + 1}}
    b = Measure{amount: 3, unit: Op{run: fn x -> x * 2}}
    c = Measure{amount: 4, unit: Op{run: fn x -> x}}
    io.inspect(a == b)
    io.inspect(a == c)
    io.inspect([a, c] == [b, c])
    io.inspect([a] == [c])
    io.inspect(a in [c, b])
    io.inspect(Some(a) == Some(b))
    io.inspect(Box{value: a} == Box{value: b})
    io.inspect(Equatable.equal(a, b))
    m = {a => "three", c => "four"}
    io.inspect(Map.get(m, b))
    io.inspect(Map.size(Map.put(m, b, "again")))
    s = Set[a, b, c]
    io.inspect(Set.size(s))
    io.inspect(Set.contains(s, b))
    io.inspect(Hashable.hash(a) == Hashable.hash(b))`
	want := "True\nFalse\nTrue\nFalse\nTrue\nTrue\nTrue\nTrue\nSome(\"three\")\n2\n2\nTrue\nTrue\n"
	got := runSourceOutput(t, writtenEqualityMeasure+"\nfn main()\n    "+body+"\nend\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A written impl's where clause is still asked, and a value with no
// equality outside a written impl stays refused: a function in a List,
// compared by `List.equal`, `==` or `in`, in an Option, in a type with no
// impl, and as the field a derive compares.
func TestWrittenImplEquality_StillRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, decls, body, want string
	}{
		{"the where clause unmet", "", `a = Measure{amount: 1, unit: Bare{n: 0}}
    io.inspect(Equatable.equal(a, a))`, "no impl of `Unit2` for `Bare` (required by the call `Equatable.equal`)"},
		{"List.equal over functions", "", `f: (Int) -> Int = fn x -> x
    io.inspect(List.equal([f], [f]))`, "List<(Int) -> Int> does not implement Equatable (required by `List.equal`)"},
		{"== over a List of functions", "", `f: (Int) -> Int = fn x -> x
    io.inspect([f] == [f])`, "`==` cannot compare `List<(Int) -> Int>`: it holds `(Int) -> Int`, and a function value has no equality"},
		{"in over functions", "", `f: (Int) -> Int = fn x -> x
    io.inspect(f in [f])`, "no impl of `Equatable` for `(Int) -> Int` (required via membership test `in`)"},
		{"== over an Option of a function", "", `f: (Int) -> Int = fn x -> x
    io.inspect(Some(f) == Some(f))`, "`==` cannot compare `Option<(Int) -> Int>`: it holds `(Int) -> Int`, and a function value has no equality"},
		{"a type with no impl over a function", "", `f: (Int) -> Int = fn x -> x
    io.inspect(Box{value: f} == Box{value: f})`, "`==` cannot compare `Box<(Int) -> Int>`: it holds `(Int) -> Int`, and a function value has no equality"},
		{"a derive over a function", "\nstruct Holder\n    f: (Int) -> Int\nend\n\nderive Equatable for Holder\n", `io.print("x")`,
			"no impl of `Equatable` for `(Int) -> Int` (required via `derive Equatable for Holder`, field `f`)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := writtenEqualityMeasure + tc.decls + "\nfn main()\n    " + tc.body + "\nend\n"
			_, err := vmhost.LoadSource("main.nomi", src)
			if err == nil {
				t.Fatalf("the front end admits the program; want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got:\n%v\nwant it to say %q", err, tc.want)
			}
		})
	}
}
