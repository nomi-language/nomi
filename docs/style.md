# Writing Idiomatic Nomi

Style guidelines for hand-written Nomi (`.nomi`): examples, the stdlib
(`std/`) and `tests/`. Each rule says which of several legal forms to choose
and why. `nomi fmt` owns layout (§1), and the compiler owns what is legal.

The [specification](spec.md) says what the language does. A reference
written `spec §N` names one of its sections; a bare `§N` names a rule of this
guide. A rule that names a feature links the spec section that defines it
and, where one teaches it, the tour chapter.

This guide is the canonical list of Nomi's conventions: a new one is added
here, not scattered across other docs.

---

## Files and layout

### 1. Let `nomi fmt` own the layout

Run `nomi fmt -w` after editing a `.nomi` file, or let the editor format on
save through `nomi-lsp`. Don't line code up by hand or fight a break `fmt`
chose. What it does is in the
[tooling reference](/tooling/formatting/); the
choices it leaves to you are these:

- **A pipe inline or stacked** (§7). `fmt` keeps the form you wrote.
- **A blank line between one-line statements** to mark a step. `fmt` keeps
  one; it sets multi-line statements apart on its own.
- **A literal broken after its opening bracket** stays one element per line
  even when it would fit. Delete that one line break to let `fmt` join it.
- **A lambda body below its `->`**, closed by `end`, stays there. Write
  `fn x -> x + 1` to let `fmt` put a short body on the header's line.
- **A blank line within a run of one-line declarations** (`once`, `type`,
  `host fn`) marks a group. Use one only at a real boundary.

### 2. Order declarations in a file

Order declarations by what helps the reader build the API model first.

The normal file shape is:

1. imports, when needed
2. file-level `once` values
3. public types, each followed by its `impl` block, derives and interface impls
4. file-level functions: entry points first, then helpers
5. standalone named tests

A type's own operations and constants go in an `impl Type` block right
after the type, and its derives and interface implementations follow that
block. An impl for a type from another file sits with the code that needs it:

```nomi
pub struct RetryPolicy
    attempts: Int
    delay: Duration
end

impl RetryPolicy
    pub once default = RetryPolicy{
        attempts: 3,
        delay: Duration.seconds(1),
    }

    pub fn with_attempts(policy: RetryPolicy, attempts: Int) -> RetryPolicy
        {..policy, attempts:}
    end
end

derive Equatable for RetryPolicy

impl Display for RetryPolicy
    fn to_string(policy: RetryPolicy) -> String
        "${policy.attempts} attempts"
    end
end
```

This is a house style. Keep a different order when the local narrative is
clearer, for example a tiny helper type right next to the one function that
builds it, or a `once` right above the one function that reads it.

Attached tests (`//!`) stay with the declaration they exercise, even if that
means a test appears before later functions. The ordering rule is for
independent declarations.

### 3. Imports

Import a file for its file-level functions and a type by name (spec §14
*Import forms*; tour: [Modules & Imports](/modules-and-imports/#import-syntax)).
`import std::io` and then `io.print(x)` keeps the file's name at each call, so
the reader sees where `print` comes from. `import std::calendar.Date` and then
`Date.parse(text)` names the type once, and every call through it says which
type it works on. Don't import a file only to write `calendar.Date` at every use.

```nomi
import
    std::calendar.{Date, Months}
    std::io
    myapp::config.Config
end
```

Several names from one path go in one selector list, as `std::calendar` above.

Use `self` only when you need both an owner (a file or an enum type) and
selected children from it. `self` inside an owner selector binds the owner as
well as the named items, so one line replaces two:

```nomi
// no
import
    shape.Shape
    shape.Shape.{Circle, Rectangle}
end

// yes
import shape.Shape.{self, Circle, Rectangle}
```

If you need only the owner or only one member, don't add `self` for its own
sake: `import shape.Shape` and `import shape.Shape.Circle` stay as they are.

A dot-shorthand pattern (`.Some(v)`) names no imported variant, so don't
import a variant you only match with the dot form.

Use a file by its short name, the last part of its path: `import db::posts`,
then `posts.find(1)`. Where the short name is ambiguous (two files share it)
or taken (a declaration has it), import the directory and write `dir::file`
at the use; alias the file only where a directory import does not help (spec
§14 *Short names*, *Directory imports*; tour: [Modules &
Imports](/modules-and-imports/#short-names-and-directory-imports)). The path
says which file at the use itself; an alias is one more name to look up.

```nomi
// no
import
    db::notifications as db_notifications
    jobs::notifications as job_notifications
end

// yes
import
    db
    jobs
end

fn pending() -> Int
    db::notifications.pending() + jobs::notifications.pending()
end
```

Import a directory to reach several files under it with one line:
`import views::admin`, then `admin::users.index(rows)` and
`admin::reports::daily.show()`. Don't import a directory to reach one file
whose short name is free; `import views::admin::users` and `users.index(rows)`
say the same thing more briefly.

## Names and bindings

### 4. Naming

The checker enforces case (spec §2 *Naming Conventions*); these rules cover
what it leaves to you. Don't add needless name suffixes: no `_basic`, `_simple`, `_v2` or
`_new` on a file, type or function unless a real counterpart exists to
distinguish it from. A lone `foo_basic` with no `foo_advanced` is noise; call
it `foo`.

Write an acronym as a word: `Json`, `Toml`, `Uuid`, `HttpClient`, `IoError`.

Split a file's name into words as any other value name is split:
`std::phrase_format`, `std::code_points`, `order_lines.nomi`, never
`orderlines`. A name ends in `_test` only when the
file holds tests, since `_test.nomi` is what marks a test file; a module that
helps tests run is named for what it holds, as `std::sql_testing` holds the
fake `Conn` and the conformance suite.

Don't repeat the file's name in a type the file is about. A module with one
error type calls it `Error`, and code outside writes it with the file's name:
`uuid.Error`, `random.Error`, `locale.Error`. Two modules' errors then never
collide, and `uuid.Error` says no less than `UuidError`. A module with several
error types names each for what failed: `Json.DecodeError`,
`NumberParseError`.

```nomi
import std::uuid.Uuid

fn read_id(text: String) -> Result<Uuid, uuid.Error>
    Uuid.parse(text)
end
```

Nomi does not overload functions by argument type (spec §7 *No Overloaded
Functions*). When two operations would
want the same verb, put the distinction in the owner or the name.
`String.to_int(text)` and `Date.parse(text)` are fine because the owners
differ; within one owner, pick a name that describes the path, such as
`String.to_codepoints(text)`.

Name a function, binding or field that returns a `Bool` for what it asks. A
state or an adjective takes `is_` or `has_`: `is_empty`, `is_valid`,
`is_leap_year`, `has_children`. A verb stays bare: `Set.contains`,
`String.starts_with`, `Iter.any_match`, `path.matches`. A `?` after an expression
propagates a failure (§29), so it is no part of a name.

### 5. Leave a binding's type off when inference settles it

That covers local bindings, `once` values (`pub` ones included) and lambda
parameters, including an `Iter.reduce` seed (spec §5 *Annotations and local
inference*; tour: [Bindings & Expressions](/bindings-and-expressions/#how-far-type-inference-reaches)):

```nomi
once upto = 400
once offsets = [(-1, 0), (1, 0), (0, -1), (0, 1)]

total = Iter.reduce(1..=upto, 0, fn sum, n -> sum + n)

lengths = Iter.reduce(words, {}, fn acc, word -> Map.put(acc, word, String.length(word)))
```

Write the type when nothing else pins the value down, such as an empty
literal (`[]`, `Vector[]`, `Set[]`, `{}`) or a `None` that no later use fixes: the
checker rejects it with "type is not locally determined" until it has an
annotation (`once routes: Map<String, Handler> = {}`,
`seen: Set<String> = []`).

A struct field or a parameter with a default follows the same rule (spec §4
*Field types from defaults*, §7 *Parameter types from defaults*): `misses =
0`, ``home = Route`GET /admin/{$}` `` and `mode = WriteMode.Replace` need no
type. Keep the type on a public field or parameter whose default is a call,
such as `limit: Int = default_limit()`: a reader of the declaration then
sees the type without looking up the function.

### 6. Thread one binding instead of inventing name ladders

Same-scope shadowing (spec §6 *Shadowing and redeclaration*; tour:
[Bindings & Expressions](/bindings-and-expressions/#bindings)) lets you refine a
value in place rather than inventing suffixed names:

```nomi
// no: invented ladder
fn normalize(name: String) -> String
    name_trimmed = String.trim(name)
    name_lower = String.lowercase(name_trimmed)
    name_lower
end

// yes: thread the binding
fn normalize(name: String) -> String
    name = String.trim(name)
    name = String.lowercase(name)
    name
end
```

Each step reads the value before it, so the compiler's check for a binding
that is never read still catches a step that throws one away.

When a value is intentionally ignored, use a discard name (spec §2 *Discard
names*): `_`, or `_name` when a label makes the ignored value clearer. The
same holds inside a pattern: write
`fn (_word, count) -> count`, `Some(_) -> True` or `[first, ..]`, and leave a struct
pattern's unneeded fields out (`.Suspended{reason:}`) rather than binding them.

A parameter the body ignores is written as its type alone when the type
says all a reader needs: `fn health(Request) -> Response`, not
`fn health(_request: Request) -> Response` (spec §7 *Parameter
Destructuring*). Keep a discard name, `_retries: Int`, when the name says
what the type does not.

## Calls and functions

### 7. Pipe nested calls

When a function call's result is an argument to another function call, write it
as a `|>` pipe chain instead of nesting (spec §8 *Pipes*; tour:
[Pipes](/pipes/#the-pipe-operator)):

```nomi
// no
Iter.to_set(Iter.map(s, f))

// yes
s
|> Iter.map(f)
|> Iter.to_set()
```

A constructor is a function call like any other, so a constructor whose
argument is another call is a nested call too:

```nomi
// no
MeteredReader.new(Reader.from_text("hello"))

// yes
"hello" |> Reader.from_text() |> MeteredReader.new()
```

The same goes for `Seed.from_int(42) |> Factory.new()` and
`Reactor.new() |> Reactor.input("input", 1)`.

**Every stage is a call**, so a stage keeps its parentheses even when
nothing else goes in them: `|> Iter.to_list()`, `|> io.print()`. Reach for
`|> then fn v -> ...` for a small step no named function covers; once the
step has a name worth giving, write the function and call it.

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
from the subject makes the data flow easier to scan.

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
  `Iter.count(s)`. A lone call is never a pipe.
- A composite literal also stays direct when there is only one operation and
  the call is the whole expression: `Iter.reduce([1, 2, 3], 0, fn acc, n -> acc + n)`.
- A nested call that is not the first argument stays nested:
  `Map.put(m, k, compute(v))`, `Cipher.decode(cipher, Cipher.encode(cipher, plaintext))`.
  `compute(v)` is a value being stored, not the pipeline subject. Reach for
  `|> then &f(a, &1)` only when the nested call is the subject and its value
  goes elsewhere than the first argument.
- A recursive call with several arguments may keep a nested first argument
  too, so that its arguments line up with the parameters they replace:
  `search(Iter.to_list(next), Set.union(seen, next), moves + 1, sizes, goal)`.
- A short nested call inside a literal stays nested:
  `(Reverse.of(Standing.points(s)), s.team)`, not
  `(Standing.points(s) |> Reverse.of(), s.team)`. A pipe reads well as a
  literal's whole field value, not as one short element among others.

**Operators and fields:**

- A `|>` chain can feed equality or ordering directly:
  `xs |> Iter.to_list() == [1, 2, 3]`.
- A `|>` chain can be an operand of `and` or `or` directly:
  `Iter.is_empty(run) or Iter.windows(list, Iter.count(run)) |> Iter.any_match(&(&1 == run))`.
  Don't bind it first: a binding runs the chain even when the other operand
  already decides the result, and the operator would not.
- For arithmetic, concatenation and other expression-building operators, bind
  the pipeline first (`inner = xs |> Iter.map(Int.to_string) |> Iter.to_list()`),
  then build the expression from the binding.
- Pipes work in place inside struct and enum literal fields
  (`Foo{v: x |> g() |> h()}`), so don't extract a binding just to pipe a field
  value.

**End a fallible stage with `?`.** Write the `?` right after the stage whose
failure it passes up, so each stage reads as one step:

```nomi
order =
    text
    |> parse()?
    |> validate()?
```

When a `then` lambda's result is what can fail, write `then?`. A `?` at the
end of the lambda's body exits the lambda, not the function:

```nomi
port =
    text
    |> String.trim()
    |> then? fn t -> parse_port(t, config)
```

### 8. Tracing with `dbg`

Reach for `dbg` (spec §8 *`dbg`*; tour: [Pipes](/pipes/#other-stages)) for
temporary tracing. It returns its value, so it goes inside an expression
(`total = dbg subtotal + tax`) or as a pipe stage (`|> dbg`) without
restructuring the code. Keep `io.inspect(value)` for intentional program
output where a Debug rendering is part of the program's behavior.

**`dbg`, `tap` or `then`.** The three stages serve different needs (spec §8
*The `tap` stage*):

- `|> dbg` is a temporary look at the value. It is a warning in the editor and
  `nomi build` refuses it, so it never ships.
- `|> tap fn v -> ...` is an effect the program keeps, such as a log line or a
  progress message, run on the value without changing it. Its lambda returns
  `Unit`. A function that takes the value as it is goes by name, `|> tap
  log_line`, not `|> tap &log_line(&1)`.
- `|> then fn v -> ...` replaces the value with the lambda's result.

```nomi
command =
    input
    |> String.words()
    |> tap fn words -> log.debug("got ${Iter.count(words)} words")
    |> parse_command()
```

Do not use `tap` to hide work that computes something: if the lambda's result
matters, it is a `then` stage or a named function.

### 9. Call style

Every call is qualified (spec §8 *Calls and qualification*; tour:
[Interfaces & Dispatch](/interfaces-and-dispatch/#qualified-call-shapes)).
An interface function can be called through its type, its interface or a
bounded type parameter (`Int.to_string(n)`, `Display.to_string(x)`,
`T.to_string(x)`); pick the qualifier that says the most. When the value's
type is known, call through the type: `Date.year(on)`, not
`DateParts.year(on)`. Name the interface when the contract is the useful
thing to name, or when two interfaces share a function name on the same
type.

Inside an `impl` block, call a sibling function of the same block bare:

```nomi
impl Speech for Dog
    fn speak(d: Dog) -> String
        "${d.name} says woof"
    end

    fn shout(d: Dog) -> String
        speak(d) + "!"
    end
end
```

**`io.print` takes any `Display` value; don't pre-stringify.** It dispatches
`Display.to_string` itself (the same path `${...}` interpolation uses), so write
`x |> io.print()` or `io.print(x)`, not `x |> Int.to_string() |> io.print()`
and not `io.print("${x}")`, a string that is one interpolation and nothing
else. Interpolate when the line has text around the value (`"total: ${x}"`).
That includes a value whose static type is the `Display` interface itself:
`io.print(v)` and `"${v}"` render it through the value's own impl. The one
case that stringifies before printing is feeding `+` concatenation
(`io.print("n=" + Int.to_string(n))`, which needs a `String`). `io.inspect(x)`
is the parallel shorthand for the Debug rendering and returns `Unit`. Use
`dbg` (§8) to inspect a value without breaking an expression or pipeline.

### 10. Read a field with `.name`, not `fn x -> x.name`

Where a function only reads a field, pass the field accessor (spec §7 *Field
Accessors*; tour: [Structs, Enums & Distinct Types](/structs-enums-distinct/#field-accessors)).
It says the one thing the lambda says, without a parameter name
to invent:

```nomi
names = users |> Iter.map(.name) |> Iter.to_list()

youngest_first = Iter.sort_by(users, .age)

cities = users |> Iter.map(.address.city) |> Iter.to_list()
```

Keep the lambda when its body does more than read fields: a computation
(`fn u -> u.age + 1`), a call (`fn u -> String.uppercase(u.name)`), or a condition
(`fn u -> u.age > 18`). Don't pipe into an accessor; read the field from the
value: `user.name`, not `user |> .name`.

A tuple's items are read the same way, `.first` and `.second`, when the
tuple was built a line or two above, where the reader can see what each part
is:

```nomi
message |> Iter.zip(zigzag(rails)) |> Iter.sort_by(.second) |> Iter.map(.first)
```

When the tuple was built far from where it is read, such as in another
function or a `once`, take it apart in the lambda instead, so the names say
what the parts are: `Iter.map(roster, fn (name, _grade) -> name)`.

### 11. Pass a constructor, not `fn x -> Some(x)`

Where a function only wraps its argument, pass the constructor itself (spec
§7 *Constructors as Function Values*; tour:
[Structs, Enums & Distinct Types](/structs-enums-distinct/#enums)): a positional variant (`Some`, `Ok`,
`Shape.Circle`) or a distinct type that wraps a value (`Id` for
`type Id Int`). The lambda says the same thing with a parameter name to
invent:

```nomi
ids = rows |> Iter.map(.id) |> Iter.map(UserId) |> Iter.to_list()

somes: List<Maybe<Int>> = Iter.map(ns, Some) |> Iter.to_list()

labelled = Result.map_err(result, ParseError)
```

Keep the lambda when it does more than wrap: a computation inside the
constructor (`fn n -> Id(n + 1)`), a struct-shaped variant, which has no function
value (`fn w -> Shape.Rect{w:, h: w}`), or a positional variant over a tuple
whose parts arrive as separate arguments.

### 12. `return` is for leaving early

A block's last expression is its value, so a function or lambda ends with
the value, not `return value` (spec §7 *Returning*). Keep `return` for an exit
before the end:

```nomi
fn clamp(n: Int) -> Int
    if n < 0 then return 0
    if n > 100 then return 100
    n
end
```

The choice is where to leave early. A run of guards like these keeps the main path unindented,
where an `if`/`else` would nest it. A body you have not written yet gets
`todo`, not a bare `return` (§30).

## Types and values

### 13. Keep a type's impls beside it

A type's shape, its inherent `impl Type` block and each
`impl Iface for Type` are separate top-level declarations (spec §12
*Implementations*; tour:
[Interfaces & Dispatch](/interfaces-and-dispatch/#defining-and-implementing-an-interface)).
Put the impls of a type you declare right after it, where §2 places them, so
a reader finds everything the type can do in one place:

```nomi
struct Dog
    name: String
end

impl Dog
    fn rename(_d: Dog, new_name: String) -> Dog
        Dog{name: new_name}
    end
end

impl Speech for Dog
    fn speak(d: Dog) -> String
        "${d.name} says woof"
    end
end
```

A type is not a namespace for unrelated helpers. Choose a separate file when a
name only groups functions; choose a `type` when the name should also be a
value type (for example a zero-data capability that implements an interface,
is stored in an application field, or is passed to a function). Related value
types are ordinary sibling declarations (`Days`, `Months`).

For a generic type, put the constraints that always hold on the type
declaration. Use a function-level or impl-level `where` for extra constraints
that apply only there:

```nomi
struct Span<T> where T: Comparable
    start: T
    stop: T
end

impl Span<T>
    fn is_longer(a: Span<T>, b: Span<T>) -> Bool
        a.stop > b.stop
    end
end
```

An interface function with a type parameter of its own declares it in its
header and constrains it with `where`:

```nomi
interface Ranked<T>
    fn item(value: self) -> T
    fn prefer<K>(value: self, lhs: K, rhs: K) -> Bool where K: Comparable
end
```

A conformance with nothing to implement (a marker interface, or one with
defaults only) is the one-line `impl Iface for Type end`.

### 14. Write one derive declaration per interface

Write one `derive Iface for Type` declaration per interface (spec §12
*Derive*; tour: [Interfaces & Dispatch](/interfaces-and-dispatch/#deriving-impls)),
where §2 places them:

```nomi
type Off

derive Comparable for Off
derive Equatable for Off
derive Hashable for Off

struct Box<T>
    value: T
end

derive Display for Box<T> where T: Display

struct Account
    first_name: String
    age: Int
end

derive ToJson for Account with ToJson.Options{rename_all: Json.Case.Camel}
derive FromJson for Account with FromJson.Options{rename_all: Json.Case.Camel}
```

Every type already has a `Debug` (spec §12 *Display and Debug*), so write
`derive Debug` only on an `opaque` type, to show its structure in place of
`<opaque T>`.

### 15. List an enum's variants with `derive Variants`

When code needs every variant of an enum, derive `Variants` (spec §12
*Derivable interfaces*; tour:
[Interfaces & Dispatch](/interfaces-and-dispatch/#listing-an-enums-variants))
and call `variants()`, rather than writing the list out. The list follows the
declaration order, so a new variant joins it and the order lives in one place:

```nomi
pub enum Nucleotide
    A
    C
    G
    T
end

derive Equatable for Nucleotide
derive Hashable for Nucleotide
derive Variants for Nucleotide

fn no_counts() -> Map<Nucleotide, Int>
    Nucleotide.variants() |> Iter.map(&(&1, 0)) |> Iter.to_map()
end
```

not `{Nucleotide.A => 0, .C => 0, .G => 0, .T => 0}`, which a fifth variant
would leave behind.

Don't compute a variant's value from its position in that list. A value the
problem states for each variant is a `case` with an arm per variant:

```nomi
fn code(color: Color) -> Int
    case color
        .Black -> 0
        .Brown -> 1
        .Red -> 2
    end
end
```

not `Color.variants() |> Iter.find_index(&(&1 == color))`, a search whose
`None` the reader has to prove cannot happen, and whose results change when
someone reorders the enum.

### 16. Leave out a literal's type name where the context names it

A variant leaves out its enum with a dot where the position names the enum:
`Shape.Circle{radius:}` is `.Circle{radius:}`. A struct leaves out its name the
same way: `Point{x: 0}` is `{x: 0}`, which builds the struct its position
expects (spec §4 *Target-typed construction*; tour:
[Structs, Enums & Distinct Types](/structs-enums-distinct/#structs)). So where
an annotation, a parameter, a field or a return type already names the
struct, write `{...}` rather than `Point{...}`:

```nomi
ada = Person{name: "Ada", address: {street: "1 Main", city: "Bath"}}

fn origin() -> Point
    {x: 0, y: 0}
end
```

Write the type name anyway when a reader cannot see the expected type from
the literal's own line: an argument to a function defined far away, an
element deep inside a nested call, or a literal whose struct shares field
names with another struct the reader might expect. `Address{...}` at the
top of an expression also says what is being built before the fields do, so
keep it on an unannotated binding (`home = Address{...}`), where `{...}`
would make an anonymous struct instead.

The same holds for a variant. Where the expected type already names its enum
(a return type, a parameter, a `case` subject, the list's first element, a
constructor's payload), write `.Accounting`, not `CurrencySign.Accounting`,
and drop the enum's import once nothing else names it:

```nomi
fn sign(style: Style) -> CurrencySign
    case style
        .American -> .Accounting
        .Dutch -> .Standard
    end
end
```

A struct pattern works the same way. Where the value being matched already
has the struct's type (a `case` subject, the right side of a binding, a
typed parameter), write `{...}`, not `Order{...}`:

```nomi
fn describe(order: Order) -> String
    case order
        {status: .Paid, total:} when total > 100 -> "big order"
        {status: .Paid} -> "paid"
        {status: .Refunded} -> "refunded"
    end
end
```

Keep the name where it chooses the type: arms that match different structs
of an `embeds` enum or an interface value, or a parameter pattern with no
annotation (`fn advance(Position{x:, y:}, ...)`), where the name is the
parameter's type.

A collection literal works the same way (spec §3 *Literal forms*). `[...]`
is the list, vector or set its position expects, so where an annotation, a
parameter, a field or a return type names the collection, write the bare
brackets; write `Vector[...]` or `Set[...]` only where nothing names it, as
on an unannotated binding:

```nomi
struct Grid
    cells: Vector<Int>
    seen: Set<Int>
end

fn empty_grid() -> Grid
    {cells: [0, 0, 0], seen: []}
end

fn main()
    pending = Set[1, 2]
    dbg Set.size(pending)
end
```

A list and a map need no name anywhere: `[1, 2]` and `{"k" => 1}` are a
`List` and a `Map` where nothing expects another collection. Never write
`List[...]` or `Map{...}`.

### 17. Use a tuple for a local pairing and a struct once it travels

Use a tuple, `(a, b)`, for a small, local pairing whose positions are
obvious (spec §4 *Tuples*; tour:
[Structs, Enums & Distinct Types](/structs-enums-distinct/#tuples)): a function returning two values, a map entry. Switch to an anonymous
struct, `{x: 1, y: 2}` (or a struct), once a slot is updated, or read by
position far from where the tuple was built. A named field reads better than
`.second`, and an anonymous struct takes a spread update, which a tuple does
not. Past four items, use a struct: `.fifth` makes the reader count, and a
field name says what the item is.

```nomi
fn divide(x: Int, y: Int) -> (Int, Int)
    (x / y, x % y)
end

fn step(point: {x: Int, y: Int}) -> {x: Int, y: Int}
    {..point, y: .. + 1}
end
```

To change part of a nested value inside an update (spec §4 *Struct
Updates*), write the bare `..`
(`{..user, address: {.., city: "NYC"}}`; repeating the path as a base,
`{..user.address, city: "NYC"}`, is a compile error). Write a literal
without `..` only when the field gets a whole new value. A new value
computed from the old one reads it as `..` too: `{..user, visits: .. + 1}`
and `address: {.., street: rename(..)}`, not `rename(user.address.street)`,
which repeats the path the field already names.

### 18. Read a distinct's value by destructuring

A distinct is read with the pattern that mirrors its construction (spec §10
*Distinct Type Destructuring*; tour:
[Structs, Enums & Distinct Types](/structs-enums-distinct/#distinct-types)):
`Balance(n) = balance`, a `case` arm, or a parameter (`fn cents(Balance(n)):
Int`). The pattern names the distinct, so changing `type Balance Int` to
`type Balance Decimal` leaves each one correct and only retypes `n`.

When other code reads the value in many places, give the type an accessor in
its own file and call that:

```nomi
type Balance Int

impl Balance
    pub fn cents(Balance(n)) -> Int
        n
    end
end
```

An opaque distinct needs one, since code outside its file cannot destructure
it.

### 19. Use `typealias` only to shorten a long anonymous shape

A `typealias` is another spelling of its target, with no identity of its own
(spec §4 *Type Aliases*; tour:
[Structs, Enums & Distinct Types](/structs-enums-distinct/#type-aliases)). Use one to shorten a long anonymous shape that signatures would otherwise
repeat: an anonymous struct, a function type, a long tuple.

```nomi
typealias Rule (String) -> Maybe<String>
typealias Span {start: Int, stop: Int, line: Int}
```

When the thing deserves a name, a domain concept that functions take and
return, declare a struct or a `type` instead. A `type` is checked as its own
type, and a struct names its fields. An alias of a named type
(`typealias UserList List<User>`) only hides what the reader would see by
writing `List<User>`.

### 20. Use a codepoint literal for codepoint work and a string for text

A codepoint literal (`'a'`) is a `Codepoint`, not a character of text (spec §3
*Codepoint literals*; tour: [Scalars & Strings](/scalars-and-strings/#text-model)). Reach
for it when the code works at the codepoint level: enumerating ASCII
(`'a'..='z'`), scanning `String.to_codepoints` output, or matching delimiters
in a `case`. Write `'a'` rather than `Int.to_codepoint(97)!`: the literal
is infallible and says which character it is.

```nomi
fn is_delimiter(cp: Codepoint) -> Bool
    case cp
        ',' or ';' or '\t' -> True
        _ -> False
    end
end
```

Keep text in strings, a one-character one included: `String.starts_with(s,
"#")`, not a comparison against `'#'`, and `"é"`, which has no codepoint
literal at all. Converting back and forth (`Codepoint.to_string('a')`) is a
sign the value wanted to be a string.

### 21. Write a typed literal of fixed text with backticks

A typed literal whose text is fixed uses backticks (spec §18 *Raw literals are
checked at compile time*; tour:
[Typed Literals](/typed-literals/#backtick-literals-are-checked-at-compile-time)).
The compiler runs its handler when it checks the program, so a bad literal is a compile error at the
literal, and a good one is the value itself, with no `?`.

```nomi
// yes: checked when the program compiles
release = Date`2026-05-04`

// no: built at run time, and the Result needs unwrapping
release = Date"2026-05-04"?
```

Use double quotes for a literal that interpolates (`Date"2026-${month}-04"`),
or whose handler has an effect and so cannot run at compile time; the compiler
says when that is the case. A test that checks a bad literal's `Err` at run
time keeps double quotes too, since a backtick literal with that text would
not compile.

### 22. Reach for `DateTime`, not `NaiveDateTime`, for real timestamps

[`std::calendar`](/reference/calendar/) gives the bare name to the safe default
(tour: [Dates & Times](/dates-and-times/)): `DateTime` is zoned by
an IANA time zone, DST-aware, and equal only to the same instant.
`NaiveDateTime` carries its prefix as a warning. It is a wall-clock reading
without a zone, not a moment in history.

```nomi
// yes: a real moment, anchored
appointment = DateTime`2026-06-15T14:30:00-04:00[America/New_York]`

// only for wall time without a zone, which is rare
template_time = NaiveDateTime`2026-06-15T14:30:00`
```

If you find yourself writing a `NaiveDateTime` literal, check whether you want a
`DateTime` with a zone. The naive type fits recurring schedule templates,
form input before zone resolution, and naturally zone-less data such as
birthdays. It does not fit "the meeting is at 2:30pm": meetings happen in time
zones.

### 23. Write bit operations with the operators

Mask, combine and shift Ints with `&&&`, `|||`, `^^^`, `~~~`, `<<<` and
`>>>` (spec §8 *Bitwise operators*; tour:
[Scalars & Strings](/scalars-and-strings/#numbers-booleans-operators)), not with
the `Int.bitwise_*` and `Int.shift_*` functions:

```nomi
low = byte &&& 0x7F

value = value <<< 7 ||| low
```

not `Int.bitwise_or(Int.shift_left(value, 7), Int.bitwise_and(byte, 0x7F))`.
The functions are for passing as values (`Iter.reduce(flags, 0, Int.bitwise_or)`). The shifts and `&&&` bind like `*`, so a shift count that is a sum
or product takes parentheses: `byte >>> (7 - i) &&& 1`.

A `Byte` constant is an integer literal where a `Byte` is expected
(`b &&& 0x7F`, `bytes == [0x81, 0x00]`, spec §3 *Byte literals*), not
`Int.to_byte(0x7F)!`.

## Patterns and conditions

### 24. Test a match with `is`, bind with `if Pattern = x`, branch with `case`

When only whether a value matches matters, write `value is Pattern` (spec §8
*Pattern test*; tour: [Pattern Matching](/pattern-matching/#testing-a-match-with-is)),
not a `case` with a `True` arm and a `_ -> False` arm:

```nomi
fn is_isosceles(sides: (Float, Float, Float)) -> Bool
    kind(sides) is Some(.Equilateral or .Isosceles)
end
```

A predicate that only tests a pattern needs no function of its own: pass a
capture, `Iter.filter(&(&1 is .Item(_)))`, or `Iter.reject(&(&1 is None))`.
An equality test against one value stays `==` (`state == .Open`); `is` reads
better once the pattern has a payload, a wildcard or an `or`
(`state is (.Open or .Paused)`).

`is` binds nothing. When the branch that matched uses the payload, write
`if Pattern = expr` for a small, local match where only that branch needs the
names:

```nomi
label = if Some(name) = maybe_name then
    name
else
    "unknown"
end
```

Write a `case` when every outcome continues with work of its own, when there
is more than one shape to tell apart, or when exhaustiveness matters.

Add `as name` to a pattern (spec §10 *Naming the Whole Match*) when the arm
needs the whole value the pattern matched, such as to pass it on. When the arm needs only fields, bind the
fields and leave the whole unnamed:

```nomi
case result
    Ok(Transaction{kind: .Deposit} as t) -> record(t)
    Ok(Transaction{amount:}) -> amount
    Err(msg) -> msg
end
```

### 25. Join arms with one body by `or`

Arms that return the same thing are one arm with alternatives (spec §10
*Alternatives*; tour:
[Pattern Matching](/pattern-matching/#matching-alternatives-with-or)):

```nomi
case cp
    ' ' or '(' or ')' or '-' -> True
    _ -> False
end
```

not one arm per value repeating `-> True`. Put the `or` where the arms
differ: `Some(.Equilateral or .Isosceles) -> True` rather than
`Some(.Equilateral) or Some(.Isosceles) -> True`. Keep separate arms when
their bodies only happen to agree today and are expected to diverge.

### 26. Take a vector apart with a pattern when its shape decides

When the code branches on a vector's length or reads its first or last
elements, match it with a vector pattern (spec §10 *Vector Destructuring*;
tour: [Pattern Matching](/pattern-matching/#lists-vectors-and-sets)) instead of
`Vector.length` and `Vector.get(...)!`. The pattern says which shapes the code handles, and the
compiler checks that it handles them all:

```nomi
fn describe(v: Vector<Int>) -> String
    case v
        [] -> "empty"
        [only] -> "just ${only}"
        [first, .., last] -> "${first} to ${last}"
    end
end
```

Keep `Vector.get` for an index that comes from arithmetic, such as a grid's
row and column. Over a set, a bracket pattern tests membership, so reach for
one when an arm needs several elements present at once (`["admin", "owner",
..]`) or binds the rest; to test one element, `in` is shorter (§31).

### 27. Write a `case` of conditions with no subject

A `case` whose arms all test conditions matches nothing, so it has no
subject (spec §8 *`case`*; tour:
[Pattern Matching](/pattern-matching/#binding-patterns-and-guards)):

```nomi
case
    count < 10 -> Err(.TooFewDigits)
    count == 10 -> Ok(digits)
    _ -> Err(.TooManyDigits)
end
```

not `case digits` with arms such as `_ when count < 10 -> ...`, where every arm matches
the subject with `_` and only the guards decide. Keep the subject when an
arm's pattern reads it, and write a condition there as that arm's `when`
guard.

An `if` chain that picks a value from three or more branches is the same
choice, so it is this `case` too:

```nomi
size = case
    bytes < 1_024 -> "${bytes} B"
    bytes < 1_048_576 -> "${bytes / 1_024} KB"
    _ -> "${bytes / 1_048_576} MB"
end
```

not

```nomi
size = if bytes < 1_024 then
    "${bytes} B"
else if bytes < 1_048_576 then
    "${bytes / 1_024} KB"
else
    "${bytes / 1_048_576} MB"
end
```

A plain `if`/`else` of two branches stays an `if`, and so does a chain
whose branches run statements for their effects rather than produce a
value.

An `if` without `else` whose branch has a value is a `Maybe` of it (spec §8
*`if`/`else`*). Write one where a `Maybe` is what the position takes, as an
`Html` literal's slot does, which shows the branch or nothing, in element
content and inside a quoted attribute value alike:

```nomi
Html"<p class='inbox ${if unread > 0 then "unread"}'>Inbox${if unread > 0 then Html" <b>${unread}</b>"}</p>"
```

not `${if unread > 0 then ... else Html.empty()}` or `else ""`. To
leave a whole attribute out, give it an unquoted value:
`class=${if active then "active"}`. Where the value is used
as itself, keep the `else`: `if c then x else y`, not an `if` without
`else` followed by `Maybe.with_default(y)`.

### 28. Negate with `!=`, `refute` and `Iter.reject` before `not`

`not` takes everything down to `==` (spec §8 *Logical operators*), so
`not a == b` reads as `a != b` and is best written that way. An assertion that
something is false is `refute x` (spec §17 *Assertions*), not `assert not x`. A predicate passed to `Iter.filter` only to be negated is
`Iter.reject`:

```nomi
fresh = candidates |> Iter.reject(&(&1 in seen))
```

not `Iter.filter(fn c -> not c in seen)`. Elsewhere a negated
call reads well as a `not` pipe stage when the value is already flowing
through a pipeline (`name |> String.trim() |> not String.is_empty()`), and as
a plain `not f(x)` otherwise. When `Iter.reject` does not fit a capture,
write `&(not f(&1))`.

## Failure

### 29. Pick the tool by what happens on failure

**Writing a function that can fail.** When a function you write cannot give
the result asked for, decide by who could have known (spec §11 *Traps,
`Maybe` and `Result`*; tour:
[Pattern Matching](/pattern-matching/#maybet-and-resultt-e)):

- Return `Maybe` or `Result` when the result depends on data the caller does
  not control or cannot cheaply check: a lookup that may miss, a parse, input,
  a file, a position that may legitimately fall off the edge.
- Trap when the caller broke a promise it could have checked: an index it
  computed, a divisor it chose, a count that must not be negative, a range
  whose bounds it picked (`Generator.int(6, 1)`). A trap reports a bug;
  nothing in the program catches it.
- Give every trapping function a total way around it: a sibling that returns
  `Maybe`, or a cheap check the caller can run first.

Write the function once, returning a `Maybe` or a `Result`, as std's `get`
does, and let a caller that knows the call cannot fail write `!` after it:

```nomi
// the index comes from data: the miss is an outcome
case Vector.get(row, column)
    Some(cell) -> cell
    None -> blank
end

// the index is within bounds by construction: a miss is a bug
middle = Vector.get(sorted, Vector.length(sorted) / 2)!
```

Don't return a `Maybe` whose `None` every caller must unwrap with an
"impossible" arm: that arm is untested code, and it hides the bug a trap would
report at its line. Don't trap on data from outside the program either; a
missing key in a config file is something to report, not a crash.

**Taking a result apart.** Five constructs take a value apart when it might
not have the shape you want. Choose by what the failure path does:

- **`?`** passes the error up unchanged. Use it when the function returns a
  `Result` or a `Maybe` and the caller should handle this step's failure as
  it is. In a pipe, write it after the stage (§7).
- **`!`** traps. Use it when a failure would be a bug and the expression
  itself explains it: constant arguments (`Date.new(2024, 1, 1)!`), a key
  taken from the map itself, an index the code computed in range, input
  already validated. Its trap quotes the expression and shows the error, so
  the reader of the trap needs nothing more.
- **A binding `else`** (`Pattern = value else ...`) handles or replaces
  one step's failure in place: it returns a different error, skips a loop
  item, or supplies a fallback, and the success path continues unindented.
- **`case`** when both outcomes continue with work of their own.
- **`if Pattern = expr`** when only the success branch needs the names and a
  miss does nothing (§24).

```nomi
// ?: the caller gets load's error as it is
user = load(id)?

// else: the error is replaced with one that says which user
Ok(user) = load(id) else
    Err(.NotFound) -> return Err("no user ${id}")
    Err(e) -> return Err("loading user ${id}: ${e}")
end

// else: a fallback stands in for the missing value
Some(email) = user.email else "none"

// else: skip what does not parse
Iter.each(lines, fn line ->
    Ok(entry) = parse(line) else continue
    record(entry)
end)

// case: both outcomes do work
case load(id)
    Ok(user) -> greet(user)
    Err(e) -> log_failure(e)
end

// if: only a match does anything
if Some(email) = user.email then send(email)
```

Prefer a binding `else` to a `case` whose failure arm only leaves: the
`case` would nest the whole success path one level in. Prefer `?` to an
`else` whose arm returns the same error unchanged
(`Err(e) -> return Err(e)`).

**State an invariant with `unreachable`.** When a `Maybe` or a `case` arm can
only miss because of an invariant the reader cannot see from the call, write a
binding `else` with `unreachable` and the invariant as its reason. A made-up
default would hide the bug instead:

```nomi
// every student is in Student.variants(), so the search cannot miss
fn position(student: Student) -> Int
    Some(i) = Student.variants() |> Iter.find_index(&(&1 == student)) else
        unreachable "every student is listed"
    end
    i
end
```

`Maybe.with_default(0)` in its place returns 0 for a student someone forgot
to list, and nothing flags it. The reason is the claim a reader checks, so
state the invariant, not the symptom: "every student is listed", not "not
found". Never use `unreachable` or `!` for data from outside the program: one
after parsing a line of input claims something the program does not control,
and a failure that can really happen is a `Maybe` or a `Result` to handle.
When the claim is only that one value is there, `!` states it where the
value is read: `Date.new(year, 1, 1)!`, not a binding `else` with
`unreachable "1 January exists"`.

**Turn a missing value into an error** with a binding `else` when the value
is bound for the lines that follow, and with `Maybe.to_result` and a `?` after
it when the lookup is one stage of a pipe. Name the variant with its enum in
`to_result`: nothing at that position fixes the error type, so `.NotFound`
alone cannot be resolved there.

```nomi
// bound for later lines
Some(user) = Map.get(users, id) else return Err(.NotFound)

// one stage of a pipe
count =
    word
    |> String.trim()
    |> String.to_int()
    |> Maybe.to_result(Error.NotANumber)?
```

### 30. Stub unwritten code with `todo`

When a signature is settled and its body is not, write `todo` with a reason
(spec §11 *Unwritten code*; tour:
[Functions & Lambdas](/functions-and-lambdas/#unwritten-code)) rather than a
value that happens to type-check:

```nomi
fn parse(text: String) -> Header
    todo "parse the header"
end
```

A made-up `Header{name: "", size: 0}` runs and returns a wrong result that
nothing flags; `todo` stops the program at the line that is missing, the editor
lists it as a warning, and `nomi build` refuses to ship it. Give a reason when
the next step is not obvious from the signature, and keep it to what is left to
do. Use `todo` while writing code, not as a permanent marker for a case that
cannot happen: such a case is better shaped out of the types, and where the
types cannot say it, `unreachable "reason"` states the invariant (§29). One
that can happen deserves a real result or an `Err`.

## Collections and iteration

### 31. Test membership with `in`

Every collection reports membership, and `x in c` asks it (spec §8
*Membership*; tour: [Collections](/collections/#sets)): a set, a map (a
key), a range, a list, a vector, bytes and a string. Write `name in
reserved`, not `List.contains(reserved, name)` or `Contains.contains(reserved,
name)`, for a Bool test:

```nomi
if name in reserved then
    Err("${name} is reserved")
else
    Ok(name)
end
```

not `Iter.any_match(reserved, fn r -> r == name)`, which spells out the comparison
`in` makes. Keep `Iter.any_match` for a predicate other than `==`, and
`Iter.contains` for a lazy pipeline, which has no `Contains` impl. A
predicate that only tests membership needs no lambda:
`Iter.reject(&(&1 in seen))`. Name the function (`Set.contains`) only where
a function value is wanted. A `List`, `Vector`, `Bytes` or `Iter` lookup is
a linear scan, so a value looked up repeatedly belongs in a `Set`: convert
once with `Iter.to_set` and test that.

A bounds check is a range membership test too: `not square in 1..=64`, not
`square < 1 or square > 64`. The range states the valid values once, and its
`..` or `..=` says whether the upper bound is one of them.

### 32. Collect a grid into a `Set` or `Map` once

When code asks what stands at positions of a grid, read the grid once into a
`Set` of the positions that hold something, or a `Map` from each position to
what is there ([std::sets](/reference/sets/), [std::maps](/reference/maps/);
tour: [Collections](/collections/#sets)), and ask that. A position off the
grid is absent, so a neighbour needs no bounds check and no
`Vector.get(...)!` pair:

```nomi
flowers =
    garden
    |> Iter.with_index()
    |> Iter.flat_map(fn (row, line) ->
        line
        |> Iter.with_index()
        |> Iter.filter(fn (_column, square) -> square == "*")
        |> Iter.map(fn (column, _square) -> (row, column))
    end)
    |> Iter.to_set()

count = Iter.count_by(neighbours, fn (dr, dc) -> (row + dr, column + dc) in flowers)
```

Keep a `Vector<Vector<T>>` read with `Vector.get` when the code reads whole
rows and columns by index, as a matrix does.

### 33. Reach for a named fold before `Iter.reduce`

[`std::iter`](/reference/iter/) names the common folds (tour:
[Iteration & Loops](/iteration-and-loops/#keeping-every-step-with-iterscan)). A
fold that starts from the empty sum and adds each element is `Iter.sum`:

```nomi
total = prices |> Iter.map(.cents) |> Iter.sum()
```

not `Iter.reduce(0, fn total, n -> total + n)`, which spells out the starting
value and the step the type's `Summable` and `Add` impls already name. A fold
that multiplies from 1 is `Iter.product` for the same reason, and a fold that
multiplies one value by itself is `Int.pow` or `Decimal.pow`:
`Int.pow(10, zeros)`, not `Iter.reduce(1..=zeros, 1, fn n, _ -> n * 10)`.

A fold whose every intermediate state is wanted is `Iter.scan`, and a map that
threads a state from one element to the next is `Iter.map_reduce`:

```nomi
balances = deposits |> Iter.scan(opening, fn balance, amount -> balance + amount)

(_width, rows) =
    lines
    |> Iter.reverse()
    |> Iter.map_reduce(0, fn width, line ->
        width = Int.max(width, String.length(line))
        (width, String.pad_end(line, width))
    end)
```

not an `Iter.reduce` whose accumulator is a `(state, list)` pair that conses
each result and is reversed afterwards. Use `scan` when the outputs are the
states themselves, and it stays lazy, so it can run over an unbounded source
under a `take`. Use `map_reduce` when each element has its own output and
the state only carries what the next one needs.

A fold whose step can fail is `Iter.try_reduce(rolls, Game.new(), Game.roll)`,
which stops at the first `Err`, not an `Iter.reduce` with `?` on a `Result`
accumulator, which visits every element and whose `?` leaves only the lambda.

Keep `Iter.reduce` when only the final state is wanted and the fold does more
than add (`total + n * weight`), starts from another value, or breaks early.
Its starting value is the positional argument before the callback, unnamed:
`Iter.reduce(orders, 0, fn total, o -> total + o.cents)`. A named function is
passed itself, after the starting value:

```nomi
total = Iter.reduce(orders, 0, add_order)
```

not `Iter.reduce(orders, 0, fn total, o -> add_order(total, o))`, a lambda
that only passes its parameters on. A capture is passed the same way:
`Iter.reduce(counts, {}, &Map.merge_with(&1, &2, Int.add))`.
`Iter.loop` takes a named step the same way, `Iter.loop(start, step)`.

### 34. Number elements with `Iter.with_index`, not a counter in the state

When a step needs each element's position, pair the elements with it
([`Iter.with_index`](/reference/iter/)) and take the pair apart in the
lambda, rather than carrying a counter in an `Iter.reduce` state or a
recursion's parameters:

```nomi
fn checksum(digits: List<Int>) -> Int
    digits
    |> Iter.reverse()
    |> Iter.with_index()
    |> Iter.map(fn (i, d) -> if i % 2 == 1 then double(d) else d)
    |> Iter.sum()
end
```

not an `Iter.reduce` whose state is `(total, index)`, or a parity flag
flipped at each step. The position comes from the element, and what remains
is a plain map and a named fold (§33). The index counts from 0.

### 35. Choose between recursion, `Iter.loop` and `Iter.iterate`

Each fits a different shape of repetition:

- **`Iter.iterate(start, next)`** for a sequence where each value comes from
  the one before, of which you want some or all values. The pipeline after
  it says how much: `take`, `take_while`, `count`, `find`.
- **`Iter.loop`** (spec §13 *`loop`*; tour:
  [Iteration & Loops](/iteration-and-loops/#loops-with-iterloop)) for a
  state that changes until a condition ends it with `break`, when only the
  final state is wanted.
- **Recursion** when the data is recursive (a tree, a grammar, nested
  lists), when a step can fail and `?` should end the whole walk, and for a
  search that backs out of dead ends. A recursive call in tail position runs
  in constant stack space (spec §7 *Tail-call optimization*); a call that
  is not grows the stack at each level.

```nomi
// a sequence: iterate, then cut it
fn steps(number: Int) -> Int
    Iter.iterate(number, next)
    |> Iter.take_while(&(&1 != 1))
    |> Iter.count()
end

// a state until a condition: loop
fn first_power_above(limit: Int) -> Int
    Iter.loop(1, fn n ->
        if n > limit then break n
        n * 2
    end)
end
```

A tail-recursive counter does what the first does, and spells out the count
the pipeline names. Prefer a pipeline over the collection when the loop only
walks a collection: `Iter.map`, a named fold (§33) or `Iter.find`.

### 36. Build a list newest first and reverse it once

A `List` is linked: `[x, ..rest]` puts `x` in front in constant time, and
`[..rest, x]` copies `rest` to put `x` at its end (spec §4 *List*). A
recursion or loop that grows a list one element per step conses, and
reverses the list once when it is done:

```nomi
// yes: each step conses, and the finished trail is reversed once
fn trail(at: Point, seen: List<Point>) -> List<Point>
    case next_step(at)
        Some(step) -> trail(step, [at, ..seen])
        None -> Iter.reverse([at, ..seen])
    end
end

// no: each step copies the trail to add one point
fn trail(at: Point, seen: List<Point>) -> List<Point>
    case next_step(at)
        Some(step) -> trail(step, [..seen, at])
        None -> [..seen, at]
    end
end
```

Say in the parameter's doc comment that it is held newest first. When each
element of an input gives one output, `Iter.map` or `Iter.map_reduce` builds
the list in order and needs neither (§33).

### 37. Sort by several keys with one tuple key

Tuples compare element by element (spec §5 *Ordering requires a declared
`Comparable`*; tour:
[Interfaces & Dispatch](/interfaces-and-dispatch/#deriving-vs-sorting-by-key)),
so a sort by several keys is one
`Iter.sort_by` whose key is a tuple of them, most significant first. Wrap a
key in `Reverse.of` (`std::reverse`) to sort it descending; it works for any
`Comparable` key, and reads as intent where negating a number reads as
arithmetic:

```nomi
// most points first, then by name
table = Iter.sort_by(standings, fn s -> (Reverse.of(s.points), s.team))
```

Don't chain stable sorts, `|> Iter.sort_by(.team)` and then
`|> Iter.sort_by(Direction.Descending, .points)`: the reader has to know the
sort is stable and read the keys bottom up. To sort a whole list descending,
pass `Direction.Descending`. `Iter.sort_with` and `Ordering.chain` remain for
a comparator that is not a key, such as one that compares by a function of
both values.

## Applications and tests

### 38. Boot builds the application value

Build the application struct's `Context` field in `boot` from
`Context.root()`, or derive a tighter one there (spec §15 *Scoped application
fields*; tour: [App Fields, Defer & Context](/app-fields/#the-basic-shape)):

```nomi
pub struct Config
    port: Int = 8080
    context: Context
end

pub fn boot() -> Result<Config, String>
    Ok(Config{context: Context.root()})
end
```

**A boot's error is the error of what it calls.** When its steps fail with
one error type, such as a database's `sql.Error`, make that the `E` of
`Result<App, E>` and end each step with `?`. A boot whose steps fail with
different types returns an error type of its own with a `From` impl for each,
so each `?` converts (spec §11 *Propagating with `?`*). A boot that cannot fail says `Result<App, String>` and returns
`Ok(...)`.

**Boot with no startup input when the tests need none.** A boot that reads no
startup input declares no parameter, `fn boot() -> Result<App, E>`, and its
groups write `boot server.boot()`. A boot that reads startup takes
`startup: Startup`. When most of its tests need no input, default the
parameter, `fn boot(startup: Startup = Startup{}) -> Result<App, E>`, so those
groups write `boot server.boot()` and only the groups that test the inputs
build a Startup. The runtime still passes the real Startup to `main`'s boot, so
the default changes nothing outside tests. Leave the parameter required when
every test must decide its inputs, such as a boot that fails without a
database URL. Configuration helpers that build the whole application take
`startup` as a parameter too.

Put `with` lines at the top of the block they govern where practical, so the
reader sees the replaced fields before the code that runs under them:

```nomi
fn import_quietly()
    with Config.logger = Silent
    with Config.store = FakeStore{}

    run_import()
end
```

A `with` lasts to the end of its block. When only part of a function should
run under it, move that part into a helper function and put the `with` lines at
the top of the helper. Use a bare block statement only when a helper would be
ceremony.

### 39. Write tests as ordinary code with direct assertions

Prefer `_test.nomi` files with explicit `test "..."` blocks for
behavior checks (spec §17 *Tests*; tour: [Testing](/testing/)). A test should read like ordinary Nomi code with a small number
of direct assertions:

```nomi
test "result values can be asserted directly"
    assert parse_id("42") == Ok(42)
    assert parse_id("nope") == Err("expected a numeric id")
end
```

Put the tests of `shapes.nomi` in `shapes_test.nomi` beside it. That file
is its partner test and reads its private declarations (spec §14
*Visibility*), so a private helper is tested as it is: don't make a
declaration `pub` only so a test can reach it.

Use unique full test names within a file. The enclosing `group` is part
of the full name, so repeated leaf names are fine in different groups.

A `group` is for tests that share a `clock`, `boot` or `setup`, or for
several tests that belong under one heading. Don't wrap a single test in a
group to name it: name the test.

```nomi
test "a new clock wraps past midnight"
    assert "${Clock.new(25, 0)}" == "01:00"
end
```

**Keep tests focused.** A test name should carry the intent, so avoid comments
that restate the next line. Split unrelated rules into separate tests instead
of collecting a tour of a feature in one block.

A compact example that belongs to one declaration is an attached test (§49),
especially a stdlib reference example. Keep broader workflows and multi-case
behavior in named `test` and `group` blocks.

### 40. Assert the value under test

Use `assert` for the positive shape and `refute` when the thing under test is
already a predicate (spec §17 *Assertions*; tour: [Testing](/testing/#assertions)). Don't wrap simple value checks in `io.inspect` or string
interpolation to produce output; assert the value itself. Don't introduce a
pipeline for a simple call-and-compare; direct equality is clearer:

```nomi
assert String.to_decimal("1.50") == Some(1.50d)
```

Write the value under test on the left of `==` and the expected value on the
right. A failed comparison of multi-line Strings prints a diff that reads the
right operand as expected and the left as actual.

`assert` and `refute` may stand alone as statements, since the check is the
reason the expression is there. Use a pattern assertion when a payload matters:

```nomi
assert parse_id("42")

assert Err(message) = parse_id("wat")

assert "numeric" in message
```

Use `?` for fallible setup when the happy-path value is what the rest of the
test needs. Use a pattern assertion when the returned shape is itself under
test:

```nomi
t = Time.parse("09:30:00")?

assert Time.to_string(t) == "09:30:00"

assert Err(Error.InvalidFormat(_)) = Time.parse("bad")
```

When the value under test comes from a pipeline and the intermediate meaning is
useful, bind the pipeline result and assert the binding. The failure report
then shows a named expression:

```nomi
found =
    [1, 2, 3, 4, 5]
    |> Iter.find(fn x -> x == 3)

assert found == Some(3)
```

For one-shot checks where a binding would only name the plumbing, let the
pipeline compute the assertion subject and put `assert` or `refute` at the
head. Prefer an existing predicate or equality function (`String.contains`,
`String.equal`, `Result.is_ok`) over a throwaway lambda. When a predicate has
semantic weight or several conditions, give it a predicate's name (§4) and
assert that instead; keep `then` stages for tiny local transformations. Indent
the stages under the asserted operand, which distinguishes it from a
statement-level pipeline:

```nomi
assert "  ADA LOVELACE  "
    |> String.trim()
    |> String.lowercase()
    |> String.equal("ada lovelace")

assert values
    |> Iter.map(normalize)
    |> Iter.all_match(is_valid)
```

### 41. Check a table of inputs with `subtests`

Use `subtests` (spec §17 *Subtests*; tour: [Testing](/testing/#subtests))
when one claim is checked over a table of inputs: the same
assertions, rows that differ only in data. Each row then reports on its own,
and a failure names the row. Write the rows as a table of tuples or structs,
pipe it into `subtests`, and bind each row with a pattern that names each
column:

```nomi
test "parses ids"
    [("42", Ok(42)), ("007", Ok(7)), ("nope", Err("expected a numeric id"))]
    |> subtests (text, expected)
        assert parse_id(text) == expected
    end
end
```

Pipe a literal table used once. Bind the table to a name, and write
`subtests cases, (text, expected)`, when the test uses it again or
when it needs a type annotation.

When the data alone does not say what a row checks, give each row a
description column and name the row with it, so a failure reads as the case
it is:

```nomi
test "leap years"
    [
        ("year divisible by 400 is leap year", 2000, True),
        ("year divisible by 100, not divisible by 400 in common year", 1900, False),
    ]
    |> subtests (description, year, expected), description
        assert is_leap_year(year) == expected
    end
end
```

Name rows with a column rather than a string built from the other columns
when the table already carries one, and leave the name out when the rendered
row says enough.

When the table's type needs one variant or struct name to settle, write the
name in the first row that needs it and leave the other rows bare, rather
than annotating the table:

```nomi
cases = [
    ("single bit one to decimal", 2, [1], 10, Ok([1])),
    ("input base is one", 1, [0], 10, Err(Error.InvalidInputBase)),
    ("input base is zero", 0, [], 10, Err(.InvalidInputBase)),
    ("negative digit", 2, [1, -1, 1, 0, 1, 0], 10, Err(.InvalidDigit)),
]
```

Annotate the table when a column is an anonymous struct, which has no name
to write, or when several columns would each need a name.

Write separate tests instead when the rows need different setup or different
assertions: a `subtests` body that branches on its row is several tests in
one. Keep a table to one claim, and split a long one into tests by what the
rows have in common.

### 42. Keep expected values the tests share in a `once`

When several tests check against the same expected values (a song's verses,
a board, a tree), write them once as a `once` in the test file (spec §6
*`once` Bindings*; tour:
[Bindings & Expressions](/bindings-and-expressions/#file-level-values-with-once))
and have each test take what it needs. One test runs `subtests` over the
table (§41), and the others build their expectations from it:

```nomi
once verses = [
    (1, "On the first day of Christmas my true love gave to me: a Partridge."),
    (2, "On the second day of Christmas my true love gave to me: two Doves."),
]

test "each verse"
    subtests verses, (number, expected)
        assert recite(number, number) == [expected]
    end
end

test "two verses together"
    expected =
        verses
        |> Iter.map(fn (_number, verse) -> verse)
        |> Iter.to_list()

    assert recite(1, 2) == expected
end
```

A value is then written once, and a fix to it reaches every test that uses
it. Keep a value one test uses in that test.

### 43. Test an interactive program with `io.replay`

Test an interactive program, one that prompts and reads in turn, with
`assert io.replay(script, main)` ([std::io](/reference/io/); tour:
[Testing](/testing/#input-and-output)) and a `"""` script, not with
`io.capture(input, main)` and separate input and output strings. The script
shows each answer next to the prompt it answers, so the reader follows one
conversation instead of matching two lists. Keep `io.capture` for a program
that reads its input in one go, or when the value `main` returns matters.
A program that reads nothing is checked with `io.replay` too, with a
script of its output alone.

Write the output at the closing `"""`'s column, so the literal keeps only
the indentation that is part of the output. Indent each input line four
columns past it and put one space after its `>`. Write a prompt the program
writes with `io.write` as its own output line, without its trailing space,
and the answer on the input line under it.

```nomi
test "deposit money"
    assert io.replay(
        """
        What is the starting balance?
            > 500
        deposit:
            > 25
        new balance is 525
        """,
        main,
    )
end
```

[`io.replay`](/reference/io/#ioreplay) gives the script's rules, such as
how to write output that would read as input.

A script with no input is its output, written the same way:

```nomi
test "prints the table"
    assert io.replay(
        """
        -10.0 C |   14.0 F
         -5.0 C |   23.0 F
        """,
        main,
    )
end
```

### 44. Share a group's boot and fakes through a helper file

Put the fakes a project's tests install, and the `Startup` they boot with when
they need one (spec §17 *Groups*; tour: [Testing](/testing/#groups-and-setup)),
in a test helper file such as `test_support.nomi`. It exports
the fake types and, when tests need startup input, `pub fn startup() ->
Startup`. A group passes `test_support.startup()` to its `boot` line and
installs fakes with `with` lines in `setup`, so every test in the group runs
against them. Build the Startup with only the fields it sets; the rest default
to empty:

```nomi
// test_support.nomi
import server.Mailer

pub fn startup() -> Startup
    Startup{env: {"SENDER" => "test@example.com"}}
end

pub type FakeMailer

impl Mailer for FakeMailer
    fn send(_mailer: FakeMailer, to: String) -> Result<Unit, String>
        if to == "" then Err("no address") else Ok(Unit)
    end
end
```
```nomi
// signup_test.nomi
import
    server
    server.App
    test_support
    test_support.FakeMailer
end

group "signup"
    boot server.boot(test_support.startup())

    setup begin
        with App.mailer = FakeMailer
        "ada@example.com"
    end

    test "sends a welcome", to
        assert server.welcome(to)
    end
end
```

A test that needs a different startup builds it from the shared one:
`{..test_support.startup(), args: ["--dry-run"]}`. Groups do not nest; write
sibling groups, and share setup between them through a function.

Put a group's `setup` at its top, after `clock` and `boot`, where a reader
looks for it.

## Designing a module's API

### 45. Put the data first

A function takes the value it works on as its first parameter, so a caller
can pipe into it (§7): `Map.get(m, k)`, `String.trim(s)`, `Iter.map(xs, f)`.
Options and callbacks come after it.

HTTP middleware follows the rule: the handler is the value it works on, so
it comes first, and the middleware returns a handler,
`fn require_admin(handler: Handler) -> (Request) -> Response`. A stack of
middleware is then a pipe. A middleware that hands the handler a value,
such as the signed-in user, says so in the handler's type,
`(Request, User) -> ToResponse`, rather than storing it on the request, so
a route that needs a user cannot be registered without the middleware that
finds one.

### 46. Name conversions and locale functions by their shape

A conversion from one value type to another is `to_<target>` on the source
type: `Int.to_float`, `Int.to_decimal`, `Float.to_int`, `String.to_int`,
`String.to_decimal`, `Int.to_codepoint`. `from_` is for building a value out
of its units, an encoding or a collection, on the type it builds:
`Instant.from_seconds`, `Uuid.from_bytes`, `Int.from_digits`,
`String.from_codepoints`, `Bytes.from_list`. A function that returns a
value of the type it takes is named for what it does, with no `to_`:
`String.trim`, `String.uppercase`, `String.fold_case`, `String.snake_case`.
A conversion interface sits on the target type, `impl From<sql.Error> for
ApiError`, because the orphan rule puts it there: `impl To<ApiError> for
sql.Error` would put the local type in a type parameter only a result names,
while `From`'s `E` is an input, so `impl From<LocalErr> for ForeignErr` is
allowed (spec §12 *Foreign Types*).

A function that reads or changes text by a locale's conventions ends in
`_in` when its name without the suffix is, or would be, the same operation
by no locale's conventions: `Date.parse` reads ISO 8601 (`2024-03-15`) and
`Date.parse_in` a reader's (`15.03.24` in `de`), and `Int.parse_in` is to
`String.to_int` what `Date.parse_in` is to `Date.parse`; `String.uppercase`
is Unicode's default casing and `String.uppercase_in` the locale's;
`Money.parse_in` reads what `Money.format` writes, beside the `12.50 EUR`
its `Display` writes. A function that only means something in a locale
takes one with no suffix: every `format` and `format_*` (a value's text in
no locale is its `Display`, not a `format`), `String.join_list`,
`PluralCategory.cardinal` and `Collator.new`. Either way the locale is the
second parameter, after the value the function reads or writes, and
before the options: `Decimal.parse_in(text, locale, lenient: True)`,
`TimeUnit.format_relative(3, locale, TimeUnit.Day)`. Std follows the rule,
and a library that formats for a reader should too.

### 47. Make a type opaque to keep an invariant

When only some values of a representation are valid (a clock's minutes
within one day, a fraction in lowest terms, a queen on the board), declare
the type `pub opaque` (spec §4 *Opaque distinct types*; tour:
[Modules & Imports](/modules-and-imports/#opaque-types)) and build it only
through functions that establish the rule. Code outside the file cannot
build or take apart the value, so every value it holds obeys the rule, and
nothing downstream checks it again:

```nomi
pub opaque type Clock Int

impl Clock
    pub fn new(hour: Int, minute: Int) -> Clock
        Clock(Int.modulo(hour * 60 + minute, 1_440))
    end
end
```

Keep the representation canonical, one value for each thing it stands for,
and a derived `Equatable` is the right equality: two clocks that show the
same time hold the same Int. A constructor that can refuse its input returns
a `Result` (`Queen.new(row, column)`), since the caller may not know the
input is bad (§29). Give the type the accessors its users need (§18). A type
with no rule to keep stays transparent: `opaque` on it only hides fields
callers would read. An opaque type inspects as `<opaque Clock>`, and
`derive Debug` shows its value; a type with secret fields writes an
`impl Debug` that leaves them out instead.

### 48. Give a module's errors an enum

A module whose functions can fail in more than one way declares an error
enum (spec §11 *Error types*), named `Error` (§4), with a variant per way,
and its functions return `Result<T, Error>`. A caller matches the variant,
tests compare variants rather than message text, and a `case` over the
error is checked for every way. A `Display` impl gives the message a person
reads:

```nomi
pub enum Error
    TooShort
    AreaCodeStartsWithZero
end

impl Display for Error
    fn to_string(error: Error) -> String
        case error
            .TooShort -> "must not be fewer than 10 digits"
            .AreaCodeStartsWithZero -> "area code cannot start with zero"
        end
    end
end
```

not `Result<String, String>`, whose callers can only compare text. Keep a
`String` error for a failure only a person reads: a script's, or a `boot`
that only reports why it stopped (§38). A variant carries the data its
message needs (`InvalidDigit Int`), not the message.

An HTTP app's error enum implements std::http's `ToResponse` as well, so a
handler returns `Result<Response, AppError>` and passes failures up with
`?` rather than turning each one into a page where it happens.

### 49. Doc comments and examples

Put a `///` doc comment (spec §2 *Comments*) on every `pub` declaration, and
show its use with `//!` attached tests (spec §17 *Attached tests*; tour:
[Testing](/testing/#attached-tests)). `nomi test` runs them, and the stdlib
reference renders them as runnable examples, so an example that goes stale
fails. The stdlib does this throughout (`std/sets.nomi`, `std/iter.nomi`):

```nomi
impl Set<T>
    /// Returns a new set with `elem` added (no-op if already present).
    //! assert Set.insert([1, 2], 3) == [1, 2, 3]
    //! assert Set.insert([1, 2], 2) == [1, 2]
    //
    pub fn insert<T>(s: Set<T>, elem: T) -> Set<T>
        Set{items: Map.put(s.items, elem, True)}
    end
end
```

When an attached assertion's subject becomes multi-line, keep the assertion at
the front and prompt each continuation line:

```nomi
//! assert Iter.loop(0, fn n ->
//!     if n >= 3 then
//!         break n
//!     else
//!         n + 1
//!     end
//! end) == 3
```

When a compact example needs setup, write the setup as ordinary prompted lines:

```nomi
//! v: Vector<Int> = []
//! assert Vector.length(v) == 0
```

Keep a fenced ` ```nomi ` block inside `///` for code that is not an assertion,
such as how to implement an interface (`Iter`'s doc comment shows a custom
`each_while`). Nothing checks those blocks, so prefer `//!` whenever an
assertion can show the point.

Describe a whole file with `//#` lines at its top, in every file, not only in
`std/`. A test file may leave it out: its name says which file it tests, and
its tests' names say what they check. Use `//#` rather than a plain `//`
above the first declaration, which reads as that declaration's comment.
```nomi
//# Parses and evaluates the word problems of the exercise.

/// Answers a word problem.
pub fn answer(question: String) -> Result<Int, Error>
    ...
end
```

### 50. Write generated code a reader can trust

These rules are for the author of a tool that generates Nomi code (`nomi
tool`), such as sqlkit's `generate` (tour:
[Generating Code](/generating-code/#keeping-generated-files-current)).

- **Write files `nomi fmt` leaves alone.** A reader reviews generated code
  in a diff, and a formatter pass that rewrites it every time hides the
  change that matters. Lay the code out as the formatter does.
- **Say what made the file.** Start each file with a comment that names the
  tool and its inputs and says not to edit it, and record a checksum of the
  inputs there: `// inputs sha256: ...`.
- **Fail before writing.** Check the inputs first, report each problem at
  its file and line, `db/queries/users.sql:12:5: ...`, and write nothing when
  there is one, so a half-written set of files never reaches the checker.
- **Offer `--check` and a test.** `--check` writes nothing and fails when a
  file is not what the tool would write, for an automated check. A generated
  test that computes the inputs' checksum again and compares it with the
  header's makes a plain `nomi test` notice a forgotten run.

### 51. Acquire a resource, then `defer` its release on the next line

std offers no scoped `with_*` form that runs a function with a resource. A
function acquires the resource, defers its release on the next line, and
uses it in the same body, so the release runs however the function ends:
by its last line, a `return`, a `?` or a trap. A writer's success path
closes it with `?`, which commits the write and reports a failed close; the
deferred `Writer.discard` throws the output away on every other way out.

```nomi
fn save(path: String, rows: List<String>) -> Result<Unit, io.Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Writer.discard(w)

    Writer.write_lines(w, rows)?
    Writer.close(w)?
    Ok(Unit)
end
```

The other pairs: `fs.open` and `Reader.close_quietly`, `fs.open_file` and
`File.close_quietly`, `fs.make_temp_dir` and `fs.remove_all_quietly`,
`process.spawn` and `Child.kill`, `http.stream` and `StreamedResponse.close`, and a
driver's open and `sql.close_quietly`.

The checker holds the rule for a type that implements `Release`, and the
success path's close for one that implements `Commit` (spec §9 *Releasing
resources*). A type of your own that holds something to give back
implements `Release`, with the body of its `release` calling the function a
caller defers.

### 52. Take an interface value unless the caller's type comes back

A parameter used only through one interface, whose type appears nowhere else
in the signature, takes the interface as its type. It reads as what it is,
any value that can do this:

```nomi
fn text(error: Display) -> String
    "failed: ${error}"
end
```

Write a type parameter when the caller's type matters past the call: it
recurs in another parameter, in the result or inside another type
(`Result<T, E>`, a returned `(E) -> Error`), it needs two bounds, or the body
calls an interface function whose result is `self` or that takes no `self`.
An interface value keeps none of that (spec §12 *Interface values*):

```nomi
fn larger<T>(a: T, b: T) -> T where T: Comparable
    if a > b then a else b
end
```

A function taking an interface value passes where a function of the
concrete type is expected, so `Result.map_err(text)` works on any error
that implements `Display` (spec §5 *Assignability*). A struct or an enum of
your own enters its interface unboxed, so the two forms cost the same.

`nomi check` and the editor suggest this rewrite, with a fix, for a type
parameter used once as a parameter's type under one bound.
