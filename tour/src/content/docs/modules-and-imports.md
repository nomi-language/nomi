---
title: "Modules & Imports"
description: "Files are import and visibility units; Nomi modules are directory trees rooted at `nomi.toml`."
---

Nomi has two layers of file organization. A **file** is a single `.nomi`
source file — what imports point at, and the unit `pub` operates on
(visibility is file-level). A **Nomi module** is a directory tree rooted at
`nomi.toml` — what ships together and what the [orphan rule](#the-orphan-rule)
operates on. Nomi modules also use a sibling `go.mod` and lean on Go module
tooling for dependency mechanics. Each file imports the names it needs
explicitly; the compiler resolves the whole program in one pass, so imports
never need to be ordered or untangled.

## Import syntax

Single imports stand on their own; two or more imports merge into one block.
You can select several exported names from one file, select children from an
owner, or alias an imported item to dodge a collision:

```nomi-run
import {
    std/calendar.Date.parse
    std/io as console
}

fn main() {
    parsed = parse("2026-05-27")

    console.print(String.length("hello"))
    console.print(Iter.count([1, 2, 3]))
    console.print(String.join(["a", "b", "c", "d"]))
    console.inspect(parsed)
}

```
<!-- expect
5
3
abcd
Ok(2026-05-27)
-->

- `std/io` — import the `std/io` file API object as `io`; reach members as [`io.print`](/reference/io/#ioprint).
- `{ … }` — group several imports in one block.
- `Date.parse` — import a child function from the `Date` owner as bare `parse`.
- `as console` — rename an imported item to dodge a local name conflict.

To select children from an owner, write the owner before `.{...}`:

```nomi ignore
import shape.Shape.{Circle, Rectangle}
```

That binds `Circle` and `Rectangle` from the exported `Shape` enum. Add `self`
when you also want the owner itself:

```nomi ignore
import shape.Shape.{self, Circle, Rectangle}
```

That binds `Shape`, `Circle`, and `Rectangle`. The `self` marker binds the
left-hand side of the brace group, so `import std/maps.{self, Map}` binds both
the `maps` file API object and the `Map` type.

## Files And Modules

For a single-file program (everything in the tour so far) you don't need
anything more. A larger project is a **Nomi module**: a directory with a
`nomi.toml` and one or more `.nomi` files inside. Files within the module
import each other by path from the module root, without the module's own name as
a prefix. A sibling file can be imported by its bare stem (`math`); a nested
file uses its slash path (`http/header`).

The next example is a small module with two `.nomi` files and a `nomi.toml`.
In a real project, these files would sit next to each other in the same
directory.

```nomi-run
// FILE: math.nomi
pub fn double(n: Int): Int {
    n * 2
}

// FILE: main.nomi
import {
    std/io
    math
}

fn main() {
    io.print(math.double(7))
}

// FILE: nomi.toml
[module]

name = "myproject"

entry_points = ["main"]
```
<!-- expect
14
-->

`entry_points` lists the files whose `fn main` is allowed to be the
program's entry. An `internal/` subdirectory creates an access barrier —
files inside `internal/` can only be imported from elsewhere in the same
module.

## Visibility

Top-level declarations are **private by default**. Private means visible only
inside the declaring file. To make a declaration reachable from another file,
mark it `pub`:

```nomi-run
// FILE: math.nomi
pub fn double(n: Int): Int {
    n * 2
}

// Private: only math.nomi can call this directly.
fn helper(n: Int): Int {
    n + 1
}

// FILE: main.nomi
import {
    std/io
    math
}

fn main() {
    io.print(math.double(7))

    // Uncomment to see the file boundary in action — main.nomi can't
    // see math.nomi's private `helper`:
    // io.print(math.helper(7))
}

// FILE: nomi.toml
[module]

name = "vis_demo"

entry_points = ["main"]
```
<!-- expect
14
-->

`pub` works on every kind of declaration — `pub fn`,
`pub struct`, `pub enum`, `pub interface`, `pub type`, `pub once`.

Executable entry files are listed in `entry_points` and define a top-level
`fn main()` callback; `main` is the program entry point, not part of the file's
imported API surface.

Use another file when an API has a natural sub-surface:

```nomi-run
// FILE: header.nomi
pub fn canonical_name(name: String): String {
    name
}

// FILE: main.nomi
import {
    std/io
    header
}

fn main() {
    io.print(header.canonical_name("content-type"))
}

// FILE: nomi.toml
[module]

name = "nested_file_demo"

entry_points = ["main"]
```
<!-- expect
content-type
-->

The rule is about the **file** boundary, not the module boundary: two files
in the same Nomi module are still different visibility units. The module (the
`nomi.toml`-rooted directory tree) is a separate layer — the orphan-rule and
distribution unit, not the visibility unit.

Interface implementation functions don't take a `pub` of their own. Their
visibility comes from the interface they implement:

- If the interface is `pub`, other files can call the implementation functions.
- If the interface is private, the implementation functions stay private too.
- The rule applies to every `impl Interface for Type { ... }` block.

Ordinary helper functions use per-item `pub`, just like other top-level
declarations. They belong in the file that exposes them; use `impl Interface for
Type` for cross-file interface implementations, not extension-style helper
APIs.

## Opaque types

A `pub` declaration exposes both the type's *name* and its
*representation* — its fields (struct), variants (enum), or wrapped
inner type (distinct `type`). For an abstract data type whose
representation you want to protect — invariants the constructor enforces,
a representation you might change later — `opaque` publishes the
name while keeping the construction surface (constructor, unwrap,
destructuring, and field access) private:

```nomi-run
// FILE: ids.nomi
pub opaque type UserId Int

impl UserId {
    pub fn new(n: Int): Result<UserId, String> {
        if n > 0 {
            Ok(UserId(n))
        } else {
            Err("user IDs must be positive")
        }
    }

    pub fn value(id: UserId): Int {
        UserId(n) = id // only ids.nomi can destructure UserId
        n
    }
}

// FILE: main.nomi
import {
    std/io
    ids.UserId
}

fn main() {
    case UserId.new(42) {
        Ok(id) -> io.print(UserId.value(id))
        Err(e) -> io.print("error: ${e}")
    }

    case UserId.new(-1) {
        Ok(_) -> io.print("unexpected")
        Err(e) -> io.print("error: ${e}")
    }

    // Uncomment to see the opaque barrier — main.nomi can hold a
    // UserId, but can't construct or destructure one directly:
    // bad = UserId(7)
}

// FILE: nomi.toml
[module]

name = "ids_demo"

entry_points = ["main"]
```
<!-- expect
42
error: user IDs must be positive
-->

From `main.nomi`, `UserId` is a type you can hold, pass, and store
in collections — but you can't construct one (`UserId(7)` is
rejected), destructure one (`UserId(raw) = id` errors), or read the
inner `Int` directly. The only way in and out is through `UserId`'s
`pub` functions. The validation in `UserId.new` becomes a real
invariant: no `UserId` value can exist with a non-positive inner.

`opaque` composes with each of the three type-declaration keywords:

- `pub opaque struct` — hides the field list.
- `pub opaque enum` — hides the variants.
- `pub opaque type Name InnerType` — hides the wrapped
  representation (the form shown above).

`opaque` is rejected on declarations without a representation —
functions, type aliases, interfaces, `once` bindings, and
zero-sized `type` markers — since there's nothing to hide.

## Re-exports

A file can re-publish names it imports — promoting them into its own
public surface — with the `export` modifier on the import. That's how a
facade file curates an API out of sibling files or dependencies
without making consumers chase the underlying source:

```nomi
// Re-export an imported type — consumers of this file see `Parser`.
import other/parser.Parser export

// Re-export an imported file API object under a different name.
import other/parser export as parser_lib

// Per-item — `add` re-exported under its own name; `subtract`
// re-exported under the alias `minus`; `helper` stays private.
import calc.{add export, subtract export as minus, helper}

// Line-level shorthand — every selected name re-exported.
import std/io.{self, IOError} export
```

Re-exports are explicit by item — there's no `import path.*` glob —
so an upstream dependency adding new public names never silently
expands your file's API. The name being re-exported must already be
`pub` upstream; re-exporting a private name is an error.

`export as ...` applies only to imported names, not to your file's
own declarations, and the alias has to match the imported symbol's
naming shape (`parser_lib` for a file API object or function;
`Parser` for a type).
To expose a local definition under a different name, rename the declaration
itself or write a thin wrapper.

## Nomi modules

Nomi module management is one of Nomi's younger layers. A Nomi module is a
directory rooted at `nomi.toml`, with files inside forming the import graph.
That manifest alone is what makes a module: the standard library is one Nomi
module with a `nomi.toml` and no `go.mod` of its own.

A module you **distribute** pairs the manifest with a `go.mod`, because Nomi
leans on Go tooling for dependency mechanics. Creating one uses the standard
Go pair:

```text
mkdir myproject
cd myproject
go mod init myproject
# then add nomi.toml + your .nomi files
```

After that, `nomi.toml` describes the Nomi module and `go.mod` carries the
current dependency mechanics. For local and Git-hosted module experiments, use
Go's familiar tooling — `go get github.com/user/somelib`, then import files
from `somelib` in your Nomi code. The long-term ergonomics are still an open
design area.

Nomi uses Go modules underneath, so the word **module** intentionally lines up
at this layer. When the tour says "Go package," it means an importable Go code
package used by a `gopkg` FFI binding; when it says "Nomi module," it means the
`nomi.toml`-rooted project or library.

## Import order doesn't matter

The compiler resolves names whole-program in two phases, so a file can
refer to a name declared in any other file without forward declarations
or careful ordering. No header files, no "include guard" ceremony. (As a
side effect, cyclic imports between files aren't build errors either —
though they may be a sign module boundaries want rethinking.)

## The orphan rule

The flip side of the [impl/interface decoupling](/interfaces-and-dispatch/) is the **orphan rule**: an
impl must be declared in the Nomi module that owns either `Iface` or `T`.
That rule applies uniformly to `impl Iface for T { ... }` blocks, including
local, foreign, generic, and constrained-generic implementations such as
`impl Iface for Box<T> where T: Bound { ... }`. You can't add an
impl in your own code that bridges two third-party libraries' types and
interfaces together — that would create coherence conflicts when both libraries
are linked. The compiler reports the violation as a build error.

The next chapter — [App Fields, Defer & Context](/capabilities-and-context/)
— covers Nomi's answer to "how do you pass runtime dependencies around without
a tangle of globals or DI containers": app fields, `with App.field = value` overrides,
deferred cleanup, and context.
