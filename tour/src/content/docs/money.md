---
title: "Money"
description: "Amounts of money with std/money: writing them, adding them, rounding them, custom currencies, and reading prices people type."
---

[`std/money`](/reference/money/) has `Money`, an amount in a currency. The
amount is a `Decimal`, so it is exact. The currency is a
[`Currency`](/reference/currency/) from `std/currency`.

## Writing an amount

`` Money`19.99 USD` `` is an amount of US dollars: the amount, one space and
the currency's ISO 4217 code. Interpolating a `Money` writes the same text.
Both fields are public, so when the amount comes from a variable, build the
value from its fields with `#Money{...}`:

```nomi-run
import {
    std/currency.Currency
    std/io
    std/money.Money
}

fn main() {
    price = Money`19.99 USD`
    io.print("${price}")
    dbg price.amount
    dbg price.currency

    cents = 1999
    amount = cents |> Decimal.from_int() |> Decimal.divide(100d, 2, .HalfEven)
    dbg #Money{amount, currency: Currency`USD`} == price
}

```
<!-- expect
19.99 USD
dbg line 10: price.amount = 19.99d
dbg line 11: price.currency = Currency`USD`
dbg line 15: #Money{amount, currency: Currency`USD`} == price = True
-->

The compiler checks a `Money` literal, so a missing currency, an unknown
code or a malformed amount is an error before the program runs:

```nomi-run error
import std/money.Money

fn main() {
    price = Money`19.99`
    dbg price
}

```
<!-- expect
main.nomi:4:13: typed literal Money`19.99` is invalid: `19.99` has no currency: write the amount, one space and the currency, as Money`19.99 EUR`
-->

The amount keeps every digit you write, as a `Decimal` does.
`` Money`12.5 USD` `` and `` Money`12.50 USD` `` are equal, but only the
second has two digits after the point.

## Adding amounts

`+` and `-` work on two amounts in one currency, `*` multiplies an amount
by an `Int`, and `Iter.sum` adds a list of them. Nothing is lost to
rounding:

```nomi-run
import {
    std/io
    std/money.Money
}

fn main() {
    book = Money`12.50 USD`
    pen = Money`2.35 USD`

    io.print("${book + pen}")
    io.print("${book - pen}")
    io.print("${book * 3}")
    io.print("${Iter.sum([book, pen, pen])}")

    empty: List<Money> = []
    io.print("${Iter.sum(empty)}")
}

```
<!-- expect
14.85 USD
10.15 USD
37.50 USD
17.20 USD
0 XXX
-->

The sum of no amounts is zero in `XXX`, ISO's code for no currency. `+`
leaves the other amount alone when one of them is that zero, so the sum of
a cart's prices is in the cart's currency.

## Mixing currencies

Adding dollars to euros gives a number with no meaning, so it stops the
program, as an `Int` overflow does. The message names both currencies:

```nomi-run error
import {
    std/io
    std/money.Money
}

fn main() {
    price = Money`12.50 USD`
    fee = Money`2.00 EUR`
    total = price + fee
    io.print("${total}")
}

```
<!-- expect
line 9: Money.add: the amounts are in two currencies, USD and EUR
-->

Convert one amount first, with a rate your program gets from somewhere it
trusts.

## Rounding

`Money` keeps every digit until you round it. A third of $10.00 is
$3.333333 to six places, and it stays that way through more arithmetic.
`Money.round` rounds an amount to its currency's digits: two for the
dollar, none for the yen.

```nomi-run
import {
    std/io
    std/money.Money
}

fn main() {
    total = Money`10.00 USD`
    third = #{..total, amount: total.amount |> Decimal.divide(3d, 6, .HalfEven)}
    io.print("${third}")
    io.print("${Money.round(third)}")

    io.print("${Money.round(Money`0.125 USD`)}")
    io.print("${Money.round(Money`0.135 USD`)}")
    io.print("${Money.round(Money`0.125 USD`, mode: .HalfUp)}")
    io.print("${Money.round(Money`1234.5 JPY`)}")
}

```
<!-- expect
3.333333 USD
3.33 USD
0.12 USD
0.14 USD
0.13 USD
1234 JPY
-->

A tie, an amount exactly halfway like `0.125`, rounds to the even digit by
default: `0.12`, and `0.135` to `0.14`. Rounding every tie up would make a
long list of rounded amounts add up to a little more than the exact total.
Rounding to even sends half the ties down and half up, so the errors cancel
out. Pass `mode:` for another rule, such as `.HalfUp`, when a law or a
contract asks for it.

## Custom currencies

A shop may count in something that isn't money in a bank, such as loyalty
points. A custom currency has its own code and says how many digits it has
after the point: `PTS:0` has none. A module lists the custom currencies it
writes in its `nomi.toml`, in the `[money]` section, and then writes them in
`Currency` and `Money` literals the same way. A currency read while the
program runs, with `Currency.custom` or `Currency.parse`, needs no listing.

This example is a small project of two files; each tab is one file:

```nomi-run
// FILE: main.nomi
import {
    std/currency.Currency
    std/io
    std/locale.Locale
    std/money.Money
}

fn main() {
    points = Money`1200 PTS:0` + Money`300 PTS:0`
    io.print("${points}")
    io.print(Money.format(points, Locale`en-US`))
    dbg Currency.parse("PTS:0") == Ok(points.currency)
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[money]
custom_currencies = ["PTS:0"]
```
<!-- expect
1500 PTS:0
PTS 1,500
dbg line 12: Currency.parse("PTS:0") == Ok(points.currency) = True
-->

Two custom currencies are equal when their codes and digits are, so two
libraries that each write `PTS:0` share one currency. `PTS:0` and `PTS:2`
are two currencies, and adding them stops the program. A custom currency
can't reuse an ISO code, and has no symbol, so `Money.format` writes its
code.

A custom currency needs no other setup, so the list is what catches a typo.
A literal naming a custom currency the module doesn't list is an error when
the program is checked, and it suggests a listed one that is close. With
`PTS:0` listed, ``Money`5 PST:0` `` is:

```console
error: typed literal Money`5 PST:0`: `PST:0` is a custom currency this module does not list: add it to [money].custom_currencies in nomi.toml
 --> main.nomi:7:17
  |
7 |     io.print("${Money`5 PST:0`}")
  |                 ^^^^^^^^^^^^
  = help: did you mean `PTS:0`? nomi.toml lists it
```

`Currency.custom("PST", 0)` with both arguments written out is checked the
same way, and the editor suggests the literal there.

Each module has its own list: a library lists the currencies it writes, and
the application's list doesn't cover them. A single file with no
`nomi.toml`, like the other examples on this page, may write any custom
currency.

## Reading what people type

`Money.parse_in` reads a price a person typed, written the way their locale
writes prices, in the currency you name. By default the price must have the
currency's symbol or code where the locale puts it. `lenient: True` also
takes the amount alone.

```nomi-run
import {
    std/currency.Currency
    std/locale.Locale
    std/money.Money
}

fn main() {
    usd = Currency`USD`
    dbg Money.parse_in("$1,299.50", Locale`en-US`, usd)
    dbg Money.parse_in("1299.50", Locale`en-US`, usd, lenient: True)
    dbg Money.parse_in("€12.50", Locale`en-US`, usd)
    Unit
}

```
<!-- expect
dbg line 9: Money.parse_in("$1,299.50", Locale`en-US`, usd) = Ok(#Money{amount: 1299.50d, currency: Currency`USD`})
dbg line 10: Money.parse_in("1299.50", Locale`en-US`, usd, lenient: True) = Ok(#Money{amount: 1299.50d, currency: Currency`USD`})
dbg line 11: Money.parse_in("€12.50", Locale`en-US`, usd) = Err(#WrongCurrency{expected: Currency`USD`, found: "€"})
-->

A price in another currency is an error, and so is one with more digits
than the currency has: `$12.505` is `Err(#TooManyFractionDigits{max: 2})`. [Formatting for a
Reader](/locales-and-formatting/#reading-what-people-type) covers how each
locale writes numbers, and the rest of what strict and lenient reading
accept.

## Room for libraries

`Money` holds only an amount and a currency, with both fields public, so
payment, invoicing and accounting libraries can share one type. Std has no
exchange rates. A library that has them takes a `Money`, checks its
`currency`, and returns a new `Money` in the other currency, rounded with
`Money.round` when it is ready to charge.
