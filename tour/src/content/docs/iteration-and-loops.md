---
title: "Iteration & Loops"
description: "Iter.loop for stateful iteration; break / continue inside iter callbacks; return is lambda-scoped."
---

Most iteration in Nomi happens through the [`Iter`](/reference/iter/#interface-iter) owner —
[`Iter.map`](/reference/iter/#itermap), [`Iter.filter`](/reference/iter/#iterfilter),
[`Iter.reduce`](/reference/iter/#iterreduce), the pipe-friendly shapes the
last few chapters used. For stateful loops that don't fit that pattern,
[`Iter.loop(|state = init| body)`](/reference/iter/#iterloop) is the primitive.

Five control statements show up in this chapter. First, Nomi's `return`:
this isn't iteration-specific, but the rules are worth pinning down before
we hit iteration callbacks below. `return` is **lambda-scoped** — `return v`
inside any lambda exits that lambda with `v`. There is no non-local return.
Two forms:

- **`return`** — exit the current lambda with `Unit`.
- **`return value`** — exit the current lambda with `value`.

**Inside iteration callbacks** — any lambda passed to one of the
iteration functions (`Iter.loop`, `Iter.map`, `Iter.reduce`, and the
rest of the iter family) — three more statements come into
play, and `return`/`return value` take on iteration-specific semantics
(exit the callback; the iteration moves on to the next element). All five:

- **`return`** — exit the callback with `Unit` as the result for this iteration. The iteration uses that result however its operation calls for (`map` would put `Unit` in the output list at this position, for example), and moves on to the next.
- **`return value`** — same as above but with `value` instead of `Unit` (`map` puts `value` in the output, `reduce` makes `value` the new accumulator, `filter` treats `value` as the Bool decision).
- **`continue`** — *skip* the current iteration entirely; the callback contributes nothing (`map` omits it from the output, `reduce` leaves the accumulator unchanged) and the iteration moves on to the next.
- **`break`** — stop the iteration. The result depends on the iteration function (e.g. stdlib's `reduce` returns the accumulator so far, `map` and `filter` return the items collected, `each` and `Iter.loop` return `Unit`).
- **`break value`** — `return value` *and* stop: `value` plays the same per-operation role it would as a normal result (`reduce` makes it the result, `map` emits it as the final element, `filter` applies it as the Bool decision for the current element — `break True` keeps it, `break False` drops it), then iteration stops.

## The `Iter.loop` primitive

`loop` lives in [`std/iter`](/reference/iter/) with the rest of the iteration machinery; the
canonical spelling is `Iter.loop(...)`. It takes a lambda that carries state.
Each iteration returns either the next state (continue looping) or
`break value` (exit and yield the value). The state is one value; use a scalar,
tuple, struct, or whatever shape fits the loop:

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

## `break` and `continue` in iter callbacks

The same control-flow keywords work inside the lambdas you pass to
`Iter.map` / `Iter.filter` / `Iter.reduce` and friends. `break` stops the
iteration early; `continue` skips the current element:

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

## `return` is lambda-scoped

Inside a lambda, `return v` exits **that lambda** — not the enclosing
function. For iter callbacks, this is an early-exit-from-this-iteration
move: the iterator continues with the next element. This is the same
scoping rule that makes `try` on a `Result` inside a lambda bubble to the
lambda's own boundary, not the enclosing fn.

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

Nomi has no non-local returns. If you want to short-circuit the whole
iteration — exit from `fn main` based on something the callback saw — you
`break` from the callback and inspect the result outside.

:::note[Continue or dig deeper]
The tour moves on to [**Structs, Enums, Distinct Types**](/structs-enums-distinct/),
which covers the data shapes you'll build with everything we've seen so
far. Or stay here for the deep-dive section below.
:::

## Going deeper

### How iteration works in Nomi

Iteration is built on three primitives, and **everything composes from
these**:

- **The [`Iter` interface](/reference/iter/#interface-iter)** — pure protocol: implement `next` (returns
  `Maybe<(T, Self)>`) on your type (and optionally override `known_count`
  if it stores its count), and every `Iter` type function
  (`map`, `filter`, `reduce`, `take_while`, `find`, `count`, …) applies to
  values of your type without further wiring.
- **[`Iter.loop`](/reference/iter/#iterloop)** — general state-threading; any imperative-style loop
  with custom termination logic fits.
- **Composing iter ops via pipes** — chain [`Iter.filter`](/reference/iter/#iterfilter),
  [`Iter.take_while`](/reference/iter/#itertake_while), [`Iter.reduce`](/reference/iter/#iterreduce), and the rest to build whatever
  data-flow shape you need.

The stdlib uses these primitives the same way users do — the strict,
List-returning form is just `Iter.map(xs, f) |> Iter.to_list()`, the lazy
adapter plus an explicit materialize step; there are no eager adapter
functions on the collections. There's no separate "engine" the user would
write that the stdlib doesn't already provide.

### No Go panics

Control signals propagate as ordinary return values inside the runtime,
not via exceptions or Go panics. The iteration function inspects the
callback's signal and decides what to do (advance, skip, terminate). No
implicit stack unwinding, no recover, no hidden stack walking — iteration
is cheap and predictable.
