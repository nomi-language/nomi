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
	// keep unwinding the task. Set too on the Funcs rows whose job is to trap
	// (std/random's range checks); unset on the other Funcs rows, whose
	// adapters recover.
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
	// count), `decimal.Decimal.divide`, `round` and `sqrt` (their scale) and
	// `decimal.Decimal.pow` (its exponent). Reading the program's own CLDR
	// data, as locale-aware formatting does, is not acting outside the
	// machine.
	Pure bool
}

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
		{Name: "calendar.offset_from_instant_nanos", Fn: stdcalendar.BoundOffsetFromInstant, Pure: true},
		{Name: "calendar.offset_parse_raw", Fn: stdcalendar.BoundOffsetParse, Pure: true},
		{Name: "calendar.offset_with_offset_raw", Fn: stdcalendar.BoundOffsetWithOffset, Pure: true},
		{Name: "calendar.pattern_check", Fn: stdformat.FFICheckPattern, Pure: true},
		{Name: "calendar.relative_format_raw", Fn: stdformat.FFIFormatRelative, Pure: true},
		{Name: "calendar.time_add_nanos", Fn: stdcalendar.BoundTimeAddNanos, Pure: true},
		{Name: "calendar.time_parse_pattern", Fn: stdformat.FFIParseTimePattern, Pure: true},
		{Name: "calendar.time_parse_raw", Fn: stdcalendar.BoundTimeParse, Pure: true},
		{Name: "calendar.time_parse_style", Fn: stdformat.FFIParseTimeStyle, Pure: true},
		{Name: "calendar.zoned_add_days", Fn: stdcalendar.BoundZonedAddDays, Pure: true},
		{Name: "calendar.zoned_add_hours", Fn: stdcalendar.BoundZonedAddHours, Pure: true},
		{Name: "calendar.zoned_add_microseconds", Fn: stdcalendar.BoundZonedAddMicroseconds, Pure: true},
		{Name: "calendar.zoned_add_milliseconds", Fn: stdcalendar.BoundZonedAddMilliseconds, Pure: true},
		{Name: "calendar.zoned_add_minutes", Fn: stdcalendar.BoundZonedAddMinutes, Pure: true},
		{Name: "calendar.zoned_add_months", Fn: stdcalendar.BoundZonedAddMonths, Pure: true},
		{Name: "calendar.zoned_add_nanoseconds", Fn: stdcalendar.BoundZonedAddNanoseconds, Pure: true},
		{Name: "calendar.zoned_add_seconds", Fn: stdcalendar.BoundZonedAddSeconds, Pure: true},
		{Name: "calendar.zoned_add_years", Fn: stdcalendar.BoundZonedAddYears, Pure: true},
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
		{Name: "decimal.decimal_refused", Fn: stdformat.FFIRefused, PanicsPropagate: true},
		{Name: "float.float_format", Fn: stdformat.FFIFormatFloat, PanicsPropagate: true, Pure: true},
		{Name: "float.float_format_percent", Fn: stdformat.FFIFormatFloatPercent, PanicsPropagate: true, Pure: true},
		{Name: "float.float_parse_in", Fn: stdformat.FFIParseFloat, Pure: true},
		{Name: "float.float_parse_percent_in", Fn: stdformat.FFIParseFloatPercent, Pure: true},
		{Name: "int.int_format", Fn: stdformat.FFIFormatInt, PanicsPropagate: true, Pure: true},
		{Name: "int.int_parse_in", Fn: stdformat.FFIParseInt, Pure: true},
		{Name: "locale.locale_available", Fn: stdlocale.FFIAvailable, Pure: true},
		{Name: "locale.locale_load_raw", Fn: stdlocale.FFILoad},
		{Name: "locale.locale_loaded_raw", Fn: stdlocale.FFILoaded},
		{Name: "phrase/format.format_raw", Fn: stdphraseformat.FFIFormat, PanicsPropagate: true, Pure: true},
		{Name: "phrase/format.load_raw", Fn: stdphraseformat.FFILoad},
		{Name: "phrase/format.noun_raw", Fn: stdphraseformat.FFINoun, PanicsPropagate: true},
		{Name: "money/format.money_format_raw", Fn: stdformat.FFIFormatMoney, Pure: true},
		{Name: "money/format.money_mixed", Fn: stdformat.FFIMoneyMixed, PanicsPropagate: true},
		{Name: "money/format.money_parse_raw", Fn: stdformat.FFIParseMoney, Pure: true},
		{Name: "money/format.money_refused", Fn: stdformat.FFIRefused, PanicsPropagate: true},
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
		{Name: "random.between_state", Fn: stdrandom.FFIBetween},
		{Name: "random.os_state", Fn: stdrandom.FFIFromOS},
		{Name: "random.secure_bytes", Fn: stdrandom.FFISecureBytes, PanicsPropagate: true},
		{Name: "random.require_float_range", Fn: stdrandom.FFIRequireFloatRange, PanicsPropagate: true},
		{Name: "random.require_int_range", Fn: stdrandom.FFIRequireIntRange, PanicsPropagate: true},
		{Name: "random.unit_float_state", Fn: stdrandom.FFIUnitFloat},
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
		{Name: "strings.String.is_upper", Fn: rt.StringIsUpper},
		{Name: "strings.String.is_lower", Fn: rt.StringIsLower},
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
		{Name: "hash.md5", Fn: rt.MD5},
		{Name: "hash.sha1", Fn: rt.SHA1},
		{Name: "hash.hmac_sha256", Fn: rt.HMACSHA256, Pure: true},
		{Name: "hash.hmac_sha512", Fn: rt.HMACSHA512, Pure: true},
		{Name: "hash.equal_in_constant_time", Fn: rt.EqualInConstantTime, Pure: true},
		{Name: "hash.pbkdf2_sha256", Fn: rt.PBKDF2SHA256},
		{Name: "signatures/host.generate_ed25519", Fn: stdhttp.SigGenerateEd25519},
		{Name: "signatures/host.generate_p256", Fn: stdhttp.SigGenerateP256},
		{Name: "signatures/host.generate_rsa", Fn: stdhttp.SigGenerateRSA},
		{Name: "signatures/host.private_from_der", Fn: stdhttp.SigPrivateFromDER},
		{Name: "signatures/host.private_from_pem", Fn: stdhttp.SigPrivateFromPEM},
		{Name: "signatures/host.private_to_pem", Fn: stdhttp.SigPrivateToPEM},
		{Name: "signatures/host.private_kind", Fn: stdhttp.SigPrivateKind},
		{Name: "signatures/host.public_of", Fn: stdhttp.SigPublicOf},
		{Name: "signatures/host.public_from_der", Fn: stdhttp.SigPublicFromDER},
		{Name: "signatures/host.public_from_pem", Fn: stdhttp.SigPublicFromPEM},
		{Name: "signatures/host.public_to_pem", Fn: stdhttp.SigPublicToPEM},
		{Name: "signatures/host.public_kind", Fn: stdhttp.SigPublicKind},
		{Name: "signatures/host.ed25519_from_seed", Fn: stdhttp.SigEd25519FromSeed},
		{Name: "signatures/host.ed25519_seed", Fn: stdhttp.SigEd25519Seed},
		{Name: "signatures/host.ed25519_public_from_bytes", Fn: stdhttp.SigEd25519PublicFromBytes},
		{Name: "signatures/host.ed25519_public_bytes", Fn: stdhttp.SigEd25519PublicBytes},
		{Name: "signatures/host.sign", Fn: stdhttp.SigSign},
		{Name: "signatures/host.is_valid", Fn: stdhttp.SigIsValid},
		{Name: "http/host.seal", Fn: rt.Seal},
		{Name: "http/host.open_sealed", Fn: rt.Open},
		{Name: "base64.standard", Fn: rt.Base64Encode, Pure: true},
		{Name: "base64.decode", Fn: rt.Base64Decode, Pure: true},
		{Name: "base64.url_safe", Fn: rt.Base64URLEncode, Pure: true},
		{Name: "base64.decode_url", Fn: rt.Base64URLDecode, Pure: true},
		{Name: "hex.encode", Fn: rt.HexEncode, Pure: true},
		{Name: "hex.decode", Fn: rt.HexDecode, Pure: true},
		{Name: "html/host.escape", Fn: rt.HTMLEscape},
		{Name: "html/host.filter_url", Fn: rt.HTMLFilterURL},
		{Name: "html/host.escape_url_path", Fn: rt.HTMLEscapeURLPath},
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
		{Name: "averageable.int_ratio_to_float", Fn: rt.IntRatioToFloat},
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
		{Name: "float.require_clamp_bounds", Fn: rt.FloatRequireClampBounds, Pure: true},
		{Name: "comparable.clamp_reversed", Fn: rt.ComparableClampReversed},
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
		{Name: "duration.Duration.to_string", Fn: rt.DurationToString, Pure: true},
		{Name: "instant.Instant.now", Fn: rt.InstantNow},
		{Name: "int.Int.to_codepoint", Fn: rt.IntToCodepoint, Pure: true},
		{Name: "code_points.Codepoint.to_string", Fn: rt.CodepointToString, Pure: true},
		{Name: "code_points.Codepoint.is_letter", Fn: rt.CodepointIsLetter, Pure: true},
		{Name: "code_points.Codepoint.is_whitespace", Fn: rt.CodepointIsWhitespace, Pure: true},
		{Name: "code_points.Codepoint.is_upper", Fn: rt.CodepointIsUpper, Pure: true},
		{Name: "code_points.Codepoint.is_lower", Fn: rt.CodepointIsLower, Pure: true},
		{Name: "strings.String.to_codepoints", Fn: rt.StringToCodepoints, Pure: true},
		{Name: "context.Context.with_timeout", Fn: rt.ContextWithTimeout},
		{Name: "context.Context.root", Fn: rt.ContextRoot},
		{Name: "json.Json.decode", Fn: rt.JsonDecode, Pure: true},
		{Name: "json.Json.encode", Fn: rt.JsonEncode, Pure: true},
		{Name: "timer.sleep", Fn: rt.TimerSleep},
		{Name: "supervisors.Supervisor.new_exact", Fn: rt.SupervisorNewExact},
		{Name: "supervisors.Supervisor.flush_bounded", Fn: rt.SupervisorFlushBounded},
		{Name: "assertions.AssertionFailure.format", Fn: rt.FormatNomiAssertionFailure, Pure: true},
		{Name: "int.Int.to_decimal", Fn: rt.IntToDecimal, Pure: true},
		{Name: "duration.nanos_in_range", Fn: rt.DurationNanosInRange, Pure: true},
		{Name: "instant.nanos_in_range", Fn: rt.InstantNanosInRange, Pure: true},
		{Name: "strings.String.to_decimal", Fn: rt.StringToDecimal, Pure: true},
		{Name: "strings.String.to_float", Fn: rt.StringToFloat, Pure: true},
		{Name: "decimal.Decimal.to_int", Fn: rt.DecimalToInt, Pure: true},
		{Name: "decimal.Decimal.to_float", Fn: rt.DecimalToFloat, Pure: true},
		{Name: "float.Float.to_decimal", Fn: rt.FloatToDecimal, Pure: true},
		{Name: "decimal.Decimal.divide", Fn: rt.DecimalDivide},
		{Name: "decimal.Decimal.divide_exact", Fn: rt.DecimalDivideExact, Pure: true},
		{Name: "decimal.Decimal.round", Fn: rt.DecimalRound},
		{Name: "decimal.Decimal.pow", Fn: rt.DecimalPow},
		{Name: "decimal.Decimal.sqrt", Fn: rt.DecimalSqrt},
		{Name: "decimal.Decimal.normalize", Fn: rt.DecimalNormalize, Pure: true},
		{Name: "decimal.Decimal.scale", Fn: rt.DecimalScale, Pure: true},
		{Name: "strings.String.repeat", Fn: rt.StringRepeat},
		{Name: "context.Context.deadline", Fn: rt.ContextDeadline},
		{Name: "context.Context.deadline_remaining", Fn: rt.ContextDeadlineRemaining},
		{Name: "context.Context.with_deadline", Fn: rt.ContextWithDeadline},
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
		{Name: "dynamic.Dynamic.hash", Fn: rt.DynamicHashInt},
		{Name: "dynamic.Dynamic.has", Fn: rt.DynamicHas, Pure: true},
		{Name: "dynamic.Dynamic.inspect", Fn: rt.DynamicInspect, Pure: true},
		{Name: "json.Json.to_dynamic", Fn: rt.JsonToDynamic, Pure: true},
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
		{Name: "io.Writer.discard", Fn: rt.WriterDiscard},
		{Name: "io.writer_from_fn", Fn: rt.WriterFromFn},
		{Name: "io.memory_writer", Fn: rt.WriterToMemory},
		{Name: "io.collected", Fn: rt.WriterCollected},
		// An embedded file is read from the program itself, which every
		// target has: no class.
		{Name: "embed/host.read", Fn: rt.EmbeddedRead},
		{Name: "embed/host.sum", Fn: rt.EmbeddedSum},
		{Name: "fs/host.read", Fn: stdio.Read, Class: ClassFiles},
		{Name: "fs/host.read_text", Fn: stdio.ReadText, Class: ClassFiles},
		{Name: "fs/host.write", Fn: stdio.Write, Class: ClassFiles},
		{Name: "fs/host.open", Fn: stdio.Open, Class: ClassFiles},
		{Name: "fs/host.create", Fn: stdio.Create, Class: ClassFiles},
		{Name: "fs/host.list", Fn: stdio.List, Class: ClassFiles},
		{Name: "fs/host.create_dir", Fn: stdio.CreateDir, Class: ClassFiles},
		{Name: "fs/host.remove", Fn: stdio.Remove, Class: ClassFiles},
		{Name: "fs/host.remove_all", Fn: stdio.RemoveAll, Class: ClassFiles},
		{Name: "fs/host.rename", Fn: stdio.Rename, Class: ClassFiles},
		{Name: "fs/host.copy", Fn: stdio.Copy, Class: ClassFiles},
		{Name: "fs/host.metadata", Fn: stdio.Metadata, Class: ClassFiles},
		{Name: "fs/host.link_metadata", Fn: stdio.LinkMetadata, Class: ClassFiles},
		{Name: "fs/host.create_symlink", Fn: stdio.CreateSymlink, Class: ClassFiles},
		{Name: "fs/host.read_link", Fn: stdio.ReadLink, Class: ClassFiles},
		{Name: "fs/host.set_mode", Fn: stdio.SetMode, Class: ClassFiles},
		{Name: "fs/host.open_file", Fn: stdio.OpenFile, Class: ClassFiles},
		{Name: "fs/host.within", Fn: stdio.Within, Class: ClassFiles},
		{Name: "fs/host.release", Fn: stdio.Release},
		{Name: "io.file_read", Fn: rt.FileRead},
		{Name: "io.File.read_at", Fn: rt.FileReadAt},
		{Name: "io.File.write", Fn: rt.FileWrite},
		{Name: "io.File.write_at", Fn: rt.FileWriteAt},
		{Name: "io.File.seek", Fn: rt.FileSeek},
		{Name: "io.File.position", Fn: rt.FilePosition},
		{Name: "io.File.size", Fn: rt.FileSize},
		{Name: "io.File.truncate", Fn: rt.FileTruncate},
		{Name: "io.File.close", Fn: rt.FileClose},
		{Name: "fs/host.temp_dir", Fn: stdio.TempDir, Class: ClassFiles},
		{Name: "fs/host.make_temp_dir", Fn: stdio.MakeTempDir, Class: ClassFiles},
		{Name: "fs/host.current_dir", Fn: stdio.CurrentDir, Class: ClassFiles},
		{Name: "fs/host.new_sandbox", Fn: stdio.NewSandbox},
		{Name: "fs/host.sandbox_fail", Fn: stdio.SandboxFail},
		{Name: "fs/host.sandbox_files", Fn: stdio.SandboxFiles},
		{Name: "fs/host.sandbox_log", Fn: stdio.SandboxLog},
		{Name: "fs/host.sandbox_open", Fn: stdio.SandboxOpen},
		{Name: "http/host.new_client", Fn: stdhttp.NewClient},
		{Name: "http/host.default_client", Fn: stdhttp.DefaultClient},
		{Name: "http/host.close_idle", Fn: stdhttp.CloseIdle},
		{Name: "http/host.send", Fn: stdhttp.Send, Class: ClassNetwork},
		{Name: "http/host.status", Fn: stdhttp.Status},
		{Name: "http/host.protocol", Fn: stdhttp.Protocol},
		{Name: "http/host.headers", Fn: stdhttp.Headers},
		{Name: "http/host.trailers", Fn: stdhttp.Trailers},
		{Name: "http/host.body_bytes", Fn: stdhttp.BodyBytes},
		{Name: "http/host.body_reader", Fn: stdhttp.BodyReader},
		{Name: "http/host.close_body", Fn: stdhttp.CloseBody},
		{Name: "http/host.content_length", Fn: stdhttp.ContentLength},
		{Name: "http/host.transfer_encoding", Fn: stdhttp.TransferEncoding},
		{Name: "http/host.closes", Fn: stdhttp.Closes},
		{Name: "http/host.uncompressed", Fn: stdhttp.Uncompressed},
		{Name: "http/host.final_url", Fn: stdhttp.FinalURL},
		{Name: "http/host.canonical_key", Fn: stdhttp.CanonicalKey},
		{Name: "http/host.status_text", Fn: stdhttp.StatusText},
		{Name: "http/host.headers_wire", Fn: stdhttp.HeadersWire},
		{Name: "http/host.parse_http_version", Fn: stdhttp.ParseHTTPVersion},
		{Name: "http/host.basic_auth", Fn: stdhttp.BasicAuth},
		{Name: "http/host.add_cookie", Fn: stdhttp.AddCookie},
		{Name: "http/host.cross_origin_check", Fn: stdhttp.CrossOriginCheck},
		{Name: "http/host.listen", Fn: stdhttp.Listen, Class: ClassListen},
		{Name: "http/host.listen_tls", Fn: stdhttp.ListenTLS, Class: ClassListen},
		{Name: "http/host.with_tls", Fn: stdhttp.WithTLS},
		{Name: "http/host.test_certificate", Fn: stdhttp.TestCertificate},
		{Name: "http/host.certificate_from_pem", Fn: stdhttp.CertificateFromPEM},
		{Name: "http/host.certificate_load", Fn: stdhttp.CertificateLoad, Class: ClassFiles},
		{Name: "http/host.certificate_info", Fn: stdhttp.CertificateInfo},
		{Name: "http/host.tls_config", Fn: stdhttp.TLSConfig},
		{Name: "http/host.tls_with_certificate", Fn: stdhttp.TLSWithCertificate},
		{Name: "http/host.cipher_suites", Fn: stdhttp.CipherSuites},
		{Name: "http/host.cipher_suite_name", Fn: stdhttp.CipherSuiteName},
		{Name: "http/host.dial_with", Fn: stdhttp.DialWith, Class: ClassConnect},
		{Name: "http/host.lookup", Fn: stdhttp.Lookup, Class: ClassConnect},
		{Name: "http/host.pipe", Fn: stdhttp.Pipe},
		{Name: "http/host.split_host_port", Fn: stdhttp.SplitHostPort},
		{Name: "http/host.join_host_port", Fn: stdhttp.JoinHostPort},
		{Name: "http/host.conn_close_read", Fn: stdhttp.ConnCloseRead},
		{Name: "http/host.conn_no_delay", Fn: stdhttp.ConnNoDelay},
		{Name: "http/host.conn_linger", Fn: stdhttp.ConnLinger},
		{Name: "http/host.conn_keep_alive", Fn: stdhttp.ConnKeepAlive},
		{Name: "http/host.conn_multipath", Fn: stdhttp.ConnMultipath},
		{Name: "http/host.tls_dial", Fn: stdhttp.TLSDial, Class: ClassConnect},
		{Name: "http/host.tls_client", Fn: stdhttp.TLSClient},
		{Name: "http/host.tls_server", Fn: stdhttp.TLSServer},
		{Name: "http/host.conn_reader", Fn: stdhttp.ConnReader},
		{Name: "http/host.conn_writer", Fn: stdhttp.ConnWriter},
		{Name: "http/host.conn_close", Fn: stdhttp.ConnClose},
		{Name: "http/host.conn_close_write", Fn: stdhttp.ConnCloseWrite},
		{Name: "http/host.conn_local", Fn: stdhttp.ConnLocal},
		{Name: "http/host.conn_remote", Fn: stdhttp.ConnRemote},
		{Name: "http/host.conn_deadline", Fn: stdhttp.ConnDeadline},
		{Name: "http/host.tls_state_parts", Fn: stdhttp.TLSStateParts},
		{Name: "http/host.hello_parts", Fn: stdhttp.HelloParts},
		{Name: "http/host.hello_supports", Fn: stdhttp.HelloSupports},
		{Name: "http/host.tls_wrap_listener", Fn: stdhttp.TLSWrapListener},
		{Name: "http/host.in_local", Fn: stdhttp.InLocal},
		{Name: "http/host.in_tls", Fn: stdhttp.InTLS},
		{Name: "http/host.extend_read_deadline", Fn: stdhttp.ExtendReadDeadline},
		{Name: "http/host.upgrade", Fn: stdhttp.Upgrade},
		{Name: "http/host.exchange_tls", Fn: stdhttp.ExchangeTLS},
		{Name: "http/host.new_tracer", Fn: stdhttp.NewTracer},
		{Name: "http/host.tracer_events", Fn: stdhttp.TracerEvents},
		{Name: "http/host.wire_read_request", Fn: stdhttp.WireReadRequest},
		{Name: "http/host.wire_read_response", Fn: stdhttp.WireReadResponse},
		{Name: "http/host.wire_parts", Fn: stdhttp.WireParts},
		{Name: "http/host.wire_body", Fn: stdhttp.WireBody},
		{Name: "http/host.wire_request", Fn: stdhttp.WireRequest},
		{Name: "http/host.wire_response", Fn: stdhttp.WireResponse},
		{Name: "http/host.chunked_reader", Fn: stdhttp.ChunkedReader},
		{Name: "http/host.chunked_writer", Fn: stdhttp.ChunkedWriter},
		{Name: "http/host.gzip_bytes", Fn: stdhttp.GzipBytes},
		{Name: "http/host.gunzip", Fn: stdhttp.Gunzip},
		{Name: "http/host.gzip_writer", Fn: stdhttp.GzipWriter},
		{Name: "http/host.gzip_reader", Fn: stdhttp.GzipReader},
		{Name: "http/host.multipart_reader", Fn: stdhttp.MultipartReader},
		{Name: "http/host.next_part", Fn: stdhttp.NextPart},
		{Name: "http/host.part_headers", Fn: stdhttp.PartHeaders},
		{Name: "http/host.part_reader", Fn: stdhttp.PartReader},
		{Name: "http/host.part_form_name", Fn: stdhttp.PartFormName},
		{Name: "http/host.part_file_name", Fn: stdhttp.PartFileName},
		{Name: "http/host.multipart_write", Fn: stdhttp.MultipartWrite},
		{Name: "http/host.close_connections", Fn: stdhttp.CloseConnections},
		{Name: "http/host.ws_accept_key", Fn: stdhttp.WsAcceptKey},
		{Name: "http/host.ws_valid_key", Fn: stdhttp.WsValidKey},
		{Name: "http/host.ws_open", Fn: stdhttp.WsOpen},
		{Name: "http/host.ws_connect", Fn: stdhttp.WsConnect},
		{Name: "http/host.ws_protocol", Fn: stdhttp.WsProtocol},
		{Name: "http/host.ws_id", Fn: stdhttp.WsID},
		{Name: "http/host.ws_receive", Fn: stdhttp.WsReceive},
		{Name: "http/host.ws_send", Fn: stdhttp.WsSend},
		{Name: "http/host.ws_ping", Fn: stdhttp.WsPing},
		{Name: "http/host.ws_close", Fn: stdhttp.WsClose},
		{Name: "http/host.ws_pipe", Fn: stdhttp.WsPipe},
		{Name: "http/host.tls_verify_hostname", Fn: stdhttp.TLSVerifyHostname},
		{Name: "http/host.net_listen_with", Fn: stdhttp.ListenWith, Class: ClassListen},
		{Name: "http/host.net_listener_deadline", Fn: stdhttp.NetListenerDeadline},
		{Name: "http/host.tls_listen", Fn: stdhttp.TLSListen, Class: ClassListen},
		{Name: "http/host.net_listener_address", Fn: stdhttp.NetListenerAddress},
		{Name: "http/host.net_accept", Fn: stdhttp.NetAccept},
		{Name: "http/host.net_listener_close", Fn: stdhttp.NetListenerClose},
		{Name: "http/host.address", Fn: stdhttp.Address},
		{Name: "http/host.next", Fn: stdhttp.Next},
		{Name: "http/host.shutdown", Fn: stdhttp.Shutdown},
		{Name: "http/host.in_method", Fn: stdhttp.InMethod},
		{Name: "http/host.in_url", Fn: stdhttp.InURL},
		{Name: "http/host.in_host", Fn: stdhttp.InHost},
		{Name: "http/host.in_remote", Fn: stdhttp.InRemote},
		{Name: "http/host.in_target", Fn: stdhttp.InTarget},
		{Name: "http/host.in_protocol", Fn: stdhttp.InProtocol},
		{Name: "http/host.in_headers", Fn: stdhttp.InHeaders},
		{Name: "http/host.in_trailers", Fn: stdhttp.InTrailers},
		{Name: "http/host.in_content_length", Fn: stdhttp.InContentLength},
		{Name: "http/host.in_transfer_encoding", Fn: stdhttp.InTransferEncoding},
		{Name: "http/host.in_close", Fn: stdhttp.InClose},
		{Name: "http/host.in_body", Fn: stdhttp.InBody},
		{Name: "http/host.parse_multipart", Fn: stdhttp.ParseMultipart},
		{Name: "http/host.encode_multipart", Fn: stdhttp.EncodeMultipart},
		{Name: "http/host.file_content_disposition", Fn: stdhttp.FileContentDisposition},
		{Name: "http/host.respond", Fn: stdhttp.Respond},
		{Name: "http/host.produce", Fn: stdhttp.Produce},
		{Name: "http/host.finish", Fn: stdhttp.Finish},
		{Name: "http/host.answered_already", Fn: stdhttp.Answered},
		{Name: "http/host.detect_content_type", Fn: stdhttp.DetectContentType},
		{Name: "http/host.new_mux", Fn: stdhttp.NewMux},
		{Name: "http/host.check_pattern", Fn: stdhttp.CheckPattern, Pure: true},
		{Name: "http/host.route", Fn: stdhttp.Route},
		{Name: "http/host.answer_content", Fn: stdhttp.AnswerContent},
		{Name: "http/host.answer_redirect", Fn: stdhttp.AnswerRedirect},
		{Name: "http/host.format_time", Fn: stdhttp.FormatTime},
		{Name: "http/host.parse_time", Fn: stdhttp.ParseTime},
		{Name: "http/host.limit_body", Fn: stdhttp.LimitBody},
		{Name: "http/host.cookie_text", Fn: stdhttp.CookieText},
		{Name: "http/host.cookie_valid", Fn: stdhttp.CookieValid},
		{Name: "http/host.parse_cookie", Fn: stdhttp.ParseCookie},
		{Name: "http/host.parse_set_cookie", Fn: stdhttp.ParseSetCookie},
		{Name: "http/host.new_jar", Fn: stdhttp.NewJar},
		{Name: "http/host.jar_cookies", Fn: stdhttp.JarCookies},
		{Name: "http/host.jar_set", Fn: stdhttp.JarSet},
		{Name: "http/host.with_jar", Fn: stdhttp.WithJar},
		{Name: "process/host.start", Fn: stdprocess.Start, Class: ClassProcesses},
		{Name: "process/host.child_stdin", Fn: stdprocess.Stdin},
		{Name: "process/host.child_stdout", Fn: stdprocess.Stdout},
		{Name: "process/host.child_stderr", Fn: stdprocess.Stderr},
		{Name: "process/host.wait", Fn: stdprocess.Wait},
		{Name: "process/host.captured", Fn: stdprocess.Captured},
		{Name: "process/host.kill", Fn: stdprocess.Kill},
		{Name: "process/host.new_fake", Fn: stdprocess.NewFake},
		{Name: "process/host.fake_answer", Fn: stdprocess.FakeAnswer},
		{Name: "process/host.fake_refuse", Fn: stdprocess.FakeRefuse},
		{Name: "process/host.fake_calls", Fn: stdprocess.FakeCalls},
		{Name: "process/host.fake_command", Fn: stdprocess.FakeCommand},
		{Name: "process/host.fake_dir", Fn: stdprocess.FakeDir},
		{Name: "process/host.fake_env", Fn: stdprocess.FakeEnv},
		{Name: "process/host.fake_input", Fn: stdprocess.FakeInput},
		{Name: "csv/host.open", Fn: stdcsv.Open},
		{Name: "csv/host.next", Fn: stdcsv.Next},
		{Name: "csv/host.line", Fn: stdcsv.Line},
		{Name: "csv/host.stopped", Fn: stdcsv.Stopped},
		{Name: "csv/host.message", Fn: stdcsv.Message},
		{Name: "csv/host.failure", Fn: stdcsv.Failure},
		{Name: "csv/host.encode", Fn: stdcsv.Encode, Pure: true},
		{Name: "xml/host.open", Fn: stdxml.Open},
		{Name: "xml/host.next", Fn: stdxml.Next},
		{Name: "xml/host.name", Fn: stdxml.Name},
		{Name: "xml/host.attributes", Fn: stdxml.Attributes},
		{Name: "xml/host.namespaces", Fn: stdxml.Namespaces},
		{Name: "xml/host.text", Fn: stdxml.Text},
		{Name: "xml/host.target", Fn: stdxml.Target},
		{Name: "xml/host.position", Fn: stdxml.Position},
		{Name: "xml/host.stopped", Fn: stdxml.Stopped},
		{Name: "xml/host.message", Fn: stdxml.Message},
		{Name: "xml/host.failure", Fn: stdxml.Failure},
		{Name: "xml/host.escape_text", Fn: stdxml.EscapeText, Pure: true},
		{Name: "xml/host.escape_attribute", Fn: stdxml.EscapeAttribute, Pure: true},
		{Name: "xml/host.is_name", Fn: stdxml.IsName, Pure: true},
		{Name: "iter.as_count", Fn: rt.IntAsCount},
	}
	for i := range rows {
		rows[i].PanicsPropagate = true
	}
	return rows
}
