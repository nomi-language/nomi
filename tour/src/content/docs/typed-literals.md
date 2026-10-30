---
title: "Typed Literals"
description: "Put a type name in front of a string, and that type's code turns the string into a value. A backtick literal is checked when the program compiles."
---

A typed literal is a type name followed by a string, such as
``Date`2026-06-15` `` from [Dates & Times](/dates-and-times/). The type's own
code turns the string into a value. This is ordinary Nomi code, and you can
write it for your own types.

## The shape

A typed literal starts with a type name: `Html"..."`, `Date"..."`,
`Box"..."`, or ``Regex`\d+```. The string can use double quotes, triple
quotes, or backticks. The type in front decides what value you get.

To make a type usable this way, implement the
[`Literal`](/reference/literals/#interface-literal) interface for it. Its one
function is [`from_fragments`](/reference/literals/#literalfrom_fragments).
When Nomi sees `Html"..."`, it calls `Html`'s `from_fragments`. This is an
ordinary interface call, so the usual rules about who may implement an
interface for a type apply:

```nomi-run
import {
    std/io
    std/literals.{Fragment, Literal}
}

type Html String

// The text with HTML's special characters written as entities.
fn escape(text: String) -> String {
    text
    |> String.replace("&", "&amp;")
    |> String.replace("<", "&lt;")
    |> String.replace(">", "&gt;")
    |> String.replace("\"", "&quot;")
}

impl Literal for Html {
    fn from_fragments(fragments: List<Fragment<String>>) -> Html {
        body = Iter.reduce(fragments, fn acc = "", frag -> {
            case frag {
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + escape(v)
            }
        })

        Html(body)
    }
}

fn main() {
    name = "Tom & <Jerry>"
    Html(text) = Html"<p>Hello, ${name}</p>"
    io.print(text)
}
```
<!-- expect
<p>Hello, Tom &amp; &lt;Jerry&gt;</p>
-->

Nomi splits the string into a list of
[fragments](/reference/literals/#enum-fragment). Plain text becomes a
`Static(text)` fragment. Each `${...}` becomes a `Dynamic(value)` fragment.
`from_fragments` walks the list and builds the result. This one escapes
each dynamic value, so a name holding `<` can't add markup to the page.

std/sql's [`Sql`](/reference/sql/#type-sql) literal works the same way, but
keeps each value apart from the SQL as a query parameter, so a value can
never change what a query does. The [Databases](/databases/#parameters)
chapter shows it.

## The prefix is just a type

The prefix can be any type, so you have two choices:

- A new distinct type, like `type Html String` above. It names the literal
  and holds the value it builds.
- A type you already have. Then the literal reads like a constructor.
  `Box"x"` builds a `Box`, the same as `#Box{contents: "x"}`:

```nomi-run
import std/literals.{Fragment, Literal}

struct Box {
    contents: String
}

impl Literal for Box {
    fn from_fragments(fragments: List<Fragment<String>>) -> Box {
        body = Iter.reduce(fragments, fn acc = "", frag -> {
            case frag {
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + v
            }
        })

        #Box{contents: body}
    }
}

fn main() {
    dbg Box"hello"
    dbg #Box{contents: "world"}
}
```
<!-- expect
dbg line 21: Box"hello" = #Box{contents: "hello"}
dbg line 22: #Box{contents: "world"} = #Box{contents: "world"}
-->

Both lines build a `Box`.

## Backtick literals are checked at compile time

A backtick string never contains `${...}`, so its text is the same every
time. If the type's `from_fragments` can fail (it returns a `Result`), Nomi
runs it while it checks your program. `Regex` from `std/regex` is one such
type:

- If it returns `Ok(v)`, the literal is the value itself. `` Regex`\d+` `` is
  a `Regex`, not a `Result`. It needs no `?`, and you can use it anywhere,
  including in a top-level `once`.
- If it returns `Err(e)`, the literal is a compile error with that error's
  message. `nomi check`, `nomi run` and your editor all report
  ``Regex`[` `` as `` typed literal Regex`[` is invalid: error parsing regexp:
  missing closing ]: `[` ``.

A double-quoted literal is built when the program runs, so it stays a
`Result`, with or without `${...}`. Use it when the pattern comes from a
value, and unwrap it with `?` or `case`:

```nomi-run
import std/regex.Regex

once digits = Regex`\d+`

fn main() -> Result<Unit, String> {
    word = "floor"
    floor = Regex"${word} \\d+"?
    text = "room 42, floor 7"

    dbg String.find_all(text, digits)
    dbg String.find(text, floor)

    Ok(Unit)
}
```
<!-- expect
dbg line 10: String.find_all(text, digits) = ["42", "7"]
dbg line 11: String.find(text, floor) = Some("floor 7")
-->

This works for every type whose `from_fragments` can fail, including the
calendar types. ``Date`2026-06-15` `` is a `Date`, and
``Date`2026-13-01` `` does not compile.

When the program runs, each backtick literal is built once, the first time
it is reached. A backtick literal inside a loop is reused on later passes.

The compiler can only run a `from_fragments` that works out its answer from
the text alone. If it prints, reads a file or the clock, or never finishes,
a backtick literal of that type is a compile error. Use double quotes for
that type instead.

If `from_fragments` cannot fail, as with `Html` and `Box` above, both kinds of
quote give the same type.

[Scalars & Strings](/scalars-and-strings/#regular-expressions) shows what
you can do with a `Regex`: find, split, replace and read capture groups.

The next chapter, [Generating code](/generating-code/), shows how a
library writes whole declarations for you from data, as ordinary Nomi files.
