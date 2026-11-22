package irbuild

import (
	"fmt"
	"strings"
	"testing"
)

// A part the builder compares itself, a tuple's slot or a distinct's inner
// value, compares through the written Equatable the checker's record at
// the part's type names (partEqualBody), as `==` on the part does and as
// the same part inside a List, a Maybe, a Map key or a Set element does.
// Meters' impl compares the last digit, so a comparison that projected
// the distinct to its Int, as the part's kind would, prints False on the
// first six lines. The struct and the enum, whose impls answer True, and
// the generic distinct, whose derived impl compares its inner value, hold
// the other shapes to their records.
func TestPartEquality_AWrittenImplDecidesEveryPart(t *testing.T) {
	const want = "tuple=True\n" +
		"tuple-ne=False\n" +
		"nested=True\n" +
		"call=True\n" +
		"wrap=True\n" +
		"wrap-tuple=True\n" +
		"struct=True\n" +
		"enum=True\n" +
		"generic=True\n" +
		"generic-ne=False\n" +
		"list=True\n" +
		"maybe=True\n" +
		"map-key=Some(a)\n" +
		"set=1\n" +
		"hash=True\n"
	irRunSource(t, partEqualitySource, want)
}

const partEqualitySource = `import std::io

type Meters Int

impl Equatable for Meters
    fn equal(a: Meters, b: Meters) -> Bool
        Meters(x) = a
        Meters(y) = b
        x % 10 == y % 10
    end
end

impl Hashable for Meters
    fn hash(m: Meters) -> Int
        Meters(x) = m
        x % 10
    end
end

struct Loose
    x: Int
end

impl Equatable for Loose
    fn equal(_: Loose, _: Loose) -> Bool
        True
    end
end

enum Sh
    Dot Int
    Line
end

impl Equatable for Sh
    fn equal(_: Sh, _: Sh) -> Bool
        True
    end
end

type Wrap Meters

derive Equatable for Wrap

type Bx<T> T

derive Equatable for Bx

fn main()
    io.print("tuple=${(Meters(1), 2) == (Meters(11), 2)}")
    io.print("tuple-ne=${(Meters(1), 2) != (Meters(11), 2)}")
    io.print("nested=${((Meters(1), 2), 3) == ((Meters(11), 2), 3)}")
    io.print("call=${Equatable.equal((Meters(1), 2), (Meters(11), 2))}")
    io.print("wrap=${Wrap(Meters(1)) == Wrap(Meters(11))}")
    io.print("wrap-tuple=${(Wrap(Meters(1)), 0) == (Wrap(Meters(11)), 0)}")
    io.print("struct=${(Loose{x: 1}, 2) == (Loose{x: 9}, 2)}")
    io.print("enum=${(Sh.Dot(1), 2) == (Sh.Line, 2)}")
    io.print("generic=${(Bx(1), 2) == (Bx(1), 2)}")
    io.print("generic-ne=${(Bx(1), 2) == (Bx(3), 2)}")
    io.print("list=${[(Meters(1), 2)] == [(Meters(11), 2)]}")
    io.print("maybe=${Some((Meters(1), 2)) == Some((Meters(11), 2))}")
    io.print("map-key=${Map.get({(Meters(1), 2) => "a"}, (Meters(11), 2))}")
    io.print("set=${Set.size(Set[(Meters(1), 2), (Meters(11), 2)])}")
    io.print("hash=${Hashable.hash((Meters(1), 2)) == Hashable.hash((Meters(11), 2))}")
end
`

// A part's equality chosen in a way elemImplClasses does not list fails
// the lowering, naming the site and the class. With every class listed,
// the same program lowers.
func TestNoteElemImpl_AnUnlistedPartClassFails(t *testing.T) {
	p, err := Analyze(writeTemp(t, partEqualitySource))
	if err != nil {
		t.Fatalf("the front end rejects this, so the test checks nothing: %v", err)
	}
	lower := func() (failure string) {
		defer func() {
			if r := recover(); r != nil {
				failure = fmt.Sprint(r)
			}
		}()
		if _, _, err := GenerateIR(p); err != nil {
			return err.Error()
		}
		return ""
	}
	if failure := lower(); failure != "" {
		t.Fatalf("the program does not lower with every class listed: %s", failure)
	}
	agreed := elemImplClasses[elemImplFromRecord]
	delete(elemImplClasses, elemImplFromRecord)
	defer func() { elemImplClasses[elemImplFromRecord] = agreed }()
	want := `entry.nomi:50:38: a part's equality is chosen "from the record", which no class lists`
	if failure := lower(); !strings.Contains(failure, want) {
		t.Fatalf("lowering with the class unlisted failed with %q, want it to name %q", failure, want)
	}
}
