---
title: "Calling Go"
description: "Call functions from Go packages, the Go standard library's included, from Nomi."
---

:::caution[Early FFI surface]
You can build real programs on the binding syntax below, and Nomi's own
FFI tests do. What may still change is the packaging around it:
how you lay out and publish a Go adapter, and the error messages you get
when something is wrong.
:::

A Nomi module can call functions from ordinary Go packages. The usual steps:

- Work in a project: a directory with a `nomi.toml` and a `go.mod`, whose
  module path names your Go code. `nomi new <module path>` starts one in a
  new directory, and `nomi init <module path>` makes the current directory
  one. A binding to the [Go standard library](#binding-the-go-standard-library)
  needs no project.
- Write Go adapter code in a normal `.go` file.
- Name the Go package in your Nomi file with `gopkg`.
- Bind each Nomi function to a Go function with `go package.Function`. Each
  Go type maps to a Nomi type: see [Go and Nomi types](#go-and-nomi-types).
- Give the rest of your Nomi code functions and types that fit your
  program, rather than the Go package's own shapes.
- Work with `nomi run`, `nomi check` and `nomi test` as usual.

The adapter can live next to your Nomi code: a small `.go` file that talks
to a service your app already has, a vendor SDK, or a platform API. Common
integrations such as SQL, SQLite and HTTP can be shared Nomi modules backed
by Go adapter code in the same way.

`gopkg` is not a Nomi import. It names a Go package, and only `go` bindings
use that name. Here `urls.nomi` binds the functions of the Go package in
`urltools.go`, which is regular Go, and `main.nomi` calls them like any
other Nomi functions:

```nomi-run
// FILE: main.nomi
import {
    std::io
    urls
}

fn main() -> Result<Unit, String> {
    url = urls.parse("https://example.com/search?tag=go&tag=nomi&q=hello+world")?

    tags =
        url.query
        |> Map.get("tag")
        |> Maybe.with_default([])

    io.print("${url.host}${url.path} tags=${tags}")
    io.print(urls.escape_query("a b+c"))
    io.print(urls.unescape_query("%zz"))
    Ok(Unit)
}

// FILE: urls.nomi
gopkg "urltools"

pub struct ParsedURL {
    scheme: String
    host: String
    path: String
    raw_query: String
    query: Map<String, List<String>>
}

pub fn escape_query(value: String) -> String go urltools.EscapeQuery

pub fn unescape_query(value: String) -> Result<String, String> go urltools.UnescapeQuery

pub fn parse_query(raw: String) -> Result<Map<String, List<String>>, String> go urltools.ParseQuery

pub fn parse(raw: String) -> Result<ParsedURL, String> go urltools.Parse

// FILE: urltools.go
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
<!-- expect
example.com/search tags=[go, nomi]
a+b%2Bc
Err(invalid URL escape "%zz")
-->

## Go and Nomi types

The Nomi signature decides what the function looks like to Nomi code. The Go
function's job is to match it. Nomi writes the code that calls the Go
function and converts values in both directions. The table below lists the
Go types it converts.

For any other Go type, convert it in your Go adapter. If Nomi only needs to
hold a Go value and hand it back, declare an `opaque type` for it, such as
`opaque type Conn go dbtools.Conn`. If Nomi
needs to look inside a value whose shape you can't write in the signature,
pass it as `Dynamic` and decode it on the Nomi side
([JSON & Dynamic](/json-and-dynamic/#dynamic-values)).

| Go type | Nomi type | Notes |
| --- | --- | --- |
| `string`, `bool`, `int64`, `float64`, `uint8` | `String`, `Bool`, `Int`, `Float`, `Byte` | An `Int` too big for `int64` is an error at the call. It is never cut short. |
| `int`, another sized or unsigned Go integer (`int32`, `uint64`, …) | `Int` | Range-checked the same way. |
| `*big.Int` | `Int` | Takes and returns any Int. |
| `[]byte` | `Bytes` | A byte buffer, not the same type as `List<Byte>`. |
| `[]T`, `map[K]V` | `List<T>`, `Map<K, V>` | The element, key and value types must be in this table too. |
| `time.Duration`, `time.Time` | `Duration`, `Instant` | |
| `io.Reader`, `io.ReadCloser`, `io.Writer`, `io.WriteCloser` | `Reader`, `Writer` from `std::io` | A Go stream becomes a Nomi stream over it. A Nomi stream passed to Go is read or written through its own buffer. |
| `*T` | `Maybe<T>` | A pointer to a Go type you bound with `opaque type` crosses as that opaque type instead. |
| two or more return values | a Nomi tuple | `func(string, string) (string, string)` binds to `(String, String)`. |
| a trailing `error` | `Result<_, String>` | `(T, error)` becomes `Result<T, String>`, and a lone `error` becomes `Result<Unit, String>`. The `Err` holds the Go error's message. |
| exported Go structs | Nomi `struct` | Exported Go fields match Nomi fields by name: `RawQuery` matches `raw_query`. The struct may come from another package your adapter imports, such as `model.User`, used directly or through an alias like `type User = model.User`. |
| a Go type alias | the alias's target | `type ID = core.ID` crosses as whatever `core.ID` is. |
| a `func(A) R` parameter | a Nomi function `(A) -> R` | Pass a lambda or a named function, and Go calls it. The Go function may return nothing (`(A) -> Unit`), one value, or a value and an `error` (`(A) -> Result<R, String>`). |
| a first parameter `ctx context.Context` | left out of the Nomi signature | Go receives the calling task's context: it carries the `with App.context` deadline and ends when the task is cancelled. |
| a Go value Nomi only holds | `opaque type` | Nomi stores it and passes it back, but can't look inside. |
| a value of unknown shape | `Dynamic` | Nomi decodes it or matches on it. |
| channels, non-empty interfaces, named function types | Go adapter or opaque handle | Convert to a type in this table, or keep the value opaque. |

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- An `Int` past a Go parameter's width traps at the call, naming the Go
  function and the parameter:
  `` `strings.Repeat`'s `count` takes an Int that fits in 64 bits; got 100000000000000000000 ``.
- `Byte` and `Int` don't stand in for each other: an `Int` doesn't fill a
  `uint8` parameter.
- A `float32` slot is range-checked. NaN and the infinities cross
  unchanged.
- A `time.Time` outside what an `Instant` holds traps. An `Instant` goes
  back to Go in UTC, with no zone.
- A Go map comes back with its keys in ascending order. A Go key type with
  no order, such as a pointer, is refused at the binding.
- Your own enums don't cross. Only `Result`, `Maybe` and `Bool` do, as a
  trailing `error`, `*T` and `bool`. A distinct type crosses as the type it
  wraps.
- A generic Go function can't be bound: write a non-generic Go function
  that calls it, and bind that.

Spec: [How Nomi types project to Go](/spec/host-declarations-and-go-bindings/#how-nomi-types-project-to-go).

</div>

## Binding the Go standard library

A Go standard library package needs no adapter file and no `go.mod`. Bind its
functions directly:

```nomi-run
import std::io

gopkg "strings"

gopkg "strconv"

fn upper(s: String) -> String go strings.ToUpper

fn parse_int(s: String) -> Result<Int, String> go strconv.Atoi

fn main() {
    io.print(upper("hi"))
    io.print(parse_int("42"))
}
```
<!-- expect
HI
Ok(42)
-->

This runs from any directory, as a `.nomi` file or as a `#!` script. The Go
function must still use the types in [Go and Nomi types](#go-and-nomi-types).
A function that returns
a concrete type such as `*gzip.Reader`, or a generic one such as
`slices.Max`, needs a small adapter of your own, like `urltools` above. You can't bind a package that is internal
to the standard library, such as `internal/abi`.

## Shipping a binding

A project with Go bindings keeps its Nomi files, its Go files, `nomi.toml` and
a `go.mod` together in one directory:

```tree
go-bindings/
├── nomi.toml
├── go.mod
├── main.nomi
├── urls.nomi
└── urls.go
```

`nomi new` writes `nomi.toml` and `go.mod` for a new project, and `nomi
init` writes them in a directory that already holds your files, as in the
steps at the top of this page. The `go.mod` is an ordinary Go module file. It
names your module and your own Go dependencies. It says nothing about Nomi:

```gomod
module gobindings

go 1.27.0
```

The module path is also the `gopkg` path of the Go code in the project
directory, so `urls.nomi` binds `urls.go` with `gopkg "gobindings"`. Go code
in a subdirectory adds the subdirectory to the path:
`gopkg "gobindings/adapters"`.

To use a third-party Go package, add its module with `nomi add` in the
project directory: `nomi add github.com/user/gopkg` takes a package's path
as well as its module's. If you have Go, `go get` works on the same file.
Then import the package
in your `.go` file. A `gopkg` can also name a package from a module you added,
with no adapter file, as long as its functions use the types in
[Go and Nomi types](#go-and-nomi-types).

Go has a feature called cgo that lets Go code call C code. Nomi builds the Go
side without it. A `gopkg` naming a package that needs cgo, itself or through
a package it imports, is a compile error at the `gopkg`.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- The cgo check follows the package's imports, so a package that is pure Go
  itself but imports one that needs cgo is refused too.
- A package internal to the Go standard library, such as `internal/abi`,
  is refused at its `gopkg`.
- A `gopkg` stands at the top level of a file, beside the bindings that use
  it, and each `go alias.Symbol` names an alias a `gopkg` in the same file
  declares.
- A Go type binding is always `opaque type`: Nomi can hold and pass it,
  never build or look inside it.

Spec: [Binding Go packages](/spec/host-declarations-and-go-bindings/#binding-go-packages-gopkg-and-go).
Building a project with Go bindings is in the tooling reference's
[Go bindings](/tooling/go-bindings/).

</div>

Go bindings are compiled with Go, and programs without them need no Go at
all. `nomi` uses the `go` that `NOMI_GO` names, else the one on your `PATH`,
else it downloads the Go release it was built with. `nomi run`, `nomi test`
and `nomi check` compile the Go side for you and reuse it until the Go code,
`go.mod` or Nomi itself changes, and `nomi build` compiles it into the
executable. The tooling reference's [Go bindings](/tooling/go-bindings/)
has the details.

The examples on this page run in your browser, which can't compile Go. So
the site compiles the Go they bind ahead of time, and an example's Go file is
read-only. You can edit its Nomi files and call the bound functions however
you like, but binding a Go function the site did not compile is an error.

[Generating Code](/generating-code/) is next. It shows how a tool writes
Nomi code from data, as sqlkit does in [Databases](/databases/).
