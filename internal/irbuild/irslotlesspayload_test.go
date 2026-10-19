package irbuild

import "testing"

// A variant whose payload has one value, Unit or a zero-sized marker, is
// constructed, matched, compared, hashed and inspected like any other: the
// payload is stored nowhere and read back from its type.
func TestIRSlotlessPayload_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

type Mark

derive Equatable for Mark
derive Hashable for Mark

enum Kind {
    Plain
    Marked(Mark)
    Tagged {m: Mark, n: Int}
    Pinged(Unit)
}

derive Equatable for Kind
derive Hashable for Kind

fn describe(k: Kind) -> String {
    case k {
        Kind.Plain -> "plain"
        Kind.Marked(m) -> "marked ${Debug.inspect(m)}"
        #Kind.Tagged{m, n} -> "tagged ${Debug.inspect(m)} ${n}"
        Kind.Pinged(u) -> "pinged ${Debug.inspect(u)}"
    }
}

fn main() {
    ks = [Kind.Plain, Kind.Marked(Mark), #Kind.Tagged{m: Mark, n: 3}, Kind.Pinged(Unit)]
    io.inspect(ks)
    ks |> Iter.each(fn k -> io.print(describe(k)))
    io.inspect(Kind.Marked(Mark) == Kind.Marked(Mark))
    io.inspect(#Kind.Tagged{m: Mark, n: 3} == #Kind.Tagged{m: Mark, n: 4})
    io.inspect(Hashable.hash(Kind.Pinged(Unit)) == Hashable.hash(Kind.Pinged(Unit)))
    io.inspect(Set.size(%[Kind.Marked(Mark), Kind.Marked(Mark), Kind.Pinged(Unit)]))
    pairs = [#(Kind.Pinged(Unit), "a"), #(Kind.Plain, "b")]
    io.inspect(pairs)
    f = Kind.Pinged
    io.inspect(f(Unit))
    k: Kind = .Marked(Mark)
    io.inspect(k)
}
`
	irRunSource(t, src, "[Plain, Marked(Mark), #Tagged{m: Mark, n: 3}, Pinged(Unit)]\n"+
		"plain\nmarked Mark\ntagged Mark 3\npinged Unit\n"+
		"True\nFalse\nTrue\n2\n"+
		"[#(Pinged(Unit), \"a\"), #(Plain, \"b\")]\n"+
		"Pinged(Unit)\nMarked(Mark)\n")
}
