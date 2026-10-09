// Package stdlibbindings is the one hand-maintained table of stdlib host
// functions: which Go function implements each std `host fn`. It holds two
// lists.
//
//   - Funcs: the three stdlib modules whose implementation is Go
//     (std/calendar, std/random and std/regex), in their FFI-shaped surface. The
//     VM calls them through generated adapters (internal/stdlibadapters).
//   - RtFuncs: the rt functions the VM reaches for std's scalar, text, byte,
//     JSON, Decimal and concurrency primitives. The VM calls every row
//     through a generated adapter, and internal/irbuild's IR builder retains a
//     std `host fn` call only when its key has a row here.
//
// It exposes descriptors rather than performing registration, so it imports no
// engine: the adapter generator reads them, and every machine binds the
// generated adapters on its first host crossing.
//
// # Adding a host function
//
// Write the `host fn` in std/*.nomi, add a row (the key the declaration
// answers to, and the Go function value), run
// `go generate ./internal/stdlibadapters`, and run
// `go test ./internal/hostpair ./internal/stdlibadapters`. A row whose Go
// signature does not match the declaration fails generation by name.
//
// # Why this is hand-written, and what stops it drifting
//
// Every std declaration is a `host fn` or a `host type`, so no Nomi source
// names a Go symbol. The other candidate sources for the symbol half do not
// work; internal/hostpair's symbolsource_test.go checks each:
//
//   - Pairing by signature against the adapter package is not a function:
//     about half of its FFI-shaped exports share a signature with another,
//     and nine `func(d DateTime, n int64) DateTime` adders are
//     indistinguishable.
//   - Computing the Go name from the Nomi name misses about a third of the
//     rows, and the exceptions are not typos: `random.os_state` is
//     `FFIFromOS`, `Regex.compile` drops its receiver, and every `_raw` or
//     `_state` row drops its suffix.
//
// It holds function values rather than names, for this reason. A pair of strings ("this Nomi name" -> "call
// go_regex.Compile") is a lookup decided by a name: a rename on
// the Go side gives a name that resolves to nothing, silently. Holding the
// function value removes the failure mode: a renamed or deleted symbol is a Go
// compile error in this file, and the signature is reflected over rather than
// restated. So the only hand-written thing is the Nomi key.
//
// # A wrong or missing key is quiet, so a test checks the keys
//
// Nothing at load time checks a std `host fn` against this table: a stdlib
// declaration with no row is simply not retained, so a program reaching it is
// BLOCKED, far from the omission and without naming the missing row. The
// function values prevent silent resolution on the Go side. The Nomi key is
// the only half that remains derivable from source, so that is where the
// check is:
// internal/hostpair.TestEveryHostDeclarationInAGoBackedModuleIsRegistered
// derives every key from `std/*.nomi` and requires this table to be a
// bijection with them, reporting a missing key, a misspelling (both halves)
// and an orphaned row.
//
// # What would make the symbol derivable, and why it is not worth it
//
// Two things would: a `go alias.Symbol` selector back in the facade, or a
// marker comment on each Go function naming its Nomi key. The first is the
// arrangement this package deliberately avoids — a `gopkg` handle in a std facade
// makes the module a discoverable co-located adapter again, and every program
// importing it then needs `go` on PATH (internal/ffirun's
// hostkeyword_toolchain_test.go). The second keeps `host fn` and the
// toolchain-free path, but the derived symbol would be a string in a comment:
// a Go rename invalidates it silently and nothing catches it, which is the
// exact failure the `Fn any` field above exists to remove. So the reason this
// table holds function values is also the reason the last route to generating
// it is closed.
//
// A generator that emitted the keys and left `Fn:` to a human would be
// strictly worse than the test: same errors caught, plus a build step and a
// generated file to keep in sync.
package stdlibbindings

import (
	"github.com/nomi-language/nomi/internal/stdcalendar"
	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/internal/stdrandom"
	"github.com/nomi-language/nomi/internal/stdregex"
	"github.com/nomi-language/nomi/internal/stdstrings"
	"github.com/nomi-language/nomi/rt"
)

// Binding pairs a Nomi `host fn` name with the Go function implementing
// it. Fn is a plain func value; the adapter generator reflects over its
// signature.
type Binding struct {
	Name string
	Fn   any
	// PanicsPropagate lets a panic in Fn unwind through the generated adapter
	// to the VM, instead of the adapter turning it into its error with the
	// text `<name>: panic: <value>` (hostadapt.Recover). Set on every RtFuncs
	// row: an rt trap (*rt.Error) is a Nomi fault the VM reports with rt's
	// text, and a cancellation unwind (rt.TimerSleep in a cancelled task) must
	// keep unwinding the task. Unset on Funcs rows, whose adapters recover.
	PanicsPropagate bool
}

// TypeBinding pairs a Nomi `host type` name with a typed nil of the Go
// type it opaquely wraps. Only the prototype's reflect.Type is read, never the
// (nil) value itself.
type TypeBinding struct {
	Name      string
	Prototype any
}

// Types returns the host-type bindings in deterministic order. Register these
// before Funcs: a signature mentioning an opaque handle only projects once the
// handle's Go type is known.
func Types() []TypeBinding {
	return []TypeBinding{
		{Name: "regex.Regex", Prototype: (*stdregex.Regex)(nil)},
	}
}

// Funcs returns the host-function bindings in deterministic order.
//
// Every symbol below is the FFI-shaped half of its package. The rt-shaped half
// beside it — `stdregex.Compile` against `stdregex.FFICompile` — is not
// interchangeable with it in either direction: measured, 13 of 14 rt-shaped symbols
// either error at this boundary or hand Nomi a different value (a struct where
// the declaration says a tuple, `{tag, ok, err}` where it says a Result). The
// VM's generated adapters call the FFI-shaped half.
func Funcs() []Binding {
	return []Binding{
		{Name: "Date.days_between", Fn: stdcalendar.FFIDateDaysBetween},
		{Name: "Date.to_string", Fn: stdcalendar.FFIDateToString},
		{Name: "DateTime.day", Fn: stdcalendar.FFIDateTimeDay},
		{Name: "DateTime.hour", Fn: stdcalendar.FFIDateTimeHour},
		{Name: "DateTime.minute", Fn: stdcalendar.FFIDateTimeMinute},
		{Name: "DateTime.month", Fn: stdcalendar.FFIDateTimeMonth},
		{Name: "DateTime.nanosecond", Fn: stdcalendar.FFIDateTimeNanosecond},
		{Name: "DateTime.second", Fn: stdcalendar.FFIDateTimeSecond},
		{Name: "DateTime.to_offset", Fn: stdcalendar.FFIDateTimeToOffset},
		{Name: "DateTime.to_string", Fn: stdcalendar.FFIDateTimeToString},
		{Name: "DateTime.year", Fn: stdcalendar.FFIDateTimeYear},
		{Name: "NaiveDateTime.to_date", Fn: stdcalendar.FFINaiveDateTimeToDate},
		{Name: "NaiveDateTime.to_string", Fn: stdcalendar.FFINaiveDateTimeToString},
		{Name: "NaiveDateTime.to_time", Fn: stdcalendar.FFINaiveDateTimeToTime},
		{Name: "OffsetDateTime.day", Fn: stdcalendar.FFIOffsetDateTimeDay},
		{Name: "OffsetDateTime.hour", Fn: stdcalendar.FFIOffsetDateTimeHour},
		{Name: "OffsetDateTime.minute", Fn: stdcalendar.FFIOffsetDateTimeMinute},
		{Name: "OffsetDateTime.month", Fn: stdcalendar.FFIOffsetDateTimeMonth},
		{Name: "OffsetDateTime.nanosecond", Fn: stdcalendar.FFIOffsetDateTimeNanosecond},
		{Name: "OffsetDateTime.second", Fn: stdcalendar.FFIOffsetDateTimeSecond},
		{Name: "OffsetDateTime.to_string", Fn: stdcalendar.FFIOffsetDateTimeToString},
		{Name: "OffsetDateTime.year", Fn: stdcalendar.FFIOffsetDateTimeYear},
		{Name: "Time.to_string", Fn: stdcalendar.FFITimeToString},
		{Name: "calendar.date_add_days", Fn: stdcalendar.FFIDateAddDays},
		{Name: "calendar.date_add_months", Fn: stdcalendar.FFIDateAddMonths},
		{Name: "calendar.date_add_years", Fn: stdcalendar.FFIDateAddYears},
		{Name: "calendar.date_new_raw", Fn: stdcalendar.FFIDateNew},
		{Name: "calendar.date_parse_raw", Fn: stdcalendar.FFIDateParse},
		{Name: "calendar.naive_add_days", Fn: stdcalendar.FFINaiveAddDays},
		{Name: "calendar.naive_add_months", Fn: stdcalendar.FFINaiveAddMonths},
		{Name: "calendar.naive_add_nanos", Fn: stdcalendar.FFINaiveAddNanos},
		{Name: "calendar.naive_add_years", Fn: stdcalendar.FFINaiveAddYears},
		{Name: "calendar.naive_between_nanos", Fn: stdcalendar.FFINaiveBetweenNanos},
		{Name: "calendar.naive_new_exact_raw", Fn: stdcalendar.FFINaiveNewExact},
		{Name: "calendar.naive_parse_raw", Fn: stdcalendar.FFINaiveParse},
		{Name: "calendar.offset_add_months", Fn: stdcalendar.FFIOffsetAddMonths},
		{Name: "calendar.offset_add_years", Fn: stdcalendar.FFIOffsetAddYears},
		{Name: "calendar.offset_from_instant_nanos", Fn: stdcalendar.FFIOffsetFromInstant},
		{Name: "calendar.offset_parse_raw", Fn: stdcalendar.FFIOffsetParse},
		{Name: "calendar.offset_with_offset_raw", Fn: stdcalendar.FFIOffsetWithOffset},
		{Name: "calendar.time_add_nanos", Fn: stdcalendar.FFITimeAddNanos},
		{Name: "calendar.time_parse_raw", Fn: stdcalendar.FFITimeParse},
		{Name: "calendar.zoned_add_days", Fn: stdcalendar.FFIZonedAddDays},
		{Name: "calendar.zoned_add_hours", Fn: stdcalendar.FFIZonedAddHours},
		{Name: "calendar.zoned_add_microseconds", Fn: stdcalendar.FFIZonedAddMicroseconds},
		{Name: "calendar.zoned_add_milliseconds", Fn: stdcalendar.FFIZonedAddMilliseconds},
		{Name: "calendar.zoned_add_minutes", Fn: stdcalendar.FFIZonedAddMinutes},
		{Name: "calendar.zoned_add_months", Fn: stdcalendar.FFIZonedAddMonths},
		{Name: "calendar.zoned_add_nanoseconds", Fn: stdcalendar.FFIZonedAddNanoseconds},
		{Name: "calendar.zoned_add_seconds", Fn: stdcalendar.FFIZonedAddSeconds},
		{Name: "calendar.zoned_add_years", Fn: stdcalendar.FFIZonedAddYears},
		{Name: "calendar.zoned_from_instant_in_raw", Fn: stdcalendar.FFIZonedFromInstantIn},
		{Name: "calendar.zoned_in_zone_raw", Fn: stdcalendar.FFIZonedInZone},
		{Name: "calendar.zoned_offset_nanos", Fn: stdcalendar.FFIZonedOffsetNanos},
		{Name: "calendar.zoned_parse_raw", Fn: stdcalendar.FFIZonedParse},
		{Name: "calendar.zoned_with_zone_raw", Fn: stdcalendar.FFIZonedWithZone},
		{Name: "random.below_state", Fn: stdrandom.FFIBelow},
		{Name: "random.os_state", Fn: stdrandom.FFIFromOS},
		{Name: "random.unit_float_state", Fn: stdrandom.FFIUnitFloat},
		{Name: "Regex.compile", Fn: stdregex.FFICompile},
		{Name: "Regex.contained_in?", Fn: stdregex.FFIMatch},
		{Name: "Regex.find", Fn: stdregex.FFIFind},
		{Name: "Regex.find_all_in", Fn: stdregex.FFIFindAll},
		{Name: "Regex.pattern", Fn: stdregex.FFIPattern},
		{Name: "Regex.prefix_of?", Fn: stdregex.FFIPrefixOf},
		{Name: "Regex.replace_all", Fn: stdregex.FFIReplaceAll},
		{Name: "Regex.replace_in", Fn: stdregex.FFIReplaceLiteral},
		{Name: "Regex.split_in", Fn: stdregex.FFISplit},
		{Name: "Regex.suffix_of?", Fn: stdregex.FFISuffixOf},
	}
}

// RtFuncs returns the rt-shaped host functions the VM calls, in deterministic
// order. Every row propagates panics (see Binding.PanicsPropagate).
func RtFuncs() []Binding {
	rows := []Binding{
		{Name: "strings.String.contained_in?", Fn: rt.StringContainedIn},
		{Name: "strings.String.prefix_of?", Fn: rt.StringPrefixOf},
		{Name: "strings.String.suffix_of?", Fn: rt.StringSuffixOf},
		{Name: "strings.String.find_all_in", Fn: rt.StringFindAllIn},
		{Name: "strings.String.to_upper", Fn: rt.StringToUpper},
		{Name: "strings.String.to_lower", Fn: rt.StringToLower},
		{Name: "strings.String.replace_in", Fn: rt.StringReplaceIn},
		{Name: "strings.String.trim", Fn: rt.StringTrim},
		{Name: "strings.String.hash", Fn: rt.StringHash},
		{Name: "strings.string_compare", Fn: rt.StringCompare},
		{Name: "strings.String.to_int", Fn: rt.StringToInt},
		{Name: "strings.String.split_in", Fn: rt.StringSplitIn},
		{Name: "strings.String.words", Fn: rt.StringWords},
		{Name: "strings.String.lines", Fn: rt.StringLines},
		{Name: "strings.String.length", Fn: rt.StringLength},
		{Name: "strings.String.slice", Fn: rt.StringSlice},
		{Name: "strings.String.reverse", Fn: rt.StringReverse},
		{Name: "bytes.Byte.from_int", Fn: rt.ByteFromInt},
		{Name: "bytes.Byte.to_int", Fn: rt.ByteToInt},
		{Name: "bytes.Bytes.length", Fn: rt.BytesLength},
		{Name: "bytes.Bytes.at", Fn: rt.BytesAt},
		{Name: "bytes.Bytes.slice", Fn: rt.BytesSlice},
		{Name: "bytes.Bytes.concat", Fn: rt.BytesConcat},
		{Name: "bytes.Bytes.to_string", Fn: rt.BytesToString},
		{Name: "bytes.Bytes.hash", Fn: rt.BytesHash},
		{Name: "strings.String.to_bytes", Fn: rt.StringToBytes},
		{Name: "int.Int.to_string", Fn: rt.FormatInt},
		{Name: "int.Int.to_float", Fn: rt.IntToFloat},
		{Name: "int.Int.wrapping_add", Fn: rt.WrapAddInt},
		{Name: "int.Int.wrapping_sub", Fn: rt.WrapSubInt},
		{Name: "int.Int.wrapping_mul", Fn: rt.WrapMulInt},
		{Name: "int.Int.bitwise_and", Fn: rt.IntBitAnd},
		{Name: "int.Int.bitwise_or", Fn: rt.IntBitOr},
		{Name: "int.Int.bitwise_xor", Fn: rt.IntBitXor},
		{Name: "int.Int.bitwise_not", Fn: rt.IntBitNot},
		{Name: "float.Float.to_string", Fn: rt.FormatFloat},
		{Name: "float.Float.nan", Fn: rt.FloatNaN},
		{Name: "float.Float.positive_infinity", Fn: rt.FloatPositiveInfinity},
		{Name: "float.Float.negative_infinity", Fn: rt.FloatNegativeInfinity},
		{Name: "float.Float.nan?", Fn: rt.FloatIsNaN},
		{Name: "float.Float.round", Fn: rt.FloatRound},
		{Name: "float.Float.floor", Fn: rt.FloatFloor},
		{Name: "float.Float.ceil", Fn: rt.FloatCeil},
		{Name: "float.Float.trunc", Fn: rt.FloatTrunc},
		{Name: "float.Float.to_int", Fn: rt.FloatToInt},
		{Name: "float.float_bits", Fn: rt.FloatBits},
		{Name: "decimal.Decimal.to_string", Fn: rt.DecimalToString},
		{Name: "decimal.Decimal.equal?", Fn: rt.EqDecimal},
		{Name: "decimal.Decimal.hash", Fn: rt.DecimalHash},
		{Name: "decimal.Decimal.compare", Fn: rt.DecimalCompare},
		{Name: "duration.Duration.to_string", Fn: rt.DurationToString},
		{Name: "instant.Instant.now", Fn: rt.InstantNow},
		{Name: "codepoints.Codepoint.to_string", Fn: rt.CodepointToString},
		{Name: "strings.String.to_codepoints", Fn: rt.StringToCodepoints},
		{Name: "context.Context.with_timeout", Fn: rt.ContextWithTimeout},
		{Name: "context.Context.root", Fn: rt.ContextRoot},
		{Name: "json.Json.decode", Fn: rt.JsonDecode},
		{Name: "json.Json.encode", Fn: rt.JsonEncode},
		{Name: "timer.sleep", Fn: rt.TimerSleep},
		{Name: "supervisors.Supervisor.new_exact", Fn: rt.SupervisorNewExact},
		{Name: "supervisors.Supervisor.flush_bounded", Fn: rt.SupervisorFlushBounded},
		{Name: "assertions.AssertionFailure.format", Fn: rt.FormatNomiAssertionFailure},
		{Name: "decimal.Decimal.from_int", Fn: rt.DecimalFromInt},
		{Name: "decimal.Decimal.from_string", Fn: rt.DecimalFromString},
		{Name: "decimal.Decimal.to_int", Fn: rt.DecimalToInt},
		{Name: "decimal.Decimal.to_float", Fn: rt.DecimalToFloat},
		{Name: "decimal.Decimal.from_float", Fn: rt.DecimalFromFloat},
		{Name: "decimal.Decimal.divide", Fn: rt.DecimalDivide},
		{Name: "decimal.Decimal.round", Fn: rt.DecimalRound},
		{Name: "decimal.Decimal.normalize", Fn: rt.DecimalNormalize},
		{Name: "decimal.Decimal.scale", Fn: rt.DecimalScale},
		{Name: "strings.String.repeat", Fn: rt.StringRepeat},
		{Name: "int.Int.shift_left", Fn: rt.IntShiftLeft},
		{Name: "int.Int.shift_right", Fn: rt.IntShiftRight},
		{Name: "context.Context.deadline", Fn: rt.ContextDeadline},
		{Name: "context.Context.deadline_remaining", Fn: rt.ContextDeadlineRemaining},
		{Name: "context.Context.with_deadline", Fn: rt.ContextWithDeadline},
		{Name: "bytes.Bytes.to_list", Fn: rt.BytesToList},
		{Name: "bytes.Bytes.from_list", Fn: rt.BytesFromList},
		{Name: "io.read_file", Fn: stdio.ReadFile},
		{Name: "io.write_file", Fn: stdio.WriteFile},
		{Name: "strings.String.normalize", Fn: stdstrings.Normalize},
		{Name: "dynamic.Dynamic.field", Fn: rt.DynamicField},
		{Name: "dynamic.Dynamic.index", Fn: rt.DynamicIndex},
		{Name: "dynamic.Dynamic.path", Fn: rt.DynamicPath},
		{Name: "dynamic.Dynamic.as_string", Fn: rt.DynamicAsString},
		{Name: "dynamic.Dynamic.as_int", Fn: rt.DynamicAsInt},
		{Name: "dynamic.Dynamic.as_float", Fn: rt.DynamicAsFloat},
		{Name: "dynamic.Dynamic.as_bool", Fn: rt.DynamicAsBool},
		{Name: "dynamic.Dynamic.as_list", Fn: rt.DynamicAsList},
		{Name: "dynamic.Dynamic.as_dict", Fn: rt.DynamicAsDict},
		{Name: "dynamic.Dynamic.null?", Fn: rt.DynamicIsNull},
		{Name: "dynamic.Dynamic.has?", Fn: rt.DynamicHas},
		{Name: "dynamic.Dynamic.inspect", Fn: rt.DynamicInspect},
		{Name: "json.Json.to_dynamic", Fn: rt.JsonToDynamic},
		{Name: "io.read_line", Fn: rt.ReadLine},
	}
	for i := range rows {
		rows[i].PanicsPropagate = true
	}
	return rows
}
