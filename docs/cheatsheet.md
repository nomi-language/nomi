# Nomi cheat sheet

Nomi is a statically typed language where every value is immutable. This page
is the short form of the rules most often written wrong by someone coming from
Rust, Go, Elixir, TypeScript or Python. Every example is a test that the
compiler checks. The full rules are in the
[specification](https://nomi-lang.org/spec/), and the idiom is in
[Writing Nomi](https://nomi-lang.org/writing/).

## Bindings and functions

- There is no `let`, `var`, `const` or `:=`. A binding is `name = expr`, with
  an optional type: `total: Int = 0`. A later `name = ...` binds the name again.
- Values are immutable. A struct or collection is "changed" by building a new
  one. Closures capture values when they are created.
- A named function writes its parameter types and any return type other than
  `Unit`. Its body's last expression is its result. `return` is there for an
  early exit.
- A parameter may have a default, and an argument may name its parameter.
- A lambda is `fn x -> ...` or `fn x, y -> { ... }`. Its parameters are
  inferred from where it is passed. A lambda bound to a name needs annotated
  parameters: `fn n: Int -> n * 2`.
- `once name = expr` is the only file-level value. It is computed on first
  use, once, even when several tasks read it at the same time.

```nomi-run
fn area(width: Int, height: Int = 1) -> Int {
    width * height
}

once limit = 3

test "bindings and functions" {
    x = 2
    x = x + 1
    double = fn n: Int -> n * 2

    assert area(x, height: 4) == 12
    assert area(5) == 5
    assert double(limit) == 6
}
```
<!-- expect
ok bindings and functions
test result: ok. 1 passed, 0 failed
-->

## Literals

- A named struct is its name and its fields: `Point{x: 1}`,
  `Shape.Circle{radius: 1.0}`, `.Circle{radius: 1.0}`. In the head of an `if`
  or `case` it goes in parentheses: `case (Point{x: 1}) { ... }`. A named
  tuple is its name and its elements: `Pair(1, "x")`.
- Every other literal but a list starts with its shape's symbol: `#` for an
  anonymous struct, a tuple or a vector (`#{x: 1}`, `#(a, b)`, `#[1, 2]`),
  `%` for a map or a set. A plain `{` is always a block.
- Patterns use the same forms as literals.
- Types are written without a symbol, except tuples and anonymous structs:
  `List<Int>`, `Vector<T>`, `Map<K, V>`, `Set<T>`, `#(Int, String)`,
  `#{x: Int}`.
- Strings interpolate with `${...}`. A backtick string is raw: no escapes and
  no interpolation. A type name before a backtick string is a typed literal,
  checked at compile time: ``Regex`\d+` ``, ``Url`https://nomi-lang.org` ``.
  A type that implements `CheckedLiteral`, such as `Url`, is checked in
  every quote style, slots included: `Url"https://x.org/${id}"` is a `Url`,
  never a `Result`.
- std/html's `Html"..."` is an HTML template the compiler checks. Each
  `${...}` is escaped for where it stands. A one-line fragment quotes its
  attributes with `'`, since `"` ends the literal:
  `Html"<b class='x'>${n}</b>"`. Several lines go in `Html"""..."""`.
- The constants are `True`, `False`, `None` and `Unit`. There is no `null`.

| Shape | Literal | Named form |
|---|---|---|
| list | `[1, 2]` | `Ids[1, 2]` |
| vector | `#[1, 2]` | `#Path[1, 2]` |
| tuple | `#(1, "x")` | `Pair(1, "x")` |
| struct | `Point{x: 1}`, `#{x: 1}` | `Shape.Circle{radius: 1.0}`, `.Circle{radius: 1.0}` |
| map | `%{"a" => 1}` | `%Kvs{"a" => 1}` |
| set | `%[1, 2]` | `%Tags["a"]` |

```nomi-run
struct Point {
    x: Int
    y: Int = 0
}

test "literals" {
    point = Point{x: 1}
    moved = #{..point, y: 5}
    #(count, name) = #(2, "Ada")

    assert moved == Point{x: 1, y: 5}
    assert %{"a" => 1, "b" => 2} == %{"b" => 2, "a" => 1}
    assert "${name} has ${count}" == "Ada has 2"
    assert `a\n` == "a\\n"
    assert [first, ..rest] = [1, 2, 3]
    assert first == 1 and rest == [2, 3]
}
```
<!-- expect
ok literals
test result: ok. 1 passed, 0 failed
-->

## Types and calls

- A struct's fields, an enum's variants and an interface's functions are one
  per line, with no commas, `|` or `field` keyword. An interface declares only
  functions. A value every implementor supplies is a function, such as
  `fn name(value: self) -> String`.
- Behavior goes in top-level `impl Type { ... }`, `impl Interface for Type
  { ... }` and `derive Interface for Type` declarations.
- `type UserId Int` is a distinct type: `UserId(5)` builds one, and the pattern
  `UserId(n)` opens it.
- There are no method calls. `x.name` only reads a field. A function is called
  through its owner:
  - a type: `String.trim(s)`, `Map.get(m, k)`, `Shape.area(shape)`;
  - an interface: `Display.to_string(x)`, or `T.to_string(x)` for a bounded
    type parameter;
  - a file, for its file-level functions: `io.print(x)`.
- `to_string(n)` with no owner, `int.to_string(n)` and `x.to_string()` are
  errors. A file's name reaches only its file-level functions, never one
  declared in an `impl`: `duration.seconds(1)` is an error, and
  `Duration.seconds(1)` is the call.

```nomi-run
enum Shape {
    Circle {radius: Float}
    Square {side: Float}
}

impl Shape {
    fn area(shape: Shape) -> Float {
        case shape {
            .Circle{radius} -> 3.0 * radius * radius
            .Square{side} -> side * side
        }
    }
}

derive Equatable for Shape

interface Named {
    fn name(value: self) -> String
}

impl Named for Shape {
    fn name(shape: Shape) -> String {
        case shape {
            .Circle{} -> "circle"
            .Square{} -> "square"
        }
    }
}

type UserId Int

fn label<T>(value: T) -> String where T: Named {
    "a ${T.name(value)}"
}

test "types and calls" {
    shape = Shape.Square{side: 2.0}
    UserId(id) = UserId(7)

    assert Shape.area(shape) == 4.0
    assert Named.name(shape) == "square"
    assert label(shape) == "a square"
    assert shape == .Square{side: 2.0}
    assert id == 7
    assert String.length("héllo") == 5
    assert Int.to_string(42) == "42"
}
```
<!-- expect
ok types and calls
test result: ok. 1 passed, 0 failed
-->

## Branching and errors

- `if` and `case` are expressions. `case` must cover every shape the value can
  take. A guard is written `when`: `Some(n) when n > 0 -> ...`.
- The branches of an `if` or `case` have one type. Branches of different
  types that implement one interface join as it only where the value's type
  is written as that interface, by an annotation, a parameter or a declared
  result: `shape: Shape = if round { Circle{r: 1.0} } else { Square{side: 2.0} }`.
- There are no exceptions. A function that can fail returns `Maybe<T>`
  (`Some(v)` or `None`) or `Result<T, E>` (`Ok(v)` or `Err(e)`).
- A postfix `?` returns an `Err` or `None` from the enclosing function, lambda
  or test. A postfix `!` traps on one. A program's `main` may return a
  `Result`.
- Inside a lambda, `return` and `?` leave the lambda.
- In a pipe, `text |> parse()?` passes up a failure of that stage, and
  `|> then? fn v -> ...` does the same for a lambda's result.
- `x is Pattern` is a `Bool`.

```nomi-run
fn parse_age(text: String) -> Result<Int, String> {
    age = String.to_int(text) |> Maybe.to_result("not a number: ${text}")?

    if age < 0 {
        Err("negative: ${age}")
    } else {
        Ok(age)
    }
}

fn describe(age: Maybe<Int>) -> String {
    case age {
        Some(n) when n >= 18 -> "adult"
        Some(_) -> "minor"
        None -> "unknown"
    }
}

test "branching and errors" {
    assert parse_age("41") == Ok(41)
    assert parse_age("x") == Err("not a number: x")
    assert describe(Some(12)) == "minor"
    assert String.to_int("7")! == 7
    assert Some(1) is Some(_)
}
```
<!-- expect
ok branching and errors
test result: ok. 1 passed, 0 failed
-->

## Iteration and pipes

- `x |> f(a)` is `f(x, a)`. A call whose result feeds another call is written
  as a pipe.
- Every collection is an `Iter`, and the algorithms are `Iter`'s functions:
  `Iter.map`, `Iter.filter`, `Iter.reduce`, `Iter.count`, `Iter.sort`. There is
  no `List.map`. Operations particular to one container stay on its type:
  `List.head`, `Map.get`, `Set.union`.
- Adapters are lazy. `Iter.to_list`, `to_vector`, `to_set` and `to_map`
  collect, and `String.join` joins strings.
- A fold's starting value comes before its callback:
  `Iter.reduce(xs, 0, fn acc, x -> acc + x)`. So do `Iter.scan`,
  `Iter.map_reduce` and `Iter.loop`.
- A capture is a short lambda: `&(&1 * 10)`, `&Map.get(m, &1)`.
- There are no `for` or `while` loops. Use `Iter` functions, recursion (tail
  calls run in constant stack), or `Iter.loop(start, fn state -> ...)` with
  `break`.
- `Iter` is a push protocol: a type is iterable through an `impl Iter` whose
  `each_while` hands out its elements.

```nomi-run
test "iteration and pipes" {
    evens =
        1..=10
        |> Iter.filter(fn n -> n % 2 == 0)
        |> Iter.map(&(&1 * 10))
        |> Iter.to_list()

    assert evens == [20, 40, 60, 80, 100]
    assert Iter.reduce(evens, 0, fn acc, n -> acc + n) == 300
    assert List.head(evens) == Some(20)
    assert ["b", "a"] |> Iter.sort() |> String.join(", ") == "a, b"
    assert Iter.map_reduce([1, 2], 0, fn total, n -> #(total + n, total)) == #(3, [0, 1])
}
```
<!-- expect
ok iteration and pipes
test result: ok. 1 passed, 0 failed
-->

## Modules and imports

- A `.nomi` file is the unit of import and of visibility. Its top-level
  declarations are private unless marked `pub`. Its partner test,
  `shapes_test.nomi` for `shapes.nomi`, reads its private ones too.
- A module is the directory tree under a `nomi.toml`. `entry_points` lists the
  files with a `fn main`. An `impl Interface for Type` needs the interface,
  the type or both to be the module's own.
- Imports name files: `import std/io`, then `io.print(x)`. Braces bring in
  names: `import std/io.{self, Writer}`. Several imports group under one
  `import { ... }`. Unused imports are errors.
- Names are snake_case, types PascalCase. A predicate is `is_<state>`,
  `has_<thing>` or a bare verb (`contains`, `starts_with`).

```nomi-run
// FILE: shapes.nomi
pub struct Box {
    side: Int
}

pub fn area(box: Box) -> Int {
    box.side * box.side
}

// FILE: main.nomi
import {
    std/io
    shapes.{self, Box}
}

fn main() {
    io.print(shapes.area(Box{side: 3}))
}

// FILE: nomi.toml
[module]
name = "demo"
entry_points = ["main"]
```
<!-- expect
9
-->

## App fields, tests and concurrency

- A program's configuration and dependencies are one app struct. The entry
  file's `fn boot(startup: Startup) -> Result<App, E>` builds it from the
  arguments and environment and returns it in `Ok`. An `Err` stops the program
  before `main`. The struct has at most one `Context` field, which boot builds
  with `Context.root()`.
- Code reads a field as `App.field`. The statement `with App.field = value`
  replaces it from that line to the end of the block. Several fields take
  several lines.
- `test "name" { ... }` blocks sit beside the code. `assert` checks a `Bool`,
  `Some` or `Ok`, or a pattern; `refute` the opposite. `nomi test` runs them.
- A `group` holds at most one `clock`, `boot` and `setup`, in that order, then
  its tests. Groups do not nest. `boot server.boot(startup)` calls an entry's
  boot. A test binds the setup value with a pattern:
  `test "loads rows", db { ... }`. Each test runs clock, boot, setup and body
  afresh. `clock Clock.Virtual` makes waits instant.
- Tasks start only inside `concurrent { ... }`, which waits for them:
  `Task.spawn(fn -> ...)` and `Task.await(task)`, from `std/tasks`. Every task
  is awaited. `std/channels` splits a channel into a `Sender` and a
  `Receiver`. A `Supervisor` is created only while `boot` builds the app
  struct.

```nomi-run
import {
    std/io
    std/tasks.Task
}

struct App {
    greeting: String
}

fn boot(startup: Startup) -> Result<App, String> {
    Ok(App{greeting: Map.get(startup.env, "GREETING") |> Maybe.with_default("hello")})
}

fn greet(name: String) -> String {
    "${App.greeting}, ${name}"
}

fn main() {
    io.print(greet("Ada"))
}

group "greetings" {
    boot boot(Startup{env: %{"GREETING" => "hi"}})

    test "app fields" {
        assert greet("Ada") == "hi, Ada"
        with App.greeting = "hey"
        assert greet("Ada") == "hey, Ada"
    }

    test "tasks" {
        total = concurrent {
            a = Task.spawn(fn -> 20)
            b = Task.spawn(fn -> 22)
            Task.await(a) + Task.await(b)
        }

        assert total == 42
    }
}
```
<!-- expect
hello, Ada
ok greetings / app fields
ok greetings / tasks
test result: ok. 2 passed, 0 failed
-->
