---
title: "Interfaces & Dispatch"
description: "Declare contracts with `interface`, implement them with `impl Iface for Type` blocks. Qualified call shapes, universal Debug, derives, and interface-bounded generics."
---

Nomi has functions, not value-dot methods. Type-related helpers live on their
type owner (`User.rename(user)`, [`List.concat(xs, ys)`](/reference/lists/#listconcat));
receiverless helpers live in files ([`io.print(value)`](/reference/io/#ioprint)).
The [Structs, Enums, Distinct Types](/structs-enums-distinct/#type-owned-functions)
chapter shows ordinary `impl Type { ... }` blocks. This chapter adds
`impl Interface for Type { ... }` blocks: functions supplied for a contract,
still called through an explicit qualifier. The everyday spelling usually names
the implementing type (`User.to_json(user)`). Use the interface qualifier when
you need it: most often to disambiguate two interfaces with the same function
name, or in generic code where the bound is the available owner
([`Display.to_string(value)`](/reference/display/#displayto_string)).

An `interface` declares a contract: the functions a type must provide. A type
supplies that contract with an `impl Iface for Type { ... }` block. This
chapter starts with the direct shape, then shows how qualified calls, defaults,
generic bounds, operators, derives, and the orphan rule fit around it.

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

In an interface signature, `self` is a type placeholder for the concrete
implementing type. It is not a value, not an implicit parameter, and not a
qualifier. Every `self` in one interface function signature denotes that same
concrete type for a given implementation.

The concrete functions spell their real implementing types (`Dog`, `Cat`)
directly. The parameter's *name* (`animal`) is documentation; the impl can
rename it (`d`, `c`).

## Qualified call shapes

At ordinary call sites, interface implementation functions are called through an
explicit qualifier, not as a bare `speak(dog)`. The qualifier is the name before
`.`. Use the implementing type for the everyday spelling (`Dog.speak(dog)`), and
use the interface qualifier when you need to disambiguate or when generic code
only has the interface bound available (`Speech.speak(dog)`). Both forms select
the implementation for the concrete type bound to `self`:

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

Functions are qualified by their nearest API container. Inherent functions use
their type or interface owner, like [`Iter.map(xs, f)`](/reference/iter/#itermap)
or [`List.concat(xs, ys)`](/reference/lists/#listconcat). Interface contract
functions use the implementing type (`Dog.speak(dog)`) or, when needed, the
interface (`Speech.speak(dog)`). Receiverless file APIs use the imported file
name, like [`io.print(x)`](/reference/io/#ioprint). A bare name still resolves
to a local helper or binding, not to ambient interface dispatch.

## Default Functions

Interface functions can include a *body* — that body becomes the
**default implementation**. Every type implementing the interface gets
the default for free; the type only needs to provide functions
for the ones that don't have a default. Defaults are **final by default** —
implementors *can't* override them. Mark a default `open` to
explicitly permit override:

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

Defaults are closed unless marked `open`. A closed default is part of the
interface's promised behavior; an `open` default says implementors may replace
it. An interface item with no body is required, and interface items do not use
`pub` — they share the interface's visibility.

Inside an interface default, a bare call such as `label(value)` names a sibling
function from the same interface. Inside an `impl` block, the same-owner rule
applies too: sibling implementation functions can be called bare from that body
when unambiguous. At outside call sites, keep the qualifier.

## Requiring a value

An interface declares functions only. To require that every implementor
supply a value, declare a function requirement for it. A struct implements
it by returning its field, and any other type computes it, so the contract
doesn't care how the value is stored:

```nomi-run
import std/io

interface HasName {
    fn name(value: self): String

    // Default — reads the name through the required function.
    fn greet(value: self): String {
        "Hello, ${name(value)}"
    }
}

struct User {
    name: String
    age: Int
}

impl HasName for User {
    fn name(user: User): String {
        user.name
    }
}

enum Pet {
    Dog String
    Cat String
}

impl HasName for Pet {
    fn name(pet: Pet): String {
        case pet {
            .Dog(name) -> name
            .Cat(name) -> name
        }
    }
}

fn main() {
    alice = User{name: "Alice", age: 30}
    rex = Pet.Dog("Rex")
    io.print(HasName.greet(alice))
    io.print(HasName.greet(rex))
}

```
<!-- expect
Hello, Alice
Hello, Rex
-->

Read the value with a qualified call: `HasName.name(x)` on a value of
interface type, `T.name(x)` through a bound. A value of interface type or of
a type parameter has no fields, so `x.name` on one is a compile error.

[App Fields, Defer & Context](/capabilities-and-context/) uses a related
shape: an app payload's `logger: Logger` is a *field whose type is an
interface*. The natural follow-up question — "why not just write
`fn greet(v: {name: String})` and match on the shape?" — is covered in
[Why not structural field matching?](#why-not-structural-field-matching)
in the deep-dive section below.

## Universal `Debug`

[`Debug`](/reference/debug/) is the only interface that's automatic. Every value can be
debugged — including the types you just declared, with no `impl`
conformance, no `derive`. Nomi provides structural Debug output for every
declared type:

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

That's why `dbg` has been working everywhere in the tour with no ceremony.
[`io.print`](/reference/io/#ioprint), in contrast, requires [`Display`](/reference/display/) — which is opt-in.

Automatic Debug is a *default*, not a fixed rule. When you want custom
formatting, write `impl Debug for Type { ... }` (or `derive Debug` for the
structural one). Your impl takes precedence.

## Opting in to `Display`

[`io.print`](/reference/io/#ioprint) requires [`Display`](/reference/display/) — the user-facing format. For your own types,
you provide it with an `impl Display for Type { ... }` block containing a
`to_string` function:

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

The function name (`to_string`) matches the function in the `Display`
interface; the implementation spells the concrete implementing type, `User`.

## Interface-bounded generics

A generic function can require its type parameter to implement an interface.
The bound is checked at the call site:

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

Both [`Int`](/reference/int/) and [`String`](/reference/strings/#type-string) already implement [`Comparable`](/reference/comparable/) (in stdlib), so both
calls satisfy the bound automatically.

Generic headers introduce names; `where` constrains those names. See
[Generics](/generics/) for the full shape, including multiple and relational
bounds. The short version is:
`where T: Comparable`, `where T: Display and Debug`,
`where T: Steppable<S>`.

Interface signatures use the same function-level form. A required interface
function can carry a `where` clause when only that function needs an extra
bound; a default function writes the clause in the same place, before its body.
If the interface function needs its own type parameter, introduce it explicitly
on the function before constraining it:

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

`T` comes from `Ranked<T>`; `K` comes from `prefer<K>`. A type variable that is
not introduced by the interface or the function's own `<...>` header is an
error.

## Operators that dispatch

Most interface functions are called directly through their qualifier:
[`Display.to_string(value)`](/reference/display/#displayto_string),
`Dog.speak(dog)`, `Iter.each_while(xs, yield)`. A few operators are also backed
by interfaces. Equality can route through [`Equatable`](/reference/equatable/), ordering
requires [`Comparable`](/reference/comparable/), and arithmetic uses [`Add`](/reference/add/), [`Subtract`](/reference/subtract/), [`Multiply`](/reference/multiply/), and
[`Divide`](/reference/divide/).

The arithmetic interfaces are deliberately two-parameter: the impl chooses both
the right-hand operand type and the expression result. That means same-type
arithmetic and domain-specific stepping use the same protocol:

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

`derive` asks Nomi to write the obvious structural implementation for a common
interface. The main derives are:

- **Display** and **Debug** for rendering values.
- **Equatable** and **Hashable** for equality and hash-based collections.
- **Comparable** for ordering with `<`, `>`, and `Iter.sort`.
- **ToJson** and **FromJson** for structural JSON conversion.

Display is often hand-written for real user-facing text, and Debug is automatic
unless you override it. The derives that need the most explanation are the
comparison and collection ones.

Some derives accept options with `with`. JSON derives use that slot for field
names. Encoding has a `User` value to dispatch from, so type and interface
qualification both work. Decoding has no existing `User` value, so the target
type usually qualifies the call:

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

Nomi usually wants one everyday way to write a call: import the owner whose API
you are using, then call through that owner. Interface dispatch is the one place
where the language keeps a little optionality. The implementation is the same;
choose the surface that makes the call read best.

For a `User` declared in `users.nomi`, type-qualified calls are the normal
application spelling:

```nomi
encoded = User.to_json(user)
user = try User.from_json(encoded)
```

The interface name is useful when type qualification would be ambiguous, or
when generic code names the bound rather than a concrete implementing type:

```nomi
encoded = ToJson.to_json(user)
user = try FromJson.from_json<User>(encoded)
```

If the target type is already written nearby, the annotation can carry it:

```nomi
user: User = try FromJson.from_json(encoded)
```

That flexibility belongs to interface dispatch. The structural derives
themselves stay plain.

### Structural equality

Equality is structural out of the box. Two values are `==` when they have
the same shape and equal parts — no impl, no derive, no ceremony. The same
goes for hashing: any value can be a Map key.

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

So what are `derive Equatable` and `derive Hashable` for? Two things:

**Generic bounds.** A function that demands `where T: Equatable` only accepts
types that *declare* conformance — structural equality working at runtime
isn't a declaration. Without the derive, this is a compile error:

```nomi
fn contains<T>(xs: List<T>, needle: T): Bool where T: Equatable {
    Iter.any?(xs, |x| x == needle)
}

contains([Point{x: 1, y: 2}], Point{x: 1, y: 2})
// error: Point does not implement Equatable (required by `where T: Equatable`)
```

Add `derive Equatable for Point` and the bound is satisfied — the compiler
generates the field-by-field impl.

**Custom equality.** A hand-written `Equatable` implementation in an
`impl Equatable for Type { ... }` block *overrides* the structural default.
That's how `DateTime` compares by instant — the same moment in two time zones
is `==` (see [Dates and times](/dates-and-times/)).

Ordering is the exception: there is no structural fallback for `<`. The
compiler can't guess whether a `Point` orders by `x`, by `y`, or by distance
from the origin, so comparing values of your own type without a `Comparable`
impl is a compile error. `derive Comparable` generates field-by-field
lexicographic ordering:

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

On an enum, `derive Comparable` uses **variant declaration order** as the
ranking — earlier variants sort first. Payloads break ties only *within*
the same variant; the rank always dominates:

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

`Warning(9) < Error(1)` is `True` — the payload never outranks the variant.
This makes the variant list itself the single place the ordering is read
and changed, which is exactly what you want for severity levels, lifecycle
states, and priority ladders.

### Deriving vs sorting by key

Most domain types shouldn't implement [`Comparable`](/reference/comparable/) at all. A `User` or an
`Order` has no *intrinsic* order — it has many contextual ones (by name in
the directory, by date in the feed). That's a use-site decision, and the
use-site tool is [`Iter.sort_by`](/reference/iter/#itersort_by) with a key projection (or [`Iter.sort_with`](/reference/iter/#itersort_with)
with a full comparator):

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

Reach for `derive Comparable` only when declaration order *is* the
ordering — the type has one true ranking and the declaration states it:

- **Single-field wrappers** — `type Score Int`, `type Priority Int`.
- **Ranked enums** — `Severity` above; the variant list is the ladder.
- **Place-value structs** — `Version{major, minor, patch}`-shaped types,
  where field-by-field comparison is the *definition* of the order (the
  stdlib's `Date` and `Time` are this case).

Deriving `Comparable` on an ordinary domain struct bakes one arbitrary
order into the type — call sites quietly start depending on it, and
reordering the fields (an otherwise meaningless refactor) silently changes
every sort. When the compiler tells you a type has no `Comparable` impl,
the right response is usually `sort_by`, not the derive.

:::note[Continue or dig deeper]
The tour moves on to [**Modules & Imports**](/modules-and-imports/),
which covers how `interface` and `impl` declarations interact with module
boundaries, import cycles, and dispatch across files. Or stay here for the
deep-dive section below.
:::

## Going deeper

### Coherence: at most one impl per (Iface, T)

A type implements an interface **at most once program-wide**. Two
`impl Display for User { ... }` blocks, anywhere in the build graph, are a
compile error. Nomi checks the whole build graph, so collisions are caught
before the program runs.

The check uses the resolved type and interface owners, so a `Display` impl for
one module's `Date` does not collide with a `Display` impl for another module's
different `Date`.

### The orphan rule

Coherence alone isn't enough — two libraries each reopening `Int` with an
`impl Display for Int { … }` block would silently collide only when both are
linked into the same program. The **orphan rule** prevents the
situation upstream: an `impl Iface for T { ... }` block must
live in the Nomi module that owns either `Iface` or `T`. Third-party code
that owns neither can't write the impl directly.

An impl's home module is the directory tree rooted at its `nomi.toml` and
`go.mod`. The escape valve is the newtype wrapper: declare `type MyId Int` in
your module, then `impl Display for MyId { ... }` is legal because
`MyId` is yours.

Ordinary helper functions are just file functions. A sibling file
can define receiverless helpers around an imported type, but it cannot add a new
type-owned function, field, variant, or orphan interface implementation to that
imported type.

### Why not structural field matching?

The natural question after seeing an interface require a `name` is:
why not just write `fn greet(v: {name: String})` and accept any
struct with a `name` field? Other languages do this — TypeScript and
Go (interface satisfaction by shape), OCaml row polymorphism.

Nomi rejects it explicitly. A function parameter typed `{x: Int, y: Int}`
accepts **only** values with that exact shape — not anon structs with
additional fields, and not nominal structs that happen to have
matching fields. Width is part of the type's identity.

The reasoning: field-name coincidence is not a semantic contract. An
`id: String` field on `User`, `Order`, and `Invoice` shares a type
but rarely shares a meaning, and silent acceptance erodes trust as
the codebase grows. So Nomi gives three explicit alternatives instead
of a structural-acceptance default:

1. **Declare an interface** — capture the shared contract as a
   nominal type. A function requirement such as `fn name(value: self): String`
   gives every implementor the same accessor, **nominally** opted in via an
   `impl Iface for Type` block, not matched by accident on layout.
2. **Construct an anon struct at the call site** — e.g.
   `distance({x: p.x, y: p.y}, {x: q.x, y: q.y})`. Explicit shape
   adaptation; the call site shows exactly which fields are borrowed.
3. **Define a nominal domain type + conversion function** — e.g.
   `fn to_coord(p: Point): Coord2D { … }`. Mismatches surface where a
   reviewer can see them.

The choice is usually clear from context: if several types should
share a contract long-term, (1); if one call site needs a one-shot
reshape, (2); if there's a meaningful domain conversion, (3).

The same intuition is why Nomi requires an explicit `impl Iface for Type`
block instead of Go's "satisfies any interface whose functions
you happen to match." Coincidence of functions, like coincidence of
field names, isn't a contract — the `impl` block is the type opting in,
visibly and grep-ably, to a published protocol.

Dispatch uses explicit qualifiers for the same reason. Outside a same-owner
body, a bare name might be a local binding or a file function; `Type.fn(value)`,
`Iface.fn(value)`, and `file.fn(value)` say which API or contract is being used.
