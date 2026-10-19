---
title: "Typed Literals"
description: "Put a type name in front of a string, and that type's code turns the string into a value. A backtick literal is checked when the program compiles."
---

A typed literal is a type name followed by a string, such as
`Date"2026-06-15"` from [Dates & Times](/dates-and-times/). The type's own
code turns the string into a value. This is ordinary Nomi code, and you can
write it for your own types.

## The shape

A typed literal starts with a type name: `Sql"..."`, `Date"..."`,
`Box"..."`, or ``Regex`\d+```. The string can use double quotes, triple
quotes, or backticks. The type in front decides what value you get.

To make a type usable this way, implement the
[`Literal`](/reference/literals/#interface-literal) interface for it. Its one
function is [`from_fragments`](/reference/literals/#literalfrom_fragments).
When Nomi sees `Sql"..."`, it calls `Sql`'s `from_fragments`. This is an
ordinary interface call, so the usual rules about who may implement an
interface for a type apply:

```nomi-run
import {
    std/io
    std/literals.{Fragment, Literal}
}

type Sql String

impl Literal for Sql {
    fn from_fragments(fragments: List<Fragment<String>>) -> Sql {
        body = Iter.reduce(fragments, fn acc = "", frag -> {
            case frag {
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + "'" + v + "'"
            }
        })

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

Nomi splits the string into a list of
[fragments](/reference/literals/#enum-fragment). Plain text becomes a
`Static(text)` fragment. Each `${...}` becomes a `Dynamic(value)` fragment.
`from_fragments` walks the list and builds the result. This one puts quotes
around each dynamic value. A real SQL type would escape the values or pass
them as query parameters.

## The prefix is just a type

The prefix can be any type, so you have two choices:

- A new distinct type, like `type Sql String` above. It names the literal
  and holds the value it builds.
- A type you already have. Then the literal reads like a constructor.
  `Box"x"` builds a `Box`, the same as `#Box{contents: "x"}`:

```nomi-run
import {
    std/literals.{Fragment, Literal}
}

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
dbg line 23: Box"hello" = #Box{contents: "hello"}
dbg line 24: #Box{contents: "world"} = #Box{contents: "world"}
-->

Both lines build a `Box`.

## Regex literals

A regex literal is a typed literal whose prefix is `Regex`. Use backticks
when the pattern is fixed, so backslashes stay as they are. Use double
quotes when the pattern needs `${...}`. The two are compiled at different
times.

### Backtick literals are checked at compile time

A backtick string never contains `${...}`, so its text is the same every
time. If the type's `from_fragments` can fail (it returns a `Result`), Nomi
runs it while it checks your program:

- If it returns `Ok(v)`, the literal is the value itself. `` Regex`\d+` `` is
  a `Regex`, not a `Result`. It needs no `try`, and you can use it anywhere,
  including in a top-level `once`.
- If it returns `Err(e)`, the literal is a compile error with that error's
  message. `nomi check`, `nomi run` and your editor all report
  ``Regex`[` `` as `` typed literal Regex`[` is invalid: error parsing regexp:
  missing closing ]: `[` ``.

A double-quoted literal is built when the program runs, so it stays a
`Result`, with or without `${...}`. Use it when the pattern comes from a
value, and unwrap it with `try` or `case`:

```nomi-run
import {
    std/regex.Regex
}

once letters = Regex`[A-Za-z]+`

fn main() -> Result<Unit, String> {
    digits: Regex = Regex`\d+`
    prefixed = try Regex"room ${Regex.pattern(digits)}"
    text = "room 42, floor 7"

    dbg String.find_all(text, letters)
    dbg String.contains?(text, digits)
    dbg String.find(text, digits)
    dbg String.find_all(text, digits)
    dbg String.contains?(text, prefixed)

    Ok(Unit)
}
```
<!-- expect
dbg line 12: String.find_all(text, letters) = ["room", "floor"]
dbg line 13: String.contains?(text, digits) = True
dbg line 14: String.find(text, digits) = Some("42")
dbg line 15: String.find_all(text, digits) = ["42", "7"]
dbg line 16: String.contains?(text, prefixed) = True
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

If `from_fragments` cannot fail, as with `Sql` and `Box` above, both kinds of
quote give the same type.

A `Regex` is a [`Matcher`](/reference/matcher/). That is why the example can
pass it to `String.contains?`, `String.find`, `String.find_all` and
`String.split`, which accept a `Regex` wherever they accept a plain string.
`String.find` returns the first match. `Regex.replace_all` replaces each
match, and `$1` in the replacement stands for the first capture group.

`String.captures` takes only a `Regex`, because only a regex has capture
groups. It returns the groups of the first match, from group 1, and
`String.captures_all` returns them for every match. A group that did not
take part in the match is `None`:

```nomi-run
import std/regex.Regex

fn main() {
    dbg String.captures("ann@home", Regex`(\w+)@(\w+)`)
    dbg String.captures("b", Regex`(a)|(b)`)
    dbg "a=1, b=22" |> String.captures_all(Regex`(\w)=(\d+)`)
}
```
<!-- expect
dbg line 4: String.captures("ann@home", Regex`(\w+)@(\w+)`) = Some([Some("ann"), Some("home")])
dbg line 5: String.captures("b", Regex`(a)|(b)`) = Some([None, Some("b")])
dbg line 6: "a=1, b=22" |> String.captures_all(Regex`(\w)=(\d+)`) = [[Some("a"), Some("1")], [Some("b"), Some("22")]]
-->

The next chapter, [FFI & Dynamic](/ffi-and-dynamic/), shows how Nomi calls
Go code, and how to handle a value whose shape you don't know yet with
[`Dynamic`](/reference/dynamic/).
