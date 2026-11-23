package vmhost_test

import "testing"

// An arm whose pattern always matches (the checker's record: a struct of
// names, a one-variant enum's variant, a distinct's pattern) ends the arms:
// the ones after it never run, and lowering them is no error. Before the
// builder read the record, it tested `{x:, y:}` and lowered the next arm
// into a block nothing reached, which the IR lint refused.
func TestIrrefutablePattern_ArmsAfterOneThatAlwaysMatchesNeverRun(t *testing.T) {
	got := runSourceOutput(t, `import std::io

struct P
    x: Int
    y: Int
end

enum Only
    One(Int)
end

type Meters Int

fn point(p: P) -> Int
    case p
        {x:, y:} -> x + y
        P{x: 1, y:} -> y
    end
end

fn only(o: Only) -> Int
    case o
        .One(n) -> n
        _ -> 0
    end
end

fn meters(m: Meters) -> Int
    case m
        Meters(n) -> n
        Meters(0) -> -1
    end
end

fn main()
    io.inspect(point(P{x: 1, y: 2}))
    io.inspect(only(Only.One(5)))
    io.inspect(meters(Meters(0)))
end
`)
	want := "3\n5\n0\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A literal over a value a condition narrowed to a refinement (`x > 0` makes x
// a PositiveInt) is tested, as the checker records: it matches one value.
func TestIrrefutablePattern_ALiteralOverANarrowedValueIsTested(t *testing.T) {
	got := runSourceOutput(t, `import std::io

fn guarded(x: Int) -> Int
    if x > 0 then
        case x
            7 -> 70
            _ -> 0
        end
    else
        -1
    end
end

fn main()
    io.inspect(guarded(7))
    io.inspect(guarded(8))
end
`)
	want := "70\n0\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
