---
title: "Pattern Matching, Maybe, Result & Try"
description: "`case` takes values apart by shape. Maybe and Result hold an answer or its absence, and `try` passes a failure up."
---

A `case` expression takes a value and runs the first arm whose pattern matches
it. You saw simple `case` expressions in
[Bindings & Expressions](/bindings-and-expressions/). This chapter covers more
kinds of pattern, then [`Maybe<T>`](/reference/maybe/),
[`Result<T, E>`](/reference/results/) and `try`.

A `case` must cover every possible value. If you leave out a variant of an
enum, the program does not compile. You find out when you build, not when the
program runs.

## Binding patterns and guards

```nomi-run
fn classify(n: Int) -> String {
    case n {
        0 -> "zero"
        n when n < 0 -> "negative"
        n when n > 100 -> "huge"
        _ -> "regular"
    }
}

fn main() {
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

A plain name in a pattern, like `n`, matches any value and gives it that name.
`when` adds a condition, called a guard. The arm runs only if the condition is
true. `_` matches anything and names nothing.

## Destructuring

Patterns can also take a value apart. This is the `Shape` enum from
[Structs, Enums, Distinct Types](/structs-enums-distinct/):

```nomi-run
enum Shape {
    Circle Float
    Rectangle {width: Float, height: Float}
}

fn area(s: Shape) -> Float {
    case s {
        .Circle(r) -> r * r * 3.14159
        .Rectangle{width, height} -> width * height
    }
}

fn main() {
    dbg area(.Circle(3.0))
    dbg area(.Rectangle{width: 4.0, height: 5.0})
}
```
<!-- expect
dbg line 14: area(.Circle(3.0)) = 28.27431
dbg line 15: area(.Rectangle{width: 4.0, height: 5.0}) = 20.0
-->

`.Circle(r)` matches a circle and names its radius `r`.
`.Rectangle{width, height}` matches a rectangle and names both fields.

Tuples and structs work the same way: `(a, b) -> ...` and
`Point{x, y} -> ...`.

### Naming the whole match with `as`

Sometimes an arm needs both the parts and the whole value. `pattern as name`
matches the pattern and also gives the whole value a name:

```nomi-run
enum Shape {
    Circle Float
    Rectangle {width: Float, height: Float}
}

fn area(s: Shape) -> Float {
    case s {
        .Circle(r) -> r * r * 3.0
        .Rectangle{width, height} -> width * height
    }
}

fn describe(s: Shape) -> String {
    case s {
        .Rectangle{width, height} as square when width == height -> "a square of area ${area(square)}"
        other -> "a shape of area ${area(other)}"
    }
}

fn main() {
    dbg describe(.Rectangle{width: 2.0, height: 2.0})
    dbg describe(.Circle(1.0))
}
```
<!-- expect
dbg line 21: describe(.Rectangle{width: 2.0, height: 2.0}) = "a square of area 4.0"
dbg line 22: describe(.Circle(1.0)) = "a shape of area 3.0"
-->

The first arm reads `width` and `height`, and passes the whole rectangle to
`area` as `square`. Without `as`, it would have to build the rectangle again.

`as` names the whole pattern to its left. `Ok(x) as r` names the whole `Result`.
`Ok(Point{x, y} as p)` names only the `Point` inside. The name has the type of
the value it names, so `square` is a `Shape`.

`as` works anywhere a pattern does, including `if`, bindings, parameters and
inside other patterns.

### Matching alternatives with `or`

`A or B` matches a value that either pattern matches, so one arm can handle
several shapes:

```nomi-run
enum Shape {
    Circle Float
    Square Float
    Rectangle {width: Float, height: Float}
}

fn size(s: Shape) -> String {
    case s {
        .Circle(r) or .Square(r) when r > 10.0 -> "big, ${r} across"
        .Circle(r) or .Square(r) -> "${r} across"
        .Rectangle{width, height} -> "${width} by ${height}"
    }
}

fn closes(c: Codepoint) -> Bool {
    case c {
        ')' or ']' or '}' -> True
        _ -> False
    }
}

fn main() {
    dbg size(.Square(12.0))
    dbg size(.Circle(2.0))
    dbg closes(']')
    dbg closes('x')
}
```
<!-- expect
dbg line 23: size(.Square(12.0)) = "big, 12.0 across"
dbg line 24: size(.Circle(2.0)) = "2.0 across"
dbg line 25: closes(']') = True
dbg line 26: closes('x') = False
-->

Every alternative must bind the same names, with the same types. Here both
bind `r` as a `Float`, and `r` holds the value from whichever one matched.
The `when` guard applies to the whole arm: if it is false, matching goes on to
the next arm.

`or` works inside other patterns too, as in `Some(.Circle(_) or .Square(_))`
or `[')' or ']' as closer, ..rest]`. A trailing `as` names the whole value
whichever alternative matched. Together, the alternatives count toward
covering every value, so `.Red or .Green` and `.Blue` cover a three-color enum
without `_`.

## `Maybe<T>` and `Result<T, E>`

Nomi uses two enums for operations that might not have an answer:

- [`Maybe<T>`](/reference/maybe/) is either `Some(value)` or `None`. Use it
  when there may be no answer and there is nothing more to say about why.
- [`Result<T, E>`](/reference/results/) is either `Ok(value)` or `Err(error)`.
  Use it when a failure comes with an error that explains it.

Both types and their variants are always available. You do not import them.

```nomi-run
fn lookup(id: Int) -> Maybe<String> {
    case id {
        1 -> Some("Alice")
        2 -> Some("Bob")
        _ -> None
    }
}

fn name_for(id: Int) -> String {
    case lookup(id) {
        Some(name) -> name
        None -> "unknown"
    }
}

fn main() {
    dbg name_for(1)
    dbg name_for(99)
}
```
<!-- expect
dbg line 17: name_for(1) = "Alice"
dbg line 18: name_for(99) = "unknown"
-->

For a quick check of a single pattern, `if` can match it directly. The names
the pattern binds exist only in the first branch:

```nomi-run
fn lookup(id: Int) -> Maybe<String> {
    case id {
        1 -> Some("Alice")
        _ -> None
    }
}

fn label_for(id: Int) -> String {
    if Some(name) = lookup(id) {
        name
    } else {
        "unknown"
    }
}

fn main() {
    dbg label_for(1)
    dbg label_for(9)
}
```
<!-- expect
dbg line 17: label_for(1) = "Alice"
dbg line 18: label_for(9) = "unknown"
-->

## The `try` keyword

```nomi-run
fn safe_div(a: Int, b: Int) -> Result<Int, String> {
    if b == 0 {
        Err("div by zero")
    } else {
        Ok(a / b)
    }
}

fn calculate(x: Int, y: Int) -> Result<Int, String> {
    half = try safe_div(x, 2)
    ratio = try safe_div(half, y)
    Ok(ratio + 1)
}

fn main() {
    dbg calculate(100, 5)
    dbg calculate(100, 0)
}

```
<!-- expect
dbg line 16: calculate(100, 5) = Ok(11)
dbg line 17: calculate(100, 0) = Err("div by zero")
-->

`try` goes in front of a call that returns a `Maybe` or a `Result`. If the call
succeeds, `try` gives you the value inside. If it fails, the whole function
returns the failure straight away.

In `calculate(100, 0)`, the second `safe_div` fails, so `calculate` returns
`Err("div by zero")` and never reaches `Ok(ratio + 1)`.

`main` can return `Result<Unit, E>`, and then you can use `try` in it:

```nomi
fn main() -> Result<Unit, String> {
    ratio = try calculate(100, 0)
    dbg ratio
    Ok(Unit)
}
```

This `main` prints `error: div by zero` to stderr and exits with status 1.
That is what happens whenever `main` returns an `Err`. The error is printed
with `Display` if its type has it, so a `String` prints as itself. Otherwise it
is printed with `Debug`.

`main` must return either nothing or a `Result<Unit, E>`. Any other return
type is a compile error, because nothing would use the value. To show a value,
print it with `io.print` and return `Ok(Unit)`.

Inside a lambda, `try` returns from the lambda, not from the function around
it, just as `return` does.

To parse a list of inputs, map each one to a `Result` and end the pipe with
`try Result.collect()`:

```nomi-run
fn parse(text: String) -> Result<Int, String> {
    Maybe.to_result(String.to_int(text), "not a number: " + text)
}

fn total(inputs: List<String>) -> Result<Int, String> {
    numbers = inputs |> Iter.map(fn s -> parse(s)) |> try Result.collect()

    Ok(Iter.sum(numbers))
}

fn main() {
    dbg total(["1", "2", "3"])
    dbg total(["1", "x", "y"])
    #(numbers, errors) = ["1", "x", "y"] |> Iter.map(fn s -> parse(s)) |> Result.partition()

    dbg numbers
    dbg errors
}
```
<!-- expect
dbg line 12: total(["1", "2", "3"]) = Ok(6)
dbg line 13: total(["1", "x", "y"]) = Err("not a number: x")
dbg line 16: numbers = [1]
dbg line 17: errors = ["not a number: x", "not a number: y"]
-->

`Result.collect` gives the list of values when every input parsed. Otherwise it
gives the first `Err`, and stops reading at that input.

When you want every error, not just the first, `Result.partition` splits the
results into two lists: the values and the errors. `Maybe.collect` and
`Maybe.values` do the same jobs for `Maybe`.

## Binding with `else`

`try` passes an error up unchanged. Sometimes you want to handle a failure
right where it happens instead. Write the pattern you expect, then `else` and
what to do when the value does not match:

```nomi-run
enum PortError {
    Missing
    Invalid String
}

fn parse_port(text: Maybe<String>) -> Result<Int, PortError> {
    Some(raw) = text else { return Err(.Missing) }

    Some(n) = String.to_int(raw) else {
        return Err(.Invalid(raw))
    }

    Ok(n)
}

fn port(text: Maybe<String>) -> Result<Int, String> {
    Ok(n) = parse_port(text) else {
        Err(.Missing) -> 8080
        Err(.Invalid(raw)) -> return Err("bad port: ${raw}")
    }

    Ok(n)
}

fn main() {
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

When the value matches, the names in the pattern can be used for the rest of
the block. When it does not, the `else` part runs. It is either a block or a
set of `case` arms over the value that did not match.

`parse_port` uses blocks. Each one returns an error.

`port` uses `case` arms over the error that came back. A missing port gives
`8080`, which takes the place of the `Ok` value, so `n` is `8080`. An invalid
port returns an error naming the bad text.

Each path through the `else` must do one of two things. It can leave, with
`return` (or `break` or `continue` inside a loop callback). Or it can give a
value to use in place of the matched one, as `8080` does for `n`. That works
only when the pattern binds a single value. `[first, ..rest] = xs else { ... }`
binds two, so its `else` must leave.

The next chapter, [Interfaces & Dispatch](/interfaces-and-dispatch/), covers
how types share behavior: `interface`, `impl` blocks, how Nomi picks which
function to call, and the built-in `Debug` that `dbg` has been using to print
every value so far.
