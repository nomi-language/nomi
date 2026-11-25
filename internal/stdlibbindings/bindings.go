package stdlibbindings

import (
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdcalendar"
	"github.com/nomi-language/nomi/internal/stdcipher"
	"github.com/nomi-language/nomi/internal/stdcompress"
	"github.com/nomi-language/nomi/internal/stdcsv"
	"github.com/nomi-language/nomi/internal/stdformat"
	"github.com/nomi-language/nomi/internal/stdhash"
	"github.com/nomi-language/nomi/internal/stdhttp"
	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/stdpasswords"
	"github.com/nomi-language/nomi/internal/stdphraseformat"
	"github.com/nomi-language/nomi/internal/stdplural"
	"github.com/nomi-language/nomi/internal/stdprocess"
	"github.com/nomi-language/nomi/internal/stdrandom"
	"github.com/nomi-language/nomi/internal/stdregex"
	"github.com/nomi-language/nomi/internal/stdstrings"
	"github.com/nomi-language/nomi/internal/stdsystem"
	"github.com/nomi-language/nomi/internal/stdterminal"
	"github.com/nomi-language/nomi/internal/stdtoml"
	"github.com/nomi-language/nomi/internal/stdwasm"
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
	// Class is the effect the function needs from the target it runs on
	// (ClassFiles), or "" for one every target has. `nomi build` refuses a
	// target that lacks a class the program reaches (Classes).
	Class string
	// Pure marks a function that computes its answer from its operands
	// alone and acts on nothing outside the machine: no output, files,
	// network, clock, the system's entropy, tasks or channels. A crossing
	// into it may run where an effect may not (a subtype rule, a typed
	// literal's handler, a `check`, a debugger's evaluation; the vm package
	// doc, "Effects"). Unset, the crossing is an effect. What a call costs
	// does not decide it: a function whose cost grows with an operand's
	// value is Pure and also Unbounded. Reading the program's own CLDR
	// data, as locale-aware formatting does, is not acting outside the
	// machine.
	Pure bool
	// Unbounded marks a Pure function whose one call costs more as an
	// operand's value grows, rather than its size: `strings.String.repeat`
	// (its count), `decimal.Decimal.divide`, `round`, `round_exact`, `sqrt`
	// and `sqrt_exact` (their scale), `decimal.Decimal.pow` (its exponent),
	// `hash.pbkdf2_sha256` (its iterations), `hash.shake128`, `shake256`
	// and `bytes.Bytes.fill` (the length asked for), `compress/host.decode`
	// and `decode_at` (the limit the data may expand to), and
	// `wasm/host.compile` and `wasm/host.run` (what the module's code
	// does). A call into Go is not interrupted, and an
	// evaluation's steps bound only the size of what it builds, so an
	// evaluation that must end (a subtype rule, a typed literal's
	// handler, a `check`, an editor's or a debugger's evaluation) refuses a
	// call into one, as unbounded and not as an effect. Only a Pure row sets
	// it (TestEveryUnboundedRowIsPure).
	Unbounded bool
	// Seeded marks a Pure function whose answer is a hash keyed by the
	// run's hash seed (rt's hashkey.go), so the same operands give a
	// different answer in another run: `strings.String.hash`,
	// `bytes.Bytes.hash`, `decimal.Decimal.hash` and
	// `dynamic.Dynamic.hash`. A call to one is no effect. An evaluation
	// whose answer the compiler keeps for the program's runs (a subtype
	// rule, a typed literal's handler, a `check`) refuses a call into one,
	// since its answer could differ when the program runs. Only a Pure row
	// sets it (TestEverySeededRowIsPure).
	Seeded bool
	// Held marks a function that acts only through a value its operand
	// already holds (a Reader, a Writer, a File, a connection, a request,
	// a process, a fake, a token reader), whose effect an effects list
	// names where the value was made. An effects list names no call to
	// it (package doc, "Effects and World"). It is still an effect: the
	// evaluations that refuse effects refuse it. A Held row is not Pure
	// and is Inside (TestEveryRowDecidesItsWorld).
	Held bool
	// World says whether a call to the function acts outside the machine,
	// the build fact a target is checked by (package doc, "Effects and
	// World"). A Pure row is Inside and leaves it
	// unset; every other row sets it, and TestEveryRowDecidesItsWorld
	// fails for one that does not.
	World World
}

// World is whether a host function acts on, or reads from, something
// outside the machine (package doc, "Effects and World"): a build fact,
// which decides what a target can run, not what an effects list names.
// The zero value is no decision.
type World uint8

const (
	// Inside is a function that acts only on the machine and the values
	// it is given.
	Inside World = iota + 1
	// Outside is a function that acts on, or reads from, something outside
	// the machine.
	Outside
)

// ClassFiles is the effect class of a function that reads or writes the
// file system. A js/wasm module lacks it.
const ClassFiles = "files"

// ClassProcesses is the effect class of a function that starts a
// program. A js/wasm or wasip1 module lacks it.
const ClassProcesses = "processes"

// ClassNetwork is the effect class of a function that makes an HTTP
// client, the handle a program reaches the network through as a client
// (`http/host.new_client`, `default_client`): a browser has it through
// fetch, and wasip1 does not.
const ClassNetwork = "network"

// ClassConnect is the effect class of a function that opens a
// connection or asks a name resolver itself, below HTTP: std/net's dial
// and lookups, std/tls's dial, std/udp's dial. A Unix socket's dial is
// one too: it is the same call to the system, and no target that lacks
// network sockets has Unix ones. A js/wasm or wasip1 module lacks it,
// since a browser offers a program only fetch.
const ClassConnect = "connect"

// ClassListen is the effect class of a function that accepts network
// connections or receives datagrams sent to an address it binds, as a
// server does: std/net's listen, std/udp's bind, on IP or a Unix
// socket's path. A js/wasm or wasip1 module lacks it.
const ClassListen = "listen"

// Classes is the effect class of every host function that has one, by
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
func Pure() map[string]bool { return marked(func(b Binding) bool { return b.Pure }) }

// Unbounded is the key of every host function whose row is marked
// Unbounded.
func Unbounded() map[string]bool { return marked(func(b Binding) bool { return b.Unbounded }) }

// Seeded is the key of every host function whose row is marked Seeded.
func Seeded() map[string]bool { return marked(func(b Binding) bool { return b.Seeded }) }

// Held is the key of every host function whose row is marked Held.
func Held() map[string]bool { return marked(func(b Binding) bool { return b.Held }) }

// marked is the key of every host function whose row has the mark has.
func marked(has func(Binding) bool) map[string]bool {
	out := map[string]bool{}
	for _, rows := range [][]Binding{Funcs(), RtFuncs()} {
		for _, b := range rows {
			if has(b) {
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
// Every symbol below is the host-shaped half of its package. The rt-shaped half
// beside it — `stdregex.Compile` against `stdregex.HostCompile` — is not
// interchangeable with it in either direction: measured, 13 of 14 rt-shaped symbols
// either error at this boundary or hand Nomi a different value (a struct where
// the declaration says a tuple, `{tag:, ok:, err:}` where it says a Result). The
// VM's generated adapters call the host-shaped half.
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
		{Name: "calendar.civil_format_fields", Fn: stdformat.HostFormatCivilFields, Pure: true},
		{Name: "calendar.civil_format_pattern", Fn: stdformat.HostFormatCivilPattern, Pure: true},
		{Name: "calendar.civil_format_style", Fn: stdformat.HostFormatCivilStyle, Pure: true},
		{Name: "calendar.date_add_days", Fn: stdcalendar.BoundDateAddDays, Pure: true},
		{Name: "calendar.date_add_months", Fn: stdcalendar.BoundDateAddMonths, Pure: true},
		{Name: "calendar.date_add_years", Fn: stdcalendar.BoundDateAddYears, Pure: true},
		{Name: "calendar.date_new_raw", Fn: stdcalendar.BoundDateNew, Pure: true},
		{Name: "calendar.date_parse_pattern", Fn: stdformat.HostParseDatePattern, Pure: true},
		{Name: "calendar.date_parse_raw", Fn: stdcalendar.BoundDateParse, Pure: true},
		{Name: "calendar.date_parse_style", Fn: stdformat.HostParseDateStyle, Pure: true},
		{Name: "calendar.fields_check", Fn: stdformat.HostCheckFields, Pure: true},
		{Name: "calendar.local_zone_raw", Fn: stdcalendar.LocalZone, World: Outside},
		{Name: "calendar.naive_add_days", Fn: stdcalendar.BoundNaiveAddDays, Pure: true},
		{Name: "calendar.naive_add_months", Fn: stdcalendar.BoundNaiveAddMonths, Pure: true},
		{Name: "calendar.naive_add_nanos", Fn: stdcalendar.BoundNaiveAddNanos, Pure: true},
		{Name: "calendar.naive_add_years", Fn: stdcalendar.BoundNaiveAddYears, Pure: true},
		{Name: "calendar.naive_between_nanos", Fn: stdcalendar.BoundNaiveBetweenNanos, Pure: true},
		{Name: "calendar.naive_new_exact_raw", Fn: stdcalendar.BoundNaiveNewExact, Pure: true},
		{Name: "calendar.naive_parse_pattern", Fn: stdformat.HostParseNaivePattern, Pure: true},
		{Name: "calendar.naive_parse_raw", Fn: stdcalendar.BoundNaiveParse, Pure: true},
		{Name: "calendar.offset_add_months", Fn: stdcalendar.BoundOffsetAddMonths, Pure: true},
		{Name: "calendar.offset_add_years", Fn: stdcalendar.BoundOffsetAddYears, Pure: true},
		{Name: "calendar.offset_add_nanos", Fn: stdcalendar.BoundOffsetAddNanos, Pure: true},
		{Name: "calendar.offset_from_instant_nanos", Fn: stdcalendar.BoundOffsetFromInstant, Pure: true},
		{Name: "calendar.offset_parse_raw", Fn: stdcalendar.BoundOffsetParse, Pure: true},
		{Name: "calendar.offset_with_offset_raw", Fn: stdcalendar.BoundOffsetWithOffset, Pure: true},
		{Name: "calendar.pattern_check", Fn: stdformat.HostCheckPattern, Pure: true},
		{Name: "calendar.relative_format_raw", Fn: stdformat.HostFormatRelative, Pure: true},
		{Name: "calendar.time_add_nanos", Fn: stdcalendar.BoundTimeAddNanos, Pure: true},
		{Name: "calendar.time_parse_pattern", Fn: stdformat.HostParseTimePattern, Pure: true},
		{Name: "calendar.time_parse_raw", Fn: stdcalendar.BoundTimeParse, Pure: true},
		{Name: "calendar.time_parse_style", Fn: stdformat.HostParseTimeStyle, Pure: true},
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
		{Name: "collation.collator_compare_raw", Fn: stdformat.HostCollatorCompare, Pure: true},
		{Name: "collation.collator_inspect_raw", Fn: stdformat.HostCollatorInspect, Pure: true},
		{Name: "collation.collator_new_raw", Fn: stdformat.HostCollatorNew, Pure: true},
		{Name: "collation.collator_sort_key_raw", Fn: stdformat.HostCollatorSortKey, Pure: true},
		{Name: "currency.currency_check", Fn: stdformat.HostCurrencyCheck, Pure: true},
		{Name: "currency.currency_code_of", Fn: stdformat.HostCurrencyCodeOf, Pure: true},
		{Name: "currency.currency_digits_of", Fn: stdformat.HostCurrencyDigitsOf, Pure: true},
		{Name: "decimal.decimal_format", Fn: stdformat.HostFormatDecimal, PanicsPropagate: true, Pure: true},
		{Name: "duration.duration_format_raw", Fn: stdformat.HostFormatDuration, Pure: true},
		{Name: "decimal.decimal_format_percent", Fn: stdformat.HostFormatDecimalPercent, PanicsPropagate: true, Pure: true},
		{Name: "decimal.decimal_parse_in", Fn: stdformat.HostParseDecimal, Pure: true},
		{Name: "decimal.decimal_parse_percent_in", Fn: stdformat.HostParseDecimalPercent, Pure: true},
		{Name: "float.float_format", Fn: stdformat.HostFormatFloat, PanicsPropagate: true, Pure: true},
		{Name: "float.float_format_percent", Fn: stdformat.HostFormatFloatPercent, PanicsPropagate: true, Pure: true},
		{Name: "float.float_parse_in", Fn: stdformat.HostParseFloat, Pure: true},
		{Name: "float.float_parse_percent_in", Fn: stdformat.HostParseFloatPercent, Pure: true},
		{Name: "int.int_format", Fn: stdformat.HostFormatInt, PanicsPropagate: true, Pure: true},
		{Name: "int.int_parse_in", Fn: stdformat.HostParseInt, Pure: true},
		{Name: "locale.locale_available", Fn: stdlocale.HostAvailable, Pure: true},
		{Name: "locale.locale_load_raw", Fn: stdlocale.HostLoad, World: Outside},
		{Name: "locale.locale_loaded_raw", Fn: stdlocale.HostLoaded, World: Inside},
		{Name: "phrase/format.format_raw", Fn: stdphraseformat.HostFormat, PanicsPropagate: true, Pure: true},
		{Name: "phrase/format.load_raw", Fn: stdphraseformat.HostLoad, World: Inside},
		{Name: "phrase/format.noun_raw", Fn: stdphraseformat.HostNoun, PanicsPropagate: true, World: Inside},
		{Name: "money/format.money_format_raw", Fn: stdformat.HostFormatMoney, Pure: true},
		{Name: "money/format.money_parse_raw", Fn: stdformat.HostParseMoney, Pure: true},
		{Name: "locale.locale_fallbacks", Fn: stdlocale.HostFallbacks, Pure: true},
		{Name: "locale.locale_from_posix_raw", Fn: stdlocale.HostFromPosix, Pure: true},
		{Name: "locale.locale_language", Fn: stdlocale.HostLanguage, Pure: true},
		{Name: "locale.locale_negotiate", Fn: stdlocale.HostNegotiate, Pure: true},
		{Name: "locale.locale_parse_raw", Fn: stdlocale.HostParse, Pure: true},
		{Name: "locale.locale_region", Fn: stdlocale.HostRegion, Pure: true},
		{Name: "locale.locale_script", Fn: stdlocale.HostScript, Pure: true},
		{Name: "plural.plural_cardinal_tag", Fn: stdplural.HostCardinalTag, Pure: true},
		{Name: "plural.plural_cardinal_tags", Fn: stdplural.HostCardinalTags, Pure: true},
		{Name: "plural.plural_ordinal_tag", Fn: stdplural.HostOrdinalTag, Pure: true},
		{Name: "plural.plural_ordinal_tags", Fn: stdplural.HostOrdinalTags, Pure: true},
		{Name: "random.between_state", Fn: stdrandom.HostBetween, Pure: true},
		{Name: "random.os_state", Fn: stdrandom.HostFromOS, World: Outside},
		{Name: "random.secure_bytes", Fn: stdrandom.HostSecureBytes, PanicsPropagate: true, World: Outside},
		{Name: "random.unit_float_state", Fn: stdrandom.HostUnitFloat, Pure: true},
		{Name: "Regex.compile", Fn: stdregex.HostCompile, Pure: true},
		{Name: "Regex.is_contained_in", Fn: stdregex.HostMatch, Pure: true},
		{Name: "Regex.find_in", Fn: stdregex.HostFind, Pure: true},
		{Name: "Regex.find_all_in", Fn: stdregex.HostFindAll, Pure: true},
		{Name: "Regex.pattern", Fn: stdregex.HostPattern, Pure: true},
		{Name: "Regex.is_prefix_of", Fn: stdregex.HostPrefixOf, Pure: true},
		{Name: "Regex.replace_in", Fn: stdregex.HostReplace, Pure: true},
		{Name: "Regex.split_in", Fn: stdregex.HostSplit, Pure: true},
		{Name: "Regex.is_suffix_of", Fn: stdregex.HostSuffixOf, Pure: true},
		{Name: "toml/host.check", Fn: stdtoml.HostCheck, Pure: true},
		{Name: "toml/host.build", Fn: stdtoml.HostBuild, Pure: true},
		{Name: "toml/host.quote", Fn: stdtoml.HostQuote, Pure: true},
		{Name: "toml/host.float", Fn: stdtoml.HostFloat, Pure: true},
		{Name: "toml/host.decode", Fn: stdtoml.HostDecode, Pure: true},
		// A key derivation's cost is its parameters', and bcrypt's
		// verification's is the cost its stored hash records. bcrypt draws its
		// own salt, a result whose meaning does not depend on it: Inside.
		{Name: "passwords/host.argon2id", Fn: stdpasswords.Argon2id, Pure: true, Unbounded: true},
		{Name: "passwords/host.scrypt", Fn: stdpasswords.Scrypt, Pure: true, Unbounded: true},
		{Name: "passwords/host.bcrypt_hash", Fn: stdpasswords.BcryptHash, World: Inside},
		{Name: "passwords/host.bcrypt_verify", Fn: stdpasswords.BcryptVerify, Pure: true, Unbounded: true},
		{Name: "passwords/host.bcrypt_cost", Fn: stdpasswords.BcryptCost, Pure: true},
		// std/cipher draws a seal's random nonce with random.secure_bytes,
		// so the AEAD rows compute from their operands alone. A new
		// key-exchange key reads the system's entropy: Outside.
		{Name: "cipher/host.seal", Fn: stdcipher.Seal, Pure: true},
		{Name: "cipher/host.open", Fn: stdcipher.Open, Pure: true},
		{Name: "cipher/host.aes_ctr", Fn: stdcipher.AESCTR, Pure: true},
		{Name: "key_exchange/host.generate", Fn: stdcipher.Generate, World: Outside},
		{Name: "key_exchange/host.private_is_valid", Fn: stdcipher.PrivateIsValid, Pure: true},
		{Name: "key_exchange/host.public_is_valid", Fn: stdcipher.PublicIsValid, Pure: true},
		{Name: "key_exchange/host.public_of", Fn: stdcipher.PublicOf, Pure: true},
		{Name: "key_exchange/host.shared_secret", Fn: stdcipher.SharedSecret, Pure: true},
		// std/system reads facts of the machine and the process when it is
		// called, which the compiler's machine would answer differently:
		// Outside, with no class, since every target answers each, if only
		// with Unsupported (internal/stdsystem's package doc).
		{Name: "system/host.os", Fn: stdsystem.OS, World: Outside},
		{Name: "system/host.arch", Fn: stdsystem.Arch, World: Outside},
		{Name: "system/host.cpu_count", Fn: stdsystem.CPUCount, World: Outside},
		{Name: "system/host.hostname", Fn: stdsystem.Hostname, World: Outside},
		{Name: "system/host.executable", Fn: stdsystem.Executable, World: Outside},
		{Name: "system/host.home_dir", Fn: stdsystem.HomeDir, World: Outside},
		{Name: "system/host.cache_dir", Fn: stdsystem.CacheDir, World: Outside},
		{Name: "system/host.config_dir", Fn: stdsystem.ConfigDir, World: Outside},
		{Name: "system/host.data_dir", Fn: stdsystem.DataDir, World: Outside},
		{Name: "system/host.user", Fn: stdsystem.User, World: Outside},
		{Name: "system/host.memory", Fn: stdsystem.Memory, World: Outside},
		{Name: "system/host.runtime", Fn: stdsystem.Runtime, World: Outside},
		{Name: "system/host.nomi_release", Fn: stdsystem.NomiRelease, World: Outside},
		// A whole-value decode costs what its limit lets the data expand to, a
		// value: Unbounded. An encode costs what its data's size does. The
		// streams act through the Reader or Writer they wrap: Held.
		{Name: "compress/host.encode", Fn: stdcompress.Encode, Pure: true},
		{Name: "compress/host.decode", Fn: stdcompress.Decode, Pure: true, Unbounded: true},
		{Name: "compress/host.gzip_header", Fn: stdcompress.GzipHeader, Pure: true},
		{Name: "compress/host.decode_at", Fn: stdcompress.DecodeAt, Pure: true, Unbounded: true},
		{Name: "compress/host.reader", Fn: stdcompress.Reader, World: Inside, Held: true},
		{Name: "compress/host.writer", Fn: stdcompress.Writer, World: Inside, Held: true},
		{Name: "compress/host.encoding_reader", Fn: stdcompress.EncodingReader, World: Inside, Held: true},
		{Name: "strings.capture_groups", Fn: stdstrings.HostCaptures, Pure: true},
		{Name: "strings.capture_groups_all", Fn: stdstrings.HostCapturesAll, Pure: true},
		{Name: "String.capitalize", Fn: stdstrings.HostCapitalize, Pure: true},
		{Name: "String.capitalize_in", Fn: stdstrings.HostCapitalizeIn, Pure: true},
		{Name: "String.fold_case", Fn: stdstrings.HostFoldCase, Pure: true},
		{Name: "String.snake_case", Fn: stdstrings.SnakeCase, Pure: true},
		{Name: "String.kebab_case", Fn: stdstrings.KebabCase, Pure: true},
		{Name: "String.pascal_case", Fn: stdstrings.PascalCase, Pure: true},
		{Name: "String.camel_case", Fn: stdstrings.CamelCase, Pure: true},
		{Name: "String.lowercase_in", Fn: stdstrings.HostLowercaseIn, Pure: true},
		{Name: "String.uppercase_in", Fn: stdstrings.HostUppercaseIn, Pure: true},
		{Name: "strings.join_list_raw", Fn: stdformat.HostJoinList, Pure: true},
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
		{Name: "strings.String.is_upper", Fn: rt.StringIsUpper, Pure: true},
		{Name: "strings.String.is_lower", Fn: rt.StringIsLower, Pure: true},
		{Name: "strings.String.replace_in", Fn: rt.StringReplaceIn, Pure: true},
		{Name: "strings.String.trim", Fn: rt.StringTrim, Pure: true},
		{Name: "strings.String.hash", Fn: rt.StringHash, Pure: true, Seeded: true},
		{Name: "strings.string_compare", Fn: rt.StringCompare, Pure: true},
		{Name: "strings.String.to_int", Fn: rt.StringToInt, Pure: true},
		{Name: "strings.String.split_in", Fn: rt.StringSplitIn, Pure: true},
		{Name: "strings.String.words", Fn: rt.StringWords, Pure: true},
		{Name: "strings.String.word_segments", Fn: rt.StringWordSegments, Pure: true},
		{Name: "strings.String.sentences", Fn: rt.StringSentences, Pure: true},
		{Name: "strings.String.display_width", Fn: rt.StringDisplayWidth, Pure: true},
		// A wrap costs what its text's size does, whatever the width.
		{Name: "strings.String.wrap", Fn: rt.StringWrap, Pure: true},
		{Name: "strings.String.lines", Fn: rt.StringLines, Pure: true},
		{Name: "strings.String.length", Fn: rt.StringLength, Pure: true},
		{Name: "strings.String.slice", Fn: rt.StringSlice, Pure: true},
		{Name: "strings.String.reverse", Fn: rt.StringReverse, Pure: true},
		{Name: "int.Int.to_byte", Fn: rt.IntToByte, Pure: true},
		{Name: "bytes.Byte.to_int", Fn: rt.ByteToInt, Pure: true},
		{Name: "bytes.Byte.wrapping_shift_left", Fn: rt.ByteShiftLeft, Pure: true},
		{Name: "bytes.Byte.wrapping_shift_right", Fn: rt.ByteShiftRight, Pure: true},
		{Name: "bytes.Bytes.length", Fn: rt.BytesLength, Pure: true},
		{Name: "hash.digest_by", Fn: stdhash.Digest, Pure: true},
		{Name: "hash.hmac_by", Fn: stdhash.HMAC, Pure: true},
		{Name: "hash.hasher_new", Fn: stdhash.HasherNew, Pure: true},
		{Name: "hash.hasher_new_hmac", Fn: stdhash.HasherNewHMAC, Pure: true},
		{Name: "hash.Hasher.update", Fn: stdhash.HasherUpdate, Pure: true},
		{Name: "hash.Hasher.finish", Fn: stdhash.HasherFinish, Pure: true},
		{Name: "hash.shake128", Fn: stdhash.Shake128, Pure: true, Unbounded: true},
		{Name: "hash.shake256", Fn: stdhash.Shake256, Pure: true, Unbounded: true},
		{Name: "hash.equal_in_constant_time", Fn: rt.EqualInConstantTime, Pure: true},
		{Name: "hash.pbkdf2_sha256", Fn: rt.PBKDF2SHA256, Pure: true, Unbounded: true},
		{Name: "hash.hkdf_by", Fn: stdhash.HKDF, Pure: true},
		{Name: "hash.hkdf_extract_by", Fn: stdhash.HKDFExtract, Pure: true},
		{Name: "hash.hkdf_expand_by", Fn: stdhash.HKDFExpand, Pure: true},
		{Name: "signatures/host.generate_ed25519", Fn: stdhttp.SigGenerateEd25519, World: Outside},
		{Name: "signatures/host.generate_ecdsa", Fn: stdhttp.SigGenerateECDSA, World: Outside},
		{Name: "signatures/host.generate_rsa", Fn: stdhttp.SigGenerateRSA, World: Outside},
		{Name: "signatures/host.private_from_der", Fn: stdhttp.SigPrivateFromDER, Pure: true},
		{Name: "signatures/host.private_from_pem", Fn: stdhttp.SigPrivateFromPEM, Pure: true},
		{Name: "signatures/host.private_to_pem", Fn: stdhttp.SigPrivateToPEM, Pure: true},
		{Name: "signatures/host.private_kind", Fn: stdhttp.SigPrivateKind, Pure: true},
		{Name: "signatures/host.public_of", Fn: stdhttp.SigPublicOf, Pure: true},
		{Name: "signatures/host.public_from_der", Fn: stdhttp.SigPublicFromDER, Pure: true},
		{Name: "signatures/host.public_from_pem", Fn: stdhttp.SigPublicFromPEM, Pure: true},
		{Name: "signatures/host.public_to_pem", Fn: stdhttp.SigPublicToPEM, Pure: true},
		{Name: "signatures/host.public_kind", Fn: stdhttp.SigPublicKind, Pure: true},
		{Name: "signatures/host.ed25519_from_seed", Fn: stdhttp.SigEd25519FromSeed, Pure: true},
		{Name: "signatures/host.ed25519_seed", Fn: stdhttp.SigEd25519Seed, Pure: true},
		{Name: "signatures/host.ed25519_public_from_bytes", Fn: stdhttp.SigEd25519PublicFromBytes, Pure: true},
		{Name: "signatures/host.ed25519_public_bytes", Fn: stdhttp.SigEd25519PublicBytes, Pure: true},
		{Name: "signatures/host.sign", Fn: stdhttp.SigSign, World: Inside},
		{Name: "signatures/host.is_valid", Fn: stdhttp.SigIsValid, Pure: true},
		{Name: "signatures/host.scheme_name", Fn: stdhttp.SigSchemeName, Pure: true},
		// OAEP draws a random seed for a result whose meaning does not
		// depend on it, as signing draws a nonce: Inside.
		{Name: "signatures/host.encrypt", Fn: stdhttp.SigEncrypt, World: Inside},
		{Name: "signatures/host.decrypt", Fn: stdhttp.SigDecrypt, Pure: true},
		{Name: "signatures/host.scheme_kind", Fn: stdhttp.SigSchemeKind, Pure: true},
		{Name: "base64.standard", Fn: rt.Base64Encode, Pure: true},
		{Name: "base64.decode", Fn: rt.Base64Decode, Pure: true},
		{Name: "base64.url_safe", Fn: rt.Base64URLEncode, Pure: true},
		{Name: "base64.decode_url", Fn: rt.Base64URLDecode, Pure: true},
		{Name: "base64.decode_bytes", Fn: rt.Base64DecodeBytes, Pure: true},
		{Name: "base64.decode_url_bytes", Fn: rt.Base64URLDecodeBytes, Pure: true},
		{Name: "hex.encode", Fn: rt.HexEncode, Pure: true},
		{Name: "hex.decode", Fn: rt.HexDecode, Pure: true},
		{Name: "hex.decode_bytes", Fn: rt.HexDecodeBytes, Pure: true},
		{Name: "html/host.escape", Fn: rt.HTMLEscape, Pure: true},
		{Name: "html/host.filter_url", Fn: rt.HTMLFilterURL, Pure: true},
		{Name: "html/host.escape_url_path", Fn: rt.HTMLEscapeURLPath, Pure: true},
		{Name: "html/host.unescape_references", Fn: rt.HTMLUnescape, Pure: true},
		{Name: "html/host.named_reference", Fn: rt.HTMLEntity, Pure: true},
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
		{Name: "bytes.Bytes.hash", Fn: rt.BytesHash, Pure: true, Seeded: true},
		{Name: "bytes.Bytes.contains", Fn: rt.BytesContains, Pure: true},
		{Name: "bytes.bytes_compare", Fn: rt.BytesCompare, Pure: true},
		{Name: "strings.String.to_bytes", Fn: rt.StringToBytes, Pure: true},
		{Name: "int.Int.to_string", Fn: rt.IntToString, Pure: true},
		{Name: "int.Int.to_float", Fn: rt.IntToFloat, Pure: true},
		{Name: "averageable.int_ratio_to_float", Fn: rt.IntRatioToFloat, Pure: true},
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
		{Name: "decimal.Decimal.hash", Fn: rt.DecimalHash, Pure: true, Seeded: true},
		{Name: "decimal.Decimal.compare", Fn: rt.DecimalCompare, Pure: true},
		{Name: "duration.render_nanos", Fn: rt.DurationNanosToString, Pure: true},
		{Name: "instant.Instant.now", Fn: rt.InstantNow, World: Outside},
		{Name: "instant.Monotonic.now", Fn: rt.MonotonicNow, World: Outside},
		{Name: "int.Int.to_codepoint", Fn: rt.IntToCodepoint, Pure: true},
		{Name: "code_points.Codepoint.to_string", Fn: rt.CodepointToString, Pure: true},
		{Name: "code_points.Codepoint.is_letter", Fn: rt.CodepointIsLetter, Pure: true},
		{Name: "code_points.Codepoint.is_whitespace", Fn: rt.CodepointIsWhitespace, Pure: true},
		{Name: "code_points.Codepoint.is_upper", Fn: rt.CodepointIsUpper, Pure: true},
		{Name: "code_points.Codepoint.is_lower", Fn: rt.CodepointIsLower, Pure: true},
		{Name: "code_points.Codepoint.is_title", Fn: rt.CodepointIsTitle, Pure: true},
		{Name: "code_points.Codepoint.is_mark", Fn: rt.CodepointIsMark, Pure: true},
		{Name: "code_points.Codepoint.is_number", Fn: rt.CodepointIsNumber, Pure: true},
		{Name: "code_points.Codepoint.is_punctuation", Fn: rt.CodepointIsPunctuation, Pure: true},
		{Name: "code_points.Codepoint.is_symbol", Fn: rt.CodepointIsSymbol, Pure: true},
		{Name: "code_points.Codepoint.is_separator", Fn: rt.CodepointIsSeparator, Pure: true},
		{Name: "code_points.Codepoint.is_control", Fn: rt.CodepointIsControl, Pure: true},
		{Name: "strings.String.to_codepoints", Fn: rt.StringToCodepoints, Pure: true},
		{Name: "context.Context.with_timeout", Fn: rt.ContextWithTimeout, World: Outside},
		{Name: "context.Context.root", Fn: rt.ContextRoot, Pure: true},
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
		{Name: "decimal.Decimal.divide", Fn: rt.DecimalDivide, Pure: true, Unbounded: true},
		{Name: "decimal.Decimal.divide_exact", Fn: rt.DecimalDivideExact, Pure: true},
		{Name: "decimal.Decimal.round", Fn: rt.DecimalRound, Pure: true, Unbounded: true},
		{Name: "decimal.Decimal.round_exact", Fn: rt.DecimalRoundExact, Pure: true, Unbounded: true},
		{Name: "decimal.Decimal.pow", Fn: rt.DecimalPow, Pure: true, Unbounded: true},
		{Name: "decimal.Decimal.sqrt", Fn: rt.DecimalSqrt, Pure: true, Unbounded: true},
		{Name: "decimal.Decimal.sqrt_exact", Fn: rt.DecimalSqrtExact, Pure: true, Unbounded: true},
		{Name: "decimal.Decimal.normalize", Fn: rt.DecimalNormalize, Pure: true},
		{Name: "decimal.Decimal.scale", Fn: rt.DecimalScale, Pure: true},
		{Name: "strings.String.repeat", Fn: rt.StringRepeat, Pure: true, Unbounded: true},
		{Name: "context.Context.deadline", Fn: rt.ContextDeadline, World: Outside},
		{Name: "context.Context.deadline_remaining", Fn: rt.ContextDeadlineRemaining, World: Outside},
		{Name: "context.Context.with_deadline", Fn: rt.ContextWithDeadline, Pure: true},
		{Name: "context.Context.inspect", Fn: rt.ContextInspect, Pure: true},
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
		{Name: "dynamic.Dynamic.hash", Fn: rt.DynamicHashInt, Pure: true, Seeded: true},
		{Name: "dynamic.Dynamic.has", Fn: rt.DynamicHas, Pure: true},
		{Name: "dynamic.Dynamic.inspect", Fn: rt.DynamicInspect, Pure: true},
		{Name: "json.Json.to_dynamic", Fn: rt.JsonToDynamic, Pure: true},
		{Name: "io.read_line", Fn: rt.ReadLine, World: Outside},
		{Name: "io.standard_input", Fn: rt.IOStdin, World: Outside},
		{Name: "io.standard_output", Fn: rt.IOStdout, World: Outside},
		{Name: "io.standard_error", Fn: rt.IOStderr, World: Outside},
		// Whether a standard stream is a terminal: rt knows the stream's
		// file, and internal/stdterminal asks golang.org/x/term, which rt
		// may not import.
		{Name: "io.stream_is_terminal", Fn: stdterminal.IsTerminal, World: Outside},
		{Name: "io.Reader.read_line", Fn: rt.ReaderReadLine, World: Inside, Held: true},
		{Name: "io.read_up_to", Fn: rt.ReaderRead, World: Inside, Held: true},
		{Name: "io.Reader.read_all", Fn: rt.ReaderReadAll, World: Inside, Held: true},
		{Name: "io.Reader.read_all_text", Fn: rt.ReaderReadAllText, World: Inside, Held: true},
		{Name: "io.Reader.copy_to", Fn: rt.ReaderCopyTo, World: Inside, Held: true},
		{Name: "io.Reader.close", Fn: rt.ReaderClose, World: Inside, Held: true},
		{Name: "io.Reader.from_bytes", Fn: rt.ReaderFromBytes, World: Inside},
		{Name: "io.Reader.from_text", Fn: rt.ReaderFromText, World: Inside},
		{Name: "io.Reader.from_fn", Fn: rt.ReaderFromFn, World: Inside},
		{Name: "io.Writer.write", Fn: rt.WriterWrite, World: Inside, Held: true},
		{Name: "io.Writer.write_text", Fn: rt.WriterWriteText, World: Inside, Held: true},
		{Name: "io.Writer.flush", Fn: rt.WriterFlush, World: Inside, Held: true},
		{Name: "io.Writer.close", Fn: rt.WriterClose, World: Inside, Held: true},
		{Name: "io.Writer.discard", Fn: rt.WriterDiscard, World: Inside, Held: true},
		{Name: "io.writer_from_fn", Fn: rt.WriterFromFn, World: Inside},
		{Name: "io.memory_writer", Fn: rt.WriterToMemory, World: Inside},
		{Name: "io.collected", Fn: rt.WriterCollected, World: Inside, Held: true},
		// An embedded file is read from the program itself, which every
		// target has: no class. It is read through the handle the program's
		// Files value holds, made by an embed declaration, which is no call.
		{Name: "embed/host.read", Fn: rt.EmbeddedRead, World: Inside, Held: true},
		{Name: "embed/host.sum", Fn: rt.EmbeddedSum, World: Inside, Held: true},
		// Every std/fs crossing acts through the file system it is handed,
		// held, but `here`, which hands a call the frame's: a path-taking
		// std/fs function reaches the files by it, and `fs.open_dir` narrows
		// what it answers to a Dir.
		{Name: "fs/host.here", Fn: stdio.Here, Class: ClassFiles, World: Outside},
		{Name: "fs/host.read", Fn: stdio.Read, World: Inside, Held: true},
		{Name: "fs/host.read_text", Fn: stdio.ReadText, World: Inside, Held: true},
		{Name: "fs/host.write", Fn: stdio.Write, World: Inside, Held: true},
		{Name: "fs/host.open", Fn: stdio.Open, World: Inside, Held: true},
		{Name: "fs/host.create", Fn: stdio.Create, World: Inside, Held: true},
		{Name: "fs/host.list", Fn: stdio.List, World: Inside, Held: true},
		{Name: "fs/host.create_dir", Fn: stdio.CreateDir, World: Inside, Held: true},
		{Name: "fs/host.remove", Fn: stdio.Remove, World: Inside, Held: true},
		{Name: "fs/host.remove_all", Fn: stdio.RemoveAll, World: Inside, Held: true},
		{Name: "fs/host.rename", Fn: stdio.Rename, World: Inside, Held: true},
		{Name: "fs/host.copy", Fn: stdio.Copy, World: Inside, Held: true},
		{Name: "fs/host.metadata", Fn: stdio.Metadata, World: Inside, Held: true},
		{Name: "fs/host.link_metadata", Fn: stdio.LinkMetadata, World: Inside, Held: true},
		{Name: "fs/host.create_symlink", Fn: stdio.CreateSymlink, World: Inside, Held: true},
		{Name: "fs/host.read_link", Fn: stdio.ReadLink, World: Inside, Held: true},
		{Name: "fs/host.set_mode", Fn: stdio.SetMode, World: Inside, Held: true},
		{Name: "fs/host.set_times", Fn: stdio.SetTimes, World: Inside, Held: true},
		{Name: "fs/host.touch", Fn: stdio.Touch, World: Inside, Held: true},
		{Name: "fs/host.create_link", Fn: stdio.CreateLink, World: Inside, Held: true},
		{Name: "fs/host.set_owner", Fn: stdio.SetOwner, World: Inside, Held: true},
		{Name: "fs/host.disk_usage", Fn: stdio.DiskUsageOf, World: Inside, Held: true},
		{Name: "fs/host.owner_name", Fn: stdio.OwnerName, World: Inside, Held: true},
		{Name: "fs/host.open_file", Fn: stdio.OpenFile, World: Inside, Held: true},
		{Name: "fs/host.within", Fn: stdio.Within, World: Inside, Held: true},
		{Name: "fs/host.release", Fn: stdio.Release, World: Inside, Held: true},
		{Name: "io.file_read", Fn: rt.FileRead, World: Inside, Held: true},
		{Name: "io.File.read_at", Fn: rt.FileReadAt, World: Inside, Held: true},
		{Name: "io.File.write", Fn: rt.FileWrite, World: Inside, Held: true},
		{Name: "io.File.write_at", Fn: rt.FileWriteAt, World: Inside, Held: true},
		{Name: "io.File.seek", Fn: rt.FileSeek, World: Inside, Held: true},
		{Name: "io.File.position", Fn: rt.FilePosition, World: Inside, Held: true},
		{Name: "io.File.size", Fn: rt.FileSize, World: Inside, Held: true},
		{Name: "io.File.truncate", Fn: rt.FileTruncate, World: Inside, Held: true},
		{Name: "io.File.close", Fn: rt.FileClose, World: Inside, Held: true},
		// Syncing and locking act on the held File alone; a lock's wait is
		// the time an operation on the handle takes.
		{Name: "io.File.sync", Fn: rt.FileSync, World: Inside, Held: true},
		{Name: "io.File.sync_data", Fn: rt.FileSyncData, World: Inside, Held: true},
		{Name: "fs/host.lock", Fn: rt.FileLock, World: Inside, Held: true},
		{Name: "fs/host.sync_dir", Fn: stdio.SyncDir, World: Inside, Held: true},
		{Name: "fs/host.temp_dir", Fn: stdio.TempDir, World: Inside, Held: true},
		{Name: "fs/host.make_temp_dir", Fn: stdio.MakeTempDir, World: Inside, Held: true},
		{Name: "fs/host.make_temp", Fn: stdio.MakeTemp, World: Inside, Held: true},
		{Name: "fs/host.current_dir", Fn: stdio.CurrentDir, World: Inside, Held: true},
		{Name: "fs/host.new_sandbox", Fn: stdio.NewSandbox, World: Inside},
		{Name: "fs/host.sandbox_fail", Fn: stdio.SandboxFail, World: Inside, Held: true},
		{Name: "fs/host.sandbox_files", Fn: stdio.SandboxFiles, World: Inside, Held: true},
		{Name: "fs/host.sandbox_log", Fn: stdio.SandboxLog, World: Inside, Held: true},
		{Name: "fs/host.sandbox_open", Fn: stdio.SandboxOpen, World: Inside, Held: true},
		{Name: "http/host.new_client", Fn: stdhttp.NewClient, Class: ClassNetwork, World: Outside},
		{Name: "http/host.default_client", Fn: stdhttp.DefaultClient, Class: ClassNetwork, World: Outside},
		{Name: "http/host.close_idle", Fn: stdhttp.CloseIdle, World: Inside, Held: true},
		{Name: "http/host.send", Fn: stdhttp.Send, World: Inside, Held: true},
		{Name: "http/host.status", Fn: stdhttp.Status, World: Inside, Held: true},
		{Name: "http/host.protocol", Fn: stdhttp.Protocol, World: Inside, Held: true},
		{Name: "http/host.headers", Fn: stdhttp.Headers, World: Inside, Held: true},
		{Name: "http/host.trailers", Fn: stdhttp.Trailers, World: Inside, Held: true},
		{Name: "http/host.body_bytes", Fn: stdhttp.BodyBytes, World: Inside, Held: true},
		{Name: "http/host.body_reader", Fn: stdhttp.BodyReader, World: Inside, Held: true},
		{Name: "http/host.close_body", Fn: stdhttp.CloseBody, World: Inside, Held: true},
		{Name: "http/host.content_length", Fn: stdhttp.ContentLength, World: Inside, Held: true},
		{Name: "http/host.transfer_encoding", Fn: stdhttp.TransferEncoding, World: Inside, Held: true},
		{Name: "http/host.closes", Fn: stdhttp.Closes, World: Inside, Held: true},
		{Name: "http/host.uncompressed", Fn: stdhttp.Uncompressed, World: Inside, Held: true},
		{Name: "http/host.final_url", Fn: stdhttp.FinalURL, World: Inside, Held: true},
		{Name: "http/host.canonical_key", Fn: stdhttp.CanonicalKey, Pure: true},
		{Name: "http/host.status_text", Fn: stdhttp.StatusText, Pure: true},
		{Name: "http/host.headers_wire", Fn: stdhttp.HeadersWire, Pure: true},
		{Name: "http/host.parse_http_version", Fn: stdhttp.ParseHTTPVersion, Pure: true},
		{Name: "http/host.basic_auth", Fn: stdhttp.BasicAuth, Pure: true},
		{Name: "http/host.add_cookie", Fn: stdhttp.AddCookie, Pure: true},
		{Name: "http/host.cross_origin_check", Fn: stdhttp.CrossOriginCheck, Pure: true},
		{Name: "http/host.listen", Fn: stdhttp.Listen, Class: ClassListen, World: Outside},
		{Name: "http/host.listen_tls", Fn: stdhttp.ListenTLS, Class: ClassListen, World: Outside},
		{Name: "http/host.with_tls", Fn: stdhttp.WithTLS, World: Inside, Held: true},
		{Name: "http/host.test_certificate", Fn: stdhttp.TestCertificate, World: Outside},
		{Name: "http/host.certificate_from_pem", Fn: stdhttp.CertificateFromPEM, Pure: true},
		{Name: "http/host.certificate_load", Fn: stdhttp.CertificateLoad, Class: ClassFiles, World: Outside},
		{Name: "http/host.certificate_info", Fn: stdhttp.CertificateInfo, Pure: true},
		{Name: "http/host.tls_config", Fn: stdhttp.TLSConfig, PanicsPropagate: true, World: Outside},
		{Name: "http/host.tls_with_certificate", Fn: stdhttp.TLSWithCertificate, World: Inside, Held: true},
		{Name: "http/host.cipher_suites", Fn: stdhttp.CipherSuites, Pure: true},
		{Name: "http/host.cipher_suite_name", Fn: stdhttp.CipherSuiteName, Pure: true},
		{Name: "http/host.dial_with", Fn: stdhttp.DialWith, Class: ClassConnect, World: Outside},
		{Name: "http/host.lookup", Fn: stdhttp.Lookup, Class: ClassConnect, World: Outside},
		{Name: "http/host.pipe", Fn: stdhttp.Pipe, World: Inside},
		{Name: "http/host.split_host_port", Fn: stdhttp.SplitHostPort, Pure: true},
		{Name: "http/host.join_host_port", Fn: stdhttp.JoinHostPort, Pure: true},
		{Name: "http/host.conn_close_read", Fn: stdhttp.ConnCloseRead, World: Inside, Held: true},
		{Name: "http/host.conn_no_delay", Fn: stdhttp.ConnNoDelay, World: Inside, Held: true},
		{Name: "http/host.conn_linger", Fn: stdhttp.ConnLinger, World: Inside, Held: true},
		{Name: "http/host.conn_keep_alive", Fn: stdhttp.ConnKeepAlive, World: Inside, Held: true},
		{Name: "http/host.conn_multipath", Fn: stdhttp.ConnMultipath, World: Inside, Held: true},
		{Name: "http/host.socket_set", Fn: stdhttp.SocketSet, World: Inside, Held: true},
		{Name: "http/host.socket_get", Fn: stdhttp.SocketGet, World: Inside, Held: true},
		{Name: "http/host.socket_group", Fn: stdhttp.SocketGroup, World: Inside, Held: true},
		{Name: "http/host.socket_multicast_interface", Fn: stdhttp.SocketMulticastInterface, World: Inside, Held: true},
		{Name: "http/host.udp_bind", Fn: stdhttp.UDPBind, Class: ClassListen, World: Outside},
		{Name: "http/host.udp_dial", Fn: stdhttp.UDPDial, Class: ClassConnect, World: Outside},
		{Name: "http/host.inherited_listeners", Fn: stdhttp.InheritedListeners, Class: ClassListen, World: Outside},
		{Name: "http/host.inherited_sockets", Fn: stdhttp.InheritedSockets, Class: ClassListen, World: Outside},
		{Name: "http/host.udp_receive", Fn: stdhttp.UDPReceive, World: Inside, Held: true},
		{Name: "http/host.udp_send", Fn: stdhttp.UDPSend, World: Inside, Held: true},
		{Name: "http/host.udp_address", Fn: stdhttp.UDPAddress, World: Inside, Held: true},
		{Name: "http/host.tls_dial", Fn: stdhttp.TLSDial, Class: ClassConnect, World: Outside},
		{Name: "http/host.tls_client", Fn: stdhttp.TLSClient, World: Inside, Held: true},
		{Name: "http/host.tls_server", Fn: stdhttp.TLSServer, World: Inside, Held: true},
		{Name: "http/host.conn_reader", Fn: stdhttp.ConnReader, World: Inside, Held: true},
		{Name: "http/host.conn_writer", Fn: stdhttp.ConnWriter, World: Inside, Held: true},
		{Name: "http/host.conn_close", Fn: stdhttp.ConnClose, World: Inside, Held: true},
		{Name: "http/host.conn_close_write", Fn: stdhttp.ConnCloseWrite, World: Inside, Held: true},
		{Name: "http/host.conn_local", Fn: stdhttp.ConnLocal, World: Inside, Held: true},
		{Name: "http/host.conn_remote", Fn: stdhttp.ConnRemote, World: Inside, Held: true},
		{Name: "http/host.conn_deadline", Fn: stdhttp.ConnDeadline, World: Inside, Held: true},
		{Name: "http/host.tls_state_parts", Fn: stdhttp.TLSStateParts, World: Inside, Held: true},
		{Name: "http/host.hello_parts", Fn: stdhttp.HelloParts, World: Inside, Held: true},
		{Name: "http/host.hello_supports", Fn: stdhttp.HelloSupports, World: Inside, Held: true},
		{Name: "http/host.tls_wrap_listener", Fn: stdhttp.TLSWrapListener, World: Inside, Held: true},
		{Name: "http/host.in_local", Fn: stdhttp.InLocal, World: Inside, Held: true},
		{Name: "http/host.in_tls", Fn: stdhttp.InTLS, World: Inside, Held: true},
		{Name: "http/host.extend_read_deadline", Fn: stdhttp.ExtendReadDeadline, World: Inside, Held: true},
		{Name: "http/host.upgrade", Fn: stdhttp.Upgrade, World: Inside, Held: true},
		{Name: "http/host.exchange_tls", Fn: stdhttp.ExchangeTLS, World: Inside, Held: true},
		{Name: "http/host.new_tracer", Fn: stdhttp.NewTracer, World: Inside},
		{Name: "http/host.tracer_events", Fn: stdhttp.TracerEvents, World: Inside, Held: true},
		{Name: "http/host.wire_read_request", Fn: stdhttp.WireReadRequest, World: Inside, Held: true},
		{Name: "http/host.wire_read_response", Fn: stdhttp.WireReadResponse, World: Inside, Held: true},
		{Name: "http/host.wire_parts", Fn: stdhttp.WireParts, World: Inside, Held: true},
		{Name: "http/host.wire_body", Fn: stdhttp.WireBody, World: Inside, Held: true},
		{Name: "http/host.wire_request", Fn: stdhttp.WireRequest, World: Inside, Held: true},
		{Name: "http/host.wire_response", Fn: stdhttp.WireResponse, World: Inside, Held: true},
		{Name: "http/host.chunked_reader", Fn: stdhttp.ChunkedReader, World: Inside, Held: true},
		{Name: "http/host.chunked_writer", Fn: stdhttp.ChunkedWriter, World: Inside, Held: true},
		{Name: "http/host.multipart_reader", Fn: stdhttp.MultipartReader, World: Inside, Held: true},
		{Name: "http/host.next_part", Fn: stdhttp.NextPart, World: Inside, Held: true},
		{Name: "http/host.part_headers", Fn: stdhttp.PartHeaders, World: Inside, Held: true},
		{Name: "http/host.part_reader", Fn: stdhttp.PartReader, World: Inside, Held: true},
		{Name: "http/host.part_form_name", Fn: stdhttp.PartFormName, World: Inside, Held: true},
		{Name: "http/host.part_file_name", Fn: stdhttp.PartFileName, World: Inside, Held: true},
		{Name: "http/host.multipart_write", Fn: stdhttp.MultipartWrite, World: Inside, Held: true},
		{Name: "http/host.close_connections", Fn: stdhttp.CloseConnections, World: Inside, Held: true},
		{Name: "http/host.ws_accept_key", Fn: stdhttp.WsAcceptKey, Pure: true},
		{Name: "http/host.ws_valid_key", Fn: stdhttp.WsValidKey, Pure: true},
		{Name: "http/host.ws_deflate_response", Fn: stdhttp.WsDeflateResponse, Pure: true},
		{Name: "http/host.ws_open", Fn: stdhttp.WsOpen, World: Inside, Held: true},
		{Name: "http/host.ws_connect", Fn: stdhttp.WsConnect, World: Outside},
		{Name: "http/host.ws_protocol", Fn: stdhttp.WsProtocol, World: Inside, Held: true},
		{Name: "http/host.ws_id", Fn: stdhttp.WsID, World: Inside, Held: true},
		{Name: "http/host.ws_receive", Fn: stdhttp.WsReceive, World: Inside, Held: true},
		{Name: "http/host.ws_send", Fn: stdhttp.WsSend, World: Inside, Held: true},
		{Name: "http/host.ws_ping", Fn: stdhttp.WsPing, World: Inside, Held: true},
		{Name: "http/host.ws_close", Fn: stdhttp.WsClose, World: Inside, Held: true},
		{Name: "http/host.ws_pipe", Fn: stdhttp.WsPipe, World: Inside},
		{Name: "http/host.tls_verify_hostname", Fn: stdhttp.TLSVerifyHostname, World: Inside, Held: true},
		{Name: "http/host.tls_export_keying_material", Fn: stdhttp.TLSExportKeyingMaterial, World: Inside, Held: true},
		{Name: "http/host.tls_server_end_point", Fn: stdhttp.TLSServerEndPoint, World: Inside, Held: true},
		{Name: "http/host.net_listen_with", Fn: stdhttp.ListenWith, Class: ClassListen, World: Outside},
		{Name: "http/host.net_listener_deadline", Fn: stdhttp.NetListenerDeadline, World: Inside, Held: true},
		{Name: "http/host.tls_listen", Fn: stdhttp.TLSListen, Class: ClassListen, World: Outside},
		{Name: "http/host.net_listener_address", Fn: stdhttp.NetListenerAddress, World: Inside, Held: true},
		{Name: "http/host.net_accept", Fn: stdhttp.NetAccept, World: Inside, Held: true},
		{Name: "http/host.net_listener_close", Fn: stdhttp.NetListenerClose, World: Inside, Held: true},
		{Name: "http/host.address", Fn: stdhttp.Address, World: Inside, Held: true},
		{Name: "http/host.next", Fn: stdhttp.Next, World: Inside, Held: true},
		{Name: "http/host.shutdown", Fn: stdhttp.Shutdown, World: Inside, Held: true},
		{Name: "http/host.in_method", Fn: stdhttp.InMethod, World: Inside, Held: true},
		{Name: "http/host.in_host", Fn: stdhttp.InHost, World: Inside, Held: true},
		{Name: "http/host.in_remote", Fn: stdhttp.InRemote, World: Inside, Held: true},
		{Name: "http/host.in_target", Fn: stdhttp.InTarget, World: Inside, Held: true},
		{Name: "http/host.in_protocol", Fn: stdhttp.InProtocol, World: Inside, Held: true},
		{Name: "http/host.in_headers", Fn: stdhttp.InHeaders, World: Inside, Held: true},
		{Name: "http/host.in_trailers", Fn: stdhttp.InTrailers, World: Inside, Held: true},
		{Name: "http/host.in_content_length", Fn: stdhttp.InContentLength, World: Inside, Held: true},
		{Name: "http/host.in_transfer_encoding", Fn: stdhttp.InTransferEncoding, World: Inside, Held: true},
		{Name: "http/host.in_close", Fn: stdhttp.InClose, World: Inside, Held: true},
		{Name: "http/host.in_body", Fn: stdhttp.InBody, World: Inside, Held: true},
		{Name: "http/host.parse_multipart", Fn: stdhttp.ParseMultipart, Pure: true},
		{Name: "http/host.encode_multipart", Fn: stdhttp.EncodeMultipart, Pure: true},
		{Name: "http/host.file_content_disposition", Fn: stdhttp.FileContentDisposition, Pure: true},
		{Name: "http/host.respond", Fn: stdhttp.Respond, World: Inside, Held: true},
		{Name: "http/host.produce", Fn: stdhttp.Produce, World: Inside, Held: true},
		{Name: "http/host.finish", Fn: stdhttp.Finish, World: Inside, Held: true},
		{Name: "http/host.answered_already", Fn: stdhttp.Answered, World: Inside, Held: true},
		{Name: "http/host.detect_content_type", Fn: stdhttp.DetectContentType, Pure: true},
		{Name: "http/host.new_mux", Fn: stdhttp.NewMux, Pure: true},
		{Name: "http/host.check_pattern", Fn: stdhttp.CheckPattern, Pure: true},
		{Name: "http/host.route", Fn: stdhttp.Route, Pure: true},
		{Name: "http/host.answer_content", Fn: stdhttp.AnswerContent, World: Inside},
		{Name: "http/host.answer_redirect", Fn: stdhttp.AnswerRedirect, Pure: true},
		{Name: "http/host.format_time", Fn: stdhttp.FormatTime, Pure: true},
		{Name: "http/host.parse_time", Fn: stdhttp.ParseTime, Pure: true},
		{Name: "http/host.limit_body", Fn: stdhttp.LimitBody, World: Inside, Held: true},
		{Name: "http/host.read_at_most", Fn: stdhttp.ReadAtMost, World: Inside, Held: true},
		{Name: "http/host.cookie_text", Fn: stdhttp.CookieText, Pure: true},
		{Name: "http/host.cookie_valid", Fn: stdhttp.CookieValid, Pure: true},
		{Name: "http/host.parse_cookie", Fn: stdhttp.ParseCookie, Pure: true},
		{Name: "http/host.parse_set_cookie", Fn: stdhttp.ParseSetCookie, Pure: true},
		{Name: "http/host.new_jar", Fn: stdhttp.NewJar, World: Inside},
		{Name: "http/host.jar_cookies", Fn: stdhttp.JarCookies, World: Inside, Held: true},
		{Name: "http/host.jar_set", Fn: stdhttp.JarSet, World: Inside, Held: true},
		{Name: "http/host.with_jar", Fn: stdhttp.WithJar, World: Inside, Held: true},
		// std/terminal's crossings read or change the terminal outside the
		// machine. Every target has them: where there is no terminal they
		// answer that there is none, as `io.read_line` answers the end of
		// the input.
		{Name: "terminal/host.size", Fn: stdterminal.Size, World: Outside},
		{Name: "terminal/host.supports_color", Fn: stdterminal.SupportsColor, World: Outside},
		{Name: "terminal/host.read_password", Fn: stdterminal.ReadPassword, World: Outside},
		{Name: "terminal/host.enter_raw", Fn: stdterminal.EnterRaw, World: Outside},
		{Name: "terminal/host.leave_raw", Fn: stdterminal.LeaveRaw, World: Outside},
		{Name: "terminal/host.read_byte", Fn: stdterminal.ReadByte, World: Outside},
		{Name: "process/host.start", Fn: stdprocess.Start, Class: ClassProcesses, World: Outside},
		{Name: "process/host.child_stdin", Fn: stdprocess.Stdin, World: Inside, Held: true},
		{Name: "process/host.child_stdout", Fn: stdprocess.Stdout, World: Inside, Held: true},
		{Name: "process/host.child_stderr", Fn: stdprocess.Stderr, World: Inside, Held: true},
		{Name: "process/host.wait", Fn: stdprocess.Wait, World: Inside, Held: true},
		{Name: "process/host.captured", Fn: stdprocess.Captured, World: Inside, Held: true},
		{Name: "process/host.kill", Fn: stdprocess.Kill, World: Inside, Held: true},
		// Signalling and the child's id act through the Child the program
		// holds. This process's own id is read from outside the machine, as
		// std/system's facts are, and every target answers it: no class.
		{Name: "process/host.child_id", Fn: stdprocess.ChildID, World: Inside, Held: true},
		{Name: "process/host.signal", Fn: stdprocess.Signal, World: Inside, Held: true},
		{Name: "process/host.id", Fn: stdprocess.ID, World: Outside},
		{Name: "process/host.new_fake", Fn: stdprocess.NewFake, World: Inside},
		{Name: "process/host.fake_answer", Fn: stdprocess.FakeAnswer, World: Inside, Held: true},
		{Name: "process/host.fake_refuse", Fn: stdprocess.FakeRefuse, World: Inside, Held: true},
		{Name: "process/host.fake_calls", Fn: stdprocess.FakeCalls, World: Inside, Held: true},
		{Name: "process/host.fake_command", Fn: stdprocess.FakeCommand, World: Inside, Held: true},
		{Name: "process/host.fake_dir", Fn: stdprocess.FakeDir, World: Inside, Held: true},
		{Name: "process/host.fake_env", Fn: stdprocess.FakeEnv, World: Inside, Held: true},
		{Name: "process/host.fake_input", Fn: stdprocess.FakeInput, World: Inside, Held: true},
		{Name: "csv/host.open", Fn: stdcsv.Open, World: Inside, Held: true},
		{Name: "csv/host.next", Fn: stdcsv.Next, World: Inside, Held: true},
		{Name: "csv/host.line", Fn: stdcsv.Line, World: Inside, Held: true},
		{Name: "csv/host.stopped", Fn: stdcsv.Stopped, World: Inside, Held: true},
		{Name: "csv/host.message", Fn: stdcsv.Message, World: Inside, Held: true},
		{Name: "csv/host.failure", Fn: stdcsv.Failure, World: Inside, Held: true},
		{Name: "csv/host.decode", Fn: stdcsv.Decode, Pure: true},
		{Name: "csv/host.encode", Fn: stdcsv.Encode, Pure: true},
		{Name: "xml/host.open", Fn: stdxml.Open, World: Inside, Held: true},
		{Name: "xml/host.next", Fn: stdxml.Next, World: Inside, Held: true},
		{Name: "xml/host.current", Fn: stdxml.Current, World: Inside, Held: true},
		{Name: "xml/host.stopped", Fn: stdxml.Stopped, World: Inside, Held: true},
		{Name: "xml/host.message", Fn: stdxml.Message, World: Inside, Held: true},
		{Name: "xml/host.failure", Fn: stdxml.Failure, World: Inside, Held: true},
		{Name: "xml/host.decode", Fn: stdxml.Decode, Pure: true},
		{Name: "xml/host.escape_text", Fn: stdxml.EscapeText, Pure: true},
		{Name: "xml/host.escape_attribute", Fn: stdxml.EscapeAttribute, Pure: true},
		{Name: "xml/host.is_name", Fn: stdxml.IsName, Pure: true},
		// A module's compile and `run` depend only on their operands, and
		// each can take long: a module compiles in time that grows with its
		// size, and nothing bounds how long a call runs. An instance is
		// state inside the machine that the operations after act through.
		{Name: "wasm/host.compile", Fn: stdwasm.Compile, Pure: true, Unbounded: true},
		{Name: "wasm/host.run", Fn: stdwasm.Run, Pure: true, Unbounded: true},
		{Name: "wasm/host.instantiate", Fn: stdwasm.Instantiate, World: Inside},
		{Name: "wasm/host.call", Fn: stdwasm.Call, World: Inside, Held: true},
		{Name: "wasm/host.run_on", Fn: stdwasm.RunOn, World: Inside, Held: true},
		{Name: "wasm/host.read", Fn: stdwasm.Read, World: Inside, Held: true},
		{Name: "wasm/host.write", Fn: stdwasm.Write, World: Inside, Held: true},
		{Name: "wasm/host.size", Fn: stdwasm.Size, World: Inside, Held: true},
		{Name: "wasm/host.drop", Fn: stdwasm.Drop, World: Inside, Held: true},
		{Name: "wasm/host.memory_read", Fn: stdwasm.MemoryRead, World: Inside, Held: true},
		{Name: "wasm/host.memory_write", Fn: stdwasm.MemoryWrite, World: Inside, Held: true},
		{Name: "wasm/host.memory_size", Fn: stdwasm.MemorySize, World: Inside, Held: true},
		{Name: "iter.as_count", Fn: rt.IntAsCount, Pure: true},
		// Byte-level primitives for binary formats and wire protocols:
		// std/bytes's reads at an index, searches, UTF-8 repair and
		// Builder, and std/int's and std/float's machine-word operations.
		{Name: "bytes.Bytes.to_string_lossy", Fn: rt.BytesToStringLossy, Pure: true},
		{Name: "bytes.Bytes.valid_utf8_length", Fn: rt.BytesValidUTF8Length, Pure: true},
		{Name: "bytes.Bytes.concat_all", Fn: rt.BytesConcatAll, Pure: true},
		{Name: "bytes.index_of_from", Fn: rt.BytesIndexOf, Pure: true},
		{Name: "bytes.index_of_byte_from", Fn: rt.BytesIndexOfByte, Pure: true},
		{Name: "bytes.Bytes.last_index_of", Fn: rt.BytesLastIndexOf, Pure: true},
		{Name: "bytes.Bytes.last_index_of_byte", Fn: rt.BytesLastIndexOfByte, Pure: true},
		{Name: "bytes.index_of_any_from", Fn: rt.BytesIndexOfAny, Pure: true},
		{Name: "bytes.Bytes.last_index_of_any", Fn: rt.BytesLastIndexOfAny, Pure: true},
		{Name: "bytes.index_of_none_from", Fn: rt.BytesIndexOfNone, Pure: true},
		{Name: "bytes.Bytes.last_index_of_none", Fn: rt.BytesLastIndexOfNone, Pure: true},
		{Name: "bytes.Bytes.contains_bytes", Fn: rt.BytesContainsBytes, Pure: true},
		{Name: "bytes.Bytes.starts_with", Fn: rt.BytesStartsWith, Pure: true},
		{Name: "bytes.Bytes.ends_with", Fn: rt.BytesEndsWith, Pure: true},
		{Name: "bytes.Bytes.split", Fn: rt.BytesSplit, Pure: true},
		{Name: "bytes.Bytes.count", Fn: rt.BytesCount, Pure: true},
		{Name: "bytes.Bytes.uint8_at", Fn: rt.BytesUint8At, Pure: true},
		{Name: "bytes.Bytes.int8_at", Fn: rt.BytesInt8At, Pure: true},
		{Name: "bytes.Bytes.uint16_be_at", Fn: rt.BytesUint16BEAt, Pure: true},
		{Name: "bytes.Bytes.uint16_le_at", Fn: rt.BytesUint16LEAt, Pure: true},
		{Name: "bytes.Bytes.int16_be_at", Fn: rt.BytesInt16BEAt, Pure: true},
		{Name: "bytes.Bytes.int16_le_at", Fn: rt.BytesInt16LEAt, Pure: true},
		{Name: "bytes.Bytes.uint32_be_at", Fn: rt.BytesUint32BEAt, Pure: true},
		{Name: "bytes.Bytes.uint32_le_at", Fn: rt.BytesUint32LEAt, Pure: true},
		{Name: "bytes.Bytes.int32_be_at", Fn: rt.BytesInt32BEAt, Pure: true},
		{Name: "bytes.Bytes.int32_le_at", Fn: rt.BytesInt32LEAt, Pure: true},
		{Name: "bytes.Bytes.uint64_be_at", Fn: rt.BytesUint64BEAt, Pure: true},
		{Name: "bytes.Bytes.uint64_le_at", Fn: rt.BytesUint64LEAt, Pure: true},
		{Name: "bytes.Bytes.int64_be_at", Fn: rt.BytesInt64BEAt, Pure: true},
		{Name: "bytes.Bytes.int64_le_at", Fn: rt.BytesInt64LEAt, Pure: true},
		{Name: "bytes.Bytes.float32_be_at", Fn: rt.BytesFloat32BEAt, Pure: true},
		{Name: "bytes.Bytes.float32_le_at", Fn: rt.BytesFloat32LEAt, Pure: true},
		{Name: "bytes.Bytes.float64_be_at", Fn: rt.BytesFloat64BEAt, Pure: true},
		{Name: "bytes.Bytes.float64_le_at", Fn: rt.BytesFloat64LEAt, Pure: true},
		{Name: "bytes.Bytes.uvarint_at", Fn: rt.BytesUvarintAt, Pure: true},
		{Name: "bytes.Bytes.varint_at", Fn: rt.BytesVarintAt, Pure: true},
		{Name: "bytes.Bytes.xor", Fn: rt.BytesXor, Pure: true},
		{Name: "bytes.Bytes.xor_repeating", Fn: rt.BytesXorRepeating, Pure: true},
		{Name: "bytes.Bytes.fill", Fn: rt.BytesFill, Pure: true, Unbounded: true},
		{Name: "bytes.Bytes.reverse", Fn: rt.BytesReverse, Pure: true},
		{Name: "bytes.Bytes.code_point_at", Fn: rt.BytesCodePointAt, Pure: true},
		{Name: "checksum.crc32_update", Fn: rt.Crc32Update, Pure: true},
		{Name: "checksum.crc32c_update", Fn: rt.Crc32cUpdate, Pure: true},
		{Name: "checksum.crc64_iso_update", Fn: rt.Crc64IsoUpdate, Pure: true},
		{Name: "checksum.crc64_ecma_update", Fn: rt.Crc64EcmaUpdate, Pure: true},
		{Name: "checksum.adler32_update", Fn: rt.Adler32Update, Pure: true},
		{Name: "checksum.fnv1a_32_update", Fn: rt.Fnv1a32Update, Pure: true},
		{Name: "checksum.fnv1a_64_update", Fn: rt.Fnv1a64Update, Pure: true},
		{Name: "bytes.builder_new", Fn: rt.BytesBuilderNew, Pure: true},
		{Name: "bytes.Builder.length", Fn: rt.BytesBuilderLength, Pure: true},
		{Name: "bytes.Builder.to_bytes", Fn: rt.BytesBuilderToBytes, Pure: true},
		{Name: "bytes.Builder.push", Fn: rt.BytesBuilderPush, Pure: true},
		{Name: "bytes.Builder.push_bytes", Fn: rt.BytesBuilderPushBytes, Pure: true},
		{Name: "bytes.Builder.push_string", Fn: rt.BytesBuilderPushString, Pure: true},
		{Name: "bytes.Builder.push_int8", Fn: rt.BytesBuilderPushInt8, Pure: true},
		{Name: "bytes.Builder.push_int16_be", Fn: rt.BytesBuilderPushInt16BE, Pure: true},
		{Name: "bytes.Builder.push_int16_le", Fn: rt.BytesBuilderPushInt16LE, Pure: true},
		{Name: "bytes.Builder.push_int32_be", Fn: rt.BytesBuilderPushInt32BE, Pure: true},
		{Name: "bytes.Builder.push_int32_le", Fn: rt.BytesBuilderPushInt32LE, Pure: true},
		{Name: "bytes.Builder.push_int64_be", Fn: rt.BytesBuilderPushInt64BE, Pure: true},
		{Name: "bytes.Builder.push_int64_le", Fn: rt.BytesBuilderPushInt64LE, Pure: true},
		{Name: "bytes.Builder.push_float32_be", Fn: rt.BytesBuilderPushFloat32BE, Pure: true},
		{Name: "bytes.Builder.push_float32_le", Fn: rt.BytesBuilderPushFloat32LE, Pure: true},
		{Name: "bytes.Builder.push_float64_be", Fn: rt.BytesBuilderPushFloat64BE, Pure: true},
		{Name: "bytes.Builder.push_float64_le", Fn: rt.BytesBuilderPushFloat64LE, Pure: true},
		{Name: "bytes.Builder.push_uvarint", Fn: rt.BytesBuilderPushUvarint, Pure: true},
		{Name: "bytes.Builder.push_varint", Fn: rt.BytesBuilderPushVarint, Pure: true},
		{Name: "int.Int.wrapping_add_32", Fn: rt.IntWrapAdd32, Pure: true},
		{Name: "int.Int.wrapping_sub_32", Fn: rt.IntWrapSub32, Pure: true},
		{Name: "int.Int.wrapping_mul_32", Fn: rt.IntWrapMul32, Pure: true},
		{Name: "int.Int.rotate_left_32", Fn: rt.IntRotateLeft32, Pure: true},
		{Name: "int.Int.rotate_left_64", Fn: rt.IntRotateLeft64, Pure: true},
		{Name: "int.Int.leading_zeros_32", Fn: rt.IntLeadingZeros32, Pure: true},
		{Name: "int.Int.leading_zeros_64", Fn: rt.IntLeadingZeros64, Pure: true},
		{Name: "int.Int.trailing_zeros_32", Fn: rt.IntTrailingZeros32, Pure: true},
		{Name: "int.Int.trailing_zeros_64", Fn: rt.IntTrailingZeros64, Pure: true},
		{Name: "int.Int.swap_bytes_16", Fn: rt.IntSwapBytes16, Pure: true},
		{Name: "int.Int.swap_bytes_32", Fn: rt.IntSwapBytes32, Pure: true},
		{Name: "int.Int.swap_bytes_64", Fn: rt.IntSwapBytes64, Pure: true},
		{Name: "int.Int.wrap_signed", Fn: rt.IntWrapSigned, Pure: true},
		{Name: "float.Float.to_bits", Fn: rt.FloatBits, Pure: true},
		{Name: "float.Float.from_bits", Fn: rt.FloatFromBits, Pure: true},
		{Name: "float.Float.to_bits_32", Fn: rt.FloatToBits32, Pure: true},
		{Name: "float.Float.from_bits_32", Fn: rt.FloatFromBits32, Pure: true},
	}
	for i := range rows {
		rows[i].PanicsPropagate = true
	}
	return rows
}
