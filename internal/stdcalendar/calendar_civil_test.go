package stdcalendar

import "github.com/nomi-language/nomi/rt"

import "testing"

// Absolute assertions for `Date` and `Time`.
//
// calendar.go is the one implementation of these rules, so the only useful
// check is an absolute one. Everything here asserts a VALUE or an IDENTITY, never an agreement.

// TestDateAndTimeAreDistinctFromEachOtherAndFromNaive is the layout claim, and
// it is the one that makes three structs correct rather than wasteful.
//
// std declares `Date`, `Time` and `NaiveDateTime` separately, each with its own
// `derive Equatable`/`Hashable`/`Comparable` — so the comparison is generated
// over the fields that exist. One struct with unused fields would make
// `Date"2026-05-04"` equal to `NaiveDateTime"2026-05-04T00:00:00"`, which is a
// WRONG ANSWER rather than a wasted word, and Go's type system is what forbids
// it. This test states the property that the type checker is enforcing, so a
// later "simplification" to a shared struct fails here with the reason attached
// rather than compiling and quietly widening equality.
func TestDateAndTimeAreDistinctFromEachOtherAndFromNaive(t *testing.T) {
	d := rt.Date{Year: 2026, Month: 5, Day: 4}
	mid := NaiveDateTimeToDate(rt.NaiveDateTime{Year: 2026, Month: 5, Day: 4})
	if d != mid {
		t.Errorf("two Dates naming one day differ: %+v vs %+v", d, mid)
	}
	// A Date carries no time-of-day and a Time no date, so neither projection
	// can round-trip through the other. Asserted as a value claim because the
	// field sets are what `derive` compares.
	tm := NaiveDateTimeToTime(rt.NaiveDateTime{Year: 2026, Month: 5, Day: 4, Hour: 14, Minute: 30})
	if want := (rt.Time{Hour: 14, Minute: 30}); tm != want {
		t.Errorf("to_time = %+v, want %+v", tm, want)
	}
	if got := NaiveDateTimeToDate(rt.NaiveDateTime{Year: 2026, Month: 5, Day: 4, Hour: 14, Minute: 30}); got != d {
		t.Errorf("to_date kept a time component: %+v", got)
	}
}

// TestTimeWrapsWhereNaiveDateTimeRollsOver is the divergence pair for these two
// types, and BOTH ANSWERS ARE RIGHT.
//
// `Time"23:30" + Duration.hours(2)` is `01:30` — std documents `impl
// Add<Duration, Time>` as wrapping around midnight, and a time-of-day has no
// day to carry into. `NaiveDateTime"…T23:30" + Duration.hours(2)` is the NEXT
// DAY at `01:30`, because a wall reading does carry a date. Same operator, same
// operand, same elapsed nanoseconds, two different and equally correct results,
// decided entirely by which type the receiver is.
//
// This is the `Date`/`Time` rung of the argument calendar_test.go makes for
// civil-versus-physical arithmetic across a DST transition, and it is the reason
// the wrap is a rule rather than an implementation detail: a `Time` that
// saturated at `23:59:59.999999999`, or one that silently became a
// NaiveDateTime, would each pass an "advance by two hours" test that only
// checked one type.
func TestTimeWrapsWhereNaiveDateTimeRollsOver(t *testing.T) {
	const twoHours = rt.Duration(2 * 3_600_000_000_000)

	tod := TimePlusDuration(rt.Time{Hour: 23, Minute: 30}, twoHours)
	if want := (rt.Time{Hour: 1, Minute: 30}); tod != want {
		t.Errorf("rt.Time 23:30 + 2h = %+v, want %+v (a time-of-day wraps)", tod, want)
	}

	wall := NaiveDateTimePlusDuration(
		rt.NaiveDateTime{Year: 2026, Month: 5, Day: 4, Hour: 23, Minute: 30}, twoHours)
	want := rt.NaiveDateTime{Year: 2026, Month: 5, Day: 5, Hour: 1, Minute: 30}
	if wall != want {
		t.Errorf("rt.NaiveDateTime 2026-05-04T23:30 + 2h = %+v, want %+v (a wall reading rolls over)", wall, want)
	}

	// The negative control: the time-of-day halves agree, so the divergence is
	// the DATE and not the clock. Without this the test above would also pass
	// if the wrap were broken in a way that happened to land on 01:30.
	if got := NaiveDateTimeToTime(wall); got != tod {
		t.Errorf("the two paths disagree on the time of day: %+v vs %+v", got, tod)
	}

	// BACKWARDS, and this block exists because a mutation escaped without it.
	// Deleting the `if total < 0` correction in TimePlusDuration left THIS
	// PACKAGE'S ENTIRE SUITE GREEN — the irbuild fixture's `tod_wraps_back` row
	// caught it, and this package's own pins did not,
	// because every row above advances FORWARD.
	//
	// Go's `%` keeps the sign of the DIVIDEND, so without the correction
	// `Time"00:30" + (-1h)` answers a negative hour rather than `23:30`. That is
	// the one arithmetic fact in this file that a forward-only test cannot see,
	// and rt is where it belongs: the rule lives here, so the guard for it
	// should not have to be in another module.
	for _, c := range []struct {
		name  string
		start rt.Time
		delta rt.Duration
		want  rt.Time
	}{
		{"back across midnight", rt.Time{Minute: 30}, -rt.Duration(3_600_000_000_000), rt.Time{Hour: 23, Minute: 30}},
		{"minus a whole day is identity", rt.Time{Hour: 9, Minute: 30}, -rt.Duration(86_400_000_000_000), rt.Time{Hour: 9, Minute: 30}},
		{"minus a nanosecond from midnight", rt.Time{}, -1, rt.Time{Hour: 23, Minute: 59, Second: 59, Nanosecond: 999_999_999}},
		{"more than a day back reduces", rt.Time{Hour: 9, Minute: 30}, -rt.Duration(90_000_000_000_000), rt.Time{Hour: 8, Minute: 30}},
	} {
		if got := TimePlusDuration(c.start, c.delta); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestDateCivilRungsClampThroughOneImplementation pins end-of-month clamping on
// `Date`, and pins that it is PlusMonthsClamped's clamp rather than a second
// one.
//
// The three rungs are namings of CivilShift, so a divergence here would mean
// somebody re-derived the arithmetic. The leap pair is the discriminating case:
// `2011-01-31 + Months(1)` and `2012-01-31 + Months(1)` differ only in whether
// the target February has 29 days, and an implementation that clamped to a
// constant 28 passes every other row.
func TestDateCivilRungsClampThroughOneImplementation(t *testing.T) {
	for _, c := range []struct {
		name string
		got  rt.Date
		want rt.Date
	}{
		{"month clamp into a common February",
			DatePlusMonths(rt.Date{Year: 2011, Month: 1, Day: 31}, 1), rt.Date{Year: 2011, Month: 2, Day: 28}},
		{"month clamp into a leap February",
			DatePlusMonths(rt.Date{Year: 2012, Month: 1, Day: 31}, 1), rt.Date{Year: 2012, Month: 2, Day: 29}},
		{"month clamp into a 30-day month",
			DatePlusMonths(rt.Date{Year: 2026, Month: 3, Day: 31}, 1), rt.Date{Year: 2026, Month: 4, Day: 30}},
		{"negative months clamp too",
			DatePlusMonths(rt.Date{Year: 2026, Month: 1, Day: 31}, -1), rt.Date{Year: 2025, Month: 12, Day: 31}},
		{"year off a leap day clamps",
			DatePlusYears(rt.Date{Year: 2024, Month: 2, Day: 29}, 1), rt.Date{Year: 2025, Month: 2, Day: 28}},
		{"year onto a leap day does not",
			DatePlusYears(rt.Date{Year: 2024, Month: 2, Day: 29}, 4), rt.Date{Year: 2028, Month: 2, Day: 29}},
		{"days cross a year boundary",
			DatePlusDays(rt.Date{Year: 2026, Month: 12, Day: 30}, 5), rt.Date{Year: 2027, Month: 1, Day: 4}},
		{"negative days cross backwards",
			DatePlusDays(rt.Date{Year: 2026, Month: 1, Day: 1}, -1), rt.Date{Year: 2025, Month: 12, Day: 31}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %+v, want %+v", c.got, c.want)
			}
		})
	}
}

// TestParseDateAndTimeFaultText pins the parse fault messages.
//
// They are user-visible through `Err(Error.InvalidFormat(msg))`, so a drift is a
// wrong answer rather than a cosmetic one.
//
// The ellipsis in the Time message is U+2026 and is quoted literally on purpose.
func TestParseDateAndTimeFaultText(t *testing.T) {
	for _, c := range []struct {
		name string
		res  rt.Result[rt.Date, rt.CalendarError]
		tag  uint8
		msg  string
	}{
		{"shape", ParseDate("05/04/2026"), rt.TagInvalidFormat, `expected YYYY-MM-DD, got "05/04/2026"`},
		{"non-numeric", ParseDate("2026-ab-04"), rt.TagInvalidFormat, `non-numeric component in "2026-ab-04"`},
		{"negative year is a shape fault", ParseDate("-2026-05-04"), rt.TagInvalidFormat, `expected YYYY-MM-DD, got "-2026-05-04"`},
		{"impossible day", ParseDate("2026-02-29"), rt.TagInvalidValue, "day 29 not valid for 2026-02"},
		{"month out of range", ParseDate("2026-13-04"), rt.TagInvalidValue, "month out of range: 13"},
	} {
		t.Run("date "+c.name, func(t *testing.T) {
			if c.res.Tag != rt.TagErr {
				t.Fatalf("parsed successfully: %+v", c.res.Ok)
			}
			if c.res.Err.Tag != c.tag || c.res.Err.Msg != c.msg {
				t.Errorf("got tag %d %q, want tag %d %q", c.res.Err.Tag, c.res.Err.Msg, c.tag, c.msg)
			}
		})
	}
	for _, c := range []struct {
		name string
		res  rt.Result[rt.Time, rt.CalendarError]
		tag  uint8
		msg  string
	}{
		{"shape", ParseTime("nope"), rt.TagInvalidFormat, `expected HH:MM:SS[.fff…], got "nope"`},
		{"hour out of range", ParseTime("24:00:00"), rt.TagInvalidValue, "hour out of range: 24"},
		{"minute out of range", ParseTime("12:60:00"), rt.TagInvalidValue, "minute out of range: 60"},
	} {
		t.Run("time "+c.name, func(t *testing.T) {
			if c.res.Tag != rt.TagErr {
				t.Fatalf("parsed successfully: %+v", c.res.Ok)
			}
			if c.res.Err.Tag != c.tag || c.res.Err.Msg != c.msg {
				t.Errorf("got tag %d %q, want tag %d %q", c.res.Err.Tag, c.res.Err.Msg, c.tag, c.msg)
			}
		})
	}
}

// TestDateAndTimeRoundTripThroughTheirRenderings pins that parsing and rendering
// are inverse, which is what makes the `Date"…"` / `Time"…"` literal and
// `Display.to_string` one pair rather than two.
//
// The sub-second row is the discriminating one: `ISOTimeText` renders a fraction
// only when nonzero, so `09:30:00` and `23:45:12.5` exercise both arms and a
// renderer that always emitted `.000` would fail the first while round-tripping.
func TestDateAndTimeRoundTripThroughTheirRenderings(t *testing.T) {
	for _, s := range []string{"2026-05-04", "2024-02-29", "0001-01-01"} {
		res := ParseDate(s)
		if res.Tag != rt.TagOk {
			t.Fatalf("ParseDate(%q) failed: %s", s, res.Err.Msg)
		}
		if got := DateToString(res.Ok); got != s {
			t.Errorf("rt.Date %q rendered as %q", s, got)
		}
	}
	for _, s := range []string{"09:30:00", "23:45:12.5", "00:00:00", "23:59:59.999999999"} {
		res := ParseTime(s)
		if res.Tag != rt.TagOk {
			t.Fatalf("ParseTime(%q) failed: %s", s, res.Err.Msg)
		}
		if got := TimeToString(res.Ok); got != s {
			t.Errorf("rt.Time %q rendered as %q", s, got)
		}
	}
}

// TestDateDaysBetweenIsSigned pins the day count in both directions. The leap
// row is the one a reimplementation gets wrong.
func TestDateDaysBetweenIsSigned(t *testing.T) {
	for _, c := range []struct {
		name           string
		earlier, later rt.Date
		want           int64
	}{
		{"same day", rt.Date{Year: 2026, Month: 5, Day: 4}, rt.Date{Year: 2026, Month: 5, Day: 4}, 0},
		{"forward", rt.Date{Year: 2026, Month: 5, Day: 4}, rt.Date{Year: 2026, Month: 5, Day: 7}, 3},
		{"backward", rt.Date{Year: 2026, Month: 5, Day: 7}, rt.Date{Year: 2026, Month: 5, Day: 4}, -3},
		{"common year", rt.Date{Year: 2026, Month: 1, Day: 1}, rt.Date{Year: 2026, Month: 12, Day: 31}, 364},
		{"leap year counts 366", rt.Date{Year: 2024, Month: 1, Day: 1}, rt.Date{Year: 2025, Month: 1, Day: 1}, 366},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := DateDaysBetween(c.earlier, c.later); got != c.want {
				t.Errorf("DateDaysBetween = %d, want %d", got, c.want)
			}
		})
	}
}
