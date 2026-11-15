package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// narrowingDecls take the std subtypes a condition narrows to, so a case
// checks only when the binding it passes has that subtype there.
const narrowingDecls = `fn nn(NonNegativeInt) -> Int
    1
end

fn pos(PositiveInt) -> Int
    1
end

fn nz(NonZeroInt) -> Int
    1
end

fn pos_float(PositiveFloat) -> Int
    1
end

fn nn_float(NonNegativeFloat) -> Int
    1
end

fn nz_float(NonZeroFloat) -> Int
    1
end

fn pos_decimal(PositiveDecimal) -> Int
    1
end

fn nz_decimal(NonZeroDecimal) -> Int
    1
end

fn ne_list(NonEmptyList<Int>) -> Int
    1
end

fn ne_vector(NonEmptyVector<Int>) -> Int
    1
end

fn ne_map(NonEmptyMap<String, Int>) -> Int
    1
end

fn ne_set(NonEmptySet<Int>) -> Int
    1
end

fn ne_string(NonEmptyString) -> Int
    1
end

`

// narrowingSource is narrowingDecls and one function body over the parameters n: Int, x:
// Float, d: Decimal, xs: List<Int>, v: Vector<Int>, m: Map<String, Int>,
// s: Set<Int> and t: String, returning an Int.
func narrowingSource(body string) string {
	return narrowingDecls + "fn f(n: Int, x: Float, d: Decimal, xs: List<Int>, v: Vector<Int>, m: Map<String, Int>, s: Set<Int>, t: String) -> Int\n" + body + "\nend\n"
}

// A recognized condition narrows its binding to the std subtype it proves
// in the code it guards (spec §4 *Narrowing*).
func TestNarrowing_AConditionNarrowsWhereItHolds(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"not zero", "    if n != 0 then nz(n) else 0"},
		{"zero, in the else", "    if n == 0 then 0 else nz(n)"},
		{"non-negative", "    if n >= 0 then nn(n) else 0"},
		{"positive", "    if n > 0 then pos(n) else 0"},
		{"positive from a positive bound", "    if n >= 1 then pos(n) else 0"},
		{"the literal first", "    if 0 < n then pos(n) else 0"},
		{"negative is non-zero", "    if n < 0 then nz(n) else nn(n)"},
		{"a negative bound", "    if n <= -1 then nz(n) else 0"},
		{"equal to a positive literal", "    if n == 3 then pos(n) else 0"},
		{"not", "    if not (n <= 0) then pos(n) else 0"},
		{"and narrows both", "    if n >= 0 and n != 0 then pos(n) else 0"},
		{"and narrows its right operand", "    if n != 0 and nz(n) > 0 then 1 else 0"},
		{"or narrows its right operand", "    if n == 0 or nz(n) > 1 then 1 else 0"},
		{"an or that is false", "    if n < 0 or n == 0 then 0 else pos(n)"},
		{"an else if", "    if n < 0 then 0 else if n == 0 then 1 else pos(n)"},
		{"an early return", "    if n == 0 then\n        return 0\n    end\n    nz(n)"},
		{"an early return in an else", "    if n > 0 then\n        _ = n\n    else\n        return 0\n    end\n    pos(n)"},
		{"a chain of early returns", "    if n < 0 then\n        return 0\n    else if n == 0 then\n        return 1\n    end\n    pos(n)"},
		{"a trap", "    if n == 0 then unreachable \"zero\"\n    nz(n)"},
		{"a lambda in the branch", "    if n > 0 then [1] |> Iter.map(fn _ -> pos(n)) |> Iter.sum() else 0"},
		{"a nested block", "    if n > 0 then\n        k = begin\n            pos(n)\n        end\n        k\n    else\n        0\n    end"},
		{"a guard", "    case n\n        k when k > 0 -> pos(k)\n        _            -> 0\n    end"},
		{"a guard on the subject", "    case n\n        _ when n != 0 -> nz(n)\n        _            -> 0\n    end"},
		{"a case without a subject", "    case\n        n < 0  -> nz(n)\n        n == 0 -> 0\n        _      -> pos(n)\n    end"},
		{"float positive", "    if x > 0.0 then pos_float(x) else 0"},
		{"float non-negative", "    if x >= 0.0 then nn_float(x) else 0"},
		{"float negative is non-zero", "    if x < 0.0 then nz_float(x) else 0"},
		{"float not unequal", "    if x != 1.5 then 0 else pos_float(x)"},
		{"decimal positive", "    if d > 0d then pos_decimal(d) else 0"},
		{"decimal not zero", "    if d != 0d then nz_decimal(d) else 0"},
		{"a list not empty", "    if not Iter.is_empty(xs) then ne_list(xs) else 0"},
		{"a piped emptiness test", "    if xs |> Iter.is_empty() then 0 else ne_list(xs)"},
		{"a list unequal to []", "    if xs != [] then ne_list(xs) else 0"},
		{"a list equal to [], in the else", "    if [] == xs then 0 else ne_list(xs)"},
		{"a vector", "    if v != [] then ne_vector(v) else 0"},
		{"a map", "    if m == {} then 0 else ne_map(m)"},
		{"a set", "    if Iter.is_empty(s) then 0 else ne_set(s)"},
		{"a string", "    if t != \"\" then ne_string(t) else 0"},
		{"an early return on an empty list", "    if xs == [] then\n        return 0\n    end\n    ne_list(xs)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := narrowingSource(tc.body)
			_, errs := checkSourceWithStdlib(src)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// An assertion narrows the rest of its block.
func TestNarrowing_AnAssertionNarrowsWhatFollows(t *testing.T) {
	src := "import std::assertions.AssertionFailure\n\n" + narrowingDecls + `fn f(n: Int, xs: List<Int>) -> Result<Int, AssertionFailure>
    assert n > 0
    refute xs == []
    Ok(pos(n) + ne_list(xs))
end
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// What a condition does not prove narrows nothing: a branch where it may
// be either way, a Float comparison that NaN can pass or fail, an `or`
// that is true, an `if` whose branch goes on, a user's subtype, and a
// value that is no binding.
func TestNarrowing_NothingElseNarrows(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"the else of positive", "    if n > 0 then 0 else nz(n)", "expected NonZeroInt, got Int"},
		{"a positive bound below one", "    if n > -1 then pos(n) else 0", "expected PositiveInt, got NonNegativeInt"},
		{"a bound below minus one", "    if n > -2 then nn(n) else 0", "expected NonNegativeInt, got Int"},
		{"a float that is not zero may be NaN", "    if x != 0.0 then nz_float(x) else 0", "expected NonZeroFloat, got Float"},
		{"the else of a float comparison may be NaN", "    if x < 0.0 then 0 else nn_float(x)", "expected NonNegativeFloat, got Float"},
		{"an or that is true", "    if n > 0 or n == 0 then nn(n) else 0", "expected NonNegativeInt, got Int"},
		{"an and that is false", "    if n > 0 and n < 10 then 0 else pos(n)", "expected PositiveInt, got Int"},
		{"after a branch that goes on", "    if n == 0 then\n        dbg n\n    end\n    nz(n)", "expected NonZeroInt, got Int"},
		{"after the region", "    k = if n > 0 then pos(n) else 0\n    k + pos(n)", "expected PositiveInt, got Int"},
		{"a guarded arm's condition, in a later arm", "    case\n        n == 0 when x > 0.0 -> 0\n        _                   -> nz(n)\n    end", "expected NonZeroInt, got Int"},
		{"a computed value", "    if n + 1 > 0 then pos(n + 1) else 0", "expected PositiveInt, got Int"},
		{"a list compared with a non-empty literal", "    if xs != [1] then ne_list(xs) else 0", "expected NonEmptyList<Int>, got List<Int>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := narrowingSource(tc.body)
			_, errs := checkSourceWithStdlib(src)
			expectErrorAmong(t, errs, tc.want)
		})
	}
}

// A user's subtype over another subtype has a rule the checker does not
// read as an Int's bounds, so no condition proves one, and a binding of
// one keeps its type rather than narrowing to a std subtype that is no
// Small; its range still narrows, and proves a std subtype where one is
// expected.
func TestNarrowing_AUserSubtypeNeitherNarrowsNorIsNarrowed(t *testing.T) {
	const decls = "subtype Small NonNegativeInt when fn n -> n < 10\n\nfn small(s: Small) -> Int\n    s\nend\n\nfn pos(n: PositiveInt) -> Int\n    n\nend\n\n"
	_, errs := checkSourceWithStdlib(decls + "fn f(n: Int) -> Int\n    if n >= 0 and n < 10 then small(n) else 0\nend\n")
	expectErrorAmong(t, errs, "expected Small, got NonNegativeInt")
	src := decls + "fn f(s: Small) -> Small\n    if s > 0 then\n        _ = pos(s)\n        s\n    else\n        s\n    end\nend\n"
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	checked := false
	for node, ty := range fa.ExprTypes {
		if id, ok := node.(*ast.Ident); ok && id.Name == "s" && id.Line == 14 {
			checked = true
			if ty.String() != "Small" {
				t.Fatalf("s after `s > 0` is a %s; want it to keep Small", ty)
			}
		}
	}
	if !checked {
		t.Fatal("no read of s on line 14")
	}
}

// A narrowed read is recorded with its subtype, which hover and pipe
// hints show and the IR builder reads, and the binding keeps its own.
func TestNarrowing_TheReadHasTheSubtype(t *testing.T) {
	src := "fn f(d: Int) -> Int\n    if d > 0 then\n        d\n    else\n        d\n    end\nend\n"
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	got := map[int]string{}
	for node, ty := range fa.ExprTypes {
		if id, ok := node.(*ast.Ident); ok && id.Name == "d" && ty != nil {
			got[id.Line] = ty.String()
		}
	}
	if got[3] != "PositiveInt" || got[5] != "Int" {
		t.Fatalf("read types by line = %v; want PositiveInt on line 3 and Int on line 5", got)
	}
}

func expectErrorAmong(t *testing.T, errs []analysis.TypeError, want string) {
	t.Helper()
	if len(errs) == 0 {
		t.Fatalf("the program checks; want %q", want)
	}
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return
		}
	}
	t.Fatalf("no error %q among %v", want, errs)
}
