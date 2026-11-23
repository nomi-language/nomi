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

One import stands alone. Two or more go in one `import` ... `end` block. You can
import a file, import single items from it, and rename what you import:

```nomi-run
import
    std::calendar.Date.parse
    std::io as console
end

fn main()
    parsed = parse("2026-05-27")

    console.print(String.length("hello"))
    console.print(Iter.count([1, 2, 3]))
    console.print(String.join(["a", "b", "c", "d"]))
    console.inspect(parsed)
end
```
<!-- expect
5
3
abcd
Ok(2026-05-27)
-->

- `std::io` imports the file `io` in the standard library, `std`. A path
  joins its parts with `::`. You then reach the file's functions through its
  last part, `io`, as in [`io.print`](/reference/io/#ioprint).
- `std::calendar.Date.parse` imports the function `parse` from the type `Date`.
  You then call it as plain `parse`.
- `as console` renames what you import. Here `std::io` is reached as `console`
  instead of `io`. Use this when a name clashes with one you already have.
- `String`, `Iter`, `List`, `Map` and the other prelude types need no import.

To import several items from one type or file, list them in `{...}`:

```nomi
import shape.Shape.{Circle, Rectangle}
```

That imports the variants `Circle` and `Rectangle` of the `Shape` enum. To
import `Shape` itself as well, add `self`:

```nomi
import shape.Shape.{self, Circle, Rectangle}
```

That imports `Shape`, `Circle` and `Rectangle`. `self` means whatever is
before the `{`. So `import std::calendar.{self, Date}` imports both the file
`calendar` and the type `Date`.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- An imported name nothing uses is an error, item by item:
  `imported name 'Months' is unused — remove it from the import`.
- Importing a name the prelude already gives you is an error too:
  `import std::iter.Iter` is
  `imported name 'Iter' is already in scope from the prelude — remove it from the import`.
- A variant comes in through its enum: `import shape.Circle` is an error
  that suggests `import shape.Shape.{Circle}`.
- Braces select names from one file. `import std::{io, regex.Regex}` is an
  error, and `import std` alone names no file.
- A path is written with `::`, never `/`: `import std/io` is the error
  ``import paths use `::`: write `std::io` ``.
- Imports come first in a file. An import at the top of a block binds its
  names in that block only.

Spec: [Import forms](/spec/files-modules-and-imports/#import-forms),
[Unused imports are errors](/spec/files-modules-and-imports/#unused-imports-are-errors),
[Re-importing a prelude name is an error](/spec/files-modules-and-imports/#re-importing-a-prelude-name-is-an-error).

</div>

## Declaration order and cycles

A file can use a name declared anywhere in it or in any file it imports, in
any order. You don't need to declare anything ahead of time. Two files can
even import each other. That works, though it may mean the code should be
split differently.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A `once` whose initializer ends up reading its own value is a compile
  error that names the chain, whichever files the cycle passes through.
- A `fn` nested in a body is the exception to "any order": it is visible
  only below its declaration, so nested functions can't call each other in
  a cycle.

Spec: [Cyclic imports](/spec/files-modules-and-imports/#cyclic-imports),
[Resolution and declaration order](/spec/declarations-and-scope/#resolution-and-declaration-order).

</div>

## Files and modules

A program in one file, like every example so far, needs nothing more. A
larger project is a Nomi module: a directory with a `nomi.toml` and one or
more `.nomi` files.

Files in a module import each other by their path from the module's
directory, without `.nomi`. A file in the same directory is imported by its
name (`math`). A file in a subdirectory is imported by its path, with `::`
between directories (`http::header`).

The next example is a small module with two `.nomi` files and a `nomi.toml`.
In a real project, the three files would sit side by side in one directory.

```nomi-run
// FILE: math.nomi
pub fn double(n: Int) -> Int
    n * 2
end

// FILE: main.nomi
import
    std::io
    math
end

fn main()
    io.print(math.double(7))
end

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

## Short names and directory imports

A file is used by its short name, the last part of its path: after
`import db::posts`, you call `posts.post_by_id(1)`.

Import a directory to reach the files under it. `import views` binds
`views`, and a path below it reaches a file: `views::users.heading()`, or
`views::admin::audit.last()` a level deeper. Only the files a path names
become part of the program. A directory import also tells apart two files
of one short name, and reaches a file whose short name a declaration has.
Here two files are both `notifications`, and a function is called `users`:

```nomi-run
// FILE: db/notifications.nomi
pub fn pending() -> Int
    3
end

// FILE: jobs/notifications.nomi
pub fn pending() -> Int
    7
end

// FILE: views/users.nomi
pub fn heading() -> String
    "Users"
end

// FILE: main.nomi
import
    std::io
    db
    jobs
    views
end

fn users() -> List<String>
    ["ada", "grace"]
end

fn main()
    io.print(db::notifications.pending() + jobs::notifications.pending())
    io.print("${views::users.heading()}: ${users()}")
end

// FILE: nomi.toml
[module]
name = "app"
entry_points = ["main"]
```
<!-- expect
10
Users: [ada, grace]
-->

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A file import binds its short name only. `db::posts.find(1)` after
  `import db::posts` is an error whose fix writes `posts.find(1)`.
- Two imports of one short name are an error at the second, and so is an
  import whose short name a declaration has.
- Where `views/admin.nomi` sits beside `views/admin/`, `import views::admin`
  names the file. `import app`, the module's own name, imports its root.

Spec: [Short names](/spec/files-modules-and-imports/#short-names),
[Directory imports](/spec/files-modules-and-imports/#directory-imports).

</div>

## Visibility

A declaration at the top of a file is private unless you mark it `pub`. Only
its own file can use a private declaration. Mark it `pub` to let other files
use it:

```nomi-run
// FILE: math.nomi
pub fn double(n: Int) -> Int
    n * 2
end

// Private: only math.nomi can call this directly.
fn helper(n: Int) -> Int
    n + 1
end

// FILE: main.nomi
import
    std::io
    math
end

fn main()
    io.print(math.double(7))

    // Uncomment to see that main.nomi can't call math.nomi's private
    // `helper`:
    // io.print(math.helper(7))
end

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
see each other's private declarations. The one exception is a file's partner
test, `math_test.nomi` for `math.nomi`, which can use them
([Testing](/testing/)).

Functions in an `impl Interface for Type` block don't take `pub`. If the
interface is `pub`, other files can call them. If the interface is private,
so are they.

Other functions take `pub` one by one, like any declaration. A file can't add
functions to a type declared in another file. To give another file's type new
behavior, implement an interface for it.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A `pub` declaration can't expose a private type. A `pub fn` that returns
  a private `Secret` is `function return exposes private type Secret`, and
  the same holds for parameters, fields, `embeds`, aliases and `pub once`.
  A `pub opaque` type is the exception, since nothing outside reaches its
  contents.
- A file may still pass its private type to another file's generic
  function. Only its own file can name it.
- A file qualifier is not a value: `x = io` is `` `io` is a file, not a value ``.
- A function in an `impl` block is reached through its type, never its
  file: `duration.seconds(1)` is an error whose help names
  `Duration.seconds`.

Spec: [Visibility](/spec/files-modules-and-imports/#visibility),
[Visibility consistency](/spec/files-modules-and-imports/#visibility-consistency).

</div>

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

impl UserId
    pub fn new(n: Int) -> Result<UserId, String>
        if n > 0 then
            Ok(UserId(n))
        else
            Err("user IDs must be positive")
        end
    end

    pub fn value(id: UserId) -> Int
        UserId(n) = id // only ids.nomi can destructure UserId
        n
    end
end

// FILE: main.nomi
import
    std::io
    ids.UserId
end

fn main()
    case UserId.new(42)
        Ok(id) -> io.print(UserId.value(id))
        Err(e) -> io.print("error: ${e}")
    end

    case UserId.new(-1)
        Ok(_)  -> io.print("unexpected")
        Err(e) -> io.print("error: ${e}")
    end

    // Uncomment to see that main.nomi can hold a UserId, but can't
    // build or take apart one directly:
    // bad = UserId(7)
end

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
pub import other::parser.Parser

// Re-export several items at once.
pub import std::io.{print, write}

// An alias is the name consumers see: `parse_source`, not `parse`.
pub import other::parser.parse as parse_source

// A block re-exports every entry.
pub import
    std::comparable.Ordering
    calc.{add, subtract}
end
```

`pub import` makes public every item the statement imports. Items you only
use yourself go in a separate plain `import`.

You list each item you re-export. There is no `*` to re-export everything.
So when a file you import gains a new public name, your file's API doesn't
change. You can only re-export an item that is already `pub` where it is
declared.

You can re-export items, but not a whole file. `pub import other::parser` is
an error. List the items your users need, or have them import `other::parser`
themselves.

`pub import` works only on imported names. A new name given with `as` must be
the same kind of name as the original: snake_case for a function, such as
`parse_source`, and PascalCase for a type, such as `Parser`. To make a
declaration of your own public under another name, rename it, or write a
small function that calls it.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- `self` after a type re-exports the type:
  `pub import std::bool.Bool.{self, False, True}` is allowed. `self` after a
  file would re-export the file, which is an error.
- A file that a facade imports is not one of the facade's items.
  `import facade.{leaf}` is an error that says to import `leaf` directly.
- A re-exported item counts as used, so it needs no other use in the file.

Spec: [Re-exporting imported names](/spec/files-modules-and-imports/#re-exporting-imported-names).

</div>

## Nomi modules

A `nomi.toml` makes a directory a module. The standard library is one module.

A module lists the modules it uses in its `nomi.toml`'s `[dependencies]`
table, one entry per dependency. The entry's key is the name your imports
use. `nomi` writes the table, `nomi.lock` beside it, which locks each
download's version and hash, and for a module with a path a one-line
`go.mod`, so Go's module proxy can serve it. Commit all three. Published
modules come through that proxy, but you don't need Go installed.

- `nomi new myproject` starts a project in a new directory, `nomi new github.com/you/myproject`
  one others download by that path, and `nomi init` makes the current directory one.
- `nomi add github.com/user/somelib` adds a dependency, downloading it and
  locking it in `nomi.lock`. `nomi add ../shared` adds a module beside
  yours by its directory.
- `nomi remove` and `nomi update` take one out or move it to a newer
  release, and `nomi fetch` downloads everything ahead of time.

The tooling reference's [Modules and dependencies](/tooling/modules-and-dependencies/)
covers each command, versions and working offline.

This example holds two projects: `app`, which runs, and `shared` beside it,
which `app` added with `nomi add ../shared`. That wrote the entry
`shared = { dir = "../shared" }`. `app/main.nomi` imports the function
`greet` from `shared`'s file `shared.nomi`. Change the greeting in
`shared.nomi` and run it again.

```nomi-run
// FILE: app/main.nomi
import
    std::io
    shared.greet
end

fn main()
    io.print(greet("Ada"))
end

// FILE: app/nomi.toml
[module]
name = "app"
entry_points = ["main"]

[dependencies]
shared = { dir = "../shared" }

// FILE: shared/shared.nomi
/// A greeting for `name`.
pub fn greet(name: String) -> String
    "Hello, ${name}!"
end

// FILE: shared/nomi.toml
[module]
name = "shared"
```
<!-- expect
Hello, Ada!
-->

To import a dependency's files, start the path with its name, the key of
its entry in your `[dependencies]`. `nomi add` uses the `name` in the
dependency's own `nomi.toml` unless you give another, as in
`nomi add lib=github.com/user/somelib`. With the entry `somelib`,
`import somelib::parse` imports the dependency's file `parse.nomi`.
`import somelib` alone imports the file at its root that has that name,
`somelib.nomi`. If your own module also has a file named `somelib.nomi`,
the import still means the dependency's file. A dependency's own files
import through its own `[dependencies]`, so the names you choose never
change what its imports mean.

## The effects a project may have

A function is pure, or it has effects, such as files, the network,
output and the clock. The list of effects its own code may have is
`std` in an `[effects.self]` table in `nomi.toml`, and each entry is a
std module as an import names it. `nomi effects --write` writes the
tightest tables the program passes, for you to read and keep. `nomi
check`, `run`, `test` and `build` refuse a program that has any other
effect, before it runs. This program only prints, so `std::io` is enough:

```nomi-run
// FILE: main.nomi
import std::io

fn main()
    total = 19.99d * 3d
    io.print("Total: ${total}")
end

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[effects.self]
std = ["std::io"]
```
<!-- expect
Total: 59.97
-->

Hovering a call that has an effect, here and in your editor, says
`Has effects: std::fs` and whether `nomi.toml` allows it. Hover over
`read_text`:

```nomi-run
// FILE: main.nomi
import
    std::fs
    std::io
end

fn main()
    notes = fs.read_text("notes.txt") |> Result.with_default("") |> String.trim()
    io.print(notes)
end

// FILE: notes.txt
Buy milk.

// FILE: nomi.toml
[module]
name = "notes"
entry_points = ["main"]

[effects.self]
std = ["std::io", "std::fs"]
```
<!-- expect
Buy milk.
-->

The editor draws bold each call that can do something besides compute
its result, and the name of each function whose body holds one, up to
`main`. A call that acts only through a handle it is passed is bold too,
and its hover says `Has effects on what it is passed.` Here `report`,
`Writer.print`, both calls of `report` and `main` are bold, and the reads
of `io.stdout` and `io.stderr` are not, since they only get a value:

```nomi
import std::io.{self, Error, Writer}

fn report(to: Writer, count: Int) -> Result<Unit, Error>
    to |> Writer.print("${count} items")
end

fn main() -> Result<Unit, Error>
    report(io.stdout, 3)?
    report(io.stderr, 0)?
    Ok(Unit)
end
```

A project can limit each dependency too. In the project's own `nomi.toml`,
an entry under `[effects.deps.modules]`, keyed by the dependency's name in
`[dependencies]`, lists the effects its files may have, and
`entries_required = true` makes the project write one for every
dependency; the dependency itself declares nothing. Here `app`
allows `receipts` to read files and `prices` no effects. This version of
`prices` also reports each use to a server, so the program does not compile:

```nomi-run error
// FILE: app/main.nomi
import
    std::io
    prices.total
    receipts.footer
end

fn main()
    io.print("Total: ${total(3d)}")
    io.print(footer())
end

// FILE: app/nomi.toml
[module]
name = "app"
entry_points = ["main"]

[dependencies]
prices = { dir = "../prices" }
receipts = { dir = "../receipts" }

[effects.self]
std = ["std::io"]

[effects.deps]
entries_required = true

[effects.deps.modules]
"prices" = []
"receipts" = ["std::fs"]

// FILE: prices/prices.nomi
import
    std::http
    std::url.Url
end

/// The price of `count` items.
pub fn total(count: Decimal) -> Decimal
    _ = http.get(Url"https://stats.example.com/used")
    19.99d * count
end

// FILE: prices/nomi.toml
[module]
name = "prices"

// FILE: receipts/receipts.nomi
import std::fs

/// The line a receipt ends with, from the shop's own file.
pub fn footer() -> String
    fs.read_text("footer.txt") |> Result.with_default("Thank you.")
end

// FILE: receipts/nomi.toml
[module]
name = "receipts"
```
<!-- expect
prices/prices.nomi:8:9: prices has the effect std::http, and its entry under [effects.deps.modules] allows it no effects
prices/prices.nomi:8:9: help: reached from `main`; `main` calls `total`
prices/prices.nomi:8:9: help: add "std::http" to "prices" under [effects.deps.modules] in nomi.toml, or remove the call
app/main.nomi:8:24: note: your code has the effect std::http in prices here: `main` calls `total`
-->

The error is at the call in `prices`, and its note points at the call in
`app` that leads there, where the editor marks it too. A call counts even
in a branch that never runs, and the error says how the program reaches
it. A dependency can declare its own list in its own `[effects.self]`, which holds what its public functions can do, not only what its tests reach,
and a project that limits its dependencies checks the dependency against
that list too. Also under `[effects.deps]`,
`std` is the most effects any one dependency may have, whatever its entry
says. The project's own modules
can have entries too, under `[effects.self.modules]`, each keyed by
the module as an import names it. To keep a module from obtaining access
to anything, give it an empty entry, such as `"pricing" = []`.
[Effects lists](/tooling/projects/#effects-lists)
in the tooling reference covers lists for tests and for each dependency, and
what counts as an effect.

## The orphan rule

Suppose two libraries each wrote `impl Display for Int`. Each would
compile on its own, but a program that used both would have two impls of
`Display` for `Int`. The **orphan rule** prevents this. An
`impl Iface for T` block must be in the Nomi module that declares
`Iface` or the one that declares `T`. An impl anywhere else is called an
orphan impl, and it is a compile error. The rule covers generic impls too,
such as `impl Iface for Box<T> where T: Bound`.

A program with no `nomi.toml`, such as a lone script, is a module of its own.
It declares neither `Display` nor `Int`, so it can't write
`impl Display for Int` either.

To implement someone else's interface for someone else's type, wrap the type
in one of your own. Declare `type MyId Int` in your module. Then
`impl Display for MyId` is allowed, because `MyId` is yours.

A generic interface, one with type parameters, gives one more way. Take
`Multiply<Rhs, Out>`, which `*` uses. Its one function is
`multiply(lhs: self, rhs: Rhs) -> Out`, so `Rhs` is the type of a parameter.
When a type you declare fills a parameter's slot like that, you may implement
the interface for someone else's type. A module that declares `Rate` may
write `impl Multiply<Rate, Decimal> for Decimal`, and then `price * rate`
works:

```nomi-run
import std::io

struct Rate
    per_unit: Decimal
end

impl Multiply<Rate, Decimal> for Decimal
    fn multiply(lhs: Decimal, rhs: Rate) -> Decimal
        lhs * rhs.per_unit
    end
end

fn main()
    rate = Rate{per_unit: 4.25d}
    io.print(12.5d * rate)
end
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

The next chapter, [App Fields, Defer & Context](/app-fields/),
shows how to give code things like a logger or a config without global
variables. It covers app fields, replacing one with `with App.field = value`,
cleanup with `defer`, and contexts.
