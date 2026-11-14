---
title: "Typed Literals"
description: "Put a type name in front of a string, and that type's code turns the string into a value. A literal without slots is checked when the program compiles."
---

A typed literal is a type name followed by a string, such as
`Date"2026-06-15"` from [Dates & Times](/dates-and-times/). The type's own
code turns the string into a value. This is ordinary Nomi code, and you can
write it for your own types.

## The shape

A typed literal starts with a type name: `Html"..."`, `Date"..."`,
`Box"..."`, or `` Regex`\d+` ``. The string can use double quotes, triple
quotes, or backticks. The type in front decides what value you get.

To make a type usable this way, implement the
[`Literal`](/reference/literals/#interface-literal) interface for it. Its one
function is [`from_fragments`](/reference/literals/#literalfrom_fragments).
When Nomi sees `Html"..."`, it calls `Html`'s `from_fragments`. This is an
ordinary interface call, so the usual rules about who may implement an
interface for a type apply:

```nomi-run
import
    std::io
    std::literals.{Fragment, Literal}
end

type Html String

// The text with HTML's special characters written as entities.
fn escape(text: String) -> String
    text
    |> String.replace("&", "&amp;")
    |> String.replace("<", "&lt;")
    |> String.replace(">", "&gt;")
    |> String.replace("\"", "&quot;")
end

impl Literal for Html
    fn from_fragments(fragments: List<Fragment<String>>) -> Html
        fragments
        |> Iter.map(fn frag ->
            case frag
                .Static(s)  -> s
                .Dynamic(v) -> escape(v)
            end
        end)
        |> String.join()
        |> Html()
    end
end

fn main()
    name = "Tom & <Jerry>"
    Html(text) = Html"<p>Hello, ${name}</p>"
    io.print(text)
end
```
<!-- expect
<p>Hello, Tom &amp; &lt;Jerry&gt;</p>
-->

Nomi splits the string into a list of
[fragments](/reference/literals/#enum-fragment). Plain text becomes a
`Static(text)` fragment. Each `${...}` becomes a `Dynamic(value)` fragment.
`from_fragments` turns each fragment into text and joins them. This one
escapes each dynamic value, so a name holding `<` can't add markup to the
page.

std::sql's [`Sql`](/reference/sql/#type-sql) literal works the same way, but
keeps each value apart from the SQL as a query parameter, so a value can
never change what a query does. The [Databases](/databases/#parameters)
chapter shows it.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- The impl chooses what a `${...}` slot must hold. `Fragment<String>`
  takes only `String` values; a slot of another type is an error at the
  slot. An interface such as `Display` takes any implementor.
- A backtick body has no slots: it is one `Static` fragment holding the
  text as written.
- A triple-quoted typed literal drops its shared indentation, as a
  triple-quoted string does.
- A prefix that is not a type in scope, or a type with no `impl Literal`,
  is an error that names what to write.
- `Literal` and `Fragment` are not in the prelude. Only a handler's author
  imports them.

Spec: [Typed literals](/spec/typed-literals-and-keyword-declarations/#typed-literals),
[Desugaring](/spec/typed-literals-and-keyword-declarations/#desugaring),
[Typed literal forms](/spec/literals/#typed-literal-forms).

</div>

## Any type can be the prefix

The prefix can be any type, so you have two choices:

- A new distinct type, like `type Html String` above. It names the literal
  and holds the value it builds.
- A type you already have. Then the literal reads like a constructor.
  `Box"x"` builds a `Box`, the same as `Box{contents: "x"}`:

```nomi-run
import std::literals.{Fragment, Literal}

struct Box
    contents: String
end

impl Literal for Box
    fn from_fragments(fragments: List<Fragment<String>>) -> Box
        contents =
            fragments
            |> Iter.map(fn frag ->
                case frag
                    .Static(s)  -> s
                    .Dynamic(v) -> v
                end
            end)
            |> String.join()

        Box{contents:}
    end
end

fn main()
    dbg Box"hello"
    dbg Box{contents: "world"}
end
```
<!-- expect
dbg line 24: Box"hello" = Box{contents: "hello"}
dbg line 25: Box{contents: "world"} = Box{contents: "world"}
-->

Both lines build a `Box`.

## A literal without slots is checked at compile time

A literal with no `${...}` slot has the same text every time, whichever
quotes it uses. So Nomi runs the type's `from_fragments` while it checks
your program. If `from_fragments` can fail (it returns a `Result`), as it can
for `Regex` from `std::regex`:

- If it returns `Ok(v)`, the literal is the value itself. `` Regex`\d+` `` is
  a `Regex`, not a `Result`. It needs no `?`, and you can use it anywhere,
  including in a top-level `once`.
- If it returns `Err(e)`, the literal is a compile error with that error's
  message. `nomi check`, `nomi run` and your editor all report
  `Regex"["` as `` typed literal Regex"[" is invalid: error parsing regexp:
  missing closing ]: `[` ``.

A literal with a slot is built when the program runs, so it stays a
`Result`. Use one when the pattern comes from a value, and unwrap it with
`?` or `case`:

```nomi-run
import std::regex.Regex

once digits = Regex`\d+`

fn main() -> Result<Unit, String>
    word = "floor"
    floor = Regex"${word} \\d+"?
    text = "room 42, floor 7"

    dbg String.find_all(text, digits)
    dbg String.find(text, floor)

    Ok(Unit)
end
```
<!-- expect
dbg line 10: String.find_all(text, digits) = ["42", "7"]
dbg line 11: String.find(text, floor) = Some("floor 7")
-->

This works for every type whose `from_fragments` can fail, including the
calendar types. `Date"2026-06-15"` is a `Date`, and `Date"2026-13-01"`
does not compile. The quotes only change how the text is read: a backtick
body keeps every backslash, which suits a regex, and double quotes suit the
rest.

The run must work out its result from the text alone. If `from_fragments`
prints, reads a file or the clock, or never finishes, a literal of that type
without slots is a compile error. To build such a value from text, call one
of the type's functions instead, such as `Date.parse(text)`. When the
program runs, a literal without slots is built once, the first time it is
reached, so a literal in a function that runs many times is built only once.

If `from_fragments` cannot fail, as with `Html` and `Box` above, the compiler
still runs it, to make sure it can, and the literal is the value
`from_fragments` returns.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A `?` after a literal without slots is an error, since the literal is
  the value, not a `Result`. The hint says to remove the `?`.
- The compile error shows the `Err` through its `Display`, or its `Debug`
  when it has none.
- A handler that doesn't finish within the compiler's step limit, or traps,
  is refused the same way as one with effects.
- A `Regex` literal's type depends on its text. One with `${...}` can't
  know its groups, so its captures come back as a list.

Spec: [A literal without slots is checked at compile time](/spec/typed-literals-and-keyword-declarations/#a-literal-without-slots-is-checked-at-compile-time).

</div>

[Scalars & Strings](/scalars-and-strings/#regular-expressions) shows what
you can do with a `Regex`: find, split, replace and read capture groups.
[`Bits`](/reference/bits/) from `std::bits` is checked the same way:
`Bits"10aa_aaaa"` is a bit pattern whose `x`s match any bit and whose
letters name a field, and `Bits"10z2"` does not compile.

## Checked literals

A type can also check a literal that has `${...}` slots, by implementing
[`CheckedLiteral`](/reference/literals/#interface-checkedliteral) instead
of `Literal`. Its `check` reads the text around the slots while Nomi checks
your program, and its `build` fills the slots when the program runs and
cannot fail. So the literal is the value, not a `Result`, whichever quotes
it uses.

`Url` works this way. `Url"https://api.example.com/greet/${name}"` is a
`Url`, with `name` escaped as a path segment. A slot takes any `Display`
value, so an `Int` or a `Uuid` id goes in as it is. `Url"https://${host}/"`
is a compile error at the slot, since no escaping makes a value safe as a
host. `Toml` does too: in `Toml"name = ${name}"` the slot is written as the
value's TOML, so a `String` is quoted and escaped, and a slot inside a TOML
string is a compile error. Spec:
[Checked literals](/spec/typed-literals-and-keyword-declarations/#checked-literals).

Where the type is already known, as a dot-variant's enum is, the tag can
be left out. `u: Url = "https://example.com/${id}"` and
`http.get("https://example.com/")` are `Url` literals, checked the same
way. Only a literal converts: a `String` value where a `Url` is expected
is still an error. Spec:
[Literals typed by their position](/spec/typed-literals-and-keyword-declarations/#literals-typed-by-their-position).

std::html's `Html`, which [HTTP](/http/) uses for pages, is not the `Html`
this page declares. The compiler checks its literals itself, as HTML
templates.

[Modules & Imports](/modules-and-imports/) is next. It covers how a program
is split across files.
