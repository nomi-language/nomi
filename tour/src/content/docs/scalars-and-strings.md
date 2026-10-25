---
title: Scalars & Strings
description: Integers, floats, decimals, booleans, strings, bytes and regular expressions.
---

Nomi has three number types: [`Int`](/reference/int/),
[`Float`](/reference/float/) and [`Decimal`](/reference/decimal/). The other
basic types are [`Bool`](/reference/bool/),
[`String`](/reference/strings/#type-string), and
[`Byte`](/reference/bytes/#type-byte) and
[`Bytes`](/reference/bytes/#type-bytes) for binary data.
The chapter ends with [regular expressions](#regular-expressions), which
the `String` functions take for finding, splitting and replacing text.

## Numbers, booleans, operators

```nomi-run
fn main() {
    dbg 42 + 8
    dbg 10 - 3
    dbg 2 * 21
    dbg 20 / 4
    dbg -(3 + 4)
    dbg 42 > 10
    dbg True and !False
}

```
<!-- expect
dbg line 2: 42 + 8 = 50
dbg line 3: 10 - 3 = 7
dbg line 4: 2 * 21 = 42
dbg line 5: 20 / 4 = 5
dbg line 6: -(3 + 4) = -7
dbg line 7: 42 > 10 = True
dbg line 8: True and !False = True
-->

Arithmetic and comparisons work as in most languages. The logical operators
are `and`, `or` and `!`.

## Number literal forms

```nomi-run
fn main() {
    dbg 1_000_000
    dbg 0xFF
    dbg 0b1010
    dbg 1.0e10
}

```
<!-- expect
dbg line 2: 1_000_000 = 1000000
dbg line 3: 0xFF = 255
dbg line 4: 0b1010 = 10
dbg line 5: 1.0e10 = 10000000000.0
-->

A number can use `_` to separate digits. `0x` starts a hexadecimal number,
`0b` a binary one, and `0o` an octal one. `e` writes a number in scientific
notation.

## Decimal — exact arithmetic

```nomi-run
fn main() {
    // Float carries base-2 imprecision
    dbg 0.1 + 0.2 == 0.3

    // Decimal is exact
    dbg 0.1d + 0.2d == 0.3d

    // Sum four prices to the cent — exactly
    dbg 19.99d + 5.00d + 2.50d + 0.99d
}

```
<!-- expect
dbg line 3: 0.1 + 0.2 == 0.3 = False
dbg line 6: 0.1d + 0.2d == 0.3d = True
dbg line 9: 19.99d + 5.00d + 2.50d + 0.99d = 28.48d
-->

[`Float`](/reference/float/) is fast but not exact. It stores numbers in
binary, so `0.1 + 0.2` is not quite `0.3`.

[`Decimal`](/reference/decimal/) stores numbers in base 10 and adds them
exactly. Use it when a rounding error would be a bug, as with money. A
decimal literal ends in `d`.

## Strings — interpolation and concatenation

```nomi-run
import std/io

fn main() {
    name = "Nomi"
    io.print("Hello, ${name}!")
    io.print("1 + 2 = ${1 + 2}")
    io.print("Hello" + ", " + "Nomi")
}

```
<!-- expect
Hello, Nomi!
1 + 2 = 3
Hello, Nomi
-->

`${expr}` inside a string inserts the value of `expr`. `+` joins two
strings.

Interpolation turns each value into text with
[`Display.to_string`](/reference/display/#displayto_string).
[`io.print`](/reference/io/#ioprint) accepts any value that implements
`Display`. So you rarely convert a value to a string yourself before
printing it.

`${` is the only special sequence in a string. To write a literal `${`,
write `\${`. A `$` on its own, `#` and `#{` are ordinary text:

```nomi-run
import std/io

fn main() {
    io.print("echo \${HOME} costs $5 #{not a struct}")
}

```
<!-- expect
echo ${HOME} costs $5 #{not a struct}
-->

Each arithmetic operator comes from an interface: `+` from
[`Add`](/reference/add/), `-` from [`Subtract`](/reference/subtract/), `*`
from [`Multiply`](/reference/multiply/) and `/` from
[`Divide`](/reference/divide/). The number types implement them, and
`String` implements `Add`, which is how `+` joins strings. Your own types can
implement them too, when an operator is the clearest way to write the
operation.

To split text:

- [`String.split`](/reference/strings/#stringsplit) splits at a separator.
- [`String.words`](/reference/strings/#stringwords) splits at runs of
  whitespace.
- [`String.lines`](/reference/strings/#stringlines) splits into lines.

The [`String` reference](/reference/strings/) lists the rest.

`String.split`, `String.contains?`, `String.starts_with?`,
`String.ends_with?`, `String.replace`, `String.find` and `String.find_all`
take a [`Matcher`](/reference/matcher/) for the text to look for. A `Matcher` is
either a plain `String` or a [`Regex`](/typed-literals/#backtick-literals-are-checked-at-compile-time):

```nomi-run
import std/regex.Regex

fn main() {
    text = "one, two,three"

    dbg String.split(text, ",")
    dbg text |> String.split(Regex`,\s*`)
    dbg String.contains?(text, Regex`\d`)
}
```
<!-- expect
dbg line 6: String.split(text, ",") = ["one", " two", "three"]
dbg line 7: text |> String.split(Regex`,\s*`) = ["one", "two", "three"]
dbg line 8: String.contains?(text, Regex`\d`) = False
-->

## Text model

Nomi looks at text at four levels:

- A [`String`](/reference/strings/#type-string) is text. There is no
  separate `Char` type, so a single character is a `String` too.
- A grapheme is one character as a reader sees it. `é` and many emoji are
  one grapheme even when Unicode builds them from several code points. Nomi
  represents a grapheme as a `String`.
- A [`Codepoint`](/reference/code_points/) is one Unicode code point: a
  number Unicode assigns to a character. Use it when you need the numbers
  that make up text.
- [`Bytes`](/reference/bytes/#type-bytes) is immutable binary data, and a
  [`Byte`](/reference/bytes/#type-byte) is one byte of it. Use it for UTF-8
  encoding, file and network data, and Go's `[]byte` when calling Go.

Most string functions count graphemes. [`String.length`](/reference/strings/#stringlength),
[`String.slice`](/reference/strings/#stringslice) and
[`String.reverse`](/reference/strings/#stringreverse) work in characters as
a reader sees them, not in bytes. Iterating over a `String` also gives its
graphemes, so `Iter.to_list("café")` is a list of four strings.

[`String.to_codepoints`](/reference/strings/#stringto_codepoints) gives the
code points instead:

```nomi-run
fn main() {
    dbg String.length("café")
    dbg Iter.to_list("café")

    "café"
    |> String.to_codepoints()
    |> Iter.map(Codepoint.to_int)
    |> Iter.to_list()
    |> dbg
}

```
<!-- expect
dbg line 2: String.length("café") = 4
dbg line 3: Iter.to_list("café") = ["c", "a", "f", "é"]
dbg line 9:
  "café"
  |> String.to_codepoints()
  |> Iter.map(Codepoint.to_int)
  |> Iter.to_list()
  = [99, 97, 102, 233]
-->

An ASCII character in single quotes is a `Codepoint` literal: `'a'`. Escapes
work too: `'\n'`, `'\''`, `'\u{7F}'`. It is a `Codepoint`, not a string, so
`"a" == 'a'` is a type error.

Use codepoint literals for work such as scanning and matching characters,
and keep text in strings. They are limited to ASCII because only there are
the character, its code point and its UTF-8 byte the same thing. `'é'` is an
error; write `"é"` as a string.

```nomi-run
fn kind(cp: Codepoint) -> String {
    case cp {
        ',' -> "comma"
        ' ' -> "space"
        _ -> Codepoint.to_string(cp)
    }
}

fn main() {
    "a, b"
    |> String.to_codepoints()
    |> Iter.map(kind)
    |> Iter.to_list()
    |> dbg
}

```
<!-- expect
dbg line 14:
  "a, b"
  |> String.to_codepoints()
  |> Iter.map(kind)
  |> Iter.to_list()
  = ["a", "comma", "space", "b"]
-->

To classify a codepoint, ask `Codepoint`:
[`letter?`](/reference/code_points/#codepointletter),
[`whitespace?`](/reference/code_points/#codepointwhitespace),
[`upper?`](/reference/code_points/#codepointupper) and
[`lower?`](/reference/code_points/#codepointlower) follow Unicode, so `é` and
`ж` are letters. [`digit?`](/reference/code_points/#codepointdigit) and
[`to_digit`](/reference/code_points/#codepointto_digit) accept only ASCII `0`
to `9`, which is what a phone number or a checksum means.

```nomi-run
fn main() {
    text = "Tél 555-01"

    text
    |> String.to_codepoints()
    |> Iter.filter(Codepoint.letter?)
    |> Iter.map(Codepoint.to_string)
    |> String.join()
    |> dbg

    text
    |> String.to_codepoints()
    |> Iter.map(Codepoint.to_digit)
    |> Maybe.values()
    |> dbg
}

```
<!-- expect
dbg line 9:
  text
  |> String.to_codepoints()
  |> Iter.filter(Codepoint.letter?)
  |> Iter.map(Codepoint.to_string)
  |> String.join()
  = "Tél"
dbg line 15:
  text
  |> String.to_codepoints()
  |> Iter.map(Codepoint.to_digit)
  |> Maybe.values()
  = [5, 5, 5, 0, 1]
-->

`==` compares strings byte by byte. The same visible text can be stored in
more than one Unicode form, and two such strings are not equal. Before
comparing text from different sources, put both in the same form with
[`String.normalize`](/reference/strings/#stringnormalize).

## Bytes — binary data

```nomi-run
import std/io

fn main() {
    raw = String.to_bytes("café")
    dbg Bytes.length(raw)
    dbg raw |> Iter.map(Byte.to_int) |> Iter.to_list()

    prefix = String.to_bytes("go:")

    case Bytes.to_string(prefix + raw) {
        Ok(text) -> io.print(text)
        Err(reason) -> io.print(reason)
    }
}
```
<!-- expect
dbg line 5: Bytes.length(raw) = 5
dbg line 6: raw |> Iter.map(Byte.to_int) |> Iter.to_list() = [99, 97, 102, 195, 169]
go:café
-->

- [`String.to_bytes`](/reference/strings/#stringto_bytes) encodes text as
  UTF-8. `é` takes two bytes, so `café` is 5 bytes.
- `Bytes` can be iterated, so the `Iter` functions work on it.
- [`Byte.to_int`](/reference/bytes/#byteto_int) turns a `Byte` into an
  `Int`.
- `+` joins two `Bytes` values, as
  [`Bytes.concat`](/reference/bytes/#bytesconcat) does.
- `Bytes.to_string` decodes UTF-8 and returns a `Result`, since the bytes
  may not be valid UTF-8.

A `Byte` is not a number, so you can't do arithmetic on it. Convert with
[`Byte.from_int`](/reference/bytes/#bytefrom_int) and `Byte.to_int` when you
need the number.

## Triple-quoted strings

```nomi-run
import std/io

fn main() {
    name = "Alice"

    query = """
        SELECT *
        FROM users
        WHERE name = '${name}'
        """

    io.print(query)
}

```
<!-- expect
SELECT *
FROM users
WHERE name = 'Alice'
-->

A string in `"""` quotes can span several lines. Interpolation works as
usual. The indentation that every line shares is removed.

## Raw strings

```nomi-run
import std/io

fn main() {
    pattern = `\d{3,4}`
    template = `price: ${PRICE}`

    query = `
        SELECT *
        FROM invoices
        WHERE total > ${MIN_TOTAL}
        `

    io.print(pattern)
    io.print(template)
    io.print(query)
}

```
<!-- expect
\d{3,4}
price: ${PRICE}
SELECT *
FROM invoices
WHERE total > ${MIN_TOTAL}
-->

A string in backticks is raw. Backslashes and `${...}` are kept as written.
Use it for text that contains those characters. A raw string can span
several lines, and its shared indentation is removed the same way.

## Regular expressions

[`std/regex`](/reference/regex/) has `Regex`. Write a pattern in backticks,
as ``Regex`\d+` ``, so its backslashes stay as written. The compiler checks
the pattern, so ``Regex`[` `` is an error before the program runs
([Typed Literals](/typed-literals/#backtick-literals-are-checked-at-compile-time) explains how).

A `Regex` is a [`Matcher`](/reference/matcher/), so the `String` functions
above take one where they take a plain string: `String.contains?` tests for
a match, `String.find` returns the first, `String.find_all` returns every
one, and `String.split` and `String.replace` split and replace around them.
`String.captures` returns the text each group in parentheses matched in the
first match, `None` for a group that took no part, and `String.captures_all`
does the same for every match. `String.replace` takes its replacement
literally, and `Regex.replace_all` reads `$1`, `$2` and so on in it as the
groups' text:

```nomi-run
import std/regex.Regex

fn main() -> Result<Unit, String> {
    text = "Order 1042 shipped 2026-10-25, order 977 on 2026-10-26"
    date = Regex`(\d{4})-(\d{2})-(\d{2})`

    dbg String.contains?(text, date)
    dbg String.find(text, Regex`\d+`)
    dbg String.find_all(text, date)
    dbg String.captures(text, date)
    dbg String.split("a, b,c ,  d", Regex`\s*,\s*`)
    dbg String.replace(text, Regex`\d{3,4} `, "# ")
    dbg Regex.replace_all(date, text, "$3/$2/$1")

    word = "order"
    orders = try Regex"(?i)${word} \\d+"
    dbg String.find_all(text, orders)

    Ok(Unit)
}
```
<!-- expect
dbg line 7: String.contains?(text, date) = True
dbg line 8: String.find(text, Regex`\d+`) = Some("1042")
dbg line 9: String.find_all(text, date) = ["2026-10-25", "2026-10-26"]
dbg line 10: String.captures(text, date) = Some([Some("2026"), Some("10"), Some("25")])
dbg line 11: String.split("a, b,c ,  d", Regex`\s*,\s*`) = ["a", "b", "c", "d"]
dbg line 12: String.replace(text, Regex`\d{3,4} `, "# ") = "Order # shipped 2026-10-25, order # on 2026-10-26"
dbg line 13: Regex.replace_all(date, text, "$3/$2/$1") = "Order 1042 shipped 25/10/2026, order 977 on 26/10/2026"
dbg line 17: String.find_all(text, orders) = ["Order 1042", "order 977"]
-->

A pattern built from a value is written in double quotes, as
`Regex"(?i)${word} \\d+"` (`(?i)` ignores case). The compiler can't check
it, since the value is known only when the program runs, so it is a
`Result` that you unwrap with `try` or `case`. Inside double quotes a backslash starts an escape, so the
pattern's `\d` is written `\\d`.

[Collections](/collections/) is next. It puts these values into lists, maps
and sets.
