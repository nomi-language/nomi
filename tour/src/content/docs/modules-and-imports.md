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
before the `.{`. So `import std/maps.{self, Map}` imports both the file
`maps` and the type `Map`.

## Files And Modules

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
program.

Files in a directory named `internal/` can be imported only by files in the
same module.

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

Every kind of declaration can be `pub`: `pub fn`, `pub struct`, `pub enum`,
`pub interface`, `pub type` and `pub once`.

`main` is where a program starts. It is never something other files import.

A part of an API that stands on its own can go in a file of its own:

```nomi-run
// FILE: header.nomi
pub fn canonical_name(name: String) -> String {
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
// Re-export an imported type — consumers of this file see `Parser`.
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

Modules are one of the newer parts of Nomi, and they will change.

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

A module that already has a `nomi.toml` gets its `go.mod` from
`nomi new github.com/you/myproject .` in its directory.

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
```

`go.mod` then requires the module and points it at that directory:

```gomod
require example.com/shared v0.0.0

replace example.com/shared => ../shared
```

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
"module". When the tour says "Go package", it means Go code that a `gopkg`
declaration binds to. When it says "Nomi module", it means a directory with a
`nomi.toml`.

## Import order doesn't matter

A file can use a name declared in any other file, in any order. You don't need
to declare anything ahead of time. Two files can even import each other. That
works, though it may mean the code should be split differently.

## The orphan rule

Modules are where the
[orphan rule](/interfaces-and-dispatch/#the-orphan-rule) applies. It decides
which module may write an `impl Iface for T { ... }` block. The impl must be
in one of these:

- the Nomi module that declares the interface;
- the Nomi module that declares the type;
- the Nomi module that declares a type the interface's functions take as a
  parameter, through a type argument. For example, the module that declares
  `Rate` may write `impl Multiply<Rate, Decimal> for Decimal`.

The rule covers generic impls too, such as
`impl Iface for Box<T> where T: Bound { ... }`. So you can't write an impl of
one library's interface for another library's type, unless one of your types
is a type argument as above. If two of your dependencies could each write the
same impl, no program could use both of them. Breaking the rule is a compile
error.

The next chapter, [App Fields, Defer & Context](/capabilities-and-context/),
shows how to give code things like a logger or a config without global
variables. It covers app fields, replacing one with `with App.field = value`,
cleanup with `defer`, and contexts.
