package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// rangeDecls take the subtypes a value's range proves, so a case checks
// only when the value passed has a range inside the parameter's.
const rangeDecls = `import std::int.Int64
import std::duration.{Duration, PositiveDuration}

subtype Digit Int when fn n -> n >= 0 and n <= 9

subtype Odd Int when fn n -> n % 2 == 1

subtype Wide Int when fn n -> n >= 0 or n <= -10

fn nn(NonNegativeInt) -> Int
    1
end

fn pos(PositiveInt) -> Int
    1
end

fn nz(NonZeroInt) -> Int
    1
end

fn int64(Int64) -> Int
    1
end

fn digit(Digit) -> Int
    1
end

fn odd(Odd) -> Int
    1
end

fn wide(Wide) -> Int
    1
end

fn pos_decimal(PositiveDecimal) -> Int
    1
end

fn pos_duration(PositiveDuration) -> Int
    1
end

fn pos_float(PositiveFloat) -> Int
    1
end

`

// rangeSource is rangeDecls and one function over n, size, a and b: Int,
// p: PositiveInt, d: Decimal, x: Float and s: NonEmptyString, returning an
// Int.
func rangeSource(body string) string {
	return rangeDecls + "fn f(n: Int, size: Int, a: Int, b: Int, p: PositiveInt, d: Decimal, x: Float, s: NonEmptyString) -> Int\n    _ = (n, size, a, b, p, d, x, s, Duration.seconds(1))\n" + body + "\nend\n"
}

// A value's range, from comparisons with constants, literals, arithmetic and
// std results, proves the subtypes it lies in (spec §4 *Value ranges*).
func TestRanges_ProveSubtypes(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"one less than a size above one", "    if size > 1 then pos(size - 1) else 0"},
		{"a product of two positives", "    if a > 0 and b > 0 then pos(a * b) else 0"},
		{"one more than a non-negative", "    if n >= 0 then pos(n + 1) else 0"},
		{"after an early return", "    if n <= 0 then\n        return 0\n    end\n    pos(n)"},
		{"after an early return, arithmetic", "    if n < 3 then\n        return 0\n    end\n    pos(n - 2)"},
		{"a bound below one is non-negative", "    if n > -1 then nn(n) else 0"},
		{"a positive minus one", "    nn(p - 1)"},
		{"a bound excluded by an inequality", "    if n >= 5 and n != 5 then pos(n - 5) else 0"},
		{"a negated negative", "    if n < 0 then pos(-n) else 0"},
		{"a local bound to literals", "    k = 60 * 60\n    pos(k)"},
		{"a local compared with a local constant", "    limit = 10\n    if n > limit then pos(n - 10) else 0"},
		{"literals alone, where expected", "    pos(1 + 2)"},
		{"a literal if", "    pos(if n > 0 then 1 else 2)"},
		{"bounds prove Int64", "    if n >= 0 and n < 1000 then int64(n) else 0"},
		{"bounds prove a module subtype over Int", "    if n >= 0 and n <= 9 then digit(n) else 0"},
		{"the larger of one and anything", "    pos(Int.max(1, n))"},
		{"the larger of anything and two", "    pos(Int.max(n, 2))"},
		{"the smaller of two non-zeros", "    nz(Int.min(p, -3))"},
		{"a divisor that is the larger of one and anything", "    n / Int.max(1, size)"},
		{"a power of a positive", "    pos(Int.pow(p, 3))"},
		{"a power of anything to zero", "    pos(Int.pow(n, 0))"},
		{"the absolute value of a non-zero", "    if n != 0 then pos(Int.abs(n)) else 0"},
		{"a piped absolute value", "    if n != 0 then pos(n |> Int.abs()) else 0"},
		{"the length of a non-empty string", "    pos(String.length(s))"},
		{"a positive Int as a Decimal", "    pos_decimal(Int.to_decimal(p))"},
		{"a positive decimal product", "    if d > 0d then pos_decimal(d * d) else 0"},
		{"minutes of a positive count", "    pos_duration(Duration.minutes(p))"},
		{"seconds of a literal", "    pos_duration(Duration.seconds(5))"},
		{"a float sum of positives", "    if x > 0.0 then pos_float(x + x) else 0"},
		{"a quotient of a non-negative by a positive", "    if n >= 0 then nn(n / p) else 0"},
		{"a quotient by a negative", "    if n <= 0 and b < 0 then nn(n / b) else 0"},
		{"a remainder of a non-negative", "    if n >= 0 and b != 0 then nn(n % b) else 0"},
		{"a remainder below a bounded divisor", "    if n >= 0 and b > 0 and b <= 10 then digit(n % b) else 0"},
		{"the root of a positive", "    pos(Int.sqrt(p))"},
		{"the nanoseconds of a positive duration", "    pos(Duration.as_nanos(Duration.seconds(p)))"},
		{"the count of a non-empty list", "    if size > 0 then pos(Iter.count(NonEmptyList.of(1, []))) else 0"},
		{"a power of constants", "    nz(Int.pow(2, 63) - 1)"},
		{"a float not zero once it is no NaN", "    if x >= 0.0 then\n        if x != 0.0 then pos_float(x) else 0\n    else\n        0\n    end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(rangeSource(tc.body))
			expectNoStdlibErrors(t, errs)
		})
	}
}

// What the ranges do not prove stays an error: no relation between two
// bindings, a rule the checker does not read as bounds, a subtype over
// another subtype, a computed value that is no binding, and a Float that
// may be NaN.
func TestRanges_ProveNothingElse(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"a size unchecked", "    pos(size - 1)", "expected PositiveInt, got Int"},
		{"a size at least one", "    if size >= 1 then pos(size - 1) else 0", "expected PositiveInt, got NonNegativeInt"},
		{"a relation between bindings", "    if a > b then pos(a - b) else 0", "expected PositiveInt, got Int"},
		{"an upper bound alone", "    if n < 10 then nn(n) else 0", "expected NonNegativeInt, got Int"},
		{"a product with a sign unknown", "    if a > 0 then pos(a * b) else 0", "expected PositiveInt, got Int"},
		{"bounds past Int64", "    if n >= 0 then int64(n) else 0", "expected Int64, got NonNegativeInt"},
		{"bounds past a module subtype", "    if n >= 0 and n <= 10 then digit(n) else 0", "expected Digit, got NonNegativeInt"},
		{"a rule that is no bounds", "    if n == 3 then odd(n) else 0", "expected Odd, got PositiveInt"},
		{"a rule with an or", "    if n >= 0 then wide(n) else 0", "expected Wide, got NonNegativeInt"},
		{"the larger of zero and anything", "    pos(Int.max(0, n))", "expected PositiveInt, got NonNegativeInt"},
		{"the smaller of one and anything", "    nz(Int.min(1, n))", "expected NonZeroInt, got Int"},
		{"a division may be zero", "    if n > 0 then pos(n / 2) else 0", "expected PositiveInt, got NonNegativeInt"},
		{"a division by a divisor of any sign", "    if n > 0 then nn(n / b) else 0", "expected NonNegativeInt, got Int"},
		{"a remainder may be zero", "    if n > 0 then pos(n % 2) else 0", "expected PositiveInt, got NonNegativeInt"},
		{"a remainder of a dividend of any sign", "    nn(n % p)", "expected NonNegativeInt, got Int"},
		{"a float that may be NaN", "    if x != 0.0 then pos_float(x * x) else 0", "expected PositiveFloat, got Float"},
		{"a float difference", "    if x > 1.0 then pos_float(x - 1.0) else 0", "expected PositiveFloat, got Float"},
		{"a power of a base of any sign", "    pos(Int.pow(n, 2))", "expected PositiveInt, got Int"},
		{"minutes of any count", "    pos_duration(Duration.minutes(n))", "expected PositiveDuration, got Duration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(rangeSource(tc.body))
			expectErrorAmong(t, errs, tc.want)
		})
	}
}

// A value whose range rests on something other than literals has the
// subtype it proves, which hover shows; one of literals alone keeps its
// base and converts only where the subtype is expected.
func TestRanges_TheTypeAValueHas(t *testing.T) {
	src := `once k = 60 * 60

fn f(size: Int) -> Int
    m = 1 + 2
    if size > 1 then
        r = size - 1
        r + m + k
    else
        m
    end
end
`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	got := map[string]string{}
	for node, ty := range fa.ExprTypes {
		if id, ok := node.(*ast.Ident); ok && ty != nil {
			got[id.Name+"@"+itoa(id.Line)] = ty.String()
		}
	}
	for at, want := range map[string]string{"r@7": "PositiveInt", "m@7": "Int", "k@7": "PositiveInt", "m@9": "Int"} {
		if got[at] != want {
			t.Errorf("%s is a %s; want %s (all: %v)", at, got[at], want, got)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A lambda or capture a std Iter function calls with its source's
// elements takes their range for the parameter that receives one (spec §4
// *Value ranges*), so `%` takes it as a divisor and a PositiveInt slot
// takes it, piped or called directly.
func TestRanges_ElementsOfASource(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a range from one, piped", "    1..=n |> Iter.filter(fn d -> a % d == 0) |> Iter.count()"},
		{"a range from one, called", "    Iter.filter(1..=n, fn d -> a % d == 0) |> Iter.count()"},
		{"a capture's hole", "    1..=n |> Iter.filter(&(a % &1 == 0)) |> Iter.count()"},
		{"a partial capture's slot", "    1..=n |> Iter.map(&pos(&1)) |> Iter.sum()"},
		{"an exclusive range's end", "    Iter.map(0..8, fn i -> a >>> (7 - i)) |> Iter.sum()"},
		{"a start from a binding", "    k = 2\n    k..n |> Iter.map(fn d -> a / d) |> Iter.sum()"},
		{"a start the range of a parameter", "    p..=n |> Iter.map(fn d -> pos(d)) |> Iter.sum()"},
		{"Iter.from through take_while", "    if Iter.from(2) |> Iter.take_while(fn d -> d * d <= a) |> Iter.all_match(fn d -> a % d != 0) then 1 else 0"},
		{"Range.from", "    Range.from(1) |> Iter.take(3) |> Iter.map(fn d -> a / d) |> Iter.sum()"},
		{"Range.naturals", "    Range.naturals() |> Iter.take(3) |> Iter.map(fn i -> nn(i)) |> Iter.sum()"},
		{"a step down", "    Range.step_by(5..=1, -1) |> Iter.map(fn d -> a / d) |> Iter.sum()"},
		{"a list literal", "    [3, 5, 7] |> Iter.filter(fn f -> a % f == 0) |> Iter.count()"},
		{"a list of tuples", "    [(3, 1), (5, 2)] |> Iter.map(fn (f, _) -> a % f) |> Iter.sum()"},
		{"through filter, sort and reverse", "    1..=n |> Iter.filter(fn d -> d > 2) |> Iter.sort() |> Iter.reverse() |> Iter.map(fn d -> a / d) |> Iter.sum()"},
		{"a mapped range", "    0..n |> Iter.map(fn i -> i + 1) |> Iter.map(fn d -> a / d) |> Iter.sum()"},
		{"a mapped capture", "    0..n |> Iter.map(&(&1 * 2 + 1)) |> Iter.map(fn d -> a / d) |> Iter.sum()"},
		{"with_index's element", "    Iter.with_index(1..=n) |> Iter.map(fn (i, d) -> i + a / d) |> Iter.sum()"},
		{"zip's parts", "    Iter.zip(1..=n, 0..n) |> Iter.map(fn (d, i) -> nn(i) + a / d) |> Iter.sum()"},
		{"concat of two", "    Iter.concat(1..=n, [7]) |> Iter.map(fn d -> a / d) |> Iter.sum()"},
		{"reduce's item", "    Iter.reduce(1..=n, 0, fn acc, d -> acc + a / d)"},
		{"sort_with's two", "    1..=n |> Iter.sort_with(fn x, y -> Int.compare(a / x, a / y)) |> Iter.count()"},
		{"Int.max with a positive", "    m = Int.max(1, b)\n    m..=n |> Iter.map(fn d -> a / d) |> Iter.sum()"},
		{"an annotated parameter keeps its type", "    1..=n |> Iter.map(fn d: PositiveInt -> pos(d)) |> Iter.sum()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(rangeSource(tc.body))
			expectNoStdlibErrors(t, errs)
		})
	}
}

// An element whose range does not rule out what the operation needs stays
// an error, and so does one the checker has no range for: a source bound
// to a name, a map whose callback leaves early, and Iter.iterate.
func TestRanges_ElementsProveNothingElse(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"a range from zero", "    0..=n |> Iter.filter(fn d -> a % d == 0) |> Iter.count()", "`%` divides by a NonZeroInt; `d` is an Int"},
		{"a range of any start", "    b..=n |> Iter.filter(fn d -> a % d == 0) |> Iter.count()", "`%` divides by a NonZeroInt; `d` is an Int"},
		{"a shift past the end", "    Iter.map(0..9, fn i -> a >>> (7 - i)) |> Iter.sum()", "`>>>` shifts by a NonNegativeInt"},
		{"a list with a zero", "    [3, 0] |> Iter.map(fn f -> a % f) |> Iter.sum()", "`%` divides by a NonZeroInt; `f` is an Int"},
		{"a source bound first", "    ds = 1..=n\n    ds |> Iter.map(fn d -> a / d) |> Iter.sum()", "`/` divides by a NonZeroInt; `d` is an Int"},
		{"a map that may reach zero", "    1..=n |> Iter.map(fn i -> i - 1) |> Iter.map(fn d -> a / d) |> Iter.sum()", "`/` divides by a NonZeroInt; `d` is an Int"},
		{"a map that leaves early", "    1..=n |> Iter.map(fn i -> if i > 2 then return 0 else i) |> Iter.map(fn d -> a / d) |> Iter.sum()", "`/` divides by a NonZeroInt; `d` is an Int"},
		{"Iter.iterate", "    Iter.iterate(1, fn d -> d + 1) |> Iter.take(3) |> Iter.map(fn d -> a / d) |> Iter.sum()", "`/` divides by a NonZeroInt; `d` is an Int"},
		{"reduce's accumulator", "    Iter.reduce(1..=n, 1, fn acc, _d -> a / acc)", "`/` divides by a NonZeroInt; `acc` is an Int"},
		{"with_index's index", "    Iter.with_index(1..=n) |> Iter.map(fn (i, _d) -> a / i) |> Iter.sum()", "`/` divides by a NonZeroInt; `i` is a NonNegativeInt"},
		{"a partial capture's slot past the range", "    0..=n |> Iter.map(&pos(&1)) |> Iter.sum()", "expected (Int) -> U, got (PositiveInt) -> Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(rangeSource(tc.body))
			expectErrorAmong(t, errs, tc.want)
		})
	}
}

// An element's range leaves the parameter's type as the source's element
// type: `d + 1` over `1..=n` is an Int, and the map's result an Iter<Int>.
func TestRanges_ElementsKeepTheirType(t *testing.T) {
	src := `fn f(n: Int) -> List<Int>
    1..=n |> Iter.map(fn d -> d + 1) |> Iter.to_list()
end
`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	for node, ty := range fa.ExprTypes {
		if id, ok := node.(*ast.Ident); ok && id.Name == "d" && ty != nil && ty.String() != "Int" {
			t.Errorf("d at line %d is a %s; want Int", id.Line, ty)
		}
	}
}
