---
title: Functions & Lambdas
description: Named functions and lambdas, with default values, named arguments, parameters that unpack their arguments, and trailing callbacks.
---

A function is a named piece of code. A lambda is a function without a name.
Both are called the same way and take the same kinds of parameters. Either
one can go into a pipe or be passed to another function.

## Defining a function

```nomi-run
fn add(x: Int, y: Int) -> Int {
    x + y
}

fn abs(x: Int) -> Int {
    if x < 0 { return -x }
    x
}

fn main() {
    dbg add(3, 4)
    dbg abs(-9)
}

```
<!-- expect
dbg line 11: add(3, 4) = 7
dbg line 12: abs(-9) = 9
-->

A function is written `fn name(params) -> ReturnType { body }`. The last
expression in the body is the return value, so most functions never write
`return`. Use `return` to leave early, as `abs` does for a negative number.

Every parameter must be read in the body. If a parameter is unused on
purpose, name it `_` or start its name with `_`, as in
`fn constant(_input: Int) -> Int { 42 }`. The rule covers functions, lambdas,
functions in `impl` blocks, and `boot`. Interface and host declarations have
no body, so it doesn't apply to them.

A function that implements an interface sometimes has to keep the
interface's parameter name. To ignore that parameter, write `_ = name` in the
body.

## Lambdas

```nomi-run
fn main() {
    inc = fn x: Int -> x + 1
    dbg inc(41)
}

```
<!-- expect
dbg line 3: inc(41) = 42
-->

A lambda is written `fn params -> body`. Write a parameter's type when nothing
else tells the compiler what it is. When the lambda is passed to a function
that expects a certain type, the type is inferred.

A lambda is a value. You can bind it to a name, pass it to a function, or
return it from one.

Named functions are values too. After `f = add`, the call `f(3, 4)` works
the same as `add(3, 4)`.

## Captures

```nomi-run
struct Point {
    x: Int
    y: Int
}

fn main() {
    xs = [1, 2, 3]
    dbg xs |> Iter.filter(&(&1 > 1)) |> Iter.to_list()
    dbg xs |> Iter.map(&(&1 * 2 + 1)) |> Iter.to_list()
    dbg xs |> Iter.map(&#Point{x: &1, y: 0}) |> Iter.to_list()
    dbg xs |> Iter.map(&Int.max(&1, 2)) |> Iter.to_list()
}

```
<!-- expect
dbg line 8: xs |> Iter.filter(&(&1 > 1)) |> Iter.to_list() = [2, 3]
dbg line 9: xs |> Iter.map(&(&1 * 2 + 1)) |> Iter.to_list() = [3, 5, 7]
dbg line 10: xs |> Iter.map(&#Point{x: &1, y: 0}) |> Iter.to_list() = [#Point{x: 1, y: 0}, #Point{x: 2, y: 0}, #Point{x: 3, y: 0}]
dbg line 11: xs |> Iter.map(&Int.max(&1, 2)) |> Iter.to_list() = [2, 2, 3]
-->

A capture writes a small function in place: `&`, then the function's body.
`&1` is its first parameter, `&2` its second. `&(&1 > 1)` is `fn x -> x >
1`, `&(&1 * 2 + 1)` is `fn x -> x * 2 + 1`, and `&#Point{x: &1, y: 0}` is
`fn x -> #Point{x: x, y: 0}`.

The body after `&` is a call, a parenthesized expression or a literal, or
one of those negated (`&!Iter.is_empty(&1)`), so any other operator goes in
parentheses. A call that takes the `&1`, `&2`, ... as
its own arguments, in order, is a partial application: `&Int.max(&1, 2)`
is `fn x -> Int.max(x, 2)`, and its parameters are the callee's, with
their names and defaults, so `inc = &Int.max(&1, 0)` needs no annotation.

The function the call expects gives the other captures' parameters their
types. Each call of a capture runs its whole body, as the lambda would.
Write a lambda when the body takes more than one step or needs names.

## No Function Overloads

Two functions in the same scope can't have the same name. Nomi never picks
between functions by the types of the arguments, so this is an error:

```nomi-run error
type UserId String

fn parse(_text: String) -> Int {
    0
}

fn parse(_id: UserId) -> Int {
    0
}

fn main() {
    Unit
}
```
<!-- expect
main.nomi:7:4: 'parse' is already defined in this scope as a function
main.nomi:7:4: help: pick a different name
main.nomi:3:4: note: 'parse' is first defined here
-->

Give each function a name that says what it does, as the standard library
does: [`String.to_int(text)`](/reference/strings/#stringto_int),
[`Date.parse(text)`](/reference/calendar/#dateparse),
[`Date.new(year, month, day)`](/reference/calendar/#datenew).

When you want one operation that works on several types, define an
[interface](/interfaces-and-dispatch/) and implement it for each type. The
call names the type or the interface, so it is always clear which version
runs.

## Default parameter values

```nomi-run
fn greet(name: String, greeting: String = "Hello") -> String {
    "${greeting}, ${name}!"
}

fn main() {
    dbg greet("World")
    dbg greet("World", "Hi")

    // Lambdas take defaults the same way:
    add = fn x: Int, y = 10 -> x + y
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

A parameter can have a default value. A caller can leave that argument out,
or pass a value to replace the default.

A named function still writes the type of a parameter with a default, as
`greeting: String` does. Its parameter types are part of its signature,
which code in other files relies on, so changing a default never changes
the function's type. A lambda is checked where it stands, so it can leave
the type off, as `y = 10` does, and the type comes from the default value.

Passed where a function with fewer parameters is expected, a named function
fills the rest from its defaults too: `["Ada", "Lin"] |> Iter.map(greet)`
greets each name with `"Hello"`, and `Iter.map(rows, String.join)` joins each
row with no separator. Bound with nothing expected, as in `g = greet`, the
value keeps every parameter, and a call through it passes every argument.

## Named arguments

```nomi-run
fn connect(host: String, port: Int = 8080, timeout: Int = 30) -> String {
    "${host}:${port}:${timeout}"
}

fn main() {
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

You can pass any argument by name, as `timeout: 10`. Named arguments can come
in any order. When you mix the two kinds, positional arguments come first.

Names help most with defaults. The second call sets `timeout` and keeps the
default `port` without writing it.

## Destructuring parameters

```nomi-run
type Dur Int

struct Point {
    x: Int
    y: Int
}

fn subtract(Dur(a), Dur(b)) -> Dur {
    Dur(a - b)
}

fn sum_point(#Point{x, y}) -> Int {
    x + y
}

fn add_pair(#(a, b): #(Int, Int)) -> Int {
    a + b
}

fn main() {
    Dur(diff) = subtract(Dur(10), Dur(3))
    dbg diff
    dbg sum_point(#Point{x: 4, y: 5})
    dbg add_pair(#(20, 22))
}

```
<!-- expect
dbg line 22: diff = 7
dbg line 23: sum_point(#Point{x: 4, y: 5}) = 9
dbg line 24: add_pair(#(20, 22)) = 42
-->

A parameter can be a pattern instead of a plain name. The pattern unpacks the
argument as the function is called. `sum_point` gets the `x` and `y` fields
of its `Point` directly.

The pattern must match every possible value of its type. Patterns like that
are called irrefutable. Tuples, structs, distinct types such as `Dur`, and
enums with one variant all qualify.

When the pattern names a type, as `Dur(a)` and `#Point{x, y}` do, you don't
write the type again. A tuple or an anonymous struct pattern names no type,
so it needs one, as in `#(a, b): #(Int, Int)`.

## Trailing callbacks

```nomi-run
fn transform(x: Int, factor: Int = 1, f: (Int) -> Int) -> Int {
    f(x * factor)
}

fn double(n: Int) -> Int {
    n * 2
}

fn main() {
    dbg transform(5, fn x -> x + 1)
    dbg transform(5, factor: 3, fn x -> x + 1)
    dbg transform(5, factor: 3, double)
}

```
<!-- expect
dbg line 10: transform(5, fn x -> x + 1) = 6
dbg line 11: transform(5, factor: 3, fn x -> x + 1) = 16
dbg line 12: transform(5, factor: 3, double) = 30
-->

When a function's last parameter is a function, you can pass that argument
last without a name. In `transform(5, fn x -> x + 1)`, the lambda skips `factor`
and goes to `f`, so `factor` keeps its default. The same happens after a
named argument, as in the second call.

The last call passes `double`, an existing function, instead of a lambda. It
goes to `f` the same way. What decides is the parameter list, not how the
argument is written.

The rule: a final unnamed argument goes to the last parameter when that
parameter takes a function and the parameter it would otherwise fill does
not. When both take functions, the arguments fill the parameters in order.

## Unwritten code

```nomi-run error
struct Header {
    title: String
}

fn parse(text: String) -> Header {
    todo "parse the header"
}

fn main() {
    dbg parse("# Notes")
}
```
<!-- expect
todo reached at main.nomi:6: parse the header
-->

`todo` marks a body or a value you haven't written yet. It fits any type, so
the rest of the program still checks and runs.

If the program reaches it, the program stops with
`todo reached at main.nomi:6: parse the header`. The editor shows each
`todo` as a warning, and `nomi build` refuses a program that still has one.

## Code that cannot be reached

```nomi-run error
fn initial(name: String) -> String {
    case Iter.first(name) {
        Some(letter) -> letter
        None -> unreachable "names are never empty"
    }
}

fn main() {
    dbg initial("Ada")
    dbg initial("")
}
```
<!-- expect
dbg line 9: initial("Ada") = "A"
line 4: unreachable: names are never empty
-->

`unreachable` marks a place your own code guarantees it never gets to, and
its reason says why. Like `todo`, it fits any type. Unlike `todo`, it is
finished code: `nomi build` accepts it.

If the program does reach it, the guarantee was wrong, so the program stops
with `line 4: unreachable: names are never empty`. Use it only for what the
program itself makes true. A name a user typed can be empty, and that case
needs a real answer.

When the guarantee is that one call succeeds, a
[postfix `!`](/pattern-matching/#calls-that-cannot-fail) says it after the
call: `Date.new(2024, 1, 1)!`. Keep `unreachable` for a reason the reader cannot see
from the call, like the one above.

[Pipes](/pipes/) is next. It shows how `|>` joins these calls into code that
reads from top to bottom.
