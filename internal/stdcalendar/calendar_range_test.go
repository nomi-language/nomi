package stdcalendar

import (
	"math"
	"math/big"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// bigInt is the Int s spells in base 10.
func bigInt(t *testing.T, s string) rt.Int {
	t.Helper()
	z, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("%q is not an Int", s)
	}
	return rt.BigIntOf(z)
}

// A civil shift of any count answers the exact date, however far it goes,
// past int64 counts and past int64 years, never trapping and never
// wrapping. The expected dates come from the same proleptic Gregorian arithmetic in exact
// integers, computed apart from this package.
func TestCivilShiftHasNoRange(t *testing.T) {
	jan1 := rt.Date{Year: rt.SmallInt(2026), Month: 1, Day: 1}
	jan31 := rt.Date{Year: rt.SmallInt(2026), Month: 1, Day: 31}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"days", DateToString(DatePlusDays(jan1, rt.SmallInt(math.MaxInt64))), "+25252734927768580-07-27"},
		{"negative days", DateToString(DatePlusDays(jan1, rt.SmallInt(math.MinInt64))), "-25252734927764529-06-07"},
		{"months", DateToString(DatePlusMonths(jan31, rt.SmallInt(math.MaxInt64))), "+768614336404566676-08-31"},
		{"years", DateToString(DatePlusYears(jan1, rt.SmallInt(300_000_000_000))), "+300000002026-01-01"},
		{"years past int64", DateToString(DatePlusYears(jan1, bigInt(t, "1000000000000000000000000000000"))),
			"+1000000000000000000000000002026-01-01"},
		{"hours", ISONaiveText(CivilShift(rt.NaiveDateTime{Year: rt.SmallInt(2026), Month: 1, Day: 1, Hour: 23},
			zero, zero, zero, rt.SmallInt(math.MaxInt64), zero, zero, zero)), "+1052197288658965-10-11T06:00:00"},
		{"zoned years", DateTimeToString(DateTimeCivilShiftOrTrap(rt.DateTime{Zone: "UTC"},
			rt.SmallInt(300_000_000_000), zero, zero, zero, zero, zero, zero)), "+300000001970-01-01T00:00:00+00:00[UTC]"},
		{"offset years", OffsetDateTimeToString(OffsetDateTimePlusYears(rt.OffsetDateTime{}, rt.SmallInt(300_000_000_000))),
			"+300000001970-01-01T00:00:00+00:00"},
		{"offset duration", OffsetDateTimeToString(OffsetDateTimePlusNanos(rt.OffsetDateTime{}, bigInt(t, "1000000000000000000000000000000"))),
			"+31688738508781-06-07T01:46:40+00:00"},
		{"zoned duration", DateTimeToString(DateTimePlusNanos(rt.DateTime{Zone: "UTC"}, bigInt(t, "1000000000000000000000000000000"))),
			"+31688738508781-06-07T01:46:40+00:00[UTC]"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %s, want %s", c.name, c.got, c.want)
		}
	}
	// Each answer counts back to where it started.
	far := DatePlusDays(jan1, rt.SmallInt(math.MaxInt64))
	if got := DateDaysBetween(jan1, far); got != rt.SmallInt(math.MaxInt64) {
		t.Errorf("days back from %s = %s", DateToString(far), got)
	}
	if got := DatePlusYears(DatePlusYears(jan1, bigInt(t, "1000000000000000000000000000000")), bigInt(t, "-1000000000000000000000000000000")); got != jan1 {
		t.Errorf("a year shift there and back is %s", DateToString(got))
	}
	// The ordinary shifts beside them still answer.
	if got := DatePlusDays(jan1, rt.SmallInt(365)); got != (rt.Date{Year: rt.SmallInt(2027), Month: 1, Day: 1}) {
		t.Errorf("2026-01-01 + 365 days = %+v", got)
	}
	if got := CivilShift(rt.NaiveDateTime{Year: rt.SmallInt(2026), Month: 1, Day: 31, Hour: 23}, zero, rt.SmallInt(1), rt.SmallInt(1), rt.SmallInt(1), zero, zero, rt.SmallInt(-1)); got != (rt.NaiveDateTime{Year: rt.SmallInt(2026), Month: 3, Day: 1, Hour: 23, Minute: 59, Second: 59, Nanosecond: 999_999_999}) {
		t.Errorf("Jan 31 23:00 + 1 month + 1 day + 1 hour - 1ns = %+v", got)
	}
	if got := DatePlusDays(jan1, rt.SmallInt(-1)); got != (rt.Date{Year: rt.SmallInt(2025), Month: 12, Day: 31}) {
		t.Errorf("2026-01-01 - 1 day = %+v", got)
	}
}

// The day number of a year past the int64 path's (DaysFromCivil's math/big
// one) agrees with the int64 arithmetic wherever both can count it, and
// CivilFromDays reads it back.
func TestDayNumbersAgreeAcrossThePaths(t *testing.T) {
	for _, y := range []int64{smallYear, smallYear + 1, smallYear + 399, -smallYear, -smallYear - 1, -smallYear - 401, 1 << 50, -(1 << 50)} {
		for _, md := range [][2]int64{{1, 1}, {2, 28}, {2, 29}, {3, 1}, {12, 31}} {
			if md[1] > DaysInMonth(rt.SmallInt(y), md[0]) {
				continue
			}
			got := DaysFromCivil(rt.SmallInt(y), md[0], md[1])
			want := daysFromCivil64(y, md[0], md[1])
			if got != rt.SmallInt(want) {
				t.Errorf("%d-%02d-%02d: day %s, want %d", y, md[0], md[1], got, want)
			}
			// The big path of CivilFromDays, from a day number past 2^60
			// reached by whole 400-year cycles.
			shift := mulI(rt.SmallInt(1<<50), rt.SmallInt(146097))
			yy, m, d := CivilFromDays(addI(got, shift))
			if yy != addI(rt.SmallInt(y), mulI(rt.SmallInt(1<<50), rt.SmallInt(400))) || m != md[0] || d != md[1] {
				t.Errorf("%d-%02d-%02d shifted 2^50 cycles reads back as %s-%02d-%02d", y, md[0], md[1], yy, m, d)
			}
		}
	}
	// A weekday and a day of the year past int64 years.
	huge := bigInt(t, "1000000000000000000000000000000")
	if got := WeekdayOf(huge, 1, 1); got != 6 {
		t.Errorf("weekday of +1e30-01-01 = %d, want 6 (Saturday)", got)
	}
	if got := DayOfYear(huge, 12, 31); got != 366 {
		t.Errorf("day of the year of +1e30-12-31 = %d, want 366 (a leap year, divisible by 400)", got)
	}
}

// A year past four digits is written, and read back, in ISO 8601's
// expanded form: a sign and at least six digits.
func TestExpandedYears(t *testing.T) {
	for _, c := range []struct {
		year int64
		text string
	}{
		{0, "0000"}, {1, "0001"}, {9999, "9999"}, {10000, "+010000"}, {-1, "-000001"},
		{123456789, "+123456789"}, {-123456789, "-123456789"},
	} {
		if got := ISOYearText(rt.SmallInt(c.year)); got != c.text {
			t.Errorf("year %d is written %q, want %q", c.year, got, c.text)
		}
		r := ParseDate(c.text + "-02-28")
		if r.Tag != rt.TagOk || r.Ok.Year != rt.SmallInt(c.year) {
			t.Errorf("%s-02-28 reads as %+v", c.text, r)
		}
	}
	for _, s := range []string{"10000-01-01", "-0044-03-15", "+2026-05-04"} {
		if r := ParseDate(s); r.Tag != rt.TagOk {
			t.Errorf("%s: %+v", s, r.Err)
		}
	}
	for _, s := range []string{"999-01-01", "-000000-01-01", "+12-01-01", "12-01-01"} {
		if r := ParseDate(s); r.Tag != rt.TagErr || r.Err.Tag != rt.TagInvalidFormat {
			t.Errorf("%s: %+v, want InvalidFormat", s, r)
		}
	}
	for _, s := range []string{
		"+1000000000000000000000000002026-02-28T12:00:00",
		"-000001-12-31T23:59:59.5",
	} {
		r := ParseNaiveDateTime(s)
		if r.Tag != rt.TagOk || ISONaiveText(r.Ok) != s {
			t.Errorf("%s reads back as %+v", s, r)
		}
	}
	if r := ParseZonedDateTime("+010000-07-04T12:00:00-04:00[America/New_York]"); r.Tag != rt.TagOk ||
		DateTimeToString(r.Ok) != "+010000-07-04T12:00:00-04:00[America/New_York]" {
		t.Errorf("a zoned reading in the year 10000: %+v", r)
	}
	// February 29 follows the leap rule in every year.
	for _, c := range []struct {
		year string
		ok   bool
	}{{"+1000000000000000000000000000000", true}, {"+1000000000000000000000000000100", false}, {"-000004", true}, {"-000100", false}} {
		if r := ParseDate(c.year + "-02-29"); (r.Tag == rt.TagOk) != c.ok {
			t.Errorf("%s-02-29: %+v", c.year, r)
		}
	}
}

// An OffsetDateTime and a DateTime hold an instant of any size, so a wall
// reading past the 64 bits of nanoseconds Go's UnixNano counts (the years
// 1677 to 2262) is an ordinary value, read back exactly.
func TestOffsetAndZonedHoldEveryDate(t *testing.T) {
	y3000 := rt.NaiveDateTime{Year: rt.SmallInt(3000), Month: 1, Day: 1, Hour: 12, Nanosecond: 5}
	odt := OffsetDateTimeWithOffset(y3000, rt.Duration(3600*nanosPerSecond))
	if odt.Tag != rt.TagOk {
		t.Fatalf("with_offset in 3000: %+v", odt.Err)
	}
	if got := OffsetDateTimeToString(odt.Ok); got != "3000-01-01T12:00:00.000000005+01:00" {
		t.Errorf("with_offset in 3000 renders %q", got)
	}
	if got := OffsetDateTimeToString(DateTimeToOffset(rt.DateTime{InstantNanos: odt.Ok.InstantNanos, Zone: "UTC"})); got != "3000-01-01T11:00:00.000000005+00:00" {
		t.Errorf("the same instant in UTC renders %q", got)
	}
	for _, s := range []string{
		"3000-01-01T00:00:00+00:00",
		"1066-10-14T09:00:00-00:01",
		"-000001-01-01T00:00:00+00:00",
		"+099999-12-31T23:59:59.999999999+14:00",
		"+40170248388634916374650-11-01T16:36:16+00:00",
	} {
		r := ParseOffsetDateTime(s)
		if r.Tag != rt.TagOk {
			t.Errorf("%s: %+v", s, r.Err)
			continue
		}
		if got := OffsetDateTimeToString(r.Ok); got != s {
			t.Errorf("%s renders %q", s, got)
		}
		back := OffsetDateTimeFromInstant(r.Ok.InstantNanos, r.Ok.OffsetSeconds)
		if OffsetDateTimeToString(back) != s {
			t.Errorf("%s from its instant: %+v", s, back)
		}
	}
	// New York's tzdb entry ends in a rule it extends forever, so a summer
	// a thousand years on keeps daylight saving, and a date before the
	// first transition reads at local mean time.
	for _, c := range []struct{ in, want string }{
		{"3026-07-04T12:00:00", "3026-07-04T12:00:00-04:00[America/New_York]"},
		{"3026-01-04T12:00:00", "3026-01-04T12:00:00-05:00[America/New_York]"},
		{"1700-01-01T00:00:00", "1700-01-01T00:00:00-04:56[America/New_York]"},
	} {
		r := ResolveInZone(mustParseNaive(t, c.in), "America/New_York", rt.Disambiguation{Tag: rt.TagReject})
		if r.Tag != rt.TagOk {
			t.Errorf("%s in New York: %+v", c.in, r.Err)
			continue
		}
		if got := DateTimeToString(r.Ok); got != c.want {
			t.Errorf("%s in New York renders %q, want %q", c.in, got, c.want)
		}
	}
	// A gap a thousand years on is still a gap.
	if r := ResolveInZone(mustParseNaive(t, "3026-03-12T02:30:00"), "America/New_York", rt.Disambiguation{Tag: rt.TagReject}); r.Tag != rt.TagErr || r.Err.Tag != rt.TagNonexistent {
		t.Errorf("2:30 on 3026's spring-forward day: %+v", r)
	}
}

// An instant of any size has a reading, at an offset and in a zone. Past
// int64 seconds, about 292 billion years either way, a zone's offset is the
// one at the nearest end.
func TestEveryInstantHasAReading(t *testing.T) {
	huge := bigInt(t, "1000000000000000000000000000000")
	if got := OffsetDateTimeToString(OffsetDateTimeFromInstant(huge, 0)); got != "+31688738508781-06-07T01:46:40+00:00" {
		t.Errorf("from_instant at 10^30ns: %s", got)
	}
	r := DateTimeFromInstantIn(huge, "America/New_York")
	if r.Tag != rt.TagOk {
		t.Fatalf("from_instant_in at 10^30ns: %+v", r.Err)
	}
	if got := DateTimeToString(r.Ok); got != "+31688738508781-06-06T20:46:40-05:00[America/New_York]" {
		t.Errorf("from_instant_in at 10^30ns: %s", got)
	}
	// 100 billion years on.
	far := rt.BigIntOf(new(big.Int).Mul(big.NewInt(3_155_760_000_000_000_000), big.NewInt(1_000_000_000)))
	if r := DateTimeFromInstantIn(far, "America/New_York"); r.Tag != rt.TagOk || DateTimeYear(r.Ok) != rt.SmallInt(100_002_055_400) {
		t.Errorf("100 billion years on: %+v, year %s", r, DateTimeYear(r.Ok))
	}
	if r := DateTimeFromInstantIn(huge, "Mars/Olympus"); r.Tag != rt.TagErr || r.Err.Tag != rt.TagUnknownZone {
		t.Errorf("an unknown zone: %+v", r)
	}
}

// Days between two dates counts every day, however far apart: the route
// through time.Duration saturated at about 292 years.
func TestDateDaysBetweenIsExact(t *testing.T) {
	a, b := rt.Date{Year: rt.SmallInt(2000), Month: 1, Day: 1}, rt.Date{Year: rt.SmallInt(2500), Month: 1, Day: 1}
	if got := DateDaysBetween(a, b); got != rt.SmallInt(182622) {
		t.Errorf("2000-01-01 to 2500-01-01 = %s days, want 182622", got)
	}
	if got := DateDaysBetween(b, a); got != rt.SmallInt(-182622) {
		t.Errorf("2500-01-01 to 2000-01-01 = %s days, want -182622", got)
	}
	if got := DateDaysBetween(rt.Date{Year: rt.SmallInt(2024), Month: 2, Day: 28}, rt.Date{Year: rt.SmallInt(2024), Month: 3, Day: 1}); got != rt.SmallInt(2) {
		t.Errorf("2024-02-28 to 2024-03-01 = %s days, want 2", got)
	}
	if got := DateDaysBetween(rt.Date{Year: rt.SmallInt(1970), Month: 1, Day: 1}, rt.Date{Year: rt.SmallInt(300_000_002_026), Month: 1, Day: 1}); got != rt.SmallInt(109572750020454) {
		t.Errorf("1970-01-01 to +300000002026-01-01 = %s days", got)
	}
}

// A Duration near int64's end added to a time of day wraps around midnight
// as any other does, rather than overflowing the sum first.
func TestTimePlusAHugeDurationWrapsByTheDay(t *testing.T) {
	const dayNanos = int64(24 * 60 * 60 * 1e9)
	start := rt.Time{Hour: 23}
	startNs := int64(23 * 60 * 60 * 1e9)
	for _, d := range []int64{math.MaxInt64, math.MinInt64, math.MaxInt64 - 1} {
		want := ((startNs+d%dayNanos)%dayNanos + dayNanos) % dayNanos
		got := TimePlusDuration(start, rt.Duration(d))
		gotNs := got.Hour*3_600_000_000_000 + got.Minute*60_000_000_000 + got.Second*1_000_000_000 + got.Nanosecond
		if gotNs != want {
			t.Errorf("23:00 + %d ns = %+v (%d ns into the day), want %d", d, got, gotNs, want)
		}
	}
}

// smallNanos is an instant the test built within int64 nanoseconds.
func smallNanos(t *testing.T, ns rt.Int) int64 {
	t.Helper()
	n, ok := ns.Int64()
	if !ok {
		t.Fatalf("instant %s is past int64 nanoseconds", ns)
	}
	return n
}
