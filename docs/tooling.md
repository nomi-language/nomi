# Nomi tooling reference

This reference covers the `nomi` command and the files around a program:
the commands and their flags, `nomi.toml`, modules and dependencies, the test
runner, what each kind of build writes, Go bindings, environment variables,
and the editor and debugger support. What the language does is in the
[specification](spec.md), and the standard library's types and functions are
in the [standard library reference](/reference/). To install `nomi`, see
[Installing Nomi](install.md).

## Commands

### The `nomi` command

`nomi` is the one tool. Each command has its section:

| Command | What it does |
|---|---|
| `nomi run <file>` | Run a program ([Running a program](#running-a-program)); `nomi <file>` runs a script ([Scripts](#scripts-nomi-file)), and `--watch` runs it again on every change ([`--watch`](#running-again-on-every-change---watch)). |
| `nomi run <module>/<entry>` | Run a project's entry point ([Project mode](#project-mode-nomi-run-moduleentry)). |
| `nomi run --web <file>` | Serve a browser build on localhost and rebuild it on a change ([A program in the browser](#a-program-in-the-browser)). |
| `nomi test [path]` | Run tests ([Running tests](#running-tests)). |
| `nomi check <path>` | Check a program without running it, as text or JSON ([Checking](#checking-nomi-check)). |
| `nomi fmt [-w\|-l\|-d] <paths>` | Format source ([Formatting](#formatting)). |
| `nomi` | The REPL ([The REPL](#the-repl)). |
| `nomi build <file>` | Write an executable ([Executables](#executables)), a browser folder with `--web` ([A program in the browser](#a-program-in-the-browser)), or a `.nomic` file with `--no-vm` ([Compiled programs and `nomi-vm`](#compiled-programs-and-nomi-vm)). |
| `nomi build-vm` | Write `nomi-vm`, or with `--reactor` the WASI reactor, with no program ([Compiled programs and `nomi-vm`](#compiled-programs-and-nomi-vm)). |
| `nomi new`, `nomi init` | Start a project ([Starting a project](#starting-a-project-nomi-new-and-nomi-init)). |
| `nomi add`, `nomi remove`, `nomi update`, `nomi fetch` | Change and download dependencies ([Add, update and remove dependencies](#add-update-and-remove-dependencies)). |
| `nomi tool <module> <name>` | Run a module's tool ([Tools](#tools-nomi-tool)). |
| `nomi messages accept <locale> <message>` | Mark a translation current ([`[i18n]`](#i18n)). |
| `nomi dap` | The debugger editors start ([Debugging](#debugging)). |
| `nomi mcp` | The compiler as tools for a coding agent ([Coding agents](#coding-agents-nomi-mcp)). |
| `nomi version` | Print the version. |

### Checking: `nomi check`

`nomi check <path>` type-checks and compiles a program without running it.
Code the program reaches that the compiler cannot lower is the error at its
source that `nomi run` would stop with and `nomi test` would report as
`BLOCKED`. A test file is checked as `nomi test` loads it, each case
lowered and none run, and a directory's test files, and a module's beside
its entries, are checked with the rest.

In a directory without `entry_points`, a file that another file there
imports is checked through that importer, as `nomi run` loads it, and
listed as `ok <file> (through <importer>)`; a file nothing there imports
is checked on its own. A failed check of a directory prints `FAIL <file>` for each file that
holds errors, a file the program imports or a message catalog, with
its errors below it, and an error two programs share once.

A file that does not parse reports every syntax error it holds in one
run: the first of each declaration, and the first of each statement inside
a body. A statement after a damaged one is read on its own, so one mistake
gives one error. Type errors are reported once the file parses.

`--format short` prints each error as one `path:line:col: message` line
instead of with its source lines (`--format full`, the default), as
`NOMI_DIAGNOSTICS=short` does for every command.

`--format json` writes JSON Lines on stdout for a tool or an agent to read,
as `nomi test --format json` does: a record per error, a record per file
checked, and a summary last.

```json
{"type":"diagnostic","file":"/abs/main.nomi","line":5,"col":12,"end_line":5,"end_col":15,"start_byte":54,"end_byte":57,"severity":"error","message":"List<Int> has no field 'len'","hints":["Nomi has no method calls: write `Iter.count(xs)`"],"fix":{"title":"Call `Iter.count`","edits":[{"line":5,"col":9,"end_line":5,"end_col":9,"start_byte":51,"end_byte":51,"text":"Iter.count("},{"line":5,"col":11,"end_line":5,"end_col":16,"start_byte":53,"end_byte":58,"text":""}]}}
{"type":"file","file":"/abs/main.nomi","status":"failed"}
{"type":"summary","files":1,"failed":1,"diagnostics":1}
```

- `file` is absolute, or `std/<module>.nomi` for an error in the standard
  library. Lines and columns are 1-based, a column counts bytes, and an end
  is one past the last byte; `start_byte` and `end_byte` are the same
  points as 0-based byte offsets into the file. A span that is a point has
  no end.
- `severity` is `error`: every diagnostic stops the program. `code` names
  a diagnostic's class when it has one (`unused-import`).
- `hints` are the `help:` lines, and `related` the other places the error
  names, each with its own `file`, span and `message`.
- `fix`, when present, is the rewrite the error knows: its `edits` replace
  each span's text with `text` (an empty span inserts), applied together.
  Source written as another language writes it (`let x = 1`, `xs.len()`,
  `match`, `Point{x: 1}`, `nil`) carries one; the editor offers the same
  edits as a quick fix.
- A `"type":"file"` record's `status` is `ok` or `failed`; a file checked
  through an importer that loads it names it as `through`.

The exit status is the same as in text mode.

### Tools: `nomi tool`

`nomi tool <module> <name> [args]` runs a module's tool: the program
`tools/<name>.nomi` at the module's root, with the arguments after its
name. A tool is an entry point without being listed in `entry_points`. It
is an ordinary program, run in the current directory with the same access to files, the network and output
that `nomi run` gives a program, and runs only when someone runs it.
`<module>` is the current module's name or one its `go.mod` requires. It
is how a module ships a code generator, such as sqlkit's `nomi tool
sqlkit generate` ([Databases](/databases/)), which writes ordinary `.nomi` files a program
commits.

### Running a program

One engine runs a Nomi program: the VM, for `nomi run`, `nomi test`, the REPL and the tour playground. The compiler parses, analyzes and type-checks the program and compiles each function it can for the VM. It is not native code.

#### File mode: `nomi run <file>`

Runs a specific file. The file must define a top-level `fn main()` callback; a program whose functions a host calls (spec §7 *Exported Functions*) has none, and is built with `nomi build --web` instead. `fn main` is the program entry point — the host invokes it from the file's own scope, so it does not need to be `pub`. The project root is the directory containing the file — all imports resolve relative to that directory.

```
my_project/
  app.nomi           # declares fn main; `import math` works
  math.nomi          # import path `math`
  models/
    user.nomi        # import path `models::user`
```

```sh
nomi run app.nomi
```

This is the simplest way to run Nomi — no config file needed. Good for scripts, small projects, and getting started quickly.

#### Running again on every change: `--watch`

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
(spec §2 *Shebang line*) it lets a file run as a command:

```console
$ cat hi.nomi
#!/usr/bin/env nomi
import std::io

fn main() {
    io.print("hi")
}
$ chmod +x hi.nomi
$ ./hi.nomi a b
```

The operating system runs `./hi.nomi a b` as `nomi ./hi.nomi a b`. Every
argument after the file, flags included, is the program's own: `boot` reads
them as `startup.args` (spec §15 *Scoped application fields*), here `["a", "b"]`.

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
module's entry points, so it must be listed in `entry_points` unless it is
one of the module's tools under `tools/`.

**Extensionless scripts.** A file without the `.nomi` extension is a program
when it starts with `#!`, so a command can be named like any other:

```console
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

### The REPL

The REPL compiles each input as one more file of the session's program, which sees the declarations and top-level bindings of the inputs before it, and runs it on one live VM. An earlier input is never checked, compiled or run again, so an input takes as long in a long session as in a new one: a later input reads the value an earlier binding computed, and a closure keeps the value it captured. Redefining a function affects later inputs only, and so does redeclaring a type with a different declaration: earlier values, functions and impls keep the old type, and a later input that names the type gets the new one, which starts with no impls. When the two meet in an error, each is named by the input that declared it (`expected Point (input 1), got Point (input 5)`). Redeclaring a type exactly as it is keeps it, and redefining an impl replaces the old one everywhere. An input that fails to check, faults or is blocked prints its error and changes nothing.

### The artifact cache

`nomi run`, `nomi check`, `nomi test` and `nomi build` keep each program
they check and compile in the artifact cache, `<user cache>/nomi/artifacts`
(`~/Library/Caches` on macOS, `~/.cache` on Linux, `%LocalAppData%` on
Windows), or `NOMI_ARTIFACT_CACHE`. The next command over the same files
reads it and checks and compiles nothing: an unchanged program of 50,000
lines runs in about 0.06 s instead of 2 s. A program is kept only when it
checks with no error and every part of it a run or its tests reach can
run, so a command that reads one prints exactly what it would print
otherwise. A program with Go bindings is not kept.

A program is read again only while the files its build can read are as
they were: its `.nomi` files and every other in its project and its
dependencies, `nomi.toml` and the other `.toml` files (message catalogs),
`go.mod` and `go.sum`, the files under a folder an `embed` pattern names, and
any `nomi.toml`, `main.nomi` or `go.mod` above the project. Any change to
them, or a new `nomi`, builds the program again; other files the project
holds, such as what a run writes, change nothing. A file saved with the
same text is not a change. The settings in `NOMI_` variables are part of
what is kept, so a command with other settings builds again.

`std` holds the standard library's artifacts and `programs` the programs'.
A program nothing has read for five days is removed by the next command
that keeps one, which looks at most once an hour. The cache is safe to
delete. `NOMI_ARTIFACT_CACHE=off` keeps and reads nothing, and
`NOMI_DEBUG_ARTIFACT_CACHE=1` says on stderr, for each command, whether it
read a kept program and why not.

## Projects

### Starting a project: `nomi new` and `nomi init`

`nomi new <module path> [dir]` starts a module in `dir` (a new directory
named for the path's last element when left out). It writes a `nomi.toml` whose `name` is that element without a `/vN` suffix, written
as an identifier (`hello-world` is `hello_world`), with `entry_points =
["main"]`, a `go.mod` with the `module` line and the `go` line of the Go
release `nomi` pins, a `main.nomi` that prints hello, and an
`AGENTS.md` for coding agents that names the commands and links the
[cheat sheet](https://nomi-lang.org/cheatsheet.md), keeping a `main.nomi`
or `AGENTS.md` already there. It refuses a directory with a `go.mod` or a `nomi.toml`, naming
`nomi init`. It prints what it wrote and the next command.

`nomi init [module path]` makes the current directory a module. It writes
the `nomi.toml` and the `go.mod` it lacks, as `nomi new` writes them, and
never replaces either. The module path is the `go.mod`'s when there is
one (a different argument is refused), and the argument otherwise, which
is then required. `entry_points` lists the module-relative name of every
file under the directory with a top-level `fn main` (tools, test files,
hidden directories and directories with their own `nomi.toml` are
skipped), or `["main"]` when there is none and there are no tools. It writes
no source file, refuses a directory that holds both files, and prints what
it wrote.

### `nomi.toml` and `go.mod`

A Nomi module's identity lives in two files at the module root:

- **`go.mod`** — the Go module path (used by `go` tooling and the
  module proxy), dependency graph (`require` / `replace`), and Go
  version. Standard `go.mod` syntax; nothing Nomi-specific. `nomi` writes
  it (`nomi new`, `nomi init`, `nomi add`, `nomi remove`, `nomi update`, `nomi fetch`),
  and a Nomi project commits it and `go.sum` without editing either.
- **`nomi.toml`** — the Nomi-side identity (`[module].name`, the
  module's short name used by other modules' import paths) and the
  entry points other than the module's tools (`[module].entry_points`;
  every file under `tools/` is an entry point without being listed, [Project mode](#project-mode-nomi-run-moduleentry)).

```toml
# nomi.toml
[module]
name = "my_app"
entry_points = ["main"]
```

```
# go.mod
module github.com/example/my_app

go 1.22

require example.com/shared_kit v0.0.0
replace example.com/shared_kit => ../shared_kit
```

Every key `nomi.toml` accepts:

| Table | Key | What it holds |
|---|---|---|
| `[module]` | `name` | The module's short name, the first part of other modules' import paths. |
| `[module]` | `entry_points` | The files with a `fn main`, other than tools ([Project mode](#project-mode-nomi-run-moduleentry)). |
| `[i18n]` | `locales` | The locales every message is translated into, and whose CLDR data the program carries ([`[i18n]`](#i18n)). |
| `[i18n]` | `partial_locales` | Locales whose catalogs may leave a message out. |
| `[i18n]` | `source_locale` | The language of `message` declarations' own text; `en` when left out. |
| `[i18n]` | `catalogs` | The catalog directory under the module root; `locales` when left out. |
| `[i18n]` | `web_embedded` | The locales a browser build puts in `app.wasm` ([Locales in the browser](#locales-in-the-browser)). |
| `[money]` | `custom_currencies` | The custom currencies the module's literals may write ([`[money]`](#money)). |

The two files are siblings; neither subsumes the other. Cross-module
dependencies are Go's `require` directives: a required module whose root
holds a `nomi.toml` is importable under its `[module].name` (spec §14). A
`replace` that points a requirement at a local directory makes that
directory the module, as in Go; `nomi add ../shared_kit` writes both lines
above.

### Project mode: `nomi run <module>/<entry>`

Runs from a Nomi module — a directory containing both `go.mod` and
`nomi.toml`. The manifest's `[module].name` is the module's short
name; its `entry_points` lists the files that contain `fn main`, other
than its tools. Every `.nomi` file under the module's `tools/` directory is
a tool, which `nomi tool <module> <name>` runs, and an entry point without
being listed. Test files there are not tools, and neither is a file in an
`internal/` directory there, which is where a tool's helpers go. Listing a
tool in `entry_points` is allowed and changes nothing. The project root is
the directory containing `nomi.toml`.

```toml
# nomi.toml
[module]
name = "my_app"
entry_points = ["main"]
```

```
my_project/
  go.mod             # Go-side identity + dependencies (require, replace)
  nomi.toml          # Nomi-side identity + entry points
  main.nomi          # entry — declares fn main + optional fn boot
  tools/
    seed.nomi        # a tool, an entry run by nomi tool my_app seed
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

The compiler enforces the manifest-vs-source invariant: every entry
point, each file listed in `entry_points` and each tool, must have
exactly one `fn main`, and every other file must have zero. Files inside `internal/` are
reachable only from files rooted at the parent of that `internal/`
(intra-module) and unreachable from any other module (regardless of
`pub`). See the *Module boundaries* note in spec §14.

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

An extensionless `#!` script's walk stops at its own directory (*Extensionless scripts* in [Scripts](#scripts-nomi-file)), in the editor as on the command line.

The walk stops at any workspace bound the host has supplied (the editor's open folder, for instance); it never escapes above. `nomi run`, `nomi test`, `nomi check`, the language server and other tooling share this one rule, so a file opened in an editor resolves its imports to the same files `nomi run` loads.

This is why a sub-directory file like `app/prod.nomi` correctly finds `types.nomi` one directory up: the walk lands at the project root that contains `main.nomi` (or `nomi.toml`), not at the file's immediate parent.

### `[i18n]`

`nomi.toml`'s `[i18n]` table names the locales whose CLDR data the program
carries ([A program's locale data](#a-programs-locale-data)) and how its messages are translated
(spec §18 *Messages*):

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
build compiles into `app.wasm` ([Locales in the browser](#locales-in-the-browser)), the source
locale when it is left out; it needs `locales`, and each entry must be one
`locales` or `partial_locales` lists.

The program carries the data of one pinned CLDR release, 48.2. Each
translation is an entry in a catalog: a TOML file at the declaring file's
path under the catalog directory, in a directory per locale, so
`shop/cart.nomi`'s Polish catalog is `locales/pl/shop/cart.toml`. Each
locale in `locales` must have every message, and a locale in
`partial_locales` may leave any out. What a catalog holds is in
[std::message](/reference/message/).

`nomi messages accept <locale> <message>` marks a locale's translation of a
message current: it records the message's text as it is now as the source
the translation was made from. `<message>` is the name of a `message`
declaration, or `path/to/file:name` when two files of the module declare
one of that name.

### `[money]`

`nomi.toml`'s `[money]` table lists the custom currencies ([std::currency](/reference/currency/)) the
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

## Modules and dependencies

A Nomi module's identity is split between `go.mod` (Go-side identity and
dependencies) and `nomi.toml` (Nomi-side identity and entry points). A
Nomi module is a Go module, so it is published by pushing a version tag and
fetched through the Go module proxy, without the `go` command. `nomi
publish` is reserved, there is no Nomi-specific registry, and the compiler
does not check semantic versions; the [roadmap](roadmap.md) holds the plans.

### Add, update and remove dependencies

Run these in the project's directory. They write `go.mod` and `go.sum`,
which the project commits; nobody edits `go.mod` by hand.

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

`nomi publish` is reserved: publishing a module is pushing a version tag to
its repository, as for any Go module, and the module proxy serves it from
there ([Distribution](#distribution)).

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

### Distribution

Distribution rides on the Go module proxy: a Nomi module is a Go module,
so a module pushed with a version tag to a public repository (GitHub,
GitLab, etc.) is served by `proxy.golang.org` and fetched as *Dependencies*
above describes. Several modules may share one repository, each in a
subdirectory with its own `go.mod` and tags prefixed by it
(`sqlite/v0.3.0`), as Go lays them out.

There is no Nomi-specific registry, and none is planned.

### No lock file

Nomi has no lock file of its own. A Nomi module's dependencies are Go
module requirements, so `go.mod` pins their versions and `go.sum` their
contents, and `nomi` checks every download against it (*Dependencies*
above); a separate `nomi.lock` is not planned.

## Running tests

What a test is, and what a test may write, is in the specification's Tests
chapter (spec §17). This page is what `nomi test` does with them.

### Finding and selecting tests

`nomi test` accepts an optional file or directory path. A file path runs
tests in that `.nomi` file regardless of its name; a directory path searches
recursively for every `*_test.nomi` file plus ordinary `.nomi` files that
contain test declarations.

`nomi test <file> --line N` runs the test whose declaration starts on line
`N` (or `N + 1`): a `test`, an attached `//!` test (any of its `//!` lines),
or a `group`, whose header line runs every test in the group. When
nothing starts there, `nomi test` runs nothing and exits non-zero with
`no test at <file>:<N>` and the nearest test's first line; with
`--format json` that error is a `"type":"file"` failure record.

### `nomi test --watch`

`nomi test --watch [path]`, with any of the other flags, runs the tests,
then runs them again whenever a `.nomi` file under the path, or a file the
tests import, changes, as `nomi run --watch` does ([`--watch`](#running-again-on-every-change---watch)): tests still running
are stopped, `<file> changed; running again` is printed on standard error,
and a file that fails to compile is reported as `nomi test` reports it. A
new test file under the path is picked up by the next run.

### Timeouts

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

### Output

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
stdout in text mode and to stderr in json mode. In a test case outside any
`io.capture`, stderr is part of the case's output, which the report shows
under the case.

```text
ok a_test.nomi :: passes
  dbg line 4: 5 = 5
  printed
FAIL a_test.nomi :: fails
  dbg line 10: 6 = 6
  line 11: assertion failed
    assert False
```

### Line diffs

When `assert a == b` fails, both operands are Strings and at least one holds a line break, the report shows a line diff in place of their two rows. The left operand is the actual value and the right the expected, so the value under test goes on the left. Each row is a marker, `│`, then the line as it is: `-` marks a line only the expected side has, `+` a line only the actual side has, and a blank marker a line both have. When the expected side is a `"""` literal in the file, each of its lines carries its line number in the file; a line only the actual side has carries no number. The assertion line shows that literal collapsed, `""" lines 8-10 """`, or `""" ... """` when its lines are not numbered (an interpolated value with a line break changed their count). A changed empty line reads `(empty line)`. More than three unchanged lines away from a change collapse to `... N lines match ...`. A changed line that ends in whitespace ends in `$` (a trailing tab reads `\t`), a carriage return reads `\r`, and when only one side ends in a newline the other's last line is followed by `\ no newline at end`. A report with a line diff has no `values:` row for a String equal to either side, line for line, with trailing whitespace and final newlines set aside; when no row is left, the `values:` heading goes too.

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

A failed `refute a == b` or `assert a != b` means the two were equal, so it has no diff. An `Assertable` whose `AssertionDetails` sets `actual` and `expected` to different values gets a line diff of the two in place of its `actual:` row.

### JSON Lines

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
  VM could not run (see [When code cannot run yet](#when-code-cannot-run-yet)); its `message` is what the text report's
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
- A test that runs `subtests` counts one case per row it ran
  (spec §17 *Subtests*). Its record carries its `cases` count, and a failed
  one its `failed_cases`; the summary counts cases.
- A test that cannot run counts one blocked test, whatever its rows.

In json mode stdout carries only records: a test's output is in its record.
The exit status is the same as in text mode: nonzero when a test file fails
to load, a case fails, or a case is blocked.

`--line`, an editor's test lens and the debugger treat a test that runs
`subtests` as one test. A breakpoint in a `subtests` body stops once per
row, in a frame of its own.

### The test environment

`nomi test` sets the environment variable `NOMI_ENV` to `test` for the run,
unless the environment already sets it. Nothing else reads it: a program
reads it from `startup.env` as it reads any variable, so a `boot` can pick
its settings by it ([App Fields, Defer &
Context](/app-fields/#settings-per-environment)). `nomi run`
does not set it.

Under `nomi test` a test file's working directory is its module root: the
nearest directory at or above the file holding a `nomi.toml`, or the file's
own directory when there is none. So a test that reads its module's files by
a relative path (`fs.read_text("data/rates.csv")`) reads the same files
wherever `nomi test` runs.

## Formatting

`nomi fmt [-w|-l|-d] <paths>` formats Nomi source: `-w` writes the result
to each file, `-l` lists the files that would change, and `-d` shows a diff.
Editors format on save through `nomi-lsp`.

`nomi fmt` gives every program one layout and has no options. It indents 4
spaces per level and breaks lines at 100 columns. It keeps a few layout
choices of the author's, which Writing Nomi lists
([Let `nomi fmt` own the layout](/writing/files-and-layout/#1-let-nomi-fmt-own-the-layout)),
and decides the rest from the syntax and the width. The
[implementation notes](implementation-notes.md#formatter) say why some of
these rules are built the way they are.

**Blank lines between statements.** A statement that spans more than one
line gets a blank line before it, unless it opens its block, and after it,
unless it closes its block. A block's final expression counts as a
statement. Whether a statement spans lines is decided as rendered at the
block's indent, so a call that fits in a shallow block may break, and be set
apart, in a deeper one. A `case`, a nested block, a triple-quoted string
written across lines and a literal broken after its opening bracket always
span lines. A stacked
pipeline, or one broken because it does not fit, is set apart; one that fits
on its line sits with its neighbours. A binding `else` is measured by its
first line, so a run of one-line guards reads as one step; a binding `else`
with arms is laid out as a `case`. One-line statements sit together, and a
single blank line the author writes between them is kept. A blank line at
the top or bottom of a block is removed, and a block's imports are followed
by one.

**Strings and templates.** A string is never split: a double-quoted one,
typed (`Html"..."`) or not, stays on its line however long. A triple-quoted
one written on one line, `Html"""<i>${n}</i>"""`, stays on one line when it
fits, and is framed, its body on lines of its own, when it does not. A
framed triple-quoted or multi-line backtick string's body and closing
delimiter share one indent, taken from the line the opening delimiter is
on. A literal that starts its line (a statement, a function's result, an
argument on a line of its own) puts them at that line's indent:

```nomi
fn footer() -> Html {
    Html"""
    <footer class="site">
      <p>Made with Nomi</p>
    </footer>
    """
}
```

A literal that follows code on its line (after a binding's `=`, a struct
field's `:`, a map entry's `=>`, a case arm's or lambda's `->`, `return`,
or a declaration's header) puts them one level in, the way a broken
pipeline continues after `=`:

```nomi
fn page(name: String, avatar: Maybe<String>) -> Html {
    title = Html"""
        <h1>${name}</h1>
        """

    case avatar {
        Some(url) -> Html"""
            ${title}
            <img src="${url}" alt="">
            """

        None ->
            title
    }
}
```

A case arm's or a lambda's literal stays on the `->` line. `nomi fmt` moves
a body written at another indent to this one, with its closing delimiter,
so the lines keep their nesting relative to each other and the string's
value is unchanged. A
`${...}` written on one line stays on one line, however long the line of
text around it, since that text cannot break either. One whose expression
spans lines, in a triple-quoted body, is laid out from the column of its
`${`, so its lines stay with the text around it.
`nomi fmt` never changes the text of a string, a template's markup
included.

**Pipes.** A pipe written on one line stays inline, and one written across
lines stays stacked, each `|>` aligned with the subject. An inline chain
breaks only when it overflows; a stacked one is never joined. An
overflowing chain bound to a name moves under the binding, one indent in. A
pipeline with a `then` or `tap` stage is always stacked, since that lambda's
body ends at the next `|>`.

**`if`, binding `else` and lambda bodies.** See
[one-line `if` and binding `else`](implementation-notes.md#formatter-one-line-if-and-binding-else)
for when a guard or a two-branch `if` goes on one line; an `else if` chain
is always broken. A single-expression lambda whose body is an `if` holding
`break`, `continue` or `return` goes below the header in braces, as does a
value-producing lambda body that does not fit, and a call it ends closes
with `})`. A braced body written with its contents below the `{` stays that
way ([below](implementation-notes.md#formatter-a-line-break-after-a-literals-opening-bracket)).

**`case` arms.** When every arm fits on one line as `pattern -> body` and no
body is a block, the arms sit on consecutive lines. Otherwise every arm
breaks: a block body keeps `-> {` on the pattern's line, and so does a
triple-quoted body its `"""`, any other body goes on the next line one indent
in, and one blank line separates every two arms. A triple-quoted body breaks
the arms as a block does.
Blank lines the source put between arms are ignored, and a comment stays
above its arm.

**Trailing commas.** A multi-line call has a trailing comma after its last
argument when it has more than one argument, and none when it has one.

**Declarations.** Declarations with bodies get a blank line between them.
Bodyless declarations of one kind (`once`, `type`, `typealias`, `host
type`, `host fn`, a run of `derive`s for one type) may sit together, and a
blank line written within a kind is kept; different kinds are separated by
a blank line. A bodyless declaration with a doc comment stands apart. The
same holds inside `impl` and `interface` blocks. Consecutive `derive`s for
one type lose the blank lines between them. A test group's `clock`, `boot`
and `setup` move to the top of the group in that order.

**Imports.** A file's or block's imports are combined into one `import { …
}` block sorted with `std::` paths first, and a lone import stays bare.
Separate `import` lines of one path merge into a selector list
(`import std::calendar.{Date, Months}`); two entries of one block do not. A
comment line between two `import` statements keeps them apart, and each side
is combined and sorted on its own. A file with both writes an `import` block
and then a `pub import` block. An import inside a block moves to the top of
that block.

**Tail `return`.** A `return` that is the last thing a function, lambda or
test body does is removed, including the last expression of each branch of a
tail `if` and each arm of a tail `case`. A bare `return` at the end of a
`Unit` body goes when an expression statement comes before it. In a test
body, a tail `if` or `case` keeps its `return value`s, since its branches'
values need not share a type there. A tail `if` with no final `else` keeps
its `return value`s too, since its branches' values are wrapped in `Some`.

**Struct literals and anonymous struct types** are kept on one line when
short and broken one field per line, each with a comma, when long
([brace-bounded field constructs](implementation-notes.md#formatter-brace-bounded-field-constructs)).
An anonymous struct type or variant written across lines keeps one field per
line. Literals follow
[a line break after a literal's opening bracket](implementation-notes.md#formatter-a-line-break-after-a-literals-opening-bracket);
lists of numbers are [filled](implementation-notes.md#formatter-filled-literal-lists); long
comparisons and boolean chains break
[before the operator](implementation-notes.md#formatter-comparison-and-boolean-operators); three
levels of brackets break
[the outermost](implementation-notes.md#formatter-compound-depth); and comments stay
[with their item](implementation-notes.md#formatter-comments). A trailing comment is never moved to
make room, so it may run past 100 columns.

**Small layouts.** One space follows `not`, and parentheses written around
it or its operand stay. A `?` or `!` sits against its expression. `} else`
and `} else if` share a line. A long `or` pattern breaks after each `or`.
The arms or branches of a `|> case` or `|> if` stage sit one indent in from
the line its `|>` is on.

## Building

### Executables

`nomi build <file> [-o <out>] [--target <goos>/<goarch>]` writes one executable: the `nomi-vm` binary with the compiled program appended to it, the way `deno compile` and `bun build --compile` work. At startup `nomi-vm` reads the program from its own file and runs `main` exactly as `nomi run` does: the same arguments, standard input, signal handling, output and exit status. It skips the front end, so a hello-world program starts in a few milliseconds, and then runs on the same VM at `nomi run` speed. It is not native code.

`nomi build` refuses what `nomi run` would refuse, with the same text, and writes nothing: a front-end error, or an error at each piece of code the compiler cannot compile. It also refuses:

- a file without `fn main`, which has nothing to build, unless it has an `export fn` and the target is `wasip1/wasm` (below);
- a program with a `todo` or a `dbg` in any of its files, whether or not a run would reach it (spec §11 *Unwritten code: `todo`* and spec §8 *`dbg`*). It prints one list of every `todo` and every `dbg`, in file and source order, under one header that counts them (`nomi build: 2 todos remain:`, or `1 todo and 2 dbgs remain:` with both). Each line is the position and the item: a `todo` with its reason, a `dbg` with its operand cut after 40 characters with `...`, and `|> dbg` for a pipe stage.

```text
nomi build: 1 todo and 2 dbgs remain:
  main.nomi:2:5 todo "parse the header"
  main.nomi:7:5 dbg total + 1000000000 + 2000000000 + 300000...
  main.nomi:8:18 |> dbg
```

`--target`, or `GOOS`/`GOARCH` in the environment, picks the platform. `nomi-vm` comes from, in order:

1. `nomi-vm` beside `nomi` (or beside the file a symlinked `nomi` resolves to), built for the target. Every release archive ships one.
2. A source checkout of the compiler: `nomi-vm` is built with `go build` and cached. This is how `go install` and a development tree work, with no release involved.
3. A `nomi-vm` downloaded earlier for this release and target.
4. Download: the target's archive from the release this `nomi` was cut as, verified against the release's checksum file, with only `nomi-vm` extracted and cached. `nomi build` prints the URL it downloads, and with no network the error names it.

A prebuilt `nomi-vm` must come from the same commit as `nomi`. Both carry it in their build information, and `nomi build` refuses a `nomi-vm` from another commit. So a release builds executables for every platform with no Go toolchain, and a built binary needs nothing at all.

Two kinds of program still need Go, which builds their own `nomi-vm` from the compiler module at the release's version:

- a project with Go FFI bindings, whose `nomi-vm` links them;
- a program importing `std::compiler`, whose `nomi-vm` links the front end and is not shipped.

That Go is the `go` on `PATH`, or, with none, the Go release `nomi` was built with, which `nomi` downloads ([The Go toolchain](#the-go-toolchain)), so neither needs Go installed.

A WebAssembly target, `js/wasm` or `wasip1/wasm`, writes `<name>.wasm` by default. Nothing may follow a module's last section, so the program goes inside the module:

- A `wasip1/wasm` module carries it as one more active data segment, which the WebAssembly engine writes into a 4 MiB area of the `nomi-vm`'s own memory when it instantiates the module. The module reads its program from there, so it needs no file access: `wasmtime run hello.wasm` runs it, and so does a browser's WASI shim with no file system.
- The area is uninitialized memory, so it adds nothing to the file. It raises the module's declared initial memory by 4 MiB, to 11.4 MiB for the reactor without the network and 14.9 MiB for the one with it. `nomi build` refuses a program whose compiled form is larger than the area, naming the limit.
- A `js/wasm` module carries it in a custom section named `nomi.image`, which every runtime skips. A module cannot read its own sections, so it looks where its host can supply them. It uses `globalThis.nomiImage` when a loader has set it to the section's bytes (`new Uint8Array(WebAssembly.Module.customSections(module, "nomi.image")[0])`), and otherwise reads the file argv[0] names through the host's `fs`.
- Go's own loader runs a `js/wasm` module under Node unchanged: `node "$(go env GOROOT)/lib/wasm/wasm_exec_node.js" hello.wasm`. That loader hands the module Node's whole environment as `startup.env`, and Go's js/wasm runtime refuses to start when the arguments and environment together pass 8 KB ("total length of command line and environment variables exceeds limit"). In a large environment, run it with only what the program reads, as `env -i PATH="$PATH" node ...`. `nomi.js` passes only the `env` its caller gives.

A `wasip1/wasm` program with at least one `export fn` is written as a WASI reactor instead of a command module (spec §7 *Exported Functions*). The program is appended to `nomi-vm-reactor`, the same VM linked as a reactor, which is found, built, downloaded and checked as `nomi-vm` is. `nomi-wasi.js` and `nomi-wasi.d.ts`, the JavaScript host for it, are written beside the module.

A project with Go bindings builds for a WebAssembly target as for any other: its `nomi-vm`, with the bound Go packages and the generated adapters linked in, is built for `js/wasm` or `wasip1/wasm`. Every bound package, and every package it imports, must build for that target. One that does not is an error at its `gopkg`, naming the target, the import chain to the package that does not build, and why: it has no Go files for the target, or Go's first compile error there.

For example, `modernc.org/libc` has no files for either WebAssembly target, so a binding that reaches `modernc.org/sqlite` reads `` main.nomi:3:8: Go package "sq" does not build for js/wasm: it imports modernc.org/libc/sys/types (sq → modernc.org/sqlite → modernc.org/libc/sys/types), which has no Go files for js/wasm (build constraints exclude every one) ``. A project with Go bindings and an `export fn` builds for `wasip1/wasm` as a reactor that links its bindings, with `nomi-wasi.js` and `nomi-wasi.d.ts` beside it.

The `js/wasm` build keeps the browser build's limits: its file system is in memory, empty under Node, and a call nests at most 1,000 deep. A command-line program that reads real files belongs on `wasip1/wasm`. Every release ships a runner-only archive for each WebAssembly target, so a release `nomi` downloads it as it would any other platform's. The `js/wasm` archive also carries the `wasm_exec.js` of the Go that built its `nomi-vm`, and the `wasip1/wasm` archive carries `nomi-vm-reactor` beside `nomi-vm`.

### Embedded files

A program's `embed` declarations (spec §18 *Embedded files*) are resolved when the program is compiled, so every build carries their files: an executable, a WebAssembly module, a `--web` folder's `app.wasm` and a `.nomic` hold the bytes in the compiled program, and read no file but their own when they run. Delete the source tree and the build still serves its pages and applies its migrations. Patterns name files from the module root, as import paths do, whatever directory the declaring file is in.

A file's contents are read when the program reads that file, never all at once. A built executable or `.nomic` keeps the files after its code and maps them, so starting it costs the same however much it embeds, and a file it serves is paged in by the operating system rather than held in the program's memory; `nomi build` writes them from the files on disk without loading them whole. `nomi run` and `nomi test` read an embedded file from disk when the program reads it, and stop the program with an error naming the file if it changed after the program was compiled; run it again to embed the new contents. The REPL reads them from its working directory; a tour example reads its `// FILE:` data files.

Only the declarations a program reaches go into its build, so an `embed` nothing reads costs nothing, and a program with none is unchanged. The files count toward the compiled program's size, so a `wasip1/wasm` module's 4 MiB limit (above) includes them. `nomi run --watch` does not watch embedded files: a change to one is picked up at the next run.

### Capability classes

A std host function that acts outside the program in a way some target cannot has a capability class: `files` for std::fs's, `processes` for std::process's, `network` for std::http's requests, `connect` for std::net's and std::tls's dials and std::net's lookups, and `listen` for a server or listener. Every target has `files`, a `js/wasm` module's in memory. A `js/wasm` module sends HTTP requests through the browser's `fetch` but cannot open other connections, start programs or serve; a `wasip1/wasm` module has none of the four.

`nomi build` walks what `main`, the boot and the exported functions can reach, and refuses a program that reaches a call of a class its target lacks. It writes nothing and lists each call in the program's own code that reaches the class, at that call: the std function it calls (`fs.read_text`), the host function that one reaches (`fs_host.read_text`), and the functions the call is reached through. A hint follows, to build for another target or pass `--allow-unsupported <class>`.

The walk counts a call in a branch the program never takes, so `--allow-unsupported <class>` (repeatable) builds anyway. Such a call answers `Err(io.Error.Unsupported{operation, target})` when it runs; it never traps. `--no-vm` names no target and is not checked.

A project with Go bindings is checked the same way, over the std calls it reaches. A Go binding itself carries no class: what a bound Go function does is opaque to the compiler, and whether its package builds for the target is Go's answer (above). A bound function that reads a file in a `js/wasm` module reaches Go's in-memory file system there, as std::fs's calls do.

`nomi build --web` of a program that reaches `files` prints one line, `` nomi build --web: this program uses files; in the browser it sees only the files run's "files" option gives it ``.

### Compiled programs and `nomi-vm`

`nomi build <file> --no-vm [-o <out>]` writes the compiled program alone (`<name>.nomic` in the working directory by default), for a `nomi-vm` that does not carry it, or for a reactor to load ([Calling exported functions](#calling-exported-functions)). It needs no Go toolchain and no `nomi-vm`, and the same file runs on every platform's `nomi-vm`.

- It records the program's exported functions, so a program with an `export fn` and no `fn main` builds too. `nomi-vm` and `nomi run` refuse to run such a program, with `` this program has no `fn main`; it is called through its exported functions ``, and exit 1.
- It refuses what `nomi build` refuses, with the same text.
- It refuses a project with Go bindings, whose bindings only a VM built with them can answer: `` nomi build --no-vm: this project has Go bindings, which only a VM built with them can answer, so it has no portable compiled program ``.
- `--no-vm` takes no `--target` and no `--web`. A test file has no compiled program.

A `.nomic` file is a header followed by the program's image:

| Offset | Size | Field |
|---|---|---|
| 0 | 8 | Magic: the bytes `NOMIC` and three zero bytes |
| 8 | 4 | The program format version, a little-endian u32 |
| 12 | 4 | Flags, a little-endian u32. Bit 0: the program imports `std::compiler`. Every other bit is zero. |
| 16 | 2 | `n`, the length of the next field, a little-endian u16 |
| 18 | `n` | The version of `nomi` that wrote it, in UTF-8, as `nomi --version` names it (the commit, or for a release, the tag and the commit) |
| 18 + `n` | 8 | `L`, the image's length, a little-endian u64 |
| 26 + `n` | `L` | The image, the same bytes `nomi build` appends to `nomi-vm` |

`nomi build-vm [--target <goos>/<goarch>] [--compiler] [--no-network] [-o <out>]` writes a copy of the `nomi-vm` that `nomi build` appends programs to, with no program: `nomi-vm` by default, with `.exe` or `.wasm` as the target needs. `--no-network` writes the smaller `nomi-vm` that `nomi build` uses for a program that never uses std::http, std::net or std::tls: it runs any other program, and refuses one that reaches the network before running it. It comes from where `nomi build` gets one, so a release needs no Go toolchain for it. For `js/wasm` it also writes the matching `wasm_exec.js` beside it. For `wasip1/wasm` it writes the command module.

`nomi build-vm --reactor [--compiler] [--no-network] [-o <out>]` writes `nomi-vm-reactor` with no program instead (`nomi-vm-reactor.wasm` by default), which takes its program through `nomi_load` ([Calling exported functions](#calling-exported-functions)). It writes `nomi-wasi.js` and `nomi-wasi.d.ts` beside it, the JavaScript host that runs it in a browser or Node, creating the folder they go in if it does not exist. The reactor exists only for `wasip1/wasm`, so that is its target, and `--target` with any other value is a usage error.

`nomi build-vm` takes no program file, and neither form takes `nomi build`'s `--no-vm`, `--web` or `--static`: a program with an `export fn` built with `nomi build --target wasip1/wasm` is already a reactor with that program inside it. `--compiler` writes the `nomi-vm` or reactor that links `std::compiler`'s hosts, which a compiled program importing `std::compiler` needs, and which needs Go to build.

A `nomi-vm` with no program of its own takes one when it starts:

```
nomi-vm <program.nomic> [args...]
nomi-vm [-] [args...]          # the .nomic file framed on stdin
nomi-vm --version
nomi-vm --help
```

Given a path, it reads the `.nomic` file there and passes the words after the path to the program as `startup.args`. Given no path, or `-`, it reads one `.nomic` file from the start of standard input, using the header's length, and reads nothing past its last byte. The rest of standard input is the program's own input, and the words after `-` are its arguments. So `cat app.nomic input.txt | nomi-vm` runs `app` over `input.txt`. Stdin is the way to hand a program to a host with no filesystem, such as a browser's WASI shim.

- A `wasip1/wasm` `nomi-vm` takes a path through a preopened directory (`wasmtime run --dir=. nomi-vm.wasm app.nomic`), and a `js/wasm` one under Go's loader reads a path through Node's `fs` (`node wasm_exec_node.js nomi-vm.wasm app.nomic`). Either takes the program on stdin.
- A `nomi-vm` whose terminal is its standard input, or whose standard input is empty or not a `.nomic` file, prints its usage. A word starting with `-` other than these is a usage error.
- A `nomi-vm` that `nomi build` appended a program to runs that program and treats every argument as the program's, so `./app --version` passes `--version` to `app`.

`nomi-vm` refuses a compiled program whose format version differs from the one it reads, before it runs anything: `` app.nomic was written by nomi v0.4.0 in program format 17, and this nomi-vm (nomi v0.5.0) reads format 18 ``, followed by how to fix it. Programs written by different versions of `nomi` in the same format run. It also refuses a program with a flag it does not know, and a program importing `std::compiler` when it does not link that module's hosts. A damaged or truncated file is an error naming it. All of these, and every usage error, exit 1, as a failed program does.

`nomi run <file.nomic> [args...]` runs a compiled program in the `nomi` process, as `nomi-vm` would, with `std::compiler`'s hosts linked. `nomi run` recognizes a `.nomic` file by its first eight bytes, whatever its name.

### A program in the browser

`nomi build <file> --web [-o <dir>] [--static <dir>]` writes a folder that any static file host can serve, which runs the program's `main` in a browser. The default folder is `<name>-web` in the working directory. It holds the generated files and the author's static files:

| File | What it is |
|---|---|
| `app.wasm` | The `js/wasm` module `--target js/wasm` writes, with its memory capped at 1 GiB. |
| `nomi.js` | An ES module: the loader and its `run` function, with Go's JS support file `wasm_exec.js` from the Go toolchain that built the js/wasm `nomi-vm` inside it. Each run starts this same file as its module worker. |
| `index.html` | A page that runs `main` with an output pane and an input line. |
| `locales/<tag>.nomil` | For a program that lists its locales, one file per listed locale `[i18n].web_embedded` leaves out: its CLDR data and translations, which the program loads with `Locale.load` ([Locales in the browser](#locales-in-the-browser)). |

`nomi.js` runs `wasm_exec.js` only where a program runs: in the worker, or on the page for a `worker: false` run. A page whose programs run in workers gets none of the globals `wasm_exec.js` sets (`Go`, and `fs`, `process` and `path` when missing). Since the worker is a file the host serves and not a `blob:` URL, a policy of `worker-src 'self'` allows it.

The static files are the files of a `web/` folder: the one at the root of the program's project, beside its `nomi.toml`, or, for a file outside a project, the one beside the file. `--static <dir>` names another folder in its place; it must exist, while a missing `web/` folder only means there are no static files.

- Every file under the folder is copied to the same path in the output, subfolders included (`web/img/logo.png` becomes `img/logo.png`), except files and folders whose names start with a dot.
- A static file wins over a generated one of the same name, so `web/index.html` replaces the generated page; such a page loads `nomi.js` and calls `run` itself.
- A static `app.wasm` or `nomi.js` is refused, naming the file: `` nomi build --web: web/app.wasm: the build writes app.wasm, so a static file cannot have that name ``. So is a static file in `locales/` ending in `.nomil`, which the build writes, and an output folder inside the static folder.
- A rebuild replaces only the generated files and the static files it copies. Anything else in the output folder stays, such as a host's `_headers`, and so does a file copied by an earlier build whose source has since been deleted from `web/`.
- A rebuild removes each locale file an earlier build wrote that it does not write. It also removes a `wasm_exec.js`, `nomi-web.js` or `nomi-worker.js` whose contents are exactly what an older `nomi build --web` wrote there, and leaves a file of that name with any other contents. A static file may have one of those names, and is copied like any other.

`--static` without `--web` is a usage error, and so is `--web` with a `--target` other than `js/wasm`. A project with Go bindings builds: `app.wasm` is its own `nomi-vm`, with the bound Go packages linked in, built for `js/wasm` as `--target js/wasm` builds it ([Executables](#executables)), and a bound package that does not build for `js/wasm` is an error at its `gopkg`. A program importing `std::compiler` builds, and its `app.wasm` is larger because its `nomi-vm` links the front end; `nomi build` says how to serve a module over 20 MB.

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
  files: { "data/prices.csv": csvText },  // the program's files, string or Uint8Array; default none
  setup: "./setup.mjs",    // a module whose default export runs, awaited, before the program; default none
});
// files: every file in the program's file system when it ended, by path, as Uint8Array
```

`run` fetches and compiles the module once per URL; every later run of it reuses the compiled module. Each run gets a fresh instance in its own worker, so a page stays responsive while a program loops, and `timeoutMs` and `signal` can stop it. The run then resolves with exit code 124 (timeout) or 130 (abort) and a `fatal` text. `worker: false` runs on the page's own thread, one run at a time; a timeout or abort then resolves the run and drops its output, but cannot stop the program.

- Output reaches the callbacks as written, so a prompt written with `io.write` and no newline appears before the program waits for input.
- Standard input is a string, served and then ended; an async function, asked for one line at a time, with `null` ending the input; or absent, so `io.read_line` returns `Ok(None)` at once.
- The environment holds what `env` gives plus `NOMI_COLOR=never`, unless `env` sets it, so diagnostics carry no colour codes. Nothing comes from the page's URL or cookies. Arguments and environment together must fit in the 8 KB Go's loader reserves for them.

`setup` (of `run` and `load`) is a module URL, resolved as `url` is, for what a program's Go bindings need from JavaScript before it starts. The module is imported where the program runs, in its worker or on the page for `worker: false`, and its default export is called and awaited before the program's first instruction. A setup that throws or rejects ends the run with exit code 2 and a `fatal` naming the module. The sqlite driver's browser build is one user: it calls SQLite's WebAssembly build through `globalThis.sqlite3`, which its setup module sets with `globalThis.sqlite3 = await sqlite3InitModule()`.

The exit code is `nomi run`'s: 0, an `Exit`'s code (spec §15 *Entry callbacks*), or 1 for a `main` returning any other `Err`, a trap or a failed assertion. When the Go runtime stops (an allocation past the 1 GiB cap, a deadlock), the run resolves with exit code 2 and Go's own line, such as `fatal error: out of memory`, as `fatal`.

The browser build's limits apply as under `--target js/wasm`: the program's file system is in memory ([std::fs](/reference/fs/)), a call nests at most 1,000 deep, and no signal arrives. The file system starts with the files `files` gives, by path from its working directory `/work`. The run resolves with `files`, every file in it when the program ended, by the same paths (absolute for a file outside `/work`); a run stopped by `timeoutMs` or `signal` has no `files`. The clock, `std::random`, tasks, channels and `sleep` work. Tasks share the worker's one thread, so a task that never yields starves the others.

`index.html` loads `nomi.js` with a `<script type="module" src>` and no inline script, so it works under a strict `script-src`. `nomi.js` mounts itself on every element with a `data-nomi-program="app.wasm"` attribute: an output pane with standard error in a distinct colour, an input line that is enabled while the program waits for a line (Enter sends it, Ctrl-D on an empty line ends the input), the exit status, and a button that runs the program again. A page that imports `run` itself needs no such element.

The page needs a server, since a browser does not fetch modules from `file:` URLs: `python3 -m http.server -d hello-web` serves one locally. Serve `app.wasm` as `Content-Type: application/wasm`, compressed with brotli or gzip (a hello world is 11 MB, 2 MB with brotli); Cloudflare Pages, Netlify and Vercel do both themselves. Without that content type the loader still works, compiling from the whole response instead of streaming it.

`nomi run --web [--port N] [--open] [--static <dir>] <file>` is the loop for working on such a program:

- It builds the generated files into a temporary directory and serves them, with the static files served from where they are, by the same rule and with the same precedence and refusals as `nomi build --web`.
- It serves on `http://localhost:<port>/`, on port 8000 or the next free one up to 8099 unless `--port` names one (`--port 0` takes any free port), with `app.wasm` as `application/wasm`, the locale files as `application/json`, and every response marked `Cache-Control: no-cache`. It prints the URL; `--open` also opens it in the default browser.
- It watches the entry, every file the program loads, the project's `nomi.toml`, and the `go.mod` and Go files beside the program's files, polling them every 250 ms, and rebuilds once a burst of changes has settled. The page reloads itself after a successful rebuild. It polls the static folder too, and a change there reloads the page without a rebuild.
- A failed build is printed on the terminal and shown over the page, which keeps running the last build that succeeded until the error is fixed; when the first build fails, the page shows the error alone. A static file given a generated name while the server runs is reported the same way and not served.
- Every `.html` page served, the generated one or a static one, gets one more script, `/__nomi/dev.js`, added before its `</head>` (or at its end when it has none). It listens to the server-sent event stream `/__nomi/events`. Each event is `{"build": n, "static": m, "error": "..."}`: `n` counts the successful builds and `m` the changes to the static files, and a page reloads when either differs from the one it was served at. `nomi build --web` never writes any of this.
- Ctrl-C stops the server and removes the temporary directory. A project with Go bindings is served as `--web` builds it.

The browser program takes no arguments, so a word after the file is a usage error. `--port`, `--open` and `--static` without `--web` are usage errors too, and so is `--watch` with `--web`, since `--web` already rebuilds on every change. Without `--web`, `nomi run` takes no flags but `--watch` ([`--watch`](#running-again-on-every-change---watch)), and every word after the file is the program's.

### Calling exported functions

A program with exported functions (spec §7 *Exported Functions*) needs no
`fn main`. `nomi check` and `nomi test` treat it as any program. Exported functions are lowered whether
or not `main` reaches them, so `nomi check` reports an exported body the VM
cannot run as it reports one `main` reaches. `nomi run` of such a program with
no `main` is the error `` <file> declares no `fn main`, so there is no program
to run; its exported functions are called by a host, so build it for a page
with `nomi build --web`, or as a WASI reactor with `nomi build --target
wasip1/wasm` ``, and a plain `nomi build` gives the same error for `build`.
`nomi build --web` and `nomi build --target wasip1/wasm` build it.

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
maps as the JavaScript column of the table in spec §7 *Exported Functions*
says; a struct or enum whose `ToJson` is derived
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
  | `nomi_main` | `() -> i32` | runs `main` over WASI stdin, stdout and stderr and returns the exit status `nomi run` would exit with |
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
ship the reactor once and each script as a small `.nomic` file ([Compiled programs and `nomi-vm`](#compiled-programs-and-nomi-vm)). `nomi build-vm --reactor` writes the
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
  read (the text `nomi-vm` gives, [above](#compiled-programs-and-nomi-vm)), import `std::compiler` in a reactor
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

The reactor's exports are the same for every program: a program's own
function names travel as data, in `nomi_call`'s name, rather than as
per-function exports such as `guess(i32, i32) -> i32`. A project with Go
bindings and an `export fn` gets a reactor of its own, with the bound Go
packages linked in, which exports the same functions.

### A program's locale data

A program carries the CLDR data of the locales its `nomi.toml` lists under
`[i18n].locales` ([`[i18n]`](#i18n)), and nothing else. A program with no
`[i18n]` table, a script outside any project among them, carries every locale
CLDR has data for. A locale the program does not list still formats, as its
nearest listed parent or else as the source locale, so it is not an error at
run time; a ``Locale`…` `` literal of one is a compile error.
[`Locale.available`](/reference/locale/#localeavailable) has the lookup
rules. The grammatical genders and cases of each carried locale's nouns,
which a `noun`'s catalog entries may give (spec §18 *Nouns and agreement*),
are carried data too.

`nomi run`, `nomi test`, the REPL and a built program ([Executables](#executables)) read the same
data, so a program prints the same text under each. A built program carries
the data in its image, cut to the parts it reaches: one that formats numbers
and never lists items carries no list patterns, and one that reads no locale
data carries none. The collation data ([std::collation](/reference/collation/)) is the root
order, which a program that compares with a `Collator` carries once, and
each carried locale's tailoring. The Chinese and Korean tailorings are
carried only by a program that lists a locale whose collation chain reaches
them; one that lists no locales sorts Chinese and Korean in the root order.

`std::calendar` embeds Go's `time/tzdata` package (≈ 450 KB) so IANA zone
lookups (`America/New_York`, `Asia/Tokyo`, …) work on every host without
requiring system-provided tzdata.

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

A program shows a page in a locale only after
[`Locale.load`](/reference/locale/#localeload) has fetched its file, which
the calling task waits for. A file another build wrote, such as one a
browser cached from the last deploy, is refused: the image and its files
share a build id. Anywhere but a browser build `Locale.load` answers at
once.

## Go bindings

A program with `gopkg` bindings needs a Go toolchain. Pure-Nomi programs and tests need none.

- `nomi run`, `nomi check` and `nomi test` compile the bound packages, with Nomi's own Go module, into a wrapper they cache under the user cache directory (`nomi/builds/`, safe to delete). The first run of a project is slower; later runs reuse the wrapper until the project's Go code, its `go.mod` or `nomi` changes, and editing `.nomi` files never rebuilds it. A released or `go install`ed `nomi` compiles against Nomi's module at its own version, which Go fetches; one built from a clone compiles against the clone ([Installing Nomi](install.md)).
- With no `go` on `PATH`, `nomi` downloads the Go release it was built with and uses that ([The Go toolchain](#the-go-toolchain)).
- A bound package may be in a module from the Go module cache, a dependency's included.
- Go bindings build with `CGO_ENABLED=0`, so a `gopkg` naming a package that needs cgo, itself or through a package it imports, is a check error at the path: `` gopkg "example.com/app/wrap": needs cgo: it imports example.com/cdriver/internal/csql (...), whose files import "C", and Go bindings build with CGO_ENABLED=0 ``.

### The Go toolchain

A module's Go code (the `gopkg` bindings of spec §19), in the project or in a
dependency, is built into the program's runner with a Go toolchain: the `go`
on `PATH`; or, with none, the Go release `nomi` was built with, which `nomi`
downloads once from go.dev, checks against a SHA-256 compiled into `nomi`,
and keeps in its cache (`<user cache>/nomi/go/`). `NOMI_GO` names a `go` (or
a Go root) to use instead of both. The language server uses that toolchain
only once it is downloaded.

## When code cannot run yet

A program that reaches a function the compiler cannot compile yet fails before its first effect:

- `nomi run` prints an error at the code the compiler stopped at, with its source line and any hint, such as ``this call to `skip_odd` is not supported yet, so `fn evens` cannot run``, and exits 1. The last hint of every such error says the gap is in Nomi, not the program, and links the issue tracker.
- `nomi check` compiles the program the same way without running it and reports the same errors for everything `main` or a test reaches.
- `nomi test` runs every case it can and reports each case it cannot as `BLOCKED <file> :: <case> <path>:<line>:<col>: <message>`, with any hint indented on the line below. It appends `, K blocked` to its summary when K is not zero, and exits nonzero when anything failed or was blocked.
- A run stopped by something else (a Go crossing nothing binds, or a VM limit) prints `BLOCKED <file> [<function>] <reason>` lines instead.

Setting `NOMI_DEBUG_LOWERING=1` adds the compiler's own reason to each of these errors as a hint.

## Environment variables

These variables change what `nomi` does. Go's own module variables
(`GOPROXY`, `GONOPROXY`, `GOPRIVATE`, `GOSUMDB`, `GONOSUMDB`,
`GOMODCACHE`, `GOFLAGS`) work as [Dependencies](#dependencies) describes,
and `GOOS` and `GOARCH` pick a build's target as `--target` does.

| Variable | What it does |
|---|---|
| `NOMI_ENV` | `nomi test` sets it to `test` unless it is set ([The test environment](#the-test-environment)). Programs read it from `startup.env`. |
| `NOMI_COLOR` | `always` (or `1`, `true`, `yes`, `force`) colours diagnostics and test reports even into a pipe; `never` (or `0`, `false`, `no`) never colours them. Otherwise `NO_COLOR` or `TERM=dumb` turns colour off, `CLICOLOR_FORCE` turns it on, and colour goes only to a terminal. |
| `NOMI_DIAGNOSTICS` | `short` prints each diagnostic as one `path:line:col: message` line, for every command. |
| `NOMI_DEBUG_LOWERING` | For work on the compiler. Any value but `0` adds the compiler's own reason to each "not supported yet" error ([When code cannot run yet](#when-code-cannot-run-yet)). |
| `NOMI_GO` | The `go` command, or a Go root, that builds Go bindings, ahead of the `go` on `PATH` and the downloaded toolchain ([The Go toolchain](#the-go-toolchain)). |
| `NOMI_GO_DOWNLOAD_URL` | Where the managed Go toolchain is downloaded from, in place of `https://go.dev/dl/`. |
| `NOMI_TOOLCHAIN_ROOT` | The directory that holds downloaded Go toolchains, in place of `<user cache>/nomi/go`. |
| `NOMI_COMPILER_SOURCE` | For work on the compiler. A checkout of the compiler that the wrapper of a program with Go bindings builds against, in place of the compiler version `nomi` was built from. It is for working on the compiler. |
| `NOMI_FFIRUN_CACHE_ROOT` | The directory that holds Go-binding builds, in place of `<user cache>/nomi/builds`. The cache is safe to delete. |
| `NOMI_FFIRUN_CACHE_MAX_ENTRIES` | How many build directories that cache keeps before it removes the oldest (256 unless set); zero or less keeps every one. |
| `NOMI_STD_CACHE_ROOT` | The directory, in place of `<user cache>/nomi`, under which the language server writes the standard library's source for go-to-definition into std. |
| `NOMI_ARTIFACT_CACHE` | The directory, in place of `<user cache>/nomi/artifacts`, where `nomi` keeps what it compiles once and reads back in later runs: each program `nomi run`, `check`, `test` and `build` compiled ([The artifact cache](#the-artifact-cache)), and for a `nomi` built from source the standard library's interfaces and its lowering, which a release embeds instead. `off` keeps nothing. The cache is safe to delete. |
| `NOMI_DEBUG_ARTIFACT_CACHE` | `1` reports on stderr whether each command read a kept program, and why not, and whether it kept one. |
| `NOMI_DEBUG_STD_BUNDLE` | For work on the compiler. `1` reports on stderr why a cached standard library lowering or its cached interfaces were not used. |

## Editors and debugger

- Opinionated formatter: `nomi fmt [-w|-l|-d] <paths>`
- Check without running: `nomi check`, which also reports, at its source, any code the program reaches that type-checks but cannot be compiled to run yet
- Language server: `nomi-lsp` (diagnostics, hover, go-to-definition, completion, rename, code actions). It reports each `todo` and `dbg` as a warning. Its quick fixes include:
  - append `()` to a pipe stage that names a function without calling it;
  - insert `then` before a lambda written as a pipe stage;
  - insert `|> Display.to_string()` before a stage that takes a `String` when the piped value implements `Display`;
  - add braces around a `case` arm whose body continues on the next line;
  - put `|> Iter.take(todo)` before a terminal stage over an infinite iterator;
  - write missing functions, fields and `case` arms, each filled with `todo`, so the result checks.
- Debugger: `nomi dap`; see [Debugging](#debugging).
- Coding agents: `nomi mcp`; see [Coding agents](#coding-agents-nomi-mcp).

Each editor's setup is in its README:
[VS Code](../editors/vscode/README.md),
[Neovim](../editors/nvim/README.md) and
[Helix](../editors/helix/README.md). The Zed extension is
[`editors/zed`](../editors/zed/extension.toml).

### Coding agents: `nomi mcp`

`nomi mcp` serves the Model Context Protocol on stdin and stdout, so a
coding agent asks the compiler what an editor asks it. To add it to Claude
Code:

```sh
claude mcp add nomi -- nomi mcp
```

Other clients take the same command in their MCP settings, as a `stdio`
server whose command is `nomi` and whose argument is `mcp`. The server
starts in the client's working directory, and a relative path in a tool's
arguments is read from there.

| Tool | Arguments | Answers |
|---|---|---|
| `check` | `path` | `nomi check --format json`'s records, as lists: `diagnostics` (each with its span, `hints` and any `fix`), `files` and `summary`, plus `ok`. |
| `type_at` | `file`, `line`, `col` | The hover text the language server shows there: a type, a signature, a doc comment. |
| `definition` | `file`, `line`, `col` | The `locations` the name there is declared at. |
| `references` | `file`, `line`, `col` | Every use of the name there, its declaration included. |
| `search_std` | `query`, `limit` | The standard library's public functions that match, each as a program calls it (`String.split`), with its signature and first doc paragraph. |
| `run_tests` | `path`, `line` | `nomi test --format json`'s records: `tests` and `summary`, plus `ok`. |
| `format` | `source` or `file` | The formatted text and whether it changed. It never writes the file. |
| `explain` | `code` | What a diagnostic's `code` means and how to fix it; with no code, the codes. |

A position is a 1-based line and a 1-based byte column, as `check` reports
them, and a location is a `file` with a span in the same form. A tool that
fails answers its error as text with `isError` set, for the agent to read.

A `search_std` query with `->` is a signature, such as `String, String ->
List<String>`. Its parameters match in any order, a parameter with a
default may be left out, and a single capital letter stands for any type:
`List<T> -> Maybe<T>` finds `List.head`, and so does `List<Int> ->
Maybe<Int>`. A query type also matches an interface it implements, so
`List<T> -> Int` finds `Iter.count`, whose parameter is an `Iter<T>`, and
`List<String> -> String` finds `String.join`; a result matches the
interface a query asks for. Exact matches come first, then generic ones,
then ones through an impl. Any other query matches names and the text of
doc comments.

`check` and `run_tests` run `nomi check` and `nomi test` as child processes
of the same `nomi`, and download the modules a project needs as those
commands do. The other tools answer in process: the language server's
analysis for positions, which reads each file from the disk when a tool
names it, and the formatter.

### Debugging

`nomi dap` is a Debug Adapter Protocol server that editors start: VS Code's
extension, Neovim through nvim-dap, Zed's extension and Helix's
`:debug-start` templates. It debugs a file's `main`, its tests, or the one
test `--line` would select, as `nomi run` and `nomi test` run them.

- **Breakpoints.** Line breakpoints (one on a line with no statement lands
  on the next statement), each with an optional condition, hit count (`5`,
  `>= 5`, `% 10`) or log message with `{expression}` parts. Function
  breakpoints take a name as the call stack shows it (`transact`,
  `Account.deposit`, `Display.to_string` for every impl, or file-qualified),
  and stop at its first statement in every instance of a generic function.
- **Exception stops.** A trap, a failed assertion and an `Err` returned from
  `main` stop by default; an uncaught task failure, where it reaches the
  `Task.await` that awaits it, can be turned on.
- **Stepping.** Step over, into and out, step into target (pick one call on
  the line, lambdas passed to `Iter` adapters included), pause, and restart
  frame. Stepping passes through the standard library unless the launch sets
  `justMyCode` to false.
- **Step back and reverse continue.** Going back replays the program from
  its start, answering input, the clock, randomness and files from what the
  session recorded, so nothing is printed or read twice. A program that
  starts a task, a `concurrent` block, a supervisor or a channel cannot go
  back, and says why.
- **Inspecting.** The call stack shows a callback under the operation that
  called it (`main`, then `Iter.map`, then the lambda) and marks a frame a
  tail call replaced. Each frame's locals appear by their Nomi names, as a
  tree whose lists page. A **Pipeline** scope shows each stage of the `|>`
  chain the frame is in, and inside `io.capture` or `io.replay` a
  **Captured output** scope shows what the code printed into the capture.
- **Evaluating.** Watches, hovers and the debug console evaluate Nomi in the
  selected frame. An expression with an effect (printing, files, the clock)
  is refused unless it ends with `// allow effects`, and a hover never runs
  one. Each evaluation stops after 1,000,000 steps or 2 seconds.
- **Input and output.** With `console` set to `"integratedTerminal"` (the
  default for a program) the program reads and writes in the editor's
  terminal; with `"internalConsole"` (the default for tests) its output goes
  to the debug console and it reads end of input.

A launch configuration takes `mode` (`"run"` or `"test"`), `program`,
`line`, `args`, `cwd`, `env`, `console`, `justMyCode` and `stopOnEntry`;
the [VS Code README](../editors/vscode/README.md) has the table.

### Embedded files in the editor

Hover on an `embed` declaration's name, or on a use of it, shows its patterns, how many files and bytes it holds, and the first files with their sizes; hover on one pattern shows what that pattern matched. Go to definition on a pattern opens the file or folder it names, every match for a glob. A pattern that matches nothing, or that a rule refuses, is a diagnostic at the pattern. The server finds the files without reading them, and it rechecks the declaration when the declaring file changes, not when a file it embeds is added or removed.

### Message catalogs in the editor

The language server reports a catalog's errors in the catalog file, open or not, and rechecks when a catalog changes. Go to definition on a call of a message lands on its declaration, and go to implementations lists its catalog entries. Hover on a message shows its signature, its doc comment and its text in the source locale and in each listed and partial locale, naming the locale whose text a locale with no current translation shows. In a catalog it completes the file's message names in a table's header, the message's parameters after `$`, and a variant's keys: the plural categories the locale's rules select for the selector, the enum's variants, or the genders the module's nouns have; in a noun's entry, its `gender` and cases, the language's genders as its `gender`, and the cases after `case=`. Hover on a noun shows its gender and forms in each locale, and go to implementations lists its catalog entries. Its quick fixes add a listed locale's missing translation (the source text as `source`, and as `text` with every arm the locale needs), mark a stale translation current as `nomi messages accept` does, add the plural, enum or gender arms a translation lacks, and add each form a noun lacks that the locale's messages ask for, a copy of its nominative. An editor attaches the server to the TOML files under `[i18n].catalogs`; the server answers no other TOML file.
