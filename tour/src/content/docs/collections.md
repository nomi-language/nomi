---
title: Collections
description: Lists, vectors, maps, sets and ranges. Collections never change; each operation gives you a new one.
---

Nomi has four collection types: [`List<T>`](/reference/lists/#type-list),
[`Vector<T>`](/reference/vectors/#type-vector), [`Map<K, V>`](/reference/maps/#type-map)
and [`Set<T>`](/reference/sets/#struct-set). It also has
[`Range<T>`](/reference/ranges/#struct-range), the values between a start and an
end.

A collection never changes. An operation that adds, removes or replaces
something gives you a new collection, and the original stays as it was.
Collections that work this way are called persistent.

A collection literal starts with a symbol that says its shape. `#` marks the
shapes you read by position: a vector `#[1, 2]`, and also a tuple `#(1, "a")`
and a struct `#{x: 1}` ([Structs, Enums and Distinct
Types](/structs-enums-distinct/)). `%` marks the shapes that find a value by
hashing it: a map `%{"a" => 1}` and a set `%[1, 2]`. A list, the commonest
collection, has no symbol: `[1, 2]`.

A type you build on a collection writes its name right after the symbol:
`#Path[1, 2]` for `type Path Vector<Int>`, `%Tags["a"]` for
`type Tags Set<String>` and `%Scores{"ada" => 3}` for
`type Scores Map<String, Int>`. A list type writes its name before the
brackets, `Ids[1, 2]`. [Distinct types](/structs-enums-distinct/) covers these.

A function that works on a collection takes it as its first argument. That is
what lets you chain calls with pipes.

## Iter

Every collection is an [`Iter`](/reference/iter/#interface-iter): something you
can walk through one value at a time. So is a range of whole numbers or
codepoints, and so is a `String`, which gives one character at a time.

The general operations live on `Iter` and work on all of them: `Iter.map`,
`Iter.filter`, `Iter.take`, `Iter.reduce`, `Iter.sum`, `Iter.count`,
`Iter.min`, `Iter.max` and more. Each collection type keeps only the operations specific to
it, such as `Map.get` or `List.head`.

```nomi-run
import std/io

fn main() {
    result =
        Range.naturals()
        |> Iter.map(fn n -> {
            io.print("squaring ${n}")
            n * n
        })
        |> Iter.filter(&(&1 > 10))
        |> Iter.take(2)
        |> Iter.to_list()

    dbg result
}

```
<!-- expect
squaring 0
squaring 1
squaring 2
squaring 3
squaring 4
squaring 5
dbg line 14: result = [16, 25]
-->

`Range.naturals()` counts 0, 1, 2 and on without end. The program still
finishes, and it squares only 0 through 5.

That is because `Iter.map`, `Iter.filter` and `Iter.take` are lazy. Each one
describes a step but runs nothing, and no list is built between steps. The work
starts when a final call asks for values. Here that is `Iter.to_list`. Others
are `Iter.to_vector`, `Iter.to_set`, `Iter.to_map`, `String.join`,
`Iter.reduce`, `Iter.sum`, `Iter.count` and `Iter.each`.

Each value goes through every step before the next value starts. The work stops
as soon as the final call has what it needs. `Iter.take(2)` has its two values
once 25 arrives, so nothing after 5 is squared.

Without the `Iter.take(2)`, the program would never finish, so Nomi doesn't
compile it. The compiler can see that `Range.naturals()` never ends, that
`Iter.map` and `Iter.filter` don't change that, and that `Iter.to_list`
needs every value:

```console
error: `Iter.to_list` of an infinite iterator (`Range.naturals`) never finishes; limit it with `Iter.take` or `Iter.take_while`
```

Sometimes the compiler can't see where the values come from, for example
when an endless iterator is passed in as a parameter. Then the program stops
with a similar message at the `Iter.to_list` line, before it squares
anything, instead of running until it runs out of memory. An iterator says
that it never ends through
[`Iter.is_infinite`](/reference/iter/#iteris_infinite).

## Lists

```nomi-run
fn main() {
    xs = [1, 2, 3, 4, 5]

    xs
    |> Iter.filter(&(&1 > 2))
    |> Iter.map(fn n -> n * n)
    |> Iter.sum()
    |> dbg
}

```
<!-- expect
dbg line 8:
  xs
  |> Iter.filter(&(&1 > 2))
  |> Iter.map(fn n -> n * n)
  |> Iter.sum()
  = 50
-->

You write a list as `[a, b, c]`. The [`List`](/reference/lists/#type-list) type
has the functions specific to lists, such as `concat`, `head` and `tail`.

To transform a list, use the `Iter` functions, as above. When you want a list
back at the end, finish with [`Iter.to_list()`](/reference/iter/#iterto_list).

`[x, ..xs]` is a new list with `x` in front of `xs`. [Spreads](#spreads) covers
this.

## Vectors

```nomi-run
fn main() {
    names =
        #["Ada", "Grace"]
        |> Vector.push("Katherine")

    dbg names
    dbg Vector.length(names)
    dbg Vector.get(names, 1)
    dbg Vector.get(names, 5)
    dbg Vector.get(names, 1)!
    dbg names + #["Dorothy"]
}

```
<!-- expect
dbg line 6: names = #["Ada", "Grace", "Katherine"]
dbg line 7: Vector.length(names) = 3
dbg line 8: Vector.get(names, 1) = Some("Grace")
dbg line 9: Vector.get(names, 5) = None
dbg line 10: Vector.get(names, 1)! = "Grace"
dbg line 11: names + #["Dorothy"] = #["Ada", "Grace", "Katherine", "Dorothy"]
-->

You write a vector as `#[a, b, c]`.
[`Iter.to_vector`](/reference/iter/#iterto_vector) builds one from any `Iter`.

A vector is a sequence you can read by position. Use a vector when you look
items up by index or add them at the end. Use a list when you add items at the
front.

Indexes start at 0.
[`Vector.get`](/reference/vectors/#vectorget) answers a
[`Maybe`](/reference/maybe/): `Some(item)` when the index is inside the
vector, `None` when it is not. Use it when the index comes from data.
`Vector.get(names, 1)!`, with a
[postfix `!`](/pattern-matching/#calls-that-cannot-fail), returns the item
itself and stops the program when the index is outside, so use it when your
code guarantees the index.
[`Vector.set`](/reference/vectors/#vectorset) replaces the item at an index and
stops the program the same way.

`xs + ys` joins two lists, or two vectors.
[`List.concat`](/reference/lists/#listconcat) and
[`Vector.concat`](/reference/vectors/#vectorconcat) do the same thing by name.

A `case` can take a vector apart from either end, as
[Pattern Matching](/pattern-matching/#lists-vectors-and-sets) shows.

## Maps

```nomi-run
fn main() {
    config = %{"host" => "localhost", "port" => "8080"}

    dbg Map.size(config)
    dbg Map.get(config, "host")
    dbg Map.get(config, "user")
}

```
<!-- expect
dbg line 4: Map.size(config) = 2
dbg line 5: Map.get(config, "host") = Some("localhost")
dbg line 6: Map.get(config, "user") = None
-->

You write a map as `%{"key" => value, ...}`, and the empty map as `%{}`.

[`Map.get`](/reference/maps/#mapget) also returns a `Maybe`: `Some(value)`
when the key is there and `None` when it is not.
[Pattern Matching](/pattern-matching/) shows how to get the value out.

`a + b` merges two maps. When both have the same key, the value from `b` wins.
[`Map.merge`](/reference/maps/#mapmerge) does the same thing by name. To add or
replace a few entries, use a spread, shown [below](#spreads).

## Sets

```nomi-run
fn main() {
    s = Iter.to_set([1, 2, 3, 2, 1])

    dbg Set.size(s)
    dbg Set.contains(s, 2)
    dbg Set.contains(s, 9)
    dbg s + %[4, 5]
    dbg s - %[1]
}

```
<!-- expect
dbg line 4: Set.size(s) = 3
dbg line 5: Set.contains(s, 2) = True
dbg line 6: Set.contains(s, 9) = False
dbg line 7: s + %[4, 5] = %[1, 2, 3, 4, 5]
dbg line 8: s - %[1] = %[2, 3]
-->

You write a set as `%[a, b, c]`. A set holds each value once, so the set built
from `[1, 2, 3, 2, 1]` has three values. It keeps them in the order each one
first appeared.

If you write the same value twice in a set literal, as in `%[1, 2, 1]`, the
compiler reports an error. It does the same for a key written twice in a map
literal.

An empty set takes its element type from where it is used. On a binding with no
other use to go by, write the type: `empty: Set<Int> = %[]`.

`a + b` is every value in either set, and `a - b` is the values of `a` that are
not in `b`. [`Set.union`](/reference/sets/#setunion) and
[`Set.difference`](/reference/sets/#setdifference) do the same by name. To add
or remove one value, use [`Set.insert`](/reference/sets/#setinsert) and
[`Set.remove`](/reference/sets/#setremove).

A `case` can ask whether a set holds certain values, as
[Pattern Matching](/pattern-matching/#lists-vectors-and-sets) shows.

## Spreads

A spread, written `..`, builds a new collection from an existing one:

```nomi-run
fn main() {
    xs = ["Bo"]
    v = #["Bo"]
    s = %["red"]
    m = %{"host" => "localhost", "port" => "8080"}

    dbg ["Ada", ..xs]
    dbg #[..v, "Cy"]
    dbg #["Ada", ..v]
    dbg %[..s, "blue"]
    dbg %{..m, "port" => "9090", "user" => "ada"}
}

```
<!-- expect
dbg line 7: ["Ada", ..xs] = ["Ada", "Bo"]
dbg line 8: #[..v, "Cy"] = #["Bo", "Cy"]
dbg line 9: #["Ada", ..v] = #["Ada", "Bo"]
dbg line 10: %[..s, "blue"] = %["red", "blue"]
dbg line 11: %{..m, "port" => "9090", "user" => "ada"} = %{"host" => "localhost", "port" => "9090", "user" => "ada"}
-->

A spread can go anywhere in the literal, and a literal can have several:
`[..xs, "Cy", ..ys]`. In a map a later key wins, so `%{..m, "port" => "9090"}`
replaces `m`'s port, and `%{"port" => "9090", ..m}` keeps it. In a list,
vector or set, a spread can be any `Iter`, such as `[0, ..xs |> Iter.map(f)]`.

In a map you are updating, a `..` with nothing after it means the value
already under that key:

```nomi-run
fn main() {
    scores = %{"ada" => [3]}
    dbg %{..scores, "ada" => [5, ..], "bob" => [1, ..]}
}

```
<!-- expect
dbg line 3: %{..scores, "ada" => [5, ..], "bob" => [1, ..]} = %{"ada" => [5, 3], "bob" => [1]}
-->

`"ada" => [5, ..]` puts 5 in front of Ada's scores. Without the `..`, the new
list would replace the old one. For a key that is not there yet, `..` starts
from an empty collection, so `"bob"` gets only `[1]`. A struct field updates
the same way ([Updating a struct](/structs-enums-distinct/#updating-a-struct)).

## Ranges

`1..5` is a range that leaves out its end: 1, 2, 3 and 4. `1..=5` includes its
end. The type of the start and end decides the type of the range: `1..5` is a
`Range<Int>`, `"a".."m"` is a `Range<String>` and `0.0..=1.0` is a
`Range<Float>`.

Any range can tell you whether a value falls inside it. Some ranges can also be
walked through one value at a time, like a list. A range of whole numbers can,
because each number has a next one:

```nomi-run
fn main() {
    dbg Iter.to_list(1..5)
    dbg Iter.to_list(1..=5)
}

```
<!-- expect
dbg line 2: Iter.to_list(1..5) = [1, 2, 3, 4]
dbg line 3: Iter.to_list(1..=5) = [1, 2, 3, 4, 5]
-->

So does a range of [`Codepoint`](/reference/code_points/) values. A codepoint is
the number of one character. An ASCII character in single quotes, such as
`'a'`, is a codepoint:

```nomi-run
fn main() {
    'a'..='d'
    |> Iter.map(Codepoint.to_string)
    |> Iter.to_list()
    |> dbg
}

```
<!-- expect
dbg line 5:
  'a'..='d'
  |> Iter.map(Codepoint.to_string)
  |> Iter.to_list()
  = ["a", "b", "c", "d"]
-->

A range of strings or floats cannot be walked through, because there is no
single next value. There is no obvious string that comes right after `"a"`.
You can still ask whether a value is inside it:

```nomi-run
fn main() {
    dbg Range.contains("a".."m", "h")
    dbg Range.contains(0.0..=1.0, 1.0)
}

```
<!-- expect
dbg line 2: Range.contains("a".."m", "h") = True
dbg line 3: Range.contains(0.0..=1.0, 1.0) = True
-->

[`Range.step_by`](/reference/ranges/#rangestep_by) walks through a range in
steps of the size you give. It works on a range of any
[`Steppable`](/reference/steppable/) type. Below, that type is `Decimal`,
written with a `d` suffix. Like `Iter.map`, `Range.step_by` is lazy:

```nomi-run
fn main() {
    1.0d..=1.3d
    |> Range.step_by(0.1d)
    |> Iter.to_list()
    |> dbg
}

```
<!-- expect
dbg line 5:
  1.0d..=1.3d
  |> Range.step_by(0.1d)
  |> Iter.to_list()
  = [1.0d, 1.1d, 1.2d, 1.3d]
-->

A range whose end is below its start, such as `5..=1`, is empty. To count
down, step by a negative amount: `Range.step_by(5..=1, -1)` gives 5, 4, 3,
2 and 1.

The next chapter, [Iteration & Loops](/iteration-and-loops/), covers
[`Iter.loop`](/reference/iter/#iterloop) for loops that carry state. It also
covers what `break`, `continue` and `return` do inside the functions you pass
to `Iter.map` and the others.
