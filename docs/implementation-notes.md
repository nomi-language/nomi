# Nomi implementation notes

How parts of the language in [`spec.md`](spec.md) are built. A language user
does not need any of this; the spec says what the language does, and this file
says where and how the implementation does it. [`AGENTS.md`](../AGENTS.md)
describes the pipeline as a whole.

## Formatter: brace-bounded field constructs

The formatter renders anonymous struct types by width: short shapes stay flat
(`{name: String, age: Int}`), and long shapes break onto multiple lines with
each field indented and trailing-commaed. The break decision happens at the
anonymous struct itself, so a long parameter annotation breaks its own
anonymous struct rather than just spilling the parameter list. The same rule
applies to struct-shaped enum variant payloads (`HttpError {status: Int, ...}`)
and to struct literals (named `Point{x: 3, y: 4}` and anonymous
`{x: 3, y: 4}`), since the surface syntax is identical. An anonymous struct
nested inside another type expression also breaks at its own column:
`#(String, {long anon})`, `List<{long anon}>` and
`Map<String, List<{long anon}>>` all keep the outer container inline and flow
the inner anonymous struct multi-line. The one exception is an anonymous struct
inside a function type's parameter list (`({long anon}) -> R`): the function
type wraps its parameter list in a Group so the whole signature including
`-> R` is fit-checked as one unit, so a too-long signature breaks at the
parameter list rather than inside the anonymous struct, which then sits flat at
the inner indent.

Fits are tail-aware: every Group's flat-or-broken decision considers the rest
of the line that follows it, not only the Group's own content. A struct literal
whose flat form fits at its column but whose trailing siblings push the line
past the width breaks, instead of staying flat and overflowing. The rest of
the line ends at the first place it may break, and at the `{` of a
construct kept multi-line (below), so `A{a} + B{` measures the same whether
`B{...}` was broken for width or kept broken from the source. Trailing
comments (`x = 1 // explanatory note`) are invisible to fits, so the formatter
does not break code to make room for a comment; the comment can run past the
width instead.

Anonymous struct types and struct-shaped variant payloads keep a multi-line
shape: if the source had fields on more than one line, the multi-line shape is
preserved even when it would fit flat. This is the same rule as for `fn`
bodies: typing a brace, pressing enter and saving does not collapse the layout
just made. Literals follow Prettier's rule for objects instead (below): a line
break right after the opening bracket keeps one broken.

The preserved shape is local; it does not force the surrounding constructs
(parameter lists, call argument lists, struct literals containing it) to
break, so a call can hug it (`f(Point{` on one line). The exception is a
surrounding call argument list or parameter list already written one element
per line, its first element on a line below its opening bracket: it stays that
way when it holds an element that breaks whatever the width. Otherwise a list
broken for width would hug its literal on the next format. The same holds for
an or-pattern written one alternative per line, the short form of a block of
one statement (`if`, `else`), and a case arm's body below its `->`
(`keepsBrokenLines` in `internal/format/list_comments.go`).

## Formatter: a line break after a literal's opening bracket

A list, vector, set, map, tuple or struct literal, and a list, map or struct
pattern, whose source has a line break between its opening bracket and its
first element stays broken, one element per line, even when it would fit on
one line (`brokenAfterOpen` in `internal/format/list_comments.go`). It is
Prettier's rule for objects, and the author joins the literal again by
deleting that one line break. Only that break counts: `[1,\n 2]` and
`Point{x: 1,\n y: 2}` join when they fit. The parser records the line of the
opening bracket on each literal (`Line`), and the formatter compares it with
the line its first element starts on, the `..` of a leading spread included,
so the flag needs no field of its own. The broken form puts the first element
on the line below the bracket, so the next format finds the same break and
the layout is stable. Without that break the width and depth rules decide. A
literal written with a spread last (`[a, ..rest]`, a list pattern with a
`..rest`) is written on one line as before, and every other bracketed form
(call arguments, parameter lists, type argument lists, pipes) is laid out by
width alone. A kept-broken literal is local, as a struct literal written
across lines is, so a call hugs it (`f([` on one line).

A kept-broken list, vector or set whose elements are all number literals is
filled (below), broken even when it fits: the fill's own output breaks after
the bracket, so a list filled for width stays filled. A list of anything else,
strings included, is written one element per line, so a board written one row
per line keeps one row per line.

A lambda body written in braces whose first statement starts on a line below the `{` stays a block,
each statement on its own line, however short; this is Prettier's rule that
a line break after an object literal's `{` keeps it expanded. A braced body
whose contents start on the `{` line (`fn x -> { x + 1 }`) and a bare body
(`fn x -> x + 1`) are laid out by width, one expression going on the
parameters' line without braces when it fits. When it does not fit, the
formatter writes the body as a braced block below the parameters, as
rustfmt writes a closure, wherever the lambda stands: a call argument, a
named argument, a pipe or `then`/`tap` stage, a binding, a struct field or
a list item (`openBraceWhenBroken` and `closeBraceWhenBroken`, inside the
lambda's own Group). An `if` that holds `break`, `continue` or `return`
always goes there, broken. The parser builds the same tree for
`fn x -> { e }` and `fn x -> e`, a `Block` holding one `ExprStmt`, and records the
`{` line and the first statement's line, which is all the formatter reads
(`lambdaKeepsBlock` in `internal/format/emit.go`). So the braces the
formatter adds stay on later runs, even after the body shrinks to fit the
parameters' line; delete the line break after the `{` to let it go back.
A block as a call's last argument hugs its `)` (`})`).

## Formatter: filled literal lists

A list, vector (`#[...]`) or set (`#{...}`) literal too wide for its line
breaks after its opening bracket. When every element is a number literal
(an Int, Float or Decimal literal, alone, after a unary `-` or in
parentheses the formatter drops), the formatter then fills: it packs as
many elements on each line as fit the width, each followed by `,` (the
last one too), and puts the closing bracket on a line of its own. Any other
element keeps the one-element-per-line layout: a string, codepoint or
boolean (a list of those is usually read one element at a time, as
messages or the rows of a grid), a name, a dot variant (`.Red`), a call, a
compound literal or a lambda. So does a spread, a comment anywhere in the
list, and every map literal. The packing is greedy, measured in display
columns (`textWidth`), with no alignment into columns; an element wider
than a line takes a line of its own. It is decided when the list renders,
from the indent its elements start at (`filledList` in
`internal/format/fill_list.go`), so it depends on the syntax tree, the
list's position and whether the source breaks the line after its opening
bracket, which keeps it broken and filled even when it fits. The derive and
impl conformance lists pack their entries with the same function
(`packGreedy`).

## Formatter: compound depth

A list, vector, set, map or struct literal, or a list, map or struct
pattern, three compound levels deep breaks one element per line even when it
fits, since three levels of brackets on one line are hard to match up
(`maxCompoundDepth` and `compoundDepth` in `internal/format/emit.go`). Each
of those literals and patterns adds a level. A call (`Ok(...)`), an enum
pattern's payload, a tuple, an `as` or `or` pattern and parentheses add
none but pass their contents' depth through, so `[#("a", #[#[0, 1]])]` is
three deep. Only the literal or pattern that reaches the depth breaks for
it: its elements are laid out on their own merits, by width and by their
own depth, so `[R{p: P{x: 0, y: 0}, n: 1}]` breaks the list and keeps
`R{p: P{x: 0, y: 0}, n: 1}` on one line inside it.

## Formatter: comments in lists

A comment inside a bracketed, comma-separated list belongs to one of its
items (`ast.ItemComments`): a comment on its own line, or after the opening
bracket, goes above the next item (`Before`); one ending the line of an item
or its comma goes after that item's comma (`Trailing`); one on its own line
after the last item goes below it (`After`). A list item that is a node keeps
them on its `TriviaCarrier.ItemComments`; a parameter, type parameter and
struct pattern field have a `Comments` field. A map entry's comments are its
key's, and a list whose node has an `EndTrivia` (list, vector, set, map and
struct literals, list and map patterns) keeps the comments before its closing
bracket there. A call or parameter list with no item keeps its comments in
`Call.EndTrivia` or `ParamsEndTrivia`. The parser's `listComments` places
them as each list's loop walks it (`internal/parser/list_comments.go`).

The formatter writes a list holding a comment one item per line
(`emitCommentedList`), each comment where it was, and with no trailing comma
where the list's syntax takes none: a lambda's parameters, type arguments
and type parameters, a tuple or variant pattern, a tuple destructure, a list
ending in a spread, and a call with one argument. Parentheses around an
expression holding a comment stay, the comment inside them. A lambda header
whose only comments each end the line of a parameter other than the last
keeps its own layout, the first parameter on the `fn` line.

## Formatter: comments with no slot

The parser keeps most comments as trivia on the construct they sit in: a
statement's leading and trailing comments, the comments before a closing
`}` (`EndTrivia`), a struct field's leading comments, a list item's comments,
and so on. A comment anywhere else, such as after a `with` statement's `=`,
after a list spread's `..` or inside the parentheses around a pattern, has no
slot of its own. `parser.ParseFile` gives each such comment to the node
nearest it (`attachDangling`): inside the innermost node whose extent
contains the comment, it goes after the last node that ends before the
comment (`DanglingAfter`), else before the first node that starts after it
(`DanglingBefore`). The formatter writes a comment before a node on the line
above it when the node starts a line, and otherwise at the end of the line (a
`LineSuffix` document, written just before the next line break). A comment
after a node goes at the end of the line the node ends on. A comment that
cannot end its line, because the line already ends in a comment or in an
opening bracket or `|`, goes on a line of its own above the next line of
code, at that line's indent when it follows an opening bracket, where it
reads back as the first item's comment. So `with App.logger = // why` then
`Silent` becomes `with App.logger = Silent // why`.

## Iteration callbacks

The closed set of functions whose callbacks accept `break` and `continue` is
listed in `internal/analysis/iter_sensitive.go`, and the analyzer rejects the
keywords anywhere else.

## Unimplemented syntax

A generic `typealias Name<T> ...` stops in the parser. The AST has no field for
a type-parameter list: `ast.TypeAlias` carries `Name`, `Public`,
`TargetTypeExpr` and `Bounds`.

## `or` patterns

`A or B` is `ast.OrPattern`. The builder makes each name the alternatives
bind one symbol, defined at every alternative's site
(`builder.bindPatternName`), and the checker compares the alternatives' name
sets and types (`internal/analysis/or_pattern.go`). Coverage expands an `or`
into its alternatives in each column of the constructor matrix.

The IR builder tests the alternatives in order against one value
(`internal/irbuild/irorpattern.go`). An alternative that matches copies each
name into a join temporary and jumps to the pattern's success block, where
each name gets its one `ir.Bind`; so an arm's guard and body are lowered once.
A parameter's straight-line destructuring prologue cannot branch, so it holds
the value an `or` inside a parameter matches, and the body's builder tests it
before the body.

## Go FFI wrapper

`nomi run`, `nomi check` and `nomi test` on a program with Go bindings scan its
`.nomi` files for `gopkg` handles and `go package.Symbol` bindings, and
generate a cached Go wrapper that imports the referenced Go packages and holds
a host adapter for each bound declaration, keyed by its Nomi key
(`internal/ffirun`). `nomi run` and `nomi test` then run the program on the VM
inside the wrapper, which answers a crossing into Go through that adapter;
`nomi check` stops after load and analysis. Pure-Nomi programs and tests run
in-process with no wrapper. AGENTS.md describes the wrapper's `go.mod` and its
cache directory.

A bound Go standard library package is read from the source of the toolchain
`go env GOROOT` names, with that toolchain's build constraints for this
platform, so a binding to `strings.ToUpper` is validated and adapted like one
to the project's own package. It needs no `go.mod`: a project with none gets a
wrapper that is its own module. The toolchain's version is part of the cached
wrapper's key whenever a binding names a standard library package
(`internal/ffirun/gostd.go`).

## Standard-library adapters

A Go-backed std module keeps its public import, such as `std/regex`. The
facade is the flat `std/regex.nomi` like every other stdlib module; its Go is a
package in the compiler's module (`internal/stdregex`), reached through
`internal/stdlibbindings` rather than through a generated wrapper. The stdlib
is one Nomi module, an adapter is not separately distributable, and it
therefore carries neither `nomi.toml` nor `go.mod`.

## Why std uses `host` rather than `go`

`host` means the implementation is outside Nomi and the standard library owns
it. Where the Go code sits is a layout question the binding table
(`internal/stdlibbindings`) answers, not something the declaration spells.
That is OCaml's arrangement under one keyword rather than two:
`external length : string -> int = "%string_length"` marks a compiler
primitive and `external open : ... = "unix_open"` marks a C stub, both
`external`, with the symbol saying which.

The implementation sits in one of two places:

- **`rt`**, the runtime library the VM runs over. The language primitives:
  `String.trim`, `Map.get`, `Int.add`, `Bytes.length`, the `Iter` adapters.
- **A package in the compiler's module**: `internal/stdcalendar`,
  `internal/stdio`, `internal/stdregex`, `internal/stdrandom`,
  `internal/stdstrings`, `internal/stdcompiler`. These wrap a library or an OS
  facility, and keeping them out of `rt` puts their dependency and their
  binary size on the programs that ask for them. `std/calendar` is why a
  hello-world artifact contains no `time/tzdata`.

Why std does not use the `go` spelling:

- **It would need a Go toolchain.** A `go`-bound module makes `internal/ffirun`
  stage and build a wrapper, so a program importing it needs a Go toolchain;
  on a machine without Go, `nomi run` fails with
  `exec: "go": executable file not found`.
  `internal/ffirun/hostkeyword_toolchain_test.go` asserts that no stdlib import
  takes that path.
- **It pays the FFI projection on every call.** An `rt`-backed `host fn` is
  called with the VM's own values; a `go` binding goes through the FFI
  projection, which converts each argument into a Go value and the result
  back. `Bytes` crossing as `[]byte` makes an O(1) length read cost an O(n)
  copy.
- **`rt`'s own types do not project.** `rt.Bytes` is a Go string, so the
  boundary would read it as a Nomi `String`, and `rt.Maybe[rt.Byte]` is a
  struct, so a `Maybe<Byte>` return would arrive as a record of its Go fields.
  `rt` chose its representations for a path that has no projection.

A user's `go` binding pays the same projection, which is correct: it crosses a
real FFI boundary into code the compiler does not own.
