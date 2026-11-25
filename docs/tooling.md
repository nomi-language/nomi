# Nomi tooling reference

This reference covers the `nomi` command and the files around a program:
the commands and their flags, `nomi.toml`, effects lists, packages and
dependencies, the test runner, the formatter, what each kind of build
writes, how a host calls a program, environment variables, the editor and
debugger support, and the JSON the commands write for tools. What
the language does is in the [specification](spec.md), and the standard
library's types and functions are in the [standard library
reference](/reference/). To install `nomi`, see [Installing
Nomi](install.md).

## Commands

`nomi` is the one tool: it runs, checks, tests, formats and builds Nomi
programs, and manages a project's dependencies. This page covers running
a program, the REPL, `nomi check`, tools and the caches.

### The `nomi` command

`nomi help` lists the commands. `nomi help <command>`, `nomi <command>
--help` and `nomi <command> -h` print one command's usage.

| Command | What it does |
|---|---|
| `nomi run [file]` | Run a program, or with no file the current package's ([Running a program](#running-a-program)). |
| `nomi <file>` | Run a script, as `nomi run` does ([Scripts](#scripts-nomi-file)). |
| `nomi` | Start the REPL ([The REPL](#the-repl)). |
| `nomi check [path]` | Check a program without running it, or with no path the current directory ([Checking](#checking-nomi-check)). |
| `nomi test [path]` | Run tests ([Running tests](#running-tests)). |
| `nomi fmt [-w\|-l\|-d\|-check] [paths]` | Format source ([Formatting](#formatting)). |
| `nomi effects [path]` | Print the effects a program has, and write or check the lists that allow them ([`nomi effects`](#nomi-effects)). |
| `nomi build [file]` | Write an executable ([Executables](#executables)), a browser folder with `--web` ([A program in the browser](#a-program-in-the-browser)), or a `.nomic` file with `--no-vm` ([Compiled programs](#compiled-programs-and-nomi-vm)). With no file, it builds the current package's program. |
| `nomi build-vm` | Write `nomi-vm`, or with `--reactor` the WASI reactor, with no program in it ([`nomi build-vm`](#nomi-build-vm)). |
| `nomi new`, `nomi init` | Start a package ([Starting a project](#starting-a-project-nomi-new-and-nomi-init)). |
| `nomi add`, `nomi remove`, `nomi update`, `nomi fetch`, `nomi outdated` | Change, fetch and list dependencies ([Add, update and remove dependencies](#add-update-and-remove-dependencies)). |
| `nomi tool <package> <name>` | Run a package's tool ([Tools](#tools-nomi-tool)). |
| `nomi types <paths>` | Write TypeScript declarations for the JSON a backend's types become ([TypeScript types](#typescript-types-nomi-types)). |
| `nomi phrases accept <locale> <phrase>` | Mark a translation current ([`[i18n]`](#i18n)). |
| `nomi editor install <editor>` | Set up Helix, Neovim, VS Code or Zed ([Setting up an editor](#setting-up-an-editor-nomi-editor)). |
| `nomi upgrade [--check] [<version>]` | Install the latest release, or the one named, and rewrite the editors' support ([Updating: `nomi upgrade`](#updating-nomi-upgrade)). |
| `nomi lsp` | The language server an editor starts ([The language server](#the-language-server)). |
| `nomi dap` | The debugger an editor starts ([Debugging](#debugging)). |
| `nomi mcp` | The compiler as tools for a coding agent ([Coding agents](#coding-agents-nomi-mcp)). |
| `nomi cache [clean\|prune [name]]` | List the caches and their sizes, empty them, or remove what nothing has used for a while ([Caches](#caches-nomi-cache)). |
| `nomi help [command]` | List the commands, or print one's usage. |
| `nomi version` | Print the version, as `nomi --version` does, and a line for each editor whose Nomi support is from another version. |

### Running a program

`nomi run <file> [args]` runs the file's top-level `fn main`, which need
not be `pub`. Every word after the file is the program's own argument.

```console
$ nomi run app.nomi
```

A file in a directory of its own needs no configuration file. That
directory is the project root, and every import resolves from it
([Project root discovery](#project-root-discovery)):

```tree
my_project/
├── app.nomi  # declares fn main; import math works
├── math.nomi  # import path math
└── models/
    └── user.nomi  # import path models::user
```

In a package (a directory with a `nomi.toml`), the file must be one of the
package's entry points. `nomi run <dir>` runs a package's one entry
point, and `nomi run` with no file or directory runs the current
directory's ([Project layout](#project-layout)):

```console
$ nomi run
```

A first word that is not a flag is always the file or directory, so the
program's arguments follow one: `nomi run . a b`.

| Flag, before the file | What it does |
|---|---|
| `--watch` | Run again on every change ([below](#running-again-on-every-change---watch)). |
| `--profile[=<file>]` | Report where the time went ([below](#where-the-time-goes---profile)). |
| `--offline` | Fetch no dependency ([`--offline`](#--offline)). |
| `--all-calls` | Print every call the effects lists refuse ([When it is checked](#when-it-is-checked)). |
| `--web` | Serve the program in a browser ([`nomi run --web`](#working-on-it-nomi-run---web)). |

`nomi run <file.nomic>` runs a compiled program ([Compiled
programs](#compiled-programs-and-nomi-vm)).

#### Running again on every change: `--watch`

`nomi run --watch <file> [args]` runs the program, and runs it again with
the same arguments each time the entry, a file it imports or the
project's `nomi.toml` changes. A run still going when a file changes is
stopped first. Ctrl-C stops it and exits with status 130.

#### Where the time goes: `--profile`

`nomi run --profile <file> [args]` runs the program and, when it ends,
prints on standard error where its time went, by Nomi function:

```console
nomi profile: 2.68s sampled over 2.68s, one sample every 1ms
garbage collector: 0.70s of CPU over 51 cycles, on threads beside the program

    self            total          function
   1.43s   53.1%    1.63s   60.6%  prefix_reversed  fannkuchredux.nomi:39
   0.37s   13.7%    2.00s   74.3%  flips  fannkuchredux.nomi:50
   0.20s    7.5%    0.20s    7.5%  List.concat  (host)
   0.18s    6.9%    0.18s    6.9%  insert_at  fannkuchredux.nomi:59
   0.14s    5.0%    0.45s   16.9%  next  fannkuchredux.nomi:89
   0.13s    4.8%    2.68s  100.0%  walk  fannkuchredux.nomi:105
   ...
```

A function's self time is spent in its own code, and its total time adds
what the functions it calls spent. A second table names the lines.
`(host)` marks a function the runtime implements, and `std` a std
function written in Nomi.

- A profile measures wall time, so a call that waits for input, a timer
  or a task is charged with its wait.
- A function that ends in a tail call is replaced by the function it
  calls, and drops out of the profile.

`--profile=<file>` also writes the profile to `<file>` in pprof's format,
which `pprof -http=: <file>` shows as a call graph and a flame graph.
`NOMI_PROFILE` profiles an executable `nomi build` wrote ([Environment
variables](#environment-variables)).

### Scripts: `nomi <file>`

`nomi <file> [args]` is `nomi run <file> [args]`. With a shebang line
(spec §2 *Shebang line*), a file runs as a command:

```console
$ cat hi.nomi
#!/usr/bin/env nomi
import std::io

fn main()
    io.print("hi")
end
$ chmod +x hi.nomi
$ ./hi.nomi a b
hi
```

Every argument after the file, flags included, is the program's own, so
`boot` reads `["a", "b"]` as `startup.args` (spec §15 *Scoped application
fields*). `nomi` reads its first argument this way:

1. A command name, such as `test`, is the command.
2. An argument that ends in `.nomi` is a file, whether or not it exists.
3. Any other argument is a file when it exists and starts with `#!`.
4. Anything else is an unknown command.

So a script can drop the extension and be named like any other command
(`~/bin/greet`). Its project root is its own directory, and it imports
the `.nomi` files beside it and below it.

### The REPL

`nomi` with no arguments starts the REPL. Each input may be any
declaration a file may hold, or a statement. An input sees the
declarations and bindings of the inputs before it, and an input that
fails changes nothing. An input continues onto the next line while a
bracket, a body or a string is open, or while the line ends in an
operator, a pipe or a comma.

Redefining a function or a type affects later inputs only: an earlier
function keeps what it was written against. Redefining an impl replaces
it everywhere.

### Checking: `nomi check`

`nomi check [path]` checks a program and compiles it without running it.
It exits 1 when it finds an error. With no path it checks the current
directory.

```console
$ nomi check main.nomi
error: List<Int> has no field 'len'
 --> main.nomi:5:20
  |
5 |     io.print("${xs.len()}")
  |                    ^^^
  = help: Nomi has no method calls: write `List.length(xs)`
```

The path is a file or a directory. A test file is checked as `nomi test`
loads it, with each case compiled and none run. A directory checks every
program in it, and prints `FAIL <file>` with its errors for each file
that holds any.

| Flag | What it does |
|---|---|
| `--format full` | Each error with its source lines. The default. |
| `--format short` | Each error as one `path:line:col: message` line. `NOMI_DIAGNOSTICS=short` does this for every command. |
| `--format json` | JSON Lines on standard output ([Output for tools](#nomi-check---format-json)). |
| `--hints` | Also report the hints the editor shows. |
| `--offline` | Fetch no dependency ([`--offline`](#--offline)). |
| `--all-calls` | Print every call the effects lists refuse ([When it is checked](#when-it-is-checked)). |

`--hints` reports each of these as a `hint:` line that fails no file:

- `redundant-ascription`: a `value as Type` whose value has the type
  without it;
- `restated-current-value`: a field's new value that reads the field's
  current value by its path, which `..` writes;
- `plain-counterpart`: a call that returns an `Option` the value's
  subtype answers for sure, such as `List.first` of a `NonEmptyList`.

### When code cannot run yet

The compiler cannot yet compile every program the checker accepts. A
program that reaches such code fails before its first effect:

- `nomi run` prints an error at the code, such as ``this call to
  `skip_odd` is not supported yet, so `fn evens` cannot run``, and exits 1.
- `nomi check` reports the same error, for everything `main` or a test
  reaches.
- `nomi test` runs every test it can and reports each one it cannot as
  `BLOCKED`, with the location, and exits nonzero.

The gap is in Nomi, not in your program. `NOMI_DEBUG_LOWERING=1` adds
the compiler's own reason to the error, which helps a bug report.

### Tools: `nomi tool`

A package can ship programs for its users to run, such as a code
generator. `nomi tool <package> <name> [args]` runs the program
`tools/<name>.nomi` at that package's root, with the arguments after the
name:

```console
$ nomi tool sqlkit generate
```

- `<package>` is the current package's own `[package].name`, or the name
  the current `nomi.toml`'s `[dependencies]` gives a dependency.
- A tool is an ordinary program. It runs in the current directory with
  the same access to files, the network and output that `nomi run` gives,
  and only when someone runs it.
- A tool is an entry point without being listed in `entry_points`
  ([Project layout](#project-layout)).
- A dependency's tool builds with the current project's dependencies: its
  packages at the versions the project takes, and the project's `dir`
  entries. The tool's own `[dependencies]` say which packages it needs,
  and its own `dir` entries are not read.

The tool finds `<package>`, the name the project gives its package, in
`NOMI_TOOL_PACKAGE` in its `startup.env`. It reads its settings from the
project's `nomi.toml` in the table of that name, and code it generates
imports the package's files by it. So a project with `db = { repo =
"github.com/nomi-language/nomi", subdir = "lib/sql/sqlkit", ... }` writes
its settings under `[db.database.main]`, runs `nomi tool db generate`, and
gets code that imports `db::sqlkit` and `db::runtime`.

### TypeScript types: `nomi types`

`nomi types <paths> [-o <file>]` writes TypeScript declarations for the
JSON a Nomi backend's types become, so a frontend written in TypeScript
shares its API's shapes:

```console
$ nomi types models.nomi api -o ../web/src/api-types.ts
```

It declares each type the named files and directories declare that
becomes JSON (it derives or implements `ToJson`, or derives `FromJson`),
and every type those reach, each once. The output goes to standard
output, or to the file `-o` names, and holds types only: no client and no
validation.

| Nomi | TypeScript |
|---|---|
| `Int`, `Float` | `number`. An Int past 2^53 loses precision in JavaScript, so send such Ints as `String`s. |
| `String` | `string` |
| `Bool` | `boolean` |
| `Decimal` | `string`, its exact digits (`"12.50"`) |
| `Uuid` | `string`, lowercase 8-4-4-4-12 hex |
| `Instant` | `string`, RFC 3339 in UTC |
| `Date`, `Time`, `NaiveDateTime` | `string` (`YYYY-MM-DD`, `HH:MM:SS[.fff]`, `YYYY-MM-DDTHH:MM:SS[.fff]`) |
| `List<T>`, `Vector<T>`, `Set<T>` | `T[]` |
| `Map<String, V>` | `Record<string, V>` |
| `Option<T>` | `T \| null`; a field is always present, and optional (`title?: T \| null`) in a type only `FromJson` reads |
| a field with a default | its type; optional (`plan?: string`) in a type only `FromJson` reads |
| a `Presence<T>` field | an optional property of `T`: `name?: string`, and `bio?: string \| null` for `Presence<Option<String>>` |
| `Presence<T>` outside a field | `T \| null` |
| a struct | an `interface` of its fields under their JSON keys, `rename_all` applied |
| an enum | a union: a bare variant's name as a string, `{ Variant: payload }` for the others, a tuple payload as `[A, B]` |
| a distinct type | a `type` of its inner type |
| `typealias A B` | `type A = B` |
| a generic type | a generic `interface` or `type`: `Page<T>`, referred to as `Page<User>` |
| a type whose `ToJson` is written by hand | `unknown`, with a comment that says so |

A type that only derives `FromJson`, such as a request body, maps to the
JSON its `FromJson` reads, and its JSDoc comment says so. A `///` comment
becomes the JSDoc comment. Two types of one name in different files are
prefixed with their file's name (`ModelsUser`, `OrdersUser`).

### Caches: `nomi cache`

`nomi run`, `nomi check`, `nomi test` and `nomi build` keep each program
they check and compile, and the next command over the same files reads it
instead: an unchanged program of 50,000 lines runs in about 0.06 s
instead of 2 s. `nomi` keeps four caches, and `nomi cache` prints where
each is and its size:

```console
$ nomi cache
packages   /Users/me/Library/Caches/nomi/packages   2.1 MiB
artifacts  /Users/me/Library/Caches/nomi/artifacts  184.3 MiB
runners    /Users/me/Library/Caches/nomi/builds     41.0 MiB
std        /Users/me/.cache/nomi/std                1.2 MiB
```

| Cache | What it holds | Moved by |
|---|---|---|
| `packages` | Each repository fetched for a project's dependencies ([Dependencies](#dependencies)). | `NOMI_PACKAGE_CACHE` |
| `artifacts` | Kept programs, compiled WebAssembly modules and the standard library's artifacts. | `NOMI_ARTIFACT_CACHE`, and `NOMI_ARTIFACT_CACHE=off` keeps nothing |
| `runners` | The `nomi-vm` runners `nomi build` builds, unpacks or downloads ([Executables](#executables)). | `NOMI_RUNNER_CACHE_ROOT` |
| `std` | The standard library's source, for go-to-definition ([The language server](#the-language-server)). | `NOMI_STD_CACHE_ROOT` |

The user cache directory is `~/Library/Caches` on macOS, `~/.cache` on
Linux and `%LocalAppData%` on Windows. Each cache removes what nothing has
used for a while on its own.

- `nomi cache clean` empties every cache, and `nomi cache clean <name>`
  one. Use it, not `rm -rf`, which cannot remove the package cache's
  read-only files.
- `nomi cache prune [name]` removes now what nothing has used for long
  enough, and keeps what recent commands use.

`NOMI_DEBUG_ARTIFACT_CACHE=1` says on standard error why a command read
or kept no program.

## Projects

A package is a directory tree with a `nomi.toml` at its root. This page
covers starting one, how its files are laid out, how a command finds a
file's project root, and what `nomi.toml` holds. The effects tables have
[their own page](#effects), and `[dependencies]` is covered under
[Packages and dependencies](#packages-and-dependencies).

### Starting a project: `nomi new` and `nomi init`

```console
$ nomi new hello
wrote hello/nomi.toml, hello/main.nomi and hello/AGENTS.md
next: cd hello && nomi run
```

`nomi new <name> [dir]` starts a package in `dir`, or in a new directory
named `name`. It writes:

- `nomi.toml`, with the `name`, `entry_points = ["main"]`, and as `nomi`
  the release of the `nomi` that wrote it ([The nomi a package
  needs](#the-nomi-a-package-needs));
- `main.nomi`, a program that prints `hello`;
- `AGENTS.md`, for coding agents: it names the commands and links the
  [cheat sheet](https://nomi-lang.org/cheatsheet.md).

A name is lowercase letters, digits and `_`, not starting with a digit,
as an import path starts: `hello_world`, not `hello-world`.

`nomi init <name>` makes the current directory a package. It writes only
a `nomi.toml`, with `entry_points` listing every file under the directory
that has a top-level `fn main`.

```console
$ nomi init shop
wrote nomi.toml
next: nomi run
```

### Project layout

Every file under a package's root belongs to the package, in any
directory, and sees the other files' declarations that are not `private`
(spec §14 *Visibility*). A directory with its own `nomi.toml` is another
package.

```tree
my_project/
├── nomi.toml  # name, entry points and dependencies
├── nomi.lock  # the fetched dependencies' versions, commits and hashes
├── main.nomi  # an entry: fn main and an optional fn boot
├── tools/
│   └── seed.nomi  # a tool, run by nomi tool my_app seed
├── math.nomi  # a library file (no fn main)
└── models/
    └── user.nomi
```

```console
$ cd my_project
$ nomi run                  # runs the entry main.nomi: entry_points lists one file
$ nomi run main.nomi        # the same, by its file
$ nomi tool my_app seed     # runs tools/seed.nomi
```

**Entry points.** `entry_points` in `nomi.toml` lists the files with a
`fn main`, by their path from the root without `.nomi` (`"main"`,
`"cli/admin"`). Every other file must have no `fn main`.

**Tools.** Every `.nomi` file under `tools/` is a tool and an entry point
without being listed: `nomi tool <package> <name>` runs it ([Tools](#tools-nomi-tool)).
A tool's helpers go under `tools/internal/`, which holds no tools.

**A directory in place of a file.** `nomi run`, `nomi build` and their
modes take a package's directory in place of a file when `entry_points`
lists one file. With no file or directory they take the package the
current directory is in, the nearest directory upward with a `nomi.toml`,
so `nomi run`, `nomi run --watch` and `nomi build` anywhere inside a
package act on its one entry point. With several entry points the error
names them, to name one.

### Project root discovery

A command and the editor find a file's project root by walking up from
the file:

1. The first directory with a `nomi.toml` is the root, and the file
   belongs to that package.
2. Otherwise, the first directory with a `main.nomi` is the root.
3. Otherwise, the file's own directory is the root.

Imports resolve from the root. So a file in a subdirectory, such as
`app/prod.nomi`, imports `types.nomi` one directory up when the
directory above holds `main.nomi` or `nomi.toml`.

`nomi run`, `nomi test`, `nomi check`, the language server and the other
tools share this rule, so a file opened in an editor resolves its imports
to the files `nomi run` loads. In the editor, the walk stops at the
folder the editor opened.

### `nomi.toml`

`nomi.toml` is what a package says about itself: its name, its entry
points and its dependencies. `nomi.lock` beside it records the version,
commit and hash of each dependency fetched for it ([`nomi.lock`](#nomilock)).
The `nomi` commands write both, and a project commits both.

```toml
# nomi.toml
[package]
name = "shop"
nomi = "v0.0.1"
entry_points = ["main"]

[dependencies]
markdown = { repo = "github.com/owner/markdown", version = "v0.3.0" }
sqlite = { repo = "github.com/nomi-language/nomi", subdir = "lib/sql/sqlite", version = "v0.0.1" }
helpers = { dir = "../helpers" }
```

| Table | Key | What it holds |
|---|---|---|
| `[package]` | `name` | The package's own name. Its own files may start an import path with it (`import shop::orders`), and `nomi add` gives the package this name by default. Required in a dependency. |
| | `nomi` | The lowest `nomi` release the package needs ([The nomi a package needs](#the-nomi-a-package-needs)). |
| | `entry_points` | The files with a `fn main`, other than tools ([Project layout](#project-layout)). |
| `[dependencies]` | one key per dependency | The name the package's files import the dependency by, and where it comes from ([Dependencies](#dependencies)). |
| `[i18n]` | `locales` | The locales every phrase is translated into, whose CLDR data the program carries ([`[i18n]`](#i18n)). |
| | `partial_locales` | Locales whose catalogs may leave a phrase out, which then shows in the source locale. Needs `locales`. |
| | `source_locale` | The locale of `phrase` declarations' own text. `en` when left out. |
| | `catalogs` | The catalog directory under the package root. `locales` when left out. |
| | `web_embedded` | The locales a browser build puts in `app.wasm`, the source locale when left out ([Locales in the browser](#locales-in-the-browser)). |
| `[money]` | `custom_currencies` | The custom currencies the package's literals may write, each as its literal writes it: `["PTS:0", "BTC:8"]` ([std::currency](/reference/currency/)). Each package's list covers its own literals only. |
| `[effects.self]` | `effects`, `effects_in_tests`, `entries_required` | The effects the package's own code may have ([The tables](#the-tables)). |
| `[effects.self.modules]` | one key per module | The effects one module's own file may have. |
| `[effects.dependencies]` | `effects`, `effects_in_tests`, `entries_required` | The most effects any dependency may have. |
| a package's own name, or a `[dependencies]` key | anything | That package's tools' settings ([Tools](#tools-nomi-tool)). |

Any other table or key is an error at its line:

```console
$ nomi check main.nomi
error: [package] has no key `entry_point`; did you mean `entry_points`?
 --> nomi.toml:3:1
  |
3 | entry_point = ["main"]
  | ^^^^^^^^^^^^^^^^^^^^^^
```

A `nomi.toml` in error stops `nomi check`, `run`, `test`, `build` and the
language server with that one error, since imports resolve through it. An
error in a dependency's `nomi.toml` is in that file, with a note at the
project's entry that reaches it.

### `[i18n]`

`[i18n]` names the locales whose CLDR data the program carries ([A
program's locale data](#a-programs-locale-data)) and how its phrases are
translated (spec §18 *Phrases*):

```toml
# nomi.toml
[i18n]
source_locale = "en"
locales = ["en", "pl", "pt", "pt-PT"]
partial_locales = ["de"]
catalogs = "locales"
```

Each locale is a BCP 47 tag, and `Locale.available()` returns them in the
order written. A program with no `[i18n]` table carries every locale.

Each translation is an entry in a catalog: a TOML file at the declaring
file's path under the catalog directory, in a directory per locale. So
`shop/cart.nomi`'s Polish catalog is `locales/pl/shop/cart.toml`. What a
catalog holds is in [std::phrase](/reference/phrase/).

`nomi phrases accept <locale> <phrase>` marks a locale's translation of a
phrase current. It records the phrase's text as it is now as the source
the translation was made from. Run it once the translation says what the
current text says. `<phrase>` is the name of a `phrase` declaration, or
`path/to/file:name` when two files of the package declare a phrase of
that name.

## Effects

A function is pure, or it has effects: it reads or changes something
outside the program, such as files, the network or the clock. A project
can list in `nomi.toml` the effects its program may have. `nomi check`,
`run`, `test` and `build` then refuse a program that has more, before it
runs. `nomi effects` prints the effects a program has and writes the
lists for you.

### Effects lists

Lists catch code that starts doing something it was not meant to. A
helper that starts making network requests is a compile error at the
call. So is a dependency that gains an effect in an upgrade, and the
error names the package.

To start, run `nomi effects --write` in the project. It writes the
tightest lists the program passes, from the effects it has today. Read
what it wrote: each name is something the program does, and a name you
did not expect points at a call to look at.

```console
$ nomi effects --write
wrote nomi.toml's [effects] tables:

[dependencies]
pricing = { dir = "../pricing", effects = ["std::fs"] }

[effects.self]
effects = ["std::io"]

[effects.dependencies]
effects = ["std::fs"]
```

Later, `nomi effects` says how the lists and the program differ, and
`nomi effects --why <name>` lists the calls that have an effect
([`nomi effects`](#nomi-effects)). In the editor, a call the lists do not
allow has a quick fix that adds its name to `nomi.toml`, and a hover on a
call or a function says which effects it has and whether the lists allow
them ([The language server](#the-language-server)).

A project with no lists is held to nothing. Lists are separate from
[effect classes](#effect-classes): a class is what a build target cannot
do, and a list is what a project allows its program to do.

### An example

This project's package is named `app`. Its own code is `main.nomi`,
`totals.nomi` and the test file `main_test.nomi`, and it depends on four
packages:

```toml
[dependencies]
# computes, and has no effects
pricing = { dir = "../pricing", effects = [] }
# reads and writes files
reports = { repo = "github.com/owner/reports", version = "v0.0.1", effects = ["std::fs"] }
# the one dependency that may make HTTP requests; its tests start a server
fetch = { repo = "github.com/owner/fetch", version = "v0.0.1", effects = ["std::http"], effects_in_tests = ["std::process"] }
# reads its configuration file
pg = { repo = "github.com/owner/pg", version = "v0.0.1", effects = ["std::fs"] }

[effects.self]
# the effects this package's own code may have
effects = ["std::io", "std::fs"]
# added to effects for its tests
effects_in_tests = ["std::process"]

[effects.self.modules]
# prints and reads files
"main" = ["std::io", "std::fs"]
# computes, and has no effects
"totals" = []
# the test file, which starts a process
"main_test" = ["std::process"]

[effects.dependencies]
# every [dependencies] entry must have an effects key
entries_required = true
# no dependency may have more effects than this
effects = ["std::fs", "std::http"]
# added to effects for the tests
effects_in_tests = ["std::process"]
```

- The project's own code may print and use files. Its tests may also
  start processes.
- Each module has an entry under `[effects.self.modules]`, which narrows
  that for the code in its own file: `main` prints and reads files,
  `totals` has no effects, and the test file `main_test` starts a process.
- No dependency may have more effects than files and HTTP requests, plus
  processes in the tests.
- Each `[dependencies]` entry has an `effects` key, which narrows that for
  the dependency and the packages it brings in. `pricing` has no effects,
  `reports` uses files, `fetch` is the only one that may use the network,
  and `pg` reads its configuration file.
- No code in the program may read the clock or open a socket, since no
  list names `std::instant` or `std::net`.

Suppose an upgrade of `reports` starts sending a request each time it
saves a report. `nomi run main.nomi` prints this and exits 1 before
`main` starts, and `nomi check` and `nomi build` refuse the program in the
same words:

```console
error: reports has the effect std::http, and its entry under [dependencies] allows it only std::fs
 --> /work/reports/summary.nomi:9:9
  |
9 |     _ = http.get(Url"https://stats.example.com/saved")
  |         ^^^^
  = help: reached from `main`; `main` calls `save`
  = help: add "std::http" to effects of reports under [dependencies] in nomi.toml, or remove the call
  = note: your code has the effect std::http in reports here: `main` calls `save`: main.nomi:14:5
```

A name is a whole std module. `std::fs` allows every path and
`std::http` every host; no list can name one file or one host. For
narrower authority, pass a handle ([Authority by handle](#authority-by-handle)).

### The tables

Three tables and two keys on each `[dependencies]` entry hold the lists.
Each list holds only the calls written in its own files
([Which list holds a call](#which-list-holds-a-call)).

| Where | Holds the calls written in |
|---|---|
| `[effects.self]` | the project's own files |
| `[effects.self.modules]` | one module of the project, per entry |
| `[effects.dependencies]` | the files of every package the project depends on, at any depth |
| `effects` on a `[dependencies]` entry | the files of that package and of every package the project reaches through it |

| Key | What it holds |
|---|---|
| `effects` | The effects the code may have. An empty list allows none. |
| `effects_in_tests` | More effects that only the test cases may have. |
| `entries_required` | In `[effects.self]` or `[effects.dependencies]`: when `true`, every module of the project, or every `[dependencies]` entry the program loads files through, must have an entry. `false` when left out. |

- A call must be in every list that holds it: a module's entry and
  `[effects.self]`, or a dependency's entry and `[effects.dependencies]`.
- A key under `[effects.self.modules]` is the module's import path within
  the project (spec §14 *Modules and packages*): `reports/daily.nomi` is
  `reports::daily`, and a test file is a module of its own.
- With no `effects` key, `[effects.dependencies]` sets no ceiling and
  only the entries hold. A `[dependencies]` entry with no `effects` key
  leaves its package to the ceiling alone.
- A name in a list is a std module as an import writes it (`std::fs`),
  or `std::startup`.

### What counts as an effect

An effect is a call that acts on, or reads from, something outside the
program: files, the network, processes, output and input, the clock,
entropy, the environment, tasks and channels. Using a handle you were
given is not one: the call that made the handle is (spec §19 *Effects*).

### Functions with effects

A call to one of these public std functions has the effect its row names,
which is the name a list must hold for it. The standard library reference
marks the same functions "Has effects". A function that acts only through
a handle it is passed, such as `Dir.write_text` or `Writer.print`, is not
in the table, though the editor draws its calls bold ([Bold
calls](#bold-calls)).

| Module | Functions with effects |
|---|---|
| `std::actors` | `Actor.call`, `Actor.start`, `Agent.get`, `Agent.get_and_update`, `Agent.start`, `Agent.update` |
| `std::calendar` | `DateTime.local_zone`, `DateTime.now_in`, `DateTime.now_local`, `NaiveDateTime.utc_now` |
| `std::channels` | `Arm.after`, `Channel.buffered`, `Channel.unbuffered`, `Receiver.receive_within` |
| `std::cipher` | `Key.generate`, `cipher.seal` |
| `std::compiler` | `compiler.check`, `compiler.check_project`, `compiler.hover`, `compiler.run`, `compiler.run_file` |
| `std::context` | `Context.deadline`, `Context.deadline_remaining`, `Context.with_timeout` |
| `std::fs` | `Dir.watch`, `Dir.watch_each`, `fs.chunks`, `fs.copy`, `fs.create`, `fs.create_dir`, `fs.create_link`, `fs.create_symlink`, `fs.current_dir`, `fs.disk_usage`, `fs.exists`, `fs.glob`, `fs.group_name`, `fs.lines`, `fs.link_metadata`, `fs.list`, `fs.make_temp_dir`, `fs.metadata`, `fs.open`, `fs.open_dir`, `fs.open_file`, `fs.read`, `fs.read_link`, `fs.read_text`, `fs.remove`, `fs.remove_all`, `fs.remove_all_quietly`, `fs.rename`, `fs.same_file`, `fs.set_link_owner`, `fs.set_mode`, `fs.set_owner`, `fs.set_times`, `fs.sync_dir`, `fs.temp_dir`, `fs.touch`, `fs.truncate`, `fs.user_name`, `fs.walk`, `fs.watch`, `fs.watch_each`, `fs.within`, `fs.write`, `fs.write_text` |
| `std::fs::testing` | `testing.conformance`, `testing.dir`, `testing.run` |
| `std::http` | `Body.reader`, `Client.new`, `Client.stream`, `Client.with_tls`, `CookieJar.new`, `FormFile.open`, `Request.dump`, `Request.dump_outgoing`, `Request.limit_body`, `Request.parts`, `Response.content`, `Response.dump`, `ReverseProxy.to`, `Tracer.new`, `http.default_client`, `http.fetch`, `http.file_server`, `http.file_server_embedded`, `http.get`, `http.head`, `http.limit_body`, `http.listen`, `http.log_requests`, `http.post`, `http.post_form`, `http.request_id`, `http.reverse_proxy`, `http.serve`, `http.serve_embedded`, `http.serve_file`, `http.serve_here`, `http.stream`, `http.timeout` |
| `std::http::testing` | `testing.chunks`, `testing.decompressed`, `testing.open_websocket`, `testing.with_server` |
| `std::instant` | `Instant.now`, `Monotonic.elapsed`, `Monotonic.now` |
| `std::io` | `Reader.from_bytes`, `Reader.from_chunks`, `Reader.from_fn`, `Reader.from_text`, `Reader.from_text_chunks`, `Writer.collect`, `Writer.from_fn`, `io.eprint`, `io.ewrite`, `io.inspect`, `io.is_terminal`, `io.lines`, `io.print`, `io.read_line`, `io.stderr`, `io.stdin`, `io.stdout`, `io.write` |
| `std::jobs` | `Worker.new`, `Worker.start`, `jobs.enqueue`, `jobs.run_one`, `jobs.wake` |
| `std::jobs::testing` | `Inline.claim`, `Inline.insert`, `Inline.new`, `Inline.prune`, `Inline.record_tick`, `Inline.release`, `Inline.renew`, `Inline.rescue`, `Inline.settle`, `testing.conformance`, `testing.rules`, `testing.run_queued` |
| `std::jwt` | `jwt.sign`, `jwt.sign_as`, `jwt.verify`, `jwt.verify_as` |
| `std::key_exchange` | `PrivateKey.generate` |
| `std::locale` | `Locale.load`, `Locale.loaded` |
| `std::log` | `Logger.json`, `Logger.text`, `log.debug`, `log.error`, `log.info`, `log.log`, `log.warn` |
| `std::log::testing` | `testing.run` |
| `std::mail` | `Smtp.send`, `mail.deliver`, `mail.render`, `mail.smtp` |
| `std::mail::testing` | `Memory.new`, `Memory.send`, `testing.with_smtp_server` |
| `std::net` | `net.dial`, `net.dial_unix`, `net.inherited_listeners`, `net.listen`, `net.listen_unix`, `net.lookup_address`, `net.lookup_cname`, `net.lookup_host`, `net.lookup_ip`, `net.lookup_mx`, `net.lookup_ns`, `net.lookup_port`, `net.lookup_srv`, `net.lookup_txt`, `net.pipe`, `net.serve` |
| `std::passwords` | `Argon2id.hash`, `Bcrypt.hash`, `Pbkdf2.hash`, `Scrypt.hash`, `passwords.hash`, `passwords.needs_rehash`, `passwords.verify` |
| `std::phrase` | `Catalog.load`, `Noun.localize` |
| `std::process` | `Child.stop`, `process.id`, `process.output`, `process.run`, `process.spawn` |
| `std::process::testing` | `testing.run` |
| `std::random` | `Seed.from_os`, `random.secure_bytes` |
| `std::sessions` | `Session.new`, `sessions.with_session`, `sessions.write` |
| `std::signals` | `signals.each`, `signals.receiver` |
| `std::signatures` | `PrivateKey.generate_ecdsa_p256`, `PrivateKey.generate_ecdsa_p384`, `PrivateKey.generate_ecdsa_p521`, `PrivateKey.generate_ed25519`, `PrivateKey.generate_rsa`, `signatures.encrypt`, `signatures.sign`, `signatures.sign_with` |
| `std::sql::testing` | `Fake.new`, `testing.conformance`, `testing.conformance_answer`, `testing.rules` |
| `std::storage` | `storage.handler`, `storage.local`, `storage.local_dir` |
| `std::storage::testing` | `Memory.new`, `Memory.put` |
| `std::supervisors` | `Supervisor.flush`, `Supervisor.new`, `Supervisor.spawn`, `Supervisor.spawn_all` |
| `std::system` | `system.arch`, `system.cache_dir`, `system.config_dir`, `system.cpu_count`, `system.data_dir`, `system.executable`, `system.home_dir`, `system.hostname`, `system.memory`, `system.nomi_release`, `system.os`, `system.runtime`, `system.user` |
| `std::tasks` | `Task.race`, `Task.spawn`, `Task.spawn_all`, `Task.stream`, `Task.try_await_all`, `Task.within` |
| `std::terminal` | `RawMode.release`, `RawMode.restore`, `terminal.raw_mode`, `terminal.read_key`, `terminal.read_key_within`, `terminal.read_password`, `terminal.size`, `terminal.size_of`, `terminal.supports_color` |
| `std::timer` | `timer.every`, `timer.sleep` |
| `std::tls` | `Certificate.load`, `tls.client`, `tls.dial`, `tls.listen`, `tls.server`, `tls.wrap_listener` |
| `std::udp` | `udp.bind`, `udp.bind_unix`, `udp.dial`, `udp.dial_unix`, `udp.inherited_sockets`, `udp.serve` |
| `std::uuid` | `Uuid.v4`, `Uuid.v7` |
| `std::wasm` | `wasm.instantiate` |
| `std::websocket` | `websocket.connect` |

`dbg` is an effect too, but no list needs an entry for it, since `nomi
build` refuses a program that holds one. A boot that takes a `Startup`
is `std::startup`.

### Which list holds a call

A call is charged to the file that holds it, and each list holds only the
calls of its own files. Suppose `pricing` calls a helper in `net::fetch`
that sends a request. The request is in `net::fetch`'s file, so
`net::fetch`'s entry must allow `std::http`, and `"pricing" = []`
holds. A callback the program passes to a dependency is the program's
code, so the dependency is not charged when it calls it.

A `[dependencies]` entry covers the package it names and every package
that package's `[dependencies]` name, at any depth. A package the project
reaches through several entries may have what any of them allows.

### A dependency's own list

A package can declare the effects of its own code in an `[effects.self]`
table in its own `nomi.toml`, which `nomi effects --write` run in the
package writes. A project that holds its dependencies holds each one to
what it declared too, as well as to the project's own lists.

```toml
# the nomi.toml of pg
[package]
name = "pg"

[effects.self]
effects = ["std::fs"]
```

### Authority by handle

A list names a whole std module, but a handle carries narrower authority.
A std::fs `Dir` is one directory. `fs.open_dir` is the call a list names
`std::fs`, and each call through the `Dir` reaches only what is under it
and is named by no list. So the program opens the directories, and a
dependency handed one needs no `std::fs` in its entry:

```nomi
// main.nomi, whose [effects.self] allows std::fs
fn main() -> Result<Unit, io.Error>
    data = fs.open_dir("data")?
    defer Dir.close(data)
    reports.write_daily(data)
end
```

```toml
[dependencies]
# writes its files through the Dir it is handed, and opens none itself
reports = { repo = "github.com/owner/reports", version = "v0.0.1", effects = [] }
```

In `reports`, `write_daily(dir: Dir)` writes with `Dir.write_text(dir,
"daily.csv", text)`, which the empty entry allows. A call there to
`fs.write_text` or `fs.open_dir` is refused at the call. The SQLite
driver's `sqlite.open_in(dir, name)` works this way. A `Dir` from
`std::fs::testing.dir` holds files in memory, for the dependency's
tests.

The same holds for any handle: a dependency's use of a handle the
program opened and passed to it is not counted, std's interface values
among them.

### When it is checked

`nomi check` holds `main`, the boot, the exported functions and the
project's public functions to each list's `effects`, and the test cases
to `effects` with `effects_in_tests` added. `nomi effects`, the editor
and `nomi mcp` do the same. The other commands hold what they run:

| Command | Holds |
|---|---|
| `nomi run` | `main`, the boot and the exported functions |
| `nomi test` | the test cases it runs |
| `nomi build` | what the build keeps |

The check counts every call the program can reach, whether or not a run
takes it: a branch that never runs, a function used as a value, every
implementation of an interface it calls (spec §19 *Effects*). To pass,
allow the name, or move the call out of the code the program reaches.

Each command prints the first call of each name a list refuses, with how
it is reached and the key to add the name to. `--all-calls` on `check`,
`run`, `test` and `build` prints every call, and so does `--format json`.

### `nomi effects`

`nomi effects [path]` prints the effects of the programs `nomi check
path` checks, whether or not `nomi.toml` has lists, as the tightest
lists the check passes:

```console
self: github.com/nomi-language/nomi/lib/sql/examples/shop
  effects            std::io
  effects_in_tests   std::instant

dependencies
  effects            std::channels, std::fs, std::instant, std::random, std::timer,
                     std::wasm
  effects_in_tests   (none)
  entries
    migrate
      no effects
    sqlite
      std::channels, std::fs, std::instant, std::random, std::timer, std::wasm
    sqlkit
      std::channels, std::fs (tests only)

nomi.toml has no [effects.self] or [effects.dependencies] table; nomi effects --write writes these
```

When `nomi.toml` has lists, the report ends with how they differ: a
`missing` line for each effect the program has that a list lacks, and an
`unused` line for each name nothing needs.

| Flag | What it does |
|---|---|
| `--write` | Write the tightest lists into `nomi.toml`. |
| `--check` | Exit 1 when the lists differ from the tightest ones, for CI. |
| `--why <name>` | List the calls that have an effect. |
| `--tree` | Print the calls from each root down to each effect. |
| `--tests` | With `--tree`, print each test's tree. |
| `--format json` | Print the report as one JSON object ([Output for tools](#nomi-effects---format-json)). |
| `--format mermaid` | With `--tree`, print the tree as a Mermaid flowchart. |

**`--write`** writes `[effects.self]`, `[effects.dependencies]` and an
`effects` key on each `[dependencies]` entry the program loads files
through. It writes entries under `[effects.self.modules]` only when the
file has that table or sets `entries_required`. It keeps the file's
comments and layout, and prints each name it added and removed.

**`--check`** fails on a name a list lacks, as `nomi check` does, and
also on a name nothing needs, so a list cannot grow without the change
that needs it. A table or key the file leaves out is not a difference.

```console
nomi.toml's [effects] tables differ:
  unused   effects.self.effects  std::net
nomi check passes; an unused name allows an effect nothing here has
```

**`--why <name>`** prints each call with that effect and how it is
reached. The name may also be a dependency (`--why pg`) or a module of
the project (`--why pricing`), for the calls with an effect that its
files make.

```console
$ nomi effects --why std::http
1 call has the effect std::http:
  rates.nomi:8:10 in fetch
    reached from `main`; `main` calls `today`
```

**`--tree`** prints, for `main` and each other root, the calls down to
each effect, and only the branches that lead to one. A function reached
twice is expanded once and then marked `(above)`, and each effect names
the list that refuses it.

```console
main
├─ io.print                                     std::io
├─ load_config → read_settings → fs.read_text   std::fs
├─ reload → read_settings (above)
├─ read_settings (as a value) (above)
├─ load (pricing) → fs.read_text                std::fs  refused: the entry for pricing
└─ 2 functions with no effects
main.nomi: 2 tests, 1 of which has the effect std::fs
--tests prints the tree of each test
```

## Packages and dependencies

A package names the packages it uses in its `nomi.toml`'s
`[dependencies]` table ([`nomi.toml`](#nomitoml)). `nomi` fetches each one
from its repository's host over HTTPS and records its version, commit and
hash in `nomi.lock` ([`nomi.lock`](#nomilock)). This page covers the
commands that change dependencies, how a build chooses versions,
workspaces, private repositories and publishing.

### Add, update and remove dependencies

```console
$ nomi add github.com/owner/kit/table
nomi: fetching github.com/owner/kit v0.3.0
added table, github.com/owner/kit/table v0.3.0 (import table)

packages
  table
    not imported yet
```

That wrote `table = { repo = "github.com/owner/kit", subdir = "table",
version = "v0.3.0" }` into `[dependencies]` and the package's line into
`nomi.lock`. A file of the project can then `import table::<file>`.

The commands work on the project of the nearest `nomi.toml` at or above
the current directory. They write its `[dependencies]` and `nomi.lock`,
and keep the file's comments and layout.

| Command | What it does |
|---|---|
| `nomi add [<name>=]<repository>[/<subdir>][@<version>]` | Add a published package at a release, the latest when none is named. |
| `nomi add [<name>=]<repository>[/<subdir>]@<commit>` | Add a package at a commit, all 40 hex digits of it. |
| `nomi add [<name>=]<directory>` | Add the package in a directory (`./`, `../` or `/`) as a `dir` entry. |
| `nomi remove <name>` | Take a dependency out, once no file imports it. |
| `nomi update [name]` | Move one dependency, or all, to the latest release of its line. |
| `nomi fetch [--update] [dir]` | Fetch what the project needs and write `nomi.lock`. |
| `nomi outdated [dir]` | List each repository with a later release. |

Each command starts from the versions `nomi.lock` holds and changes only
what it must:

| Command | What happens to the locked versions |
|---|---|
| `nomi add`, `nomi remove`, `nomi fetch` | They stay, even above what the requirements ask for. |
| `nomi add <repository>@<version>` | That repository takes the version named, even below the lock's. |
| `nomi update [name]` | The entry named, or every entry and every repository only a dependency names, rises within its line. |
| `nomi fetch --update` | Every version is selected from the manifests again, as with no lock. |
| `run`, `test`, `check`, `build` | They read the lock and never write it. |

#### `nomi add`

`nomi add <repository>` names a published package in `[dependencies]`,
fetches it and what it needs, and writes `nomi.lock`.

- **The repository** is a host and a path, with no scheme and no `.git`:
  `github.com/owner/name`. A path past the repository names the package
  in that directory, which the entry writes as `subdir`.
- **The version** is the repository's latest release when none is given,
  or the tag named, such as `@v1.2.3` or `@v1.3.0-rc.1`.
- **The name** is `<name>` when given, else the package's own
  `[package].name`.

One tag versions a whole repository ([Which versions a build
takes](#which-versions-a-build-takes)). So a package of a repository the
project already takes is added at that version, and a version named
moves the project's other entries of that repository with it. A
first-party library is taken at the version of the running `nomi`
([First-party libraries](#first-party-libraries)).

#### `nomi remove`

`nomi remove <name>` takes the entry out of `[dependencies]`, and writes
`nomi.lock` without it and without the packages only it needed. It
refuses while a file imports the dependency:

```console
$ nomi remove greet
nomi remove: main.nomi:2 imports greet; remove the import first
```

#### `nomi update`

`nomi update [name]` moves one entry, or every entry with a version, to
its repository's highest release in the same line:

| From | It takes | Not |
|---|---|---|
| `v1.0.0` | the highest `v1.x.y` | `v2.0.0` |
| `v0.1.0` | the highest `v0.1.x` | `v0.2.0` |
| `v0.0.3` | nothing: each `v0.0.x` release is a line of its own | `v0.0.4` |

```console
$ nomi update
nomi: fetching github.com/owner/greet v0.2.3
nomi: fetching github.com/owner/strutil v1.1.0
github.com/owner/greet v0.2.0 -> v0.2.3
github.com/owner/strutil v1.0.0 -> v1.1.0 (only a dependency names it)

packages
  greet
    no effects
    no change
```

It never takes a prerelease, and never crosses a line. To cross one, run
`nomi add <repository>@<version>`. `nomi update` names the release past
the line when there is one.

#### `nomi fetch` and `nomi outdated`

`nomi fetch [dir]` fetches the packages the project needs and writes
`nomi.lock`, so a later command with `--offline` needs no network.
`nomi fetch --update` selects every version again from the manifests.

`nomi outdated [dir]` lists each repository of the build that has a later
release than the one it is taken at, and changes nothing:

```console
$ nomi outdated
repository                version  latest in its line  latest  reached
github.com/owner/greet    v0.2.3   v0.2.3              v0.3.0  direct
github.com/owner/strutil  v1.1.0   v1.1.0              v2.0.0  through greet
```

#### `--offline`

`--offline` on `run`, `test`, `check`, `build`, `effects`, `types`,
`add` and `fetch` fetches nothing, and names any package the cache lacks.

#### The effects of an added dependency

`nomi add` and `nomi update` print the effects of each entry they added
or moved, as `nomi effects` prints them ([`nomi effects`](#nomi-effects)),
and for an update what changed from the old version:

```console
$ nomi add github.com/owner/files
nomi: fetching github.com/owner/files v1.2.0
added files, github.com/owner/files v1.2.0 (import files)

packages
  files
    std::fs

wrote effects = ["std::fs"] on files under [dependencies] in nomi.toml
nomi.toml's [effects] tables allow every effect the program has
```

When the project holds its dependencies and `[effects.dependencies]` sets
`entries_required = true`, `nomi add` writes the new entry's `effects`
key, as above. Otherwise it prints the key `nomi effects --write` would
write.

### Dependencies

A `[dependencies]` entry names a package by `repo` and `version`, or
`repo` and `commit`, with `subdir` for a package that is not at the
repository's root; by `dir`; or by both:

```toml
[dependencies]
markdown = { repo = "github.com/owner/markdown", version = "v0.3.0" }
sqlite = { repo = "github.com/nomi-language/nomi", subdir = "lib/sql/sqlite", version = "v0.0.1" }
helpers = { dir = "../helpers" }
kit = { repo = "example.com/kit", version = "v1.2.0", dir = "../kit" }
fix = { repo = "github.com/owner/fix", commit = "4f1c0b7e9d2a6c3b8e5f0a1d7c9b2e4f6a8d0c13" }
```

| Key | What it holds |
|---|---|
| `repo` | A host and a path, with no scheme and no `.git`. |
| `version` | A tag of the repository: `vX.Y.Z`, or a prerelease `vX.Y.Z-...`. |
| `commit` | In place of `version`, a commit, all 40 hex digits of it. |
| `subdir` | The package's directory in the repository. |
| `dir` | A directory relative to this `nomi.toml` that holds the package. |
| `effects`, `effects_in_tests` | The most effects the package may have ([The tables](#the-tables)). |

- **Names are the importer's.** The key is the name the package's own
  files import the dependency by (`import sqlite::driver`; spec §14
  *Modules and packages*). Each package imports through its own
  `[dependencies]`, so two dependencies with the same own name can both
  be used under different keys.
- **`dir` with `repo`** makes the directory stand for that package
  wherever another package requires it, as when you work on a dependency
  in a checkout beside the project. In a fetched package, a `dir` entry
  names a package of the same repository at the same tag.
- **Fetching.** `run`, `test`, `check` and `build` fetch what is missing
  into the package cache ([Caches](#caches-nomi-cache)), and check every
  package against the commit and hash `nomi.lock` records. The language
  server never fetches: it says to run `nomi fetch`.

### Which versions a build takes

A build takes each repository at one version: the highest any package of
the build asks for, and at least the one `nomi.lock` locks. Every package
of the repository is taken at that tag. Requirements that may break each
other are in different lines, and every requirement on a repository must
be in one line:

| Versions | Line | Example |
|---|---|---|
| `v1.0.0` and up | the major version | `v1`, `v2` |
| `v0.1.0` to `v1.0.0` | the minor version | `v0.1`, `v0.2` |
| below `v0.1.0` | the release itself | `v0.0.3` |

So `v0.1.0` and `v0.1.3` of one repository take `v0.1.3`, while `v0.1.0`
and `v0.2.0` are an error that names both requirements. A prerelease is in
the line of its release. Requirements on one repository are all at tags,
or all at one commit.

A repository the project reaches only through its dependencies rises only
when a dependency asks for more, or when `nomi update` raises it within
its line. To require a higher version of it, name it in `[dependencies]`
with no file importing it.

#### First-party libraries

The packages under `lib/` of `github.com/nomi-language/nomi` (the SQLite
driver, sqlkit, Markdown and others) share the compiler's version. A
release `nomi` takes them at its own version, whatever version an entry
names, and `nomi.lock` records it. After installing another release, run
`nomi fetch`. A `nomi` built from source takes the versions as written.

### `nomi.lock`

`nomi.lock` beside `nomi.toml` locks the build. It has one line per
fetched package the project needs: the repository, the package's
directory there, the version (or `commit`), the commit, the hash of its
files, and `direct` when the project names it.

```text
github.com/owner/greet . v0.2.0 b59dd9377cf60b87eaf6825df22d7e32875e2d23 sha256:c02f53f71111860cd71831c5b9871bdb5d96af575fb40c3733eadec79e03ff10 direct
```

Commit it. `nomi fetch`, `add`, `remove` and `update` write it, and every
other command reads it. A package it does not hold is an error that says
to run `nomi fetch`. A lock that disagrees with the requirements, as
after editing `nomi.toml` by hand, is an error until `nomi fetch --update`
selects again. Files whose hash or commit differs from their line are a
`SECURITY ERROR`, and nothing is installed. A package loaded from a `dir`
has no line.

### Workspaces

A workspace is several packages developed together with one `nomi.lock`.
Its root holds a `nomi-workspace.toml`, which names its members:

```toml
# nomi-workspace.toml
members = ["billing", "shop", "lib/*"]
exclude = ["vendor/*"]
```

```tree
store/
├── nomi-workspace.toml  # the members
├── nomi.lock            # the versions, commits and hashes every member takes
├── billing/
│   ├── nomi.toml
│   └── main.nomi
├── shop/
│   └── nomi.toml
├── lib/
│   ├── money/
│   │   └── nomi.toml
│   └── notes/           # no nomi.toml, so lib/* skips it
└── vendor/
    └── legacy/          # excluded: its own nomi.lock and versions
        ├── nomi.toml
        └── nomi.lock
```

- `members` lists the members' directories, relative to the file. `*`
  matches within one path segment, and skips a directory with no
  `nomi.toml`.
- `exclude` lists directories whose packages stand on their own, with
  their own `nomi.lock`, such as vendored code or a test fixture.
- A package under the root that neither list names is an error at its
  `nomi.toml`, whose fix adds it to `members`.
- Every member takes each repository at one version, from the
  workspace's `nomi.lock`. A member holds no `nomi.lock` of its own.
- Each member is its own project: its imports, entry points, effects
  lists and tools are its own, and commands run in it work as in a
  package on its own.

At the root, with no path:

| Command | What it does |
|---|---|
| `nomi check`, `nomi test`, `nomi effects`, `nomi fmt` with a flag | Acts on every member, with one report. |
| `nomi fetch`, `nomi update`, `nomi outdated` | Acts on the whole workspace, here or in any member. |
| `nomi run`, `build`, `tool`, `types` | Lists the members, to name one. |
| `nomi add`, `nomi remove` | Lists the members. In a member, they edit its `nomi.toml` and the workspace's `nomi.lock`. |
| `nomi new`, `nomi init` | Under the root, add the new package to `members`. |

### The nomi a package needs

A package says which release of `nomi` it needs in `[package]`:

```toml
[package]
name = "kit"
nomi = "v0.0.2"
```

An older release stops before checking anything, with one error at that
line, for the project and for a dependency alike:

```console
$ nomi check main.nomi
error: package app (this project) needs nomi v0.0.2 or later, and this nomi is v0.0.1; install a newer nomi
 --> nomi.toml:3:1
  |
3 | nomi = "v0.0.2"
  | ^^^^^^^^^^^^^^^
```

The key is optional and has no upper bound. A `nomi` built from source
has no release and refuses no package. `nomi new` and `nomi init` write
it.

### Private packages

A private repository is read through `git`, which must be on `PATH`, as
is a repository on a host other than the five in
[Distribution](#distribution). `git` reads your own configuration, so
your credential helper and your `url.<base>.insteadOf` rules apply. When
`git` has no credentials for the host over HTTPS, `nomi` reads the
repository over SSH as `ssh://git@<repo>`, with your SSH key.

`nomi` never prompts. Load your key into the SSH agent, and connect to
the host once with `ssh` to record its key. `nomi` reads no token
variable and no `.netrc` of its own.

### Distribution

To publish a package, push a version tag to its repository: `v1.2.3`, or
a prerelease such as `v1.3.0-rc.1`, which `nomi add` takes only when it is
named. There is no `nomi publish` command and no registry. Others add the
package by its repository, and by its directory there when it is not at
the root.

- **One tag versions the whole repository.** Several packages may share
  a repository, each a directory with its own `nomi.toml`, and each is at
  the tag the repository is at. They name each other by `dir` entries.
- **Hosts.** `nomi` reads tags and archives with no `git` from
  `github.com`, `gitlab.com`, `codeberg.org`, `bitbucket.org` and
  `git.sr.ht`. A repository on any other host is read through `git`, so
  its users need `git` on `PATH`.
- **The first fetch of a version trusts the host.** There is no checksum
  database. `nomi.lock` catches a tag moved, or files changed, after the
  first fetch, but not a host that served the wrong files to it.
- **A version lives as long as its tag.** A repository or a tag deleted
  from its host cannot be fetched again by a fresh clone.
- **Only files and directories install.** A link in a package's own
  directory is an error.

A `nomi` is a release when it was installed from a release archive, with
`go install github.com/nomi-language/nomi/cmd/nomi@<tag>`, or built in a
clean checkout at a release tag. `nomi version` names its tag. Any other
build is a `nomi` built from source.

## Running tests

`nomi test` runs the `test` blocks of a program. What a test is, and what
it may write, is in the specification's Tests chapter (spec §17). This
page covers how `nomi test` finds, runs and reports tests.

```console
$ nomi test
ok a_test.nomi :: adds
FAIL a_test.nomi :: fails
  line 6: assertion failed
    assert False
test result: FAILED. 1 passed, 1 failed
```

The exit status is nonzero when a test file fails to load, a test fails,
or a test is blocked ([When code cannot run yet](#when-code-cannot-run-yet)).

### Finding and selecting tests

`nomi test [path]` takes a file or a directory, the current directory by
default. A file runs the tests in that `.nomi` file, whatever its name. A
directory runs every `*_test.nomi` file under it, and every other `.nomi`
file that declares tests.

| Flag | Runs |
|---|---|
| `--line N` | The test, attached `//!` test or `group` whose declaration starts on line `N`, in the file given. |
| `--run <regexp>` | The tests whose names the regular expression matches. |
| `--reaching <file>:<line>` | The tests whose code can run the function at that line. |
| `--all` | Every test, including unchanged ones ([Unchanged tests](#unchanged-tests)). |
| `--changed` | Skips unchanged tests that have effects too. |
| `--timeout <duration>` | Sets how long each test may run ([Timeouts](#timeouts)). |
| `--format json` | Reports as JSON Lines ([Output for tools](#nomi-test---format-json)). |
| `--watch` | Runs the tests again whenever a `.nomi` file under the path, or a file the tests import, changes. |

`--run` matches anywhere in the name the report prints after `<file> ::
`, in RE2 syntax:

| Test | Name |
|---|---|
| a test | `parses a date` |
| a test in a group | `dates / parses a date` |
| an attached test | `double //! test line 4` |
| a test in a group's `subtests` block | `todo api [sqlite] / creates a todo` |

So `--run '\[sqlite\]'` runs the sqlite row's tests, and `--run '^dates /
'` runs one group. `--run` and `--line` both narrow, and a selection that
matches no test is an error, not an empty pass.

### Unchanged tests

`nomi test` does not run a test again when nothing it can run has changed
since it last passed, and the test has no effects. It reports the test as
passed and unchanged:

```text
ok a_test.nomi :: adds (unchanged)
ok a_test.nomi :: rounds (9 cases, unchanged)
test result: ok. 4216 passed, 0 failed; 3 ran, 4213 unchanged
```

A test has no effects when nothing it can reach writes output (`dbg`
included), reads input, the clock, files, the network or the system's
randomness, makes a channel or a task, starts a process, or reads a
hash. Its result then depends on its code alone, so unchanged code cannot
pass one run and fail the next. A test with effects always runs, and so
does a test that last failed.

| To | Do |
|---|---|
| run every test, and keep what passes | `--all` |
| also skip an unchanged test that has effects | `--changed` |
| see why each test ran | `NOMI_DEBUG_ARTIFACT_CACHE=1` |

A run with no artifact cache (`NOMI_ARTIFACT_CACHE=off`, a fresh CI
machine, the browser) runs every test.

### Timeouts

Each test may run for 10 seconds of wall-clock time, counting its group's
`clock`, `boot` and `setup`, and under `Clock.Virtual` too. `--timeout <duration>`
changes the limit, written as `30s`, `2m` or `500ms`, and `--timeout 0`
removes it. A test still running at its limit is stopped and fails, with
where it was running:

```console
FAIL coins_test.nomi :: flips forever
  line 7: did not finish within 10s, so it was stopped
  it was running:
    coins_test.nomi:7 in flips
    coins_test.nomi:12 in flips forever
  help: `nomi test --timeout <duration>` sets how long a case may run (10s unless given), and `--timeout 0` removes the limit
```

A `subtests` table is one test, so its rows share the limit. A test under
the debugger has no limit. A test under `Clock.Virtual` that is waiting on
a socket or a process at its limit cannot be stopped: it fails, and no
later test of the run starts. A test whose every task waits for another of
its tasks fails at once (spec §16 *Deadlock*).

### Output

What a test writes while it runs (`io.print`, `io.write`, `io.inspect`
and `dbg`) is reported under the test, indented two spaces, for passing
tests too:

```console
ok a_test.nomi :: passes
  dbg line 4: 5 = 5
  printed
FAIL a_test.nomi :: fails
  dbg line 10: 6 = 6
  line 11: assertion failed
    assert False
```

Tests run one at a time, so outputs never interleave.

### Line diffs

When `assert a == b` fails on two Strings and one holds a line break, the
report shows a line diff. The left operand is the actual value and the
right the expected, so put the value under test on the left:

```console
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

### The test environment

**`NOMI_ENV`.** `nomi test` sets the environment variable `NOMI_ENV` to
`test` for the run, unless it is set already. Nothing else reads it: a
program the tests start reads it from `startup.env` as it reads any
variable. `nomi run` does not set it. A `boot` that a test group's `boot`
line calls gets the `Startup` that line builds, not the real environment,
so a test passes the variable itself: `boot boot(Startup{env:
{"NOMI_ENV" => "test"}})` ([App Fields, Defer &
Context](/app-fields/#settings-per-environment)).

**The working directory.** A test file's working directory is its package
root: the nearest directory at or above the file that holds a
`nomi.toml`, or the file's own directory when there is none. So a test
that reads its package's files by a relative path
(`fs.read_text("data/rates.csv")`) reads the same files wherever `nomi
test` runs.

## Formatting

`nomi fmt` gives every Nomi program one layout. It has no options. This
page covers the command and the layout rules it applies.

### Running `nomi fmt`

```console
$ nomi fmt -w .
```

`nomi fmt [paths]` formats each file, and each `.nomi` file under each
directory. With no flag it prints the result on standard output, and with
no path it formats standard input.

| Flag | What it does |
|---|---|
| `-w` | Writes the result to each file. |
| `-l` | Lists the files that would change, and exits 0. |
| `-check` | Lists the files that would change, and exits 1 when there are any. This is the check for a CI step. |
| `-d` | Prints a unified diff of each file that would change, and exits 0. |

Editors format on save through `nomi lsp`.

`nomi fmt` indents 4 spaces per level and breaks lines at 100 columns. It
keeps a few layout choices of the author's, which Writing Nomi lists
([Let `nomi fmt` own the layout](/writing/files-and-layout/#1-let-nomi-fmt-own-the-layout)),
and decides the rest from the syntax and the width. It never changes the
text of a string.

### Blank lines

- A statement that spans more than one line gets a blank line before it,
  unless it opens its block, and after it, unless it closes its block. A
  block's final expression counts as a statement.
- Whether a statement spans lines is decided at the block's indent, so a
  call that fits in a shallow block may break, and be set apart, in a
  deeper one.
- A `case`, a nested block, a triple-quoted string written across lines
  and a literal broken after its opening bracket always span lines.
- A stacked pipeline, or one broken because it does not fit, is set
  apart. One that fits on its line sits with its neighbors.
- A binding `else` is measured by its first line, so a run of one-line
  guards reads as one step. A binding `else` with arms is laid out as a
  `case`.
- One-line statements sit together, and a single blank line the author
  writes between them is kept.
- A blank line at the top or bottom of a block is removed, and a block's
  imports are followed by one.

### Strings and templates

A string is never split. A triple-quoted one written on one line stays on
one line when it fits, and is framed, its body on lines of its own, when
it does not.

A framed string's body and closing delimiter share one indent: the
indent of its line when the literal starts the line, and one level in
when it follows code on its line (after `=`, `:`, `=>`, `->`, `return` or
a declaration's header). A case arm's or a lambda's literal stays on the
`->` line.

```nomi
fn page(name: String, avatar: Option<String>) -> Html
    title = Html"""
        <h1>${name}</h1>
        """

    case avatar
        Some(url) -> Html"""
            ${title}
            <img src="${url}" alt="">
            """

        None ->
            title
    end
end
```

A `${...}` written on one line stays on one line. One whose expression
spans lines is laid out from the column of its `${`.

### Pipes

A pipe written on one line stays inline, and one written across lines
stays stacked, each `|>` aligned with the subject. An inline chain breaks
only when it overflows, and a stacked one is never joined. An overflowing
chain bound to a name moves under the binding, one indent in. A pipeline
with a `then` or `tap` stage is always stacked. The arms or branches of a
`|> case` or `|> if` stage sit one indent in from the line its `|>` is
on.

### Values bound to a name

A value bound to a name (a local binding, a destructuring, a `once`), a
struct field's value in a literal and a field's default in a declaration
stay on the name's line when they fit there.

A value the grammar closes with `end` (an `if`, a `case`, a lambda, a
`begin` or `concurrent` block) that does not fit moves to the lines
below, one indent in, so its keyword and its `end` share a column:

```nomi
misses =
    if String.contains(game.word, letter) then
        game.misses
    else
        game.misses + 1
    end
```

- A value closed by a bracket (a struct literal, a call, a list or a map)
  stays on the name's line and breaks inside its brackets. So does a
  lambda whose body is a struct or triple-quoted literal.
- An `if` or a lambda that does not fit on the name's line but fits on
  one line below it is written there on one line.
- A binding `else` keeps its value on the binding's line, and so do a
  named argument, a map entry, a parameter's default, `return` and
  `assert`.

### Bodies, `if` and lambdas

A function's, a declaration's and a test's body always goes on the lines
below its header, one indent in, closed by `end` at the header's indent.
An empty one is `end` on the header's line.

- A guard (`if n < 0 then return 0`) and a binding `else` stay on one
  line whenever they fit.
- A two-branch `if` is written inline (`if c then a else b`) up to 50
  columns, or up to the line's width when its `else` is one atom such as
  `None`, `0`, `""` or `[]`.
- An `else if` chain is always in block form. So is a binding `else`
  whose statement holds an `if` or a `case`.
- An inline `if` that something follows on its line goes in parentheses.
  An `if` stage that another stage follows is written in block form.
- A single-expression lambda whose body is an `if` holding `break`,
  `continue` or `return` goes below the header, closed by `end`, as does a
  lambda body that does not fit. A lambda body written below its `->`
  stays there.

### `case` arms

When every arm fits on one line as `pattern -> body` and no body is a
block, the arms sit on consecutive lines. Otherwise every arm breaks,
with one blank line between every two arms, and each body goes on the
lines below the `->`, one indent in (a triple-quoted body keeps its `"""`
on the pattern's line). So the arms of one `case` always share one
layout. A comment stays above its arm, and a long `or` pattern breaks
after each `or`.

### Alignment

Consecutive one-line items of one construct line up:

- a `case`'s arms, at their `->`;
- a struct declaration's fields, their types after `name:`;
- a multi-line struct literal's fields, a multi-line call's named
  arguments and a multi-line parameter list's parameters, after `name:`;
- a multi-line map literal's entries, at `=>`;
- rows: a multi-line list or argument list whose items are all tuples of
  one length, or all calls of one callee with one argument count, each
  column after its comma, as a `subtests` table is written;
- comments that end consecutive lines of code at one indent;
- consecutive `once` declarations, at their `=`.

```nomi
struct Monster
    name:       String
    hit_points: Int = 10
end

fn label(n: Int) -> String
    case n
        0   -> "none"
        100 -> "a hundred"
        _   -> "some"
    end
end
```

A blank line, a comment line or an item over several lines ends a run.
In a run, an item much wider or narrower than the ones before it starts a
new section that aligns on its own. A run that padding would take past
100 columns stays unaligned. Local bindings' `=`, enum variants'
payloads, one-line literals and pipelines are not aligned.

### Declarations and imports

- Declarations with bodies get a blank line between them.
- Bodyless declarations of one kind (`once`, `type`, `typealias`, `host
  type`, `host fn`, a run of `derive`s for one type) may sit together.
  Different kinds are separated by a blank line, and a bodyless
  declaration with a doc comment stands apart. The same holds inside
  `impl` and `interface` blocks.
- A test group's `clock`, `boot` and `setup` move to the top of the group,
  in that order.

A file's or block's imports are combined into one `import` block, one
entry per line, sorted with `std::` paths first. A lone import stays
bare. Separate `import` lines of one path merge into a selector list
(`import std::calendar.{Date, Months}`). A comment line between two
imports keeps them apart. An import inside a block moves to the top of
that block.

### Other rules

- **Tail `return`.** A `return` that is the last thing a function,
  lambda or test body does is removed, in each branch of a tail `if` and
  each arm of a tail `case` too. A tail `if` or `case` in a test body, and
  a tail `if` with no final `else`, keep theirs.
- **Trailing commas.** A multi-line call has a trailing comma after its
  last argument when it has more than one argument, and none when it has
  one.
- **Struct literals and anonymous struct types** stay on one line when
  short, and break one field per line, each with a comma, when long.
- **Literals.** A literal written with a line break after its opening
  bracket stays broken; delete that line break to join it again. A list
  of number literals is filled, as many elements to a line as fit. When
  literals nest three deep, the outermost breaks. A list that holds a
  comment is written one item per line.
- **Operators.** A long comparison or `and`/`or` chain breaks before each
  operator, the right operand one indent in. A `?` or `!` sits against
  its expression.
- **Comments.** A trailing comment is never moved to make room, so it may
  run past 100 columns.

## Building

`nomi build` turns a program into something that runs without the
compiler: an executable, a WebAssembly module, a folder for a browser, or
a compiled program for a `nomi-vm` to run. This page covers each kind of
build and what a build carries with it. A program a host calls into is
covered under [Embedding](#embedding).

### Executables

```console
$ nomi build app.nomi
$ ./app
```

`nomi build <file> [-o <out>] [--target <os>/<arch>]` writes one
executable: the `nomi-vm` binary with the compiled program appended to
it. It runs `main` exactly as `nomi run` does, with the same arguments,
input, output and exit status, but starts in a few milliseconds since it
skips the compiler. It runs on the same VM at `nomi run` speed, and is
not native code.

The executable goes in the working directory, named after the file
without `.nomi`, unless `-o` names it. `nomi build <dir>` builds the one
entry point of a package's directory and names the executable after the
directory. `nomi build` with no file or directory builds the current
directory's.

`nomi build` refuses what `nomi run` would refuse, and also:

- a program with a `todo` or a `dbg` in any of its files, whether or not
  a run would reach it (spec §11 *Unwritten code: `todo`*, spec §8
  *`dbg`*);
- a call its target cannot make ([Effect classes](#effect-classes));
- a test file.

**Targets.** `--target <os>/<arch>` picks the platform, such as
`linux/amd64`, `windows/amd64`, `js/wasm` or `wasip1/wasm`. Without it
the executable is for the platform `nomi` runs on. A built executable
needs nothing else installed.

#### Where `nomi-vm` comes from

A `nomi` from a release archive carries `nomi-vm` for its own platform.
For another target, `nomi build` downloads that platform's runners
archive from the same release, checks it against the release's checksum
file, and caches it ([Caches](#caches-nomi-cache)); it prints the URL it
downloads. A `nomi` built from source builds `nomi-vm` from its checkout
with `go build`.

`nomi-vm` comes in a full build and a lean one without the network and
WebAssembly modules, which a program that reaches neither gets. A program
that imports `std::compiler` needs `nomi-vm-compiler`, which a release
ships in an archive of its own per target,
`nomi_<version>_<os>_<arch>_compiler.tar.gz`, that `nomi build`
downloads.

#### WebAssembly targets

A WebAssembly target writes `<name>.wasm`, with the program inside the
module.

- **`wasip1/wasm`** needs no file access to start: `wasmtime run
  hello.wasm` runs it, and so does a browser's WASI shim with no file
  system. The compiled program, embedded files included, may be at most
  4 MiB. A program with an `export fn` is written as a WASI reactor
  instead ([Embedding](#from-a-wasi-host)).
- **`js/wasm`** is written with `wasm_exec.js`, its support file, and
  `nomi-node.mjs`, which runs it under Node 22 or later. Its file system
  is in memory, and a call nests at most 1,000 deep, so a command-line
  program that reads real files belongs on `wasip1/wasm`.

```console
$ node nomi-node.mjs hello.wasm [args...]
```

### Effect classes

A std host function that acts outside the program in a way some target
cannot has an effect class:

| Class | Calls |
|---|---|
| `files` | std::fs's |
| `processes` | std::process's |
| `network` | std::http's requests |
| `connect` | std::net's, std::tls's and std::udp's dials (a Unix socket's included) and std::net's name lookups |
| `listen` | a server, a listener or a bound datagram socket |

| Target | Has |
|---|---|
| your platform's executables | every class |
| `js/wasm` | `files` (in memory) and `network` (through the browser's `fetch`) |
| `wasip1/wasm` | `files` |

`nomi build` refuses a program whose `main`, boot or exported functions
can reach a call of a class its target lacks, and lists each such call.
The walk counts a call in a branch the program never takes, so
`--allow-unsupported <class>` (repeatable) builds anyway, and such a call
returns `Err(io.Error.Unsupported{operation:, target:})` when it runs.

std::signals has no class: where no signal arrives (Windows, a browser,
WASI), `signals.receiver` answers `Err(Unsupported)`. A class is about
what a target can do. What a project lets its program do, on any target,
is an [effects list](#effects-lists).

### Embedded files

A program's `embed` declarations (spec §18 *Embedded files*) are read
when the program is compiled, so every build carries their files and
reads no file but its own when it runs. Patterns name files from the
package root, whatever directory the declaring file is in. Only the
declarations a program reaches go into its build.

Under `nomi run` and `nomi test`, an embedded file is read from disk when
the program reads it, and one that changed after the program was
compiled stops the program with an error naming the file. A pattern whose
folder does not exist yet, such as a frontend's `web/dist` before its
first build, is a warning and reads as an empty directory, but `nomi
build` refuses it.

### Compiled programs and `nomi-vm`

`nomi build <file> --no-vm [-o <out>]` writes the compiled program alone,
`<name>.nomic`. The same file runs on every platform's `nomi-vm`, in a
reactor ([Loading a program into a reactor](#loading-a-program-into-a-reactor)),
and under `nomi run`:

```console
$ nomi-vm app.nomic [args...]
$ cat app.nomic input.txt | nomi-vm   # the .nomic file framed on stdin
$ nomi run app.nomic [args...]
```

On standard input, `nomi-vm` reads one `.nomic` file and leaves the rest
as the program's own input, which suits a host with no file system. A
`nomi-vm` refuses a program written in another program format, and says
which `nomi` wrote it.

#### `nomi build-vm`

`nomi build-vm` writes a `nomi-vm` with no program in it, for running
`.nomic` files:

| Command | Writes |
|---|---|
| `nomi build-vm [--target <os>/<arch>] [-o <out>]` | `nomi-vm`, with `.exe` or `.wasm` as the target needs, and for `js/wasm` its two support files beside it. |
| `nomi build-vm --reactor [-o <out>]` | `nomi-vm-reactor.wasm`, the WASI reactor, which takes its program through `nomi_load`, with `nomi-wasi.js` and `nomi-wasi.d.ts` beside it ([`nomi-wasi.js`](#nomi-wasijs)). |

`--no-network` writes the lean build, and `--compiler` the one that runs
a program importing `std::compiler`.

### A program in the browser

`nomi build <file> --web [-o <dir>] [--static <dir>]` writes a folder
that any static file host can serve, which runs the program's `main` in a
browser. The default folder is `<name>-web`:

| File | What it is |
|---|---|
| `app.wasm` | The `js/wasm` module, with its memory capped at 1 GiB. |
| `nomi.js` | An ES module: the loader, with its `run` and `load` functions. |
| `nomi.d.ts` | TypeScript declarations for `nomi.js` ([From a page](#from-a-page)). |
| `index.html` | A page that runs `main`, with an output pane and an input line. |
| `locales/<tag>.nomil` | For a program that lists its locales, one file per listed locale that `[i18n].web_embedded` leaves out ([Locales in the browser](#locales-in-the-browser)). |

The page needs a server: `python3 -m http.server -d hello-web` serves one
locally. Serve `app.wasm` as `Content-Type: application/wasm`, compressed
with brotli or gzip (a hello world is 11 MB, 2 MB with brotli), as
Cloudflare Pages, Netlify and Vercel do. The page has no inline script,
so it works under a strict `script-src`.

**Static files.** Every file under the `web/` folder beside the
project's `nomi.toml` (or beside the file, outside a project) is copied to
the same path in the output, except names that start with a dot.
`--static <dir>` names another folder. A static file wins over a
generated one, so `web/index.html` replaces the generated page; such a
page loads `nomi.js` and calls `run` itself.

#### Running it from a page

A page runs the program with `run`:

```js
import { run } from "./nomi.js";

const { exitCode, fatal } = await run("./app.wasm", {
  args: ["--verbose"],     // startup.args; default []
  env: { NAME: "x" },      // startup.env; default {}
  stdout: (text) => {},    // default: console.log, one call per line
  stderr: (text) => {},    // default: console.error, one call per line
  // or an async () => string | null; default: end of input
  stdin: "all of it\n",
  timeoutMs: 10_000,       // default: none
  signal: controller.signal,
  // where Locale.load fetches <tag>.nomil; default beside app.wasm
  locales: "./locales/",
  // the program's files, string or Uint8Array; default none
  files: { "data/prices.csv": csvText },
});
// files: every file in the program's file system when it ended, by
// path, as Uint8Array
```

- Each run gets a fresh instance in its own worker, so the page stays
  responsive, and `timeoutMs` and `signal` stop it with exit code 124 or
  130. `worker: false` runs on the page's own thread instead.
- Standard input is a string, served and then ended; an async function,
  asked for one line at a time, with `null` ending the input; or absent.
- The file system is in memory ([std::fs](/reference/fs/)), with the
  working directory `/work`.
- The exit code is `nomi run`'s. When the runtime itself stops, as on an
  allocation past 1 GiB, the run resolves with exit code 2 and the
  runtime's message as `fatal`.

The clock, `std::random`, tasks, channels and `sleep` work. Tasks share
the worker's one thread, so a task that never yields starves the others.

#### Working on it: `nomi run --web`

`nomi run --web [--port N] [--open] [--static <dir>] <file>` serves the
program on `http://localhost:8000/` (or the next free port, or `--port`)
and rebuilds it as you edit. The page reloads after each successful
rebuild, and a change to a static file reloads it without one. A failed
build is shown over the page, which keeps running the last build that
succeeded. `--open` opens the page in the default browser, and Ctrl-C
stops the server.

### Frontend builds

A JavaScript frontend's static build is embedded and served with
`http.file_server_embedded` ([Frontends](/frontends/#a-javascript-frontend)).
Each bundler needs three settings to match:

- the folder it writes, which the `embed` names and `Files.sub` makes the
  served root;
- a dev proxy that forwards `/api` to the Nomi server, so the page calls
  the API on its own origin;
- the paths of the files whose names carry a hash of their contents,
  which `immutable:` lists.

| Bundler | Output folder | Dev proxy | `immutable:` |
|---|---|---|---|
| Vite | `dist/` | `server.proxy` in `vite.config.ts` | `["assets/**"]` |
| webpack | `dist/` | `devServer.proxy` | `["assets"]`, with output named `assets/[name].[contenthash].js` |
| Angular | `dist/<project>/browser/` | `proxy.conf.json` | `["*-*.js", "*-*.css"]`, the hashed files at its root |
| Create React App | `build/` | `"proxy"` in `package.json` | `["static/**"]` |
| SvelteKit, static adapter | `build/` | `server.proxy`, as Vite | `["_app/immutable/**"]` |
| Next, `output: 'export'` in `next.config.js` | `out/` | none | `["_next/static/**"]` |

`http.file_server_embedded` sends a `.br` or `.gz` copy that sits beside
a file to a browser that accepts it. In Vite, a compression plugin (`npm
install -D vite-plugin-compression2`) writes those copies. A React app's
`web/vite.config.ts`, with the proxy to a Nomi server on port 8080:

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { compression } from "vite-plugin-compression2";

export default defineConfig({
  plugins: [react(), compression()],
  server: {
    proxy: { "/api": "http://localhost:8080" },
  },
});
```

### A program's locale data

A program carries the CLDR data of the locales its `nomi.toml` lists
under `[i18n].locales` ([`[i18n]`](#i18n)), and nothing else. A program
with no `[i18n]` table, a script outside any project among them, carries
every locale.

- A locale the program does not list formats as its nearest listed
  parent or else as the source locale, so it is not an error at run time.
  A `Locale"…"` literal of one is a compile error.
  [`Locale.available`](/reference/locale/#localeavailable) has the lookup
  rules.
- A built program carries only the parts of the data it reaches.
- `std::calendar` carries the IANA time zone database, so zone lookups
  work on every host without the system's tzdata.

#### Locales in the browser

A site may offer many locales while each reader reads one, so `nomi build
--web` compiles into `app.wasm` only the locales `[i18n].web_embedded`
names (the source locale unless it says otherwise), and writes each other
listed locale to `locales/<tag>.nomil` beside it. A program shows a page
in such a locale only after [`Locale.load`](/reference/locale/#localeload)
has fetched its file. Anywhere but a browser build, `Locale.load` returns
at once.

## Embedding

A program with exported functions (spec §7 *Exported Functions*) is
called by a host: a web page, a WASI runner or a Go program. It needs no
`fn main`. `nomi build --web` and `nomi build --target wasip1/wasm` build
it, and `nomi check` and `nomi test` treat it as any program, its
exported functions included. This page covers each kind of host.

### From a page

`nomi build --web` records each exported function, and `nomi.js` loads
the program once and calls the functions any number of times:

```js
import { load } from "./nomi.js";

const nomi = await load("./app.wasm");
let game = await nomi.call("new_game", "pipeline");
game = await nomi.call("guess", game, "e");
```

- `load(url, options)` starts the program in its own worker and runs its
  `main` first when it has one. It takes `stdout` and `stderr` as `run`
  does ([Running it from a page](#running-it-from-a-page)).
- `call(name, ...args)` encodes the arguments as JSON, runs the function,
  and resolves with its decoded result. Calls run one at a time, on the
  one program, so a `once` value an earlier call forced is not computed
  again.
- A call that traps, or whose argument does not decode, rejects with an
  `Error` whose message is the Nomi diagnostic, and the next call runs
  normally.
- `exports()` resolves with each exported function's name and signature,
  and `close()` stops the worker.

`nomi.d.ts` declares a typed `call` overload for each exported function
and each type the functions name, mapped as [`nomi
types`](#typescript-types-nomi-types) maps it, except that an `Int` is
`number | bigint`: an Int past `Number.MAX_SAFE_INTEGER` arrives as a
BigInt.

### From a WASI host

`nomi build --target wasip1/wasm` of a program with at least one `export
fn` writes a WASI reactor, which carries its program in its own memory
and needs no file access, environment or argv. It exports `_initialize`,
which the runner calls once, its `memory`, and a call interface over that
memory that is the same for every program: a program's own function names
travel as data. `nomi build` writes `nomi-wasi.js` beside it
([`nomi-wasi.js`](#nomi-wasijs)), which does all of this for a JavaScript
host.

| Export | Signature | Does |
|---|---|---|
| `nomi_alloc` | `(len: i32) -> i32` | Reserves `len` bytes and returns a pointer the runner writes into. |
| `nomi_call` | `(name_ptr, name_len, args_ptr, args_len: i32) -> i64` | Calls the exported function named by the UTF-8 name with the JSON array of arguments. Returns the result's pointer in the high 32 bits and its length in the low 32 bits. |
| `nomi_call_with` | `(name_ptr, name_len, args_ptr, args_len, opts_ptr, opts_len: i32) -> i64` | `nomi_call` with options ([Options](#options-effects-and-time-limits)). |
| `nomi_free` | `(ptr, len: i32)` | Releases a buffer from `nomi_alloc` or a result another export returned. |
| `nomi_main` | `() -> i32` | Runs `main` over WASI standard input, output and error, and returns the exit status `nomi run` would exit with. |
| `nomi_load` | `(ptr, len: i32) -> i64` | Boots the compiled program (`.nomic`) at `ptr` in a reactor that has none, or with `len` 0 the program built into the module. Returns as `nomi_call` does. |
| `nomi_load_with` | `(ptr, len, opts_ptr, opts_len: i32) -> i64` | `nomi_load` with options. |

The reactor imports one function besides WASI's, which the runner must
supply: `nomi.host_call(key_ptr, key_len, args_ptr, args_len: i32) ->
i64` ([Host functions](#host-functions-the-runner-answers)). A runner
with none to answer passes `{ nomi: { host_call: () => 0n } }`.

A result is UTF-8 JSON: `{"ok": value}`, with `null` for a `Unit`, or
`{"error": "diagnostic"}`. The module stays usable after an error. The
runner may free the name and arguments once `nomi_call` returns, and
frees the result with `nomi_free`:

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
const result = x.nomi_call(name, nameLen, args, argsLen);
const packed = BigInt.asUintN(64, result);
x.nomi_free(name, nameLen);
x.nomi_free(args, argsLen);
const ptr = Number(packed >> 32n), len = Number(packed & 0xffffffffn);
const bytes = new Uint8Array(x.memory.buffer, ptr, len);
const answer = JSON.parse(new TextDecoder().decode(bytes));
x.nomi_free(ptr, len);
```

The program boots on the first call, and every later call runs on that
booted program. `main` runs only when the runner calls `nomi_main`, at
any point. Calls run one at a time, and the tasks a call starts make
progress only while some export is running.

### Loading a program into a reactor

A runner that runs many scripts can ship the reactor once and each script
as a small `.nomic` file ([Compiled programs](#compiled-programs-and-nomi-vm)).
`nomi build-vm --reactor` writes the reactor with no program of its own.
The runner compiles it once, and for each script instantiates it, calls
`_initialize`, writes the whole `.nomic` file into a buffer from
`nomi_alloc`, and calls `nomi_load`.

`nomi_load` returns `{"ok": [...]}` with the program's exported
functions' signatures (`"add(a: Int, b: Int) -> Int"`), or `{"error":
"..."}` when the bytes are not a program this reactor runs or its boot
fails. A failed load leaves the reactor ready for another. An instance
runs one program for its life, so to run another, instantiate the module
again, which is cheap. `nomi_load` with length 0 boots the program built
into the module and returns its signatures, which tells a runner what it
can call.

### Options: effects and time limits

A runner that loads scripts it did not write passes options to
`nomi_load_with` and `nomi_call_with` as a JSON object in a buffer from
`nomi_alloc`, such as `{"std": ["std::io"], "timeout_ms": 500}`:

| Option | Export | What it holds |
|---|---|---|
| `"std"` | `nomi_load_with` | The effects the program may have, named as an [effects list](#effects-lists) names them. An empty array allows none. Without the key, std calls are not held. |
| `"host"` | `nomi_load_with` | The keys of the host functions linked into the runner that the program may call (`["lookup"]`). |
| `"host_calls"` | `nomi_load_with` | The keys of the program's own `host fn`s that the runner answers through `nomi.host_call`. |
| `"timeout_ms"` | both | How long the boot and each call may run, in milliseconds. On `nomi_call_with` it bounds that call alone. |

A load over its `"std"` or `"host"` list is refused before the program
runs anything, with each call at the file and line the program was built
from. A `"host_calls"` key is allowed only when `"host"` names it too, if
either list is given. A call past its time stops with the tasks it
started and returns an error, and the program stays loaded for the next
call.

### Host functions the runner answers

A program's own `host fn`s are answered by the runner, through
`nomi.host_call`, for the keys `"host_calls"` lists. A key is the one
spec §19 *`host` declarations and embedders* gives each `host fn`:
`greet` in the entry file, `helpers/util.greet` for `helpers/util.nomi`.
The reactor passes the key and the arguments as a JSON array. The runner
writes its answer, `{"ok": value}` or `{"error": "message"}`, into a
buffer it reserves with `nomi_alloc`, and returns where it is as
`nomi_call` does. An `"error"` fails the Nomi call as a trap with that
message.

```js
const hosts = { greet: (name) => `hello, ${name}` };
let x; // the instance's exports, once it is instantiated
const read = (ptr, len) => new TextDecoder().decode(new Uint8Array(x.memory.buffer, ptr >>> 0, len));
const nomi = {
  host_call(keyPtr, keyLen, argsPtr, argsLen) {
    let answer;
    try {
      const args = JSON.parse(read(argsPtr, argsLen));
      answer = { ok: hosts[read(keyPtr, keyLen)](...args) ?? null };
    } catch (e) {
      answer = { error: e.message };
    }
    const bytes = new TextEncoder().encode(JSON.stringify(answer));
    const ptr = x.nomi_alloc(bytes.length) >>> 0;
    new Uint8Array(x.memory.buffer, ptr, bytes.length).set(bytes);
    return BigInt(ptr) << 32n | BigInt(bytes.length);
  },
};
const instance = await WebAssembly.instantiate(module, { ...wasi.getImportObject(), nomi });
x = instance.exports;
```

A `host fn` whose parameters or result cannot cross as JSON, such as a
function value, can be answered only from Go. A host function runs to
its end inside the Nomi call, and `"timeout_ms"` cannot stop it partway.

### `nomi-wasi.js`

`nomi-wasi.js` is a JavaScript host for the reactor, an ES module with
TypeScript declarations in `nomi-wasi.d.ts`, that runs in browsers and in
Node with no WASI library:

- `loadRuntime(reactor)` compiles the module once;
- `runtime.load(nomic, options)` loads a program into a fresh instance
  and resolves to one whose `exports` are its signatures and whose
  `call(name, ...args)` resolves with the result;
- `runtime.run(nomic, {stdin:, args:, env:})` runs `main` and resolves
  with its exit status and output.

Both take `std`, `host` and `timeoutMs`, passed as the options above, and
`hosts`, a table from each `host fn`'s key to a function:
`runtime.load(nomic, {hosts: {greet: (name) => "hi, " + name}})`. Without
`nomic`, they use the program built into the module.

### From Go

A Go program loads Nomi with the `vmhost` package, and runs `.nomic`
files with `vmrunner`, which links no compiler. Either holds a program it
did not write to a list with `WithEffects`: the names an effects list
uses, plus `host:<key>` for a function the Go program's own host tables
answer. With no names the program may have no effects:

```go
p, err := vmhost.LoadSource("rule", src, vmhost.WithEffects())
rate, err := p.Call(ctx, "discount", vmhost.Fields{"items": 12, "loyal": true})
```

The load refuses a program over its list before anything runs. The list
does not confine the files the host gives the program, nor its time and
memory, which `WithStop` and the contexts passed to each call bound. The
[vmhost](../vmhost/doc.go) and [vmrunner](../vmrunner/doc.go) package
docs cover the Go API.

## Environment variables

These environment variables change how `nomi` and the programs it builds
behave.

| Variable | What it does |
|---|---|
| `NOMI_COLOR` | `always` colors diagnostics and test reports even into a pipe, and `never` turns color off. When it is unset, `NO_COLOR` or `TERM=dumb` turns color off, `CLICOLOR_FORCE` turns it on, and otherwise color goes only to a terminal. A program's `terminal.supports_color` follows the same rule. |
| `NOMI_DIAGNOSTICS` | `short` prints each diagnostic as one `path:line:col: message` line, for every command. |
| `NOMI_ALL_CALLS` | Any value prints every call the effects lists refuse, as `--all-calls` does ([When it is checked](#when-it-is-checked)). |
| `NOMI_DEBUG_LOWERING` | `1` adds the compiler's own reason to each "not supported yet" error, for a bug report ([When code cannot run yet](#when-code-cannot-run-yet)). |
| `NOMI_DEBUG_ARTIFACT_CACHE` | `1` says on standard error why a command read or kept no program, and why each test ran ([Caches](#caches-nomi-cache)). |
| `NOMI_ENV` | `nomi test` sets it to `test` unless it is set ([The test environment](#the-test-environment)). |
| `NOMI_PROFILE` | `1` profiles a run, of `nomi run` or of an executable `nomi build` wrote, as `--profile` does. A file name also writes the profile there ([`--profile`](#where-the-time-goes---profile)). |
| `NOMI_HASH_SEED` | Fixes the seed a run keys its hashes by, which it otherwise draws at random, to reproduce a run that printed a hash. Whoever knows a fixed seed can choose keys that collide. |
| `LISTEN_PID`, `LISTEN_FDS`, `LISTEN_FDNAMES` | systemd's socket activation: `net.inherited_listeners` and `udp.inherited_sockets` take the sockets passed. Not on Windows. |
| `NOMI_TOOL_PACKAGE` | `nomi tool` sets it to the name the project gives the tool's package ([Tools](#tools-nomi-tool)). |
| `NOMI_MAIN_PACKAGE` | `nomi tool` sets it to the project's directory, so the tool, and any `nomi` it starts, builds with the project's dependencies. |
| `NOMI_ARTIFACT_CACHE` | The artifact cache's directory, or `off` to keep nothing ([Caches](#caches-nomi-cache)). |
| `NOMI_PACKAGE_CACHE` | The package cache's directory. |
| `NOMI_RUNNER_CACHE_ROOT` | The runner cache's directory. |
| `NOMI_RUNNER_CACHE_MAX_ENTRIES` | How many runner directories that cache keeps, 256 unless set; zero or less keeps every one. |
| `NOMI_STD_CACHE_ROOT` | The directory, in place of `~/.cache/nomi`, under whose `std` the language server writes the standard library's source. |
| `NOMI_GO` | For a `nomi` built from source, the `go` command or Go root to build `nomi-vm` with ([Installing Nomi](install.md)). |
| `NOMI_COMPILER_SOURCE` | For a `nomi` built from source, a checkout of the compiler to build `nomi-vm` from ([Installing Nomi](install.md)). |

## Editors and debugger

Editors work with Nomi through three servers in `nomi`: the language
server `nomi lsp`, which reports what `nomi check` reports
([Checking](#checking-nomi-check)) and formats on save with `nomi fmt`
([Formatting](#formatting)); the debugger `nomi dap`; and `nomi mcp`, the
compiler as tools for a coding agent. `nomi editor install <editor>` sets
up an editor. Each editor's README has the rest:
[VS Code](../editors/vscode/README.md),
[Neovim](../editors/nvim/README.md) and
[Helix](../editors/helix/README.md). The Zed extension is
[`editors/zed`](../editors/zed/extension.toml).

### Setting up an editor: `nomi editor`

`nomi` carries each editor's Nomi support and writes it where the editor
reads it, so the editor's files match the compiler's version:

```console
$ nomi editor install helix
$ nomi editor install nvim
$ nomi editor install vscode
$ nomi editor install zed
```

| Editor | What `install` does |
|---|---|
| `helix` | Writes the queries, the grammar and Nomi's block of `languages.toml` into Helix's configuration directory, then builds the grammar with `hx --grammar build` and runs `hx --health nomi`. |
| `nvim` | Writes the plugin into `site/pack/nomi/start/nomi` in Neovim's data directory, and compiles the parser with `$CC`, else `cc`. |
| `vscode` | Installs the extension with `code --install-extension`. |
| `zed` | Writes the extension into `extensions/installed/nomi` in Zed's data directory. Restart Zed to load it. |

- `nomi editor` with no words lists each editor and whether its support
  is installed, from which version.
- `nomi editor uninstall <editor>` removes what `install` wrote, and
  `--dry-run` on either prints what it would do.
- `nomi editor install --installed` takes no editor. It installs for each
  editor that has Nomi support already, VS Code when `code
  --list-extensions` lists the extension.
- `nomi upgrade` rewrites the support after updating `nomi`, and `nomi
  --version` names each editor whose support is from another version.

The `nomi` in a release archive carries the VS Code and Zed extensions.
A `nomi` built from source carries neither, and `make install-vscode` and
`make install-zed` in a clone build them. An `extensions/installed/nomi`
that `nomi` did not write, such as a dev extension, is left alone unless
`install` is given `--force`.

### Updating: `nomi upgrade`

```console
$ nomi upgrade
```

`nomi upgrade` downloads the latest release for this platform, checks it
against the release's checksums, and renames it over the running `nomi`.
The new `nomi` then rewrites the support of each editor that `nomi editor
install` set up from another version; restart the editor afterwards.
`nomi upgrade v0.0.1` installs that release, an older one too. `--check`
prints this version, the latest release and the editors it would rewrite,
and changes nothing.

A `nomi` installed by Homebrew, or built from source, is not replaced:
`nomi upgrade` prints the command that updates it (`brew upgrade nomi`,
`git pull` and `go install`, or `go install ...@latest`) and rewrites the
editors' support from the running `nomi`. A dev extension, and any other
support `nomi` did not write, is left alone. VS Code's extension records no
version, so it is rewritten only when `nomi` was replaced.

**Settings an extension cannot make.** Neovim and Helix wrap comments at
`nomi fmt`'s 100 columns. A Zed extension cannot set a line length or
indent guides, so these go in Zed's `settings.json`:

- `"languages": { "Nomi": { "preferred_line_length": 100 } }` for
  "editor: rewrap";
- `"languages": { "Nomi": { "indent_guides": { "enabled": false } } }` to
  turn indent guides off;
- `"languages": { "TOML": { "language_servers": ["!nomi", "..."] } }` to
  stop Zed sending every TOML buffer to `nomi lsp`, which answers only a
  `nomi.toml` and a package's phrase catalogs.

### The language server

`nomi lsp` serves the Language Server Protocol on standard input and
output. Every editor starts it from the `nomi` on `PATH`. It gives:

- the diagnostics `nomi check` gives, as you type, and a warning for each
  `todo` and `dbg`;
- hover, go-to-definition, find references, completion and rename;
- quick fixes and refactors ([Quick fixes and refactors](#quick-fixes-and-refactors));
- formatting with `nomi fmt`;
- errors, completion and hover in `nomi.toml` ([`nomi.toml` in the
  editor](#nomitoml-in-the-editor)) and in phrase catalogs ([Phrase
  catalogs in the editor](#phrase-catalogs-in-the-editor));
- the effects of each call and function, in hover and as bold calls
  ([Bold calls](#bold-calls)).

Go-to-definition into std opens the module's file in its source tree, or,
in an installed `nomi`, a read-only copy of its standard library under
`~/.cache/nomi/std/<version>/` (`NOMI_STD_CACHE_ROOT` replaces
`~/.cache/nomi`).

#### `nomi.toml` in the editor

Each editor sends a package's `nomi.toml` to the server, and keeps its
own TOML highlighting for it. The server checks the program against the
text as you type, saved or not:

- the errors `nomi check` reports there, each at its line;
- completion of the std modules in every effects list, and of a whole
  entry, with the effects its code has, under `[effects.self.modules]`
  and on a `[dependencies]` entry;
- hover on a name in a list, which shows the calls it is there for, as
  `nomi effects --why` prints them;
- a hint on each name nothing needs, with a quick fix that removes it.

On a call `nomi.toml`'s lists do not allow ([Effects lists](#effects-lists)),
the quick fix adds the name to the key the error's help names, such as
"Allow std::http for this package in nomi.toml". The diagnostic clears
once the edit is in the editor, before it is saved.

#### Hover

On a keyword, hover shows the construct it starts, what it does there and
a link to the page that teaches it. It also states rules the text leaves
implicit, such as when an `if` without `else` runs, which collection a
`[` builds, and what a conversion to a wider type does at run time.

A call or a function with effects ends with them and whether the lists
allow them, through the functions on the way:

```text
Has effects: `std::fs` through `read_settings`, which `nomi.toml` allows in `[effects.self]`
```

A call that acts only through a handle it is passed ends with "Has
effects on what it is passed." A function that reads app fields also says
which ("Uses `App.limit`, `App.logger`"), as information only. These
lines cover every function of an open file's program, once its
background compilation has finished.

#### Bold calls

The server draws bold each call the compiler cannot show is pure, and the
name of each function whose body holds such a call, so the mark climbs
from the call through every caller up to `main`. A mark says the call can
act, not that it does: `report(buffer, 3)` with a writer into memory is
marked as `report(io.stdout, 3)` is. This is the rule `nomi test` uses to
decide that a test has no effects. The `dbg` keyword, assertions and
reads of a value are not marked.

Neovim and VS Code draw the marks with no setting, and Zed once semantic
tokens are on ([Phrase catalogs in the editor](#phrase-catalogs-in-the-editor)
shows the setting). Helix does not read semantic tokens. To style the
mark yourself, use the semantic token modifier `effect`: in VS Code,
`"editor.semanticTokenColorCustomizations": { "rules": { "*.effect:nomi":
{ "underline": true } } }`, and in Neovim, the highlight group
`@lsp.mod.effect.nomi`.

#### Quick fixes and refactors

Quick fixes include:

- append `()` to a pipe stage that names a function without calling it;
- insert `then` before a lambda written as a pipe stage;
- insert `|> Display.to_string()` before a stage that takes a `String`
  when the piped value implements `Display`;
- put `|> Iter.take(todo)` before a terminal stage over an infinite
  iterator;
- write a tuple read by number by its ordinal, `t.0` as `t.first`;
- name a struct literal's values written without field names, in
  declaration order;
- add `else None` to an `if` without `else` whose branch is an `Option`,
  or write `_ = ` before a branch's value where a statement drops it;
- write missing functions, fields and `case` arms, each filled with
  `todo`, so the result checks;
- rewrite source written as another language writes it (`let x = 1`,
  `xs.len()`, `match`, `nil`);
- write a type parameter used once as a parameter's type, under one bound,
  as an interface value (`fn label<T>(value: T) where T: Named` becomes
  `fn label(value: Named)`).

"Remove type annotation" and "Add type annotation" are offered on a field
or parameter with a default (`limit: Int = 10` and `limit = 10`).

### Coding agents: `nomi mcp`

`nomi mcp` serves the Model Context Protocol on standard input and
output, so a coding agent can ask the compiler what an editor asks it. To
add it to Claude Code:

```console
$ claude mcp add nomi -- nomi mcp
```

Other clients take the same command in their MCP settings, as a `stdio`
server whose command is `nomi` and whose argument is `mcp`. A relative
path in a tool's arguments is read from the client's working directory.

| Tool | Arguments | Returns |
|---|---|---|
| `check` | `path` | `nomi check --format json`'s records ([Output for tools](#nomi-check---format-json)) as lists, plus `ok`. |
| `type_at` | `file`, `line`, `col` | The hover text the language server shows there. |
| `definition` | `file`, `line`, `col` | The `locations` the name there is declared at. |
| `references` | `file`, `line`, `col` | Every use of the name there, its declaration included. |
| `search_std` | `query`, `limit` | The standard library's public functions that match, each with its signature and first doc paragraph. `limit` is 20 by default. |
| `run_tests` | `path`, `line`, `all`, `changed`, `unchanged` | `nomi test --format json`'s records ([Output for tools](#nomi-test---format-json)), with `all` and `changed` as `--all` and `--changed`, and the count of unchanged tests. |
| `tests_reaching` | `file`, `line`, `path`, `all`, `changed` | The tests whose code can run the function at that line, run as `nomi test --reaching` runs them. |
| `format` | `source` or `file` | The formatted text and whether it changed. It never writes the file. |
| `explain` | `code` | What a diagnostic's `code` means and how to fix it; with no code, the codes. |

A position is a 1-based line and a 1-based byte column. A `search_std`
query with `->` is a signature, such as `String, String -> List<String>`:
its parameters match in any order, and a single capital letter stands for
any type, so `List<T> -> Option<T>` finds `List.first`. Any other query
matches names and doc comments.

### Debugging

`nomi dap` is a Debug Adapter Protocol server that editors start: VS
Code's extension, Neovim through nvim-dap, Zed's extension and Helix's
`:debug-start` templates. It debugs a file's `main`, its tests, or the one
test `--line` would select, as `nomi run` and `nomi test` run them.

- **Breakpoints.** Line breakpoints, each with an optional condition, hit
  count (`5`, `>= 5`, `% 10`) or log message with `{expression}` parts.
  Function breakpoints take a name as the call stack shows it
  (`Account.deposit`, `Display.to_string for Card`) or as code calls it
  (`Card.to_string`).
- **Exception stops.** A trap, a failed assertion and an `Err` returned
  from `main` stop by default. An uncaught task failure can be turned on.
- **Stepping.** Step over, into and out, step into target, pause, and
  restart frame. Stepping passes through the standard library unless the
  launch sets `justMyCode` to false.
- **Step back and reverse continue.** Going back replays the program from
  its start, with the input, clock, randomness and files the session
  recorded. A program that starts a task or makes a channel cannot go
  back, and says why.
- **Inspecting.** The call stack shows a callback under the operation
  that called it, and each frame's locals appear by their Nomi names. A
  **Pipeline** scope shows each stage of the `|>` chain the frame is in.
- **Evaluating.** Watches, hovers and the debug console evaluate Nomi in
  the selected frame. An expression with an effect is refused unless it
  ends with `// allow effects`, and a hover never runs one.
- **Input and output.** With `console` set to `"integratedTerminal"` (the
  default for a program) the program reads and writes in the editor's
  terminal. With `"internalConsole"` (the default for tests) its output
  goes to the debug console and it reads end of input.

A launch configuration takes `mode` (`"run"` or `"test"`), `program`,
`line`, `args`, `cwd`, `env`, `console`, `justMyCode` and `stopOnEntry`.
The [VS Code README](../editors/vscode/README.md) has the table.

### Embedded files in the editor

Hover on an `embed` declaration's name, or on a use of it, shows its
patterns, how many files and bytes it holds, and the first files. Go to
definition on a pattern opens the file or folder it names. A pattern that
matches nothing is a diagnostic at the pattern.

### Phrase catalogs in the editor

The language server reports a catalog's errors in the catalog file, open
or not, and checks again when a catalog changes.

- **Navigation.** Go to definition on a call of a phrase lands on its
  declaration, and go to implementations lists its catalog entries.
- **Hover.** On a phrase it shows its text in the source locale and in
  each listed locale. Inside MessageFormat 2, a variant key shows its
  plural category in the text's locale with example numbers.
- **Completion.** In a catalog it completes phrase names, parameters
  after `$`, and a variant's keys.
- **Quick fixes.** They add a listed locale's missing translation, mark a
  stale translation current as `nomi phrases accept` does, and add the
  plural, enum or gender arms a translation lacks.

The server colors MessageFormat 2 in a `phrase` or `noun` body and in a
catalog with semantic tokens. VS Code and Neovim show them with no
setting. Zed shows them with `"languages": { "Nomi": { "semantic_tokens":
"combined" } }` in its settings, and for catalogs the same under
`"TOML"`. Helix does not read semantic tokens.

On a `phrase` whose text has no `.match`, the refactor "Add plural forms
for `$count`" rewrites the text to select on an `Int` parameter, with an
arm for each plural category of the source locale:

```nomi
phrase cart_items(count: Int) `
    .input {$count :integer}
    .match $count
    one {{{$count} items in your cart}}
    * {{{$count} items in your cart}}
    `
```

## Output for tools

`nomi check`, `nomi test` and `nomi effects` take `--format json` and
write records for a tool or a coding agent. Standard output then carries
only the records, and the exit status is the same as in text mode.
`nomi mcp` answers with the same records ([Coding
agents](#coding-agents-nomi-mcp)).

### `nomi check --format json`

One JSON object per line: a record per error, one per file checked, and
a summary last.

```json
{"type":"diagnostic","file":"/abs/main.nomi","line":5,"col":12,"end_line":5,"end_col":15,"start_byte":54,"end_byte":57,"severity":"error","message":"List<Int> has no field 'len'","hints":["Nomi has no method calls: write `List.length(xs)`"],"fix":{"title":"Call `List.length`","edits":[{"line":5,"col":9,"end_line":5,"end_col":9,"start_byte":51,"end_byte":51,"text":"List.length("},{"line":5,"col":11,"end_line":5,"end_col":16,"start_byte":53,"end_byte":58,"text":""}]}}
{"type":"file","file":"/abs/main.nomi","status":"failed"}
{"type":"summary","files":1,"failed":1,"diagnostics":1}
```

| Field | What it holds |
|---|---|
| `file` | The absolute path, or `std/<module>.nomi` for an error in the standard library. |
| `line`, `col`, `end_line`, `end_col` | The span. Lines and columns are 1-based, a column counts bytes, and the end is one past the last byte. A point has no end. |
| `start_byte`, `end_byte` | The same span as 0-based byte offsets. |
| `severity` | `error`; `warning`, which fails no file and is not counted in the summary; or `hint`, only under `--hints`. |
| `code` | The diagnostic's class, such as `unused-import`. `nomi mcp`'s `explain` tool says what each code means. |
| `args` | When present, what the message names that a tool acts on, in the order the code's explanation gives. |
| `message` | The error's text. |
| `hints` | The `help:` lines. |
| `related` | Other places the error names, each with its own `file`, span and `message`. |
| `fix` | When present, the rewrite the error knows: `edits` that replace each span's text with `text`, applied together. An edit in another file names it as its `file`. |

A `"type":"file"` record's `status` is `ok` or `failed`, and a file
checked through an importer names it as `through`.

**Refused effects.** A call the effects lists refuse
([Effects](#effects)) has the code `effect-not-allowed`, its `file` is
`nomi.toml` (where its fix edits), and its `related` lists each place in
the project's own code that leads to it. Its `args` are:

1. the name, such as `std::http`;
2. the list: `effects.self`, `effects.self.modules`,
   `effects.dependencies`, or `dependencies` for the entries the package
   is reached through;
3. whose files hold the call: the project's `[package].name`, the
   dependency's name, or the module's key;
4. each name the list allows.

So `"args":["std::http","effects.self","app","std::io","std::fs"]`. A
call over the `[effects.self]` a dependency declares itself has the code
`effect-not-declared`, with the same `args`.

### `nomi test --format json`

One record per test, and a summary last:

```json
{"type":"test","file":"/abs/path/a_test.nomi","line":14,"end_line":17,"name":"a_test.nomi :: fails","status":"failed","message":"  line 16: assertion failed\n    assert x == 4","error_line":16,"output":"dbg line 15: x = 3\n","report":"FAIL a_test.nomi :: fails\n  dbg line 15: x = 3\n  line 16: assertion failed\n    assert x == 4\n"}
{"type":"file","file":"/abs/path/b_test.nomi","name":"b_test.nomi","status":"failed","message":"...","report":"..."}
{"type":"test","file":"/abs/path/a_test.nomi","line":20,"end_line":22,"name":"a_test.nomi :: counts","status":"blocked","message":"a_test.nomi:9:20: this call to `skip_odd` is not supported yet, so `fn evens` cannot run","error_line":9}
{"type":"summary","passed":3,"failed":2,"blocked":1}
```

| Field | What it holds |
|---|---|
| `file` | The absolute path. |
| `line`, `end_line` | The declaration's first line, which `--line` matches, and its last. Find a result by `file` and `line`, not by `name`. |
| `name` | The label the text report prints. A test in a `subtests` block has one record per row, all at the test's `line`. |
| `status` | `passed`, `failed` or `blocked` ([When code cannot run yet](#when-code-cannot-run-yet)). |
| `message` | What the text report prints under a failing or blocked test, never colored. Empty for a pass. |
| `error_line` | The line the failure points at, when there is one. |
| `output` | What the test wrote, when it wrote anything. |
| `report` | What the text report prints for the record. It is colored when text mode would color it (`NOMI_COLOR=always` forces it). |
| `cases`, `failed_cases` | For a test that runs `subtests`, the rows it ran and the rows that failed. |
| `decided` | Why the test ran: `new`, `changed`, `moved`, `effects` or `all`, or `unchanged` when it did not run ([Unchanged tests](#unchanged-tests)). Left out when no cache was used. |
| `effects` | `true` for a test whose code can have effects. |

A `"type":"file"` record is a failure that stopped a whole file, such as
a type error, and has no `line`. The summary counts cases, and
`unchanged` counts those a stored pass answered.

### `nomi effects --format json`

One object with the facts the text report prints ([`nomi
effects`](#nomi-effects)):

```json
{
  "project": "app",
  "self": {"effects": ["std::fs", "std::io"], "effects_in_tests": ["std::process"]},
  "dependencies": {
    "effects": ["std::fs", "std::http"],
    "effects_in_tests": [],
    "entries": [
      {"package": "fetch", "effects": ["std::http"], "effects_in_tests": []},
      {"package": "pg", "effects": ["std::fs"], "effects_in_tests": []}
    ]
  },
  "table": {"changes": [{"change": "add", "list": "effects.dependencies.effects", "name": "std::http"}], "left_out": [], "refused": 1, "problems": []},
  "written": false,
  "declared_less": []
}
```

| Field | What it holds |
|---|---|
| `self` | The tightest `[effects.self]`. |
| `dependencies` | The tightest `[effects.dependencies]`, and in `entries` each `[dependencies]` entry by its key as `package`, with the keys `--write` writes on it. |
| `table` | `null` when `nomi.toml` has no lists. |
| `table.changes` | How the lists differ from the tightest ones, or with `--write` what it changed: each a `change` (`add` or `remove`), a `list` (the key's path, such as `effects.self.effects`), a `name`, and the `module` or `package` for an entry. |
| `table.left_out` | Each table the file does not have whose code has effects. |
| `table.refused` | How many calls `nomi check` refuses under the lists. |
| `table.problems` | Each problem `nomi check` finds in the lists themselves. |
| `written` | Whether `--write` wrote `nomi.toml`. |
| `declared_less` | Each `package` and `name` a dependency's own `[effects.self]` does not allow its own code. |

With `--why`, the object is the `name` asked about and its `calls`, each
with `name`, `package`, `site`, `callee`, `tests`, `reach` (the chain in
words) and, for a dependency's call, `through`. At a workspace's root
there is one object per member.
