// Package cldrdata holds the locale data Nomi takes from CLDR, and reads it.
//
// internal/cldrgen writes the data from the pinned CLDR release vendored in
// internal/cldrgen/data, as blobs in internal/cldrfull: root.cldr (plural
// rules, parent locales, numbering-system digits, ISO 4217 codes and their
// minor digits, the root collation), locales.cldr (each locale's number
// symbols and patterns, list patterns, grammatical features, currency
// symbols, names and patterns, calendar names and patterns, relative-time
// forms, duration units and collation tailorings) and collation-cjk.cldr
// (the large Chinese and Korean collation tailorings, which a program
// carries only when it lists those locales). This package decodes a blob
// from any source and answers questions of the decoded Data; it embeds
// nothing.
//
// # Where a program's data comes from
//
// A program carries the data of the locales its nomi.toml lists
// (`[i18n].locales`), cut down to the parts it reaches, in its image
// (Select writes that bundle). A built executable decodes the bundle from its
// image; `nomi run`, `nomi test` and the REPL build the same bundle from the
// data internal/cldrfull compiles into `nomi`. A machine hands its Data to the
// std adapters through the frames it runs on (WithData, From), so the
// runner, which links no cldrfull, never carries data a program did not ask
// for. A Go function that reads the data takes *rt.Frame first and finds it
// on the frame's context (stdlocale.Data). A built program whose date
// skeletons are all literals carries each one's pattern resolved per listed
// locale instead of the available formats (Selection.Skeletons).
//
// # Format
//
// Text, one record per line, fields separated by tabs:
//
//	nomi-cldr	6                         format version
//	cldr	48.2                              CLDR release
//	parent	<tag>	<parent tag>             CLDR parentLocales ("und" is root)
//	plural-parent	<tag>	<parent tag>     parentLocales for the plurals component
//	nonlikely-script-parent	<tag>          a tag whose script is not the language's likely one has this parent
//	cardinal	<tag,tag,...>	<category> <rule>	...   cardinal rules, shared by the listed locales
//	ordinal	<tag,tag,...>	<category> <rule>	...    ordinal rules
//	digits	<system>	<ten digits>       a numeric numbering system's digits, zero first
//	currencies	<code,code,...>          every ISO 4217 code CLDR knows, current or historical
//	currency-digits	<n>	<code,code,...>  the codes whose minor digits are n, not 2
//	available	<tag,tag,...>            the locales a program lists (Locale.available)
//	locale	<tag>                           opens a locale's section; the records below are its own
//	numbering	<default system>	<native system>
//	min-grouping	<n>                     CLDR's minimumGroupingDigits
//	symbols	<system>	<decimal>	<group>	<percent>	<plus>	<minus>	<infinity>	<nan>
//	decimal	<system>	<pattern>          the standard decimal pattern
//	percent	<system>	<pattern>          the standard percent pattern
//	list	<type>	<start>	<middle>	<end>	<two>   a list pattern (standard, or-short, unit-narrow, ...)
//	genders	<gender,gender,...>        the grammatical genders of the language's nouns
//	cases	<case,case,...>            the grammatical cases of the language's nouns
//	currency-symbol	<code>	<symbol>        a currency's symbol (the code when none)
//	currency-narrow	<code>	<symbol>        its narrow symbol (the symbol when none)
//	currency-names	<code>	<zero>	<one>	<two>	<few>	<many>	<other>   its name in each plural form
//	currency-pattern	<system>	<standard>	<alphaNextToNumber>   the currency patterns
//	currency-unit	<zero>	...	<other>      an amount with its currency's name (`{0} {1}`)
//	months	<context>-<width>	<12 names>   format or stand-alone; abbreviated, wide, narrow
//	days	<context>-<width>	<7 names>     Sunday first; also short
//	day-periods	<context>-<width>	<am>	<pm>
//	eras	<width>	<before>	<after>      abbreviated, wide, narrow
//	date-format	<style>	<pattern>      full, long, medium, short
//	time-format	<style>	<pattern>
//	datetime-format	<style>	<pattern>  how a date ({1}) and a time ({0}) join
//	skeleton	<skeleton>	<pattern>      an available format, or a program's skeleton resolved
//	gmt	<format>	<zero>	<hour format>   the localized GMT format
//	relative	<unit>	<-2>	<-1>	<0>	<1>	<2>   a unit's words for days near now (yesterday)
//	relative-future	<unit>	<zero>	...	<other>   `in {0} days`
//	relative-past	<unit>	<zero>	...	<other>     `{0} days ago`
//	duration-unit	<unit>-<width>	<zero>	...	<other>   `{0} hours`
//	collation	<type>	<n>              a collation type's tailoring: n bytes on the next line
//	collation-default	<type>          the collation type a locale uses when its tag names none
//	collation-parent	<tag>	<parent>   the collations component's parentLocales
//	collation-root	<n>                 the root collation table: n bytes on the next line
//	remote	<tag,tag,...>             locales whose numbers and lists are in locale files (store.go)
//	source	<tag>                    the program's source locale, which a locale it does not carry formats as
//	build	<id>                       the build a locale file must name to be loaded
//
// A rule set lists every category but `other`, which is what matches when
// no rule does. A locale section may hold only the records its CLDR parents
// do not already give it; Resolve looks a record up along the locale's chain
// and then in und, which always holds every record.
//
// # A locale the program does not carry
//
// A program that lists its locales carries their sections and und's, and
// its bundle names its source locale (`[i18n].source_locale`) when it lists
// that too. A locale whose chain reaches none of the carried sections
// (`fr` in a program that lists `en` and `de`, or a browser build's `pl`
// before its file is loaded) is looked up in the source locale's section
// before und's, so it formats as the source language rather than as root's
// bare patterns (`2026 M10 20`). A chain that reaches a carried locale never
// reads the source's records, and data with no source record (every
// locale's, or a program that lists none) is looked up along the chain and
// then in und. A typed `Locale` literal of a locale the program does not
// list is a compile error.
package cldrdata
