package rt

import (
	"math"
	"math/big"
	"testing"
)

func bigOf(t *testing.T, text string) Value {
	t.Helper()
	v, ok := ParseIntValue(text)
	if !ok {
		t.Fatalf("%q is not an Int", text)
	}
	return v
}

// TestInt_EveryOperationAnswersInCanonicalForm: a value that fits int64 is an
// int64 whichever operation produced it, including every operation that can
// bring a big value back into range.
func TestInt_EveryOperationAnswersInCanonicalForm(t *testing.T) {
	two64 := bigOf(t, "18446744073709551616")
	two63 := bigOf(t, "9223372036854775808")
	for _, c := range []struct {
		name string
		got  Value
		want string
	}{
		{"big - big", IntSub(IntAdd(two64, int64(5)), two64), "5"},
		{"big + big", IntAdd(two64, IntNeg(two64)), "0"},
		{"big * 0", IntMul(two64, int64(0)), "0"},
		{"big * small", IntMul(two63, int64(-1)), "-9223372036854775808"},
		{"big / big", must(IntQuo(two64, two63)), "2"},
		{"big % big", must(IntRem(IntAdd(two64, int64(7)), two64)), "7"},
		{"small / big", must(IntQuo(int64(5), two64)), "0"},
		{"-big", IntNeg(two63), "-9223372036854775808"},
		{"-MinInt64", IntNeg(int64(math.MinInt64)), "9223372036854775808"},
		{"MaxInt64 + 1", IntAdd(int64(math.MaxInt64), int64(1)), "9223372036854775808"},
		{"MinInt64 - 1", IntSub(int64(math.MinInt64), int64(1)), "-9223372036854775809"},
		{"MinInt64 / -1", must(IntQuo(int64(math.MinInt64), int64(-1))), "9223372036854775808"},
		{"MinInt64 % -1", must(IntRem(int64(math.MinInt64), int64(-1))), "0"},
		{"MaxInt64 * MaxInt64", IntMul(int64(math.MaxInt64), int64(math.MaxInt64)), "85070591730234615847396907784232501249"},
	} {
		if s := FormatIntValue(c.got); s != c.want {
			t.Errorf("%s = %s, want %s", c.name, s, c.want)
		}
		asBig, isBig := c.got.(*BigInt)
		fits := new(big.Int)
		fits.SetString(c.want, 10)
		if isBig && fits.IsInt64() {
			t.Errorf("%s answered a *BigInt for %s, which fits int64", c.name, asBig)
		}
		if !isBig && !fits.IsInt64() {
			t.Errorf("%s answered %T for %s, which does not fit int64", c.name, c.got, c.want)
		}
	}
}

func must(v Value, ok bool) Value {
	if !ok {
		panic("a zero divisor")
	}
	return v
}

func TestInt_ADivisorOfZeroIsRefused(t *testing.T) {
	if _, ok := IntQuo(bigOf(t, "1"+"000000000000000000000"), int64(0)); ok {
		t.Error("big / 0 answered")
	}
	if _, ok := IntRem(int64(5), int64(0)); ok {
		t.Error("5 % 0 answered")
	}
}

// TestInt_EqualValuesAreEqualAndHashAlikeHoweverBuilt holds equality,
// ordering and hashing to the value, whichever way it was computed, and a
// Map to find a key by an equal value built another way.
func TestInt_EqualValuesAreEqualAndHashAlikeHoweverBuilt(t *testing.T) {
	parsed := bigOf(t, "100000000000000000001")
	computed := IntAdd(IntMul(int64(100000000000), int64(1000000000)), int64(1))
	fromBig := IntFromBig(new(big.Int).Add(new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil), big.NewInt(1)))
	for _, v := range []Value{computed, fromBig} {
		if !Equal(parsed, v) || !IntEqual(parsed, v) || IntCompare(parsed, v) != 0 {
			t.Errorf("%v and %v are one value and compare unequal", parsed, v)
		}
		if Hash(parsed) != Hash(v) {
			t.Errorf("%v and %v are one value and hash differently", parsed, v)
		}
	}
	if Equal(parsed, IntAdd(parsed, int64(1))) {
		t.Error("two different big values compare equal")
	}
	m := MapOf[any, any](Hash, Equal, []MapEntry[any, any]{{Key: parsed, Val: "big"}, {Key: int64(5), Val: "small"}})
	if v, ok := MapLookup(m, Hash, Equal, computed); !ok || v != "big" {
		t.Errorf("a Map keyed by a big Int does not find it by an equal value: %v %v", v, ok)
	}
	small := IntSub(IntAdd(parsed, int64(5)), parsed)
	if v, ok := MapLookup(m, Hash, Equal, small); !ok || v != "small" {
		t.Errorf("a Map keyed by 5 does not find big + 5 - big: %v %v", v, ok)
	}
	if IntCompare(IntNeg(parsed), int64(math.MinInt64)) >= 0 || IntCompare(parsed, int64(math.MaxInt64)) <= 0 {
		t.Error("big values do not order outside the int64 range")
	}
}

// TestInt_ARecordKeepsABigFieldBesideItsWord holds a record's Int slot to its
// value: the word and the big slot, through Clone and With, equality and
// hashing, and a slot written small again.
func TestInt_ARecordKeepsABigFieldBesideItsWord(t *testing.T) {
	d := NewStructDesc("main.Account", []FieldSpec{{Name: "id", Type: SlotInt}, {Name: "name", Type: SlotRef}, {Name: "balance", Type: SlotInt}})
	huge := bigOf(t, "123456789012345678901234567890")
	a := d.Make(int64(1), "alice", huge)
	if len(a.R) != a.Layout().NR+a.Layout().NW {
		t.Errorf("a record holding a big Int has %d reference slots, want %d", len(a.R), a.Layout().NR+a.Layout().NW)
	}
	if got := a.Field(2); !IntEqual(got, huge) {
		t.Errorf("the big field reads back as %v", got)
	}
	plain := d.Make(int64(1), "alice", int64(math.MinInt64))
	if len(plain.R) != plain.Layout().NR {
		t.Errorf("a record holding only int64s grew its reference slots to %d", len(plain.R))
	}
	if got := plain.Field(2); got != int64(math.MinInt64) {
		t.Errorf("math.MinInt64 in a slot reads back as %v", got)
	}
	b := a.Clone()
	if !Equal(a, b) || Hash(a) != Hash(b) || !IntEqual(b.Field(2), huge) {
		t.Error("a clone of a record with a big field is not equal to it")
	}
	if Equal(a, plain) {
		t.Error("a record with a big field equals one with math.MinInt64 there")
	}
	c := a.With([]int{2}, []any{int64(7)})
	if got := c.Field(2); got != int64(7) {
		t.Errorf("a big field written small reads back as %v", got)
	}
	if !IntEqual(a.Field(2), huge) {
		t.Error("With changed the record it copied")
	}
	again := d.Make(int64(1), "alice", IntAdd(IntSub(huge, int64(1)), int64(1)))
	if !Equal(a, again) || Hash(a) != Hash(again) {
		t.Error("two records with one big value built two ways are unequal")
	}
}

// TestInt_Wrap64IsTheLowSixtyFourBits holds the wrapping functions' reduction.
func TestInt_Wrap64IsTheLowSixtyFourBits(t *testing.T) {
	for text, want := range map[string]int64{
		"18446744073709551621":  5,
		"9223372036854775808":   math.MinInt64,
		"-9223372036854775809":  math.MaxInt64,
		"-18446744073709551617": -1,
		"36893488147419103231":  -1,
	} {
		if got := Wrap64(bigOf(t, text)); got != want {
			t.Errorf("Wrap64(%s) = %d, want %d", text, got, want)
		}
	}
	if got := IntWrapMul(SmallInt(math.MaxInt64), SmallInt(2)); got != -2 {
		t.Errorf("wrapping_mul(MaxInt64, 2) = %d", got)
	}
}

// TestInt_TheBitwiseFunctionsReadTwosComplementAtAnySize.
func TestInt_TheBitwiseFunctionsReadTwosComplementAtAnySize(t *testing.T) {
	two80 := IntOf(bigOf(t, "1208925819614629174706176"))
	if got := IntBitAnd(SmallInt(-1), two80); got.String() != two80.String() {
		t.Errorf("-1 & 2^80 = %s", got)
	}
	if got := IntBitOr(two80, SmallInt(5)); got.String() != "1208925819614629174706181" {
		t.Errorf("2^80 | 5 = %s", got)
	}
	if got := IntBitNot(two80); got.String() != "-1208925819614629174706177" {
		t.Errorf("^2^80 = %s", got)
	}
	if got := IntShiftLeft(SmallInt(1), SmallInt(80)); got.String() != two80.String() {
		t.Errorf("1 << 80 = %s", got)
	}
	if got := IntShiftRight(IntOf(IntNeg(two80.Value())), SmallInt(79)); got.String() != "-2" {
		t.Errorf("-2^80 >> 79 = %s", got)
	}
	if got := IntShiftRight(SmallInt(-1), SmallInt(500)); got.String() != "-1" {
		t.Errorf("-1 >> 500 = %s", got)
	}
}

// TestInt_PowRefusesAPowerTooLargeToHold: a power past maxShift bits faults
// with a reason rather than allocating without end.
func TestInt_PowRefusesAPowerTooLargeToHold(t *testing.T) {
	if got := IntPow(SmallInt(2), SmallInt(100)); got.String() != "1267650600228229401496703205376" {
		t.Errorf("2^100 = %s", got)
	}
	if got := IntPow(SmallInt(-1), IntOf(bigOf(t, "100000000000000000001"))); got.String() != "-1" {
		t.Errorf("(-1)^(10^20 + 1) = %s", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("2^(2^62) answered")
		}
	}()
	IntPow(SmallInt(2), SmallInt(1<<62))
}

// TestInt_SmallArithmeticAllocatesNothing counts, rather than times, the cost
// that matters on the fast path.
func TestInt_SmallArithmeticAllocatesNothing(t *testing.T) {
	var sink Value
	allocs := testing.AllocsPerRun(100, func() {
		sink = IntAdd(int64(1<<40), int64(3))
		sink = IntMul(sink, int64(2))
		sink = IntSub(sink, int64(1))
		_ = IntCompare(sink, int64(9))
	})
	// The int64 result boxed into a Value is the one allocation Go makes
	// for a value of 256 or more; the arithmetic adds none of its own.
	if allocs > 3 {
		t.Errorf("small Int arithmetic allocates %.0f times per run", allocs)
	}
	_ = sink
}

func TestInt_ParseAndFormatAtAnySize(t *testing.T) {
	for _, text := range []string{"0", "-1", "9223372036854775807", "-9223372036854775808", "9223372036854775808", "-123456789012345678901234567890"} {
		v, ok := ParseIntValue(text)
		if !ok || FormatIntValue(v) != text {
			t.Errorf("%s reads back as %v", text, v)
		}
	}
	for _, text := range []string{"", "1.5", "0x10", "1_000", " 1", "--1"} {
		if _, ok := ParseIntValue(text); ok {
			t.Errorf("%q parsed as an Int", text)
		}
	}
}

func TestInt_IntSaturatedClampsPastInt64(t *testing.T) {
	if got := IntSaturated(bigOf(t, "100000000000000000000")); got != math.MaxInt64 {
		t.Errorf("IntSaturated(10^20) = %d", got)
	}
	if got := IntSaturated(bigOf(t, "-100000000000000000000")); got != math.MinInt64 {
		t.Errorf("IntSaturated(-10^20) = %d", got)
	}
}
