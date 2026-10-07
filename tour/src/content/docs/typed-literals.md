---
title: "Typed Literals"
description: "Typed literals call a type's `Literal` handler on string fragments. Use quoted strings or raw backtick strings."
---

You've already met one of these: `Date"2026-06-15"` in [Dates & Times](/dates-and-times/)
isn't a separate language feature, it's regular `Literal` dispatch dressed up
as a literal. A typed literal starts with a type name and then a quoted,
triple-quoted, or raw backtick string. The body becomes a list of *fragments*
— alternating literal text and `${…}` interpolations — that the type's handler
reduces into a value.

## The shape

A typed literal starts with a type name: `Sql"..."`, `Date"..."`,
`Box"..."`, or ``Regex`\d+```. The prefix type decides how to turn the literal
body into a value.

To opt in, the prefix type implements [`Literal`](/reference/literals/#interface-literal) by defining
[`from_fragments`](/reference/literals/#literalfrom_fragments). A `Sql"..."` use site dispatches to
the `Literal.from_fragments` implementation for `Sql`, passing a list that
contains literal text plus the values from `${...}` slots.
That is ordinary interface dispatch, so the same coherence and orphan-rule
checks apply:

```nomi-run
import {
    std/io
    std/literals.{Fragment, Literal}
}

type Sql String

impl Literal for Sql {
    fn from_fragments(fragments: List<Fragment<String>>): Sql {
        body = Iter.reduce(fragments, |acc = "", frag|
            case frag {
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + "'" + v + "'"
            }
        )

        Sql(body)
    }
}

fn main() {
    user = "alice"
    Sql(text) = Sql"SELECT * FROM users WHERE name = ${user}"
    io.print(text)
}
```
<!-- expect
SELECT * FROM users WHERE name = 'alice'
-->

The body of `Sql"…"` is split into [`Static(text)` and `Dynamic(value)` fragments](/reference/literals/#enum-fragment)
slots wherever `${…}` appears. The handler walks the list and builds the
result however it likes — here we add SQL-style quotes around dynamic
values, but a real implementation might validate, parameterize, or escape.

## The prefix is just a type

Because the prefix is a type, you have two natural ways to spell one:

- **A dedicated distinct type**, as above — `type Sql String` both names the
  literal and carries the value it produces.
- **An existing domain type**, so the use site reads as a constructor. `Box"…"`
  builds an actual `Box` — same spelling whether you write `Box{contents: "x"}`
  or `Box"x"`:

```nomi-run
import {
    std/literals.{Fragment, Literal}
}

struct Box {
    contents: String
}

impl Literal for Box {
    fn from_fragments(fragments: List<Fragment<String>>): Box {
        body = Iter.reduce(fragments, |acc = "", frag|
            case frag {
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + v
            }
        )

        Box{contents: body}
    }
}

fn main() {
    dbg Box"hello"
    dbg Box{contents: "world"}
}
```
<!-- expect
dbg line 23: Box"hello" = Box{contents: "hello"}
dbg line 24: Box{contents: "world"} = Box{contents: "world"}
-->

`Box"hello"` and `Box{contents: "hello"}` build the same value (an actual
`Box`), through different syntactic doors.

## Regex literals

Regex literals are ordinary typed literals whose prefix type is `Regex`.
Use a backtick body when regex syntax should pass through literally. Use a
quoted body when the pattern needs interpolation:

```nomi-run
import {
    std/regex.Regex
}

fn main(): Result<Unit, String> {
    digits = try Regex`\d+`
    prefixed = try Regex"room ${Regex.pattern(digits)}"
    text = "room 42, floor 7"

    dbg Regex.pattern(digits)
    dbg Regex.match?(digits, text)
    dbg Regex.find(digits, text)
    dbg Regex.find_all(digits, text)
    dbg Regex.match?(prefixed, text)

    Ok(Unit)
}

```
<!-- expect
dbg line 10: Regex.pattern(digits) = "\\d+"
dbg line 11: Regex.match?(digits, text) = True
dbg line 12: Regex.find(digits, text) = Some("42")
dbg line 13: Regex.find_all(digits, text) = ["42", "7"]
dbg line 14: Regex.match?(prefixed, text) = True
-->

A `Regex` literal is a typed literal, not a special parser rule for regular
expressions. Raw backtick bodies are usually best for regex syntax, while
quoted bodies can still interpolate because they use the same `Literal`
fragment machinery. Invalid patterns stay in ordinary `Result` flow.

The next chapter — [FFI & Dynamic](/ffi-and-dynamic/) — steps back to the
host boundary: embedding Nomi in host programs, wrapping host libraries in
Nomi modules, and using [`Dynamic`](/reference/dynamic/) when a boundary value's shape is not known
yet.
