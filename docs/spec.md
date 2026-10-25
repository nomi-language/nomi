# Nomi Language Specification

Nomi is a fully immutable, functional-first, statically typed language. Types are inferred within a function; function signatures are annotated. Programs run on a bytecode VM (§25).

This document says what the language does. A feature described here works unless it carries a `Status: not yet implemented` callout. How parts of it are built is in [`implementation-notes.md`](implementation-notes.md).

---

## 1. Core Philosophy

- Fully immutable — no mutation concept
- Opinionated — one way to do things
- One canonical layout, owned by the formatter (`nomi fmt`), with no style options
- No null — `Maybe` and `Result` types instead
- No classes or inheritance
- `Iter.loop`, recursion, and higher-order functions — no imperative mutation
- Statically typed with local type inference
- Unused imports and variables are compiler errors

## 2. Bindings

```nomi
// Container level — once bindings (set on first access, then immutable)
pub once max_retries = 3 // public — `pub` modifier
once default_port = 8080 // private — no `pub` modifier
once route_table: Map<String, Handler> = build_route_table() // type annotation optional

// Inside function bodies — immutable bindings (snake_case enforced)
x = 5

name = "hello"
```

- All bindings are immutable. No mutation concept.
- All data operations return new values.
- `once name [: T] = expression` is the only "set once, never changes" declaration at file or type-owner level. The right-hand side is evaluated lazily on first access and cached forever; the type annotation is optional and inferred from the value when omitted (see "`once` Bindings" below). Visibility is determined by the optional `pub` modifier (see §3) — `pub` makes the binding public; without `pub` it is private to the file or owner.
- No other runtime bindings at file level — only `once`, functions, types, and other declarations. A binding (`x = 3`), an expression (`io.print("hi")`) or another statement (`assert`, `defer`, `return`) written at file level is a compile error; code runs inside a function. Function bodies use ordinary `pattern = value` bindings for the supported binding-pattern subset; a pattern that can fail takes an `else` (§10, *Bindings with `else`*). In a binding position, `=` is a match/bind separator, not assignment: the right-hand side is evaluated, matched against the left-hand pattern, and any names introduced by that pattern are bound immutably.
- Every ordinary local binding and every ordinary parameter in a function, lambda, or method body must be read. To intentionally ignore a value, bind it with a discard name: `_` or `_name`. Discard names use a single leading underscore; double-underscore names are reserved for runtime/compiler internals and remain ordinary bindings. Discard names are binding-position patterns, not readable variables; `_name` is only a human-readable label for the ignored value. The rule covers every name a pattern binds: a destructured parameter's names are parameters (`fn #(word, count) -> count` reports `parameter 'word' is never read`), and the names a `case` arm, an `else` arm, an `if` condition or a test's setup binding introduces are local bindings. A struct pattern matches partially, so a field it does not need is left out (`#.Suspended{reason}`).
- Parameter use is checked in every body, including `boot` and interface default methods. Bodyless interface and host declarations have no body to check. Use `_` or `_name` for intentionally unused parameters; a discard parameter does not introduce a readable binding. An interface implementation that must retain a named parameter can explicitly discard it in its body with `_ = name`.
- A statement that starts right after the end of another, with no space, newline or `;` between them, is a parse error at its first token: `p = Point{}` is `unexpected LBRACE "{" right after the end of a statement`, rather than a binding followed by an empty block. Statements on one line separated by a space are unchanged.
- Non-final expression statements must return `Unit` unless they are `dbg` observations. A value-producing expression that appears before the block's final expression is rejected because its result would disappear silently. Bind intentional discards with `_ = expr`; use `dbg expr` or `expr |> dbg` when the point is temporary observation.
- The final expression of a body whose return type is `Unit`, written or omitted, may be a `dbg` observation too: `dbg expr`, or a pipe whose last stage is `|> dbg`. The value `dbg` passes through is discarded and the body returns `Unit`. `dbg` itself still answers its operand, so a `dbg` ending a body of any other return type is that body's value. Any other final expression whose type is not `Unit` is a return type mismatch in a `Unit` body.

```nomi
import std/io

fn main() {
    total = 2 + 3
    io.print("computed")

    [1, 2, 3]
    |> Iter.map(&(&1 * total))
    |> Iter.to_list()
    |> dbg
}
```

`=` is not a general expression operator and does not mean mutation. It has a
pattern-match role only where the left side is a binding, control-flow, or
assertion pattern, including ordinary local bindings, destructuring bindings,
parameter patterns, `if Pattern = expr`, bindings with `else`
(`Pattern = expr else { ... }`), and pattern assertions
(`assert Pattern = expr`). Other syntactic uses of the same glyph are
declaration punctuation: parameter defaults
(`port: Int = 8080`), struct/enum field defaults, `once` initializers,
`defer cleanup(value)`, and application-field overrides
(`with App.logger = logger`) introduce or configure values but do not
perform a pattern match.

### Shadowing and redeclaration

Nomi rejects redeclaration aggressively, but allows ordinary bindings to shadow prior bindings, parameters, and file-level decls (Gleam/Rust-style):

- **Same-scope duplicates are an error for declaration kinds other than ordinary bindings.** Two declarations of the same name in the same scope — `once x = 1; once x = 2`, `fn foo() {}; fn foo() {}`, duplicate types, duplicate parameters within one signature, duplicate names within one pattern — are rejected. There is no last-wins fallback. Top-level local definitions that share a name with an imported name are also rejected — alias the import (`import std/maps.{self, Map} as StdMap`) to keep both. See §14 (Explicit item imports) for the full rule.
- **An ordinary `name = value` binding may shadow a prior same-scope binding or parameter** (Gleam/Rust-style). The newest binding wins for subsequent references, and a shadow may change the type. A closure created before the shadow keeps the value it captured (§23). This is the value-threading idiom:
  ```nomi
  fn normalize(name: String) -> String {
      name = String.trim(name) // shadow the parameter
      name = String.to_lower(name) // shadow the local
      name
  }
  ```
  Duplicate `once`, `fn`, `type`, `interface`, `typealias`, duplicate parameters within one signature, and duplicate names within one pattern remain same-scope errors.
- **Ordinary `name = ...` bindings may shadow file-level decls.** This is the standard immutable-rebind pattern; it is allowed and idiomatic:
  ```nomi
    once max_retries = 3

    fn main() {
        max_retries = 5 // fine — local binding shadows the file-level once binding inside main
        ...
    }
  ```
- **Block-nested ordinary bindings may shadow each other across nested scopes** (function bodies, lambda bodies, `case` arms).

Inside an implementation or interface default, a bare call first uses its
lexically resolved binding, including a local callable parameter or an ordinary
file function. Same-owner method lookup supplies a callee only when no such
binding resolves the name. This precedence also applies to pipeline calls.

A binding may carry an optional inline type annotation: `name: Type = value`. The annotation drives bidirectional inference into the value (so `m: Map<String, Int> = %{}` gives the empty map literal its key and value types) and is also a checked assertion — a value whose inferred type cannot unify with the annotation is a compile error. A function-typed annotation also drives each lambda parameter's type, so `f: (Int) -> Int = fn x -> x + 1` is accepted without per-param annotations on the lambda. Annotations are not yet supported on destructuring patterns (`#(a, b) = pair`).

```nomi
m: Map<String, Int> = %{} // types the empty literal

count: Int = try parse_int("42") // assertion: Int or compile error

f: (Int) -> Int = fn x -> x + 1 // annotation drives lambda param types
```

#### Locally-determined bindings and turbofish

Type inference and the requirement to annotate are **decoupled**. The checker propagates types as far as it can — a value with an unsolved type parameter unifies against a concrete parameter at a call site, generic returns flow through arguments, and so on; inference is a capability to maximize. *Separately*, a binding must be **locally determined**: its type must be readable from its own line, without scanning forward to how the binding is later used.

A binding is rejected when its type is *wholly unresolved* — it carries no concrete information because its only type parameters are unsolved with nothing on the line to pin them:

```nomi
m = %{} // ✗ Map<?, ?> — not locally determined

ch = Channel.buffered(4) // ✗ Channel<?>

xs = [] // ✗ List<?>

x = None // ✗ Maybe<?>
```

Supply the type with an **LHS annotation** or an **RHS turbofish** — explicit type arguments on a call, `f<T>(...)`, which mirror the callee's signature `<...>`:

```nomi
m: Map<String, Int> = %{} // LHS annotation

ch = Channel.buffered<Int>(4) // RHS turbofish on a type-qualified callee

xs: List<Int> = []

f1 = Static<Int>("hi") // RHS turbofish on a bare variant constructor

f2 = Some<Int>(42) // works on any callable name, snake_case or PascalCase
```

A binding whose type carries *any* concrete information is locally readable and needs no annotation — including the common variant constructions where only a secondary, value-less type parameter is open:

```nomi
x = Ok(42) // ✓ Result<Int, ?> — the success type is known

n = Some(Ok(42)) // ✓ Maybe<Result<Int, ?>>

xs = [1, 2, 3] // ✓ List<Int>
```

The enclosing function's return type is an expected type only for the function's result: its tail, a `return`, and the arms of a `case` or `if` in tail position. In `fn g(s: String) -> Result<Unit, String>`, the binding `r = case s { "a" -> f(); _ -> Err("bad") }` types `Err("bad")` as `Result<?, String>`, which unifies with `f()`'s `Result<Int, String>`; the function's `Result<Unit, String>` plays no part.

A block's expected type is likewise an expected type only for its tail. A binding before the tail is checked with nothing expected, so in `once o: Shape = { b = .Circle(2.0); b }` the `.Circle` names no enum, as `b = .Circle(2.0)` names none in a function body; annotate the binding (`b: Shape = .Circle(2.0)`) or end the block with the variant.

This is deliberately stricter than full inference: a binding's type is *not* rescued by a later use, even though the checker could solve it from that use. The local-readability requirement is a policy, independent of how far inference reaches. Turbofish itself is an ordinary call form — usable anywhere a call is, required only where a binding would otherwise be wholly unresolved. Each type argument is a type annotation, so a name that is no type is the same `unknown type "Nope"` error at the name (`ident<Nope>(1)`).

A name a pattern binds (in a `case` arm, an `if Pattern = value`, a destructure, or a binding with `else`) is held to a narrower rule, checked once the whole file is: its type must not be wholly unknown. `Maybe.to_result(None, "none")` is `Result<?, String>`, so `Ok(z)` would bind `z` to a type nothing fixes, and that is an error at `z` unless the binding's `else` fallback or a later use of `z` fixes it. A name whose type only contains an unknown part, such as `a` in `#(a, b) = #(None, 1)`, is accepted.

```nomi
Ok(z) = Maybe.to_result(None, "none") else {
    return
} // ✗ the type of 'z' is not determined

Some(n) = None else { 0 } // ✓ the fallback makes n an Int
```

#### Determined type arguments

A generic function runs at the type arguments of each call (§13), so a call must determine every type argument its function makes values of, wherever the call appears: an argument, a `dbg`, an `assert`, a pipe stage. A function makes values of a type parameter that its signature mentions inside a function type, or two levels inside a parameter type when its result holds that parameter in a collection: `Result.collect` takes `Iter<Result<T, E>>` and returns `Result<List<T>, E>`, so it makes Ts. It also makes values of a type parameter whose bound declares a function that takes no value of the type: `Iter.sum` is bounded by `Summable`, whose `empty_sum()` makes a T from nothing, so `Iter.sum([])` makes a T with no element in hand. A bound whose every function takes a value of the type, such as `Comparable`, is called only on the elements, so `Iter.max([])` makes none. A type argument nothing in the enclosing declaration fixes, by the end of it, is a compile error at the argument that carries it:

```nomi
dbg Result.collect([]) // ✗ the element type of `[]` is not determined

dbg Maybe.values([None]) // ✗ the type argument T of `Maybe.values` is not determined

dbg Iter.map([], fn x -> x) // ✗ the element type of `[]` is not determined: x has it

dbg Iter.sum([]) // ✗ the element type of `[]` is not determined: `empty_sum()` makes a T
```

An unsolved type argument that no value has is not an error: the element of an empty collection (`dbg []`, `Iter.to_list([])`, `List.head([])`, `dbg #[]`, and `Iter.sum([[]])`, whose T is a `List` and whose sum is `[]`) or the variant a value is not (`Ok(1)`, `Result.collect([Ok(1)])`, whose E only passes an error through). Fix the error as a binding: annotate the empty value (`xs: List<Result<Int, String>> = []`) or give the call its type arguments (`Result.collect<Int, String>([])`).

### `once` Bindings

`once name [: T] = expression` declares a value that is computed lazily and cached forever. It appears at file scope or in an inherent `impl` block. The right-hand side evaluates the first time the binding is accessed; the result is stored and reused on every subsequent access. Use sites read like ordinary values — `config.value`, `Int.max_value`, not `config.value()` or `Int.max_value()`. `once` is the language's only "set once, never changes" declaration; trivial constants and lazily-computed values share the same syntax.

```nomi
pub once max_retries = 3 // trivial constant — type inferred
once compiled_email_regex = Regex.compile("^[^@]+@[^@]+$")
once route_table: Map<String, Handler> = build_route_table()
once schema = parse_schema(schema_source_string)

host type Int

impl Int {
    pub once max_value = 9223372036854775807
}
```

Rules:

- **Type annotation is optional.** Inferred from the value when omitted; required in cases where the value's type is genuinely ambiguous (rare in practice). An inferred type holds at every read, in any file and above the declaration. Unannotated bindings whose values read each other have no type to infer, and each is rejected at its declaration with the cycle (`once 'a' has no type annotation and its value depends on its own type (a → b → a)`); annotating one of them settles the others.
- **Container-level only.** `once` cannot appear inside a function body or an interface `impl Iface for Type` block; the parser points at a file-scope or inherent-impl declaration instead. Block-scoped lazy bindings would be a separate design.
- **File scope or an inherent `impl Type` block.** `once` bindings live beside types and functions, or as items of an inherent impl block (`impl Int { pub once max_value = ... }`) — not inside the type declaration itself.
- **No purity enforcement.** The RHS may be any expression valid at file scope. A `once` binding is lazy memoization, no more — like any other function call, the RHS can do whatever the function call could do. The only guarantee is "evaluated at most once." Any side effects in the RHS happen exactly once, on first access.
- **No early exits.** An initializer is not a function body, so it has nothing for `try`, `return`, `break`, `continue` or an assertion to exit. One written directly in the initializer is a compile error at the keyword (`once port = try parse(text)` is `` `try` cannot be used in the initializer of `once port`: a `once` has no function to return from ``); bind the `Result` or `Maybe` and handle it where it is read, or compute the value inside a function. A lambda, a nested `fn` or a `concurrent` block inside the initializer is a boundary of its own, and an exit there leaves it as it does anywhere else.
- **Cycles are rejected at evaluation time.** If `once a` transitively reads itself while forcing, the runtime reports a cyclic-binding error rather than stack-overflowing.
- **Exportable like other container members.** `once` bindings can carry the `pub` modifier (see §3).
- **A function value has no defaults.** A `once` that holds a lambda or a partial capture is a value, not a declaration: a call through it passes every argument, as a call through a parameter does (§5, *Default Arguments*).

## 3. Visibility

Each declaration may carry the optional `pub` modifier. `pub` makes the declaration visible to other files; without it, the declaration is private. Private means visible only within the declaring file.

```nomi
import std/maybe.Maybe

pub fn parse_input(s: String) -> Result<Config, Error> {
    ...
}

pub struct Config {
    host: String
    port: Int
}

pub enum Error {
    Malformed {line: Int}
    Truncated
}

fn validate_internal(c: Config) -> Bool {
    ...
} // private
```

A file qualifies the declarations it exports. Other files import the file and call through its file API object (`io.print(...)`, `#io.Error.NotFound{...}`). The file API object is only a qualifier: `io` alone, as a statement, a bound value or an argument, is the error `` `io` is a file, not a value ``. Inside the defining file, sibling declarations remain bare (`Error`, `print`). Plain imports are lexical aliases only. An item imported with `pub import` is re-exported by the current file and becomes part of its public surface; a file itself is never re-exported, so a file's public surface is its own declarations and the items it lists.

```nomi
pub enum Error {
    NotFound {path: String}
}

pub fn print(value: String) -> Unit {
    ...
}
```

Rules:

- **Placement.** `pub` is the leftmost modifier on a declaration. Order: `pub [opaque] <keyword> <name> ...`.
- **Applicable to.** `fn`, `struct`, `enum`, `type` (distinct), `interface`, `typealias`, `once`, `host fn`, `host type`, and `import` (a re-export; see below). Structs, enums, and distinct types may also take `opaque` after `pub`: `pub opaque struct Name { ... }`, `pub opaque enum Name { ... }`, or `pub opaque type Name InnerType` (primitive-distinct).
- **Not applicable to.** Interface implementation functions (inherit visibility from the implemented interface), struct fields (inherit from the struct), enum variants (inherit from the enum), and anything declared inside a body, a function's, a lambda's, a test's or a `once` initializer's, which is always block-scoped: `pub` there is the error `` `pub` applies to top-level declarations only ``.
- **Not `export`.** `export fn` (§5, *Exported Functions*) makes a function callable from outside the program, which is a different thing from visible to other files; the two do not combine.
- **Root-level public functions are file API.** A file can publish root-level `pub fn` and `pub host fn`; callers reach them through the imported file API object (`users.find(...)`, `io.print(...)`). Functions and `once`s declared in an `impl` block are not file API: they are reached through their owner (`Duration.seconds(1)`, `Int.max_value`, `Int.to_string(n)`, `Display.to_string(n)`), and a file-qualified spelling such as `duration.seconds(1)` is an error that names the owner.
- **Private declarations are invisible to other files.** No other file can name a private function, type or interface in any spelling: a call (`other.helper()`), a type annotation or type argument (`x: other.Hidden`, `List<other.Hidden>`), a struct literal or pattern head (`#other.Hidden{v: 1}`), an enum variant or constructor (`other.Color.Red`), an interface bound or `impl other.Shape for ...` header, or a selective import (`import other.Hidden`). A file-qualified spelling is the error "file 'other' has no member 'Hidden'", and an import is "'Hidden' is private and cannot be imported". A file may still pass its own private type as the type argument of another file's public generic (`other.ident(#Point{x: 1})`, `#other.Span{start: p, stop: q}`): only the declaring file names the type.
- **Std's internals.** A std file's private declaration is std's own, and so are the internal modules `std/message_format` and `std/money_format`, the host functions behind std/message and std/money. A program naming one is the error `` `message.compiled` is internal to std/message `` (`` `std/money_format` is internal to std/money `` for an import), and the reference does not list them. The function a `message` or `noun` declaration expands into (§41) reaches std/message's private constructors through a hidden import no source can name.
- **No renames at the declaration site.** A declaration is published under its own name. An imported name is re-exported under its local name, the alias when it has one, by `pub import` (see *Re-exporting imported names* below).
- **Opaque qualifier.** `pub opaque struct`, `pub opaque enum`, and `pub opaque type Name InnerType` publish the type's *type name* while keeping its construction surface (constructor, destructuring, field access) private. Applies only to types with a representation — using `opaque` on functions, type aliases, interfaces, `once` bindings, or zero-sized `type` declarations is rejected. See §15 *Opaque distinct types*.
- **Interface implementation functions inherit interface visibility.** A function inside an `impl Iface for Type { ... }` block has no per-function `pub` marker. If the interface is `pub`, other files can call the implementation functions; if the interface is private, those functions stay private too. See §13 for the private-wrapper pattern when you want a public interface's defaults only inside the file.
- **Struct fields and enum variants are not separately publishable.** Field access follows the struct's visibility; enum variant visibility follows the enum.

Keywords are always lowercase: `pub`, `export`, `opaque`, `once`, `fn`, `struct`, `enum`, `interface`, `impl`, `for`, `type`, `typealias`, `as`, `todo`.

PascalCase is a compiler-enforced style rule for type names (`type`, `interface`, `typealias`) and enum variants — it is not a visibility mechanism.

Struct fields are always snake_case — there is no per-field visibility. Enum variant visibility follows the enum.

### Visibility Consistency

A `pub` declaration must not expose private types. The compiler enforces this — it is an error for any of the following:

- A `pub` function's parameter or return type to reference a private type
- A `pub` struct's field type to reference a private type
- A `pub` sum type to embed a private type
- A `pub` interface's function signatures to reference a private type
- A `pub` type alias to reference a private type

```nomi
struct Secret {
    value: String // private — no `pub` modifier on the struct
}

pub enum Wrapper {
    embeds Secret
}

pub fn leak() -> Secret {
    ...
} // error — public function returns private type

pub struct Container {
    data: Secret
}
```

This prevents private types from leaking into the public API. If a type appears in a public signature, it must itself be public.

**Relaxation for opaque types.** When a distinct type is declared `pub opaque type <name> InnerType`, its wrapped representation (the inner type, struct fields, or enum variant payloads) may be private. Outside callers can't reach the representation through the construction surface — that's blocked by the opacity check — so a private wrapped type doesn't leak. This enables the "public handle, private internals" idiom: a type that's reference-able by name but whose shape is hidden. Non-opaque distinct types are still subject to the standard rule above. See §15 *Opaque distinct types*.

### Re-exporting imported names

A file or module can re-export items it imports, making them part of its own public surface. This is opt-in: an `import` is private by default, and `pub import` makes every item the statement selects public, under its local name.

```nomi
// Every selected item, re-exported under its imported name.
pub import std/io.{print, write}

// An aliased item is re-exported under its alias.
pub import other.{Helper as Helpr, Thing}

// An owner type and its variants, re-exported together.
pub import std/bool.Bool.{self, False, True}

// A block re-exports every entry.
pub import {
    add.Add
    lists.List
}
```

Rules:

- **Items only, never a file.** A facade publishes exactly the items it lists. Re-exporting a file is a compile-time error at the `pub`, in every spelling: `pub import leaf`, `pub import leaf as lf`, and a brace list whose `self` is the file (`pub import std/io.{self, Error}`) all report `` `leaf` is a file, and a file cannot be re-exported ``, with a hint to list the items or have importers import the file directly. `self` after an owner type (`Bool.{self, False, True}`) is the type, not a file, and re-exports like any item. A file a facade imports is not one of its items either: `import facade.{leaf}` is the error `` `leaf` is a file that `facade` imports, not an item it exports; import `leaf` directly ``.
- **The whole statement.** `pub` re-exports every item the statement selects, and in a block every entry. A file that re-exports some items and imports others privately writes two statements, an `import { ... }` and a `pub import { ... }`; `nomi fmt` groups each run of imports into those two blocks, the private one first.
- **One name per item.** A re-exported item is public under its local name: the `as` alias when there is one, the imported name otherwise. Importers of the file see the same name the file itself uses.
- **No globbing.** There is no `import path.*` form. Each re-exported name is enumerated. This keeps the public surface explicit and prevents upstream changes from silently expanding a downstream module's API.
- **Visibility.** A re-exported name's upstream definition must already be public — `pub import other.{private_thing}` errors at the import site, since `private_thing` isn't reachable to begin with.
- **Imported names only.** `pub import` applies only to imported names. To expose a local definition under a different name, rename it or write a wrapper.

## 4. Naming Conventions

| Entity               | Convention              | Example                        |
| -------------------- | ----------------------- | ------------------------------ |
| Types (`type`, `interface`, `typealias`) | PascalCase (required) | `User`, `HttpRequest` |
| Enum variants        | PascalCase (required)   | `Some(T)`, `Active`, `True`    |
| Type parameters      | PascalCase (required)   | `T`, `Elem`                    |
| Functions            | snake_case              | `fn parse_input()`             |
| `once` bindings      | snake_case              | `once max_retries = 3`         |
| Struct fields        | snake_case              | `name`, `email`, `age`         |
| Variables/bindings   | snake_case              | `user_name`, `max_retries`     |
| Modules              | snake_case              | `users`, `http`, `math_utils`  |

Visibility is declared by the optional `pub` modifier on each declaration (see §3). PascalCase is a style rule for types and enum variants, not a visibility mechanism. The lexer enforces the leading-character rule (lowercase identifiers are `IDENT`, uppercase are `TYPE_IDENT`); the analyzer further checks that PascalCase names contain no underscores and snake_case names contain no uppercase letters, so violations like `Http_Request` or `userName` are flagged at compile time.

A snake_case identifier may carry a single trailing `?` (the predicate convention) — `empty?`, `starts_with?`, `String.contains?`. The `?` attaches only when glued to the identifier's tail (no intervening whitespace), only one is allowed, and only at the very end. It applies to lowercase identifiers only — types, enum variants, and numbers never take a trailing `?` — and to any kind of identifier (function names, bindings, and parameters), not just functions.

A binding-position identifier that starts with a single `_` is a discard name. It still follows snake_case spelling, but it intentionally does not enter scope.

## 5. Functions

### Named Functions

```nomi
fn add(x: Int, y: Int) -> Int {
    x + y
}
```

- Type annotations required on parameters and every non-`Unit` return type; omitting the return annotation means the function returns `Unit`. A parameter written without a type, in a `fn`, an impl's or an interface's function, or a `host fn`, is the error `parameter 'x' needs a type annotation`; an interface implementation's parameters are written out, not taken from the interface. A parameter needs a name too: a type alone, `fn echo(String) -> String`, is the error `parameter 'String' needs a name`, for a `go`-bound `fn` as for any other. Only a lambda's parameter types may be inferred. A lambda's parameter needs a name as well: `fn String -> 1` is the same error, whether or not a function type is expected for the lambda.
- Types inferred within function body
- Function bodies always render across multiple lines — `fn add(x: Int, y: Int) -> Int { x + y }` reformats to the multi-line shape shown above. This matches the dominant convention from gofmt, Prettier, dart format, zig fmt, swift-format, and rustfmt's default. Empty bodies stay flat as `{}`. The rule applies to top-level `fn`s, impl-block functions, and interface default functions; inline expression-position blocks like `if cond { x }` keep their flat form (see §11).
- Last expression is the return value
- `return` keyword for early exit — `return value` exits the nearest function boundary with `value`. This applies equally to named functions (`fn name(...)`) and lambdas (`fn ... -> ...`). Nomi has no non-local control flow: `return`, `break` and `continue` each leave only the function they are written in, with no exceptions (§12 *Named Functions as Callbacks*). Bare `return` is valid when the function returns `Unit`. A bare `return` that the body would reach its end without is a compiler error: one that is the last statement of a function, lambda or test body, reached through the last statement of a block, either branch of a final `if` or any arm of a final `case` (`` this `return` does nothing: it is the last statement of `reset` ``), and a final `if` or `case` whose every branch is empty or a bare `return` (`` this `if` does nothing: every branch returns and nothing follows it ``). A bare `return` after a `dbg`, or after an assertion outside a test body, is not one: it discards that statement's value. A tail `return value` is legal, and `nomi fmt` rewrites it to `value`. A statement after one that always exits can never run, and is a compiler error at that statement whose help is "remove it". A statement always exits when it is a `return`, `break` or `continue` (`unreachable code after return`); an `if` with an `else` whose every branch always exits (`` unreachable code: the `if` above returns in every branch ``); a `case` whose every arm always exits (`` unreachable code: the `case` above returns in every arm ``); or a block statement that holds one (`unreachable code: the block above always returns`). An `Iter.loop` statement whose callback holds no `break` of its own, a lambda or a named step passed as the callback, never finishes, and counts the same way (`unreachable code after a loop that never breaks`, with the help "remove it, or give the loop a `break` that ends it"); see §12 *`loop`*. A `todo` statement is `Unit` and does not exit, so code after a stub stays legal while it is written, and no other call counts as an exit.
- `try x` is sugar for `case x { Ok(val) -> val, Err(e) -> return Err(e) }` (and similarly for `Maybe`). Since `return` exits the nearest function boundary, `try` inside a lambda returns from that lambda, not from an outer function.
- `defer call(args)` evaluates the call's operands where it stands and runs the call when the block it is written in ends, the most recently registered first. A function's or a lambda's body is such a block, so a `defer` in a lambda runs when that invocation of the lambda ends, and one in an `Iter.loop` or other iteration callback runs at the end of each iteration, before the next starts. Every way out of the block runs them: reaching its end, `return`, a callback's `break` or `continue`, `try` propagating a failure, and a trap.
- `->` separates the parameter list from the return type: `fn add(x: Int, y: Int) -> Int`. In a generic function the return type comes before the `where` clause: `fn shown<T>(x: T) -> String where T: Display { ... }`.
- Data-first convention — the primary data argument comes first (works naturally with `|>`)
- Named functions are first-class values. Reference without parens, call with parens:
  ```nomi
  handler = process_request // function reference

  handler(request) // call it

  Iter.map(users, format_user) // pass named function as argument
  ```
- Only a function value can be called. Calling a value of any other type is an error at the callee: `1()` is `` `Int` is not a function ``, and so is calling a tuple, a list or a struct value.
- A generic function used as a value is instantiated against the function type its position expects, as a call is instantiated from its arguments, and its `where` bounds are checked at the types that instantiation picks: `Iter.each(names, io.print)` passes `io.print` at `(String) -> Unit`, and `f: (Int) -> Int = ident` binds `ident` at `Int`. A position that leaves a type parameter unsolved, such as the unannotated binding `f = ident`, is a compile error asking for an annotation.
- `fn` declarations may also appear inside a function body. A nested `fn` is scoped to its enclosing block — visible to siblings in the same block (and inner blocks) but invisible outside. Nested fns can close over names in scope, just like lambdas. They cannot carry `pub` (only file-level declarations can).
  ```nomi
  fn classify_all(xs: List<Int>) -> List<Int> {
      fn classify(n: Int) -> Int {
          // helper, only visible inside classify_all
          if n < 0 { return 0 }
          if n > 100 { return 100 }
          n
      }

      xs |> Iter.map(classify) |> Iter.to_list()
  }
  ```
- A `struct`, `enum`, `type`, `typealias` or `interface` may also be declared inside a function body, scoped as a nested `fn` is. Its name is a type in every annotation in that block and its inner blocks: a binding's (`m: Meters = Meters(3)`), a lambda parameter's and its default's, a function type, and a type argument (`ident<Meters>(m)`). An `impl` block or a `derive` declaration may not: behavior is declared at module top level, and one inside a body is the error `` an `impl` block must appear at module top-level, not inside a body `` (or `` a `derive` declaration ... ``).

### Default Arguments

Defaults can appear on any parameter:

```nomi
fn connect(_host: String, _port: Int = 8080, _timeout: Int = 30) {
    // ...
}

connect("localhost") // port = 8080, timeout = 30

connect("localhost", 3000) // timeout = 30

connect("localhost", 3000, 10) // all explicit

connect("localhost", _timeout: 10) // skip port, use default
```

A default is checked against its parameter's type, on a `fn` and on a `host fn` alike: `host fn pad(s: String, width: Int = "wide") -> String` is the error `default value for parameter 'width' is String, expected Int`. A `host fn`'s or `go` binding's default is Nomi code like a `fn`'s: it may name the declaring file's functions and an earlier parameter, and a call that omits the argument evaluates it before the call leaves Nomi.

When a default is **not** trailing, positional args still fill consecutive slots from the start, so to leave a non-trailing default empty you switch to named args for everything after it:

```nomi
fn middle(a: Int, b: Int = 10, c: Int) -> Int {
    ...
}

middle(1, 2, 3) // a=1, b=2, c=3

middle(1, c: 3) // a=1, b=10 (default), c=3

middle(1, 2) // error: missing argument for parameter 'c' (positional can't skip b)
```

An omitted default is evaluated once per call, after the written arguments, in parameter order; a default may refer to earlier parameters.

A default is not part of the function's body, so `try`, `return`, `break`, `continue` or an assertion written directly in it is a compile error at the keyword: `fn f(x: Int = try parse(s))` is `` `try` cannot be used in the default of parameter 'x': a default has no function to return from ``. A lambda's defaults follow the same rule. An exit inside a lambda that the default builds is the lambda's own.

The one exception is the **trailing-lambda routing** described in the next subsection: a lambda passed as the final positional argument can skip earlier defaulted slots without naming.

**Defaults apply to calls, not to function values.** A function named as a value has its full parameter list, defaulted parameters included, so it does not fit a function type with fewer parameters. Passing it, binding it, or piping it where the shorter type is expected is a compile error that suggests the lambda to write instead, which says which arguments the defaults fill:

```nomi
fn parse(s: String, strict: Bool = False) -> Maybe<Int> {
    ...
}

parse("12") // OK — a call; strict = False

Iter.map(words, parse) // error: expected (String) -> U, got (String, Bool) -> Maybe<Int>

f: (String) -> Maybe<Int> = parse // error: the same mismatch

Iter.map(words, fn w -> parse(w)) // OK

Iter.map(words, &parse(&1)) // OK — a partial capture leaves strict to its default
```

A call through the value passes every argument too, since only a call of `parse` by name fills its defaults. After `g = parse`, `g("12")` is the error `` expected 2 arguments, got 1: `g` holds the function `parse` as a value, and defaults apply only to a call of `parse` by name ``, whose help suggests calling `parse` directly or binding `g = fn s -> parse(s)`. The same holds for `"12" |> g()`, for `(parse)("12")`, and for a value passed through a parameter, a field or a generic function. The value's parameters have no names either (*Named Arguments* below). A lambda's own defaults (*Lambda Default Parameters*) and a partial capture's open defaults belong to the name the value is bound to and its copies, so `h = fn x = 1 -> x` is called as `h()`, and so is `g = h`, by a call or a pipe (`1 |> g()`). The value passed on or taken out of a container has no defaults (*Named Arguments*), and neither has one reached through anything but a local name: `(h)()`, `(if b { h } else { k })()` and a pipe into a call's result each pass every argument. A `once` holding a lambda or a partial capture (`once ping = &connect(&1, port: &2)`) has no defaults either: its value is computed once in its own initializer, so `ping("a")` is `expected 2 arguments, got 1`, and `ping("a", 80)` runs.

### Parameter Destructuring

A function or lambda parameter may be an **irrefutable** destructuring pattern instead of a plain name — the same pattern shapes a binding uses (§7). The body sees the bound sub-names directly:

```nomi
fn subtract(Duration(x), Duration(y)) -> Duration {
    Duration(x - y)
} // distinct, self-typed

fn sum_pt(#Point{x, y}) -> Int {
    x + y
} // typed struct, self-typed

fn add_pair(#(a, b): #(Int, Int)) -> Int {
    a + b
} // tuple needs an annotation

fn rename(#{name: n, age: a}: Person) -> String {
    n
} // anon struct, renaming fields

sub = fn Duration(x), Duration(y) -> Duration(x - y) // lambdas too
```

**The self-typing annotation rule.** A parameter's type comes from *either* its `: Type` annotation *or* a pattern whose head names a concrete type. So:

- **Self-typing → annotation optional.** A pattern whose head names a type carries that type itself: distinct `Duration(x)`, a distinct type over a tuple `#Pair(a, b)`, typed struct `#Point{x, y}`, qualified single-variant enum `E.V(x)`. Writing the redundant annotation (`Duration(x): Duration`) stays legal but `fmt` strips it. A head with nothing after it keeps its annotation: `fn f(Label)` is a parameter with a type and no name, an error (`parameter 'Label' needs a name`), so `fmt` leaves `Label: Label` alone.
- **Type-less → annotation required.** A pattern with no type head has nothing to infer from, so it needs `: Type`: tuple `#(a, b): #(Int, Int)`, anonymous struct `#{x, y}: Point`, dot-enum `.V(x): E`, and the plain `x: Int`. A generic newtype without type arguments (`Box(x)`) also still needs an annotation — it can't pin its type parameters from the head alone.

**Only irrefutable patterns are allowed.** A pattern is irrefutable when *every* value of the parameter's type necessarily matches it. Refutable patterns are **rejected by design** with a diagnostic pointing the author at a body `case`:

- literals (`42`, `"x"`, `true`)
- multi-variant enum variants (`.Some(x)`, `Color.Red`) — a value of the enum may be a *different* variant
- map patterns (`%{"k" => v}`) — the key may be absent at runtime
- list and vector patterns (`[a, b]`, `[h, ..t]`, `#[a, b]`, `#[h, ..t]`) — the length may not match; a vector pattern is refused in a parameter even when it would match every vector (`#[..all]`)
- set patterns (`%["a", ..rest]`) — the element may be absent
- any pattern nesting a refutable sub-pattern

A **single-variant** enum is irrefutable (every value is that one variant), so `E.V(x)` / `.V(x)` is accepted; a multi-variant enum variant is not. The distinction is type-driven: `Duration(x)` (a distinct, irrefutable) and `Some(x)` (an enum variant, refutable) are syntactically identical, and only resolving the head against the parameter's type tells them apart. Matching *into* one variant of a multi-variant enum is still a refutable `case` in the body — that's where conditional, possibly-failing matches belong.

**Multi-clause / overloaded definitions remain deferred** (§5 "No Overloaded Functions"): because Nomi has no fall-through between clauses, a refutable parameter pattern would have nowhere to go on a non-match. Restricting parameters to irrefutable patterns is the principled boundary (the same stance as Rust and Swift).

### Lambdas at Call Sites

Lambdas are always passed explicitly inside the argument parens — there is no trailing-lambda block sugar. A function with a final lambda parameter is called like any other:

```nomi
fn do_thing(required: Int, _option_a: String = "default", f: (Int) -> Int) -> Int {
    f(required)
}

do_thing(5, fn x -> x + 1) // skip optionals — lambda routed to f

do_thing(5, option_a: "custom", fn x -> x + 1) // override option_a; the trailing callback still routes to f
```

**Trailing-callback routing:** when the function's last parameter is still empty, the *final positional* argument is automatically routed into that last slot — even if defaulted parameters sit between it and the preceding positional args. This lets a required trailing callback stay unnamed in the common pattern (`Iter.each(items, fn x -> ...)`, `Iter.reduce(items, fn acc = 0, x -> ...)`) without forcing the caller to name it whenever a middle default is skipped.

The routing is decided by the parameter list, not by how the argument was written, so `each(items, process)` routes exactly like `each(items, fn x -> process(x))`. A lambda routes on sight, and so do a field accessor (`Iter.sort_by(users, .age)`, §8 Field Accessors) and a capture, a partial one included (`Iter.sort_by(names, &String.length(&1))`, `do_thing(5, &(&1 * 2))`, §5 Captures). An argument written as a bare name routes when the last parameter takes a function and the slot it would otherwise fill does not — where both take functions there is a genuine choice, and the left-to-right order the call already states wins.

A single trailing positional may also follow named arguments (`each(items, opts: cfg, process)`): the named argument has already claimed its slot, so the positional skips past it. A positional written *before* a named argument still means the slot it sits in, which keeps `sum(1, a: 2)` the duplicate it reads as.

Defaults otherwise work as described in §5 Default Arguments; the call-site rule for lambdas is just "args go in `(...)`."

`concurrent { ... }` in §20 is a keyword-level block construct, not trailing-lambda sugar. Task work inside it is spawned with ordinary calls such as `Task.spawn(fn -> work())`.

### Named Arguments

Callers can name arguments at the call site. Positional and named can be mixed — positional must come first, with one exception: a single trailing positional is allowed after named args, so the common callback pattern doesn't force the user to name the lambda. It fills the first slot the named arguments left empty (see §5 Lambdas at Call Sites):

```nomi
connect("localhost", port: 3000, timeout: 10) // mixed

connect(host: "localhost", port: 3000) // all named

connect(timeout: 10, "localhost") // OK — the lone trailing positional fills host

each(items, opts: cfg, fn x -> process(x)) // OK — trailing callback after named arg
```

More than one positional after a named argument is a parse error:
`connect(timeout: 10, "localhost", 3000)` reports `positional argument after
named argument`.

**Resolution order:**
1. Positional args fill slots `0..N` left-to-right, where `N` is the number of positional args at the call site. (Exception: trailing-lambda routing — see §5 Lambdas at Call Sites.)
2. Named args fill remaining slots by name.
3. Defaults backfill any still-empty slots.
4. A required slot left empty is a compile error.

A named arg whose name matches a slot already filled positionally, or by an earlier named arg (`add(x: 1, x: 2)`), is a compile error (`parameter 'x' already has a value`), reported at the later one. Reorder the call — switch the colliding positional to named, or drop the redundant name:

```nomi
fn add(x: Int, y: Int) -> Int {
    x + y
}

add(1, 2) // OK — positional

add(x: 1, y: 2) // OK — all named

add(1, y: 2) // OK — positional fills x, named fills y

add(1, x: 2) // error — x already filled by the positional 1
```

This rule also governs a partial capture: a named `&N` targets its named slot, and any positional `&N` or value still fills `0..N` strictly. The `&N` may be named (`port: &1`); either way, the partial's parameter at that slot takes the callee's name for it (see §5 Captures).

A name needs a declaration to name. A declared `fn`, a lambda bound to a name and a partial capture bound to a name have parameter names. A value whose type is only a function type does not: a parameter or struct field typed `(String, Int) -> String`, a binding that copies another function value (`g = connect`), a call's result, or a value taken out of a `Maybe` or a list. A named argument through such a value, in a call or a partial capture, is the error `no parameter named 'port'`, whose help says a function type's parameters have no names and to pass the arguments by position. Such a value has no defaults either, even when it came from a function with some, except a copy of a lambda or partial capture bound to a name, which keeps them: a call through it passes every argument, and leaving one out is the error `expected 2 arguments, got 1`, whose help says a function type's parameters have no defaults.

A positional construction has no names either: a variant's payload (`Some(1)`, `Shape.Square(2.0)`, `.Square(2.0)`, `&Some(&1)`), a distinct type's wrapped value (`UserId(5)`) and a struct's record call form (`Point(#{x: 1})`). `Some(value: 1)` is the error `no parameter named 'value'` at the name, and its help says the payload is positional. An interface-qualified call takes the interface function's own parameter names: `Display.to_string(value: 1)`, `T.to_string(value: x)` and `Struct.update(updates: #{x: 2}, original: p)`.

### Anonymous Functions (Lambdas)

Lambda syntax is `fn params -> body`. The parameters are written as a
function's are, without parentheses: names, annotations, defaults and
irrefutable patterns, separated by commas. The first `->` after them that is
not part of a parameter's type annotation ends them, so a parameter of function
type keeps its own arrow: `fn f: (Int) -> Int -> f(1)` takes `f` and calls it.
The body is a single expression. A body of several statements is a block,
`fn x -> { ... }`.

`fn` starts a declaration when a lowercase name and `(` or `<` follow it
(`fn helper(x: Int) -> Int { ... }`, `fn id<T>(x: T) -> T { ... }`), at the
top level and inside a block; anything else after `fn` starts a lambda
(`fn x -> x`, `fn -> 1`, `fn #(a, b) -> a`, `fn Duration(ns) -> ns`). A
declaration's `->` comes after its parameter list's `)`; a lambda's comes
after its parameters. `|x| x + 1` is not a lambda: `|` is a parse error. In a call's arguments, a line below an unbraced lambda body, indented deeper than the line the lambda starts on, is the error `this lambda's body continues on the next line; a lambda body with more than one statement needs braces`, and the language server offers a quick fix that adds the braces.

A block body's result type is inferred from its final expression and its
explicit `return` values. All returning paths must agree; bare `return`
contributes `Unit`. Returns inside a nested function, lambda or `concurrent`
block belong to that inner boundary.

A lambda with no parameters writes nothing between `fn` and `->`:

```nomi
// no args
fn -> fetch_user(42)

// single arg
Iter.map(users, fn u -> u.name)

Iter.filter(users, fn u -> u.age > 18)

// multiple args, the first with a default: reduce's starting value
Iter.reduce(items, fn total = 0, item -> total + item.value)

// irrefutable destructuring in parameters — see §5 Parameter Destructuring
Iter.map(pairs, fn #(label, value) -> "  ${label}: ${value}")

Iter.map(pairs, fn #(_, value) -> value)

Iter.map(durations, fn Duration(ns) -> ns) // distinct, self-typed

// lambda parameters with default values
Iter.loop(fn attempts = 0 -> {
    if attempts >= 3 { break attempts }
    attempts + 1
})
```

There is no implicit `it` parameter — every lambda names its parameters explicitly.

A parameter's type comes from its annotation, from its default, or from the function type the lambda's position expects: a call's parameter, an annotated binding, a return. A generic call's parameter counts when the call's own expected type fixes it: in `f: (Int) -> Int = ident(fn x -> x + 1)`, `ident`'s `T` is `(Int) -> Int`, so `x` is an `Int`. A generic constructor's field or payload counts the same way: in `f: Box<(Int) -> Int> = #Box{v: fn x -> x + 1}` (or `Box(#{v: ...})`) and `g: Maybe<(Int) -> Int> = Some(fn x -> x * 3)`, `x` is an `Int`. A lambda in a position that expects no type, such as an unannotated binding (`f = fn x -> 1`) or a tuple or list element (`#(fn x -> 1, 2)`), must annotate each parameter that has no default; otherwise it is `cannot infer type for parameter x`. A later use never types a parameter.

### Lambda Default Parameters

Lambda parameters can have default values. This is especially useful with `Iter.loop` where the defaults provide the initial state:

```nomi
// Count the failed attempts before a fetch succeeds
failures = Iter.loop(fn attempts = 0 -> {
    case fetch(url) {
        Ok(_) -> break attempts
        Err(_) -> attempts + 1
    }
})

// Tuple state — the annotation and the default attach to the whole tuple
// parameter, not to its elements
Iter.loop(fn state = #(n / 2.0, 0.0) -> {
    #(guess, prev) = state
    diff = guess - prev

    if diff > -0.0001 and diff < 0.0001 {
        break state
    }

    next = (guess + n / guess) / 2.0
    #(next, guess)
})
```

### Multi-Expression Bodies

The body of a lambda is a single expression. To run multiple statements, use a block expression — `{ ... }` is itself an expression whose value is its last expression (see §33):

```nomi
fn x -> {
    doubled = x * 2
    doubled + 1
}
```

The block is the body; the lambda still has a single-expression body, that expression is the block.

### Type Annotations

Type annotations use `(Args) -> Return` for function types — `fn` defines functions, `(Args) -> Return` is the type:

```nomi
fn handler(db: database.Db) -> (Request) -> Response
```

**A function value fits a function type when it accepts every argument and its result fits.** A function of type `(P1) -> R1` may stand where `(P2) -> R2` is expected when each `P2` fits its `P1` (parameters are contravariant) and `R1` fits `R2` (results are covariant). Inside a function type, one type fits another when they are the same, when a concrete type meets an interface it implements (a `List<Int>` where an `Iter<Int>` is expected, an `Int` where a `Display` is), when a type meets an enum that `embeds` it, or when both are function types that fit by this rule. Type arguments are compared exactly: a `List<(Display) -> String>` is not a `List<(Float) -> String>`. The rule holds at every position a function value flows into: an argument, an annotated binding, a list element, a field, a result and a generic parameter.

```nomi
fn shown(x: Display) -> String {
    Display.to_string(x)
}

fn count(xs: Iter<Int>) -> Int {
    Iter.count(xs)
}

fn pair(n: Int) -> List<Int> {
    [n, n]
}

fn examples() {
    f: (Int) -> String = shown // Int fits Display
    g: (List<Int>) -> Int = count // List<Int> fits Iter<Int>
    h: (Int) -> Iter<Int> = pair // the result List<Int> fits Iter<Int>
}
```

The reverse is a compile error, since the function would receive a value it does not accept: `fn float_str(f: Float) -> String` is not a `(Display) -> String`, and `fn total(xs: List<Int>) -> Int` is not an `(Iter<Int>) -> Int`. Where nothing says which way a value flows, two function types must be the same: the branches of an `if` or `case`, the elements of an unannotated list, and two arguments for one type parameter (`pick(c, shown, float_str)` with `fn pick<T>(c: Bool, a: T, b: T) -> T`). Pass a lambda of the one type instead (`pick(c, float_str, fn x -> shown(x))`).

### Captures

A capture is a function written in place: `&` and the function's body, in
which `&1`, `&2`, ... stand for its parameters.

```nomi
big = Iter.filter(xs, &(&1 > 2)) |> Iter.to_list()

points = Iter.map(xs, &#Point{x: &1, y: 0}) |> Iter.to_list()

lengths = Iter.map(names, &String.length(&1)) |> Iter.to_list()

add: (Int, Int) -> Int = &(&1 + &2)
```

The `&` is written against the body, with no space, and the body is one of
these:

- a call: `&Int.max(&1, 0)`, `&String.length(&1)`, `&Some(&1 + 1)`,
  `&T.from_json(&1)`, `&f(&2, &1)`;
- a parenthesized expression: `&(&1 > 2)`, `&(&1 * 2 + 1)`,
  `&(&1 % 2 == 0)`, `&(&2)`, `&(!&1)`;
- a literal or a construction: `&#Point{x: &1, y: 0}`, `&#(&1, 0)`,
  `&#.Circle{radius: &1}`, `&%{&1 => 0}`, `&[&1, 0]`, `&#[&1]`, `&%[&1]`.

The body is one postfix-level operand, as a prefix `try`'s is. So
`&f(&1)(2)` captures the call `f(&1)(2)`, and `&f(&1) + 1` adds 1 to a
capture, which is a type error; the function of the sum is
`&(f(&1) + 1)`. Anything else after `&` is the parse error `` a capture
is `&` and a call, a parenthesized expression or a literal, as in `&f(&1)`
or `&(&1 > 2)` ``: `&x`, `&f(&1).x` (write `&(f(&1).x)`), and `&Int.max`,
which is no call (the function itself is `Int.max`). `& (x)`, with a
space, is the error that says the `&` goes against its body.

`&N` is parameter N of the capture around it: `&(&1 > 2)` is `fn x -> x >
2`, `&(&2 - &1)` is `fn a, b -> b - a`, and `&#Point{x: &1, y: 0}` is `fn
x -> #Point{x: x, y: 0}`. `&` followed by a decimal number from 1, with
no leading zero, is one token; `&0` and `&01` are errors that say so, and
`&&` keeps its own error (Nomi spells it `and`).

Each `&N` belongs to one capture, so captures do not nest: `&f(&1,
&g(&1))` is the error `` captures do not nest; write the inner one as `fn
x -> ...` ``, whose help names the inner capture. A lambda is a function
of its own: a capture inside a lambda inside a capture is no nesting, and
an `&N` in a lambda's body belongs to no capture outside the lambda.

An `&N` outside every capture is the error `` `&1` is a capture's
parameter: write the function as `&(...)` or `&f(...)` ``. Its help writes
the capture the `&N` most likely belongs to: the call it is an argument of
(`Iter.map(xs, Int.max(&1, 0))` says to write `&Int.max(&1, 0)`), the
expression an operator, a range, a field read or parentheses make of it
(`Iter.map(xs, &1 * 2 + 1)` says `&(&1 * 2 + 1)`), or the literal it is a
field or element of (`&#Point{x: &1, y: 0}`). In a pipe stage the help
says the pipe fills the first argument (§6, *The `then` stage*). A
capture with no `&N` in it is the error `` a capture uses at least `&1`
``, whose help writes the function of no arguments as a lambda (`fn ->
f()`).

`_` is not a capture's parameter. It is a discard name, in a pattern or as
a parameter or binding name (§2, *Bindings*). Read as a value, `_` is the
error `` `_` is a discard name, not a value ``. When the `_` is a call's
argument, the help writes the call as a capture with its parameters
numbered: `add(1, _)` says to write `&add(1, &1)`, `connect(_, port: _)`
says `&connect(&1, port: &2)`, and a pipe stage's `|> add(100, _)` says
`|> then &add(100, &1)`.

#### Parameters and their types

The function type a capture's position expects fixes how many parameters
it has and their types, as it gives an unannotated lambda's (*Anonymous
Functions* above): a call's parameter (`Iter.map(xs, &(&1 * 2))` solves
`Iter.map`'s result from the body), an annotated binding, a field of
function type (`#Rule{keep: &(&1 > 2)}`), a return, and the piped value
of a `then` stage. Each `&N` must name one of those parameters:
`Iter.map(xs, &(&2 + 1))` is `` `Iter.map` passes one argument; `&2`
names a second ``, and `g: (Int) -> Int = &(&1 + &3)` is `` the function
expected here takes one argument; `&3` names a third ``. A parameter no
`&N` names is ignored, so `apply2(&(&2))`, where `apply2` takes an
`(Int, Int) -> Int`, is `fn _a, b -> b`. One `&N` may stand several times
(`&(&1 * &1)` is `fn x -> x * x`), and the parameters in any order
(`&f(&2, &1)`).

With no function type expected, `f = &(&1 > 2)` is `` cannot infer type
for `&1` in `&(&1 > 2)` ``, whose help says to annotate the binding (`f:
(Int) -> Bool = &(&1 > 2)`) or to write a lambda with an annotated
parameter (`fn x: Int -> x > 2`). A capture that is the argument of a
construction is this error when nothing expects the construction to hold
a function, and its help writes the construction as the capture:
`Some(&(&1 + 1))` is a `Maybe` holding a function, and `&Some(&1 + 1)` is
a function answering a `Maybe`.

A partial capture is the one that needs no expected type: a capture whose
body is a call taking each of its `&N` as one of its own arguments,
positional or named, numbered `&1`, `&2`, ... in the order they are
written, each once (`&add(1, &1)`, `&connect(&1, port: &2)`). It is a
partial application of the callee. Its parameters are the callee's
parameters at those slots, with their types, names and defaults, and it
is held to the callee's argument count as a full call is: `&add(&1, 1,
2)` against a two-parameter `add` is `expected 2 arguments, got 3`. A
generic callee's type parameters are solved from the expected function
type when there is one: `f: (Int) -> Int = &pick(True, &1, 4)`
instantiates `pick` at `Int`. An expected function type of more
parameters gives the ones past the partial's, which it ignores
(`apply2(&add(&1, 100))` is `fn a, _b -> add(a, 100)`); one of fewer is
the error for an `&N` past the arity. A call whose `&N`s repeat, come in
another order or stand deeper than its own arguments (`&add(&1, &1)`,
`&add(&2, &1)`, `&Some(&1 + 1)`) is no partial, and takes its types from
the expected function type, and so is a parenthesized call (`&(add(1,
&1))`).

```nomi
halve = &divide(&1, 2)

divide_from_10 = &divide(10, &1)

connect_local = &connect("localhost", port: &1, timeout: 10)

connect_local(3000) // connect("localhost", 3000, 10)
```

Partial application with defaults:

```nomi
f = &connect(&1, port: &2)

f("localhost", 3000) // timeout uses default 30

f("localhost") // port uses default 8080, timeout uses default 30
```

A partial's parameters are the callee's parameters at its open slots, positional and named alike, with the callee's names and defaults. So a partial bound to a name is called by those names, partially applied again, and leaves a defaulted open slot to its default:

```nomi
import std/io

fn connect(host: String, port: Int = 8080, timeout: Int = 30) -> String {
    "${host}:${Int.to_string(port)}/${Int.to_string(timeout)}"
}

fn main() {
    c = &connect(&1, port: &2)
    io.print(c(port: 80, host: "x")) // x:80/30
    io.print((&c(port: &1, host: "x"))(9)) // x:9/30
    io.print(c("y")) // y:8080/30
}
```

Any other capture's parameters have no names and no defaults, except a
struct literal whose `&N`s are its whole fields, each once, which takes
the fields' names: after `mk: (Int) -> Point = &#Point{x: &1, y: 0}`,
`mk(x: 3)` is `#Point{x: 3, y: 0}`, and `big(lhs: 3)` after `big: (Int)
-> Bool = &(&1 > 2)` is `no parameter named 'lhs'`. A field's default
fills a field the literal leaves out, and a field written `&1` is not left
out, so a call passes every argument.

#### Evaluation

A capture is a function value, and nothing in its body runs when it is
made. A call of it evaluates the whole body with its parameters filled,
as the equivalent `fn` does: `big: (Int) -> Bool = &(&1 > limit())` calls
`limit` on each call of `big`, and `f: (Int) -> Int = &add(noted(1),
&1)` calls `noted` on each call of `f`. A partial capture's written
arguments run in the order its call would run them: the positional ones,
then the named ones, each in the order they are written (*Named
Arguments* above).

A capture routes to a trailing callback as a lambda does
(`do_thing(5, &(&1 * 2))` past a defaulted parameter, *Lambdas at Call
Sites* above), and it is no reduction's starting value
(`Iter.reduce(xs, &(&1 + &2))` is `` `Iter.reduce` needs a starting value
``; `Iter.reduce(xs, &(&1 + &2), from: 0)` gives it one). It is an operand like any other value: in `&(&1 > 2) and ready`
nothing expects a function of the capture, so it is `` cannot infer type
for `&1` in `&(&1 > 2)` ``, and the function of the whole condition is
`&(&1 > 2 and ready)`.

### No Auto-Currying

Functions are not automatically curried. Use a partial capture (`&add(1, &1)`) instead.

### No Overloaded Functions

Each function name maps to exactly one definition in a scope. Nomi does not
choose between same-named functions by argument type; use descriptive names
(`Date.parse`, `Time.parse`, `String.to_int`) and use interfaces
for polymorphism. Interface implementation functions may share a contract name
across implementing types, but the callable surface is still qualified by a
type or interface (`Dog.speak(dog)`, `Speech.speak(dog)`), not selected
from a same-scope overload set.

### Unwritten Code: `todo`

`todo` is a placeholder for code not written yet. It is an expression, written
bare or with a reason:

```nomi
fn parse(text: String) -> Header {
    todo "parse the header"
}

fn area(shape: Shape) -> Float {
    case shape {
        .Circle(r) -> 3.14 * r * r
        .Square(_) -> todo
    }
}
```

- **Type.** `todo` never produces a value, so it has the type its position
  expects: a function's result, an annotated binding, an argument, a struct
  field, a `case` arm or `if` branch (whose type its other arms decide), a
  lambda body, a pipe stage (`items |> Iter.to_list() |> todo`), a generic
  function's result, an `if` condition or an operand whose type its operator
  decides (`!todo` and `todo and ok` are `Bool`, `n == todo` has `n`'s type,
  `1 + todo` is the type `+` takes on the right). As a statement it is `Unit`. A binding with no
  annotation expects nothing, so `x = todo` is an error asking for one
  (`x: Int = todo`); `_ = todo` is fine.
- **Reason.** The reason is a string literal: `"..."`, `"""..."""` or a raw
  backtick string. It is fixed text that tools list without running anything,
  so an interpolated or tagged string is a parse error.
- **Running it.** A program that reaches a `todo` traps (§17 *Traps, `Maybe` and `Result`*)
  with `todo reached at <file>:<line>` and then `: <reason>` when there
  is one. The file is shown relative to the working directory. A `todo` on a
  path the run does not take does nothing. As a pipe stage, the piped value is
  computed first.
- **Unread parameters.** A function or lambda whose body contains a `todo` is
  not finished, so its parameters are not reported as never read. Likewise the names
  a `case` arm, an `else` arm, an `if` condition or a test's setup binding
  binds are not reported when the body they scope over contains a `todo`.
- **Tooling.** `nomi check`, `nomi run`, `nomi test` and the REPL accept
  `todo`. `nomi build` refuses a program with a `todo` anywhere in its files,
  reached or not, and lists each one:

  ```text
  nomi build: 2 todos remain:
    main.nomi:12:5 todo "parse the header"
    shapes.nomi:30:23 todo
  ```

  It refuses a `dbg` the same way (§6, *Debugging pipes*), and a program with
  both gets one list, in file and source order, under one header
  (`1 todo and 2 dbgs remain:`).

  The LSP reports every `todo` as a warning, as it does `dbg`, because it is
  code that is not written yet; the message carries the reason. The quick
  fixes that write missing functions, fields and `case` arms fill each hole
  with `todo`, so their result checks.

### Exported Functions

`export fn` marks a top-level function that a host calls by name: a web page
through `nomi build --web`, a WASI runtime through the reactor `nomi build
--target wasip1/wasm` writes, and in time a Go embedder. Its arguments and its
result cross as JSON, through `ToJson` and `FromJson`.

```nomi
import std/json.{FromJson, ToJson}

struct Game {
    word: String
    guesses: List<String>
}

derive ToJson for Game
derive FromJson for Game

export fn new_game(word: String) -> Game {
    #Game{word, guesses: []}
}

export fn guess(game: Game, letter: String) -> Game {
    #{..game, guesses: [letter, ..game.guesses]}
}
```

The signature is a contract with a caller the compiler cannot see: renaming
the function or changing a parameter breaks that caller with no build error.
A program that wants its logic free of the marker keeps its exported functions
in a thin entry file of short wrappers over plain functions in other files.

Rules:

- **Top level only.** `export` is the leftmost modifier and the only one:
  `export fn name(...)`. `export` before anything else is the error
  `` `export` marks a function: `export fn name(...)` ``. Inside a body it is
  `` `export` applies to top-level functions only ``, and inside an `impl`
  block `` a function in an `impl Type` block cannot be exported: `export fn`
  declares a top-level function ``. A `host fn` has no body to export:
  `export host fn` is an error, and so is `export fn` with a `go` binding.
- **Independent of `pub`.** `pub` makes a function visible to other files of
  the program; `export` makes it callable from outside the program. An
  exported function needs no `pub`, and `pub export fn` is the error
  `` an exported function takes no `pub`: write `export fn` ``. Other files
  call the plain function an exported one wraps.
- **Concrete signature.** An exported function cannot be generic
  (`` exported function `ident` cannot be generic: a host calls it with JSON
  values of one known type ``), and every parameter and result type is one
  known type: an interface value such as `Display` is refused.
- **JSON crossing.** Each parameter's type implements `FromJson`, since the
  host's JSON is decoded into it, and the result's type implements `ToJson`,
  since it is encoded for the host. A generic type's arguments must too, so
  `List<Plain>` needs `FromJson` for `Plain`. A `Unit` result crosses as
  nothing. The error names the parameter, the type and the interface, with
  the derive that fixes it:
  `` parameter `p` of exported function `sum` cannot cross as JSON: `Plain`
  does not implement `FromJson` `` with the help `` add `derive FromJson for
  Plain` ``. A struct, an enum and a distinct type the program declares
  derive both.
- **Unique names.** The host calls by name, so no two files of a program
  export the same name: `` exported function `guess` is already exported by
  other.nomi: a host calls exported functions by name, so each name is
  exported once ``, at the second declaration, with the first as a related
  location.
- **Effects.** An exported function may do anything a function may: print,
  read the clock, use randomness.

A program with exported functions needs no `fn main`. `nomi check` and
`nomi test` treat it as any program. Exported functions are lowered whether
or not `main` reaches them, so `nomi check` reports an exported body the VM
cannot run as it reports one `main` reaches. `nomi run` of such a program with
no `main` is the error `` <file> declares no `fn main`, so there is no program
to run; its exported functions are called by a host, so build it for a page
with `nomi build --web`, or as a WASI reactor with `nomi build --target
wasip1/wasm` ``, and a plain `nomi build` gives the same error for `build`.
`nomi build --web` and `nomi build --target wasip1/wasm` build it.

Values cross as `ToJson` writes them and `FromJson` reads them:

| Nomi | JSON | JavaScript |
|---|---|---|
| `Int`, `Float` | number | `number` |
| `String`, `Bool` | string, boolean | `string`, `boolean` |
| `List<T>`, `Vector<T>`, `Set<T>` | array | array |
| `Map<String, V>` | object | object |
| struct | object, as `derive ToJson` writes it | object |
| enum | a bare variant's name as a string; `{"Variant": payload}` for one with a payload | string or object |
| `Maybe<T>` | the value, or `null` for `None` | the value or `null` |
| `Unit` (a result only) | nothing | `undefined` |

A `Float` parameter reads a whole number too (JavaScript writes `2.0` as `2`).
An `Int` result outside ±2⁵³ is refused rather than rounded: a JavaScript
number cannot hold it exactly, and the call fails with
`` the result of `f` holds the Int 9007199254740993, outside ±2^53: a JSON
number cannot hold it exactly ``.

**Calling from a page.** `nomi build --web` records each exported function in
the program it builds, and `nomi.js` beside it loads the program once and
calls the functions any number of times:

```js
import { load } from "./nomi.js";

const nomi = await load("./app.wasm");
let game = await nomi.call("new_game", "pipeline");
game = await nomi.call("guess", game, "e");
```

`load(url, options)` starts the program in its own worker and runs its `main`
first when it has one, so `main` can print or force `once` values; its output
goes to `options.stdout` and `options.stderr` as `run()`'s does. It resolves
when the functions can be called, and rejects with `main`'s failure when
`main` fails. `call(name, ...args)` encodes the arguments as JSON, runs the
function, and resolves with its decoded result. Calls run one at a time, in
the order they are made, on the one program, so a `once` value an earlier
call forced is not computed again. A call that traps, or whose argument its
parameter's `FromJson` refuses, rejects with an `Error` whose message is the
Nomi diagnostic (`` argument `game` of `guess(game: Game, letter: String):
Game`: guesses: expected List<String>, got missing ``), and the next call runs
normally; so does a call to a name the program does not export, whose error
lists the exported names. `exports()` resolves with each exported function's
name, parameters, result type and signature, and `close()` stops the worker.

`nomi build --web` also writes `nomi.d.ts`, TypeScript declarations for
`nomi.js` with a typed `call` overload for each exported function. A type
maps as the table above says; a struct or enum whose `ToJson` is derived
becomes an interface or a union of its JSON shape, and a type whose impls are
written by hand is `unknown`.

**Calling from a WASI host.** `nomi build --target wasip1/wasm` writes one of
two module shapes, chosen by the program, with no flag. Either carries its
program in its own linear memory, so it needs no file access, no
environment and no particular argv: a browser's WASI shim with no file
system, or any runner that only calls its exports, runs it.

- **No `export fn`: a command module.** It exports `_start`, which runs `main`
  with WASI stdin, stdout and arguments, then exits.
- **At least one `export fn`: a reactor.** It exports `_initialize`, which
  the runner calls once (the WASI reactor convention), its `memory`, and a
  generic call ABI over that memory:

  | Export | Signature | Does |
  |---|---|---|
  | `nomi_alloc` | `(len: i32) -> i32` | reserves `len` bytes and returns a pointer the runner writes into |
  | `nomi_call` | `(name_ptr, name_len, args_ptr, args_len: i32) -> i64` | calls the exported function named by the UTF-8 name with the JSON array of arguments; returns the result's pointer in the high 32 bits and its length in the low 32 bits |
  | `nomi_free` | `(ptr, len: i32)` | releases a buffer from `nomi_alloc` or a result from `nomi_call` or `nomi_load` |
  | `nomi_main` | `() -> i32` | runs `main` over WASI stdin, stdout and stderr and returns the exit status, 0 or 1 |
  | `nomi_load` | `(ptr, len: i32) -> i64` | boots the compiled program (`.nomic`) at `ptr` in a reactor that has none, or with `len` 0 the program built into the module; returns its result as `nomi_call` does |

The result is UTF-8 JSON: `{"ok": value}` on success, with `null` for a
`Unit` result, and `{"error": "diagnostic"}` on a trap, a `FromJson` failure
or an argument list that is not a JSON array of the right length. The module
stays usable after an error. A name the program does not export is an error
result naming the exported functions. `nomi_call` copies the name and the
arguments before it runs anything, so the runner may free them as soon as it
returns; a result stays where it is until `nomi_free` releases it.

A runner in JavaScript, after instantiating the module with its WASI imports:

```js
wasi.initialize(instance);
const x = instance.exports;
const put = (text) => {
  const bytes = new TextEncoder().encode(text);
  const ptr = x.nomi_alloc(bytes.length);
  new Uint8Array(x.memory.buffer, ptr, bytes.length).set(bytes);
  return [ptr, bytes.length];
};
const [name, nameLen] = put("guess");
const [args, argsLen] = put(JSON.stringify([game, "e"]));
const packed = BigInt.asUintN(64, x.nomi_call(name, nameLen, args, argsLen));
x.nomi_free(name, nameLen);
x.nomi_free(args, argsLen);
const ptr = Number(packed >> 32n), len = Number(packed & 0xffffffffn);
const answer = JSON.parse(new TextDecoder().decode(new Uint8Array(x.memory.buffer, ptr, len)));
x.nomi_free(ptr, len);
```

The program boots on the first `nomi_call` or `nomi_main`, and every later
call runs on that booted program, so a `once` value an earlier call forced is
not computed again. A boot that fails makes every `nomi_call` answer its
error. `main` does not run unless the runner calls `nomi_main`, which it may
do before, between or after calls; in a program with no `fn main` it writes
`` this program has no `fn main`; it is called through its exported
functions `` to stderr and returns 1. Calls run one at a time: the module is
single-threaded, and tasks a call starts make progress only while some
export is running.

**Loading a program into a reactor.** A runner that runs many scripts can
ship the reactor once and each script as a small `.nomic` file (§25,
*Compiled programs and nomi-vm*). `nomi build-vm --reactor` writes the
reactor with no program of its own: the module a
program with an `export fn` is embedded in, with its image area empty.
The runner compiles it once, instantiates it for each script, calls
`_initialize`, writes the whole `.nomic` file, header included, into a buffer
from `nomi_alloc`, and calls `nomi_load` with it. `nomi_load` copies the
bytes before it returns, boots the program, and answers like `nomi_call`:

- `{"ok": ["add(a: Int, b: Int) -> Int", ...]}` on success: the program's
  exported functions as `export fn` declares them, without `export fn`, in
  declaration order. A program with only a `main` answers `{"ok": []}`.
- `{"error": "..."}` when the bytes are not a `.nomic` file (`` the loaded
  program: not a compiled Nomi program (.nomic) ``), are cut short or carry
  bytes after the program, are in a program format this reactor does not
  read (the text `nomi-vm` gives, §25), import `std/compiler` in a reactor
  that does not link its hosts, hold neither a `fn main` nor an `export fn`,
  or the program's `boot` fails.

After a successful load, `nomi_call` and `nomi_main` work on the loaded
program exactly as on an embedded one. A failed load leaves the reactor with
no program, so the runner may load again. A reactor with no program answers
`nomi_call` with `` {"error": "this reactor has no program yet: write a
compiled program (.nomic) into memory from nomi_alloc and pass it to
nomi_load first"} ``, and `nomi_main` writes that text to stderr and returns 1.

One instance runs one program. A second `nomi_load` after a successful one
is an error result, `` this reactor already loaded a program, and a reactor
runs one program for its life. To run another, instantiate the module again
``, and the loaded program keeps running. A booted program leaves state
behind in the instance (its `once` values, the tasks it started, the memory
it grew), and nothing can return an instance to the state `_initialize` left
it in; instantiating the compiled module again gives a clean one cheaply,
since the engine compiles a module once however many instances it makes. A
reactor `nomi build` embedded a program in refuses `nomi_load` the same way:
`` this reactor carries the program `nomi build` embedded in it, so it loads
no other. Load programs into a reactor `nomi build-vm --reactor` writes,
which carries none ``.

`nomi_load` with a length of 0 loads nothing (`ptr` is not read). In a
reactor `nomi build` embedded a program in, it boots that program, as the
first `nomi_call` would, and answers as a load does: the exported functions'
signatures, or the boot's error. A runner calls it to learn what it can call
before it calls anything; calling it again answers the same. In a reactor
with no program it is the error result `` this reactor has no program built
in: pass it a compiled program (.nomic) ``, and the reactor stays ready for
a load.

`nomi build-vm --reactor` also writes `nomi-wasi.js` beside the reactor,
with its TypeScript declarations in `nomi-wasi.d.ts`, and `nomi build
--target wasip1/wasm` writes both beside a reactor it embeds a program in: the toolchain's
JavaScript host for this ABI, an ES module that runs in browsers and in Node
with no WASI library, as Go's `wasm_exec.js` is the host for `js/wasm`. It
implements the WASI preview1 imports the reactor needs (no preopened
directories; an import it does not know answers `ENOSYS`), and over the ABI
above it offers `loadRuntime(reactor)`, which compiles the module once;
`runtime.load(nomic)`, which instantiates it, calls `nomi_load`, and resolves
to a program whose `exports` are the load's signatures and whose
`call(name, ...args)` JSON-encodes the arguments, calls `nomi_call`, and
resolves with `ok` or rejects with an `Error` carrying `error`; and
`runtime.run(nomic, #{stdin, args, env})`, which loads into a fresh instance,
calls `nomi_main`, and resolves with its exit status and output. Without
`nomic`, `load()` and `run()` call `nomi_load` with length 0, so they use
the program built into the module, and reject with that error result on a
reactor that has none; a `.nomic` handed to a reactor with a program built
in rejects with the refusal above. A WASI call
is synchronous, so a sleeping task blocks the calling thread for its duration
(with `Atomics.wait` where the thread may block, by spinning elsewhere).

Go fixes a module's
export names when the runner is compiled, and one runner serves every
program, so the program's own function names travel as data rather than as
per-function exports such as `guess(i32, i32) -> i32`. A project with Go
bindings and an `export fn` cannot be built for `wasip1/wasm`: the reactor
links no project's Go bindings.

## 6. Pipe Operator

Left-to-right piping with `|>`. A stage is a call, and the piped value fills its **first argument**. To put the value anywhere else, apply a capture to it with `then` (*The `then` stage* below):

```nomi
// a call stage: the value fills the first argument
[1, 2, 3] |> Iter.filter(fn x -> x > 2)

// a capture applied to the value, which is its `&1`
10 |> then &divide(100, &1)

// `&1` may be a named argument
"7" |> then &String.pad_start(width: 3, s: &1)

// works with case — piped value becomes the match target
value
|> case transform() {
    Some(x) -> x
    None -> default
}
```

A pipe fills the first argument only. An `&N` in a call stage is outside
every capture, so `x |> divide(100, &1)` is the error `` `&1` is a capture's
parameter: write the function as `&(...)` or `&f(...)` ``, whose help says
`` a pipe fills the first argument; write `|> then &divide(100, &1)` ``. In
a `then` stage the capture's `&1` is the piped value wherever it stands,
positional or named, and as often as it is written, computed once: `x |>
then &connect(port: &1, "localhost")` is `connect("localhost", port: x)`,
and `n |> then &add(&1, b: &1)` is `add(n, b: n)` (§5, *Captures*).

**A stage is a call.** `x |> f()` is `f(x)` and `x |> f(a)` is `f(x, a)`:
the pipe inserts its value into the call that follows it. A name without
parentheses is a function reference wherever it stands, as in
`Iter.map(xs, io.print)`, so it is not a stage. `x |> f`, `x |> io.print`,
`x |> Ok` and `x |> Shape.area` are errors that name the call to write
(`` a pipe stage is a call: write `io.print()` ``), and the language server
offers a quick fix that appends the `()`. A `.Variant` stage is a call
written through its enum (`x |> Shape.Dot()`), since a bare `.Variant`
stage cannot see its enum (§8). The stages that are not calls are the
`then` and `tap` stages and the keyword stages below.

**A stage that does not take the piped value is the error.** It is reported
at the stage and names the argument the value fills: `` `String.pad_start`
takes a String as its first argument, but the pipe passes a Fahrenheit ``,
with a note at the expression the value comes from. A `then` stage's
partial capture that does not take the value at its `&1` is reported the
same way, at the `&1`: `` `label` takes a String as its second argument ``,
or, for a named `&1`, the parameter (`` `label` takes a String as `text`,
but the pipe passes an Int ``). When the value's type implements `Display` and the stage takes a
`String`, the help says to add `|> Display.to_string()` before the stage, and
the language server offers a quick fix that inserts it.

**A lambda's body runs to the end of its expression,** or to the `)`, `]`,
`}` or `,` that closes the construct it stands in. A `|>` inside it belongs
to the body: `Iter.map(xs, fn s -> String.to_int(s) |> Maybe.with_default(0))`
maps each string to an `Int`, and `fn n -> n * 2 |> dbg` is a lambda whose body
ends in `dbg`. The one exception is the lambda of a `then` or `tap` stage.

**The `then` stage:** `x |> then fn v -> body` applies the lambda to the piped
value, as `(fn v -> body)(x)` would, and `x |> then &f(a, &1)` applies the
capture, as `(&f(a, &1))(x)` would. `then` is followed by the lambda or the
capture itself; anything else after it (`x |> then f`) is the parse error
`` `then` takes a lambda or a capture: write `then fn v -> ...` or `then &f(&1)` ``,
and the same holds for `tap`. The function takes one parameter, the piped
value: a lambda's may be a destructuring pattern (`then fn #(a, b) -> a +
b`), and a capture's is `&1` (`then &add(&1, &2)` is `` the function
expected here takes one argument; `&2` names a second ``). A capture ends
with its body, so `10 |> then &divide(100, &1) |> dbg` divides and then
prints. A lambda's body ends at the next `|>` of the pipeline, so the
stages after it stay in the pipeline:

```nomi
total =
    orders
    |> Iter.filter(.paid?)
    |> then fn paid -> Iter.count(paid) * 100 / Iter.count(orders)
    |> dbg
```

Braces keep a pipe inside the body:
`|> then fn v -> { v |> Iter.filter(.paid?) |> Iter.count() }`. A parameter of a
bare `then` body used in a later stage is an error that says where the body
ended. A lambda is not a stage without `then`: `xs |> fn n -> n * 2` is the error
`` a lambda is not a pipe stage: write `then fn n -> ...` ``, and the language
server offers a quick fix that inserts the `then`. A capture is not a
stage without `then` either: `x |> &f(&1)` is `` a capture is not a pipe
stage: write `then &f(&1)` ``. `then` is a reserved word and stands only
after `|>`; a keyword stage does not prefix it (`|> try then fn v -> ...`
is an error), so a `try` after a `then` is its own stage. `nomi fmt` puts
each stage of a pipeline with a `then` lambda on its own line; a `then`
capture, which ends with its body, does not move the pipeline off one
line.

**The `tap` stage:** `x |> tap fn v -> body` runs the lambda on the piped value
for its effect and passes the value on unchanged, and so does `x |> tap
&io.print(&1)` with its capture. The value is computed once, and the
function runs exactly once, at that point in the pipeline:

```nomi
import std/io

fn main() {
    shout =
        "go north now"
        |> String.words()
        |> tap fn words -> io.print("got ${Iter.count(words)} words")
        |> Iter.map(String.to_upper)
        |> String.join(" ")

    io.print(shout)
}
```

This prints `got 3 words`, then `GO NORTH NOW`. The lambda is a `then`
lambda in every other way: it takes one parameter, which may be a
destructuring pattern; its bare body ends at the next `|>`, and braces keep a
pipe inside it; a keyword stage does not prefix it (`|> try tap fn v -> ...` is
an error), so a `try` before or after a `tap` is its own stage. `tap` is a
reserved word and stands only after `|>`.

The lambda must return `Unit`, because the stage discards its result. A body
whose value is not `Unit` is the error `` `tap` passes its input on unchanged,
so its lambda must return Unit; got Int ``, whose help says to use `then` to
replace the value. A body may end in a `dbg` observation (`tap fn v -> dbg v`), as
a `Unit` function body may. `try` and `return` inside the lambda exit the
lambda, as they do in any lambda, so a body that uses `try` answers a
`Result` or a `Maybe` and is rejected: handle the failure inside the body, or
use `then`. `nomi fmt` stacks a pipeline with a `tap` lambda as it does one
with a `then` lambda.

**Keyword stages:** `dbg` and `try` are unary pipe stages. Read them as
built-in one-argument functions over the current pipe value, written without
parentheses. The bare form is canonical when the current pipe value is already
the value being consumed:

```nomi
value
|> try
```

When the keyword consumes the result of a single call stage, prefix that
stage. The formatter normalizes a trailing bare keyword into this form:

```nomi
id
|> try User.get()
```

Assertions wrap the pipeline from the head rather than sitting at the tail:

```nomi
assert message
    |> String.contains?("ready")
```

`if` and `case` also consume one pipe value. They can be bare stages, or they
can prefix the single operation that computes their condition/scrutinee:

```nomi
status
|> if Status.ready?() { "ready" } else { "blocked" }

input
|> case parse_id() {
    Ok(id) -> id
    Err(_) -> 0
}
```

`input |> case parse_id() {` and `input |> parse_id() |> case {` are the same
program, and `nomi fmt` keeps whichever was written. Unlike `try`, it does not
move a stage into a keyword's subject. The arms or branches sit one indent in
from the line the `|>` is on.

**Debugging pipes:** `dbg` prints a source-located Debug rendering and returns
the value, so you can insert it anywhere in a pipe chain without breaking the
flow:

```nomi
users
|> Iter.filter(.active)
|> Iter.to_list()
|> dbg // prints the filtered list, passes it through
|> Iter.map(.name)
|> Iter.to_list()
```

`dbg expr` works in expression position too. It uses the same universal
`Debug` rendering as `io.inspect`, but includes the source expression and line
number in its output. A parenthesized `dbg expr` is the value of `expr`, not
a pipe stage, so `x |> (dbg 1)` is an error whose help names `|> dbg`.
Because `dbg` is development-only instrumentation, the
LSP reports it as a warning, and `nomi build` refuses a program with a `dbg`
anywhere in its files, reached or not, so it does not ship. `nomi check`,
`nomi run`, `nomi test` and the REPL accept and run it. The refusal lists each
`dbg` with its operand on one line, cut after 40 characters, or `|> dbg` for
the pipe stage:

```text
nomi build: 2 dbgs remain:
  main.nomi:12:5 dbg total
  report.nomi:30:8 |> dbg
```

`io.inspect(value)` remains the ordinary file-level function for explicitly
printing a Debug rendering as a `Unit`-returning side effect.

See §9 *The `try` Keyword* for how `try` works outside pipes.

## 7. Types

All types are named. Four keywords declare them:

- **`struct`** — a nominal record type with named fields. The body is a newline-separated list of `name: Type` field lines. Derives are sibling `derive Iface for Type` declarations. Manual interface implementations are sibling `impl Iface for Type { ... }` blocks, not type-body items. A single space separates the type name from the opening `{` — matches every other declaration keyword that takes a brace body. Construction uses no space (`#Foo{a: 3, b: "x"}`).
- **`enum`** — a nominal sum type. Variants are one per line — `Red`, `Pending Int`, `embeds OtherType`. There is no `|` separator and no single-line form; enum bodies are always multi-line. Derives are sibling `derive Iface for Type` declarations. Manual interface implementations are sibling `impl Iface for Type { ... }` blocks.
- **`type`** — a distinct-type wrapper or zero-sized marker. The shape after the name distinguishes: tuple-distinct (`#(A, B)`), generic-distinct (`Map<K, V>`), primitive-distinct (a bare type name), or zero-sized (nothing). Distinct `type` declarations do not take bodies; put constructors and helpers beside the type. Derives are sibling `derive Iface for Type` declarations. Manual interface implementations are sibling `impl Iface for Type { ... }` blocks. For nominal records and sum types, see `struct` and `enum`.
- **`typealias`** — a transparent synonym for an existing type. No body.

Type declaration names may be dotted PascalCase paths when the declared type is
owned by a real parent type. `type Civil.Days Int` declares a nominal type named
`Civil.Days`; it is a sibling declaration whose parent must already be in
scope, not an item nested inside `Civil`. Construction, destructuring, derives,
owner functions, and interface impls all use the full name:

```nomi
struct Civil {}

type Civil.Days Int

fn days_value(days: Civil.Days) -> Int {
    Civil.Days(n) = days
    n
}

impl Add<Civil.Days, Date> for Date {
    fn add(lhs: Date, rhs: Civil.Days) -> Date {
        ...
    }
}
```

```nomi
struct User {
    name: String
    age: Int
}

type Id Int
type Nothing

struct Preferences {
    theme: String
    language: String
}

fn new(name: String, age: Int) -> User {
    #User{name, age}
}

user = #User{name: "Alice", age: 30}

prefs = #Preferences{theme: "dark", language: "en"}

id = Id(42)
```

Nominal record types are declared with `struct` — snake_case field names followed by `:` Type, with a single space between the type name and the opening `{`. Distinct-type wrappers use `type`: tuple-distinct `type Pair #(Int, String)`, map-distinct `type Kvs Map<K, V>`, primitive-distinct `type Id Int`, zero-sized `type Expired`. See §15 for the full distinct-type taxonomy.

Struct *declarations* take a single space before `{` (matching every other brace-bodied declaration keyword); struct *construction* uses no space (`#Foo{a: 3, b: "x"}`).

Each name in a declaration is declared once: a struct field, a struct-shaped variant's field, an enum variant, an interface function, or a function's or lambda's parameter declared twice is a compile error at the second (``field 'x' is declared twice in struct `Point` ``, with a note at the first), as a repeated field in an anonymous record type is. Two fields on one line of a struct body, or two functions on one line of an interface body, are a parse error; a semicolon may separate them.

The newline-separated field layout applies only to **declaration bodies**. Anonymous struct *type expressions* (`#{name: String, age: Int}` in type position), struct construction literals (`#Foo{a: 3}`), struct-shaped enum variant payloads (`Card {rank: Int}`), and pattern syntax all keep their comma-separated shape unchanged.

### Shape and Implementations

A type declaration owns the type's shape and its concrete qualified API.
`derive Iface for Type` asks the compiler to synthesize an implementation from
the declaration's fields or variants. Derives are sibling declarations in the
same file/scope as the type. Manual interface implementations are sibling
`impl Iface for Type { ... }` blocks; the functions inside are plain `fn` /
`host fn` items:

```nomi
struct User {
    name: String
    age: Int
}

fn rename(user: User, name: String) -> User {
    #User{name, age: user.age}
}

impl Display for User {
    fn to_string(user: User) -> String {
        ...
    }
}

impl Serializable for User {
    fn serialize(user: User) -> String {
        ...
    }
}
```

`rename(u, "Bob")` is an ordinary function beside the type; `User.to_string(u)` / `Display.to_string(u)` dispatch through the `Display` implementation declared by `impl Display for User`. See §13 for the full implementation rules.

### Default Values

Struct fields can have default values. Fields with defaults can be omitted at construction:

```nomi
struct Response {
    status: Int = 200
    body: String = ""
}

ok = #Response{} // status: 200, body: ""

not_found = #Response{status: 404} // status: 404, body: ""

full = #Response{status: 200, body: "hi"} // all explicit
```

Fields without defaults must always be provided. Use constructor functions when you need validation or transformation logic beyond simple defaults.

A field default has no function to exit, so `try`, `return`, `break`, `continue` or an assertion written directly in it is a compile error at the keyword (`` `try` cannot be used in the default of field 'port' of Config: a default has no function to return from ``).

A struct-shaped enum variant's fields take defaults the same way, and a default expression runs per construction, only when its field is omitted:

```nomi
enum Shape {
    Circle {radius: Float = 1.0}
    Rectangle {width: Float = 2.1, height: Float}
}

unit = #Shape.Circle{} // radius: 1.0

tall = #Shape.Rectangle{height: 9.0} // width: 2.1, height: 9.0
```

Only named fields take defaults. A positional payload (`Circle Float`) and a bare variant (`North`) cannot: `Circle Float = 3.14` and `North = 1` are compile errors, and the positional one names the struct-shaped form to use instead. A default needs a name so a construction that omits it still says what it left out, and a positional variant referenced bare (`Shape.Circle`) already means the constructor as a function value.

### Optional Fields

A field whose *value may be absent* uses `Maybe<T>`:

```nomi
struct User {
    name: String
    nickname: Maybe<String>
}

user = #User{name: "Alice", nickname: None}
```

`Maybe<T>` and "field with a default" are independent concepts. A field with a default (see Default Values above) always has a value at runtime — the default just spares the caller from spelling it out. `Maybe<T>` says the value can semantically be absent (`None`). The two compose: `nickname: Maybe<String> = None` would mean "optional, defaults to absent."

### Construction

Struct construction uses braces with named fields. Field punning is a shorthand: when a variable has the same name as a field, the field name alone is enough:

```nomi
user = #User{name: "Alice", age: 30}

// destructuring — type name not needed: `#{...}` takes the value's struct
#{name, age} = user

#{name: n, age: a} = user

// field punning — `name` and `age` are bound already, so the field names
// on their own are enough
punned = #User{name, age} // same as #User{name: name, age: age}

bumped = #User{name, age: age + 1} // mix punning and explicit
```

Positional data uses tuples `#(value, value)` or enum variants with parens — struct construction always requires named fields.

A brace literal names each field at most once. `#User{name: "Al", name: "Bo"}` is the compile error `field 'name' is given twice in this User literal`, at the second `name`; there is no last-wins rule. The same holds for an anonymous literal (`this struct literal`), a struct variant (`#.Rect{w: 1, w: 2}` is `this Rect literal`), the fields after a spread and a `Struct.update` patch. A field after a spread may repeat a field of the base, since replacing it is what the spread is for.

A struct can also be constructed in **call-form** by passing a single anonymous struct value to the type name:

```nomi
f1 = #User{name: "Alice", age: 30} // literal

f2 = User(#{name: "Alice", age: 30}) // call-form
```

The two forms are equivalent — pick whichever reads better. The call-form passes an anonymous struct value to the constructor, which coerces it to the nominal struct. Field names and types must match exactly; defaulted fields can be omitted just as in the literal:

```nomi
struct Defaulted {
    a: Int
    b: String = "default"
}

d = Defaulted(#{a: 7}) // b takes its default
```

Generic structs work the same way — type parameters are inferred from the anonymous struct's shape:

```nomi
struct Box<T> {
    v: T
}

b = Box(#{v: 1}) // T = Int, inferred
```

#### Target-typed construction

`#{...}`, a struct literal with no type name and no spread, builds the nominal struct its position expects, so the type name after the `#` can be left out where the context already says it:

```nomi
struct Address {
    street: String
    city: String
    zip: String = "00000"
}

struct Person {
    name: String
    address: Address
}

fn home() -> Address {
    #{street: "1 Main", city: "Bath"} // the return type says Address
}

fn main() {
    a: Address = #{street: "2 Main", city: "Bath"} // the annotation says Address
    ada = #Person{name: "Ada", address: #{street: "3 Main", city: "York"}} // the field says Address
    people: List<Person> = [#{name: "Bo", address: a}] // the element type says Person
    dbg a == #Address{street: "2 Main", city: "Bath"}
    dbg home().zip
    dbg ada.address.city
    dbg people
    Unit
}
```

The positions that supply the struct are the ones that supply an enum to `.Variant` (see *Variant resolution*): an annotated binding or `once`, a struct field in construction, a function or constructor argument, a return value or block tail, an element of a typed `List`, `Set`, `Vector` or `Map` literal, a `case` or `if` arm whose result has an expected type, an operand of `==`, `!=`, `<`, `>`, `<=` or `>=` (`p == #{x: 1, y: 2}` builds `p`'s struct), and a generic position once its type argument is known (`b: Box<Int> = #{item: 3}` builds `Box<Int>`; `Some(#{street: "1 Main", city: "Bath"})` under `Maybe<Address>` builds an `Address`).

The literal follows the ordinary construction rules and builds exactly the value `#Address{...}` builds: every field without a default is given, defaults fill in the rest, an unknown field is rejected, and an opaque struct is constructed this way only in its defining file. Errors name the struct: `missing field 'street' of Address`, `field 'city' of Address: expected String, got Int`, `Address has no field 'zip'`.

Where no struct type is expected, `#{...}` is an anonymous struct, as below. It also stays anonymous where the expected type is an anonymous struct type, an interface, a type parameter or an enum: none of them names one struct to build, so the checker does not guess, and a mismatch there reports the anonymous struct's type (`expected Display, got #{street: String, city: String}`). An empty `#{}` builds a struct its position expects, from the struct's field defaults; where none is expected it is the error `an anonymous struct has at least one field`. Under a struct-update spread a literal without `..` is still a new struct; a literal with a bare `..` (`#{.., city: "NYC"}`) updates the field's current value instead (see *Struct Updates*).

A plain `{` always opens a block, so `{x: 1}` is a block whose first statement is `x: 1`, a parse error, and `{name}` is a block whose value is `name`. A name followed by `{` never starts a literal either: `Point{x: 1}` is a parse error at the `{`, and every struct literal starts with `#`.

### Tuples

A tuple is written `#(a, b, ...)` — fixed-size, positional, heterogeneous, with at least two items. Its type is written the same way, `#(Int, String)`, and a tuple pattern too. Accessed by index:

```nomi
pair = #("Good Morning", "Good Evening")

pair.0 // "Good Morning"

pair.1 // "Good Evening"

// Destructuring
#(x, y) = pair

// In function signatures
fn divide(x: Int, y: Int) -> #(Int, Int) {
    #(x / y, x % y)
}

#(quotient, remainder) = divide(10, 3)
```

Tuples work inside other types:

```nomi
fn safe_divide(x: Int, y: Int) -> Result<#(Int, Int), String> {
    case {
        y == 0 -> Err("division by zero")
        _ -> Ok(#(x / y, x % y))
    }
}
```

Tuples are structurally typed — the type is `#(Int, String)`, `#(Float, Float)`, etc. No single-element or empty tuples: `#(1)` and `#()` are errors. Parentheses only group, in an expression, a pattern and a type: `(x)` is `x`, `((Int))` is `Int`, and `(1, 2)` is a parse error. The empty value is `Unit`. Use named structs when the type appears in multiple places or needs its own functions.

### Anonymous Structs

An anonymous struct is written `#{...}`, a struct literal with no type name after the `#`, and always has named fields. A lone field may be punned: `#{name}` is `#{name: name}`.

```nomi
g = #{morning: "Good Morning", night: "Good Evening"}

g.morning // "Good Morning"

g.night // "Good Evening"

// Destructuring (field punning)
#{morning, night} = g

// Destructuring with rename
#{morning: m, night: n} = g
```

Anonymous structs are structurally typed — the type is its shape, written as the value is (`#{morning: String, night: String}`), not a name. Two anon struct types are equal when their field sets (name → type) match, regardless of declared order: `#{a: Int, b: String}` and `#{b: String, a: Int}` are the same type. A function expecting `#{morning: String, night: String}` accepts any anonymous struct with those fields and types. There is no nominal distinction.

Anon struct types are writable in any nested type position: function parameters, distinct-type tuple elements, generic type arguments, struct fields, and `typealias` right-hand sides. A distinct type directly over an anonymous struct, `type Foo #{...}`, is the error `a type over an anonymous struct is a struct`: the `struct` keyword declares record types.

```nomi
fn greet(p: #{name: String, age: Int}) -> String {
    p.name
}

type Tagged #(String, #{tag: String, payload: Int})

struct Box {
    value: #{x: Int, y: Int}
    // struct keyword for top-level
}

typealias Point #{x: Int, y: Int} // a name for the shape
```

A function parameter typed `#{name: String, age: Int}` accepts any anonymous struct value with those fields and types — field order at the call site doesn't matter. Error messages and hover render the type in the order it was declared at its source site, so two equivalent shapes can render differently; that's intentional. Nominal struct values (declared with the `struct` keyword) do *not* coerce — they're nominal.

`nomi fmt` keeps a short anonymous struct type or struct literal on one line and breaks a long one with a field per line. It keeps an anonymous struct type you wrote across lines that way, and a struct literal broken after its `{`; the layout rules are in [`implementation-notes.md`](implementation-notes.md#formatter-brace-bounded-field-constructs).

**No width subtyping.** A function parameter typed `#{x: Int, y: Int}` accepts only values with *exactly* that shape — not anon structs with additional fields, and not nominal structs that happen to have matching fields. Width is part of the type's identity. This is a deliberate choice against TypeScript-style structural over-acceptance: field-name coincidence is not a semantic contract (an `id: String` on `User`, `Order`, and `Invoice` shares a type but rarely a meaning), and silent acceptance erodes trust as the codebase grows. Code that genuinely needs to operate across multiple struct shapes has three explicit alternatives — declare an interface for the shared *behavior* (not the shared shape); construct an anon struct at the callsite (`distance(#{x: p.x, y: p.y}, #{x: q.x, y: q.y})`); or define a nominal domain type and write a conversion function (`to_coord(p: Point) -> Coord2D`) so mismatches surface where they can be seen.

### Struct Updates

`Struct.update` returns a new struct with selected fields replaced. It is the sole function of `Struct`, the universal interface every struct — named or anonymous — satisfies, so it is reached type-qualified (`Struct` is in the prelude, so no import is needed). Its signature is `update(original: self, updates: Partial<self>) -> self`; for a call such as `Struct.update(point, ...)`, `self` denotes `Point`, so the return type is `Point`, not an abstract `Struct`. `updates` is a `Partial<self>`, a built-in type (like `List<T>` / `Map<K, V>`) describing a *partial* of the struct: an anonymous struct whose fields are a subset of the target's. Each field it writes is a whole value of that field, or, in a literal patch, a literal with a bare `..` that updates the field's current value (*Nested updates* below).

```nomi
updated = Struct.update(user, #{name: "Bob", age: 42})

// works naturally with pipes (inside users.nomi)
user
|> Struct.update(#{name: "Bob"})
|> users.validate()
|> try users.save()
```

**Nested updates.** A bare `..`, written with no base, stands for the value already at the field or key the literal is written at. A literal holding one is that value with the changes written, and a literal without one is a whole new value that replaces the field:

```nomi
// `..` is user.address: only city changes, street and zip are kept
Struct.update(user, #{address: #{.., city: "NYC"}})

// means:
Struct.update(user, #{address: #{..user.address, city: "NYC"}})

// nests to any depth
Struct.update(company, #{
    ceo: #{
        ..,
        address: #{
            ..,
            city: "NYC",
        },
    },
})

// no `..`: a whole new Address replaces the field
Struct.update(user, #{address: #{street: "123 Main", city: "NYC", zip: "10001"}})
```

`#{address: #{city: "NYC"}}` is therefore a whole `Address` with no `street`, and is the error `missing field 'street' of Address`, whose help suggests `#{.., city: "NYC"}`. The same holds for an anonymous-typed field: there is no width subtyping, so a brace that leaves out a field is not the field's type.

The `..` works at any field or key of an update, for every value that has a spread form:

| At a field or key holding | Write | Means |
|---|---|---|
| a struct | `#{.., f: v}` | the struct with `f` replaced, as `#{..old, f: v}` |
| a map | `%{.., k => v}` | `Map.merge(old, %{k => v})` |
| a list | `[x, ..]` | `[x, ..old]`; the `..` is last, as in any list spread |
| a set | `%[.., x]` | `Set.union(old, %[x])`; the `..` is first, as in a set spread |
| a vector | `#[.., x]` or `#[x, ..]` | `Vector.concat(old, #[x])` or `Vector.concat(#[x], old)` |

**Where a bare `..` is legal.** Only as the spread of a literal written as the value at a field or key of a literal that is itself an update: one with a named spread (`#{..base, ...}`, `%{..base, ...}`), one that is a bare-`..` literal, or the literal patch of a `Struct.update` call (whose fields are always update positions, since an update is partial). Anywhere else there is no current value, and the `..` is the error `a bare \`..\` has no value to update here`: at the top of a plain literal (`x = #{.., a: 1}`), in a list element (`[#{.., a: 1}]`, which has no key), in an operand of `+` or `Map.merge` (which stay shallow), in a call argument, and in a patch passed to any function other than `Struct.update`. The top-level spread always names its base. A literal whose `..` is followed by nothing (`#{..}`, `[..]`) is `an update with nothing after \`..\` changes nothing; leave this field or key out`.

**A `..` must stand for a value of its literal's kind.** `#{.., f: v}` at a list field is `struct spread \`..\` requires a struct, got List<String>`, and likewise for the other forms. A `Maybe`-typed field has no `..` form: the position may hold no value, so `#{.., city: "NYC"}` at a `Maybe<Address>` is `struct spread \`..\` requires a struct, got Maybe<Address>`, and the field takes a whole value (`Some(...)`, `None`). `Some(#{.., ...})` is a call argument, so it has no value to update either. An interface-typed field takes values only, as below.

**A missing map key is created.** In a map update, the base may lack an entry's key, so a `..` at a key may have no old value. It then stands for nothing, and the literal builds the value: an empty map, list, set or vector with the entries or elements written, or a struct from the fields written and the struct's field defaults (*Default Values*). Positions inside a missing value are missing too, so a whole missing chain is built:

```nomi
// no "address" key: the result has "address" => %{"city" => "Atlanta"}
%{..team, "address" => %{.., "city" => "Atlanta"}}

// Geo's fields default to lat 0 and city "?": a missing "rome" builds #Geo{lat: 0, city: "Rome"}
%{..geos, "oslo" => #{.., lat: 59}, "rome" => #{.., city: "Rome"}}
```

The compiler knows which fields a struct literal leaves out, so a struct whose omitted field has no default is a compile error, never a runtime trap: `a missing key leaves \`..\` no Address to update, and field 'street' has no default`. A struct field always has a value, so this applies only under a map key.

**Conformance is structural.** Every struct value satisfies `Struct`; no non-struct type does, with no conformance entry written anywhere. Requiring `Struct` of a non-struct — calling `Struct.update(5, …)`, or passing a non-struct to a `where T: Struct` bound — is a compile-time error (`Int does not implement Struct`), never a runtime trap. The compiler also verifies the patch's field names and types against the target via `Partial<self>`: an unknown field name (`User has no field 'bogus'`) or a type-mismatched override (`field 'age' of User: expected Int, got String`) is a compile error; only the top-level patch target must be a struct, while its *fields* may be any type. A patch held in a binding is checked by its type, and its fields must be whole values. Because `Struct` is a genuine interface, it is also usable as a **bound** (`fn widen<T>(x: T) -> T where T: Struct`) and an **existential** (`fn dump(s: Struct)`, `List<Struct>` holding heterogeneous structs). It is **not hand-implementable**, though: conformance is decided by shape, not a registered impl, and `update` is a host-backed final default — so an `impl Struct for T { ... }` block couldn't change what satisfies `Struct` and is rejected (`Struct` is a built-in structural marker and cannot be implemented).

**Opacity restricts the operation, not the conformance.** An *opaque* struct (§15 *Opaque distinct types*) satisfies `Struct` exactly like any other struct — at a `where T: Struct` bound, at a `Struct` existential, and everywhere else conformance is asked — because conformance is a property of the type's shape and nothing else. What opacity closes is the one place `Struct.update` names a private field: **a patch may not name a field of an opaque struct from outside that struct's defining file** (`cannot update field 'value' of opaque type 'Counter' outside its defining module`). This is the same rule as the field-access row of §15 *Opaque distinct types*, applied to the patch, and it composes through nesting — `Struct.update(box, #{inner: #{.., value: 0}})` is rejected when `Box.inner` is an opaque `Counter`, even though `Box` itself is transparent. Replacing a value wholesale names no field and stays legal, including replacing an opaque field: `Struct.update(box, #{inner: counter.new()})` is fine. Deciding this at the operation rather than at the conformance is the same choice the auto-`Debug` default makes for an opaque type (§15 *Opaque distinct types*): `Debug` stays satisfied by every type, and only the *body* goes name-only.

#### Struct update by spread

`#{..base, field: value}` is the same operation written as a literal. One spread, written first, followed by the fields that change.

```nomi
updated = #{..point, x: 9}
```

**A spread changes at least one field.** `#{..point}` is rejected (`a struct spread with no fields is its base value; write \`point\``): values are immutable and have no identity, so a copy could do nothing the value itself does not.

**The result has the spread head's type.** A nominal struct in gives that nominal struct out, an anonymous struct in gives an anonymous struct out — which is `Struct.update`'s `update(original: self, …) -> self` rule in literal form. That is also why there is no named spelling: `#Cfg{..base, f: v}` is rejected (`struct spread \`..\` has no named form; write \`#{..base, field: value}\``), because the head already carries the type and the name would repeat what the compiler knows.

**Exactly one spread, and it must be the first element.** Two spreads and a spread after a field are both `struct spread \`..\` must be the first element`.

**Position differs from the list spread, and the reason is representation on one side and notation on the other.** A list spread must be *last* (§19): a `List` is cons cells, so prepending is O(1) and appending is O(n), and `[0, ..xs]` is the cheap direction that syntax should make easy. Order in a record update is pure notation — nothing is cheaper either way — and base-then-overrides is the only order under which "a later field wins" is true.

**The fields are checked exactly as an ordinary struct literal's are**, by the same validator: an unknown field name is a compile error, and each value is checked against the head's declared field type, so an interface-typed or type-parameter field stays as lenient here as in `#Cfg{...}`. One rule differs, and only in an update: a *missing* field is never an error, because the spread supplies every field. A field written as a literal with a bare `..` updates that field's current value (*Nested updates* above).

**The two forms are the same operation.** A spread and `Struct.update`'s `Partial<self>` admit the same fields and the same `..` literals, so these are one operation written two ways:

```nomi
moved = Struct.update(user, #{address: #{.., city: "NYC"}})

same = #{..user, address: #{.., city: "NYC"}}
```

Both leave `address.street` and `address.zip` in place. The nested-spread spelling is still legal and still equivalent:

```nomi
also_same = #{..user, address: #{..user.address, city: "NYC"}}
```

**A `..` literal updates; everything else at a field position is a value.** A literal without `..` (`#{street: …, city: …, zip: …}`) or a nominal literal *replaces* the field wholesale, a binding or a call is the value it evaluates to, and a nested spread with a named head is already a complete value of the field's type — so its own head is the base, not the enclosing field. The discrimination is syntactic: the `..` says which literal updates.

**An interface-typed field takes values only.** You cannot update through an interface that does not name its fields, so `#{..stage, who: #{n: 2}}` is `field 'who' of Stage: expected Speaker, got #{n: Int}` and `#{..stage, who: #{.., n: 2}}` is `struct spread \`..\` requires a struct, got Speaker`, while `#{..stage, who: #Quiet{n: 2}}` is accepted. A field whose declared type is a type parameter takes a `..` literal once the argument is solved (`Box<Address>`) and only a value while it is not (`Box<T>` inside a generic function).

**A brace without `..` is the field's whole value, in an update or not.** `#Person{name: "Ada", address: #{city: "NYC"}}` has no base for `address.street` to come from, so the brace is a target-typed `Address` (see *Target-typed construction*) and reports `missing field 'street' of Address`. `#{..ada, address: #{city: "NYC"}}` reports the same, and its help suggests `#{.., city: "NYC"}`.

**A diagnostic names the type the mistake is in.** `#{..user, address: #{.., zip: 10001}}` reports `Address has no field 'zip'`, and `#{..user, address: #{.., city: 10001}}` reports `field 'city' of Address: expected String, got Int` — the nested type and the nested field, not `Person.address`.

**Field punning works after a spread at any arity**, including one: `#{..base, name}` means `#{..base, name: name}`, as `#{name}` means `#{name: name}`.

Opacity applies as it does to a patch: `#{..counter, value: 9}` from outside `Counter`'s defining file is rejected with the same diagnostic.

`Struct.update` stays. It is the reachable-by-name form, it is what a pipe stage uses, and it is the one of the two that takes a patch as a *value* — a computed patch, held in a binding, which the spread's syntax cannot express.

### Calling Convention

A file's public functions are called through its file API object:

```nomi
user = users.new("Alice", 30)

name = users.full_name(user)
```

Pipes thread a value through a sequence of qualified functions as the first argument:

```nomi
id
|> try users.get()
|> users.validate()
|> try users.save()
```

No methods — behavior is defined as functions in files, inherent `impl Type { ... }` blocks, interface impl blocks, or interfaces, and every call is a *qualified prefix call*, never a value-dot `x.func()`. The qualifier is the name before `.`, and it names a file, a type, an interface, or a bounded type parameter. A file-level function is qualified by its file (`io.print(x)`, `timer.sleep(d)`). A function declared in an `impl` block is qualified by its owner: an inherent function by its type (`String.trim(s)`, `Duration.seconds(1)`), an interface implementation function by the implementing type (`User.to_string(x)`) or by the interface (`Display.to_string(x)`). File qualification never reaches into an `impl` block: `users.to_string(u)` is an error that names the owner (`file 'users' has no member 'to_string': it implements Display for User, so call it as User.to_string(...) or Display.to_string(...)`). In an interface-qualified call, the implementation is selected for the concrete type bound to `self` by the call arguments, or by an explicit target type argument for functions such as `FromJson.from_json<User>(json)`. This is more verbose than `x.func()`, but it buys three things:

1. **Clear member surface** — the qualifier tells you what you're looking through: `users.create(x)` names a function in the `users` file API; `Display.to_string(x)` names the contract a function satisfies; `String.trim(s)` names the type that owns it. Value-dot method syntax like `x.to_string()` hides that surface.
2. **No collision risk** — when a single type implements two interfaces that share a function name, the interface qualifier disambiguates (see §13); file and type qualifiers keep same-named functions distinct.
3. **Pipes already handle chaining** — method syntax primarily exists for fluent chaining (`x.area().to_string()`), but pipes serve the same purpose: `x |> Shape.area() |> Display.to_string()`. Having both would be redundant.

```nomi
// users.nomi
pub struct User {
    name: String
    age: Int
}

pub fn create(name: String, age: Int) -> User {
    ...
} // file-level function — called as users.create(...)

impl Display for User {
    fn to_string(user: User) -> String {
        ...
    } // implementation function — called as User.to_string(...) / Display.to_string(...)
}
```

Like Rust's `impl Trait for Type`, Nomi attributes an interface's functions to a type explicitly with an `impl Display for User { ... }` block (see §13). The difference is at the *call site*: Rust's `user.to_string()` hides the interface behind value-dot method syntax, whereas Nomi has no methods at all. The interface function is reached by a qualified call — type-qualified (`User.to_string(my_user)`) or interface-qualified (`Display.to_string(my_user)`).

## 8. Enums (Algebraic Data Types)

Enums (sum types) are declared with the `enum` keyword. Each variant is written on its own line — there is no `|` separator, no comma, and no single-line form. Enum bodies are always multi-line.

```nomi
enum Maybe<T> {
    None
    Some T
}
```

Enums are always named. Layout is fixed: one variant line at a time, always multi-line — the formatter never collapses an enum body onto one line. Variants can carry data. Helper functions and `once` values live beside the enum, at file level or in an inherent `impl` block. Derives are sibling `derive Iface for Enum` declarations. Manual interface implementations are sibling `impl Iface for Enum { ... }` blocks. Enums can implement interfaces (see §35 for an example with `Display`).

A variant payload is written as a type expression after the variant name, with no wrapping parens — same shape as a distinct-type declaration. Examples: `Number Int`, `Position #(Int, Int)`, `Card {rank: Int}`, `Computation (Int) -> Int`. Construction (`Token.Number(42)`) and pattern matching (`case t { Number(n) -> ... }`) keep their parens / braces; only the declaration drops the construction-site parens.

Enum variants live in the enum's member surface. Within a single enum, variant names cannot conflict with other variant names. The compiler errors on collisions.

### Variant Kinds

Four kinds of variants. The fragments below show variants as they appear inside an enum body — each on its own line.

**Bare variants** — no data:

```nomi
Point

Permanent

None
```

**Single-payload variants** — exactly one payload type, written as a bare type expression after the variant name:

```nomi
Some T
Milliseconds Int
Rectangle #(Float, Float)    // payload is a tuple
Bag Map<String, Int>        // payload is a generic type
Computation (Int) -> Int    // payload is a function type
```

A single-payload variant carries one value. The payload type can be anything — primitive, tuple, struct, distinct, generic, or function type. If you want to bundle several values, spell the tuple explicitly (`Rectangle #(Float, Float)`) or — usually nicer — name the bundle as a distinct or use a struct variant. Multi-positional declarations (`Rectangle Float, Float`) are not accepted; the parser rejects them with a hint pointing at the explicit tuple form. The payload syntax mirrors a distinct-type declaration (`type Rectangle #(Float, Float)`).

**Struct variants** — named fields in braces, with optional defaults (the payload's anon-struct shape keeps its commas — only declaration *bodies* are newline-separated):

```nomi
Rectangle {width: Float, height: Float}
HttpError {status: Int, message: String, retryable: Bool = False}
```

**Embedded types** — a standalone struct or distinct type included as a variant via `embeds`. The type is defined independently and can appear in multiple enums:

```nomi
enum Item {
    embeds Circle // struct
    embeds UserId // distinct type
}
```

Only struct types and distinct types can be embedded. Embedding an interface (`embeds SomeIface`) is rejected by the checker — there is no meaningful subtyping relationship to install (open polymorphism over an interface would be `dyn Trait`-style dispatch, which Nomi does not support; see §13). For an interface-typed payload, use a single-payload variant: `Render Renderable`. Embedding an enum (`embeds Shape`), directly or through a `typealias` that names one, is rejected at the embedded name; carry the enum in a single-payload variant instead (`Shape Shape`, matched as `Drawable.Shape(Shape.Square(n))`). A distinct over an enum (`type Boxed Shape`) is a distinct type and embeds.

Embedded structs and embedded distinct types are types of their own and can be used as function parameter types (see "Variants as Parameter Types" below). Struct variants and embedded structs are constructed with a struct's two forms — the literal `#Shape.Rectangle{...}` or `#.Rectangle{...}`, or the record call form `Shape.Rectangle(#{...})` — and matched with a struct pattern, `#.Rectangle{...}`; a struct-shaped variant's paren pattern matches the anonymous struct of its fields, so `.Rectangle(r)` binds that record and `.Rectangle(#{width, height: 0.0})` is a record pattern over it. Single-payload variants use parens at construction and pattern sites (only the declaration drops the parens). §8 "Embedded Types" states the construction rule in full.

### Examples

```nomi
struct Circle {
    radius: Float
}

enum Shape {
    embeds Circle
    Rectangle {width: Float, height: Float}
    Point
}
```

```nomi
enum Error {
    HttpError {status: Int, message: String, retryable: Bool = False}
    Timeout
    ConnectionRefused
}
```

### Construction and Typing

A variant constructor produces a value of the **enum type**, not a distinct variant type. This means `#Shape.Circle{radius: 5.0}` has type `Shape`, `Some(42)` has type `Maybe<Int>`, and `True` has type `Bool`. There is no separate "variant type" to widen from — the value is already the enum type at the point of construction.

Variants must be **qualified** at construction sites — either as `EnumName.Variant` (`Shape.Circle(5.0)`) or as the dot-leading shorthand `.Variant` (`.Circle(5.0)`) when the expected type at the position determines the enum. Bare names like `Circle(5.0)` or `Point` are not legal; the qualification rule eliminates ambiguity when two enums share variant names (e.g. enums `BinaryOp` and `AssignOp` both having `Add` and `Sub` variants in the same file). The few prelude-promoted variants — `Some`, `None`, `Ok`, `Err`, `True`, `False` — are bare-callable everywhere because the standard library's `Maybe`, `Result`, and `Bool` enums are imported by default; user enums cannot opt into the prelude. See the *Variant resolution* subsection below for the full rule on when the dot-leading form resolves.

```nomi
shape = #Shape.Circle{radius: 5.0} // embedded struct

shape = #Shape.Rectangle{width: 3.0, height: 4.0} // struct variant

shape = Shape.Point // bare variant

err = #Error.HttpError{status: 404, message: "not found"} // retryable defaults to False

err = #Error.HttpError{status: 503, message: "unavailable", retryable: True}

x = Animal.Dog("Rex") // x has type Animal — no annotation needed

y = Some(42) // y has type Maybe<Int>          (prelude variant)

active = True // active has type Bool            (prelude variant)

// Dot-leading shorthand at typed positions:
s: Shape = #.Circle{radius: 5.0} // expected type Shape from the annotation

c: Color = .Red // bare-payload form

paint(.Red) // expected type Color from paint's signature
```

This rule eliminates the need for typed bindings purely for type widening. You never need to write `x: Animal = Animal.Dog("Rex")` because `Animal.Dog("Rex")` is already an `Animal`. Embedded structs and embedded distinct types can also be used as **parameter types** (see "Variants as Parameter Types" below). Such a parameter takes a value of the embedded type, not of the enum: a `Shape` may hold another variant, so a caller holding a `Shape` matches it first (see "Type Narrowing").

When a file works heavily with one enum, an owner selector can lift specific variants into bare scope (see §21):

```nomi
import shape.Shape.{Circle, Point, Rectangle}

shape = #Circle{radius: 5.0} // bare-callable after the owner selector import

shape = Point
```

A selected variant of a generic enum infers the enum's type arguments as the qualified spelling does: after `import shapes.Holder.{Of}`, `#Of{value: 1}` is a `Holder<Int>`, from the field or from the expected type where there is one. A list-, map-, vector- or set-payload variant written as a literal infers the same way, in every spelling, from its elements: with `Items List<T>` and `Named Map<String, T>` in `Bag<T>`, `shapes.Bag.Items[1, 2]`, `Bag.Items[1, 2]` in the declaring file and `Items[1, 2]` after `import shapes.Bag.{Items}` are each a `Bag<Int>`, and so is `%Named{"a" => 4}`. An empty literal (`Items[]`, `%Named{}`) takes its arguments from the expected type, and without one its binding needs an annotation, as `[]`'s does. The std collections' literals, `#[1, 2]`, `%[1, 2]` and `%{"a" => 1}`, infer the same way: `%[]` takes its element type from the expected type.

**A variant's name never takes a type's place.** Because a same-file variant is never referenced bare, its name does not compete with the file's other names. A variant may share its name with a type, interface or function the same file declares, or with a name the file imports, in either source order, and that other name is what the bare spelling means: in type position, in an `impl` header, as the owner of a call, and as a value. The variant stays reachable as `Enum.Variant`, as `.Variant` and in patterns. No legal program is made ambiguous by this, because the only reference whose meaning changes is a bare same-file variant, which is an error either way. A variant imported with an owner selector is an import like any other, and two non-variant declarations of one name remain the error "'X' is already defined in this scope".

```nomi
import std/json.{Json, ToJson}

// The variant is Op.ToJson; bare ToJson is still the interface.
enum Op {
    ToJson
    Other
}

fn encode<T>(v: T) -> Json where T: ToJson {
    ToJson.to_json(v)
}
```

### Constructors as Function Values

A name that builds a value from one argument is a function value. A positional variant named without a call is its constructor: `Shape.Circle` is a `(Float) -> Shape`, and `Some` is a `(T) -> Maybe<T>`. So is a distinct type that wraps a value (§15 *Distinct Types*): `Id` for `type Id Int` is an `(Int) -> Id`. A value built through the function is the value the call builds: it compares equal and renders the same.

```nomi
enum Shape {
    Circle Float
    Segment #(Int, Int)
    Point
}

circles = Iter.map([1.0, 2.5], Shape.Circle) |> Iter.to_list()

somes: List<Maybe<Int>> = Iter.map([1, 2], Some) |> Iter.to_list()
```

- **A generic constructor is instantiated as a generic function used as a value is** (§5): its type parameters are solved from the function type its position expects. `Iter.map(ns, Some)` solves `T` from the elements. `Ok` and `Err` leave the other parameter to the result, so `Iter.map(ns, Ok)` needs an annotated destination (`rs: List<Result<Int, String>> = Iter.map(ns, Ok) |> Iter.to_list()`) or an annotated function (`f: (String) -> Result<Int, String> = Err`). With nothing to solve it from, `f = Some` is the error "cannot infer type parameter T of generic constructor 'Some' used as a value".
- **A variant over a tuple takes the tuple.** A variant has one payload, so its function takes one argument. `Shape.Segment` is a `(#(Int, Int)) -> Shape`, and `Iter.map(pairs, Shape.Segment)` builds one from each tuple. The flat call `Shape.Segment(1, 2)` is a call form of that one tuple, not a second parameter list. A tuple-distinct (`type Pair #(Int, String)`) has the same function, `(#(Int, String)) -> Pair`, but no flat call: `Pair(1, "x")` is an error, and the literal `#Pair(1, "x")` builds one from its elements (§15 *Distinct Types*). A variant's flat call takes the elements as a call takes arguments: a generic variant's type arguments are solved from them as from the tuple (`Two.Both(1, "x")` is a `Two<Int, String>`), a pipe's head is the first (`2 |> Shape.Segment(3)`), and a partial capture leaves one open (`&Shape.Segment(&1, 0)` is an `(Int) -> Shape`).
- **A payload-free variant is a value, not a function.** `Shape.Point` is a `Shape`.
- **A struct-shaped variant is not a function value**, as a struct is not: `Iter.map(records, Shape.Rectangle)` is an error naming the brace and record forms.
- **`.Variant` is not a function value.** A dot-leading variant resolves only where the expected type is its enum (*Variant Resolution* below), and a function type is not an enum: `f: (Float) -> Shape = .Circle` is an error whose hint names `Shape.Circle`.
- **An opaque type's constructor stays private.** Naming it as a value outside its file is the same error as calling it.

### Pattern Matching

Patterns inside a `case` arm use the dot-leading form `.Variant` or the fully-qualified form `Shape.Variant`. Bare variant names are rejected for non-prelude variants — the rule is symmetric with construction. The dot-leading form is the idiomatic choice; qualified is the disambiguation/explicit alternative.

```nomi
case shape {
    #.Circle{radius} -> radius * radius * 3.14
    #.Rectangle{width, height} -> width * height
    .Point -> 0.0
}

case err {
    #.HttpError{status, retryable: True} -> retry(status)
    #.HttpError{status, message} -> log(message)
    .Timeout -> retry_later()
    .ConnectionRefused -> fail()
}
```

Qualified pattern forms (`#Shape.Circle{radius} -> ...`) are also accepted when disambiguation is wanted or when the reader prefers the explicit form. Prelude variants (`Ok`, `Err`, `Some`, `None`, `True`, `False`) stay bare via the prelude scope export and are not rewritten through the dot-leading rule.

### Variant Resolution

A `.Variant` expression (or `.Variant(payload)` / `#.Variant{fields}` / `.Variant[elements]` / `#.Variant[elements]` / `%.Variant{entries}`) resolves to the variant `Variant` of enum `E` when, at the expression's syntactic position, the **expected type** — as determined by the checker's standard outside-in walk from the position to the enclosing declaration — is `E`. The walk passes through transparent constructs (`case`/`if-else` arm result positions, block-expression returns, `return` statements, list, tuple and map literal elements, and generic-function argument positions whose `T` is constrained by the call's return type against an outer expected type, including the head of a pipe into such a call). The constraint is solved before the arguments are checked, so it reaches a variant nested anywhere inside them: `exits: Map<Direction, Place> = Iter.to_map([#(.North, .Cave)])` checks the list against `Iter<#(Direction, Place)>`. A list, vector or set literal checked against `Iter<T>` checks each item against `T`, as it would against `List<T>`. An operand of `==`, `!=`, `<`, `>`, `<=` or `>=` is a determining position too: the two operands have one type (§22), so each is checked against the other's. The operand whose type depends on its position (a `.Variant` in any spelling, or a `#{...}` literal with no type name) is checked second, against the type of the other: `k == .Deposit` and `.Deposit == k` both resolve against `k`'s enum, and `p == #{x: 1, y: 2}` builds `p`'s struct. The comparison's own expected type (`Bool`, or none at a statement or `if` condition) is never its operands'. When both operands are dot-leading (`.Deposit == .Withdrawal`), neither names the enum, and the comparison is an error that asks for one to be qualified. A **generic variant constructor** is exactly such a position: `Ok(.Quit)` in a `fn (): Result<Input, String>` resolves `.Quit` to `Input.Quit`, because `Ok`'s payload param is pinned from `Ok`'s `Result<T, E>` return against the outer `Result<Input, String>` — and likewise inside a `case`/`if` arm, an annotated binding, or an argument slot. It does **not** look at later uses, unannotated `=` bindings, a bare `.Variant` standing alone as a **pipe stage** (`… |> .Guess() |> Ok()`, where the enum would have to be read from the downstream `Ok()`), or any other site that would require backward inference. This matches Nomi's broader explicit-types philosophy — the same outside-in walk the checker already does for ordinary type checking, reused for variant resolution.

Concrete consequences:

| Site | Resolves? |
|---|---|
| `#Box{color: .Red}` | ✓ field type is `Color` |
| `paint(.Red)` (paint takes Color) | ✓ param type is `Color` |
| `Iter.sort(xs, .Descending)` | ✓ param type is std/comparable's `Direction`; the calling file need not import it |
| `Iter.sort_with(xs, fn a, b -> .Equal)` | ✓ the lambda's result is checked against the callback's declared result, `Ordering`; a declared result holding a type parameter supplies no enum |
| `paint(case x { 1 -> .Red; _ -> .Blue })` | ✓ propagates through case arms |
| `palette: List<Color> = [.Red, .Green]` | ✓ element type from annotation |
| `themes: Map<String, Color> = %{"a" => .Red}` | ✓ map value type from annotation |
| `c: Color = .Red` | ✓ binding annotation |
| `c: Color = id(.Red)` (generic `id<T>(x: T): T`) | ✓ annotation pins `T` through `id`'s return |
| `pair: #(Color, Size) = #(.Red, .Large)` | ✓ element types from annotation |
| `exits: Map<Direction, Place> = Iter.to_map([#(.North, .Cave)])` | ✓ annotation pins `K` and `V` before the list is checked against `Iter<#(K, V)>` |
| `exits: Map<Direction, Place> = [#(.North, .Cave)] \|> Iter.to_map()` | ✓ the pipe's head is checked against the stage's pinned first parameter |
| `Ok(.Quit)` in `fn (): Result<Input, String>` | ✓ generic constructor's payload pinned from the return |
| `Ok(.Guess(n))` in a `case`/`if` arm | ✓ expected type flows from the arm |
| `c: Color = if cond { .Red } else { .Blue }` | ✓ annotation propagates through if |
| `pub fn default_color() -> Color { .Red }` | ✓ annotated return position |
| `c == .Red`, `.Red != c`, `if c == .Red { … }` (`c: Color`) | ✓ the other operand's type is `Color` |
| `size < .Large` (`size: Size`, `Size` is `Comparable`) | ✓ the other operand's type is `Size` |
| `p == #{x: 1, y: 2}` (`p: Point`) | ✓ builds a `Point` from the other operand's type |
| Patterns: `case (jv: Json) { %.Obj{...} -> ... }` | ✓ scrutinee's type is enum |
| `c = .Red` (unannotated binding) | ✗ no enclosing expected type |
| `id(.Red)` alone (statement position) | ✗ `T` unconstrained |
| `c = id(.Red); paint(c)` | ✗ would require backward flow from `paint` |
| `… \|> .Guess() \|> Ok()` (bare `.Variant` pipe stage) | ✗ would require backward flow from the next stage — qualify it (`Input.Guess()`) |
| `.Red == .Blue` | ✗ neither operand names the enum — qualify one (`Color.Red == .Blue`) |
| `paint(.NotAColor)` (Color has no `NotAColor`) | ✗ enum found, variant absent |
| `f(.Red)` where `f` takes `Box` | ✗ expected type isn't an enum |

**Prelude carve-out.** The prelude variants `Ok`, `Err`, `Some`, `None`, `True`, `False` remain reachable as bare names through the prelude's scope export, not through variant resolution. Calling `Ok(user)` or matching `case r { Ok(v) -> ... }` is ordinary name resolution. The dot-leading rule and prelude bare-name reachability are independent paths — they never collide because the prelude exports don't conflict with the dot form (both `Ok(user)` and `.Ok(user)` work; the bare form is idiomatic for prelude variants).

**Diagnostics.** Three failure modes:

- *No determinable enum type at this position.* The position has no annotation/signature to walk to, and no enclosing call/case/binding determines an enum. Example: `c = .Red` with `c` never used in a typed position. Resolution: annotate the binding, or qualify the variant. An argument whose parameter type is a type parameter of the called function that nothing has solved determines no enum either: in `total = String.to_int(s) |> Maybe.to_result(.SyntaxError) |> try`, `to_result`'s error type is fixed only by the later `try`. The error says no enum is known there and names the qualified variant (`Error.SyntaxError`) when one enum in scope declares it.
- *Expected type isn't an enum.* The walk found an expected type, but it's not an enum. Example: `b: Box = .Red`. Resolution: check the binding's type, or qualify.
- *No such variant on the enum.* The expected type is an enum, but the named variant doesn't exist on it. Example: `c: Color = .Purple`. Resolution: pick a valid variant.

**Struct literals use the same walk.** A `#{...}` literal, with no type name and no spread, at a position whose expected type is a nominal struct builds that struct: `#Box{color: .Red}` resolves `.Red` from the field type, and `#Person{name: "Ada", address: #{street: "1 Main", city: "Bath"}}` builds an `Address` from the field type the same way. The positions are this section's, and so are the limits: an unannotated binding has no expected type, so `a = #{street: "1 Main", city: "Bath"}` is an anonymous struct. See *Target-typed construction*.

### Field Accessors

`.name` is a function that reads field `name` from its argument. A lower-case name after the leading dot is a field; a capitalized one is a `.Variant`. A chain `.address.city` reads `address`, then `city` from it, and a tuple index reads an element (`.0`, `.1`, or `.1.0` for the first element of the second).

```nomi
names = users |> Iter.map(.name) |> Iter.to_list()

oldest = Iter.sort_by(people, .age) |> Iter.last()

cities = users |> Iter.map(.address.city) |> Iter.to_list()

seconds = pairs |> Iter.map(.1) |> Iter.to_list()
```

An accessor stands only where a function is expected, and it resolves by the same outside-in walk as a `.Variant`. The expected type must be a function type `(S) -> R` of one parameter, and S must be known at the accessor's position: an annotation, a declared parameter type, or a generic parameter that an earlier argument or the piped value has already pinned. S must be a struct, an anonymous struct or a tuple. The accessor's type is `(S) -> F`, where F is the type of the last field read, and R unifies with F as a lambda's result would. In `Iter.map(users, .name)` the first argument pins `T` to `User` before `.name` is checked, so `.name` is `(User) -> String` and the call returns `Iter<String>`.

A generic struct is read at its instantiation: over a `List<Box<Point>>`, `.value` is `(Box<Point>) -> Point`. A field that holds a function is returned, not called: with `handler: (Request) -> Response`, `.handler` is `(Server) -> (Request) -> Response`.

As an argument, an accessor routes like a trailing lambda, so `Iter.sort_by(people, .age)` fills `key` past the defaulted `direction`.

| Site | Accepted? |
|---|---|
| `Iter.map(users, .name)` | ✓ `users` pins the parameter type first |
| `users \|> Iter.map(.name)` | ✓ the piped value pins it |
| `get: (User) -> String = .name` | ✓ the annotation gives it |
| `fn name_of() -> (User) -> String { .name }` | ✓ the return type gives it |
| `user \|> .name` | ✗ a pipe stage; write `user.name` |
| `get = .name` | ✗ no function type is expected |
| `apply(.name, user)` (`fn apply<A, B>(f: (A) -> B, x: A) -> B`) | ✗ `A` is not known until a later argument |
| `n: Int = .name` | ✗ the expected type is not a function |

```nomi
import std/io

struct Address {
    city: String
}

struct User {
    name: String
    age: Int
    address: Address
}

fn main() {
    users = [
        #User{name: "Ada", age: 36, address: #Address{city: "London"}},
        #User{name: "Alan", age: 41, address: #Address{city: "Wilmslow"}},
    ]

    io.print(
        users
        |> Iter.map(.name)
        |> Iter.to_list()
    )

    io.print(
        Iter.sort_by(users, .age)
        |> Iter.map(.address.city)
        |> Iter.to_list()
    )

    age: (User) -> Int = .age

    io.print(
        Iter.map(users, age)
        |> Iter.to_list()
    )
}
```

**Diagnostics.** Each points at the accessor:

- *No function expected.* `` `.name` reads a field only where a function is expected; write `x.name` to read it from a value ``. A pipe stage gets `` `.name` is not a pipe stage; read the field from the value instead, as in `x.name` ``.
- *Parameter type not known.* `` `.name` needs the record type it reads from; annotate the binding, e.g. `f: (User) -> String = .name` ``.
- *No such field.* `struct 'User' has no field 'nmae'`, with the hint `did you mean 'name'?`, the same as for `user.nmae`.
- *Not a record.* `` `.name` reads a field of a struct, a record or a tuple, and Int is none of those ``.

### Variants as Parameter Types

An embedded type is a type of its own, so a function that only operates on that variant can take it directly and read its fields:

```nomi
struct Circle {
    radius: Float
}

enum Shape {
    embeds Circle
    Rectangle {width: Float, height: Float}
    Point
}

// Circle is both a standalone struct and a Shape variant
fn area(circle: Circle) -> Float {
    3.14 * circle.radius * circle.radius
}

// Both are valid Shape values
shapes: List<Shape> = [#Circle{radius: 5.0}, #Shape.Rectangle{width: 3.0, height: 4.0}]
```

> **Status: not yet implemented.** A struct variant declared inline
> (`Rectangle {width: Float, height: Float}`) is not a type:
> `fn perimeter(rect: Rectangle)` and `fn perimeter(rect: Shape.Rectangle)`
> are both `unknown type` errors. Declare the payload as a struct and
> `embeds` it when a function needs to take that one variant. Track via
> `feature-status.md`.

**Embedded subtyping goes one way.** A value of an embedded type is accepted wherever its enum is expected: an argument, an annotated binding, a field, a return, a list, map or set element, and a type argument of any of those (`List<Circle>` where `List<Shape>` is expected). An enum is never accepted where one of its embedded types is expected, at any of those positions, since a `Shape` may hold a variant other than `Circle`. `area(shape)` with `shape: Shape` is the error "argument 1: expected Circle, got Shape", whose help says to match the value first. The only way to get a `Circle` from a `Shape` is to match it: in an arm whose pattern is `#.Circle{...}`, the matched name is a `Circle` (see "Type Narrowing"):

```nomi
fn total(shape: Shape) -> Float {
    case shape {
        #.Circle{} -> area(shape) // shape is a Circle in this arm
        _ -> 0.0
    }
}
```

Where a `Circle` and a `Shape` meet without a direction, as the two branches of an `if` or `case`, the elements of an unannotated list, vector or set, the keys or values of an unannotated map, or two arguments of one generic parameter, the result is the `Shape`. The same holds inside a tuple, record or collection: `[#(1, circle), #(2, shape)]` is a `List<#(Int, Shape)>`, and `[[circle], [shape]]` a `List<List<Shape>>`. When neither side takes the other's values, as `#(circle, shape)` beside `#(shape, circle)`, the elements do not join and the literal is an error. `==` compares a `Circle` with a `Shape` as two `Shape`s. An impl function follows the same rule against its interface: it may not take a `Circle` where the interface's parameter is a `Shape`, nor return a `Shape` where the interface returns a `Circle`. A function value passed as an argument follows it too, since the callee calls it: `Iter.map(shapes, take)` with `take(c: Circle)` and `shapes: List<Shape>` is an error, because `take` would be called with every `Shape`, and `Iter.map(circles, area)` with `area(s: Shape)` is accepted.

### Embedded Types

A standalone struct or distinct type can be embedded in one or more enums via `embeds`. Only structs and distinct types can be embedded — enums cannot embed other enums. This keeps variant provenance clear (every variant name traces to one definition) and avoids transitive flattening complexity. The embedded type exists independently — it can be constructed, passed to functions, and used as a type whether or not it's part of an enum:

```nomi
struct Circle {
    radius: Float
}

type UserId String

enum Shape {
    embeds Circle
    Point
}

enum Drawable {
    embeds Circle
    Line {length: Float}
}

enum Identifier {
    embeds UserId
    Anonymous
}

// Circle works on its own
c = #Circle{radius: 5.0}

fn area(c: Circle) -> Float {
    3.14 * c.radius * c.radius
}

// And as a variant of either enum
s: Shape = #Circle{radius: 5.0} // s is a Shape

d: Drawable = #Circle{radius: 5.0} // d is a Drawable

// Distinct types work the same way
id: Identifier = UserId("abc") // id is an Identifier
```

**Subtype coercion.** Embedded types are subtypes of their containing enum. A value of type `Circle` (where `Shape` has `embeds Circle`) is also a value of type `Shape`, and flows into `Shape`-typed positions — function arguments, list elements, return values, bindings — without explicit `Shape.Circle(...)` wrapping:

```nomi
fn render(s: Shape) -> String {
    ...
}

c = #Circle{radius: 5.0}

render(c) // Circle -> Shape arg

shapes: List<Shape> = [c, #Circle{radius: 1.0}, Shape.Point]

// each element coerces independently
uid = UserId("alice")

ids: List<Identifier> = [uid, UserId("bob"), Identifier.Anonymous]
```

The coercion reaches inside type arguments, tuple elements and record fields: a `Maybe<Circle>` fits where a `Maybe<Shape>` is expected, and so does a `Box<Circle>` where a `Box<Shape>` is, for any generic type. A construction in such a position builds the expected type, so its embedded value is widened where it sits:

```nomi
enum Box<T> {
    Full T
    Empty
}

b: Box<Shape> = Box.Full(c) // a Box<Shape>

h: Maybe<Shape> = Some(c)
```

A value already built at the narrower type converts the same way, inside a generic type, a tuple, a record or a collection's elements, keys and values: `m: Maybe<Circle> = Some(c)` then `w: Maybe<Shape> = m`, and `cs: List<Circle>` passed where a `List<Shape>` is expected.

The two operands of `==` and `!=` may differ the same way, and compare as the wider type: `Some(c) == Some(Shape.Point)` compares two `Maybe<Shape>` values and is `False`, and `Box.Full(c) == Box.Full(s)` for `s: Shape = c` is `True`. An operand that takes its type from the other (a literal holding `Shape.Point`) takes the wider of the two: `[c, c] == [s, Shape.Point]` compares two `List<Shape>` values.

The qualified construction form (`#Shape.Circle{radius: 5.0}`, `Identifier.UserId("alice")`) is still available — useful when type inference can't see the target enum, or when the reader benefits from the explicit variant tag. Pattern matching is unchanged: `case s { #Shape.Circle{radius} -> ... }` destructures embedded structs the same way regardless of how the value flowed in.

**Construction through a variant.** `Enum.V` followed by a construction syntax takes exactly the construction syntaxes V's own shape takes, builds that V, and widens it into the enum. The rule covers every struct-shaped variant, declared inline or embedded:

```nomi
struct Circle {
    radius: Float = 1.0
}

type UserId String

type Expired

enum Shape {
    embeds Circle
    Rectangle {width: Float = 2.0, height: Float}
}

enum Session {
    embeds UserId
    embeds Expired
}

// A struct shape, inline or embedded: the brace form and the record call
// form, piped or not. Omitted fields take their defaults.
a = #Shape.Circle{radius: 2.0}

b = Shape.Circle(#{radius: 2.0})

c = Shape.Circle(#{}) // #Circle{radius: 1.0}

d = #{height: 3.0} |> Shape.Rectangle() // #Rectangle{width: 2.0, height: 3.0}

e = Shape.Rectangle(dimensions) // a record value, as `Circle(rec)` takes one

// An embedded wrapping distinct: the distinct's call form, from its inner value.
f = Session.UserId("alice") // builds a UserId

g = "bob" |> Session.UserId()

// An embedded zero-sized type: the value, with no arguments.
h = Session.Expired
```

A struct has no positional call form, so neither does a struct-shaped variant: `Shape.Rectangle(4.0, 5.0)` is an error naming the brace and record forms. A struct has no function value either, so a struct-shaped variant is not one: `f = Shape.Rectangle` and `Iter.map(records, Shape.Circle)` are errors, and a lambda that builds the variant (`fn r -> Shape.Rectangle(r)`) is the function. A positional variant (`Iter.map(xs, Shape.Dot)`) and an embedded wrapping distinct (`Session.UserId`) are functions of their payload and remain values. Passing a value that is already a V is not construction and is rejected too — `Shape.Circle(c)` with `c: Circle`, or `Session.UserId(uid)` with `uid: UserId` — because such a value widens on its own at any position that wants the enum (`s: Shape = c`, `area(c)`). `Session.Expired()` is rejected: the variant takes no arguments.

The built value is the same value as V widened into the enum. `Session.UserId("alice") == (w: Session = UserId("alice"))` is `True`, both render `UserId("alice")`, and both match `Session.UserId(name)` with `name` bound to the inner `"alice"`.

**Dispatch follows the enum.** A value in a position typed as the enum is a value of the enum, however it got there: built through the variant, or widened by a binding (`s: Shape = c`), an argument, a result, a field or a list element. Interface calls on it run the enum's impls, not the embedded type's: `Display.to_string(s)`, `Debug.inspect(s)`, and `==` and `<` through `Equatable` and `Comparable`. The same value typed as the embedded type (`c: Circle`) runs the embedded type's impls. An enum's automatic `Debug` and its derived impls hand an embedded variant to the embedded type's impl, which is why both values above render `UserId("alice")`.

**Variant payload shape.** The variant's payload type — what the constructor takes and what the pattern destructures — depends on which kind of type is embedded:

- **Struct embed** (`embeds Circle`): the payload is the struct itself. `case s { #Shape.Circle{radius} -> ... }` destructures the struct fields.
- **Wrapping-distinct embed** (`embeds UserId` where `type UserId String`): pattern destructure binds the *inner* value directly: `case id { Identifier.UserId(name) -> ... }` binds `name: String`. Qualified construction is the distinct's own call form, `Identifier.UserId("alice")`, which builds a `UserId` from the inner String; the value is a `UserId`, equal to `UserId("alice")` widened into `Identifier`. A distinct over any other value works the same way: for `type Wrapped Circle`, `Shape.Wrapped(#{r})` is a struct pattern over the Circle, and `Shape.Wrapped(#Circle{r: 1})` builds a `Wrapped`.
- **Zero-sized distinct embed** (`embeds Expired` where `type Expired`): the payload is the (zero-sized) distinct. The variant takes no arguments — `Session.Expired` is field-access, not a call.

**Reading a field off the enum.** `s.radius` on a value typed as the enum reads the field of whichever variant the value is, from a struct-shaped variant or an embedded struct. A value of a variant without the field faults at run time (`variant 'Rect' has no field 'radius'`). Every variant that has the field must give it the same type: `w: Int` in one variant and `w: Float` in another makes `s.w` the error `field 'w' is Int in Shape.Rect and Float in Shape.Circle, so reading it off a Shape has no one type`; read it in a `case` arm that matches the variant.

## 9. Built-in Types: Maybe and Result

### Maybe

```nomi
enum Maybe<T> {
    Some T
    None
}
```

### Result

```nomi
enum Result<T, E> {
    Ok T
    Err E
}
```

### Maybe Functions

Owner functions on `Maybe`, called `Maybe.map(m, f)` and so on:

```nomi
fn some?(maybe: Maybe<T>) -> Bool
fn none?(maybe: Maybe<T>) -> Bool
fn map(maybe: Maybe<T>, f: (T) -> U) -> Maybe<U>
fn flat_map(maybe: Maybe<T>, f: (T) -> Maybe<U>) -> Maybe<U>
fn with_default(maybe: Maybe<T>, default: T) -> T
fn to_result(maybe: Maybe<T>, error: E) -> Result<T, E>
```

### Result Functions

Owner functions on `Result`, called `Result.map(r, f)` and so on:

```nomi
fn ok?(result: Result<T, E>) -> Bool
fn err?(result: Result<T, E>) -> Bool
fn map(result: Result<T, E>, f: (T) -> U) -> Result<U, E>
fn map_err(result: Result<T, E>, f: (E) -> F) -> Result<T, F>
fn flat_map(result: Result<T, E>, f: (T) -> Result<U, E>) -> Result<U, E>
fn with_default(result: Result<T, E>, default: T) -> T
fn to_maybe(result: Result<T, E>) -> Maybe<T>
```

### The `try` Keyword

Works on both `Result` and `Maybe`. A prefix keyword that unwraps the success value or returns early from the nearest function boundary (the enclosing `fn` or lambda). A `once` initializer and a parameter or field default have no such boundary, and a `try` written directly in one is a compile error (§2, *`once` Bindings*). `try x` is sugar for a pattern match with `return`:

```text
try x  ≡  case x { Ok(v) -> v, Err(e) -> return Err(e) }
```

(and similarly for `Maybe`: unwrap `Some`, or `return None`).

```nomi
// With Result: unwrap Ok or return Err
fn process(id: Int) -> Result<User, String> {
    user = try users.get(id)
    profile = users.get_profile(user)
    try email.send(profile)
}

// With Maybe: unwrap Some or return None
fn find_name(id: Int) -> Maybe<String> {
    user = try users.find(id)
    Some(user.name)
}

// Works with pipes too — prefix the fallible stage with `try`
id
|> try users.get()
|> users.get_profile()
|> try email.send()
```

The propagated value must inhabit the nearest function boundary's declared result type. Three clauses, all checked at the `try` and all naming both sides:

1. **the boundary must be a `Result` or a `Maybe`** — a function returning `Int`, or one with no declared return type (whose result is `Unit`), has nothing for the propagated value to inhabit;
2. **the flavours must agree** — `Result` if `try` is applied to a `Result`, `Maybe` if applied to a `Maybe`. Inside a lambda this means the lambda's return type, not the outer function's;
3. **when both sides are `Result`s, the ERROR TYPES must be the same type.**

Clause 3 matters because `try` propagates the operand's `Err` payload out of the boundary unchanged. An error type the boundary does not declare would leave the signature not describing the value, and the failure would surface far away (`Display.to_string: no implementation for type 'json.Json.DecodeError'`) naming neither the `try` nor the boundary. All three are diagnostics at the `try`:

```nomi
// REJECTED — Json.decode fails with a Json.DecodeError and this
// function promises String, so the Err path would carry a value the
// signature does not describe.
//   try error type mismatch: expected String, got Json.DecodeError
fn decode_len(s: String) -> Result<Int, String> {
    parsed = try Json.decode(s)

    Ok(String.length(Json.encode(parsed)))
}

// ACCEPTED — the error is converted AT THE SITE, so the declared type
// describes every value the function can produce.
fn decode_len(s: String) -> Result<Int, String> {
    parsed = try Result.map_err(Json.decode(s), fn e: Json.DecodeError -> Display.to_string(e))

    Ok(String.length(Json.encode(parsed)))
}
```

There is no implicit conversion at the `try` boundary — no `From`-style widening, no coercion to a common supertype. `Result.map_err` is the conversion, and it is written where the mismatch is. The one relaxation is the ordinary one: an INTERFACE-typed declared error side accepts a concrete implementer of that interface, exactly as an interface-typed parameter does.

At a boundary without a declared return type — a `concurrent` block or an unannotated lambda — the error side of the result is **inferred from the `try` sites** inside it. A block whose tail is `Ok(...)` pins only the success type; the error type comes from the `try`-unwrapped `Result`s, so this needs no annotation:

```nomi
// outcome: Result<Int, String> — the String is inferred from `try Task.await(t)`
outcome = concurrent {
    t = Task.spawn(fn -> fetch()) // fetch(): Result<Int, String>
    Ok(try Task.await(t))
}
```

`try` sites that unwrap `Result`s with incompatible error types in the same boundary are rejected (the boundary's error type would be ambiguous). A `try` on a `Maybe` contributes no error type, so `Maybe`-tailed boundaries are unaffected. When no `try` site fixes the error type, as in a `concurrent` block whose only `try` is `try Ok(3)`, it stays open, as `Ok(1)`'s does (see "Determined type arguments"), and the result is a value like any other: it can be matched, compared and interpolated.

Clause 3 therefore does not arise at an inferring boundary — the error type IS the `try` sites' — but clauses 1 and 2 still do, and they are checked against the result the body PRODUCED rather than against a declaration:

```nomi
// REJECTED — the lambda's result is Int, so the propagated Err has
// nowhere to go and the caller would receive it where it expects an Int.
//   `try` cannot propagate a Result out of a lambda whose result is Int
_ = fn x: Int -> {
    v = try parse(x) // parse(x): Result<Int, String>

    v
}
```

## 10. Pattern Matching

`case` keyword with `->` arrows. Two forms:

### Matching on a value (destructuring)

```nomi
case shape {
    #.Circle{r} -> 3.14 * r * r
    #.Rectangle{w, h} -> w * h
    .Point -> 0
}
```

Variant patterns use the dot-leading form (`.Circle`) or the fully-qualified form (`Shape.Circle`); a bare variant prefix (`Circle`, `#Circle{radius}`, `Items[x, y]`, `%Named{"k" => v}`) is accepted only for a prelude variant or one an owner selector imported (`import shape.Shape.{Circle}`, or an alias it binds), and is rejected otherwise. The qualifier of the fully-qualified form must name the scrutinee's enum (or a module-qualified or imported spelling of it): an unknown name is an error (`C6lor.Blue`: `unknown type "C6lor"`), and so is another type, even one with a variant of that name (`Mood.Blue` against a `Color`). The dot-leading prefix resolves against the scrutinee's enum type — the same outside-in walk described in §8's *Variant Resolution* subsection, with the case's scrutinee as the determining position. Prelude variants (`Ok`, `Err`, `Some`, `None`, `True`, `False`) remain bare-callable in patterns via the prelude scope export.

A literal pattern (`3`, `-1`, `2.5`, `1.50d`, `"a"`, `'a'`) has its literal's type, and the value it is matched against must have that same type, at any depth: a `case` arm, a variant payload, a tuple element, a struct field, a list or vector element, a set element, a map value, an `assert` pattern or a binding's `else` pattern. A literal never converts, so `case n { "a" -> ... }` over an `Int` is the compile error `pattern "a" is a String, but the value is an Int`, and `3` does not match a `Float` (write `3.0`).

### Ad hoc conditionals (no value)

```nomi
case {
    x == 0 -> "zero"
    x > 0 -> "positive"
    _ -> "negative"
}
```

`_` in patterns means "ignore this value."

An arm's body is one expression. A body of several statements is a block, in braces:

```nomi
case result {
    Ok(n) -> {
        io.print("got ${n}")
        n
    }

    Err(_) ->
        0
}
```

Indentation never ends an arm. A line below an unbraced body, indented deeper than its arm and not itself an arm, is the error ``this `case` arm continues on the next line; an arm with more than one statement needs braces``, whose hint shows the arm braced (``this `else` arm ...`` in a binding's `else` arms). The language server offers a quick fix that adds the braces.

A no-payload pattern (`None`, `True`, `.Point`) matches only variants that carry no data. Data-carrying variants must spell their payload explicitly — `Some(_)`, `Err(_)`, `#.Rectangle{}` — so the reader sees the shape of what the variant actually holds. A payload of a type with one value is still data: with `Ping(Unit)` or `Marked(Mark)` (for `type Mark`), the variant is built as `Signal.Ping(Unit)` and matched as `Signal.Ping(_)`, and only an `embeds` of a zero-sized type (`embeds Expired`) is written bare:

```nomi
case maybe {
    Some(n) -> n // ok — binds the inner Int (Some is a prelude variant, bare)
    Some(_) -> 0 // ok — explicit "ignore the payload"
    None -> -1 // ok — None has no data
}

case result {
    Ok(v) -> v
    Err -> 0 // error: Err carries data; use Err(_) or Err(name)
}
```

### Exhaustiveness Checking

The compiler requires all cases to be handled:

```nomi
// error: non-exhaustive match, missing None
case user.nickname {
    Some(name) -> "Hi, ${name}"
}

// error: non-exhaustive match, missing Point
case shape {
    #.Circle{r} -> r
    #.Rectangle{w, h} -> w * h
}
```

Use `_` as a catch-all when you don't need to handle every variant individually:

```nomi
case shape {
    #.Circle{r} -> r
    _ -> 0.0
}
```

Every `case` must be exhaustive: its unguarded arms together must match every value of the scrutinee's type. The compiler checks this component by component:

- `_` and a bare binding (`n -> ...`) match everything.
- An enum is covered when every variant is, and a variant is covered when the arms naming it cover its payload. `Some(Ok(_))`, `Some(Err(_))` and `None` together cover a `Maybe<Result<T, E>>`.
- A tuple, struct or distinct value is covered when its components are, taken together. `#(Some(_), _)`, `#(None, Some(_))` and `#(None, None)` cover a `#(Maybe<A>, Maybe<B>)`; a struct pattern that only binds its fields covers the struct.
- A list is covered by length: `[]` and `[head, ..tail]` cover every list, while `[]` and `[x]` miss lists of two or more. A vector is covered the same way: `#[]` and `#[first, ..rest]` cover every vector, and so does `#[..all]`.
- A literal (`0`, `"a"`, `'x'`), a string prefix (`"a" + rest`), a map pattern or a set pattern never covers its type, since the compiler does not enumerate the values of `Int`, `Float`, `Decimal`, `String`, `Byte`, `Codepoint`, `Map` or `Set`. Where one of those decides the match, some arm needs `_` or a binding in that position.
- An `or` pattern covers what its alternatives cover together: `Some(.Red or .Green)`, `Some(.Blue)` and `None` cover a `Maybe<Color>`.
- An arm with a `when` guard never counts toward coverage.

A subject-less `case` (§*Ad Hoc Conditionals*) needs an unguarded `_` arm.

A `case` that misses something is a compile error. When the compiler can name a missing value it does: `non-exhaustive case on Shape: missing Rect` for an enum, and `non-exhaustive case on #(Maybe<Int>, Maybe<Int>): missing #(None, None)`, `non-exhaustive case on List<Int>: missing [_, _, ..]` or `non-exhaustive case on Vector<Int>: missing #[_, _, ..]` for the rest. When the only thing missing is a value no literal list can cover, as in `case #(n, n) { #(0, 0) -> "zero" }`, the error is `non-exhaustive case: add a `_` arm`.

### Guards

Use `when` to add a condition to an arm. The arm runs when its pattern matches and the guard is `True`; otherwise matching continues with the next arm:

```nomi
case shape {
    #.Circle{r} when r > 10.0 -> "big circle"
    #.Circle{r} when r > 0.0 and r < 100.0 -> "valid circle"
    #.Circle{r} or #.Square{r} -> "has radius ${r}"
    _ -> "other"
}
```

A guard is a Bool expression: `n when n + 1 -> ...` is the error `` `when` guard must be Bool, got Int ``. In a guard, `or` is the boolean operator: `#.Circle{r} when r < 1.0 or r > 9.0 -> ...`.

### Alternatives: `or`

`A or B` is a pattern that matches a value either `A` or `B` matches. One arm then handles values of several shapes with one body:

```nomi
import std/io

enum Shape {
    Circle {r: Float}
    Square {side: Float}
    Rect {w: Float, h: Float}
    Point
}

fn describe(shape: Shape) -> String {
    case shape {
        #.Circle{r} or #.Square{side: r} when r > 10.0 -> "big, ${r} across"
        #.Circle{r} or #.Square{side: r} -> "${r} across"
        #.Rect{w: 0.0} or #.Rect{h: 0.0} or .Point -> "flat"
        #.Rect{w, h} -> "${w} by ${h}"
    }
}

fn closer(text: List<Codepoint>) -> String {
    case text {
        [')' or ']' or '}' as c, ..rest] -> "${c} closes, ${Iter.count(rest)} left"
        _ -> "no closer"
    }
}

fn main() {
    io.print(describe(#Shape.Circle{r: 12.0})) // big, 12.0 across
    io.print(describe(#Shape.Square{side: 2.0})) // 2.0 across
    io.print(describe(#Shape.Rect{w: 0.0, h: 3.0})) // flat
    io.print(describe(#Shape.Rect{w: 2.0, h: 3.0})) // 2.0 by 3.0
    io.print(closer([']', 'x'])) // ] closes, 1 left
}
```

The rules:

- **Every alternative binds the same names, each at one type.** `#.Circle{r} or #.Square{side: r}` binds `r` from whichever alternative matched. A name one alternative lacks is the error ``` `r` is bound by `#.Circle{r}` but not by `.Point`; every alternative of an `or` pattern must bind the same names ```, and a name bound at two types names both: ``` `v` is an Int in `.N(v)` but a String in `.S(v)`; a name an `or` pattern binds has one type in every alternative ```. A discard name (`_`, `_rest`) binds nothing and need not appear in every alternative. The name is one binding: it is read, renamed and reported unused as one, and go-to-definition shows each alternative's site.
- **Alternatives are tried left to right**, and the first that matches binds the names.
- **`or` nests** inside any pattern that holds subpatterns: a variant's payload (`Some(.Equilateral or .Isosceles)`), a tuple element (`#(0, x) or #(x, 0)`), a struct field (`#P{kind: .A or .B, n}`), a list or vector element (`[')' or ']' as c, ..rest]`, `#[0 or 1, ..]`) and a map value (`%{"k" => 1 or 2}`). Alternatives may be literals of any type a literal pattern takes (`1 or 2 or 3`, `"yes" or "y"`, `'a' or 'b'`).
- **`or` binds looser than every pattern construct but `as`.** `A or B as x` names the whole value whichever alternative matched (*Naming the Whole Match*). Parentheses group: `(.A(n) as a) or (.B(n) as a)` names each alternative's value, and `.A(n) as a or .B(n)` is the parse error ``` an `as` names the whole `or` pattern, so it goes after the last alternative; to name one alternative, parenthesize it: `(P as a) or ...` ```. A parenthesized `or` among alternatives is the same pattern as without the parentheses: `(A or B) or C` is `A or B or C`.
- **A guard applies to the whole arm.** `.A(n) or .B(n) when n > 0 -> ...` matches either alternative, then tests `n > 0`. When the guard is `False`, matching continues with the next arm, not with the next alternative.
- **Coverage is the union of the alternatives.** `.Red or .Green -> ...` and `.Blue -> ...` cover a three-variant `Color` with no `_` (*Exhaustiveness Checking*).
- **`or` is allowed wherever a pattern is.** In a position that may fail (a `case` arm, `if P = v`, a binding with `else`, `assert P = v`) any alternatives do. In a position that must match every value (a binding without `else`, a parameter or lambda-parameter pattern, a test's setup pattern, a `subtests` pattern) the alternatives must together match every value of its type, by the coverage rules: `fn value(.L(n) or .R(n): Side)` over a two-variant `Side` is fine, and `fn f(.Red or .Green: Color)` is ``refutable pattern in parameter; bind the parameter and use a `case` in the body``. A binding's `else` fallback stands in for one variant's payload (*Bindings with `else`*), so `Ok(.L(n) or .R(n)) = r else { Side.L(0) }` takes a fallback, and `.A(n) or .B(n) = e else { return 0 }`, which names two variants, must leave.
- A long `or` pattern breaks after each `or`, one alternative per line, the later alternatives indented one level.

### Struct Destructuring

Partial matching — only specify the fields you care about. Field punning works here too — `#User{name}` binds the field value to a variable called `name`:

```nomi
case user {
    #User{name: "Alice"} -> "found Alice"
    #User{name, age} when age > 18 -> "adult: ${name}"
    #User{age} when age > 18 -> "adult"
    _ -> "minor"
}

// mix punning and explicit binding
case user {
    #User{name, age: user_age} -> "${name} is ${user_age}"
}

// type name optional when not matching enum variants
case user {
    #{name, age} when age > 18 -> "adult: ${name}"
    _ -> "minor"
}
```

A written type name must name the value's struct, directly or through a `typealias`; any other name is a compile error at the pattern. A typed pattern does not match an anonymous struct: `#User{name} = #{name: "Ada"}` is an error, and `#{name} = #{name: "Ada"}` destructures it.

A struct pattern names each field at most once: `#User{age: 1, age: a}` is the error `field 'age' is given twice in this User pattern`, at the second `age`, and an anonymous one says `this struct pattern`. A punned repeat (`#User{age, age}`) also binds `age` twice, which is the pattern error below.

### Naming the Whole Match: `as`

`P as name` matches the pattern `P` and also binds the whole value `P` matched to `name`. It reads a value's shape and keeps the value itself:

```nomi
import std/io

enum Kind {
    Deposit
    Withdrawal
}

struct Transaction {
    kind: Kind
    starting_balance: Int
    ending_balance: Int
}

fn describe(result: Result<Transaction, String>) -> String {
    case result {
        Ok(#Transaction{kind: .Deposit} as t) ->
            "deposited ${t.ending_balance - t.starting_balance}"

        Ok(t) ->
            "withdrew ${t.starting_balance - t.ending_balance}"

        Err(msg) ->
            msg
    }
}

fn main() {
    io.print(describe(Ok(#Transaction{kind: .Deposit, starting_balance: 10, ending_balance: 25})))
    io.print(describe(Ok(#Transaction{kind: .Withdrawal, starting_balance: 25, ending_balance: 5})))
    io.print(describe(Err("no account")))
}
```

The rules:

- **`as` binds looser than every other pattern construct.** It names everything to its left within its pattern position: `Ok(x) as r` binds `r` to the whole `Result`, `Ok(#Transaction{kind: .Deposit} as t)` binds `t` to the payload, and `"/users/" + id as path` binds `path` to the whole string. Parentheses are only grouping: `(Ok(x)) as r` is `Ok(x) as r`.
- **The name is a binding name**, snake_case, not a pattern: `P as Big`, `P as _` and `P as (r)` are parse errors (`expected a binding name after `as` in a pattern`).
- **The name's type is the type of the position the pattern sits in**: the scrutinee's type at the top of a `case` arm, the payload's type inside a variant, the element's inside a list, vector or tuple, the value's inside a map entry, the field's inside a struct pattern. `P` does not narrow it: `Ok(_) as r` over a `Result<Int, String>` binds `r` as a `Result<Int, String>`.
- **`as` never changes what a pattern matches.** `P as name` is refutable exactly when `P` is, and a `case` is exhaustive with it exactly when it is without it.
- **An `as` must add a name.** `y as x` is the error ``` `y as x` binds the same value twice; use one name ```, `P as a as b` is ``` `as a as b` binds the same value twice; use one name ```, and `_ as x` is ``` `_ as x` matches every value, as `x` does; write `x` ```.
- **A pattern binds each name once**, the `as` name included: `Some(n) as n` is the error ``` `n` is bound twice in one pattern; give each binding its own name ```, at the second `n`.
- **The name follows the rules of any other pattern name**: it is in scope in the arm's guard and body (or after the binding), may shadow an earlier binding, must be read unless it starts with `_`, and an `_name` is a discard.

`as` is allowed wherever a pattern is: `case` arms (piped ones too), `if P as name = v`, bindings and bindings with `else` (`Some(n) as m = find() else { return 0 }`), `assert P as name = v`, parameter and lambda-parameter patterns (irrefutable ones, as always: `fn total(#(a, b) as pair: #(Int, Int))`, `fn #{x, y} as p -> ...`), a test's setup pattern, and nested inside variant payloads, tuples, lists, vectors, maps and struct fields.

A binding's `else` fallback stands in for the payload of the variant the pattern names (*Bindings with `else`*), so an `as` around the whole pattern rules a fallback out: in `Some(n) as m = find() else { 0 }` the fallback supplies `n` but there is no `Maybe` to bind to `m`, and the error is the one for a pattern with no single payload. An `as` inside the payload is fine: `Ok(#(w, h) as size) = r else { #(80, 24) }` binds `size` to the fallback when `r` is an `Err`.

With `or` between patterns (*Alternatives: `or`*), `as` binds looser than `or`: `#.Circle{r} or #.Square{r} as s` names the whole value either alternative matched.

### Type Narrowing

When pattern matching on an enum, the matched value's type is narrowed to the variant's type within that branch. A variant that has a type of its own is an `embeds` variant, so narrowing applies to embedded structs and embedded distinct types:

```nomi
fn describe(value: Shape) -> String {
    case value {
        #.Circle{radius} -> Circle.to_string(value) // value is Circle here
        #.Rectangle{width, height} -> "${width} x ${height}"
        .Point -> "Point"
    }
}
```

Inside the `Circle` branch, `value` is narrowed from `Shape` to `Circle`. You can pass it to any function expecting a `Circle`, or call `Circle`'s interface implementations on it.

- **What narrows is a name.** The matched value must be a parameter or a local binding written as a plain name (`case value`, not `case w.shape` or `case load()`). Every read of that name in the arm's guard and body is the narrowed value, lambdas created there included. A name the arm rebinds is a new binding and is not narrowed.
- **Where it narrows.** A `case` arm, and the first branch of `if Pattern = name`, whose pattern matches one `embeds` variant: `#.Circle{radius}`, `#Shape.Circle{radius: r}` and `.Id(n)` for an embedded distinct `Id` all narrow. An `as` around the pattern narrows the name as it would without the `as` (the `as` name itself keeps the type of its position, as *Naming the Whole Match* says). An `or` pattern, a struct-shaped or positional variant, the `else` branch and a binding with `else` and a piped `case` narrow nothing.

A narrowed value can still be passed to functions expecting the original enum type — a `Circle` is always a valid `Shape`.

### List Destructuring

Lists can be destructured in patterns using `..` to spread the tail after a comma-separated head list:

```nomi
case items {
    [] -> "empty"
    [only] -> "just ${only}"
    [first, ..rest] -> "first is ${first}, ${Iter.count(rest)} more"
}

// multiple fixed elements
case args {
    ["--port", value, ..rest] ->
        parse_rest(rest, port: String.to_int(value) |> Maybe.with_default(8080))

    ["--verbose", ..rest] ->
        parse_rest(rest, verbose: True)

    [_, ..rest] ->
        parse_rest(rest)

    [] ->
        config
}

// nested destructuring
case grid {
    [[first, .._], .._] -> first // first element of first row
    _ -> default
}
```

`.._` ignores the tail. `..rest` must appear last in the list pattern. Note: structs don't need a spread — partial matching is already the default (see Struct Destructuring above).

List patterns are refutable — a `_` catch-all or `[]` base case is required for exhaustiveness.

### Vector Destructuring

A vector pattern is written as a vector literal is, `#[...]`, and takes a vector apart as a list pattern takes a list, from either end. Elements before the `..` match from the front, elements after it from the end, and the `..` stands for everything between them:

```nomi
case v {
    #[] -> "empty"
    #[only] -> "just ${only}"
    #[first, .., last] -> "${first} to ${last}"
}

// a head and the rest
case v {
    #[x, ..rest] -> x + sum(rest)
    #[] -> 0
}

// heads, the middle and a tail
case v {
    #[a, ..mid, z] -> #(a, mid, z)
    _ -> #(0, #[], 0)
}

// the first two, ignoring the rest; exactly two
case v {
    #[a, b, ..] -> a + b
    _ -> 0
}

case v {
    #[a, b] -> a + b
    _ -> 0
}
```

- **One `..`, anywhere among the elements.** It is followed by a name, which binds the elements between the heads and the tails as a `Vector`, by `_`, or by nothing. A second `..` is the parse error ``a vector pattern takes one `..` ``, and anything else after it is ``the rest of a vector pattern is a name, `_` or nothing``.
- **Without a `..`, the pattern matches only vectors of its length.** `#[a, b]` matches a two-element vector, and `#[]` the empty one.
- **The rest shares the vector's storage.** Binding it takes no copy, so a recursive walk over `#[x, ..rest]` is linear.
- **Coverage is by length, as a list's is**: `#[]` and `#[x, ..rest]` cover every vector, and `#[]` with `#[x]` is ``non-exhaustive case on Vector<Int>: missing #[_, _, ..]``. `#[..all]` covers every vector.
- **Elements are patterns** and nest as any pattern does: `#[#(n, s), ..]`, `#[Some(x), ..]`, `#[] or #[_]`, `#[4, ..] as v`. A vector pattern stands in a `case` arm, `if Pattern = expr`, a binding with `else` and `assert Pattern = expr`. It is refutable, so as a parameter pattern it is the error `refutable pattern in parameter`, as a list pattern is.
- **A type built on a vector names itself after the `#`**, as its literal does: `#Path[a, .., z]` takes a `type Path Vector<Int>` apart, and `#.Items[x, ..]` a vector-payload variant.
- A vector pattern over a value that is not a vector is the error `vector pattern requires a vector, got Set<String>`.

### Map Destructuring

Maps can be destructured in patterns using the same `%{key => pattern}` syntax as map construction. A key is an ordinary expression evaluated in the branch scope, exactly like a map-construction key — so **any keyable value matches** (variants, tuples, lists, structs, distinct types, …), not just scalar literals, and computed keys are allowed (`%{x => v}` matches the entry whose key equals the current value of `x`). A key sees the names in scope before the pattern, never a name the pattern itself binds: after `a = "host"`, `%{"mode" => a, a => b}` looks up `"host"` for `b` and binds the mode's value to `a`. Matching is partial — extra keys in the map are ignored (consistent with struct patterns):

```nomi
config = %{"host" => "localhost", "port" => 8080, "debug" => True}

case config {
    %{"debug" => True, "port" => p} -> "debug on port ${p}"
    %{"port" => p} -> "port ${p}"
    _ -> "no port"
}

// literal value matching
case headers {
    %{"content-type" => "application/json"} -> parse_json(body)
    %{"content-type" => ct} -> parse_other(ct, body)
    _ -> Err("no content type")
}

// integer keys
case grid {
    %{0 => first_row} -> first_row
    _ -> []
}

// wildcard values — check key exists without binding
case env {
    %{"DATABASE_URL" => _} -> "db configured"
    _ -> "no db"
}

// non-literal keys: any keyable value, including computed ones
case scores {
    %{#(1, 2) => v} -> v // tuple key
    %{Color.Red => v} -> v // variant key
    %{target => v} -> v // computed — matches the entry equal to `target`
    _ -> 0
}
```

A map pattern is refutable. Whether a key is present is data the program does
not control, so a missing key is a pattern that does not match, never a trap
(§17 *Traps, `Maybe` and `Result`). In a `case` arm the next arm is tried, and
`if Pattern = expr` takes its `else` branch. A map pattern never matches every
map, so a `case` over a map needs a `_` arm (`` non-exhaustive case: add a `_` arm ``).

In a binding, a map pattern takes an `else` as `Some(x) = maybe` does (§10
*Bindings with `else`*). A map pattern has no single payload a fallback could
stand in for, so every path through the `else` leaves:

```nomi
fn address(config: Map<String, String>) -> String {
    %{"host" => host, "port" => port} = config else {
        return "no address"
    }

    "${host}:${port}"
}

fn port_of(config: Map<String, Int>) -> Int {
    %{"port" => port} = config else {
        %{"fallback" => fallback} -> return fallback
        _ -> return 80
    }

    port
}
```

Without `else`, `%{"port" => port} = config` is the error `` this pattern can fail
to match a value of type Map<String, Int>; add `else { ... }` to handle a value
it does not match, or match it with `case` or `if Pattern = expr` ``, with the
help `` a map pattern does not match a map that lacks one of its keys. Handle a
missing key with `else { ... }`, or read one key with `Map.get`, which returns
a `Maybe` ``. In a test, `assert %{"port" => port} = config` fails the assertion
when a key is missing and binds `port` when none is.

A map pattern that names the same constant key twice (`%{"k" => a, "k" => b}`) is a compile error at the second key, `` key `"k"` is given twice in this map pattern; match its value once ``, by the rule for map literals (§19, Map, *Repeated keys*). Keys equal only at run time (`%{target => a, other => b}`) both look up the one entry, and both patterns must match its value.

### Set Destructuring

A set pattern is written as a set literal is, `%[...]`, and tests membership as a map pattern tests keys. Each element is an expression, evaluated in the scope before the pattern, and the set must hold its value. A `..` at the end lets the set hold other elements too, and a name after it binds them:

```nomi
fn role(s: Set<String>) -> String {
    case s {
        %["admin", ..rest] -> "admin and ${Set.size(rest)} more"
        %["user", ..] -> "user"
        %[] -> "nobody"
        _ -> "other"
    }
}

who = "user"

case roles {
    %[who, ..others] -> others // the current value of `who`
    _ -> %[]
}

// without `..`: exactly these elements
case s {
    %[1, 2] -> "both"
    _ -> "other"
}
```

- **An element is a value to look up, never a place to bind.** `%[_, ..]` is the error ``an element of a set pattern is a value to look up, not a place to bind: `_` names no value; `..` matches the other elements``, and an element naming nothing in scope, `%[role, ..]`, is ``an element of a set pattern is a value to look up, not a place to bind: `role` is not in scope, and `..role` binds the other elements``.
- **The `..` comes last**: `%[..rest, 1]` is the parse error ``set pattern `..` must be the last element``.
- **A constant element is named once.** `%["a", "a", ..]` is ``element `"a"` is given twice in this set pattern; test it once``. Each element is checked against the set's element type (`set pattern element type mismatch: expected String, got Int`), and a set pattern over a value that is not a set is `set pattern requires a set, got Vector<Int>`.
- **A set pattern never covers every set.** A `case` over a set needs a `_` arm (`` non-exhaustive case: add a `_` arm ``). A binding takes an `else`, and since a set pattern has no single payload, every path through it leaves. Without one, `%["admin", ..rest] = s` is the error `` this pattern can fail to match a value of type Set<String> ``, with the help `` a set pattern does not match a set that lacks one of its elements. Handle that with `else { ... }`, or test one element with `Set.contains?` ``.
- **A type built on a set names itself after the `%`**: `%Tags["a", ..others]` takes a `type Tags Set<String>` apart.

### Distinct Type Destructuring

Distinct types over tuples, maps, vectors and sets mirror their construction syntax: the named literal pattern (`#Pair(a, b)`, `%Kvs{...}`, `#Path[...]`, `%Tags[...]`) and the call-form pattern (`Pair(#(a, b))`, `Kvs(%{...})`) are both accepted. Pick whichever matches how the value was built:

```nomi
type Pair #(Int, String)
type Kvs Map<String, Int>

p = #Pair(1, "hello")

kv = %Kvs{"a" => 1, "b" => 2}

// tuple-distinct: the named tuple pattern or the call form
case p {
    #Pair(a, b) -> "${a}, ${b}" // equivalent to Pair(#(a, b))
    Pair(#(a, b)) -> "${a}, ${b}" // call-form — same effect
}

// map-distinct: the named map pattern or the call form
case kv {
    %Kvs{"a" => v} -> v // named map pattern
    Kvs(%{"b" => v}) -> v // call-form
}
```

For a tuple-distinct, `Pair(a)` binds the entire inner tuple to `a`: the call form takes one value, the tuple. The flat pattern `Pair(a, b)` is the error ``Pair takes one value, its tuple #(Int, String); a pattern takes it apart with `#Pair(...)` ``. Use `#Pair(a, b)` to take the tuple apart.

### String Prefix Patterns

Strings can be destructured in patterns using `+`, the same operator used for string concatenation. Just as `#User{name: n}` in a pattern destructures what `#User{name: "Alice"}` constructs, `"prefix" + rest` in a pattern destructures what `"prefix" + rest` would concatenate. Construction and destructuring use the same syntax.

Only prefix matching is supported — the left side must be a string literal without interpolation, and the right side a name. `+` prefix patterns are string-only — lists use `..` for destructuring (see List Destructuring above). A string prefix pattern is always refutable (the string may not match the prefix), so it stands where a refutable pattern may: a `case` arm, a pattern `if`, a binding with `else`, or nested inside another pattern (`Some("/users/" + id)`). A plain binding (`"/" + rest = path`) is an error.

The match compares bytes: the pattern matches when the string begins with the literal's bytes, and the name binds the rest, so the string is exactly `literal + rest`. The rest may be empty, and it may begin with a combining mark that follows the literal's last codepoint (`"e" + rest` against `"e\u{301}"` binds `"\u{301}"`).

```nomi
case path {
    "/reports/" + id -> handle_report(id)
    "/users/" + id -> handle_user(id)
    "/health" -> handle_health()
    _ -> not_found()
}
```

A `_` catch-all is always required since the compiler cannot prove string prefix patterns are exhaustive.

### Ad Hoc Conditionals

For ad hoc conditionals (without a value), each arm's condition is a Bool expression (`x + 1 -> ...` is the error `case condition must be Bool, got Int`), and a `_` catch-all is always required since the compiler cannot prove boolean conditions are exhaustive:

```nomi
// error: non-exhaustive case: add a `_` arm
case {
    x > 0 -> "positive"
    x == 0 -> "zero"
}

// correct
case {
    x > 0 -> "positive"
    x == 0 -> "zero"
    _ -> "negative"
}
```

### Bindings with `else`

A binding whose pattern can fail may be followed by `else` and a braced block that runs when the value does not match. The braces are always required.

```nomi
// A block that leaves
Some(email) = user.email else {
    return Err("user ${id} has no email")
}

// Case arms over the value that did not match, each leaving
Ok(user) = load(id) else {
    Err(.NotFound) -> return Err("no user ${id}")
    Err(e) -> return Err("loading user ${id}: ${e}")
}

// A fallback value instead of leaving
Some(email) = user.email else { "none" }

Ok(#(width, height)) = parse_size(text) else {
    #(80, 24)
}

// Arms mixing a fallback with leaving
Ok(port) = parse_port(text) else {
    Err(.Empty) -> 8080
    Err(e) -> return Err("bad port: ${e}")
}
```

The braces hold case arms when their first item is `pattern ->` (or `pattern when`), as a `case`'s do; otherwise they are an ordinary block.

- The right-hand side is evaluated once. If the pattern matches, its names are bound and are in scope for the rest of the enclosing block, like any binding's. They are not in scope inside the `else`, which runs because the pattern did not match.
- If it does not match, the `else` runs. Arms match the value that failed, which has the right-hand side's type. The binding's pattern and the arms together must be exhaustive, by `case`'s rules (§*Exhaustiveness Checking*), including its `_` requirements; the error is `non-exhaustive `else` on Result: missing Err`.
- Every path through the `else` either **leaves** — `return`, `break` or `continue` (inside an iteration callback, §12), or a branch every path of which leaves — or **produces a fallback**.
- A fallback is allowed only when the pattern names one variant with a payload, `Variant(inner)` or `#.Variant{fields}`, whose own pattern always matches. The fallback's type is the payload's (for a struct-shaped variant, the anonymous struct of its fields), and the inner pattern binds from it exactly as it would from a match: `Some(email) = m else { "none" }` binds `email` to `"none"`, and `Ok(#(w, h)) = r else { #(80, 24) }` binds `w` to 80 and `h` to 24. A fallback of another type is the error `the fallback stands in for Some's payload of type String, got Int`.
- A pattern with no single payload to stand in for — a list pattern such as `[first, ..rest]`, a vector pattern such as `#[first, ..rest]`, a map pattern such as `%{"port" => p}`, a set pattern such as `%["admin", ..rest]`, a nested refutable pattern such as `Ok(Some(x))`, a literal, a tuple with a refutable part — requires every path to leave. A path that produces a value is the error `this pattern has no single payload a fallback could stand in for; every path through `else` must return, break or continue`.
- A pattern that always matches takes no `else`: `#(x, y) = pair else { ... }` is the error `this pattern always matches; remove the else`.
- A single-variant enum's pattern always matches, as it does as a parameter (§5 *Parameter Destructuring*): for `enum Only { One Int }`, `Only.One(p) = only` binds `p` to the Int with no `else`, and over a struct-shaped variant `Rec.Fields(r) = rec` binds `r` to the anonymous struct of its fields.
- A pattern that can fail requires one. `Some(n) = maybe` alone is the error `this pattern can fail to match a value of type Maybe<Int>; add `else { ... }` to handle a value it does not match, or match it with `case` or `if Pattern = expr``. On `Ok(x)` over a `Result` or `Some(x)` over a `Maybe` its help points at `try`, which unwraps the value and returns early on `Err` or `None`: write `x = try ...`, or, when the value is a pipe, end it with `|> try`.
- `try` inside the `else` behaves as it does anywhere: it exits the enclosing function or lambda. `return` inside a lambda's `else` exits the lambda.

```nomi
import std/io

enum Check {
    Valid {addr: String, score: Int}
    Invalid String
}

fn label(check: Check) -> String {
    #.Valid{addr, score} = check else {
        .Invalid(_) -> #{addr: "unknown", score: 0}
    }

    "${addr}: ${score}"
}

fn first_word(words: List<String>) -> String {
    [first, .._rest] = words else { return "(none)" }
    first
}

fn main() {
    io.print(label(#Check.Valid{addr: "a@b", score: 3})) // a@b: 3
    io.print(label(Check.Invalid("bad"))) // unknown: 0
    io.print(first_word(["hello", "world"])) // hello
    io.print(first_word([])) // (none)

    Iter.each(["1", "x", "3"], fn text -> {
        Some(n) = String.to_int(text) else { continue }
        io.print(n * 10) // 10, then 30
    })
}
```

Which to reach for: `try` passes an error up unchanged; a binding `else` handles or replaces one step's failure in place, or supplies a fallback; `case` when both outcomes continue; `if Pattern = expr` when only the success branch needs the names.

## 11. If/Else

Expression-based conditionals. No parentheses around the condition. `else` is required when the body returns a non-`Unit` value. When the body returns `Unit`, `else` can be omitted (implicitly evaluates to `Unit`).

```nomi
label = if x > 0 { "positive" } else { "negative" }
```

An `if` condition can also be a one-off refutable pattern match:

```nomi
label = if Some(name) = maybe_name {
    name
} else {
    "unknown"
}
```

The pattern bindings are visible only in the then branch. If the pattern does
not match, the else branch runs; without an else branch the expression evaluates
to `Unit`, like any other `if` without `else`.

The pattern must be able to fail. One that matches every value of the value's
type (a name, `_`, a tuple of names, a distinct type or struct destructure, a
single-variant enum) tests nothing, and is the error `this pattern always
matches a value of type Int, so the `if` has nothing to test; bind it on its
own line with `pattern = value``. `if x = 0` binds a new `x` to 0; when an `x`
of the value's type is already in scope, the help says to write `if x == 0` to
compare it.

`else if` for a few branches:

```nomi
if x > 0 {
    "positive"
} else if x == 0 {
    "zero"
} else {
    "negative"
}
```

The parser accepts `else` and `else if` rungs on a fresh line after the previous block's closing `}` (`}\nelse if ... {`) — equivalent to keeping `else` on the same line as `}`. The formatter (`nomi fmt`) normalizes every chain to the same-line `} else if` form and always splits each branch body onto its own line, so long chains read uniformly regardless of how they were written.

When the body returns `Unit`, `else` is optional:

```nomi
if should_log { io.print(msg) }
```

`return` can be used for early exit from within `if` branches:

```nomi
fn process(items: List<Item>) -> Result<Summary, String> {
    validated = try validate(items)

    if Iter.empty?(validated) {
        return Ok(EmptySummary)
    }

    // continue processing without nesting into else
    build_summary(validated)
}
```

For many branches, `case` is cleaner:

```nomi
case {
    x > 10 -> "big"
    x > 0 -> "positive"
    x == 0 -> "zero"
    _ -> "negative"
}
```

## 12. Iteration

Nomi has two iteration mechanisms: **stdlib higher-order functions** for collection processing, and **bare `loop`** for stateful or stateless infinite loops. Both support `break` for early exit and `continue` to skip to the next iteration.

### Collection Iteration

**Every generic iteration op is an `Iter` owner function** over any `Iter<T>`: `Iter.map`, `Iter.filter`, `Iter.reduce`, `Iter.each`, `Iter.find`, `Iter.count`, `Iter.sort`, `Iter.reverse`, and the rest. The adapters (`map`/`filter`/`take`/`drop`/`flat_map`/…) are **lazy** — they return new iterators that fuse, building no intermediate collection. The terminals (`reduce`/`each`/`find`/`any?`/`all?`/`empty?`/`count`/`sort`/…) and materializers (`to_list`/`to_vector`/`to_set`/`to_map`/`reverse`) **consume** the iterator. There are no eager collection-adapter functions: to get a container back from a chain, append an explicit materialize step. An `Iter<String>` is joined into one `String` with `String.join`.

```nomi
// transform (lazy — materialize with Iter.to_list)
names = Iter.map(users, .name) |> Iter.to_list()

// accumulate (terminal)
total = Iter.reduce(items, fn acc = 0, item -> acc + item.value)

// side effects (terminal)
Iter.each(items, fn item -> io.print(item.name))

// iterating a map (each step is a #(key, value) tuple)
Iter.each(ages, fn #(k, v) -> io.print("${k}: ${v}"))
```

The one rule: **generic over any iterable → `Iter.X`; specific to a container's structure → that container's owner.** A `List` back from a `List` is always the explicit `Iter.map(xs, f) |> Iter.to_list()` — the lazy adapter plus a visible materialize step is what makes the cost (one allocation) explicit at the call site. Container-specific ops that read or rebuild structure live on the relevant owner: `Map.map_values`, `Set.union`, `List.concat`, `Vector.get`, `String.split`, `Map.size`/`Set.size`, and native `String.reverse`.

**A collection is an `Iter`; an `Iter` is not a collection.** A `List<T>`, `Set<T>`, `Vector<T>` or `Map<K, V>` stands wherever an `Iter<T>` is expected. An `Iter<T>` where a collection is expected (an annotation, an argument, a field, a list element, a result) is a type error at the expression, `type mismatch: expected List<Student>, got Iter<Student>`, whose help names the materializer to append: `|> Iter.to_list()`, `Iter.to_set`, `Iter.to_vector` or `Iter.to_map`. The same holds for every interface: a value of an interface type never stands where one of its implementers is expected.

### `break` and `continue` in Callbacks

`break` and `continue` work inside iteration callbacks — the callbacks passed to the closed, analyzer-enforced set of iteration functions: `Iter.map`, `Iter.scan`, `Iter.filter`, `Iter.take_while`, `Iter.drop_while`, `Iter.flat_map`, `Iter.iterate`, `Iter.reduce`, `Iter.map_reduce`, `Iter.each`, `Iter.find`, `Iter.any?`, `Iter.all?`, `Map.map_values` and `Map.map_keys`, plus `Iter.loop`. `break` or `continue` anywhere outside an iteration callback is a compiler error — there's no iteration context.

The keywords are the entire surface of the iteration control protocol. A callback's bare last expression means "here's my result for this element," `continue` means "skip this element," `break` means "stop iterating" (`break value` means "stop, producing this value"). These lower to a compiler/runtime-internal signal that the iteration machinery interprets — the signal has no surface spelling: it is not a Nomi type, and there is nothing to import, construct, or pattern-match.

The `break` / `continue` / `return` keywords are valid as `case`-arm bodies, not just block statements — they unwind to the enclosing loop / lambda exactly as they do elsewhere, so a callback can dispatch with `case` and break or continue from an arm:

```nomi
Iter.loop(fn sum = 0 -> {
    case Receiver.receive(ch.receiver) {
        Some(v) -> sum + v // bare expr: next state, loop again
        None -> break sum // stop, loop value = sum
    }
})
```

**`continue`** — skip this element, never takes a value:

```nomi
// Skip inactive items
Iter.each(items, fn item -> {
    if !item.active { continue }
    process(item)
})

// Skip invalid items in reduce — accumulator unchanged
total = Iter.reduce(items, fn total = 0, item -> {
    if item.invalid { continue }
    total + item.value
})

// Skip in map — element excluded from output
names =
    Iter.map(users, fn u -> {
        if u.inactive { continue }
        u.name
    })
    |> Iter.to_list()
```

**`break`** — stop iteration, return what's accumulated so far. `break value` is `return value` *then* stop — `value` plays the same per-operation role it would as a normal callback result (the accumulator for `reduce`, the emitted element for `map`, the keep/drop decision for `filter`):

```nomi
// Buy items until budget is exceeded — don't include the one that busted it
#(cart, spent) = Iter.reduce(items, fn acc = #([], 0.0), item -> {
    #(cart, spent) = acc
    new_spent = spent + item.price
    if new_spent > budget { break }
    #([item, ..cart], new_spent)
})

// Map with early exit — include the last one, then stop
names =
    Iter.map(users, fn u -> {
        if u.is_last { break u.name }
        u.name
    })
    |> Iter.to_list()

// Stop without including current element
names =
    Iter.map(users, fn u -> {
        if u.inactive { break }
        u.name
    })
    |> Iter.to_list()
```

The other callbacks read the value the same way. In `find`, `any?` and `all?` it is the predicate's answer for this element: `break True` in `find` answers `Some` of this element, and a bare `break` answers what has accumulated, which is `None` for `find`, `False` for `any?` and `True` for `all?`. In `drop_while` it is the drop decision: `break False` emits this element and ends the sequence, and `break True` or a bare `break` ends it with nothing more. In `flat_map` it is this element's expansion, emitted before the sequence ends. In `Map.map_values` and `Map.map_keys` it is this entry's new value or key; `continue` leaves the entry out, and a bare `break` keeps the entries made so far. In `scan` it is this element's new state, yielded before the sequence ends; `continue` yields nothing and keeps the state, and a bare `break` ends the sequence without yielding. In `map_reduce` it is this element's `#(output, next_state)`, kept before the call answers; `continue` adds no output and keeps the state, and a bare `break` answers the outputs and state so far.

**`loop`** follows the same rules:

```nomi
// continue — retry
Iter.loop(fn -> {
    case acquire_lock(handle) {
        Ok(lock) -> {
            use_lock(lock)
            break
        }

        Err(_) -> {
            timer.sleep(Duration.milliseconds(100))
            continue
        }
    }
})

// break — stop with the current state; a stateless loop's state is Unit
Iter.loop(fn -> {
    case Receiver.receive(inbox) {
        Some(Message.Shutdown) -> {
            break
        }

        Some(message) ->
            process(message)

        None -> {
            break
        }
    }
})

// break value — stop and return a value; the state carries the attempt
// count and the last result, since `break` answers the state's type
#(_, result) = Iter.loop(fn state: #(Int, Result<Data, String>) = #(0, Err("not tried")) -> {
    #(attempts, _) = state

    case fetch(url) {
        Ok(data) -> break #(attempts, Ok(data))
        Err(_) when attempts >= 3 -> break #(attempts, Err("gave up"))
        Err(e) -> #(attempts + 1, Err(e))
    }
})
```

### Named Functions as Callbacks

Named functions with plain return types pass directly as callbacks — a `(T) -> R` function is exactly what an iteration function expects:

```nomi
fn double(x: Int) -> Int {
    x * 2
}

// These are equivalent:
doubled = Iter.map(numbers, fn x -> x * 2) |> Iter.to_list()

doubled = Iter.map(numbers, double) |> Iter.to_list()
```

A named function may use `break` and `continue` in its body; the return type stays the plain value type:

```nomi
fn sum_valid(total: Int, value: Int) -> Int {
    if value < 0 { continue }
    if total > 1000 { break total }
    total + value
}

result = Iter.reduce(values, sum_valid, from: 0)
```

Nomi has no non-local control flow. `return`, `break` and `continue` each leave only the function they are written in. `return value` hands a value back to the caller; `continue`, `break` and `break value` hand a signal back to the iteration function that called the callback. There are no exceptions.

Using the keywords makes the function **iter-sensitive**, and so it is used in one way only: passed as an iteration callback itself, positional, by name or piped (`Iter.map(xs, keep)`, `Iter.find(xs, f: is_big)`, `xs |> Iter.map(keep)`). A fold or a loop takes its starting value by name, `from:` (§19 *Iter Interface*, *Starting values*, and *`loop`* below): `Iter.reduce(values, sum_valid, from: 0)`, `Iter.loop(step, from: 0)`. The iteration function calls it, so its signal reaches the iteration function.

Any call of it is a compiler error at the call, since the signal would cross the caller's boundary too: as the whole body of a callback lambda (`fn x -> keep(x)`) or a capture (`&keep(&1)`), after a binding, in an `if` or `case` arm, as an argument or an operand, from another function, in a `defer` or a `concurrent` block, or outside every callback. Naming the function as a value (`f = keep`, `apply(keep, 1)`, `[keep]`) is an error too. The error suggests passing the function, in a call its parameters fit: the iteration whose callback holds the call when the function fits that callback, otherwise `Iter.map` for one parameter, or `Iter.reduce` with `from:` for two whose first parameter's type is its result's:

```text
`keep` uses `continue`, so it cannot be called: pass it as an iteration callback itself, as in `Iter.map(xs, keep)`
`add_until_zero` uses `break`, so it cannot be called: pass it as an iteration callback itself, as in `Iter.reduce(xs, add_until_zero, from: <start>)`
```

Inside a callback the help is "to decide in this callback instead, return a `Maybe` or an enum from `keep` and write `continue` here". To decide inside a callback, have the helper answer a `Maybe` or an enum, and write the `continue` in the callback:

```nomi
import std/io

fn score(n: Int) -> Maybe<Int> {
    if n < 0 { None } else { Some(n * 2) }
}

fn main() {
    doubled =
        Iter.map([3, -1, 4], fn n -> {
            Some(s) = score(n) else { continue }
            io.print("kept ${s}")
            s
        })
        |> Iter.to_list()

    io.inspect(doubled) // [6, 8]
}
```

A `break` or `continue` written in a lambda or capture that is not itself an iteration callback, in a nested `fn`, or directly in a `concurrent` block has no iteration to act on, and is a compiler error too. An iter-sensitive function may be `pub`: which of `break` and `continue` a function uses is part of how other files may use it, and each file's uses are checked as the declaring file's are.

Summary:

- `continue` — skip this element. Never takes a value: `continue <expr>` is a compile error. In `Iter.loop`, `continue` goes round again with the state unchanged; to go round with a new state, make it the callback's result.
- `break` — stop, return what's accumulated so far. In `Iter.loop` that is the current state.
- `break value` — `return value` then stop: `value` is this element's result for the operation (the accumulator for `reduce`, the emitted element for `map`, the keep/drop decision for `filter` — `break True` keeps the element, `break False` drops it), then iteration stops.
- `break` and `continue` control the iteration. `return` exits the nearest function boundary (the callback lambda).
- Same semantics everywhere — no special cases for specific functions: `break value` is uniformly `return value` then stop, and a callback result already means whatever that operation makes of it (output element, accumulator, decision) on the normal path too.

### `loop`

`loop` lives on `Iter` in `std/iter` — the canonical spelling is `Iter.loop(...)`, and `import std/iter.Iter.{loop}` restores the bare form. It sits beside the iterator algorithms because it speaks the same `break`/`continue` callback protocol, even though it has no collection input and threads state instead. The callback takes the current state and returns the next state. Lambda default parameters provide the initial state:

```nomi
countdown = Iter.loop(fn n = 5 -> {
    if n == 0 { break n }
    n - 1
})
```

The callback's type is `(S) -> S` — one parameter for the current state, returning the next state. The next state is the callback's result, never an operand of `continue`: bare `continue` starts the next round with the state unchanged, and `continue #(i + 1, s)` is rejected rather than read as a new state. A stateless loop's `continue` is the next round of a read loop; a loop with a state and no effect whose `continue` is reached runs forever, as any loop that reaches no `break` does. The default value (`n = 5`) provides the initial state and pins the type `S`. The initial state may be given by name instead, `from:`, with any callback: `Iter.loop(step, from: 5)` over a named function, or `Iter.loop(fn n -> …, from: 5)`. This is the same rule as `Iter.reduce`'s: exactly one of `from:` and the parameter's default gives the initial state. A callback with neither is a compile error at the callback, "`Iter.loop` needs a starting state", whose help names both spellings: a lambda whose parameter has no default (`Iter.loop(fn n: Int -> …)`) and a function value (`Iter.loop(step)`). Both is "`Iter.loop` has two starting states: `from:` and the default of the callback's parameter", at `from:`, and `from:` beside a stateless callback is "a stateless `Iter.loop` takes no starting state". A starting state written positionally (`Iter.loop(step, 5)`) is "`Iter.loop` takes its starting state by name".

`break value` exits the loop and produces the final value. The state type and the break type are the same type `S` — `break "hello"` inside `Iter.loop(fn n = 5 -> …)` is a compile error.

Bare `break` stops the loop with the current state, the value the callback's parameter holds on this iteration: in `Iter.loop(fn n = 5 -> …)` it is `break n`. This is the iter-callback rule, stop with what has accumulated so far, and in `loop` the state is what has accumulated. A binding in the body that shadows the parameter does not change it: the loop answers the state the iteration started with.

A stateless callback (`fn ->`) has state `Unit`. The loop answers `Unit`, every `break` in it is a bare `break` (or `break Unit`), and the body's result is `Unit`. `break 0` or a body ending in `n + 1` inside `Iter.loop(fn -> …)` is a compile error; to end with a value, give the loop a state (`Iter.loop(fn n = 0 -> …)` and `break n`).

```nomi
// Echo lines until "quit" or the end of the input.
Iter.loop(fn -> {
    Ok(line) = io.read_line() else { break }
    if line == "quit" { break }
    io.print(line)
})
```

```nomi
// Read an opening balance. The end of the input keeps the account as it is.
account = Iter.loop(fn account = #Account{} -> {
    io.print("What is the starting balance?")
    Ok(input) = io.read_line() else { break }

    case parse_amount(input) {
        Ok(amount) ->
            break #{..account, balance: amount}

        Err(msg) -> {
            io.print(msg)
            account
        }
    }
})
```

```nomi
// retry with attempts counter: the state is the attempts made and the
// last result, and the loop answers both
#(_, result) = Iter.loop(fn state: #(Int, Result<Data, String>) = #(0, Err("not tried")) -> {
    #(attempts, _) = state

    case fetch(url) {
        Ok(data) -> break #(attempts, Ok(data))
        Err(_) when attempts >= 3 -> break #(attempts, Err("gave up"))
        Err(e) -> #(attempts + 1, Err(e))
    }
})

// reading input
lines = Iter.loop(fn lines = [] -> {
    case io.read_line() {
        Ok("") -> break lines
        Ok(line) -> [line, ..lines]
        Err(_) -> break lines
    }
})
```

Multiple state values use a tuple parameter with destructuring in the body:

```nomi
import std/io

// Newton's method — converge on square root
fn square_root(n: Float) -> Float {
    #(_, guess) = Iter.loop(fn state = #(n / 2.0, 0.0) -> {
        #(guess, prev) = state
        diff = guess - prev

        if diff > -0.0001 and diff < 0.0001 {
            break state
        }

        next = (guess + n / guess) / 2.0
        #(next, guess)
    })

    guess
}

fn main() {
    io.inspect(square_root(2.0)) // 1.4142156862745097
}
```

`return` inside the body exits the callback lambda (the nearest function boundary), not the enclosing function — consistent with Nomi's no-non-local-returns rule.

A loop whose callback holds no `break` of its own never finishes. A `break` inside a lambda or a nested `fn` in the body belongs to that callback, and `return value` and `continue` only go round again, so nothing ends the loop but a trap, the cancellation of its task or the end of the process. That is how a worker loop is written (§20 *Periodic task*). The loop still has its state type `S`, a value it never produces. A statement after it is unreachable code (`unreachable code after a loop that never breaks`). A binding of it, or an operand holding it, is legal, and nothing after it runs.

### Iteration is built from three primitives

Iteration in Nomi composes from a closed set of primitives. There is no user-defined-iteration-function escape hatch — and we haven't found one we'd need:

- **The `Iter` interface** — implement `each_while` (push each element to a `yield` callback until it returns `False`) on a type, and every `Iter` owner function (`Iter.map`, `Iter.reduce`, `Iter.take_while`, …) applies to values of that type.
- **`Iter.loop`** — general state-threading; covers anything you'd reach for an imperative loop. Its starting state is the callback lambda's parameter default or `from:` (`Iter.loop(step, from: 0)`).
- **Composing existing iter ops via pipes** — `Iter.filter`, `Iter.take_while`, `Iter.reduce`, and the rest chain freely.

Every generic iteration op is an `Iter` owner function; container-specific ops (`Map.map_values`, `Set.union`, …) build on them internally. The set of call-sites that accept `break`/`continue` is the closed list of iter-family functions plus `Iter.loop`, and the analyzer enforces that closure. The lowering target for `break`/`continue`/`return` is the compiler/runtime-internal signal described in "`break` and `continue` in Callbacks" above — not a Nomi type, with no surface spelling.

### Compared to Recursion

Recursion is stack-safe in Nomi (see Tail-call optimization below), so the choice between `loop` and explicit recursion is purely ergonomic. `loop` replaces patterns that would otherwise need helper functions with accumulators:

**Parsing args — recursive (needs helper):**

```nomi
fn parse_args(args: List<String>) -> Config {
    parse_args_acc(args, config.default())
}

fn parse_args_acc(args: List<String>, config: Config) -> Config {
    // private helper
    case args {
        [] ->
            config

        ["--port", value, ..rest] ->
            parse_args_acc(
                rest,
                Struct.update(config, #{port: String.to_int(value) |> Maybe.with_default(8080)}),
            )

        ["--verbose", ..rest] ->
            parse_args_acc(rest, Struct.update(config, #{verbose: True}))

        [_, ..rest] ->
            parse_args_acc(rest, config)
    }
}
```

**Parsing args — loop:**

```nomi
fn parse_args(args: List<String>) -> Config {
    #(_, config) = Iter.loop(fn state = #(args, config.default()) -> {
        #(args, config) = state

        case args {
            [] ->
                break state

            ["--port", value, ..rest] ->
                #(
                    rest,
                    Struct.update(
                        config,
                        #{port: String.to_int(value) |> Maybe.with_default(8080)},
                    ),
                )

            ["--verbose", ..rest] ->
                #(rest, Struct.update(config, #{verbose: True}))

            [_, ..rest] ->
                #(rest, config)
        }
    })

    config
}
```

### Summary

- **Stdlib functions** (`Iter.map`, `Iter.reduce`, `Iter.each`, `Iter.find`, etc.) — collection iteration. `break` and `continue` work in callbacks.
- **`Iter.loop(fn state = init -> ...)`** — stateful infinite loops (I/O, retries, state machines, channel consumption). `break` and `continue` work here too.
- **Direct recursion** — naturally recursive structures (trees, divide-and-conquer, mutual recursion) where the problem itself branches.
- `continue` — skip this element. Never takes a value.
- `break` — stop, return what's accumulated so far (the current state in `Iter.loop`). `break value` includes one final value before stopping.
- Same semantics everywhere — no special cases for specific functions.
- `break` and `continue` control the iteration, `return` exits the nearest function boundary (the callback lambda).
- Tail calls run in constant stack space (see Tail-call optimization below). `loop` and explicit tail recursion are equivalent in stack behavior — the choice between them is purely ergonomic.

### Tail-call optimization

Tail calls are guaranteed to run in constant stack space. Any call to a Nomi-defined function in tail position trampolines through a single activation frame instead of recursing through the host's call stack, so a function that recurses on itself in tail position iterates indefinitely without overflowing. This is a language-level guarantee, not a best-effort optimization — there is no annotation, no `recur` keyword, and no diagnostic telling you whether a particular call qualifies. The grammar below is the contract.

A call is in **tail position** when its value becomes the enclosing function's return value with no further work in between. Tail position propagates into:

- The last expression of a function or lambda body (and the last statement of any block; preceding statements are not tail). A nested `fn` is a function: its body is its own tail context, so a nested `fn` that calls itself in tail position loops in constant stack like a top-level one.
- Both arms of `if` / `else`, when the `if` itself is in tail position.
- Every arm of `case`, when the `case` itself is in tail position. Guards are not tail.
- The last stage of a `|>` pipe.
- The expression in `return expr`.
- The right-hand side of `and` / `or` (the short-circuit second operand).

Tail position does **not** cross:

- **Lambda body boundaries.** A lambda has its own tail context. In `fn f(xs: List<Int>) -> Int { Iter.reduce(xs, fn acc = 0, x -> f(rest(x))) }`, the call to `Iter.reduce` is `f`'s tail position and gets trampolined, but the call to `f` *inside the lambda* is the lambda's tail, not `f`'s. `Iter.reduce` invokes the lambda fresh on each iteration, so there is no growing chain that crossing the boundary could collapse. This rule is universal across TCO'd languages (Scheme, Clojure, Erlang, Haskell, OCaml). The practical guidance: when you want to "iterate by recursing through `Iter.each`," reach for `loop` instead.
- Default-value expressions. They run before the body, not as part of it.
- The operand of `try`. The keyword performs a pattern-match between the call's return and the function's return, so the call inside `try expr` is not directly the enclosing function's tail.

**Host functions** (host-provided / builtin) are unaffected. They execute one Go function and return — there is no Nomi-stack frame to trampoline. Interface-dispatch shims look like host functions but resolve at runtime to user-defined functions; those participate in TCO via an internal handoff back to the trampoline, so `Iface.some_fn(x)` calls behave identically to direct calls for the purposes of this guarantee.

A function that recurses in tail position needs no annotation. A computation whose natural form keeps an accumulator can be a tail-recursive helper or an `Iter.loop`; a function that recurses on both branches of a tree is not tail recursive:

```nomi
import std/io

enum Tree<T> {
    Leaf T
    Node #(Tree<T>, Tree<T>)
}

// tail recursive — requires a private helper to hide the accumulator
fn factorial(n: Int) -> Int {
    factorial_acc(n, 1)
}

fn factorial_acc(n: Int, acc: Int) -> Int {
    // private helper
    case {
        n <= 1 -> acc
        _ -> factorial_acc(n - 1, n * acc)
    }
}

// same computation with loop — no helper function needed
fn factorial_loop(input: Int) -> Int {
    #(_, acc) = Iter.loop(fn #(n, acc): #(Int, Int) = #(input, 1) -> {
        if n <= 1 { break #(n, acc) }
        #(n - 1, n * acc)
    })

    acc
}

// NOT tail recursive — both branches recurse, can't be in tail position
fn depth<T>(tree: Tree<T>) -> Int {
    case tree {
        .Leaf(_) ->
            1

        .Node(left, right) -> {
            l = depth(left)
            r = depth(right)
            if l >= r { 1 + l } else { 1 + r }
        }
    }
}

fn main() {
    io.inspect(factorial(5)) // 120
    io.inspect(factorial_loop(5)) // 120
    io.inspect(depth(Tree.Node(Tree.Leaf(1), Tree.Leaf(2)))) // 2
}
```

Non-tail recursion is allowed; it suits bounded-depth data like trees. For iteration over large or unbounded data, use `loop` or a tail call. `Iter.loop` runs its callback in constant stack space as well.

**Diagnostics.** Tail calls do not appear as separate frames in error reporting. When an error fires inside a tail-recursive function, the trace shows one frame regardless of how deep the iteration ran — that is the cost of constant stack space.

**Deferred calls.** A tail call leaves its function before the callee runs. In a function with pending `defer` registrations, the tail call's arguments are evaluated first, then the function's deferred calls run, most recent first, and then the callee runs:

```nomi
import std/io

fn leave(n: Int) {
    io.print("leave ${n}")
}

fn down(n: Int) -> Int {
    defer leave(n)
    io.print("enter ${n}")
    step(n)
}

fn step(n: Int) -> Int {
    if n == 0 { 0 } else { down(n - 1) }
}

fn main() {
    _ = down(1)
}
```

`down(1)` prints `enter 1`, `leave 1`, `enter 0`, `leave 0`.

### Recursion depth

A call that is not a tail call keeps its caller's frame, so recursion that never reaches its base case grows without bound. At most 100,000 calls may be in progress at once on one task. The call that would start one more is a runtime fault at that call's line:

```
line 4: stack overflow: more than 100000 nested calls
```

It behaves like any other fault: `nomi run` reports it and exits non-zero, `nomi test` fails that test and continues, and inside a spawned task it is the task's `Failed` outcome, which the spawner can inspect with `Task.outcome`. Each spawned task counts from zero.

A tail call does not keep its caller's frame, so it does not count toward the limit: a tail-recursive loop that never reaches its base case runs forever rather than faulting. The constant-stack guarantee holds for every tail call to a Nomi-defined function: direct, mutual, through a function value and through interface dispatch.

A function that cannot return without calling itself with the arguments it was given is a compile error at the function's name. Every path through its body must reach such a call: a call by its own name, by its owner (`Foo.again(foo)`), or, in an impl, by its interface (`Display.to_string(value)`), passing each parameter, or a plain binding of it, in its own position or by its own name. A call outside tail position always counts, since the chain can only end in the fault above. A call in tail position counts only when nothing before it on its path calls a function, because an effect there can make the loop deliberate (`fn serve(l: Listener) { handle(l); serve(l) }`):

```nomi
fn grow(x: Int) -> Int {
    y = grow(x) // every path reaches grow(x): an error at `grow` above
    y + 1
}
```

```
every path through grow calls grow(x) with the arguments it was given, so it never returns
```

A path that returns without the call (`if x == 0 { 0 } else { f(x) }`), a call with any argument changed (`f(x - 1)`), a call inside a lambda, and mutual recursion are not checked. Interface default bodies are not checked either.

## 13. Interfaces

Define a contract that types can implement. Interfaces live in files like everything else. Interface definitions use `self` as a type placeholder for the concrete implementing type. It is valid in interface signature type positions only: `self` is not a value, not an implicit parameter, and not a qualifier. Every `self` in one interface function signature denotes the same concrete type for a given implementation. A type supplies manual interface functions with a top-level `impl Iface for Type { ... }` block. See the *Implementations* section below.

```nomi
// Display lives in the standard library
interface Display {
    fn to_string(value: self) -> String
}

// Iter — the iteration interface, lives in std/iter
interface Iter<T> {
    fn each_while(collection: self, yield: (T) -> Bool) -> Bool
}
```

The `Iter` interface defines a **push** iterator: `each_while` walks the collection and hands each element to `yield`, which returns `False` to ask it to stop. `each_while` returns `True` when the source ran to exhaustion and `False` when a consumer stopped it early. The source drives its own loop, so no `self` appears in the signature and nothing is rebuilt per element. Works naturally with immutability since `each_while` reads a value it never mutates. This is Go's `iter.Seq` shape, so a Nomi iterator and a Go `for range` speak the same protocol.

`List<T>` implements `Iter<T>` — `each_while` walks the cons chain. `Vector<T>` also implements `Iter<T>`, retaining its O(1) known count while walking its slice window by index. `Map<K, V>` implements `Iter<#(K, V)>`. `String` implements `Iter<String>` and iterates by grapheme cluster, one user-visible character per element. Ranges over `Discrete` values such as `Int` and `Codepoint` also implement it, so everything composes under one protocol.

`List`, `Vector`, and `Map` also implement `Equatable` and `Hashable` (structural; map equality and hashing are order-insensitive), so a type with a collection field can write `derive Equatable` / `derive Hashable`. `List<T>` and `Vector<T>` additionally implement `Comparable` (lexicographic) when `T` does, so a List of tuples or anonymous structs has no order: `List.compare`, `<`, `Comparable.compare` and `Iter.sort` over one are compile errors, as `<` on a tuple is. `Map` is intentionally **not** `Comparable` — it has no natural total order (matching Rust's `HashMap`).

A tuple is `Equatable` and `Hashable` structurally, and not `Comparable` (as Rust derives `Hash` and `Eq` for tuples; the order is left undeclared here for the reason `<` on a tuple is an error). `Hashable.hash(#(a, b, c))` is the `derive Hashable` mix over the slots, `(Hashable.hash(a) * 31 + Hashable.hash(b)) * 31 + Hashable.hash(c)` with wrapping arithmetic, each slot hashed by its own type's impl — the same number a positional variant carrying those slots mixes before its variant index. So `Hashable.hash(#(1, 2)) == 33`. An anonymous record is `Equatable` and `Hashable` the same way, over its fields in name order, so `#{b: 2, a: 1}` hashes as `#(1, 2)` does, and `Equatable.equal?` over two tuples or two records is `==`.

### Interface Body Items — the Kinds

An interface body is an item list. Each item's *form* determines its role, on two orthogonal axes — whether/how a body is supplied, and (for defaults) whether it's overridable. There is **no `pub` on interface items**: a function's visibility is the interface's. The grid:

| Item form | Role |
|---|---|
| `fn` signature, no body | **Required function** — part of the contract; every impl must supply it. |
| `fn` with body | **Final default** — impls get it for free; overriding is an error. |
| `open fn` with body | **Open default** — impls get it for free; an impl may override it. |
| `host fn` (no body) | **Host-backed default** — provided by the runtime to every implementor; final. |
| `open host fn` (no body) | **Open host-backed default** — host-provided; an impl may override it. |

`open` is the overridability axis; the body source (Nomi `{ … }`, host-provided, or none) is the other. Every item is a function: an interface declares no fields (see *Requiring a Value* below).

Every bodied/host item is part of the contract (a default), reachable both interface-qualified (`Iter.known_count(xs)`) and type-qualified on an implementor (`List.known_count(xs)`) with full generic inference — the two are the *same* function, selected by the concrete type bound to `self`. Operations that take no `self` are **not** interface functions — put them in an inherent `impl Iface { ... }` block beside the interface and call them owner-qualified (`Iter.from(0)`).

> The example uses `known_count` because, for `Iter`, the defaults are protocol hooks — the interface is pure protocol, just `each_while` (required) plus the two `open` defaults `known_count` and `infinite?`. **Every generic iteration algorithm is an `Iter` owner function, not a dispatched interface function**: the adapters (`map`/`scan`/`filter`/`take`/`drop`/`take_while`/`drop_while`/`flat_map`/`zip`/`cycle`/`concat`/`with_index`), the terminals (`reduce`/`map_reduce`/`find`/`each`/`any?`/`all?`/`empty?`/`first`/`last`/`get`/`count`/`min`/`max`/`min_by`/`max_by`/`sum`/`sort`/`sort_by`/`partition`/`group_by`/`frequencies`), and the materializers (`to_list`/`to_vector`/`to_set`/`to_map`/`reverse`/`flatten`). So `Iter.map(xs, f)` and `Iter.reduce(xs, f)` are owner-qualified calls, not dispatch calls. There are also no eager collection-adapter functions: a `List` back from a chain is the explicit `Iter.map(xs, f) |> Iter.to_list()`. Container-specific work a generic adapter can't express because it reads or rebuilds a container's structure lives on the relevant owner — `Map.map_values`, `Set.union`, `List.concat`, `Vector.get`, `Map.size`/`Set.size`, native `String.reverse`. The one rule: generic over any iterable → `Iter.X`; specific to a container → that container's owner. `known_count` and `infinite?` are the only ops that are dispatched defaults rather than owner functions, because they are the protocol hooks `Iter.count` consults for an O(1) count and every full-consuming terminal consults before it starts (see §19).

A Nomi-bodied default function may give its **trailing parameters default values**, exactly like a free `fn` (`fn announce(value: self, loud: Bool = False) -> String { … }`); a caller omitting the trailing argument gets the default. Defaults are rejected on a required or host-backed function — only a Nomi default body carries the value at runtime.

An interface function may carry a **function-level `where` clause** that
constrains an in-scope type variable for that one function. Required
signatures write it after the return type; default functions write it between
the return type and the body:

```nomi
interface Ranked<T> {
    fn entries(collection: self) -> List<T>

    // `T` is the interface's element type; `best` additionally requires it
    // be Comparable, without constraining the rest of the interface.
    fn best(collection: self) -> Maybe<T> where T: Comparable {
        Iter.sort(entries(collection), Direction.Descending) |> Iter.first()
    }
}
```

The named variable is the interface's own type parameter (`best`'s `T` above), an explicit method-local type parameter introduced by that interface function, or, on a default, `self` (below):

```nomi
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

Here `K` is declared by `fn prefer<K>` and then constrained by `where K: Comparable`. A bare `K` that appears only in parameters, returns, or a `where` clause is an error; interface methods do not invent implicit function-local generics. The bound list uses the same `: A and B` form everywhere `where` appears. This is Rust's `fn max(self) -> Option<Self::Item> where Self::Item: Ord` — a per-function bound on the element type, so a function like `best` can require `Comparable` while the interface's other functions stay usable on any element. The bound is enforced and its `#(concrete, Iface)` dispatch demand recorded at each call site. `where` is a hard keyword. Ordinary `fn` declarations, implementation functions, `host fn` declarations, required interface functions, and interface defaults may all use a `where` clause; a Nomi-bodied function writes it between the return type and the body, while a bodyless signature or `host fn` writes it at the end of the signature.

**`where self` on a default.** A default function runs once for each implementor, with `self` replaced by the implementing type, so its body may use only what every implementor has: the interface's own functions and `Debug`, which every type implements. Anything else it needs of a `self` value (another interface's function, `"${value}"`, an ordering operator, a generic call whose bound names another interface) is an error at the use, `` `scale` is a `self`, and Scale does not require Display of its implementors ``, with a hint showing the clause to add. `==` is not, since it compares structurally where a type has no `Equatable`. The default states what it needs with `where self: Display` (the usual `: A and B` list, beside the function's other constraints):

```nomi
interface Scale {
    fn unit(scale: self) -> String

    fn format(scale: self) -> String where self: Display {
        "${scale} ${unit(scale)}"
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

impl Scale for Secret {
    fn unit(_s: Secret) -> String {
        "?"
    }

    fn format(_s: Secret) -> String {
        "hidden"
    }
}
```

Inside the body `self` has the bound: a `self` value enters a parameter, list or other position typed with a bound interface (`shown(scale)` with `fn shown(x: Display)`, `items: List<Display> = [scale]`), and a bound such as `where self: Add<self, self>` gives it the operator (`scale + scale`). Another value of the interface's own type (`other: Scale`) does not get the bound, since its implementor may lack it. A `self` passed to a generic function (`inspect_all(scale)` with `fn inspect_all<T>(x: T)`) instantiates it at the implementor. An implementor that implements every bound gets the default, which stays final unless it is `open`. An implementor that lacks one does not get it and writes the function itself, final or not, as `Secret` does; an `impl` block that does neither is the error `` `Secret` does not implement Display, which `Scale.format`'s default requires (`where self: Display`) `` at the block, with the hint ``implement Display for Secret, or write `fn format` in this impl``. So every implementor has the function, and a call (`Scale.format(x)`, `Secret.format(x)`, `T.format(x)` under `where T: Scale`, or through a `Scale` value) checks no `self` bound. This is Rust's `where Self: Display` on a provided method, except that an implementor without the bound supplies the function rather than losing it. `where self` belongs to a default only: on a required function it is the error ``interface function 'unit': `where self` is only allowed on a default function, and 'unit' is a required function`` (every implementor writes a required function, so the bound would restrict nothing), and on anything outside an interface it is ``function 'f': `where self` is only allowed on an interface's default function``.

A default runs for one implementor at a time, so its signature may name `self` anywhere: in its result (`fn grown(x: self) -> self`), inside a composite (`fn pair(x: self) -> #(self, self)`, `fn twice(x: self, y: self) -> List<self>`), and with no `self` parameter at all (`fn zero() -> self { Sized.make(0) }`, calling a required `fn make(n: Int) -> self`). An interface-qualified call of a function with no `self` parameter inside the interface's own default runs the implementor's function.

**Defaults over distinct types.** A family of distinct types over one representation (temperatures in several units, IDs over `Int`, money in several currencies) would otherwise repeat one body per type, because a default cannot name the implementor to take it apart. The built-in interface `Distinct<T>` (§*Distinct Types*) gives it a way: in a default bounded by `where self: Distinct<T>`, the pattern `self(p)` takes a `self` value apart, binding what it wraps to `p`, and `self(x)` builds a `self` from a `T`:

```nomi
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
```

`Temperature.label(Celsius(21.456d))` is `"21.5 C"` and `Celsius.scale(Celsius(10d), 2d)` is `Celsius(20d)`. A default is lowered once per implementor, so `self(p)` is that implementor's own destructuring (`Celsius(n)`) and `self(x)` its constructor. A parameter written `self(p)` has type `self`, and needs no annotation; `self(p)` is also a pattern in a `case` arm and in a binding (`self(m) = other`) for another `self` value, and a `case` over `self` patterns must cover the wrapped value as one over `Celsius(...)` would. Named as a value, `self` is the constructor function (`ns |> Iter.map(self)`, `n |> self()`). `T` may be a type parameter of the interface, so `interface Wrapper<T>` with `fn unwrap(self(x)) -> T where self: Distinct<T>` serves `impl Wrapper<Int> for UserId` and `impl Wrapper<String> for Name`. A free function bounded by `where D: Distinct<T>` cannot take `D` apart or build one; only interface defaults can.

An implementor that is not a distinct type, or wraps another type, lacks the bound and writes the function itself, as for any `where self` bound; the error at the impl names it, `` `Count` does not implement Distinct<Decimal>, which `Temperature.label`'s default requires (`where self: Distinct<Decimal>`) ``, with the hint `` `Count` wraps Int, so it is not a Distinct<Decimal>; write `fn label` in this impl ``. An opaque type implemented outside its own file (§*Opaque distinct types*) satisfies `Distinct<T>`, but a default that takes it apart or builds it would open its representation there, so it does not get such a default: the `impl` is the error `` `Meters` is opaque here, so it can't use `Scaled.scale`'s default, which builds and takes apart `self`; write `fn scale` in this impl ``, and writing the function is allowed even when the default is final. An impl in the type's own file gets the default as usual. `self(...)` in a default without the bound is the error `` `self(...)` needs `where self: Distinct<T>` on this function ``, with the clause in the hint; anywhere outside an interface default it is `` `self(...)` only appears in an interface's default function `` (an `impl` names its own type: `Celsius(n)`). A pattern or argument that does not fit `T` is the usual mismatch at it, and `self(p)` against a value of the interface's own type (`other: Temperature`, any implementor) is `` `self(...)` takes apart a value of type `self` ``.

Interfaces can provide default implementations. Default implementations can call other interface functions unqualified. Implementors get defaults for free; defaults are **final by default** — implementors only get to override them when the interface marks the default `open`:

```nomi
interface Formatted {
    fn format(value: self, style: String) -> String

    // final default — implementors get this for free, can't override.
    fn format_pretty(value: self) -> String {
        format(value, "pretty")
    }

    // open default — an implementing type may override format_brief.
    open fn format_brief(value: self) -> String {
        format(value, "brief")
    }
}
```

The final-by-default rule is the same intuition as a `final` method in Java/C# or a non-`open` method in Swift, translated into Nomi's function model: a default that the interface author did not flag as an extension point is part of the protocol's stable surface, and overriding it would break the contract for callers that rely on the published behavior. Marking a default `open` is an explicit "this is meant to be tweaked" affordance.

**Host-backed defaults** (`host fn` / `open host fn`) are the home for operations the runtime implements rather than Nomi. They behave like any other default (provided to every implementor, dispatched), but the body comes from the host; `open host fn` permits a type to ship a specialized override. `Iter`'s own `known_count` is the mirror image: an `open` *Nomi* default returning `None`. `List` overrides it with a host-backed `host fn known_count`, while `Vector` overrides it by returning `Some(Vector.length(vector))`; both give `Iter.count` an O(1) fast path (§19). The generic iteration *algorithms* themselves — `reduce`/`find`/`sort`/`map`/`count`/… — are **not** interface defaults at all: they're non-dispatched owner functions (`reduce`/`sort_with` host-backed, the rest Nomi-bodied), so the host-backed-default feature is used by `Iter` only for the `known_count` override path.

### Requiring a Value

An interface declares functions only. To require that every implementor supply a value, declare a function requirement and read the value with a qualified call:

```nomi
struct Tag {
    id: Int
}

pub interface Tagged {
    fn tag(value: self) -> Tag
}

struct Event {
    tag: Tag
    name: String
}

impl Tagged for Event {
    fn tag(event: Event) -> Tag {
        event.tag
    }
}

fn tag_id<T>(x: T) -> Int where T: Tagged {
    T.tag(x).id
}
```

A struct implements the requirement by returning its field; an enum, a distinct type or any other receiver implements it with whatever computation produces the value. The call is spelled as the dispatch it is: `Tagged.tag(e)` on a value of interface type, `T.tag(x)` through a bound, `Event.tag(e)` on the concrete type.

`field name: Type` in an interface body is a parse error that names the replacement: ``interfaces declare functions only; replace `field tag: Tag` with a function requirement such as `fn tag(value: self) -> Tag` ``. A value of interface type or of type-parameter type has no fields, so `x.tag` on one is an error (§14 *Member access is fail-loud*).

An interface whose functions all have defaults, or one that declares none, is implemented by a bodyless declaration, `impl Iface for Type`, which is the same as an empty body.

### Implementations

Manual implementations are top-level `impl Iface for Type { ... }` blocks. The header names one interface and one implementing type; the body provides that interface's functions as plain `fn` / `host fn` items. Interface signatures use `self` as the concrete implementing-type placeholder; implementations spell the actual concrete type:

```nomi
interface Formatted {
    fn format(value: self, style: String) -> String
}

struct User {
    name: String
}

impl Formatted for User {
    fn format(user: User, style: String) -> String {
        "User: ${user.name} (${style})"
    }
}
```

Inside an impl block, the implementing type is written exactly like any other type annotation: `User`, `Box<T>`, `Result<T, E>`. This keeps generic signatures self-contained — `fn map(maybe: Maybe<T>, f: (T) -> U) -> Maybe<U>` shows immediately which parameters belong to the implementing type and which are function-local. Implementation functions do not write `self`; `self` is never a value, implicit argument, expression qualifier, or concrete-body type alias. Inside an `impl Iface for Type { ... }` block, sibling functions may be called bare; outside that declaration body, calls are qualified with a type or interface (`Box.map(...)`, `Display.to_string(...)`).

Operations that are not interface implementations are ordinary file-level
functions beside the type, or inherent functions in an `impl Type { ... }` block. Nomi does not have extension methods: a sibling file
cannot reopen an imported type with new type-qualified members.

A file may contain an `impl Iface for Type { ... }` block, but the file is only the lexical home for that declaration. It does not implement the interface. If a stateless API needs to participate in an interface, model it as a zero-sized `type` and implement the interface on that type.

The same interface-implementation form is used whether the implementing type is declared locally or imported from another package. For generic receiver types, the type parameters come from the receiver itself: write `impl Formatted for Box<T> { ... }`, not `impl<T> Formatted for Box<T>`. The receiver must name them: `impl Formatted for Box` and `impl Box { ... }` for a generic `Box` are the error `` `Box` expects 1 type argument, got 0 ``, whose hint is the header to write. Constraints written on the type declaration flow into its impls. An impl-level `where` clause adds only further narrowing constraints for that implementation, such as `impl Iter for Range<T> where T: Discrete { ... }`. The block holds only that interface's implementation functions as plain `fn`/`host fn` items — no inherent `fn` items, no nested `impl` entry, and no `derive` entry:

```nomi
import users.User

impl Formatted for User {
    fn format(user: User, style: String) -> String {
        ...
    }
}
```

Once accepted, every manual impl feeds the same implementation model: coherence ("one impl per `#(Iface, Type)` and type arguments, program-wide"), the orphan rule, missing-impl diagnostics, and dispatch. Duplicate `impl Iface for Type` declarations collide.

**The `impl ... for` rule:** an `impl Iface for Type` declaration declares exactly one implementation. Add a `{ ... }` body only when the interface has functions to implement. Implement several interfaces with one declaration each. Generic implementing types name their receiver parameters in the receiver type: `impl Iface for Box<T>`.

**Derives.** `derive Iface for Type` asks the compiler to synthesize the impl (see §38.1). Each derive declaration names one interface and the type it applies to. A derive may pass derive-specific compile-time options with `with <config-expression>`, and may add receiver-side narrowing constraints with `where`, using the same form as a manual impl. When both are present, `with` comes before `where`:

```nomi
pub opaque type Duration Int

derive Equatable for Duration
derive Hashable for Duration
derive Comparable for Duration

struct Box<T> {
    value: T
}

derive Display for Box<T> where T: Display

struct User {
    first_name: String
    age: Int
}

derive ToJson for User with #ToJson.Options{rename_all: Json.Case.Camel}
derive FromJson for User with #FromJson.Options{rename_all: Json.Case.Camel}

impl Debug for Duration {
    fn inspect(d: Duration) -> String {
        ...
    }
}

impl Display for Duration {
    fn to_string(d: Duration) -> String {
        ...
    }
}
```

Interface functions can be called two ways — qualified by the implementing type or by the interface itself. Both route through the same runtime dispatch: the concrete type bound to `self` selects the implementation.

```nomi
user = #User{name: "Alice"}

// Type-qualified — names the implementing type
User.format(user, "pretty") // "User: Alice (pretty)"

user |> User.format("pretty") // equivalent with pipe

// Interface-qualified — names the contract
Formatted.format(user, "pretty") // "User: Alice (pretty)"
```

Type-qualified — `User.format(x)` with a concrete type, or `T.format(x)` when `T` is a generic parameter bounded by the interface (see "Type-parameter-qualified" below) — is the usual spelling when the type is in hand. Interface-qualified calls are for places where the interface is the meaningful static surface: existentials (an interface-typed value, where the static type **is** the interface), function-name collisions, generic code that is intentionally written against a protocol rather than a known concrete type, and target-typed functions such as `FromJson.from_json<User>(json)`. A bare `format(user, ...)` is rejected: interface-function calls are always qualified. A file-qualified `users.format(user)` is rejected too: file qualification reaches only file-level functions, never an `impl` block's (§7 *Calling Convention*).

Interface functions do not carry their own visibility — they inherit the visibility of the implemented interface (there is no per-function `pub` marker). Implementing an exported interface makes the functions callable from outside the defining file; implementing a private interface confines them to the same file. Helper functions that don't belong in the interface go in free-standing `fn` declarations at file level — they use the same qualified-prefix call style as every other Nomi function.

Because interface functions inherit interface visibility, implementing a public interface is a public commitment. Outside callers can dispatch every function on that interface, including defaults, against values of the implementing type. A public interface is a published protocol, and an `impl` opts the type into that protocol. The implication is that you cannot use a public interface as purely internal scaffolding for a public type; using the defaults commits to the contract.

When you want the protocol's machinery (typically the default functions) only inside the file — without exposing the impl as part of the type's API — wrap the type in a private distinct type and impl on the wrapper:

```nomi
interface Aggregate {
    fn values(it: self) -> List<Float>

    fn mean(it: self) -> Float {
        xs = values(it)
        Iter.sum(xs) / Int.to_float(Iter.count(xs))
    }
}

pub struct Stats {
    samples: List<Float>
}

// internal scaffolding — not pub, not constructible outside the defining file
type AggStats Stats

impl Aggregate for AggStats {
    fn values(it: AggStats) -> List<Float> {
        AggStats(s) = it
        s.samples
    }
}

pub fn average(s: Stats) -> Float {
    Aggregate.mean(AggStats(s)) // uses Aggregate's default internally
}
```

Outside callers see only `Stats` (no `Aggregate` impl) and cannot construct `AggStats`, so the scaffolding is invisible. The wrapping friction this adds at every internal entry point is usually a useful forcing function: a wrapper that earns a name and a few helpers is a real domain concept; a wrapper that hurts only because of one default-function call probably wants the default inlined instead.

Multiple types in the same file can implement the same interface — each uses
its own `impl Iface for Type` declaration. The compiler dispatches based on the
argument type through the qualified interface function:

```nomi
import std/io

interface Speaker {
    fn speak(value: self) -> String
}

struct Dog {
    name: String
}

impl Speaker for Dog {
    fn speak(dog: Dog) -> String {
        "${dog.name}: woof"
    }
}

struct Cat {
    name: String
}

impl Speaker for Cat {
    fn speak(cat: Cat) -> String {
        "${cat.name}: meow"
    }
}

fn main() {
    // Both dispatched the same way (interface- or type-qualified):
    io.print(Speaker.speak(#Dog{name: "Rex"})) // Rex: woof
    io.print(Cat.speak(#Cat{name: "Tom"})) // Tom: meow
}
```

### Function Name Collisions

If a type implements two interfaces that define the same function name, each
implementation function uses an interface-qualified name; use
interface-qualified calls the same way:

```nomi
import std/io

interface Speaker {
    fn speak(value: self) -> String
}

interface Performer {
    fn speak(value: self) -> String
}

struct Dog {
    name: String
}

impl Speaker for Dog {
    fn speak(dog: Dog) -> String {
        "${dog.name}: woof"
    }
}

impl Performer for Dog {
    fn speak(dog: Dog) -> String {
        "${dog.name} takes a bow"
    }
}

fn main() {
    my_dog = #Dog{name: "Rex"}

    // Disambiguate via interface-qualified calls
    io.print(Speaker.speak(my_dog)) // Rex: woof
    io.print(Performer.speak(my_dog)) // Rex takes a bow
}
```

Type-qualification cannot disambiguate here — `Dog.speak(my_dog)` names the concrete type, which both impls share. Only the interface qualifier can pick a contract, so interface-qualification is **required** for a function name a type implements under two interfaces. That holds wherever the impls are: a file that imports only `Dog`, with the `Performer` impl in a third file, gets the same error for `Dog.speak(my_dog)`. A bare `speak(my_dog)` is rejected because interface function calls are always qualified. A file may expose a `speak` façade, but that façade still has to resolve to exactly one interface function for the receiver type.

> **Display and Debug deliberately do *not* collide.** The two most prolific stdlib interfaces have *distinct* function names — `Display.to_string` and `Debug.inspect` — so a type can implement both without a same-name clash. Reach them through the interface (`Display.to_string(xs)`, `Debug.inspect(xs)`). The collision rule above is reachable only by *user* code that defines two interfaces sharing a function name.

When one type intentionally satisfies several contracts that genuinely share a
function name and signature, put each implementation in its own
`impl Interface for Type { ... }` block. The shared function name is resolved
by the interface qualifier at the call site.

### Parameter Name Matching

Parameter names for positions typed exactly `self` are free — implementors can use descriptive names because `self` is the implementing type placeholder. Their type is not: a position typed `self` takes the block's receiver, so `fn greet(p: Person)` in `impl Greeter for Peon` is a compile error, and so is an impl block whose receiver names no type. All other parameter names must match the interface (Swift-style). This is required because Nomi supports named arguments at call sites — the interface's parameter names are the contract:

```nomi
interface Formatted {
    fn format(value: self, style: String) -> String
}

struct User {
    name: String
}

impl Formatted for User {
    // ok — the `self` position uses descriptive name "user", non-User param "style" matches
    fn format(user: User, style: String) -> String {
        ...
    }
}

struct Report {
    title: String
}

impl Formatted for Report {
    // compiler error — "s" doesn't match "style"
    fn format(report: Report, s: String) -> String {
        ...
    }
}
```

### Parameter and Return Type Matching

An implementation function's parameter and return types must match the interface function's, after the impl header's type arguments replace the interface's type parameters and the block's receiver replaces `self`. That holds for every function, including one that names `self` only in its return type (`fn make(n: Int) -> self` implemented for `Day` returns `Day`), and a function the interface declares with no return type returns `Unit` in the implementation too. Each of the header's type arguments must name a type: `impl Add<Dbys, Day> for Day` is the error `unknown type "Dbys"`. A mismatch names the function the implementation must write, such as ``interface 'Add<Days, Day>' requires `fn add(lhs: Day, rhs: Days) -> Day` ``. The impl's own type parameters are fixed inside the impl: where the interface requires `T`, the function takes or returns `T`, never a concrete type and never another of the impl's parameters. A function-level type parameter may be renamed, but each one of the interface function's corresponds to exactly one of the implementation's: the implementation may not fix it to a concrete type, merge two into one, or replace it with one of the impl's parameters.

```nomi
interface Store<K, V> {
    fn get(store: self, key: K) -> Maybe<V>
}

struct Cache<T> {
    items: Map<String, T>
}

impl Store<String, T> for Cache<T> {
    // ok — V is T
    fn get(cache: Cache<T>, key: String) -> Maybe<T> {
        ...
    }
}

impl Store<String, T> for Shelf<T> {
    // compiler error — return type Maybe<Int> does not match Maybe<T>
    fn get(shelf: Shelf<T>, key: String) -> Maybe<Int> {
        ...
    }
}
```

An implementation function's `where` clause may only repeat bounds the call through the interface already guarantees: the interface function's own `where` bounds on the corresponding parameter, and, for one of the impl's parameters, the bounds on the impl block (`impl Show<T> for Cache<T> where T: Named`) and on the receiver's declaration. A call through the interface checks only those, so any other bound is an error; a bound on one of the impl's parameters belongs on the impl block.

### Composition: No Interface Inheritance

Nomi deliberately omits interface inheritance. There is no `extends` keyword and no parent/child relationship between interfaces. Relationships between contracts are expressed at use sites with `where` bounds, optionally named with a `typealias`:

```nomi
// Comparable is the prelude's `interface Comparable { fn compare(a: self, b: self) -> Ordering }`.
interface Sortable {
    fn sort_key(value: self) -> String
}

// Direct multi-interface bound at the consumer.
fn sort_direct<T>(list: List<T>) -> List<T> where T: Sortable and Comparable

// Named bound — `typealias` over `and`. Use sites read as a single name.
typealias SortableValue Sortable and Comparable

fn sort<T>(list: List<T>) -> List<T> where T: SortableValue
```

Each interface stands alone. A function that needs both contracts says so directly in its bound clause (or via a typealias that names the conjunction); nothing is implied by the interface declarations themselves. This keeps interface declarations local — no chain to chase — and makes every required contract visible at the consumer site.

An interface-bound alias (`typealias Name A and B`) is usable only in `where` bounds and in the RHS of another bound alias. Using it as a value type (parameter annotation, struct field, return type) is rejected by the checker with a pointer at the `where T: Name` form.

The cost of this design is a small amount of repetition at consumers that need several interfaces together; bound aliases collapse that to a single name. The benefit is that interface declarations carry no hidden constraints, refactoring an interface doesn't silently change requirements elsewhere, and there is no "where does function X live?" navigation question.

Heterogeneous collections of interface-implementing values — the use case `extends`-style hierarchies often serve in OOP languages (for example, `List<Event>` for mixed click/key/resize events) — are expressed in Nomi via enums with `embeds` instead. See §8 Embedded Types for the tagged-sum pattern that replaces the inheritance-flavored design.

### Interface Bounds on Generics

Generic type parameters can be constrained to types that implement a specific interface:

```nomi
fn sort<T>(list: List<T>) -> List<T> where T: Comparable
```

`T` must implement `Comparable`. The bound is also what the body may use: a generic body runs for every type argument its callers pass, so an interface it needs of a value of type `T` (an interface-qualified call or pipe, `"${value}"`, an ordering or arithmetic operator, a call of a generic function bounding it) must be one of `T`'s bounds, from the function's `where`, its impl block's or its type's declaration. Without it the use is an error, `` `x` is a `T`, and `f` does not require Display of `T` ``, with a hint showing the signature with `where T: Display` added; a value that holds `T` (`xs: List<T>` in `"${xs}"`) is reported as `` `xs` holds values of `T` ``. On an interface impl the hint shows the bound on the block (`impl Display for Box<T> where T: Display`), and on an inherent impl, whose block has no `where`, on the function. `Debug` needs no bound, since every type implements it, and neither does `==`, which compares structurally. The compiler rejects calls with types that don't satisfy the bound: `sort([#(1, 2)])` fails with ``no impl of `Comparable` for `#(Int, Int)`; tuple types cannot implement `Comparable` ``.

The error's help offers what the program can write. For a type it declares, that is `derive` when the interface is derivable (§38.1) and an impl block spelled with the interface's required functions (``write an `impl Shape for Point` block with `fn area(s: Point) -> Float` ``). Where it declares neither the type nor the interface, the orphan rule forbids the impl, and the help says so. For an interface at most four types implement, it names them: `String.split("a1b", 1)` reports ``Int does not implement Matcher (required by `where M: Matcher`)`` with the help ``only std can implement `Matcher` for `Int`; `Codepoint`, `Regex` and `String` implement it``. For one that more types implement, it says to wrap the type: ``only std can implement `Hashable` for `Ordering`; wrap it in a type of your own that implements `Hashable` ``.

Multiple bounds are joined with `and`:

```nomi
fn describe<T>(value: T) -> String where T: Showable and Tagged {
    Tagged.tag(value) + " " + Showable.show(value)
}
```

The bound list is conjunctive — every interface in the list must be satisfied at every call site. There is intentionally no `or` operator: a disjunctive bound `T: A or B` could only call functions present in both `A` and `B`, so it adds nothing beyond what bounding on the common parent interface already gives you. The keyword `and` mirrors Nomi's boolean conjunction; symbolic forms like Rust's `+` or Swift's `&` are not used.

Generic headers introduce names only. Constraints are written in a `where` clause. On functions it appears after the return type; on type declarations it appears after the type parameter list and before the body:

```nomi
fn step_by<T, S>(r: Range<T>, by: S) -> Iter<T> where T: Comparable and Steppable<S> {
    #StepByRange{range: r, by}
}
```

All type parameter names are in scope throughout the full generic header and the `where` clause, so `Steppable<S>` refers to the same `S` declared in `<T, S>`.

```nomi
opaque struct StepByRange<T, S> where T: Comparable {
    range: Range<T>
    by: S
}
```

`where` is always a narrowing constraint. It adds requirements to an already-named type parameter; it never removes, weakens, or replaces bounds from the receiver type declaration.

### Calling Interface Functions in Generic Code

Inside a generic function, you don't know the concrete type — so you can't write `User.format(item, style)` (you don't know `item` is a `User`). Use the fully-qualified interface name to call the function:

```nomi
fn render<T>(item: T, style: String) -> String where T: Formatted {
    Formatted.format(item, style)
}

render(#User{name: "Alice"}, "pretty") // "User: Alice"

render(#Report{title: "Q1"}, "pretty") // "Report: Q1"
```

`Formatted.format(item, style)` means "call whichever `format` implementation `T` provides." This interface-qualified call form is implemented today and used throughout `std/iter.nomi` (for `Iter.each_while(...)` over a polymorphic `source: Iter<T>` parameter).

Named arguments work as expected — the interface's parameter names are the contract:

```nomi
Formatted.format(value: item, style: "pretty")
```

**Type-parameter-qualified calls.** In generic code, a value's type is often a parameter `T` bounded by an interface (`fn show<T>(x: T) -> String where T: Display`). The call can be qualified by the *parameter* — `T.to_string(x)` — as a generic form of qualified dispatch. The analyzer resolves `T` to the bound interface that declares the function (rejecting an unbounded `T`, a function no bound provides, or — when two bounds declare the same name — an ambiguous call that must be interface-qualified instead); the call then selects the implementation for the concrete type bound to `self`, exactly like `Display.to_string(x)`. The `T` keeps its identity — hover and go-to-def see the type parameter, not the interface it resolves to.

Interface-qualified calls (`Interface.some_fn(value)`) are *required* in exactly two cases: when the static type **is** the interface — an *existential*, i.e. an interface-typed parameter, field, or collection element whose concrete type is erased, so there is neither a concrete type nor a parameter to name (this is what `std/iter.nomi` uses for `Iter.each_while(...)` over a polymorphic `source: Iter<T>` parameter) — and when two of a parameter's bounds declare the same function name (`T.some_fn` is ambiguous; name the interface to disambiguate). Everywhere a concrete type or its bounded parameter is in hand, type- or `T`-qualified works too — see "Implementations" above for when each reads more naturally.

#### Implementation: Instantiation and Dispatch

The compiler builds one body per instantiation: each generic function, generic
type and impl a program reaches is lowered once for each tuple of concrete type
arguments it is used at, so inside a body `T` is a known type and
`T.format(x)` calls that type's implementation directly.

Interface calls on existentials still **dispatch at run time**. An
interface-typed parameter, field, or collection element erases its concrete
type (§15), so there is nothing to instantiate against; values carry their type
identity, and the call selects the implementation by the receiver's identity.
The standard library depends on this: every lazy adapter in `std/iter` takes
its upstream as an existential `Iter<T>`, and typed literals pass
`List<Fragment<I>>` whose slots are heterogeneous per element.

A value of interface type may hold any implementor, and an interface
requires nothing of its implementors but its own functions; there are no
supertraits. So the interfaces a value of type `Scale` can be used at are
`Scale` itself, `Debug`, `Equatable` and `Hashable`. Every type has a
`Debug`, and equality and hashing need nothing of the implementor (below).
Using any other one on it (`Display.to_string(other)`, `"${other}"`, `a <
b`, `Iter.sort(scales)`, a generic call whose bound names another interface,
or the same through a `List<Scale>`) is an error at the use, `` `other` is a
`Scale`, and Scale does not require Display of its implementors ``, whose
hint writes a type parameter bounded by both: `fn f<T>(other: T) -> String
where T: Scale and Display`. `Comparable` is never satisfied, since two
values of different types have no order, and its error says so: `` `a` is a
`Scale`, and Scale does not require Comparable of its implementors: values
of different types have no order ``.

`==` and `!=` on two values of interface type compare the values they hold,
and a `Set` element or a `Map` key of interface type (`Set<Scale>`,
`Map<Scale, Int>`, `Map<List<Scale>, Int>`) matches by the same rule. Two
values held as different concrete types are unequal: a `Point` and a `Spot`
with the same fields, an `Int` and a `String`, a `List<Int>` and a
`List<String>` (each empty), or a value of an enum with an `embeds` variant
and a value of the struct it embeds. Two values of one concrete type compare
as `==` on that type does (§30): through its hand-written `impl Equatable`
when it has one, structurally otherwise. Hashing follows the type's
hand-written `impl Hashable`, as a key of that type does (`Map<K, V>`).
This holds for std's `Display` and `Debug` as for an interface of your own:
two empty lists of different element types held as `Display` are two
values, and an `embeds` enum's value held as `Display` renders through the
enum's `impl Display`, not the one of the struct it embeds.

A value of interface type, a generic interface's instance (`Has<Int>`)
included, therefore satisfies an `Equatable` or `Hashable` bound with that
meaning: `List.contains?(scales, s)`, `Iter.contains?`, `Iter.chunk_by` with
an interface-valued key, `Iter.to_set`, a function of your own `where T:
Equatable` or `where T: Hashable` given a `Scale`, and a `derive Equatable`
or `derive Hashable` on a struct with a `Scale` field each compare and hash
the held values that way. `Equatable.equal?(a, b)` on two `Scale` values is
`a == b`, and `Hashable.hash(s)` is the hash a `Set<Scale>` gives `s`, so two
values `==` calls equal hash alike. An `Iter<T>` value is the exception: it
is a function and has no equality (§30).

A call through the interface on such a value needs nothing but the receiver
to be the implementor. `Sized.same(e)` with `fn same(x: self) -> self`, a
result holding `self` (`#(self, self)`), a second `self` argument, or a
parameter holding `self` values (`xs: List<self>`) would each need the
implementor's own type, which a `Sized` value does not name, so each is an
error at the argument, `` `Sized.same` cannot be called on `e`, a `Sized`:
its result is `self`, the implementor's own type, and a `Sized` value does
not say which implementor it holds ``, with a hint naming a type parameter
bounded by `Sized`. A function with no `self` parameter
(`fn make(n: Int) -> self`) called through the interface is an error unless
where the result goes names the implementor (`p: Pot = Sized.make(1)`) or
the call is in one of the interface's own defaults: `` `Sized.make` has no
`self` parameter, so this call does not say which implementor of Sized runs
``. This is the rule Rust calls object safety. Through the implementing type
(`Pot.same(p)`) or a bounded type parameter (`T.same(t)`) every one of these
calls is fine.

A generic function that instantiates itself at an ever-growing type
(`fn grow<T>(x: T)` calling `grow(#Box{inner: x})`) has no finite set of
instantiations, so it is a compile error at the call that grows the type
(§15 *Generics*).

Heterogeneous collections have two spellings, and both are supported. Use an
enum with pattern matching when the alternatives are a closed, known set;
use an interface-typed collection when the set is open and callers supply their
own conforming types (§15).

### Generic Interfaces

Interfaces can have type parameters:

```nomi
interface Container<T> {
    fn get(container: self, index: Int) -> Maybe<T>
    fn size(container: self) -> Int
}
```

In interface definitions, `self` implicitly includes the type parameters — so `self` in `Container<T>` means the full implementing type with its type arguments. A generic implementation writes the concrete implementing type explicitly (`impl Container for Shelf<T> { ... }`); `T` is the type parameter declared by `Shelf`.

Interface arguments may be inferred from the implementation function signatures or written in the impl header when the interface identity needs to be explicit. For example, `impl Container for Shelf<T>` can infer the contained item type from `fn get(...) -> Maybe<T>`, while `impl Add<Days, Day> for Day` says up front that `Day + Days` returns `Day`.

#### Several instantiations for one type

A type may implement one generic interface at several type arguments: `impl Contains<String> for String`, `impl Contains<Regex> for String` and `impl Contains<Codepoint> for String` are three impls, as are `impl Add<Days, Day> for Day` and `impl Add<Weeks, Day> for Day`. The operator interfaces follow the same rule as every other generic interface. Two rules keep the impls apart:

- **Each writes its type arguments.** An impl whose header leaves them to be inferred beside another impl of the interface for the type is the error `` `Box` implements `Holds` more than once, so each impl writes its type arguments in its header ``, whose help is the header to write (`` write `impl Holds<Int> for Box` ``).
- **No two overlap.** Either every impl names its interface type arguments with distinct concrete types, or there is exactly one impl, which may be generic in them. `impl Holds<T> for Wrap<T>` beside `impl Holds<Int> for Wrap<T>` is the error `` overlapping impls: `Holds<T>` for `Wrap` is generic in its type arguments, so it covers `Holds<Int>`, which `Wrap` also implements ``. Two impls at the same arguments are a duplicate impl, `` duplicate impl: `Holds<Int>` is implemented for `Box` by modules main (function `holds?`); a type implements an interface once at each set of type arguments ``.

The orphan rule applies to each impl as to any other (§*Foreign Types*): the interface, the receiver, or a type heading an input type argument is the module's own. So `impl Contains<Tag> for String` is allowed in the module that declares `Tag`, since `contains?` takes a `Tag`, and `impl Contains<Int> for String` outside std is an orphan impl.

A call takes the impl its arguments' types choose, statically, and calls it directly: `String.contains?(s, "x")`, `Contains.contains?(s, '-')`, a pipe stage `` s |> String.contains?(Regex`\d`) ``, a partial capture `&String.contains?(&1, "x")` where a `(String) -> Bool` is expected, an operator, and a generic call whose bound `where C: Contains<T>` the call solves (`all_present(s, ["a"])` takes `Contains<String>`) all select the same way. A type argument that no parameter names, only the result, is chosen by the type the result is expected to have, as a generic call's result-only type parameter is (§2): with `impl Make<Int> for Factory` and `impl Make<String> for Factory`, `n: Int = Factory.make(f)` calls the first. When the arguments and the expected type do not choose one impl, the call is an error that names the candidates: `Box.holds?(b, .Red)` with `Holds` implemented at `Color` and `Shade` is `` `Holds.holds?` for `Box` has impls at `Color` and `Shade`; the argument's type does not choose one: annotate it ``, an unannotated `Factory.make(f)` is `` `Make.make` for `Factory` has impls at `Int` and `String`; only the result's type tells them apart, and nothing here expects one: annotate the result ``, and an argument no impl takes is `` `Holds.holds?` for `Box` has impls at `Int`, `String` and `Color`, and none takes `Float` ``. An argument whose type its parameter decides (`.Variant`, a partial capture's open `&1` with nothing expected) does not choose.

A value held as an instantiation, `h: Holds<Int>` or a `List<Holds<Int>>`, takes any type with an impl at those arguments, and a call through it dispatches to that impl; a type implementing two instantiations is held as either.

```nomi
import std/io

interface Holds<T> {
    fn holds?(b: self, x: T) -> Bool
}

struct Box {
    n: Int
    s: String
}

impl Holds<Int> for Box {
    fn holds?(b: Box, x: Int) -> Bool {
        b.n == x
    }
}

impl Holds<String> for Box {
    fn holds?(b: Box, x: String) -> Bool {
        b.s == x
    }
}

fn held_by<C, T>(c: C, x: T) -> Bool where C: Holds<T> {
    C.holds?(c, x)
}

fn first_text(hs: List<Holds<String>>) -> Bool {
    case hs {
        [h, .._] -> Holds.holds?(h, "a")
        [] -> False
    }
}

fn main() {
    b = #Box{n: 1, s: "a"}
    io.print(Bool.to_string(Box.holds?(b, 1))) // True: Holds<Int>
    io.print(Bool.to_string(Holds.holds?(b, "b"))) // False: Holds<String>
    io.print(Bool.to_string(held_by(b, "a"))) // True: the bound at String
    io.print(Bool.to_string(first_text([b]))) // True
}
```

Example implementation:

```nomi
import collections.Container

struct Shelf<T> {
    items: List<T>
}

impl Container for Shelf<T> {
    fn get(shelf: Shelf<T>, index: Int) -> Maybe<T> {
        Iter.get(shelf.items, index)
    }

    fn size(shelf: Shelf<T>) -> Int {
        Iter.count(shelf.items)
    }
}

s = #Shelf{items: ["a", "b", "c"]}

Shelf.get(s, 1) // Some("b")

Shelf.size(s) // 3

s |> Shelf.get(0) // Some("a")
```

A function can accept any `Container`:

```nomi
fn first<T>(container: Container<T>) -> Maybe<T> {
    Container.get(container, 0)
}

first(shelf) // Some("a")
```

Or **bound a type parameter** by the interface. Where an interface-typed parameter erases the concrete type, a bound keeps it — and the interface's type argument names the projected element:

```nomi
// `I` is some concrete iterator; `T` names its element type
fn saw_any?<T, I>(it: I) -> Bool where I: Iter<T> {
    // `each_while` answers False when a consumer stopped the source, so
    // refusing every element asks "did this source have anything?".
    Iter.each_while(it, fn _x -> False) == False
}

saw_any?([10, 20, 30]) // True — I = List<Int>, T = Int
```

This `<T>` is the same one the impl receiver names, seen from the other direction. When a type implements a generic interface once, the interface's type parameter is *determined* by the implementor, so the impl may leave it unwritten (`impl Iter for List<T>`, not `impl Iter<T> for List<T>`); a type that implements the interface at several type arguments writes them on each impl (§*Several instantiations for one type*). Where the parameter earns a name is where the implementor is *unknown*: an interface-typed parameter (`container: Container<T>`) or a bound (`I: Iter<T>`), abstracting over an arbitrary conformer and projecting its element type. The projection resolves by inference: `saw_any?([10, 20, 30])` pins `I = List<Int>` and `T = Int` from the argument, because List's one impl of `Iter` makes the element type a function of the implementor. A type parameter that only a bound and the result name is projected the same way, wherever the call sits: `plus(Day(10), Days(4))` for `fn plus<L, R, Out>(lhs: L, rhs: R) -> Out where L: Add<R, Out>` is a `Day`, because `impl Add<Days, Day> for Day` is Day's impl of `Add` at `Days`. When the subject has several impls, the call's arguments and expected type choose among them, and a bound they leave open is the error `` `Box` implements `Holds` at `Int` and `String`, and the call's arguments do not choose one for this bound: annotate them ``. This is the multi-parameter interface encoding of associated types: there is no separate associated-type declaration or projection syntax (no `I::Item`).

### Foreign Types

The same `impl Iface for Type { ... }` form can implement an imported interface for a local type, a local interface for an imported type, or any combination where the orphan rule permits it:

```nomi
import users.User

interface Html {
    fn to_html(value: self) -> String
}

impl Html for User {
    fn to_html(user: User) -> String {
        "<span>${user.name}</span>"
    }
}
```

Any file that can see the impl calls it through the implementing type or the interface — `User.to_html(user)` or `Html.to_html(user)`. Coherence guarantees at most one `Html` impl for `User` program-wide, so there is never anything to disambiguate by location.

A std generic container is an imported type like any other, so an interface of the module's own may be implemented for it: generically (`impl Show for List<T> where T: Show`), at a partly concrete instance (`impl Show for Map<String, V> where V: Show`) or at a concrete one (`impl Sized for List<Int>`). The call `Show.show([[1], [2]])` takes the `List<T>` impl at `T = List<Int>`, whose `T.show` takes it again at `T = Int`. A concrete instance beside a generic impl of the same interface over the same container is a duplicate impl. A std interface on a std container is an orphan impl, unless a type the module declares heads an input type argument (the orphan rule below).

An impl block is part of a program when its file is, and a file is part of a program when some file in the program imports it. Once it is, the impl applies everywhere in the program, including files that do not import its file. A file that holds only impls for types and interfaces declared elsewhere is brought in with an import that names nothing from it (`import html_impls`); that import is used when the program uses one of those impls (§*Unused imports are errors*). The standard library's impls are always part of the program.

**Orphan rule.** An `impl Iface for T { ... }` block must have either `Iface` or `T` declared in the *current Nomi module* (the directory tree rooted at `nomi.toml` + `go.mod`, per the module model in §14 and §31). A program outside any module is one module of its own, rooted at its project root (§26, *Project root discovery*), so `impl Debug for Bool` in a lone script is an orphan impl too. A third-party module that owns neither side cannot write the impl directly — the normal escape is a wrapper (newtype) the module owns. The whole-program collision check is the correctness backstop; the orphan rule is the hygiene layer that makes the common case of "two libraries both implementing `Display` for `Int`" unrepresentable.

**A local type argument.** An impl of a foreign interface for a foreign receiver is also allowed when a type the module declares heads one of the interface's type arguments, and that argument is an *input*. A unit library that declares `Rate` may write `impl Multiply<Rate, Decimal> for Decimal`, and then `d * rate` works wherever the impl is part of the program:

```nomi
import std/io

struct Rate {
    per_unit: Decimal
}

impl Multiply<Rate, Decimal> for Decimal {
    fn multiply(lhs: Decimal, rhs: Rate) -> Decimal {
        lhs * rhs.per_unit
    }
}

fn main() {
    rate = #Rate{per_unit: 4.25d}
    io.print(Decimal.to_string(12.5d * rate)) // 53.125
    io.print(Decimal.to_string(Decimal.multiply(2d, rate))) // 8.50
    io.print(Decimal.to_string(2d * 3d)) // 6: std's impl
}
```

A type argument is an input when the interface names its type parameter in a parameter type of one of its functions: `Rhs` in `Multiply<Rhs, Out>`, whose `multiply(lhs: self, rhs: Rhs) -> Out` takes it, and `T` in `Contains<T>`. One the interface names only in results, as `Multiply` names `Out`, is not. A type parameter named by both a parameter and a result is an input. The impl must also meet three conditions, which are Rust's (RFC 2451) narrowed to inputs:

- **The local type heads the argument.** `Rate` and `Rate<Int>` count; `List<Rate>` does not, since `List` is std's and std keeps the freedom to implement an interface generically over its own container in that position. The error says `` `Rate` is declared in this module but does not head the type argument `List<Rate>` ``.
- **No bare type parameter comes first.** Neither the receiver nor a type argument before the local one is a bare type parameter: `impl<T> Multiply<Rate, T> for T` would claim every type as its receiver, std's own included, and is the error ``its receiver is the bare type parameter `T` ``.
- **Not generic in that position.** `impl Contains<Tagged<T>> for List<T>` names no one concrete argument where the local type stands, and is the error ``the impl is generic in `Contains`'s type parameter `T` (`Tagged<T>`)``.

A local type in a result-only position is still an orphan impl: ``impl Multiply<Decimal, Rate> for Decimal`` is the error `` `Rate` stands in `Multiply`'s type parameter `Out`, which only results name ``, because it would compete for calls that already exist: an unannotated `m * 2d` would have two results to choose between. std's `Literal` admits no impl this way (``a typed literal's handler is chosen by its type alone``): a typed literal's handler is chosen by its type and not by an argument (§*Typed literals*), so a library's `impl Literal<Rate, ..> for Date` would take `Date"2024-01-01"` away from std's impl. Any other impl whose interface and receiver are both foreign is an orphan impl whose message says one of the three must be the module's own.

The rule keeps coherence because only the module that declares `Rate` can write an impl naming it: another module may import `Rate`, but `Rate` is not its own, so its `impl Multiply<Rate, Decimal> for Decimal` is an orphan impl. Two libraries can each add their own instantiation to one std interface and receiver, `Multiply<Rate, Decimal>` in one and `Multiply<Shares, Decimal>` in another, and both stand in one program beside std's `Multiply<Decimal, Decimal>`, because they name distinct concrete type arguments (§*Several instantiations for one type*). A call takes the impl its arguments choose, as for any receiver with several impls, and since the local type is an input, a new impl answers only calls that pass one. An impl generic in that type argument beside them is the overlap error of that section. When the generic impl is std's, the program's impl reports it, since std is checked once apart from every program: `impl Contains<Rate> for List<Rate>` beside std's `impl Contains<T> for List<T>` is ``overlapping impls: std's `Contains<T>` for `List` is generic in its type arguments, so it covers `Contains<Rate>`, which this impl implements``. Between two of the program's own impls the generic one reports it, as above. The cost falls on the owner of the interface and the receiver: once such impls exist, adding an impl generic in that position overlaps them.

## 14. Files and Imports

> **Terminology.** A **file** is a single `.nomi` source file — the
> unit of import, scope, and visibility (`pub`). A **file API object** is the
> qualifier bound by a path-only import, such as `request` from
> `import http/request`. A **Nomi module** is the directory tree rooted at
> `nomi.toml` + `go.mod` — what ships together and what the orphan rule
> operates on. Nomi modules are backed by Go modules for dependency mechanics;
> when this spec needs to refer to that Go fact explicitly it writes
> "Go module."
>
> **Module boundaries.** Within a Nomi module, file paths map to import
> paths, child files compose, and cyclic imports between files work.
> Across modules, an import prefixes the producing module's short name
> (declared in its `nomi.toml`), `internal/` directories enforce a
> sub-tree access barrier, and cross-module dependencies resolve through
> `go.mod`'s `require`/`replace`. A file may also prefix its own module's
> short name (`import my_app/models/user` inside `my_app`). An import that
> is the short name alone is an ordinary path: inside module `sqlite`,
> `import sqlite` names `sqlite.nomi` at the module root. From another
> module, a dependency's short name alone names the file of that name at
> the dependency's root: `import sqlite` is `import sqlite/sqlite` when
> module `sqlite` holds `sqlite.nomi`, and binds `sqlite` either way. A
> dependency's name wins over a file of the same name in the importing
> module. A module with no such file is imported by path (`import
> sqlite/driver`), and its short name alone names no file. Entry-point
> declarations live in `nomi.toml`'s `[module].entry_points`. The orphan
> rule (§13) applies at this boundary.

Everything lives in a file. A file is an import source, a visibility unit, and a file API object. The import path is derived from the file path — no file declaration needed. Exported items inside that file are top-level declarations (`pub fn`, `pub once`, `pub struct`, `pub enum`, etc.) or members of type/interface owner blocks. Importers either bind the file API object (`import http/request`, used as `request.get(...)`) or name selected exported items explicitly (`import http/request.get`, used as `get(...)`).

Inside a file, the final path segment is also available as that file's implicit file API alias when no top-level declaration already uses the name. In `std/float.nomi`, attached tests and implementation code can call `Float.to_string(...)` without importing `std/float`; in `main.nomi`, `fn main` owns the name, so no implicit `main` file alias is introduced.

```nomi
// File: users.nomi
pub struct User {
    name: String
    age: Int
}

pub fn new(name: String, age: Int) -> User {
    #User{name, age}
}
```

### File Naming

The file path is the import path. snake_case is used for file names:

- `users.nomi` → import path `users`
- `users/helpers.nomi` → import path `users/helpers`
- `http/request.nomi` → import path `http/request`

Slash-separated import paths map to directory structure. No declaration, no mismatch — rename the file to rename the import path.

### Referencing sibling modules

Files import other files by path. A path-only import binds the final path segment as a file API object; `as` may rename that qualifier locally:

```nomi
// File: http.nomi
import {
    http/request
    http/response as res
}

pub fn handle(req: HttpRequest) -> HttpResponse {
    parsed = request.parse(req)
    res.build_from(parsed)
}
```

### Import forms

**Path separator rule:** Import-path segments are separated by `/` (mirroring the filesystem layout under `std/` and any user package tree). A path-only import binds the final path segment as a file API object (`import http/request` binds `request`). A dot after the path imports a single selected item (`import task_parser.parse`). A brace selector after a path or owner imports several children from that left-hand side (`import std/maps.{self, Map}`, `import shape.Shape.{Circle, Rectangle}`). Braces select names from a file; a path cannot be grouped. `import std/{io, regex.Regex}` is a syntax error at the `{`, and several files go in an import block, one path per line. Between the path and the selected names, only PascalCase segments are owners, since an owner is a type: a lowercase segment written after a dot, with more after it, is a path segment, so `import std.io.print` is `import std/io.print`, and `nomi fmt` writes it with `/`.

**Two forms — bare statements and the block.** An import may be written as a bare per-line statement or, when several are imported together, grouped in a brace block:

```nomi
// Single file API import — binds `request`.
import http/request

// Single selected value import — binds `parse`.
import task_parser.parse

// Multiple imports — the idiomatic form is a brace block, newline-separated,
// one entry per line (the analog of Go's `import ( ... )`). No commas.
import {
    std/lists.List
    std/maps.{self, Map}
    http/request
}
```

Both forms are valid syntax, but **the formatter canonicalizes them**: consecutive imports with no blank line between them combine into a single sorted block; same-module statements merge (`import std/calendar.Date` + `import std/calendar.Months` → `import std/calendar.{Date, Months}`) unless the two bind a name twice, and the entries of a block written as one stay as written; a lone import stays bare. A **blank line between imports is the "keep separate" signal** — it's preserved as a group boundary, so distinct groups (stdlib vs. project) stay in distinct blocks. Comments inside a block are preserved. The block is therefore the canonical form for two or more grouped imports. Import-block entries are newline-separated; commas only separate children inside a `.{...}` selector. The block always renders multiline (it never collapses onto a single line).

```nomi
// Stdlib imports — use `std/` prefix (combined into one block)
import std/io

io.print("hello")

// Prelude types need no import
List.head([1, 2, 3])

// User file imports — bind a file API object or name exported items explicitly
import {
    auth
    http/request.get
}

get("https://example.com")

auth.validate(token)

// Specific imports — use unqualified names.
import users.{User, Role}

u = #User{name: "Alice", age: 30}

r = Role.Admin

// Bind a file API object and a selected type from the same file.
import std/supervisors.{self, Supervisor}

// Owner selector variant import — lift specific enum variants by selecting
// them through the enum type (PascalCase segment) at the import site.
// Required when bareness is wanted; flat selective variant imports are rejected.
import shape.Shape.{Circle, Rectangle}
import std/maybe.Maybe.{Some as Just, None as Nothing}

c = Circle(5.0) // bare-callable variant constructor

m = Just(42)

// `self` inside a brace list lifts the LHS of the brace group in
// addition to the named items. One line replaces the two-line
// "import the type, then select variants from it" idiom.
import std/calendar.Disambiguation.{self, Earlier, Later}
//     ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^  ^^^^^^^^^^^^^^^^^^^^^
//     select from Disambiguation;     self binds Disambiguation;
//                                     Earlier/Later lift as variants
//     same end state as:
//       import std/calendar.Disambiguation
//       import std/calendar.Disambiguation.{Earlier, Later}
// `self` can also bind the file API object while selecting items from it:
import std/calendar.{self, Date}
//     ^^^^^^^^^^^^  ^^^^^^^^^^
//     binds calendar; selects Date
// File-level functions can be imported bare with a dotted selector:
import task_parser.parse
//            ^^^^^
//            lifts parse as bare
import std/io.{self, print}

//             ^^^^  ^^^^^
//             binds io; lifts print as bare
command = parse("done 42")
```

Name collisions between specific imports are a compiler error.

- Unused imports are a compiler error (see *Unused imports are errors* below)
- `as` aliases work on imported items: `import std/maybe.Maybe.Some as Just`
- File API aliases use `as`: `import http/request as req`, then `req.get(...)`.
- Stdlib files use the `std/` prefix: `import std/io`, `import std/iter`. The `std/` prefix routes to the embedded stdlib filesystem and prevents user modules from shadowing stdlib names. `std` alone, like the short name of any module with no file of that name at its root, is a directory and names no file: `import std` and `import std.io` (which selects `io` from a file `std`) are errors whose hint spells `import std/io`.
- Variants must come through their enum. `import shape.Circle` (a flat selective variant import) is rejected with a clear error pointing at the owner selector form (`import shape.Shape.Circle`) or the qualified form (`import shape.Shape` then `Shape.Circle`). The owner selector path makes the originating enum visible at the import line, so cross-module collisions are caught at the import rather than silently shadowed.
- Every user file is preceded conceptually by the imports listed in `std/prelude.nomi`. That file pulls in the primitives (`Int`, `Float`, `Decimal`, `String`, `Unit`, `Infallible`, `Byte`, `Bytes`, `Codepoint`, `List`, `Vector`, `Map`, `Set`), `Bool` (with `True`/`False` variants) from `std/bool`, the `Maybe`/`Some`/`None` and `Result`/`Ok`/`Err` enums, the `Range` struct (so range literals like `1..=10` resolve everywhere), the core interfaces (`Add`/`Subtract`/`Multiply`/`Divide`/`Comparable`/`Display`/`Equatable`/`Hashable`/`Discrete`/`Iter`/`Steppable`/`Struct`/`Distinct`/`Summable`/`Contains`/`Variants`, plus universal `Debug`), `Ordering`, `Type`, and the application lifecycle types `Startup` and `Context`. Owner functions on those types need no import (`Iter.map`, `String.trim`, `Map.get`, `Result.ok?`). The prelude exports no file API objects, so a file-level function needs its file imported (`import std/io` for `io.print`), and an owner function on a type outside the prelude needs that type imported (`import std/assertions.AssertionFailure` for `AssertionFailure.format`). The `std/literals` cluster (`Literal`/`Fragment`) and niche/helper types such as `Assertable` and `AssertionFailure` are *not* preluded — their authors import them explicitly. The prelude exports no bare functions — bare names are types, interfaces, and variant constructors only. Anything not listed in the prelude must be imported explicitly. Stdlib files do not receive the prelude — they import their dependencies by hand. See §24 for details.
- Imports may appear inside a function body. A nested `import` binds names in the enclosing block's scope only and shadows any file-level binding of the same name. The file load itself is cached, so a nested import has no per-call cost beyond the first.

```nomi
fn shout(message: String) {
    import std/io.print

    print(String.to_upper(message))
}
```

Import statements must appear at the top of the file, before any declarations. Inside a function body, imports must precede any other statement, and nested function bodies follow that rule recursively. Mid-block imports are rejected with an analysis error to keep "what does this name mean?" answerable by reading the top of the enclosing scope.

### Cyclic imports between user files

Cyclic imports between user files are permitted at the type level. The analyzer and the runtime both pre-discover every reachable file from the entry file and register top-level declarations before any nested import recursively loads its dependencies, so a child file can freely import its parent (and vice versa) when the cycle is only used for type and function-signature references:

```
http.nomi           // imports http/request, http/response
http/request.nomi   // imports http.HttpRequest
http/response.nomi  // imports http.HttpResponse
```

This is the working layout for §37-style configuration patterns where a parent file both defines a shared type and dispatches to environment-specific child files that consume that type (see `tests/15-app-and-defer/config_pattern/`).

Real value cycles in `once` bindings — where forcing one binding transitively reads its own in-progress value — are still caught at evaluation time and reported as cyclic-binding errors (see §2).

### File Rules

- One import source per file, derived from the file path (no declaration)
- An import path that names no file is a compile-time error at the path, in every import form, whether or not the file uses the import: `import lib` with no `lib.nomi` reports ``no module `lib`: no file lib.nomi in this file's directory``. The message names the directory searched, relative to the importing file, and a hint names a sibling file the path is a misspelling of, or a file inside a directory the path names (`sub` is a directory, not a file). `import std/x` with no standard library module `x` is the same error. When `go.mod` requires a module the Go module cache does not hold (§31), the hint names it and says to run `nomi fetch`. When the path's first name is neither a file nor a directory there and is no misspelling of one, and the project's `go.mod` has no `require` line for a module of that name, the message says so too (``no module `csvdata`: no file csvdata.nomi in this file's directory, and go.mod has no `require` line for `csvdata` ``), with a hint to run `nomi add`: with the directory, when exactly one directory under the project root (three levels down at most) or beside it holds a Nomi module of that name (`` run `nomi add ../csvdata` to add the module in that directory ``), and with a module path or a directory otherwise; a `replace` of the name with no `require` gives `` go.mod replaces `csvdata` with ../csvdata but does not require it; run `nomi add ../csvdata` ``; a module `go.mod` requires whose directory has no `nomi.toml` is ``no module `csvdata`: go.mod replaces `csvdata` with ./macros/csvdata, which has no nomi.toml``. A declaration whose keyword the failed import would have brought reports nothing more. A selected name the file does not export is an error at the name: `file 'std/io' has no exported name 'nosuchname'`.
- The `/`-joined segments always name the file: `import utils/inner.Thing` needs `utils/inner.nomi`, and is not a lookup of `inner` inside `utils.nomi`.
- Files cannot be reopened or split across files
- Importing `http` does not import `http/request` — each must be imported separately. A facade file can flatten this for its own consumers by re-exporting the items they need (`pub import http/request.{Request, get}` in `http.nomi` lets consumers write `import http.{Request, get}`); it cannot re-export the file `request` itself (see §3 *Re-exporting imported names*).
- Visibility rules apply: declarations carrying `pub` are public, everything else is private (see §3)

### Unused imports are errors

An imported item that no visible text in the file uses is a compile-time error (an analyzer error: it fails `nomi run` and surfaces as the editor diagnostic). The granularity is the imported **item**: in `import std/comparable.{Comparable, Ordering}` with `Ordering` dead, the error names `Ordering` at its own position (`imported name 'Ordering' is unused — remove it from the import`); an aliased item is used through its alias; `self` in a brace list is its own item, used iff the parent name it binds is referenced. Any reference resolving to the imported name counts as a use — expressions, patterns, type annotations, `impl Iface for Type { ... }` blocks, `where` bounds (`where T: Comparable`), `derive Iface for Type` declarations, and typed-literal tags (`Date"..."` uses `Date`). Two carve-outs: a re-exported item (`pub import mod.{a}`) is used by definition, and names appearing only in comments or doc-comment ` ```nomi ` examples are not uses. Note that a dot-shorthand pattern (`.Some(v)`) resolves through the scrutinee's type, not through scope — so it does **not** keep a variant import alive. Nomi has no warning tier; the rule is an error by design (a later LSP quick-fix, not a softer diagnostic, is the iteration-friction mitigation). `fmt` does not auto-prune imports — layout is fmt's job, semantics are the compiler's.

An import of a whole project file (`import html_impls`) also puts that file's impl blocks into the program (§*Foreign Types*), so it is used when nothing in the file names it but the program uses one of those impl blocks: a call that resolves to one of its functions (`User.to_html(u)`, `Html.to_html(u)`) or a conformance the program requires of the `(type, interface)` pair it implements (a `where` bound, an interface-typed parameter, an operator, an interpolation). The use may be anywhere in the program, not only in the importing file, and it is decided once every file is checked. Two kinds of impl do not count: an impl for a type the imported file itself declares (a value of that type can only come from naming something in that file, so another import already brings the file in), and a call from inside the imported file to its own impl. An import of a standard-library file is never used this way, since the standard library's impls are always in the program.

### Re-importing a prelude name is an error

The prelude (§*Prelude*) already puts its exports in scope in every non-stdlib file, so `import std/iter.Iter` binds `Iter` to exactly the symbol the file already had. Writing it is inert text, and doing so is a compile-time error at the item's own position: `imported name 'Iter' is already in scope from the prelude — remove it from the import`. This is distinct from the unused-import error above — the import may be heavily *used* (`Iter.loop(...)`); what it cannot be is load-bearing.

The rule is derived from `prelude.nomi`, not a curated list, and only fires where the prelude is in scope. Four shapes are therefore never redundant:

- a whole-file import (`import std/strings`), which binds a file API object — the prelude deliberately exports none, so `strings.foo` needs it;
- an aliased item (`import std/iter.Iter as It`), which establishes a new name;
- a drill-through the prelude stops short of — `import std/iter.Iter.{loop}` binds bare `loop`, and `import std/comparable.Ordering.{Less}` binds bare `Less`; the prelude exports only the owners;
- a same-named item from a different module, since identity is compared through the resolved declaration rather than the spelling.

A `self` item under a prelude type (the `self` in `import std/maybe.Maybe.{self, None}`) binds that type and is redundant like the plain item.

Stdlib modules receive no prelude and are exempt by construction: their explicit imports are load-bearing.

Editors offer *Remove redundant prelude import* as a quick-fix, and the fix-all-on-save action prunes these alongside unused imports. The formatter does not: `nomi fmt` is layout-only and runs without analysis, while this rule needs the prelude scope.

### Explicit item imports

A path-only import binds the final path segment as a file API object. To bind one
exported item directly, append the item after a dot; to bind several, use a
brace selector:

```nomi
import {
    http/client
    http/client.get
    http/request.Request
}

req = Request.parse(raw)

client.timeout()

response = get(req)
```

`X.Y` has no file-lookup fallback. `X` is a value or an imported qualifier such
as a file API object, type, enum, struct, or interface. If a top-level function
is imported directly, it is called bare; if a file is imported path-only, its
members are reached through that qualifier.

### Member access is fail-loud

Accessing a member the value's resolved type does not have is a **compile-time error**, never a silent fallthrough that traps at runtime. The rule holds for every value kind, and you may access only the members the static type *guarantees*:

- **A value of a concrete type** — `p.field` on a struct, anonymous struct, primitive, list, map, enum, tuple, or function — must name a field that type declares. A missing field (a typo, `#Point{x, y}` accessed as `p.z`) is a compile error: `struct 'Point' has no field 'z'`. A function of the type is not a field: where `impl HasName for Person` writes `fn name`, `p.name` is `struct 'Person' has no field 'name'`, and its hint names the call, `Person.name(p)` or `HasName.name(p)`. (Tuples index by position: `pair.0`, `pair.1`. Indices chain left to right, so `nested.1.0` is `(nested.1).0`. An index is decimal digits with no leading zero; `pair.01`, `pair.1_0` and `pair.0x1` are parse errors.)
- **A value of interface type** (an existential) has no fields, because an interface declares functions only. `s.x` on a `Struct`-typed value is `interface 'Struct' has no field 'x'`. A value the interface requires is read through a function requirement, `HasName.name(n)` (§13 *Requiring a Value*). Only the interface's name qualifies its functions: `n.name` is `interface 'HasName' has no field 'name'` even though `HasName` declares `fn name`, bare, as a function value or called.
- **A value of type-parameter type** `T` has no fields, bounded or not. `x.name` on `T: HasName` is ``type parameter `T` has no field 'name'``; call the bound's function, `T.name(x)`.
- **A type qualifier** — `Type.member` — must name a real member of that type: an interface function, a constructor, or (for enums) a variant. A missing member is a compile error (`type 'List' has no member 'empty'`, `enum 'Maybe' has no variant 'Bogus'`), for both the call form `Type.member(...)` and a bare value `Type.member`. `empty` or `new` on `Map`, `Set` or `Vector` adds a hint naming the collection's empty literal (`write the empty map as %{}`). Cross-module members resolve too — an imported type's interface functions are visible to single-file analysis (the editor), so `User.to_string(user)` on an imported `User` is checked, not merely tolerated.

Only members the static type can prove present are reachable; an unresolved member is reported where it is written. The lone exception is a value whose type is still being inferred (an unresolved type variable) — that defers rather than erroring, since inference resolves it first.

Resolution rules per `X`:

1. **`X` is a value binding (local, parameter, `once`, or local function name):** `X.Y` is field access on the value of `X`. If `X`'s type has no field `Y` — including types like functions and primitives that don't support field access at all — the expression is an error ("not supported" or "no such field"), full stop. The compiler does not silently divert to a file of the same name, because file paths are not bound as values.
2. **`X` is an imported file API object, type, or interface name:** `X.Y` is qualified resolution (`Y` must be a public member of that imported container).

## 15. Type System

### Primitive Types

`Int` (64-bit integer), `Float` (64-bit IEEE 754 floating point), `Decimal` (arbitrary-precision, exact base-10), `Bool`, `String` (UTF-8, always), `Unit`. All primitive types implement `Display` and `Debug` — see §16 *Display and Debug* for the per-type rendering rules.

`Int` is the single integer type — 64-bit, signed, two's-complement. There are no sized (`Int32`) or unsigned (`UInt`) variants; this follows the one-`Int` simplicity of Elm and Gleam, with specific widths added only if a concrete FFI/interop need arises. `Int.min_value` / `Int.max_value` give the bounds. Bitwise operations are stdlib functions in `std/int` — `Int.bitwise_and` / `or` / `xor` / `not` and `Int.shift_left` / `shift_right` — rather than operators, matching the functional-language convention (Gleam `int.bitwise_*`, Elm `Bitwise.*`).

`Float` follows the IEEE 754 binary64 format. That includes the three special values **NaN**, **+Infinity**, and **-Infinity** — they are valid `Float` values (not separate types), produced by IEEE 754 arithmetic and constructable directly. See *Float Special Values* below.

`Decimal` is the third blessed numeric type: an arbitrary-precision, **exact base-10** number (mantissa × 10⁻ˢᶜᵃˡᵉ), for money and any value where `0.1 + 0.2 == 0.3` must hold (it does not in `Float`). It is the shopspring/`BigDecimal` family — but with the famous `BigDecimal` footguns removed. Like `Int` and `Float` it is a *blessed primitive*, not a stdlib type: it participates in the built-in arithmetic and comparison operators (§22). Key properties:

- **Exact-by-default.** `+ - *` never round — scale grows as needed (`1.50d + 1.5d == 3.00d`; `1.50d * 1.5d == 2.250d`). Only `Decimal.divide` and `Decimal.round` round, and they always name a `RoundingMode` explicitly — there is no hidden global precision/rounding context (rejecting Python's mutable `getcontext()` and Java's per-op `MathContext`).
- **No special values.** Unlike `Float`, `Decimal` has no NaN and no ±Infinity. Division by zero is a runtime error (like `Int`), not an Infinity.
- **Scale is display-significant, equality is value-based (Model C).** A `Decimal` remembers its scale for display — `1.50d` prints `"1.50"`, `Decimal.scale(1.50d) == 2` — but `==`, `Comparable`, `Hashable`, and pattern matching are *scale-insensitive*: `1.50d == 1.5d` is `True`, and the two are one `Map`/`Set` key. This is the same value-vs-representation split `Float` already has (`-0.0 == +0.0`, reflexive `NaN == NaN`), not a new principle. Two `==`-equal decimals remain observably distinguishable via `Decimal.scale` / display.
- **Literals** carry a `d` (or `D`) suffix, and the literal's text determines its scale: `1.50d` → scale 2, `1.5d` → scale 1, `5d` → scale 0. Underscore digit separators are allowed between two digits, as in Int and Float literals (`1_000.00d`; `1__0.5d` and `1_d` are errors); exponent notation is not. Runtime/parsed values come from `Decimal.from_string`.

The arithmetic, conversion, and shaping functions live in `std/decimal` (`from_int` / `from_string` / `to_string` / `to_int` / `to_float` / `from_float` / `divide` / `round` / `normalize` / `scale` / `abs` / `negate` / `sign` / `zero?` / `compare` / `min` / `max`), along with the `RoundingMode` enum (`Up | Down | Ceiling | Floor | HalfUp | HalfDown | HalfEven | Unnecessary`), display/debug/equality/hash/order impls, and the standard arithmetic operator impls. `Decimal.format` and `Decimal.format_percent` (with fraction-digit limits and a `RoundingMode`), `Decimal.parse_in` and `Decimal.parse_percent_in` format and parse it for a locale (§41). `Money` (`std/money`, §41 *Money*) pairs a `Decimal` amount with a `Currency` and builds on `Decimal` without changing it.

`Unit` is the type for functions that don't return a meaningful value. Can be omitted from function signatures:

```nomi
// Unit return can be omitted — these are equivalent:
fn log(message: String) {
    io.print(message)
}

fn log(message: String) -> Unit {
    io.print(message)
}
```

`Unit` is a real type that works in generics: `Result<Unit, String>`. It implements `Equatable` and `Hashable`: its one value equals itself and always hashes the same, so a tuple, struct or record holding `Unit` compares and hashes, and `Set<Unit>` and `Map<Unit, V>` work. It implements neither `Display` (rendering one is a compile error, see §16) nor `Comparable`.

`Infallible` is an uninhabited type — no values of type `Infallible` can ever be constructed. Used to indicate impossible cases, such as `Result<Unit, Infallible>` for operations that always succeed but need to satisfy a `Result`-returning API. Similar to Rust's `Infallible` / `!` type. It implements `Display`, `Equatable` and `Hashable`, whose functions can never be called, so `Display.to_string(r)`, `"${r}"`, `Hashable.hash(r)` and a bound such as `where T: Hashable` accept a `Result<Int, Infallible>`.

No `Char` type — single characters as text are strings of length 1. `Codepoint`
is the scalar-value type for low-level Unicode work, and an ASCII codepoint has
a literal, `'a'` (see "Codepoint literals" in §16). UTF-8 bytes are exposed as
immutable `Bytes`, whose elements are `Byte` values:

```nomi
String.to_bytes("café")
|> Iter.map(Byte.to_int)
|> Iter.to_list() // [99, 97, 102, 195, 169] — "é" is 0xC3 0xA9 in UTF-8

String.to_codepoints("é")
|> Iter.map(Codepoint.to_int)
|> Iter.to_list() // [233]
```

`Bytes` is distinct from text `String` and is the FFI boundary type for Go
`[]byte`. `Byte` is not a numeric type: convert explicitly with
`Byte.to_int` / `Byte.from_int` when integer arithmetic or display is wanted.
`Bytes + Bytes` concatenates buffers, matching `Bytes.concat`.

### Built-in Types

- `Maybe<T>` — `Some(value)` or `None`. Replaces null.
- `Result<T, E>` — `Ok(value)` or `Err(error)`. For fallible operations.
- `List<T>` — linked list. Primary collection type.
- `Map<K, V>` — key-value collection. Keys may be any *keyable* value — primitives, tuples, lists, structs, distinct types — not just primitives; keys match by Nomi's `==`, so e.g. a `#(Int, Int)` or a record can be a key. (A function value, an `Iter`, a handle to running state such as a `Task` or a `Sender`, or a value holding one has no equality (§30), so it can't be a key: a literal, call or pipe that builds such a `Map` or `Set` is a compile error.) Because key matching uses Nomi's `==`, Float keys behave consistently with it: `NaN` is retrievable (reflexive) and `-0.0` / `+0.0` are the same key. **A key of a declared type with a hand-written `impl Equatable` matches through that impl, at any depth** — as the key itself, or inside a tuple, a list, a struct field or an enum payload — so two values its `equal?` calls equal are one key, exactly as `==` would say. The bucket is chosen by the type's hand-written `impl Hashable` when it has one; a type whose `Equatable` is hand-written and whose `Hashable` is not hashes by its type alone, so equal values always share a bucket and `equal?` decides (correct by construction, at the cost of that type's keys sharing one bucket). Every other value — a derived impl included — keys by structural hash and structural equality, which is what a derived `Equatable`/`Hashable` answers. An `impl Hashable` must give equal values equal hashes; one that does not is the author's broken contract, as in Rust and Java.
- `Set<T>` — an insertion-ordered collection of unique values, backed by `Map` (elements may be any keyable value, same as map keys, and are unique under `==` by the same rule). Literal syntax: `%[1, 2, 3]`; empty sets need type context (`empty: Set<Int> = %[]`). `Equatable`/`Hashable` but not `Comparable`.
- `#(A, B, ...)` — tuples. Positional, fixed-size, heterogeneous. No single-element tuples: `(A)` is `A`.
- `#{name: A, age: B, ...}` — anonymous structs. Named fields, fixed-size, heterogeneous.
- `(A, B) -> C` — function type. `fn` defines functions, `(Args) -> Return` is the type.

All are immutable. Defined in detail in their respective sections (§7, §9, §19).

### Distinct Types

Nominal types are always distinct from any other type — including their underlying representation, when there is one. The type name acts as a conversion function for construction. Always named. Nomi has three keywords for declaring nominal types: `struct` (record types — §7), `enum` (tagged unions — §8), and `type` (the other distinct-type forms documented below). All three produce nominal, distinct types; this section covers specifically the `type`-keyword forms.

The body shape attached to the `type` declaration determines its kind:

- **Tuple-distinct** — `#(A, B)` (arity ≥ 2). Wraps a tuple.
- **Generic-distinct** — a generic type expression like `Map<K, V>`, `List<T>` or `Maybe<Int>`. Wraps a generic shape.
- **Primitive-distinct** — a bare type name like `Int`, `String`, or a declared struct, enum or distinct (`type Origin Point`, `type Twice Meters`). Wraps a single non-tuple type.
- **Zero-sized** — nothing after the name. A unique standalone tag. Equivalent to a bare enum variant in standalone form.

The wrapped type is the distinct type's representation, so it cannot contain the type itself, directly or through other distinct types: `type E E`, `type L List<L>`, `type M Maybe<M>` and the pair `type A B` / `type B A` are each an error at the declaration (`type 'L' is defined in terms of itself`). A recursive type is a struct or an enum, and a distinct type may wrap one: `struct Node { next: Maybe<Node> }` is legal, and so is `type Wrapped Node`.

Every distinct type that wraps a value satisfies the prelude interface `Distinct<T>` for exactly the type it wraps: `type Celsius Decimal` is a `Distinct<Decimal>`, `type Twice Meters` a `Distinct<Meters>` (not a `Distinct<Int>`), and a zero-sized type no `Distinct<T>`. Like `Struct` (§7), conformance is decided by the type, so `impl Distinct<Int> for X` is an error. `Distinct<T>` declares no functions. It is a bound (`fn tag<D>(d: D) -> String where D: Distinct<Int>`), and on an interface default (`where self: Distinct<T>`) it lets the body take the implementor apart with `self(p)` and build one with `self(x)` (§13, *Defaults over distinct types*).

Derives are sibling `derive Iface for Type` declarations. Distinct type names
may be dotted PascalCase paths (`type Civil.Days Int`) when the type belongs
under a real parent type; the full dotted name is the nominal identity.

```nomi
type Id Int
type Status Int

fn id_from_string(s: String) -> Result<Id, String> {
    ...
}

fn id_to_string(id: Id) -> String {
    ...
}

id = Id(42)

// destructuring — mirrors construction syntax
Id(n) = id // n is 42, type Int

Id(_) = id // assert type without binding
```

The call form takes the one value the type wraps, checked against the wrapped type, prefix or piped: `Id(42)` and `42 |> Id()` build an `Id`, and `Id("42")` is an error naming both (`Id wraps Int, so Id(...) takes an Int; got String`). A tuple-distinct's call form takes its one value too, the tuple: `Pair(#(1, "x"))`, or piped, `#(1, "x") |> Pair()`. Its flat call `Pair(1, "x")` is the error `Pair takes one argument, its tuple #(Int, String); got 2`, whose hint names the literal `#Pair(1, "x")`, which builds the type from its elements. `1 |> Pair("x")` is an error too (`Pair takes the piped value as its only argument, got 1 more`).

A distinct's value is read by destructuring, in any pattern position: a binding (`Id(n) = id`), a `case` arm, an `if` pattern, a binding with `else`, or a function or lambda parameter (`fn raw(Id(n)) -> Int`). Destructuring reads one level: for `type A Int` and `type B A`, `B(a) = b` binds an `A`. A file that reads a distinct's value often defines an accessor for it. Calling a type name is not a conversion and not an unwrap: `Int(id)`, `Int("4")` and `String(4)` are errors (`` `Int` is a type, not a function ``). On a distinct argument the hint names the destructuring to write instead (`Id(n) = id`); otherwise it names the conversion to call, `String.to_int` (a `Maybe<Int>`), `Int.to_float`, `Float.to_int`, or interpolation `"${x}"` for a String. Calling any other type name is an error too (`Dir(1)`, `Maybe(3)`, `Display(x)`); the type names with a call form are a struct (`Point(#{x: 1})`) and a distinct type. A pipe into a type name is the same call with the piped value first: a struct takes the piped record (`#{x: 1} |> Point()`), so `1 |> Point()` is an error (`Point expects an anonymous struct literal #{...} matching its fields, got Int`). An opaque struct or distinct (`Range`, `Instant`) has no call form outside its defining module, prefix or piped.

A distinct type that wraps a value, named without a call, is its constructor as a function value: `Id` is an `(Int) -> Id`, so `Iter.map(ns, Id)` builds an `Id` from each Int, `f = Id` binds the function, and `Id` passes where an `(Int) -> Id` is expected. A tuple-distinct's function takes the tuple (`(#(Int, String)) -> Pair`); §8 *Constructors as Function Values* states the rule for variants too. Any other type name is not a value, a `host type` such as `Map` or `List` included, wherever it stands (a binding, an argument, a statement): `f = Int`, `x = Point`, `m = Map` and `Iter.map(xs, String)` are errors (`` `Int` is a type, not a value ``). The other names that are values are an enum variant (`Dir.North`, and a positional variant as its constructor), a zero-sized type (`Expired`), `True`, `False` and `Unit`, and a type name where a `Type<T>` witness is expected (`Context.value(c, TraceId)`).

Distinct types can implement interfaces, just like structs and enums — write an `impl Iface for Type { ... }` block:

```nomi
type Id Int

impl Display for Id {
    fn to_string(id: Id) -> String {
        Id(n) = id
        Display.to_string(n)
    }
}
```

Distinct types can also be zero-sized — no wrapped type, just a unique name:

```nomi
type Expired

impl Display for Expired {
    fn to_string(_value: Expired) -> String {
        "token expired"
    }
}

```

A zero-sized type is its own one value and is written bare: `Expired`. It has no call form, so `Expired()` is an error (`Expired is a zero-sized type and takes no arguments; write Expired`).

Zero-sized distinct types can implement interfaces and be embedded in enums, just like any other distinct type. This is the standalone equivalent of a bare enum variant — use it when the same tag needs to appear in multiple enums or carry interface implementations.

Distinct types can wrap any type, including function types:

```nomi
struct Request {
    method: String
    path: String
    body: String
}

struct Response {
    status: Int
    body: String
}

interface Server {
    fn serve(handler: self, req: Request) -> Response
}

type Handler (Request) -> Response

impl Server for Handler {
    fn serve(handler: Handler, req: Request) -> Response {
        handler(req)
    }
}

```

Usage:

```nomi
handler = Handler(fn _req -> #Response{status: 200, body: "hello"})

response = Server.serve(handler, some_request)
```

Other types can also implement `Server` — for example, a struct with middleware:

```nomi
struct LoggingHandler {
    inner: Handler
}

impl Server for LoggingHandler {
    fn serve(logging: LoggingHandler, req: Request) -> Response {
        io.print("${req.method} ${req.path}")
        Server.serve(logging.inner, req)
    }
}
```

#### Distinct Types Over Tuples and Maps

Distinct types whose underlying type has a shape literal (tuple, list, vector, set, map, or struct body) support two construction forms: the **named literal** and the **call form** (parens around the inner value). Both are equivalent; pick whichever reads better. A named literal writes the type's name right after its shape's symbol, `#` for a tuple, vector or struct and `%` for a map or set: `#Pair(1, "x")`, `#Path[1, 2]`, `%Kvs{"a" => 1}`, `%Tags["a"]`, `#User{name}`. A list has no symbol, so its named literal is the name and the brackets, `Ids[1, 2]`. A named literal always builds the type it names, never the underlying shape, wherever it stands: `Ids[1, 2]` is an `Ids` even where a `List<Int>` is expected (a type mismatch there), and a name that names no type is an error. The same holds for a variant whose payload has a shape literal (`Json.Arr[...]`, `%.Obj{"k" => v}`, `#.Items[1, 2]`).

The std collections have only the unnamed literals, `#[1, 2]`, `%[1, 2]` and `%{"a" => 1}`, which are literal syntax, not calls, since `Vector`, `Set` and `Map` are reserved names. The parser knows the shape from the symbol and the token after the name, so nothing is decided by what the name is: `#User{}` is always a struct, built from its field defaults, and `%Kvs{}` and `%Json.Obj{}` are always the empty map of that type. A name followed by `{` or `[` with no symbol before it never starts a map or a struct: `Kvs{"a" => 1}` and `Point{x: 1}` are parse errors at the `{`, and `Vector[1]` is a list literal naming a type that is not a list type. A spread has no named form: `%Kvs{..base, "a" => 1}` is `map spread \`..\` has no named form; write \`%{..base, key => value}\``, and so for the other shapes (`#{..base, field: value}`, `#[..base, item]`, `%[..base, item]`).

| Distinct over | Named literal | Call-form |
|---|---|---|
| `type Id Int` | (no parallel — primitive has no shape literal) | `Id(42)` |
| `type Pair #(Int, String)` | `#Pair(1, "x")` | `Pair(#(1, "x"))` |
| `type Ids List<Int>` | `Ids[1, 2]` | `Ids(some_list_value)` |
| `type Path Vector<Int>` | `#Path[1, 2]` | `Path(some_vector_value)` |
| `type Tags Set<String>` | `%Tags["a", "b"]` | `Tags(some_set_value)` |
| `type Kvs Map<String, Int>` | `%Kvs{"a" => 1, "b" => 2}` | `Kvs(some_map_value)` |
| `struct User` (fields `name: String`, `age: Int`) | `#User{name: "Alice", age: 30}` | `User(#{name: "Alice", age: 30})` |
| `type Expired` (zero-sized) | `Expired` (no parens) | `Expired` (no parens) |

Tuple-distinct types require **arity ≥ 2** — `type Wrap (Int)` is treated as `type Wrap Int` (wrapping a single non-tuple type), since 1-tuples don't exist in Nomi.

```nomi
type Pair #(Int, String)
type Triple #(Int, Int, Int)
type Kvs Map<String, Int>

p1 = #Pair(1, "hello") // named literal — the elements

p2 = Pair(#(2, "world")) // call-form — explicit inner tuple

t = #Triple(1, 2, 3) // arity 3

kv1 = %Kvs{"a" => 1, "b" => 2} // named literal — inline map literal

kv2 = Kvs(some_existing_map) // call-form — wrap an existing value
```

Construction errors at the call site:

```nomi
Pair(1) // error: Pair wraps #(Int, String), got Int

Pair(1, "x") // error: Pair takes one argument, its tuple #(Int, String); got 2

#Pair("x", 1) // error: element 1 of Pair: expected Int, got String
```

Patterns mirror construction — see §10 "Distinct Type Destructuring" for `case` syntax.

#### Opaque distinct types

A distinct type can be declared `pub opaque type <name>`, which exposes the *type name* while keeping its *construction surface* (constructor, destructuring, field access, and an update patch naming a field) private. Outside callers can refer to the type, pass values around, and call interface functions, but they cannot inspect or build values directly — they go through the owning file's public smart constructors and accessors.

```nomi
// positive.nomi
pub opaque type PositiveInt Int

pub fn positive(n: Int) -> Maybe<PositiveInt> {
    if n > 0 { Some(PositiveInt(n)) } else { None }
}

pub fn to_int(PositiveInt(n)) -> Int {
    n
}
```

Inside `positive.nomi`, every form of the construction surface works — that's how `positive` and `to_int` do their job. From any other file, the boundary blocks four operations:

| Outside the owning file | Status |
|---|---|
| Use in signatures, return types, generic args, field types | **Allowed** — `fn process(p: PositiveInt) -> PositiveInt`, `List<PositiveInt>`, etc. |
| `PositiveInt(5)` (constructor) | **Rejected** — go through an exported constructor |
| `PositiveInt(n) = p`, `case p { PositiveInt(n) -> ... }` (destructuring) | **Rejected** — go through an exported accessor |
| `u.name` (field access on an opaque struct) | **Rejected** — go through an exported accessor |
| `Struct.update(u, #{name: "x"})` (a patch naming an opaque struct's field) | **Rejected** — go through an exported constructor |

`==` and `!=` on opaque values still work — Nomi's structural equality runs through the runtime's value comparison, never through user-visible code. Calling interface functions (`Display.to_string(p)`) works whether the impl lives in the owning module or another module — dispatch is on the type, and the impl body decides what to show.

**Opacity restricts operations, never conformance.** An opaque type satisfies every interface a transparent one would — `Struct`, `Debug`, `Display`, `Equatable` — and it satisfies them at bounds and existentials too (`fn dump(s: Struct)` accepts an opaque struct). Only the operations in the table above are closed, and only from outside the defining file. Two consequences worth naming, because the alternative reading looks equally defensible:

- **The universal interfaces stay universal.** `Struct` is satisfied by every struct value (§7), `Distinct<T>` by every distinct type over `T`, and `Debug` by every type, with no opacity carve-out in any. What an opaque type implemented outside its file does not get is an interface default that takes `self` apart or builds one (`self(p)`, `self(x)`, §13); that impl writes the function. Withdrawing `Struct` conformance from an opaque struct would be the other way to close the `Struct.update` hole, and it would also withdraw the bound and the existential, which name no field and expose nothing.
- **The auto-`Debug` default is the model.** An opaque type with no explicit `Debug` gets a name-only `"<opaque T>"` body rather than the structural one. Conformance is untouched; the *behaviour* changes because the structural body would leak the representation. `Struct.update` is the same move on the same reasoning. The one place they differ: `Debug`'s adjustment is unconditional — `<opaque T>` renders identically inside and outside the defining file, because a synthesized impl body is fixed at the declaration and has no call site to consult — whereas every operation in the table is *positional*, allowed inside the defining file and rejected outside.

**Allowed forms.** `opaque` can wrap any distinct type with a representation:

- **Primitive-distinct:** `pub opaque type PositiveInt Int`
- **Struct:** `pub opaque struct Date { ... }` (with `year: Int` etc. in the body) — the field set IS the representation, equivalent to wrapping an unnamed inner struct.
- **Enum:** `pub opaque enum Status { ... }` (with variant lines in the body) — the variant set IS the representation, equivalent to wrapping an unnamed sum type.

The `opaque` qualifier sits between `pub` and the type-introducing keyword (`struct`, `enum`, or `type`), and applies only to the type declaration it precedes — `pub opaque type A Int` is opaque, while a separate `pub type B Int` is a normal distinct type.

**Rejected applications** (compile error at the declaration site): functions, type aliases, interfaces, `once` bindings, zero-sized distinct types (no representation to hide), foreign types (opacity is the *defining* file's choice), and built-in types.

Because outside callers can't reach the representation, an opaque distinct type's wrapped type may be private — see §3 *Visibility Consistency*'s relaxation. This enables the "public handle, private internals" idiom (OCaml signatures, Haskell newtype with private constructor):

```nomi
// private — no `pub` modifier
struct InternalUserData {
    id: Int
    label: String
}

pub opaque type UserHandle InternalUserData // public type, private internals

pub fn create(id: Int, label: String) -> UserHandle {
    UserHandle(#InternalUserData{id, label})
}

pub fn label(h: UserHandle) -> String {
    case h {
        UserHandle(d) -> d.label
    }
}
```

**Inline struct body — preferred when the wrapped representation has no other use.** The field set IS the representation; no separately-named inner struct is needed. From outside the defining file, the type is opaque exactly as in the wrapper form: callers cannot construct values directly with `#Date{...}`, cannot destructure with `case`, and cannot read fields with `d.year`. Inside the defining file, fields are reached directly without a `case` step.

```nomi
// Inline struct body — equivalent to defining an unnamed inner struct
// and wrapping it. The Date opaque type's representation is hidden from
// outside callers (no field access, no destructuring) but year/month/day
// can be reached via exported accessors.
pub opaque struct Date {
    year: Int
    month: Int
    day: Int
}

pub fn year(d: Date) -> Int {
    d.year
}

pub fn month(d: Date) -> Int {
    d.month
}

pub fn day(d: Date) -> Int {
    d.day
}
```

**Inline enum body — opacity is strict, and the API surface is heavier.** Outside the defining file, the variants are unreachable for construction, pattern matching, *and* payload access — callers get pass-around, equality, and interface functions only. The owning file therefore typically exports a smart constructor per variant, a predicate per variant, and a `Maybe<T>`-returning accessor for any payload-carrying variant, because outside callers can't `case` on the value themselves.

```nomi
pub opaque enum Status {
    Active
    Inactive String
}

pub fn active() -> Status {
    Status.Active
}

pub fn inactive(reason: String) -> Status {
    Status.Inactive(reason)
}

pub fn is_active(s: Status) -> Bool {
    case s {
        .Active -> True
        _ -> False
    }
}

pub fn inactive_reason(s: Status) -> Maybe<String> {
    case s {
        .Inactive(r) -> Some(r)
        _ -> None
    }
}
```

Reach for opaque enum when hiding the variant set is the goal — API stability across variant churn, invariant-enforced state-machine transitions, or representation flexibility (enum today, struct tomorrow). Most enums should stay plain `pub`: `Result<T, E>`, `Maybe<T>`, `ParseError`, `Bool` work *because* exhaustive pattern matching is the whole point. This is the ML-family idiom (Haskell module exports, OCaml signatures); systems languages without it push you to the struct-with-private-state workaround instead.

The compelling pay-off: opaque types let invariants live in the type system. `Int.divide(x: Int, y: NonZeroInt) -> Int` is **total** — the type rules out `y == 0` at the call site, so the function doesn't need a `Maybe` return. The fallibility moves to the validation step (`Int.to_non_zero(y) -> Maybe<NonZeroInt>`), which is exactly where it belongs.

### Type Aliases

A `typealias` creates a transparent synonym for an existing type. The alias and the original type are fully interchangeable — no wrapping or unwrapping needed:

```nomi
typealias UserResults Map<String, List<Result<User, Error>>>

results = %{"alice" => [Ok(user)]}

Map.keys(results) // works — UserResults IS Map<String, ...>
```

An alias may name any type the file sees, including an alias declared below it. An alias whose target names the alias itself, directly or through other aliases, is an error at each alias on the cycle (`typealias 'X' refers to itself (X → Y → X)`), since the target would be infinite; a recursive type is a struct or an enum.

Type aliases can also be generic:

```nomi
typealias Lookup<V> Map<String, V>

fn get_ages() -> Lookup<Int> {
    %{"Alice" => 30}
}
```

> **Status: not yet implemented.** Non-generic aliases work end-to-end.
> Generic aliases — `typealias Name<T> ...` — are a hard parse error at the
> `<`: `expected type name, got LT`. Track via `feature-status.md`.

Use `typealias` for readability shortcuts. Use `type` when you want a new, incompatible type with its own identity:

| | `type Id Int` | `typealias Name String` |
|---|---|---|
| New type? | Yes — `Id` ≠ `Int` | No — `Name` = `String` |
| Conversion | Explicit: `Id(42)`, `Id(n) = id` | None needed |
| Can implement interfaces? | Yes | No (implement on the underlying type) |
| Use case | Type safety, domain modeling | Abbreviating complex types |

### Type Annotations

Required on function signatures, inferred within function bodies:

```nomi
fn add(x: Int, y: Int) -> Int {
    result = x + y // type inferred as Int
    result
}

names = ["Alice", "Bob"] // inferred as List<String>

ages = %{"Alice" => 30} // inferred as Map<String, Int>
```

Annotations are also accepted, optionally, on bindings (`name: Type = value` — see §2), struct fields, function parameters, and lambda parameters. On a binding the annotation drives bidirectional inference into the right-hand side and is a checked assertion against the inferred type. Destructuring patterns do not yet accept annotations.

A lambda parameter with no annotation and no default takes its type from the function type its position expects: a parameter's declared type at a call, a function-typed binding annotation, a declared return type. Where no function type is expected, the parameter is the error "cannot infer type for parameter x", and a later use of the lambda does not type it. That covers a lambda bound to a name with no annotation (`f = fn x -> x`), a tuple or list element, and the argument of a generic constructor with no expected type (`h = Some(fn x -> x)`, `h = #Box{v: fn x -> x}`), whose type parameter the lambda would itself have to fix. Annotate the parameter (`Some(fn x: Int -> x)`) or the binding (`h: Maybe<(Int) -> Int> = Some(fn x -> x)`).

### Generics

Angle bracket syntax. Type parameters are inferred at call sites.

Generic types:

```nomi
struct Pair<A, B> {
    first: A
    second: B
}

p = #Pair{first: 1, second: "hello"}
```

Generic functions:

```nomi
fn first<A>(list: List<A>) -> Maybe<A> {
    List.head(list)
}
```

A function's type parameters are solved at each call from its arguments and
the type its result is expected to have, so each one must appear in a
parameter's type, in the return type, or in a `where` bound on a type
parameter that does (`fn any?<I, T>(items: I) -> Bool where I: Iter<T>`). One
that appears nowhere else is a compile error at its declaration: in
`fn f<U>(x: Int) -> Int`, ``type parameter `U` is not used in the signature of
`f`, so no call can determine it``. A `where` bound alone does not count.

A function in an inherent `impl Span<T>` block may use the block's `T`
directly (`fn longer?(span: Span<T>, other: Span<T>) -> Bool`) or declare its
own, which shadows the block's (`fn first<T>(span: Span<T>) -> T`). Either way
the rule above applies to the parameters the function declares.

The block's `T` is optional. A function whose signature does not name it
(`fn seven() -> Int`, `fn twice<U>(x: U) -> #(U, U)`) is compiled without it, and
a call to it names no instance (`Span.seven()`), as std's `Iter.from(3)`
names no `Iter`. Its body may not name the block's `T` either: in a type
annotation, a turbofish, a lambda parameter or a `T.member` qualifier it is
the error
`` `T` is `Span`'s type parameter, and `seven`'s signature does not determine it ``
at that use. The same holds for an inherent block on an interface
(`impl Iter<T> { ... }`).

Generic type usage:

```nomi
List<String>
Maybe<Int>
Result<User, String>
```

Each generic function is compiled once per tuple of type arguments it is
called with. A generic function that reaches itself, directly or through
other generic functions, at a type built from its own type parameter
(polymorphic recursion) would need infinitely many copies, so it is a
compile error at the call that grows the type:

```nomi
fn grow<T>(x: T, n: Int) -> Int {
    if n == 0 {
        0
    } else {
        grow(#Box{inner: x}, n - 1)
    } // T = Box<T>: an error
}
```

Recursion at the same type parameters, at a concrete type, or with the
parameters permuted is finite and allowed. Rust, C++ and Go reject the same
shape (Go reports an "instantiation cycle").

## 16. Strings

Opaque type, worked with through `String` owner functions. No `Char` type —
single characters as text are strings of length 1; `Codepoint` is the
scalar-value type for low-level Unicode work, with a single-quoted literal for
ASCII (`'a'`, see "Codepoint literals" below). Handles Unicode correctly.

```nomi
String.length("café") // 4 (not 5 — é is one grapheme)

Iter.to_list("café") // ["c", "a", "f", "é"] — a String iterates by grapheme

String.length("👨\u{200d}👩\u{200d}👧") // 1 (family emoji is one grapheme)

Iter.to_list("héllo") // ["h", "é", "l", "l", "o"]

String.slice("café", 0, 3) // "caf"

String.contains?("héllo", "éll") // True

String.to_bytes("café")
|> Iter.map(Byte.to_int)
|> Iter.to_list() // [99, 97, 102, 195, 169] — UTF-8 bytes

String.to_codepoints("café")
|> Iter.map(Codepoint.to_int)
|> Iter.to_list() // [99, 97, 102, 233] — Unicode scalar values

String.to_codepoints("👨\u{200d}👩\u{200d}👧")
|> Iter.map(Codepoint.to_int)
|> Iter.to_list() // [128104, 8205, 128105, 8205, 128103]
```

`String.length` counts graphemes (what humans see). `String.to_bytes` and
`String.to_codepoints` are available when you need the underlying representation.

**Equality is byte-based — no implicit normalization.** `==`, hashing, and ordering compare the raw UTF-8 bytes (ordering is byte-lexicographic, which equals Unicode-scalar order). So two visually identical strings in different Unicode forms — a precomposed `"é"` (U+00E9) and a decomposed `"e"` + U+0301 — are *not* `==`, even though both are one grapheme. This matches Python/Go/Rust/JS (only Swift normalizes implicitly). When you need form-insensitive comparison, normalize first with `String.normalize(s, form)` (forms `NFC` / `NFD` / `NFKC` / `NFKD`).

### String Literals

Two forms:

**Single-line strings** — escapes and interpolation:

```nomi
"Hello, ${name}!\n"
```

A string literal of any form, raw and typed ones included, holds UTF-8 text. A byte in its body that is not part of a UTF-8 sequence is a compile error: no escape writes one, so `String` stays UTF-8.

Escape sequences: `\n` (newline), `\t` (tab), `\\` (backslash), `\"` (quote), `\u{1F600}` (Unicode scalar value — braces required, 1–6 hex digits; must be ≤ U+10FFFF and not a surrogate U+D800–U+DFFF). These are the *only* escapes; any other backslash sequence (e.g. `\q`, a brace-less `\uXXXX`, `\u{}` or `\u{0000041}` with no digits or more than six, `\u{110000}` above U+10FFFF, or a surrogate `\u{D800}`) is a compile error rather than being silently kept as literal text or coerced to U+FFFD. `${expr}` interpolates an expression, and `\${` writes a literal `${`. That pair is the only special one: braces, `$`, `#` and `#{` need no escape.

**Triple-quoted strings** — multi-line, raw (no escapes), interpolation still works:

```nomi
query = """
    SELECT *
    FROM users
    WHERE name = '${name}'
    """
```

- No escape processing — backslashes are literal
- Interpolation with `${}` still works; `\${` writes a literal `${`, the one backslash sequence a triple-quoted string reads

**Indentation rules** (applied in order):

1. **Leading-newline strip**: If the opening `"""` is immediately followed by a newline, that newline is removed.
2. **Indentation baseline**: The strip baseline is the **minimum leading whitespace across all non-blank body lines and the closing-delimiter line**. That prefix is removed from the start of each line. Blank (empty or whitespace-only) lines are excluded from the minimum calculation; their content is stripped of whatever leading whitespace they have, up to the baseline length. This matches Java text blocks (JEP 378), Swift multi-line strings, Python `textwrap.dedent`, and Kotlin `trimIndent`.
3. **Trailing-newline strip**: The newline immediately before the closing `"""` is removed.

These rules apply to triple-quoted strings whether or not the body contains interpolation.

For example:

```nomi
q = """
    SELECT *
    FROM users
    """
```

All non-blank body lines and the closing-delimiter line are at four spaces, so the baseline is 4. The body is `"SELECT *\nFROM users"` — the leading newline after the opening `"""` is stripped, the four-space baseline is removed from each line, and the newline before the closing `"""` is stripped.

When body lines have different indents, the **shortest** non-blank line wins:

```nomi
sql = """
    SELECT *
      FROM users
    """
```

`SELECT *` is at 4 spaces, `FROM users` is at 6, the closing-delimiter line is at 4. The minimum is 4, so 4 spaces are stripped from each line: the body is `"SELECT *\n  FROM users"` — `SELECT *` flush left, `FROM users` keeps its 2 extra spaces of relative indent. Lines indented *less* than the closing-delimiter line don't have leading whitespace silently leak into the value; they re-anchor the baseline instead.

**Literal `${`**: Write `\${` for a literal `${...}` in a string. `"\${HOME}"` produces the text `${HOME}` — useful for shell snippets and templating-language content. A `$` not followed by `{` is ordinary text, so `$5` and `$$` need nothing; `$${n}` is a `$` followed by an interpolation.

**Hashes**: `#`, `##` and `#{` are ordinary text in a string. `#{...}` is a struct literal in code (§7) and nothing inside a string.

**Interpolation dispatches `Display`**: `${expr}` calls `Display.to_string(expr)` at runtime. The value's type must implement `Display` (via an `impl Display for T { ... }` block or `derive Display`); otherwise interpolation errors. See *Display and Debug* at the end of §16 for the full rule table.

### Raw strings

A backtick-delimited literal opens a **raw string**:

- `` `...` `` — single-line raw
- A backtick literal spanning multiple lines — multi-line raw

**Semantics** — inside a raw string, the body is verbatim text:

- `${...}` is **not** parsed as interpolation; the characters `$`, `{`, ..., `}` are literal.
- `\${` is not an escape; the backslash stays.
- Backslashes are literal (same as triple-quoted non-raw).

For multi-line raw strings, the indentation rules from the previous subsection still apply: leading-newline strip, min-indent baseline strip, trailing-newline strip.

**When to use it** — embedded DSLs with literal backslashes or interpolation markers: regex patterns such as `\d{3,4}`, and shell scripts or templates containing `${...}`, which a quoted string would read as interpolation unless written `\${`.

```nomi
regex = `\d{3,4}` // literal backslash-d, literal braces

shell = `
    for f in *.sh; do
        echo "${f%.sh}"
    done
    `

// ${f%.sh} preserved verbatim
template = `price tag: $${PRICE}` // literal $$ {PRICE}
```

**Raw strings cannot contain a backtick** — the body terminates at the first bare backtick. Variable-length delimiters along the lines of Rust's `r#"..."#` are a known future gap.

Backticks also compose with typed literals (for example, `` Regex`\d+` `` and multi-line `Bash` raw literals). Because a raw body is always fixed text, a backtick typed literal whose handler can fail is checked at compile time and has the handler's success type: `` Regex`\d+` `` is a `Regex`, and `` Regex`[` `` is a compile error. See "Typed literals" below.

### Codepoint literals

A single-quoted literal is a `Codepoint`, the scalar value of one ASCII character:

```nomi
'a' // Codepoint 97

'\n' // Codepoint 10

'\'' // the single quote, Codepoint 39

'\u{7F}' // DEL, Codepoint 127

'a'..='z' // the 26 lowercase letters, as a Range<Codepoint>

'0'..'9' // the digits 0 through 8

case cp {
    '(' -> Open
    ')' -> Close
    ' ' -> Space
    '\t' -> Space
    _ -> Other
}
```

The literal is infallible: its type is std's `Codepoint` whatever the file has in scope. `Codepoint` is in the prelude, so naming the type needs no import either. It is not text: `"a" == 'a'` is a type error (String vs Codepoint), and `Codepoint.to_string('a')` or `"${'a'}"` turns one into a one-character string. It compares, orders and hashes as a `Codepoint`, so `cp == ','` and `cp >= 'a'` work, and a range of codepoint literals iterates (`Codepoint` is `Discrete`). It is a pattern too, at the top of a `case` arm or inside a variant, tuple or struct pattern; a `case` on a `Codepoint` whose arms test literals needs a `_` arm. There are no range patterns, for codepoints or for `Int`.

**ASCII only (U+0000–U+007F).** For ASCII the codepoint, the UTF-8 byte and the grapheme are the same thing, so `'a'` cannot mean anything other than what it looks like. Above 127 they come apart: `é` is one grapheme, but it is one codepoint (U+00E9) precomposed or two (`e` + U+0301) decomposed, and two UTF-8 bytes either way. A literal there would have to pick one reading, so text stays a `String` (`"é"`), and another scalar value comes from `Codepoint.from_int(0xE9)`, which returns a `Maybe` because it validates.

**What a Codepoint offers.** Besides the literal, `std/code_points` gives `Codepoint.from_int` and `Codepoint.to_int`, `Codepoint.to_string` (`Display`), `next`, `steps_between` and `step_by` (`Discrete`, `Steppable`), and six classifiers:

| Function | Answers |
|---|---|
| `Codepoint.letter?(cp) -> Bool` | Unicode general category L (Lu, Ll, Lt, Lm, Lo), Go's `unicode.IsLetter`. `é`, `ж` and `中` are letters; U+0301 COMBINING ACUTE ACCENT is not. |
| `Codepoint.digit?(cp) -> Bool` | ASCII `'0'` through `'9'` only. Arabic-Indic `٣` and full-width `１` are not digits here. |
| `Codepoint.to_digit(cp) -> Maybe<Int>` | `Some(0)` through `Some(9)` for ASCII `'0'` through `'9'`, `None` for everything `digit?` rejects. |
| `Codepoint.whitespace?(cp) -> Bool` | The Unicode White_Space property, Go's `unicode.IsSpace`: `' '`, `'\t'`, `'\n'`, U+00A0, U+3000 and the rest. |
| `Codepoint.upper?(cp) -> Bool` | General category Lu, Go's `unicode.IsUpper`. |
| `Codepoint.lower?(cp) -> Bool` | General category Ll, Go's `unicode.IsLower`. |

`digit?` and `to_digit` are ASCII-only on purpose: a phone number, a checksum or a number to parse means ASCII digits. There is no function for Unicode decimal digits (category Nd). A title-case letter such as `ǅ` (U+01C5, category Lt) is a letter but neither `upper?` nor `lower?`, and so is a letter without case, such as `中`. The Unicode answers come from the Go toolchain's tables, so they follow its Unicode version.

```nomi
String.to_codepoints("Call 555-0134")
|> Iter.map(Codepoint.to_digit)
|> Maybe.values() // [5, 5, 5, 0, 1, 3, 4]
```

**Escapes.** The 95 printable ASCII characters are written directly, except the single quote and the backslash, which are written `'\''` and `'\\'`. A control character (U+0000–U+001F, U+007F) has to be escaped. The escapes are the string escapes with the literal's own delimiter in place of the string's: `\n`, `\t`, `\\`, `\'` and `\u{...}` (braces, 1–6 hex digits, at most `7F` here). There is no `\r` or `\0`, as in strings: write `'\u{D}'` and `'\u{0}'`. Each literal escapes only its own quote: `\"` is an error in a codepoint literal (write `'"'`), and `\'` is an error in a string (write `"it's"`).

These are compile errors, each naming the literal: an empty `''`; more than one character (`'ab'`, which suggests `"ab"`); a character or `\u{...}` above U+007F (`'é'`, which suggests `"é"` or `Codepoint.from_int`); a control character written directly; an unknown escape; and a literal with no closing quote on its line.

### Typed literals

A **typed literal** is a string-shaped expression whose body is processed by its prefix *type*'s handler to produce a typed value. Use them where a plain string would lose information or invite injection bugs: parameterised SQL (`Sql"SELECT * FROM users WHERE id = ${id}"`), escape-aware HTML (`Html"<p>${input}</p>"`), typed dates (`Date"2026-05-04"`), regex, and any other embedded DSL whose contents deserve a type stronger than `String`.

The **prefix is a type** — a PascalCase name in scope (`Date`, `Sql`, `Html`) whose `Literal` impl backs the literal. A `<Type>"..."` site desugars to the type-qualified call `<Type>.from_fragments([...])`, so it is the ordinary interface dispatch the rest of the language already uses (coherence, the orphan rule, and the dispatch path all apply unchanged).

**Surface forms.** Regular typed literals use quoted or triple-quoted strings; raw typed literals use backticks:

```nomi
Date"2026-05-04" // single-line, interpolated

Date"""
    2026-05-04
    """

// triple-quoted, interpolated
Regex`\d{3,4}` // single-line, raw

Bash`
    for f in *.sh; do
        echo "${f%.sh}"
    `
// multi-line, raw
```

The type name sits **immediately adjacent** to the opening delimiter — no whitespace between the name and `"` / `"""` / backtick. The prefix is PascalCase only: a lowercase identifier adjacent to a literal opener is an ordinary identifier next to a literal, not a typed literal.

**The `Literal` interface.** A type backs its literal by implementing the compiler-known `Literal` interface (imported from `std/literals`, alongside `Fragment`):

```nomi
pub opaque struct Date {
    // ...fields...
}

impl Literal for Date {
    fn from_fragments(fragments: List<Fragment<String>>) -> Result<Date, ParseError> {
        ...
    }
}

type Sql

impl Literal for Sql {
    fn from_fragments(fragments: List<Fragment<Display>>) -> Query {
        ...
    }
}
```

- The prefix is the type's own name; it appears at use sites as `<Type>"..."`.
- `from_fragments` is supplied by an implementation of the compiler-known `Literal` interface for the prefix type. It is reached through typed-literal syntax (`Sql"..."`), not called as an ordinary file function.
- The contract is `from_fragments(List<Fragment<I>>) -> R` with the impl free to choose `I` (the per-slot value interface) and `R` (the literal's static type). `Literal` is declared `interface Literal<I, R>`; the impl pins both — `I` in the parameter position, `R` in the return.
- The body is just a function body — no special semantics, no hidden parameters.

**The prefix is a type already in scope.** There is no separate module for literal prefixes: the use site `<Type>"..."` resolves `<Type>` to a type by ordinary name lookup, and that type's `(Literal, <Type>)` impl supplies the handler.

- A *domain type* makes the use site read as a constructor (`Date"2026-05-04"` builds a `Date`, exactly like `Date.new(...)` would).
- A *zero-sized `type Sql`* gives a DSL a dedicated stateless value type (`Sql"SELECT ..."`).

Imports work like any other type: `import std/calendar.DateTime` brings the type — and its `Literal` impl rides along via the manifest, exactly like a `Display` impl. Collisions between an imported prefix type and a local definition raise the regular cross-name diagnostic.

**Prefix resolution at the use site.** The lexer's adjacency rule distinguishes `Date"..."` (typed literal) from `Date "..."` (type reference next to an unrelated string); the parser builds a `TaggedString` AST node carrying the prefix type's name. At each `<Type>"..."` site the analyzer resolves `<Type>` to a type with a `(Literal, <Type>)` impl and records that impl demand (so the runtime bridge force-loads its module). If `<Type>` isn't a type, or the type has no `Literal` impl, the error fires at the prefix's position (parallel to "`T` has no `Display` impl").

**A tag type declares `from_fragments` exactly once, and a second declaration is an error at the use site.** The rest of the language chooses between two providers of one method name by reading the call: an inherent `impl T { pub fn f }` beats an `impl Iface for T { fn f }` because the author wrote `f` and the more specific declaration is the one they named. A typed literal names no method — `from_fragments` is the compiler's, from this section — so there is nothing to read, and the language rejects rather than picking. Both spellings of the clash are rejected:

```nomi
type Pick

impl Literal for Pick {
    fn from_fragments(fragments: List<Fragment<String>>) -> String {
        ...
    }
}

impl Pick {
    pub fn from_fragments(fragments: List<Fragment<String>>) -> String {
        ...
    }
} // error at `Pick"..."`

interface Other {
    fn from_fragments(fragments: List<Fragment<String>>) -> Int
}

impl Other for Pick {
    fn from_fragments(fragments: List<Fragment<String>>) -> Int {
        ...
    }
} // error at `Pick"..."`
```

The diagnostic points at the tag, names the rival declaration's line, and says which block to change. It is a rule about the LITERAL and not about the declarations: both programs above are legal so long as nothing writes `Pick"..."`.

**The rule follows from the desugaring rather than adding to it,** and each spelling shows that a different way. Written by hand, `Pick.from_fragments([...])` with the second interface present is *already* rejected — §13 *Function Name Collisions*' ambiguity check fires, and it can suggest a fix a literal cannot use (`qualify by interface, e.g. Literal.from_fragments(...)`); with the inherent block present it resolves to the inherent method by inherent-beats-impl. So in one spelling the sugar was the only form that picked where its own desugaring refuses, and in the other it picked the opposite side from its own desugaring. Go agrees by construction: two methods of one name on one type is not expressible there at all.

**Desugaring.** The parser emits a `TaggedString` AST node with the type name, raw flag, and the interleaved static / dynamic body parts. At evaluation it desugars to the type-qualified call into the type's handler:

```text
<Type>.from_fragments(fragments: List<Fragment<I>>): R
```

where `Fragment` is the `std/literals` ADT:

```nomi
pub enum Fragment<T> {
    Static String
    Dynamic T
}
```

Nothing in the `std/literals` cluster is prelude-exported — not the `Literal` interface, the `Fragment` type, or its `Static`/`Dynamic` variants. Handler authors are the only audience that names any of them, so they import explicitly: `import std/literals.{Fragment, Literal}` for the interface and type, plus `import std/literals.Fragment.{Static, Dynamic}` to pattern-match the variants. Every other program merely *uses* `<Type>"…"` and names none of these, so reserving the words globally (`Some`/`None`/`Ok`/`Err` earn that; a niche DSL-authoring vocabulary doesn't) isn't worth it.

Each `${expr}` slot in the body becomes a `Dynamic(value)`; intervening literal text becomes a `Static(text)`. The fragment list always alternates `Static, Dynamic, Static, Dynamic, ..., Static` — the leading and trailing `Static` are present even when empty, so consumers can rely on the alternation.

For example, `Joiner"name: ${name}, age: ${age}"` desugars to a call equivalent to:

```nomi
Joiner.from_fragments(
    [Static("name: "), Dynamic(name), Static(", age: "), Dynamic(age), Static("")]
)
```

**Signature contract.** The compiler verifies at each typed-literal site that the prefix type's handler is:

```nomi
fn from_fragments(fragments: List<Fragment<I>>) -> R
```

- `I` is the per-literal value-type interface — the constraint each `Dynamic` slot's value must satisfy. Common choices: `Display` for stringifying-then-parsing handlers, `Bindable` for SQL parameter binding, `Safe` for HTML escaping. Concrete (non-interface) `I` is allowed too — `Fragment<String>` accepts only String-typed slots.
- `R` is the handler's result. For fallible parsing it is typically `Result<T, E>`; for builders it is the produced domain value (`Html`, `Query`, ...). A double-quoted literal's type is `R`. A backtick literal's type is `R` too, except when `R` is `Result<T, E>`: then it is `T` (see "Raw typed literals are checked at compile time" below).

At each `${expr}` slot, the analyzer checks that the slot expression's type implements `I`; a mismatch is a type error pointing at the offending slot. The interface in `List<Fragment<I>>` is used as an *existential* — slot values are heterogeneous per element, each implementing `I` in its own way (see §15 on interface-as-existential).

**Coherence.** A typed-literal handler is an ordinary interface impl, so it inherits the impl rules wholesale — nothing typed-literal-specific. At most one `(Literal, <Tag>)` impl program-wide (the coherence check), and the orphan rule requires either `Literal` or the tag type to be declared in the implementing package. A type argument the package declares does not admit a `Literal` impl, as it admits other impls (§13, *Foreign Types*): the handler is chosen by the tag alone, so a second impl for a tag would take its literals away from the first.

**Raw typed literals.** A backtick delimiter disables `${...}` parsing inside the body. The body becomes a single `Static` fragment containing the verbatim text — no `Dynamic` slots. The same handler signature applies. Useful when `${...}` or backslashes appear literally in the embedded language.

**Raw typed literals are checked at compile time.** A raw body never interpolates, so the handler's input is fixed text. When the handler can fail (it returns `Result<T, E>`), the compiler runs it while checking the program, and the literal's type is `T`:

- `Ok(v)`: the literal is a `T`. `` Regex`\d+` `` is a `Regex`, so it needs no `try`, and `` once letters = Regex`[A-Za-z]+` `` works at the top level of a file.
- `Err(e)`: the literal is a compile error at the literal, carrying the handler's message (the `Display` rendering of `e`, or its `Debug` rendering when `E` has no `Display`): `` Regex`[` `` is ``typed literal Regex`[` is invalid: error parsing regexp: missing closing ]: `[` ``.
- A handler that reaches an effect (output, files, the clock, tasks, an app field), or that does not finish within the compiler's step limit, cannot be run at compile time, and the literal is a compile error saying so. Write such a literal with double quotes and handle its `Result`.

```nomi
import {
    std/io
    std/regex.Regex
}

once letters = Regex`[A-Za-z]+`

fn main() {
    digits: Regex = Regex`\d+`
    io.print(String.find_all("order 66, aisle 7", digits)) // [66, 7]
    io.print(String.find_all("ab 12 cd", letters)) // [ab, cd]
}
```

`try` on a backtick literal checked at compile time is the ordinary "try requires a Result or Maybe" error, with a hint that the literal is already a `T`.

The quote style alone decides this. A double-quoted typed literal keeps its handler's result type, interpolated or not: `Regex"\\d+"` and `Regex"room ${n}"` are both `Result<Regex, String>`, built when the program runs. A handler that cannot fail (returns `T` itself) gives the same type in both forms.

At run time a backtick literal still calls its handler, once per literal site: the first evaluation builds the value and later evaluations reuse it, so a literal in a loop is not rebuilt on each iteration. The handler is pure and its input fixed, so the run finds the `Ok` the compiler found.

**Indentation and `${...}` rules.** Triple-quoted typed literals follow the same leading-newline strip, min-indent baseline, trailing-newline strip rules as plain triple-quoted strings (see "Triple-quoted strings" above). The `${expr}` interpolation and `\${` escape rules from "String Literals" above also apply unchanged inside non-raw typed-literal bodies.

**Worked example: `std/calendar`.** The canonical demonstration. `std/calendar` defines an opaque `Date` type whose `Literal` impl's `from_fragments` takes `List<Fragment<String>>` and returns `Result<Date, Error>`:

```nomi
// std/calendar.nomi (excerpt; stdlib files import each other with bare paths)
import literals.{Fragment, Literal}

pub opaque struct Date {}

impl Literal for Date {
    fn from_fragments(fragments: List<Fragment<String>>) -> Result<Date, Error> {
        body = Iter.reduce(fragments, fn acc = "", frag -> {
            case frag {
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + v
            }
        })

        Date.parse(body)
    }
}
```

At the use site:

```nomi
import {
    std/calendar.Date
    std/io
}

fn main() {
    case Date"2026-05-04" {
        Ok(d) -> io.print("year=${Date.year(d)}")
        Err(_) -> io.print("parse failed")
    }

    month_str = "07"

    case Date"2026-${month_str}-04" {
        Ok(_) -> io.print("ok")
        Err(_) -> io.print("err")
    }
}
```

`Date` is opaque — outside callers can't bypass parsing. Because parsing is fallible, a double-quoted `Date"…"` is a `Result<Date, Error>` at the use site above; callers `case`-match or `try`-unwrap there. A backtick `` Date`2026-05-04` `` is checked at compile time and is a `Date`. The `Literal` conformance makes `Date` itself the tag; `from_fragments` is the fixed function name. The same shape — opaque type, fallible parse, `Display`-or-domain interface for slots — applies to `Time`, `NaiveDateTime`, `OffsetDateTime`, `DateTime`, plus user-defined cases like `Url`, `Regex`, `Json`, etc.

**Worked example: `Box` (a domain type as its own tag).** A user-defined type backs a tag by implementing `Literal`:

```nomi
import {
    std/io
    std/literals.{Fragment, Literal}
}

pub struct Box {
    contents: String
}

impl Literal for Box {
    fn from_fragments(fragments: List<Fragment<String>>) -> Box {
        body = Iter.reduce(fragments, fn acc = "", frag -> {
            case frag {
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + v
            }
        })

        #Box{contents: body}
    }
}

fn main() {
    b = Box"hello" // reads as a constructor; no separate tag binding
    io.print(b.contents) // hello
}
```

**Tooling.** Editors may use the tag identifier to highlight the body with an embedded grammar (e.g. Zed via tree-sitter `injections.scm`). The tag-to-grammar mapping is editor-side and not part of the language definition; the compiler treats every tag as opaque.

### String Functions

Owner functions on `String`, called `String.length(s)` and so on:

```nomi
fn length(s: String) -> Int                                          // grapheme count
fn slice(s: String, start: Int, end: Int) -> String                  // grapheme indices
fn pad_start(s: String, width: Int, fill: String = " ") -> String    // to `width` graphemes; never truncates
fn pad_end(s: String, width: Int, fill: String = " ") -> String
fn contains?(c: String, x: String) -> Bool                         // Contains<String> impl: a substring
fn contains?(c: String, x: Regex) -> Bool                          // Contains<Regex> impl: a match anywhere
fn contains?(c: String, x: Codepoint) -> Bool                      // Contains<Codepoint> impl: it occurs
fn starts_with?<M>(s: String, prefix: M) -> Bool where M: Matcher
fn ends_with?<M>(s: String, suffix: M) -> Bool where M: Matcher
fn to_upper(s: String) -> String                                     // Unicode-aware, locale-independent
fn to_lower(s: String) -> String
fn trim(s: String) -> String
fn split<M>(s: String, separator: M) -> List<String> where M: Matcher
fn replace<M>(s: String, old: M, new: String) -> String where M: Matcher  // every match; `new` is literal
fn find_all<M>(s: String, needle: M) -> List<String> where M: Matcher     // every match, left to right
fn find<M>(s: String, needle: M) -> Maybe<String> where M: Matcher        // the first match: find_all's first element
fn captures(s: String, re: Regex) -> Maybe<List<Maybe<String>>>      // the first match's groups 1.., None for one that did not take part
fn captures_all(s: String, re: Regex) -> List<List<Maybe<String>>>    // the groups of every match, left to right
fn words(s: String) -> List<String>                                  // split on runs of whitespace
fn lines(s: String) -> List<String>                                  // split on \n or \r\n
fn repeat(s: String, count: Int) -> String                           // count >= 0
fn reverse(s: String) -> String                                      // by grapheme cluster
fn normalize(s: String, form: NormalForm) -> String                  // NFC / NFD / NFKC / NFKD
fn to_int(s: String) -> Maybe<Int>
fn to_bytes(s: String) -> Bytes                                      // UTF-8 bytes
fn to_codepoints(s: String) -> List<Codepoint>                       // Unicode scalars
fn strip_prefix(s: String, prefix: String) -> Maybe<String>
fn strip_suffix(s: String, suffix: String) -> Maybe<String>
fn join(parts: Iter<String>, separator: String = "") -> String       // any Iter<String>, driven once
```

The search functions take any `Matcher` (`std/matcher`) as the thing to look for: a `String` matches its own text, a `Regex` (`std/regex`) matches its pattern, and a `Codepoint` matches itself, so `String.split("a,b", ",")` and `` String.split(text, Regex`\s+`) `` are the same function. `String.contains?` is String's `Contains` impl at the same three types, chosen by the needle's type (§*Several instantiations for one type*), so a `String` also satisfies `where C: Contains<String>` (§*Membership: `Contains`*). Each Matcher function answers one of these operations with the matcher first (`contained_in?`, `prefix_of?`, `suffix_of?`, `split_in`, `replace_in`, `find_all_in`, `find_in`), and a type of your own implements it to become searchable. A generic function of your own takes either with `where M: Matcher`. "Pattern" is not the name because a pattern in Nomi is a destructuring shape (§10). `String.replace` inserts its replacement literally for a regex too; `Regex.replace_all` expands `$1` capture references. `String.captures` and `String.captures_all` take a `Regex` rather than any `Matcher`, since only a regex has groups. Each match gives one entry per group from group 1 in the order the pattern opens them (the whole match is `String.find`'s answer, not an entry), `Some` of the text for a group that took part, even an empty one, and `None` for one that did not: `` String.captures("b", Regex`(a)|(b)`) `` is `Some([None, Some("b")])`. `String.split` and the other Matcher search functions are generic, so naming one as a value needs a function type to settle `M`: `split: (String, String) -> List<String> = String.split`.

`String` also implements `Iter<String>` (one grapheme cluster per step), so every `Iter` owner function works on a string. To map or filter graphemes, run the generic adapter and join the result back into a `String`: `Iter.map(s, f) |> String.join()`, `Iter.filter(s, p) |> String.join()`. `String.join` takes any `Iter<String>` (a List, Set or Vector, graphemes, or a lazy chain) and a separator that defaults to `""`. `String.reverse`/`String.length` stay container-specific (native, by grapheme cluster).

### Byte and Bytes Functions

Owner functions on `Byte` (`from_int`, `to_int`) and `Bytes` (the rest):

```nomi
fn from_int(n: Int) -> Maybe<Byte>                 // 0..255 only
fn to_int(b: Byte) -> Int
fn length(data: Bytes) -> Int
fn get(data: Bytes, index: Int) -> Maybe<Byte>     // None outside the buffer
fn at(data: Bytes, index: Int) -> Byte             // traps outside the buffer
fn slice(data: Bytes, start: Int, end: Int) -> Bytes
fn concat(a: Bytes, b: Bytes) -> Bytes
fn to_list(data: Bytes) -> List<Byte>
fn from_list(items: List<Byte>) -> Bytes
fn to_string(data: Bytes) -> Result<String, String> // UTF-8 decode
fn contains?(c: Bytes, x: Byte) -> Bool            // Contains<Byte> impl; linear scan
```

`Bytes` implements `Contains<Byte>`, `Iter<Byte>`, `Add<Bytes, Bytes>`,
`Display`, `Debug`, `Equatable`, and `Hashable`. `Byte` intentionally does not implement arithmetic
operators; use `Byte.to_int` for numeric work.

### Display and Debug

`Display` and `Debug` are stdlib interfaces with the same signature shape but *distinct function names*, distinguished by audience:

```nomi
pub interface Display {
    fn to_string(value: self) -> String
} // for users

pub interface Debug {
    fn inspect(value: self) -> String
} // for developers
```

The names differ deliberately so the two don't collide, leaving both freely callable type- or interface-qualified (`Point.to_string(p)` / `Display.to_string(p)`, `Point.inspect(p)` / `Debug.inspect(p)`). Dispatch sites:
- `${expr}` (string interpolation) and `io.print(expr)` — `Display.to_string(expr)`
- `io.inspect(expr)` — `Debug.inspect(expr)`

An impl that renders its own receiver through the function it defines never returns, and is a compile error at the rendering. In `impl Display for Foo`, `fn to_string(foo: Foo)` may not contain `"${foo}"`, `io.print(foo)`, `io.write(foo)`, `Display.to_string(foo)` or `Foo.to_string(foo)`; in `impl Debug for Foo`, `fn inspect(foo: Foo)` may not contain `io.inspect(foo)`, `dbg foo`, `Debug.inspect(foo)` or `Foo.inspect(foo)`. A plain binding of the receiver (`same = foo`, then `"${same}"`) is the same value and the same error. Rendering a field (`"${foo.name}"`), another value of the type (`"${node.left}"` in a tree), or the receiver through the other interface is legal:

```
"${foo}" calls Display.to_string(foo), the function being defined, so it never returns
help: Debug.inspect(foo) renders its fields
```

The same holds for equality, ordering and hashing. In `impl Equatable for P`, `fn equal?(a: P, b: P)` may not compare its own arguments with `==` or `!=`, or pass them to `Equatable.equal?` or `P.equal?`; in `impl Comparable for P`, `fn compare(a: P, b: P)` may not compare them with `<`, `>`, `<=` or `>=`, or pass them to `Comparable.compare` or `P.compare`; in `impl Hashable for P`, `fn hash(p: P)` may not pass `p` to `Hashable.hash` or `P.hash`. Each dispatches back to the function being defined with the values it was given, so it calls itself forever, and is a compile error at the comparison whose help says to compare the fields instead or to write `derive Equatable for P`. A plain binding of an argument is the same value. Comparing or hashing a field is legal, even a field of type `P`, as in a tree whose `equal?` compares its children with `==`:

```
`a == b` calls P's `equal?`, the function being defined, with its own arguments, so it calls itself forever
help: compare the fields instead (`a.field == b.field`), or write `derive Equatable for P`
```

The two interfaces have **opposite default policies**:

- **`Debug` is universal and automatic.** Every value is `Debug.inspect`-able with no derive and no hand-written impl — the compiler eagerly synthesizes a structural Debug impl (byte-identical to `derive Debug`) for every declared type that doesn't supply one, so `io.inspect(x)` never errors for missing-impl. There is no `where T: Debug` bound to write; the checker treats Debug as satisfied by every type (including bare type variables and interface existentials, so `Fragment<Display>` and `List<SomeInterface>` are inspectable). See §38.2 for precedence and the opaque/host rules.
- **`Display` stays opt-in.** A type must supply an `impl Display for T { ... }` block or `derive Display`; without one, rendering a value of it is a compile error: `"${p}"` reports ``no impl of `Display` for `P` ``, and `io.print(p)` reports ``P does not implement Display (required by `where T: Display`)``.

The result is a two-tier story: **Debug = universal floor (always available), Display = curated human form (opt-in).**

#### Primitives

| Type | Display | Debug |
|---|---|---|
| `Int`, `Float`, `Bool` | numeric / variant form — `42`, `3.14`, `True` / `False` | identical |
| `Unit` | no Display impl; rendering one is a compile error | `Unit` |
| `String` | raw text, no quotes | `"..."` with `\` and `"` escaped |
| `Infallible` | uninhabited; can never be called | — |

Numeric / Bool primitives have no user-vs-developer distinction. String is the one primitive where the split matters: Display gives the bare text so `"hello ${name}"` doesn't double-quote; Debug gives the round-trippable literal so `io.inspect("a\"b")` reveals the escape.

#### Stdlib intrinsics

Containers recurse through the **same** interface as the caller — Display container uses Display on elements; Debug uses Debug. Brackets / braces / parens / separators are byte-identical between the two; only the per-element function differs.

| Type | Display | Debug |
|---|---|---|
| `List<T>` | `[Display(v1), Display(v2), ...]`, `[]` for empty | `[Debug(...), ...]` |
| `Vector<T>` | `#[Display(v1), ...]`, `#[]` for empty | `#[Debug(...), ...]` |
| `Set<T>` | `%[Display(v1), ...]`, `%[]` for empty | `%[Debug(...), ...]` |
| `Map<K, V>` | `%{Display(k) => Display(v), ...}`, `%{}` for empty | `%{Debug(...) => Debug(...), ...}` |
| `Maybe<T>` | `Some(Display(v))`, `None` | `Some(Debug(v))`, `None` |
| `Result<T, E>` | `Ok(Display(v))`, `Err(Display(e))` | `Ok(Debug(v))`, `Err(Debug(e))` |
| `Range<T>` | `Debug(start)..Debug(end)`, `Debug(start)..=Debug(end)`, `Debug(start)..` for unbounded integer ranges | identical |
| Tuple `#(...)` | `#(Display(a), Display(b), ...)` | `#(Debug(a), Debug(b), ...)` |
| Anon struct `#{f: V, ...}` | `#{f: Display(v), ...}` | `#{f: Debug(v), ...}` |

Tuples and anon structs are syntactic intrinsics with no named type to attach an impl to; they get language-level rendering (similar to how their literal syntax is privileged). Every other intrinsic gets a Nomi-side impl in stdlib.

Container impls require their elements to implement the corresponding interface — `${[some_fn]}` errors because functions have no `Display` impl. Each container impl states the constraint as a bound (`impl Display for List<T> where T: Display`), which its body needs to render an element, so `List<Foo>` is Display exactly when `Foo` is; a missing element impl is reported as the element's (``no impl of `Display` for `Foo` ``).

For `Maybe` / `Result`, the variant constructor is always rendered (`Some(...)` / `None` / `Ok(...)` / `Err(...)`) — never unwrapped. Display preserves variant identity; the user-facing form of `Maybe.None` is `"None"`, not the empty string.

#### Custom types

**Debug is always available** for custom types (auto-synthesized — see §38.2); the table below describes how each construct gets its rendering. **Display** must be opted into.

| Construct | Display source | Debug source |
|---|---|---|
| `struct T {...}` | `impl Display for T { fn to_string(...) }`, or `derive Display` (else errors) | auto-synthesized structural impl, unless overridden by `impl Debug for T { fn inspect(...) }`, or `derive Debug` |
| `enum E { ... }` | same | same |
| `type T Foo` (distinct, all flavors) | same | same |
| `opaque struct/enum/type` | only the owning file can declare or derive an impl; outside callers depend on what the file exposes | auto default is **name-only** `<opaque T>` (never structural — would leak hidden internals); the owning file may write `impl Debug` plus `fn inspect(...)`, or `derive Debug`, for richer output |
| `host type T` (declared) | `impl Display for T { fn to_string(...) }` (else errors) | auto default is the **bare type name** `T` (internals are host-side, nothing structural to render), including for a Go-handle value; `impl Debug for T { fn inspect(...) }` overrides, wherever the value is nested |
| `interface I` (existential value) | runtime dispatches on the concrete type; concrete must have a Display impl | runtime dispatches on the concrete type, which always has Debug |

`derive Display` synthesizes a structural impl mirroring `derive Debug`'s shape:

- **Struct**: `#TypeName{f1: Display(v1), f2: Display(v2), ...}`, `#TypeName{}` for empty.
- **Enum (bare variant)**: `VariantName`.
- **Enum (positional arity n)**: `VariantName(Display(p0), Display(p1), ...)` — no enum-name prefix, matching how `derive Debug` formats variants. The arity is the declaration's: a payload declared as a tuple type (`Node #(Tree, Tree)`) has one per element and renders flat, `Node(Leaf(1), Leaf(2))`, as its flat call form builds it; any other payload has arity 1, so a type parameter that holds a tuple renders it with its own parentheses, `Some(#(1, 2))`, in a user enum (`Has T`) as in `Maybe`.
- **Enum (struct payload)**: `#VariantName{f: Display(v), ...}`, unqualified like the other variants.
- **Enum (embedded variant)**: bare delegation to the embedded value's Display.
- **Distinct primitive**: `TypeName(Display(inner))`.
- **Distinct tuple**: `#TypeName(Display(inner.0), Display(inner.1), ...)`, the named tuple literal.
- **Distinct over a map, vector or set**: the call form around the inner value, `Kvs(%{"a" => 1})`, `Path(#[1, 2])`, `Tags(%["a"])`.
- **Distinct zero-sized**: bare `TypeName`.

The `derive Debug` entry uses the same shape with Debug on each nested value. Concrete consequence: a type without a `String` field gets byte-identical output from `derive Display` and `derive Debug`; with a String field, Display has unquoted strings, Debug has quoted.

#### Non-structural value kinds — Debug placeholders, no Display

Some runtime value kinds have no declaration to render structurally. Because Debug is universal, `io.inspect` on them yields a deterministic placeholder (no addresses, test-stable) rather than an error; they have no **Display**, so interpolating or printing one is an error:

| Kind | `Debug.inspect` (`io.inspect`) | `Display` (`${...}` / `io.print`) |
|---|---|---|
| Function / builtin values | `<function>` | errors — no meaningful Display |
| `Task` / `Sender` / `Receiver` | `<task>` / `<channel>` | errors |
| `Context` | `<context>` | errors |
| Lazy `Iter` (an adapter or constructor result such as `Iter.map(xs, f)` or `Iter.from(0)`, before materializing) | `<iter>` | errors |

Iters are lazy by design; rendering the elements would have to consume the iterator (observable cost and state change, and no end for an infinite one), so inspecting one prints `<iter>` and leaves it unconsumed: it still yields every element afterwards. The placeholder is the same nested in a container (`[<iter>]`) and in a failed assertion's `values:` row. To see the elements, materialize first with `Iter.to_list(it)` and inspect the result. An `Iter<T>` position holding a source rather than a pipeline (`fn show(xs: Iter<Int>)` called with `[1, 2]`) holds that source, so it inspects as that value: `[1, 2]`. The same holds for a Range (`1..=3`), a value of a type with its own `impl Iter` (`#Countdown{from: 3}`), and a collection of declared values (`[#Point{x: 1, y: 2}]`).

For a declared type, the compile error names the type that lacks an impl, so the fix (add an `impl Display for T { ... }` block or `derive Display`) is clear from the message. A function value has no Display, and the error says so (``no impl of `Display` for `(Int) -> Int`; a function value has no `Display` ``). A tuple or anonymous struct is Display exactly when each part is, so `"${#(1, f)}"` reports the function and `"${#(1, p)}"` reports `p`'s type when it has no impl.

> **Status: not yet implemented.** For a lazy `Iter`, `"${it}"` and
> `io.print(it)` pass the type checker; the program is then refused before it
> runs with a "not supported yet" error (§25) rather than a type error.

## 17. Error Handling

`Result` type with `Ok`/`Err` for fallible operations. `try` keyword for propagation. `case` for explicit handling:

```nomi
case users.get(id) {
    Ok(user) -> process(user)
    Err(e) -> handle_error(e)
}
```

Or with `try` for the happy path:

```nomi
fn process(id: Int) -> Result<Bool, String> {
    user = try users.get(id)
    profile = users.get_profile(user)
    try email.send(profile)
}
```

Or piped:

```nomi
fn process(id: Int) -> Result<Bool, String> {
    id
    |> try users.get()
    |> users.get_profile()
    |> try email.send()
}
```

Or with a binding `else`, which handles one step's failure in place (§10, *Bindings with `else`*):

```nomi
fn process(id: Int) -> Result<Bool, String> {
    Ok(user) = users.get(id) else {
        Err(e) -> return Err("loading user ${id}: ${e}")
    }

    try email.send(users.get_profile(user))
}
```

### Traps, `Maybe` and `Result`

An operation that cannot give the answer asked for either returns a `Maybe` or
`Result`, or traps. One rule decides which, for the standard library and for
APIs written in Nomi:

- **`Maybe` or `Result`** when the outcome depends on data the caller does not
  control or cannot cheaply check: a key that may be absent (`Map.get`),
  parsing (`String.to_int`), input, I/O, or a position that may legitimately be
  off the edge (`Vector.get`, `Iter.first`).
- **A trap** when the caller broke a promise it could have checked: a divisor
  it chose (`/`, §22), arithmetic past 64 bits (§22 *Integer overflow traps*),
  an index it computed (`Vector.at`, `Vector.set`), a range whose bounds it
  picked (`Generator.int`, §40), or a `todo` it left in (§5 *Unwritten
  Code*). A trap means the program has a bug. It is not a situation to
  handle.
- **Every trapping operation has a total way around it**: a sibling that
  returns `Maybe`, or a cheap check the caller can make first. `Int.divide`
  takes a `NonZeroInt` that `Int.to_non_zero` makes, `Decimal.divide` takes a
  scale and a rounding mode where `/` would trap on a quotient that does not
  terminate, `Vector.get` answers `None` where `Vector.at` traps, and
  `Vector.length` is the check before `Vector.set`. When wrapping is the
  intent, `Int.wrapping_add`, `Int.wrapping_sub` and `Int.wrapping_mul` never
  overflow.
- **A trap is never caught inside the program.** It ends the task it happens
  in. `nomi run` prints it and exits with status 1, `nomi test` fails the case
  and goes on to the next, and a spawned task's trap is its `Failed` outcome
  (§20 *Failures And Restarts*), which a supervisor may restart. A program
  handles what it expects with `Maybe` and `Result`, through `try`, `case` and
  a binding `else`.

A trap's text names the broken promise: `line 4: division by zero`,
`line 7: integer overflow: 9223372036854775807 + 1`,
`line 9: index 7 is out of range for a Vector of length 5`. The line is the
program's own, even when the trap happens inside the standard library
(§22 *Where a trap is reported*).

The naming convention follows from the rule. A function named **`get`**
returns a `Maybe`, because what it looks for may not be there (`Map.get`,
`Vector.get`, `Bytes.get`, `Iter.get`). A function named **`at`** returns the
value itself and traps when the position does not exist (`Vector.at`,
`Bytes.at`): the caller is promising it does.

A map pattern follows the rule too. Whether a key is present is data, so
`%{"port" => p} = config` is refutable: it needs an `else`, a `case` or an
`if` (§10 *Map Destructuring*).

## 18. Comments

Line comments:

```nomi
// This is a comment
```

Doc comments use triple-slash (`///`) and attach to the declaration that follows:

```nomi
/// 64-bit integer.
host type Int

/// Returns the absolute value of n.
host fn abs(n: Int) -> Int

/// A value that may or may not be present.
enum Maybe<T> {
    Some T
    None
}
```

- No block comments
- A line comment may sit inside a bracketed, comma-separated list: a call's arguments, a list, vector, set, map, tuple or struct literal, a function's or lambda's parameters, type parameters and type arguments, a list, vector, set, tuple, map, struct or variant pattern, a destructure, and an import's `{...}` selector. It goes on a line of its own before an item, after the opening bracket, after the last item, or at the end of a line after an item's comma. `nomi fmt` writes a list holding a comment one item per line, keeping each comment where it was:

```nomi
import std/io

fn add(a: Int, b: Int) -> Int {
    a + b
}

fn main() {
    total = add(
        // the base
        1,
        2, // the step
    )

    io.print("${total}")
}
```

- Doc comments are preserved in the AST and available to tooling (LSP hover, documentation generation)
- A `///` comment with no declaration after it to document (above an `import` or a `test` block, for instance) is a parse error; write `//` or `//#` there
- A `//!` line is an attached test (§36), so `//!` must start its line: after code on the same line (`x = 1 //! note`) it is a parse error; write `//` there
- Inside a body, a `///` line is a parse error (a doc comment documents the declaration after it), and so is a `//!` line not followed by a nested declaration; the language server offers a quick fix that makes each such line of the run a `//` comment

File-level documentation uses `//#`. To the
compiler these are ordinary line comments — they are **not** doc comments and do
**not** attach to any declaration — so a module description written as `//#` at
the top of a file won't bleed into the first declaration's hover. The reference
generator (`cmd/nomi-docgen`) collects a module's `//#` lines as its page intro:

```nomi
//# Operations on the persistent linked `List<T>` type. Use `Iter` owner
//# functions for lazy pipelines over any `Iter<T>`.

/// Returns the next element and remaining list, or None if empty.
pub fn list_next(list: List<T>) -> Maybe<#(T, List<T>)> {
    ...
}
```

### Shebang line

A file may begin with a `#!` line so that it runs as an executable script
(§26, *Scripts*). The `#!` must be the file's first two bytes; the compiler
ignores the rest of that line, and line numbers count it, so everything after
it keeps its source position in diagnostics, test reports and the editor.
`#!` anywhere else is a syntax error (`` a `#!` line is allowed only as the
first line of a file ``). `nomi fmt` keeps the line as written, minus trailing
whitespace; a blank line after it stays one blank line, and no blank line
stays none.

```nomi
#!/usr/bin/env nomi
import std/io

fn main() {
    io.print("hi")
}
```

## 19. Collections

All collections are immutable. Operations return new values.

### Literal forms

Every literal of a built-in shape except a list starts with that shape's symbol: `#` for the indexed and product shapes (vector, struct, tuple) and `%` for the hashed ones (map, set). A type built on a shape writes its name right after the symbol. The parser knows the shape from the first token and the token after the name, so it never looks at what a name declares:

| Shape | Unnamed | Named | Update | Pattern |
|---|---|---|---|---|
| struct | `#{x: 1}` | `#Point{x: 1}`, `#geo.Point{x: 1}`, `#Shape.Circle{radius: 1.0}`, `#.Circle{radius: 1.0}` | `#{..p, x: 2}`, `#{.., x: 2}` | `#{x, y}`, `#Point{x, y}`, `#.Circle{radius}` |
| tuple | `#(a, b)` | `#Pair(1, "x")` | | `#(a, b)`, `#Pair(a, b)` |
| vector | `#[1, 2]`, `#[]` | `#Path[1, 2]`, `#.Items[1, 2]` | `#[..v, x]`, `#[x, ..v]`, `#[.., x]` | `#[first, ..rest]`, `#[.., last]`, `#[a, b]`, `#Path[a, ..]` |
| list | `[1, 2]`, `[]` | `Ids[1, 2]`, `.Items[1, 2]` | `[x, ..xs]`, `[x, ..]` | `[first, ..rest]`, `[a, b]` |
| map | `%{"k" => v}`, `%{}` | `%Kvs{"k" => 1}`, `%Json.Obj{"k" => v}`, `%.Obj{"k" => v}` | `%{..m, k => v}`, `%{.., k => v}` | `%{"k" => v}`, `%Kvs{"k" => v}` |
| set | `%[1, 2]`, `%[]` | `%Tags["a"]` | `%[..s, x]`, `%[.., x]` | `%["admin", ..rest]`, `%["admin", ..]`, `%[1, 2]` |

- A spread has no named form: the base already carries the type (`#Point{..q, x: 1}` is `struct spread \`..\` has no named form; write \`#{..base, field: value}\``).
- Types are written without a symbol, except a tuple's and an anonymous struct's: `Vector<T>`, `Set<T>`, `Map<K, V>`, `#(Int, Int)`, `#{x: Int}`.
- A plain `{` only opens a block, and a name followed by `{` never starts a literal. `Point{x: 1}`, `.Circle{radius: 1.0}`, `Kvs{"a" => 1}` and `Map{"a" => 1}` are parse errors, and `Vector[1]` and `Set[1]` are list literals naming a type that is not a list type.
- A payload variant and a distinct type over a scalar are calls, not literals: `Some(1)`, `.Circle(2.0)`, `UserId(5)`.

### Membership: `Contains`

Every collection answers "does it hold this value?" with one function name,
`contains?`. The collections implement the prelude interface `Contains`
(`std/contains`):

```nomi
interface Contains<T> {
    fn contains?(c: self, x: T) -> Bool
}
```

| Implementor | `contains?(c, x)` is true when | Cost |
|---|---|---|
| `Set<T>` | `x` is a member | hash lookup |
| `Map<K, V>`, at its key type `K` | `x` is a key; a value is not a key | hash lookup |
| `Range<T>` | `x` lies between the ends; an exclusive range leaves out its end, an unbounded one checks only its start | two comparisons |
| `List<T>` where `T: Equatable` | an element is `==` to `x` | linear scan |
| `Vector<T>` where `T: Equatable` | an element is `==` to `x` | linear scan |
| `Bytes`, at `Byte` | a byte equals `x` | linear scan |
| `String`, at `String` | `x` occurs as a substring | linear search |
| `String`, at `Regex` | `x` matches anywhere | linear search |
| `String`, at `Codepoint` | `x` occurs in the text | linear search |

`String` implements `Contains` at three type arguments, and a call takes the
one its argument's type chooses (§*Several instantiations for one type*):
`String.contains?(s, "x")`, `` Contains.contains?(s, Regex`\d`) `` and
`String.contains?(s, '-')` are three impls. `Iter` answers membership under the
same name without implementing `Contains`: `Iter.contains?(source, x)`, where
`T: Equatable`, scans any iterable, a lazy pipeline included (§*`std/iter`*).

```nomi
Set.contains?(%["admin"], "admin") // True

Map.contains?(%{"a" => 1}, "a") // True

Map.contains?(%{"a" => 1}, 1) // compile error: 1 is not a key type

Range.contains?(1..5, 5) // False: the end is excluded

List.contains?([1, 2, 3], 2) // True

String.contains?("a-b", '-') // True

Iter.contains?(Iter.from(0), 1_000) // True, and stops there
```

A linear scan stops at its first match, so `Iter.contains?` ends on an
infinite source that holds `x`. It compares each element with `==`: a struct
with a hand-written `impl Equatable` is compared by it, and every other value
in the runtime, with no Nomi call per element. Float equality is reflexive
(§*Float Special Values*), so `List.contains?([Float.nan()], Float.nan())` is
`True` and `List.contains?([0.0], -0.0)` is `True`, as for a `Set` or a `Map`
key. For repeated lookups in a `List`, `Vector` or `Bytes`, build a `Set` once.

A generic function takes any implementor with a bound:

```nomi
fn all_present<C, T>(c: C, wanted: List<T>) -> Bool where C: Contains<T> {
    Iter.all?(wanted, &C.contains?(c, &1))
}
```

`all_present("a word", ["a", "word"])` takes String's impl at `String`, and
`all_present("a-b", ['-'])` its impl at `Codepoint`: the bound's `T` is solved
from `wanted`, and that chooses the impl.

### List

Linked list. Use it for cons-style recursion, structural pattern matching, and
cheap prepends.

```nomi
names = ["Alice", "Bob", "Carol"]

numbers = [1, 2, 3, 4, 5]
```

- Literal syntax: `[1, 2, 3]`
- Homogeneous — all elements must be the same type
- Immutable — operations return new lists
- `List.contains?(xs, x)` is the `Contains` impl, a linear scan (§*Membership: `Contains`*)
- `..tail` after a comma spreads (cons): `[item, ..list]` is O(1), supports structural sharing. The spread must be the last element (`list spread ".." must be the last element`), because cons cells make prepending O(1) and appending O(n). The struct-update spread (§7) must be *first* instead; the rules differ because a list's is about representation and a record's is about notation.
- At a field or key of an update, a bare `..` tail is the list already there: `#{..account, log: [t, ..]}` puts `t` in front of the old `log` (§7 *Nested updates*). A missing map key starts from `[]`.
- A spread construction evaluates its tail first, then its head expressions from left to right. Each expression runs once. A plain list literal evaluates its elements from left to right.
- Works in both construction and pattern matching:
  ```nomi
  list = [1, ..[2, ..[3, ..[]]]] // [1, 2, 3]

  case list {
      [] -> "empty"
      [head, ..tail] -> "head is ${head}"
      [a, b, ..rest] -> "first two: ${a}, ${b}"
  }
  ```

#### List Operations

`List`'s owner API is small: it carries only the ops that read or rebuild a `List`'s cons structure (`head`, `tail`, `concat`, `next_item`), the `Contains` impl (`contains?`), and the `Iter` protocol (`each_while`, `known_count`). **Every generic operation — transform, filter, reduce, sort, count, index access — is an `Iter` owner function** over any `Iter<T>`, and a `List` is an `Iter<T>`. The adapters are lazy and fuse; end a chain with `Iter.to_list()` to materialize a `List` back (one allocation, at the visible step):

```nomi
names = ["Alice", "Bob"]

names = ["Carol", ..names]

// lazy pipeline, strict terminal
[1, 2, 3, 4, 5]
|> Iter.filter(fn x -> x > 2)
|> Iter.map(fn x -> x * 2)
|> Iter.sum()

// lazy pipeline, materialize back to a List
[1, 2, 3, 4, 5]
|> Iter.filter(fn x -> x > 2)
|> Iter.map(fn x -> x * 2)
|> Iter.to_list()
```

#### List functions (container-specific)

```nomi
fn head(list: List<T>) -> Maybe<T>          // first element (cons-list idiom)
fn tail(list: List<T>) -> Maybe<List<T>>    // all but the first
fn concat(a: List<T>, b: List<T>) -> List<T>
fn next_item(list: List<T>) -> Maybe<#(T, List<T>)> // head and tail together
fn contains?(c: List<T>, x: T) -> Bool where T: Equatable // Contains impl; linear scan
```

Plus the `Iter` protocol — `each_while`, and `known_count` (O(1), read from the cached length). Everything else is generic and lives on `Iter`:

```nomi
Iter.count(xs) // O(1) via known_count

Iter.first(xs) / Iter.last(xs) / Iter.get(xs, i)

Iter.empty?(xs) / Iter.not_empty?(xs)

Iter.reverse(xs) // → List

Iter.map(xs, f) |> Iter.to_list()

Iter.filter(xs, p) |> Iter.to_list()

Iter.take(xs, n) / Iter.drop(xs, n)

Iter.take_while(xs, p) / Iter.drop_while(xs, p)

Iter.flat_map(xs, f) / Iter.flatten(xss) / Iter.zip(a, b) / Iter.with_index(xs)

Iter.chunks(xs, n) / Iter.chunk_by(xs, key)

Iter.reduce(xs, f, from: start) / Iter.each(xs, f) / Iter.find(xs, p)

Iter.scan(xs, f, from: start) / Iter.map_reduce(xs, fn acc = start, x -> #(out, next))

Iter.min(xs) / Iter.max(xs) / Iter.min_by(xs, key) / Iter.max_by(xs, key) / Iter.sum(xs)

Iter.any?(xs, p) / Iter.all?(xs, p) / Iter.contains?(xs, x)

Iter.sort(xs) / Iter.sort_by(xs, key) / Iter.sort_with(xs, cmp)

Iter.partition(xs, p) / Iter.group_by(xs, key) / Iter.frequencies(xs)
```

The rule: generic over any iterable → `Iter.X`; specific to a container's structure → that container's owner. Reach for `Iter.*` for any transformation/query; the only `List`-native ops are the cons-structure ones above.

### Vector

Immutable random-access sequence. Use it when indexed lookup and push-at-end are
the natural operations.

```nomi
names = #["Alice", "Bob", "Carol"]

empty: Vector<Int> = #[]
```

- Literal syntax: `#[1, 2, 3]`, the `#` and the elements in brackets. The
  empty vector is `#[]`, which takes its element type from the expected type,
  as `[]` does. There is no empty constructor: `#[]` is the empty value. A type over a vector writes its name after the `#`,
  `#Path[1, 2]`, and so does a vector-payload variant, `#.Items[1, 2]`.
  `Vector[1, 2]` is not a vector: it is a list literal naming a type that is
  not a list type, an error
- Homogeneous — all elements must be the same type
- Immutable — operations return new vectors
- `Vector.get`, `Vector.at`, `Vector.push`, `Vector.set`, `Vector.concat`,
  `Vector.length` and `Vector.contains?` (the `Contains` impl, a linear scan)
  are container-specific operations. Converting is `Iter`'s
  job: `Iter.to_list(v)` and `Iter.to_vector(xs)`
- Indexes start at 0. `Vector.get(v, i)` answers `None` for an index outside
  `0 <= i < length`; `Vector.at(v, i)` returns the element and traps on such an
  index, and so does `Vector.set(v, i, x)`, which returns the new vector. The
  trap reads `line 9: index 7 is out of range for a Vector of length 5`, at the
  line of the call. Use `get`
  when the index comes from data and may miss, and `at` and `set` when the
  program guarantees it (§17 *Traps, `Maybe` and `Result`*)
- `#[..v, x]` is `Vector.concat(v, #[x])` and `#[x, ..v]` is
  `Vector.concat(#[x], v)`, written as a literal. A vector takes its spread at
  either end, since appending and prepending are both a concatenation; a list
  takes it last (`[x, ..xs]`) and a map or set first (`%{..m, k => v}`,
  `%[..s, x]`). One spread per literal: a `..` between elements is `vector
  spread ".." must be the first or the last element`, and a second spread
  (`#[..a, ..b]`) is `a vector literal takes one spread ".."; join two vectors
  with \`Vector.concat\``, since `Vector.concat(a, b)` already says it. The
  spread must be a vector (`vector spread ".." requires a vector, got
  List<String>`, whose help names `Iter.to_vector`), each element has its
  element type, with the coercions a vector literal's elements take, and an
  empty `#[]` spread takes the elements' type. A spread with no elements is
  `a vector spread with no elements is its base value; write \`v\``. The
  spread is evaluated first, then the elements from left to right, as in a
  list spread. At a field or key of an update the spread may be a bare `..`,
  the vector already there (§7 *Nested updates*): `#[.., x]` is
  `Vector.concat(old, #[x])` and `#[x, ..]` is `Vector.concat(#[x], old)`. A
  missing map key starts from `#[]`.
- Generic transforms still go through `Iter`, then materialize explicitly when
  needed: `1..=3 |> Iter.map(f) |> Iter.to_vector()` builds the vector in one
  pass with no intermediate list
- A vector pattern takes a vector apart from either end: `#[first, ..rest]`,
  `#[.., last]`, `#[a, ..mid, z]`, `#[a, b, ..]` and the exact `#[a, b]`
  (§10 *Vector Destructuring*). The rest is a window onto the vector's
  storage, so binding it copies nothing

### Set

Collection of unique values, backed by `Map`. Elements may be any keyable value,
same as map keys. Iteration follows first-occurrence insertion order.

```nomi
roles = %["admin", "editor"]

empty: Set<String> = %[]
```

- Literal syntax: `%[1, 2, 3]`, the `%` and the elements in brackets. The
  empty set is `%[]`, and there is no empty constructor. A type over a set writes its name after the `%`,
  `%Tags["a"]`
- Elements equal at run time make one element; the same constant given twice
  (`%[1, 2, 1]`) is a compile error, by the rule for map keys (Map, *Repeated
  keys*)
- An empty `%[]` takes its element type from the expected type, as `[]` does;
  with none, its binding needs an annotation
- Homogeneous — all elements must be the same type
- Immutable — operations return new sets
- `Set.insert`, `Set.remove`, `Set.contains?` (the `Contains` impl, a hash
  lookup), `Set.union`, `Set.intersection`, `Set.difference`, `Set.subset?`,
  and `Set.size` are container-specific operations
- `a + b` is `Set.union(a, b)` and `a - b` is `Set.difference(a, b)`; both
  operands are sets, so one element is `s + %[x]`
- `%[..s, x]` is `Set.union(s, %[x])` written as a literal: one spread, first,
  as a map update's (`set spread ".." must be the first element`), so the
  base's elements keep their places and new ones follow. The spread goes
  first because `%[..s, x]` is one insert into `s`, while putting elements
  before an existing set would rebuild it; write `Set.union(%[x], s)` for that
  order. It requires a set
  (`set spread ".." requires a set, got List<Int>`) and at least one element
  (`a set spread with no elements is its base value; write \`s\``). At a field
  or key of an update, the bare `%[.., x]` adds to the set already there (§7
  *Nested updates*); a missing map key starts from `%[]`
- `Set<T>` implements `Equatable` and `Hashable`, but not `Comparable`
- A set pattern tests membership: `%["admin", ..rest]` matches a set holding
  `"admin"` and binds the other elements, `%["admin", ..]` only tests, and
  `%[1, 2]` matches exactly that set (§10 *Set Destructuring*). It never
  covers every set, so a `case` needs a `_` arm

### Map

Key-value collection. Any keyable value can be a key (safe because all values are immutable). Functions and values holding functions cannot be keys since they don't support `==`.

```nomi
ages = %{"Alice" => 30, "Bob" => 25}

name = Map.get(ages, "Alice") // Maybe<Int>

updated = Map.put(ages, "Carol", 28) // new map with Carol added

updated = Map.merge(ages, %{"Alice" => 31, "Bob" => 26}) // new map with values changed
```

- Literal syntax: `%{key => value}`. The empty map is `%{}`, which takes its key and value types from the expected type; there is no empty constructor. A map-distinct type or a map-payload variant writes its name after the `%`: `%Kvs{"a" => 1}`, `%Json.Obj{"k" => v}`, `%.Obj{"k" => v}` (§15 *Distinct Types Over Tuples and Maps*).
- `%{` and `%Name{` always open a map literal and `#{` and `#Name{` a struct literal, so `%Kvs{}` is always the empty map and `#Point{}` always a struct. A plain `{` is a block, never a map, and a name followed by `{` never starts a literal (`Map{"a" => 1}` is a parse error). Where an operand begins, `%{`, `%[` and `%Name` start a literal, written with no space after the `%`; after an operand, `%` is the remainder operator (`a % b`).
- Keys are always expressions — string literals require quotes, bare identifiers are evaluated as variables, and any keyable value works (tuples, variants, lists, structs, distinct types), not just scalars
- `Map.get` returns `Maybe` since key might not exist
- `Map.put` adds a single key-value pair, `Map.merge` combines two maps (second map wins on conflicts)
- `a + b` is `Map.merge(a, b)`; both operands are maps, so one entry is `m + %{k => v}`. There is no `Map - Map`
- Operations return new maps
- Maps iterate in insertion order. `Map.put(m, k, v)` appends the entry at the end when `k` is new, or preserves position when `k` already exists (only the value is updated). `Map.merge(a, b)` preserves `a`'s order, then appends any new keys from `b` in `b`'s order.
- `%{..m, k => v}` is `Map.merge(m, %{k => v})`, written as a literal (see *Map update by spread* below)

```nomi
%{"name" => "Alice"} // string literal key

%{key => "Alice"} // variable key — evaluates key

%{String.to_lower(k) => "Alice"} // any expression as key

%{#(1, 2) => "origin"} // composite key — tuple

%{Color.Red => "danger"} // composite key — variant

%{#Point{x: 1, y: 2} => "p"} // composite key — struct
```

#### Map update by spread

`%{..base, key => value}` is `Map.merge(base, %{key => value})` written as a literal: the base's entries, then the literal's, a literal key replacing the base's value and keeping its position, a new key appended in the order written.

```nomi
defaults = %{"theme" => "light", "font" => "mono"}

prefs = %{..defaults, "theme" => "dark", "size" => "12"}
// %{"theme" => "dark", "font" => "mono", "size" => "12"}
```

The rules mirror the struct spread's (§7 *Struct update by spread*):

- **Exactly one spread, and it is the first element.** A spread after an entry, or a second spread, is `map spread \`..\` must be the first element`.
- **At least one entry follows it.** `%{..m}` is `a map spread with no entries is its base value; write \`m\``.
- **The result has the base's type.** The base must be a `Map<K, V>` (`map spread \`..\` requires a map, got Point` otherwise), and each entry's key must be a `K` and its value a `V`, with the coercions any map literal's entries get from an expected type: `None` for a `Maybe<V>`, `[]` for a `List<T>`. A key or value of the wrong type is `map spread key type mismatch: expected String, got Int` (or `value`). A base whose types are still open, such as `%{}`, takes them from the entries: `%{..%{}, "a" => 1}` is a `Map<String, Int>`.
- **A key may repeat the base's; it may not repeat the literal's.** Replacing a base key is the point of the form, but two equal constant keys among the literal's entries are the error *Repeated keys* below describes.

**An entry replaces the value at its key whole**, even when that value is itself a map, unless the value is written with a bare `..` (§7 *Nested updates*). The `..` stands for the value already at the key, so only the entries or fields written change; where the base lacks the key, it starts from an empty collection or builds a struct from its field defaults:

```nomi
team = %{"home" => %{"city" => "Atlanta", "arena" => "State Farm"}}

moved = %{..team, "home" => %{.., "city" => "Decatur"}, "away" => %{.., "city" => "Leeds"}}
// %{"home" => %{"city" => "Decatur", "arena" => "State Farm"}, "away" => %{"city" => "Leeds"}}
```

The same works at a struct's map field: `#{..cfg, prefs: %{.., "theme" => "dark"}}`.

`m + %{k => v}` and `Map.merge(m, other)` say the same thing, and both stay shallow: a bare `..` in either operand has no value to update. The spread reads best when the entries are written out; `+` and `Map.merge` when the other map is a value already in hand.

```nomi
import std/io

struct Settings {
    name: String
    prefs: Map<String, String>
}

fn with_entry<K, V>(m: Map<K, V>, k: K, v: V) -> Map<K, V> {
    %{..m, k => v}
}

fn main() {
    base = %{"a" => 1, "b" => 2}
    io.print(%{..base, "b" => 20, "c" => 3})
    s = #Settings{name: "x", prefs: %{"theme" => "light", "font" => "mono"}}
    dark = #{..s, prefs: %{.., "theme" => "dark"}}
    io.print(dark.prefs)
    teams = %{"home" => %{"city" => "Atlanta"}}

    io.print(
        %{..teams, "home" => %{.., "arena" => "State Farm"}, "away" => %{.., "city" => "Leeds"}}
    )

    io.print(with_entry(%{1 => "one"}, 2, "two"))
    scores: Map<String, Maybe<Int>> = %{"ada" => Some(3)}
    io.print(%{..scores, "bob" => None})
}
```

#### Repeated keys

Keys are evaluated at run time, so two entries of one literal can have equal
keys. The map holds that key once: the later entry's value wins, and the key
keeps the earlier entry's position, as `Map.put` would place it.

```nomi
x = 1

y = 1

m = %{x => "a", 2 => "c", y => "b"} // %{1 => "b", 2 => "c"}
```

When two keys are the same constant, the earlier entry can never be read, so
the literal is a compile error at the second key:
`` key `1` is given twice in this map literal; the later entry would replace the earlier ``.
A constant here is a value whose equality to another constant is certain at
compile time:

- an `Int`, `Float`, `Decimal`, `String` or `Codepoint` literal, or a negated
  numeric literal, compared by value: `255` and `0xFF` are one key, `1.0` and
  `1.00` are one key, `0.0` and `-0.0` are one key (keys match by `==`), and
  `1.0d` and `1.00d` are one key;
- `True` and `False`;
- a variant with no payload (`None`, `Direction.North`, `.North`) or a variant
  constructor applied to constants (`Some(1)`), when the enum has no
  `impl Equatable` of its own, written or derived, or std declares it;
- a tuple of constants (`#(1, "a")`).

Any other key, a variable, a call, a struct or a value of a type with its own
`impl Equatable`, is never compared at compile time, and the later entry wins.
The same rule holds for a set literal (`%[1, 2, 1]` is
`` element `1` is given twice in this set literal; a set holds it once ``)
and for a map pattern, where both entries would match the one value
(`%{"k" => a, "k" => b}` is
`` key `"k"` is given twice in this map pattern; match its value once ``).
A set literal's elements equal only at run time make one element.

#### Map Functions

```nomi
// Construction & access
fn get(m: Map<K, V>, key: K) -> Maybe<V>
fn put(m: Map<K, V>, key: K, value: V) -> Map<K, V>
fn remove(m: Map<K, V>, key: K) -> Map<K, V>
fn merge(a: Map<K, V>, b: Map<K, V>) -> Map<K, V>
fn keys(m: Map<K, V>) -> List<K>
fn values(m: Map<K, V>) -> List<V>
fn contains?(c: Map<K, V>, x: K) -> Bool          // Contains<K> impl: x is a key
fn size(m: Map<K, V>) -> Int                       // O(1); the generic count is Iter.count
fn map_next(m: Map<K, V>) -> Maybe<#(#(K, V), Map<K, V>)> // first entry and the rest

// Map-specific transformations — generic map/filter can't keep the other half
// of a pair (or could collide keys), so these stay on Map
fn map_values(m: Map<K, V>, f: (V) -> U) -> Map<K, U>
fn map_keys(m: Map<K, V>, f: (K) -> J) -> Map<J, V>
```

Maps iterate in insertion order — preserved by every transformation. A map element is a `#(K, V)` pair, so every generic `Iter` op (`Iter.reduce` / `Iter.each` / `Iter.find` / `Iter.filter` / …) takes a single tuple parameter. To get a `Map` back from a lazy pipeline, materialize with `Iter.to_map`; filtering or generic mapping a map is `Iter.filter(m, p) |> Iter.to_map()`.

`Map<K, V>` implements `Iter<#(K, V)>`, so every `Iter` owner function works on maps. To get a `Map` back from a lazy pipeline, use `Iter.to_map`:

```nomi
// Filter a map — Iter.filter returns Iter<#(K, V)>, materialize with Iter.to_map
ages
|> Iter.filter(fn #(_, age) -> age > 18)
|> Iter.to_map()
```

### Range

A comparable range, written with `..` (half-open) or `..=` (closed). Both forms require a left and right operand of the same `Comparable` type (see §32 Ranges). Unbounded ranges are integer-only and built with `Range.from(n)` / `Range.naturals()`. `Range<T>` implements `Iter<T>` when `T` also implements `Discrete`, so `Int` and `Codepoint` ranges work with `Iter` owner functions without allocating a list; ranges over successor-less types like `String`, `Float`, and `Decimal` are interval values for queries like `Range.contains?`.

```nomi
Iter.known_count(1..5) // Some(4)

Iter.known_count(1..=5) // Some(5)

Iter.known_count(Range.from(1)) // None — unbounded

Range.contains?(1..5, 3) // True

Range.contains?("a".."m", "h") // True

Range.contains?(1.25d..=2.50d, 1.50d) // True

Iter.to_list(1..5) // [1, 2, 3, 4]

Iter.to_list(1..=5) // [1, 2, 3, 4, 5]

// Lazy consumption via Iter — no intermediate list
1..1_000_000
|> Iter.filter(fn n -> n % 7 == 0)
|> Iter.take(5)
|> Iter.to_list() // [7, 14, 21, 28, 35]

// Unbounded range bounded by Iter.take
Range.from(1) |> Iter.take(5) |> Iter.to_list() // [1, 2, 3, 4, 5]
```

#### Range functions (container-specific)

```nomi
fn contains?(c: Range<T>, x: T) -> Bool    // Contains impl: two comparisons
fn bounded?<T>(r: Range<T>) -> Bool
fn from(n: Int) -> Range<Int>              // unbounded ascending from n
fn naturals() -> Range<Int>                // unbounded from 0
fn step_by<T, S>(r: Range<T>, by: S) -> Iter<T> where T: Comparable and Steppable<S>
```

Plus the `Iter` protocol for ranges whose endpoint type implements `Discrete`: `Iter.known_count(r)` reports `Some(n)` for bounded discrete ranges that can count steps and `None` for an unbounded one (the safe, non-consuming bounded count). Everything else is generic over iterables — materialize with `Iter.to_list(r)` (traps on an unbounded range, which is `Iter.infinite?`; bound with `Iter.take` first), count with `Iter.count(r)`.

### Iter Interface

The single iteration protocol is `Iter<T>`, defined in `std/iter`. It is pure protocol — three functions, no algorithms:

```nomi
interface Iter<T> {
    fn each_while(collection: self, yield: (T) -> Bool) -> Bool

    // Some(n) if this iterator can report its length without consuming it
    // (a type that stores its count); None otherwise (lazy pipeline) or for an
    // unbounded source. Iter.count consults it for an O(1) count.
    open fn known_count(_collection: self) -> Maybe<Int> {
        None
    }

    // True if this iterator never ends on its own, so a terminal that must
    // reach its end could never answer. False means it may end.
    open fn infinite?(_collection: self) -> Bool {
        False
    }
}
```

`each_while` walks the collection and pushes each element to `yield`; returning `False` from `yield` asks the source to stop, and `each_while` itself answers `True` when the source ran out and `False` when a consumer stopped it. That single Bool is the whole early-termination mechanism: `Iter.take(3)` over an infinite source stops it by refusing a fourth element, and `find`/`any?`/`all?`/`first` stop at the first decisive one. Because `each_while` reads a value it never mutates, Nomi iterators are replayable. An `Iter<T>` is an ordinary value: a binding, a parameter or result, a struct field, a tuple or record part, a `Some`/`Ok` or enum payload, a list element and a closure capture can all hold one. Holding it runs nothing, and each consumer runs it from its start, so a field read twice with `Iter.to_list` yields the same list twice. Any source can enter a position declared `Iter<T>` — a `List`, `Vector`, `Set`, `Map`, `String`, `Bytes`, range or a user type that implements `Iter` — and it keeps its own `known_count` there: `Iter.known_count(xs)` for an `xs: Iter<Int>` holding `[1, 2, 3]` is `Some(3)`. `List`, `Vector`, `Map`, `String` (via the grapheme iterator), and ranges over `Discrete` values all implement `Iter<T>` — so they feed the `Iter` owner functions directly. `List`/`Vector`/`Map`/`Set` and bounded ranges whose element type can count steps override `known_count`; everything else inherits the `None` default.

**Infinite iterators.** `infinite?` says that an iterator never ends. `Iter.repeat`, `Iter.from`, `Iter.iterate` (whose step cannot `break`), `Range.from` and `Range.naturals` (unless the start is within 2^48 of `Int.max_value`, where the range ends), `Range.step_by` of an unbounded range whose step moves, and `Iter.cycle` of a source that is infinite or knows a non-zero count answer `True`; a user type answers `False` unless its `impl Iter` writes `infinite?`. The adapters carry the answer when the pipeline is built, so it costs nothing per element:

| Adapter | Infinite when |
|---|---|
| `map`, `filter`, `drop`, `drop_while`, `with_index`, `scan`, `chunk_by`, `flat_map` | the source is, unless the callback can `break` |
| `chunks(n)` | the source is and `n > 0` |
| `cycle` | the source is, or knows a count above zero |
| `concat(a, b)` | either is |
| `zip(a, b)` | both are |
| `take`, `take_while` | never |

`filter` of an infinite source stays infinite even when nothing passes, since driving it to its end never finishes either way. `flat_map` is infinite only through its outer source: an inner iterator is not known until it runs. When in doubt the answer is `False`, so a pipeline that might end is never refused. `Iter.take(n)` also answers `known_count`: `n` over an infinite source, and the lesser of `n` and the source's count when it has one.

A terminal that has to reach the end of its source asks `infinite?` before taking anything from it and traps on `True` (§17 *Traps, `Maybe` and `Result`*), naming the terminal the program called and the source that never ends, at the program's own line:

```text
line 3: `Iter.to_list` of an infinite iterator (`Iter.repeat`) never finishes; limit it with `Iter.take` or `Iter.take_while`
```

Those terminals are `to_list`, `to_vector`, `to_set`, `to_map`, `count`, `last`, `reverse`, `flatten`, `sort`, `sort_by`, `sort_with`, `partition`, `group_by`, `frequencies`, `min`, `max`, `min_by`, `max_by`, `sum`, `String.join`, `Maybe.values`, `Result.partition`, `Task.spawn_all`, `Supervisor.spawn_all`, and `reduce`, `each` and `map_reduce` when their callback cannot `break`. The terminals that may stop early do not trap: `first`, `find`, `any?`, `all?`, `contains?`, `get`, `empty?`, `not_empty?`, `each_while`, `Maybe.collect`, `Result.collect`, and a `reduce`, `each` or `map_reduce` whose callback can `break`. An iterator that never ends without saying so (a user type that does not write `infinite?`, a `take_while` whose predicate never fails) still runs until it is stopped.

When the whole pipeline is written where the terminal is, the checker reports the trap as a compile error instead, with the same words, at the terminal and naming the source:

```nomi
fn coins(coin: Int) -> List<Int> {
    coin
    |> Iter.repeat()
    |> Iter.to_list() // error: `Iter.to_list` of an infinite iterator (`Iter.repeat`) never finishes; ...
}
```

It follows the rules above through pipes, nested calls (`Iter.to_list(Iter.map(Iter.from(0), f))`) and local bindings whose value is such a pipeline, through any number of them (`xs = Iter.from(0)`, then `ys = Iter.map(xs, f)`, then `Iter.sum(ys)`). It reads only what the source spells: a `Range.from` whose start is an `Int` literal at least 2^48 below `Int.max_value`, negative included, `Range.step_by` of such a `Range.from` starting between -2^60 and 2^60, or of `Range.naturals`, by a literal step between -1024 and 1024 other than 0, `Iter.chunks` with a literal size above 0, and `Iter.cycle` of an infinite source or of a non-empty list, vector, set or map literal. A callback with `break` or `continue` in it, or one it cannot see into, ends the analysis, as `take` and `take_while` do. A parameter, a field, a `once`, a function's result and a user `impl Iter` are not followed, and the runtime trap still catches them. The language server offers to put `|> Iter.take(todo)` before such a pipe stage.

Implementing it means walking your own structure and stopping as soon as `yield` says to:

```nomi
impl Iter for Tree {
    fn each_while(t: Tree, yield: (Int) -> Bool) -> Bool {
        case t {
            .Leaf ->
                True

            .Node(left, value, right) ->
                if each_while(left, yield) {
                    if yield(value) {
                        each_while(right, yield)
                    } else {
                        False
                    }
                } else {
                    False
                }
        }
    }
}
```

**`zip` is the one adapter push cannot express directly** — two sources must advance in lockstep, and only one of them can own the loop. `Iter.zip` therefore runs its right operand through a pull cursor (Nomi's equivalent of Go's `iter.Pull`), which costs a coroutine and a handoff per element. It is the only place that cost is paid, and it buys `zip` over sources that are unbounded on either side.

### Generic vs. container-specific

Nomi separates iteration by one rule:

> **Generic over any iterable → `Iter.X`. Specific to a container's structure → that container's owner.**

1. **Generic ops** live on `Iter` over any `Iter<T>` — every transform, filter, reduce, sort, count, index access, and materializer. The adapters (`map`/`filter`/`take`/…) are lazy: each returns a new iterator so pipelines fuse without building intermediate collections. The terminals (`reduce`/`find`/`sort`/`count`/…) and materializers (`to_list`/`to_vector`/`to_set`/`to_map`/`reverse`) consume the iterator. Strings are joined with `String.join`, which takes any `Iter<String>`.
2. **Container-specific ops** stay on the container owner — exactly the work a generic adapter can't express because it reads or rebuilds the container's structure: `Map.map_values`/`map_keys`/`merge`/`get`/`put`/`size`, `Set.union`/`intersection`/`difference`/`insert`/`size`, `List.concat`/`head`/`tail`, `Vector.get`/`at`/`push`/`set`/`concat`/`length`, `String.split`/`replace`/`slice`/`join`, native `String.reverse`.

There are **no eager collection-adapter functions** (`List.map`, `Set.filter`, … don't exist): to get a container back from a chain, append an explicit materialize step — `Iter.map(xs, f) |> Iter.to_list()`, `Iter.filter(s, p) |> Iter.to_set()`. The cost (one allocation) is then visible at the call site.

### `std/iter`

The `std/iter` file hosts the `Iter<T>` interface and its owner functions. The protocol surface is only `each_while` (required) and the two `open` defaults `known_count` (`Maybe<Int>`; `None` unless a type stores its size) and `infinite?` (`Bool`; `False` unless the source never ends). Every generic iteration op lives on `Iter`, reached `Iter.X(...)`:

- **Adapters** (lazy, return `Iter<...>` — one closure over the upstream's push loop, allocated when the chain is built rather than rebuilt per element, so pipelines fuse): `map`/`filter`/`take`/`drop`/`take_while`/`drop_while`/`flat_map`/`zip`/`cycle`/`concat`/`with_index`/`chunks`/`chunk_by`.
- **Terminals** (consume, produce a scalar): `reduce`/`find`/`each`/`any?`/`all?`/`contains?`/`empty?`/`not_empty?`/`first`/`last`/`get`/`count`/`min`/`max`/`min_by`/`max_by`/`sum`/`sort`/`sort_by`/`sort_with`/`partition`/`group_by`/`frequencies`.
- **Materializers / constructors** (consume or seed): `to_list`/`to_vector`/`to_set`/`to_map`/`reverse`/`flatten`/`from`/`repeat`/`iterate`.

Because they're owner functions, `Iter.map` / `Iter.reduce` are not dispatch calls — the protocol functions remain `Iter.each_while`, `Iter.known_count` and `Iter.infinite?`. `Iter.count` is O(1) on any source that stores or can compute its count (`List`/`Vector`/`Map`/`Set`/bounded countable ranges, via `known_count`) and an O(n) fold otherwise; like every full-consuming terminal it traps on an infinite source (*Infinite iterators* above) — bound it with `Iter.take(n)` first.

**Infinite constructors** (return iterators, consume nothing):

```nomi
fn from(start: Int) -> Iter<Int>          // start, start+1, start+2, ...
fn repeat(x: T) -> Iter<T>                // x, x, x, ...
fn iterate(seed: T, step: (T) -> T) -> Iter<T>   // seed, step(seed), step(step(seed)), ...
```

**Non-terminal ops** (return iterators — lazy, fuse with other iter ops):

```nomi
fn map(source: Iter<T>, f: (T) -> U) -> Iter<U>
fn scan<T, U>(source: Iter<T>, f: (U, T) -> U) -> Iter<U>   // f is a lambda whose first parameter has a default; yields each state after it
fn filter(source: Iter<T>, f: (T) -> Bool) -> Iter<T>
fn flat_map(source: Iter<T>, f: (T) -> Iter<U>) -> Iter<U>
fn take(source: Iter<T>, n: Int) -> Iter<T>
fn take_while(source: Iter<T>, f: (T) -> Bool) -> Iter<T>
fn drop(source: Iter<T>, n: Int) -> Iter<T>
fn drop_while(source: Iter<T>, f: (T) -> Bool) -> Iter<T>
fn cycle(source: Iter<T>) -> Iter<T>
fn concat(a: Iter<T>, b: Iter<T>) -> Iter<T>
fn with_index(source: Iter<T>) -> Iter<#(Int, T)>
fn zip<A, B>(a: Iter<A>, b: Iter<B>) -> Iter<#(A, B)>
fn chunks(source: Iter<T>, size: Int) -> Iter<List<T>>
fn chunk_by<T, K>(source: Iter<T>, key_fn: (T) -> K) -> Iter<List<T>> where K: Equatable
```

**Terminal ops** (consume the iterator, produce a scalar):

```nomi
fn reduce<T, U>(source: Iter<T>, f: (U, T) -> U) -> U   // f is a lambda whose first parameter has a default
fn map_reduce<T, U, A>(source: Iter<T>, f: (A, T) -> #(U, A)) -> #(List<U>, A)   // seeded as reduce; the outputs in order and the final state
fn each(source: Iter<T>, f: (T) -> Unit) -> Unit
fn find(source: Iter<T>, f: (T) -> Bool) -> Maybe<T>
fn any?(source: Iter<T>, f: (T) -> Bool) -> Bool
fn all?(source: Iter<T>, f: (T) -> Bool) -> Bool
fn contains?<T>(source: Iter<T>, x: T) -> Bool where T: Equatable   // an element == x; stops at the first
fn empty?(source: Iter<T>) -> Bool
fn not_empty?(source: Iter<T>) -> Bool
fn first(source: Iter<T>) -> Maybe<T>
fn last(source: Iter<T>) -> Maybe<T>
fn get(source: Iter<T>, index: Int) -> Maybe<T>   // O(index); None past the end
fn count(source: Iter<T>) -> Int                   // O(1) via known_count; else O(n) fold
fn min<T>(source: Iter<T>) -> Maybe<T> where T: Comparable   // None when empty; the first of equal least elements
fn max<T>(source: Iter<T>) -> Maybe<T> where T: Comparable   // None when empty; the first of equal greatest elements
fn min_by<T, K>(source: Iter<T>, key: (T) -> K) -> Maybe<T> where K: Comparable   // least key; ties as min
fn max_by<T, K>(source: Iter<T>, key: (T) -> K) -> Maybe<T> where K: Comparable   // greatest key; ties as max
fn sum<T>(source: Iter<T>) -> T where T: Add<T, T> and Summable   // T.empty_sum() when empty
fn sort<T>(source: Iter<T>, direction: Direction = Direction.Ascending) -> List<T> where T: Comparable
fn sort_by<T, K>(source: Iter<T>, direction: Direction = Direction.Ascending, key: (T) -> K) -> List<T> where K: Comparable
fn sort_with(source: Iter<T>, compare: (T, T) -> Ordering) -> List<T>
fn partition(source: Iter<T>, f: (T) -> Bool) -> #(List<T>, List<T>)
fn group_by<T, K>(source: Iter<T>, key_fn: (T) -> K) -> Map<K, List<T>>
fn frequencies<T>(source: Iter<T>) -> Map<T, Int>      // keys in first-appearance order
```

**Materializers** (consume the iterator, produce a concrete collection):

```nomi
fn to_list(source: Iter<T>) -> List<T>             // identity-preserving for a List (O(1))
fn to_vector(source: Iter<T>) -> Vector<T>         // one pass, sized by known_count; identity for a Vector
fn to_set(source: Iter<T>) -> Set<T>
fn to_map(source: Iter<#(K, V)>) -> Map<K, V>       // requires #(K, V) pair elements
fn reverse(source: Iter<T>) -> List<T>
fn flatten(source: Iter<Iter<U>>) -> List<U>
```

To join an `Iter<String>` into one `String`, use `String.join(parts, separator)`.

**Starting values.** `Iter.reduce` starts from `from`, or from the default of its callback lambda's first parameter: `Iter.reduce(xs, Int.add, from: 0)` and `Iter.reduce(xs, fn sum = 0, x -> sum + x)` are the same fold, 0 for an empty `xs`. `from:` works with any callback: a named function, a capture, a lambda without a default. Exactly one of the two gives the starting value. A call with neither is a compile error at the callback, "`Iter.reduce` needs a starting value", whose help names both spellings (`` give it with `from:`: `Iter.reduce(xs, add, from: <start>)` ``, and for a lambda also its accumulator's default): a function value (`Iter.reduce(xs, Int.add)`, `xs |> Iter.reduce(add)`), a lambda whose first parameter has no default (`fn a, b -> a + b`), a capture (`&add(&1, &2)`), a callback slot left open (`&Iter.reduce(xs, &1)`) and `Iter.reduce` named as a value. A call with both is "`Iter.reduce` has two starting values: `from:` and the default of the callback's first parameter", at `from:`, with the help "keep one: remove `from:`, or remove the default from `acc`". `from` is given by name: `Iter.reduce(xs, 0, add)` is "`Iter.reduce` takes its starting value by name". The callback is the positional argument after the source wherever `from:` is written, so `Iter.reduce(xs, from: 0, add)` is the same call. There is no first-element fallback, so a reduction never fails at run time for an empty source. For the least or greatest element, which has no answer for an empty source, use `Iter.min` and `Iter.max`: each answers a `Maybe`, `None` when the source is empty.

`Iter.scan` and `Iter.map_reduce` take their starting value the same way, `from:` or the callback lambda's first-parameter default, and the errors are the same with their own names ("`Iter.scan` needs a starting value"). `Iter.scan` is lazy: it runs the fold and yields each state after the starting value, one per element, so an empty source yields nothing. `Iter.map_reduce` is eager: its callback answers `#(output, next_state)` for each element, and the call answers the outputs in order with the final state, `#([], start)` for an empty source.

```nomi
[3, 1, 4] |> Iter.scan(fn total = 0, x -> total + x) |> Iter.to_list() // [3, 4, 8]

["a", "bb"] |> Iter.map_reduce(fn at = 0, s -> #(#(at, s), at + String.length(s))) // #([#(0, "a"), #(1, "bb")], 3)
```

`Iter.sum(xs)` adds the elements with `+`, left to right, from
`T.empty_sum()`, so it is `Iter.reduce(xs, fn total = T.empty_sum(), x -> total +
x)` exactly, Float rounding included, and an Int total past the Int range traps
as `+` does. Its bound is `where T: Add<T, T> and Summable` (§22), so an empty
source sums to the type's empty sum, and a type without the two impls is a
compile error at the call: `Iter.sum([True])` reports `` Bool does not implement
Summable (required by `where T: Summable`) ``. Summing Maps follows Map's `+`:
a later map wins on a shared key.

```nomi
Iter.reduce([3, 1, 2], fn sum = 0, n -> sum + n) // 6

Iter.sum([3, 1, 2]) // 6

Iter.sum(["a", "b"]) // "ab"

Iter.min([3, 1, 2]) // Some(1)

Iter.max(["b", "c", "a"]) // Some("c")

Iter.max([]) // None
```

`Iter.to_map` and `String.join` enforce their element-type constraints at compile time, not just runtime. Each `impl Iter for X { ... }` block registers `X`'s element type `E` with the type checker; when a parameter's element-type slot is `#(K, V)` or `String`, the unifier rejects sources whose `E` doesn't fit:

```nomi
Iter.to_map([1, 2, 3]) // type error: Iter<#(K, V)> expected, got Iter<Int>

String.join([1, 2, 3], ", ") // type error: Iter<String> expected, got Iter<Int>
```

This works for built-in iteration sources (`List`, `Vector`, `Map`, `String`, `Range`) and for any user-defined `Iter` impl — the unifier consults file-local impl element types first, then the package-wide stdlib registry.

`break` and `continue` work in all `Iter` callbacks — same semantics as everywhere else (see §12). Short-circuit terminals (`find`, `any?`, `all?`, `empty?`, `first`) terminate on infinite sources when the predicate permits; full-consuming terminals (`count`, `reduce`, `last`, `sort`, `to_list`, …) trap on one that is `Iter.infinite?` and never finish on one that never ends without saying so — bound the source with `Iter.take(n)` first.

`Iter.zip` truncates to the shorter iterator:

```nomi
Iter.zip([1, 2, 3], ["a", "b"]) |> Iter.to_list() // [#(1, "a"), #(2, "b")]
```

### Custom Iters

Any type can implement `Iter<T>` and immediately work with every `Iter` owner function. Implement `each_while` to walk your own structure, pushing each element to `yield` and stopping as soon as it returns `False`:

```nomi
// File: countdown.nomi
pub struct Countdown {
    remaining: Int
}

impl Iter for Countdown {
    fn each_while(c: Countdown, yield: (Int) -> Bool) -> Bool {
        if c.remaining <= 0 {
            True
        } else {
            if yield(c.remaining) {
                each_while(#Countdown{remaining: c.remaining - 1}, yield)
            } else {
                False
            }
        }
    }
}

pub fn new(n: Int) -> Countdown {
    #Countdown{remaining: n}
}

```

Once `Iter<T>` is implemented, every `Iter` owner function works on it. Non-terminal ops still return iterators — materialize with `Iter.to_list` (or a similar terminal) when you need a concrete value:

```nomi
countdown.new(5) |> Iter.map(fn n -> n * 10) |> Iter.to_list() // [50, 40, 30, 20, 10]

countdown.new(4) |> Iter.filter(fn n -> n > 2) |> Iter.to_list() // [4, 3]

countdown.new(3) |> Iter.sum() // 6

countdown.new(5)
|> Iter.filter(fn n -> n % 2 == 0)
|> Iter.map(fn n -> "${n} bottles")
|> Iter.to_list()
// ["4 bottles", "2 bottles"]
```

Each lazy adapter returns a private wrapper struct that itself implements `Iter`, so a chain of adapters fuses into one pass, and `break`/`continue` inside an adapter's callback propagate through the fused pipeline to the consuming terminal.

### Tuples

Tuples are fixed-size, positional and heterogeneous; see §7 *Tuples*.

### Anonymous Structs

Anonymous structs always have named fields. See §7 for full syntax.

```nomi
point = #{x: 1.0, y: 2.0}

point.x // 1.0
```

## 20. Concurrency

Structured concurrency — every concurrent task has an explicit owner. No fire-and-forget. Full immutability means no data races — channels only pass immutable values.

Layer 1 covers the foundational primitives: `concurrent { }` blocks, `Task.spawn` / `Task.await` / `Task.await_all` and `Task<T>` in `std/tasks`, the `std/channels` module, and `timer.sleep`. Layer 2 is named supervisors: `std/supervisors` owns long-lived background work with restart policies, shared concurrency bounds, and shutdown draining.

### concurrent

`concurrent` is a keyword that creates a structured-concurrency scope. It is a block expression (not a function call) — its value is the last expression in the block. Control flow inside the block is lambda-scoped: `return` / `try` / `break` / `continue` exit the block, not the enclosing function. Any non-normal exit cancels every task still running in the block before propagating outward. All tasks are guaranteed to complete (or be cancelled) before the block returns.

A failed `assert` or `refute` in the block's own body exits the block the same way, as `Err(AssertionFailure)`, so a block holding one must produce `Result<_, AssertionFailure>`; any other block value is a compile error at the assertion. Inside a test, a block that is the body's final value then fails the case with the assertion's report. A spawned task's body is a lambda, and an assertion in a lambda is a compile error: assert in a `fn` that returns `Result<_, AssertionFailure>`, spawn that, and the failure is the task's `Completed(Err(...))`, which the code that awaits it handles. This is Go's rule that a test fails only from its own goroutine (`t.FailNow` from another one is invalid) and Rust's, where a panic in a spawned thread reaches the test only through `join`.

```nomi
result = concurrent {
    user = Task.spawn(fn -> fetch_user(42))
    order = Task.spawn(fn -> fetch_order(101))
    Ok(#(try Task.await(user), try Task.await(order)))
}
// only reached when both tasks are done
```

### Task

`Task<T>` is the opaque handle returned by `Task.spawn`. It carries a single type parameter — the body's return type. Errors-as-values via `Result<T, E>` are the universal Nomi idiom, so a fallible task body returns `Result<T, E>` and `try Task.await(t)` propagates errors the same way `try fetch_user(42)` does. Besides `Task.await`, a handle can be cancelled with `Task.cancel` and inspected with `Task.outcome` (see *Failures And Restarts* below).

`Task<T>` lives in `std/tasks`; import it with `import std/tasks.Task`. Task operations are owner functions on `Task`: `Task.spawn(...)`, `Task.await(...)`, `Task.spawn_all(...)`, `Task.await_all(...)`, `Task.outcome(...)`, and `Task.cancel(...)`.

### Task.spawn

`Task.spawn(body)` spawns `body` (a zero-parameter function value, conventionally written as a lambda: `fn -> expr`) as a parallel task in the enclosing `concurrent { }` block. The body runs on its own goroutine; values captured from the enclosing scope are shared immutably. Returns `Task<T>` where `T` is the body's return type.

```nomi
concurrent {
    user = Task.spawn(fn -> fetch_user(42)) // Task<Result<User, String>>
    order = Task.spawn(fn -> fetch_order(101)) // Task<Result<Order, String>>
    ...
}
```

For work that doesn't return `Result`, the task type is the body's return type directly:

```nomi
concurrent {
    sums = Task.spawn(fn -> Iter.map(numbers, fn n -> n * 2) |> Iter.to_list()) // Task<List<Int>>
    ...
}
```

### Task.await

`Task.await(task)` blocks until a task completes and returns its value (`T` from `Task<T>`). For a `Task<Result<T, E>>`, use `try Task.await(t)` to propagate errors:

```nomi
// try — propagate the task's Err, cancel siblings, exit the concurrent block with Err
u = try Task.await(user)

// case — handle the result explicitly, no cancellation
u = case Task.await(user) {
    Ok(val) -> val
    Err(_) -> default_user
}

// return — translate to a custom error, cancel siblings, exit
u = case Task.await(user) {
    Ok(val) -> val
    Err(e) -> return Err("fetch_user failed: " + e)
}
```

### Error propagation

Errors from tasks are values (`Result`), not exceptions. The programmer controls handling per-await:

```nomi
concurrent {
    a = Task.spawn(fn -> fetch_user(42))
    b = Task.spawn(fn -> fetch_order(101))

    // try propagates Err and cancels b as cleanup
    u = try Task.await(a)

    // case handles the error — b is not affected
    o = case Task.await(b) {
        Ok(val) -> val
        Err(_) -> default_order
    }

    Ok(#(u, o))
}
```

### Cancellation visibility

Cancellation has two shapes; user code observes them differently.

- **Internal cancellation** — the `concurrent { }` block exits non-normally (via `try`, `return`, `break`, or `continue`) and cancels every still-running sibling as cleanup. This is invisible to user code: the cancelled task's body just stops executing at the next safe point and its goroutine wrapper unwinds silently. There is no `finally` and no exception type — immutability means there's nothing to clean up at the value level.
- **Ancestor cancellation** — the active app context fires because of an earlier `with App.context = Context.with_timeout(App.context, d)` override or because the embedder cancels the runtime root. Ancestor cancellation surfaces naturally through cancellation-aware operations: `timer.sleep`, `Sender.send` / `Receiver.receive`, and context-aware HTTP requests all `select` on the context's done channel and return an `Err` (or `None`) when the context fires. That outcome becomes the task body's return value — a normal task return — and `try Task.await(t)` propagates it through the concurrent block. Pure-compute code that never reads the context ignores deadlines and runs to completion, matching Go's behavior. The language guarantees cancellation is observed only at those cancellation-aware operations. A program must not depend on where, between two cancellation-aware operations, a cancelled task stops.

Put `with App.context = Context.with_timeout(App.context, d)` before a `concurrent` block to give the whole block a deadline:

```nomi
fn fetch_both() {
    with App.context = Context.with_timeout(App.context, Duration.milliseconds(50))

    concurrent {
        a = Task.spawn(fn -> slow_call())
        b = Task.spawn(fn -> slow_call())
        _ = Task.await(a)
        _ = Task.await(b)
    }
}
```

Exceeding that deadline is not a value the block hands back. The work unwinds at its next safe point and everything after it in the deadline's region, the rest of the block holding the `with` line, is skipped. A check written after the `concurrent` block in the same block cannot report the timeout: it sits inside the region the deadline covers and unwinds along with the work. To observe which happened, put the deadline inside a spawned task and read `Task.outcome` from outside the region the deadline covers.

### Static guarantees

The analyzer enforces three structural rules at compile time. Together they statically rule out the leaked-goroutine class of bugs:

1. **`Task.spawn` is legal only inside the dynamic extent of a `concurrent { }` block.** Reaches transitively — `Task.spawn(fn -> helper())` where `helper` itself calls `Task.spawn` is fine *if* the chain bottoms out inside a `concurrent` block. Calls to `Task.spawn` outside any block are rejected at analysis time.
2. **Every `Task<T>` value must be consumed by `Task.await`.** A bound Task whose value is never awaited (or explicitly discarded with `_ = Task.await(t)`) is rejected. Discarding the raw Task with `_ = t` is also rejected because it skips the wait.
3. **`Task<T>` values cannot escape their enclosing `concurrent` block.** No `return t`, no `Sender.send(ch.sender, t)`, no storing in a struct field reachable past the block boundary. Tasks are block-local.

### Channels

Typed FIFO channels for inter-task communication, held as their two halves. The `std/channels` module exports the whole channel, the halves, and the operations.

`Channel<T>` is a struct of a `Sender<T>` and a `Receiver<T>`. The halves are what you hand out; the whole channel is what its creator keeps:

```nomi
import std/channels.{Channel, Receiver, Sender}

ch: Channel<Int> = Channel.unbuffered() // every send is a rendezvous

buffered: Channel<Int> = Channel.buffered(4) // up to 4 pending values

producer: Sender<Int> = buffered.sender

consumer: Receiver<Int> = buffered.receiver
```

Buffered or not is a choice about coupling rather than a size, which is why it is two constructors and not a capacity that may be zero: `Channel.buffered` requires a capacity of at least 1, and `Channel.unbuffered` takes none. There is no `Channel.new` — there is no third thing a channel could be.

`Sender.send` returns `Result<Unit, ChannelClosed>` — fails if the channel was closed at send time. `Receiver.receive` returns `Maybe<T>` — `Some(value)` when data is available, `None` when the channel is closed and drained:

```nomi
Sender.send(ch.sender, value) // Result<Unit, ChannelClosed>

Receiver.receive(ch.receiver) // Maybe<T>

Sender.close(ch.sender) // signals no more values will be sent
```

There is deliberately no `Channel.send` or `Channel.receive`. Operations reachable from the whole channel would let the split be bypassed, and being bypassable is the only way it could fail to pay.

**`close` lives on `Sender` alone.** Closing says nothing more is coming, which is a producer's statement to make; a consumer closing what it reads ends the stream for every producer still writing, and it reads like ordinary cleanup right up until it isn't. Giving a worker a `Receiver<T>` rather than a `Channel<T>` is what makes that unwritable rather than merely discouraged.

Both halves name the same underlying channel — the direction is static, enforced by which of the two a function accepts, and there is nothing at runtime keeping them apart.

Channel ops are cancellation-aware natively: a blocked `send` or `receive` inside a `concurrent` block whose context fires unwinds the goroutine via the silent internal-cancellation path. Ancestor cancellation surfaces through the block's `await` on the main goroutine (see "Cancellation visibility" above).

`ChannelClosed` is a zero-sized distinct type:

```nomi
type ChannelClosed
```

Channels live independent of any specific `concurrent` block — create one inside a block, return it out, and consume it from a later block. The runtime garbage-collects channels when no Nomi reference remains.

#### Example: producer/consumer pipeline

Each worker takes the half it needs, so the consumer cannot send and neither producer nor consumer can close behind the other's back:

```nomi
import {
    std/channels.{Channel, Receiver, Sender}
    std/io
    std/tasks.Task
}

fn produce(out: Sender<Int>, n: Int) -> Unit {
    case Sender.send(out, n) {
        Ok(u) -> u
        Err(_) -> io.print("send failed")
    }
}

fn consume(inbox: Receiver<Int>) -> Int {
    Iter.loop(fn sum: Int = 0 -> {
        case Receiver.receive(inbox) {
            Some(v) -> sum + v
            None -> break sum
        }
    })
}

fn main() -> Unit {
    ch: Channel<Int> = Channel.buffered(4)

    total = concurrent {
        p1 = Task.spawn(fn -> produce(ch.sender, 10))
        p2 = Task.spawn(fn -> produce(ch.sender, 20))
        p3 = Task.spawn(fn -> produce(ch.sender, 30))
        cons = Task.spawn(fn -> consume(ch.receiver))

        // Wait for all producers, then close so the consumer's loop
        // terminates on the drained-channel None.
        Task.await(p1)
        Task.await(p2)
        Task.await(p3)
        Sender.close(ch.sender)
        Task.await(cons)
    }

    io.print("total=" + Display.to_string(total)) // total=60
}
```

#### Multiplexing

To wait on multiple event sources, use a single channel with an enum:

```nomi
concurrent {
    events: Channel<Event> = Channel.buffered(2)

    p1 = Task.spawn(fn -> Sender.send(events.sender, Event.UserMsg("hello")))
    p2 = Task.spawn(fn -> Sender.send(events.sender, Event.Tick(1)))

    case Receiver.receive(events.receiver) {
        Some(.UserMsg(msg)) -> handle_msg(msg)
        Some(.Tick(n)) -> handle_tick(n)
        Some(.Shutdown) -> io.print("shutting down")
        None -> io.print("channel closed")
    }

    _ = Task.await(p1)
    _ = Task.await(p2)
    Ok(Unit)
}
```

The enum is the multiplexing mechanism — producers tag their messages, the consumer pattern matches on variants. Multi-channel `select` as a language construct is not part of layer 1; the tagged-enum-channel pattern covers the realistic cases.

### Follow-on candidates

The following are intentionally not core language constructs. Each can land as a stdlib helper or runtime primitive once a real use case justifies the surface.

- **Non-blocking channel ops (`try_send` / `try_receive`)**.
- **Multi-channel `select` as a language construct** — see Multiplexing above for the layer-1 path.

### Supervision

Long-lived background work is owned by named `Supervisor` values from `std/supervisors`. A supervisor is a set of tasks sharing one policy: a required `max_running` bound, a shutdown budget, and a restart policy. Supervisors are created during `boot` and are usually stored on the active app value.

```nomi
import {
    std/duration.Duration
    std/supervisors.Supervisor
}

struct Config {
    context: Context
    audit: Supervisor
    notify: Supervisor
}

fn boot() -> Config {
    #Config{
        context: Context.root(),
        audit: Supervisor.new(max_running: 4, shutdown_timeout: Duration.seconds(30)),
        notify: Supervisor.new(max_running: 50),
    }
}
```

`max_running:` is required because there is no default concurrency bound that is safe for an unknown downstream. `shutdown_timeout:` defaults to a runtime budget suitable for ordinary background work; set it explicitly when a supervisor owns unusually slow or disposable work.

Creating a supervisor is boot-only. A constructor called from `boot` may create one, so server/domain types can own their own supervisors, but ordinary request code cannot allocate new program-wide task groups.

#### Spawning Under A Supervisor

`Supervisor.spawn(supervisor, body)` starts a task under that supervisor and returns immediately with a `Task<Unit>`. Supervisor-owned task bodies must return `Unit`: nobody is required to await the handle, so a returned value would have no reliable owner.

```nomi
fn handle_order(id: Int) -> Response {
    response = build_response(id)
    _ = Supervisor.spawn(App.audit, fn -> record_audit(id, response))
    response
}
```

The returned handle is optional extra control. You can ignore it with `_ =`, cancel it with `Task.cancel(handle)`, inspect it with `Task.outcome(handle)`, or wait for it with `Task.await(handle)`. Dropping the handle leaks nothing because the supervisor drains its work during shutdown.

For batches, `Supervisor.spawn_all(supervisor, source, f)` mirrors `Task.spawn_all` but uses the supervisor's own `max_running` bound:

```nomi
_ = Supervisor.spawn_all(App.notify, users, send_welcome)
```

#### Flushing And Shutdown

`Supervisor.flush(supervisor)` waits for all currently outstanding work under that supervisor. The program also drains supervisors automatically when `main` returns or the process receives `SIGINT`/`SIGTERM`.

```nomi
fn finish_batch() -> Unit {
    _ = Supervisor.spawn(App.audit, fn -> record_audit(1, "accepted"))
    _ = Supervisor.flush(App.audit)
}
```

Supervisors drain concurrently; shutdown takes as long as the slowest supervisor that still has work, bounded by that supervisor's own timeout. When a budget expires, remaining work is cancelled and abandoned. A second interrupt exits immediately.

#### Failures And Restarts

Task failures that are not returned values are observable through `Task.outcome`. `std/tasks` distinguishes:

```nomi
enum Outcome<T> {
    Completed T
    Cancelled
    Failed Failure
}
```

A body returning `Err(e)` is still `Completed(Err(e))`; it produced a value. `Failed` is for a trap, breakage the program did not plan for (§17 *Traps, `Maybe` and `Result`*).

Supervisors can restart failed work according to their restart policy. Cancellation is not a failure, so cancelling a task does not fight shutdown or restart loops.

#### Concurrency Scenarios

Every concurrent task in Nomi is either owned by a `concurrent` block or by a supervisor. No orphaned work.

**Parallel fetch** — fire N requests, collect results. `concurrent` + `Task.spawn`/`Task.await`:

```nomi
concurrent {
    user = Task.spawn(fn -> fetch_user(id))
    order = Task.spawn(fn -> fetch_order(id))
    u = try Task.await(user)
    o = try Task.await(order)
    Ok(build_response(u, o))
}
```

**Producer/consumer pipeline** — multiple producers feed a channel, one consumer processes:

```nomi
concurrent {
    ch = Channel.buffered<Page>(10)

    producers =
        Iter.map(urls, fn url -> {
            Task.spawn(fn -> {
                url
                |> try fetch()
                |> try parse()
                |> then &Sender.send(ch.sender, &1)
                |> Result.map_err(fn _ -> "channel closed")
            })
        })
        |> Iter.to_list()

    consumer = Task.spawn(fn -> {
        Iter.loop(fn acc = Ok(0) -> {
            case Receiver.receive(ch.receiver) {
                None ->
                    break acc

                Some(page) -> {
                    count = try acc
                    try database.save(db, page)
                    Ok(count + 1)
                }
            }
        })
    })

    _ = Task.await_all(producers)
    Sender.close(ch.sender)
    Task.await(consumer)
}
```

**Long-running/background work** — enqueue audit, email, or periodic work under a named supervisor:

```nomi
fn handle_signup(user: User) -> Response {
    _ = Supervisor.spawn(App.notify, fn -> send_welcome(user))
    #Response{status: 202}
}
```

**Periodic task** — poll an API, clean up stale data, or drain an outbox:

```nomi
fn outbox_worker() -> Unit {
    Iter.loop(fn -> {
        drain_pending(App.outbox)
        timer.sleep(Duration.seconds(1))
    })
}
```

**Request-scoped concurrency inside background work** — a supervisor owns the background task; a nested `concurrent` block owns per-request parallelism inside that task.

## 21. Imports

Import syntax and its rules are in §14 *Files and Imports*, including that an
import of a file that does not exist is a compile-time error (§14 *File
Rules*).

## 22. Operators

The operator token set is fixed: users cannot define new operators or change
precedence. The arithmetic operators `+`, `-`, `*`, and `/` are extensible
through standard interfaces. `%` stays closed to the numeric scalar types.

**Arithmetic:**
`+` `-` `*` `/` `%`

**Arithmetic interfaces.** `+`, `-`, `*`, and `/` are backed by
`Add<Rhs, Out>`, `Subtract<Rhs, Out>`, `Multiply<Rhs, Out>`, and
`Divide<Rhs, Out>`. The left-hand operand is the implementing type (`self`);
the interface type arguments describe the accepted right-hand operand and the
result type:

```nomi
interface Add<Rhs, Out> {
    fn add(lhs: self, rhs: Rhs) -> Out
}

type Day Int
type Days Int

impl Add<Days, Day> for Day {
    fn add(day: Day, rhs: Days) -> Day {
        Day(d) = day
        Days(n) = rhs
        Day(d + n)
    }
}
```

That means `Duration + Duration` can return `Duration`, while
`Date + Days` can return `Date`. One receiver may have several impls of an
operator, told apart by the right-hand type: `Instant - Duration` is an
`Instant` and `Instant - Instant` a `Duration`. The same shape applies to the other
operator interfaces: `Duration - Duration`, `Score * Scale`, or `Ratio / Scale`
can each choose the right-hand type and the output type independently. The
stdlib supplies arithmetic impls for `Int`, `Float`, and `Decimal`; `Add` also
covers `String` and `Bytes` concatenation. Generic code that uses an
extensible operator must say the relationship explicitly, e.g.
`where T: Add<Step, Out>` or `where T: Divide<Divisor, Out>`.

On a collection, `+` and `-` combine two collections of the same type:
`List<T> + List<T>` and `Vector<T> + Vector<T>` concatenate, `Set<T> + Set<T>`
is the union and `Set<T> - Set<T>` the difference, and `Map<K, V> + Map<K, V>`
is `Map.merge`, the right-hand map winning on a shared key. Map has no `-`.
There is no collection-plus-element form; a single element is `s + %[x]`,
`list + [x]` or `m + %{k => v}`.

An operator impl may be generic over the receiver's type parameters, and its
interface arguments may name them. The arguments are read at the left
operand's type, so `Set<Int>`'s `Add<Set<T>, Set<T>>` takes a `Set<Int>` and
returns a `Set<Int>`:

```nomi
impl Add<Set<T>, Set<T>> for Set<T> {
    fn add(a: Set<T>, rhs: Set<T>) -> Set<T> {
        Set.union(a, rhs)
    }
}

%[1, 2] + %[2, 3] // %[1, 2, 3]

%[1] + 2 // error: binary + right operand mismatch: Add expects Set<Int>, got Int
```

The same holds for a user type (`impl Add<T, Box<T>> for Box<T>`) and inside
generic code, where `a + b` with `a: Set<T>` and `b: Set<T>` is `Set<T>`.

**Summable.** A type whose `+` takes two of its values and returns one can
also implement the prelude interface `Summable`, which names the sum of no
values: the one value `+` leaves alone, so `T.add(x, T.empty_sum()) == x`.

```nomi
interface Summable {
    fn empty_sum() -> self
}
```

The stdlib implements it for `Int` (`0`), `Float` (`0.0`), `Decimal` (`0d`, at
scale 0, so `x + 0d` keeps `x`'s scale), `Duration` (zero length), `String`
(`""`), `List`, `Vector`, `Set`, `Map` and `Bytes` (each empty). It is not a
default or "unset" value: a type with no `+` over its own values (`Bool`,
`Maybe`, an enum, `Date`, `Instant`) does not implement it. Interfaces do not
extend one another (§13), so code that sums names both bounds: `where T:
Add<T, T> and Summable`. `Iter.sum` (§*`std/iter`*) is that code.

**Integer overflow traps.** `Int` is 64-bit. The arithmetic operators `+`, `-`, `*` (and `/` in the single `MinInt64 / -1` case) raise a runtime error on signed overflow rather than wrapping silently — the same fail-loud stance as division by zero. When modular (wrapping) arithmetic is the intent — hashing, checksums, ring counters — use the explicit `Int.wrapping_add` / `Int.wrapping_sub` / `Int.wrapping_mul` functions, which wrap two's-complement. `derive Hashable` uses wrapping multiply internally for exactly this reason. Unary `-` traps on `Int.min_value`, whose magnitude is one past `Int.max_value`, and so does `Int.abs(Int.min_value)`, with the same error. An overflow is a trap, by the rule in §17 *Traps, `Maybe` and `Result`*: the program asked for a number that does not fit.

**No implicit numeric conversion.** `Int`, `Float`, and `Decimal` are three non-mixing islands — none mixes implicitly with either of the others. Mixed-operand arithmetic (`1 + 2.0`, `1 + 1.50d`, `2.0 + 1.50d`), comparison (`1 < 2.0`, `1 < 2.50d`), and equality (`1 == 1.0`, `1.0 == 1d`) are all compile errors; convert explicitly across the boundary with `Int.to_float` / `Float.to_int` / `Decimal.from_int` / `Decimal.to_float` / `Decimal.from_float`. This avoids the silent precision loss that implicit `Int`→`Float` widening causes for magnitudes above 2^53. More generally, `==` / `!=` require operands of the same type — comparing unrelated types (e.g. `String` to `Int`) is a compile error, not a silent `false`.

**Integer division and modulo.** `/` truncates toward zero and `%` takes the sign of the dividend, so the identity `(a / b) * b + (a % b) == a` holds (`-7 / 3 == -2`, `-7 % 3 == -1`). `%` is the remainder of that division. `Int.modulo(a, b)` is floored modulo: its result is zero or takes the sign of the divisor, which wraps a value into a range (`Int.modulo(-7, 3) == 2`, `Int.modulo(-1, 1440) == 1439`, `Int.modulo(7, -3) == -2`). The two agree when the operands have the same sign, and both answer `0` for `Int.min_value` and `-1`. `/`, `%` and `Int.modulo` raise a runtime error on a zero divisor. A zero divisor is a promise the caller broke, so it traps (§17 *Traps, `Maybe` and `Result`*). When the divisor is proven non-zero, `Int.divide` takes a `NonZeroInt` and is total; `Int.to_non_zero(d)` is the check that makes one. `Int.divide` is a `Divide<NonZeroInt, Int>` impl; the ordinary `/` operator uses `Divide<Int, Int>`. Modulo is not defined on `Float`.

**Where a trap is reported.** A runtime fault (overflow, a zero divisor, a `case` with no arm, a call past the depth limit, a failed host function) prints `line N:` and then the fault's own text. `N` is a line of the program's own code: the line of the operation that faulted, or, when the fault happens inside the standard library, the line of the program's call into the standard library that led to it, however many std calls deep the fault is. The text after the line is the fault's own and does not name the std function; the call on line `N` does. So both of these programs print `line 2: integer overflow: 0 - -9223372036854775808`:

```
fn main() {
    _ = -Int.min_value
}
```

```
fn main() {
    _ = Int.abs(Int.min_value)
}
```

A lambda of the program that faults while a std function runs it (`Iter.map(xs, fn x -> 10 / x)`) is the program's code, so the fault names the lambda's line. A fault whose text names no line (a host function's own fault, such as `String.repeat` with a negative count) is given the line of the program's call. A `todo` names its own file and line instead (§*Unwritten Code*). The same line is printed by `nomi run`, `nomi test`, a built executable, the REPL and `compiler.run`'s `Err`; the debugger stops where the fault happened, in std's frame when it is std's, and its exception text is the one the program prints.

**Decimal arithmetic.** `Decimal` is the third blessed operator type (§*Primitive Types*): `+ - * /` and `< > <= >= == !=` work on two `Decimal` operands and yield a `Decimal` / `Bool`. `+ - *` are always **exact** — they never round; the scale grows as needed (`1.50d + 1.5d == 3.00d`, `1.50d * 1.5d == 2.250d`). The `/` operator is **exact-or-trap**: a quotient that terminates in base 10 just works at its preferred scale (`10.00d / 4d == 2.50d`), but a non-terminating quotient (`10d / 3d`) raises a runtime error pointing at `Decimal.divide(a, b, scale, mode)` — the explicit-rounding division path — rather than silently rounding. Division by zero is a runtime error (no Infinity). Both are traps by the rule in §17 *Traps, `Maybe` and `Result`*, and `Decimal.divide` is the total route for a quotient that may not terminate. There is **no `%`** on `Decimal`. Equality and comparison are value-based and scale-insensitive (`1.50d == 1.5d` is `True`); scale is preserved only for display (`Decimal.scale(1.50d) == 2`, `Decimal.to_string(1.50d) == "1.50"`). Decimal literals carry the `1.50d` / `1.50D` suffix.

**Comparison:**
`==` `!=` `<` `>` `<=` `>=`

`==` / `!=` work on any two operands of the same type — structural equality is the universal fallback. The ordering operators `<` `>` `<=` `>=` require the operand type to have a declared `Comparable` impl (`derive Comparable` or an `impl Comparable for T` block); using them on a type without one is a compile-time missing-impl error. See §30 for the rationale behind the asymmetry.

**Logical:**
`and` `or` (keywords) `!` (negation)

`and` and `or` short-circuit: the right operand runs only when the left does not decide the answer. `a and b` is `if a { b } else { False }`, and `a or b` is `if a { True } else { b }`. So the right operand may leave instead of answering: a block ending in `return`, `break` or `continue`, an `if` or `case` every arm of which leaves, a failing `try` or a trap. When it leaves, the expression has no value on that path, and its value is the left operand's on the other. `ok = ready? or { return Err("not ready") }` returns early when `ready?` is `False` and binds `ok` to `True` otherwise; `_ = n < limit or { break }` ends an `Iter` callback's iteration.

`&&` and `||` are not operators. `a && b` is a parse error at the `&&` that says to write `a and b`, and `a || b` is a parse error at the `||` that says to write `a or b`.

**Other:**

- `|>` — pipe
- `try` — unwrap Result/Maybe (prefix keyword)
- `=` — binding
- `.` — member access
- `+` — `Add` addition/concatenation

## 23. Closures

Lambdas capture values from enclosing scope naturally. Full immutability means no concerns about captured values changing.

```nomi
fn make_greeter(greeting: String) -> (String) -> String {
    fn name -> "${greeting}, ${name}!"
}

hello = make_greeter("Hello")

hello("Alice") // "Hello, Alice!"
```

Closures are flat: a lambda, a nested `fn` or a task's body copies the value each name it reads holds when the closure is created, and reads those copies from then on. A later binding of the same name, including a same-scope shadow (§2), is a new binding that does not reach the closure:

```nomi
x = 1

f = fn -> x

x = 2

f() // 1
```

A nested `fn` that calls itself refers to itself through its own closure, not through the enclosing scope, and every other name it reads is captured by value like any closure's. A nested `fn` can call only `fn`s declared before it, so nested `fn`s are never mutually recursive.

Types on anonymous functions are inferred from context. If the compiler can't infer, annotations are required:

```nomi
// inferred — compiler knows Iter.map expects (Int) -> Int
[1, 2, 3] |> Iter.map(fn n -> n * 2)

// explicit — when context isn't enough
f = fn x: Int -> x * 2
```

## 24. Standard Library

### Host And Foreign Declarations

Stdlib types and functions are declared in `.nomi` files using the `host` keyword. This tells the compiler the signature exists but the implementation is provided by the runtime (Go host):

```nomi
/// 64-bit integer.
host type Int

/// Returns the absolute value of n.
host fn abs(n: Int) -> Int
```

- `host type Name` — declares a host-provided type with no user-visible fields or body. Host operations live as file/module `host fn` declarations or impl-block items. Derives are sibling `derive Iface for Name` declarations. Manual interface implementations live in sibling `impl Iface for Name { ... }` blocks.
- `host fn name(params) -> ReturnType` — declares a function signature with no body (implementation is host-provided). Also legal as an impl-block item, where the host key is derived as `module.Type.function`.
- Host declarations can have doc comments (`///`) like any other declaration.
- User-facing Go interop binds ordinary Go packages through `gopkg` declarations. A declaration such as `gopkg "example.com/app/ffi" as ffi` introduces a compile-time package handle whose import path is resolved by Go's normal module machinery. The path must be a valid Go import path. A Go standard library package (`gopkg "strings"`) needs no `go.mod` and no Go package of the project's own: its source is the Go toolchain's, and a script binding only the standard library runs from a directory with no `go.mod` at all. A package internal to the standard library (`internal/abi`) is a check error, since no other module may import it. Any other path must be provided by the nearest `go.mod` above the declaring file: a package of the project's own module, or a package of a module that `go.mod` requires or replaces. Any other is a check error at the path, such as `gopkg "callback:= f(s": not a valid Go import path: invalid char ':'`, or one naming the project's module for a path outside all of those. A Go type binding must be opaque. It may remain private as a raw implementation handle (`opaque type RawConn go ffi.Conn`) behind a Nomi facade, or it may be intentionally public as a public opaque foreign type (`pub opaque type Regex go ffi.Regex`) when the module deliberately commits to that Go-backed representation. Public opaque foreign types can be named and passed across the module boundary but cannot be constructed, unwrapped, or inspected structurally. A Go function binding maps an exported Go function into a local Nomi function name: `fn open_raw(path: String) -> Result<RawConn, String> go ffi.OpenRaw`. The alias in `go alias.Symbol` must be one a `gopkg` declaration of the same file introduces; any other is a check error at the alias. A `gopkg` is a top-level declaration, as the bindings that use it are; one inside a body is the error `` a `gopkg` declaration belongs at the top level of a file, beside the `go` bindings that use it; it cannot appear inside a body ``.
- A program with `gopkg` bindings needs a Go toolchain: `nomi run`, `nomi check` and `nomi test` build a cached Go wrapper that links the bound packages (see [`implementation-notes.md`](implementation-notes.md#go-ffi-wrapper)). With no `go` on `PATH`, `nomi` downloads the Go release it was built with and uses that (§31, *Dependencies*). A bound package may be in a module from the Go module cache, a dependency's included; its adapters are generated from its source there. The wrapper builds with `CGO_ENABLED=0`, so a `gopkg` naming a package that needs cgo, itself or through a package it imports, is a check error at the path: `` gopkg "example.com/app/wrap": needs cgo: it imports example.com/cdriver/internal/csql (...), whose files import "C", and Go bindings build with CGO_ENABLED=0 ``. Pure-Nomi programs and tests need no toolchain.
- Every std declaration is a bare `host fn` / `host type`, so importing any std module, Go-backed ones such as `std/regex` included, needs no Go toolchain — see "The rule: `host` for the standard library, `go` for user FFI" below. A user's own Go binding is declared in Nomi source with `gopkg` and `go alias.Symbol`; there is no Go-side registration function. A Go program that embeds Nomi answers the `host fn`s its program declares from a host table passed to `vmhost.Load` (`vmhost.WithHosts`). A plain `host type` in such a program is an opaque handle: the table's functions create and read its values, which Nomi code passes around and stores and reads only through those functions, and the type itself needs no registration.
- Go binding signatures project Nomi `String` → Go `string`, `Bool` → `bool`, `Int` → `int64` (Go `int` and the sized signed widths project to `Int`, as do the unsigned widths other than `uint8` when the value fits `int64`; every narrowing is range-checked in both directions rather than truncated), `Byte` → `uint8` (`Byte` is its own Nomi type, so an `Int` does not fill a `uint8` parameter and a `Byte` does not fill an `int64` one), `Float` → `float64` (a `float32` slot is range-checked; `NaN` and `±Inf` cross unchanged, and `NaN` stays reflexive under Nomi's equality so it remains usable as a map key), `Bytes` → `[]byte`, `Duration` → `time.Duration`, `Instant` → `time.Time` (an `Instant` is nanoseconds since the Unix epoch, so a `time.Time` outside that window is rejected rather than wrapped, and an `Instant` returns to Go as UTC — the projection carries no zone and no monotonic reading, so two round-tripped times are `Equal`, not `==`), `List<T>` → `[]T`, `Map<K, V>` → `map[K]V` (the projected key must be a valid Go map key; a Nomi `Map` iterates in a defined order and a Go map has none, so a Go map projects with its keys in ascending order rather than inheriting Go's randomized walk (strings and numbers in their natural order, `false` before `true`, a struct by its exported fields in declaration order; a Go key type with no such order, such as a pointer or an interface, is refused at the binding), and two keys that would collapse into one on either side fail the conversion rather than silently drop an entry), `Maybe<T>` → `*T`, a declared Nomi struct → a Go struct with the matching exported fields (snake_case by default, overridable with a `nomi:"..."` tag), where the Nomi type identity always comes from the declaration at that position — the `host fn`'s declared return type, or the declared parameter type of the Nomi function being called — never from the Go type, because `Point` and `Vec` are one Go shape and a struct with no identity misses every impl keyed on it (so the two names need not agree: a Go `func(Row) bool` binds a Nomi `(RawRow) -> Bool`, and only the fields are compared, at every depth), `Dynamic` → a Go empty interface (`any`, `interface{}`, or a named type over one), which holds the value the `Dynamic` wraps (a `Dynamic` from `Json.to_dynamic` holds the shapes `encoding/json` decodes into: `map[string]any`, `[]any`, `string`, `float64` or `int64`, `bool`, `nil`), or `rt.Dynamic` from `github.com/nomi-language/nomi/rt`, which is the `Dynamic` itself, and nothing else (a Go `int64` slot does not take a `Dynamic`; declare the type the Go value has), bound opaque Go types → their registered Go pointer type, and direct callback parameters `(A, ...) -> R` → `func(A, ...) R`. A user-declared `enum` has no Go projection: the only enums that cross are `Result`, `Maybe`, and `Bool`, as a trailing Go `error`, `*T`, and `bool`, and they are matched on the stdlib type's own runtime identity — a same-named type from another module is a different type and is rejected rather than projected as the stdlib one. The return position is driven by the Go signature: counting the non-`error` results as `N`, `N == 0` projects to `Unit`, `N == 1` to that value, and `N >= 2` to the tuple `#(A, B, ...)`; an optional trailing `error` wraps whatever that produced in `Result<_, String>`, so `error` alone is `Result<Unit, String>`, `#(T, error)` is `Result<T, String>`, and `#(A, B, error)` is `Result<#(A, B), String>`. `N` is uncapped, so a Go function that naturally returns several values needs no carrier struct. Tuples project one way only: a tuple in *parameter* position is rejected because Go has no tuple type to bind it to, and a plain Go struct against a Nomi struct of the same shape already carries several values in both directions. Type/function bindings are preflighted against the Go source of the project's own packages, of packages reached through a local `replace`, and of bound standard library packages (read from the toolchain's GOROOT with this platform's build constraints) for missing bound names, generic Go functions (a binding needs one concrete signature, so bind a non-generic Go function that calls the generic one) and signature mismatches. Unsupported projected shapes fail before program load with a diagnostic that points back to the binding declaration.
- A distinct type, opaque or not, crosses as its inner type does, at every depth: `type Id Int` crosses as `int64` (or any Go integer width an `Int` takes), `type Ids List<Int>` as `[]int64`, a distinct over a struct as a Go struct with that struct's fields, and `type Outer Inner` over `type Inner Int` as `int64`, in a parameter, a result, a `List`, `Maybe`, `Map` or `Result`, a tuple, a struct field and a callback's own parameters and result. The value Go returns is wrapped in the declared distinct again. That is what gives `Duration` and `Instant` (both `opaque type ... Int` in the stdlib) a second legal Go spelling alongside `time.Duration` and `time.Time`: an `int64` slot carries either one. A marker (`type Expired`, no inner type) holds no value and has no Go projection.
- The projection is checked in the FORWARD direction only. For each declared Nomi type the boundary computes the Go type it projects to and compares that against the Go type actually bound, read from the bound Go declaration's signature: a scalar by its row of the table above, `Dynamic` against a Go empty interface or `rt.Dynamic`, a `List`, `Maybe`, `Map`, `Result`, tuple or callback by its structure, an opaque binding by the Go type it binds, and a struct of the binding's file field by field. A distinct type declared in the binding's file, and std's `Duration` and `Instant`, is compared through its inner type. The check does not read the declarations of another file: a name declared there fills a Go struct slot on trust, its fields held to the Go struct's by the adapter generator, and fills no other slot, so a binding that passes a distinct of another file declares it in that file. The inverse direction is deliberately NOT checked: a Go type does not determine the Nomi type it must be (`int64` is `Int` and equally every distinct over `Int`, a Go struct is any Nomi struct with matching fields), so an inverse check would reject declarations that work. Disagreements the forward rule proves (a declared `String` against a Go `int64`, a `Dynamic` against a Go `int64`, a `type Name String` against a Go `int64`, in a parameter, a return, a tuple element, a `Result` payload, a struct field, or a callback's own argument or return) are rejected at load, naming both sides and what the declaration projects to.

**The rule: `host` for the standard library, `go` for user FFI.** Every
declaration in `std/` whose implementation is Go writes `host fn` or
`host type`, whatever Go package that implementation lives in. `fn f(x: T) -> U
go pkg.Sym` is the spelling a USER writes to bind their own Go code, alongside
`gopkg "import/path"`.

`host` means the implementation is outside Nomi and the standard library owns it; where the Go code sits, and why std does not use the `go` spelling, is in [`implementation-notes.md`](implementation-notes.md#why-std-uses-host-rather-than-go).

The stdlib `.nomi` files are embedded in the compiler binary and analyzed once per process. Each is an ordinary file reached with a `std/` import path: `import std/io` binds the `io` file API object, `import std/calendar.{self, Date}` binds the file and a type, and `import std/io.print` binds one function bare. Owner functions on a prelude type (`String.length`, `Map.get`) need no import.

### Prelude

One stdlib file, `std/prelude.nomi`, has a special role: it lists the imports every user file gets implicitly. It is itself a regular Nomi file containing only a `pub import` block — no special syntax. After every other stdlib module loads, the analyzer and runtime each evaluate `prelude.nomi`; the resulting module scope becomes the parent scope of every user file (analysis side) and the bindings are written into the root environment (runtime side). The set is intentionally minimal: primitives (`Int`/`Float`/`Decimal`/`String`/`Unit`/`Infallible`/`Byte`/`Bytes`/`Codepoint`/`List`/`Vector`/`Map`/`Set`), `Bool`/`True`/`False` from `std/bool`, `Maybe`/`Some`/`None`, `Result`/`Ok`/`Err`, the `Range` struct (so range literals work everywhere), the core interfaces (`Add`/`Subtract`/`Multiply`/`Divide`/`Comparable`/`Display`/`Equatable`/`Hashable`/`Discrete`/`Iter`/`Steppable`/`Struct`/`Distinct`/`Summable`/`Contains`/`Variants`, plus universal `Debug`), the `Ordering` and `Type` types, and the application lifecycle types `Startup` and `Context`. File API objects are deliberately left out: importing `String` into the prelude does not import `strings`, importing `Map` does not import `maps`, and importing `Iter` does not import `iter`. Type-qualified calls such as `String.to_upper`, `Map.get`, `Iter.map`, and `Context.root` use the prelude types directly. File-qualified calls such as `io.print` require the corresponding file import (`import std/io`). `Ordering`'s `Less`/`Equal`/`Greater` variants are deliberately left out: `derive Comparable`'s synthesized body references them *qualified* (`Ordering.Less`), so nothing depends on the bare names being in scope, and — like the `std/literals` cluster (`Literal`/`Fragment` and the `Fragment` variants) and assertion-authoring types such as `Assertable`/`AssertionFailure` — they're named only by a narrower authoring audience that imports them explicitly. The prelude exports no bare functions. Anything else must be imported explicitly.

Stdlib files themselves do **not** receive the prelude — they each import their dependencies by hand. The "is this a stdlib file?" check is the only place that distinguishes who gets the implicit imports. A stdlib file's `//!` prompts are part of the file and see the file's scope, as its bodies do: a prompt that writes a bare `False` or `Some` needs the file to import it (`import bool.Bool.False`). `nomi test`, `nomi check`, the language server and the reference editors all check a prompt in that scope.

Batteries-included philosophy (like Go): minimize the need for third-party dependencies. The stdlib today, one file per module, each imported as `std/<name>`:

| Module | What it holds |
|---|---|
| `int`, `float`, `decimal`, `bool`, `unit`, `code_points` | Primitives: `Int` (bitwise ops, wrapping arithmetic, `NonZeroInt`/`PositiveInt`), `Float` (special values, rounding), `Decimal` and `RoundingMode`, `Bool`, `Unit`, `Codepoint` |
| `strings`, `bytes`, `matcher` | `String` (grapheme-based text, `NormalForm`), `Byte`, `Bytes`, and `Matcher`, what String's search functions accept |
| `lists`, `vectors`, `maps`, `sets`, `ranges` | `List`, `Vector`, `Map`, `Set`, `Range` and their container-specific owner functions |
| `iter` | The `Iter` interface and every generic iteration owner function, plus `Iter.loop` |
| `maybe`, `results` | `Maybe`, `Result`, `Infallible` |
| `add`, `subtract`, `multiply`, `divide`, `comparable`, `equatable`, `hashable`, `discrete`, `steppable`, `variants`, `display`, `debug`, `structs`, `type` | The core interfaces, `Ordering`, `Direction`, and `Type<T>` |
| `io` | `print`, `write` (no trailing newline), `inspect`, `read_line`, `read_file`, `write_file`, `Error`, and `capture`, which runs a function against given input and collects what it prints (§36) |
| `tasks`, `channels`, `supervisors`, `timer`, `context`, `startup` | Structured concurrency (§20), `timer.sleep`, `Context` (§28), `Startup` (§27) |
| `duration`, `instant`, `calendar` | `Duration`, `Instant` (`Instant.now`), and the civil date-time types (§39) |
| `random` | Pure, seeded random generation (§40) |
| `locale`, `plural`, `localize` | `Locale` with a ``Locale`pt-BR` `` typed literal, CLDR fallback chains and negotiation; `PluralCategory` and CLDR's cardinal and ordinal plural rules; `Localize` and `NumberParseError`, for the locale-aware `format`, `format_percent` and `parse_in` functions of `Int`, `Float` and `Decimal` and `String.join_list` (§41) |
| `collation` | `Collator`, `Strength` and `CaseFirst`: ordering strings as a locale orders them (the Unicode Collation Algorithm with CLDR's tailorings) and their sort keys; `String.to_upper_in`, `to_lower_in` and `fold_case` are in `strings` (§41) |
| `json`, `toml`, `dynamic` | `Json` with `ToJson`/`FromJson` and their derives, the `Toml` typed literal, `Dynamic` for untyped host data |
| `regex` | `Regex`, with a `Regex"..."` typed literal. A `Regex` is a `Matcher`, so `String.contains?`, `String.find`, `String.find_all`, `String.split` and the other search functions take one; `String.captures` and `String.captures_all` take only a `Regex` and answer its capture groups; `Regex.replace_all` (`$1` expansion) is its own |
| `literals` | `Literal` and `Fragment`, for typed-literal authors (§16) |
| `assertions`, `testing` | `AssertionFailure`, `Assertable`, `testing.check`, test `Clock` (§36) |
| `compiler` | The compiler as a library: `check`, `run`, `hover` |

Not in the stdlib: math functions beyond the numeric types' own (no trigonometry or logarithms), cryptography and networking (no HTTP client or server; bind a Go package with `gopkg`, as Host And Foreign Declarations above describes).

## 25. Compilation

Implementation language: **Go**

### Execution engines

One engine runs a Nomi program: the VM, for `nomi run`, `nomi test`, the REPL and the tour playground. The compiler parses, analyzes and type-checks the program and lowers each function it can to IR; the VM compiles that IR to bytecode and runs it over the runtime library (`rt`). A program that reaches a function the compiler could not lower fails before its first effect: `nomi run` prints an error at the code the compiler stopped at, with its source line and any hint, such as ``this call to `skip_odd` is not supported yet, so `fn evens` cannot run``, and exits 1. The last hint of every such error says the gap is in Nomi, not the program, and links the issue tracker. `nomi check` lowers the program the same way without running it and reports the same errors for everything `main` or a test reaches. `nomi test` runs every case it can, reports each case it cannot as `BLOCKED <file> :: <case> <path>:<line>:<col>: <message>`, with any hint indented on the line below, and appends `, K blocked` to its summary when K is not zero; it exits nonzero when anything failed or was blocked. A run stopped by something other than code the compiler could not lower (a Go crossing nothing binds, or a VM limit) prints `BLOCKED <file> [<function>] <reason>` lines instead. Setting `NOMI_DEBUG_LOWERING=1` adds the compiler's own reason to each of these errors as a hint. The REPL compiles each input as a new program that sees the declarations and top-level bindings of the inputs before it, and runs it on one live VM. An earlier input never runs again: a later input reads the value an earlier binding computed, and a closure keeps the value it captured. Redefining a function or type affects later inputs only; an input that fails to check, faults or is blocked prints its error and changes nothing.

### Standalone executables

`nomi build <file> [-o <out>] [--target <goos>/<goarch>]` writes one executable: the `nomi-vm` binary with the program's lowered IR appended to it, the way `deno compile` and `bun build --compile` work. At startup `nomi-vm` reads the IR from its own file and runs `main` exactly as `nomi run` does: the same arguments, standard input, signal handling, output and exit status. The binary skips the front end and the lowering, so a hello-world program starts in a few milliseconds, and then runs on the same VM at `nomi run` speed. It is not native code.

A program `nomi run` would refuse is refused at build time with the same text, and nothing is written: a front-end error, or an error at each piece of code the compiler cannot lower. A file without `fn main` has nothing to build, unless it has an `export fn` and the target is `wasip1/wasm` (below). A program with a `todo` or a `dbg` in any of its files is refused with one list of every `todo` (§5, *Unwritten Code*) and every `dbg` (§6, *Debugging pipes*), whether or not a run would reach it. `--target`, or `GOOS`/`GOARCH` in the environment, picks the platform. `nomi-vm` comes from, in order:

1. `nomi-vm` beside `nomi` (or beside the file a symlinked `nomi` resolves to), built for the target. Every release archive ships one.
2. A source checkout of the compiler: `nomi-vm` is built with `go build` and cached. This is how `go install` and a development tree work, with no release involved.
3. A `nomi-vm` downloaded earlier for this release and target.
4. Download: the target's archive from the release this `nomi` was cut as, verified against the release's checksum file, with only `nomi-vm` extracted and cached. `nomi build` prints the URL it downloads, and with no network the error names it.

A prebuilt `nomi-vm` must come from the same commit as `nomi`; both carry it in their build information, and `nomi build` refuses a `nomi-vm` from another commit. So a release builds executables for every platform with no Go toolchain, and a built binary needs nothing at all. Two kinds of program still need Go, which builds their own `nomi-vm` from the compiler module at the release's version: a project with Go FFI bindings, whose `nomi-vm` links them, and a program importing `std/compiler`, whose `nomi-vm` links the front end and is not shipped. That Go is the `go` on `PATH`, or, with none, the Go release `nomi` was built with, which `nomi` downloads (§31, *Dependencies*), so neither needs Go installed.

A WebAssembly target, `js/wasm` or `wasip1/wasm`, writes `<name>.wasm` by default. Nothing may follow a module's last section, so the program goes inside the module:

- A `wasip1/wasm` module carries it as one more active data segment, which the WebAssembly engine writes into a 4 MiB area of the `nomi-vm`'s own memory when it instantiates the module. The module reads its program from there, so it needs no file access: `wasmtime run hello.wasm` runs it, and so does a browser's WASI shim with no file system. The area is uninitialized memory in the `nomi-vm`, so it adds nothing to the file; it raises the module's declared initial memory by 4 MiB, to 9.8 MiB for the reactor. A program whose compiled form is larger than the area is refused by `nomi build`, naming the limit.
- A `js/wasm` module carries it in a custom section named `nomi.image`, which every runtime skips. A module cannot read its own sections, so it looks for the section where its host can supply it: it uses `globalThis.nomiImage` when a loader has set it to the section's bytes (`new Uint8Array(WebAssembly.Module.customSections(module, "nomi.image")[0])`), and otherwise reads the file argv[0] names through the host's `fs`. Go's own loader runs it under Node unchanged: `node "$(go env GOROOT)/lib/wasm/wasm_exec_node.js" hello.wasm`. That loader hands the module Node's whole environment as `startup.env`, and Go's js/wasm runtime refuses to start when the arguments and environment together pass 8 KB ("total length of command line and environment variables exceeds limit"); in a large environment, run it with only what the program reads, as `env -i PATH="$PATH" node ...`. `nomi.js` passes only the `env` its caller gives.

A `wasip1/wasm` program with at least one `export fn` is written as a WASI reactor instead of a command module (§5, *Exported Functions*): the program is appended to `nomi-vm-reactor`, the same VM linked as a reactor, found, built and downloaded as `nomi-vm` is and checked the same way. `nomi-wasi.js` and `nomi-wasi.d.ts`, the JavaScript host for it, are written beside the module.

The `js/wasm` build keeps the browser build's limits: `io.read_file` and `io.write_file` fail with an `io.Error`, even under Node, and a call nests at most 1,000 deep. A command-line program that reads files belongs on `wasip1/wasm`. Every release ships a runner-only archive for each WebAssembly target, so a release `nomi` downloads it as it would any other platform's; the `js/wasm` archive also carries the `wasm_exec.js` of the Go that built its `nomi-vm`, and the `wasip1/wasm` archive carries `nomi-vm-reactor` beside `nomi-vm`.

### Compiled programs and nomi-vm

`nomi build <file> --no-vm [-o <out>]` writes the program's lowered IR alone, as a compiled program (`<name>.nomic` in the working directory by default), for a `nomi-vm` that does not carry it, or for a reactor to load (§5, *Loading a program into a reactor*). It records the program's exported functions, so a program with an `export fn` and no `fn main` builds too; `nomi-vm` and `nomi run` refuse to run such a program, with `` this program has no `fn main`; it is called through its exported functions ``, and exit 1. It needs no Go toolchain and no `nomi-vm`, and the same file runs on every platform's `nomi-vm`. It refuses what `nomi build` refuses, with the same text, and also a project with Go bindings, whose bindings only a VM built with them can answer: `` nomi build --no-vm: this project has Go bindings, which only a VM built with them can answer, so it has no portable compiled program ``. `--no-vm` takes no `--target` and no `--web`. A test file has no compiled program.

A `.nomic` file is a header followed by the program's IR image:

| Offset | Size | Field |
|---|---|---|
| 0 | 8 | Magic: the bytes `NOMIC` and three zero bytes |
| 8 | 4 | The program format version, a little-endian u32 |
| 12 | 4 | Flags, a little-endian u32. Bit 0: the program imports `std/compiler`. Every other bit is zero. |
| 16 | 2 | `n`, the length of the next field, a little-endian u16 |
| 18 | `n` | The version of `nomi` that wrote it, in UTF-8, as `nomi --version` names it (the commit, or for a release, the tag and the commit) |
| 18 + `n` | 8 | `L`, the image's length, a little-endian u64 |
| 26 + `n` | `L` | The image, the same bytes `nomi build` appends to `nomi-vm` |

`nomi build-vm [--target <goos>/<goarch>] [--compiler] [-o <out>]` writes a copy of the `nomi-vm` that `nomi build` appends programs to, with no program: `nomi-vm` by default, with `.exe` or `.wasm` as the target needs. It comes from where `nomi build` gets one, so a release needs no Go toolchain for it. For `js/wasm` it also writes the matching `wasm_exec.js` beside it. For `wasip1/wasm` it writes the command module. `nomi build-vm --reactor [--compiler] [-o <out>]` writes `nomi-vm-reactor` with no program instead (`nomi-vm-reactor.wasm` by default), which takes its program through `nomi_load` (§5, *Loading a program into a reactor*), and writes `nomi-wasi.js` and `nomi-wasi.d.ts` beside it, the JavaScript host that runs it in a browser or Node, creating the folder they go in if it does not exist. The reactor exists only for `wasip1/wasm`, so that is its target, and `--target` with any other value is a usage error. `nomi build-vm` takes no program file, and neither form takes `nomi build`'s `--no-vm`, `--web` or `--static`: a program with an `export fn` built with `nomi build --target wasip1/wasm` is already a reactor with that program inside it. `--compiler` writes the `nomi-vm` or reactor that links `std/compiler`'s hosts, which a compiled program importing `std/compiler` needs, and which needs Go to build.

A `nomi-vm` with no program of its own takes one when it starts:

```
nomi-vm <program.nomic> [args...]
nomi-vm [-] [args...]          # the .nomic file framed on stdin
nomi-vm --version
nomi-vm --help
```

Given a path, it reads the `.nomic` file there and passes the words after the path to the program as `startup.args`. Given no path, or `-`, it reads one `.nomic` file from the start of standard input, using the header's length, and reads nothing past its last byte: the rest of standard input is the program's own input, and the words after `-` are its arguments. So `cat app.nomic input.txt | nomi-vm` runs `app` over `input.txt`. Stdin is the way to hand a program to a host with no filesystem, such as a browser's WASI shim. A `wasip1/wasm` `nomi-vm` takes a path through a preopened directory (`wasmtime run --dir=. nomi-vm.wasm app.nomic`), and a `js/wasm` one under Go's loader reads a path through Node's `fs` (`node wasm_exec_node.js nomi-vm.wasm app.nomic`). Either takes the program on stdin. A `nomi-vm` whose terminal is its standard input, or whose standard input is empty or not a `.nomic` file, prints its usage. A word starting with `-` other than these is a usage error. A `nomi-vm` that `nomi build` appended a program to runs that program and treats every argument as the program's, so `./app --version` passes `--version` to `app`.

`nomi-vm` refuses a compiled program whose format version differs from the one it reads, before it runs anything: `` app.nomic was written by nomi v0.4.0 in program format 17, and this nomi-vm (nomi v0.5.0) reads format 18 ``, followed by how to fix it. Programs written by different versions of `nomi` in the same format run. It also refuses a program with a flag it does not know, and a program importing `std/compiler` when it does not link that module's hosts. A damaged or truncated file is an error naming it. All of these, and every usage error, exit 1, as a failed program does.

`nomi run <file.nomic> [args...]` runs a compiled program in the `nomi` process, as `nomi-vm` would, with `std/compiler`'s hosts linked. `nomi run` recognizes a `.nomic` file by its first eight bytes, whatever its name.

### A program in the browser

`nomi build <file> --web [-o <dir>] [--static <dir>]` writes a folder that any static file host can serve, which runs the program's `main` in a browser. The default folder is `<name>-web` in the working directory. It holds three generated files and the author's static files:

| File | What it is |
|---|---|
| `app.wasm` | The `js/wasm` module `--target js/wasm` writes, with its memory capped at 1 GiB. |
| `nomi.js` | An ES module: the loader and its `run` function, with Go's JS support file `wasm_exec.js` from the Go toolchain that built the js/wasm `nomi-vm` inside it. Each run starts this same file as its module worker. |
| `index.html` | A page that runs `main` with an output pane and an input line. |
| `locales/<tag>.nomil` | For a program that lists its locales, one file per listed locale `[i18n].web_embedded` leaves out: its CLDR data and translations, which the program loads with `Locale.load` (§41, *Locales in the browser*). |

`nomi.js` keeps `wasm_exec.js`'s text unchanged as the body of a function, which runs only where a program runs: in the worker, or on the page for a `worker: false` run. A page whose programs run in workers gets none of the globals `wasm_exec.js` sets (`Go`, and `fs`, `process` and `path` when missing). Since the worker is a file the host serves and not a `blob:` URL, a policy of `worker-src 'self'` allows it.

The static files are the files of a `web/` folder: the one at the root of the program's project, beside its `nomi.toml`, or, for a file outside a project, the one beside the file. `--static <dir>` names another folder in its place; it must exist, while a missing `web/` folder only means there are no static files. Every file under the folder is copied to the same path in the output, subfolders included (`web/img/logo.png` becomes `img/logo.png`), except files and folders whose names start with a dot. A static file wins over a generated one of the same name, so `web/index.html` replaces the generated page; such a page loads `nomi.js` and calls `run` itself. A static `app.wasm` or `nomi.js` is refused, naming the file: `` nomi build --web: web/app.wasm: the build writes app.wasm, so a static file cannot have that name ``. So is a static file in `locales/` ending in `.nomil`, which the build writes, and an output folder inside the static folder. A rebuild replaces only the generated files and the static files it copies; anything else in the output folder stays, such as a host's `_headers`, and so does a file copied by an earlier build whose source has since been deleted from `web/`. The one exception is the files earlier versions of `nomi build --web` generated and this one does not, `wasm_exec.js`, `nomi-web.js` and `nomi-worker.js`: a rebuild removes each one whose contents are what such a build wrote (Go's `wasm_exec.js`, or the exact loader or worker it wrote), and leaves a file of that name with any other contents. A static file may have one of those names, and is copied like any other. A rebuild also removes each locale file an earlier build wrote that it does not write.

`--static` without `--web` is a usage error. `--web` with a `--target` other than `js/wasm` is a usage error. A project with Go bindings is refused: `` nomi build --web: this project has Go bindings, and the browser build cannot link them ``. A program importing `std/compiler` builds, and its `app.wasm` is larger because its `nomi-vm` links the front end; `nomi build` says how to serve a module over 20 MB.

A page runs the program with `run`:

```js
import { run } from "./nomi.js";

const { exitCode, fatal } = await run("./app.wasm", {
  args: ["--verbose"],     // startup.args; default []
  env: { NAME: "x" },      // startup.env; default {}
  stdout: (text) => {},    // default: console.log, one call per line
  stderr: (text) => {},    // default: console.error, one call per line
  stdin: "all of it\n",    // or an async () => string | null; default: end of input
  timeoutMs: 10_000,       // default: none
  signal: controller.signal,
  locales: "./locales/",   // where Locale.load fetches <tag>.nomil; default beside app.wasm
});
```

`run` fetches and compiles the module once per URL; every later run of it reuses the compiled module. Each run gets a fresh instance in its own worker, so a page stays responsive while a program loops, and `timeoutMs` and `signal` can stop it: the run then resolves with exit code 124 (timeout) or 130 (abort) and a `fatal` text. `worker: false` runs on the page's own thread, one run at a time; a timeout or abort then resolves the run and drops its output, but cannot stop the program. Output reaches the callbacks as written, so a prompt written with `io.write` and no newline appears before the program waits for input. Standard input is a string, served and then ended; an async function, asked for one line at a time, with `null` ending the input; or absent, so `io.read_line` returns `Err("eof")` at once. The environment holds what `env` gives plus `NOMI_COLOR=never`, unless `env` sets it, so diagnostics carry no colour codes; nothing comes from the page's URL or cookies. Arguments and environment together must fit in the 8 KB Go's loader reserves for them.

The exit code is `nomi run`'s: 0, or 1 for a `main` returning `Err`, a trap or a failed assertion. When the Go runtime stops (an allocation past the 1 GiB cap, a deadlock), the run resolves with exit code 2 and Go's own line, such as `fatal error: out of memory`, as `fatal`. The browser build's limits apply as under `--target js/wasm`: file access fails with an `io.Error`, a call nests at most 1,000 deep, and no signal arrives. The clock, `std/random`, tasks, channels and `sleep` work; tasks share the worker's one thread, so a task that never yields starves the others.

`index.html` loads `nomi.js` with a `<script type="module" src>` and no inline script, so it works under a strict `script-src`. `nomi.js` mounts itself on every element with a `data-nomi-program="app.wasm"` attribute: an output pane with standard error in a distinct colour, an input line that is enabled while the program waits for a line (Enter sends it, Ctrl-D on an empty line ends the input), the exit status, and a button that runs the program again. A page that imports `run` itself needs no such element. The page needs a server, since a browser does not fetch modules from `file:` URLs: `python3 -m http.server -d hello-web` serves one locally. Serve `app.wasm` as `Content-Type: application/wasm`, compressed with brotli or gzip (a hello world is 11 MB, 2 MB with brotli); Cloudflare Pages, Netlify and Vercel do both themselves. Without that content type the loader still works, compiling from the whole response instead of streaming it.

`nomi run --web [--port N] [--open] [--static <dir>] <file>` is the loop for working on such a program. It builds the generated files into a temporary directory and serves them, with the static files served from where they are, by the same rule and with the same precedence and refusals as `nomi build --web`, on `http://localhost:<port>/`, on port 8000 or the next free one up to 8099 unless `--port` names one (`--port 0` takes any free port), with `app.wasm` as `application/wasm`, the locale files as `application/json`, and every response marked `Cache-Control: no-cache`. It prints the URL; `--open` also opens it in the default browser. It then watches the entry, every file the program loads and the project's `nomi.toml`, polling them every 250 ms, and rebuilds once a burst of changes has settled. It polls the static folder too, and a change there reloads the page without a rebuild. The page reloads itself after a successful rebuild. A failed build is printed on the terminal and shown over the page, which keeps running the last build that succeeded until the error is fixed; when the first build fails, the page shows the error alone. A static file given a generated name while the server runs is reported the same way and not served. Every `.html` page served, the generated one or a static one, gets one more script for this, `/__nomi/dev.js`, added before its `</head>` (or at its end when it has none), which listens to the server-sent event stream `/__nomi/events`. Each event is `{"build": n, "static": m, "error": "..."}`: `n` counts the successful builds and `m` the changes to the static files, and a page reloads when either differs from the one it was served at. `nomi build --web` never writes any of this. Ctrl-C stops the server and removes the temporary directory. A project with Go bindings is refused as `--web` refuses it, and the browser program takes no arguments, so a word after the file is a usage error; `--port`, `--open` and `--static` without `--web` are too, and so is `--watch` with `--web`, since `--web` already rebuilds on every change. Without `--web`, `nomi run` takes no flags but `--watch` (§26, *Running again on every change*), and every word after the file is the program's.

### Future: native code

A native backend could come back as a reader of the typed IR if native compilation becomes a goal; the VM is the only engine today.

### Tooling

- Opinionated formatter: `nomi fmt [-w|-l|-d] <paths>`
- Check without running: `nomi check`, which also reports, at its source, any code the program reaches that type-checks but cannot be compiled to run yet
- Language server: `nomi-lsp` (diagnostics, hover, go-to-definition, completion, rename, code actions)
- Debugger: `nomi dap`, a Debug Adapter Protocol server that editors start (VS Code's extension, Neovim's nvim-dap). It debugs `main` of a file, or a file's tests, or the one test `--line` would select, as `nomi run` and `nomi test` run them: line breakpoints (a breakpoint on a line with no statement lands on the next one that has one), step over, into and out, pause, the call stack, and each frame's locals by their Nomi names, shown as a tree whose lists page. A callback's stack shows the operation that called it (`main`, then `Iter.map`, then the lambda), and a frame a tail call replaced is marked, since its caller is gone. It stops where a trap happens, at a failed assertion, and when `main` returns `Err`, and optionally where a failed task's fault reaches the `Task.await` that awaits it. Stepping passes through the standard library unless the launch sets `justMyCode` to false. An interactive program reads its input from the editor's terminal. Watches, expression evaluation and conditional breakpoints are not part of it.

## 26. Entry Point

Nomi programs can be run in two ways, file mode and project mode; a script is file mode:

### File mode: `nomi run <file>`

Runs a specific file. The file must define a top-level `fn main()` callback; a program whose functions a host calls (§5, *Exported Functions*) has none, and is built with `nomi build --web` instead. `fn main` is the program entry point — the host invokes it from the file's own scope, so it does not need to be `pub`. The project root is the directory containing the file — all imports resolve relative to that directory.

```
my_project/
  app.nomi           # declares fn main; `import math` works
  math.nomi          # import path `math`
  models/
    user.nomi        # import path `models/user`
```

```sh
nomi run app.nomi
```

This is the simplest way to run Nomi — no config file needed. Good for scripts, small projects, and getting started quickly.

### Running again on every change: `--watch`

`nomi run --watch <file> [args]` runs the program, then watches the files it
is built from: the entry, every file it imports (not the standard library's)
and its project's `nomi.toml`, polled every 250 ms as `nomi run --web` polls
them. Once a burst of saves has settled, it stops the run if it is still
going, prints `<file> changed; running again` on standard error, and runs the
program again with the same arguments. A run that is computing stops at its
next call or loop iteration, and one waiting on input, a timer, a channel or a
task stops waiting; what a stopped run would still write is dropped. A
program that fails to compile is reported as `nomi run` reports it, and the
next change runs it again. The watched files are worked out again on every
run, so a file the program starts importing is watched from then on.

The program reads standard input as under `nomi run`. A read still waiting
when a file changes ends with the run, and a line typed while the program
restarts goes to the new run. Ctrl-C stops the run and exits with status 130.
A compiled program (`.nomic`) has no source to watch and is refused.

### Scripts: `nomi <file>`

`nomi <file> [args]` is `nomi run <file> [args]`. Together with a shebang line
(§18, *Shebang line*) it lets a file run as a command:

```sh
$ cat hi.nomi
#!/usr/bin/env nomi
import std/io

fn main() {
    io.print("hi")
}
$ chmod +x hi.nomi
$ ./hi.nomi a b
```

The operating system runs `./hi.nomi a b` as `nomi ./hi.nomi a b`. Every
argument after the file, flags included, is the program's own: `boot` reads
them as `startup.args` (§27), here `["a", "b"]`.

Subcommand names are matched first. After them, the first argument is a file
when it ends in `.nomi`, whether or not it exists, so a missing `x.nomi` is
reported as a missing file; no subcommand name ends in `.nomi`, so `nomi test`
is the command and `nomi test.nomi` runs that file. Any other argument is a
file only when it exists and its first two bytes are `#!`; otherwise it is an
unknown command.

A `.nomi` script runs exactly as `nomi run` would run it, including the
project root discovery below. A script alone in a directory such as `~/bin` is
its own project, and sibling scripts do not affect it unless it imports them.
A `.nomi` script inside a module (a `nomi.toml` above it) is held to that
module's `entry_points`, so it must be listed there.

**Extensionless scripts.** A file without the `.nomi` extension is a program
when it starts with `#!`, so a command can be named like any other:

```sh
$ head -1 ~/bin/greet
#!/usr/bin/env nomi
$ greet world        # runs as nomi /home/me/bin/greet world
```

`nomi run`, `nomi check`, `nomi test` and `nomi build` accept such a file by
path. Its project root is its own directory: root discovery looks for
`nomi.toml` or `main.nomi` there and nowhere above, so a `main.nomi` in a home
directory or a module around the script never changes what a command on
`PATH` does. It imports `.nomi` files from its own directory and below, as
`import greeting` reads `greeting.nomi` beside it, and nothing above it. Its
module name is the file name, so a `greet.nomi` beside `greet` is a different
file that `import greet` would read. A script in a module's subdirectory is
not one of the module's entries and needs no `entry_points` line; one beside
the module's `nomi.toml` is, and is listed by its bare name. `nomi check` and
`nomi test` on a directory still look only at `.nomi` files. A script binds
Go as a `.nomi` file does: Go FFI discovery reads the script itself and the
`.nomi` files of its project, but no other extensionless file beside it, and
a `gopkg` resolves through the nearest `go.mod` above the script, as it does
for any declaring file. A script that binds only Go standard library packages
needs no `go.mod`.

### Project mode: `nomi run <package>/<entry>`

Runs from a Nomi module — a directory containing both `go.mod` and
`nomi.toml`. The manifest's `[module].name` is the module's short
name; its `entry_points` is the inventory of files that contain
`fn main`. The project root is the directory containing `nomi.toml`.

```toml
# nomi.toml
[module]
name = "my_app"
entry_points = ["main", "tools/seed"]
```

```
my_project/
  go.mod             # Go-side identity + dependencies (require, replace)
  nomi.toml          # Nomi-side identity + entry points
  main.nomi          # entry — declares fn main + optional fn boot
  tools/
    seed.nomi        # entry — second binary
  math.nomi          # library file (no fn main)
  models/
    user.nomi
  internal/
    impl.nomi        # reachable from anywhere in this module; blocked cross-module
```

```sh
cd my_project
nomi run main             # runs main.nomi
nomi run tools/seed       # runs tools/seed.nomi
```

The compiler enforces the manifest-vs-source invariant: every file
listed in `entry_points` must have exactly one `fn main`, and every
file not listed must have zero. Files inside `internal/` are
reachable only from files rooted at the parent of that `internal/`
(intra-module) and unreachable from any other module (regardless of
`pub`). See §14's *Module boundaries* note.

In both modes, the project root is the same concept — the directory
from which imports resolve. File mode infers it from the file's
location; project mode infers it from `nomi.toml`'s location. The
runtime walks upward from the entry file looking for `nomi.toml`
first, then `main.nomi` as the file-mode fallback.

### Project root discovery

When the runtime or tooling needs to determine a project root from any file in a project — typically a file deep in a sub-directory the user has opened in their editor, not the entry-point — discovery walks upward from that file:

1. First directory containing `nomi.toml` → project root (project mode).
2. Else first directory containing `main.nomi` → project root (file mode).
3. Else the file's own directory → fallback for true single-file scripts.

An extensionless `#!` script's walk stops at its own directory (*Extensionless scripts* above), in the editor as on the command line.

The walk stops at any workspace bound the host has supplied (the editor's open folder, for instance); it never escapes above. `nomi run`, `nomi test`, `nomi check`, the LSP and other tooling share this one rule, so a file opened in an editor resolves its imports to the same files `nomi run` loads. This is why a sub-directory file like `app/prod.nomi` correctly finds `types.nomi` one directory up: the walk lands at the project root that contains `main.nomi` (or `nomi.toml`), not at the file's immediate parent.

### Entry callbacks

`fn main` takes no parameters. A top-level `fn main` that declares any is a compile error over its parameter list, `` `main` takes no parameters ``; its help says a program reads its arguments in `fn boot(startup: Startup)` as `startup.args` (§27). It returns `Unit` (with or without the annotation) or `Result<Unit, E>` for any `E`. Any other declared return type is a compile error at the annotation, `` `main` must return `Unit` or `Result<Unit, E>` ``, because a run would drop the value; its help says to print the value with `io.print` and return `Ok(Unit)`. A `main` that returns `Err(e)` fails the program: `nomi run` (and an executable from `nomi build`) prints `error: ` followed by `e`'s text to stderr and exits with status 1. The text is `Display.to_string(e)` when `e`'s type implements `Display`, so a `String` prints as itself without quotes, and `Debug.inspect(e)` otherwise. An `Err` holding an `AssertionFailure`, which a failed `assert` in `main` returns, prints as that failed assertion instead. A `main` that returns `Unit` or `Ok(Unit)` exits 0.

```
fn main() -> Result<Unit, String> {
    Err("config file not found")
}
```

prints `error: config file not found` to stderr and exits 1.

`fn boot` is optional and belongs to the same entry file as `fn main` (§27). A program with no boot runs under a root context and publishes no application fields.

```nomi
// simplest — no env reach
import std/io

fn main() {
    io.print("Hello, world!")
}
```

```nomi
// with error handling — boot reads the arguments; main may fail
import std/io

struct App {
    name: String
}

fn boot(startup: Startup) -> App {
    #App{name: List.head(startup.args) |> Maybe.with_default("world")}
}

fn main() -> Result<Unit, String> {
    if App.name == "" {
        return Err("the name is empty")
    }

    io.print("Hello, ${App.name}!")
    Ok(Unit)
}
```

```nomi
// app-backed program — fn boot returns Config, including its Context field.
// Code reads fields as `Config.logger` and `Config.port`.
import std/io

interface Logger {
    fn log(logger: self, level: String, message: String)
}

type StdoutLogger

impl Logger for StdoutLogger {
    fn log(_logger: StdoutLogger, level: String, message: String) {
        io.print("[${level}] ${message}")
    }
}

struct Config {
    context: Context
    deployment: String = "prod"
    port: Int = 8080
    logger: Logger
}

fn run() {
    Logger.log(Config.logger, "INFO", "running")
}

fn boot() -> Config {
    #Config{context: Context.root(), logger: StdoutLogger}
}

fn main() {
    Logger.log(Config.logger, "INFO", "starting on port ${Config.port}") // [INFO] starting on port 8080
    run() // [INFO] running
}
```

`fn boot() -> Config` declares its application type
explicitly. `Config` is an ordinary developer-defined struct, and helpers that
construct it use ordinary return-type checking. A `group` runs the same
boot by naming it on its `boot` line (§36).

## 27. Scoped application fields

An entry file, one that defines `fn main`, may define a boot, either
`fn boot() -> App` or `fn boot(startup: Startup) -> App`. A boot that needs no
startup input takes no parameter. Boot builds the application value, an
ordinary struct, and returns it. The struct name is not reserved, and no
interface implementation is required. The struct an entry boot of the project
returns is an *application type*. It may declare at most one top-level field of
the nominal `std/context.Context` type, under any name. Boot receives no
context: one that wants a Context field builds it with `Context.root()` or a
deriver such as `Context.with_timeout(Context.root(), d)`. An application
with no Context field runs under the root context. A boot that returns an
anonymous record publishes fields no read can name.

`Startup` from `std/startup` holds immutable snapshots of the process
environment and arguments:

```nomi
pub struct Startup {
    env: Map<String, String> = %{}
    args: List<String> = []
}
```

Helpers receive startup inputs as ordinary arguments. Environment lookup is
`Map.get(startup.env, name)`. Startup is not itself an ambient value. A test
builds one as an ordinary struct, naming only the inputs it sets: `#Startup{}`
is empty, and `#Startup{args: ["-v"]}` has arguments and an empty env.

The runtime calls a boot that takes no parameter with no arguments, and passes
the process's Startup to a boot that takes one. The parameter may have a
default, `fn boot(startup: Startup = #Startup{}) -> App`. The runtime always
passes the real Startup, so the default serves only a `group`'s `boot`
line that passes none (§36).

```nomi
struct App {
    logger: Logger
    execution: Context
}

fn boot() -> App {
    #App{logger: Stdout, execution: Context.root()}
}

fn audit() {
    with App.logger = #PrefixedLogger{tag: "audit"}
    with App.execution = Context.with_timeout(App.execution, Duration.seconds(3))
    work()
}
```

The boot's signature is checked. A boot that takes anything but no parameter
or one `Startup` (two parameters, or a parameter of another type such as
`Context`) is
`` boot must be `fn boot() -> App` or `fn boot(startup: Startup) -> App`, taking no parameter or one Startup and returning the application struct ``;
one that returns something other than a struct is
`boot must return a struct, the application type`; a struct with two Context
fields is `an application struct may have at most one Context-typed field`. A
`fn boot` in a file without `fn main` is
`` `boot` belongs in an entry file, one that defines `fn main`; a `group` names an entry's boot with `boot entry.boot(startup)` ``.
An entry boot is not an ordinary function. The one place code calls it is a
`group`'s `boot` line; a call or reference anywhere else is
`` entry-point boot cannot be called or captured as an ordinary function; a `group` calls it on its `boot` line ``.
A test file that names a boot imports its entry file, so a boot that tests run
is `pub`, and so is the struct it returns.

`App.field` reads one top-level field of the published application value. The
read names the application type, so a file that reads a field imports the type
as it imports any other name. `T.field` is an application-field read only when
`T` is an application type, which the checker learns from the entry boots in
the project: the entry file's own, and those of the entry files it imports. On an application type, `App.field` always names the
field: an application type may not declare an inherent function or `once` with
a field's name. On any other type, `Type.member` keeps its owner-qualified
meaning.

`with App.field = expr` is a statement that replaces one field. The
replacement lasts from that line to the end of the enclosing block, the extent
a `defer` registered on the same line would have, and it reaches every
function called in that extent. However the block exits (reaching its end,
`return`, `try`, or a failure), the previous value comes back. The value must
have the field's declared type, and a `.Variant` value resolves against it. A
struct-literal value needs no parentheses: `with App.store = #FakeStore{}`.

Several fields are replaced by several `with` lines. Each value is evaluated
when its line runs, so a later line reads the fields an earlier line replaced:

```nomi
fn in_region() {
    with App.region = "eu"
    with App.endpoint = "https://${App.region}.example.com"
    sync()
}
```

A `with` works in any block: a function body, an `if` or `case` arm, a block
statement, a lambda's body, a test body, or a group's `setup`. It has no value
and takes no block. To limit an override to part of a function, put the `with`
line and the code it governs in a block statement, or move them into a helper
function:

```nomi
fn publish_quietly() {
    {
        with App.logger = Silent
        warm_cache()
    }

    publish()
}
```

There is no whole-application override and no nested-field override. Replace a
structured field with an immutable update such as
`with App.database = #{..App.database, host: "localhost"}`.

The parser and checker reject the other shapes. A comma after the value is
`` a `with` statement replaces one field; write one `with` line per field ``. A
`{` after the value on the same line is
`` `with` is a statement and takes no block: the override lasts to the end of the enclosing block ``.
A target that is not an application field is
`` `with` replaces an application field, such as `MyApp.logger`; `Point.x` is not one ``,
and a value of the wrong type is `` `App.port` replacement expects Int, got String ``.

An override changes the fields in force; it does not change values already
computed. A closure reads the fields in force when it is called, not when it was
created, so a closure returned from a function after a `with` line does not
carry that override. A task spawned with `Task.spawn` takes a snapshot of the
fields in force when it is spawned. String interpolation is `${...}`,
including `${App.port}`.

```nomi
import std/io

struct App {
    greeting: String
}

fn boot() -> App {
    #App{greeting: "hello"}
}

fn greet() -> String {
    "${App.greeting}, world"
}

fn quiet_greeter() -> () -> String {
    with App.greeting = "psst"
    io.print(greet()) // psst, world
    fn -> greet()
}

fn main() {
    later = quiet_greeter()
    io.print(later()) // hello, world
}
```

Each entry's `main` runs under its file's boot, or none. Each test runs under
its group's `boot` line (§36), or none. A boot may return any application type,
but it must return the type named by every application-field read reachable
from the code it runs: the `main` or test body, a group's `setup`, and every
function they call, across files and through the callbacks and lambdas they
run. A mismatch is an error at the boot that names the read and where it is,
for example
`` this test boots `Fake`, but code it runs reads `MyApp.logger` (server.nomi:2) ``;
with no boot it is reported at the `main` or the test
(`` this program has no boot, but code it runs reads `App.port` (main.nomi:9) ``).
Reads of two application types reachable from one boot are an error at that
boot. Code that reads no application field runs under any boot, or none.

The runtime publishes application fields only after boot succeeds. Direct or
indirect reads of them during a boot are rejected, and so are reads in a group
`boot` line's argument, which runs before the boot it calls
(`` boot runs before any application field is published, but code it runs reads `App.port` (main.nomi:4) ``).
The runtime identifies a custom context field by declared type, so a field
named `execution` is read and replaced as `App.execution` and receives the
same context protections.

Direct outer-body boot defers run after main and owned task shutdown. Test boot
and setup defers run after the individual test and owned work. Helpers and
nested blocks retain ordinary block-scoped cleanup. Initialization failure
skips main or setup/body, shuts down started work, and runs all registered
cleanup. Deferred calls retain evaluated arguments and the scoped environment at
registration; cleanup failures do not hide the initial failure or prevent
remaining cleanup.

## 28. Context

A **Context** is an opaque, per-life value carrying deadline information and execution-scoped metadata through a call chain. Unlike dependency handlers such as `Logger` or `Clock`, Context is short-lived: one context per request, per worker, or per sub-life the program defines. A deadline is the only state a Context carries; a task being stopped — `Task.cancel`, a supervisor drain, shutdown — travels on task ownership and is acted on at the safe points described in §20, never written back onto the Context value.

Context is installed independently of the application struct and read through its Context field, `Config.context` below:

```nomi
import std/duration.Duration

struct Config {
    context: Context

    port: Int = 8080
}

fn boot() -> Config {
    #Config{context: Context.root()}
}

fn handler(req: Request) -> Response {
    with Config.context = Context.with_timeout(Config.context, Duration.seconds(30))
    do_expensive_work(req)
}
```

What makes Context special is that user code cannot construct one from scratch: only the language runtime, `Context.root()`, and the `Context.with_*` derivers produce Context values. Boot receives no context; it builds the one it returns, usually from `Context.root()`, and that Context field is installed for main. Embedder cancellation and inherited deadline floors remain effective when scoped context values are replaced.

### Why `context` is a field and not ambient

Access to the context field is dynamically scoped: a `with App.context = …` override reaches a deep callee through intermediate functions that never mention the field, and no signature anywhere declares it. So naming the field is not what restricts who can read the context, and it should not be described as if it were.

What the field buys is that the **rebind has a syntactic home**. The statement `with App.context = next` is one place, evaluated once where it is written, and it is where the deadline floor is pinned and a single Go context is derived for the blocking operations that observe it — the timer sleep, both channel halves, task await and outcome, the cancellation probe, the scope wait, the supervisor flush, and the batch semaphore. Deriving per read instead would allocate a context and a timer for every element of an iterator pipeline, which is the constraint that decides this.

An ambient spelling would have to reproduce that scope form with no field to name. A block-taking function (`Context.scope(next) { … }`) would take a lambda, and `return` is lambda-scoped, so a function could not return out of the scoped region. `with` is a statement in an ordinary block, so a `return` after it returns from the function, and the override ends with the block.

Boot’s declared return type defines the available application fields, and code that reads one names that type.

### Context Values

Context values are for request- or execution-scoped metadata that should follow the current unit of work: trace IDs, auth subjects, request IDs, or similar metadata that crosses API boundaries. They are **not** the dependency/capability mechanism; use ordinary payload fields such as `App.store` and `App.logger` for dependencies. A locale is not a context value either: a function whose output depends on one takes a `Locale` argument, and an application keeps the current one in an application field (§41 *Locales and Plural Rules*).

Values are addressed by their Nomi type:

```nomi
type TraceId String

with App.context = Context.with_value(App.context, TraceId("trace-123"))

case Context.value(App.context, TraceId) {
    Some(TraceId(id)) -> Logger.info(App.logger, "trace ${id}")
    None -> Logger.info(App.logger, "no trace")
}
```

The value's Nomi type is the context key. `Context.with_value` returns a child context with the value associated to its type; parent contexts are not mutated. `Context.value(context, T)` walks from the current context to its ancestors and returns the nearest value of type `T`. In that second argument position, `T` is a contextual `Type<T>` witness: a bare type name becomes a value only because the function expects `Type<T>`. The type should usually be a domain type (`trace.Id`, `Auth.Subject`) rather than a raw scalar, so unrelated metadata cannot collide on `String`.

The usual pattern is to wrap a context value in a tiny file so callers do not traffic in the low-level `Context.value(context, T)` form:

```nomi
// trace.nomi
pub type Id String

pub fn with_id(context: Context, id: Id) -> Context {
    Context.with_value(context, id)
}

pub fn id(context: Context) -> Maybe<Id> {
    Context.value(context, Id)
}
```

### The `context` stdlib module

The module's entire public surface is the opaque `Context` type with inherent ops — every call is type-qualified (`Context.deadline(c)`). There are no file-level free functions.

```nomi
pub host type Context

impl Context {
    pub host fn root() -> Context

    pub host fn deadline(c: Context) -> Maybe<Instant>

    pub host fn deadline_remaining(c: Context) -> Maybe<Duration>

    pub host fn with_deadline(c: Context, at: Instant) -> Context

    pub host fn with_timeout(c: Context, dur: Duration) -> Context

    pub host fn with_value<T>(c: Context, value: T) -> Context

    pub host fn value<T>(c: Context, value_type: Type<T>) -> Maybe<T>
}
```

`with_deadline` takes an absolute `Instant`; `with_timeout` is sugar for deriving a deadline at `now() + dur`. Both return a fresh Context. Deadlines flow from parent to child and only tighten: the earliest deadline anywhere along the chain is the one in force, so a child can bound its own work and can never loosen the bound it inherited. There is no cancellation predicate on the surface. A task stopping is not a state its body reads, because the runtime consults the task's cancellation at every safe point and unwinds the task there, ahead of any check the body could run.

### Forward compatibility

Per-life Context is the foundational building block for the concurrency story (§20). Structured concurrency (`concurrent { }` blocks, `Task.spawn` / `Task.await`, `std/channels`, `timer.sleep`) composes directly with the Context machinery here: each `concurrent` block derives its own context from the active app context, every task inherits it, and cancellation-aware ops (`timer.sleep`, `Sender.send` / `Receiver.receive`) `select` on the context's done channel so cancellation and deadlines cascade naturally across task boundaries. Put `with App.context = Context.with_timeout(App.context, d)` before the work, in the block that holds it, to give it a deadline. The v1 Context surface is intentionally minimal; adding `with_cancel` for explicit cancel handles remains a deliberate follow-up gated on real use cases.

## 29. Tail Call Optimization

Tail calls run in constant stack space. Which positions are tail, how they interact with `defer`, and the limit on non-tail recursion are in §12 *Tail-call optimization* and *Recursion depth*.

## 30. Equality and Ordering Semantics

Structural equality everywhere. `==` compares values deeply, not references.

```nomi
// structs — all fields compared
#User{name: "Alice", age: 30} == #User{name: "Alice", age: 30} // True

// enums — variant + carried data
#Shape.Circle{radius: 5.0} == #Shape.Circle{radius: 5.0} // True

#Shape.Circle{radius: 5.0} == #Shape.Rectangle{width: 5.0, height: 5.0} // False

// collections — deep equality
[1, 2, 3] == [1, 2, 3] // True

%{"a" => 1} == %{"a" => 1} // True

// tuples
#(1, "hello") == #(1, "hello") // True
```

**NaN equality is reflexive.** IEEE 754 says `NaN != NaN`, but Nomi's `==` is reflexive on `Float` so that pattern matching, `Maybe`/`Result` equality, and structural equality on containers all behave consistently — `nan == nan` is `True`, and `Some(nan) == Some(nan)` is `True`. This deviates from IEEE 754 in `==` only; the underlying Float64 representation and arithmetic are unchanged. To test for NaN-ness specifically, use `Float.nan?(x)`.

**Functions and Iters cannot be compared.** Using `==` or `!=` on a function or an `Iter<T>` is a compiler error: a function's only identity is its closure, and an `Iter` is a function over its source, so comparing two would either compare closures or run both sources. A value that holds one — a struct field, a tuple or record part, a list element, a `Maybe`/`Result` or enum payload — is also incomparable, and `==` on it is a compiler error naming the part (`` `==` cannot compare `Feed`: it holds `Iter<Int>`, and an Iter has no equality ``). A struct or enum with its own `impl Equatable` compares through that impl, so write one to compare such a type by its other fields; to compare an Iter's elements, materialize it with `Iter.to_list` first. A `Set` finds its elements and a `Map` its keys by equality, so such a value cannot be either: building a `Set` or `Map` over one (`%[it]`, `%{f => 1}`) is a compiler error at the expression that builds it (`` `Iter<Int>` cannot be a Set element: an Iter has no equality or hashing ``).

**Handles to running state cannot be compared.** std's `Task<T>`, `Sender<T>`, `Receiver<T>`, `Context` and `Supervisor` each refer to something the runtime is running: a task, one half of a channel, a cancellation scope, a supervisor's workload. None has a value to compare, so `==` or `!=` on one, or on a value that holds one, is a compiler error under the same rule as a function (`` `==` cannot compare `Sender<Int>`: a handle to running state has no equality ``), and none can be a `Map` key or a `Set` element.

```nomi
// compiler error: functions cannot be compared
f = fn x -> x + 1

f == f

// compiler error: Handler contains function field on_success
struct Handler {
    on_success: (String) -> Unit
}

a = #Handler{on_success: fn s -> io.print(s)}

a == a
```

### Custom equality via `impl Equatable`

`==` on a type *without* an `impl Equatable` block uses the structural fallback above — no declaration needed. A hand-written `impl Equatable` block **overrides** the structural default: `==` / `!=` on a struct type with a registered `Equatable.equal?` route through that impl. That's how `std/calendar`'s `OffsetDateTime` / `DateTime` get instant-only equality (same moment in two zones is `==`; the zone is display metadata, not identity — see §39). Primitives keep the direct-comparison path and never dispatch.

The override holds wherever the value sits. `==` on a container, a tuple, or a struct or enum whose own `Equatable` is derived or absent compares each nested value of such a type through its impl (as a derived impl's field-by-field `==` does; Rust's derived `PartialEq` is the same rule), and a `Map` key or `Set` element matches the same way (see `Map<K, V>` in the collections list for how the bucket is chosen). So `[a] == [b]`, `Some(a) == Some(b)`, `%[a, b]` and `Map.get(%{a => 1}, b)` all agree with `a == b`.

### Ordering requires a declared `Comparable`

Equality and ordering are deliberately asymmetric:

- **Equality has exactly one canonical structural meaning** — same shape, equal parts, invariant under field reordering — so falling back structurally is almost always what the user meant, and an `impl Equatable` exists to *override* it.
- **Ordering has no canonical structural meaning.** Which field orders first is an arbitrary choice, it is *not* invariant under reordering fields (a pure-refactor hazard if implicit), and a wrong implicit order fails silently — a sort mis-sorts rather than erroring. Nomi is fail-loud, so ordering must be declared.

Concretely: `<` `>` `<=` `>=` on a concrete type with no `Comparable` impl is a **compile-time missing-impl error**:

```nomi
struct Point {
    x: Int
    y: Int
}

#Point{x: 1, y: 2} < #Point{x: 3, y: 4}
// error: no impl of `Comparable` for `Point` (required via ordering operator `<`)
// help: add `derive Comparable` to `Point` or write an `impl Comparable for Point` block
```

Add `derive Comparable` (field-by-field lexicographic, §38.1) or a hand-written `Comparable.compare` function and the operators work, routed through `Comparable.compare`. The stdlib declares `Comparable` for the naturally-ordered types — `Int`, `Float`, `Decimal`, `String`, `Bool`, `List` (lexicographic), `Duration`, and the `std/calendar` ladder — while `Map` is deliberately non-`Comparable` (no natural total order) and opaque `Instant` exposes `Instant.before?` instead. Nameless structural types — **anonymous structs** (`#{x: 1} < #{x: 2}`) and **tuples** (`#(1, 2) < #(3, 4)`) — are *also* a compile-time error here, and unrecoverably so: there is no declaration to attach an impl (or `derive Comparable`) to, so the diagnostic reads `no impl of `Comparable` for `#{x: Int}` … anonymous struct types cannot implement `Comparable` rather than suggesting a fix. (Equality is unaffected — `#{x: 1} == #{x: 1}` works via the structural fallback.) This is Python's line (positional containers compare, records don't), and the opt-in posture matches Rust/Haskell; OCaml/Erlang-style universal compare is a known footgun Nomi declines.

## 31. Module & Dependency Management

A Nomi module's identity is split between `go.mod` (Go-side identity and
dependencies) and `nomi.toml` (Nomi-side identity and entry points). A
Nomi module is a Go module, so it is published by pushing a version tag and
fetched through the Go module proxy, without the `go` command. `nomi
publish`, a registry, and compiler-enforced semver are not implemented;
each carries a status callout below.

### Project config

A Nomi module's identity lives in two files at the module root:

- **`go.mod`** — the Go module path (used by `go` tooling and the
  module proxy), dependency graph (`require` / `replace`), and Go
  version. Standard `go.mod` syntax; nothing Nomi-specific. `nomi` writes
  it (`nomi new`, `nomi add`, `nomi remove`, `nomi update`, `nomi fetch`),
  and a Nomi project commits it and `go.sum` without editing either.
- **`nomi.toml`** — the Nomi-side identity (`[module].name`, the
  module's short name used by other modules' import paths) and the
  entry-point inventory (`[module].entry_points`).

```toml
# nomi.toml
[module]
name = "my_app"
entry_points = ["main", "tools/seed"]
```

```
# go.mod
module github.com/example/my_app

go 1.22

require example.com/shared_kit v0.0.0
replace example.com/shared_kit => ../shared_kit
```

The two files are siblings; neither subsumes the other. Cross-module
dependencies are Go's `require` directives: a required module whose root
holds a `nomi.toml` is importable under its `[module].name` (§14). A
`replace` that points a requirement at a local directory makes that
directory the module, as in Go; `nomi add ../shared_kit` writes both lines
above.

### Dependencies

A required module comes from the Go module proxy, as the `go` command
fetches it, with no Go toolchain needed for a module that is all Nomi:

- **Which modules.** A project needs the modules its `go.mod` requires
  directly (no `// indirect`), and, for each that is a Nomi module, the
  modules that module's `go.mod` requires and the project's `go.mod` also
  lists. Each is taken at the version the project's `go.mod` requires; `nomi
  add`, `nomi update` and `nomi fetch` keep that list complete, as `go mod
  tidy` does, and `nomi remove` drops what no module needs any more.
  `run`, `test`, `check` and `build` download them for a Nomi module (a
  `nomi.toml` beside its `go.mod`); a Go module that merely holds `.nomi`
  files leaves its Go dependencies to the `go` command.
- **Download.** Module zips come from `GOPROXY` (default
  `https://proxy.golang.org,direct`) through the module proxy protocol, into
  the Go module cache (`GOMODCACHE`), which the `go` command shares; a module
  already there is not downloaded again. A module that `GOPROXY`,
  `GONOPROXY` or `GOPRIVATE` sends `direct` is read from its version control
  repository by the `go` command.
- **Verification.** Each zip's hash is checked against `go.sum`. A module
  `go.sum` does not list is checked against the checksum database
  (`GOSUMDB`, default `sum.golang.org`; `GONOSUMDB`, `GOPRIVATE` and
  `GOSUMDB=off` exempt modules as they do for Go). A hash that differs from
  either is an error and installs nothing.
- **When `go.sum` changes.** `nomi add`, `nomi update` and `nomi fetch`
  write the checksums of what they download to `go.sum`, and `nomi remove`
  and `nomi update` drop the lines of what they took out. `nomi run`, `test`, `check` and `build`
  download what is missing but, as Go's `-mod=readonly` default, refuse a
  module `go.sum` does not list and say to run `nomi fetch`;
  `GOFLAGS=-mod=mod` lets them write it.
- **The language server** reads only the module cache and never downloads.
  An import of a module that is required and not in the cache is an error
  whose hint says to run `nomi fetch`.

A module's Go code (§24's `gopkg` bindings), in the project or in a
dependency, is built into the program's runner with a Go toolchain: the `go`
on `PATH`; or, with none, the Go release `nomi` was built with, which `nomi`
downloads once from go.dev, checks against a SHA-256 compiled into `nomi`,
and keeps in its cache (`<user cache>/nomi/go/`). `NOMI_GO` names a `go` (or
a Go root) to use instead of both. The language server uses that toolchain
only once it is downloaded. Go bindings build with `CGO_ENABLED=0`, so a
`gopkg` naming a Go package that needs cgo (one whose files, or those of a
package it imports, import `"C"`) is an error at the `gopkg`.

### Locales: `[i18n]`

`nomi.toml`'s `[i18n]` table names the locales whose CLDR data the program
carries (§41 *A program's locale data*) and how its messages are translated
(§41 *Messages*):

```toml
# nomi.toml
[i18n]
source_locale = "en"
locales = ["en", "pl", "pt", "pt-PT"]
partial_locales = ["de"]
catalogs = "locales"
```

Each entry is a BCP 47 tag, read as `Locale.parse` reads one and kept in its
canonical spelling (`"PL"` is `pl`), in the order written, which is the
order `Locale.available()` answers. A program with no `[i18n]` table carries
every locale. Each of these is an error at the manifest, which `nomi check`,
`nomi run` and the editor report: an empty list, an entry that is not a tag
(`"pt_BR"`, whose error suggests `pt-BR`), one listed twice after
canonicalization, `und` (CLDR's root, which every program carries), and a
tag for which neither CLDR nor any parent of it but root has data (`qaa`).

`source_locale` is the locale a `message` declaration's own text is in,
English when it is left out. `partial_locales` are locales whose catalogs may
leave a message out, which then shows in the source locale; they need
`locales`, are checked as its entries are, may not repeat one, and the
program carries their data too. `catalogs` is the directory under the module
root holding a directory of catalogs per locale, `locales` when it is left
out; it must stay under the root. `web_embedded` are the locales a browser
build compiles into `app.wasm` (§41 *Locales in the browser*), the source
locale when it is left out; it needs `locales`, and each entry must be one
`locales` or `partial_locales` lists.

### Custom currencies: `[money]`

`nomi.toml`'s `[money]` table lists the custom currencies (§41 *Money*) the
module's source may write as a literal:

```toml
# nomi.toml
[money]
custom_currencies = ["PTS:0", "BTC:8"]
```

Each entry is a custom currency as its literal writes it, the code, a colon
and the minor digits, and must be one `Currency.custom` accepts. Each of
these is an error at the manifest: an entry with no digits (`"PTS"`), a
malformed code (`"pts:0"`), an ISO 4217 code (`"EUR:2"`), digits that are not
a whole number from 0 to 18 written without leading zeros (`"PTS:19"`,
`"PTS:00"`), and an entry listed twice. A module with no `[money]` table, or
an empty list, writes no custom currency literal. The table is the module's
own: a dependency's literals answer to the dependency's `nomi.toml`, and the
application's list does not reach them.

### CLI

The `nomi` CLI is the single tool:

- `nomi run <file>` — file mode (single-file scripts).
- `nomi <file> [args]` — the same as `nomi run`, for a file that ends in
  `.nomi` or starts with `#!`; a `#!/usr/bin/env nomi` script runs as this
  (see §26, *Scripts*).
- `nomi run <module>/<entry>` — project mode (a module with
  `nomi.toml`'s entry_points; see §26).
- `nomi run --watch <file> [args]` — run the program, and stop it and run
  it again whenever a file it is built from changes (see §26).
- `nomi test [path] [--line N] [--format text|json] [--timeout <duration>] [--watch]`
  — run tests in a `.nomi` file or discover tests under a directory;
  `--watch` runs them again on every change, and a test that runs longer
  than `--timeout` (10 seconds unless given) is stopped and fails; see §36.
- `nomi` — the REPL.
- `nomi check <path>` — type-check and compile without running; code
  the program reaches that the compiler cannot lower is the error at its
  source that `nomi run` would stop with and `nomi test` would report as
  `BLOCKED`. A test file is checked as `nomi test` loads it, each case
  lowered and none run, and a directory's test files, and a module's beside
  its entries, are checked with the rest.
  In a directory without `entry_points`, a file that another file there
  imports is checked through that importer, as `nomi run` loads it, and
  listed as `ok <file> (through <importer>)`; a file nothing there imports
  is checked on its own.
  A failed check of a directory prints `FAIL <file>` for each file that
  holds errors, a file the program imports or an input a macro read, with
  its errors below it, and an error two programs share once.
- `nomi build <file> [-o <binary>] [--target <goos>/<goarch>]` — one
  executable: nomi-vm with the program's IR appended. It runs at
  `nomi run` speed without the startup lowering; it is not native code
  (see §25).
- `nomi build <file> --web [-o <dir>] [--static <dir>]` — a folder a static
  host serves that runs the program in a browser, with the files of its
  `web/` folder copied in (see §25, *A program in the browser*).
- `nomi build <file> --no-vm [-o <out>]` — the compiled program alone, a
  `.nomic` file (see §25, *Compiled programs and nomi-vm*).
- `nomi build-vm [--target <goos>/<goarch>] [--compiler] [-o <out>]` —
  nomi-vm with no program, which runs a `.nomic` file it is given;
  `nomi build-vm --reactor [--compiler] [-o <out>]` writes the WASI reactor
  with no program and `nomi-wasi.js` beside it (see §25).
- `nomi run --web [--port N] [--open] [--static <dir>] <file>` — serve that
  folder on localhost, rebuilding and reloading the page when the source
  changes and reloading it when a static file changes.
- `nomi fmt [-w|-l|-d] <paths>` — format Nomi source.
- `nomi expand <file>` — print what the file's declarations written with a
  macro's keyword expand to (see §45).
- `nomi tool <module> <name> [args]` — run a module's tool,
  `tools/<name>.nomi`, with every effect (see §45).
- `nomi new <module path> [dir]` — start a module in `dir` (a new
  directory named for the path's last element when left out): a
  `nomi.toml` whose `name` is that element without a `/vN` suffix, written
  as an identifier (`hello-world` is `hello_world`), with `entry_points =
  ["main"]`; a `go.mod` with the `module` line and the `go` line of the Go
  release `nomi` pins; and a `main.nomi` that prints hello. It refuses a
  directory with a `go.mod`; in one with a `nomi.toml` and no `go.mod` it
  writes the `go.mod` alone. It prints what it wrote and the next command.
- `nomi add <module>[@version]` — require a module in `go.mod` (its latest
  release when no version is given, else a full version such as `v1.2.3`),
  with at least the versions of what it requires, download it, and write
  the checksums to `go.sum`. A Go package's path adds the module that holds
  it, the longest prefix of the path the proxy has.
- `nomi add <directory>` — for an argument that starts with `./`, `../` or
  `/`: require the Nomi module in that directory (`require <its module
  path> v0.0.0`, from its `go.mod`) and `replace` it with the directory,
  relative to the project root. A directory without a `go.mod` or a
  `nomi.toml` is refused.
- `nomi remove <module>` — take a requirement, and any `replace` of it, out
  of `go.mod`, with each `// indirect` requirement no remaining module's
  `go.mod` requires, and their lines out of `go.sum`. `<module>` is the
  module path or the module's short name. It refuses while a Nomi file of
  the project imports the module, or binds a Go package of it with
  `gopkg`, and names the file and line.
- `nomi update [module]` — move one requirement, or every one not replaced
  with a directory, to the highest release of its module path, as `go get
  -u` does: never a new major version (that is another path, `/v2`), and
  never a pre-release. It raises what the new versions require, writes
  `go.sum` (dropping the old versions' zip checksums), and prints each
  change as `<path> <old> -> <new>`.
- `nomi fetch [--target <goos>/<goarch>]... [dir]` — download everything the
  project needs: the modules (adding to `go.mod` what a Nomi dependency
  requires), and, for a project with Go bindings, the Go toolchain when
  there is no `go`, the Go modules its bindings link, and the runner for
  each `--target`. A later command with `--offline` then needs no network.
- `--offline` on `run`, `test`, `check`, `build`, `fetch` and `add` —
  download nothing; a module or toolchain the caches lack is an error that
  names it. `GOPROXY=off` refuses module downloads the same way.
- `nomi version` — print the version.

> **Status: not yet implemented.** `nomi publish` is reserved. Publishing
> a module is pushing a version tag to its repository, as for any Go
> module; the module proxy serves it from there.

### Registry & Distribution

Distribution rides on the Go module proxy: a Nomi module is a Go module,
so a module pushed with a version tag to a public repository (GitHub,
GitLab, etc.) is served by `proxy.golang.org` and fetched as *Dependencies*
above describes. Several modules may share one repository, each in a
subdirectory with its own `go.mod` and tags prefixed by it
(`sqlite/v0.3.0`), as Go lays them out.

> **Status: not yet implemented.** No Nomi-specific registry, and none
> is planned; if Nomi-specific module discovery becomes a need, the
> simplest path is metadata indexing on top of existing Go module
> hosting rather than a parallel registry.

### Lock File

Nomi has no lock file of its own. A Nomi module's dependencies are Go
module requirements, so `go.mod` pins their versions and `go.sum` their
contents, and `nomi` checks every download against it (*Dependencies*
above); a separate `nomi.lock` is not planned.

### Compiler-Enforced Semver

> **Status: not yet implemented.** The intent — a compiler that diffs
> the public API between versions and rejects mismatched version
> bumps — is a long-horizon nice-to-have. The closest existing precedent is Elm/Gleam; the
> mechanism requires both a registry hook (where to compare against)
> and a published-API extraction step that doesn't exist yet.

## 32. Numeric Literals

### Integers

```nomi
42 // decimal

1_000_000 // underscores for readability

0xFF // hexadecimal

0b1010 // binary

0o77 // octal (Go/Rust style, not C-style 077)
```

### Floats

```nomi
3.14 // basic float

0.5 // leading zero required (.5 is not allowed)

1.0 // trailing digit required (1. is not allowed)

1.0e10 // scientific notation

2.5e-3 // negative exponent

1_000.123_456 // underscores in floats
```

### No Implicit Conversion

Int and Float are distinct types. No implicit conversion between them — use the explicit conversion functions in `std/int` / `std/float`:

```nomi
x = 5

y = 3.14

z = x + y // error: cannot add Int and Float

z = Int.to_float(x) + y // fine — total: every Int64 → Float64

w = Float.to_int(y) // Maybe<Int> — truncates toward zero;
// None on NaN, ±Infinity, or out of Int64 range
```

`Float.to_int` returns `Maybe<Int>` because some Float values have no Int64 representation (`NaN`, `±Infinity`, `|x| > 2^63`). For other rounding rules, compose with `Float.round` (half-to-even), `Float.floor`, or `Float.ceil` first — each returns a `Float`, leaving the conversion as a single concern.

### Float Special Values

Nomi's `Float` is IEEE 754 binary64, which means three special values are part of the type:

- **NaN** ("not a number") — produced by indeterminate operations like `0.0 / 0.0`.
- **+Infinity** — produced by overflow and by dividing a positive Float by `0.0`.
- **-Infinity** — produced by overflow and by dividing a negative Float by `0.0`.

These are *values* of `Float`, not separate types. Constructed via `Float`:

```nomi
Float.nan() // NaN

Float.positive_infinity() // +Inf

Float.negative_infinity() // -Inf
```

Tested via predicates:

```nomi
Float.nan?(x) // True if x is NaN

Float.infinite?(x) // True if x is +Infinity or -Infinity

Float.finite?(x) // True if x is a real number — not NaN, not ±Infinity
```

Float division by zero **does not error** — it produces the IEEE 754 value:

```nomi
1.0 / 0.0 // +Inf

-1.0 / 0.0 // -Inf

0.0 / 0.0 // NaN
```

Int division by zero still errors at the operator (`1 / 0` is a runtime error), because Int has no spare bit patterns to encode "no value" — every Int64 is a valid integer. Code that wants total integer division validates the divisor first with `Int.to_non_zero(y) -> Maybe<NonZeroInt>`, then calls `Int.divide(x, nz) -> Int`. The infix operator stays direct, and the fallible step is named at the boundary where the divisor becomes known-safe.

NaN and ±Infinity propagate through arithmetic and through the rounding helpers (`Float.round`, `floor`, `ceil`, `trunc`) per IEEE 754 — `Float.round(NaN)` is `NaN`, `Float.floor(+Inf)` is `+Inf`, etc.

`Int`, `Float` and `Decimal` each have `abs`, `sign` (an `Int`: -1, 0 or 1),
`min` and `max`. On `Float` they follow IEEE 754-2019:

- `Float.abs` clears the sign bit, so `Float.abs(-0.0)` is `0.0`, the abs of
  either infinity is `+Inf`, and the abs of NaN is NaN.
- `Float.sign` is 0 for both zeros, and 0 for NaN too: NaN is neither less
  than nor greater than zero, and no `Float` function traps on a NaN operand.
  Check `Float.nan?` first where NaN must be told apart from zero.
- `Float.min` and `Float.max` are IEEE 754-2019's `minimum` and `maximum`:
  NaN if either operand is NaN, and `-0.0` is the lesser zero, so
  `Float.min(0.0, -0.0)` is `-0.0` and `Float.max(-0.0, 0.0)` is `0.0`. For
  any other tie they return their first argument.

#### NaN and the three comparison behaviours

`Float` compares three different ways, and they do not all agree. Two of the
three are forced, so this is a documented consequence rather than a wart to file:

| | Behaviour | On NaN |
|---|---|---|
| `==` / `!=`, `Equatable.equal?` | **reflexive** | `nan == nan` is `True` |
| `Comparable.compare` | **a total order**, NaN at the top | `compare(nan, nan)` is `Equal` |
| `<` `<=` `>` `>=` | **IEEE** | `nan <= nan` is `False` |

**Why `==` is reflexive rather than IEEE:** `Hashable` requires that equal values
hash equally, and Nomi's `Map` keys on structural equality — so an IEEE `==`
would make a `NaN` key unretrievable after insertion. `impl Hashable for Float`
normalizes NaN and signed zero for exactly this reason. **Why `compare` agrees
with `==`:** so that `Iter.sort` is deterministic and collects NaNs at one end
rather than depending on comparison order.

**The residual:** `a == b` does not imply `a <= b` when both are NaN. This is
unobservable except on NaN, and it is the price of the hashing law.

Java carries the same three behaviours for the same reason across three
spellings — `==` (IEEE), `Double.equals` (reflexive, so `HashMap` works), and
`Double.compare` (total). Nomi has two spellings, and its `==` is Java's
`.equals()`. Sorting or keying on `Float` therefore behaves predictably; only
the ordering *operators* retain IEEE's non-reflexivity.

### Ranges

Two bounded literal forms desugar to a `Range<T>` value:

```nomi
1..5 // Range<Int>, half-open bounded — 1, 2, 3, 4 when iterated

1..=5 // Range<Int>, closed bounded — 1, 2, 3, 4, 5 when iterated

"a".."m" // Range<String>, half-open interval

0.0..=1.0 // Range<Float>, closed interval

1.25d..=2.50d // Range<Decimal>, closed interval
```

`..` is half-open (the unmarked default); `..=` is the explicit inclusive form. Both forms require a left and right operand of the same `Comparable` type; `..` is reserved elsewhere as the list-spread element marker, so open-ended forms (`..5`, `5..`, `..`) are not valid range syntax. For unbounded ascending integer sequences use the stdlib constructors:

```nomi
Range.from(1) // unbounded ascending from 1 — pair with Iter.take

Range.naturals() // unbounded ascending from 0
```

Reversed integer bounds (`5..1`) produce an empty range — use `Iter.reverse(1..=5)` for descending iteration.

The opaque struct backing the literals — the fields are internal; interact through the constructors, the `Iter` impl, and the query functions below:

```nomi
opaque struct Range<T> where T: Comparable {
    start: T
    end: Maybe<T>
    inclusive: Bool
}
```

`end` is `None` only when the value is built via `Range.from(N)` or `Range.naturals()`; in that case `inclusive` carries no meaning and is canonically `False`. `Range<T>` implements `Iter<T>` when `T` implements `Discrete`, because iteration needs a successor operation. Bounded discrete ranges terminate; unbounded integer ranges don't (they are `Iter.infinite?`, and a terminal that must reach their end traps), so bound them with `Iter.take(N)` downstream — `Range.from(1) |> Iter.take(10) |> Iter.to_list()`. Non-discrete ranges are interval values: use `Range.contains?("a".."m", "h")`, `Range.bounded?(0.0..=1.0)`, `Range.contains?(1.25d..=2.50d, 1.50d)`, and similar queries.

Container-specific functions (`std/ranges`):

```nomi
fn from(n: Int) -> Range<Int>              // unbounded ascending from n
fn naturals() -> Range<Int>                // unbounded ascending from 0
fn contains?(c: Range<T>, x: T) -> Bool    // Contains impl
fn bounded?<T>(r: Range<T>) -> Bool
```

Plus the `Iter` protocol for ranges whose endpoint type implements `Discrete` — `Iter.known_count(r)` is `Some(n)` for bounded discrete ranges that can count steps, `None` for an unbounded one (the safe bounded count). Generic ops are `Iter` owner functions: `Iter.to_list(r)` (traps on unbounded, which is `Iter.infinite?`; bound with `Iter.take` first), `Iter.count(r)`.

Range operator precedence sits above pipes and below arithmetic — `1+2..5+6` groups as `(1+2)..(5+6)`. Pipe precedence sits above comparison, so `xs |> Iter.to_list() == [1, 2, 3]` groups as `(xs |> Iter.to_list()) == [1, 2, 3]`; range comparisons still group naturally as `(1..5) == (1..5)`. Range operands must have the same `Comparable` type.

## 33. Block Expressions

Braces `{ }` form block expressions. The last expression in a block is its value. This is consistent with function bodies, `case` branches, and `if/else` — all of which are already blocks.

Expressions are separated by newlines. Semicolons are supported but not required — the compiler inserts them at newlines (like Go). Explicit semicolons allow multiple expressions on one line: `{ try database.save(db, page); count + 1 }`. The formatter normalizes multi-expression blocks to use newlines.

```nomi
result = {
    x = compute()
    y = transform(x)
    x + y
}
```

Blocks work anywhere an expression is expected:

```nomi
// in a function argument
do_something(
    {
        a = parse(input)
        transform(a)
    }
)

// in a pipe
{
    config = load_config()
    build_query(config)
}
|> execute()
```

Bindings inside a block are scoped to that block — they don't leak into the enclosing scope. This holds for every block: an `if` or `else` arm, a `case` arm, a bare `{ }`, a lambda body, a `concurrent { }` body. A binding inside a block that shadows an outer name hides it until the block ends, and a read after the block reads the outer binding. So an inner binding nothing in its block reads is the unused-binding error (`binding 'total' is never read`), like any other.

A block whose last statement is a binding has the value `Unit`. A `_` binding may end a block whose value is not used.

Expressions, patterns and types nest at most 256 levels deep: blocks, parentheses, list, vector, set, map, tuple and struct literals, lambdas, `if` bodies, unary operators, interpolations and type arguments each add a level, and a chain of binary operators, `|>` stages or `else if` arms does not. Deeper nesting is a parse error at the first token past the limit (`nesting deeper than 256 levels`).

## 34. Struct Field Access

The `.` operator accesses struct fields. Returns the field's value directly.

```nomi
user = users.new("Alice", 30)

name = user.name // "Alice"
```

Chained access for nested structs:

```nomi
city = user.address.city
```

Tuple field access by index:

```nomi
pair = #(1, "hello")

x = pair.0 // 1

y = pair.1 // "hello"
```

Where a function is expected, `.name` with nothing before the dot is a function that reads the field (`users |> Iter.map(.name)`); see §8 Field Accessors.

`.` is also used for qualified names (`users.new`, `Iter.map`), but the compiler distinguishes between qualification and field access by context — a qualifier is resolved at compile time, field access is a runtime operation.

Struct fields are always snake_case — there is no per-field visibility. Field access is controlled by the struct's visibility.

## 35. Error Types

No built-in `Error` type or interface. `Result<T, E>` already communicates "E is the error" semantically — no additional marker needed.

`E` is unconstrained — any type works as an error:

```nomi
Result<User, String>       // quick and dirty
Result<User, UserError>    // custom error enum
Result<User, Int>          // even this works
```

For printable errors, implement `Display` on your error type:

```nomi
enum UserError {
    NotFound Int
    InvalidName String
}

impl Display for UserError {
    fn to_string(e: UserError) -> String {
        case e {
            .NotFound(id) -> "User not found: ${id}"
            .InvalidName(name) -> "Invalid name: ${name}"
        }
    }
}
```

`String` implements `Display`, so `io.print(e)` works when `E` is `String` too.

If error-specific behavior is needed later (wrapping, codes, stack traces), an `Error` interface can be introduced then.

## 36. Testing

Tests can live at file scope in any `.nomi` file, including files whose names end in
`_test.nomi`. `nomi test` accepts an optional file or directory path. A
file path runs tests in that `.nomi` file regardless of its name; a
directory path searches recursively for every `*_test.nomi` file plus
ordinary `.nomi` files that contain test declarations. Test declarations
are inert during ordinary `nomi run` loading and execute only under
`nomi test`.

`nomi test <file> --line N` runs the test whose declaration starts on line
`N` (or `N + 1`): a `test`, an attached `//!` test (any of its `//!` lines),
or a `group`, whose header line runs every test in the group. When
nothing starts there, `nomi test` runs nothing and exits non-zero with
`no test at <file>:<N>` and the nearest test's first line; with
`--format json` that error is a `"type":"file"` failure record.

`nomi test --watch [path]`, with any of the other flags, runs the tests,
then runs them again whenever a `.nomi` file under the path, or a file the
tests import, changes, as `nomi run --watch` does (§26): tests still running
are stopped, `<file> changed; running again` is printed on standard error,
and a file that fails to compile is reported as `nomi test` reports it. A
new test file under the path is picked up by the next run.

Each test may run for 10 seconds of wall-clock time, counting its group's
`clock`, `boot` and `setup` with its body; a `group` with
`clock Clock.Virtual` is held to real time too. `--timeout <duration>`
changes the limit, written as `30s`, `2m` or `500ms`, and `--timeout 0`
removes it. A test still running when its limit passes is stopped, tasks it
started included, and fails; the tests after it run as usual. Its failure
is at the line it was running in its own file, outside the standard
library, and lists where it was running, innermost call first, each as
`<path>:<line> in <function>` (a test body is named by its test):

```text
FAIL coins_test.nomi :: flips forever
  line 7: did not finish within 10s, so it was stopped
  it was running:
    coins_test.nomi:7 in flips
    coins_test.nomi:12 in flips forever
  help: `nomi test --timeout <duration>` sets how long a case may run (10s unless given), and `--timeout 0` removes the limit
```

A `subtests` table is one test, so its rows share the limit. `nomi run` has
no limit, and neither has a test run under the debugger (`nomi dap`), where
a paused test must not time out. Work inside a single call into Go (a Go
binding, say) cannot be stopped until the call returns.

What a test writes while it runs (`io.print`, `io.write`, `io.inspect` and
`dbg`) is reported under the test, not where it was written. Each test's
output is collected while it runs: from its group's `boot` and `setup`, its
body, the tasks it starts, and a `once` initializer it is the first to force.
The text report prints it under the test's `ok`, `FAIL` or `BLOCKED` line,
indented two spaces, for passing tests too; a failing test's output comes
before its failure block, and a test stopped early keeps what it wrote
before stopping. `dbg` lines keep their colour when the report is coloured.
Tests run one at a time, so outputs never interleave. Output inside an
`io.capture` belongs to the capture, as anywhere, except `dbg`, which still
goes to the test. Output a task writes after its test has finished goes to
stdout in text mode and to stderr in json mode.

```text
ok a_test.nomi :: passes
  dbg line 4: 5 = 5
  printed
FAIL a_test.nomi :: fails
  dbg line 10: 6 = 6
  line 11: assertion failed
    assert False
```

`nomi test --format json` reports as JSON Lines on stdout instead of text
(`--format text` is the default). Each test produces one record, and a
summary record comes last:

```json
{"type":"test","file":"/abs/path/a_test.nomi","line":14,"end_line":17,"name":"a_test.nomi :: fails","status":"failed","message":"  line 16: assertion failed\n    assert x == 4","error_line":16,"output":"dbg line 15: x = 3\n","report":"FAIL a_test.nomi :: fails\n  dbg line 15: x = 3\n  line 16: assertion failed\n    assert x == 4\n"}
{"type":"file","file":"/abs/path/b_test.nomi","name":"b_test.nomi","status":"failed","message":"...","report":"..."}
{"type":"test","file":"/abs/path/a_test.nomi","line":20,"end_line":22,"name":"a_test.nomi :: counts","status":"blocked","message":"a_test.nomi:9:20: this call to `skip_odd` is not supported yet, so `fn evens` cannot run","error_line":9}
{"type":"summary","passed":3,"failed":2,"blocked":1}
```

- `file` is absolute; `line` is the declaration's first line, the one
  `--line` matches, and `end_line` its last. Tools should locate a result by
  `file` and `line`: `name` is the label the text report prints, and an
  attached test's label embeds display wording and line numbers.
- `status` is `passed`, `failed` or `blocked`. `blocked` marks a test the
  VM could not run (see §25); its `message` is what the text report's
  `BLOCKED` lines give: for code the compiler could not lower, the error
  `nomi check` reports, as `<path>:<line>:<col>: <message>` with a
  `<path>:<line>:<col>: help: ...` line per hint, the path relative to the
  working directory when it is under it. Its `error_line` is that code's
  line when it is in the test's own file. The summary's `blocked` counts
  those tests.
- `message` is the block the text report prints under a failing test,
  without colour; it is empty for a passing test. `error_line` is the line
  the failure points at and is omitted when there is none.
- `output` is what the test wrote while it ran, without colour, and is
  omitted when it wrote nothing.
- `report` is what the text report prints for that record, trailing newline
  included: `ok <name>` for a passed test, the `FAIL` line and its block for
  a failure, the `BLOCKED` lines for a blocked test, each with the test's
  output indented under its line. It is coloured and its
  source lines highlighted when text mode would colour stdout, which
  `NOMI_COLOR=always` forces even though stdout is a pipe; otherwise it is
  plain. `message` never carries colour. An editor shows `report` in an
  output panel and uses `message` for diagnostics.
- A `"type":"file"` record is a failure that stopped a whole file from
  running, such as a type error: the text report's `FAIL <file>: <error>`
  line. It has no `line`.

In json mode stdout carries only records: a test's output is in its record.
The exit status is the same as in text mode.

The syntax is declaration-shaped:

```nomi
test "new user" {
    user = users.new("Alice", 30)
    assert user.name == "Alice"
}

group "defaults" {
    setup #{name: "No Name"}

    test "default user", #{name} {
        user = users.default()
        assert user.name == name
    }
}
```

`test "name" { ... }` is one executable test case. `group "name" { ... }`
groups tests. A `test` is declared at the top level of a file or in a
`group`, and a `group` at the top level. One inside a body (a function's, a
lambda's, a test's, an attached test's or a `once` initializer's) is the error
`` a `test` is declared at the top level of a file or in a `group`; it cannot be nested inside a body ``,
or `` a `group` is declared at the top level of a file; it cannot be nested inside a body ``. A group's `setup` produces a value, and a test receives it
through a pattern after the test's name (`test "default user", #{name}` above).
`group` is a keyword only at the top level and before the group's name
string; anywhere else it is an ordinary name, so `group = Iter.group_by(...)`, a
parameter or a field named `group` work as written. A top-level `group {`
is a group with its name missing,
`` a `group` needs a name; name the group for what its tests share, such as `group "dates" { ... }` ``.
A group's parts are described after attached tests, below.

A test must be able to fail, so its body holds at least one `assert`,
`refute` or `subtests` statement; a test without one is
`test block must contain at least one assert or refute`, and an attached
test without one is `attached test must contain at least one assert or refute`.

An attached test uses a docs-flavored `//!` prompt immediately before the
declaration it exercises. It is discovered and run by `nomi test` like an
ordinary test declaration, and the body is type-checked in the following
declaration's lexical scope. Its lines hold what a `test` block's body holds,
`subtests` included. Contiguous `//!` lines form one attached test; a
physical blank source line starts a separate attached test:

```nomi
//! assert Int.to_string(42) == "42"
//! assert Int.to_string(-7) == "-7"
//
host fn to_string(n: Int) -> String

//! assert answer() == 42
//
fn answer() -> Int {
    42
}
```

Put setup before ordinary `assert` / `refute` statements, and keep related
assertions in the same attached test.

A `group` holds at most one `clock` line, at most one `boot` line, at
most one `setup`, and its `test` blocks. Each part is optional, and the lines
may appear in any order, before, between or after the tests: they run in a
fixed order whatever their position (below). `nomi fmt` places them first, in
the order they run: `clock`, `boot`, `setup`, then the tests in source order.
Groups do not nest; a `group` inside a `group` is
`` a `group` cannot contain another `group`; write a sibling group instead ``.
A second `clock`, `boot` or `setup` is a parse error, such as
`` a `group` has at most one `setup` line ``.

The `boot` line calls an entry's boot (§27). The call is an ordinary call
checked against the boot's signature: `boot server.boot()` calls a boot that
takes no parameter or whose parameter has a default, and
`boot server.boot(startup)` passes a `Startup` to one that takes it. Most tests
need no startup input, so a boot that reads startup usually defaults its
parameter to `#Startup{}`. A test file reaches the boot by importing its entry
file:

```nomi
// server.nomi
import std/io

pub interface Mailer {
    fn send(mailer: self, to: String) -> Result<Unit, String>
}

pub type SmtpMailer

impl Mailer for SmtpMailer {
    fn send(_mailer: SmtpMailer, to: String) -> Result<Unit, String> {
        io.print("smtp: welcome ${to}")
        Ok(Unit)
    }
}

pub struct App {
    mailer: Mailer
    sender: String
}

pub fn boot(startup: Startup = #Startup{}) -> App {
    sender = Map.get(startup.env, "SENDER") |> Maybe.with_default("noreply")
    #App{mailer: SmtpMailer, sender}
}

pub fn welcome(to: String) -> Result<Unit, String> {
    Mailer.send(App.mailer, to)
}

fn main() {
    _ = welcome("ada@example.com")
}
```

```nomi
// test_support.nomi
pub fn startup() -> Startup {
    #Startup{env: %{"SENDER" => "ada"}}
}
```

```nomi
// server_test.nomi
import {
    server
    server.App
    server.Mailer
    test_support
}

type FakeMailer

impl Mailer for FakeMailer {
    fn send(_mailer: FakeMailer, to: String) -> Result<Unit, String> {
        if to == "" {
            Err("no address")
        } else {
            Ok(Unit)
        }
    }
}

group "welcome" {
    boot server.boot()

    setup {
        with App.mailer = FakeMailer
        "ada@example.com"
    }

    test "sends through the installed mailer", to {
        assert server.welcome(to)
    }

    test "falls back to the default sender" {
        assert App.sender == "noreply"
    }
}

group "configured sender" {
    boot server.boot(test_support.startup())

    test "reads the booted sender" {
        assert App.sender == "ada"
    }
}
```

A group does not define its own boot: a `fn boot` inside a `group` is
`` a `group` does not define `fn boot`; it names an entry's boot on a `boot` line, such as `boot server.boot(startup)` ``,
and a `boot` line that calls anything but an entry's boot is
`` `boot` in a `group` calls an entry's boot, the `fn boot` of a file that defines `fn main`, such as `boot server.boot(startup)`; `make_app` is not one ``.
A test outside a group, or in a group with no `boot` line, runs with no boot
and cannot read application fields.

`setup expr`, or `setup { ... }` with statements before its final value,
produces the value the group's tests receive. It may be any value; a setup
whose last line is a statement, such as a `with` line, produces `Unit`. One
whose last line is `assert x` or `refute x` produces what `ok = assert x`
binds, and a test binds a `Unit` setup value as any `Unit` binding does.
`return value` inside a setup ends the setup, not the test, with that value,
as it ends a function; the setup's type is the type its final value and every
`return` agree on. A test
binds the value with a pattern after its name: a name
(`test "loads rows", db`), a tuple (`test "pair", #(left, right)`), or a record
(`test "default user", #{name}`). A test with no pattern ignores the value. The
pattern must match every value of the setup's type; a refutable pattern such as
`Some(n)` or a literal is
`a test's pattern must match every setup value; bind it with a name or an irrefutable pattern and match it in the body`.
Setup-local bindings do not reach the tests; return what a test should see.

Each test starts fresh. Its clock is selected, the `boot` line's call runs and
its application value is published, then `setup` runs, then the test's body.
Nothing carries from one test to the next. A `with` line in `setup` stays in
force for the body of the test it serves, so `setup` is where a group installs
fakes. `defer` statements in boot and in setup run when that test finishes,
after its body and its owned work:

```nomi
group "store" {
    setup {
        db = try sqlite.open("tasks.tsv")
        defer sqlite.close(db)
        db
    }

    test "loads rows", db {
        assert Store.count(db) == 0
    }
}
```

A `with App.field = value` line in a test body replaces an application field
for the rest of that body; callees that read `App.field` see the replacement.

A `group` may declare its clock on a `clock` line: `clock Clock.Virtual`,
or `clock Clock.System` (the default), with `Clock` imported from
`std/testing`. Under a virtual clock, time moves only when every task in the
test is blocked, so a test that sleeps or waits on a deadline costs nothing and
takes the same path on every run, and work that can never finish is reported
as a deadlock rather than hanging. Waiting on something outside the program
(reading input, a real network call) never lets a virtual clock move, so it is
reported as a deadlock too.

Assertions are language forms, not functions:

```nomi
test "membership" {
    assert Set.contains?(roles, "admin")
    refute Set.contains?(roles, "banned")
}
```

`testing.check(expr)` accepts the same subjects as `assert` and returns
`Result<Subject, AssertionFailure>` instead of propagating failure. Bool
subjects return their successful shape (`True`); `Result<T, E>`,
`Maybe<T>`, and custom `Assertable` subjects return the original subject value
on success. An `Assertable` value succeeds when its `failure` function returns
`None` and fails with custom details when it returns `Some(AssertionDetails)`.

```nomi
import std/testing

passed = testing.check(user.name == "Alice")

failed = testing.check(user.name == "Grace")

assert passed == Ok(True)

refute failed
```

A test body, or a `group`'s `setup` block, whose final value is
`Err(AssertionFailure)` fails with that failure's report, so a body ending in
`testing.check(total == 10)` fails like one ending in `assert total == 10`.
The rule reaches through a final `if`, `case` or block to the arm that ran. A
body whose final statement is itself an assertion (`assert`, `refute`,
`assert pattern = value`) is judged by the assertion, not by its value.

`return` in a test body ends the case, as it ends a function: the body's
pending deferred calls run and the case passes. `return value` makes `value`
the body's final value, so `return testing.check(total == 10)` fails the case
when the check fails, and any other value is discarded.

A failed assertion's report lists the operands it compared under `values:`,
each with its value. A value reads structurally there: a String is quoted,
a struct's fields are sorted by name, and a type's `impl Display` is not
consulted. A value whose type has a hand-written `impl Debug` reads as that
impl renders it, at any depth, as `dbg` prints it; a derived or universal
`Debug` does not change the row. A literal operand has no row, since the
assertion line already shows it, and neither does a function named by its
own name (`main` would read `<func: main>`). A name bound to a function
keeps its row, which says which function it holds (`g = <func: double>`).

When `assert a == b` fails and both operands are Strings, at least one of
them holding a line break, the report shows a line diff in place of their
two rows. The left operand is the actual value and the right the expected
one, so write the value under test on the left. The diff's header names
the two sides. Each row is a marker column, `│`, then the line's text as it
is: `-` marks a line only the expected side has, `+` a line only the actual
side has, and a blank marker a line both have. When the expected side is a
`"""` literal written in the file, each of its lines also carries, left of
the marker, the line of the file it is written on; a line only the actual
side has carries no number. A changed empty line reads `(empty line)`.
More than three unchanged lines away from a change collapse to
`... N lines match ...`. A changed line ending in whitespace ends in `$` (a
trailing tab reads `\t`), a carriage return reads `\r`, and when only one
side ends in a newline the other's last line is followed by
`\ no newline at end`. Since the diff shows the expected literal's lines,
the assertion above it writes that literal collapsed: `""" lines 8-10 """`
names the lines of the file it spans, or `""" ... """` when its lines are
not numbered (an interpolated value holding a line break changed their
count). A literal with no diff after it is shown whole:

```text
  line 7: assertion failed
    assert output == """ lines 8-10 """
    expected """ ... """ (-) and actual output (+) differ

         8   │ balance?
         9 - │ Your balance: 500
        10 - │ bye
             │ \ no newline at end
           + │ Your balance is 500
           + │ bye  $
             │ ($ ends a line that ends in whitespace)
```

A failed `refute a == b` or `assert a != b` means the two were equal, so it
has no diff. On a terminal the `-` rows are red, the `+` rows green, and the
line numbers and `(empty line)` dim.

`assert` and `refute` can wrap pipelines. Put the keyword at the head so the
whole pipeline is the assertion subject:

```nomi
assert "Ada Lovelace"
    |> String.contains?("Ada")

refute "Grace Hopper"
    |> String.contains?("Ada")
```

`assert expr` and `refute expr` propagate `AssertionFailure` to the nearest
enclosing test or `Result`-returning function. Bool assertions return the
successful shape: `assert` expects and returns `True`, while `refute` expects
and returns `False`. Result assertions validate shape without extracting:
`assert result` requires `Ok(_)`, while `refute result` requires `Err(_)`.
Maybe assertions mirror the same shape: `assert maybe` requires `Some(_)`,
while `refute maybe` requires `None`. Custom `Assertable` subjects succeed by
returning their original subject value after `Assertable.failure(...)` returns
`None`. An assertion stands as a statement or as a binding's value (`ok = assert
x`), and is not an operand: `!assert x` passes when `x` holds and is then
`False`, so it is the error `` `assert` cannot be an operand of `!` ``, whose help
suggests `refute x` or `assert !x`. An assertion on either side of a binary
operator other than `|>` is the same error. Anywhere else a value is needed,
an assertion is the error `` `assert` cannot be used as a value here ``: as
another assertion's subject (`assert assert x`), a call's argument, a
collection element, inside parentheses (`ok = (assert x)`), a condition or
scrutinee, a returned value, or the head of a pipe (`(assert x) |> f()`; put
the keyword before the whole pipeline instead). A case arm's body may be an
assertion, as a statement may. Its value is the same wherever it stands:
`ok = { assert x }`, an `if` or `case` arm ending in `assert x` whose value
is bound, `ok: Bool = assert x` and `Some(n) = assert m else { ... }` all
bind what `ok = assert x` binds, and `_ = assert x` judges `x` and discards
it. A failure exits the nearest enclosing test or `Result`-returning
function as a statement assertion's does, so in `main` (which then returns
`Result<Unit, AssertionFailure>`) it ends the program with the report.

Assertions may also bind through a pattern. The pattern uses the same language
as ordinary destructuring and `case` arms. The right-hand side is evaluated
normally; the pattern must match, and a mismatch is reported as an assertion
failure:

```nomi
assert [left, right] = pair

assert Err(Error.InvalidFormat(reason)) = Date"not-a-date"
```

In tests, use `try` when a fallible value is setup for the rest of the test.
The test runner reports the `try` source and returned value if setup exits
early. Use a pattern assertion when the returned shape is itself part of the
test's claim, such as `assert Err(Error.InvalidFormat(_)) = Date.parse(text)`.
A `try` or an assertion in a `group`'s `setup` block behaves the same
way: the setup runs inside each case it serves, so an early exit there fails
that case with the same report, and none of the case's body runs.

`subtests rows, pattern { body }` runs one test's body over a table of rows.
`rows` is anything an `Iter` accepts (a `List`, `Vector`, `Set`, a `Map`,
whose rows are its `#(key, value)` pairs, a `Range` or an `Iter`), and the body
runs once per row, in order, with `pattern` bound to the row. The pattern
must match every row, as a test's setup pattern must; a refutable one is
`` a `subtests` pattern must match every row; bind it with a name or an irrefutable pattern and match it in the body ``,
and rows that are not iterable are
`` `subtests` runs its body once per element of what it is given, which must be something `Iter` accepts, such as a List, a Range or an Iter; got Int ``.

```nomi
test "determines palindromes" {
    cases = [#("abba", True), #("abc", False), #("x", True)]

    subtests cases, #(line, known_palindrome) {
        assert palindrome?(line) == known_palindrome
    }
}
```

A failed assertion in the body ends that row only, and the next row runs.
After the last row, when any row failed, the statement fails as one
assertion does, with a report that lists each failed row: the row rendered
as a `values:` row renders a value, then that row's own assertion report,
then the count. Rows that pass print nothing, and a test whose rows all pass
prints its one `ok` line. Were the second row above `#("abc", True)`, the test
would report:

```text
FAIL palindrome_test.nomi :: determines palindromes
  subtest #("abc", True): line 5: assertion failed
    assert palindrome?(line) == known_palindrome
    values:
      line
        = "abc"
      palindrome?(line)
        = False
      known_palindrome
        = True
  1 of 3 subtests failed
```

A row's final statement whose value is `Err(AssertionFailure)` fails the
row, as it fails a test body, so a row may end with a helper that asserts:

```nomi
fn check_row(line: String, known_palindrome: Bool) -> Result<Unit, AssertionFailure> {
    assert palindrome?(line) == known_palindrome
    Ok(Unit)
}

test "determines palindromes" {
    subtests [#("abba", True), #("abc", False)], #(line, known_palindrome) {
        check_row(line, known_palindrome)
    }
}
```

`subtests rows, pattern, name { body }` names each row. `name` is any
expression of type `String`, written after the pattern and a comma. It is
evaluated once per row, after the pattern binds and before the body runs, in
the scope of the pattern's bindings and the enclosing test's, so it may read
a column (`description`) or build a name from several
(`` "${a} + ${b}" ``). A binding that only the name reads counts as used. A
name of another type is
`` the name of a `subtests` row must be a String, got Int ``.

```nomi
test "leap years" {
    cases = [
        #("year divisible by 400 is leap year", 2000, True),
        #("year divisible by 100, not divisible by 400 in common year", 1900, False),
    ]

    subtests cases, #(description, year, expected), description {
        assert leap_year?(year) == expected
    }
}
```

A failed row's report shows its name, rendered as a String literal, where the
rendered row would be; the rest of the report is unchanged. Two rows may share
a name, and each failed one is listed under it. Were `leap_year?(2000)`
`False`, the test above would report:

```text
FAIL leap_test.nomi :: leap years
  subtest "year divisible by 400 is leap year": line 8: assertion failed
    assert leap_year?(year) == expected
    values:
      year
        = 2000
      leap_year?(year)
        = False
      expected
        = True
  1 of 2 subtests failed
```

`_` and a `_`-prefixed name discard a column with or without a name, so a
description column may still be left out of a pattern
(`subtests cases, #(_, year, expected) { ... }`). The name runs on its own,
before its row, so `return`, `break`, `continue`, `try` and an assertion
written directly in it are errors, such as
``` `try` cannot be used in the name of a `subtests` row: the name is evaluated on its own, before the row runs ```.
A name that traps fails its row, and the row's body does not run. That row is
reported under the rendered row, with the header
`its name trapped:` and the trap's text, without the trap's own `line N:`
when the trap is on the name's line, then the name as written:

```text
  subtest 0: line 12: its name trapped: division by zero
    "${10 / n}"
```

The name ends where the body's `{` begins. A struct literal starts with `#`,
so it stands in a name as anywhere else:
`subtests pairs, #(x, y), #Point{x, y} |> Point.describe { ... }`.

A `subtests` statement is its test's assertion, so its body must be able to
fail a row: one with no `assert` or `refute` of its own (outside a lambda or
a nested `fn`) whose final value is not a `Result<_, AssertionFailure>`, in
any arm of a final `if` or `case`, is
`` `subtests` body must contain an assert or refute, or end each row with a Result<_, AssertionFailure> ``. A trap in a row (a division by zero, a `case`
with no arm, a `todo`) is not caught: it fails the test as a trap anywhere in
it does, and the rows after it do not run.

`subtests` is a statement, and its value is `Unit`. It stands where a
statement assertion may, and its failure leaves the same way: it fails a test
body, and in a function returning `Result<_, AssertionFailure>` the function
returns `Err(failure)`, whose `keyword` is `"subtests"`, whose `reason` is the
count line (`2 of 3 subtests failed`), and whose `subtests` field lists each
failed row as a `SubtestFailure` with its `row` and its `failure`;
`AssertionFailure.format` renders the same report. Anywhere an assertion is
not allowed, `subtests` is the same error with `subtests` named in it: in a
function with any other result
(`` `subtests` can return AssertionFailure, but the enclosing function returns Int ``),
in a lambda, in a `once` initializer or a default. It cannot be written in a
`group`'s `setup`
(`` `subtests` cannot be used in a `setup`: a setup builds the value each test binds ``),
or inside another `subtests` body
(`` `subtests` cannot be written inside another `subtests` body: each row of the outer statement is one case ``).

A row is not a function, and every row runs to its end, so `return`, `break`,
`continue` and `try` written directly in the body are errors, such as
`` `return` cannot be used in a `subtests` body: a row is not a function, and each row runs to its end ``.
Inside a lambda or a nested `fn` in the body they leave that, as anywhere.

A test that uses `subtests` is still one test: it counts once in the totals,
and `--line`, an editor's test lens and the debugger treat it as one test. A
breakpoint in the body stops once per row, in a frame of its own.

`subtests` is a keyword only where a statement starts with it, an expression
starts on its line, and a comma follows that expression. Anywhere else it is
an ordinary name: `subtests = rows`, `subtests(2)` and a function named
`subtests` keep working.

That makes assertions usable in production code as runtime checks as well as in
tests. Inside `nomi test`, a failed assertion stops that test and reports the
failure; other discovered tests continue to run. `nomi test` exits nonzero if
any test file fails to load, any test case fails, or the VM cannot run a case
(it is reported `BLOCKED`; see §25). Test names must be unique
within a file by their full path, including enclosing test groups. A
`test` or `group` name that is empty or only whitespace is a compile error.
Assertion failures carry observed values for comparison operands, predicate-call
arguments, and pipeline stages where available, so ordinary Nomi expressions can
produce useful test reports without a separate assertion-helper API for each
operation.

### Capturing input and output

`io.capture(input, run)` calls `run`, a function of no arguments, with
`input` as its standard input, and answers an `io.Captured<T>`: `value` is
what `run` returned, `output` is everything it printed, and `transcript` is
the output with each line `run` read written in where it was read.

```nomi
import std/io

fn greet() -> Result<Unit, String> {
    case io.read_line() {
        Ok(name) -> io.print("hello, ${name}")
        Err(_) -> io.print("hello, stranger")
    }

    Ok(Unit)
}

test "greets by name" {
    run = io.capture("Ada\n", greet)
    assert run.value == Ok(Unit)
    assert run.output == "hello, Ada\n"
}
```

While `run` runs, `io.read_line` reads `input` one line at a time with the
line ending stripped, as it reads stdin (`\n` or `\r\n`; a last line with no
newline is still a line), and answers `Err("eof")` once the input is used up.
`io.print`, `io.write` and `io.inspect` append exactly what they would have
written to stdout to `output`. `dbg`, `io.read_file`, `io.write_file` and the
runtime's own diagnostics are not redirected. A program's `main` can be
passed as `run`, which is how a test checks what a program prints for an
input.

The redirection lasts for the call and follows its tasks: a task spawned
inside `run` reads and writes the same capture, and two tasks each running
their own `io.capture` at the same time never see each other's input or
output. An inner `io.capture` takes over for its own call, so what it prints
is in its `Captured` and not the outer one's. Once `io.capture` returns, its
capture is closed: a task that outlives the call (a supervised one) reads and
writes whatever was in force where `io.capture` was called. If `run` traps,
the trap propagates as it would without the capture and the output captured
so far is dropped. `io.capture` works outside tests too, in `nomi run` and in
`nomi build` executables.

A transcript is in the form an `io.replay` script takes. Every output line
is the line exactly as `run` wrote it, and a blank output line is blank. A
line read is four spaces, `>` and a space, followed by exactly the text
read. An output line that would read as input (see below) gets a backslash
before its `>`. Part of a line written before a read (a prompt with no
newline) is output: it ends its own transcript line, and the line read
follows on the next. After `io.write("age: ")`, a read of `36` gives
`age: ` and then `    > 36`; after `io.write("> ")`, a read of `north`
gives `> ` and then `    > north`. A read at the end of the input adds
nothing, and the prompt it answered stays output, on one line with
whatever is printed after it, as in `output`. An inner `io.capture`'s
reads are in its own transcript only.

`io.replay(script, run)` runs `run` against a script of an interactive
session and answers an `io.Replayed`, an `Assertable` that holds when the
transcript matches the script. The script's base column is the start of
each line of its value; for a `"""` script, which removes its baseline
(see the multi-line string rules), that is the closing delimiter's column
when no line sits left of it. Every line of the script follows one rule:

- A line indented past the base column, by spaces or tabs, whose text then
  starts with `> ` is input. What follows `> ` is what the next
  `io.read_line` answers, leading spaces included. `>` alone after the
  indentation (a `> ` whose trailing space was dropped) is an empty line.
- Every other line is output the program must print, exactly as written,
  indentation included. A blank line is a blank output line. `>` at the
  base column, `>>`, and `>` with no space after it are output.
- An indented output line whose text starts with `> ` would read as input,
  so it is written with a backslash before the `>`: the script line
  `    \> quoted` is the output `    > quoted`. The backslash is removed
  only there, after the indentation and before what would otherwise be
  input; one already there takes one more (`    \\> x` is the output
  `    \> x`). A backslash anywhere else is the character itself. A `"""`
  literal does not process escapes, so the script writes `\>` as it is.

Every script is accepted. Once the script's input lines are used up,
`io.read_line` answers `Err("eof")`.

```nomi
import std/io

fn main() {
    io.print("What is your name?")
    name = io.read_line() |> Result.with_default("")
    io.write("Hello, ${name}. Your age: ")
    age = io.read_line() |> Result.with_default("")
    io.print("${name} is ${age}")
}

test "asks name and age" {
    assert io.replay(
        """
        What is your name?
            > Ada
        Hello, Ada. Your age:
            > 36
        Ada is 36
        """,
        main,
    )
}
```

The prompt `Hello, Ada. Your age: ` is written without a newline, so it is
an output line of its own, and the input line follows it. A script with no
input is the output as written; indentation past the closing delimiter's
column is part of the output, even on every line:

```nomi
import std/io

fn main() {
    io.print("-10.0 C |   14.0 F")
    io.print(" -5.0 C |   23.0 F")
}

test "converts" {
    assert io.replay(
        """
        -10.0 C |   14.0 F
         -5.0 C |   23.0 F
        """,
        main,
    )
}
```

Replay compares the transcript and the script line by line, with each
line's trailing spaces, tabs and carriage return dropped and every line,
the last included, ended by a newline, so a `"""` script, which drops its
final newline, matches output that ends in one, and a prompt's trailing
space need not be written. A read writes its line into the transcript at
the indentation the script's input line has, so a transcript that matches
is the script line for line. `Replayed`'s `transcript` and `expected`
fields hold the two in that form, and `output` holds what `run` printed
byte for byte. An input line that `run` never read fails the replay, and
the reason says how many were left. A failed replay shows the same line
diff a failed `==` over multi-line Strings shows, with the script as
expected (`-`) and the transcript as actual (`+`), under the header
`io.replay: the transcript (-) and what main printed (+) differ`. Each
script line written in a `"""` literal carries its line in the file, and
the text column holds each line as the script writes it. The assertion
above the diff writes that literal collapsed, as `==`'s does:

```text
  line 12: the transcript differs from the script
    assert io.replay(""" lines 14-18 """, main)
    io.replay: the transcript (-) and what main printed (+) differ

        14   │ What is your name?
        15   │     > Ada
        16 - │ Hello, Ada. Your age?
           + │ Hello, Ada. Your age:
        17   │     > 36
        18   │ Ada is 36
```

What `run` returned is not checked; use `io.capture` for that.

An `Assertable` can ask for that diff itself: an `AssertionDetails` with
both `actual` and `expected` set, and different, shows a line diff of the
two in place of its `actual:` row.

A report that shows a line diff has no `values:` row for a String the diff
already shows: one equal to either side, line for line, with trailing
whitespace and final newlines set aside. So a failed replay prints no row
for its script, even one that interpolates a value, and when no row is
left the `values:` heading goes too.

`dbg expr` prints the expression's source location, source text, and Debug
rendering, then returns the original value. In a pipe, `dbg` follows the bare
unary keyword-stage rule from §6 and acts as a transparent stage. The LSP
reports every `dbg` as a warning, and `nomi build` refuses it, because it is
development-only instrumentation:

```nomi
total =
    items
    |> Iter.map(price)
    |> Iter.sum()
    |> dbg
```

## 37. Configuration

Configuration is ordinary Nomi code. There is no special config syntax, no implicit environment-variable mapping, and no required wrapper module. A package typically puts its payload struct in `app.nomi`; that struct carries runtime dependencies and config data, and exposes a `load` function that constructs the value returned directly by `fn boot`.

### Structure

```
app.nomi              // defines Config and Config.load
dev.nomi              // development wiring/defaults
prod.nomi             // production wiring/defaults
test.nomi             // test wiring/defaults
```

`Config` lives in `app.nomi` alongside `load`. Environment files may import `Config` back from the app file — the loader handles this cycle at the type level (see §14, "Cyclic imports between user files").

### Module organization options

The layout above keeps the payload struct co-located with its constructor. Larger projects often nest a pure-data `Settings` value inside the payload (`settings: Settings`) and let environment child files layer that value. An equally valid alternative is to factor shared types into a separate leaf file (`types.nomi`) that both `app.nomi` and the environment child files import — the cycle is avoided entirely at the cost of an extra file. Choose whichever reads more clearly for your project.

### Payload Struct

The `app` file defines the payload struct with defaults and a `load` function. Process environment variables are mapped explicitly from `startup.env` while constructing the payload:

```nomi
// app.nomi
import {
    prod
    test as test_env
}

pub struct Config {
    context: Context
    deployment: String = "dev"
    port: Int = 3000
    database_url: String = "postgres://localhost/myapp_dev"
    max_connections: Int = 5
}

impl Config {
    pub fn load(startup: Startup) -> Config {
        base = #Config{context: Context.root()}

        deployment = case Map.get(startup.env, "NOMI_ENV") {
            Some(value) -> value
            None -> "dev"
        }

        configured = case deployment {
            "prod" -> prod.configure(base)
            "test" -> test_env.configure(base)
            _ -> base
        }

        Struct.update(configured, #{
            deployment,
            database_url: case Map.get(startup.env, "DATABASE_URL") {
                Some(value) -> value
                None -> configured.database_url
            },
        })
    }
}
```

```nomi
// main.nomi
import app.Config

pub fn boot(startup: Startup) -> Config {
    Config.load(startup)
}

fn main() {
    ...
}
```

```nomi
// prod.nomi
import app.Config

pub fn configure(config: Config) -> Config {
    Struct.update(config, #{max_connections: 20})
}
```

```nomi
// test.nomi
import app.Config

pub fn configure(config: Config) -> Config {
    Struct.update(config, #{
        database_url: "postgres://localhost/myapp_test",
        max_connections: 2,
    })
}
```

### Design Principles

- **Typed** — app/config values are structs, not string keys. Compile-time type safety.
- **Centralized** — one file declares what the app needs. Single source of truth.
- **Validated upfront** — call `Config.load(startup)` from `fn boot`. Missing required values fail immediately, not 20 minutes into execution.
- **Layered** — environment-specific defaults in code, env vars override at deploy time.
- **Explicit** — process environment mapping is manual. Every `Map.get(startup.env, "VAR")` call is visible in the load function.
- **No magic env var** — `NOMI_ENV` selects the environment (like `MIX_ENV`, `NODE_ENV`, `RAILS_ENV`). This is the only "magic" env var.

### Testing

Tests use the same language mechanisms as application code. A `group`
runs the entry's boot on its `boot` line with a `Startup` the test builds,
replaces fields with `with Config.field = value` lines in `setup` or in a test
body, and uses `defer` for temporary databases, files, or servers that must
close when each test finishes:

```nomi
// main_test.nomi
import {
    app.Config
    main
}

group "limited app config" {
    boot main.boot(#Startup{env: %{"NOMI_ENV" => "test"}})

    test "handles connection limit" {
        assert Config.max_connections == 2
    }
}
```

## 38. Derive and Decorators

Nomi has no user-facing decorators. Structural derivation is its own
declaration, `derive Iface for Type` (§38.1, §13), and attached tests are
`//!` prompts (§36). An `@name` line is a parse error
(`` `@uses` is not supported: Nomi has no decorators ``), and `uses` is an
ordinary identifier. When the compiler lowers a
`derive` declaration it attaches an internal `derive` marker to the type, but
that marker is never written in source.

The attached-test grammar:

```
attached-test      = attached-test-line+
attached-test-line = "//!" [statement]
```

An attached test prompt sits above the declaration it exercises, and its
`//!` starts the line, after nothing but indentation. A `//!` after code on
its line is a parse error.

### 38.1 `derive` — Compile-time impl synthesis

> **Limitations.** A derive over a struct with an anonymous-struct field (e.g. `value: #{x: Int, y: Int}`) type-checks, but the synthesized body is not lowered, so a program that calls it is refused with a "not supported yet" error (§25). Interface-typed fields (existentials) are skipped at the `derive` site — the static check can't know the runtime concrete type, so missing inner impls surface as runtime "no impl found" rather than at the `derive` site. The Hashable mix is `*31 + h` (Java-string-style) — fine for in-process bucketing, but collisions are easy to construct (e.g. `Hashable.hash(#Point{x: 0, y: 31}) == Hashable.hash(#Point{x: 1, y: 0}) == 31`); upgrade to FNV-1a / SipHash if a real workload demands it.

> **`derive Debug` is redundant.** Debug is **universal and automatic** (§38.2) — the compiler auto-synthesizes a structural Debug impl, byte-identical to `derive Debug`, for every declared type that lacks one. So you never need to write `derive Debug`; the only reason to mention Debug in `impl Debug` is to *override* the default (e.g. a **structural** override on an opaque type, whose auto default is name-only `<opaque T>` — see Opaque types below). `derive Debug` remains valid (it's just the explicit form of what happens automatically); the examples in this section keep it for illustration.

> **Same-file constraint.** A `derive Iface for Type` declaration must sit in the **same file and scope as the type's declaration** — an `impl Iface for Type { ... }` block can't be derived, and a cross-file derive is rejected (structural derivation reads the type's fields/variants, which live with the declaration). The derivable protocols are `Equatable`, `Hashable`, `Comparable`, `Display`, `Debug`, `ToJson`, `FromJson`, and `Variants`.

A `derive Iface for Type` declaration instructs the compiler to synthesize that interface's impl based on the type's structure. Write one derive declaration per interface. The synthesized impls are byte-identical to hand-written ones:

```nomi
struct Point {
    x: Int
    y: Int
}

derive Equatable for Point
derive Hashable for Point
derive Debug for Point
```

is equivalent to writing the impls out by hand:

```nomi
struct Point {
    x: Int
    y: Int
}

impl Equatable for Point {
    fn equal?(a: Point, b: Point) -> Bool {
        a.x == b.x and a.y == b.y
    }
}

impl Hashable for Point {
    fn hash(p: Point) -> Int {
        Hashable.hash(p.x) * 31 + Hashable.hash(p.y)
    }
}

impl Debug for Point {
    fn inspect(p: Point) -> String {
        "#Point{x: ${Debug.inspect(p.x)}, y: ${Debug.inspect(p.y)}}"
    }
}

```

The user never writes the synthesized functions; they exist at compile time and dispatch like any other impl. Internally, derive synthesis (like universal-Debug synthesis) emits implementation records equivalent to `impl Iface for Type { ... }` blocks rather than rewriting the type's body; this is a compiler-internal lowering detail, invisible except in this equivalence and not subject to the source-form placement rule.

#### Derive args and scope

Imports cover the names the file's **visible text** writes — and a `derive Comparable for Point` declaration *names* `Comparable` in the source. So each derived interface resolves through ordinary scope: it counts as a use of the protocol name, go-to-def on it jumps to the interface declaration, and naming a protocol that isn't in scope is a compile error pointing at the line with the exact import to add (``` `Comparable` is not in scope — import `std/comparable.Comparable` ```). Derive options are visible text too: `derive ToJson for User with #ToJson.Options{rename_all: Json.Case.Camel}` keeps the owner imports for `ToJson` and `Json` live; importing `Json.{self, Case.Camel}` and writing `rename_all: Camel` keeps both `Json` and the imported variant live instead. Prelude-exported protocols are in scope automatically for user files; protocols that are not in the prelude, such as `ToJson` and `FromJson`, are imported explicitly. `Debug` is the one exemption: it is compiler-known universally (§38.2), so `derive Debug for Type` carries no scope requirement.

Names referenced only by the **synthesized** code resolve through the compiler-known route, never through the deriving file's imports: the synthesized impl headers, the `Ordering` type (named in the `compare` return *and* as the qualifier of the `Ordering.Less`/`Equal`/`Greater` value and pattern references the body emits — qualifying through the type is what keeps the variants out of the prelude), `True` / `False` in synthesized bodies, and the interface-qualified recursion calls (`Equatable.equal?(...)`, `Comparable.compare(...)`, …). When synthesis creates an ordinary module call internally, it carries its own synthetic support import; for example `derive FromJson` bodies use `Map.get` without requiring the source file to import `std/maps`. Derive is compiler machinery; its output resolves like compiler output. A file deriving a protocol therefore imports exactly the protocol names its `derive Iface for Type` declarations write — nothing extra for the synthesis internals. Hand-written `impl Display for T { ... }` blocks still require `Display` in scope.

#### v1 derivable protocols

| Protocol | Synthesis rule |
|----------|---------------|
| `Equatable` | Pairwise field equality with `==` short-circuiting on first false. For enums: variants must match before payload comparison. |
| `Hashable` | Left-fold mix `Hashable.hash(value.f0) * 31 + Hashable.hash(value.f1) * 31 + ...` over each field. For enums: variant tag (declaration index) is mixed in via the same `* 31 + h` chain ahead of the payload hashes. Collision-prone for small-int patterns; see callout. |
| `Comparable` | Lexicographic by field declaration order, short-circuiting on first non-equal. For enums: earlier-declared variant < later-declared, ties broken by payload comparison. |
| `Debug` | Structural string in literal-shape form. For struct `#Point{x: 3, y: 4}` → `"#Point{x: 3, y: 4}"`. For enum `Shape.Circle(5.0)` → `"Circle(5.0)"` (the variant, with no enum-name prefix). |
| `Display` | Same structural shape as `Debug`, but nested `String` values render **unquoted** — `#C{name: bob}` where Debug emits `#C{name: "bob"}`. A convenience default; authors usually hand-write a `Display.to_string` function for real user-facing presentation (see below). |
| `ToJson` | Struct fields encode as JSON object keys; distinct wrappers encode through their inner value; enums encode as strings for bare variants and externally-tagged objects for payload variants. `#ToJson.Options{rename_all: ...}` controls generated field keys with a `Json.Case` variant. |
| `FromJson` | Struct fields decode from JSON objects with missing `Maybe<T>` fields as `None` and missing defaulted fields from their declaration defaults; distinct wrappers decode through their inner value; enums decode what derived `ToJson` writes: a bare variant from the string of its name, a payload variant from an object whose key is its name (an array for a tuple payload, an object of the fields for a struct variant), with an error naming the variant and field that did not match. Callers name the target with the implementing type (`User.from_json(value)`), an explicit target type argument on the interface (`FromJson.from_json<User>(value)`), or an expected target type (`user: User = try FromJson.from_json(value)`). `#FromJson.Options{rename_all: ...}` controls expected field keys with a `Json.Case` variant. |
| `Variants` | Enums only, and only an enum without type parameters whose variants all carry no data. `variants()` takes no argument and returns a `List` of every variant in declaration order, the order derived `Comparable` ranks them in: `derive Variants for Coin` gives `Coin.variants() == [Coin.Penny, Coin.Nickel, Coin.Dime, Coin.Quarter]`. Generic code calls it through a bound (`T.variants()` with `where T: Variants`). Deriving it for a struct, a distinct type, a generic enum, or an enum with a variant that carries data is a compile error at the derive that names the reason (`` `derive Variants for Shape`: variant `Shape.Circle` carries data, and `Variants` lists only variants that carry none; ... ``). |

`Display` **is** derivable — `derive Display` synthesizes the same structural shape as `derive Debug`, except nested strings render unquoted (`#C{name: bob}` where Debug emits `#C{name: "bob"}`). It's a convenience for throwaway / structural output and for generic code that just needs *some* `Display` impl to satisfy a bound. Real user-facing presentation usually wants a per-type opinion rather than the structural dump, so authors typically hand-write a `Display.to_string` function instead — the same split Rust draws between `Debug` (`#[derive]`-friendly) and `Display` (hand-written), except Nomi *also* lets you derive `Display` when the structural form is good enough.

#### Field-type requirements

`derive Iface for Type` where the type's fields don't themselves implement `Iface` is a compile error at the derive site (not deep in synthesized code). E.g., `derive Equatable for Wrapper` on a `struct Wrapper` with `id: User` requires `User: Equatable`.

#### Generic types

A derive on a generic type synthesizes the needed interface bound implicitly:

```nomi
struct Box<T> {
    value: T
}

derive Debug for Box<T>
```

synthesizes `impl Debug for Box<T> { fn inspect(b: Box<T>) -> String { ... } }`. For most protocols the implied `T: Iface` bound is real (calling errors at the call site if it isn't satisfied) — but **`T: Debug` is vacuous**: Debug is universal (§38.2), so every `T` satisfies it and `Debug.inspect(box)` always type-checks. (For `Equatable`/`Hashable`/`Comparable`/`Display` derives the implied bound on `T` is still enforced.)

#### Distinct types

`derive Iface for Type` on a distinct type delegates to the inner type:

```nomi
type Id Int

derive Hashable for Id
```

produces `Hashable.hash(Id(n))` = `Hashable.hash(n)`.

#### Opaque types

The `derive` entry is permitted on `pub opaque` types. Opacity describes the *construction surface* the file hides; an author writing `derive Debug` on an opaque type is opting in to exposing structural output through Debug, which is part of the file's curated API. The `Equatable`/`Hashable`/`Comparable` derives don't leak structure (their outputs are `Bool` / `Int` / `Ordering`); `Debug` does — by the author's choice.

For opaque types this is the load-bearing case of universal Debug's opaque rule (§38.2): the *auto* Debug default for an opaque type is **name-only** (`<opaque T>`) precisely so the universal floor never leaks hidden internals. Writing `derive Debug` (or a hand-written `Debug.inspect` function) on an opaque type is therefore a deliberate **structural override** of that name-only default — the explicit form *is* meaningful here, unlike on a non-opaque type where it's redundant. (This is why stdlib keeps `derive Debug` on `int.NonZeroInt` / `int.PositiveInt`: dropping it would regress `NonZeroInt(5)` to `<opaque NonZeroInt>`.)

Spec note: `derive Debug` on a type with sensitive fields (tokens, passwords, credentials) leaks those fields through Debug output. Hand-write a `Debug.inspect` function and redact rather than relying on the derive.

#### Host types

The `derive` entry on a declared `host type` is the author's **assertion that the type has trivial structure** — a zero-sized singleton with exactly one inhabitant (the `True`/`False` shape). The declaration carries no payload shape the synthesizer can see (internals are host-side), so the synthesized bodies are constants: `Equatable.equal?` returns `True`, `Comparable.compare` returns `Equal`, and `Hashable.hash` returns `0` (all values equal ⇒ equal hashes, so the equals/hash agreement law holds by construction). `Debug` / `Display` derives render the bare type name — the same rendering as universal Debug's host-type default (§38.2), so `derive Debug` on a host type is the explicit spelling of what happens automatically. There are no field-type requirements to check — the assertion is vacuously consistent. A **value-carrying** host type (the `Instant` class of types) would get semantically wrong impls from these constant bodies — that is the rule, not a gap: value-carrying host types hand-write host-backed impls (`host fn` items satisfying `impl` entries), as every such stdlib type does.

#### Manual + derive collision

Combining a `derive Iface for Type` declaration and a manual `impl Iface for Type { ... }` block for the same type is a compile error. This catches the bug where the user added a manual impl but forgot to remove the corresponding derive. Mitigate by removing one of the two.

#### Customization

v1 ships only the per-entry derive. Per-field exclude/rename, per-interface options, and user-definable derives (compile-time macros) are explicitly out of scope. Adding any of these later is purely additive.

### 38.2 `Display` and `Debug` as separate stdlib protocols

Two separate interfaces with distinct function names — `Display` with `to_string`, `Debug` with `inspect` — but **opposite default policies**: Debug is the universal floor (always available), Display is the curated, opt-in human form.

- **`Display`** — user-facing presentation, **opt-in**. Format is a UI choice, so authors usually hand-write per type. Used by string interpolation `${value}` and by `io.print` (both dispatch through `Display.to_string`). Derivable via `derive Display` (§38.1) — the synthesized form is the structural shape with strings unquoted, a convenience rather than a substitute for a hand-written presentation impl. A type with no Display impl errors at the dispatch site.
- **`Debug`** — structural representation, **universal and automatic**. Every value is `Debug.inspect`-able with no derive or hand-written impl required — the compiler eagerly auto-synthesizes a structural impl (compile-time, no reflection; byte-identical to `derive Debug`) for every declared type that doesn't supply one. So `io.inspect(x)` never errors for a missing impl, and there is no `where T: Debug` bound to write (the checker treats Debug as satisfied by every type, so generics and interface existentials like `Fragment<Display>` inspect freely). Used by explicit `Debug.inspect(value)` calls and by `io.inspect`.

**Precedence** (highest first): an explicit hand-written `impl Debug` > explicit `derive Debug` > auto-synthesized structural impl. Auto-synthesis fires only for types lacking *any* explicit Debug, so an override always wins.

**Opaque rule.** The auto Debug default for an `opaque struct/enum/type` is **name-only** (`<opaque T>`), never structural — a structural default would leak exactly the internals `opaque` exists to hide. The owning file opts into richer output by writing a `Debug.inspect` function or `derive Debug` (a deliberate structural override of the name-only default; see §38.1 Opaque types).

**Host type rule.** A declared `host type` also gets a name-only auto default, rendered as the **bare type name** (`True`, not an `<...>`-marked form) — the declaration carries no payload shape the synthesizer can see (internals are host-side), which is the same shape as a zero-sized distinct, and those inspect as their bare name (`type Expired` → `Expired`). The bare name is what makes `embeds`-variant delegation come out right: an enum embedding a zero-sized host singleton (`Bool`'s `embeds True`) inspects as the variant name, and a bare singleton value bound out of an `embeds` pattern inspects the same way. A sibling `impl Debug for T` overrides the synthesized default, like anywhere else (`Int`, `Decimal`, `Dynamic`, …). The rule is the same for a host type whose values are opaque Go handles (std's `Regex`, a user's `go`-bound or embedder-provided `host type`): its Debug is its own `impl Debug` when it declares one, at the top level and nested in a struct, payload, tuple or collection, and otherwise its bare type name. `Regex` declares one that renders the typed literal that builds it (`` Regex`\d+` ``, or `` Regex"a`b" `` when the pattern holds a backtick); a handle type that declares none inspects as its bare name.

**Non-structural value kinds.** Value kinds with no declaration to render — function/builtin values, `Task`, a channel half, `Context`, a lazy `Iter` — get deterministic Debug placeholders (`<function>`, `<task>`, `<channel>`, `<context>`, `<iter>`); `Unit` is a declared `host type` and inspects as its bare name, `Unit`; Display on them still errors. (Tuples and anon structs render structurally via language intrinsics.) See §16's "Non-structural value kinds" table.

**Same-named types.** Dispatch is keyed by a type's nominal identity (declaring file, name), so two types that share a name — a user `enum Direction` and `std/comparable.Direction`, or a `Point` in each of two files — each keep their own Debug impl, like any other impl.

The two export *different* function names — `Display.to_string` and `Debug.inspect` — so a value implementing both is called unambiguously either way: `Point.to_string(p)` for the Display form, `Point.inspect(p)` for the Debug form (type- or interface-qualified). This is why neither needs disambiguation despite being the most widely-implemented pair.

This is the Rust split (`std::fmt::Display` vs `std::fmt::Debug`) ported to Nomi's call conventions — except Nomi makes Debug *universal* (Go `%v` / Python `repr` / Java `toString` ergonomics) via compile-time auto-derive rather than runtime reflection. Stdlib ships hand-written `impl Debug` impls for primitives and containers (`Int`, `Float`, `String`, `List`, `Vector`, `Map`, `Set`, `Range`, …); `Bool` rides the auto path like any other non-customizing type (the auto enum impl renders its `embeds` variants as the bare variant name, coinciding with Display). For `Int`/`Float`/`Bool` the format coincides with Display since the literal source matches the user-facing form, while `Debug.inspect("hello")` returns `"hello"` (quoted, escaped) where `Display.to_string("hello")` returns `hello` (bare).

### 38.3 Derive interaction with visibility

Structural derivation introduces no visibility of its own. A `derive Iface for Type` declaration produces an impl whose visibility is inherited from `Iface`, identical to a hand-written conformance for that interface (per §3 / §13).

(Interface impls themselves are `impl Iface for Type { ... }` blocks; their visibility rule lives in §13.)

### 38.4 Derive targets — summary

Attached test prompts may attach to declarations and contribute runnable test
cases. Structural derivation is requested with the `derive Iface for Type`
declaration (§38.1 / §13): it targets `struct`, `enum`, distinct `type`, and
`host type` declarations.

## 39. Calendar (Dates and Times)

`std/calendar` covers the civil date-time surface — wall-clock readings,
zoned moments, the conversions between them, and DST-aware arithmetic.
Together with `std/instant` (absolute-instant clock and the `Instant`
type) and `std/duration` (exact elapsed-time `Duration`), it's the
full date/time story.

### The type ladder

Five civil types in `std/calendar`, in increasing specificity:

- **`Date`** — calendar day. Year/month/day. No time-of-day.
- **`Time`** — time of day. Hour/minute/second/nanosecond. No calendar.
- **`NaiveDateTime`** — wall-clock reading. Date + time of day, no
  zone. **Not a real moment in history.**
- **`OffsetDateTime`** — anchored at a fixed UTC offset. A real moment;
  no DST.
- **`DateTime`** — anchored in an IANA zone (`America/New_York`,
  `Asia/Tokyo`, …). Fully DST-aware. **The default for real moments.**

Plus the two adjacent types in sibling modules:

- **`Instant`** (`std/instant`) — pure UTC nanos since Unix epoch.
  No calendar, no zone, no display ceremony. `Instant + Duration` and
  `Instant - Duration` are Instants; `a - b` on two Instants is the
  `Duration` from `b` to `a` (`Instant.between(b, a)`).
- **`Duration`** (`std/duration`) — exact elapsed time, in
  nanoseconds at the storage layer. Construct with unit functions such as
  `Duration.seconds(5)`; hours/minutes/seconds exist here, while
  months/years do not (those aren't exact units).

The five-type ladder mirrors `java.time` (`LocalDate`, `LocalTime`,
`LocalDateTime`, `OffsetDateTime`, `ZonedDateTime`), but the naming
**reverses the conventional pattern**: the bare attractive name
`DateTime` is the IANA-zoned safe default, and `NaiveDateTime`
carries the "Naive" prefix as a footgun-marker. The verbose name
attaches to the unsafe shape so callers have to opt into it by
spelling it out — the opposite of "Local" or "Zoned" prefixes that
make the unsafe form the easier reach.

### Construction

Every type has a same-named `Literal` handler — the
typed-literal form is the primary construction path. Parse failure
yields `Err`:

```nomi
import {
    std/calendar.{Date, DateTime, Error}
    std/io
}

fn main() -> Result<Unit, Error> {
    d = try Date"2026-05-04"
    dt = try DateTime"2026-06-15T12:00:00-04:00[America/New_York]"
    io.print(d) // 2026-05-04
    io.print(dt) // 2026-06-15T12:00:00-04:00[America/New_York]
    Ok(Unit)
}

```

`DateTime`'s typed literal uses **RFC 9557 bracket notation** — the
IANA zone bracket is required. A bare offset (no bracket) parses via
`OffsetDateTime"…"` instead. The bracket distinguishes "anchored in a
zone" from "anchored at a fixed offset."

Programmatic constructors:

- `Date.new(year, month, day): Result<Date, Error>` — validated.
- `Time.new(hour, minute, second?, nanosecond?): Result<Time, Error>`
- `NaiveDateTime.new(year, month, day, hour, minute, second?, nanosecond?): Result<NaiveDateTime, Error>`
- `DateTime.in_zone(naive, zone, mode?): Result<DateTime, Error>` — wall
  reading → instant, projected through the zone. See "DST" below.
- `OffsetDateTime.with_offset(naive, offset): Result<OffsetDateTime, Error>`
- `OffsetDateTime.from_instant(instant, offset): OffsetDateTime` — total.
- `DateTime.from_instant_in(instant, zone): Result<DateTime, Error>` —
  total once the zone is known (fails only on `UnknownZone`).
- `DateTime.now_in(zone): Result<DateTime, Error>` — system clock
  projected into a zone.

### Arithmetic: `Duration` vs civil periods

Two intents. The split exists because
**`+ Hours(24)` is not always `+ Duration.hours(24)`** around DST
transitions.

- **`value + Duration` / `value - Duration`** — exact-elapsed-time arithmetic.
  Adding or subtracting 24 real hours shifts the *instant* by 24 hours of physical
  time, wherever the wall clock lands. Time-bearing types implement `Add`
  and `Subtract` with `Duration` as the right-hand operand and the same
  time-bearing type as the result, for example
  `impl Add<Duration, DateTime> for DateTime`.
- **`value + Years(...)` / `value - Years(...)` / `Months(...)` / `Weeks(...)` /
  `Days(...)` / `Hours(...)` / `Minutes(...)` /
  `Seconds(...)` / `Milliseconds(...)` /
  `Microseconds(...)` / `Nanoseconds(...)`** — civil period
  arithmetic. Adjusts the wall reading and, for zoned `DateTime`, re-resolves
  through the zone. Months and years clamp end-of-month
  (`Date"2026-01-31" + Months(1)` → `Date"2026-02-28"`).

The headline example, where civil and physical diverge across a
spring-forward transition:

```nomi
start = try DateTime"2026-03-07T12:00:00-05:00[America/New_York]"

start + Hours(24) // → noon Mar 8 EDT (23h physical)

start + Duration.hours(24) // → 1pm  Mar 8 EDT (24h physical)
```

The wall reading "tomorrow at noon" stays at noon after the
spring-forward jump — only 23 hours of real time elapse. "Add 24 real
hours" advances *through* the transition and lands at 1pm. Both are
correct; the split lets the caller spell which intent applies. See
`tests/05-calendar-and-time/dst_spring_forward_test.nomi` for the
runnable demo.

### DST: gaps, folds, and `Disambiguation`

Projecting a wall reading into an IANA zone (`in_zone`) can hit one
of two anomalies:

- **Spring-forward gap** — the wall reading **never exists**.
  `2026-03-08T02:30` in New York is in the gap (02:00 EST jumps
  straight to 03:00 EDT; the 30-minute mark in between is unreachable).
- **Fall-back fold** — the wall reading **exists twice**.
  `2026-11-01T01:30` in New York happens twice (02:00 EDT rolls back
  to 01:00 EST; 01:30 occurs once before the rollback and once after).

The `Disambiguation` enum controls how `in_zone` resolves these:

| Mode | In a fold | In a gap |
|---|---|---|
| `Compatible` (default) | Earlier instant (pre-transition) | Post-transition instant |
| `Earlier` | Earlier instant | Pre-transition instant |
| `Later` | Later instant | Post-transition instant |
| `Reject` | `Err(Ambiguous ...)` | `Err(Nonexistent ...)` |

`Compatible` is the default — total, deterministic, and matches the
naive intuition that "30 minutes after the gap started" means
"30 minutes of wall time after the gap's start." `Reject` is the
strict mode for when the wall reading came from user input (a form,
an API payload) and the right answer is to bounce the ambiguity back
to the user rather than silently disambiguate.

civil shifts (`+ Years(...)` / `+ Months(...)` /
`+ Hours(...)` / …) always use `Compatible` internally so the
operation is total. Only `DateTime.in_zone(..., Disambiguation.Reject)` surfaces
gap/fold failures.

### The conversion lattice — missing rows are the feature

```
                                Date  ──────┐
                                            │
                                Time  ──────┤  of / at_midnight
                                            ▼
Instant  ◄─────────────►  OffsetDateTime ◄────►  NaiveDateTime
   │                            ▲                    │
   │                            │ to_offset          │ in_zone
   │                            │ (lossy)            │ (with mode)
   ▼                            │                    ▼
Duration                        └──────  DateTime  ◄─┘
                                            │
                                            └─ with_zone
                                               (same instant,
                                                different zone)
```

Three "obvious" conversions are **intentionally missing**:

1. **`NaiveDateTime → Instant`** — a wall reading isn't a moment. The
   only way to project a naive into an instant is to commit to a zone
   via `in_zone(naive, zone, mode)` or to an offset via
   `with_offset(naive, offset)`. The missing arrow is the safety
   feature: it forces the zone choice into the calling code where the
   author has the context to make it.
2. **`OffsetDateTime → DateTime`** — a fixed offset doesn't determine
   an IANA zone (offset `-05:00` is in effect in many zones at various
   points in history). Going from an offset to a zone requires the
   caller to *name* the zone explicitly; we don't sugar it because the
   implicit zone choice would be wrong half the time. The path is
   `OffsetDateTime → Instant → from_instant_in(zone)`.
3. **`Date / Time → Instant`** directly — same story as `NaiveDateTime
   → Instant`: a calendar day or a time-of-day isn't a moment without
   a zone. `at(date, time)` produces a `NaiveDateTime`, then
   `in_zone` carries it the rest of the way.

These aren't oversights. They're the API's way of saying "you can't
turn a wall reading into a moment without naming a zone" — the most
common date-time bug class, encoded in the type system.

### Equality and ordering

`OffsetDateTime` and `DateTime` use **instant-only** equality and
ordering. Two values with the same underlying nanos but different
offsets / zones compare equal:

```nomi
ny = try DateTime"2026-06-15T12:00:00-04:00[America/New_York]"

paris = try DateTime"2026-06-15T18:00:00+02:00[Europe/Paris]"

ny == paris // → True (same instant)
```

The offset / zone is **display metadata, not identity**. If you
specifically need "same instant AND same zone," compare the rendered
forms too, since a `DateTime` displays with its zone bracket:

```nomi
a == b and Display.to_string(a) == Display.to_string(b)
```

See §30 Equality and Ordering Semantics for the operator-dispatch rule that makes
`==` route through an `Equatable` impl for these types — primitives
keep the direct-comparison path, struct types with an explicit
`Equatable` impl go through dispatch so custom semantics like this
reach the operator.

`Date` / `Time` / `NaiveDateTime` use structural equality (every
field equal) and chronological `Comparable` by field order.

### Typed literals — the canonical construction form

Every calendar type carries a `Literal` handler,
so the literal form is the primary user-facing constructor:

```
Date"YYYY-MM-DD"
Time"HH:MM:SS[.fff…]"
NaiveDateTime"YYYY-MM-DDTHH:MM:SS[.fff…]"
OffsetDateTime"YYYY-MM-DDTHH:MM:SS[.fff…]±HH:MM"  // or trailing Z
DateTime"YYYY-MM-DDTHH:MM:SS[.fff…]±HH:MM[Zone/Name]"
```

The bracket on `DateTime` is **required** — bare-offset forms without
the bracket parse via `OffsetDateTime` instead. A double-quoted literal's
parse failure returns `Err(calendar.Error)`; `try`-unwrap at the call
site. A backtick literal (`` Date`2026-05-04` ``) is parsed at compile
time: it is a `Date`, and a malformed one is a compile error. See §16
Typed Literals for the underlying mechanism.

A calendar value writes itself for a reader in a locale with `format` (a
style) and `format_fields` (a skeleton, `` DateFields`yMMMd` ``), reads what
a reader typed with `parse_in`, and writes and reads a fixed numeric
pattern, the same in every locale, with `to_string_with` and `parse_with`
(`` DatePattern`yyyyMMdd` ``); §41 *Dates and times* gives them.

### Embedded IANA tzdata

`std/calendar` embeds Go's `time/tzdata` package (≈ 450 KB) so IANA
zone lookups (`America/New_York`, `Asia/Tokyo`, …) work on every host
without requiring system-provided tzdata. Programs ship with their
own zone database; behavior is deterministic across operating
systems and minimal Docker images.

## 40. Random Generation

`std/random` provides pure, reproducible pseudo-random generation. There is no stateful global RNG and no mutation:
randomness is modeled as an explicit value threaded by hand or composed with
combinators. Same seed in → same value out, which makes every draw testable
and replayable.

### The model

Two opaque types:

- **`random.Seed`** — opaque PRNG state (a splitmix64 register). Threaded
  explicitly; never mutated.
- **`random.Generator<T>`** — an immutable *recipe* for producing a `T`. It is
  not a value; it describes how to produce one from a seed.

One runner:

- **`Generator.step(gen, seed): #(T, random.Seed)`** — runs a generator against a
  seed, returning the value paired with the *next* seed (which you thread
  into the following draw). Generating is a pure expression.

```nomi
seed = Seed.from_int(42)

die = Generator.int(1, 6)

#(roll, seed) = die |> Generator.step(seed)

#(next, _) = die |> Generator.step(seed) // a fresh seed → a fresh draw
```

### Seeding

- **`Seed.from_int(n): random.Seed`** — a reproducible seed. The same `n` yields
  the same sequence, forever (splitmix64 is fully specified, so this holds
  across platforms and releases).
- **`Seed.from_os(): Result<random.Seed, random.Error>`** — a non-deterministic seed from
  OS entropy. This is the module's **one impurity** — the entry point when
  you want real randomness. A program built from `from_os()` is no longer
  reproducible (and so cannot be doctested); reach for `from_int` in tests.

### Constructors

`Generator.int(from, to)` (inclusive) and
`Generator.float(from, to)` (half-open `[from, to)`) return the generator
itself. The caller chooses the bounds, so a range with nothing in it is a
broken promise and traps (§17): `Generator.int(6, 1)` stops the program with
`Generator.int: from 6 is greater than to 1`, and `Generator.float` traps
unless both bounds are finite and `from < to` (a NaN bound included). Compare
the bounds first when they come from data. Every range that passes draws
uniformly, the whole of `Int` and a Float range wider than a Float can span
included. `Generator.bool()`, `Generator.constant(value)`, and
`Generator.uniform(first, rest)` cannot fail.
`Generator.weighted(first, rest)` returns
`Result<random.Generator<T>, random.Error>` because weights are often data:
they must be non-negative and at least one must be positive; they need not sum
to 1.

### Combinators and collections

- **`Generator.map(gen, f)`** — reshape the produced value.
- **`Generator.flat_map(gen, f)`** — the bind operation: sequence a
  *dependent* generator (the next draw depends on the previous value), and
  the way to build a composite `random.Generator` value.
- **`Generator.list(gen, length)`** — a list of `length` independent draws.

```nomi
seed = Seed.from_int(7)

die = Generator.int(1, 6)

#(hand, _) =
    die
    |> Generator.list(5)
    |> Generator.step(seed) // → [4, 1, 1, 4, 5], reproducibly
```

When several draws are known up front, compose them into one generator and
`step` once; thread the `random.Seed` by hand only when draws are interleaved with
other work (e.g. across turns of an interactive loop). The applicative
`mapN` family (`map2`/`map3`/…) is intentionally absent — Nomi's exposed
`step` makes hand-threading the natural move, and `flat_map` + `map` express
any composite generator when one is needed.

## 41. Locales and Plural Rules

`std/locale` provides `Locale`, and `std/plural` provides CLDR's plural
rules. `Int`, `Float`, `Decimal` and `String` format and parse numbers and
join lists for a locale, `std/currency` and `std/money` provide `Currency`
and `Money`, the calendar types and `Duration` format and parse dates,
times and lengths of time, `std/localize` holds the `Localize` interface
and the parse error, and `std/message` holds the `Message` a `message`
declaration returns (*Messages* below). All the locale data comes from one pinned CLDR release,
48.2, and a program carries the locales its `nomi.toml` lists.

### `Locale`

A `Locale` is a canonical [BCP 47](https://www.rfc-editor.org/rfc/rfc5646)
language tag, Unicode extensions included (`en-US-u-nu-latn`). It is an opaque
type: values come from parsing, and two spellings of one locale are equal.
`Locale` is `Equatable` and `Hashable`, so it is a map key, and it is not
`Comparable`. `Display` gives the tag; `Debug` gives the literal.

The functions are owner functions of `Locale` (`Locale.parse(tag)`):

```nomi
fn parse(tag: String) -> Result<Locale, Error>
fn from_posix(value: String) -> Result<Locale, Error>
fn language(locale: Locale) -> String
fn script(locale: Locale) -> Maybe<String>
fn region(locale: Locale) -> Maybe<String>
fn fallbacks(locale: Locale) -> List<Locale>
fn negotiate(requested: List<Locale>, available: List<Locale>, default: Locale) -> Locale
```

- **Parsing canonicalizes.** Case is normalized (`EN-us` is `en-US`,
  `zh-hant-hk` is `zh-Hant-HK`) and deprecated subtags are replaced (`iw` is
  `he`, `in` is `id`, `sh` is `sr-Latn`). A failure is a `locale.Error`:
  `Empty`; `Underscore {suggestion}` for a POSIX spelling such as `pt_BR`,
  whose suggestion is `pt-BR`; `Malformed {text}` for text that is not a
  well-formed tag; `UnknownSubtag {subtag}` for a well-formed tag with an
  unregistered subtag (`xx`).
- **The typed literal.** `` Locale`pt-BR` `` is a `Locale`. Its handler is
  `Locale.parse`, so by the raw-literal rule (§16 *Typed literals*) a
  malformed tag is a compile error at the literal, worded by `locale.Error`'s
  `Display`:
  ``typed literal Locale`pt_BR` is invalid: a locale tag separates subtags with `-`, not `_`: write `pt-BR` (Locale.from_posix reads a POSIX value)``.
  In a program that lists its locales (`[i18n].locales` and
  `partial_locales`), a literal whose locale's fallback chain reaches none
  of them is a compile error too, since the program would never format in
  it: with `en` and `de` listed,
  ``typed literal Locale`fr`: `fr` is not one of this program's locales (`en`, `de` in nomi.toml's [i18n]): add it there, or format in one of them``.
  `` Locale`de-CH` `` is valid there, as is any literal in a program that
  lists no locales. A locale the program only compares or negotiates is
  parsed while it runs (`Locale.parse("fr")`), which no check refuses.
- **POSIX values.** `Locale.from_posix` reads what `LANG` and `LC_ALL` hold,
  `language[_territory][.codeset][@modifier]`. The codeset is dropped.
  `@latin`, `@cyrillic` and `@devanagari` become the script and `@valencia`
  the variant; any other modifier (`@euro`) is dropped. `C` and `POSIX`, with
  or without a codeset, name no language and are `und`. `pl_PL.UTF-8` is
  `pl-PL`, and `sr_RS@latin` is `sr-Latn-RS`.
- **Parts.** `Locale.language` is the language subtag, `und` when the tag
  names none. `Locale.script` and `Locale.region` are the subtags the tag
  spells; neither guesses a likely one.
- **Fallback chains.** `Locale.fallbacks` is the chain locale data is looked
  up along, nearest first: the locale, the locale without its extensions
  when it has any, then its ancestors by CLDR's parent locales, with root
  (`und`) excluded. A parent is the one CLDR's parentLocales names, else the
  tag with its last subtag removed. Two rules refine truncation. A tag with a
  region and no script whose region implies a different script than the
  language's own inherits from the language with that script (`zh-TW` goes to
  `zh-Hant`). A language-and-script tag whose script is not the language's
  likely one has root as its parent (`ru-Latn` has no ancestor). So `es-MX`
  is `[es-MX, es-419, es]`, `en-150` is `[en-150, en-001, en]`, `pt-AO` is
  `[pt-AO, pt-PT, pt]`, `pt-BR` is `[pt-BR, pt]`, and `zh-Hant-HK` is
  `[zh-Hant-HK, zh-Hant]`, never reaching `zh`.
- **Negotiation.** `Locale.negotiate` is CLDR language matching: the locale
  in `available` that best serves a reader who asked for `requested`, in
  order of preference, or `default` when none is close enough.
  `[pl-PL]` against `[en, pl, es]` is `pl`; `[ja]` against the same is the
  default.

### How code gets a locale

Nothing reads a locale implicitly. A function whose output depends on one
takes it as an argument, and an application keeps the current one in an
application field (§27), by convention named `locale`. A library cannot read
application fields, so it takes the `Locale` it needs. A locale is not a
`Context` value (§28).

```nomi
pub struct App {
    locale: Locale
}

pub fn boot(startup: Startup) -> App {
    locale =
        Map.get(startup.env, "LANG")
        |> Maybe.flat_map(fn value -> Locale.from_posix(value) |> Result.to_maybe())
        |> Maybe.with_default(Locale`en`)

    #App{locale}
}
```

A test group pins the locale through its boot, or overrides it with a `with`
line in `setup` (§36).

### Plural rules

```nomi
pub enum PluralCategory {
    Zero
    One
    Two
    Few
    Many
    Other
}

pub interface PluralOperand {
    fn plural_operand(value: self) -> Decimal
}
```

The functions are owner functions of `PluralCategory`
(``PluralCategory.cardinal(3, Locale`pl`)``):

```nomi
fn cardinal<N>(value: N, locale: Locale) -> PluralCategory where N: PluralOperand
fn ordinal(value: Int, locale: Locale) -> PluralCategory
fn cardinals(locale: Locale) -> Set<PluralCategory>
fn ordinals(locale: Locale) -> Set<PluralCategory>
```

`PluralCategory.cardinal` is the category a count selects, the form of a noun
it counts; `PluralCategory.ordinal` is the category of an ordinal ("1st",
"2nd"). `cardinals` and `ordinals` list the categories a locale's rules can
select, `Other` always among them. `Display` gives CLDR's keyword (`one`,
`few`).

`Int` and `Decimal` implement `PluralOperand`. A type implements it by giving
the number as its reader sees it, a `Decimal` whose scale is the number of
visible fraction digits; the sign does not matter. CLDR's operands (`n`, `i`,
`v`, `w`, `f`, `t`, `e`) are computed exactly from that Decimal's digits, at
any size, and `e` is always 0. Visible trailing zeros count: `1d` is `One` in
English and `1.0d` is `Other`. `Float` does not implement `PluralOperand`,
because its category depends on how many digits are shown, which a `Float`
does not carry; round it to a `Decimal` first (`Decimal.from_float`).

| Locale | Cardinal |
|---|---|
| `en` | 1 is `One`; 0, 2 and 1.0 are `Other` |
| `fr` | 0, 1 and 1.5 are `One`; 1000000 is `Many` |
| `pl` | 1 is `One`; 2–4, 22–24 are `Few`; 0, 5–21, 25 are `Many`; 1.5 is `Other` |
| `ar` | 0 `Zero`, 1 `One`, 2 `Two`, 3–10 `Few`, 11–99 `Many`, 100 `Other` |
| `ja` | every number is `Other` |

In English, ordinals 1, 21 and 101 are `One`, 2 and 22 are `Two`, 3 and 23
are `Few`, and 4 and 11 to 13 are `Other`.

A locale's rules are the first CLDR rule set on its plural fallback chain,
else root's, under which every number is `Other`. The plural chain follows
CLDR's parent locales for the plurals component, which in CLDR 48.2 is
truncation alone: `pt-BR` uses `pt`'s rules, `pt-PT` has its own, and
`zh-Hant-HK` uses `zh`'s.

### A program's locale data

A program carries the CLDR data of the locales its `nomi.toml` lists under
`[i18n].locales` (§31 *Locales*), and nothing else. A program with no
`[i18n]` table, a script outside any project among them, carries every locale
CLDR has data for. `Locale.available()` lists the carried locales, in the
manifest's order, or every locale when none is listed:

```nomi
fn available() -> List<Locale>
```

Every function that reads locale data (plural rules, number, date, money
and list formatting, parsing) looks a locale up along its fallback chain
among the carried locales. A locale whose chain reaches none of them is
looked up as the source locale (`[i18n].source_locale`, `en` unless the
manifest says otherwise) when the program lists it, and then in CLDR's
root. A locale the program does not list is therefore not an error at run
time: listing `en` and `de`, `de-CH` formats as `de`, and `fr`, read from a
reader's settings, formats as `en`: `October 20, 2026`, `€12.50`,
`a, b, and c`, with English plural rules. A program whose source locale is
not listed formats such a locale as root, which writes bare patterns
(`2026 M10 20`, `€ 12.50`, `a, b, c`) and whose plural rules make every
number `Other`. Whether the program carries a locale is
`Locale.available()`'s question at run time; a literal of one it does not
carry is a compile error (§41 *Locales*). `Locale.fallbacks` answers from
CLDR's parent locales for any tag, carried or not, and `Locale.negotiate`
reads no locale data, so neither depends on what is carried.

The grammatical genders and cases of a locale's nouns, as CLDR's
grammatical features list them, are carried data too. They are what a
`noun`'s catalog entries may give (§41 *Nouns and agreement*):

```nomi
fn genders(locale: Locale) -> List<String>   // pl: animate, inanimate, personal, feminine, neuter
fn cases(locale: Locale) -> List<String>     // pl: nominative, genitive, ... locative; en: none
```

`nomi run`, `nomi test`, the REPL and a built program (§25) read the same
data, so a program prints the same text under each. A built program carries
the data in its image, cut to the parts it reaches: one that formats numbers
and never lists items carries no list patterns, and one that reads no locale
data carries none. The collation data (§41 *Collation and case*) is the root
order, which a program that compares with a `Collator` carries once, and
each carried locale's tailoring. The Chinese and Korean tailorings are
carried only by a program that lists a locale whose collation chain reaches
them; one that lists no locales sorts Chinese and Korean in the root order.

#### Locales in the browser

A site may offer many locales while each reader reads one, so `nomi build
--web` does not put them all in `app.wasm`. It compiles in the locales
`[i18n].web_embedded` names (the source locale unless it says otherwise),
with CLDR's root, every listed locale's plural rules and grammatical
features, and every message's source text, and writes each other listed
locale to `locales/<tag>.nomil` beside it: that locale's numbers and lists
and its messages' and nouns' translations, with those of each ancestor on
its fallback chain that is not embedded either, so one file serves the
locale (`pt-PT`'s holds `pt`'s too). Every locale is still checked when the
program is built. A program that lists no locales, a program with no
messages that formats nothing, and every other kind of build carry every
locale in the program.

```nomi
fn loaded() -> List<Locale>                       // impl Locale
fn load(locale: Locale) -> Result<Unit, LoadError>  // impl Locale

pub enum LoadError {
    Unreachable {locale: Locale, reason: String}   // the file could not be fetched
    Unreadable {locale: Locale, reason: String}    // another build's file, or no locale file
}
```

`Locale.load` fetches the files of the locales on its argument's fallback
chain that are not loaded yet, and the calling task waits until they arrive
or fail; the program runs in a module worker, so the page goes on. A file
another build wrote, such as one a browser cached from the last deploy, is
refused: the image and its files share a build id. Anywhere but a browser
build `load` answers `Ok(Unit)` at once, as it does for a locale already
loaded or one the program does not list. `Locale.loaded()` is
`Locale.available()` less the locales whose files are not loaded yet.

Until its file is loaded a locale formats as its nearest loaded ancestor,
or as the source locale when none is loaded: the source text, and the
source locale's numbers, dates and lists when the image embeds it (it does
unless `web_embedded` leaves it out), else CLDR's root's. That is a safety
net, not the way to show a page, which loads the reader's locale before
formatting in it:

```nomi
fn choose_language(locale: Locale) -> Result<Unit, LoadError> {
    try Locale.load(locale)
    with App.locale = locale
    render_page()
    Ok(Unit)
}
```

### Formatting numbers

`Display` stays locale-independent: `"${1234567}"` is `1234567` everywhere,
because that text goes into file names, URLs, headers and CSV. Text for a
reader comes from owner functions that take a `Locale`, with options as named
arguments:

```nomi
// Int
fn format(n: Int, locale: Locale, grouping: Bool = True, min_digits: Int = 1) -> String

// Float
fn format(f: Float, locale: Locale, min_fraction: Int = 0, max_fraction: Int = 3, grouping: Bool = True) -> String
fn format_percent(f: Float, locale: Locale, max_fraction: Int = 0) -> String

// Decimal
fn format(d: Decimal, locale: Locale, min_fraction: Int = 0, max_fraction: Int = Int.max(Decimal.scale(d), min_fraction), grouping: Bool = True, mode: RoundingMode = RoundingMode.HalfEven) -> String
fn format_percent(d: Decimal, locale: Locale, min_fraction: Int = 0, max_fraction: Int = Int.max(Decimal.scale(d) - 2, min_fraction), mode: RoundingMode = RoundingMode.HalfEven) -> String
```

```nomi
Int.format(1234567, Locale`en`) // "1,234,567"

Int.format(1234567, Locale`de`) // "1.234.567"

Int.format(1234567, Locale`hi`) // "12,34,567"

Int.format(1234567, Locale`ar-EG`) // "١٬٢٣٤٬٥٦٧"

Int.format(1234567, Locale`ar-EG-u-nu-latn`) // "1,234,567"

Float.format(1234.5, Locale`fr`, min_fraction: 2) // "1 234,50" (U+202F)

Float.format_percent(0.256, Locale`de`) // "26 %" (U+00A0)

Decimal.format(1234.50d, Locale`de`) // "1.234,50"

Decimal.format_percent(0.125d, Locale`en`) // "12.5%"

Decimal.format(1d / 8d, Locale`en`, max_fraction: 2) // "0.12"
```

- **Symbols and patterns** are the locale's CLDR decimal and percent
  patterns and number symbols: decimal and group separators, plus, minus and
  percent signs, infinity and NaN. Grouping follows the pattern's sizes
  (`#,##,##0` groups lakhs), and CLDR's minimum grouping digits: Polish and
  Spanish group only from five integer digits, so `1234` in `pl` is `1234`
  and `12345` is `12 345`.
- **Digits** are the locale's default numbering system's (`ar-EG` writes
  Arabic-Indic digits). A `-u-nu-` extension naming a numeric system
  overrides it (`ar-EG-u-nu-latn`, `en-u-nu-arab`), and `-u-nu-native` picks
  the locale's native system. When the locale has no symbols for that system,
  its Latin symbols stand.
- **`Int.format`** pads to `min_digits` digits (1 to 100). **`Float.format`**
  rounds half to even to at most `max_fraction` digits and pads to at least
  `min_fraction` (0 to 100, `min_fraction <= max_fraction`); the rounding
  works on the shortest decimal that reads back as the float, so `1.015`
  rounds to `1.02`, as the digits a reader sees say. Infinity and NaN are the
  locale's symbols. An option out of range traps:
  `Int.format: min_digits must be 1 to 100, got 0`.
- **`Decimal.format`** writes as many fraction digits as the Decimal's
  scale has, at least `min_fraction` and at most `max_fraction` (the same
  bounds as `Float.format`'s, trapping outside them), rounding a digit past
  `max_fraction` by `mode`, half to even by default as `Money.round` does.
  By default it writes every digit of the scale and rounds none: `1234.50d`
  is `1,234.50` in `en`. **`format_percent`** multiplies by 100 (a Decimal
  exactly: `0.125d` is `12.5%`, a scale two less), then limits and rounds
  the percentage's digits the same way (`0.12345d` with `max_fraction: 1`
  is `12.3%`), and places the locale's percent sign where its pattern puts
  it (`%26` in `tr`).

### Parsing numbers

```nomi
// Int, Float and Decimal
fn parse_in(text: String, locale: Locale, lenient: Bool = False) -> Result<Int, NumberParseError>
// Float and Decimal
fn parse_percent_in(text: String, locale: Locale, lenient: Bool = False) -> Result<Float, NumberParseError>

pub enum NumberParseError {     // std/localize
    Empty
    Unexpected {at: Int, text: String}
    Grouping {at: Int}
    OutOfRange
    TooManyFractionDigits {max: Int}                   // Money.parse_in
    WrongCurrency {expected: Currency, found: String}  // Money.parse_in
}
```

Parsing never guesses the locale: `1.234` is one thousand two hundred
thirty-four in `de` and one point two three four in `en`.

- **Strict** (the default) accepts what the matching formatter writes for
  the locale, with three allowances: group separators may be left out, any
  Unicode space stands for a space the locale writes (a reader cannot type
  U+202F), and ASCII digits stand for the locale's own. Anything else is an
  error: a group separator where the pattern puts none is `Grouping` (`en`
  `12,34,567` is `Grouping{at: 2}`; `hi` `1,234,567` is `Grouping{at: 1}`),
  and any other text is `Unexpected`, including a group separator after the
  decimal separator (`en` `1.234,5` is `Unexpected{at: 5, text: ","}`),
  surrounding space, a leading `+` and a fraction in an `Int`. Text that
  stops early is `Unexpected` at the text's length with empty `text`
  (`Decimal.parse_percent_in("12.5", Locale`en`)`). `at` counts grapheme
  clusters, as `String.length` does.
- **Lenient** (`lenient: True`) also accepts group separators anywhere in
  the integer part, space around the text, a leading `+`, a minus sign after
  the number (`12-`) or parentheses around it (`(12)`), ASCII `-` and `%` for
  the locale's signs, the percent sign on either side, and leaves out the
  invisible direction marks some locales' signs carry.
- **Results.** A number outside the type is `OutOfRange` (an `Int` past
  64 bits; a `Float` whose digits overflow to infinity). `Decimal.parse_in`
  keeps the digits typed after the decimal separator as the scale, so `12,50`
  in `pl` is `12.50d`. `Decimal.parse_percent_in` divides by 100 exactly:
  `12,5 %` in `de` is `0.125d`, and `Float.parse_percent_in` reads the same
  text as the nearest `Float` to the fraction, `0.125`. `Float.parse_in`
  reads the infinity and NaN symbols. A formatted number parses back to its value in either mode.

### Lists

```nomi
// String
fn join_list(items: List<String>, locale: Locale, style: ListStyle = ListStyle.And, width: ListWidth = ListWidth.Wide) -> String

pub enum ListStyle {   // std/strings
    And
    Or
    Unit
}

pub enum ListWidth {   // std/strings
    Wide
    Short
    Narrow
}
```

`String.join_list` joins items as a reader of the locale lists them, with
CLDR's list patterns: `And` for all of the items, `Or` for one of them, and
`Unit` for the parts of one measure ("5 ft 3 in"); the width picks the
pattern's wide, short or narrow words. The last two items join by the
pattern's end form, each earlier one by its middle form and the first by its
start form, so `en` writes `a, b, and c` and `a and b`, `en-GB` `a, b and c`,
`pl` `a, b i c` and `ja` `a、b、c`. An `Iter` is materialized first
(`Iter.to_list()`).

### Collation and case

`<` on `String` compares UTF-8 bytes, and collation is never implicit. A
`Collator` (`std/collation`) orders strings as readers of a locale order
them:

```nomi
// impl Collator
fn new(locale: Locale, strength: Strength = Strength.Tertiary, numeric: Bool = False, case_first: CaseFirst = CaseFirst.Default, ignore_punctuation: Bool = False) -> Collator
fn compare(collator: Collator, a: String, b: String) -> Ordering
fn sort_key(collator: Collator, text: String) -> Bytes
```

```nomi
pub opaque type Collator String // std/collation; Equatable, Hashable, Debug

pub enum Strength {
    Primary // base letters: a = á = A
    Secondary // and accents: a = A, a < á
    Tertiary // and case: the default
    Quaternary // and what ignore_punctuation ignores, hiragana from katakana
    Identical // and code points
}

pub enum CaseFirst {
    Default // as the locale's collation says
    Upper
    Lower
    Off
}
```

The order is the Unicode Collation Algorithm (UTS #10) over CLDR's root
collation, with the locale's CLDR tailoring laid over it (UTS #35 part 5):
German keeps `ä` with `a`, Swedish puts `å`, `ä` and `ö` after `z`, Polish
puts `ł` after `l`, Czech `ch` after `h`, Lithuanian `y` with `i`, Chinese
sorts Han by pinyin before Latin, and Traditional Chinese by stroke count.
Strings are compared in NFD, so canonically equivalent strings are equal at
every strength below `Identical`. A Han character a tailoring does not list
sorts by its code point, the core ideographs before the rest, and an
unassigned code point after every assigned one.

The locale's tag chooses among a language's collations and sets the
options: `-u-co-` names a CLDR collation type (`de-u-co-phonebk` sorts `ü`
as `ue`; `es-u-co-trad` sorts `ch` and `ll` as letters; `zh-u-co-stroke`),
and `-u-ks-` (`level1` to `level4`, `identic`), `-u-kn-true`, `-u-kf-upper`
or `-u-kf-lower`, and `-u-ka-shifted` set what `strength`, `numeric`,
`case_first` and `ignore_punctuation` set. The settings apply in order: the
tailoring's own (Danish puts upper case first), then the tag's keywords, then
each argument that is not its default. A locale whose collation the program
does not carry orders as its nearest carried parent along the collation
chain (the collations component of CLDR's parent locales, else the main
ones short of root, else the tag less its last subtag: `zh-Hant` takes
`zh`'s stroke order), else in the root order.

`numeric: True` orders a run of decimal digits, of any script, by its value
(`file9` before `file10`). `ignore_punctuation: True` is UTS #10's shifted
alternate: spaces and punctuation are ignored at the first three levels and
compared at the quaternary. `Collator.sort_key` answers bytes that compare,
under `Bytes`' `Comparable` order, as `compare` compares the strings; a key
is meaningful only beside keys of the same collator and Nomi release.

`String` has case mapping for a language and caseless matching:

```nomi
// String
fn to_upper_in(s: String, locale: Locale) -> String
fn to_lower_in(s: String, locale: Locale) -> String
fn fold_case(s: String) -> String
```

`to_upper_in` and `to_lower_in` apply Unicode's full case mappings
(SpecialCasing: `ß` upper-cases to `SS`, and a capital sigma ending a word
lower-cases to `ς`) and the rules of the locale's language: Turkish and
Azeri map `i` to `İ` and `I` to `ı`, and Lithuanian keeps the dot of an
`i` under an accent. `String.to_upper` and `String.to_lower` stay
independent of any language. `fold_case` is Unicode's full case folding,
the same in every language: two strings that differ only in case fold to
the same string.

### Money

`std/currency` provides `Currency` and `std/money` provides `Money`, the
types payment, invoicing and exchange-rate libraries share:

```nomi
pub opaque type Currency String   // std/currency

fn parse(text: String) -> Result<Currency, Error>                  // `EUR`, or `BTC:8`
fn custom(code: String, minor_digits: Int) -> Result<Currency, Error>
fn code(currency: Currency) -> String
fn minor_digits(currency: Currency) -> Int
fn iso?(currency: Currency) -> Bool

pub struct Money {                // std/money
    amount: Decimal
    currency: Currency
}

pub enum CurrencyDisplay {
    Symbol
    Narrow
    Code
    Name
}

fn format(money: Money, locale: Locale, display: CurrencyDisplay = CurrencyDisplay.Symbol) -> String
fn round(money: Money, mode: RoundingMode = RoundingMode.HalfEven) -> Money
fn parse_in(text: String, locale: Locale, currency: Currency, lenient: Bool = False) -> Result<Money, NumberParseError>

impl Literal for Money          // Money`12.50 EUR`, Money`1200 PTS:0`
```

- **ISO currencies.** `` Currency`EUR` `` is an ISO 4217 currency, current
  or historical, as the pinned CLDR lists it, with its minor digits (EUR 2,
  JPY 0, BHD 3). A code CLDR does not list is a compile error at the literal:
  ``typed literal Currency`BTC` is invalid: `BTC` is not an ISO 4217
  currency: a custom currency names its minor digits, as Currency`BTC:2` ``.
- **Custom currencies.** `` Currency`BTC:8` `` and `Currency.custom("BTC",
  8)` make a currency ISO does not list: a code of 1 to 12 of `A`–`Z` and
  `0`–`9` starting with a letter, that is not an ISO code, and 0 to 18 minor
  digits. Two custom currencies are equal when their codes and digits are,
  so `PTS:0` and `PTS:2` are two currencies. The literal checks the same
  rules when the program is checked (`currency.Error`: `Malformed {text}`,
  `IsoCode {code}`, `NotIso {code}`, `Digits {digits}`). If a CLDR upgrade
  lists a custom code as ISO, its literal stops compiling.
- **Listed custom currencies.** A custom currency needs no setup, so a typo
  makes another one. In a file of a module (one with a `nomi.toml` above
  it), a `` Currency`…` `` or `` Money`…` `` literal naming a custom
  currency must name one its module's `[money].custom_currencies` lists
  (§31), code and digits; otherwise it is a compile error at the literal
  that suggests a listed entry with the same code or one edit away:
  ``typed literal Currency`PST:0`: `PST:0` is a custom currency this module
  does not list: add it to [money].custom_currencies in nomi.toml``, with
  ``help: did you mean `PTS:0`? nomi.toml lists it``. A
  `Currency.custom("PTS", 0)` call whose code and digits are both literals
  is checked the same way, at the call, and its error's help names the
  literal form. The editor shows a warning at every such call that is no
  error, naming the literal (``write Currency`PTS:0`, which the compiler
  checks``); `nomi check` prints none. A currency made from values while
  the program runs (`Currency.custom(code, digits)`, `Currency.parse`,
  `Money.parse_in`) is not checked, and a file in no module (a script, the
  REPL) and the standard library's own files may name any.
- **Money literals.** `` Money`1299.50 USD` `` is `#Money{amount: 1299.50d,
  currency: Currency`USD`}`: the amount, one space and the currency, the
  text `Display` writes. The amount is what `Decimal.from_string` reads (an
  optional sign, digits, `_` separators and an optional fraction) and keeps
  every digit written (`` Money`12.5 EUR` `` has scale 1); the currency is
  what `` Currency`…` `` reads, so `` Money`1200 PTS:0` `` names a custom
  one. A backtick literal is checked when the program is checked, and its
  errors (`money.Error`: `Malformed {text}`, `Amount {text}`, `NoCurrency
  {amount}`, `NotACurrency {amount, error}`, `Interpolated`) name what is
  wrong and show the form: ``typed literal Money`12.50` is invalid: `12.50`
  has no currency: write the amount, one space and the currency, as
  Money`12.50 EUR` ``. The literal does not interpolate: a double-quoted
  `Money"${amount} USD"` is `Err(Interpolated)`, whose message says to build
  `#Money{...}` from the fields.
- **Arithmetic.** `Money` implements `Add`, `Subtract`, `Multiply<Int,
  Money>` and `Summable`, all exact. Two amounts in two currencies trap,
  naming both, as `Int` overflow does: `Money.add: the amounts are in two
  currencies, EUR and PLN`, and `PTS:0 and PTS:2` for two custom currencies
  of one code. `Money.empty_sum()` is zero in `XXX`, ISO's code for no
  currency, which `+` leaves the other amount alone for, so `Iter.sum` of a
  list of amounts is their total in their currency. Two amounts are equal
  when their currencies are and their amounts are equal numbers. Std has no
  exchange rates; a library that converts checks `m.currency` first.
- **Rounding.** `Money.round` rounds the amount to its currency's minor
  digits, half to even unless `mode` says otherwise.
- **Formatting** writes the locale's currency pattern with the currency as
  `display` says: its `Symbol` (`€`, `US$` in `en-CA`), its `Narrow` symbol
  (`$`), its ISO `Code`, or its `Name` in the plural form the amount takes,
  by the locale's unit pattern (`12.50 euros`). The amount shows at least
  the currency's minor digits and every digit it has, so formatting rounds
  nothing. A symbol made of letters is kept apart from the digits by CLDR's
  `alphaNextToNumber` pattern (`BTC 0.50000000` in `en`, U+00A0). A custom
  currency has no symbol or name in CLDR, so every display writes its code.
  `Money` implements `Localize` with `format` and `Display` as the amount
  and the currency as its literal writes them, the same everywhere
  (`12.50 EUR`, `-5.00 USD`, `1200 PTS:0`).
- **Parsing.** `Money.parse_in` reads what `format` writes, with the currency's
  symbol, narrow symbol or code where the pattern puts it, and the
  allowances of `Decimal.parse_in`; lenient also takes the symbol or code on
  either side of the number, or none. A different currency is
  `WrongCurrency` (`12,50 zł` read as euros), and more fraction digits than
  the currency has is `TooManyFractionDigits`. The digits typed are the
  amount's scale.

```nomi
Money.format(Money`12.50 EUR`, Locale`pl`) // "12,50 €" (U+00A0)

Money.format(Money`1500 JPY`, Locale`ja`) // "￥1,500"

Money.format(Money`2 EUR`, Locale`en`, display: .Name) // "2.00 euros"

Money.format(#Money{amount: total, currency: eur}, Locale`en`) // computed values: the fields

Money.parse_in("12,50 €", Locale`pl`, Currency`EUR`) // Ok(12.50 EUR)
```

### Dates and times

The calendar types write themselves for a reader in three ways, Gregorian
only:

```nomi
// Date (DateStyle and DateFields), and alike for Time (TimeStyle,
// TimeFields), NaiveDateTime and DateTime (both styles, DateTimeFields)
fn format(d: Date, locale: Locale, style: DateStyle = DateStyle.Medium) -> String
fn format_fields(d: Date, locale: Locale, fields: DateFields) -> String
fn parse_in(text: String, locale: Locale, style: DateStyle = DateStyle.Short, lenient: Bool = False) -> Result<Date, Error>
fn to_string_with(d: Date, pattern: DatePattern) -> String
fn parse_with(text: String, pattern: DatePattern) -> Result<Date, Error>

fn format(dt: DateTime, locale: Locale, date: DateStyle = DateStyle.Medium, time: TimeStyle = TimeStyle.Short) -> String
fn weekday(d: Date) -> Weekday
```

- **Styles.** `DateStyle` and `TimeStyle` are `Full`, `Long`, `Medium` and
  `Short`, and the locale decides the rest: `Tuesday, October 20, 2026`,
  `October 20, 2026`, `Oct 20, 2026` and `10/20/26` in `en`. A date and a
  time join as the locale's date-time pattern for the date's style joins
  them. A `Full` or `Long` time of a `DateTime` ends with its offset in the
  locale's GMT format (`GMT+02:00`, `UTC+2` in `fr`); zone names are not
  std's. A `Time` and a `NaiveDateTime` have no zone, so their `Full` and
  `Long` times write what `Medium` does.
- **Skeletons.** `DateFields`, `TimeFields` and `DateTimeFields` name the
  fields a value shows and their widths as a CLDR skeleton: `y` the year,
  `M` or `L` the month (`M` its number, `MMM` its short name, `MMMM` its
  name), `d` the day, `E` the weekday, `G` the era, `j` the hour as the
  locale writes it (`h` a 12-hour clock's, `H` a 24-hour one's), `m`, `s`
  and `S`. Each locale's data gives the order, the punctuation and the
  words: `` DateFields`MMMMd` `` is `October 20` in `en`, `20 października` in
  `pl` and `10月20日` in `ja`. This is ICU's date-time pattern generator over
  the locale's available formats (UTS #35, *Matching Skeletons*): the
  available skeleton with the same fields and the nearest widths, widened to
  the widths asked for, except a field the locale writes in another form
  (`ja` writes a month as a number where a skeleton names it); a skeleton
  with date and time fields no one format covers is the two joined. The
  literal is checked when the program is checked: a letter that is no
  field, a field the type does not hold (an hour in a `DateFields`), a field
  named twice or too wide is a compile error at the literal. When every
  skeleton a built program uses is a literal, the build resolves each one
  for each listed locale and the image carries those patterns instead of
  the locales' available formats.
- **Fixed patterns.** `DatePattern`, `TimePattern` and `DateTimePattern`
  are numeric LDML patterns that write the same text in every locale, for
  file names, logs and protocols: `y` (`yy` two digits), `M`, `d`, `D`, `H`,
  `m`, `s` (each doubled for two digits), `S` (one per fraction digit), and
  quoted or non-letter text. A month or weekday name, AM or PM, an era and a
  zone are words or need them, so a pattern holds none: ``typed literal
  DatePattern`dd MMM yyyy` is invalid: invalid format: `MMM` writes the
  month's name, which is a word of a language: a fixed pattern writes
  numbers, the same in every locale; write it for a reader with a skeleton
  and a locale, as Date.format_fields(date, locale, DateFields`yMMMd`)``.
  `parse_with` reads what `to_string_with` writes, a one-digit field
  standing for a two-digit one. `DatePattern.parse` and its kin check a
  pattern made while the program runs.
- **Parsing.** `parse_in` reads text by the locale's pattern at a style,
  strictly by default: a two-digit field may be written with one digit, any
  space stands for a space the locale writes, and ASCII digits for the
  locale's. Lenient also reads names in any width or case, with or without
  their final period, any spaces and punctuation between fields, a
  four-digit year where the locale writes two, and spaces around the text.
  A two-digit year is 2000 plus its digits. Text that does not read is
  `Error.InvalidFormat`, naming what was expected where, and a value that
  is no date `Error.InvalidValue` (`month 2 of 2026 has no day 31`).
- **`Weekday`** is `Monday` to `Sunday`; `Date.weekday` gives the date's.
- `Date`, `Time`, `NaiveDateTime` and `DateTime` implement `Localize` with
  `format`'s defaults. `Display` stays ISO 8601.

### Durations and relative time

```nomi
fn format(d: Duration, locale: Locale, width: DurationWidth = DurationWidth.Long) -> String   // impl Duration
fn format_relative(count: Int, locale: Locale, unit: TimeUnit, numeric: Bool = False) -> String  // impl TimeUnit

pub enum DurationWidth {   // std/duration
    Long
    Short
    Narrow
}

pub enum TimeUnit {   // std/calendar
    Second
    Minute
    Hour
    Day
    Week
    Month
    Quarter
    Year
}
```

`Duration.format` writes each unit the length has, hours down to
nanoseconds, in the plural form its count takes, with CLDR's duration unit
patterns at `width`, CLDR's unit widths (`Long`, `Short` or `Narrow` words;
a list's `ListWidth` is a different set, CLDR's list widths), joined by the locale's
unit list pattern: `1 hour, 30 minutes` in `en`, `1 godzina i 30 minut` in
`pl`. A length is counted in hours, since a day is civil; zero is `0
seconds`. `Duration` implements `Localize` with it.

`TimeUnit.format_relative` writes `count` units from now: `in 3 days` and
`3 days ago` in `en`, `vor 3 Tagen` in `de`, `za 3 tygodnie` and `za 5
tygodni` in `pl`, each in the plural form the count takes. Unless `numeric:
True`, a count the locale has a word for is that word (`yesterday`,
`vorgestern`). Relative time takes a civil unit and a count, not a
`Duration`, because a month is no fixed length.

### `Localize`

```nomi
pub interface Localize {
    // std/localize
    fn localize(value: self, locale: Locale) -> String
}
```

`Localize` is how a type reads in a locale, as `Display` is how it reads in a
log line. `Int`, `Float` and `Decimal` implement it with their `format` and
its defaults, `String` with itself, `Money`, `Date`, `Time`,
`NaiveDateTime`, `DateTime` and `Duration` with theirs, `Message` with
`Message.format`, and `Noun` with its nominative for one thing.

### Messages

Text a person reads is declared once, in the program's source language,
with `message`, and translated in catalogs, one TOML file per locale. The
text is [MessageFormat 2](https://www.unicode.org/reports/tr35/tr35-messageFormat.html)
(MF2), the Unicode standard stable since CLDR 47, so translation tools read
it.

```nomi
/// How many items the cart holds.
pub message cart_items(count: Int) `
    .input {$count :integer}
    .match $count
    one {{{$count} item in your cart}}
    * {{{$count} items in your cart}}
    `
```

`message` and `noun` declarations follow the general rule for keyword declarations (§44).

#### The declaration

A `message` declares a function of the same name whose parameters are the
message's and whose result is a `Message` (`std/message`). It is called like
any function of its file, `cart.cart_items(3)`, and takes a doc comment and
`pub`. It writes no `-> type`. Its body is the text in the source locale,
`[i18n].source_locale` (English unless the manifest says otherwise).

A `Message` is locale-free: it holds which message and its arguments, and
`Message.format(message, locale)` writes it in a locale. Libraries return
messages and leave the formatting to the code that knows the reader's
locale. Two messages are equal when they are the same declaration with equal
arguments, `Debug` shows `cart.cart_items(3)`, and `Message` implements
`Localize`, so a message passed to another message is formatted in the same
locale.

The functions MF2 text formats with are the stable `:string`, `:number`,
`:integer`, `:percent` and `:currency`, the draft `:date`, `:time` and
`:datetime`, and four of Nomi's own, in the `nomi:` namespace MF2 gives a
function an implementation adds (a tool that does not know one still parses
the text):

| Function | Parameter type | Options |
|---|---|---|
| `:integer` | `Int` | `select`, `useGrouping`, `minimumIntegerDigits` |
| `:number`, `:percent` | `Int`, `Float`, `Decimal` | those, `minimumFractionDigits`, `maximumFractionDigits` |
| `:string` | `String`, an enum whose variants carry nothing | none |
| `:currency` | `Money` (std/money) | `currencyDisplay` (`symbol`, `narrowSymbol`, `code`, `name`) |
| `:date` | `Date`, `NaiveDateTime`, `DateTime` | `style` (`full`, `long`, `medium` when left out, `short`) |
| `:time` | `Time`, `NaiveDateTime`, `DateTime` | `style` (`short` when left out) |
| `:datetime` | `Date`, `Time`, `NaiveDateTime`, `DateTime` | `dateStyle`, `timeStyle`, or the fields `weekday`, `era`, `year`, `month`, `day`, `hour`, `minute`, `second`, `fractionalSecondDigits`, with `hourCycle` |
| `:nomi:noun` | `Noun` | `case` (a case of the locale's language, `nominative` when left out), `count` (a whole number or an `Int` parameter, 1 when left out) |
| `:nomi:gender` | `Noun` | none |
| `:nomi:list` | `List<String>` | none |
| `:nomi:weekday` | `Date` (std/calendar) | none |

`:currency` writes an amount as `Money.format` does. `:date` and `:time`
write a value at a style, and `:datetime` at its `dateStyle` and
`timeStyle` or by the fields its options name, as a skeleton
(`weekday=long month=long day=numeric` is `MMMMEEEEd`; `hour=numeric` is `j`,
and `hourCycle` makes it `h` or `H`); with neither it writes the date at
`medium` and the time at `short`. A field the value lacks is an error at the
option (`` en: `hour` names a time of day, and `$day` is a `Date`, which has
none ``), and so are styles beside fields. None of these selects: a
selector by one is an error, whose help points to `:nomi:weekday`.

`:nomi:list` writes the list as `String.join_list` joins it in the locale and
selects by its length, as a plural selector of an `Int` does. `:nomi:weekday`
only selects, by the date's day of the week: its keys are `monday` through
`sunday`, and a selector that names one day needs all seven. A placeholder
that writes it is an error. `:nomi:noun` and `:nomi:gender` are below.

`select` is `plural` (the default), `ordinal` or `exact`; `useGrouping` is
`auto`, `always`, `never` or `min2`. A placeholder with no function, `{$name}`,
writes its value with `Localize`, so its type must implement `Localize`
(`Int`, `Float`, `Decimal`, `String` and `Message` do). An enum selected or
written with `:string` reads as its variant's name (`Shipped`), whatever its
`Debug` says. `:offset` and `:unit` are each `` `:offset` is not supported
yet ``.

#### Catalogs

Each translation is an entry in a catalog: a TOML file at the declaring
file's path under the catalog directory, in a directory per locale, so
`shop/cart.nomi`'s Polish catalog is `locales/pl/shop/cart.toml`. An entry is
a table named by the message, holding `text`, the translation, and `source`,
the source text it was translated from:

```toml
# locales/pl/cart.toml
[cart_items]
source = '''
.input {$count :integer}
.match $count
one {{{$count} item in your cart}}
* {{{$count} items in your cart}}
'''
text = '''
.input {$count :integer}
.match $count
one {{{$count} produkt w koszyku}}
few {{{$count} produkty w koszyku}}
many {{{$count} produktów w koszyku}}
* {{{$count} produktu w koszyku}}
'''
```

A catalog holds comments, tables with bare or quoted names, and the two keys
as strings of any TOML kind; `'''` strings need no escaping of MF2's braces.
`[i18n]` names the locales (§31 *Locales*): `locales` must each have every
message, and `partial_locales` may leave any out.

#### Nouns and agreement

A message often has to agree with a noun: in Polish "Your book has shipped"
is "Twoja książka została wysłana" and "Your laptop has shipped" is "Twój
laptop został wysłany", because the possessive, the auxiliary and the
participle follow the noun's gender. A `noun` declaration names such a
thing as a value of std/message's `Noun`:

```nomi
pub noun book `.input {$count :integer} .match $count one {{book}} * {{books}}`

/// Sent when a parcel leaves the warehouse.
pub message has_shipped(product: Noun) `Your {$product :nomi:noun} has shipped.`
```

The declaration takes no parameters and no `-> type`; its body is its form in
the source locale, MF2 over one implicit `$count: Int`, how many things the
text names. `shop.book` is the noun, a `Noun` a message takes as an argument
and a struct may hold. Its catalog entry, in the same catalog as the file's
messages, gives the `source` it was translated from, its `gender` where the
language's nouns have one, and a form per grammatical case, each MF2 over
`$count`:

```toml
[book]
source = '''.input {$count :integer} .match $count one {{book}} * {{books}}'''
gender = "feminine"
nominative = '''.input {$count :integer} .match $count one {{książka}} few {{książki}} many {{książek}} * {{książki}}'''
genitive = '''.input {$count :integer} .match $count one {{książki}} few {{książek}} many {{książek}} * {{książki}}'''

[has_shipped]
source = '''Your {$product :nomi:noun} has shipped.'''
text = '''
.input {$product :nomi:gender}
.match $product
feminine {{Twoja {$product :nomi:noun} została wysłana.}}
inanimate {{Twój {$product :nomi:noun} został wysłany.}}
* {{Twoja przesyłka została wysłana.}}
'''
```

The genders and cases a language has are CLDR's grammatical features:
Polish has `animate`, `inanimate`,
`personal`, `feminine` and `neuter`, and seven cases; English has neither, so
an English noun has only its `nominative`, the form the declaration gives. A
language CLDR lists no cases for has the `nominative` alone.

`{$product :nomi:noun}` writes the noun's form for `case` and `count` in the
locale of the message's text. `.input {$product :nomi:gender}` selects by its
gender there; a placeholder with no function, `{$product}`, and `Localize`
write its `nominative` for one thing. A noun is formatted only through a
message or `Localize`, never by `Display`. Two nouns are equal when they are
the same declaration, and `Debug` shows `shop.book`.

The set of nouns is closed: every `Noun` is a declaration in a file the
program reaches, so agreement is checked over the whole program, as
coherence is. A message may be given any noun, so in each locale every noun
must have every form any message there asks for, and every selector on
gender needs an arm for every gender any noun there has. A product's own name
read at run time, such as a title from a database, is a `String`; passing one
where a `Noun` is wanted is the type error `` expected Noun, got String ``,
whose hint says a noun is a declaration. A message writes such a name with a
`String` parameter, in a sentence that does not agree with it.

A `noun` must be declared in a source locale whose language has no
grammatical gender: the declaration has no place to give one.

#### What the compiler checks

Every check is an error, at the declaration, in its text, or in the catalog
entry it is about:

1. **Completeness.** Every message has text in every locale `locales`
   lists: in that locale's catalog, or in a listed ancestor's on its CLDR
   fallback chain (`pt-PT` may leave out what `pt` says), or because the
   source locale is that ancestor. A locale in `partial_locales` may leave a
   message out, and the message then shows in the source locale.
2. **No stray entries.** A catalog entry that names no message of its file
   (`` pl: `ghost` is not a message of cart.nomi ``), a catalog file whose
   Nomi file does not exist, and a catalog directory for a locale `[i18n]`
   does not list, or for the source locale, whose text is the declarations'.
3. **Parameters.** Every variable a text reads is a parameter or a variable
   the text declares (`` pl: `$cnt` is not a parameter of `cart_items` ``),
   and the source text uses every parameter.
4. **Types.** Each function takes the type its row above names, and an
   unannotated placeholder's type implements `Localize`.
5. **Plural arms.** A plural selector has an arm for every category the
   locale's rules select for the operand's type, except `other`, which `*`
   covers: for an `Int` English needs `one`, and Polish `one`, `few` and
   `many`. An English-shaped Polish translation is
   `` pl: `cart_items` has no `few` arm; 2, 3 and 4 select `few` in Polish ``.
   A key that is not one of the locale's categories (or a number) is an
   error, and a `Float` is no plural operand: its category depends on the
   digits it shows, which a `Decimal` keeps.
6. **Enum selectors.** A `:string` selector over an enum has an arm for each
   variant and no other key, as `case` does.
7. **Freshness.** An entry's `source` is the declaration's current text,
   compared as MF2 data models, so layout and the order of declarations do
   not count. A stale entry in `locales` is an error:

   ```
   pl: `cart_items` was translated from an older English text
     was: {$count :integer} items
     now: {$count :integer} items in your cart
   help: update the translation, then accept it with the quick fix or `nomi messages accept pl cart_items`
   ```

   In `partial_locales` a stale entry counts as missing: the message shows
   in the source locale, never in a translation of the old meaning.
8. **MF2 validity**, including MF2's mandatory variant whose keys are all
   `*`, kept even where the other arms cover every value, so catalogs stay
   valid MF2 for other tools.
9. **Agreement.** Every noun has a translation in every listed locale, as a
   message does, recording its `source`, with no `text`. Its entry has a
   `gender` exactly when the language's nouns have one, and it is one CLDR
   lists (`` pl: `masculine` is not a gender in Polish ``); every other key
   is a case of the language (`` pl: `genetive` is not a case in Polish, nor
   a key of a noun's entry ``), the `nominative` is there, and each form has
   the plural arms the locale needs. A `case=` names a case of the language
   (`` en: `genitive` is not a case in English ``). Over the whole program,
   every noun has each form some message in the locale asks for
   (`` pl: `laptop` has no `genitive` form, which `sold_out` asks for ``, at
   the noun's entry), and each selector on gender has an arm for every gender
   some noun has there, naming the noun that has it
   (`` pl: `has_shipped` has no `personal` arm; the noun `staff.courier` is
   personal in Polish ``). A selector on gender in a language with none is
   `` en: English nouns have no grammatical gender, so `has_shipped` cannot
   select on `$product`'s ``.

`nomi messages accept <locale> <message>` records a message's current text as
a translation's `source`, once the translation says what it says.

#### Formatting

`Message.format` writes the text of the requested locale when the program has
one, else that of the nearest locale on its CLDR fallback chain that has one,
else the source text, so an unlisted locale shows the source. Numbers are
written with the requested locale's symbols and patterns, and plural
categories are its rules', from the CLDR data the program carries (§41 *A
program's locale data*). In a right-to-left locale each placeholder is
wrapped in Unicode isolates (U+2068 and U+2069), as MF2 recommends. Each
message's texts are stored in the function it declares, so a program carries
the texts of the messages it reaches and no others.

Plural categories are selected by the reader's locale's rules when the text
is a translation into its language (`pt`'s text read in `pt-PT` selects by
`pt-PT`'s rules), and by the text's own language's otherwise, since its arms
are keyed by that language's categories (an unlisted `fr` reads the English
text by English rules). A noun is written in the locale of the message's
text: its entry there, or the nearest listed ancestor's, else its source
form.

Two pseudo-locales need no catalog: `` Locale`en-XA` `` writes the source text
accented, bracketed and about a third longer (`[3 îţéɱš îñ ýöûŕ çåŕţ one
two]`), and `` Locale`ar-XB` `` writes it mirrored right to left, so text that
was not declared as a message stands out and layouts are tried at another
length and direction.

The language server reports a catalog's errors in the catalog file, open or
not, and rechecks when a catalog changes. Go to definition on a call of a
message lands on its declaration, and go to implementations lists its
catalog entries. Hover on a message shows its signature, its doc comment and
its text in the source locale and in each listed and partial locale, naming
the locale whose text a locale with no current translation shows. In a
catalog it completes the file's message names in a table's header, the
message's parameters after `$`, and a variant's keys: the plural categories
the locale's rules select for the selector, the enum's variants, or the
genders the module's nouns have; in a noun's entry, its `gender` and cases,
the language's genders as its `gender`, and the cases after `case=`. Hover
on a noun shows its gender and forms in each locale, and go to
implementations lists its catalog entries. Its quick fixes add a listed
locale's missing translation (the source text as `source`, and as `text`
with every arm the locale needs), mark a stale translation current as `nomi
messages accept` does, add the plural, enum or gender arms a translation
lacks, and add each form a noun lacks that the locale's messages ask for, a
copy of its nominative. An editor attaches the server to the TOML files
under `[i18n].catalogs`; the server answers no other TOML file.

#### Catalogs loaded at run time

A program can take translations while it runs, from a translation service
say, so a fixed translation needs no new build. `Catalog.load` reads a
catalog's TOML for one locale and checks it against the messages and nouns
the program was compiled with; `Message.format` reads the catalogs it is
handed before the program's own:

```nomi
// std/message
fn load(text: String, locale: Locale) -> #(Catalog, List<Rejected>)  // impl Catalog
fn format(message: Message, locale: Locale, catalogs: List<Catalog> = []) -> String  // impl Message

pub struct Rejected {
    key: String       // the table's name, `cart.cart_items`; "" outside any table
    locale: Locale
    reason: String    // what the compiler says of the same entry
}
```

```nomi
fn refresh(text: String) -> Catalog {
    #(catalog, rejected) = Catalog.load(text, Locale`pl`)
    Iter.each(rejected, fn r -> io.print("${r.locale} ${r.key}: ${r.reason}"))
    catalog
}

fn badge(catalog: Catalog) -> String {
    cart.cart_items(3) |> Message.format(Locale`pl`, catalogs: [catalog])
}
```

A loaded catalog holds the translations of any file's declarations, so each
table names its message or noun by its file, as `Debug` shows it:
`[cart.cart_items]`. Each entry is held to the checks above: it names a
message or noun the program uses (`` `cart.ghost` is not a message or noun
the program uses ``), in a locale `[i18n]` lists other than the source
locale; its keys are a message's or a noun's; its `source` is the
declaration's current text, so a stale entry is rejected in every locale;
its text is valid MF2 that reads only the message's parameters, with the
types, plural arms and enum arms its signature and the locale's rules need;
and its nouns and messages agree with the program's in that locale (a loaded
noun has every form a message there asks for, and a loaded message an arm
for every gender a noun there has). Completeness is not asked: a catalog may
translate one message.

`load` answers every entry it rejects, with the first problem the compiler
would report about it, without the locale it starts with. A rejected entry
is never used: its message keeps its compiled text, as does every message
the catalog does not translate. The checks read the declarations' types as
the compiler resolved them, which the program carries for each message it
reaches, so an entry for a message the program never formats is rejected as
one it does not use. At each locale of a fallback chain the first of
`catalogs` for that locale with an entry gives the text, before the
program's own; a message passed as an argument of another is formatted with
the same catalogs. Each catalog is checked against the compiled program, not
against the other catalogs a call is handed.

## 42. UUIDs

`std/uuid` provides `Uuid`, a 128-bit identifier as
[RFC 9562](https://www.rfc-editor.org/rfc/rfc9562) defines it. It is an
opaque type: values come from parsing, from bytes, or from generation.
`Uuid` is `Equatable`, `Hashable` and `Comparable`. Two UUIDs compare by
their 128 bits as an unsigned number, which is the order of their text and of
their bytes. `Display` gives the lowercase hyphenated form
(`6ba7b810-9dad-11d1-80b4-00c04fd430c8`); `Debug` gives the literal.

The functions are owner functions of `Uuid` (`Uuid.parse(text)`):

```nomi
fn parse(text: String) -> Result<Uuid, Error>
fn nil() -> Uuid
fn from_bytes(data: Bytes) -> Result<Uuid, Error>
fn to_bytes(id: Uuid) -> Bytes
fn version(id: Uuid) -> Int
fn v4() -> Result<Uuid, random.Error>
fn v7() -> Result<Uuid, random.Error>
```

- **Parsing** reads the 36-character hyphenated form in either case. Braces,
  a `urn:uuid:` prefix and the 32 digits without hyphens are not accepted. A
  failure is a `uuid.Error`: `Length {length}` for text that is not 36
  characters (or bytes that are not 16), `Separator {index, found}` for a
  missing hyphen, and `Digit {index, found}` for a character that is not a
  hexadecimal digit. `index` counts characters from 0.
- **The typed literal.** `` Uuid`6ba7b810-9dad-11d1-80b4-00c04fd430c8` `` is a
  `Uuid`. Its handler is `Uuid.parse`, so by the raw-literal rule (§16
  *Typed literals*) a malformed UUID is a compile error at the literal.
- **Bytes** are the 16 bytes most significant first, as a `bytea` or `BLOB`
  column holds them.
- **`version`** is the version field: 4 for a random UUID, 7 for a
  time-ordered one, 0 for the nil UUID.
- **`v4`** fills 122 bits from the operating system's entropy source and sets
  the version and the variant. **`v7`** puts the Unix time in milliseconds
  from `Instant.now` in the first 48 bits and fills the remaining 74 bits
  that are not version or variant from the entropy source. A v7 UUID made in
  a later millisecond compares greater, so v7 keys keep a database index in
  insertion order; two made in the same millisecond are in random order.
  Under a test group's virtual clock (§36) the time is the virtual one. Both
  return `Err(.OsEntropy{reason})` when the entropy source is unavailable,
  as `Seed.from_os` does (§40).

## 43. SQL Databases

`std/sql` is the vocabulary every SQL driver and SQL library shares; it
holds no driver. A driver module implements `Conn` for its engine, and code
that reads or writes a database takes a `Conn`, so one function serves a
connection, a transaction and a test's fake. `std/sql_testing` holds a recording
fake `Conn` and the conformance suite every driver runs. Both are written in
Nomi.

### Statements and the `Sql` literal

```nomi
pub struct Statement {
    parts: List<String>
    params: List<Value>
}
```

A `Statement` is the SQL text around its parameters (`parts`, always one
more than `params`) and the parameters themselves, so a driver writes its own
dialect's placeholders between the parts. `Sql"..."` builds one: `Sql` is the
tag type, and its `Literal` impl takes `List<Fragment<ToSql>>` (§16 *Typed
literals*). The text between the slots is the SQL, and each `${...}` slot's
value, which must implement `ToSql`, becomes a parameter. A value never
becomes SQL text, so a slot cannot inject SQL whatever it holds:

```nomi
name = "x'); DROP TABLE users; --"

s = Sql"SELECT id FROM users WHERE name = ${name}"
// s.parts == ["SELECT id FROM users WHERE name = ", ""]
// s.params == [Value.Text(name)]
```

Two slots side by side, or a slot at either end, have an empty part between
them or beside them. `Statement` is `Equatable`; `Display` writes the SQL with
`?1`, `?2`, ... for the parameters, numbered from 1 in the order they
appear, whatever placeholders a driver sends
(`SELECT id FROM users WHERE name = ?1`).

`a + b` (`impl Add<Statement, Statement> for Statement`) is `a`'s SQL
followed by `b`'s, with `a`'s parameters then `b`'s: `a`'s last part and
`b`'s first become one part. `Statement.join(statements, separator)` puts
`separator`, itself a `Statement`, between each two, and joins no statements
to the empty statement. Both build a query at run time from `Sql"..."`
pieces, so a value still reaches the database only as a parameter:

```nomi
filters = [Sql"status = ${status}", Sql"total >= ${min_total}"]

s = Sql"SELECT id FROM orders WHERE " + Statement.join(filters, Sql" AND ")
// "${s}" == "SELECT id FROM orders WHERE status = ?1 AND total >= ?2"
```

`Statement.values(items)`, for any `T: ToSql`, is `(?1, ?2, ...)` for SQL's
`IN`, and `(NULL)`, which matches no row, for an empty list.
`Statement.where_all(conditions)` is ` WHERE ` and the conditions joined by
`AND` (`where_any`: `OR`), each parenthesized when there is more than one,
and the empty statement for no conditions.

### Values

```nomi
pub enum Value {
    Null
    Int Int
    Float Float
    Decimal Decimal
    Text String
    Bool Bool
    Bytes Bytes
    Date Date
    Time Time
    Timestamp NaiveDateTime
    Timestamptz Instant
    Uuid Uuid
    Json Json
}

pub interface ToSql {
    fn to_sql(value: self) -> Value
}

pub interface FromSql {
    fn from_sql(value: Value) -> Result<self, String>
}
```

`Value` is a value as it crosses to or from a database; `Null` is SQL's NULL.
std implements `ToSql` and `FromSql` for `Int`, `Float`, `Decimal`, `String`,
`Bool`, `Bytes`, `Date`, `Time`, `NaiveDateTime`, `Instant`, `Uuid` (§42),
`Json`, `Value` itself, and `Maybe<T>` for any `T` that implements them, whose
`None` is NULL. Each type maps to its own variant and reads back only from it:
`Float.from_sql(Value.Int(1))` is an `Err`. `from_sql` answers `Err` with the
name of the type it expected (`"Int"`). `Value.kind(v)` is the variant's name.

### Rows

A `Row` is one result row: each column's name and value in order, and the SQL
of the statement that read it, which its errors name. It is opaque; a driver
builds it with `Row.new`. The functions are owner functions of `Row`:

```nomi
fn new(query: String, cells: List<#(String, Value)>) -> Row
fn query(row: Row) -> String
fn columns(row: Row) -> List<String>
fn values(row: Row) -> List<Value>
fn value(row: Row, column: String) -> Result<Value, Error>
fn value_position(row: Row, position: Int) -> Result<Value, Error>
fn get<T>(row: Row, column: String) -> Result<T, Error> where T: FromSql
fn get_position<T>(row: Row, position: Int) -> Result<T, Error> where T: FromSql
```

A column is found by its name (the first of that name) or by its position
from 0. A missing name is `Err(.MissingColumn{query, column})`, a position past
either end `Err(.ColumnPosition{query, position, count})`. `get` reads the
value as a `T`: NULL read as a type that is not a `Maybe` is
`Err(.UnexpectedNull{query, column})`, and any other value `T` cannot hold is
`Err(.Decode{query, column, expected, found})`, where `expected` is what
`from_sql` answered and `found` the value's `Value.kind`. `T` is inferred from
the expected type (`n: Int = try Row.get(row, "n")`) or given
(`Row.get<Int>(row, "n")`).

### Errors

```nomi
pub enum Error {
    Connection {message: String}
    Constraint {kind: ConstraintKind, name: String, table: Maybe<String>, column: Maybe<String>}
    Serialization {message: String}
    Database {code: String, message: String}
    RowCount {query: String, expected: String, found: Int}
    UnexpectedNull {query: String, column: String}
    Decode {query: String, column: String, expected: String, found: String}
    MissingColumn {query: String, column: String}
    ColumnPosition {query: String, position: Int, count: Int}
    Finished
    Closed
    Timeout
}

pub enum ConstraintKind {
    Unique
    ForeignKey
    NotNull
    Check
    Exclusion
}
```

`sql.Error` is every failure. A driver maps its engine's errors to these.
`Constraint` keeps the violated constraint's name, so a library can map it to
a field. The name is the same on every engine: the schema's own name for the
constraint, or the name Postgres gives one the schema leaves unnamed
(`users_pkey`, `users_email_key`, `orders_user_id_fkey`,
`orders_status_check`, `users_email_not_null`). A driver whose engine reports
something else (SQLite reports the columns, or a check's expression) works
the name out from the schema. `Serialization` means the transaction conflicted with
another and should run again. `Database` carries the engine's own code (a
Postgres SQLSTATE, an SQLite result code). `Finished` is an operation on a
transaction already committed or rolled back; the connection still works.
`Closed` is an operation on a connection that was closed; the caller needs a
new connection, which is why it is a variant of its own rather than
`Finished`. Both enums are `Equatable` and
`Display`: ``unique constraint `users_email_key` on `users.email` failed``.

### Connections and transactions

```nomi
pub interface Conn {
    fn query(conn: self, statement: Statement) -> Result<List<Row>, Error>
    fn each_row(conn: self, statement: Statement, f: (Row) -> Bool) -> Result<Unit, Error>
    fn exec(conn: self, statement: Statement) -> Result<Int, Error>
    fn begin(conn: self) -> Result<Conn, Error>
    fn commit(tx: self) -> Result<Unit, Error>
    fn rollback(tx: self) -> Result<Unit, Error>
    fn ping(conn: self) -> Result<Unit, Error>
    fn dialect(conn: self) -> Dialect
    fn close(conn: self) -> Result<Unit, Error>
}

pub enum Dialect {
    Sqlite
    Postgres
    Other String
}
```

Every driver follows these rules, which `std/sql_testing`'s conformance suite
checks:

- `begin` on a connection starts a transaction. `begin` on a transaction
  starts a savepoint inside it; the returned `Conn`'s `commit` releases the
  savepoint and its `rollback` rolls back to it.
- `commit` or `rollback` of a transaction ends it and every savepoint inside
  it. After that, `commit` and any statement are `Err(.Finished)`, and
  `rollback` is a no-op that answers `Ok`.
- A statement's parameters are sent as values, never spliced into its SQL.
- `each_row` calls `f` with each row in order and stops when `f` answers
  `False`; `exec` answers the number of rows the statement changed.
- A `Timestamptz` compares in time order with the timestamps the database
  writes itself (`DEFAULT CURRENT_TIMESTAMP`), in `WHERE` and `ORDER BY`.
- A constraint error's `name` is the constraint's name as Postgres reports
  it, as `Error.Constraint` says above.
- `close` closes the connection, whichever of its `Conn`s it is called on:
  the connection or a transaction or savepoint that `begin` returned. A
  transaction's `Conn` is a view of its connection, so there is one thing to
  close, and closing only the transaction would be `rollback`. A transaction
  still open is rolled back with every savepoint inside it, as SQLite and
  Postgres do when a connection ends. After that every operation of every
  `Conn` of the connection, `rollback` included, is `Err(.Closed)`; `dialect`
  still answers. `close` again is a no-op that answers `Ok`, as `rollback`
  after a finished transaction is.

`Dialect` names the engine for code whose SQL differs between them; its
`Display` is `SQLite`, `Postgres` or the other engine's name.

```nomi
fn transaction<T>(conn: Conn, body: (Conn) -> Result<T, Error>) -> Result<T, Error>
```

`sql.transaction` runs `body` in a transaction on `conn`: it calls `begin`,
then `commit` when the body answers `Ok`, or `rollback` when the body answers
`Err` or traps, and answers the body's result or the `Err` of a `begin` or
`commit` that failed. On a `conn` that is itself a transaction the
transaction is a savepoint, so an inner `sql.transaction` that fails rolls back
only its own work. The rollback is a `defer` that runs whenever the
transaction is left; after a commit it is the no-op the rules above make it.
A trap ends the task it happens in (§17 *Traps, `Maybe` and `Result`*), so
the rollback on a trap is seen from another task, after a spawned task's
`Failed` outcome.

```nomi
fn with_connection<C, T>(open: () -> Result<C, Error>, body: (Conn) -> Result<T, Error>) -> Result<T, Error> where C: Conn
```

`sql.with_connection` opens a connection with `open`, runs `body` on it and
closes it. It answers the body's result, or the `Err` of an `open` that
failed (the body does not run) or of a `close` that failed after the body
answered `Ok`. The close is also a `defer`, so the connection closes when the
body answers `Err` or traps; after the close on `Ok` it is the no-op the rules
above make it.

```nomi
count = sql.with_connection(fn -> sqlite.open("app.db"), fn conn -> {
    Conn.exec(conn, Sql"DELETE FROM sessions WHERE expired")
})
```

```nomi
fn close_quietly(conn: Conn)
```

A `defer` call answers `Unit`, so it cannot report a failed close.
`sql.close_quietly` closes `conn` and drops a failed close, for a `defer` on a
connection kept past one body, such as one a test group's `setup` opens for
each test (`defer` in a setup runs when the test finishes, §36). Nothing
reports or records the dropped error, so code that uses the connection in one
body runs it in `sql.with_connection`, which answers a failed close.
`with_connection`'s own `defer` is `close_quietly`.

```nomi
group "sessions" {
    setup {
        conn = try sqlite.open("app.db")
        defer sql.close_quietly(conn)
        conn
    }

    test "deletes expired sessions", conn {
        assert Conn.exec(conn, Sql"DELETE FROM sessions WHERE expired") == Ok(0)
    }
}
```

### Testing with `std/sql_testing`

`Fake` is a `Conn` that runs no SQL. It records everything sent to it, keeps
track of which statements took effect, and answers each statement with a
function the test gives it:

```nomi
fn new(dialect: Dialect = Dialect.Other("fake"), answer: (Statement, List<Statement>) -> Result<List<Row>, sql.Error> = ...) -> Fake
fn log(fake: Fake) -> List<String>
fn statements(fake: Fake) -> List<Statement>
fn committed(fake: Fake) -> List<Statement>
fn closed?(fake: Fake) -> Bool
```

`answer` gets the statement and the statements in effect as the connection
sees them (the committed ones, then those of each open transaction and
savepoint); by default it answers no rows. `exec` answers the number of rows
`answer` gives. A statement answered `Ok` takes effect in the innermost open
transaction or savepoint; one answered `Err` does not. `log` is everything
sent, each statement as `Statement`'s `Display` writes it and each
transaction command as a driver would send it: `BEGIN`, `COMMIT` and
`ROLLBACK` for a transaction, `SAVEPOINT sp1`, `RELEASE SAVEPOINT sp1` and
`ROLLBACK TO SAVEPOINT sp1` for a savepoint, numbered by depth, and `CLOSE` for
the first `close`. `committed` is the statements in effect outside every
transaction; a close discards those of every transaction still open.
`closed?` is whether `close` was called on the fake or on a `Conn` its `begin`
returned. Every `Conn` that `begin` returns shares its connection's record,
and the fake follows `Conn`'s rules.

```nomi
fn conformance<C>(open: () -> Result<C, sql.Error>) -> List<Failure> where C: Conn
fn rules() -> List<String>
fn conformance_answer(statement: Statement, visible: List<Statement>) -> Result<List<Row>, sql.Error>

pub struct Failure {
    rule: String
    message: String
}
```

`conformance` runs every rule against connections from `open` and answers
the rules broken, in the order `rules` lists them; a driver that conforms
answers `[]`. Each rule opens a fresh connection, closes it when the rule
ends (a close that fails breaks the rule), creates
`CREATE TEMPORARY TABLE nomi_conformance (n INTEGER NOT NULL, t TEXT)` on it,
writes it with `INSERT INTO nomi_conformance (n, t) VALUES (?1, ?2)` and reads
it with `SELECT n, t FROM nomi_conformance ORDER BY n`. The rules cover
`ping`, parameters as values (a text holding quotes and `--`), NULL, reading
a row by name and by position, `each_row` stopping, a transaction seeing its
own writes, commit and rollback, savepoints (rolled back, committed inside a
rolled-back transaction, and three deep), rollback after commit and after
rollback as no-ops, commit and a statement after commit as `Finished`, and
`sql.transaction` committing on `Ok` and rolling back on `Err`, on a trap and
in a failed inner transaction, a second close as a no-op, every operation
after close as `Closed`, closing a transaction closing its connection,
closing a connection ending its transaction and savepoint, and
`sql.with_connection` closing on `Ok`, on `Err` and on a trap, an `Instant`
written an hour before and after `CURRENT_TIMESTAMP` comparing and sorting
around it, and the names of a unique, primary-key, named check, not-null and
foreign-key violation, each on temporary tables of its own. The rollback of
an open transaction by a close is not checked: the suite's table is temporary
and goes with the connection. `Failure`'s `Display` is
`rule: message`. `conformance_answer` is an `answer` for `Fake.new` that reads
the suite's tables back from its inserts in effect and answers each
constraint error the suite expects, so the suite runs against the fake:

```nomi
test "the driver conforms" {
    assert sql_testing.conformance(fn -> Ok(Fake.new(answer: sql_testing.conformance_answer))) == []
}
```

## 44. Keyword Declarations

A top-level declaration that starts with a keyword and a name is a
**keyword declaration**, read by one rule whatever the keyword:

```
[pub] [file.]keyword[!] name [(params)] [-> type] [[Tag]raw body]
```

Each part after the name may be left out: `message cart_items(count: Int)
`...``, `noun item `...``, `sqlkit.database! main` and ``sqlkit.query!
orders(user_id: Int) Sql`SELECT ...` `` all fit it. A keyword and a name in
a row start nothing else at the top of a file, and a line break between
them ends a statement. The name is snake_case, or, after a keyword marked
with `!` or qualified by its file, a type name, since a macro may declare a
type: ``pub choices.one_of! Size `small medium large` `` and ``one_of! Size
`small` `` fit the rule too. An unmarked, unqualified keyword before a type
name starts no declaration, so `pub strct Point` is `` `strct` declares
nothing: a type is declared with `struct`, `enum` or `type`, and a macro's
declaration marks its keyword with `!`: `strct! Point` ``, and `message Hi`
is `` the name of a `message` is snake_case, not the type name `Hi` ``. The body is a backtick raw string, and may be a
typed literal's raw form: a tag written right before the opening backtick
names the body's language, so editors highlight it as they highlight that
typed literal (`Sql` as SQL). The tag is not a type and needs no import.

The keyword decides what the declaration means. `message` and `noun` are
built-in declarations (§41, *Messages* and *Nouns and agreement*), never
macros, written bare and unmarked; their body
is MessageFormat 2 and takes no tag (`` a `message` body is MessageFormat 2 text and takes no tag: remove
`Mf2` ``), and `message!` is `` `message` is built in; write it without `!`
``. Any other keyword is a macro's (§45), marked with a `!` written
against it, so a reader can tell that a library expands the declaration:
qualified by the file it comes from (`sqlkit.query!`), or bare after an
item import. A macro's keyword written without its `!` is the error ``
`sqlkit.query` is a macro, so it is written `sqlkit.query!`
``, and a keyword nothing declares is `` unknown declaration keyword
`database!` ``. `nomi fmt` prints each part that was written, the `!` and
the tag against what they mark.

## 45. Macros

A library adds a declaration keyword with a **macro**: Nomi code that
the compiler runs while it checks a program, which expands each declaration
written with the keyword into ordinary Nomi declarations. The keyword's
declarations are keyword declarations (§44), so the
grammar, the formatter and the editors need no change per keyword.

### Declaring a macro

A module declares the keyword and the type whose impls expand it, and
implements `Macro` from `std/macro` for the type in the same file:

```nomi
import std/macro.{ExpandCall, Expansion, Macro, Source}

/// A function that answers 42.
pub macro answer: Answer

type Answer

impl Macro for Answer {
    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl
        visibility = if decl.public { "pub " } else { "" }

        Source.new()
        |> Source.add("${visibility}fn ${decl.name}() -> Int {\n    42\n}\n")
        |> Source.expansion()
    }
}
```

`macro name: Type` names the keyword and the type; the type must be one
the file declares, and the file must implement `Macro` for it:
`` `Answer` implements no `Macro` in this file, so the macro
`answer` has nothing to run `` otherwise. `expand` is the one function
an impl must write: `request`, which says what to read, has a default
that asks for nothing, so a macro writes it only when it reads files or
other declarations (csvdata's `table`, below, asks for its CSV file). A
`pub` macro is part of the module's public surface, as a `pub` function
is.

### Using one

A file imports the macro's file from a module its `go.mod` requires
(`nomi add ../csvdata` adds one in a directory), and writes declarations with the keyword qualified by it and marked with
`!`, as it calls a function of the file (`io.print`):

```nomi
import csvdata

/// Exchange rates to the euro, in basis points.
pub csvdata.table! rate(code: String) -> Maybe<Int> `rates.csv`
```

A macro's declaration states the type of what it declares, so a reader
of the declaration sees the real signature, and the compiler holds the
expansion to it (*What an expansion declares*, below): `rate` answers `None`
for a code the table does not have, so its declaration says `->
Maybe<Int>`, and csvdata refuses `-> Int` with `` a table answers `None`
for a missing key, so write `-> Maybe<Int>` ``.

The qualifier is the file API object the import binds (§14): `csvdata`
after `import csvdata` (the module's root file `csvdata.nomi`), `ck`
after `import csvdata as ck`, `tables` after `import csvdata/tables`. A
qualifier no import binds is `` `kit` names no imported file, so
`kit.table!` names no macro ``, and a keyword the file does not
declare as a `pub macro` is `` file `csvdata` has no macro
`tabel` ``, whose hint lists the file's macros. As with a
function, an item import brings the keyword in bare instead: after `import
csvdata.table` the declaration is `pub table! rate(...)`, and
the import may rename it (`import csvdata.{table as csv}`, then
`csv! rate(...)`); the usual rules for an item import apply, so an
unused or clashing one is an error. Nomi's own `message` and `noun` are
never qualified. A module cannot use a macro it declares, as a Rust crate
cannot use its own procedural macros: the compiler runs a macro's code
before it checks the module that uses it, so the declaration is the error
`` `table` is a macro this module declares, and a module cannot
use its own macros ``. A file in no module (no `nomi.toml` above it)
cannot use a macro.

### What a macro is given

The compiler builds the macro's file as a program of its own and runs
two of its impl's functions. Each takes one record, so a macro reads only
the fields it uses. `request` is given a `RequestCall`, the `decl` and
`config` below, and answers what to read: `files`, paths or globs
(`data/*.csv`) from the declaring module's root, and `sites`, keywords
whose declarations in the module the macro wants to know. Each is empty
unless the macro names some (`#Request{files: [path]}`), and the default
`request` answers `#Request{}`. `expand` is then given an `ExpandCall`,
whose fields are:

- `decl`, the `Declaration` as the parser read it: its keyword (alone,
  `table`, with no qualifier or `!`), name, visibility, parameters
  and result type as written (`TypeText`: the text, which the macro
  never resolves, and where it is), its raw body (`Body`: the text without
  the indentation a multi-line body's lines share, where each line starts,
  so `Body.span_at` places a part of it, its tag, `Some("Sql")` for
  ``Sql`...` `` and `None` for an untagged body, and the tag's span,
  `tag_span`), its doc comment, the declaring file's path from the module
  root, the keyword's position, its `!` included (`span`), and the name's
  (`name_span`), wherever the parser read it. A macro that wants
  a tag, or none, says so with `Expansion.error` at the tag or the body:
  csvdata's `table` body is a path, so ``Csv`rates.csv` `` is `` a
  table's body is the path of its CSV file and takes no tag: remove
  `Csv` ``, under `Csv`;
- `config`, the table of the declaring module's `nomi.toml` named after the
  macro's module (`[csvdata]`), as `Json`, `Json.Null` when there is
  none. Nomi reads two tables of `nomi.toml` itself, `[module]` and
  `[i18n]`, so a macro of a module named `module` or `i18n` cannot be
  used: a declaration with its keyword is the error `` the macro `gen`
  cannot be used: its module is named `i18n`, so its settings would be
  nomi.toml's `[i18n]` table, which is Nomi's own ``, and declares
  nothing;
- `inputs`, each requested file that exists, with its text. A path or glob
  that leaves the module root, by `..` or a symbolic link, is an error at
  the declaration, and nothing outside it is read;
- `sites`, the declarations of the requested keywords in the module's
  files, each with its name, file and position.

A `Span` is a file's path from the module root, and a line, column, end line
and end column, columns counting bytes. A macro reads its body and inputs
as `String`s, so it finds a piece by its offsets, counted in graphemes as
`String.slice` counts them, and `std/macro` turns the offsets into a span:
`Body.span_at(body, start, end)` for a piece of the body, on as many lines
as it covers, `Input.span_at(input, start, end)` for a piece of an input,
and `Span.on_line(file, line, text, start, end)` for a piece of a line
`text` of an input that the macro reads line by line, as a CSV cell is
placed. `Span.line` is a whole line.

### What it answers

`expand` answers an `Expansion`: Nomi source text, a span map from byte
ranges of the source to the spans the user wrote they were made from (each
with an optional hint), and diagnostics. Each has a default, so
`#Expansion{diagnostics: problems}` declares nothing and reports
`problems`. `Source` writes the source piece by piece and keeps the map:
`Source.add` adds text, `Source.mapped` text that was made from a span
(with `hint: Some(...)` added to an error in it), and
`Source.string_literal` a string copied from the input, written as the
Nomi string literal that reads back as it (`String.to_literal`: quoted,
with `\n`, `\t`, `\\`, `\"` and `\${` escaped and `\u{...}` for any other
character that is not printable), so a CSV cell holding a tab, a quote or
`${` is the string the file holds. `pascal_case` and `snake_case` name
what a macro makes from its input's words (`extra_large` is
`ExtraLarge`, `HTTPServer` is `http_server`). The checker checks the
source as part of the declaring file:

- Its top-level names join the file's: a call of `tables.rate("usd")`
  reaches the function a `table` in `tables.nomi` declares, and the
  function with the declaration's name is at the declaration's name for go to
  definition and hover. A declaration whose name is a type name declares
  that type: ``pub choices.one_of! Size `small medium large` `` expands to
  `pub enum Size` with the variants `Small`, `Medium` and `Large`, which
  another file imports (`import sizes.Size`) and matches on as any enum
  (`Size.Small -> 8`). Go to definition on `Size` goes to the
  declaration's name, and on `Size.Small` to the word `small` the macro
  mapped it from. The `Declaration` says nothing more: a macro that
  declares a type reads the case of `name` itself, and reports a
  snake_case name with `Declaration.error`. A `pub` name it declares is public, which another
  file reaches through the declaring file; any other is private to the
  declaring file, which may use it. An expansion may declare names besides
  the declaration's own (sqlkit's `database! main` declares a struct per
  table), and a macro names such a helper after the declaration
  (`rate_rows`) so that two declarations in a file do not collide.
- A name the source declares that the file declares or imports too is an
  error at the file's declaration, ``  `helper` is declared here and by
  `gen! answer` on line 6 ``, and one that another declaration's expansion
  in the file declares is an error at the later declaration, ``  `helper`
  is declared by `gen! two` here and by `gen! one` on line 6 ``. Either
  way the expansion that collides declares nothing, so the file's own
  declaration keeps its meaning, and the program reports the collision
  alone, as it reports a declaration the macro declined. Expansions in
  two files never collide: each file is its own scope.
- The source may start with whole-file imports (`import std/strings`),
  which only it sees, under names the file's code cannot write; an import
  with selected names is an error.
- The source names only what it declares, what its own imports bring, the
  prelude, and what the declaration writes: the names of its parameter and
  result types (`Maybe<User>` in `sqlkit.query! user(id: Int) ->
  Maybe<User>`, which the expansion returns, through the file's import of
  `User`), and a name of its body that the macro copies into the source
  with `Source.mapped`. A bare name in the source that reaches any other
  declaration or import of the declaring file is an error at the
  declaration, `` the macro `gen` (module genkit) expanded `gen! answer` to
  code that names `helper`, which this file declares on line 8: a macro's
  code names only what it declares, what its own imports bring and what
  the declaration writes, a defect in the macro ``. So a macro's code means
  the same in every file that uses it, whatever else the file declares.
  A local binding, a parameter or a type parameter of the source is its
  own, whatever the file declares. A type the declaration writes is the
  file's use of its import, whether or not the expansion names it, so an
  import that only a declaration's types need is not unused.
- An error the checker reports inside a mapped range is reported at the
  range's span, with its hint: a Float cell in an Int table is
  `` case branch type mismatch: expected Maybe<Int>, got Maybe<Float> `` at
  the cell in the CSV file, with the hint the macro mapped the cell
  with. The range's hint comes before the checker's own, since it is about
  what the user wrote and the checker's are about the generated code. An
  error outside every range is a defect of the macro, reported
  at the declaration as `` the macro `table` (module csvdata) expanded
  `csvdata.table! rate` to code with an error, a defect in the macro: ... ``,
  and a source that does not parse is likewise reported at the declaration.
  A location an error refers to (the return type a mismatch names) moves
  the same way: to the span its range was made from, or else to the
  declaration's name, its note saying it is in the code the macro
  generated. No position inside the generated source is ever shown.
- A diagnostic the macro reports is reported at its span: in the
  declaring file, or in an input file, where the language server publishes
  it on that file.

### What an expansion declares

The declaration says what the expansion declares, and the compiler checks
the expansion against it after the macro runs:

- A declaration whose name is a type name (`one_of! Size`) declares a type
  of that name: a struct, an enum, a distinct type, a type alias or an
  interface.
- One that writes a parameter list or a result type (`table! rate(code:
  String) -> Maybe<Int>`, `query! order_counts()`) declares a
  function of that name. The function's parameters end with the
  declaration's: the same names and types, in the same order, and a macro
  may put parameters of its own before them, which it documents (sqlkit's
  generated functions take a leading `conn: sql.Conn`). When the
  declaration writes a result type `T`, the function returns `T` or
  `Result<T, E>` for any `E` (sqlkit's queries return `Result<T,
  sql.Error>`). One that writes no result type leaves the result to the
  macro.
- One that writes neither (`database! main`) names what the macro makes,
  and need not declare anything of its name.

What the declaration names is `pub` exactly when the declaration is. Types
compare as the checker resolves them, so `sqlkit.Rows<Orders>`, written
through the expansion's own import, is the `Rows<Orders>` a declaration
writes after `import sqlkit.Rows`. A declaration's other names (sqlkit's
row structs) are the macro's to name and to make public or not.

What the declaration names also carries the declaration's doc comment: the
compiler attaches it, in place of any doc comment the macro wrote there, so
a macro does not copy `decl.doc`, and `nomi expand` prints the expansion
with it.

An expansion that breaks any of these is a defect in the macro, reported
at the declaration with what it expected and what the expansion declares:
`` the macro `hello` (module greet) expanded `greet.hello! say -> Int` to
`pub fn say() -> String`: its result should be `Int`, a defect in the macro
``, `` ... to `fn say() -> Int`: it should be `pub`, as the declaration
is ``, `` ... to code that declares no function `say` ``, `` ... to `pub fn
say(name: String, loud: Bool) -> Int`: its parameters should end with the
declaration's, `name: String` ``, `` ... its parameter `name` should be
`String` ``, `` the macro `one_of` (module choices) expanded `choices.one_of!
Size` to code that declares no type `Size` ``. The program reports it
alone, as it reports a declaration the macro declined, so a call of the
function elsewhere is not an error too.

### Declining a declaration

`Declaration.error(decl, message)` is an expansion that declares nothing and
reports `message` at the declaration's keyword, and `Expansion.error(span,
message)` one that reports it at a span the macro has, such as the result
type's or the tag's. When a macro answers diagnostics
and no source for a declaration, the declaration declares nothing, and
the program's other errors (an import of a name it would have declared, a
call of its function) only follow from that. A program with such a
declaration reports the diagnostics of its macro declarations, and
their macros' whole-module checks, alone, as a syntax error in an
imported file is reported alone. A macro that keeps a declaration's
signature while reporting an error about it (sqlkit's query whose SQL
changed since the snapshot) answers source and diagnostics both, and the
program's errors are reported beside its own.

### How it runs

The macro runs on the VM, in the sandbox a backtick typed literal's
handler runs in (§16, *Typed literals*): a `request` or `expand` that can reach an effect
(output, files, the network, the clock, randomness, tasks, channels, the
running app's fields) is refused before it runs, at the declaration, as
`` the macro `table` (module csvdata) could not expand `csvdata.table!
rate`: its `expand` has effects, which a macro may not have while a
program is checked (...) ``. Each run is held to a budget of 20,000,000
steps (calls, backward branches and iteration elements) and a growth of
the heap of 1 GiB; one that passes either, or faults, is an error at the
declaration that says which: `` `expand` did not finish within its budget
of 20,000,000 steps (calls, backward branches and iteration elements) ``.
Those two decide whether a run succeeds, and neither depends on how fast
the machine is, so a declaration that expands on a laptop expands on a
slow build server, and a cached expansion is what any machine would
answer. The compiler also stops a run still going after two minutes, far
longer than the whole step budget takes, as a guard against a step that
takes far longer than any should; that is reported as an internal failure,
not as the macro's answer, is never cached, and the next check runs the
macro again. A declaration whose run is refused or stopped, or whose
macro's module does not check, declares nothing, and the program reports
it alone, as it reports one the macro declined: `` the macro `one_of`
(module choices) could not expand `choices.one_of! Size`: its module
does not check: ... `` at the declaration, beside the macro module's own
errors in its files, and no error at a use of what it would have
declared. So `nomi check`, `nomi fmt` and the language server run no
code that acts outside the VM, whatever a checkout's macros are. The code
a macro writes runs with the program's effects, as any library's code
does.

An expansion is a function of what it read: the compiler's version, the text
of every file the macro's program is built from, the declaration, the
configuration, the requested files' paths and texts, and the sites. The
compiler caches each expansion under all of them, in memory, so a check runs
a macro only when one changed. Nothing is kept on disk, so each run of
`nomi check`, `nomi run` or `nomi build` runs every macro the program
uses. Every file an expansion read, the
macro's own source among them, is an input of the declaring file: the
language server expands the declaration again when one changes, and an
edit to a table that keeps what the expansion declares rechecks the
declaring file's bodies alone.

### Whole-module checks and the editor

Two interfaces of `std/macro` are optional, and their functions take one
record too. A `ProgramCheck` impl's `check_all` is given a `CheckCall`
(`decls`, `config`, `inputs`) and runs once over every declaration of the
keyword in a program's files, with the files their requests read, for what
no one declaration decides (two tables reading one file). A `Navigation`
impl answers the language server inside a declaration's body, on the VM
with the same limits: `hover`, `definition` and `complete` are given a
`NavigationCall` (`decl`, `config`, `inputs`, and the position's `line`
and `col`), and `actions`, given an `ActionsCall` (`decl`, `config`,
`inputs`, `diagnostic`), answers the quick fixes for a diagnostic the
macro reported. Each of the four has a default that answers nothing
(`None` or `[]`), so an impl writes only the ones it answers: a macro
that offers only quick fixes writes `actions` alone. A fix is
a list of edits to the declaring file or an input, and optionally a `Tool`
of the macro's own module to run: choosing such a fix runs `nomi tool
<module> <name> <args>` in the declaring module's root, shows what it
printed, and checks the open files again with what it wrote. The language
server runs a tool only when someone chooses the fix that names it, so
sqlkit's fix for a query the snapshot does not describe yet runs its
`prepare` tool.

### `nomi expand` and `nomi tool`

`nomi expand <file>` prints what each of the file's macro declarations
expands to, formatted, with what the macro reports. `nomi tool <module>
<name> [args]` runs the module's tool, the program `tools/<name>.nomi` at its
root, which its `entry_points` list: an ordinary program with every effect,
run in the current directory, for a macro's work that needs a
database, the network or files, such as writing a file the macro then
reads. A tool runs only when someone runs it. `<module>` is the current
module's name or one its `go.mod` requires.

## 46. Open Design Questions

1. **Exhaustive struct pattern opt-in.** Struct pattern matching is partial by
   default: unmentioned fields are ignored. A `!..` marker could opt into
   exhaustive field matching, so `#User{name, age, email, !..}` would become a
   compile error when `User` gains a field. This is the inverse of Rust, where
   `..` opts *out* of exhaustive matching. It waits on real usage showing the
   need.
