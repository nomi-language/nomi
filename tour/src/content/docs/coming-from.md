---
title: Coming from another language
description: How the ideas of a language you know look in Nomi, and a page for each language.
---

Pick the language you know best. Its page shows how its ideas look in
Nomi.

**[Elixir](/from-elixir/).** Pipes, pattern matching, immutable data and
functions that transform data will feel familiar. What is different: types
are static, variants are declared types rather than atoms or tagged tuples,
`Iter` pipelines replace `Enum` and `Stream`, and concurrency starts from
`concurrent` blocks and supervisors, with `std::actors` for mailbox-style
servers.

**Gleam.** Sum types, `Result` and `Maybe`, no null, local type inference and
expression-oriented code will feel familiar. Nomi adds bindings without
`let`, `impl Interface for Type` blocks, `derive`, app fields, `defer`, typed
literals, and early `return`, `break` and `continue`.

**[Rust](/from-rust/).** Traits become interfaces, and an implementation is
`impl Interface for Type`. `where` bounds are checked at each call.
`Result` and `Maybe` propagate with `?`, as Rust's do. Distinct types do the job of
newtypes. There is no ownership, no `mut`, no `value.method()` calls and no
macros: a library [generates code](/generating-code/) into ordinary files
instead. Type inference stays inside a function body.

**[Go](/from-go/).** You will recognize modules that work like Go modules, a standard
formatter, explicit imports, channels and context-style cancellation. Values
are immutable, errors are `Result` values, a type says which interfaces it
implements, and concurrency is structured.

There are also guides for [TypeScript](/from-typescript/),
[Python](/from-python/), [Kotlin](/from-kotlin/), [Swift](/from-swift/) and
[Ruby](/from-ruby/).

After that, the [Language Tour](/introduction/) covers the whole
language, and the [Cheat Sheet](/cheat-sheet/) lists the rules most often
written wrong.
