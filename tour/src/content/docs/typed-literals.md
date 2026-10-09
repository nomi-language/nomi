---
title: "Typed Literals"
description: "Typed literals call a type's `Literal` handler on string fragments. A raw backtick literal is checked at compile time; a quoted one can interpolate."
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

Regex literals are ordinary typed literals whose prefix type is `Regex`. Use
a backtick body when regex syntax should pass through literally, and a
quoted body when the pattern needs interpolation. The two differ in when the
pattern is compiled.

### Backtick literals are checked at compile time

A backtick body never interpolates, so its handler always sees the same
text. When that handler can fail (it returns a `Result`), Nomi runs it while
checking your program:

- If it answers `Ok(v)`, the literal is the value itself. `` Regex`\d+` `` is a
  `Regex`, not a `Result`, so it needs no `try`, and it works anywhere a
  value does, including a top-level `once`.
- If it answers `Err(e)`, the literal is a compile error, with the handler's
  message: ``Regex`[` `` is reported by `nomi check`, `nomi run` and your
  editor as `` typed literal Regex`[` is invalid: error parsing regexp:
  missing closing ]: `[` ``.

A double-quoted literal keeps the handler's `Result`, whether or not it
interpolates, because it is built when the program runs. Use it when the
pattern comes from a value, and handle the `Result` with `try` or `case`:

```nomi-run
import {
    std/regex.Regex
}

once letters = Regex`[A-Za-z]+`

fn main(): Result<Unit, String> {
    digits: Regex = Regex`\d+`
    prefixed = try Regex"room ${Regex.pattern(digits)}"
    text = "room 42, floor 7"

    dbg String.find_all(text, letters)
    dbg String.contains?(text, digits)
    dbg Regex.find(digits, text)
    dbg String.find_all(text, digits)
    dbg String.contains?(text, prefixed)

    Ok(Unit)
}
```
<!-- expect
dbg line 12: String.find_all(text, letters) = ["room", "floor"]
dbg line 13: String.contains?(text, digits) = True
dbg line 14: Regex.find(digits, text) = Some("42")
dbg line 15: String.find_all(text, digits) = ["42", "7"]
dbg line 16: String.contains?(text, prefixed) = True
-->

The same holds for every type whose handler can fail, such as the calendar
types: ``Date`2026-06-15` `` is a `Date`, and ``Date`2026-13-01` `` does not
compile. The handler runs again when the program does, once per literal, so
a backtick literal inside a loop is built the first time and reused after.

The compiler can only run a handler that computes its answer from the text:
one that prints, reads a file or the clock, or never finishes is a compile
error on a backtick literal. Write that literal with double quotes instead.
A handler that cannot fail, like `Sql`'s and `Box`'s above, gives the same
type with either quote.

A `Regex` is a [`Matcher`](/reference/matcher/), so the example above
searches with the String functions: `String.contains?`, `String.find_all`
and `String.split` take a `Regex` wherever they take a plain string.
`Regex.find` returns the first match, and `Regex.replace_all` expands `$1`
capture references in its replacement.

The next chapter — [FFI & Dynamic](/ffi-and-dynamic/) — steps back to the
host boundary: embedding Nomi in host programs, wrapping host libraries in
Nomi modules, and using [`Dynamic`](/reference/dynamic/) when a boundary value's shape is not known
yet.
