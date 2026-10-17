---
title: Generics
description: Write one function or type that works with many types, and require what those types must support with `where`.
---

A generic function or type works with many types. The names in `<...>` stand
for types. They are called type parameters. The declaration can use them for
its parameters, fields and return type. Each use fills them in with real
types.

## Generic functions

A generic function lists its type parameters in `<...>`, before its ordinary
parameters. You don't usually write the types at the call. Nomi works them out
from the arguments:

```nomi-run
fn pair<T, U>(first: T, second: U) -> #(T, U) {
    #(first, second)
}

fn main() {
    dbg pair("age", 30)
}

```
<!-- expect
dbg line 6: pair("age", 30) = #("age", 30)
-->

In this call, `T` is `String` and `U` is `Int`. Another call can use
different types.

## Generic types

Structs, enums, opaque types, host types, interfaces and type aliases can all
have type parameters:

```nomi-run
struct Box<T> {
    value: T
}

fn main() {
    box = Box{value: 42}
    dbg box.value
}

```
<!-- expect
dbg line 7: box.value = 42
-->

Every `Box` holds one value, of type `T`. `Box<Int>` and `Box<String>` are
different types, made from the same definition.

## Bounds with where

A function can store, return and pass along a value of type `T`. It can't do
anything else with it, such as compare two of them, because `T` could be any
type. A `where` clause says what `T` must support. This is called a bound.

Read `where T: Comparable` as "`T` must be a type whose values can be
compared." `Comparable` is an interface. The
[next chapter](/interfaces-and-dispatch/) explains interfaces.

```nomi-run
fn first_two_sorted<T>(xs: List<T>) -> List<T> where T: Comparable {
    xs
    |> Iter.sort()
    |> Iter.take(2)
    |> Iter.to_list()
}

fn main() {
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

[`Int`](/reference/int/) and [`String`](/reference/strings/#type-string) both
implement [`Comparable`](/reference/comparable/), so both calls are allowed.

To require more than one interface, join them with `and`:

```nomi ignore
fn describe<T>(value: T) -> String where T: Display and Debug
```

`T` must implement every interface listed.

## Relational bounds

A bound can mention another type parameter. This links two type parameters
without making them the same type:

```nomi-run
interface StepBy<S> {
    fn step_by(value: self, step: S) -> self
}

type Counter Int

impl StepBy for Counter {
    fn step_by(value: Counter, step: Int) -> Counter {
        Counter(n) = value
        Counter(n + step)
    }
}

fn advance<T, S>(value: T, step: S) -> T where T: StepBy<S> {
    T.step_by(value, step)
}

fn main() {
    dbg advance(Counter(10), 5)
}

```
<!-- expect
dbg line 19: advance(Counter(10), 5) = Counter(15)
-->

`advance<T, S>` has two type parameters. The bound `where T: StepBy<S>` says
`T` must be able to step by an `S`. In this call, `T` is `Counter` and `S` is
`Int`.

This matters for ranges and dates. A value often steps by something of a
different type: a date steps by a number of days, not by another date. The
[Dates & Times](/dates-and-times/) chapter uses this for adding durations,
days and months.

Operators work the same way. [`Add<Rhs, Out>`](/reference/add/) says what the
right-hand side of `+` may be, and what type the sum is. So do
[`Subtract`](/reference/subtract/), [`Multiply`](/reference/multiply/) and
[`Divide`](/reference/divide/) for their operators:

```nomi-run
type Day Int

fn day_number(day: Day) -> Int {
    Day(n) = day
    n
}

type Days Int

fn days_number(days: Days) -> Int {
    Days(n) = days
    n
}

impl Add<Days, Day> for Day {
    fn add(lhs: Day, rhs: Days) -> Day {
        Day(day_number(lhs) + days_number(rhs))
    }
}

fn plus<L, R, Out>(lhs: L, rhs: R) -> Out where L: Add<R, Out> {
    lhs + rhs
}

fn main() {
    dbg plus(Day(10), Days(4))
}

```
<!-- expect
dbg line 26: plus(Day(10), Days(4)) = Day(14)
-->

From the arguments, `L` is `Day` and `R` is `Days`. The only matching impl is
`impl Add<Days, Day> for Day`, so Nomi works out that `Out` is `Day`. The call
doesn't have to say so.

## Bounds on declarations

Types, impls and derives can have a `where` clause too:

```nomi ignore
struct Range<T> where T: Comparable {
    start: T
    end: T
}

impl Range<T> {
    fn contains?(r: Range<T>, value: T) -> Bool {
        r.start <= value and value <= r.end
    }
}

derive Display for Box<T> where T: Display
```

A bound on a type declaration applies everywhere the type is used. A bound on
an `impl`, a `derive` or a function applies only to that one declaration.

A function in an interface can have its own type parameters:

```nomi ignore
interface Ranked<T> {
    fn item(value: self) -> T
    fn prefer<K>(value: self, lhs: K, rhs: K) -> Bool where K: Comparable
}
```

`T` comes from `Ranked<T>`. `K` comes from `prefer<K>`. A `where` clause can
only name type parameters declared in one of those two places.

Next: [Interfaces & Dispatch](/interfaces-and-dispatch/).
