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
fn double(x: Int) -> Int
    x * 2
end

fn add(x: Int, y: Int) -> Int
    x + y
end

fn main()
    5 |> double() |> dbg

    5 |> add(3) |> dbg
end
```
<!-- expect
dbg line 10: 5 |> double() = 10
dbg line 12: 5 |> add(3) = 8
-->

When the call takes more arguments, you write the rest in the parentheses.
`5 |> add(3)` is `add(5, 3)`.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A stage is a call. `5 |> double` is
  ``a pipe stage is a call: write `double()` ``, since a name without
  parentheses is a function value.
- A lambda or a capture is a stage only after `then` or `tap`.
  `5 |> fn n -> n * 2` is an error that suggests `then`.
- A variant stage names its enum: `5 |> Shape.Dot()`. A bare `.Dot()` has
  no enum to look in.
- `|> if` and `|> case` may prefix the call that computes their operand:
  `input |> case parse_id()` is `input |> parse_id() |> case`, each above the
  same arms.

Spec: [Pipes](/spec/expressions/#pipes),
[Keyword stages](/spec/expressions/#keyword-stages).

</div>

A longer pipeline puts one stage on each line:

```nomi-run
fn main()
    [1, 2, 3]
    |> Iter.map(fn n -> n * n)
    |> Iter.to_list()
    |> dbg
end
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
fn main()
    // inside-out
    dbg Iter.count(Iter.filter([1, 2, 3, 4, 5], &(&1 > 2)))

    // pipeline
    [1, 2, 3, 4, 5]
    |> Iter.filter(&(&1 > 2))
    |> Iter.count()
    |> dbg
end
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
fn divide(x: Int, y: Int) -> Int
    x / y
end

fn main()
    10 |> then &divide(100, &1) |> dbg
    100 |> then &divide(y: 4, x: &1) |> dbg
end
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
fn main()
    "  ada  "
    |> String.trim()
    |> then fn name -> "Hello, ${name}!"
    |> dbg
end
```
<!-- expect
dbg line 5:
  "  ada  "
  |> String.trim()
  |> then fn name -> "Hello, ${name}!"
  = "Hello, ada!"
-->

`then` calls a lambda, or a capture, with the piped value and passes on
what it returns. Use it for a small step that no named function does. The
lambda's body ends at the next `|>`, so the stages after it are still part
of the pipeline.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A `then` capture may use `&1` more than once, positional or named. The
  piped value is computed once: `3 |> then &add(&1, b: &1)` is 6.
- A second parameter is an error: the stage passes one value.
- A lambda's parameter is out of scope after its body ends. Using it in a
  later stage is an error whose help says to write the body below the `->`,
  closed by `end`.

```nomi
fn main()
    total =
        [3, 4]
        |> then fn xs ->
            xs |> Iter.map(&(&1 * 2)) |> Iter.sum()
        end
        |> then &(&1 + 1)

    dbg total // 15
end
```

Spec: [The `then` stage](/spec/expressions/#the-then-stage).

</div>

## The `tap` stage

```nomi-run
import std::io

fn main()
    shout =
        "go north now"
        |> String.words()
        |> tap fn words -> io.print("got ${Iter.count(words)} words")
        |> Iter.map(String.uppercase)
        |> String.join(" ")

    io.print(shout)
end
```
<!-- expect
got 3 words
GO NORTH NOW
-->

`tap` calls a lambda with the piped value and then passes the same value on.
Use it for a side effect, such as logging. The lambda must return `Unit`. A
function works by name too: `|> tap io.print`.

If a `tap` lambda returns some other value, the compiler reports an error
and suggests `then`, because `tap` would throw that value away. To look at a
value while debugging, a bare `dbg` stage is shorter. Use `tap` for effects
the program keeps.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A `tap` body may end in `dbg`, as a `Unit` function body may.
- A `?` inside the lambda exits the lambda, so its result is a `Result` or
  `Maybe`, not `Unit`, and the stage is rejected.
- `tap?` is an error: `tap` passes its input on, so there is nothing for
  the `?` to propagate. Use `then?`.

Spec: [The `tap` stage](/spec/expressions/#the-tap-stage).

</div>

## Other stages

A bare `dbg` prints the value and passes it on. A
[`?`](/pattern-matching/#passing-failures-up) after a stage unwraps its
[`Maybe`](/reference/maybe/) or [`Result`](/reference/results/). An
[assertion](/testing/#assertions) can start a pipeline, and `if` and `case`
can take the piped value. A stage that starts with `not` negates the stage's
result: `name |> not String.is_empty()` is `not String.is_empty(name)`.

A `?` unwraps a `Some` or an `Ok` and passes the value inside on. On `None`
or `Err`, it returns that from the function straight away:

```nomi-run
fn parse(text: String) -> Maybe<Int>
    text
    |> String.trim()
    |> String.to_int()?
    |> then &(&1 * 2)
    |> Some()
end

fn main()
    dbg parse("  21  ")
    dbg parse("abc")
end
```
<!-- expect
dbg line 10: parse("  21  ") = Some(42)
dbg line 11: parse("abc") = None
-->

When a `then` lambda's result is what can fail, write `then?`:
`|> then? fn n -> half(n)` passes on the value inside, or returns the `None`
or `Err` from the function.

An assertion can check the result of a whole pipeline:

```nomi-run
test "pipeline result is true"
    assert "  Ada Lovelace  "
        |> String.trim()
        |> String.contains("Ada")
end
```
<!-- expect
ok pipeline result is true
test result: ok. 1 passed, 0 failed
-->

`if` and `case` can take the piped value as well. Here the value becomes the
first argument of `String.contains`, the call that computes the `if`
condition. The `case` matches its arms against the piped value:

```nomi-run
fn main()
    "Ada"
    |> if String.contains("A") then
        "initialed"
    else
        "plain"
    end
    |> dbg

    Some("Ada")
    |> case
        Some(name) -> "named ${name}"
        None -> "missing"
    end
    |> dbg
end
```
<!-- expect
dbg line 8:
  "Ada"
  |> if String.contains("A") then
      "initialed"
  else
      "plain"
  end
  = "initialed"
dbg line 15:
  Some("Ada")
  |> case
      Some(name) -> "named ${name}"
      None -> "missing"
  end
  = "named Ada"
-->

## See it in action

<div class="tour-examples">
<a href="/examples/word-count/"><span class="tour-example-name">Word Count</span><span class="tour-example-what">Count each word in a sentence.</span></a>
<a href="/examples/acronym/"><span class="tour-example-name">Acronym</span><span class="tour-example-what">Build an acronym from a phrase.</span></a>
<a href="/examples/high-scores/"><span class="tour-example-name">High Scores</span><span class="tour-example-what">The latest, the best and the top three.</span></a>
</div>

<a class="tour-examples-all" href="/examples/?chapter=pipes">Every example that uses Pipes</a>

[Scalars & Strings](/scalars-and-strings/) is next.
