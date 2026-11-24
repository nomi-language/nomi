package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A type implements an interface once at each set of type arguments, and
// coherence counts impl blocks, not the functions they write: two empty
// blocks of a marker interface for one type are a duplicate, reported at
// the second with the first as its related location.
func TestImplCoherence_TwoEmptyBlocksAreADuplicate(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `struct P
    x: Int
end

interface Marked
end

impl Marked for P
end

impl Marked for P
end

fn main()
    dbg P{x: 1}
end
`, nil)
	expectRejected(t, errs, "duplicate impl: `Marked` is implemented for `P` twice in this module; a type implements an interface once at each set of type arguments")
	e := duplicateImplError(t, errs)
	if e.Line != 11 || e.Col != 1 {
		t.Errorf("the duplicate is reported at %d:%d, want the second block's header, 11:1", e.Line, e.Col)
	}
	if len(e.Related) != 1 || e.Related[0].Line != 8 || e.Related[0].Message != "the other impl is here" {
		t.Errorf("the duplicate's related location is %+v, want the first block's header at line 8", e.Related)
	}
}

// Two blocks of one interface for one type that write different functions
// of it, the rest left to its defaults, are a duplicate too.
func TestImplCoherence_BlocksWritingDifferentFunctionsAreADuplicate(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `interface Named
    fn first(value: self) -> String
        "first"
    end

    fn last(value: self) -> String
        "last"
    end
end

struct P
    x: Int
end

impl Named for P
    fn first(_p: P) -> String
        "p"
    end
end

impl Named for P
    fn last(_p: P) -> String
        "q"
    end
end

fn main()
    dbg Named.first(P{x: 1})
end
`, nil)
	expectRejected(t, errs, "duplicate impl: `Named` is implemented for `P`")
}

// The type arguments are part of the key: one receiver implements a
// generic interface at several, with functions or with none, and two
// impls that differ only in the output type or in a type argument's own
// arguments are two instantiations.
func TestImplCoherence_DistinctInstantiationsAreNotADuplicate(t *testing.T) {
	for _, src := range []string{
		`interface Tag<T>
end

struct P
    x: Int
end

impl Tag<Int> for P end

impl Tag<String> for P end

fn main()
    dbg P{x: 1}
end
`,
		`interface Holds<T>
    fn holds(b: self, x: T) -> Bool
end

struct Box
    n: Int
    s: String
end

impl Holds<Int> for Box
    fn holds(b: Box, x: Int) -> Bool
        b.n == x
    end
end

impl Holds<String> for Box
    fn holds(b: Box, x: String) -> Bool
        b.s == x
    end
end

fn main()
    dbg Holds.holds(Box{n: 1, s: "a"}, 1)
end
`,
		`struct Bag
    n: Int
end

impl Add<List<Int>, Bag> for Bag
    fn add(lhs: Bag, rhs: List<Int>) -> Bag
        Bag{n: lhs.n + Iter.count(rhs)}
    end
end

impl Add<List<String>, Bag> for Bag
    fn add(lhs: Bag, rhs: List<String>) -> Bag
        Bag{n: lhs.n - Iter.count(rhs)}
    end
end

fn main()
    dbg (Bag{n: 1} + [1, 2]).n
end
`,
	} {
		expectSelectionClean(t, buildProjectExpectingErrors(t, src, nil))
	}
}

// One instantiation twice is the duplicate, named by its header.
func TestImplCoherence_OneInstantiationTwiceIsADuplicate(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `interface Tag<T>
end

struct P
    x: Int
end

impl Tag<Int> for P end

impl Tag<Int> for P end

fn main()
    dbg P{x: 1}
end
`, nil)
	expectRejected(t, errs, "duplicate impl: `Tag<Int>` is implemented for `P` twice in this module")
}

// Blocks that differ only in their `where` clauses implement one
// interface for one type: a value of a type that meets both would have
// two impls.
func TestImplCoherence_BlocksDifferingInWhereAreADuplicate(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `interface Show
    fn show(value: self) -> String
end

struct Box<T>
    v: T
end

impl Show for Box<T> where T: Display
    fn show(b: Box<T>) -> String
        "${b.v}"
    end
end

impl Show for Box<T> where T: Equatable
    fn show(_b: Box<T>) -> String
        "eq"
    end
end

fn main()
    dbg Show.show(Box{v: 1})
end
`, nil)
	expectRejected(t, errs, "duplicate impl: `Show` is implemented for `Box` twice in this module")
}

// A derive beside a hand-written impl of the same interface in the type's
// module is the derive conflict, reported at the hand-written block.
func TestImplCoherence_ADeriveBesideAWrittenImplConflicts(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `struct P
    x: Int
end

derive Display for P

impl Display for P
    fn to_string(p: P) -> String
        "p ${p.x}"
    end
end

fn main()
    dbg "${P{x: 1}}"
end
`, nil)
	expectRejected(t, errs, "`derive Display for P` conflicts with a hand-written `impl Display for P` block")
	if e := duplicateImplError(t, errs); e.Line != 7 || e.Col != 1 {
		t.Errorf("the conflict is reported at %d:%d, want the hand-written block's header, 7:1", e.Line, e.Col)
	}
}

// Two functions of one name in one block are no second impl: the block
// declares the name twice, which is reported at the second.
func TestImplCoherence_AFunctionTwiceInOneBlockIsARepeatedName(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `struct P
    x: Int
end

impl Display for P
    fn to_string(p: P) -> String
        "a ${p.x}"
    end

    fn to_string(p: P) -> String
        "b ${p.x}"
    end
end

fn main()
    dbg "${P{x: 1}}"
end
`, nil)
	expectRejected(t, errs, "function 'to_string' is declared twice in `impl Display for P`")
}

// duplicateImplError is the coherence error among errs.
func duplicateImplError(t *testing.T, errs []analysis.TypeError) analysis.TypeError {
	t.Helper()
	for _, e := range errs {
		if strings.HasPrefix(e.Message, "duplicate impl:") || strings.Contains(e.Message, "conflicts with a hand-written") {
			return e
		}
	}
	t.Fatalf("no coherence error among %v", errs)
	return analysis.TypeError{}
}
