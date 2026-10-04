---
title: Functions & Lambdas
description: Named functions, anonymous lambdas, and the call-site conveniences — defaults, named arguments, destructuring, and trailing callbacks.
---

A function names a piece of computation; a lambda is the anonymous version of
the same thing. Both share the same call shape and the same parameter
conveniences — defaults, named arguments, irrefutable destructuring — and both
slot interchangeably into pipes and higher-order code.

## Defining a function

`fn name(params): ReturnType { body }`. The body is a block expression: its
**last expression is the return value** — no `return` keyword needed for the
happy path. `return` exists for early exits from inside a conditional:

```nomi-run
fn add(x: Int, y: Int): Int {
    x + y
}

fn abs(x: Int): Int {
    if x < 0 { return -x }
    x
}

fn main(): Int {
    dbg add(3, 4)
    dbg abs(-9)
}

```
<!-- expect
dbg line 11: add(3, 4) = 7
dbg line 12: abs(-9) = 9
-->

Every ordinary parameter must be read in its body. Use `_` or `_name` when a
parameter is intentionally unused, for example `fn constant(_input: Int): Int { 42 }`.
This applies to functions, lambdas, methods, and `boot`. Discard parameters are
not readable bindings. Bodyless interface and host declarations are exempt.
When implementing an interface, retain its parameter labels and use `_ = name`
in the body to explicitly discard a value.

## Lambdas

Lambdas are written `|params| body`. Annotate parameter types when there's no
other clue; otherwise let inference handle it. A lambda is a first-class value
— bind it to a name, pass it to another function, or return one.

```nomi-run
fn main(): Int {
    inc = |x: Int| x + 1
    dbg inc(41)
}

```
<!-- expect
dbg line 3: inc(41) = 42
-->

Named functions are first-class too: `f = add` binds the value of `add` to `f`,
and `f(3, 4)` works exactly like `add(3, 4)`.

## No Function Overloads

Within a scope, a function name has one meaning. Nomi does not choose between
multiple same-named functions by argument type:

```nomi-run ignore
type UserId String

fn parse(_text: String): Int {
    0
}

fn parse(_id: UserId): Int {
    0
}

fn main() {
    Unit
}
```

Use names that describe the conversion or construction path:
[`String.to_int(text)`](/reference/strings/#stringto_int),
[`Date.parse(text)`](/reference/calendar/#dateparse), or
[`Date.new(year, month, day)`](/reference/calendar/#datenew). For polymorphism, define an
[interface](/interfaces-and-dispatch/) and implement it for each type; dispatch
is explicit at the call site with a type or interface qualifier.

## Default parameter values

Any parameter — in a function or a lambda — can carry a default. Callers can
omit it, or pass a value to override. Named `fn` signatures still write their
parameter types explicitly, even when a default would make the type obvious;
lambdas are local expressions, so their parameter types can come from context
or from the default value itself.

```nomi-run
fn greet(name: String, greeting: String = "Hello"): String {
    "${greeting}, ${name}!"
}

fn main(): Int {
    dbg greet("World")
    dbg greet("World", "Hi")

    // Lambdas take defaults the same way:
    add = |x: Int, y = 10| x + y
    dbg add(5)
    dbg add(5, 2)
}

```
<!-- expect
dbg line 6: greet("World") = "Hello, World!"
dbg line 7: greet("World", "Hi") = "Hi, World!"
dbg line 11: add(5) = 15
dbg line 12: add(5, 2) = 7
-->

## Named arguments

Any parameter can be passed by name at the call site, in any order. Positional
and named arguments can mix — positional first, then named. Named arguments
make defaulted middle parameters easy to override without remembering
positions:

```nomi-run
fn connect(host: String, port: Int = 8080, timeout: Int = 30): String {
    "${host}:${port}:${timeout}"
}

fn main(): String {
    dbg connect("localhost")
    dbg connect("localhost", timeout: 10)
    dbg connect(host: "api", port: 443, timeout: 5)
}

```
<!-- expect
dbg line 6: connect("localhost") = "localhost:8080:30"
dbg line 7: connect("localhost", timeout: 10) = "localhost:8080:10"
dbg line 8: connect(host: "api", port: 443, timeout: 5) = "api:443:5"
-->

## Destructuring parameters

A function or lambda parameter can take any *irrefutable* pattern in place of a
plain name — tuple, anonymous struct, distinct-type wrapper, single-variant
enum. The pattern unpacks the argument on the way in.

A pattern whose head names a concrete type (a distinct constructor like
`Dur(...)`, a typed struct like `Point{...}`) is **self-typing** — the type is
in the pattern, so no `: Type` annotation needed. A type-less pattern (a plain
tuple, an anonymous struct) needs the annotation:

```nomi-run
type Dur Int

struct Point {
    x: Int
    y: Int
}

fn subtract(Dur(a), Dur(b)): Dur {
    Dur(a - b)
}

fn sum_point(Point{x, y}): Int {
    x + y
}

fn add_pair((a, b): (Int, Int)): Int {
    a + b
}

fn main(): Int {
    Dur(diff) = subtract(Dur(10), Dur(3))
    dbg diff
    dbg sum_point(Point{x: 4, y: 5})
    dbg add_pair((20, 22))
}

```
<!-- expect
dbg line 22: diff = 7
dbg line 23: sum_point(Point{x: 4, y: 5}) = 9
dbg line 24: add_pair((20, 22)) = 42
-->

## Trailing callbacks

When a function's last parameter takes a function, the call site can leave that
argument *unnamed at the end* and it routes into the last slot automatically —
even when middle defaulted parameters are overridden by name. The pattern keeps
callback-style functions readable:

```nomi-run
fn transform(x: Int, factor: Int = 1, f: (Int) -> Int): Int {
    f(x * factor)
}

fn double(n: Int): Int {
    n * 2
}

fn main(): Int {
    dbg transform(5, |x| x + 1)
    dbg transform(5, factor: 3, |x| x + 1)
    dbg transform(5, factor: 3, double)
}

```
<!-- expect
dbg line 10: transform(5, |x| x + 1) = 6
dbg line 11: transform(5, factor: 3, |x| x + 1) = 16
dbg line 12: transform(5, factor: 3, double) = 30
-->

The last two lines pass the same kind of thing — a function — so they route the
same way. Writing the callback inline is the common case, but it is not what
makes the routing work: an existing function passed by name lands in the same
slot.

Nomi settles the routing from the parameter list, not from how the argument was
written. A trailing argument goes to the final parameter when that parameter
takes a function and the slot it would otherwise fill does not. When both take
functions there is a real choice between them, and the call keeps the
left-to-right order it already states.

## Unwritten code

`todo` stands in for a body or a value you have not written yet. It fits
whatever type its place expects, so the rest of the program checks and runs:

```nomi
fn parse(text: String): Header {
    todo "parse the header"
}
```

Reaching it stops the program with `todo reached at main.nomi:2: parse the
header`. The editor lists every `todo` as a warning, and `nomi build` refuses
a program that still has one.

The next chapter — [Pipes](/pipes/) — shows how `|>` weaves everything in this
chapter into expressions that read top-to-bottom.
