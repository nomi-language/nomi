---
title: Pipes
description: The `|>` operator passes a value through a series of calls, so code reads from top to bottom.
---

`x |> f()` means `f(x)`. The value on the left becomes the first argument of
the call on the right. Pipes let you write a series of calls from top to
bottom, instead of nesting them inside each other. Most Nomi code is written
this way.

## The pipe operator

```nomi-run
fn double(x: Int) -> Int {
    x * 2
}

fn add(x: Int, y: Int) -> Int {
    x + y
}

fn main() {
    5 |> double() |> dbg

    5 |> add(3) |> dbg
}

```
<!-- expect
dbg line 10: 5 |> double() = 10
dbg line 12: 5 |> add(3) = 8
-->

When the call takes more arguments, you write the rest in the parentheses.
`5 |> add(3)` is `add(5, 3)`.

A longer pipeline puts one stage on each line:

```nomi-run
fn main() {
    [1, 2, 3]
    |> Iter.map(fn n -> n * n)
    |> Iter.to_list()
    |> dbg
}

```
<!-- expect
dbg line 5:
  [1, 2, 3]
  |> Iter.map(fn n -> n * n)
  |> Iter.to_list()
  = [1, 4, 9]
-->

## Pipes start from data

A pipeline reads best when it starts with the data and ends where the result
is used. Here is the same calculation written both ways:

```nomi-run
fn main() {
    // inside-out
    dbg Iter.count(Iter.filter([1, 2, 3, 4, 5], &(&1 > 2)))

    // pipeline — data first, transformations next, output last
    [1, 2, 3, 4, 5]
    |> Iter.filter(&(&1 > 2))
    |> Iter.count()
    |> dbg
}

```
<!-- expect
dbg line 3: Iter.count(Iter.filter([1, 2, 3, 4, 5], &(&1 > 2))) = 3
dbg line 9:
  [1, 2, 3, 4, 5]
  |> Iter.filter(&(&1 > 2))
  |> Iter.count()
  = 3
-->

Both give `3`. The nested version runs from the inside out, so you read it
backwards. The pipeline runs in the order you read it.

## Piping into another argument

A stage takes the piped value as its first argument. To pass it as another
argument, write a `then` stage with a
[capture](/functions-and-lambdas/#captures), whose `&1` is the piped value:

```nomi-run
fn divide(x: Int, y: Int) -> Int {
    x / y
}

fn main() {
    10 |> then &divide(100, &1) |> dbg
    100 |> then &divide(y: 4, x: &1) |> dbg
}

```
<!-- expect
dbg line 6: 10 |> then &divide(100, &1) = 10
dbg line 7: 100 |> then &divide(y: 4, x: &1) = 25
-->

`10 |> then &divide(100, &1)` is `divide(100, 10)`. A named argument can
be `&1` too: `100 |> then &divide(y: 4, x: &1)` is `divide(100, 4)`. The
stage passes one value, so the capture takes one parameter. A bare `&1` in
a call stage, as in `10 |> divide(100, &1)`, is an error that suggests the
`then` stage.

## The `then` stage

```nomi-run
fn find_name(id: Int) -> Maybe<String> {
    case id {
        1 -> Some("Ada")
        _ -> None
    }
}

fn main() {
    1
    |> find_name()
    |> then fn name -> name == Some("Ada")
    |> dbg
}
```
<!-- expect
dbg line 12:
  1
  |> find_name()
  |> then fn name -> name == Some("Ada")
  = True
-->

`then` calls a lambda, or a capture, with the piped value and passes on
what it returns. Use it for a small step that no named function does. The
lambda's body ends at the next `|>`, so the stages after it are still part
of the pipeline.

## The `tap` stage

```nomi-run
import std/io

fn main() {
    shout =
        "go north now"
        |> String.words()
        |> tap fn words -> io.print("got ${Iter.count(words)} words")
        |> Iter.map(String.to_upper)
        |> String.join(" ")

    io.print(shout)
}
```
<!-- expect
got 3 words
GO NORTH NOW
-->

`tap` calls a lambda with the piped value and then passes the same value on.
Use it for a side effect, such as logging. The lambda must return `Unit`.

If a `tap` lambda returns some other value, the compiler reports an error
and suggests `then`, because `tap` would throw that value away. To look at a
value while debugging, a bare `dbg` stage is shorter. Use `tap` for effects
the program keeps.

## Keyword stages

Some keywords can be pipeline stages too:

- A bare `dbg` prints the value and passes it on.
- [`try`](/pattern-matching/#the-try-keyword) unwraps a
  [`Maybe`](/reference/maybe/) or [`Result`](/reference/results/).
- [Assertions](/testing/#assertions) often start a pipeline in a test.
- `if` and `case` can take the piped value.

`try` unwraps a `Some` or an `Ok` and passes the value inside on. On `None`
or `Err`, it returns that from the function straight away:

```nomi-run
fn parse() -> Maybe<Int> {
    "  42  "
    |> String.trim()
    |> try String.to_int()
    |> Some()
    |> dbg
}

fn main() {
    _ = parse()
}

```
<!-- expect
dbg line 6:
  "  42  "
  |> String.trim()
  |> try String.to_int()
  |> Some()
  = Some(42)
-->

An assertion can check the result of a whole pipeline:

```nomi-run
test "pipeline result is true" {
    assert "  Ada Lovelace  "
        |> String.trim()
        |> String.contains?("Ada")
}

```
<!-- expect
-->

`if` and `case` can take the piped value as well. Here the value becomes the
first argument of `String.contains?`, the call that computes the `if`
condition. The `case` matches its arms against the piped value:

```nomi-run
fn main() {
    "Ada"
    |> if String.contains?("A") {
        "initialed"
    } else {
        "plain"
    }
    |> dbg

    Some("Ada")
    |> case {
        .Some(name) -> "named ${name}"
        .None -> "missing"
    }
    |> dbg
}
```
<!-- expect
dbg line 8:
  "Ada"
  |> if String.contains?("A") {
      "initialed"
  } else {
      "plain"
  }
  = "initialed"
dbg line 15:
  Some("Ada")
  |> case {
      .Some(name) -> "named ${name}"
      .None -> "missing"
  }
  = "named Ada"
-->

[Scalars & Strings](/scalars-and-strings/) is next.
