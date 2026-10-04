package stdcalendar

import "github.com/nomi-language/nomi/rt"

import (
	"testing"
	"time"
)

// Absolute assertions on the calendar core, and they are the PRIMARY guard
// rather than a supplement.
//
// These functions are the one implementation of the calendar rules, so every
// claim below spells out the expected answer rather than comparing two runs.
//
// The four properties are the ones a structural lowering gets silently wrong:
// instant-only identity, the civil/physical DST divergence, end-of-month
// clamping, and the Disambiguation modes.

func mustParseZoned(t *testing.T, s string) rt.DateTime {
	t.Helper()
	res := ParseZonedDateTime(s)
	if res.Tag != rt.TagOk {
		t.Fatalf("ParseZonedDateTime(%q) failed: %s", s, CalendarErrorText(res.Err))
	}
	return res.Ok
}

func mustParseNaive(t *testing.T, s string) rt.NaiveDateTime {
	t.Helper()
	res := ParseNaiveDateTime(s)
	if res.Tag != rt.TagOk {
		t.Fatalf("ParseNaiveDateTime(%q) failed: %s", s, CalendarErrorText(res.Err))
	}
	return res.Ok
}

// TestCalendarZoneIsNotIdentity is the two-zones-one-instant property, at the
// representation level.
//
// std/calendar.nomi hand-writes Equatable/Hashable/Comparable for DateTime over
// `instant_nanos` alone, so the same moment in two zones is ONE value as far as
// the language is concerned. This asserts both halves: the instants agree, and
// Go's own `==` on the struct DISAGREES — because that second half is what makes
// a structural lowering of `==` a wrong answer rather than a slower right one.
func TestCalendarZoneIsNotIdentity(t *testing.T) {
	ny := mustParseZoned(t, "2026-06-15T12:00:00-04:00[America/New_York]")
	tokyo := DateTimeWithZone(ny, "Asia/Tokyo")
	if tokyo.Tag != rt.TagOk {
		t.Fatalf("with_zone failed: %s", CalendarErrorText(tokyo.Err))
	}
	if ny.InstantNanos != tokyo.Ok.InstantNanos {
		t.Fatalf("re-zoning changed the instant: %d != %d", ny.InstantNanos, tokyo.Ok.InstantNanos)
	}
	if ny == tokyo.Ok {
		t.Fatal("Go's == answered true for two zones; the test's premise is gone " +
			"and a structural lowering would no longer be detectable here")
	}
	// The wall readings differ, which is the whole point of carrying the zone.
	if got := DateTimeToString(ny); got != "2026-06-15T12:00:00-04:00[America/New_York]" {
		t.Fatalf("New York renders %q", got)
	}
	if got := DateTimeToString(tokyo.Ok); got != "2026-06-16T01:00:00+09:00[Asia/Tokyo]" {
		t.Fatalf("Tokyo renders %q", got)
	}
	if got := DateTimeDay(tokyo.Ok); got != 16 {
		t.Fatalf("Tokyo day is %d, want 16", got)
	}
	if got := DateTimeHour(tokyo.Ok); got != 1 {
		t.Fatalf("Tokyo hour is %d, want 1", got)
	}
}

// TestCalendarCivilAndPhysicalDivergeAcrossSpringForward pins the headline
// property: civil and physical arithmetic land on DIFFERENT wall times across a
// DST transition, deliberately.
//
// America/New_York springs forward at 2am on 2026-03-08. From noon on Mar 7:
//   - civil `+ Days(1)` keeps the WALL reading and so elapses 23 hours;
//   - physical `+ 24h` keeps the ELAPSED time and so lands at 1pm.
//
// Both numbers are asserted, and so is the 23-hour gap between the instants,
// because an implementation that quietly made civil arithmetic physical would
// still pass a test that only checked one of them.
func TestCalendarCivilAndPhysicalDivergeAcrossSpringForward(t *testing.T) {
	start := mustParseZoned(t, "2026-03-07T12:00:00-05:00[America/New_York]")

	civil := DateTimeCivilShift(start, 0, 0, 1, 0, 0, 0, 0)
	if civil.Tag != rt.TagOk {
		t.Fatalf("civil shift failed: %s", CalendarErrorText(civil.Err))
	}
	if got := DateTimeToString(civil.Ok); got != "2026-03-08T12:00:00-04:00[America/New_York]" {
		t.Fatalf("civil + 1 day renders %q, want noon EDT on Mar 8", got)
	}

	physical := rt.DateTime{InstantNanos: start.InstantNanos + 24*int64(time.Hour), Zone: start.Zone}
	if got := DateTimeToString(physical); got != "2026-03-08T13:00:00-04:00[America/New_York]" {
		t.Fatalf("physical + 24h renders %q, want 1pm EDT on Mar 8", got)
	}

	elapsed := civil.Ok.InstantNanos - start.InstantNanos
	if elapsed != 23*int64(time.Hour) {
		t.Fatalf("civil + 1 day elapsed %v, want 23h", time.Duration(elapsed))
	}
	if civil.Ok.InstantNanos == physical.InstantNanos {
		t.Fatal("civil and physical arithmetic agreed across the gap; the divergence " +
			"std/calendar exists to express is gone")
	}
}

// TestCalendarEndOfMonthClamping pins the clamp, including the leap-year case
// and the negative direction.
//
// Each row is an absolute answer. A clamp that overflowed into the next month
// instead (Go's `time.Date` normalization, which is what you get by forgetting
// to clamp at all) fails every row but the last two.
func TestCalendarEndOfMonthClamping(t *testing.T) {
	cases := []struct {
		from   string
		months int64
		want   string
	}{
		// The canonical case: no 31st of February.
		{"2011-01-31T00:00:00", 1, "2011-02-28T00:00:00"},
		// Leap year: the clamp target is a day longer, and it follows from
		// DaysInMonth rather than from a second rule.
		{"2012-01-31T00:00:00", 1, "2012-02-29T00:00:00"},
		// Backwards clamps too, and the month index has to floor rather than
		// truncate for this to land in February at all.
		{"2011-03-31T00:00:00", -1, "2011-02-28T00:00:00"},
		// A year is twelve months, so the leap day clamps the same way.
		{"2012-02-29T00:00:00", 12, "2013-02-28T00:00:00"},
		// Crossing a year boundary in each direction.
		{"2011-12-31T06:30:00", 2, "2012-02-29T06:30:00"},
		{"2011-01-31T06:30:00", -1, "2010-12-31T06:30:00"},
		// No clamp needed: the day survives and the time of day is untouched.
		{"2011-01-15T06:30:45", 1, "2011-02-15T06:30:45"},
	}
	for _, c := range cases {
		got := ISONaiveText(PlusMonthsClamped(mustParseNaive(t, c.from), c.months))
		if got != c.want {
			t.Errorf("%s + %d months = %s, want %s", c.from, c.months, got, c.want)
		}
	}
}

// TestCalendarClampBeforeDayShift pins CivilShift's ORDER, which is the part
// two independently-correct steps can get wrong between them.
//
// Most orderings agree, and that is the trap: `Jan 31 + 1 month + 1 day` is
// Mar 1 either way (clamp to Feb 28 then +1, or Feb 1 then +1 month). The
// discriminator is a NEGATIVE day shift, where clamping first loses a day the
// clamp itself removed:
//
//	clamp-first: Jan 31 -> Feb 28 -> Feb 27
//	shift-first: Jan 30 -> Feb 28
//
// CivilShift documents clamp-first, so Feb 27 is the contract. Nothing in the
// language reaches a multi-unit shift today — each `+ Months(n)` is its own
// call — so this pins CivilShift's own contract rather than a Nomi semantics
// claim, and it is the guard that would catch the order being swapped when
// something does reach it.
func TestCalendarClampBeforeDayShift(t *testing.T) {
	if got := ISONaiveText(CivilShift(mustParseNaive(t, "2011-01-31T00:00:00"), 0, 1, -1, 0, 0, 0, 0)); got != "2011-02-27T00:00:00" {
		t.Errorf("Jan 31 + 1 month - 1 day = %s, want 2011-02-27T00:00:00 (clamp before the day shift)", got)
	}
	// The agreeing direction, kept so a rewrite that broke the ordinary case
	// fails here rather than only on the discriminator.
	if got := ISONaiveText(CivilShift(mustParseNaive(t, "2011-01-31T00:00:00"), 0, 1, 1, 0, 0, 0, 0)); got != "2011-03-01T00:00:00" {
		t.Errorf("Jan 31 + 1 month + 1 day = %s, want 2011-03-01T00:00:00", got)
	}
	// Sub-day units carry through `time.Date` normalization, and the time of
	// day is preserved by the clamp.
	if got := ISONaiveText(CivilShift(mustParseNaive(t, "2011-01-31T23:30:00"), 0, 1, 0, 1, 0, 0, 0)); got != "2011-03-01T00:30:00" {
		t.Errorf("Jan 31 23:30 + 1 month + 1 hour = %s, want 2011-03-01T00:30:00", got)
	}
}

// TestCalendarDisambiguationModes pins all four modes on both a gap and a fold.
//
// America/New_York 2026: spring forward 2am -> 3am on Mar 8 (2:30am does not
// exist), fall back 2am -> 1am on Nov 1 (1:30am happens twice).
func TestCalendarDisambiguationModes(t *testing.T) {
	gap := mustParseNaive(t, "2026-03-08T02:30:00")
	fold := mustParseNaive(t, "2026-11-01T01:30:00")
	const zone = "America/New_York"

	// A gap: Compatible and Later take the post-transition instant, Earlier the
	// pre-transition one, Reject refuses.
	for _, c := range []struct {
		mode uint8
		want string
	}{
		{rt.TagCompatible, "2026-03-08T03:30:00-04:00[America/New_York]"},
		{rt.TagLater, "2026-03-08T03:30:00-04:00[America/New_York]"},
		{rt.TagEarlier, "2026-03-08T01:30:00-05:00[America/New_York]"},
	} {
		res := ResolveInZone(gap, zone, rt.Disambiguation{Tag: c.mode})
		if res.Tag != rt.TagOk {
			t.Fatalf("gap mode %d refused: %s", c.mode, CalendarErrorText(res.Err))
		}
		if got := DateTimeToString(res.Ok); got != c.want {
			t.Errorf("gap mode %d resolved to %s, want %s", c.mode, got, c.want)
		}
	}
	rejectGap := ResolveInZone(gap, zone, rt.Disambiguation{Tag: rt.TagReject})
	if rejectGap.Tag != rt.TagErr || rejectGap.Err.Tag != rt.TagNonexistent {
		t.Fatalf("Reject on a gap answered tag=%d err=%d, want rt.Err(Nonexistent)", rejectGap.Tag, rejectGap.Err.Tag)
	}
	if got := rejectGap.Err.Msg; got != "2026-03-08T02:30:00 does not exist in America/New_York (DST gap)" {
		t.Errorf("Nonexistent message is %q", got)
	}

	// A fold: Compatible and Earlier take the first (EDT, -04:00) instant,
	// Later the second (EST, -05:00), Reject refuses. Both render the same WALL
	// reading, so the offset is the only thing that distinguishes them — which
	// is exactly why a formatted-string round trip cannot catch a fold bug.
	for _, c := range []struct {
		mode uint8
		want string
	}{
		{rt.TagCompatible, "2026-11-01T01:30:00-04:00[America/New_York]"},
		{rt.TagEarlier, "2026-11-01T01:30:00-04:00[America/New_York]"},
		{rt.TagLater, "2026-11-01T01:30:00-05:00[America/New_York]"},
	} {
		res := ResolveInZone(fold, zone, rt.Disambiguation{Tag: c.mode})
		if res.Tag != rt.TagOk {
			t.Fatalf("fold mode %d refused: %s", c.mode, CalendarErrorText(res.Err))
		}
		if got := DateTimeToString(res.Ok); got != c.want {
			t.Errorf("fold mode %d resolved to %s, want %s", c.mode, got, c.want)
		}
	}
	rejectFold := ResolveInZone(fold, zone, rt.Disambiguation{Tag: rt.TagReject})
	if rejectFold.Tag != rt.TagErr || rejectFold.Err.Tag != rt.TagAmbiguous {
		t.Fatalf("Reject on a fold answered tag=%d err=%d, want rt.Err(Ambiguous)", rejectFold.Tag, rejectFold.Err.Tag)
	}
	// Earlier and Later must be one hour apart in real time, which the wall
	// readings above cannot show.
	earlier := ResolveInZone(fold, zone, rt.Disambiguation{Tag: rt.TagEarlier})
	later := ResolveInZone(fold, zone, rt.Disambiguation{Tag: rt.TagLater})
	if d := later.Ok.InstantNanos - earlier.Ok.InstantNanos; d != int64(time.Hour) {
		t.Errorf("fold Earlier and Later are %v apart, want 1h", time.Duration(d))
	}
}

// TestCalendarUnknownZoneIsAnError pins that an unknown IANA name is a Nomi
// error variant rather than a Go error or a panic, on every entry point that
// takes a zone.
func TestCalendarUnknownZoneIsAnError(t *testing.T) {
	ny := mustParseZoned(t, "2026-06-15T12:00:00-04:00[America/New_York]")
	if res := DateTimeWithZone(ny, "Not/A_Zone"); res.Tag != rt.TagErr || res.Err.Tag != rt.TagUnknownZone {
		t.Errorf("with_zone on an unknown zone answered tag=%d err=%d", res.Tag, res.Err.Tag)
	}
	if res := DateTimeFromInstantIn(rt.Instant(0), "Mars/Base"); res.Tag != rt.TagErr || res.Err.Tag != rt.TagUnknownZone {
		t.Errorf("from_instant_in on an unknown zone answered tag=%d err=%d", res.Tag, res.Err.Tag)
	}
	if res := ResolveInZone(mustParseNaive(t, "2026-06-15T12:00:00"), "Mars/Base", rt.Disambiguation{Tag: rt.TagCompatible}); res.Tag != rt.TagErr || res.Err.Tag != rt.TagUnknownZone {
		t.Errorf("in_zone on an unknown zone answered tag=%d err=%d", res.Tag, res.Err.Tag)
	}
	if res := ParseZonedDateTime("2026-06-15T12:00:00-04:00[Mars/Base]"); res.Tag != rt.TagErr || res.Err.Tag != rt.TagUnknownZone {
		t.Errorf("parse with an unknown zone answered tag=%d err=%d", res.Tag, res.Err.Tag)
	}
}

// TestCalendarParseRejectsTheBareOffsetForm pins the RFC 9557 requirement: a
// bracket suffix is mandatory for a DateTime, because a bare offset names no
// zone and inventing one would be a silent wrong answer.
func TestCalendarParseRejectsTheBareOffsetForm(t *testing.T) {
	res := ParseZonedDateTime("2026-06-15T12:00:00-04:00")
	if res.Tag != rt.TagErr || res.Err.Tag != rt.TagInvalidFormat {
		t.Fatalf("bare-offset parse answered tag=%d err=%d, want rt.Err(InvalidFormat)", res.Tag, res.Err.Tag)
	}
}

// TestCalendarTzdataIsEmbedded is the guarantee the blank import exists for:
// zone lookup must not depend on a host tzdb, and a dependence there is
// invisible on any developer machine.
//
// Asserted by loading a zone whose RULES are checked, not merely its name: a
// stub that answered a UTC location for every name would pass a
// does-this-name-load test.
func TestCalendarTzdataIsEmbedded(t *testing.T) {
	loc, _, ok := LoadZone("Australia/Lord_Howe")
	if !ok {
		t.Fatal("Australia/Lord_Howe did not load; the embedded tzdb is missing")
	}
	// Lord Howe's DST shift is 30 minutes, which no fallback could invent.
	_, jan := time.Date(2026, 1, 15, 12, 0, 0, 0, loc).Zone()
	_, jul := time.Date(2026, 7, 15, 12, 0, 0, 0, loc).Zone()
	if jan-jul != 1800 {
		t.Fatalf("Lord Howe summer/winter offsets differ by %ds, want 1800", jan-jul)
	}
}

// TestCalendarDaysInMonth pins the month lengths the clamp depends on,
// including all three leap-year rules.
func TestCalendarDaysInMonth(t *testing.T) {
	cases := []struct {
		year, month, want int64
	}{
		{2011, 2, 28}, {2012, 2, 29}, // ordinary and divisible-by-4
		{1900, 2, 28}, {2000, 2, 29}, // century, and divisible-by-400
		{2026, 1, 31}, {2026, 4, 30}, {2026, 12, 31},
	}
	for _, c := range cases {
		if got := DaysInMonth(c.year, c.month); got != c.want {
			t.Errorf("DaysInMonth(%d, %d) = %d, want %d", c.year, c.month, got, c.want)
		}
	}
}

// TestCalendarNaiveRoundTripAndBetween pins the naive surface: the ISO
// rendering, the fractional-second forms, and the UTC-reading difference.
func TestCalendarNaiveRoundTripAndBetween(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"2026-05-04T14:30:00", "2026-05-04T14:30:00"},
		{"2026-05-04 14:30:00", "2026-05-04T14:30:00"},
		{"2026-05-04T14:30:00.5", "2026-05-04T14:30:00.5"},
		{"2026-05-04T14:30:00.123456789", "2026-05-04T14:30:00.123456789"},
		// Past nine digits truncates rather than rounding.
		{"2026-05-04T14:30:00.1234567890", "2026-05-04T14:30:00.123456789"},
	} {
		if got := NaiveDateTimeToString(mustParseNaive(t, c.in)); got != c.want {
			t.Errorf("naive %q renders %q, want %q", c.in, got, c.want)
		}
	}
	if got := NaiveDateTimeBetween(mustParseNaive(t, "2026-05-04T09:00:00"), mustParseNaive(t, "2026-05-04T11:30:00")); got != rt.Duration(150*int64(time.Minute)) {
		t.Errorf("between = %d nanos, want 150 minutes", int64(got))
	}
	// An impossible date is a value error, not a format error.
	res := ParseNaiveDateTime("2026-02-30T12:00:00")
	if res.Tag != rt.TagErr || res.Err.Tag != rt.TagInvalidValue {
		t.Errorf("Feb 30 answered tag=%d err=%d, want rt.Err(InvalidValue)", res.Tag, res.Err.Tag)
	}
}

// TestCalendarNaivePlusDurationIsExactNotCivil pins the `+ Duration` rung,
// and the rows are chosen to fail if it were ever wired to civil arithmetic.
//
// `+ Duration` and `+ Months(1)` are different operations that agree on most
// inputs, so a month END is the only place the difference shows: January 31st
// plus 24 hours is February 1st, where a civil `+ 1 month` clamps to February
// 28th. The leap pair then pins the month LENGTH, which comes from time.Date's
// normalization rather than from a table this file wrote.
//
// This is where that rule is checked absolutely. The Nomi-level fixture
// (internal/irbuild/testdata/stdkey_overloads.nomi) is checked against its
// golden record, which was written by running THIS function.
func TestCalendarNaivePlusDurationIsExactNotCivil(t *testing.T) {
	for _, c := range []struct {
		from string
		add  time.Duration
		want string
	}{
		// Exact, not civil: past the end of a 31-day month rather than clamped
		// back into it.
		{"2011-01-31T12:00:00", 24 * time.Hour, "2011-02-01T12:00:00"},
		// The month length, in both directions of the leap rule.
		{"2012-02-28T23:00:00", 2 * time.Hour, "2012-02-29T01:00:00"},
		{"2011-02-28T23:00:00", 2 * time.Hour, "2011-03-01T01:00:00"},
		// Backwards borrows across the month boundary.
		{"2011-01-31T12:00:00", -24 * time.Hour, "2011-01-30T12:00:00"},
		{"2011-03-01T00:00:00", -time.Nanosecond, "2011-02-28T23:59:59.999999999"},
		// Sub-second components survive, which a whole-second implementation
		// would silently drop.
		{"2026-05-04T14:30:00.25", 500 * time.Millisecond, "2026-05-04T14:30:00.75"},
		// Zero is the identity rather than a normalization.
		{"2026-05-04T14:30:00.123456789", 0, "2026-05-04T14:30:00.123456789"},
	} {
		got := ISONaiveText(NaiveDateTimePlusDuration(mustParseNaive(t, c.from), rt.Duration(c.add)))
		if got != c.want {
			t.Errorf("%s + %v = %s, want %s", c.from, c.add, got, c.want)
		}
	}
	// And the inverse closes: between(t, t+d) is d.
	base := mustParseNaive(t, "2011-01-31T12:00:00")
	d := rt.Duration(37*int64(time.Hour) + 12345)
	if got := NaiveDateTimeBetween(base, NaiveDateTimePlusDuration(base, d)); got != d {
		t.Errorf("between(t, t+d) = %d nanos, want %d", int64(got), int64(d))
	}
}

// TestCalendarZeroValuesAreDetectablyInvalid pins the tag-0 reservation for
// both enums rt declares here, which is the enum-representation decision rather
// than a new one.
func TestCalendarZeroValuesAreDetectablyInvalid(t *testing.T) {
	var e rt.CalendarError
	if e.Tag != rt.TagInvalid {
		t.Fatalf("a zero rt.CalendarError has tag %d", e.Tag)
	}
	if got := CalendarErrorText(e); got != "invalid calendar error" {
		t.Fatalf("a never-constructed error renders %q; it must not read as a real variant", got)
	}
	var d rt.Disambiguation
	if d.Tag != rt.TagInvalid {
		t.Fatalf("a zero rt.Disambiguation has tag %d", d.Tag)
	}
	// A zero Disambiguation must NOT silently behave as Compatible on a value
	// that would otherwise refuse: the `default` arms in ResolveInZone are
	// reached by tag 0 too, so this records the choice rather than asserting a
	// guard that does not exist.
	res := ResolveInZone(mustParseNaive(t, "2026-03-08T02:30:00"), "America/New_York", d)
	if res.Tag != rt.TagOk {
		t.Fatalf("a zero rt.Disambiguation refused; ResolveInZone's default arms should resolve")
	}
}
