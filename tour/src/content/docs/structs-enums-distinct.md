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

The examples print values with `dbg`.
[Interfaces & Dispatch](/interfaces-and-dispatch/), later in the tour, shows
how to give a type its own printed form with [`Display`](/reference/display/).

## Structs

```nomi-run
struct User
    name: String
    age:  Int = 0
end

fn main()
    alice = User{name: "Alice", age: 30}
    dbg alice.name
    dbg alice.age

    // The default lets the caller omit `age`.
    bob = User{name: "Bob"}
    dbg bob.age

    // Field-name punning: `User{name:, age:}` is shorthand for
    // `User{name: name, age: age}` when bindings of those names are in scope.
    name = "Carol"
    age = 28
    carol = User{name:, age:}
    dbg carol
end
```
<!-- expect
dbg line 8: alice.name = "Alice"
dbg line 9: alice.age = 30
dbg line 13: bob.age = 0
dbg line 20: carol = User{name: "Carol", age: 28}
-->

You build a struct with `TypeName{field: value, ...}` and read a field with
`.field`. A field can have a default value, like `age: Int = 0`. Then you can
leave that field out when you build the struct. When the default says the
type, the type can go: `age = 0` is an `Int` field. An empty list or `None`
says no type, so `tags: List<String> = []` keeps it.

You can often leave out the type name too, and write just `{...}`: when the
code around the literal already says which struct it needs, the literal
builds that struct. That happens with a type annotation, a parameter, a field, a
return type, or an earlier element of the same list or map
(`[Address{...}, {...}]`):

```nomi-run
struct Address
    street: String
    city:   String
end

struct Person
    name:    String
    address: Address
end

fn main()
    // The annotation names Person, and Person's field names Address.
    ada: Person = {name: "Ada", address: {street: "1 Main", city: "Bath"}}
    dbg ada
end
```
<!-- expect
dbg line 14: ada = Person{name: "Ada", address: Address{street: "1 Main", city: "Bath"}}
-->

When nothing says which struct is needed, a `{...}` literal is an anonymous
struct. A `{` always opens data, since code is never in braces, so a named
literal stands in the head of an `if` or `case` as anywhere else:
`case User{name: "Ada"}`.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A field that may be absent has type `Option<T>`. A default only spares
  the caller from writing a value: `nickname: Option<String> = None` is both.
- A default runs once for each construction that leaves its field out.
- Each field is named once: `User{name: "Al", name: "Bo"}` is an error.
- A struct also has a call form taking an anonymous struct:
  `User({name: "Ada"})`, which a pipe can feed.
- `{...}` stays anonymous where an interface or a type parameter is
  expected, since neither names one struct.

Spec: [Structs](/spec/types/#structs),
[Construction](/spec/types/#construction),
[Target-typed construction](/spec/types/#target-typed-construction).

</div>

## Anonymous structs

```nomi-run
fn main()
    point = {x: 10, y: 20}
    dbg point
    dbg point.x + point.y
end
```
<!-- expect
dbg line 3: point = {x: 10, y: 20}
dbg line 4: point.x + point.y = 30
-->

An anonymous struct is a quick record with no declared type. Its type is just
its fields, written the way the value is: here `{x: Int, y: Int}`. You read
its fields the same way. Punning works here too: with a binding `name` in scope,
`{name:}` is `{name: name}`.

## Updating a struct

A value never changes. To change a field, you build a new value from the old
one. `{..base, field: value}` copies `base` and replaces the fields you write
after it:

```nomi-run
struct User
    name: String
    age:  Int
end

fn main()
    alice = User{name: "Alice", age: 30}
    older = {..alice, age: 31}
    dbg older

    point = {x: 10, y: 20}
    dbg {..point, x: 9}

    // Punning works after a spread: `age:` means `age: age`.
    age = 32
    dbg {..alice, age:}
end
```
<!-- expect
dbg line 9: older = User{name: "Alice", age: 31}
dbg line 12: {..point, x: 9} = {x: 9, y: 20}
dbg line 16: {..alice, age:} = User{name: "Alice", age: 32}
-->

The result has the same type as `base`. Update a `User` and you get a `User`;
update an anonymous struct and you get an anonymous struct. So you do not write
the type name: `User{..alice, age: 31}` is an error.

To change a field inside a nested struct, write `..` with nothing after it:

```nomi-run
struct Address
    street: String
    city:   String
end

struct Person
    name:    String
    address: Address
end

fn main()
    ada = Person{name: "Ada", address: {street: "1 Main", city: "Bath"}}
    dbg {..ada, address: {.., city: "NYC"}}
end
```
<!-- expect
dbg line 13: {..ada, address: {.., city: "NYC"}} = Person{name: "Ada", address: Address{street: "1 Main", city: "NYC"}}
-->

The `..` means the value the field already holds, so this changes the city and
keeps the street. Spelling that value out, `{..ada.address, city: "NYC"}`, is
an error that asks for the `..`. Inside a field's new value, `..` is that
value too: `{..ada, name: String.trim(..), address: {.., city: .. + "!"}}`.

[`Struct.update`](/reference/structs/) does the same as a function call. It
takes the changes as an argument:
`Struct.update(ada, {address: {.., city: "NYC"}})`. Use it when the changes
are computed, or as a step in a pipe.

Anything else you put in a field replaces the whole value. That includes a
`{...}` literal without `..`. So `{..ada, address: {city: "NYC"}}` is an error,
because it builds a new `Address` with no street. The compiler says
`missing field 'street' of Address` and suggests `{.., city: "NYC"}`.

The same `..` works for a map's values and for list fields (`[t, ..]`), as the
[collections chapter](/collections/#spreads) shows.

## Tuples

```nomi-run
fn main()
    pair = ("Ada", 37)
    (name, _) = pair

    dbg pair
    dbg name
    dbg pair.second
end
```
<!-- expect
dbg line 5: pair = ("Ada", 37)
dbg line 6: name = "Ada"
dbg line 7: pair.second = 37
-->

A tuple groups a fixed number of values by position instead of by name. You
write one as `(a, b)`, with at least two values, and its type the same way,
`(String, Int)`. One value in parentheses is only grouped. You read an item by its
ordinal, `.first`, `.second` and so on, counting from one as `&1` does, or
take the tuple apart with a pattern like `(name, _)`. A named tuple,
`type Pair (String, Int)`, reads its items the same way.

Tuples suit small groupings used in one place, where the positions are
obvious. When the values travel further, such as through a public function,
give them names with a struct: a `User` with `name` and `score` fields says
more than `(String, Int)`.

## Field accessors

```nomi-run
struct User
    name: String
    age:  Int
end

fn main()
    [User{name: "Alan", age: 41}, User{name: "Ada", age: 36}]
    |> Iter.sort_by(.age)
    |> Iter.map(.name)
    |> Iter.to_list()
    |> dbg
end
```
<!-- expect
dbg line 11:
  [User{name: "Alan", age: 41}, User{name: "Ada", age: 36}]
  |> Iter.sort_by(.age)
  |> Iter.map(.name)
  |> Iter.to_list()
  = ["Ada", "Alan"]
-->

`.name` is short for the lambda `fn user -> user.name`. You can use it wherever a
function is expected and the argument's type is already known. In a pipe, the
type comes from the collection before it.

`.address.city` reads a field of a field. A tuple item works too:
`Iter.map(pairs, .second)`. An accessor is only ever passed to a function. To read
a field of a value you have, write `user.name`.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- Chains mix fields and items: `.second.first` reads the first item of the
  second.
- The record type must be known where the accessor stands. Where only a
  later argument would give it, annotate a binding:
  `f: (User) -> String = .name`.
- An accessor is not a pipe stage: `user |> .name` is an error. Write
  `user.name`.
- A field that holds a function is returned, not called.

Spec: [Field Accessors](/spec/functions-and-closures/#field-accessors).

</div>

## Distinct types

```nomi-run
type Id Int
type Email String

fn raw(Id(n)) -> Int
    n
end

fn main()
    id = Id(42)
    dbg id

    // To read the inner value, destructure it:
    Id(n) = id
    dbg n

    dbg raw(id)
end
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
with the type's name in front:

```nomi-run
type Pair (Int, String)
type Path Vector<Int>
type Scores Map<String, Int>

fn main()
    p = Pair(1, "one")
    Pair(n, word) = p
    dbg p
    dbg n
    dbg word

    dbg Path[3, 1, 4]
    dbg Scores{"ada" => 3}

    home: Path = [0, 0]
    dbg home
end
```
<!-- expect
dbg line 8: p = Pair(1, "one")
dbg line 9: n = 1
dbg line 10: word = "one"
dbg line 12: Path[3, 1, 4] = Path(Vector[3, 1, 4])
dbg line 13: Scores{"ada" => 3} = Scores({"ada" => 3})
dbg line 16: home = Path(Vector[0, 0])
-->

Where a `Path` is expected, as on the annotated `home`, the bare `[0, 0]`
builds one, as `{x: 1}` builds the struct its place expects.

`Pair(1, "one")` builds a `Pair` from its two values, and the pattern
`Pair(n, word)` takes it apart again. With one argument, `Pair` takes the
whole tuple instead, `Pair((1, "one"))`, as any distinct type takes the one
value it wraps. A `Path` or `Scores` prints in its call form, the type's
name around the collection it wraps.

A type that belongs under another can take a dotted name:
`type Civil.Days Int` declares `Civil.Days`, written in full wherever it is
used, as in `Civil.Days(3)`. `Civil` must be a type in scope.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- The wrapped value must have the wrapped type: `Id("42")` is
  `Id wraps Int, so Id(...) takes an Int; got String`.
- Destructuring reads one level. For `type A Int` and `type B A`,
  `B(a) = b` binds an `A`.
- A distinct type can't contain itself: `type L List<L>` is an error. A
  recursive type is a struct or an enum.
- A dotted name is a declaration of its own, not an item inside its
  parent. Impls, derives and patterns use the full name.

```nomi
struct Civil end

type Civil.Days Int

fn days_value(Civil.Days(n)) -> Int
    n
end
```

Spec: [Distinct Types](/spec/types/#distinct-types),
[Types](/spec/types/), whose opening covers dotted names.

</div>

## Validated types

The `type Email String` declared in [Distinct types](#distinct-types)
accepts any `String`, even one with no `@`. A `when` rule after the wrapped
type says which values the type accepts, and every value is checked before it
exists.

There are two ways to make one. A literal, as in `Email("ada@example.com")`,
is checked when the program compiles, so a bad one never runs. A value known
only when the program runs goes through `Email.try_from`, which runs the rule
and returns an `Option<Email>`: `Some` when the value passes, `None` when it
doesn't.

```nomi-run
type Email String when fn s -> String.contains(s, "@")

fn main()
    dbg Email("ada@example.com")
    dbg Email.try_from("ada")
end
```
<!-- expect
dbg line 4: Email("ada@example.com") = Email("ada@example.com")
dbg line 5: Email.try_from("ada") = None
-->

`try_from` is a function of the prelude's `TryFrom` interface, which the rule
implements for the type.
[Interfaces & Dispatch](/interfaces-and-dispatch/) covers interfaces.

Spec: [Validated types](/spec/types/#validated-types).

## Subtypes

An `Email` with a rule is still its own type, so it doesn't pass where a
`String` is expected. Sometimes you want the check without a new type. A port
number is an `Int` from 1 to 65535, and code that takes one should still be
able to print it or compare it as an `Int`. A `subtype` is the values of
another type that a `when` rule allows, and its values remain that type.

```nomi-run
subtype Port Int when fn p -> p >= 1 and p <= 65535

fn serve(port: Port) -> String
    "listening on ${port}"
end

fn scale(x: Float, by: NonNegativeFloat) -> Float
    x * by
end

fn main()
    dbg serve(8080)

    // A computed Int is checked when the program runs.
    base = 8000
    dbg Port.try_from(base + 80)
    dbg Port.try_from(base * 10)

    // A PositiveFloat is a NonNegativeFloat and a NonZeroFloat.
    half: PositiveFloat = 0.5
    dbg scale(10.0, half)
    dbg Float.floor(half)
end
```
<!-- expect
dbg line 12: serve(8080) = "listening on 8080"
dbg line 16: Port.try_from(base + 80) = Some(8080)
dbg line 17: Port.try_from(base * 10) = None
dbg line 21: scale(10.0, half) = 5.0
dbg line 22: Float.floor(half) = 0.0
-->

A `Port` is stored and used as an `Int`, so `serve` can interpolate it, and
a `Port` passes wherever an `Int` is expected. A subtype may narrow other
subtypes, several joined by `and`: the prelude's `PositiveFloat` is
`NonNegativeFloat and NonZeroFloat` over `Float`, so `half` goes to `scale`
and to `Float.floor` as it is. Nothing converts down or sideways.

A literal where a `Port` is expected, as in `serve(8080)`, is checked while
the program compiles, so a bad one never runs:

```nomi-run error
subtype Port Int when fn p -> p >= 1 and p <= 65535

fn serve(port: Port) -> String
    "listening on ${port}"
end

fn main()
    dbg serve(0)
end
```
<!-- expect
main.nomi:8:15: 0 is not a Port: `fn p -> p >= 1 and p <= 65535` is false
main.nomi:8:15: help: a literal is checked when the program is compiled; a value known only when it runs goes through `Port.try_from(...)`, an `Option<Port>`
-->

Any other `Int` becomes a `Port` through `Port.try_from`, as a validated
type's value does. The prelude also has `NonZeroInt`,
`NonNegativeInt` and `PositiveInt`, their Float and Decimal counterparts, and
`NonEmptyString`. Std functions take them where a value would trap or be
ignored, and return them where a result always satisfies one: `Int.pow`'s
exponent and the counts of `String.repeat` and `Iter.take` are
`NonNegativeInt`s, and so is `List.length`, so `Int.pow(2, List.length(xs))` needs no `try_from`.
`Iter.chunks` takes a `PositiveInt`, so `Iter.chunks(xs, 0)` does not
compile.

A subtype can take type parameters. The prelude's `NonEmptyList<T>` is a
List with at least one element, so its first element is a `T`, not an
`Option<T>`:

```nomi-run
fn greet(names: NonEmptyList<String>) -> String
    "Hello, ${NonEmptyList.first(names)}"
end

fn main()
    dbg greet(["Ada", "Bo"])

    typed = String.words("Cy Di")
    dbg NonEmptyList.try_from(typed) |> Option.map(greet)

    // Below this check, typed is a NonEmptyList.
    if typed == [] then return
    dbg greet(typed)
end
```
<!-- expect
dbg line 6: greet(["Ada", "Bo"]) = "Hello, Ada"
dbg line 9: NonEmptyList.try_from(typed) |> Option.map(greet) = Some("Hello, Cy")
dbg line 13: greet(typed) = "Hello, Cy"
-->

A list literal with an element is a `NonEmptyList` whatever its elements
are, so `greet([name])` needs no check, and the same holds for vector, set and map literals and their non-empty subtypes.
A binding with no type written keeps that: after `names = [name]`, and for `once names = ["Ada"]`, `greet(names)` needs no check either.

A check narrows a binding to the std subtype it proves, where it holds:
past `if typed == [] then return`, `typed` is a `NonEmptyList`, and in
`if n > 0 then ...`, `n` is a `PositiveInt`. So a value the code checks
already needs no `try_from`. The compiler follows each number's range
through comparisons with constants and arithmetic, so after `if size > 1`,
`size - 1` is a `PositiveInt` too. Division leans on this: `/` and `%`
divide by a `NonZeroInt`, so `total / count` compiles once `count` is
checked (`if count == 0 then return 0`) or typed, and never divides by zero.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- The rule is a lambda or the name of a function returning `Bool`. It must
  be pure: a rule that prints or reads a file is an error.
- `Port.unchecked(n)` skips the rule. Only the file declaring `Port` may call
  it, where the rule is known to hold.
- A `struct` takes a rule as the last line of its body, after every field:
  `when fn s -> s.low <= s.high`.
- A subtype can't be `opaque`. It may take type parameters, which its
  parents name, bounded with `where` before `when`:
  `subtype Sorted<T> List<T> where T: Comparable when ...`.
- A subtype must narrow: one with no rule of its own needs two parents,
  as `subtype Strict NonNegativeInt and NonZeroInt`, and a value of every
  parent converts into it.
- A `type` over a subtype is a validated type: `type UserId PositiveInt`
  has `UserId.try_from`, and converts to nothing.
- `NonNegativeInt and NonZeroInt` is a type wherever one is written, the
  values that are both.
- Where a `Port` meets an `Int` with no type written, as the two branches
  of an `if` or two elements of a list, the value is an `Int`. A literal
  beside a std numeric subtype that satisfies its rule is that subtype:
  with `n` a `NonNegativeInt`, `if c then n else 0` is one.
- Arithmetic gives the std numeric subtype its result's range proves:
  with `n` a `NonNegativeInt`, `n + 1` is a `PositiveInt`, `n * n` a
  `NonNegativeInt`, and `n - 1` an `Int`. A value whose range proves a
  subtype passes where one is expected, std::int's `Int64` and a rule of
  literal bounds over `Int` included. No range relates two bindings:
  `i < n` says nothing of `i`.

Spec: [Subtypes](/spec/types/#subtypes).

</div>

## Bare types

```nomi-run
type Expired
type Online

fn main()
    // A bare type's value is its name, with no parentheses or fields.
    state = Expired
    dbg state
    dbg Online
end
```
<!-- expect
dbg line 7: state = Expired
dbg line 8: Online = Online
-->

A `type` with nothing after its name is a bare type. It is a tag with no data,
and it takes no memory. You write its value as just its name.

Bare types are useful as marker values, and as data-free variants of an enum
(see [Embedding existing types](#embedding-existing-types)).

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A bare type has no call form: `Expired()` is
  `Expired is a zero-sized type and takes no arguments; write Expired`.
- It can implement interfaces and be embedded in an enum like any type.
- It can't be `opaque`, since it has no representation to hide.

Spec: [Distinct Types](/spec/types/#distinct-types).

</div>

## Enums

```nomi-run
enum Direction
    North
    South
    East
    West
end

enum Shape
    Circle Float
    Rectangle {width: Float, height: Float}
end

fn main()
    dbg Direction.North

    c = Shape.Circle(3.0)
    dbg c

    r = Shape.Rectangle{width: 4.0, height: 5.0}
    dbg r
end
```
<!-- expect
dbg line 14: Direction.North = North
dbg line 17: c = Circle(3.0)
dbg line 20: r = Rectangle{width: 4.0, height: 5.0}
-->

An enum value is exactly one of a fixed set of variants. A variant can hold
nothing (`North`), one value (`Circle Float`), or named fields
(`Rectangle {width: Float, height: Float}`).

### Dot-leading shorthand

```nomi-run
enum Direction
    North
    South
    East
    West
end

fn describe(d: Direction) -> String
    // `d` is a Direction, so each arm's dot-leading pattern
    // unambiguously matches one of Direction's variants.
    case d
        .North -> "up"
        .South -> "down"
        .East  -> "right"
        .West  -> "left"
    end
end

fn main()
    // `describe` expects a Direction, so `.North` means `Direction.North`.
    dbg describe(.North)
    dbg describe(.West)
end
```
<!-- expect
dbg line 21: describe(.North) = "up"
dbg line 22: describe(.West) = "left"
-->

`Direction.North` always works. When the compiler already knows the code needs
a `Direction`, you can write just `.North`. A variant with fields drops its
enum the same way: `.Rectangle{width: 4.0, height: 5.0}` is
`Shape.Rectangle{...}`, as a struct's `{...}` drops the struct's name.

That works in the arms of a `case` on a `Direction`, as above. `case` is
covered in [Pattern Matching](/pattern-matching/). It also works for an
argument (`describe(.North)`), a return value, a list element
(`walk: List<Direction> = [.North, .East]`) and an annotated binding
(`d: Direction = .North`).

With no annotation, name the enum on the first element and the rest follow
it: `walk = [Direction.North, .East]`.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A variant is never bare, even in its own file: `North` alone is an error
  that asks for `Direction.North`. The prelude's `Some`, `None`, `Ok`,
  `Err`, `True` and `False` are the exceptions.
- `[.North, Direction.East]` is refused: the shorthand looks at earlier
  elements only. Tuple items don't help each other either.
- In a comparison one side must name the enum: `.Red == .Green` is an
  error.
- A struct variant has no positional form: `Shape.Rectangle(4.0, 5.0)` is
  an error. `Shape.Rectangle({width: 4.0, height: 5.0})` works.
- Only a struct variant's fields take defaults. `Circle Float = 3.14` is an
  error.

Spec: [Variant Resolution](/spec/types/#variant-resolution),
[Construction and Typing](/spec/types/#construction-and-typing).

</div>

## Type-owned functions

```nomi-run
struct User
    first: String
    last:  String
end

impl User
    fn full_name(user: User) -> String
        "${user.first} ${user.last}"
    end

    fn rename(user: User, first: String) -> User
        {..user, first:}
    end
end

fn main()
    alice = User{first: "Ada", last: "Lovelace"}
    dbg User.full_name(alice)

    renamed = User.rename(alice, "Augusta")
    dbg User.full_name(renamed)
end
```
<!-- expect
dbg line 18: User.full_name(alice) = "Ada Lovelace"
dbg line 21: User.full_name(renamed) = "Augusta Lovelace"
-->

An `impl User` block holds functions that belong to `User`. You call
them with the type's name in front: `User.full_name(user)`. Nomi has no
`value.method()` syntax, so the type is always visible where you call. The
stdlib works the same way: `String.trim(text)`, `List.concat(xs, ys)`.

Put functions here that build, read, check, convert or change values of the
type. An `impl` block works for any type you declare: structs, enums, distinct
types, and generic types such as `impl Box<T>`.

## Embedding existing types

```nomi-run
struct Click
    x: Int
    y: Int
end

struct KeyDown
    key: String
end

type FocusLost // a bare type: no data

enum Event
    embeds Click
    embeds KeyDown
    embeds FocusLost
end

fn main()
    // Each event is built as its own type, with no `Event.Click{...}` wrapping.
    // Structs use `Name{...}`; the bare type is just its name.
    events: List<Event> = [Click{x: 10, y: 20}, KeyDown{key: "Enter"}, FocusLost]
    dbg events
end
```
<!-- expect
dbg line 22: events = [Click{x: 10, y: 20}, KeyDown{key: "Enter"}, FocusLost]
-->

`embeds Click` makes the existing type `Click` a variant of `Event`. You do not
repeat its fields in the enum, and `Click` is still a type of its own. You can
embed a struct, a distinct type or a bare type.

A `Click` value can go anywhere an `Event` is expected, with no wrapping: a
list element, an argument or a return value. It does not work the other way.
An `Event` might hold a different variant, so to pass it to a function that
takes a `Click`, match it first. A `case` takes an embedded variant apart like
any other, with an arm such as `.Click{x:, y:} -> ...`. See
[Pattern Matching](/pattern-matching/).

Matching also narrows. In an arm whose pattern matches one embedded variant,
the matched name has that variant's type, so `.Click{} -> handle_click(e)`
passes `e` to a function that takes a `Click`.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- The widening reaches inside other types: a `List<Click>` goes where a
  `List<Event>` is expected, and an `Option<Click>` where an `Option<Event>` is.
- Where the two meet with no direction, such as the branches of an `if`,
  the result is the enum.
- Narrowing needs a plain name: `case e` narrows `e`, `case load()` and
  `case w.event` narrow nothing. An `or` pattern and a binding with
  `else` narrow nothing.
- Only a struct or a distinct type can be embedded. An enum is carried in
  a payload variant instead: `Shape Shape`.

Spec: [Embedded Types](/spec/types/#embedded-types),
[Type Narrowing](/spec/patterns/#type-narrowing).

</div>

Where another language would use a class hierarchy, Nomi uses an enum like
this one, with one `embeds` per kind of event.

## Type aliases

```nomi-run
typealias Rule (String) -> Option<String>

fn problems(name: String, rules: List<Rule>) -> List<String>
    rules
    |> Iter.map(fn rule -> rule(name))
    |> Option.values()
    |> Iter.to_list()
end

fn too_short(name: String) -> Option<String>
    if String.length(name) < 3 then Some("too short") else None
end

fn has_space(name: String) -> Option<String>
    if " " in name then Some("has a space") else None
end

fn main()
    rules: List<Rule> = [too_short, has_space]

    dbg problems("al", rules)
    dbg problems("ada lovelace", rules)
    dbg problems("ada", rules)
end
```
<!-- expect
dbg line 21: problems("al", rules) = ["too short"]
dbg line 22: problems("ada lovelace", rules) = ["has a space"]
dbg line 23: problems("ada", rules) = []
-->

A `typealias` is another name for an existing type. `Rule` and
`(String) -> Option<String>` are the same type, and you can use either one
anywhere. There is no wrapping and no conversion: `too_short` is an ordinary
function, and the list holds it as a `Rule`. `Option.values` keeps the `Some`
returns and drops the `None`s.

An alias shortens signatures: `problems` takes a `List<Rule>` instead of a
`List<(String) -> Option<String>>`. Reach for an alias to shorten a long
anonymous shape like this one: a function type, an anonymous struct or a long
tuple.

An alias may take type parameters, as in `typealias Pair<T> (T, T)`, and may
name several interfaces joined by `and`. It has no rule; that is a `subtype`.
You cannot write an `impl` for it, and you call its target's functions by the
target's name: for `typealias Names List<String>`, write `List.first`, not
`Names.first`.

Use `type` when you want the compiler to tell two things apart. A `UserId` and
an `OrderId` may both hold an `Int`, but you should not pass one where the
other belongs. When the thing deserves a name of its own, declare a struct or
a `type` rather than an alias.

## See it in action

<div class="tour-examples">
<a href="/examples/queen-attack/"><span class="tour-example-name">Queen Attack</span><span class="tour-example-what">Can two queens on a board attack?</span></a>
<a href="/examples/resistor-color/"><span class="tour-example-name">Resistor Color</span><span class="tour-example-what">Read a band's color as a digit.</span></a>
<a href="/examples/complex-numbers/"><span class="tour-example-name">Complex Numbers</span><span class="tour-example-what">A number type with its own arithmetic.</span></a>
</div>

<a class="tour-examples-all" href="/examples/?chapter=structs-enums-distinct">Every example that uses Structs, Enums, Distinct Types</a>

Next: [Pattern Matching, Option & Result](/pattern-matching/), which shows how
`case` takes apart every value you can now build.
