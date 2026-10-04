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
`(String, {long anon})`, `List<{long anon}>` and
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
past the width breaks, instead of staying flat and overflowing. Trailing
comments (`x = 1 // explanatory note`) are invisible to fits, so the formatter
does not break code to make room for a comment; the comment can run past the
width instead.

All four brace-bounded field constructs (anonymous struct types, struct-shaped
variant payloads, named struct literals, anonymous struct literals) keep a
multi-line shape: if the source had fields on more than one line, the
multi-line shape is preserved even when it would fit flat. This is the same
rule as for `fn` bodies: typing a brace, pressing enter and saving does not
collapse the layout just made. The preserved shape is local; it does not force
the surrounding constructs (parameter lists, call argument lists, struct
literals containing it) to break.

## Iteration callbacks

The closed set of functions whose callbacks accept `break` and `continue` is
listed in `internal/analysis/iter_sensitive.go`, and the analyzer rejects the
keywords anywhere else.

## Unimplemented syntax

A generic `typealias Name<T> ...` stops in the parser. The AST has no field for
a type-parameter list: `ast.TypeAlias` carries `Name`, `Public`,
`TargetTypeExpr` and `Bounds`.

There is no partial implementation of `or` between patterns: no
alternative-pattern node exists in `internal/ast`, `internal/parser` or
`internal/analysis`.

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
