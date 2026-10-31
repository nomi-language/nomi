---
title: "Modules & Imports"
description: "Import from other files, choose what each file makes public, hide a type's contents, and group files into a module."
---

Nomi organizes code at two levels:

- A file is one `.nomi` file. An import names a file. `pub` makes a
  declaration visible to other files.
- A Nomi module is a directory with a `nomi.toml` file, and everything under
  it. A module is what you publish as a project or library.

Each file imports the names it uses from other files.

## Import syntax

One import stands alone. Two or more go in one `import { ... }` block. You can
import a file, import single items from it, and rename what you import:

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

- `std/io` imports the file `std/io`. You then reach its functions through
  `io`, as in [`io.print`](/reference/io/#ioprint).
- `std/calendar.Date.parse` imports the function `parse` from the type `Date`.
  You then call it as plain `parse`.
- `as console` renames what you import. Here `std/io` is reached as `console`
  instead of `io`. Use this when a name clashes with one you already have.
- `String`, `Iter`, `List`, `Map` and the other prelude types need no import.

To import several items from one type or file, list them in `.{...}`:

```nomi ignore
import shape.Shape.{Circle, Rectangle}
```

That imports the variants `Circle` and `Rectangle` of the `Shape` enum. To
import `Shape` itself as well, add `self`:

```nomi ignore
import shape.Shape.{self, Circle, Rectangle}
```

That imports `Shape`, `Circle` and `Rectangle`. `self` means whatever is
before the `.{`. So `import std/calendar.{self, Date}` imports both the file
`calendar` and the type `Date`.

## Declaration order and cycles

A file can use a name declared anywhere in it or in any file it imports, in
any order. You don't need to declare anything ahead of time. Two files can
even import each other. That works, though it may mean the code should be
split differently.

## Files and modules

A program in one file, like every example so far, needs nothing more. A
larger project is a Nomi module: a directory with a `nomi.toml` and one or
more `.nomi` files.

Files in a module import each other by their path from the module's
directory, without `.nomi`. A file in the same directory is imported by its
name (`math`). A file in a subdirectory is imported by its path
(`http/header`).

The next example is a small module with two `.nomi` files and a `nomi.toml`.
In a real project, the three files would sit side by side in one directory.

```nomi-run
// FILE: math.nomi
pub fn double(n: Int) -> Int {
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

`entry_points` lists the files that have a `fn main`, and so can be run as a
program. A file in the module's `tools/` directory is a
[tool](/generating-code/), which is an entry point without being listed.

Files under a directory named `internal/` can be imported only by files under
that directory's parent. `a/internal/x.nomi` is open to `a/` and below; an
`internal/` at the module root is open to the whole module and closed to other
modules.

## Visibility

A declaration at the top of a file is private unless you mark it `pub`. Only
its own file can use a private declaration. Mark it `pub` to let other files
use it:

```nomi-run
// FILE: math.nomi
pub fn double(n: Int) -> Int {
    n * 2
}

// Private: only math.nomi can call this directly.
fn helper(n: Int) -> Int {
    n + 1
}

// FILE: main.nomi
import {
    std/io
    math
}

fn main() {
    io.print(math.double(7))

    // Uncomment to see that main.nomi can't call math.nomi's private
    // `helper`:
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

Every kind of declaration can be `pub`: `pub fn`, `pub struct`, `pub enum`,
`pub interface`, `pub type` and `pub once`.

Privacy is per file, not per module. Two files in the same module still can't
see each other's private declarations.

Functions in an `impl Interface for Type` block don't take `pub`. If the
interface is `pub`, other files can call them. If the interface is private,
so are they.

Other functions take `pub` one by one, like any declaration. A file can't add
functions to a type declared in another file. To give another file's type new
behavior, implement an interface for it.

## Opaque types

A `pub` type shows its contents to other files: a struct's fields, an enum's
variants, or the value a distinct type wraps. Other files can build a value,
take one apart and read its fields.

Sometimes you want to stop that. The constructor may check something, such
as that a number is positive. Or you may want to change the contents later.
`opaque` makes the type's name public and keeps its contents private:

```nomi-run
// FILE: ids.nomi
pub opaque type UserId Int

impl UserId {
    pub fn new(n: Int) -> Result<UserId, String> {
        if n > 0 {
            Ok(UserId(n))
        } else {
            Err("user IDs must be positive")
        }
    }

    pub fn value(id: UserId) -> Int {
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

    // Uncomment to see that main.nomi can hold a UserId, but can't
    // build or take apart one directly:
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

In `main.nomi`, you can hold a `UserId`, pass it around and store it in a
list. You can't build one with `UserId(7)`, take one apart with
`UserId(raw) = id`, or read the `Int` inside. Only `UserId`'s `pub` functions
can do those things. Since `UserId.new` checks its argument, every `UserId`
holds a positive number.

`opaque` works with each of the three kinds of type:

- `pub opaque struct` hides the fields.
- `pub opaque enum` hides the variants.
- `pub opaque type Name InnerType` hides the wrapped value, as above.

Declarations with no contents to hide can't be `opaque`. These are
functions, type aliases, interfaces, `once` bindings, and `type` declarations
that wrap nothing.

## Re-exports

`pub import` imports items and makes them public from your file too. Other
files can then import them from your file. This lets one file gather an API
from several files, so its users import from one place:

```nomi
// Re-export an imported type: files that import this one see `Parser`.
pub import other/parser.Parser

// Re-export several items at once.
pub import std/io.{print, write}

// An alias is the name consumers see: `parse_source`, not `parse`.
pub import other/parser.parse as parse_source

// A block re-exports every entry.
pub import {
    std/comparable.Ordering
    calc.{add, subtract}
}
```

`pub import` makes public every item the statement imports. Items you only
use yourself go in a separate plain `import`.

You list each item you re-export. There is no `*` to re-export everything.
So when a file you import gains a new public name, your file's API doesn't
change. You can only re-export an item that is already `pub` where it is
declared.

You can re-export items, but not a whole file. `pub import other/parser` is
an error. List the items your users need, or have them import `other/parser`
themselves.

`pub import` works only on imported names. A new name given with `as` must be
the same kind of name as the original: snake_case for a function, such as
`parse_source`, and PascalCase for a type, such as `Parser`. To make a
declaration of your own public under another name, rename it, or write a
small function that calls it.

## Nomi modules

A directory with a `nomi.toml` is a Nomi module, and the files under it import
each other. The `nomi.toml` alone makes a module. The standard library is one
module, with a `nomi.toml` and no `go.mod`.

A module that uses other modules, or that you publish for others to use, also
has a `go.mod` file. Nomi publishes and downloads modules the same way Go
does, so it keeps Go's file to give the module's full name and list the
modules it uses. You don't need Go installed, and you don't edit `go.mod`
yourself: `nomi` writes it. Start a project with `nomi new` and the module's
full name, which is usually the address of its repository:

```console
$ nomi new github.com/you/myproject
wrote myproject/nomi.toml, myproject/go.mod and myproject/main.nomi
next: cd myproject && nomi run main.nomi
```

The project's short name is the last part of the full name, `myproject`.
The `go.mod` it writes holds the full name and the Go version:

```gomod
module github.com/you/myproject

go 1.27.0
```

To make a directory that already holds your files a project, run `nomi init`
in it with the full name. It writes the `nomi.toml` and `go.mod` the
directory lacks and no source file, lists each file with a `fn main` as an
entry point, and never replaces a file. A directory that already has a
`go.mod` needs no name, since `go.mod` holds it:

```console
$ nomi init github.com/you/myproject
wrote nomi.toml and go.mod
next: nomi run main.nomi
```

`nomi.toml` describes the Nomi side of the module: its short name and its
entry points. `go.mod` lists its **dependencies**, the other modules it uses.
Commit `go.mod` and `go.sum` with your code. To add a dependency, run `nomi
add` with the module's full name:

```console
$ nomi add github.com/user/somelib
```

`nomi add` does this:

- It adds the module to `go.mod`, at its latest release. To pick a version,
  write it after an `@`: `nomi add github.com/user/somelib@v1.2.0`.
- It downloads the module from the Go module proxy, the public server that
  Go uses to hand out published modules.
- It writes the module's checksum to `go.sum`. A checksum is a fingerprint of
  the module's files. A later download that doesn't match it is refused, so
  everyone who builds the project gets the same code.

A module that isn't published, such as one beside your project, is added by
its directory. The path starts with `./` or `../`:

```console
$ nomi add ../shared
added example.com/shared from ../shared (module shared; import shared)
```

`go.mod` then requires the module and points it at that directory:

```gomod
require example.com/shared v0.0.0

replace example.com/shared => ../shared
```

This example holds both projects: `app`, which runs, and `shared` beside
it, which `app` added this way. `app/main.nomi` imports the function `greet`
from `shared`'s file `shared.nomi`. Change the greeting in `shared.nomi` and
run it again.

```nomi-run
// FILE: app/main.nomi
import {
    std/io
    shared.greet
}

fn main() {
    io.print(greet("Ada"))
}

// FILE: app/nomi.toml
[module]
name = "app"
entry_points = ["main"]

// FILE: app/go.mod
module example.com/app

go 1.27.0

require example.com/shared v0.0.0

replace example.com/shared => ../shared

// FILE: shared/shared.nomi
/// A greeting for `name`.
pub fn greet(name: String) -> String {
    "Hello, ${name}!"
}

// FILE: shared/nomi.toml
[module]
name = "shared"

// FILE: shared/go.mod
module example.com/shared

go 1.27.0
```
<!-- expect
Hello, Ada!
-->

Two more commands change the list:

- `nomi remove github.com/user/somelib` takes a dependency out of `go.mod`
  and `go.sum`. It refuses while a file still imports it, and names the file.
- `nomi update` moves every dependency to its latest release, and `nomi
  update github.com/user/somelib` moves one. It prints each change, such as
  `github.com/user/somelib v1.2.0 -> v1.3.0`. A new major version has a new
  full name ending in `/v2`, so `nomi update` never takes it; add it with
  `nomi add github.com/user/somelib/v2`.

Other commands download dependencies too:

- `nomi run`, `nomi test`, `nomi check` and `nomi build` download any
  dependency in `go.mod` that isn't on your machine yet.
- `nomi fetch` downloads everything the project needs ahead of time, for
  example before you go offline.
- `--offline`, given to any of these commands, downloads nothing. A
  dependency that isn't on your machine is then an error that names it.

To import a dependency's files, start the path with its short name, the
`name` in its `nomi.toml`. If `somelib`'s `nomi.toml` says
`name = "somelib"`, then `import somelib/parse` imports its file
`parse.nomi`. `import somelib` alone imports the file at its root that has
the module's name, `somelib.nomi`. If your own module also has a file
named `somelib.nomi`, the import still means the dependency's file.

To publish a module, push a version tag such as `v0.1.0` to its public
repository. The Go module proxy then serves that version to anyone who adds
the module.

A Nomi module is built on a Go module, which is why both use the word
"module".

## The orphan rule

Suppose two libraries each wrote `impl Display for Int { … }`. Each would
compile on its own, but a program that used both would have two impls of
`Display` for `Int`. The **orphan rule** prevents this. An
`impl Iface for T { ... }` block must be in the Nomi module that declares
`Iface` or the one that declares `T`. An impl anywhere else is called an
orphan impl, and it is a compile error. The rule covers generic impls too,
such as `impl Iface for Box<T> where T: Bound { ... }`.

A program with no `nomi.toml`, such as a lone script, is a module of its own.
It declares neither `Display` nor `Int`, so it can't write
`impl Display for Int` either.

To implement someone else's interface for someone else's type, wrap the type
in one of your own. Declare `type MyId Int` in your module. Then
`impl Display for MyId { ... }` is allowed, because `MyId` is yours.

A generic interface, one with type parameters, gives one more way. Take
`Multiply<Rhs, Out>`, which `*` uses. Its one function is
`multiply(lhs: self, rhs: Rhs) -> Out`, so `Rhs` is the type of a parameter.
When a type you declare fills a parameter's slot like that, you may implement
the interface for someone else's type. A module that declares `Rate` may
write `impl Multiply<Rate, Decimal> for Decimal`, and then `price * rate`
works:

```nomi-run
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
    io.print(12.5d * rate)
}
```
<!-- expect
53.125
-->

Two libraries can't clash this way. No other module can write an impl that
names your `Rate`. If one library writes `Multiply<Rate, Decimal>` and
another writes `Multiply<Shares, Decimal>`, those are two impls at different
type arguments. Each call uses the one that matches the type on the right of
the `*`.

Your type has to meet two conditions:

- It must be the type argument itself, not inside one. `List<Rate>` doesn't
  count.
- It must fill a parameter's slot. `impl Multiply<Decimal, Rate> for Decimal`
  puts `Rate` in `Out`, which only the result uses, so it is still an orphan
  impl. It would give an existing `price * 2d` a second possible result type.

Any file can write ordinary functions that take an imported type. But it
can't add a function, field or variant to that type, or an impl the orphan
rule forbids.

The next chapter, [App Fields, Defer & Context](/capabilities-and-context/),
shows how to give code things like a logger or a config without global
variables. It covers app fields, replacing one with `with App.field = value`,
cleanup with `defer`, and contexts.
