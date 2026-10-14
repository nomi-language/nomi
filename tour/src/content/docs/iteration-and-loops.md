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

## The `Iter.loop` primitive

```nomi-run
fn main() {
    // Countdown — break when n reaches 0.
    countdown = Iter.loop(|n = 5| {
        if n == 0 { break n }
        n - 1
    })

    dbg countdown

    // Tuple state — first Fibonacci pair where the second is > 100.
    (a, b) = Iter.loop(|state = (0, 1)| {
        (x, y) = state
        if y > 100 { break (x, y) }
        (y, x + y)
    })

    dbg a
    dbg b
}

```
<!-- expect
dbg line 8: countdown = 0
dbg line 17: a = 89
dbg line 18: b = 144
-->

`Iter.loop` calls the function you give it again and again. The function's
parameter is the loop's state, and its default value is the starting state. In
the countdown, `n` starts at 5.

Each round, the function returns the state for the next round. `break value`
ends the loop, and `value` becomes the loop's result. A bare `break` ends it
with the current state.

The state is a single value. To carry more than one thing, use a tuple or a
struct. The Fibonacci loop carries a pair.

A loop with nothing to carry takes no parameter: `Iter.loop(|| ...)`. It ends
with a bare `break`, and its result is `Unit`. `break 0` in such a loop is a
compile error. To end with a value, give the loop a state and break with that.

```nomi
Iter.loop(|| {
    Ok(line) = io.read_line() else { break }
    if line == "quit" { break }
    io.print(line)
})
```

## `break` and `continue` in iter callbacks

A callback is a function you pass to another function, like the lambda you
pass to `Iter.map`. Inside a callback you pass to an `Iter` function, you can
use `break` to stop early and `continue` to skip an element:

```nomi-run
fn main() {
    // break in reduce — stop accumulating once total would exceed 60.
    partial_sum =
        [10, 20, 30, 40, 50]
        |> Iter.reduce(|acc = 0, x| {
            if acc + x > 60 { break }
            acc + x
        })

    dbg partial_sum

    // continue in map — skip odd numbers from the output.
    evens_doubled =
        [1, 2, 3, 4, 5]
        |> Iter.map(|x| {
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

## `return` is lambda-scoped

```nomi-run
fn main() {
    // `return 300` exits the map callback's lambda; the map keeps going
    // and the value lands in the output list at this position.
    mapped =
        [1, 2, 3, 4, 5]
        |> Iter.map(|x| {
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

`return` inside a lambda leaves that lambda, not the function around it. In a
callback, `return value` makes `value` the result for this element, and the
iteration moves on to the next one. Here `map` puts 300 where 30 would have
gone. A bare `return` gives `Unit`.

`try` follows the same rule. Inside a lambda, a `try` that fails returns from
the lambda.

There is no way to return from the enclosing function from inside a lambda. To
stop a whole iteration because of something a callback saw, `break` from the
callback and check the result afterwards.

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
  callback until the callback returns `False`. A type that knows its size can
  also override `known_count`. Then every `Iter` function (`map`, `filter`,
  `reduce`, `take_while`, `find`, `count` and the rest) works on that type.
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
throw an exception or a Go panic to get out of a callback. The callback hands
back a signal, and the iteration function decides whether to go on, skip the
element or stop. Nothing unwinds the stack, so stopping early is cheap and
predictable.
