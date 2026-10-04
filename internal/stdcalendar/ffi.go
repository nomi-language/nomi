package stdcalendar

// The bound view of the boundary: the crossings internal/stdlibbindings binds,
// the same ones boundary.go declares, over structs declared in this package.
//
// # Why this file exists, measured rather than assumed
//
// The plan for this adapter was one Go function per crossing, on the ground
// that `Int`, `String` and the calendar structs all project. Two of those
// three hold. The third does not, and the reason is the go/ast PREFLIGHT:
//
//	std/calendar.nomi:200:71: Go function "DateDaysBetween" in package
//	"github.com/nomi-language/nomi/internal/stdcalendar" has unsupported parameter type: unbound named type
//	rt.Date
//
// `internal/ffirun`'s validator resolves a named Go type from
// `readGoPackageSymbols`, which parses the adapter's OWN directory, so a struct
// it can project is one declared as a `*goast.StructType` there. A
// `*goast.SelectorExpr` into an imported package reaches
// `ffitypes.NomiForGoStdlibType` — two rows, `time.Duration` and `time.Time` —
// and is otherwise "unbound". A type ALIAS does not help: `type Date = rt.Date`
// classifies as `goTypeNamed`, falls through to the same selector arm, and
// reports the same thing about `rt.Date`.
//
// # The duplication is one CAST wide, and the Go compiler is the guard
//
// Every type below has the identical field list to its `rt` counterpart, so Go
// treats the two as having identical underlying types and `Date(d)` is a legal
// conversion. Add, remove, rename or retype a field on either side and every
// cast in this file stops compiling — which is a stronger guarantee than a
// layout test, because it cannot be forgotten.
//
// # What each function does and does not do
//
// Cast, delegate, cast back. Not one calendar rule is decided here; every
// answer comes from boundary.go, which comes from calendar.go. That is what
// makes the two Go spellings safe: they are one implementation.

import "github.com/nomi-language/nomi/rt"

// The five calendar records, in the FFI's view. Field-for-field identical to
// `rt.Date`, `rt.Time`, `rt.NaiveDateTime`, `rt.OffsetDateTime` and
// `rt.DateTime` — see those declarations for what each field MEANS and, for
// the two anchored types, why the instant is the identity and the
// offset/zone is not.

// Date is `std/calendar.Date` across the FFI boundary.
type Date struct {
	Year  int64
	Month int64
	Day   int64
}

// Time is `std/calendar.Time` across the FFI boundary.
type Time struct {
	Hour       int64
	Minute     int64
	Second     int64
	Nanosecond int64
}

// NaiveDateTime is `std/calendar.NaiveDateTime` across the FFI boundary.
type NaiveDateTime struct {
	Year       int64
	Month      int64
	Day        int64
	Hour       int64
	Minute     int64
	Second     int64
	Nanosecond int64
}

// OffsetDateTime is `std/calendar.OffsetDateTime` across the FFI boundary.
type OffsetDateTime struct {
	InstantNanos  int64
	OffsetSeconds int64
}

// DateTime is `std/calendar.DateTime` across the FFI boundary.
type DateTime struct {
	InstantNanos int64
	Zone         string
}

// The five fallible-constructor carriers, in the FFI's view. `Kind` is 0 on
// success, otherwise the ordinal of the `Error` variant; `Reason` is that
// variant's payload. rt's declarations say why a carrier exists at all.

// DateAttempt carries `Result<Date, Error>`.
type DateAttempt struct {
	Value  Date
	Kind   int64
	Reason string
}

// TimeAttempt carries `Result<Time, Error>`.
type TimeAttempt struct {
	Value  Time
	Kind   int64
	Reason string
}

// NaiveAttempt carries `Result<NaiveDateTime, Error>`.
type NaiveAttempt struct {
	Value  NaiveDateTime
	Kind   int64
	Reason string
}

// OffsetAttempt carries `Result<OffsetDateTime, Error>`.
type OffsetAttempt struct {
	Value  OffsetDateTime
	Kind   int64
	Reason string
}

// ZonedAttempt carries `Result<DateTime, Error>`.
type ZonedAttempt struct {
	Value  DateTime
	Kind   int64
	Reason string
}

// --- renderings and projections --------------------------------------------

// FFIDateToString is `impl Display for Date`.
func FFIDateToString(d Date) string { return DateToString(rt.Date(d)) }

// FFITimeToString is `impl Display for Time`.
func FFITimeToString(t Time) string { return TimeToString(rt.Time(t)) }

// FFINaiveDateTimeToString is `impl Display for NaiveDateTime`.
func FFINaiveDateTimeToString(dt NaiveDateTime) string {
	return NaiveDateTimeToString(rt.NaiveDateTime(dt))
}

// FFIOffsetDateTimeToString is `impl Display for OffsetDateTime`.
func FFIOffsetDateTimeToString(odt OffsetDateTime) string {
	return OffsetDateTimeToString(rt.OffsetDateTime(odt))
}

// FFIDateTimeToString is `impl Display for DateTime`: the RFC 9557 rendering.
func FFIDateTimeToString(d DateTime) string { return DateTimeToString(rt.DateTime(d)) }

// FFIDateDaysBetween is `Date.days_between`.
func FFIDateDaysBetween(earlier, later Date) int64 {
	return DateDaysBetween(rt.Date(earlier), rt.Date(later))
}

// FFINaiveDateTimeToDate is `NaiveDateTime.to_date`.
func FFINaiveDateTimeToDate(dt NaiveDateTime) Date {
	return Date(NaiveDateTimeToDate(rt.NaiveDateTime(dt)))
}

// FFINaiveDateTimeToTime is `NaiveDateTime.to_time`.
func FFINaiveDateTimeToTime(dt NaiveDateTime) Time {
	return Time(NaiveDateTimeToTime(rt.NaiveDateTime(dt)))
}

// FFIDateTimeToOffset is `DateTime.to_offset`: freeze the zone's offset at this
// instant, discarding the zone.
func FFIDateTimeToOffset(d DateTime) OffsetDateTime {
	return OffsetDateTime(DateTimeToOffset(rt.DateTime(d)))
}

// --- the fourteen anchored component accessors -----------------------------

func FFIOffsetDateTimeYear(odt OffsetDateTime) int64 {
	return OffsetDateTimeYear(rt.OffsetDateTime(odt))
}

func FFIOffsetDateTimeMonth(odt OffsetDateTime) int64 {
	return OffsetDateTimeMonth(rt.OffsetDateTime(odt))
}

func FFIOffsetDateTimeDay(odt OffsetDateTime) int64 {
	return OffsetDateTimeDay(rt.OffsetDateTime(odt))
}

func FFIOffsetDateTimeHour(odt OffsetDateTime) int64 {
	return OffsetDateTimeHour(rt.OffsetDateTime(odt))
}

func FFIOffsetDateTimeMinute(odt OffsetDateTime) int64 {
	return OffsetDateTimeMinute(rt.OffsetDateTime(odt))
}

func FFIOffsetDateTimeSecond(odt OffsetDateTime) int64 {
	return OffsetDateTimeSecond(rt.OffsetDateTime(odt))
}

func FFIOffsetDateTimeNanosecond(odt OffsetDateTime) int64 {
	return OffsetDateTimeNanosecond(rt.OffsetDateTime(odt))
}

func FFIDateTimeYear(d DateTime) int64 { return DateTimeYear(rt.DateTime(d)) }

func FFIDateTimeMonth(d DateTime) int64 { return DateTimeMonth(rt.DateTime(d)) }

func FFIDateTimeDay(d DateTime) int64 { return DateTimeDay(rt.DateTime(d)) }

func FFIDateTimeHour(d DateTime) int64 { return DateTimeHour(rt.DateTime(d)) }

func FFIDateTimeMinute(d DateTime) int64 { return DateTimeMinute(rt.DateTime(d)) }

func FFIDateTimeSecond(d DateTime) int64 { return DateTimeSecond(rt.DateTime(d)) }

func FFIDateTimeNanosecond(d DateTime) int64 { return DateTimeNanosecond(rt.DateTime(d)) }

// The five carrier projections. A whole-struct cast does not reach here: Go
// requires identical FIELD types, and a carrier's `Value` is `rt.Date` on one
// side and `Date` on the other — convertible, not identical. So each carrier
// names its three fields, which is also where a reader can see that `Value` is
// only meaningful when `Kind` is 0.

func dateAttempt(r rt.CalendarDateResult) DateAttempt {
	return DateAttempt{Value: Date(r.Value), Kind: r.Kind, Reason: r.Reason}
}

func timeAttempt(r rt.CalendarTimeResult) TimeAttempt {
	return TimeAttempt{Value: Time(r.Value), Kind: r.Kind, Reason: r.Reason}
}

func naiveAttempt(r rt.CalendarNaiveResult) NaiveAttempt {
	return NaiveAttempt{Value: NaiveDateTime(r.Value), Kind: r.Kind, Reason: r.Reason}
}

func offsetAttempt(r rt.CalendarOffsetResult) OffsetAttempt {
	return OffsetAttempt{Value: OffsetDateTime(r.Value), Kind: r.Kind, Reason: r.Reason}
}

func zonedAttempt(r rt.CalendarZonedResult) ZonedAttempt {
	return ZonedAttempt{Value: DateTime(r.Value), Kind: r.Kind, Reason: r.Reason}
}

// --- the fallible constructors ---------------------------------------------

// FFIDateNew is `Date.new`.
func FFIDateNew(year, month, day int64) DateAttempt {
	return dateAttempt(BoundDateNew(year, month, day))
}

// FFIDateParse is `Date.parse`, and it also backs the `Date"…"` typed literal.
func FFIDateParse(s string) DateAttempt { return dateAttempt(BoundDateParse(s)) }

// FFITimeParse is `Time.parse`, and it also backs the `Time"…"` typed literal.
func FFITimeParse(s string) TimeAttempt { return timeAttempt(BoundTimeParse(s)) }

// FFINaiveNewExact is `NaiveDateTime.new_exact`: seven components, no defaults.
func FFINaiveNewExact(year, month, day, hour, minute, second, nanosecond int64) NaiveAttempt {
	return naiveAttempt(BoundNaiveNewExact(year, month, day, hour, minute, second, nanosecond))
}

// FFINaiveParse is `NaiveDateTime.parse`, and it also backs the
// `NaiveDateTime"…"` typed literal.
func FFINaiveParse(s string) NaiveAttempt { return naiveAttempt(BoundNaiveParse(s)) }

// FFIOffsetWithOffset is `OffsetDateTime.with_offset`.
func FFIOffsetWithOffset(dt NaiveDateTime, offsetNanos int64) OffsetAttempt {
	return offsetAttempt(BoundOffsetWithOffset(rt.NaiveDateTime(dt), offsetNanos))
}

// FFIOffsetParse is `OffsetDateTime.parse`, and it also backs the
// `OffsetDateTime"…"` typed literal.
func FFIOffsetParse(s string) OffsetAttempt { return offsetAttempt(BoundOffsetParse(s)) }

// FFIZonedInZone is `DateTime.in_zone_resolve`. `mode` is a Disambiguation
// ordinal — see BoundZonedInZone for why the enum does not cross as itself.
func FFIZonedInZone(dt NaiveDateTime, zone string, mode int64) ZonedAttempt {
	return zonedAttempt(BoundZonedInZone(rt.NaiveDateTime(dt), zone, mode))
}

// FFIZonedFromInstantIn is `DateTime.from_instant_in`.
func FFIZonedFromInstantIn(instantNanos int64, zone string) ZonedAttempt {
	return zonedAttempt(BoundZonedFromInstantIn(instantNanos, zone))
}

// FFIZonedWithZone is `DateTime.with_zone`.
func FFIZonedWithZone(d DateTime, zone string) ZonedAttempt {
	return zonedAttempt(BoundZonedWithZone(rt.DateTime(d), zone))
}

// FFIZonedParse is `DateTime.parse`, and it also backs the `DateTime"…"` typed
// literal (the RFC 9557 bracket form).
func FFIZonedParse(s string) ZonedAttempt { return zonedAttempt(BoundZonedParse(s)) }

// --- the nanosecond crossings ----------------------------------------------

// FFIOffsetFromInstant is `OffsetDateTime.from_instant`. Total.
func FFIOffsetFromInstant(instantNanos, offsetNanos int64) OffsetDateTime {
	return OffsetDateTime(BoundOffsetFromInstant(instantNanos, offsetNanos))
}

// FFINaiveBetweenNanos is `NaiveDateTime.between` in nanoseconds.
func FFINaiveBetweenNanos(earlier, later NaiveDateTime) int64 {
	return BoundNaiveBetweenNanos(rt.NaiveDateTime(earlier), rt.NaiveDateTime(later))
}

// FFIZonedOffsetNanos is `impl Anchored for DateTime`'s `offset`: a tzdb read.
func FFIZonedOffsetNanos(d DateTime) int64 { return BoundZonedOffsetNanos(rt.DateTime(d)) }

// --- the civil ladder ------------------------------------------------------

func FFIDateAddYears(d Date, n int64) Date { return Date(BoundDateAddYears(rt.Date(d), n)) }

func FFIDateAddMonths(d Date, n int64) Date { return Date(BoundDateAddMonths(rt.Date(d), n)) }

func FFIDateAddDays(d Date, n int64) Date { return Date(BoundDateAddDays(rt.Date(d), n)) }

func FFITimeAddNanos(t Time, nanos int64) Time {
	return Time(BoundTimeAddNanos(rt.Time(t), nanos))
}

func FFINaiveAddYears(dt NaiveDateTime, n int64) NaiveDateTime {
	return NaiveDateTime(BoundNaiveAddYears(rt.NaiveDateTime(dt), n))
}

func FFINaiveAddMonths(dt NaiveDateTime, n int64) NaiveDateTime {
	return NaiveDateTime(BoundNaiveAddMonths(rt.NaiveDateTime(dt), n))
}

func FFINaiveAddDays(dt NaiveDateTime, n int64) NaiveDateTime {
	return NaiveDateTime(BoundNaiveAddDays(rt.NaiveDateTime(dt), n))
}

func FFINaiveAddNanos(dt NaiveDateTime, nanos int64) NaiveDateTime {
	return NaiveDateTime(BoundNaiveAddNanos(rt.NaiveDateTime(dt), nanos))
}

func FFIOffsetAddYears(odt OffsetDateTime, n int64) OffsetDateTime {
	return OffsetDateTime(BoundOffsetAddYears(rt.OffsetDateTime(odt), n))
}

func FFIOffsetAddMonths(odt OffsetDateTime, n int64) OffsetDateTime {
	return OffsetDateTime(BoundOffsetAddMonths(rt.OffsetDateTime(odt), n))
}

func FFIZonedAddYears(d DateTime, n int64) DateTime {
	return DateTime(BoundZonedAddYears(rt.DateTime(d), n))
}

func FFIZonedAddMonths(d DateTime, n int64) DateTime {
	return DateTime(BoundZonedAddMonths(rt.DateTime(d), n))
}

func FFIZonedAddDays(d DateTime, n int64) DateTime {
	return DateTime(BoundZonedAddDays(rt.DateTime(d), n))
}

func FFIZonedAddHours(d DateTime, n int64) DateTime {
	return DateTime(BoundZonedAddHours(rt.DateTime(d), n))
}

func FFIZonedAddMinutes(d DateTime, n int64) DateTime {
	return DateTime(BoundZonedAddMinutes(rt.DateTime(d), n))
}

func FFIZonedAddSeconds(d DateTime, n int64) DateTime {
	return DateTime(BoundZonedAddSeconds(rt.DateTime(d), n))
}

func FFIZonedAddMilliseconds(d DateTime, n int64) DateTime {
	return DateTime(BoundZonedAddMilliseconds(rt.DateTime(d), n))
}

func FFIZonedAddMicroseconds(d DateTime, n int64) DateTime {
	return DateTime(BoundZonedAddMicroseconds(rt.DateTime(d), n))
}

func FFIZonedAddNanoseconds(d DateTime, n int64) DateTime {
	return DateTime(BoundZonedAddNanoseconds(rt.DateTime(d), n))
}
