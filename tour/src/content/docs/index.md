---
title: Nomi Lang
description: A runnable introduction to the Nomi language.
# The wordmark carries the NAME, so the <h1> carries the DESCRIPTION rather than
# repeating it — a mark reading "NOMI" above a heading reading "Nomi Lang" is the
# same word twice. `title` above still supplies the browser tab, the sidebar
# label and the image's alt text, so the name is not lost anywhere the mark
# cannot be seen. The h1 text is this page's own `description`, not new copy.
hero:
  title: A runnable introduction to the Nomi language.
  image:
    alt: Nomi Lang
    light: ../../assets/nomi-wordmark.svg
    dark: ../../assets/nomi-wordmark-dark.svg
---

Nomi is a statically typed language where every value is immutable. Most
code is written as expressions: `if`, `case` and blocks all produce values.

This site is the language tour and the public reference. You can edit and run
every example in your browser. An example formats itself after each edit that
parses, and hovering shows the same information your editor does.

## Nomi at a glance

- **Static types.** Named functions write out their parameter types and any
  return type other than `Unit`. Bindings and lambdas infer their types.
- **Immutable values.** Bindings, structs and collections never change. To
  "modify" a value, you make a new one.
- **Functional style.** Functions are values, types can be sums of variants,
  and [`case`](/pattern-matching/) must cover every shape a value can take.
  Tail calls run in constant stack space.
- **Plain control flow.** `return`, `break`, `continue` and a postfix `?` are
  there when they make a function easier to read.
- **Explicit interfaces.** A type implements an interface with
  `impl Interface for Type { ... }`, or gets a generated implementation with
  `derive`.
- **Qualified calls.** A function is called through the type, interface or
  file that owns it: `User.rename(user)`, `String.length(name)`,
  `Display.to_string(value)`. There is no `user.rename()`.
- **Pipes.** `x |> f()` lets data flow read from top to bottom.
- **Structured concurrency.** Tasks are started inside a
  [`concurrent { ... }` block](/concurrency/), which keeps them scoped to it.
  Channels and context-style cancellation are built in.
- **App fields.** Configuration and dependencies live on one
  [app value](/capabilities-and-context/). A test can swap one with a line
  such as `with App.logger = ...`.
- **Built-in tests.** [`test` blocks](/testing/), assertions and `//!`
  examples are ordinary Nomi code, and `nomi test` runs them.

:::note[Design Status]
Nomi is very early. This site is the main place to try the language while the
design is still being worked out. Expressions, types, pattern matching,
imports, interfaces, derives, tests, collections and control flow are the most
used and tested parts. Concurrency, calling Go, and module management are
newer. Next on the list is incremental compilation, which will make the
editor and `nomi check` faster.
:::

## Running Nomi

```console
$ nomi run app.nomi           # run a program
$ nomi run --watch app.nomi   # run it again on every save
$ nomi test                   # run every test under the current directory
$ nomi test --watch           # run the tests again on every save
$ nomi                        # an interactive REPL
$ nomi build app.nomi         # write a standalone executable
```

`nomi build` writes one executable file that needs nothing else installed.
`--target` builds it for another platform. `nomi build --web` writes a web
page that runs the program in the browser, the way the examples on this site
run. [Building & Shipping](/building-and-shipping/) covers these and the other
ways to ship a program.

### The Nomi VM

Nomi compiles your program to bytecode and runs it on its own virtual
machine (VM), the way Lua, Python and Erlang do:

- `nomi run`, `nomi test` and the REPL run your program on it.
- `nomi build` puts it in the executable beside your program.
- The examples on this site run on it in your browser. Edit one and it runs
  again as you type.
- A Go application can embed it to load a Nomi program and call its
  functions.

## Where Go fits

Nomi is written in Go. It follows Go on practical matters: garbage
collection, builds for other platforms, shipping one file, formatting and
tooling, and dependencies. The language itself is different from Go.

- A Nomi module works like a Go module. It is a directory tree with a
  manifest, `nomi.toml`, and it is versioned and used as one unit. Inside a
  module, you import a file, and `pub` controls what other files can see.
  There are no packages.
- Nomi can [call Go code](/ffi-and-dynamic/) when you need a library the
  standard library doesn't have. A program that calls Go needs the Go
  toolchain to build. Other programs don't.

## Coming from another language

**Elixir.** Pipes, pattern matching, immutable data and functions that
transform data will feel familiar. What is different: types are static,
variants are declared types rather than atoms or tagged tuples, `Iter`
pipelines replace `Enum` and `Stream`, and concurrency uses
`concurrent { }` blocks and supervisors rather than mailboxes.

**Gleam.** Sum types, `Result` and `Maybe`, no null, local type inference and
expression-oriented code will feel familiar. Nomi adds bindings without
`let`, `impl Interface for Type` blocks, `derive`, app fields, `defer`, typed
literals, and early `return`, `break` and `continue`.

**Rust.** Traits become interfaces, and an implementation is
`impl Interface for Type { ... }`. `where` bounds are checked at each call.
`Result` and `Maybe` propagate with `?`, as Rust's do. Distinct types do the job of
newtypes. There is no ownership, no `mut`, no `value.method()` calls and no
macros: a library [generates code](/generating-code/) into ordinary files
instead. Type inference stays inside a function body.

**Go.** You will recognize modules that work like Go modules, a standard formatter,
explicit imports, channels and context-style cancellation. Nomi has no Go
packages. Values are immutable, errors are `Result` values, a type says which
interfaces it implements, and concurrency is structured.

Start with [Bindings & Expressions](/bindings-and-expressions/). Each chapter
builds on the one before.
