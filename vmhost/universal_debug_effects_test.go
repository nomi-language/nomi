package vmhost

import "testing"

// A rendering by a type's shape has no effect, whichever way it is
// spelled (`Point.inspect(p)`, `Debug.inspect(p)`), and a written Debug
// with an effect keeps it, called on its own or nested in a shape the
// machine renders.
func TestEffectMarks_ADebugByShapeIsPure(t *testing.T) {
	marksCase{
		src: `import std::io

struct Point
    x: Int
end

struct Loud
    n: Int
end

impl Debug for Loud
    fn inspect(value: Loud) -> String
        io.print("loud")
        "L${value.n}"
    end
end

struct Pair
    p: Point
    l: Loud
end

fn main()
    _ = Point.inspect(Point{x: 1})
    _ = Debug.inspect(Point{x: 2})
    _ = Loud.inspect(Loud{n: 3})
    _ = Pair.inspect(Pair{p: Point{x: 4}, l: Loud{n: 5}})
end
`,
		marked: []string{"12:inspect", "13:io.print", "23:main", "26:Loud.inspect", "27:Pair.inspect"},
		hovers: map[string]string{
			"Point.inspect(Point": "",
			"Debug.inspect(Point": "",
			"Loud.inspect(Loud":   ioLine,
			"Pair.inspect(Pair":   ioLine,
		},
	}.run(t)
}
