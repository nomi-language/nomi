# Nomi Language Specification

Nomi is a fully immutable, functional-first, statically typed language. Types are inferred within a function; function signatures are annotated.

---

## 1. Introduction

### Scope and companion documents

This document defines Nomi: its syntax, its static rules, what a program does
when it runs, and the error text a violation gives. A feature it describes
works unless its section carries a status callout.

Other documents cover the rest:

- The [tour](/) teaches the language in learning order.
- [Writing Nomi](/writing/) says which of several legal forms to choose.
- The [standard library reference](/reference/) documents std's types and
  functions. This document names a std type only where a language rule
  depends on it.
- The [tooling reference](/tooling/) documents the `nomi` command,
  `nomi.toml`, dependencies and builds.
- How parts of the language are built is in the implementation's package
  docs, which [`AGENTS.md`](../AGENTS.md) maps.
- The [roadmap](roadmap.md) holds open design questions.

### Notation and error text

Code is shown in `nomi` blocks. A block that declares `fn main` at column 0
is a complete program; any other block is a fragment, and `...` in it stands
for code left out. A comment after an expression gives its value (`// 3`),
or says the expression is rejected.

A compile error is quoted as the compiler prints it, in a code span:
`` `type mismatch: expected Byte, got Int` ``. A compile error stops the
program before any of it runs. A run-time fault, a trap, is quoted with the
line prefix it prints: `line 3: index 7 is out of range for a Vector of length 5`.

A section that a tour chapter teaches starts with a "Taught in" line that
links it. A citation `§N *Heading*` names a heading in chapter N.

### Status callouts

A callout that starts **Status: not yet implemented** marks text that
describes intended behavior the compiler does not have yet. Each one is
listed in [feature-status.md](feature-status.md)'s "Aspirational language
features". All other text describes the compiler as it is.

## 2. Lexical structure

### Source text

A source file is UTF-8 text. Outside string literals, codepoint literals and
comments it holds only ASCII: any other character is an error at that
character (`` unexpected `Ã` ``).

A line ends in `\n` or `\r\n`, and the two mean the same program: a
multi-line string or a comment holds no `\r` from a `\r\n` line end.

Spaces, tabs and line ends separate tokens. Indentation has no meaning to
the grammar. Only a multi-line string's body (§3 *String Literals*) and the
lambda body check (§7 *Lambdas*) read it.

### Shebang line

A file may begin with a `#!` line, so that it runs as a script
([tooling reference](/tooling/commands/#scripts-nomi-file)). The `#!` must
be the file's first two bytes. The compiler ignores the rest of that line,
and line numbers count it, so every later line keeps its position in
diagnostics, test reports and the editor. `#!` anywhere else is the error
`` a `#!` line is allowed only as the first line of a file, starting at its first byte ``.

```nomi
#!/usr/bin/env nomi
import std::io

fn main()
    io.print("hi")
end
```

### Comments

Taught in [Bindings & Expressions](/bindings-and-expressions/#comments-and-tiny-tests).

Every comment runs from its marker to the end of the line. There are no
block comments.

| Marker | Meaning |
|---|---|
| `//` | An ordinary comment. Four or more slashes are one too. |
| `///` | A doc comment. It documents the declaration that follows it. |
| `//#` | File-level documentation. To the compiler it is an ordinary comment that attaches to nothing; the standard library reference reads a module's `//#` lines as its page intro. |
| `//!` | An attached test line (§17). |

A `///` comment with no declaration after it, above an `import` or a `test`
for instance, is the error `` a `///` doc comment must be followed by a declaration it documents; use `//` for an ordinary comment or `//#` for file-level text ``.
Inside a body, a `///` line is the error
`` a `///` doc comment documents the declaration after it, so it can't go inside a body; use `//` for a comment here ``,
and so is a `//!` line that no nested declaration follows.

A `//!` line must start its line. After code on the same line it is the
error `` a `//!` attached test must start its own line, above the declaration it tests; write `//` for a comment after code ``.

A comment may stand inside a bracketed, comma-separated list: a call's
arguments; a list, vector, set, map, tuple or struct literal; a function's or
lambda's parameters; type parameters and type arguments; a list, vector, set,
tuple, map, struct or variant pattern; a destructure; and an import's `{...}`
selector. It goes on a line of its own before an item, after the opening
bracket or after the last item, or at the end of a line after an item's
comma.

```nomi
fn add(a: Int, b: Int) -> Int
    a + b
end

fn main()
    total = add(
        // the base
        1,
        2, // the step
    )

    dbg total // 3
end
```

### Naming Conventions

An identifier is an ASCII letter or `_`, followed by ASCII letters, digits
and `_`. Its first character decides its class:

- A name that starts with an uppercase letter is a type name: a type, an
  interface, a type parameter, an enum variant, or the prefix of a typed
  literal.
- Any other name is a value name: a function, a binding, a parameter, a
  field, a `once`, a module.

A type name is PascalCase and holds no `_`. A value name is snake_case and
holds no uppercase letter. The checker enforces both, for every kind of
declaration:

| Entity | Case | Example |
|---|---|---|
| Types (`struct`, `enum`, `type`, `typealias`, `interface`) | PascalCase | `User`, `HttpRequest` |
| Enum variants | PascalCase | `Some`, `Active` |
| Type parameters | PascalCase | `T`, `Elem` |
| Functions, bindings, parameters, fields, `once` values | snake_case | `parse_input`, `max_retries` |
| Modules (files) | snake_case | `math_utils` |

`userName = 1` is the error `binding name "userName" must be snake_case`,
and `struct Http_Request` is `struct name "Http_Request" must be PascalCase`.

Case is not visibility: a declaration is visible outside its file only when
it is `pub` (§14).

A `?` or `!` written right after a name is the postfix operator of §11, not
part of the name: `f?(x)` propagates the failure of `f` and calls the
function it holds.

### Discard names

In a binding position, `_` and a name that starts with a single `_`
(`_unused`) are discard names. A discard name matches any value and binds
nothing; `_name` only labels the value for the reader. Read as a value, `_`
is the error `` `_` is a discard name, not a value ``. That error makes `_`
a typed hole: where the place it stands in expects a type, its help names
the type and the values in scope that have it, nearest binding first, as
in `` as a hole, it stands for a value of type `String` `` and
`` in scope with that type: `label`, `fallback` ``. A program with a hole
does not run. A name that starts
with two underscores (`__x`) is not a discard name; it is an ordinary
binding.

### Keywords

These words are reserved and cannot name anything:

`and`, `as`, `assert`, `begin`, `break`, `case`, `continue`, `dbg`, `defer`,
`else`, `end`, `enum`, `export`, `extern`, `fn`, `for`, `if`, `impl`,
`import`, `in`, `interface`, `is`, `not`, `once`, `opaque`, `or`, `pub`,
`refute`, `return`, `self`, `struct`, `subtype`, `tap`, `test`, `then`,
`type`, `typealias`, `unreachable`, `when`, `where`, `with`.

One written as a parameter, a binding, a field or a function's name is the
error `` `type` is a keyword (declares a distinct type), so it can't name a
binding; name it something else, such as `kind` ``, which says what the
word is for and offers another name.

`when` stands only before an arm's guard (§10 *Guards*) and a type's rule
(§4 *Subtypes*, §4 *Validated types*). `extern` has no construct:
`extern fn` is the error
``runtime-provided declarations use `host fn`, not `extern fn` ``, and
`extern` before any other declaration is
`` unexpected 'extern'; runtime-provided declarations use `host` ``.

These words have a meaning only where their construct stands, and are
ordinary names elsewhere: `host` (§19), `open` and `derive` (§12),
`embeds` (§4), `on` and `error` (§9), `phrase`, `noun` and `embed` (§18),
`concurrent` (§16), and `group`, `clock`, `boot`, `setup` and `subtests`
(§17), `todo` (§11), and `private` (§14).

`todo` names anything a name can: a parameter, a binding, a field, a
function, a pattern's binding, an import. Read as a value, a bare `todo` is
the placeholder unless a binding named `todo` is in scope, and then it reads
that binding:

```nomi
fn apply(todo: Todo) -> Todo
    todo
end
```

returns its argument. Inside that scope the placeholder is written with a
reason, `todo "apply the patch"`, which is always the placeholder. A
function named `todo` is such a binding throughout its file and in a file
that imports it by name. A module named `todo` (a file `todo.nomi`) is not
a value, so it leaves a bare `todo` the placeholder; `todo.create(...)`
still names its member.

`derive` may name a parameter or a binding, but a statement that begins
with it always starts a declaration. So `derive + 1` is an error
(`` expected interface name after `derive` ``) even inside
`fn f(derive: Int)`.

### Operators and punctuation

| Token | Use |
|---|---|
| `+` `-` `*` `/` `%` | arithmetic (§8) |
| `==` `!=` `<` `<=` `>` `>=` | comparison (§8) |
| `and` `or` `not` | logic (§8) |
| `is` `in` | pattern test and membership (§8) |
| `&&&` `\|\|\|` `^^^` `~~~` `<<<` `>>>` | bitwise operators (§8) |
| `?` `!` | postfix propagate and unwrap (§11) |
| `\|>` | pipe (§8) |
| `->` | function result, lambda body, `case` arm |
| `=>` | map entry |
| `=` | binding and declaration punctuation (below) |
| `..` `..=` | ranges (§3); `..` is also a spread and a rest pattern |
| `.` | field access, `.Variant`, field accessor |
| `::` | joins the segments of a file's path, with no space around it (§14) |
| `&` | a capture, and `&1`, `&2`, ... its parameters (§7) |
| `,` `:` `;` | separators |
| `(` `)` `[` `]` `{` `}` | brackets |

The C-family spellings are errors that name Nomi's: `a && b` is
`` Nomi spells logical and `and`: write `a and b`, not `a && b` ``, and
`||`, `!x`, `<<` and `~` get the same kind of message. `@`, `^` and a lone
`|` are not operators.

`=` matches a pattern only where its left side is a pattern: a local binding,
a destructure, a parameter pattern, `if Pattern = expr`, a binding with
`else` (`Pattern = expr else return 0`) and `assert Pattern = expr`. Its
other uses are punctuation that names or configures a value and matches
nothing: a parameter's or a field's default (`port: Int = 8080`), a `once`
initializer, and `with App.logger = logger`. `=` is never assignment.

### Statement ends

A newline ends a statement where the statement could end. After a token that
cannot end one, such as an operator, `,`, `=` or an opening bracket, the
statement continues on the next line.

A line that starts with a token no expression can start with continues the
line above: `|>`, `)`, `]`, a comparison (`==`, `!=`, `<`, `<=`, `>`, `>=`),
a bitwise operator other than `~~~`, `and`, `or`, `is`, `in` and `where`. So
a long condition breaks before its operator:

```nomi
fn in_grid(rows: Int, columns: Int, top: Int, left: Int, bottom: Int, right: Int) -> Bool
    bottom > top
        and right > left
        and Int.max(bottom, right) < Int.min(rows, columns)
        and Int.min(top, left) >= 0
end

fn main()
    dbg in_grid(5, 5, 0, 0, 3, 3) // True
end
```

A line that starts with an arithmetic operator does not continue, since `-`
also starts an expression: `+ 2` alone on a line is the error
`` `+` cannot start a line: the line break ended the expression before it ``.
The operator goes at the end of the line above.

`;` ends a statement explicitly, so several fit on one line:
`database.save(db, page)?; count + 1`.

A statement that starts right after the end of another, with no space,
newline or `;` between them, is an error at its first token: `"a"[1]` is a
string followed by a list, `` unexpected `[` right after the end of a statement ``.

### Bodies

Code is never written in braces. Every construct that holds statements, a
function, an `if` branch, a `case`'s arms, a lambda, a declaration's body,
is a header line, the body on the lines below it, and `end`:

```nomi
struct Point
    x: Int
    y: Int
end

fn norm(p: Point) -> Int
    if p.x > p.y then p.x else p.y
end

fn main()
    dbg norm(Point{x: 3, y: 4}) // 4
end
```

- A header ends at the end of its line. Brackets carry it across lines, and
  a line that starts with `where` continues it with long bounds, as a line
  that starts with `|>` or a binary operator continues an expression.
- The body starts on the line after its header. Anything but a comment after
  the header on its line is the error
  `` unexpected ...: a function's body starts on the line after its header and ends at `end` ``,
  which names the construct.
- A `{` that ends a header line, where a brace-bodied body would open
  (`fn f() -> Int {`, `if c {`, `case x {`, `struct P {`, `import {`), is
  `` a body is written on the lines after its header and closed by `end`, not in braces ``,
  whose fix removes the `{`. When a file does not parse, this error at its
  first such `{` comes before any other, since the brace is what the
  later errors stem from.
- An empty body is one line: `fn noop() end`, `struct Empty end`,
  `impl Marker for P end`.
- `begin` and `end` around statements are a block expression
  (§8 *Block expressions*).
- An `if` whose `then` has more on its line, an arm whose `->` has more on
  its line and a lambda whose `->` has more on its line are one-line forms
  with no `end` (§8 *`if`/`else`*, §8 *`case`*, §7 *Lambdas*).

Braces and brackets hold data only: struct and map literals, lists, sets,
vectors and their patterns and types (§3). A `{` always begins data.

## 3. Literals

### Integers

Taught in [Scalars & Strings](/scalars-and-strings/#number-literal-forms).

An integer literal is decimal, or hexadecimal, binary or octal after a `0x`,
`0b` or `0o` prefix (either case). A `_` may stand between two digits. Its
type is `Int`, except where a `Byte` is expected (below). It may have any
number of digits, since an `Int` has no largest value (§4).

```nomi
fn main()
    dbg 1_000_000 // 1000000
    dbg 0xFF      // 255
    dbg 0b1010    // 10
    dbg 0o77      // 63
end
```

<!-- OPEN: a leading zero reads the literal as octal: `077` is 63, though octal is written `0o77` and the old text called the C-style form not Nomi's. Decide whether `077` is decimal 77, an error, or octal. -->

`1__0` and `10_` are errors (`invalid integer "1__0"`).

#### Byte literals

Taught in [Scalars & Strings](/scalars-and-strings/#binary-data-with-bytes).

An integer literal written where a `Byte` is expected is a `Byte`. A `Byte`
is expected in:

- an annotated binding or `once`, a `Byte` parameter's argument or default,
  a `Byte` field of a struct literal, and a function's result;
- an element, key or value of a collection or tuple literal whose type is
  expected, and the payload of a generic constructor whose result is
  expected (`Some(0x81)` as an `Option<Byte>`);
- each branch of an `if` or `case` whose value is expected, and an arm
  pattern over a `Byte` (`0x80 -> ...`, `Some(0x80) -> ...`).

With no expected type, a literal element or branch after a `Byte` one is a
`Byte` too (`[b, 0x42]`, `if keep then b else 0x00`). In a comparison and
in `&&&`, `|||` and `^^^`, a literal operand takes `Byte` from the other
operand (`b == 0x80`, `bytes == [0x81, 0x00]`, `b &&& 0x7F`). A generic call
is checked left to right, so in `Iter.map(bytes, fn b -> b &&& 0x7F)` the
literal follows the `Byte` that `bytes` gives `b`.

```nomi
fn tag(b: Byte) -> String
    case b
        0x00 -> "nul"
        0x7F -> "del"
        _    -> "other"
    end
end

fn main()
    bytes: List<Byte> = [0x81, 0x00, 0b0111_1111]
    dbg tag(0x7F) // "del"
    dbg bytes     // [129, 0, 127]
end
```

Only the literal itself, or the literal under a unary minus, takes its type
from its position. `n = 0x81` is an `Int`, and an `Int` value never becomes a
`Byte`: `b: Byte = n` is `type mismatch: expected Byte, got Int`.
`Int.to_byte` converts one.

A `Byte` literal outside 0 to 255 is an error in any spelling:
`` `300` does not fit in a Byte (0 to 255) ``, and the same for `0x100` and
`-1`. `-0` is `` a Byte literal has no sign: write `0` for `-0` ``.

### Floats

Taught in [Scalars & Strings](/scalars-and-strings/#number-literal-forms).

A float literal is digits, a `.` and digits, with an optional exponent
(`e` or `E`, an optional sign, digits). Digits with an exponent and no `.`
are a float too. A `_` may stand between two digits. Its type is `Float`.

```nomi
fn main()
    dbg 3.14          // 3.14
    dbg 2.5e-3        // 0.0025
    dbg 1e10          // 10000000000.0
    dbg 1_000.123_456 // 1000.123456
end
```

Both sides of the `.` need a digit: `.5` and `1.` are not float literals.

### Decimal literals

Taught in [Scalars & Strings](/scalars-and-strings/#exact-arithmetic-with-decimal).

A decimal literal is an integer or a float without an exponent, followed by
`d` or `D`. Its type is `Decimal` (§4), and its text sets its scale: `1.50d`
has scale 2, `1.5d` scale 1, `5d` scale 0. A `_` may stand between two
digits, as in other number literals (`1_000.00d`); `1__0.5d` is
`invalid decimal "1__0.5d"`. An exponent is not allowed.

### String Literals

Taught in [Scalars & Strings](/scalars-and-strings/#string-interpolation-and-concatenation).

A string literal is a `String`. Every form, raw and typed ones included,
holds UTF-8 text: a byte in its body that is not part of a UTF-8 sequence is
a compile error, and no escape writes one.

A quoted string, `"..."`, is one line. It reads these escapes and no others:

| Escape | Character |
|---|---|
| `\n` `\r` `\t` | newline, carriage return, tab |
| `\\` `\"` | backslash, quote |
| `\u{1F600}` | the Unicode scalar value with that hex number: 1 to 6 digits in braces, at most `10FFFF`, not a surrogate (`D800` to `DFFF`) |
| `\${` | a literal `${` |

Any other backslash sequence is a compile error: `\q` is `invalid escape \q`,
a `\u` without braces is ``\u escape requires braces, e.g. \u{1F600}``,
`\u{}` and seven digits are
`invalid \u{...} code point: 0000041; a \u{...} escape takes 1 to 6 hex digits`,
and `\u{110000}` and `\u{D800}` name the limit they break. There is no `\0`
and no `\'`.

`${expr}` interpolates the value of `expr`, which runs when the string is
built. Interpolation calls `Display.to_string` on the value, so its type must
implement `Display` (§12); `"${Unit}"` is
`` no impl of `Display` for `Unit` (required to interpolate it) ``. A `$` not
followed by `{` is text (`$5`, `$$`), and `$${n}` is a `$` followed by an
interpolation. `#`, `#{` and braces are text.

A `$name` is text too, so a quoted or triple-quoted string that writes one
where a value named `name` is in scope (a parameter, a binding or a `once`)
compiles with the warning `` `$name` is text here, not the value of `name`:
Nomi interpolates with `${...}`, not `$` ``, whose help is the literal with
`${name}` in its place; `\u{24}name` writes the same text with no warning.

```nomi
fn main()
    name = "Ada"
    dbg "Hello, ${name}!"            // "Hello, Ada!"
    dbg "\${HOME}" == "$" + "{HOME}" // True
    dbg "costs $5"                   // "costs $5"
end
```

#### Triple-quoted strings

A triple-quoted string, `"""..."""`, spans lines. It reads no escapes, so a
backslash is text, except `\${`, which writes `${`. It interpolates as a
quoted string does. Its body is cut by three rules, in order:

1. A newline right after the opening `"""` is removed.
2. The baseline is the least leading whitespace of every line that is not
   blank and of the closing `"""` line. That much is removed from the start
   of each line. A blank line loses whatever leading whitespace it has, up
   to the baseline.
3. The newline right before the closing `"""` is removed.

```nomi
fn main()
    sql = """
        SELECT *
          FROM users
        """

    dbg sql // "SELECT *\n  FROM users"
end
```

A line indented less than the closing `"""` sets the baseline, and the
lines indented more keep the difference.

### Raw strings

Taught in [Scalars & Strings](/scalars-and-strings/#raw-strings).

A backtick literal, `` `...` ``, is a raw string. Its body is the text as
written: no escapes, so every backslash is text, and no interpolation, so
`${...}` and `\${` are text. A raw string that spans lines is cut by the
three rules of a triple-quoted string. The body ends at the first backtick,
so a raw string cannot hold one.

```nomi
fn main()
    regex = `\d{3,4}`
    template = `price: ${PRICE}`
    dbg regex                          // "\\d{3,4}"
    dbg template == "price: \${PRICE}" // True
end
```

A backtick literal with a type prefix is a typed literal (*Typed literal
forms* below).

### Codepoint literals

Taught in [Scalars & Strings](/scalars-and-strings/#text-model).

A single-quoted literal holding one ASCII character (U+0000 to U+007F) is a
`Codepoint`, the type of a Unicode scalar value. Its type is std's
`Codepoint` whatever the file has in scope, and `Codepoint` is in the
prelude.

```nomi
fn main()
    dbg Codepoint.to_int('a')  // 97
    dbg Codepoint.to_int('\n') // 10
    dbg Codepoint.to_int('\'') // 39
    dbg Iter.count('a'..='z')  // 26
end
```

A codepoint literal is ASCII only because above U+007F one character can be
one codepoint or several (`é` precomposed or decomposed), and a literal would
have to pick one. Text is a `String` (`"é"`); another scalar value comes from
`Int.to_codepoint`.

- It is not text: `"a" == 'a'` is `equality type mismatch: String vs Codepoint`.
- It compares, orders and hashes as a `Codepoint`, and a range of codepoints
  iterates.
- It is a pattern (§10). A `case` on a `Codepoint` whose arms test literals
  needs a `_` arm. There are no range patterns.

A printable ASCII character is written as itself, except `'` and `\`, which
are `'\''` and `'\\'`. A control character (U+0000 to U+001F, U+007F) must be
escaped. The escapes are `\n`, `\r`, `\t`, `\\`, `\'` and `\u{...}` (1 to 6
hex digits, at most `7F`). There is no `\0` (write `'\u{0}'`), and `\"` is
an error (write `'"'`).

These are compile errors, each naming the literal: an empty `''`
(`` empty codepoint literal ''; a codepoint literal holds exactly one ASCII character ``),
more than one character (`codepoint literal 'ab' holds more than one character; write "ab" as a string`),
a character above U+007F (`'é'`, whose message suggests `"é"` or
`Int.to_codepoint`), a control character written directly, an unknown
escape, and a literal with no closing quote on its line.

### Literal forms

Taught in [Collections](/collections/).

A named struct is built by its name, with the `{` against it: `Point{x: 1}`,
as a variant (`Some(1)`) or a distinct type (`UserId(5)`) is built by its
name. A named tuple, a distinct type over a tuple, is built by its name
with the elements as arguments: `Pair(1, "x")`. A tuple is its parentheses
with a comma inside, `(1, "x")` (§4 *Tuples*). A `{` always begins data
(§2 *Bodies*).

Collection literals have no symbol. Brackets, `[...]`, are a list, a vector
or a set; braces, `{...}`, are a map or a struct. A type built on a
collection writes its name in front of the bracket or brace, and so may the
std collections themselves: `Vector[1, 2]`, `Set["a"]`.

| Shape | Unnamed | Named | Pattern |
|---|---|---|---|
| struct | `{x: 1}`, `{x:}` | `Point{x: 1}`, `geo.Point{x: 1}`, `Shape.Circle{radius: 1.0}`, `.Circle{radius: 1.0}` | `{x:, y:}`, `Point{x:, y:}`, `.Circle{radius:}` |
| tuple | `(a, b)` | `Pair(1, "x")` | `(a, b)`, `Pair(a, b)` |
| list | `[1, 2]`, `[]` | `Ids[1, 2]`, `.Items[1, 2]` | `[first, ..rest]`, `[a, b]` |
| vector | `[1, 2]`, `[]` where one is expected | `Vector[1, 2]`, `Path[1, 2]`, `.Steps[1, 2]` | `[first, ..rest]`, `[.., last]`, `Path[a, ..]` |
| set | `[1, 2]`, `[]` where one is expected | `Set[1, 2]`, `Tags["a"]`, `.Tagged["a"]` | `["admin", ..rest]`, `[1, 2]` |
| map | `{"k" => v}`, `{}` | `Kvs{"k" => 1}`, `Json.Obj{"k" => v}`, `.Obj{"k" => v}` | `{"k" => v}`, `{}`, `Kvs{"k" => v}` |

Which collection an unnamed bracket literal is comes from its position, as
the struct a brace literal with no name builds does:

- Where the expected type is a `Vector<T>` or a `Set<T>`, `[...]` is that
  collection, its elements checked against `T`. Where it is a distinct type
  over a list, vector or set (`type Path Vector<Int>`), `[...]` builds that
  type, its elements checked against the collection it wraps; an opaque
  distinct type is built so only in its own file. Anywhere else, a `List<T>`
  or an `Iter<T>` expected or no type at all, it is a list.
- With no collection expected, a literal with spreads is the collection its
  first spread with a type of its own is, when that is a vector or a set:
  with `v: Vector<Int>`, `[..v, 4]` is a `Vector<Int>`. A spread that is
  any other `Iter` leaves it a list.
- An expected type reaches a literal wherever one reaches a dot-leading
  variant (§4 *Variant Resolution*): an annotated binding, an argument (a
  generic one included: `Vector.length([1, 2])` takes a vector), a field, a
  return value, an element of an enclosing literal, and the other operand
  of `==`, `!=`, `+` and `-`.
- `Vector[...]` and `Set[...]` name the collection where no position fixes
  it. A list literal and a map literal have no name in front, since
  `[...]` and `{...}` are those already: `List[1, 2]` is the error
  ``a list literal has no name in front``, with the help ``write `[1, 2]` ``,
  and `Map{"a" => 1}` and `Map{}` are the error
  ``a map literal has no name in front``, with the help
  ``write `{"a" => 1}` `` or ``write `{}` ``.

The first entry of a brace literal says which it is: `name:` opens a struct,
`key =>` a map, and the entries that follow must agree. `{}` is the empty
map, or the empty value of the map type expected; an anonymous struct has at
least one field, so `{}` is never one. A struct update's spread comes first
and its fields after (`{..base, x: 1}`); a brace holding one spread and
nothing else is a struct update, and one holding two or more spreads a map.
`Name{}` is what the name declares: `Point{}` a struct built from its field
defaults, `Kvs{}` the empty map of a type over one.

- A struct literal names every value it gives. A value with no name is an
  error, never a field by position, so reordering a struct's fields cannot
  swap two values of one type: `Student{&1.first, &1.second}` is
  `` a struct literal names its fields; write `Student{name: &1.first, score: &1.second}` ``,
  whose fix writes the names in declaration order. When the values do not
  fit the fields, the error counts them and names the fields instead.
- A named literal always builds the type it names, never the underlying
  shape: `Ids[1, 2]` is an `Ids` even where a `List<Int>` is expected, where
  it is a type mismatch. A name that names no type is an error, and so is a
  name of a type over no list, vector or set before a bracket:
  `` Point is not a type over a list, vector or set; `Point[...]` requires `type Point List<T>`, `Vector<T>` or `Set<T>` ``.
- A capitalized name is always a type or a variant (§2 *Naming
  Conventions*), so `Name[` always opens a literal; Nomi has no indexing
  syntax.
- A field is written `name: value`. A field whose value is a binding of its
  own name is punned, `name:`, which is `name: name`; the name alone is no
  field. `P{a}` is `` a field is written `a: value`, or `a:` for `a: a` ``,
  with the help `` write `P{a:}` ``.
- A name followed by a `{` on its line opens a struct or map literal or
  pattern, in an `if` or `case` head as anywhere else: `case Point{x: 1}`,
  `if p == Point{x: 1} then`.
- A payload variant and a distinct type over a scalar are calls, not
  literals: `Some(1)`, `.Circle(2.0)`, `UserId(5)`.
- Map keys are expressions: `{key => 1}` reads the binding `key`, and a
  string key is quoted.
- `#` and `%` write no literal. `#[1, 2]` is
  `` a vector is written without `#`: `[1, 2]` where a Vector is expected, else `Vector[1, 2]` ``,
  `%[1, 2]` the same for a set, `%{"k" => 1}` is
  `` a map is written without `%`: `{key => value}` ``, and `#Path[1]` or
  `%Kvs{...}` is
  `` a literal or pattern naming its type is written without `#`: `Name[...]`, `Name{...}` ``
  (or `%`); each fix drops the symbol, or writes `Vector` or `Set` for it.
  After an operand, `%` is the remainder operator.
- Types are written without a symbol, except a tuple's and an anonymous
  struct's: `Vector<T>`, `Set<T>`, `Map<K, V>`, `(Int, Int)`, `{x: Int}`. A
  `{` where a type is expected always opens an anonymous struct type.

An empty literal takes its element types from the expected type. With none,
its binding needs an annotation (§5 *Locally-determined bindings and
turbofish*).

A literal evaluates its parts from left to right, each once.

### Spreads

Taught in [Collections](/collections/#spreads).

`..e` inside a literal is a spread: the literal holds the parts of `e`. A
list, vector, set or map literal takes any number of spreads, anywhere among
its elements, and is its parts joined in the order written:

- A list's and a vector's parts are concatenated, and a set's are unioned.
- A map's parts apply left to right: a later key replaces an earlier key's
  value and keeps the earlier key's position. So `{"a" => 0, ..m}` is `m`
  with a default for `"a"`, and `{..m, "a" => 0}` is `m` with `"a"`
  replaced.

```nomi
fn main()
    xs = [2, 3]
    dbg [1, ..xs, 4] // [1, 2, 3, 4]
    s: Set<Int> = [1, 2]
    dbg [..s, 3] // Set[1, 2, 3]
    defaults = {"theme" => "light", "font" => "mono"}
    dbg {..defaults, "theme" => "dark"} // {"theme" => "dark", "font" => "mono"}
end
```

A spread in a list, vector or set literal is any `Iter` of the literal's
elements: a collection, an `Option` (zero or one element), a `Range`, a lazy
`Iter` chain, or a type with an `impl Iter`. Its elements join in the order
it hands them out. A spread that is no `Iter` is
``list spread `..` requires an Iter of the list's elements, got Int`` (or
`vector`, `set`). An error about a spread is reported at its `..`, in every
kind of literal.

Where the literal's collection of an element type is expected, the literal
is that collection, and each spread's elements and each element convert to
the element type as an element of a literal without spreads does. With
`vs: Vector<Int>`, `f: Vector<Display> = [..vs, "a"]` is a
`Vector<Display>`, as `[..xs, "a"]` is a `List<Display>` for a list `xs`
and `[..ss, "a"]` a `Set<Display>` for a set `ss`.

A bracket literal with no expected type is the collection its first spread
with a type of its own is, when that is a vector or a set (§3 *Literal
forms*). Its spreads decide its element type, wherever they are written:
the first spread's element type is the literal's, and each element is
built as that type, as it is under an annotation (§5 *Type argument
conversion*). With `xs: List<Int>`, `["s", ..xs]` is
`list element type mismatch: expected Int, got String` at `"s"`, and a later
spread of other elements, `[..xs, ..words]` with `words: List<String>`, is
`list spread element type mismatch: expected Int, got String` at its `..`
(or `vector`, `set`). An element or later spread of an ancestor of that
type, or of a subtype that shares one with it, joins with it as the
nearest shared ancestor (§4 *Chains and conversion*): with
`ps: List<PositiveInt>` and `n: Int`, `[..ps, n]` is a `List<Int>`, and a
literal element is its base's before it joins, so `[..ps, 0]` is a
`List<NonNegativeInt>`. With
`routes: List<(Route, Handler)>`, ``[(Route`GET /`, home), ..routes]`` is a
`List<(Route, Handler)>` though `home` is a `(Request) -> Response`.

A map spread is a map. Each spread must be a `Map<K, V>`
(``map spread `..` requires a map, got Point``), all of one type
(`map spread type mismatch: expected Map<String, Int>, got Map<Int, Int>`),
and the first spread's type is the literal's. Spreads whose keys or values
differ where a subtype meets an ancestor join as the nearest shared
ancestor (§4 *Chains and conversion*): with `counts: Map<String, Int>` and
`sizes: Map<String, PositiveInt>`, `{..counts, ..sizes}` and `{..sizes,
..counts}` are `Map<String, Int>`s. Each entry's key must be a `K`
and its value a `V`, with the coercions any map literal's entries get from
an expected type: `map spread key type mismatch: expected String, got Int`
(or `value`). With no map expected, an entry of an ancestor joins with the
spreads the same way: `{..sizes, "c" => 0}` is a `Map<String,
NonNegativeInt>`. A base whose types are still open, such as `{}`, takes them
from the entries. An entry may repeat a key of a spread; two equal constant
keys among the literal's own entries are the error of *Repeated keys*.

A list, vector, set or map literal of one spread of its own type, or of a
subtype of it, and nothing else is that spread, since values are immutable
and the copy is the same value: `[..xs]` with `xs` a list is
`` a list spread with no elements is its base value; write `xs` `` (or
`vector`, `set`, `map ... with no entries`). A lone spread the literal
converts is not one. A lone spread of another `Iter`, `Set[..option]` or
`Vector[..xs]` with `xs` a list, builds the collection from it. So does a
lone spread whose elements convert to the expected element type,
`shapes: Vector<Shape> = [..circles]` with `circles: Vector<Circle>`, or
`ns: List<Int> = [..ps]` with `ps: List<PositiveInt>`, and one of a
distinct type's inner type, which builds the distinct type.

The spreads are evaluated first, from left to right, then the elements from
left to right. Each expression runs once.

A spread has no named form, since the base already carries the type:
`Kvs{..base, "a" => 1}` is
`` map spread `..` has no named form; write `{..base, key => value}` — the spread head carries the type ``,
and `Path[..base, 1]` is
`` spread `..` has no named form; write `[..base, item]` — the spread head carries the type ``.
The std collections' own names are the exception, since a spread of
another `Iter` fixes no collection: `Vector[..iter, 1]`, `Set[..option]`.

A struct literal takes one spread, first: that is a struct update (§4
*Struct Updates*). At a field or key of an update a spread may be a bare
`..`, the value already there (§4 *Struct Updates*).

### Repeated keys

Map keys are evaluated at run time, so two entries of one literal can have
equal keys. The map holds that key once: the later entry's value wins, and
the key keeps the earlier entry's position.

```nomi
fn main()
    x = 1
    y = 1
    dbg {x => "a", 2 => "c", y => "b"} // {1 => "b", 2 => "c"}
end
```

When two keys are the same constant, the earlier entry can never be read, so
the literal is a compile error at the second key:
`` key `0x1` is given twice in this map literal; the later entry would replace the earlier ``.
A constant is a value whose equality to another constant is certain at
compile time:

- an `Int`, `Float`, `Decimal`, `String` or `Codepoint` literal, or a
  negated number literal, compared by value: `255` and `0xFF` are one key,
  `1.0` and `1.00` are one, `0.0` and `-0.0` are one, and `1.0d` and `1.00d`
  are one;
- `True` and `False`;
- a variant with no payload (`None`, `Direction.North`, `.North`), or a
  variant constructor applied to constants (`Some(1)`), when the enum has no
  `impl Equatable` of its own, written or derived, and std declares none;
- a tuple of constants (`(1, "a")`).

Any other key, a variable, a call, a struct or a value of a type with its own
`impl Equatable`, is never compared at compile time. The same rule holds for
a set literal (`Set[1, 2, 1]`, or `[1, 2, 1]` where a set is expected, is
`` element `1` is given twice in this set literal; a set holds it once ``)
and for a map pattern
(`` key `"k"` is given twice in this map pattern; match its value once ``).
Set elements equal only at run time make one element:

```nomi
fn main()
    x = 1
    y = 1
    dbg Set[x, y] // Set[1]
end
```

### Range literals

Taught in [Collections](/collections/#ranges).

`a..b` is a half-open range and `a..=b` a closed one. Both operands must have
the same `Comparable` type, and the literal is a `Range<T>` (§4 *Built-in
Types*). A subtype and its ancestor join as a branch does, and a range
with no type written for it ranges over the base (§4 *Chains and
conversion*): `0..List.length(xs)` is a `Range<Int>`. An empty collection
literal in one operand, whole or inside a tuple or list, takes its element
type from the other: `[1]..[]` is a `Range<List<Int>>`. Both bounds are
required: `..5`, `5..` and `..` are not
ranges, since `..` alone is a spread.

A range over a type that implements `Discrete` (`Int`, `Codepoint`) is an
`Iter` of its values, lazy, with no list built. A range over any other type
(`String`, `Float`, `Decimal`) is an interval, used for membership.

`Discrete` and `Steppable<S>` are the prelude interfaces behind this.
`Discrete.next(v)` returns the value after `v`, or `None` past the last, and
a range of a `Discrete` type walks its values with it. `Steppable<S>`'s
`step_by(v, by)` moves `v` by a step of type `S`, and `Range.step_by` walks
a range of a `Steppable` type by a step the caller gives, as
`Range.step_by(0d..1d, 0.25d)` does for `Decimal`.
[std::discrete](/reference/discrete/) and
[std::steppable](/reference/steppable/) list their implementors.

```nomi
fn main()
    dbg Iter.to_list(1..5)     // [1, 2, 3, 4]
    dbg Iter.to_list(1..=5)    // [1, 2, 3, 4, 5]
    dbg 3 in 1..5              // True
    dbg "h" in "a".."m"        // True
    dbg 1.50d in 1.25d..=2.50d // True
    dbg Iter.to_list(5..1)     // []
end
```

Reversed bounds give an empty range. An unbounded range comes from
`Range.from(n)`, of `n`'s type, or `Range.naturals()`, and a step from `Range.step_by`
([std::ranges](/reference/ranges/)). A range's parts are
`Range.start(r)`, `Range.stop(r)` (its end, `None` when unbounded) and
`Range.is_inclusive(r)`.

The prelude holds three subtypes of `Range<T>` (§4 *Generic subtypes*).
`NonEmptyRange<T>` is a range that holds a value, `Range.contains(r,
Range.start(r))`: `a..b` with `a < b`, `a..=b` with `a <= b`, or an
unbounded range. `ClosedRange<T>` is a NonEmptyRange that holds its end or
has none, `a..=b` or `Range.from(n)`, and `BoundedRange<T>` is a
NonEmptyRange with an end. A function whose two bounds must agree takes one
of them, so a literal written backwards is an error when the program is
compiled, with the rule that is false:
``this ClosedRange<Int> literal breaks the rule of NonEmptyRange<Int>, a supertype of it: `fn r -> Range.contains(r, Range.start(r))` is false``.
`Comparable.clamp(value, 0..=11)` takes a ClosedRange (§5),
`random`'s `Generator.int(1..=6)` takes a BoundedRange, and
`Generator.float(0.0..1.0)` takes a `FiniteRange`, a bounded range whose
ends are finite. `Range.from(n)` and
`Range.naturals()` are ClosedRanges: where a `ClosedRange<NonNegativeInt>`
is expected, `Range.from(2)` is one, and its literal `2` is checked as a
NonNegativeInt when the program is compiled. A Float range with a NaN end holds no
value, since no Float is `<=` NaN, so it is none of the three.

`..` binds tighter than comparison and the pipe, and looser than arithmetic:
`1 + 2..5 + 6` is `(1 + 2)..(5 + 6)`.

### Typed literal forms

Taught in [Typed Literals](/typed-literals/#the-shape).

A string literal with a type name written against its opening delimiter is a
typed literal: `Date"2026-05-04"`, `Sql"""..."""`, `` Regex`\d+` ``. The
name is PascalCase, with no space before the delimiter. A lowercase name
against a string is a name followed by a string, which is an error. What a
typed literal means, and when its handler runs, is §18. The delimiters
differ only in escapes and interpolation, as for an untyped string: a
quoted body reads escapes and `${...}` slots, a triple-quoted one reads
slots and no escapes, and a backtick body is raw text with no slots. A
literal without slots is checked at compile time in any of the three, and a
failure is a compile error at the literal.

```nomi
// quoted: escapes and slots
Date"2026-05-04"

// triple-quoted: slots, no escapes
Date"""
2026-05-04
"""

// backtick: raw text
Regex`\d{3,4}`
```

## 4. Types

Every declared type is named. Four keywords declare types:

- `struct` declares a nominal record type with named fields (*Structs*).
- `enum` declares a nominal sum type (*Enums (Algebraic Data Types)*).
- `type` declares a distinct type over another type, or a zero-sized type
  (*Distinct Types*).
- `typealias` declares another name for an existing type (*Type Aliases*).

A type body holds only its fields or variants. Functions and `once` values a
type owns go in an inherent `impl Type` block, interface implementations in
`impl Iface for Type` blocks, and derived ones in
`derive Iface for Type` declarations, all at top level beside the type
(§12).

A type's name may be a dotted PascalCase path when the type belongs under a
parent type: `type Civil.Days Int` declares the type `Civil.Days`. It is a
declaration of its own, not an item inside `Civil`, and `Civil` must be in
scope. Construction, destructuring, derives, owner calls and impls all use
the full name, which is the type's identity.

```nomi
struct Civil end

type Civil.Days Int

fn days_value(days: Civil.Days) -> Int
    Civil.Days(n) = days
    n
end

fn main()
    dbg days_value(Civil.Days(3)) // 3
end
```

### Primitive Types

Taught in [Scalars & Strings](/scalars-and-strings/#numbers-booleans-operators).

The primitive types are `Int`, `Float`, `Decimal`, `Bool`, `String`,
`Codepoint`, `Byte` and `Unit`. Each implements `Display` and `Debug`
except as noted (§12).

**`Int`** is the one integer type: signed and exact at any size, with no
largest or smallest value. There are no sized or unsigned variants.
Arithmetic never overflows; the value grows (§8). Where an `Int` meets a
fixed number of bits (a host function, a database column), a
value that does not fit is an error there. The bitwise operators work on
`Int`s of any size (§8).

**`Float`** is IEEE 754 binary64, including its special values (*Float
Special Values*).

**`Decimal`** is an exact base-10 number of any precision, a mantissa and a
scale. It takes part in the arithmetic and comparison operators like `Int`
and `Float`.

- `+`, `-` and `*` never round; the scale grows as needed
  (`1.50d * 1.5d == 2.250d`). Rounding happens only in functions that name a
  `RoundingMode` ([std::decimal](/reference/decimal/)). There is no global
  precision or rounding context.
- `/` rounds a quotient that does not end to 34 significant digits,
  half-even (`10d / 3d` is `3.333333333333333333333333333333333d`), and an
  exact quotient keeps its smallest scale (`10.00d / 4d` is `2.5d`). §8
  *Decimal arithmetic* gives the rule.
- `Decimal` has no NaN and no infinities. `/` and `%` take a
  `NonZeroDecimal` divisor, as `Int`'s take a `NonZeroInt`, so a divisor
  that may be zero is a compile error (§8 *Decimal arithmetic*).
- The scale shows in display (`1.50d` prints `1.50`) but not in equality:
  `==`, `Comparable`, `Hashable` and patterns compare by value, so
  `1.50d == 1.5d` and the two are one map key.

**`Bool`** is the enum of `True` and `False`. std::bool declares `True` and
`False` as types of their own that `Bool` embeds; imported by those names
(`import std::bool.{False, True}`), each is still `Bool`'s variant as a
value, as it is through `std::bool.Bool.{False, True}` and the prelude.
Written as any enum's variant is, `Bool.True` and `Bool.False` (or
`bool.Bool.True` through the module) are the same values, in an
expression and in a pattern.

**`Unit`** is the type of a function with no meaningful result, and has one
value, `Unit`. A function with no `->` result returns `Unit`. `Unit` works as
a type argument (`Result<Unit, String>`). It implements `Equatable` and
`Hashable`, and neither `Display` nor `Comparable`.

**`Infallible`** has no values. `Result<T, Infallible>` is a result that is
never an `Err`. It implements `Display`, `Equatable` and `Hashable`, and
std/http's `ToResponse`, whose functions can never be called, so a bound
such as `where T: Hashable` accepts a `Result<Int, Infallible>`. It is what
a lambda's result leaves open against an interface (§7 *Lambdas*).

**`Codepoint`** is a Unicode scalar value, with a literal for ASCII (§3
*Codepoint literals*). std declares it as an opaque validated type,
`pub opaque type Codepoint Int64 when ...` (*Validated types*), whose rule
admits `0` through `0x10FFFF` except the surrogates `0xD800` through
`0xDFFF`, so every Codepoint is one: `Int.to_codepoint(n)` answers `None`
for an Int the rule refuses. There is no character type: one character of text is a
`String` of length 1.

**`Byte`** is a value from 0 to 255, and `Bytes` an immutable byte sequence,
std's type for binary data. `Byte` is not a number: it has no
arithmetic (`b + 1` is ``no impl of `Add` for `Byte` (required via add operator `+`)``),
and `Byte.to_int` and `Int.to_byte` convert. An integer literal where a
`Byte` is expected is a `Byte` (§3 *Byte literals*). `Bytes + Bytes`
concatenates.

### Strings

Taught in [Scalars & Strings](/scalars-and-strings/#text-model).

A `String` is UTF-8 text, always. It is a sequence of graphemes, the
characters a reader sees: `String.length` counts graphemes, and a `String`
iterates by grapheme. Its bytes and codepoints are reached through
`String.to_bytes` and `String.to_codepoints`.

```nomi
fn main()
    dbg String.length("café")               // 4
    dbg Iter.to_list("café")                // ["c", "a", "f", "é"]
    dbg String.length("👨‍👩‍👧")                 // 1
    dbg Iter.count(String.to_bytes("café")) // 5
end
```

Equality, hashing and ordering compare the UTF-8 bytes, with no
normalization; byte order is codepoint order. A precomposed `"é"` (U+00E9)
and a decomposed `"e"` followed by U+0301 are one grapheme each and are not
`==`. `String.normalize` converts between forms. The functions over strings
are in [std::strings](/reference/strings/).

### Float Special Values

A `Float` may be NaN, +Infinity or -Infinity. They are values of `Float`,
produced by IEEE 754 arithmetic and by `Float.nan()`,
`Float.positive_infinity()` and `Float.negative_infinity()`.

`Float` division by zero does not trap: it gives the IEEE value. `Int`
division takes a `NonZeroInt` divisor, since every `Int` is a number and
none can stand for the missing result, so a divisor that may be zero is a
compile error (§8 *Integer division and remainder*).

```nomi
fn main()
    dbg 1.0 / 0.0               // +Inf
    dbg -1.0 / 0.0              // -Inf
    dbg Float.is_nan(0.0 / 0.0) // True
end
```

NaN and the infinities pass through arithmetic and rounding as IEEE 754
says. `-0.0` and `0.0` are two values that are `==`. How `==`, `compare` and
`<` treat NaN is §5 *Equality and Ordering Semantics*. No `Float` function
traps on a NaN operand. The functions over floats are in
[std::float](/reference/float/).

### Built-in Types

Taught in [Collections](/collections/).

The built-in types are immutable: every operation on a value returns a new
value.

- `Option<T>`, `Some(value)` or `None`, and `Result<T, E>`, `Ok(value)` or
  `Err(error)`, are enums (§11).
- `List<T>`, `Vector<T>`, `Map<K, V>` and `Set<T>` are the collections
  (below).
- `Range<T>` is a range of a `Comparable` type, written `a..b` or `a..=b`
  (§3 *Range literals*). It is opaque: its parts are reached through
  [std::ranges](/reference/ranges/). It implements `Iter<T>` when `T`
  implements `Discrete`.
- `(A, B, ...)` is a tuple and `{name: A, ...}` an anonymous struct
  (below).
- `(A, B) -> C` is a function type (§7).

The functions over each type are in the standard library reference:
[std::lists](/reference/lists/), [std::vectors](/reference/vectors/),
[std::maps](/reference/maps/), [std::sets](/reference/sets/),
[std::ranges](/reference/ranges/) and [std::iter](/reference/iter/).

#### Tuples

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#tuples).

A tuple is written `(a, b, ...)`: fixed-size, positional, of mixed types,
with at least two items. Its type is written the same way, `(Int, String)`,
and so is its pattern. Its items are read by ordinal name, counting from
one as captures do: `t.first`, `t.second`, `t.third`, ... up to
`t.twentieth`. A tuple type is structural: two tuples of the same item types
have the same type.

```nomi
fn divide(x: Int, y: NonZeroInt) -> (Int, Int)
    (x / y, x % y)
end

fn main()
    (q, r) = divide(10, 3)
    dbg (q, r)              // (3, 1)
    dbg divide(7, 2).second // 1
end
```

A named tuple, a distinct type over a tuple (§4 *Distinct Types Over
Tuples and Maps*), reads its items the same way: `Pair(1, "x").first` is
`1`. Only a tuple has ordinal names; a struct's field called `first` is an
ordinary field.

Items are never read by number. `t.0` is the error
`` tuple items are read by ordinal name, not by number: write `t.first` ``,
whose fix writes the ordinal, and `t.1.0` is the same error for
`t.second.first`. An item past the twentieth has no name: `t.20` is
`` tuple items are read by ordinal name, `.first` to `.twentieth`, and item 21 has none ``,
and such an item is bound by a pattern (`(a, b, ...)`). A tuple that wide
is better written as a struct. An ordinal past a tuple's last item is
`` (Int, Int) has 2 items, so it has no `third`; its last is `second` ``.

A comma at the top level of parentheses makes a tuple. Without one,
parentheses only group, in an expression, a pattern and a type: `(x)` is
`x`, and `((Int))` is `Int`. `(1,)` is
`` a tuple has at least two elements; `(x)` is grouping. Use a struct or distinct type if you need a wrapper ``,
and `()` is
`` `()` is no value: a tuple has at least two elements, and the empty value is `Unit` ``.
A trailing comma is allowed after a tuple's last item, as in a list.

A tuple passed as a call's one argument is written in its own parentheses,
`f((a, b))`. In a type, `(A, B) -> C` is a function of two parameters, and a
function that takes one tuple is `((A, B)) -> C`: a `->` after the `)` makes
the parentheses a parameter list. `() -> C` takes no parameters; there is no
empty tuple.

A tuple has no `#`: `#(1, 2)`, as a value, a pattern or a type, is
`` a tuple is written without `#`: `(a, b)` ``, whose fix drops the `#`.

#### Anonymous Structs

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#anonymous-structs).

An anonymous struct is written `{...}` with named fields and no type name.
A field may be punned: `{name:}` is `{name: name}`. Its type is its
shape, written as the value is (`{morning: String, night: String}`), and
may stand in any type position: a parameter, a field, a type argument, a
tuple item, a `typealias`.

Two anonymous struct types are the same when their fields have the same
names and types, in any order. There is no width subtyping: a parameter of
type `{x: Int}` takes exactly that shape, not one with more fields
(`argument 1: expected {x: Int}, got {x: Int, y: Int}`) and not a nominal
struct with those fields. A field name shared by two types is not a shared
meaning, so code that works across shapes declares an interface, builds an
anonymous struct at the call, or converts explicitly.

`type Foo {...}` is an error whose message says a type over an anonymous
struct is a struct, declared with `struct Foo`, one field per line, and
`end`. An anonymous struct has at least one field: `{}` as a literal, a type
or a pattern is `an anonymous struct has at least one field`.
A struct whose fields all have defaults is built by its name, `Settings{}`.

#### List

`List<T>` is a linked list, literal `[1, 2, 3]`. `[x, ..xs]` puts `x` in
front of `xs`. A list pattern takes a list apart from the front (§10).

#### Vector

`Vector<T>` is a random-access sequence, indexed from 0. Its literal is
`[1, 2, 3]` where a vector is expected and `Vector[1, 2, 3]` anywhere (§3
*Literal forms*). A bracket pattern takes a vector apart from either end
(§10).

#### Map

`Map<K, V>` maps keys to values, literal `{key => value}`. Any value with
equality can be a key (§5 *Equality and Ordering Semantics*). A map iterates
in insertion order: putting a new key appends it, and putting an existing
key keeps its position. Maps combine with `+` (§8 *Arithmetic operators*).

#### Set

`Set<T>` is a collection of unique values. Its literal is `[1, 2, 3]` where
a set is expected and `Set[1, 2, 3]` anywhere (§3 *Literal forms*). Any
value with equality can be an element, unique under `==`. A set iterates in
first-insertion order, and sets combine with `+` and `-` (§8 *Arithmetic
operators*). A set implements `Equatable` and `Hashable`, not `Comparable`.
A bracket pattern over a set tests membership and never covers every set
(§10).

### Structs

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#structs).

A `struct` body is a list of fields, one `name: Type` per line (or separated
by `;`), with an optional default. Field names are snake_case.

```nomi
struct User
    name: String
    age:  Int
    role: String = "member"
end

fn main()
    user = User{name: "Ada", age: 36}
    dbg user.role // "member"
end
```

A struct must have values: a field without a default may not hold the
struct itself, directly or through other structs, distinct types, tuples
and anonymous structs, unless something on the way can be built without
one (an `Option`, a list, vector, set or map, a function, or an enum variant
that does not hold it), so `struct S` with a field `s: S` is
`` struct 'S' is defined in terms of itself (S.s → S) ``, and an enum every
variant of which holds itself is the same error.

Two fields on one line with no `;` between them, `x: Int y: Int`, are the
error `struct fields are newline- or semicolon-separated`, and a comma
between fields is an error too. The newline rule is for
declaration bodies only: an anonymous struct type, a struct literal and a
struct-shaped variant (`Card {rank: Int}`) keep commas.

#### Default Values

A field may have a default, which construction uses when it leaves the field
out. A field without a default must always be given. A default expression
runs once per construction that omits its field.

A default has no function to exit, so a `?`, `return`, `break`, `continue`
or assertion written directly in it is an error at that token:
`` `?` cannot be used in the default of field 'port' of Config: a default has no function to return from ``.
An exit inside a lambda the default builds belongs to the lambda.

#### Field types from defaults

A field with a default may leave out its type: `misses = 0` is an `Int`
field. Its type is the default's, checked with no expected type, so it is
known when nothing in the default is left open: a literal, a typed literal
with its tag (`Route"GET /admin/{$}"` is a `Route<Unit>`), a struct
literal or a constructor call, a call (a function's declared result), a
non-empty collection literal, an operator over such values, or a read of a
`once` or of another field whose type is known. An untagged string literal
is a `String`, even where the field is meant to hold a checked literal.

```nomi
struct Game
    word: String
    misses = 0
    guessed: Set<String> = []
end

fn main()
    game = Game{word: "nomi"}
    dbg game.misses + 1 // 1
end
```

A default whose type only an expected type tells (`[]`, `Set[]`, `{}`, `Vector[]`,
`None`, a `.Variant`, or a generic call nothing fixes the type arguments of)
is an error at the field, with help that writes the annotation:
`` can't tell this field's type from `Set[]` ``, with
``write `guessed: Set<_> = Set[]`, with the type for each `_` ``. A default
that reads its own field's type, directly or through other such fields, is
`` can't tell this field's type from `Bad{}.loop`: it depends on a type that comes from this default ``.

The inferred type is part of the struct's interface, as a written one is:
another file reads it, and a public struct's field may not take a private
type from its default (`public struct field exposes private type Secret`).
`derive FromJson` decodes each field by its written type, so it refuses a
field without one:
`` `derive FromJson for Cfg` needs field 'port' to have a written type ``.

A struct-shaped variant's fields take defaults the same way. A positional
payload and a bare variant cannot: `Circle Float = 3.14` is
`` a positional variant payload can't have a default; name the field to give it one: `Circle {<name>: Float = <value>}` ``,
and `North = 1` is
`` enum variant `North` carries no value, so it can't be assigned one ``.

#### Optional Fields

A field whose value may be absent has type `Option<T>`. That is independent
of a default, which only spares the caller from writing a value:
`nickname: Option<String> = None` is optional and defaults to absent.

#### Construction

A struct literal names each field: `User{name: "Ada", age: 36}`. A field
whose value is a binding of the same name may be punned: `User{name:, age:}`
is `User{name: name, age: age}`, and punned and named fields mix. A struct
has no positional construction. The name must be a struct's (or a
struct-shaped variant's, `Shape.Rect{...}`): `Context{}` is `` `Context` is
a host type, not a struct, so a struct literal cannot build it ``, and an
enum, a distinct type or an interface is the same error.

A struct literal names each field at most once:
`User{name: "Al", name: "Bo"}` is
`` field 'name' is given twice in this User literal ``, at the second. The
same holds for an anonymous literal (`this struct literal`), a struct-shaped
variant (`this Rect literal`), the fields after a spread and a
`Struct.update` patch. A field after a spread may repeat a field of the
base.

A struct also has a call form: the type name applied to one anonymous struct
value whose fields match, with defaulted fields optional. A generic struct
infers its type arguments from it.

```nomi
struct Box<T>
    v:     T
    label: String = "box"
end

fn main()
    b = Box({v: 1})                  // a Box<Int>
    dbg b == Box{v: 1, label: "box"} // True
    dbg {v: 2} |> Box()              // Box{v: 2, label: "box"}
end
```

#### Target-typed construction

`{...}`, a struct literal with no type name and no spread, builds the
nominal struct its position expects, as the dot of `.Circle{radius:}`
stands for `Shape`: `{x: 0}` is `Point{x: 0}` where a `Point` is expected.
The positions that supply a struct are the ones that supply an enum to a
`.Variant` (*Variant Resolution*):

- an annotated binding or `once`;
- a struct field in construction, and a function or constructor argument;
- a return value or block tail;
- an element of a typed `List`, `Set`, `Vector` or `Map` literal;
- a `case` or `if` arm whose result has an expected type;
- an operand of `==`, `!=`, `<`, `>`, `<=` or `>=`, against the other
  operand's type;
- a generic position once its type argument is known
  (`b: Box<Int> = {v: 3}`, `Some({...})` under `Option<Address>`), the
  enclosing function's own type parameters among them (`Some((x, {...}))`
  under `Option<(T, Box<T>)>` builds a `Box<T>`).

```nomi
struct Address
    street: String
    city:   String
    zip:    String = "00000"
end

struct Person
    name:    String
    address: Address
end

fn home() -> Address
    {street: "1 Main", city: "Bath"}
end

fn main()
    ada = Person{name: "Ada", address: {street: "3 Main", city: "York"}}
    dbg home().zip       // "00000"
    dbg ada.address.city // "York"
end
```

The literal follows the ordinary construction rules and builds exactly what
`Address{...}` builds. Errors name the struct:
`missing field 'street' of Address`, `field 'city' of Address: expected String, got Int`,
`Address has no field 'zip'`. An opaque struct is built this way only in its
defining file.

Where no struct type is expected, `{...}` is an anonymous struct. It stays
anonymous where the expected type is an anonymous struct type, an interface,
a type parameter or an enum, since none names one struct to build. Against
an anonymous struct type, each field's value is checked with that field's
type expected, as a tuple literal's items are: in
`r: {b: Byte, v: Vector<Int>, p: Point} = {b: 66, v: [3], p: {x: 2}}` the
fields are a `Byte`, a `Vector<Int>` and a `Point`. An
interface takes the anonymous struct when the struct implements it:
`a: Display = {street: "x", city: "y"}` holds an anonymous struct, which
has `Display` when each field does (§12), and
`a: Comparable = {street: "x", city: "y"}` is
`type mismatch: expected Comparable, got {street: String, city: String}`.
An enum takes none: `c: Color = {street: "x"}` is
`type mismatch: expected Color, got {street: String}`.

#### Struct Updates

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#updating-a-struct).

A struct update builds a new struct from an old one with some fields
replaced. It is written as a literal, `{..base, field: value}`, or as a
call, `Struct.update(base, {field: value})`. The two admit the same fields
and the same nested `..` literals, and mean the same thing.

```nomi
struct Point
    x: Int
    y: Int
end

fn main()
    p = Point{x: 1, y: 2}
    dbg {..p, x: 9}              // Point{x: 9, y: 2}
    dbg Struct.update(p, {y: 0}) // Point{x: 1, y: 0}
end
```

The literal form takes exactly one spread, written first, and at least one
field:

- `{..p}` is `` a struct spread with no fields is its base value; write `p` ``.
- Two spreads, or a spread after a field, is
  `` struct spread `..` must be the first element ``.
- A named form, `Point{..p, x: 1}`, is
  `` struct spread `..` has no named form; write `{..base, field: value}` — the spread head carries the type ``.

The result has the base's type: a nominal struct gives that struct, an
anonymous struct an anonymous struct. The fields are checked as a struct
literal's are, against the base's field types, except that a missing field
is not an error. A punned field works after a spread: `{..base, name:}`.

`Struct.update(original: self, updates: Partial<self>) -> self` is the one
function of `Struct`, the interface every struct, named or anonymous,
satisfies; `Struct` is in the prelude. `Partial<self>` is an anonymous struct
whose fields are a subset of the target's. An unknown field
(`User has no field 'bogus'`) and a field of the wrong type
(`field 'age' of User: expected Int, got String`) are errors. A patch held in
a binding is checked by its type, and its fields must be whole values.
`Struct.update` takes a patch as a value, which the literal form cannot.

**Nested updates.** At a field or key of an update, a bare `..`, with no
base written, stands for the value already there. A literal holding one is
that value with the changes written. A literal without one is a whole new
value that replaces the field.

```nomi
struct Address
    street: String
    city:   String
end

struct Person
    name:    String
    address: Address
end

fn main()
    ada = Person{name: "Ada", address: Address{street: "1 Main", city: "Bath"}}
    moved = {..ada, address: {.., city: "York"}}
    dbg moved.address // Address{street: "1 Main", city: "York"}
end
```

`{..ada, address: {city: "York"}}` is therefore a whole `Address` with no
`street`, the error `missing field 'street' of Address`, whose help suggests
`{.., city: "York"}`. A diagnostic names the type the mistake is in:
`{..ada, address: {.., zip: 1}}` is `Address has no field 'zip'`.

A `..` is also an operand anywhere in a field's or key's new value, and it
is then the value already there, with the field's type: `visits: .. + 1`,
`name: String.trim(..)`, `tags: .. |> Iter.filter(keep) |> Iter.to_list()`.
It belongs to the innermost field or key whose value holds it, so in
`address: {.., street: rename(..)}` the first `..` is `address` and the one
in `rename(..)` is `street`. It keeps that meaning inside a lambda written in
the value (`street: Option.map(m, fn s -> s + ..)`). The update's base is
evaluated once, however many `..`s read it.

```nomi
struct Address
    street: String
    city:   String
end

struct Account
    owner:   String
    visits:  Int
    address: Address
end

fn main()
    a = Account{owner: " Ada ", visits: 2, address: Address{street: "Main", city: "Bath"}}
    b = {..a, owner: String.trim(..), visits: .. + 1, address: {.., street: .. + " St"}}
    dbg b // Account{owner: "Ada", visits: 3, address: Address{street: "Main St", city: "Bath"}}
end
```

A `..` is an operand when what follows it is not one: `)`, `,`, `]`, `}`, a
binary operator, `|>` or a line end. `..e` at the start of a literal's
element is still a spread, and `a..b` still a range; a range needs both
bounds and a call takes no spread, so `f(..)` means only this. A field whose
whole value is `..` is
`` a field or key whose value is `..` keeps the value it holds; leave it out ``.
A `..` operand outside a field's or key's value in an update, such as
`x = .. + 1` or in a named literal `Address{street: .., ...}`, is
`` `..` means the value already at a field or key of an update, and this `..` is not in one ``.

The `..` is the one way to write the value already there. A nested struct
update whose spread names that value as a path, the enclosing update's base
followed by the field it is written at, is
`` `..ada.address` is the value `address` already holds here: write `..` ``:
`{..ada, address: {..ada.address, city: "York"}}` is an error, and so is
`{..ada, address: {.., geo: {..ada.address.geo, lat: 1}}}`, where the
enclosing `..` is `ada.address`. The same holds in the literal patch of
`Struct.update(ada, {...})`. Paths compare by the bindings they name, not
their text. A spread naming another value (`{..bob.address, city: "York"}`)
is a value like any other. A base that is not a binding or a chain of field
reads of one, such as a call (`{..load(), address: {..load().address, ...}}`),
has no such path, since a `..` would not run the call again. An operand
that reads the field by its path, `street: rename(ada.address.street)` in
an update of `ada.address`, means the same as `rename(..)` and is no error;
the editor marks it with a hint that `..` writes it.

| At a field or key holding | Write | Means |
|---|---|---|
| a struct | `{.., f: v}` | the struct with `f` replaced |
| a map | `{.., k => v}` | `Map.merge(old, {k => v})` |
| a list | `[x, ..]` or `[.., x]` | `[x, ..old]` or `[..old, x]` |
| a set | `[.., x]` | `Set.union(old, Set[x])` |
| a vector | `[.., x]` or `[x, ..]` | `old` with `x` after, or before |

The literal is the collection the field or key holds, as any bracket
literal is the one its position expects. A bare `..` may stand anywhere a
spread may, beside other spreads, and a literal takes one: a second is
``list literal takes one bare `..` `` (or `map`). A literal whose `..` is followed by nothing
(`[..]`) is
`` an update with nothing after `..` changes nothing; leave this field or key out ``.

A bare `..`, as a spread or an operand, is legal only in the value of a
field or key of a literal that is itself an update: one with a named spread
(`{..base, ...}`), one with a bare `..`, or the literal
patch of a `Struct.update` call. Anywhere else, at the top of a plain
literal, in a list element, in an operand of `+` or `Map.merge`, in a call
argument (`Some({.., ...})` included) or in a patch passed to another
function, it is `` a bare `..` has no value to update here ``.

A `..` must stand for a value of its literal's kind: `{.., f: v}` at a list
field is `` struct spread `..` requires a struct, got List<Int> ``. An
`Option` field has no `..` form, since it may hold no value; it takes a whole
value: `{..p, a: {.., city: "x"}}` with `a: Option<A>` is
`` struct spread `..` requires a struct, got Option<A> ``. A field of interface type takes values only:
`{..stage, who: {.., n: 2}}` is
`` struct spread `..` requires a struct, got Speaker ``. A field whose type
is a type parameter takes a `..` literal once the argument is known.

In a map update the base may lack an entry's key. A `..` there stands for
nothing, and the literal builds the value: an empty collection with the
entries written, or a struct from the fields written and its defaults.
Positions inside a missing value are missing too. A struct whose left-out
field has no default is a compile error, never a trap:
`` a missing key leaves `..` no Geo to update, and field 'city' has no default ``.
A `..` operand there is the empty collection when the value is one
(`{..lists, "a" => .. + [1]}`), and otherwise an error, since there is no
value to read: `{..counts, "a" => .. + 1}` is
`` a missing key leaves `..` no Int to read ``, whose help suggests
`Map.update(m, key, default, f)`.

```nomi
fn main()
    team = {"home" => {"city" => "Atlanta"}}
    moved = {..team, "home" => {.., "arena" => "State Farm"}, "away" => {.., "city" => "Leeds"}}
    dbg moved // {"home" => {"city" => "Atlanta", "arena" => "State Farm"}, "away" => {"city" => "Leeds"}}
end
```

`m + {k => v}` and `Map.merge` stay shallow: a bare `..` in their operands
has no value to update.

Every struct satisfies `Struct` by its shape, and no other type does.
Requiring `Struct` of another type, as `Struct.update(5, ...)` or a
`where T: Struct` bound does, is a compile error
(`Int does not implement Struct`). `Struct` works as a bound and as an
interface value. It cannot be implemented: `impl Struct for P` is
`` `Struct` is a built-in structural marker and cannot be implemented; every struct satisfies it automatically — remove this `impl Struct for P` ``.

An opaque struct satisfies `Struct` too. What opacity closes is a patch that
names one of its fields outside its defining file, for a `Counter`
declared in `counter.nomi`
``cannot update field 'value' of opaque type 'Counter' outside `counter.nomi` — use an exported constructor function``,
through any depth of nesting. Replacing an opaque field whole names none of
its fields and is legal.

### Enums (Algebraic Data Types)

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#enums).

An `enum` body lists its variants, one per line. There is no `|` and no
single-line form. Variant names are PascalCase and unique within the enum.

```nomi
struct Circle
    radius: Float
end

enum Shape
    embeds Circle
    Rectangle {width: Float, height: Float}
    Segment (Int, Int)
    Point
end
```

#### Variant Kinds

A variant is one of four kinds:

- A **bare variant**, a name alone: `Point`, `None`.
- A **single-payload variant**: a name and one type expression, with no
  parentheses, as a distinct type's declaration writes it: `Some T`,
  `Milliseconds Int`, `Segment (Int, Int)`, `Computation (Int) -> Int`.
  Several values go in a tuple, a struct or a struct variant;
  `Rectangle Float, Float` is a parse error.
- A **struct variant**: a name and named fields in braces, with optional
  defaults: `HttpError {status: Int, retryable: Bool = False}`.
- An **embedded type**: `embeds Name`, a struct or distinct type declared on
  its own (*Embedded Types*).

#### Construction and Typing

A variant constructor makes a value of the enum type. There is no separate
variant type: `Some(42)` is an `Option<Int>`, and `Animal.Dog("Rex")` an
`Animal` with no annotation.

A variant is written qualified, `Shape.Circle(5.0)`, or dot-leading,
`.Circle(5.0)`, where the expected type is its enum (*Variant Resolution*).
A bare variant name is an error even in its own file:
`variant 'Red' must be qualified through its enum; use 'Color.Red'`. The
prelude's `Some`, `None`, `Ok`, `Err`, `True` and `False` are bare names
through ordinary name resolution; `.Ok(x)` resolves too. A user enum cannot
join the prelude, but an owner selector import lifts chosen variants into
bare scope (`import shape.Shape.{Circle, Point}`, §14).

Each kind builds as its shape does:

```nomi
enum Shape
    Circle Float
    Rectangle {width: Float = 2.0, height: Float}
    Point
end

fn main()
    a = Shape.Circle(1.0)
    b = Shape.Rectangle{height: 3.0}
    c = Shape.Rectangle({width: 1.0, height: 1.0})
    d: Shape = .Point
    dbg [a, b, c, d] // [Circle(1.0), Rectangle{width: 2.0, height: 3.0}, Rectangle{width: 1.0, height: 1.0}, Point]
end
```

A single-payload variant over a tuple also has a flat call:
`Shape.Segment(1, 2)` builds from the elements, a pipe's head is the first
(`2 |> Shape.Segment(3)`), and a generic variant's type arguments are
solved from them. A struct variant has no positional form:
`Shape.Rectangle(4.0, 5.0)` is
`` Shape.Rectangle is a struct-shaped variant and has no positional form; construct it with Shape.Rectangle{...} or Shape.Rectangle({...}) ``.
A struct variant's paren pattern matches the anonymous struct of its fields,
so `.Rectangle(r)` binds that record.

A list-, map-, vector- or set-payload variant written as a literal
(`Bag.Items[1, 2]`, `Named{"a" => 4}`) infers a generic enum's type
arguments from its elements, as an empty one takes them from the expected
type. A variant lifted into bare scope by an owner selector infers them as
the qualified spelling does: after `import shapes.Holder.{Items, Of}`,
`Of{value: 1}` and `Items[1, 2]` are each a `Holder<Int>`.

A variant's name never takes the place of another name in its file. Since a
same-file variant is never referenced bare, a variant may share its name
with a type, interface or function the file declares or imports, and the
bare spelling means that other name. The variant stays reachable as
`Enum.Variant`, `.Variant` and in patterns.

#### Variant Resolution

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#dot-leading-shorthand).

A `.Variant` expression, in any spelling (`.V`, `.V(x)`, `.V{...}` for a
struct-shaped variant or a map payload, `.V[...]` for a list, vector or set
payload), resolves to variant `V` of enum `E` when
the expected type at its position is `E`. The expected type is the one the
checker's outside-in walk finds, from the position up to the enclosing
declaration. The walk passes through:

- `case` and `if` arm results, block tails and `return` values;
- bracket, tuple and map literal elements, including the heads before a
  spread, and the field values of an anonymous struct literal where an
  anonymous struct type with those fields is expected
  (`a: {c: Color} = {c: .Green}`);
- an argument of a generic function or constructor whose type parameter the
  call's return type pins against an outer expected type, the head of a pipe
  into such a call included. The constraint is solved before the argument
  holding the variant is checked, for the type parameters the arguments
  before it left open (§5 *Type arguments from the arguments*), so it
  reaches a variant nested anywhere in it, and through an `if` or `case`
  argument's branches;
- an operand of `==`, `!=`, `<`, `>`, `<=` or `>=`, which is checked against
  the other operand's type (`k == .Deposit`, `.Deposit == k`);
- a lambda's result, checked against the callback's declared result
  (`Iter.sort_with(xs, fn a, b -> .Equal)`), unless that result holds a type
  parameter;
- a pattern, whose expected type is the scrutinee's (§10).

A bracket literal checked against `Iter<T>` checks each item
against `T`. The expected type may name the enclosing function's own type
parameters: in `fn write<T>(b: Buffer<T>, item: T) -> Result<Buffer<T>, Error>`,
`Err(.Full)` takes `E` as `Error`, and in `fn pop<T>(...) -> Option<(T, Stack<T>)>`,
`Some((x, {items: []}))` takes its payload as `(T, Stack<T>)`, so the literal
builds a `Stack<T>`. A callee parameter is not pinned to a type that names the
callee's own parameters, as a recursive call's expected type would.

The walk does not look at later uses, at unannotated bindings, or at a bare
`.Variant` written as a pipe stage, which would need the type of the stage
after it. A comparison's own expected type is never its operands'.

With no expected type, a bracket or map literal checks its elements
left to right, and each element after the first is checked against the type
the earlier ones give, when that type holds an enum, a named struct, or a
vector, a set or a distinct type over a collection that an unnamed literal
in the element takes (§3 *Literal forms*). A map's keys and values are two
positions. So `[Color.Red, .Green]` is a `List<Color>`,
`{Nucleotide.A => 0, .C => 0}` a `Map<Nucleotide, Int>`,
`[Point{x: 0, y: 0}, {x: 1, y: 2}]` a `List<Point>`, and
`[Set[1], [2]]` a `List<Set<Int>>`. The earlier element's
whole type is what a later one is checked against, so the rule reaches into
nested literals: `[Some(Color.Red), Some(.Green)]` and
`[{c: Color.Red}, {c: .Green}]` resolve. An element whose
type is still open sets nothing and blocks nothing. A tuple's items are separate
positions, so `(Color.Red, .Green)` gives `.Green` nothing.

A `case` or `if` with no expected type does the same for its arms: each arm
after the first is checked against the type the earlier arms give. A
diverging arm (`todo`, `return`) gives nothing.

| Site | Resolves? |
|---|---|
| `Box{color: .Red}`, `paint(.Red)` | yes: the field or parameter type |
| `Iter.sort(xs, .Descending)` | yes: the parameter type; the file need not import it |
| `palette: List<Color> = [.Red, .Green]` | yes: the annotation |
| `palette = [Color.Red, .Green]` | yes: the earlier element |
| `c: Color = id(.Red)` with generic `id` | yes: the annotation pins `T` |
| `exits: Map<Direction, Place> = [(.North, .Cave)] \|> Iter.to_map()` | yes: the pipe's head is checked against the pinned parameter |
| `Ok(.Quit)` in a function returning `Result<Input, String>` | yes: `Ok`'s payload is pinned from the return |
| `c == .Red`, `size < .Large` | yes: the other operand |
| `c = .Red` | no: no expected type |
| `palette = [.Red, Color.Green]` | no: nothing before `.Red` names the enum |
| `c = id(.Red); paint(c)` | no: that would need a later use |
| `... \|> .Guess() \|> Ok()` | no: a bare pipe stage |
| `.Red == .Blue` | no: neither operand names the enum |

The errors:

- No enum at the position:
  `.Red requires a determinable enum type at this position; annotate the binding or qualify the variant`.
  In a literal with no expected type, the help asks to qualify the first
  element, and only the first refused variant is reported. An argument
  whose parameter type is an unsolved type parameter determines nothing:
  in `String.to_int(s) |> Option.to_result(.SyntaxError)?` the message is
  `.SyntaxError cannot be resolved here: nothing at this position determines its enum`,
  with help naming `E.SyntaxError` when one enum in scope declares it.
- The expected type is not an enum:
  `.Red resolves only to enum variants, but the expected type at this position is Box`.
- The enum has no such variant: `no variant 'Purple' on enum Color`.
- Both operands of a comparison are dot-leading:
  `` neither operand of `==` names its enum; qualify one variant with the enum's name (write `E.Red` for `.Red`) ``.

#### Embedded Types

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#embedding-existing-types).

`embeds T` makes a struct or distinct type `T`, declared on its own, a
variant of the enum. One type may be embedded in several enums, and it stays
a type of its own whether or not an enum embeds it.

Only a struct or a distinct type embeds. An interface is
``embeds requires a struct or distinct type, got interface Display; for an interface-typed payload use a single-payload variant: `Name Display` ``
and an enum, directly or through a `typealias`, is
`` `Shape` is an enum, and an enum cannot be embedded in another enum ``;
carry an enum in a single-payload variant (`Shape Shape`). A distinct type
over an enum embeds.

**Subtype coercion.** An embedded type is a subtype of its enum. A value of
the embedded type is accepted wherever the enum is expected: an argument, an
annotated binding, a field, a return, a collection element, and a tuple
item, record field or type argument of any of those that converts by its
variance (§5 *Covariant conversion*): `List<Circle>` where `List<Shape>`
is expected, `Option<Circle>` where `Option<Shape>` is. An invariant type
argument takes no embedded type: a `Cell<Circle>` whose fields both hold
and take a `T` is no `Cell<Shape>`. A construction in such a position
builds the expected type. A type parameter an argument solved to an
embedded type widens to the enum when a later argument is a value of the
enum (`pick(c, circle, shape)` is a `Shape`), only where every earlier
argument still converts to it: with `fn apply<T>(f: (T) -> Int, x: T)`
and `area` a `(Circle) -> Int`, `apply(area, shape)` is
`argument 2: expected Circle, got Shape`, since `area` would be called
with a shape that may be no circle. A later collection literal widens it
too: `List.concat([circle], [shape])` is a `List<Shape>`.

```nomi
struct Circle
    radius: Float
end

enum Shape
    embeds Circle
    Point
end

fn area(c: Circle) -> Float
    3.0 * c.radius * c.radius
end

fn total(s: Shape) -> Float
    case s
        .Circle{} -> area(s) // s is a Circle in this arm
        _         -> 0.0
    end
end

fn main()
    c = Circle{radius: 1.0}
    shapes: List<Shape> = [c, Shape.Point]
    dbg Iter.map(shapes, total) |> Iter.to_list() // [3.0, 0.0]
end
```

The enum is never accepted where an embedded type is expected, at any of
those positions, since an enum value may be another variant: `area(s)` with
`s: Shape` is `argument 1: expected Circle, got Shape`. Matching the value is
the way from the enum to the embedded type (§10).

Where the two meet with no direction (the branches of an `if` or `case`, the
elements of an unannotated literal, two arguments of one type parameter),
the result is the enum, inside tuples, records and collections too. When
neither side takes the other's values, as `(circle, shape)` beside
`(shape, circle)`, they do not join and the literal is an error. `==`
compares a `Circle` with a `Shape` as two `Shape`s, at any depth: with
`s: Shape = c`, `Some(c) == Some(s)` and `[c, c] == [s, s]` compare two
`Option<Shape>` and two `List<Shape>` values and are `True`, and
`Some(c) == Some(Shape.Point)` is `False`.

An impl function may not take the embedded type where its interface's
parameter is the enum, nor return the enum where the interface returns the
embedded type. A function value passed as an argument follows the same rule,
since the callee calls it: `Iter.map(shapes, area)` with `area(c: Circle)`
is an error, and `Iter.map(circles, total)` with `total(s: Shape)` is
accepted.

**Construction through a variant.** `Enum.V` followed by a construction
takes the forms `V`'s own shape takes and widens the result into the enum:

- A struct, inline or embedded: `Shape.Circle{radius: 2.0}` and
  `Shape.Circle({radius: 2.0})`, prefix or piped, with defaults filling
  omitted fields.
- An embedded distinct that wraps a value: the distinct's call form,
  `Session.UserId("alice")`, from the wrapped value.
- An embedded zero-sized type: the value alone, `Session.Expired`.
  `Session.Expired()` is
  `Session.Expired is a zero-sized variant and takes no arguments; write Session.Expired`.

A value that is already a `V` is not built again: `Shape.Circle(c)` with
`c: Circle` is an error whose message says it widens on its own wherever a
`Shape` is expected.

The built value is the same value as `V` widened: `Session.UserId("alice")`
equals `UserId("alice")` widened into `Session`, renders the same and
matches `Session.UserId(name)` with `name` bound to `"alice"`.

**Dispatch follows the static type.** A value typed as the enum runs the
enum's impls, however it became one; the same value typed as the embedded
type runs the embedded type's. An enum's automatic `Debug` and its derived
impls hand an embedded variant to the embedded type's impl.

**Payload shape.** Matching an embedded variant binds:

- for an embedded struct, the struct (`Shape.Circle{radius:}`
  destructures its fields);
- for an embedded distinct that wraps a value, the wrapped value
  (`Identifier.UserId(name)` binds the `String`);
- for an embedded zero-sized type, nothing.

**A field read off the enum.** `s.w` on a value typed as the enum reads the
field from whichever variant the value is. Every variant must have the
field: a struct-shaped variant's own, an embedded struct's, or that of a
variant whose one payload is a struct. A variant without it is an error at
the field,
`field 'w' is not in every variant of Shape: Circle has none`, with the help
``match the value with `case` to read a field only some variants have``.
Past three such variants the message names three and counts the rest. The
variants must also give the field one type, or the read is
`field 'w' is Int in Shape.Rect and Float in Shape.Circle, so reading it off a Shape has no one type`,
which is reported first when both hold. The accessor `.w` follows the same
rule (§7 *Field Accessors*).

#### Variants as Parameter Types

An embedded type is a type of its own, so a function that handles one
variant takes it as a parameter (`fn area(c: Circle)` above).

A variant declared inside the enum is not a type, bare or qualified:
`fn perimeter(rect: Shape.Rectangle)` is `unknown type "Shape.Rectangle"`,
with the help
`` `Rectangle` is a variant of `Shape`, not a type: take a `Shape`, or declare `Rectangle` on its own and write `embeds Rectangle` in `Shape` ``.
A function that takes one variant takes an embedded type, and a `case` arm
that matches that variant narrows an enum value to it (§10 *Type Narrowing*).
An embedded variant's qualified name is not a type either: `Shape.Circle`
is `unknown type "Shape.Circle"`, with the help
``write `Circle`, the embedded variant's own type``.

### Distinct Types

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#distinct-types).

A `type` declaration makes a new nominal type, distinct from every other
type, its representation included. What follows the name decides its kind:

- a tuple type (`type Pair (Int, String)`), at least two items;
- a generic type (`type Kvs Map<String, Int>`, `type Ids List<Int>`);
- a single type name (`type Id Int`, `type Origin Point`, `type Twice Meters`);
- a function type (`type Handler (Request) -> Response`);
- nothing: a zero-sized type (`type Expired`).

A `type` declaration may add a `when` rule, which makes it a validated
type (*Validated types* below), and a `type` over a subtype is built the
same checked way. A `subtype` declaration is no new identity: its values
are values of the types it names (*Subtypes* below). The rest of this
section is about distinct types without a rule.

A `type` declaration has no body. `type Wrap (Int)` is `type Wrap Int`,
since parentheses only group. No `=` comes between the name and the
inner type: `type Id = Int` is ``a type has no `=`: write `type X T` ``.

A distinct type may take type parameters after its name, which its inner
type names: `type Tagged<T> (String, T)`, `type Ids<T> List<T>`. Its call
form solves them from the value it wraps, as a generic struct's literal
does: `Tagged(("n", 1))` is a `Tagged<Int>`. A zero-sized type takes none:
`type Marker<T>` is
``zero-sized `type Marker` takes no type parameters: it has no value for them to describe``.

The wrapped type is the representation, so it cannot contain the type
itself, directly or through other distinct types: `type L List<L>` is
`type 'L' is defined in terms of itself`. A recursive type is a struct or an
enum, and a distinct type may wrap one.

Every distinct type that wraps a value satisfies the prelude interface
`Distinct<T>` for exactly the type it wraps: `type Twice Meters` is a
`Distinct<Meters>`, not a `Distinct<Int>`. A zero-sized type is none.
`Distinct<T>` declares no functions; it is a bound, and on an interface
default it lets the body take `self` apart and build one (§12).
`impl Distinct<Int> for X` is
`` `Distinct` is a built-in marker and cannot be implemented; every distinct type satisfies `Distinct<T>` for the type it wraps — remove this `impl Distinct for X` ``.

```nomi
type Id Int

fn raw(Id(n)) -> Int
    n
end

fn main()
    id = Id(42)
    dbg raw(id)   // 42
    dbg 7 |> Id() // Id(7)
    dbg Iter.map([1, 2], Id) |> Iter.to_list() // [Id(1), Id(2)]
end
```

The call form takes the one value the type wraps, prefix or piped, checked
against the wrapped type: `Id("42")` is
`Id wraps Int, so Id(...) takes an Int; got String`. A distinct over a tuple
takes its tuple, `Pair((1, "x"))`, or the tuple's elements,
`Pair(1, "x")`. Any other number of arguments is
`Pair wraps (Int, String), a tuple of 2 elements; got 3`, and
`1 |> Pair("x")` is
`Pair takes the piped value as its only argument, got 1 more`.

A distinct's value is read by destructuring, in any pattern: a binding
(`Id(n) = id`), a `case` arm, an `if` pattern, a binding with `else`, a
parameter (`fn raw(Id(n))`). Destructuring reads one level: for
`type A Int` and `type B A`, `B(a) = b` binds an `A`.

A zero-sized type is its own one value, written bare: `Expired`. It has no
call form: `Expired()` is
`Expired is a zero-sized type and takes no arguments; write Expired`. It may
implement interfaces and be embedded, as a standalone bare variant.

A distinct type implements interfaces as structs and enums do. A distinct
type that wraps a value, named without a call, is its constructor as a
function value (§7 *Constructors as Function Values*).

A type name is not a conversion, a function or a value:

- `Int(id)`, `Int("4")` and `String(4)` are `` `Int` is a type, not a function ``.
  On a distinct argument the help names the destructuring to write; otherwise
  it names the conversion (`String.to_int`, `Int.to_float`, `"${x}"`).
- Calling any other type name is that error too (`Dir(1)`, `Option(3)`). The
  type names with a call form are a struct and a distinct type. A pipe into a
  type name is the same call: `1 |> Point()` is
  `Point expects an anonymous struct literal {...} matching its fields, got Int`.
- A type name standing as a value, `f = Int` or `Iter.map(xs, String)`, is
  `` `Int` is a type, not a value ``. The names that are values are a
  variant, a distinct constructor, a zero-sized type, `True`, `False`,
  `Unit`, and a type name where a `Type<T>` witness is expected
  (`Context.value(c, TraceId)`).
- An opaque struct or distinct type has no call form outside its defining
  file.

#### Distinct Types Over Tuples and Maps

A distinct type whose underlying type has a shape literal (tuple, list,
vector, set, map) has two construction forms: the named literal, from the
elements, and the call form, from a value of the underlying type. A struct
has the same two. A tuple's named literal is a call with the tuple's
elements as its arguments, so it has no symbol, as a struct's literal has
none.

| Declaration | Named literal | Call form |
|---|---|---|
| `type Id Int` | none | `Id(42)` |
| `type Pair (Int, String)` | `Pair(1, "x")` | `Pair((1, "x"))` |
| `type Ids List<Int>` | `Ids[1, 2]` | `Ids(xs)` |
| `type Path Vector<Int>` | `Path[1, 2]` | `Path(v)` |
| `type Tags Set<String>` | `Tags["a", "b"]` | `Tags(s)` |
| `type Kvs Map<String, Int>` | `Kvs{"a" => 1}` | `Kvs(m)` |
| `struct User` | `User{name: "Ada"}` | `User({name: "Ada"})` |
| `type Expired` | `Expired` | `Expired` |

Where a distinct type over a list, vector, set or map is expected, the
unnamed literal builds it as the named one does: `p: Path = [1, 2]` and
`k: Kvs = {"a" => 1}` (§3 *Literal forms*). `Kvs{}` is the empty `Kvs`.

The named literal checks each element: `Pair("x", 1)` is
`element 1 of Pair: expected Int, got String`, and `Pair(1)` is
`Pair wraps (Int, String), got Int`. Patterns mirror construction (§10).
A named tuple reads its items by ordinal name, as its tuple does:
`Pair(1, "x").second` is `"x"`, and `Iter.map(pairs, .first)` reads each
pair's first item.

#### Opaque distinct types

Taught in [Modules & Imports](/modules-and-imports/#opaque-types).

`opaque` before `struct`, `enum` or `type` keeps a type's representation
private to its defining file. It is written with `pub`, which makes the
name visible to other packages; `opaque type T Int` without `pub` is
internal, its name seen by the other files of its package and its inside by none. Outside
the defining file the type may be named in signatures, fields and type
arguments, passed around, compared with `==` and given to interface
functions. These are rejected:

| Outside the defining file | |
|---|---|
| `PositiveInt(5)`, `Date{...}`, `Status.Active` (construction) | rejected |
| `Percent.try_from(n)`, `Percent.unchecked(n)` (construction through a provided function) | rejected |
| `PositiveInt(n) = p`, a `case` on its variants (destructuring) | rejected |
| `d.year`, a named tuple's `p.first` (field and item access) | rejected |
| `Struct.update(d, {year: 1})`, `{..d, year: 1}` (a patch naming a field) | rejected |

Inside the defining file every form works. An opaque enum's variants are
unreachable outside it for construction, matching and payload access alike.
The `try_from` and `unchecked` the compiler provides for a type with a rule
in its chain (*Validated types*) build a value of it, so for an opaque type
they are construction too: outside its file a call of either, or either
named as a value, is ``` `Percent.try_from` constructs the opaque type 'Percent', whose construction is private to `percent.nomi` — use an exported constructor function ``` for a `Percent` declared in `percent.nomi`.
A `try_from` the file writes in an `impl TryFrom<E>` of its own is a
public function like any other.

```nomi
// positive.nomi
pub opaque type PositiveInt Int

pub fn positive(n: Int) -> Option<PositiveInt>
    if n > 0 then Some(PositiveInt(n)) else None
end

pub fn to_int(PositiveInt(n)) -> Int
    n
end
```

Opacity restricts these operations and nothing else. An opaque type
satisfies every interface a transparent one would, at bounds and as an
interface value: `Struct`, `Distinct<T>`, `Debug`, `Display`, `Equatable`.
The one exception is the `TryFrom` the compiler provides for it, which
builds a value and so is construction: outside the file it counts toward
no bound, so a generic call bounded by `TryFrom<Int>` at the type, an
impl whose `where` needs it, a conversion to a `TryFrom<Int>` value and
`TryFrom.try_from(n)` where the type is expected are each an error that
the type lacks the impl, at a bound
``Percent does not implement TryFrom<Int> (required by `where T: TryFrom<Int>`)``,
with the help ``'Percent' is opaque, and its construction is private to `percent.nomi`: the `try_from` its rule provides builds a value, so its `TryFrom` impl counts only there; use an exported constructor function``.
A `TryFrom` impl the file writes itself counts everywhere.
Its automatic `Debug` renders `<opaque T>`, inside and outside its file. An
interface default that takes `self` apart or builds one does not apply to an
opaque type implemented outside its file; that impl writes the function
(§12).

`opaque` applies to a struct, an enum and a distinct type with a
representation. On a function, a type alias, an interface, a `once`, a
zero-sized type, a foreign type or a built-in type it is an error at the
declaration. An opaque type's representation may be private, since no
caller outside can reach it (§14).

### Subtypes

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#subtypes).

A `subtype` declaration names a narrower set of another type's values:
its name, the types it narrows, its *parents*, joined by `and`, and a
rule, `when` and a function from its first parent to `Bool`, a lambda or
a function's name. A struct may declare a rule too, as its body's last
item, on its own line after every field; a rule over several lines keeps
its own `end` inside the struct's. Such a struct is a *validated struct*.

```nomi
subtype NonNegative Float when fn x -> x >= 0.0

subtype Positive NonNegative when fn x -> x > 0.0

subtype Port Int when fn port -> port >= 1 and port <= 65535

fn is_tag(s: String) -> Bool
    String.length(s) > 0 and not String.contains(s, " ")
end

subtype Tag String when is_tag

struct Span
    low:  Int
    high: Int
    when fn span -> span.low <= span.high
end

fn half(x: Float) -> Float
    x / 2.0
end

fn main()
    p: Positive = 2.5
    dbg half(p)                          // 1.25
    dbg Positive.try_from(-1.0)          // None
    dbg Port(8080) + 1                   // 8081
    dbg Tag.try_from("two words")        // None
    dbg Span.try_from({low: 3, high: 1}) // None
end
```

The rule is checked as a `(P) -> Bool`, where P is the subtype's first
parent: `fn x -> x + 1` is
``type mismatch: expected (Float) -> Bool, got (Float) -> Float``. A
validated struct's rule takes the struct. A `when` after the struct's
`end`, on its line, is
``a struct's `when` rule goes inside its body, after its fields``, and one
above a field is ``a struct's `when` rule comes after every field``.
The rule is a pure function of
its value alone: it may call pure functions and read a `once` with no effects,
and a rule that reaches an effect (§19 *Effects*) is an error at the declaration,
``the rule of Noisy has effects (loud calls io.print): a rule is a pure function of the value it checks``.
So is a rule that reads an app field, since the compiler checks it with the value alone:
``the rule of Bounded reads `App.limit`: a rule is checked with the value alone, and an app field exists only while the program runs``.
So is a rule that reaches a pure function whose cost grows with its argument, such as `String.repeat`, which §19 *Effects* calls unbounded.
So is a rule that reaches a hash, `String.hash` or `Hashable.hash` of a String, a collection or a struct, whose value differs between runs (§5 *Hashing and keys*):
``the rule of EvenHash reads a hash, which differs between runs (lambda calls strings.String.hash): a rule must give the same answer in every run``.
The compiler runs the rule once, and the program's runs would hash the value otherwise.
A subtype is declared at the top of a file. It cannot be `opaque`, since its point is that its base is visible:
``a subtype cannot be opaque: `Secret`'s values are values of its parent, which every file sees; drop `opaque`, or declare a new type, `opaque type Secret ...`, and check values in its constructor``.

A subtype narrows. Its `when` is optional only where its parents narrow
already: a subtype of two or more parents with a rule among them is
their intersection, `subtype PositiveInt NonNegativeInt and NonZeroInt`,
and needs no rule of its own. A subtype with no rule anywhere in its
chain is an error that names the alias it amounts to,
``a subtype with no rule has the same values as Float: `Celsius` adds no `when`, and nothing it narrows has one`` with the help
``name it with `typealias Celsius Float`, declare a new type with `type Celsius Float`, or give the subtype a rule with `when` ``,
and so is one of a single parent with no `when` of its own, whose rule
is all in the parent: `subtype Small Digit` is
``a subtype of one parent with no rule of its own has the same values as Digit: `Small` adds no `when` ``,
with the help naming `typealias Small Digit`. A `where` bounds the type
arguments, not the values, so `subtype Sortable<T> NonEmptyList<T> where
T: Comparable` with no `when` is that error too, whose help names
`typealias Sortable<T> NonEmptyList<T> where T: Comparable`.

#### Chains and conversion

A subtype's parent may itself be a subtype, as `Positive` narrows
`NonNegative`, and a subtype may have several parents joined by
`and`: with `subtype NonZero Int when fn n -> n != 0` and `subtype Count
Int when fn n -> n >= 0`, `subtype Size Count and NonZero when fn n -> n
> 0` is a `Count` and a `NonZero`. Its parents, their parents and so on
are its *ancestors*. Following parents from a subtype reaches its *base*,
the first type that is no subtype (`Float` for `Positive`), and every
parent of a subtype reaches the same base, since the subtype's value is
that one value at run time:
``the parents of Mixed narrow different types: Count is an Int and Tag is a String``.
A parent named twice, and a parent that reaches the subtype itself
(`subtype A B and C when ...` with `subtype C A when ...`), are errors at
the declaration. A value of a subtype is a value of each of its
ancestors: it converts to each of them and to its base wherever one is
expected, implicitly, at the top and at every position a value converts
at (§5 *Covariant conversion*), so a `List<Positive>` is a `List<Float>`
and a `List<Size>` is a `List<NonZero>`. Nothing converts down or across:
a `NonNegative` is no `Positive`, a `Count` is no `NonZero`, and of two
subtypes of one parent, `subtype Even Int when ...` and `subtype Odd Int
when ...`, neither converts to the other. The one conversion down is into
a subtype with no rule of its own: its values are exactly the values of
all its parents, so a value of every parent converts into it. With
`subtype Strict NonNegativeInt and NonZeroInt`, a `PositiveInt` passes
where a `Strict` is expected, and a `NonNegativeInt` does not.

Where values meet with no type written for them (the branches of an `if`
or `case`, the elements and spreads of a list, vector or set literal and a
map literal's keys, values and spreads, a lambda's results, two arguments
of one type parameter, the two operands of an operator, a range's
endpoints), a subtype and an ancestor of it, or two subtypes that
share one, join as the nearest ancestor they share, which is the base when
nothing nearer is, whatever their order. `if c then List.length(xs) else
-1` is an `Int`; `PositiveInt` beside `NonZeroInt` is a `NonZeroInt`, and
`NonNegativeInt` beside `NonZeroInt` is an `Int`. They join the same way
at every position a value converts at: `[Some(List.length(xs)), Some(-1)]`
is a `List<Option<Int>>`, `{..counts, ..sizes}` with a `Map<String, Int>`
and a `Map<String, PositiveInt>` is a `Map<String, Int>`, and `ints +
positives` with a `List<Int>` and a `List<PositiveInt>` adds two
`List<Int>`s. A collection literal beside a value of a subtype of a
collection is its base's, so with `names` a `NonEmptyList<String>`, `if c
then [] else names`, `if c then names else []` and
`Option.with_default(found, [])` with `found` an
`Option<NonEmptyList<String>>` are `List<String>`s, and
`Iter.group_by(xs, f) |> Map.get(k) |> Option.with_default([])` is a
`List` of `xs`'s elements. A literal beside values of a subtype there, in
any order, is the nearest of that subtype and its ancestors that is a std
numeric subtype (*Value ranges*) whose rules the literal
satisfies, which the compiler decides as it checks the program, and the
values join as that type: with `n` a `NonNegativeInt`, `if c then n else
0`, `if c then 0 else n`, `[0, n]` and `Option.with_default(Iter.find_index(xs,
f), 0)` are `NonNegativeInt`s, with `ps` a `List<PositiveInt>`, `[..ps, 0]`
and `[0, ..ps]` are `List<NonNegativeInt>`s, and with `s` a `subtype Small NonNegativeInt
when fn n -> n < 10`, `if c then s else 1` is a `NonNegativeInt`. A
literal that satisfies none is its base's, so
`Option.with_default(Iter.find_index(xs, f), -1)` is an `Int`, and so is a
literal beside a subtype whose rule only the built program runs: with `p`
a `Port`, `if c then p else 80` is an `Int`. A range with no type written
ranges over its endpoints' base: `0..List.length(xs)` is a `Range<Int>`. A type written
for the value still decides it, so `m: NonNegativeInt = if c then 1 else
2` checks each literal against the rule, and `all: List<PositiveInt> =
[..ps, 0]` is ``0 is not a PositiveInt: PositiveInt is a subtype of NonZeroInt, and NonZeroInt's rule `fn n -> n != 0` is false``. A type parameter solved to a
subtype widens to the ancestor only where every argument it was solved
from converts to it: with `fn apply<T>(f: (T) -> Int, x: T)`, `count` a
`(NonNegativeInt) -> Int` and `i` an `Int`, `apply(count, i)` is
`argument 2: expected NonNegativeInt, got Int`. A later argument that is
a collection literal widens it the same way: with `n` a `NonNegativeInt`,
`List.concat([n], [i])` is a `List<Int>`. So does a later argument that
names the ancestor at a type argument that converts nothing: with `p` a
`PositiveInt`, `Int.clamp(p, -5..=5)` takes a `ClosedRange<Int>`, solves
`self` to `Int`, and `p` converts to it.

A subtype's value is its base's at run time: converting up
costs nothing, and `Debug` and `Display` show the base value (`dbg p`
prints `2.5`). It has its base's impls, so a subtype of a List, Map,
Vector, Set or String is iterated, spread, matched and tested with `in`
as its base is (`Iter.sum(p)` with `p: Pair`), and implements no
interface of its own: `impl Display for Port` is
``a subtype implements no interface of its own: `Port` is an Int at run time and has Int's impls``,
and so is a `derive`. Functions in an inherent block, `impl Port`, are
allowed. An interface impl whose interface takes a subtype as an
argument (`impl Add<Port, Int> for Int`) is an error, since the impl at
the base serves the subtype already. The right operand of `/`, `%`, `<<<`
and `>>>` is the exception: an impl of `Divide`, `Remainder`, `ShiftLeft`
or `ShiftRight` may take a subtype there, which states the operands the
operator takes, as std's `impl Divide<NonZeroInt, Int> for Int` does
(§8 *Integer division and remainder*). An operand of a subtype is its
base's value: `p + 1.0` is a `Float`, `port == 80` compares Ints, a
collection literal beside one is built as the base (`names + ["Dorothy"]`
with `names` a `NonEmptyVector<String>` adds two Vectors), and a
literal pattern matches a narrowed value as it matches the base's, one
value of it, so a `case` over a `Port` whose arms are literals needs a
`_` or `Port(n)` arm. The std numeric subtypes are
the one exception: an operation on them is the subtype its value's range proves (*Value ranges*). An enum cannot embed a
subtype. A validated struct is a struct: it converts to nothing, and
its rule only constrains how values are built.

A subtype does not have its base's impl of an interface that makes a
value of its type: one with a function whose result names `self` and
whose body each impl writes. In std these are `Parse`, `ParseFields`,
`FromJson`, `FromSql`, `Summable`, `Productable`, `Discrete`,
`Steppable`, `From`, `TryFrom`, `Variants` and `Struct`. The base's
impl makes a value of the base, which no rule of the subtype has
checked. So `Parse.parse<Port>(text)` is the error
``Port has no impl of Parse (required by `Parse.parse`): Port is a subtype of Int, and Int's `Parse.parse` makes an Int that Port's rule has not checked``
with the help
``make an Int and check it with `Port.try_from(...)`, which is `Some` when it satisfies `fn port -> port >= 1 and port <= 65535` and `None` when not``,
and so is `Summable.empty_sum<Port>()`.

A reader is the exception: an interface function that makes a value from
outside data and reports a failure the compiler can build reads a
subtype at its base and checks the subtype's rules, a value that breaks
one being the read's failure. `FromJson` is the one: `from_json` at a
`Port` is Int's `from_json` and then `Port.try_from` of the Int, and a
breach is the `Json.ShapeError` that expects the subtype and got the
value, `port: expected Port, got 0`. So `Json.decode_as<Port>(text)`,
`Json.decode_as<List<Port>>(text)`, `Json.decode_as<{port: Port}>(text)`
and a `derive FromJson` for a struct with a `Map<String, Port>` field
each read Ints and check them, an intersection against each member's
rules and a subtype of a subtype against every rule its `try_from`
checks. A record read from texts, std::parse's `ParseFields`, reads a
field of a subtype with its base's `Parse` and checks it the same way, a
breach the record's `None`: `parse.read_fields<{v: Port}>({"v" => "0"})`
is `None`, and a route's `{id: Port}` parameter that breaks the rule
answers no route (404). `Parse.parse<Port>(text)` stays the error above,
since a breach would need an error of Parse's `E`, which each impl
chooses. Inside a generic type, std::json's own impls read a subtype
(`List`, `Vector`, `Set`, `Option`, `Result` and `Map<String, V>`). Any
other generic type, the program's or another std module's, has one impl
for every instance of one kind, so its `where T: FromJson` reads no
subtype: with `struct Box<T>` deriving FromJson,
`Json.decode_as<Box<Port>>(text)` is
``Box<Port> does not implement FromJson (required by `where T: FromJson`)``
with the help
``Box<Port> is read through an impl of FromJson that one generic type's instances share, which would read Port as its base; read Box<Int> and check the value with `Port.try_from(...)` ``.

A generic call whose arguments solve a type parameter to a subtype,
under a bound of an interface that makes a value, is called at the base
instead: with `ports` a `List<Port>`,
`Iter.sum(ports)` is an `Int` and `Discrete.next(p)` an `Option<Int>`, so
`total: Port = Iter.sum(ports)` is ``type mismatch: expected Port, got Int``,
checked with `Port.try_from` as any Int is. A generic function named as a
value is instantiated the same way, at the base, when its position solves
such a parameter to a subtype: with `groups` a `List<List<Port>>`,
`Iter.map(groups, Iter.sum)` sums Ints, as `Iter.sum(ports)` does. A range iterates through
`Discrete`, so a `Range<NonNegativeInt>` is no `Iter`; `0..n` ranges
over Ints, and its elements keep their range. An interface whose
functions that name `self` in their result are all defaults it writes
still serves a subtype, since such a default answers a value it was
given: `Comparable`'s `min`, `max` and `clamp`, so `Iter.max(ports)` is
an `Option<Port>` and `Iter.sort(ports)` a `List<Port>`. `Display`,
`Debug`, `ToJson`, `Equatable` and `Hashable` take a value and make
none.

```nomi
import std::parse.Parse

subtype Port Int when fn port -> port >= 1 and port <= 65535

fn read_port(text: String) -> Option<Port>
    case Parse.parse<Int>(text)
        Ok(n)  -> Port.try_from(n)
        Err(_) -> None
    end
end

fn main()
    ports: List<Port> = [80, 443]
    dbg Iter.sum(ports)   // 523, an Int
    dbg Iter.max(ports)   // Some(443), an Option<Port>
    dbg read_port("0")    // None
    dbg read_port("8080") // Some(8080)
end
```

```nomi
import std::json.{FromJson, Json}

subtype Port Int when fn port -> port >= 1 and port <= 65535

struct Server
    host:  String
    ports: List<Port>
end

derive FromJson for Server

fn main()
    dbg Json.decode_as<Server>(`{"host": "a", "ports": [80]}`) |> Result.is_ok() // True
    bad = Json.decode_as<Server>(`{"host": "a", "ports": [80, 0]}`)
    dbg bad |> Result.map_err(&Display.to_string(&1)) // Err("ports.[1]: expected Port, got 0")
end
```

#### Intersections of subtypes

`A and B` of subtypes of one base is a type wherever a type is written:
a parameter, a field, a result, a collection's element, a `type`'s inner
type, a `typealias`'s target. It is the subtype whose parents are its
members and which adds no rule, so a value converts into it when it
converts to every member, and it converts to each member and to the base.
A literal there is checked against every member's rule, and the error
names the member whose rule fails: `half(0)` below is
``0 is not a NonZeroInt, as NonNegativeInt and NonZeroInt requires: `fn n -> n != 0` is false``.

```nomi
fn half(n: NonNegativeInt and NonZeroInt) -> Int
    n / 2
end

fn main()
    p: PositiveInt = 8
    dbg half(p) // 4
    dbg half(6) // 3
end
```

The order of the members does not matter, and a member that is an
ancestor of another adds nothing: `NonNegativeInt and NonZeroInt` and `NonZeroInt and NonNegativeInt`
are one type, and `PositiveInt and NonZeroInt` is `PositiveInt`. Members
of different bases are an error, ``` `and` intersects subtypes of one type, and NonEmptyString is a String where NonNegativeInt is an Int ```,
and so is a member that is neither an interface nor a subtype, ``` `and` joins interfaces or subtypes, and Int is neither ```. An
interface beside subtypes is one their base must implement:
`NonNegativeInt and Display` is a `NonNegativeInt`, and with `interface
Named` that Int lacks, `NonNegativeInt and Named` is
``no value is both NonNegativeInt and Named: NonNegativeInt is an Int, and Int does not implement Named``.
Intersections join as subtypes do: two of them meet at the nearest
ancestor they share.

#### Generic subtypes

A subtype may take type parameters, which its parents name:
`subtype NonEmpty<T> List<T> when fn xs -> not Iter.is_empty(xs)`. A rule
that needs an operation on the elements bounds a parameter with `where`,
written after the parents and before `when`, as a generic function's
`where` follows its signature. Parents may carry type arguments, and a
subtype may fix its parent's (`subtype IntPair List<Int> when ...`).

```nomi
subtype NonEmpty<T> List<T> when fn xs -> not Iter.is_empty(xs)

subtype Ascending<T> List<T> where T: Comparable when fn xs ->
    Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)
end

subtype Run<T> NonEmpty<T> and Ascending<T> where T: Comparable when fn _ -> True

fn longest(xs: NonEmpty<String>) -> String
    Iter.max_by(xs, String.length)!
end

fn main()
    r: Run<Int> = [1, 4, 9]
    dbg Ascending.try_from([3, 1]) // None
    dbg longest(["ab", "abc"])     // "abc"
    a: Ascending<Int> = r
    dbg a // [1, 4, 9]
end
```

The rule is checked per instance. A literal of `Run<Int>` is checked when
the program is compiled with `T` as `Int`, and `T.try_from(x)` is generic,
its argument solving the type parameters: `Ascending.try_from(circles)`,
with `Circle` no `Comparable`, is the error
``Circle does not implement Comparable (required by `where T: Comparable`)``.
A literal of a generic subtype is checked through the `try_from` of the
subtype's declaration at the literal's type arguments, whatever the
literal's file names: `first([1, 2])` checks its argument against a
`NonEmpty<Int>` parameter of a module whose `NonEmpty` the file does not
import, and an import of `NonEmpty` used by nothing else is unused. A type
argument that breaks a bound is the error at the literal:
`u: Ascending<Circle> = []` is
``Circle does not implement Comparable (required by `where T: Comparable`)``.

A generic rule is checked for effects at its declaration, whatever its
type arguments will be: its own body, and through its bounds every impl
of their interfaces the program and std have, since any implementor can
be the argument. With `impl Display for Loud` printing, `subtype
Shown<T> List<T> where T: Display when fn xs -> Iter.all_match(xs, fn x
-> Display.to_string(x) != "")` is the error
``the rule of Shown has effects (Display.to_string for Loud calls io.print): a rule is a pure function of the value it checks``
at its `when`, with or without a `Shown<Loud>` anywhere.

A generic subtype's type arguments convert as its parents' do (§5
*Covariant conversion*): a parameter its parents only hold is covariant,
so a `NonEmpty<Circle>` passes where a `NonEmpty<Shape>` or a
`List<Shape>` is expected when `Circle` implements `Shape`, and a
`NonEmpty<PositiveInt>` is a `NonEmpty<Int>`; a parameter a parent holds
as a function's parameter converts the other way, and one held both ways
compares exactly. A join is at the nearest shared ancestor with its type
arguments joined: `Run<Int>` beside `Ascending<Int>` is an
`Ascending<Int>`. A validated struct takes no type parameters:
``a validated struct takes no type parameters: `Span`'s rule is checked on values of one struct; a generic subtype narrows a type, `subtype Span<T> ... when ...` ``.

#### Literals, `try_from` and `unchecked`

A value enters a subtype three ways:

- A literal of the base (a number, a negated number, a string with no
  `${...}`, `True` or `False`, or a typed literal with no `${...}` such as
  `Duration"15m"`) where a subtype is expected (an
  annotation, a parameter, a field, a result, a collection element, an
  argument of a generic call such as `Some(22)` as an `Option<Port>`), or as
  the argument of its call form, `Port(8080)`, is that subtype. A
  struct literal of a validated struct that writes every field as a
  literal is one too, and so is a list, vector, set or map literal where a
  subtype of a collection is expected whose every element, key and
  value is a literal, through nested collection and tuple literals: with
  `subtype Pair List<Int> when fn xs -> List.length(xs) == 2`,
  `p: Pair = [1, 2]` and `Pair([1, 2])` are checked, and `[1, 2, 3]` there is
  ``this Pair literal breaks its rule: `fn xs -> List.length(xs) == 2` is false``.
  A list, vector, set or map literal where `NonEmptyList`,
  `NonEmptyVector`, `NonEmptySet` or `NonEmptyMap` is expected needs no
  literal elements when its shape proves it holds something: it writes
  an element or an entry, or spreads a value that holds something: one
  of those types (or a subtype of one), or a binding that holds
  something (below). `[a, b]`, `Vector[f(x)]`, `Set[x]`, `{k => v}`,
  `[..xs, y]` and `[..xs, ..ys]` with `ys` a `NonEmptyList` are each one, with
  each element checked against the element type as usual. A literal
  made only of spreads of collections that may be empty, `[..xs]` with
  `xs` a `List`, is the error ``` `[..xs]` may be empty, and a NonEmptyList holds something: none of its spreads holds something, and it writes no element ```,
  and an empty one, `[]`, breaks the rule. A subtype declared over one
  of them keeps its own rule, so its literal's elements are literals. A
  string literal where a `NonEmptyString` is expected holds something by
  its shape when it writes any text, whatever its `${...}` parts are:
  `"n=${n}"` is one. One made only of `${...}` parts, `"${name}"`, may
  be empty. A literal headed by a collection's name, `Vector[...]` or `Set[...]`,
  builds a subtype of that collection where one is expected, as the
  unnamed literal does.
  A range literal whose endpoints are both literals is one where a subtype
  of `Range` is expected: with `subtype Wide Range<Int> when fn r ->
  Range.contains(r, 0) and Range.contains(r, 5)`, `w: Wide = 0..10` is
  checked, and `0..n` there is an error naming `n` and `Wide.try_from`.
  The compiler evaluates the rule of each ancestor that has one
  on the literal before the program runs, each once, a parent's ancestors
  before the parent and the parents in the order they are written, then
  the subtype's own; the first that answers `False` is an error at the
  literal that names the path to it:
  ``-1.0 is not a Positive: Positive is a subtype of NonNegative, and NonNegative's rule `fn x -> x >= 0.0` is false``,
  ``0 is not a Size: Size is a subtype of NonZero, and NonZero's rule `fn n -> n != 0` is false``,
  or ``0 is not a Port: `fn port -> port >= 1 and port <= 65535` is false``.
  A rule that traps there is the error ``0 is not a Trapping: Trapping's rule `fn n -> 10 / n > 1` failed: ...``
  with the trap's message, and one that does not finish within the
  compiler's step limit is an error naming the rule.
- `T.try_from(x)`, as public as the type, is
  `Some(x)` as a `T` when `x` satisfies the rule of every ancestor and
  T's own, checked through each parent's `try_from` in the order the
  parents are written, and `None` when it does not. A rule two parents
  reach through their own parents runs once for each; a rule is pure, so
  that changes nothing but time. A rule that traps on `x` counts as false:
  with `subtype Ratio Int when fn n -> 10 / n > 1`, `Ratio.try_from(0)` is
  `None`, and the program goes on. It takes a value of the base, or for a
  validated struct the record of its fields:
  `Span.try_from({low: 1, high: 2})`. A subtype implements no interface,
  so its `try_from` is its own function, not `TryFrom`'s.
- `T.unchecked(x)` builds a `T` without evaluating any rule. Only the file
  that declares `T` may call it, for functions that keep the rule
  (adding two non-negative values gives a non-negative one); elsewhere it
  is ``` `Port.unchecked` builds a Port without checking its rule, and only the file that declares Port may call it ```.

A binding with no type written keeps what its value proves about holding
something, as it keeps a number's range (*Value ranges*). A List,
Vector, Set, Map or String value holds something when it is of a
non-empty type, a literal that holds something by its shape as above, a
read of a binding that holds something, a block whose last expression
holds something, or an `if` with an `else` or a `case` whose every
branch that finishes holds something. With `names = ["a", "b"]`, `names`
and `[..names]` pass where a `NonEmptyList<String>` is expected, with
nothing run when the program does; `names` is still a `List<String>`
wherever else it is read, as `x = 3` is an Int. With `label = "n=${n}"`,
`label` passes where a `NonEmptyString` is expected the same way. An
unannotated `once` whose value holds something is the non-empty subtype:
`once words = ["a"]` is a `NonEmptyList<String>` and `once title =
"Nomi"` a `NonEmptyString` in every file that reads it. A binding with a
type written, a parameter and a field hold what their type states, and a
value a function computes holds what its result type states:
`Iter.to_list(xs)` is a `List`, `NonEmptyList.push(xs, y)` a
`NonEmptyList`, and `String.trim(s)` a `String`. With `flag` a Bool, `if
flag then ["a"] else []` may be empty, and so may `""` and `"${s}"`.

```nomi
once words = ["nomi", "pipeline"]

once title = "Nomi"

fn first_word(xs: NonEmptyList<String>) -> String
    NonEmptyList.first(xs)
end

fn initial(s: NonEmptyString) -> String
    String.take(s, 1)
end

fn main()
    local = ["map", "filter"]
    both = [..local, ..words]
    n = 2
    label = "n=${n}"
    dbg first_word(words) // "nomi"
    dbg first_word(local) // "map"
    dbg first_word(both)  // "map"
    dbg initial(title)    // "N"
    dbg initial(label)    // "n"
end
```

Any other value of the base where a subtype is expected, unless a check
narrowed it (*Narrowing*), its range proves the rule (*Value ranges*) or
it holds something (above), is an error that names `try_from`: with `n: Int`, `p: Port = n` is
`type mismatch: expected Port, got Int` with the help
``Port is a subtype of Int; check the value with `Port.try_from(...)`, which is `Some` when it satisfies `fn port -> port >= 1 and port <= 65535` and `None` when not``,
and `Port(n)` is
`Port(...) takes a literal: Port is a subtype, and an Int computed when the program runs may not satisfy its rule`.
A validated struct's literal with a field that is no literal, its call
form over a record value, a patch of one (`{..span, low: 0}`,
`Struct.update`), and a collection subtype's literal with an element
that is no literal or a spread (`[1, n]` as a `Pair`), except one of the
std non-empty collections above, are errors naming
`try_from` the same way. An operand of a comparison is the exception: a
literal compared with a value whose type holds a subtype is checked
against that type with its subtypes erased, the type the two are
compared at, so with `p` a `Pair`, `p == [1, n]` and `p == [1, 2, 3]`
compare Lists, and with `names` a `NonEmptyList<String>`, `names == []`,
`[] == names` and `Some(names) == Some([])` are `False`. A subtype named as a function value, `Iter.map(xs, Port)`, is the error ``` `Port` is a subtype and has no constructor function: its call form `Port(...)` takes only a literal ```.
The names `try_from` and `unchecked` are the subtype's: an inherent
block that declares either is an error.

The prelude holds subtypes of the numeric types and of String:
`NonZeroInt`, `NonNegativeInt` and `PositiveInt` (`NonNegativeInt and
NonZeroInt`); `NonZeroFloat` (neither zero nor NaN), `NonNegativeFloat` and
`PositiveFloat`; `NonZeroDecimal`, `NonNegativeDecimal` and
`PositiveDecimal`; and `NonEmptyString`. It holds generic subtypes of
the collections too: `NonEmptyList<T>`, `NonEmptyVector<T>`,
`NonEmptyMap<K, V>` and `NonEmptySet<T>`. Each has functions that need no
`Option` because a value is there (`NonEmptyList.first`, `NonEmptyList.max`,
`NonEmptyList.reduce` with no starting value, `NonEmptyMap.first_entry`,
`NonEmptyVector.pop`), functions whose result keeps the guarantee
(`NonEmptyList.map`, `NonEmptyList.sort_by`, `NonEmptyList.prepend` onto
any List, `NonEmptySet.union` with any Set), a constructor that cannot fail
(`NonEmptyList.of(first, rest)`), and a length or size that is a
`PositiveInt`. A std function returns the most precise of these types its
result always has: `Vector.push`, `Set.insert`, `Map.put` and
`Map.update` return the non-empty subtype whatever they were given,
`String.split` a `NonEmptyList<String>`, and `Iter.frequencies` a
`Map<T, PositiveInt>`. Outside the prelude, std::int's `Int64` is the Ints
from -2^63 to 2^63 - 1. A std
function whose limit is 64 bits takes one, so `Int64.try_from(n)` checks a
computed value before the call. A std function whose argument
would otherwise trap or be ignored takes one: `Int.pow`'s exponent,
`Int.bit_count`'s argument, a count of elements or characters
(`Iter.take`, `Iter.drop`, `Iter.combinations`, `String.take`,
`String.drop`, `String.repeat`), the width of `String.pad_start` and
`String.pad_end`, and the digit counts of `Int.format`,
`Float.format_percent` and `Duration.clock` are `NonNegativeInt`s, so
`Int.pow(2, -1)` and `Iter.take(xs, -1)` are
``-1 is not a NonNegativeInt: `fn n -> n >= 0` is false``; a size
that must hold something is a `PositiveInt` (`Channel.buffered`'s
capacity, `Reader.read`'s `max`, `Iter.chunks` and `Iter.windows`); and
the divisor of a named function that divides is a `NonZeroInt`
(`Int.modulo`, `Int.floor_div`) or a `NonZeroDecimal` (`Decimal.divide`,
`Decimal.divide_exact`). Two bounds that must agree are one range of the
prelude's range subtypes (§3 *Range literals*): `Int.clamp(n, 0..=11)`,
`Generator.int(1..=6)`, and the digits after the point of `Float.format`,
`Decimal.format` and `Decimal.format_percent`, a
`ClosedRange<NonNegativeInt>` (`fraction: 0..=2`, or `Range.from(2)` for
at least two and every digit past them). Std modules declare
subtypes of their own where one argument has a rule: `Decimal.allocate`
and `Money.allocate` take std::decimal's `Ratios` (none negative, not all
zero, at least one), `Generator.float` std::random's `FiniteRange` (a
range with finite ends), std::csv's options a `Delimiter` (no quote, line
break, U+FFFD or U+0000), `Bits.field` std::bits' `FieldBits` (a pattern
with a field letter) and `jwt.sign_as` std::json's `JsonObject` (a
`Json.Obj`). A computed argument goes through `try_from`, or a check that narrows it. A std function
whose result always satisfies one returns it: a length, size or count
(`List.length`, `String.length`, `Map.size`, `Iter.count`) and an index
(`Iter.with_index`'s, `Iter.find_index`'s) are `NonNegativeInt`s, and so
are `Int.abs`, `Int.gcd` and `Int.sqrt`, so `Int.pow(2, List.length(xs))`
needs no `try_from`. A group or a run is a `NonEmptyList`:
`Iter.group_by`'s values and the elements of `Iter.chunks`,
`Iter.windows`, `Iter.chunk_by` and `Iter.chunk_equal`. `Float.abs` is a
Float, since the absolute value of NaN is NaN, which is no
`NonNegativeFloat`.

Destructuring reads a subtype's parent, its first when it has
several, as it reads a distinct type's inner value: `Positive(x) = p`
binds a `NonNegative`.

#### Value ranges

The std numeric subtypes are the prelude's `NonZeroInt`,
`NonNegativeInt` and `PositiveInt`, their Float and Decimal counterparts,
and std::duration's `NonZeroDuration`, `NonNegativeDuration` and
`PositiveDuration`. The compiler knows, of each value of Int, Decimal,
Duration and Float where it can, an interval the value lies in, open or
unbounded at either end, and whether it is off zero: the value's
*range*. A value is the narrowest std numeric subtype its range proves:
Positive when no value in it is zero or below, NonNegative when none is
below zero, NonZero when it excludes zero. With `count` a
`NonNegativeInt`, `count + 1` is a `PositiveInt`, `count + 0` a
`NonNegativeInt` and `count - 1` an `Int`; after `if size > 1`, `size -
1` is a `PositiveInt`.

A range comes from:

- a literal, possibly negated: its value;
- a type: what the std numeric subtypes on its chain state (a
  `PositiveInt` is 1 and up), with the bounds of each Int subtype on it
  whose rule compares its parameter with Int literals (a conjunct of
  another form bounds nothing); a parameter, a field, an annotated
  binding and any other value not listed here has its type's range;
- a binding with no type written: its value's, so with `k = 60 * 60`,
  `k` is 3600 wherever it is read;
- a check of a binding (*Narrowing*);
- arithmetic, which is exact (§8 *Integer arithmetic is exact*, §8
  *Decimal arithmetic*): `a + b`, `a - b`, `a * b` and `-a` of Ints and
  of Decimals, and `a + b` and `a - b` of Durations and a Duration
  times an Int, as Int arithmetic on nanoseconds. Of Floats, `a + b`
  where neither is below zero is no NaN (an overflow is `+inf`), at
  least zero, and above zero when either is, since rounding to nearest
  takes no such sum below its larger operand; `-a` is exact. A Float
  product or difference has no range: the product of two positive
  values can round to `0.0`, and `0.0 * inf` and `inf - inf` are NaN;
- an Int's `a / b`, which truncates toward zero, by a `b` above zero:
  between 0 and `a`, so `n / 2` of a non-negative `n` is non-negative;
  by a `b` below zero, between 0 and `-a`. An Int's `a % b`, which takes
  `a`'s sign: between 0 and `a`, and smaller in size than the largest
  `b` can be when `b` is bounded. `Int.modulo(a, b)`, which takes `b`'s
  sign: from 0 to below the highest `b` can be when `b` is above zero,
  so `Int.modulo(n, 26)` is 0 to 25 and `Int.modulo(n, p)` with `p` a
  `PositiveInt` is a `NonNegativeInt`, and from above the lowest `b` can
  be to 0 when `b` is below zero. `Int.floor_div(a, b)`: as `a / b`, and
  below zero when `a` and `b` differ in sign. An operator's function has
  the operator's range: `Int.remainder(a, b)` is `a % b`'s;
- the branches of an `if` with an `else` or of a `case`, and a block's
  last expression: every value a branch that finishes can have;
- `Int.abs`, `Decimal.abs` and `Duration.abs`: the absolute values of the
  argument's range; `Int.pow(b, e)` and `Decimal.pow(b, e)`: 1 when `e`
  is 0, the power when both are one value and it is small enough to work
  out (`Int.pow(2, 63) - 1` is 2^63 - 1), else positive, non-negative or
  off zero as `b` is; `Int.sqrt(n)`: the roots of `n`'s ends;
  `Int.sign(n)` and `Decimal.sign(d)`: the signs of the argument's ends,
  so the sign of a positive value is 1;
  `Int.to_decimal(n)`: `n`'s range; `Duration.as_nanos(d)`: `d`'s range,
  and `Duration.as_micros`, `as_millis`, `as_seconds`, `as_minutes` and
  `as_hours`: its quotient by the unit;
  `Duration.nanoseconds`,
  `microseconds`, `milliseconds`, `seconds`, `minutes` and `hours`: the
  argument's range in nanoseconds, so `Duration.minutes(p)` with `p` a
  `PositiveInt` is a `PositiveDuration`; `String.length`, `List.length`,
  `Vector.length`, `Map.size`, `Set.size` and `Iter.count` of a value
  that holds something (*Literals, `try_from` and `unchecked`*), as a
  value of a non-empty subtype does and a binding with no type written
  of `["a"]` does: positive, so with `names = ["a"]`,
  `List.length(names)` is a `PositiveInt`; `Int.max(a, b)`, `Decimal.max` and
  `Duration.max`: at least the higher of the arguments' lowest values
  and at most the higher of their highest, and `min` the other way
  round, so `Int.max(1, n)` is a `PositiveInt`; `Int.clamp(v, a..=b)`,
  `Decimal.clamp` and `Duration.clamp` of a range written in the call,
  `a..=b` or `Range.from(a)`: the smaller of `b` and the larger of `v`
  and `a`, so `Int.clamp(n, 0..=9)` is 0 to 9 and a `NonNegativeInt`. A
  range bound to a name first gives the result its type's range;
- an element a std `Iter` function passes to a callback (below).

Anything else, a Decimal's `/` and `%` and the bitwise operators among
them, has its type's range. A Float's range holds only of a value that is no NaN, and a
Float of none may be NaN. A Duration's typed literal has none, since the
compiler does not read its value. No range relates two values: after `i
< n`, `i` is no narrower than before.

A value whose range rests on literals alone keeps its base as its type,
so `1 + 2` and `x` after `x = 3` are Ints, and `List.concat([n], [-1])`
is a `List<Int>`. A check of such a binding (*Narrowing*) types it by
what the check proves of its type alone: after `x = 3` and `if x < 0
then return`, `x` is still an Int, and its range still rests on literals. An unannotated `once` is a constant, and its type is the
narrowest std numeric subtype its value's range proves: `once k = 60 *
60` is a `PositiveInt` in every file that reads it.

Where a subtype is expected, a value converts to it when its range proves
the subtype's rules, with nothing run when the program does: a std
numeric subtype, an intersection of them, or an Int subtype whose only
parent is `Int` and whose rule is comparisons of its parameter with Int
literals joined by `and`, as std::int's `Int64` is. With `k = 60 * 60`,
`Int.pow(2, k)` needs no `try_from`, and with `subtype Digit Int when fn
n -> n >= 0 and n <= 9`, neither does `digit(n)` after `if n >= 0 and n
<= 9`. A subtype whose rule is of any other form, or whose parent is
another subtype, is checked through `try_from` as before.

A user-declared subtype gets no arithmetic of its own; its value is a
value of its ancestors, so with `s` a `subtype Small NonNegativeInt when
fn n -> n < 10`, `s + s` is a `NonNegativeInt` (0 to 18), not a `Small`.

The elements of some iterables have a range, and a lambda or capture
that a std `Iter` function calls with them takes it for the parameter
that receives one: the element of `Iter.map`, `filter`, `reject`,
`each`, `find`, `find_map`, `find_index`, `any_match`, `all_match`,
`flat_map`, `take_while`, `drop_while`, `partition`, `group_by`,
`count_by`, `min_by`, `max_by`, `sum_by`, `mean_by`, `product_by`,
`chunk_by`, `sort_by` and `sort_with`, and the item of `reduce`, `scan`,
`try_reduce` and `map_reduce`. The parameter keeps its type, an `Int`
here, and converts where its range proves a subtype, as a binding with
no type written does. A function passed by name is the same callback:
with `fn verse(day: PositiveInt) -> String`, `1..=n |> Iter.map(verse)`
is accepted as `Iter.map(&verse(&1))` and `Iter.map(fn d -> verse(d))`
are, since every element is 1 or more. The elements with a range are
those of:

- a range literal of Ints: `a..=b` from the lowest value `a`'s range
  holds to the highest `b`'s, and `a..b` below that, so every element of
  `1..=n` is 1 or more and every element of `0..8` is 0 to 7;
- `Range.from(n)` and `Iter.from(n)`: `n`'s lowest value and up;
  `Range.naturals()`: 0 and up; `Range.step_by(a..=b, by)`: between `a`
  and `b`, whichever way the step goes;
- a List, Vector or Set literal with no spread: its elements' ranges
  together, a tuple's part by part;
- `Iter.filter`, `reject`, `take`, `drop`, `take_while`, `drop_while`,
  `sort`, `sort_by`, `sort_with`, `reverse`, `to_list`, `to_vector`,
  `to_set` and `cycle` of a source with a range, which pass on elements
  they keep unchanged; `Iter.concat` of two; the second part of
  `Iter.with_index`'s tuples and each part of `Iter.zip`'s;
- `Iter.map` and `Iter.scan`: the range of the callback's result, when
  its body has no `return` or `break`.

A source of any other form, a binding among them, gives its elements no
range, and `Iter.iterate` gives none.

```nomi
fn divisors(n: PositiveInt) -> List<Int>
    1..=n |> Iter.filter(fn d -> n % d == 0) |> Iter.to_list()
end

fn bits(byte: Int) -> List<Int>
    Iter.map(0..8, &(byte >>> (7 - &1) &&& 1)) |> Iter.to_list()
end

fn main()
    dbg divisors(12) // [1, 2, 3, 4, 6, 12]
    dbg bits(5)      // [0, 0, 0, 0, 0, 1, 0, 1]
end
```

#### Narrowing

A check of a parameter or local binding narrows its range in the code
the check guards, so a value computed while the program runs needs no
`try_from` there:

```nomi
fn divide(n: Int, d: Int) -> Option<Int>
    if d == 0 then return None
    Some(Int.divide(n, d)) // d is a NonZeroInt here
end

fn first_or(xs: List<Int>, fallback: Int) -> Int
    if xs == [] then
        fallback
    else
        NonEmptyList.first(xs)
    end
end

fn last_index(size: Int) -> PositiveInt
    if size <= 1 then return 1
    size - 1 // size is 2 and up here
end

fn main()
    dbg divide(7, 2)    // Some(3)
    dbg first_or([], 0) // 0
    dbg last_index(4)   // 3
    n = 3
    dbg if n >= 0 then Int.pow(2, n) else 0 // 8
end
```

A comparison `x op k` or `k op x`, with `op` one of `==`, `!=`, `<`,
`<=`, `>` and `>=`, narrows `x`, a binding of Int, Decimal, Duration or
Float, when `k` is a constant of its base: a value whose range is one
value, a literal, possibly negated, or a binding of one. Where it is
true, `x > k` keeps of `x`'s range the values above `k`, `x >= k` those
from `k` up, `x < k` and `x <= k` those below, `x == k` the value `k`, and
`x != k` all but `k`: after `n >= 0 and n != 0`, `n` is a `PositiveInt`,
and `x != 0` makes `x` off zero. A comparison of an Int, Decimal or
Duration that is false is its opposite that is true: in the `else` of
`if n < 0`, `n >= 0` and `n` is a `NonNegativeInt`. A Float comparison
but `!=` is false when `x` is NaN, so a true one also proves `x` is no
NaN, and a false one proves nothing unless `x`'s range already rules NaN
out, except `x != k` false, which is `x == k`. So `x != 0.0` narrows
nothing of a Float that may be NaN, since `NonZeroFloat` holds no NaN.

`Iter.is_empty(x)` false, `x != e` true and `x == e` false, with `e` an
empty literal, `[]`, `{}` or `""`, narrow a List, Vector, Map, Set or
String to `NonEmptyList`, `NonEmptyVector`, `NonEmptyMap`, `NonEmptySet`
or `NonEmptyString` at its type arguments; `xs |> Iter.is_empty()` is
`Iter.is_empty(xs)`.

`not c` proves what `c` proves when it has the other value; `a and b` that
is true proves what each proves true, and `a or b` that is false what each
proves false. Two facts about one binding meet. A binding that is a std
subtype already narrows further the same way, so a `NonNegativeInt`
checked `!= 0` is a `PositiveInt`. A check narrows:

- the `then` branch of an `if` where its condition is true, and the
  `else` branch, with each `else if` after it, where it is false;
- the right operand of `and` where the left one is true, and of `or`
  where it is false: `d != 0 and n % d == 0` is no error;
- a `case` arm's body after its `when` guard, and in a `case` with no
  subject, an arm's body where its condition is true and every earlier
  unguarded condition false;
- the rest of the block after an `if` whose first branch leaves (`return`,
  `break`, `continue`, a trap or another expression of type `Infallible`)
  where the condition is false, after an `if` whose `else` leaves where it
  is true, after an `else if` chain with no final `else` whose branches all
  leave where every condition is false, and after `assert c` where `c` is
  true and `refute c` where it is false.

Bindings are immutable, so a narrowed binding holds its value everywhere
in that code, a lambda's body included, and a name rebound there is a new
binding with its own type. Nothing else narrows: a value that is no
binding (`if n + 1 > 0` says nothing of `n + 1`), a field read, an `is`
test, and a comparison of two bindings neither of which is a constant
(`a > b`). A binding of a user's subtype keeps its type, though its range
narrows: after `s > 0` with `s` a `Small`, `s` is a `Small` that passes
where a `PositiveInt` is expected. A narrowed value is its base at run
time, as every subtype's is.

### Validated types

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#validated-types).

A `type` with a `when` rule is a *validated type*: a new type, which
converts to nothing and which nothing converts to, whose values all
satisfy the rule. A `type` whose inner type is a subtype, or an
intersection of subtypes, is built the same checked way, against the
inner type's rules.

```nomi
fn is_email(s: String) -> Bool
    String.contains(s, "@")
end

type Email String when is_email

type UserId PositiveInt

fn user(e: Email) -> String
    Email(s) = e
    s
end

fn main()
    dbg user(Email("a@b.c"))   // "a@b.c"
    dbg Email.try_from("nope") // None
    dbg UserId(5)              // UserId(5)
    dbg UserId.try_from(0)     // None
end
```

A value enters one through a literal, `Email("a@b.c")` or `UserId(5)`,
checked when the program is compiled as a subtype's literal is
(``"nope" is not an Email: `is_email` is false``, ``0 is not a UserId:
UserId wraps PositiveInt, which is a subtype of NonZeroInt, and
NonZeroInt's rule `fn n -> n != 0` is false``); through `T.try_from(x)`
on a value of the base, which for an opaque type is private to its file
as its call form is (*Opaque distinct types*); through `T.unchecked(x)`
in the declaring file;
and, for a `type` over a subtype with no rule of its own, from a value
already of that subtype, `UserId(p)` with `p` a `PositiveInt`. Any other
argument of the call form is an error that names `try_from` on it:
`UserId(n)` with `n` an `Int` is
`UserId(...) takes a literal or a PositiveInt: UserId wraps the subtype PositiveInt, and an Int computed when the program runs may not be one`,
with the help ``check the value with `UserId.try_from(n)`, an `Option<UserId>` ``.
`e: Email = s` with `s` a String is `type mismatch: expected Email, got
String`, whose help names `Email.try_from`. The call form named as a
function value is an error as a subtype's is. Destructuring,
`Email(s) = e`, reads the inner value as any distinct type's does. A plain
`type UserId Int`, with no rule anywhere, keeps its plain call form and
has no `try_from`. So does a `type` whose inner type holds a subtype
without being one, `type Ids List<PositiveInt>`: its call form takes a
`List<PositiveInt>`, whose elements are checked already, as `type Contact
Email` takes an `Email`. Which `type`s wrap a subtype is decided by what
their inner type's name resolves to, so a subtype another file declares,
or names through a `typealias`, counts as one this file declares does.

A validated type, and a `type` over a subtype, is declared at the top of
a file, as a subtype is, since its `try_from` comes with an impl and an
impl cannot stand in a body. Inside a body, `type Local PositiveInt` is
``a type over a subtype is declared at the top of a file: move `Local` out of the body, so that it has `Local.try_from` ``,
and `type Code String when is_code` is
``a type with a rule is declared at the top of a file: move `Code` out of the body``.

A validated type, and a `type` over a subtype, implements `TryFrom` of its
base (§12 *TryFrom*) because of its rule and its base: `try_from` is
`TryFrom`'s function, with a body the compiler supplies, which checks the
base's rules, then the type's own, and answers `Some` or `None`. A
generic one implements it at its parameters: `type Short<T> List<T> when
fn xs -> List.length(xs) < 3` implements `TryFrom<List<T>>` for
`Short<T>`. `impl TryFrom<Int> for Percent`, with `type Percent Int when ...`, is
`` `TryFrom<Int>` for Percent is supplied by its rule and cannot be implemented; every `type` with a rule over Int satisfies `TryFrom<Int>` automatically — remove this `impl TryFrom<Int> for Percent` ``.
`unchecked` stays the type's own function.

### Type Aliases

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#type-aliases).

`typealias Name Target` makes `Name` another spelling of `Target` in type
positions. The two are one type, and nothing converts between them since
nothing needs to. No `=` comes between them:
`typealias Name = Target` is ``a typealias has no `=`: write `typealias X T` ``.
The target may be an intersection: `typealias Item Priced and Costed`
names the interface conjunction, and `typealias Both NonNegativeInt and
NonZeroInt` the intersection of subtypes.

```nomi
typealias Point {x: Int, y: Int}

fn shift(p: Point) -> Point
    {x: p.x + 1, y: p.y}
end

fn main()
    dbg shift({x: 1, y: 2}) // {x: 2, y: 2}
end
```

An alias may name any type the file sees, including an alias declared below
it. An alias that reaches itself is an error at each alias on the cycle:
`typealias 'X' refers to itself (X → Y → X)`.

An alias may take type parameters, which its target names, and bound them
with `where` after the target: `typealias Pair<T> (T, T)`, `typealias
Sortable<T> List<T> where T: Comparable`. A use is the target at the
use's type arguments, `Pair<Int>` is `(Int, Int)`, and a use whose
argument breaks the bound is an error at the use:
`Sortable<Circle>` is
``Circle does not implement Comparable (required by `where T: Comparable` on typealias Sortable)``.
A use without its arguments, `Pair`, is
``typealias Pair takes 1 type argument: write `Pair<T>` ``.

An alias has no rule, since it is its target under another name:
`typealias Small Int when fn n -> n < 10` is
``a typealias has no rule, since it is its target under another name: for the values of Int that satisfy a rule, declare `subtype Small Int when ...` ``.

Functions do not hang off an alias. `impl P`, `impl Display for P` and
`derive Debug for P`, where `P` is an alias, are errors that name the target
(`` `P` is a typealias for `Pt`; write `impl Pt` ``), and so is a call
through one, `P.get(p)` (`` `P` is a typealias for `Pt`; call `Pt.get` ``).

### Generics

Taught in [Generics](/generics/).

A struct, enum, distinct type, interface or function may take type
parameters, in angle brackets after its name: `struct Pair<A, B>`,
`fn first<A>(list: List<A>) -> Option<A>`. A use writes type arguments the
same way (`List<String>`, `Result<User, String>`); a call usually infers
them (§5). A use writes as many type arguments as the type takes, so a type
declared without type parameters, a primitive or a type parameter takes
none: `S<Int>` for `struct S` is
`` `S` expects 0 type arguments, got 1 ``. A `where` clause bounds a type parameter by interfaces (§12
*Bounds and dispatch in generic code*).

#### Default type arguments

A struct, enum, distinct type, host type or interface may give a type
parameter a default type, written after `=`: `pub opaque type Route<P = Unit> String`.
A use that leaves the argument out takes the default, so `Route` is
`Route<Unit>`, and `Pair<Int>` is `Pair<Int, String>` for
`struct Pair<A, B = String>`.

```nomi
struct Tagged<T, Tag = String>
    value: T
    tag:   Tag
end

fn label(t: Tagged<Int>) -> String
    "${t.tag}: ${t.value}"
end

fn main()
    dbg label(Tagged{value: 1, tag: "n"}) // "n: 1"
end
```

The rules:

- The parameters with a default come after every one without:
  `struct Pair<A = Int, B>` is
  `` type parameter `B` has no default, so it can't follow `A`, which has one ``.
- A default names no type parameter, its own declaration's or another's:
  `` a type parameter's default can't name a type parameter (`A`) ``. It is
  resolved where the declaration is, so it may name a type the file using
  the declaration does not import.
- A default may name its own declaration only with every argument
  written. `struct Box<T = List<Box<Int>>>` is fine, since `Box<Int>` takes
  no default. `struct Box<T = Box>` would expand forever (`Box<Box<Box<…>>>`)
  and is an error at the default:
  `` `Box`'s default for `T` names `Box` itself, which would never end; write the argument where `Box` is used ``.
  The same holds for a cycle through another declaration's default
  (`struct A<T = B>` and `struct B<U = A>`), reported at one default on it:
  `` `B`'s default for `U` names `A`, whose default names `B` again, which would never end; write the argument where `A` is used ``.
- A function's type parameters take none, since a call infers them:
  `` a function's type parameter can't have a default; a call infers `T` ``.
- A default fills a type position only: an annotation, a parameter or result
  type, a field's type, a type argument. There, `Route` with no arguments is
  `Route<Unit>`, never a type to infer: `fn to(route: Route)` takes only a
  `Route<Unit>`, while a generic function's own type parameter is still
  inferred at each call. Where a type name stands for the declaration rather
  than a use of it (an `impl` or `derive` header, a struct literal's or a
  pattern's head), it keeps its parameters open as before, and they are
  solved from the values.
- A use with too few arguments for the parameters without a default is
  `` `Pair` expects 1 to 2 type arguments, got 0 ``.

A type prints with its trailing default arguments left out, in hover and in
error messages: a `Route<Unit>` prints as `Route`, and a
`Tagged<Int, String>` as `Tagged<Int>`.

A function's type parameters are solved at each call from its arguments,
then from the type its result is expected to have for those the arguments
leave open (§5 *Type arguments from the arguments*). So each must appear in a
parameter's type, in the return type, or in a `where` bound on a type
parameter that does (`fn any<I, T>(items: I) -> Bool where I: Iter<T>`). A
`where` bound alone does not count. One that appears nowhere else is an
error at its declaration:
``type parameter `U` is not used in the signature of `f`, so no call can determine it``.

A function in an inherent `impl Span<T>` block may use the block's `T`, or
declare its own, which shadows it. A function whose signature does not name
the block's `T` does not depend on it, and a call to it names no instance
(`Span.seven()`). Its body may not name that `T` either:
`` `T` is `Span`'s type parameter, and `seven`'s signature does not determine it ``.
The same holds in an inherent block on an interface (`impl Iter<T>`). A
function of an interface impl may use the block's `T` whatever its
signature names, since a call through the interface names the implementor,
and so `T`: in `impl Unit for Wrap<T> where T: Unit`, `fn symbol() -> String`
may call `T.symbol()`, and `Unit.symbol<Wrap<Meters>>()` runs it with `T` as
`Meters`. A call through the implementor's name writes no instance of it,
so when the signature names neither `self` nor `Wrap`, `Wrap.symbol()` is
`` `Wrap.symbol` names no instance of `Wrap`, and its impl is for every `Wrap<T>`, so this call does not say which one it runs for ``,
whose help writes the call through the interface. An impl for one instance
(`impl Unit for Box<Meters>`) is that instance, so `Box.symbol()` runs it.

A generic function is instantiated for each tuple of type arguments it is
called with. A generic function that reaches itself, directly or through
others, at a type built from its own type parameter needs infinitely many
instances, and is an error at the call that grows the type:

```nomi
struct Box<T>
    inner: T
end

fn grow<T>(x: T, n: Int) -> Int
    if n == 0 then
        0
    else
        grow(Box{inner: x}, n - 1) // T = Box<T>: an error
    end
end
```

The error is
`` `grow` instantiates `grow` at `T = Box<T>`, a type that grows on every recursive call, so it needs infinitely many instances (polymorphic recursion); each generic function is instantiated per type, so this is rejected: recurse at the same type parameters, or through a non-generic helper or an interface-typed parameter ``.

Recursion at the same type parameters, at a concrete type, or with the
parameters permuted is allowed.

A generic type is laid out for each tuple of type arguments it is used at.
A generic struct, enum, distinct type or subtype that holds itself, in
a field, a variant's payload or its inner type, directly or through other
types, at a type argument that strictly contains one of its own type
parameters needs infinitely many instances, and is an error at the type of
the part that grows the argument:

```nomi
struct Nested<T>
    value: T
    inner: Option<Nested<List<T>>> // T = List<T>: an error
end
```

The error is
`` `Nested` holds itself at `T = List<T>` in `inner`, so the argument grows at every level and `Nested` needs infinitely many instances (polymorphic recursion); each generic type is laid out per type argument, so this is rejected: hold it at its own type parameters, or at arguments that do not grow ``.
Through another declaration the error also names the path back:
`struct Right<U>` holding `Option<Left<List<U>>>`, where `struct Left<T>`
holds `Option<Right<T>>`, is an error at `Right`'s field that says
`` `Left` holds `Right` again (Right → Left → Right) ``. A typealias is
its target, so a part written as an alias is read as the target. The rule
holds where a finite value exists, as for `enum Perfect<T>` with `Leaf(T)`
and `Node(Perfect<(T, T)>)`: `Perfect<Int>` is laid out with
`Perfect<(Int, Int)>` inside it, and so on without end. A type may hold
itself at its own type parameters (`Tree<T>` holding `List<Tree<T>>`), at
a concrete type (`Option<Tagged<Int>>` in `Tagged<T>`), or with its
parameters permuted (`Pair<A, B>` holding `Option<Pair<B, A>>`), and may
hold a larger instance of a type that does not hold it back
(`Option<Box<List<T>>>`).

### Type witnesses

`Type<T>` is a prelude type whose value stands for the type `T` and holds
nothing else. A type name is a value of `Type<T>` where a `Type<T>` is
expected, as a function lets a caller pass a type rather than a value of it:

```nomi
type TraceId String

fn first_of<T>(xs: List<T>, _type: Type<T>) -> Option<T>
    Iter.first(xs)
end

fn main()
    dbg first_of([TraceId("a")], TraceId) // Some(TraceId("a"))
end
```

Anywhere else a type name is a value only in the forms §4 *Distinct Types*
lists, and otherwise `` `Int` is a type, not a value ``. No code builds or
reads a `Type<T>` value. `Context.value(c, TraceId)` (§15) takes one. Two
witnesses are equal when they name the same type (§5 *Equality*), and one
inspects as `Type<TraceId>`.

## 5. Type relations

### Type identity

A declared type's identity is its declaring file and its name. Two files may
each declare a `Point`, and the two are different types. A struct, an enum
and a distinct type are each distinct from every other type, their
representation included.

An interface's identity is its declaring file and its name too. Two files
may each declare a `Show`, and an impl of one is no impl of the other. With
only `impl a.Show for User`, the call `b.Show.show(u)` is the error
``no impl of `b.Show` for `User` (required by the call `b.Show.show`)``, and a bound
`where T: b.Show` or a value `s: b.Show = u` is rejected the same way. A
call, a bound and a value find only impls of the interface they name,
however the program spells it. An impl is likewise of the interface its
header names: after `import std::resources.{Release as Freeable}`, `impl
Freeable for Lock` is Lock's `Release`, and beside an `impl Release for
Lock` it is a duplicate impl.

A tuple type and an anonymous struct type are structural: two of the same
shape are one type (§4 *Built-in Types*). A function type is structural. A
`typealias` is another spelling of its target and adds no type (§4 *Type
Aliases*). A generic type's instances are the same type exactly when their
type arguments are.

### Assignability

A value of type `S` may stand where type `T` is expected when:

- `S` and `T` are the same type;
- `T` is an interface that `S` implements, and the value becomes an
  interface value (§12);
- `T` is an interface conjunction `A and B` whose every member `S`
  implements, or that `S`, itself a conjunction or an interface, holds every
  member of (§12 *Interface conjunctions*);
- `T` is an enum that embeds `S` (§4 *Embedded Types*), at the top or at
  any position that converts (*Covariant conversion*);
- `S` is a subtype and `T` is one of its ancestors, its base
  included (§4 *Subtypes*), or `T` is a subtype with no rule of its own
  whose every parent `S` converts to, at the top or at any position that
  converts; and where `T` is an interface, `S` converts to it as its base
  does;
- both are function types and `S` fits `T` by the rule below;
- both are one generic type, tuple, record or function type, and `S`
  converts to `T` part by part (*Covariant conversion*).

There is no other implicit conversion (*No Implicit Conversion*).

A tuple's elements and a record's fields convert as the value does:
`(1, p)` is a `(Int, Named)` when `p`'s type implements `Named`. `Iter`
takes its element by the same rule as the top, since an `Iter` only hands
its elements out: a `List<Int>` passes where an `Iter<Display>` is
expected, and a `List<List<Int>>` where a `List<Iter<Int>>` is.

A function of type `(P1) -> R1` fits where `(P2) -> R2` is expected when
each `P2` fits its `P1` and `R1` fits `R2`: parameters are contravariant and
results covariant. The two take the same number of parameters, except a
function whose parameters past the expected ones have defaults (§7 *Default
Arguments*) and the callback of a stateless `Iter.loop` (§13 *`loop`*). Inside a function type, one type fits another as a value
does: when they are the same, when a concrete type meets an interface it
implements, when a type meets an enum that embeds it, when both are
function types that fit, or when both are one generic type whose type
arguments convert (*Covariant conversion*): a `(Int) -> List<Int>` fits
where a `(Int) -> List<Display>` is expected. The rule holds at every
position a function value flows into,
a tuple's element and a record's field among them: `("a", f)` is a
`(String, (Int) -> Display)` when `f` is an `(Int) -> Int`.

```nomi
fn shown(x: Display) -> String
    Display.to_string(x)
end

fn pair(n: Int) -> List<Int>
    [n, n]
end

fn main()
    f: (Int) -> String = shown   // Int fits Display
    h: (Int) -> Iter<Int> = pair // List<Int> fits Iter<Int>
    dbg f(1)                     // "1"
    dbg Iter.to_list(h(2))       // [2, 2]
end
```

The reverse is an error, since the function would receive a value it does
not accept: `fn float_str(f: Float) -> String` as a `(Display) -> String` is
`type mismatch: expected (Display) -> String, got (Float) -> String`. Where
nothing says which way a value flows (the branches of an `if` or `case`, the
elements of an unannotated list, two arguments of one type parameter), two
function types must be the same. A collection literal beside a value of
that collection, as an operand of `+` or `-` or beside a spread in a list
literal, is built as the value's type, which says the way (§8 *Arithmetic
operators*, §3 *Spreads*). With
`fn pick<T>(c: Bool, a: T, b: T) -> T`, `pick(c, shown, float_str)` is
`argument 3: expected (Display) -> String, got (Float) -> String`; pass a
lambda of the one type instead: `pick(c, float_str, fn x -> shown(x))`.

### Covariant conversion

Values are immutable, so a list of `Int`s may stand where a list of
`Display`s is expected: nothing can put a `String` into it through the
wider type. Where a value of type `F<A>` is expected as `F<B>`, it converts
when each type argument converts by its parameter's *variance*:

- a **covariant** parameter converts as a value does: `F<A>` is an `F<B>`
  when `A` converts to `B`;
- a **contravariant** parameter converts the other way: `F<A>` is an `F<B>`
  when `B` converts to `A`;
- an **invariant** parameter compares exactly.

The rule applies again inside each type argument, at any depth.

| Type | Its parameters |
|---|---|
| `List<T>`, `Vector<T>`, `Set<T>`, `Option<T>` | `T` covariant |
| `Map<K, V>`, `Result<T, E>` | both covariant |
| a tuple, an anonymous record | each item and field covariant |
| a function type | its result covariant, each parameter contravariant |
| a struct, enum or distinct type the program declares | inferred from its fields, payloads or inner type |
| `Iter<T>` | `T` covariant (§5 *Assignability*) |
| any other std or host type, any other interface | invariant |

Nothing is written to declare a parameter's variance. It is read off where
the parameter appears in the type's fields, its variants' payloads, or a
distinct type's inner type (`type Ids<T> List<T>` is covariant in `T`): a
parameter that appears only where a value is held (a field, a collection's
element, a function type's result) is covariant; one that appears only in a
function type's parameter is contravariant, and two such flips cancel; one
that appears in both is invariant. A parameter passed to another generic
type takes that type's variance at the place it is passed, and a recursive
type reads its own. A parameter no field names converts either way and is
treated as covariant.

```nomi
struct Box<T>
    item: T
end

struct Sink<T>
    take: (T) -> String
end

struct Cell<T>
    get: () -> T
    put: (T) -> Unit
end

interface Store
    fn size(s: self) -> Int
end

struct Memory
    n: Int
end

impl Store for Memory
    fn size(m: Memory) -> Int
        m.n
    end
end

fn shown(d: Display) -> String
    "<${d}>"
end

fn opened(open: () -> Result<Store, String>) -> Int
    case open()
        Ok(s)  -> Store.size(s)
        Err(_) -> 0
    end
end

fn main()
    m = Some([1, 2])
    wide: Option<Display> = m // each List<Int> boxed as a Display
    bx = Box{item: [1]}
    b: Box<Display> = bx // Box's T is covariant
    sink = Sink{take: shown}
    ints: Sink<Int> = sink             // Sink's T is contravariant
    dbg Option.map(wide, shown)        // Some("<[1, 2]>")
    dbg shown(b.item)                  // "<[1]>"
    dbg ints.take(5)                   // "<5>"
    dbg opened(fn -> Ok(Memory{n: 3})) // 3
end
```

`Cell`'s `T` is invariant, so a `Cell<Int>` is no `Cell<Display>`:
`c: Cell<Display> = cell` is `type mismatch: expected Cell<Display>, got
Cell<Int>`, with help `Cell<Int> does not convert to Cell<Display>: Cell's
type parameter T is invariant, since Cell's fields both hold a T and take
one; build a new Cell<Display> from the value's parts`. A contravariant
parameter refused the way it would widen names the conversion it needs:
`s: Sink<Display> = floats` is `Sink<Float> does not convert to
Sink<Display>: Sink's type parameter T is contravariant, since Sink's fields
only take a T, so it converts only where Display converts to Float`. A
collection of functions whose parameter would narrow is refused the same
way: `gs: List<(Display) -> String> = fs` with `fs: List<(Float) -> String>`
is `List<(Float) -> String> does not convert to List<(Display) -> String>:
each function takes only Float, and one of type (Display) -> String may be
passed any Display`. An element that implements no interface the expected
type holds is named: `ys: List<Display> = units` is `List<Unit> does not
convert to List<Display>: Unit does not implement Display`. An interface
value converts to no other interface, at any depth.

A `Set`'s elements and a `Map`'s keys convert too. Converting one into an
interface value keeps its equality and its hash: a value whose type names
it at run time is the interface value itself, and any other is held with
the static type it had, and compares and hashes by that type's own
equality (§5 *Hashing and keys*). Two distinct keys of one type therefore
stay distinct, and the converted `Set` or `Map` has the size it had:
with `m = Set[Some(1), None]`, `s: Set<Hashable> = m` holds two elements,
and `Set.contains(s, Some(1))` is `True`.

A conversion costs nothing when each element already is a value of the
wider type: an `Int`, `Float`, `Bool` or `String`, a declared struct, a
declared enum with no `embeds` variant or a declared distinct type entering
an interface it implements is the interface value itself, so a `List<Int>`
is a `List<Display>` as it is, whatever its length, and so is a function
whose parameters and result convert that way. Any other conversion is work
proportional to the value: a `List`, `Vector`, `Set` or `Map` is built
again with each element converted, an `Option`, `Result`, user enum or struct,
tuple or record is built again with its parts converted, and a function is
wrapped in one that converts its arguments and its result on each call. A
value built where the wider type is expected converts each element as it
is built, with no second pass: `ys: List<Display> = [1, Some(2)]`. So does
a literal operand of `+` or `-` beside a value of the same collection, and
the elements of a list literal beside a spread: `[1, Some(2)] + ds` and
`[1, ..ds]` are `List<Display>`s for `ds: List<Display>`. The branches of
an `if` or `case` whose type is written join as it when each converts to
it: `c: List<Display> = if f then ints else words`.

### Annotations and local inference

Taught in [Bindings & Expressions](/bindings-and-expressions/#type-annotations).

#### Type Annotations

A function's parameters and its non-`Unit` result are annotated (§7). Inside
a body, types are inferred. An annotation is optional on a binding
(`name: Type = value`), a `once` and a lambda parameter. A struct field
always has one. A destructuring pattern takes none.

An annotation on a binding drives inference into the value and is checked
against it: `m: Map<String, Int> = {}` gives the empty literal its types,
`f: (Int) -> Int = fn x -> x + 1` gives the lambda its parameter type, and a
value that cannot have the annotated type is an error. A function type is
written `(Args) -> Result`.

```nomi
fn main()
    m: Map<String, Int> = {}
    f: (Int) -> Int = fn x -> x + 1
    dbg Map.size(m) // 0
    dbg f(1)        // 2
end
```

#### Locally-determined bindings and turbofish

Taught in [Bindings & Expressions](/bindings-and-expressions/#how-far-type-inference-reaches).

The checker infers types as far as it can, but a binding must be locally
determined: its type must be readable from its own line, not from how the
binding is used later. A binding whose type is wholly unresolved, carrying
no concrete type because its only type parameters are unsolved, is an error:

```nomi
xs = [] // error: List<?>

m = {} // error: Map<?, ?>

x = None // error: Option<?>

ch = Channel.buffered(4) // error: Channel<?>
```

`xs = []` is
``binding 'xs' type is not locally determined: it has unsolved type parameter(s) — add an annotation (`xs: T = …`) or a type argument (`f<T>(…)`)``.
A later use never rescues it. The type comes from an annotation, or from
explicit type arguments on a call, written after the callee as its signature
writes them:

```nomi
xs: List<Int> = []

ch = Channel.buffered<Int>(4)

f1 = Some<Int>(42)
```

A type argument is a type annotation: a name that is no type is
`unknown type "Nope"` at the name (`ident<Nope>(1)`). Explicit type
arguments are an ordinary call form, a pipe stage's included
(`2 |> pick<String>()`). A call writes one for each type parameter its
function takes, so a function with no type parameters takes none:
`g<Int>()` for `fn g() -> Int` is `expected 0 type argument(s), got 1`. A
function value, a lambda bound to a name included, is never generic and
takes none either.

A construction's type arguments are its type's own parameters, in the
order the type declares them: `Tagged<String>(("n", "x"))` for `type
Tagged<T> (String, T)`, `Box<Int>({v: 1})` for a struct's call form, and
a variant's are its enum's, so `Err<Int, String>("bad")` is a
`Result<Int, String>`. The argument is then checked against that instance:
`Box<String>({v: 1})` is `field 'v' of Box: expected String, got Int`. A
parameter with a default may be left out, as in a written type. A type
with no type parameters takes none: `UserId<Int>(5)` for `type UserId Int`
is `expected 0 type argument(s), got 1`.

A binding whose type carries any concrete information is determined, even
with a parameter still open: `x = Ok(42)` is a `Result<Int, ?>`, and
`n = Some(Ok(42))` an `Option<Result<Int, ?>>`.

The enclosing function's return type is an expected type only for its
result: its tail, a `return`, and the arms of a `case` or `if` in tail
position. A block's expected type reaches only its tail: in
a `once o: Shape = begin ... end` whose body is `b = .Circle(2.0)` and then
`b`, `.Circle` names no enum.

A name a pattern binds, in a `case` arm, an `if Pattern = value`, a
destructure or a binding with `else`, must not have a wholly unknown type
once the file is checked. `Ok(z) = Option.to_result(None, "none") else return`
is
``the type of 'z' is not determined: nothing fixes the type the pattern binds it to — annotate the matched value (`v: T = …`) or give a call a type argument (`f<T>(…)`)``,
unless the `else` fallback or a later use of `z` fixes it.

A destructuring binding (a tuple, struct, distinct or variant pattern
followed by `=`, with or without `else`, or after `assert`) holds each name
it binds to the rule a `name = value` binding is held to, once the file is
checked: a name whose type is wholly unresolved is an error. With
`fn read<T>() -> (Box<T>, Bool)`, `(box, _) = read()` binds `box` to a
`Box<?>`, and is
``binding 'box' type is not locally determined: it has unsolved type parameter(s) — give the call a type argument (`f<T>(…)`) or annotate the matched value (`v: T = …`)``;
`(box, _) = read<Visit>()` is determined. `(a, b) = (None, 1)` is the same
error unless a later use of `a` fixes its type. In a `case` arm or an
`if Pattern = value`, a name whose type only contains an unknown part, as an
`Option` that can only be `None`, is accepted.

#### Type arguments from the arguments

A generic call's type arguments come from its arguments first. The type the
call's result is expected to have (an annotation, a parameter, a return
position) only fills the type parameters the arguments leave open. The
result then converts to the expected type as any value does (*Assignability*):
an `Int` into `Display`, an `Iter<Int>` into an `Iter<Display>`. So a
lambda's parameter has the type the other arguments give it, not the
interface the binding names:

```nomi
fn main()
    x: Iter<Display> = [1, 2, 3] |> Iter.filter(fn n -> n > 1)

    d: Display = Iter.loop(0, fn i ->
        if i > 2 then
            break i
        else
            i + 1
        end
    end)

    dbg Iter.to_list(x) // [2, 3]
    dbg d               // 3
end
```

`filter` is called at `Int`, so `n > 1` compares Ints, and its `Iter<Int>`
is the `Iter<Display>`. Each argument the arguments alone cannot type still
takes the expected type, for the type parameters its own parameter names
and the arguments before it left open:

- a dot-leading variant, an empty literal, `None`, a brace literal with no
  name, and a constructor or generic function named as a value:
  `c: Option<Color> = Some(.Red)`, `xs: List<Int> = Iter.to_list([])`;
- a lambda whose parameter nothing else types:
  `f: (Int) -> Int = ident(fn x -> x + 1)`;
- a bracket literal, an `if` or a `case` passed where the parameter is a
  bare type parameter, since the expected type says which collection the
  literal builds and how the branches join: `m: Option<Vector<Int>> =
  Some([1, 2])`.

A type parameter the result holds below its top comes from the arguments
by the same rule, and the result converts (*Covariant conversion*):
`Ok(Memory.new())` where a `Result<Store, Error>` is expected builds a
`Result<Memory, Error>`, which converts to the `Result<Store, Error>`, and
with `fn boxed<T>(v: T) -> Box<T>`, `b: Box<Display> = boxed(1)` builds a
`Box<Int>` and converts it. Only a type parameter the result holds at an
invariant position (an invariant parameter of a declared type, a std
struct's or host type's argument, an interface's other than `Iter`'s)
takes the expected type ahead of the arguments, since a type argument
there never converts: with `fn cell<T>(v: T) -> Cell<T>`, `c: Cell<Display>
= cell(1)` is a `Cell<Display>`.

Arguments that give the call a type that does not convert to the expected
one are an error at the call: `n: Int = Iter.loop("a", fn s -> if s == ""
then s else break s)` is `type mismatch: expected Int, got String`.

A range literal's endpoints are its arguments in the same way: each is
checked against the element of the `Range<T>` its position expects, so
`c: Range<List<Int>> = []..[]` is a range of `List<Int>`s, where with no
annotation the two empty lists settle nothing.

#### Determined type arguments

A call must determine every type argument its function makes values of,
wherever the call stands. A function makes values of a type parameter when:

- its signature mentions the parameter inside a function type; or
- a parameter's type holds the type parameter as a type argument of a type
  argument (`Iter<Result<T, E>>` holds `T` that deep), and its result holds
  the type parameter in a collection (`Result<List<T>, E>`). `Result.collect`
  is such a function; or
- the parameter's bound declares a function that takes no value of the type
  (`Iter.sum`'s `Summable` has `empty_sum()`). A bound whose every function
  takes a value of the type, such as `Comparable`, makes none.

A call's own result is a value too: a function whose result is the type
parameter itself makes a value of it, and so does a `!` or `?` that
unwraps the parameter out of the call's `Option` or `Result`. Discarding
the value or only printing it does not change that.

A type argument nothing in the enclosing declaration fixes is an error at
the argument that carries it:

```nomi
dbg Result.collect([]) // error: the element type of `[]` is not determined

dbg Option.collect([None]) // error: the type argument T of `Option.collect` is not determined

dbg Iter.sum([]) // error: the element type of `[]` is not determined

_ = Vector.get([], 0)! // error: the element type of `[]` is not determined
```

An unsolved type argument that no value has is not an error: the element of
an empty collection (`dbg []`, `Iter.to_list([])`, `List.first([])`,
`dbg Vector[]`, and `Iter.sum([[]])`, whose `T` is a `List` and whose sum is
`[]`) or the variant a value is not (`Ok(1)`'s error type). The fix is an annotation on the empty value or
explicit type arguments (`Result.collect<Int, String>([])`).

#### Lambda parameter types

A lambda parameter with a default and no annotation takes its default's
type, by the rule for a function's parameter (§7 *Parameter types from
defaults*): `add = fn a: Int, b = 1 -> a + b` needs no other annotation.
Where the lambda's position also expects a function type, the two must
agree: passed where a `(Int) -> Int` is expected, `fn x = "a" -> 1` is
`default value for parameter 'x' is String, expected Int`.

A lambda parameter with no annotation and no default takes its type from the
function type its position expects: a parameter's type at a call, a
function-typed annotation, a declared return type, or a generic parameter the
call's own expected type fixes where no other argument does (`f: (Int) -> Int
= ident(fn x -> x + 1)`, `g: Option<(Int) -> Int> = Some(fn x -> x * 3)`; §5
*Type arguments from the arguments*).

A parameter typed by a type parameter of the callee that nothing has solved
yet, and called in the body, is a function: the call solves it to a
function of its arguments' types, returning what the call's position needs.
`Actor.start(workers, 0, fn s, f -> f(s))` makes the actor's message type
`(Int) -> Int`, since the handler's result is the state, an `Int`.
A result nothing fixes leaves the type argument undetermined
(§5 *Determined type arguments*).

In a list, vector or set literal, or a map literal's keys or values, with no
expected type, the elements share one type, so a parameter annotated in one
element types the unannotated parameter at the same place in the others,
before or after it. A place is the lambda's position inside the element,
through tuples, records and nested collection literals: in
`[("a", fn d: Date -> f(d)), ("b", fn d -> g(d))]` the second `d` is a
`Date`. An earlier element that is a function value rather than a lambda
(`("a", month_name)`) types the places of the elements after it. Elements
that annotate different types at one place are an element type mismatch.

Anywhere else no function type is expected, it is `cannot infer type for
parameter x`, whose help says a later use does not type it. That covers an
unannotated binding (`f = fn x -> x`), a tuple element, a collection literal
whose elements type nothing at the parameter's place, and the argument of a
generic constructor with no expected type (`h = Some(fn x -> x)`).

### Type ascription

`value as Type` checks `value` with `Type` as its expected type, exactly as
`x: Type = value` does, and is a `Type`. It gives a type where no
annotation can stand: a field of an anonymous struct literal, an argument,
a list element. An untagged literal takes the type its position gives it
(§18 *Literals typed by their position*), and so do an empty collection and
`None`:

```nomi
import
    std::http.Route
    std::uuid.Uuid
end

once admin = {
    home: Route"GET /admin/{$}",
    user: `GET /admin/users/{id}` as Route<{id: Uuid}>,
    tags: [] as List<String>,
}

fn main()
    dbg admin.tags + ["new"] // ["new"]
    dbg None as Option<Int>  // None
end
```

`as` converts nothing (*No Implicit Conversion*). It admits what
assignability admits (§5 *Assignability*): a value as an interface it
implements, an embedded type as its enum, a collection under type-argument
conversion. Anything else is the annotation's error at the value: with
`x: Int`, `x as Float` is `type mismatch: expected Float, got Int`, with
the help `` `as` doesn't convert a value; write `Int.to_float(x)` ``. The
help names the converting function for the scalar pairs a cast in another
language covers (`Float.to_int`, `String.to_int`, `String.to_float`,
`Int.to_string` and the like).

`as` binds like `|>` (§8 *Operators and precedence*). Two in a row,
`a as T as U`, are
`` `as` can't follow another `as`: an expression has one ascribed type ``.
An `as` whose value has the type without it changes nothing and is no
error. Where the value is a name, a field read or a number literal
(`x as Int` with `x: Int`), the language server marks the `as`
unnecessary.

### No Implicit Conversion

Taught in [Scalars & Strings](/scalars-and-strings/#numbers-booleans-operators).

No value converts to another type implicitly. `Int`, `Float` and `Decimal`
are three types, and arithmetic needs one: `x + y` with an `Int` and a
`Float` is the error `` `+` needs two values of one type, and these are Int
and Float ``. Conversions are
functions: `Int.to_float(x)` is total, and `Float.to_int(y)` is an
`Option<Int>` that truncates toward zero and is `None` for NaN and the
infinities ([std::int](/reference/int/), [std::float](/reference/float/)).

### Equality and Ordering Semantics

Taught in [Interfaces & Dispatch](/interfaces-and-dispatch/#structural-equality).

#### Equality

`==` compares values, never references. With no `impl Equatable` of its
own, a value compares structurally: a struct by every field, an enum by
variant and payload, a tuple by position, a collection by its elements (and
a map by entries).

```nomi
struct User
    name: String
    age:  Int
end

fn main()
    dbg User{name: "Ada", age: 36} == User{name: "Ada", age: 36} // True
    dbg [1, 2] == [1, 2] // True
    dbg Some(1) == None  // False
end
```

`==` on `Float` is reflexive: `nan == nan` is `True`, so pattern matching,
`Option` equality and container equality agree. This differs from IEEE 754 in
`==` only. `==` compares every bit otherwise, and
`Float.equal_within(a, b, tolerance)` compares within a tolerance.

Two `Regex` values are equal when their patterns are, and hash alike, at
the top or inside a collection, a tuple or a payload.

Two `Dynamic` values are equal when their contents are, as JSON values (an
object's keys in any order, an Int never equal to a Float), and hash alike.

Two `Type<T>` witnesses are equal when they name the same type, and hash by
that type, so a witness can be a `Set` element or a `Map` key.

These have no equality: a function, an `Iter`, and a handle to running state
or to a resource outside the program. The handles are std's `Task`, `Sender`,
`Receiver`, `Arm`, `Context` and `Supervisor`; std::io's `Reader`, `Writer`
and `File`; std::fs's `FileSystem`; std::process's `Child` and `Fake`;
std::http's `Client`, `Exchange`, `Listener`, `Incoming`, `Mux`, `Jar` and
`Handle`, and std::tls, std::net and std::udp's `Certificate`, `TlsConfig`,
`Conn` and `TcpListener`; std::csv's `Records` and std::xml's `Tokens`. A
function's only identity is its closure, an `Iter` is a function over its
source, and a handle names something running or held outside the program.
`==` or `!=` on one is a compile error:

- `` `==` cannot compare `(Int) -> Int`: a function value has no equality ``;
- `` `==` cannot compare `Sender<Int>`: a handle to running state has no equality ``.

A value that holds one, in a field, a tuple or record part, an element or a
payload, has no equality either:
`` `==` cannot compare `Feed`: it holds `Iter<Int>`, and an Iter has no equality; collect it into a list with `Iter.to_list` to compare its elements ``.
A struct, enum or distinct type whose own `impl Equatable` gives `equal` a body
compares through it instead, whatever its fields and type arguments hold: it asks of
them only what the impl's `where` clause says. A derived `Equatable`, and
std's `List`, `Map`, `Set` and `Vector`, compare every part, so `[f] == [f]`,
`Some(f) == Some(f)` and a `derive Equatable` over a function field stay
errors. `Hashable` follows the same rule.

#### Equality bounds

A `where T: Equatable` bound means that `T` has equality: it accepts exactly
the types `==` accepts. A type with a hand-written or derived `impl Equatable`
meets it through that impl. A struct, enum or distinct type with no impl of
its own meets it by its structural equality, and `==` and `Equatable.equal`
on it in the body compare it as `==` does outside, through any nested type's
own impl. A function, an `Iter`, a handle, and a value with no impl of its own
that holds one do not meet it (`` (Int) -> Int does not implement Equatable
(required by `where T: Equatable`) ``, and for a struct `Op` holding a
function, `` Op does not implement Equatable (required by `where T:
Equatable`) `` with the help `` `Op` holds `(Int) -> Int`, and a function
value has no equality ``).

```nomi
struct Point
    x: Int
    y: Int
end

fn same<T>(a: T, b: T) -> Bool where T: Equatable
    a == b
end

fn main()
    p = Point{x: 1, y: 2}
    dbg same(p, Point{x: 1, y: 2})    // True
    dbg p in [p]                      // True
    dbg List.starts_with([p, p], [p]) // True
end
```

So `p in [q]`, `List.contains`, `Iter.contains`, `List.starts_with` and
the other std functions bounded by `Equatable` take `Point` with no
`derive Equatable`. A `where T: Hashable` bound follows the same rule
(*Hashing and keys*). `Comparable` does not: ordering needs a declared
impl (*Ordering requires a declared `Comparable`*).

#### Custom equality via `impl Equatable`

A hand-written `impl Equatable` replaces the structural comparison for its
type. It holds wherever the value sits: `==` on a container, a tuple, or a
struct or enum without its own `Equatable` compares each nested value of
that type through its impl, so `[a] == [b]`, `Some(a) == Some(b)` and
`Map.get({a => 1}, b)` agree with `a == b`. Primitives compare directly.

#### Hashing and keys

A `Map` finds its keys and a `Set` its elements by `==`. So any value with
equality may be a key, at any depth: a tuple, a list, a struct, a distinct
value. A value with no equality may not: building a `Set` or `Map` over one
is an error at the expression that builds it
(`` `Iter<Int>` cannot be a Set element: an Iter has no equality or hashing; collect it into a list with `Iter.to_list` first ``).

Keys match by `==`, so a NaN key is found again, and `-0.0` and `0.0` are
one key. A key of a type with a hand-written `impl Equatable` matches
through it at any depth. The bucket comes from the type's hand-written
`impl Hashable` when it has one; a type whose `Equatable` is hand-written and
whose `Hashable` is not hashes by its type alone, so all its keys share a
bucket. Every other value hashes structurally, by its shape and each part's
hash, as a derived `Hashable` does (§12 *Derive*). An anonymous record
hashes alike whatever order its fields are written in, and Map equality and
hashing ignore order. An `impl Hashable` must give equal values equal
hashes.

`Hashable.hash` promises only that: equal values hash equal, and a value
hashes the same each time within one run. Which Int it gives for a value
with no hand-written impl is not specified, and may differ between runs
and between versions of Nomi, so no program may depend on a particular
hash. A run keys its hashes of Strings, of Bytes and of every value made
of parts (a tuple, a struct, a variant's payload, a collection) by a seed
it draws at random when it starts, so keys chosen outside the program
cannot be made to share a bucket. An Int and a Float hash as their own
bits.

Code the compiler runs once for every run of the program may not read a
hash the seed keys: a subtype's rule, a typed literal's handler or a
checked literal's `check` that can reach one is an error with the code
`seeded-hash` (§19 *Effects*). Building a `Map` or `Set` there is not
reading a hash: a Map iterates in insertion order and compares by `==`,
so its answers are the same under every seed. A key of a type with a
hand-written `impl Hashable` is hashed by running it, so keying a Map
there by such a type whose `hash` reads a hash is the same error: where
that hash disagrees with `==`, it decides which keys share an entry, and
a `hash` that can fail on the value it computes fails under some seeds
only. Neither is the hash of an
Int, a Float, a Byte or `Unit`, which no seed keys: `Hashable.hash(n)` of
an Int is `n`, for an Int past 64 bits too.

A key or element needs equality, not an impl: `{p => 1}`, `Set[p]`,
`Iter.to_set` and `Iter.group_by` take a struct `P` with no `Equatable` or
`Hashable` of its own, as `p == p` does. A `where T: Hashable` bound accepts
what a key accepts: a type with an `impl Hashable` hashes through it, and any
other type with equality meets the bound and hashes in the body as its keys
do, so `Hashable.hash(a) == Hashable.hash(b)` whenever `a == b`. A derive
asks the same of each field: `derive Hashable for W` over a field of type
`P`, `List<P>` or `(Int, P)` is accepted, and over a `P` that holds a
function is ``no impl of `Hashable` for `P` (required via `derive Hashable
for W`, field `f`)`` (§12 *Derive*).

#### Ordering requires a declared `Comparable`

`<`, `>`, `<=` and `>=` call `Comparable.compare`, and a type has no
ordering unless it declares one. Structural equality has one meaning; a
structural order would depend on field order, so a reordering refactor would
change it silently. On a struct with no `Comparable`:

```nomi
Point{x: 1, y: 2} < Point{x: 3, y: 4}
// error: no impl of `Comparable` for `Point` (required via ordering operator `<`)
// help: add `derive Comparable` to `Point` or write an `impl Comparable for Point` block
```

`derive Comparable` orders field by field (§12). The std types with a
natural order implement `Comparable`: `Int`, `Float`, `Decimal`, `String`,
`Bool`, `Duration` and the calendar types. `List<T>` and `Vector<T>` are
`Comparable`, lexicographically, when `T` is. `Map` and `Set` are not.

`Comparable` also gives every implementing type `min`, `max` and `clamp`,
open defaults written over `compare`: `Int.min(a, b)`, `Date.clamp(day,
first..=last)`, `Version.max(a, b)` on a derived impl, or `Comparable.min(a,
b)` in a function generic over `T: Comparable`. A tie returns the first
argument. `clamp(value, range)` holds `value` to a `ClosedRange<T>` (§3
*Range literals*): the range's start when `value` is below it, its end when above,
and an unbounded `Range.from(n)` clamps only below. A backwards range
(`10..=1`) or an exclusive one (`0..10`) is an error when the program is
compiled, and a range computed when it runs goes through
`ClosedRange.try_from`. `Float` overrides all three: `min` and `max` are IEEE
754-2019's `minimum` and `maximum` (NaN if either operand is NaN, and `-0.0`
below `0.0`), and `clamp` keeps a NaN value NaN. A Float range with a NaN end
holds no value, so it is no `ClosedRange`, and no literal is NaN.

A tuple is `Comparable` when every element is: the first elements decide
unless they are `Equal`, then the second, and so on, and an element after
the deciding one is never compared. `(1, "b") < (2, "a")`. Ordering a
tuple with a non-`Comparable` element is a compile error naming the element
type. An anonymous struct is never `Comparable`, since nothing declares it:
``no impl of `Comparable` for `{x: Int}` (required via ordering operator `<`); anonymous struct types cannot implement `Comparable` ``.
Its equality is unaffected.

#### NaN and the three comparison behaviours

`Float` compares in three ways:

| | Behaviour | On NaN |
|---|---|---|
| `==`, `!=`, `Equatable.equal` | reflexive | `nan == nan` is `True` |
| `Comparable.compare` | a total order, NaN highest | `Float.compare(nan, nan)` is `Equal` |
| `<`, `<=`, `>`, `>=` | IEEE 754 | `nan <= nan` is `False` |

`==` is reflexive so that a NaN key is found again in a `Map`, and
`Hashable` for `Float` treats every NaN, and both zeros, as one value.
`compare` agrees with `==`, so a sort is deterministic and gathers NaNs at
one end. So `a == b` does not imply `a <= b` when both are NaN.

## 6. Declarations and scope

### Top-level declarations

A file's top level holds declarations only: imports, functions, types,
interfaces, impls, derives, `once` values, tests, host declarations and
keyword declarations (`phrase`, `noun`, `embed`). A binding, an expression or another statement at top
level is an error; code runs inside a function. `x = 3` at top level is
`` a binding cannot be written at file level: a file binds a value only with `once` ``.

A top-level declaration is internal to its package unless it is `pub` or `private` (§14 *Visibility*).

### Bindings

Taught in [Bindings & Expressions](/bindings-and-expressions/#bindings).

Inside a body, `pattern = value` evaluates `value`, matches it against the
pattern and binds the names the pattern introduces. A binding is immutable;
there is no assignment. A pattern that can fail takes an `else` (§10). An
optional annotation follows a plain name: `count: Int = parse(s)?` (§5).

```nomi
fn main()
    x = 5
    (a, b) = (1, 2)
    dbg x + a + b // 8
end
```

Every name a binding, a parameter or a pattern introduces must be read. That
includes a destructured parameter's names, the names a `case` arm, an
`else` arm, an `if` condition or a test's setup binding introduces, and
parameters in every body (`boot` and interface defaults included). An
unread one is an error: `fn f((word, count): ...)` that reads only `count`
is `parameter 'word' is never read`. A value kept on purpose is bound to a
discard name (§2 *Discard names*). A struct pattern leaves out a field it
does not need (`.Suspended{reason:}`). An impl function that must keep a
parameter's name may discard it in its body with `_ = name`. A bodyless
declaration has nothing to check.

### Shadowing and redeclaration

An ordinary `name = value` binding may shadow an earlier binding or
parameter in the same scope, and a file-level declaration. The newest binding
is the one later code reads, and it may have another type. A closure made
before the shadow keeps the value it captured (§7 *Closures*).

```nomi
fn normalize(name: String) -> String
    name = String.trim(name)
    name = String.lowercase(name)
    name
end

fn main()
    dbg normalize("  Ada ") // "ada"
end
```

Bindings in nested scopes (bodies, lambdas, `case` arms) may shadow outer
ones.

Every other declaration is declared once in its scope: two `once`, `fn`,
`type`, `struct`, `enum`, `interface` or `typealias` declarations of one
name, two parameters of one signature, and one name twice in a pattern are
errors (`'x' is already defined in this scope as a once binding`). A
top-level declaration that shares a name with an imported name is an error
too (§14). There is no last-wins rule.

### `once` Bindings

Taught in [Bindings & Expressions](/bindings-and-expressions/#file-level-values-with-once).

`once name = expression` declares a value computed the first time it is
read and kept for every later read. It stands at file scope or in an
inherent `impl` block, and is read like any value (`config.value`,
`Money.zero_usd`), with no call. It is the only file-level value computed
by code; an `embed` declaration (§18 *Embedded files*) names a value of
files read when the program is compiled.

```nomi
once max_retries = 3

struct Money
    cents: Int
end

impl Money
    once zero = Money{cents: 0}
end

fn main()
    dbg max_retries      // 3
    dbg Money.zero.cents // 0
end
```

- **Annotation.** `once name: T = expression` is optional; the type is
  inferred otherwise, and holds at every read, in any file and above the
  declaration. Unannotated `once` values whose values read each other have
  no type to infer:
  ``once 'a' has no type annotation and its value depends on its own type (a → b → a) — annotate it (`a: T = …`)``,
  at each.
- **Where.** In a function body it is
  `'once x' must appear at file level or in an inherent impl block — function bodies use ordinary 'name = value' bindings`.
  In an `impl Iface for Type` block it is
  ``interface impl blocks hold only `fn` / `host fn` implementation items; write `once` as a file or inherent impl declaration``.
  It may not stand in a type body either.
- **Effects.** The initializer may do anything a function call may. Its
  effects happen once, on the first read, even when several tasks read it at
  once.
- **No early exits.** An initializer is not a function body, so a `?`,
  `return`, `break`, `continue` or assertion written directly in it is an
  error at that token:
  `` `?` cannot be used in the initializer of `once port`: a `once` has no function to return from ``.
  A lambda, a nested `fn` or a `concurrent` block inside it is a boundary of
  its own.
- **Cycles.** A `once` whose initializer reaches the `once` itself is an
  error at its declaration, naming the chain:
  ``cyclic 'once' binding: `seed` depends on itself: it calls `read_seed` (main.nomi:7), which reads `once seed` (main.nomi:4)``.
  Each `once` on the cycle is reported. The check follows, across
  modules, what an initializer does while it runs: each `once` it reads,
  and each call whose function is known when the program is compiled, into
  that function's body and on. Those calls are a call by name or as
  `Type.f`, an interface function called on a value of a concrete type
  (`Display.to_string(money)`), a pipe stage or `tap` naming a function,
  and the lambda a `then` or `tap` stage applies. Every branch counts,
  whether or not initialization takes it. A lambda, capture or nested `fn`
  written anywhere else runs when something calls it, so a `once` that
  holds a lambda reading the `once` is no cycle:
  `once retry: () -> Int = fn -> retry() + 1`. Not followed are a call
  through a function value, an interface value or a type parameter, an
  operator's implementation, and a default argument. A cycle closed through
  one of those traps when the read re-enters, at that read:
  `line 2: cyclic 'once' binding: 'a' depends on itself`.
- **A function value has no defaults.** A `once` that holds a lambda or a
  partial capture is a value, and a call through it passes every argument
  (§7 *Default Arguments*).

### A name declared twice

Each name inside a declaration is declared once: a struct field, a
struct-shaped variant's field, an enum variant, an interface function, and a
function's or lambda's parameter declared twice is an error at the second,
with a note at the first: ``field 'x' is declared twice in struct `Point` ``.
So is a repeated field in an anonymous struct type.

Two functions on one line of an interface body are a parse error,
`interface functions are newline- or semicolon-separated`, as two fields on
one line of a struct body are (§4 *Structs*); a `;` may separate them.

### Resolution and declaration order

Names resolve over the whole program. A top-level declaration is visible in
its whole file, above and below it, and order between files and imports
never matters (§14).

A nested `fn` or type in a body is visible in the rest of its block and the
blocks inside it, and not outside. A nested `fn` can call only `fn`s
declared before it, so nested `fn`s are never mutually recursive.

Inside an impl or an interface default, a bare call first uses the name's
lexical binding, a local function value or an ordinary file function
included; a function of the same owner supplies the callee only when no such
binding exists. The same order holds for a pipe stage.

## 7. Functions and closures

### Function declarations

Taught in [Functions & Lambdas](/functions-and-lambdas/#defining-a-function).

`fn name(parameters) -> Result`, the body on the lines below and `end`,
declares a function (§2 *Bodies*). A generic function writes its type
parameters after the name, and a `where` clause after the result:
`fn shown<T>(x: T) -> String where T: Display`. A long `where` clause goes on
the next line, which the `where` continues.

```nomi
fn add(x: Int, y: Int) -> Int
    x + y
end

fn main()
    dbg add(1, 2) // 3
end
```

- Every parameter has a type. A missing type is
  `parameter 'x' needs a type annotation`, in a `fn`, an impl's or an
  interface's function and a `host fn`; an impl's parameters are written
  out, not taken from its interface. A parameter has a name too, unless it
  is written as its type alone, `fn stats(Request)`, which takes a
  `Request` and binds nothing (*Parameter Destructuring*). Only a lambda's
  parameter types may be inferred.
- The result type is written unless it is `Unit`; a function with no `->`
  returns `Unit`.
- The body's last expression is the result (*Returning*).
- A function's or lambda's body is a block for `defer` (§9 *`defer`*).

A `fn`, and a `struct`, `enum`, `type`, `typealias` or `interface`, may be
declared inside a body. It is visible in the rest of its block and the
blocks inside it (§6). A nested `fn` closes over the names in scope as a
lambda does, and takes no `pub`. A nested type's name is a type in every
annotation of that block. An `impl` or a `derive` may not stand in a body:
``an `impl` block must appear at module top-level, not inside a body``, and
``a `derive` declaration must appear at module top-level, not inside a body``.
Nor may a subtype or a validated type (§4 *Validated types*). So a nested
type has no impl, and a use that needs one (`a + b`, `a < b`, `"${a}"`) is
an error whose help says to declare the type at module level, even when a
std type of its name has the impl. In the same way an interface declared
in a body has no impl, since no impl header can name it: a call of its
function, a bound of it or a value of it is an error whose help says to
declare the interface at module level, even when a top-level interface of
its name has an impl for the type. A nested type may share a top-level
type's name and is then its own type, but it may not take a name the
prelude binds (§14 *Prelude*), as a top-level declaration may not:
``type name 'String' is reserved by the language and cannot be redeclared
as a struct; pick a different name (e.g. `struct MyString`)``.

### Parameters

#### Default Arguments

Taught in [Functions & Lambdas](/functions-and-lambdas/#default-parameter-values).

Any parameter may have a default, `port: Int = 8080`. A call that leaves the
argument out evaluates the default, after the written arguments, in
parameter order, once per call. A default may read earlier parameters and
the declaring file's functions. A default is checked against its parameter's
type: `width: Int = "wide"` is
`default value for parameter 'width' is String, expected Int`. A `host fn`'s
default is Nomi code like a `fn`'s, evaluated before the call leaves Nomi.

```nomi
fn connect(host: String, port: Int = 8080, timeout: Int = 30) -> String
    "${host}:${port}/${timeout}"
end

fn main()
    dbg connect("x")             // "x:8080/30"
    dbg connect("x", 80)         // "x:80/30"
    dbg connect("x", timeout: 5) // "x:8080/5"
end
```

Positional arguments fill parameters in order, so leaving out a default
that is not last means naming the arguments after it. `middle(1, 2)` for
`fn middle(a: Int, b: Int = 10, c: Int)` is
``missing argument for parameter 'c'; positional arguments fill parameters in order and cannot skip the defaulted 'b', so pass this one by name (c: ...)``.
The one exception is a trailing callback (*Lambdas at Call Sites*).

A default is not part of the function's body, so a `?`, `return`, `break`,
`continue` or assertion written directly in it is an error at that token:
`` `?` cannot be used in the default of parameter 'x': a default has no function to return from ``.
A lambda's defaults follow the same rule.

Defaults fill a call of the function by name, and a call through a local
name that copies it (below). A function used as a value has its full
parameter list, with one exception: where its position
expects a function type with fewer parameters, and each parameter past those
has a default, the value is the function of its first parameters, and a call
of it fills the rest from their defaults. That holds at an argument, a
pipe's head (§8 *Pipes*), an annotated binding, a field, a list element, a
return and a `case` or `if` arm: where `apply` takes a `(String) ->
Option<Int>`, `parse |> apply("1")` passes `parse` as `apply(parse, "1")`
does. A local copy is not the function's name, so after `g = parse`,
`apply(g, "1")` is
`` argument 1: expected (String) -> Option<Int>, got (String, Bool) -> Option<Int> ``,
whose help suggests the capture `&g(&1)`.

```nomi
fn parse(s: String, strict: Bool = False) -> Option<Int>
    if strict then None else String.to_int(s)
end

fn main()
    f: (String) -> Option<Int> = parse
    dbg f("12") // Some(12)
    dbg Iter.map(["1", "x"], parse) |> Iter.to_list() // [Some(1), None]
end
```

A `fn` declaration and a lambda bound to a name are two ways to name a
function, and one rule covers both. A function's defaults, a lambda's own
and a partial capture's open defaults stay with the name and the local
names that copy it: after `g = parse`, `g("12")` fills `strict`, and so
does a copy of a copy, a pipe through it and a closure that captured it,
as `h()` does after `h = fn x = 1 -> x`. That holds for a module function,
an owner's (`p = Parser.parse`) and a nested `fn`. A copy has no parameter
names: `g(s: "12")` is `no parameter named 's'`. A value passed on, taken
out of a container, bound with a type annotation or reached other than by a
local name has none, and a call through it that leaves an
argument out is `expected 2 arguments, got 1`, whose help is
`a function type's parameters have no defaults; pass every argument`. A
`once` that holds a lambda or a partial
capture has none either: after `once ping = &connect(&1, port: &2)`,
`ping("a")` is `expected 2 arguments, got 1`.

#### Parameter types from defaults

A parameter with a default may leave out its type when the default alone
tells it, by the rule for fields (§4 *Field types from defaults*): `limit =
10` is an `Int` parameter, and `mode = WriteMode.Replace` a `WriteMode`.
This holds for a `fn`, a generic one included, a function in an `impl`
block or a type's namespace, an interface's default function, a `host fn`
and a lambda (§5 *Lambda parameter types*). A default that reads an
earlier parameter takes its type from it (`fn window(page: Page, offset =
page.size * 2)`).

```nomi
fn search(query: String, limit = 10, offset = 0) -> String
    "${query} ${limit} ${offset}"
end

fn main()
    dbg search("nomi")             // "nomi 10 0"
    dbg search("nomi", offset: 20) // "nomi 10 20"
end
```

A default whose type only an expected type tells is the field rule's
error, `` can't tell this parameter's type from `None` ``, with
``write `since: Option<_> = None`, with the type for each `_` ``. The
inferred type is the function's signature for every caller, in every file,
and hover, signature help and the reference show it. A public function's
parameter may not take a private type from its default
(`function parameter exposes private type Secret`).

#### Parameter Destructuring

Taught in [Functions & Lambdas](/functions-and-lambdas/#destructuring-parameters).

A parameter of a function or lambda may be an irrefutable pattern instead
of a name. The body sees the names it binds.

```nomi
struct Point
    x: Int
    y: Int
end

fn sum(Point{x:, y:}) -> Int
    x + y
end

fn add_pair((a, b): (Int, Int)) -> Int
    a + b
end

fn main()
    dbg sum(Point{x: 1, y: 2}) // 3
    dbg add_pair((3, 4))       // 7
end
```

A pattern whose head names a type gives the parameter that type, and needs
no annotation, though one is allowed: a distinct `Duration(x)`, a named tuple
`Pair(a, b)`, a named struct `Point{x:, y:}`, a single-variant enum's
`E.V(x)`. A pattern with no
type head needs one: a tuple, an anonymous struct pattern (`{x:, y:}: Point`),
a dot-leading variant (`.V(x): E`), and a generic distinct without type
arguments.

A head with nothing after it names the parameter's type and binds nothing:
`fn stats(Request) -> Response` takes a `Request`, as `_: Request` does,
and so does a lambda's `fn Request -> ...`. The head may name any type
written as one name: a struct, an enum of any number of variants (the
parameter matches every value, whatever its variant), a distinct type, an
alias, a builtin such as `String`, or a type parameter. A generic type
needs its arguments, so `fn f(Box)` is the error that `Box` expects 1 type
argument, got 0.

The type alone may be written in any other form a parameter's type takes,
and binds nothing the same way: a generic type, `fn f(Vector<Int>, i: Int)`;
one named through its file, `fn f(http.Request)`, `fn f(mod.Box<Int>)`; a
tuple type, `fn f((Int, String))`; a function type, `fn f((Int) -> Int)`;
an anonymous struct type, `fn f({x: Int})`. Where a pattern is spelled the
same, the pattern is read: `Shape.Circle` and `geo.Shape.Circle` are a
variant's pattern, `(a, b)` a tuple pattern, and an annotation after the
parentheses or braces, `(a, b): (Int, Int)`, makes them a pattern. A
lambda's parameter takes these forms too, but not a function type alone:
in `fn (Int) -> n` the `->` is the lambda's, which takes an `Int` and
returns `n`, so a lambda takes a function it ignores as `_: (Int) -> Int`.

A name that is no type is `unknown type "Label"`. A name that is also a
variant in scope is not read as a type: `fn f(None)` is
`parameter 'None' needs a name`, and a variant that a pattern may not write
bare, `fn f(One)`, is the bare-variant error; where a type has the name too,
`_: One` takes the type and `.One` matches the variant. A call cannot name
such an argument, and an impl's parameter matches an interface's written so
whatever its name.

```nomi
struct Request
    path: String
end

fn stats(Request) -> String
    "ok"
end

fn main()
    dbg stats(Request{path: "/"}) // "ok"
end
```

The pattern must match every value of the parameter's type. A refutable one
is ``refutable pattern in parameter; bind the parameter and use a `case` in the body``:
a literal, a variant of an enum with more than one, a map or bracket
pattern (even one that matches every vector), and any
pattern holding one. A single-variant enum's variant is irrefutable.
`Duration(x)` and `Some(x)` look alike; only the parameter's type tells the
distinct from the variant.

### Calls

Taught in [Functions & Lambdas](/functions-and-lambdas/#named-arguments).

A call is a callee followed by arguments in parentheses. Only a function
value can be called: `1()` is `` `Int` is not a function ``, and so is
calling a tuple, a list or a struct. How a callee is qualified is §8.

#### Named Arguments

An argument may name its parameter: `connect(host: "x", port: 80)`. A
named argument whose value is a binding of the parameter's name is punned,
as a struct's field is: `Response.json(body, status:)` is
`Response.json(body, status: status)`. Positional arguments come first. One positional argument may follow named
ones, and fills the first slot they left empty; two are
`positional argument after named argument`.

A call fills its parameters in this order:

1. Positional arguments fill slots from the first, left to right, except a
   trailing callback (*Lambdas at Call Sites*).
2. Named arguments fill their slots.
3. Defaults fill the slots still empty.
4. A slot without a default still empty is an error.

A named argument for a slot already filled, positionally or by an earlier
name, is `parameter 'x' already has a value`, at the later one.

A name needs a declaration to name. A declared `fn`, and a lambda or partial
capture bound to a name, have parameter names. A value whose type is only a
function type has none: a parameter or field of function type, a copy of a
function value, a call's result, a value taken out of a container. A named
argument through such a value is `no parameter named 'port'`, whose help is
`a function type's parameters have no names; pass the arguments by position`.
A variant's
payload, a distinct's wrapped value and a struct's record call are
positional: `Some(value: 1)` is `no parameter named 'value'`. An
interface-qualified call takes the interface function's own parameter names
(`Display.to_string(value: 1)`, `Struct.update(updates: {x: 2}, original: p)`).

#### Lambdas at Call Sites

Taught in [Functions & Lambdas](/functions-and-lambdas/#trailing-callbacks).

A lambda is passed inside the parentheses like any argument; there is no
trailing-block syntax. When the callee's last parameter is still empty, the
final positional argument goes to that last slot, even past defaulted
parameters between it and the earlier positional arguments. So a trailing
callback stays unnamed when a middle default is skipped.

```nomi
fn apply(x: Int, scale: Int = 1, f: (Int) -> Int) -> Int
    f(x * scale)
end

fn main()
    dbg apply(5, fn x -> x + 1)           // 6
    dbg apply(5, scale: 2, fn x -> x + 1) // 11
end
```

The parameter list decides the routing, not how the argument is written. A
lambda, a field accessor (`Iter.sort_by(users, .age)`) and a capture route
on sight. A bare name routes when the last parameter takes a function and
the slot it would otherwise fill does not; where both take functions, the
written order wins. A trailing positional after named arguments skips the
slots they claimed. A positional written before a named argument fills the
slot it sits in.

#### No Auto-Currying

A function is not curried: a call passes every argument, and a function of
some of them is a partial capture (`&add(1, &1)`, *Captures*).

#### No Overloaded Functions

A name in a scope has one definition. Nomi never picks among same-named
functions by argument type: use separate names (`Date.parse`, `Time.parse`),
or interfaces for polymorphism. Interface functions share a name across
types, but each call is qualified by a type or an interface (§8).

### Lambdas

Taught in [Functions & Lambdas](/functions-and-lambdas/#lambdas).

`fn parameters -> body` is a lambda. Its parameters are written as a
function's are, without parentheses: names, annotations, defaults and
irrefutable patterns, separated by commas. A lambda with none writes nothing
between `fn` and `->`. The first `->` that is not inside a parameter's
annotation ends the parameters, so `fn f: (Int) -> Int -> f(1)` takes `f`.
An annotation in parentheses is a function type when what follows its result
type ends a parameter (`,`, `->` or `=`); otherwise the `->` ends the
parameters, so `fn p: (Int, Int) -> p.first` takes a tuple `p`. A lambda that
takes one tuple apart writes the tuple pattern as its parameter,
`fn (name, score) -> ...`. There is no implicit parameter.

```nomi
fn main()
    double = fn x: Int -> x * 2
    dbg double(4) // 8
    dbg Iter.reduce([1, 2, 3], 0, fn total, x -> total + x)    // 6
    dbg Iter.map([("a", 1)], fn (_, n) -> n) |> Iter.to_list() // [1]
    dbg (fn -> 7)() // 7
end
```

A lambda takes the parameters of the function type its position expects
(§5 *Assignability*), so a lambda that ignores them still names each one
`_`. Where a `(Request) -> ToResponse` is expected, `fn -> page` is
``this lambda takes no parameters, but a (Request) -> ToResponse takes 1 parameter``,
whose help writes `fn _ -> ...`. The one lambda of no parameters that stands
for a parameter is a stateless `Iter.loop(fn -> ...)` (§13 *`loop`*).

`fn` starts a declaration when a lowercase name and `(` or `<` follow it;
anything else after `fn` starts a lambda. `|x| x + 1` is not a lambda
(`` unexpected `|` ``).

A body on the `->`'s line is one expression, and runs to the end of the
enclosing expression. It may also be a discard of one,
`fn i -> _ = Sender.send(out, i)`, which is the body below the `->` that
holds that one statement, and has type `Unit`
(§9 *Statements and the Unit rule*). No other statement stands on the
`->`'s line: a binding of a name, a destructure, `return` and `break` go in
a body below it. A `->` that ends its line takes the lines below as the
body, closed by `end`, and `fn -> end` is the empty body:

```nomi
fn main()
    shout =
        fn s: String ->
            loud = String.uppercase(s)
            "${loud}!"
        end

    dbg [1, 2]
    |> Iter.map(fn n ->
        doubled = n * 2
        doubled + 1
    end)
    |> Iter.to_list() // [3, 5]

    dbg shout("hi") // "HI!"
end
```

A block body's result type comes from its last expression and its `return`
values, which must agree; a bare `return` gives `Unit`. A lambda parameter's
default gives its type. A lambda parameter without an annotation or a
default takes its type from the expected function type (§5 *Lambda
parameter types*).

The body's type is the lambda's result, and an expected result that names
a type fixes what the body leaves open: against `(Int) -> Result<Int, MyError>`,
`fn x -> Ok(x)` is a `Result<Int, MyError>`. An expected result that is an
interface fixes nothing, so a type the body leaves open is `Infallible`, the
type with no values, since the body made no value of it. Passed as a
`(Request) -> ToResponse`, `fn _ -> Ok(page)` is a
`Result<Response, Infallible>` and `fn _ -> Err(failure)` a
`Result<Infallible, Response>`, and each implements the interface when
`Infallible` does. An `Err(e)` in the body names the error type, as it does
anywhere.

```nomi
fn show(f: (Int) -> Display) -> String
    "${f(1)}"
end

fn main()
    dbg show(fn x -> Ok(x + 1)) // "Ok(2)"
end
```

With no expected function type, the open type stays open, which is harmless
until something needs to know it: after `f = fn x: Int -> Ok(x)`,
`io.print(f(1))` is
``Result<Int, _> needs Display (required by `where T: Display`), and nothing names the type its `_` stands for``,
whose help writes the annotation, `f: (Int) -> Result<Int, String> = fn x -> Ok(x)`.

### Captures

Taught in [Functions & Lambdas](/functions-and-lambdas/#captures).

A capture is a function written in place: `&` and the function's body, in
which `&1`, `&2`, ... stand for its parameters. The `&` stands against its
body, which is one of:

- a call: `&Int.max(&1, 0)`, `&Some(&1 + 1)`, `&f(&2, &1)`;
- a parenthesized expression: `&(&1 > 2)`, `&(&1 * 2 + 1)`, `&(not &1)`;
- a literal or construction: `&Point{x: &1, y: 0}`, `&(&1, 0)`, `&[&1, 0]`,
  and a string literal of any form, typed literals among them:
  `&"id ${&1}"` is `fn x -> "id ${x}"`, and `&Sql"id = ${&1}"` is
  `fn x -> Sql"id = ${x}"`.

```nomi
fn main()
    dbg Iter.filter([1, 3], &(&1 > 2)) |> Iter.to_list()       // [3]
    dbg Iter.map(["ab"], &String.length(&1)) |> Iter.to_list() // [2]
    add: (Int, Int) -> Int = &(&1 + &2)
    dbg add(1, 2) // 3
    dbg Iter.map([7], &"id ${&1}") |> Iter.to_list() // ["id 7"]
end
```

The body is one postfix-level operand, so `&f(&1) + 1` adds 1 to a capture;
the function of the sum is `&(f(&1) + 1)`. Anything else after `&` is
``a capture is `&` and a call, a parenthesized expression or a literal, as in `&f(&1)` or `&(&1 > 2)` ``:
`&x`, `&f(&1).x` (write `&(f(&1).x)`), `&Int.max` (the function itself is
`Int.max`), `&'a'`, `&-1`. `&not f(&1)` is the error that asks for
parentheses, and `& (x)` with a space is
``a capture's `&` is written against its body, with no space: `&(&1 > 2)`, `&f(&1)`, `&Point{x: &1}` ``.

`&N` is parameter N of the capture around it. `&` and a decimal number from
1 with no leading zero is one token; `&0` is
``a capture's parameter is numbered from 1, with no leading zero: write `&1` for the first parameter, `&2` for the second``.
Captures do not nest: `&f(&1, &g(&1))` is
``captures do not nest; write the inner one as `fn x -> ...` ``. A lambda
is a function of its own, so a capture inside a lambda inside a capture is
not nesting.

An `&N` outside every capture is
`` `&1` is a capture's parameter: write the function as `&(...)` or `&f(...)` ``,
whose help writes the capture it most likely belongs to
(`Iter.map(xs, Int.max(&1, 0))` suggests `&Int.max(&1, 0)`). A capture with
no `&N` is ``a capture uses at least `&1` ``, whose help writes `fn -> f()`.
`_` is not a parameter: as an argument, `add(1, _)` is
`` `_` is a discard name, not a value ``, whose help writes `&add(1, &1)`;
in a pipe stage, `|> add(100, _)`, the help writes `|> then &add(100, &1)`.

#### Parameters and their types

The function type a capture's position expects fixes its parameter count
and types, as for an unannotated lambda. Each `&N` must name one of them:
`Iter.map(xs, &(&2 + 1))` is
`` `Iter.map` passes one argument; `&2` names a second ``, and against an
annotation, ``the function expected here takes one argument; `&3` names a third``.
A parameter no `&N` names is ignored, one `&N` may stand several times
(`&(&1 * &1)`), and in any order. With no function type expected,
`f = &(&1 > 2)` is ``cannot infer type for `&1` in `&(&1 > 2)` ``.
`Some(&(&1 + 1))` is an `Option` holding a function; `&Some(&1 + 1)` is a
function returning an `Option`.

A **partial capture** needs no expected type. It is a capture whose body is
a call taking each `&N` as one of its own arguments, positional or named,
numbered in the order written, each once (`&add(1, &1)`,
`&connect(&1, port: &2)`). It is a partial application: its parameters are
the callee's at those slots, with their types, names and defaults, and it is
held to the callee's argument count (`&add(&1, 1, 2)` is
`expected 2 arguments, got 3`). A generic callee is solved from the expected
function type when there is one. An expected function type with more
parameters gives the extra ones, which it ignores. A call whose `&N`s repeat,
come out of order or stand deeper than its own arguments is not a partial.

```nomi
fn connect(host: String, port: Int = 8080, timeout: Int = 30) -> String
    "${host}:${port}/${timeout}"
end

fn main()
    c = &connect(&1, port: &2)
    dbg c(port: 80, host: "x") // "x:80/30"
    dbg c("y")                 // "y:8080/30"
end
```

A partial of an operator's function (`&Int.divide(12, &1)`) takes each open
operand's type from the expected function type, which picks among the type's
impls. With nothing expected, an open left operand is the type that
qualifies the function (`f = &Int.divide(&1, 2)` takes an `Int`, and `f(9)`
is 4), and an open right operand is the right-hand type of the type's one
impl (`&Int.divide(12, &1)` takes a `NonZeroInt`); with several it is
``cannot infer type for `&1` in `&Date.add(day, &1)` ``, whose help names
the impls.

Any other capture's parameters have no names and no defaults, except a
struct literal whose `&N`s are its whole fields, each once, which takes the
fields' names: after `mk: (Int) -> Point = &Point{x: &1, y: 0}`, `mk(x: 3)`
works, while `big(lhs: 3)` after `big: (Int) -> Bool = &(&1 > 2)` is
`no parameter named 'lhs'`.

#### Evaluation

A capture is a function value: nothing in its body runs when it is made,
and a call evaluates the whole body, as the equivalent lambda does.
`&(&1 > limit())` calls `limit` on every call. A partial capture's written
arguments run in its call's order. A capture routes to a trailing callback as
a lambda does, and it carries no starting value: `Iter.reduce(xs, &(&1 + &2))`
is `expected 3 arguments, got 2`.
In `&(&1 > 2) and ready`, nothing expects a function of the capture; the
function of the whole condition is `&(&1 > 2 and ready)`.

### Closures

Taught in [Functions & Lambdas](/functions-and-lambdas/#lambdas).

A lambda, a nested `fn` and a task's body are closures. A closure copies the
value of each name it reads when it is made, and reads those copies from
then on. A later binding of the same name, a same-scope shadow included, is
a new binding that does not reach it.

```nomi
fn make_greeter(greeting: String) -> (String) -> String
    fn name -> "${greeting}, ${name}!"
end

fn main()
    x = 1
    f = fn -> x
    x = 2
    dbg f()                          // 1
    dbg x                            // 2
    dbg make_greeter("Hello")("Ada") // "Hello, Ada!"
end
```

A nested `fn` that calls itself refers to itself through its own closure.

### Function values

A named function, referred to without a call, is a function value: it can be
bound, passed and returned (`Iter.map(users, format_user)`).

A generic function used as a value is instantiated against the function type
its position expects, and its `where` bounds are checked at those types:
`Iter.each(names, io.print)` passes `io.print` at `(String) -> Unit`. A
parameter the position solves to a subtype, under a bound of an
interface that makes a value, is solved to the base, as a call's arguments
solve it (§4 *Chains and conversion*): `Iter.map(rows, Iter.sum)` with
`rows` a `List<List<NonNegativeInt>>` sums Ints. A
position that leaves a type parameter unsolved, as `f = ident` does, is
``cannot infer type parameter T of generic function 'ident' used as a value: nothing here gives it a function type; annotate one (`f: (Int) -> Int = ident`) or pass it where a function type is expected``.

#### Constructors as Function Values

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#enums).

A positional variant named without a call is its constructor:
`Shape.Circle` is a `(Float) -> Shape`, and `Some` a `(T) -> Option<T>`. So is
a distinct type that wraps a value: `Id` for `type Id Int` is an
`(Int) -> Id`. A value built through the function equals the one the call
builds.

```nomi
enum Shape
    Circle Float
    Segment (Int, Int)
end

fn main()
    dbg Iter.map([1.0], Shape.Circle) |> Iter.to_list()     // [Circle(1.0)]
    dbg Iter.map([(1, 2)], Shape.Segment) |> Iter.to_list() // [Segment(1, 2)]
    somes: List<Option<Int>> = Iter.map([1, 2], Some) |> Iter.to_list()
    dbg somes // [Some(1), Some(2)]
end
```

- A generic constructor is instantiated as a generic function value is.
  `Ok` and `Err` leave the other type parameter to the result, so
  `Iter.map(ns, Ok)` needs an annotated destination. With nothing to solve
  it from, `f = Some` is
  `cannot infer type parameter T of generic constructor 'Some' used as a value: ...`.
- A variant over a tuple takes the tuple: `Shape.Segment` is a
  `((Int, Int)) -> Shape`. Its flat call is a call form of the one tuple,
  and a partial capture leaves one element open
  (`&Shape.Segment(&1, 0)` is an `(Int) -> Shape`). A distinct over a tuple
  is the same: `Pair` is a `((Int, String)) -> Pair`, and `Pair(1, "x")`
  builds one from the elements.
- A bare variant is a value, not a function: `Shape.Point` is a `Shape`.
- A struct and a struct-shaped variant are not function values:
  `f = Shape.Rectangle` is
  `Shape.Rectangle is a struct-shaped variant and is not a function value; ...`.
  A lambda that builds one is the function.
- `.Variant` is not a function value, since a function type is not an enum:
  `f: (Float) -> Shape = .Circle` is an error whose help names
  `Shape.Circle`.
- An opaque type's constructor is private outside its file, as a value too.

#### Field Accessors

Taught in [Structs, Enums & Distinct Types](/structs-enums-distinct/#field-accessors).

`.name` is a function that reads field `name` from its argument. A chain
`.address.city` reads one field after another, and an ordinal reads a
tuple's item (`.first`, `.second.first`, §4 *Tuples*). A lowercase name
after the dot is a field; a capitalized one is a `.Variant`.

```nomi
struct User
    name: String
    age:  Int
end

fn main()
    users = [User{name: "Ada", age: 36}, User{name: "Alan", age: 41}]
    dbg users |> Iter.map(.name) |> Iter.to_list() // ["Ada", "Alan"]
    age: (User) -> Int = .age
    dbg Iter.map(users, age) |> Iter.to_list() // [36, 41]
end
```

An accessor stands only where a function type `(S) -> R` of one parameter is
expected, and `S` must be known at its position: an annotation, a declared
parameter type, or a generic parameter an earlier argument or the piped value
has pinned. `S` must be a struct, an anonymous struct, an enum or a tuple.
An enum's field is read as `s.w` reads it, so every variant must have it
(§4 *Enums (Algebraic Data Types)*). The accessor is `(S) -> F`, `F` the
last field's type, and `R` unifies with `F`. A generic struct is read at its instance, and a field holding a function is
returned, not called. As an argument, an accessor routes like a trailing
lambda.

The errors, each at the accessor:

- No function expected:
  `` `.name` reads a field only where a function is expected; write `x.name` to read it from a value ``.
  As a pipe stage:
  `` `.name` is not a pipe stage; read the field from the value instead, as in `x.name` ``.
- The parameter type is not known yet, as in `apply(.name, user)` where a
  later argument would give it:
  `` `.name` needs the record type it reads from; annotate the binding, e.g. `f: (User) -> String = .name` ``.
- No such field: `struct 'User' has no field 'nmae'`, with the help
  `did you mean 'name'?`. A field only some of an enum's variants have is
  the error a field read gives.
- Not a record:
  `` `.name` reads a field of a struct, an enum, a record or a tuple, and Int is none of those ``.

### Returning

A function's result is its body's last expression. `return value` leaves
the nearest function or lambda with `value`, and a bare `return` leaves a
`Unit` one. `return`, `break` and `continue` leave only the function they are
written in; there is no non-local exit and there are no exceptions (§13). A
`?` inside a lambda returns from the lambda (§11). A `return` inside a nested
function, a lambda or a `concurrent` block belongs to that inner boundary.
A tail `return value` is legal. A body whose last statement always
evaluates an operand that returns in every path needs no result after it:
`defer close(if c then return 1 else return 2)` may end a function that
returns an `Int`.

A bare `return` the body would reach its end without is an error: one that
is the last statement of a function, lambda or test body, reached through
the last statement of a block, a branch of a final `if` or an arm of a final
`case` (``this `return` does nothing: it is the last statement of `reset` ``),
and a final `if` or `case` whose every branch is empty or a bare `return`
(``this `if` does nothing: every branch returns and nothing follows it``). A
bare `return` after a `dbg` statement is not one: it discards that
statement's value.

A statement after one that always exits can never run, and is an error at
that statement whose help is "remove it". A statement always exits when it
is:

- a `return`, `break` or `continue` (`unreachable code after return`);
- an `unreachable` (``unreachable code after `unreachable` ``, §11);
- an `if` with an `else` whose every branch exits
  (``unreachable code: the `if` above returns in every branch``);
- a `case` whose every arm exits
  (``unreachable code: the `case` above returns in every arm``);
- a block that holds one (`unreachable code: the block above always returns`);
- an `Iter.loop` statement whose callback holds no `break` of its own
  (``unreachable code after a loop that never breaks``, with the help
  "remove it, or give the loop a `break` that ends it"; §13);
- any other statement that always evaluates an operand that is one of
  these: a call's argument, a binding's value, a `defer` call's argument
  (`unreachable code: the statement above always returns`). After
  `defer close(if c then return 1 else return 2)` nothing runs, and nothing
  runs after `x = Iter.loop(0, fn n -> n + 1)` (`unreachable code: the
  statement above always loops forever`). The right side of `and` or `or`,
  an arm and a lambda's body are evaluated only sometimes, and are not such
  operands. Nor is an `unreachable` that is an operand: code after
  `x: Int = unreachable "..."` stays legal.

A `todo` is `Unit` and does not exit, so code after a stub stays legal, and
no other call counts as an exit. When the branches of an `if` or `case` all
exit and one of them traps, the message says it `exits` in every branch; when
all of them trap, it says it `traps`.

### Tail-call optimization

A call in tail position runs in constant stack space: it replaces the
caller's frame instead of adding one. This is a guarantee, with no
annotation and no `recur` form, for every tail call to a Nomi function:
direct, mutual, through a function value and through interface dispatch. A
host function has no frame to replace.

A call is in tail position when its value becomes the enclosing function's
result with nothing done after it. Tail position reaches:

- the last expression of a function or lambda body, and the last statement
  of a block in tail position;
- both branches of an `if`, and every arm of a `case` (not a guard), in tail
  position;
- the last stage of a pipe in tail position;
- the operand of `return`;
- the right operand of `and` and `or`.

It does not reach:

- into a lambda, which has its own tail position: in
  `Iter.reduce(xs, 0, fn acc, x -> f(x))`, the call to `f` is the lambda's
  tail, not the outer function's;
- a default expression, which runs before the body;
- the operand of `?`, whose result is matched before it returns.

A nested `fn` is a function, with its own tail position.

A call in tail position while a `defer` registered in a block around it is
pending is an ordinary call, since the deferred call runs after it returns:

```nomi
fn leave(n: Int)
    dbg n
end

fn down(n: Int) -> Int
    defer leave(n)
    if n == 0 then 0 else down(n - 1)
end

fn main()
    dbg down(1) // 0
end
```

`leave(0)` runs before `leave(1)`, and each `down` keeps its frame until its
deferred call has run. A `defer` in a block that has already ended is not
pending. Recursion that registers a `defer` at each step keeps a frame per
step, and counts toward the limit of *Recursion depth*.

A trap inside tail-recursive code reports one frame, however deep the
iteration ran.

### Recursion depth

At most 100,000 calls may be in progress at once on one task. The call that
would start one more is a trap at its line:

```console
line 4: stack overflow: more than 100000 nested calls
```

It is a trap like any other (§11). Each spawned task counts from zero. A
tail call keeps no frame and does not count, so a tail-recursive loop that
never reaches its base case runs forever.

A function that cannot return without calling itself with the arguments it
was given is a compile error at its name: every path through its body
reaches a call by its own name, its owner (`Foo.again(foo)`) or, in an impl,
its interface (`Display.to_string(value)`), passing each parameter, or a
plain binding of it, in its own position or by its own name. A call outside
tail position always counts. A call in tail position counts only when
nothing before it on its path calls a function, since an effect there can
make the loop deliberate (a `fn serve(l: Listener)` whose body is
`handle(l); serve(l)`).

```nomi
fn grow(x: Int) -> Int
    y = grow(x) // error at `grow` above
    y + 1
end
```

The error is
`every path through grow calls grow(x) with the arguments it was given, so it never returns`.
A path that returns without the call, a call with an argument changed, a
call inside a lambda, mutual recursion and interface default bodies are not
checked.

### Exported Functions

Taught in [Building & Shipping](/building-and-shipping/#calling-nomi-from-a-page).

`export fn` declares a top-level function that a host calls by name: a web
page, or a WASI runtime. Its arguments and result cross as JSON, through
`FromJson` and `ToJson`. How a host calls one is in the [tooling
reference](/tooling/embedding/).

```nomi
import std::json.{FromJson, ToJson}

struct Game
    word:    String
    guesses: List<String>
end

derive ToJson for Game
derive FromJson for Game

export fn new_game(word: String) -> Game
    Game{word:, guesses: []}
end

export fn guess(game: Game, letter: String) -> Game
    {..game, guesses: [letter, ..]}
end
```

- **Top level only.** `export` is the only modifier and comes first.
  Before anything but `fn` it is
  `` `export` marks a function: `export fn name(...)` ``. Inside a body it
  is `` `export` applies to top-level functions only ``, and in an `impl`
  block
  ``a function in an `impl Type` block cannot be exported: `export fn` declares a top-level function``.
  A `host fn` has no body to export
  (``a `host fn` is supplied by the host and cannot be exported; an `export fn` has a body``).
- **Not `pub`.** `pub` makes a function visible to the program's other
  files; `export` makes it callable from outside the program.
  `pub export fn` is ``an exported function takes no `pub`: write `export fn` ``.
  An exported function is not a member of its file, so other files cannot
  call it: `other.twice(2)` for an `export fn twice` in other.nomi is
  `file 'other' has no member 'twice'`. Other files call the plain function
  an exported one wraps.
- **Concrete.** An exported function is not generic
  (``exported function `ident` cannot be generic: a host calls it with JSON values of one known type``),
  and each parameter and the result is one known type, not an interface
  value.
- **JSON.** Each parameter's type implements `FromJson` and the result's
  `ToJson`, type arguments included. A `Unit` result crosses as nothing. A
  violation names the parameter, the type and the interface:
  ``parameter `p` of exported function `sum` cannot cross as JSON: `Plain` does not implement `FromJson` ``,
  with help naming the derive.
- **One name.** No two files of a program export one name:
  ``exported function `guess` is already exported by other.nomi: a host calls exported functions by name, so each name is exported once``,
  at the second, with the first as a related location.
- **Effects.** An exported function may do anything a function may.

A program with exported functions needs no `fn main`. Exported functions are
checked and lowered whether or not `main` reaches them. Running such a
program with no `main` is an error that says to build it for a host
([tooling reference](/tooling/building/)).

Values cross as `ToJson` writes them and `FromJson` reads them:

| Nomi | JSON | JavaScript |
|---|---|---|
| `Int` | number, its exact digits at any size | `number`, or a `BigInt` past `Number.MAX_SAFE_INTEGER` |
| `Float` | number | `number` |
| `String`, `Bool` | string, boolean | `string`, `boolean` |
| `Decimal` | string of its exact digits, scale kept (`"12.50"`); a number is read too | `string` |
| `Uuid` | string, lowercase 8-4-4-4-12 hex | `string` |
| `Instant` | string, RFC 3339 in UTC (`"2023-11-14T22:13:20.25Z"`); any offset is read | `string` |
| `Date`, `Time`, `NaiveDateTime` | string, `YYYY-MM-DD`, `HH:MM:SS[.fff]`, `YYYY-MM-DDTHH:MM:SS[.fff]`; a year outside 0000 to 9999 as `+010000` | `string` |
| `List<T>`, `Vector<T>`, `Set<T>` | array | array |
| `Map<String, V>` | object | object |
| struct | object, as `derive ToJson` writes it | object |
| enum | a bare variant's name as a string; `{"Variant": payload}`, an object with that one key, for one with a payload | string or object |
| `Option<T>` | the value, or `null` for `None` | the value or `null` |
| `Result<T, E>` | `{"Ok": value}` or `{"Err": error}`, as an enum's variant with a payload | object |
| `Unit` (a result only) | nothing | `undefined` |

A `Float` parameter reads a whole number too, since JavaScript writes `2.0`
as `2`. An `Int` crosses exactly in both directions: the loaders read an
integer past `Number.MAX_SAFE_INTEGER` from its own digits as a `BigInt`, and
write a `BigInt` argument as its digits, so `9007199254740993` arrives as
`9007199254740993n` rather than rounded to `9007199254740992`.

## 8. Expressions

Expressions, patterns and types nest at most 256 levels deep. Blocks,
parentheses, list, vector, set, map, tuple and struct literals, lambdas, `if`
bodies, unary operators, interpolations and type arguments each add a level. A
chain of binary operators, `|>` stages or `else if` arms does not. Deeper
nesting is a parse error at the first token past the limit: `nesting deeper
than 256 levels; move the inner part into a binding or a function`.

### Evaluation order

Operands and arguments are evaluated once each, left to right as written. A
named argument is evaluated where it is written, not where its parameter
stands, so `f(b: g(), a: h())` runs `g` before `h`. A pipe's head is evaluated
before the stage's other arguments. `x in c` evaluates `x` before `c`. `and`
and `or` evaluate their right operand only when the left does not decide the
return (*Logical operators*).

```nomi
fn add(a: Int, b: Int) -> Int
    a + b
end

fn main()
    _ = add(b: dbg 1, a: dbg 2) // prints 1, then 2
    _ = (dbg 3) |> add(dbg 4)   // prints 3, then 4
end
```

### Operators and precedence

The operator set is fixed. A program cannot define an operator or change a
precedence. The arithmetic operators `+`, `-`, `*`, `/` and `%` and the
bitwise operators dispatch through std interfaces (*Operators through
interfaces*).

Precedence, from loosest to tightest:

| Level | Operators |
|---|---|
| 1 | `or` |
| 2 | `and` |
| 3 | prefix `not` |
| 4 | `==` `!=` `is` `in` |
| 5 | `<` `>` `<=` `>=` |
| 6 | `\|>` `as` |
| 7 | `..` `..=` |
| 8 | `+` `-` `\|\|\|` `^^^` |
| 9 | `*` `/` `%` `&&&` `<<<` `>>>` |
| 10 | prefix `-` `~~~` |
| 11 | calls, field access, postfix `?` and `!` |

- Binary operators of one level associate to the left.
- `&&&` and the shifts bind as `*` does, and `|||` and `^^^` as `+` does, so
  each binds tighter than a comparison: `x &&& mask == 0` is
  `(x &&& mask) == 0`, and `hi <<< 8 ||| lo` is `(hi <<< 8) ||| lo`.
- `is` and `in` bind as `==` does but do not associate. Neither chains with
  `==`, `!=`, an ordering operator, `is` or `in` (*Pattern test: `is`*,
  *Membership: `Contains`*).
- Prefix `not` takes everything to its right that binds at least as tightly as
  `==` (*Logical operators*).
- `as` takes a type, not an expression, on its right, and ascribes
  everything to its left at its level, so `xs |> Iter.to_list() as
  List<Int>` ascribes the pipeline and `a == b as T` is `a == (b as T)`. It
  does not chain (§5 *Type ascription*).

Other operator tokens: `?` and `!` (postfix, §11 *Propagating with `?`*, §11
*Unwrapping with `!`*), `=` (binding, §6), `.` (field access and
qualification, *Field access*), and `&` (capture, §7 *Captures*).

### Arithmetic operators

Taught in [Scalars and Strings](/scalars-and-strings/#numbers-booleans-operators).

`+`, `-`, `*`, `/` and `%`, and prefix `-`.

- `Int`, `Float` and `Decimal` have `+`, `-`, `*`, `/`, `%` and prefix `-`
  over two operands of one type. `%` is the remainder of `/`'s truncating
  division and takes the sign of the left operand (*Integer division and
  remainder*).

- The numeric types never mix. `1 + 2.0`, `1 + 1.50d`, `1 < 2.0` and `1 ==
  1.0` are compile errors (`` `+` needs two values of one type, and these
  are Int and Float ``, `equality type mismatch: Int vs Float`, `comparison
  type mismatch: Int vs Decimal`).
  Convert with `Int.to_float`, `Float.to_int`, `Int.to_decimal`,
  `Decimal.to_float` or `Float.to_decimal`.
- An operand of a subtype is its base's value, and the result is the
  base's, except that `+`, `*` and prefix `-` on the std numeric subtypes
  keep one where it provably holds: with `count` a `NonNegativeInt`,
  `count + 1` is a `PositiveInt` and `count - 1` an `Int` (§4 *Arithmetic
  on numeric subtypes*).
- `+` on two `String` or two `Bytes` values concatenates.
- On collections, `+` and `-` combine two collections of one type. `List<T> +
  List<T>` and `Vector<T> + Vector<T>` concatenate. `Set<T> + Set<T>` is the
  union and `Set<T> - Set<T>` the difference. `Map<K, V> + Map<K, V>` is
  `Map.merge`: on a shared key the right-hand value wins. `Map` has no `-`.
  There is no collection-plus-element form: write `s + Set[x]`, `list + [x]` or
  `m + {k => v}`.
- A collection literal operand beside an operand of the same collection is
  built as that operand's type, on either side, as a literal is under an
  annotation (§5 *Covariant conversion*): with `routes: List<(Route,
  Handler)>`, ``[(Route`GET /`, home)] + routes`` is a `List<(Route,
  Handler)>` though `home` is a `(Request) -> Response`. The literal beside
  a subtype of a collection is built as its base: `names + ["Dorothy"]`
  with `names` a `NonEmptyVector<String>` is a `Vector<String>`. Two
  operands that each have a type of their own must have one type, or types
  that differ where a subtype meets an ancestor, which join (§4 *Chains
  and conversion*): with ``mine = [(Route`GET /`, home)]``, `mine + routes`
  is the error that `+` needs two values of one type, and `ints +
  positives` with a `List<Int>` and a `List<PositiveInt>` is a `List<Int>`.

#### Integer arithmetic is exact

An `Int` holds a whole number of any size. `+`, `-`, `*`, prefix `-`, `/` and
`%` give exact results and never overflow. A value that fits in 64 bits costs what
a machine integer costs; a larger one grows as it needs to.

```nomi
fn main()
    dbg 9_223_372_036_854_775_807 + 1                  // 9223372036854775808
    dbg -(-9_223_372_036_854_775_808)                  // 9223372036854775808
    dbg Int.wrapping_add(9_223_372_036_854_775_807, 1) // -9223372036854775808
end
```

`Int.wrapping_add`, `Int.wrapping_sub` and `Int.wrapping_mul` return the low
64 bits of the exact result as a signed 64-bit `Int`, for arithmetic defined
over 64 bits (a hash, a checksum). Their
32-bit siblings (`Int.wrapping_add_32` and the rest), and the rotations, zero
counts and byte swaps at 32 and 64 bits (`Int.rotate_left_32`,
`Int.leading_zeros_64`, `Int.swap_bytes_32`), read the low bits of their
operands in two's complement, so any `Int` is an operand. A 32-bit one
answers the unsigned value and a 64-bit one the signed value, so each answer
fits 64 bits. `Int.wrap_signed(n, width)` reduces `n` into the signed range
of `width` bits, and `n &&& 0xFFFF_FFFF` keeps its low 32 bits unsigned.

Where a value must fit a machine integer, the boundary says so:

- An index, a count or a size past 64 bits reads as past any collection:
  `Iter.get(xs, Int.pow(10, 30))` is `None`, `Iter.take(xs, n)` with `n`
  past 64 bits takes all of `xs`, and `Vector.get(v, Int.pow(10, 30))!` traps as any index
  out of range does.
- A library type with a 64-bit range says so in its reference: a
  database driver refuses an `Int` its engine cannot store. A
  [`Duration`](/reference/duration/) is an `Int` of nanoseconds with no
  range, and an [`Instant`](/reference/instant/) an `Int` of nanoseconds
  from the epoch with none, so `Instant + Duration` never faults; a wait
  past the 64 bits the clock counts (about 292 years) never ends, and a
  deadline past 2262 never comes. The calendar types have no range either:
  a [`Date`](/reference/calendar/)'s year is an `Int` of any size, an
  `OffsetDateTime` or `DateTime` holds any `Instant`, and no construction,
  conversion or arithmetic on them faults for how far it goes. A year
  outside 0000 to 9999 is written in ISO 8601's expanded form, a sign and
  at least six digits (`+010000-01-01`, `-000001-12-31`), and every `parse`
  reads it back.
  [std::json](/reference/json/) reads and writes an `Int` of any size, and an
  `export fn` result past ±2⁵³ fails the call (§7 *Exported Functions*).

#### Integer division and remainder

`/` truncates toward zero. `%` is the remainder of that division and takes the
sign of the dividend, so `(a / b) * b + (a % b) == a`: `-7 / 3 == -2` and `-7
% 3 == -1`.

- The divisor is a `NonZeroInt`: std's impls are `Divide<NonZeroInt, Int>`
  and `Remainder<NonZeroInt, Int>`, and `Int.divide` and `Int.remainder`
  take one. A divisor passes when it is a non-zero literal, when its type is
  `NonZeroInt` or below it, or when its range proves it off zero (§4 *Value
  ranges*): a binding checked first (§4 *Narrowing*), `n + 1` with `n` a
  `NonNegativeInt`, `Duration.minutes(15)`. Any other divisor is an error at
  the divisor that says how to make it one: ``` `/` divides by a NonZeroInt; `elapsed` is an Int ``` with the help
  ``check it first (`if elapsed == 0 then ...`) or take a NonZeroInt``.
  A divisor that is always zero is
  `division by zero: the divisor is the literal 0`, or with `zero = 0`,
  `` division by zero: `zero` is 0 here ``.

```nomi
fn per_hour(count: Int, hours: Int) -> Int
    if hours == 0 then return 0
    count / hours // hours is a NonZeroInt here
end

fn average(total: Int, n: PositiveInt) -> Int
    total / n
end

fn main()
    dbg per_hour(120, 4) // 30
    dbg average(10, 4)   // 2
    dbg 7 % 3            // 1
    dbg Int.divide(9, NonZeroInt.try_from(3 - 1)!) // 4
end
```

- So a checked program never divides an `Int` by zero. The VM still checks
  each division and stops with `line N: division by zero` (§11 *Traps,
  `Option` and `Result`*), a backstop no checked program reaches.
- A generic function that divides by its own type, `where T: Divide<T, T>`,
  does not take an `Int`, whose `/` takes a `NonZeroInt`; one bounded by the
  divisor's type does: `fn rem<T, D>(a: T, b: D) -> T where T:
  Remainder<D, T>` takes `rem(10, NonZeroInt(4))`.
- The named functions that divide take a non-zero divisor too:
  `Int.modulo(x, y)` and `Int.floor_div(x, y)` a `NonZeroInt`, and
  `Decimal.divide` and `Decimal.divide_exact` a `NonZeroDecimal`, so
  `Int.modulo(x, 0)` is ``0 is not a NonZeroInt: `fn n -> n != 0` is false``.
- An impl of `Divide` or `Remainder` for a type of one's own takes a divisor
  that cannot be zero in the same way: a `NonZeroInt`
  (`impl Divide<NonZeroInt, Score> for Score`, so `score / 4`), or a subtype
  of its own type whose rule rules zero out (`impl
  Divide<NonZeroRational, Rational> for Rational`). Its body divides an
  `Int` under the same rule as any other code.
- `Float`'s `%` is IEEE 754's fmod: `a - b * trunc(a / b)` computed exactly,
  with the sign of `a` (`-7.5 % 2.0 == -1.5`). It never traps: a zero divisor
  or an infinite dividend gives NaN, and an infinite divisor gives `a`.

Floored division and modulo, powers, roots and digits are functions in
[std::int](/reference/int/).

#### Decimal arithmetic

Taught in [Scalars and Strings](/scalars-and-strings/#exact-arithmetic-with-decimal).

`Decimal` (§4 *Primitive Types*) has `+`, `-`, `*`, `/`, `%`, the comparisons
and equality. `+`, `-` and `*` never round. The scale grows as needed: `1.50d +
1.5d == 3.00d` and `1.50d * 1.5d == 2.250d`.

`/` rounds. A quotient is kept to 34 significant digits, rounded half-even,
which is IEEE 754 decimal128's precision (Java's `MathContext.DECIMAL128`):
enough for any amount of money and any rate applied to it.

- The digits are counted from the quotient's first non-zero digit, so
  `1d / 3000d` keeps 34 threes after `0.000`.
- A quotient is never rounded left of the decimal point: one whose whole part
  has more than 34 digits keeps all of them, at scale 0.
- The result has no trailing zeros after the point. A quotient that ends
  within its 34 digits is therefore exact, at the smallest scale that holds
  it, whatever the operands' scales: `10.00d / 4d` is `2.5d`, `1d / 4d` is
  `0.25d` and `6.00d / 3d` is `2d`. `Decimal.scale` reads that scale (`1`,
  `2` and `0`) and `Decimal.to_string` prints it (`"2.5"`, `"0.25"`, `"2"`).
- A quotient that does not end has all 34 digits: `10d / 3d` is
  `3.333333333333333333333333333333333d` and `2d / -3d` is
  `-0.6666666666666666666666666666666667d`.
- The divisor is a `NonZeroDecimal`, as `Int`'s is a `NonZeroInt`: `d / e`
  with `e` a `Decimal` is ``` `/` divides by a NonZeroDecimal; `e` is a Decimal ```
  until a check (`if e == 0d then ...`) or a type says it is not zero.
- `Divide.divide(a, b)` and `Decimal.divide(a, b)` are the same operation,
  and `Decimal` meets a `Divide<NonZeroDecimal, Decimal>` bound.
- `/` takes two `Decimal`s, as `*` does: `1d / 3` is `` `/` needs two
  values of one type, and these are Decimal and Int ``, whose help writes
  `3d`.

[std::decimal](/reference/decimal/)'s `Decimal.divide(a, b, scale, mode)`
rounds to a scale and `RoundingMode` you name, for an answer whose digits are
fixed (cents, a rate to four places). `Decimal.divide_exact` returns `None`
for a quotient that does not end, and `Decimal.round_exact` and
`Decimal.sqrt_exact` return `None` where a digit would be lost; no
`RoundingMode` asserts exactness.

`%` is exact: `a - b * q`, where `q` is the quotient truncated toward zero.
It takes the sign of `a`, never rounds, and has the larger of the two scales:
`10.5d % 3d == 1.5d`, `7.25d % 0.5d` is `0.25d`, `-7.5d % 2d == -1.5d` and
`10d % 2.50d` is `0.00d`. Its divisor is a `NonZeroDecimal`, as `/`'s is.

Equality and comparison are by value and ignore scale: `1.50d == 1.5d` is
`True`. Scale is kept for display: `Decimal.scale(1.50d) == 2` and
`Decimal.to_string(1.50d) == "1.50"`.

### Operators through interfaces

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#operators-that-dispatch).

`+`, `-`, `*`, `/` and `%` are the prelude interfaces `Add<Rhs, Out>`,
`Subtract<Rhs, Out>`, `Multiply<Rhs, Out>`, `Divide<Rhs, Out>` and
`Remainder<Rhs, Out>`. The left
operand is the implementing type. The type arguments name the right operand's
type and the result's:

```nomi
interface Add<Rhs, Out>
    fn add(lhs: self, rhs: Rhs) -> Out
end
```
```nomi
type Day Int
type Days Int

impl Add<Days, Day> for Day
    fn add(lhs: Day, rhs: Days) -> Day
        Day(d) = lhs
        Days(n) = rhs
        Day(d + n)
    end
end

fn main()
    dbg Day(3) + Days(4) // Day(7)
end
```

- One receiver may implement an operator several times, told apart by the
  right operand's type (§12 *Several instantiations for one type*): `Instant -
  Duration` is an `Instant` and `Instant - Instant` a `Duration`.
- std implements arithmetic for `Int`, `Float` and `Decimal`, and `Add` for
  `String` and `Bytes`. `Int`'s and `Decimal`'s `Divide` and `Remainder`
  take a `NonZeroInt` and a `NonZeroDecimal` (*Integer division and
  remainder*). `Duration` has `Divide<NonZeroInt, Duration>` and
  `Remainder<NonZeroDuration, Duration>`: `elapsed % Duration.minutes(15)`
  is how far into the current quarter hour `elapsed` is.
- Generic code that uses an operator names the bound: `where T: Add<Step,
  Out>`.
- An operator impl may be generic over the receiver's type parameters. Its
  interface arguments are read at the left operand's type, so `impl Add<Set<T>,
  Set<T>> for Set<T>` makes `Set[1, 2] + Set[2, 3]` a `Set<Int>`. A right operand
  no impl takes is an error: `Set[1] + 2` is `binary + right operand mismatch: Add
  expects Set<Int>, got Int`.
- An operand type with no impl is an error at the operator that names the
  interface: ``no impl of `BitwiseAnd` for `Float` (required via bitwise and
  operator `&&&`)``, and for a `type Score Int` with no `Remainder`, ``no impl
  of `Remainder` for `Score` (required via remainder operator `%`)``.

**Summable.** A type whose `+` takes two of its values and returns one may
implement the prelude interface `Summable`. `empty_sum()` is the sum of no
values, the value `+` leaves alone: `T.add(x, T.empty_sum()) == x`.

```nomi
interface Summable
    fn empty_sum() -> self
end
```

Interfaces do not extend one another (§12), so code that sums names both
bounds, `where T: Add<T, T> and Summable`, as `Iter.sum` does.
[std::summable](/reference/summable/) lists the std types that implement it.

**Productable.** The same for `*`: `empty_product()` is the product of no
values. `Iter.product` is bounded by `where T: Multiply<T, T> and
Productable`, and [std::productable](/reference/productable/) lists the std
types that implement it.

```nomi
interface Productable
    fn empty_product() -> self
end
```

**Averageable.** `Iter.mean` and `Iter.mean_by` are bounded by `where T:
Averageable<R>`: `mean(values)` is the mean of `values` as an `R`, `None`
when there are none, and the impl's `R` is the result's type, so a call
solves it from the element type. Int's mean is a Float, the exact sum over
the count rounded once; Float's sums with compensation; Decimal's is a
Decimal and Duration's a Duration. A subtype has its base's impl, so the
mean of `NonNegativeInt`s is a Float.
[std::averageable](/reference/averageable/) gives each std impl's rounding.

```nomi
interface Averageable<R>
    fn mean(values: Iter<self>) -> Option<R>
end
```

### Bitwise operators

`&&&` (and), `|||` (or), `^^^` (exclusive or), `~~~` (prefix not), `<<<`
(shift left) and `>>>` (arithmetic shift right).

```nomi
fn main()
    byte = 0b1010_0000
    dbg byte >>> 7 &&& 1 // (byte >>> 7) &&& 1 == 1
    dbg 1 <<< 64         // 18446744073709551616
    dbg -1 &&& Int.pow(2, 80) == Int.pow(2, 80) // True
end
```

- On `Int` the operators read the value as two's complement with as many bits
  as it needs, a negative value having endless leading ones. So `&&&`, `|||`,
  `^^^` and `~~~` give exact results at any size, and `~~~n == -n - 1`.
- `a <<< n` is `a` times 2 to the `n`, exact: no bit is shifted out.
- `a >>> n` divides by 2 to the `n`, rounding toward negative infinity. A count
  past `a`'s bits returns `0` for a non-negative `a` and `-1` for a negative
  one.
- The count is a `NonNegativeInt` (`impl ShiftLeft<NonNegativeInt, Int> for
  Int`), held to it as a divisor is to `NonZeroInt` (§8 *Integer division
  and remainder*): with `n` an `Int`, `1 <<< n` is ``` `<<<` shifts by a NonNegativeInt; `n` is an Int ``` with the help
  ``check it first (`if n < 0 then ...`) or take a NonNegativeInt``. A left
  shift by a count past 2^34 traps, since its result would not fit in
  memory: `shift count 17179869185 is too large: the result would have more than 17179869184 bits`.
- `Byte` has `&&&`, `|||`, `^^^` and `~~~`, whose results stay within 8 bits
  (`~~~b` is `255 - b`). It has no shift operator, since a left shift could
  carry bits past 8: shift the `Int` (`Byte.to_int(b) <<< 1`), or call
  `Byte.wrapping_shift_left` and `Byte.wrapping_shift_right`, which drop the
  bits moved out. `b <<< 1` is ``no impl of
  `ShiftLeft` for `Byte` (required via shift left operator `<<<`)``.
- The C-family spellings are parse errors that name the Nomi one: `a << n` is
  ``Nomi spells shift left `<<<`: write `a <<< n`, not `a << n` ``, and the
  same for `>>`. A `>>` or `>>>` that closes nested type arguments
  (`List<List<List<Int>>>`) is still closing brackets.

The operators dispatch through prelude interfaces as `+` does through `Add`:
`BitwiseAnd<Rhs, Out>`, `BitwiseOr<Rhs, Out>`, `BitwiseXor<Rhs, Out>`,
`ShiftLeft<Rhs, Out>` and `ShiftRight<Rhs, Out>`, each with one function named
for its operation (`fn bitwise_and(lhs: self, rhs: Rhs) -> Out`), and
`BitwiseNot<Out>` with `fn bitwise_not(value: self) -> Out`. A distinct type
opts in with an impl:

```nomi
type Flags Int

impl BitwiseOr<Flags, Flags> for Flags
    fn bitwise_or(lhs: Flags, rhs: Flags) -> Flags
        Flags(a) = lhs
        Flags(b) = rhs
        Flags(a ||| b)
    end
end
```

`Int.bitwise_and(a, b)` and the other five are the `Int` impls' functions, for
passing as values (`Iter.reduce(flags, 0, Int.bitwise_or)`).

### Comparison and equality

`==`, `!=`, `<`, `>`, `<=` and `>=`.

- `==` and `!=` take two operands of one type. Equality is structural unless
  the type has a hand-written `impl Equatable` (§5 *Equality and Ordering
  Semantics*). Two operands of different types are a compile error, not
  `False`: `"a" == 1` is `equality type mismatch: String vs Int`.
- `<`, `>`, `<=` and `>=` need the operand type to implement `Comparable`,
  through `derive Comparable` or an impl. Without one they are an error:
  ``no impl of `Comparable` for `P` (required via ordering operator `<`)``.
  An operand held as an interface value has no order, `Comparable` itself
  included (§12 *Interface values*).
- The operands are a determining position for each other (§5): `k == .Deposit`
  resolves `.Deposit` against `k`'s enum. A subtype in one operand's type
  is no rule for the other, which is compared as the base
  (§4 *Literals, `try_from` and `unchecked`*): `names == []` with `names` a
  `NonEmptyList<String>` is `False`.

### Logical operators

`and`, `or` and prefix `not`. Each takes and returns `Bool`.

- `not x` is `True` when `x` is `False`. An operand of another type is an
  error: `` `not` takes a Bool, got Int ``.
- `not`'s operand is everything to its right that binds at least as tightly as
  `==`. `not a == b` is `not (a == b)`, `not a < b and c` is `(not (a < b)) and
  c`, and `not a and b` is `(not a) and b`. A pipeline binds more tightly than
  a comparison, so `not xs |> Iter.is_empty()` is `not (xs |> Iter.is_empty())`.
  `not ready()?` is `not (ready()?)`. `not not x` is `x`.
- A `not` stands alone, after `and` or `or`, or after another `not`. As the
  operand of a tighter operator it is an error: `a == not b` is `` `not` binds
  looser than comparisons and arithmetic, so it cannot be their operand ``,
  whose help shows `a == (not b)`. The same holds for `-not x` and a capture
  body `&not f(&1)` (write `&(not f(&1))`).
- `a and b` is `if a then b else False`, and `a or b` is `if a then True
  else b`: the right operand runs only when the left does not decide the
  result. The right operand may leave instead of returning (a block ending in
  `return`, `break` or `continue`, an `if` or `case` every arm of which
  leaves, a failing `?`, a trap). On that path the expression has no value;
  on the other its value is the left operand's.

```nomi
fn ready(flag: Bool) -> Result<Bool, String>
    ok = flag
        or begin
            return Err("not ready")
        end

    Ok(ok)
end

fn main()
    dbg ready(False) // Err("not ready")
    dbg ready(True)  // Ok(True)
end
```

- A pipe stage may start with `not`, which negates the stage's result: `name
  |> not Iter.is_empty()` is `not Iter.is_empty(name)`, and the stages after it
  take the negated value. The stage is a call, possibly unwrapped with `!`
  (`flags |> not Iter.first()!`), or another `not` stage. A `?` there is an
  error, since it would propagate the call's failure rather than the
  negation's: `` a `not` stage cannot end in `?`: the `?` would propagate the
  call's failure, not the negation's ``.
- The C-family forms are parse errors that name the Nomi one: `!x` is ``Nomi
  spells logical not `not`: write `not x`, not `!x` ``, `a && b` is ``Nomi
  spells logical and `and`: write `a and b`, not `a && b` ``, and `a || b` is
  ``Nomi spells logical or `or`: write `a or b`, not `a || b` ``.

### Pattern test: `is`

Taught in [Pattern Matching](/pattern-matching/#testing-a-match-with-is).

`value is Pattern` is a `Bool`: whether the value matches the pattern. It
returns what a `case` with a `True` arm for the pattern and a `_ -> False` arm
returns.

```nomi
enum Block
    Item String
    Text String
end

fn main()
    blocks = [Block.Item("milk"), Block.Text("note"), Block.Item("eggs")]
    dbg blocks |> Iter.filter(&(&1 is .Item(_))) |> Iter.count() // 2
    dbg 3 is (1 or 2)                      // False
    dbg not [1, 2] |> List.first() is None // True
end
```

- **Precedence.** `is` binds as `==` does: looser than `|>`, ranges and
  arithmetic, so `xs |> List.first() is Some(_)` tests the pipeline's result,
  and tighter than `not`, `and` and `or`, so `not block is .Item(_)` is `not
  (block is .Item(_))`.
- **It does not chain.** `a == b is True` is `` `is` does not chain with a
  comparison ``, whose help shows `(a == b) is True`. An operator that binds
  more tightly than `is`, written after the pattern, is an error too, since a
  pattern is not an expression: `x is 1 + 1` is `` an `is` test's pattern ends
  before `+` ``, and `x is P |> f()` is `` an `is` test's pattern ends before
  `|>` ``.
- **The pattern is a `case` arm's.** Every pattern form works, nested to any
  depth (§10). It is checked against the value's type with an arm's errors: a
  `.Variant` resolves against the value's type, and `n is "x"` over an `Int`
  is `pattern "x" is a String, but the value is an Int`. A map pattern's keys
  are evaluated where the test is.
- **It binds nothing.** A name in the pattern is the error `` an `is` test
  binds nothing, so its pattern cannot name `n` ``, whose help names the
  binding form, `if Some(n) = m then ...`. When the whole pattern is the name,
  the help is ``to compare the value with `n`, write `m == n` ``. An `as` name
  is the same error. A discard name (`_rest`) is allowed.
- **An `or` pattern goes in parentheses:** `kind is (.Circle or .Square)`. A
  bare `or` after the pattern is the Boolean `or`, so `x is .A or y > 0` is `(x
  is .A) or (y > 0)`. When the right side can only be a pattern (`_`, a
  `.Variant`, `.B(...)` or `.B{...}`), it is the parse error `` this `or`
  joins two Bool values, and a pattern cannot be its right operand: `is` takes
  one pattern ``. When the right side is read as a Bool and is not one, the
  `'or' operands must be Bool` error's help names the parenthesized form.
- **A pattern that always matches is an error,** since the test could only
  return `True`: `` `_` matches every value of type Kind, so this test is
  always True ``. There is no exhaustiveness to check.
- The value is evaluated once, before the pattern is tested.
- There is no `is not`: `x is not 1` is `` `is not` is not an operator ``,
  whose help says to write `not x is P`.

### Membership: `Contains`

`x in c` is `True` when the collection `c` holds `x`. It is
`Contains.contains(c, x)` written as an operator, and calls the same impl.

```nomi
interface Contains<T>
    fn contains(c: self, x: T) -> Bool
end
```

The std collections implement it: a `Set` holds its members, a `Map` its
keys, a `Range` the values between its ends, a `List`, `Vector` or `Bytes`
its elements, and a `String` a substring, a `Regex` match or a `Codepoint`.
[std::contains](/reference/contains/) says how each one looks and what it
costs.

```nomi
fn main()
    dbg 2 in [1, 2, 3]    // True
    dbg "a" in {"a" => 1} // True: a map holds its keys
    dbg 10 in 1..10       // False: the end is excluded
    dbg '-' in "a-b"      // True
    dbg not 7 in [1, 2]   // True
end
```

- **Precedence.** `in` binds as `==` does: looser than `|>`, ranges,
  arithmetic and the bitwise operators, so `x in 1..10` is `x in (1..10)` and
  `id in ids |> Iter.to_set()` looks in the set, and tighter than `not`, `and`
  and `or`, so `not id in seen` is `not (id in seen)`.
- **It does not chain.** `a == b in flags`, `x in xs == b`, `x in a < b`, `x in
  xs is True` and `x in xs in ys` are parse errors that ask for parentheses
  (`` `in` does not chain with `==` ``).
- There is no `not in`: `x not in xs` is `` `not in` is not an operator ``,
  whose help says to write `not x in xs`.
- Any type with a `Contains` impl supports `in`, and so does a type parameter
  bounded by `Contains<T>`. `String` implements `Contains` at three type
  arguments, and a call takes the one its argument's type chooses (§12
  *Several instantiations for one type*).
- A collection with no impl is an error that names the operand: `` the
  collection `in` looks in: Bag does not implement Contains ``. A value the
  impl does not take is `` the value `in` looks for: expected Int, got String
  `` for `"a" in [1, 2]`, and `1 in {"a" => 1}` is `` the value `in` looks
  for: expected String, got Int ``.
- `List` and `Vector` implement `Contains` where their element is
  `Equatable`, which every type with equality is (§5 *Equality bounds*):
  `p in [p]` and `p in Vector[p]` take a struct `P` with no impl of its own, as
  `p in Set[p]` and `p in {p => 1}` do. An element type with no equality is
  one error, the element's, at the collection: `o in [o]` for a struct `Op`
  holding a function is ``no impl of `Equatable` for `Op` (required via
  membership test `in`)``. A program's impl with a bound on its element is
  reported the same way.
- Elements compare as `==` does, so Float equality is reflexive here (§5
  *Equality*): a `NaN` element is found by `NaN`.

```nomi
fn all_present<C, T>(c: C, wanted: List<T>) -> Bool where C: Contains<T>
    Iter.all_match(wanted, &(&1 in c))
end

fn main()
    dbg all_present("a word", ["a", "word"]) // True: String's impl at String
    dbg all_present("a-b", ['-'])            // True: its impl at Codepoint
end
```

### Field access

Taught in [Structs, Enums and Distinct Types](/structs-enums-distinct/#structs).

`value.field` reads a struct's or an anonymous struct's field, and
`value.first`, `value.second`, ... a tuple's item (§4 *Tuples*). Access
chains: `user.address.city`.

- A value of interface type or of type-parameter type has no fields (§14
  *Member access is fail-loud*).
- Where a function is expected, `.name` with nothing before the dot is a
  function that reads the field (`users |> Iter.map(.name)`, §7 *Field
  Accessors*).
- `.` also qualifies names (`users.new`, `Iter.map`). A qualifier is resolved
  at compile time; field access is an operation on a value.
- There is no per-field visibility. A field is reachable wherever its struct
  is visible.

### Calls and qualification

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#qualified-call-shapes).

Nomi has no methods. Every call of a declared function is a qualified call
`Q.f(args)` or a bare call of a name in scope, never `x.f()`. The qualifier
names a file, a type, an interface or a bounded type parameter:

- A file-level function is qualified by its file: `io.print(x)`,
  `users.create(name, age)`.
- An inherent function (in an `impl Type` block) is qualified by its
  type: `String.trim(s)`, `Duration.seconds(1)`.
- An interface impl's function is qualified by the implementing type
  (`User.to_string(u)`), by the interface (`Display.to_string(u)`) or by a type
  parameter bounded by the interface (`T.to_string(x)`, §12).
- File qualification never reaches into an `impl` block. `users.to_string(u)`
  is the error `file 'users' has no member 'to_string'`, whose help names the
  owner: `it implements Display for User, so call it as User.to_string(...) or
  Display.to_string(...)`.
- A bare call of an interface function (`to_string(u)`) is an error. Inside an
  `impl` block, sibling functions are called bare.

An interface-qualified call selects the impl for the concrete type bound to
`self` by the arguments, or by an explicit type argument for a function with
no `self` parameter (`FromJson.from_json<User>(json)`). Its type arguments
name the implementor first, then the function's own type parameters in
order, and the implementor may stand alone when the arguments determine the
rest: for `fn conv<U>(u: U) -> String`, `Conv.conv<Money, Int>(5)` and
`Conv.conv<Money>(5)` both run Money's `conv` at `Int`, and
`Conv.conv<Money, Int, Int>(5)` is
`expected 1 or 2 type argument(s), got 3`. So a function whose signature names neither `self` nor a type parameter
of its own takes the implementor as its one type argument
(`Unit.symbol<Meters>()` for `fn symbol() -> String`), and in a generic body
that may be a type parameter (`Unit.symbol<P>()` under `where P: Unit`). An
argument in a `self` position must be the implementor the type argument
names, and so must the left operand of an operator interface's function:
`Add.add<Float>(1.5, 2.5)` is a Float, and `Add.add<Float>(1, 2)` is
`argument 1: expected Float, got Int`. A pipe chains qualified calls:
`x |> Shape.area() |> Display.to_string()`.

### Pipes

Taught in [Pipes](/pipes/#the-pipe-operator).

`x |> stage` passes `x` to the stage. The value of the pipeline is the last
stage's.

- **A call stage.** `x |> f()` is `f(x)` and `x |> f(a)` is `f(x, a)`: the
  value fills the first argument, and is checked as that argument is, so a
  pipe accepts what the call accepts: with `fn show(fs: List<(Int) ->
  Display>)`, `[number, word] |> show()` builds that list as
  `show([number, word])` does. A `.Variant` stage is written through its
  enum (`x |> Shape.Dot()`), since a bare `.Variant` cannot see its enum.
- **A name is not a stage.** A name without parentheses is a function
  reference, so `x |> f`, `x |> io.print`, `x |> Ok` and `x |> Shape.area` are
  errors that name the call to write: `` a pipe stage is a call: write
  `io.print()` ``.
- **The value fills only the first argument.** An `&N` in a call stage stands
  outside every capture, so `x |> divide(100, &1)` is two errors: ``expected 2
  arguments, got 3 (counting the piped value)``, and `` `&1` is a capture's
  parameter: write the function as `&(...)` or `&f(...)` ``, whose help says
  `` a pipe fills the first argument; write `|> then &divide(100, &1)` ``.
- **A stage that does not take the value** is an error at the stage that
  names the argument the value fills, with a note at the value's source:
  `` `String.pad_start` takes a String as its first argument, but the pipe
  passes a Fahrenheit ``. When the value's type implements `Display` and the
  stage takes a `String`, the help says to add `|> Display.to_string()`
  first.
- **A `?` after a stage** propagates that stage's `Err` or `None` and passes
  the value inside to the next stage (`id |> User.get()? |> User.profile()`).
  It may follow a call stage or the `end` of an `if` or `case` stage. A
  `?` inside a stage's parentheses is an error: `t |> (String.to_int()?)` is
  `` a `?` inside a stage's parentheses applies before the pipe passes its
  value: write the `?` after the stage, as in `|> f()?` ``.
- **A `!` after a stage** unwraps its result: `text |> String.to_int()! |>
  Int.abs()` (§11 *Unwrapping with `!`*).
- **A lambda body runs to the end of its expression,** or to the `)`, `]`, `}`
  or `,` that closes the construct it stands in. A `|>` inside it belongs to the
  body: in `Iter.map(xs, fn s -> String.to_int(s) |> Option.with_default(0))`
  the pipe is the lambda's. The exception is the lambda of a `then` or `tap`
  stage.
- **A lambda or a capture is not a stage** without `then`: `xs |> fn n -> n *
  2` is `` a lambda is not a pipe stage: write `then fn n -> ...` ``, and `x |>
  &f(&1)` is `` a capture is not a pipe stage: write `then &f(&1)` ``.

`then` and `tap` are reserved words. `tap` stands only after `|>`, and
`then` after `|>` or as an `if`'s (§8 *`if`/`else`*).

#### The `then` stage

Taught in [Pipes](/pipes/#the-then-stage).

`x |> then fn v -> body` applies the lambda to the piped value, as `(fn v ->
body)(x)` would. `x |> then &f(a, &1)` applies the capture, as `(&f(a,
&1))(x)` would, and `x` is checked as `f`'s argument at the `&1`.

```nomi
fn divide(a: Int, b: NonZeroInt) -> Int
    a / b
end

fn add(a: Int, b: Int) -> Int
    a + b
end

fn main()
    dbg 10 |> then &divide(100, &1) // 10
    dbg 3 |> then &add(&1, b: &1)   // 6

    dbg (1, 2)
    |> then fn (a, b) -> a + b // 3
end
```

- `then` is followed by a lambda or a capture. Anything else is the parse
  error `` `then` takes a lambda or a capture: write `then fn v -> ...` or
  `then &f(&1)` ``. For a function by name, `x |> then f` would be `x |> f()`,
  so it is that error with the help `` write `|> f()` for a function by name
  ``.
- The function takes one parameter, the piped value. A lambda's parameter may
  be a destructuring pattern. A capture's is `&1`, which may stand anywhere,
  positional or named, as often as it is written, and is computed once:
  `x |> then &connect(port: &1, "localhost")` is `connect("localhost", port:
  x)`. A second parameter is an error: `then &add(&1, &2)` is `` the function
  expected here takes one argument; `&2` names a second ``.
- A capture ends with its body, so `10 |> then &divide(100, &1) |> dbg`
  divides and then prints.
- A bare lambda body ends at the next `|>` of the pipeline, so the stages after
  it stay in the pipeline. A body written below `then fn v ->` and closed by
  `end` keeps a pipe inside it, and so do parentheses: `|> then fn v -> (v |>
  Iter.filter(.is_paid) |> Iter.count())`. A parameter of a bare `then` body
  used in a later stage is an error that says where the body ended.
- A partial capture that does not take the value at its `&1` is reported at
  the `&1`, as a call stage is: `` `label` takes a String as its second
  argument ``, or for a named `&1`, `` `label` takes a String as `text`, but
  the pipe passes an Int ``.
- `then?` is a `then` stage followed by a `?`: `text |> then? fn t -> parse(t,
  16)` applies the function and propagates an `Err` or `None` in its result. A
  `?` at the end of the lambda's body belongs to the body and exits the
  lambda.

#### The `tap` stage

Taught in [Pipes](/pipes/#the-tap-stage).

`x |> tap fn v -> body` runs the lambda on the piped value for its effect and
passes the value on unchanged. `x |> tap io.print` does the same with a
function by name, and `x |> tap &log(level, &1)` with a capture. The value is
computed once and the function runs once, at that point in the pipeline.

```nomi
import std::io

fn main()
    shout =
        "go north now"
        |> String.words()
        |> tap fn words -> io.print("got ${Iter.count(words)} words")
        |> Iter.map(String.uppercase)
        |> String.join(" ")
        |> tap io.print

    io.print(String.length(shout))
end
```

This prints `got 3 words`, then `GO NORTH NOW`, then `12`.

- The lambda follows the `then` rules: one parameter, which may be a pattern;
  a bare body ends at the next `|>`.
- The lambda returns `Unit`. A body of another type is `` `tap` passes its
  input on unchanged, so its lambda must return Unit; got Int ``, whose help
  says to use `then`. A body may end in a `dbg` observation, as a `Unit`
  function body may (§9).
- A function by name is a plain name (`log_it`), a qualified one
  (`io.print`, `Log.info`) or a local binding that holds a function,
  perhaps in parentheses: `tap (log_it)` is `tap log_it`. Only a name may
  stand in them; `tap (fn v -> ...)` is the parse error below. It is
  typed as a function value passed where `(T) -> Unit` is expected, `T` the
  piped value's type: a generic function is instantiated from the piped
  value, and a function whose parameters after the first all have defaults
  is called with them filled (§7 *Default Arguments*). Any other function is
  an error at the name: `` a `tap` function takes one parameter, the piped
  value; `pair` takes 2 ``, `` `says` takes a String, but the pipe passes an
  Int ``, or `` `tap` passes its input on unchanged, so its function must
  return Unit; `double` returns Int ``, whose help says to call it as a
  stage, `|> double()`. A name that holds no function is `` `tap` takes a
  function, but `count` is an Int ``. Anything after `tap` other than a name,
  a lambda or a capture is the parse error `` `tap` takes a function: write
  `tap f`, `tap fn v -> ...` or `tap &f(&1)` ``.
- A `?` or `return` in the lambda exits the lambda, so a body that uses `?`
  returns a `Result` or `Option` and is rejected.
- `tap?` is `` `tap` passes its input on unchanged, so there is no result for
  a `?` to propagate ``, whose help says to handle the failure inside the
  lambda or use `then?`.

#### Keyword stages

Taught in [Pipes](/pipes/#other-stages).

`dbg`, `if` and `case` consume one piped value. Each is a bare stage, or
prefixes the one call stage that computes its operand, condition or
scrutinee:

```nomi
status
|> if Status.is_ready() then "go" else "wait"

input
|> case parse_id()
    Ok(id) -> id
    Err(_) -> 0
end
```

`input |> case parse_id()` and `input |> parse_id() |> case`, each above the
same arms, are the same
program. An assertion wraps a pipeline from
its head (`assert message |> String.contains("ready")`, §17).

### Block expressions

Taught in [Bindings and Expressions](/bindings-and-expressions/#blocks-are-expressions).

`begin`, statements on the lines below, and `end` is an expression whose
value is its last statement's. A block stands anywhere an expression does: a
function argument, a pipe's head, a binding's value. `begin end` is the
empty block, whose value is `Unit`.

```nomi
fn main()
    total =
        begin
            x = 2
            y = 3
            x + y
        end

    dbg total // 5
end
```

- Bindings inside a block are scoped to it. This holds for every body: an
  `if` or `else` branch, a `case` arm, a `begin` block, a lambda body, a
  `concurrent` body. A `defer` inside a `begin` block runs at its `end`.
- A binding that shadows an outer name hides it until the block ends; a read
  after the block reads the outer binding. So an inner binding nothing in its
  block reads is the error `binding 'total' is never read`.
- A block whose last statement is a binding has the value `Unit`.
- The statement rules are in §9.

### `if`/`else`

Taught in [Bindings and Expressions](/bindings-and-expressions/#ifelse-and-case-are-expressions).

`if condition then ... else ...` is an expression. The condition is a
`Bool`, written without parentheses. It has two forms, told apart by what
follows `then` on its line:

- **Block form.** A `then` that ends its line takes the lines below as the
  branch, up to `else` or `end`. An `else` that ends its line takes the lines
  below to `end`. `else if c then` chains further branches, and the chain
  shares the one `end` that closes its last branch.
- **Inline form.** Anything after `then` on its line makes the `if` one
  expression on that line, with no `end`: `sign = if n < 0 then "-" else "+"`.
  Each branch is one expression, or `break`, `continue` or `return`. The
  `else` stands on the same line, and its branch extends as far right as an
  expression does, as a lambda's body does: `if c then a else b |> f` pipes
  `b` alone, and `(if c then a else b) |> f` pipes the `if`. A `,`, `)`, `]`
  or the end of the line ends it. `else if` chains work inline, and an `if`
  with no `else` is inline too: `if verbose then log("x")`.

An inline `if` is single-line only: an `else` on the next line is an error,
and `nomi fmt` writes an inline `if` too wide for its line in block form.

```nomi
fn sign(x: Int) -> String
    if x > 0 then
        "positive"
    else if x == 0 then
        "zero"
    else
        "negative"
    end
end

fn main()
    dbg sign(-3) // "negative"
end
```

- A condition that checks a binding against a constant, or tests it for
  emptiness, narrows it to a std subtype in each branch (§4 *Narrowing*):
  in `if d != 0 then n / d else 0`, `d` is a `NonZeroInt` in the first
  branch, and after `if d == 0 then return 0`, in the rest of the block.
- An `if` without `else`, and an `else if` chain with no final `else`, is
  a statement, of type `Unit`. Its branch is `Unit`, leaves (`return`,
  `break`, a `?` that propagates, a call that never returns), or ends in an
  `assert`, `refute` or `dbg`, whose value a block drops. So
  `if debug then log.debug("x")`, `if n < 0 then return Err(e)` and
  `if on then assert n > 0` are statements.
- A branch of any other type is an error at the branch's value, an `Option`
  included. A value that may be absent is written with its `else`:
  `if c then Some(x) else None`. A `fn first_even(n: Int) -> Option<Int>`
  whose body is `if n % 2 == 0 then Some(n)` is ``an `if` without `else` is
  a statement, so its branch must be `Unit`; this branch is an `Option<Int>`.
  Add `else None` to make the `if` an `Option<Int>` ``, and an editor's quick
  fix adds the `else None`. Any other branch says to add an `else`:
  `if n > 0 then "positive"` is ``... this branch is a `String`. Add an
  `else` to give the `if` a value``. Where a statement drops the branch's
  value, before a block's end or at the end of a `Unit` function, the error
  says ``... this branch is an `Int`, which this statement drops. Write
  `_ = n + 1` in the branch, or add an `else` ``, and the quick fix writes
  the `_ = `.

```nomi
fn sign(n: Int) -> Option<String>
    if n > 0 then
        Some("positive")
    else if n < 0 then
        Some("negative")
    else
        None
    end
end

fn main()
    dbg sign(3) // Some("positive")
    dbg sign(0) // None
end
```

- The branches have one type. Two types are the error `if/else branch type
  mismatch: then is Range<Int>, else is List<Int>`. Two joins need nothing
  written: a concrete type beside an interface it implements is the
  interface, and a type beside an enum that `embeds` it is the enum.
- Two types that both implement an interface join as that interface only
  where the value's type is written as it: a binding's annotation, a
  parameter's type or a function's declared result. Any interface joins
  this way, std's or the program's, generic or not:
  `s: Shape = if round then Circle{r: 1.0} else Square{side: 2.0}` is a
  `Shape`, `d: Display = if n > 0 then n else "none"` is a `Display`,
  and a `Range` beside a `List` under a written `Iter<Int>` is an
  `Iter<Int>`. Each branch's value enters the interface as any value
  assigned to it does. The same holds for every arm of a `case`. A pipe's
  head is its first stage's first argument, so
  `(if up then 1..4 else [3, 2, 1]) |> Iter.count()` joins at
  `Iter.count`'s `Iter<T>` parameter.
- Without a written interface the mismatch stands, and its help may name
  the annotation. When both branches are an `Iter` of one element type, the
  help is ``both are an Iter<Int>, and two kinds of Iter join only where the
  value's type is written as one: annotate it `: Iter<Int>`, or declare the
  function's result as `Iter<Int>` ``. When exactly one interface declared
  outside std, with no type parameters, is implemented by both types, the
  help names it: ``both implement Shape, and two types join as an interface
  only where the value's type is written as one: annotate it `: Shape`, or
  declare the function's result as `Shape` ``. No std interface but `Iter`
  is named, since nearly any two types share `Display`, `Debug` and
  `Equatable`, and two candidate interfaces give no help.
```nomi
fn in_order(values: Range<Int>, descending: Bool) -> Iter<Int>
    if descending then Iter.reverse(values) else values
end
```

**Pattern `if`.** `if Pattern = value then ... else ...` runs the first
branch when the value matches the pattern, with the pattern's names bound
there only, and the `else` branch otherwise. Without `else` it is a
statement, as an `if` without `else` is: `if Some(n) = m then io.inspect(n)`.

```nomi
fn label(option_name: Option<String>) -> String
    if Some(name) = option_name then
        name
    else
        "unknown"
    end
end
```

The pattern must be able to fail. One that matches every value of the type (a
name, `_`, a tuple of names, a struct or distinct destructure, a single-variant
enum) is the error ``this pattern always matches a value of type Int, so the
`if` has nothing to test; bind it on its own line with `pattern = value` ``.
When `if x = 0` names an `x` of the value's type already in scope, the help
says to write `if x == 0`.

### `case`

Taught in [Pattern Matching](/pattern-matching/#binding-patterns-and-guards).

`case value`, its arms on the lines below and `end`, matches the value
against each arm's pattern in order and has the value of the first arm that
matches. An arm is `Pattern -> body` or `Pattern when guard -> body` (§10
*Guards*). The arms must be exhaustive (§10 *Exhaustiveness checking*).

`case` alone on its line, with no value, is a conditional. Each arm is `condition ->
body`, and the first arm whose condition is `True` runs. A condition is a
`Bool`: `x + 1 -> ...` is `case condition must be Bool, got Int`. The arms must
end with `_ -> body`, since conditions cannot be shown exhaustive: without one
the error is ``non-exhaustive case: add a `_` arm``. An arm's body runs where
its condition is true and every earlier one false, which narrows the
bindings they check (§4 *Narrowing*): after `n < 0` and `n == 0`, the `_`
arm's `n` is a `PositiveInt`.

```nomi
fn size(x: Int) -> String
    case
        x > 10 -> "big"
        x > 0  -> "positive"
        _      -> "not positive"
    end
end

fn main()
    dbg size(4) // "positive"
end
```

- The arms have one type, by the rules for `if` branches.
- Each arm starts its own line. A body on the `->`'s line is one
  expression. A `->` that ends its line takes the lines below as the body, up
  to the next line that starts an arm or the `case`'s `end`, as Elixir reads
  an arm. A line starts an arm when what stands before its first `->`
  outside brackets is a pattern with an optional `when` guard; a statement is
  never one, since a pattern holds no `=` and a lambda's `->` stands inside
  a call's parentheses. Indentation decides nothing.

```nomi
fn describe(n: Option<Int>) -> String
    case n
        Some(x) when x > 9 ->
            digits = Int.to_string(x)
            "big: ${digits}"

        Some(x) ->
            "small: ${x}"

        None ->
            "none"
    end
end

fn main()
    dbg describe(Some(42)) // "big: 42"
end
```

- `case e end` on one line has no arms, for a value of a type with none.
- `break`, `continue` and `return` may be an arm's body (§9).
- `case` is a pipe stage (*Keyword stages*).

### `dbg`

`dbg expr` prints a Debug rendering of `expr` with its source line and source
text, and has the value of `expr`. `|> dbg` is the same as a pipe stage, and
passes the value on.

```nomi
fn main()
    total =
        [1, 2, 3]
        |> Iter.map(&(&1 * 2))
        |> Iter.to_list()
        |> dbg // prints the list and passes it on
        |> Iter.sum()

    dbg total + 1 // prints `dbg line 9: total + 1 = 13`
end
```

- The rendering is `Debug.inspect`'s, as `io.inspect` prints it (§12 *Display
  and Debug*).
- A parenthesized `dbg expr` is the value of `expr`, not a stage: `x |> (dbg
  1)` is the error `` a parenthesized `dbg expr` is not a pipe stage: it is
  the value of `expr`, and the pipe has nothing to call ``, whose help names
  `|> dbg`.
- A `dbg` may be a non-final statement, and may end a `Unit` body (§9
  *Statements and the Unit rule*).
- `dbg` is for development: `nomi build` refuses a program that holds one
  ([tooling reference](/tooling/building/#executables)).

## 9. Statements and control flow

### Statements and the Unit rule

A block, and a function, lambda or test body, is a sequence of statements: a
binding (§6), an expression, `defer`, `with`, an assertion, a nested
declaration (§7 *Function declarations*), `return`, `break` or `continue`.
Statements end at a newline (§2).

- A statement before the last one that is an expression must have type `Unit`,
  unless it is a `dbg` observation. Its value would otherwise be lost: `1 + 2`
  there is the error ``non-final expression has type Int, but only Unit results
  can be ignored — write `_ = ...` if intentional, use `dbg ...` to inspect it,
  or move it to the end to return it``. Discard a value with `_ = expr`.
- The last statement of a body whose return type is `Unit`, written or
  omitted, may be a `dbg` observation: `dbg expr`, or a pipe whose last stage
  is `|> dbg`. The body returns `Unit`. Any other last statement whose type is
  not `Unit` is the error `return type mismatch: expected Unit, got Int`.
- A body whose last statement is no expression (a binding, a destructure,
  `defer`, `with` or a nested declaration) has type `Unit`.
- A statement after one that always exits is unreachable code and an error
  (§7 *Returning*).

```nomi
import std::io

fn main()
    total = 2 + 3
    io.print("computed")

    [1, 2, 3]
    |> Iter.map(&(&1 * total))
    |> Iter.to_list()
    |> dbg
end
```

### `defer`

Taught in [App Fields, Defer and Context](/app-fields/#defer-runs-block-scoped-cleanup).

`defer call(args)` registers a call to run when the enclosing block ends.

- **The operand is a call.** Anything else is the error `` `defer` requires a
  function call ``. The call returns `Unit`: `defer g()` with a `g` that
  returns a `Result<Int, String>` is `` `defer` call must return Unit, got
  Result<Int, String> ``. A call of a function value works (`defer f()`).
- **Operands are evaluated where the `defer` stands.** The function and its
  arguments are evaluated when the statement runs, with the bindings and the
  application fields (§15) in scope there. The call runs later with those
  values.
- **It runs when its block ends,** the most recently registered first. A block
  is a function, lambda or test body, an `if` branch, a `case` arm, a binding's
  `else` body, a `concurrent` body or a `begin` block. A `defer` in a lambda
  runs when that call of the lambda ends, so one in an
  `Iter.loop` or other iteration callback runs at the end of each iteration,
  before the next starts.
- **Every way out of the block runs it:** reaching its end, `return`, a
  callback's `break` or `continue`, a `?` that propagates a failure, and a trap.
  A task stopped by cancellation runs its pending deferred calls as it unwinds
  (§16). A deferred call that traps does not stop the ones registered before
  it, and does not hide the failure that ended the block.
- **It is a statement.** `x = defer f()` is a parse error (``unexpected
  `defer` ``). At file level it is the error `this statement cannot be written
  at file level: a file holds declarations, and code runs inside a function`.
- **A pending `defer` stops a tail call.** A call in tail position while a
  `defer` in a block around it is pending is an ordinary call, since the
  deferred call runs after it returns (§7 *Tail-call optimization*).
  Recursion that registers a `defer` at each step keeps a frame per step and
  counts toward the recursion depth limit (§7 *Recursion depth*). A `defer` in
  a block that has ended, such as a branch of an earlier `if`, is not pending.
- A function that uses `break` or `continue` cannot be the deferred call (§13
  *Named Functions as Callbacks*).
- In an entry file's `boot`, a `defer` written directly in its body runs after
  `main` and the tasks it owns have finished. In a test, one in `boot` or
  `setup` runs when that test finishes, also when the test called boot
  itself (§15, §17).

**`defer on error call(args)`** registers a call that runs only if the block
is left through an error exit, and is dropped otherwise. It is otherwise a
`defer`: its operand is a Unit call evaluated where it stands, and it is on
the same list as the block's other deferred calls, so it runs in its place
among them, most recent first.

- **An error exit** is a trap (and a cancelled task's unwinding), or the
  function, lambda or test body returning `Err(...)` when its result is a
  `Result`, or `None` when it is an `Option`, whichever way it returns it: a
  `?` that propagates, `return Err(e)`, or a last expression that is an
  `Err`.
- **Anything else is a normal exit**, and drops the call unrun: the block
  reaching its end, a `return` or last expression of any other value, and a
  callback's `break` or `continue`. A `defer on error` in a nested block
  whose end is reached is dropped there, even if the function fails later.
- **The function must be able to fail.** In a function whose result is
  neither a `Result` nor an `Option`, where only a trap could run it, it is the
  error `` `defer on error` runs only when `f` fails, and it returns Int,
  which carries no failure; write `defer` ``.
- **`on error` is not reserved.** The two words open the form only right
  after `defer` on its line; `on` and `error` stay names everywhere else, and
  `defer on(x)` defers a call of `on`.
- A function that registers one makes no tail call while it is pending, as
  for `defer`.

```nomi
import std::io

fn note(label: String)
    io.print(label)
end

fn half(n: Int) -> Result<Int, String>
    defer note("done")
    defer on error note("failed ${n}")
    if n % 2 != 0 then return Err("odd")
    Ok(n / 2)
end

fn main()
    _ = half(4) // done
    _ = half(3) // failed 3, done
end
```
```nomi
import std::io

fn work(n: Int) -> Int
    defer io.print("a ${n}")
    defer io.print("b ${n}")
    io.print("work ${n}")
    n + 1
end

fn main()
    _ = work(1) // work 1, b 1, a 1

    Iter.each([1, 2], fn i ->
        defer io.print("end ${i}")
        io.print("body ${i}")
    end) // body 1, end 1, body 2, end 2
end
```

### Releasing resources

Taught in [Files](/files/#streams).

A type that holds something outside the program (a file, a process, a
connection) implements the prelude interface `Release`, and a type whose
release throws away work done with the value also implements `Commit<E>`:

```nomi
pub interface Release
    fn release(value: self) -> Unit
end

pub interface Commit<E>
    fn commit(value: self) -> Result<Unit, E>
end
```

The body of an impl's `release` or `commit` is one call, which names the
type's usual release or commit: `impl Release for Writer` calls
`Writer.discard(w)`, and `impl Commit for Writer` calls
`Writer.close(w)`. In std, `Reader` releases with `Reader.close_quietly`,
`Writer` with `Writer.discard` and commits with `Writer.close`, `File` with
`File.close_quietly`, `Child` with `Child.kill`, `http.StreamedResponse`
(what `http.stream` returns) with `StreamedResponse.close`, `wasm.Instance`
with `Instance.drop`, and `sql.Conn` with `sql.close_quietly`. A type of the
program's own may implement either.

**An acquisition must be released.** An acquisition is a call whose result is
a value of a Release type, or a `Result` with one in its `Ok`, and which
names no value of a Release type outside a lambda. Opening can fail
(`fs.open`, `fs.create`, `process.spawn`, `http.stream`, a database
driver's `open`) or not (`postgres.pool`, a program's `fn lock() -> Lock`).
A call given a Release value returns a view of it that its own release
covers: a transaction's `Conn` from `Conn.begin_transaction(conn)`, a child's
`Child.stdout(child)`. Two calls that return a Release value directly are
no acquisition:

- a constructor of a variant or distinct type (`Handle(raw)`), which builds
  the value from parts the body already holds;
- a call returning a `Reader` or a `Writer`. std makes them over memory
  (`Reader.from_text(s)`) and borrows them from values of no Release type
  (`Body.reader(body)`, `Connection.reader(conn)`), none of which the caller
  releases, so only a `Result` holding one acquires it.

A Release value that is not a call's, such as `io.stdout`, a parameter or a
struct literal, is no acquisition either.

A name a body binds to an acquired value (`w = fs.create(path)?`, an
`Ok(r)` arm of a `case` over one, `Ok(r) = fs.open(path) else return`) is
released or handed on in that body:

- **released**: a `defer` among the statements after the binding, in the
  block the binding is in (or the arm's block), calls a function that takes
  it: `defer Writer.discard(w)`, `defer Child.kill(child)`, the type's
  `release`;
- **handed on**: it is returned, or is the value of its block; it is an
  element of a list, set, vector, map, tuple or struct literal, or the
  argument of a variant or distinct type's constructor (`Ok(r)`,
  `Body.Stream(r)`), wherever that value goes; or another name is bound to
  it.

A `defer on error` that calls a function taking it releases it on the error
exits only, so it does not count as released on its own: a name with one
and no hand-on is the error `` `r` is released only if the body fails;
write `defer Reader.close_quietly(r)` after it, or return it ``.

**A value handed on is handed on along every path.** A name with no `defer`
that releases it is handed on along each path that returns normally, or
released on it. A normal return is a `return` whose value is not `Err(...)`
or `None`, the body's last expression through the branches of an `if`, a
`case` or a block that ends it, and the end of the block the binding is in.
The path hands the value on when the return's value carries it (`r`,
`Ok(Some(r))`, a struct holding it) or an earlier statement on that path
handed it on, and releases it with a call of the type's release or commit
that takes it (`Reader.close_quietly(r)`). A `defer on error` does not run on
a normal return, so it does not cover one. A path that does neither is the
error `` `r` leaks here: this path returns without handing it on or
releasing it; call `Reader.close_quietly(r)` before the return ``, at the
return. A plain `defer` is no fix when another path hands the value on: it
would release that value too (*A released value stays in its block*, below).

```nomi
fn option_open(path: String, want: Bool) -> Result<Option<Reader>, io.Error>
    r = fs.open(path)?
    defer on error Reader.close_quietly(r)

    if not want then
        Reader.close_quietly(r) // without it, `r` leaks here
        return Ok(None)
    end

    Ok(Some(r))
end
```

An acquisition whose value is dropped is the error `` the `Reader` this
acquires is never released; bind it and write `defer
Reader.close_quietly(reader)` after it ``: one bound to `_`
(`_ = fs.open(path)?`), and one that is the last expression of a body whose
value nothing takes, a test's or a callback's whose result is `Unit` (the
function `Iter.each` calls). Any other statement whose value is not `Unit`
is already an error.

Passing it to a call is a use, not a hand-on: the check reads one body and
does not follow the value into the callee, so a function that releases a
parameter does not release its caller's acquisition. A body is a function's,
a lambda's or a test's, each checked on its own. A name that is neither is
the error `` `r` is never released; write `defer Reader.close_quietly(r)`
after it, or return it ``, at the binding, which names the type's usual
release. A `defer` inside an `if` releases on one path only, so it does not
count.

**A released value stays in its block.** A deferred call runs when its block
ends, so a value it releases is released by the time anything outside the
block sees it. When a `defer` calls a function given a value of a Release
type, written as a name or a field path (`db`, `db.conn`), that value must
not leave the `defer`'s block after it: not as the block's value, not in a
`return`, not inside a literal or a constructor that does (`Ok(r)`,
`App{store: db}`), not through a name bound to one, and not captured by a
lambda that does. A value holding the released one counts too:
`defer sql.close_quietly(db.conn)` releases part of `db`, so `db` stays in
the block as well. Each is the error `` this `defer` releases `db.conn` when
the block ends, but `db` leaves the block as its value; defer the release
where `db` is finally used, or hand `db` on without deferring it here ``, at
the `defer`. Using the value in the block, passing it to a call, and a
`defer` in an outer block are fine. A `defer on error` is not held to this:
its release runs only when the block fails, so the value it covers is meant
to leave on the success path.

**An error exit before the hand-on releases.** In a body whose result is a
`Result` or an `Option`, or a test's, an error exit between a binding and the
statement that defers its release, or, with no `defer`, the statement that
hands it on, leaks the value: a `?` that propagates, or a `return` of
`Err(...)` or `None`, outside a lambda. With no deferred release it is the
error `` `conn` leaks if this `?` returns an error: it isn't released on that
path; add `defer on error sql.close_quietly(conn)` after acquiring it ``
(`` if this `return` runs `` for a `return`), at the first such exit. With a
`defer` or a `defer on error` after it, the error says to move it up to
right after the binding. A trap and `!` are not checked; the program stops.
The check stops at the first statement that hands the value on, so an
error exit after the value is stored in a struct that is later returned is
not seen.

```nomi
pub fn open(path: String) -> Result<SqliteStore, sql.Error>
    conn = sqlite.open(path)?
    defer on error sql.close_quietly(conn) // released if a later `?` fails
    _ = Conn.exec(conn, Sql"CREATE TABLE IF NOT EXISTS notes (body TEXT)")?
    Ok(SqliteStore{conn:})
end
```
```nomi
fn open_app(kind: String, path: String) -> Result<App, sql.Error>
    store =
        case kind
            "sqlite" ->
                db = sqlite_store.open(path)?
                defer sql.close_quietly(db.conn) // error: `db` leaves the block as its value
                db

            _ ->
                memory_store()
        end

    Ok(App{store:})
end
```

**A deferred discard needs a commit on every normal return.** When the
deferred call is a Commit type's usual release (`Writer.discard`, which drops
a `ReplaceAtomically` writer's temporary file), every path from the `defer`
that returns normally calls the commit, or the release itself, which discards
on purpose, before it returns. A normal return is a `return` or a block's
last expression that is not `Err(...)` or `None`, and the end of a function
whose result is `Unit`; the end of the block the `defer` is in, when that
block is not the body's last, discards too. A path that leaves by `?`,
`return Err(...)`, `unreachable` or a trap needs no commit, and neither does a
commit inside a lambda on the path, which counts as made. A path without one
is the error `` `w` is discarded here: this path returns without
`Writer.close(w)?` `` at that return, written with `!` in a body whose result
is neither a `Result` nor an `Option`. A deferred release that commits as it
releases (`Writer.close_quietly`, `File.close_quietly`) needs nothing more; a
failed close is then dropped, so the success path writes `Writer.close(w)?`
when it must report one.

```nomi
fn save(path: String, rows: List<String>) -> Result<Unit, io.Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Writer.discard(w)

    Writer.write_lines(w, rows)?

    if rows == [] then
        return Ok(Unit) // `w` is discarded here: this path returns without `Writer.close(w)?`
    end

    Writer.close(w)?
    Ok(Unit)
end
```

The editor's quick fix for an unreleased name writes the `defer` after the
binding and, for a Commit type, the commit before each return that lacks
it; the one for a dropped acquisition first binds it to a name; the one for
a missing commit writes the commit; the one for an error exit that leaks
writes the `defer on error` after the binding; the one for a normal return
that leaks writes the release before it, when the return starts its line.

### `break` and `continue`

Taught in [Iteration and Loops](/iteration-and-loops/#break-and-continue-in-iter-callbacks).

`break` and `continue` act on an iteration. They are valid only in a callback
passed to one of the iteration functions that take them (§13 *Callbacks that
take `break` and `continue`*), and in a named function passed as such a
callback (§13 *Named Functions as Callbacks*).

- `continue` skips the rest of this element or round. It takes no value:
  `continue x` is the error `continue takes no value; it goes to the next
  iteration with the loop state or accumulator unchanged. To carry a new value
  forward, make it the callback's result: write it as the last expression,
  without continue (in Iter.loop that result is the next state)`.
- `break` stops the iteration with what has accumulated so far. `break value`
  stops it after `value` is taken as this element's result.
- They leave only the callback they are written in. Nomi has no non-local
  control flow and no exceptions.
- They may be a `case` arm's body or an `if` branch's.
- Outside an iteration callback they are an error. In a lambda that is not one
  the error is `'break' can only be used inside an iter-callback (e.g. a lambda
  passed to Iter.map/Iter.filter/Iter.loop/etc.)`. In a function the run
  calls itself, `main`, `boot` or an `export fn`, at any visibility, and in
  a `private` function its file never passes as a callback, it is `'main'
  uses break/continue but has no iter-callback call site — break/continue
  can only be used where an iter-callback (Iter.map/Iter.filter/Iter.loop/etc.)
  can catch them`. An internal or `pub` function no use in its own file
  passes is no error there, since another file may pass it.

```nomi
fn main()
    total = Iter.loop(0, fn n ->
        case n
            3 -> break n
            _ -> n + 1
        end
    end)

    dbg total // 3
end
```

### `with`

`with App.field = value` replaces one application field from that line to the
end of its block. It is defined in §15.

### `assert` and `refute` outside tests

An `assert` or `refute` in a function body returns `Err(AssertionFailure)` from
the function when it fails (§17). The function must return a
`Result<_, AssertionFailure>`; otherwise the assertion is the error ``
`assert` can return AssertionFailure, but the enclosing function returns Unit
``. An assertion in a lambda is an error.

```nomi
import std::assertions.AssertionFailure

fn checked(n: Int) -> Result<Int, AssertionFailure>
    assert n > 2
    Ok(n)
end

fn main()
    dbg Result.is_ok(checked(3)) // True
    dbg Result.is_ok(checked(1)) // False
end
```

## 10. Patterns

Taught in [Pattern Matching](/pattern-matching/).

### Where patterns appear

A pattern stands in a `case` arm (§8 *`case`*), a pattern `if` (§8
*`if`/`else`*), a binding (§6), a binding with `else` (*Bindings with
`else`*), an `assert` (§17), an `is` test (§8 *Pattern test: `is`*), a
function or lambda parameter (§7 *Parameter Destructuring*), a test's setup
binding and a `subtests` row (§17).

A pattern is *refutable* when some value of its type does not match it, and
*irrefutable* when every value does.

- A `case` arm, a pattern `if`, a binding with `else`, an `assert` and an `is`
  test take any pattern.
- A plain binding, a parameter, a lambda parameter, a test's setup binding and
  a `subtests` row take an irrefutable pattern. A refutable one in a binding is
  the error `` this pattern can fail to match a value of type Option<Int>; add
  `else` to handle a value it does not match, or match it with `case`
  or `if Pattern = expr` ``, and in a parameter `` refutable pattern in
  parameter; bind the parameter and use a `case` in the body ``.
- Irrefutable patterns are names, `_`, tuple, struct and distinct patterns
  whose parts are irrefutable, a single-variant enum's variant, and an `or`
  pattern whose alternatives together cover the type (*Exhaustiveness
  checking*).

A name in a pattern binds the matched value. A name that starts with `_` is a
discard and binds nothing. A pattern binds each name once: `Some(n) as n` is
`` `n` is bound twice in one pattern; give each binding its own name ``.

A name never compares, even when it names a value in scope. In a pattern that
may fail to match (a `case` arm, a pattern `if`, a binding with `else`, an
`else` arm and an `assert`), a name that names a `once` visible there, declared
in the file or imported, is an error at the name, since it would match every
value: with `once not_found = 404`, an arm `not_found -> ...` of
`case code` is
`` this binds a new `not_found`, which matches every value; `not_found` is a
`once` ``, with the help ``to compare with it, write `code when code ==
not_found` ``. A local binding of the same name hides the `once` and the rule
does not apply, and neither does a discard name. A plain binding, a
parameter and a destructure without `else` bind new names by intent: there a
name may hide a `once` as it may any other name.

### Literal patterns

A literal pattern (`3`, `-1`, `2.5`, `1.50d`, `"a"`, `'a'`, `True`) matches a
value equal to the literal. The value must have the literal's type, at any
depth. A literal never converts: an arm `"a" -> ...` over an `Int` is the
error `pattern "a" is a String, but the value is an Int`, and `3` does not
match a `Float` (write `3.0`).

The constants are patterns too. `True`, `False` and `None` are variant
patterns (*Variant patterns*), and `Unit` matches the one value of type
Unit, so it covers its type: `Ok(Unit)` and `Err(e)` are exhaustive over a
`Result<Unit, E>`. `Unit` over a value of another type is the error
`pattern Unit is a Unit, but the value is an Int`.

### Variant patterns

A variant pattern names the variant in the dot-leading form (`.Circle`,
`.Circle{r:}`, `.Some(x)`) or qualified by its enum (`Shape.Circle`,
`Shape.Circle{r:}`). Its payload pattern is written as the variant is built
(§4 *Enums (Algebraic Data Types)*).

```nomi
enum Shape
    Circle {r: Float}
    Rect {w: Float, h: Float}
    Point
end

fn area(shape: Shape) -> Float
    case shape
        .Circle{r:}   -> 3.14 * r * r
        .Rect{w:, h:} -> w * h
        .Point        -> 0.0
    end
end

fn main()
    dbg area(Shape.Rect{w: 2.0, h: 3.0}) // 6.0
end
```

- A `.Variant` resolves against the scrutinee's enum, by the outside-in walk of
  §4 *Variant Resolution*, with the scrutinee as the determining position.
- A qualifier must name the scrutinee's enum, or a module-qualified or imported
  spelling of it. An unknown name is an error (`C6lor.Blue` is `unknown type
  "C6lor"`), and so is another enum, even one with a variant of that name.
- A bare variant name (`Circle`, `Circle{r:}`, `Items[x, y]`, `Named{"k" =>
  v}`) is accepted only for a prelude variant (`Ok`, `Err`, `Some`, `None`,
  `True`, `False`) or one an owner selector imports (`import
  shape.Shape.{Circle}`). Otherwise it is the error `bare variant 'Blue' in
  pattern position; use '.Blue' (or 'Color.Blue')`.
- A variant that carries data is matched with its payload written:
  `Some(_)`, `Err(_)`, `.Rect{}`. A bare `Err` is the error `variant Err
  carries data; pattern needs an explicit payload (e.g. Err(_) or Err(name))`.
  A payload of a type with one value is still data: `Ping(Unit)` is matched as
  `.Ping(_)`. Only an `embeds` of a zero-sized type (`embeds Expired`) is
  matched bare.

### Struct Destructuring

`Type{field: pattern, ...}` matches a struct. `{field: pattern, ...}`
matches a struct or an anonymous struct.

```nomi
struct User
    name: String
    age:  Int
end

fn describe(user: User) -> String
    case user
        User{name: "Alice"}             -> "found Alice"
        User{name:, age:} when age > 18 -> "adult: ${name}"
        {age: years}                    -> "minor of ${years}"
    end
end

fn main()
    dbg describe(User{name: "Bo", age: 9}) // "minor of 9"
end
```

- Matching is partial: fields left out are not tested.
- `field:` alone binds the field to a name of the same name, and `field:
  pattern` matches it against the pattern.
- A written type name must name the value's struct, directly or through a
  `typealias`. A typed pattern does not match an anonymous struct: `User{name:}
  = {name: "Ada"}` is the error `struct pattern names User, but the value is
  the anonymous struct {name: String}`.
- A pattern names each field once: `User{age: 1, age: a}` is `field 'age' is
  given twice in this User pattern`, and an anonymous one says `this struct
  pattern`. A punned repeat (`User{age:, age:}`) also binds `age` twice: ``
  `age` is bound twice in one pattern; give each binding its own name ``.

### Tuple patterns

`(p1, p2, ...)` matches a tuple of that many elements, each against its
pattern.

### Distinct Type Destructuring

A distinct type's value is matched in the form it is built: `Id(n)` for a
distinct over a scalar, and for one over a tuple, map, vector or set either the
named literal pattern (`Pair(a, b)`, `Kvs{...}`, `Path[...]`, `Tags[...]`)
or the call form (`Pair((a, b))`, `Kvs({...})`).

```nomi
type Pair (Int, String)

fn show(p: Pair) -> String
    case p
        Pair(a, b) -> "${a}, ${b}"
    end
end

fn first(p: Pair) -> Int
    Pair((a, _)) = p
    a
end

fn main()
    dbg show(Pair(1, "hello"))  // "1, hello"
    dbg first(Pair(1, "hello")) // 1
end
```

The call form takes one value. For a tuple distinct, `Pair(a)` binds the whole
tuple to `a`, and `Pair(a, b)`, one pattern per element, takes the tuple apart
with it.

A distinct declared in another module is named in a pattern as it is in a
literal, bare when imported or through its module: `kinds.Items[a, b]`,
`kinds.Kvs{"a" => v}` and `kinds.Pair(a, b)` each take apart that module's
type. The prefix names a type as an annotation does, and must be the value's
type itself: another module's type of the same name is a different type
(`pattern names other.Items, but the value is a kinds.Items`), and so is any
other type (`pattern names kinds.Other, but the value is an Items`).

### List Destructuring

Taught in [Pattern Matching](/pattern-matching/#lists-vectors-and-sets).

A bracket pattern takes apart the collection its subject is: a list or a
vector by position (this section and *Vector Destructuring*), a set by
membership (*Set Destructuring*). The pattern has no symbol of its own, as
a bracket literal has none (§3 *Literal forms*).

`[p1, p2]` matches a list of exactly that many elements. `..` among the
elements matches any number of elements between those before it and those
after it.

```nomi
fn describe(lines: List<String>) -> String
    case lines
        []                       -> "empty"
        [only]                   -> "just ${only}"
        ["graph {", ..body, "}"] -> "a graph of ${Iter.count(body)} lines"
        [first, ..rest]          -> "${first} and ${Iter.count(rest)} more"
    end
end

fn main()
    dbg describe(["graph {", "a", "b", "}"]) // "a graph of 2 lines"
end
```

- **One `..`.** Elements before it match from the front and elements after it
  from the end. A second `..` is the parse error ``a list pattern takes one
  `..` ``.
- **What follows the `..`.** At the end of the pattern, `..` takes a pattern
  for the rest, a `List`, or nothing. With elements after it, it takes a name or
  nothing (`[first, ..mid, last]`, `[1, .., 9]`); anything else is the parse
  error ``a list pattern's `..` takes a name or nothing when elements follow it``.
- A bare `..` ignores the rest. `.._` is the parse error ``write `..` to ignore
  the rest of a list pattern, not `.._` ``, over a list, a vector or a set. A
  labeled discard (`.._rest`) is allowed.
- **Cost.** A rest at the end shares the list's cells, so `[x, ..rest]` takes
  no copy and a recursive walk over it is linear. Elements after the `..` take
  one walk to the list's end, and a name for the middle (`..mid`) is a copy of
  those elements.
- A list pattern is refutable. Coverage is by length (*Exhaustiveness
  checking*): `[1, .., 3]` can fail, so as a binding it takes an `else`, and
  alone in a `case` it is ``non-exhaustive case on List<Int>: missing []``.
  A rest that is a list pattern covers the elements it names, so `[a,
  ..[b]]` covers the lists of two elements, as `[a, b]` does; a rest any
  other pattern constrains covers no length.
- Over a value that is no list, vector or set it is the error `list pattern
  requires a list, vector or set, got Int`.
- `+` patterns are for strings only (*String Prefix Patterns*).

### Vector Destructuring

Over a vector, `[...]` matches as it matches a list, from either end.

```nomi
fn ends(v: Vector<Int>) -> Int
    case v
        []                -> 0
        [only]            -> only
        [first, .., last] -> first + last
    end
end

fn main()
    dbg ends([1, 2, 3]) // 4
end
```

- **One `..`, anywhere among the elements,** followed by a name, which binds
  the elements between as a `Vector`, or by nothing. Anything else after it
  is ``the rest of a vector pattern is a name or nothing: `..rest` or `..` ``.
- Without a `..`, the pattern matches only vectors of its length: `[a, b]`
  matches a two-element vector and `[]` the empty one.
- The rest shares the vector's storage, so binding it takes no copy.
- A vector pattern is refutable, so as a parameter pattern it is the error
  `refutable pattern in parameter`. Coverage is by length, as a list's is:
  `[]` with `[x]` is ``non-exhaustive case on Vector<Int>: missing [_, _,
  ..]``, and `[1, .., 3]` alone is ``non-exhaustive case on Vector<Int>:
  missing []``.
- A type built on a vector names itself before the bracket, as its literal
  does: `Path[a, .., z]` for a `type Path Vector<Int>`, and `.Items[x, ..]`
  for a vector-payload variant. Over the type itself, `[a, .., z]` takes it
  apart too.

### Map Destructuring

`{key => pattern, ...}` matches a map that holds every key, each value
matching its pattern. Other keys are ignored. `{}` holds no key and matches
every map.

```nomi
fn port(config: Map<String, Int>) -> String
    case config
        {"debug" => 1, "port" => p} -> "debug on ${p}"
        {"port" => p}               -> "port ${p}"
        _                           -> "no port"
    end
end

fn main()
    dbg port({"port" => 8080}) // "port 8080"
end
```

- A key is an expression, evaluated in the scope before the pattern, as a map
  literal's key is. Any keyable value works (variants, tuples, structs), and a
  key may be computed: `{target => v}` matches the entry whose key equals
  `target`'s value. A key never sees a name the pattern itself binds: after `a =
  "host"`, `{"mode" => a, a => b}` looks up `"host"` for `b` and binds the
  mode's value to `a`.
- The first entry's `=>` makes the braces a map pattern, as `name:` makes
  them a struct pattern (§3 *Literal forms*).
- A missing key is a pattern that does not match, never a trap, since whether
  a key is present is data the program does not control (§11 *Traps, `Option`
  and `Result`*).
- A map pattern never covers every map for the coverage check, `{}` included,
  though `{}` matches every map. A `case` over a map needs a `_` arm
  (``non-exhaustive case: add a `_` arm``).
- A binding with a map pattern takes an `else`, every path of which leaves
  (*Bindings with `else`*). Without one, `{"port" => port} = config` is the
  error `` this pattern can fail to match a value of type Map<String, Int>; add
  `else` ... ``, whose help is `` a map pattern does not match a map
  that lacks one of its keys. Handle a missing key with `else`, or read
  one key with `Map.get`, which returns an `Option` ``. In a test, `assert
  {"port" => port} = config` fails when a key is missing.
- The same constant key twice (`{"k" => a, "k" => b}`) is a compile error at
  the second, `` key `"k"` is given twice in this map pattern; match its value
  once `` (§3 *Repeated keys*). Keys equal only at run time both look up the one
  entry, and both patterns must match its value.

### Set Destructuring

Over a set, `[e1, e2, ...]` matches a set that holds exactly those
elements. With a `..` at the end, the set may hold others too, and a name
after the `..` binds them.

```nomi
fn role(s: Set<String>) -> String
    case s
        ["admin", ..rest] -> "admin and ${Set.size(rest)} more"
        ["user", ..]      -> "user"
        []                -> "nobody"
        _                 -> "other"
    end
end

fn main()
    dbg role(["admin", "dev"]) // "admin and 1 more"
end
```

- **An element is a constant to look up, never a place to bind:** a number,
  string or codepoint literal, the value the set must contain. `[_, ..]` is
  the error ``an element of a set pattern is a constant the set contains, not
  a place to bind: `_` names no value; `..` matches the other elements``, and
  `[role, ..]` is the same error, which says `role` would bind a name and
  that a guard tests a value in scope: `[..] when Set.contains(s, role)`.
  Another pattern there, a variant or a tuple, is the same error, which says
  to write a number, string or codepoint.
- The `..` comes last: `[..rest, 1]` over a set is ``set pattern `..` must
  be the last element``.
- A constant element is named once: `["a", "a", ..]` is ``element `"a"` is
  given twice in this set pattern; test it once``.
- Each element is checked against the element type (`set pattern element type
  mismatch: expected String, got Int`).
- A set pattern never covers every set. A `case` needs a `_` arm, and a binding
  takes an `else`, every path of which leaves. Without one, the binding's help
  is `` a set pattern does not match a set that lacks one of its elements.
  Handle that with `else`, or test one element with `Set.contains` ``.
- A type built on a set names itself before the bracket: `Tags["a", ..others]`.

### String Prefix Patterns

`"prefix" + rest` matches a string that begins with the literal's bytes, and
binds `rest` to the remainder, so the string is `"prefix" + rest`.

```nomi
fn route(path: String) -> String
    case path
        "/users/" + id -> "user ${id}"
        "/health"      -> "ok"
        _              -> "not found"
    end
end

fn main()
    dbg route("/users/7") // "user 7"
end
```

- The left side is a string literal without interpolation, and the right side a
  name. Only a prefix is matched.
- The rest may be empty, and may begin with a combining mark that follows the
  literal's last codepoint: `"e" + rest` against `"e\u{301}"` binds `"\u{301}"`.
- The pattern is refutable. It stands in a `case` arm, a pattern `if`, a binding
  with `else`, or nested in another pattern (`Some("/users/" + id)`). A `case`
  needs a `_` arm.

### Guards

`Pattern when condition -> body` runs the arm when the pattern matches and the
condition is `True`. Otherwise matching continues with the next arm. The
pattern's names are in scope in the condition.

- A guard is a `Bool`: `n when n + 1 -> ...` is the error `` `when` guard must
  be Bool, got Int ``.
- In a guard, `or` is the Boolean operator: `.Circle{r:} when r < 1.0 or r >
  9.0`.
- An arm with a guard never counts toward exhaustiveness.
- A guard narrows what it checks in the arm's body, as an `if` condition
  does its branch (§4 *Narrowing*): in `n when n > 0 -> ...`, `n` is a
  `PositiveInt`.

### Alternatives: `or`

Taught in [Pattern Matching](/pattern-matching/#matching-alternatives-with-or).

`A or B` matches a value that `A` or `B` matches.

```nomi
enum Shape
    Circle {r: Float}
    Square {side: Float}
    Point
end

fn describe(shape: Shape) -> String
    case shape
        .Circle{r:} or .Square{side: r} when r > 10.0 -> "big, ${r} across"
        .Circle{r:} or .Square{side: r}               -> "${r} across"
        .Point                                        -> "a point"
    end
end

fn main()
    dbg describe(Shape.Square{side: 2.0}) // "2.0 across"
end
```

- **Every alternative binds the same names, each at one type.** A name one
  alternative lacks is the error ``` `r` is bound by `.A(r)` but not by `.P`;
  every alternative of an `or` pattern must bind the same names ```. A name
  bound at two types is ``` `v` is an Int in `.N(v)` but a String in `.T(v)`; a
  name an `or` pattern binds has one type in every alternative ```. A discard
  name need not appear in every alternative.
- A name an `or` pattern binds is one binding: it is read, renamed and reported
  unused as one.
- Alternatives are tried left to right, and the first that matches binds the
  names.
- `or` nests inside any pattern that holds subpatterns: a payload
  (`Some(.Equilateral or .Isosceles)`), a tuple element (`(0, x) or (x,
  0)`), a struct field, a list or vector element (`[')' or ']' as c, ..rest]`)
  and a map value. Alternatives may be literals (`1 or 2 or 3`, `"yes" or
  "y"`).
- `or` binds looser than every pattern construct but `as`. Parentheses group:
  `(A or B) or C` is `A or B or C`.
- A guard applies to the whole arm. When it is `False`, matching continues
  with the next arm, not the next alternative.
- Coverage is the union of the alternatives.
- `or` stands wherever a pattern does. Where the pattern must match every
  value, the alternatives must together cover the type: `fn value(.L(n) or
  .R(n): Side)` over a two-variant `Side` is accepted, and `fn f(.Red or
  .Green: Color)` over a three-variant `Color` is ``refutable pattern in
  parameter; bind the parameter and use a `case` in the body``.
- In a binding with `else`, a fallback stands in for one variant's payload, so
  `Ok(.L(n) or .R(n)) = r else Side.L(0)` takes a fallback and `.A(n) or
  .B(n) = e else return 0` must leave.

### Naming the Whole Match: `as`

Taught in [Pattern Matching](/pattern-matching/#naming-the-whole-match-with-as).

`P as name` matches `P` and binds the whole value `P` matched to `name`.

```nomi
fn describe(result: Result<Int, String>) -> String
    case result
        Ok(n) as r when n > 9 -> "big: ${r}"
        Ok(_)                 -> "small"
        Err(msg)              -> msg
    end
end

fn main()
    dbg describe(Ok(12)) // "big: Ok(12)"
end
```

- **Scope.** `as` binds looser than every other pattern construct, `or`
  included. It names everything to its left in its position: `Ok(x) as r` binds
  the whole `Result`, `Ok(T{kind: .A} as t)` the payload, and `"/u/" + id as
  path` the whole string. `A or B as x` names the value whichever alternative
  matched. To name one alternative, parenthesize it: `.A(n) as a or .B(n)` is
  the parse error ``` an `as` names the whole `or` pattern, so it goes after
  the last alternative; to name one alternative, parenthesize it: `(P as a) or
  ...` ```.
- **The name is a binding name**, not a pattern: `P as Big` is the parse error
  ``expected a binding name after `as` in a pattern, got "Big"; `P as name`
  binds the whole matched value to name``. `P as _` and `P as (r)` are the same
  error, with `got "_"` and `got "("`.
- **Its type is the type of the position**: the scrutinee's at the top of an
  arm, the payload's inside a variant, the element's inside a list, the field's
  inside a struct pattern. `P` does not narrow it: `Ok(_) as r` over a
  `Result<Int, String>` binds a `Result<Int, String>`.
- **It never changes what matches.** `P as name` is refutable exactly when `P`
  is.
- **It must add a name.** `y as x` is ``` `y as x` binds the same value twice;
  use one name ```, `P as a as b` is ``` `as a as b` binds the same value
  twice; use one name ```, and `_ as x` is ``` `_ as x` matches every value, as
  `x` does; write `x` ```.
- The name follows the rules of any pattern name: it is in scope in the guard
  and body, may shadow, must be read unless it starts with `_`.
- In a binding with `else`, an `as` around the whole pattern rules out a
  fallback, since there is no value to bind it to: `Some(n) as m = find() else
  0` is the no-single-payload error. An `as` inside the payload is allowed:
  `Ok((w, h) as size) = r else (80, 24)`.

### Exhaustiveness checking

A `case` is exhaustive when its unguarded arms together match every value of
the scrutinee's type. A `case` that is not is a compile error. The check works
component by component:

- `_` and a name match everything.
- An enum is covered when every variant is, and a variant when the arms naming
  it cover its payload. `Some(Ok(_))`, `Some(Err(_))` and `None` cover an
  `Option<Result<T, E>>`.
- A tuple, struct or distinct value is covered when its components are, taken
  together. A struct pattern that only binds fields covers the struct.
- A list or vector is covered by length. `[]` and `[head, ..tail]` cover every
  list; `[]` and `[x]` miss lists of two or more. Elements after the `..` are
  tested against the last elements, so `[]`, `[_]` and `[_, .., _]` cover every
  list, and `[1, .., 3]` covers nothing on its own. `[..all]` covers every
  list or vector.
- A literal, a string prefix, a map pattern and a set pattern never cover their
  type: the compiler does not enumerate `Int`, `Float`, `Decimal`, `String`,
  `Byte`, `Codepoint`, `Map` or `Set`. Where one of those decides the match,
  some arm needs `_` or a name in that position.
- An `or` pattern covers what its alternatives cover together.
- An arm with a `when` guard never counts.
- A subject-less `case` needs a `_` arm (§8 *`case`*).

When the compiler can name a missing value it does: `non-exhaustive case on
Shape: missing Rect`, `non-exhaustive case on (Option<Int>, Option<Int>):
missing (None, None)`, `non-exhaustive case on List<Int>: missing [_, _,
..]`, `non-exhaustive case on Vector<Int>: missing [_, _, ..]`. When only a
value no list of literals can cover is missing, as in a `case (n, n)` whose
only arm is `(0, 0) -> "zero"`, the error is ``non-exhaustive case: add a
`_` arm``.

### Type Narrowing

In a `case` arm whose pattern matches one `embeds` variant, the scrutinee's
name has the embedded type.

```nomi
struct Circle
    radius: Float
end

enum Shape
    embeds Circle
    Point
end

fn area(c: Circle) -> Float
    3.14 * c.radius * c.radius
end

fn describe(value: Shape) -> Float
    case value
        .Circle{radius: _} -> area(value) // value is a Circle here
        .Point             -> 0.0
    end
end

fn main()
    dbg describe(Circle{radius: 1.0}) // 3.14
end
```

- **What narrows is a name.** The scrutinee must be a parameter or a local
  binding written as a plain name (`case value`, not `case w.shape` or `case
  load()`). Every read of the name in the arm's guard and body is the narrowed
  value, lambdas created there included. A name the arm rebinds is a new
  binding.
- **Where.** A `case` arm, and the first branch of `if Pattern = name`, whose
  pattern matches one `embeds` variant: `.Circle{radius:}`,
  `Shape.Circle{radius: r}`, and `.Id(n)` for an embedded distinct `Id`. An
  `as` around the pattern narrows the name as it would without the `as`; the
  `as` name keeps the type of its position.
- An `or` pattern, a struct-shaped or positional variant that is not an
  `embeds`, the `else` branch, a binding with `else` and a piped `case` narrow
  nothing.
- A narrowed value may still be passed where the enum is expected.
- A `value is Pattern` test narrows nothing, in an `if` condition or
  anywhere; `if Pattern = name` is the form that narrows. A condition
  narrows a binding's range, and so its std subtype, by its own rule (§4 *Narrowing*).

### Bindings with `else`

Taught in [Pattern Matching](/pattern-matching/#binding-with-else).

`Pattern = value else ...` binds the pattern's names for the rest of the
enclosing block when the value matches, and runs the `else` when it does
not. The `else` takes one of two forms:

- **Inline.** Anything after `else` on its line is one expression, or
  `break`, `continue` or `return`: `Some(address) = user else "none"`.
- **Below.** An `else` that ends its line takes the lines below, closed by
  `end`: case arms when the first line is `pattern ->` or `pattern when`, and
  an ordinary body otherwise.

The `else` stands on the binding's line, or for a pattern that can fail it
may start the line after a value written over several lines.

```nomi
fn first_word(words: List<String>) -> String
    [first, .._rest] = words else return "(none)"
    first
end

fn email(user: Option<String>) -> String
    Some(address) = user else "none"
    address
end

fn port(text: String) -> Result<Int, String>
    Ok(n) = String.to_int(text) |> Option.to_result("empty") else
        Err(e) -> return Err("bad port: ${e}")
    end

    Ok(n)
end

fn main()
    dbg first_word([]) // "(none)"
    dbg email(None)    // "none"
    dbg port("80")     // Ok(80)
end
```

- The value is evaluated once. The pattern's names are not in scope inside the
  `else`.
- Arms match the value that did not match, of the value's type. The binding's
  pattern and the arms together must be exhaustive by `case`'s rules (*Exhaustiveness
  checking*): ``non-exhaustive `else` on Result: missing Err``.
- Every path through the `else` either leaves (`return`; `break` or
  `continue` in an iteration callback, §9; a trap such as `unreachable
  "reason"` or `todo`; a branch every path of which leaves) or produces a
  fallback. A path that traps binds none of the names.
- **A fallback** is allowed only when the pattern names one variant with a
  payload, `Variant(inner)` or `.Variant{fields:}`, whose inner pattern always
  matches. The fallback has the payload's type (for a struct-shaped variant,
  the anonymous struct of its fields), and the inner pattern binds from it as
  it would from a match: `Ok((w, h)) = r else (80, 24)` binds `w` to 80.
  A fallback of another type is `the fallback stands in for Some's payload of
  type String, got Int`.
- A pattern with no single payload (a bracket or map pattern, a
  nested refutable pattern such as `Ok(Some(x))`, a literal, a tuple with a
  refutable part) requires every path to leave. A path that produces a value is
  the error ``this pattern has no single payload a fallback could stand in for;
  every path through `else` must return, break or continue``.
- A pattern that always matches takes no `else`: `(x, y) = pair else return`
  is `this pattern always matches; remove the else`. A single-variant enum's
  pattern always matches: for an `enum Only` whose one variant is `One Int`,
  `Only.One(p) = only` needs no `else`, and over a struct-shaped variant, `Rec.Fields(r) = rec` binds
  `r` to the anonymous struct of its fields.
- A pattern that can fail requires one (*Where patterns appear*). On `Ok(x)`
  over a `Result` or `Some(x)` over an `Option`, the help points at `?`: write `x
  = ...?`.
- A `?` inside the `else` exits the enclosing function or lambda, and `return`
  inside a lambda's `else` exits the lambda.

## 11. Failure

Taught in [Pattern Matching](/pattern-matching/#optiont-and-resultt-e).

### Option and Result

`Option<T>` is a value that may be absent, and `Result<T, E>` the outcome of an
operation that may fail. Both are prelude enums, and their variants are
written bare (§10 *Variant patterns*):

```nomi
enum Option<T>
    Some T
    None
end

enum Result<T, E>
    Ok T
    Err E
end
```

A program handles one with `case`, a pattern `if`, a binding with `else` (§10
*Bindings with `else`*), `?` (*Propagating with `?`*) or `!` (*Unwrapping with
`!`*). The functions over them are in [std::option](/reference/option/) and
[std::results](/reference/results/).

std::presence's `Presence<T>`, `Present(value)` or `Absent`, says whether an
input gave a value at all, so `Presence<Option<T>>` tells a JSON key left out
(`Absent`) from one set to `null` (`Present(None)`). It is not in the
prelude, and its functions are in [std::presence](/reference/presence/).
`Absent` is no failure, so `?` and `!` do not take a `Presence`: `p?` is the
error `` `?` requires a Result or Option, got Presence<Int> ``, and where
absence is a failure the program writes `Presence.to_option(p)?`.

### Propagating with `?`

Taught in [Pattern Matching](/pattern-matching/#passing-failures-up).

`expr?` unwraps a `Result` or an `Option`. On `Ok(v)` or `Some(v)` its value is
`v`. On `Err(e)` or `None` the nearest function boundary returns `Err(e)` or
`None` at once:

```text
x?  ≡  case x { Ok(v) -> v, Err(e) -> return Err(e) }
x?  ≡  case x { Some(v) -> v, None -> return None }
```

```nomi
fn total(a: String, b: String) -> Option<Int>
    x = String.to_int(a)?
    y = String.to_int(b)?
    Some(x + y)
end

fn main()
    dbg total("2", "3") // Some(5)
    dbg total("2", "x") // None
end
```

- **It binds as tightly as a call.** `f(x)?`, `load()?.name` (the loaded
  value's `name`), `user.address?.city` and `a.b()?.c()?`. After a `!` it
  applies to the payload, so `half(4)!?` is `` `?` requires a Result or Option,
  got Int `` unless the payload is itself a `Result` or `Option`. `not
  ready(x)?` is `not (ready(x)?)`.
- **In a pipe it follows a stage** (§8 *Pipes*). `|> then? fn v -> ...`
  propagates the lambda's result, and `tap?` is an error (§8 *The `tap`
  stage*).
- **A boundary** is the enclosing `fn`, lambda or test body, or a `concurrent`
  block (§16). A `?` in a lambda returns from the lambda, so the lambda's
  result is a `Result` or `Option`. A failing `?` in a test body ends the test
  with a report of the `?` and its value (§17).
- **Some places have no boundary.** A `?` written directly in a `once`
  initializer, a parameter or field default, or a `subtests` row or row name is
  a compile error: `` `?` cannot be used in the initializer of `once port`: a
  `once` has no function to return from `` (§6, §7 *Default Arguments*, §4
  *Default Values*, §17). A lambda inside one is a boundary of its own.

The propagated value must fit the boundary's declared result. Three rules,
each checked at the `?`:

1. **The boundary returns a `Result` or an `Option`.** `` `?` cannot propagate a
   Result out of a function returning Int: the enclosing function must return
   a Result or an Option ``. A function with no declared result returns `Unit`
   and has the same error.
2. **The kinds agree.** A `?` on a `Result` needs a `Result` boundary, and one
   on an `Option` an `Option` boundary: `` `?` on an Option cannot propagate out of a
   function returning Result<Int, String>: the enclosing function must return an
   Option ``. In a lambda this is the lambda's result.
3. **The error type fits the boundary's, or converts to it.** For a `?` on a
   `Result<T, E>` in a function declared `Result<_, F>`, in this order:
   - E is F, or F is an interface E implements, and the `Err(e)` propagates
     as it is, as an interface-typed parameter takes an implementor;
   - else F has an `impl From<E>` (std's `From`, in the prelude), and the
     function returns `Err(F.from(e))`;
   - else the `?` is an error naming both fixes: `` `?` error type
     mismatch: expected ApiError, got DbError (the enclosing function
     returns Result<Int, ApiError>); write `impl From<DbError> for ApiError`
     and the `?` converts the error, or convert it before the `?` with
     Result.map_err ``. It names the impl only when one of the two types is
     not std's, since an impl between two std types is an orphan (§12
     *Foreign Types*). Its help writes the `map_err` stage when one is
     plain: the boundary enum's one variant whose payload is the error
     (`` |> Result.map_err(AppError.Io)? ``), or, for a `String` boundary
     and an error with a `Display` impl, `` |> Result.map_err(&"...: ${&1}")? ``.

A domain error type writes one `From` impl for each error it wraps, and each
`?` converts:

```nomi
enum DbError
    Closed
end

enum ApiError
    Database DbError
    NotFound String
end

impl From<DbError> for ApiError
    fn from(e: DbError) -> ApiError
        ApiError.Database(e)
    end
end

fn count(open: Bool) -> Result<Int, DbError>
    if open then Ok(3) else Err(.Closed)
end

fn total(open: Bool) -> Result<Int, ApiError>
    n = count(open)?
    Ok(n + 1)
end

fn main()
    dbg total(True)  // Ok(4)
    dbg total(False) // Err(Database(Closed))
end
```

- **The impl is on the target.** `From<E>` is implemented for F, not for E:
  `impl From<sql.Error> for ApiError`. E is an input of `From` (§12 *Foreign
  Types*), so a package may also write `impl From<MyError> for String`.
- **One conversion.** The `?` looks up F's impl at E only. It does not
  follow `impl From<IoError> for DbError` and then `impl From<DbError> for
  ApiError`; a `?` on an `IoError` there is the mismatch.
- **No identity impl.** Equal types need none, and std declares none.
- **Only a declared boundary converts.** An inferred boundary (below) has no
  declared error type to convert to.
- **The impl is an ordinary function.** `ApiError.from(e)` and `|>
  ApiError.from()` call it.

`Result.map_err` converts at one site, for a conversion that is that
function's own:

```nomi
fn status(text: String) -> Result<Int, Int>
    String.to_int(text) |> Option.to_result(400)
end

fn port(text: String) -> Result<Int, String>
    n = status(text) |> Result.map_err(&"error ${&1}")?
    Ok(n)
end

fn main()
    dbg port("x") // Err("error 400")
end
```

**Inferred boundaries.** At a boundary with no declared result (a `concurrent`
block, an unannotated lambda) the error type is inferred from the `?` sites in
it. A body whose last expression is `Ok(...)` fixes only the success type.

```nomi
// outcome: Result<Int, String>; String comes from `Task.await(t)?`
outcome =
    concurrent
        t = Task.spawn(fn -> fetch()) // fetch(): Result<Int, String>
        Ok(Task.await(t)?)
    end
```

- `?` sites with different error types in one boundary are the error ``
  inconsistent error types in `?` expressions: String vs Int ``. A `?` on an
  `Option` contributes no error type.
- When no `?` fixes the error type, it stays open, as `Ok(1)`'s does (§5).
- Rules 1 and 2 apply to the result the body produces: `` `?` cannot
  propagate a Result out of a lambda whose result is Int: the lambda must
  produce a Result or an Option ``.

### Unwrapping with `!`

Taught in [Pattern Matching](/pattern-matching/#calls-that-cannot-fail).

`expr!` unwraps a `Result<T, E>` or an `Option<T>`. Its value is the `T` in `Ok`
or `Some`. An `Err` or a `None` traps at the `!`'s line. It states that the
value is there, so a failure is a bug in the program.

```nomi
fn half(n: Int) -> Result<Int, String>
    if n % 2 == 0 then
        Ok(n / 2)
    else
        Err("${n} is odd")
    end
end

fn main()
    ages = {"Ada" => 36}
    dbg Map.get(ages, "Ada")!    // 36
    dbg half(10)!                // 5
    dbg "42" |> String.to_int()! // 42
end
```

- **Any expression takes it**: a call of any form, a field (`user.nickname!`),
  a name (`found!`), a parenthesized expression (`(a |> b())!`). It binds as
  tightly as a call or field access, so `user.address!.city` reads the
  payload's `city`. As a pipe stage it unwraps the stage's result. In a
  capture, `&half(&1)!` is `fn x -> half(x)!`.
- **It is written against the expression**, with no space, on its line. A `!`
  that starts the next line is the C-family negation, an error (§8 *Logical
  operators*). `x!= y` is `x != y`; to compare an unwrapped value, write `x! ==
  y`. `not found!` negates `found!`.
- **The operand must be a `Result` or an `Option`.** Anything else is an error at
  the `!`: `` `!` unwraps a Result or an Option, and `count(1)` is Int ``, whose
  help says to remove the `!`. For a `Bool`, the help says to write an
  `assert`.
- **The trap** quotes the expression as written, on one line, cut after 60
  characters with `...`. An `Err(e)` shows `e` by its `Display` when its type
  implements `Display`, and by its `Debug` otherwise. A `None` says it found
  nothing:

  ```console
  line 7: `half(3)` failed: 3 is odd
  line 4: `Date.new(2024, 13, 1)` failed: invalid value: month out of range: 13
  line 3: `Map.get(counts, "z")` found nothing
  ```

  The line is the `!`'s, even when the failure comes from inside the standard
  library (*Where a trap is reported*). On success a `!` costs the test of the
  variant.
- `!` is for a value the program knows is there: constant arguments
  (`Date.new(2024, 1, 1)!`), a key taken from the map itself, an index computed
  in range, input already validated. A failure that can happen is an `Option` or
  `Result` to handle, or to pass on with `?`.

### Traps, `Option` and `Result`

An operation that cannot always give the result asked for returns an `Option`
or `Result`, or takes a type that rules the failure out. std returns an
`Option` or `Result` when the outcome depends on data the caller does not
control (`Map.get`, `String.to_int`, I/O). Where the caller can check an
operand first, the parameter's type requires the check: `/` and `%` take a
`NonZeroInt` divisor, so dividing by an `Int` that may be zero does not
compile (§8 *Integer division and remainder*). Writing Nomi's
[Pick the tool by what happens on failure](/writing/) applies the same rule
to code a program writes.

A trap stops the task it happens in, with a message. A program traps
where it asks to:

- a `!` on an `Err` or a `None` (*Unwrapping with `!`*), such as an index it
  computed and unwrapped (`Vector.get(v, i)!`);
- an `unreachable` it reaches, or a `todo` it left in;
- `Task.await` of a task that failed or was cancelled (§16 *Errors and
  cancellation*).

Beyond those, a run stops when it:

- runs out of memory, or makes more than 100,000 nested calls (`stack
  overflow: more than 100000 nested calls`);
- asks `Int.pow` or `<<<` for one number too big for memory. An `Int` has no
  fixed size; the stop is at 2^34 bits, which is 2 GiB for a single number (`Int.pow: exponent 17179869185 is too large: the power would have more than 17179869184 bits`);
- asks for a `Decimal` whose scale is outside -2147483648 to 2147483647, or
  with more digits than a `Decimal` holds: a `*` whose operands' scales add
  past that range, a `/` whose quotient has too many digits, and a
  `Decimal.divide`, `Decimal.round`, `Decimal.round_exact`, `Decimal.sqrt`,
  `Decimal.sqrt_exact`, `Decimal.pow` or `Float.to_decimal` whose scale is
  past it or whose answer needs one (`Decimal.sqrt(2d, 4294967298, .HalfEven)`);
- asks for a `String.repeat` of more copies than memory holds;
- consumes the whole of an iterator that never ends (§13 *Infinite
  iterators*);
- closes a `once` cycle through a function value (§6 *`once` Bindings*);
- passes the deadline of the context in force while `main` waits on its
  tasks (§16 *Errors and cancellation*);
- reads, under `nomi run`, `nomi test` or the REPL, an embedded file whose
  contents changed on disk after the program was compiled (§18 *Embedded
  files*).

Any other program that never finishes runs until something outside stops it.

- **A trap is never caught inside the program.** It ends the task it happens
  in: a program's main task ends the program with status 1, and a spawned
  task's trap is its `Failed` outcome (§16), which a supervisor may restart.

A function named `get` returns an `Option` (`Map.get`, `Vector.get`, `Bytes.get`,
`Iter.get`). A caller that knows the value is there writes `Map.get(m, k)!`. A
map pattern is refutable for the same reason (§10 *Map Destructuring*).

#### Where a trap is reported

A trap prints `line N:` and then its own text: `` line 4: `Int.from_digits(ds, 2)` found nothing ``,
`line 8: unreachable: chunk_by never yields an empty run`.

- `N` is a line of the program's own code: the line of the operation that
  trapped or, when the trap happens inside the standard library, the line of
  the program's call into it, however many std calls deep. `shown =
  Decimal.format(1.5d, locale, fraction: Range.from(4_294_967_298))` on
  line 2, which rounds through a std function of `Decimal.format`'s own,
  prints `line 2: Decimal.round: scale 4294967298 is past a Decimal's,
  which is -2147483648 to 2147483647`.
- A lambda of the program that traps while a std function runs it
  (`Iter.map(xs, fn i -> Vector.get(v, i)!)`) is the program's code, so the trap names the
  lambda's line.
- A trap whose own text names no line (a host function's, such as
  `Decimal.sqrt` with a scale past a `Decimal`'s) gets the line of the
  program's call.
- A `todo` names its own file and line instead (*Unwritten code: `todo`*).
- `nomi run`, `nomi test`, a built executable and the REPL print the same
  line.

### Unwritten code: `todo`

Taught in [Functions and Lambdas](/functions-and-lambdas/#unwritten-code).

`todo` is a placeholder for code not written yet. It is an expression, bare or
with a reason: `todo` or `todo "parse the header"`.

```nomi
fn parse(text: String) -> Int
    todo "parse the header"
end
```

- **Type.** `todo` never produces a value, so it has the type its position
  expects: a function's result, an annotated binding, an argument, a struct
  field, an arm or branch (whose type the others decide), a lambda body, a pipe
  stage, a generic function's result, a condition, or an operand whose type
  its operator decides (`not todo` is `Bool`, `n == todo` has `n`'s type). As a
  statement it is `Unit` and does not exit. An unannotated binding expects
  nothing, so `x = todo` is the error ``cannot infer a type for 'x' from
  `todo`: annotate the binding (`x: T = todo`)``. `_ = todo` is accepted.
- **Reason.** A string literal: `"..."`, `"""..."""` or a raw backtick string.
  An interpolated or tagged reason is the parse error ``a `todo` reason is a
  plain string literal: it cannot interpolate or carry a tag``.
- **Running it.** Reaching a `todo` traps with `todo reached at <file>:<line>`,
  then `: <reason>` when there is one. The file is relative to the working
  directory. As a pipe stage, the piped value is computed first.
- **Unread names.** A function or lambda whose body holds a `todo` has its
  parameters exempt from the never-read error, and so are the names an arm, an
  `else` arm, an `if` condition or a test's setup binding binds when the body
  they scope over holds one.
- **Builds.** `nomi build` refuses a program that holds a `todo`
  ([tooling reference](/tooling/building/#executables)).
- **A name.** `todo` is a contextual keyword (§2 *Keywords*): a bare `todo`
  in the scope of a binding named `todo` reads the binding, and `todo
  "reason"` is the placeholder everywhere.

### Unreachable code: `unreachable`

Taught in [Functions and Lambdas](/functions-and-lambdas/#code-that-cannot-be-reached).

`unreachable "reason"` claims that the program never reaches this point.
Reaching it traps.

```nomi
fn encode_run(run: List<String>) -> String
    case run
        [letter]     -> letter
        [letter, ..] -> "${Iter.count(run)}${letter}"
        []           -> unreachable "chunk_by never yields an empty run"
    end
end

fn main()
    dbg encode_run(["a", "a"]) // "2a"
end
```

- **Type.** As `todo`'s. An unannotated binding of one is ``cannot infer a type
  for 'x' from `unreachable`: annotate the binding (`x: T = unreachable "..."`)``.
- **Reason.** Required, a string literal in the forms a `todo` reason takes. A
  bare `unreachable` is `` `unreachable` needs a reason ``.
- **It exits.** A statement after an `unreachable` statement is the error ``
  unreachable code after `unreachable` ``. It is finished code: a function that
  holds one still reports a parameter it never reads, and `nomi build`
  accepts it.
- **Running it.** Reaching it traps with `line N: unreachable: <reason>` at the
  program's line. It always traps, in every build; the compiler never optimizes
  on it.

### Error types

The `E` of a `Result<T, E>` may be any type: `String`, an enum of the
program's, `Int`. There is no built-in `Error` type or interface. An error type
that implements `Display` renders where a message is needed: `io.print(e)`,
`"${e}"`, and the text a failing `main` prints (§15).

```nomi
enum UserError
    NotFound Int
    InvalidName String
end

impl Display for UserError
    fn to_string(e: UserError) -> String
        case e
            .NotFound(id)      -> "user not found: ${id}"
            .InvalidName(name) -> "invalid name: ${name}"
        end
    end
end

fn main()
    dbg "${UserError.NotFound(3)}" // "user not found: 3"
end
```

## 12. Interfaces, implementations and derive

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/).

### Interface declarations

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#defining-and-implementing-an-interface).

`interface Name`, its items on the lines below and `end`, declares a
contract. Every item is a function.
`self` in an item's signature stands for the implementing type. It is a type,
valid only in an interface's signatures: not a value, not an implicit
parameter, not a qualifier. Every `self` in one signature denotes the same
type for a given impl. At a call, every `self` in the signature is the type
the call is for, in a generic interface as in any other: with `impl
Scale<Int> for Money` and `m: Money`, `Scale.scale(m, 2)` for `fn
scale(value: self, by: N) -> self` is a `Money`, and `N` is `Int`, the
impl's argument (*Generic interfaces*).

```nomi
interface Display
    fn to_string(value: self) -> String
end
```

#### Item kinds

| Item form | Role |
|---|---|
| `fn` signature, no body | Required function: every impl writes it. The next line is another item or the interface's `end`. |
| `fn` with a body | Final default: every implementor gets it, and an impl may not write it. |
| `open fn` with a body | Open default: every implementor gets it, and an impl may write its own. |
| `host fn`, no body | Host-backed default, supplied by the runtime; final. |
| `open host fn`, no body | Host-backed default; an impl may override it. |

- Interface items carry no `pub`. A function's visibility is the interface's.
- An interface declares no fields: `field tag: Int` is the parse error
  ``interfaces declare functions only; replace `field tag: Int` with a function
  requirement such as `fn tag(value: self) -> Int` `` (*Requiring a Value*).
- A default is reachable interface-qualified (`Iter.known_count(xs)`) and
  type-qualified on an implementor (`List.known_count(xs)`). Both name one
  function, selected by the type bound to `self`.
- A default an implementor inherits is the implementor's member exactly as a
  function its impl writes is. `Square.describe(sq)` needs no import of the
  interface, as `Square.area(sq)` needs none, wherever the interface and the
  impl are declared. An import of the interface that only such a call would
  use is unused, and so an error. The interface-qualified call
  `Shape.describe(sq)` names the interface and needs it in scope. Named as a
  value through the type, `f = Square.describe`, it is a function of the
  type's values, `(Square) -> String`.
- A default's body calls the interface's other functions bare.
- A Nomi-bodied default may give its trailing parameters default values
  (`fn announce(value: self, loud: Bool = False) -> String`). A required
  or host-backed function may not.
- A call that leaves a defaulted parameter out takes the default of the
  function that runs: the impl's, when its function declares one for that
  parameter, otherwise the interface's. That holds however the call is
  written: on the type, interface-qualified, through a type parameter, or on
  an interface value, where the impl, and with it the default, is chosen
  when the call runs. Which parameters a call may leave out is the
  declaration it names: `Circle.area(c)` needs a default on Circle's
  function, `Shape.area(s)` one on the interface's.

```nomi
interface Shape
    open fn area(_value: self, scale: Int = 1) -> Int
        scale
    end
end

struct Circle
    r: Int
end

impl Shape for Circle
    fn area(c: Circle, scale: Int = c.r * 10) -> Int
        c.r * scale
    end
end

struct Dot end

impl Shape for Dot end

fn main()
    shapes: List<Shape> = [Circle{r: 2}, Dot{}]
    dbg shapes |> Iter.map(&Shape.area(&1)) |> Iter.to_list() // [40, 1]
end
```
- An operation with no `self` that belongs with the interface goes in an
  inherent `impl Iface` block beside it and is called owner-qualified
  (`Iter.from(0)`).

```nomi
interface Formatted
    fn format(value: self, style: String) -> String

    fn format_pretty(value: self) -> String
        format(value, "pretty")
    end

    open fn format_brief(value: self) -> String
        format(value, "brief")
    end
end
```

`Iter`'s `known_count` is an `open` Nomi default returning `None`. `List`
overrides it with a `host fn known_count`, and `Vector` returns
`Some(Vector.length(vector))` (§13 *The Iter protocol*).

#### `where` on an interface function

An interface function may carry its own `where` clause, constraining the
interface's type parameter or a type parameter the function declares. A
required signature writes it after the result; a default writes it between the
result and the body. The bound is checked at each call.

```nomi
interface Ranked<T>
    fn entries(collection: self) -> List<T>

    fn best(collection: self) -> Option<T> where T: Comparable
        Iter.sort(entries(collection), .Descending) |> Iter.first()
    end

    fn prefer<K>(_value: self, lhs: K, rhs: K) -> Bool where K: Comparable
        Comparable.compare(lhs, rhs) != .Less
    end
end
```

A type name that the function neither declares nor the interface has (a bare
`K` in `fn prefer(lhs: K)`) is an error: an interface function does not invent
type parameters. `where` is a keyword, and every function form may have one.

#### `where self` on a default

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#defaults-that-need-another-interface).

A default runs for each implementor, so its body may use only what every
implementor has: the interface's own functions, `Debug`, and `==`. Any other
interface it needs of a `self` value (a function of another interface,
`"${value}"`, an ordering operator, a generic call whose bound names another
interface) is an error at the use: `` `scale` is a `self`, and Scale does not
require Display of its implementors ``, with a hint that shows the clause. The
default states what it needs with `where self: Display`:

```nomi
interface Scale
    fn unit(scale: self) -> String

    fn format(scale: self) -> String where self: Display
        "${scale} ${unit(scale)}"
    end
end
```

- In the body, a `self` value may enter a position typed with a bound
  interface, and a bound such as `where self: Add<self, self>` gives it the
  operator. Another value of the interface's own type (`other: Scale`) does
  not get the bound. A `self` passed to a generic function instantiates it at
  the implementor.
- An implementor that implements every bound gets the default. One that lacks
  a bound writes the function itself, even when the default is final. An impl
  that does neither is the error `` `Secret` does not implement Display, which
  `Scale.format`'s default requires (`where self: Display`) ``, with the hint
  ``implement Display for Secret, or write `fn format` in this impl``. So
  every implementor has the function, and a call checks no `self` bound.
- On a required function it is the error ``interface function 'unit': `where
  self` is only allowed on a default function, and 'unit' is a required
  function``. Outside an interface it is ``function 'f': `where self` is only
  allowed on an interface's default function``.
- A default's signature may name `self` anywhere: in its result (`fn grown(x:
  self) -> self`), in a composite (`-> (self, self)`, `List<self>`), or with
  no `self` parameter at all (`fn zero() -> self`). An interface-qualified call
  of a function with no `self` parameter inside the interface's own default
  runs the implementor's function.

#### Defaults over distinct types

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#defaults-that-need-another-interface).

In a default bounded by `where self: Distinct<T>` (§4 *Distinct Types*), the
pattern `self(p)` takes a `self` value apart, binding what it wraps to `p`, and
`self(x)` builds a `self` from a `T`. The default is specialized to each
implementor, so `self(p)` is that implementor's own destructuring and `self(x)`
its constructor.

```nomi
type Celsius Decimal

interface Temperature
    fn unit(t: self) -> String

    fn label(self(n) as t) -> String where self: Distinct<Decimal>
        "${Decimal.round(n, 1, .HalfUp)} ${Temperature.unit(t)}"
    end

    fn scale(self(n), by: Decimal) -> self where self: Distinct<Decimal>
        self(n * by)
    end
end

impl Temperature for Celsius
    fn unit(_t: Celsius) -> String
        "C"
    end
end

fn main()
    dbg Temperature.label(Celsius(21.456d)) // "21.5 C"
    dbg Celsius.scale(Celsius(10d), 2d)     // Celsius(20d)
end
```

- A parameter written `self(p)` has type `self` and needs no annotation.
- `self(p)` is also a pattern in a `case` arm and a binding (`self(m) = other`)
  for another `self` value.
- Named as a value, `self` is the constructor (`ns |> Iter.map(self)`).
- `T` may be a type parameter of the interface: `interface Wrapper<T>` with `fn
  unwrap(self(x)) -> T where self: Distinct<T>` serves `impl Wrapper<Int> for
  UserId` and `impl Wrapper<String> for Name`.
- A free function bounded by `where D: Distinct<T>` cannot take `D` apart or
  build one.
- An implementor that is not a distinct type over `T` writes the function. The
  error at its impl is `` `Count` does not implement Distinct<Decimal>, which
  `Temperature.label`'s default requires (`where self: Distinct<Decimal>`) ``,
  with the hint `` `Count` wraps Int, so it is not a Distinct<Decimal>; write
  `fn label` in this impl ``.
- An opaque type implemented outside its own file (§4 *Opaque distinct types*)
  satisfies `Distinct<T>` but does not get a default that takes it apart or
  builds it: `` `Meters` is opaque here, so it can't use `Scaled.scale`'s
  default, which builds and takes apart `self`; write `fn scale` in this impl
  ``. Writing the function is then allowed even when the default is final.
- `self(...)` in a default without the bound is `` `self(...)` needs `where
  self: Distinct<T>` on this function ``. Outside an interface default it is ``
  `self(...)` only appears in an interface's default function ``. Against a
  value of the interface's own type it is `` `self(...)` takes apart a value of
  type `self` ``.

### Requiring a Value

An interface declares functions only. A value every implementor must supply is
a function requirement, read with a qualified call:

```nomi
interface Tagged
    fn tag(value: self) -> Int
end

struct Event
    tag:  Int
    name: String
end

impl Tagged for Event
    fn tag(value: Event) -> Int
        value.tag
    end
end

fn tag_of(x: Tagged) -> Int
    Tagged.tag(x)
end

fn main()
    dbg tag_of(Event{tag: 7, name: "start"}) // 7
end
```

A value of interface type or of type-parameter type has no fields, so `x.tag`
on one is an error (§14 *Member access is fail-loud*).

### Implementations

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#defining-and-implementing-an-interface).

`impl Iface for Type`, its functions below and `end`, implements one
interface for one type. Its body holds that interface's functions as plain
`fn` or `host fn` items, with the implementing type written where the
interface writes `self`. An impl with nothing to write, for a marker or a
default-only interface, is one line: `impl Marker for P end`. `impl Type`
with its functions below is an inherent impl: functions owned by the type,
called `Type.f(...)` (§8 *Calls and qualification*).

```nomi
interface Formatted
    fn format(value: self, style: String) -> String
end

struct User
    name: String
end

impl Formatted for User
    fn format(value: User, style: String) -> String
        "User: ${value.name} (${style})"
    end
end

fn main()
    user = User{name: "Alice"}
    dbg User.format(user, "pretty")     // "User: Alice (pretty)"
    dbg Formatted.format(user, "brief") // "User: Alice (brief)"
end
```

- Impls, derives and the type are sibling top-level declarations. An impl block
  holds only its interface's functions: no inherent `fn`, no nested `impl`, no
  `derive`.
- Implementation functions do not write `self`. Inside the block, sibling
  functions are called bare; outside, a call is qualified.
- A generic receiver names its type parameters: `impl Formatted for Box<T>`,
  never `impl<T> Formatted for Box<T>`. `impl Formatted for Box` for a generic
  `Box` is `` `Box` expects 1 type argument, got 0 ``, whose hint is the header
  to write.
- Constraints on the type's declaration apply to its impls. An impl's `where`
  only adds constraints: `impl Iter for Range<T> where T: Discrete`. They are
  all a use of the impl asks of the type arguments: with `impl Display for
  Measure<P> where P: Scale`, a `Measure<Feet>` is Display when `Feet`
  implements `Scale`, whether or not `Feet` is Display. The same holds for a
  written `Equatable` or `Hashable`: a `Measure<Op>` has equality when `Op`
  implements `Scale`, though `Op` holds a function (§5 *Equality*). Every
  use of the impl asks them, alone or with the value inside a list, an
  `Option`, a tuple or a record: `==`, `!=`, `in`, an ordering, a `Set`
  element or `Map` key, an interpolation, `dbg`, a bound, a parameter
  typed with the interface and a typed literal's slot. With a
  `Measure<Bare>` whose `Bare` does not implement `Scale`, `[m] == [m]` is
  the error ``no impl of `Scale` for `Bare` ``. The error says what the
  program wrote that required it and which impl asked it: passed to `fn
  shown(x: Display)`, `m` is ``no impl of `Scale` for `Bare` (required by
  parameter `x` of `shown`)``, with the note `` `Measure<P>`'s `Display`
  impl requires `P: Scale` ``; in an `Html` template's slot it is ``(required by
  this `Html` slot)``, and interpolated, ``(required to interpolate it)``.
  A value whose part lacks the
  interface a parameter or slot takes is that position's error, which
  names the whole value: an `Option<Bool>` in an `Html` element's content
  is ``element content does not show an `Option<Bool>` ``.
- An interface whose functions all have defaults, or that declares none, is
  implemented by the one-line `impl Iface for Type end`.
- One impl block per interface, type and type arguments, program-wide, whatever
  functions each block writes. A second block is an error at its header, two
  empty `impl Marked for P end` blocks included (*Several instantiations for
  one type*), and so is an impl beside a `derive` of the same interface
  (*Derive*). Two blocks that differ only in their `where` clauses are a
  duplicate. One block that writes a function twice is `` function 'show' is
  declared twice in `impl Show for P` ``.
- A file holding an impl does not itself implement anything. A stateless API
  that must take part in an interface is a zero-sized type with an impl.
- Nomi has no extension methods. A file cannot add type-qualified members to an
  imported type; it writes file-level functions beside it.
- The functions of an impl have the interface's visibility. Implementing a
  public interface lets every caller dispatch every function of it, defaults
  included, on the type. To use an interface's defaults only inside a file,
  implement it on a private distinct type that wraps the public one.

`derive Iface for Type` asks the compiler to write the impl (*Derive*). It may
take options after `with` and constraints after `where`, `with` first:

```nomi
derive Display for Box<T> where T: Display

derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}
```

### Matching an impl to its interface

An implementation function matches the interface function's signature after
the header's type arguments replace the interface's type parameters and the
receiver replaces `self`.

- **Parameter names match,** since a call may name its arguments. A name at a
  position typed exactly `self` is free. `fn format(report: Report, s: String)`
  for `format(value: self, style: String)` is the error `impl function
  'format': parameter name 's' does not match interface declaration 'style'`.
  A parameter the body does not read is named in the discard form:
  `_style` matches `style`, and a call still names the argument `style`.
- **A `self` position takes the receiver.** `fn greet(p: Person)` in `impl
  Greeter for Peon` is `impl function 'greet': parameter 1 has type Person, but
  it stands for `self`, which this block implements for Peon`.
- **Types match,** a result naming `self` included (`fn make(n: Int) -> self`
  implemented for `Day` returns `Day`). A function the interface declares with
  no result returns `Unit`. A mismatch names the function to write: `impl
  function 'add': parameter 2 has type Day, but interface 'Add' declares Days`,
  with the help ``interface 'Add<Days, Day>' requires `fn add(lhs: Day, rhs:
  Days) -> Day` ``.
- Each header type argument names a type: `impl Add<Dbys, Day> for Day` is
  `unknown type "Dbys"`.
- The impl's own type parameters are fixed inside it: where the interface
  requires `T`, the function takes or returns `T`, never a concrete type or
  another parameter.
- A function-level type parameter may be renamed, but each of the interface
  function's corresponds to exactly one of the implementation's. The
  implementation may not fix one to a concrete type, merge two, or replace one
  with an impl parameter.
- An implementation function's `where` may only repeat bounds a call through
  the interface already guarantees: the interface function's own bounds and,
  for an impl parameter, the bounds on the impl block and the receiver's
  declaration. A bound on an impl parameter belongs on the impl block.

### Function Name Collisions

When a type implements two interfaces that declare the same function name, a
type-qualified call is ambiguous and must be interface-qualified:

```nomi
interface Speaker
    fn speak(value: self) -> String
end

interface Performer
    fn speak(value: self) -> String
end

struct Dog
    name: String
end

impl Speaker for Dog
    fn speak(value: Dog) -> String
        "${value.name}: woof"
    end
end

impl Performer for Dog
    fn speak(value: Dog) -> String
        "${value.name} takes a bow"
    end
end

fn main()
    rex = Dog{name: "Rex"}
    dbg Speaker.speak(rex)   // "Rex: woof"
    dbg Performer.speak(rex) // "Rex takes a bow"
end
```

- `Dog.speak(rex)` is the error `type 'Dog' impl 'Performer' and 'Speaker',
  which each declare 'speak' — type-qualified 'Dog.speak(...)' is ambiguous;
  qualify by interface, e.g. 'Performer.speak(...)'`. This holds wherever the
  impls are, a third file included, and for defaults the type inherits as for
  functions its impls write: two interfaces that each give `Dog` a default
  `speak` make `Dog.speak(rex)` ambiguous, whether or not the calling file
  imports either. Two interfaces of one name declared by two files are two
  interfaces, and the error names each through its file: `type 'User' impl
  'a.Show' and 'b.Show', which each declare 'show'`, qualified by interface as
  `a.Show.show(u)`.
- A type's own function may not share a name with a function its interface
  impls write, in any file or order, since Nomi does not overload. Beside `impl
  Multiply<Int, Box> for Box`, an inherent `fn multiply` is the error ``
  `Box.multiply` is already `Multiply<Int, Box>`'s `multiply` for `Box`, and
  Nomi does not overload, so a call `Box.multiply(...)` would name both; give
  this function another name `` at the inherent function's name. Every type
  implements `Debug`, so an inherent `fn inspect` is this error for every
  type, with an `impl Debug` or none: `` `Point.inspect` is already
  `Debug`'s `inspect` for `Point` ``.
- The same holds for a default the type gets from an interface whose impl
  does not write it. Beside `impl Ord2 for Odd`, where `Ord2` declares `open fn
  least`, an inherent `fn least` is the error `` `Odd.least` shadows the
  default `least` that `Ord2` gives `Odd`, and Nomi does not overload, so a
  call `Odd.least(...)` would name both; to override the default, write `fn
  least` in `impl Ord2 for Odd` instead ``. For a final default the error ends
  `` `Ord2`'s `least` is final and cannot be overridden, so give this function
  another name ``, and for a derived impl it says to rename the function or
  replace the derive with a written impl.
- `Display` and `Debug` name their functions `to_string` and `inspect`, so a
  type implements both without a collision.

### No interface inheritance

An interface does not extend another, and requires nothing of its implementors
but its own functions. A function that needs two contracts names both in its
bound. A `typealias` may name the conjunction:

```nomi
typealias SortableValue Sortable and Comparable

fn sort<T>(list: List<T>) -> List<T> where T: SortableValue
    list
end
```

The alias names the interface conjunction `Sortable and Comparable`, which is
a bound in a `where` clause and a value type anywhere else (*Interface
conjunctions*).

A collection of values of several types is an enum with `embeds` variants
when the set is closed (§4), and an interface-typed collection when callers
supply their own types (*Interface values*).

### Bounds and dispatch in generic code

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#interface-bounded-generics).

`where T: Iface` requires every type argument for `T` to implement `Iface`.
For `Equatable` and `Hashable` that means having equality: a declared type
with no impl of its own meets either bound when `==` accepts it (§5
*Equality bounds*). Several
bounds join with `and`: `where T: Showable and Tagged`. There is no `or`: a
`T` bounded by one interface or another could call neither's functions, so
`where T: Priced or Costed` is ``` `where` bounds combine with `and`: a type parameter bounded by one interface or another could call neither's functions; declare an interface both implement, or bound on one ```
at the `or`. A generic header introduces names only; constraints go in the `where` clause,
after a function's result or after a type declaration's parameter list. Every
type parameter is in scope in the whole header and the `where` clause.

```nomi
fn step_by<T, S>(r: Range<T>, by: S) -> Iter<T> where T: Comparable and Steppable<S>
    StepByRange{range: r, by:}
end

opaque struct StepByRange<T, S> where T: Comparable
    range: Range<T>
    by:    S
end
```

**What a body may use.** A generic body runs for every type argument its
callers pass, so an interface it needs of a `T` value is one of `T`'s bounds,
from the function's `where`, its impl block's, or its type's declaration. That
covers an interface-qualified call or pipe, `"${value}"`, an ordering or
arithmetic operator, and a call of a generic function that bounds it.

A `T` value with `where T: Iface` also enters any position of type `Iface`:
a parameter, an annotated binding, a list element, a result or a struct
field. It is held there as an interface value of the type argument the
caller passed (*Interface values*). For a generic interface the bound's type
arguments must be the position's: `where T: Conv<String>` does not make a
`T` a `Conv<Int>`.

```nomi
fn take(x: Display) -> String
    "${x}"
end

fn wrap<T>(v: T) -> String where T: Display
    take(v)
end
```

Without the bound it is the position's ordinary mismatch, with a hint
showing the signature with the bound added: `take(v)` in `fn wrap<T>(v: T)
-> String` is `argument 1: expected Display, got T`, with the help ``require
it in the signature: `fn wrap<T>(v: T) -> String where T: Display` ``.

- Without the bound the use is an error: `` `x` is a `T`, and `f` does not
  require Display of `T` ``, with a hint showing the signature with `where T:
  Display`. A value that holds `T` is reported as `` `xs` holds values of `T`,
  and `f` does not require Display of `T` ``.
- On an interface impl the hint puts the bound on the block (`impl Display for
  Box<T> where T: Display`). On an inherent impl it puts it on the function.
- `Debug` needs no bound, since every type implements it, and neither does
  `==`.

**At the call.** A type argument that does not satisfy a bound is an error:
`sort([{x: 1}])` is ``no impl of `Comparable` for `{x: Int}`; anonymous
struct types cannot implement `Comparable` ``. The help offers what the program
can write:

- for a type the program declares, `derive` when the interface is derivable,
  and an impl block spelled with its required functions;
- where the program declares neither the type nor the interface, a note that
  the orphan rule forbids the impl;
- for an interface at most four types implement, their names: with `fn
  has<M>(text: String, m: M) -> Bool where M: Matcher`, `has("a1b", 1)` is
  ``Int does not implement Matcher (required by `where M: Matcher`)`` with the
  help ``only std can implement `Matcher` for `Int`; `Codepoint`, `Regex` and
  `String` implement it``;
- otherwise, to wrap the type: ``only std can implement `Comparable` for
  `Ordering`; wrap it in a type of your own that implements `Comparable` ``.

**Tuples, anonymous structs and functions.** No impl can be written for a
tuple, an anonymous struct or a function type, so none of them implements
an interface the program declares. What each implements of std's:

- A tuple implements `Equatable`, `Hashable`, `Comparable` and `Display`
  when every element does, and `Debug`.
- An anonymous struct implements `Struct` and `Debug`, and `Equatable`,
  `Hashable` and `Display` when every field does. It implements std::parse's
  `ParseFields`, which reads named texts into its fields and writes them
  back, when every field implements `Parse` and `Display`, a field of a
  subtype read with its base's `Parse` and checked (§4 *Subtypes*). It implements
  std::json's `ToJson` and `FromJson` when every field does, as a struct
  that derives them with no options: an object keyed by its field names as
  written, keys it has no field for ignored, and a missing key read as the
  field type's `from_missing` (*Derive*). So
  `Response.json({error: "no todo 99"})` sends `{"error":"no todo 99"}`
  and `Json.decode_as<{title: String, order: Option<Int>}>(text)` reads a
  request body. A tuple implements neither.
- A part implements an interface as any value of its type does. A part of
  an interface type, an `Iter` among them, has only what that interface
  guarantees (*Interface values*), and a part of a type parameter only what
  the parameter's bounds say. So `{v: x}`, with `x: T` and `where T:
  Display`, is not `ParseFields`, and `{v: Iter.from(1)}` is neither
  `ParseFields` nor `ToJson`.
- A function implements only `Debug`.

The same rule decides a bound and an interface-typed position (*Interface
values*): a parameter, a field, a list element, or a map key of type
`Hashable`. So a `where T: Hashable` bound takes `(1, "a")` and
`[(1, 2)]`, and refuses `(1, f)` for a function `f`:
``(Int, (Int) -> Int) does not implement Hashable (required by `where T:
Hashable`)``. One difference: an interface value holds an implementor, so a
position of type `Equatable` or `Hashable` takes a declared type `P` only
when `P` has an impl, where a bound takes any `P` with equality.
`fn f(h: Hashable)` called with `(1, p)` for a `p: P` with no impl is ``
argument 1: expected Hashable, got (Int, P) ``, with help that begins ``
`(Int, P)` is Hashable when each element is, and `P` is not ``. A `where T:
Shape` bound refuses `(1, 2)` with the help ``no impl can be written for a tuple, which implements only Equatable,
Hashable, Comparable and Display, each when every element does; declare a
struct of your own that implements `Shape` ``.

A tuple held as an interface value behaves as the tuple does: its
`Hashable.hash` is the tuple's own hash, two equal tuples are `==`, and
`"${d}"` renders it as the tuple renders.

**Calls through a bound.** In generic code, call an interface function through
the interface (`Formatted.format(item, style)`) or through the bounded type
parameter (`T.format(item, style)`). The checker resolves `T` to the bound that
declares the function, and the call selects the impl for the concrete type
bound to `self`. An unbounded `T`, a function no bound provides, and a function
two bounds both declare are errors; the last must be interface-qualified.
Named arguments use the interface's parameter names.

An interface-qualified call is required in two cases: when the static type is
the interface (*Interface values*), and when two of a parameter's bounds
declare the same function name.

### Generic interfaces

An interface may have type parameters: `interface Container<T>`. In it, `self`
is the whole implementing type with its type arguments.

- An impl writes the interface's type arguments in its header (`impl Add<Days,
  Day> for Day`) or leaves them to be inferred from its functions' signatures
  when the type implements the interface once (`impl Iter for List<T>`, its
  `T` read from `each_while`).
- A type parameter of the interface names a type the implementor determines.
  An interface-typed parameter (`container: Container<T>`) or a bound (`where
  I: Iter<T>`) projects it: `saw_any([10, 20])` for `fn saw_any<T, I>(it: I)
  where I: Iter<T>` solves `I = List<Int>` and `T = Int`. There is no separate
  associated-type syntax.
- A type parameter that only a bound and the result name is projected the same
  way: `plus(Day(10), Days(4))` for `fn plus<L, R, Out>(lhs: L, rhs: R) -> Out
  where L: Add<R, Out>` is a `Day`.

#### Several instantiations for one type

A type may implement one generic interface at several type arguments: `impl
Contains<String> for String`, `impl<C> Contains<Regex<C>> for String` and `impl
Contains<Codepoint> for String` are three impls (the second's `<C>` declares
the parameter its argument names, since the receiver names none).

```nomi
interface Holds<T>
    fn holds(b: self, x: T) -> Bool
end

struct Box
    n: Int
    s: String
end

impl Holds<Int> for Box
    fn holds(b: Box, x: Int) -> Bool
        b.n == x
    end
end

impl Holds<String> for Box
    fn holds(b: Box, x: String) -> Bool
        b.s == x
    end
end

fn held_by<C, T>(c: C, x: T) -> Bool where C: Holds<T>
    C.holds(c, x)
end

fn main()
    b = Box{n: 1, s: "a"}
    dbg Box.holds(b, 1)     // True: Holds<Int>
    dbg Holds.holds(b, "b") // False: Holds<String>
    dbg held_by(b, "a")     // True: the bound at String
end
```

- **Each impl writes its type arguments.** An impl that leaves them to be
  inferred beside another impl of the interface for the type is `` `Box`
  implements `Holds` more than once, so each impl writes its type arguments in
  its header ``, whose help is the header (`` write `impl Holds<Int> for Box`
  ``).
- **No two overlap.** Either every impl names distinct concrete type
  arguments, or there is one impl, generic in them. `impl Holds<T> for Wrap<T>`
  beside `impl Holds<Int> for Wrap<T>` is `` overlapping impls: `Holds<T>` for
  `Wrap` is generic in its type arguments, so it covers `Holds<Int>`, which
  `Wrap` also implements ``. Two impls at the same arguments are `` duplicate
  impl: `Holds<Int>` is implemented for `Box` by modules ...; a type
  implements an interface once at each set of type arguments ``, which names
  the modules that declare them, or says `twice in this module` when one
  module declares both, and points at the other block.
- The orphan rule applies to each impl (*Foreign Types*).
- **A call takes the impl its arguments' types choose,** statically, in every
  call form: type- or interface-qualified, a pipe stage, a partial capture, an
  operator, and a generic call whose bound the call solves. An argument whose
  type its parameter decides (`.Variant`, an open `&1`) does not choose.
- **An argument that fits several impls takes the most specific.** A type and
  the enum that embeds it are two instantiations: with `impl From<Circle> for
  F` and `impl From<Shape> for F`, a `Shape` takes the second, and a
  `Circle`, which converts to a `Shape`, takes the first, whose parameter
  takes only what the other's does. A `?` that converts its error calls the
  impl `F.from(e)` would.
- A type argument only the result names is chosen by the expected type, as a
  generic call's result-only type parameter is (§5): with `impl Make<Int> for
  Factory` and `impl Make<String> for Factory`, `n: Int = Factory.make(f)` calls
  the first.
- When nothing chooses, the call is an error naming the candidates: ``
  `Holds.holds` for `Box` has impls at `Color` and `Shade`; the argument's type
  does not choose one: annotate it ``, `` `Make.make` for `Factory` has impls at
  `Int` and `String`; only the result's type tells them apart, and nothing here
  expects one: annotate the result ``, and `` `Holds.holds` for `Box` has
  impls at `Int` and `String`, and none takes `Float` ``. A bound the call
  leaves open is `` `Box` implements `Holds` at `Int` and `String`, and the
  call's arguments do not choose one for this bound: annotate them ``.
- A value held as an instantiation (`h: Holds<Int>`, a `List<Holds<Int>>`)
  takes any type with an impl at those arguments, and a call through it
  dispatches to that impl. A type that implements two instantiations is held
  as either: `a: Holds<Int> = b` and `s: Holds<String> = b` are both accepted.

### Foreign Types

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#one-impl-per-type-and-interface).

An impl may implement an imported interface for a local type, a local
interface for an imported type, or both local, as the orphan rule permits. A
std generic container is an imported type like any other: a package's own
interface may be implemented for it generically (`impl Show for List<T> where
T: Show`), at a partly concrete instance (`impl Show for Map<String, V> where V:
Show`) or at a concrete one (`impl Sized for List<Int>`). A concrete instance
beside a generic impl of the same interface over the same container is a
duplicate.

An impl block is part of a program when its file is, and a file is when some
file in the program imports it. Then the impl applies everywhere in the
program, in files that do not import it too. A file of impls only is brought
in with an import that names nothing from it (`import html_impls`), which
counts as used when the program uses one of its impls (§14 *Unused imports are
errors*). std's impls are always part of the program. Coherence allows at most
one impl per interface, type and type arguments program-wide, so a call never
depends on which file it is in.

**Orphan rule.** An `impl Iface for T` block must have `Iface` or `T` declared
in the current package (§14). A program outside any package is one package, rooted
at its project root ([tooling reference](/tooling/projects/#project-root-discovery)),
so `impl Debug for Bool` in a lone script is an orphan impl: `orphan impl:
`Debug` (declared in package "std") for `Bool` (declared in package "std") cannot
be implemented in package "..." (function `inspect`): the interface, the
implementing type, or a type heading a type argument the interface's functions
take must be declared in the implementing package`.

**A local type argument.** An impl of a foreign interface for a foreign
receiver is also allowed when a type the package declares heads one of the
interface's type arguments, and that argument is an input: the interface names
its type parameter in a parameter type of one of its functions (`Rhs` in
`Multiply<Rhs, Out>`, `T` in `Contains<T>`). One named only in results (`Out`)
is not.

```nomi
struct Rate
    per_unit: Decimal
end

impl Multiply<Rate, Decimal> for Decimal
    fn multiply(lhs: Decimal, rhs: Rate) -> Decimal
        lhs * rhs.per_unit
    end
end

fn main()
    rate = Rate{per_unit: 4.25d}
    dbg 12.5d * rate // 53.125d
    dbg 2d * 3d      // 6d: std's impl
end
```

The impl must also meet three conditions (Rust's RFC 2451, narrowed to
inputs). The orphan error names the one it breaks:

- **The local type heads the argument.** `Rate` and `Rate<Int>` count;
  `List<Rate>` does not, since std keeps the freedom to implement an interface
  generically over its own container there: `` `Rate` is declared in this
  package but does not head the type argument `List<Rate>` ``.
- **No bare type parameter comes first.** Neither the receiver nor a type
  argument before the local one is a bare type parameter: `impl<T> Multiply<Rate,
  T> for T` is ``its receiver is the bare type parameter `T` ``.
- **Not generic in that position.** `impl Contains<Tagged<T>> for List<T>` is
  ``the impl is generic in `Contains`'s type parameter `T` (`Tagged<T>`)``.

A local type in a result-only position is an orphan impl, since it would compete
for calls that exist: `` `Rate` stands in `Multiply`'s type parameter `Out`,
which only results name ``. std's `Literal` admits no impl this way (``a typed
literal's handler is chosen by its type alone``, §18). Only the package that
declares `Rate` can write an impl naming it, which keeps coherence.

Two libraries may each add an instantiation to one std interface and receiver
(`Multiply<Rate, Decimal>`, `Multiply<Shares, Decimal>`). Both stand beside
std's `Multiply<Decimal, Decimal>`, since they name distinct type arguments,
and a call takes the impl its arguments choose. An impl generic in that type
argument overlaps them. When the generic impl is std's, the error is at the
program's impl: `impl Contains<Rate> for List<Rate>` is ``overlapping impls:
std's `Contains<T>` for `List` is generic in its type arguments, so it covers
`Contains<Rate>`, which this impl implements``.

### Interface values

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#interfaces-as-types).

A value of interface type holds a value of any implementor. An interface
requires nothing of its implementors but its own functions, so the interfaces
a `Scale` value can be used at are `Scale`, `Debug`, `Equatable` and
`Hashable`.

- A value whose type does not implement the interface is the position's
  mismatch, `` argument 2: expected Matcher, got Int ``, with the help a
  bound gives the same type: `derive` or an impl block for a type the
  program declares, and for a std interface and type, ``only std can
  implement `Matcher` for `Int`; `Codepoint`, `Regex` and `String`
  implement it``.
- Any other use (`Display.to_string(other)`, `"${other}"`, `a < b`,
  `Iter.sort(scales)`, a generic call whose bound names another interface) is
  an error at the use: `` `other` is a `Scale`, and Scale does not require
  Display of its implementors ``, whose hint writes a type parameter bounded by
  both. For `Comparable` it adds that values of different types have no order:
  `` `a` is a `Scale`, and Scale does not require Comparable of its
  implementors: values of different types have no order ``.
- `==` and `!=` on two interface values, and a `Set` element or `Map` key of
  interface type, compare the held values. Values held as different concrete
  types are unequal: a `Point` and a `Spot` with the same fields, an `Int` and
  a `String`, two empty lists of different element types, a value of an enum
  with an `embeds` variant and a value of the struct it embeds. Values of one
  concrete type compare as `==` on that type does (§5 *Equality and Ordering
  Semantics*), and hash by its hand-written `impl Hashable` when it has one.
  The same holds for values held as `Display` or `Debug`, and for `==` and
  `!=` between an interface value and a value of a concrete type, on either
  side: `t == (1, "a")` for a `t: Equatable` compares as if the tuple were
  held as an `Equatable` too.
- So an interface value satisfies an `Equatable` or `Hashable` bound with that
  meaning (`List.contains(scales, s)`, `Iter.to_set`, `derive Equatable` over a
  `Scale` field). An `Iter<T>` value is the exception: it is a function and has
  no equality.
- A call through the interface needs only the receiver to be the implementor.
  A function whose result is `self`, that takes a second `self` or a value
  holding `self`, cannot be called on an interface value, or on a conjunction
  that has the interface as a member: `` `Sized.same` cannot be called on
  `e`, a `Sized`: its result is `self`, the implementor's own type, and a
  `Sized` value does not say which implementor it holds ``. A function with
  no `self` parameter called through the interface is an error unless a type
  argument names the implementor (`Sized.make<Pot>(1)`; it comes before the
  function's own, as in `Conv.conv<Money, Int>(5)`), the expected type
  does (`p: Pot = Sized.make(1)`), or the call is in one of the interface's
  defaults: `` `Sized.make` has no `self`
  parameter, so this call does not say which implementor of Sized runs ``.
  Through the implementing type or a bounded type parameter every such call is
  accepted.
- An ordering is a call of `Comparable.compare`, which takes two `self`
  arguments, so `<`, `<=`, `>` and `>=` with an operand held as `Comparable`,
  or as a conjunction holding it, are an error, whatever the other operand
  is: `` `<` cannot compare `a`, a `Comparable`: its two operands must be one
  implementing type, and `Comparable` values may hold different ones ``,
  whose hint writes a type parameter instead: `fn less<T>(a: T, b: T) -> Bool
  where T: Comparable`. `==` and `!=` are no such use, since every interface
  value has equality (above).
- An interface type meets a bound `T: I` when a generic body could call
  every function of `I` on its values: the language defines `I` across
  implementors, as it does `Debug`, and `Equatable` and `Hashable` by
  comparing and hashing the held values (above), or the type holds `I` and
  no function of `I` needs the implementor's own type by the rule above.
  `Display` is met by a `Display` value. `Comparable` is met by no interface
  type, since `compare` takes two `self` arguments: with `fn lt<T>(a: T, b:
  T) -> Bool where T: Comparable`, `lt(x, y)` over two `Comparable` values,
  `[x] < [y]` (List's ordering is bounded by its elements') and `Iter.sort`
  over a `List<Comparable>` are each an error at the instantiation: ``
  `Comparable` does not meet the bound `Comparable`, since
  `Comparable.compare` cannot be called on a `Comparable` value: its 2
  `self` arguments must be one implementing type, and `Comparable` values
  may hold different ones ``, whose help orders values of one type or by a
  key of one type (`Iter.sort_by`). An interface of the program's with a
  function of two `self` arguments is met by no interface type the same way.
- A generic function that instantiates itself at an ever-growing type (`fn
  grow<T>(x: T)` calling `grow(Box{inner: x})`) is a compile error at that call
  (§4 *Generics*).

### Interface conjunctions

`A and B` is a type: a value of any type that implements both. It is written
wherever a type is, and `typealias Name A and B` names it.

```nomi
interface Priced
    fn price(item: self) -> Int
end

interface Costed
    fn cost(item: self) -> Int
end

struct Coffee
    beans: Int
end

impl Priced for Coffee
    fn price(c: Coffee) -> Int
        c.beans * 3
    end
end

impl Costed for Coffee
    fn cost(c: Coffee) -> Int
        c.beans
    end
end

fn margin(item: Priced and Costed) -> Int
    Priced.price(item) - Costed.cost(item)
end

fn main()
    items: List<Priced and Costed> = [Coffee{beans: 2}, Coffee{beans: 5}]
    dbg items |> Iter.map(margin) |> Iter.to_list() // [4, 10]
    both: Priced and Costed = Coffee{beans: 2}
    one: Priced = both
    dbg Priced.price(one) // 6
end
```

- **Entering.** A value converts into `A and B` when its type implements
  every member. Otherwise it is the position's mismatch, with help naming the
  member it lacks: `type mismatch: expected Costed and Priced, got Mug`, help
  `` Mug does not implement Costed; write an `impl Costed for Mug` block ``.
- **Calls.** A call through any member (`Priced.price(item)`) dispatches to
  the held value's impl. A use that needs an interface outside the members is
  the error *Interface values* gives: `` `item` is a `Costed and Priced`, and
  Costed and Priced does not require Display of its implementors ``.
- **Widening.** `A and B` converts to `A`, and to any conjunction of some of
  its members, at the top and at any position that converts (§5 *Covariant
  conversion*). `A` does not convert to `A and B`: `a Priced value need not
  implement Costed, so it does not convert to Costed and Priced`. Widening
  moves nothing at run time.
- **Identity.** Members are interfaces, in any order, and one written twice
  counts once: `A and B`, `B and A` and `A and B and A` are one type, shown
  with its members sorted by name. A member that is no interface is an error:
  `` `and` joins interfaces or subtypes, and Int is neither ``.
- **Equality.** `==`, hashing and `Debug` work on a conjunction as on any
  interface value, through the held value (*Interface values*), so
  `Equatable and Hashable` values go in a `Set`. A conjunction with an
  `Iter` member has no equality. `Display` renders only when it is a member.
- **Bounds.** In a `where` clause `T: A and B` bounds `T` by each member
  (*Bounds and dispatch in generic code*).

### TryFrom

`TryFrom<E>`, in the prelude beside `From`, makes a value from an `E`
when the `E` allows it:

```nomi
interface TryFrom<E>
    fn try_from(value: E) -> Option<self>
end
```

A `type` with a rule in its chain, its own `when` or a subtype it wraps
(§4 *Validated types*), implements `TryFrom` of its base because of its
rule and its base, as every distinct type satisfies `Distinct<T>`:
`try_from` is `TryFrom`'s function, with a body the compiler supplies,
which checks the base's rules, then the type's own, and answers `Some`
or `None`. That impl cannot be written by hand:
`impl TryFrom<Int> for Percent`, with `type Percent Int when ...`, is
`` `TryFrom<Int>` for Percent is supplied by its rule and cannot be implemented; every `type` with a rule over Int satisfies `TryFrom<Int>` automatically — remove this `impl TryFrom<Int> for Percent` ``.
An impl from any other source type is the program's to write, one per
source type as `From`'s are, and `T.try_from(x)` picks by the argument's
type. A subtype implements no interface of its own (§4 *Subtypes*), so
its `try_from` is its own function and no `TryFrom` impl.

```nomi
type Celsius Float when fn c -> c >= -273.15

type Kelvin Float when fn k -> k >= 0.0

impl TryFrom<Celsius> for Kelvin
    fn try_from(Celsius(c)) -> Option<Kelvin>
        Kelvin.try_from(c + 273.15)
    end
end

enum Weekday
    Monday
    Tuesday
end

impl TryFrom<Int> for Weekday
    fn try_from(value: Int) -> Option<Weekday>
        case value
            1 -> Some(Weekday.Monday)
            2 -> Some(Weekday.Tuesday)
            _ -> None
        end
    end
end

fn load<T>(n: Int) -> Option<T> where T: TryFrom<Int>
    T.try_from(n)
end

fn main()
    dbg Kelvin.try_from(Celsius(20.0)) // Some(Kelvin(293.15))
    dbg Kelvin.try_from(-1.0)          // None
    day: Option<Weekday> = load(2)
    dbg day // Some(Tuesday)
end
```

A `type` with no rule anywhere, `type Plain Int`, has no `TryFrom`:
`load` at a `Plain` is
``Plain does not implement TryFrom<Int> (required by `where T: TryFrom<Int>`)``.
For an opaque type the impl the compiler supplies is construction, so it
counts only in the type's file (§4 *Opaque distinct types*); outside it,
`load` at a `Codepoint` is
``Codepoint does not implement TryFrom<Int> (required by `where T: TryFrom<Int>`)``.

### Display and Debug

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#universal-debug).

`Display` renders a value for users and `Debug` for developers:

```nomi
interface Display
    fn to_string(value: self) -> String
end

interface Debug
    fn inspect(value: self) -> String
end
```

Interpolation (`"${x}"`) and `io.print` call `Display.to_string`. `io.inspect`
and `dbg` call `Debug.inspect`.

**`Display` is opt-in.** A type has it through an impl or `derive Display`.
Rendering a value without it is a compile error: `"${p}"` is ``no impl of
`Display` for `P` (required to interpolate it)``, with the help ``add `derive
Display` to `P` or write an `impl Display for P` block``, and `io.print(p)` is ``P does not implement Display
(required by `where T: Display`)``. A function value has no `Display`: ``no impl
of `Display` for `(Int) -> Int`; a function value has no `Display` ``. A tuple
or anonymous struct has `Display` when each part has it.

**`Debug` is universal.** Every type implements it, with no derive and no
impl, so there is no `where T: Debug` bound to write, and type variables and
interface values inspect. A value of any type is a `Debug` value: `xs:
List<Debug> = [Point{x: 1}, (2, "a")]`. `Debug.inspect(x)` and `T.inspect(x)`
render `x` alike for every type `T`, a type declared inside a function body
included, and `T.inspect` named as a value is a function of `T`'s values
(`Iter.map(points, Point.inspect)`). Precedence, highest first: a hand-written
`impl Debug`, then `derive Debug`, then the rendering the table below gives a
type with neither.

| Construct | `Display` | `Debug` without an impl |
|---|---|---|
| struct, enum, distinct type | an impl or `derive Display` | structural, the same as `derive Debug` |
| `opaque` type | only the owning file can write or derive one | the name only, `<opaque T>` |
| `host type` | an impl | the bare type name, `T` |
| interface value | the held value's | the held value's |
| function or builtin value | none | `<function>` |
| `Task`, `Sender`, `Receiver`, `Arm` | none | `<task>`, `<channel>`, `<arm>` |
| `Context` | none | `<context>` |
| `Type<T>` witness | none | `Type<T>` with the type as its declaration names it, `Type<TraceId>` |
| lazy `Iter` (an adapter or constructor result, before materializing) | none | `<iter>` |

- **Opaque types** get the name-only default, so the floor never shows hidden
  internals. The owning file writes `impl Debug` or `derive Debug` to show
  structure (*Derive*). A failed test's report, its `values:` rows and
  `returned:` block, shows a value without a written `impl Debug` by its
  shape, an opaque one too (§17 *Failure reports*). A type that holds a
  secret writes `impl Debug`, as std's `SessionKey` does with
  `SessionKey(<hidden>)`, and `dbg`, `Debug.inspect` and the report all
  print that text. Secret text, such as a password or a token, belongs in
  std::secret's `Secret`, whose `Debug` prints `Secret(<hidden>)` and which
  has no `Display`.
- **Host types** get the bare type name: `Bool`'s `embeds True` inspects as
  `True`, and `Unit` as `Unit`. A host type whose values only its host functions make
  and read (std's `Regex`) renders by its own `impl Debug` when it declares
  one, at the top level and nested, and otherwise by its name. `Regex`'s
  renders the typed literal that builds it (``Regex`\d+` ``).
- **A lazy `Iter`** inspects as `<iter>`, nested too (`[<iter>]`), and is left
  unconsumed. An `Iter<T>` position holding a source rather than a pipeline (a
  list, a range, a value of a type with its own `impl Iter`) inspects as that
  source: `[1, 2]`.
- Dispatch is keyed by nominal identity (declaring file, name), so two types
  named `Point` each keep their own impl.
- Each type's rendering is in its std module's reference.

A lazy `Iter` has no `Display`: `"${it}"` and `io.print(it)` are
`` `it` is an `Iter<Int>`, and Iter<Int> does not require Display of its implementors ``.

**Self-calls.** An impl that renders, compares or hashes its own arguments
through the function it defines calls itself forever, and is a compile error at
that use:

- In `impl Display for Foo`, `fn to_string(foo: Foo)` may not contain
  `"${foo}"`, `io.print(foo)`, `io.write(foo)`, `Display.to_string(foo)` or
  `Foo.to_string(foo)`: ``"${foo}" calls Display.to_string(foo), the function
  being defined, so it never returns``, with the help `Debug.inspect(foo)
  renders its fields`.
- In `impl Debug for Foo`, `fn inspect(foo: Foo)` may not contain
  `io.inspect(foo)`, `dbg foo`, `Debug.inspect(foo)` or `Foo.inspect(foo)`.
- In `impl Equatable for P`, `fn equal(a: P, b: P)` may not compare `a` and `b`
  with `==` or `!=` or pass them to `Equatable.equal` or `P.equal`: ``
  `a == b` calls P's `equal`, the function being defined, with its own
  arguments, so it calls itself forever ``, with the help ``compare the fields
  instead (`a.field == b.field`), or write `derive Equatable for P` ``.
- In `impl Comparable for P`, `fn compare(a: P, b: P)` may not compare them
  with `<`, `>`, `<=` or `>=` or pass them to `Comparable.compare` or
  `P.compare`. In `impl Hashable for P`, `fn hash(p: P)` may not pass `p` to
  `Hashable.hash` or `P.hash`.
- A plain binding of the argument (`same = foo`) is the same value and the same
  error. Rendering, comparing or hashing a field, or another value of the type,
  is allowed, as is the receiver through the other interface.

### Derive

Taught in [Interfaces and Dispatch](/interfaces-and-dispatch/#deriving-impls).

`derive Iface for Type` asks the compiler to synthesize `Type`'s impl of
`Iface` from its fields or variants. One declaration derives one interface. The
synthesized impl is the one a program would write by hand, and dispatches like
any other.

```nomi
struct Point
    x: Int
    y: Int
end

derive Equatable for Point
derive Hashable for Point

fn main()
    p = Point{x: 1, y: 2}
    dbg p == Point{x: 1, y: 2}                               // True
    dbg Hashable.hash(p) == Hashable.hash(Point{x: 1, y: 2}) // True
end
```

- **Targets.** A `struct`, `enum`, distinct `type` or `host type`.
- **Same file.** A derive sits in the file and scope of the type's
  declaration. A derive in any other file, a test file beside it included,
  is the error `` `derive ... for T` must be written in the file that
  declares `T` ``. A type that tests compare or decode derives what they
  need in its own file.
- **Scope.** The interface name resolves through ordinary scope, as an
  `impl` header's does: bare (`derive ToJson for User`) or through its
  module (`derive json.ToJson for User`). It counts as a use, and a name not
  in scope is an error with the import to add: `` `ToJson` is not in scope —
  import `std::json.ToJson` ``, or `` `json.ToJson` is not in scope — import
  `std::json` ``. Names in `with` options are uses too, spelled as the
  interface is (`json.ToJson.Options{...}`). `Debug` needs no import. Names
  only the synthesized code uses (`Ordering`, `True`, the recursive calls,
  `Ok` in `derive FromJson`) need none, and mean std's whatever the
  file binds.
- **Identity.** A derive names its interface by identity: the name must
  resolve to the std interface of that name in the table below. A file
  that declares its own `interface ToJson` and writes `derive ToJson for
  User` names its own interface, which is the error `` `derive ToJson for
  User`: `ToJson` here is the interface this file declares, and derive
  writes only std's `ToJson` (`std::json`); to derive std's, write `derive
  json.ToJson for User` with `import std::json` ``. Through its module, the
  derive writes std's impl beside the file's own interface, and an impl of
  that interface for the type is a second interface's impl, not a conflict:

```nomi
import std::json

interface ToJson
    fn to_json(value: self) -> String
end

struct User
    name: String
end

impl ToJson for User
    fn to_json(value: User) -> String
        "user ${value.name}"
    end
end

derive json.ToJson for User

fn main()
    u = User{name: "ann"}
    dbg ToJson.to_json(u)      // "user ann"
    dbg json.Json.encode_as(u) // "{\"name\":\"ann\"}"
end
```

A name no derivable interface has is the error `` `derive Assertable for
User`: `Assertable` is not an interface derive can write; derive writes
Equatable, Hashable, Comparable, Debug, Display, ToJson, FromJson,
Variants ``.

- **Fields.** Each field's type must implement the interface, checked at the
  derive as any use of the interface at that type is checked: ``no impl of
  `Display` for `User` (required via `derive Display for Wrapper`, field
  `id`)``, with the help to derive or implement it on `User`. A variant's
  payload is checked the same way, named by its variant. For `Equatable`
  and `Hashable` a field's type implements the interface when it meets the
  bound (§5 *Equality bounds*): a struct `P` with no impl of its own is
  accepted when it has equality. A field whose type has a hand-written impl
  asks only that impl's `where` clause of its type arguments: with `impl
  Equatable for Measure<P> where P: Unit2`, a field `m: Measure<Feet>` meets
  a derived `Equatable` when `Feet` implements `Unit2`, whatever else `Feet`
  holds, and `m: Measure<Bare>` is ``no impl of `Unit2` for `Bare`
  (required via `derive Equatable for Holder`, field `m`)``. When the
  field's type is a std type, which a program cannot derive or implement an
  interface for, the help says so: ``only std can implement `Equatable` for
  `Json.ShapeError`; wrap it in a type of your own that implements
  `Equatable` ``. Every derivable interface is checked, and so is `Debug`
  where a field's type has a written `impl Debug` with a `where` clause. A
  field of a tuple or anonymous struct type is checked part by part, since
  it implements `Equatable`, `Hashable`, `Display` (and, for a tuple,
  `Comparable`) when each part does: `f: (Int, P)` is ``no impl of
  `Hashable` for `P` (required via `derive Hashable for W`, field `f`)``.
  An interface-typed field holds a value of any implementor, and an
  interface requires nothing else of its implementors, so it implements the
  derived interface only when it is that interface or the interface holds
  of every value: `Debug`, and `Equatable` and `Hashable`, which compare and
  hash the held value by its type (§5 *Equality*); no interface field takes
  a derived `Comparable`, since values of two types have no order. A field
  `g: Debug` is ``` `derive Display for H`: field `g` holds values of
  `Debug`, and Debug does not require Display of its implementors ```, and
  `g: Display` is accepted. A function and an `Iter` have no equality, so a
  field that is or holds one cannot take a derived `Equatable`, `Hashable`
  or `Comparable`: ``no impl of `Equatable` for `(Int) -> Int` (required
  via `derive Equatable for W`, field `f`)``, with the help to write an
  impl that leaves the field out. A function has no `Display` either.
- **With a hand-written impl.** A derive beside an impl of the same interface
  for the same type, whichever spelling each names it by (`ToJson`,
  `json.ToJson`), is the error `` `derive Equatable for P` conflicts with a
  hand-written `impl Equatable for P` block — a derived impl is
  synthesized whole, so it cannot coexist with a manual impl; drop one ``,
  reported at the hand-written impl.
- **Options.** Only the per-declaration `with` options. There is no per-field
  exclude or rename, and no user-defined derive.
- **Visibility.** A derived impl's functions have the interface's visibility,
  as a hand-written impl's do.
- Nomi has no decorators: an `@name` line is the parse error `` `@uses` is not
  supported: Nomi has no decorators ``.

#### Derivable interfaces

| Interface | Declared in | Synthesized impl |
|---|---|---|
| `Equatable` | `std::equatable` | Field by field with `==`, stopping at the first difference. Enum values are equal when the variants match and then the payloads. |
| `Hashable` | `std::hashable` | By shape, as a `Map` key of the type hashes (§5 *Hashing and keys*): the type, the variant and each field's or payload's own hash. Equal values hash equal; the Int itself is not specified. |
| `Comparable` | `std::comparable` | Lexicographic in field declaration order. An earlier-declared variant is less than a later one, and ties compare the payloads. |
| `Debug` | `std::debug` | The literal shape: `Point{x: 3, y: 4}`, `Circle(5.0)` (the variant, with no enum prefix). |
| `Display` | `std::display` | `Debug`'s shape with nested strings unquoted: `C{name: bob}` where `Debug` gives `C{name: "bob"}`. |
| `ToJson` | `std::json` | Fields as object keys, each written as its type's `ToJson.to_json_field` answers: `Some(json)` writes the key, `None` leaves it out. The default is `Some(to_json(v))`, so a `None` field is `null`; `Presence<T>`'s leaves an `Absent` field out. A distinct type through its inner value; a bare variant as its name, a payload variant as an object keyed by its name. `ToJson.Options{rename_all: ...}` sets the key case. |
| `FromJson` | `std::json` | The inverse of derived `ToJson`. A missing key reads as its field type's `FromJson.from_missing()`: `Some(v)` is the field's value, as `Option<T>`'s `None` and `Presence<T>`'s `Absent` are. The default answers `None`, and then a defaulted field takes its default and any other is the error `title: expected String, got missing`. A bare variant reads only its name as a string. A payload variant reads only an object with exactly one key, its name, as `Result`'s `{"Ok": value}`: an object with no key, several keys or a key no payload variant has is an error naming its keys, `expected Shape, got object with keys "Circle", "Rect"`. A mismatch names the variant and field. Every shape error names the Nomi type it expected and the JSON kind it got (`Json.kind`): `title: expected String, got null`, `expected Todo, got array`. `FromJson.Options{rename_all: ...}` sets the key case. |
| `Variants` | `std::variants` | Only for an enum without type parameters whose variants carry no data. `variants()` lists every variant in declaration order: `Coin.variants() == [Coin.Penny, Coin.Nickel]`. |

- `derive Display` and `derive Debug` give the same output for a type with no
  `String` inside it. Each variant shape renders as follows (`D` is the derived
  interface applied to the part): a struct as `T{f1: D(v1), ...}` (`T{}`
  when empty); a bare variant as its name; a positional variant as `V(D(p0),
  ...)`, with one part per element of a payload declared as a tuple
  (`Node(Leaf(1), Leaf(2))`) and one part otherwise (`Some((1, 2))`); a
  struct-payload variant as `V{f: D(v), ...}`; an `embeds` variant as the
  embedded value's rendering; a distinct primitive as `T(D(inner))`; a distinct
  tuple as `T(D(a), D(b))`; a distinct map, vector or set in the call form
  (`Kvs({"a" => 1})`, `Path(Vector[1, 2])`, `Tags(Set["a"])`); a zero-sized distinct
  as its name.
- `Variants` on anything else is an error at the derive that names the reason:
  `` `derive Variants for Shape`: variant `Shape.Circle` carries data, and
  `Variants` lists only variants that carry none; remove its data, or write
  `impl Variants for Shape` by hand ``. Generic code calls it through a
  bound: `T.variants()` with `where T: Variants`.
- A `FromJson` target is named by the implementing type
  (`User.from_json(value)`), an explicit type argument
  (`FromJson.from_json<User>(value)`) or an expected type. `Json.encode_as` and
  `Json.decode_as` convert and encode or decode in one call
  ([std::json](/reference/json/)). A field of a subtype is read at its
  base and checked against the subtype's rules, a value that breaks one
  the shape error `port: expected Port, got 0` (§4 *Subtypes*).
- std implements `ToJson` and `FromJson` for `Map<String, V>` alone, since a
  JSON object's keys are strings, and a tuple implements neither (*Tuples,
  anonymous structs and functions*). A derive over a field that holds either,
  directly or inside a `List`, `Vector`, `Set`, `Option` or `Map` value, is an
  error at the field: ``no impl of `ToJson` for `Map<Int, Int>` (required via
  `derive ToJson for W`, field `m`)``.

`derive Equatable`, `derive Hashable`, `derive Debug` and `derive Display`
write an impl with no function. A derived Equatable compares by shape, as
`==` compares a type with no impl (§5 *Equality*), each part through its own
impl. A derived Hashable hashes by shape, as a `Map` key of the type hashes
(§5 *Hashing and keys*), each part through its own impl, so it agrees with
the type's equality: beside a hand-written `impl Equatable`, it hashes every
value of the type alike. Debug and Display render the type by its shape, the
literal that builds it, each part through its own impl. `derive Debug` on a type that is neither opaque nor a host type is
redundant, since the type renders so with no derive, and it is accepted.
`derive Display` is what gives a type Display. Each is an impl all the same: a
hand-written impl of the interface beside it is the conflict above, and `T:
Equatable`, `T: Hashable` and `T: Display` are met by the derive.

#### Generic types

`derive Equatable for Box<T>` implies the bound `T: Equatable`, checked at each
use. The implied bound for `Debug` is always met. The type may be named bare,
`derive FromJson for Box`, and the derive applies it to the declaration's own
parameters: decoding a `Box<Int>` needs `Int: FromJson`, as a hand-written
`impl FromJson for Box<T> where T: FromJson` does.

A field whose type's impl asks more of `T` than the implied bound, as
`m: Measure<T>` does under `impl Equatable for Measure<P> where P: Unit2`,
needs that bound on the derive. `derive Equatable for Hold` is an error:

```console
`derive Equatable for Hold`: field `m` holds values of `T`, and the derive does not require Unit2 of `T`
```

Its help says to write `derive Equatable for Hold<T> where T: Equatable and
Unit2`. A `where` clause on a
derive is checked at each use as the implied bound is: with it, `a == a`
over a `Hold<Bare>` is ``no impl of `Unit2` for `Bare` ``.

#### Distinct types

A derive goes through the inner value: with `type Id Int` and `derive
Equatable for Id`, `Id(m) == Id(n)` exactly when `m == n`.

#### Opaque types

A `pub opaque` type may be derived. `Equatable`, `Hashable` and `Comparable`
show no structure. `derive Debug` replaces the name-only default with
structural output in place of `<opaque Token>`.

#### Host types

A derive on a `host type` asserts the type has one inhabitant.
`Equatable.equal` returns `True`, `Comparable.compare` returns `Equal`,
`Hashable.hash` gives every value one hash, and `Debug` and `Display` render
the bare type name. There are no fields to check. A host type that carries a value (such as
`Instant`) writes its impls with `host fn` items instead.

## 13. Iteration

Taught in [Iteration and Loops](/iteration-and-loops/).

### The Iter protocol

Taught in [Collections](/collections/#iter).

`Iter<T>` in `std::iter` is the one iteration protocol. It is a push iterator:

```nomi
interface Iter<T>
    fn each_while(collection: self, yield: (T) -> Bool) -> Bool

    open fn known_count(_collection: self) -> Option<Int>
        None
    end

    open fn is_infinite(_collection: self) -> Bool
        False
    end
end
```

- `each_while` walks the source and passes each element to `yield`, which
  returns `False` to ask it to stop. `each_while` returns `True` when the source
  ran out and `False` when a consumer stopped it. This is the whole
  early-termination mechanism: `Iter.take(3)` stops an infinite source by
  refusing a fourth element.
- `known_count` is `Some(n)` when the source knows its length without being
  walked, and `None` otherwise. `Iter.count` consults it. `List`, `Vector`,
  `Map`, `Set`, `Option` and bounded ranges whose element type can count steps
  override it.
- `is_infinite` is `True` when the source never ends on its own (*Infinite
  iterators*).
- `each_while` reads a value it never changes, so an iterator can be run
  again. A stream's iterators (`Reader.lines`, `Reader.chunks`, `io.lines`) are
  the exception: they read the stream as they run
  ([std::io](/reference/io/#standard-streams)).
- An `Iter<T>` is an ordinary value: a binding, a parameter, a result, a
  field, a tuple or record part, a payload, a list element and a capture can
  hold one. Holding it runs nothing, and each consumer runs it from its start.
- Any source stands where an `Iter<T>` is expected (a `List`, `Vector`, `Set`,
  `Map`, `String`, `Bytes`, `Option`, range, or a type with an `impl Iter`) and
  keeps its own `known_count` there.

`Map<K, V>` is an `Iter<(K, V)>`, `String` an `Iter<String>` of grapheme
clusters, and `Option<T>` an `Iter<T>` of zero or one element. A range is an
`Iter` when its element type is `Discrete` (§3 *Range literals*). What each std
source yields is in [std::iter](/reference/iter/).

**A collection is an `Iter`; an `Iter` is not a collection.** An `Iter<T>`
where a collection is expected (an annotation, an argument, a field, an
element, a result) is a type error whose help names the function that
collects it: `type mismatch: expected List<Int>, got Iter<Int>`, with ``an
Iter is not a collection; collect it into one with `|> Iter.to_list()` ``. The same holds for
every interface: a value of interface type never stands where an implementor is
expected.

#### Infinite iterators

`is_infinite` returns `True` for `Iter.repeat`, `Iter.from`, `Iter.iterate`
(whose step cannot `break`), `Range.from`, `Range.naturals`, `Range.step_by` of
an unbounded range whose step moves, and `Iter.cycle` of a source that is
infinite or knows a non-zero count. A type of the program returns `False`
unless its `impl Iter` writes `is_infinite`. Each adapter carries the
result when the pipeline is built: `map` over an infinite source is
infinite, and `take` never is. When in doubt the result is `False`.

A terminal that has to reach the end of its source (`to_list`, `count`,
`sort`, `sum`, ...) asks `is_infinite` first and traps on `True`, naming the
terminal and the source, at the program's line:

```console
line 6: `Iter.to_list` of an infinite iterator (`Iter.repeat`) never finishes; limit it with `Iter.take` or `Iter.take_while`
```

A terminal that may stop early (`first`, `find`, `any_match`, ...) does not trap.
An iterator that never ends without saying so (a type that does not write
`is_infinite`, a `take_while` whose predicate never fails) runs until
something stops it. [std::iter](/reference/iter/) lists when each adapter is
infinite and which terminals trap.

When the whole pipeline is written where the terminal is, the checker reports
the trap as a compile error, with the same text, at the terminal, and a note at
the source:

```nomi
fn coins(coin: Int) -> List<Int>
    coin
    |> Iter.repeat()
    |> Iter.to_list() // error: `Iter.to_list` of an infinite iterator (`Iter.repeat`) never finishes; ...
end
```

- The check follows pipes, nested calls (`Iter.to_list(Iter.map(Iter.from(0),
  f))`) and local bindings whose value is such a pipeline, through any number
  of them.
- It reads only what the source spells: a `Range.from`, a `Range.step_by` of
  a `Range.from` or `Range.naturals` by a literal step other than 0,
  `Iter.chunks` with a literal size above 0, and `Iter.cycle` of an infinite
  source or of a non-empty list, vector, set or map literal.
- A callback that holds `break` or `continue`, or one it cannot see into, ends
  the check, as `take` and `take_while` do.
- A parameter, a field, a `once`, a function's result and a program's `impl
  Iter` are not followed; the run-time trap still catches them.

### Laziness and fusion

Every generic iteration algorithm is an owner function of `Iter` over any
`Iter<T>`: `Iter.map`, `Iter.filter`, `Iter.reduce`, `Iter.count`, and the rest
([std::iter](/reference/iter/)). They are owner functions, not interface
functions, and do not dispatch; `known_count` and `is_infinite` are the only
dispatched defaults.

- **Adapters are lazy.** `map`, `filter`, `take`, `flat_map` and the other
  adapters return a new iterator and run nothing. A chain of them fuses into
  one pass over the source, with no intermediate collection.
- **Terminals consume.** `reduce`, `each`, `find`, `count`, `sort` and the
  materializers `to_list`, `to_vector`, `to_set`, `to_map` and `reverse` run
  the pipeline.
- **There are no eager collection adapters.** `List.map` and `Set.filter` do
  not exist; a container from a chain is an explicit materialize step:
  `Iter.map(xs, f) |> Iter.to_list()`. An `Iter<String>` is joined with
  `String.join`.
- **Container-specific operations stay on the container's owner**: the work
  that reads or rebuilds a container's structure, such as `Map.map_values`,
  `Set.union`, `List.concat`, `Vector.get`, `Map.size`, `String.split` and
  `String.reverse`.
- `break` and `continue` in an adapter's callback act through the fused
  pipeline on the consuming terminal.
- `Iter.zip` pulls its right operand one element at a time, since two
  sources cannot both own the loop. It is the only
  adapter that pays a coroutine handoff per element, and it works over sources
  unbounded on either side.

### Custom Iters

A type implements `Iter<T>` by writing `each_while`: walk the structure, pass
each element to `yield`, and stop as soon as `yield` returns `False`. Every
`Iter` owner function then works on it.

```nomi
struct Countdown
    remaining: Int
end

impl Iter for Countdown
    fn each_while(collection: Countdown, yield: (Int) -> Bool) -> Bool
        n = collection.remaining

        case
            n <= 0   -> True
            yield(n) -> each_while(Countdown{remaining: n - 1}, yield)
            _        -> False
        end
    end
end

fn main()
    dbg Countdown{remaining: 3} |> Iter.map(&(&1 * 10)) |> Iter.to_list() // [30, 20, 10]
    dbg Countdown{remaining: 3} |> Iter.sum()                             // 6
end
```

### `loop`

Taught in [Iteration and Loops](/iteration-and-loops/#loops-with-iterloop).

`Iter.loop(start, step)` calls `step` with a state, `start` first, and takes
its result as the next state, until a `break`. `import std::iter.Iter.{loop}` allows the bare `loop`.

```nomi
fn main()
    countdown = Iter.loop(5, fn n ->
        if n == 0 then break n
        n - 1
    end)

    dbg countdown // 0
end
```

- **The callback's type is `(S) -> S`.** Its result is the next state.
  `continue` goes round again with the state unchanged, and takes no value
  (§9 *`break` and `continue`*).
- **The starting state** is `from`, written before the callback:
  `Iter.loop(5, fn n -> ...)`, `Iter.loop(5, step)`. It fixes `S`. A loop
  with state and no starting state is `expected 2 arguments, got 1`.
- **`break value`** ends the loop with `value`, which has type `S`: `break
  "hello"` in `Iter.loop(5, fn n -> ...)` is `break value type mismatch:
  expected Int, got String`.
- **Bare `break`** ends the loop with the state this round started with. A
  binding in the body that shadows the parameter does not change it.
- **A stateless callback** (`fn -> ...`) has state `Unit`. Its body's result is
  `Unit`, every `break` in it is bare or `break Unit`, and the loop returns
  `Unit`. The call takes the callback alone, `Iter.loop(fn -> ...)`. A
  starting state beside it is ``a stateless `Iter.loop` takes no starting
  state``.
- `return` in the callback exits the callback.
- **A loop that never breaks.** A callback with no `break` of its own (a
  `break` inside a nested lambda or `fn` belongs to that callback) never
  finishes: only a trap, its task's cancellation or the end of the process
  stops it. A statement after it is the error `unreachable code after a loop
  that never breaks`, and a statement after one that always evaluates it,
  such as a binding of it, is unreachable too (§7 *Returning*). As a body's
  last statement it is legal: a server's main loop.
- Each round is a safe point (§16), and the loop runs in constant stack space
  (§7 *Tail-call optimization*).

```nomi
import std::io

fn main()
    // stateless: echo lines until "quit" or the end of the input
    Iter.loop(fn ->
        Ok(Some(line)) = io.read_line() else break
        if line == "quit" then break
        io.print(line)
    end)
end
```

### Callbacks that take `break` and `continue`

Taught in [Iteration and Loops](/iteration-and-loops/#break-and-continue-in-iter-callbacks).

`break` and `continue` work in the callbacks of a closed set of functions:
`Iter.map`, `Iter.scan`, `Iter.filter`, `Iter.take_while`, `Iter.drop_while`,
`Iter.flat_map`, `Iter.iterate`, `Iter.reduce`, `Iter.map_reduce`,
`Iter.each`, `Iter.find`, `Iter.any_match`, `Iter.all_match`, `Map.map_values`,
`Map.map_keys` and `Iter.loop`.

A callback's result is its result for this element. `continue` skips the
element. `break` stops the iteration with what has accumulated. `break value`
gives `value` as this element's result and then stops. The signal they send has
no spelling in the language: it is not a type, and nothing imports or matches
it.

| Function | `value` in `break value` | `continue` | bare `break` |
|---|---|---|---|
| `map` | this element's output, emitted | no output | stops; outputs so far |
| `filter` | the keep decision (`break True` keeps it) | drops it | stops |
| `reduce` | the accumulator | accumulator unchanged | the accumulator so far |
| `find`, `any_match`, `all_match` | the predicate's result | skips | `None`, `False`, `True` |
| `drop_while` | the drop decision: `break False` emits this element and ends | drops it | ends with nothing more |
| `flat_map` | this element's expansion, emitted | no output | stops |
| `scan` | the new state, yielded | yields nothing, keeps the state | ends without yielding |
| `map_reduce` | `(next_state, output)`, kept | no output, keeps the state | the state and outputs so far |
| `Map.map_values`, `Map.map_keys` | the entry's new value or key | leaves the entry out | the entries made so far |
| `Iter.loop` | the loop's value | the same state again | the state this round started with |
| `Iter.iterate` | the last element, emitted | a compile error | ends without emitting |

`continue` in an `Iter.iterate` step, written in its lambda or in a named
function passed as the step, is the error ``` `continue` in an `Iter.iterate` step has no next state to go on to: the step's answer is the next state ```
at the `continue`, or at the function's name; skip states with
`Iter.iterate(seed, step) |> Iter.filter(keep)`.

```nomi
fn main()
    names =
        Iter.map(["ann", "", "bo", "end", "cy"], fn name ->
            if name == "" then continue
            if name == "end" then break
            name
        end)
        |> Iter.to_list()

    dbg names // ["ann", "bo"]
end
```

### Named Functions as Callbacks

Taught in [Iteration and Loops](/iteration-and-loops/#helper-functions-that-break-or-continue).

A named function passes as a callback wherever a function type fits:
`Iter.map(numbers, double)`. A named function may use `break` and `continue`
in its body; its result type stays the plain value type.

```nomi
fn sum_valid(total: Int, value: Int) -> Int
    if value < 0 then continue
    if total > 1000 then break total
    total + value
end

fn main()
    dbg Iter.reduce([1, -2, 3], 0, sum_valid) // 4
end
```

Using `break` or `continue` makes a function *iter-sensitive*. It is then used
in one way only: passed as an iteration callback itself, positional, by name or
piped (`Iter.map(xs, keep)`, `Iter.find(xs, f: is_big)`, `xs |> Iter.map(keep)`).
A fold or loop takes its starting value before the callback:
`Iter.reduce(values, 0, sum_valid)`.

- Any call of it is an error at the call, since its signal would cross the
  caller: as the whole body of a callback lambda (`fn x -> keep(x)`) or capture
  (`&keep(&1)`), after a binding, in an arm, as an argument or operand, in
  another function, in a `defer` or a `concurrent` block, or outside every
  callback. So is naming it as a value (`f = keep`, `[keep]`).
- The error suggests passing the function, in a call its parameters fit: the
  iteration whose callback holds the call, otherwise `Iter.map` for one
  parameter, or `Iter.reduce` after a starting value for two whose first parameter's type
  is its result's:

  ```console
  `keep` uses `continue`, so it cannot be called: pass it as an iteration callback itself, as in `Iter.map(xs, keep)`
  `add_until_zero` uses `break`, so it cannot be called: pass it as an iteration callback itself, as in `Iter.reduce(xs, <start>, add_until_zero)`
  ```

  Inside a callback the help is ``to decide in this callback instead, return an
  `Option` or an enum from `keep` and write `continue` here``.
- A `break` or `continue` in a lambda or capture that is not itself an
  iteration callback, in a nested `fn`, or directly in a `concurrent` block is
  an error (§9 *`break` and `continue`*).
- An iter-sensitive function may be internal or `pub`. Which of `break` and
  `continue` it uses is part of how other files may use it, and their uses are
  checked as the declaring file's are. `main`, `boot` and an `export fn` may
  not be iter-sensitive: the run calls them, and no iteration catches their
  signal (§9 *`break` and `continue`*).

## 14. Files, modules and imports

### Modules and packages

Taught in [Modules & Imports](/modules-and-imports/#modules-and-packages).

A **module** is one `.nomi` source file, named by its import path (`users::helpers`, `std::fs`). It is what an `import` names, and the unit of scope and of private visibility. A **package** is the directory tree rooted at a `nomi.toml`, whose `[dependencies]` table names the packages it uses. It is what ships together under one version, the unit of internal visibility, and the unit the orphan rule (§12) checks. The standard library is one package of many modules. A **workspace** is several packages developed together, named by a `nomi-workspace.toml` above them, whose builds take one version of each dependency; each member is still its own package for names and visibility ([Workspaces](/tooling/packages-and-dependencies/#workspaces)). Projects, `nomi.toml` and dependencies are in the [tooling reference](/tooling/packages-and-dependencies/).

A file's import path is its path from the package root, without `.nomi`, with `::` between directories. Nothing in the file declares it, so renaming the file renames the path.

| File | Import path |
|---|---|
| `users.nomi` | `users` |
| `users/helpers.nomi` | `users::helpers` |
| `http/request.nomi` | `http::request` |

File names are snake_case. A file cannot be reopened or split across files. Importing `http` does not import `http::request`: a file is imported on its own, or reached through an import of a directory above it (*Directory imports* below).

A **file API object** is the qualifier a path-only import binds: `import http::request` binds `request`, and `request.get(...)` calls its `pub fn get`. Inside a file, the last segment of its own path is also that file's qualifier when no top-level declaration of the file has that name: `users.nomi` may call its own `pub fn new` as `users.new(...)`. In `main.nomi`, `fn main` owns the name, so there is no `main` qualifier.

**Import paths across packages.** Within a package, a path names a file of that package. A file may also start the path with its own package's short name, the `name` in its `nomi.toml` (`import my_app::models::user` inside `my_app`). A path into another package starts with the name the importing package gives it: the key of its entry in the importing package's `[dependencies]`. With `sqlite = { repo = "github.com/nomi-language/nomi", subdir = "lib/sql/sqlite", version = "v0.0.1" }`, `import sqlite::driver` names `driver.nomi` at the root of that package. The name is the importer's, so each package's files import through its own `[dependencies]`: a dependency's own files never see the names its consumer chose, and two dependencies with the same `[package].name` can both be used under different keys. An import that is a short name alone is an ordinary path inside its own package: inside `sqlite`, `import sqlite` names `sqlite.nomi` at the root, and with no such file and no directory `sqlite/`, the package's root directory (*Directory imports* below). From another package, a dependency's name alone names the file of that name at the dependency's root, so `import sqlite` is `import sqlite::sqlite` when the package holds `sqlite.nomi`, and binds `sqlite` either way. A dependency's name wins over a file of the same name in the importing package. A package with no such file is its root directory under its name alone: `import sqlite`, then `sqlite::driver.open(path)`.

### Import forms

Taught in [Modules & Imports](/modules-and-imports/#import-syntax).

Import paths separate segments with `::`, written with no space around it. An import takes one of these forms:

| Form | Binds |
|---|---|
| `import http::request` | the file API object `request` |
| `import http::request as req` | the file API object, renamed `req` |
| `import views::admin` | the directory `admin`, when `views/admin/` holds no file of that name (*Directory imports*) |
| `import task_parser.parse` | the exported item `parse`, bare |
| `import std::option.Option.Some as Just` | an item, renamed |
| `import users.{User, Role}` | each listed item |
| `import std::io.{self, print}` | `self` binds the file API object `io`, beside `print` |
| `import shape.Shape.{Circle, Square}` | variants selected through their enum |
| `import std::calendar.Disambiguation.{self, Earlier}` | `self` binds the owner type `Disambiguation`, beside the variant |

Several imports may be written as one block, one entry per line, with no commas:

```nomi
import
    std::io.{self, print}
    std::option.Option.{None as Nothing, Some as Just}
    http::request
end
```

The rules:

- `X.Y` after an import names a member of `X`, never a file: `import utils::inner.Thing` needs `utils/inner.nomi`, and does not look inside `utils.nomi`. A top-level function imported this way is called bare; a file imported path-only is reached through its qualifier.
- Between the path and the selected names, only PascalCase segments are owners, since an owner is a type. A lowercase segment after a dot with more after it is a path segment, so `import std.io.print` is `import std::io.print`.
- A `/` between segments is an error that spells the path with `::`: `import std/io` is `` import paths use `::`: write `std::io` ``. A `::` with a space on either side joins nothing, and is the error `` `::` joins the lowercase segments of a file's path, with no space around it, as in `std::io` or `db::posts.post_by_id` ``.
- Braces select names from one file; a path cannot be grouped. `import std::{io, regex.Regex}` is the error `` braces select names from one file, as in `std::regex.{Regex, Match}` ``.
- A selector list cannot hold another: `import shapes.{self, Shape.{Circle, Square}}` is the error `a selector list cannot hold another list`. Write `shapes` and `shapes.Shape.{Circle, Square}` as two entries of a block.
- A variant is imported through its enum. `import shape.Circle` is the error `` cannot import variant 'Circle' directly — use 'import shape.Shape.{Circle}' to lift specific variants, or 'import shape.{Shape}' and qualify as 'Shape.Circle' ``.
- There is no glob import (`import path.*`).
- Standard library modules start with `std::`: `import std::io`. A module that helps test another is nested in that module's directory as `testing`: `import std::fs::testing` binds `testing`, beside `import std::fs`, which binds `fs`. `std` alone names the standard library's root directory: `import std` binds it, and `std::io.print(x)` reaches `io.nomi` (*Directory imports*). A directory has no members to select, so `import std.io` is the error `` `std` is a package, not a file; an import names a file in it ``, with the hint ``did you mean `import std::io`?``.

Two imports that bind one name to two declarations are an error at the second: `import a.{Thing}` then `import b.{Thing}` is `'Thing' is already imported on line 1 from another file; two imports cannot bind one name`, with the hint to import one under another name (`Thing as OtherThing`). Importing the same declaration twice is an unused import.

A top-level declaration may not share a name with an imported item: `import other.helper` beside `fn helper` is `'helper' is already defined in this scope as a function`. A file or directory import's name follows the same rule, with its own message; see *Short names* below.

### Short names

Taught in [Modules & Imports](/modules-and-imports/#short-names-and-directory-imports).

A path-only import binds one name, its **short name**: the last segment of its path or its `as` alias. `import db::posts` binds `posts`, and `posts.post_by_id(1)` calls the file's `pub fn post_by_id`. The import's path is not a name at a use: after `import db::posts`, `db::posts.post_by_id(1)` is the error `` `db::posts` is not a name in this file: `import db::posts` binds the file as `posts` ``, with the hint `` write `posts.post_by_id`, or import its directory (`import db`) and keep `db::posts.post_by_id` ``. The editor's quick fix, and its `source.fixAll`, write the short name. A path at a use starts with the name a directory import binds (*Directory imports* below); any other path is the error `` `views::admin::reports` is not imported ``, whose hint names the file import and the directory import that would reach the file.

- **Two imports may not bind one name.** `import db::notifications` beside `import jobs::notifications` is the error at the second, `'notifications' is already imported on line 1 from db::notifications; two imports cannot bind one name`, with the hint `` import the directories (`import db` and `import jobs`) and write `db::notifications` and `jobs::notifications`, or import one under another name with `as` ``. A file and a directory of one name are the same error. The same file imported twice is an unused import. Two std modules nested under their parents (`std::fs::testing` and `std::process::testing`) have no directory to import, since `std::fs` is a file, so the hint renames one after its parent: `` import one under another name, as in `import std::process::testing as process_testing` ``.
- **A declaration keeps its name.** A top-level declaration of the file named like an import's short name is what the name means, and the import is the error ``'users' is already defined in this scope as a function on line 3, so `import views::admin::users` cannot bind it``, with the hint `` import its directory (`import views::admin`) and write `admin::users`, or import it under another name with `as` ``. A file at the package root is reached through the package's root: `import my_app`, then `my_app::users`.
- **A local keeps its name.** A local, a parameter or a pattern binding named like an import's short name wins in its scope, and `name.member` there reads the local. When a member access on it fails and the file imports a file of that short name, the error's hint says how to reach the file: `schema.post(1)` where `schema` is a local and `db::schema` is imported is `` Int has no field 'post' ``, with the hint `` `schema` here is the binding on line 4, not the file db::schema; import its directory (`import db`) and write `db::schema.post`, or import the file under another name with `as` ``.
- An import that binds nothing, since another import or a declaration holds its name, is that error alone and not also an unused import.
- An alias is a short name. `import jobs::mail as notifications` binds the file as `notifications` only.

Prefer the short name. Where it is ambiguous or taken, import the directory and write `dir::file`; alias a file where a directory import does not help (Writing Nomi: *Imports*).

### Directory imports

Taught in [Modules & Imports](/modules-and-imports/#short-names-and-directory-imports).

A path-only import whose path names a directory and no file binds the directory under its short name: `import views::admin` binds `admin` when `views/admin/` exists and `views/admin.nomi` does not. A path written below the name at a use reaches a file under the directory: `admin::users.index(rows)` calls `pub fn index` of `views/admin/users.nomi`, and `admin::reports::daily.show()` a function of `views/admin/reports/daily.nomi`. A directory import is how a file reaches several files under one directory with one import line.

```nomi
import views::admin

fn page(rows: List<admin::users.Row>) -> String
    "${admin::users.index(rows)}${admin::reports::daily.show()}"
end
```

- **Every position.** A path at a use stands wherever a file's short name does: a call (`admin::users.index(rows)`), a type (`admin::users.Row`, `std::http.Response<Int>`), a pattern (`db::schema.Kind.Draft`, `db::schema.Post{id:, title:}`), a struct literal (`db::schema.Post{id: 1, title: "x"}`), an owner function (`db::schema.Post.blank()`) and a variant (`db::schema.Kind.Published(4)`). Its last segment names a file and the segments between name directories. Like a short name, a path alone is not a value: `` `admin::users` is a file, not a value; name one of its members, as in `admin::users.name` ``.
- **The directory's name is not a file.** `admin.users` is the error `` `admin` is a directory, not a file ``, with the hint `` reach a file in it with `::`, as in `admin::users.name` ``, and `admin` alone is `` `admin` is a directory, not a value ``.
- **A path that names no file** is an error at the use that says what is there: `admin::user.render(u)` is `` `admin::user` names no file: views::admin has no user.nomi ``, with the hint `` did you mean `admin::users`? ``, or a list of the directory's files and directories when no name is close. A path that names a directory, `admin::reports.total()`, is `` `admin::reports` is a directory, not a file ``, whose hint names a file in it.
- **Only the files a path reaches are part of the program.** Importing a directory loads no file under it. Each path the import's scope writes below the name loads the file it names, as an import of that file would, and the file's impls come with it. A directory import through which no path reaches a file is unused: `imported directory 'admin' is unused — remove the import`.
- **A file wins over a directory of its name.** Where `views/admin.nomi` sits beside `views/admin/`, `import views::admin` names the file, since a parent file beside the directory of its children is the usual layout (*Cyclic imports* below). The directory is reached from its parent: `import views`, then `views::admin::users.index(rows)`.
- **Package roots.** A package's own name alone names its root directory when the root holds no file and no directory of that name: inside package `my_app`, `import my_app` binds the root, and `my_app::users.count()` reaches `users.nomi` there. A dependency's name alone is its root in the same way when the dependency has no file of its name (*Modules and packages* above), and `import std` binds the standard library's root: `std::io.print(x)`. A path through `std` reaches an internal std module no more than an import does: `` `std::phrase::format` is internal to std::phrase ``.
- **Another package's directory.** A directory that holds its own `nomi.toml` is another package's root, which no path of this package reaches. `import plugins::billing` is the error `` `plugins::billing` is another package's directory: it holds a nomi.toml ``, whose hint says to add the package with `nomi add` and import it by its name, and a path through it at a use is `` `plugins::billing::pay` is in another package's directory: plugins/billing/ holds a nomi.toml ``.
- **Scope.** The directory's name is a short name like a file's (*Short names* above): an alias renames it (`import views::admin as pages`, then `pages::users.index(rows)`), an import at the top of a block binds it in that block only, and a local, a parameter or a pattern binding of the name wins in its scope, where a path through the name is the error `` `admin` here is the parameter on line 3, not the directory views::admin ``.

**Placement.** Imports come first: at the top of a file, before any declaration, and at the top of a block, before any other statement. An import after a declaration is the error `imports must appear at the top of the file — 'std::io' came after another statement`, and one after a statement in a block is `imports must appear at the top of this block — 'std::io' came after another statement`.

An import inside a function body binds its names in that block only, and shadows a file-level binding of the same name. It costs nothing per call.

```nomi
import std::io

fn main()
    import std::io.print

    print("bare")
    io.print("qualified")
end
```

### Prelude

Every file outside the standard library starts with the imports in `std/prelude.nomi`, an ordinary file that holds one `pub import` block. It brings in:

- the primitive and collection types `Int`, `Float`, `Decimal`, `String`, `Unit`, `Infallible`, `Byte`, `Bytes`, `Codepoint`, `List`, `Vector`, `Map`, `Set` and `Range`, and `Bool` with its variants `True` and `False`;
- `Option` with `Some` and `None`, and `Result` with `Ok` and `Err`;
- std's subtypes of the primitive and collection types (§4 *Subtypes*): `NonZeroInt`, `NonNegativeInt` and `PositiveInt`, the same three for `Float` and `Decimal` (`NonZeroFloat`, `PositiveDecimal`), `NonEmptyString`, `NonEmptyList`, `NonEmptyVector`, `NonEmptyMap`, `NonEmptySet`, and `NonEmptyRange`, `ClosedRange` and `BoundedRange`;
- the interfaces `Add`, `Subtract`, `Multiply`, `Divide`, `Remainder`, `BitwiseAnd`, `BitwiseOr`, `BitwiseXor`, `BitwiseNot`, `ShiftLeft`, `ShiftRight`, `Averageable`, `Comparable`, `Contains`, `Debug`, `Discrete`, `Display`, `Distinct`, `Equatable`, `Hashable`, `Iter`, `Productable`, `Steppable`, `Struct`, `Summable` and `Variants`, `From` (§11 *Propagating with `?`*) and `TryFrom` (§12 *TryFrom*), and `Release` and `Commit` (§9 *Releasing resources*);
- `Ordering`, `Type`, `Startup` and `Context`;
- `Locale` (std::locale), and `Phrase` and `Noun` (§18 *Phrases*), so formatting a phrase needs no import.

What it leaves out:

- File API objects. `String` is in scope but `strings` is not, so `String.uppercase(s)` needs no import and `io.print` needs `import std::io`.
- Bare functions. The prelude binds types, interfaces and variants only.
- `Ordering`'s variants. Code writes `Ordering.Less` or `.Less`.
- `Literal` and `Fragment` (§18 *Typed literals*) and assertion types such as `Assertable` and `AssertionFailure`. Code that names them imports them.

Owner functions on a prelude type need no import (`Iter.map`, `Map.get`, `Result.is_ok`). An owner function on another type needs that type imported (`import std::assertions.AssertionFailure` for `AssertionFailure.format`).

Standard library files do not get the prelude; each imports what it uses. A std file's `//!` prompts (§17 *Attached tests*) see the file's scope, so a prompt that writes a bare `False` needs the file to import it.

The standard library's modules and their contents are in the [standard library reference](/reference/).

### Cyclic imports

Taught in [Modules & Imports](/modules-and-imports/#declaration-order-and-cycles).

Files may import each other in a cycle. Names resolve across the whole program, so the order of imports and declarations never matters. A parent file may import a child and the child the parent:

```tree
my_project/
├── http.nomi  # imports http/request; declares HttpRequest
└── http/
    └── request.nomi  # imports http.HttpRequest
```

A `once` whose initializer reads its own value is a compile error at its declaration, `` cyclic 'once' binding: `a` depends on itself: it reads `once a` (main.nomi:1) ``. A cycle closed through a function value traps when the read re-enters (§6 *`once` Bindings*).

### Import errors

#### Paths that name no file

An import path that names no file is an error at the path, in every import form, whether or not the file uses the import:

| Case | Error |
|---|---|
| No such file | ``no module `lib`: no file lib.nomi in this file's directory`` |
| No such file in a directory | ``no module `utils::inner`: no file inner.nomi in utils/`` |
| No such std module | ``no module `std::nosuch`: the standard library has no nosuch.nomi`` |
| No file, no directory, and no dependency of that name | ``no module `csvdata`: no file csvdata.nomi in this file's directory, and nomi.toml has no dependency named `csvdata` `` |
| A dependency whose directory has no `nomi.toml` | ``no module `csvdata`: [dependencies] csvdata is the directory ./generators/csvdata, which has no nomi.toml`` |
| A published dependency with no `nomi.toml` | ``no module `csvdata`: package `example.com/csvdata` has no nomi.toml, so it is not a Nomi package`` |

A hint names a sibling file the path misspells (``did you mean 'helpers'?``), or, for an import that selects names from a path that is a directory, a file inside it (`` `sub` is a directory; import a file in it, such as `sub::inner` ``). When the project needs a package the package cache does not hold, the hint says to run `nomi fetch` (`` the project needs package example.com/greet v0.1.0, which is not in the package cache; run `nomi fetch` to download it ``). When the first name is neither a file nor a directory and `nomi.toml` has no dependency of that name, the hint says to run `nomi add`, with the directory when exactly one directory under the project root (at most three levels down) or beside it holds a package of that name (`` run `nomi add ../csvdata` to add the package in that directory ``), and otherwise `` run `nomi add <repository>` to add a published package, such as `nomi add github.com/owner/name`, or `nomi add <directory>` for one on disk ``.


A selected name the file does not export is an error at the name: `file 'std::io' has no exported name 'nosuchname'`.

#### Names a missing import would bind

A name is in scope only through an import, so a std type or module used without one is the ordinary error for an unbound name, with a help naming the import. `Method.Post` is `undefined type or variant 'Method'` with ``help: import `std::http.Method` ``, `c: Catalog` is `unknown type "Catalog"` with ``help: import `std::phrase.Catalog` ``, and `io.print("x")` is `undefined variable 'io'` with ``help: import `std::io` ``. A type name two or three std modules export lists each import (``help: import `std::mail.Message` or `std::websocket.Message` ``); one that more export names none. The help never names an internal std module.

#### Unused imports are errors

An imported item that nothing in the file uses is an error, not a warning. Nomi warns only where a program is legal and probably not what its writer meant: an `embed` pattern with a missing base, and `$name` in a string where a value `name` is in scope (§3 *String Literals*). The unit is the item: in `import std::calendar.{Days, Months}` with `Months` unused, the error is at `Months`, `imported name 'Months' is unused — remove it from the import`. An unused file import is `imported module 'io' is unused — remove the import`, and an unused directory import `imported directory 'admin' is unused — remove the import`.

- An aliased item is used through its alias.
- `self` in a brace list is its own item, used when the name it binds is used.
- Any reference that resolves to the imported name is a use: an expression, a pattern, a type annotation, an `impl` header, a `where` bound, a `derive` declaration, and a typed literal's prefix (`Date"..."` uses `Date`).
- An item re-exported with `pub import` is used.
- A name in a comment or a doc-comment example is not a use.
- A dot-shorthand pattern (`.Some(v)`) resolves through the scrutinee's type, so it does not use a variant import.

An import of a whole project file also brings that file's impl blocks into the program (§12 *Foreign Types*). It is used when the program uses one of those impls, even if nothing names the file: a call that resolves to one of its functions, or a conformance the program needs of the type and interface it implements (a `where` bound, an interface-typed parameter, an operator, an interpolation). The use may be anywhere in the program. Two impls do not count: one for a type the imported file declares, since a value of that type already needs another import of the file, and a call from inside the file to its own impl. A standard library file is never used this way, since std's impls are always in the program.

#### Re-importing a prelude name is an error

Importing a name the prelude already binds is an error at the item, even when the item is used: `import std::iter.Iter` is `imported name 'Iter' is already in scope from the prelude — remove it from the import`. A `self` under a prelude type (`import std::option.Option.{self, None}`) is the same error. The rule follows from `prelude.nomi` and applies only where the prelude is in scope, so standard library files are exempt.

These are not re-imports:

- a whole-file import (`import std::strings`), since the prelude binds no file API object;
- an aliased item (`import std::iter.Iter as It`), which binds a new name;
- a selection below what the prelude binds (`import std::comparable.Ordering.{Less}` binds bare `Less`);
- a same-named item of another module, since identity is the resolved declaration, not the spelling.

### Visibility

Taught in [Modules & Imports](/modules-and-imports/#visibility).

A top-level declaration has one of three levels of visibility, which say which files may name it:

| Written | Level | Named from |
|---|---|---|
| `pub fn f()` | public | every file of every package |
| `fn f()` | internal | every file of its own package |
| `private fn f()` | private | its own file |

A package is the directory tree under a `nomi.toml` (*Modules and packages* above), and all of the standard library is one package. A file run with no `nomi.toml` is in the package its project root holds, the directory project-root discovery finds: the first directory up from the file that holds a `main.nomi`, else the file's own directory ([tooling reference](/tooling/projects/#project-root-discovery)). Other files reach a declaration through its file's qualifier (`io.print(...)`, `io.Error.NotFound{...}`) or by importing it by name. Inside the declaring file, its declarations are bare.

```nomi
pub struct Config
    host: String
    port: Int
end

pub fn parse(s: String) -> Result<Config, String>
    ...
end

fn validate(c: Config) -> Bool
    ...
end // internal: every file of this package may call it

private fn trim_all(s: String) -> String
    ...
end // private to this file
```

- **Placement.** A visibility modifier is the first one: `pub [opaque] <keyword> <name> ...` or `private [opaque] <keyword> <name> ...`. `private` is a contextual keyword: it is a modifier only where a declaration's keyword follows it, and a name everywhere else. A declaration takes one: `pub private fn` and `private pub fn` are each the error `` a declaration has one visibility: write `pub` or `private`, not both ``.
- **What takes a modifier.** `fn`, `struct`, `enum`, `type`, `interface`, `typealias`, `once`, `host fn`, `host type`, a function or `once` of an inherent `impl Type` block, and a keyword declaration (`phrase`, `noun`, `embed`). `pub` also stands on `import`, which then re-exports (*Re-exporting imported names* below); `private import` is the error `` an import takes no `private`: it binds its names in this file only; write `import` ``, since an import binds names in its file and nowhere else.
- **What does not.** Interface impl functions take their interface's visibility, and struct fields and enum variants their owner's. A declaration inside a body (a function's, a lambda's, a test's, a `once` initializer's) is local to that body, and a modifier there is the error `` `pub` applies to top-level declarations only ``, or the same for `private`. An `export fn` (§7 *Exported Functions*) is callable from outside the program, which is a different thing; `pub export fn` is the error `` an exported function takes no `pub`: write `export fn` ``, and `private export fn` the same with `private`.
- **A file qualifier is not a value.** `io` alone, as a statement, a binding's value or an argument, is the error `` `io` is a file, not a value ``.
- **File API is top-level functions.** A file's functions and host functions are reached through its qualifier (`users.find(...)`). A function or `once` declared in an `impl` block is reached through its owner (`Duration.seconds(1)`, `Display.to_string(n)`). A file-qualified spelling of one, `duration.seconds(1)`, is the error `file 'duration' has no member 'seconds'`, whose hint names the owner.
- **A declaration is invisible where its level does not reach.** No file outside its reach can name it in any position: a call, a type annotation or type argument, a struct literal or pattern, a variant, an interface bound or `impl` header, an owner function (`Gear.spin`), or an import. Naming a private declaration from another file is the error `` `helper` is private to `engine.nomi` ``, whose hint says to drop `private` or write `pub`. Naming an internal declaration from another package is `` `checksum` is internal to package `sqlite` ``, whose hint says to write `pub`. The hint is shown only when the declaring package is one the reader can edit: the program's own, or a dependency named by `dir`. A dependency fetched into the package cache gets the message with no hint, since its files are not the reader's to change.
- **A test of a private declaration is in its file.** A test of a private declaration, or of an opaque type's inside, is a `test` block in the declaring file (§17 *Tests*), since `shapes_test.nomi` sees `shapes.nomi` as every other file of the package does.

- **A type may be a type argument where it cannot be named.** A file may pass its own private type to another file's public generic (`other.ident(Point{x: 1})`). Only the files that see the type name it.
- **Impl functions follow the interface.** An impl's functions are called from every file that sees the interface. There is no per-function modifier in an `impl Iface for Type` block.
- **No renames at the declaration.** A declaration is published under its own name. An imported item is re-exported under its local name.
- **`opaque`.** `pub opaque struct`, `pub opaque enum` and `pub opaque type Name Inner` publish the type's name and keep its construction, destructuring and field access to its file (§4 *Opaque distinct types*), and `opaque` with no modifier does the same within the package. `opaque` on anything else is an error: `'pub opaque' must be followed by 'struct', 'enum', or 'type'`, and on a zero-sized type `` cannot declare 'Marker' as opaque — zero-sized distinct types have no representation to hide ``.
- **Std's internals.** All of the standard library is one package, so a std declaration without `pub` is internal to it, as any package's is. Naming one from a program is the error `` `compiled` is internal to package `std` ``, with the help `` `compiled` is not part of std's public surface ``, since no program can change std; when one public std module declares a type of the name, the help gives that module's reference page. The modules that hold std's plumbing (`std::fs::host`, `std::money::format`) declare nothing public.

### Visibility consistency

A declaration may not expose a type fewer files see than see the declaration: a public declaration may name no internal or private type, and an internal one no private type, whether the type is its own file's or one it imports or reaches through a qualifier. A function or `once` of an inherent `impl` block is held to its own level as a top-level one is. It also may not be seen by more files than see its type, whatever its signature names: a `pub` member of an internal or private type, or an internal member of a private type, is one error at the member's name, in place of an error at each place its signature names its type. A private declaration exposes nothing. Each of these is an error, with the type's level in it:

| Exposure | Error |
|---|---|
| A `pub fn`'s parameter | `function parameter exposes internal type Secret` |
| A `pub fn`'s result | `function return exposes internal type Secret` |
| A `pub struct`'s field | `public struct field exposes internal type Secret` |
| A `pub enum`'s `embeds` | `public enum embed exposes internal type Secret` |
| A `pub interface`'s function signature | `public interface function parameter exposes internal type Secret` |
| A `pub typealias` | `public type alias exposes internal type Secret` |
| A `pub once`'s type, written or inferred | `public once binding exposes internal type Secret` |
| An internal `fn`'s result | `function return exposes private type Secret` |
| An internal `struct`'s field | `internal struct field exposes private type Secret` |
| A `pub once` whose type is declared in a body | `public once binding exposes private type Box, which is declared inside a body` |
| A `pub fn` or `pub once` of an internal type's inherent `impl` | `` `double` is `pub`, but its type `Money` is internal: drop `pub`, or write `pub struct Money` `` |
| An internal `fn` or `once` of a private type's inherent `impl` | `` `double` is internal, but its type `Money` is private: write `private fn double`, or drop `private` from `struct Money` `` |

```nomi
struct Secret
    value: String
end

pub fn leak() -> Secret
    Secret{value: "x"}
end // error: function return exposes internal type Secret
```

An `opaque` type is the exception: its representation (the inner type, its fields or its variants' payloads) may name any type, since no other file can reach the representation (§4 *Opaque distinct types*).

### Re-exporting imported names

Taught in [Modules & Imports](/modules-and-imports/#re-exports).

`pub import` makes every item the statement selects part of the file's public surface, under its local name: the alias when there is one, the imported name otherwise. In a block, every entry is re-exported. A file that re-exports some items and imports others privately writes an `import` and a `pub import`.

```nomi
pub import
    std::bool.Bool.{self, False, True} // the type and its variants
    std::io.{print, write}
    other.{Helper as Helpr, Thing}
end
```

- **Items only, never a file.** `pub import leaf`, `pub import leaf as lf` and a brace list whose `self` is the file (`pub import std::io.{self, Error}`) are each the error `` `leaf` is a file, and a file cannot be re-exported ``, with a hint to list the items or have importers import the file. A `self` after an owner type (`Bool.{self, False, True}`) is the type, and is re-exported like any item.
- A file that a facade imports is not one of the facade's items: `import facade.{leaf}` is the error `` `leaf` is a file that `facade` imports, not an item it exports; import `leaf` directly ``.
- The upstream item must be public. A re-export cannot widen a declaration: `pub import other.{helper}`, where `helper` is internal, is `` `helper` is internal, and `pub import` re-exports public items only ``, though a plain `import other.{helper}` in the same package is allowed. An item the file cannot import at all is the error that import is (`` `helper` is private to `other.nomi` ``).
- `pub import` applies only to imported names. To publish a local declaration under another name, rename it or write a wrapper.

### Member access is fail-loud

`x.y` and `T.y` must name a member the static type guarantees. A missing member is a compile error where it is written, never a run-time trap. The one exception is a value whose type is still being inferred; the check waits until inference resolves it.

- **A value of a concrete type.** `p.z` must name a field the type declares: `struct 'Point' has no field 'z'`. A function of the type is not a field: where `impl HasName for Person` declares `fn name`, `p.name` is `struct 'Person' has no field 'name'`, whose hint names the call `Person.name(p)` or `HasName.name(p)`. A value with no fields at all is the same error, `() -> Int has no field 'x'`.
- **A tuple** and a named tuple read their items by ordinal name: `pair.first`, `pair.second`. Reads chain left to right, so `nested.second.first` is `(nested.second).first`. An ordinal past the last item is an error that names the last (§4 *Tuples*), and `pair.0` is the error that writes `pair.first`.
- **A value of interface type** has no fields, since an interface declares functions only: `interface 'Struct' has no field 'x'`. A required value is read through a function requirement (§12 *Requiring a Value*), qualified by the interface: `n.name` is `interface 'HasName' has no field 'name'` even when `HasName` declares `fn name`.
- **A value of type-parameter type** has no fields, bounded or not: ``type parameter `T` has no field 'name'``. Call the bound's function, `T.name(x)`.
- **A type qualifier** must name a real member: an inherent or interface function, a `once`, or a variant. `List.empty()` is `type 'List' has no member 'empty'`, and `Option.Bogus` is `type 'Option' has no member 'Bogus'`. `empty` or `new` on `Map`, `Set` or `Vector` adds a hint naming the empty literal (`write the empty map as {}`). Members of imported types are checked the same way.

How `X.Y` resolves:

1. When `X` is a value (a local, a parameter, a `once`, a local function), `X.Y` is field access on that value. A file of the same name is never consulted, since files are not values.
2. When `X` is an imported file API object, type or interface, `X.Y` is a qualified name, and `Y` must be a public member of `X`.
3. When `X` is a path at a use (`admin::users`), `X.Y` is a qualified name when the path's first segment is a directory import's name in that scope (§14 *Directory imports*), and an error otherwise.

## 15. Program execution

### Entry point

An **entry file** is a file that declares a top-level `fn main`. A program runs one entry's `main`, after its `boot` when it has one. How a program is started, from a file or a project, is in the [tooling reference](/tooling/commands/#running-a-program).

#### Entry callbacks

`fn main` takes no parameters. A `main` that declares one is the error `` `main` takes no parameters `` over its parameter list, whose help says a program reads its arguments in `fn boot(startup: Startup)` as `startup.args`.

`main` returns `Unit`, with or without the annotation, or `Result<Unit, E>` for any `E`. Any other result type is the error `` `main` must return `Unit` or `Result<Unit, E>` `` at the annotation, since a run would drop the value; its help says to print the value with `io.print` and return `Ok(Unit)`.

`fn boot` is optional and is declared in the entry file beside `main` (*Scoped application fields* below). A program with no boot runs under a root context and has no application fields.

#### Exit status

A `main` that returns `Unit` or `Ok(Unit)` exits with status 0.

A `main` that returns `Err(e)` prints `error: ` and `e`'s text to standard error and exits with status 1. The text is `Display.to_string(e)` when `e`'s type implements `Display`, so a `String` prints without quotes, and `Debug.inspect(e)` otherwise. An `Err` that holds an `AssertionFailure`, which a failed `assert` in `main` returns, prints the assertion's report.

```nomi
fn run() -> Result<Unit, String>
    Err("config file not found")
end // as `main`: prints `error: config file not found` and exits 1
```

To choose the status, `main` returns std::process's `Exit` in its `Err`. `Exit` is a struct with the fields `code: ExitCode = 1` and `message: String = ""`, where `ExitCode` is std::process's `subtype ExitCode Int when fn code -> code >= 0 and code <= 255`. A `main` declared `-> Result<Unit, Exit>` that returns `Err(e)` prints `e.message` and a newline to standard error, with no `error: ` before it, when the message is not empty, and exits with `e.code`. A code of 0 exits 0, and its message still prints. A literal code outside 0 to 255 does not compile (`300 is not an ExitCode: ...`), and a computed one goes through `ExitCode.try_from`.

```nomi
import std::process.Exit

fn run() -> Result<Unit, Exit>
    Err(Exit{code: 2, message: "usage: count <file>"})
end // as `main`: prints `usage: count <file>` and exits 2
```

Only std's `Exit` does this. A program's own type named `Exit` is an ordinary error and prints after `error: `. No function exits on the spot: an `Exit` returned through `?` ends the program from anywhere `main` calls. A test checks an exit as a value: `assert main() == Err(Exit{code: 2, message: "usage"})`.

A boot that returns `Err` ends the program the same way, before `main` runs (*Cleanup and boot failure* below). For a boot declared `-> Result<App, Exit>`, supervised tasks drain and boot's deferred calls run before the program exits with the code, so the message prints after them.

A program that `SIGINT` or `SIGTERM` stops exits with status 130 or 143, the status a shell gives a process the signal ends, after its supervisors drain and boot's deferred calls run (§16 *Flushing and shutdown*). It does so whatever `main` returns, also when `main` returns during the drain.

### Scoped application fields

Taught in [App Fields, Defer & Context](/app-fields/#the-basic-shape).

#### Boot and `Startup`

An entry file may declare a boot, `fn boot() -> Result<App, E>` or `fn boot(startup: Startup) -> Result<App, E>`, for any struct `App` and any error type `E`. Boot builds the application value and returns it in `Ok`, or returns `Err` when the program cannot start. A boot that cannot fail still returns a `Result`, so adding a step that can fail does not change its signature. The struct in the `Ok` of an entry boot is an **application type**. Its name is not reserved and it needs no impl. A boot may also return an anonymous record (`fn boot() -> Result<{n: Int}, String>`); its fields are published, but no read can name them.

An application type has at most one field of type `Context`, under any name. Boot receives no context; one that wants a `Context` field builds it with `Context.root()` or a deriver such as `Context.with_timeout(Context.root(), d)`. An application with no `Context` field runs under the root context.

`Startup`, in the prelude, holds snapshots of the process's environment and arguments:

```nomi
pub struct Startup
    env:  Map<String, String> = {}
    args: List<String> = []
end
```

Startup is not ambient. Boot passes what helpers need as ordinary arguments, and a test builds one as an ordinary struct (`Startup{args: ["-v"]}`). The runtime calls a boot that takes no parameter with no arguments, and passes the process's `Startup` to one that takes it. The parameter may have a default, `startup: Startup = Startup{}`; the runtime always passes the real one, so the default serves only a `group`'s `boot` line that passes none (§17 *Groups*).

```nomi
import std::io

struct App
    name: String
end

fn boot(startup: Startup) -> Result<App, String>
    Ok(App{name: List.first(startup.args) |> Option.with_default("world")})
end

fn main()
    io.print("Hello, ${App.name}!") // Hello, world!
end
```

The boot's signature is checked:

| Violation | Error |
|---|---|
| Any parameters but none or one `Startup` | `` boot must be `fn boot() -> Result<App, E>` or `fn boot(startup: Startup) -> Result<App, E>`, taking no parameter or one Startup and returning the application struct in a Result `` |
| A result that is not a `Result` of a struct | `` boot must return `Result<App, E>`: the application struct, or the error that stops the program `` |
| Two `Context` fields | `an application struct may have at most one Context-typed field` |
| `fn boot` in a file with no `fn main` | `` `boot` belongs in an entry file, one that defines `fn main`; a `group` names an entry's boot with `boot entry.boot(startup)` `` |
| A call of boot outside a test, or a reference to it as a value | `` entry-point boot cannot be called or captured as an ordinary function; a `group` calls it on its `boot` line, and a test may call it to check the Result it returns `` |

An entry boot is called in two places: a `group`'s `boot` line, which publishes the app for the group's tests (§17 *Groups*), and a test, which calls it as an ordinary function to check the `Result` it returns (§17 *Calling boot in a test*). A test file names the boot through an import of its entry file, so a boot that tests run is `pub`, and so is its struct.

#### Reading a field

`App.field` reads one top-level field of the application value in force. `T.field` is a field read only when `T` is an application type, which the checker learns from the project's entry boots: the entry file's own and those of the entry files it imports. A file that reads a field imports the type as it imports any name. On an application type, `App.field` always names the field, so the type may not declare an inherent function or `once` with a field's name: `` `App` is an application type, so `App.n` reads its field `n`; rename this member ``. On any other type, `Type.member` keeps its owner-qualified meaning.

#### `with`

Taught in [App Fields, Defer & Context](/app-fields/#with-replaces-a-field-for-the-rest-of-the-block).

`with App.field = expr` is a statement that replaces one field from that line to the end of the enclosing block: the extent a `defer` on the same line would have. Every function called in that extent reads the replacement. However the block exits (its end, `return`, `?`, a failure), the previous value comes back. The value must have the field's type, and a `.Variant` value resolves against it. A struct literal needs no parentheses: `with App.store = FakeStore{}`.

Several fields take several lines. Each value is evaluated when its line runs, so a later line reads what an earlier one replaced:

```nomi
fn in_region()
    with App.region = "eu"
    with App.endpoint = "https://${App.region}.example.com"
    sync()
end
```

`with` works in any block: a function body, an `if` or `case` arm, a `begin` block, a lambda body, a test body, a group's `setup`. It has no value and takes no block. To limit an override to part of a function, put the line in a `begin` block or a helper function. There is no override of the whole application value or of a nested field; replace a structured field with an update, `with App.database = {..App.database, host: "localhost"}`.

| Violation | Error |
|---|---|
| A comma after the value | ``a `with` statement replaces one field; write one `with` line per field`` |
| A `{` after the value on its line | `` `with` is a statement and takes no block: the override lasts to the end of the enclosing block `` |
| A target that is not an application field | `` `with` replaces an application field, such as `MyApp.logger`; `Point.x` is not one `` |
| A value of the wrong type | `` `App.port` replacement expects Int, got String `` |

An override changes the fields in force; it does not change values already computed.

#### Closures and tasks

Code runs with the app fields in force where it was written. A call passes them on, and a closure and a task keep them. An app field is an argument the program does not write: a call passes it to the callee, and a closure captures it as it captures any other value in scope (§7 *Closures*).

A lambda, a capture, a nested `fn` and a named function used as a value each keep every field in force where the function value is made, replaced or not. The body runs under those fields wherever and whenever the value is called, and the caller's fields are back in force when it returns, however it leaves. A `with` in the body replaces a field for the rest of its block, as anywhere else. A task's body is a closure, so a task started with `Task.spawn` runs under the fields its body kept.

```nomi
import std::io

struct App
    greeting: String
end

fn boot() -> Result<App, String>
    Ok(App{greeting: "hello"})
end

fn greet() -> String
    "${App.greeting}, world"
end

fn quiet_greeter() -> () -> String
    with App.greeting = "psst"
    io.print(greet()) // psst, world
    fn -> greet()
end

fn main()
    later = quiet_greeter()
    io.print(later()) // psst, world: the closure keeps the field it was made under
end
```

Anything lazy that holds closures follows from this: an `Iter` pipeline built in a block with a `with` and consumed after it runs its callbacks under the fields where they were written. A type's own `impl Iter` is not a closure. The code that consumes the value calls its `each_while` and passes on its own fields.

A closure made where no application exists yet keeps nothing, and its body runs with the fields in force where it is called. That is a closure made while boot runs, and one made while a `once` initializer runs. Written out, such a closure would take the application from its caller as a parameter, since nothing in force where it was written could supply one.

The `Context` field is an app field like the others: a closure keeps the context in force where it was made, its values and its deadline. Calling the closure puts its fields in force as `with` lines would, so a deadline in force at the call still bounds the body, as one does when a program replaces its context field (*Context* below). A supervised task keeps its body's fields and drops their deadline, since the task outlives the work that started it.

#### Which boot code runs under

Each entry's `main` runs under its file's boot, or none, and each test under its group's `boot` line, or none (§17 *Groups*). A boot must return the application type that every field read reachable from the code it runs names: the `main` or test body, a group's `setup`, and every function they call, across files and through callbacks and lambdas. Code that reads no field runs under any boot or none.

| Violation | Error |
|---|---|
| A read of another application type | `` this test boots `Fake`, but code it runs reads `MyApp.logger` (server.nomi:2) ``, at the boot |
| A read with no boot | `` this program has no boot, but code it runs reads `App.n` (main.nomi:4) `` at `main`, or `` this test has no boot, but code it runs reads `MyApp.logger` (server.nomi:10) `` at the test |
| A read of a type no entry boot returns | `` `App.port` reads an application field only when an entry boot returns `App`, and no `fn boot` in this project does; check the entry file that boots it `` |
| Reads of two application types | `` code this program runs reads `App.port` (main.nomi:12) and `Other.n` (other.nomi:4); one boot publishes one application type `` |
| A read during boot, or in a `boot` line's argument | `` boot runs before any application field is published, but code it runs reads `App.port` (main.nomi:4) `` |

The runtime finds the `Context` field by its type, so a field named `execution` is read and replaced as `App.execution` and gets the same protections.

#### Cleanup and boot failure

Taught in [App Fields, Defer & Context](/app-fields/#cleanup-and-failure-in-boot).

A `defer` in the outer block of an entry boot runs after `main` returns and the tasks it owns have stopped. A `defer` in a test's boot or `setup` runs after that test and its work, and so does one in a boot a test calls directly. A `defer` anywhere else runs when its block ends. A deferred call keeps the arguments and the field values in force when it was registered. A failing cleanup does not hide the first failure or stop the rest of the cleanup.

A boot that returns `Err(e)` fails the program: `main` does not run, work boot started is stopped, the calls boot registered with `defer` run, most recent first, and the program exits as one whose `main` returned `Err(e)` does (*Exit status* above). A program with exported functions returns that error in place of the booted program (§7 *Exported Functions*). In a `group`, a boot that returns `Err` fails each test it serves before the body runs, with the report of an early `?` (§17 *Groups*). A trap in boot is a trap like any other.

```nomi
import std::io

struct App
    name: String
end

fn boot(startup: Startup) -> Result<App, String>
    defer io.print("boot cleanup")
    name = List.first(startup.args) |> Option.to_result("pass a name")?
    Ok(App{name:})
end
```

Run with no arguments, a program with this boot prints `boot cleanup`, then `error: pass a name` on standard error, and exits 1.

### Context

Taught in [App Fields, Defer & Context](/app-fields/#deadlines).

A `Context` is an opaque value that carries a deadline and execution-scoped values through a call chain. Only the runtime, `Context.root()` and the `Context.with_*` derivers make one. A deadline is the only control state it carries. Stopping a task (`Task.cancel`, a supervisor's drain, shutdown) travels with task ownership and takes effect at safe points (§16 *Safe points*); it is never written onto a `Context`.

A program holds its context in the application type's `Context` field and replaces it with `with`:

```nomi
struct Config
    context: Context
end

fn handle(req: Request) -> Response
    with Config.context = Context.with_timeout(Config.context, Duration.seconds(30))
    do_expensive_work(req)
end
```

The context is a field rather than an ambient value so that each `with` line is the one place a deadline is fixed: the runtime derives one wait context there for every blocking operation in its extent, not one per read. An embedder's cancellation and an inherited deadline stay in force when a program replaces its context field.

A deadline is a timeout or a moment. `Context.with_timeout(c, d)` ends `d` of elapsed time after it is made, counted on the monotonic clock, so setting the system clock (by NTP, a resumed laptop or a user) neither ends it early nor holds it late. `Context.with_deadline(c, at)` ends when the wall clock reads `at`, and it is measured against the wall clock each time it is read, so setting the system clock moves it as it moves that moment. The deadline with the least time left anywhere along the chain is in force. `Context.deadline_remaining` is that time, and `Context.deadline` the `Instant` it ends at: the one named, or for a timeout the wall clock's reading plus the time left. The `with` line turns the deadline into time left, and the waits in its extent count that time down on the monotonic clock.

#### Context values

A context value is metadata that follows the current unit of work: a trace ID, an auth subject, a request ID. It is not how a program passes dependencies (use application fields such as `App.store`) or a locale (pass a `Locale`, [std::locale](/reference/locale/#how-code-gets-a-locale)).

A value's Nomi type is its key. `Context.with_value(c, v)` returns a child of `c` holding `v` under `v`'s type, and `Context.value(c, TraceId)` the nearest value of that type from `c` up through its ancestors; `TraceId` there is a type witness (§4 *Type witnesses*). [std::context](/reference/context/) has the functions.

```nomi
import std::io

type TraceId String

struct App
    context: Context
end

fn boot() -> Result<App, String>
    Ok(App{context: Context.root()})
end

fn main()
    with App.context = Context.with_value(App.context, TraceId("trace-123"))

    case Context.value(App.context, TraceId)
        Some(TraceId(id)) -> io.print("trace ${id}") // trace trace-123
        None              -> io.print("no trace")
    end
end
```

The functions of `Context` are in [std::context](/reference/context/).

## 16. Concurrency

Every task has an owner: a `concurrent` block or a supervisor. There is no fire-and-forget task. Values are immutable, so tasks share no mutable state, and channels pass immutable values.

### `concurrent` and tasks

Taught in [Concurrency](/concurrency/#concurrent-taskspawn-taskawait).

`concurrent` alone on its line, a body below it and `end`, is a block expression that opens a task scope. Its value is its last expression. `concurrent` is an ordinary name anywhere else, so a binding named `concurrent` read at the end of a line goes in parentheses. Every task started in it has finished or been cancelled before the block's value is produced.

Control flow in the block is scoped to the block, as in a lambda: `return`, `?`, `break` and `continue` leave the block, not the enclosing function. Any exit other than reaching the end cancels every task still running in the block before the exit continues outward.

`Task<T>`, from `std::tasks`, is the handle of a running task; `T` is its body's result. `Task.spawn(body)` starts `body`, a function of no parameters, as a task of the enclosing block, and returns its `Task<T>` at once. The body runs alongside the code that started it, and the values it captures are shared, immutably. `Task.await(task)` waits for the task and returns its `T`. The other task operations (`Task.spawn_all`, `Task.await_all`, `Task.try_await_all`, `Task.race`, `Task.within`, `Task.stream`, `Task.outcome`, `Task.cancel`) are in [std::tasks](/reference/tasks/).

```nomi
import
    std::io
    std::tasks.Task
end

fn square(n: Int) -> Int
    n * n
end

fn main()
    total =
        concurrent
            a = Task.spawn(fn -> square(3))
            b = Task.spawn(fn -> square(4))
            Task.await(a) + Task.await(b)
        end

    io.print("${total}") // 25
end
```

A failed `assert` or `refute` in the block's own body leaves the block as `Err(AssertionFailure)`, so the block's value must be a `Result<_, AssertionFailure>`. Any other value is the error `` an assertion inside a concurrent block exits the block with Err(AssertionFailure), but the block's value is Int: end the block with a Result<_, AssertionFailure> (`Ok(...)`), or move the assertion out of the block `` at the assertion. In a test, such a block as the body's final value fails the case with the assertion's report.

A task body is a lambda, and an assertion in a lambda is an error (§17 *Assertions*). To assert in a task, spawn a `fn` that returns `Result<_, AssertionFailure>`; its failure is the task's value, which the code that awaits it handles. A test fails only from its own line of execution.

### Static guarantees

The compiler enforces three rules, which together rule out leaked tasks.

1. **`Task.spawn` runs only inside the dynamic extent of a `concurrent` block.** A function that calls `Task.spawn` may itself be called from inside a block. A spawn that can run outside every block is the error `spawn outside concurrent block`. A task's body runs in its block's extent, so a spawn inside it, directly or through a helper at any depth (`Task.spawn(fn -> helper())` where `helper` spawns), joins that block: the block waits for the task, its failure and cancellation are those of a task spawned in the block's own body, and cancelling the task that spawned it does not cancel it. A `concurrent` block inside a task body is a block of its own.
2. **Every task is awaited or cancelled.** A handle bound from `Task.spawn`, or a list of handles bound from `Task.spawn_all`, in a `concurrent` block is settled on every path from its binding to the end of the block that declares it. A reference settles it in these positions:
    - an argument of a call of `Task.await`, `Task.await_all`, `Task.try_await_all`, `Task.outcome` or `Task.cancel`;
    - an argument of a call where another argument names one of those functions, or is a lambda or capture that calls one on its parameter on every path through its body: `Iter.each(handles, Task.cancel)`, `Iter.each(handles, fn t -> _ = Task.outcome(t))`;
    - the left of a `|>` whose stage is such a call, names such a function, or is such a lambda: `handles |> Task.await_all()`, `handles |> Iter.map(Task.outcome)`.

    An `if` settles a handle when both branches do, and an `if` with no `else` settles nothing; a `case` settles it when every arm does. A branch that always leaves the block (`return`, `break`, `continue`, `unreachable`, a loop that never ends) needs no settle, and neither does the path a `?` takes out: every exit from the block other than its end cancels the tasks still running in it (*Errors and cancellation* below). What runs only sometimes or later settles nothing outside it: the right of `and` or `or`, and the body of a lambda or capture, which may run many times or never, whether it is passed to a loop, bound to a name or returned. A settle in a nested `concurrent` block counts, since that block runs once in place.

    No other use settles it: reading it (`Task.inspect(t)`, `Iter.count(handles)`), copying it to another name (`u = [t]`, `_ = t`), or passing it to a function the program declares. A handle that is not settled is the error `Task bound to 't' is never awaited or cancelled`, which names the use the block makes of it, or `Task bound to 't' is not awaited or cancelled on every path`, which names a branch that leaves it unsettled. A task a supervisor owns (*Supervision* below) is not held to this rule.

3. **A `Task<T>` does not leave its block.** It cannot be returned from the block, sent on a channel, or stored where it outlives the block: `Task<T> cannot escape its enclosing concurrent block`.

Under rule 2, the first two blocks below settle each handle, and the other three are errors:

```nomi
concurrent
    first = Task.spawn(fn -> fetch(1))
    rest = Task.spawn_all([2, 3], fetch, max_running: 2)
    Iter.each(rest, Task.cancel) // settles every handle in `rest`
    Task.await(first)
end

concurrent
    t = Task.spawn(fn -> fetch(1))
    n = fetch(0)? // an Err leaves the block and cancels t
    Ok(n + Task.await(t)?)
end

concurrent
    t = Task.spawn(fn -> fetch(1))
    Task.inspect(t) // error: Task bound to 't' is never awaited or cancelled
end

concurrent
    t = Task.spawn(fn -> fetch(1)) // error: not awaited or cancelled on every path
    // when `cached` is False, nothing awaits or cancels t
    if cached then use(Task.await(t))
end

concurrent
    t = Task.spawn(fn -> fetch(1)) // error: never awaited or cancelled
    _check = fn -> Task.outcome(t) // a lambda that is never called
end
```

### Errors and cancellation

Taught in [Concurrency](/concurrency/#errors-cancel-siblings).

A task's error is a value. A fallible body returns `Result<T, E>`, and the code that awaits it decides what to do:

```nomi
concurrent
    a = Task.spawn(fn -> fetch_user(42))
    b = Task.spawn(fn -> fetch_order(101))

    u = Task.await(a)? // an Err leaves the block and cancels b

    o =
        case Task.await(b)
            Ok(order) -> order
            Err(_)    -> default_order // handled here; nothing is cancelled
        end

    Ok((u, o))
end
```

A task that fails is a different matter: its body trapped, so there is no value to return. `Task.await` propagates the failure to the code that awaits it, by design, as the trap would have surfaced had the body run inline, and awaiting a task that was cancelled propagates the cancellation. On the main line either ends the program with the task's fault, as any trap does. `Task.await_all` propagates a failure the same way, as soon as a task in the block fails. `Task.outcome(task)` is the total form: it waits as `Task.await` does and returns how the task ended as a value, `Outcome<T>` (*Failures and restarts* below). Code that cancelled a task, or that reports a task's failure instead of inheriting it, reads the task with `Task.outcome`.

Cancellation has two sources. Both stop the work at its next safe point (*Safe points* below), and the stop unwinds the task, running its deferred calls on the way out.

- **The block exits early.** When a `concurrent` block exits by `?`, `return`, `break` or `continue`, it cancels every task still running in it. User code does not see this: the cancelled body stops and unwinds.
- **A deadline or the embedder.** Each `concurrent` block derives its context from the application context in force (§15 *Context*), and its tasks inherit it. When that context's deadline passes, or the embedder cancels the root, the work under it stops. A task settles `Cancelled`, and the main line ends with the deadline's error: `` deadline exceeded: the context in force when `main` blocked ran out, so the remaining work was not run ``.

A deadline is set with `with App.context = Context.with_timeout(App.context, d)` before the work, in the block that holds it. There is no handle that cancels a context. Exceeding the deadline is not a value the block returns: the rest of the region the `with` line governs is skipped, so code after the `concurrent` block in that same region cannot report the timeout. To observe it, put the deadline inside a spawned task and read `Task.outcome` from outside that region.

```nomi
fn fetch_both()
    with App.context = Context.with_timeout(App.context, Duration.milliseconds(50))

    concurrent
        a = Task.spawn(fn -> slow_call())
        b = Task.spawn(fn -> slow_call())
        _ = Task.await(a)
        _ = Task.await(b)
    end
end
```

### Safe points

A **safe point** is every function call, tail calls included; every loop iteration (an `Iter.loop` step, and each element an iteration passes along, including one with no callback); and every operation that waits (`timer.sleep`, `Sender.send`, `Receiver.receive`, `Task.await`, a read of standard input). Waiting operations also wait on the context, so cancellation and deadlines reach a task blocked in one.

A task that was cancelled, or whose deadline has passed, stops at the first safe point it reaches, so pure computation stops too. A single call to a host function is not interrupted; the work stops at the next safe point after it returns. A program must not depend on which safe point a stop lands on.

### Channels

Taught in [Concurrency](/concurrency/#channels).

A channel is a typed FIFO queue between tasks, held as two halves. `std::channels` declares `Channel<T>`, a struct of a `Sender<T>` (`ch.sender`) and a `Receiver<T>` (`ch.receiver`). A function that takes one half can use only that direction; nothing at run time keeps the halves apart.

- `Sender.send` returns an `Err` once the channel is closed, including a send waiting for room when it closes, and `Receiver.receive` returns `None` once it is closed and drained. `Receiver.iter` reads a channel's values as an `Iter` until then. Only a `Sender` closes it, and closing a closed channel does nothing. [std::channels](/reference/channels/) has the functions.
- A `send`, `receive` or `select` blocked when its context is cancelled unwinds like any cancelled wait.
- A channel belongs to no block. It may be made in one block, returned, and used in a later one, and its memory is reclaimed when nothing refers to it.

```nomi
import
    std::channels.{Channel, Receiver, Sender}
    std::io
    std::tasks.Task
end

fn produce(out: Sender<Int>, n: Int) -> Unit
    case Sender.send(out, n)
        Ok(u)  -> u
        Err(_) -> io.print("send failed")
    end
end

fn consume(inbox: Receiver<Int>) -> Int
    Iter.loop(0, fn sum: Int ->
        case Receiver.receive(inbox)
            Some(v) -> sum + v
            None    -> break sum
        end
    end)
end

fn main() -> Unit
    ch: Channel<Int> = Channel.buffered(4)

    total =
        concurrent
            p1 = Task.spawn(fn -> produce(ch.sender, 10))
            p2 = Task.spawn(fn -> produce(ch.sender, 20))
            cons = Task.spawn(fn -> consume(ch.receiver))
            Task.await(p1)
            Task.await(p2)
            Sender.close(ch.sender)
            Task.await(cons)
        end

    io.print("total=${total}") // total=30
end
```

#### Waiting on several channels

`Channel.select(arms)` waits until one of a non-empty list of arms can finish, runs that arm's handler and returns its result. An arm is an `Arm<R>`: `Arm.receive(receiver, handle)` passes `handle` the received `Option<T>`, `Arm.send(sender, value, handle)` passes it the send's `Result<Unit, ChannelClosed>`, and `Arm.after(wait, handle)` calls it with nothing once `wait` has passed. The arms may be on channels of different element types; every handler returns the one type `R`.

```nomi
import
    std::channels.{Arm, Channel}
    std::duration.Duration
end

fn main()
    inbox = Channel.buffered<String>(1)

    reply = Channel.select([
        Arm.receive(inbox.receiver, fn message -> Option.with_default(message, "closed")),
        Arm.after(Duration.seconds(0), fn -> "nothing yet"),
    ])

    dbg reply // "nothing yet"
end
```

- **The first listed ready arm wins.** When several arms can finish as the `select` starts, the first one in the list finishes and the others take and send nothing, so a run repeats exactly. An arm that is always ready therefore starves the arms listed after it; a loop that serves several channels fairly rotates its arms. While the `select` waits, the first arm to become ready wins.
- **Closed channels are ready.** A receive arm on a closed and drained channel finishes at once with `None`, and a send arm on a closed channel with `Err(ChannelClosed)`. So a `select` whose channels are all closed runs its first listed channel arm.
- **Timers count on the clock in force** from the start of the `select`: under `clock Clock.Virtual` a timer fires as soon as every task is waiting, with no real wait. A timer of zero or less is ready at once, so `Arm.after` of zero listed last makes a `select` that never waits.
- **The arms are a `NonEmptyList<Arm<R>>`**, which a list literal of arms is, whatever its arms are (§4 *Subtypes*); a list built while the program runs goes through `NonEmptyList.try_from`.
- **A waiting `select` is cancellable**: when its context is cancelled it unwinds like any cancelled wait, and no handler runs.

`Receiver.receive_within(receiver, wait)` is the one-channel case: `Ok(Some(value))`, `Ok(None)` when the channel is closed and drained, or `Err(TimedOut)` when `wait` runs out first. A value already waiting is received even when `wait` is zero.

Fan-in from producers that share one message type needs no `select`: they send on one channel of a tagged enum, as [std::channels](/reference/channels/#multiplexing) shows. `select` is for timeouts, sends that must not block, and channels of different types or owners.

### Deadlock

When every task of a program waits for another of its tasks, none can run again. The program then stops with a fault at the wait of its main line: `every task is waiting, so none can run again:`, then a line for each task, naming what it waits for and where. The main line is `main`, a test case, or the function an embedder called. A test case that deadlocks fails at once with this report, not at its time limit.

```console
line 9: every task is waiting, so none can run again:
  main waits for a task to end at shop.nomi:9
  the task spawned at shop.nomi:7 waits to receive on a channel at shop.nomi:4
```

The waits it counts are a send, a receive, a `select`, `Task.await`, `Task.outcome` and `Task.await_all`, the end of a `concurrent` block, a `max_running` slot, and a `once` another task is computing. It reports only when nothing else could still end one of them. These could, so a program waiting on one is not reported, and runs until it ends or is stopped:

- a timer: `Arm.after`, `Receiver.receive_within`, a deadline in force (§15 *Context*);
- a task in a call outside the program: a socket, a process, standard input, a file, `timer.sleep`, a supervisor's restart delay;
- a channel a signal feed sends on (`std::signals`), or one that `Sender.close_on_drain` closes when the program shuts down;
- `Supervisor.flush`, and anything while the program shuts down.

### Supervision

Taught in [Background Work](/background-work/#declaring-your-supervisors).

Long-lived background work is owned by a `Supervisor`, from `std::supervisors`. A supervisor is a set of tasks that share one policy: a required `max_running` bound (a `PositiveInt`), a shutdown budget (`shutdown_timeout`, with a default for ordinary background work) and a restart policy. `max_running` is required because no default bound is safe for an unknown downstream.

A supervisor is created only while `boot` builds the application value, in boot or in something boot calls, and is usually kept in an application field. Anywhere else, `Supervisor.new` is the error `` a supervisor here would be a new one every time this runs, and each is kept for the life of the program with a `max_running` of its own — so the bound would be per call rather than per downstream. Create it while `boot` builds the app value: in boot, or in something boot calls ``.

The checker follows this across the whole program, std included. A function is *boot-only* when it calls `Supervisor.new` or another boot-only function, directly or through any chain of calls in any module: std::jobs's `Worker.new` is one, and so is an app's function that calls it. Code starts running outside boot in `fn main`, in a test body, in a lambda or capture (wherever it is written, since it runs whenever it is called), and in a private function nothing calls. Each call such code makes to a boot-only function is an error at that call, naming the chain down to `Supervisor.new`: `` `make_worker` creates a supervisor (through `Worker.new` at workers.nomi:4, then `Supervisor.new` at jobs.nomi:483), so it can only be called while `boot` builds the app value ``. A call inside a boot-only function is not itself an error; the call into that function is. A `pub` function nothing calls is not checked from inside: it is another package's to call. `boot`, and a function a test group's `boot` calls, runs inside boot, and so does an entry boot a test calls. A boot-only function named as a value (`f = Worker.new`) is an error where it is named, in boot too, since a function value may be called anywhere. The runtime traps with the same message as a backstop.

```nomi
import
    std::duration.Duration
    std::supervisors.Supervisor
end

struct App
    context: Context
    audit:   Supervisor
end

fn boot() -> Result<App, String>
    Ok(App{
        context: Context.root(),
        audit:   Supervisor.new(max_running: 4, shutdown_timeout: Duration.seconds(30)),
    })
end
```

#### Spawning under a supervisor

`Supervisor.spawn(supervisor, body)` starts a task under the supervisor and returns a `Task<Unit>` at once. The body must return `Unit`, since nobody is required to await it: `argument 2: expected () -> Unit, got () -> Int`. The handle is the one task handle that may be discarded with `_ =`; it may also be cancelled, inspected with `Task.outcome` or awaited.

```nomi
fn handle_order(id: Int) -> Response
    response = build_response(id)
    _ = Supervisor.spawn(App.audit, fn -> record_audit(id, response))
    response
end
```

A supervised task may open its own `concurrent` block; the block owns the tasks it starts, and the supervisor owns the task.

#### Flushing and shutdown

The program drains every supervisor when `main` returns or the process receives `SIGINT` or `SIGTERM`. Supervisors drain at the same time, each within its own `shutdown_timeout`; when a budget runs out, the remaining work is cancelled, and shutdown waits up to one second for it to unwind and run its deferred calls before abandoning it. Boot's deferred calls run after that (§15 *Cleanup and boot failure*). A second interrupt exits at once. When the drain starts, a terminal std::terminal holds in raw mode, or with echo off for a password, is put back in the mode it had before, so a program interrupted there leaves the terminal as it found it ([std::terminal](/reference/terminal/)).

`SIGINT` and `SIGTERM` belong to the runtime: no program can catch, delay or ignore them, so a program always stops when asked, and one that needs longer to finish gives its supervisors a longer `shutdown_timeout`. `SIGHUP`, `SIGUSR1` and `SIGUSR2`, which ask a running program to do something and keep running, end the program until it subscribes to them; [std::signals](/reference/signals/)'s `signals.receiver` makes each arrival a value on a channel instead. `SIGWINCH`, a change in the terminal's size, is ignored until a program subscribes to it, and is then a value the same way. The drain closes those channels as it closes a listener, and a signal that arrives during it is dropped.

#### Failures and restarts

`Task.outcome(task)` reports how a task ended, as std::tasks's `Outcome<T>`:

```nomi
enum Outcome<T>
    Completed T
    Cancelled
    Failed Failure
end
```

A body that returns `Err(e)` produced a value, so its outcome is `Completed(Err(e))`. `Failed` is a trap (§11 *Traps, `Option` and `Result`*). A supervisor may restart failed work, and never restarts a cancelled task, since cancellation is not a failure. [std::supervisors](/reference/supervisors/) has the restart policies and `Supervisor.flush`.

## 17. Tests

### Tests

Taught in [Testing](/testing/).

`test "name"`, its body below and `end`, declares one test. Tests may be declared in any `.nomi` file. They are checked with the program but run only under `nomi test`; an ordinary run ignores them. How `nomi test` finds, selects, times and reports tests is in the [tooling reference](/tooling/running-tests/).

```nomi
test "new user"
    user = users.new("Alice", 30)
    assert user.name == "Alice"
end
```

- A `test` is declared at the top level of a file or in a `group`. One inside a body (a function's, a lambda's, a test's or a `once` initializer's) is the error `` a `test` is declared at the top level of a file or in a `group`; it cannot be nested inside a body ``.
- A test must be able to fail, so its body holds at least one `assert`, `refute` or `subtests` statement. One without is the error `test block must contain at least one assert or refute`.
- A test's name is not empty or only whitespace: `` a test name must not be empty; name the test for what it checks, such as `test "parses a date"` ``. Names are unique within a file by their full path, group included. `nomi check` accepts a duplicate; `nomi test` reports it as a failed case, `duplicate test name "x" at line 5; first declared at line 1`.
- `return` in a test body ends the test: its pending deferred calls run and it passes. `return value` makes `value` the body's final value (*Assertions* below).
- A `with App.field = value` line in a test body replaces an application field for the rest of the body (§15 *Scoped application fields*).

### Groups

Taught in [Testing](/testing/#groups-and-setup).

`group "name"`, its lines and tests below and `end`, holds tests that share a clock, a boot or a setup. It holds at most one `clock` line, at most one `boot` line, at most one `setup`, and its `test` blocks, or a `subtests` block that holds them all and runs them once per row (§17 *Subtests in a group*). Each part is optional and may stand anywhere among the tests; they run in a fixed order whatever their position.

```nomi
group "defaults"
    setup {name: "No Name"}

    test "default user", {name:}
        user = users.default()
        assert user.name == name
    end
end
```

| Violation | Error |
|---|---|
| A `group` inside a body | `` a `group` is declared at the top level of a file; it cannot be nested inside a body `` |
| A `group` inside a `group` | `` a `group` cannot contain another `group`; write a sibling group instead `` |
| A `group` with no name | `` a `group` needs a name; name the group for what its tests share, such as `group "dates"` `` |
| An empty name | `` a `group` name must not be empty; name the group for what its tests share, such as `group "dates"` `` |
| A second `clock`, `boot` or `setup` | `` a `group` has at most one `setup` line `` (and the same for `clock` and `boot`) |
| `fn boot` in a group | `` a `group` does not define `fn boot`; it names an entry's boot on a `boot` line, such as `boot server.boot(startup)` `` |
| Anything else in a group body | `` a group body may contain only clock, boot, setup, subtests and test declarations `` |
| A `boot` line that calls anything but an entry's boot | `` `boot` in a `group` calls an entry's boot, the `fn boot` of a file that defines `fn main`, such as `boot server.boot(startup)`; `helpers.make_app` is not one `` |

`group` is a keyword only at the top level of a file, before a name string or alone on its line. Anywhere else it is an ordinary name, so `group = Iter.group_by(...)` works.

**Each test starts fresh.** For each test of a group: its clock is selected, the `boot` line's call runs and publishes the application value, `setup` runs, then the test's body. Nothing carries from one test to the next. A `defer` in the boot or the setup runs when that test finishes, after its body and its work.

**`clock`.** `clock Clock.Virtual` or `clock Clock.System` (the default), with `Clock` from `std::testing`. Under a virtual clock, time moves only when every task in the test is blocked, so a test that sleeps or waits on a deadline costs no time and takes the same path every run. Work that can never finish is reported as a deadlock, and so is a wait on something outside the program (reading input, a network call), since that never lets a virtual clock move.

**`boot`.** The `boot` line calls an entry's boot (§15 *Scoped application fields*) as an ordinary call checked against the boot's signature: `boot server.boot()` calls a boot that takes no parameter or whose parameter has a default, and `boot server.boot(startup)` passes a `Startup`. The test file imports the entry file. A test outside a group, or in a group with no `boot` line, runs with no boot and cannot read application fields. A boot that returns `Err` fails each test it serves before the body runs, with the report of an early `?`: the `boot` line, and the `Err` it returned.

```nomi
import
    server
    server.App
end

group "configured sender"
    boot server.boot(Startup{env: {"SENDER" => "ada"}})

    test "reads the booted sender"
        assert App.sender == "ada"
    end
end
```

**Calling boot in a test.** A test may also call an entry's boot as an ordinary function, to check what it returns: the `Err` that stops the program, or the fields of the app in `Ok`. The call may stand in a `test` body, a group's `setup` or an attached test, lambdas inside them included; anywhere else, and as a function value anywhere, it is the error in §15 *Scoped application fields*. The call runs boot as the program does, so boot may create supervisors, but it publishes nothing: the test reads the app's fields from the value it got, and `App.field` still reads only what the group's `boot` line published. What the boot starts belongs to the test that called it: its supervised work is stopped and the calls it registered with `defer` run when that test finishes, after its body, whether boot returned `Ok` or `Err`.

```nomi
import server

test "postgres needs a database URL"
    startup = Startup{env: {"STORE" => "postgres"}}
    assert server.boot(startup) is Err("STORE=postgres needs DATABASE_URL")
end
```

**`setup`.** `setup expr`, or `setup begin` with statements before its final value and `end`, produces the value the group's tests receive. A setup whose last line is a statement, such as a `with` line, produces `Unit`; one whose last line is `assert x` produces what `ok = assert x` binds. `return value` in a setup ends the setup with that value, and the setup's type is the type its final value and every `return` agree on. A `with` line in `setup` stays in force for the body of the test it serves, so `setup` is where a group installs fakes. A `?` or a failed assertion in `setup` fails the test it serves with the same report it would give in the body, and the body does not run.

A test binds the setup's value with a pattern after its name: a name (`test "loads rows", db`), a tuple (`(left, right)`) or a record (`{name:}`). A test with no pattern ignores the value. The pattern must match every value of the setup's type: `a test's pattern must match every setup value; bind it with a name or an irrefutable pattern and match it in the body`. Bindings inside `setup` do not reach the tests.

```nomi
group "store"
    setup begin
        db = sqlite.open("tasks.tsv")?
        defer sqlite.close(db)
        db
    end

    test "loads rows", db
        assert Store.count(db) == 0
    end
end
```

A test's standard input and output are captured with `io.capture` and `io.replay` ([std::io](/reference/io/)), and its files with [std::fs::testing](/reference/fs/testing/).

### Attached tests

Taught in [Testing](/testing/#attached-tests).

An **attached test** is a run of `//!` lines immediately before a declaration. It runs under `nomi test` like a `test`, and its lines are checked in the scope of the declaration that follows. Its lines hold what a test body holds, `subtests` included, and it too needs an assertion: `attached test must contain at least one assert or refute`.

```text
attached-test      = attached-test-line+
attached-test-line = "//!" [statement]
```

Contiguous `//!` lines are one attached test; a blank line starts another. A `//!` starts its line, after nothing but indentation: one after code is the error `` a `//!` attached test must start its own line, above the declaration it tests; write `//` for a comment after code ``.

```nomi
//! assert answer() == 42
//
fn answer() -> Int
    42
end
```

### Assertions

Taught in [Testing](/testing/#assertions).

`assert expr` and `refute expr` are language forms, not functions. A failure leaves the nearest enclosing test, or `Result`-returning function, with `Err(AssertionFailure)`; in `main`, declared `-> Result<Unit, AssertionFailure>`, it ends the program with the report. In a function with another result, an assertion is the error `` `assert` can return AssertionFailure, but the enclosing function returns Int ``. An assertion in a lambda is an error, since it would only be the lambda's value.

What each subject requires, and what the assertion's value is when it holds:

| Subject | `assert` requires | `refute` requires | Value |
|---|---|---|---|
| `Bool` | `True` | `False` | the `Bool` |
| `Result<T, E>` | `Ok(_)` | `Err(_)` | the subject |
| `Option<T>` | `Some(_)` | `None` | the subject |
| an `Assertable` | `Assertable.failure` returns `None` | it returns `Some(details)` | the subject |

`assert pattern = value` asserts that the value matches the pattern and binds the pattern's names. A mismatch is an assertion failure.

```nomi
assert [left, right] = pair

assert Err(Error.InvalidFormat(reason)) = Date.parse("not-a-date")
```

**Where an assertion stands.** An assertion is a statement, or a binding's value (`ok = assert x`). Its value is the same wherever it stands: `ok = begin` above `assert x` and `end`, an `if` or `case` arm ending in `assert x` whose value is bound, `ok: Bool = assert x` and `Some(n) = assert m else return` all bind what `ok = assert x` binds, and `_ = assert x` checks `x` and discards it. A case arm's body may be an assertion. It is not an operand: `not assert x` is the error `` `assert` cannot be an operand of `not` ``, whose help suggests `refute x` or `assert not x`, and so is an assertion beside any binary operator but `|>`. Anywhere else it is the error `` `assert` cannot be used as a value here: an assertion stands as its own statement or as a binding's value ``: as another assertion's subject, an argument, a collection element, inside parentheses, a condition or scrutinee, a returned value, or the head of a pipe. To assert a pipeline, put the keyword before the whole of it:

```nomi
assert "Ada Lovelace"
    |> String.contains("Ada")
```

**`testing.check`.** `testing.check(expr)`, from `std::testing`, takes the subjects `assert` takes and returns `Result<Subject, AssertionFailure>` instead of leaving: `Ok` of the assertion's value, or `Err` of its failure. It is std's `check` under any name the file gives the module (`t.check(expr)` after `import std::testing as t`) or the function (`check(expr)` after `import std::testing.{check}`, `must(expr)` after `{check as must}`), called or as a pipe stage. It takes no type arguments. Named as a value (`Iter.map(flags, testing.check)`), it takes the subject type its position gives it, which must be one `assert` takes: `Iter.map(counts, testing.check)` over Ints is the error `testing.check expects Bool, Result, Option, or Assertable, got Int`. A project's own file named `testing` is not `std::testing`, and its `check` is an ordinary function.

**A final `Err(AssertionFailure)` fails the test.** A test body or a `setup` whose final value is `Err(AssertionFailure)` fails with that failure's report. The rule reaches through a final `if`, `case` or block to the arm that ran, and applies to `return value`. A body whose final statement is itself an assertion is judged by the assertion. The body still needs an assertion of its own (*Tests* above).

```nomi
import std::testing

test "total"
    total = 10
    assert total > 0
    testing.check(total == 10)
end
```

When a `?` exits a test early, the report shows the `?`'s source and the value it returned.

### Failure reports

A failed assertion's report shows the assertion and, under `values:`, each operand it compared with its value. A value reads structurally: a `String` is quoted, a struct's fields are sorted by name, a host value with no `impl Debug` reads as its type's name, as `dbg` prints it, and a type's `impl Display` is not used. A type with a hand-written `impl Debug` reads as that impl renders it, at any depth, as `dbg` prints it; a derived or universal `Debug` changes nothing. A literal operand has no row, nor does a function named by its own name. A name bound to a function keeps its row (`g = <func: double>`). A test that a `?` ends early, or whose group's `boot` returns `Err`, reports that line and, under `returned:`, the `Err` or `None` it returned, rendered as a row is.

**Line diffs.** When `assert a == b` fails and both operands are Strings, at least one with a line break, the report shows a line diff in place of their two rows, the left operand as the actual value and the right as the expected. The [tooling reference](/tooling/running-tests/#line-diffs) shows its layout.

### Subtests

Taught in [Testing](/testing/#subtests).

`subtests rows, pattern`, its body below and `end`, runs the body once per row of a table, in order, with `pattern` bound to the row. `rows` is anything `Iter` accepts: a `List`, `Vector`, `Set`, `Range` or `Iter`, or a `Map`, whose rows are its `(key, value)` pairs.

```nomi
test "determines palindromes"
    cases = [("abba", True), ("abc", False), ("x", True)]

    subtests cases, (line, known_palindrome)
        assert is_palindrome(line) == known_palindrome
    end
end
```

The rows may also be piped into the statement: `rows |> subtests pattern, name` is `subtests rows, pattern, name`, each with the same body and `end`. The `|> subtests` stage has no value, so it must be the last stage of a pipeline that stands as a statement of its own. A stage after it is the error `` `|> subtests` must be the last stage of its pipeline: it has no value to pass on ``, and a pipeline used as a value is `` a `|> subtests` stage has no value, so its pipeline must be a statement of its own ``.

```nomi
test "doubles"
    [(1, 2), (2, 4), (3, 6)]
    |> subtests (n, doubled)
        assert n * 2 == doubled
    end
end
```

The rules:

- The pattern must match every row: `` a `subtests` pattern must match every row; bind it with a name or an irrefutable pattern and match it in the body ``. Rows that are not iterable are `` `subtests` runs its body once per element of what it is given, which must be something `Iter` accepts, such as a List, a Range or an Iter; got Int ``.
- The body must be able to fail a row: it holds an `assert` or `refute` of its own (outside a lambda or a nested `fn`), or its final value, in every arm of a final `if` or `case`, is a `Result<_, AssertionFailure>`. Otherwise it is the error `` `subtests` body must contain an assert or refute, or end each row with a Result<_, AssertionFailure> ``. A row whose final value is `Err(AssertionFailure)` fails, so a row may end with a helper that asserts.
- A row is not a function, and every row runs to its end, so `return`, `break`, `continue` and `?` directly in the body are errors, such as `` `return` cannot be used in a `subtests` body: a row is not a function, and each row runs to its end `` and `` `?` cannot be used in a `subtests` body: a row is not a function, so there is nothing to return the Err or None to ``. Inside a lambda or a nested `fn` in the body they leave that, as anywhere.
- A failed assertion ends its row only, and the next row runs. A trap in a row is not caught: it fails the test, and the rows after it do not run.
- `subtests` is a statement whose value is `Unit`, and it fails as an assertion does. In a function that returns `Result<_, AssertionFailure>`, a failure returns `Err(failure)`, whose `keyword` is `"subtests"`, whose `reason` is the count line (`2 of 3 subtests failed`), and whose `subtests` field lists each failed row as a `SubtestFailure` with its `row` and its `failure`. Where an assertion cannot stand, neither can `subtests`: in a function with another result (`` `subtests` can return AssertionFailure, but the enclosing function returns Int ``), in a lambda, in a `once` initializer (`` `subtests` cannot be used in the initializer of `once v`: a `once` has no function to return from ``) or in a default. It cannot be written in a `setup` (`` `subtests` cannot be used in a `setup`: a setup builds the value each test binds ``) or inside another `subtests` body (`` `subtests` cannot be written inside another `subtests` body: each row of the outer statement is one case ``).
- `subtests` is a keyword only where a statement starts with it, an expression starts on its line, and a comma follows that expression, or after `|>`. Anywhere else it is an ordinary name: `subtests = rows` and a function named `subtests` work.

**Row names.** `subtests rows, pattern, name` names each row. `name` is an expression of type `String`, written after the pattern and a comma, and evaluated once per row after the pattern binds and before the body runs, in the scope of the pattern's bindings. A binding only the name reads counts as used. The name ends with the header's line, and a struct literal may stand in it (`Point{x:, y:} |> Point.describe()`). A `_` or `_`-prefixed name in the pattern discards a column either way.

- A name of another type is `` the name of a `subtests` row must be a String, got Int ``.
- The name is evaluated on its own, so `return`, `break`, `continue`, `?` and an assertion directly in it are errors, such as `` `?` cannot be used in the name of a `subtests` row: the name is evaluated on its own, before the row runs ``.
- A name that traps fails its row, and the row's body does not run (below).

```nomi
test "leap years"
    cases = [
        ("year divisible by 400 is leap year",                         2000, True),
        ("year divisible by 100, not divisible by 400 in common year", 1900, False),
    ]

    subtests cases, (description, year, expected), description
        assert is_leap_year(year) == expected
    end
end
```

**Reports.** After the last row, when any row failed, the statement fails with a report that lists each failed row: the row rendered as a `values:` row renders a value, or its name as a String literal, then that row's assertion report, then the count. Passing rows are not listed. Two rows may share a name.

```console
FAIL palindrome_test.nomi :: determines palindromes
  subtest ("abc", True): line 5: assertion failed
    assert is_palindrome(line) == known_palindrome
    values:
      line
        = "abc"
      is_palindrome(line)
        = False
      known_palindrome
        = True
  1 of 3 subtests failed
```

A row whose name trapped is listed under the rendered row with `its name trapped:` and the trap's text (without its own `line N:` when the trap is on the name's line), then the name as written:

```console
  subtest 0: line 12: its name trapped: `NonZeroInt.try_from(n)` found nothing
    "${10 / NonZeroInt.try_from(n)!}"
```

**Cases.** Each row a test runs is a case in the run's totals. A test that runs `subtests` counts as many cases as rows it ran, summed over its statements, and a test with none counts one. A failed row is a failed case and a passing row a passed one, so 9 rows of which 1 failed add `8 passed, 1 failed`. A failure outside every row (an assertion after the rows, or a trap in a row) is one more failed case. A passing test prints one `ok` line with its count, `ok palindrome_test.nomi :: determines palindromes (3 cases)`.

### Subtests in a group

Taught in [Testing](/testing/#subtests-in-a-group).

`subtests rows, pattern`, written in a `group` with its items below and `end`, holds the group's `clock`, `boot` and `setup` lines and its tests, and runs all of them once per row, with `pattern` bound to the row in the `boot` line, the `setup` and every test. Each row's tests are cases of their own, and each starts fresh as any test of a group does: for each row and each test, the clock is selected, the `boot` line runs with that row, then the `setup`, then the test's body. The boot is the entry's `fn boot`, run once for each case.

```nomi
import
    server
    server.App
end

group "todo api"
    subtests ["memory", "sqlite"], store
        boot server.boot(Startup{env: {"STORE" => store}})

        test "boots the row's store"
            assert App.store == store
        end
    end
end
```

A case is named with its row after the group's name, in brackets: `todo api [memory] / boots the row's store`, then `todo api [sqlite] / boots the row's store`, rows in order and, within a row, the tests in order. A row that is a String is named by its text. `subtests rows, pattern, name` names a row by a column: `name` is a name the pattern binds to a String in every row, as in `subtests [("memory", 1), ("sqlite", 4)], (store, workers), store`. Any other row is named as a `values:` row renders it: `[2]`, `[("memory", 1)]`. A test inside may run a `subtests` statement of its own, whose rows count as that case's cases (*Cases* above), so the group's cases are its rows times its tests, each counted as its own statements' rows. `nomi test --line` at the `subtests` line, as at the group's, runs the whole group, and at a test, that test for every row. `nomi test --run` matches a case by this name, so a pattern naming a row, `--run '\[sqlite\]'`, runs that row alone.

The rows are read when the tests are collected, before anything runs, because each row's cases are named and counted then. So:

- The rows are a list literal: `` the rows of a `subtests` block in a `group` are a list literal, such as `["memory", "sqlite"]`: each row's tests are named and counted when the tests are collected, before anything runs ``. A test that needs computed rows runs a `subtests` statement in its body.
- Each row is a literal value, a String without interpolation, an Int or Float with or without a `-`, `True` or `False`, or a tuple of them: `` a row of a `subtests` block in a `group` is a literal value, read when the tests are collected: a String, a number, True or False, or a tuple of them ``. A `?` in a row is `` `?` cannot be used in a row of a `subtests` block in a `group`: the rows are literal values, read when the tests are collected ``.
- The pattern must match every row, as a statement's must: `` a `subtests` pattern must match every row; bind it with a name or an irrefutable pattern and match it in the body ``.
- A name is a String column of the pattern: `` the name of a row of a `subtests` block in a `group` is a String column its pattern binds, such as `subtests [("memory", 1)], (store, workers), store` ``, and a column of another type is `` the name of a `subtests` row must be a String, got Int ``.
- The block is the group's only item, so every item runs once per row and none runs without one: `` a `group` with a `subtests` block holds nothing else: its clock, boot, setup and tests go inside the block, which runs them once per row ``.
- Rows have two dimensions at most: a group's row, and a statement's row in one of its tests. A block inside the block is `` `subtests` in a group cannot hold another `subtests`: each row of the outer one boots the group once ``; write the rows as tuples instead, as in `subtests [("memory", 1), ("sqlite", 2)], (store, workers)`.
- In a group, `subtests` is a keyword where an item starts with it and an expression follows on its line.

## 18. Typed literals and keyword declarations

### Typed literals

Taught in [Typed Literals](/typed-literals/#any-type-can-be-the-prefix).

A typed literal is a type name written directly against a string literal: `Date"2026-05-04"`, ``Regex`\d+` ``. Its surface forms are in §3 *Typed literal forms*. This section says what one means.

#### The `Literal` interface

A type backs its literals by implementing `Literal`, from `std::literals`:

```nomi
pub interface Literal<I, R>
    fn from_fragments(fragments: List<Fragment<I>>) -> R
end

pub enum Fragment<T>
    Static String
    Dynamic T
end
```

The impl chooses `I`, what each `${...}` slot's value must be, and `R`, the literal's result. `I` may be an interface, such as `Display`, whose slot values each implement it in their own way, or a concrete type: `Fragment<String>` accepts only `String` slots. `from_fragments` is an ordinary function body.

```nomi
import
    std::io
    std::literals.{Fragment, Literal}
end

pub struct Box
    contents: String
end

impl Literal for Box
    fn from_fragments(fragments: List<Fragment<String>>) -> Box
        body = Iter.reduce(fragments, "", fn acc, frag ->
            case frag
                .Static(s)  -> acc + s
                .Dynamic(v) -> acc + v
            end
        end)

        Box{contents: body}
    end
end

fn main()
    name = "world"
    b = Box"hello ${name}"
    io.print(b.contents) // hello world
end
```

Neither `Literal`, `Fragment` nor `Fragment`'s variants are in the prelude. Only a handler's author names them, so they are not reserved names.

#### The prefix

The prefix is a type, found by ordinary name lookup, and its `Literal` or `CheckedLiteral` impl supplies the handler. Importing the type brings its impl, as it brings a `Display` impl. A domain type makes the literal read as a constructor (`Date"2026-05-04"`), and a zero-sized type gives a DSL a stateless prefix (`type Sql`, then `Sql"SELECT ..."`).

- A prefix that is not a type in scope is the error ``tag 'Nope' is not in scope — a typed literal `Nope"..."` requires `Nope` to be a type with an `impl Literal for Nope` block``.
- A type with no `Literal` impl is ``type 'Plain' has no typed-literal handler — write `impl Literal for Plain { fn from_fragments(fragments: List<Fragment<I>>) -> R }` ``.
- A slot whose type does not implement `I`, or is not `I`, is an error at the slot: `slot expression of type 'Int' does not match required type 'String' (required by tag 'Joiner')`.

**`Html` is a prefix the compiler knows.** std::html's `Html` has no `Literal` handler: the compiler reads an `Html` literal's text as an HTML template (§18 *HTML templates*), and the literal is an `Html`, never a `Result`. A type named `Html` declared anywhere else is an ordinary prefix, since a type's identity is its declaring file and name.

**One `from_fragments` per prefix.** A typed literal names no interface, so its prefix type may declare `from_fragments` only once. When another of its impls, or its inherent block, also declares `from_fragments`, the literal is an error at the prefix that names the rival declaration:

```console
typed literal 'Pick"..."' is ambiguous: `impl Other for Pick` also declares `from_fragments`, at line 16, and a typed literal has no way to say which one it means — rename one of them, or drop `impl Other for Pick`
```

The error is about the literal: two impls that both declare `from_fragments` are legal while nothing writes `Pick"..."`. An inherent `from_fragments` beside the impl is also an error at its own declaration, since a type's own function may not share a name with one of its impls' functions (§12 *Function Name Collisions*). The rule matches the call the literal stands for, which §12's ambiguity check already rejects.

#### Desugaring

A typed literal is a call of its prefix type's handler:

```text
<Type>.from_fragments(fragments: List<Fragment<I>>): R
```

Each `${expr}` slot becomes a `Dynamic(value)`, and the text between slots a `Static(text)`.
The list always alternates `Static, Dynamic, ..., Static`: the first and last `Static` are present even when empty. `Joiner"name: ${name}, age: ${age}"` is:

```nomi
Joiner.from_fragments(
    [Static("name: "), Dynamic(name), Static(", age: "), Dynamic(age), Static("")]
)
```

A triple-quoted typed literal strips its leading newline, common indentation and trailing newline as a triple-quoted string does, and the `${...}` and `\${` rules of strings apply inside a non-raw body (§3 *String Literals*). A raw (backtick) body has no slots: it is one `Static` holding the text as written.

The type of a typed literal with a `${...}` slot is `R`: `Date"2026-${month}-04"` is a `Result<Date, Error>`, built when the program runs. A literal without slots is checked at compile time, and when its handler returns `Result<T, E>` its type is `T` (below): `Date"2026-05-04"` is a `Date`. A checked literal's type is its `build`'s result with or without slots (§18 *Checked literals*), and an `Html` literal is always an `Html`. A `Regex` literal is the one whose type depends on its text: with no `${...}`, `Regex"(\\d+)"` is a `Regex<String>`, while `Regex"room ${n}"` is `Result<Regex<List<Option<String>>>, String>` ([std::regex](/reference/regex/#capture-types)).

#### A literal without slots is checked at compile time

Taught in [Typed Literals](/typed-literals/#a-literal-without-slots-is-checked-at-compile-time).

A typed literal with no `${...}` slot, written with double quotes, triple quotes or backticks, gives its handler fixed text. The compiler runs the handler while it checks the program. When the handler returns `Result<T, E>`, the literal's type is `T`:

- `Ok(v)`: the literal is that `T`. `Date"2026-05-04"` is a `Date` and ``Regex`\d+` `` is a `Regex<Unit>`; neither needs a `?`, and ``once letters = Regex`[A-Za-z]+` `` works at the top level of a file.
- `Err(e)`: the literal is a compile error carrying the handler's message (`e`'s `Display`, or its `Debug` when it has none): ``typed literal Regex`[` is invalid: error parsing regexp: missing closing ]: `[` ``.

A handler that returns `T` itself gives the type `T`, and it runs at compile time too. Any handler that reaches an effect (§19 *Effects*), reads an app field, reads a hash the run's seed keys (§5 *Hashing and keys*), traps, or does not finish within the compiler's step limit cannot be run at compile time, and the literal is an error that says so: ``typed literal Loud"x" cannot be checked at compile time: its handler has effects (Literal.from_fragments for Loud calls io.print)``, with the help ``a typed literal without `${...}` slots is checked at compile time; to build the value when the program runs, call one of `Loud`'s functions instead, such as its `parse`, and handle its result``. A text known only when the program runs goes through such a function, `Date.parse(text)`, or into a slot.

A `?` on a literal without slots is the ordinary error `` `?` requires a Result or Option, got Regex<Unit> ``, whose hint says to remove the `?`.

```nomi
import
    std::calendar.Date
    std::io
    std::regex.Regex
end

once letters = Regex"[A-Za-z]+"

fn main()
    digits: Regex<Unit> = Regex`\d+`
    io.print(String.find_all("order 66, aisle 7", digits)) // [66, 7]
    io.print(String.find_all("ab 12 cd", letters))         // [ab, cd]
    io.print(Date.year(Date"2026-05-04"))                  // 2026
end
```

When the program runs, a literal without slots still calls its handler, once per literal site: the first evaluation builds the value and later ones reuse it. The handler is pure and its input fixed, so the run gets the value the compiler got.

#### Checked literals

A type may implement `CheckedLiteral<I, T>` from std::literals instead of `Literal`:

```nomi
pub interface CheckedLiteral<I, T>
    fn check(statics: List<String>) -> Result<Unit, LiteralError>

    open fn check_type(statics: List<String>, arguments: List<String>) -> Result<Unit, LiteralError>
        _ = arguments
        check(statics)
    end
    fn build(fragments: List<Fragment<I>>) -> T
end
```

Its literals are checked literals. A checked literal's type is `T` in every quote style, with or without slots; it is never a `Result`:

- `check` runs while the program is checked, once per literal, on the literal's static text: the `n + 1` pieces around its `n` slots, in order, empty ones included. It follows the rules of a handler run at compile time: a `check` that reaches an effect (§19 *Effects*) is one error at the check, ``Url.check has effects (...); a checked literal's check runs when the program is checked``, so is one that reads an app field or a hash the run's seed keys (§5 *Hashing and keys*), and one that does not finish within the step limit is an error at the literal.
- What the compiler runs is `check_type`, whose default is `check`. It gets the statics and a description of the literal's type: each of its type arguments as Nomi spells it, in order, after the expected type solved them (§18 *Literals typed by their position*). A `Route<{id: Uuid}>` literal gets `["{id: Uuid}"]`, a `Url` literal `[]`. The description is text, not the types; nothing in it is a value of the type. A type whose text must agree with its type arguments, as a route's wildcards agree with its parameters, writes `check_type`; it follows `check`'s rules, and an effect in it is reported as `Route.check_type has effects (...)`.
- A generic type's impl, `impl CheckedLiteral<String, Route<P>> for Route<P>`, gives each literal fresh type arguments, which the expected type solves as it solves a generic call's.
- std::http's `Route<P>` is the one checked literal whose type follows from its text when nothing else gives it, as a `Regex` literal's captures do: with no expected type, or one that leaves `P` open once the arguments that solve it on their own are checked (§18 *Literals typed by their position*), `Route"GET /users/{id}"` is a `Route<{id: String}>`, a `String` field per wildcard (`{rest...}` included), and a route with none is a `Route<Unit>`. Its `check_type` holds the wildcards to `P`'s fields, ``the pattern has `{id}`, but `{post_id: Uuid}` has no field `id` `` at the wildcard, and the checker holds each field's type to `Parse` and `Display`: ``a route's parameters are read from its path and written into links as text: each field's type implements `Parse` and `Display`, and `Bool`, the type of `id`, does not implement `Parse` ``.
- An `Err(LiteralError{fragment:, offset:, message:})` is a compile error carrying `message` at the character `offset` code points into static `fragment`, counted in the text as `check` received it. An offset at a static's end points at the `${` of the slot after it; fragment 0 and offset 0 point at the literal.
- `build` runs when the program runs, gets the fragments `from_fragments` would get, and cannot fail: once `check` accepts the text, every slot value must build. A `build` that traps anyway traps where it runs.

A type implements `Literal` or `CheckedLiteral`, not both: ``type `Twice` implements both `Literal` and `CheckedLiteral`; a typed literal has one handler``. The orphan rule below applies to both.

std::url's `Url` is a checked literal, and its slot interface is `Display`: a slot's value is its `Display` text, escaped for where it stands, as one segment in the path, one key or value in the query, or the fragment. A `.` or `..` in the path is escaped too. A `Query` or a `Url` in a slot is its text like any other value, one segment or one value; `Url.with_query` and `Url.join_path` add pairs and segments. A slot in its scheme, user or host is a compile error at the slot, whatever its type: ``typed literal Url"https://${host}/greet" is invalid: a slot in the scheme, user or host cannot be escaped``.

```nomi
import
    std::io
    std::url.Url
end

fn main()
    name = "ann b"
    u = Url"https://api.example.com/greet/${name}"
    io.print(u) // https://api.example.com/greet/ann%20b
end
```

std::toml's `Toml` is a checked literal too. Its text, with a stand-in for each slot, must be TOML. A slot in value position (after `=`, or as an array item) is its value's TOML. The slot interface `ToToml` turns the value into a `Toml.Value` (a string, number, boolean, date or time, an array, or an inline table), and the literal writes that itself: a `String` quoted and escaped, a `List` as an array, a `Map<String, V>` as an inline table with quoted keys. No impl writes TOML text, so no slot can add a key. A slot in key position (at the start of a line, after a `.` in a dotted key, in a table header, or as an inline table's key) is one quoted key. A slot anywhere else is a compile error at the slot: ``typed literal Toml"user = \"${name}\"" is invalid: a slot cannot go inside a TOML string; write it where the whole value goes, as in `key = ${value}` ``.

```nomi
import
    std::io
    std::toml.Toml
end

fn main()
    user = "x\"\nadmin = true"
    io.print(Toml.text(Toml"user = ${user}")) // user = "x\"\nadmin = true"
end
```

A `?` or `!` on a checked literal is the ordinary error for a value that is not a `Result`, and a `?`'s hint says the literal is already its type: `` a `Url` literal is checked when the program is checked, so this one is already a Url: remove the `?` ``. std::literals::testing's `build_each` runs a type's `check` and then its `build` over test values, so a library's tests can hold `build` to its contract.

#### Literals typed by their position

A string literal with no tag, written where the expected type is a type with a `CheckedLiteral` impl, is that type's checked literal, as if the type's name were written before it. It is the rule a dot-variant follows (§4 *Variant Resolution*): the expected type the checker's outside-in walk finds names the type, and the literal is read as that type's. So an annotated binding, an argument, a struct field, a returned value, a list element and a `case` or `if` arm in one of those all give it.

```nomi
import
    std::io
    std::url.Url
end

struct Link
    label: String
    to:    Url
end

fn home() -> Url
    "https://example.com/"
end

fn main()
    name = "ann b"
    profile: Url = "https://example.com/users/${name}"
    links = [Link{label: "home", to: home()}, Link{label: "me", to: profile}]

    io.print(
        links
        |> Iter.map(fn l -> "${l.label}: ${l.to}")
        |> Iter.to_list()
    )
end
```

- The literal is checked as the tagged one is: its slots take the impl's slot type, `check` runs when the program is checked, and a refusal is an error at the place it names, labelled with the type: ``a `Url` literal "https://${host}/a" is invalid: a slot in the scheme, user or host cannot be escaped``.
- Only a literal converts. A `String` value where a `Url` is expected is the ordinary mismatch, `` argument 1: expected Url, got String ``.
- A checked literal, tagged or not, passed to a generic function where its parameter's type holds a type parameter that another argument solves on its own is checked after that argument, against the solved type. An argument solves it on its own when its type does not depend on its slot: a name of a value or function that is not generic, a lambda whose parameters are all annotated, or a call of a named function on such arguments whose result names no type parameter its parameters leave out. With `fn update(r: Request, p: {id: Int}) -> Response`, `Route.to_with("PATCH /todos/{id}", update)` checks the route as a `Route<{id: Int}>`, and so does `Route.to_with("PATCH /todos/{id}", auth(update))` with a middleware `fn auth<P>(handler: (Request, P) -> Response) -> (Request, P) -> Response`, through any number of layers. A lambda that leaves a parameter's type to the call, `fn r, p -> ...`, takes it from the literal, which is checked first, as written. The arguments still run in the order they are written. A literal piped into such a stage, `"PATCH /todos/{id}" |> Route.to_with(update)`, is typed the same way, though there only a name of a value or a call on such names counts, since the head is checked before the stage.
- With no expected type, or one with no `CheckedLiteral` impl, the literal is a `String`, as always. A `Literal` impl does not make a literal typed by its position, since such a literal is its handler's result, usually a `Result` built when the program runs.
- The literal keeps the form it was written in; `nomi fmt` does not add or remove a tag.

#### Coherence

A handler is an ordinary interface impl, so the impl rules apply (§12). There is at most one `Literal` or `CheckedLiteral` impl for a type in a program, and the orphan rule requires the interface or the prefix type to be declared in the implementing package. Unlike other impls (§12 *Foreign Types*), a `Literal` impl is not admitted by a type argument the package declares: `impl Literal for List<Mine>` is an orphan impl even when `Mine` is the package's, since the handler is chosen by the prefix alone.

#### HTML templates

An `Html` literal, with std::html's `Html` as its prefix, is a fragment of HTML that the compiler reads when it checks the program. It is zero or more nodes: text, elements and comments, with an optional `<!DOCTYPE html>` at its start. Every element opened in the literal is closed in it.

A small fragment is usually written on one line with double quotes, its attributes quoted with `'`, since a `"` ends the literal. Markup of several lines is triple-quoted:

```nomi
import
    std::html.Html
    std::io
end

fn badge(count: Int) -> Html
    Html"<span class='badge'>${count}</span>"
end

fn comment(author: String, body: String, likes: Int) -> Html
    Html"""
    <article class="comment">
      <p>${author} ${badge(likes)}</p>
      <p>${body}</p>
    </article>
    """
end

fn main()
    io.print(Html.to_string(comment("Bo & Co", "<b>hi</b>", 2)))
end
```

The program prints the comment with its text escaped:

```text
<article class="comment">
  <p>Bo &amp; Co <span class='badge'>2</span></p>
  <p>&lt;b&gt;hi&lt;/b&gt;</p>
</article>
```

The template language is HTML with stricter rules:

- Every non-void element has an explicit end tag (`</p>`, `</li>`, `</td>`, which HTML lets a page leave out).
- A `/>` closes only a void element (`<input/>`) or an element inside `<svg>` or `<math>`.
- An element's name is an HTML, SVG or MathML name, or a custom element's name, which has a hyphen (`el-dropdown`). Attribute names are not checked.
- An attribute value quoted with `'` is read as one quoted with `"`: the same contexts, escaping and errors.

Each `${...}` slot has the context of where it stands, which decides how its value is written and which type it must have:

| Context | Example | The value must be | Written as |
|---|---|---|---|
| Element content | `<p>${x}</p>` | `ToHtml`, or another `Display` | a `ToHtml` value by its `to_html`: `String` and numbers escaped as text, `Html` as it is, `List<Html>` joined, an `Option` of a `ToHtml` value as that value and `None` as nothing; another `Display` value's text escaped |
| Text of `<title>` or `<textarea>` | `<title>${t}</title>` | `Display`, or an `Option` of one | escaped text; an `Html` is refused, since its markup could end the element |
| Quoted attribute value | `class='a ${c}'`, `data-reaction="${kind}"` | `Display`, or an `Option` of one | escaped for an attribute: `&`, `<`, `>`, `"` and `'` |
| URL attribute, the start of its value | `href="${link}"` | `UrlValue` (`Url`, `SafeUrl`, an `Option` of one) | a `Url` whose scheme is not `http`, `https`, `mailto` or `tel` as `about:invalid#unsafe-url`; then escaped |
| URL attribute, after text | `href="/posts/${id}"`, `?q=${q}`, `#${f}` | `Display`, or an `Option` of one | percent-encoded as a path segment (`/` and `:` included), a query value or a fragment, then escaped |
| `style` attribute | `style="width: ${pct}%"` | `CssValue` (`Int`, `Float`, `Decimal`, `Css`, an `Option` of one) | a number as written; a `Css` as it is; escaped |
| A whole unquoted value | `checked=${on}`, `title=${tip}` | `OptionalAttribute` (`Bool`, `Option<String>`) | `True` writes `checked`; `False` and `None` leave the attribute out; `Some(s)` writes `title="..."` escaped |

The URL attributes are `action`, `background`, `cite`, `formaction`, `href`, `icon`, `longdesc`, `manifest`, `poster`, `profile`, `src`, `usemap`, `xmlns`, and `data` on `<object>`.

A value of the wrong type is an error at the slot that says what the slot takes: `<title>${page}</title>` with an `Html` is ``this slot takes a `Display`, and an `Html` is not one``, with help for the context.

In element content, a type that implements `ToHtml` renders itself, even when it is also `Display`. Std's `Option`, `Result`, collections and tuples are `Display`, but their text is not the value's, `Some(x)` or `[a, b]`, so element content refuses one that is not `ToHtml`: `<p>${names}</p>` with a `List<String>` is ``element content does not show a `List<String>` ``, with help to render the elements with `Html.each`. A value of a type parameter is taken as a `ToHtml`.

A slot shows a part of the page only when a condition holds by holding an `Option`: `${if active then Some("active") else None}` is `None` when `active` is false (§8 *`if`/`else`*). A fragment over several lines is wrapped by a `|> Some()` on the line after its closing `"""`, with `else None` below it. In a whole unquoted value an `Option` writes the attribute or leaves it out. In every other context it writes its value as that context writes one, or nothing for `None`, so an `Option`'s text, `Some(...)`, is never written:

```nomi
import
    std::html.Html
    std::io
end

fn inbox(unread: Int, active: Bool) -> Html
    count = if unread > 0 then Some(Html" <b>${unread}</b>") else None

    Html"<a class=${if active then Some("active") else None}>Inbox${count}</a>"
end

fn badge(label: String, urgent: Bool, count: Option<Int>) -> Html
    Html"<span class='badge ${if urgent then Some("urgent") else None}' data-count='${count}'>${label}</span>"
end

fn main()
    io.print(Html.to_string(inbox(2, True)))
    io.print(Html.to_string(inbox(0, False)))
    io.print(Html.to_string(badge("new", True, Some(3))))
    io.print(Html.to_string(badge("old", False, None)))
end
```
```text
<a class="active">Inbox <b>2</b></a>
<a>Inbox</a>
<span class='badge urgent' data-count='3'>new</span>
<span class='badge ' data-count=''>old</span>
```

The rule goes by the slot's type when the program is checked: a value whose type is a type parameter or `Display` writes its `Display` text, `Some(...)` included, when it holds an `Option`. In a context that takes a `Display`, an `Option` of an `Option` is an error at the slot, ``this slot does not show an `Option<Option<Int>>` ``, with help to flatten it with `Option.flatten`. `None` in a URL attribute's value writes an empty value, `href=''`, which is a link to the page itself.

A slot anywhere else is an error at the slot:

| Where | Error |
|---|---|
| A tag or attribute name | ``a slot cannot name a tag or an attribute: `<${tag}>` `` |
| Part of an unquoted value | ``quote the attribute's value to put a slot in it: `class="...${...}..."` `` |
| An unquoted URL or `style` value | ``quote `href`'s value to put a slot in it: `href="${...}"` `` |
| An event handler attribute | ``a slot in `onclick` would be JavaScript; pass the value in a `data-` attribute and read it in a script`` |
| Inside `<script>` or `<style>` | ``a slot in a `<script>` element would be JavaScript; pass the value in a `data-` attribute`` |
| Inside a comment | `a slot in an HTML comment is not rendered safely` |
| In `srcset` | ``a slot in `srcset` is not supported; build the attribute from `SafeUrl` values`` |
| A URL's scheme or host | ``a slot in a URL's scheme or host cannot be escaped: `href="https://${host}/"` `` |

Malformed markup is an error at the place it is noticed: `` `<div>` is not closed in this literal ``, `` `</span>` closes `<p>`, opened at line 4 ``, ``a `/>` does not close `<div>` in HTML; write `</div>` ``, ``unknown element `<dvi>`; a custom element's name has a hyphen``.

A raw body, ``Html`...` ``, is a template with no slots under the same rules.

**The value.** Static text is written exactly as it stands, after a triple-quoted literal's indentation strip; no whitespace is collapsed. An `Html` built from templates, `Html.text`, `Html.each` and `Html.concat` is balanced markup, because each template was checked and each slot's value was escaped or was itself such an `Html`. `Html.trusted(markup)` is the one way to insert markup unchecked, for HTML a trusted renderer produced, such as Markdown.

**How it is built.** The literal is read once, when the program is checked; nothing is parsed when it runs. It stands for a call of std::html's `assemble` over the static text and one call per slot. The slot's context picks that function, and in element content the value's type does too (`slot_text` for a `ToHtml`, `slot_text_display` for another `Display`), as it does for an `Option` in a context that takes a `Display` (`slot_attribute_option`); the function's parameter type is the slot's type rule:

```nomi
html.assemble(["<p class=\"", html.slot_attribute(c), "\">", html.slot_text(x), "</p>"])
```

The slot expressions are the program's own, so hovering and errors inside a slot are at the source.

### Keyword declarations

A top-level declaration that starts with a keyword and a name is a **keyword declaration**:

```text
[pub] keyword name [(params)] [-> type] [raw body]
```

Each part after the name may be left out, and the body is a backtick string. Two identifiers in a row at the top level of a file always start a keyword declaration (`foo bar` is ``unknown declaration keyword `foo` ``), and a line break between them ends a statement. The keyword decides what the declaration means: `phrase` and `noun` (*Phrases* below) and `embed` (*Embedded files* below) are the keywords Nomi knows.

| Violation | Error |
|---|---|
| Another keyword | `` unknown declaration keyword `database` `` |
| `message` | ``unknown declaration keyword `message` ``, with a help naming `phrase` |
| A type name as the name | `` the name of a `phrase` is snake_case, not the type name `Hi` `` |
| A name before a type name | `` `strct` declares nothing: a type is declared with `struct`, `enum` or `type` `` |
| A tag on a `phrase` body | `` a `phrase` body is MessageFormat 2 text and takes no tag: remove `Mf2` `` |
| A result type on a `phrase` | ``a `phrase` declaration returns a `Phrase`, so it is written with no `-> String` `` |

Nomi has no macros. Code a library would generate from data, such as functions from SQL queries, is written by a tool the library ships into ordinary `.nomi` files that a program imports and commits (`nomi tool`, [tooling reference](/tooling/commands/); taught in [Generating Code](/generating-code/#a-tool)).

### Phrases

Taught in [Translating Text](/translating-text/#declaring-a-phrase).

Text a person reads is declared once, in the program's source language, with `phrase`, and translated in catalogs, one TOML file per locale and Nomi file. The text is [MessageFormat 2](https://www.unicode.org/reports/tr35/tr35-messageFormat.html) (MF2). Catalogs and `[i18n]` are in the [tooling reference](/tooling/projects/#i18n).

```nomi
/// How many items the cart holds.
pub phrase cart_items(count: Int) `
    .input {$count :integer}
    .match $count
    one {{{$count} item in your cart}}
    * {{{$count} items in your cart}}
    `
```

#### The declaration

A `phrase` declares a function of the same name whose parameters are the phrase's and whose result is a `Phrase` (`std::phrase`). It is called like any function of its file (`cart.cart_items(3)`) and takes a doc comment and `pub`. It writes no result type. Its body is the text in the source locale, `[i18n].source_locale`, English unless the manifest says otherwise.

A `Phrase` holds which phrase and its arguments, and no locale; `Phrase.format(phrase, locale)` writes it in one. Two phrases are equal when they are the same declaration with equal arguments, `Debug` shows `cart.cart_items(3)`, and `Phrase` implements `Localize`, so a phrase passed to another phrase is formatted in the same locale.

MF2 text formats with the stable functions `:string`, `:number`, `:integer`, `:percent` and `:currency`, the draft `:date`, `:time` and `:datetime`, and Nomi's own `:nomi:noun`, `:nomi:gender`, `:nomi:list` and `:nomi:weekday`. Each takes a parameter of the types and the options [std::phrase](/reference/phrase/#functions-in-a-text) lists, and anything else is a compile error at the placeholder:

- A field the value lacks is an error at the option, ``en: `hour` names a time of day, and `$day` is a `Date`, which has none``, and so are styles beside fields. `:date`, `:time` and `:datetime` select nothing: ``en: `:date` writes a calendar value and selects nothing``, whose help points to `:nomi:weekday`.
- `:nomi:weekday` only selects. A selector that names one day needs all seven, and a placeholder that writes it is ``en: `:nomi:weekday` selects by the day of the week and writes nothing``.
- A placeholder with no function, `{$name}`, writes its value with `Localize`, so its type must implement `Localize` (`Int`, `Float`, `Decimal`, `String` and `Phrase` do).
- An enum selected or written with `:string` reads as its variant's name (`Shipped`), whatever its `Debug` says.
- `:offset` and `:unit` are each `` `:offset` is not supported yet ``.

#### Nouns and agreement

Taught in [Translating Text](/translating-text/#nouns).

A sentence often agrees with a noun: in Polish, the possessive, auxiliary and participle of "Your book has shipped" follow the noun's gender. A `noun` declaration names such a thing as a `Noun`, a prelude type:

```nomi
pub noun book `.input {$count :integer} .match $count one {{book}} * {{books}}`

/// Sent when a parcel leaves the warehouse.
pub phrase has_shipped(product: Noun) `Your {$product :nomi:noun} has shipped.`
```

A `noun` takes no parameters and no result type. Its body is its form in the source locale, MF2 over one implicit `$count: Int`, how many things the text names. `shop.book` is the noun: a `Noun` a phrase takes as an argument and a struct may hold. Its catalog entry, in the catalog of the file's phrases, gives the `source` it was translated from, its `gender` where the language's nouns have one, and one form per grammatical case, each MF2 over `$count`:

```toml
[book]
source = '''.input {$count :integer} .match $count one {{book}} * {{books}}'''
gender = "feminine"
nominative = '''.input {$count :integer} .match $count one {{książka}} few {{książki}} many {{książek}} * {{książki}}'''
genitive = '''.input {$count :integer} .match $count one {{książki}} few {{książek}} many {{książek}} * {{książki}}'''

[has_shipped]
source = '''Your {$product :nomi:noun} has shipped.'''
text = '''
.input {$product :nomi:gender}
.match $product
feminine {{Twoja {$product :nomi:noun} została wysłana.}}
inanimate {{Twój {$product :nomi:noun} został wysłany.}}
* {{Twoja przesyłka została wysłana.}}
'''
```

A language's genders and cases are CLDR's grammatical features: Polish has `animate`, `inanimate`, `personal`, `feminine` and `neuter`, and seven cases. A language CLDR lists no cases for, English among them, has only the `nominative`, the form the declaration gives.

`{$product :nomi:noun}` writes the noun's form for `case` and `count` in the locale of the phrase's text, and `.input {$product :nomi:gender}` selects by its gender there. `{$product}` with no function, and `Localize`, write its `nominative` for one thing. A noun is formatted only through a phrase or `Localize`, never by `Display`. Two nouns are equal when they are the same declaration, and `Debug` shows `shop.book`.

The set of nouns is closed: every `Noun` is a declaration in a file the program reaches, so agreement is checked over the whole program, as coherence is. A name known only at run time, such as a title from a database, is a `String`, and passing one where a `Noun` is expected is the error `expected Noun, got String`, whose hint says a noun is a declaration. A phrase writes such a name through a `String` parameter, in a sentence that does not agree with it. A `noun` must be declared in a project whose source locale's language has no grammatical gender, since the declaration has no place to give one: `` `noun book` is declared in Polish, whose nouns have a grammatical gender, which a `noun` declaration has no place to give ``.

#### What the compiler checks

Taught in [Translating Text](/translating-text/#what-the-compiler-checks).

Each check is an error at the declaration, in its text, or in the catalog entry it is about:

1. **Completeness.** Every phrase has text in every locale `locales` lists: in that locale's catalog, in a listed ancestor's on its CLDR fallback chain (`pt-PT` may leave out what `pt` says), or because the source locale is that ancestor. A locale in `partial_locales` may leave a phrase out, and the phrase then shows in the source locale.
2. **No stray entries.** A catalog entry that names no phrase or noun of its file (``pl: `ghost` is not a phrase or noun of cart.nomi``), a catalog file whose Nomi file does not exist, and a catalog directory for a locale `[i18n]` does not list, or for the source locale.
3. **Parameters.** Every variable a text reads is a parameter or a variable the text declares (``pl: `$cnt` is not a parameter of `cart_items` ``), and the source text uses every parameter (``en: the source text of `m` does not use `$count` ``).
4. **Types.** Each function takes the types its row above names, and an unannotated placeholder's type implements `Localize`.
5. **Plural arms.** A plural selector has an arm for every category the locale's rules select for the operand's type, except `other`, which `*` covers: for an `Int`, English needs `one`, and Polish `one`, `few` and `many`. An English-shaped Polish text is ``pl: `cart_items` has no `few` arm; 2, 3 and 4 select `few` in Polish``. A key that is not one of the locale's categories, or a number, is an error. A `Float` selects no plural category, since its category depends on the digits it shows, which a `Decimal` keeps.
6. **Enum selectors.** A `:string` selector over an enum has an arm for each variant and no other key, as `case` does: ``en: `m` has no `Pending` arm; `.match $s` needs an arm for every variant of `Status` ``.
7. **Freshness.** An entry's `source` is the declaration's current text, compared as MF2 data models, so layout and declaration order do not count. A stale entry in `locales` is an error:

   ```console
   pl: `cart_items` was translated from an older English text
     was: {$count :integer} items
     now: {$count :integer} items in your cart
   help: update the translation, then accept it with the quick fix or `nomi phrases accept pl cart_items`
   ```

   In `partial_locales` a stale entry counts as missing, so the phrase shows in the source locale, never in a translation of an old meaning.
8. **MF2 validity**, including MF2's required variant whose keys are all `*`, kept even where the other arms cover every value, so catalogs stay valid for other MF2 tools.
9. **Agreement.** Every noun has a translation in every listed locale, as a phrase does, with its `source` and no `text`. Its entry has a `gender` exactly when the language's nouns have one, and it is one CLDR lists (``pl: `masculine` is not a gender in Polish``). Every other key is a case of the language (``pl: `genetive` is not a case in Polish, nor a key of a noun's entry``), the `nominative` is present, and each form has the plural arms the locale needs. A `case=` option names a case of the language (``en: `genitive` is not a case in English``). Over the whole program, every noun has each form some phrase in the locale asks for (``pl: `laptop` has no `genitive` form, which `sold_out` asks for``, at the noun's entry), and each gender selector has an arm for every gender some noun has there, naming the noun (``pl: `has_shipped` has no `personal` arm; the noun `staff.courier` is personal in Polish``). A gender selector in a language with no genders is ``en: English nouns have no grammatical gender, so `has_shipped` cannot select on `$product`'s``.

### Embedded files

Taught in [Building & Shipping](/building-and-shipping/#executables).

An `embed` declaration names files a program carries inside itself:

```text
[pub] embed name `pattern ...`
```

The compiler finds the files the patterns match when it compiles the program, and records each one's size and SHA-256, and `name` is a value of std::embed's `Files`, a read-only tree of them ([std::embed](/reference/embed/)). It is read like a `once` value (`site`, `web.site` from another file). A program reaches an embedded file only through the declaration that names it, and a built image holds the files of the declarations the program reaches.

**When a file is read.** The tree, each file's path, size and SHA-256, is in the program from the start: `paths`, `list`, `walk`, `metadata`, `exists`, `sum` and `sub` read no file, and `metadata` reports the Unix epoch as every file's modification time. A file's contents are read when the program reads that file (`read`, `read_text`), each time it does, and are kept only as long as the program keeps the `Bytes`. A built program holds the contents in its image, after the code it starts from, so `nomi build`'s executable, its WebAssembly modules and a `.nomic` read no file but their own, and starting one costs the same however much it embeds. `nomi run`, `nomi test` and the REPL read a file's contents from the file, and a file whose size or SHA-256 is not what the program was compiled with stops the program with an error naming the file (`static/a.css changed after the program was compiled`); the next run compiles the new contents. A build checks each file the same way when it writes it into the image.

```nomi
import std::embed.Files

/// The site's pages and styles.
pub embed site `
    static
    templates/*.html
    `

fn home() -> String
    Files.read_text(site, "static/index.html")!
end
```

The body is one or more patterns, separated by white space; a pattern that holds a space is written in double quotes (`"my notes.txt"`), which take no escapes. A pattern is a list of names separated by `/`, and names files from the package root, as an import path names a file (§14 *Modules and packages*), whatever directory the declaring file is in, so moving the file never changes what its patterns name. It never reaches outside the package. Each name of the pattern matches a name in the tree at the same depth, so no wildcard crosses a `/`:

- `*` matches any run of code points, the empty run included, and `?` matches one code point.
- `[...]`, a class, matches one code point it lists, and `[^...]` one it does not. A class lists single code points (`[abc]`) and ranges (`[a-z]`), each range holding both its ends and every code point between them. A class lists at least one entry. It cannot list `-` or `]`, since nothing escapes them; `*`, `?` and `[` inside one are ordinary code points, so `[*]` matches a `*`.
- Any other code point matches itself, a `-` or `]` outside a class included. `\` is an error rather than an escape, so a pattern reads the same on every system.
- A `[` with no `]` to close its class, an empty class, and a `-` that does not join a range's two ends make the pattern malformed.

What a pattern embeds:

- A pattern that matches a file embeds it, whatever its name. One that matches a directory embeds every file under it, except names that start with `.` or `_`; a pattern that starts with `all:` embeds those too (`all:static`).
- A directory holding a `nomi.toml` of its own is another package: a directory walk leaves it out, and a pattern that names it, or a file under it, is an error.
- A path in the tree is the file's path from the package root (`static/index.html`), whatever pattern matched it, and two patterns that match one file embed it once.
- A pattern whose base does not exist, from the package root or from the declaring file's directory, is a warning rather than an error, except in a build. The base is the path its names spell before the first one holding a wildcard, the whole pattern when none does: `web/dist` for `web/dist` and for `web/dist/*.js`. The tree holds the base as an empty directory, so `Files.sub(site, "web/dist")` is an empty tree, and a read under it returns `NotFound`. This is the state of a frontend's build folder before its first build: `nomi run`, `nomi check`, `nomi test`, the REPL and the language server report ``the `embed` pattern `web/dist` matches no file: `web/dist` does not exist, so `site` is empty until it does`` at the pattern and go on, and `nomi build` refuses it, for every target, so a built program holds the files its patterns name. A pattern that matches nothing in a base that exists (`static/*.png` in a `static` with no `.png`) is an error everywhere.

| Violation | Error, at the pattern |
|---|---|
| A pattern that matches nothing, in a base that exists, or in any base in a build | ``the `embed` pattern `static/*.png` matches no file`` |
| A leading or trailing `/` | ``the `embed` pattern `/static` starts with `/` `` |
| A `.` or `..` name, or an empty one | ``the `embed` pattern `../x` names `..` `` |
| `**` | ``the `embed` pattern `static/**` holds `**` ``: a directory's name already embeds everything under it |
| A `\` | ``the `embed` pattern `static\a` holds `\` `` |
| A malformed class | ``the `embed` pattern `static/[a` is not a valid pattern: a `[` has no `]` to close its class``; for `static/[]`, ``the class `[]` is empty``; for `static/[a-]`, ``a `-` in a class does not join two characters into a range`` |
| A directory with nothing to embed | ``cannot embed `empty`: it holds no file to embed`` |
| A symbolic link or other irregular file | ``cannot embed `link.html`: it is a symbolic link`` |
| Another package's directory | ``cannot embed `tools`: it is another package, with its own nomi.toml`` |
| No body, parameters or a result type | ``an `embed` names the files it holds`` |

**Literal paths are checked.** A call to `Files.read`, `read_text`, `metadata`, `list`, `walk` or `sub` whose path is a string literal, and whose tree is an `embed` declaration (`site`, `web.site`) or a `Files.sub` of one with a literal directory, unwrapped with `!` or `?` (`Files.sub(site, "static")!`, nested or piped), is checked against the files the declaration holds. `read` and `read_text` name a file, `list`, `walk` and `sub` a directory, and `metadata` either. The path is cleaned as the run time cleans it, so `./static//a.css` is `static/a.css`. A path built at run time (an interpolation, a variable, a parameter) is not checked and stays a run-time `Result`, and neither `exists` nor `paths` is checked. A literal at or under the base of a pattern whose base does not exist is not checked either, since its files are not there to check it against; the build refuses the pattern itself.

| Violation | Error, at the literal |
|---|---|
| No such file | `` `static/indx.html` is not embedded in `site` ``, with ``did you mean `static/index.html`?`` when one path is close |
| A directory where a file is read | `` `static` is a directory in `site`, not a file `` |
| No such directory | `` `public` is not an embedded directory of `site` `` |
| A file where a directory is read | `` `page.html` is a file in `site`, not a directory `` |
| A path under a `Files.sub` | `` `index.htm` is not embedded in `static` of `site` `` |
| A leading `/` | `` `/a.css` is not an embedded path: an embedded path is relative to the root of the embedded tree `` |
| `..` out of the tree | `` `../a.css` is not an embedded path: the path leaves the embedded tree `` |

## 19. Host declarations

### Host declarations

A `host` declaration gives a signature whose implementation is outside Nomi:

```nomi
/// An integer of any size.
host type Int

/// Returns the absolute value of n.
host fn abs(n: Int) -> Int
```

- `host type Name` declares a type with no visible fields or body. Its operations are `host fn` declarations or functions in `impl` blocks, and its derives and impls are sibling `derive` and `impl` declarations.
- `host fn name(params) -> R` declares a function with no body. It may also be an item of an `impl` block, where its host key is `module.Type.function`.
- Host declarations take doc comments like any declaration.

**`host` for the standard library and for embedders.** Every std declaration the runtime implements is a `host fn` or `host type`. Code outside Nomi is reached through the standard library, through a WebAssembly module std::wasm runs, or from a program that embeds Nomi and implements its `host fn` declarations (below).

#### `host` declarations and embedders

A Go program that embeds Nomi implements the `host fn`s its Nomi program declares from a host table it passes in ([the `vmhost` package doc](../vmhost/doc.go) covers the Go API). A program built as a WASI reactor has its `host fn`s answered by the program that loads the reactor, in any language, through the reactor's one import, `nomi.host_call` (the tooling reference's *Embedding*). There each argument crosses as JSON by its type's `ToJson` and the answer by the result's `FromJson`, as an `export fn`'s do the other way, so a `host fn` whose parameter is a function value, an opaque `host type` or another type with no JSON form has no JSON host and is answered only from Go.

- A plain `host type` in such a program is an opaque handle. The table's functions create and read its values; Nomi code passes them around and stores them. The type needs no registration.
- A table implements each function by its declaration's key: its name in the entry file, and in another file the file's path within its package, `/`-separated and without `.nomi`, then `.` and the name. `helpers/util.nomi`'s `host fn greet` is `helpers/util.greet`, a file at the package's root gives `util.greet`, and a dependency's file is under the dependency's name in the program: `lib/sub/thing.nomi` of the dependency `lib` gives `lib/sub/thing.ping`. Two files with one base name never share a key.
- A program's own host functions never implement a std call, nor the reverse, even when a file of the program has a std module's name: a project's `regex.nomi` may declare a `host fn` named `compile` beside `std::regex`'s.
- A `host fn`'s default is Nomi code like a `fn`'s, evaluated before the call leaves Nomi.

### Effects

A function is pure when its result depends only on its inputs and nothing else happens. Its inputs are its arguments and the app fields it reads (§15 *Scoped application fields*). An effect is a call to a host function that is not marked pure (below): output, files, the network, the clock, the system's entropy, tasks and channels. Reading an app field is not an effect, and neither is replacing one with `with`, which rebinds an input for the rest of a block as a local binding shadows another. An effect reached through a field's value still counts: a function that calls `App.logger` and so reaches std::io has std::io's effect.

Some code must be pure: a subtype's or validated type's rule (§4 *Subtypes*, §4 *Validated types*), a typed literal's handler run at compile time and a checked literal's `check` (§18 *Typed literals*), and an expression a debugger evaluates. The compiler runs the first three with no app: it calls a rule with the value it checks and a handler with the literal's parts, and has no other value to pass. So they may read no app field, as they may use no other value that exists only while the program runs: ``the rule of Bounded reads `App.limit`: a rule is checked with the value alone, and an app field exists only while the program runs``. A debugger's expression runs in a running program and reads the app fields in force where it stopped.

The standard library marks a std `host fn` pure when it computes its answer from its operands alone and acts on nothing outside the program. `String.uppercase`, `Map.get` and `Date.to_string` are pure. A function whose one call costs more as an operand's value grows, rather than its size, is pure and also marked unbounded, and is refused where evaluation must be bounded, as in all four places above, since a host function's call cannot be stopped partway: `String.repeat` and `Decimal.divide` are unbounded. A function whose answer is a hash keyed by the run's seed (§5 *Hashing and keys*) is pure and also marked seeded: `String.hash`, `Bytes.hash`, `Decimal.hash`, `Dynamic.hash`, and `Hashable.hash` of a value hashed by its shape. A call to one is no effect, and a debugger's expression may make one, but the first three places above refuse it, since the compiler runs them once and their answer must hold in every run. A function an embedder's host table implements is always an effect. Code reaches an effect when its body, or any function it may call, has one; a call through an interface counts every implementation of the method.

An effects list in `nomi.toml` names a program's effects, by the std module the program's code called (`std::fs`), and an empty list allows none. A `dbg` (§8 *`dbg`*) prints and is an effect, but no list names it, since `nomi build` refuses a program that holds one. Using a value already made, such as reading from a reader, sending on a channel end or awaiting a task, is named where the value was made: opening the file, making the channel, starting the task. A std::fs `Dir` is such a value: `fs.open_dir` is the call a list names `std::fs`, and a call through the `Dir`, which reaches only what is under its directory, is named by none. A call through an interface value is one such use when the implementation it reaches is std's and only std code makes values of its type: `Store.put` on a store from `storage.local` is named `std::storage` at the `storage.local` call, and names nothing where the store is used. What a call costs is not an effect, and neither is failing.

Reading the program's own embedded files (§18 *Embedded files*) is not an effect, since an `embed` declaration is no call. A `boot` that takes a `Startup` reads the environment and the arguments, and is named `std::startup`. A call is named by the std module the code called, not by a module that does the work underneath: `net.dial` is `std::net` though std::net dials through std::http's functions. A function an embedder's host table implements is named `host:` and its key (§19 *`host` declarations and embedders*), which only that embedder's own list can allow.

A program has an effect when code it can reach has one, whether or not a run takes that path. The check walks the program as `nomi build` walks it to choose what a build keeps, from its roots: `main`, the boot, the exported functions, the test cases, and each public function of the project's own files, with the `impl` bodies of each type a public function's code makes a value of, since the value can leave through the function. A public function is one another package can call: a `pub fn` at the top of a file or in an inherent `impl`, outside a test file, and each function of an interface's `impl` for a `pub` type that is not `opaque`. A dependency's public functions are not roots, so a dependency's code is held where the program reaches it. The walk counts a call in a branch that never runs; a function used as a value, whether or not anything calls it; every implementation of an interface function the program calls through the interface, std's included for a type whose values code outside std makes; the `Display`, `Debug`, `Equatable` and `Hashable` bodies of each type a value the program displays, inspects, compares or hashes may be or hold, at any depth, and every such body where the value's type does not say what it holds, as an interface value's does not; every hand-written `Debug` body a test's assertion may show; and a `once`'s initializer when the program reads it.

A public generic function runs at whatever types its callers give it, so it is read with each type parameter held as its bounds, whether or not anything instantiates it. A call through a bound counts every implementation in the program, as a call through an interface value does, including a function of the bound that takes no value of the type (`T.from_sql(v)`); a use of `Comparable`, `Debug` or `Display` through a bound counts every implementation, whatever file declares it. A type parameter with no bound is held as any value. A generic function it calls at its type parameters, or at a type made of them (`Option<T>`), is read the same way, and so is each function of an `impl` over a generic type and each generic function of any `impl`. Under a project's `[effects.self]`, a public generic function whose body cannot be read this way is an error at its name with the code `effects-not-determined` and the reason as its help.
