package rt

import "testing"

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
		{"divide by zero", func() { DecimalDivide(DecimalFromInt(1), DecimalFromInt(0), 2, RoundHalfEven) },
			"Decimal.divide: decimal: division by zero"},
		{"divide inexact under Unnecessary", func() { DecimalDivide(DecimalFromInt(1), DecimalFromInt(3), 2, RoundUnnecessary) },
			"Decimal.divide: decimal: division of 1 by 3 is not exact at scale 2 but mode is Unnecessary"},
		{"round inexact under Unnecessary", func() { DecimalRound(mustParse(t, "1.55"), 1, RoundUnnecessary) },
			"Decimal.round: decimal: rounding necessary to reach scale 1 but mode is Unnecessary"},
		{"from_float inexact under Unnecessary", func() { DecimalFromFloat(0.1, 2, RoundUnnecessary) },
			"Decimal.from_float: decimal: rounding necessary to reach scale 2 but mode is Unnecessary"},
	}
	for _, c := range cases {
		if got := trapMsg(t, c.f); got != c.want {
			t.Errorf("%s: trapped %q, want %q", c.name, got, c.want)
		}
	}
	// And the total paths do not trap, so an empty text above is a real miss.
	if got := trapMsg(t, func() { DecimalRound(mustParse(t, "1.55"), 1, RoundHalfEven) }); got != "" {
		t.Errorf("a HalfEven round trapped %q", got)
	}
}

// DecimalCompare answers an Ordering whose tag is OrderingTag's for the
// variant it names.
func TestDecimalCompareAnswersOrderingTags(t *testing.T) {
	if got := DecimalCompare(DecimalFromInt(1), DecimalFromInt(2)).Tag; got != OrderingTag("Less") {
		t.Errorf("DecimalCompare(1, 2) has tag %d, want Less (%d)", got, OrderingTag("Less"))
	}
}
