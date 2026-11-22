---
title: Coming from another language
description: How the ideas of a language you know look in Nomi, and a page for each language.
---

Pick the language you know best. Its page lists what carries over, what
will trip you up and what is only spelled differently, then shows one
program written in both languages.

**[Rust](/from-rust/).** Enums with payloads, exhaustive matching, `Result`
with a postfix `?`, `impl` blocks, bounded generics and `derive` carry over,
and a trait is an `interface`. There is no ownership, no `mut`, no
`value.method()` call and no macros: a library
[generates code](/generating-code/) into ordinary files instead.

**[Go](/from-go/).** Errors as values, structs, interfaces, one formatter,
channels and a single executable carry over. Values never change, an error
passes up with a postfix `?`, a type says which interfaces it implements,
and tasks run only inside a `concurrent` block, which waits for them.

**[Elixir](/from-elixir/).** Immutable data, pipes, pattern matching,
functions called through their module and supervised tasks carry over. Types
are static, an `enum` variant stands where an atom or a tagged tuple would,
`Iter` does the work of `Enum` and `Stream`, and tasks talk over channels,
with `std::actors` for a server like a `GenServer`.

**[TypeScript](/from-typescript/).** Static types with inference for locals,
generics, lambdas and `${...}` interpolation carry over, and a discriminated
union is an `enum`. Nothing is `null` or `undefined`, a failure is a
`Result` rather than a thrown error, and `Int`, `Float` and `Decimal` are
separate types.

**[Python](/from-python/).** Little punctuation, named arguments with
defaults, tuples you unpack and first-class functions carry over. Every type
is checked before the program runs, a block ends with `end`, values never
change, and a failure is a value the function returns.

**[Kotlin](/from-kotlin/).** `val` bindings, data classes, sealed
hierarchies with an exhaustive `when`, named arguments with defaults and
structured concurrency carry over. A nullable type is a `Maybe`, errors are
values, and there are no methods or extension functions.

**[Swift](/from-swift/).** Value types, enums with associated values,
optionals that must be handled, protocols a type adopts by saying so and
the leading dot of `.small` carry over. Every value is immutable, a
function that `throws` returns a `Result` instead, and a function is called
through its owner.

**[Ruby](/from-ruby/).** The last expression as the result, `Enumerable`
chains, `=>` in a hash and `case`/`in` patterns carry over. Types are
checked before the program runs, values never change, there are no methods
to call on a value, and a class is never reopened.

**Gleam** has no page of its own. Custom types with variants, `Result`, no
null, pipes and code built from expressions carry over, and `Option` is
`Maybe`. Nomi adds bindings without `let`, interfaces with
`impl Interface for Type`, `derive`, app fields, `defer`, typed literals,
and early `return`, `break` and `continue`.

After that, the [Language Tour](/introduction/) covers the whole
language, and the [Cheat Sheet](/cheat-sheet/) lists the rules most often
written wrong.
