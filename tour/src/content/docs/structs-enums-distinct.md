---
title: Structs, Enums, Distinct Types
description: Records, tuples, sums, zero-cost wrappers, and tag-only types — the building blocks for your own data.
---

Nomi gives you these building blocks for declaring and grouping your own data:

- **`struct`** — records with named fields, one field line at a time.
- **`tuple`** — fixed positional groupings for small local shapes.
- **`type`** — distinct wrappers around an existing type (`type Id Int`)
  and tag-only "bare" types with no payload (`type Expired`).
- **`enum`** — sum types (a value that's exactly one of several variants),
  declared one variant line at a time.

Declaration bodies are newline-separated: struct fields and enum variants do
not use commas. Construction literals still do.

We'll start with named and anonymous records, then tuples, wrappers, tags, and
sums. The examples use `dbg` so you can inspect values before the next chapter
introduces [Interfaces & Dispatch](/interfaces-and-dispatch/) and
[`Display`](/reference/display/).

## Structs

A struct is a record with named fields. Construct with
`TypeName{field: value, …}`, access fields with `.field`. Fields can carry
default values:

```nomi-run
struct User {
    name: String
    age: Int = 0
}

fn main(): User {
    alice = User{name: "Alice", age: 30}
    dbg alice.name
    dbg alice.age

    // The default lets the caller omit `age`.
    bob = User{name: "Bob"}
    dbg bob.age

    // Field-name punning: `User{name, age}` is shorthand for
    // `User{name: name, age: age}` when bindings of those names are in scope.
    name = "Carol"
    age = 28
    carol = User{name, age}
    dbg carol
}

```
<!-- expect
dbg line 8: alice.name = "Alice"
dbg line 9: alice.age = 30
dbg line 13: bob.age = 0
dbg line 20: carol = User{name: "Carol", age: 28}
-->

Where the position already says which struct it wants (an annotation, a
parameter, a field, a return type), the type name can be left out: a brace
literal builds the struct its position expects.

```nomi-run
struct Address {
    street: String
    city: String
}

struct Person {
    name: String
    address: Address
}

fn main(): Person {
    // The annotation names Person, and Person's field names Address.
    ada: Person = {name: "Ada", address: {street: "1 Main", city: "Bath"}}
    dbg ada
}

```
<!-- expect
dbg line 14: ada = Person{name: "Ada", address: Address{street: "1 Main", city: "Bath"}}
-->

Without an expected struct type, a brace literal is an anonymous struct.

## Anonymous structs

When you want a quick record without declaring a named type, drop the type
name and write the struct literally. The value carries its own structural
type — `{x: Int, y: Int}` here — and field access works the same way:

```nomi-run
fn main(): Int {
    point = {x: 10, y: 20}
    dbg point
    dbg point.x + point.y
}

```
<!-- expect
dbg line 3: point = {x: 10, y: 20}
dbg line 4: point.x + point.y = 30
-->

## Updating a struct

Bindings are immutable, so changing a field means building a new value from an
existing one. `{..base, field: value}` copies `base` and replaces the fields
written after the spread:

```nomi-run
struct User {
    name: String
    age: Int
}

fn main(): User {
    alice = User{name: "Alice", age: 30}
    older = {..alice, age: 31}
    dbg older

    point = {x: 10, y: 20}
    dbg {..point, x: 9}

    // Punning works after a spread: `age` means `age: age`.
    age = 32
    dbg {..alice, age}
}

```
<!-- expect
dbg line 9: older = User{name: "Alice", age: 31}
dbg line 12: {..point, x: 9} = {x: 9, y: 20}
dbg line 16: {..alice, age} = User{name: "Alice", age: 32}
-->

The result has the base's type: a `User` in gives a `User` out, and an
anonymous struct gives an anonymous struct. That is why there is no
`User{..alice, age: 31}` spelling; the name would repeat what the base already
says.

To change a field inside a nested struct, spread the inner value as well, or
write a bare `{…}` at the field. Under a spread, a bare brace at a
struct-typed field is a patch applied to whatever the base holds there, so
both lines replace `address.city` and keep `address.street`:

```nomi-run
struct Address {
    street: String
    city: String
}

struct Person {
    name: String
    address: Address
}

fn main(): Person {
    ada = Person{name: "Ada", address: {street: "1 Main", city: "Bath"}}
    dbg {..ada, address: {..ada.address, city: "NYC"}}
    dbg {..ada, address: {city: "NYC"}}
}

```
<!-- expect
dbg line 13: {..ada, address: {..ada.address, city: "NYC"}} = Person{name: "Ada", address: Address{street: "1 Main", city: "NYC"}}
dbg line 14: {..ada, address: {city: "NYC"}} = Person{name: "Ada", address: Address{street: "1 Main", city: "NYC"}}
-->

[`Struct.update`](/reference/structs/) does the same by name, taking the patch
as an argument (`Struct.update(ada, {address: {city: "NYC"}})`), which is what
a computed patch or a pipe stage needs.

Everything other than a bare brace at a field position is a value. A nominal
literal (`Address{street: "2 Elm", city: "NYC"}`) replaces the field, a
binding or a call is whatever it evaluates to, and a nested spread is already
a complete `Address`. An interface-typed field takes values only, since a
patch cannot name fields the interface does not declare.

The patch exists only underneath a spread. Outside one, a bare brace at a
struct field builds that struct, so `Person{name: "Ada", address: {city:
"NYC"}}` has no base for `street` to come from and reports `missing field
'street' of Address`.

## Tuples

Tuples group a fixed number of values by position instead of by field name.
They are handy for small, local pairings where the positions are obvious.
A tuple's slots are read by position with `.0`, `.1`, …, or by destructuring
with a tuple pattern:

```nomi-run
fn main(): Int {
    pair = ("Ada", 37)
    (name, score) = pair

    dbg pair
    dbg name
    dbg pair.1

    score
}

```
<!-- expect
dbg line 5: pair = ("Ada", 37)
dbg line 6: name = "Ada"
dbg line 7: pair.1 = 37
-->

Use a struct when the grouped values deserve names at the boundary of an API.
`(String, Int)` is fine while the meaning is local; `User{name: String, score:
Int}` is clearer once the shape starts traveling.

## Field accessors

A lambda that only reads a field, like `|user| user.name`, can be written
`.name`: a function that reads that field from its argument. It works wherever
a function is expected and the argument's type is already known, such as a
call that follows the collection it runs over:

```nomi-run
struct User {
    name: String
    age: Int
}

fn main(): List<String> {
    [User{name: "Alan", age: 41}, User{name: "Ada", age: 36}]
    |> Iter.sort_by(.age)
    |> Iter.map(.name)
    |> Iter.to_list()
    |> dbg
}
```
<!-- expect
dbg line 11:
  [User{name: "Alan", age: 41}, User{name: "Ada", age: 36}]
  |> Iter.sort_by(.age)
  |> Iter.map(.name)
  |> Iter.to_list()
  = ["Ada", "Alan"]
-->

`.address.city` reads a chain of fields, and a tuple's slots read the same way:
`Iter.map(pairs, .1)`. An accessor is always an argument; to read a field of a
value you already have, write `user.name`.

## Distinct types

`type Name UnderlyingType` declares a *distinct* type that wraps an existing
one. The two share a runtime representation but the compiler keeps them
separate — a function taking `Id` will refuse a plain `Int`, even though
both use the same representation. This catches whole categories of
domain bugs at compile time:

```nomi-run
type Id Int
type Email String

fn main(): Int {
    id = Id(42)
    dbg id

    // To pull the inner value out, cast or destructure-bind:
    raw = Int(id)
    dbg raw

    Id(again) = id
    dbg again
}

```
<!-- expect
dbg line 6: id = Id(42)
dbg line 10: raw = 42
dbg line 13: again = 42
-->

## Bare types

A `type` declaration with **nothing after the name** declares a zero-sized
"bare" type — just a tag, no payload. Useful as a sentinel value or as a
no-data variant when embedded in an enum (see the deep-dive below), and for
stateless adapters that need a real value to implement an interface:

```nomi-run
type Expired
type Online

fn main(): Online {
    // Bare types are constructed by name — no parens, no fields.
    state = Expired
    dbg state
    dbg Online
}

```
<!-- expect
dbg line 7: state = Expired
dbg line 8: Online = Online
-->

## Enums

An enum is a value that's exactly one of a fixed set of variants. Variants
can be bare (no payload), positional (a single anonymous field), or
struct-shaped (named fields):

```nomi-run
enum Direction {
    North
    South
    East
    West
}

enum Shape {
    Circle Float
    Rectangle {width: Float, height: Float}
}

fn main(): Shape {
    dbg Direction.North

    c = Shape.Circle(3.0)
    dbg c

    r = Shape.Rectangle{width: 4.0, height: 5.0}
    dbg r
}

```
<!-- expect
dbg line 14: Direction.North = North
dbg line 17: c = Circle(3.0)
dbg line 20: r = Rectangle{width: 4.0, height: 5.0}
-->

### Dot-leading shorthand

The fully-qualified `Direction.North` / `Shape.Circle(...)` form
always works. When the **expected type is known** — most commonly
inside a `case` whose subject is a known enum, but also a binding
annotation, a function parameter, or a return value — you can drop
the enum name and use the dot-leading shorthand:

```nomi-run
enum Direction {
    North
    South
    East
    West
}

fn describe(d: Direction): String {
    // `d` is a Direction, so each arm's dot-leading pattern
    // unambiguously matches one of Direction's variants.
    case d {
        .North -> "up"
        .South -> "down"
        .East -> "right"
        .West -> "left"
    }
}

fn main(): String {
    // `describe` expects a Direction, so `.North` means `Direction.North`.
    dbg describe(.North)
    dbg describe(.West)
}
```
<!-- expect
dbg line 21: describe(.North) = "up"
dbg line 22: describe(.West) = "left"
-->

This previews `case`, which [Pattern Matching](/pattern-matching/) covers in full — but
the dot-leading rule is the same in any position the compiler can
pin the type: pattern arms, function arguments, return values, list
element types (`walk: List<Direction> = [.North, .East]`), annotated
bindings (`d: Direction = .North`).

## Type-Owned Functions

Named types can own helper functions in an `impl Type { ... }` block. Nomi
does not have `value.method()` syntax; the owner stays visible at the call site:
`User.full_name(user)`, `String.trim(text)`, `List.concat(xs, ys)`.

```nomi-run
struct User {
    first: String
    last: String
}

impl User {
    fn full_name(user: User): String {
        "${user.first} ${user.last}"
    }

    fn rename(user: User, first: String): User {
        User{first, last: user.last}
    }
}

fn main(): String {
    alice = User{first: "Ada", last: "Lovelace"}
    dbg User.full_name(alice)

    renamed = User.rename(alice, "Augusta")
    dbg User.full_name(renamed)

    User.full_name(renamed)
}

```
<!-- expect
dbg line 18: User.full_name(alice) = "Ada Lovelace"
dbg line 21: User.full_name(renamed) = "Augusta Lovelace"
-->

Use `impl Type` for operations whose natural home is a real value type:
constructors, projections, validations, conversions, and transformations. The
same shape works for structs, enums, distinct types, opaque types, host types,
and generic types such as `impl Box<T> { ... }`.

:::note[Continue or dig deeper]
The tour moves on to [**Pattern Matching, Maybe, Result & Try**](/pattern-matching/),
which shows how `case` destructures every value you've now learned to
build. Or stay here for the deep-dive section below — `embeds` for
composing existing types as enum variants, and `typealias` for
shorthand over complex generic types.
:::

## Going deeper

### Embedding existing types

When a variant's payload would be a type you've already defined as a
standalone struct, distinct type, or bare type, declare it with `embeds`
instead of repeating the shape inline. The embedded type stays
independently usable, and values of that type flow into the enum
*without* a wrapping constructor:

```nomi-run
struct Click {
    x: Int
    y: Int
}

struct KeyDown {
    key: String
}

type FocusLost // bare — zero-sized

enum Event {
    embeds Click
    embeds KeyDown
    embeds FocusLost
}

fn main(): List<Event> {
    // Each event constructed standalone — no `Event.Click{...}` wrapping.
    // Structs use `{...}`; the bare type is just its name.
    events: List<Event> = [Click{x: 10, y: 20}, KeyDown{key: "Enter"}, FocusLost]
    dbg events
}

```
<!-- expect
dbg line 22: events = [Click{x: 10, y: 20}, KeyDown{key: "Enter"}, FocusLost]
-->

The benefit is **subtype coercion**: a `Click` value flows into any
`Event`-typed slot (list elements, function arguments, return values)
without explicit construction. Pattern matching destructures embedded
structs the same way as struct variants — `case e { Event.Click{x, y} -> … }` —
shown in [Pattern Matching](/pattern-matching/).

`embeds` is Nomi's replacement for the OO `extends` pattern: a
`List<Event>` of mixed UI event values is an enum with one `embeds`
declaration per concrete event shape rather than a class hierarchy.

### Type aliases

A `typealias` is a *transparent* synonym for an existing type — the alias
and the original are fully interchangeable, no wrapping or conversion. The
payoff is making complex generic signatures readable:

```nomi-run
typealias Index Map<String, List<Int>>

fn record(index: Index, bucket: String, n: Int): Index {
    existing: List<Int> = case Map.get(index, bucket) {
        Some(xs) -> xs
        None -> []
    }

    Map.put(index, bucket, [n, ..existing])
}

fn main(): Map<String, List<Int>> {
    start: Index = Map.empty()

    result =
        start
        |> record("evens", 2)
        |> record("evens", 4)
        |> record("odds", 1)

    dbg result
}
```
<!-- expect
dbg line 21: result = {"evens" => [4, 2], "odds" => [1]}
-->

`record`'s signature reads `(Index, String, Int) -> Index` instead of
the noisier `(Map<String, List<Int>>, String, Int) -> Map<String, List<Int>>`.
`Index` is **literally** a [`Map`](/reference/maps/#type-map) of [`List`](/reference/lists/#type-list)
values — call sites pass plain map literals, and `result` is the same type
whether you spell it `Index` or the full generic.

Use `type` when you want a *new* type the compiler distinguishes from
its representation (`UserId` shouldn't accidentally be passed where
`OrderId` is expected — both are represented as `Int`, but the wrapper
keeps them apart). Use `typealias` when a complex type expression has
a meaningful name and writing it out everywhere clutters signatures.
