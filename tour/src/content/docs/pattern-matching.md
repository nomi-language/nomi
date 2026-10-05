---
title: "Pattern Matching, Maybe, Result & Try"
description: "`case` destructures values by shape; `Maybe<T>` and `Result<T, E>` are its canonical use case, with `try` to short-circuit failures."
---

A `case` expression takes a value and runs the first arm whose pattern matches.
The compiler enforces **exhaustiveness** — miss a variant of an enum and the
build fails, not a runtime surprise. We saw the literal-pattern shape in
[Bindings & Expressions](/bindings-and-expressions/); this chapter goes deeper
into patterns, [`Maybe<T>`](/reference/maybe/), [`Result<T, E>`](/reference/results/), and `try`.

## Binding patterns and guards

A bare identifier in a pattern position binds the matched value to that name.
Add `when <cond>` to gate an arm on a runtime check:

```nomi-run
fn classify(n: Int): String {
    case n {
        0 -> "zero"
        n when n < 0 -> "negative"
        n when n > 100 -> "huge"
        _ -> "regular"
    }
}

fn main(): String {
    dbg classify(0)
    dbg classify(-5)
    dbg classify(500)
    dbg classify(42)
}
```
<!-- expect
dbg line 11: classify(0) = "zero"
dbg line 12: classify(-5) = "negative"
dbg line 13: classify(500) = "huge"
dbg line 14: classify(42) = "regular"
-->

## Destructuring

The real power of `case` is taking apart structured values. Reusing the
`Shape` enum from [Structs, Enums, Distinct Types](/structs-enums-distinct/):

```nomi-run
enum Shape {
    Circle Float
    Rectangle {width: Float, height: Float}
}

fn area(s: Shape): Float {
    case s {
        .Circle(r) -> r * r * 3.14159
        .Rectangle{width, height} -> width * height
    }
}

fn main(): Float {
    dbg area(.Circle(3.0))
    dbg area(.Rectangle{width: 4.0, height: 5.0})
}
```
<!-- expect
dbg line 14: area(.Circle(3.0)) = 28.27431
dbg line 15: area(.Rectangle{width: 4.0, height: 5.0}) = 20.0
-->

Tuples and structs destructure the same way: `(a, b) -> …`, `Point{x, y} -> …`.

## `Maybe<T>` and `Result<T, E>`

The two enums Nomi reaches for whenever an operation can have a "no answer"
case. [`Maybe<T>`](/reference/maybe/) is `None | Some(T)` — for an absence with no further
explanation. [`Result<T, E>`](/reference/results/) is `Ok(T) | Err(E)` — when failure carries a
description. Both types and their bare constructors are in scope by default.

```nomi-run
fn lookup(id: Int): Maybe<String> {
    case id {
        1 -> Some("Alice")
        2 -> Some("Bob")
        _ -> None
    }
}

fn name_for(id: Int): String {
    case lookup(id) {
        Some(name) -> name
        None -> "unknown"
    }
}

fn main(): String {
    dbg name_for(1)
    dbg name_for(99)
}
```
<!-- expect
dbg line 17: name_for(1) = "Alice"
dbg line 18: name_for(99) = "unknown"
-->

For a small, one-off match, `if` can bind a pattern directly. The names from
the pattern are visible only in the success branch:

```nomi-run
fn lookup(id: Int): Maybe<String> {
    case id {
        1 -> Some("Alice")
        _ -> None
    }
}

fn label_for(id: Int): String {
    if Some(name) = lookup(id) {
        name
    } else {
        "unknown"
    }
}

fn main(): String {
    dbg label_for(1)
    dbg label_for(9)
}
```
<!-- expect
dbg line 17: label_for(1) = "Alice"
dbg line 18: label_for(9) = "unknown"
-->

## The `try` keyword

When a function returns `Maybe<T>` or `Result<T, E>` and you want to
short-circuit on the failure case, prefix the call with `try`: the wrapped value is
unwrapped on success and the whole function returns immediately on failure.

```nomi-run
fn safe_div(a: Int, b: Int): Result<Int, String> {
    if b == 0 {
        Err("div by zero")
    } else {
        Ok(a / b)
    }
}

fn calculate(x: Int, y: Int): Result<Int, String> {
    half = try safe_div(x, 2)
    ratio = try safe_div(half, y)
    Ok(ratio + 1)
}

fn main() {
    dbg calculate(100, 5)
    _ = dbg calculate(100, 0)
}

```
<!-- expect
dbg line 16: calculate(100, 5) = Ok(11)
dbg line 17: calculate(100, 0) = Err("div by zero")
-->

`main` may return a `Result` too, and then `try` works in it. A `main` that
returns `Err` fails the program: it prints `error: ` and the error to stderr
and exits with status 1. The error prints through `Display` when its type
has one, so a `String` prints as itself, and through `Debug` otherwise.

```nomi
fn main(): Result<Unit, String> {
    ratio = try calculate(100, 0)
    dbg ratio
    Ok(Unit)
}
```

This `main` prints `error: div by zero` to stderr and exits 1.

`try` is **lambda-scoped**: inside a lambda, `try` bubbles to the lambda's own
boundary (like `return`), not the enclosing function. Nomi has no non-local
returns.

To parse a list of inputs, map each one to a `Result` and finish the pipe with
`try Result.collect()`. It gives the list of values when every input parsed,
and otherwise passes the first `Err` up; it stops reading at that error. When
you want every error rather than the first, `Result.partition` splits the
results into the values and the errors. `Maybe.collect` and `Maybe.values` do
the same for `Maybe`.

```nomi-run
fn parse(text: String): Result<Int, String> {
    Maybe.to_result(String.to_int(text), "not a number: " + text)
}

fn total(inputs: List<String>): Result<Int, String> {
    numbers = inputs |> Iter.map(|s| parse(s)) |> try Result.collect()

    Ok(Iter.reduce(numbers, |acc = 0, n| acc + n))
}

fn main() {
    dbg total(["1", "2", "3"])
    dbg total(["1", "x", "y"])
    (numbers, errors) = ["1", "x", "y"] |> Iter.map(|s| parse(s)) |> Result.partition()

    dbg numbers
    _ = dbg errors
}
```
<!-- expect
dbg line 12: total(["1", "2", "3"]) = Ok(6)
dbg line 13: total(["1", "x", "y"]) = Err("not a number: x")
dbg line 16: numbers = [1]
dbg line 17: errors = ["not a number: x", "not a number: y"]
-->

## Binding with `else`

`try` passes an error up unchanged. When a step's failure should be handled
where it happens, bind the pattern you want and say what runs when the value
does not match it. The pattern's names are in scope for the rest of the block.
The `else` holds either a block or case arms over the value that failed, and
each path through it must leave (`return`, or `break` and `continue` in a loop
callback) or supply a fallback for the pattern's payload:

```nomi-run
enum PortError {
    Missing
    Invalid String
}

fn parse_port(text: Maybe<String>): Result<Int, PortError> {
    Some(raw) = text else { return Err(.Missing) }

    Some(n) = String.to_int(raw) else {
        return Err(.Invalid(raw))
    }

    Ok(n)
}

fn port(text: Maybe<String>): Result<Int, String> {
    Ok(n) = parse_port(text) else {
        Err(.Missing) -> 8080
        Err(.Invalid(raw)) -> return Err("bad port: ${raw}")
    }

    Ok(n)
}

fn main(): Result<Int, String> {
    dbg port(Some("3000"))
    dbg port(Some("eighty"))
    dbg port(None)
}
```
<!-- expect
dbg line 26: port(Some("3000")) = Ok(3000)
dbg line 27: port(Some("eighty")) = Err("bad port: eighty")
dbg line 28: port(None) = Ok(8080)
-->

`parse_port` uses the block form: each binding names the error to return when
its value does not match. `port` uses case arms over the error that came back,
and the arms take different paths. A missing port falls back to `8080`, which
stands in for the `Ok` payload, so `n` is bound either way; an invalid one
returns an error naming the bad text. A fallback works only where the pattern
has one payload to stand in for; `[first, ..rest] = xs else { ... }` must leave.

The next chapter — [Interfaces & Dispatch](/interfaces-and-dispatch/) — covers
how Nomi composes behavior across types: `interface`, `impl` blocks, dispatch
resolution, and the universal `Debug` that's been powering `dbg` for every
value you've seen.
