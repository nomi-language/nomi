---
title: "Interfaces & Dispatch"
description: "Declare an interface, implement it for your types, and call its functions. Defaults, Debug and Display, generic bounds, operators and derives."
---

Nomi has no `value.method()` calls. A function about a type is called through
the type's name, as in `User.rename(user)` or
[`List.concat(xs, ys)`](/reference/lists/#listconcat). A function that
belongs to no type is called through its file's name, as in
[`io.print(value)`](/reference/io/#ioprint).

The [Structs, Enums, Distinct Types](/structs-enums-distinct/#type-owned-functions)
chapter showed `impl Type { ... }` blocks, which add functions to a type. This
chapter covers interfaces. An `interface` lists functions that a type must
provide. A type provides them in an `impl Interface for Type { ... }` block.

You call these functions through the type's name, as in `User.to_json(user)`,
or through the interface's name, as in
[`Display.to_string(value)`](/reference/display/#displayto_string).

## Defining and implementing an interface

```nomi-run
import std/io

interface Speech {
    fn speak(animal: self): String
}

struct Dog {
    name: String
}

impl Speech for Dog {
    fn speak(d: Dog): String {
        "${d.name} says woof"
    }
}

struct Cat {
    name: String
}

impl Speech for Cat {
    fn speak(c: Cat): String {
        "${c.name} says meow"
    }
}

fn main() {
    io.print(Dog.speak(Dog{name: "Rex"}))
    io.print(Cat.speak(Cat{name: "Whiskers"}))
}

```
<!-- expect
Rex says woof
Whiskers says meow
-->

In the interface, `self` is a type. It stands for whichever type implements
the interface: `Dog` in one impl, `Cat` in the other. It is not a value or a
hidden parameter.

Each impl writes its own type in place of `self`. The parameter's name doesn't
have to match: the interface says `animal`, and the impls say `d` and `c`.

## Qualified call shapes

You call an interface function through a name and a dot, never bare as
`speak(dog)`. Usually you write the type's name: `Dog.speak(dog)`. You can
also write the interface's name: `Speech.speak(dog)`. Both call the `Dog`
impl:

```nomi-run
import std/io

interface Speech {
    fn speak(animal: self): String
}

struct Dog {
    name: String
}

impl Speech for Dog {
    fn speak(d: Dog): String {
        "${d.name} says woof"
    }
}

fn main() {
    rex = Dog{name: "Rex"}

    // Type-qualified — names the implementing type.
    io.print(Dog.speak(rex))

    // Interface-qualified — same dispatch through the contract.
    io.print(Speech.speak(rex))

    // Most printing does not need explicit stringification.
    io.print(42)
}

```
<!-- expect
Rex says woof
Rex says woof
42
-->

Use the interface's name when two interfaces have a function with the same
name, or in generic code that knows only the interface (see
[Interface-bounded generics](#interface-bounded-generics)).

To sum up the rule for every call:

- A function declared in an `impl Type` or `impl Interface` block is called
  through its type or interface:
  [`Iter.map(xs, f)`](/reference/iter/#itermap), `Dog.speak(dog)`,
  `Speech.speak(dog)`.
- A function declared at the top of a file is called through the file's name
  once you import it: [`io.print(x)`](/reference/io/#ioprint).
- A bare name such as `speak` means a function or binding in the current
  file. It never finds an interface function by itself.

## Default Functions

A function in an interface can have a body. That body is the default: every
type that implements the interface gets it, and the impl doesn't write it.
An impl must write every function that has no body.

A type can't replace a default unless the interface marks it `open`:

```nomi-run
import std/io

interface Formatted {
    fn label(value: self): String

    // Final default — overriding it is a compile error.
    fn shout(value: self): String {
        "${label(value)}!"
    }

    // Open default — implementors MAY override.
    open fn brief(value: self): String {
        label(value)
    }
}

struct User {
    name: String
}

impl Formatted for User {
    fn label(u: User): String {
        u.name
    }

    // `brief` is `open`, so this override is allowed.
    fn brief(u: User): String {
        "user:${u.name}"
    }
}

fn main() {
    alice = User{name: "Alice"}
    io.print(Formatted.label(alice))
    io.print(Formatted.shout(alice))
    io.print(Formatted.brief(alice))
}

```
<!-- expect
Alice
Alice!
user:Alice
-->

A default without `open` behaves the same for every type, so code that calls
it knows what it does. Interface functions don't take `pub`. They are visible
wherever the interface is.

Inside a default, a bare call such as `label(value)` calls another function of
the same interface. Inside an `impl` block, you can likewise call the block's
other functions by their bare names. Everywhere else, write the qualifier.

### Defaults that need another interface

A default runs for every type that implements the interface. So its body can
only use what all of those types have: the interface's own functions, and
`Debug`.

To use another interface, add `where self: Display` (or whichever interface)
to that default. A type that implements `Display` gets the default. A type
that doesn't must write the function itself. An `impl` that does neither is
an error.

```nomi-run
import std/io

interface Scale {
    fn unit(value: self): String

    fn format(value: self): String where self: Display {
        "${value} ${unit(value)}"
    }
}

type Grams Int

impl Display for Grams {
    fn to_string(Grams(n)): String {
        "${n}"
    }
}

impl Scale for Grams {
    fn unit(_g: Grams): String {
        "g"
    }
}

type Secret Int

// No Display, so Secret writes `format` itself.
impl Scale for Secret {
    fn unit(_s: Secret): String {
        "?"
    }

    fn format(_s: Secret): String {
        "hidden"
    }
}

fn main() {
    io.print(Scale.format(Grams(250)))
    io.print(Scale.format(Secret(7)))
}

```
<!-- expect
250 g
hidden
-->

Without the `where self` clause, `"${value}"` in that default is an error:
`` `value` is a `self`, and Scale does not require Display of its implementors ``.

### Defaults over distinct types

A distinct type wraps another type. Every distinct type implements
`Distinct<T>`, where `T` is the type it wraps: `Celsius` below wraps
`Decimal`, so it is a `Distinct<Decimal>`.

A default with `where self: Distinct<T>` can unwrap `self` with the pattern
`self(n)`, and wrap a value with `self(x)`. This lets several wrapper types
share one function body. For `Celsius`, `self(n)` unwraps a `Celsius` and
`self(x)` makes one.

```nomi-run
import std/io

type Celsius Decimal
type Fahrenheit Decimal

interface Temperature {
    fn unit(t: self): String

    fn label(self(n) as t): String where self: Distinct<Decimal> {
        "${Decimal.round(n, 1, .HalfUp)} ${Temperature.unit(t)}"
    }

    fn scale(self(n), by: Decimal): self where self: Distinct<Decimal> {
        self(n * by)
    }
}

impl Temperature for Celsius {
    fn unit(_t: Celsius): String {
        "C"
    }
}

impl Temperature for Fahrenheit {
    fn unit(_t: Fahrenheit): String {
        "F"
    }
}

fn main() {
    io.print(Temperature.label(Celsius(21.456d)))
    io.print(Temperature.label(Fahrenheit(70d)))
    io.inspect(Celsius.scale(Celsius(10d), 2d))
}
```
<!-- expect
21.5 C
70.0 F
Celsius(20d)
-->

`self(n)` also works in a `case` arm and in a binding (`self(m) = other`).
`self` on its own is the wrapping function, which you can pass around:
`Iter.map(ns, self)`.

A type that isn't a distinct type over `Decimal` must write these functions
itself, as with any `where self` bound. So must an opaque type whose impl is
in another file, because the default would unwrap it there, where its
contents are hidden.

## Universal `Debug`

[`Debug`](/reference/debug/) is the only interface every type has
automatically. You don't write an `impl` or a `derive` for it. Every value,
including those of types you declare, can be shown with `dbg`:

```nomi-run
struct Point {
    x: Int
    y: Int
}

enum Status {
    Active
    Pending Int
}

type Email String

fn main() {
    dbg Point{x: 3, y: 4}
    dbg Status.Pending(7)
    dbg Email("a@b.com")
}

```
<!-- expect
dbg line 14: Point{x: 3, y: 4} = Point{x: 3, y: 4}
dbg line 15: Status.Pending(7) = Pending(7)
dbg line 16: Email("a@b.com") = Email("a@b.com")
-->

That's why `dbg` has worked on everything in the tour so far.

To show a type differently, write `impl Debug for Type { ... }`. Your impl is
used in place of the automatic one.

## Opting in to `Display`

[`io.print`](/reference/io/#ioprint) needs a different interface,
[`Display`](/reference/display/). `Display` is the text you show to users, and
your types don't get it automatically. Write an `impl Display for Type { ... }`
block with a `to_string` function:

```nomi-run
import std/io

struct User {
    name: String
    age: Int
}

impl Display for User {
    fn to_string(u: User): String {
        "${u.name} (${u.age})"
    }
}

fn main() {
    io.print(User{name: "Alice", age: 30})
}

```
<!-- expect
Alice (30)
-->

The function is named `to_string`, as in the `Display` interface, and takes a
`User`.

## Interface-bounded generics

A generic function can require its type parameter to implement an interface.
Each call is checked against that requirement:

```nomi-run
fn first_two_sorted<T>(xs: List<T>): List<T> where T: Comparable {
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

The standard library makes [`Int`](/reference/int/) and
[`String`](/reference/strings/#type-string) implement
[`Comparable`](/reference/comparable/), so both calls are allowed.

[Generics](/generics/) covers bounds in full. Some examples:
`where T: Comparable`, `where T: Display and Debug`,
`where T: Steppable<S>`.

A function in an interface can have its own `where` clause, written before
its body if it has one. If it needs its own type parameter, declare it in
`<...>` after the function's name:

```nomi ignore
interface Ranked<T> {
    fn item(value: self): T

    fn prefer<K>(_value: self, lhs: K, rhs: K): Bool where K: Comparable {
        case Comparable.compare(lhs, rhs) {
            .Less -> False
            _ -> True
        }
    }
}
```

`T` comes from `Ranked<T>`. `K` comes from `prefer<K>`. Naming a type
parameter that neither of them declares is an error.

## Operators that dispatch

Some operators call interface functions:

- `==` uses [`Equatable`](/reference/equatable/), when the type implements it.
- `<`, `>` and the other comparisons use [`Comparable`](/reference/comparable/).
- `+`, `-`, `*` and `/` use [`Add`](/reference/add/),
  [`Subtract`](/reference/subtract/), [`Multiply`](/reference/multiply/) and
  [`Divide`](/reference/divide/).

The arithmetic interfaces take two type parameters: the type of the right-hand
side, and the type of the result. So the same interface covers adding two
scores, and adding a number of days to a day:

```nomi-run
type Score Int

fn score_number(score: Score): Int {
    Score(n) = score
    n
}

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

impl Add<Score, Score> for Score {
    fn add(lhs: Score, rhs: Score): Score {
        Score(score_number(lhs) + score_number(rhs))
    }
}

impl Add<Days, Day> for Day {
    fn add(lhs: Day, rhs: Days): Day {
        Day(day_number(lhs) + days_number(rhs))
    }
}

fn main() {
    score = Score(2) + Score(3)
    day = Day(10) + Days(4)

    dbg score_number(score)
    dbg day_number(day)
}

```
<!-- expect
dbg line 38: score_number(score) = 5
dbg line 39: day_number(day) = 14
-->

## `derive` entries — structural shortcuts

`derive Interface for Type` asks Nomi to write the impl for you. The impl it
writes works field by field, or variant by variant. You can derive:

- `Display` and `Debug`, to turn values into text.
- `Equatable` and `Hashable`, for `==` and for use as `Map` keys and `Set`
  elements.
- `Comparable`, for `<`, `>` and `Iter.sort`.
- `ToJson` and `FromJson`, to convert to and from JSON.
- `Variants`, to list every variant of an enum whose variants carry no data.

You will often write `Display` by hand, since text for users rarely matches
the fields one for one.

Some derives take options after `with`. The JSON derives use them to rename
fields:

```nomi-run
import {
    std/json.{FromJson, Json, ToJson}
    std/json.Json.Case.Camel
}

struct User {
    first_name: String
    age: Int
}

derive ToJson for User with ToJson.Options{rename_all: Camel}
derive FromJson for User with FromJson.Options{rename_all: Camel}

fn main(): Result<Unit, Json.ShapeError> {
    user = User{first_name: "Ada", age: 36}
    shape = User.to_json(user)

    dbg Json.encode(shape)

    user = try User.from_json(shape)

    user
    |> ToJson.to_json()
    |> Json.encode()
    |> dbg

    Ok(Unit)
}

```
<!-- expect
dbg line 18: Json.encode(shape) = "{\"firstName\":\"Ada\",\"age\":36}"
dbg line 25:
  user
  |> ToJson.to_json()
  |> Json.encode()
  = "{\"firstName\":\"Ada\",\"age\":36}"
-->

### Choosing a dispatch surface

An interface function can be called through the type or through the
interface. Both run the same code, so pick the one that reads best.

For a `User` declared in `users.nomi`, you would usually write the type:

```nomi
encoded = User.to_json(user)
user = try User.from_json(encoded)
```

The interface's name helps when the type's name would be ambiguous, or in
generic code that has a bound instead of a type. `from_json` has no `User`
argument to go by, so this form names the type in `<...>`:

```nomi
encoded = ToJson.to_json(user)
user = try FromJson.from_json<User>(encoded)
```

A type annotation on the binding works too:

```nomi
user: User = try FromJson.from_json(encoded)
```

### Structural equality

You can compare any two values of the same type with `==`, with no impl or
derive. They are equal when their fields are equal. In the same way, any value
can be a `Map` key.

```nomi-run
struct Point {
    x: Int
    y: Int
}

fn main() {
    dbg Point{x: 1, y: 2} == Point{x: 1, y: 2}
    dbg Point{x: 1, y: 2} == Point{x: 1, y: 3}
    dbg {Point{x: 1, y: 2} => "home"}
}

```
<!-- expect
dbg line 7: Point{x: 1, y: 2} == Point{x: 1, y: 2} = True
dbg line 8: Point{x: 1, y: 2} == Point{x: 1, y: 3} = False
dbg line 9: {Point{x: 1, y: 2} => "home"} = {Point{x: 1, y: 2} => "home"}
-->

You still sometimes need `derive Equatable` or `derive Hashable`, for one of
two reasons.

The first is generic bounds. A function with `where T: Equatable` accepts only
types that implement `Equatable`. Built-in `==` doesn't count. Without the
derive, this call is a compile error:

```nomi
fn contains<T>(xs: List<T>, needle: T): Bool where T: Equatable {
    Iter.any?(xs, |x| x == needle)
}

contains([Point{x: 1, y: 2}], Point{x: 1, y: 2})
// error: Point does not implement Equatable (required by `where T: Equatable`)
```

Add `derive Equatable for Point` and the call compiles.

The second is custom equality. An `impl Equatable for Type { ... }` block you
write replaces the built-in `==` for that type. That's how `DateTime` compares
by the moment it names: the same moment in two time zones is `==` (see
[Dates and times](/dates-and-times/)).

Ordering is different. There is no built-in `<` for your types. Nomi can't
guess whether a `Point` should order by `x`, by `y`, or by distance from the
origin. So using `<` on your own type without a `Comparable` impl is a compile
error. `derive Comparable` compares the first field, then the next field if
those are equal, and so on:

```nomi-run
struct Money {
    amount: Int
}

derive Comparable for Money

fn main() {
    dbg Money{amount: 100} < Money{amount: 250}

    ms = [Money{amount: 3}, Money{amount: 1}, Money{amount: 2}]
    Iter.sort(ms) |> dbg
}

```
<!-- expect
dbg line 8: Money{amount: 100} < Money{amount: 250} = True
dbg line 11: Iter.sort(ms) = [Money{amount: 1}, Money{amount: 2}, Money{amount: 3}]
-->

### Ranked enums

On an enum, `derive Comparable` orders the variants as they are declared.
Earlier variants sort first. Payloads are compared only between two values of
the same variant:

```nomi-run
enum Severity {
    Info
    Warning Int
    Error Int
}

derive Comparable for Severity

fn main() {
    dbg Severity.Info < Severity.Warning(1)
    dbg Severity.Warning(9) < Severity.Error(1)
    dbg Severity.Warning(1) < Severity.Warning(2)
}

```
<!-- expect
dbg line 10: Severity.Info < Severity.Warning(1) = True
dbg line 11: Severity.Warning(9) < Severity.Error(1) = True
dbg line 12: Severity.Warning(1) < Severity.Warning(2) = True
-->

`Warning(9) < Error(1)` is `True`, because `Warning` comes before `Error`. To
change the order, reorder the variants. This suits severity levels, stages of
a process and priorities.

### Listing an enum's variants

`derive Variants` gives an enum whose variants carry no data a `variants()`
function. It returns every variant in a `List`, in the order they are
declared, which is also the order `derive Comparable` ranks them in:

```nomi-run
enum Coin {
    Penny
    Nickel
    Dime
    Quarter
}

derive Variants for Coin

fn cents(coin: Coin): Int {
    case coin {
        .Penny -> 1
        .Nickel -> 5
        .Dime -> 10
        .Quarter -> 25
    }
}

fn main() {
    dbg Coin.variants()
    dbg Coin.variants() |> Iter.map(cents) |> Iter.to_list()
}

```
<!-- expect
dbg line 20: Coin.variants() = [Penny, Nickel, Dime, Quarter]
dbg line 21: Coin.variants() |> Iter.map(cents) |> Iter.to_list() = [1, 5, 10, 25]
-->

A variant that carries data has no single value to list, so deriving
`Variants` for such an enum is a compile error that names the variant.

### Deriving vs sorting by key

Most of your types shouldn't implement [`Comparable`](/reference/comparable/)
at all. A `User` or an `Order` has no single natural order. A directory sorts
users by name, and a feed sorts orders by date. Choose the order where you
sort, with [`Iter.sort_by`](/reference/iter/#itersort_by) and the field to
sort on, or [`Iter.sort_with`](/reference/iter/#itersort_with) and a function
that compares two values:

```nomi-run
struct User {
    name: String
    age: Int
}

fn main() {
    users = [User{name: "Cara", age: 35}, User{name: "Ann", age: 41}]
    sorted = users |> Iter.sort_by(.name)

    dbg sorted
}

```
<!-- expect
dbg line 10: sorted = [User{name: "Ann", age: 41}, User{name: "Cara", age: 35}]
-->

Use `derive Comparable` only when the type has one obvious order, and the
order of its fields or variants gives it:

- A type that wraps one value, such as `type Score Int` or
  `type Priority Int`.
- An enum whose variants are in rank order, such as `Severity` above.
- A struct like `Version{major, minor, patch}`, where comparing field by field
  is what the order means. The standard library's `Date` and `Time` are like
  this.

On an ordinary struct, deriving `Comparable` picks one order for every sort.
Code comes to depend on it without saying so. Then reordering the struct's
fields changes how every sort behaves. So when the compiler says a type has no
`Comparable` impl, the fix is usually `sort_by`, not the derive.

:::note[Continue or dig deeper]
The next chapter is [Modules & Imports](/modules-and-imports/), which covers
how a program is split across files. The rest of this chapter goes into more
detail on impls.
:::

## Going deeper

### Coherence: at most one impl per (Iface, T)

A type can implement an interface only once in a whole program. Two
`impl Display for User { ... }` blocks anywhere in the program are a compile
error, reported before anything runs.

Two types with the same name in different files are different types. So a
`Display` impl for one file's `Date` doesn't clash with a `Display` impl for
another file's `Date`.

### The orphan rule

Suppose two libraries each wrote `impl Display for Int { … }`. Each would
compile alone, but a program using both would have two impls. The orphan
rule prevents this. An `impl Iface for T { ... }` block must be in the Nomi
module that declares either `Iface` or `T`. Code that declares neither can't
write the impl.

A Nomi module is the directory tree that holds its `nomi.toml` and `go.mod`.
To implement someone else's interface for someone else's type, wrap the type
in your own. Declare `type MyId Int` in your module, and then
`impl Display for MyId { ... }` is allowed, because `MyId` is yours.

Any file can write ordinary functions that take an imported type. But it
can't add a function, field or variant to that type, or an impl the orphan
rule forbids.

### Why not structural field matching?

Why not write `fn greet(v: {name: String})` and accept any struct with a
`name` field? TypeScript, Go and OCaml each allow something like this.

Nomi doesn't. A parameter of type `{x: Int, y: Int}` accepts only values with
exactly those fields. It rejects an anonymous struct with more fields. It also
rejects a named struct, such as a `Point`, even if its fields match.

Two fields with the same name often mean different things. An `id: String`
on a `User`, an `Order` and an `Invoice` is three different kinds of id. A
function that accepted all three by accident would hide mistakes. Nomi gives
you three explicit ways instead:

1. Declare an interface. A function such as `fn name(value: self): String`
   gives every type that implements it the same way to get a name. Each type
   opts in with an `impl` block.
2. Build an anonymous struct at the call, such as
   `distance({x: p.x, y: p.y}, {x: q.x, y: q.y})`. The call shows exactly
   which fields it passes.
3. Declare a type for the purpose and a function that converts to it, such
   as `fn to_coord(p: Point): Coord2D { … }`.

Use an interface when several types should share the behavior for a long
time. Build an anonymous struct when one call needs a different shape.
Declare a type and a conversion when the conversion means something in your
program.

For the same reason, a type implements an interface only through an `impl`
block. In Go, a type implements any interface whose functions it happens to
have. In Nomi, the `impl` block shows that the type means to, and you can
search for it.

Calls are qualified for the same reason too. A bare name could be a local
binding or a function in the file. `Type.fn(value)`, `Iface.fn(value)` and
`file.fn(value)` each say where the function comes from.
