package irbuild

import "testing"

// An `if` or `case` whose branches are different Iters, joined by a written
// `Iter<T>`, views each branch as that Iter and runs.
func TestIRIterBranchJoin_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn in_order(values: Range<Int>, descending: Bool) -> Iter<Int>
    if descending then
        Iter.reverse(values)
    else
        values
    end
end

fn total(xs: Iter<Int>) -> Int
    Iter.count(xs)
end

fn main()
    io.inspect(in_order(1..4, True) |> Iter.to_list())
    io.inspect(in_order(1..4, False) |> Iter.to_list())
    pick = 2
    b: Iter<Int> = case pick
        1 -> 1..=3
        2 -> Set[9]
        _ -> [7, 7] |> Iter.map(fn x -> x + 1)
    end
    io.inspect(Iter.to_list(b))
    io.inspect(total(if pick == 2 then Vector[1, 2] else [1]))
    io.inspect((if pick == 2 then 1..4 else [7]) |> Iter.count())
    io.inspect(case pick
        2 -> [5, 6]
        _ -> 1..3
    end |> Iter.map(fn x -> x * 10) |> Iter.to_list())
end
`
	irRunSource(t, src, "[3, 2, 1]\n[1, 2, 3]\n[9]\n2\n3\n[50, 60]\n")
}

// An `if` or `case` whose branches are different types, joined by a written
// interface, boxes each branch into that interface and runs: a program's
// interface at an annotation, a declared result, a parameter and a pipe
// head, over an `if` and a `case`, a generic program interface, and std's
// Display and Hashable.
func TestIRInterfaceBranchJoin_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

interface Shape
    fn area(shape: self) -> Float
end

struct Circle
    r: Float
end

struct Square
    s: Float
end

impl Shape for Circle
    fn area(c: Circle) -> Float
        3.0 * c.r * c.r
    end
end

impl Shape for Square
    fn area(q: Square) -> Float
        q.s * q.s
    end
end

fn pick(flag: Bool) -> Shape
    if flag then
        Circle{r: 1.0}
    else
        Square{s: 2.0}
    end
end

fn describe(shape: Shape) -> Float
    Shape.area(shape)
end

interface Source<T>
    fn get(source: self) -> T
end

impl Source<Float> for Circle
    fn get(c: Circle) -> Float
        c.r
    end
end

impl Source<Float> for Square
    fn get(q: Square) -> Float
        q.s
    end
end

fn label(n: Int) -> Display
    case n
        1 -> "one"
        2 -> 2
        _ -> 3.5
    end
end

fn arm(n: Int) -> Float
    s: Shape = case n
        1 -> Circle{r: 2.0}
        2 -> Square{s: 3.0}
        _ -> pick(True)
    end
    Shape.area(s)
end

fn main()
    io.inspect(Shape.area(pick(True)))
    io.inspect(Shape.area(pick(False)))
    io.inspect([arm(1), arm(2), arm(3)])
    io.print("${label(1)} ${label(2)} ${label(3)}")
    up = False
    io.inspect(describe(if up then Circle{r: 1.0} else Square{s: 5.0}))
    io.inspect((if up then Circle{r: 1.0} else Square{s: 4.0}) |> describe())
    d: Display = if up then 1 else "text"
    io.print(Display.to_string(d))
    h: Hashable = if up then 1 else "text"
    io.inspect(Hashable.hash(h) == Hashable.hash("text"))
    g: Source<Float> = if up then Circle{r: 1.0} else Square{s: 7.0}
    io.inspect(Source.get(g))
end
`
	irRunSource(t, src, "3.0\n4.0\n[12.0, 9.0, 3.0]\none 2 3.5\n25.0\n16.0\ntext\nTrue\n7.0\n")
}
