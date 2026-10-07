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
- Every ordinary local binding and every ordinary parameter in a function, lambda, or method body must be read. To intentionally ignore a value, bind it with a discard name: `_` or `_name`. Discard names use a single leading underscore; double-underscore names are reserved for runtime/compiler internals and remain ordinary bindings. Discard names are binding-position patterns, not readable variables; `_name` is only a human-readable label for the ignored value. The rule covers every name a pattern binds: a destructured parameter's names are parameters (`|(word, count)| count` reports `parameter 'word' is never read`), and the names a `case` arm, an `else` arm, an `if` condition or a test's setup binding introduces are local bindings. A struct pattern matches partially, so a field it does not need is left out (`.Suspended{reason}`).
- Parameter use is checked in every body, including `boot` and interface default methods. Bodyless interface and host declarations have no body to check. Use `_` or `_name` for intentionally unused parameters; a discard parameter does not introduce a readable binding. An interface implementation that must retain a named parameter can explicitly discard it in its body with `_ = name`.
- Non-final expression statements must return `Unit` unless they are `dbg` observations. A value-producing expression that appears before the block's final expression is rejected because its result would disappear silently. Bind intentional discards with `_ = expr`; use `dbg expr` or `expr |> dbg` when the point is temporary observation.
- The final expression of a body whose return type is `Unit`, written or omitted, may be a `dbg` observation too: `dbg expr`, or a pipe whose last stage is `|> dbg`. The value `dbg` passes through is discarded and the body returns `Unit`. `dbg` itself still answers its operand, so a `dbg` ending a body of any other return type is that body's value. Any other final expression whose type is not `Unit` is a return type mismatch in a `Unit` body.

```nomi
import std/io

fn main() {
    total = 2 + 3
    io.print("computed")
    [1, 2, 3]
    |> Iter.map(|n| n * total)
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
fn normalize(name: String): String {
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
        max_retries = 5    // fine — local binding shadows the file-level once binding inside main
        ...
    }
  ```
- **Block-nested ordinary bindings may shadow each other across nested scopes** (function bodies, lambda bodies, `case` arms).

Inside an implementation or interface default, a bare call first uses its
lexically resolved binding, including a local callable parameter or an ordinary
file function. Same-owner method lookup supplies a callee only when no such
binding resolves the name. This precedence also applies to pipeline calls.

A binding may carry an optional inline type annotation: `name: Type = value`. The annotation drives bidirectional inference into the value (so `m: Map<String, Int> = Map.empty()` constrains the polymorphic `empty()` return to a concrete map type) and is also a checked assertion — a value whose inferred type cannot unify with the annotation is a compile error. A function-typed annotation also drives each lambda parameter's type, so `f: (Int) -> Int = |x| x + 1` is accepted without per-param annotations on the lambda. Annotations are not yet supported on destructuring patterns (`(a, b) = pair`).

```nomi
m: Map<String, Int> = Map.empty() // pins the polymorphic return

count: Int = try parse_int("42") // assertion: Int or compile error

f: (Int) -> Int = |x| x + 1 // annotation drives lambda param types
```

#### Locally-determined bindings and turbofish

Type inference and the requirement to annotate are **decoupled**. The checker propagates types as far as it can — a value with an unsolved type parameter unifies against a concrete parameter at a call site, generic returns flow through arguments, and so on; inference is a capability to maximize. *Separately*, a binding must be **locally determined**: its type must be readable from its own line, without scanning forward to how the binding is later used.

A binding is rejected when its type is *wholly unresolved* — it carries no concrete information because its only type parameters are unsolved with nothing on the line to pin them:

```nomi
m = Map.empty() // ✗ Map<?, ?> — not locally determined

ch = Channel.buffered(4) // ✗ Channel<?>

xs = [] // ✗ List<?>

x = None // ✗ Maybe<?>
```

Supply the type with an **LHS annotation** or an **RHS turbofish** — explicit type arguments on a call, `f<T>(...)`, which mirror the callee's signature `<...>`:

```nomi
m: Map<String, Int> = Map.empty() // LHS annotation

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

This is deliberately stricter than full inference: a binding's type is *not* rescued by a later use, even though the checker could solve it from that use. The local-readability requirement is a policy, independent of how far inference reaches. Turbofish itself is an ordinary call form — usable anywhere a call is, required only where a binding would otherwise be wholly unresolved. Each type argument is a type annotation, so a name that is no type is the same `unknown type "Nope"` error at the name (`ident<Nope>(1)`).

#### Determined type arguments

A generic function runs at the type arguments of each call (§13), so a call must determine every type argument its function makes values of, wherever the call appears: an argument, a `dbg`, an `assert`, a pipe stage. A function makes values of a type parameter that its signature mentions inside a function type, or two levels inside a parameter type when its result holds that parameter in a collection: `Result.collect` takes `Iter<Result<T, E>>` and returns `Result<List<T>, E>`, so it makes Ts. A type argument nothing in the enclosing declaration fixes, by the end of it, is a compile error at the argument that carries it:

```nomi
dbg Result.collect([]) // ✗ the element type of `[]` is not determined

dbg Maybe.values([None]) // ✗ the type argument T of `Maybe.values` is not determined

dbg Iter.map([], |x| x) // ✗ the element type of `[]` is not determined: x has it
```

An unsolved type argument that no value has is not an error: the element of an empty collection (`dbg []`, `Iter.to_list([])`, `List.head([])`, `Vector.empty()`) or the variant a value is not (`Ok(1)`, `Result.collect([Ok(1)])`, whose E only passes an error through). Fix the error as a binding: annotate the empty value (`xs: List<Result<Int, String>> = []`) or give the call its type arguments (`Result.collect<Int, String>([])`).

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

## 3. Visibility

Each declaration may carry the optional `pub` modifier. `pub` makes the declaration visible to other files; without it, the declaration is private. Private means visible only within the declaring file.

```nomi
import std/maybe.Maybe

pub fn parse_input(s: String): Result<Config, Error> { ... }

pub struct Config {
    host: String
    port: Int
}

pub enum Error {
    Malformed {line: Int}
    Truncated
}

fn validate_internal(c: Config): Bool { ... }              // private
```

A file qualifies the declarations it exports. Other files import the file and call through its file API object (`io.print(...)`, `io.IOError.NotFound{...}`). The file API object is only a qualifier: `io` alone, as a statement, a bound value or an argument, is the error `` `io` is a file, not a value ``. Inside the defining file, sibling declarations remain bare (`IOError`, `print`). Plain imports are lexical aliases only; import entries with `export` re-export from the current file, so an exported import becomes part of that file's public surface.

```nomi
pub enum IOError {
    NotFound {path: String}
}

pub fn print(value: String): Unit { ... }
```

Rules:

- **Placement.** `pub` is the leftmost modifier on a declaration. Order: `pub [opaque] <keyword> <name> ...`.
- **Applicable to.** `fn`, `struct`, `enum`, `type` (distinct), `interface`, `typealias`, `once`, `host fn`, `host type`. Structs, enums, and distinct types may also take `opaque` after `pub`: `pub opaque struct Name { ... }`, `pub opaque enum Name { ... }`, or `pub opaque type Name InnerType` (primitive-distinct).
- **Not applicable to.** Interface implementation functions (inherit visibility from the implemented interface), struct fields (inherit from the struct), enum variants (inherit from the enum), and local bindings inside function bodies (always block-scoped).
- **Root-level public functions are file API.** A file can publish root-level `pub fn` and `pub host fn`; callers reach them through the imported file API object (`users.find(...)`, `io.print(...)`). Functions and `once`s declared in an `impl` block are not file API: they are reached through their owner (`Duration.seconds(1)`, `Int.max_value`, `Int.to_string(n)`, `Display.to_string(n)`), and a file-qualified spelling such as `duration.seconds(1)` is an error that names the owner.
- **Private declarations are invisible to other files.** No other file can name a private function, type or interface in any spelling: a call (`other.helper()`), a type annotation or type argument (`x: other.Hidden`, `List<other.Hidden>`), a struct literal or pattern head (`other.Hidden{v: 1}`), an enum variant or constructor (`other.Color.Red`), an interface bound or `impl other.Shape for ...` header, or a selective import (`import other.Hidden`). A file-qualified spelling is the error "file 'other' has no member 'Hidden'", and an import is "'Hidden' is private and cannot be imported". A file may still pass its own private type as the type argument of another file's public generic (`other.ident(Point{x: 1})`, `other.Span{start: p, stop: q}`): only the declaring file names the type.
- **No renames at the declaration site.** A declaration is published under its own name. Re-exporting an imported name under a different alias is handled by import statements (see §14 *Files and Imports* — re-export forms are documented alongside imports).
- **Opaque qualifier.** `pub opaque struct`, `pub opaque enum`, and `pub opaque type Name InnerType` publish the type's *type name* while keeping its construction surface (constructor, unwrap, destructuring, field access) private. Applies only to types with a representation — using `opaque` on functions, type aliases, interfaces, `once` bindings, or zero-sized `type` declarations is rejected. See §15 *Opaque distinct types*.
- **Interface implementation functions inherit interface visibility.** A function inside an `impl Iface for Type { ... }` block has no per-function `pub` marker. If the interface is `pub`, other files can call the implementation functions; if the interface is private, those functions stay private too. See §13 for the private-wrapper pattern when you want a public interface's defaults only inside the file.
- **Struct fields and enum variants are not separately publishable.** Field access follows the struct's visibility; enum variant visibility follows the enum.

Keywords are always lowercase: `pub`, `opaque`, `once`, `fn`, `struct`, `enum`, `interface`, `impl`, `for`, `type`, `typealias`, `as`, `todo`.

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
    value: String                      // private — no `pub` modifier on the struct
}

pub enum Wrapper {
    embeds Secret
}

pub fn leak(): Secret { ... }            // error — public function returns private type

pub struct Container {
    data: Secret
}
```

This prevents private types from leaking into the public API. If a type appears in a public signature, it must itself be public.

**Relaxation for opaque types.** When a distinct type is declared `pub opaque type <name> InnerType`, its wrapped representation (the inner type, struct fields, or enum variant payloads) may be private. Outside callers can't reach the representation through the construction surface — that's blocked by the opacity check — so a private wrapped type doesn't leak. This enables the "public handle, private internals" idiom: a type that's reference-able by name but whose shape is hidden. Non-opaque distinct types are still subject to the standard rule above. See §15 *Opaque distinct types*.

### Re-exporting imported names

A file or module can re-export names it imports, making them part of its own public surface. This is opt-in: an `import` is private by default; adding the `export` modifier promotes the imported name(s) to public.

```nomi
// Per-item re-export.
import other.{Helper export as Helpr, Internal, Thing export}

// Line-level shorthand — every selected item re-exported under its imported name.
import std/io.{self, IOError} export
```

Rules:

- **Per-item placement.** `export` follows the optional `as <Local>` rename: `Name [as Local] [export [as Pub]]`. The `Local` (if any) governs the local binding; the `Pub` (if any) governs the externally-visible name.
- **Line-level placement.** `export` follows the full import entry and re-exports every selected item under its imported name: `import path.{A, B} export`. Use braces when the selector needs per-item detail (`A export as Pub`, `A as Local`) or an owner selector (`Owner.{self, A, B}`). There is no line-level `export as <Alias>` for multi-name selector lists, since there is no single name to rename. Use per-item `export as` inside braces instead.
- **No globbing.** There is no `import path.*` form. Each re-exported name is enumerated, either individually or via the selector-list shorthand. This keeps the public surface explicit and prevents upstream changes from silently expanding a downstream module's API.
- **Shorthand vs per-item.** A line-level `export` means "re-export every selected item under its imported name." Per-item `export [as Pub]` overrides this for that item. Both can appear in the same statement; per-item wins for the item it's on.
- **Visibility.** A re-exported name's upstream definition must already be public — `import other.{private_thing export}` errors at the import site, since `private_thing` isn't reachable to begin with.
- **No alias-on-export for local definitions.** `export` applies only to imported names. To expose a local definition under a different name, rename it or write a wrapper.

## 4. Naming Conventions

| Entity               | Convention              | Example                        |
| -------------------- | ----------------------- | ------------------------------ |
| Types (`type`, `interface`, `typealias`) | PascalCase (required) | `User`, `HttpRequest` |
| Enum variants        | PascalCase (required)   | `Some(T)`, `Active`, `True`    |
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
fn add(x: Int, y: Int): Int {
    x + y
}
```

- Type annotations required on parameters and every non-`Unit` return type; omitting the return annotation means the function returns `Unit`. A parameter written without a type, in a `fn`, an impl's or an interface's function, or a `host fn`, is the error `parameter 'x' needs a type annotation`; an interface implementation's parameters are written out, not taken from the interface. Only a lambda's parameter types may be inferred.
- Types inferred within function body
- Function bodies always render across multiple lines — `fn add(x: Int, y: Int): Int { x + y }` reformats to the multi-line shape shown above. This matches the dominant convention from gofmt, Prettier, dart format, zig fmt, swift-format, and rustfmt's default. Empty bodies stay flat as `{}`. The rule applies to top-level `fn`s, impl-block functions, and interface default functions; inline expression-position blocks like `if cond { x }` keep their flat form (see §11).
- Last expression is the return value
- `return` keyword for early exit — `return value` exits the nearest function boundary with `value`. This applies equally to named functions (`fn`) and lambdas (`|...| ...`) — no non-local returns. Bare `return` is valid when the function returns `Unit`. A bare `return` that the body would reach its end without is a compiler error: one that is the last statement of a function, lambda or test body, reached through the last statement of a block, either branch of a final `if` or any arm of a final `case` (`` this `return` does nothing: it is the last statement of `reset` ``), and a final `if` or `case` whose every branch is empty or a bare `return` (`` this `if` does nothing: every branch returns and nothing follows it ``). A bare `return` after a `dbg`, or after an assertion outside a test body, is not one: it discards that statement's value. A tail `return value` is legal, and `nomi fmt` rewrites it to `value`. A statement after one that always exits can never run, and is a compiler error at that statement whose help is "remove it". A statement always exits when it is a `return`, `break` or `continue` (`unreachable code after return`); an `if` with an `else` whose every branch always exits (`` unreachable code: the `if` above returns in every branch ``); a `case` whose every arm always exits (`` unreachable code: the `case` above returns in every arm ``); or a block statement that holds one (`unreachable code: the block above always returns`). A `todo` statement is `Unit` and does not exit, so code after a stub stays legal while it is written, and a call never counts as an exit.
- `try x` is sugar for `case x { Ok(val) -> val, Err(e) -> return Err(e) }` (and similarly for `Maybe`). Since `return` exits the nearest function boundary, `try` inside a lambda returns from that lambda, not from an outer function.
- `:` separates parameters from return type
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
fn classify_all(xs: List<Int>): List<Int> {
    fn classify(n: Int): Int {
        // helper, only visible inside classify_all
        if n < 0 { return 0 }
        if n > 100 { return 100 }
        n
    }

    xs |> Iter.map(classify) |> Iter.to_list()
}
  ```
- A `struct`, `enum`, `type`, `typealias` or `interface` may also be declared inside a function body, scoped as a nested `fn` is. Its name is a type in every annotation in that block and its inner blocks: a binding's (`m: Meters = Meters(3)`), a lambda parameter's and its default's, a function type, and a type argument (`ident<Meters>(m)`).

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

A default is checked against its parameter's type, on a `fn` and on a `host fn` alike: `host fn pad(s: String, width: Int = "wide"): String` is the error `default value for parameter 'width' is String, expected Int`.

When a default is **not** trailing, positional args still fill consecutive slots from the start, so to leave a non-trailing default empty you switch to named args for everything after it:

```nomi
fn middle(a: Int, b: Int = 10, c: Int): Int { ... }

middle(1, 2, 3)            // a=1, b=2, c=3
middle(1, c: 3)            // a=1, b=10 (default), c=3
middle(1, 2)               // error: missing argument for parameter 'c' (positional can't skip b)
```

An omitted default is evaluated once per call, after the written arguments, in parameter order; a default may refer to earlier parameters.

A default is not part of the function's body, so `try`, `return`, `break`, `continue` or an assertion written directly in it is a compile error at the keyword: `fn f(x: Int = try parse(s))` is `` `try` cannot be used in the default of parameter 'x': a default has no function to return from ``. A lambda's defaults follow the same rule. An exit inside a lambda that the default builds is the lambda's own.

The one exception is the **trailing-lambda routing** described in the next subsection: a lambda passed as the final positional argument can skip earlier defaulted slots without naming.

**Defaults apply to calls, not to function values.** A function named as a value has its full parameter list, defaulted parameters included, so it does not fit a function type with fewer parameters. Passing it, binding it, or piping it where the shorter type is expected is a compile error that suggests the lambda to write instead, which says which arguments the defaults fill:

```nomi
fn parse(s: String, strict: Bool = False): Maybe<Int> { ... }

parse("12") // OK — a call; strict = False

Iter.map(words, parse) // error: expected (String) -> U, got (String, Bool) -> Maybe<Int>

f: (String) -> Maybe<Int> = parse // error: the same mismatch

Iter.map(words, |w| parse(w)) // OK

Iter.map(words, parse(_)) // OK — a partial application leaves strict to its default
```

### Parameter Destructuring

A function or lambda parameter may be an **irrefutable** destructuring pattern instead of a plain name — the same pattern shapes a binding uses (§7). The body sees the bound sub-names directly:

```nomi
fn subtract(Duration(x), Duration(y)): Duration {
    Duration(x - y)
} // distinct, self-typed

fn sum_pt(Point{x, y}): Int {
    x + y
} // typed struct, self-typed

fn add_pair((a, b): (Int, Int)): Int {
    a + b
} // tuple needs an annotation

fn rename({name: n, age: a}: Person): String {
    n
} // anon struct, renaming fields

sub = |Duration(x), Duration(y)| Duration(x - y) // lambdas too
```

**The self-typing annotation rule.** A parameter's type comes from *either* its `: Type` annotation *or* a pattern whose head names a concrete type. So:

- **Self-typing → annotation optional.** A pattern whose head names a type carries that type itself: distinct `Duration(x)`, typed struct `Point{x, y}`, qualified single-variant enum `E.V(x)`. Writing the redundant annotation (`Duration(x): Duration`) stays legal but `fmt` strips it.
- **Type-less → annotation required.** A pattern with no type head has nothing to infer from, so it needs `: Type`: tuple `(a, b): (Int, Int)`, anonymous struct `{x, y}: Point`, dot-enum `.V(x): E`, and the plain `x: Int`. A generic newtype without type arguments (`Box(x)`) also still needs an annotation — it can't pin its type parameters from the head alone.

**Only irrefutable patterns are allowed.** A pattern is irrefutable when *every* value of the parameter's type necessarily matches it. Refutable patterns are **rejected by design** with a diagnostic pointing the author at a body `case`:

- literals (`42`, `"x"`, `true`)
- multi-variant enum variants (`.Some(x)`, `Color.Red`) — a value of the enum may be a *different* variant
- map patterns (`{"k" => v}`) — the key may be absent at runtime
- list patterns (`[a, b]`, `[h, ..t]`) — the length may not match
- any pattern nesting a refutable sub-pattern

A **single-variant** enum is irrefutable (every value is that one variant), so `E.V(x)` / `.V(x)` is accepted; a multi-variant enum variant is not. The distinction is type-driven: `Duration(x)` (a distinct, irrefutable) and `Some(x)` (an enum variant, refutable) are syntactically identical, and only resolving the head against the parameter's type tells them apart. Matching *into* one variant of a multi-variant enum is still a refutable `case` in the body — that's where conditional, possibly-failing matches belong.

**Multi-clause / overloaded definitions remain deferred** (§5 "No Overloaded Functions"): because Nomi has no fall-through between clauses, a refutable parameter pattern would have nowhere to go on a non-match. Restricting parameters to irrefutable patterns is the principled boundary (the same stance as Rust and Swift).

### Lambdas at Call Sites

Lambdas are always passed explicitly inside the argument parens — there is no trailing-lambda block sugar. A function with a final lambda parameter is called like any other:

```nomi
fn do_thing(required: Int, _option_a: String = "default", f: (Int) -> Int): Int {
    f(required)
}

do_thing(5, |x| x + 1) // skip optionals — lambda routed to f

do_thing(5, option_a: "custom", |x| x + 1) // override option_a; the trailing callback still routes to f
```

**Trailing-callback routing:** when the function's last parameter is still empty, the *final positional* argument is automatically routed into that last slot — even if defaulted parameters sit between it and the preceding positional args. This lets a required trailing callback stay unnamed in the common pattern (`Iter.each(items, |x| ...)`, `Iter.reduce(items, |acc = 0, x| ...)`) without forcing the caller to name it whenever a middle default is skipped.

The routing is decided by the parameter list, not by how the argument was written, so `each(items, process)` routes exactly like `each(items, |x| process(x))`. A lambda routes on sight, and so does a field accessor (`Iter.sort_by(users, .age)`, §8 Field Accessors). An argument written as a bare name routes when the last parameter takes a function and the slot it would otherwise fill does not — where both take functions there is a genuine choice, and the left-to-right order the call already states wins.

A single trailing positional may also follow named arguments (`each(items, opts: cfg, process)`): the named argument has already claimed its slot, so the positional skips past it. A positional written *before* a named argument still means the slot it sits in, which keeps `sum(1, a: 2)` the duplicate it reads as.

Defaults otherwise work as described in §5 Default Arguments; the call-site rule for lambdas is just "args go in `(...)`."

`concurrent { ... }` in §20 is a keyword-level block construct, not trailing-lambda sugar. Task work inside it is spawned with ordinary calls such as `Task.spawn(|| work())`.

### Named Arguments

Callers can name arguments at the call site. Positional and named can be mixed — positional must come first, with one exception: a single trailing positional is allowed after named args, so the common callback pattern doesn't force the user to name the lambda. It fills the first slot the named arguments left empty (see §5 Lambdas at Call Sites):

```nomi
connect("localhost", port: 3000, timeout: 10) // mixed

connect(host: "localhost", port: 3000) // all named

connect(timeout: 10, "localhost") // OK — the lone trailing positional fills host

each(items, opts: cfg, |x| process(x)) // OK — trailing callback after named arg
```

More than one positional after a named argument is a parse error:
`connect(timeout: 10, "localhost", 3000)` reports `positional argument after
named argument`.

**Resolution order:**
1. Positional args fill slots `0..N` left-to-right, where `N` is the number of positional args at the call site. (Exception: trailing-lambda routing — see §5 Lambdas at Call Sites.)
2. Named args fill remaining slots by name.
3. Defaults backfill any still-empty slots.
4. A required slot left empty is a compile error.

A named arg whose name matches a slot already filled positionally is a compile error (`parameter 'x' already has a value`). Reorder the call — switch the colliding positional to named, or drop the redundant name:

```nomi
fn add(x: Int, y: Int): Int {
    x + y
}

add(1, 2) // OK — positional

add(x: 1, y: 2) // OK — all named

add(1, y: 2) // OK — positional fills x, named fills y

add(1, x: 2) // error — x already filled by the positional 1
```

This rule also governs partial application: a named placeholder targets its named slot, and any positional placeholder/value still fills `0..N` strictly. The `_` may be named (`port: _`), in which case the partial's lone parameter inherits that name (see §5 Partial Application).

### Anonymous Functions (Lambdas)

Lambda syntax is `|params| body`. The body is a single expression.

A block body's result type is inferred from its final expression and its
explicit `return` values. All returning paths must agree; bare `return`
contributes `Unit`. Returns inside a nested function, lambda or `concurrent`
block belong to that inner boundary.

Zero-arg lambdas use empty pipes:

```nomi
// no args
|| fetch_user(42)

// single arg
Iter.map(users, |u| u.name)

Iter.filter(users, |u| u.age > 18)

// multiple args
Iter.reduce(xs, |a, b| a + b)

// with initial value via lambda default parameter
Iter.reduce(items, |total = 0, item| total + item.value)

// irrefutable destructuring in parameters — see §5 Parameter Destructuring
Iter.map(pairs, |(label, value)| "  ${label}: ${value}")

Iter.map(pairs, |(_, value)| value)

Iter.map(durations, |Duration(ns)| ns) // distinct, self-typed

// lambda parameters with default values
Iter.loop(|attempts = 0| {
    if attempts >= 3 { break Err("gave up") }
    attempts + 1
})
```

There is no implicit `it` parameter — every lambda names its parameters explicitly.

A parameter's type comes from its annotation, from its default, or from the function type the lambda's position expects: a call's parameter, an annotated binding, a return. A generic call's parameter counts when the call's own expected type fixes it: in `f: (Int) -> Int = ident(|x| x + 1)`, `ident`'s `T` is `(Int) -> Int`, so `x` is an `Int`. A generic constructor's field or payload counts the same way: in `f: Box<(Int) -> Int> = Box{v: |x| x + 1}` (or `Box({v: ...})`) and `g: Maybe<(Int) -> Int> = Some(|x| x * 3)`, `x` is an `Int`. A lambda in a position that expects no type, such as an unannotated binding (`f = |x| 1`) or a tuple or list element (`(|x| 1, 2)`), must annotate each parameter that has no default; otherwise it is `cannot infer type for parameter x`. A later use never types a parameter.

### Lambda Default Parameters

Lambda parameters can have default values. This is especially useful with `Iter.loop` where the defaults provide the initial state:

```nomi
Iter.loop(|attempts = 0| {
    case fetch(url) {
        Ok(data) -> break Ok(data)
        Err(_) when attempts >= 3 -> break Err("gave up")
        Err(_) -> attempts + 1
    }
})

// Tuple state — the annotation and the default attach to the whole tuple
// parameter, not to its elements
Iter.loop(|state = (n / 2.0, 0.0)| {
    (guess, prev) = state
    diff = guess - prev
    if diff > -0.0001 and diff < 0.0001 { break state }
    next = (guess + n / guess) / 2.0
    (next, guess)
})
```

### Multi-Expression Bodies

The body of a lambda is a single expression. To run multiple statements, use a block expression — `{ ... }` is itself an expression whose value is its last expression (see §33):

```nomi
|x| {
    doubled = x * 2
    doubled + 1
}
```

The block is the body; the lambda still has a single-expression body, that expression is the block.

### Type Annotations

Type annotations use `(Args) -> Return` for function types — `fn` defines functions, `(Args) -> Return` is the type:

```nomi
fn handler(db: database.Db): (Request) -> Response
```

**A function value fits a function type when it accepts every argument and its result fits.** A function of type `(P1) -> R1` may stand where `(P2) -> R2` is expected when each `P2` fits its `P1` (parameters are contravariant) and `R1` fits `R2` (results are covariant). Inside a function type, one type fits another when they are the same, when a concrete type meets an interface it implements (a `List<Int>` where an `Iter<Int>` is expected, an `Int` where a `Display` is), when a type meets an enum that `embeds` it, or when both are function types that fit by this rule. Type arguments are compared exactly: a `List<(Display) -> String>` is not a `List<(Float) -> String>`. The rule holds at every position a function value flows into: an argument, an annotated binding, a list element, a field, a result and a generic parameter.

```nomi
fn shown(x: Display): String {
    Display.to_string(x)
}

fn count(xs: Iter<Int>): Int {
    Iter.count(xs)
}

fn pair(n: Int): List<Int> {
    [n, n]
}

fn examples() {
    f: (Int) -> String = shown      // Int fits Display
    g: (List<Int>) -> Int = count   // List<Int> fits Iter<Int>
    h: (Int) -> Iter<Int> = pair    // the result List<Int> fits Iter<Int>
}
```

The reverse is a compile error, since the function would receive a value it does not accept: `fn float_str(f: Float): String` is not a `(Display) -> String`, and `fn total(xs: List<Int>): Int` is not an `(Iter<Int>) -> Int`. Where nothing says which way a value flows, two function types must be the same: the branches of an `if` or `case`, the elements of an unannotated list, and two arguments for one type parameter (`pick(c, shown, float_str)` with `fn pick<T>(c: Bool, a: T, b: T): T`). Pass a lambda of the one type instead (`pick(c, float_str, |x| shown(x))`).

### Partial Application

Explicit, using `_` placeholder. `_` as a bare function argument creates a partially applied function:

```nomi
add(1, _) // same as |x| add(1, x)
```

Each `_` leaves exactly one argument open. Can be named. Defaults are preserved.

A partial application is a function of its open arguments, in the order the `_`s are written, returning the callee's result. Where a function type is expected, that function meets it, and a generic callee's type parameters are solved from it: `f: (Int) -> Int = pick(True, _, 4)` instantiates `pick` at `Int`.

`_` stands for a value only as a call's argument, positional or named (`add(1, _)`, `connect(port: _)`), including the argument a pipe fills (`10 |> divide(100, _)`). Elsewhere `_` is a binding-position name: a discard binding (`_ = expr`), a discard parameter (`|_| 0`, `fn f(_: Int)`), a wildcard pattern, or a subject-less `case`'s catch-all arm. Any other `_` read as a value (`Point{x: _}`, `.A -> _`, `x = _`, `[1, _]`, `add(1 + _, 2)`) is the error `` `_` has no value here ``, whose hint says that `_` stands for a missing argument only in a call.

```nomi
halve = divide(_, 2)

divide_from_10 = divide(10, _)

connect_local = connect("localhost", port: _, timeout: 10)

connect_local(3000) // connect("localhost", 3000, 10)
```

Partial application with defaults:

```nomi
f = connect(_, port: _)

f("localhost", 3000) // timeout uses default 30

f("localhost") // port uses default 8080, timeout uses default 30
```

### No Auto-Currying

Functions are not automatically curried. Use `_` for partial application instead.

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
fn parse(text: String): Header {
    todo "parse the header"
}

fn area(shape: Shape): Float {
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
  function's result. As a statement it is `Unit`. A binding with no
  annotation expects nothing, so `x = todo` is an error asking for one
  (`x: Int = todo`); `_ = todo` is fine.
- **Reason.** The reason is a string literal: `"..."`, `"""..."""` or a raw
  backtick string. It is fixed text that tools list without running anything,
  so an interpolated or tagged string is a parse error.
- **Running it.** A program that reaches a `todo` stops, as a runtime fault
  does, with `todo reached at <file>:<line>` and then `: <reason>` when there
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

## 6. Pipe Operator

Left-to-right piping with `|>`. A stage is a call, and by default the piped value fills its **first argument**. Use `_` to override placement:

```nomi
// default: fills the first argument
[1, 2, 3] |> Iter.filter(|x| x > 2)

// explicit: fills a different position
10 |> divide(100, _)

// works with case — piped value becomes the match target
value
|> case transform() {
        Some(x) -> x
        None -> default
    }
```

**A stage is a call.** `x |> f()` is `f(x)` and `x |> f(a)` is `f(x, a)`:
the pipe inserts its value into the call that follows it. A name without
parentheses is a function reference wherever it stands, as in
`Iter.map(xs, io.print)`, so it is not a stage. `x |> f`, `x |> io.print`,
`x |> Ok` and `x |> Shape.area` are errors that name the call to write
(`` a pipe stage is a call: write `io.print()` ``), and the language server
offers a quick fix that appends the `()`. A `.Variant` stage is a call
written through its enum (`x |> Shape.Dot()`), since a bare `.Variant`
stage cannot see its enum (§8). The stages that are not calls are the
`then` stage and the keyword stages below.

**A lambda's body runs to the end of its expression,** or to the `)`, `]`,
`}` or `,` that closes the construct it stands in. A `|>` inside it belongs
to the body: `Iter.map(xs, |s| String.to_int(s) |> Maybe.with_default(0))`
maps each string to an `Int`, and `|n| n * 2 |> dbg` is a lambda whose body
ends in `dbg`. The one exception is the lambda of a `then` stage.

**The `then` stage:** `x |> then |v| body` applies the lambda to the piped
value, as `(|v| body)(x)` would. The lambda takes one parameter, which may
be a destructuring pattern (`then |(a, b)| a + b`). Its body ends at the next
`|>` of the pipeline, so the stages after it stay in the pipeline:

```nomi
total =
    orders
    |> Iter.filter(.paid?)
    |> then |paid| Iter.count(paid) * 100 / Iter.count(orders)
    |> dbg
```

Braces keep a pipe inside the body:
`|> then |v| { v |> Iter.filter(.paid?) |> Iter.count() }`. A parameter of a
bare `then` body used in a later stage is an error that says where the body
ended. A lambda is not a stage without `then`: `xs |> |n| n * 2` is the error
`` a lambda is not a pipe stage: write `then |n| ...` ``, and the language
server offers a quick fix that inserts the `then`. `then` is a reserved word
and stands only after `|>`; a keyword stage does not prefix it
(`|> try then |v| ...` is an error), so a `try` after a `then` is its own
stage. `nomi fmt` puts each stage of a pipeline with a `then` stage on its
own line.

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
number in its output. Because `dbg` is development-only instrumentation, the
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

- **`struct`** — a nominal record type with named fields. The body is a newline-separated list of `name: Type` field lines. Derives are sibling `derive Iface for Type` declarations. Manual interface implementations are sibling `impl Iface for Type { ... }` blocks, not type-body items. A single space separates the type name from the opening `{` — matches every other declaration keyword that takes a brace body. Construction uses no space (`Foo{a: 3, b: "x"}`).
- **`enum`** — a nominal sum type. Variants are one per line — `Red`, `Pending Int`, `embeds OtherType`. There is no `|` separator and no single-line form; enum bodies are always multi-line. Derives are sibling `derive Iface for Type` declarations. Manual interface implementations are sibling `impl Iface for Type { ... }` blocks.
- **`type`** — a distinct-type wrapper or zero-sized marker. The shape after the name distinguishes: tuple-distinct (`(A, B)`), generic-distinct (`Map<K, V>`), primitive-distinct (a bare type name), or zero-sized (nothing). Distinct `type` declarations do not take bodies; put constructors and helpers beside the type. Derives are sibling `derive Iface for Type` declarations. Manual interface implementations are sibling `impl Iface for Type { ... }` blocks. For nominal records and sum types, see `struct` and `enum`.
- **`typealias`** — a transparent synonym for an existing type. No body.

Type declaration names may be dotted PascalCase paths when the declared type is
owned by a real parent type. `type Civil.Days Int` declares a nominal type named
`Civil.Days`; it is a sibling declaration whose parent must already be in
scope, not an item nested inside `Civil`. Construction, destructuring, derives,
owner functions, and interface impls all use the full name:

```nomi
struct Civil {}

type Civil.Days Int

fn days_value(days: Civil.Days): Int {
    Civil.Days(n) = days
    n
}

impl Add<Civil.Days, Date> for Date {
    fn add(lhs: Date, rhs: Civil.Days): Date { ... }
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

fn new(name: String, age: Int): User {
    User{name, age}
}

user = User{name: "Alice", age: 30}

prefs = Preferences{theme: "dark", language: "en"}

id = Id(42)
```

Nominal record types are declared with `struct` — snake_case field names followed by `:` Type, with a single space between the type name and the opening `{`. Distinct-type wrappers use `type`: tuple-distinct `type Pair (Int, String)`, map-distinct `type Kvs Map<K, V>`, primitive-distinct `type Id Int`, zero-sized `type Expired`. See §15 for the full distinct-type taxonomy.

Struct *declarations* take a single space before `{` (matching every other brace-bodied declaration keyword); struct *construction* uses no space (`Foo{a: 3, b: "x"}`).

The newline-separated field layout applies only to **declaration bodies**. Anonymous struct *type expressions* (`{name: String, age: Int}` in type position), struct construction literals (`Foo{a: 3}`), struct-shaped enum variant payloads (`Card {rank: Int}`), and pattern syntax all keep their comma-separated shape unchanged.

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

fn rename(user: User, name: String): User {
    User{name: name, age: user.age}
}

impl Display for User {
    fn to_string(user: User): String { ... }
}

impl Serializable for User {
    fn serialize(user: User): String { ... }
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

ok = Response{} // status: 200, body: ""

not_found = Response{status: 404} // status: 404, body: ""

full = Response{status: 200, body: "hi"} // all explicit
```

Fields without defaults must always be provided. Use constructor functions when you need validation or transformation logic beyond simple defaults.

A field default has no function to exit, so `try`, `return`, `break`, `continue` or an assertion written directly in it is a compile error at the keyword (`` `try` cannot be used in the default of field 'port' of Config: a default has no function to return from ``).

A struct-shaped enum variant's fields take defaults the same way, and a default expression runs per construction, only when its field is omitted:

```nomi
enum Shape {
    Circle {radius: Float = 1.0}
    Rectangle {width: Float = 2.1, height: Float}
}

unit = Shape.Circle{} // radius: 1.0

tall = Shape.Rectangle{height: 9.0} // width: 2.1, height: 9.0
```

Only named fields take defaults. A positional payload (`Circle Float`) and a bare variant (`North`) cannot: `Circle Float = 3.14` and `North = 1` are compile errors, and the positional one names the struct-shaped form to use instead. A default needs a name so a construction that omits it still says what it left out, and a positional variant referenced bare (`Shape.Circle`) already means the constructor as a function value.

### Optional Fields

A field whose *value may be absent* uses `Maybe<T>`:

```nomi
struct User {
    name: String
    nickname: Maybe<String>
}

user = User{name: "Alice", nickname: None}
```

`Maybe<T>` and "field with a default" are independent concepts. A field with a default (see Default Values above) always has a value at runtime — the default just spares the caller from spelling it out. `Maybe<T>` says the value can semantically be absent (`None`). The two compose: `nickname: Maybe<String> = None` would mean "optional, defaults to absent."

### Construction

Struct construction uses braces with named fields. Field punning is a shorthand: when a variable has the same name as a field, the field name alone is enough:

```nomi
user = User{name: "Alice", age: 30}

// destructuring — type name not needed, just use braces
{name, age} = user

{name: n, age: a} = user

// field punning — `name` and `age` are bound already, so the field names
// on their own are enough
punned = User{name, age} // same as {name: name, age: age}

bumped = User{name, age: age + 1} // mix punning and explicit
```

Positional data uses tuples `(value, value)` or enum variants with parens — struct construction always requires named fields.

A struct can also be constructed in **call-form** by passing a single anonymous struct value to the type name:

```nomi
f1 = User{name: "Alice", age: 30} // literal-attach

f2 = User({name: "Alice", age: 30}) // call-form
```

The two forms are equivalent — pick whichever reads better. The call-form passes an anonymous struct value to the constructor, which coerces it to the nominal struct. Field names and types must match exactly; defaulted fields can be omitted just like in literal-attach:

```nomi
struct Defaulted {
    a: Int
    b: String = "default"
}

d = Defaulted({a: 7}) // b takes its default
```

Generic structs work the same way — type parameters are inferred from the anonymous struct's shape:

```nomi
struct Box<T> {
    v: T
}

b = Box({v: 1}) // T = Int, inferred
```

#### Target-typed construction

A brace literal with no type name and no spread builds the nominal struct its position expects, so the type name can be left out where the context already says it:

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

fn home(): Address {
    {street: "1 Main", city: "Bath"} // the return type says Address
}

fn main() {
    a: Address = {street: "2 Main", city: "Bath"} // the annotation says Address
    ada = Person{name: "Ada", address: {street: "3 Main", city: "York"}} // the field says Address
    people: List<Person> = [{name: "Bo", address: a}] // the element type says Person
    dbg a == Address{street: "2 Main", city: "Bath"}
    dbg home().zip
    dbg ada.address.city
    dbg people
    Unit
}
```

The positions that supply the struct are the ones that supply an enum to `.Variant` (see *Variant resolution*): an annotated binding or `once`, a struct field in construction, a function or constructor argument, a return value or block tail, an element of a typed `List`, `Set`, `Vector` or `Map` literal, a `case` or `if` arm whose result has an expected type, and a generic position once its type argument is known (`b: Box<Int> = {item: 3}` builds `Box<Int>`; `Some({street: "1 Main", city: "Bath"})` under `Maybe<Address>` builds an `Address`).

The literal follows the ordinary construction rules and builds exactly the value `Address{...}` builds: every field without a default is given, defaults fill in the rest, an unknown field is rejected, and an opaque struct is constructed this way only in its defining file. Errors name the struct: `missing field 'street' of Address`, `field 'city' of Address: expected String, got Int`, `Address has no field 'zip'`.

Where no struct type is expected, `{...}` is an anonymous struct, as below. It also stays anonymous where the expected type is an anonymous struct type, an interface, a type parameter or an enum: none of them names one struct to build, so the checker does not guess, and a mismatch there reports the anonymous struct's type (`expected Display, got {street: String, city: String}`). Under a struct-update spread a bare brace at a struct-typed field is a patch of the base's value, not a new struct (see *Struct Updates*).

### Tuples

Tuples use parentheses — fixed-size, positional, heterogeneous. Accessed by index:

```nomi
pair = ("Good Morning", "Good Evening")

pair.0 // "Good Morning"

pair.1 // "Good Evening"

// Destructuring
(x, y) = pair

// In function signatures
fn divide(x: Int, y: Int): (Int, Int) {
    (x / y, x % y)
}

(quotient, remainder) = divide(10, 3)
```

Tuples work inside other types:

```nomi
fn safe_divide(x: Int, y: Int): Result<(Int, Int), String> {
    case {
        y == 0 -> Err("division by zero")
        _ -> Ok((x / y, x % y))
    }
}
```

Tuples are structurally typed — the type is `(Int, String)`, `(Float, Float)`, etc. No single-element tuples — `(x)` is just grouping. Use named structs when the type appears in multiple places or needs its own functions.

### Anonymous Structs

Anonymous structs use braces without a type name. Always have named fields:

```nomi
g = {morning: "Good Morning", night: "Good Evening"}

g.morning // "Good Morning"

g.night // "Good Evening"

// Destructuring (field punning)
{morning, night} = g

// Destructuring with rename
{morning: m, night: n} = g
```

Anonymous structs are structurally typed — the type is its shape (`{morning: String, night: String}`), not a name. Two anon struct types are equal when their field sets (name → type) match, regardless of declared order: `{a: Int, b: String}` and `{b: String, a: Int}` are the same type. A function expecting `{morning: String, night: String}` accepts any anonymous struct with those fields and types. There is no nominal distinction.

Anon struct types are writable in any nested type position: function parameters, distinct-type tuple elements, generic type arguments, struct fields, and `typealias` right-hand sides. Top-level `type Foo {...}` (anon struct as the bare RHS of a `type` declaration) is reserved for the `struct` keyword:

```nomi
fn greet(p: {name: String, age: Int}): String {
    p.name
}

type Tagged (String, {tag: String, payload: Int})

struct Box {
    value: {x: Int, y: Int}
    // struct keyword for top-level
}

typealias Point {x: Int, y: Int} // a name for the shape
```

A function parameter typed `{name: String, age: Int}` accepts any anonymous struct value with those fields and types — field order at the call site doesn't matter. Error messages and hover render the type in the order it was declared at its source site, so two equivalent shapes can render differently; that's intentional. Nominal struct values (declared with the `struct` keyword) do *not* coerce — they're nominal.

`nomi fmt` keeps a short anonymous struct type or struct literal on one line, breaks a long one with a field per line, and keeps a multi-line shape you wrote; the layout rules are in [`implementation-notes.md`](implementation-notes.md#formatter-brace-bounded-field-constructs).

**No width subtyping.** A function parameter typed `{x: Int, y: Int}` accepts only values with *exactly* that shape — not anon structs with additional fields, and not nominal structs that happen to have matching fields. Width is part of the type's identity. This is a deliberate choice against TypeScript-style structural over-acceptance: field-name coincidence is not a semantic contract (an `id: String` on `User`, `Order`, and `Invoice` shares a type but rarely a meaning), and silent acceptance erodes trust as the codebase grows. Code that genuinely needs to operate across multiple struct shapes has three explicit alternatives — declare an interface for the shared *behavior* (not the shared shape); construct an anon struct at the callsite (`distance({x: p.x, y: p.y}, {x: q.x, y: q.y})`); or define a nominal domain type and write a conversion function (`to_coord(p: Point): Coord2D`) so mismatches surface where they can be seen.

### Struct Updates

`Struct.update` returns a new struct with selected fields replaced. It is the sole function of `Struct`, the universal interface every struct — named or anonymous — satisfies, so it is reached type-qualified (`Struct` is in the prelude, so no import is needed). Its signature is `update(original: self, updates: Partial<self>): self`; for a call such as `Struct.update(point, ...)`, `self` denotes `Point`, so the return type is `Point`, not an abstract `Struct`. `updates` is a `Partial<self>`, a built-in type (like `List<T>` / `Map<K, V>`) describing a *deep partial* of the struct: an anonymous struct whose fields are a subset of the target's, where each field is itself either a full value or a deep partial of the corresponding field's struct type.

```nomi
updated = Struct.update(user, {name: "Bob", age: 42})

// works naturally with pipes (inside users.nomi)
user
|> Struct.update({name: "Bob"})
|> users.validate()
|> try users.save()
```

**Nested updates:** When a field's value in the update is an anonymous struct, it's treated as a recursive merge — only the specified fields change, the rest are preserved. A concrete (named) struct replaces the field entirely:

```nomi
// anonymous struct → merge (only city changes, other address fields preserved)
Struct.update(user, {address: {city: "NYC"}})

// desugars to:
Struct.update(user, {address: Struct.update(user.address, {city: "NYC"})})

// nests arbitrarily deep
Struct.update(company, {
    ceo: {
        address: {
            city: "NYC",
        },
    },
})

// concrete struct → replace the entire field
Struct.update(
    user,
    {address: Address{street: "123 Main", city: "NYC", zip: "10001"}},
)
```

**Conformance is structural.** Every struct value satisfies `Struct`; no non-struct type does, with no conformance entry written anywhere. Requiring `Struct` of a non-struct — calling `Struct.update(5, …)`, or passing a non-struct to a `where T: Struct` bound — is a compile-time error (`Int does not implement Struct`), never a runtime trap. The compiler also verifies the patch's field names and types against the target via `Partial<self>`: an unknown field name or a type-mismatched override is a compile error; only the top-level patch target must be a struct, while its *fields* may be any type. Because `Struct` is a genuine interface, it is also usable as a **bound** (`fn widen<T>(x: T): T where T: Struct`) and an **existential** (`fn dump(s: Struct)`, `List<Struct>` holding heterogeneous structs). It is **not hand-implementable**, though: conformance is decided by shape, not a registered impl, and `update` is a host-backed final default — so an `impl Struct for T { ... }` block couldn't change what satisfies `Struct` and is rejected (`Struct` is a built-in structural marker and cannot be implemented).

**Opacity restricts the operation, not the conformance.** An *opaque* struct (§15 *Opaque distinct types*) satisfies `Struct` exactly like any other struct — at a `where T: Struct` bound, at a `Struct` existential, and everywhere else conformance is asked — because conformance is a property of the type's shape and nothing else. What opacity closes is the one place `Struct.update` names a private field: **a patch may not name a field of an opaque struct from outside that struct's defining file** (`cannot update field 'value' of opaque type 'Counter' outside its defining module`). This is the same rule as the field-access row of §15 *Opaque distinct types*, applied to the patch, and it composes through nesting — `Struct.update(box, {inner: {value: 0}})` is rejected when `Box.inner` is an opaque `Counter`, even though `Box` itself is transparent. Replacing a value wholesale names no field and stays legal, including replacing an opaque field: `Struct.update(box, {inner: counter.new()})` is fine. Deciding this at the operation rather than at the conformance is the same choice the auto-`Debug` default makes for an opaque type (§15 *Opaque distinct types*): `Debug` stays satisfied by every type, and only the *body* goes name-only.

#### Struct update by spread

`{..base, field: value}` is the same operation written as a literal. One spread, written first, followed by the fields that change.

```nomi
updated = {..point, x: 9}
```

**A spread changes at least one field.** `{..point}` is rejected (`a struct spread with no fields is its base value; write \`point\``): values are immutable and have no identity, so a copy could do nothing the value itself does not.

**The result has the spread head's type.** A nominal struct in gives that nominal struct out, an anonymous struct in gives an anonymous struct out — which is `Struct.update`'s `update(original: self, …): self` rule in literal form. That is also why there is no named spelling: `Cfg{..base, f: v}` is rejected (`struct spread \`..\` has no named form`), because the head already carries the type and the name would repeat what the compiler knows.

**Exactly one spread, and it must be the first element.** Two spreads and a spread after a field are both `struct spread \`..\` must be the first element`.

**Position differs from the list spread, and the reason is representation on one side and notation on the other.** A list spread must be *last* (§19): a `List` is cons cells, so prepending is O(1) and appending is O(n), and `[0, ..xs]` is the cheap direction that syntax should make easy. Order in a record update is pure notation — nothing is cheaper either way — and base-then-overrides is the only order under which "a later field wins" is true.

**The fields are checked exactly as an ordinary struct literal's are**, by the same validator: an unknown field name is a compile error, and each value is checked against the head's declared field type, so an interface-typed or type-parameter field stays as lenient here as in `Cfg{...}`. Two rules differ, and only in a patch: a *missing* field is never an error, because the spread supplies every field; and a bare `{…}` at a struct-typed field is itself a **patch**, applied recursively.

**The two forms are the same operation.** A spread patches deeply, exactly as `Struct.update`'s `Partial<self>` does, so these are one operation written two ways:

```nomi
moved = Struct.update(user, {address: {city: "NYC"}})
same = {..user, address: {city: "NYC"}}
```

Both leave `address.street` and `address.zip` in place. The nested-spread spelling is still legal and still equivalent:

```nomi
also_same = {..user, address: {..user.address, city: "NYC"}}
```

**A bare brace is a patch; everything else at a field position is a value.** A nominal literal (`Address{street: …, city: …, zip: …}`) *replaces* the field wholesale, a binding or a call is the value it evaluates to, and a nested spread is already a complete value of the field's type — so its own head is the base, not the enclosing field. That discrimination is syntactic rather than type-driven, because after checking, `{city: "NYC"}` and `Address{…}` can have the same type and the two lines must not mean the same thing.

**An interface-typed field takes values only.** You cannot patch through an interface that does not name its fields, so `{..stage, who: {n: 2}}` is `field 'who' of Stage: expected Speaker, got {n: Int}` while `{..stage, who: Quiet{n: 2}}` is accepted. `Partial<self>` stops in the same place, and it is forced rather than chosen. A field whose declared type is a type parameter patches once the argument is solved (`Box<Address>`) and takes a value while it is not (`Box<T>` inside a generic function).

**The depth exists only underneath a patch.** `Person{name: "Ada", address: {city: "NYC"}}` has no base for `address.street` to come from, so outside a spread the brace is a target-typed `Address` (see *Target-typed construction*) and reports `missing field 'street' of Address`. Under a spread the patch rule wins at every struct-typed field; everywhere else target typing applies.

**A diagnostic names the type the mistake is in.** `{..user, address: {zip: 10001}}` reports `Address has no field 'zip'`, and `{..user, address: {city: 10001}}` reports `field 'city' of Address: expected String, got Int` — the nested type and the nested field, not `Person.address`.

**Field punning works after a spread at any arity**, including one: `{..base, name}` means `{..base, name: name}`. A lone punned field without a spread does *not* pun — `{name}` re-parses as a block whose value is `name`, so the formatter writes `{name: name}` there. The spread removes that ambiguity structurally rather than by special case: a block's first token is never `..`.

Opacity applies as it does to a patch: `{..counter, value: 9}` from outside `Counter`'s defining file is rejected with the same diagnostic.

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

pub fn create(name: String, age: Int): User { ... } // file-level function — called as users.create(...)

impl Display for User {
    fn to_string(user: User): String { ... } // implementation function — called as User.to_string(...) / Display.to_string(...)
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

A variant payload is written as a type expression after the variant name, with no wrapping parens — same shape as a distinct-type declaration. Examples: `Number Int`, `Position (Int, Int)`, `Card {rank: Int}`, `Computation (Int) -> Int`. Construction (`Token.Number(42)`) and pattern matching (`case t { Number(n) -> ... }`) keep their parens / braces; only the declaration drops the construction-site parens.

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
Rectangle (Float, Float)    // payload is a tuple
Bag Map<String, Int>        // payload is a generic type
Computation (Int) -> Int    // payload is a function type
```

A single-payload variant carries one value. The payload type can be anything — primitive, tuple, struct, distinct, generic, or function type. If you want to bundle several values, spell the tuple explicitly (`Rectangle (Float, Float)`) or — usually nicer — name the bundle as a distinct or use a struct variant. Multi-positional declarations (`Rectangle Float, Float`) are not accepted; the parser rejects them with a hint pointing at the explicit tuple form. The payload syntax mirrors a distinct-type declaration (`type Rectangle (Float, Float)`).

**Struct variants** — named fields in braces, with optional defaults (the payload's anon-struct shape keeps its commas — only declaration *bodies* are newline-separated):

```nomi
Rectangle {width: Float, height: Float}
HttpError {status: Int, message: String, retryable: Bool = False}
```

**Embedded types** — a standalone struct or distinct type included as a variant via `embeds`. The type is defined independently and can appear in multiple enums:

```nomi
embeds Circle // struct
embeds UserId // distinct type
```

Only struct types and distinct types can be embedded. Embedding an interface (`embeds SomeIface`) is rejected by the checker — there is no meaningful subtyping relationship to install (open polymorphism over an interface would be `dyn Trait`-style dispatch, which Nomi does not support; see §13). For an interface-typed payload, use a single-payload variant: `Render Renderable`.

Embedded structs and embedded distinct types are types of their own and can be used as function parameter types (see "Variants as Parameter Types" below). Struct variants and embedded structs are constructed with a struct's two forms — braces, or the record call form `Shape.Rectangle({...})` — and matched with braces; single-payload variants use parens at construction and pattern sites (only the declaration drops the parens). §8 "Embedded Types" states the construction rule in full.

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

A variant constructor produces a value of the **enum type**, not a distinct variant type. This means `Shape.Circle{radius: 5.0}` has type `Shape`, `Some(42)` has type `Maybe<Int>`, and `True` has type `Bool`. There is no separate "variant type" to widen from — the value is already the enum type at the point of construction.

Variants must be **qualified** at construction sites — either as `EnumName.Variant` (`Shape.Circle(5.0)`) or as the dot-leading shorthand `.Variant` (`.Circle(5.0)`) when the expected type at the position determines the enum. Bare names like `Circle(5.0)` or `Point` are not legal; the qualification rule eliminates ambiguity when two enums share variant names (e.g. enums `BinaryOp` and `AssignOp` both having `Add` and `Sub` variants in the same file). The few prelude-promoted variants — `Some`, `None`, `Ok`, `Err`, `True`, `False` — are bare-callable everywhere because the standard library's `Maybe`, `Result`, and `Bool` enums are imported by default; user enums cannot opt into the prelude. See the *Variant resolution* subsection below for the full rule on when the dot-leading form resolves.

```nomi
shape = Shape.Circle{radius: 5.0} // embedded struct

shape = Shape.Rectangle{width: 3.0, height: 4.0} // struct variant

shape = Shape.Point // bare variant

err = Error.HttpError{status: 404, message: "not found"} // retryable defaults to False

err = Error.HttpError{status: 503, message: "unavailable", retryable: True}

x = Animal.Dog("Rex") // x has type Animal — no annotation needed

y = Some(42) // y has type Maybe<Int>          (prelude variant)

active = True // active has type Bool            (prelude variant)

// Dot-leading shorthand at typed positions:
s: Shape = .Circle{radius: 5.0} // expected type Shape from the annotation

c: Color = .Red // bare-payload form

paint(.Red) // expected type Color from paint's signature
```

This rule eliminates the need for typed bindings purely for type widening. You never need to write `x: Animal = Animal.Dog("Rex")` because `Animal.Dog("Rex")` is already an `Animal`. Embedded structs and embedded distinct types can also be used as **parameter types** for narrowing (see "Variants as Parameter Types" below). This is a function signature feature — callers still pass enum-typed values, and the runtime narrows at the call boundary.

When a file works heavily with one enum, an owner selector can lift specific variants into bare scope (see §21):

```nomi
import shape.Shape.{Circle, Point, Rectangle}

shape = Circle{radius: 5.0} // bare-callable after the owner selector import

shape = Point
```

**A variant's name never takes a type's place.** Because a same-file variant is never referenced bare, its name does not compete with the file's other names. A variant may share its name with a type, interface or function the same file declares, or with a name the file imports, in either source order, and that other name is what the bare spelling means: in type position, in an `impl` header, as the owner of a call, and as a value. The variant stays reachable as `Enum.Variant`, as `.Variant` and in patterns. No legal program is made ambiguous by this, because the only reference whose meaning changes is a bare same-file variant, which is an error either way. A variant imported with an owner selector is an import like any other, and two non-variant declarations of one name remain the error "'X' is already defined in this scope".

```nomi
import std/json.{Json, ToJson}

// The variant is Op.ToJson; bare ToJson is still the interface.
enum Op {
    ToJson
    Other
}

fn encode<T>(v: T): Json where T: ToJson {
    ToJson.to_json(v)
}
```

### Constructors as Function Values

A name that builds a value from one argument is a function value. A positional variant named without a call is its constructor: `Shape.Circle` is a `(Float) -> Shape`, and `Some` is a `(T) -> Maybe<T>`. So is a distinct type that wraps a value (§15 *Distinct Types*): `Id` for `type Id Int` is an `(Int) -> Id`. A value built through the function is the value the call builds: it compares equal and renders the same.

```nomi
enum Shape {
    Circle Float
    Segment (Int, Int)
    Point
}

circles = Iter.map([1.0, 2.5], Shape.Circle) |> Iter.to_list()

somes: List<Maybe<Int>> = Iter.map([1, 2], Some) |> Iter.to_list()
```

- **A generic constructor is instantiated as a generic function used as a value is** (§5): its type parameters are solved from the function type its position expects. `Iter.map(ns, Some)` solves `T` from the elements. `Ok` and `Err` leave the other parameter to the result, so `Iter.map(ns, Ok)` needs an annotated destination (`rs: List<Result<Int, String>> = Iter.map(ns, Ok) |> Iter.to_list()`) or an annotated function (`f: (String) -> Result<Int, String> = Err`). With nothing to solve it from, `f = Some` is the error "cannot infer type parameter T of generic constructor 'Some' used as a value".
- **A variant over a tuple takes the tuple.** A variant has one payload, so its function takes one argument. `Shape.Segment` is a `((Int, Int)) -> Shape`, and `Iter.map(pairs, Shape.Segment)` builds one from each tuple. The flat call `Shape.Segment(1, 2)` is a call form of that one tuple, not a second parameter list, and a tuple-distinct (`type Pair (Int, String)`) works the same way.
- **A payload-free variant is a value, not a function.** `Shape.Point` is a `Shape`.
- **A struct-shaped variant is not a function value**, as a struct is not: `Iter.map(records, Shape.Rectangle)` is an error naming the brace and record forms.
- **`.Variant` is not a function value.** A dot-leading variant resolves only where the expected type is its enum (*Variant Resolution* below), and a function type is not an enum: `f: (Float) -> Shape = .Circle` is an error whose hint names `Shape.Circle`.
- **An opaque type's constructor stays private.** Naming it as a value outside its file is the same error as calling it.

### Pattern Matching

Patterns inside a `case` arm use the dot-leading form `.Variant` or the fully-qualified form `Shape.Variant`. Bare variant names are rejected for non-prelude variants — the rule is symmetric with construction. The dot-leading form is the idiomatic choice; qualified is the disambiguation/explicit alternative.

```nomi
case shape {
    .Circle{radius} -> radius * radius * 3.14
    .Rectangle{width, height} -> width * height
    .Point -> 0.0
}

case err {
    .HttpError{status, retryable: True} -> retry(status)
    .HttpError{status, message} -> log(message)
    .Timeout -> retry_later()
    .ConnectionRefused -> fail()
}
```

Qualified pattern forms (`Shape.Circle{radius} -> ...`) are also accepted when disambiguation is wanted or when the reader prefers the explicit form. Prelude variants (`Ok`, `Err`, `Some`, `None`, `True`, `False`) stay bare via the prelude scope export and are not rewritten through the dot-leading rule.

### Variant Resolution

A `.Variant` expression (or `.Variant(payload)` / `.Variant{fields}` / `.Variant[elements]`) resolves to the variant `Variant` of enum `E` when, at the expression's syntactic position, the **expected type** — as determined by the checker's standard outside-in walk from the position to the enclosing declaration — is `E`. The walk passes through transparent constructs (`case`/`if-else` arm result positions, block-expression returns, `return` statements, list, tuple and map literal elements, and generic-function argument positions whose `T` is constrained by the call's return type against an outer expected type, including the head of a pipe into such a call). The constraint is solved before the arguments are checked, so it reaches a variant nested anywhere inside them: `exits: Map<Direction, Place> = Iter.to_map([(.North, .Cave)])` checks the list against `Iter<(Direction, Place)>`. A list, vector or set literal checked against `Iter<T>` checks each item against `T`, as it would against `List<T>`. A **generic variant constructor** is exactly such a position: `Ok(.Quit)` in a `fn (): Result<Input, String>` resolves `.Quit` to `Input.Quit`, because `Ok`'s payload param is pinned from `Ok`'s `Result<T, E>` return against the outer `Result<Input, String>` — and likewise inside a `case`/`if` arm, an annotated binding, or an argument slot. It does **not** look at later uses, unannotated `=` bindings, a bare `.Variant` standing alone as a **pipe stage** (`… |> .Guess() |> Ok()`, where the enum would have to be read from the downstream `Ok()`), or any other site that would require backward inference. This matches Nomi's broader explicit-types philosophy — the same outside-in walk the checker already does for ordinary type checking, reused for variant resolution.

Concrete consequences:

| Site | Resolves? |
|---|---|
| `Box{color: .Red}` | ✓ field type is `Color` |
| `paint(.Red)` (paint takes Color) | ✓ param type is `Color` |
| `Iter.sort(xs, .Descending)` | ✓ param type is std/comparable's `Direction`; the calling file need not import it |
| `Iter.sort_with(xs, \|a, b\| .Equal)` | ✓ the lambda's result is checked against the callback's declared result, `Ordering`; a declared result holding a type parameter supplies no enum |
| `paint(case x { 1 -> .Red; _ -> .Blue })` | ✓ propagates through case arms |
| `palette: List<Color> = [.Red, .Green]` | ✓ element type from annotation |
| `themes: Map<String, Color> = {"a" => .Red}` | ✓ map value type from annotation |
| `c: Color = .Red` | ✓ binding annotation |
| `c: Color = id(.Red)` (generic `id<T>(x: T): T`) | ✓ annotation pins `T` through `id`'s return |
| `pair: (Color, Size) = (.Red, .Large)` | ✓ element types from annotation |
| `exits: Map<Direction, Place> = Iter.to_map([(.North, .Cave)])` | ✓ annotation pins `K` and `V` before the list is checked against `Iter<(K, V)>` |
| `exits: Map<Direction, Place> = [(.North, .Cave)] \|> Iter.to_map()` | ✓ the pipe's head is checked against the stage's pinned first parameter |
| `Ok(.Quit)` in `fn (): Result<Input, String>` | ✓ generic constructor's payload pinned from the return |
| `Ok(.Guess(n))` in a `case`/`if` arm | ✓ expected type flows from the arm |
| `c: Color = if cond { .Red } else { .Blue }` | ✓ annotation propagates through if |
| `pub fn default_color(): Color { .Red }` | ✓ annotated return position |
| Patterns: `case (jv: Json) { .Obj{...} -> ... }` | ✓ scrutinee's type is enum |
| `c = .Red` (unannotated binding) | ✗ no enclosing expected type |
| `id(.Red)` alone (statement position) | ✗ `T` unconstrained |
| `c = id(.Red); paint(c)` | ✗ would require backward flow from `paint` |
| `… \|> .Guess() \|> Ok()` (bare `.Variant` pipe stage) | ✗ would require backward flow from the next stage — qualify it (`Input.Guess()`) |
| `paint(.NotAColor)` (Color has no `NotAColor`) | ✗ enum found, variant absent |
| `f(.Red)` where `f` takes `Box` | ✗ expected type isn't an enum |

**Prelude carve-out.** The prelude variants `Ok`, `Err`, `Some`, `None`, `True`, `False` remain reachable as bare names through the prelude's scope export, not through variant resolution. Calling `Ok(user)` or matching `case r { Ok(v) -> ... }` is ordinary name resolution. The dot-leading rule and prelude bare-name reachability are independent paths — they never collide because the prelude exports don't conflict with the dot form (both `Ok(user)` and `.Ok(user)` work; the bare form is idiomatic for prelude variants).

**Diagnostics.** Three failure modes:

- *No determinable enum type at this position.* The position has no annotation/signature to walk to, and no enclosing call/case/binding determines an enum. Example: `c = .Red` with `c` never used in a typed position. Resolution: annotate the binding, or qualify the variant.
- *Expected type isn't an enum.* The walk found an expected type, but it's not an enum. Example: `b: Box = .Red`. Resolution: check the binding's type, or qualify.
- *No such variant on the enum.* The expected type is an enum, but the named variant doesn't exist on it. Example: `c: Color = .Purple`. Resolution: pick a valid variant.

**Struct literals use the same walk.** A brace literal with no type name and no spread at a position whose expected type is a nominal struct builds that struct: `Box{color: .Red}` resolves `.Red` from the field type, and `Person{name: "Ada", address: {street: "1 Main", city: "Bath"}}` builds an `Address` from the field type the same way. The positions are this section's, and so are the limits: an unannotated binding has no expected type, so `a = {street: "1 Main", city: "Bath"}` is an anonymous struct. See *Target-typed construction*.

### Field Accessors

`.name` is a function that reads field `name` from its argument. A lower-case name after the leading dot is a field; a capitalized one is a `.Variant`. A chain `.address.city` reads `address`, then `city` from it, and a tuple index reads an element (`.0`, `.1`).

```nomi
names = users |> Iter.map(.name) |> Iter.to_list()
oldest = Iter.sort_by(people, .age) |> Iter.last()
cities = users |> Iter.map(.address.city) |> Iter.to_list()
seconds = pairs |> Iter.map(.1) |> Iter.to_list()
```

An accessor stands only where a function is expected, and it resolves by the same outside-in walk as a `.Variant`. The expected type must be a function type `(S) -> R` of one parameter, and S must be known at the accessor's position: an annotation, a declared parameter type, or a generic parameter that an earlier argument or the piped value has already pinned. S must be a struct, an anonymous struct or a tuple (or an interface or bounded type parameter, through its `field` requirements). The accessor's type is `(S) -> F`, where F is the type of the last field read, and R unifies with F as a lambda's result would. In `Iter.map(users, .name)` the first argument pins `T` to `User` before `.name` is checked, so `.name` is `(User) -> String` and the call returns `Iter<String>`.

A generic struct is read at its instantiation: over a `List<Box<Point>>`, `.value` is `(Box<Point>) -> Point`. A field that holds a function is returned, not called: with `handler: (Request) -> Response`, `.handler` is `(Server) -> (Request) -> Response`.

As an argument, an accessor routes like a trailing lambda, so `Iter.sort_by(people, .age)` fills `key` past the defaulted `direction`.

| Site | Accepted? |
|---|---|
| `Iter.map(users, .name)` | ✓ `users` pins the parameter type first |
| `users \|> Iter.map(.name)` | ✓ the piped value pins it |
| `get: (User) -> String = .name` | ✓ the annotation gives it |
| `fn name_of(): (User) -> String { .name }` | ✓ the return type gives it |
| `user \|> .name` | ✗ a pipe stage; write `user.name` |
| `get = .name` | ✗ no function type is expected |
| `apply(.name, user)` (`fn apply<A, B>(f: (A) -> B, x: A): B`) | ✗ `A` is not known until a later argument |
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
        User{name: "Ada", age: 36, address: Address{city: "London"}},
        User{name: "Alan", age: 41, address: Address{city: "Wilmslow"}},
    ]
    io.print(users |> Iter.map(.name) |> Iter.to_list())
    io.print(Iter.sort_by(users, .age) |> Iter.map(.address.city) |> Iter.to_list())
    age: (User) -> Int = .age
    io.print(Iter.map(users, age) |> Iter.to_list())
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
fn area(circle: Circle): Float {
    3.14 * circle.radius * circle.radius
}

// Both are valid Shape values
shapes: List<Shape> = [Circle{radius: 5.0}, Shape.Rectangle{width: 3.0, height: 4.0}]
```

> **Status: not yet implemented.** A struct variant declared inline
> (`Rectangle {width: Float, height: Float}`) is not a type:
> `fn perimeter(rect: Rectangle)` and `fn perimeter(rect: Shape.Rectangle)`
> are both `unknown type` errors. Declare the payload as a struct and
> `embeds` it when a function needs to take that one variant. Track via
> `feature-status.md`.

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
c = Circle{radius: 5.0}

fn area(c: Circle): Float {
    3.14 * c.radius * c.radius
}

// And as a variant of either enum
s: Shape = Circle{radius: 5.0} // s is a Shape

d: Drawable = Circle{radius: 5.0} // d is a Drawable

// Distinct types work the same way
id: Identifier = UserId("abc") // id is an Identifier
```

**Subtype coercion.** Embedded types are subtypes of their containing enum. A value of type `Circle` (where `Shape` has `embeds Circle`) is also a value of type `Shape`, and flows into `Shape`-typed positions — function arguments, list elements, return values, bindings — without explicit `Shape.Circle(...)` wrapping:

```nomi
fn render(s: Shape): String { ... }

c = Circle{radius: 5.0}
render(c)                                              // Circle -> Shape arg

shapes: List<Shape> = [c, Circle{radius: 1.0}, Shape.Point]
                                                       // each element coerces independently

uid = UserId("alice")
ids: List<Identifier> = [uid, UserId("bob"), Identifier.Anonymous]
```

The qualified construction form (`Shape.Circle{radius: 5.0}`, `Identifier.UserId("alice")`) is still available — useful when type inference can't see the target enum, or when the reader benefits from the explicit variant tag. Pattern matching is unchanged: `case s { Shape.Circle{radius} -> ... }` destructures embedded structs the same way regardless of how the value flowed in.

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
a = Shape.Circle{radius: 2.0}
b = Shape.Circle({radius: 2.0})
c = Shape.Circle({})                        // Circle{radius: 1.0}
d = {height: 3.0} |> Shape.Rectangle()      // Rectangle{width: 2.0, height: 3.0}
e = Shape.Rectangle(dimensions)             // a record value, as `Circle(rec)` takes one

// An embedded wrapping distinct: the distinct's call form, from its inner value.
f = Session.UserId("alice")                 // builds a UserId
g = "bob" |> Session.UserId()

// An embedded zero-sized type: the value, with no arguments.
h = Session.Expired
```

A struct has no positional call form, so neither does a struct-shaped variant: `Shape.Rectangle(4.0, 5.0)` is an error naming the brace and record forms. A struct has no function value either, so a struct-shaped variant is not one: `f = Shape.Rectangle` and `Iter.map(records, Shape.Circle)` are errors, and a lambda that builds the variant (`|r| Shape.Rectangle(r)`) is the function. A positional variant (`Iter.map(xs, Shape.Dot)`) and an embedded wrapping distinct (`Session.UserId`) are functions of their payload and remain values. Passing a value that is already a V is not construction and is rejected too — `Shape.Circle(c)` with `c: Circle`, or `Session.UserId(uid)` with `uid: UserId` — because such a value widens on its own at any position that wants the enum (`s: Shape = c`, `area(c)`). `Session.Expired()` is rejected: the variant takes no arguments.

The built value is the same value as V widened into the enum. `Session.UserId("alice") == (w: Session = UserId("alice"))` is `True`, both render `UserId("alice")`, and both match `Session.UserId(name)` with `name` bound to the inner `"alice"`.

**Dispatch follows the enum.** A value in a position typed as the enum is a value of the enum, however it got there: built through the variant, or widened by a binding (`s: Shape = c`), an argument, a result, a field or a list element. Interface calls on it run the enum's impls, not the embedded type's: `Display.to_string(s)`, `Debug.inspect(s)`, and `==` and `<` through `Equatable` and `Comparable`. The same value typed as the embedded type (`c: Circle`) runs the embedded type's impls. An enum's automatic `Debug` and its derived impls hand an embedded variant to the embedded type's impl, which is why both values above render `UserId("alice")`.

**Variant payload shape.** The variant's payload type — what the constructor takes and what the pattern destructures — depends on which kind of type is embedded:

- **Struct embed** (`embeds Circle`): the payload is the struct itself. `case s { Shape.Circle{radius} -> ... }` destructures the struct fields.
- **Wrapping-distinct embed** (`embeds UserId` where `type UserId String`): pattern destructure binds the *inner* value directly: `case id { Identifier.UserId(name) -> ... }` binds `name: String`. Qualified construction is the distinct's own call form, `Identifier.UserId("alice")`, which builds a `UserId` from the inner String; the value is a `UserId`, equal to `UserId("alice")` widened into `Identifier`.
- **Zero-sized distinct embed** (`embeds Expired` where `type Expired`): the payload is the (zero-sized) distinct. The variant takes no arguments — `Session.Expired` is field-access, not a call.

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
fn some?(maybe: Maybe<T>): Bool
fn none?(maybe: Maybe<T>): Bool
fn map(maybe: Maybe<T>, f: (T) -> U): Maybe<U>
fn flat_map(maybe: Maybe<T>, f: (T) -> Maybe<U>): Maybe<U>
fn with_default(maybe: Maybe<T>, default: T): T
fn to_result(maybe: Maybe<T>, error: E): Result<T, E>
```

### Result Functions

Owner functions on `Result`, called `Result.map(r, f)` and so on:

```nomi
fn ok?(result: Result<T, E>): Bool
fn err?(result: Result<T, E>): Bool
fn map(result: Result<T, E>, f: (T) -> U): Result<U, E>
fn map_err(result: Result<T, E>, f: (E) -> F): Result<T, F>
fn flat_map(result: Result<T, E>, f: (T) -> Result<U, E>): Result<U, E>
fn with_default(result: Result<T, E>, default: T): T
fn to_maybe(result: Result<T, E>): Maybe<T>
```

### The `try` Keyword

Works on both `Result` and `Maybe`. A prefix keyword that unwraps the success value or returns early from the nearest function boundary (the enclosing `fn` or lambda). A `once` initializer and a parameter or field default have no such boundary, and a `try` written directly in one is a compile error (§2, *`once` Bindings*). `try x` is sugar for a pattern match with `return`:

```text
try x  ≡  case x { Ok(v) -> v, Err(e) -> return Err(e) }
```

(and similarly for `Maybe`: unwrap `Some`, or `return None`).

```nomi
// With Result: unwrap Ok or return Err
fn process(id: Int): Result<User, String> {
    user = try users.get(id)
    profile = users.get_profile(user)
    try email.send(profile)
}

// With Maybe: unwrap Some or return None
fn find_name(id: Int): Maybe<String> {
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
fn decode_len(s: String): Result<Int, String> {
    parsed = try Json.decode(s)

    Ok(String.length(Json.encode(parsed)))
}

// ACCEPTED — the error is converted AT THE SITE, so the declared type
// describes every value the function can produce.
fn decode_len(s: String): Result<Int, String> {
    parsed = try Result.map_err(Json.decode(s), |e: Json.DecodeError| Display.to_string(e))

    Ok(String.length(Json.encode(parsed)))
}
```

There is no implicit conversion at the `try` boundary — no `From`-style widening, no coercion to a common supertype. `Result.map_err` is the conversion, and it is written where the mismatch is. The one relaxation is the ordinary one: an INTERFACE-typed declared error side accepts a concrete implementer of that interface, exactly as an interface-typed parameter does.

At a boundary without a declared return type — a `concurrent` block or an unannotated lambda — the error side of the result is **inferred from the `try` sites** inside it. A block whose tail is `Ok(...)` pins only the success type; the error type comes from the `try`-unwrapped `Result`s, so this needs no annotation:

```nomi
// outcome: Result<Int, String> — the String is inferred from `try Task.await(t)`
outcome = concurrent {
    t = Task.spawn(|| fetch()) // fetch(): Result<Int, String>
    Ok(try Task.await(t))
}
```

`try` sites that unwrap `Result`s with incompatible error types in the same boundary are rejected (the boundary's error type would be ambiguous). A `try` on a `Maybe` contributes no error type, so `Maybe`-tailed boundaries are unaffected.

Clause 3 therefore does not arise at an inferring boundary — the error type IS the `try` sites' — but clauses 1 and 2 still do, and they are checked against the result the body PRODUCED rather than against a declaration:

```nomi
// REJECTED — the lambda's result is Int, so the propagated Err has
// nowhere to go and the caller would receive it where it expects an Int.
//   `try` cannot propagate a Result out of a lambda whose result is Int
_ = |x: Int| {
    v = try parse(x) // parse(x): Result<Int, String>

    v
}
```

## 10. Pattern Matching

`case` keyword with `->` arrows. Two forms:

### Matching on a value (destructuring)

```nomi
case shape {
    .Circle{r} -> 3.14 * r * r
    .Rectangle{w, h} -> w * h
    .Point -> 0
}
```

Variant patterns use the dot-leading form (`.Circle`) or the fully-qualified form (`Shape.Circle`); bare variant prefixes (`Circle`) are rejected for non-prelude variants. The qualifier of the fully-qualified form must name the scrutinee's enum (or a module-qualified or imported spelling of it): an unknown name is an error (`C6lor.Blue`: `unknown type "C6lor"`), and so is another type, even one with a variant of that name (`Mood.Blue` against a `Color`). The dot-leading prefix resolves against the scrutinee's enum type — the same outside-in walk described in §8's *Variant Resolution* subsection, with the case's scrutinee as the determining position. Prelude variants (`Ok`, `Err`, `Some`, `None`, `True`, `False`) remain bare-callable in patterns via the prelude scope export.

A literal pattern (`3`, `-1`, `2.5`, `1.50d`, `"a"`, `'a'`) has its literal's type, and the value it is matched against must have that same type, at any depth: a `case` arm, a variant payload, a tuple element, a struct field, a list element, a map value, an `assert` pattern or a binding's `else` pattern. A literal never converts, so `case n { "a" -> ... }` over an `Int` is the compile error `pattern "a" is a String, but the value is an Int`, and `3` does not match a `Float` (write `3.0`).

### Ad hoc conditionals (no value)

```nomi
case {
    x == 0 -> "zero"
    x > 0 -> "positive"
    _ -> "negative"
}
```

`_` in patterns means "ignore this value."

A no-payload pattern (`None`, `True`, `.Point`) matches only variants that carry no data. Data-carrying variants must spell their payload explicitly — `Some(_)`, `Err(_)`, `.Rectangle{_, _}` — so the reader sees the shape of what the variant actually holds:

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
    .Circle{r} -> r
    .Rectangle{w, h} -> w * h
}
```

Use `_` as a catch-all when you don't need to handle every variant individually:

```nomi
case shape {
    .Circle{r} -> r
    _ -> 0.0
}
```

Every `case` must be exhaustive: its unguarded arms together must match every value of the scrutinee's type. The compiler checks this component by component:

- `_` and a bare binding (`n -> ...`) match everything.
- An enum is covered when every variant is, and a variant is covered when the arms naming it cover its payload. `Some(Ok(_))`, `Some(Err(_))` and `None` together cover a `Maybe<Result<T, E>>`.
- A tuple, struct or distinct value is covered when its components are, taken together. `(Some(_), _)`, `(None, Some(_))` and `(None, None)` cover a `(Maybe<A>, Maybe<B>)`; a struct pattern that only binds its fields covers the struct.
- A list is covered by length: `[]` and `[head, ..tail]` cover every list, while `[]` and `[x]` miss lists of two or more.
- A literal (`0`, `"a"`, `'x'`), a string prefix (`"a" + rest`) or a map pattern never covers its type, since the compiler does not enumerate the values of `Int`, `Float`, `Decimal`, `String`, `Byte`, `Codepoint` or `Map`. Where one of those decides the match, some arm needs `_` or a binding in that position.
- An arm with a `when` guard never counts toward coverage.

A subject-less `case` (§*Ad Hoc Conditionals*) needs an unguarded `_` arm.

A `case` that misses something is a compile error. When the compiler can name a missing value it does: `non-exhaustive case on Shape: missing Rect` for an enum, and `non-exhaustive case on (Maybe<Int>, Maybe<Int>): missing (None, None)` or `non-exhaustive case on List<Int>: missing [_, _, ..]` for the rest. When the only thing missing is a value no literal list can cover, as in `case (n, n) { (0, 0) -> "zero" }`, the error is `non-exhaustive case: add a `_` arm`.

### Guards

Use `when` to add conditions to a pattern. `or` between patterns matches either:

```nomi
case shape {
    .Circle{r} when r > 10.0 -> "big circle"
    .Circle{r} when r > 0.0 and r < 100.0 -> "valid circle"
    .Circle{r} or .Square{r} -> "has radius ${r}"
    _ -> "other"
}
```

A guard is a Bool expression: `n when n + 1 -> ...` is the error `` `when` guard must be Bool, got Int ``.

`or` between patterns requires all patterns to bind the same variables.

> **Status: not yet implemented.** `when` guards work; `or` between patterns
> does not. The parser reports `expected '->' in case branch` at the `or`,
> for both payload-carrying and bare variants. The feature needs semantics
> nothing else in the language needs: binding-set equality
> across alternatives (the rule above), an interaction with variant
> exhaustiveness, an answer for `when` combined with `or`, and a
> decision-tree shape in the IR. Track via `feature-status.md`.

### Struct Destructuring

Partial matching — only specify the fields you care about. Field punning works here too — `User{name}` binds the field value to a variable called `name`:

```nomi
case user {
    User{name: "Alice"} -> "found Alice"
    User{name, age} when age > 18 -> "adult: ${name}"
    User{age} when age > 18 -> "adult"
    _ -> "minor"
}

// mix punning and explicit binding
case user {
    User{name, age: user_age} -> "${name} is ${user_age}"
}

// type name optional when not matching enum variants
case user {
    {name, age} when age > 18 -> "adult: ${name}"
    _ -> "minor"
}
```

A written type name must name the value's struct, directly or through a `typealias`; any other name is a compile error at the pattern. A typed pattern does not match an anonymous struct: `User{name} = {name: "Ada"}` is an error, and `{name} = {name: "Ada"}` destructures it.

### Type Narrowing

When pattern matching on an enum, the matched value's type is narrowed to the variant's type within that branch. This applies to embedded types and struct variants:

```nomi
fn describe(value: Shape): String {
    case value {
        .Circle{radius} -> Circle.to_string(value) // value is Circle here
        .Rectangle{width, height} -> "${width} x ${height}"
        .Point -> "Point"
    }
}
```

Inside the `Circle` branch, `value` is narrowed from `Shape` to `Circle`. You can pass it to any function expecting a `Circle`, or call `Circle`'s interface implementations on it.

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

### Map Destructuring

Maps can be destructured in patterns using the same `{key => pattern}` syntax as map construction. A key is an ordinary expression evaluated in the branch scope, exactly like a map-construction key — so **any keyable value matches** (variants, tuples, lists, structs, distinct types, …), not just scalar literals, and computed keys are allowed (`{x => v}` matches the entry whose key equals the current value of `x`). Matching is partial — extra keys in the map are ignored (consistent with struct patterns):

```nomi
config = {"host" => "localhost", "port" => 8080, "debug" => True}

case config {
    {"debug" => True, "port" => p} -> "debug on port ${p}"
    {"port" => p} -> "port ${p}"
    _ -> "no port"
}

// literal value matching
case headers {
    {"content-type" => "application/json"} -> parse_json(body)
    {"content-type" => ct} -> parse_other(ct, body)
    _ -> Err("no content type")
}

// integer keys
case grid {
    {0 => first_row} -> first_row
    _ -> []
}

// wildcard values — check key exists without binding
case env {
    {"DATABASE_URL" => _} -> "db configured"
    _ -> "no db"
}

// non-literal keys: any keyable value, including computed ones
case scores {
    {(1, 2) => v} -> v // tuple key
    {Color.Red => v} -> v // variant key
    {target => v} -> v // computed — matches the entry equal to `target`
    _ -> 0
}
```

Map destructuring also works in bindings. All specified keys must exist or it's a runtime error:

```nomi
config = {"host" => "localhost", "port" => 8080}

{"host" => host, "port" => port} = config

io.print(host) // "localhost"

io.print(port) // 8080

// missing key is a runtime error
{"missing" => x} = config // runtime error: key missing not found in map
```

Map patterns are refutable — a `_` catch-all is required for exhaustiveness unless the map type is known to have exactly the matched keys.

### Distinct Type Destructuring

Distinct types over tuples and maps mirror their construction syntax — both literal-attach and call-form patterns are accepted. Pick whichever matches how the value was built:

```nomi
type Pair (Int, String)
type Kvs Map<String, Int>

p = Pair(1, "hello")

kv = Kvs{"a" => 1, "b" => 2}

// tuple-distinct: flat destructure or nested
case p {
    Pair(a, b) -> "${a}, ${b}" // flat — equivalent to Pair((a, b))
    Pair((a, b)) -> "${a}, ${b}" // call-form — same effect
}

// map-distinct: literal-attach or call-form
case kv {
    Kvs{"a" => v} -> v // literal-attach
    Kvs({"b" => v}) -> v // call-form
}
```

For tuple-distinct over arity ≥ 2, `Pair(a)` is **not** a flat destructure — it binds the entire inner tuple to `a` (since 1-tuples don't exist, a single-pattern arg can only mean "the whole inner value"). Use `Pair(a, b)` for the flat form.

### String Prefix Patterns

Strings can be destructured in patterns using `+`, the same operator used for string concatenation. Just as `User{name: n}` in a pattern destructures what `User{name: "Alice"}` constructs, `"prefix" + rest` in a pattern destructures what `"prefix" + rest` would concatenate. Construction and destructuring use the same syntax.

Only prefix matching is supported — the left side must be a string literal. `+` prefix patterns are string-only — lists use `..` for destructuring (see List Destructuring above). String prefix patterns are only allowed in `case` expressions, not in bindings, because they are always refutable (the string may not match the prefix):

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

Ok((width, height)) = parse_size(text) else { (80, 24) }

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
- A fallback is allowed only when the pattern names one variant with a payload, `Variant(inner)` or `.Variant{fields}`, whose own pattern always matches. The fallback's type is the payload's (for a struct-shaped variant, the anonymous struct of its fields), and the inner pattern binds from it exactly as it would from a match: `Some(email) = m else { "none" }` binds `email` to `"none"`, and `Ok((w, h)) = r else { (80, 24) }` binds `w` to 80 and `h` to 24. A fallback of another type is the error `the fallback stands in for Some's payload of type String, got Int`.
- A pattern with no single payload to stand in for — a list pattern such as `[first, ..rest]`, a nested refutable pattern such as `Ok(Some(x))`, a literal, a tuple with a refutable part — requires every path to leave. A path that produces a value is the error `this pattern has no single payload a fallback could stand in for; every path through `else` must return, break or continue`.
- A pattern that always matches takes no `else`: `(x, y) = pair else { ... }` is the error `this pattern always matches; remove the else`.
- A pattern that can fail requires one. `Some(n) = maybe` alone is the error `this pattern can fail to match a value of type Maybe<Int>; add `else { ... }` to handle a value it does not match, or match it with `case` or `if Pattern = expr``.
- `try` inside the `else` behaves as it does anywhere: it exits the enclosing function or lambda. `return` inside a lambda's `else` exits the lambda.

```nomi
import std/io

enum Check {
    Valid {addr: String, score: Int}
    Invalid String
}

fn label(check: Check): String {
    .Valid{addr, score} = check else {
        .Invalid(_) -> {addr: "unknown", score: 0}
    }

    "${addr}: ${score}"
}

fn first_word(words: List<String>): String {
    [first, .._rest] = words else { return "(none)" }
    first
}

fn main() {
    io.print(label(Check.Valid{addr: "a@b", score: 3})) // a@b: 3
    io.print(label(Check.Invalid("bad"))) // unknown: 0
    io.print(first_word(["hello", "world"])) // hello
    io.print(first_word([])) // (none)

    Iter.each(["1", "x", "3"], |text| {
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
fn process(items: List<Item>): Result<Summary, String> {
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
total = Iter.reduce(items, |acc = 0, item| acc + item.value)

// side effects (terminal)
Iter.each(items, |item| io.print(item.name))

// iterating a map (each step is a (key, value) tuple)
Iter.each(ages, |(k, v)| io.print("${k}: ${v}"))
```

The one rule: **generic over any iterable → `Iter.X`; specific to a container's structure → that container's owner.** A `List` back from a `List` is always the explicit `Iter.map(xs, f) |> Iter.to_list()` — the lazy adapter plus a visible materialize step is what makes the cost (one allocation) explicit at the call site. Container-specific ops that read or rebuild structure live on the relevant owner: `Map.map_values`, `Set.union`, `List.concat`, `Vector.at`, `String.split`, `Map.size`/`Set.size`, and native `String.reverse`.

### `break` and `continue` in Callbacks

`break` and `continue` work inside iteration callbacks — the callbacks passed to the closed, analyzer-enforced set of iteration functions: the iter-family higher-order functions plus `Iter.loop`. `break` or `continue` anywhere outside an iteration callback is a compiler error — there's no iteration context.

The keywords are the entire surface of the iteration control protocol. A callback's bare last expression means "here's my result for this element," `continue` means "skip this element," `break` means "stop iterating" (`break value` means "stop, producing this value"). These lower to a compiler/runtime-internal signal that the iteration machinery interprets — the signal has no surface spelling: it is not a Nomi type, and there is nothing to import, construct, or pattern-match.

The `break` / `continue` / `return` keywords are valid as `case`-arm bodies, not just block statements — they unwind to the enclosing loop / lambda exactly as they do elsewhere, so a callback can dispatch with `case` and break or continue from an arm:

```nomi
Iter.loop(|sum = 0|
    case Receiver.receive(ch.receiver) {
        Some(v) -> sum + v // bare expr: next state, loop again
        None -> break sum // stop, loop value = sum
    }
)
```

**`continue`** — skip this element, never takes a value:

```nomi
// Skip inactive items
Iter.each(items, |item| {
    if !item.active { continue }
    process(item)
})

// Skip invalid items in reduce — accumulator unchanged
total = Iter.reduce(items, |total = 0, item| {
    if item.invalid { continue }
    total + item.value
})

// Skip in map — element excluded from output
names =
    Iter.map(users, |u| {
        if u.inactive { continue }
        u.name
    })
    |> Iter.to_list()
```

**`break`** — stop iteration, return what's accumulated so far. `break value` is `return value` *then* stop — `value` plays the same per-operation role it would as a normal callback result (the accumulator for `reduce`, the emitted element for `map`, the keep/drop decision for `filter`):

```nomi
// Buy items until budget is exceeded — don't include the one that busted it
(cart, spent) = Iter.reduce(items, |acc = ([], 0.0), item| {
    (cart, spent) = acc
    new_spent = spent + item.price
    if new_spent > budget { break }
    ([item, ..cart], new_spent)
})

// Map with early exit — include the last one, then stop
names =
    Iter.map(users, |u| {
        if u.is_last { break u.name }
        u.name
    })
    |> Iter.to_list()

// Stop without including current element
names =
    Iter.map(users, |u| {
        if u.inactive { break }
        u.name
    })
    |> Iter.to_list()
```

**`loop`** follows the same rules:

```nomi
// continue — retry
Iter.loop(||
    case acquire_lock(handle) {
        Ok(lock) ->
            break lock

        Err(_) -> {
            timer.sleep(Duration.milliseconds(100))
            continue
        }
    }
)

// break — stop, return Unit
Iter.loop(||
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
)

// break value — stop and return a value
result = Iter.loop(|attempts = 0|
    case fetch(url) {
        Ok(data) -> break Ok(data)
        Err(_) when attempts >= 3 -> break Err("gave up")
        Err(_) -> attempts + 1
    }
)
```

### Named Functions as Callbacks

Named functions with plain return types pass directly as callbacks — a `(T) -> R` function is exactly what an iteration function expects:

```nomi
fn double(x: Int): Int {
    x * 2
}

// These are equivalent:
doubled = Iter.map(numbers, |x| x * 2) |> Iter.to_list()

doubled = Iter.map(numbers, double) |> Iter.to_list()
```

A named function may use `break` and `continue` in its body; the return type stays the plain value type:

```nomi
fn sum_valid(total: Int, value: Int): Int {
    if value < 0 { continue }
    if total > 1000 { break total }
    total + value
}

result = Iter.reduce(values, sum_valid)
```

A named function supplies no initial accumulator, so `Iter.reduce` starts from the first element and the accumulator has the element's type; `Iter.reduce(items, f)` with `f: (Int, Item) -> Int` is a compile error. To start from another value or type, pass a lambda whose accumulator has a default (`|total = 0, item| ...`).

Using the keywords makes the function **iter-sensitive**: the analyzer requires every call site to be an iter-callback position (passed as the callback to an iteration function, as above). Calling an iter-sensitive function directly — outside any iteration context — is a compiler error, because `break`/`continue` would have no iteration to control.

Summary:

- `continue` — skip this element. Never takes a value: `continue <expr>` is a compile error. In `Iter.loop`, `continue` goes round again with the state unchanged; to go round with a new state, make it the callback's result.
- `break` — stop, return what's accumulated so far.
- `break value` — `return value` then stop: `value` is this element's result for the operation (the accumulator for `reduce`, the emitted element for `map`, the keep/drop decision for `filter` — `break True` keeps the element, `break False` drops it), then iteration stops.
- `break` and `continue` control the iteration. `return` exits the nearest function boundary (the callback lambda).
- Same semantics everywhere — no special cases for specific functions: `break value` is uniformly `return value` then stop, and a callback result already means whatever that operation makes of it (output element, accumulator, decision) on the normal path too.

### `loop`

`loop` lives on `Iter` in `std/iter` — the canonical spelling is `Iter.loop(...)`, and `import std/iter.Iter.{loop}` restores the bare form. It sits beside the iterator algorithms because it speaks the same `break`/`continue` callback protocol, even though it has no collection input and threads state instead. The callback takes the current state and returns the next state. Lambda default parameters provide the initial state:

```nomi
countdown = Iter.loop(|n = 5| {
    if n == 0 { break n }
    n - 1
})
```

The callback's type is `(S) -> S` — one parameter for the current state, returning the next state. The next state is the callback's result, never an operand of `continue`: bare `continue` repeats with the state unchanged, and `continue (i + 1, s)` is rejected rather than read as a new state. The default value (`n = 5`) provides the initial state and pins the type `S`. This is the same pattern as `Iter.reduce`, where the accumulator parameter's default provides the initial value.

`break value` exits the loop and produces the final value. The state type and the break type are the same type `S` — `break "hello"` inside `Iter.loop(|n = 5| …)` is a compile error.

**Bare `break` semantics differ between iter callbacks and `loop`.** In iter callbacks (`Iter.reduce`, `Iter.map`, etc.) bare `break` stops with the *accumulated value so far* — the accumulator is naturally the result. In `loop` there is no accumulated collection or running value distinct from the state, so bare `break` produces `Unit` (i.e. it is equivalent to `break Unit`). If you want the loop to terminate with the current state, write `break n` explicitly. This is a real semantic difference, not just wording — the worked examples below pin down both forms.

```nomi
// retry with attempts counter
result = Iter.loop(|attempts = 0|
    case fetch(url) {
        Ok(data) -> break Ok(data)
        Err(_) when attempts >= 3 -> break Err("gave up")
        Err(_) -> attempts + 1
    }
)

// reading input
lines = Iter.loop(|lines = []|
    case io.read_line() {
        Ok("") -> break lines
        Ok(line) -> [line, ..lines]
        Err(_) -> break lines
    }
)
```

Multiple state values use a tuple parameter with destructuring in the body:

```nomi
// Newton's method — converge on square root
(_, guess) = Iter.loop(|state = (n / 2.0, 0.0)| {
    (guess, prev) = state
    diff = guess - prev

    if diff > -0.0001 and diff < 0.0001 {
        break state
    }

    next = (guess + n / guess) / 2.0
    (next, guess)
})
```

`return` inside the body exits the callback lambda (the nearest function boundary), not the enclosing function — consistent with Nomi's no-non-local-returns rule.

### Iteration is built from three primitives

Iteration in Nomi composes from a closed set of primitives. There is no user-defined-iteration-function escape hatch — and we haven't found one we'd need:

- **The `Iter` interface** — implement `each_while` (push each element to a `yield` callback until it returns `False`) on a type, and every `Iter` owner function (`Iter.map`, `Iter.reduce`, `Iter.take_while`, …) applies to values of that type.
- **`Iter.loop`** — general state-threading; covers anything you'd reach for an imperative loop.
- **Composing existing iter ops via pipes** — `Iter.filter`, `Iter.take_while`, `Iter.reduce`, and the rest chain freely.

Every generic iteration op is an `Iter` owner function; container-specific ops (`Map.map_values`, `Set.union`, …) build on them internally. The set of call-sites that accept `break`/`continue` is the closed list of iter-family functions plus `Iter.loop`, and the analyzer enforces that closure. The lowering target for `break`/`continue`/`return` is the compiler/runtime-internal signal described in "`break` and `continue` in Callbacks" above — not a Nomi type, with no surface spelling.

### Compared to Recursion

Recursion is stack-safe in Nomi (see Tail-call optimization below), so the choice between `loop` and explicit recursion is purely ergonomic. `loop` replaces patterns that would otherwise need helper functions with accumulators:

**Parsing args — recursive (needs helper):**

```nomi
fn parse_args(args: List<String>): Config {
    parse_args_acc(args, config.default())
}

fn parse_args_acc(args: List<String>, config: Config): Config {
    // private helper
    case args {
        [] ->
            config

        ["--port", value, ..rest] ->
            parse_args_acc(rest, Struct.update(config, {port: String.to_int(value) |> Maybe.with_default(8080)}))

        ["--verbose", ..rest] ->
            parse_args_acc(rest, Struct.update(config, {verbose: True}))

        [_, ..rest] ->
            parse_args_acc(rest, config)
    }
}
```

**Parsing args — loop:**

```nomi
fn parse_args(args: List<String>): Config {
    (_, config) = Iter.loop(|state = (args, config.default())| {
        (args, config) = state

        case args {
            [] ->
                break state

            ["--port", value, ..rest] ->
                (rest, Struct.update(config, {port: String.to_int(value) |> Maybe.with_default(8080)}))

            ["--verbose", ..rest] ->
                (rest, Struct.update(config, {verbose: True}))

            [_, ..rest] ->
                (rest, config)
        }
    })

    config
}
```

### Summary

- **Stdlib functions** (`Iter.map`, `Iter.reduce`, `Iter.each`, `Iter.find`, etc.) — collection iteration. `break` and `continue` work in callbacks.
- **`Iter.loop(|state = init| ...)`** — stateful infinite loops (I/O, retries, state machines, channel consumption). `break` and `continue` work here too.
- **Direct recursion** — naturally recursive structures (trees, divide-and-conquer, mutual recursion) where the problem itself branches.
- `continue` — skip this element. Never takes a value.
- `break` — stop, return what's accumulated so far. `break value` includes one final value before stopping.
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

- **Lambda body boundaries.** A lambda has its own tail context. In `fn f(xs: List<Int>): Int { Iter.reduce(xs, |acc, x| f(rest(x))) }`, the call to `Iter.reduce` is `f`'s tail position and gets trampolined, but the call to `f` *inside the lambda* is the lambda's tail, not `f`'s. `Iter.reduce` invokes the lambda fresh on each iteration, so there is no growing chain that crossing the boundary could collapse. This rule is universal across TCO'd languages (Scheme, Clojure, Erlang, Haskell, OCaml). The practical guidance: when you want to "iterate by recursing through `Iter.each`," reach for `loop` instead.
- Default-value expressions. They run before the body, not as part of it.
- The operand of `try`. The keyword performs a pattern-match between the call's return and the function's return, so the call inside `try expr` is not directly the enclosing function's tail.

**Host functions** (host-provided / builtin) are unaffected. They execute one Go function and return — there is no Nomi-stack frame to trampoline. Interface-dispatch shims look like host functions but resolve at runtime to user-defined functions; those participate in TCO via an internal handoff back to the trampoline, so `Iface.some_fn(x)` calls behave identically to direct calls for the purposes of this guarantee.

A function that recurses in tail position needs no annotation. A computation whose natural form keeps an accumulator can be a tail-recursive helper or an `Iter.loop`; a function that recurses on both branches of a tree is not tail recursive:

```nomi
// tail recursive — requires a private helper to hide the accumulator
fn factorial(n: Int): Int {
    factorial_acc(n, 1)
}

fn factorial_acc(n: Int, acc: Int): Int {  // private helper
    case {
        n <= 1 -> acc
        _ -> factorial_acc(n - 1, n * acc)
    }
}

// same computation with loop — no helper function needed
fn factorial_loop(input: Int): Int {
    (_, acc) = Iter.loop(|(n, acc): (Int, Int) = (input, 1)| {
        if n <= 1 { break (n, acc) }
        (n - 1, n * acc)
    })

    acc
}

// NOT tail recursive — both branches recurse, can't be in tail position
fn depth<T>(tree: Tree<T>): Int {
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
```

Non-tail recursion is allowed; it suits bounded-depth data like trees. For iteration over large or unbounded data, use `loop` or a tail call. `Iter.loop` runs its callback in constant stack space as well.

**Diagnostics.** Tail calls do not appear as separate frames in error reporting. When an error fires inside a tail-recursive function, the trace shows one frame regardless of how deep the iteration ran — that is the cost of constant stack space.

**Deferred calls.** A tail call leaves its function before the callee runs. In a function with pending `defer` registrations, the tail call's arguments are evaluated first, then the function's deferred calls run, most recent first, and then the callee runs:

```nomi
import std/io

fn leave(n: Int) {
    io.print("leave ${n}")
}

fn down(n: Int): Int {
    defer leave(n)
    io.print("enter ${n}")
    step(n)
}

fn step(n: Int): Int {
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
fn grow(x: Int): Int {
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
    fn to_string(value: self): String
}

// Iter — the iteration interface, lives in std/iter
interface Iter<T> {
    fn each_while(collection: self, yield: (T) -> Bool): Bool
}
```

The `Iter` interface defines a **push** iterator: `each_while` walks the collection and hands each element to `yield`, which returns `False` to ask it to stop. `each_while` returns `True` when the source ran to exhaustion and `False` when a consumer stopped it early. The source drives its own loop, so no `self` appears in the signature and nothing is rebuilt per element. Works naturally with immutability since `each_while` reads a value it never mutates. This is Go's `iter.Seq` shape, so a Nomi iterator and a Go `for range` speak the same protocol.

`List<T>` implements `Iter<T>` — `each_while` walks the cons chain. `Vector<T>` also implements `Iter<T>`, retaining its O(1) known count while walking its slice window by index. `Map<K, V>` implements `Iter<(K, V)>`. `String` implements `Iter<String>` and iterates by grapheme cluster, one user-visible character per element. Ranges over `Discrete` values such as `Int` and `Codepoint` also implement it, so everything composes under one protocol.

`List`, `Vector`, and `Map` also implement `Equatable` and `Hashable` (structural; map equality and hashing are order-insensitive), so a type with a collection field can write `derive Equatable` / `derive Hashable`. `List<T>` and `Vector<T>` additionally implement `Comparable` (lexicographic) when `T` does, so a List of tuples or anonymous structs has no order: `List.compare`, `<`, `Comparable.compare` and `Iter.sort` over one are compile errors, as `<` on a tuple is. `Map` is intentionally **not** `Comparable` — it has no natural total order (matching Rust's `HashMap`).

A tuple is `Equatable` and `Hashable` structurally, and not `Comparable` (as Rust derives `Hash` and `Eq` for tuples; the order is left undeclared here for the reason `<` on a tuple is an error). `Hashable.hash((a, b, c))` is the `derive Hashable` mix over the slots, `(Hashable.hash(a) * 31 + Hashable.hash(b)) * 31 + Hashable.hash(c)` with wrapping arithmetic, each slot hashed by its own type's impl — the same number a positional variant carrying those slots mixes before its variant index. So `Hashable.hash((1, 2)) == 33`.

### Interface Body Items — the Kinds

An interface body is an item list. Each item's *form* determines its role, on two orthogonal axes — whether/how a body is supplied, and (for defaults) whether it's overridable. There is **no `pub` on interface items**: a function's visibility is the interface's. The grid:

| Item form | Role |
|---|---|
| `fn` signature, no body | **Required function** — part of the contract; every impl must supply it. |
| `fn` with body | **Final default** — impls get it for free; overriding is an error. |
| `open fn` with body | **Open default** — impls get it for free; an impl may override it. |
| `host fn` (no body) | **Host-backed default** — provided by the runtime to every implementor; final. |
| `open host fn` (no body) | **Open host-backed default** — host-provided; an impl may override it. |

`open` is the overridability axis; the body source (Nomi `{ … }`, host-provided, or none) is the other. Plus one non-function item kind: `field name: Type` declares a **field requirement** (see *Field Requirements* below), which an interface may declare alongside any number of functions.

Every bodied/host item is part of the contract (a default), reachable both interface-qualified (`Iter.known_count(xs)`) and type-qualified on an implementor (`List.known_count(xs)`) with full generic inference — the two are the *same* function, selected by the concrete type bound to `self`. Operations that take no `self` are **not** interface functions — put them in an inherent `impl Iface { ... }` block beside the interface and call them owner-qualified (`Iter.from(0)`).

> The example uses `known_count` because, for `Iter`, that is the *only* default — the interface is pure protocol, just `each_while` (required) plus the lone `open` default `known_count`. **Every generic iteration algorithm is an `Iter` owner function, not a dispatched interface function**: the adapters (`map`/`filter`/`take`/`drop`/`take_while`/`drop_while`/`flat_map`/`zip`/`cycle`/`concat`/`with_index`), the terminals (`reduce`/`find`/`each`/`any?`/`all?`/`empty?`/`first`/`last`/`at`/`count`/`sort`/`sort_by`/`partition`/`group_by`/`frequencies`), and the materializers (`to_list`/`to_vector`/`to_set`/`to_map`/`reverse`/`flatten`). So `Iter.map(xs, f)` and `Iter.reduce(xs, f)` are owner-qualified calls, not dispatch calls. There are also no eager collection-adapter functions: a `List` back from a chain is the explicit `Iter.map(xs, f) |> Iter.to_list()`. Container-specific work a generic adapter can't express because it reads or rebuilds a container's structure lives on the relevant owner — `Map.map_values`, `Set.union`, `List.concat`, `Vector.at`, `Map.size`/`Set.size`, native `String.reverse`. The one rule: generic over any iterable → `Iter.X`; specific to a container → that container's owner. `known_count` is the single op that's a dispatched default rather than an owner function, because it's the protocol hook `Iter.count` consults for an O(1) count (see §19).

A Nomi-bodied default function may give its **trailing parameters default values**, exactly like a free `fn` (`fn announce(value: self, loud: Bool = False): String { … }`); a caller omitting the trailing argument gets the default. Defaults are rejected on a required or host-backed function — only a Nomi default body carries the value at runtime.

An interface function may carry a **function-level `where` clause** that
constrains an in-scope type variable for that one function. Required
signatures write it after the return type; default functions write it between
the return type and the body:

```nomi
interface Ranked<T> {
    fn entries(collection: self): List<T>

    // `T` is the interface's element type; `best` additionally requires it
    // be Comparable, without constraining the rest of the interface.
    fn best(collection: self): Maybe<T> where T: Comparable {
        Iter.sort(entries(collection), Direction.Descending) |> Iter.first()
    }
}
```

The named variable is the interface's own type parameter (`best`'s `T` above) or an explicit method-local type parameter introduced by that interface function:

```nomi
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

Here `K` is declared by `fn prefer<K>` and then constrained by `where K: Comparable`. A bare `K` that appears only in parameters, returns, or a `where` clause is an error; interface methods do not invent implicit function-local generics. The bound list uses the same `: A and B` form everywhere `where` appears. This is Rust's `fn max(self) -> Option<Self::Item> where Self::Item: Ord` — a per-function bound on the element type, so a function like `best` can require `Comparable` while the interface's other functions stay usable on any element. The bound is enforced and its `(concrete, Iface)` dispatch demand recorded at each call site. `where` is a hard keyword. Ordinary `fn` declarations, implementation functions, `host fn` declarations, required interface functions, and interface defaults may all use a `where` clause; a Nomi-bodied function writes it between the return type and the body, while a bodyless signature or `host fn` writes it at the end of the signature.

Interfaces can provide default implementations. Default implementations can call other interface functions unqualified. Implementors get defaults for free; defaults are **final by default** — implementors only get to override them when the interface marks the default `open`:

```nomi
interface Formatted {
    fn format(value: self, style: String): String

    // final default — implementors get this for free, can't override.
    fn format_pretty(value: self): String {
        format(value, "pretty")
    }

    // open default — an implementing type may override format_brief.
    open fn format_brief(value: self): String {
        format(value, "brief")
    }
}
```

The final-by-default rule is the same intuition as a `final` method in Java/C# or a non-`open` method in Swift, translated into Nomi's function model: a default that the interface author did not flag as an extension point is part of the protocol's stable surface, and overriding it would break the contract for callers that rely on the published behavior. Marking a default `open` is an explicit "this is meant to be tweaked" affordance.

**Host-backed defaults** (`host fn` / `open host fn`) are the home for operations the runtime implements rather than Nomi. They behave like any other default (provided to every implementor, dispatched), but the body comes from the host; `open host fn` permits a type to ship a specialized override. `Iter`'s own `known_count` is the mirror image: an `open` *Nomi* default returning `None`. `List` overrides it with a host-backed `host fn known_count`, while `Vector` overrides it by returning `Some(Vector.length(vector))`; both give `Iter.count` an O(1) fast path (§19). The generic iteration *algorithms* themselves — `reduce`/`find`/`sort`/`map`/`count`/… — are **not** interface defaults at all: they're non-dispatched owner functions (`reduce`/`sort_with` host-backed, the rest Nomi-bodied), so the host-backed-default feature is used by `Iter` only for the `known_count` override path.

### Field Requirements

In addition to function signatures, an interface body can declare `field name: Type` requirements. Any struct that satisfies the interface — asserted with an `impl Iface for Struct { ... }` block — must declare a field of that name with that exact type:

```nomi
struct Tag {
    id: Int
}

struct Logger {
    name: String
}

pub interface Tagged {
    field tag: Tag
}

struct Event {
    tag: Tag
    name: String
    logger: Logger
}

impl Tagged for Event

```

Fields are **required-only** — interfaces never carry default values; the implementing struct's field declaration owns its default. Field types are matched Nomi-nominally (`field logger: Logger` requires the impl struct's field to be exactly `Logger`-typed, not `ProdLogger` even when both share a shape).

An `impl Iface for Struct` declaration is what asserts a struct satisfies a fields-bearing interface; without it, the analyzer has no fact to validate. For a fields-only interface there are no functions to implement, so the body is omitted and the declaration exists purely to declare the conformance. A struct can implement several field-bearing interfaces with one declaration per interface; each is checked independently against its interface's field set:

```nomi
struct Handler {
    context: Context
    logger: Logger
}

impl HasContext for Handler

impl HasLogger for Handler

```

For a **mixed** interface (fields + functions), the `impl Iface for Struct { ... }` block asserts the field requirements and its functions supply the function contract. For a **functions-only** interface, omitting a required function is an error. For a **fields-only** interface, the bodyless `impl Iface for Struct` declaration is the whole declaration.

Only a struct can satisfy a field-bearing interface, and the compiler rejects any other receiver rather than accepting it vacuously. `impl Tagged for Color` on an `enum Color` is an error — `'Color' is an enum, so it declares no fields, and interface 'Tagged' requires field 'tag: Tag'` — because a `field` requirement is a claim about STORAGE and only a struct declares storage. The requirement exists so that `t.tag` on a `Tagged`-typed value cannot fail; a receiver whose kind declares no fields would break exactly that guarantee. When the value a non-struct can supply is what the contract is about, declare a function requirement instead (`fn tag(value: self): Tag`), which any receiver kind can implement and whose call site `Tagged.tag(x)` is spelled as the dispatch it is.

A generic interface's requirement is compared after its type arguments are substituted, so `interface Container<T> { field items: List<T> }` is satisfied by `struct Box { items: List<Int> }` under `impl Container<Int> for Box`, and refused by `items: List<String>`. A requirement may also name `self`, which means the implementing type: `field next: self` on `impl H for S` requires `next: S`.

### Implementations

Manual implementations are top-level `impl Iface for Type { ... }` blocks. The header names one interface and one implementing type; the body provides that interface's functions as plain `fn` / `host fn` items. Interface signatures use `self` as the concrete implementing-type placeholder; implementations spell the actual concrete type:

```nomi
interface Formatted {
    fn format(value: self, style: String): String
}

struct User {
    name: String
}

impl Formatted for User {
    fn format(user: User, style: String): String {
        "User: ${user.name} (${style})"
    }
}
```

Inside an impl block, the implementing type is written exactly like any other type annotation: `User`, `Box<T>`, `Result<T, E>`. This keeps generic signatures self-contained — `fn map(maybe: Maybe<T>, f: (T) -> U): Maybe<U>` shows immediately which parameters belong to the implementing type and which are function-local. Implementation functions do not write `self`; `self` is never a value, implicit argument, expression qualifier, or concrete-body type alias. Inside an `impl Iface for Type { ... }` block, sibling functions may be called bare; outside that declaration body, calls are qualified with a type or interface (`Box.map(...)`, `Display.to_string(...)`).

Operations that are not interface implementations are ordinary file-level
functions beside the type, or inherent functions in an `impl Type { ... }` block. Nomi does not have extension methods: a sibling file
cannot reopen an imported type with new type-qualified members.

A file may contain an `impl Iface for Type { ... }` block, but the file is only the lexical home for that declaration. It does not implement the interface. If a stateless API needs to participate in an interface, model it as a zero-sized `type` and implement the interface on that type.

The same interface-implementation form is used whether the implementing type is declared locally or imported from another package. For generic receiver types, the type parameters come from the receiver itself: write `impl Formatted for Box<T> { ... }`, not `impl<T> Formatted for Box<T>`. Constraints written on the type declaration flow into its impls. An impl-level `where` clause adds only further narrowing constraints for that implementation, such as `impl Iter for Range<T> where T: Discrete { ... }`. The block holds only that interface's implementation functions as plain `fn`/`host fn` items — no inherent `fn` items, no nested `impl` entry, and no `derive` entry:

```nomi
import users.User

impl Formatted for User {
    fn format(user: User, style: String): String { ... }
}
```

Once accepted, every manual impl feeds the same implementation model: coherence ("one impl per `(Iface, Type)` program-wide"), the orphan rule, missing-impl diagnostics, and dispatch. Duplicate `impl Iface for Type` declarations collide.

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
derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}
derive FromJson for User with FromJson.Options{rename_all: Json.Case.Camel}

impl Debug for Duration {
    fn inspect(d: Duration): String { ... }
}

impl Display for Duration {
    fn to_string(d: Duration): String { ... }
}
```

Interface functions can be called two ways — qualified by the implementing type or by the interface itself. Both route through the same runtime dispatch: the concrete type bound to `self` selects the implementation.

```nomi
user = User{name: "Alice"}

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
    fn values(it: self): List<Float>

    fn mean(it: self): Float {
        xs = values(it)
        Iter.reduce(xs, |sum = 0.0, x| sum + x) / Int.to_float(Iter.count(xs))
    }
}

pub struct Stats {
    samples: List<Float>
}

// internal scaffolding — not pub, not constructible outside the defining file
type AggStats Stats

impl Aggregate for AggStats {
    fn values(it: AggStats): List<Float> {
        AggStats(s) = it
        s.samples
    }
}

pub fn average(s: Stats): Float {
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
    fn speak(value: self): String
}

struct Dog {
    name: String
}

impl Speaker for Dog {
    fn speak(dog: Dog): String {
        "${dog.name}: woof"
    }
}

struct Cat {
    name: String
}

impl Speaker for Cat {
    fn speak(cat: Cat): String {
        "${cat.name}: meow"
    }
}

fn main() {
    // Both dispatched the same way (interface- or type-qualified):
    io.print(Speaker.speak(Dog{name: "Rex"})) // Rex: woof
    io.print(Cat.speak(Cat{name: "Tom"})) // Tom: meow
}
```

### Function Name Collisions

If a type implements two interfaces that define the same function name, each
implementation function uses an interface-qualified name; use
interface-qualified calls the same way:

```nomi
import std/io

interface Speaker {
    fn speak(value: self): String
}

interface Performer {
    fn speak(value: self): String
}

struct Dog {
    name: String
}

impl Speaker for Dog {
    fn speak(dog: Dog): String {
        "${dog.name}: woof"
    }
}

impl Performer for Dog {
    fn speak(dog: Dog): String {
        "${dog.name} takes a bow"
    }
}

fn main() {
    my_dog = Dog{name: "Rex"}

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
    fn format(value: self, style: String): String
}

struct User {
    name: String
}

impl Formatted for User {
    // ok — the `self` position uses descriptive name "user", non-User param "style" matches
    fn format(user: User, style: String): String { ... }
}

struct Report {
    title: String
}

impl Formatted for Report {
    // compiler error — "s" doesn't match "style"
    fn format(report: Report, s: String): String { ... }
}
```

### Parameter and Return Type Matching

An implementation function's parameter and return types must match the interface function's, after the impl header's type arguments replace the interface's type parameters. The impl's own type parameters are fixed inside the impl: where the interface requires `T`, the function takes or returns `T`, never a concrete type and never another of the impl's parameters. A function-level type parameter may be renamed, but each one of the interface function's corresponds to exactly one of the implementation's: the implementation may not fix it to a concrete type, merge two into one, or replace it with one of the impl's parameters.

```nomi
interface Store<K, V> {
    fn get(store: self, key: K): Maybe<V>
}

struct Cache<T> {
    items: Map<String, T>
}

impl Store<String, T> for Cache<T> {
    // ok — V is T
    fn get(cache: Cache<T>, key: String): Maybe<T> { ... }
}

impl Store<String, T> for Shelf<T> {
    // compiler error — return type Maybe<Int> does not match Maybe<T>
    fn get(shelf: Shelf<T>, key: String): Maybe<Int> { ... }
}
```

An implementation function's `where` clause may only repeat bounds the call through the interface already guarantees: the interface function's own `where` bounds on the corresponding parameter, and, for one of the impl's parameters, the bounds on the impl block (`impl Show<T> for Cache<T> where T: Named`) and on the receiver's declaration. A call through the interface checks only those, so any other bound is an error; a bound on one of the impl's parameters belongs on the impl block.

### Composition: No Interface Inheritance

Nomi deliberately omits interface inheritance. There is no `extends` keyword and no parent/child relationship between interfaces. Relationships between contracts are expressed at use sites with `where` bounds, optionally named with a `typealias`:

```nomi
// Comparable is the prelude's `interface Comparable { fn compare(a: self, b: self): Ordering }`.
interface Sortable {
    fn sort_key(value: self): String
}

// Direct multi-interface bound at the consumer.
fn sort_direct<T>(list: List<T>): List<T> where T: Sortable and Comparable

// Named bound — `typealias` over `and`. Use sites read as a single name.
typealias SortableValue Sortable and Comparable

fn sort<T>(list: List<T>): List<T> where T: SortableValue
```

Each interface stands alone. A function that needs both contracts says so directly in its bound clause (or via a typealias that names the conjunction); nothing is implied by the interface declarations themselves. This keeps interface declarations local — no chain to chase — and makes every required contract visible at the consumer site.

An interface-bound alias (`typealias Name A and B`) is usable only in `where` bounds and in the RHS of another bound alias. Using it as a value type (parameter annotation, struct field, return type) is rejected by the checker with a pointer at the `where T: Name` form.

The cost of this design is a small amount of repetition at consumers that need several interfaces together; bound aliases collapse that to a single name. The benefit is that interface declarations carry no hidden constraints, refactoring an interface doesn't silently change requirements elsewhere, and there is no "where does function X live?" navigation question.

Heterogeneous collections of interface-implementing values — the use case `extends`-style hierarchies often serve in OOP languages (for example, `List<Event>` for mixed click/key/resize events) — are expressed in Nomi via enums with `embeds` instead. See §8 Embedded Types for the tagged-sum pattern that replaces the inheritance-flavored design.

### Interface Bounds on Generics

Generic type parameters can be constrained to types that implement a specific interface:

```nomi
fn sort<T>(list: List<T>): List<T> where T: Comparable
```

`T` must implement `Comparable`. The compiler rejects calls with types that don't satisfy the bound: `sort([(1, 2)])` fails with ``no impl of `Comparable` for `(Int, Int)`; tuple types cannot implement `Comparable` ``.

Multiple bounds are joined with `and`:

```nomi
fn describe<T>(value: T): String where T: Showable and Tagged {
    Tagged.tag(value) + " " + Showable.show(value)
}
```

The bound list is conjunctive — every interface in the list must be satisfied at every call site. There is intentionally no `or` operator: a disjunctive bound `T: A or B` could only call functions present in both `A` and `B`, so it adds nothing beyond what bounding on the common parent interface already gives you. The keyword `and` mirrors Nomi's boolean conjunction; symbolic forms like Rust's `+` or Swift's `&` are not used.

Generic headers introduce names only. Constraints are written in a `where` clause. On functions it appears after the return type; on type declarations it appears after the type parameter list and before the body:

```nomi
fn step_by<T, S>(
    r: Range<T>,
    by: S,
): Iter<T> where T: Comparable and Steppable<S> {
    StepByRange{range: r, by}
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
fn render<T>(item: T, style: String): String where T: Formatted {
    Formatted.format(item, style)
}

render(User{name: "Alice"}, "pretty")       // "User: Alice"
render(Report{title: "Q1"}, "pretty")       // "Report: Q1"
```

`Formatted.format(item, style)` means "call whichever `format` implementation `T` provides." This interface-qualified call form is implemented today and used throughout `std/iter.nomi` (for `Iter.each_while(...)` over a polymorphic `source: Iter<T>` parameter).

Named arguments work as expected — the interface's parameter names are the contract:

```nomi
Formatted.format(value: item, style: "pretty")
```

**Type-parameter-qualified calls.** In generic code, a value's type is often a parameter `T` bounded by an interface (`fn show<T>(x: T): String where T: Display`). The call can be qualified by the *parameter* — `T.to_string(x)` — as a generic form of qualified dispatch. The analyzer resolves `T` to the bound interface that declares the function (rejecting an unbounded `T`, a function no bound provides, or — when two bounds declare the same name — an ambiguous call that must be interface-qualified instead); the call then selects the implementation for the concrete type bound to `self`, exactly like `Display.to_string(x)`. The `T` keeps its identity — hover and go-to-def see the type parameter, not the interface it resolves to.

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

A generic function that instantiates itself at an ever-growing type
(`fn grow<T>(x: T)` calling `grow(Box{inner: x})`) has no finite set of
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
    fn get(container: self, index: Int): Maybe<T>
    fn size(container: self): Int
}
```

In interface definitions, `self` implicitly includes the type parameters — so `self` in `Container<T>` means the full implementing type with its type arguments. A generic implementation writes the concrete implementing type explicitly (`impl Container for Shelf<T> { ... }`); `T` is the type parameter declared by `Shelf`.

Interface arguments may be inferred from the implementation function signatures or written in the impl header when the interface identity needs to be explicit. For example, `impl Container for Shelf<T>` can infer the contained item type from `fn get(...): Maybe<T>`, while `impl Add<Days, Day> for Day` says up front that `Day + Days` returns `Day`. A type can implement distinct instantiations of the same generic interface when those instantiations represent different contracts, such as `impl Add<Days, Day> for Day` and `impl Add<Weeks, Day> for Day`.

Example implementation:

```nomi
import collections.Container

struct Shelf<T> {
    items: List<T>
}

impl Container for Shelf<T> {
    fn get(shelf: Shelf<T>, index: Int): Maybe<T> {
        Iter.at(shelf.items, index)
    }

    fn size(shelf: Shelf<T>): Int {
        Iter.count(shelf.items)
    }
}

s = Shelf{items: ["a", "b", "c"]}

Shelf.get(s, 1) // Some("b")

Shelf.size(s) // 3

s |> Shelf.get(0) // Some("a")
```

A function can accept any `Container`:

```nomi
fn first<T>(container: Container<T>): Maybe<T> {
    Container.get(container, 0)
}

first(shelf) // Some("a")
```

Or **bound a type parameter** by the interface. Where an interface-typed parameter erases the concrete type, a bound keeps it — and the interface's type argument names the projected element:

```nomi
// `I` is some concrete iterator; `T` names its element type
fn saw_any?<T, I>(it: I): Bool where I: Iter<T> {
    // `each_while` answers False when a consumer stopped the source, so
    // refusing every element asks "did this source have anything?".
    Iter.each_while(it, |_x| False) == False
}

saw_any?([10, 20, 30]) // True — I = List<Int>, T = Int
```

This `<T>` is the same one the impl receiver names, seen from the other direction. A generic interface's type parameter is *determined* by each implementor (one impl per type), so it is never written on the interface name where the implementor is already known (`impl Iter for List<T>`, not `impl Iter<T> for List<T>`). Where it earns a name is exactly where the implementor is *unknown*: an interface-typed parameter (`container: Container<T>`) or a bound (`I: Iter<T>`), abstracting over an arbitrary conformer and projecting its element type. The projection resolves by inference — `saw_any?([10, 20, 30])` pins `I = List<Int>` and `T = Int` from the argument — precisely because the one-impl-per-type rule makes the element type a function of the implementor. A type parameter that only a bound and the result name is projected the same way, wherever the call sits: `plus(Day(10), Days(4))` for `fn plus<L, R, Out>(lhs: L, rhs: R): Out where L: Add<R, Out>` is a `Day`, because `impl Add<Days, Day> for Day` is Day's impl of `Add` at `Days`. This is the multi-parameter interface encoding of associated types: there is no separate associated-type declaration or projection syntax (no `I::Item`), because the type parameter plus the coherence rule already supply both the name and the functional dependency that would otherwise require one.

### Foreign Types

The same `impl Iface for Type { ... }` form can implement an imported interface for a local type, a local interface for an imported type, or any combination where the orphan rule permits it:

```nomi
import users.User

interface Html {
    fn to_html(value: self): String
}

impl Html for User {
    fn to_html(user: User): String {
        "<span>${user.name}</span>"
    }
}
```

Any file that can see the impl calls it through the implementing type or the interface — `User.to_html(user)` or `Html.to_html(user)`. Coherence guarantees at most one `Html` impl for `User` program-wide, so there is never anything to disambiguate by location.

An impl block is part of a program when its file is, and a file is part of a program when some file in the program imports it. Once it is, the impl applies everywhere in the program, including files that do not import its file. A file that holds only impls for types and interfaces declared elsewhere is brought in with an import that names nothing from it (`import html_impls`); that import is used when the program uses one of those impls (§*Unused imports are errors*). The standard library's impls are always part of the program.

**Orphan rule.** An `impl Iface for T { ... }` block must have either `Iface` or `T` declared in the *current Nomi module* (the directory tree rooted at `nomi.toml` + `go.mod`, per the module model in §14 and §31). A third-party module that owns neither side cannot write the impl directly — the normal escape is a wrapper (newtype) the module owns. The whole-program collision check is the correctness backstop; the orphan rule is the hygiene layer that makes the common case of "two libraries both implementing `Display` for `Int`" unrepresentable.

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
> `import sqlite` names `sqlite.nomi` at the module root. Entry-point
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

pub fn new(name: String, age: Int): User {
    User{name, age}
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

pub fn handle(req: HttpRequest): HttpResponse {
    parsed = request.parse(req)
    res.build_from(parsed)
}
```

### Import forms

**Path separator rule:** Import-path segments are separated by `/` (mirroring the filesystem layout under `std/` and any user package tree). A path-only import binds the final path segment as a file API object (`import http/request` binds `request`). A dot after the path imports a single selected item (`import task_parser.parse`). A brace selector after a path or owner imports several children from that left-hand side (`import std/maps.{self, Map}`, `import shape.Shape.{Circle, Rectangle}`). Braces select names from a file; a path cannot be grouped. `import std/{io, regex.Regex}` is a syntax error at the `{`, and several files go in an import block, one path per line.

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

Both forms are valid syntax, but **the formatter canonicalizes them**: consecutive imports with no blank line between them combine into a single sorted block; same-module statements merge (`import std/calendar.Date` + `import std/calendar.Months` → `import std/calendar.{Date, Months}`); a lone import stays bare. A **blank line between imports is the "keep separate" signal** — it's preserved as a group boundary, so distinct groups (stdlib vs. project) stay in distinct blocks. Comments inside a block are preserved. The block is therefore the canonical form for two or more grouped imports. Import-block entries are newline-separated; commas only separate children inside a `.{...}` selector. The block always renders multiline (it never collapses onto a single line).

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

u = User{name: "Alice", age: 30}

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
- Stdlib files use the `std/` prefix: `import std/io`, `import std/iter`. The `std/` prefix routes to the embedded stdlib filesystem and prevents user modules from shadowing stdlib names.
- Variants must come through their enum. `import shape.Circle` (a flat selective variant import) is rejected with a clear error pointing at the owner selector form (`import shape.Shape.Circle`) or the qualified form (`import shape.Shape` then `Shape.Circle`). The owner selector path makes the originating enum visible at the import line, so cross-module collisions are caught at the import rather than silently shadowed.
- Every user file is preceded conceptually by the imports listed in `std/prelude.nomi`. That file pulls in the primitives (`Int`, `Float`, `Decimal`, `String`, `Unit`, `Infallible`, `Byte`, `Bytes`, `Codepoint`, `List`, `Vector`, `Map`, `Set`), `Bool` (with `True`/`False` variants) from `std/bool`, the `Maybe`/`Some`/`None` and `Result`/`Ok`/`Err` enums, the `Range` struct (so range literals like `1..=10` resolve everywhere), the core interfaces (`Add`/`Subtract`/`Multiply`/`Divide`/`Comparable`/`Display`/`Equatable`/`Hashable`/`Discrete`/`Iter`/`Steppable`/`Struct`, plus universal `Debug`), `Ordering`, `Type`, and the application lifecycle types `Startup` and `Context`. Owner functions on those types need no import (`Iter.map`, `String.trim`, `Map.get`, `Result.ok?`). The prelude exports no file API objects, so a file-level function needs its file imported (`import std/io` for `io.print`), and an owner function on a type outside the prelude needs that type imported (`import std/assertions.AssertionFailure` for `AssertionFailure.format`). The `std/literals` cluster (`Literal`/`Fragment`) and niche/helper types such as `Assertable` and `AssertionFailure` are *not* preluded — their authors import them explicitly. The prelude exports no bare functions — bare names are types, interfaces, and variant constructors only. Anything not listed in the prelude must be imported explicitly. Stdlib files do not receive the prelude — they import their dependencies by hand. See §24 for details.
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
- An import path that names no file is a compile-time error at the path, in every import form, whether or not the file uses the import: `import lib` with no `lib.nomi` reports ``no module `lib`: no file lib.nomi in this file's directory``. The message names the directory searched, relative to the importing file, and a hint names a sibling file the path is a misspelling of, or a file inside a directory the path names (`sub` is a directory, not a file). `import std/x` with no standard library module `x` is the same error. A selected name the file does not export is an error at the name: `file 'std/io' has no exported name 'nosuchname'`.
- The `/`-joined segments always name the file: `import utils/inner.Thing` needs `utils/inner.nomi`, and is not a lookup of `inner` inside `utils.nomi`.
- Files cannot be reopened or split across files
- Importing `http` does not import `http/request` — each must be imported separately. A facade file can flatten this for its own consumers via re-exports (see §3 *Re-exporting imported names*).
- Visibility rules apply: declarations carrying `pub` are public, everything else is private (see §3)

### Unused imports are errors

An imported item that no visible text in the file uses is a compile-time error (an analyzer error: it fails `nomi run` and surfaces as the editor diagnostic). The granularity is the imported **item**: in `import std/comparable.{Comparable, Ordering}` with `Ordering` dead, the error names `Ordering` at its own position (`imported name 'Ordering' is unused — remove it from the import`); an aliased item is used through its alias; `self` in a brace list is its own item, used iff the parent name it binds is referenced. Any reference resolving to the imported name counts as a use — expressions, patterns, type annotations, `impl Iface for Type { ... }` blocks, `where` bounds (`where T: Comparable`), `derive Iface for Type` declarations, and typed-literal tags (`Date"..."` uses `Date`). Two carve-outs: a re-exported item (`import mod.{a export}` or line-level `export` on a selective list) is used by definition, and names appearing only in comments or doc-comment ` ```nomi ` examples are not uses. Note that a dot-shorthand pattern (`.Some(v)`) resolves through the scrutinee's type, not through scope — so it does **not** keep a variant import alive. Nomi has no warning tier; the rule is an error by design (a later LSP quick-fix, not a softer diagnostic, is the iteration-friction mitigation). `fmt` does not auto-prune imports — layout is fmt's job, semantics are the compiler's.

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

- **A value of a concrete type** — `p.field` on a struct, anonymous struct, primitive, list, map, enum, tuple, or function — must name a field that type declares. A missing field (a typo, `Point{x, y}` accessed as `p.z`) is a compile error: `struct 'Point' has no field 'z'`. (Tuples index by position: `pair.0`, `pair.1`. Indices chain left to right, so `nested.1.0` is `(nested.1).0`.)
- **A value of interface type** (an existential) exposes only the interface's declared `field` requirements (§13 *Field Requirements*). `s.x` on a `Struct`-typed value, where `Struct` declares no fields, is rejected; `n.name` on a `HasName`-typed value, where `interface HasName { field name: String }`, resolves to `String`.
- **A value of type-parameter type** `T` exposes only the `field` requirements its interface bounds declare. `x.foo` on an unbounded `T` is an error; `x.name` on `T: HasName` resolves via the bound.
- **A type qualifier** — `Type.member` — must name a real member of that type: an interface function, a constructor, or (for enums) a variant. A missing member is a compile error (`type 'List' has no member 'empty'`, `enum 'Maybe' has no variant 'Bogus'`), for both the call form `Type.member(...)` and a bare value `Type.member`. Cross-module members resolve too — an imported type's interface functions are visible to single-file analysis (the editor), so `User.to_string(user)` on an imported `User` is checked, not merely tolerated.

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
- **Literals** carry a `d` (or `D`) suffix, and the literal's text determines its scale: `1.50d` → scale 2, `1.5d` → scale 1, `5d` → scale 0. Underscore digit separators are allowed (`1_000.00d`); exponent notation is not. Runtime/parsed values come from `Decimal.from_string`.

The arithmetic, conversion, and shaping functions live in `std/decimal` (`from_int` / `from_string` / `to_string` / `to_int` / `to_float` / `from_float` / `divide` / `round` / `normalize` / `scale` / `abs` / `negate` / `sign` / `zero?` / `compare` / `min` / `max`), along with the `RoundingMode` enum (`Up | Down | Ceiling | Floor | HalfUp | HalfDown | HalfEven | Unnecessary`), display/debug/equality/hash/order impls, and the standard arithmetic operator impls. A currency-aware `Money` type and locale-aware formatting are deliberately deferred to later stdlib layers that build on `Decimal` without changing it.

`Unit` is the type for functions that don't return a meaningful value. Can be omitted from function signatures:

```nomi
// Unit return can be omitted — these are equivalent:
fn log(message: String) {
    io.print(message)
}

fn log(message: String): Unit {
    io.print(message)
}
```

`Unit` is a real type that works in generics: `Result<Unit, String>`.

`Infallible` is an uninhabited type — no values of type `Infallible` can ever be constructed. Used to indicate impossible cases, such as `Result<Unit, Infallible>` for operations that always succeed but need to satisfy a `Result`-returning API. Similar to Rust's `Infallible` / `!` type.

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
- `Map<K, V>` — key-value collection. Keys may be any *keyable* value — primitives, tuples, lists, structs, distinct types — not just primitives; keys match by Nomi's `==`, so e.g. a `(Int, Int)` or a record can be a key. (A function value, an `Iter`, or a value holding one has no equality (§30), so it can't be a key: a literal, call or pipe that builds such a `Map` or `Set` is a compile error.) Because key matching uses Nomi's `==`, Float keys behave consistently with it: `NaN` is retrievable (reflexive) and `-0.0` / `+0.0` are the same key. **A key of a declared type with a hand-written `impl Equatable` matches through that impl, at any depth** — as the key itself, or inside a tuple, a list, a struct field or an enum payload — so two values its `equal?` calls equal are one key, exactly as `==` would say. The bucket is chosen by the type's hand-written `impl Hashable` when it has one; a type whose `Equatable` is hand-written and whose `Hashable` is not hashes by its type alone, so equal values always share a bucket and `equal?` decides (correct by construction, at the cost of that type's keys sharing one bucket). Every other value — a derived impl included — keys by structural hash and structural equality, which is what a derived `Equatable`/`Hashable` answers. An `impl Hashable` must give equal values equal hashes; one that does not is the author's broken contract, as in Rust and Java.
- `Set<T>` — unordered collection of unique values, backed by `Map` (elements may be any keyable value, same as map keys, and are unique under `==` by the same rule). Literal syntax: `#{1, 2, 3}`; empty sets need type context (`empty: Set<Int> = #{}`). Insertion-ordered like the backing map; `Equatable`/`Hashable` but not `Comparable`.
- `(A, B, ...)` — tuples. Positional, fixed-size, heterogeneous. No single-element tuples.
- `{name: A, age: B, ...}` — anonymous structs. Named fields, fixed-size, heterogeneous.
- `(A, B) -> C` — function type. `fn` defines functions, `(Args) -> Return` is the type.

All are immutable. Defined in detail in their respective sections (§7, §9, §19).

### Distinct Types

Nominal types are always distinct from any other type — including their underlying representation, when there is one. The type name acts as a conversion function for construction. Always named. Nomi has three keywords for declaring nominal types: `struct` (record types — §7), `enum` (tagged unions — §8), and `type` (the other distinct-type forms documented below). All three produce nominal, distinct types; this section covers specifically the `type`-keyword forms.

The body shape attached to the `type` declaration determines its kind:

- **Tuple-distinct** — `(A, B)` (arity ≥ 2). Wraps a tuple.
- **Generic-distinct** — a generic type expression like `Map<K, V>`, `List<T>` or `Maybe<Int>`. Wraps a generic shape.
- **Primitive-distinct** — a bare type name like `Int`, `String`, or a declared struct, enum or distinct (`type Origin Point`, `type Twice Meters`). Wraps a single non-tuple type.
- **Zero-sized** — nothing after the name. A unique standalone tag. Equivalent to a bare enum variant in standalone form.

The wrapped type is the distinct type's representation, so it cannot contain the type itself, directly or through other distinct types: `type E E`, `type L List<L>`, `type M Maybe<M>` and the pair `type A B` / `type B A` are each an error at the declaration (`type 'L' is defined in terms of itself`). A recursive type is a struct or an enum, and a distinct type may wrap one: `struct Node { next: Maybe<Node> }` is legal, and so is `type Wrapped Node`.

Derives are sibling `derive Iface for Type` declarations. Distinct type names
may be dotted PascalCase paths (`type Civil.Days Int`) when the type belongs
under a real parent type; the full dotted name is the nominal identity.

```nomi
type Id Int
type Status Int

fn id_from_string(s: String): Result<Id, String> { ... }
fn id_to_string(id: Id): String { ... }

id = Id(42)
n = Int(id)

// destructuring — mirrors construction syntax
Id(n) = id         // n is 42, type Int
Id(_) = id         // assert type without binding
```

The call form takes the one value the type wraps, checked against the wrapped type, prefix or piped: `Id(42)` and `42 |> Id()` build an `Id`, and `Id("42")` is an error naming both (`Id wraps Int, so Id(...) takes an Int; got String`). A tuple-distinct also takes its elements flat (`Pair(1, "x")`); piped, it takes the whole tuple (`(1, "x") |> Pair()`), and `1 |> Pair("x")` is an error (`Pair takes the piped value as its only argument, got 1 more`), as for a variant.

`Int(id)`, `Float(d)` and `String(s)` unwrap a distinct that wraps exactly that type, one level. They are not conversions: `Int("4")` and `String(4)` are errors (`` `Int` is a type, not a function ``) whose hint names the conversion to call, `String.to_int` (a `Maybe<Int>`), `Int.to_float`, `Float.to_int`, or interpolation `"${x}"` for a String. Calling any other type name is an error too (`Dir(1)`, `Maybe(3)`, `Display(x)`); the type names with a call form are a struct (`Point({x: 1})`), a distinct type, and these three unwraps. A pipe into a type name is the same call with the piped value first: a struct takes the piped record (`{x: 1} |> Point()`), so `1 |> Point()` is an error (`Point expects an anonymous struct literal {...} matching its fields, got Int`). An opaque struct or distinct (`Range`, `Instant`) has no call form outside its defining module, prefix or piped.

A distinct type that wraps a value, named without a call, is its constructor as a function value: `Id` is an `(Int) -> Id`, so `Iter.map(ns, Id)` builds an `Id` from each Int, `f = Id` binds the function, and `Id` passes where an `(Int) -> Id` is expected. A tuple-distinct's function takes the tuple (`((Int, String)) -> Pair`); §8 *Constructors as Function Values* states the rule for variants too. Any other type name is not a value, a `host type` such as `Map` or `List` included, wherever it stands (a binding, an argument, a statement): `f = Int`, `x = Point`, `m = Map` and `Iter.map(xs, String)` are errors (`` `Int` is a type, not a value ``), and `Int(id)` stays a call-only unwrap. The other names that are values are an enum variant (`Dir.North`, and a positional variant as its constructor), a zero-sized type (`Expired`), `True`, `False` and `Unit`, and a type name where a `Type<T>` witness is expected (`Context.value(c, TraceId)`).

Distinct types can implement interfaces, just like structs and enums — write an `impl Iface for Type { ... }` block:

```nomi
type Id Int

impl Display for Id {
    fn to_string(id: Id): String {
        Display.to_string(Int(id))
    }
}
```

Distinct types can also be zero-sized — no wrapped type, just a unique name:

```nomi
type Expired

impl Display for Expired {
    fn to_string(_value: Expired): String {
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
    fn serve(handler: self, req: Request): Response
}

type Handler (Request) -> Response

impl Server for Handler {
    fn serve(handler: Handler, req: Request): Response {
        handler(req)
    }
}

```

Usage:

```nomi
handler = Handler(|_req| Response{status: 200, body: "hello"})

response = Server.serve(handler, some_request)
```

Other types can also implement `Server` — for example, a struct with middleware:

```nomi
struct LoggingHandler {
    inner: Handler
}

impl Server for LoggingHandler {
    fn serve(logging: LoggingHandler, req: Request): Response {
        io.print("${req.method} ${req.path}")
        Server.serve(logging.inner, req)
    }
}
```

#### Distinct Types Over Tuples and Maps

Distinct types whose underlying type has a shape literal (tuple, map, or struct body) support two construction forms — **literal-attach** (no parens around the inner literal) and **call-form** (parens around the inner value). Both are equivalent; pick whichever reads better:

| Distinct over | Literal-attach | Call-form |
|---|---|---|
| `type Id Int` | (no parallel — primitive has no shape literal) | `Id(42)` |
| `type Pair (Int, String)` | `Pair(1, "x")` | `Pair((1, "x"))` |
| `type Kvs Map<String, Int>` | `Kvs{"a" => 1, "b" => 2}` | `Kvs(some_map_value)` |
| `struct User` (fields `name: String`, `age: Int`) | `User{name: "Alice", age: 30}` | `User({name: "Alice", age: 30})` |
| `type Expired` (zero-sized) | `Expired` (no parens) | `Expired` (no parens) |

Tuple-distinct types require **arity ≥ 2** — `type Wrap (Int)` is treated as `type Wrap Int` (wrapping a single non-tuple type), since 1-tuples don't exist in Nomi.

```nomi
type Pair (Int, String)
type Triple (Int, Int, Int)
type Kvs Map<String, Int>

p1 = Pair(1, "hello") // literal-attach — flat args

p2 = Pair((2, "world")) // call-form — explicit inner tuple

t = Triple(1, 2, 3) // arity 3

kv1 = Kvs{"a" => 1, "b" => 2} // literal-attach — inline map literal

kv2 = Kvs(some_existing_map) // call-form — wrap an existing value
```

Construction errors at the call site:

```nomi
Pair(1) // error: Pair takes 2 arguments, got 1; expected Pair(Int, String)

Pair("x", 1) // error: argument 1 of Pair: expected Int, got String
```

Patterns mirror construction — see §10 "Distinct Type Destructuring" for `case` syntax.

#### Opaque distinct types

A distinct type can be declared `pub opaque type <name>`, which exposes the *type name* while keeping its *construction surface* (constructor, unwrap, destructuring, field access, and an update patch naming a field) private. Outside callers can refer to the type, pass values around, and call interface functions, but they cannot inspect or build values directly — they go through the owning file's public smart constructors and accessors.

```nomi
// positive.nomi
pub opaque type PositiveInt Int

pub fn positive(n: Int): Maybe<PositiveInt> {
    if n > 0 { Some(PositiveInt(n)) } else { None }
}

pub fn to_int(p: PositiveInt): Int {
    Int(p)
}
```

Inside `positive.nomi`, every form of the construction surface works — that's how `positive` and `to_int` do their job. From any other file, the boundary blocks five operations:

| Outside the owning file | Status |
|---|---|
| Use in signatures, return types, generic args, field types | **Allowed** — `fn process(p: PositiveInt): PositiveInt`, `List<PositiveInt>`, etc. |
| `PositiveInt(5)` (constructor) | **Rejected** — go through an exported constructor |
| `Int(p)` (unwrap to the wrapped type) | **Rejected** — go through an exported accessor |
| `case p { PositiveInt(n) -> ... }` (destructuring) | **Rejected** — same as above |
| `u.name` (field access on an opaque struct) | **Rejected** — go through an exported accessor |
| `Struct.update(u, {name: "x"})` (a patch naming an opaque struct's field) | **Rejected** — go through an exported constructor |

`==` and `!=` on opaque values still work — Nomi's structural equality runs through the runtime's value comparison, never through user-visible code. Calling interface functions (`Display.to_string(p)`) works whether the impl lives in the owning module or another module — dispatch is on the type, and the impl body decides what to show.

**Opacity restricts operations, never conformance.** An opaque type satisfies every interface a transparent one would — `Struct`, `Debug`, `Display`, `Equatable` — and it satisfies them at bounds and existentials too (`fn dump(s: Struct)` accepts an opaque struct). Only the operations in the table above are closed, and only from outside the defining file. Two consequences worth naming, because the alternative reading looks equally defensible:

- **The universal interfaces stay universal.** `Struct` is satisfied by every struct value (§7) and `Debug` by every type, with no opacity carve-out in either. Withdrawing `Struct` conformance from an opaque struct would be the other way to close the `Struct.update` hole, and it would also withdraw the bound and the existential, which name no field and expose nothing.
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

pub fn create(id: Int, label: String): UserHandle {
    UserHandle(InternalUserData{id, label})
}

pub fn label(h: UserHandle): String {
    case h {
        UserHandle(d) -> d.label
    }
}
```

**Inline struct body — preferred when the wrapped representation has no other use.** The field set IS the representation; no separately-named inner struct is needed. From outside the defining file, the type is opaque exactly as in the wrapper form: callers cannot construct values directly with `Date{...}`, cannot destructure with `case`, and cannot read fields with `d.year`. Inside the defining file, fields are reached directly without a `case` step.

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

pub fn year(d: Date): Int {
    d.year
}

pub fn month(d: Date): Int {
    d.month
}

pub fn day(d: Date): Int {
    d.day
}
```

**Inline enum body — opacity is strict, and the API surface is heavier.** Outside the defining file, the variants are unreachable for construction, pattern matching, *and* payload access — callers get pass-around, equality, and interface functions only. The owning file therefore typically exports a smart constructor per variant, a predicate per variant, and a `Maybe<T>`-returning accessor for any payload-carrying variant, because outside callers can't `case` on the value themselves.

```nomi
pub opaque enum Status {
    Active
    Inactive String
}

pub fn active(): Status {
    Status.Active
}

pub fn inactive(reason: String): Status {
    Status.Inactive(reason)
}

pub fn is_active(s: Status): Bool {
    case s {
        .Active -> True
        _ -> False
    }
}

pub fn inactive_reason(s: Status): Maybe<String> {
    case s {
        .Inactive(r) -> Some(r)
        _ -> None
    }
}
```

Reach for opaque enum when hiding the variant set is the goal — API stability across variant churn, invariant-enforced state-machine transitions, or representation flexibility (enum today, struct tomorrow). Most enums should stay plain `pub`: `Result<T, E>`, `Maybe<T>`, `ParseError`, `Bool` work *because* exhaustive pattern matching is the whole point. This is the ML-family idiom (Haskell module exports, OCaml signatures); systems languages without it push you to the struct-with-private-state workaround instead.

The compelling pay-off: opaque types let invariants live in the type system. `Int.divide(x: Int, y: NonZeroInt): Int` is **total** — the type rules out `y == 0` at the call site, so the function doesn't need a `Maybe` return. The fallibility moves to the validation step (`Int.to_non_zero(y): Maybe<NonZeroInt>`), which is exactly where it belongs.

### Type Aliases

A `typealias` creates a transparent synonym for an existing type. The alias and the original type are fully interchangeable — no wrapping or unwrapping needed:

```nomi
typealias UserResults Map<String, List<Result<User, Error>>>

results = {"alice" => [Ok(user)]}

Map.keys(results) // works — UserResults IS Map<String, ...>
```

An alias may name any type the file sees, including an alias declared below it. An alias whose target names the alias itself, directly or through other aliases, is an error at each alias on the cycle (`typealias 'X' refers to itself (X → Y → X)`), since the target would be infinite; a recursive type is a struct or an enum.

Type aliases can also be generic:

```nomi
typealias Lookup<V> Map<String, V>

fn get_ages(): Lookup<Int> {
    {"Alice" => 30}
}
```

> **Status: not yet implemented.** Non-generic aliases work end-to-end.
> Generic aliases — `typealias Name<T> ...` — are a hard parse error at the
> `<`: `expected type name, got LT`. Track via `feature-status.md`.

Use `typealias` for readability shortcuts. Use `type` when you want a new, incompatible type with its own identity:

| | `type Id Int` | `typealias Name String` |
|---|---|---|
| New type? | Yes — `Id` ≠ `Int` | No — `Name` = `String` |
| Conversion | Explicit: `Id(42)`, `Int(id)` | None needed |
| Can implement interfaces? | Yes | No (implement on the underlying type) |
| Use case | Type safety, domain modeling | Abbreviating complex types |

### Type Annotations

Required on function signatures, inferred within function bodies:

```nomi
fn add(x: Int, y: Int): Int {
    result = x + y // type inferred as Int
    result
}

names = ["Alice", "Bob"] // inferred as List<String>

ages = {"Alice" => 30} // inferred as Map<String, Int>
```

Annotations are also accepted, optionally, on bindings (`name: Type = value` — see §2), struct fields, function parameters, and lambda parameters. On a binding the annotation drives bidirectional inference into the right-hand side and is a checked assertion against the inferred type. Destructuring patterns do not yet accept annotations.

A lambda parameter with no annotation and no default takes its type from the function type its position expects: a parameter's declared type at a call, a function-typed binding annotation, a declared return type. Where no function type is expected, the parameter is the error "cannot infer type for parameter x", and a later use of the lambda does not type it. That covers a lambda bound to a name with no annotation (`f = |x| x`), a tuple or list element, and the argument of a generic constructor with no expected type (`h = Some(|x| x)`, `h = Box{v: |x| x}`), whose type parameter the lambda would itself have to fix. Annotate the parameter (`Some(|x: Int| x)`) or the binding (`h: Maybe<(Int) -> Int> = Some(|x| x)`).

### Generics

Angle bracket syntax. Type parameters are inferred at call sites.

Generic types:

```nomi
struct Pair<A, B> {
    first: A
    second: B
}

p = Pair{first: 1, second: "hello"}
```

Generic functions:

```nomi
fn first<A>(list: List<A>): Maybe<A> {
    List.head(list)
}
```

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
fn grow<T>(x: T, n: Int): Int {
    if n == 0 { 0 } else { grow(Box{inner: x}, n - 1) } // T = Box<T>: an error
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

**Hashes**: `#`, `##` and `#{` are ordinary text in a string. `#{...}` is the set literal in code (§6) and nothing inside a string.

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

Backticks also compose with typed literals (for example, `` Regex`\d+` `` and multi-line `Bash` raw literals); see "Typed literals" below.

### Codepoint literals

A single-quoted literal is a `Codepoint`, the scalar value of one ASCII character:

```nomi
'a'           // Codepoint 97
'\n'          // Codepoint 10
'\''          // the single quote, Codepoint 39
'\u{7F}'      // DEL, Codepoint 127

'a'..='z'     // the 26 lowercase letters, as a Range<Codepoint>
'0'..'9'      // the digits 0 through 8

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
    fn from_fragments(fragments: List<Fragment<String>>): Result<Date, ParseError> { ... }
}

type Sql

impl Literal for Sql {
    fn from_fragments(fragments: List<Fragment<Display>>): Query { ... }
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

impl Literal for Pick { fn from_fragments(fragments: List<Fragment<String>>): String { ... } }

impl Pick { pub fn from_fragments(fragments: List<Fragment<String>>): String { ... } }   // error at `Pick"..."`

interface Other { fn from_fragments(fragments: List<Fragment<String>>): Int }
impl Other for Pick { fn from_fragments(fragments: List<Fragment<String>>): Int { ... } } // error at `Pick"..."`
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
fn from_fragments(fragments: List<Fragment<I>>): R
```

- `I` is the per-literal value-type interface — the constraint each `Dynamic` slot's value must satisfy. Common choices: `Display` for stringifying-then-parsing handlers, `Bindable` for SQL parameter binding, `Safe` for HTML escaping. Concrete (non-interface) `I` is allowed too — `Fragment<String>` accepts only String-typed slots.
- `R` is the literal's static type. For fallible parsing it is typically `Result<T, E>`; for builders it is the produced domain value (`Html`, `Query`, ...).

At each `${expr}` slot, the analyzer checks that the slot expression's type implements `I`; a mismatch is a type error pointing at the offending slot. The interface in `List<Fragment<I>>` is used as an *existential* — slot values are heterogeneous per element, each implementing `I` in its own way (see §15 on interface-as-existential).

**Coherence.** A typed-literal handler is an ordinary interface impl, so it inherits the impl rules wholesale — nothing typed-literal-specific. At most one `(Literal, <Tag>)` impl program-wide (the coherence check), and the orphan rule requires either `Literal` or the tag type to be declared in the implementing package.

**Raw typed literals.** A backtick delimiter disables `${...}` parsing inside the body. The body becomes a single `Static` fragment containing the verbatim text — no `Dynamic` slots. The same handler signature still applies; raw is purely about the parsing of the body, not the contract. Useful when `${...}` or backslashes appear literally in the embedded language.

**Indentation and `${...}` rules.** Triple-quoted typed literals follow the same leading-newline strip, min-indent baseline, trailing-newline strip rules as plain triple-quoted strings (see "Triple-quoted strings" above). The `${expr}` interpolation and `\${` escape rules from "String Literals" above also apply unchanged inside non-raw typed-literal bodies.

**Worked example: `std/calendar`.** The canonical demonstration. `std/calendar` defines an opaque `Date` type whose `Literal` impl's `from_fragments` takes `List<Fragment<String>>` and returns `Result<Date, Error>`:

```nomi
// std/calendar.nomi (excerpt; stdlib files import each other with bare paths)
import literals.{Fragment, Literal}

pub opaque struct Date {}

impl Literal for Date {
    fn from_fragments(fragments: List<Fragment<String>>): Result<Date, Error> {
        body = Iter.reduce(fragments, |acc = "", frag|
            case frag {
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + v
            }
        )

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

`Date` is opaque — outside callers can't bypass parsing. Because parsing is fallible and Nomi has no compile-time literal validation, the return type is `Result<Date, Error>` at the use site above; callers `case`-match or `try`-unwrap there. The `Literal` conformance makes `Date` itself the tag; `from_fragments` is the fixed function name. The same shape — opaque type, fallible parse, `Display`-or-domain interface for slots — applies to `Time`, `NaiveDateTime`, `OffsetDateTime`, `DateTime`, plus user-defined cases like `Url`, `Regex`, `Json`, etc.

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
    fn from_fragments(fragments: List<Fragment<String>>): Box {
        body = Iter.reduce(fragments, |acc = "", frag|
            case frag {
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + v
            }
        )

        Box{contents: body}
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
fn length(s: String): Int                                          // grapheme count
fn slice(s: String, start: Int, end: Int): String                  // grapheme indices
fn contains?<M>(s: String, needle: M): Bool where M: Matcher
fn starts_with?<M>(s: String, prefix: M): Bool where M: Matcher
fn ends_with?<M>(s: String, suffix: M): Bool where M: Matcher
fn to_upper(s: String): String                                     // Unicode-aware, locale-independent
fn to_lower(s: String): String
fn trim(s: String): String
fn split<M>(s: String, separator: M): List<String> where M: Matcher
fn replace<M>(s: String, old: M, new: String): String where M: Matcher  // every match; `new` is literal
fn find_all<M>(s: String, needle: M): List<String> where M: Matcher     // every match, left to right
fn words(s: String): List<String>                                  // split on runs of whitespace
fn lines(s: String): List<String>                                  // split on \n or \r\n
fn repeat(s: String, count: Int): String                           // count >= 0
fn reverse(s: String): String                                      // by grapheme cluster
fn normalize(s: String, form: NormalForm): String                  // NFC / NFD / NFKC / NFKD
fn to_int(s: String): Maybe<Int>
fn to_bytes(s: String): Bytes                                      // UTF-8 bytes
fn to_codepoints(s: String): List<Codepoint>                       // Unicode scalars
fn strip_prefix(s: String, prefix: String): Maybe<String>
fn strip_suffix(s: String, suffix: String): Maybe<String>
fn join(parts: Iter<String>, separator: String = ""): String       // any Iter<String>, driven once
```

The search functions take any `Matcher` (`std/matcher`) as the thing to look for: a `String` matches its own text and a `Regex` (`std/regex`) matches its pattern, so `String.split("a,b", ",")` and `` String.split(text, try Regex`\s+`) `` are the same function. Each Matcher function answers one of these operations with the matcher first (`contained_in?`, `prefix_of?`, `suffix_of?`, `split_in`, `replace_in`, `find_all_in`), and a type of your own implements it to become searchable. A generic function of your own takes either with `where M: Matcher`. "Pattern" is not the name because a pattern in Nomi is a destructuring shape (§10). `String.replace` inserts its replacement literally for a regex too; `Regex.replace_all` expands `$1` capture references. `String.split` and the other search functions are generic, so naming one as a value needs a function type to settle `M`: `contains: (String, String) -> Bool = String.contains?`.

`String` also implements `Iter<String>` (one grapheme cluster per step), so every `Iter` owner function works on a string. To map or filter graphemes, run the generic adapter and join the result back into a `String`: `Iter.map(s, f) |> String.join()`, `Iter.filter(s, p) |> String.join()`. `String.join` takes any `Iter<String>` (a List, Set or Vector, graphemes, or a lazy chain) and a separator that defaults to `""`. `String.reverse`/`String.length` stay container-specific (native, by grapheme cluster).

### Byte and Bytes Functions

Owner functions on `Byte` (`from_int`, `to_int`) and `Bytes` (the rest):

```nomi
fn from_int(n: Int): Maybe<Byte>                 // 0..255 only
fn to_int(b: Byte): Int
fn length(data: Bytes): Int
fn at(data: Bytes, index: Int): Maybe<Byte>
fn slice(data: Bytes, start: Int, end: Int): Bytes
fn concat(a: Bytes, b: Bytes): Bytes
fn to_list(data: Bytes): List<Byte>
fn from_list(items: List<Byte>): Bytes
fn to_string(data: Bytes): Result<String, String> // UTF-8 decode
```

`Bytes` implements `Iter<Byte>`, `Add<Bytes, Bytes>`, `Display`, `Debug`,
`Equatable`, and `Hashable`. `Byte` intentionally does not implement arithmetic
operators; use `Byte.to_int` for numeric work.

### Display and Debug

`Display` and `Debug` are stdlib interfaces with the same signature shape but *distinct function names*, distinguished by audience:

```nomi
pub interface Display {
    fn to_string(value: self): String
} // for users

pub interface Debug {
    fn inspect(value: self): String
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
| `Map<K, V>` | `{Display(k) => Display(v), ...}`, `{=>}` for empty | `{Debug(...) => Debug(...), ...}` |
| `Maybe<T>` | `Some(Display(v))`, `None` | `Some(Debug(v))`, `None` |
| `Result<T, E>` | `Ok(Display(v))`, `Err(Display(e))` | `Ok(Debug(v))`, `Err(Debug(e))` |
| `Range<T>` | `Debug(start)..Debug(end)`, `Debug(start)..=Debug(end)`, `Debug(start)..` for unbounded integer ranges | identical |
| Tuple `(...)` | `(Display(a), Display(b), ...)` | `(Debug(a), Debug(b), ...)` |
| Anon struct `{f: V, ...}` | `{f: Display(v), ...}` | `{f: Debug(v), ...}` |

Tuples and anon structs are syntactic intrinsics with no named type to attach an impl to; they get language-level rendering (similar to how their literal syntax is privileged). Every other intrinsic gets a Nomi-side impl in stdlib.

Container impls require their elements to implement the corresponding interface — `${[some_fn]}` errors because functions have no `Display` impl. The constraint is enforced at the recursive dispatch (`Display.to_string(item)` inside the container's impl); a static `where T: Display` bound on the impl would not change runtime behavior.

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

- **Struct**: `TypeName{f1: Display(v1), f2: Display(v2), ...}`, `TypeName{}` for empty.
- **Enum (bare variant)**: `VariantName`.
- **Enum (positional arity n)**: `VariantName(Display(p0), Display(p1), ...)` — no enum-name prefix, matching how `derive Debug` formats variants.
- **Enum (struct payload)**: `VariantName{f: Display(v), ...}`.
- **Enum (embedded variant)**: bare delegation to the embedded value's Display.
- **Distinct primitive**: `TypeName(Display(inner))`.
- **Distinct tuple**: `TypeName(Display(inner.0), Display(inner.1), ...)`.
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

Iters are lazy by design; rendering the elements would have to consume the iterator (observable cost and state change, and no end for an infinite one), so inspecting one prints `<iter>` and leaves it unconsumed: it still yields every element afterwards. The placeholder is the same nested in a container (`[<iter>]`) and in a failed assertion's `values:` row. To see the elements, materialize first with `Iter.to_list(it)` and inspect the result. An `Iter<T>` position holding a source rather than a pipeline (`fn show(xs: Iter<Int>)` called with `[1, 2]`) holds that source, so it inspects as that value: `[1, 2]`. The same holds for a Range (`1..=3`), a value of a type with its own `impl Iter` (`Countdown{from: 3}`), and a collection of declared values (`[Point{x: 1, y: 2}]`).

For a declared type, the compile error names the type that lacks an impl, so the fix (add an `impl Display for T { ... }` block or `derive Display`) is clear from the message.

> **Status: not yet implemented.** For a function value or a lazy `Iter`,
> `"${f}"` and `io.print(f)` pass the type checker; the program is then
> refused before it runs with a "not supported yet" error (§25) rather than
> a type error.

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
fn process(id: Int): Result<Bool, String> {
    user = try users.get(id)
    profile = users.get_profile(user)
    try email.send(profile)
}
```

Or piped:

```nomi
fn process(id: Int): Result<Bool, String> {
    id
    |> try users.get()
    |> users.get_profile()
    |> try email.send()
}
```

Or with a binding `else`, which handles one step's failure in place (§10, *Bindings with `else`*):

```nomi
fn process(id: Int): Result<Bool, String> {
    Ok(user) = users.get(id) else {
        Err(e) -> return Err("loading user ${id}: ${e}")
    }

    try email.send(users.get_profile(user))
}
```

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
host fn abs(n: Int): Int

/// A value that may or may not be present.
enum Maybe<T> {
    Some T
    None
}
```

- No block comments
- Doc comments are preserved in the AST and available to tooling (LSP hover, documentation generation)
- A `///` comment with no declaration after it to document (above an `import` or a `test` block, for instance) is a parse error; write `//` or `//#` there

File-level documentation uses `//#`. To the
compiler these are ordinary line comments — they are **not** doc comments and do
**not** attach to any declaration — so a module description written as `//#` at
the top of a file won't bleed into the first declaration's hover. The reference
generator (`cmd/nomi-docgen`) collects a module's `//#` lines as its page intro:

```nomi
//# Operations on the persistent linked `List<T>` type. Use `Iter` owner
//# functions for lazy pipelines over any `Iter<T>`.

/// Returns the next element and remaining list, or None if empty.
pub fn list_next(list: List<T>): Maybe<(T, List<T>)> { ... }
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
- `..tail` after a comma spreads (cons): `[item, ..list]` is O(1), supports structural sharing. The spread must be the last element (`list spread ".." must be the last element`), because cons cells make prepending O(1) and appending O(n). The struct-update spread (§7) must be *first* instead; the rules differ because a list's is about representation and a record's is about notation.
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

`List`'s owner API is small: it carries only the ops that read or rebuild a `List`'s cons structure (`head`, `tail`, `concat`, `next_item`) plus the `Iter` protocol (`each_while`, `known_count`). **Every generic operation — transform, filter, reduce, sort, count, index access — is an `Iter` owner function** over any `Iter<T>`, and a `List` is an `Iter<T>`. The adapters are lazy and fuse; end a chain with `Iter.to_list()` to materialize a `List` back (one allocation, at the visible step):

```nomi
names = ["Alice", "Bob"]

names = ["Carol", ..names]

// lazy pipeline, strict terminal
[1, 2, 3, 4, 5]
|> Iter.filter(|x| x > 2)
|> Iter.map(|x| x * 2)
|> Iter.reduce(|acc = 0, x| acc + x)

// lazy pipeline, materialize back to a List
[1, 2, 3, 4, 5]
|> Iter.filter(|x| x > 2)
|> Iter.map(|x| x * 2)
|> Iter.to_list()
```

#### List functions (container-specific)

```nomi
fn head(list: List<T>): Maybe<T>          // first element (cons-list idiom)
fn tail(list: List<T>): Maybe<List<T>>    // all but the first
fn concat(a: List<T>, b: List<T>): List<T>
fn next_item(list: List<T>): Maybe<(T, List<T>)> // head and tail together
```

Plus the `Iter` protocol — `each_while`, and `known_count` (O(1), read from the cached length). Everything else is generic and lives on `Iter`:

```nomi
Iter.count(xs) // O(1) via known_count

Iter.first(xs) / Iter.last(xs) / Iter.at(xs, i)

Iter.empty?(xs) / Iter.not_empty?(xs)

Iter.reverse(xs) // → List

Iter.map(xs, f) |> Iter.to_list()

Iter.filter(xs, p) |> Iter.to_list()

Iter.take(xs, n) / Iter.drop(xs, n)

Iter.take_while(xs, p) / Iter.drop_while(xs, p)

Iter.flat_map(xs, f) / Iter.flatten(xss) / Iter.zip(a, b) / Iter.with_index(xs)

Iter.chunks(xs, n) / Iter.chunk_by(xs, key)

Iter.reduce(xs, f) / Iter.each(xs, f) / Iter.find(xs, p)

Iter.any?(xs, p) / Iter.all?(xs, p)

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

- Literal syntax: `#[1, 2, 3]`
- Homogeneous — all elements must be the same type
- Immutable — operations return new vectors
- `Vector.at`, `Vector.push`, `Vector.set` (returns `Maybe<Vector<T>>`,
  `None` for an index out of range), `Vector.concat` and `Vector.length` are
  container-specific operations. Converting is `Iter`'s job: `Iter.to_list(v)`
  and `Iter.to_vector(xs)`
- Generic transforms still go through `Iter`, then materialize explicitly when
  needed: `1..=3 |> Iter.map(f) |> Iter.to_vector()` builds the vector in one
  pass with no intermediate list

### Set

Collection of unique values, backed by `Map`. Elements may be any keyable value,
same as map keys. Iteration follows first-occurrence insertion order.

```nomi
roles = #{"admin", "editor"}

empty: Set<String> = #{}
```

- Literal syntax: `#{1, 2, 3}`
- Empty set literals need type context
- Homogeneous — all elements must be the same type
- Immutable — operations return new sets
- `Set.insert`, `Set.remove`, `Set.contains?`, `Set.union`,
  `Set.intersection`, `Set.difference`, `Set.subset?`, and `Set.size` are
  container-specific operations
- `a + b` is `Set.union(a, b)` and `a - b` is `Set.difference(a, b)`; both
  operands are sets, so one element is `s + #{x}`
- `Set<T>` implements `Equatable` and `Hashable`, but not `Comparable`

### Map

Key-value collection. Any keyable value can be a key (safe because all values are immutable). Functions and values holding functions cannot be keys since they don't support `==`.

```nomi
ages = {"Alice" => 30, "Bob" => 25}

name = Map.get(ages, "Alice") // Maybe<Int>

updated = Map.put(ages, "Carol", 28) // new map with Carol added

updated = Map.merge(ages, {"Alice" => 31, "Bob" => 26}) // new map with values changed
```

- Literal syntax: `{key => value}`
- `=>` separates keys from values — distinguishes maps from anonymous structs (which use `:`)
- Keys are always expressions — string literals require quotes, bare identifiers are evaluated as variables, and any keyable value works (tuples, variants, lists, structs, distinct types), not just scalars
- `Map.get` returns `Maybe` since key might not exist
- `Map.put` adds a single key-value pair, `Map.merge` combines two maps (second map wins on conflicts)
- `a + b` is `Map.merge(a, b)`; both operands are maps, so one entry is `m + {k => v}`. There is no `Map - Map`
- Operations return new maps
- Maps iterate in insertion order. `Map.put(m, k, v)` appends the entry at the end when `k` is new, or preserves position when `k` already exists (only the value is updated). `Map.merge(a, b)` preserves `a`'s order, then appends any new keys from `b` in `b`'s order.
- Maps have **no spread**. `{..m, "b" => 2}` is `map literals do not support spread ".."; use Map.merge` — `Map.merge` already covers it and the struct-update spread (§7) is a record form only.

```nomi
{"name" => "Alice"} // string literal key

{key => "Alice"} // variable key — evaluates key

{String.to_lower(k) => "Alice"} // any expression as key

{(1, 2) => "origin"} // composite key — tuple

{Color.Red => "danger"} // composite key — variant

{Point{x: 1, y: 2} => "p"} // composite key — struct
```

#### Map Functions

```nomi
// Construction & access
fn empty(): Map<K, V>
fn get(m: Map<K, V>, key: K): Maybe<V>
fn put(m: Map<K, V>, key: K, value: V): Map<K, V>
fn remove(m: Map<K, V>, key: K): Map<K, V>
fn merge(a: Map<K, V>, b: Map<K, V>): Map<K, V>
fn keys(m: Map<K, V>): List<K>
fn values(m: Map<K, V>): List<V>
fn contains_key?(m: Map<K, V>, key: K): Bool
fn size(m: Map<K, V>): Int                       // O(1); the generic count is Iter.count
fn map_next(m: Map<K, V>): Maybe<((K, V), Map<K, V>)> // first entry and the rest

// Map-specific transformations — generic map/filter can't keep the other half
// of a pair (or could collide keys), so these stay on Map
fn map_values(m: Map<K, V>, f: (V) -> U): Map<K, U>
fn map_keys(m: Map<K, V>, f: (K) -> J): Map<J, V>
```

Maps iterate in insertion order — preserved by every transformation. A map element is a `(K, V)` pair, so every generic `Iter` op (`Iter.reduce` / `Iter.each` / `Iter.find` / `Iter.filter` / …) takes a single tuple parameter. To get a `Map` back from a lazy pipeline, materialize with `Iter.to_map`; filtering or generic mapping a map is `Iter.filter(m, p) |> Iter.to_map()`.

`Map<K, V>` implements `Iter<(K, V)>`, so every `Iter` owner function works on maps. To get a `Map` back from a lazy pipeline, use `Iter.to_map`:

```nomi
// Filter a map — Iter.filter returns Iter<(K, V)>, materialize with Iter.to_map
ages
|> Iter.filter(|(_, age)| age > 18)
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
|> Iter.filter(|n| n % 7 == 0)
|> Iter.take(5)
|> Iter.to_list() // [7, 14, 21, 28, 35]

// Unbounded range bounded by Iter.take
Range.from(1) |> Iter.take(5) |> Iter.to_list() // [1, 2, 3, 4, 5]
```

#### Range functions (container-specific)

```nomi
fn contains?<T>(r: Range<T>, n: T): Bool
fn bounded?<T>(r: Range<T>): Bool
fn from(n: Int): Range<Int>              // unbounded ascending from n
fn naturals(): Range<Int>                // unbounded from 0
fn step_by<T, S>(r: Range<T>, by: S): Iter<T> where T: Comparable and Steppable<S>
```

Plus the `Iter` protocol for ranges whose endpoint type implements `Discrete`: `Iter.known_count(r)` reports `Some(n)` for bounded discrete ranges that can count steps and `None` for an unbounded one (the safe, non-consuming bounded count). Everything else is generic over iterables — materialize with `Iter.to_list(r)` (hangs on an unbounded range; bound with `Iter.take` first), count with `Iter.count(r)`.

### Iter Interface

The single iteration protocol is `Iter<T>`, defined in `std/iter`. It is pure protocol — two functions, no algorithms:

```nomi
interface Iter<T> {
    fn each_while(collection: self, yield: (T) -> Bool): Bool

    // Some(n) if this iterator can report its length without consuming it
    // (a type that stores its count); None otherwise (lazy pipeline) or for an
    // unbounded source. Iter.count consults it for an O(1) count.
    open fn known_count(_collection: self): Maybe<Int> {
        None
    }
}
```

`each_while` walks the collection and pushes each element to `yield`; returning `False` from `yield` asks the source to stop, and `each_while` itself answers `True` when the source ran out and `False` when a consumer stopped it. That single Bool is the whole early-termination mechanism: `Iter.take(3)` over an infinite source stops it by refusing a fourth element, and `find`/`any?`/`all?`/`first` stop at the first decisive one. Because `each_while` reads a value it never mutates, Nomi iterators are replayable. An `Iter<T>` is an ordinary value: a binding, a parameter or result, a struct field, a tuple or record part, a `Some`/`Ok` or enum payload, a list element and a closure capture can all hold one. Holding it runs nothing, and each consumer runs it from its start, so a field read twice with `Iter.to_list` yields the same list twice. Any source can enter a position declared `Iter<T>` — a `List`, `Vector`, `Set`, `Map`, `String`, `Bytes`, range or a user type that implements `Iter` — and it keeps its own `known_count` there: `Iter.known_count(xs)` for an `xs: Iter<Int>` holding `[1, 2, 3]` is `Some(3)`. `List`, `Vector`, `Map`, `String` (via the grapheme iterator), and ranges over `Discrete` values all implement `Iter<T>` — so they feed the `Iter` owner functions directly. `List`/`Vector`/`Map`/`Set` and bounded ranges whose element type can count steps override `known_count`; everything else inherits the `None` default.

Implementing it means walking your own structure and stopping as soon as `yield` says to:

```nomi
impl Iter for Tree {
    fn each_while(t: Tree, yield: (Int) -> Bool): Bool {
        case t {
            .Leaf ->
                True

            .Node(left, value, right) ->
                if each_while(left, yield) {
                    if yield(value) { each_while(right, yield) } else { False }
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
2. **Container-specific ops** stay on the container owner — exactly the work a generic adapter can't express because it reads or rebuilds the container's structure: `Map.map_values`/`map_keys`/`merge`/`get`/`put`/`size`, `Set.union`/`intersection`/`difference`/`insert`/`size`, `List.concat`/`head`/`tail`, `Vector.at`/`push`/`set`/`concat`/`length`, `String.split`/`replace`/`slice`/`join`, native `String.reverse`.

There are **no eager collection-adapter functions** (`List.map`, `Set.filter`, … don't exist): to get a container back from a chain, append an explicit materialize step — `Iter.map(xs, f) |> Iter.to_list()`, `Iter.filter(s, p) |> Iter.to_set()`. The cost (one allocation) is then visible at the call site.

### `std/iter`

The `std/iter` file hosts the `Iter<T>` interface and its owner functions. The protocol surface is only `each_while` (required) and the lone `open` default `known_count` (`Maybe<Int>`; `None` unless a type stores its size). Every generic iteration op lives on `Iter`, reached `Iter.X(...)`:

- **Adapters** (lazy, return `Iter<...>` — one closure over the upstream's push loop, allocated when the chain is built rather than rebuilt per element, so pipelines fuse): `map`/`filter`/`take`/`drop`/`take_while`/`drop_while`/`flat_map`/`zip`/`cycle`/`concat`/`with_index`/`chunks`/`chunk_by`.
- **Terminals** (consume, produce a scalar): `reduce`/`find`/`each`/`any?`/`all?`/`empty?`/`not_empty?`/`first`/`last`/`at`/`count`/`sort`/`sort_by`/`sort_with`/`partition`/`group_by`/`frequencies`.
- **Materializers / constructors** (consume or seed): `to_list`/`to_vector`/`to_set`/`to_map`/`reverse`/`flatten`/`from`/`repeat`/`iterate`.

Because they're owner functions, `Iter.map` / `Iter.reduce` are not dispatch calls — the protocol functions remain `Iter.each_while` and `Iter.known_count`. `Iter.count` is O(1) on any source that stores or can compute its count (`List`/`Vector`/`Map`/`Set`/bounded countable ranges, via `known_count`) and an O(n) fold otherwise; like every full-consuming terminal it **does not terminate on an infinite source** — bound it with `Iter.take(n)` first.

**Infinite constructors** (return iterators, consume nothing):

```nomi
fn from(start: Int): Iter<Int>          // start, start+1, start+2, ...
fn repeat(x: T): Iter<T>                // x, x, x, ...
fn iterate(seed: T, step: (T) -> T): Iter<T>   // seed, step(seed), step(step(seed)), ...
```

**Non-terminal ops** (return iterators — lazy, fuse with other iter ops):

```nomi
fn map(source: Iter<T>, f: (T) -> U): Iter<U>
fn filter(source: Iter<T>, f: (T) -> Bool): Iter<T>
fn flat_map(source: Iter<T>, f: (T) -> Iter<U>): Iter<U>
fn take(source: Iter<T>, n: Int): Iter<T>
fn take_while(source: Iter<T>, f: (T) -> Bool): Iter<T>
fn drop(source: Iter<T>, n: Int): Iter<T>
fn drop_while(source: Iter<T>, f: (T) -> Bool): Iter<T>
fn cycle(source: Iter<T>): Iter<T>
fn concat(a: Iter<T>, b: Iter<T>): Iter<T>
fn with_index(source: Iter<T>): Iter<(Int, T)>
fn zip<A, B>(a: Iter<A>, b: Iter<B>): Iter<(A, B)>
fn chunks(source: Iter<T>, size: Int): Iter<List<T>>
fn chunk_by<T, K>(source: Iter<T>, key_fn: (T) -> K): Iter<List<T>> where K: Equatable
```

**Terminal ops** (consume the iterator, produce a scalar):

```nomi
fn reduce<T, U>(source: Iter<T>, f: (U, T) -> U): U
fn each(source: Iter<T>, f: (T) -> Unit): Unit
fn find(source: Iter<T>, f: (T) -> Bool): Maybe<T>
fn any?(source: Iter<T>, f: (T) -> Bool): Bool
fn all?(source: Iter<T>, f: (T) -> Bool): Bool
fn empty?(source: Iter<T>): Bool
fn not_empty?(source: Iter<T>): Bool
fn first(source: Iter<T>): Maybe<T>
fn last(source: Iter<T>): Maybe<T>
fn at(source: Iter<T>, index: Int): Maybe<T>
fn count(source: Iter<T>): Int                   // O(1) via known_count; else O(n) fold
fn sort<T>(source: Iter<T>, direction: Direction = Direction.Ascending): List<T> where T: Comparable
fn sort_by<T, K>(source: Iter<T>, direction: Direction = Direction.Ascending, key: (T) -> K): List<T> where K: Comparable
fn sort_with(source: Iter<T>, compare: (T, T) -> Ordering): List<T>
fn partition(source: Iter<T>, f: (T) -> Bool): (List<T>, List<T>)
fn group_by<T, K>(source: Iter<T>, key_fn: (T) -> K): Map<K, List<T>>
fn frequencies<T>(source: Iter<T>): Map<T, Int>      // keys in first-appearance order
```

**Materializers** (consume the iterator, produce a concrete collection):

```nomi
fn to_list(source: Iter<T>): List<T>             // identity-preserving for a List (O(1))
fn to_vector(source: Iter<T>): Vector<T>         // one pass, sized by known_count; identity for a Vector
fn to_set(source: Iter<T>): Set<T>
fn to_map(source: Iter<(K, V)>): Map<K, V>       // requires (K, V) pair elements
fn reverse(source: Iter<T>): List<T>
fn flatten(source: Iter<Iter<U>>): List<U>
```

To join an `Iter<String>` into one `String`, use `String.join(parts, separator)`.

`Iter.to_map` and `String.join` enforce their element-type constraints at compile time, not just runtime. Each `impl Iter for X { ... }` block registers `X`'s element type `E` with the type checker; when a parameter's element-type slot is `(K, V)` or `String`, the unifier rejects sources whose `E` doesn't fit:

```nomi
Iter.to_map([1, 2, 3]) // type error: Iter<(K, V)> expected, got Iter<Int>

String.join([1, 2, 3], ", ") // type error: Iter<String> expected, got Iter<Int>
```

This works for built-in iteration sources (`List`, `Vector`, `Map`, `String`, `Range`) and for any user-defined `Iter` impl — the unifier consults file-local impl element types first, then the package-wide stdlib registry.

`break` and `continue` work in all `Iter` callbacks — same semantics as everywhere else (see §12). Short-circuit terminals (`find`, `any?`, `all?`, `empty?`, `first`) terminate on infinite sources when the predicate permits; full-consuming terminals (`count`, `reduce`, `last`, `sort`, `to_list`, …) do not — bound the source with `Iter.take(n)` first.

`Iter.zip` truncates to the shorter iterator:

```nomi
Iter.zip([1, 2, 3], ["a", "b"]) |> Iter.to_list() // [(1, "a"), (2, "b")]
```

### Custom Iters

Any type can implement `Iter<T>` and immediately work with every `Iter` owner function. Implement `each_while` to walk your own structure, pushing each element to `yield` and stopping as soon as it returns `False`:

```nomi
// File: countdown.nomi
pub struct Countdown {
    remaining: Int
}

impl Iter for Countdown {
    fn each_while(c: Countdown, yield: (Int) -> Bool): Bool {
        if c.remaining <= 0 {
            True
        } else {
            if yield(c.remaining) {
                each_while(Countdown{remaining: c.remaining - 1}, yield)
            } else {
                False
            }
        }
    }
}

pub fn new(n: Int): Countdown {
    Countdown{remaining: n}
}

```

Once `Iter<T>` is implemented, every `Iter` owner function works on it. Non-terminal ops still return iterators — materialize with `Iter.to_list` (or a similar terminal) when you need a concrete value:

```nomi
countdown.new(5) |> Iter.map(|n| n * 10) |> Iter.to_list() // [50, 40, 30, 20, 10]

countdown.new(4) |> Iter.filter(|n| n > 2) |> Iter.to_list() // [4, 3]

countdown.new(3) |> Iter.reduce(|a, b| a + b) // 6

countdown.new(5)
|> Iter.filter(|n| n % 2 == 0)
|> Iter.map(|n| "${n} bottles")
|> Iter.to_list()
// ["4 bottles", "2 bottles"]
```

Each lazy adapter returns a private wrapper struct that itself implements `Iter`, so a chain of adapters fuses into one pass, and `break`/`continue` inside an adapter's callback propagate through the fused pipeline to the consuming terminal.

### Tuples

Tuples are fixed-size, positional and heterogeneous; see §7 *Tuples*.

### Anonymous Structs

Anonymous structs always have named fields. See §7 for full syntax.

```nomi
point = {x: 1.0, y: 2.0}

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
    user = Task.spawn(|| fetch_user(42))
    order = Task.spawn(|| fetch_order(101))
    Ok((try Task.await(user), try Task.await(order)))
}
// only reached when both tasks are done
```

### Task

`Task<T>` is the opaque handle returned by `Task.spawn`. It carries a single type parameter — the body's return type. Errors-as-values via `Result<T, E>` are the universal Nomi idiom, so a fallible task body returns `Result<T, E>` and `try Task.await(t)` propagates errors the same way `try fetch_user(42)` does. Besides `Task.await`, a handle can be cancelled with `Task.cancel` and inspected with `Task.outcome` (see *Failures And Restarts* below).

`Task<T>` lives in `std/tasks`; import it with `import std/tasks.Task`. Task operations are owner functions on `Task`: `Task.spawn(...)`, `Task.await(...)`, `Task.spawn_all(...)`, `Task.await_all(...)`, `Task.outcome(...)`, and `Task.cancel(...)`.

### Task.spawn

`Task.spawn(body)` spawns `body` (a zero-parameter function value, conventionally written as a lambda: `|| expr`) as a parallel task in the enclosing `concurrent { }` block. The body runs on its own goroutine; values captured from the enclosing scope are shared immutably. Returns `Task<T>` where `T` is the body's return type.

```nomi
concurrent {
    user  = Task.spawn(|| fetch_user(42))        // Task<Result<User, String>>
    order = Task.spawn(|| fetch_order(101))      // Task<Result<Order, String>>
    ...
}
```

For work that doesn't return `Result`, the task type is the body's return type directly:

```nomi
concurrent {
    sums = Task.spawn(|| Iter.map(numbers, |n| n * 2) |> Iter.to_list())   // Task<List<Int>>
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
    a = Task.spawn(|| fetch_user(42))
    b = Task.spawn(|| fetch_order(101))

    // try propagates Err and cancels b as cleanup
    u = try Task.await(a)

    // case handles the error — b is not affected
    o = case Task.await(b) {
        Ok(val) -> val
        Err(_) -> default_order
    }

    Ok((u, o))
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
        a = Task.spawn(|| slow_call())
        b = Task.spawn(|| slow_call())
        _ = Task.await(a)
        _ = Task.await(b)
    }
}
```

Exceeding that deadline is not a value the block hands back. The work unwinds at its next safe point and everything after it in the deadline's region, the rest of the block holding the `with` line, is skipped. A check written after the `concurrent` block in the same block cannot report the timeout: it sits inside the region the deadline covers and unwinds along with the work. To observe which happened, put the deadline inside a spawned task and read `Task.outcome` from outside the region the deadline covers.

### Static guarantees

The analyzer enforces three structural rules at compile time. Together they statically rule out the leaked-goroutine class of bugs:

1. **`Task.spawn` is legal only inside the dynamic extent of a `concurrent { }` block.** Reaches transitively — `Task.spawn(|| helper())` where `helper` itself calls `Task.spawn` is fine *if* the chain bottoms out inside a `concurrent` block. Calls to `Task.spawn` outside any block are rejected at analysis time.
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

fn produce(out: Sender<Int>, n: Int): Unit {
    case Sender.send(out, n) {
        Ok(u) -> u
        Err(_) -> io.print("send failed")
    }
}

fn consume(inbox: Receiver<Int>): Int {
    Iter.loop(|sum: Int = 0|
        case Receiver.receive(inbox) {
            Some(v) -> sum + v
            None -> break sum
        }
    )
}

fn main(): Unit {
    ch: Channel<Int> = Channel.buffered(4)

    total = concurrent {
        p1 = Task.spawn(|| produce(ch.sender, 10))
        p2 = Task.spawn(|| produce(ch.sender, 20))
        p3 = Task.spawn(|| produce(ch.sender, 30))
        cons = Task.spawn(|| consume(ch.receiver))

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

    p1 = Task.spawn(|| Sender.send(events.sender, Event.UserMsg("hello")))
    p2 = Task.spawn(|| Sender.send(events.sender, Event.Tick(1)))

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

fn boot(): Config {
    Config{
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
fn handle_order(id: Int): Response {
    response = build_response(id)
    _ = Supervisor.spawn(App.audit, || record_audit(id, response))
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
fn finish_batch(): Unit {
    _ = Supervisor.spawn(App.audit, || record_audit(1, "accepted"))
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

A body returning `Err(e)` is still `Completed(Err(e))`; it produced a value. `Failed` is for unplanned breakage such as a panic or runtime error.

Supervisors can restart failed work according to their restart policy. Cancellation is not a failure, so cancelling a task does not fight shutdown or restart loops.

#### Concurrency Scenarios

Every concurrent task in Nomi is either owned by a `concurrent` block or by a supervisor. No orphaned work.

**Parallel fetch** — fire N requests, collect results. `concurrent` + `Task.spawn`/`Task.await`:

```nomi
concurrent {
    user = Task.spawn(|| fetch_user(id))
    order = Task.spawn(|| fetch_order(id))
    u = try Task.await(user)
    o = try Task.await(order)
    Ok(build_response(u, o))
}
```

**Producer/consumer pipeline** — multiple producers feed a channel, one consumer processes:

```nomi
concurrent {
    ch = Channel.buffered<Page>(10)

    producers = Iter.map(urls, |url| {
        Task.spawn(|| {
            url
            |> try fetch()
            |> try parse()
            |> Sender.send(ch.sender, _)
            |> Result.map_err(|_| "channel closed")
        })
    }) |> Iter.to_list()

    consumer = Task.spawn(|| Iter.loop(|acc = Ok(0)| {
        case Receiver.receive(ch.receiver) {
            None ->
                break acc

            Some(page) -> {
                count = try acc
                try database.save(db, page)
                Ok(count + 1)
            }
        }
    }))

    _ = Task.await_all(producers)
    Sender.close(ch.sender)
    Task.await(consumer)
}
```

**Long-running/background work** — enqueue audit, email, or periodic work under a named supervisor:

```nomi
fn handle_signup(user: User): Response {
    _ = Supervisor.spawn(App.notify, || send_welcome(user))
    Response{status: 202}
}
```

**Periodic task** — poll an API, clean up stale data, or drain an outbox:

```nomi
fn outbox_worker(): Unit {
    Iter.loop(|| {
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
    fn add(lhs: self, rhs: Rhs): Out
}

type Day Int
type Days Int

impl Add<Days, Day> for Day {
    fn add(day: Day, rhs: Days): Day {
        Day(Int(day) + Int(rhs))
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
There is no collection-plus-element form; a single element is `s + #{x}`,
`list + [x]` or `m + {k => v}`.

An operator impl may be generic over the receiver's type parameters, and its
interface arguments may name them. The arguments are read at the left
operand's type, so `Set<Int>`'s `Add<Set<T>, Set<T>>` takes a `Set<Int>` and
returns a `Set<Int>`:

```nomi
impl Add<Set<T>, Set<T>> for Set<T> {
    fn add(a: Set<T>, rhs: Set<T>): Set<T> {
        Set.union(a, rhs)
    }
}

#{1, 2} + #{2, 3} // #{1, 2, 3}
#{1} + 2 // error: binary + right operand mismatch: Add expects Set<Int>, got Int
```

The same holds for a user type (`impl Add<T, Box<T>> for Box<T>`) and inside
generic code, where `a + b` with `a: Set<T>` and `b: Set<T>` is `Set<T>`.

**Integer overflow traps.** `Int` is 64-bit. The arithmetic operators `+`, `-`, `*` (and `/` in the single `MinInt64 / -1` case) raise a runtime error on signed overflow rather than wrapping silently — the same fail-loud stance as division by zero. When modular (wrapping) arithmetic is the intent — hashing, checksums, ring counters — use the explicit `Int.wrapping_add` / `Int.wrapping_sub` / `Int.wrapping_mul` functions, which wrap two's-complement. `derive Hashable` uses wrapping multiply internally for exactly this reason.

**No implicit numeric conversion.** `Int`, `Float`, and `Decimal` are three non-mixing islands — none mixes implicitly with either of the others. Mixed-operand arithmetic (`1 + 2.0`, `1 + 1.50d`, `2.0 + 1.50d`), comparison (`1 < 2.0`, `1 < 2.50d`), and equality (`1 == 1.0`, `1.0 == 1d`) are all compile errors; convert explicitly across the boundary with `Int.to_float` / `Float.to_int` / `Decimal.from_int` / `Decimal.to_float` / `Decimal.from_float`. This avoids the silent precision loss that implicit `Int`→`Float` widening causes for magnitudes above 2^53. More generally, `==` / `!=` require operands of the same type — comparing unrelated types (e.g. `String` to `Int`) is a compile error, not a silent `false`.

**Integer division and modulo.** `/` truncates toward zero and `%` takes the sign of the dividend, so the identity `(a / b) * b + (a % b) == a` holds (`-7 / 3 == -2`, `-7 % 3 == -1`). When the divisor is proven non-zero at compile time, prefer the total `Int.divide` / `Int.modulo`, which take a `NonZeroInt` (the bare `/` `%` operators raise a runtime error on a zero divisor). `Int.divide` is a `Divide<NonZeroInt, Int>` impl; the ordinary `/` operator uses `Divide<Int, Int>`. Modulo is not defined on `Float`.

**Decimal arithmetic.** `Decimal` is the third blessed operator type (§*Primitive Types*): `+ - * /` and `< > <= >= == !=` work on two `Decimal` operands and yield a `Decimal` / `Bool`. `+ - *` are always **exact** — they never round; the scale grows as needed (`1.50d + 1.5d == 3.00d`, `1.50d * 1.5d == 2.250d`). The `/` operator is **exact-or-trap**: a quotient that terminates in base 10 just works at its preferred scale (`10.00d / 4d == 2.50d`), but a non-terminating quotient (`10d / 3d`) raises a runtime error pointing at `Decimal.divide(a, b, scale, mode)` — the explicit-rounding division path — rather than silently rounding. Division by zero is a runtime error (no Infinity). There is **no `%`** on `Decimal`. Equality and comparison are value-based and scale-insensitive (`1.50d == 1.5d` is `True`); scale is preserved only for display (`Decimal.scale(1.50d) == 2`, `Decimal.to_string(1.50d) == "1.50"`). Decimal literals carry the `1.50d` / `1.50D` suffix.

**Comparison:**
`==` `!=` `<` `>` `<=` `>=`

`==` / `!=` work on any two operands of the same type — structural equality is the universal fallback. The ordering operators `<` `>` `<=` `>=` require the operand type to have a declared `Comparable` impl (`derive Comparable` or an `impl Comparable for T` block); using them on a type without one is a compile-time missing-impl error. See §30 for the rationale behind the asymmetry.

**Logical:**
`and` `or` (keywords) `!` (negation)

**Other:**

- `|>` — pipe
- `try` — unwrap Result/Maybe (prefix keyword)
- `=` — binding
- `.` — member access
- `+` — `Add` addition/concatenation

## 23. Closures

Lambdas capture values from enclosing scope naturally. Full immutability means no concerns about captured values changing.

```nomi
fn make_greeter(greeting: String): (String) -> String {
    |name| "${greeting}, ${name}!"
}

hello = make_greeter("Hello")

hello("Alice") // "Hello, Alice!"
```

Closures are flat: a lambda, a nested `fn` or a task's body copies the value each name it reads holds when the closure is created, and reads those copies from then on. A later binding of the same name, including a same-scope shadow (§2), is a new binding that does not reach the closure:

```nomi
x = 1
f = || x
x = 2
f() // 1
```

A nested `fn` that calls itself refers to itself through its own closure, not through the enclosing scope, and every other name it reads is captured by value like any closure's. A nested `fn` can call only `fn`s declared before it, so nested `fn`s are never mutually recursive.

Types on anonymous functions are inferred from context. If the compiler can't infer, annotations are required:

```nomi
// inferred — compiler knows Iter.map expects (Int) -> Int
[1, 2, 3] |> Iter.map(|n| n * 2)

// explicit — when context isn't enough
f = |x: Int| x * 2
```

## 24. Standard Library

### Host And Foreign Declarations

Stdlib types and functions are declared in `.nomi` files using the `host` keyword. This tells the compiler the signature exists but the implementation is provided by the runtime (Go host):

```nomi
/// 64-bit integer.
host type Int

/// Returns the absolute value of n.
host fn abs(n: Int): Int
```

- `host type Name` — declares a host-provided type with no user-visible fields or body. Host operations live as file/module `host fn` declarations or impl-block items. Derives are sibling `derive Iface for Name` declarations. Manual interface implementations live in sibling `impl Iface for Name { ... }` blocks.
- `host fn name(params): ReturnType` — declares a function signature with no body (implementation is host-provided). Also legal as an impl-block item, where the host key is derived as `module.Type.function`.
- Host declarations can have doc comments (`///`) like any other declaration.
- User-facing Go interop binds ordinary Go packages through `gopkg` declarations. A declaration such as `gopkg "example.com/app/ffi" as ffi` introduces a compile-time package handle whose import path is resolved by Go's normal module machinery. The path must be a valid Go import path. A Go standard library package (`gopkg "strings"`) needs no `go.mod` and no Go package of the project's own: its source is the Go toolchain's, and a script binding only the standard library runs from a directory with no `go.mod` at all. A package internal to the standard library (`internal/abi`) is a check error, since no other module may import it. Any other path must be provided by the nearest `go.mod` above the declaring file: a package of the project's own module, or a package of a module that `go.mod` requires or replaces. Any other is a check error at the path, such as `gopkg "callback:= f(s": not a valid Go import path: invalid char ':'`, or one naming the project's module for a path outside all of those. A Go type binding must be opaque. It may remain private as a raw implementation handle (`opaque type RawConn go ffi.Conn`) behind a Nomi facade, or it may be intentionally public as a public opaque foreign type (`pub opaque type Regex go ffi.Regex`) when the module deliberately commits to that Go-backed representation. Public opaque foreign types can be named and passed across the module boundary but cannot be constructed, unwrapped, or inspected structurally. A Go function binding maps an exported Go function into a local Nomi function name: `fn open_raw(path: String): Result<RawConn, String> go ffi.OpenRaw`. The alias in `go alias.Symbol` must be one a `gopkg` declaration of the same file introduces; any other is a check error at the alias.
- A program with `gopkg` bindings needs a Go toolchain: `nomi run`, `nomi check` and `nomi test` build a cached Go wrapper that links the bound packages (see [`implementation-notes.md`](implementation-notes.md#go-ffi-wrapper)). Pure-Nomi programs and tests need no toolchain.
- Every std declaration is a bare `host fn` / `host type`, so importing any std module, Go-backed ones such as `std/regex` included, needs no Go toolchain — see "The rule: `host` for the standard library, `go` for user FFI" below. A user's own Go binding is declared in Nomi source with `gopkg` and `go alias.Symbol`; there is no Go-side registration function. A Go program that embeds Nomi answers the `host fn`s its program declares from a host table passed to `vmhost.Load` (`vmhost.WithHosts`). A plain `host type` in such a program is an opaque handle: the table's functions create and read its values, which Nomi code passes around and stores and reads only through those functions, and the type itself needs no registration.
- Go binding signatures project Nomi `String` → Go `string`, `Bool` → `bool`, `Int` → `int64` (Go `int` and the sized signed widths project to `Int`, as do the unsigned widths other than `uint8` when the value fits `int64`; every narrowing is range-checked in both directions rather than truncated), `Byte` → `uint8` (`Byte` is its own Nomi type, so an `Int` does not fill a `uint8` parameter and a `Byte` does not fill an `int64` one), `Float` → `float64` (a `float32` slot is range-checked; `NaN` and `±Inf` cross unchanged, and `NaN` stays reflexive under Nomi's equality so it remains usable as a map key), `Bytes` → `[]byte`, `Duration` → `time.Duration`, `Instant` → `time.Time` (an `Instant` is nanoseconds since the Unix epoch, so a `time.Time` outside that window is rejected rather than wrapped, and an `Instant` returns to Go as UTC — the projection carries no zone and no monotonic reading, so two round-tripped times are `Equal`, not `==`), `List<T>` → `[]T`, `Map<K, V>` → `map[K]V` (the projected key must be a valid Go map key; a Nomi `Map` iterates in a defined order and a Go map has none, so a Go map projects with its keys in ascending order rather than inheriting Go's randomized walk, and two keys that would collapse into one on either side fail the conversion rather than silently drop an entry), `Maybe<T>` → `*T`, a declared Nomi struct → a Go struct with the matching exported fields (snake_case by default, overridable with a `nomi:"..."` tag), where the Nomi type identity always comes from the declaration at that position — the `host fn`'s declared return type, or the declared parameter type of the Nomi function being called — never from the Go type, because `Point` and `Vec` are one Go shape and a struct with no identity misses every impl keyed on it, bound opaque Go types → their registered Go pointer type, and direct callback parameters `(A, ...) -> R` → `func(A, ...) R`. A user-declared `enum` has no Go projection: the only enums that cross are `Result`, `Maybe`, and `Bool`, as a trailing Go `error`, `*T`, and `bool`, and they are matched on the stdlib type's own runtime identity — a same-named type from another module is a different type and is rejected rather than projected as the stdlib one. The return position is driven by the Go signature: counting the non-`error` results as `N`, `N == 0` projects to `Unit`, `N == 1` to that value, and `N >= 2` to the tuple `(A, B, ...)`; an optional trailing `error` wraps whatever that produced in `Result<_, String>`, so `error` alone is `Result<Unit, String>`, `(T, error)` is `Result<T, String>`, and `(A, B, error)` is `Result<(A, B), String>`. `N` is uncapped, so a Go function that naturally returns several values needs no carrier struct. Tuples project one way only: a tuple in *parameter* position is rejected because Go has no tuple type to bind it to, and a plain Go struct against a Nomi struct of the same shape already carries several values in both directions. Type/function bindings are preflighted against the Go source of the project's own packages, of packages reached through a local `replace`, and of bound standard library packages (read from the toolchain's GOROOT with this platform's build constraints) for missing bound names, generic Go functions (a binding needs one concrete signature, so bind a non-generic Go function that calls the generic one) and signature mismatches. Unsupported projected shapes fail before program load with a diagnostic that points back to the binding declaration.
- An opaque distinct type is unwrapped to its representation before the projection table is consulted, so `pub opaque type Meters Int` crosses as whatever `Int` crosses as. That is what gives `Duration` and `Instant` — both `opaque type ... Int` in the stdlib — a second legal Go spelling alongside `time.Duration` and `time.Time`: an `int64` slot carries either one. A distinct type therefore has no *unique* Go spelling, which is what the preflight below relies on.
- The projection is checked in the FORWARD direction only, and only where it is unique. For each declared Nomi type the boundary computes the Go type it projects to and compares that against the Go type actually bound, read from the bound Go declaration's signature. A declaration whose projection is not unique is skipped rather than guessed: `Dynamic` (every Go type can carry one), `Unit` (no value to carry), an opaque distinct type (see above), a struct (a shape, not a type), a type parameter, a bound opaque Go type, and a Go interface slot at any depth. The inverse direction is deliberately NOT checked: a Go type does not determine the Nomi type it must be — `int64` is `Int` and equally every distinct over `Int`, `any` is `Dynamic` and equally any handle, a Go struct is any Nomi struct with matching fields — so an inverse check would reject declarations that work, which is worse than a mismatch reported late. Disagreements the forward rule proves — a declared `String` against a Go `int64`, in a parameter, a return, a tuple element, a `Result` payload, or a callback's own argument or return — are rejected at load, naming both sides and what the declaration projects to.

**The rule: `host` for the standard library, `go` for user FFI.** Every
declaration in `std/` whose implementation is Go writes `host fn` or
`host type`, whatever Go package that implementation lives in. `fn f(x: T): U
go pkg.Sym` is the spelling a USER writes to bind their own Go code, alongside
`gopkg "import/path"`.

`host` means the implementation is outside Nomi and the standard library owns it; where the Go code sits, and why std does not use the `go` spelling, is in [`implementation-notes.md`](implementation-notes.md#why-std-uses-host-rather-than-go).

The stdlib `.nomi` files are embedded in the compiler binary and analyzed once per process. Each is an ordinary file reached with a `std/` import path: `import std/io` binds the `io` file API object, `import std/calendar.{self, Date}` binds the file and a type, and `import std/io.print` binds one function bare. Owner functions on a prelude type (`String.length`, `Map.get`) need no import.

### Prelude

One stdlib file, `std/prelude.nomi`, has a special role: it lists the imports every user file gets implicitly. It is itself a regular Nomi file containing only `import` statements — no special syntax. After every other stdlib module loads, the analyzer and runtime each evaluate `prelude.nomi`; the resulting module scope becomes the parent scope of every user file (analysis side) and the bindings are written into the root environment (runtime side). The set is intentionally minimal: primitives (`Int`/`Float`/`Decimal`/`String`/`Unit`/`Infallible`/`Byte`/`Bytes`/`Codepoint`/`List`/`Vector`/`Map`/`Set`), `Bool`/`True`/`False` from `std/bool`, `Maybe`/`Some`/`None`, `Result`/`Ok`/`Err`, the `Range` struct (so range literals work everywhere), the core interfaces (`Add`/`Subtract`/`Multiply`/`Divide`/`Comparable`/`Display`/`Equatable`/`Hashable`/`Discrete`/`Iter`/`Steppable`/`Struct`, plus universal `Debug`), the `Ordering` and `Type` types, and the application lifecycle types `Startup` and `Context`. File API objects are deliberately left out: importing `String` into the prelude does not import `strings`, importing `Map` does not import `maps`, and importing `Iter` does not import `iter`. Type-qualified calls such as `String.to_upper`, `Map.get`, `Iter.map`, and `Context.root` use the prelude types directly. File-qualified calls such as `io.print` require the corresponding file import (`import std/io`). `Ordering`'s `Less`/`Equal`/`Greater` variants are deliberately left out: `derive Comparable`'s synthesized body references them *qualified* (`Ordering.Less`), so nothing depends on the bare names being in scope, and — like the `std/literals` cluster (`Literal`/`Fragment` and the `Fragment` variants) and assertion-authoring types such as `Assertable`/`AssertionFailure` — they're named only by a narrower authoring audience that imports them explicitly. The prelude exports no bare functions. Anything else must be imported explicitly.

Stdlib files themselves do **not** receive the prelude — they each import their dependencies by hand. The "is this a stdlib file?" check is the only place that distinguishes who gets the implicit imports. A stdlib file's `//!` prompts are part of the file and see the file's scope, as its bodies do: a prompt that writes a bare `False` or `Some` needs the file to import it (`import bool.Bool.False`). `nomi test`, `nomi check`, the language server and the reference editors all check a prompt in that scope.

Batteries-included philosophy (like Go): minimize the need for third-party dependencies. The stdlib today, one file per module, each imported as `std/<name>`:

| Module | What it holds |
|---|---|
| `int`, `float`, `decimal`, `bool`, `unit`, `codepoints` | Primitives: `Int` (bitwise ops, wrapping arithmetic, `NonZeroInt`/`PositiveInt`), `Float` (special values, rounding), `Decimal` and `RoundingMode`, `Bool`, `Unit`, `Codepoint` |
| `strings`, `bytes`, `matcher` | `String` (grapheme-based text, `NormalForm`), `Byte`, `Bytes`, and `Matcher`, what String's search functions accept |
| `lists`, `vectors`, `maps`, `sets`, `ranges` | `List`, `Vector`, `Map`, `Set`, `Range` and their container-specific owner functions |
| `iter` | The `Iter` interface and every generic iteration owner function, plus `Iter.loop` |
| `maybe`, `results` | `Maybe`, `Result`, `Infallible` |
| `add`, `subtract`, `multiply`, `divide`, `comparable`, `equatable`, `hashable`, `discrete`, `steppable`, `display`, `debug`, `structs`, `type` | The core interfaces, `Ordering`, `Direction`, and `Type<T>` |
| `io` | `print`, `write` (no trailing newline), `inspect`, `read_line`, `read_file`, `write_file`, `IOError` |
| `tasks`, `channels`, `supervisors`, `timer`, `context`, `startup` | Structured concurrency (§20), `timer.sleep`, `Context` (§28), `Startup` (§27) |
| `duration`, `instant`, `calendar` | `Duration`, `Instant` (`Instant.now`), and the civil date-time types (§39) |
| `random` | Pure, seeded random generation (§40) |
| `json`, `toml`, `dynamic` | `Json` with `ToJson`/`FromJson` and their derives, the `Toml` typed literal, `Dynamic` for untyped host data |
| `regex` | `Regex`, with a `Regex"..."` typed literal |
| `literals` | `Literal` and `Fragment`, for typed-literal authors (§16) |
| `assertions`, `testing` | `AssertionFailure`, `Assertable`, `testing.check`, test `Clock` (§36) |
| `compiler` | The compiler as a library: `check`, `run`, `hover` |

Not in the stdlib: math functions beyond the numeric types' own (no trigonometry or logarithms), cryptography and networking (no HTTP client or server; bind a Go package with `gopkg`, as Host And Foreign Declarations above describes).

## 25. Compilation

Implementation language: **Go**

### Execution engines

One engine runs a Nomi program: the VM, for `nomi run`, `nomi test`, the REPL and the tour playground. The compiler parses, analyzes and type-checks the program and lowers each function it can to IR; the VM compiles that IR to bytecode and runs it over the runtime library (`rt`). A program that reaches a function the compiler could not lower fails before its first effect: `nomi run` prints an error at the code the compiler stopped at, with its source line and any hint, such as ``this call to `skip_odd` is not supported yet, so `fn evens` cannot run``, and exits 1. The last hint of every such error says the gap is in Nomi, not the program, and links the issue tracker. `nomi check` lowers the program the same way without running it and reports the same errors for everything `main` or a test reaches. `nomi test` runs every case it can, reports each case it cannot as `BLOCKED <file> :: <case> <path>:<line>:<col>: <message>`, with any hint indented on the line below, and appends `, K blocked` to its summary when K is not zero; it exits nonzero when anything failed or was blocked. A run stopped by something other than code the compiler could not lower (a Go crossing nothing binds, or a VM limit) prints `BLOCKED <file> [<function>] <reason>` lines instead. Setting `NOMI_DEBUG_LOWERING=1` adds the compiler's own reason to each of these errors as a hint. The REPL compiles each input as a new program that sees the declarations and top-level bindings of the inputs before it, and runs it on one live VM. An earlier input never runs again: a later input reads the value an earlier binding computed, and a closure keeps the value it captured. Redefining a function or type affects later inputs only; an input that fails to check, faults or is blocked prints its error and changes nothing.

### Standalone executables

`nomi build <file> [-o <out>] [--target <goos>/<goarch>]` writes one executable: a VM runner binary with the program's lowered IR appended to it, the way `deno compile` and `bun build --compile` work. At startup the runner reads the IR from its own file and runs `main` exactly as `nomi run` does: the same arguments, standard input, signal handling, output and exit status. The binary skips the front end and the lowering, so a hello-world program starts in a few milliseconds, and then runs on the same VM at `nomi run` speed. It is not native code.

A program `nomi run` would refuse is refused at build time with the same text, and nothing is written: a front-end error, or an error at each piece of code the compiler cannot lower. A file without `fn main` has nothing to build. A program with a `todo` or a `dbg` in any of its files is refused with one list of every `todo` (§5, *Unwritten Code*) and every `dbg` (§6, *Debugging pipes*), whether or not a run would reach it. `--target`, or `GOOS`/`GOARCH` in the environment, picks the platform. The runner comes from, in order:

1. `nomi-runner` beside `nomi` (or beside the file a symlinked `nomi` resolves to), built for the target. Every release archive ships one.
2. A source checkout of the compiler: the runner is built with `go build` and cached. This is how `go install` and a development tree work, with no release involved.
3. A runner downloaded earlier for this release and target.
4. Download: the target's archive from the release this `nomi` was cut as, verified against the release's checksum file, with only `nomi-runner` extracted and cached. `nomi build` prints the URL it downloads, and with no network the error names it.

A prebuilt runner must come from the same commit as `nomi`; both carry it in their build information, and `nomi build` refuses a runner from another commit. So a release builds executables for every platform with no Go toolchain, and a built binary needs nothing at all. Two kinds of program still need `go`, which builds their runner from the compiler module at the release's version: a project with Go FFI bindings, whose runner links them, and a program importing `std/compiler`, whose runner links the front end and is not shipped.

### Future: native code

A native backend could come back as a reader of the typed IR if native compilation becomes a goal; the VM is the only engine today.

### Tooling

- Opinionated formatter: `nomi fmt [-w|-l|-d] <paths>`
- Check without running: `nomi check`, which also reports, at its source, any code the program reaches that type-checks but cannot be compiled to run yet
- Language server: `nomi-lsp` (diagnostics, hover, go-to-definition, completion, rename, code actions)
- There is no debugger yet (see `roadmap.md`).

## 26. Entry Point

Nomi programs can be run in two ways, file mode and project mode; a script is file mode:

### File mode: `nomi run <file>`

Runs a specific file. The file must define a top-level `fn main()` callback. `fn main` is the program entry point — the host invokes it from the file's own scope, so it does not need to be `pub`. The project root is the directory containing the file — all imports resolve relative to that directory.

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
fn main(): Result<Unit, String> {
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

fn boot(startup: Startup): App {
    App{name: List.head(startup.args) |> Maybe.with_default("world")}
}

fn main(): Result<Unit, String> {
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

fn boot(): Config {
    Config{context: Context.root(), logger: StdoutLogger}
}

fn main() {
    Logger.log(Config.logger, "INFO", "starting on port ${Config.port}") // [INFO] starting on port 8080
    run() // [INFO] running
}
```

`fn boot(): Config` declares its application type
explicitly. `Config` is an ordinary developer-defined struct, and helpers that
construct it use ordinary return-type checking. A `tests` group runs the same
boot by naming it on its `boot` line (§36).

## 27. Scoped application fields

An entry file, one that defines `fn main`, may define a boot, either
`fn boot(): App` or `fn boot(startup: Startup): App`. A boot that needs no
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
    env: Map<String, String> = Map.empty()
    args: List<String> = []
}
```

Helpers receive startup inputs as ordinary arguments. Environment lookup is
`Map.get(startup.env, name)`. Startup is not itself an ambient value. A test
builds one as an ordinary struct, naming only the inputs it sets: `Startup{}`
is empty, and `Startup{args: ["-v"]}` has arguments and an empty env.

The runtime calls a boot that takes no parameter with no arguments, and passes
the process's Startup to a boot that takes one. The parameter may have a
default, `fn boot(startup: Startup = Startup{}): App`. The runtime always
passes the real Startup, so the default serves only a `tests` group's `boot`
line that passes none (§36).

```nomi
struct App {
    logger: Logger
    execution: Context
}

fn boot(): App {
    App{logger: Stdout, execution: Context.root()}
}

fn audit() {
    with App.logger = PrefixedLogger{tag: "audit"}
    with App.execution = Context.with_timeout(App.execution, Duration.seconds(3))
    work()
}
```

The boot's signature is checked. A boot that takes anything but no parameter
or one `Startup` (two parameters, or a parameter of another type such as
`Context`) is
`` boot must be `fn boot(): App` or `fn boot(startup: Startup): App`, taking no parameter or one Startup and returning the application struct ``;
one that returns something other than a struct is
`boot must return a struct, the application type`; a struct with two Context
fields is `an application struct may have at most one Context-typed field`. A
`fn boot` in a file without `fn main` is
`` `boot` belongs in an entry file, one that defines `fn main`; a `tests` group names an entry's boot with `boot entry.boot(startup)` ``.
An entry boot is not an ordinary function. The one place code calls it is a
`tests` group's `boot` line; a call or reference anywhere else is
`` entry-point boot cannot be called or captured as an ordinary function; a `tests` group calls it on its `boot` line ``.
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
struct-literal value needs no parentheses: `with App.store = FakeStore{}`.

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
`with App.database = {..App.database, host: "localhost"}`.

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

fn boot(): App {
    App{greeting: "hello"}
}

fn greet(): String {
    "${App.greeting}, world"
}

fn quiet_greeter(): () -> String {
    with App.greeting = "psst"
    io.print(greet()) // psst, world
    || greet()
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
import {
    std/duration.Duration
}

struct Config {
    context: Context

    port: Int = 8080
}

fn boot(): Config {
    Config{context: Context.root()}
}

fn handler(req: Request): Response {
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

Context values are for request- or execution-scoped metadata that should follow the current unit of work: trace IDs, auth subjects, locale, request IDs, or similar metadata that crosses API boundaries. They are **not** the dependency/capability mechanism; use ordinary payload fields such as `App.store` and `App.logger` for dependencies.

Values are addressed by their Nomi type:

```nomi
type TraceId String

with App.context = Context.with_value(App.context, TraceId("trace-123"))
case Context.value(App.context, TraceId) {
    Some(TraceId(id)) -> Logger.info(App.logger, "trace ${id}")
    None -> Logger.info(App.logger, "no trace")
}
```

The value's Nomi type is the context key. `Context.with_value` returns a child context with the value associated to its type; parent contexts are not mutated. `Context.value(context, T)` walks from the current context to its ancestors and returns the nearest value of type `T`. In that second argument position, `T` is a contextual `Type<T>` witness: a bare type name becomes a value only because the function expects `Type<T>`. The type should usually be a domain type (`trace.Id`, `Auth.Subject`, `Locale`) rather than a raw scalar, so unrelated metadata cannot collide on `String`.

The usual pattern is to wrap a context value in a tiny file so callers do not traffic in the low-level `Context.value(context, T)` form:

```nomi
// trace.nomi
pub type Id String

pub fn with_id(context: Context, id: Id): Context {
    Context.with_value(context, id)
}

pub fn id(context: Context): Maybe<Id> {
    Context.value(context, Id)
}
```

### The `context` stdlib module

The module's entire public surface is the opaque `Context` type with inherent ops — every call is type-qualified (`Context.deadline(c)`). There are no file-level free functions.

```nomi
pub host type Context

impl Context {
    pub host fn root(): Context

    pub host fn deadline(c: Context): Maybe<Instant>

    pub host fn deadline_remaining(c: Context): Maybe<Duration>

    pub host fn with_deadline(c: Context, at: Instant): Context

    pub host fn with_timeout(c: Context, dur: Duration): Context

    pub host fn with_value<T>(c: Context, value: T): Context

    pub host fn value<T>(c: Context, value_type: Type<T>): Maybe<T>
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
User{name: "Alice", age: 30} == User{name: "Alice", age: 30} // True

// enums — variant + carried data
Shape.Circle{radius: 5.0} == Shape.Circle{radius: 5.0} // True

Shape.Circle{radius: 5.0} == Shape.Rectangle{width: 5.0, height: 5.0} // False

// collections — deep equality
[1, 2, 3] == [1, 2, 3] // True

{"a" => 1} == {"a" => 1} // True

// tuples
(1, "hello") == (1, "hello") // True
```

**NaN equality is reflexive.** IEEE 754 says `NaN != NaN`, but Nomi's `==` is reflexive on `Float` so that pattern matching, `Maybe`/`Result` equality, and structural equality on containers all behave consistently — `nan == nan` is `True`, and `Some(nan) == Some(nan)` is `True`. This deviates from IEEE 754 in `==` only; the underlying Float64 representation and arithmetic are unchanged. To test for NaN-ness specifically, use `Float.nan?(x)`.

**Functions and Iters cannot be compared.** Using `==` or `!=` on a function or an `Iter<T>` is a compiler error: a function's only identity is its closure, and an `Iter` is a function over its source, so comparing two would either compare closures or run both sources. A value that holds one — a struct field, a tuple or record part, a list element, a `Maybe`/`Result` or enum payload — is also incomparable, and `==` on it is a compiler error naming the part (`` `==` cannot compare `Feed`: it holds `Iter<Int>`, and an Iter has no equality ``). A struct or enum with its own `impl Equatable` compares through that impl, so write one to compare such a type by its other fields; to compare an Iter's elements, materialize it with `Iter.to_list` first. A `Set` finds its elements and a `Map` its keys by equality, so such a value cannot be either: building a `Set` or `Map` over one (`#{it}`, `{f => 1}`) is a compiler error at the expression that builds it (`` `Iter<Int>` cannot be a Set element: an Iter has no equality or hashing ``).

```nomi
// compiler error: functions cannot be compared
f = |x| x + 1

f == f

// compiler error: Handler contains function field on_success
struct Handler {
    on_success: (String) -> Unit
}

a = Handler{on_success: |s| io.print(s)}

a == a
```

### Custom equality via `impl Equatable`

`==` on a type *without* an `impl Equatable` block uses the structural fallback above — no declaration needed. A hand-written `impl Equatable` block **overrides** the structural default: `==` / `!=` on a struct type with a registered `Equatable.equal?` route through that impl. That's how `std/calendar`'s `OffsetDateTime` / `DateTime` get instant-only equality (same moment in two zones is `==`; the zone is display metadata, not identity — see §39). Primitives keep the direct-comparison path and never dispatch.

The override holds wherever the value sits. `==` on a container, a tuple, or a struct or enum whose own `Equatable` is derived or absent compares each nested value of such a type through its impl (as a derived impl's field-by-field `==` does; Rust's derived `PartialEq` is the same rule), and a `Map` key or `Set` element matches the same way (see `Map<K, V>` in the collections list for how the bucket is chosen). So `[a] == [b]`, `Some(a) == Some(b)`, `#{a, b}` and `Map.get({a => 1}, b)` all agree with `a == b`.

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

Point{x: 1, y: 2} < Point{x: 3, y: 4}
// error: no impl of `Comparable` for `Point` (required at … via ordering operator `<`)
// help: add `derive Comparable` to `Point` or write an `impl Comparable for Point { fn <function>(value: Point): <Ret> }` block
```

Add `derive Comparable` (field-by-field lexicographic, §38.1) or a hand-written `Comparable.compare` function and the operators work, routed through `Comparable.compare`. The stdlib declares `Comparable` for the naturally-ordered types — `Int`, `Float`, `Decimal`, `String`, `Bool`, `List` (lexicographic), `Duration`, and the `std/calendar` ladder — while `Map` is deliberately non-`Comparable` (no natural total order) and opaque `Instant` exposes `Instant.before?` instead. Nameless structural types — **anonymous structs** (`{x: 1} < {x: 2}`) and **tuples** (`(1, 2) < (3, 4)`) — are *also* a compile-time error here, and unrecoverably so: there is no declaration to attach an impl (or `derive Comparable`) to, so the diagnostic reads `no impl of `Comparable` for `{x: Int}` … anonymous struct types cannot implement `Comparable` rather than suggesting a fix. (Equality is unaffected — `{x: 1} == {x: 1}` works via the structural fallback.) This is Python's line (positional containers compare, records don't), and the opt-in posture matches Rust/Haskell; OCaml/Erlang-style universal compare is a known footgun Nomi declines.

## 31. Module & Dependency Management

A Nomi module's identity is split between `go.mod` (Go-side identity and
dependencies) and `nomi.toml` (Nomi-side identity and entry points).
Dependencies on sibling modules through a local `replace` work. Resolution
without a local `replace`, `nomi add` / `nomi publish`, a registry, and
compiler-enforced semver are not implemented; each carries a status callout
below.

### Project config

A Nomi module's identity lives in two files at the module root:

- **`go.mod`** — the Go module path (used by `go` tooling and the
  module proxy), dependency graph (`require` / `replace`), and Go
  version. Standard `go.mod` syntax; nothing Nomi-specific.
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
dependencies use Go's `require` + `replace` directives: a `require` whose
`replace` points at a local directory with its own `nomi.toml` makes that
module importable under its `[module].name`.

> **Status: not yet implemented.** A `require` with no local `replace`
> is skipped: Nomi does not resolve modules through the Go module proxy,
> so a dependency must be checked out beside the project and replaced
> into it. Track via `feature-status.md`.

### CLI

The `nomi` CLI is the single tool:

- `nomi run <file>` — file mode (single-file scripts).
- `nomi <file> [args]` — the same as `nomi run`, for a file that ends in
  `.nomi` or starts with `#!`; a `#!/usr/bin/env nomi` script runs as this
  (see §26, *Scripts*).
- `nomi run <module>/<entry>` — project mode (a module with
  `nomi.toml`'s entry_points; see §26).
- `nomi test [path] [--line N] [--format text|json]` — run tests in a
  `.nomi` file or discover tests under a directory; see §36.
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
- `nomi build <file> [-o <binary>] [--target <goos>/<goarch>]` — one
  executable: a VM runner with the program's IR appended. It runs at
  `nomi run` speed without the startup lowering; it is not native code
  (see §25).
- `nomi fmt [-w|-l|-d] <paths>` — format Nomi source.
- `nomi version` — print the version.

> **Status: not yet implemented.** The following surface is reserved
> for future work: `nomi add <module>`, `nomi publish`.
> For now, add deps by editing `go.mod` directly.

### Registry & Distribution

> **Status: not yet implemented.** No Nomi-specific registry. The
> intent is that distribution rides on the Go module proxy by virtue
> of a Nomi module being a Go module, so `go.mod`'s `require` lines
> resolve through the standard Go ecosystem once a module is
> published to a public location (GitHub, GitLab, etc.); that needs
> the proxy resolution above. A separate Nomi-side registry is not
> planned; if Nomi-specific module discovery becomes a need, the
> simplest path is metadata indexing on top of existing Go module
> hosting rather than a parallel registry.

### Lock File

Nomi has no lock file of its own. A Nomi module's dependencies are Go
module requirements, so Go's `go.sum` is the artifact that pins them;
a separate `nomi.lock` is not planned.

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

Int division by zero still errors at the operator (`1 / 0` is a runtime error), because Int has no spare bit patterns to encode "no value" — every Int64 is a valid integer. Code that wants total integer division validates the divisor first with `Int.to_non_zero(y): Maybe<NonZeroInt>`, then calls `Int.divide(x, nz): Int`. The infix operator stays direct, and the fallible step is named at the boundary where the divisor becomes known-safe.

NaN and ±Infinity propagate through arithmetic and through the rounding helpers (`Float.round`, `floor`, `ceil`, `trunc`) per IEEE 754 — `Float.round(NaN)` is `NaN`, `Float.floor(+Inf)` is `+Inf`, etc.

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

`end` is `None` only when the value is built via `Range.from(N)` or `Range.naturals()`; in that case `inclusive` carries no meaning and is canonically `False`. `Range<T>` implements `Iter<T>` when `T` implements `Discrete`, because iteration needs a successor operation. Bounded discrete ranges terminate; unbounded integer ranges don't, so bound them with `Iter.take(N)` downstream — `Range.from(1) |> Iter.take(10) |> Iter.to_list()`. Non-discrete ranges are interval values: use `Range.contains?("a".."m", "h")`, `Range.bounded?(0.0..=1.0)`, `Range.contains?(1.25d..=2.50d, 1.50d)`, and similar queries.

Container-specific functions (`std/ranges`):

```nomi
fn from(n: Int): Range<Int>              // unbounded ascending from n
fn naturals(): Range<Int>                // unbounded ascending from 0
fn contains?<T>(r: Range<T>, n: T): Bool
fn bounded?<T>(r: Range<T>): Bool
```

Plus the `Iter` protocol for ranges whose endpoint type implements `Discrete` — `Iter.known_count(r)` is `Some(n)` for bounded discrete ranges that can count steps, `None` for an unbounded one (the safe bounded count). Generic ops are `Iter` owner functions: `Iter.to_list(r)` (hangs on unbounded; bound with `Iter.take` first), `Iter.count(r)`.

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

Bindings inside a block are scoped to that block — they don't leak into the enclosing scope.

Expressions, patterns and types nest at most 256 levels deep: blocks, parentheses, list, map and struct literals, lambdas, `if` bodies, unary operators, interpolations and type arguments each add a level, and a chain of binary operators, `|>` stages or `else if` arms does not. Deeper nesting is a parse error at the first token past the limit (`nesting deeper than 256 levels`).

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
pair = (1, "hello")

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
    fn to_string(e: UserError): String {
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
`N` (or `N + 1`): a `test`, an attached `//!` test, or a `tests` group, whose
header line runs every test in the group.

`nomi test --format json` reports as JSON Lines on stdout instead of text
(`--format text` is the default). Each test produces one record, and a
summary record comes last:

```json
{"type":"test","file":"/abs/path/a_test.nomi","line":14,"end_line":17,"name":"a_test.nomi :: fails","status":"failed","message":"  line 16: assertion failed\n    assert x == 4","error_line":16}
{"type":"file","file":"/abs/path/b_test.nomi","name":"b_test.nomi","status":"failed","message":"..."}
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
- A `"type":"file"` record is a failure that stopped a whole file from
  running, such as a type error: the text report's `FAIL <file>: <error>`
  line. It has no `line`.

In json mode, output a test prints (`io.print` and friends) goes to stderr
so stdout carries only records. The exit status is the same as in text mode.

The syntax is declaration-shaped:

```nomi
test "new user" {
    user = users.new("Alice", 30)
    assert user.name == "Alice"
}

tests "defaults" {
    setup {name: "No Name"}

    test "default user", {name} {
        user = users.default()
        assert user.name == name
    }
}
```

`test "name" { ... }` is one executable test case. `tests "name" { ... }`
groups tests. A group's `setup` produces a value, and a test receives it
through a pattern after the test's name (`test "default user", {name}` above).
A group's parts are described after attached tests, below.

An attached test uses a docs-flavored `//!` prompt immediately before the
declaration it exercises. It is discovered and run by `nomi test` like an
ordinary test declaration, and the body is type-checked in the following
declaration's lexical scope. Contiguous `//!` lines form one attached test; a
physical blank source line starts a separate attached test:

```nomi
//! assert Int.to_string(42) == "42"
//! assert Int.to_string(-7) == "-7"
//
host fn to_string(n: Int): String

//! assert answer() == 42
//
fn answer(): Int {
    42
}
```

Put setup before ordinary `assert` / `refute` statements, and keep related
assertions in the same attached test.

A `tests` group holds at most one `clock` line, at most one `boot` line, at
most one `setup`, and its `test` blocks. Each part is optional, and the lines
may appear in any order, before, between or after the tests: they run in a
fixed order whatever their position (below). `nomi fmt` places them first, in
the order they run: `clock`, `boot`, `setup`, then the tests in source order.
Groups do not nest; a `tests` inside a `tests` is
`` a `tests` group cannot contain another `tests` group; write a sibling group instead ``.
A second `clock`, `boot` or `setup` is a parse error, such as
`` a `tests` group has at most one `setup` line ``.

The `boot` line calls an entry's boot (§27). The call is an ordinary call
checked against the boot's signature: `boot server.boot()` calls a boot that
takes no parameter or whose parameter has a default, and
`boot server.boot(startup)` passes a `Startup` to one that takes it. Most tests
need no startup input, so a boot that reads startup usually defaults its
parameter to `Startup{}`. A test file reaches the boot by importing its entry
file:

```nomi
// server.nomi
import std/io

pub interface Mailer {
    fn send(mailer: self, to: String): Result<Unit, String>
}

pub type SmtpMailer

impl Mailer for SmtpMailer {
    fn send(_mailer: SmtpMailer, to: String): Result<Unit, String> {
        io.print("smtp: welcome ${to}")
        Ok(Unit)
    }
}

pub struct App {
    mailer: Mailer
    sender: String
}

pub fn boot(startup: Startup = Startup{}): App {
    sender = Map.get(startup.env, "SENDER") |> Maybe.with_default("noreply")
    App{mailer: SmtpMailer, sender}
}

pub fn welcome(to: String): Result<Unit, String> {
    Mailer.send(App.mailer, to)
}

fn main() {
    _ = welcome("ada@example.com")
}
```

```nomi
// test_support.nomi
pub fn startup(): Startup {
    Startup{env: {"SENDER" => "ada"}}
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
    fn send(_mailer: FakeMailer, to: String): Result<Unit, String> {
        if to == "" {
            Err("no address")
        } else {
            Ok(Unit)
        }
    }
}

tests "welcome" {
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

tests "configured sender" {
    boot server.boot(test_support.startup())

    test "reads the booted sender" {
        assert App.sender == "ada"
    }
}
```

A group does not define its own boot: a `fn boot` inside `tests` is
`` a `tests` group does not define `fn boot`; it names an entry's boot on a `boot` line, such as `boot server.boot(startup)` ``,
and a `boot` line that calls anything but an entry's boot is
`` `boot` in a `tests` group calls an entry's boot, the `fn boot` of a file that defines `fn main`, such as `boot server.boot(startup)`; `make_app` is not one ``.
A test outside a group, or in a group with no `boot` line, runs with no boot
and cannot read application fields.

`setup expr`, or `setup { ... }` with statements before its final value,
produces the value the group's tests receive. It may be any value; a setup
whose last line is a statement, such as a `with` line, produces `Unit`.
`return value` inside a setup ends the setup, not the test, with that value,
as it ends a function; the setup's type is the type its final value and every
`return` agree on. A test
binds the value with a pattern after its name: a name
(`test "loads rows", db`), a tuple (`test "pair", (left, right)`), or a record
(`test "default user", {name}`). A test with no pattern ignores the value. The
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
tests "store" {
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

A `tests` group may declare its clock on a `clock` line: `clock Clock.Virtual`,
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

A test body, or a `tests` group's `setup` block, whose final value is
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
`Debug` does not change the row.

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
operator other than `|>` is the same error.

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
A `try` or an assertion in a `tests` group's `setup` block behaves the same
way: the setup runs inside each case it serves, so an early exit there fails
that case with the same report, and none of the case's body runs.

That makes assertions usable in production code as runtime checks as well as in
tests. Inside `nomi test`, a failed assertion stops that test and reports the
failure; other discovered tests continue to run. `nomi test` exits nonzero if
any test file fails to load, any test case fails, or the VM cannot run a case
(it is reported `BLOCKED`; see §25). Test names must be unique
within a file by their full path, including enclosing `tests` groups. A
`test` or `tests` name that is empty or only whitespace is a compile error.
Assertion failures carry observed values for comparison operands, predicate-call
arguments, and pipeline stages where available, so ordinary Nomi expressions can
produce useful test reports without a separate assertion-helper API for each
operation.

`dbg expr` prints the expression's source location, source text, and Debug
rendering, then returns the original value. In a pipe, `dbg` follows the bare
unary keyword-stage rule from §6 and acts as a transparent stage. The LSP
reports every `dbg` as a warning, and `nomi build` refuses it, because it is
development-only instrumentation:

```nomi
total =
    items
    |> Iter.map(price)
    |> Iter.reduce(|sum = 0, value| sum + value)
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
    pub fn load(startup: Startup): Config {
        base = Config{context: Context.root()}

        deployment =
            case Map.get(startup.env, "NOMI_ENV") {
                Some(value) -> value
                None -> "dev"
            }

        configured = case deployment {
            "prod" -> prod.configure(base)
            "test" -> test_env.configure(base)
            _ -> base
        }

        Struct.update(configured, {
            deployment: deployment,
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
import {
    app.Config
}

pub fn boot(startup: Startup): Config {
    Config.load(startup)
}

fn main() {
    ...
}
```

```nomi
// prod.nomi
import app.Config

pub fn configure(config: Config): Config {
    Struct.update(config, {max_connections: 20})
}
```

```nomi
// test.nomi
import app.Config

pub fn configure(config: Config): Config {
    Struct.update(config, {
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

Tests use the same language mechanisms as application code. A `tests` group
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

tests "limited app config" {
    boot main.boot(Startup{env: {"NOMI_ENV" => "test"}})

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

An attached test prompt sits above the declaration it exercises.

### 38.1 `derive` — Compile-time impl synthesis

> **Limitations.** A derive over a struct with an anonymous-struct field (e.g. `value: {x: Int, y: Int}`) type-checks, but the synthesized body is not lowered, so a program that calls it is refused with a "not supported yet" error (§25). Interface-typed fields (existentials) are skipped at the `derive` site — the static check can't know the runtime concrete type, so missing inner impls surface as runtime "no impl found" rather than at the `derive` site. The Hashable mix is `*31 + h` (Java-string-style) — fine for in-process bucketing, but collisions are easy to construct (e.g. `Hashable.hash(Point{x: 0, y: 31}) == Hashable.hash(Point{x: 1, y: 0}) == 31`); upgrade to FNV-1a / SipHash if a real workload demands it.

> **`derive Debug` is redundant.** Debug is **universal and automatic** (§38.2) — the compiler auto-synthesizes a structural Debug impl, byte-identical to `derive Debug`, for every declared type that lacks one. So you never need to write `derive Debug`; the only reason to mention Debug in `impl Debug` is to *override* the default (e.g. a **structural** override on an opaque type, whose auto default is name-only `<opaque T>` — see Opaque types below). `derive Debug` remains valid (it's just the explicit form of what happens automatically); the examples in this section keep it for illustration.

> **Same-file constraint.** A `derive Iface for Type` declaration must sit in the **same file and scope as the type's declaration** — an `impl Iface for Type { ... }` block can't be derived, and a cross-file derive is rejected (structural derivation reads the type's fields/variants, which live with the declaration). The derivable protocols are `Equatable`, `Hashable`, `Comparable`, `Display`, `Debug`, `ToJson`, and `FromJson`.

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
    fn equal?(a: Point, b: Point): Bool {
        a.x == b.x and a.y == b.y
    }
}

impl Hashable for Point {
    fn hash(p: Point): Int {
        Hashable.hash(p.x) * 31 + Hashable.hash(p.y)
    }
}

impl Debug for Point {
    fn inspect(p: Point): String {
        "Point{x: ${Debug.inspect(p.x)}, y: ${Debug.inspect(p.y)}}"
    }
}

```

The user never writes the synthesized functions; they exist at compile time and dispatch like any other impl. Internally, derive synthesis (like universal-Debug synthesis) emits implementation records equivalent to `impl Iface for Type { ... }` blocks rather than rewriting the type's body; this is a compiler-internal lowering detail, invisible except in this equivalence and not subject to the source-form placement rule.

#### Derive args and scope

Imports cover the names the file's **visible text** writes — and a `derive Comparable for Point` declaration *names* `Comparable` in the source. So each derived interface resolves through ordinary scope: it counts as a use of the protocol name, go-to-def on it jumps to the interface declaration, and naming a protocol that isn't in scope is a compile error pointing at the line with the exact import to add (``` `Comparable` is not in scope — import `std/comparable.Comparable` ```). Derive options are visible text too: `derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}` keeps the owner imports for `ToJson` and `Json` live; importing `Json.{self, Case.Camel}` and writing `rename_all: Camel` keeps both `Json` and the imported variant live instead. Prelude-exported protocols are in scope automatically for user files; protocols that are not in the prelude, such as `ToJson` and `FromJson`, are imported explicitly. `Debug` is the one exemption: it is compiler-known universally (§38.2), so `derive Debug for Type` carries no scope requirement.

Names referenced only by the **synthesized** code resolve through the compiler-known route, never through the deriving file's imports: the synthesized impl headers, the `Ordering` type (named in the `compare` return *and* as the qualifier of the `Ordering.Less`/`Equal`/`Greater` value and pattern references the body emits — qualifying through the type is what keeps the variants out of the prelude), `True` / `False` in synthesized bodies, and the interface-qualified recursion calls (`Equatable.equal?(...)`, `Comparable.compare(...)`, …). When synthesis creates an ordinary module call internally, it carries its own synthetic support import; for example `derive FromJson` bodies use `Map.get` without requiring the source file to import `std/maps`. Derive is compiler machinery; its output resolves like compiler output. A file deriving a protocol therefore imports exactly the protocol names its `derive Iface for Type` declarations write — nothing extra for the synthesis internals. Hand-written `impl Display for T { ... }` blocks still require `Display` in scope.

#### v1 derivable protocols

| Protocol | Synthesis rule |
|----------|---------------|
| `Equatable` | Pairwise field equality with `==` short-circuiting on first false. For enums: variants must match before payload comparison. |
| `Hashable` | Left-fold mix `Hashable.hash(value.f0) * 31 + Hashable.hash(value.f1) * 31 + ...` over each field. For enums: variant tag (declaration index) is mixed in via the same `* 31 + h` chain ahead of the payload hashes. Collision-prone for small-int patterns; see callout. |
| `Comparable` | Lexicographic by field declaration order, short-circuiting on first non-equal. For enums: earlier-declared variant < later-declared, ties broken by payload comparison. |
| `Debug` | Structural string in literal-shape form. For struct `Point{x: 3, y: 4}` → `"Point{x: 3, y: 4}"`. For enum `Shape.Circle(5.0)` → `"Circle(5.0)"` (the variant, with no enum-name prefix). |
| `Display` | Same structural shape as `Debug`, but nested `String` values render **unquoted** — `C{name: bob}` where Debug emits `C{name: "bob"}`. A convenience default; authors usually hand-write a `Display.to_string` function for real user-facing presentation (see below). |
| `ToJson` | Struct fields encode as JSON object keys; distinct wrappers encode through their inner value; enums encode as strings for bare variants and externally-tagged objects for payload variants. `ToJson.Options{rename_all: ...}` controls generated field keys with a `Json.Case` variant. |
| `FromJson` | Struct fields decode from JSON objects with missing `Maybe<T>` fields as `None` and missing defaulted fields from their declaration defaults; distinct wrappers decode through their inner value. Callers name the target with the implementing type (`User.from_json(value)`), an explicit target type argument on the interface (`FromJson.from_json<User>(value)`), or an expected target type (`user: User = try FromJson.from_json(value)`). `FromJson.Options{rename_all: ...}` controls expected field keys with a `Json.Case` variant. |

`Display` **is** derivable — `derive Display` synthesizes the same structural shape as `derive Debug`, except nested strings render unquoted (`C{name: bob}` where Debug emits `C{name: "bob"}`). It's a convenience for throwaway / structural output and for generic code that just needs *some* `Display` impl to satisfy a bound. Real user-facing presentation usually wants a per-type opinion rather than the structural dump, so authors typically hand-write a `Display.to_string` function instead — the same split Rust draws between `Debug` (`#[derive]`-friendly) and `Display` (hand-written), except Nomi *also* lets you derive `Display` when the structural form is good enough.

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

synthesizes `impl Debug for Box<T> { fn inspect(b: Box<T>): String { ... } }`. For most protocols the implied `T: Iface` bound is real (calling errors at the call site if it isn't satisfied) — but **`T: Debug` is vacuous**: Debug is universal (§38.2), so every `T` satisfies it and `Debug.inspect(box)` always type-checks. (For `Equatable`/`Hashable`/`Comparable`/`Display` derives the implied bound on `T` is still enforced.)

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

#### Extern types

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

fn main(): Result<Unit, Error> {
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
                                Time  ──────┤  at / at_midnight
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
the bracket parse via `OffsetDateTime` instead. Parse failures return
`Err(calendar.Error)`; `try`-unwrap at the call site. See §16 Typed
Literals for the underlying mechanism.

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

- **`Generator.step(gen, seed): (T, random.Seed)`** — runs a generator against a
  seed, returning the value paired with the *next* seed (which you thread
  into the following draw). Generating is a pure expression.

```nomi
seed = Seed.from_int(42)

die = try Generator.int(1, 6)

(roll, seed) = die |> Generator.step(seed)

(next, _) = die |> Generator.step(seed) // a fresh seed → a fresh draw
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
`Generator.float(from, to)` (half-open `[from, to)`) return
`Result<random.Generator<_>, random.Error>` because their ranges can be
invalid. `Generator.bool()`, `Generator.constant(value)`, and
`Generator.uniform(first, rest)` are infallible.
`Generator.weighted(first, rest)` returns
`Result<random.Generator<T>, random.Error>` because weights must be
non-negative and at least one weight must be positive; weights need not sum to
1.

### Combinators and collections

- **`Generator.map(gen, f)`** — reshape the produced value.
- **`Generator.flat_map(gen, f)`** — the bind operation: sequence a
  *dependent* generator (the next draw depends on the previous value), and
  the way to build a composite `random.Generator` value.
- **`Generator.list(gen, length)`** — a list of `length` independent draws.

```nomi
seed = Seed.from_int(7)

die = try Generator.int(1, 6)

(hand, _) = die
|> Generator.list(5)
|> Generator.step(seed) // → [4, 1, 1, 4, 5], reproducibly
```

When several draws are known up front, compose them into one generator and
`step` once; thread the `random.Seed` by hand only when draws are interleaved with
other work (e.g. across turns of an interactive loop). The applicative
`mapN` family (`map2`/`map3`/…) is intentionally absent — Nomi's exposed
`step` makes hand-threading the natural move, and `flat_map` + `map` express
any composite generator when one is needed.

## 41. Open Design Questions

1. **Exhaustive struct pattern opt-in.** Struct pattern matching is partial by
   default: unmentioned fields are ignored. A `!..` marker could opt into
   exhaustive field matching, so `User{name, age, email, !..}` would become a
   compile error when `User` gains a field. This is the inverse of Rust, where
   `..` opts *out* of exhaustive matching. It waits on real usage showing the
   need.
2. **Nested distinct-type destructuring in bindings.** For
   `type Point (Int, Int)`, a `case` arm accepts `Point(x, y)` and
   `Point((x, y))`, but a binding takes only a single name: `Point(p) = point`
   then `(x, y) = p`. `Point(x, y) = point` and `Point((x, y)) = point` are
   parse errors. Whether a binding should take the arm's sub-patterns is open.
