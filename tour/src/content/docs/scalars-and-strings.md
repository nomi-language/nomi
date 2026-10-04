---
title: Scalars & Strings
description: Integers, floats, decimals, booleans, and strings — the values you build with.
---

Nomi has three numeric scalar types — [`Int`](/reference/int/),
[`Float`](/reference/float/), [`Decimal`](/reference/decimal/) — plus
[`Bool`](/reference/bool/), [`String`](/reference/strings/#type-string),
[`Byte`](/reference/bytes/#type-byte), and [`Bytes`](/reference/bytes/#type-bytes).

## Numbers, booleans, operators

Arithmetic, comparisons, and the logical operators `and` / `or` / `!` all read
as you'd expect:

```nomi-run
fn main(): Bool {
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

## Number literal forms

Numbers can use digit separators, alternate radices (hex / binary / octal), or
scientific notation — different spellings of the same values:

```nomi-run
fn main(): Float {
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

## Decimal — exact arithmetic

[`Float`](/reference/float/) is fast but approximate: small rounding errors accumulate, and
familiar identities like the one below quietly fail. [`Decimal`](/reference/decimal/) is the exact
alternative — use it whenever rounding error is a bug (money, billing,
anywhere correctness matters more than speed). Decimal literals carry a `d`
suffix:

```nomi-run
fn main(): Decimal {
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

## Strings — interpolation and concatenation

Strings interpolate `${expr}` and concatenate with `+`:

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

[`io.print`](/reference/io/#ioprint) accepts any value that can be displayed, so
you rarely need to stringify before printing — interpolation calls each value's
[`Display.to_string`](/reference/display/#displayto_string) for you.

`${` is the only special pair in a string. Write `\${` for a literal `${`; a
`$` on its own, `#` and `#{` are ordinary text:

```nomi-run
import std/io

fn main() {
    io.print("echo \${HOME} costs $5 #{not a set}")
}

```
<!-- expect
echo ${HOME} costs $5 #{not a set}
-->

`+`, `-`, `*`, and `/` are backed by standard operator interfaces:
[`Add`](/reference/add/), [`Subtract`](/reference/subtract/),
[`Multiply`](/reference/multiply/), and [`Divide`](/reference/divide/). The
built-in scalar impls cover numeric arithmetic; `+` also covers string
concatenation. Custom types can implement the same interfaces when the operator
is the clearest domain operation.

## Text model

Nomi's text model has three pieces:

- [`String`](/reference/strings/#type-string) is text. Nomi has no separate `Char` type, so even one-character
  text is a `String`.
- A grapheme is one user-visible character. `é` and many emoji count as one
  grapheme even when they are built from multiple Unicode values. Nomi
  represents graphemes as `String` values.
- [`Codepoint`](/reference/codepoints/) is an integer-backed Unicode scalar value: a valid number in
  Unicode's scalar-value range. Use it when you need to inspect the lower-level
  values that make up text.
- [`Bytes`](/reference/bytes/#type-bytes) is immutable binary data, and
  [`Byte`](/reference/bytes/#type-byte) is one byte in that buffer. Use it for
  UTF-8 boundaries, file/network payloads, and Go `[]byte` FFI.

Most string operations use the grapheme view: [`String.length`](/reference/strings/#stringlength),
[`String.slice`](/reference/strings/#stringslice), and
[`String.reverse`](/reference/strings/#stringreverse) work in user-visible
characters rather than raw bytes. A `String` iterates by grapheme too, so
`Iter.to_list("café")` is its list of graphemes.

Use [`String.to_codepoints`](/reference/strings/#stringto_codepoints) when you need the
lower-level scalar values:

```nomi-run
fn main(): List<Int> {
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

An ASCII codepoint has a literal: the character in single quotes, `'a'`, or an
escape, `'\n'`, `'\''`, `'\u{7F}'`. It is a `Codepoint`, not a one-character
string, so `"a" == 'a'` is a type error. Use it for codepoint-level work such
as scanning and matching, and keep text in strings. The literal is ASCII only
because only there do the character, its codepoint and its UTF-8 byte agree;
`"é"` stays a string.

```nomi-run
fn kind(cp: Codepoint): String {
    case cp {
        ',' -> "comma"
        ' ' -> "space"
        _ -> Codepoint.to_string(cp)
    }
}

fn main(): List<String> {
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

String equality is byte-based, so visually identical text can compare unequal
when it uses different Unicode forms. Normalize with
[`String.normalize`](/reference/strings/#stringnormalize) before comparing text
from mixed sources.

## Bytes — binary data

Use [`String.to_bytes`](/reference/strings/#stringto_bytes) to encode text as
UTF-8 bytes. `Bytes` is iterable, so the generic `Iter.*` functions work on it,
and [`Byte.to_int`](/reference/bytes/#byteto_int) exposes each `Byte` as an
integer when you need to inspect it. `Bytes + Bytes` concatenates buffers; the
named form is [`Bytes.concat`](/reference/bytes/#bytesconcat).

```nomi-run
import {
    std/io
}

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
dbg line 7: Bytes.length(raw) = 5
dbg line 8: raw |> Iter.map(Byte.to_int) |> Iter.to_list() = [99, 97, 102, 195, 169]
go:café
-->

`Byte` itself is not numeric. Convert through
[`Byte.from_int`](/reference/bytes/#bytefrom_int) and `Byte.to_int` when a
boundary really needs integer values.

## Triple-quoted strings

A `"""…"""` literal spans multiple lines. Interpolation works the same way,
and leading indentation common to every line is stripped:

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

## Raw strings

A backtick string is raw: escapes and `${...}` interpolation are not
processed. Use it for embedded text where those characters should pass through
literally:

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

Multi-line raw strings use the same indentation rules as triple-quoted
strings.

For compiled regular expressions, use the `Regex` typed literal form shown in
[Typed Literals](/typed-literals/#regex-literals).

The next chapter — [Collections](/collections/) — uses these scalar values
inside lists, maps, and sets.
