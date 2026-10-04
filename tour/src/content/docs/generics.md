---
title: Generics
description: Type parameters, reusable declarations, and `where` bounds.
---

Generics let one declaration work with many concrete types while keeping those
types visible in the signature. The names in `<...>` are type placeholders that
the declaration can reuse in parameters, fields, return types, and bodies. Each
call or concrete type fills those placeholders with real types. Bounds go in a
`where` clause.

## Generic functions

A generic function introduces type parameters before its value parameters. The
call site usually pins those types from the arguments and expected return type:

```nomi-run
fn pair<T, U>(first: T, second: U): (T, U) {
    (first, second)
}

fn main(): (String, Int) {
    dbg pair("age", 30)
}

```
<!-- expect
dbg line 6: pair("age", 30) = ("age", 30)
-->

Here `T` is `String` and `U` is `Int` for this call. Another call can choose a
different pair of concrete types.

## Generic types

Structs, enums, opaque types, host types, interfaces, and type aliases can
all introduce type parameters. Those names are available anywhere the
declaration needs to talk about its parts:

```nomi-run
struct Box<T> {
    value: T
}

fn main(): Int {
    box = Box{value: 42}
    dbg box.value
}

```
<!-- expect
dbg line 7: box.value = 42
-->

`Box<T>` says every `Box` carries one value, and the type of that value is the
same `T` wherever it appears. `Box<Int>` and `Box<String>` are different
concrete types made from the same generic definition.

## Bounds with where

An unconstrained `T` can be stored, returned, and passed around, but it does not
promise any extra capabilities. A `where` clause adds those promises.

For now, read a bound like `where T: Comparable` as "`T` must be a type that can
be compared." Bounds name interfaces; the next chapter explains how interfaces
are defined and implemented. Here, the important part is the generic shape:

```nomi-run
fn first_two_sorted<T>(xs: List<T>): List<T> where T: Comparable {
    xs
    |> Iter.sort()
    |> Iter.take(2)
    |> Iter.to_list()
}

fn main(): List<String> {
    first_two_sorted([3, 1, 4, 1, 5, 9, 2, 6])
    |> dbg

    first_two_sorted(["banana", "apple", "cherry"])
    |> dbg
}

```
<!-- expect
dbg line 10: first_two_sorted([3, 1, 4, 1, 5, 9, 2, 6]) = [1, 1]
dbg line 13: first_two_sorted(["banana", "apple", "cherry"]) = ["apple", "banana"]
-->

Both [`Int`](/reference/int/) and [`String`](/reference/strings/#type-string) implement [`Comparable`](/reference/comparable/), so both calls satisfy
`where T: Comparable`.

Multiple bounds use `and`:

```nomi ignore
fn describe<T>(value: T): String where T: Display and Debug
```

The type parameter has to satisfy every listed interface.

## Relational bounds

A bound can mention another type parameter. That ties the choices together
without requiring them to be the same type:

```nomi-run
interface StepBy<S> {
    fn step_by(value: self, step: S): self
}

type Counter Int

impl StepBy for Counter {
    fn step_by(value: Counter, step: Int): Counter {
        Counter(Int(value) + step)
    }
}

fn advance<T, S>(value: T, step: S): T where T: StepBy<S> {
    T.step_by(value, step)
}

fn main(): Counter {
    dbg advance(Counter(10), 5)
}

```
<!-- expect
dbg line 18: advance(Counter(10), 5) = Counter(15)
-->

`advance<T, S>` introduces two independent type parameters. The bound
`where T: StepBy<S>` says the chosen `T` must know how to step by the chosen
`S`. In this example, `T` is `Counter` and `S` is `Int`.

That shape matters for APIs like ranges and dates: a value type might step by
an `Int`, a duration, or some other unit type instead of stepping by another
value of its own type. The [Dates & Times](/dates-and-times/) chapter uses the
same idea for adding and subtracting durations, calendar days, months, and
other time units.

The same idea lets operator interfaces infer a result that is not necessarily
the left-hand type. [`Add<Rhs, Out>`](/reference/add/) says what the right-hand operand may be
and what the expression returns; [`Subtract`](/reference/subtract/), [`Multiply`](/reference/multiply/), and [`Divide`](/reference/divide/) use the
same rhs/result shape:

```nomi-run
type Day Int

fn day_number(day: Day): Int {
    Day(n) = day
    n
}

type Days Int

fn days_number(days: Days): Int {
    Days(n) = days
    n
}

impl Add<Days, Day> for Day {
    fn add(lhs: Day, rhs: Days): Day {
        Day(day_number(lhs) + days_number(rhs))
    }
}

fn plus<L, R, Out>(lhs: L, rhs: R): Out where L: Add<R, Out> {
    lhs + rhs
}

fn main(): Day {
    dbg plus(Day(10), Days(4))
}

```
<!-- expect
dbg line 26: plus(Day(10), Days(4)) = Day(14)
-->

Here the call arguments set `L` to `Day` and `R` to `Days`. The bound then
matches `impl Add<Days, Day> for Day`, so `Out` is `Day`; the `main(): Day`
return type agrees with that inferred result.

## Bounds on declarations

`where` can appear on the generic declaration that needs the constraint:

```nomi ignore
struct Range<T> where T: Comparable {
    start: T
    end: T

    fn contains?(r: Range<T>, value: T): Bool {
        r.start <= value and value <= r.end
    }
}

derive Display for Box<T> where T: Display
```

Constraints on a type declaration apply to that type everywhere. Constraints on
an `impl`, `derive`, or function are narrower: they apply only to that
implementation, derive, or function.

Generic interface functions can introduce their own type parameters too:

```nomi ignore
interface Ranked<T> {
    fn item(value: self): T
    fn prefer<K>(value: self, lhs: K, rhs: K): Bool where K: Comparable
}
```

`T` comes from `Ranked<T>`. `K` comes from `prefer<K>`. A type parameter must be
introduced before a `where` clause can constrain it.

Next: [Interfaces & Dispatch](/interfaces-and-dispatch/).
