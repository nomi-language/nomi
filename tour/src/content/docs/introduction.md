---
title: Introduction
description: What Nomi is like, how to run it, and how it relates to Go, before the tour's first chapter.
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
used and tested parts. Concurrency, calling Go, and module management are
newer. The editor rechecks only what an edit touches, and `nomi run`,
`nomi check` and `nomi test` relink a program from what they kept when an
edit changes only function bodies.
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
- Nomi can [call Go code](/calling-go/) when you need a library the
  standard library doesn't have. You don't need Go installed: Nomi's own
  libraries that wrap Go come prebuilt, and for your own Go code `nomi`
  uses the `go` on your `PATH` or downloads the release it was built with.

Start with [Bindings & Expressions](/bindings-and-expressions/). Each chapter
builds on the one before.
