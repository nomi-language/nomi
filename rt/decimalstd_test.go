package rt

import (
	"math"
	"math/big"
	"testing"
)

// trapMsg runs f and returns the text of the rt fault it raised, or "" when it
// returned normally.
func trapMsg(t *testing.T, f func()) (msg string) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			fault, ok := p.(*Error)
			if !ok {
				panic(p)
			}
			msg = fault.Msg
		}
	}()
	f()
	return ""
}

// The std/decimal host faults name the std function they came from, so
// `Decimal.divide(1d, 0d, 2, HalfEven)` fails naming `Decimal.divide` rather
// than with the bare arithmetic error.
func TestDecimalStdFaultsNameTheirFunction(t *testing.T) {
	cases := []struct {
		name string
		f    func()
		want string
	}{
		{"divide by zero", func() {
			DecimalDivide(IntToDecimal(SmallInt(1)), IntToDecimal(SmallInt(0)), SmallInt(2), RoundHalfEven)
		},
			"Decimal.divide: decimal: division by zero"},
		{"sqrt of a negative", func() { DecimalSqrtExact(mustParse(t, "-4"), SmallInt(2)) },
			"Decimal.sqrt_exact: negative number -4"},
	}
	for _, c := range cases {
		if got := trapMsg(t, c.f); got != c.want {
			t.Errorf("%s: trapped %q, want %q", c.name, got, c.want)
		}
	}
	// And the total paths do not trap, so an empty text above is a real miss.
	if got := trapMsg(t, func() { DecimalRound(mustParse(t, "1.55"), SmallInt(1), RoundHalfEven) }); got != "" {
		t.Errorf("a HalfEven round trapped %q", got)
	}
}

// round_exact and sqrt_exact answer None where a digit would be lost, and
// the value at the scale asked for where none is.
func TestDecimalExactFormsAnswerNoneWhenInexact(t *testing.T) {
	for _, c := range []struct {
		name string
		got  Maybe[Decimal]
		want string // "" for None
	}{
		{"round 1.50 to 1", DecimalRoundExact(mustParse(t, "1.50"), SmallInt(1)), "1.5"},
		{"round 1.55 to 1", DecimalRoundExact(mustParse(t, "1.55"), SmallInt(1)), ""},
		{"round 1.5 to 3", DecimalRoundExact(mustParse(t, "1.5"), SmallInt(3)), "1.500"},
		{"sqrt 6.25 at 3", DecimalSqrtExact(mustParse(t, "6.25"), SmallInt(3)), "2.500"},
		{"sqrt 2 at 4", DecimalSqrtExact(mustParse(t, "2"), SmallInt(4)), ""},
		{"sqrt 0 at 2", DecimalSqrtExact(mustParse(t, "0"), SmallInt(2)), "0.00"},
	} {
		switch {
		case c.want == "" && c.got.Tag != TagNone:
			t.Errorf("%s = %s, want None", c.name, c.got.Some.Display())
		case c.want != "" && (c.got.Tag != TagSome || c.got.Some.Display() != c.want):
			t.Errorf("%s = %+v, want Some(%s)", c.name, c.got, c.want)
		}
	}
}

// DecimalCompare answers an Ordering whose tag is OrderingTag's for the
// variant it names.
func TestDecimalCompareAnswersOrderingTags(t *testing.T) {
	if got := DecimalCompare(IntToDecimal(SmallInt(1)), IntToDecimal(SmallInt(2))).Tag; got != OrderingTag("Less") {
		t.Errorf("DecimalCompare(1, 2) has tag %d, want Less (%d)", got, OrderingTag("Less"))
	}
}

// A scale an Int names past a Decimal's int32 scale traps naming the scale
// and the limit, rather than wrapping: 4294967298 truncated to 32 bits is 2,
// which once rounded 1.5d to 1.50d.
func TestDecimalStdScalePastInt32Traps(t *testing.T) {
	const huge = int64(1)<<32 + 2
	one := IntToDecimal(SmallInt(1))
	cases := []struct {
		name string
		f    func()
		want string
	}{
		{"round", func() { DecimalRound(mustParse(t, "1.5"), SmallInt(huge), RoundHalfEven) },
			"Decimal.round: scale 4294967298 is past a Decimal's, which is -2147483648 to 2147483647"},
		{"round negative", func() { DecimalRound(mustParse(t, "1.5"), SmallInt(-huge), RoundHalfEven) },
			"Decimal.round: scale -4294967298 is past a Decimal's, which is -2147483648 to 2147483647"},
		{"divide", func() { DecimalDivide(one, IntToDecimal(SmallInt(3)), SmallInt(huge), RoundHalfEven) },
			"Decimal.divide: scale 4294967298 is past a Decimal's, which is -2147483648 to 2147483647"},
		{"sqrt", func() { DecimalSqrt(IntToDecimal(SmallInt(4)), SmallInt(huge), RoundHalfEven) },
			"Decimal.sqrt: scale 4294967298 is past a Decimal's, which is -2147483648 to 2147483647"},
		{"to_decimal", func() { FloatToDecimal(1.5, SmallInt(huge), RoundHalfEven) },
			"Float.to_decimal: scale 4294967298 is past a Decimal's, which is -2147483648 to 2147483647"},
		// A scale past int64 reaches the same trap, naming it whole.
		{"round past int64", func() {
			DecimalRound(mustParse(t, "1.5"), BigIntOf(new(big.Int).Lsh(big.NewInt(1), 70)), RoundHalfEven)
		},
			"Decimal.round: scale 1180591620717411303424 is past a Decimal's, which is -2147483648 to 2147483647"},
		// A scale that fits whose root needs 10^(2s - k) past int32 digits.
		{"sqrt doubled scale", func() {
			DecimalSqrt(Decimal{Mantissa: big.NewInt(4), Scale: math.MinInt32}, SmallInt(math.MaxInt32), RoundHalfEven)
		},
			"Decimal.sqrt: a root at scale 2147483647 of a number at scale -2147483648 needs 10^6442450942, past a Decimal's scale, which is -2147483648 to 2147483647"},
		// Products and quotients of Decimals whose scales add past int32.
		{"mul", func() { MulDecimal(Decimal{Scale: 1 << 30}, Decimal{Scale: 1 << 30}) },
			"Decimal *: the product's scale, 1073741824 plus 1073741824, is past a Decimal's, which is -2147483648 to 2147483647"},
		{"divide shift", func() {
			DecimalDivide(Decimal{Mantissa: big.NewInt(1), Scale: math.MinInt32}, one, SmallInt(math.MaxInt32), RoundHalfEven)
		},
			"Decimal.divide: decimal: a quotient at scale 2147483647 of a dividend at scale -2147483648 by a divisor at scale 0 needs 10^4294967295, past a Decimal's scale, which is -2147483648 to 2147483647"},
		{"round drop", func() {
			DecimalRound(Decimal{Mantissa: big.NewInt(1), Scale: math.MaxInt32}, SmallInt(math.MinInt32), RoundHalfEven)
		},
			"Decimal.round: decimal: rounding from scale 2147483647 to scale -2147483648 drops 4294967295 digits, past a Decimal's scale, which is -2147483648 to 2147483647"},
	}
	for _, c := range cases {
		if got := trapMsg(t, c.f); got != c.want {
			t.Errorf("%s: trapped %q, want %q", c.name, got, c.want)
		}
	}
}
