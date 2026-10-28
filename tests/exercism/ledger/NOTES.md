# Ledger

Subject: Strings & Text

Print a ledger of dated entries as a fixed-width table, with dates and amounts in American or Dutch style.

Adapted from Exercism problem-specifications (MIT), exercises/ledger.

## Choices

- The exercise is a refactoring one: tracks ship a badly written printer to clean up. There is no Nomi stub to start from, so this is the solution written directly, against the canonical cases.
- The canonical `locale` input, `en_US` or `nl_NL`, is a `Style` enum, `American` or `Dutch`, since the ledger has headings for those two and no others. Each style knows its std `Locale` and its three headings.
- Dates and amounts come from std's CLDR data rather than hand-written patterns. `Date.format_fields` with the skeleton `yMMdd` writes `01/01/2015` in `en-US` and `12-03-2015` in `nl-NL`, and `Money.format` with the `Narrow` symbol writes `$1,234.56` and `$ 1.234,56` (US dollars are `US$` in Dutch with the plain symbol).
- Two things are the ledger's own and are done by hand: an American negative amount is the absolute amount in parentheses (see Friction), and the Dutch amount's no-break space after the symbol becomes a plain space, which the canonical lines use.
- An entry holds its change in cents, as the canonical input does, and the currency is the ledger's. The change becomes a `Money` only to be formatted.
- The entries are sorted by one tuple key, `#(date, description, cents)` (style §32).
- A row is its three columns padded and joined with `" | "`. The heading pads its last title on the right before the row pads it on the left, so the title sits at the left of its column and the amounts at the right.
- The twelve canonical cases are one `subtests` table. Two canonical cases share the description "multiple entries on same date ordered by description"; the first of them, whose entries are on different dates, is named "..., as first written" so each row has its own name.

## Friction

- [stdlib] `Money.format` writes only CLDR's standard currency pattern, `-$10.00`, not its accounting pattern, `($10.00)`, which is what the American ledger shows. So `format_change` formats `Money.abs` of a negative amount and wraps it in parentheses itself. JavaScript's `Intl.NumberFormat` takes `currencySign: "accounting"`, and ICU and Java have an accounting currency style.
