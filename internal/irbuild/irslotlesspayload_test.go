package irbuild

import "testing"

// A variant whose payload has one value, Unit or a zero-sized marker, is
// constructed, matched, compared, hashed and inspected like any other: the
// payload is stored nowhere and read back from its type.
func TestIRSlotlessPayload_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

type Mark

derive Equatable for Mark
derive Hashable for Mark

enum Kind
    Plain
    Marked(Mark)
    Tagged {m: Mark, n: Int}
    Pinged(Unit)
end

derive Equatable for Kind
derive Hashable for Kind

fn describe(k: Kind) -> String
    case k
        Kind.Plain -> "plain"
        Kind.Marked(m) -> "marked ${Debug.inspect(m)}"
        Kind.Tagged{m:, n:} -> "tagged ${Debug.inspect(m)} ${n}"
        Kind.Pinged(u) -> "pinged ${Debug.inspect(u)}"
    end
end

fn main()
    ks = [Kind.Plain, Kind.Marked(Mark), Kind.Tagged{m: Mark, n: 3}, Kind.Pinged(Unit)]
    io.inspect(ks)
    ks |> Iter.each(fn k -> io.print(describe(k)))
    io.inspect(Kind.Marked(Mark) == Kind.Marked(Mark))
    io.inspect(Kind.Tagged{m: Mark, n: 3} == Kind.Tagged{m: Mark, n: 4})
    io.inspect(Hashable.hash(Kind.Pinged(Unit)) == Hashable.hash(Kind.Pinged(Unit)))
    io.inspect(Set.size(Set[Kind.Marked(Mark), Kind.Marked(Mark), Kind.Pinged(Unit)]))
    pairs = [(Kind.Pinged(Unit), "a"), (Kind.Plain, "b")]
    io.inspect(pairs)
    f = Kind.Pinged
    io.inspect(f(Unit))
    k: Kind = .Marked(Mark)
    io.inspect(k)
end
`
	irRunSource(t, src, "[Plain, Marked(Mark), Tagged{m: Mark, n: 3}, Pinged(Unit)]\n"+
		"plain\nmarked Mark\ntagged Mark 3\npinged Unit\n"+
		"True\nFalse\nTrue\n2\n"+
		"[(Pinged(Unit), \"a\"), (Plain, \"b\")]\n"+
		"Pinged(Unit)\nMarked(Mark)\n")
}

// A payload of a field-less struct, or of a struct or tuple holding nothing
// but such values and Units, has a slot as any record does: only Unit and a
// marker are slotless (irSlotlessPayload). Each enum below, generic or not,
// with the payload as a struct variant's field, a single payload, an
// embedded type or a generic struct's field, is built, matched, compared,
// hashed and inspected.
func TestIRFieldlessStructPayload_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Feet end

derive Equatable for Feet
derive Hashable for Feet

struct Pair
    a: Feet
    b: Feet
end

struct Box<T>
    v: T
end

enum Reading<P>
    Seen {unit: P}
    Pos P
    Missing
end

derive Equatable for Reading
derive Hashable for Reading

enum Plain
    Seen {unit: Feet}
    Pos Feet
    Two (Unit, Unit)
    Paired Pair
    Tupled (Feet, Int)
end

enum Shape
    embeds Feet
    Other Int
end

fn reading(r: Reading<Feet>) -> String
    case r
        Reading.Seen{unit:} -> "seen ${Debug.inspect(unit)}"
        Reading.Pos(f) -> "pos ${Debug.inspect(f)}"
        Reading.Missing -> "missing"
    end
end

fn shape(s: Shape) -> String
    case s
        Shape.Feet(f) -> "feet ${Debug.inspect(f)}"
        Shape.Other(n) -> "other ${n}"
    end
end

fn main()
    a = Reading.Seen{unit: Feet{}}
    b = Reading.Pos(Feet{})
    io.inspect(a)
    io.inspect([a, b, Reading.Missing] |> Iter.map(reading) |> Iter.to_list())
    io.inspect(a == Reading.Seen{unit: Feet{}})
    io.inspect(a == b)
    io.inspect(Set.size(Set[a, b, a]))
    io.inspect({a => 1, b => 2})
    io.inspect(Reading.Seen{unit: Box{v: Feet{}}})
    io.inspect([Plain.Seen{unit: Feet{}}, Plain.Pos(Feet{}), Plain.Two((Unit, Unit)), Plain.Paired(Pair{a: Feet{}, b: Feet{}}), Plain.Tupled((Feet{}, 3))])
    shapes: List<Shape> = [Feet{}, Shape.Other(1)]
    io.inspect(shapes |> Iter.map(shape) |> Iter.to_list())
end
`
	irRunSource(t, src, "Seen{unit: Feet{}}\n"+
		"[\"seen Feet{}\", \"pos Feet{}\", \"missing\"]\n"+
		"True\nFalse\n2\n"+
		"{Seen{unit: Feet{}} => 1, Pos(Feet{}) => 2}\n"+
		"Seen{unit: Box{v: Feet{}}}\n"+
		"[Seen{unit: Feet{}}, Pos(Feet{}), Two(Unit, Unit), Paired(Pair{a: Feet{}, b: Feet{}}), Tupled(Feet{}, 3)]\n"+
		"[\"feet Feet{}\", \"other 1\"]\n")
}
