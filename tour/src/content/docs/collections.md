---
title: Collections
description: Lists, Vectors, Maps, Sets — immutable and persistent; every operation returns a new collection.
---

Nomi has four core collection types — [`List<T>`](/reference/lists/#type-list),
[`Vector<T>`](/reference/vectors/#type-vector), [`Map<K, V>`](/reference/maps/#type-map), and
[`Set<T>`](/reference/sets/#struct-set) — plus [`Range<T>`](/reference/ranges/#struct-range), an interval over comparable values. Ranges over
discrete start and end values, such as [`Int`](/reference/int/) and
[`Codepoint`](/reference/codepoints/), also iterate. The
collections are all **immutable and persistent**: every operation returns a new collection,
leaving the original alone. The stdlib puts the value being transformed first on
every operation, so chains read naturally through pipes.

## Iter

Every collection, every iterable range and `String` (one grapheme at a time)
implements [`Iter`](/reference/iter/#interface-iter). The generic algorithms
live once, on the `Iter` owner: `Iter.map`, `Iter.filter`, `Iter.take`,
`Iter.reduce`, `Iter.count`, and the rest. A collection's own owner keeps only
what is specific to it, such as `Map.get` or `List.head`.

The adapters are lazy. `Iter.map`, `Iter.filter`, `Iter.take` and the like
build a pipeline and run nothing; no intermediate collection is made between
stages. The work happens when a consumer pulls values through: `Iter.to_list`,
`Iter.to_vector`, `Iter.to_set`, `Iter.to_map`, `String.join`, `Iter.reduce`,
`Iter.count`, `Iter.each`. Each value then passes through every stage before
the next one starts, and the pipeline stops as soon as the consumer has enough.
That is why an unbounded source is safe:

```nomi-run
import std/io

fn main(): List<Int> {
    result =
        Range.naturals()
        |> Iter.map(|n| {
            io.print("squaring ${n}")
            n * n
        })
        |> Iter.filter(|sq| sq > 10)
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

Only 0 through 5 are squared: `Iter.take(2)` has its two values after 25.

## Lists

`[a, b, c]` is the literal syntax. The [`List`](/reference/lists/#type-list) owner provides the
List-specific operations (`concat`, `head`, `tail`, …). A pipeline over a list
goes through `Iter`, and [`Iter.to_list()`](/reference/iter/#iterto_list)
materializes a List again when you want one:

```nomi-run
fn main(): Int {
    xs = [1, 2, 3, 4, 5]

    xs
    |> Iter.filter(|n| n > 2)
    |> Iter.map(|n| n * n)
    |> Iter.reduce(|acc = 0, n| acc + n)
    |> dbg
}

```
<!-- expect
dbg line 8:
  xs
  |> Iter.filter(|n| n > 2)
  |> Iter.map(|n| n * n)
  |> Iter.reduce(|acc = 0, n| acc + n)
  = 50
-->

## Vectors

`#[a, b, c]` is the vector literal, and [`Iter.to_vector`](/reference/iter/#iterto_vector)
builds one from any iterator. `Vector<T>` is an immutable random-access sequence.
Use `List<T>` for cons-list pattern matching and cheap prepends; use `Vector<T>` when indexed
lookup and push-at-end are the natural operations. Lists and vectors both implement [`Add`](/reference/add/),
so `xs + ys` concatenates two values of the same collection type; the named functions
[`List.concat`](/reference/lists/#listconcat) and
[`Vector.concat`](/reference/vectors/#vectorconcat) are the explicit equivalents.

```nomi-run
fn main() {
    names =
        #["Ada", "Grace"]
        |> Vector.push("Katherine")

    dbg names
    dbg Vector.length(names)
    dbg Vector.at(names, 1)
    dbg names + #["Dorothy"]
    Unit
}

```
<!-- expect
dbg line 6: names = #["Ada", "Grace", "Katherine"]
dbg line 7: Vector.length(names) = 3
dbg line 8: Vector.at(names, 1) = Some("Grace")
dbg line 9: names + #["Dorothy"] = #["Ada", "Grace", "Katherine", "Dorothy"]
-->

## Maps

`{"key" => value, …}` is the map literal. [`Map.get`](/reference/maps/#mapget) returns [`Maybe<V>`](/reference/maybe/) —
`Some(value)` when the key exists, `None` when it doesn't. `a + b` merges two
maps, and the right-hand map wins on a shared key
([`Map.merge`](/reference/maps/#mapmerge) is the named equivalent):

```nomi-run
fn main(): Maybe<String> {
    config = {"host" => "localhost", "port" => "8080"}

    dbg Map.size(config)
    dbg config + {"port" => "9090", "user" => "ada"}
    dbg Map.get(config, "host")
    dbg Map.get(config, "user")
}

```
<!-- expect
dbg line 4: Map.size(config) = 2
dbg line 5: config + {"port" => "9090", "user" => "ada"} = {"host" => "localhost", "port" => "9090", "user" => "ada"}
dbg line 6: Map.get(config, "host") = Some("localhost")
dbg line 7: Map.get(config, "user") = None
-->

We'll see how to unwrap `Maybe<T>` properly in [Pattern Matching](/pattern-matching/).

## Sets

`#{a, b, c}` is the set literal. `Set<T>` stores unique values and keeps the
first occurrence's insertion order for iteration. Empty sets need type context:
`empty: Set<Int> = #{}`. Like lists and vectors, sets combine with another set of
the same type: `a + b` is the union and `a - b` the difference
([`Set.union`](/reference/sets/#setunion) and
[`Set.difference`](/reference/sets/#setdifference)). To add or remove one
element, use [`Set.insert`](/reference/sets/#setinsert) and
[`Set.remove`](/reference/sets/#setremove), or combine with a one-element set.

```nomi-run
fn main(): Set<Int> {
    s = #{1, 2, 3, 2, 1}

    dbg Set.size(s)
    dbg Set.contains?(s, 2)
    dbg Set.contains?(s, 9)
    dbg s + #{4, 5}
    dbg s - #{1}
}

```
<!-- expect
dbg line 4: Set.size(s) = 3
dbg line 5: Set.contains?(s, 2) = True
dbg line 6: Set.contains?(s, 9) = False
dbg line 7: s + #{4, 5} = #{1, 2, 3, 4, 5}
dbg line 8: s - #{1} = #{2, 3}
-->

## Ranges

`1..5` (half-open) and `1..=5` (inclusive) are range literals. The start and
end values determine the range type: `1..5` is `Range<Int>`, `"a".."m"` is
`Range<String>`, and `0.0..=1.0` is `Range<Float>`.

Every range is an interval over comparable values, so it can answer containment
questions. A range is also iterable only when its value type has a well-defined
next value. That makes integer and codepoint ranges natural
replacements for generated lists:

```nomi-run
fn main(): List<Int> {
    dbg Iter.to_list(1..5)
    dbg Iter.to_list(1..=5)

    // Unbounded ranges work too; bound them with Iter.take downstream.
    Range.naturals()
    |> Iter.take(3)
    |> Iter.to_list()
    |> dbg
}

```
<!-- expect
dbg line 2: Iter.to_list(1..5) = [1, 2, 3, 4]
dbg line 3: Iter.to_list(1..=5) = [1, 2, 3, 4, 5]
dbg line 9:
  Range.naturals()
  |> Iter.take(3)
  |> Iter.to_list()
  = [0, 1, 2]
-->

`Codepoint` is discrete too, so a range of codepoints iterates. An ASCII
codepoint has a literal, the character in single quotes (`'a'`), so the range
is written with its two ends:

```nomi-run
fn main(): List<String> {
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

Strings and floats are comparable, so they work as range bounds. They are not
plain iterables because neither type has one canonical successor: a string
range like `"a".."m"` can test whether `"a" <= "h" < "m"`, but there is no
single obvious next string after `"a"` to enumerate.

```nomi-run
fn main(): Bool {
    dbg Range.contains?("a".."m", "h")
    dbg Range.contains?(0.0..=1.0, 1.0)
}

```
<!-- expect
dbg line 2: Range.contains?("a".."m", "h") = True
dbg line 3: Range.contains?(0.0..=1.0, 1.0) = True
-->

When the caller supplies the step, [`Range.step_by`](/reference/ranges/#rangestep_by) turns [`Steppable`](/reference/steppable/) ranges into
lazy iterables:

```nomi-run
fn main(): List<Decimal> {
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

## Persistent immutability

A "modifying" operation returns a *new* collection; the original binding still
holds its original value:

```nomi-run
fn main(): Int {
    m1 = {"a" => 1, "b" => 2}
    m2 = Map.put(m1, "c", 3)

    dbg Map.size(m1)
    dbg Map.size(m2)
}

```
<!-- expect
dbg line 5: Map.size(m1) = 2
dbg line 6: Map.size(m2) = 3
-->

The next chapter — [Iteration & Loops](/iteration-and-loops/) — covers
[`Iter.loop`](/reference/iter/#iterloop) for stateful iteration and how `break` / `continue` / `return`
behave inside the callbacks you pass to these collection ops.
