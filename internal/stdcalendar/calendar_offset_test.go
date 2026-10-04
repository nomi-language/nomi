package stdcalendar

import "github.com/nomi-language/nomi/rt"

import "testing"

// The rules rt/calendar_offset.go OWNS, asserted absolutely.
//
// These functions are the one implementation of these rules, so a comparison
// of two runs cannot see a bug in any of them. Everything here is therefore an
// ABSOLUTE assertion about a
// value, not a comparison between two implementations.

// TestOffsetDateTimeWallProjectsThroughTheOffsetNotUTC is the property every
// accessor rests on.
//
// The stored instant is UTC nanoseconds and the wall reading is that instant
// AS SEEN at the offset. 23:30 at -05:00 is 04:30 the NEXT DAY in UTC, so a
// projection that forgot the offset would answer a different day as well as a
// different hour — which is why the day is checked and not only the hour.
func TestOffsetDateTimeWallProjectsThroughTheOffsetNotUTC(t *testing.T) {
	res := ParseOffsetDateTime("2026-05-04T23:30:00-05:00")
	if res.Tag != rt.TagOk {
		t.Fatalf("parse failed: %+v", res.Err)
	}
	odt := res.Ok
	if odt.OffsetSeconds != -5*3600 {
		t.Fatalf("offset seconds = %d, want -18000", odt.OffsetSeconds)
	}
	wall := OffsetDateTimeWall(odt)
	if wall.Day != 4 || wall.Hour != 23 || wall.Minute != 30 {
		t.Errorf("wall = %d-%02d-%02dT%02d:%02d, want 2026-05-04T23:30 — a UTC projection "+
			"would answer the 5th at 04:30", wall.Year, wall.Month, wall.Day, wall.Hour, wall.Minute)
	}
	if got := OffsetDateTimeDay(odt); got != 4 {
		t.Errorf("day = %d, want 4", got)
	}
	if got := OffsetDateTimeHour(odt); got != 23 {
		t.Errorf("hour = %d, want 23", got)
	}
	// And the instant itself is offset-free: 2026-05-05T04:30:00Z.
	if got := odt.InstantNanos; got != 1777955400*int64(1e9) {
		t.Errorf("instant nanos = %d, want %d", got, 1777955400*int64(1e9))
	}
}

// TestOffsetDateTimeWithOffsetRejectsSubSecondAndOverLargeOffsets pins the two
// failure modes, both of which are the OFFSET's.
//
// The sub-second case is the one a reader is likeliest to "simplify" into a
// truncation, and truncating is a WRONG ANSWER rather than a lost error: the
// caller named an offset the type cannot hold, and answering about a different
// offset silently is worse than refusing.
func TestOffsetDateTimeWithOffsetRejectsSubSecondAndOverLargeOffsets(t *testing.T) {
	naive := rt.NaiveDateTime{Year: 2026, Month: 5, Day: 4, Hour: 9}
	sub := OffsetDateTimeWithOffset(naive, rt.Duration(500*int64(1e6)))
	if sub.Tag == rt.TagOk {
		t.Errorf("a 500ms offset was accepted as %+v; neither IANA nor a fixed Go zone carries "+
			"a sub-second offset", sub.Ok)
	} else if sub.Err.Tag != rt.TagInvalidValue {
		t.Errorf("sub-second offset error tag = %d, want rt.TagInvalidValue", sub.Err.Tag)
	}
	over := OffsetDateTimeWithOffset(naive, rt.Duration(19*3600*int64(1e9)))
	if over.Tag == rt.TagOk {
		t.Errorf("a +19h offset was accepted as %+v; the cap is ±18h", over.Ok)
	}
	// The boundary itself is INSIDE, so the check is `>` and not `>=`.
	edge := OffsetDateTimeWithOffset(naive, rt.Duration(MaxOffsetSeconds*int64(1e9)))
	if edge.Tag != rt.TagOk {
		t.Errorf("exactly ±18h was rejected (%+v); the cap is inclusive", edge.Err)
	}
	ok := OffsetDateTimeWithOffset(naive, rt.Duration(-5*3600*int64(1e9)))
	if ok.Tag != rt.TagOk {
		t.Fatalf("a whole-hour offset was rejected: %+v", ok.Err)
	}
	if got := OffsetDateTimeToString(ok.Ok); got != "2026-05-04T09:00:00-05:00" {
		t.Errorf("with_offset rendered %q, want 2026-05-04T09:00:00-05:00", got)
	}
}

// TestOffsetDateTimeFromInstantIsTotalWhereWithOffsetIsNot is the asymmetry
// std/calendar.nomi declares in its two return types, checked rather than
// assumed.
//
// `from_instant` names a MOMENT at an offset and every moment has one, so it
// truncates a sub-second offset instead of refusing. Reversing the two would
// make `from_instant` fallible for no reachable reason and `with_offset` silently
// wrong.
func TestOffsetDateTimeFromInstantIsTotalWhereWithOffsetIsNot(t *testing.T) {
	odt := OffsetDateTimeFromInstant(rt.Instant(0), rt.Duration(500*int64(1e6)))
	if odt.OffsetSeconds != 0 {
		t.Errorf("offset seconds = %d, want 0 (500ms truncates)", odt.OffsetSeconds)
	}
	if got := OffsetDateTimeToString(odt); got != "1970-01-01T00:00:00+00:00" {
		t.Errorf("epoch at +00:00 rendered %q", got)
	}
}

// TestOffsetDateTimeCivilShiftsClampAndKeepTheOffset covers the only two rungs
// that are not `+ Duration` in disguise.
//
// The leap-year pair is the discriminating one: a clamp to a constant 28 answers
// the same date for both years, and Go's `time.AddDate` used directly normalizes
// FORWARD into March instead.
func TestOffsetDateTimeCivilShiftsClampAndKeepTheOffset(t *testing.T) {
	parse := func(s string) rt.OffsetDateTime {
		t.Helper()
		res := ParseOffsetDateTime(s)
		if res.Tag != rt.TagOk {
			t.Fatalf("parse %q failed: %+v", s, res.Err)
		}
		return res.Ok
	}
	cases := []struct {
		name string
		got  rt.OffsetDateTime
		want string
	}{
		{"months clamp, common year",
			OffsetDateTimePlusMonths(parse("2026-01-31T09:15:00-05:00"), 1),
			"2026-02-28T09:15:00-05:00"},
		{"months clamp, leap year",
			OffsetDateTimePlusMonths(parse("2024-01-31T09:15:00-05:00"), 1),
			"2024-02-29T09:15:00-05:00"},
		{"months back floors rather than normalizing forward",
			OffsetDateTimePlusMonths(parse("2026-03-31T09:15:00-05:00"), -1),
			"2026-02-28T09:15:00-05:00"},
		{"years is twelve months, so Feb 29 clamps",
			OffsetDateTimePlusYears(parse("2024-02-29T09:15:00-05:00"), 1),
			"2025-02-28T09:15:00-05:00"},
		{"a non-UTC offset survives the shift",
			OffsetDateTimePlusMonths(parse("2026-01-31T09:15:00+05:30"), 1),
			"2026-02-28T09:15:00+05:30"},
	}
	for _, c := range cases {
		if got := OffsetDateTimeToString(c.got); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestOffsetDateTimeCivilAndPhysicalAgreeBelowAMonth is the fact that keeps this
// file short, so it is pinned rather than left as a comment.
//
// A fixed offset has no transitions, so shifting the WALL READING by a day and
// shifting the INSTANT by 24 hours land on the same value. That is why
// std/calendar.nomi writes the day, week and sub-day rungs as Nomi bodies over
// `Duration` and rt carries no symbol for them. The negative control is the
// zoned type across a spring-forward, where the same two operations differ by an
// hour — asserted here so the two claims sit together.
func TestOffsetDateTimeCivilAndPhysicalAgreeBelowAMonth(t *testing.T) {
	res := ParseOffsetDateTime("2026-03-07T12:00:00-05:00")
	if res.Tag != rt.TagOk {
		t.Fatalf("parse failed: %+v", res.Err)
	}
	odt := res.Ok
	civil := OffsetDateTimeWall(odt)
	civil.Day++
	physical := rt.OffsetDateTime{
		InstantNanos:  odt.InstantNanos + 24*3600*int64(1e9),
		OffsetSeconds: odt.OffsetSeconds,
	}
	shifted := OffsetDateTimeWithOffset(civil, rt.Duration(odt.OffsetSeconds*int64(1e9)))
	if shifted.Tag != rt.TagOk {
		t.Fatalf("re-anchoring the shifted wall reading failed: %+v", shifted.Err)
	}
	if shifted.Ok != physical {
		t.Errorf("civil %+v and physical %+v differ; a fixed offset has no transition for them "+
			"to differ over", shifted.Ok, physical)
	}
	// The zoned control: New York's spring-forward is 2026-03-08, so the same
	// pair diverges by an hour there.
	ny := rt.DateTime{InstantNanos: odt.InstantNanos, Zone: "America/New_York"}
	zonedCivil := DateTimePlusDays(ny, 1)
	zonedPhysical := rt.DateTime{InstantNanos: ny.InstantNanos + 24*3600*int64(1e9), Zone: ny.Zone}
	if zonedCivil.InstantNanos == zonedPhysical.InstantNanos {
		t.Errorf("zoned civil and physical agree across a spring-forward; they must differ by an " +
			"hour, or the control proves nothing about the rt.OffsetDateTime case above")
	}
}

// TestDateTimeToOffsetResolvesTheZoneAtThatInstant is the bridge's whole
// content: a zoned value carries a RULE and an offset value carries a NUMBER.
//
// One zone at two instants either side of a transition is the discriminating
// input — a bridge that read a constant offset would answer the same number
// twice.
func TestDateTimeToOffsetResolvesTheZoneAtThatInstant(t *testing.T) {
	winter := ParseZonedDateTime("2026-01-15T12:00:00-05:00[America/New_York]")
	summer := ParseZonedDateTime("2026-06-15T12:00:00-04:00[America/New_York]")
	if winter.Tag != rt.TagOk || summer.Tag != rt.TagOk {
		t.Fatalf("parse failed: %+v / %+v", winter.Err, summer.Err)
	}
	w, s := DateTimeToOffset(winter.Ok), DateTimeToOffset(summer.Ok)
	if w.OffsetSeconds != -5*3600 {
		t.Errorf("January offset = %ds, want -18000", w.OffsetSeconds)
	}
	if s.OffsetSeconds != -4*3600 {
		t.Errorf("June offset = %ds, want -14400", s.OffsetSeconds)
	}
	// Instant-preserving in both cases: the projection forgets the zone, not the
	// moment.
	if w.InstantNanos != winter.Ok.InstantNanos || s.InstantNanos != summer.Ok.InstantNanos {
		t.Error("to_offset changed the instant; it may only drop the zone")
	}
	// A stored zone that no longer loads yields +00:00 rather than faulting,
	// which is the convention every DateTime accessor follows. Unreachable
	// through the language — a DateTime only exists after a successful
	// construction — and pinned so the arm is a decision rather than an
	// accident.
	if got := DateTimeToOffset(rt.DateTime{InstantNanos: 0, Zone: "Not/A_Zone"}); got.OffsetSeconds != 0 {
		t.Errorf("an unloadable zone gave offset %ds, want 0", got.OffsetSeconds)
	}
}
