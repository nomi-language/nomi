---
title: Introduction
description: What Nomi is like, how to run it, and how it is built, before the tour's first chapter.
---

The Language Tour covers the whole language, a chapter at a time, each
building on the one before. It assumes you program already; if you don't,
start with [First Steps](/learn/values/).

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
  `impl Interface for Type`, or gets a generated implementation with
  `derive`.
- **Qualified calls.** A function is called through the type, interface or
  file that owns it: `User.rename(user)`, `String.length(name)`,
  `Display.to_string(value)`. There is no `user.rename()`.
- **Pipes.** `x |> f()` lets data flow read from top to bottom.
- **Structured concurrency.** Tasks are started inside a
  [`concurrent` block](/concurrency/), which keeps them scoped to it.
  Channels and context-style cancellation are built in.
- **App fields.** Configuration and dependencies live on one
  [app value](/app-fields/). A test can swap one with a line
  such as `with App.logger = ...`.
- **Web apps.** A server renders checked HTML templates, or serves a React
  or Vue build and its JSON API; [Frontends](/frontends/) compares the ways.
- **Built-in tests.** [`test` blocks](/testing/), assertions and `//!`
  examples are ordinary Nomi code, and `nomi test` runs them.

:::note[Design Status]
Nomi is very early. This site is the main place to try the language while the
design is still being worked out. Expressions, types, pattern matching,
imports, interfaces, derives, tests, collections and control flow are the most
used and tested parts. Concurrency and module management are newer.
:::

## Running Nomi

[Installing Nomi](/tooling/install/) covers getting the `nomi` command.
Then:

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

Nomi compiles your program and runs it on its own virtual machine (VM), as
Lua and Python do. Each function becomes bytecode the first time it runs:

- `nomi run`, `nomi test` and the REPL run your program on it.
- `nomi build` puts it in the executable beside your program.
- You can edit most examples on this site, and they run on the VM in your
  browser.
- Another application can embed it to load a Nomi program and call its
  functions.

## How Nomi is built

The compiler and runtime are written in Go. You don't need Go to write, run
or ship Nomi.

- A module is a directory tree with a manifest, `nomi.toml`, versioned and
  used as one unit. Inside a module you import a file, and `pub` controls
  what other files can see.
- A library that needs native code, such as the
  [SQLite driver](/databases/), carries it as WebAssembly and runs it
  through [std::wasm](/reference/wasm/).
- Another program can host Nomi and supply functions to it, from Go
  directly or from any language with a WebAssembly runtime. A
  [`host fn`](/spec/host-declarations/) declares one.

The tour starts with [Bindings & Expressions](/bindings-and-expressions/).
