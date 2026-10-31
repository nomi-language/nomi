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

Structs, enums, interfaces and type aliases can have type parameters. A
distinct type, such as `type Score Int`, can't.

```nomi-run
struct Box<T> {
    value: T
}

fn main() {
    box = #Box{value: 42}
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

Without the `where` clause, `Iter.sort` is an error:
`` `xs` holds values of `T`, and `first_two_sorted` does not require Comparable of `T` ``.

To require more than one interface, join them with `and`:

```nomi ignore
fn describe<T>(value: T) -> String where T: Display and Debug
```

`T` must implement every interface listed.

## Bounds on declarations

Types and derives can have a `where` clause too:

```nomi ignore
struct Span<T> where T: Comparable {
    start: T
    end: T
}

derive Display for Box<T> where T: Display
```

A bound on a type declaration carries into every impl of that type, so an
impl for `Span<T>` can compare `start` and `end` without its own `where`. A
bound on a `derive` or a function applies only to that one declaration.

Next: [Interfaces & Dispatch](/interfaces-and-dispatch/), which also covers
bounds that name an interface's own type parameters.
