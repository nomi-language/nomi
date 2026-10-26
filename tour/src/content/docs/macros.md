---
title: "Macros"
description: "Use a library's macro to write declarations for you, see what it wrote, read its errors, and write a small macro of your own."
---

A macro is code from a library that writes declarations for you while your
program is checked. You write one line with the macro's keyword, and the
macro turns it into ordinary Nomi functions or types. The `!` after the
keyword marks a macro, so you can always tell which declarations a library
writes.

The [Databases](/databases/) chapter, later in the tour, uses one:
`sqlkit.query!` turns a SQL query into a typed Nomi function. This chapter
uses a smaller one, `csvdata.table!`, which turns a CSV file into a lookup
function.

## Using a macro

A shop keeps its prices and product names in two CSV files:

```tree
shop/
├── go.mod
├── nomi.toml
├── main.nomi
├── tables.nomi
├── data/
│   ├── names.csv
│   └── prices.csv
└── macros/csvdata/
```

`data/prices.csv` holds each product's price in cents:

```csv
sku,cents
mug,1450
poster,2200
tote,1800
```

The macro comes from the module `csvdata`, which here is the directory
`macros/csvdata/`. You add it as you add any dependency, and `go.mod`
then requires it:

```console
$ nomi add ./macros/csvdata
added csvdata from ./macros/csvdata (module csvdata; import csvdata)
```

```gomod
module shop

go 1.27.0

require csvdata v0.0.0

replace csvdata => ./macros/csvdata
```

`tables.nomi` imports csvdata and declares two tables:

```nomi
import csvdata

/// Each product's price, in cents.
pub csvdata.table! price(sku: String) -> Maybe<Int> `prices.csv`

/// What each product is called.
pub csvdata.table! product_name(sku: String) -> Maybe<String> `names.csv`
```

A macro declaration has the same parts every time:

- the keyword with its `!`, qualified by the file that declares it, as
  `io.print` is qualified by `io`: `csvdata.table!`;
- a name, `price`, and optionally parameters and a result type, written as
  a function's are. They give the type of what the macro declares, so
  `price` is a function from `String` to `Maybe<Int>`;
- optionally a body between backticks. This macro's body is the name of
  the CSV file. A body may have a tag written right before its opening
  backtick, as a [typed literal](/typed-literals/) does, and editors
  highlight the body in the tag's language: sqlkit's queries are tagged
  `Sql`. csvdata's body is a file name, so it takes no tag.

`pub` makes the generated function public, as it does for a function you
write yourself. A doc comment on the declaration becomes the generated
function's doc comment. A macro writes only top-level declarations. It
cannot change code you wrote, and it cannot appear inside a function body.

You can also import the keyword by name and write it without the
qualifier, as you can import a function:

```nomi
import csvdata.table

/// Each product's price, in cents.
pub table! price(sku: String) -> Maybe<Int> `prices.csv`
```

A macro reads its settings from a table in your `nomi.toml` named after
its module. csvdata's `dir` is the directory the CSV files are in:

```toml
[module]
name = "shop"
entry_points = ["main"]

[csvdata]
dir = "data"
```

Each table becomes a function that answers `Some(value)` for a key in the
file and `None` for any other key. You call it like any function. This
example is the whole shop: `main.nomi`, the files above, and the csvdata
module under `macros/csvdata/`, each in a tab. Running a macro takes a
moment, so this example runs when you press Run. Change the price on the
`mug` row of `data/prices.csv`, or the sku in `main.nomi`, and run it
again:

```nomi-run manual
// FILE: main.nomi
import {
    std/io
    tables
}

fn main() {
    sku = "mug"
    name = tables.product_name(sku) |> Maybe.with_default(sku)
    cents = tables.price(sku) |> Maybe.with_default(0)
    io.print("${name}: ${cents} cents")
}

// FILE: tables.nomi
import csvdata

/// Each product's price, in cents.
pub csvdata.table! price(sku: String) -> Maybe<Int> `prices.csv`

/// What each product is called.
pub csvdata.table! product_name(sku: String) -> Maybe<String> `names.csv`

// FILE: data/prices.csv
sku,cents
mug,1450
poster,2200
tote,1800

// FILE: data/names.csv
sku,name
mug,Coffee mug
poster,Concert poster
tote,Tote bag

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[csvdata]
dir = "data"

// FILE: go.mod
module shop

go 1.27.0

require csvdata v0.0.0

replace csvdata => ./macros/csvdata

// FILE: macros/csvdata/csvdata.nomi
// `table`: a lookup function whose rows are a CSV file's. The body names
// the file, under the directory `dir` of the module's `[csvdata]` table.
// Its first row is a header; every other row is a key and a value.
import {
    std/json.Json
    std/macro.{Body, Declaration, Diagnostic, ExpandCall, Expansion, Input, Macro}
    std/macro.{Request, RequestCall, Source, Span}
}

/// A lookup function over the rows of a CSV file.
pub macro table: CsvTable

type CsvTable

/// One row of a table: its line, its key and value cells and their text.
struct Row {
    line: Int
    key: String
    value: String
    text: String
}

impl Macro for CsvTable {
    fn request(call: RequestCall) -> Request {
        case table_path(call.decl, call.config) {
            Some(path) -> #Request{files: [path]}
            None -> #Request{}
        }
    }

    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl

        param = case decl.params {
            [p] -> p
            _ -> return Declaration.error(decl, "a table takes one parameter, its key")
        }

        result = case decl.result {
            Some(t) -> t
            None -> return Declaration.error(decl, "a table names its result type: `-> Maybe<Int>`")
        }

        value_type = case maybe_argument(result.text) {
            Some(t) ->
                t

            None ->
                return Expansion.error(
                    result.span,
                    "a table answers `None` for a missing key, so write `-> Maybe<${result.text}>`",
                )
        }

        path = case table_path(decl, call.config) {
            Some(p) -> p
            None -> return Declaration.error(decl, "a table names its CSV file in backticks")
        }

        case decl.body {
            Some(#Body{tag: Some(tag), tag_span: Some(span)}) ->
                return Expansion.error(
                    span,
                    "a table's body is the path of its CSV file and takes no tag: remove `${tag}`",
                )

            _ -> {}
        }

        input = case Input.find(call.inputs, path) {
            Some(i) -> i
            None -> return #Expansion{
                diagnostics: [
                    #Diagnostic{
                        span: body_span(decl),
                        message: "no file ${path}",
                        hints: ["the path is under the module root, after `[csvdata].dir`"],
                    },
                ],
            }
        }

        #(rows, problems) = read_rows(input)

        if !Iter.empty?(problems) {
            return #Expansion{diagnostics: problems}
        }

        quote_keys = param.type_text.text == "String"
        quote_values = value_type == "String"
        visibility = if decl.public { "pub " } else { "" }

        header =
            Source.new()
            |> Source.add("${visibility}fn ${decl.name}(")
            |> Source.mapped(param.name, param.span)
            |> Source.add(": ")
            |> Source.mapped(param.type_text.text, param.type_text.span)
            |> Source.add(") -> ")
            |> Source.mapped(result.text, result.span)
            |> Source.add(" {\n    case ${param.name} {\n")

        arms = Iter.reduce(
            rows,
            fn src, row -> {
                key_end = String.length(row.key)
                key_span = Span.on_line(input.path, row.line, row.text, 0, key_end)
                value_start = key_end + 1
                value_end = value_start + String.length(row.value)
                value_span = Span.on_line(input.path, row.line, row.text, value_start, value_end)
                key_hint = Some("keys of `${decl.name}` are of type `${param.type_text.text}`")

                value = if quote_values {
                    String.to_literal(row.value)
                } else {
                    row.value
                }

                src
                |> Source.add("        ")
                |> key_cell(quote_keys, row.key, key_span, key_hint)
                |> Source.add(" -> ")
                |> Source.mapped(
                    "Some(${value})",
                    value_span,
                    hint: Some("values of `${decl.name}` are of type `${value_type}`"),
                )
                |> Source.add("\n")
            },
            from: header,
        )

        arms
        |> Source.add("        _ -> None\n    }\n}\n")
        |> Source.expansion()
    }
}

/// source followed by the key `text`, made from `span`: a string literal
/// when `quoted`, else the text as it is.
fn key_cell(source: Source, quoted: Bool, text: String, span: Span, hint: Maybe<String>) -> Source {
    if quoted {
        Source.string_literal(source, text, span, hint)
    } else {
        Source.mapped(source, text, span, hint)
    }
}

/// The file decl reads: its body, under the configuration's `dir`.
fn table_path(decl: Declaration, config: Json) -> Maybe<String> {
    dir = case config {
        Json.Obj(fields) ->
            case Map.get(fields, "dir") {
                Some(Json.String(d)) -> d + "/"
                _ -> ""
            }

        _ ->
            ""
    }

    decl.body
    |> Maybe.map(fn body -> String.trim(body.text))
    |> Maybe.flat_map(fn text -> if text == "" { None } else { Some(dir + text) })
}

/// The type argument of `text` when it is `Maybe<T>`: `T`.
fn maybe_argument(text: String) -> Maybe<String> {
    text
    |> String.strip_prefix("Maybe<")
    |> Maybe.flat_map(fn rest -> String.strip_suffix(rest, ">"))
}

/// The span of decl's body, or of decl when it has none.
fn body_span(decl: Declaration) -> Span {
    case decl.body {
        Some(body) -> body.span
        None -> decl.span
    }
}

/// The rows of a CSV input after its header, and what is wrong with them.
fn read_rows(input: Input) -> #(List<Row>, List<Diagnostic>) {
    #(rows, problems) =
        input.text
        |> String.lines()
        |> Iter.with_index()
        |> Iter.drop(1)
        |> Iter.reduce(
            fn acc: #(List<Row>, List<Diagnostic>), #(index, text) -> {
                #(rows, problems) = acc
                line = index + 1

                case String.split(text, ",") {
                    [""] ->
                        acc

                    [key, value] ->
                        #([#Row{line, key, value, text}, ..rows], problems)

                    cells -> {
                        count = Iter.count(cells)

                        problem = #Diagnostic{
                            span: Span.line(input.path, line, text),
                            message: "this row has ${count} cells; a table row is a key and a value",
                        }

                        #(rows, [problem, ..problems])
                    }
                }
            },
            from: #([], []),
        )

    // Each list was built last first.
    #(Iter.reverse(rows), Iter.reverse(problems))
}

// FILE: macros/csvdata/nomi.toml
[module]
name = "csvdata"

// FILE: macros/csvdata/go.mod
module csvdata

go 1.27.0
```
<!-- expect
Coffee mug: 1450 cents
-->

In the editor, go to definition on `tables.price` jumps to the `table!`
declaration in `tables.nomi`.

## Seeing what it wrote

`nomi expand <file>` prints what each macro declaration in a file became,
its *expansion*:

This example shows what `nomi expand tables.nomi` prints for its first
tab. Add a row to a CSV file and run it again to see the new case:

```nomi-run manual expand
// FILE: tables.nomi
import csvdata

/// Each product's price, in cents.
pub csvdata.table! price(sku: String) -> Maybe<Int> `prices.csv`

/// What each product is called.
pub csvdata.table! product_name(sku: String) -> Maybe<String> `names.csv`

// FILE: data/prices.csv
sku,cents
mug,1450
poster,2200
tote,1800

// FILE: data/names.csv
sku,name
mug,Coffee mug
poster,Concert poster
tote,Tote bag

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[csvdata]
dir = "data"

// FILE: go.mod
module shop

go 1.27.0

require csvdata v0.0.0

replace csvdata => ./macros/csvdata

// FILE: macros/csvdata/csvdata.nomi
// `table`: a lookup function whose rows are a CSV file's. The body names
// the file, under the directory `dir` of the module's `[csvdata]` table.
// Its first row is a header; every other row is a key and a value.
import {
    std/json.Json
    std/macro.{Body, Declaration, Diagnostic, ExpandCall, Expansion, Input, Macro}
    std/macro.{Request, RequestCall, Source, Span}
}

/// A lookup function over the rows of a CSV file.
pub macro table: CsvTable

type CsvTable

/// One row of a table: its line, its key and value cells and their text.
struct Row {
    line: Int
    key: String
    value: String
    text: String
}

impl Macro for CsvTable {
    fn request(call: RequestCall) -> Request {
        case table_path(call.decl, call.config) {
            Some(path) -> #Request{files: [path]}
            None -> #Request{}
        }
    }

    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl

        param = case decl.params {
            [p] -> p
            _ -> return Declaration.error(decl, "a table takes one parameter, its key")
        }

        result = case decl.result {
            Some(t) -> t
            None -> return Declaration.error(decl, "a table names its result type: `-> Maybe<Int>`")
        }

        value_type = case maybe_argument(result.text) {
            Some(t) ->
                t

            None ->
                return Expansion.error(
                    result.span,
                    "a table answers `None` for a missing key, so write `-> Maybe<${result.text}>`",
                )
        }

        path = case table_path(decl, call.config) {
            Some(p) -> p
            None -> return Declaration.error(decl, "a table names its CSV file in backticks")
        }

        case decl.body {
            Some(#Body{tag: Some(tag), tag_span: Some(span)}) ->
                return Expansion.error(
                    span,
                    "a table's body is the path of its CSV file and takes no tag: remove `${tag}`",
                )

            _ -> {}
        }

        input = case Input.find(call.inputs, path) {
            Some(i) -> i
            None -> return #Expansion{
                diagnostics: [
                    #Diagnostic{
                        span: body_span(decl),
                        message: "no file ${path}",
                        hints: ["the path is under the module root, after `[csvdata].dir`"],
                    },
                ],
            }
        }

        #(rows, problems) = read_rows(input)

        if !Iter.empty?(problems) {
            return #Expansion{diagnostics: problems}
        }

        quote_keys = param.type_text.text == "String"
        quote_values = value_type == "String"
        visibility = if decl.public { "pub " } else { "" }

        header =
            Source.new()
            |> Source.add("${visibility}fn ${decl.name}(")
            |> Source.mapped(param.name, param.span)
            |> Source.add(": ")
            |> Source.mapped(param.type_text.text, param.type_text.span)
            |> Source.add(") -> ")
            |> Source.mapped(result.text, result.span)
            |> Source.add(" {\n    case ${param.name} {\n")

        arms = Iter.reduce(
            rows,
            fn src, row -> {
                key_end = String.length(row.key)
                key_span = Span.on_line(input.path, row.line, row.text, 0, key_end)
                value_start = key_end + 1
                value_end = value_start + String.length(row.value)
                value_span = Span.on_line(input.path, row.line, row.text, value_start, value_end)
                key_hint = Some("keys of `${decl.name}` are of type `${param.type_text.text}`")

                value = if quote_values {
                    String.to_literal(row.value)
                } else {
                    row.value
                }

                src
                |> Source.add("        ")
                |> key_cell(quote_keys, row.key, key_span, key_hint)
                |> Source.add(" -> ")
                |> Source.mapped(
                    "Some(${value})",
                    value_span,
                    hint: Some("values of `${decl.name}` are of type `${value_type}`"),
                )
                |> Source.add("\n")
            },
            from: header,
        )

        arms
        |> Source.add("        _ -> None\n    }\n}\n")
        |> Source.expansion()
    }
}

/// source followed by the key `text`, made from `span`: a string literal
/// when `quoted`, else the text as it is.
fn key_cell(source: Source, quoted: Bool, text: String, span: Span, hint: Maybe<String>) -> Source {
    if quoted {
        Source.string_literal(source, text, span, hint)
    } else {
        Source.mapped(source, text, span, hint)
    }
}

/// The file decl reads: its body, under the configuration's `dir`.
fn table_path(decl: Declaration, config: Json) -> Maybe<String> {
    dir = case config {
        Json.Obj(fields) ->
            case Map.get(fields, "dir") {
                Some(Json.String(d)) -> d + "/"
                _ -> ""
            }

        _ ->
            ""
    }

    decl.body
    |> Maybe.map(fn body -> String.trim(body.text))
    |> Maybe.flat_map(fn text -> if text == "" { None } else { Some(dir + text) })
}

/// The type argument of `text` when it is `Maybe<T>`: `T`.
fn maybe_argument(text: String) -> Maybe<String> {
    text
    |> String.strip_prefix("Maybe<")
    |> Maybe.flat_map(fn rest -> String.strip_suffix(rest, ">"))
}

/// The span of decl's body, or of decl when it has none.
fn body_span(decl: Declaration) -> Span {
    case decl.body {
        Some(body) -> body.span
        None -> decl.span
    }
}

/// The rows of a CSV input after its header, and what is wrong with them.
fn read_rows(input: Input) -> #(List<Row>, List<Diagnostic>) {
    #(rows, problems) =
        input.text
        |> String.lines()
        |> Iter.with_index()
        |> Iter.drop(1)
        |> Iter.reduce(
            fn acc: #(List<Row>, List<Diagnostic>), #(index, text) -> {
                #(rows, problems) = acc
                line = index + 1

                case String.split(text, ",") {
                    [""] ->
                        acc

                    [key, value] ->
                        #([#Row{line, key, value, text}, ..rows], problems)

                    cells -> {
                        count = Iter.count(cells)

                        problem = #Diagnostic{
                            span: Span.line(input.path, line, text),
                            message: "this row has ${count} cells; a table row is a key and a value",
                        }

                        #(rows, [problem, ..problems])
                    }
                }
            },
            from: #([], []),
        )

    // Each list was built last first.
    #(Iter.reverse(rows), Iter.reverse(problems))
}

// FILE: macros/csvdata/nomi.toml
[module]
name = "csvdata"

// FILE: macros/csvdata/go.mod
module csvdata

go 1.27.0
```
<!-- expect
// csvdata.table! price (line 4), expanded by the macro table of module csvdata
/// Each product's price, in cents.
pub fn price(sku: String) -> Maybe<Int> {
    case sku {
        "mug" -> Some(1450)
        "poster" -> Some(2200)
        "tote" -> Some(1800)
        _ -> None
    }
}

// csvdata.table! product_name (line 7), expanded by the macro table of module csvdata
/// What each product is called.
pub fn product_name(sku: String) -> Maybe<String> {
    case sku {
        "mug" -> Some("Coffee mug")
        "poster" -> Some("Concert poster")
        "tote" -> Some("Tote bag")
        _ -> None
    }
}
-->

## Errors

The compiler checks the generated code as part of your file. An error
never points into generated code; it is reported at something you wrote.

### Errors in your input

csvdata links each value it writes to its cell in the CSV file. If someone
writes a price in dollars instead of cents, the error points at the cell:

```nomi-run manual error
// FILE: data/prices.csv
sku,cents
mug,1450
poster,2200
tote,18.00

// FILE: main.nomi
import {
    std/io
    tables
}

fn main() {
    sku = "mug"
    name = tables.product_name(sku) |> Maybe.with_default(sku)
    cents = tables.price(sku) |> Maybe.with_default(0)
    io.print("${name}: ${cents} cents")
}

// FILE: tables.nomi
import csvdata

/// Each product's price, in cents.
pub csvdata.table! price(sku: String) -> Maybe<Int> `prices.csv`

/// What each product is called.
pub csvdata.table! product_name(sku: String) -> Maybe<String> `names.csv`

// FILE: data/names.csv
sku,name
mug,Coffee mug
poster,Concert poster
tote,Tote bag

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[csvdata]
dir = "data"

// FILE: go.mod
module shop

go 1.27.0

require csvdata v0.0.0

replace csvdata => ./macros/csvdata

// FILE: macros/csvdata/csvdata.nomi
// `table`: a lookup function whose rows are a CSV file's. The body names
// the file, under the directory `dir` of the module's `[csvdata]` table.
// Its first row is a header; every other row is a key and a value.
import {
    std/json.Json
    std/macro.{Body, Declaration, Diagnostic, ExpandCall, Expansion, Input, Macro}
    std/macro.{Request, RequestCall, Source, Span}
}

/// A lookup function over the rows of a CSV file.
pub macro table: CsvTable

type CsvTable

/// One row of a table: its line, its key and value cells and their text.
struct Row {
    line: Int
    key: String
    value: String
    text: String
}

impl Macro for CsvTable {
    fn request(call: RequestCall) -> Request {
        case table_path(call.decl, call.config) {
            Some(path) -> #Request{files: [path]}
            None -> #Request{}
        }
    }

    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl

        param = case decl.params {
            [p] -> p
            _ -> return Declaration.error(decl, "a table takes one parameter, its key")
        }

        result = case decl.result {
            Some(t) -> t
            None -> return Declaration.error(decl, "a table names its result type: `-> Maybe<Int>`")
        }

        value_type = case maybe_argument(result.text) {
            Some(t) ->
                t

            None ->
                return Expansion.error(
                    result.span,
                    "a table answers `None` for a missing key, so write `-> Maybe<${result.text}>`",
                )
        }

        path = case table_path(decl, call.config) {
            Some(p) -> p
            None -> return Declaration.error(decl, "a table names its CSV file in backticks")
        }

        case decl.body {
            Some(#Body{tag: Some(tag), tag_span: Some(span)}) ->
                return Expansion.error(
                    span,
                    "a table's body is the path of its CSV file and takes no tag: remove `${tag}`",
                )

            _ -> {}
        }

        input = case Input.find(call.inputs, path) {
            Some(i) -> i
            None -> return #Expansion{
                diagnostics: [
                    #Diagnostic{
                        span: body_span(decl),
                        message: "no file ${path}",
                        hints: ["the path is under the module root, after `[csvdata].dir`"],
                    },
                ],
            }
        }

        #(rows, problems) = read_rows(input)

        if !Iter.empty?(problems) {
            return #Expansion{diagnostics: problems}
        }

        quote_keys = param.type_text.text == "String"
        quote_values = value_type == "String"
        visibility = if decl.public { "pub " } else { "" }

        header =
            Source.new()
            |> Source.add("${visibility}fn ${decl.name}(")
            |> Source.mapped(param.name, param.span)
            |> Source.add(": ")
            |> Source.mapped(param.type_text.text, param.type_text.span)
            |> Source.add(") -> ")
            |> Source.mapped(result.text, result.span)
            |> Source.add(" {\n    case ${param.name} {\n")

        arms = Iter.reduce(
            rows,
            fn src, row -> {
                key_end = String.length(row.key)
                key_span = Span.on_line(input.path, row.line, row.text, 0, key_end)
                value_start = key_end + 1
                value_end = value_start + String.length(row.value)
                value_span = Span.on_line(input.path, row.line, row.text, value_start, value_end)
                key_hint = Some("keys of `${decl.name}` are of type `${param.type_text.text}`")

                value = if quote_values {
                    String.to_literal(row.value)
                } else {
                    row.value
                }

                src
                |> Source.add("        ")
                |> key_cell(quote_keys, row.key, key_span, key_hint)
                |> Source.add(" -> ")
                |> Source.mapped(
                    "Some(${value})",
                    value_span,
                    hint: Some("values of `${decl.name}` are of type `${value_type}`"),
                )
                |> Source.add("\n")
            },
            from: header,
        )

        arms
        |> Source.add("        _ -> None\n    }\n}\n")
        |> Source.expansion()
    }
}

/// source followed by the key `text`, made from `span`: a string literal
/// when `quoted`, else the text as it is.
fn key_cell(source: Source, quoted: Bool, text: String, span: Span, hint: Maybe<String>) -> Source {
    if quoted {
        Source.string_literal(source, text, span, hint)
    } else {
        Source.mapped(source, text, span, hint)
    }
}

/// The file decl reads: its body, under the configuration's `dir`.
fn table_path(decl: Declaration, config: Json) -> Maybe<String> {
    dir = case config {
        Json.Obj(fields) ->
            case Map.get(fields, "dir") {
                Some(Json.String(d)) -> d + "/"
                _ -> ""
            }

        _ ->
            ""
    }

    decl.body
    |> Maybe.map(fn body -> String.trim(body.text))
    |> Maybe.flat_map(fn text -> if text == "" { None } else { Some(dir + text) })
}

/// The type argument of `text` when it is `Maybe<T>`: `T`.
fn maybe_argument(text: String) -> Maybe<String> {
    text
    |> String.strip_prefix("Maybe<")
    |> Maybe.flat_map(fn rest -> String.strip_suffix(rest, ">"))
}

/// The span of decl's body, or of decl when it has none.
fn body_span(decl: Declaration) -> Span {
    case decl.body {
        Some(body) -> body.span
        None -> decl.span
    }
}

/// The rows of a CSV input after its header, and what is wrong with them.
fn read_rows(input: Input) -> #(List<Row>, List<Diagnostic>) {
    #(rows, problems) =
        input.text
        |> String.lines()
        |> Iter.with_index()
        |> Iter.drop(1)
        |> Iter.reduce(
            fn acc: #(List<Row>, List<Diagnostic>), #(index, text) -> {
                #(rows, problems) = acc
                line = index + 1

                case String.split(text, ",") {
                    [""] ->
                        acc

                    [key, value] ->
                        #([#Row{line, key, value, text}, ..rows], problems)

                    cells -> {
                        count = Iter.count(cells)

                        problem = #Diagnostic{
                            span: Span.line(input.path, line, text),
                            message: "this row has ${count} cells; a table row is a key and a value",
                        }

                        #(rows, [problem, ..problems])
                    }
                }
            },
            from: #([], []),
        )

    // Each list was built last first.
    #(Iter.reverse(rows), Iter.reverse(problems))
}

// FILE: macros/csvdata/nomi.toml
[module]
name = "csvdata"

// FILE: macros/csvdata/go.mod
module csvdata

go 1.27.0
```
<!-- expect
data/prices.csv:4:6: case branch type mismatch: expected Maybe<Int>, got Maybe<Float>
data/prices.csv:4:6: help: values of `price` are of type `Int`
-->

The error names the file that holds it, here the CSV file, with the line
and column of the cell.

### The macro's own errors

A macro reports mistakes it finds itself at the part of the declaration
they are about. Here the first table has a tag and the second names a file
that does not exist:

```nomi-run manual error
// FILE: tables.nomi
import csvdata

/// Each product's price, in cents.
pub csvdata.table! price(sku: String) -> Maybe<Int> Csv`prices.csv`

/// What each product is called.
pub csvdata.table! product_name(sku: String) -> Maybe<String> `name.csv`

// FILE: main.nomi
import {
    std/io
    tables
}

fn main() {
    sku = "mug"
    name = tables.product_name(sku) |> Maybe.with_default(sku)
    cents = tables.price(sku) |> Maybe.with_default(0)
    io.print("${name}: ${cents} cents")
}

// FILE: data/prices.csv
sku,cents
mug,1450
poster,2200
tote,1800

// FILE: data/names.csv
sku,name
mug,Coffee mug
poster,Concert poster
tote,Tote bag

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[csvdata]
dir = "data"

// FILE: go.mod
module shop

go 1.27.0

require csvdata v0.0.0

replace csvdata => ./macros/csvdata

// FILE: macros/csvdata/csvdata.nomi
// `table`: a lookup function whose rows are a CSV file's. The body names
// the file, under the directory `dir` of the module's `[csvdata]` table.
// Its first row is a header; every other row is a key and a value.
import {
    std/json.Json
    std/macro.{Body, Declaration, Diagnostic, ExpandCall, Expansion, Input, Macro}
    std/macro.{Request, RequestCall, Source, Span}
}

/// A lookup function over the rows of a CSV file.
pub macro table: CsvTable

type CsvTable

/// One row of a table: its line, its key and value cells and their text.
struct Row {
    line: Int
    key: String
    value: String
    text: String
}

impl Macro for CsvTable {
    fn request(call: RequestCall) -> Request {
        case table_path(call.decl, call.config) {
            Some(path) -> #Request{files: [path]}
            None -> #Request{}
        }
    }

    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl

        param = case decl.params {
            [p] -> p
            _ -> return Declaration.error(decl, "a table takes one parameter, its key")
        }

        result = case decl.result {
            Some(t) -> t
            None -> return Declaration.error(decl, "a table names its result type: `-> Maybe<Int>`")
        }

        value_type = case maybe_argument(result.text) {
            Some(t) ->
                t

            None ->
                return Expansion.error(
                    result.span,
                    "a table answers `None` for a missing key, so write `-> Maybe<${result.text}>`",
                )
        }

        path = case table_path(decl, call.config) {
            Some(p) -> p
            None -> return Declaration.error(decl, "a table names its CSV file in backticks")
        }

        case decl.body {
            Some(#Body{tag: Some(tag), tag_span: Some(span)}) ->
                return Expansion.error(
                    span,
                    "a table's body is the path of its CSV file and takes no tag: remove `${tag}`",
                )

            _ -> {}
        }

        input = case Input.find(call.inputs, path) {
            Some(i) -> i
            None -> return #Expansion{
                diagnostics: [
                    #Diagnostic{
                        span: body_span(decl),
                        message: "no file ${path}",
                        hints: ["the path is under the module root, after `[csvdata].dir`"],
                    },
                ],
            }
        }

        #(rows, problems) = read_rows(input)

        if !Iter.empty?(problems) {
            return #Expansion{diagnostics: problems}
        }

        quote_keys = param.type_text.text == "String"
        quote_values = value_type == "String"
        visibility = if decl.public { "pub " } else { "" }

        header =
            Source.new()
            |> Source.add("${visibility}fn ${decl.name}(")
            |> Source.mapped(param.name, param.span)
            |> Source.add(": ")
            |> Source.mapped(param.type_text.text, param.type_text.span)
            |> Source.add(") -> ")
            |> Source.mapped(result.text, result.span)
            |> Source.add(" {\n    case ${param.name} {\n")

        arms = Iter.reduce(
            rows,
            fn src, row -> {
                key_end = String.length(row.key)
                key_span = Span.on_line(input.path, row.line, row.text, 0, key_end)
                value_start = key_end + 1
                value_end = value_start + String.length(row.value)
                value_span = Span.on_line(input.path, row.line, row.text, value_start, value_end)
                key_hint = Some("keys of `${decl.name}` are of type `${param.type_text.text}`")

                value = if quote_values {
                    String.to_literal(row.value)
                } else {
                    row.value
                }

                src
                |> Source.add("        ")
                |> key_cell(quote_keys, row.key, key_span, key_hint)
                |> Source.add(" -> ")
                |> Source.mapped(
                    "Some(${value})",
                    value_span,
                    hint: Some("values of `${decl.name}` are of type `${value_type}`"),
                )
                |> Source.add("\n")
            },
            from: header,
        )

        arms
        |> Source.add("        _ -> None\n    }\n}\n")
        |> Source.expansion()
    }
}

/// source followed by the key `text`, made from `span`: a string literal
/// when `quoted`, else the text as it is.
fn key_cell(source: Source, quoted: Bool, text: String, span: Span, hint: Maybe<String>) -> Source {
    if quoted {
        Source.string_literal(source, text, span, hint)
    } else {
        Source.mapped(source, text, span, hint)
    }
}

/// The file decl reads: its body, under the configuration's `dir`.
fn table_path(decl: Declaration, config: Json) -> Maybe<String> {
    dir = case config {
        Json.Obj(fields) ->
            case Map.get(fields, "dir") {
                Some(Json.String(d)) -> d + "/"
                _ -> ""
            }

        _ ->
            ""
    }

    decl.body
    |> Maybe.map(fn body -> String.trim(body.text))
    |> Maybe.flat_map(fn text -> if text == "" { None } else { Some(dir + text) })
}

/// The type argument of `text` when it is `Maybe<T>`: `T`.
fn maybe_argument(text: String) -> Maybe<String> {
    text
    |> String.strip_prefix("Maybe<")
    |> Maybe.flat_map(fn rest -> String.strip_suffix(rest, ">"))
}

/// The span of decl's body, or of decl when it has none.
fn body_span(decl: Declaration) -> Span {
    case decl.body {
        Some(body) -> body.span
        None -> decl.span
    }
}

/// The rows of a CSV input after its header, and what is wrong with them.
fn read_rows(input: Input) -> #(List<Row>, List<Diagnostic>) {
    #(rows, problems) =
        input.text
        |> String.lines()
        |> Iter.with_index()
        |> Iter.drop(1)
        |> Iter.reduce(
            fn acc: #(List<Row>, List<Diagnostic>), #(index, text) -> {
                #(rows, problems) = acc
                line = index + 1

                case String.split(text, ",") {
                    [""] ->
                        acc

                    [key, value] ->
                        #([#Row{line, key, value, text}, ..rows], problems)

                    cells -> {
                        count = Iter.count(cells)

                        problem = #Diagnostic{
                            span: Span.line(input.path, line, text),
                            message: "this row has ${count} cells; a table row is a key and a value",
                        }

                        #(rows, [problem, ..problems])
                    }
                }
            },
            from: #([], []),
        )

    // Each list was built last first.
    #(Iter.reverse(rows), Iter.reverse(problems))
}

// FILE: macros/csvdata/nomi.toml
[module]
name = "csvdata"

// FILE: macros/csvdata/go.mod
module csvdata

go 1.27.0
```
<!-- expect
tables.nomi:4:53: a table's body is the path of its CSV file and takes no tag: remove `Csv`
tables.nomi:7:64: no file data/name.csv
tables.nomi:7:64: help: the path is under the module root, after `[csvdata].dir`
-->

csvdata also refuses `-> Int`, with
``a table answers `None` for a missing key, so write `-> Maybe<Int>` ``
under the `Int`.

A declaration that fails like this declares nothing, and the compiler
reports only the macro's errors. `main.nomi` calls `tables.price`, but you
don't also get an error for each call.

### A name you declare too

The names a macro declares join your file's top-level names. If
`tables.nomi` also declares a `fn price` of its own, both claim the name:

```nomi-run manual error
// FILE: tables.nomi
import csvdata

/// Each product's price, in cents.
pub csvdata.table! price(sku: String) -> Maybe<Int> `prices.csv`

/// What each product is called.
pub csvdata.table! product_name(sku: String) -> Maybe<String> `names.csv`

fn price(sku: String) -> Int {
    0
}

// FILE: main.nomi
import {
    std/io
    tables
}

fn main() {
    sku = "mug"
    name = tables.product_name(sku) |> Maybe.with_default(sku)
    cents = tables.price(sku) |> Maybe.with_default(0)
    io.print("${name}: ${cents} cents")
}

// FILE: data/prices.csv
sku,cents
mug,1450
poster,2200
tote,1800

// FILE: data/names.csv
sku,name
mug,Coffee mug
poster,Concert poster
tote,Tote bag

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[csvdata]
dir = "data"

// FILE: go.mod
module shop

go 1.27.0

require csvdata v0.0.0

replace csvdata => ./macros/csvdata

// FILE: macros/csvdata/csvdata.nomi
// `table`: a lookup function whose rows are a CSV file's. The body names
// the file, under the directory `dir` of the module's `[csvdata]` table.
// Its first row is a header; every other row is a key and a value.
import {
    std/json.Json
    std/macro.{Body, Declaration, Diagnostic, ExpandCall, Expansion, Input, Macro}
    std/macro.{Request, RequestCall, Source, Span}
}

/// A lookup function over the rows of a CSV file.
pub macro table: CsvTable

type CsvTable

/// One row of a table: its line, its key and value cells and their text.
struct Row {
    line: Int
    key: String
    value: String
    text: String
}

impl Macro for CsvTable {
    fn request(call: RequestCall) -> Request {
        case table_path(call.decl, call.config) {
            Some(path) -> #Request{files: [path]}
            None -> #Request{}
        }
    }

    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl

        param = case decl.params {
            [p] -> p
            _ -> return Declaration.error(decl, "a table takes one parameter, its key")
        }

        result = case decl.result {
            Some(t) -> t
            None -> return Declaration.error(decl, "a table names its result type: `-> Maybe<Int>`")
        }

        value_type = case maybe_argument(result.text) {
            Some(t) ->
                t

            None ->
                return Expansion.error(
                    result.span,
                    "a table answers `None` for a missing key, so write `-> Maybe<${result.text}>`",
                )
        }

        path = case table_path(decl, call.config) {
            Some(p) -> p
            None -> return Declaration.error(decl, "a table names its CSV file in backticks")
        }

        case decl.body {
            Some(#Body{tag: Some(tag), tag_span: Some(span)}) ->
                return Expansion.error(
                    span,
                    "a table's body is the path of its CSV file and takes no tag: remove `${tag}`",
                )

            _ -> {}
        }

        input = case Input.find(call.inputs, path) {
            Some(i) -> i
            None -> return #Expansion{
                diagnostics: [
                    #Diagnostic{
                        span: body_span(decl),
                        message: "no file ${path}",
                        hints: ["the path is under the module root, after `[csvdata].dir`"],
                    },
                ],
            }
        }

        #(rows, problems) = read_rows(input)

        if !Iter.empty?(problems) {
            return #Expansion{diagnostics: problems}
        }

        quote_keys = param.type_text.text == "String"
        quote_values = value_type == "String"
        visibility = if decl.public { "pub " } else { "" }

        header =
            Source.new()
            |> Source.add("${visibility}fn ${decl.name}(")
            |> Source.mapped(param.name, param.span)
            |> Source.add(": ")
            |> Source.mapped(param.type_text.text, param.type_text.span)
            |> Source.add(") -> ")
            |> Source.mapped(result.text, result.span)
            |> Source.add(" {\n    case ${param.name} {\n")

        arms = Iter.reduce(
            rows,
            fn src, row -> {
                key_end = String.length(row.key)
                key_span = Span.on_line(input.path, row.line, row.text, 0, key_end)
                value_start = key_end + 1
                value_end = value_start + String.length(row.value)
                value_span = Span.on_line(input.path, row.line, row.text, value_start, value_end)
                key_hint = Some("keys of `${decl.name}` are of type `${param.type_text.text}`")

                value = if quote_values {
                    String.to_literal(row.value)
                } else {
                    row.value
                }

                src
                |> Source.add("        ")
                |> key_cell(quote_keys, row.key, key_span, key_hint)
                |> Source.add(" -> ")
                |> Source.mapped(
                    "Some(${value})",
                    value_span,
                    hint: Some("values of `${decl.name}` are of type `${value_type}`"),
                )
                |> Source.add("\n")
            },
            from: header,
        )

        arms
        |> Source.add("        _ -> None\n    }\n}\n")
        |> Source.expansion()
    }
}

/// source followed by the key `text`, made from `span`: a string literal
/// when `quoted`, else the text as it is.
fn key_cell(source: Source, quoted: Bool, text: String, span: Span, hint: Maybe<String>) -> Source {
    if quoted {
        Source.string_literal(source, text, span, hint)
    } else {
        Source.mapped(source, text, span, hint)
    }
}

/// The file decl reads: its body, under the configuration's `dir`.
fn table_path(decl: Declaration, config: Json) -> Maybe<String> {
    dir = case config {
        Json.Obj(fields) ->
            case Map.get(fields, "dir") {
                Some(Json.String(d)) -> d + "/"
                _ -> ""
            }

        _ ->
            ""
    }

    decl.body
    |> Maybe.map(fn body -> String.trim(body.text))
    |> Maybe.flat_map(fn text -> if text == "" { None } else { Some(dir + text) })
}

/// The type argument of `text` when it is `Maybe<T>`: `T`.
fn maybe_argument(text: String) -> Maybe<String> {
    text
    |> String.strip_prefix("Maybe<")
    |> Maybe.flat_map(fn rest -> String.strip_suffix(rest, ">"))
}

/// The span of decl's body, or of decl when it has none.
fn body_span(decl: Declaration) -> Span {
    case decl.body {
        Some(body) -> body.span
        None -> decl.span
    }
}

/// The rows of a CSV input after its header, and what is wrong with them.
fn read_rows(input: Input) -> #(List<Row>, List<Diagnostic>) {
    #(rows, problems) =
        input.text
        |> String.lines()
        |> Iter.with_index()
        |> Iter.drop(1)
        |> Iter.reduce(
            fn acc: #(List<Row>, List<Diagnostic>), #(index, text) -> {
                #(rows, problems) = acc
                line = index + 1

                case String.split(text, ",") {
                    [""] ->
                        acc

                    [key, value] ->
                        #([#Row{line, key, value, text}, ..rows], problems)

                    cells -> {
                        count = Iter.count(cells)

                        problem = #Diagnostic{
                            span: Span.line(input.path, line, text),
                            message: "this row has ${count} cells; a table row is a key and a value",
                        }

                        #(rows, [problem, ..problems])
                    }
                }
            },
            from: #([], []),
        )

    // Each list was built last first.
    #(Iter.reverse(rows), Iter.reverse(problems))
}

// FILE: macros/csvdata/nomi.toml
[module]
name = "csvdata"

// FILE: macros/csvdata/go.mod
module csvdata

go 1.27.0
```
<!-- expect
tables.nomi:9:4: `price` is declared here and by `csvdata.table! price` on line 4
tables.nomi:9:4: help: rename one of them
-->

The table declares nothing, so your own `price` keeps its meaning. A
`price` declared in another file does not clash, since each file has its
own names.

### A bug in the macro

The compiler checks the generated code against the declaration. The
generated code must declare the name you wrote, be `pub` exactly when the
declaration is, and take the parameters and return the type you wrote. A
macro may add parameters before yours, such as a database connection, and
may return a `Result` of your type. An expansion that breaks these rules, or has an error in code
that came from nothing you wrote, is a bug in the macro. The compiler
reports it at your declaration, says it is a defect in the macro, and
points you to `nomi expand`.

## Files a macro reads

The CSV files are inputs of `tables.nomi`, and so is the macro's own
source. In the editor, saving `data/prices.csv` expands the tables again
and updates every error that depends on them, and an error in the CSV file
shows in the CSV file. The examples on this page do the same when you
press Run after editing a CSV tab or the macro's own tab.

`nomi check`, `nomi run` and `nomi build` keep nothing on disk between
runs. Each run reads the files again and runs every macro in the program.

## Writing a macro

A macro is a type that implements the [`Macro`](/reference/macro/)
interface from `std/macro`. Use a macro to declare functions or types. To
check a value at compile time, use a [typed literal](/typed-literals/).

Here is a small macro, `choices.one_of!`. It declares an enum whose
variants are the words of its body. A café's `sizes.nomi` uses it:

```nomi
import choices

/// How big a coffee is.
pub choices.one_of! Size `small medium large`
```

That line declares the same type as `pub enum Size { Small Medium Large }`.
Only a macro can do this, since no function can declare a type.

### Where a macro lives

A module cannot use a macro it declares, because the compiler runs the
macro before it checks the module that uses it. So a macro lives in a
module of its own. For a macro of your own app, make one in a subdirectory
and add it:

```console
$ nomi new choices macros/choices
wrote macros/choices/nomi.toml, macros/choices/go.mod and macros/choices/main.nomi
next: cd macros/choices && nomi run main.nomi
```

A macro module has no program to run, so delete the `main.nomi` it wrote
and the `entry_points` line of its `nomi.toml`. Write `choices.nomi`
beside them, and add the module to your app:

```console
$ nomi add ./macros/choices
added choices from ./macros/choices (module choices; import choices)
```

Files under `macros/choices/` belong to choices, not to your app, because
that directory has its own `nomi.toml`. To share the macro with other
projects, publish its module like any Nomi module, as
[Modules & Imports](/modules-and-imports/) describes.

### The macro

This is `macros/choices/choices.nomi`, in the first tab of the café below.
`main.nomi` uses the `Size` that `sizes.nomi` declares. The macro's tab
is editable too: change what `expand` writes and run the example again.

```nomi-run manual
// FILE: macros/choices/choices.nomi
import std/macro.{Body, Declaration, ExpandCall, Expansion, Macro, Source, Span, pascal_case}

/// An enum whose variants are the words of the body:
/// ``choices.one_of! Size `small medium large` ``.
pub macro one_of: OneOf

type OneOf

impl Macro for OneOf {
    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl
        type_name = pascal_case(decl.name)

        if type_name != decl.name {
            return Expansion.error(
                decl.name_span,
                "a `one_of` declares a type, so its name is a type name: `${type_name}`",
            )
        }

        Some(body) = decl.body else {
            return Declaration.error(decl, "write the variants as the body: `small medium large`")
        }

        found = words(body)

        if Some(#(_, span)) = Iter.find(found, fn #(word, _) -> !snake_case?(word)) {
            return Expansion.error(span, "a variant is a snake_case word, such as `extra_large`")
        }

        visibility = if decl.public { "pub " } else { "" }
        start = Source.new() |> Source.add("${visibility}enum ${decl.name} {\n")

        Iter.reduce(
            found,
            fn source, #(word, span) -> {
                source
                |> Source.add("    ")
                |> Source.mapped(pascal_case(word), span, hint: Some("each word names one variant"))
                |> Source.add("\n")
            },
            from: start,
        )
        |> Source.add("}\n")
        |> Source.expansion()
    }
}

/// The words of the body, separated by spaces or line breaks, each with
/// its span.
fn words(body: Body) -> List<#(String, Span)> {
    pieces = body.text |> String.replace("\n", " ") |> String.split(" ")

    #(found, _) = Iter.reduce(
        pieces,
        fn #(found, at), word -> {
            end = at + String.length(word)

            if word == "" {
                #(found, end + 1)
            } else {
                #([#(word, Body.span_at(body, at, end)), ..found], end + 1)
            }
        },
        from: #([], 0),
    )

    Iter.reverse(found)
}

/// Whether `word` is a snake_case name, such as `extra_large`.
fn snake_case?(word: String) -> Bool {
    letters = String.to_codepoints(word)
    lower? = fn c: Codepoint -> c >= 'a' and c <= 'z'
    first = Iter.first(letters) |> Maybe.map(lower?) |> Maybe.with_default(False)
    first and Iter.all?(letters, fn c -> lower?(c) or c >= '0' and c <= '9' or c == '_')
}

// FILE: sizes.nomi
import choices

/// How big a coffee is.
pub choices.one_of! Size `small medium large`

// FILE: main.nomi
import {
    std/io
    sizes.Size
}

fn ounces(size: Size) -> Int {
    case size {
        Size.Small -> 8
        Size.Medium -> 12
        Size.Large -> 16
    }
}

fn main() {
    io.print(ounces(Size.Medium))
}

// FILE: nomi.toml
[module]
name = "cafe"
entry_points = ["main"]

// FILE: go.mod
module cafe

go 1.27.0

require choices v0.0.0

replace choices => ./macros/choices

// FILE: macros/choices/nomi.toml
[module]
name = "choices"

// FILE: macros/choices/go.mod
module choices

go 1.27.0
```
<!-- expect
12
-->

`pub macro one_of: OneOf` declares the keyword and the type that expands
it. The file must implement `Macro` for that type, and `pub` lets other
modules use the keyword.

The compiler calls `expand` once for each declaration. Its `ExpandCall`
holds `decl`, the declaration as you wrote it: its name, whether it is
`pub`, its parameters, result type and body, each with where it is. It
also holds the macro's `nomi.toml` table and the files the macro asked to
read; a macro that reads files also writes `request`, as csvdata does.
`one_of` reads none.

`words` splits the body and records where each word is; `snake_case?`
checks a word.

`one_of` checks that the name is a type name, splits the body into words,
checks each word, and writes an enum with one variant per word.
`pascal_case` makes `Small` from `small` and `ExtraLarge` from
`extra_large`:

This is what `nomi expand sizes.nomi` prints:

```nomi-run manual expand
// FILE: sizes.nomi
import choices

/// How big a coffee is.
pub choices.one_of! Size `small medium large`

// FILE: macros/choices/choices.nomi
import std/macro.{Body, Declaration, ExpandCall, Expansion, Macro, Source, Span, pascal_case}

/// An enum whose variants are the words of the body:
/// ``choices.one_of! Size `small medium large` ``.
pub macro one_of: OneOf

type OneOf

impl Macro for OneOf {
    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl
        type_name = pascal_case(decl.name)

        if type_name != decl.name {
            return Expansion.error(
                decl.name_span,
                "a `one_of` declares a type, so its name is a type name: `${type_name}`",
            )
        }

        Some(body) = decl.body else {
            return Declaration.error(decl, "write the variants as the body: `small medium large`")
        }

        found = words(body)

        if Some(#(_, span)) = Iter.find(found, fn #(word, _) -> !snake_case?(word)) {
            return Expansion.error(span, "a variant is a snake_case word, such as `extra_large`")
        }

        visibility = if decl.public { "pub " } else { "" }
        start = Source.new() |> Source.add("${visibility}enum ${decl.name} {\n")

        Iter.reduce(
            found,
            fn source, #(word, span) -> {
                source
                |> Source.add("    ")
                |> Source.mapped(pascal_case(word), span, hint: Some("each word names one variant"))
                |> Source.add("\n")
            },
            from: start,
        )
        |> Source.add("}\n")
        |> Source.expansion()
    }
}

/// The words of the body, separated by spaces or line breaks, each with
/// its span.
fn words(body: Body) -> List<#(String, Span)> {
    pieces = body.text |> String.replace("\n", " ") |> String.split(" ")

    #(found, _) = Iter.reduce(
        pieces,
        fn #(found, at), word -> {
            end = at + String.length(word)

            if word == "" {
                #(found, end + 1)
            } else {
                #([#(word, Body.span_at(body, at, end)), ..found], end + 1)
            }
        },
        from: #([], 0),
    )

    Iter.reverse(found)
}

/// Whether `word` is a snake_case name, such as `extra_large`.
fn snake_case?(word: String) -> Bool {
    letters = String.to_codepoints(word)
    lower? = fn c: Codepoint -> c >= 'a' and c <= 'z'
    first = Iter.first(letters) |> Maybe.map(lower?) |> Maybe.with_default(False)
    first and Iter.all?(letters, fn c -> lower?(c) or c >= '0' and c <= '9' or c == '_')
}

// FILE: nomi.toml
[module]
name = "cafe"
entry_points = ["main"]

// FILE: go.mod
module cafe

go 1.27.0

require choices v0.0.0

replace choices => ./macros/choices

// FILE: macros/choices/nomi.toml
[module]
name = "choices"

// FILE: macros/choices/go.mod
module choices

go 1.27.0
```
<!-- expect
// choices.one_of! Size (line 4), expanded by the macro one_of of module choices
/// How big a coffee is.
pub enum Size {
    Small
    Medium
    Large
}
-->

It writes `pub` when the declaration has it, because the compiler checks
that the two match. A version that always left `pub` out would get an error at each
declaration, ending
``it should be `pub`, as the declaration is, a defect in the macro``.
It writes no doc comment, because the compiler gives the enum the
declaration's.

### Mapping code to what the user wrote

`expand` returns an `Expansion`: Nomi source, and diagnostics for the
mistakes the macro found. `Source` builds the source piece by piece.
`Source.add` adds text. `Source.mapped` adds text and records the span it
was made from, a place in the user's file or in an input.
`Source.string_literal` adds a string from the input as a Nomi string
literal, so a quote or a `${` in it stays text.

The mapping lets the compiler report an error at the code the user wrote.
`words` gives each word its span with `Body.span_at(body, start, end)`,
whose offsets count graphemes, as `String.slice` does, and `one_of` maps each variant to its word. Write
a word twice and the checker's error lands on the second one, with the
hint the macro gave and a note at the first:

```nomi-run manual error
// FILE: sizes.nomi
import choices

/// How big a coffee is.
pub choices.one_of! Size `small medium small`

// FILE: macros/choices/choices.nomi
import std/macro.{Body, Declaration, ExpandCall, Expansion, Macro, Source, Span, pascal_case}

/// An enum whose variants are the words of the body:
/// ``choices.one_of! Size `small medium large` ``.
pub macro one_of: OneOf

type OneOf

impl Macro for OneOf {
    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl
        type_name = pascal_case(decl.name)

        if type_name != decl.name {
            return Expansion.error(
                decl.name_span,
                "a `one_of` declares a type, so its name is a type name: `${type_name}`",
            )
        }

        Some(body) = decl.body else {
            return Declaration.error(decl, "write the variants as the body: `small medium large`")
        }

        found = words(body)

        if Some(#(_, span)) = Iter.find(found, fn #(word, _) -> !snake_case?(word)) {
            return Expansion.error(span, "a variant is a snake_case word, such as `extra_large`")
        }

        visibility = if decl.public { "pub " } else { "" }
        start = Source.new() |> Source.add("${visibility}enum ${decl.name} {\n")

        Iter.reduce(
            found,
            fn source, #(word, span) -> {
                source
                |> Source.add("    ")
                |> Source.mapped(pascal_case(word), span, hint: Some("each word names one variant"))
                |> Source.add("\n")
            },
            from: start,
        )
        |> Source.add("}\n")
        |> Source.expansion()
    }
}

/// The words of the body, separated by spaces or line breaks, each with
/// its span.
fn words(body: Body) -> List<#(String, Span)> {
    pieces = body.text |> String.replace("\n", " ") |> String.split(" ")

    #(found, _) = Iter.reduce(
        pieces,
        fn #(found, at), word -> {
            end = at + String.length(word)

            if word == "" {
                #(found, end + 1)
            } else {
                #([#(word, Body.span_at(body, at, end)), ..found], end + 1)
            }
        },
        from: #([], 0),
    )

    Iter.reverse(found)
}

/// Whether `word` is a snake_case name, such as `extra_large`.
fn snake_case?(word: String) -> Bool {
    letters = String.to_codepoints(word)
    lower? = fn c: Codepoint -> c >= 'a' and c <= 'z'
    first = Iter.first(letters) |> Maybe.map(lower?) |> Maybe.with_default(False)
    first and Iter.all?(letters, fn c -> lower?(c) or c >= '0' and c <= '9' or c == '_')
}

// FILE: nomi.toml
[module]
name = "cafe"
entry_points = ["main"]

// FILE: go.mod
module cafe

go 1.27.0

require choices v0.0.0

replace choices => ./macros/choices

// FILE: macros/choices/nomi.toml
[module]
name = "choices"

// FILE: macros/choices/go.mod
module choices

go 1.27.0
```
<!-- expect
sizes.nomi:4:40: variant 'Small' is declared twice in enum `Size`
sizes.nomi:4:40: help: each word names one variant
sizes.nomi:4:27: note: the first is here
-->

Map each piece the checker might reject. Text added with `Source.add` has
no place in the user's files, so an error in it is reported as a bug in
the macro. The mapping also serves the editor: go to definition on
`Size.Medium` in another file jumps to the word `medium`.

### Reporting mistakes

When the macro finds a mistake itself, it reports it with
`Expansion.error(span, message)`, an expansion that declares nothing and
reports `message` at the span. `Declaration.error(decl, message)` reports at the declaration's keyword, for a mistake with no
better place, such as a missing body.

`one_of` could copy any word into the code. A word such as `12oz` would
then make an enum the parser rejects, and since that error is in the
expansion and not in your file, the compiler would call it a defect in
the macro. So `one_of` checks each word itself and reports a bad one
under the word:

```nomi-run manual error
// FILE: sizes.nomi
import choices

/// How big a coffee is.
pub choices.one_of! Size `small 12oz large`

// FILE: macros/choices/choices.nomi
import std/macro.{Body, Declaration, ExpandCall, Expansion, Macro, Source, Span, pascal_case}

/// An enum whose variants are the words of the body:
/// ``choices.one_of! Size `small medium large` ``.
pub macro one_of: OneOf

type OneOf

impl Macro for OneOf {
    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl
        type_name = pascal_case(decl.name)

        if type_name != decl.name {
            return Expansion.error(
                decl.name_span,
                "a `one_of` declares a type, so its name is a type name: `${type_name}`",
            )
        }

        Some(body) = decl.body else {
            return Declaration.error(decl, "write the variants as the body: `small medium large`")
        }

        found = words(body)

        if Some(#(_, span)) = Iter.find(found, fn #(word, _) -> !snake_case?(word)) {
            return Expansion.error(span, "a variant is a snake_case word, such as `extra_large`")
        }

        visibility = if decl.public { "pub " } else { "" }
        start = Source.new() |> Source.add("${visibility}enum ${decl.name} {\n")

        Iter.reduce(
            found,
            fn source, #(word, span) -> {
                source
                |> Source.add("    ")
                |> Source.mapped(pascal_case(word), span, hint: Some("each word names one variant"))
                |> Source.add("\n")
            },
            from: start,
        )
        |> Source.add("}\n")
        |> Source.expansion()
    }
}

/// The words of the body, separated by spaces or line breaks, each with
/// its span.
fn words(body: Body) -> List<#(String, Span)> {
    pieces = body.text |> String.replace("\n", " ") |> String.split(" ")

    #(found, _) = Iter.reduce(
        pieces,
        fn #(found, at), word -> {
            end = at + String.length(word)

            if word == "" {
                #(found, end + 1)
            } else {
                #([#(word, Body.span_at(body, at, end)), ..found], end + 1)
            }
        },
        from: #([], 0),
    )

    Iter.reverse(found)
}

/// Whether `word` is a snake_case name, such as `extra_large`.
fn snake_case?(word: String) -> Bool {
    letters = String.to_codepoints(word)
    lower? = fn c: Codepoint -> c >= 'a' and c <= 'z'
    first = Iter.first(letters) |> Maybe.map(lower?) |> Maybe.with_default(False)
    first and Iter.all?(letters, fn c -> lower?(c) or c >= '0' and c <= '9' or c == '_')
}

// FILE: nomi.toml
[module]
name = "cafe"
entry_points = ["main"]

// FILE: go.mod
module cafe

go 1.27.0

require choices v0.0.0

replace choices => ./macros/choices

// FILE: macros/choices/nomi.toml
[module]
name = "choices"

// FILE: macros/choices/go.mod
module choices

go 1.27.0
```
<!-- expect
sizes.nomi:4:33: a variant is a snake_case word, such as `extra_large`
-->

A lower-case name, `one_of! size`, gets
``a `one_of` declares a type, so its name is a type name: `Size` ``
under `size`.

### Testing a macro

`expand` is an ordinary Nomi function, so a test can call it with an
`ExpandCall` it builds and check what comes back. This test goes at the
end of `choices.nomi`, which then imports `std/json.Json` too:

```nomi
/// The call `expand` gets for ``pub choices.one_of! Size `text` ``.
fn size(text: String) -> ExpandCall {
    at = #Span{file: "sizes.nomi", line: 4, col: 27, end_line: 4, end_col: 27}

    decl = #Declaration{
        keyword: "one_of",
        name: "Size",
        public: True,
        params: [],
        result: None,
        body: Some(#Body{text, span: at, start_line: 4, start_col: 27, indent_col: 1, tag: None}),
        doc: "",
        file: "sizes.nomi",
        span: at,
        name_span: at,
    }

    #ExpandCall{decl, config: Json.Null, inputs: [], sites: []}
}

test "each word is a variant, and a word that is not a name is an error at the word" {
    expansion = OneOf.expand(size("small medium large"))
    assert expansion.source == "pub enum Size {\n    Small\n    Medium\n    Large\n}\n"

    problems = OneOf.expand(size("small 12oz large")).diagnostics

    assert Iter.map(problems, fn d -> #(d.message, d.span.col)) |> Iter.to_list() == [
        #("a variant is a snake_case word, such as `extra_large`", 33),
    ]
}
```

`size` uses one zero-width `Span` throughout (the test reads only the
body's start, so one point serves for every span). The body starts at
column 27, so `12oz`, six graphemes in, is at column 33.

```console
$ nomi test macros/choices
ok macros/choices/choices.nomi :: each word is a variant, and a word that is not a name is an error at the word
test result: ok. 1 passed, 0 failed
```

### More for macro authors

The [`std/macro` reference](/reference/macro/) and the spec's
[Macros section](https://github.com/nomi-language/nomi/blob/main/docs/spec.md#45-macros)
cover the rest:

- the hygiene rules: the generated code names only what it declares, what
  its own imports bring, the prelude and what the declaration writes, and
  a helper it declares is named after the declaration (`price_rows`) so two
  declarations in one file do not collide
  ([What it answers](https://github.com/nomi-language/nomi/blob/main/docs/spec.md#what-it-answers)),
  and what the compiler checks an expansion against
  ([What an expansion declares](https://github.com/nomi-language/nomi/blob/main/docs/spec.md#what-an-expansion-declares));
- the sandbox and the budgets. A macro runs while code is checked, in the
  editor too, so it may not print, touch files or the network, or read the
  clock, and each run has a fixed budget of steps and memory
  ([How it runs](https://github.com/nomi-language/nomi/blob/main/docs/spec.md#how-it-runs));
- `ProgramCheck`, a check over every declaration of a keyword at once, and
  `Navigation`, hover, go to definition, completion and quick fixes inside
  a declaration's body
  ([Whole-module checks and the editor](https://github.com/nomi-language/nomi/blob/main/docs/spec.md#whole-module-checks-and-the-editor));
- tools: programs in the macro's module, run with `nomi tool`, that do what
  a macro may not, such as reading a database
  ([`nomi expand` and `nomi tool`](https://github.com/nomi-language/nomi/blob/main/docs/spec.md#nomi-expand-and-nomi-tool)).

The next chapter, [Formatting for a Reader](/locales-and-formatting/),
writes numbers, prices, dates and lists the way a reader's language writes
them.
