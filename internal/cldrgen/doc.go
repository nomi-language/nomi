// Package cldrgen generates Nomi's locale data from one pinned CLDR release.
//
// The source is cldr-json at Release, vendored under data/ with the Unicode
// license it is published under:
//
//   - from cldr-core, unchanged: supplemental/plurals.json, ordinals.json,
//     parentLocales.json, numberingSystems.json and grammaticalFeatures.json
//     (each language's grammatical genders and cases);
//   - from cldr-core, cut to what is read: currencyData.json (each
//     currency's minor digits, the codes regions have used and those in use
//     today);
//   - numbers.json and listPatterns.json, every locale's
//     main/<tag>/numbers.json (cldr-numbers-full) and listPatterns.json
//     (cldr-misc-full) gathered into one file each, keeping only the keys
//     this package reads, with their values unchanged;
//   - currencies.json (cldr-numbers-full: symbols, and the plural names of
//     the currencies in use today), ca-gregorian.json, dateFields.json and
//     timeZoneNames.json (cldr-dates-full: Gregorian names and patterns,
//     available formats, relative-time forms, the GMT format) and
//     units.json (cldr-units-full: the duration units), gathered the same
//     way but holding each locale's own values only, those its CLDR parent
//     does not already give it (sources.go reads them back resolved along
//     the same chain).
//
// `go run ./internal/cldrgen/extract <dir>` writes them from the unpacked
// npm packages (cldr-core, cldr-numbers-full, cldr-misc-full,
// cldr-dates-full, cldr-units-full). Building Nomi needs no network;
// upgrading CLDR is replacing those files with a newer release's, changing
// Release, and running `go generate ./internal/cldrfull`.
// TestGeneratedDataIsCurrent fails while the generated blobs are stale.
//
// It writes internal/cldrfull's root.cldr (plural rules, parent locales,
// numbering-system digits, and ISO 4217 codes with their minor digits) and
// locales.cldr (every locale's number symbols, decimal, percent and
// currency patterns, list patterns, grammatical genders and cases, currency
// symbols and names, calendar names and patterns, available formats,
// relative-time forms and duration units), in the format
// internal/cldrdata's comment gives.
//
// The collation data comes from CLDR's source release instead, as
// cldr-json has none: data/collation holds common/uca/FractionalUCA_SHORT.txt
// unchanged and rules.txt, the collation/*.xml rules of the types
// CollationTypes names (`go run ./internal/cldrgen/extract collation <dir>`
// over the unpacked CLDR source release). collation.go compiles them with
// internal/collation/collgen into the root collation (in root.cldr), each
// locale's tailorings (in its section of locales.cldr) and the large
// Chinese and Korean tailorings (collation-cjk.cldr).
// TestCollationMatchesICU (icu_test.go) checks the compiled collations
// against ICU when NOMI_ICU_ORACLE names a build of testdata/icuoracle.c.
//
// The plural rules are kept as CLDR writes them, without samples; every one
// must parse with cldrdata.ParseRule, and every number pattern with
// cldrdata.ParsePattern. The samples stay in the vendored files, where the
// tests check every one against the generated rules.
package cldrgen
