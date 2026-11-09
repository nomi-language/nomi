// Package collation compares strings as the reader of a language orders
// them: the Unicode Collation Algorithm (UTS #10) over CLDR's root
// collation, with CLDR's per-locale tailorings (UTS #35 part 5).
//
// The design is ICU's, as ICU4X ports it: one root table every locale
// shares, and per locale a tailoring that overlays it. internal/cldrgen
// builds both from the pinned CLDR release (FractionalUCA.txt and the
// collation/*.xml rules, compiled by package collgen) and writes them into
// the locale data a program carries; this package decodes them and
// compares. It links no generator and no rule parser.
//
// A string is normalized to NFD, mapped to collation elements (CEs) through
// the tailoring and then the root (contractions, discontiguous contractions,
// prefix contexts, expansions, implicit weights for Han and unassigned code
// points, and numeric ordering when asked), and the CEs are compared level
// by level as the options say: strength, alternate handling, case first,
// case level, backwards secondary, and script reordering.
//
// # Where the tables are
//
// std/collation's `Collator` reads the tables from the locale data a
// program carries (internal/cldrdata): the root collation in root.cldr,
// each locale's tailorings in its section, and the Chinese and Korean ones
// (over 64 KB each) in collation-cjk.cldr, which only a bundle for listed
// locales reads, so only a program listing them carries them. Tailorings
// follow their own fallback chain (the `collation-parent` records).
// collgen builds the root from
// internal/cldrgen/data/collation/FractionalUCA_SHORT.txt and each
// tailoring by compiling CLDR's rules with a port of ICU's
// CollationBuilder.
//
// # Conformance
//
// The conformance tests (conformance_test.go) run CLDR's CollationTest
// files, vendored gzipped in testdata, against the carried root.
// internal/cldrgen's TestCollationMatchesICU compares every type with ICU
// when NOMI_ICU_ORACLE names a build of
// internal/cldrgen/testdata/icuoracle.c (macOS). It skips three differences
// where Nomi follows UTS #10 and CLDR's conformance files rather than
// ICU4C: ICU4C's root orders Han by radical and stroke (Nomi by code
// point); ICU orders a precomposed Turkish `â̧` apart from its NFD form
// (Nomi treats them alike); ICU keeps U+0901 and U+0A01 after a variable
// character in shifted mode (UTS #10 makes them ignorable). Some CLDR rule
// sets (`aa`, `bal`, `cu`, `dz`, `vo`, `wae` and others, and `cs`'s
// `digits-after`) are not in ICU's data, so nothing compares them.
package collation
