package stdcalendar

// The FFI-shaped boundary: the functions calendar.nomi's `go` selectors name
// when the semantics beside them cannot be named directly.
//
// # Why a boundary layer exists at all, and how small it had to be
//
// A bound Go function crosses through the FFI projection (`internal/ffitypes`,
// which `internal/hostgen` generates adapters from), so a type the projection
// does not carry cannot appear in a bound signature.
//
// It carries `Int` (int64), `String` (string), `Bool` (bool), `Float`
// (float64) and every struct rt declares — which is most of
// std/calendar's surface, and is why `DateToString`, `DateDaysBetween`,
// `NaiveDateTimeToDate`, `NaiveDateTimeToString`, `OffsetDateTimeToString`,
// `DateTimeToString`, `DateTimeToOffset` and the fourteen
// `OffsetDateTime*`/`DateTime*` component accessors in calendar.go are bound
// straight from the semantics with no wrapper here. A wrapper for those would
// be a function whose only content is a second name.
//
// It does not carry exactly three things, each measured rather than assumed:
//
//  1. `Result<_, E>` for E other than String. A Go `error` crosses only as
//     `Result<_, String>`. std/calendar has
//     twenty-five fallible declarations returning `Result<_, Error>`, a
//     five-variant enum whose classification callers match on. So failure
//     crosses as DATA in one of rt's five carrier structs and the Nomi facade
//     rebuilds the variant.
//
//  2. `Duration` and `Instant`. `internal/ffitypes` pins them to `time.Duration`
//     and `time.Time`, while the semantics use `rt.Duration` and `rt.Instant`.
//     MEASURED: a Go named int64 offered for a declared `Duration` is refused
//     "parameter 1 projects to Int, but Nomi declares Duration". So neither
//     type appears in a bound signature: nanoseconds cross as `Int` and the
//     facade wraps with `Duration.nanoseconds` / `Instant.from_nanos`.
//
//  3. The ten civil periods (`Years` … `Nanoseconds`). `internal/ffitypes` is a
//     closed table with no clause for a Nomi distinct over Int, so
//     `rhs: Years` is refused the same way. Each ladder rung is a NOMI body
//     that destructures its period and calls an `Int`-taking function here —
//     the shape std/calendar already used for `Add<Weeks, Date>`. The
//     semantics keep `rt.Years`, so the civil-period types stay load-bearing
//     one layer in.
//
// # Every declaration bound from Nomi is TOP-LEVEL and module-private
//
// std/random does the same, and here it also avoids two traps. A
// `host fn` carrying a `go` selector inside an impl block is a PARSE ERROR
// (`parseImplBlockBody` leaves `go` unconsumed), and an impl-owned key drops
// its module prefix (hostpair disagreement 1, still open), so a top-level
// declaration is the shape with one unambiguous key and no interaction with
// the interface-instantiation rules.
//
// # Nothing here decides a calendar rule
//
// Every function is a projection: unwrap an Int into an rt newtype, or move an
// `rt.Result`'s two arms into a carrier's three fields. A rule added here would
// be a second encoding of something calendar.go already decides, which is the
// property the whole two-file split exists to keep checkable.

import "github.com/nomi-language/nomi/rt"

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

// --- Date ------------------------------------------------------------------

// BoundDateNew is `Date.new`.
func BoundDateNew(year, month, day int64) rt.CalendarDateResult {
	return dateCarrier(NewDate(year, month, day))
}

// BoundDateParse is `Date.parse`, and it also backs the `Date"…"` typed literal
// through `impl Literal for Date`.
func BoundDateParse(s string) rt.CalendarDateResult { return dateCarrier(ParseDate(s)) }

// BoundDateAddYears is `impl Add<Years, Date>`.
func BoundDateAddYears(d rt.Date, n int64) rt.Date { return DatePlusYears(d, rt.Years(n)) }

// BoundDateAddMonths is `impl Add<Months, Date>`.
func BoundDateAddMonths(d rt.Date, n int64) rt.Date { return DatePlusMonths(d, rt.Months(n)) }

// BoundDateAddDays is `impl Add<Days, Date>`.
func BoundDateAddDays(d rt.Date, n int64) rt.Date { return DatePlusDays(d, rt.Days(n)) }

// --- Time ------------------------------------------------------------------

// BoundTimeParse is `Time.parse`, and it also backs the `Time"…"` typed literal.
func BoundTimeParse(s string) rt.CalendarTimeResult { return timeCarrier(ParseTime(s)) }

// BoundTimeAddNanos is `impl Add<Duration, Time>`: exact elapsed time, wrapping
// around midnight.
func BoundTimeAddNanos(t rt.Time, nanos int64) rt.Time {
	return TimePlusDuration(t, rt.Duration(nanos))
}

// --- NaiveDateTime ---------------------------------------------------------

// BoundNaiveNewExact is `NaiveDateTime.new_exact`: seven components, no
// defaults. The two defaults `NaiveDateTime.new` carries belong to the Nomi
// declaration that documents them, which is the whole point of the split.
func BoundNaiveNewExact(year, month, day, hour, minute, second, nanosecond int64) rt.CalendarNaiveResult {
	return naiveCarrier(NaiveDateTimeFromComponents(year, month, day, hour, minute, second, nanosecond))
}

// BoundNaiveParse is `NaiveDateTime.parse`, and it also backs the
// `NaiveDateTime"…"` typed literal.
func BoundNaiveParse(s string) rt.CalendarNaiveResult { return naiveCarrier(ParseNaiveDateTime(s)) }

// BoundNaiveBetweenNanos is `NaiveDateTime.between` in nanoseconds.
func BoundNaiveBetweenNanos(earlier, later rt.NaiveDateTime) int64 {
	return int64(NaiveDateTimeBetween(earlier, later))
}

// BoundNaiveAddYears is `impl Add<Years, NaiveDateTime>`.
func BoundNaiveAddYears(dt rt.NaiveDateTime, n int64) rt.NaiveDateTime {
	return NaiveDateTimePlusYears(dt, rt.Years(n))
}

// BoundNaiveAddMonths is `impl Add<Months, NaiveDateTime>`.
func BoundNaiveAddMonths(dt rt.NaiveDateTime, n int64) rt.NaiveDateTime {
	return NaiveDateTimePlusMonths(dt, rt.Months(n))
}

// BoundNaiveAddDays is `impl Add<Days, NaiveDateTime>`.
func BoundNaiveAddDays(dt rt.NaiveDateTime, n int64) rt.NaiveDateTime {
	return NaiveDateTimePlusDays(dt, rt.Days(n))
}

// BoundNaiveAddNanos is `impl Add<Duration, NaiveDateTime>`: the EXACT rung.
func BoundNaiveAddNanos(dt rt.NaiveDateTime, nanos int64) rt.NaiveDateTime {
	return NaiveDateTimePlusDuration(dt, rt.Duration(nanos))
}

// --- OffsetDateTime --------------------------------------------------------

// BoundOffsetWithOffset is `OffsetDateTime.with_offset`.
func BoundOffsetWithOffset(dt rt.NaiveDateTime, offsetNanos int64) rt.CalendarOffsetResult {
	return offsetCarrier(OffsetDateTimeWithOffset(dt, rt.Duration(offsetNanos)))
}

// BoundOffsetFromInstant is `OffsetDateTime.from_instant`. Total: every instant
// has a representation at every fixed offset.
func BoundOffsetFromInstant(instantNanos, offsetNanos int64) rt.OffsetDateTime {
	return OffsetDateTimeFromInstant(rt.Instant(instantNanos), rt.Duration(offsetNanos))
}

// BoundOffsetParse is `OffsetDateTime.parse`, and it also backs the
// `OffsetDateTime"…"` typed literal.
func BoundOffsetParse(s string) rt.CalendarOffsetResult {
	return offsetCarrier(ParseOffsetDateTime(s))
}

// BoundOffsetAddYears is `impl Add<Years, OffsetDateTime>`.
func BoundOffsetAddYears(odt rt.OffsetDateTime, n int64) rt.OffsetDateTime {
	return OffsetDateTimePlusYears(odt, rt.Years(n))
}

// BoundOffsetAddMonths is `impl Add<Months, OffsetDateTime>`.
func BoundOffsetAddMonths(odt rt.OffsetDateTime, n int64) rt.OffsetDateTime {
	return OffsetDateTimePlusMonths(odt, rt.Months(n))
}

// --- DateTime --------------------------------------------------------------

// BoundZonedInZone is `DateTime.in_zone_resolve`.
//
// `mode` is a Disambiguation TAG rather than the enum: rt's `Disambiguation` is
// a one-field Go struct, so the FFI would marshal it onto a Nomi STRUCT while
// std declares an enum. The facade maps the four variants onto these tags, and
// rt's constants are the other half of that pairing.
func BoundZonedInZone(dt rt.NaiveDateTime, zone string, mode int64) rt.CalendarZonedResult {
	return zonedCarrier(ResolveInZone(dt, zone, rt.Disambiguation{Tag: uint8(mode)}))
}

// BoundZonedFromInstantIn is `DateTime.from_instant_in`.
func BoundZonedFromInstantIn(instantNanos int64, zone string) rt.CalendarZonedResult {
	return zonedCarrier(DateTimeFromInstantIn(rt.Instant(instantNanos), zone))
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
func BoundZonedAddYears(d rt.DateTime, n int64) rt.DateTime {
	return DateTimePlusYears(d, rt.Years(n))
}

// BoundZonedAddMonths is `impl Add<Months, DateTime>`.
func BoundZonedAddMonths(d rt.DateTime, n int64) rt.DateTime {
	return DateTimePlusMonths(d, rt.Months(n))
}

// BoundZonedAddDays is `impl Add<Days, DateTime>`, and it is the rung the whole
// civil-versus-physical distinction is visible through.
func BoundZonedAddDays(d rt.DateTime, n int64) rt.DateTime {
	return DateTimePlusDays(d, rt.Days(n))
}

// BoundZonedAddHours is `impl Add<Hours, DateTime>`: a WALL-CLOCK hour, not
// `Duration.hours(n)`.
func BoundZonedAddHours(d rt.DateTime, n int64) rt.DateTime {
	return DateTimePlusHours(d, rt.Hours(n))
}

// BoundZonedAddMinutes is `impl Add<Minutes, DateTime>`.
func BoundZonedAddMinutes(d rt.DateTime, n int64) rt.DateTime {
	return DateTimePlusMinutes(d, rt.Minutes(n))
}

// BoundZonedAddSeconds is `impl Add<Seconds, DateTime>`.
func BoundZonedAddSeconds(d rt.DateTime, n int64) rt.DateTime {
	return DateTimePlusSeconds(d, rt.Seconds(n))
}

// BoundZonedAddMilliseconds is `impl Add<Milliseconds, DateTime>`.
func BoundZonedAddMilliseconds(d rt.DateTime, n int64) rt.DateTime {
	return DateTimePlusMilliseconds(d, rt.Milliseconds(n))
}

// BoundZonedAddMicroseconds is `impl Add<Microseconds, DateTime>`.
func BoundZonedAddMicroseconds(d rt.DateTime, n int64) rt.DateTime {
	return DateTimePlusMicroseconds(d, rt.Microseconds(n))
}

// BoundZonedAddNanoseconds is `impl Add<Nanoseconds, DateTime>`.
func BoundZonedAddNanoseconds(d rt.DateTime, n int64) rt.DateTime {
	return DateTimePlusNanoseconds(d, rt.Nanoseconds(n))
}
