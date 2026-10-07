---
title: "FFI & Dynamic"
description: "How Nomi wraps Go packages and handles loose boundary values."
---

:::caution[Early FFI surface]
The binding syntax below is stable enough to build real programs on, and the
FFI fixtures in this repository do. What is still moving is the packaging around
it: how an adapter is laid out, how it is published, and what the diagnostics
say when it is wrong. Treat the conventions as a snapshot of the current
direction rather than a settled interface.
:::

Nomi modules can bind ordinary Go packages. The usual shape is:

- write Go adapter code in a normal `.go` file,
- declare a compile-time Go package handle with `gopkg`,
- bind Nomi declarations to Go symbols with `go handle.Symbol`,
- expose domain-shaped Nomi APIs instead of raw Go package details,
- use `nomi run`, `nomi check`, and `nomi test` as the workflow.

For app-specific boundaries, the adapter can live next to the Nomi module in
the same codebase: a small `.go` file that speaks to an existing app service,
vendor SDK, or platform API, with a Nomi file exposing the shape the rest of the
app uses. Common boundaries belong in the ecosystem: SQL, SQLite, HTTP, and
other standard integrations can be ordinary Nomi modules backed by Go adapter
code.

`gopkg` is not a Nomi import. It is a Go package handle used only by FFI
bindings:

```nomi
gopkg "urltools"

pub struct ParsedURL {
    scheme: String
    host: String
    path: String
    raw_query: String
    query: Map<String, List<String>>
}

pub fn escape_query(value: String): String go urltools.EscapeQuery

pub fn unescape_query(value: String): Result<String, String> go urltools.UnescapeQuery

pub fn parse_query(raw: String): Result<Map<String, List<String>>, String> go urltools.ParseQuery

pub fn parse(raw: String): Result<ParsedURL, String> go urltools.Parse
```

The corresponding Go file is regular Go:

```go
package urltools

import "net/url"

type ParsedURL struct {
	Scheme   string
	Host     string
	Path     string
	RawQuery string
	Query    map[string][]string
}

func EscapeQuery(value string) string {
	return url.QueryEscape(value)
}

func UnescapeQuery(value string) (string, error) {
	return url.QueryUnescape(value)
}

func ParseQuery(raw string) (map[string][]string, error) {
	return url.ParseQuery(raw)
}

func Parse(raw string) (ParsedURL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ParsedURL{}, err
	}
	return ParsedURL{
		Scheme:   parsed.Scheme,
		Host:     parsed.Host,
		Path:     parsed.Path,
		RawQuery: parsed.RawQuery,
		Query:    map[string][]string(parsed.Query()),
	}, nil
}
```

The Nomi signature is the contract. The Go function should project to that
contract through a small adapter rather than exposing raw Go package details.
At the binding boundary, Nomi generates the wrapper code that calls the Go
symbol and converts the common shapes: scalars like `String`, `Bool`, `Int`,
`Float`, `Byte`, and `Bytes`; containers like `List` and `Map`; a Go function's
extra return values as a tuple; optional and fallible shapes like `Maybe` and
`Result`; plain structs by matching exported Go fields to Nomi fields; and
opaque handles declared with `opaque type`.

Shapes outside that set should be normalized in the Go adapter. If Nomi only
needs to hold a Go-owned value and pass it back, declare an opaque handle. If
Nomi needs to inspect a loose value whose shape is not known at the signature,
cross it as `Dynamic` and decode it deliberately at the edge.

| Go boundary shape | Nomi surface | Notes |
| --- | --- | --- |
| `string`, `bool`, `int64`, `float64`, `uint8` | `String`, `Bool`, `Int`, `Float`, `Byte` | These are the exact widths; `Int` is Go's `int64`. |
| a narrower Go integer (`int32`, `int8`, …) | `Int` | Range-checked at the boundary, not truncated. A value that will not fit is an error, not a wrong answer. |
| `[]byte` | `Bytes` | A byte buffer, distinct from `List<Byte>`. |
| `[]T`, `map[K]V` | `List<T>`, `Map<K, V>` | Keys and values must also be supported shapes. |
| `time.Duration`, `time.Time` | `Duration`, `Instant` | The standard time types cross directly. |
| `*T` | `Maybe<T>` | Explicit opaque handles are passed as the handle type instead. |
| two or more return values | a Nomi tuple | `func(string, string) (string, string)` binds to `(String, String)`. |
| a trailing `error` | `Result<_, String>` | `(T, error)` becomes `Result<T, String>` and a lone `error` becomes `Result<Unit, String>`. The `Err` side carries the Go error's message. |
| exported Go structs | Nomi `struct` | Exported Go fields are matched to Nomi fields. |
| a `func(A) R` parameter | a Nomi function `(A) -> R` | Pass a lambda or a named function, and Go calls it. The Go function may return nothing (`(A) -> Unit`), one value, or a value and an `error` (`(A) -> Result<R, String>`). |
| Go-owned resources | `opaque type` | Use when Nomi should hold and return the value, not inspect it. |
| loose runtime values | `Dynamic` | Use when Nomi will decode or pattern over an unknown shape. |
| channels, non-empty interfaces, named function types | Go adapter or opaque handle | Normalize to a supported shape, or keep the value opaque. |

## Binding the Go standard library

A Go standard library package needs no adapter file and no `go.mod`. Bind its
functions directly:

```nomi
import std/io

gopkg "strings"

gopkg "strconv"

fn upper(s: String): String go strings.ToUpper

fn parse_int(s: String): Result<Int, String> go strconv.Atoi

fn main() {
    io.print(upper("hi"))
    io.print(parse_int("42"))
}
```

This runs from any directory, as a `.nomi` file or as a `#!` script. The Go
signature must still be one of the shapes above: a function that takes an
`io.Reader`, or a generic one such as `slices.Max`, needs a small adapter of
your own, like `urltools` above. A package internal to the standard
library, such as `internal/abi`, cannot be bound.

## Shipping a binding

A project with Go bindings keeps its Nomi files, its Go files, `nomi.toml` and
a `go.mod` together in one directory:

```
go-bindings/
├── nomi.toml
├── go.mod
├── main.nomi
├── urls.nomi
└── urls.go
```

The `go.mod` is an ordinary Go module file. It names your module and your own
Go dependencies, and nothing about Nomi:

```
module gobindings

go 1.27.0
```

Add a third-party Go package the usual way, with `go get` in the project
directory, and import it from your `.go` file.

Go bindings need the Go toolchain installed. The project's Go side is compiled
together with Nomi's own Go module: a released `nomi` uses the module at its
own version, which Go downloads like any other dependency, and a `nomi` built
from a clone uses that clone. Programs without Go bindings need no Go.

`nomi run`, `nomi test` and `nomi check` compile the Go side for you. The first
run of a project is slower while that happens; later runs reuse the result
until the project's Go code, its `go.mod`, or Nomi itself changes. Editing
`.nomi` files does not recompile it.

`nomi build` compiles the Go side into the executable it writes, so the
program runs without Nomi or Go installed.

The examples on this site run in your browser, which cannot compile Go, so
examples that use Go bindings are shown here but do not run.

## Dynamic Values

Some boundary values do not have a known Nomi shape: plugin messages, decoded
payloads, loosely typed configuration, or data from another runtime.
[`Dynamic`](/reference/dynamic/) is the escape hatch for that case.

Use it at the edge, then decode deliberately when the expected shape is known.
Every navigator and extractor in [`std/dynamic`](/reference/dynamic/) returns a
`Result`, and the error side is a structured
[`DecodeError`](/reference/dynamic/#struct-decodeerror) carrying the path to
the failure along with what was expected and what was there, so a bad payload
tells you *where* it was bad.

When a format has a typed representation, prefer that. For JSON,
[`std/json`](/reference/json/) decodes into a [`Json`](/reference/json/#enum-json)
enum you pattern-match directly with `case`, which keeps the shape in the type
system instead of in a decode chain.

`Dynamic` is useful for crossing messy boundaries. It should not become the
default way to model data inside Nomi.

---

That's the tour. You've seen Nomi's core syntax
([Bindings & Expressions](/bindings-and-expressions/),
[Functions](/functions-and-lambdas/), [Pipes](/pipes/)), its data story
([Scalars](/scalars-and-strings/), [Collections](/collections/),
[Structs/Enums](/structs-enums-distinct/),
[Pattern Matching](/pattern-matching/)), its abstraction layer
([Interfaces](/interfaces-and-dispatch/), [Modules](/modules-and-imports/)),
its distinctive features ([App Fields, Defer](/capabilities-and-context/),
[Concurrency](/concurrency/), [Dates & Times](/dates-and-times/),
[Typed Literals](/typed-literals/)), and how it talks to host programs.
The [Standard Library](/reference/) reference is generated from the stdlib's
own docs.
