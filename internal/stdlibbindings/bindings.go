// Package stdlibbindings is the one hand-maintained table of stdlib host
// functions: which Go function implements each std `host fn`. It holds two
// lists.
//
//   - Funcs: the stdlib modules whose implementation is Go (std/calendar,
//     std/locale, std/plural, std/random and std/regex), in their FFI-shaped surface,
//     std/currency's code checks and std/money_format's money formatting,
//     plus std/strings' `capture_groups` and `capture_groups_all`, behind
//     `String.captures`, which take a `Regex` handle and so cannot be
//     RtFuncs rows, and the
//     locale-aware formatting and parsing behind `Int.format`,
//     `Float.format`, `Decimal.format` and `String.join_list`
//     (internal/stdformat), which read the program's CLDR data. The VM calls
//     them through generated adapters (internal/stdlibadapters).
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
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdcalendar"
	"github.com/nomi-language/nomi/internal/stdformat"
	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/stdmessageformat"
	"github.com/nomi-language/nomi/internal/stdplural"
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
	// keep unwinding the task. Set too on the Funcs rows whose job is to trap
	// (std/random's range checks); unset on the other Funcs rows, whose
	// adapters recover.
	PanicsPropagate bool
	// Class is the capability the function needs from the target it runs on
	// (ClassFiles), or "" for one every target has. `nomi build` refuses a
	// target that lacks a class the program reaches (Classes).
	Class string
}

// ClassFiles is the capability class of a function that reads or writes the
// file system. A js/wasm module lacks it.
const ClassFiles = "files"

// Classes is the capability class of every host function that has one, by
// its key.
func Classes() map[string]string {
	out := map[string]string{}
	for _, rows := range [][]Binding{Funcs(), RtFuncs()} {
		for _, b := range rows {
			if b.Class != "" {
				out[b.Name] = b.Class
			}
		}
	}
	return out
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
// the declaration says a tuple, `#{tag, ok, err}` where it says a Result). The
// VM's generated adapters call the FFI-shaped half.
func Funcs() []Binding {
	return []Binding{
		{Name: "Date.days_between", Fn: stdcalendar.DateDaysBetween},
		{Name: "Date.to_string", Fn: stdcalendar.DateToString},
		{Name: "DateTime.day", Fn: stdcalendar.DateTimeDay},
		{Name: "DateTime.hour", Fn: stdcalendar.DateTimeHour},
		{Name: "DateTime.minute", Fn: stdcalendar.DateTimeMinute},
		{Name: "DateTime.month", Fn: stdcalendar.DateTimeMonth},
		{Name: "DateTime.nanosecond", Fn: stdcalendar.DateTimeNanosecond},
		{Name: "DateTime.second", Fn: stdcalendar.DateTimeSecond},
		{Name: "DateTime.to_offset", Fn: stdcalendar.DateTimeToOffset},
		{Name: "DateTime.to_string", Fn: stdcalendar.DateTimeToString},
		{Name: "DateTime.year", Fn: stdcalendar.DateTimeYear},
		{Name: "NaiveDateTime.to_date", Fn: stdcalendar.NaiveDateTimeToDate},
		{Name: "NaiveDateTime.to_string", Fn: stdcalendar.NaiveDateTimeToString},
		{Name: "NaiveDateTime.to_time", Fn: stdcalendar.NaiveDateTimeToTime},
		{Name: "OffsetDateTime.day", Fn: stdcalendar.OffsetDateTimeDay},
		{Name: "OffsetDateTime.hour", Fn: stdcalendar.OffsetDateTimeHour},
		{Name: "OffsetDateTime.minute", Fn: stdcalendar.OffsetDateTimeMinute},
		{Name: "OffsetDateTime.month", Fn: stdcalendar.OffsetDateTimeMonth},
		{Name: "OffsetDateTime.nanosecond", Fn: stdcalendar.OffsetDateTimeNanosecond},
		{Name: "OffsetDateTime.second", Fn: stdcalendar.OffsetDateTimeSecond},
		{Name: "OffsetDateTime.to_string", Fn: stdcalendar.OffsetDateTimeToString},
		{Name: "OffsetDateTime.year", Fn: stdcalendar.OffsetDateTimeYear},
		{Name: "Time.to_string", Fn: stdcalendar.TimeToString},
		{Name: "calendar.civil_format_fields", Fn: stdformat.FFIFormatCivilFields},
		{Name: "calendar.civil_format_pattern", Fn: stdformat.FFIFormatCivilPattern},
		{Name: "calendar.civil_format_style", Fn: stdformat.FFIFormatCivilStyle},
		{Name: "calendar.date_add_days", Fn: stdcalendar.BoundDateAddDays},
		{Name: "calendar.date_add_months", Fn: stdcalendar.BoundDateAddMonths},
		{Name: "calendar.date_add_years", Fn: stdcalendar.BoundDateAddYears},
		{Name: "calendar.date_new_raw", Fn: stdcalendar.BoundDateNew},
		{Name: "calendar.date_parse_pattern", Fn: stdformat.FFIParseDatePattern},
		{Name: "calendar.date_parse_raw", Fn: stdcalendar.BoundDateParse},
		{Name: "calendar.date_parse_style", Fn: stdformat.FFIParseDateStyle},
		{Name: "calendar.fields_check", Fn: stdformat.FFICheckFields},
		{Name: "calendar.naive_add_days", Fn: stdcalendar.BoundNaiveAddDays},
		{Name: "calendar.naive_add_months", Fn: stdcalendar.BoundNaiveAddMonths},
		{Name: "calendar.naive_add_nanos", Fn: stdcalendar.BoundNaiveAddNanos},
		{Name: "calendar.naive_add_years", Fn: stdcalendar.BoundNaiveAddYears},
		{Name: "calendar.naive_between_nanos", Fn: stdcalendar.BoundNaiveBetweenNanos},
		{Name: "calendar.naive_new_exact_raw", Fn: stdcalendar.BoundNaiveNewExact},
		{Name: "calendar.naive_parse_pattern", Fn: stdformat.FFIParseNaivePattern},
		{Name: "calendar.naive_parse_raw", Fn: stdcalendar.BoundNaiveParse},
		{Name: "calendar.offset_add_months", Fn: stdcalendar.BoundOffsetAddMonths},
		{Name: "calendar.offset_add_years", Fn: stdcalendar.BoundOffsetAddYears},
		{Name: "calendar.offset_from_instant_nanos", Fn: stdcalendar.BoundOffsetFromInstant},
		{Name: "calendar.offset_parse_raw", Fn: stdcalendar.BoundOffsetParse},
		{Name: "calendar.offset_with_offset_raw", Fn: stdcalendar.BoundOffsetWithOffset},
		{Name: "calendar.pattern_check", Fn: stdformat.FFICheckPattern},
		{Name: "calendar.relative_format_raw", Fn: stdformat.FFIFormatRelative},
		{Name: "calendar.time_add_nanos", Fn: stdcalendar.BoundTimeAddNanos},
		{Name: "calendar.time_parse_pattern", Fn: stdformat.FFIParseTimePattern},
		{Name: "calendar.time_parse_raw", Fn: stdcalendar.BoundTimeParse},
		{Name: "calendar.time_parse_style", Fn: stdformat.FFIParseTimeStyle},
		{Name: "calendar.zoned_add_days", Fn: stdcalendar.BoundZonedAddDays},
		{Name: "calendar.zoned_add_hours", Fn: stdcalendar.BoundZonedAddHours},
		{Name: "calendar.zoned_add_microseconds", Fn: stdcalendar.BoundZonedAddMicroseconds},
		{Name: "calendar.zoned_add_milliseconds", Fn: stdcalendar.BoundZonedAddMilliseconds},
		{Name: "calendar.zoned_add_minutes", Fn: stdcalendar.BoundZonedAddMinutes},
		{Name: "calendar.zoned_add_months", Fn: stdcalendar.BoundZonedAddMonths},
		{Name: "calendar.zoned_add_nanoseconds", Fn: stdcalendar.BoundZonedAddNanoseconds},
		{Name: "calendar.zoned_add_seconds", Fn: stdcalendar.BoundZonedAddSeconds},
		{Name: "calendar.zoned_add_years", Fn: stdcalendar.BoundZonedAddYears},
		{Name: "calendar.zoned_from_instant_in_raw", Fn: stdcalendar.BoundZonedFromInstantIn},
		{Name: "calendar.zoned_in_zone_raw", Fn: stdcalendar.BoundZonedInZone},
		{Name: "calendar.zoned_offset_nanos", Fn: stdcalendar.BoundZonedOffsetNanos},
		{Name: "calendar.zoned_parse_raw", Fn: stdcalendar.BoundZonedParse},
		{Name: "calendar.zoned_with_zone_raw", Fn: stdcalendar.BoundZonedWithZone},
		{Name: "collation.collator_compare_raw", Fn: stdformat.FFICollatorCompare},
		{Name: "collation.collator_inspect_raw", Fn: stdformat.FFICollatorInspect},
		{Name: "collation.collator_new_raw", Fn: stdformat.FFICollatorNew},
		{Name: "collation.collator_sort_key_raw", Fn: stdformat.FFICollatorSortKey},
		{Name: "currency.currency_check", Fn: stdformat.FFICurrencyCheck},
		{Name: "currency.currency_code_of", Fn: stdformat.FFICurrencyCodeOf},
		{Name: "currency.currency_digits_of", Fn: stdformat.FFICurrencyDigitsOf},
		{Name: "decimal.decimal_format", Fn: stdformat.FFIFormatDecimal, PanicsPropagate: true},
		{Name: "duration.duration_format_raw", Fn: stdformat.FFIFormatDuration},
		{Name: "decimal.decimal_format_percent", Fn: stdformat.FFIFormatDecimalPercent, PanicsPropagate: true},
		{Name: "decimal.decimal_parse_in", Fn: stdformat.FFIParseDecimal},
		{Name: "decimal.decimal_parse_percent_in", Fn: stdformat.FFIParseDecimalPercent},
		{Name: "float.float_format", Fn: stdformat.FFIFormatFloat, PanicsPropagate: true},
		{Name: "float.float_format_percent", Fn: stdformat.FFIFormatFloatPercent, PanicsPropagate: true},
		{Name: "float.float_parse_in", Fn: stdformat.FFIParseFloat},
		{Name: "float.float_parse_percent_in", Fn: stdformat.FFIParseFloatPercent},
		{Name: "int.int_format", Fn: stdformat.FFIFormatInt, PanicsPropagate: true},
		{Name: "int.int_parse_in", Fn: stdformat.FFIParseInt},
		{Name: "locale.locale_available", Fn: stdlocale.FFIAvailable},
		{Name: "locale.locale_load_raw", Fn: stdlocale.FFILoad},
		{Name: "locale.locale_loaded_raw", Fn: stdlocale.FFILoaded},
		{Name: "message_format.format_raw", Fn: stdmessageformat.FFIFormat, PanicsPropagate: true},
		{Name: "message_format.load_raw", Fn: stdmessageformat.FFILoad},
		{Name: "message_format.noun_raw", Fn: stdmessageformat.FFINoun, PanicsPropagate: true},
		{Name: "money_format.money_format_raw", Fn: stdformat.FFIFormatMoney},
		{Name: "money_format.money_mixed", Fn: stdformat.FFIMoneyMixed, PanicsPropagate: true},
		{Name: "money_format.money_parse_raw", Fn: stdformat.FFIParseMoney},
		{Name: "locale.locale_fallbacks", Fn: stdlocale.FFIFallbacks},
		{Name: "locale.locale_from_posix_raw", Fn: stdlocale.FFIFromPosix},
		{Name: "locale.locale_language", Fn: stdlocale.FFILanguage},
		{Name: "locale.locale_negotiate", Fn: stdlocale.FFINegotiate},
		{Name: "locale.locale_parse_raw", Fn: stdlocale.FFIParse},
		{Name: "locale.locale_region", Fn: stdlocale.FFIRegion},
		{Name: "locale.locale_script", Fn: stdlocale.FFIScript},
		{Name: "plural.plural_cardinal_tag", Fn: stdplural.FFICardinalTag},
		{Name: "plural.plural_cardinal_tags", Fn: stdplural.FFICardinalTags},
		{Name: "plural.plural_ordinal_tag", Fn: stdplural.FFIOrdinalTag},
		{Name: "plural.plural_ordinal_tags", Fn: stdplural.FFIOrdinalTags},
		{Name: "random.between_state", Fn: stdrandom.FFIBetween},
		{Name: "random.os_state", Fn: stdrandom.FFIFromOS},
		{Name: "random.require_float_range", Fn: stdrandom.FFIRequireFloatRange, PanicsPropagate: true},
		{Name: "random.require_int_range", Fn: stdrandom.FFIRequireIntRange, PanicsPropagate: true},
		{Name: "random.unit_float_state", Fn: stdrandom.FFIUnitFloat},
		{Name: "Regex.compile", Fn: stdregex.FFICompile},
		{Name: "Regex.is_contained_in", Fn: stdregex.FFIMatch},
		{Name: "Regex.find_in", Fn: stdregex.FFIFind},
		{Name: "Regex.find_all_in", Fn: stdregex.FFIFindAll},
		{Name: "Regex.pattern", Fn: stdregex.FFIPattern},
		{Name: "Regex.is_prefix_of", Fn: stdregex.FFIPrefixOf},
		{Name: "Regex.replace_in", Fn: stdregex.FFIReplace},
		{Name: "Regex.split_in", Fn: stdregex.FFISplit},
		{Name: "Regex.is_suffix_of", Fn: stdregex.FFISuffixOf},
		{Name: "strings.capture_groups", Fn: stdstrings.FFICaptures},
		{Name: "strings.capture_groups_all", Fn: stdstrings.FFICapturesAll},
		{Name: "String.capitalize", Fn: stdstrings.FFICapitalize},
		{Name: "String.capitalize_in", Fn: stdstrings.FFICapitalizeIn},
		{Name: "String.fold_case", Fn: stdstrings.FFIFoldCase},
		{Name: "String.snake_case", Fn: stdstrings.SnakeCase},
		{Name: "String.kebab_case", Fn: stdstrings.KebabCase},
		{Name: "String.pascal_case", Fn: stdstrings.PascalCase},
		{Name: "String.camel_case", Fn: stdstrings.CamelCase},
		{Name: "String.lowercase_in", Fn: stdstrings.FFILowercaseIn},
		{Name: "String.uppercase_in", Fn: stdstrings.FFIUppercaseIn},
		{Name: "strings.join_list_raw", Fn: stdformat.FFIJoinList},
	}
}

// LocaleParts names, for each Funcs row that reads the program's CLDR data,
// the parts of it the row reads. `nomi build` writes into an image only the
// parts of the data the program's reachable crossings name here, so a row
// that reads data and is missing here would find none in a built program;
// vmhost's TestEveryRowThatReadsLocaleDataNamesItsParts holds every row
// whose Go function takes the frame in a locale package to an entry.
//
// A row names every part it may read. vmhost narrows money_format_raw's
// further: it writes a currency's name, which needs PartCurrencyNames and
// PartPlurals, only for a program that can ask for one.
func LocaleParts() map[string]cldrdata.Parts {
	numbers := cldrdata.PartListedParents | cldrdata.PartNumbers
	plurals := cldrdata.PartListedParents | cldrdata.PartPlurals
	dates := numbers | cldrdata.PartDates
	return map[string]cldrdata.Parts{
		"locale.locale_fallbacks":          cldrdata.PartParents,
		"locale.locale_available":          cldrdata.PartAvailable,
		"locale.locale_load_raw":           cldrdata.PartListedParents,
		"locale.locale_loaded_raw":         cldrdata.PartAvailable,
		"plural.plural_cardinal_tag":       plurals,
		"plural.plural_cardinal_tags":      plurals,
		"plural.plural_ordinal_tag":        plurals,
		"plural.plural_ordinal_tags":       plurals,
		"int.int_format":                   numbers,
		"int.int_parse_in":                 numbers,
		"float.float_format":               numbers,
		"float.float_format_percent":       numbers,
		"float.float_parse_in":             numbers,
		"float.float_parse_percent_in":     numbers,
		"decimal.decimal_format":           numbers,
		"decimal.decimal_format_percent":   numbers,
		"decimal.decimal_parse_in":         numbers,
		"decimal.decimal_parse_percent_in": numbers,
		"strings.join_list_raw":            cldrdata.PartListedParents | cldrdata.PartLists,
		"collation.collator_compare_raw":   cldrdata.PartListedParents | cldrdata.PartCollation,
		"collation.collator_sort_key_raw":  cldrdata.PartListedParents | cldrdata.PartCollation,
		"calendar.civil_format_style":      dates,
		"calendar.civil_format_fields":     dates | cldrdata.PartSkeletons,
		"calendar.date_parse_style":        dates,
		"calendar.time_parse_style":        dates,
		"calendar.relative_format_raw":     numbers | plurals | cldrdata.PartRelative,
		"duration.duration_format_raw":     numbers | plurals | cldrdata.PartUnits | cldrdata.PartLists,
		"currency.currency_check":          cldrdata.PartCurrencyCodes,
		"currency.currency_digits_of":      cldrdata.PartCurrencyCodes,
		"money_format.money_format_raw":    numbers | cldrdata.PartCurrencies | cldrdata.PartCurrencyNames | cldrdata.PartPlurals,
		"money_format.money_parse_raw":     numbers | cldrdata.PartCurrencies,
		"message_format.format_raw":        cldrdata.PartListedParents | cldrdata.PartPlurals | cldrdata.PartNumbers,
		"message_format.noun_raw":          cldrdata.PartListedParents | cldrdata.PartPlurals | cldrdata.PartNumbers,
		"message_format.load_raw":          cldrdata.PartListedParents | cldrdata.PartPlurals | cldrdata.PartGrammar | cldrdata.PartAvailable,
	}
}

// RtFuncs returns the rt-shaped host functions the VM calls, in deterministic
// order. Every row propagates panics (see Binding.PanicsPropagate).
func RtFuncs() []Binding {
	rows := []Binding{
		{Name: "strings.String.is_contained_in", Fn: rt.StringContainedIn},
		{Name: "strings.String.is_prefix_of", Fn: rt.StringPrefixOf},
		{Name: "strings.String.is_suffix_of", Fn: rt.StringSuffixOf},
		{Name: "strings.String.find_all_in", Fn: rt.StringFindAllIn},
		{Name: "strings.String.uppercase", Fn: rt.StringUppercase},
		{Name: "strings.String.lowercase", Fn: rt.StringLowercase},
		{Name: "strings.String.is_upper", Fn: rt.StringIsUpper},
		{Name: "strings.String.is_lower", Fn: rt.StringIsLower},
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
		{Name: "int.Int.to_byte", Fn: rt.IntToByte},
		{Name: "bytes.Byte.to_int", Fn: rt.ByteToInt},
		{Name: "bytes.Bytes.length", Fn: rt.BytesLength},
		{Name: "hash.sha256", Fn: rt.SHA256},
		{Name: "hash.sha256_hex", Fn: rt.SHA256Hex},
		{Name: "bytes.Bytes.get", Fn: rt.BytesGet},
		{Name: "bytes.Bytes.slice", Fn: rt.BytesSlice},
		{Name: "bytes.Bytes.concat", Fn: rt.BytesConcat},
		{Name: "bytes.Bytes.to_string", Fn: rt.BytesToString},
		{Name: "bytes.Bytes.hash", Fn: rt.BytesHash},
		{Name: "bytes.Bytes.contains", Fn: rt.BytesContains},
		{Name: "bytes.bytes_compare", Fn: rt.BytesCompare},
		{Name: "strings.String.to_bytes", Fn: rt.StringToBytes},
		{Name: "int.Int.to_string", Fn: rt.IntToString},
		{Name: "int.Int.to_float", Fn: rt.IntToFloat},
		{Name: "int.Int.wrapping_add", Fn: rt.IntWrapAdd},
		{Name: "int.Int.wrapping_sub", Fn: rt.IntWrapSub},
		{Name: "int.Int.wrapping_mul", Fn: rt.IntWrapMul},
		{Name: "int.Int.bit_count", Fn: rt.IntBitCount},
		{Name: "int.Int.bit_length", Fn: rt.IntBitLength},
		{Name: "int.Int.pow", Fn: rt.IntPow},
		{Name: "int.Int.sqrt", Fn: rt.IntSqrt},
		{Name: "int.Int.gcd", Fn: rt.IntGcd},
		{Name: "int.Int.lcm", Fn: rt.IntLcm},
		{Name: "int.int_to_digits", Fn: rt.IntToDigits},
		{Name: "int.int_from_digits", Fn: rt.IntFromDigits},
		{Name: "float.Float.to_string", Fn: rt.FormatFloat},
		{Name: "float.Float.nan", Fn: rt.FloatNaN},
		{Name: "float.Float.positive_infinity", Fn: rt.FloatPositiveInfinity},
		{Name: "float.Float.negative_infinity", Fn: rt.FloatNegativeInfinity},
		{Name: "float.Float.is_nan", Fn: rt.FloatIsNaN},
		{Name: "float.float_round", Fn: rt.FloatRound},
		{Name: "float.Float.floor", Fn: rt.FloatFloor},
		{Name: "float.Float.ceil", Fn: rt.FloatCeil},
		{Name: "float.Float.trunc", Fn: rt.FloatTrunc},
		{Name: "float.Float.to_int", Fn: rt.FloatToInt},
		{Name: "float.float_bits", Fn: rt.FloatBits},
		{Name: "float.require_tolerance", Fn: rt.FloatRequireTolerance},
		{Name: "float.Float.pow", Fn: rt.FloatPow},
		{Name: "float.Float.sqrt", Fn: rt.FloatSqrt},
		{Name: "float.Float.exp", Fn: rt.FloatExp},
		{Name: "float.Float.log", Fn: rt.FloatLog},
		{Name: "float.Float.log2", Fn: rt.FloatLog2},
		{Name: "float.Float.log10", Fn: rt.FloatLog10},
		{Name: "float.Float.sin", Fn: rt.FloatSin},
		{Name: "float.Float.cos", Fn: rt.FloatCos},
		{Name: "float.Float.tan", Fn: rt.FloatTan},
		{Name: "float.Float.atan2", Fn: rt.FloatAtan2},
		{Name: "decimal.Decimal.to_string", Fn: rt.DecimalToString},
		{Name: "decimal.Decimal.equal", Fn: rt.EqDecimal},
		{Name: "decimal.Decimal.hash", Fn: rt.DecimalHash},
		{Name: "decimal.Decimal.compare", Fn: rt.DecimalCompare},
		{Name: "duration.Duration.to_string", Fn: rt.DurationToString},
		{Name: "instant.Instant.now", Fn: rt.InstantNow},
		{Name: "int.Int.to_codepoint", Fn: rt.IntToCodepoint},
		{Name: "code_points.Codepoint.to_string", Fn: rt.CodepointToString},
		{Name: "code_points.Codepoint.is_letter", Fn: rt.CodepointIsLetter},
		{Name: "code_points.Codepoint.is_whitespace", Fn: rt.CodepointIsWhitespace},
		{Name: "code_points.Codepoint.is_upper", Fn: rt.CodepointIsUpper},
		{Name: "code_points.Codepoint.is_lower", Fn: rt.CodepointIsLower},
		{Name: "strings.String.to_codepoints", Fn: rt.StringToCodepoints},
		{Name: "context.Context.with_timeout", Fn: rt.ContextWithTimeout},
		{Name: "context.Context.root", Fn: rt.ContextRoot},
		{Name: "json.Json.decode", Fn: rt.JsonDecode},
		{Name: "json.Json.encode", Fn: rt.JsonEncode},
		{Name: "timer.sleep", Fn: rt.TimerSleep},
		{Name: "supervisors.Supervisor.new_exact", Fn: rt.SupervisorNewExact},
		{Name: "supervisors.Supervisor.flush_bounded", Fn: rt.SupervisorFlushBounded},
		{Name: "assertions.AssertionFailure.format", Fn: rt.FormatNomiAssertionFailure},
		{Name: "int.Int.to_decimal", Fn: rt.IntToDecimal},
		{Name: "duration.nanos_in_range", Fn: rt.DurationNanosInRange},
		{Name: "instant.nanos_in_range", Fn: rt.InstantNanosInRange},
		{Name: "strings.String.to_decimal", Fn: rt.StringToDecimal},
		{Name: "decimal.Decimal.to_int", Fn: rt.DecimalToInt},
		{Name: "decimal.Decimal.to_float", Fn: rt.DecimalToFloat},
		{Name: "float.Float.to_decimal", Fn: rt.FloatToDecimal},
		{Name: "decimal.Decimal.divide", Fn: rt.DecimalDivide},
		{Name: "decimal.Decimal.divide_exact", Fn: rt.DecimalDivideExact},
		{Name: "decimal.Decimal.round", Fn: rt.DecimalRound},
		{Name: "decimal.Decimal.pow", Fn: rt.DecimalPow},
		{Name: "decimal.Decimal.sqrt", Fn: rt.DecimalSqrt},
		{Name: "decimal.Decimal.normalize", Fn: rt.DecimalNormalize},
		{Name: "decimal.Decimal.scale", Fn: rt.DecimalScale},
		{Name: "strings.String.repeat", Fn: rt.StringRepeat},
		{Name: "context.Context.deadline", Fn: rt.ContextDeadline},
		{Name: "context.Context.deadline_remaining", Fn: rt.ContextDeadlineRemaining},
		{Name: "context.Context.with_deadline", Fn: rt.ContextWithDeadline},
		{Name: "bytes.Bytes.to_list", Fn: rt.BytesToList},
		{Name: "bytes.Bytes.from_list", Fn: rt.BytesFromList},
		{Name: "strings.String.normalize", Fn: stdstrings.Normalize},
		{Name: "strings.String.to_literal", Fn: stdstrings.Literal},
		{Name: "dynamic.Dynamic.field", Fn: rt.DynamicField},
		{Name: "dynamic.Dynamic.index", Fn: rt.DynamicIndex},
		{Name: "dynamic.Dynamic.path", Fn: rt.DynamicPath},
		{Name: "dynamic.Dynamic.as_string", Fn: rt.DynamicAsString},
		{Name: "dynamic.Dynamic.as_int", Fn: rt.DynamicAsInt},
		{Name: "dynamic.Dynamic.as_float", Fn: rt.DynamicAsFloat},
		{Name: "dynamic.Dynamic.as_bool", Fn: rt.DynamicAsBool},
		{Name: "dynamic.Dynamic.as_list", Fn: rt.DynamicAsList},
		{Name: "dynamic.Dynamic.as_dict", Fn: rt.DynamicAsDict},
		{Name: "dynamic.Dynamic.is_null", Fn: rt.DynamicIsNull},
		{Name: "dynamic.Dynamic.has", Fn: rt.DynamicHas},
		{Name: "dynamic.Dynamic.inspect", Fn: rt.DynamicInspect},
		{Name: "json.Json.to_dynamic", Fn: rt.JsonToDynamic},
		{Name: "io.read_line", Fn: rt.ReadLine},
		{Name: "io.standard_input", Fn: rt.IOStdin},
		{Name: "io.standard_output", Fn: rt.IOStdout},
		{Name: "io.standard_error", Fn: rt.IOStderr},
		{Name: "io.Reader.read_line", Fn: rt.ReaderReadLine},
		{Name: "io.read_up_to", Fn: rt.ReaderRead},
		{Name: "io.Reader.read_all", Fn: rt.ReaderReadAll},
		{Name: "io.Reader.read_all_text", Fn: rt.ReaderReadAllText},
		{Name: "io.Reader.copy_to", Fn: rt.ReaderCopyTo},
		{Name: "io.Reader.close", Fn: rt.ReaderClose},
		{Name: "io.Reader.from_bytes", Fn: rt.ReaderFromBytes},
		{Name: "io.Reader.from_text", Fn: rt.ReaderFromText},
		{Name: "io.Reader.from_fn", Fn: rt.ReaderFromFn},
		{Name: "io.Writer.write", Fn: rt.WriterWrite},
		{Name: "io.Writer.write_text", Fn: rt.WriterWriteText},
		{Name: "io.Writer.flush", Fn: rt.WriterFlush},
		{Name: "io.Writer.close", Fn: rt.WriterClose},
		{Name: "io.writer_from_fn", Fn: rt.WriterFromFn},
		{Name: "io.memory_writer", Fn: rt.WriterToMemory},
		{Name: "io.collected", Fn: rt.WriterCollected},
		{Name: "fs_host.read", Fn: stdio.Read, Class: ClassFiles},
		{Name: "fs_host.read_text", Fn: stdio.ReadText, Class: ClassFiles},
		{Name: "fs_host.write", Fn: stdio.Write, Class: ClassFiles},
		{Name: "fs_host.open", Fn: stdio.Open, Class: ClassFiles},
		{Name: "fs_host.create", Fn: stdio.Create, Class: ClassFiles},
		{Name: "fs_host.discard", Fn: rt.WriterDiscard},
		{Name: "fs_host.list", Fn: stdio.List, Class: ClassFiles},
		{Name: "fs_host.create_dir", Fn: stdio.CreateDir, Class: ClassFiles},
		{Name: "fs_host.remove", Fn: stdio.Remove, Class: ClassFiles},
		{Name: "fs_host.remove_all", Fn: stdio.RemoveAll, Class: ClassFiles},
		{Name: "fs_host.rename", Fn: stdio.Rename, Class: ClassFiles},
		{Name: "fs_host.copy", Fn: stdio.Copy, Class: ClassFiles},
		{Name: "fs_host.metadata", Fn: stdio.Metadata, Class: ClassFiles},
		{Name: "fs_host.link_metadata", Fn: stdio.LinkMetadata, Class: ClassFiles},
		{Name: "fs_host.create_symlink", Fn: stdio.CreateSymlink, Class: ClassFiles},
		{Name: "fs_host.read_link", Fn: stdio.ReadLink, Class: ClassFiles},
		{Name: "fs_host.set_mode", Fn: stdio.SetMode, Class: ClassFiles},
		{Name: "fs_host.open_file", Fn: stdio.OpenFile, Class: ClassFiles},
		{Name: "fs_host.within", Fn: stdio.Within, Class: ClassFiles},
		{Name: "fs_host.release", Fn: stdio.Release},
		{Name: "io.file_read", Fn: rt.FileRead},
		{Name: "io.File.read_at", Fn: rt.FileReadAt},
		{Name: "io.File.write", Fn: rt.FileWrite},
		{Name: "io.File.write_at", Fn: rt.FileWriteAt},
		{Name: "io.File.seek", Fn: rt.FileSeek},
		{Name: "io.File.position", Fn: rt.FilePosition},
		{Name: "io.File.size", Fn: rt.FileSize},
		{Name: "io.File.truncate", Fn: rt.FileTruncate},
		{Name: "io.File.close", Fn: rt.FileClose},
		{Name: "fs_host.temp_dir", Fn: stdio.TempDir, Class: ClassFiles},
		{Name: "fs_host.make_temp_dir", Fn: stdio.MakeTempDir, Class: ClassFiles},
		{Name: "fs_host.current_dir", Fn: stdio.CurrentDir, Class: ClassFiles},
		{Name: "fs_host.new_sandbox", Fn: stdio.NewSandbox},
		{Name: "fs_host.sandbox_fail", Fn: stdio.SandboxFail},
		{Name: "fs_host.sandbox_files", Fn: stdio.SandboxFiles},
		{Name: "fs_host.sandbox_log", Fn: stdio.SandboxLog},
		{Name: "fs_host.sandbox_open", Fn: stdio.SandboxOpen},
	}
	for i := range rows {
		rows[i].PanicsPropagate = true
	}
	return rows
}
