---
title: "Iteration & Loops"
description: "Iter.loop for loops that carry state, and what break, continue and return do inside the functions you pass to Iter."
---

Most loops in Nomi are calls to [`Iter`](/reference/iter/#interface-iter)
functions such as [`Iter.map`](/reference/iter/#itermap),
[`Iter.filter`](/reference/iter/#iterfilter) and
[`Iter.reduce`](/reference/iter/#iterreduce), as in the last few chapters. For a
loop that carries a value from one round to the next, use
[`Iter.loop`](/reference/iter/#iterloop).

This chapter also covers `break`, `continue` and `return` inside the functions
you pass to these calls.

## Loops with `Iter.loop`

`Iter.loop` runs a loop that carries a value, its **state**, from one round to
the next. Here are two loops, a countdown and a search through Fibonacci
numbers:

```nomi-run
fn main() {
    // Countdown — break when n reaches 0.
    countdown = Iter.loop(fn n = 5 -> {
        if n == 0 { break n }
        n - 1
    })

    dbg countdown

    // Tuple state — first Fibonacci pair where the second is > 100.
    #(a, b) = Iter.loop(fn #(x, y) = #(0, 1) -> {
        if y > 100 { break #(x, y) }
        #(y, x + y)
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
parameter is the loop's state, and its default value is the starting state. In
the countdown, `n` starts at 5.

Each round, the function returns the state for the next round. `break value`
ends the loop, and `value` becomes the loop's result. A bare `break` ends it
with the current state.

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
import std/io

fn main() {
    Iter.loop(fn -> {
        Ok(Some(line)) = io.read_line() else { break }
        if line == "" { continue }
        if line == "quit" { break }
        io.print(line)
    })
}
```

You can also give the starting state as a named argument, `from:`. You need
it when the loop's body is a named function, such as `step`. A function passed
as a value leaves its parameter defaults behind, so a default can't give the
start. `Iter.loop(step, from: 5)` calls `step` with 5 in the first round. Give
the starting state one way or the other, not both.

## Keeping every step with `Iter.scan`

`Iter.reduce` gives back only the final total.
[`Iter.scan`](/reference/iter/#iterscan) gives back the total after each
element. Like `Iter.map`, it is lazy: it computes each total only when
something asks for it. Its starting value is given the same way as a loop's,
as the default of the callback's first parameter or by name as `from:`:

```nomi-run
fn main() {
    running =
        [3, 1, 4, 1, 5]
        |> Iter.scan(fn total = 0, x -> total + x)
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
callback returns `#(result, next_state)`, and the call gives back the list of
results and the final state.

## `break` and `continue` in iter callbacks

A callback is a function you pass to another function, like the lambda you
pass to `Iter.map`. Inside a callback you pass to an `Iter` function, you can
use `break` to stop early and `continue` to skip an element:

```nomi-run
fn main() {
    // break in reduce — stop accumulating once total would exceed 60.
    partial_sum =
        [10, 20, 30, 40, 50]
        |> Iter.reduce(fn acc = 0, x -> {
            if acc + x > 60 { break }
            acc + x
        })

    dbg partial_sum

    // continue in map — skip odd numbers from the output.
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

A fold such as `reduce` needs a starting value. A lambda gives it as the
default of its first parameter, as in `fn total = 0, x -> ...`. A named
function passed as a value leaves its defaults behind, so give the start by
name instead, as `from:`:

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
        |> Iter.reduce(add_until_zero, from: 0)

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
        |> Iter.reduce(fn total = 0, x -> add_until_zero(total, x))

    dbg total
}
```
<!-- expect
main.nomi:9:43: `add_until_zero` uses `break`, so it cannot be called: pass it as an iteration callback itself, as in `Iter.reduce(xs, add_until_zero, from: <start>)`
main.nomi:9:43: help: to decide in this callback instead, return a `Maybe` or an enum from `add_until_zero` and write `break` here
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

## `return` is lambda-scoped

`return` inside a lambda leaves that lambda, not the function the lambda is
written in:

```nomi-run
fn main() {
    // `return 300` exits the map callback's lambda; the map keeps going
    // and the value lands in the output list at this position.
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
dbg line 12: mapped = [10, 20, 300, 40, 50]
-->

In a callback, `return value` makes `value` the result for this element, and
the iteration moves on to the next one. Here `map` puts 300 where 30 would
have gone. A bare `return` gives `Unit`.

`?` follows the same rule. Inside a lambda, a `?` that fails returns from
the lambda.

A lambda can't return from the function it is written in. To stop a whole
iteration because of something a callback saw, `break` from the callback and
check the result afterwards.

:::note[Continue or dig deeper]
The tour moves on to [**Structs, Enums, Distinct Types**](/structs-enums-distinct/),
where you declare your own data types. The section below explains how
iteration is built.
:::

## Going deeper

### How iteration works in Nomi

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

### No Go panics

Nomi runs on a virtual machine written in Go. `break` and `continue` do not
throw an exception or a Go panic to get out of a callback. The callback
returns as usual and hands back a signal with its result. The iteration
function reads the signal and decides whether to go on, skip the element or
stop. Stopping early is an ordinary return, so it costs little.
