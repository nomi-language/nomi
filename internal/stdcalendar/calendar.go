// Package calendar is std/calendar's implementation: calendar dates, times,
// civil periods, machine-time projections, and IANA-zone-aware date-time
// arithmetic.
//
// # ONE implementation
//
// A rule set whose input space is every wall reading in every IANA zone
// across every transition rule has exactly one implementation, and it is here.
// `ResolveInZone` is the one place a DST gap or fold is classified and
// resolved; `PlusMonthsClamped` is the one place a shorter target month clamps;
// `ParseDate` / `ParseTime` / `ParseNaiveDateTime` / `ParseOffsetDateTime` /
// `ParseZonedDateTime` are the one place each spelling is accepted. The VM
// reaches them through the FFI projection: std/calendar.nomi declares 56 bare
// `host fn`s, internal/stdlibbindings pairs each with the FFI-shaped wrapper
// in ffi.go, and internal/stdlibadapters generates the VM's adapter for each.
// The pairing is held to the declarations by
// internal/hostpair.TestEveryHostDeclarationInAGoBackedModuleIsRegistered.
//
// Everything about these functions is pinned by ABSOLUTE assertions in the
// tests beside this file — the two-zones-one-instant equality, the
// civil-versus-physical DST divergence, end-of-month clamping, and each
// `Disambiguation` mode — and the corpus's own 02-scalars-and-time/dst_*.nomi
// programs exercise them through the VM.
//
// # What the move buys, measured
//
// `time/tzdata` is blank-imported below. Its registration runs in `init`, so
// the linker cannot drop it, so every binary that links this package pays its
// size. Keeping it out of rt keeps that cost off binaries that never mention a
// calendar type.
//
// # Two layers in two files
//
// This file is the SEMANTICS, typed in rt's shapes:
// `rt.Result[T, rt.CalendarError]`, `rt.Duration`, `rt.Instant`, `rt.Years`.
// boundary.go is the FFI-shaped surface, and every function there is a
// projection onto one of these. The split exists because the FFI projection
// does not carry three of rt's types; see boundary.go's header for which three
// and why.
package stdcalendar

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	// Embedded IANA time-zone database.
	//
	// std/calendar's `DateTime` is IANA-zoned and DST-aware, and linking this
	// package guarantees that on every host. Without it a machine with no
	// system tzdb — a scratch container, Windows — would answer `UnknownZone`
	// from `time.LoadLocation` for a zone that resolves elsewhere. The golden
	// files cannot see that, because they are recorded on a host that HAS a
	// tzdb.
	//
	// It costs binary size in a binary that links this package, which is why
	// the implementation lives here and not in rt.
	_ "time/tzdata"

	"github.com/nomi-language/nomi/rt"
)

// CalendarErr builds one error variant. rt's own callers go through these
// rather than writing the tag, for the reason rt.Some/rt.Ok exist: a tag
// written at each site is exactly the drift the constants above prevent.
func CalendarErr(tag uint8, msg string) rt.CalendarError {
	return rt.CalendarError{Tag: tag, Msg: msg}
}

// CalendarErrorText is the `Display.to_string` rendering of one error, and it
// is the one place a variant is paired with its prefix.
//
// std/calendar.nomi spells the same five arms in Nomi
// (`impl Display for Error`); that impl has a Nomi body and lowers through the
// ordinary path, so this exists for this package's own messages (fault texts
// and test failures), not as a second encoding a program reaches.
func CalendarErrorText(e rt.CalendarError) string {
	switch e.Tag {
	case rt.TagInvalidFormat:
		return "invalid format: " + e.Msg
	case rt.TagInvalidValue:
		return "invalid value: " + e.Msg
	case rt.TagUnknownZone:
		return "unknown zone: " + e.Msg
	case rt.TagNonexistent:
		return "nonexistent local time: " + e.Msg
	case rt.TagAmbiguous:
		return "ambiguous local time: " + e.Msg
	}
	return "invalid calendar error"
}

// ValidateDateComponents reports the error a (year, month, day) triple is
// invalid with, or ok when it names a real calendar date.
//
// Leap years are not special-cased: `time.Date` NORMALIZES an impossible
// combination, so a round-trip that comes back different is the detection, and
// it is right for every proleptic-Gregorian year by construction.
func ValidateDateComponents(year, month, day int64) (rt.CalendarError, bool) {
	if month < 1 || month > 12 {
		return CalendarErr(rt.TagInvalidValue, fmt.Sprintf("month out of range: %d", month)), false
	}
	if day < 1 || day > 31 {
		return CalendarErr(rt.TagInvalidValue, fmt.Sprintf("day out of range: %d", day)), false
	}
	t := time.Date(int(year), time.Month(month), int(day), 0, 0, 0, 0, time.UTC)
	if int64(t.Year()) != year || int64(t.Month()) != month || int64(t.Day()) != day {
		return CalendarErr(rt.TagInvalidValue, fmt.Sprintf("day %d not valid for %04d-%02d", day, year, month)), false
	}
	return rt.CalendarError{}, true
}

// ValidateTimeComponents bounds-checks a time-of-day. Leap seconds are not
// representable, matching `time`.
func ValidateTimeComponents(hour, minute, second, nanosecond int64) (rt.CalendarError, bool) {
	switch {
	case hour < 0 || hour > 23:
		return CalendarErr(rt.TagInvalidValue, fmt.Sprintf("hour out of range: %d", hour)), false
	case minute < 0 || minute > 59:
		return CalendarErr(rt.TagInvalidValue, fmt.Sprintf("minute out of range: %d", minute)), false
	case second < 0 || second > 59:
		return CalendarErr(rt.TagInvalidValue, fmt.Sprintf("second out of range: %d", second)), false
	case nanosecond < 0 || nanosecond > 999_999_999:
		return CalendarErr(rt.TagInvalidValue, fmt.Sprintf("nanosecond out of range: %d", nanosecond)), false
	}
	return rt.CalendarError{}, true
}

// NaiveDateTimeFromComponents validates a seven-component civil reading and
// builds it, which is `calendar.NaiveDateTime.new_exact`.
//
// The DATE combination is checked before the time-of-day, and the order is
// observable: `new(2026, 2, 30, 99, 0)` reports the impossible day rather than
// the impossible hour.
//
// ParseNaiveDateTime below is NOT factored through this, deliberately. It runs
// the same two validations in the same order, but INTERLEAVED with its own
// shape checks — it validates the date before it has even split the time — so
// routing it here would reorder the blame for a string that is wrong in both
// halves: `"2026-02-30Tbogus"` reports the impossible day today and would
// report the malformed time instead. Two call sites of two validators is the
// lesser duplication.
//
// EXACT: seven components, no defaults. A default belongs to the Nomi
// declaration that documents it — std/calendar.nomi's `pub fn new` fills
// `second` and `nanosecond` and calls this — and that is the whole point of the
// split. This function padding a short list would put the default's authority
// back in Go, one layer further down.
func NaiveDateTimeFromComponents(year, month, day, hour, minute, second, nanosecond int64) rt.Result[rt.NaiveDateTime, rt.CalendarError] {
	if e, valid := ValidateDateComponents(year, month, day); !valid {
		return rt.Err[rt.NaiveDateTime, rt.CalendarError](e)
	}
	if e, valid := ValidateTimeComponents(hour, minute, second, nanosecond); !valid {
		return rt.Err[rt.NaiveDateTime, rt.CalendarError](e)
	}
	return rt.Ok[rt.NaiveDateTime, rt.CalendarError](rt.NaiveDateTime{
		Year: year, Month: month, Day: day,
		Hour: hour, Minute: minute, Second: second, Nanosecond: nanosecond,
	})
}

// FormatNanosFrac renders a nanosecond count as a `.fff…` suffix, trailing
// zeros trimmed, empty when zero.
func FormatNanosFrac(nanos int64) string {
	if nanos == 0 {
		return ""
	}
	return "." + strings.TrimRight(fmt.Sprintf("%09d", nanos), "0")
}

// ISODateText renders `YYYY-MM-DD`.
func ISODateText(year, month, day int64) string {
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

// ISOTimeText renders `HH:MM:SS[.fff…]`.
func ISOTimeText(hour, minute, second, nanosecond int64) string {
	return fmt.Sprintf("%02d:%02d:%02d%s", hour, minute, second, FormatNanosFrac(nanosecond))
}

// ISONaiveText renders `YYYY-MM-DDTHH:MM:SS[.fff…]`.
func ISONaiveText(dt rt.NaiveDateTime) string {
	return ISODateText(dt.Year, dt.Month, dt.Day) + "T" +
		ISOTimeText(dt.Hour, dt.Minute, dt.Second, dt.Nanosecond)
}

// FormatOffsetHHMM renders signed seconds as `±HH:MM`.
func FormatOffsetHHMM(offsetSeconds int64) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	return fmt.Sprintf("%s%02d:%02d", sign, offsetSeconds/3600, (offsetSeconds%3600)/60)
}

// MaxOffsetSeconds caps a fixed UTC offset at ±18h, matching Temporal and
// `time.FixedZone`'s own guard.
const MaxOffsetSeconds int64 = 18 * 3600

// ParseFracSecondsNanos parses the digits after `.` in `HH:MM:SS.fff` into
// nanoseconds: right-padded to nine digits, truncated past nine, at least one
// digit required.
func ParseFracSecondsNanos(frac string) (int64, bool) {
	if frac == "" {
		return 0, false
	}
	if len(frac) > 9 {
		frac = frac[:9]
	} else {
		frac += strings.Repeat("0", 9-len(frac))
	}
	n, err := strconv.Atoi(frac)
	if err != nil {
		return 0, false
	}
	return int64(n), true
}

// SplitTimeOfDay splits `HH:MM:SS[.fff…]` into components WITHOUT bounds
// checking them — that is ValidateTimeComponents' job, and keeping the two
// apart is what lets a format error and a range error carry different variants.
func SplitTimeOfDay(s string) (hour, minute, second, nanosecond int64, ok bool) {
	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	parts := strings.Split(intPart, ":")
	if len(parts) != 3 {
		return 0, 0, 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, 0, false
	}
	var ns int64
	if hasFrac {
		ns, ok = ParseFracSecondsNanos(fracPart)
		if !ok {
			return 0, 0, 0, 0, false
		}
	}
	return int64(h), int64(m), int64(sec), ns, true
}

// ISODateSplit is how SplitISODate failed.
//
// Reported rather than folded into one `ok` because the two failures carry
// DIFFERENT observable messages and different quoted subjects at each caller —
// `Date.parse` says "non-numeric component in <whole string>" where
// `NaiveDateTime.parse` says "non-numeric date component in <whole string>" but
// quotes only the date half for a shape failure. Collapsing them here would
// silently reword one of the two, which is the kind of change nothing fails on.
type ISODateSplit uint8

const (
	// ISODateOK means the three components parsed. They are not bounds-checked;
	// that is ValidateDateComponents' job.
	ISODateOK ISODateSplit = iota
	// ISODateShape means the string was not exactly three `-`-separated parts.
	ISODateShape
	// ISODateNonNumeric means it had three parts and one was not an integer.
	ISODateNonNumeric
)

// SplitISODate splits `YYYY-MM-DD` into components without bounds checking.
//
// Exactly three `-`-separated parts, so a negative year (which would tokenize
// as four) is a shape failure rather than being silently accepted.
func SplitISODate(s string) (year, month, day int64, status ISODateSplit) {
	parts := strings.Split(s, "-")
	if len(parts) != 3 {
		return 0, 0, 0, ISODateShape
	}
	y, err1 := strconv.Atoi(parts[0])
	mo, err2 := strconv.Atoi(parts[1])
	d, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, ISODateNonNumeric
	}
	return int64(y), int64(mo), int64(d), ISODateOK
}

// ParseNaiveDateTime parses `YYYY-MM-DDTHH:MM:SS[.fff…]`, tolerating a single
// space in place of the `T`.
func ParseNaiveDateTime(s string) rt.Result[rt.NaiveDateTime, rt.CalendarError] {
	sep := strings.IndexAny(s, "T ")
	if sep < 0 {
		return rt.Err[rt.NaiveDateTime, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "expected YYYY-MM-DDTHH:MM:SS[.fff], got "+strconv.Quote(s)))
	}
	datePart, timePart := s[:sep], s[sep+1:]
	year, month, day, status := SplitISODate(datePart)
	switch status {
	case ISODateShape:
		return rt.Err[rt.NaiveDateTime, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "expected YYYY-MM-DD before T, got "+strconv.Quote(datePart)))
	case ISODateNonNumeric:
		return rt.Err[rt.NaiveDateTime, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "non-numeric date component in "+strconv.Quote(s)))
	}
	if e, valid := ValidateDateComponents(year, month, day); !valid {
		return rt.Err[rt.NaiveDateTime, rt.CalendarError](e)
	}
	hour, minute, second, nanosecond, ok := SplitTimeOfDay(timePart)
	if !ok {
		return rt.Err[rt.NaiveDateTime, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "expected HH:MM:SS[.fff…] after T, got "+strconv.Quote(timePart)))
	}
	if e, valid := ValidateTimeComponents(hour, minute, second, nanosecond); !valid {
		return rt.Err[rt.NaiveDateTime, rt.CalendarError](e)
	}
	return rt.Ok[rt.NaiveDateTime, rt.CalendarError](rt.NaiveDateTime{
		Year: year, Month: month, Day: day,
		Hour: hour, Minute: minute, Second: second, Nanosecond: nanosecond,
	})
}

// ParseOffsetHHMM parses `±HH:MM` (or `±HHMM`) into seconds.
func ParseOffsetHHMM(s string) (int64, bool) {
	if len(s) < 3 {
		return 0, false
	}
	sign := int64(1)
	switch s[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, false
	}
	rest := strings.Replace(s[1:], ":", "", 1)
	if len(rest) != 4 {
		return 0, false
	}
	h, err := strconv.Atoi(rest[:2])
	if err != nil || h < 0 {
		return 0, false
	}
	m, err := strconv.Atoi(rest[2:])
	if err != nil || m < 0 || m >= 60 {
		return 0, false
	}
	return sign * int64(h*3600+m*60), true
}

// ParseOffsetDateTime parses `YYYY-MM-DDTHH:MM:SS[.fff…]±HH:MM` or the `Z` form.
//
// The offset sign is found by scanning BACKWARDS from the end past the `T`,
// because the date half has `-` characters of its own.
func ParseOffsetDateTime(s string) rt.Result[rt.OffsetDateTime, rt.CalendarError] {
	if strings.HasSuffix(s, "Z") {
		naive := ParseNaiveDateTime(strings.TrimSuffix(s, "Z"))
		if naive.Tag != rt.TagOk {
			return rt.Err[rt.OffsetDateTime, rt.CalendarError](naive.Err)
		}
		return rt.Ok[rt.OffsetDateTime, rt.CalendarError](rt.OffsetDateTime{
			InstantNanos: naiveAsUTC(naive.Ok).UnixNano(),
		})
	}
	tIdx := strings.IndexAny(s, "T ")
	if tIdx < 0 {
		return rt.Err[rt.OffsetDateTime, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "expected an offset suffix (±HH:MM or Z), got "+strconv.Quote(s)))
	}
	signIdx := -1
	for i := len(s) - 1; i > tIdx; i-- {
		if s[i] == '+' || s[i] == '-' {
			signIdx = i
			break
		}
	}
	if signIdx < 0 {
		return rt.Err[rt.OffsetDateTime, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "expected an offset suffix (±HH:MM or Z), got "+strconv.Quote(s)))
	}
	offsetSeconds, ok := ParseOffsetHHMM(s[signIdx:])
	if !ok {
		return rt.Err[rt.OffsetDateTime, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "expected offset suffix ±HH:MM, got "+strconv.Quote(s[signIdx:])))
	}
	if offsetSeconds < -MaxOffsetSeconds || offsetSeconds > MaxOffsetSeconds {
		return rt.Err[rt.OffsetDateTime, rt.CalendarError](
			CalendarErr(rt.TagInvalidValue, fmt.Sprintf("offset out of range (±18h): %ds", offsetSeconds)))
	}
	naive := ParseNaiveDateTime(s[:signIdx])
	if naive.Tag != rt.TagOk {
		return rt.Err[rt.OffsetDateTime, rt.CalendarError](naive.Err)
	}
	dt := naive.Ok
	t := time.Date(int(dt.Year), time.Month(dt.Month), int(dt.Day), int(dt.Hour), int(dt.Minute),
		int(dt.Second), int(dt.Nanosecond), time.FixedZone("", int(offsetSeconds)))
	return rt.Ok[rt.OffsetDateTime, rt.CalendarError](rt.OffsetDateTime{
		InstantNanos:  t.UnixNano(),
		OffsetSeconds: offsetSeconds,
	})
}

func naiveAsUTC(dt rt.NaiveDateTime) time.Time {
	return time.Date(int(dt.Year), time.Month(dt.Month), int(dt.Day), int(dt.Hour), int(dt.Minute),
		int(dt.Second), int(dt.Nanosecond), time.UTC)
}

// LoadZone resolves an IANA zone name, answering the `UnknownZone` error rather
// than a Go error so every caller reports the same variant.
func LoadZone(zone string) (*time.Location, rt.CalendarError, bool) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, CalendarErr(rt.TagUnknownZone, zone), false
	}
	return loc, rt.CalendarError{}, true
}

// ResolveInZone is the DST gap/fold core: it projects a wall reading into a
// zone under one Disambiguation mode.
//
// Three cases, and the detection of each is a property rather than a table:
//
//  1. GAP (spring forward). `time.Date` silently normalizes a wall reading that
//     does not exist, so the candidate's own components differ from the input.
//     The two instants either side of the gap are then built by interpreting
//     the REQUESTED components at the pre- and post-transition offsets, because
//     which side `time.Date` normalized toward depends on the zone's rule shape
//     and is not something to rely on.
//  2. FOLD (fall back). Probe one hour forward in real time; if the offset
//     there differs, a transition just passed, so the same wall reading may
//     name two instants. The second is `candidate + (candidateOffset -
//     otherOffset)`, and it is only a genuine fold if that instant really does
//     render back to the same wall reading.
//  3. Unambiguous — one instant, every mode agrees.
func ResolveInZone(dt rt.NaiveDateTime, zone string, mode rt.Disambiguation) rt.Result[rt.DateTime, rt.CalendarError] {
	loc, zerr, ok := LoadZone(zone)
	if !ok {
		return rt.Err[rt.DateTime, rt.CalendarError](zerr)
	}
	candidate := time.Date(int(dt.Year), time.Month(dt.Month), int(dt.Day), int(dt.Hour),
		int(dt.Minute), int(dt.Second), int(dt.Nanosecond), loc)
	if !sameWallReading(candidate, dt) {
		// Gap: the requested reading does not exist in this zone.
		preOffset := offsetSecondsAt(candidate.Add(-time.Hour))
		postOffset := offsetSecondsAt(candidate.Add(time.Hour))
		wallUTC := naiveAsUTC(dt)
		post := wallUTC.Add(-time.Duration(preOffset) * time.Second)
		pre := wallUTC.Add(-time.Duration(postOffset) * time.Second)
		switch mode.Tag {
		case rt.TagReject:
			return rt.Err[rt.DateTime, rt.CalendarError](CalendarErr(rt.TagNonexistent,
				fmt.Sprintf("%s does not exist in %s (DST gap)", ISONaiveText(dt), zone)))
		case rt.TagEarlier:
			return rt.Ok[rt.DateTime, rt.CalendarError](rt.DateTime{InstantNanos: pre.UnixNano(), Zone: zone})
		default: // Compatible, Later
			return rt.Ok[rt.DateTime, rt.CalendarError](rt.DateTime{InstantNanos: post.UnixNano(), Zone: zone})
		}
	}
	candidateOffset := offsetSecondsAt(candidate)
	if afterOffset := offsetSecondsAt(candidate.Add(time.Hour)); afterOffset != candidateOffset {
		other := candidate.Add(time.Duration(candidateOffset-afterOffset) * time.Second)
		if sameWallReading(other.In(loc), dt) {
			// Fold: candidate is the earlier instant (larger offset).
			switch mode.Tag {
			case rt.TagReject:
				return rt.Err[rt.DateTime, rt.CalendarError](CalendarErr(rt.TagAmbiguous,
					fmt.Sprintf("%s is ambiguous in %s (DST fold)", ISONaiveText(dt), zone)))
			case rt.TagLater:
				return rt.Ok[rt.DateTime, rt.CalendarError](rt.DateTime{InstantNanos: other.UnixNano(), Zone: zone})
			default: // Compatible, Earlier
				return rt.Ok[rt.DateTime, rt.CalendarError](rt.DateTime{InstantNanos: candidate.UnixNano(), Zone: zone})
			}
		}
	}
	return rt.Ok[rt.DateTime, rt.CalendarError](rt.DateTime{InstantNanos: candidate.UnixNano(), Zone: zone})
}

func sameWallReading(t time.Time, dt rt.NaiveDateTime) bool {
	return int64(t.Year()) == dt.Year && int64(t.Month()) == dt.Month && int64(t.Day()) == dt.Day &&
		int64(t.Hour()) == dt.Hour && int64(t.Minute()) == dt.Minute &&
		int64(t.Second()) == dt.Second && int64(t.Nanosecond()) == dt.Nanosecond
}

func offsetSecondsAt(t time.Time) int64 {
	_, off := t.Zone()
	return int64(off)
}

// DaysInMonth is the length of one month, leap-year aware. Derived by asking
// `time` for the day before the first of the next month rather than from a
// table, so February needs no special case and neither does the century rule.
func DaysInMonth(year, month int64) int64 {
	return int64(time.Date(int(year), time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day())
}

// PlusMonthsClamped shifts a wall reading by whole calendar months, CLAMPING to
// the last day of the target month.
//
// The clamp is the semantics and not a convenience: `2011-01-31 + 1 month` is
// `2011-02-28`, because there is no 31st of February and the alternative
// (overflowing into March) would make `+1 month` skip a month. The leap-year
// case follows from DaysInMonth rather than from a second rule, so
// `2012-01-31 + 1 month` is `2012-02-29`.
//
// Negative n is the same computation: the month index floors toward negative
// infinity, which is what makes `2011-03-31 - 1 month` land on `2011-02-28`
// rather than on a normalized `2011-03-03`.
func PlusMonthsClamped(dt rt.NaiveDateTime, n int64) rt.NaiveDateTime {
	monthIdx := dt.Year*12 + (dt.Month - 1) + n
	year := monthIdx / 12
	month := monthIdx%12 + 1
	if monthIdx%12 < 0 {
		month += 12
		year--
	}
	day := dt.Day
	if maxDay := DaysInMonth(year, month); day > maxDay {
		day = maxDay
	}
	out := dt
	out.Year, out.Month, out.Day = year, month, day
	return out
}

// CivilShift is the general civil-arithmetic step over a wall reading: months
// and years clamp first, then every remaining unit is added to its component
// and `time.Date` normalizes the carries.
//
// Order is load-bearing. Clamping BEFORE the day shift is what makes
// `Jan 31 + 1 month + 1 day` land on `Mar 1` (clamp to Feb 28, then +1) rather
// than on `Mar 4` (Jan 31 + 1 day, then +1 month with nothing to clamp).
func CivilShift(dt rt.NaiveDateTime, years, months, days, hours, minutes, seconds, nanos int64) rt.NaiveDateTime {
	if years != 0 || months != 0 {
		dt = PlusMonthsClamped(dt, years*12+months)
	}
	t := time.Date(int(dt.Year), time.Month(dt.Month), int(dt.Day+days), int(dt.Hour+hours),
		int(dt.Minute+minutes), int(dt.Second+seconds), int(dt.Nanosecond+nanos), time.UTC)
	return rt.NaiveDateTime{
		Year: int64(t.Year()), Month: int64(t.Month()), Day: int64(t.Day()),
		Hour: int64(t.Hour()), Minute: int64(t.Minute()),
		Second: int64(t.Second()), Nanosecond: int64(t.Nanosecond()),
	}
}

// DateTimeCivilShift is civil arithmetic on a ZONED moment: shift the WALL
// reading, then re-resolve through the zone.
//
// This is the divergence std/calendar exists to make available, and it is not
// an approximation of it. `noon Mar 7 in New York + Days(1)` is noon on Mar 8 —
// 23 hours of elapsed time, because the clocks moved forward — while
// `+ Duration.hours(24)` is 1pm on Mar 8. Civil arithmetic preserves the wall
// reading; physical arithmetic preserves the elapsed time; they cannot both
// hold across a transition.
//
// Resolution is Compatible, which is the design's choice for
// arithmetic-as-total: a civil shift must not fail, and Compatible resolves
// every gap and fold. A gap CAN be landed on (`+ Days(1)` from the day before a
// spring-forward 2:30am), so the mode is doing real work rather than covering
// an impossible case.
func DateTimeCivilShift(d rt.DateTime, years, months, days, hours, minutes, seconds, nanos int64) rt.Result[rt.DateTime, rt.CalendarError] {
	wall, cerr, ok := DateTimeWall(d)
	if !ok {
		return rt.Err[rt.DateTime, rt.CalendarError](cerr)
	}
	shifted := CivilShift(wall, years, months, days, hours, minutes, seconds, nanos)
	return ResolveInZone(shifted, d.Zone, rt.Disambiguation{Tag: rt.TagCompatible})
}

// DateTimeWall projects a moment onto the wall reading its zone shows.
func DateTimeWall(d rt.DateTime) (rt.NaiveDateTime, rt.CalendarError, bool) {
	loc, cerr, ok := LoadZone(d.Zone)
	if !ok {
		return rt.NaiveDateTime{}, cerr, false
	}
	t := time.Unix(0, d.InstantNanos).In(loc)
	return rt.NaiveDateTime{
		Year: int64(t.Year()), Month: int64(t.Month()), Day: int64(t.Day()),
		Hour: int64(t.Hour()), Minute: int64(t.Minute()),
		Second: int64(t.Second()), Nanosecond: int64(t.Nanosecond()),
	}, rt.CalendarError{}, true
}

// ParseZonedDateTime parses the RFC 9557 bracket form,
// `YYYY-MM-DDTHH:MM:SS[.fff…]±HH:MM[Zone/Name]`.
//
// The bracket suffix is REQUIRED: a bare-offset string is an OffsetDateTime and
// parses through `OffsetDateTime.parse`, so accepting it here would silently
// invent a zone.
func ParseZonedDateTime(s string) rt.Result[rt.DateTime, rt.CalendarError] {
	openIdx := strings.IndexByte(s, '[')
	if openIdx < 0 || !strings.HasSuffix(s, "]") {
		return rt.Err[rt.DateTime, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "expected `[Zone/Name]` suffix, got "+strconv.Quote(s)))
	}
	zone := s[openIdx+1 : len(s)-1]
	front := ParseOffsetDateTime(s[:openIdx])
	if front.Tag != rt.TagOk {
		return rt.Err[rt.DateTime, rt.CalendarError](front.Err)
	}
	if _, cerr, ok := LoadZone(zone); !ok {
		return rt.Err[rt.DateTime, rt.CalendarError](cerr)
	}
	return rt.Ok[rt.DateTime, rt.CalendarError](rt.DateTime{InstantNanos: front.Ok.InstantNanos, Zone: zone})
}

// DateTimeWithZone re-projects the same moment through another IANA zone.
func DateTimeWithZone(d rt.DateTime, zone string) rt.Result[rt.DateTime, rt.CalendarError] {
	if _, cerr, ok := LoadZone(zone); !ok {
		return rt.Err[rt.DateTime, rt.CalendarError](cerr)
	}
	return rt.Ok[rt.DateTime, rt.CalendarError](rt.DateTime{InstantNanos: d.InstantNanos, Zone: zone})
}

// DateTimeFromInstantIn projects an absolute Instant into a zone. Total once
// the zone is known.
func DateTimeFromInstantIn(i rt.Instant, zone string) rt.Result[rt.DateTime, rt.CalendarError] {
	if _, cerr, ok := LoadZone(zone); !ok {
		return rt.Err[rt.DateTime, rt.CalendarError](cerr)
	}
	return rt.Ok[rt.DateTime, rt.CalendarError](rt.DateTime{InstantNanos: int64(i), Zone: zone})
}

// DateTimeToString is the RFC 9557 rendering — wall reading, effective offset,
// bracketed zone.
//
// A stored zone that no longer loads renders as the error text rather than
// panicking: the value came from a successful construction, so this is
// unreachable through the language, and a `Display` that can fault would make
// every render site fallible.
func DateTimeToString(d rt.DateTime) string {
	loc, cerr, ok := LoadZone(d.Zone)
	if !ok {
		return CalendarErrorText(cerr)
	}
	t := time.Unix(0, d.InstantNanos).In(loc)
	_, off := t.Zone()
	wall := rt.NaiveDateTime{
		Year: int64(t.Year()), Month: int64(t.Month()), Day: int64(t.Day()),
		Hour: int64(t.Hour()), Minute: int64(t.Minute()),
		Second: int64(t.Second()), Nanosecond: int64(t.Nanosecond()),
	}
	return ISONaiveText(wall) + FormatOffsetHHMM(int64(off)) + "[" + d.Zone + "]"
}

func DateTimeYear(d rt.DateTime) int64 { w, _, _ := DateTimeWall(d); return w.Year }

func DateTimeMonth(d rt.DateTime) int64 { w, _, _ := DateTimeWall(d); return w.Month }

func DateTimeDay(d rt.DateTime) int64 { w, _, _ := DateTimeWall(d); return w.Day }

func DateTimeHour(d rt.DateTime) int64 { w, _, _ := DateTimeWall(d); return w.Hour }

func DateTimeMinute(d rt.DateTime) int64 { w, _, _ := DateTimeWall(d); return w.Minute }

func DateTimeSecond(d rt.DateTime) int64 { w, _, _ := DateTimeWall(d); return w.Second }

func DateTimeNanosecond(d rt.DateTime) int64 { w, _, _ := DateTimeWall(d); return w.Nanosecond }

// DateTimeOffset is the UTC offset effective at this moment in this zone.
func DateTimeOffset(d rt.DateTime) rt.Duration {
	loc, _, ok := LoadZone(d.Zone)
	if !ok {
		return rt.Duration(0)
	}
	_, off := time.Unix(0, d.InstantNanos).In(loc).Zone()
	return rt.Duration(int64(off) * int64(time.Second))
}

// NaiveDateTimeToString is the ISO rendering.
func NaiveDateTimeToString(dt rt.NaiveDateTime) string { return ISONaiveText(dt) }

// NaiveDateTimeBetween is `later - earlier`, computed as if both were UTC wall
// readings — which is the only reading available, since a naive value names no
// instant.
func NaiveDateTimeBetween(earlier, later rt.NaiveDateTime) rt.Duration {
	return rt.Duration(naiveAsUTC(later).UnixNano() - naiveAsUTC(earlier).UnixNano())
}

// NaiveDateTimePlusDuration is `impl Add<Duration, NaiveDateTime>`: the EXACT
// rung of std/calendar's arithmetic ladder.
//
// Physical rather than civil, and on a naive value the distinction is narrower
// than it is on a zoned one but still real. A NaiveDateTime names no instant, so
// "exact" here means the wall reading advances by the duration's nanoseconds
// with ordinary carries — no zone is consulted and none exists to consult. The
// civil rungs (`+ Months(1)`) clamp end-of-month first and cannot be expressed
// this way at all; see PlusMonthsClamped and CivilShift for the arithmetic they
// need instead. `+ Duration.hours(24)` and `+ Days(1)` agree here and disagree
// on a DateTime across a transition, which is DateTimeCivilShift's subject.
//
// The computation: interpret the reading as UTC, add the nanoseconds through
// time.Time so the carries and month lengths come from one implementation, read
// the components back.
func NaiveDateTimePlusDuration(dt rt.NaiveDateTime, d rt.Duration) rt.NaiveDateTime {
	t := naiveAsUTC(dt).Add(time.Duration(d))
	return rt.NaiveDateTime{
		Year: int64(t.Year()), Month: int64(t.Month()), Day: int64(t.Day()),
		Hour: int64(t.Hour()), Minute: int64(t.Minute()),
		Second: int64(t.Second()), Nanosecond: int64(t.Nanosecond()),
	}
}

// NewDate is `Date.new`: validate the combination, then build.
//
// The validation is ValidateDateComponents', which detects an impossible
// combination by round-tripping through `time.Date` rather than special-casing
// leap years, so `2026-02-29` fails for every proleptic-Gregorian year by
// construction.
func NewDate(year, month, day int64) rt.Result[rt.Date, rt.CalendarError] {
	if e, ok := ValidateDateComponents(year, month, day); !ok {
		return rt.Err[rt.Date, rt.CalendarError](e)
	}
	return rt.Ok[rt.Date, rt.CalendarError](rt.Date{Year: year, Month: month, Day: day})
}

// ParseDate is `Date.parse`, and it also backs the `Date"…"` typed literal
// through `impl Literal for Date`.
//
// The three arms are three different faults and std's own doctests distinguish
// them: a SHAPE fault (not three `-`-separated parts), a NON-NUMERIC fault
// (right shape, unparseable parts), and a VALUE fault (parses, names no real
// date). Only the third comes from the validator; the first two are composed
// here.
func ParseDate(s string) rt.Result[rt.Date, rt.CalendarError] {
	year, month, day, status := SplitISODate(s)
	switch status {
	case ISODateShape:
		return rt.Err[rt.Date, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "expected YYYY-MM-DD, got "+strconv.Quote(s)))
	case ISODateNonNumeric:
		return rt.Err[rt.Date, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "non-numeric component in "+strconv.Quote(s)))
	}
	return NewDate(year, month, day)
}

// NewTime is the validating `Time` constructor.
//
// std writes `Time.new` as a NOMI body with `second: Int = 0, nanosecond:
// Int = 0`, so a program reaches that body rather than this function and the
// two are not a duplicated rule: this is ParseTime's tail, and it exists so the
// bounds check and the construction are one step at every call site here.
func NewTime(hour, minute, second, nanosecond int64) rt.Result[rt.Time, rt.CalendarError] {
	if e, ok := ValidateTimeComponents(hour, minute, second, nanosecond); !ok {
		return rt.Err[rt.Time, rt.CalendarError](e)
	}
	return rt.Ok[rt.Time, rt.CalendarError](rt.Time{
		Hour: hour, Minute: minute, Second: second, Nanosecond: nanosecond,
	})
}

// ParseTime is `Time.parse`, and it also backs the `Time"…"` typed literal.
//
// `SplitTimeOfDay` answers a bool rather than ISODateSplit's three-way status,
// so there are two arms here rather than three; the ellipsis in the message is
// U+2026 and is observable, which is why the pin quotes it exactly.
func ParseTime(s string) rt.Result[rt.Time, rt.CalendarError] {
	hour, minute, second, nanosecond, ok := SplitTimeOfDay(s)
	if !ok {
		return rt.Err[rt.Time, rt.CalendarError](
			CalendarErr(rt.TagInvalidFormat, "expected HH:MM:SS[.fff…], got "+strconv.Quote(s)))
	}
	return NewTime(hour, minute, second, nanosecond)
}

// DateToString is `impl Display for Date`: the ISO-8601 `YYYY-MM-DD` form.
func DateToString(d rt.Date) string { return ISODateText(d.Year, d.Month, d.Day) }

// TimeToString is `impl Display for Time`.
//
// Sub-second precision renders only when nonzero (`09:30:00`, not
// `09:30:00.000`), which is ISOTimeText's rule and std's documented promise.
func TimeToString(t rt.Time) string {
	return ISOTimeText(t.Hour, t.Minute, t.Second, t.Nanosecond)
}

// NaiveDateTimeToDate is `NaiveDateTime.to_date`: drop the time-of-day.
func NaiveDateTimeToDate(dt rt.NaiveDateTime) rt.Date {
	return rt.Date{Year: dt.Year, Month: dt.Month, Day: dt.Day}
}

// NaiveDateTimeToTime is `NaiveDateTime.to_time`: drop the date.
func NaiveDateTimeToTime(dt rt.NaiveDateTime) rt.Time {
	return rt.Time{Hour: dt.Hour, Minute: dt.Minute, Second: dt.Second, Nanosecond: dt.Nanosecond}
}

// asNaive lifts a Date to midnight, so the civil rungs below can be a naming of
// CivilShift rather than a second clamping implementation.
func asNaive(d rt.Date) rt.NaiveDateTime {
	return rt.NaiveDateTime{Year: d.Year, Month: d.Month, Day: d.Day}
}

// DateDaysBetween is `Date.days_between`: whole days from `earlier` to `later`,
// negative when `later` is the earlier of the two.
//
// The `Hours()/24` route rather than a direct day subtraction is deliberate:
// both signs are pinned, and changing the route is a behaviour change to make
// on its own.
//
// Both operands are midnight UTC, so no DST transition can shorten a day and
// the quotient is exact.
func DateDaysBetween(earlier, later rt.Date) int64 {
	t1 := time.Date(int(earlier.Year), time.Month(earlier.Month), int(earlier.Day), 0, 0, 0, 0, time.UTC)
	t2 := time.Date(int(later.Year), time.Month(later.Month), int(later.Day), 0, 0, 0, 0, time.UTC)
	return int64(t2.Sub(t1).Hours()) / 24
}

// DatePlusYears is `impl Add<Years, Date>`: clamp the day into the target month.
func DatePlusYears(d rt.Date, n rt.Years) rt.Date {
	return NaiveDateTimeToDate(CivilShift(asNaive(d), int64(n), 0, 0, 0, 0, 0, 0))
}

// DatePlusMonths is `impl Add<Months, Date>`: end-of-month clamping.
func DatePlusMonths(d rt.Date, n rt.Months) rt.Date {
	return NaiveDateTimeToDate(CivilShift(asNaive(d), 0, int64(n), 0, 0, 0, 0, 0))
}

// DatePlusDays is `impl Add<Days, Date>`: advance the day.
func DatePlusDays(d rt.Date, n rt.Days) rt.Date {
	return NaiveDateTimeToDate(CivilShift(asNaive(d), 0, 0, int64(n), 0, 0, 0, 0))
}

// TimePlusDuration is `impl Add<Duration, Time>`: exact elapsed time, wrapping
// around midnight.
//
// Modular rather than saturating, which
// is std's documented promise, and the explicit negative correction is
// load-bearing: Go's `%` keeps the sign of the DIVIDEND, so without it
// `Time"00:30:00" + Duration.hours(-1)` would answer a negative hour instead of
// `23:30:00`. Both directions are pinned by this package's tests.
//
// This is the ONE place `Time` arithmetic is decided; `Subtract<Duration, Time>`
// is a Nomi body over `rhs * -1`, so it reaches this same function.
func TimePlusDuration(t rt.Time, d rt.Duration) rt.Time {
	const dayNanos = int64(24 * time.Hour)
	total := t.Hour*int64(time.Hour) + t.Minute*int64(time.Minute) +
		t.Second*int64(time.Second) + t.Nanosecond
	total = (total + int64(d)) % dayNanos
	if total < 0 {
		total += dayNanos
	}
	out := rt.Time{Hour: total / int64(time.Hour)}
	total %= int64(time.Hour)
	out.Minute = total / int64(time.Minute)
	total %= int64(time.Minute)
	out.Second = total / int64(time.Second)
	out.Nanosecond = total % int64(time.Second)
	return out
}

// OffsetDateTimeWall is the wall reading an OffsetDateTime displays.
//
// Total, unlike DateTimeWall: a fixed offset needs no tzdb lookup, so there is
// no zone that can fail to load and no error arm to propagate.
func OffsetDateTimeWall(odt rt.OffsetDateTime) rt.NaiveDateTime {
	t := offsetWall(odt)
	return rt.NaiveDateTime{
		Year: int64(t.Year()), Month: int64(t.Month()), Day: int64(t.Day()),
		Hour: int64(t.Hour()), Minute: int64(t.Minute()),
		Second: int64(t.Second()), Nanosecond: int64(t.Nanosecond()),
	}
}

// offsetWall projects instant + offset onto a Go time in a fixed zone. The one
// place that projection is written; every accessor below reads through it.
func offsetWall(odt rt.OffsetDateTime) time.Time {
	return time.Unix(0, odt.InstantNanos).In(time.FixedZone("", int(odt.OffsetSeconds)))
}

// OffsetDateTimeWithOffset combines a wall reading with a fixed UTC offset.
//
// Two failure modes and both are the OFFSET's, never the reading's: the reading
// arrives as a NaiveDateTime, which was validated when it was built.
//
//   - A SUB-SECOND offset is refused rather than truncated. Neither IANA nor
//     `time.FixedZone` carries one, so truncating would answer a question about
//     a different offset than the caller asked about.
//   - Beyond ±18h is refused, matching Temporal's cap and Go's own guard.
func OffsetDateTimeWithOffset(dt rt.NaiveDateTime, offset rt.Duration) rt.Result[rt.OffsetDateTime, rt.CalendarError] {
	nanos := int64(offset)
	if nanos%int64(time.Second) != 0 {
		return rt.Err[rt.OffsetDateTime, rt.CalendarError](CalendarErr(rt.TagInvalidValue,
			"offset must be a whole number of seconds (got sub-second nanos)"))
	}
	offsetSeconds := nanos / int64(time.Second)
	if offsetSeconds < -MaxOffsetSeconds || offsetSeconds > MaxOffsetSeconds {
		return rt.Err[rt.OffsetDateTime, rt.CalendarError](CalendarErr(rt.TagInvalidValue,
			fmt.Sprintf("offset out of range (±18h): %ds", offsetSeconds)))
	}
	t := time.Date(int(dt.Year), time.Month(dt.Month), int(dt.Day), int(dt.Hour), int(dt.Minute),
		int(dt.Second), int(dt.Nanosecond), time.FixedZone("", int(offsetSeconds)))
	return rt.Ok[rt.OffsetDateTime, rt.CalendarError](rt.OffsetDateTime{
		InstantNanos:  t.UnixNano(),
		OffsetSeconds: offsetSeconds,
	})
}

// OffsetDateTimeFromInstant attaches a fixed offset to an absolute instant.
//
// TOTAL, and std declares it returning `OffsetDateTime` rather than a Result to
// say so: every instant has a representation at every offset, so there is
// nothing to reject. A sub-second offset truncates here where WithOffset
// refuses, and the asymmetry is std/calendar.nomi's own design — the two
// functions differ in whether a caller is naming a MOMENT (total) or asserting
// a WALL READING at an offset (validated).
func OffsetDateTimeFromInstant(i rt.Instant, offset rt.Duration) rt.OffsetDateTime {
	return rt.OffsetDateTime{
		InstantNanos:  int64(i),
		OffsetSeconds: int64(offset) / int64(time.Second),
	}
}

// OffsetDateTimeToString is the `Display.to_string` rendering:
// `YYYY-MM-DDTHH:MM:SS[.fff…]±HH:MM`.
//
// Both halves are calendar.go's — ISONaiveText for the reading and
// FormatOffsetHHMM for the suffix — so this is a composition and not a third
// place a timestamp is formatted.
func OffsetDateTimeToString(odt rt.OffsetDateTime) string {
	return ISONaiveText(OffsetDateTimeWall(odt)) + FormatOffsetHHMM(odt.OffsetSeconds)
}

func OffsetDateTimeYear(odt rt.OffsetDateTime) int64 { return int64(offsetWall(odt).Year()) }

func OffsetDateTimeMonth(odt rt.OffsetDateTime) int64 { return int64(offsetWall(odt).Month()) }

func OffsetDateTimeDay(odt rt.OffsetDateTime) int64 { return int64(offsetWall(odt).Day()) }

func OffsetDateTimeHour(odt rt.OffsetDateTime) int64 { return int64(offsetWall(odt).Hour()) }

func OffsetDateTimeMinute(odt rt.OffsetDateTime) int64 { return int64(offsetWall(odt).Minute()) }

func OffsetDateTimeSecond(odt rt.OffsetDateTime) int64 { return int64(offsetWall(odt).Second()) }

func OffsetDateTimeNanosecond(odt rt.OffsetDateTime) int64 {
	return int64(offsetWall(odt).Nanosecond())
}

// OffsetDateTimePlusMonths shifts the WALL READING by whole calendar months,
// clamping a shorter target month, and re-anchors at the same offset.
//
// The clamp is PlusMonthsClamped's — the one place a shorter target month
// clamps, per rt/calendar.go's header — so Date, NaiveDateTime, DateTime and
// this type all answer 2026-02-28 for January 31st plus one month.
func OffsetDateTimePlusMonths(odt rt.OffsetDateTime, n rt.Months) rt.OffsetDateTime {
	return offsetShiftMonths(odt, int64(n))
}

// OffsetDateTimePlusYears shifts by whole calendar years.
//
// Twelve months rather than a year field, which is what makes 2024-02-29 plus
// one year clamp to 2025-02-28 through the SAME rule as the month rung instead
// of a second leap-day special case.
func OffsetDateTimePlusYears(odt rt.OffsetDateTime, n rt.Years) rt.OffsetDateTime {
	return offsetShiftMonths(odt, int64(n)*12)
}

func offsetShiftMonths(odt rt.OffsetDateTime, months int64) rt.OffsetDateTime {
	shifted := PlusMonthsClamped(OffsetDateTimeWall(odt), months)
	t := time.Date(int(shifted.Year), time.Month(shifted.Month), int(shifted.Day),
		int(shifted.Hour), int(shifted.Minute), int(shifted.Second), int(shifted.Nanosecond),
		time.FixedZone("", int(odt.OffsetSeconds)))
	return rt.OffsetDateTime{InstantNanos: t.UnixNano(), OffsetSeconds: odt.OffsetSeconds}
}

// DateTimeToOffset projects a zoned moment onto the fixed offset that is
// effective at that instant, discarding the zone.
//
// It lives with the TARGET type because it is the only function that produces
// an OffsetDateTime from a DateTime, and the interesting half of it is what an
// OffsetDateTime is: an instant plus a number, where the zone was a rule.
// Converting back is deliberately not the inverse — an OffsetDateTime cannot
// say which zone it came from, so `DateTime.in_zone` has to be told again.
//
// The offset comes from DateTimeOffset, so the tzdb read is not encoded a
// second time; a stored zone that no longer loads therefore yields +00:00,
// which is the convention every DateTime accessor already follows and is
// unreachable through the language because the value came from a successful
// construction. So this function answers where its thirteen siblings answer,
// rather than being the one that faults.
func DateTimeToOffset(d rt.DateTime) rt.OffsetDateTime {
	return rt.OffsetDateTime{
		InstantNanos:  d.InstantNanos,
		OffsetSeconds: int64(DateTimeOffset(d)) / int64(time.Second),
	}
}

// NaiveDateTimePlusYears is `impl Add<Years, NaiveDateTime>`: clamp the day
// into the target month, then keep the wall clock.
func NaiveDateTimePlusYears(dt rt.NaiveDateTime, n rt.Years) rt.NaiveDateTime {
	return CivilShift(dt, int64(n), 0, 0, 0, 0, 0, 0)
}

// NaiveDateTimePlusMonths is `impl Add<Months, NaiveDateTime>`: end-of-month
// clamping, leap years included.
func NaiveDateTimePlusMonths(dt rt.NaiveDateTime, n rt.Months) rt.NaiveDateTime {
	return CivilShift(dt, 0, int64(n), 0, 0, 0, 0, 0)
}

// NaiveDateTimePlusDays is `impl Add<Days, NaiveDateTime>`: advance the day and
// leave the time components alone.
//
// On a naive value this agrees with `+ Duration.hours(24)`, and on a DateTime
// it does not. The agreement here is a fact about there being no zone, not a
// sign that the two operations are the same one.
func NaiveDateTimePlusDays(dt rt.NaiveDateTime, n rt.Days) rt.NaiveDateTime {
	return CivilShift(dt, 0, 0, int64(n), 0, 0, 0, 0)
}

// CivilShiftFaultText is the message a civil shift that cannot resolve produces.
//
// Exported so the trap text has one definition rather than two copies that
// agree, which is the same reason DurationToString lives here.
func CivilShiftFaultText(zone string, e rt.CalendarError) string {
	return fmt.Sprintf("civil shift on %q: %s", zone, CalendarErrorText(e))
}

// DateTimeCivilShiftOrTrap is the TOTAL civil shift std's signature declares.
//
// `impl Add<Years, DateTime>`'s `add` returns `DateTime`, not
// `Result<DateTime, Error>` — std's decision that arithmetic does not fail,
// which DateTimeCivilShift honours by resolving every gap and fold with
// `Compatible`. The one residual failure is a zone the tzdb does not carry, and
// no DateTime a program can hold has one: every constructor (`in_zone`,
// `from_instant_in`, `parse`, `with_zone`) validates the zone first. So this
// arm is unreachable rather than merely unlikely, and it traps rather than
// inventing a value, with CivilShiftFaultText's text.
func DateTimeCivilShiftOrTrap(d rt.DateTime, years, months, days, hours, minutes, seconds, nanos int64) rt.DateTime {
	res := DateTimeCivilShift(d, years, months, days, hours, minutes, seconds, nanos)
	if res.Tag != rt.TagOk {
		rt.Trap(CivilShiftFaultText(d.Zone, res.Err))
	}
	return res.Ok
}

// DateTimePlusYears is `impl Add<Years, DateTime>`.
func DateTimePlusYears(d rt.DateTime, n rt.Years) rt.DateTime {
	return DateTimeCivilShiftOrTrap(d, int64(n), 0, 0, 0, 0, 0, 0)
}

// DateTimePlusMonths is `impl Add<Months, DateTime>`.
func DateTimePlusMonths(d rt.DateTime, n rt.Months) rt.DateTime {
	return DateTimeCivilShiftOrTrap(d, 0, int64(n), 0, 0, 0, 0, 0)
}

// DateTimePlusDays is `impl Add<Days, DateTime>`, and it is the rung the whole
// civil-versus-physical distinction is visible through.
//
// `noon Mar 7 in New York + Days(1)` is noon on Mar 8 — 23 hours of elapsed
// time — while `+ Duration.hours(24)` is 1pm on Mar 8.
func DateTimePlusDays(d rt.DateTime, n rt.Days) rt.DateTime {
	return DateTimeCivilShiftOrTrap(d, 0, 0, int64(n), 0, 0, 0, 0)
}

// DateTimePlusHours is `impl Add<Hours, DateTime>`: a WALL-CLOCK hour.
//
// Not `Duration.hours(n)`. Adding 24 civil hours across a spring-forward lands
// on the same reading `+ Days(1)` does, because both shift the wall reading and
// then re-resolve; adding a 24-hour Duration does not.
func DateTimePlusHours(d rt.DateTime, n rt.Hours) rt.DateTime {
	return DateTimeCivilShiftOrTrap(d, 0, 0, 0, int64(n), 0, 0, 0)
}

// DateTimePlusMinutes is `impl Add<Minutes, DateTime>`.
func DateTimePlusMinutes(d rt.DateTime, n rt.Minutes) rt.DateTime {
	return DateTimeCivilShiftOrTrap(d, 0, 0, 0, 0, int64(n), 0, 0)
}

// DateTimePlusSeconds is `impl Add<Seconds, DateTime>`.
func DateTimePlusSeconds(d rt.DateTime, n rt.Seconds) rt.DateTime {
	return DateTimeCivilShiftOrTrap(d, 0, 0, 0, 0, 0, int64(n), 0)
}

// DateTimePlusMilliseconds is `impl Add<Milliseconds, DateTime>`.
func DateTimePlusMilliseconds(d rt.DateTime, n rt.Milliseconds) rt.DateTime {
	return DateTimeCivilShiftOrTrap(d, 0, 0, 0, 0, 0, 0, int64(n)*1_000_000)
}

// DateTimePlusMicroseconds is `impl Add<Microseconds, DateTime>`.
func DateTimePlusMicroseconds(d rt.DateTime, n rt.Microseconds) rt.DateTime {
	return DateTimeCivilShiftOrTrap(d, 0, 0, 0, 0, 0, 0, int64(n)*1_000)
}

// DateTimePlusNanoseconds is `impl Add<Nanoseconds, DateTime>`.
func DateTimePlusNanoseconds(d rt.DateTime, n rt.Nanoseconds) rt.DateTime {
	return DateTimeCivilShiftOrTrap(d, 0, 0, 0, 0, 0, 0, int64(n))
}
