# Nomi Test Programs

This directory is the runnable corpus for `nomi test`. It is also a browsable
library of small language examples. The numbered folders are ordered for
scanning; the numbers are only there to keep the categories in a stable
sequence.

## File Shapes

- `*_test.nomi` files are assertion tests run by `nomi test`.
- Root-level `*_test.nomi` files should be self-contained except for stdlib
  imports.
- Test programs with local support modules live in a same-category directory.
  The directory contains the `*_test.nomi` entry file plus every local support
  module it imports, all at one flat level.
- Plain `.nomi` files inside those directories are support modules imported by
  that test program.
- App-shaped examples include a real entry file with `fn main` and
  `pub fn boot(): App`, or `pub fn boot(startup: Startup): App` when boot reads
  startup input. A `*_test.nomi` either runs that entry file with
  `compiler.run_file(RunFile{entry_point: "main"})` and asserts the captured
  output (use `RunFile.env` when the app's `boot` reads `startup.env`), or
  imports it and names its boot in a group: `boot main.boot()`, or
  `boot main.boot(Startup{args: ["-v"]})` for a boot that takes a Startup. A
  test that reads app fields needs such a group, so its directory holds an
  entry file even when the test is the only other file. Every entry's `main`
  must run.
- Diagnostic and runtime failure cases should be written as ordinary tests that
  call `compiler.check`, `compiler.check_project`, `compiler.run`, or another
  explicit API.

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

The Nomi test surface itself: `test`, `tests`, setup values bound by a test's pattern,
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

## Adding Or Moving A Program

Put the file in the category that owns the behavior being exercised. If a file
mainly tests test infrastructure, tooling, formatting, or diagnostics, prefer
`02-testing` or `03-tooling-and-diagnostics` over `01-foundations`.

When a fixture needs reusable helpers, keep those helpers in the fixture's own
directory. Duplicate a small helper rather than sharing one across categories;
the test program should stay self-contained and easy to render or copy.
