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
    dbg 7 / 2
    dbg 7 % 2
    dbg -(3 + 4)
    dbg 42 > 10
    dbg True and not False
    dbg Int.pow(2, 100)
    dbg 0b1011 >>> 1 &&& 1 // the second-lowest bit
    dbg 0b0011 ||| 0b0100 // set a bit
}

```
<!-- expect
dbg line 2: 42 + 8 = 50
dbg line 3: 10 - 3 = 7
dbg line 4: 2 * 21 = 42
dbg line 5: 7 / 2 = 3
dbg line 6: 7 % 2 = 1
dbg line 7: -(3 + 4) = -7
dbg line 8: 42 > 10 = True
dbg line 9: True and not False = True
dbg line 10: Int.pow(2, 100) = 1267650600228229401496703205376
dbg line 11: 0b1011 >>> 1 &&& 1 = 1
dbg line 12: 0b0011 ||| 0b0100 = 7
-->

Arithmetic and comparisons work as in most languages. The logical operators
are `and`, `or` and `not`. `not` binds looser than a comparison, so
`not a == b` is `not (a == b)`. An [`Int`](/reference/int/) is a whole number
of any size, so arithmetic never overflows. `/` on `Int` drops the remainder,
rounding toward zero, and `%` gives the remainder. Dividing by zero stops the
program. The bitwise operators are `&&&`, `|||`, `^^^`, prefix `~~~`, `<<<`
and `>>>`; `&&&` and the shifts bind like `*`, and `|||` and `^^^` like `+`.

Each arithmetic operator comes from an interface: `+` from
[`Add`](/reference/add/), `-` from [`Subtract`](/reference/subtract/), `*`
from [`Multiply`](/reference/multiply/) and `/` from
[`Divide`](/reference/divide/). The number types implement them. Your own
types can implement them too, when an operator is the clearest way to write
the operation.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- `%` takes the sign of the dividend: `-7 / 3` is `-2` and `-7 % 3` is
  `-1`. [`Int.modulo`](/reference/int/#intmodulo) is the floored form:
  `Int.modulo(-7, 3)` is `2`.
- `Int` division by zero traps with `division by zero`. `Float` division
  by zero gives an infinity or NaN instead (see
  [Exact arithmetic with `Decimal`](#exact-arithmetic-with-decimal)).
- The number types never mix. `1 + 2.0` is
  `binary + type mismatch: Int vs Float`; convert with `Int.to_float` or
  `Float.to_int`, which is `None` for NaN and the infinities.
- `%` works on `Float` and `Decimal` too, with the dividend's sign:
  `-7.5 % 2.0` is `-1.5`, and `1.0 % 0.0` is NaN.

Spec: [Integer division and remainder](/spec/expressions/#integer-division-and-remainder),
[Integer arithmetic is exact](/spec/expressions/#integer-arithmetic-is-exact),
[No Implicit Conversion](/spec/type-relations/#no-implicit-conversion).

</div>

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

## Exact arithmetic with `Decimal`

```nomi-run
fn main() {
    // Float carries base-2 imprecision
    dbg 0.1 + 0.2 == 0.3

    // Decimal is exact
    dbg 0.1d + 0.2d == 0.3d

    // Sum four prices
    dbg 19.99d + 5.00d + 2.50d + 0.99d
}

```
<!-- expect
dbg line 3: 0.1 + 0.2 == 0.3 = False
dbg line 6: 0.1d + 0.2d == 0.3d = True
dbg line 9: 19.99d + 5.00d + 2.50d + 0.99d = 28.48d
-->

[`Float`](/reference/float/) is fast but not exact. It stores numbers in
binary, so `0.1 + 0.2` is not quite `0.3`. To compare a computed Float, give
the difference you accept: `Float.equal_within(0.1 + 0.2, 0.3, 1e-9)` is
`True`. A `Float` follows IEEE 754, so dividing by zero doesn't stop the
program: `1.0 / 0.0` is `+Inf`, and `0.0 / 0.0` is NaN, which
[`Float.is_nan`](/reference/float/#floatis_nan) detects.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

NaN compares three ways, chosen so maps, sorting and patterns behave:

- `==` is reflexive: `nan == nan` is `True`, so a NaN map key is found
  again. `-0.0 == 0.0` is `True`, and the two are one key.
- `Float.compare` is a total order with NaN highest, so a sort is
  deterministic: `Float.compare(nan, nan)` is `Equal`.
- `<`, `<=`, `>` and `>=` follow IEEE 754: `nan <= nan` is `False`.

`Decimal` has no NaN or infinity, and its division by zero traps. Its scale
shows in display but not in equality: `1.50d == 1.5d`.

Spec: [Float Special Values](/spec/types/#float-special-values),
[NaN and the three comparison behaviours](/spec/type-relations/#nan-and-the-three-comparison-behaviours),
[Decimal arithmetic](/spec/expressions/#decimal-arithmetic).

</div>

[`Decimal`](/reference/decimal/) stores numbers in base 10 and adds them
exactly. Use it when a rounding error would be a bug, as with money. A
decimal literal ends in `d`.

`Decimal`'s `+`, `-` and `*` are exact. `/` keeps 34 significant digits,
rounded half-even, so `10d / 3d` is `3.333333333333333333333333333333333d`,
and a quotient that ends is exact with no trailing zeros: `10.00d / 4d` is
`2.5d`. To round to a number of places, name them and the rounding:
`Decimal.divide(10d, 3d, 2, .HalfEven)` is `3.33d`.

## String interpolation and concatenation

```nomi-run
import std::io

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
strings, because `String` implements `Add`, the interface behind `+`.

Interpolation turns each value into text with
[`Display.to_string`](/reference/display/#displayto_string).
[`io.print`](/reference/io/#ioprint) accepts any value that implements
`Display`. So you rarely convert a value to a string yourself before
printing it.

`${` is the only special sequence in a string. To write a literal `${`,
write `\${`. A `$` on its own is ordinary text:

```nomi-run
import std::io

fn main() {
    io.print("echo \${HOME} costs $5")
}

```
<!-- expect
echo ${HOME} costs $5
-->

To split text:

- [`String.split`](/reference/strings/#stringsplit) splits at a separator.
- [`String.words`](/reference/strings/#stringwords) splits at runs of
  whitespace.
- [`String.lines`](/reference/strings/#stringlines) splits into lines.

The [`String` reference](/reference/strings/) lists the rest, and
[Regular expressions](#regular-expressions) below shows how to split and
search by a pattern.

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
[`is_letter`](/reference/code_points/#codepointis_letter),
[`is_whitespace`](/reference/code_points/#codepointis_whitespace),
[`is_upper`](/reference/code_points/#codepointis_upper) and
[`is_lower`](/reference/code_points/#codepointis_lower) follow Unicode, so `é`
and `ж` are letters. [`is_digit`](/reference/code_points/#codepointis_digit)
and [`to_digit`](/reference/code_points/#codepointto_digit) accept only ASCII
`0` to `9`, which is what a phone number or a checksum means.

```nomi-run
fn main() {
    text = "Tél 555-01"

    text
    |> String.to_codepoints()
    |> Iter.filter(Codepoint.is_letter)
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
  |> Iter.filter(Codepoint.is_letter)
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

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A grapheme can be many code points. The family emoji `👨‍👩‍👧` is one
  grapheme of five code points, so `String.length` counts it as 1.
- Ordering compares bytes too, which is code point order. For the order a
  reader expects, use [`std::collation`](/reference/collation/).
- A `Codepoint` literal holds one ASCII character. Outside ASCII, take one
  from `String.to_codepoints` or build it with
  [`Int.to_codepoint`](/reference/int/#intto_codepoint).

Spec: [Strings](/spec/types/#strings),
[Codepoint literals](/spec/literals/#codepoint-literals).

</div>

## Binary data with `Bytes`

```nomi-run
import std::io

fn main() {
    raw = String.to_bytes("café")
    dbg Bytes.length(raw)
    dbg raw |> Iter.map(Byte.to_int) |> Iter.to_list()

    prefix = Bytes.from_list([0x67, 0x6F, 0x3A])

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
[`Int.to_byte`](/reference/int/#intto_byte) and `Byte.to_int` when you
need the number. An integer literal where a `Byte` is expected is one, so
`[0x67, 0x6F, 0x3A]` above is the bytes of `go:`, and a literal outside 0
to 255 there is a compile error.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- Only the literal takes its type from where it stands. `n = 0x81` is an
  `Int`, and `b: Byte = n` is `type mismatch: expected Byte, got Int`.
- In a comparison or a bitwise operator, a literal takes `Byte` from the
  other operand: `b == 0x41`, `b &&& 0x7F`.
- A `Byte` pattern is a literal too: `0x00 -> "nul"`.

Spec: [Byte literals](/spec/literals/#byte-literals).

</div>

## Triple-quoted strings

```nomi-run
import std::io

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
import std::io

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

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A raw string has no escapes at all, so it can't hold a backtick: the
  first backtick ends it.
- A triple-quoted string reads no escapes either, except `\${`, but it
  still interpolates `${...}`.
- In both multi-line forms, a newline right after the opening quote and
  right before the closing one is dropped, and the closing line's
  indentation counts toward the shared indentation.
- A backtick string with a type in front, as in ``Regex`\d+` ``, is a typed
  literal ([Typed Literals](/typed-literals/)).

Spec: [Raw strings](/spec/literals/#raw-strings),
[Triple-quoted strings](/spec/literals/#triple-quoted-strings).

</div>

## Regular expressions

[`std::regex`](/reference/regex/) has `Regex`. Write a pattern in backticks,
as ``Regex`\d+` ``, so its backslashes stay as written. The compiler checks
the pattern, so ``Regex`[` `` is an error before the program runs
([Typed Literals](/typed-literals/#backtick-literals-are-checked-at-compile-time) explains how).

`String.split`, `String.contains`, `String.starts_with`,
`String.ends_with`, `String.replace`, `String.find` and `String.find_all`
take a [`Matcher`](/reference/matcher/) for the text to look for: a plain
`String` or a `Regex`. `String.find` returns the first match and
`String.find_all` every one. `String.captures` returns the text each group in
parentheses matched, shaped by the literal's type: `date` below is a
`Regex<(String, String, String)>`, a group a match can skip is a
`Maybe<String>`, and named groups (`(?P<year>\d{4})`) make a struct. With a
`Regex`, `String.replace` reads `$1`, `$2` in the replacement as the groups'
text (`$$` is a literal `$`):

```nomi-run
import std::regex.Regex

fn main() {
    text = "Order 1042 shipped 2026-10-25, order 977 on 2026-10-26"
    date = Regex`(\d{4})-(\d{2})-(\d{2})`

    dbg String.contains(text, date)
    dbg String.find(text, Regex`\d+`)
    dbg String.find_all(text, date)
    dbg String.captures(text, date)
    dbg String.split("a, b,c ,  d", Regex`\s*,\s*`)
    dbg String.replace(text, Regex`\d{3,4} `, "# ")
    dbg String.replace(text, date, "$3/$2/$1")

    dbg String.find_all(text, Regex`(?i)order \d+`)
}
```
<!-- expect
dbg line 7: String.contains(text, date) = True
dbg line 8: String.find(text, Regex`\d+`) = Some("1042")
dbg line 9: String.find_all(text, date) = ["2026-10-25", "2026-10-26"]
dbg line 10: String.captures(text, date) = Some(("2026", "10", "25"))
dbg line 11: String.split("a, b,c ,  d", Regex`\s*,\s*`) = ["a", "b", "c", "d"]
dbg line 12: String.replace(text, Regex`\d{3,4} `, "# ") = "Order # shipped 2026-10-25, order # on 2026-10-26"
dbg line 13: String.replace(text, date, "$3/$2/$1") = "Order 1042 shipped 25/10/2026, order 977 on 26/10/2026"
dbg line 15: String.find_all(text, Regex`(?i)order \d+`) = ["Order 1042", "order 977"]
-->

`(?i)` at the start of a pattern ignores case. A pattern in double quotes,
as `Regex"\\d+"`, is compiled when the program runs, so it is a `Result`
that you unwrap with `?` or `case`. Inside double quotes a backslash starts
an escape, so the pattern's `\d` is written `\\d`.

## See it in action

<div class="tour-examples">
<a href="/examples/reverse-string/"><span class="tour-example-name">Reverse String</span><span class="tour-example-what">Reverse a string as a reader sees it.</span></a>
<a href="/examples/micro-blog/"><span class="tour-example-name">Micro Blog</span><span class="tour-example-what">Cut a post to five characters, emoji and all.</span></a>
<a href="/examples/line-up/"><span class="tour-example-name">Line Up</span><span class="tour-example-what">Write 1st, 2nd, 3rd and 11th.</span></a>
</div>

<a class="tour-examples-all" href="/examples/?chapter=scalars-and-strings">Every example that uses Scalars &amp; Strings</a>

[Collections](/collections/) is next. It puts these values into lists, maps
and sets.
