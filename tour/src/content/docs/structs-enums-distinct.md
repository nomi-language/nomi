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

- A field that may be absent has type `Maybe<T>`. A default only spares
  the caller from writing a value: `nickname: Maybe<String> = None` is both.
- A default runs once for each construction that leaves its field out.
- Each field is named once: `User{name: "Al", name: "Bo"}` is an error.
- A struct also has a call form taking an anonymous struct:
  `User({name: "Ada"})`, which a pipe can feed.
- `{...}` stays anonymous where an interface or a type parameter is
  expected, since neither names one struct: `a: Display = {street: "x"}`
  is a type mismatch.

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
  `List<Event>` is expected, and a `Maybe<Click>` where a `Maybe<Event>` is.
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
typealias Rule (String) -> Maybe<String>

fn problems(name: String, rules: List<Rule>) -> List<String>
    rules
    |> Iter.map(fn rule -> rule(name))
    |> Maybe.values()
    |> Iter.to_list()
end

fn too_short(name: String) -> Maybe<String>
    if String.length(name) < 3 then Some("too short") else None
end

fn has_space(name: String) -> Maybe<String>
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
`(String) -> Maybe<String>` are the same type, and you can use either one
anywhere. There is no wrapping and no conversion: `too_short` is an ordinary
function, and the list holds it as a `Rule`. `Maybe.values` keeps the `Some`
returns and drops the `None`s.

An alias shortens signatures: `problems` takes a `List<Rule>` instead of a
`List<(String) -> Maybe<String>>`. Reach for an alias to shorten a long
anonymous shape like this one: a function type, an anonymous struct or a long
tuple. An alias takes no type parameters. You cannot write an `impl` for it,
and you call its target's functions by the target's name (`List.head`, not
`Names.head` for `typealias Names List<String>`).

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

Next: [Pattern Matching, Maybe & Result](/pattern-matching/), which shows how
`case` takes apart every value you can now build.
