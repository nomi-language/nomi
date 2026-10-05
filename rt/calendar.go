package rt

// std/calendar's Go type declarations, and nothing else.
//
// The semantics (DST gap/fold resolution, end-of-month clamping, RFC 9557 and
// ISO parsing, the renderings, the whole civil ladder) live in the first-party
// adapter package `nomi/stdcalendar`, so there is exactly one implementation of
// each rule. `time/tzdata` is a blank import whose registration runs in
// `init`, and it is the adapter's import, so its binary size is paid only by a
// binary that links the adapter.
//
// # Why the types live here and not with the implementation
//
// `rtTypeName` requires `PkgPath() == rtModulePath`, so a `stdStructSpecs` or
// `stdEnumSpecs` row can only name a Go type declared here. Those rows are
// what give a calendar value a representation in the IR builder
// (internal/irbuild). Move a type and the builder loses the representation.
//
// This is the same split std/regex took: `rt.Regex` is the handle and
// `nomi/stdregex` is the implementation. Calendar's boundary is spelled in Int,
// String and these structs, so one adapter function serves every caller with
// no projection between shapes.
//
// # The five carrier structs at the bottom are the price of a typed error
//
// A host function's `Result<_, E>` projects only for E = `String`.
// std/calendar's twenty-five fallible functions
// return `Result<_, calendar.Error>`, a five-variant enum, and collapsing that
// to String would lose the classification a caller matches on
// (`Err(Error.Nonexistent(_))` is a DST gap; `Err(Error.InvalidFormat(_))` is
// a bad string). So the Go side reports the failure as data in a carrier and
// the Nomi facade rebuilds the variant.

// Calendar error tags, in std/calendar.nomi's declaration order. 0 is
// TagInvalid (prelude.go), shared by every enum rt declares.
//
// Written out rather than left implicit at the construction sites because they
// are the one thing a reader has to be able to check against
// `pub enum Error` in std/calendar.nomi by eye. The adapter
// constructs errors with them and the carriers below transport them, so the
// Nomi facade's `error_of` is the only other place the pairing appears.
const (
	TagInvalidFormat uint8 = 1
	TagInvalidValue  uint8 = 2
	TagUnknownZone   uint8 = 3
	TagNonexistent   uint8 = 4
	TagAmbiguous     uint8 = 5
)

// Disambiguation tags, in std/calendar.nomi's declaration order.
const (
	TagCompatible uint8 = 1
	TagEarlier    uint8 = 2
	TagLater      uint8 = 3
	TagReject     uint8 = 4
)

// CalendarError is Nomi's `std/calendar.Error`.
//
// Every one of the five variants carries a single `String`, so the payload gets
// one slot shared across them, which is the builder's own dedup rule for a
// monomorphic enum (internal/irbuild/types.go's slotDef: one slot per distinct
// underlying Go type, reused across variants because only one variant is live
// at a time). Stated here because rt's other tagged structs are the generic
// prelude enums, where dedup is impossible and prelude.go says so.
//
// Fields are exported because the adapter is in another Go package and has
// to write the composite literal and read the payload back.
type CalendarError struct {
	// Tag is 1..5 in declaration order. 0 means never constructed.
	Tag uint8
	// Msg is the payload of whichever variant is live.
	Msg string
}

// Disambiguation is Nomi's `std/calendar.Disambiguation`: how a wall reading
// that falls in a DST gap or fold resolves. Four bare variants, so no payload.
type Disambiguation struct {
	// Tag is 1 for Compatible, 2 for Earlier, 3 for Later, 4 for Reject. 0
	// means never constructed.
	Tag uint8
}

// Date is Nomi's `std/calendar.Date`: a calendar date, no time-of-day, no zone.
//
// Date and Time are two structs rather than one with unused fields because std
// declares them separately with separate `derive Equatable`/`Hashable`/
// `Comparable`. A single struct would make `Date"2026-05-04"` and
// `NaiveDateTime"2026-05-04T00:00:00"` structurally equal, which is a wrong
// answer rather than a wasted field — `derive` generates the comparison over
// the fields that exist.
type Date struct {
	Year  int64
	Month int64
	Day   int64
}

// Time is Nomi's `std/calendar.Time`: a time-of-day, no date, no zone.
type Time struct {
	Hour       int64
	Minute     int64
	Second     int64
	Nanosecond int64
}

// NaiveDateTime is Nomi's `std/calendar.NaiveDateTime`: a wall reading with no
// zone and no offset.
//
// The seven components are the representation — std declares
// `pub opaque struct NaiveDateTime { year: Int; month: Int; ... }` and its
// Equatable/Hashable/Comparable come from `derive`, i.e. structurally over
// these fields. That is correct for this type and wrong for DateTime, and the
// difference is why the two are not one struct: a wall reading is
// its components, and a moment is its instant.
//
// Fields are `int64` because the Nomi fields are `Int`, and the adapter
// reads and writes them directly.
type NaiveDateTime struct {
	Year       int64
	Month      int64
	Day        int64
	Hour       int64
	Minute     int64
	Second     int64
	Nanosecond int64
}

// DateTime is Nomi's `std/calendar.DateTime`: a moment, plus the IANA zone it
// is displayed in.
//
// # InstantNanos is the identity and Zone is not
//
// std/calendar.nomi hand-writes `impl Equatable`, `impl Hashable` and
// `impl Comparable` for this type over `instant_nanos` alone, so the same
// moment rendered in two zones compares equal and hashes the same. Go's `==` on
// this struct would compare Zone too and answer False, which is why Nomi's
// `==` is never Go's here: `==` on a named type routes through the stdlib's
// Equatable impl (internal/irbuild/stdlib.go's stdlibEquality) and refuses when
// there is none. TestCalendarZoneIsNotIdentity pins the property from the
// adapter's side, and internal/irbuild's testdata/calendar_datetime.nomi pins it
// from the language's.
type DateTime struct {
	// InstantNanos is Unix nanoseconds: the moment, and the whole of the
	// identity.
	InstantNanos int64
	// Zone is an IANA name (`America/New_York`). Display metadata.
	Zone string
}

// OffsetDateTime is Nomi's `std/calendar.OffsetDateTime`: a moment, plus the
// fixed UTC offset it is displayed at.
//
// The instant is the identity here for the same reason it is DateTime's —
// std/calendar hand-writes Equatable, Hashable and Comparable over
// `instant_nanos` alone, so `2026-05-04T14:30:00-05:00` and
// `2026-05-04T20:30:00+01:00` are one value and Go's `==` on this struct would
// answer False. Nomi's `==` is never Go's here: it routes through the
// stdlib's Equatable impl (internal/irbuild/stdlib.go's stdlibEquality).
//
// The difference from DateTime is DST, not layout. A fixed offset has no
// transitions, so there is no gap, no fold and no civil-versus-physical
// divergence below a month: `+ Days(1)` and `+ Duration.hours(24)` agree here
// and disagree for DateTime. Only the month and year rungs need a wall-reading
// shift, which is why the adapter carries exactly two.
type OffsetDateTime struct {
	// InstantNanos is Unix nanoseconds: the moment, and the whole of the
	// identity.
	InstantNanos int64
	// OffsetSeconds is the signed UTC offset, capped at ±18h. Display
	// metadata.
	OffsetSeconds int64
}

// std/calendar's ten civil periods — `Years` through `Nanoseconds`.
//
// # These are the plain-distinct half of the opaque family
//
// std declares them `pub type Years Int`, not `pub opaque type`, so they are
// constructible and destructurable from any file that imports std/calendar
// (`Days(n * 7)` appears inside std/calendar itself, and a user writes
// `dt + Hours(3)`). That is a use-site rule the front end enforces and it is
// not a representation: like Duration and Instant, each is a Go defined type
// over int64. internal/irbuild/opaque.go's `declForm` is what lets one table hold
// both clauses while still checking, per row, which one std wrote.
//
// # Why they are declared here
//
// Same reason opaque.go gives for Duration: the builder's kind for a signature
// that mentions them names a Go type, and that type has to live in a package
// everything links.
//
// They appear in no bound Go signature. `internal/ffitypes` is a closed projection
// table — String, Bool, Byte, Int, Float, Bytes, Duration, Instant, Dynamic,
// Unit — with no clause for a Nomi distinct over Int, so `fn add(lhs: Date,
// rhs: Years): Date go pkg.Sym` cannot bind: the boundary reports "parameter 2
// projects to Int, but Nomi declares Years". Each ladder rung is therefore a
// Nomi body that destructures its period (`Years(n)`) and calls an
// `Int`-taking adapter function, which is the shape std/calendar uses for
// `Add<Weeks, Date>`. Two rungs cannot collide on one Go signature
// (`Add<Hours, DateTime>` and `Add<Minutes, DateTime>` would both be
// `(DateTime, int64) DateTime`), because a Nomi body is lowered rather than
// dispatched through stdPick.
//
// # The width is int64 and TestOpaqueGoWidthMatchesTheDeclaredInner checks it
//
// Every row's declared inner is `Int`, which is int64. A narrower Go type would
// pass every test that runs a small value through it and truncate a large one.
//
// # Civil, not physical
//
// Nothing here is a duration in disguise. `Days(1)` advances the day and leaves
// the wall clock reading where it was; `Duration.hours(24)` advances the
// instant by 24 hours. Across a DST transition those land on different wall
// times and neither is a rounding error in the other — see the adapter's
// DateTimeCivilShift. Months and years additionally clamp end-of-month before
// anything else moves, which is why `2011-01-31 + Months(1)` is `2011-02-28`
// and `2012-01-31 + Months(1)` is `2012-02-29`.
type (
	// Years is `std/calendar.Years`: whole calendar years.
	Years int64
	// Months is `std/calendar.Months`: whole calendar months, clamping.
	Months int64
	// Weeks is `std/calendar.Weeks`: whole calendar weeks. std lowers
	// `+ Weeks(n)` to `+ Days(n * 7)` in Nomi; the type is here because a
	// signature names it, which is what an opaqueSpecs row is for.
	Weeks int64
	// Days is `std/calendar.Days`: whole calendar days.
	Days int64
	// Hours is `std/calendar.Hours`: whole civil hours.
	Hours int64
	// Minutes is `std/calendar.Minutes`: whole civil minutes.
	Minutes int64
	// Seconds is `std/calendar.Seconds`: whole civil seconds.
	Seconds int64
	// Milliseconds is `std/calendar.Milliseconds`: whole civil milliseconds.
	Milliseconds int64
	// Microseconds is `std/calendar.Microseconds`: whole civil microseconds.
	Microseconds int64
	// Nanoseconds is `std/calendar.Nanoseconds`: whole civil nanoseconds.
	Nanoseconds int64
)

// --- the fallible-constructor carriers -------------------------------------
//
// One per value type, not one per function, because what varies across the
// twenty-five fallible declarations is the Ok payload and nothing else.
//
// `Kind` is an rt error tag: 0 for success, otherwise TagInvalidFormat…
// TagAmbiguous. `Reason` is the message that variant carries. On success
// `Value` holds the result and Reason is empty; on failure Value is the zero
// value and is never read, which the Nomi facade enforces by branching on Kind
// before touching it.
//
// Kind is int64 rather than uint8 because the Nomi field is `Int`: the FFI
// projects uint8 onto `Byte`, and a Byte-typed tag would make the facade's
// comparison against a literal a different type than it reads.

// CalendarDateResult carries `Result<Date, Error>`.
type CalendarDateResult struct {
	Value  Date
	Kind   int64
	Reason string
}

// CalendarTimeResult carries `Result<Time, Error>`.
type CalendarTimeResult struct {
	Value  Time
	Kind   int64
	Reason string
}

// CalendarNaiveResult carries `Result<NaiveDateTime, Error>`.
type CalendarNaiveResult struct {
	Value  NaiveDateTime
	Kind   int64
	Reason string
}

// CalendarOffsetResult carries `Result<OffsetDateTime, Error>`.
type CalendarOffsetResult struct {
	Value  OffsetDateTime
	Kind   int64
	Reason string
}

// CalendarZonedResult carries `Result<DateTime, Error>`.
type CalendarZonedResult struct {
	Value  DateTime
	Kind   int64
	Reason string
}
