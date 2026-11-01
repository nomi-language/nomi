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
    fn speak(animal: self) -> String
}

struct Dog {
    name: String
}

impl Speech for Dog {
    fn speak(d: Dog) -> String {
        "${d.name} says woof"
    }
}

struct Cat {
    name: String
}

impl Speech for Cat {
    fn speak(c: Cat) -> String {
        "${c.name} says meow"
    }
}

fn main() {
    io.print(Dog.speak(#Dog{name: "Rex"}))
    io.print(Cat.speak(#Cat{name: "Whiskers"}))
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
Every other parameter keeps the interface's name, since a caller can pass it by
name. An impl that doesn't use one writes `_ = name` in its body.

## Qualified call shapes

You call an interface function through a name and a dot, never bare as
`speak(dog)`. Usually you write the type's name: `Dog.speak(dog)`. You can
also write the interface's name: `Speech.speak(dog)`. Both call the `Dog`
impl:

```nomi-run
import std/io

interface Speech {
    fn speak(animal: self) -> String
}

struct Dog {
    name: String
}

impl Speech for Dog {
    fn speak(d: Dog) -> String {
        "${d.name} says woof"
    }
}

fn main() {
    rex = #Dog{name: "Rex"}

    // Through the type.
    io.print(Dog.speak(rex))

    // Through the interface: the same impl runs.
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

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- When a type implements two interfaces that both declare `speak`,
  `Dog.speak(rex)` is ambiguous and an error. Write `Speech.speak(rex)` or
  the other interface's name.
- A type's own function can't share a name with a function one of its
  interface impls writes, since Nomi doesn't overload.
- An impl's parameter names must match the interface's, except the one in
  `self`'s place: `fn format(r: R, s: String)` for
  `format(value: self, style: String)` is an error that asks for `style`.
- `Display` and `Debug` name their functions `to_string` and `inspect`, so
  a type implements both with no clash.

Spec: [Function Name Collisions](/spec/interfaces-implementations-and-derive/#function-name-collisions),
[Matching an impl to its interface](/spec/interfaces-implementations-and-derive/#matching-an-impl-to-its-interface).

</div>

## Default functions

A function in an interface can have a body. That body is the default: every
type that implements the interface gets it, and the impl doesn't write it.
An impl must write every function that has no body.

A type can't replace a default unless the interface marks it `open`:

```nomi-run
import std/io

interface Formatted {
    fn label(value: self) -> String

    // No `open`: an impl can't replace this.
    fn shout(value: self) -> String {
        "${label(value)}!"
    }

    // `open`: an impl may replace this.
    open fn brief(value: self) -> String {
        label(value)
    }
}

struct User {
    name: String
}

impl Formatted for User {
    fn label(u: User) -> String {
        u.name
    }

    // `brief` is `open`, so this override is allowed.
    fn brief(u: User) -> String {
        "user:${u.name}"
    }
}

fn main() {
    alice = #User{name: "Alice"}
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

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- Writing a default that is not `open` in an impl is an error:
  `` 'shout' is a final default on interface 'F'; mark it `open` on the interface to allow overriding ``.
- An interface whose functions all have defaults is implemented with an
  empty `impl Formatted for User`, no body needed.
- A default may give its trailing parameters default values. A required
  function may not.
- An operation with no `self` that belongs with the interface goes in an
  inherent `impl Formatted { ... }` block and is called
  `Formatted.name(...)`.

Spec: [Item kinds](/spec/interfaces-implementations-and-derive/#item-kinds),
[Implementations](/spec/interfaces-implementations-and-derive/#implementations).

</div>

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
    fn unit(value: self) -> String

    fn format(value: self) -> String where self: Display {
        "${value} ${unit(value)}"
    }
}

type Grams Int

impl Display for Grams {
    fn to_string(Grams(n)) -> String {
        "${n}"
    }
}

impl Scale for Grams {
    fn unit(_g: Grams) -> String {
        "g"
    }
}

type Secret Int

// No Display, so Secret writes `format` itself.
impl Scale for Secret {
    fn unit(_s: Secret) -> String {
        "?"
    }

    fn format(_s: Secret) -> String {
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
    dbg #Point{x: 3, y: 4}
    dbg Status.Pending(7)
    dbg Email("a@b.com")
}

```
<!-- expect
dbg line 14: #Point{x: 3, y: 4} = #Point{x: 3, y: 4}
dbg line 15: Status.Pending(7) = Pending(7)
dbg line 16: Email("a@b.com") = Email("a@b.com")
-->

That's why `dbg` has worked on everything in the tour so far.

To show a type differently, write `impl Debug for Type { ... }`. Your impl is
used in place of the automatic one.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- An `opaque` type shows only its name, `<opaque Token>`, so `dbg` never
  reveals what the type hides. Its own file can derive or write `Debug` to
  show more.
- A function shows as `<function>`, a lazy `Iter` chain as `<iter>`, a
  task as `<task>` and a channel end as `<channel>`.
- A hand-written `impl Debug` wins over `derive Debug`, which wins over the
  automatic one. `derive Debug` on an ordinary type changes nothing.
- An `impl Display` that interpolates its own argument, `"${value}"`, would
  call itself forever and is a compile error.

Spec: [Display and Debug](/spec/interfaces-implementations-and-derive/#display-and-debug).

</div>

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
    fn to_string(u: User) -> String {
        "${u.name} (${u.age})"
    }
}

fn main() {
    io.print(#User{name: "Alice", age: 30})
}

```
<!-- expect
Alice (30)
-->

The function is named `to_string`, as in the `Display` interface, and takes a
`User`.

## Interface-bounded generics

[Generics](/generics/#bounds-with-where) showed bounds such as
`where T: Comparable`. A bound can name an interface you declare, and it can
mention another type parameter. That links two type parameters without making
them the same type:

```nomi-run
interface StepBy<S> {
    fn step_by(value: self, step: S) -> self
}

type Counter Int

impl StepBy<Int> for Counter {
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
`Int`. Inside `advance`, `T.step_by` calls the impl for whatever type `T` is.

A value often steps by something of a different type: a date steps by a
number of days, not by another date. The standard library's `Date` uses the
same idea for `+`: `date + Days(10)` steps a date by a number of days, as
[Dates & Times](/dates-and-times/) shows.

A function in an interface can have its own `where` clause, written before
its body if it has one. If it needs its own type parameter, declare it in
`<...>` after the function's name:

```nomi ignore
interface Ranked<T> {
    fn item(value: self) -> T

    fn prefer<K>(_value: self, lhs: K, rhs: K) -> Bool where K: Comparable {
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

fn score_number(score: Score) -> Int {
    Score(n) = score
    n
}

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

impl Add<Score, Score> for Score {
    fn add(lhs: Score, rhs: Score) -> Score {
        Score(score_number(lhs) + score_number(rhs))
    }
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
    score = Score(2) + Score(3)
    day = Day(10) + Days(4)

    dbg score_number(score)
    dbg day_number(day)
    dbg plus(Day(10), Days(4))
}

```
<!-- expect
dbg line 42: score_number(score) = 5
dbg line 43: day_number(day) = 14
dbg line 44: plus(Day(10), Days(4)) = Day(14)
-->

A generic function can use an operator through its interface, as `plus` does.
From the arguments, `L` is `Day` and `R` is `Days`. The only matching impl is
`impl Add<Days, Day> for Day`, so Nomi works out that `Out` is `Day`. The call
doesn't have to say so.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- The bitwise operators dispatch the same way: `&&&` through `BitwiseAnd`,
  `|||` through `BitwiseOr`, and so on.
- `%` has no interface. It works on `Int` only, and a type of your own
  can't implement it.
- `==` uses a hand-written `Equatable` when there is one and compares
  structurally otherwise. `<` and the other comparisons need `Comparable`.
- A type with a `+` over its own values can implement `Summable`, whose
  `empty_sum()` is the sum of nothing. `Iter.sum` needs both.
- One type may implement an operator several times, told apart by the
  right-hand type: `Instant - Duration` is an `Instant` and
  `Instant - Instant` a `Duration`.

Spec: [Operators through interfaces](/spec/expressions/#operators-through-interfaces),
[Bitwise operators](/spec/expressions/#bitwise-operators).

</div>

## Deriving impls

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
fields, as [JSON & Dynamic](/json-and-dynamic/#your-own-types-as-json)
shows.

### Structural equality

Values of your own types compare with `==` with no impl or derive: they are
equal when their fields are equal, and they can be `Map` keys. Function values
are the exception; they have no equality.

```nomi-run
struct Point {
    x: Int
    y: Int
}

fn main() {
    dbg #Point{x: 1, y: 2} == #Point{x: 1, y: 2}
    dbg #Point{x: 1, y: 2} == #Point{x: 1, y: 3}
    dbg %{#Point{x: 1, y: 2} => "home"}
}

```
<!-- expect
dbg line 7: #Point{x: 1, y: 2} == #Point{x: 1, y: 2} = True
dbg line 8: #Point{x: 1, y: 2} == #Point{x: 1, y: 3} = False
dbg line 9: %{#Point{x: 1, y: 2} => "home"} = %{#Point{x: 1, y: 2} => "home"}
-->

You still sometimes need `derive Equatable` or `derive Hashable`, for one of
two reasons.

The first is generic bounds. A function with `where T: Equatable` accepts only
types that implement `Equatable`; built-in `==` doesn't count. `List.contains`
is one, so `List.contains([#Point{x: 1, y: 2}], #Point{x: 1, y: 2})` is the
error `` Point does not implement Equatable (required by `where T: Equatable`) ``
until you add `derive Equatable for Point`.

The second is custom equality. An `impl Equatable for Type { ... }` block you
write replaces the built-in `==` for that type. That's how `DateTime` compares
by the moment it names: the same moment in two time zones is `==` (see
[Dates and times](/dates-and-times/)).

### Ordering

There is no built-in `<` for your types. Nomi can't guess whether a `Point`
should order by `x`, by `y`, or by distance from the origin, so `<` on your
own type without a `Comparable` impl is a compile error. `derive Comparable`
compares the first field, then the next field if those are equal, and so on:

```nomi-run
struct Version {
    major: Int
    minor: Int
}

derive Comparable for Version

fn main() {
    dbg #Version{major: 1, minor: 9} < #Version{major: 1, minor: 10}
    dbg #Version{major: 2, minor: 0} < #Version{major: 1, minor: 10}
}

```
<!-- expect
dbg line 9: #Version{major: 1, minor: 9} < #Version{major: 1, minor: 10} = True
dbg line 10: #Version{major: 2, minor: 0} < #Version{major: 1, minor: 10} = False
-->

On an enum, `derive Comparable` ranks the variants in the order they are
declared, and compares payloads only between two values of the same variant.
That suits severity levels, stages of a process and priorities.

Most of your types shouldn't implement [`Comparable`](/reference/comparable/)
at all. A `User` or an `Order` has no single natural order, and a derived one
changes whenever someone reorders the fields. Choose the order where you sort,
with [`Iter.sort_by`](/reference/iter/#itersort_by) and the field to sort on:

```nomi-run
struct User {
    name: String
    age: Int
}

fn main() {
    users = [#User{name: "Cara", age: 35}, #User{name: "Ann", age: 41}]
    sorted = users |> Iter.sort_by(.name)

    dbg sorted
}

```
<!-- expect
dbg line 10: sorted = [#User{name: "Ann", age: 41}, #User{name: "Cara", age: 35}]
-->

A tuple key sorts by several fields, and
[`Reverse.of`](/reference/reverse/) around a key puts the largest first:
`Iter.sort_by(users, fn u -> #(Reverse.of(u.age), u.name))`.
[`Iter.sort_with`](/reference/iter/#itersort_with) takes a function that
compares two values. So when the compiler says a type has no `Comparable`
impl, the fix is usually `sort_by`, not the derive.

### Listing an enum's variants

`derive Variants` gives an enum whose variants carry no data a `variants()`
function. It returns every variant in a `List`, in the order they are
declared:

```nomi-run
enum Coin {
    Penny
    Nickel
    Dime
    Quarter
}

derive Variants for Coin

fn main() {
    dbg Coin.variants()
}

```
<!-- expect
dbg line 11: Coin.variants() = [Penny, Nickel, Dime, Quarter]
-->

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A derive sits in the same file as its type.
- Each field's type must implement the interface. `derive Hashable for
  Outer` with a field of type `Inner` needs `Inner` to implement `Hashable`
  too.
- On a generic type, `derive Equatable for Box<T>` implies `T: Equatable`,
  checked where `Box` is used.
- A distinct type's derive goes through the value it wraps:
  `Hashable.hash(Id(5)) == Hashable.hash(5)`.
- A derive beside a hand-written impl of the same interface is an error.
  Drop one.
- `with` options are per declaration. There is no per-field rename or
  exclude, and no derive you write yourself.
- `derive Variants` on an enum with a variant that carries data is an error
  that names the variant.

Spec: [Derive](/spec/interfaces-implementations-and-derive/#derive),
[Derivable interfaces](/spec/interfaces-implementations-and-derive/#derivable-interfaces).

</div>

## Defaults over distinct types

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
    fn unit(t: self) -> String

    fn label(self(n) as t) -> String where self: Distinct<Decimal> {
        "${Decimal.round(n, 1, .HalfUp)} ${Temperature.unit(t)}"
    }

    fn scale(self(n), by: Decimal) -> self where self: Distinct<Decimal> {
        self(n * by)
    }
}

impl Temperature for Celsius {
    fn unit(_t: Celsius) -> String {
        "C"
    }
}

impl Temperature for Fahrenheit {
    fn unit(_t: Fahrenheit) -> String {
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
itself, as with any `where self` bound.

## One impl per type and interface

A type can implement an interface only once in a whole program. Two
`impl Display for User { ... }` blocks anywhere in the program are a compile
error, reported before anything runs.

A generic interface is the one exception: a type may implement it once at
each set of type arguments. `String` implements `Contains<String>`,
`Contains<Regex<C>>` and `Contains<Codepoint>`, and each call takes the impl its
argument's type chooses, so `String.contains(s, "x")` and
`String.contains(s, '-')` call different impls. Each such impl writes its
type arguments in its header.

Two types with the same name in different files are different types. So a
`Display` impl for one file's `Date` doesn't clash with a `Display` impl for
another file's `Date`.

Where an impl may be written is the orphan rule: it must be in the module that
declares the interface or the type, with one exception for type arguments.
[Modules & Imports](/modules-and-imports/#the-orphan-rule) explains it.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- Each impl of a generic interface for one type writes its type arguments
  in its header: `impl Holds<Int> for Box`, `impl Holds<String> for Box`.
- Two impls may not overlap. A generic `impl Holds<T> for Wrap<T>` covers
  every `Holds<Int>`, so another impl at `Int` beside it is an error.
- The argument's type picks the impl at each call. When nothing picks one,
  as with an argument of a type no impl takes, the error names the impls
  there are.
- An impl is part of the program once its file is imported anywhere, and
  then it applies in every file.

```nomi
interface Holds<T> {
    fn holds(b: self, x: T) -> Bool
}

struct Box {
    n: Int
    s: String
}

impl Holds<Int> for Box {
    fn holds(b: Box, x: Int) -> Bool {
        b.n == x
    }
}

impl Holds<String> for Box {
    fn holds(b: Box, x: String) -> Bool {
        b.s == x
    }
}
```

Spec: [Several instantiations for one type](/spec/interfaces-implementations-and-derive/#several-instantiations-for-one-type),
[Foreign Types](/spec/interfaces-implementations-and-derive/#foreign-types).

</div>

## Why not structural field matching?

Why not write `fn greet(v: #{name: String})` and accept any struct with a
`name` field? TypeScript, Go and OCaml each allow something like this.

Nomi doesn't. A parameter of type `#{x: Int, y: Int}` accepts only values with
exactly those fields. It rejects an anonymous struct with more fields. It also
rejects a named struct, such as a `Point`, even if its fields match.

Two fields with the same name often mean different things. An `id: String`
on a `User`, an `Order` and an `Invoice` is three different kinds of id. A
function that accepted all three by accident would hide mistakes. Nomi gives
you three explicit ways instead:

1. Declare an interface. A function such as `fn name(value: self) -> String`
   gives every type that implements it the same way to get a name. Each type
   opts in with an `impl` block.
2. Build an anonymous struct at the call, such as
   `distance(#{x: p.x, y: p.y}, #{x: q.x, y: q.y})`. The call shows exactly
   which fields it passes.
3. Declare a type for the purpose and a function that converts to it, such
   as `fn to_coord(p: Point) -> Coord2D { … }`.

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

## See it in action

<div class="tour-examples">
<a href="/examples/clock/"><span class="tour-example-name">Clock</span><span class="tour-example-what">A time of day that wraps around.</span></a>
<a href="/examples/rational-numbers/"><span class="tour-example-name">Rational Numbers</span><span class="tour-example-what">Fractions kept in lowest terms.</span></a>
<a href="/examples/allergies/"><span class="tour-example-name">Allergies</span><span class="tour-example-what">Decode a score into allergens.</span></a>
</div>

<a class="tour-examples-all" href="/examples/?chapter=interfaces-and-dispatch">Every example that uses Interfaces &amp; Dispatch</a>

[Typed Literals](/typed-literals/) is next. It shows how a type turns a string
literal such as `Date"2026-05-04"` into a checked value, through an
interface it implements.
