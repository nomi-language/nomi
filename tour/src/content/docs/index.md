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

Nomi is a statically typed, immutable language for writing expression-shaped
programs that stay explicit as they grow. This site is both
the language tour and the public reference: runnable examples execute in the
browser, format themselves after valid edits, and show the same hovers as the
editor.

## Nomi at a glance

- **Statically typed**, with local inference — named functions spell out
  parameters and every non-`Unit` return type; bindings and lambdas infer.
- **Fully immutable** — bindings, structs, and collections are all
  persistent; "modifying" returns a new value.
- **Functional-first and pattern-matching** — first-class functions, algebraic
  data types, exhaustive [`case` destructuring](/pattern-matching/), and tail
  calls that run in constant stack space.
- **Clear local control flow** — `return`, `break`, `continue`, and prefix
  `try` are available when they make a function easier to read.
- **Explicit interfaces** — types implement interfaces with
  `impl Interface for Type { ... }`, opt into generated implementations with
  `derive`, and call dispatch through qualified names.
- **Qualified calls, not value methods** — behavior lives in the nearest API
  container: usually a type owner, interface, or file API. Calls look like
  `User.rename(user)`,
  `String.length(name)`, or `Display.to_string(value)`, never `user.rename()`.
- **Pipe-oriented** — `x |> f()` keeps data flow reading top-to-bottom.
- **Structured concurrency** — [`concurrent { ... }` blocks](/concurrency/),
  tasks, channels, and context-style cancellation keep spawned work scoped.
- **Typed app fields** — runtime context, configuration, and dependencies live
  on a single [app value](/capabilities-and-context/); tests can swap
  dependencies with a scoped
  `with App.logger = ...` line.
- **Built-in tests** — named [`test` / `tests` blocks](/testing/),
  assertions, setup, and `//!` attached examples are ordinary Nomi code run by
  `nomi test`.

:::note[Design Status]
Nomi is very early. This site is the main public artifact right now: the place
to try the language in the browser while the design is still being worked out.
The core surfaces — expressions, types, pattern matching, imports, interfaces,
derives, tests, collections, and standard control flow — are the most
exercised. Concurrency, FFI, and module management are younger. Next on the
list: a debugger and faster editor tooling.
:::

## Running Nomi

```
nomi run app.nomi       # run a program
nomi test               # run every test under the current directory
nomi                    # an interactive REPL
nomi build app.nomi     # write a standalone executable
```

`nomi build` writes a **single executable** that needs nothing installed to
run. Build for another platform with `--target`, for example
`--target linux/amd64`.

A file can also run as a script. Start it with a `#!/usr/bin/env nomi` line,
make it executable, and `./hi.nomi a b` runs it as `nomi run hi.nomi a b`
would; `nomi hi.nomi a b` does the same.

### The Nomi VM

Nomi compiles your program to bytecode and runs it on its own virtual machine,
the way Lua, Python and Erlang do. There is one VM, and it is the same
everywhere Nomi runs:

- `nomi run`, `nomi test` and the REPL run your program on it.
- `nomi build` packages it with your program into one executable; a built
  program is the VM plus your code, not native machine code.
- The examples on this site run on it in your browser. Edit one and it
  re-runs as you type.
- A Go application can embed it to load a Nomi program and call its
  functions.

## Where Go fits

Nomi is **implemented in Go**, and follows Go's lead on practical things:
garbage collection, cross-platform builds, single-binary distribution, strong
tooling, and dependency management. The language itself is its own.

- A Nomi **module** works like a Go module: a directory tree with a manifest
  (`nomi.toml`), versioned and depended on as one unit. Inside a module, the
  **file** is the unit you import and the unit `pub` controls visibility
  across. There are no packages.
- Nomi can [**call Go code**](/ffi-and-dynamic/) through a typed FFI when you need a library the
  standard library doesn't cover. Programs that do need the Go toolchain to
  build; programs that don't, don't.

## Coming from another language

**Elixir** — Pipes, pattern matching, immutable data, and
transformation-heavy functions transfer well. Expect static types, declared
ADTs instead of atoms/tagged tuples, `Iter` pipelines instead of
`Enum`/`Stream`, and structured `concurrent { }` blocks plus supervisors
rather than actor mailboxes.

**Gleam** — ADTs, `Result`/`Maybe`, no null, local inference, and
expression-shaped code should feel familiar. Nomi adds bare bindings,
explicit `impl Interface for Type` blocks, `derive`, app fields, `defer`,
typed literals, and pragmatic local control flow.

**Rust** — Traits map to interfaces, conformances use
`impl Interface for Type { ... }`, `where` bounds are checked at call sites,
`Result`/`Maybe` use prefix `try`, and distinct types fill the newtype role.
There is no ownership system, no `mut`, no macros, no value-dot methods, and
inference stays local to function bodies.

**Go** — You will recognize `go.mod`, `go get`, strong formatting, explicit
imports, channels, and context-style cancellation. Nomi does not have Go's
packages. The language model is not Go's: values are immutable, errors are
`Result`/sum types, interface conformance is explicit, and concurrency is
structured.

[Bindings & Expressions](/bindings-and-expressions/) is the place to
start; each chapter builds on the last.
