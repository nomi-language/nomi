# Writing Idiomatic Nomi

Style guidelines for hand-written Nomi (`.nomi`): examples, the stdlib
(`std/`) and `tests/`. `nomi fmt -w` owns *layout* (indentation, line
breaks, spacing, import grouping): code is indented 4 spaces per level and
lines break at 100 columns. This guide owns *idiom*, which construct to reach
for. Run `fmt` after editing a `.nomi` file. Where a rule below is one
`fmt` applies for you, the guide says so, so that you know not to fight it.

This is the canonical list. When a new convention is established, add it here
rather than scattering it across other docs.

---

## 1. Pipe nested calls

When a function call's result is an argument to another function call, write it
as a `|>` pipe chain instead of nesting:

```nomi
// no
Iter.to_set(Iter.map(s, f))

// yes
s
|> Iter.map(f)
|> Iter.to_set()
```

**Inline or stacked is your choice, and `fmt` keeps it.** Write a pipe on one
line and it stays inline; write it across several lines and it stays stacked,
each `|>` aligned with the subject. `fmt` breaks an inline chain only when it
overflows 100 columns, and never joins a stacked chain back onto one line:

```nomi
// written inline, kept inline (any stage count)
Set.insert(s, 9) |> Set.size() |> io.print()

// written stacked, kept stacked
Set.insert(s, 9)
|> Set.size()
|> io.print()
```

When `fmt` breaks an overflowing chain that is bound to a name, it moves the
chain under the binding:

```nomi
evens =
    [1, 2, 3, 4, 5, 6, 7, 8]
    |> Iter.filter(|x| x % 2 == 0)
    |> Iter.map(|x| x * 1000)
    |> Iter.to_list()
```

**Let the pipeline subject lead.** When a value is the subject flowing through
several operations, put it on its own first line and align every `|>` with it,
even when the subject is a bare name:

```nomi
s
|> Set.size()
|> io.print()
```

Inline is still fine for a short expression where the call itself is the point:
`Set.size(s) |> io.print()`. Prefer the stacked form once the chain is doing
real pipeline work, when a lambda makes a stage multi-line, or when starting
from the subject makes the data flow easier to scan. `fmt` stacks every
pipeline that has a lambda stage: a lambda stage's body ends at the next `|>`,
and a line per stage shows where.

**Lift a composite-literal seed to its own stage.** When the innermost subject
is a composite literal (a list, map, set, tuple or struct literal), give it its
own seed stage so the pipeline visibly starts from the data:

```nomi
// no: the data is buried inside the first call
Iter.to_set([1, 2, 3])
|> Set.size()
|> io.print()

// yes: the list literal seeds the chain
[1, 2, 3]
|> Iter.to_set()
|> Set.size()
|> io.print()
```

**Don't pipe when there's no nested call:**

- A plain value or literal argument stays a direct call: `Int.to_string(n)`,
  `Iter.reduce(s, f)`. A lone call is never a pipe.
- A composite literal also stays direct when there is only one operation and
  the call is the whole expression: `Iter.reduce([1, 2, 3], |acc = 0, n| acc + n)`.
- An incidental, non-subject argument stays nested:
  `Map.put(m, k, compute(v))`. `compute(v)` is a value being stored, not the
  pipeline subject. Reach for `|> f(a, _)` placeholder placement only when the
  nested call is the subject.

**Operators and fields:**

- A `|>` chain can feed equality or ordering directly:
  `xs |> Iter.to_list() == [1, 2, 3]`.
- For arithmetic, concatenation and other expression-building operators, bind
  the pipeline first (`inner = xs |> Iter.map(Int.to_string) |> Iter.to_list()`),
  then build the expression from the binding.
- Pipes work in place inside struct and enum literal fields
  (`Foo{v: x |> g() |> h()}`), so don't extract a binding just to pipe a field
  value.

## 2. Blank lines between statements

`fmt` sets a statement that spans more than one line apart from its
neighbours: a blank line goes before it, unless it opens its block, and after
it, unless it closes its block. A block's final expression counts as a
statement. Whether a statement spans lines is what `fmt` renders at the
block's indent and the 100-column width, so a call that fits on one line in a
shallow block may break, and be set apart, in a deeper one. A `case`, a nested
block, a triple-quoted string and a struct literal you write across lines
always span lines. The rule holds
in every block: function, lambda, test and `case` arm bodies and `if`/`else`
branches.

`fmt` also puts a blank line after every pipe statement (a `|>` chain, inline
or stacked, bound or not) and after a block's imports. One-line statements sit
together; `fmt` keeps a single blank line you write between them, so use one to
mark a step. It removes a blank line at the top or bottom of a block:

```nomi
fn pad(generation: Int): String {
    text = "${generation}"
    width = String.length(text)

    if width >= 3 {
        text
    } else {
        String.repeat(" ", 3 - width) + text
    }
}
```

```nomi
s = #{1, 2, 3}
Set.size(s) |> io.print() // expect: 3

Set.contains?(s, 2) |> io.print() // expect: True

s
|> Iter.map(|x| x * 2)
|> Iter.to_list()
|> io.print() // expect: [2, 4, 6]
```

A short `if`/`else` whose branches are single expressions collapses to one line
when it fits.

`fmt` lays out the arms of a `case` together, in one of two ways. When every
arm fits on one line as `pattern -> body` and no arm's body is a block, the
arms sit on consecutive lines:

```nomi
fn label(n: Int): String {
    case n {
        0 -> "zero"
        n when n < 0 -> "negative"
        _ -> "regular"
    }
}
```

Otherwise the arms break. An arm whose body is a block keeps `-> {` on the
pattern's line, with its statements on the lines below, even when there is
only one. Every other arm puts its body on the next line, one indent in. One
blank line separates every two arms:

```nomi
fn describe(result: Result<Int, String>): String {
    case result {
        Ok(n) ->
            "${n}"

        Err(message) -> {
            io.print(message)
            "failed"
        }
    }
}
```

In both layouts `fmt` ignores the blank lines the source put between arms, and
a comment stays directly above its arm.

A multi-line call has a trailing comma after its last argument when it has
more than one argument, and none when it has one. `fmt` applies this.

**Declarations inside `impl` and `interface` blocks** follow the same rule as
top-level declarations: `fmt` puts a blank line between items that have bodies,
and one-line items (`host fn` declarations, `once` values, interface
requirements with no default body) may sit together as a tight group. A blank
line you write inside such a group is kept as a grouping boundary.

At top level, group related bodyless declarations tightly: `once`, `type`,
`typealias`, `host type`, `host fn`, and same-receiver `derive` runs. `fmt`
separates different declaration kinds with a blank line and keeps any blank
line you write within a kind, so use one only to mark a real grouping boundary:

```nomi
once max_retries = 3
once default_host = "localhost"

pub type Email String
pub type Coord (Int, Int)
pub type Handler (String, String) -> Result<String, String>
pub type Expired
pub opaque type Counter Int

pub typealias UserList List<User>
pub typealias UserMap Map<String, User>
```

Declarations with bodies get a blank line between them. A bodyless declaration
with a doc comment also stands apart, so the comment visibly belongs to that one
declaration.

## 3. Order declarations in a file

Order declarations by what helps the reader build the API model first. `fmt`
owns import layout; authors choose where declarations live.

The normal file shape is:

1. imports, when needed
2. file-level `once` values
3. public types, each followed by its `impl` block, derives and interface impls
4. file-level functions: entry points first, then helpers
5. standalone named tests

A type's own operations and constants go in an `impl Type { ... }` block right
after the type, and its derives and interface implementations follow that
block:

```nomi
pub struct RetryPolicy {
    attempts: Int
    delay: Duration
}

impl RetryPolicy {
    pub once default = RetryPolicy{
        attempts: 3,
        delay: Duration.seconds(1),
    }

    pub fn with_attempts(policy: RetryPolicy, attempts: Int): RetryPolicy {
        {..policy, attempts}
    }
}

derive Equatable for RetryPolicy

impl Display for RetryPolicy {
    fn to_string(policy: RetryPolicy): String {
        "${policy.attempts} attempts"
    }
}
```

Type bodies are shape only: structs hold fields, enums hold variants, and
opaque, host and distinct types declare shape. A type is not a namespace for
unrelated helpers. Choose a separate file when a name only groups functions;
choose a `type` when the name should also be a value type (for example a
zero-data capability that implements an interface, is stored in an application
field, or is passed to a function). Related value types are ordinary sibling
declarations (`Days`, `Months`).

This is a house style. Keep a different order when the local narrative is
clearer, for example a tiny helper type right next to the one function that
builds it.

Attached tests (`//!`) stay with the declaration they exercise, even if that
means a test appears before later functions. The ordering rule is for
independent declarations.

## 4. Naming

Types use `PascalCase`; functions, bindings and parameters use `snake_case`
(spec §4). Don't add needless name suffixes: no `_basic`, `_simple`, `_v2` or
`_new` on a file, type or function unless a real counterpart exists to
distinguish it from. A lone `foo_basic` with no `foo_advanced` is noise; call
it `foo`.

Nomi does not overload functions by argument type. When two operations would
want the same verb, put the distinction in the owner or the name.
`String.to_int(text)` and `Date.parse(text)` are fine because the owners
differ; within one owner, pick a name that describes the path, such as
`String.to_codepoints(text)`. The operator tokens `+`, `-`, `*` and `/` are
extensible only through their standard interfaces (`Add`, `Subtract`,
`Multiply`, `Divide`), with the right-hand and result types in the impl header:
`impl Add<Months, Date> for Date`.

Boolean-returning functions and predicate bindings may end in `?`, in the
Ruby/Scheme idiom: `empty?`, `valid?`, `Set.contains?`, `String.contains?`. `?` is
the only such marker; there is no `!` form. Reach for it when the name reads as
a yes/no question about its subject; a plain snake_case name is equally fine.

## 5. No `let`

Bindings are bare `name = expr` (optionally `name: T = expr`), never
`let name = …`. This applies to every binding shape: simple, tuple destructure
(`(a, b) = …`), struct destructure (`{a, b} = …`) and distinct destructure
(`Id(n) = …`). File-level values use `once name = …`.

Read binding-position `=` as match-and-bind, not assignment: the value on the
right is matched by the shape on the left, and any names the shape introduces
become immutable bindings. This is distinct from `=` in declaration defaults
(`port: Int = 8080`) and in application-field overrides
(`with App.logger = logger`), where the glyph is punctuation rather
than a match.

Use `if Pattern = expr` for a small, local refutable match where only the
success branch needs the destructured names:

```nomi
label = if Some(name) = maybe_name {
    name
} else {
    "unknown"
}
```

For more than one shape, or when exhaustiveness matters, use `case`.

Leave a binding's type off when inference settles it. That covers local
bindings, `once` values (`pub` ones included) and lambda parameters,
including an `Iter.reduce` seed:

```nomi
once upto = 400
once offsets = [(-1, 0), (1, 0), (0, -1), (0, 1)]

total = Iter.reduce(1..=upto, |sum = 0, n| sum + n)
lengths = Iter.reduce(words, |acc = Map.empty(), word| Map.put(acc, word, String.length(word)))
```

Write the type when nothing else pins the value down, such as an empty
`Map`, `List` or `None` that no later use fixes: the checker rejects it with
"type is not locally determined" until it has an annotation
(`once routes: Map<String, Handler> = Map.empty()`). Struct field defaults
(`port: Int = 8080`) are declarations, not bindings, and always carry their
type.

## 6. Call style

Nomi has functions, not methods: there is no value-dot `x.method()` call, and
`x.field` is only field access. Which qualifier a call takes depends on where
the function is declared:

- **A file-level function** is called bare in its own file and file-qualified
  from another: `validate(user)`, `users.validate(user)`, `io.print(x)`.
- **A function in an inherent `impl Type { ... }` block** is called
  owner-qualified: `String.trim(s)`, `Map.get(m, key)`, `List.head(xs)`.
  File-qualifying it (`strings.trim(s)`) is a compile error whose hint names
  the owner form.
- **An interface implementation function** is called type-qualified
  (`Int.to_string(n)`), interface-qualified (`Display.to_string(x)`) or through
  a bounded type parameter (`T.to_string(x)`). Use the interface qualifier when
  the contract is the useful thing to name, or when two interfaces share a
  function name on the same type. Bare dispatch (`to_string(n)`) and
  file-qualified dispatch (`int.to_string(n)`) are errors.

Inside an `impl` block, a sibling function of the same block may be called
bare:

```nomi
impl Speech for Dog {
    fn speak(d: Dog): String {
        "${d.name} says woof"
    }

    fn shout(d: Dog): String {
        speak(d) + "!"
    }
}
```

**`io.print` takes any `Display` value; don't pre-stringify.** It dispatches
`Display.to_string` itself (the same path `${...}` interpolation uses), so write
`x |> io.print()` or `io.print(x)`, not `x |> Int.to_string() |> io.print()`.
Two cases still stringify before printing: feeding `+` concatenation
(`io.print("n=" + Int.to_string(n))`, which needs a `String`), and a value whose
static type is the `Display` interface itself, where the interface-qualified
`Display.to_string(v)` is required. `io.inspect(x)` is the parallel shorthand
for the Debug rendering and returns `Unit`. Use `dbg` (§10) to inspect a value
without breaking an expression or pipeline.

**Don't write `derive Debug` on an ordinary type; Debug is automatic.** The
compiler synthesizes a structural Debug impl for every declared type, so
`io.inspect(x)` always works. Write a hand-written `impl Debug` only to
customize a type's rendering. `derive Debug` means something only on an
`opaque` type, where it replaces the name-only `<opaque T>` rendering with the
structural one. `Display` is the opposite, opt-in, so `derive Display` and a
hand-written `impl Display` stay meaningful.

**Signatures spell the concrete type.** In an interface declaration, `self`
appears only in type position, as the placeholder for the implementing type.
It is not a value, an implicit argument or a qualifier, and there is no
`self.some_fn(x)` form. An implementation writes the actual type (`String`,
`List<T>`, `Point`, `Result<T, E>`), and so does an inherent function:

```nomi
struct Stack<T> {
    items: List<T>
}

impl Stack<T> {
    pub fn peek<T>(stack: Stack<T>): Maybe<T> {
        List.head(stack.items)
    }

    pub fn push<T>(stack: Stack<T>, item: T): Stack<T> {
        Stack{items: [item, ..stack.items]}
    }
}

impl Display for Stack<T> {
    fn to_string(stack: Stack<T>): String {
        "stack of ${Iter.count(stack.items)}"
    }
}
```

So a contract function reads concretely across implementors: `String`'s
`Display` function is `fn to_string(s: String): String`, and `Int`'s is
`fn to_string(n: Int): String`. What differs between signatures is only ever a
different type: a different instantiation (`Iter.map` returns `Iter<U>`, not
`Iter<T>`), the element type (`List.head` returns `Maybe<T>`), or a plain other
type (`Comparable.compare` returns `Ordering`). See spec §13.

## 7. Doc comments and examples

Put a `///` doc comment on every `pub` declaration. Show usage with `//!`
attached tests between the doc comment and the declaration, ended by a bare
`//` line. `nomi test` runs them, and the generated stdlib reference renders
them as runnable examples. The stdlib does this throughout (`std/sets.nomi`,
`std/iter.nomi`):

```nomi
impl Set<T> {
    /// Returns a new set with `elem` added (no-op if already present).
    //! assert Set.insert(#{1, 2}, 3) == #{1, 2, 3}
    //! assert Set.insert(#{1, 2}, 2) == #{1, 2}
    //
    pub fn insert<T>(s: Set<T>, elem: T): Set<T> {
        Set{items: Map.put(s.items, elem, True)}
    }
}
```

Keep a fenced ` ```nomi ` block inside `///` for code that is not an assertion,
such as how to implement an interface (`Iter`'s doc comment shows a custom
`each_while`). Nothing checks those blocks, so prefer `//!` whenever an
assertion can show the point.

## 8. Tests

Prefer `_test.nomi` files with explicit `test "..." { ... }` blocks for
behavior checks. A test should read like ordinary Nomi code with a small number
of direct assertions:

```nomi
test "result values can be asserted directly" {
    assert parse_id("42") == Ok(42)
    assert parse_id("nope") == Err("expected a numeric id")
}
```

Use unique full test names within a file. The enclosing `tests` group is part
of the full name, so repeated leaf names are fine in different groups.

Use attached tests (§7) for compact examples that belong to one declaration,
especially stdlib reference examples. Keep broader workflows and multi-case
behavior in named `test` / `tests` blocks.

When an attached assertion's subject becomes multi-line, keep the assertion at
the front and prompt each continuation line:

```nomi
//! assert Iter.loop(|n = 0|
//!     if n >= 3 {
//!         break n
//!     } else {
//!         n + 1
//!     }
//! ) == 3
```

When a compact example needs setup, write the setup as ordinary prompted lines:

```nomi
//! v: Vector<Int> = Vector.empty()
//! assert Vector.length(v) == 0
```

`fmt` applies the same layout in ordinary code: a single-expression lambda
whose body is an `if` containing `break`, `continue` or `return` breaks after
the lambda header. A value-producing conditional lambda stays inline when it
fits.

Use `assert` for the positive shape and `refute` when the thing under test is
already a predicate. Don't wrap simple value checks in `io.inspect` or string
interpolation to produce output; assert the value itself. Don't introduce a
pipeline for a simple call-and-compare; direct equality is clearer:

```nomi
assert Decimal.from_string("1.50") == Some(1.50d)
```

When the value under test comes from a pipeline and the intermediate meaning is
useful, bind the pipeline result and assert the binding. The failure report
then shows a named expression:

```nomi
found =
    [1, 2, 3, 4, 5]
    |> Iter.find(|x| x == 3)

assert found == Some(3)
```

For one-shot checks where a binding would only name the plumbing, let the
pipeline compute the assertion subject and put `assert` or `refute` at the
head. Prefer an existing predicate or equality function (`String.contains?`,
`String.equal?`, `Result.ok?`) over a throwaway lambda. When a predicate has
semantic weight or several conditions, give it a `?`-suffixed name and assert
that instead; keep lambda stages for tiny local transformations. Indent the
stages under the asserted operand, which distinguishes it from a statement-level
pipeline:

```nomi
assert "  ADA LOVELACE  "
    |> String.trim()
    |> String.to_lower()
    |> String.equal?("ada lovelace")

assert values
    |> Iter.map(normalize)
    |> Iter.all?(valid?)
```

**Boot with no startup input when the tests need none.** A boot that reads no
startup input declares no parameter, `fn boot(): App`, and its groups write
`boot server.boot()`. A boot that reads startup takes
`startup: Startup`. When most of its tests need no input, default the
parameter, `fn boot(startup: Startup = Startup{}): App`, so those groups
write `boot server.boot()` and only the groups that test the inputs build a
Startup. The runtime still passes the real Startup to `main`'s boot, so the
default changes nothing outside tests. Leave the parameter required when every
test must decide its inputs, such as a boot that fails without a database URL.

**Groups that boot share one helper file.** Put the fakes a project's tests
install, and the `Startup` they boot with when they need one, in a test helper
file such as `test_support.nomi`. It exports the fake types and, when tests
need startup input, `pub fn startup(): Startup`. A group passes
`test_support.startup()` to its `boot` line and installs fakes with `with`
lines in `setup`, so every test in the group runs against them. Build the
Startup with only the fields it sets; the rest default to empty:

```nomi
// test_support.nomi
import server.Mailer

pub fn startup(): Startup {
    Startup{env: {"SENDER" => "test@example.com"}}
}

pub type FakeMailer

impl Mailer for FakeMailer {
    fn send(_mailer: FakeMailer, to: String): Result<Unit, String> {
        if to == "" {
            Err("no address")
        } else {
            Ok(Unit)
        }
    }
}
```

```nomi
// signup_test.nomi
import {
    server
    server.App
    test_support
    test_support.FakeMailer
}

tests "signup" {
    boot server.boot(test_support.startup())

    setup {
        with App.mailer = FakeMailer
        "ada@example.com"
    }

    test "sends a welcome", to {
        assert server.welcome(to)
    }
}
```

A test that needs a different startup builds it from the shared one:
`{..test_support.startup(), args: ["--dry-run"]}`. Groups do not nest; write
sibling groups, and share setup between them through a function.

**A group's lines may be written in any order.** `clock`, `boot` and `setup`
always run in that order, before each test's body, wherever they sit in the
group. `nomi fmt` places them first, in that order, so add a `setup` below an
existing test and let the formatter move it.

**Keep tests focused.** A test name should carry the intent, so avoid comments
that restate the next line. Split unrelated rules into separate tests instead
of collecting a tour of a feature in one block.

**Compiler diagnostics are tested as values.** Use `compiler.check` with a
`"""..."""` literal and assert on the diagnostics. If a fixture needs helper
functions, keep them in a file beside it in the same test-program directory.
Keep the embedded program focused on the rejected shape; when a bad expression
only needs to be referenced, prefer `dbg bad_expression` over importing
`std/io` to call `io.inspect`:

```nomi
import {
    std/compiler
    test_helpers
}

test "missing members are rejected" {
    diagnostics = compiler.check(
        """
            fn main() {
                dbg List.empty()
            }
        """
    )

    assert test_helpers.has_diagnostic?(diagnostics, "type 'List' has no member 'empty'")
}
```

**Program output is tested through `compiler.run`.** Use it when stdout is the
behavior under test, such as `dbg` rendering. It returns the captured output:

```nomi
test "debug output is rendered" {
    result = compiler.run(
        """
        fn main() {
          _ = dbg 41 + 1
        }
        """
    )

    assert result == Ok(
        """
        dbg line 2: 41 + 1 = 42

        """
    )
}
```

**Hover text is tested through `compiler.hover`.** Use a hover test when the
hover text is the behavior under test: inferred signatures, doc rendering,
import visibility. Don't add type-describing comments to ordinary value tests;
assert the value, then add one focused hover test if the signature is part of
the contract. Put the marker `ˇ` inside the name whose hover is under test:

```nomi
test "once hover shows its inferred type" {
    signature = test_helpers.hover_signature(
        """
          once defaultˇ_port = 8080

          fn main() {
            Unit
          }
        """
    )

    assert signature == "once default_port: Int = 8080"
}
```

**Use `// expect:` only for intentional output examples.** Trailing
`// expect:` comments belong in runnable examples where printed output is the
behavior being shown, one expected line per comment. They are not the way to
test ordinary values.

**Tests live in numbered category folders**, `tests/NN-category/`,
discovered recursively by `nomi test` (see `tests/README.md`). A
single-file test is usually `NN-category/<name>_test.nomi`; supporting files
are plain `.nomi` files imported by those tests. Drop a new test in the right
category; no harness wiring is needed.

**A test of a build-time failure** stays a passing test that asserts
diagnostics with `compiler.check` or `compiler.check_project`. Runtime failure
behavior uses `compiler.run` or a domain API that returns an inspectable value.

**A test that exercises a syntactic form keeps that form**, even when a rule
here would rewrite it. The pipe tests in `tests/13-iterators-and-pipes/`
keep value-seeded and single-stage pipes because the pipe is what they test.
Apply §1 only where the pipe is incidental.

## 9. Imports

**`fmt` owns import layout.** It combines a file's or block's imports into one
`import { … }` block sorted with `std/` paths first, whatever blank lines sit
between them, and leaves a lone import bare. A comment inside an `import { … }`
block is kept in place; a comment line between two separate `import`
statements keeps them apart.

```nomi
import {
    std/io
    std/lists.List
    myapp/config.Config
    myapp/db.Db
}
```

Several names from one path go in one selector list,
`import std/calendar.{Date, Months}`; `fmt` merges separate selections of the
same path into that form.

Use `self` only when you need both an owner (a file or an enum type) and
selected children from it. `self` inside an owner selector binds the owner as
well as the named items, so one line replaces two:

```nomi
// no
import {
    shape.Shape
    shape.Shape.{Circle, Rectangle}
}

// yes
import shape.Shape.{self, Circle, Rectangle}
```

If you need only the owner or only one member, don't add `self` for its own
sake: `import shape.Shape` and `import shape.Shape.Circle` stay as they are.

When a line-level `export` applies to every name selected from a path, put it
once after the selector list:

```nomi
// no: valid, but says export twice
import std/comparable.{Comparable export, Ordering export}

// yes
import std/comparable.{Comparable, Ordering} export
```

Use per-item `export` when only some selected names are re-exported or an item
needs its own alias: `import calc.{add export, helper, subtract export as minus}`.

Don't write imports you don't use; the compiler rejects each unused imported
item, module, alias or `self` marker (spec §14). Dot-shorthand patterns
(`.Some(v)`) don't reference imported variant names, so don't import variants
you only match with the dot form.

An import inside a block sits at the top of that block, followed by a blank
line; `fmt` moves it there:

```nomi
fn first(): Maybe<Int> {
    import std/maybe.Maybe as Local

    Local.Some(1)
}
```

## 10. Tracing with `dbg`

`dbg expr` prints the source line, the expression and its Debug rendering, then
returns the value:

```nomi
total = dbg subtotal + tax
```

In a pipeline, use `dbg` as a transparent stage:

```nomi
total =
    items
    |> Iter.map(price)
    |> Iter.reduce(|sum = 0, value| sum + value)
    |> dbg
```

Keep `io.inspect(value)` for intentional program output where a Debug rendering
is part of the program's behavior. Reach for `dbg` for temporary tracing.

**Keyword stages.** `try` can prefix the fallible stage it unwraps:

```nomi
user =
    id
    |> try User.get()
```

`if` and `case` can consume a piped value directly, or prefix the stage that
produces their subject:

```nomi
status
|> if Status.ready?() { "ready" } else { "blocked" }

input
|> case parse_id() {
        Ok(id) -> id
        Err(_) -> 0
    }
```

## 11. Thread one binding instead of inventing name ladders

Same-scope shadowing lets you refine a value in place rather than inventing
suffixed names:

```nomi
// no: invented ladder
fn normalize(name: String): String {
    name_trimmed = String.trim(name)
    name_lower = String.to_lower(name_trimmed)
    name_lower
}

// yes: thread the binding
fn normalize(name: String): String {
    name = String.trim(name)
    name = String.to_lower(name)
    name
}
```

**A rebinding that discards the old value is a bug.** If the shadowed value is
never read before the new binding takes over, the compiler reports it as a
binding that is never read:

```nomi
fn greet() {
    name = "jane"
    name = "john"
    io.print(name)
}
```

The threading idiom (`x = f(x)`) always reads the prior `x`, so it is clean.

When a value is intentionally ignored, use a discard binder: `_`, or `_name`
when a label makes the ignored value clearer. Discard binders do not enter
scope, so `_name` cannot be read later.

`assert` and `refute` may stand alone as statements, since the check is the
reason the expression is there. Use a pattern assertion when a payload matters:

```nomi
assert parse_id("42")

assert Err(message) = parse_id("wat")

assert String.contains?(message, "numeric")
```

Inside tests, use `try` for fallible setup when the happy-path value is what
the rest of the test needs. Use a pattern assertion when the returned shape is
itself under test:

```nomi
t = try Time.parse("09:30:00")

assert Time.to_string(t) == "09:30:00"

assert Err(Error.InvalidFormat(_)) = Time.parse("bad")
```

## 12. Reach for `DateTime`, not `NaiveDateTime`, for real timestamps

`std/calendar` gives the bare name to the safe default: `DateTime` is zoned by
an IANA time zone, DST-aware, and equal only to the same instant.
`NaiveDateTime` carries its prefix as a warning. It is a wall-clock reading
without a zone, not a moment in history.

```nomi
// yes: a real moment, anchored
appointment = try DateTime"2026-06-15T14:30:00-04:00[America/New_York]"

// only for wall time without a zone, which is rare
template_time = try NaiveDateTime"2026-06-15T14:30:00"
```

If you find yourself writing `NaiveDateTime"…"`, check whether you want a
`DateTime"…"` with a zone. The naive type fits recurring schedule templates,
form input before zone resolution, and naturally zone-less data such as
birthdays. It does not fit "the meeting is at 2:30pm": meetings happen in time
zones.

## 13. Keep shape, operations and interface impls in separate declarations

Type declarations own shape. Struct bodies hold only `name: Type` fields, one
per line and optionally with a default; enum bodies hold only variants; and
`type` / `host type` declarations describe an opaque, distinct or host-backed
value. A type's operations live in an inherent `impl Type { ... }` block beside
it. Interface implementations use top-level `impl Iface for Type { ... }`
blocks, whether the type is local, from another file, generic or constrained.
The header names one interface, so the body holds only that interface's
functions.

```nomi
struct Dog {
    name: String
}

impl Dog {
    fn rename(_d: Dog, new_name: String): Dog {
        Dog{name: new_name}
    }
}

impl Speech for Dog {
    fn speak(d: Dog): String {
        "${d.name} says woof"
    }
}

impl Html for users.User {
    fn to_html(u: users.User): String {
        "<span>${u.name}</span>"
    }
}
```

For a generic type, put the constraints that always hold on the type
declaration. Use a function-level or impl-level `where` for extra constraints
that apply only there:

```nomi
struct Span<T> where T: Comparable {
    start: T
    stop: T
}

impl Span<T> {
    fn longer?(a: Span<T>, b: Span<T>): Bool {
        a.stop > b.stop
    }
}
```

Interface functions follow the same rule for their own type parameters: declare
the parameter in the function header, then constrain it with `where`. A bare
PascalCase name does not create an implicit type parameter.

```nomi
interface Ranked<T> {
    fn item(value: self): T
    fn prefer<K>(value: self, lhs: K, rhs: K): Bool where K: Comparable
}
```

A conformance with nothing to implement (an interface of field requirements or
defaults only) is a bodyless `impl Iface for Type` declaration. Bodiless type
declarations (`pub host type Unit`, `type Expired`) stay braceless too.

## 14. Write one derive declaration per interface

Structural derivation is requested with `derive Iface for Type`. Write one
derive declaration per interface and cluster consecutive derives for the same
type without blank lines (`fmt` removes blank lines inside such a run). Put
derive options after `with`, and a `where` clause after the options.

```nomi
type Off

derive Comparable for Off
derive Equatable for Off
derive Hashable for Off

struct Box<T> {
    value: T
}

derive Display for Box<T> where T: Display

struct Account {
    first_name: String
    age: Int
}

derive ToJson for Account with ToJson.Options{rename_all: Json.Case.Camel}
derive FromJson for Account with FromJson.Options{rename_all: Json.Case.Camel}
```

A `derive` must sit in the same file and scope as the type's declaration, since
it needs the declaration's fields or variants; a derive for a type from another
file is rejected. `Debug` never needs a derive on a non-opaque type (§6).

## 15. Boot builds the application value

`boot` lives in the entry file, beside `main`, and takes no parameter or one
`Startup`. Give the application struct at most one `Context` field, and build
it in boot from `Context.root()`, or derive a tighter one there:

```nomi
pub struct Config {
    port: Int = 8080
    context: Context
}

pub fn boot(): Config {
    Config{context: Context.root()}
}
```

Make `boot` and the struct it returns `pub` when tests name the boot.

Name the application type freely, and read its fields by naming it:
`Config.port` and `Config.context`. A differently named Context field, such as
`execution`, is read as `Config.execution`. A file that reads a field imports
the type as it would any other.

Put `with` lines at the top of the block they govern where practical, so the
reader sees the replaced fields before the code that runs under them. Write one
`with` line per field:

```nomi
fn import_quietly() {
    with Config.logger = Silent
    with Config.store = FakeStore{}

    run_import()
}
```

A `with` lasts to the end of its block. When only part of a function should
run under it, move that part into a helper function and put the `with` lines at
the top of the helper. Use a bare block statement only when a helper would be
ceremony.

Startup environment and arguments are explicit inputs. Configuration helpers
that build the whole application receive `startup` explicitly. `defer` in
`boot` itself runs at application shutdown; `defer` in a helper runs when that
helper returns.

## 16. Pick the tool by what happens on failure

Four constructs take a value apart when it might not have the shape you want.
Choose by what the failure path does:

- **`try`** passes the error up unchanged. Use it when the caller should see
  this step's error as it is.
- **A binding `else`** (`Pattern = value else { ... }`) handles or replaces
  one step's failure in place: it returns a different error, skips a loop
  item, or supplies a fallback, and the success path continues unindented.
- **`case`** when both outcomes continue with work of their own.
- **`if Pattern = expr`** when only the success branch needs the names and a
  miss does nothing.

```nomi
// try: the caller gets load's error as it is
user = try load(id)

// else: the error is replaced with one that says which user
Ok(user) = load(id) else {
    Err(.NotFound) -> return Err("no user ${id}")
    Err(e) -> return Err("loading user ${id}: ${e}")
}

// else: a fallback stands in for the missing value
Some(email) = user.email else { "none" }

// else: skip what does not parse
Iter.each(lines, |line| {
    Ok(entry) = parse(line) else { continue }
    record(entry)
})

// case: both outcomes do work
case load(id) {
    Ok(user) -> greet(user)
    Err(e) -> log_failure(e)
}

// if: only a match does anything
if Some(email) = user.email {
    send(email)
}
```

Prefer a binding `else` to a `case` whose failure arm only leaves: the
`case` would nest the whole success path one level in. Prefer `try` to an
`else` whose arm returns the same error unchanged
(`Err(e) -> return Err(e)`).

## 17. Leave out a struct literal's type name where the context names it

A brace literal builds the struct its position expects (spec, *Target-typed
construction*), so where an annotation, a parameter, a field or a return type
already names the struct, write the fields alone:

```nomi
ada = Person{name: "Ada", address: {street: "1 Main", city: "Bath"}}

fn origin(): Point {
    {x: 0, y: 0}
}
```

Write the type name anyway when a reader cannot see the expected type from
the literal's own line: an argument to a function defined far away, an
element deep inside a nested call, or a literal whose struct shares field
names with another struct the reader might expect. `Address{...}` at the top
of an expression also says what is being built before the fields do, so keep
it on an unannotated binding (`home = Address{...}`), where leaving it out
would make an anonymous struct instead.

## 18. Use a tuple for a local pairing and a struct once it travels

Use a tuple for a small, local pairing whose positions are obvious: a function
returning two values, a map entry. Switch to an anonymous struct (or a
struct) once a slot is updated, or read by position far from where the tuple
was built. A named field reads better than `.1`, and an anonymous struct
already supports a spread update, which a tuple does not: there is no
`Tuple.update`, because its result type would depend on a literal index.

```nomi
fn divide(x: Int, y: Int): (Int, Int) {
    (x / y, x % y)
}

fn step(point: {x: Int, y: Int}): {x: Int, y: Int} {
    {..point, y: point.y + 1}
}
```

## 19. Use a codepoint literal for codepoint work and a string for text

A codepoint literal (`'a'`) is a `Codepoint`, not a character of text. Reach
for it when the code works at the codepoint level: enumerating ASCII
(`'a'..='z'`), scanning `String.to_codepoints` output, or matching delimiters
in a `case`. Write `'a'` rather than `try Codepoint.from_int(97)`: the literal
is infallible and says which character it is.

```nomi
fn delimiter?(cp: Codepoint): Bool {
    case cp {
        ',' -> True
        ';' -> True
        '\t' -> True
        _ -> False
    }
}
```

Keep text in strings, a one-character one included: `String.starts_with?(s,
"#")`, not a comparison against `'#'`, and `"é"`, which has no codepoint
literal at all. Converting back and forth (`Codepoint.to_string('a')`) is a
sign the value wanted to be a string.

## 20. Stub unwritten code with `todo`

When a signature is settled and its body is not, write `todo` with a reason
rather than a value that happens to type-check:

```nomi
fn parse(text: String): Header {
    todo "parse the header"
}
```

A made-up `Header{name: "", size: 0}` runs and returns a wrong answer that
nothing flags; `todo` stops the program at the line that is missing, the editor
lists it as a warning, and `nomi build` refuses to ship it. Give a reason when
the next step is not obvious from the signature, and keep it to what is left to
do. Use `todo` while writing code, not as a permanent "unreachable" marker: a
case that cannot happen is better shaped out of the types, and one that can
happen deserves a real answer or an `Err`.

## 21. Read a field with `.name`, not `|x| x.name`

Where a function only reads a field, pass the field accessor (spec, *Field
Accessors*). It says the one thing the lambda says, without a parameter name
to invent:

```nomi
names = users |> Iter.map(.name) |> Iter.to_list()
youngest_first = Iter.sort_by(users, .age)
cities = users |> Iter.map(.address.city) |> Iter.to_list()
```

Keep the lambda when its body does more than read fields: a computation
(`|u| u.age + 1`), a call (`|u| String.to_upper(u.name)`), or a condition
(`|u| u.age > 18`). Don't pipe into an accessor; read the field from the
value: `user.name`, not `user |> .name`, which is an error.
