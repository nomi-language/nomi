package analysis_test

import (
	"strings"
	"testing"
)

// numericSubtypeDecls take each std numeric subtype, so a case passes its
// expression where one is expected and checks only when the expression is
// that subtype or below it. Each expression is bound first, with no type
// written, so nothing expected reaches it.
const numericSubtypeDecls = `import std::duration.{Duration, PositiveDuration}

fn nn(n: NonNegativeInt) -> NonNegativeInt
    n
end

fn pos(n: PositiveInt) -> PositiveInt
    n
end

fn nz(n: NonZeroInt) -> NonZeroInt
    n
end

fn nns(xs: List<NonNegativeInt>) -> List<NonNegativeInt>
    xs
end

fn pos_float(x: PositiveFloat) -> PositiveFloat
    x
end

fn nn_float(x: NonNegativeFloat) -> NonNegativeFloat
    x
end

fn nz_float(x: NonZeroFloat) -> NonZeroFloat
    x
end

fn pos_decimal(d: PositiveDecimal) -> PositiveDecimal
    d
end

fn nz_decimal(d: NonZeroDecimal) -> NonZeroDecimal
    d
end

fn pos_duration(d: PositiveDuration) -> PositiveDuration
    d
end

fn pick<T>(c: Bool, a: T, b: T) -> T
    if c then a else b
end

fn main()
    c = True
    n: NonNegativeInt = 3
    z: NonZeroInt = -4
    p: PositiveInt = 5
    count: Maybe<NonNegativeInt> = Some(n)
    f: NonNegativeFloat = 1.5
    pf: PositiveFloat = 2.5
    zf: NonZeroFloat = -2.5
    d: PositiveDecimal = 2d
    zd: NonZeroDecimal = -2d
    du: PositiveDuration = Duration"1s"
`

// An operator on the std numeric subtypes is the narrowest of them the
// operation's range proves (spec §4 *Value ranges*), and a
// literal beside one is that subtype where it satisfies its rules
// (spec §4 *Chains and conversion*), whichever side the literal is on.
func TestNumericSubtypes_KeptWhereTheyProvablyHold(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"non-negative plus a literal is positive", "    v = n + 1\n    _ = pos(v)"},
		{"the literal first", "    v = 1 + n\n    _ = pos(v)"},
		{"plus zero stays non-negative", "    v = n + 0\n    _ = nn(v)"},
		{"non-negative plus non-negative", "    v = n + n\n    _ = nn(v)"},
		{"positive plus non-negative", "    v = p + n\n    _ = pos(v)"},
		{"non-negative times non-negative", "    v = n * n\n    _ = nn(v)"},
		{"positive times positive", "    v = p * p\n    _ = pos(v)"},
		{"non-zero times non-zero", "    v = z * z\n    _ = nz(v)"},
		{"positive times non-zero", "    v = p * z\n    _ = nz(v)"},
		{"non-zero times a negative literal", "    v = z * -2\n    _ = nz(v)"},
		{"negated non-zero", "    v = -z\n    _ = nz(v)"},
		{"nested", "    v = (n + 1) * p + n\n    _ = pos(v)"},
		{"float sum", "    v = f + f\n    _ = nn_float(v)"},
		{"positive float sum", "    v = pf + f + 0.5\n    _ = pos_float(v)"},
		{"negated non-zero float", "    v = -zf\n    _ = nz_float(v)"},
		{"decimal product", "    v = d * d + d\n    _ = pos_decimal(v)"},
		{"negated non-zero decimal", "    v = -zd\n    _ = nz_decimal(v)"},
		{"duration sum and scale", "    v = du + du * 3\n    _ = pos_duration(v)"},
		{"a default literal", "    v = Maybe.with_default(count, 1)\n    _ = nn(v)"},
		{"a piped default literal", "    v = count |> Maybe.with_default(0)\n    _ = nn(v)"},
		{"a literal argument first", "    v = pick(c, 0, n)\n    _ = nn(v)"},
		{"a literal argument last", "    v = pick(c, n, 0)\n    _ = nn(v)"},
		{"an if", "    v = if c then n else 0\n    _ = nn(v)"},
		{"an if, the literal first", "    v = if c then 0 else n\n    _ = nn(v)"},
		{"an else-if chain of literals", "    v = if c then n else if not c then 0 else 7\n    _ = nn(v)"},
		{"a case", "    v = case c\n        True -> 1\n        False -> p\n    end\n    _ = pos(v)"},
		{"a list", "    v = [n, 0]\n    _ = nns(v)"},
		{"a list, the literal first", "    v = [0, n, 2]\n    _ = nns(v)"},
		{"a count plus one", "    v = List.length([1, 2]) + 1\n    _ = pos(v)"},
		{"literals alone, where expected", "    v = 1 + 2\n    _ = pos(v)"},
		{"non-negative minus one after a check", "    v = if p > 1 then p - 1 else 1\n    _ = pos(v)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := numericSubtypeDecls + tc.body + "\n    _ = (n, z, p, count, f, pf, zf, d, zd, du)\nend\n"
			_, errs := checkSourceWithStdlib(src)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// Where the operation or the literal does not keep a subtype, the value
// is the nearest ancestor, the base at the latest: a literal that breaks
// the rule, `-`, `/` and `%`, a sum with a non-zero operand that may be
// negative, a float product, and an operation on literals alone.
func TestNumericSubtypes_TheBaseWhereNothingHolds(t *testing.T) {
	for _, tc := range []struct{ name, body, want, at string }{
		{"a negative literal", "    v = n + -1\n    _ = nn(v)", "argument 1: expected NonNegativeInt, got Int", "v"},
		{"subtraction", "    v = n - 1\n    _ = nn(v)", "argument 1: expected NonNegativeInt, got Int", "v"},
		{"division of a value of any sign", "    v = z / 1\n    _ = nn(v)", "argument 1: expected NonNegativeInt, got Int", "v"},
		{"remainder of a value of any sign", "    v = z % 2\n    _ = nn(v)", "argument 1: expected NonNegativeInt, got Int", "v"},
		{"non-zero plus non-zero", "    v = z + z\n    _ = nz(v)", "argument 1: expected NonZeroInt, got Int", "v"},
		{"non-negative times non-zero", "    v = n * z\n    _ = nz(v)", "argument 1: expected NonZeroInt, got Int", "v"},
		{"negated non-negative", "    v = -n\n    _ = nn(v)", "argument 1: expected NonNegativeInt, got Int", "v"},
		{"literals alone that are negative", "    v = 1 - 2\n    _ = pos(v)", "argument 1: expected PositiveInt, got Int", "v"},
		{"a float product", "    v = pf * pf\n    _ = pos_float(v)", "argument 1: expected PositiveFloat, got Float", "v"},
		{"a negative default", "    v = Maybe.with_default(count, -1)\n    _ = nn(v)", "argument 1: expected NonNegativeInt, got Int", "v"},
		{"a negative branch", "    v = if c then n else -1\n    _ = nn(v)", "argument 1: expected NonNegativeInt, got Int", "v"},
		{"a negative element", "    v = [n, -1]\n    _ = nns(v)", "argument 1: expected List<NonNegativeInt>, got List<Int>", "v"},
		{"a later collection literal", "    v = List.concat([n], [-1])\n    _ = nns(v)", "argument 1: expected List<NonNegativeInt>, got List<Int>", "v"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := numericSubtypeDecls + tc.body + "\n    _ = (n, z, p, count, f, pf, zf, d, zd, du)\nend\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the program checks; want %q", tc.want)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no error %q among %v", tc.want, errs)
			}
		})
	}
}

// A user's subtype gets no arithmetic of its own: its value is its
// ancestors' for arithmetic, so a sum of two `Small`s is the std subtype
// Small refines, and a literal beside a `Small` is no Small, since the
// checker does not run Small's rule.
func TestNumericSubtypes_AUserSubtypeGetsNothingNew(t *testing.T) {
	const decls = "subtype Small NonNegativeInt when fn n -> n < 10\n\nfn small(s: Small) -> Small\n    s\nend\n\nfn nn(n: NonNegativeInt) -> NonNegativeInt\n    n\nend\n\n"
	for _, tc := range []struct{ name, body string }{
		{"a sum is the std ancestor's", "    v = s + s\n    _ = nn(v)"},
		{"a literal branch meets it at the std ancestor", "    v = if True then 1 else s\n    _ = nn(v)"},
		{"a literal argument meets it at the std ancestor", "    v = pick(True, s, 1)\n    _ = nn(v)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "fn pick<T>(c: Bool, a: T, b: T) -> T\n    if c then a else b\nend\n\nfn main()\n    s: Small = 3\n" + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			expectNoStdlibErrors(t, errs)
		})
	}
	for _, tc := range []struct{ name, body string }{
		{"a sum is no Small", "    v = s + s\n    _ = small(v)"},
		{"a literal branch is no Small", "    v = if True then s else 1\n    _ = small(v)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "fn main()\n    s: Small = 3\n" + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatal("the program checks; want a mismatch at small's argument")
			}
			if !strings.Contains(errs[0].Message, "expected Small, got NonNegativeInt") {
				t.Fatalf("got %v", errs)
			}
		})
	}
}
