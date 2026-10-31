---
title: "Pattern Matching, Maybe & Result"
description: "`case` takes values apart by shape. Maybe and Result hold an answer or its absence, and `?` passes a failure up."
---

A `case` expression takes a value and runs the first arm whose pattern matches
it. You saw simple `case` expressions in
[Bindings & Expressions](/bindings-and-expressions/). This chapter covers more
kinds of pattern, then [`Maybe<T>`](/reference/maybe/),
[`Result<T, E>`](/reference/results/), and the `?` that passes a failure up.

A `case` must cover every possible value. If you leave out a variant of an
enum, the program does not compile. You find out when you build, not when the
program runs.

## Binding patterns and guards

```nomi-run
fn label(text: String) -> String {
    case String.length(text) {
        0 -> "empty"
        n when n > 10 -> "long, ${n} characters"
        n -> "${n} characters"
    }
}

fn main() {
    dbg label("")
    dbg label("hello")
    dbg label("hello there, world")
}
```
<!-- expect
dbg line 10: label("") = "empty"
dbg line 11: label("hello") = "5 characters"
dbg line 12: label("hello there, world") = "long, 18 characters"
-->

A plain name in a pattern, like `n`, matches any value and gives it that name,
here the length. `when` adds a condition, called a guard. The arm runs only if
the condition is true. `_` matches anything and names nothing.

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
        #.Rectangle{width, height} -> width * height
    }
}

fn main() {
    dbg area(.Circle(3.0))
    dbg area(#.Rectangle{width: 4.0, height: 5.0})
}
```
<!-- expect
dbg line 14: area(.Circle(3.0)) = 28.27431
dbg line 15: area(#.Rectangle{width: 4.0, height: 5.0}) = 20.0
-->

`.Circle(r)` matches a circle and names its radius `r`.
`#.Rectangle{width, height}` matches a rectangle and names both fields.

Tuples and structs work the same way: `#(a, b) -> ...` and
`#Point{x, y} -> ...`. A pattern is written the way the value is built.

### Lists, vectors and sets

A list or vector pattern names items from the front, `..` stands for the
rest, and items after the `..` are named from the end. A vector pattern is
`#[...]`. A set pattern, `%[...]`, checks which values the set holds:

```nomi-run
fn first_two(words: List<String>) -> String {
    case words {
        [] -> "no words"
        [only] -> only
        [a, b, ..] -> "${a} ${b}"
    }
}

fn last(v: Vector<Int>) -> Maybe<Int> {
    case v {
        #[] -> None
        #[.., z] -> Some(z)
    }
}

fn access(roles: Set<String>) -> String {
    case roles {
        %["admin", ..] -> "everything"
        %["editor", "viewer", ..] -> "write and read"
        _ -> "read only"
    }
}

fn main() {
    dbg first_two(["hello", "big", "world"])
    dbg last(#[1, 2, 3])
    dbg access(%["viewer", "editor"])
    dbg access(%["viewer"])
}
```
<!-- expect
dbg line 25: first_two(["hello", "big", "world"]) = "hello big"
dbg line 26: last(#[1, 2, 3]) = Some(3)
dbg line 27: access(%["viewer", "editor"]) = "write and read"
dbg line 28: access(%["viewer"]) = "read only"
-->

`[a, b, ..]` matches a list of two or more items and ignores the rest;
`..rest` would name it. `#[.., z]` matches a vector of at least one item and
names the last, and `["graph {", ..body, "}"]` names the lines between two
others. Reaching a list's last items walks the list once. The compiler checks
list and vector arms by length, so `first_two` and `last` need no `_` arm.

A set pattern matches when the set holds every value it lists, in any order.
`..others` names the values it did not list, and without a `..` the set must
hold exactly the values written. The values are expressions, looked up rather
than bound, so a set pattern can never cover every set, and `access` needs its
`_` arm.

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
        #.Rectangle{width, height} -> width * height
    }
}

fn describe(s: Shape) -> String {
    case s {
        #.Rectangle{width, height} as sq when width == height -> "a square of area ${area(sq)}"
        other -> "a shape of area ${area(other)}"
    }
}

fn main() {
    dbg describe(#.Rectangle{width: 2.0, height: 2.0})
    dbg describe(.Circle(1.0))
}
```
<!-- expect
dbg line 21: describe(#.Rectangle{width: 2.0, height: 2.0}) = "a square of area 4.0"
dbg line 22: describe(.Circle(1.0)) = "a shape of area 3.0"
-->

The first arm reads `width` and `height`, and passes the whole rectangle to
`area` as `sq`. Without `as`, it would have to build the rectangle again.

`as` names the whole pattern to its left. `Ok(x) as r` names the whole `Result`.
`Ok(#Point{x, y} as p)` names only the `Point` inside. The name has the type of
the value it names, so `sq` is a `Shape`.

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
        #.Rectangle{width, height} -> "${width} by ${height}"
    }
}

fn main() {
    dbg size(.Square(12.0))
    dbg size(.Circle(2.0))
}
```
<!-- expect
dbg line 16: size(.Square(12.0)) = "big, 12.0 across"
dbg line 17: size(.Circle(2.0)) = "2.0 across"
-->

Every alternative must bind the same names, with the same types. Here both
bind `r` as a `Float`, and `r` holds the value from whichever one matched.
The `when` guard applies to the whole arm: if it is false, matching goes on to
the next arm.

`or` works inside other patterns too, as in `Some(.Circle(_) or .Square(_))`.
A trailing `as` names the whole value
whichever alternative matched. Together, the alternatives count toward
covering every value, so `.Red or .Green` and `.Blue` cover a three-color enum
without `_`.

### Testing a match with `is`

`value is Pattern` asks whether a value matches a pattern. It is a `Bool`
and binds nothing, so it fits wherever a condition does, in place of a
`case` with a `True` arm and a `_ -> False` arm:

```nomi-run
enum Block {
    Item String
    Text String
}

fn closes(c: Codepoint) -> Bool {
    c is (')' or ']' or '}')
}

fn main() {
    blocks = [Block.Item("milk"), Block.Text("note"), Block.Item("eggs")]
    dbg blocks |> Iter.filter(&(&1 is .Item(_))) |> Iter.count()
    dbg closes(']')
    dbg not Some(3) is None
}
```
<!-- expect
dbg line 12: blocks |> Iter.filter(&(&1 is .Item(_))) |> Iter.count() = 2
dbg line 13: closes(']') = True
dbg line 14: not Some(3) is None = True
-->

An `or` pattern after `is` goes in parentheses, since a bare `or` there joins
two `Bool`s. A name in the pattern is an error: to use the payload, bind it
with a `case` or with `if Some(n) = x`, as
[`Maybe<T>` and `Result<T, E>`](#maybet-and-resultt-e) shows.

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

## Passing failures up

```nomi-run
fn safe_div(a: Int, b: Int) -> Result<Int, String> {
    if b == 0 {
        Err("div by zero")
    } else {
        Ok(a / b)
    }
}

fn calculate(x: Int, y: Int) -> Result<Int, String> {
    half = safe_div(x, 2)?
    ratio = safe_div(half, y)?
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

A `?` after a `Maybe` or `Result` value gives you the value inside, or returns
the `None` or `Err` from the whole function at once. The function must return
the same kind: a `Maybe`'s `?` needs a function that returns a `Maybe`. To turn
one into the other, use `Maybe.to_result`, as below.

In `calculate(100, 0)`, the second `safe_div` fails, so `calculate` returns
`Err("div by zero")` and never reaches `Ok(ratio + 1)`.

`main` can return `Result<Unit, E>`, and then you can use `?` in it:

```nomi-run error
fn safe_div(a: Int, b: Int) -> Result<Int, String> {
    if b == 0 {
        Err("div by zero")
    } else {
        Ok(a / b)
    }
}

fn calculate(x: Int, y: Int) -> Result<Int, String> {
    half = safe_div(x, 2)?
    ratio = safe_div(half, y)?
    Ok(ratio + 1)
}

fn main() -> Result<Unit, String> {
    ratio = calculate(100, 0)?
    dbg ratio
    Ok(Unit)
}
```
<!-- expect
error: div by zero
-->

This `main` prints `error: div by zero` to stderr and exits with status 1.
That is what happens whenever `main` returns an `Err`. The error is printed
with `Display` if its type has it, so a `String` prints as itself. Otherwise it
is printed with `Debug`.

`main` must return either nothing or a `Result<Unit, E>`. Any other return
type is a compile error, because nothing would use the value. To show a value,
print it with `io.print` and return `Ok(Unit)`.

To exit with a status other than 1, make `E` an `Exit` from `std/process` and
return it in an `Err`, as [Input & Output](/input-and-output/#exit-codes)
shows.

Inside a lambda, a `?` returns from the lambda, not from the function around
it, just as `return` does.

To parse a list of inputs, map each one to a `Result` and end the pipe with
`Result.collect()?`:

```nomi-run
fn parse(text: String) -> Result<Int, String> {
    Maybe.to_result(String.to_int(text), "not a number: ${text}")
}

fn total(inputs: List<String>) -> Result<Int, String> {
    numbers = inputs |> Iter.map(parse) |> Result.collect()?

    Ok(Iter.sum(numbers))
}

fn main() {
    dbg total(["1", "2", "3"])
    dbg total(["1", "x", "y"])

    #(numbers, errors) =
        ["1", "x", "y"]
        |> Iter.map(parse)
        |> Result.partition()

    dbg numbers
    dbg errors
}
```
<!-- expect
dbg line 12: total(["1", "2", "3"]) = Ok(6)
dbg line 13: total(["1", "x", "y"]) = Err("not a number: x")
dbg line 20: numbers = [1]
dbg line 21: errors = ["not a number: x", "not a number: y"]
-->

`Result.collect` gives the list of values when every input parsed. Otherwise it
gives the first `Err`, and stops reading at that input.

When you want every error, not just the first, `Result.partition` splits the
results into two lists: the values and the errors. For `Maybe`,
`Maybe.collect` gives `Some` of every value or `None`, and `Maybe.values` keeps
the `Some` values and drops the rest.

A `Maybe` is also an `Iter` of zero or one elements, so it goes where an
`Iter` does: `Iter.to_list(Some(3))` is `[3]`, `Iter.to_list(None)` is `[]`,
and `Iter.flat_map` with a function that returns a `Maybe` keeps only the
`Some` values.

## Calls that cannot fail

A `!` written right after an expression unwraps its `Maybe` or `Result`:

```nomi-run error
fn half(n: Int) -> Result<Int, String> {
    if n % 2 == 0 {
        Ok(n / 2)
    } else {
        Err("${n} is odd")
    }
}

fn main() {
    ages = %{"Ada" => 36, "Grace" => 45}
    dbg Map.get(ages, "Ada")!
    dbg half(10)!
    dbg "8" |> String.to_int()!
    dbg Map.get(ages, "Alan")!
}
```
<!-- expect
dbg line 11: Map.get(ages, "Ada")! = 36
dbg line 12: half(10)! = 5
dbg line 13: "8" |> String.to_int()! = 8
line 14: `Map.get(ages, "Alan")` found nothing
-->

`Map.get(ages, "Ada")!` is `36`, not `Some(36)`. When the value is `None` or
`Err`, the program stops at that line and quotes the expression. A `None`
says it found nothing, as above, and an `Err` shows the error: `half(3)!`
stops with ``line 12: `half(3)` failed: 3 is odd``.

Use `!` when a failure would be a bug in your own code: the arguments are
constants, the key came from the map itself, or the input was checked
already. When the failure can really happen, pass it up with `?` or handle it
with `case` or `else`. `!` works on any expression that is a `Maybe` or a `Result`: a call,
a pipe stage (`"8" |> String.to_int()!`), a field (`user.nickname!`) or a
name.

## Binding with `else`

`?` passes an error up unchanged. Sometimes you want to handle a failure
right where it happens instead. Write the pattern you expect, then `else` and
what to do when the value does not match:

```nomi-run
enum PortError {
    Missing
    Invalid String
}

fn parse_port(text: Maybe<String>) -> Result<Int, PortError> {
    Some(raw) = text else { return Err(.Missing) }

    Some(n) = String.to_int(raw) else { return Err(.Invalid(raw)) }

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
dbg line 24: port(Some("3000")) = Ok(3000)
dbg line 25: port(Some("eighty")) = Err("bad port: eighty")
dbg line 26: port(None) = Ok(8080)
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

The next chapter, [Testing](/testing/), shows how to write and run tests.
[Interfaces & Dispatch](/interfaces-and-dispatch/) later covers how types
share behavior, and the `Debug` that `dbg` has been using to print every value
so far.
