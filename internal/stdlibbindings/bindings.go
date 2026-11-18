package stdlibbindings

import (
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdcalendar"
	"github.com/nomi-language/nomi/internal/stdcsv"
	"github.com/nomi-language/nomi/internal/stdformat"
	"github.com/nomi-language/nomi/internal/stdhttp"
	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/stdphraseformat"
	"github.com/nomi-language/nomi/internal/stdplural"
	"github.com/nomi-language/nomi/internal/stdprocess"
	"github.com/nomi-language/nomi/internal/stdrandom"
	"github.com/nomi-language/nomi/internal/stdregex"
	"github.com/nomi-language/nomi/internal/stdstrings"
	"github.com/nomi-language/nomi/internal/stdtoml"
	"github.com/nomi-language/nomi/internal/stdxml"
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
	// keep unwinding the task. Set too on the Funcs rows that trap: on a
	// value past a range (std/random's range checks, a TLS cipher suite
	// past 16 bits), and std/calendar's zoned civil shifts on a broken
	// invariant (a DateTime's zone loads); unset on the other Funcs rows,
	// whose adapters recover.
	PanicsPropagate bool
	// Class is the capability the function needs from the target it runs on
	// (ClassFiles), or "" for one every target has. `nomi build` refuses a
	// target that lacks a class the program reaches (Classes).
	Class string
	// Pure marks a function that computes its answer from its operands
	// alone and acts on nothing outside the machine: no output, files,
	// network, clock, randomness, tasks, channels or Context. A crossing
	// into it may run where an effect may not (a refinement rule, a typed
	// literal's handler, a `check`, a debugger's evaluation; the vm package
	// doc, "Effects"). Unset, the crossing is an effect. A function whose
	// cost follows one operand's value rather than the operands' size is
	// not marked either, since a call into Go is not interrupted and an
	// editor's evaluation is bounded by steps: `strings.String.repeat` (its
	// count), `decimal.Decimal.divide`, `round`, `round_exact`, `sqrt` and
	// `sqrt_exact` (their scale) and
	// `decimal.Decimal.pow` (its exponent). Reading the program's own CLDR
	// data, as locale-aware formatting does, is not acting outside the
	// machine.
	Pure bool
	// World says whether a call to the function is an outside call
	// (package doc, "Outside calls"). A Pure row is Inside and leaves it
	// unset; every other row sets it, and TestEveryRowDecidesItsWorld
	// fails for one that does not.
	World World
}

// World is whether a host function is an outside call: one that acts on,
// or reads from, something outside the machine (package doc, "Outside
// calls"). The zero value is no decision.
type World uint8

const (
	// Inside is a function that acts only on the machine and the values
	// it is given.
	Inside World = iota + 1
	// Outside is a function that acts on, or reads from, something outside
	// the machine.
	Outside
)

// ClassFiles is the capability class of a function that reads or writes the
// file system. A js/wasm module lacks it.
const ClassFiles = "files"

// ClassProcesses is the capability class of a function that starts a
// program. A js/wasm or wasip1 module lacks it.
const ClassProcesses = "processes"

// ClassNetwork is the capability class of a function that reaches the
// network as an HTTP client: a browser has it through fetch, and wasip1
// does not.
const ClassNetwork = "network"

// ClassConnect is the capability class of a function that opens a
// connection or asks a name resolver itself, below HTTP: std/net's dial
// and lookups, std/tls's dial. A js/wasm or wasip1 module lacks it, since
// a browser offers a program only fetch.
const ClassConnect = "connect"

// ClassListen is the capability class of a function that accepts network
// connections, as a server does. A js/wasm or wasip1 module lacks it.
const ClassListen = "listen"

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

// Pure is the key of every host function whose row is marked Pure.
func Pure() map[string]bool {
	out := map[string]bool{}
	for _, rows := range [][]Binding{Funcs(), RtFuncs()} {
		for _, b := range rows {
			if b.Pure {
				out[b.Name] = true
			}
		}
	}
	return out
}

// Worlds is whether each host function that is not Pure is an outside
// call, by its key: true for Outside. A Pure row is inside and absent.
func Worlds() map[string]bool {
	out := map[string]bool{}
	for _, rows := range [][]Binding{Funcs(), RtFuncs()} {
		for _, b := range rows {
			if !b.Pure {
				out[b.Name] = b.World == Outside
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
// the declaration says a tuple, `{tag:, ok:, err:}` where it says a Result). The
// VM's generated adapters call the FFI-shaped half.
func Funcs() []Binding {
	return []Binding{
		{Name: "Date.days_between", Fn: stdcalendar.DateDaysBetween, Pure: true},
		{Name: "Date.to_string", Fn: stdcalendar.DateToString, Pure: true},
		{Name: "DateTime.day", Fn: stdcalendar.DateTimeDay, Pure: true},
		{Name: "DateTime.hour", Fn: stdcalendar.DateTimeHour, Pure: true},
		{Name: "DateTime.minute", Fn: stdcalendar.DateTimeMinute, Pure: true},
		{Name: "DateTime.month", Fn: stdcalendar.DateTimeMonth, Pure: true},
		{Name: "DateTime.nanosecond", Fn: stdcalendar.DateTimeNanosecond, Pure: true},
		{Name: "DateTime.second", Fn: stdcalendar.DateTimeSecond, Pure: true},
		{Name: "DateTime.to_offset", Fn: stdcalendar.DateTimeToOffset, Pure: true},
		{Name: "DateTime.to_string", Fn: stdcalendar.DateTimeToString, Pure: true},
		{Name: "DateTime.year", Fn: stdcalendar.DateTimeYear, Pure: true},
		{Name: "NaiveDateTime.to_date", Fn: stdcalendar.NaiveDateTimeToDate, Pure: true},
		{Name: "NaiveDateTime.to_string", Fn: stdcalendar.NaiveDateTimeToString, Pure: true},
		{Name: "NaiveDateTime.to_time", Fn: stdcalendar.NaiveDateTimeToTime, Pure: true},
		{Name: "OffsetDateTime.day", Fn: stdcalendar.OffsetDateTimeDay, Pure: true},
		{Name: "OffsetDateTime.hour", Fn: stdcalendar.OffsetDateTimeHour, Pure: true},
		{Name: "OffsetDateTime.minute", Fn: stdcalendar.OffsetDateTimeMinute, Pure: true},
		{Name: "OffsetDateTime.month", Fn: stdcalendar.OffsetDateTimeMonth, Pure: true},
		{Name: "OffsetDateTime.nanosecond", Fn: stdcalendar.OffsetDateTimeNanosecond, Pure: true},
		{Name: "OffsetDateTime.second", Fn: stdcalendar.OffsetDateTimeSecond, Pure: true},
		{Name: "OffsetDateTime.to_naive", Fn: stdcalendar.OffsetDateTimeWall, Pure: true},
		{Name: "OffsetDateTime.to_string", Fn: stdcalendar.OffsetDateTimeToString, Pure: true},
		{Name: "OffsetDateTime.year", Fn: stdcalendar.OffsetDateTimeYear, Pure: true},
		{Name: "Time.to_string", Fn: stdcalendar.TimeToString, Pure: true},
		{Name: "calendar.civil_format_fields", Fn: stdformat.FFIFormatCivilFields, Pure: true},
		{Name: "calendar.civil_format_pattern", Fn: stdformat.FFIFormatCivilPattern, Pure: true},
		{Name: "calendar.civil_format_style", Fn: stdformat.FFIFormatCivilStyle, Pure: true},
		{Name: "calendar.date_add_days", Fn: stdcalendar.BoundDateAddDays, Pure: true},
		{Name: "calendar.date_add_months", Fn: stdcalendar.BoundDateAddMonths, Pure: true},
		{Name: "calendar.date_add_years", Fn: stdcalendar.BoundDateAddYears, Pure: true},
		{Name: "calendar.date_new_raw", Fn: stdcalendar.BoundDateNew, Pure: true},
		{Name: "calendar.date_parse_pattern", Fn: stdformat.FFIParseDatePattern, Pure: true},
		{Name: "calendar.date_parse_raw", Fn: stdcalendar.BoundDateParse, Pure: true},
		{Name: "calendar.date_parse_style", Fn: stdformat.FFIParseDateStyle, Pure: true},
		{Name: "calendar.fields_check", Fn: stdformat.FFICheckFields, Pure: true},
		{Name: "calendar.naive_add_days", Fn: stdcalendar.BoundNaiveAddDays, Pure: true},
		{Name: "calendar.naive_add_months", Fn: stdcalendar.BoundNaiveAddMonths, Pure: true},
		{Name: "calendar.naive_add_nanos", Fn: stdcalendar.BoundNaiveAddNanos, Pure: true},
		{Name: "calendar.naive_add_years", Fn: stdcalendar.BoundNaiveAddYears, Pure: true},
		{Name: "calendar.naive_between_nanos", Fn: stdcalendar.BoundNaiveBetweenNanos, Pure: true},
		{Name: "calendar.naive_new_exact_raw", Fn: stdcalendar.BoundNaiveNewExact, Pure: true},
		{Name: "calendar.naive_parse_pattern", Fn: stdformat.FFIParseNaivePattern, Pure: true},
		{Name: "calendar.naive_parse_raw", Fn: stdcalendar.BoundNaiveParse, Pure: true},
		{Name: "calendar.offset_add_months", Fn: stdcalendar.BoundOffsetAddMonths, Pure: true},
		{Name: "calendar.offset_add_years", Fn: stdcalendar.BoundOffsetAddYears, Pure: true},
		{Name: "calendar.offset_add_nanos", Fn: stdcalendar.BoundOffsetAddNanos, Pure: true},
		{Name: "calendar.offset_from_instant_nanos", Fn: stdcalendar.BoundOffsetFromInstant, Pure: true},
		{Name: "calendar.offset_parse_raw", Fn: stdcalendar.BoundOffsetParse, Pure: true},
		{Name: "calendar.offset_with_offset_raw", Fn: stdcalendar.BoundOffsetWithOffset, Pure: true},
		{Name: "calendar.pattern_check", Fn: stdformat.FFICheckPattern, Pure: true},
		{Name: "calendar.relative_format_raw", Fn: stdformat.FFIFormatRelative, Pure: true},
		{Name: "calendar.time_add_nanos", Fn: stdcalendar.BoundTimeAddNanos, Pure: true},
		{Name: "calendar.time_parse_pattern", Fn: stdformat.FFIParseTimePattern, Pure: true},
		{Name: "calendar.time_parse_raw", Fn: stdcalendar.BoundTimeParse, Pure: true},
		{Name: "calendar.time_parse_style", Fn: stdformat.FFIParseTimeStyle, Pure: true},
		{Name: "calendar.zoned_add_days", Fn: stdcalendar.BoundZonedAddDays, PanicsPropagate: true, Pure: true},
		{Name: "calendar.zoned_add_hours", Fn: stdcalendar.BoundZonedAddHours, PanicsPropagate: true, Pure: true},
		{Name: "calendar.zoned_add_microseconds", Fn: stdcalendar.BoundZonedAddMicroseconds, PanicsPropagate: true, Pure: true},
		{Name: "calendar.zoned_add_milliseconds", Fn: stdcalendar.BoundZonedAddMilliseconds, PanicsPropagate: true, Pure: true},
		{Name: "calendar.zoned_add_minutes", Fn: stdcalendar.BoundZonedAddMinutes, PanicsPropagate: true, Pure: true},
		{Name: "calendar.zoned_add_months", Fn: stdcalendar.BoundZonedAddMonths, PanicsPropagate: true, Pure: true},
		{Name: "calendar.zoned_add_nanoseconds", Fn: stdcalendar.BoundZonedAddNanoseconds, PanicsPropagate: true, Pure: true},
		{Name: "calendar.zoned_add_seconds", Fn: stdcalendar.BoundZonedAddSeconds, PanicsPropagate: true, Pure: true},
		{Name: "calendar.zoned_add_years", Fn: stdcalendar.BoundZonedAddYears, PanicsPropagate: true, Pure: true},
		{Name: "calendar.zoned_add_nanos", Fn: stdcalendar.BoundZonedAddNanos, Pure: true},
		{Name: "calendar.zoned_from_instant_in_raw", Fn: stdcalendar.BoundZonedFromInstantIn, Pure: true},
		{Name: "calendar.zoned_in_zone_raw", Fn: stdcalendar.BoundZonedInZone, Pure: true},
		{Name: "calendar.zoned_offset_nanos", Fn: stdcalendar.BoundZonedOffsetNanos, Pure: true},
		{Name: "calendar.zoned_parse_raw", Fn: stdcalendar.BoundZonedParse, Pure: true},
		{Name: "calendar.zoned_with_zone_raw", Fn: stdcalendar.BoundZonedWithZone, Pure: true},
		{Name: "collation.collator_compare_raw", Fn: stdformat.FFICollatorCompare, Pure: true},
		{Name: "collation.collator_inspect_raw", Fn: stdformat.FFICollatorInspect, Pure: true},
		{Name: "collation.collator_new_raw", Fn: stdformat.FFICollatorNew, Pure: true},
		{Name: "collation.collator_sort_key_raw", Fn: stdformat.FFICollatorSortKey, Pure: true},
		{Name: "currency.currency_check", Fn: stdformat.FFICurrencyCheck, Pure: true},
		{Name: "currency.currency_code_of", Fn: stdformat.FFICurrencyCodeOf, Pure: true},
		{Name: "currency.currency_digits_of", Fn: stdformat.FFICurrencyDigitsOf, Pure: true},
		{Name: "decimal.decimal_format", Fn: stdformat.FFIFormatDecimal, PanicsPropagate: true, Pure: true},
		{Name: "duration.duration_format_raw", Fn: stdformat.FFIFormatDuration, Pure: true},
		{Name: "decimal.decimal_format_percent", Fn: stdformat.FFIFormatDecimalPercent, PanicsPropagate: true, Pure: true},
		{Name: "decimal.decimal_parse_in", Fn: stdformat.FFIParseDecimal, Pure: true},
		{Name: "decimal.decimal_parse_percent_in", Fn: stdformat.FFIParseDecimalPercent, Pure: true},
		{Name: "float.float_format", Fn: stdformat.FFIFormatFloat, PanicsPropagate: true, Pure: true},
		{Name: "float.float_format_percent", Fn: stdformat.FFIFormatFloatPercent, PanicsPropagate: true, Pure: true},
		{Name: "float.float_parse_in", Fn: stdformat.FFIParseFloat, Pure: true},
		{Name: "float.float_parse_percent_in", Fn: stdformat.FFIParseFloatPercent, Pure: true},
		{Name: "int.int_format", Fn: stdformat.FFIFormatInt, PanicsPropagate: true, Pure: true},
		{Name: "int.int_parse_in", Fn: stdformat.FFIParseInt, Pure: true},
		{Name: "locale.locale_available", Fn: stdlocale.FFIAvailable, Pure: true},
		{Name: "locale.locale_load_raw", Fn: stdlocale.FFILoad, World: Inside},
		{Name: "locale.locale_loaded_raw", Fn: stdlocale.FFILoaded, World: Inside},
		{Name: "phrase/format.format_raw", Fn: stdphraseformat.FFIFormat, PanicsPropagate: true, Pure: true},
		{Name: "phrase/format.load_raw", Fn: stdphraseformat.FFILoad, World: Inside},
		{Name: "phrase/format.noun_raw", Fn: stdphraseformat.FFINoun, PanicsPropagate: true, World: Inside},
		{Name: "money/format.money_format_raw", Fn: stdformat.FFIFormatMoney, Pure: true},
		{Name: "money/format.money_parse_raw", Fn: stdformat.FFIParseMoney, Pure: true},
		{Name: "locale.locale_fallbacks", Fn: stdlocale.FFIFallbacks, Pure: true},
		{Name: "locale.locale_from_posix_raw", Fn: stdlocale.FFIFromPosix, Pure: true},
		{Name: "locale.locale_language", Fn: stdlocale.FFILanguage, Pure: true},
		{Name: "locale.locale_negotiate", Fn: stdlocale.FFINegotiate, Pure: true},
		{Name: "locale.locale_parse_raw", Fn: stdlocale.FFIParse, Pure: true},
		{Name: "locale.locale_region", Fn: stdlocale.FFIRegion, Pure: true},
		{Name: "locale.locale_script", Fn: stdlocale.FFIScript, Pure: true},
		{Name: "plural.plural_cardinal_tag", Fn: stdplural.FFICardinalTag, Pure: true},
		{Name: "plural.plural_cardinal_tags", Fn: stdplural.FFICardinalTags, Pure: true},
		{Name: "plural.plural_ordinal_tag", Fn: stdplural.FFIOrdinalTag, Pure: true},
		{Name: "plural.plural_ordinal_tags", Fn: stdplural.FFIOrdinalTags, Pure: true},
		{Name: "random.between_state", Fn: stdrandom.FFIBetween, World: Inside},
		{Name: "random.os_state", Fn: stdrandom.FFIFromOS, World: Outside},
		{Name: "random.secure_bytes", Fn: stdrandom.FFISecureBytes, PanicsPropagate: true, World: Outside},
		{Name: "random.unit_float_state", Fn: stdrandom.FFIUnitFloat, World: Inside},
		{Name: "Regex.compile", Fn: stdregex.FFICompile, Pure: true},
		{Name: "Regex.is_contained_in", Fn: stdregex.FFIMatch, Pure: true},
		{Name: "Regex.find_in", Fn: stdregex.FFIFind, Pure: true},
		{Name: "Regex.find_all_in", Fn: stdregex.FFIFindAll, Pure: true},
		{Name: "Regex.pattern", Fn: stdregex.FFIPattern, Pure: true},
		{Name: "Regex.is_prefix_of", Fn: stdregex.FFIPrefixOf, Pure: true},
		{Name: "Regex.replace_in", Fn: stdregex.FFIReplace, Pure: true},
		{Name: "Regex.split_in", Fn: stdregex.FFISplit, Pure: true},
		{Name: "Regex.is_suffix_of", Fn: stdregex.FFISuffixOf, Pure: true},
		{Name: "toml/host.check", Fn: stdtoml.FFICheck, Pure: true},
		{Name: "toml/host.build", Fn: stdtoml.FFIBuild, Pure: true},
		{Name: "toml/host.quote", Fn: stdtoml.FFIQuote, Pure: true},
		{Name: "toml/host.float", Fn: stdtoml.FFIFloat, Pure: true},
		{Name: "strings.capture_groups", Fn: stdstrings.FFICaptures, Pure: true},
		{Name: "strings.capture_groups_all", Fn: stdstrings.FFICapturesAll, Pure: true},
		{Name: "String.capitalize", Fn: stdstrings.FFICapitalize, Pure: true},
		{Name: "String.capitalize_in", Fn: stdstrings.FFICapitalizeIn, Pure: true},
		{Name: "String.fold_case", Fn: stdstrings.FFIFoldCase, Pure: true},
		{Name: "String.snake_case", Fn: stdstrings.SnakeCase, Pure: true},
		{Name: "String.kebab_case", Fn: stdstrings.KebabCase, Pure: true},
		{Name: "String.pascal_case", Fn: stdstrings.PascalCase, Pure: true},
		{Name: "String.camel_case", Fn: stdstrings.CamelCase, Pure: true},
		{Name: "String.lowercase_in", Fn: stdstrings.FFILowercaseIn, Pure: true},
		{Name: "String.uppercase_in", Fn: stdstrings.FFIUppercaseIn, Pure: true},
		{Name: "strings.join_list_raw", Fn: stdformat.FFIJoinList, Pure: true},
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
		"money/format.money_format_raw":    numbers | cldrdata.PartCurrencies | cldrdata.PartCurrencyNames | cldrdata.PartPlurals,
		"money/format.money_parse_raw":     numbers | cldrdata.PartCurrencies,
		"phrase/format.format_raw":         cldrdata.PartListedParents | cldrdata.PartPlurals | cldrdata.PartNumbers,
		"phrase/format.noun_raw":           cldrdata.PartListedParents | cldrdata.PartPlurals | cldrdata.PartNumbers,
		"phrase/format.load_raw":           cldrdata.PartListedParents | cldrdata.PartPlurals | cldrdata.PartGrammar | cldrdata.PartAvailable,
	}
}

// RtFuncs returns the rt-shaped host functions the VM calls, in deterministic
// order. Every row propagates panics (see Binding.PanicsPropagate).
func RtFuncs() []Binding {
	rows := []Binding{
		{Name: "strings.String.is_contained_in", Fn: rt.StringContainedIn, Pure: true},
		{Name: "strings.String.is_prefix_of", Fn: rt.StringPrefixOf, Pure: true},
		{Name: "strings.String.is_suffix_of", Fn: rt.StringSuffixOf, Pure: true},
		{Name: "strings.String.find_all_in", Fn: rt.StringFindAllIn, Pure: true},
		{Name: "strings.String.uppercase", Fn: rt.StringUppercase, Pure: true},
		{Name: "strings.String.lowercase", Fn: rt.StringLowercase, Pure: true},
		{Name: "strings.String.is_upper", Fn: rt.StringIsUpper, World: Inside},
		{Name: "strings.String.is_lower", Fn: rt.StringIsLower, World: Inside},
		{Name: "strings.String.replace_in", Fn: rt.StringReplaceIn, Pure: true},
		{Name: "strings.String.trim", Fn: rt.StringTrim, Pure: true},
		{Name: "strings.String.hash", Fn: rt.StringHash, Pure: true},
		{Name: "strings.string_compare", Fn: rt.StringCompare, Pure: true},
		{Name: "strings.String.to_int", Fn: rt.StringToInt, Pure: true},
		{Name: "strings.String.split_in", Fn: rt.StringSplitIn, Pure: true},
		{Name: "strings.String.words", Fn: rt.StringWords, Pure: true},
		{Name: "strings.String.lines", Fn: rt.StringLines, Pure: true},
		{Name: "strings.String.length", Fn: rt.StringLength, Pure: true},
		{Name: "strings.String.slice", Fn: rt.StringSlice, Pure: true},
		{Name: "strings.String.reverse", Fn: rt.StringReverse, Pure: true},
		{Name: "int.Int.to_byte", Fn: rt.IntToByte, Pure: true},
		{Name: "bytes.Byte.to_int", Fn: rt.ByteToInt, Pure: true},
		{Name: "bytes.Bytes.length", Fn: rt.BytesLength, Pure: true},
		{Name: "hash.sha256", Fn: rt.SHA256, Pure: true},
		{Name: "hash.sha512", Fn: rt.SHA512, Pure: true},
		{Name: "hash.md5", Fn: rt.MD5, World: Inside},
		{Name: "hash.sha1", Fn: rt.SHA1, World: Inside},
		{Name: "hash.hmac_sha256", Fn: rt.HMACSHA256, Pure: true},
		{Name: "hash.hmac_sha512", Fn: rt.HMACSHA512, Pure: true},
		{Name: "hash.equal_in_constant_time", Fn: rt.EqualInConstantTime, Pure: true},
		{Name: "hash.pbkdf2_sha256", Fn: rt.PBKDF2SHA256, World: Inside},
		{Name: "signatures/host.generate_ed25519", Fn: stdhttp.SigGenerateEd25519, World: Outside},
		{Name: "signatures/host.generate_p256", Fn: stdhttp.SigGenerateP256, World: Outside},
		{Name: "signatures/host.generate_rsa", Fn: stdhttp.SigGenerateRSA, World: Outside},
		{Name: "signatures/host.private_from_der", Fn: stdhttp.SigPrivateFromDER, World: Inside},
		{Name: "signatures/host.private_from_pem", Fn: stdhttp.SigPrivateFromPEM, World: Inside},
		{Name: "signatures/host.private_to_pem", Fn: stdhttp.SigPrivateToPEM, World: Inside},
		{Name: "signatures/host.private_kind", Fn: stdhttp.SigPrivateKind, World: Inside},
		{Name: "signatures/host.public_of", Fn: stdhttp.SigPublicOf, World: Inside},
		{Name: "signatures/host.public_from_der", Fn: stdhttp.SigPublicFromDER, World: Inside},
		{Name: "signatures/host.public_from_pem", Fn: stdhttp.SigPublicFromPEM, World: Inside},
		{Name: "signatures/host.public_to_pem", Fn: stdhttp.SigPublicToPEM, World: Inside},
		{Name: "signatures/host.public_kind", Fn: stdhttp.SigPublicKind, World: Inside},
		{Name: "signatures/host.ed25519_from_seed", Fn: stdhttp.SigEd25519FromSeed, World: Inside},
		{Name: "signatures/host.ed25519_seed", Fn: stdhttp.SigEd25519Seed, World: Inside},
		{Name: "signatures/host.ed25519_public_from_bytes", Fn: stdhttp.SigEd25519PublicFromBytes, World: Inside},
		{Name: "signatures/host.ed25519_public_bytes", Fn: stdhttp.SigEd25519PublicBytes, World: Inside},
		{Name: "signatures/host.sign", Fn: stdhttp.SigSign, World: Inside},
		{Name: "signatures/host.is_valid", Fn: stdhttp.SigIsValid, World: Inside},
		{Name: "http/host.seal", Fn: rt.Seal, World: Inside},
		{Name: "http/host.open_sealed", Fn: rt.Open, World: Inside},
		{Name: "base64.standard", Fn: rt.Base64Encode, Pure: true},
		{Name: "base64.decode", Fn: rt.Base64Decode, Pure: true},
		{Name: "base64.url_safe", Fn: rt.Base64URLEncode, Pure: true},
		{Name: "base64.decode_url", Fn: rt.Base64URLDecode, Pure: true},
		{Name: "hex.encode", Fn: rt.HexEncode, Pure: true},
		{Name: "hex.decode", Fn: rt.HexDecode, Pure: true},
		{Name: "html/host.escape", Fn: rt.HTMLEscape, World: Inside},
		{Name: "html/host.filter_url", Fn: rt.HTMLFilterURL, World: Inside},
		{Name: "html/host.escape_url_path", Fn: rt.HTMLEscapeURLPath, World: Inside},
		{Name: "url/host.escape_query", Fn: rt.URLEscapeQuery, Pure: true},
		{Name: "url/host.unescape_query", Fn: rt.URLUnescapeQuery, Pure: true},
		{Name: "url/host.escape_path_segment", Fn: rt.URLEscapePathSegment, Pure: true},
		{Name: "url/host.unescape_path_segment", Fn: rt.URLUnescapePathSegment, Pure: true},
		{Name: "url/host.parse_parts", Fn: rt.URLParse, Pure: true},
		{Name: "url/host.format_parts", Fn: rt.URLFormat, Pure: true},
		{Name: "url/host.redacted_parts", Fn: rt.URLRedacted, Pure: true},
		{Name: "url/host.escaped_path_parts", Fn: rt.URLEscapedPath, Pure: true},
		{Name: "url/host.escaped_fragment_parts", Fn: rt.URLEscapedFragment, Pure: true},
		{Name: "url/host.request_uri_parts", Fn: rt.URLRequestURI, Pure: true},
		{Name: "url/host.hostname_parts", Fn: rt.URLHostname, Pure: true},
		{Name: "url/host.port_parts", Fn: rt.URLPort, Pure: true},
		{Name: "url/host.resolve_parts", Fn: rt.URLResolve, Pure: true},
		{Name: "url/host.join_path_parts", Fn: rt.URLJoinPath, Pure: true},
		{Name: "url/host.escape_fragment", Fn: rt.URLEscapeFragment, Pure: true},
		{Name: "url/host.query_pairs", Fn: rt.QueryPairs, Pure: true},
		{Name: "url/host.query_error", Fn: rt.QueryError, Pure: true},
		{Name: "url/host.query_encode", Fn: rt.QueryEncode, Pure: true},
		{Name: "path/host.clean", Fn: rt.PathClean, Pure: true},
		{Name: "path/host.join", Fn: rt.PathJoin, Pure: true},
		{Name: "path/host.base", Fn: rt.PathBase, Pure: true},
		{Name: "path/host.dir", Fn: rt.PathDir, Pure: true},
		{Name: "mail/host.quoted_printable", Fn: rt.MailQuotedPrintable, Pure: true},
		{Name: "mail/host.date", Fn: rt.MailDate, Pure: true},
		{Name: "bytes.Bytes.get", Fn: rt.BytesGet, Pure: true},
		{Name: "bytes.Bytes.slice", Fn: rt.BytesSlice, Pure: true},
		{Name: "bytes.Bytes.concat", Fn: rt.BytesConcat, Pure: true},
		{Name: "bytes.Bytes.to_string", Fn: rt.BytesToString, Pure: true},
		{Name: "bytes.Bytes.hash", Fn: rt.BytesHash, Pure: true},
		{Name: "bytes.Bytes.contains", Fn: rt.BytesContains, Pure: true},
		{Name: "bytes.bytes_compare", Fn: rt.BytesCompare, Pure: true},
		{Name: "strings.String.to_bytes", Fn: rt.StringToBytes, Pure: true},
		{Name: "int.Int.to_string", Fn: rt.IntToString, Pure: true},
		{Name: "int.Int.to_float", Fn: rt.IntToFloat, Pure: true},
		{Name: "averageable.int_ratio_to_float", Fn: rt.IntRatioToFloat, World: Inside},
		{Name: "int.Int.wrapping_add", Fn: rt.IntWrapAdd, Pure: true},
		{Name: "int.Int.wrapping_sub", Fn: rt.IntWrapSub, Pure: true},
		{Name: "int.Int.wrapping_mul", Fn: rt.IntWrapMul, Pure: true},
		{Name: "int.Int.bit_count", Fn: rt.IntBitCount, Pure: true},
		{Name: "int.Int.bit_length", Fn: rt.IntBitLength, Pure: true},
		{Name: "int.Int.pow", Fn: rt.IntPow, Pure: true},
		{Name: "int.Int.sqrt", Fn: rt.IntSqrt, Pure: true},
		{Name: "int.Int.gcd", Fn: rt.IntGcd, Pure: true},
		{Name: "int.Int.lcm", Fn: rt.IntLcm, Pure: true},
		{Name: "int.int_to_digits", Fn: rt.IntToDigits, Pure: true},
		{Name: "int.int_from_digits", Fn: rt.IntFromDigits, Pure: true},
		{Name: "float.Float.to_string", Fn: rt.FormatFloat, Pure: true},
		{Name: "float.Float.nan", Fn: rt.FloatNaN, Pure: true},
		{Name: "float.Float.positive_infinity", Fn: rt.FloatPositiveInfinity, Pure: true},
		{Name: "float.Float.negative_infinity", Fn: rt.FloatNegativeInfinity, Pure: true},
		{Name: "float.Float.is_nan", Fn: rt.FloatIsNaN, Pure: true},
		{Name: "float.float_round", Fn: rt.FloatRound, Pure: true},
		{Name: "float.Float.floor", Fn: rt.FloatFloor, Pure: true},
		{Name: "float.Float.ceil", Fn: rt.FloatCeil, Pure: true},
		{Name: "float.Float.trunc", Fn: rt.FloatTrunc, Pure: true},
		{Name: "float.Float.to_int", Fn: rt.FloatToInt, Pure: true},
		{Name: "float.float_bits", Fn: rt.FloatBits, Pure: true},
		{Name: "float.Float.pow", Fn: rt.FloatPow, Pure: true},
		{Name: "float.Float.sqrt", Fn: rt.FloatSqrt, Pure: true},
		{Name: "float.Float.exp", Fn: rt.FloatExp, Pure: true},
		{Name: "float.Float.log", Fn: rt.FloatLog, Pure: true},
		{Name: "float.Float.log2", Fn: rt.FloatLog2, Pure: true},
		{Name: "float.Float.log10", Fn: rt.FloatLog10, Pure: true},
		{Name: "float.Float.sin", Fn: rt.FloatSin, Pure: true},
		{Name: "float.Float.cos", Fn: rt.FloatCos, Pure: true},
		{Name: "float.Float.tan", Fn: rt.FloatTan, Pure: true},
		{Name: "float.Float.atan2", Fn: rt.FloatAtan2, Pure: true},
		{Name: "decimal.Decimal.to_string", Fn: rt.DecimalToString, Pure: true},
		{Name: "decimal.Decimal.equal", Fn: rt.EqDecimal, Pure: true},
		{Name: "decimal.Decimal.hash", Fn: rt.DecimalHash, Pure: true},
		{Name: "decimal.Decimal.compare", Fn: rt.DecimalCompare, Pure: true},
		{Name: "duration.render_nanos", Fn: rt.DurationNanosToString, Pure: true},
		{Name: "instant.Instant.now", Fn: rt.InstantNow, World: Outside},
		{Name: "int.Int.to_codepoint", Fn: rt.IntToCodepoint, Pure: true},
		{Name: "code_points.Codepoint.to_string", Fn: rt.CodepointToString, Pure: true},
		{Name: "code_points.Codepoint.is_letter", Fn: rt.CodepointIsLetter, Pure: true},
		{Name: "code_points.Codepoint.is_whitespace", Fn: rt.CodepointIsWhitespace, Pure: true},
		{Name: "code_points.Codepoint.is_upper", Fn: rt.CodepointIsUpper, Pure: true},
		{Name: "code_points.Codepoint.is_lower", Fn: rt.CodepointIsLower, Pure: true},
		{Name: "strings.String.to_codepoints", Fn: rt.StringToCodepoints, Pure: true},
		{Name: "context.Context.with_timeout", Fn: rt.ContextWithTimeout, World: Outside},
		{Name: "context.Context.root", Fn: rt.ContextRoot, World: Inside},
		{Name: "json.Json.decode", Fn: rt.JsonDecode, Pure: true},
		{Name: "json.Json.encode", Fn: rt.JsonEncode, Pure: true},
		{Name: "timer.sleep", Fn: rt.TimerSleep, World: Outside},
		{Name: "supervisors.Supervisor.new_exact", Fn: rt.SupervisorNewExact, World: Inside},
		{Name: "supervisors.Supervisor.flush_bounded", Fn: rt.SupervisorFlushBounded, World: Outside},
		{Name: "assertions.AssertionFailure.format", Fn: rt.FormatNomiAssertionFailure, Pure: true},
		{Name: "int.Int.to_decimal", Fn: rt.IntToDecimal, Pure: true},
		{Name: "strings.String.to_decimal", Fn: rt.StringToDecimal, Pure: true},
		{Name: "strings.String.to_float", Fn: rt.StringToFloat, Pure: true},
		{Name: "decimal.Decimal.to_int", Fn: rt.DecimalToInt, Pure: true},
		{Name: "decimal.Decimal.to_float", Fn: rt.DecimalToFloat, Pure: true},
		{Name: "float.Float.to_decimal", Fn: rt.FloatToDecimal, Pure: true},
		{Name: "decimal.Decimal.divide", Fn: rt.DecimalDivide, World: Inside},
		{Name: "decimal.Decimal.divide_exact", Fn: rt.DecimalDivideExact, Pure: true},
		{Name: "decimal.Decimal.round", Fn: rt.DecimalRound, World: Inside},
		{Name: "decimal.Decimal.round_exact", Fn: rt.DecimalRoundExact, World: Inside},
		{Name: "decimal.Decimal.pow", Fn: rt.DecimalPow, World: Inside},
		{Name: "decimal.Decimal.sqrt", Fn: rt.DecimalSqrt, World: Inside},
		{Name: "decimal.Decimal.sqrt_exact", Fn: rt.DecimalSqrtExact, World: Inside},
		{Name: "decimal.Decimal.normalize", Fn: rt.DecimalNormalize, Pure: true},
		{Name: "decimal.Decimal.scale", Fn: rt.DecimalScale, Pure: true},
		{Name: "strings.String.repeat", Fn: rt.StringRepeat, World: Inside},
		{Name: "context.Context.deadline", Fn: rt.ContextDeadline, World: Inside},
		{Name: "context.Context.deadline_remaining", Fn: rt.ContextDeadlineRemaining, World: Outside},
		{Name: "context.Context.with_deadline", Fn: rt.ContextWithDeadline, World: Inside},
		{Name: "bytes.Bytes.to_list", Fn: rt.BytesToList, Pure: true},
		{Name: "bytes.Bytes.from_list", Fn: rt.BytesFromList, Pure: true},
		{Name: "strings.String.normalize", Fn: stdstrings.Normalize, Pure: true},
		{Name: "strings.String.to_literal", Fn: stdstrings.Literal, Pure: true},
		{Name: "dynamic.Dynamic.field", Fn: rt.DynamicField, Pure: true},
		{Name: "dynamic.Dynamic.index", Fn: rt.DynamicIndex, Pure: true},
		{Name: "dynamic.Dynamic.path", Fn: rt.DynamicPath, Pure: true},
		{Name: "dynamic.Dynamic.as_string", Fn: rt.DynamicAsString, Pure: true},
		{Name: "dynamic.Dynamic.as_int", Fn: rt.DynamicAsInt, Pure: true},
		{Name: "dynamic.Dynamic.as_float", Fn: rt.DynamicAsFloat, Pure: true},
		{Name: "dynamic.Dynamic.as_bool", Fn: rt.DynamicAsBool, Pure: true},
		{Name: "dynamic.Dynamic.as_list", Fn: rt.DynamicAsList, Pure: true},
		{Name: "dynamic.Dynamic.as_dict", Fn: rt.DynamicAsDict, Pure: true},
		{Name: "dynamic.Dynamic.is_null", Fn: rt.DynamicIsNull, Pure: true},
		{Name: "dynamic.Dynamic.hash", Fn: rt.DynamicHashInt, World: Inside},
		{Name: "dynamic.Dynamic.has", Fn: rt.DynamicHas, Pure: true},
		{Name: "dynamic.Dynamic.inspect", Fn: rt.DynamicInspect, Pure: true},
		{Name: "json.Json.to_dynamic", Fn: rt.JsonToDynamic, Pure: true},
		{Name: "io.read_line", Fn: rt.ReadLine, World: Outside},
		{Name: "io.standard_input", Fn: rt.IOStdin, World: Outside},
		{Name: "io.standard_output", Fn: rt.IOStdout, World: Outside},
		{Name: "io.standard_error", Fn: rt.IOStderr, World: Outside},
		{Name: "io.Reader.read_line", Fn: rt.ReaderReadLine, World: Inside},
		{Name: "io.read_up_to", Fn: rt.ReaderRead, World: Inside},
		{Name: "io.Reader.read_all", Fn: rt.ReaderReadAll, World: Inside},
		{Name: "io.Reader.read_all_text", Fn: rt.ReaderReadAllText, World: Inside},
		{Name: "io.Reader.copy_to", Fn: rt.ReaderCopyTo, World: Inside},
		{Name: "io.Reader.close", Fn: rt.ReaderClose, World: Inside},
		{Name: "io.Reader.from_bytes", Fn: rt.ReaderFromBytes, World: Inside},
		{Name: "io.Reader.from_text", Fn: rt.ReaderFromText, World: Inside},
		{Name: "io.Reader.from_fn", Fn: rt.ReaderFromFn, World: Inside},
		{Name: "io.Writer.write", Fn: rt.WriterWrite, World: Inside},
		{Name: "io.Writer.write_text", Fn: rt.WriterWriteText, World: Inside},
		{Name: "io.Writer.flush", Fn: rt.WriterFlush, World: Inside},
		{Name: "io.Writer.close", Fn: rt.WriterClose, World: Inside},
		{Name: "io.Writer.discard", Fn: rt.WriterDiscard, World: Inside},
		{Name: "io.writer_from_fn", Fn: rt.WriterFromFn, World: Inside},
		{Name: "io.memory_writer", Fn: rt.WriterToMemory, World: Inside},
		{Name: "io.collected", Fn: rt.WriterCollected, World: Inside},
		// An embedded file is read from the program itself, which every
		// target has: no class.
		{Name: "embed/host.read", Fn: rt.EmbeddedRead, World: Inside},
		{Name: "embed/host.sum", Fn: rt.EmbeddedSum, World: Inside},
		{Name: "fs/host.read", Fn: stdio.Read, Class: ClassFiles, World: Outside},
		{Name: "fs/host.read_text", Fn: stdio.ReadText, Class: ClassFiles, World: Outside},
		{Name: "fs/host.write", Fn: stdio.Write, Class: ClassFiles, World: Outside},
		{Name: "fs/host.open", Fn: stdio.Open, Class: ClassFiles, World: Outside},
		{Name: "fs/host.create", Fn: stdio.Create, Class: ClassFiles, World: Outside},
		{Name: "fs/host.list", Fn: stdio.List, Class: ClassFiles, World: Outside},
		{Name: "fs/host.create_dir", Fn: stdio.CreateDir, Class: ClassFiles, World: Outside},
		{Name: "fs/host.remove", Fn: stdio.Remove, Class: ClassFiles, World: Outside},
		{Name: "fs/host.remove_all", Fn: stdio.RemoveAll, Class: ClassFiles, World: Outside},
		{Name: "fs/host.rename", Fn: stdio.Rename, Class: ClassFiles, World: Outside},
		{Name: "fs/host.copy", Fn: stdio.Copy, Class: ClassFiles, World: Outside},
		{Name: "fs/host.metadata", Fn: stdio.Metadata, Class: ClassFiles, World: Outside},
		{Name: "fs/host.link_metadata", Fn: stdio.LinkMetadata, Class: ClassFiles, World: Outside},
		{Name: "fs/host.create_symlink", Fn: stdio.CreateSymlink, Class: ClassFiles, World: Outside},
		{Name: "fs/host.read_link", Fn: stdio.ReadLink, Class: ClassFiles, World: Outside},
		{Name: "fs/host.set_mode", Fn: stdio.SetMode, Class: ClassFiles, World: Outside},
		{Name: "fs/host.open_file", Fn: stdio.OpenFile, Class: ClassFiles, World: Outside},
		{Name: "fs/host.within", Fn: stdio.Within, Class: ClassFiles, World: Outside},
		{Name: "fs/host.release", Fn: stdio.Release, World: Inside},
		{Name: "io.file_read", Fn: rt.FileRead, World: Inside},
		{Name: "io.File.read_at", Fn: rt.FileReadAt, World: Inside},
		{Name: "io.File.write", Fn: rt.FileWrite, World: Inside},
		{Name: "io.File.write_at", Fn: rt.FileWriteAt, World: Inside},
		{Name: "io.File.seek", Fn: rt.FileSeek, World: Inside},
		{Name: "io.File.position", Fn: rt.FilePosition, World: Inside},
		{Name: "io.File.size", Fn: rt.FileSize, World: Inside},
		{Name: "io.File.truncate", Fn: rt.FileTruncate, World: Inside},
		{Name: "io.File.close", Fn: rt.FileClose, World: Inside},
		{Name: "fs/host.temp_dir", Fn: stdio.TempDir, Class: ClassFiles, World: Outside},
		{Name: "fs/host.make_temp_dir", Fn: stdio.MakeTempDir, Class: ClassFiles, World: Outside},
		{Name: "fs/host.current_dir", Fn: stdio.CurrentDir, Class: ClassFiles, World: Outside},
		{Name: "fs/host.new_sandbox", Fn: stdio.NewSandbox, World: Inside},
		{Name: "fs/host.sandbox_fail", Fn: stdio.SandboxFail, World: Inside},
		{Name: "fs/host.sandbox_files", Fn: stdio.SandboxFiles, World: Inside},
		{Name: "fs/host.sandbox_log", Fn: stdio.SandboxLog, World: Inside},
		{Name: "fs/host.sandbox_open", Fn: stdio.SandboxOpen, World: Inside},
		{Name: "http/host.new_client", Fn: stdhttp.NewClient, World: Inside},
		{Name: "http/host.default_client", Fn: stdhttp.DefaultClient, World: Inside},
		{Name: "http/host.close_idle", Fn: stdhttp.CloseIdle, World: Inside},
		{Name: "http/host.send", Fn: stdhttp.Send, Class: ClassNetwork, World: Outside},
		{Name: "http/host.status", Fn: stdhttp.Status, World: Inside},
		{Name: "http/host.protocol", Fn: stdhttp.Protocol, World: Inside},
		{Name: "http/host.headers", Fn: stdhttp.Headers, World: Inside},
		{Name: "http/host.trailers", Fn: stdhttp.Trailers, World: Inside},
		{Name: "http/host.body_bytes", Fn: stdhttp.BodyBytes, World: Inside},
		{Name: "http/host.body_reader", Fn: stdhttp.BodyReader, World: Inside},
		{Name: "http/host.close_body", Fn: stdhttp.CloseBody, World: Inside},
		{Name: "http/host.content_length", Fn: stdhttp.ContentLength, World: Inside},
		{Name: "http/host.transfer_encoding", Fn: stdhttp.TransferEncoding, World: Inside},
		{Name: "http/host.closes", Fn: stdhttp.Closes, World: Inside},
		{Name: "http/host.uncompressed", Fn: stdhttp.Uncompressed, World: Inside},
		{Name: "http/host.final_url", Fn: stdhttp.FinalURL, World: Inside},
		{Name: "http/host.canonical_key", Fn: stdhttp.CanonicalKey, World: Inside},
		{Name: "http/host.status_text", Fn: stdhttp.StatusText, World: Inside},
		{Name: "http/host.headers_wire", Fn: stdhttp.HeadersWire, World: Inside},
		{Name: "http/host.parse_http_version", Fn: stdhttp.ParseHTTPVersion, World: Inside},
		{Name: "http/host.basic_auth", Fn: stdhttp.BasicAuth, World: Inside},
		{Name: "http/host.add_cookie", Fn: stdhttp.AddCookie, World: Inside},
		{Name: "http/host.cross_origin_check", Fn: stdhttp.CrossOriginCheck, World: Inside},
		{Name: "http/host.listen", Fn: stdhttp.Listen, Class: ClassListen, World: Outside},
		{Name: "http/host.listen_tls", Fn: stdhttp.ListenTLS, Class: ClassListen, World: Outside},
		{Name: "http/host.with_tls", Fn: stdhttp.WithTLS, World: Inside},
		{Name: "http/host.test_certificate", Fn: stdhttp.TestCertificate, World: Outside},
		{Name: "http/host.certificate_from_pem", Fn: stdhttp.CertificateFromPEM, World: Inside},
		{Name: "http/host.certificate_load", Fn: stdhttp.CertificateLoad, Class: ClassFiles, World: Outside},
		{Name: "http/host.certificate_info", Fn: stdhttp.CertificateInfo, World: Inside},
		{Name: "http/host.tls_config", Fn: stdhttp.TLSConfig, PanicsPropagate: true, World: Outside},
		{Name: "http/host.tls_with_certificate", Fn: stdhttp.TLSWithCertificate, World: Inside},
		{Name: "http/host.cipher_suites", Fn: stdhttp.CipherSuites, World: Inside},
		{Name: "http/host.cipher_suite_name", Fn: stdhttp.CipherSuiteName, World: Inside},
		{Name: "http/host.dial_with", Fn: stdhttp.DialWith, Class: ClassConnect, World: Outside},
		{Name: "http/host.lookup", Fn: stdhttp.Lookup, Class: ClassConnect, World: Outside},
		{Name: "http/host.pipe", Fn: stdhttp.Pipe, World: Inside},
		{Name: "http/host.split_host_port", Fn: stdhttp.SplitHostPort, World: Inside},
		{Name: "http/host.join_host_port", Fn: stdhttp.JoinHostPort, World: Inside},
		{Name: "http/host.conn_close_read", Fn: stdhttp.ConnCloseRead, World: Inside},
		{Name: "http/host.conn_no_delay", Fn: stdhttp.ConnNoDelay, World: Inside},
		{Name: "http/host.conn_linger", Fn: stdhttp.ConnLinger, World: Inside},
		{Name: "http/host.conn_keep_alive", Fn: stdhttp.ConnKeepAlive, World: Inside},
		{Name: "http/host.conn_multipath", Fn: stdhttp.ConnMultipath, World: Inside},
		{Name: "http/host.tls_dial", Fn: stdhttp.TLSDial, Class: ClassConnect, World: Outside},
		{Name: "http/host.tls_client", Fn: stdhttp.TLSClient, World: Inside},
		{Name: "http/host.tls_server", Fn: stdhttp.TLSServer, World: Inside},
		{Name: "http/host.conn_reader", Fn: stdhttp.ConnReader, World: Inside},
		{Name: "http/host.conn_writer", Fn: stdhttp.ConnWriter, World: Inside},
		{Name: "http/host.conn_close", Fn: stdhttp.ConnClose, World: Inside},
		{Name: "http/host.conn_close_write", Fn: stdhttp.ConnCloseWrite, World: Inside},
		{Name: "http/host.conn_local", Fn: stdhttp.ConnLocal, World: Inside},
		{Name: "http/host.conn_remote", Fn: stdhttp.ConnRemote, World: Inside},
		{Name: "http/host.conn_deadline", Fn: stdhttp.ConnDeadline, World: Inside},
		{Name: "http/host.tls_state_parts", Fn: stdhttp.TLSStateParts, World: Inside},
		{Name: "http/host.hello_parts", Fn: stdhttp.HelloParts, World: Inside},
		{Name: "http/host.hello_supports", Fn: stdhttp.HelloSupports, World: Inside},
		{Name: "http/host.tls_wrap_listener", Fn: stdhttp.TLSWrapListener, World: Inside},
		{Name: "http/host.in_local", Fn: stdhttp.InLocal, World: Inside},
		{Name: "http/host.in_tls", Fn: stdhttp.InTLS, World: Inside},
		{Name: "http/host.extend_read_deadline", Fn: stdhttp.ExtendReadDeadline, World: Inside},
		{Name: "http/host.upgrade", Fn: stdhttp.Upgrade, World: Inside},
		{Name: "http/host.exchange_tls", Fn: stdhttp.ExchangeTLS, World: Inside},
		{Name: "http/host.new_tracer", Fn: stdhttp.NewTracer, World: Inside},
		{Name: "http/host.tracer_events", Fn: stdhttp.TracerEvents, World: Inside},
		{Name: "http/host.wire_read_request", Fn: stdhttp.WireReadRequest, World: Inside},
		{Name: "http/host.wire_read_response", Fn: stdhttp.WireReadResponse, World: Inside},
		{Name: "http/host.wire_parts", Fn: stdhttp.WireParts, World: Inside},
		{Name: "http/host.wire_body", Fn: stdhttp.WireBody, World: Inside},
		{Name: "http/host.wire_request", Fn: stdhttp.WireRequest, World: Inside},
		{Name: "http/host.wire_response", Fn: stdhttp.WireResponse, World: Inside},
		{Name: "http/host.chunked_reader", Fn: stdhttp.ChunkedReader, World: Inside},
		{Name: "http/host.chunked_writer", Fn: stdhttp.ChunkedWriter, World: Inside},
		{Name: "http/host.gzip_bytes", Fn: stdhttp.GzipBytes, World: Inside},
		{Name: "http/host.gunzip", Fn: stdhttp.Gunzip, World: Inside},
		{Name: "http/host.gzip_writer", Fn: stdhttp.GzipWriter, World: Inside},
		{Name: "http/host.gzip_reader", Fn: stdhttp.GzipReader, World: Inside},
		{Name: "http/host.multipart_reader", Fn: stdhttp.MultipartReader, World: Inside},
		{Name: "http/host.next_part", Fn: stdhttp.NextPart, World: Inside},
		{Name: "http/host.part_headers", Fn: stdhttp.PartHeaders, World: Inside},
		{Name: "http/host.part_reader", Fn: stdhttp.PartReader, World: Inside},
		{Name: "http/host.part_form_name", Fn: stdhttp.PartFormName, World: Inside},
		{Name: "http/host.part_file_name", Fn: stdhttp.PartFileName, World: Inside},
		{Name: "http/host.multipart_write", Fn: stdhttp.MultipartWrite, World: Inside},
		{Name: "http/host.close_connections", Fn: stdhttp.CloseConnections, World: Inside},
		{Name: "http/host.ws_accept_key", Fn: stdhttp.WsAcceptKey, World: Inside},
		{Name: "http/host.ws_valid_key", Fn: stdhttp.WsValidKey, World: Inside},
		{Name: "http/host.ws_open", Fn: stdhttp.WsOpen, World: Inside},
		{Name: "http/host.ws_connect", Fn: stdhttp.WsConnect, World: Outside},
		{Name: "http/host.ws_protocol", Fn: stdhttp.WsProtocol, World: Inside},
		{Name: "http/host.ws_id", Fn: stdhttp.WsID, World: Inside},
		{Name: "http/host.ws_receive", Fn: stdhttp.WsReceive, World: Inside},
		{Name: "http/host.ws_send", Fn: stdhttp.WsSend, World: Inside},
		{Name: "http/host.ws_ping", Fn: stdhttp.WsPing, World: Inside},
		{Name: "http/host.ws_close", Fn: stdhttp.WsClose, World: Inside},
		{Name: "http/host.ws_pipe", Fn: stdhttp.WsPipe, World: Inside},
		{Name: "http/host.tls_verify_hostname", Fn: stdhttp.TLSVerifyHostname, World: Inside},
		{Name: "http/host.net_listen_with", Fn: stdhttp.ListenWith, Class: ClassListen, World: Outside},
		{Name: "http/host.net_listener_deadline", Fn: stdhttp.NetListenerDeadline, World: Inside},
		{Name: "http/host.tls_listen", Fn: stdhttp.TLSListen, Class: ClassListen, World: Outside},
		{Name: "http/host.net_listener_address", Fn: stdhttp.NetListenerAddress, World: Inside},
		{Name: "http/host.net_accept", Fn: stdhttp.NetAccept, World: Inside},
		{Name: "http/host.net_listener_close", Fn: stdhttp.NetListenerClose, World: Inside},
		{Name: "http/host.address", Fn: stdhttp.Address, World: Inside},
		{Name: "http/host.next", Fn: stdhttp.Next, World: Inside},
		{Name: "http/host.shutdown", Fn: stdhttp.Shutdown, World: Inside},
		{Name: "http/host.in_method", Fn: stdhttp.InMethod, World: Inside},
		{Name: "http/host.in_host", Fn: stdhttp.InHost, World: Inside},
		{Name: "http/host.in_remote", Fn: stdhttp.InRemote, World: Inside},
		{Name: "http/host.in_target", Fn: stdhttp.InTarget, World: Inside},
		{Name: "http/host.in_protocol", Fn: stdhttp.InProtocol, World: Inside},
		{Name: "http/host.in_headers", Fn: stdhttp.InHeaders, World: Inside},
		{Name: "http/host.in_trailers", Fn: stdhttp.InTrailers, World: Inside},
		{Name: "http/host.in_content_length", Fn: stdhttp.InContentLength, World: Inside},
		{Name: "http/host.in_transfer_encoding", Fn: stdhttp.InTransferEncoding, World: Inside},
		{Name: "http/host.in_close", Fn: stdhttp.InClose, World: Inside},
		{Name: "http/host.in_body", Fn: stdhttp.InBody, World: Inside},
		{Name: "http/host.parse_multipart", Fn: stdhttp.ParseMultipart, World: Inside},
		{Name: "http/host.encode_multipart", Fn: stdhttp.EncodeMultipart, World: Inside},
		{Name: "http/host.file_content_disposition", Fn: stdhttp.FileContentDisposition, World: Inside},
		{Name: "http/host.respond", Fn: stdhttp.Respond, World: Inside},
		{Name: "http/host.produce", Fn: stdhttp.Produce, World: Inside},
		{Name: "http/host.finish", Fn: stdhttp.Finish, World: Inside},
		{Name: "http/host.answered_already", Fn: stdhttp.Answered, World: Inside},
		{Name: "http/host.detect_content_type", Fn: stdhttp.DetectContentType, World: Inside},
		{Name: "http/host.new_mux", Fn: stdhttp.NewMux, World: Inside},
		{Name: "http/host.check_pattern", Fn: stdhttp.CheckPattern, Pure: true},
		{Name: "http/host.route", Fn: stdhttp.Route, World: Inside},
		{Name: "http/host.answer_content", Fn: stdhttp.AnswerContent, World: Inside},
		{Name: "http/host.answer_redirect", Fn: stdhttp.AnswerRedirect, World: Inside},
		{Name: "http/host.format_time", Fn: stdhttp.FormatTime, World: Inside},
		{Name: "http/host.parse_time", Fn: stdhttp.ParseTime, World: Inside},
		{Name: "http/host.limit_body", Fn: stdhttp.LimitBody, World: Inside},
		{Name: "http/host.cookie_text", Fn: stdhttp.CookieText, World: Inside},
		{Name: "http/host.cookie_valid", Fn: stdhttp.CookieValid, World: Inside},
		{Name: "http/host.parse_cookie", Fn: stdhttp.ParseCookie, World: Inside},
		{Name: "http/host.parse_set_cookie", Fn: stdhttp.ParseSetCookie, World: Inside},
		{Name: "http/host.new_jar", Fn: stdhttp.NewJar, World: Inside},
		{Name: "http/host.jar_cookies", Fn: stdhttp.JarCookies, World: Inside},
		{Name: "http/host.jar_set", Fn: stdhttp.JarSet, World: Inside},
		{Name: "http/host.with_jar", Fn: stdhttp.WithJar, World: Inside},
		{Name: "process/host.start", Fn: stdprocess.Start, Class: ClassProcesses, World: Outside},
		{Name: "process/host.child_stdin", Fn: stdprocess.Stdin, World: Inside},
		{Name: "process/host.child_stdout", Fn: stdprocess.Stdout, World: Inside},
		{Name: "process/host.child_stderr", Fn: stdprocess.Stderr, World: Inside},
		{Name: "process/host.wait", Fn: stdprocess.Wait, World: Inside},
		{Name: "process/host.captured", Fn: stdprocess.Captured, World: Inside},
		{Name: "process/host.kill", Fn: stdprocess.Kill, World: Inside},
		{Name: "process/host.new_fake", Fn: stdprocess.NewFake, World: Inside},
		{Name: "process/host.fake_answer", Fn: stdprocess.FakeAnswer, World: Inside},
		{Name: "process/host.fake_refuse", Fn: stdprocess.FakeRefuse, World: Inside},
		{Name: "process/host.fake_calls", Fn: stdprocess.FakeCalls, World: Inside},
		{Name: "process/host.fake_command", Fn: stdprocess.FakeCommand, World: Inside},
		{Name: "process/host.fake_dir", Fn: stdprocess.FakeDir, World: Inside},
		{Name: "process/host.fake_env", Fn: stdprocess.FakeEnv, World: Inside},
		{Name: "process/host.fake_input", Fn: stdprocess.FakeInput, World: Inside},
		{Name: "csv/host.open", Fn: stdcsv.Open, World: Inside},
		{Name: "csv/host.next", Fn: stdcsv.Next, World: Inside},
		{Name: "csv/host.line", Fn: stdcsv.Line, World: Inside},
		{Name: "csv/host.stopped", Fn: stdcsv.Stopped, World: Inside},
		{Name: "csv/host.message", Fn: stdcsv.Message, World: Inside},
		{Name: "csv/host.failure", Fn: stdcsv.Failure, World: Inside},
		{Name: "csv/host.encode", Fn: stdcsv.Encode, Pure: true},
		{Name: "xml/host.open", Fn: stdxml.Open, World: Inside},
		{Name: "xml/host.next", Fn: stdxml.Next, World: Inside},
		{Name: "xml/host.name", Fn: stdxml.Name, World: Inside},
		{Name: "xml/host.attributes", Fn: stdxml.Attributes, World: Inside},
		{Name: "xml/host.namespaces", Fn: stdxml.Namespaces, World: Inside},
		{Name: "xml/host.text", Fn: stdxml.Text, World: Inside},
		{Name: "xml/host.target", Fn: stdxml.Target, World: Inside},
		{Name: "xml/host.position", Fn: stdxml.Position, World: Inside},
		{Name: "xml/host.stopped", Fn: stdxml.Stopped, World: Inside},
		{Name: "xml/host.message", Fn: stdxml.Message, World: Inside},
		{Name: "xml/host.failure", Fn: stdxml.Failure, World: Inside},
		{Name: "xml/host.escape_text", Fn: stdxml.EscapeText, Pure: true},
		{Name: "xml/host.escape_attribute", Fn: stdxml.EscapeAttribute, Pure: true},
		{Name: "xml/host.is_name", Fn: stdxml.IsName, Pure: true},
		{Name: "iter.as_count", Fn: rt.IntAsCount, World: Inside},
	}
	for i := range rows {
		rows[i].PanicsPropagate = true
	}
	return rows
}
