---
title: "Formatting for a Reader"
description: "Write numbers, prices, dates, durations and lists the way a reader's language writes them, read what people type, pick the reader's locale, and sort names as their language orders them."
---

This chapter writes numbers, prices, dates and lists the way a reader's
language writes them, and reads back what they type. A **locale** names one
way of writing: a language, and sometimes a region, such as English as
written in the United States. An American writes `1,299.50`, a German
`1.299,50`.

Nomi never guesses the locale. Every function whose result depends on one
takes the locale as an argument. The rules for each locale come from
[CLDR](https://cldr.unicode.org/), a shared collection of locale data kept
by the Unicode project.

The examples come from a shop's order page: what the items cost, when they
ship, and who else is looking at them. The next chapter,
[Translating Text](/translating-text/), translates the page's sentences.

## Numbers

A [`Locale`](/reference/locale/) is written as a short code called a
language tag, in backticks: `` Locale`en-US` `` is English as written in the
United States, `` Locale`de` `` is German, and `` Locale`pt-BR` `` is
Portuguese as written in Brazil. The language comes first, then the region.

`Int.format`, `Float.format` and `Decimal.format` write a number the way a
reader of a locale expects. `format_percent` writes a fraction as a
percentage, so `0.15` becomes `15%`. Use `Decimal` for prices, so the cents
stay exact.

```nomi-run
import {
    std/io
    std/locale.Locale
}

fn main() {
    sold = 1234567
    price = 1299.50d
    discount = 0.15d

    [
        Locale`en-US`,
        Locale`de`,
        Locale`fr`,
        Locale`hi`,
        Locale`ar-EG`,
        Locale`ar-EG-u-nu-latn`,
    ]
    |> Iter.each(fn locale -> {
        count = Int.format(sold, locale)
        amount = Decimal.format(price, locale)
        off = Decimal.format_percent(discount, locale)
        io.print("${locale}: ${count} sold, ${amount}, ${off} off")
    })
}

```
<!-- expect
en-US: 1,234,567 sold, 1,299.50, 15% off
de: 1.234.567 sold, 1.299,50, 15 % off
fr: 1 234 567 sold, 1 299,50, 15 % off
hi: 12,34,567 sold, 1,299.50, 15% off
ar-EG: ١٬٢٣٤٬٥٦٧ sold, ١٬٢٩٩٫٥٠, ١٥٪؜ off
ar-EG-u-nu-latn: 1,234,567 sold, 1,299.50, 15‎%‎ off
-->

German swaps the comma and the dot. French separates thousands with a thin
space that never breaks across lines. Hindi groups digits in twos after the
first three, so 1,234,567 is `12,34,567`. Egyptian Arabic uses Arabic
digits. Adding `-u-nu-latn` to a locale makes it use the digits 0 to 9 and
the separators that go with them.

Putting a number in a string, as in `"${sold}"`, always gives `1234567`.
That text often ends up in web addresses, headers or CSV files, which a
program reads, so it must not change from reader to reader.

Each format function takes options as named arguments:

- `Int.format`: `grouping` turns the thousands separators on or off, and
  `min_digits` pads with zeros.
- `Float.format` and `Decimal.format`: `min_fraction` and `max_fraction`
  set how many digits come after the decimal point, and `grouping` works as
  for `Int`.
- `format_percent`: `max_fraction`, and for a `Decimal` also `min_fraction`
  and `mode`.

A `Float` is rounded to three digits after the point unless you pass
`max_fraction`, and a tie rounds to the even digit. A `Decimal` shows every digit it has unless you pass `max_fraction`.
Then it rounds by `mode`, which is also half to even unless you pass
another, such as `.HalfUp`.

```nomi-run
import std/locale.Locale

fn main() {
    rating = 4.8125

    dbg Float.format(rating, Locale`de`, max_fraction: 1)
    dbg Float.format(4.5, Locale`de`, min_fraction: 2)
    dbg Float.format_percent(0.256, Locale`en-US`)
    dbg Decimal.format(2.665d, Locale`en-US`, max_fraction: 2)
    dbg Decimal.format(2.665d, Locale`en-US`, max_fraction: 2, mode: .HalfUp)
    dbg Int.format(1234567, Locale`en-US`, grouping: False)
    dbg Int.format(7, Locale`en-US`, min_digits: 2)
    Unit
}

```
<!-- expect
dbg line 6: Float.format(rating, Locale`de`, max_fraction: 1) = "4,8"
dbg line 7: Float.format(4.5, Locale`de`, min_fraction: 2) = "4,50"
dbg line 8: Float.format_percent(0.256, Locale`en-US`) = "26%"
dbg line 9: Decimal.format(2.665d, Locale`en-US`, max_fraction: 2) = "2.66"
dbg line 10: Decimal.format(2.665d, Locale`en-US`, max_fraction: 2, mode: .HalfUp) = "2.67"
dbg line 11: Int.format(1234567, Locale`en-US`, grouping: False) = "1234567"
dbg line 12: Int.format(7, Locale`en-US`, min_digits: 2) = "07"
-->

## Money

A `Money` is an amount in a currency, such as
`` Money`1299.5 USD` ``. `Money.format` writes a price the way a reader of a
locale expects, with the currency's symbol in the right place. The
[Money](/money/) chapter covers writing amounts, adding and rounding them,
and custom currencies.

```nomi-run
import {
    std/io
    std/locale.Locale
    std/money.Money
}

fn main() {
    price = Money`1299.5 USD`

    [Locale`en-US`, Locale`de`, Locale`fr`]
    |> Iter.each(fn locale -> io.print("${locale}: ${Money.format(price, locale)}"))

    dbg "${price}"
    dbg Money.format(Money`1500 JPY`, Locale`ja`)
    Unit
}

```
<!-- expect
en-US: $1,299.50
de: 1.299,50 $
fr: 1 299,50 $US
dbg line 13: "${price}" = "1299.5 USD"
dbg line 14: Money.format(Money`1500 JPY`, Locale`ja`) = "￥1,500"
-->

The dollar sign comes before the amount in American English and after it in
German and French. French writes `$US`, since several countries call their
money a dollar. CLDR also knows how many digits each currency has after the
decimal point. The dollar has two, so `1299.5` becomes `1,299.50`. The yen
has none.

Putting a `Money` in a string, as in `"${price}"`, gives the amount and the
code: `1299.5 USD`, the text its literal is written in. Like a number in a
string, it is the same for every reader.

### Showing the currency

`display:` picks how the currency is shown: its symbol (the default), its
narrow symbol, its code, or its name.

```nomi-run
import {
    std/locale.Locale
    std/money.Money
}

fn main() {
    usd = Money`5 USD`
    dbg Money.format(usd, Locale`en-US`, display: .Code)
    dbg Money.format(usd, Locale`en-US`, display: .Name)
    dbg Money.format(usd, Locale`en-CA`)
    dbg Money.format(usd, Locale`en-CA`, display: .Narrow)
    Unit
}

```
<!-- expect
dbg line 8: Money.format(usd, Locale`en-US`, display: .Code) = "USD\u{a0}5.00"
dbg line 9: Money.format(usd, Locale`en-US`, display: .Name) = "5.00 US dollars"
dbg line 10: Money.format(usd, Locale`en-CA`) = "US$5.00"
dbg line 11: Money.format(usd, Locale`en-CA`, display: .Narrow) = "$5.00"
-->

After `USD` is a no-break space, which `dbg` writes `\u{a0}`. Canadian
English writes `US$` for the US dollar, since `$` alone would mean the
Canadian dollar. `.Narrow` drops the `US`, for a page where the currency is
clear. A [custom currency](/money/#custom-currencies) has no symbol or name
in CLDR, so it is always shown by its code.

## Dates and times

An American writes October 20 as `10/20/26`, and a German as `20.10.26`.
The types in [`std/calendar`](/reference/calendar/) (`Date`, `Time`,
`NaiveDateTime` and `DateTime`) each have a `format` that writes them for a
locale. `style:` picks how long the text is: `.Short`, `.Medium` (the
default for a date), `.Long` or `.Full`.

```nomi-run
import {
    std/calendar.{Date, DateTime}
    std/io
    std/locale.Locale
}

fn main() {
    ships = Date`2026-10-20`
    order = DateTime`2026-10-20T17:30:00-04:00[America/New_York]`

    [Locale`en-US`, Locale`de`]
    |> Iter.each(fn locale -> {
        io.print(Date.format(ships, locale, style: .Full))
        io.print(Date.format(ships, locale, style: .Short))
        io.print(DateTime.format(order, locale))
        io.print(DateTime.format(order, locale, date: .Long, time: .Long))
    })
}

```
<!-- expect
Tuesday, October 20, 2026
10/20/26
Oct 20, 2026, 5:30 PM
October 20, 2026, 5:30:00 PM GMT-4
Dienstag, 20. Oktober 2026
20.10.26
20.10.2026, 17:30
20. Oktober 2026, 17:30:00 GMT-4
-->

A time is written with the clock the locale uses: `5:30 PM` in American
English and `17:30` in German. `Time.format` writes a time on its own. A
`DateTime` takes a style for each half, as `date:` (`.Medium` by default)
and `time:` (`.Short`). `.Long` and `.Full` times show the offset from UTC, here four hours behind.

### Choosing the fields

A style decides which fields appear. To choose them yourself, say what you
want with a **skeleton**: a few letters for each field, in any order, where
the number of letters sets the width: `MMMM` is the month's full name and
`MMM` its short name, `EEEE` the weekday's name, `d` the day of the month,
and `y` the year. CLDR then finds the order and punctuation each
locale uses for those fields.

```nomi-run
import {
    std/calendar.{Date, DateFields}
    std/io
    std/locale.Locale
}

fn main() {
    ships = Date`2026-10-20`

    [Locale`en-US`, Locale`de`]
    |> Iter.each(fn locale -> {
        day = Date.format_fields(ships, locale, DateFields`MMMMEEEEd`)
        month = Date.format_fields(ships, locale, DateFields`yMMM`)
        io.print("${locale}: ${day} | ${month}")
    })
}

```
<!-- expect
en-US: Tuesday, October 20 | Oct 2026
de: Dienstag, 20. Oktober | Okt. 2026
-->

The skeleton is a `DateFields` for a `Date`, a `TimeFields` for a `Time`,
and a `DateTimeFields` for both. The compiler checks its letters, so a time
field in a `DateFields` is an error.

### Fixed patterns

A file name, a log line or another program's input needs a date that is the
same for every reader. A `DatePattern` spells out the exact layout, with
`yyyy` for the year, `MM` for the month and `dd` for the day:
`` Date.to_string_with(ships, DatePattern`yyyyMMdd`) `` is `20261020`, and
`Date.parse_with` reads such text back. A fixed pattern holds numbers only.
A month's name belongs to a language, so the compiler refuses one in a
pattern and points you to a skeleton:

```console
error: typed literal DatePattern`dd MMM yyyy` is invalid: invalid format: `MMM` writes the month's name, which is a word of a language: a fixed pattern writes numbers, the same in every locale; write it for a reader with a skeleton and a locale, as Date.format_fields(date, locale, DateFields`yMMMd`)
```

## Durations and relative time

`TimeUnit.format_relative` writes a time relative to now: "in 3 days", or
"yesterday" when the language has a word for it. It takes the count, the
locale and the unit. `Duration.format` writes a length of time, such as how
long delivery takes.

```nomi-run
import {
    std/calendar.TimeUnit
    std/duration.Duration
    std/io
    std/locale.Locale
}

fn main() {
    [-1, 3, -3]
    |> Iter.each(fn n -> {
        en = TimeUnit.format_relative(n, Locale`en-US`, TimeUnit.Day)
        pl = TimeUnit.format_relative(n, Locale`pl`, TimeUnit.Day)
        io.print("${n}: ${en} | ${pl}")
    })

    delivery = Duration.minutes(90)
    io.print(Duration.format(delivery, Locale`en-US`))
    io.print(Duration.format(delivery, Locale`pl`))
    io.print(Duration.format(delivery, Locale`en-US`, width: .Narrow))
}

```
<!-- expect
-1: yesterday | wczoraj
3: in 3 days | za 3 dni
-3: 3 days ago | 3 dni temu
1 hour, 30 minutes
1 godzina i 30 minut
1h 30m
-->

`width:` takes a [`DurationWidth`](/reference/duration/#enum-durationwidth):
`.Long` (the default), `.Short` or `.Narrow` (`1h 30m`).

## Lists

The page shows who else is looking at an item. `String.join_list` joins the
names the way the reader's language joins them:

```nomi-run
import {
    std/io
    std/locale.Locale
}

fn main() {
    watching = ["Anna", "Ben", "Chloé"]

    [Locale`en-US`, Locale`en-GB`, Locale`pl`]
    |> Iter.each(fn locale -> {
        io.print("${locale}: ${String.join_list(watching, locale)}")
    })

    dbg String.join_list(["Anna", "Ben"], Locale`en-US`)
    dbg String.join_list(["S", "M", "L"], Locale`en-US`, style: .Or)
    dbg String.join_list(["S", "M", "L"], Locale`pl`, style: .Or)
    dbg String.join_list(["2 lb", "3 oz"], Locale`en-US`, style: .Unit)
    Unit
}

```
<!-- expect
en-US: Anna, Ben, and Chloé
en-GB: Anna, Ben and Chloé
pl: Anna, Ben i Chloé
dbg line 14: String.join_list(["Anna", "Ben"], Locale`en-US`) = "Anna and Ben"
dbg line 15: String.join_list(["S", "M", "L"], Locale`en-US`, style: .Or) = "S, M, or L"
dbg line 16: String.join_list(["S", "M", "L"], Locale`pl`, style: .Or) = "S, M lub L"
dbg line 17: String.join_list(["2 lb", "3 oz"], Locale`en-US`, style: .Unit) = "2 lb, 3 oz"
-->

American English puts a comma before "and", and British English doesn't.
Polish uses "i" for "and" and "lub" for "or".

The `style` argument
([`ListStyle`](/reference/strings/#enum-liststyle)) says what the list means:

- `And`, the default: all of the items.
- `Or`: one of the items.
- `Unit`: the parts of one amount, such as a weight.

The `width` argument ([`ListWidth`](/reference/strings/#enum-listwidth))
chooses `Wide` (the default), `Short` or `Narrow`. To join numbers, format
each number first, then join the strings.

## Reading what people type

When a reader types a quantity, a price or a date, read it with `parse_in`
and the same locale you used to show it to them. The locale matters:
`1.200` means twelve hundred in German but one point two in English.

```nomi-run
import {
    std/io
    std/locale.Locale
}

fn main() {
    dbg Int.parse_in("1,200", Locale`en-US`)
    dbg Int.parse_in("1.200", Locale`de`)
    dbg Int.parse_in("1200", Locale`de`)
    dbg Int.parse_in("1.200", Locale`en-US`)
    dbg Decimal.parse_in("49,90", Locale`de`)
    dbg Int.parse_in(" 3 ", Locale`en-US`)
    dbg Int.parse_in(" 3 ", Locale`en-US`, lenient: True)

    case Int.parse_in("2.5", Locale`en-US`) {
        Ok(n) -> io.print("quantity ${n}")
        Err(e) -> io.print("not a quantity: ${e}")
    }
}

```
<!-- expect
dbg line 7: Int.parse_in("1,200", Locale`en-US`) = Ok(1200)
dbg line 8: Int.parse_in("1.200", Locale`de`) = Ok(1200)
dbg line 9: Int.parse_in("1200", Locale`de`) = Ok(1200)
dbg line 10: Int.parse_in("1.200", Locale`en-US`) = Err(#Unexpected{at: 1, text: "."})
dbg line 11: Decimal.parse_in("49,90", Locale`de`) = Ok(49.90d)
dbg line 12: Int.parse_in(" 3 ", Locale`en-US`) = Err(#Unexpected{at: 0, text: " "})
dbg line 13: Int.parse_in(" 3 ", Locale`en-US`, lenient: True) = Ok(3)
not a quantity: unexpected `.` at 1
-->

By default `parse_in` is strict: it reads what `format` writes, so `" 3 "`
with its spaces is an error, though `1200` without its separator reads. An
error is a
[`NumberParseError`](/reference/localize/#enum-numberparseerror), which says
what went wrong and where. `at` counts characters from the start of the text.

`lenient: True` accepts more: separators in any position, spaces around the
number, a leading `+`, and a negative number written as `(12)` or `12-`.
[`Int.parse_in`](/reference/int/#intparse_in) in the reference lists what
each way takes. `Decimal.parse_in` keeps the digits the reader typed, so
`49,90` becomes `49.90d`, and
`` Decimal.parse_percent_in("15 %", Locale`de`) `` is `Ok(0.15d)`.

`Money.parse_in` reads a price in a currency you name, and gives the same
errors. `Date.parse_in` reads a date written in a locale's style, `.Short`
unless you pass another:

```nomi-run
import {
    std/calendar.Date
    std/currency.Currency
    std/locale.Locale
    std/money.Money
}

fn main() {
    usd = Currency`USD`
    us = Locale`en-US`

    dbg Money.parse_in("$1,299.50", us, usd)
    dbg Money.parse_in("1.299,50 $", Locale`de`, usd)
    dbg Money.parse_in("1299.50", us, usd)
    dbg Money.parse_in("1299.50", us, usd, lenient: True)
    dbg Money.parse_in("€12.50", us, usd)
    dbg Money.parse_in("$12.505", us, usd)

    dbg Date.parse_in("10/20/26", us)
    dbg Date.parse_in("20.10.26", Locale`de`)
    dbg Date.parse_in("10/20/2026", us)
    dbg Date.parse_in("10/20/2026", us, lenient: True)
    dbg Date.parse_in("2/31/26", us)
    dbg Date.parse_in("Oct 20, 2026", us, style: .Medium)
    Unit
}

```
<!-- expect
dbg line 12: Money.parse_in("$1,299.50", us, usd) = Ok(#Money{amount: 1299.50d, currency: Currency`USD`})
dbg line 13: Money.parse_in("1.299,50 $", Locale`de`, usd) = Ok(#Money{amount: 1299.50d, currency: Currency`USD`})
dbg line 14: Money.parse_in("1299.50", us, usd) = Err(#Unexpected{at: 0, text: "1"})
dbg line 15: Money.parse_in("1299.50", us, usd, lenient: True) = Ok(#Money{amount: 1299.50d, currency: Currency`USD`})
dbg line 16: Money.parse_in("€12.50", us, usd) = Err(#WrongCurrency{expected: Currency`USD`, found: "€"})
dbg line 17: Money.parse_in("$12.505", us, usd) = Err(#TooManyFractionDigits{max: 2})
dbg line 19: Date.parse_in("10/20/26", us) = Ok(2026-10-20)
dbg line 20: Date.parse_in("20.10.26", Locale`de`) = Ok(2026-10-20)
dbg line 21: Date.parse_in("10/20/2026", us) = Err(InvalidFormat("unexpected `2` at 8 after the date"))
dbg line 22: Date.parse_in("10/20/2026", us, lenient: True) = Ok(2026-10-20)
dbg line 23: Date.parse_in("2/31/26", us) = Err(InvalidValue("month 2 of 2026 has no day 31"))
dbg line 24: Date.parse_in("Oct 20, 2026", us, style: .Medium) = Ok(2026-10-20)
-->

By default a price must have the currency's symbol or code, written where
the locale writes it. `lenient: True` also accepts the amount alone. A price
in another currency is an error, and so is a price with more digits than the
currency has.

A strict date may leave out a leading zero, as in `2/3/26`. `lenient: True`
also accepts a four-digit year where the locale writes two, a month name in
any width or case, and any spaces or punctuation between the fields. A date
that doesn't exist, such as February 31, is an error even when it is written
correctly. [`Money.parse_in`](/reference/money/#moneyparse_in) and
[`Date.parse_in`](/reference/calendar/#dateparse_in) in the reference list
the rest.

## The reader's locale

The examples so far wrote each locale in the program. A real program learns
the reader's locale while it runs.

### Reading a locale from text

When the locale comes from outside, such as a web address or a setting, use
`Locale.parse`. The `LANG` environment variable on Linux and macOS uses a
different spelling, such as `pl_PL.UTF-8`, which `Locale.from_posix` reads.

```nomi-run
import std/locale.Locale

fn main() {
    dbg Locale.parse("EN-us")
    dbg Locale.parse("pt_BR")
    dbg Locale.from_posix("pl_PL.UTF-8")
    dbg Locale.fallbacks(Locale`es-MX`)
    Unit
}

```
<!-- expect
dbg line 4: Locale.parse("EN-us") = Ok(Locale`en-US`)
dbg line 5: Locale.parse("pt_BR") = Err(#Underscore{suggestion: "pt-BR"})
dbg line 6: Locale.from_posix("pl_PL.UTF-8") = Ok(Locale`pl-PL`)
dbg line 7: Locale.fallbacks(Locale`es-MX`) = [Locale`es-MX`, Locale`es-419`, Locale`es`]
-->

`Locale.parse` fixes the capital letters, so `EN-us` becomes `en-US`.
`pt_BR`, with an underscore, is the `LANG` spelling, so `Locale.parse` turns
it down and suggests `pt-BR`. `Locale.from_posix` accepts it and ignores the
`.UTF-8` at the end.

When Nomi has no rules for a locale, it uses the nearest one that it has.
`Locale.fallbacks` shows the order it tries. Mexican Spanish (`es-MX`) falls
back to Latin American Spanish (`es-419`), then to Spanish (`es`).

The compiler checks a backtick literal, as
[Typed Literals](/typed-literals/#backtick-literals-are-checked-at-compile-time)
explains. ``Locale`pt_BR` `` gives this error:

```console
error: typed literal Locale`pt_BR` is invalid: a locale tag separates subtags with `-`, not `_`: write `pt-BR` (Locale.from_posix reads a POSIX value)
```

### Picking the reader's locale

A browser tells each website which languages its reader prefers, best first.
The shop has pages in only a few languages. `Locale.negotiate` picks the one
that suits the reader best, or a default when none comes close:

```nomi-run
import std/locale.Locale

once shop = [Locale`en`, Locale`de`, Locale`pl`, Locale`pt`]

fn page_locale(wanted: List<Locale>) -> Locale {
    Locale.negotiate(wanted, shop, Locale`en`)
}

fn main() {
    dbg page_locale([Locale`pt-BR`, Locale`en-US`])
    dbg page_locale([Locale`de-AT`])
    dbg page_locale([Locale`ja`, Locale`pl`])
    dbg page_locale([Locale`ja`])
    Unit
}

```
<!-- expect
dbg line 10: page_locale([Locale`pt-BR`, Locale`en-US`]) = Locale`pt`
dbg line 11: page_locale([Locale`de-AT`]) = Locale`de`
dbg line 12: page_locale([Locale`ja`, Locale`pl`]) = Locale`pl`
dbg line 13: page_locale([Locale`ja`]) = Locale`en`
-->

A reader from Brazil gets Portuguese, and a reader from Austria gets German.
The third reader wants Japanese first. The shop has no Japanese, so they get
their second choice, Polish. The last reader wants only Japanese and gets the
default, English.

### Where the locale lives

A program usually keeps the reader's locale in an
[app field](/capabilities-and-context/). You can call the field anything;
`locale` is the usual name. `boot` sets a default. Code that handles one
request replaces it with a `with` line, which lasts until the request is
done. The functions that format text don't read the field themselves, so
pass it to them:

```nomi-run
import {
    std/io
    std/locale.Locale
}

struct App {
    locale: Locale
}

struct Request {
    path: String
    accept: List<Locale>
}

once shop = [Locale`en`, Locale`de`, Locale`pl`]

fn boot() -> Result<App, String> {
    Ok(#App{locale: Locale`en`})
}

fn main() {
    [
        #Request{path: "/cart", accept: [Locale`de-AT`, Locale`en`]},
        #Request{path: "/cart", accept: [Locale`ja`]},
    ]
    |> Iter.each(handle)
}

fn handle(request: Request) {
    reader = Locale.negotiate(request.accept, shop, App.locale)
    with App.locale = reader

    total = cart_total(1299.50d)
    io.print("${request.path} in ${App.locale}: ${total}")
}

fn cart_total(total: Decimal) -> String {
    Decimal.format(total, App.locale)
}

```
<!-- expect
/cart in de: 1.299,50
/cart in en: 1,299.50
-->

`accept` stands in for the list of languages a browser sends. In a real
program, `boot` would take the default from the `LANG` environment variable
with `Locale.from_posix`. A library can't see your app fields, so a library
function that writes text for people should take a `Locale` argument.

## Which locales a program carries

List the locales your program supports in `nomi.toml`'s `[i18n]` section,
which [Translating Text](/translating-text/#the-i18n-settings) covers in
full:

```toml
# nomi.toml
[i18n]
locales = ["en", "de", "pl"]
```

The program then carries the CLDR data for those locales only, and
`nomi build` keeps only the parts of it the program uses. `Locale.available()`
returns the listed locales, in the order you wrote them. A program without
an `[i18n]` section carries every locale CLDR has. That includes a single
script outside any project, and the examples on this page:

```nomi-run
import std/locale.Locale

fn main() {
    dbg Iter.contains(Locale.available(), Locale`pl`)
    dbg Iter.count(Locale.available())
    Unit
}

```
<!-- expect
dbg line 4: Iter.contains(Locale.available(), Locale`pl`) = True
dbg line 5: Iter.count(Locale.available()) = 765
-->

A locale you didn't list still formats, as its nearest listed parent. When
no parent is listed, it formats in the **source locale**, the language the
program is written in, which is English unless `[i18n]` says otherwise (or
in CLDR's neutral root data when the source locale isn't listed). For
example, with `en` and `de` listed, `de-CH` (Swiss German) is formatted as
German, and `fr` as English, so `Date.format` at `.Long` writes
`October 20, 2026`. A locale read from a reader's settings falls back this
way while the program runs.

A locale you write as a literal is checked against the list. With `en` and
`de` listed, ``Locale`fr` `` is an error, since the program would never
format in it:

```console
error: typed literal Locale`fr`: `fr` is not one of this program's locales (`en`, `de` in nomi.toml's [i18n]): add it there, or format in one of them
```

The [`std/locale` reference](/reference/locale/) lists how much each kind
of locale data adds to a built program.

## Sorting and case

The shop's staff page lists customers by name. `Iter.sort` puts strings in
the order of their bytes, which is wrong for every language: every name that
starts with an accented letter comes after `Z`. Each language also has its own
alphabet. German puts `Ä` with `A`, Swedish puts `Å`, `Ä` and `Ö` after
`Z`, and Polish puts `Ś` after `S` and `Ż` after `Z`.

A [`Collator`](/reference/collation/) compares strings the way readers of
one language order them. Make one for the locale, then sort with its
`compare`:

```nomi-run
import {
    std/collation.Collator
    std/io
    std/locale.Locale
}

fn main() {
    customers = ["Zorn", "Żak", "Śliwa", "Sowa", "Öberg", "Abel", "Åberg", "Ärlig"]

    [Locale`de`, Locale`sv`, Locale`pl`]
    |> Iter.each(fn locale -> {
        by_name = Collator.new(locale)
        sorted = Iter.sort_with(customers, &Collator.compare(by_name, &1, &2))
        io.print("${locale}: ${String.join(sorted, ", ")}")
    })

    io.print("bytes: ${String.join(Iter.sort(customers), ", ")}")
}

```
<!-- expect
de: Abel, Åberg, Ärlig, Öberg, Śliwa, Sowa, Żak, Zorn
sv: Abel, Śliwa, Sowa, Żak, Zorn, Åberg, Ärlig, Öberg
pl: Abel, Åberg, Ärlig, Öberg, Sowa, Śliwa, Zorn, Żak
bytes: Abel, Sowa, Zorn, Ärlig, Åberg, Öberg, Śliwa, Żak
-->

In German, `Śliwa` sorts as if it were `Sliwa`, so it comes before
`Sowa`. In Polish, `Ś` is a letter of its own after `S`, so `Śliwa` comes
after `Sowa`. Make a collator once and use it for the whole sort.

`Collator.new` takes options, such as `numeric: True`, which puts
`order-9` before `order-10`. The
[`Collator.new` reference](/reference/collation/#collatornew) lists them,
and the [`Collator` reference](/reference/collation/#type-collator) shows
how a locale's tag picks another order of a language, such as the German
phone book's. To sort many strings, or to keep an order in a database,
compute each string's `Collator.sort_key` once and sort by the keys.

Upper and lower case depend on the language too. `String.uppercase` and
`String.lowercase` follow one rule for every language. Turkish has a dotted
`İ` and a dotless `ı`, so `String.uppercase_in` and `String.lowercase_in`
take the locale. To match text without regard to case, compare the
results of `String.fold_case`:

```nomi-run
import std/locale.Locale

fn main() {
    dbg String.uppercase("istanbul")
    dbg String.uppercase_in("istanbul", Locale`tr`)
    dbg String.lowercase_in("KIRMIZI", Locale`tr`)
    dbg String.fold_case("Straße") == String.fold_case("STRASSE")
    Unit
}

```
<!-- expect
dbg line 4: String.uppercase("istanbul") = "ISTANBUL"
dbg line 5: String.uppercase_in("istanbul", Locale`tr`) = "İSTANBUL"
dbg line 6: String.lowercase_in("KIRMIZI", Locale`tr`) = "kırmızı"
dbg line 7: String.fold_case("Straße") == String.fold_case("STRASSE") = True
-->

## `Localize`

A type can describe itself in two ways. [`Display`](/reference/display/)
gives text that is the same for everyone, used by `"${...}"` and in logs.
[`Localize`](/reference/localize/#interface-localize) gives text for a
reader in a given locale. Its one function is `localize(value, locale)`.
`Int`, `Float`, `Decimal`, `Money`, `Duration` and the calendar types
implement it with their `format` function, and a `String` gives itself
back. Give your own type both when it is shown to programs and to people:

```nomi-run
import {
    std/io
    std/locale.Locale
    std/localize.Localize
}

struct Weight {
    grams: Int
}

impl Display for Weight {
    fn to_string(w: Weight) -> String {
        "${w.grams}g"
    }
}

impl Localize for Weight {
    fn localize(w: Weight, locale: Locale) -> String {
        kilograms = Int.to_float(w.grams) / 1000.0
        "${Float.format(kilograms, locale, max_fraction: 2)} kg"
    }
}

fn main() {
    parcel = #Weight{grams: 1250}

    io.print("order-1042,${parcel}")
    io.print("en-US: ships at ${Localize.localize(parcel, Locale`en-US`)}")
    io.print("de: ships at ${Localize.localize(parcel, Locale`de`)}")
}

```
<!-- expect
order-1042,1250g
en-US: ships at 1.25 kg
de: ships at 1,25 kg
-->

The first line is a CSV row for another program, so it uses `Display`. The
other two are for people, so they use `Localize` with each reader's locale.
The [next chapter](/translating-text/) uses `Localize` to put values into
translated sentences.
