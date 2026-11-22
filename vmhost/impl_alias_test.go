package vmhost_test

import "testing"

// An impl whose header names an interface through an import alias is that
// interface's impl when the program runs: operators, interpolation,
// rendering, sorting, a bound, a qualified call and an interface value
// each reach its body. A program's own interface under another name, in
// either file order, runs the same way.
func TestImplAlias_StdInterfacesUnderOtherNamesRunTheirImpls(t *testing.T) {
	out, err := runVM(t, `import
    std::io
    std::comparable.{Comparable as Ord}
    std::debug.{Debug as Dbg}
    std::display.{Display as Show}
    std::equatable.{Equatable as Eq}
    std::resources.{Release as Freeable}
end

struct P
    x: Int
end

impl Eq for P
    fn equal(a: P, b: P) -> Bool
        a.x % 10 == b.x % 10
    end
end

impl Ord for P
    fn compare(a: P, b: P) -> Ordering
        Int.compare(a.x % 10, b.x % 10)
    end
end

impl Show for P
    fn to_string(p: P) -> String
        "P${p.x}"
    end
end

impl Dbg for P
    fn inspect(p: P) -> String
        "<P ${p.x}>"
    end
end

impl Freeable for P
    fn release(p: P)
        io.print("released ${p.x}")
    end
end

fn free<T>(x: T) where T: Freeable
    Freeable.release(x)
end

fn main()
    io.print(P{x: 1} == P{x: 11})
    io.print(P{x: 2} < P{x: 11})
    io.print("${P{x: 5}}")
    io.print(Debug.inspect([P{x: 7}]))
    io.print([P{x: 3}, P{x: 12}] |> Iter.sort() |> Iter.map(&Show.to_string(&1)) |> String.join(","))
    free(P{x: 4})
    r: Freeable = P{x: 6}
    Release.release(r)
end
`)
	if err != nil {
		t.Fatalf("run: %v\noutput so far:\n%s", err, out)
	}
	want := "True\nFalse\nP5\n[<P 7>]\nP12,P3\nreleased 4\nreleased 6\n"
	if out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestImplAlias_AProgramInterfaceUnderAnotherNameRunsItsImpl(t *testing.T) {
	loadInEveryFileOrder(t, map[string]string{
		"%s.nomi": `pub interface Shape
    fn area(value: self) -> Int

    fn twice(value: self) -> Int
        Shape.area(value) * 2
    end
end

pub fn total<T>(xs: List<T>) -> Int where T: Shape
    Iter.reduce(xs, 0, fn acc, x -> acc + T.area(x))
end
`,
		"main.nomi": `import
    std::io
    %s.{Shape as Figure, total}
end

struct Sq
    side: Int
end

impl Figure for Sq
    fn area(s: Sq) -> Int
        s.side * s.side
    end
end

fn main()
    f: Figure = Sq{side: 4}
    io.print(Figure.area(Sq{side: 2}))
    io.print(Figure.twice(Sq{side: 2}))
    io.print(total([Sq{side: 1}, Sq{side: 2}]))
    io.print(Figure.area(f))
end
`,
	}, "4\n8\n5\n16\n")
}
