package stdcalendar

import "github.com/nomi-language/nomi/rt"

import "testing"

// Each civil rung must move ITS OWN component and nothing else.
//
// The rungs are one-line wrappers over CivilShift and DateTimeCivilShift, whose
// rules are pinned in calendar_test.go. What is NOT pinned there is the WIRING:
// CivilShift takes seven positional int64s, and a wrapper that passed its
// operand into the neighbouring slot would still have the right operand TYPE,
// still compile, and still be accepted by every guard that reads signatures —
// internal/irbuild's TestCalendarUnitsAreNominallyDistinctAtTheGoLevel included,
// because that one compares `rt.Minutes` against the key's `Minutes` and cannot
// see which argument the value reaches.
//
// So the table is one row per rung with a UNIQUE expected delta, applied to an
// anchor whose every component is far from a boundary so a carry cannot make
// two rows agree by accident.
func TestCalendarUnitRungsMoveTheirOwnComponent(t *testing.T) {
	anchor := rt.NaiveDateTime{Year: 2021, Month: 6, Day: 15, Hour: 10, Minute: 20, Second: 30, Nanosecond: 400_000_000}
	zone := "America/New_York"
	zoned := ResolveInZone(anchor, zone, rt.Disambiguation{Tag: rt.TagCompatible})
	if zoned.Tag != rt.TagOk {
		t.Fatalf("the anchor does not resolve in %s, so this table tests nothing", zone)
	}

	naive := []struct {
		name string
		got  rt.NaiveDateTime
		want rt.NaiveDateTime
	}{
		{"rt.Years", NaiveDateTimePlusYears(anchor, 3), withYMD(anchor, 2024, 6, 15)},
		{"rt.Months", NaiveDateTimePlusMonths(anchor, 3), withYMD(anchor, 2021, 9, 15)},
		{"rt.Days", NaiveDateTimePlusDays(anchor, 3), withYMD(anchor, 2021, 6, 18)},
	}
	for _, c := range naive {
		if c.got != c.want {
			t.Errorf("NaiveDateTimePlus%s: got %+v, want %+v — the operand reached the wrong component",
				c.name, c.got, c.want)
		}
	}

	// The zoned rungs are compared by INSTANT, which is what a DateTime is.
	// June is inside DST for New York in both directions here, so a civil shift
	// of n units is exactly n units of elapsed time and the expected instant is
	// computable without consulting the zone twice.
	const (
		sec  = int64(1_000_000_000)
		hour = 3600 * sec
	)
	base := zoned.Ok.InstantNanos
	zonedRows := []struct {
		name string
		got  rt.DateTime
		want int64
	}{
		{"rt.Hours", DateTimePlusHours(zoned.Ok, 5), base + 5*hour},
		{"rt.Minutes", DateTimePlusMinutes(zoned.Ok, 5), base + 5*60*sec},
		{"rt.Seconds", DateTimePlusSeconds(zoned.Ok, 5), base + 5*sec},
		{"rt.Milliseconds", DateTimePlusMilliseconds(zoned.Ok, 5), base + 5*1_000_000},
		{"rt.Microseconds", DateTimePlusMicroseconds(zoned.Ok, 5), base + 5*1_000},
		{"rt.Nanoseconds", DateTimePlusNanoseconds(zoned.Ok, 5), base + 5},
		{"rt.Days", DateTimePlusDays(zoned.Ok, 3), base + 3*24*hour},
	}
	for _, c := range zonedRows {
		if c.got.InstantNanos != c.want {
			t.Errorf("DateTimePlus%s: instant %d, want %d (delta %d, want %d)",
				c.name, c.got.InstantNanos, c.want, c.got.InstantNanos-base, c.want-base)
		}
		if c.got.Zone != zone {
			t.Errorf("DateTimePlus%s: zone became %q, want %q — a civil shift re-resolves IN the zone",
				c.name, c.got.Zone, zone)
		}
	}
	// The two calendar rungs, whose answer is a WALL reading rather than an
	// elapsed span, so they are checked through the wall projection.
	for _, c := range []struct {
		name string
		got  rt.DateTime
		want string
	}{
		{"rt.Years", DateTimePlusYears(zoned.Ok, 3), "2024-06-15T10:20:30.4-04:00[America/New_York]"},
		{"rt.Months", DateTimePlusMonths(zoned.Ok, 3), "2021-09-15T10:20:30.4-04:00[America/New_York]"},
	} {
		if got := DateTimeToString(c.got); got != c.want {
			t.Errorf("DateTimePlus%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func withYMD(dt rt.NaiveDateTime, y, m, d int64) rt.NaiveDateTime {
	dt.Year, dt.Month, dt.Day = y, m, d
	return dt
}

// TestCivilShiftFaultTextIsOneString covers the arm no Nomi program reaches.
//
// std declares the zoned civil rungs TOTAL (`host fn add(lhs: DateTime, rhs:
// Days): DateTime`), and the one residual failure is a zone the tzdb does not
// carry. No DateTime a program can hold has one — every constructor validates
// the zone — so this arm is unreachable through the language and therefore
// invisible to the golden files, to the corpus and to every fixture.
//
// It is covered anyway because the message is built in one function
// (CivilShiftFaultText) and delivered by another (the trap), and a drift
// between the two is exactly what nothing else here would report.
func TestCivilShiftFaultTextIsOneString(t *testing.T) {
	bogus := rt.DateTime{InstantNanos: 0, Zone: "Mars/Olympus_Mons"}
	res := DateTimeCivilShift(bogus, 0, 0, 1, 0, 0, 0, 0)
	if res.Tag == rt.TagOk {
		t.Fatal("an unknown zone resolved, so this test has nothing to check")
	}
	want := CivilShiftFaultText(bogus.Zone, res.Err)
	// Absolute, because deriving it from the function under test is what makes
	// the rest of this test tautological about the STRING. Measured: dropping
	// the zone from the format leaves the substring intact anyway, since
	// CalendarErrorText's own payload carries it — so a `Contains` check here
	// passes for the wrong reason and this is the pin instead.
	if want != `civil shift on "Mars/Olympus_Mons": unknown zone: Mars/Olympus_Mons` {
		t.Errorf("the fault text changed shape: %q", want)
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("DateTimeCivilShiftOrTrap returned a value for an unknown zone; " +
				"a total signature over an unresolvable input must trap, not invent a moment")
		}
		err, isErr := r.(*rt.Error)
		if !isErr {
			t.Fatalf("trapped with %T, want *rt.Error", r)
		}
		if err.Msg != want {
			t.Errorf("trap text %q, want %q — every path to this trap must carry one string",
				err.Msg, want)
		}
	}()
	DateTimeCivilShiftOrTrap(bogus, 0, 0, 1, 0, 0, 0, 0)
}
