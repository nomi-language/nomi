package stdcalendar

// The boundary: the functions std/calendar.nomi's `host fn`s are bound to
// (internal/stdlibbindings) where the semantics beside them take a shape
// the facade does not pass.
//
// # What the boundary carries
//
// A `host fn` crosses through the host projection that `internal/hostgen`
// generates adapters from (its shape.go lists the rows). Most of
// std/calendar's surface fits a row, which is why `DateToString`,
// `DateDaysBetween`,
// `NaiveDateTimeToDate`, `NaiveDateTimeToString`, `OffsetDateTimeToString`,
// `DateTimeToString`, `DateTimeToOffset` and the fourteen
// `OffsetDateTime*`/`DateTime*` component accessors in calendar.go are bound
// straight from the semantics with no wrapper here. A wrapper for those would
// be a function whose only content is a second name.
//
// The functions here pass three things in another shape:
//
//  1. Failure. A Go `error` crosses only as `Result<_, String>`, and
//     std/calendar has twenty-five fallible declarations returning
//     `Result<_, Error>`, a five-variant enum whose classification callers
//     match on. So failure crosses as DATA in one of rt's five carrier
//     structs and the Nomi facade rebuilds the variant.
//
//  2. `Duration` and `Instant`. Nanoseconds cross as `Int` and the facade
//     wraps with `Duration.nanoseconds` / `Instant.from_nanos`.
//
//  3. The ten civil periods (`Years` … `Nanoseconds`). Each ladder rung is
//     a NOMI body that destructures its period and calls an `Int`-taking
//     function here, the shape std/calendar uses for `Add<Weeks, Date>`.
//     The count crosses as an rt.Int of any size, and the semantics take it
//     as one.
//
// # Every declaration bound from Nomi is TOP-LEVEL and module-private
//
// std/random does the same. An impl-owned key drops its module prefix
// (hostpair disagreement 1, still open), so a top-level declaration is the
// shape with one unambiguous key and no interaction with the
// interface-instantiation rules.
//
// # Nothing here decides a calendar rule
//
// Every function is a projection: unwrap an Int into an rt newtype, or move an
// `rt.Result`'s two arms into a carrier's three fields. A rule added here would
// be a second encoding of something calendar.go already decides, which is the
// property the whole two-file split exists to keep checkable.

import (
	"fmt"
	"math/big"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// --- the carriers ----------------------------------------------------------
//
// One projection per value type. `Kind` is 0 on success, otherwise the rt error
// tag; `Reason` is that variant's payload. On failure `Value` stays the zero
// value and the facade never reads it, because it branches on Kind first.

func dateCarrier(r rt.Result[rt.Date, rt.CalendarError]) rt.CalendarDateResult {
	if r.Tag != rt.TagOk {
		return rt.CalendarDateResult{Kind: int64(r.Err.Tag), Reason: r.Err.Msg}
	}
	return rt.CalendarDateResult{Value: r.Ok}
}

func timeCarrier(r rt.Result[rt.Time, rt.CalendarError]) rt.CalendarTimeResult {
	if r.Tag != rt.TagOk {
		return rt.CalendarTimeResult{Kind: int64(r.Err.Tag), Reason: r.Err.Msg}
	}
	return rt.CalendarTimeResult{Value: r.Ok}
}

func naiveCarrier(r rt.Result[rt.NaiveDateTime, rt.CalendarError]) rt.CalendarNaiveResult {
	if r.Tag != rt.TagOk {
		return rt.CalendarNaiveResult{Kind: int64(r.Err.Tag), Reason: r.Err.Msg}
	}
	return rt.CalendarNaiveResult{Value: r.Ok}
}

func offsetCarrier(r rt.Result[rt.OffsetDateTime, rt.CalendarError]) rt.CalendarOffsetResult {
	if r.Tag != rt.TagOk {
		return rt.CalendarOffsetResult{Kind: int64(r.Err.Tag), Reason: r.Err.Msg}
	}
	return rt.CalendarOffsetResult{Value: r.Ok}
}

func zonedCarrier(r rt.Result[rt.DateTime, rt.CalendarError]) rt.CalendarZonedResult {
	if r.Tag != rt.TagOk {
		return rt.CalendarZonedResult{Kind: int64(r.Err.Tag), Reason: r.Err.Msg}
	}
	return rt.CalendarZonedResult{Value: r.Ok}
}

// --- counts of any size ---------------------------------------------------
//
// A count crosses as an Int of any size (rt.Int). A year and a shift's count
// pass to the semantics as they are, which are exact for every Int; a
// bounded component (a month, a day, a time of day) past int64 is past its
// bound, and refused as such.

// bounded is a constructor's bounded component as an int64, or the
// InvalidValue naming it when it passes int64, which is past every bound.
func bounded(name string, n rt.Int) (int64, rt.CalendarError, bool) {
	v, ok := n.Int64()
	if !ok {
		return 0, CalendarErr(rt.TagInvalidValue, fmt.Sprintf("%s out of range: %s", name, n)), false
	}
	return v, rt.CalendarError{}, true
}

// components is bounded over several components, the first failure first.
func components(names []string, ns ...rt.Int) ([]int64, rt.CalendarError, bool) {
	out := make([]int64, len(ns))
	for i, n := range ns {
		v, e, ok := bounded(names[i], n)
		if !ok {
			return nil, e, false
		}
		out[i] = v
	}
	return out, rt.CalendarError{}, true
}

// --- Date ------------------------------------------------------------------

// BoundDateNew is `Date.new`.
func BoundDateNew(year, month, day rt.Int) rt.CalendarDateResult {
	c, e, ok := components([]string{"month", "day"}, month, day)
	if !ok {
		return rt.CalendarDateResult{Kind: int64(e.Tag), Reason: e.Msg}
	}
	return dateCarrier(NewDate(year, c[0], c[1]))
}

// BoundDateParse is `Date.parse`, and it also backs the `Date"…"` typed literal
// through `impl Literal for Date`.
func BoundDateParse(s string) rt.CalendarDateResult { return dateCarrier(ParseDate(s)) }

// BoundDateAddYears is `impl Add<Years, Date>`.
func BoundDateAddYears(d rt.Date, n rt.Int) rt.Date { return DatePlusYears(d, n) }

// BoundDateAddMonths is `impl Add<Months, Date>`.
func BoundDateAddMonths(d rt.Date, n rt.Int) rt.Date { return DatePlusMonths(d, n) }

// BoundDateAddDays is `impl Add<Days, Date>`.
func BoundDateAddDays(d rt.Date, n rt.Int) rt.Date { return DatePlusDays(d, n) }

// --- Time ------------------------------------------------------------------

// BoundTimeParse is `Time.parse`, and it also backs the `Time"…"` typed literal.
func BoundTimeParse(s string) rt.CalendarTimeResult { return timeCarrier(ParseTime(s)) }

// BoundTimeAddNanos is `impl Add<Duration, Time>`: exact elapsed time, wrapping
// around midnight, for a Duration of any length.
func BoundTimeAddNanos(t rt.Time, nanos rt.Int) rt.Time {
	if v, ok := nanos.Int64(); ok {
		return TimePlusDuration(t, rt.Duration(v))
	}
	day := new(big.Int).Mod(nanos.Big(), big.NewInt(int64(24*time.Hour)))
	return TimePlusDuration(t, rt.Duration(day.Int64()))
}

// --- NaiveDateTime ---------------------------------------------------------

// BoundNaiveNewExact is `NaiveDateTime.new_exact`: seven components, no
// defaults. The two defaults `NaiveDateTime.new` carries belong to the Nomi
// declaration that documents them, which is the whole point of the split.
func BoundNaiveNewExact(year, month, day, hour, minute, second, nanosecond rt.Int) rt.CalendarNaiveResult {
	c, e, ok := components([]string{"month", "day", "hour", "minute", "second", "nanosecond"},
		month, day, hour, minute, second, nanosecond)
	if !ok {
		return rt.CalendarNaiveResult{Kind: int64(e.Tag), Reason: e.Msg}
	}
	return naiveCarrier(NaiveDateTimeFromComponents(year, c[0], c[1], c[2], c[3], c[4], c[5]))
}

// BoundNaiveParse is `NaiveDateTime.parse`, and it also backs the
// `NaiveDateTime"…"` typed literal.
func BoundNaiveParse(s string) rt.CalendarNaiveResult { return naiveCarrier(ParseNaiveDateTime(s)) }

// BoundNaiveBetweenNanos is `NaiveDateTime.between` in nanoseconds, exact at
// any distance (NaiveDateTimeBetween).
func BoundNaiveBetweenNanos(earlier, later rt.NaiveDateTime) rt.Int {
	return NaiveDateTimeBetween(earlier, later)
}

// BoundNaiveAddYears is `impl Add<Years, NaiveDateTime>`.
func BoundNaiveAddYears(dt rt.NaiveDateTime, n rt.Int) rt.NaiveDateTime {
	return NaiveDateTimePlusYears(dt, n)
}

// BoundNaiveAddMonths is `impl Add<Months, NaiveDateTime>`.
func BoundNaiveAddMonths(dt rt.NaiveDateTime, n rt.Int) rt.NaiveDateTime {
	return NaiveDateTimePlusMonths(dt, n)
}

// BoundNaiveAddDays is `impl Add<Days, NaiveDateTime>`.
func BoundNaiveAddDays(dt rt.NaiveDateTime, n rt.Int) rt.NaiveDateTime {
	return NaiveDateTimePlusDays(dt, n)
}

// BoundNaiveAddNanos is `impl Add<Duration, NaiveDateTime>`: the EXACT rung,
// for a Duration of any length.
func BoundNaiveAddNanos(dt rt.NaiveDateTime, nanos rt.Int) rt.NaiveDateTime {
	return CivilShift(dt, zero, zero, zero, zero, zero, zero, nanos)
}

// --- OffsetDateTime --------------------------------------------------------

// BoundOffsetWithOffset is `OffsetDateTime.with_offset`.
// An offset is a Duration of any length; one past int64 nanoseconds
// saturates, which is past ±18 hours either way and refused as such.
func BoundOffsetWithOffset(dt rt.NaiveDateTime, offsetNanos rt.Int) rt.CalendarOffsetResult {
	return offsetCarrier(OffsetDateTimeWithOffset(dt, rt.Duration(offsetNanos.Saturated())))
}

// BoundOffsetFromInstant is `OffsetDateTime.from_instant`: an instant of
// any size at a fixed offset. The facade has checked the offset is within
// ±18 hours, so saturating it changes nothing, and a sub-second offset is
// cut to whole seconds.
func BoundOffsetFromInstant(instantNanos rt.Int, offsetNanos rt.Int) rt.OffsetDateTime {
	return OffsetDateTimeFromInstant(instantNanos, offsetNanos.Saturated()/int64(time.Second))
}

// BoundOffsetAddNanos is `impl Add<Duration, OffsetDateTime>`.
func BoundOffsetAddNanos(odt rt.OffsetDateTime, nanos rt.Int) rt.OffsetDateTime {
	return OffsetDateTimePlusNanos(odt, nanos)
}

// BoundOffsetParse is `OffsetDateTime.parse`, and it also backs the
// `OffsetDateTime"…"` typed literal.
func BoundOffsetParse(s string) rt.CalendarOffsetResult {
	return offsetCarrier(ParseOffsetDateTime(s))
}

// BoundOffsetAddYears is `impl Add<Years, OffsetDateTime>`.
func BoundOffsetAddYears(odt rt.OffsetDateTime, n rt.Int) rt.OffsetDateTime {
	return OffsetDateTimePlusYears(odt, n)
}

// BoundOffsetAddMonths is `impl Add<Months, OffsetDateTime>`.
func BoundOffsetAddMonths(odt rt.OffsetDateTime, n rt.Int) rt.OffsetDateTime {
	return OffsetDateTimePlusMonths(odt, n)
}

// --- DateTime --------------------------------------------------------------

// BoundZonedInZone is `DateTime.in_zone_resolve`.
//
// `mode` is a Disambiguation TAG rather than the enum: rt's `Disambiguation` is
// a one-field Go struct, so the host projection would map it onto a Nomi STRUCT while
// std declares an enum. The facade maps the four variants onto these tags, and
// rt's constants are the other half of that pairing.
func BoundZonedInZone(dt rt.NaiveDateTime, zone string, mode int64) rt.CalendarZonedResult {
	return zonedCarrier(ResolveInZone(dt, zone, rt.Disambiguation{Tag: uint8(mode)}))
}

// BoundZonedFromInstantIn is `DateTime.from_instant_in`: an instant of any
// size in an IANA zone.
func BoundZonedFromInstantIn(instantNanos rt.Int, zone string) rt.CalendarZonedResult {
	return zonedCarrier(DateTimeFromInstantIn(instantNanos, zone))
}

// BoundZonedAddNanos is `impl Add<Duration, DateTime>`.
func BoundZonedAddNanos(d rt.DateTime, nanos rt.Int) rt.DateTime {
	return DateTimePlusNanos(d, nanos)
}

// BoundZonedWithZone is `DateTime.with_zone`.
func BoundZonedWithZone(d rt.DateTime, zone string) rt.CalendarZonedResult {
	return zonedCarrier(DateTimeWithZone(d, zone))
}

// BoundZonedParse is `DateTime.parse`, and it also backs the `DateTime"…"`
// typed literal (the RFC 9557 bracket form).
func BoundZonedParse(s string) rt.CalendarZonedResult { return zonedCarrier(ParseZonedDateTime(s)) }

// BoundZonedOffsetNanos is `impl Anchored for DateTime`'s `offset`: the UTC
// offset effective at this moment in this zone, in nanoseconds.
//
// COMPUTED rather than read, which is the difference from OffsetDateTime: a
// zoned value has to ask the tzdb what its offset is at that instant, and a
// fixed-offset value carries the answer in a field the facade reads directly.
func BoundZonedOffsetNanos(d rt.DateTime) int64 { return int64(DateTimeOffset(d)) }

// The ten DateTime civil rungs. Each shifts the WALL reading and re-resolves
// through the zone, which is the civil-versus-physical divergence std/calendar
// exists to make available: `noon Mar 7 in New York + Days(1)` is noon on
// Mar 8 (23 hours of elapsed time) while `+ Duration.hours(24)` is 1pm.

// BoundZonedAddYears is `impl Add<Years, DateTime>`.
func BoundZonedAddYears(d rt.DateTime, n rt.Int) rt.DateTime {
	return DateTimePlusYears(d, n)
}

// BoundZonedAddMonths is `impl Add<Months, DateTime>`.
func BoundZonedAddMonths(d rt.DateTime, n rt.Int) rt.DateTime {
	return DateTimePlusMonths(d, n)
}

// BoundZonedAddDays is `impl Add<Days, DateTime>`, and it is the rung the whole
// civil-versus-physical distinction is visible through.
func BoundZonedAddDays(d rt.DateTime, n rt.Int) rt.DateTime {
	return DateTimePlusDays(d, n)
}

// BoundZonedAddHours is `impl Add<Hours, DateTime>`: a WALL-CLOCK hour, not
// `Duration.hours(n)`.
func BoundZonedAddHours(d rt.DateTime, n rt.Int) rt.DateTime {
	return DateTimePlusHours(d, n)
}

// BoundZonedAddMinutes is `impl Add<Minutes, DateTime>`.
func BoundZonedAddMinutes(d rt.DateTime, n rt.Int) rt.DateTime {
	return DateTimePlusMinutes(d, n)
}

// BoundZonedAddSeconds is `impl Add<Seconds, DateTime>`.
func BoundZonedAddSeconds(d rt.DateTime, n rt.Int) rt.DateTime {
	return DateTimePlusSeconds(d, n)
}

// BoundZonedAddMilliseconds is `impl Add<Milliseconds, DateTime>`.
func BoundZonedAddMilliseconds(d rt.DateTime, n rt.Int) rt.DateTime {
	return DateTimePlusMilliseconds(d, n)
}

// BoundZonedAddMicroseconds is `impl Add<Microseconds, DateTime>`.
func BoundZonedAddMicroseconds(d rt.DateTime, n rt.Int) rt.DateTime {
	return DateTimePlusMicroseconds(d, n)
}

// BoundZonedAddNanoseconds is `impl Add<Nanoseconds, DateTime>`.
func BoundZonedAddNanoseconds(d rt.DateTime, n rt.Int) rt.DateTime {
	return DateTimePlusNanoseconds(d, n)
}
