---
title: "Iteration & Loops"
description: "Iter.loop for loops that carry state, and what break, continue and return do inside the functions you pass to Iter, and recursion with tail calls."
---

Most loops in Nomi are calls to [`Iter`](/reference/iter/#interface-iter)
functions such as [`Iter.map`](/reference/iter/#itermap),
[`Iter.filter`](/reference/iter/#iterfilter) and
[`Iter.reduce`](/reference/iter/#iterreduce), as in the last few chapters. For a
loop that carries a value from one round to the next, use
[`Iter.loop`](/reference/iter/#iterloop).

This chapter also covers `break`, `continue` and `return` inside the functions
you pass to these calls, and recursion.

## Loops with `Iter.loop`

`Iter.loop` runs a loop that carries a value, its **state**, from one round to
the next. Here are two loops, a countdown and a search through Fibonacci
numbers:

```nomi-run
fn main() {
    // Countdown: break when n reaches 0.
    countdown = Iter.loop(5, fn n -> {
        if n == 0 { break }
        n - 1
    })

    dbg countdown

    // Tuple state: the first Fibonacci pair whose second number is over 100.
    (a, b) = Iter.loop((0, 1), fn (x, y) -> {
        if y > 100 { break (x, y) }
        (y, x + y)
    })

    dbg a
    dbg b
}

```
<!-- expect
dbg line 8: countdown = 0
dbg line 16: a = 89
dbg line 17: b = 144
-->

`Iter.loop` calls the function you give it again and again. The function's
parameter is the loop's state, and the value before the function is the
starting state. In the countdown, `n` starts at 5.

Each round, the function returns the state for the next round. A bare `break`
ends the loop with the current state as its result. `break value` ends it with
`value`, which has the same type as the state.

The state is a single value. To carry more than one thing, use a tuple or a
struct. The Fibonacci loop carries a pair, and its parameter is a pattern that
takes the pair apart.

`continue` ends the round early and starts the next one with the same state.
Take care with it in a loop that has a state. The next round starts from the
same value as the last one. Unless the last round did something outside the
loop, such as reading a line of input, the next one does exactly the same
thing, and the loop never ends.

A loop with nothing to carry takes no parameter: `Iter.loop(fn -> ...)`. It
ends with a bare `break`, and its result is `Unit`. Writing `break 0` in such a
loop is a compile error. To end with a value, give the loop a state and break
with that.

This loop prints the lines it reads until it reads `quit` or runs out of
input. `continue` skips a blank line and goes on to read the next one:

```nomi-run no-run
import std::io

fn main() {
    Iter.loop(fn -> {
        Ok(Some(line)) = io.read_line() else { break }
        if line == "" { continue }
        if line == "quit" { break }
        io.print(line)
    })
}
```

`Ok(Some(line)) = io.read_line() else { break }` binds `line` when the read
gives a line, and runs the `else` block otherwise
([Binding with `else`](/pattern-matching/#binding-with-else)).

The loop's body can also be a named function: `Iter.loop(5, step)` calls
`step` with 5 in the first round.

## Keeping every step with `Iter.scan`

`Iter.reduce` gives back only the final total.
[`Iter.scan`](/reference/iter/#iterscan) gives back the total after each
element. Like `Iter.map`, it is lazy: it computes each total only when
something asks for it. Its starting value comes before the function, as a
loop's does:

```nomi-run
fn main() {
    running =
        [3, 1, 4, 1, 5]
        |> Iter.scan(0, fn total, x -> total + x)
        |> Iter.to_list()

    dbg running
}

```
<!-- expect
dbg line 7: running = [3, 4, 8, 9, 14]
-->

The starting value itself is not in the output, so there is one total per
element. When each element should produce its own result while a value is
carried along, use [`Iter.map_reduce`](/reference/iter/#itermap_reduce). Its
function returns `(next_state, result)`, the state first as it takes it, and
the call gives back the final state and the list of results.

## `break` and `continue` in iter callbacks

A callback is a function you pass to another function, like the lambda you
pass to `Iter.map`. Inside a callback you pass to an `Iter` function, you can
use `break` to stop early and `continue` to skip an element:

```nomi-run
fn main() {
    // break in reduce: stop adding once the total would pass 60.
    partial_sum =
        [10, 20, 30, 40, 50]
        |> Iter.reduce(0, fn acc, x -> {
            if acc + x > 60 { break }
            acc + x
        })

    dbg partial_sum

    // continue in map: leave odd numbers out of the output.
    evens_doubled =
        [1, 2, 3, 4, 5]
        |> Iter.map(fn x -> {
            if x % 2 != 0 { continue }
            x * 2
        })
        |> Iter.to_list()

    dbg evens_doubled
}

```
<!-- expect
dbg line 10: partial_sum = 60
dbg line 21: evens_doubled = [4, 8]
-->

The `reduce` stops when adding 40 would take the total past 60, and gives back
the total so far. The `map` skips the odd numbers.

**`continue`** skips the current element. `map` leaves it out of the output,
and `reduce` keeps the total as it was.

**`break`** stops the iteration and gives back what has been built so far.
`reduce` gives the total so far, `map` and `filter` give the items so far,
`Iter.loop` gives the current state, and `each` gives `Unit`.

**`break value`** uses `value` as the result for this element, then stops. In
`reduce`, `value` is the final total. In `map`, it is the last item of the
output. In `filter`, `break True` keeps this element and `break False` drops
it.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- Only these functions' callbacks take `break` and `continue`: `Iter.map`,
  `scan`, `filter`, `take_while`, `drop_while`, `flat_map`, `iterate`,
  `reduce`, `map_reduce`, `each`, `find`, `any`, `all`, `Iter.loop`, and
  `Map.map_values` and `Map.map_keys`. In any other lambda they are an
  error.
- `continue` takes no value. To carry a new value forward, make it the
  callback's result.
- A bare `break` in `find`, `any` and `all` returns `None`, `False` and
  `True`.
- A `defer` in a callback runs at the end of each round, before the next
  one starts.

Spec: [Callbacks that take `break` and `continue`](/spec/iteration/#callbacks-that-take-break-and-continue),
[`break` and `continue`](/spec/statements-and-control-flow/#break-and-continue).

</div>

## Helper functions that `break` or `continue`

`return`, `break` and `continue` each leave only the function they are
written in. None of them can jump past that function into the one that called
it.

- `return value` hands `value` back to the function's caller.
- `break`, `break value` and `continue` hand a signal back to the `Iter`
  function that called the callback. That function then stops or skips the
  element.

A named function can use `break` and `continue` too. They act on the
iteration you pass the function to. Pass the function itself as the callback,
as in `Iter.map(keep_small)`.

A named function passed to a fold comes after the starting value, as with
`Iter.loop`:

```nomi-run
fn keep_small(x: Int) -> Int {
    if x > 100 { continue }
    x
}

fn add_until_zero(total: Int, x: Int) -> Int {
    if x == 0 { break }
    total + x
}

fn main() {
    small =
        [5, 500, 7]
        |> Iter.map(keep_small)
        |> Iter.to_list()

    dbg small

    total =
        [1, 2, 0, 4]
        |> Iter.reduce(0, add_until_zero)

    dbg total
}

```
<!-- expect
dbg line 17: small = [5, 7]
dbg line 23: total = 3
-->

`keep_small`'s `continue` leaves 500 out of the list. `add_until_zero`'s
`break` stops the reduction when it reaches the 0, and `reduce` gives back the
total so far, 3.

You can't call a function that uses `break` or `continue`. Its signal would
have to pass through the function that called it to reach the iteration, and
a signal leaves only one function. This holds even when the call is the whole
body of a lambda, so this is a compile error:

```nomi-run error
fn add_until_zero(total: Int, x: Int) -> Int {
    if x == 0 { break }
    total + x
}

fn main() {
    total =
        [1, 2, 0, 4]
        |> Iter.reduce(0, fn total, x -> add_until_zero(total, x))

    dbg total
}
```
<!-- expect
main.nomi:9:42: `add_until_zero` uses `break`, so it cannot be called: pass it as an iteration callback itself, as in `Iter.reduce(xs, <start>, add_until_zero)`
main.nomi:9:42: help: to decide in this callback instead, return a `Maybe` or an enum from `add_until_zero` and write `break` here
-->

When you want a helper to decide and the callback to act, have the helper
return a `Maybe` (or an enum), and write the `continue` in the callback:

```nomi-run
fn small(x: Int) -> Maybe<Int> {
    if x > 100 { None } else { Some(x) }
}

fn main() {
    doubled =
        [5, 500, 7]
        |> Iter.map(fn x -> {
            Some(n) = small(x) else { continue }
            n * 2
        })
        |> Iter.to_list()

    dbg doubled
}

```
<!-- expect
dbg line 14: doubled = [10, 14]
-->

`small` gives `None` for 500. The callback skips that element with its own
`continue` and doubles the others.

## `return` inside a lambda

`return` inside a lambda leaves that lambda, not the function the lambda is
written in:

```nomi-run
fn main() {
    mapped =
        [1, 2, 3, 4, 5]
        |> Iter.map(fn x -> {
            if x == 3 { return 300 }
            x * 10
        })
        |> Iter.to_list()

    dbg mapped
}

```
<!-- expect
dbg line 10: mapped = [10, 20, 300, 40, 50]
-->

In a callback, `return value` makes `value` the result for this element, and
the iteration moves on to the next one. Here `map` puts 300 where 30 would
have gone. A bare `return` gives `Unit`.

`?` follows the same rule. Inside a lambda, a `?` that fails returns from
the lambda.

A lambda can't return from the function it is written in. To stop a whole
iteration because of something a callback saw, `break` from the callback and
check the result afterwards.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A `return` in a nested `fn` or a `concurrent` block also belongs to that
  inner boundary.
- A bare `return` as the last statement does nothing, and is an error:
  ``this `return` does nothing``.
- A statement after a `return`, `break`, `continue` or `unreachable` can
  never run, and is an error. So is one after an `Iter.loop` with no
  `break`.

Spec: [Returning](/spec/functions-and-closures/#returning).

</div>

## Recursion and tail calls

A function may call itself. Recursion fits a problem that breaks into a
smaller copy of itself, such as counting the digits of a number:

```nomi-run
fn digits(n: Int) -> Int {
    if n < 10 { 1 } else { 1 + digits(n / 10) }
}

fn sum_to(n: Int, total: Int) -> Int {
    if n == 0 {
        total
    } else {
        sum_to(n - 1, total + n)
    }
}

fn main() {
    dbg digits(2026)
    dbg sum_to(200_000, 0)
}
```
<!-- expect
dbg line 14: digits(2026) = 4
dbg line 15: sum_to(200_000, 0) = 20000100000
-->

A call is a tail call when its result is the function's result, with
nothing left to do after it. `sum_to` ends in a tail call. Nomi runs a tail
call in place of the caller, so it uses no extra memory however many times
it repeats. Carrying the running total as a parameter is what makes the
call a tail call.

`digits` adds 1 after its call returns, so each call waits for the next.
At most 100,000 calls can wait at once. One more stops the program with
`stack overflow: more than 100000 nested calls`. `digits` never gets near
that, but a call like it that walks 200,000 steps would.

For a loop over a collection, the `Iter` functions are still the first
choice. Recursion is for steps that `Iter` doesn't describe well.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A tail call is the last expression of a body, either branch of an `if`
  or any arm of a `case` in tail position, the last stage of a pipe, the
  operand of `return`, and the right side of `and` and `or`.
- A call inside a lambda is in the lambda's tail position, not the outer
  function's. A default and the operand of `?` are never in tail position.
- A pending `defer` cancels a tail call, since the deferred call runs after
  it returns. Recursion that registers a `defer` each round keeps a frame
  per round and counts toward the limit.
- Each task has its own 100,000-call limit.
- A function that always calls itself with the arguments it was given is a
  compile error: ``every path through grow calls grow(x) with the arguments it was given, so it never returns``.

```nomi
fn leave(n: Int) {
    dbg n
}

fn down(n: Int) -> Int {
    defer leave(n) // keeps each frame until leave runs
    if n == 0 { 0 } else { down(n - 1) }
}
```

Spec: [Tail-call optimization](/spec/functions-and-closures/#tail-call-optimization),
[Recursion depth](/spec/functions-and-closures/#recursion-depth).

</div>

## How iteration works in Nomi

Iteration is built from three pieces:

- **The [`Iter` interface](/reference/iter/#interface-iter).** A type
  implements one function, `each_while`, which hands each element to a
  callback until the callback returns `False`. Then every `Iter` function
  (`map`, `filter`, `reduce`, `take_while`, `find`, `count` and the rest)
  works on that type. Two more functions are optional. A type that knows how
  many elements it has can say so with `known_count`. A type that never ends
  can say so with `is_infinite`, and then `Iter.to_list`, `Iter.count` and the
  other functions that need every element stop with an error instead of
  running forever.
- **[`Iter.loop`](/reference/iter/#iterloop)**, for any loop with its own
  state and stopping rule.
- **Pipes**, which chain [`Iter.filter`](/reference/iter/#iterfilter),
  [`Iter.take_while`](/reference/iter/#itertake_while),
  [`Iter.reduce`](/reference/iter/#iterreduce) and the rest into whatever steps
  you need.

The standard library uses the same pieces you do. Collections have no `map` of
their own that builds a new list right away. To get a list, you write
`Iter.map(xs, f) |> Iter.to_list()`.

`break` and `continue` are ordinary returns that carry a signal, so stopping
early costs little.

## See it in action

<div class="tour-examples">
<a href="/examples/pascals-triangle/"><span class="tour-example-name">Pascal's Triangle</span><span class="tour-example-what">Build each row from the one above.</span></a>
<a href="/examples/flatten-array/"><span class="tour-example-name">Flatten Array</span><span class="tour-example-what">Flatten a nested array into one list.</span></a>
<a href="/examples/killer-sudoku-helper/"><span class="tour-example-name">Killer Sudoku Helper</span><span class="tour-example-what">List every way to fill a cage.</span></a>
</div>

<a class="tour-examples-all" href="/examples/?chapter=iteration-and-loops">Every example that uses Iteration &amp; Loops</a>

Next: [Structs, Enums, Distinct Types](/structs-enums-distinct/), where you
declare your own data types.
