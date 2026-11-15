package rt

import (
	"math"
	"math/big"
	"testing"
	"time"
)

// InstantTimeOf keeps the moment of every Instant a program names, past
// UnixNano's 64 bits and before the epoch, and clamps only past 2^62
// seconds, where time.Unix's own arithmetic would wrap.
func TestInstantTimeOf_KeepsTheMoment(t *testing.T) {
	big70 := BigIntOf(new(big.Int).Lsh(big.NewInt(1), 70))
	for _, tc := range []struct {
		ns   Int
		want time.Time
	}{
		{SmallInt(0), time.Unix(0, 0).UTC()},
		{SmallInt(-1), time.Unix(-1, 999_999_999).UTC()},
		{SmallInt(math.MaxInt64), time.Unix(0, math.MaxInt64).UTC()},
		{big70, time.Unix(1_180_591_620_717, 411_303_424).UTC()},
	} {
		if got := InstantTimeOf(tc.ns); !got.Equal(tc.want) {
			t.Errorf("InstantTimeOf(%s) = %v, want %v", tc.ns, got, tc.want)
		}
	}
	huge := BigIntOf(new(big.Int).Lsh(big.NewInt(1), 200))
	if got := InstantTimeOf(huge); !got.Equal(time.Unix(1<<62, 0)) {
		t.Errorf("InstantTimeOf(2^200) = %v, want the clamp at 2^62 seconds", got)
	}
}

// InstantFromTime answers the exact Instant of every Go time, past the
// 64 bits UnixNano counts (where 9999-12-31 read as a moment in 1816), and
// InstantTimeOf takes each back to the same moment.
func TestInstantFromTime_IsExact(t *testing.T) {
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Unix(0, math.MinInt64), "-9223372036854775808"},
		{time.Unix(0, math.MaxInt64), "9223372036854775807"},
		{time.Unix(1_700_000_000, 5).In(time.FixedZone("", -5*3600)), "1700000000000000005"},
		{time.Unix(0, math.MinInt64).Add(-1), "-9223372036854775809"},
		{time.Unix(0, math.MaxInt64).Add(1), "9223372036854775808"},
		{time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), "253402300799000000000"},
		{time.Time{}, "-62135596800000000000"},
	} {
		got := InstantFromTime(tc.at)
		if got.String() != tc.want {
			t.Errorf("%v: %s, want %s", tc.at, got, tc.want)
		}
		if back := InstantTimeOf(got); !back.Equal(tc.at) {
			t.Errorf("%v: back as %v", tc.at, back)
		}
	}
}
