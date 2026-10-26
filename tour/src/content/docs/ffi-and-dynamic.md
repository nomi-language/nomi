---
title: "FFI & Dynamic"
description: "Call Go packages from Nomi, and handle values whose shape you don't know yet with Dynamic."
---

:::caution[Early FFI surface]
You can build real programs on the binding syntax below, and the FFI tests
in this repository do. What may still change is the packaging around it:
how you lay out and publish a Go adapter, and the error messages you get
when something is wrong.
:::

A Nomi module can call functions from ordinary Go packages. The usual steps:

- Work in a project: a directory with a `nomi.toml` and a `go.mod`, whose
  module path names your Go code. `nomi new <module path>` starts one in a
  new directory, and `nomi init <module path>` makes the current directory
  one. A binding to the [Go standard library](#binding-the-go-standard-library)
  needs neither.
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
    std/io
    urls
}

fn main() -> Result<Unit, String> {
    url = try urls.parse("https://example.com/search?tag=go&tag=nomi&q=hello+world")

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
hold a Go value and hand it back, declare an `opaque type` for it. If Nomi
needs to look inside a value whose shape you can't write in the signature,
pass it as `Dynamic` and decode it on the Nomi side.

| Go type | Nomi type | Notes |
| --- | --- | --- |
| `string`, `bool`, `int64`, `float64`, `uint8` | `String`, `Bool`, `Int`, `Float`, `Byte` | `Int` is Go's `int64`. |
| a smaller Go integer (`int32`, `int8`, …) | `Int` | A value that doesn't fit is an error. It is never cut short. |
| `[]byte` | `Bytes` | A byte buffer, not the same type as `List<Byte>`. |
| `[]T`, `map[K]V` | `List<T>`, `Map<K, V>` | The element, key and value types must be in this table too. |
| `time.Duration`, `time.Time` | `Duration`, `Instant` | |
| `*T` | `Maybe<T>` | A pointer to a Go type you bound with `opaque type` crosses as that opaque type instead. |
| two or more return values | a Nomi tuple | `func(string, string) (string, string)` binds to `#(String, String)`. |
| a trailing `error` | `Result<_, String>` | `#(T, error)` becomes `Result<T, String>`, and a lone `error` becomes `Result<Unit, String>`. The `Err` holds the Go error's message. |
| exported Go structs | Nomi `struct` | Exported Go fields match Nomi fields by name: `RawQuery` matches `raw_query`. The struct may come from another package your adapter imports, such as `model.User`, used directly or through an alias like `type User = model.User`. |
| a Go type alias | the alias's target | `type ID = core.ID` crosses as whatever `core.ID` is. |
| a `func(A) R` parameter | a Nomi function `(A) -> R` | Pass a lambda or a named function, and Go calls it. The Go function may return nothing (`(A) -> Unit`), one value, or a value and an `error` (`(A) -> Result<R, String>`). |
| a Go value Nomi only holds | `opaque type` | Nomi stores it and passes it back, but can't look inside. |
| a value of unknown shape | `Dynamic` | Nomi decodes it or matches on it. |
| channels, non-empty interfaces, named function types | Go adapter or opaque handle | Convert to a type in this table, or keep the value opaque. |

## Binding the Go standard library

A Go standard library package needs no adapter file and no `go.mod`. Bind its
functions directly:

```nomi-run
import std/io

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
function must still use the types in [Go and Nomi types](#go-and-nomi-types). A function that takes
an `io.Reader`, or a generic one such as `slices.Max`, needs a small adapter
of your own, like `urltools` above. You can't bind a package that is internal
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

Go bindings are compiled with Go. Programs without Go bindings don't need Go
at all. To find a Go, `nomi` tries these in order:

- the `NOMI_GO` environment variable, which names a `go` command or the
  folder a Go installation lives in;
- a `go` on your `PATH`;
- the Go release `nomi` was built with. `nomi` downloads it from go.dev the
  first time it needs it, which is about 70 MB. It checks the download against
  a checksum built into `nomi`, and keeps it so it never downloads it again.

The editor's language server never downloads Go. If you have no `go`, run
`nomi fetch` in the project once. It downloads Go along with the project's
modules, and the editor uses that Go from then on. A command given
`--offline` doesn't download Go either.

Your Go code is compiled together with Nomi's own Go module. A `nomi` from a
release, or from `go install`, uses that module at its own version, which is
downloaded like any other dependency. So it needs no copy of Nomi's source. A
`nomi` you built from a clone of the repository uses that clone.

`nomi run`, `nomi test` and `nomi check` compile the Go side for you. The
first run of a project is slower while that happens. Later runs reuse the
result until the project's Go code, its `go.mod`, or Nomi itself changes.
Editing `.nomi` files doesn't trigger a recompile.

`nomi build` compiles the Go side into the executable it writes, so the
program runs without Nomi or Go installed. `nomi build --web` compiles it
into the page's WebAssembly module the same way, as long as every Go package
it uses builds for WebAssembly ([Web pages](/building-and-shipping/#web-pages)).

The examples on this page run in your browser, which can't compile Go. So
the site compiles the Go they bind ahead of time, and an example's Go file is
read-only. You can edit its Nomi files and call the bound functions however
you like, but binding a Go function the site did not compile is an error.

## Dynamic Values

Sometimes you get a value with no known Nomi type: a plugin message, a
decoded payload, loosely typed configuration, or data from another runtime.
[`Dynamic`](/reference/dynamic/) holds such a value.

Take the value in as `Dynamic`, then decode it into a real type once you
know what shape to expect. Every function in
[`std/dynamic`](/reference/dynamic/) that reads a `Dynamic` returns a
`Result`. Its error is a [`dynamic.Error`](/reference/dynamic/#struct-error),
which holds the path to the bad part, what was expected, and what was
there. So when a payload is wrong, the error tells you where.

For JSON, [`std/json`](/reference/json/) decodes text into a
[`Json`](/reference/json/#enum-json) enum, which you can match on directly
with `case`. To read it into types of your own, `Json.to_dynamic` turns it
into a `Dynamic`. Here a file of users has one bad entry:

```nomi-run
// FILE: main.nomi
import {
    std/dynamic.{self, Dynamic}
    std/fs
    std/io
    std/json.Json
}

struct User {
    name: String
    age: Int
}

// One user, from a value read as Dynamic.
fn user(d: Dynamic) -> Result<User, dynamic.Error> {
    name = try Dynamic.at_field(d, "name", Dynamic.as_string)
    age = try Dynamic.at_field(d, "age", Dynamic.as_int)
    Ok(#User{name, age})
}

// Print each user, or why it could not be read.
fn print_users(users: Dynamic, count: Int) {
    0..count
    |> Iter.each(fn i -> {
        case Dynamic.at_index(users, i, user) {
            Ok(u) -> io.print("${u.name} is ${u.age}")
            Err(e) -> io.print("user ${i}: ${e}")
        }
    })
}

fn main() -> Result<Unit, String> {
    text = fs.read_text("users.json") |> try Result.map_err(fn e -> "${e}")
    users = Json.decode(text) |> try Result.map_err(fn e -> "${e}") |> Json.to_dynamic()

    count = Dynamic.as_list(users) |> try Result.map_err(fn e -> "${e}") |> Iter.count()
    print_users(users, count)
    Ok(Unit)
}

// FILE: users.json
[
    {"name": "Ada", "age": 36},
    {"name": "Grace", "age": "unknown"}
]
```
<!-- expect
Ada is 36
user 1: decode error at [1].age: expected Int, got String
-->

`user` reads one entry with `Dynamic.at_field`, which adds the field's name
to the path of any error, and `Dynamic.at_index` adds the entry's position.
So the second entry's error says where it is, `[1].age`, and what was wrong.

Use `Dynamic` where data comes in from outside. Inside your program, give
data real types.

---

The last chapter, [Building & Shipping](/building-and-shipping/), shows how
to package a Nomi program as an executable, a web page, or a WebAssembly
module. After that, the [Standard Library](/reference/) reference covers
every module, generated from the library's own doc comments.
