package hostgen

import (
	"fmt"
	"math/big"
	"reflect"

	"github.com/nomi-language/nomi/internal/ast"
)

// IntRange is the values an Int position's declared type admits, as far as
// the generator can read them off the declaration: the bounds a subtype
// chain's rules state, nil on a side with none. A plain Int has no range.
//
// The generator reads a rule's bounds, not its meaning. A rule that is a
// lambda whose body is comparisons of its parameter with Int literals,
// joined by `and` (`fn n -> n >= 0 and n <= 255`), bounds the subtype; a
// conjunct of any other form narrows the values further and is ignored,
// which keeps the range a superset of the values. A rule of another form
// (a function's name, an `or`) bounds nothing. A subtype's range is the
// intersection of its parents' and its own rule's, and so is an
// intersection type's (`NonNegativeInt and Int64`).
type IntRange struct {
	Min, Max *big.Int
	// Via is the declared type the range is read from, as written, for
	// refusals.
	Via string
}

// intersect is the values both ranges admit; either may be nil.
func (r *IntRange) intersect(o *IntRange) *IntRange {
	if r == nil {
		return o
	}
	if o == nil {
		return r
	}
	out := &IntRange{Min: r.Min, Max: r.Max, Via: r.Via}
	if o.Min != nil && (out.Min == nil || o.Min.Cmp(out.Min) > 0) {
		out.Min = o.Min
	}
	if o.Max != nil && (out.Max == nil || o.Max.Cmp(out.Max) < 0) {
		out.Max = o.Max
	}
	return out
}

// within reports whether every value r admits lies in [min, max].
func (r *IntRange) within(min, max *big.Int) bool {
	return r != nil && r.Min != nil && r.Max != nil && r.Min.Cmp(min) >= 0 && r.Max.Cmp(max) <= 0
}

// key is the range as it enters a memo key: two Int positions of different
// ranges are checked apart.
func (r *IntRange) key() string {
	if r == nil {
		return ""
	}
	side := func(b *big.Int) string {
		if b == nil {
			return "_"
		}
		return b.String()
	}
	return "[" + side(r.Min) + ".." + side(r.Max) + "]"
}

// ruleRange reads the bounds a subtype's `when` rule states
// (ast.IntRuleBounds). A conjunct it cannot read narrows the values further,
// so the range it leaves out is still a superset of them.
func ruleRange(rule ast.Node) *IntRange {
	min, max, _ := ast.IntRuleBounds(rule)
	if min == nil && max == nil {
		return nil
	}
	return &IntRange{Min: min, Max: max}
}

// goIntRange is the values a fixed-width Go integer type holds, and a std
// type whose every value it holds, to suggest in a refusal: `Int64` for a
// 64-bit signed integer, `NonNegativeInt and Int64` for a 64-bit unsigned
// one, and "" for a narrower width, which needs a subtype of the
// program's own.
func goIntRange(t reflect.Type) (min, max *big.Int, subtype string) {
	signed := func(bits uint) (*big.Int, *big.Int) {
		m := new(big.Int).Lsh(big.NewInt(1), bits-1)
		return new(big.Int).Neg(m), new(big.Int).Sub(m, big.NewInt(1))
	}
	unsigned := func(bits uint) (*big.Int, *big.Int) {
		m := new(big.Int).Lsh(big.NewInt(1), bits)
		return big.NewInt(0), m.Sub(m, big.NewInt(1))
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int64:
		// Go's int is 64 bits on every target Nomi builds for (wasm
		// included).
		min, max = signed(64)
		subtype = "Int64"
	case reflect.Int32:
		min, max = signed(32)
	case reflect.Int16:
		min, max = signed(16)
	case reflect.Int8:
		min, max = signed(8)
	case reflect.Uint, reflect.Uint64, reflect.Uintptr:
		min, max = unsigned(64)
		subtype = "NonNegativeInt and Int64"
	case reflect.Uint32:
		min, max = unsigned(32)
	case reflect.Uint16:
		min, max = unsigned(16)
	}
	return min, max, subtype
}

// fits refuses an Int position whose declared type admits a value the
// fixed-width Go integer t cannot hold, so no adapter meets such a value
// as the program runs. A Go function that takes an Int of any size takes
// rt.Int and defines what a large one means.
func (g *Generator) fits(s *Shape, t reflect.Type) error {
	min, max, want := goIntRange(t)
	if min == nil || s.Range.within(min, max) {
		return nil
	}
	declared := "Int"
	if s.Range != nil && s.Range.Via != "" {
		declared = s.Range.Via
	}
	switch {
	case want == "" || declared == "Int":
	case want == "NonNegativeInt and Int64" && declared == "Int64":
	case want == "NonNegativeInt and Int64" && (declared == "NonNegativeInt" || declared == "PositiveInt"):
		want = declared + " and Int64"
	default:
		want = declared + " and " + want
	}
	fit := fmt.Sprintf("a subtype whose `when` bounds it within %s..%s", min, max)
	if want != "" {
		fit = fmt.Sprintf("`%s` (std::int), or a subtype whose `when` bounds it within that range", want)
	}
	return fmt.Errorf("Go %s holds an Int in %s..%s, and %s admits Ints past that: declare it %s; a Go function that takes an Int of any size takes rt.Int", t, min, max, declared, fit)
}
