---
title: Structs, Enums, Distinct Types
description: Structs, tuples, wrapper types, tag types and enums, the ways to declare your own data.
---

This chapter covers the ways to declare your own data:

- A `struct` is a record with named fields.
- A tuple groups a few values by position.
- A `type` wraps an existing type under a new name (`type Id Int`), or declares
  a tag with no data at all (`type Expired`).
- An `enum` is a value that is one of several variants.

In a declaration, each struct field and each enum variant goes on its own line,
with no commas between them. When you build a value, you do use commas.

The examples print values with `dbg`. The next chapter,
[Interfaces & Dispatch](/interfaces-and-dispatch/), shows how to give a type
its own printed form with [`Display`](/reference/display/).

## Structs

```nomi-run
struct User {
    name: String
    age: Int = 0
}

fn main() {
    alice = #User{name: "Alice", age: 30}
    dbg alice.name
    dbg alice.age

    // The default lets the caller omit `age`.
    bob = #User{name: "Bob"}
    dbg bob.age

    // Field-name punning: `#User{name, age}` is shorthand for
    // `#User{name: name, age: age}` when bindings of those names are in scope.
    name = "Carol"
    age = 28
    carol = #User{name, age}
    dbg carol
}

```
<!-- expect
dbg line 8: alice.name = "Alice"
dbg line 9: alice.age = 30
dbg line 13: bob.age = 0
dbg line 20: carol = #User{name: "Carol", age: 28}
-->

You build a struct with `#TypeName{field: value, ...}` and read a field with
`.field`. A field can have a default value, like `age: Int = 0`. Then you can
leave that field out when you build the struct.

You can often leave out the type name too, and write just `#{...}`: when the
code around the literal already says which struct it needs, the literal
builds that struct. That happens with a type annotation, a parameter, a field, a
return type, or an earlier element of the same list or map
(`[#Address{...}, #{...}]`):

```nomi-run
struct Address {
    street: String
    city: String
}

struct Person {
    name: String
    address: Address
}

fn main() {
    // The annotation names Person, and Person's field names Address.
    ada: Person = #{name: "Ada", address: #{street: "1 Main", city: "Bath"}}
    dbg ada
}

```
<!-- expect
dbg line 14: ada = #Person{name: "Ada", address: #Address{street: "1 Main", city: "Bath"}}
-->

When nothing says which struct is needed, a `#{...}` literal is an anonymous
struct. A plain `{` always opens a block, and a name followed by `{`, such as
`User{name: "Ada"}`, is an error: every struct literal starts with `#`.

## Anonymous structs

```nomi-run
fn main() {
    point = #{x: 10, y: 20}
    dbg point
    dbg point.x + point.y
}

```
<!-- expect
dbg line 3: point = #{x: 10, y: 20}
dbg line 4: point.x + point.y = 30
-->

An anonymous struct is a quick record with no declared type. Its type is just
its fields, written the way the value is: here `#{x: Int, y: Int}`. You read
its fields the same way. Punning works here too: with a binding `name` in scope,
`#{name}` is `#{name: name}`.

The `#` starts every struct literal, named or not, as it starts a tuple
`#(...)` (below) and a vector `#[...]`. Maps and sets start with `%` instead:
`%{"a" => 1}`, `%[1, 2]` ([Collections](/collections/)).

## Updating a struct

A value never changes. To change a field, you build a new value from the old
one. `#{..base, field: value}` copies `base` and replaces the fields you write
after it:

```nomi-run
struct User {
    name: String
    age: Int
}

fn main() {
    alice = #User{name: "Alice", age: 30}
    older = #{..alice, age: 31}
    dbg older

    point = #{x: 10, y: 20}
    dbg #{..point, x: 9}

    // Punning works after a spread: `age` means `age: age`.
    age = 32
    dbg #{..alice, age}
}

```
<!-- expect
dbg line 9: older = #User{name: "Alice", age: 31}
dbg line 12: #{..point, x: 9} = #{x: 9, y: 20}
dbg line 16: #{..alice, age} = #User{name: "Alice", age: 32}
-->

The result has the same type as `base`. Update a `User` and you get a `User`;
update an anonymous struct and you get an anonymous struct. So you do not write
the type name: `#User{..alice, age: 31}` is an error.

To change a field inside a nested struct, there are two ways:

```nomi-run
struct Address {
    street: String
    city: String
}

struct Person {
    name: String
    address: Address
}

fn main() {
    ada = #Person{name: "Ada", address: #{street: "1 Main", city: "Bath"}}
    dbg #{..ada, address: #{..ada.address, city: "NYC"}}
    dbg #{..ada, address: #{.., city: "NYC"}}
}

```
<!-- expect
dbg line 13: #{..ada, address: #{..ada.address, city: "NYC"}} = #Person{name: "Ada", address: #Address{street: "1 Main", city: "NYC"}}
dbg line 14: #{..ada, address: #{.., city: "NYC"}} = #Person{name: "Ada", address: #Address{street: "1 Main", city: "NYC"}}
-->

The first line spreads `ada.address` by name. The second writes a `..` with
nothing after it, which means the value the field already holds. Both change
the city and keep the street.

[`Struct.update`](/reference/structs/) does the same as a function call. It
takes the changes as an argument:
`Struct.update(ada, #{address: #{.., city: "NYC"}})`. Use it when the changes
are computed, or as a step in a pipe.

Anything else you put in a field replaces the whole value. That includes a
`#{...}` literal without `..`. So `#{..ada, address: #{city: "NYC"}}` is an error,
because it builds a new `Address` with no street. The compiler says
`missing field 'street' of Address` and suggests `#{.., city: "NYC"}`.

A field whose type is an interface can only be replaced whole. An interface
declares no fields, so there are none to update.

The same `..` works for a map's values and for list fields (`[t, ..]`), as the
[collections chapter](/collections/#spreads) shows.

## Tuples

```nomi-run
fn main() {
    pair = #("Ada", 37)
    #(name, _) = pair

    dbg pair
    dbg name
    dbg pair.1
}

```
<!-- expect
dbg line 5: pair = #("Ada", 37)
dbg line 6: name = "Ada"
dbg line 7: pair.1 = 37
-->

A tuple groups a fixed number of values by position instead of by name. You
write one as `#(a, b)`, with at least two values, and its type the same way,
`#(String, Int)`. Plain parentheses only group. You read a slot with `.0`,
`.1` and so on, or take the tuple apart with a pattern like `#(name, _)`.

Tuples suit small groupings used in one place, where the positions are
obvious. When the values travel further, such as through a public function,
give them names with a struct: a `User` with `name` and `score` fields says
more than `#(String, Int)`.

## Field accessors

```nomi-run
struct User {
    name: String
    age: Int
}

fn main() {
    [#User{name: "Alan", age: 41}, #User{name: "Ada", age: 36}]
    |> Iter.sort_by(.age)
    |> Iter.map(.name)
    |> Iter.to_list()
    |> dbg
}
```
<!-- expect
dbg line 11:
  [#User{name: "Alan", age: 41}, #User{name: "Ada", age: 36}]
  |> Iter.sort_by(.age)
  |> Iter.map(.name)
  |> Iter.to_list()
  = ["Ada", "Alan"]
-->

`.name` is short for the lambda `fn user -> user.name`. You can use it wherever a
function is expected and the argument's type is already known. In a pipe, the
type comes from the collection before it.

`.address.city` reads a field of a field. A tuple slot works too:
`Iter.map(pairs, .1)`. An accessor is only ever passed to a function. To read
a field of a value you have, write `user.name`.

## Distinct types

```nomi-run
type Id Int
type Email String

fn raw(Id(n)) -> Int {
    n
}

fn main() {
    id = Id(42)
    dbg id

    // To read the inner value, destructure it:
    Id(n) = id
    dbg n

    dbg raw(id)
}

```
<!-- expect
dbg line 10: id = Id(42)
dbg line 14: n = 42
dbg line 16: raw(id) = 42
-->

`type Id Int` declares a new type, `Id`, that holds an `Int`. At run time an
`Id` is stored just like an `Int`. The compiler still treats them as different
types, so a function that takes an `Id` will not accept a plain `Int`. That
catches mix-ups, like passing a user's ID where an order's ID belongs, before
the program runs.

You build one with `Id(42)`. To get the `Int` back, use the pattern `Id(n)`.
It works anywhere a pattern does: in a binding, in a `case` arm, or as a
parameter, as in `raw`.

`Int(id)` does not convert. It is an error, and the compiler's hint shows the
pattern to write instead.

A distinct type over a tuple or a collection is built the way its literal is,
with the type's name right after the symbol:

```nomi-run
type Pair #(Int, String)
type Path Vector<Int>
type Scores Map<String, Int>

fn main() {
    p = #Pair(1, "one")
    #Pair(n, word) = p
    dbg p
    dbg n
    dbg word

    dbg #Path[3, 1, 4]
    dbg %Scores{"ada" => 3}
}

```
<!-- expect
dbg line 8: p = #Pair(1, "one")
dbg line 9: n = 1
dbg line 10: word = "one"
dbg line 12: #Path[3, 1, 4] = Path(#[3, 1, 4])
dbg line 13: %Scores{"ada" => 3} = Scores(%{"ada" => 3})
-->

`#Pair(1, "one")` builds a `Pair` from its two values, and the pattern
`#Pair(n, word)` takes it apart again. The call form takes the one value a
distinct type wraps, so for a `Pair` that is the whole tuple,
`Pair(#(1, "one"))`; `Pair(1, "one")` is an error whose hint names
`#Pair(...)`. A `Path` or `Scores` prints in its call form, the type's name
around the collection it wraps.

## Bare types

```nomi-run
type Expired
type Online

fn main() {
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

A `type` with nothing after its name is a bare type. It is a tag with no data,
and it takes no memory. You write its value as just its name.

Bare types are useful as marker values, and as data-free variants of an enum
(see [Embedding existing types](#embedding-existing-types)). They also suit
an interface implementation that needs a value but no data.

## Enums

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

fn main() {
    dbg Direction.North

    c = Shape.Circle(3.0)
    dbg c

    r = #Shape.Rectangle{width: 4.0, height: 5.0}
    dbg r
}

```
<!-- expect
dbg line 14: Direction.North = North
dbg line 17: c = Circle(3.0)
dbg line 20: r = #Rectangle{width: 4.0, height: 5.0}
-->

An enum value is exactly one of a fixed set of variants. A variant can hold
nothing (`North`), one value (`Circle Float`), or named fields
(`Rectangle {width: Float, height: Float}`).

### Dot-leading shorthand

```nomi-run
enum Direction {
    North
    South
    East
    West
}

fn describe(d: Direction) -> String {
    // `d` is a Direction, so each arm's dot-leading pattern
    // unambiguously matches one of Direction's variants.
    case d {
        .North -> "up"
        .South -> "down"
        .East -> "right"
        .West -> "left"
    }
}

fn main() {
    // `describe` expects a Direction, so `.North` means `Direction.North`.
    dbg describe(.North)
    dbg describe(.West)
}
```
<!-- expect
dbg line 21: describe(.North) = "up"
dbg line 22: describe(.West) = "left"
-->

`Direction.North` always works. When the compiler already knows the code needs
a `Direction`, you can write just `.North`. For a variant with fields, the `#`
goes in front of the dot: `#.Rectangle{width: 4.0, height: 5.0}`, as in
`#Shape.Rectangle{...}`.

That works in the arms of a `case` on a `Direction`, as above. `case` is
covered in [Pattern Matching](/pattern-matching/). It also works for an
argument (`describe(.North)`), a return value, a list element
(`walk: List<Direction> = [.North, .East]`) and an annotated binding
(`d: Direction = .North`).

In a list, vector, set or map with no annotation, the first element can name
the enum for the rest. In `walk = [Direction.North, .East, .East]` the first
element makes the list a `List<Direction>`, and each bare variant after it is
a `Direction`. A map's keys and values work the same way, each on its own:
`%{Direction.North => 1, .South => 2}`. The compiler reads left to right, so
`[.North, Direction.East]` is an error at `.North`.

## Type-Owned Functions

```nomi-run
struct User {
    first: String
    last: String
}

impl User {
    fn full_name(user: User) -> String {
        "${user.first} ${user.last}"
    }

    fn rename(user: User, first: String) -> User {
        #User{first, last: user.last}
    }
}

fn main() {
    alice = #User{first: "Ada", last: "Lovelace"}
    dbg User.full_name(alice)

    renamed = User.rename(alice, "Augusta")
    dbg User.full_name(renamed)
}

```
<!-- expect
dbg line 18: User.full_name(alice) = "Ada Lovelace"
dbg line 21: User.full_name(renamed) = "Augusta Lovelace"
-->

An `impl User { ... }` block holds functions that belong to `User`. You call
them with the type's name in front: `User.full_name(user)`. Nomi has no
`value.method()` syntax, so the type is always visible where you call. The
stdlib works the same way: `String.trim(text)`, `List.concat(xs, ys)`.

Put functions here that build, read, check, convert or change values of the
type. An `impl` block works for any named type: structs, enums, distinct
types, opaque types, host types, and generic types such as
`impl Box<T> { ... }`.

:::note[Continue or dig deeper]
The tour moves on to [**Pattern Matching, Maybe & Result**](/pattern-matching/),
which shows how `case` takes apart every value you can now build. The section
below covers `embeds`, for using existing types as enum variants, and
`typealias`, for short names for long anonymous types.
:::

## Going deeper

### Embedding existing types

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

fn main() {
    // Each event constructed standalone — no `#Event.Click{...}` wrapping.
    // Structs use `#Name{...}`; the bare type is just its name.
    events: List<Event> = [#Click{x: 10, y: 20}, #KeyDown{key: "Enter"}, FocusLost]
    dbg events
}

```
<!-- expect
dbg line 22: events = [#Click{x: 10, y: 20}, #KeyDown{key: "Enter"}, FocusLost]
-->

`embeds Click` makes the existing type `Click` a variant of `Event`. You do not
repeat its fields in the enum, and `Click` is still a type of its own. You can
embed a struct, a distinct type or a bare type.

A `Click` value can go anywhere an `Event` is expected, with no wrapping: a
list element, an argument or a return value. It does not work the other way.
An `Event` might hold a different variant, so to pass it to a function that
takes a `Click`, match it first. A `case` takes an embedded variant apart like
any other: `case e { #.Click{x, y} -> ... }`. See
[Pattern Matching](/pattern-matching/).

Where another language would use a class hierarchy, Nomi uses an enum like
this one, with one `embeds` per kind of event.

### Type aliases

```nomi-run
typealias Rule (String) -> Maybe<String>

fn problems(name: String, rules: List<Rule>) -> List<String> {
    rules
    |> Iter.map(fn rule -> rule(name))
    |> Maybe.values()
}

fn too_short(name: String) -> Maybe<String> {
    if String.length(name) < 3 {
        Some("too short")
    } else {
        None
    }
}

fn has_space(name: String) -> Maybe<String> {
    if String.contains(name, " ") {
        Some("has a space")
    } else {
        None
    }
}

fn main() {
    rules: List<Rule> = [too_short, has_space]

    dbg problems("al", rules)
    dbg problems("ada lovelace", rules)
    dbg problems("ada", rules)
}
```
<!-- expect
dbg line 28: problems("al", rules) = ["too short"]
dbg line 29: problems("ada lovelace", rules) = ["has a space"]
dbg line 30: problems("ada", rules) = []
-->

A `typealias` is another name for an existing type. `Rule` and
`(String) -> Maybe<String>` are the same type, and you can use either one
anywhere. There is no wrapping and no conversion: `too_short` is an ordinary
function, and the list holds it as a `Rule`.

The point is shorter signatures. `problems` takes a `List<Rule>` instead of a
`List<(String) -> Maybe<String>>`. Reach for an alias to shorten a long
anonymous shape like this one: a function type, an anonymous struct or a long
tuple. An alias takes no type parameters, and you cannot `impl` one or call a
function through it; it only stands for its target.

Use `type` when you want the compiler to tell two things apart. A `UserId` and
an `OrderId` may both hold an `Int`, but you should not pass one where the
other belongs. When the thing deserves a name of its own, declare a struct or
a `type` rather than an alias.
