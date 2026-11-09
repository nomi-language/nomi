---
title: "Translating Text"
description: "Declare the text people read as phrases, translate it in catalogs the compiler checks, and let each language choose its own plural forms and words."
---

A **phrase** is text for people. You write it once in the program's own
language, and each other language gets a translation in a file beside the
code. A phrase can also change its words with a number: in Polish,
"3 items" is "3 produkty" but "5 items" is "5 produktów".

A phrase has two parts:

- Its **declaration** in Nomi: a name, typed parameters, and the text in
  the **source locale**, the language the program is written in (English
  unless you say otherwise).
- Its **translations**, one per locale, in **catalogs**: one TOML file per
  locale for each Nomi file that declares phrases.

`Phrase.format` writes it in a reader's locale.

The text uses
[MessageFormat 2](https://www.unicode.org/reports/tr35/tr35-messageFormat.html),
a Unicode standard for translatable text, so translators and their tools
can read it.

## Declaring a phrase

Here is the cart badge as a phrase:

```nomi
/// The cart badge, such as "3 items in your cart".
pub phrase cart_count(count: Int) `
    .input {$count :integer}
    .match $count
    one {{{$count} item in your cart}}
    * {{{$count} items in your cart}}
    `
```

It reads like this:

- `phrase cart_count(count: Int)` declares a function, `cart_count`, that
  takes an `Int` and returns a [`Phrase`](/reference/phrase/). If the
  declaration is in `cart.nomi`, another file calls it as
  `cart.cart_count(3)`.
- The text goes in backticks. Inside it, `$count` is the parameter.
- `.input {$count :integer}` says how to read `$count`: as a whole number.
  `:integer` is a **function**, MessageFormat's word for what turns a
  value into text.
- `.match $count` chooses one of the lines below it by the number's
  [plural category](#plural-categories). Each line is a **variant** (the
  compiler calls a variant an *arm*): a **key**, such as `one`, then its
  text in `{{...}}`.
- `*` matches anything no other key matches. Every `.match` must have one.
- `{$count}` inside a variant writes the number, formatted for the
  reader's locale.

Start from `` `{$count} items in your cart` `` and the editor's refactor
**Add plural forms for `$count`** writes the `.input`, the `.match` and a
variant for each category your source language has, each holding the old
text.

## Catalogs

The Polish translation of `cart.nomi`'s phrases goes in
`locales/pl/cart.toml`: the catalog directory, then the locale, then the
Nomi file's own path with `.toml` at the end. Each phrase is a table named
after it, holding two strings:

- `text` is the translation.
- `source` is the English text the translation was made from. It lets the
  compiler tell when the English has changed since.

`nomi.toml`'s `[i18n]` section lists the locales the program supports.

This example runs in the browser as a small project of four files; each tab
is one file. Edit the Polish and run it again:

```nomi-run
// FILE: main.nomi
import {
    std::io
    cart
}

fn main() {
    [1, 2, 5, 22]
    |> Iter.each(fn n -> {
        badge = cart.cart_count(n)
        en = Phrase.format(badge, Locale`en`)
        pl = Phrase.format(badge, Locale`pl`)
        io.print("${en} | ${pl}")
    })
}

// FILE: cart.nomi
/// The cart badge, such as "3 items in your cart".
pub phrase cart_count(count: Int) `
    .input {$count :integer}
    .match $count
    one {{{$count} item in your cart}}
    * {{{$count} items in your cart}}
    `

// FILE: locales/pl/cart.toml
[cart_count]
source = '''
.input {$count :integer}
.match $count
one {{{$count} item in your cart}}
* {{{$count} items in your cart}}
'''
text = '''
.input {$count :integer}
.match $count
one {{{$count} produkt w koszyku}}
few {{{$count} produkty w koszyku}}
many {{{$count} produktów w koszyku}}
* {{{$count} produktu w koszyku}}
'''

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[i18n]
locales = ["en", "pl"]
```
<!-- expect
1 item in your cart | 1 produkt w koszyku
2 items in your cart | 2 produkty w koszyku
5 items in your cart | 5 produktów w koszyku
22 items in your cart | 22 produkty w koszyku
-->

`cart.cart_count(n)` doesn't make any text yet. It makes a `Phrase`: which
phrase, and its arguments. `Phrase.format` picks the text for the locale
and fills it in. So a function can return a `Phrase` without knowing who
will read it, and the code that knows the reader's locale formats it.
`Phrase`, `Noun` and `Locale` are in the prelude, so none needs an import.

A locale the program has no translation for gets the source locale's text.
A locale near one it has gets that one's: `pl-PL` shows the Polish.

## Plural categories

English has two forms of a noun for counting: "1 item" and "2 items". Other
languages have more. Polish has three for whole numbers, plus one for
fractions, and the form doesn't follow the size of the number: 22 takes the
same form as 2. CLDR gives each form a name, called a **plural category**, and
lists which numbers take it in each language:

| Category | English | Polish |
|---|---|---|
| `one` | 1 | 1 |
| `few` | | 2 to 4, 22 to 24, 32 to 34, ... |
| `many` | | 0, 5 to 21, 25 to 31, ... |
| `other` (`*`) | everything else | fractions, such as 1.5 |

A `.match` on a count has a variant for each category the language uses,
and `*` stands for `other`. English needs `one` and `*`. The Polish text
above needs `one`, `few` and `many` as well. Its `*` variant is for
fractions, which an `Int` never has. Other languages use `zero` and `two`
too, and some, such as Japanese, use `other` alone.

Count with an `Int` or a `Decimal`. A `Decimal` keeps the digits you wrote,
and they change the category: English says "1 item" but "1.0 items". A
`Float` doesn't keep track of its digits, so it can't be used as a count.
Turn it into a `Decimal` first.

[`PluralCategory.cardinal`](/reference/plural/#pluralcategorycardinal)
gives a number's category in program code, but don't use it to choose words.
A `case` on the category holds one language's words, chosen by that
language's rules, and every new language needs another branch that only a
programmer can change. Put the words in a phrase instead, and let each
translation choose its own forms.

## What the compiler checks

The compiler reads the catalogs with the program and holds each
translation to its phrase. A Polish `cart_count` with no `few` variant
gives this error:

```console
error: pl: `cart_count` has no `few` arm; 2, 3 and 4 select `few` in Polish
  --> locales/pl/cart.toml:10:8
   |
10 | .match $count
   |        ^^^^^^
```

The error says which numbers would have used the missing category, so you
can check the fix.

The editor helps with each of these. Hovering a phrase shows its text in
every locale. In a catalog, the editor completes phrase names, parameter
names after `$`, and the keys a `.match` needs. Its quick fixes add a
missing translation (a copy of the English with every variant the locale
needs, ready to translate) and add missing variants.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A locale in `locales` with no translation of a phrase is an error.
- So is a catalog entry that names no phrase, a catalog that belongs to
  no Nomi file, and a catalog directory for a locale `[i18n]` doesn't list.
- A text may use only the phrase's parameters, and the English text must
  use every one, so a translator can see what each is for.
- A function on the wrong type, such as `:integer` on a `String`, is an
  error, and so is a plural `.match` missing a category the locale needs or
  an enum `.match` missing a variant.
- A translation made from an older English text is an error, as
  [When the English changes](#when-the-english-changes) shows.

Spec: [Phrases](/spec/typed-literals-and-keyword-declarations/#phrases),
[Nouns and agreement](/spec/typed-literals-and-keyword-declarations/#nouns-and-agreement),
[What the compiler checks](/spec/typed-literals-and-keyword-declarations/#what-the-compiler-checks).

</div>

## Lists, and phrases inside phrases

A placeholder with no function, such as `{$name}`, writes its value with
[`Localize`](/locales-and-formatting/#localize) in the phrase's locale, so
any type that implements `Localize` can be a parameter. A `Phrase`
implements `Localize` too, so one phrase can hold another.

A list of names needs more than that. `:nomi:list` takes a `List<String>`,
joins it the way the reader's language joins a list (from
[Lists](/locales-and-formatting/#lists)), and lets `.match` choose by how
many items there are. That is how the verb can agree with the number of
people.

`:nomi:list` is one of Nomi's own functions. Their names start with `nomi:`,
which is how MessageFormat marks a function a program adds, so other tools
still read the text.

The full cart page puts these together:

```nomi-run
// FILE: main.nomi
import {
    std::io
    cart
}

fn main() {
    lines = [
        cart.cart_count(3),
        cart.order_status(.Shipped),
        cart.watching(["Anna"]),
        cart.watching(["Anna", "Ben", "Chloé"]),
        cart.greeting("Anna", cart.cart_count(22)),
    ]

    [Locale`en`, Locale`pl`]
    |> Iter.each(fn locale -> {
        lines |> Iter.each(&io.print(Phrase.format(&1, locale)))
    })
}

// FILE: cart.nomi
/// Where an order is.
pub enum Status {
    Pending
    Shipped
    Delivered
}

/// The cart badge, such as "3 items in your cart".
pub phrase cart_count(count: Int) `
    .input {$count :integer}
    .match $count
    one {{{$count} item in your cart}}
    * {{{$count} items in your cart}}
    `

/// Where the customer's order is.
pub phrase order_status(status: Status) `
    .input {$status :string}
    .match $status
    Pending {{Your order is waiting to ship}}
    Shipped {{Your order is on its way}}
    Delivered {{Your order was delivered}}
    * {{Your order is on its way}}
    `

/// Other shoppers looking at the same item.
pub phrase watching(people: List<String>) `
    .input {$people :nomi:list}
    .match $people
    one {{{$people} is watching this item}}
    * {{{$people} are watching this item}}
    `

/// A greeting before the cart badge.
pub phrase greeting(name: String, cart: Phrase) `{$name}, you have {$cart}`

// FILE: locales/pl/cart.toml
[cart_count]
source = '''
.input {$count :integer}
.match $count
one {{{$count} item in your cart}}
* {{{$count} items in your cart}}
'''
text = '''
.input {$count :integer}
.match $count
one {{{$count} produkt w koszyku}}
few {{{$count} produkty w koszyku}}
many {{{$count} produktów w koszyku}}
* {{{$count} produktu w koszyku}}
'''

[order_status]
source = '''
.input {$status :string}
.match $status
Pending {{Your order is waiting to ship}}
Shipped {{Your order is on its way}}
Delivered {{Your order was delivered}}
* {{Your order is on its way}}
'''
text = '''
.input {$status :string}
.match $status
Pending {{Zamówienie czeka na wysyłkę}}
Shipped {{Zamówienie zostało wysłane}}
Delivered {{Zamówienie zostało dostarczone}}
* {{Zamówienie zostało wysłane}}
'''

[watching]
source = '''
.input {$people :nomi:list}
.match $people
one {{{$people} is watching this item}}
* {{{$people} are watching this item}}
'''
text = '''
.input {$people :nomi:list}
.match $people
one {{{$people} ogląda ten produkt}}
few {{{$people} oglądają ten produkt}}
many {{{$people} oglądają ten produkt}}
* {{{$people} oglądają ten produkt}}
'''

[greeting]
source = '''{$name}, you have {$cart}'''
text = '''{$name}, masz {$cart}'''

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[i18n]
locales = ["en", "pl"]
```
<!-- expect
3 items in your cart
Your order is on its way
Anna is watching this item
Anna, Ben, and Chloé are watching this item
Anna, you have 22 items in your cart
3 produkty w koszyku
Zamówienie zostało wysłane
Anna ogląda ten produkt
Anna, Ben i Chloé oglądają ten produkt
Anna, masz 22 produkty w koszyku
-->

Three of the lines need more than a plain translation:

- **The order status.** `:string` reads an enum value as the name of its
  variant, and `.match` then needs a key for every variant. It still needs
  `*`, as every `.match` does.
- **The watchers.** The names are joined in each locale's way: English puts
  a comma before "and", Polish uses "i". The verb follows the number of
  people: one person "ogląda" (is watching), more "oglądają".
- **The greeting.** The cart badge inside it is formatted in the same
  locale as the greeting, so the Polish reads "22 produkty".

## Prices and dates in phrases

A phrase takes a `Money` and the calendar types like any other value.
`{$total :currency}` writes a price as `Money.format` does, and `:date`,
`:time` and `:datetime` write dates and times. `:datetime` takes either
`dateStyle` and `timeStyle`, or the fields to show, as in
`weekday=long month=long day=numeric`.

```nomi-run
// FILE: main.nomi
import {
    std::calendar.Date
    std::io
    std::money.Money
    order
}

fn main() {
    lines = [
        order.order_total(Money`27.35 USD`),
        order.ships_on(Date`2026-10-20`),
    ]

    [Locale`en`, Locale`pl`]
    |> Iter.each(fn locale -> {
        lines |> Iter.each(&io.print(Phrase.format(&1, locale)))
    })
}

// FILE: order.nomi
import {
    std::calendar.Date
    std::money.Money
}

/// The order's total, in its currency.
pub phrase order_total(total: Money) `Total: {$total :currency}`

/// The day the order ships.
pub phrase ships_on(date: Date) `Ships {$date :datetime weekday=long month=long day=numeric}`

// FILE: locales/pl/order.toml
[order_total]
source = '''Total: {$total :currency}'''
text = '''Razem: {$total :currency}'''

[ships_on]
source = '''Ships {$date :datetime weekday=long month=long day=numeric}'''
text = '''Wysyłka: {$date :datetime weekday=long month=long day=numeric}'''

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[i18n]
locales = ["en", "pl"]
```
<!-- expect
Total: $27.35
Ships Tuesday, October 20
Razem: 27,35 USD
Wysyłka: wtorek, 20 października
-->

The compiler checks the types here as well: `:currency` on a `Decimal` is
an error, since a `Decimal` has no currency. A translation can also choose
its words by the day of the week with `:nomi:weekday`, which the
[`std::phrase` reference](/reference/phrase/) describes.

## Nouns

When a parcel ships, the shop says "Your book has shipped." In Polish the
words around "book" change with it: "Twoja książka została wysłana", but
"Twój laptop został wysłany". "Your", "has" and "shipped" all follow the
noun's **gender**: "książka" (book) is feminine and "laptop" is masculine.
English never shows this, so one English sentence becomes several Polish
ones.

A `noun` declaration names a thing a phrase can talk about. Its text is
the English word, written as a small phrase over `$count`, so it can say
"book" or "books". The catalog gives the noun's Polish `gender` and its
forms, and the translation of a phrase chooses a variant by the gender with
`:nomi:gender`:

```nomi-run
// FILE: main.nomi
import {
    std::io
    shop
}

fn main() {
    [shop.book, shop.laptop]
    |> Iter.each(fn product -> {
        shipped = shop.has_shipped(product)
        en = Phrase.format(shipped, Locale`en`)
        pl = Phrase.format(shipped, Locale`pl`)
        io.print("${en} | ${pl}")
    })
}

// FILE: shop.nomi
pub noun book `
    .input {$count :integer}
    .match $count
    one {{book}}
    * {{books}}
    `

pub noun laptop `
    .input {$count :integer}
    .match $count
    one {{laptop}}
    * {{laptops}}
    `

/// Sent when a parcel leaves the warehouse.
pub phrase has_shipped(product: Noun) `Your {$product :nomi:noun} has shipped.`

// FILE: locales/pl/shop.toml
[book]
source = '''
.input {$count :integer}
.match $count
one {{book}}
* {{books}}
'''
gender = "feminine"
nominative = '''
.input {$count :integer}
.match $count
one {{książka}}
few {{książki}}
many {{książek}}
* {{książki}}
'''

[laptop]
source = '''
.input {$count :integer}
.match $count
one {{laptop}}
* {{laptops}}
'''
gender = "inanimate"
nominative = '''
.input {$count :integer}
.match $count
one {{laptop}}
few {{laptopy}}
many {{laptopów}}
* {{laptopa}}
'''

[has_shipped]
source = '''Your {$product :nomi:noun} has shipped.'''
text = '''
.input {$product :nomi:gender}
.match $product
feminine {{Twoja {$product :nomi:noun} została wysłana.}}
inanimate {{Twój {$product :nomi:noun} został wysłany.}}
* {{Twoja przesyłka została wysłana.}}
'''

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[i18n]
locales = ["en", "pl"]
```
<!-- expect
Your book has shipped. | Twoja książka została wysłana.
Your laptop has shipped. | Twój laptop został wysłany.
-->

`{$product :nomi:noun}` writes the noun. Polish calls a thing such as a
laptop `inanimate`, one of three kinds of masculine. The `nominative` is the
noun's plain form; a phrase can ask for another form with `case=`, as in
`{$product :nomi:noun case=genitive}`, the form after "brak"
("no ... left").

The compiler holds every noun to every phrase, since a phrase can be
given any noun the program declares. Every Polish noun needs a gender and
each form some phrase asks for, and a `.match` on gender needs a variant
for every gender the program's nouns have. A product's own name read from a
database is a `String`, not a `Noun`, so a phrase writes it with a plain
`{$title}` in a sentence that doesn't need to agree with it. The
[`Noun` reference](/reference/phrase/#struct-noun) covers genders, cases
and these checks in full.

## The `[i18n]` settings

`nomi.toml`'s `[i18n]` section says which locales must translate every
phrase (`locales`), which may leave some out and show the source text
instead (`partial_locales`, for a translation still being written), the
source locale when it is not English, and where the catalogs live. English
has no catalog: its text is in the declarations.

The same lists decide which CLDR data the program carries: only that of the
locales it lists. A locale it didn't list formats as its nearest listed
parent, or else in the source locale, and a ``Locale`…` `` literal of one is
a compile error. A program with no `[i18n]` section, such as each example
in the tour, carries every locale. [`[i18n]`](/tooling/projects/#i18n) lists
every setting, and
[A program's locale data](/tooling/building/#a-programs-locale-data) what a
build carries.

## When the English changes

When the English changes, every translation of the old English may now say
the wrong thing. Say the badge changes "cart" to "bag" in its `*` variant.
The Polish entry's `source` still holds the old text, so the compiler shows
both:

```console
error: pl: `cart_count` was translated from an older English text
  was:
        .input {$count :integer}
        .match $count
        one {{{$count} item in your cart}}
        * {{{$count} items in your cart}}
  now:
        .input {$count :integer}
        .match $count
        one {{{$count} item in your cart}}
        * {{{$count} items in your bag}}
 --> locales/pl/cart.toml:2:1
  |
2 | source = '''
  | ^^^^^^^^^^^^ (through line 7)
  = help: update the translation, then accept it with the quick fix or `nomi phrases accept pl cart_count`
```

The text is compared as a phrase, not letter by letter, so changing only
spaces or line breaks makes nothing stale. Once the translator has updated
the Polish, run:

```console
$ nomi phrases accept pl cart_count
```

It records the new English as the entry's `source`, and the error goes
away. The editor's quick fix does the same.

A program can also take corrected translations while it runs. Fetch the
catalog text, from a translation service say, and read it with
[`Catalog.load`](/reference/phrase/#catalogload)`(text, locale)`. It
checks each entry as the compiler does, and returns the catalog with the
entries it rejected. Pass the catalog to `Phrase.format` in a list:
`catalogs: [catalog]`.

## Phrases in the browser

`nomi build --web` puts only the source locale, or the locales
`web_embedded` lists, in `app.wasm`, and writes each other locale's data
and translations to its own file beside it. Call
[`Locale.load`](/reference/locale/#localeload) with the reader's locale
before showing a page in it. Outside the browser every listed locale is in
the program, and `Locale.load` returns `Ok(Unit)` at once.

`web_embedded` under `[i18n]` names the locales to put in `app.wasm`
([Locales in the browser](/tooling/building/#locales-in-the-browser)).

[Files](/files/) is next. It reads and writes files.
