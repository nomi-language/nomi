package irbuild

import "testing"

// A scalar, a String or a scalar distinct runs where an interface is
// expected in every position that stores it into the interface's register:
// a function's result, an early `return`, `if` and `case` arms (as a result
// and under a binding annotation), a partial application's bound argument on
// either side of the hole, and a lambda's result under an annotated function
// type. The existential holds the concrete value, so the move into it boxes
// the value as its own type and the distinct keeps its impl.
func TestIRErasedScalar_EveryPositionRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

type Meters Int

impl Display for Meters
    fn to_string(Meters(n)) -> String
        "${Int.to_string(n)} m"
    end
end

fn show(n: Int) -> Display
    n
end

fn text(s: String) -> Display
    s
end

fn meters(n: Int) -> Display
    Meters(n)
end

fn pick(c: Bool) -> Display
    if c then 1.5 else 2.5
end

fn arm(n: Int) -> Display
    case n
        0 -> True
        _ -> False
    end
end

fn early(n: Int) -> Display
    if n > 0 then
        return n
    end
    "negative"
end

fn both(d: Display, n: Int) -> String
    "${Display.to_string(d)}/${Int.to_string(n)}"
end

fn flag() -> Bool
    True
end

fn main()
    io.print(Display.to_string(show(5)))
    io.print(Display.to_string(text("hi")))
    io.print(Display.to_string(meters(3)))
    io.print(Display.to_string(pick(True)))
    io.print(Display.to_string(pick(False)))
    io.print(Display.to_string(arm(0)))
    io.print(Display.to_string(arm(1)))
    io.print(Display.to_string(early(8)))
    io.print(Display.to_string(early(-1)))
    d: Display = if flag() then 5 else 6
    io.print(Display.to_string(d))
    e: Display = case 2
        1 -> "one"
        _ -> "other"
    end
    io.print(Display.to_string(e))
    f = &both(5, &1)
    io.print(f(3))
    g = &both(Meters(7), &1)
    io.print(g(4))
    h: (Int) -> Display = fn n -> n + 1
    io.print(Display.to_string(h(9)))
end
`
	const want = "5\n" +
		"hi\n" +
		"3 m\n" +
		"1.5\n" +
		"2.5\n" +
		"True\n" +
		"False\n" +
		"8\n" +
		"negative\n" +
		"5\n" +
		"other\n" +
		"5/3\n" +
		"7 m/4\n" +
		"10\n"
	irRunSource(t, src, want)
}
