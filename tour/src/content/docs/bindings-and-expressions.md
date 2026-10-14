---
title: "Bindings & Expressions"
description: Values, bindings, blocks, if and case, and the other pieces you'll use in almost every Nomi file.
---

A Nomi program is a list of declarations, and it starts running at
`fn main`. This chapter covers the pieces you'll use in almost every file:
values, bindings, blocks, and `if` and `case`.

## A first program

```nomi-run
fn main() {
    greeting = "Hello, Nomi!"
    dbg greeting
}

```
<!-- expect
dbg line 3: greeting = "Hello, Nomi!"
-->

`dbg` prints the line it is on, the expression, and the value. Then it
returns the value unchanged, so you can put it in the middle of an
expression. It works on every value, and the tour uses it throughout.

To print output for the people using your program, use
[`std/io`](/reference/io/):

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

[`io.print`](/reference/io/#ioprint) prints a value the way a user should see
it. A string prints without quotes. It works for strings, numbers, booleans,
and any type that implements `Display`, which
[Interfaces & Dispatch](/interfaces-and-dispatch/) covers.

[`io.inspect`](/reference/io/#ioinspect) prints any value in its debug form,
which is why the string has quotes. When you define your own types later,
`io.inspect` can print them with no extra work.

`io.inspect` returns [`Unit`](/reference/unit/), the value an expression
gives when it has nothing useful to return. So use `io.inspect` as a
statement on its own line. Use `dbg` when you want to look at a value and
keep using it.

Because `dbg` returns its value, it can be a stage in a pipeline:

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

The pipe operator `|>` passes each stage's result to the stage below it.
This pipeline computes `io.print(double(dbg double(5)))`. The `dbg` stage
prints the value in the middle and passes it on. [Pipes](/pipes/) covers
pipelines in detail.

## Tiny functions

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

A function is written `fn name(params): ReturnType { body }`. The last
expression in the body is the value the function returns.

That is enough for the examples in this chapter.
[Functions & Lambdas](/functions-and-lambdas/) covers the rest: default
values, lambdas, callbacks, and parameters that unpack their arguments.

## Comments and tiny tests

Nomi keeps comments, documentation and tests next to the code they describe.

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

There are three kinds of comment:

- `//` is an ordinary comment.
- `//#` documents the whole file.
- `///` documents the declaration below it. Editors show it on hover, and
  the generated reference pages are built from it.

A test can live in the same file as the code it checks:

```nomi-run
fn double(n: Int): Int {
    n * 2
}

test "double doubles its input" {
    assert double(4) == 8
    refute double(4) == 9
}

```

`assert` fails the test when its condition is false. `refute` fails it when
its condition is true.

For a small example, write it directly above the declaration with `//!`:

```nomi-run
//! assert double(4) == 8
//! refute double(4) == 9
fn double(n: Int): Int {
    n * 2
}

```

`nomi test` runs these as tests. Editors treat them as code, with hovers and
errors. They are drawn dimmed so they stand back from the code below. Neovim
and VS Code show the line under the cursor in full color. The standard
library uses them for its examples: the reference entry for
[`String.contains?`](/reference/strings/#stringcontains) is generated from
them.

`assert` and `refute` are for tests. Ordinary code reports failure with a
[`Result`](/reference/results/) or a [`Maybe`](/reference/maybe/) value,
which the caller handles with `case` or passes on with `try`.
[Pattern Matching, Maybe, Result & Try](/pattern-matching/) covers those.
[Testing](/testing/) covers the rest of testing: setup, pattern assertions
and captured checks.

## Bindings

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

`name = expression` gives a name to a value. There is no `let`. A binding
never changes: the name stands for that one value.

The `=` does not assign to a variable. It matches the value on the right
against the name or pattern on the left. Later chapters use patterns there
to unpack values.

Nomi works out a binding's type from the expression on the right.
[Type annotations](#type-annotations) below shows when you need to write the
type yourself.

Every binding must be read. If you mean to ignore a value, bind it to `_`
or to a name that starts with `_`, such as `_unused_port`:

```nomi-run
fn main() {
    _ignored_count = 3
    dbg "done"
}

```
<!-- expect
dbg line 3: "done" = "done"
-->

The same rule applies to an expression on a line by itself. Before the last
line of a block, such an expression must return `Unit`, as `io.print(...)`
does. If it returns a value, use the value, look at it with `dbg`, or
discard it with `_ =`:

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

This version is an error, because nothing uses the `Int` that `compute()`
returns:

```nomi-run ignore
fn compute(): Int {
    42
}

fn main() {
    compute()
    Unit
}
```

Write `_ = compute()` to discard it on purpose. Moving `compute()` to the
last line doesn't help either. It would become `main`'s result, and `main`
can only return `Unit` or `Result<Unit, E>`.

You can bind a name that is already in use. The new binding shadows the old
one: from that line on, the name means the new value. Nothing is mutated.
Functions and types at the top of a file can't be redeclared this way.

Shadowing is useful when you refine a value one step at a time:

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

Each line reads the previous `name` and binds a new one. When each step
feeds straight into the next, a pipeline says the same thing more simply:

```nomi
"  JANE  "
|> String.trim()
|> String.to_lower()
|> dbg
```

A binding that is replaced before anything reads it is an error. In
`name = "Jane"` followed by `name = "John"`, the compiler reports that the
first `name` is never read.

## Type annotations

Most bindings don't need a type written out, because Nomi reads it from the
right-hand side. You need one when the right-hand side doesn't say what the
type is. The usual case is a function that creates an empty value:

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

`Map.empty()` alone could make a map of any key and value types, so you say
which. There are two ways:

- Annotate the binding: `scores: Map<String, Int> = Map.empty()`. Use this
  when you name the value.
- Give the type arguments on the call: `Map.empty<String, Int>()`. This form
  is called a turbofish. Use it when the value goes straight into a pipe or
  another call.

Without either, the compiler reports that the binding's type is not locally
determined.

## Blocks are expressions

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

A `{ ... }` block runs its lines and gives the value of its last expression.
Names bound inside the block, like `a` and `b`, are not visible outside it.
That makes a block a tidy place for intermediate steps.

## `if`/`else` and `case` are expressions

`if`/`else` and `case` also give values, so you can bind their result:

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

- `if` takes a `Bool` condition.
- `case x { ... }` compares `x` against each pattern in turn and uses the
  first arm that matches. `_` matches anything.
- `case { ... }`, with nothing after `case`, takes the first arm whose
  condition is true. Use it instead of a long `else if` chain.

[Pattern Matching](/pattern-matching/) covers patterns in full, including
the `if Pattern = expr` form.

## `once` — file-level values

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

`once` declares a value at the top level of a file. Its expression runs the
first time the value is used, and later uses get the saved result. Use it
for constants and configuration.

:::note[Continue or dig deeper]
The next chapter is [Functions & Lambdas](/functions-and-lambdas/). The
section below explains how far Nomi's type inference reaches, if you want
the detail now.
:::

## Going deeper

### Type inference: local unification, explicit boundaries

Inside a function body, Nomi works out types from how values are used:

- In `Iter.map(xs, |x| x + 1)`, the type of `xs`'s elements and the type of
  the lambda's `x` are worked out together.
- `Ok(42)` is a `Result<Int, ?>`. The error type `?` stays open until
  something nearby fixes it: a binding's annotation, the function's return
  type, or another `case` arm.
- In `f: (Int) -> Int = |x| x + 1`, the lambda's `x` gets its type from the
  annotation, so you don't write `|x: Int|`.
- In `m: Map<String, Int> = Map.empty()`, the annotation tells `empty()`
  which map to make.

Inference stops at two boundaries.

**Function signatures are written out.** Every `fn` writes its parameter
types, and its return type unless it returns `Unit`. Leaving the return
type off means the function returns `Unit`. Nomi never works out a
signature from the body.

This keeps a function's types visible where it is declared. Readers,
reviewers and editors see them without tracing other code. If you change a
function's body, its signature stays the same, so callers keep working or
the error appears in the function you changed rather than in some caller.

**Each binding's type is settled where it is written.** The type has to come
from an annotation on the binding, or from the expression on the right and
what is expected of it there. Later lines don't count:

```nomi
m = Map.empty() // Map<?, ?> — locally undetermined

m = Map.put(m, "alice", 90) // K=String, V=Int could be solved
// from this line — but line 1 is
// rejected anyway.
```

The second line would tell the compiler that `m` maps `String` to `Int`.
Nomi rejects the first line anyway. This is a deliberate rule: someone
reading from the top should know what `m` is without looking ahead.

The fix is one of the two forms from earlier, an annotation or a turbofish:

```nomi
m: Map<String, Int> = Map.empty() // LHS annotation

m = Map.empty<String, Int>() // RHS turbofish
```

Most lines in a function body need no annotation. You write types at
signatures, and on the few bindings whose type the line itself doesn't show.

[Functions & Lambdas](/functions-and-lambdas/) is next.
