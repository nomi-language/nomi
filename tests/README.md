# Nomi Test Programs

This directory is the runnable corpus for `nomi test`. It is also a browsable
library of small language examples. Tests live in numbered category folders,
`tests/NN-category/`, which `nomi test` discovers recursively, so a new test
needs no harness wiring: drop it in the right category. The numbers are only
there to keep the categories in a stable sequence.

How to write a test in Nomi (names, assertions, `subtests`, `io.replay`,
groups) is in the style guide, [`docs/style.md`](../docs/style.md) §40 to
§45. This file holds the corpus's own conventions.

## File Shapes

- `*_test.nomi` files are assertion tests run by `nomi test`. A single-file
  test is usually `NN-category/<name>_test.nomi`.
- Root-level `*_test.nomi` files should be self-contained except for stdlib
  imports.
- Test programs with local support modules live in a same-category directory.
  The directory contains the `*_test.nomi` entry file plus every local support
  module it imports, all at one flat level.
- Plain `.nomi` files inside those directories are support modules imported by
  that test program.
- App-shaped examples include a real entry file with `fn main` and
  `pub fn boot() -> Result<App, String>`, or
  `pub fn boot(startup: Startup) -> Result<App, String>` when boot reads
  startup input. A `*_test.nomi` either runs that entry file with
  `compiler.run_file(RunFile{entry_point: "main"})` and asserts the captured
  output (use `RunFile.env` when the app's `boot` reads `startup.env`), or
  imports it and names its boot in a group: `boot main.boot()`, or
  `boot main.boot(Startup{args: ["-v"]})` for a boot that takes a Startup. A
  test that reads app fields needs such a group, so its directory holds an
  entry file even when the test is the only other file. Every entry's `main`
  must run.

## Testing The Compiler

Every corpus test passes. A build-time failure, a runtime failure or a piece of
tooling output is a value the test asserts on, through `std/compiler` or a
domain API that returns an inspectable value.

**Compiler diagnostics are tested as values.** Use `compiler.check` (or
`compiler.check_project`) with a `"""..."""` literal and assert on the
diagnostics. If a fixture needs helper functions, keep them in a file beside it
in the same test-program directory. Keep the embedded program focused on the
rejected shape; when a bad expression only needs to be referenced, prefer
`dbg bad_expression` over importing `std/io` to call `io.inspect`:

```nomi
import {
    std::compiler
    test_helpers
}

test "missing members are rejected" {
    diagnostics = compiler.check(
        """
            fn main() {
                dbg List.empty()
            }
        """
    )

    assert test_helpers.has_diagnostic(diagnostics, "type 'List' has no member 'empty'")
}
```

**Program output is tested through `compiler.run`.** Use it when stdout is the
behavior under test, such as `dbg` rendering, and for runtime failure
behavior. It returns the captured output:

```nomi
test "debug output is rendered" {
    result = compiler.run(
        """
        fn main() {
          _ = dbg 41 + 1
        }
        """
    )

    assert result == Ok(
        """
        dbg line 2: 41 + 1 = 42

        """
    )
}
```

**Hover text is tested through `compiler.hover`.** Use a hover test when the
hover text is the behavior under test: inferred signatures, doc rendering,
import visibility. Don't add type-describing comments to ordinary value tests;
assert the value, then add one focused hover test if the signature is part of
the contract. Put the marker `ˇ` inside the name whose hover is under test:

```nomi
test "once hover shows its inferred type" {
    signature = test_helpers.hover_signature(
        """
          once defaultˇ_port = 8080

          fn main() {
            Unit
          }
        """
    )

    assert signature == "once default_port: Int = 8080"
}
```

**Use `// expect:` only for intentional output examples.** A trailing
`// expect:` comment belongs in a runnable example where printed output is the
behavior being shown, one expected line per comment. It is not the way to test
ordinary values.

**A test that exercises a syntactic form keeps that form**, even when a style
rule would rewrite it. The pipe tests in `13-iterators-and-pipes/` keep
value-seeded and single-stage pipes because the pipe is what they test. Apply
the pipe rule (style §7) only where the pipe is incidental.

## Harnesses

Run the corpus with:

```sh
nomi test tests
```

## Categories

### 01-foundations

Core syntax and expression rules: literals, comments, bindings, block scope,
shadowing, `once`, inline type annotations, `if`/`else`, redeclarations, and
triple-quoted strings.

### 02-testing

The Nomi test surface itself: `test`, `group`, setup values bound by a test's pattern,
`check`, `assert`, `refute`, assertion rendering, and custom assertable values.

### 03-tooling-and-diagnostics

Compiler/tooling APIs and editor-facing fixtures: `dbg`, formatter behavior,
import actions, unused imports, typed compiler checks, and member/variant
diagnostics.

### 04-scalars-and-text

Scalar and text types: integers, decimals, numeric conversion, ranges, strings,
codepoints, and random generation.

### 05-calendar-and-time

Calendar and clock-domain values: `Date`, `Time`, `Duration`, `NaiveDateTime`,
`OffsetDateTime`, `DateTime`, calendar shifts, zone conversion, DST gaps/folds,
and disambiguation.

### 06-collections

Collection literals and operations: lists, tuples, maps, sets, and collection
transforms.

### 07-structs-and-enums

Named product/sum types and anonymous structs: fields, builders, updates,
variants, and enum payload forms.

### 08-pattern-matching

`case`, exhaustiveness, `try`, nested/compound patterns, map patterns, variant
resolution, dot-leading variants, and negative pattern fixtures.

### 09-functions-and-control-flow

Functions and call shapes: lambdas, closures, named/default args, partial
application, trailing lambdas, field chains, param destructuring, predicate
names, and tail-call fixtures.

### 10-generics-and-type-wrappers

Generic functions/types and wrapper types: bounds, explicit type args,
embedding, distinct types, aliases, and opaque smart-constructor patterns.

### 11-interfaces-and-impls

Interface protocol and implementation behavior: defaults, dispatch, coherence,
missing impl diagnostics, struct interface behavior, existential dispatch,
type-param dispatch, and constrained defaults.

### 12-derives-and-standard-interfaces

Derivable and standard interfaces: `Display`, `Debug`, `Equatable`, `Hashable`,
`Comparable`, `Ordering`, universal debug fallback, and collection interface
derives.

### 13-iterators-and-pipes

Iter protocol, the `Iter` module, lazy adapters, terminals, materializers,
pipe dispatch, callback control flow, loop control, sort constraints, and custom
iterators.

### 14-modules-and-packaging

Module/project behavior: item imports, aliases, module imports, visibility
chains, public re-exports, and multi-module project fixtures.

### 15-app-and-defer

Runtime app value and cleanup behavior: boot app values, scoped `with`
overrides, context values, process `os` access, deferred cleanup, and config
patterns.

### 16-concurrency

Structured concurrency: `concurrent`, `async`, `await`, task lifetime rules,
channels, cancellation, timeouts, and pipeline/concurrency examples.

### 17-typed-literals

Typed literals and literal handlers: `Toml`, fragments, multi-literal modules,
SQL fragments, and type-attached literals.

### 18-ffi-and-dynamic

Boundary values and decoding fixtures: `Dynamic`, JSON, and FFI-shaped examples.
`tagged_ffi_app/` is a browsable `nomi run` and `nomi test` fixture for
Nomi-side Go FFI binding discovery.

### 19-locales

Locales and plural rules: `Locale` parsing, canonicalization, POSIX values,
CLDR fallback chains and negotiation; plural categories; an app that keeps
its locale in an application field; locale-aware number formatting and
parsing and list joining; and a project whose `nomi.toml` lists its locales
under `[i18n]` (`listed_locales/`), with how an unlisted locale falls back;
and a shop whose `message` declarations are translated in catalogs
(`shop/`, with `locales/<tag>/cart.toml`): plurals, an enum selector,
fallbacks through `pt-PT` and partial locales, right-to-left isolation and
the pseudo-locales; and its `noun` declarations (`order.nomi`): Polish and
Portuguese gender agreement, a case form asked for with `case=genitive`, a
noun's form following a count, `:nomi:list` with the verb agreeing with the
list's length, and `:nomi:weekday`.

### 20-data-access

Data access vocabulary: `Uuid` parsing, bytes, typed errors, v4 and v7
generation, v7 ordering under a virtual clock, and UUIDs as map keys.

## Adding Or Moving A Program

Put the file in the category that owns the behavior being exercised. If a file
mainly tests test infrastructure, tooling, formatting, or diagnostics, prefer
`02-testing` or `03-tooling-and-diagnostics` over `01-foundations`.

When a fixture needs reusable helpers, keep those helpers in the fixture's own
directory. Duplicate a small helper rather than sharing one across categories;
the test program should stay self-contained and easy to render or copy.
