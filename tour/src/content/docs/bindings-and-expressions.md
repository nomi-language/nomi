---
title: "Bindings & Expressions"
description: The shape of a Nomi program — values, bindings, expressions, and the constructs you'll use in almost every file.
---

A Nomi program is a set of declarations. Execution starts at `fn main`. This
chapter covers the bones: values, bindings, expressions, and the handful of
constructs you'll use in almost every file.

## A first program

For quick tracing, use `dbg`. It prints the source location, the expression,
and the value's debug rendering, then returns the original value:

```nomi-run
fn main() {
    greeting = "Hello, Nomi!"
    dbg greeting
}

```
<!-- expect
dbg line 3: greeting = "Hello, Nomi!"
-->

You'll see `dbg` throughout the tour because it works for every value and is
transparent in expressions and pipelines.

For user-facing output, [`std/io`](/reference/io/) gives you two printers that cover almost
every need:

```nomi-run
import std/io

fn main() {
    io.print("Hello, Nomi!")
    io.inspect("Hello, Nomi!")
}

```
<!-- expect
Hello, Nomi!
"Hello, Nomi!"
-->

[`io.print`](/reference/io/#ioprint) writes a value in its **user-facing** form — strings (no quotes),
numbers, booleans, and any type that opts in to `Display` (a contract we'll
meet in [Interfaces & Dispatch](/interfaces-and-dispatch/)). [`io.inspect`](/reference/io/#ioinspect)
writes **any** value in a debug-friendly form (note the quotes around the
string) and returns [`Unit`](/reference/unit/). `Unit` is the single value used when an expression
has nothing interesting to return. Use `io.inspect` when you want output as a
statement; use `dbg` when you want to inspect while keeping the value in an
expression or pipeline. When you build your own types later in the tour,
`io.inspect` is the printer that always works.

Because `dbg` returns the original value, it works as a pipe stage:

```nomi-run
import std/io

fn double(n: Int): Int {
    n * 2
}

fn main() {
    5
    |> double()
    |> dbg
    |> double()
    |> io.print()
}

```
<!-- expect
dbg line 10:
  5
  |> double()
  = 10
20
-->

The pipe operator `|>` passes each stage's result into the next stage below it.
The pipeline above is the same calculation as
`io.print(double(dbg double(5)))`: `dbg` inspects the intermediate value without
changing it. [Pipes](/pipes/) goes deeper later.

## Tiny functions

Functions give a name to an expression you want to reuse. The pocket version
is `fn name(params): ReturnType { body }`, and the body's last expression is
the return value:

```nomi-run
fn double(n: Int): Int {
    n * 2
}

fn main() {
    dbg double(4)
}

```
<!-- expect
dbg line 6: double(4) = 8
-->

That is enough for small helpers and examples. The full function model,
including defaults, lambdas, trailing callbacks, destructuring parameters, and
higher-order code, gets its own chapter in
[Functions & Lambdas](/functions-and-lambdas/).

## Comments and tiny tests

Nomi code tends to keep comments, docs, and tests close to the declarations
they describe. The tour introduces all three; the standard library leans on
them heavily, and its generated reference is built from those same docs and
attached examples.

Use `//` for ordinary comments near the code they explain. Use `//#` for
file-level documentation, and `///` directly above a declaration for
documentation that appears in hovers and generated reference pages:

```nomi-run
//# Geometry helpers.

/// Returns the area of a rectangle.
fn area(width: Int, height: Int): Int {
    // Rectangles multiply their sides.
    width * height
}

fn main() {
    dbg area(6, 7)
}

```
<!-- expect
dbg line 10: area(6, 7) = 42
-->

Tests can live in the same `.nomi` files as regular code, close to the
behavior they protect. A regular test names the behavior and runs a block:

```nomi-run
fn double(n: Int): Int {
    n * 2
}

test "double doubles its input" {
    assert double(4) == 8
    refute double(4) == 9
}

```

When tiny examples belong directly with a declaration, write them above the
declaration as `//!` attached tests. `nomi test` runs them, and editors treat
them as code, with hover and diagnostics. Editors draw them dimmed so they
don't compete with the code below; Neovim and VS Code show the one under the
cursor in full color. For a real stdlib example, see
[`String.contains?`](/reference/strings/#stringcontains), whose reference entry
is generated from attached tests like these.

```nomi-run
//! assert double(4) == 8
//! refute double(4) == 9
fn double(n: Int): Int {
    n * 2
}

```

Use `assert` and `refute` for tests: `test` blocks and `//!` attached
examples. In non-test code, expected failures are usually represented with
[`Result`](/reference/results/) or [`Maybe`](/reference/maybe/), then handled explicitly with `case` or short-circuited
with `try`. The [Pattern Matching, Maybe, Result & Try](/pattern-matching/)
chapter covers those forms, and [Testing](/testing/) covers named `test`
blocks, `refute`, setup, pattern assertions, and captured checks.

## Bindings

You introduce a name with `name = expression`. Bindings are immutable: a name
stands for one value. In a binding position, `=` is a match/bind separator, not
assignment: the expression on the right is matched by the binding shape on the
left.

```nomi-run
fn main() {
    name = "Nomi"
    port = 8080
    dbg name
    dbg port
}

```
<!-- expect
dbg line 4: name = "Nomi"
dbg line 5: port = 8080
-->

A binding's type is inferred from the expression on the right. Later in this
chapter, [Type annotations](#type-annotations) shows when to write that type
explicitly.

Every ordinary binding must be read. If a value is intentionally ignored, bind
it to `_` or a descriptive discard name such as `_unused_port`:

```nomi-run
fn main() {
    _ignored_count = 3
    dbg "done"
}

```
<!-- expect
dbg line 3: "done" = "done"
-->

The same rule applies to expression statements. A non-final expression can
stand alone only when it returns `Unit`, such as `io.print(...)` or
`io.inspect(...)`. If it returns
a value, either use the value, inspect it with `dbg`, or explicitly discard it:

```nomi-run
fn compute(): Int {
    42
}

fn main() {
    _ = compute()
    dbg compute()
    Unit
}

```
<!-- expect
dbg line 7: compute() = 42
-->

By contrast, this does not use or discard the `Int` returned by `compute()`:

```nomi-run ignore
fn compute(): Int {
    42
}

fn main() {
    compute()
    Unit
}
```

Only `Unit` results can stand alone before the end of a block. Write
`_ = compute()` when discarding the value is intentional. If `compute()` were
the final expression instead, it would be `main`'s result, and `main` may
only return `Unit` or `Result<Unit, E>`, so `_ = compute()` is still the fix.

You can reuse a name within a scope: the new binding **shadows** the previous
one, so from there the name refers to the new value. This isn't mutation — each
binding is still immutable; shadowing just introduces a fresh binding over the
old name (top-level declarations like functions and types can't be redeclared
this way).

The natural shape is to **thread a value through successive refinements**, each
line transforming the previous binding:

```nomi-run
fn main() {
    name = "  JANE  "
    name = String.trim(name)
    name = String.to_lower(name)
    dbg name
}

```
<!-- expect
dbg line 5: name = "jane"
-->

Each `name = …` reads the prior `name` and rebinds it — no mutation, just a new
binding shadowing the old one. When every step feeds directly into the next,
a vertical pipeline often expresses the same flow more cleanly:

```nomi
"  JANE  "
|> String.trim()
|> String.to_lower()
|> dbg
```

The shape you should *not* reach for is a shadowed value that's never read
before it's replaced (`name = "Jane"; name = "John"`): that reads as a likely
bug rather than a refinement.

## Type annotations

Most bindings don't need one — Nomi's inference reads the type from the
right-hand side, and leaving the annotation off keeps signatures clean.
Reach for an explicit annotation when inference *can't* see the type.
The canonical case: a constructor that takes no value of the type it
returns, so the compiler has nothing to read from.

```nomi-run
fn main() {
    // LHS annotation — `Map<String, Int>` tells the compiler what K and V
    // are. `Map.empty()` alone has nothing to infer from.
    scores: Map<String, Int> = Map.empty()
    Map.size(scores) |> dbg

    // RHS turbofish — same job, supplied on the call instead of the
    // binding. Reach for it when the value isn't being bound — e.g. it
    // flows straight through a pipe.
    Map.empty<String, Int>()
    |> Map.size()
    |> dbg
}

```
<!-- expect
dbg line 5: Map.size(scores) = 0
dbg line 12:
  Map.empty<String, Int>()
  |> Map.size()
  = 0
-->

Without one of these annotations, the compiler reports "cannot infer type" —
it genuinely requires the information. Pick whichever form reads more cleanly
at the call site: the LHS annotation when you're naming the value, the
turbofish (`f<T>(...)`, `mod.f<T>(...)`) when you're not.

## Blocks are expressions

A `{ ... }` block is an expression: it runs its statements and evaluates to its
last expression. That makes it a tidy way to name intermediate steps without
leaking them into the surrounding scope:

```nomi-run
fn main() {
    total = {
        a = 10
        b = 20
        a + b
    }

    dbg total
}

```
<!-- expect
dbg line 8: total = 30
-->

## `if`/`else` and `case` are expressions

Because blocks are expressions, control flow is too: `if`/`else` and `case`
both produce a value you can bind. `if` conditions are booleans; `case x { … }`
matches the value's shape; `case { … }` (no scrutinee) is the multi-way
conditional that replaces long `else if` chains. [Pattern Matching](/pattern-matching/) shows the
refutable `if Pattern = expr` form. Here, focus on the expression shape:

```nomi-run
fn main() {
    x = 42

    size = if x > 50 { "big" } else { "small" }
    dbg size

    label = case x {
        0 -> "zero"
        1 -> "one"
        _ -> "many"
    }

    dbg label

    range = case {
        x < 0 -> "negative"
        x < 10 -> "small"
        x < 100 -> "medium"
        _ -> "large"
    }

    dbg range
}
```
<!-- expect
dbg line 5: size = "small"
dbg line 13: label = "many"
dbg line 22: range = "medium"
-->

## `once` — file-level values

Top-level "set once, never changes" values are declared with `once`. The
right-hand side is evaluated lazily the first time the value is used, then
cached for later uses, so it's the natural home for configuration constants and
derived values:

```nomi-run
once max_retries = 3
once timeout_ms = 30 * 1000

fn main() {
    dbg max_retries
    dbg timeout_ms
}

```
<!-- expect
dbg line 5: max_retries = 3
dbg line 6: timeout_ms = 30000
-->

:::note[Continue or dig deeper]
The tour moves on to [**Functions & Lambdas**](/functions-and-lambdas/) —
the units of code that put these values to work. Or stay here for the
deep-dive on Nomi's inference policy below.
:::

## Going deeper

### Type inference: local unification, explicit boundaries

Within a function body, Nomi uses familiar local unification:

- **Call-site unification.** `Iter.map(xs, |x| x + 1)` pins `xs`'s element
  type and the lambda's parameter type *together*, in one step.
- **Generic return propagation.** `Ok(42)` produces `Result<Int, ?>`; the
  unsolved `?` flows up until something in the surrounding context pins
  it (a binding annotation, a function return type, a `case` arm).
- **Lambda parameters from a function-typed context.** Given
  `f: (Int) -> Int = |x| x + 1`, the lambda's parameter takes its type
  from the annotation — no `|x: Int|` needed.
- **Bidirectional flow from LHS annotations.** `m: Map<String, Int> = Map.empty()`
  pushes `Map<String, Int>` into the polymorphic `empty()`'s return.

Nomi deliberately draws boundaries around that inference. Two rules:

**1. Function signatures must be annotated explicitly.**

Parameter types and every non-`Unit` return type are required at every
`fn`. Omitting the return annotation means the function returns `Unit`.
There's no whole-signature-from-body inference. Three reasons stack behind
the rule:

- **Readability at the declaration site.** A signature is the function's
  published contract; readers, reviewers, IDEs, and stack traces see
  the types at the source where they're declared, not by replaying
  inference across the call graph to figure out what each function ended
  up being.
- **Clearer diagnostic boundaries.** A body edit can't silently change the
  inferred signature of a function and push the resulting mismatch into some
  downstream caller. Type errors can still require context to understand, but
  the checker doesn't infer across function signatures that the source never
  wrote down.
- **Stable contracts under body edits.** Changing the body of an
  annotated function can't silently widen or narrow what the function
  *appears* to be. The signature is the contract; callers' types stay
  valid as long as the signature holds. Bodies and contracts evolve on
  separate axes.

**2. Nomi currently requires every binding to be locally determined.**

A binding's type has to be readable where the binding is written: either from
an annotation on the binding, or from enough information in the right-hand
expression and its immediate expected type. This is independent of how far
inference *could* reach:

```nomi
m = Map.empty() // Map<?, ?> — locally undetermined

m = Map.put(m, "alice", 90) // K=String, V=Int could be solved
// from this line — but line 1 is
// rejected anyway.
```

The checker isn't blind — inference would in fact solve `m`'s type
from the later use. The point is that solvability isn't the bar. A
separate **policy** sits in front of inference and rejects any binding
whose type can't be read locally, regardless of what the checker would
discover by looking further. A reader scanning top-to-bottom shouldn't
need to peek ahead to learn what `m` is.

The fix is the form you've already seen — an LHS annotation, or a
turbofish on the RHS:

```nomi
m: Map<String, Int> = Map.empty() // LHS annotation

m = Map.empty<String, Int>() // RHS turbofish
```

The key split is that inference and the requirement to annotate are
decoupled. Inference is a capability the checker maximizes; annotation
discipline is a **policy**, applied where reading top-to-bottom would otherwise
force a forward scan.

The result is a language where most body-level lines don't need annotations,
but the boundaries that matter for reading and review do. The two ends are
independent: inside, lean as far as the checker reaches; at signatures and
locally-undetermined bindings, name the type.

That's the binding-and-expression shape. [Functions & Lambdas](/functions-and-lambdas/)
introduces the units of code that put these values to work.
