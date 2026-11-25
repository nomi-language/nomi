# Nomi tooling reference

This reference covers the `nomi` command and the files around a program:
the commands and their flags, `nomi.toml`, packages and dependencies, the test
runner, what each kind of build writes, environment variables,
and the editor and debugger support. What the language does is in the
[specification](spec.md), and the standard library's types and functions are
in the [standard library reference](/reference/). To install `nomi`, see
[Installing Nomi](install.md).

## Commands

### The `nomi` command

`nomi` is the one tool. Each command has its section. `nomi <command>
--help`, `nomi <command> -h` and `nomi help <command>` print a command's
usage, and `nomi help` lists them all:

| Command | What it does |
|---|---|
| `nomi run <file>` | Run a program ([Running a program](#running-a-program)), in a package one of its entry points, or name the package's directory to run its one entry point ([Project mode](#project-mode)); `nomi <file>` runs a script ([Scripts](#scripts-nomi-file)), `--watch` runs it again on every change ([`--watch`](#running-again-on-every-change---watch)), and `--profile` reports where its time went ([`--profile`](#where-the-time-goes---profile)). At a workspace's root it lists the members to name ([Workspaces](#workspaces)). |
| `nomi run --web <file>` | Serve a browser build on localhost and rebuild it on a change ([A program in the browser](#a-program-in-the-browser)). |
| `nomi test [path]` | Run tests ([Running tests](#running-tests)), every member's at a workspace's root ([Workspaces](#workspaces)). |
| `nomi check <path>` | Check a program without running it, as text or JSON ([Checking](#checking-nomi-check)); at a workspace's root, with no path, every member ([Workspaces](#workspaces)). |
| `nomi effects [path]` | Print the effects a program's own code and its dependencies have, and with `--write` write the `[effects]` tables that allow them ([The effects a program has](#the-effects-a-program-has-nomi-effects)); each member's at a workspace's root ([Workspaces](#workspaces)). |
| `nomi fmt [-w\|-l\|-d\|-check] <paths>` | Format source ([Formatting](#formatting)); with a flag and no path at a workspace's root, every member ([Workspaces](#workspaces)). |
| `nomi` | The REPL ([The REPL](#the-repl)). |
| `nomi build <file>` | Write an executable from a file or a package's directory ([Executables](#executables)), a browser folder with `--web` ([A program in the browser](#a-program-in-the-browser)), or a `.nomic` file with `--no-vm` ([Compiled programs and `nomi-vm`](#compiled-programs-and-nomi-vm)). At a workspace's root it lists the members to name. |
| `nomi build-vm` | Write `nomi-vm`, or with `--reactor` the WASI reactor, with no program ([Compiled programs and `nomi-vm`](#compiled-programs-and-nomi-vm)). |
| `nomi new`, `nomi init` | Start a project ([Starting a project](#starting-a-project-nomi-new-and-nomi-init)), a member when under a workspace's root ([Workspaces](#workspaces)). |
| `nomi add`, `nomi remove`, `nomi update`, `nomi fetch`, `nomi outdated` | Change, fetch and list dependencies ([Add, update and remove dependencies](#add-update-and-remove-dependencies)); a workspace has one lock for every member ([Workspaces](#workspaces)). |
| `nomi tool <package> <name>` | Run a package's tool ([Tools](#tools-nomi-tool)). At a workspace's root it lists the members to run it in. |
| `nomi types <paths>` | Write TypeScript declarations of the JSON a backend's types cross as ([TypeScript types](#typescript-types-nomi-types)). At a workspace's root it lists the members to name. |
| `nomi phrases accept <locale> <phrase>` | Mark a translation current ([`[i18n]`](#i18n)). |
| `nomi editor install <editor>` | Set up Helix, Neovim, VS Code or Zed from the files `nomi` carries ([Setting up an editor](#setting-up-an-editor-nomi-editor)). |
| `nomi lsp` | The language server editors start ([The language server](#the-language-server)). |
| `nomi dap` | The debugger editors start ([Debugging](#debugging)). |
| `nomi mcp` | The compiler as tools for a coding agent ([Coding agents](#coding-agents-nomi-mcp)). |
| `nomi help [command]` | List the commands, or print one's usage. |
| `nomi version` | Print the version, as `nomi --version` does. |

### Running a program

One engine runs every Nomi program: the VM, under `nomi run`, `nomi test`, the REPL, a built executable and the tour's editors. The compiler parses and type-checks the program and compiles its functions to bytecode for the VM, not to native code.

#### File mode: `nomi run <file>`

`nomi run <file>` runs the file's top-level `fn main`, which need not be `pub`. A program whose functions a host calls (spec §7 *Exported Functions*) has no `main`, and is built with `nomi build --web` instead. For a file in a directory of its own, the project root is that directory, and every import resolves from it ([Project root discovery](#project-root-discovery)).

```tree
my_project/
├── app.nomi  # declares fn main; import math works
├── math.nomi  # import path math
└── models/
    └── user.nomi  # import path models::user
```

```console
$ nomi run app.nomi
```

File mode needs no config file, which suits scripts and small projects.

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

#### Where the time goes: `--profile`

`nomi run --profile <file> [args]` runs the program and, when it ends, prints
on standard error where its time went, by Nomi function:

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
what the functions it calls spent. A second table, after the functions,
names the lines the time was spent at. A lambda is `<lambda>` at its line and
column. A std function written in Nomi is marked `std`, and a function
the runtime implements, such as `Map.get` or `String.join`, is marked
`(host)`. `--profile=<file>` also writes the profile to `<file>` in
pprof's format, a gzipped `profile.proto`, which any pprof viewer reads:
`pprof -http=: <file>` shows it as a call graph and a flame graph.

The VM samples the program every millisecond, at its next call or loop
iteration, and when a host function returns, so a profile measures wall time:
a call that waits for input, a timer or a task is charged with its wait. A
function that ends in a tail call is replaced by the function it calls, as a
tail call does, and drops out of the stack, except that a call into a std
function (an `Iter` pipeline at the end of a function) keeps the caller under
the std function. A caller is shown by its function, not the line it called
from. The garbage collector runs on other threads beside the program, so its
time is a total rather than a share. A profiled run is a few percent slower;
one that is not profiled pays nothing. An executable `nomi build` wrote is
profiled with `NOMI_PROFILE` ([Environment variables](#environment-variables)).

### Scripts: `nomi <file>`

`nomi <file> [args]` is `nomi run <file> [args]`. Together with a shebang line
(spec §2 *Shebang line*) it lets a file run as a command:

```console
$ cat hi.nomi
#!/usr/bin/env nomi
import std::io

fn main()
    io.print("hi")
end
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
A `.nomi` script inside a package (a `nomi.toml` above it) is held to that
package's entry points, so it must be listed in `entry_points` unless it is
one of the package's tools under `tools/`.

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
directory or a package around the script never changes what a command on
`PATH` does. It imports `.nomi` files from its own directory and below, as
`import greeting` reads `greeting.nomi` beside it, and nothing above it. Its
module name is the file name, so a `greet.nomi` beside `greet` is a different
file that `import greet` would read. A script in a package's subdirectory is
not one of the package's entries and needs no `entry_points` line; one beside
the package's `nomi.toml` is, and is listed by its bare name. `nomi check` and
`nomi test` on a directory still look only at `.nomi` files.

### The REPL

The REPL compiles each input as one more file of the session's program, which sees the declarations and top-level bindings of the inputs before it, and runs it on one live VM. An earlier input is never checked, compiled or run again, so an input takes as long in a long session as in a new one: a later input reads the value an earlier binding computed, and a closure keeps the value it captured. Redefining a function affects later inputs only, and so does redeclaring a type with a different declaration: earlier values, functions and impls keep the old type, and a later input that names the type gets the new one, which starts with no impls. When the two meet in an error, each is named by the input that declared it (`expected Point (input 1), got Point (input 5)`). Redeclaring a type exactly as it is keeps it, and redefining an impl replaces the old one everywhere. The inputs of a session are one file to `private` and `opaque`: a later input calls an earlier input's `private` function, reads its `private` once and builds, reads and destructures its opaque types. A position the REPL shows, in an error, a `dbg`, a `todo` or a failed assertion, is a line of the input it was typed in, counted from that input's first line. An input that fails to check, faults or is blocked prints its error and changes nothing.

An input may be any declaration a file may hold: `fn`, `host fn`, a type, an interface, an `impl` or `derive`, an `import`, `once`, `embed`, `test` and `group`, and `phrase` and `noun` declarations. A `phrase`, `noun` or `embed` is redefined as a function is: an earlier function keeps the one it was written against. The REPL is no project, so its source locale is `en` and it reads no catalogs, as a file run outside a project does. An input continues onto the next line while a `{`, `(` or `[` is open, while it ends in an operator, and while a backtick or `"""` string is open.

### Checking: `nomi check`

`nomi check <path>` type-checks and compiles a program without running it.
Code the program reaches that the compiler cannot lower is the error at its
source that `nomi run` would stop with and `nomi test` would report as
`BLOCKED`. A test file is checked as `nomi test` loads it, each case
lowered and none run, and a directory's test files, and a package's beside
its entries, are checked with the rest.

In a directory without `entry_points`, a file that another file there
imports is checked through that importer, as `nomi run` loads it, and
listed as `ok <file> (through <importer>)`; a file nothing there imports
is checked on its own. A failed check of a directory prints `FAIL <file>` for each file that
holds errors, a file the program imports or a phrase catalog, with
its errors below it, and an error two programs share once.

A file that does not parse reports every syntax error it holds in one
run: the first of each declaration, and the first of each statement inside
a body. A statement after a damaged one is read on its own, so one mistake
gives one error. Type errors are reported once the file parses.

`--hints` also reports the hints the editor shows: a `value as Type` whose
value has the type without it (code `redundant-ascription`), and an operand
in a field's new value that reads the field's current value by its path,
which `..` writes (code `restated-current-value`), and a call whose Option
the value's refinement answers plainly, `List.first(names)` for a
NonEmptyList `names`, whose `NonEmptyList.first(names)` is the element
(code `plain-counterpart`). Each prints as `hint:`
and fails no file; without the flag `nomi check` says nothing of them.

`--format short` prints each error as one `path:line:col: message` line
instead of with its source lines (`--format full`, the default), as
`NOMI_DIAGNOSTICS=short` does for every command.

`--format json` writes JSON Lines on stdout for a tool or an agent to read,
as `nomi test --format json` does: a record per error, a record per file
checked, and a summary last.

```json
{"type":"diagnostic","file":"/abs/main.nomi","line":5,"col":12,"end_line":5,"end_col":15,"start_byte":54,"end_byte":57,"severity":"error","message":"List<Int> has no field 'len'","hints":["Nomi has no method calls: write `List.length(xs)`"],"fix":{"title":"Call `List.length`","edits":[{"line":5,"col":9,"end_line":5,"end_col":9,"start_byte":51,"end_byte":51,"text":"List.length("},{"line":5,"col":11,"end_line":5,"end_col":16,"start_byte":53,"end_byte":58,"text":""}]}}
{"type":"file","file":"/abs/main.nomi","status":"failed"}
{"type":"summary","files":1,"failed":1,"diagnostics":1}
```

- `file` is absolute, or `std/<module>.nomi` for an error in the standard
  library. Lines and columns are 1-based, a column counts bytes, and an end
  is one past the last byte; `start_byte` and `end_byte` are the same
  points as 0-based byte offsets into the file. A span that is a point has
  no end.
- `severity` is `error`, which stops the program, or `warning`, which
  fails no file and is not counted in the summary: an `embed` pattern
  whose folder does not exist yet (code `embed-missing`), a string that
  writes `$name` where a value named `name` is in scope, with the fix that
  writes `${name}` (code `dollar-interpolation`), or a type
  parameter the style guide writes as an interface value, with the fix
  that does (code `interface-value-parameter`, from `nomi check` only), or
  `hint`, under `--hints` only, which fails no file either.
  `code` names a diagnostic's class when it has one (`unused-import`,
  `undefined-variable`, `missing-field`); `nomi mcp`'s `explain` tool
  lists every code and says what each means. `args`, when present, are
  what the message names that a tool acts on, in the order the code's
  explanation gives: an `undefined-variable`'s name.
- `hints` are the `help:` lines, and `related` the other places the error
  names, each with its own `file`, span and `message`: for a call
  `nomi.toml`'s lists refuse in a dependency's file, each place in the
  project's own code that leads there ([The errors](#the-errors)).
- `fix`, when present, is the rewrite the error knows: its `edits` replace
  each span's text with `text` (an empty span inserts), applied together.
  Source written as another language writes it (`let x = 1`, `xs.len()`,
  `match`, `Point{x: 1}`, `nil`) carries one; the editor offers the same
  edits as a quick fix. A fix whose edits are in another file names it as
  its `file`: a call `nomi.toml`'s lists do not allow
  ([The errors](#the-errors)) is fixed in `nomi.toml`.
- A `"type":"file"` record's `status` is `ok` or `failed`; a file checked
  through an importer that loads it names it as `through`.

The exit status is the same as in text mode.

### The effects a program has: `nomi effects`

`nomi effects [path]` prints the effects the programs `nomi check
path` checks have, whether or not `nomi.toml` has
`[effects]` tables ([Effects lists](#effects-lists)). The path
is a project directory or a file, the current directory by default. It
counts what the check counts: `main`, the boot, the exported functions and
the public functions ([When it is checked](#when-it-is-checked)), then
the test cases.

```console
self: github.com/nomi-language/nomi/lib/sql/examples/shop
  std            std::io
  std_in_tests   std::instant

deps
  std            std::channels, std::fs, std::instant, std::random, std::timer,
                 std::wasm
  std_in_tests   (none)
  packages
    migrate
      no effects
    sqlite
      std::channels, std::fs, std::instant, std::random, std::timer, std::wasm
    sqlkit
      std::channels, std::fs (tests only)

nomi.toml has no [effects.self] or [effects.deps] table; nomi effects --write writes these
```

The report is the tightest tables the check passes, in their own words.
Under `self`, the project's own code: `std` holds the effects `main`, the
boot, the exported functions and the public functions have in the
project's own files, and
`std_in_tests` those only the tests have beyond that. Under `deps`, the
code of the packages it depends on: the same lists over every
dependency's files, and under `packages` each dependency the program's
code comes from, with the effects its own files have; `(tests only)`
marks those only the tests have. When `nomi.toml` has tables, the report
ends with how they differ, one name per line: `missing` for an effect the
program has that a list lacks, and `unused` for a name a list holds
that nothing there needs. An unused name is not an error. The last line
says whether `nomi check` passes the tables, after any problem it finds in
the tables themselves.

`--write` writes those tables into `nomi.toml`: `[effects.self]`
with `std`, then `std_in_tests` when it holds a name; `[effects.deps]`
with `std`, then `std_in_tests` when it holds a name; and under
`[effects.deps.packages]` an entry for each package the project
needs whose Nomi files the program loads, keyed by its name in the program
([Dependencies](#dependencies)), `[]` for one with no
effects. When the file has `[effects.self.modules]`, or
`entries_required` under `[effects.self]`, it writes an entry there
for each module of the project whose files the programs load too; with
neither it writes no such table, since an entry for a module of the
project is a choice the file makes. In tables the file already has, it
changes only what differs, and keeps the file's order, comments and
blank lines:

- A comment on a key's line, or directly above it with no blank line
  between, belongs to that key. It stays while the key stays and goes when
  the key goes: the entry of a module or package the program no longer loads, or a
  list other than `std` that no longer holds a name.
- A list keeps the names it had, in their order, and new names follow. In a
  list written one name per line, each name keeps its own comments. A list
  laid out any other way is written again whole, and a comment inside it
  goes.
- A comment that belongs to no key stays where it is, and blank lines
  between keys that both stay are kept.
- A key a table lacks goes after the keys it has, with no comment.
  `entries_required` stays as the file has it.

Every line outside the four tables stays as it was. A table the file
lacks goes after the table before it, or before the first one the file
has, or at the end of the file when it has none. A file with CRLF line
endings gets them on the lines it writes. If the edited file would not
read back as exactly the tables, it writes nothing and says to edit the
tables by hand. It prints the tables as written, and when the file had
any, each name it added and removed, so a wider table never goes
unnoticed. A second run with no change to the code writes nothing and
says the tables are current. A program a dependency's own `nomi.toml`
holds to fewer effects than its code has is still refused, and the report
names each such call, since only that package's table can allow it.

`--check` holds `nomi.toml`'s tables to the tightest ones, for CI. It
prints only how the tables differ, the same `missing` and `unused` lines
and the line about `nomi check`, and exits 1 when there is any. A problem
`nomi check` finds in the tables themselves, such as a name that is no std
module or a module or package `entries_required` finds with no entry, is printed
as `nomi check` prints it, the last line says `nomi check fails on the
problem above` instead of whether it passes, and `--check` exits 1. A table
with a name the program needs and lacks fails, as `nomi check` does, and
so does a table with a name nothing has, so a table cannot grow
without the change that needs it. What the file leaves out is a choice
and not a difference: a table it does not have, `[effects.deps]`'
`std` when the table leaves it out, and the entry of a module of the
project or a dependency it gives none, unless `entries_required` is set in
the table above that entry. Exact tables print one line and
exit 0. A project with no `[effects]` tables has nothing to hold,
which `--check` says, and exits 0. With `--format json` it prints the
object below and exits the same way.

```console
nomi.toml's [effects] tables differ:
  unused   self.std  std::net
nomi check passes; an unused name allows an effect nothing here has
```

`--why <name>` prints each call that has an effect (`--why
std::http`), or each call a package's or module's files make, a
dependency by its name (`--why pg`) or a module of the project
as its key under `[effects.self.modules]` is written (`--why
pricing`), at the call, with the chain a refusal's help gives:

```console
1 call has the effect std::http:
  rates.nomi:8:10 in fetch
    reached from `main`; `main` calls `today`
```

A name the program does not have is an error that lists the effects it has.

`--tree` prints, for `main` and each other root the report counts, the
calls from the root down to each effect. Only the branches that lead to
one are shown:

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

A function reached by several paths is expanded once and named with
`(above)` after that. A chain of functions that each lead on to one thing
is one line, joined with arrows. Each call with an effect ends with the
std module an effects list names it by, and, when `nomi.toml` has tables,
the list that refuses it. A dependency's function names its package, and
a step that is not a plain call says how it is reached: `as a value`,
`through an interface`, `displays a value`, `inspects a value`, `compares
or hashes a value`, `a once's initializer`. The functions a function
reaches that have no effects are counted on one line under it. The tests
are one line a test file, with how many there are and the effects of
those that have any. `--tests` prints the tree of each test that has
effects instead and counts the rest, and so does a path that
names a file.

`--tree --format mermaid` writes the same graph as a Mermaid flowchart, to
paste into documentation: each function once as a node, an edge to each
function and call with an effect it reaches, labelled when it is not a
plain call, those calls in one box per std module with a refused one
outlined in red, and one node for the functions under each function that
have no effects. Without `--tests`, a test file is one node with an edge
to each effect its tests have.

A program that does not check prints the check's errors and exits 1, as
`nomi check` does.

`--format json` prints the same facts as one JSON object:

```json
{
  "project": "app",
  "self": {"std": ["std::fs", "std::io"], "std_in_tests": ["std::process"]},
  "deps": {
    "std": ["std::fs", "std::http"],
    "std_in_tests": [],
    "packages": [
      {"path": "fetch", "std": ["std::http"], "std_tests_only": []},
      {"path": "pg", "std": ["std::fs"], "std_tests_only": []}
    ]
  },
  "table": {"changes": [{"change": "add", "list": "deps.std", "name": "std::http"}], "refused": 1, "problems": []},
  "written": false,
  "declared_less": []
}
```

- `self` is the tightest `[effects.self]`: its `std` and `std_in_tests`
  lists.
- `deps` is the tightest `[effects.deps]`: its two lists, and in
  `packages` each dependency with its name in the program as `path`, `std`
  for every std module its files reach and `std_tests_only` for those only
  the tests reach. An entry `--write` writes is a package's `std`.
- `table` is null when `nomi.toml` has neither table. Its `changes` are
  what `--write` adds to and removes from it: `change` is `add` or
  `remove`; `list` is the key's path below `[effects]`
  (`self.std`, `self.std_in_tests`, `deps.std`, `deps.std_in_tests`, or
  `self.modules` with the entry's key as `module`, or `deps.packages` with
  the entry's key as `package`), or
  `self` or `deps` for a whole table; and `name` is the name, empty for a
  table, an entry with no names, or `deps.std` with no names. `refused` is how many calls `nomi check` refuses under the
  tables, and `problems` each problem `nomi check` finds in the tables
  themselves, in the short form it prints.
- `written` says whether `--write` wrote `nomi.toml`.
- `declared_less` is each `package` and `name` a dependency's own
  `[effects.self]` does not allow its own code.

With `--why`, the object is the `name` asked about and its `calls`, each
with its `name`, `package` (the dependency whose files hold it, or the
project), `site`, `callee`, `tests` and `reach`, the chain in words.

### Tools: `nomi tool`

`nomi tool <package> <name> [args]` runs a package's tool: the program
`tools/<name>.nomi` at the package's root, with the arguments after its
name. A tool is an entry point without being listed in `entry_points`. It
is an ordinary program, run in the current directory with the same access to files, the network and output
that `nomi run` gives a program, and runs only when someone runs it.
`<package>` is the current package's own `[package].name` or the name its
`nomi.toml`'s `[dependencies]` gives a dependency. A dependency's tool runs
in the current project's build: its packages at the versions the project
selects, and the project's `dir` entries. The tool's own `[dependencies]` say which packages it needs,
and its `dir` entries are not read. The tool runs with `NOMI_TOOL_PACKAGE`
in its `startup.env` set to `<package>`, the name the project gives its
package. It reads its configuration from the project's `nomi.toml` in the
table of that name, and code it generates imports the package's files by
it: a project with `db = { repo = "github.com/nomi-language/nomi",
subdir = "lib/sql/sqlkit", ... }` writes
`[db.database.main]`, runs `nomi tool db generate`, and gets code that
imports `db::sqlkit` and `db::runtime`. It is how a package ships a code
generator, such as sqlkit's `nomi tool sqlkit generate`
([Databases](/databases/)), which writes ordinary `.nomi` files a program
commits. A generator passes its text through `compiler.format`
([std::compiler](/reference/compiler/)) before writing it, so `nomi fmt
-check` finds the files it writes formatted.

### TypeScript types: `nomi types`

`nomi types <paths> [-o <file>]` writes TypeScript declarations for the JSON
a Nomi backend's types cross as, so a frontend written in TypeScript shares
the shapes of its API. Each path is a `.nomi` file or a directory, which
names every `.nomi` file under it but those of another package (a directory
with its own `nomi.toml`). The output declares each struct, enum, distinct
type and `typealias` those files declare that crosses as JSON (it derives
`ToJson` or `FromJson`, or implements `ToJson` by hand) and is public or
internal to its package, so an application, which writes no `pub`, gets its
types. It also declares every type those reach in any file, std types and
`private` ones included, each once, in the order they are reached. A
`private` type no other type reaches is not written. The output goes to
stdout, or to the file `-o` names. The files are
checked as `nomi check` checks their project: in each of its programs that
loads one of them, so a file that reads an application field is checked
under the entry whose `fn boot` returns the application. An error stops
the command with its diagnostics. A path whose files declare no such type
is an error. The command writes types only: no client, fetch wrapper or
validation.

```console
$ nomi types models.nomi api -o ../web/src/api-types.ts
```

A type maps to the JSON its `ToJson` writes, and a type that derives
`FromJson` and has no `ToJson`, such as a request body, to the JSON its
`FromJson` reads:

| Nomi | TypeScript |
|---|---|
| `Int`, `Float` | `number` |
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
| a generic type | a generic `interface` or `type`: `Page<T>`, referred to as `Page<User>`. A field of type `T` is optional (`value?: T`) when some use of the type puts a `Presence` at `T`, in a type with `ToJson` |
| a type whose `ToJson` is written by hand, in any file | `unknown`, with a comment that says so |

An `Int` is a `number` because `JSON.parse` reads every JSON number as one,
so an Int past 2^53 loses precision in JavaScript. An app that sends such
Ints, such as 64-bit IDs, should send them as `String`s. A derived
`FromJson` reads a missing key as `None` for an `Option` field, as the
default for a field with one, and as `Absent` for a `Presence` field, and a
derived `ToJson` leaves out the key of an `Absent` field. So a `Presence`
field is optional in every declaration, and an `Option` field or a field
with a default is optional in a type that only `FromJson` reads. A type
with `ToJson` is declared as its `ToJson` writes it, so there an `Option`
field is always present, even when the type derives `FromJson` too. A type
only `FromJson` reads says so in its JSDoc comment.

A generic type is declared once for every use, so a field whose type is a
type parameter cannot follow the argument. When some use the command
reaches puts a `Presence` at that parameter, directly (`Slot<Presence<String>>`)
or through another generic type, the property is optional in a type with
`ToJson`, which leaves the key out for `Absent`. In a type only `FromJson`
reads it stays required: each use's `FromJson` takes the key, and a
`Presence` there reads `null` as `Absent`.

A declaration takes its Nomi name. When two files each declare a type of one
name, each is named with its file's name before it (`ModelsUser`,
`OrdersUser`), and with its directories' when the files share a name. A
Nomi type named `Record` is named with its file's too, since the
declarations use TypeScript's own `Record`. A `///` comment on a type
becomes its JSDoc comment.

### The artifact cache

`nomi run`, `nomi check`, `nomi test` and `nomi build` keep each program
they check and compile in the artifact cache, `<user cache>/nomi/artifacts`
(`~/Library/Caches` on macOS, `~/.cache` on Linux, `%LocalAppData%` on
Windows), or `NOMI_ARTIFACT_CACHE`. The next command over the same files
reads it and checks and compiles nothing: an unchanged program of 50,000
lines runs in about 0.06 s instead of 2 s. A program is kept only when it
checks with no error and every part of it a run or its tests reach can
run, so a command that reads one prints exactly what it would print
otherwise.

A program is read again only while the files its build can read are as
they were: the `.nomi` files it loads, `nomi.toml` and the other `.toml`
files (phrase catalogs) and `nomi.lock` in its project and its
dependencies, the files under a folder an `embed` pattern names, and any
`nomi.toml`, `main.nomi`, `nomi-workspace.toml` or `nomi.lock` above the
project. A `.nomi` file the
program does not load changes it only by being added or removed, since
that can change what an import names. Any such change, or a new `nomi`,
builds the program again, and after an edit to the `.nomi` files alone it
checks and compiles only the files the edit reaches. An edit to another
test file of the same project leaves a test file's program as it was kept.
Other files the project holds, such as what a run writes, change nothing.
A file saved with the same text is not a change. The settings in `NOMI_`
variables are part of what is kept, so a command with other settings
builds again.

`std` holds the standard library's artifacts, `programs` the programs',
and `wasm` the machine code of each WebAssembly module a program compiled
with `wasm.compile`, one directory a module by the SHA-256 of its bytes, so
the next process reads the code back instead of compiling the module
again. A module nothing has compiled for two weeks is removed by a later
compile, which looks at most once a day. A program nothing has read for
five days is removed by the next command that keeps one, which looks at
most once an hour. A build of `nomi` keeps
its own standard library artifacts, which go a day after the last command
of that build read them, when another build writes its own. The cache is
safe to delete. `NOMI_ARTIFACT_CACHE=off` keeps and reads nothing, and
`NOMI_DEBUG_ARTIFACT_CACHE=1` says on stderr, for each command, whether it
read a kept program and why not.

## Projects

### Starting a project: `nomi new` and `nomi init`

```console
$ nomi new hello
wrote hello/nomi.toml, hello/main.nomi and hello/AGENTS.md
next: cd hello && nomi run main.nomi
```

`nomi new <name> [dir]` starts a package in `dir` (a new directory named
`name` when left out). It writes a `nomi.toml` with that `name`, the
release of the `nomi` that wrote it as `nomi` (left out by a `nomi` built
from source), and `entry_points = ["main"]`, a `main.nomi` that prints
hello, and an
`AGENTS.md` for coding agents that names the commands and links the
[cheat sheet](https://nomi-lang.org/cheatsheet.md), keeping a `main.nomi`
or `AGENTS.md` already there. A name is lowercase letters, digits and `_`,
not starting with a digit, the form an import path starts with
(`hello_world`, not `hello-world`), and not `std`. A package does not say
where it is published: a project that depends on it names its repository
([Distribution](#distribution)). So `nomi new` refuses a repository as its
argument:

```console
$ nomi new github.com/you/hello
nomi new: "github.com/you/hello" is not a package name: a name is lowercase letters, digits and _, not starting with a digit, such as hello_world; a package names no repository: a project that depends on it names the repository it is published in
```

`nomi new` refuses a name it could not write in `nomi.toml`, and a
directory with a `nomi.toml`, which is a project already, before it writes
anything. It prints what it wrote and the next command.

`nomi init <name>` makes the current directory a package. It writes its
`nomi.toml`, as `nomi new` writes one, and never replaces it. With no name
it is an error that asks for one. `entry_points` lists the package-relative
name of every file under the directory with a top-level `fn main` (tools,
test files, hidden directories and directories with their own `nomi.toml`
are skipped), or `["main"]` when there is none and there are no tools. It
writes no source file, refuses a directory that is a project already, and
prints what it wrote.

### `nomi.toml`

`nomi.toml` at a package's root is the one file a package writes about
itself: its name, its entry points and its dependencies. `nomi.lock`
beside it locks the version, commit and hash of each dependency fetched
for it ([`nomi.lock`](#nomilock)). `nomi` writes both (`nomi new`,
`nomi init`, `nomi add`, `nomi remove`, `nomi update`, `nomi fetch`), and
a project commits both.

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

- `[package].name` is the package's own name: what its own files may start
  an import path with (`import shop::orders`), and the name `nomi add`
  gives the package by default in a project that adds it.
- `[package].nomi` is the lowest `nomi` release the package's source needs
  ([The nomi a package needs](#the-nomi-a-package-needs)). It is optional.
- `[package].entry_points` lists the files with a `fn main` other than the
  package's tools; every file under `tools/` is an entry point without
  being listed ([Project mode](#project-mode)).
- `[dependencies]` names each package the package's files import. The key is
  the name its imports use: `sqlite` above makes `import sqlite::driver`
  name `driver.nomi` at that package's root (spec §14 *Modules and packages*).
  A key is lowercase letters, digits and `_`, and may not be `std` or the
  package's own name, and two keys may not name one package.
- An entry holds `repo` and `version`, or `repo` and `commit`, with
  `subdir` for a package that is not at the repository's root; or `dir`, a
  directory relative to this `nomi.toml`'s that holds the package; or all
  of them ([Dependencies](#dependencies)).
- A package that needs a repository it reaches through a dependency at a
  higher version than the dependency asks for names it in its own
  `[dependencies]`; an entry no file imports is allowed
  ([Dependencies](#dependencies)).

Every key `nomi.toml` accepts:

| Table | Key | What it holds |
|---|---|---|
| `[package]` | `name` | The package's own name, which its own files' import paths may start with and which `nomi add` gives it by default. |
| `[package]` | `nomi` | The lowest `nomi` release the package's source needs ([The nomi a package needs](#the-nomi-a-package-needs)). |
| `[package]` | `entry_points` | The files with a `fn main`, other than tools ([Project mode](#project-mode)). |
| `[dependencies]` | a dependency's name, as its imports write it | `repo`, `subdir` and `version` or `commit`, `dir`, or all of them ([Dependencies](#dependencies)). |
| `[i18n]` | `locales` | The locales every phrase is translated into, and whose CLDR data the program carries ([`[i18n]`](#i18n)). |
| `[i18n]` | `partial_locales` | Locales whose catalogs may leave a phrase out. |
| `[i18n]` | `source_locale` | The language of `phrase` declarations' own text; `en` when left out. |
| `[i18n]` | `catalogs` | The catalog directory under the package root; `locales` when left out. |
| `[i18n]` | `web_embedded` | The locales a browser build puts in `app.wasm` ([Locales in the browser](#locales-in-the-browser)). |
| `[money]` | `custom_currencies` | The custom currencies the package's literals may write ([`[money]`](#money)). |
| `[effects.self]` | `std` | The effects this package's own code may have, by std module ([Effects lists](#effects-lists)). |
| `[effects.self]` | `std_in_tests` | More effects that only its tests may have. |
| `[effects.self]` | `entries_required` | Whether every module of the project must have an entry. |
| `[effects.self.modules]` | a module of the project, as its own code imports it | The effects that module's own files may have. |
| `[effects.deps]` | `std` | The most effects any dependency may have. |
| `[effects.deps]` | `std_in_tests` | More effects dependencies may have in tests only. |
| `[effects.deps]` | `entries_required` | Whether this file must hold an entry for every dependency. A dependency's own nomi.toml needs none. |
| `[effects.deps.packages]` | a dependency's name, as `[dependencies]` gives it | The effects that dependency's own files may have. |

Any other table or key is an error at its line, naming the nearest key when one is close in spelling. In the `[effects]` tables it says what each key holds instead, and a key directly under `[effects]` is an error that names the two tables. The exception is a top-level table named for the package itself or by a key of its `[dependencies]`, which belongs to that package's tools: `nomi tool` runs them under that name, and they read the table by it ([Tools](#tools-nomi-tool)). A dependency's own `[package].name` names no table unless the project's key for it is the same.

A `nomi.toml` with an error, the project's or a dependency's, stops `nomi check`, `run`, `test`, `build` and the language server before they check anything, with that one error, whose code is `manifest`. Imports by the package's name and by its dependencies' names resolve through it, so the files' own errors would only follow from it.

### Effects lists

A function is pure, or it has effects. A project can list the effects its program may have, in two tables in `nomi.toml`, each with a table of entries under it, and `nomi check`, `run`, `test` and `build` refuse a program that has more, before it runs. `[effects.self]` holds the calls written in the project's own files, and an entry under `[effects.self.modules]` narrows that for one of its modules. `[effects.deps]` holds the calls written in the files of the packages it depends on, directly or through other packages, and an entry under `[effects.deps.packages]` narrows that for one dependency. Code in the project that has an effect it was not meant to, such as a helper that starts making network requests, is a compile error at the call. So is a dependency that has a new effect after an upgrade, with the package named. A table the project leaves out holds nothing, so a project with neither is held to nothing. [Effect classes](#effect-classes) are a different thing: what a build target lacks, where these lists are what a project allows.

**Starting.** Run `nomi effects --write` in the project ([The effects a program has](#the-effects-a-program-has-nomi-effects)). It writes the tightest tables the program passes, from the effects it has today. Read what it wrote: each name is something the program does, and a name you did not expect is a call to look at. To keep a module from obtaining access to anything, give it an empty entry under `[effects.self.modules]`, such as `"pricing" = []`: a call in its files that has an effect a list names is then an error, and the module acts only through the handles it is passed. Once the file has that table, `--write` writes an entry for each of the project's modules. Later, `nomi effects` says how the tables and the program differ, and `--why <name>` says which calls have an effect. In the editor, a call the tables do not allow has a quick fix that adds its name to the key the help names, in `nomi.toml`, keeping the file's comments, a hover on a name in `nomi.toml` shows the calls that need it, and a name nothing needs is a hint with a fix that takes it out. A hover on a call, or on a function of the program and its calls, says the effects it has and whether the tables allow them, and the editor draws bold each call that can act ([The language server](#the-language-server)).

These tables are for a project whose `[package].name` is `app` and whose `[dependencies]` names four packages, `pricing`, `reports`, `fetch` and `pg`, and whose own code is in `main.nomi`, `totals.nomi` and the test file `main_test.nomi`:

```toml
[effects.self]
# the effects this package's own code may have
std = ["std::io", "std::fs"]
# added to std for its tests
std_in_tests = ["std::process"]

[effects.self.modules]
# prints and reads files
"main" = ["std::io", "std::fs"]
# computes, and has no effects
"totals" = []
# the test file, which starts a process
"main_test" = ["std::process"]

[effects.deps]
# every dependency must have an entry below
entries_required = true
# no dependency may have more effects than this
std = ["std::fs", "std::http"]

[effects.deps.packages]
# computes, and has no effects
"pricing" = []
# reads and writes files
"reports" = ["std::fs"]
# the one dependency that may make HTTP requests
"fetch" = ["std::http"]
# reads its configuration file
"pg" = ["std::fs"]
```

The project's own code may print and use files, and its tests may also start processes. Each of its modules has an entry, which narrows that for the code in its own files: `main` prints and reads files, `totals` has no effects, and the test file `main_test` starts a process. No dependency may have more effects than files and HTTP requests. Each dependency has an entry, which narrows that for the code in its own files: `pricing` has no effects, `reports` uses files, `fetch` is the only one that may use the network, and `pg` reads its configuration file. No code in the program may read the clock or open a socket, since no list names `std::instant` or `std::net`.

Suppose an upgrade of `reports` starts sending a request each time it saves a report. `nomi run main.nomi` prints this and exits 1 before `main` starts, and `nomi check` and `nomi build` refuse the program in the same words:

```console
error: reports has the effect std::http, and its entry under [effects.deps.packages] allows it only std::fs
 --> /work/reports/summary.nomi:9:9
  |
9 |     _ = http.get(Url"https://stats.example.com/saved")
  |         ^^^^
  = help: reached from `main`; `main` calls `save`
  = help: add "std::http" to "reports" under [effects.deps.packages] in nomi.toml, or remove the call
  = note: your code has the effect std::http in reports here: `main` calls `save`: main.nomi:14:5
```

**Limits.** A name is a whole std module: `std::fs` allows every path and `std::http` every host, and no list can name a file or a host. The check counts every call with an effect the program can reach, whether or not a run takes it ([Why a call counts though it may never run](#why-a-call-counts-though-it-may-never-run)). A handle the program opens and passes to a dependency is the program's call, and the dependency's use of it is not counted, an interface value std made among them, so a `Dir` gives a dependency one directory and no more ([Authority by handle](#authority-by-handle)).

#### The keys

`[effects.self]` holds the calls written in the project's own files:

- `std` is the effects they may have, by std module. The table with no `std` allows no std call.
- `std_in_tests` is added to `std` for the effects the test cases have.
- `entries_required = true` makes every module of the project whose files the program loads have an entry under `[effects.self.modules]`. A module with none is an error at that key, one per module, whose `fix` in `nomi check --format json` and quick fix in the editor write the entry with the effects the module has. The key is false when left out.

`[effects.self.modules]` gives one of the project's modules an entry, for the calls written in that module's files: a call there must be in its entry, and in `[effects.self]`' `std`, with `std_in_tests` for the effects the test cases have. An empty entry allows no effects: the module obtains access to nothing, and acts only through the handles it is passed. An entry holds the module's calls from every root, tests included. A module with no entry is held by `[effects.self]` alone. The table is under `[effects.self]`, so a file with this table and no `std` there allows no std call.

A key is a module of the project as its own code imports it: the path of its file from the project's root, without `.nomi`, with `::` between directories (spec §14 *Modules and packages*). `pricing.nomi` is `pricing`, `reports/daily.nomi` is `reports::daily`, a test file `pricing_test.nomi` is `pricing_test`, and a nested testing module `fs/testing.nomi` is `fs::testing`. A test file is a module of its own and has its own entry. A file under a directory that holds its own `nomi.toml` is another package's, and no key here. A key that names no module of the project is an error at the key, naming the nearest.

`[effects.deps]` holds the calls written in the files of every package the program's code comes from other than the project:

- `std` is the most effects any dependency may have: a call in a dependency's files must be in it. Left out, there is no such ceiling, and only the entries hold. `std = []` lets no dependency have any effect.
- `std_in_tests` is added to `std` for the effects the test cases have. It is set only beside `std`.
- `entries_required = true` makes every dependency whose Nomi files the program loads have an entry under `[effects.deps.packages]`, or an `[effects.self]` in its own `nomi.toml`. A dependency with neither is an error at that key, one per package, whose `fix` in `nomi check --format json` and quick fix in the editor write the entry with the effects the package has. The packages counted are those whose Nomi files the program loads, whether or not it runs their code, and not every package the project needs. The key is false when left out.

`[effects.deps.packages]` gives a dependency an entry, keyed by its name in the program ([Dependencies](#dependencies)): the name the project's `[dependencies]` gives it (`"pricing" = []`). The entry is for the calls written in that package's files: a call there must be in its entry, and in `[effects.deps]`' `std` when it is set. An empty entry allows no effects. An entry holds the package's calls from every root, tests included. The project's own name is not a key here, since `[effects.self]` and its entries hold its code.

#### A dependency's own list

A package the project needs can declare the effects its own code has, in an `[effects.self]` table in its own `nomi.toml`, which is the table it is held to when it is built on its own. `nomi effects --write`, run in the package, writes it.

```toml
# the nomi.toml of pg
[package]
name = "pg"

[effects.self]
std = ["std::fs"]
```

When the project has an `[effects.deps]` table too, it holds the package to what the package declared:

- A call written in the package's files must be in the package's own `std`, in its own entry for the file's module under its `[effects.self.modules]` if there is one, in the project's entry for the package if there is one, and in the project's `[effects.deps]` `std` when that is set. The lists intersect, so a project entry longer than the package's own is not an error.
- The rest of the package's tables are for its own builds and are not read: its `std_in_tests`, its `entries_required` and its whole `[effects.deps]`.

A package with no `[effects.self]` and no entry in the project is held by the project's `[effects.deps]` `std` alone. A project with no `[effects.deps]` table does not hold its dependencies, and reads none of their tables.

#### Names

A std name is a std module as an import writes it (`std::fs`, `std::net`), or `std::startup`. Each of these is an error at its line in `nomi.toml`: a std name of another form, a `host:` name (which only a program that embeds Nomi can allow, below), a name listed twice, a std name that is no std module (naming the nearest, `did you mean "std::fs"?`), a key under `[effects.self.modules]` that is no module of the project (naming the nearest), and, as a key under `[effects.deps.packages]`, a name that names no package the project needs, or the project's own name. A dependency is named by the key the project's `[dependencies]` gives it, so its repository or its own `[package].name` there is an error whose help names that key (`did you mean "pg"?`). A name that is allowed and that nothing has is not an error, and neither is a std module whose functions have no effects, which allows nothing. A dependency's own `std` and entries are held to the same rules, and an error in them is reported at its line in the dependency's `nomi.toml`. A key directly under `[effects]` is an error that names the two tables.

#### What counts as an effect

The lists name a program's effects. An effect acts on, or reads from, something outside the program: files, the network, processes, output and input, the clock, entropy, the environment and the arguments, tasks, a nested program, and a host function the embedding program supplies. An effect also makes state that later calls act through: a channel, a Reader or Writer over memory, a sandbox or a fake, a cookie jar, a supervisor, a WebAssembly instance. A signature or a seal draws a nonce and is one. Two kinds of call are not named. A pure function computes its answer from its operands alone: formatting a time, a seeded generator, parsing. A call that acts only through a value it is given is named where that value was made: getting a handle is the call (opening a file, dialing, listening, making an HTTP client, starting a process, the standard streams, making a channel), and using it is not, so a function handed an open file and writing to it, handed a channel and sending on it, or handed a `Client` and fetching through it, has no effect a list names. `Client.new` and `http.default_client` are `std::http`, and so is every module function that sends with the default client (`http.get`, `http.fetch`), since it gets that client itself. A call through an interface value is a use too when it reaches a std implementation of a type only std code makes: what any std implementation of the type does is named at the call into std that makes the value, such as `storage.local` or `mail.smtp`, so a function handed a `Store` and putting through it has no effect a list names, and a program that makes no `std::storage::testing` store is not charged for one. A call that reaches an implementation outside std is named in that implementation's file, as any call there is. Reading the program's own embedded files (`Files.read`) acts through the tree an `embed` declaration holds, and a declaration is no call, so no list names it. What a call costs is not an effect, and neither is an exit status, a trap or a failed assertion. `dbg` prints, and is an effect: the editor and `nomi effects --tree` show it as `dbg`. No list needs an entry for it, since `nomi build` refuses a program that holds one, so a module or package whose entry is `[]` may hold a `dbg` while you work on it. A boot that takes a `Startup` reads the environment and the arguments, and is `std::startup`, reported at the boot.

A call to one of these public std functions has the effect its row names, the name a list must hold for it: the function obtains access to something outside the program, or makes state that later calls act through. A function that acts only through a handle it is passed, such as `Dir.write_text` or `Writer.print`, is not in the table, though the editor draws a call to it bold, since bold marks a call that is not provably pure ([The language server](#the-language-server)). The standard library reference marks the same functions "Has effects": the table and the marks come from one analysis of std.

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

#### How a call is named

A call is named by the std module the code called, not the one that crosses: std::net, std::tls and std::websocket dial and look up names through std::http's functions, so `net.dial(...)` is `std::net` and `http.get(...)` is `std::http`. The standard library reference puts that name under the signature of each function a call to which has an effect, as `Has effects: std::fs`, by the same walk. A host function the embedding program supplies is `host:` and its key, which only that program's own list can allow ([A host's own list](#a-hosts-own-list)).

#### Which list holds a call

A call is charged to the file that holds it, and each list holds only the calls of its files: `[effects.self]` the project's files, an entry under `[effects.self.modules]` its module's file, and `[effects.deps]` and its entries the dependencies' files. When `pricing` calls a helper in `net::fetch` that sends a request, the request is in `net::fetch`'s file, so `net::fetch`'s entry must allow `std::http`, and `"pricing" = []` still holds. A callback the program passes to a dependency is the program's code, so the dependency is not charged when it calls it.

#### Authority by handle

A list names a whole std module, but a handle the program passes carries narrower authority. A std::fs `Dir` is one directory: `fs.open_dir` is the call a list names `std::fs`, and each call through the `Dir` reaches only what is under it and is named by no list. So the program opens the directories, and a dependency handed one needs no `std::fs` in its entry:

```nomi
// main.nomi, whose [effects.self] allows std::fs
fn main() -> Result<Unit, io.Error>
    data = fs.open_dir("data")?
    defer Dir.close(data)
    reports.write_daily(data)
end
```

```toml
[effects.deps.packages]
# writes its files through the Dir it is handed, and opens none itself
"reports" = []
```

In `reports`, `write_daily(dir: Dir)` writes with `Dir.write_text(dir, "daily.csv", text)`, which the empty entry allows; a call there to `fs.write_text` or `fs.open_dir` is refused at the call. The SQLite driver's `sqlite.open_in(dir, name)` works this way. A `Dir` from `std::fs::testing.dir` holds files in memory, for the dependency's tests.

#### When it is checked

`nomi check` holds `main`, the boot, the exported functions and the project's public functions to each table's `std`, and the test cases to it with `std_in_tests`. A public function is one another package can call: a `pub fn` at the top of a file or in an inherent `impl`, outside a test file, and each function of an interface's `impl` for a `pub` type that is not `opaque`, since another package can build a value of it. The check also counts the `impl` bodies of each type a value of which a public function's code makes, since the value can leave through the function. So a library with no `main` is held to its `[effects.self]` by everything it publishes, and `std_in_tests` holds what only its tests reach. A public function `main` reaches is reported reached from `main`. A public generic function runs at whatever types its callers give it, so it is read with each type parameter held as its bounds, whether or not anything instantiates the function: a call through a bound counts every implementation the program has, as a call through an interface value does, and that holds for a function of the bound that takes no value of the type (`T.from_sql(v)`) too. A type parameter with no bound is held as any value. A generic function it calls at its type parameters, or at a type made of them (`Option<T>`), is read the same way, and so is each function of an `impl` block over a generic type and each generic function of any `impl` block. A use of `Comparable`, `Debug` or `Display` through a bound counts every implementation in the program, whatever file declares it; a derived or built-in one acts on nothing. Under `[effects.self]`, a public generic function whose body the compiler cannot yet read that way is an error at its name (`effects-not-determined`), with the reason as its help: the list cannot hold what it does at types no call gives it. A dependency's public functions are not roots: `[effects.deps]` holds a dependency's code where the program reaches it, so a library function the program never calls never refuses it. `nomi effects`, the editor and `nomi mcp` read the same roots. `nomi run` holds `main`, the boot and the exported functions, `nomi test` the cases it runs, and `nomi build` what the build keeps. Each refuses before the program does anything, prints the diagnostics and exits 1. They print one diagnostic per name and list: the first call in source order, with its chain and fix, and a help line `and 14 more calls have the effect std::io; --all-calls lists them` for the rest. `--all-calls` on those four commands prints every call, and `--format json` and the editor always give every call. The artifact cache keeps no program its lists refuse, and an edit to `nomi.toml` is a change like any other. The editor shows each diagnostic at its call and rechecks when `nomi.toml` changes, as you type in it or when it is saved.

#### Why a call counts though it may never run

The check walks what the program can reach, as `nomi build` does to choose what a build keeps, and counts every call it can reach whether or not a run takes it: a call in a branch that never runs; a function used as a value, whether or not anything calls it; every implementation of an interface function the program calls through the interface, outside std, and std's for a type whose values code outside std makes ([What counts as an effect](#what-counts-as-an-effect)); the `Display`, `Debug`, `Equatable` and `Hashable` bodies of each type a value the program displays, inspects, compares or hashes may be or hold, at any depth, and every such body where the value's type does not say what it holds, as an interface value's does not; every hand-written `Debug` body from a test's assertion, whose failure report shows its values; a `once`'s initializer when the program reads it; the boot; and every public function of the project's own files, with the `impl` bodies of each type its code makes ([When it is checked](#when-it-is-checked)). The diagnostic's help line gives the chain from a root, each step in those words.

```console
error: this package's own code has the effect std::http, and [effects.self] allows it only std::io, std::fs
 --> main.nomi:8:10
  |
8 |     case http.get(Url"https://rates.example.com/today")
  |          ^^^^
  = help: reached from `main`; `main` calls `rates`
  = help: add "std::http" to std under [effects.self] in nomi.toml, or remove the call
```

A call that cannot happen still counts. Allow its name, or move the call out of the code the program reaches: out of a `Display` body, an interface implementation or a function value the program does not need.

#### The errors

Each refusal names the module or package and the table whose list does not allow the call, and its help names the key to add to:

- A call in the project's own files over `[effects.self]`: `this package's own code has the effect std::http, and [effects.self] allows it only std::io, std::fs`, and `add "std::http" to std under [effects.self] in nomi.toml`. A test's call is `this package's tests have the effect ...`, and the help names `std_in_tests`. A list with no names says `allows it no effects`.
- A call in one of the project's modules over its entry: `` `pricing` has the effect std::http, and its entry under [effects.self.modules] allows it no effects ``, and `add "std::http" to "pricing" under [effects.self.modules] in nomi.toml`. When `[effects.self]`' `std` does not allow it either, a help line says `std under [effects.self] does not allow std::http either`, and the help adds `and to std under [effects.self]`.
- A call in a dependency's files over its entry: `reports has the effect std::http, and its entry under [effects.deps.packages] allows it only std::fs`, and `add "std::http" to "reports" under [effects.deps.packages] in nomi.toml`. When the ceiling does not allow it either, a help line says `std under [effects.deps] does not allow std::http either`, and the help adds `and to std under [effects.deps]`.
- A call in a dependency's files over the ceiling: `fetch has the effect std::net, and [effects.deps] allows dependencies only std::fs, std::http`, and `add "std::net" to std under [effects.deps] in nomi.toml`, or `std_in_tests` for a test's call.

A refused call in a dependency's files is one error, at the call, and it ends with a note at the place in the project's own code that leads there: the last step out of the project's files on the way, a call, a function used as a value, a call through an interface or a value displayed, in the words the help's chain uses. When several places lead there, the note names the first and counts the rest; `--all-calls`, `--format json`'s `related` and the editor give each. The editor marks each place in the project's file with the refusal and offers the same quick fix there, and the mark's related location is the dependency's call.

`nomi check --format json` gives each such diagnostic the code `effect-not-allowed` and its `args`: the name; the list it is over, `self`, `self.modules`, `deps` or `deps.packages`; the module or package whose files hold the call, the project's own `[package].name` (else its entry's name) for its own files, a dependency's name in the program for a dependency's, and the entry's key for `self.modules`; then what that list allows: `"args":["std::http","self","app","std::io","std::fs"]`. Its `fix` adds what the help says to add to each list the call is over, and its `file` is `nomi.toml`, since the edit is there; the editor offers the same edit as a quick fix.

A call over the `[effects.self]` a dependency declares in its own `nomi.toml` has the code `effect-not-declared`, with the same `args` and the list `self`: the name, `self`, the package, then each name its own `std` holds. Over its own entry for the file's module under its `[effects.self.modules]`, the list is `self.modules` and the names are the entry's, and the headline names the entry: `` pricing's `calc` has the effect std::fs, and its entry under the package's own [effects.self.modules] allows it no effects ``. The package declares fewer effects than its code has, so the help says the fix is in that package, naming its `nomi.toml`, and names the project's lists only when they do not allow the call either. A call over more than one list is reported once: over the package's own list first, then the project's entry for the package, then `[effects.deps]`, and the help names the others it is over.

```console
error: pricing has the effect std::fs, and its own [effects.self] allows it only std::io
 --> /work/pricing/calc.nomi:5:9
  |
5 |     _ = fs.read_text("rates.txt")
  |         ^^
  = help: reached from `main`; `main` calls `total`
  = help: the project's nomi.toml does not allow std::fs to it either; to allow it there, add "std::fs" to "pricing" under [effects.deps.packages]
  = help: pricing declares fewer effects than its code has, and the fix is in that package: add "std::fs" to std under [effects.self] in /work/pricing/nomi.toml, or remove the call
  = note: your code has the effect std::fs in pricing here: `main` calls `total`: main.nomi:11:20
```

#### A host's own list

A Go program that loads Nomi with the `vmhost` package holds it to a list with `vmhost.WithEffects`, whose names are those of a std list, plus `host:<key>` for a function the Go program's own host tables answer, by the key spec §19 *`host` declarations and embedders* gives it (`host:stamp` in the entry file, `host:helpers/util.greet` in helpers/util.nomi). With no names the program may have no effects. A host's list holds the whole program it loads, its own code and its dependencies' alike.

```go
p, err := vmhost.LoadSource("rule", src, vmhost.WithEffects())
rate, err := p.Call(ctx, "discount", vmhost.Fields{"items": 12, "loyal": true})
```

The load refuses a program that has more effects from anything the Go program can run: `main`, the boot, every function the entry file declares (`Call` may call any of them) and the test cases. The error is a `*vmhost.EffectError`, whose text is the diagnostics above, in the words `the host allows no effects`, and whose `Calls` hold each call's name, site and chain. `Run`, `Test` and `Call` hold the program again, and the project's own `nomi.toml` tables apply too. The list does not confine what it cannot see: the files `vmhost.WithFileSystem` gives the program, the Go program's own host functions once allowed, a nested program `std::compiler` runs once that is allowed, and time and memory, which `vmhost.WithStop` and the contexts passed to `Run` and `Call` bound.

A Go program that runs `.nomic` files with the `vmrunner` package, which links no compiler, holds each to a list with `vmrunner.WithEffects`, and a JavaScript or other WASI host passes the lists to the reactor in `nomi_load_with`'s `"std"` and `"host"` ([Calling exported functions](#calling-exported-functions)), where a `host fn` the host answers through `nomi.host_call` is `host:<key>` too. Both hold the program from `main`, the boot and every exported function, with the names and the walk above, and refuse it at the load in the same words, at the file and line the program was built from. A std name that is no std module is an error that names the nearest, as in `nomi.toml`. `vmrunner.WithStop` and the context passed to `Instance.Call` and `Instance.Main` bound the time a load and a call run, and the reactor's `"timeout_ms"` does the same.

### Project mode

A package is a directory containing a `nomi.toml`, and it is the
project root of every file under it. `nomi.toml`'s
`entry_points` lists the files that contain `fn main`, other than the package's tools. Every `.nomi`
file under the package's `tools/` directory is a tool, which `nomi tool
<package> <name>` runs, and an entry point without being listed. Test files
there are not tools, and neither is a file in an `internal/` directory
there, which is where a tool's helpers go. Listing a tool in `entry_points`
is allowed and changes nothing.

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
$ nomi run main.nomi        # runs the entry main.nomi
$ nomi run .                # the same: entry_points lists one file
$ nomi tool my_app seed     # runs tools/seed.nomi
```

`nomi run`, `nomi run --watch`, `nomi run --web` and `nomi build` take a
package's directory in place of a file when its `nomi.toml` lists one entry
point (tools are not counted), and run or build that file. When it lists
several, the error names each file and asks for one. A directory with no
`nomi.toml`, or whose `entry_points` is empty, is refused with
`nomi run: <dir> is a directory; name the program's .nomi file`.

Every entry point, each file listed in `entry_points` and each tool, must
have exactly one `fn main`, and every other file must have none. Every
file of the package, in any directory, sees the others' declarations that are
not `private` (spec §14 *Visibility*).

File mode and project mode differ only in where the project root comes
from: the file's own location, or `nomi.toml`'s.

### Project root discovery

A command or the editor finds the project root of a file, such as one deep in a subdirectory, by walking upward from it:

1. First directory containing `nomi.toml` → project root (project mode).
2. Else first directory containing `main.nomi` → project root (file mode).
3. Else the file's own directory → fallback for true single-file scripts.

An extensionless `#!` script's walk stops at its own directory (*Extensionless scripts* in [Scripts](#scripts-nomi-file)), in the editor as on the command line.

The walk stops at the folder the editor opened, when the language server has one; it never escapes above it. `nomi run`, `nomi test`, `nomi check`, the language server and other tooling share this one rule, so a file opened in an editor resolves its imports to the same files `nomi run` loads.

So a file in a subdirectory, such as `app/prod.nomi`, imports `types.nomi` one directory up: the walk ends at the directory that holds `main.nomi` or `nomi.toml`, not at the file's own.

### `[i18n]`

`nomi.toml`'s `[i18n]` table names the locales whose CLDR data the program
carries ([A program's locale data](#a-programs-locale-data)) and how its phrases are translated
(spec §18 *Phrases*):

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
order `Locale.available()` returns. A program with no `[i18n]` table carries
every locale. Each of these is an error at the manifest, which `nomi check`,
`nomi run` and the editor report: an empty list, an entry that is not a tag
(`"pt_BR"`, whose error suggests `pt-BR`), one listed twice after
canonicalization, `und` (CLDR's root, which every program carries), and a
tag for which neither CLDR nor any parent of it but root has data (`qaa`).

`source_locale` is the locale a `phrase` declaration's own text is in,
English when it is left out. `partial_locales` are locales whose catalogs may
leave a phrase out, which then shows in the source locale; they need
`locales`, are checked as its entries are, may not repeat one, and the
program carries their data too. `catalogs` is the directory under the package
root holding a directory of catalogs per locale, `locales` when it is left
out; it must stay under the root. `web_embedded` are the locales a browser
build compiles into `app.wasm` ([Locales in the browser](#locales-in-the-browser)), the source
locale when it is left out; it needs `locales`, and each entry must be one
`locales` or `partial_locales` lists.

The program carries the data of one pinned CLDR release, 48.2. Each
translation is an entry in a catalog: a TOML file at the declaring file's
path under the catalog directory, in a directory per locale, so
`shop/cart.nomi`'s Polish catalog is `locales/pl/shop/cart.toml`. Each
locale in `locales` must have every phrase, and a locale in
`partial_locales` may leave any out. What a catalog holds is in
[std::phrase](/reference/phrase/).

`nomi phrases accept <locale> <phrase>` marks a locale's translation of a
phrase current: it records the phrase's text as it is now as the source
the translation was made from. `<phrase>` is the name of a `phrase`
declaration, or `path/to/file:name` when two files of the package declare
one of that name.

### `[money]`

`nomi.toml`'s `[money]` table lists the custom currencies ([std::currency](/reference/currency/)) the
package's source may write as a literal:

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
`"PTS:00"`), and an entry listed twice. A package with no `[money]` table, or
an empty list, writes no custom currency literal. The table is the package's
own: a dependency's literals belong to the dependency's `nomi.toml`, and the
application's list does not reach them.

## Packages and dependencies

A package names its dependencies in its `nomi.toml`'s `[dependencies]`
table ([`nomi.toml`](#nomitoml)), and `nomi.lock` beside it locks the
version, commit and hash of each one fetched ([`nomi.lock`](#nomilock)),
or beside the `nomi-workspace.toml` of the workspace it is a member of
([Workspaces](#workspaces)). A
published package is a directory of a git repository at a version tag,
fetched from the repository's host over HTTPS.
`nomi publish` is a reserved name with no command behind it
([Distribution](#distribution)), and the compiler does not check semantic
versions.

### Add, update and remove dependencies

These commands work on the project of the nearest `nomi.toml` at or above
the current directory. With none, they say so and name the two commands
that start a project:

```console
nomi add: no nomi.toml in <dir> or above it, so there is no project to change; start one with `nomi new <name>`, or make this directory one with `nomi init <name>`
```

They write `nomi.toml`'s `[dependencies]` and `nomi.lock`,
which the project commits. An edit keeps the file's comments, blank lines
and other tables: an entry that changes keeps its comment, a new entry goes
at the end of `[dependencies]`, and a file with no such table gets one
after `[package]`. They check only `[dependencies]`: an error elsewhere in
`nomi.toml` is `nomi check`'s to report, so `nomi add sqlkit=<dir>` works
in a project whose `[sqlkit]` table is an error until `sqlkit` is a
dependency.

Each starts from the versions `nomi.lock` holds and changes only what it
must, so a version the lock holds above what the requirements ask for
stays. `nomi add`, `nomi remove`, `nomi update <name>` and `nomi fetch`
keep every version the lock holds that they do not need to move; `nomi
add` with a version named takes that version, below the lock's too.
`nomi update` with no name raises every repository of the build within
its line. `nomi fetch --update` alone selects from the manifests as if
there were no lock.

- `nomi add [<name>=]<repository>[/<subdir>][@version]`: name a published
  package in `[dependencies]`, at its repository's latest release when no
  version is given (or `@latest`), else at the tag named, such as `v1.2.3`
  or a prerelease such as `v1.3.0-rc.1`; fetch it and what it needs; and
  write `nomi.lock`. A repository is a host and a path, with no scheme and
  no `.git`: `github.com/owner/name`. A path past the repository names the
  package in that directory of it, which the entry writes as `subdir`. On
  GitHub, Codeberg, Bitbucket and sourcehut a repository is the first two
  path elements after the host; elsewhere `nomi add` takes the longest
  prefix of the path whose host answers for its tags. The entry's name is
  `<name>` when given, else the name an entry already gives that package,
  else the package's own `[package].name`. A directory with no `nomi.toml`
  at that tag is refused: `github.com/owner/kit v1.0.0 has no nomi.toml,
  so it is not a Nomi package`. A short version is refused with the full one
  (`write the full version, v0.1.0`), and so is a branch name, since a
  version is a tag. One tag versions a whole repository, so with no
  version given, a package of a repository the project already takes at a
  version, by its own entries or through a dependency as `nomi.lock`
  holds it, is added at that version, and the line says so: `added b,
  github.com/owner/kit/b v0.3.0 (import b), at the version the project
  already takes github.com/owner/kit at`. A version named (or `@latest`)
  moves the project's other entries of that repository to it too, into
  another line as well ([Dependencies](#dependencies)), and prints `moved
  a to v1.0.0 with it, since one
  tag versions github.com/owner/kit` for each. A first-party library
  ([Dependencies](#dependencies)) is taken at the version of the release
  `nomi` was installed from, and that version is written, for a version
  named or `@latest` too: `added markdown,
  github.com/nomi-language/nomi/lib/markdown v0.0.2 (import markdown), at
  this nomi's own release: a release takes the first-party libraries at
  its version`. A commit named is taken as it is. A `nomi` built from
  source takes a library at the version named, else at the repository's
  latest release.

  ```console
  $ nomi add github.com/owner/kit/table
  nomi: fetching github.com/owner/kit v0.3.0
  added table, github.com/owner/kit/table v0.3.0 (import table)

  packages
    table
      not imported yet
  ```

  That wrote `table = { repo = "github.com/owner/kit", subdir = "table",
  version = "v0.3.0" }`.
- `nomi add [<name>=]<repository>[/<subdir>]@<commit>`, with a commit's
  40 hex digits: name the package at that commit, written as the entry's
  `commit` (`fix = { repo = "github.com/owner/fix", commit = "..." }`),
  and move the project's other entries of the repository to it, as a
  version named does. A short commit is refused: `` nomi add:
  github.com/owner/fix@4f1c0b7e9d2a: "4f1c0b7e9d2a" is a short commit:
  write all 40 hex digits of it ``.
- `nomi add [<name>=]<directory>`, for an argument that starts with `./`,
  `../` or `/`: name the package in that directory with a `dir` entry,
  relative to the project root (`csvdata = { dir = "../csvdata" }`). An
  entry of that name that names a repository keeps its repository and
  version and gains the `dir`. A directory with no `nomi.toml` is refused.
  It prints `added csvdata from ../csvdata (import csvdata)`.
- `nomi remove <name>`: take the entry out of `[dependencies]`, and write
  `nomi.lock` without it and the packages only it needed. `<name>` is the
  entry's name or its package (`github.com/owner/kit/table`). It refuses
  while a Nomi file of the project imports the dependency, and names the
  file and line: `` nomi remove: main.nomi:2 imports greet; remove the
  import first ``. It prints `removed table (github.com/owner/kit/table
  v0.3.0)` or `removed csvdata (from ../csvdata)`, and
  `removed github.com/owner/kit/csv v0.3.0, which nothing else needs` for
  each package that went with it.
- `nomi update [name]`: move one entry, named by its name or its package, or
  every entry with a version and no `dir`, to its repository's highest
  release in the same line ([Dependencies](#dependencies)): from `v1.0.0`
  it takes the highest `v1.x.y` and not `v2.0.0`, from `v0.1.0` the
  highest `v0.1.x` and not `v0.2.0`, and from `v0.0.3` nothing, since each
  0.0.x release is a line of its own. It never takes a prerelease, so an
  entry that names a prerelease later than every release stays at it, and
  one at `v0.0.4-rc.1` moves to `v0.0.4` once that is out. One tag
  versions a whole repository, so the entries of one repository move
  together. A repository the project names that has a release past its
  line gets one line that names its latest release, the latest `nomi
  outdated` lists, with the rule of the line's tier and the command that
  takes it: `` github.com/owner/kit has v0.0.5, past the v0.0.3 the project
  takes; below v0.1.0 each release is a line of its own, which nomi update
  does not leave: `nomi add github.com/owner/kit@v0.0.5` takes it ``. From
  `v0.1.0` the rule is `below v1.0.0 each minor version is a line of its
  own`, and from `v1.0.0` it is `from v1.0.0 each major version is a line
  of its own`. With
  no name, it then covers the whole build: each repository only a
  dependency names rises to its highest release in its line too, as each
  one that version then needs does. `nomi.lock` records those versions,
  and no `nomi.toml` changes for them
  ([Repositories only a dependency names](#dependencies)). With a name, it
  moves that entry alone.
  `nomi add <repository>@<version>` is how a project crosses a line.
  An entry at a commit has no later release, so `nomi update` leaves it as
  it is and says so: `fix is at commit 4f1c0b7e9d2a6c3b8e5f0a1d7c9b2e4f6a8d0c13, which nomi update
  leaves as it is`. It writes `nomi.lock` and prints each
  package of the build list that changed, such as
  `github.com/owner/strutil v1.1.0 (added: an updated package requires it)`
  or `github.com/owner/strutil v1.0.0 (removed: nothing needs it any
  more)`, or `every repository of the build is at the latest release of its line` when nothing
  moved. A first-party library moves to the version of the release `nomi`
  was installed from, down from a later tag too, so `nomi.toml` states the
  version the build takes; from a `nomi` built from source it moves within
  its line like any other.

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
    strutil
      no effects
      no change
  ```
- `nomi fetch [dir]`: fetch the packages the project needs and write
  `nomi.lock`, keeping each version it holds. It prints `2 packages in the
  package cache for .`. A later command with `--offline` then needs no
  network.
- `nomi fetch --update [dir]`: select the versions from the manifests
  alone, as with no `nomi.lock`, and write the lock. A repository only a
  dependency names that `nomi update` raised returns to the highest
  version a dependency asks for, and each version that goes down gets a
  line: `github.com/owner/strutil v1.1.0 -> v1.0.0 (down: no package asks
  for more)`.
- `nomi outdated [dir]`: list each repository of the build, as
  `nomi.lock` locks it, that has a later release than the one it is
  taken at: its version, the latest release in its line, which
  `nomi update` would take, the latest release of all, and `direct` for a
  repository the project names or `through` the packages whose
  `[dependencies]` reach it. A repository taken at a commit is not listed.
  It changes nothing, and exits 0 whether or not anything is behind. It
  reads each repository's tags from its host each time, so it needs the
  network and takes no `--offline`.

  ```console
  $ nomi outdated
  repository                version  latest in its line  latest  reached
  github.com/owner/greet    v0.2.3   v0.2.3              v0.3.0  direct
  github.com/owner/strutil  v1.1.0   v1.1.0              v2.0.0  through greet
  ```
- `--offline` on `run`, `test`, `check`, `build`, `fetch`, `add`, `types`
  and `effects`: fetch nothing. A package the cache lacks is an error that
  names it: `package github.com/owner/greet v0.2.0 is not in the package
  cache, and --offline forbids fetching it`.

`nomi add` and `nomi update` then print the effects each package they
added or moved has, from its own files, as `nomi effects` prints them
under `packages`
([The effects a program has](#the-effects-a-program-has-nomi-effects)):
its std modules, or `no effects`. A package no file imports yet has no
effects in this program and is `not imported yet`. `nomi update` adds what
changed from the old version, one line each: `now also has the effect
std::http`, `no longer has the effect std::fs`, or `no change`.

```console
$ nomi add github.com/owner/files
nomi: fetching github.com/owner/files v1.2.0
added files, github.com/owner/files v1.2.0 (import files)

packages
  files
    std::fs

wrote "files" = ["std::fs"] under [effects.deps.packages] in nomi.toml
nomi.toml's [effects] tables allow every effect the program has
```

When `nomi.toml` has `[effects]` tables
([Effects lists](#effects-lists)), the last line says whether the
tables allow every call the program reaches, or how many calls `nomi
check` refuses. When `[effects.deps]` sets `entries_required = true`,
`nomi add` writes the added package's entry under
`[effects.deps.packages]`, the tightest one, and says so; a package no
file imports yet gets its entry from `nomi effects --write` once one
does. With no `[effects.deps]` table `nomi add` writes nothing to `nomi.toml` beyond
the entry, and with no tables the reach is all they print. If the program
does not check, they print one line saying so in place of the reach, and
the add or update stands.

### Dependencies

An entry of `[dependencies]` names a package by `repo` and `version`, or
`repo` and `commit`, with `subdir` for a package that is not at the
repository's root; by `dir`; or by all of them:

```toml
[dependencies]
markdown = { repo = "github.com/owner/markdown", version = "v0.3.0" }
sqlite = { repo = "github.com/nomi-language/nomi", subdir = "lib/sql/sqlite", version = "v0.0.1" }
helpers = { dir = "../helpers" }
kit = { repo = "example.com/kit", version = "v1.2.0", dir = "../kit" }
fix = { repo = "github.com/owner/fix", commit = "4f1c0b7e9d2a6c3b8e5f0a1d7c9b2e4f6a8d0c13" }
```

- **A package is a repository and a directory.** `repo` is a host and a
  path, with no scheme and no `.git`. `subdir` is a slash path below the
  repository's root, never empty, `.` or with `..`. A version is a tag of
  the repository: `vX.Y.Z`, or a prerelease `vX.Y.Z-...`. The package is
  that directory of the repository at that tag, and it must hold a
  `nomi.toml`.
- **A package at a commit.** `commit` in place of `version` takes the
  package at that commit, all 40 hex digits of it: a short commit is an
  error that says to write all 40, and an entry with both `version` and
  `commit` is an error. A build takes a repository at one point, so a
  requirement on a repository at a commit and one on it at a tag,
  anywhere in the graph, are an error that names both requirers, as two
  lines are, and so are two different commits: `` app requires
  github.com/owner/fix v1.0.0 and github.com/owner/kit v1.0.0 requires
  github.com/owner/fix at commit 4f1c0b7e9d2a6c3b8e5f0a1d7c9b2e4f6a8d0c13: a build takes a repository at one point, so its
  requirements are all at tags or all at one commit ``. Two requirements
  at the same commit are one. A fetched package's own entries at a commit
  are read the same way. A commit is fetched by its id: from the host's
  archive of the commit on the five known hosts
  ([Distribution](#distribution)), and through `git` otherwise
  ([Private packages](#private-packages)).
- **Names are the importer's.** The key is the name the package's own files
  import the dependency by (`import sqlite::driver`). Each package's files
  import through that package's own `[dependencies]`: a dependency's files
  use the names its `nomi.toml` gives, never its consumer's. A
  dependency's own `[package].name` is only the name `nomi add` gives it by
  default, so two dependencies with the same own name are both usable
  under different keys.
- **`dir` is read where the directory is.** A `dir` applies in the package
  being built, the main package (the project of `nomi run`, `test`,
  `check` or `build`), and in every package loaded from a directory. There
  the directory, relative to that package's own `nomi.toml`, holds the
  package. An entry with a `repo` and a `dir` makes the directory stand for
  that package wherever another package requires it. So a project with only
  `sqlkit = { dir = "../nomi/lib/sql/sqlkit" }` also takes `migrate`,
  `postgres` and `sqlite` from the directories sqlkit's own `nomi.toml`
  names. The main package's own entry for a package wins over a
  dependency's `dir` for it, whether that entry is a `dir` or a version.
- **A fetched package's `dir` names its neighbor.** In a package fetched
  from its repository, a `dir` entry names the package in that directory of
  the same repository at the same tag: sqlkit's
  `sqlite = { dir = "../sqlite" }` is `lib/sql/sqlite` at sqlkit's
  version. So packages of one repository name each other with no version,
  in a checkout and fetched alike. A fetched package's `dir` entry that
  leaves its repository is an error: a package names a package of another
  repository by `repo` and `version`.
- **Which versions.** For each repository the build reaches, the version
  is the highest any reached package's `[dependencies]` asks for of any
  package in it, and at least the version `nomi.lock` locks, in every
  command but `nomi fetch --update`
  ([Add, update and remove dependencies](#add-update-and-remove-dependencies)).
  Every package of the repository is taken at that one tag. Only the
  selected versions' own `[dependencies]` are read, and the selection
  repeats until no package asks for a higher version. With `nomi.lock`
  current, that is the lock's list.
- **One line per repository.** A repository's line is its major version
  from v1 on (`v1`, `v2`); from v0.1 to v1 its minor version (`v0.1`,
  `v0.2`), since each 0.x line may break the one before, as each major
  version may; and below v0.1 the release itself (`v0.0.3`), since every
  0.0.x release may break the one before. A prerelease is in the line of
  its release: `v0.0.4-rc.1` in `v0.0.4`'s, `v0.2.0-rc.1` in `v0.2`,
  `v2.0.0-rc.1` in `v2`. Two requirements on one repository in different
  lines are an error that names both, as `nomi update` never moves a
  repository into another line: `` hello requires github.com/owner/strutil
  v2.0.0 and github.com/owner/greet v0.2.0 requires github.com/owner/strutil
  v1.1.0: one tag versions a whole repository, so every requirement on it
  must be in one line: a major version from v1 on, a minor version from
  v0.1 on, and below v0.1 one release ``. So `v0.1.0` and `v0.2.0` of one
  repository are an error, `v0.1.0` and `v0.1.3` take `v0.1.3`, and
  `v0.0.3` and `v0.0.4` are an error. A project whose dependency asks for
  another line of a repository than the project does builds once one of
  them moves to the other's line, by `nomi add <repository>@<version>` or
  by a release of the dependency.
- **Repositories only a dependency names.** A repository the project
  reaches only through its dependencies is taken at the highest version
  they ask for, so it rises only when one of them asks for more, or when
  `nomi update` raises it within its line. `nomi.lock` alone records that
  raise, and every later command keeps it
  ([Add, update and remove dependencies](#add-update-and-remove-dependencies)).
  Deleting `nomi.lock`, or `nomi fetch --update`, returns such a
  repository to the highest version a dependency asks for. A package that
  needs it at a higher version names it in its own `[dependencies]`, as
  any requirement is named:

  ```toml
  [dependencies]
  greet = { repo = "github.com/owner/greet", version = "v0.2.0" }
  # needed at v1.1.0, though no file imports it
  strutil = { repo = "github.com/owner/strutil", version = "v1.1.0" }
  ```

  An entry no file imports is allowed. It is a requirement like any other,
  for the package's consumers too: held to the others on the repository,
  in one line and not beside a commit.
- **First-party libraries.** The packages under `lib/` of
  `github.com/nomi-language/nomi` (the SQLite driver, sqlkit, Markdown,
  ...) live in the compiler's own repository, so they share the
  compiler's version: a library at one release is written against that
  release's standard library. A release `nomi` takes them at its own
  version, and every requirement on `github.com/nomi-language/nomi`, from
  the project or from any package it fetches, at any version or line,
  resolves to that version and never conflicts with another. A version an
  entry names for a library is advisory under a release, and `nomi.lock`
  records the version taken. A package that needs a newer compiler says so
  with its `[package].nomi` ([The nomi a package needs](#the-nomi-a-package-needs)),
  so a package written for v0.0.2's libraries, fetched by v0.0.1, fails
  with that error and not with a version conflict. A `commit` entry for a
  library is taken at that commit, and beside a version entry for the
  repository it is the usual conflict. While `nomi.lock` takes the
  libraries at another version, as after installing a new release, `run`,
  `test`, `check` and `build` stop: `` nomi.lock has no entry for package
  github.com/nomi-language/nomi/lib/markdown v0.0.2: this nomi is release
  v0.0.2, and takes the first-party libraries at that version; run `nomi
  fetch` to add it ``. A library loaded from a `dir` entry is read from
  its directory, and a `nomi` built from source takes the versions as
  written.
- **Names in the program.** A package the project's `[dependencies]` names
  has that name in the program. A package the project does not name, which
  only another dependency needs, has the name the first package that
  reaches it gives it, with `~2`, `~3` and so on added when another
  package, or a file or directory at the project's root, already has that
  name. That name keys its entry under `[effects.deps.packages]` and names
  it in `nomi effects`' report and its errors
  ([Effects lists](#effects-lists)).
- **Fetching.** `run`, `test`, `check` and `build` fetch what is missing,
  and name each repository they fetch on a terminal (`nomi: fetching
  github.com/owner/greet v0.2.0`). A repository's tags come from git's
  smart HTTP advertisement and its files from the host's archive of the
  tag, two HTTPS requests with no git, on the five hosts in
  [Distribution](#distribution); any other host is read through `git`
  ([Private packages](#private-packages)).
- **The package cache.** A repository at a tag is extracted once, whole,
  into `<user cache>/nomi/packages/<host>/<path>@<tag>` (`~/Library/Caches`
  on macOS, `~/.cache` on Linux, `%LocalAppData%` on Windows), or under
  `NOMI_PACKAGE_CACHE`, and every package of it is read from there. Its
  files are read-only. A package already there is not fetched again, so a
  command whose `nomi.lock` is current and whose packages are cached makes
  no network request. A repository nothing has read for two weeks is
  removed by a later fetch, which looks at most once a day.
- **Verification.** Every package, fetched or already in the cache, is
  checked against the commit and hash `nomi.lock` holds for it. A
  different hash, or a tag that names another commit, is a
  `SECURITY ERROR` ([`nomi.lock`](#nomilock)), and a fetch that finds one
  installs nothing.
- **The language server** reads only the package cache and never fetches.
  An import of a package the project needs and the cache lacks is an error
  whose hint says to run `nomi fetch`.

### The nomi a package needs

Nomi makes no compatibility promise yet, so a package written for a later
`nomi` can fail on an earlier one with errors that have nothing to do with
the cause. A package says which release it needs in `[package]`:

```toml
[package]
name = "kit"
nomi = "v0.0.2"
```

When the running `nomi` is a release older than that, every command that
reads the package (`check`, `run`, `test`, `build`, `fetch`, `add`,
`update` and the language server) stops before checking anything, with one
error that names the package, the release it needs and the one running.
The package is the project itself or a dependency, which the error names by
its repository, directory and version:

```console
$ nomi check main.nomi
error: package github.com/owner/kit/table v1.2.0 needs nomi v0.0.2 or later, and this nomi is v0.0.1; install a newer nomi
 --> main.nomi:1:1
  |
1 | import std::io
  | ^^^^^^
```

Its code is `nomi-version`. `nomi add` of such a package leaves `nomi.toml`
as it was. The key is optional, and a package without it is never refused.
There is no upper bound. A `nomi` built from source has no release, so it
refuses no package and prints nothing about it. `nomi new` and `nomi init`
write the key with the release of the `nomi` that runs them, and leave it
out on a `nomi` built from source.

### Private packages

A private repository is read through `git`, which must be on `PATH`. When
one of the five known hosts refuses a repository's tags or archive with
401, 403, 404 or 410, as it refuses a private repository, `nomi` asks `git`
instead, and a repository on any other host is always read through `git`:
`git ls-remote --tags https://<repo>`, then a depth-1 fetch of the tag into
a temporary repository and `git archive` of it. A package at a commit needs
no tag listing: `git` fetches the commit by its id at depth 1. A server
serves a commit by its id when a branch or tag reaches it and it speaks
git's protocol version 2, which `git` asks for and current servers speak,
or sets `uploadpack.allowReachableSHA1InWant`. One that does not, or a
commit the repository lacks, is an error that says so, with `git`'s own
message: `` the server of git.example.com/team/kit does not serve commit
... ``; push a branch or tag that reaches the commit, or take a tag. `git` reads your own
configuration, so your credential helper answers for the repository, and
your `url.<base>.insteadOf` rules apply. `nomi` runs it with
`GIT_TERMINAL_PROMPT=0`, so it never asks for a password on the terminal.

When `git` fails over HTTPS because it has no credentials for the host, or
the host refuses the ones it sent, `nomi` reads the repository over SSH
instead, as `ssh://git@<repo>`, with your SSH key. Its tags and its files
then both come over SSH, and so does every other repository on that host
for the rest of the command, with no HTTPS try first. `nomi` runs ssh with
`-o BatchMode=yes` added to the command `git` would run (`GIT_SSH_COMMAND`,
`core.sshCommand`, `GIT_SSH`, else `ssh`), so it never asks for a
passphrase and never accepts a host key it does not know: load your key
into the SSH agent, and connect to the host once with `ssh` to record its
key. When SSH fails too, the error names the repository and gives both of
`git`'s messages:

```console
nomi add: fetching github.com/owner/private@v1.0.0: git could not read repository github.com/owner/private over HTTPS or over SSH:
	git ls-remote --tags https://github.com/owner/private: fatal: could not read Username for 'https://github.com': terminal prompts disabled
	git ls-remote --tags ssh://git@github.com/owner/private: git@github.com: Permission denied (publickey).
	    fatal: Could not read from remote repository.
	    ...
git reads a private repository over SSH with a key the host accepts, or over HTTPS with a credential helper (`git help credentials`)
```

`nomi` reads no token variable and no `.netrc` of its own, and looks up no
vanity path.

With no `git` on `PATH`, such a repository is an error that says it needs
git: `` repository github.com/owner/private was refused by github.com (404
Not Found), as a private repository is, which nomi reads through git, and
git is not on PATH ``, or, on another host, `` repository
git.example.com/team/kit is on git.example.com, a host nomi reads only
through git, and git is not on PATH ``.

### Distribution

Publishing a package is pushing a version tag to its repository: `v1.2.3`,
or a prerelease such as `v1.3.0-rc.1`, which `nomi add` takes only when
it is named. Others add it by its repository, and by its
directory there when it is not at the root. A package says nothing in its
`nomi.toml` about where it is published.

One tag versions the whole repository. Several packages may share one
repository, each a directory with its own `nomi.toml`, and every one of
them is at the tag the repository is at: there are no tags per directory.
They name each other by `dir` entries, which need no version
([Dependencies](#dependencies)). A new major version is a tag such as
`v2.0.0` of the same repository. Below v1 a new minor version such as
`v0.2.0` starts a new line too, and below v0.1 every release does, so a
project takes `v0.0.4` after `v0.0.3` with `nomi add
<repository>@v0.0.4`, never with `nomi update`.

A host must serve two things over HTTPS: the tag advertisement of git's
smart HTTP protocol, at `https://<repo>.git/info/refs?service=git-upload-pack`
(sourcehut's without `.git`), which every git host serves, and an archive
of a tag or a commit, a `.tar.gz` with one top directory. `nomi` knows
the archive URL of five hosts, each with `<ref>` the tag, or for a package
at a commit the commit's 40 hex digits:

| Host | Archive of a tag or a commit |
|---|---|
| `github.com` | `https://github.com/<owner>/<repo>/archive/refs/tags/<tag>.tar.gz`, and `https://github.com/<owner>/<repo>/archive/<commit>.tar.gz` for a commit |
| `gitlab.com` | `https://gitlab.com/<path>/-/archive/<ref>/<name>-<ref>.tar.gz`, `<name>` the last element of `<path>` |
| `codeberg.org` | `https://codeberg.org/<owner>/<repo>/archive/<ref>.tar.gz` |
| `bitbucket.org` | `https://bitbucket.org/<owner>/<repo>/get/<ref>.tar.gz` |
| `git.sr.ht` | `https://git.sr.ht/~<user>/<repo>/archive/<ref>.tar.gz` |

A repository on any other host, a self-hosted Gitea or GitLab among them,
is read through `git` ([Private packages](#private-packages)), so a package
there needs `git` on its users' `PATH`. Only regular files and
directories are installed from an archive. A link, symbolic or hard, is
not installed: a package whose own directory holds one is an error that
names it, and a link elsewhere in the repository is skipped. An archive
that holds a device file or a path that escapes its directory is refused.

This way of publishing has costs to know:

- **The first fetch of a version trusts the host.** There is no checksum
  database. The first `nomi add` or `nomi fetch` of a version records the
  commit and hash it got in `nomi.lock`, and every later fetch, in any
  clone of the project, must match them. So `nomi.lock` catches a tag
  moved, or files changed, after that first fetch, but not a host that
  served the wrong files to it.
- **A version lives as long as its tag.** A repository or a tag deleted
  from its host cannot be fetched again. A machine whose package cache
  still holds it builds on, and a fresh clone or an emptied cache fails:

  ```console
  $ nomi fetch
  nomi fetch: fetching github.com/owner/files v1.2.0: repository github.com/owner/files has no tag v1.2.0
  ```
- **Every package in one repository shares its version.** A release of one
  is a release of all of them, and a project takes them all at one tag.

The first-party libraries are directories of the compiler's repository,
`github.com/nomi-language/nomi`, under `lib/`, published this way. A
release of `nomi` is one tag of that repository (`v0.0.2`), so a
library's version is the running `nomi`'s: a release builds, adds and
updates the libraries at that tag, whatever version `nomi.toml` names
([Dependencies](#dependencies)). If the repository lacks the tag, `nomi
add` and `nomi update` stop and say the release was published without
it; a commit (`nomi add
github.com/nomi-language/nomi/lib/markdown@<commit>`) or a checkout
(`nomi add <directory>`) still works. A `nomi` built from source has no
release version: it takes the libraries at the versions written, adds
one at the latest release tag, and while the repository has none, `nomi
add` of a library says so: ``github.com/nomi-language/nomi has no
release yet, so its first-party libraries have no version to take; add
one from a checkout with `nomi add <directory>` ``. In a checkout of the
compiler, a project reaches `lib/` by `dir` entries.

There is no Nomi-specific registry, and none is planned.

### Workspaces

A workspace is several packages developed together with one `nomi.lock`.
Its root directory holds a `nomi-workspace.toml`, which names the
packages, its members, and the `nomi.lock` that every member's build
reads:

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

Every key `nomi-workspace.toml` accepts:

| Key | What it holds |
|---|---|
| `members` | The members' directories, relative to this file. Required, with at least one entry. |
| `exclude` | Directories whose packages, and every package below them, are not members and are on their own, relative to this file. |

Any other key or table is an error at its line, naming the nearest key
when one is close in spelling, as in `nomi.toml`.

- **Members.** An entry is a clean slash path below the workspace's root,
  listed once. `*` matches within one path segment, and the matches are
  sorted; no entry matches across segments. An entry with `*` skips a
  directory that holds no `nomi.toml`; an entry without one names a
  directory that must hold one. Each member is a package: its `nomi.toml`
  has a `[package]` name.
- **Excluded packages.** `exclude` is for a package under the root that
  cannot share the workspace's versions, such as vendored code, a test
  fixture or a template: it is on its own, with its own `nomi.lock`, as if
  no `nomi-workspace.toml` were above it, and the workspace's commands do
  not visit it. Its entries take the same forms as `members`'; an entry
  with no `*` names a directory, which need not hold a `nomi.toml`, and
  an entry that names a member, or a directory above one, is an error. A
  member may still name an excluded package by `dir`, as any directory
  package.
- **The root is not a package.** A directory that holds a
  `nomi-workspace.toml` and a `nomi.toml` is an error, and so is a member
  that holds a `nomi-workspace.toml`.
- **Which workspace a package is in.** `nomi` walks from the package's
  root up through every parent directory to the file system's root, with
  no bound, and the nearest `nomi-workspace.toml` decides, with no file
  further up read; with none, or a directory the walk cannot read, the
  package is on its own. The package is a member when `members` names it
  and on its own when `exclude` names it or a directory above it, and a
  package neither list names, at any depth below the root, is an error at
  its `nomi.toml` that says to add it to `members`, or to `exclude` if it
  keeps a lock of its own; the error's fix adds it to `members`.
- **One lock.** The workspace's `nomi.lock` is beside
  `nomi-workspace.toml`, in the same form as a package's, and `direct`
  marks a package some member names ([`nomi.lock`](#nomilock)). A member
  that holds a `nomi.lock` of its own is an error that says to delete it.
- **One version of each repository.** Selection takes every member's
  requirements together, as one project's ([Dependencies](#dependencies)),
  so every member's build takes a repository at one version: two members
  that need it at `v1.2.0` and `v1.3.0` both take `v1.3.0`. Two members
  whose entries one build cannot hold are an error that names both
  members and both entries: two lines of one repository, a tag beside a
  commit, or one package taken from a directory in one member and from its
  repository, or another directory, in the other.

  ```console
  $ nomi fetch
  nomi fetch: workspace members billing and shop cannot share one build: billing ([dependencies] kit) requires example.com/kit v1.2.0 and shop ([dependencies] kit) requires example.com/kit v2.0.0: one tag versions a whole repository, so every requirement on it must be in one line: a major version from v1 on, a minor version from v0.1 on, and below v0.1 one release
  ```

- **Each member is its own project.** A member's files import its
  dependencies by the names its own `nomi.toml` gives them, and its entry
  points, effects lists and tools are its own. `nomi check`, `run`,
  `test`, `build` and `nomi tool` in a member work as in a package on its
  own, with the versions the workspace's lock selects. So does
  the language server, whose folder may be the member: the walk reads
  past it.
- **Commands at the root.** At the workspace's root, with no path or
  with the root named, `nomi check`, `nomi test`, `nomi fmt` with a mode
  flag (`-w`, `-l`, `-d`, `-check`) and `nomi effects` act on every
  member, in order of the members' directories, and on no excluded
  package and nothing else under the root. Each gives one report in its
  usual form: `check` prints an `ok` or `FAIL` line per file with its
  path from the root, `test` one summary of every member's cases, `fmt`
  one list, and `effects` each member's report under its directory (with
  `--format json`, one object per member). A member that fails fails the
  run, and the others are still checked. `nomi fmt` with no flag and no
  path still formats standard input. `nomi run`, `build`, `tool` and
  `types` there name no one package, so they are an error that lists the
  members.

  ```console
  $ nomi check
  ok billing/main.nomi
  $ nomi run
  nomi run: . is a workspace's root, which is not a package; name a member's directory or program: billing, lib/money and shop
  ```

- **Commands on the lock.** `nomi fetch`, `nomi update` and `nomi
  outdated`, run at the workspace's root or anywhere in a member, act on
  the whole workspace. `nomi update` moves every member's entries and
  raises each repository only a dependency names in the workspace's
  `nomi.lock`, which alone records it; `nomi update <name>` moves the
  entry of the member it runs in. A member that needs such a repository
  at a version names it in its own `[dependencies]`.
  `nomi add` and `nomi remove` in a member edit that member's
  `nomi.toml` and the workspace's `nomi.lock`; at the root, which is no
  package, they are an error that names the members.
- **Crossing a line.** The workspace takes one version of a repository,
  so `nomi add <repository>@<version>` in a member moves every member's
  entry of that repository to the version, into another major version
  too, edits each of those members' `nomi.toml`, and says which; `nomi
  add` of a repository no other member names changes only the member it
  runs in.

  ```console
  $ nomi add github.com/owner/kit@v2.0.0
  added kit, github.com/owner/kit v2.0.0 (import kit)
  moved kit in shop to v2.0.0 with it, since the workspace takes one version of github.com/owner/kit
  ```

- **New members.** `nomi new <name>` under a workspace's root, and `nomi
  init <name>` in a directory under it, add the new package's directory
  to `members` when no entry names it yet, and say so. At the root
  itself, which is no package, both are an error.
- **In an editor.** The language server reads an open
  `nomi-workspace.toml` as it reads a `nomi.toml`: its errors at their
  lines, completion of its keys, and a hover on an entry of `members` or
  `exclude` that names the package directories it takes in. A hover on
  `[package]` or its `name` in a member's `nomi.toml` names the
  workspace it is a member of, or says it is excluded. The unlisted
  package's error carries the fix that adds it to `members`, and a change
  to `nomi-workspace.toml` or the root's `nomi.lock` checks the open
  members again. Helix, Neovim and VS Code send the file to the server
  as they send `nomi.toml`.
- **A member someone else fetches.** A project that names a member by
  `repo` and `subdir` reads it alone, as any package: the
  `nomi-workspace.toml` and `nomi.lock` published beside it are not read.
- **Errors.** An error in `nomi-workspace.toml` stops a member's `nomi
  check`, `run`, `test`, `build` and the language server with that one
  error at its line, whose code is `manifest`, as an error in a
  `nomi.toml` does.

### `nomi.lock`

`nomi.lock` beside `nomi.toml` locks the build: one line per fetched
package the project needs, with its repository, its directory there (`.`
for the root), its selected version, the commit the tag named, the hash of
its files, and `direct` when the project's `nomi.toml` names it. A package
taken at a commit has the word `commit` as its version, which no tag can
be since a tag is a version `vX.Y.Z`, and the commit beside it. The hash
is `sha256:` and the SHA-256 of a line `<sum>  <path>` per regular file in
the package's directory at that tag, as `sha256sum` prints them, sorted by
path, each path from the package's directory. The repository and the
version are not part of it, and neither are file modes. Lines are sorted
by repository and directory. A package loaded from a directory, by the
project's `dir` entry or by one of a package so loaded, has no line, so a
project whose dependencies are all directories gets no `nomi.lock`, and
one already there is written with no lines. The project commits it. A
member of a workspace has none: the workspace's `nomi.lock`, beside its
`nomi-workspace.toml`, locks every member's build
([Workspaces](#workspaces)).

```text
# nomi.lock locks each package this project fetches: its repository, its
# directory there (. for the root), the tag (or `commit` for a package taken
# at a commit), the commit and the hash of its files. nomi fetch, add,
# remove and update write it; commit it.
github.com/owner/fix . commit 4f1c0b7e9d2a6c3b8e5f0a1d7c9b2e4f6a8d0c13 sha256:5d1e6a0f3c9b8e7d2a4f6c1b0e9d8a7f3c2b1a0e9f8d7c6b5a4f3e2d1c0b9a87 direct
github.com/owner/greet . v0.2.0 b59dd9377cf60b87eaf6825df22d7e32875e2d23 sha256:c02f53f71111860cd71831c5b9871bdb5d96af575fb40c3733eadec79e03ff10 direct
github.com/owner/kit csv v0.3.0 ecfeb371a6f410df58c279acc94dd013d841c73c sha256:fe3e9fed62198abf1023eb6a167b4388179529bd39358a0d4b5a2019212dde32
github.com/owner/kit table v0.3.0 ecfeb371a6f410df58c279acc94dd013d841c73c sha256:cc7cf8a18fb19398707c83c76b239ee7e02b5ad9ae899c6e63f1186d04872e30 direct
github.com/owner/strutil . v1.1.0 52bd494d04551f4b4025aba1e7387a8b14230e68 sha256:7ae190245d125b235bae549084a7642cd9cc3dd551ed9572f641dfe05fff7a72
```

`nomi fetch`, `nomi add`, `nomi remove` and `nomi update` write it, from
the versions the requirements select starting from the ones it holds, so
a version it holds above what the requirements ask for stays until `nomi
fetch --update` selects from the requirements alone. A version it holds
in another line than a requirement on that repository, or a tag where a
requirement names a commit or the reverse, as after editing `nomi.toml`
by hand, is an error in every command, every member's requirements
counted in a workspace: `` nomi.lock holds github.com/owner/kit at v2.0.0
and app requires github.com/owner/kit v1.0.0: one tag versions a whole
repository, ...; `nomi fetch --update` selects again from the manifests ``.
`nomi fetch --update`, and `nomi add` with a version of that repository,
select it again. `run`, `test`, `check` and `build`
read it and never write it: they fetch a package it locks that the cache
lacks, and refuse one it does not hold:

```console
$ nomi check main.nomi
error: nomi.lock has no entry for package github.com/owner/strutil v1.1.0; run `nomi fetch` to add it
 --> main.nomi:1:1
  |
1 | import std::io
  | ^^^^^^

error: no module `greet::greeting`: no file greeting.nomi in greet/
 --> main.nomi:2:8
  |
2 | import greet::greeting
  |        ^^^^^^^^^^^^^^^
  = help: the project needs package github.com/owner/strutil v1.1.0, which is not in the package cache; run `nomi fetch` to download it
```

Files whose hash differs from their line fail with `checksum mismatch`
and a `SECURITY ERROR`, and install nothing:

```console
$ nomi fetch
nomi: fetching github.com/owner/greet v0.2.0
nomi fetch: fetching github.com/owner/greet v0.2.0: verifying github.com/owner/greet v0.2.0: checksum mismatch
	files:     sha256:c02f53f71111860cd71831c5b9871bdb5d96af575fb40c3733eadec79e03ff10
	nomi.lock: sha256:d02f53f71111860cd71831c5b9871bdb5d96af575fb40c3733eadec79e03ff10

SECURITY ERROR
The files do not match the hash nomi.lock records for them. So the repository's tag, the host or nomi.lock has been changed. Nothing was installed
```

A tag that names another commit than its line is a `SECURITY ERROR` too
(`tag v0.2.0 names commit ..., and nomi.lock records ...`): the tag moved
after the lock was written. Files in the package cache that do not match
their line are one as well, which says the cache or `nomi.lock` has been
changed.

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
or a `group`, whose header line runs every test in the group. In a group's
`subtests` block (spec §17 *Subtests in a group*), the block's line runs the
whole group too, and a test's line runs that test once for every row, each
row a test of its own in the report, `todo api [sqlite] / creates a todo`.
When
nothing starts there, `nomi test` runs nothing and exits non-zero with
`no test at <file>:<N>` and the nearest test's first line; with
`--format json` that error is a `"type":"file"` failure record.

`nomi test --run <regexp>` runs only the tests whose names the regular
expression matches (RE2 syntax, as std::regex reads it). The name
is the one the report prints after `<file> :: `: `parses a date`,
`dates / parses a date` for a test in a group, `double //! test line 4` for
an attached test, and `todo api [sqlite] / creates a todo` for a test in a
group's `subtests` block. The pattern is matched anywhere in the name, so
`--run 'creates'` runs that test for every row, `--run '\[sqlite\]'` runs
the sqlite row's tests, and `--run '^dates / '` runs one group. A row
whose tests all miss is never booted. The rows of a `subtests` statement
in a test's body are computed as the test runs, so they are not selected
on their own: a test that matches runs all its rows. The ` / ` between a
group and its test is part of the name, with its spaces, and a pattern
does not split on it.

`nomi test --reaching <file>:<line>` runs only the tests whose code can run
the function at that line: the function declared there, or the innermost
one whose body holds the line, with every instance of a generic one. A
test reaches it when its body, its group's `boot` or anything they call can
call it, directly or through an impl a dispatch may choose, which is the
walk that decides whether a test is unchanged. Each test that runs says
whether it has effects (`"effects": true` in its JSON record). A line no
test reaches is an error, `no test reaches <file>:<line>`. Every test file
under the path is loaded to answer it.

`--run` and `--line` both narrow: `--line 12 --run sqlite` runs the test
on line 12 for the sqlite row only. A file in which the pattern selects
no test is not loaded. The count, the summary line and the exit status
cover only the tests that ran. A pattern that selects nothing is an error,
`no test matches --run "<regexp>"`, not an empty pass, and a
`"type":"file"` failure record with `--format json`.

### `nomi test --watch`

`nomi test --watch [path]`, with any of the other flags, runs the tests,
then runs them again whenever a `.nomi` file under the path, or a file the
tests import, changes, as `nomi run --watch` does ([`--watch`](#running-again-on-every-change---watch)): tests still running
are stopped, `<file> changed; running again` is printed on standard error,
and a file that fails to compile is reported as `nomi test` reports it. A
new test file under the path is picked up by the next run.

### Unchanged tests

`nomi test` does not run a test again when nothing it can run has changed
since it last passed. When a test passes, the artifact cache keeps a
fingerprint of its code: the code of every function it can reach, with no
source positions, the `once` values it reads, its group's `boot`, `setup`
and `clock`, the `--timeout`, the project's locales and the build of `nomi`.
On the next run, a test with no effects whose fingerprint matches is
reported as passed and unchanged, with the cases it counted, and is not run:

```text
ok a_test.nomi :: adds (unchanged)
ok a_test.nomi :: rounds (9 cases, unchanged)
test result: ok. 4216 passed, 0 failed; 3 ran, 4213 unchanged
```

A test has no effects when everything it can reach computes its results
from its operands alone: nothing writes output (`dbg` included), reads
input, the clock, files, the network or the system's randomness, makes a
channel or a task, or starts a process. Reading a hash counts as
an effect here: `String.hash`, or `Hashable.hash` of a String, a collection
or a struct, is keyed by a seed each run draws, so the next run may hash
the same value otherwise. An app field its group's
`boot` publishes is no effect, since the boot is part of the test's code.
Such a test's result depends on its code alone,
so code that did not change cannot pass one run and fail the next. A test
with effects always runs, since what it reads may have changed. Only a pass
is kept: a test that failed, was blocked or timed out runs every time until
it passes. A test is still skipped after lines move above it, unless an
assertion whose failure it reads as a value (a `testing.check`) moved, or a
typed literal it reaches moved.

A file whose every selected test is unchanged and has no effects is not
loaded at all, as long as none of the files its program is built from
changed: its tests are reported from what the cache kept, as they would be
if it were loaded. That starts with the second run after its program last
changed, since the first decides each test on the program the cache kept.

`--all` runs every test, and keeps what passes. `--changed` also skips an
unchanged test that has effects: its code is the code that passed, but the
files, clock or network it reads may not be. A run with no cache
(`NOMI_ARTIFACT_CACHE=off`, a fresh CI machine, the browser) runs every
test. A standard library module's tests always run. `NOMI_DEBUG_ARTIFACT_CACHE=1` says on stderr why each test ran:
no stored pass, its effects, or which function's code differs; and for each
file, that it was answered from what the cache kept, or why it was loaded.

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

```console
FAIL coins_test.nomi :: flips forever
  line 7: did not finish within 10s, so it was stopped
  it was running:
    coins_test.nomi:7 in flips
    coins_test.nomi:12 in flips forever
  help: `nomi test --timeout <duration>` sets how long a case may run (10s unless given), and `--timeout 0` removes the limit
```

A `subtests` table is one test, so its rows share the limit. `nomi run` has
no limit, and neither has a test run under the debugger (`nomi dap`), where
a paused test must not time out. Work inside a single host function call
cannot be stopped until the call returns.

A test under `clock Clock.Virtual` that is still waiting outside the
program when its limit passes, on a socket or a process, cannot be stopped:
the virtual clock cannot wake such a wait. The test fails, naming where it
waits, and no test after it starts in that run, since a virtual-clock test
beside it is not safe. `nomi test` reports the tests before it, reports
each test after it as not run, and exits non-zero:

```console
FAIL server_test.nomi :: waits for a client
  did not finish within 10s and could not be stopped: it was waiting outside the program
  it was waiting at:
    std/net.nomi:545 in Listener.accept
  help: `nomi test --timeout <duration>` sets how long a case may run (10s unless given), and `--timeout 0` removes the limit
FAIL server_test.nomi :: answers
  did not run: a case before it was still waiting outside the program when its time ran out, and no other case can start beside it
```

Under the real clock the same wait is stopped like any other, and the
tests after it run.

A test whose every task waits for another of its tasks, on either clock,
fails at once rather than at its limit (spec §16 *Deadlock*). Its failure
is at the line the test waits on, and lists each task's wait, the test's
own first:

```console
FAIL queue_test.nomi :: drains the queue
  line 9: every task is waiting, so none can run again:
    the test waits for a task to end at queue_test.nomi:9
    the task spawned at queue_test.nomi:6 waits to receive on a channel at queue_test.nomi:4
```

### Output

What a test writes while it runs (`io.print`, `io.write`, `io.inspect` and
`dbg`) is reported under the test, not where it was written. Each test's
output is collected while it runs: from its group's `boot` and `setup`, its
body, the tasks it starts, and a `once` initializer it is the first to force.
The text report prints it under the test's `ok`, `FAIL` or `BLOCKED` line,
indented two spaces, for passing tests too; a failing test's output comes
before its failure block, and a test stopped early keeps what it wrote
before stopping. `dbg` lines keep their colour when the report is coloured.
Tests run one at a time, so outputs never interleave. An unchanged test
does not run and writes nothing; it has no effects, so it had nothing to
write. Output inside an
`io.capture` belongs to the capture, as anywhere, except `dbg`, which still
goes to the test. Output a task writes after its test has finished goes to
stdout in text mode and to stderr in json mode. In a test case outside any
`io.capture`, stderr is part of the case's output, which the report shows
under the case.

```console
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
  attached test's label embeds display wording and line numbers. A test in
  a group's `subtests` block has one record per row, all at the test's
  `line`, each `name` carrying its row (`todo api [sqlite] / creates a
  todo`).
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
- When the artifact cache holds the program ([Unchanged tests](#unchanged-tests)),
  each passed or failed test's record carries `decided`: `unchanged` for
  a test that did not run, and for one that ran, why: `new` (no stored
  pass), `changed` (its code or settings differ), `moved` (an assertion
  whose failure is a value moved), `effects` (it has effects or reads a
  hash) or `all` (`--all`), and `effects` set to `true` for a test whose
  code can have effects or read a hash, as `--reaching` records carry it
  too. The summary then counts
  the unchanged cases, which `passed` includes, as `unchanged`. These are
  omitted when no cache was used or nothing was unchanged.

In json mode stdout carries only records: a test's output is in its record.
The exit status is the same as in text mode: nonzero when a test file fails
to load, a case fails, or a case is blocked.

`--line`, an editor's test lens and the debugger treat a test that runs
`subtests` as one test. A breakpoint in a `subtests` body stops once per
row, in a frame of its own.

### The test environment

`nomi test` sets the environment variable `NOMI_ENV` to `test` for the run,
unless the environment already sets it. Nothing else reads it: a program the
tests start reads it from `startup.env` as it reads any variable. A `boot`
that a test group's `boot` line calls gets the `Startup` that line builds,
not the real environment, so a test passes the variable itself:
`boot boot(Startup{env: {"NOMI_ENV" => "test"}})` ([App Fields, Defer &
Context](/app-fields/#settings-per-environment)). `nomi run` does not set
it.

Under `nomi test` a test file's working directory is its package root: the
nearest directory at or above the file holding a `nomi.toml`, or the file's
own directory when there is none. So a test that reads its package's files by
a relative path (`fs.read_text("data/rates.csv")`) reads the same files
wherever `nomi test` runs.

## Formatting

`nomi fmt [-w|-l|-d|-check] <paths>` formats Nomi source: `-w` writes the
result to each file, `-l` lists the files that would change, and `-d` shows a
diff. `-check` lists them as `-l` does and exits 1 when there are any, which is
the check a CI step runs (`-l` exits 0). Editors format on save through
`nomi lsp`.

`nomi fmt` gives every program one layout and has no options. It indents 4
spaces per level and breaks lines at 100 columns. It keeps a few layout
choices of the author's, which Writing Nomi lists
([Let `nomi fmt` own the layout](/writing/files-and-layout/#1-let-nomi-fmt-own-the-layout)),
and decides the rest from the syntax and the width.

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
fn footer() -> Html
    Html"""
    <footer class="site">
      <p>Made with Nomi</p>
    </footer>
    """
end
```

A literal that follows code on its line (after a binding's `=`, a struct
field's `:`, a map entry's `=>`, a case arm's or lambda's `->`, `return`,
or a declaration's header) puts them one level in, the way a broken
pipeline continues after `=`:

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

**Values after `=` and `:`.** A value bound to a name (a local binding, a
destructuring, a `once`), a struct field's value in a literal and a field's
default in a declaration stay on the name's line when they fit there. A
value the grammar closes with `end` (an `if`, a `case`, a lambda, a `begin`
or `concurrent` block, with or without a `?` or `!` after its `end`) that
does not fit moves to the lines below, one indent in, so its keyword and
its `end` share a column. `nomi fmt` rewrites this:

```text
misses = if String.contains(game.word, letter) then
    game.misses
else
    game.misses + 1
end
```

as this:

```nomi
misses =
    if String.contains(game.word, letter) then
        game.misses
    else
        game.misses + 1
    end
```

A value closed by a bracket (a struct literal, a call, a list or a map)
stays on the name's line and breaks inside its brackets, and a lambda whose
body is a struct or triple-quoted literal stays there too, since it ends
with the literal's closer. An overflowing pipeline moves under, as above.
An `if` or a lambda that does not fit on the name's line but fits on one
line below it is written there on one line. A binding `else` keeps its
value on the binding's line, and so do a named argument, a map entry, a
parameter's default, `return` and `assert`.

**Bodies.** A function's, a declaration's and a test's body always goes on
the lines below its header, one indent in, closed by `end` at the header's
indent; an empty one is `end` on the header's line.

**`if`, binding `else` and lambda bodies.** A guard
(`if n < 0 then return 0`) and a binding `else` stay on one line whenever
they fit the line, so a run of guards reads as a run of one-line
statements. A two-branch `if` is written inline (`if c then a else b`) up
to 50 columns, or up to the line's width when its `else` is one atom such
as `None`, `0`, `""` or `[]`; an `else if` chain is always in block form. A binding `else` whose statement
holds an `if` or a `case` is always in block form too, so `else`, `return`,
`if` and `then` never share a line. An inline `if` too wide for its
line is written in block form. An inline `if` that something follows on its
line, a binary operator's left operand or a pipe's head, goes in
parentheses, and an `if` stage that another stage follows is written in
block form. A single-expression lambda whose body is an `if` holding
`break`, `continue` or `return` goes below the header, closed by `end`, as
does a value-producing lambda body that does not fit, and a call it ends
closes with `end)`. A body written below its `->` stays there, closed by
`end`, even when it would fit on the header's line, so a body the
formatter once moved below does not move back on a later run.

**`case` arms.** When every arm fits on one line as `pattern -> body` and no
body is a block, the arms sit on consecutive lines. Otherwise every arm
breaks: a triple-quoted body keeps its `"""` on the pattern's line, any
other body, a block's statements included, goes on the lines below the `->`
one indent in, and one blank line separates every two arms. A triple-quoted body breaks
the arms as a block does, and so does a stacked pipeline. A binding `else`
with arms lays them out the same way. The rule is deliberate: when any arm
needs more than one line, every arm moves its body below `->` with a blank
line between arms, so the arms of one `case` always share one layout.
Blank lines the source put between arms are ignored, and a comment stays
above its arm.

**Alignment.** Consecutive one-line items of one construct line up:

- a `case`'s arms, with or without a subject, at their `->`;
- a struct declaration's fields, their types after `name:` (`at:       Place`);
  a default's `= value` is not a column of its own;
- a multi-line struct or record literal's fields, a multi-line call's named
  arguments and a multi-line parameter list's parameters, the value or type
  after `name:` (`name:   "goblin",`);
- a multi-line map literal's entries, at `=>`;
- rows: a multi-line list or argument list whose items are all tuples of one
  length, or all calls of one callee with one argument count, pads each
  column after its comma to the column's widest entry (`("Mei",    100),`),
  as a `subtests` table is written;
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

A block is a run of such items on consecutive lines at one indent. A blank
line, a comment line or an item over several lines ends it, and each run
aligns on its own, so a `case` whose arms put their bodies below `->`
aligns nothing. Where a run breaks: two neighbouring items
whose left parts are both at most 40 columns wide always align, and past
that an item starts a new section when its left part is 2.5 times wider or
narrower than the geometric mean of its section's so far. Each section
aligns on its own, so one long item leaves the lines around it aligned;
rows have no such limit. Widths are display columns, as line width is measured. Alignment
comes after the line breaks and never changes one: a block that padding
would take past 100 columns stays unaligned. Local bindings' `=`, enum
variants' payloads, one-line literals and pipelines are not aligned.

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

**Imports.** A file's or block's imports are combined into one `import`
block, `import` alone on its line, one entry per line and `end`, sorted with
`std::` paths first, and a lone import stays bare.
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
its `return value`s too, since such an `if` is a statement and has no
value.

**Struct literals and anonymous struct types** are kept on one line when
short and broken one field per line, each with a comma, when long. The
break is made at the braces, so a long anonymous struct in a parameter's
type breaks itself, not the parameter list. An anonymous struct type or
variant written across lines keeps one field per line.

**Literals and operators.** A literal written with a line break after its
opening bracket stays broken, its first element on the line below the
bracket; delete that line break to join it again. A list of number literals
is filled, as many elements to a line as fit; a list of other literals is
not. A long comparison or `and`/`or` chain breaks before the operator, the
right operand one indent in, and a broken chain puts each operator on a
line of its own. When literals (lists, maps, sets, struct literals) nest
three deep, the outermost breaks, and the ones inside it stay on one line
when they fit. A comment inside a bracketed list stays with its item, and a
list that holds a comment is written one item per line. A trailing comment
is never moved to make room, so it may run past 100 columns.

**Small layouts.** One space follows `not`, and parentheses written around
it or its operand stay. A `?` or `!` sits against its expression. In a block `if`, `else` and
`else if c then` start their own lines at the `if`'s indent. A long `or`
pattern breaks after each `or`.
The arms or branches of a `|> case` or `|> if` stage sit one indent in from
the line its `|>` is on.

## Building

### Executables

`nomi build <file> [-o <out>] [--target <os>/<arch>]` writes one executable: the `nomi-vm` binary with the compiled program appended to it, the way `deno compile` and `bun build --compile` work. At startup `nomi-vm` reads the program from its own file and runs `main` exactly as `nomi run` does: the same arguments, standard input, signal handling, output and exit status. It skips the front end, so a hello-world program starts in a few milliseconds, and then runs on the same VM at `nomi run` speed. It is not native code.

The executable goes in the working directory, named after the file without `.nomi`, unless `-o` names it. `nomi build <dir>` builds the one entry point of a package's directory ([Project mode](#project-mode)) and names the executable after the directory. Beside the directory that name is the directory's own, so `nomi build` refuses it there and `-o` names another file.

`nomi build` refuses what `nomi run` would refuse, with the same text, and writes nothing: a front-end error, or an error at each piece of code the compiler cannot compile. It also refuses:

- a file without `fn main`, which has nothing to build, unless it has an `export fn` and the target is `wasip1/wasm` (below);
- a program with a `todo` or a `dbg` in any of its files, whether or not a run would reach it (spec §11 *Unwritten code: `todo`* and spec §8 *`dbg`*). It prints one list of every `todo` and every `dbg`, in file and source order, under one header that counts them (`nomi build: 2 todos remain:`, or `1 todo and 2 dbgs remain:` with both). Each line is the position and the item: a `todo` with its reason, a `dbg` with its operand cut after 40 characters with `...`, and `|> dbg` for a pipe stage.

```console
nomi build: 1 todo and 2 dbgs remain:
  main.nomi:2:5 todo "parse the header"
  main.nomi:7:5 dbg total + 1000000000 + 2000000000 + 300000...
  main.nomi:8:18 |> dbg
```

`--target <os>/<arch>` picks the platform, such as `linux/amd64`, `windows/amd64`, `js/wasm` or `wasip1/wasm`; without it the executable is for the platform `nomi` runs on. `nomi-vm` comes in two builds: the full one, and a lean one without the network and WebAssembly modules that `nomi build` uses for a program that reaches neither. It comes from, in order:

1. For its own platform, a release `nomi` carries both builds, compressed, and unpacks the one a build needs into its cache the first time.
2. `nomi-vm` beside `nomi` (or beside the file a symlinked `nomi` resolves to), built for the target.
3. Not for a release `nomi`: a source checkout of the compiler. `nomi-vm` is built with `go build` and cached. This is how `go install` and a development tree work.
4. A `nomi-vm` unpacked or downloaded earlier for this release and target.
5. Download: the platform's runners archive, `nomi_<version>_<os>_<arch>_runners.tar.gz`, from the release this `nomi` was cut as, verified against the release's checksum file, with only that `nomi-vm` extracted and cached. `nomi build` prints the URL it downloads, and with no network the error names it.

A release keeps the runners it unpacks or downloads in the runner cache, one directory per release. A release's directory that no build has used for two weeks is removed by a build of another release, which looks at most once a day. If that release builds again, it unpacks or downloads its runners again.

A program importing `std::compiler` is appended to `nomi-vm-compiler`, which links the front end and has no lean build. A release ships it in an archive of its own per target, `nomi_<version>_<os>_<arch>_compiler.tar.gz`. Unpack it beside `nomi`, or let `nomi build` download it.

A prebuilt `nomi-vm` must come from the same commit as `nomi` and be the kind asked for. Both are in its build information, and `nomi build` refuses a `nomi-vm` from another commit, or a plain one where a program needs `nomi-vm-compiler`. So a release builds executables for every platform and every program, and a built binary needs nothing at all.

A WebAssembly target, `js/wasm` or `wasip1/wasm`, writes `<name>.wasm` by default. Nothing may follow a module's last section, so the program goes inside the module:

- A `wasip1/wasm` module carries it as one more active data segment, which the WebAssembly engine writes into a 4 MiB area of the `nomi-vm`'s own memory when it instantiates the module. The module reads its program from there, so it needs no file access: `wasmtime run hello.wasm` runs it, and so does a browser's WASI shim with no file system.
- The area is uninitialized memory, so it adds nothing to the file. It raises the module's declared initial memory by 4 MiB, to 12.3 MiB for the reactor without the network and 15.8 MiB for the one with it. `nomi build` refuses a program whose compiled form is larger than the area, naming the limit.
- A `js/wasm` module carries it in a custom section named `nomi.image`, which every runtime skips. A module cannot read its own sections, so it looks where its host can supply them. It uses `globalThis.nomiImage` when a loader has set it to the section's bytes (`new Uint8Array(WebAssembly.Module.customSections(module, "nomi.image")[0])`), and otherwise reads the file argv[0] names through the host's `fs`.
- Beside a `js/wasm` module `nomi build` writes `wasm_exec.js`, the module's JavaScript support file, and `nomi-node.mjs`, which runs the module under Node 22 or later with nothing else installed: `node nomi-node.mjs hello.wasm [args...]`. The words after the module are its `startup.args`, and Node's whole environment is its `startup.env`, whatever their size. The loader sets them as `globalThis.nomiArgs` and `globalThis.nomiEnv` before the module starts, and the runtime reads them from there. Go's js/wasm support file has room for 8 KiB of arguments and environment, so only the module's path and the variables the Go runtime reads as it starts (`GODEBUG`, `GOGC`, `GOMEMLIMIT` and `TMPDIR`) go through it. `nomi.js` passes only the `env` its caller gives, the same way.

A `wasip1/wasm` program with at least one `export fn` is written as a WASI reactor instead of a command module (spec §7 *Exported Functions*). The program is appended to `nomi-vm-reactor`, the same VM linked as a reactor, which is found, built, downloaded and checked as `nomi-vm` is. `nomi-wasi.js` and `nomi-wasi.d.ts`, the JavaScript host for it, are written beside the module.

The `js/wasm` build keeps the browser build's limits: its file system is in memory, empty under Node, and a call nests at most 1,000 deep. A command-line program that reads real files belongs on `wasip1/wasm`. Every release ships a runners archive for each WebAssembly target, so a release `nomi` downloads it as it would any other platform's. The `js/wasm` archive also carries the `wasm_exec.js` its `nomi-vm` needs, and the `wasip1/wasm` archive carries `nomi-vm-reactor` beside `nomi-vm`.

### Embedded files

A program's `embed` declarations (spec §18 *Embedded files*) are resolved when the program is compiled, so every build carries their files: an executable, a WebAssembly module, a `--web` folder's `app.wasm` and a `.nomic` hold the bytes in the compiled program, and read no file but their own when they run. Delete the source tree and the build still serves its pages and applies its migrations. Patterns name files from the package root, as import paths do, whatever directory the declaring file is in.

A file's contents are read when the program reads that file, never all at once. A built executable or `.nomic` keeps the files after its code and maps them, so starting it costs the same however much it embeds, and a file it serves is paged in by the operating system rather than held in the program's memory; `nomi build` writes them from the files on disk without loading them whole. `nomi run` and `nomi test` read an embedded file from disk when the program reads it, and stop the program with an error naming the file if it changed after the program was compiled; run it again to embed the new contents. The REPL reads them from its working directory; a tour example reads its `// FILE:` data files.

A pattern whose folder or file does not exist yet, such as a frontend's `web/dist` before its first `npm run build`, is a warning in `nomi run`, `nomi check`, `nomi test`, the REPL and the editor: the program compiles with that path as an empty directory, and a read under it returns `NotFound`. `nomi build`, for every target, refuses it as an error, so a release never carries an empty frontend. A program with such a warning is not kept in the artifact cache, so each run reports it, and the next run after the folder appears embeds its files.

Only the declarations a program reaches go into its build, so an `embed` nothing reads costs nothing, and a program with none is unchanged. The files count toward the compiled program's size, so a `wasip1/wasm` module's 4 MiB limit (above) includes them. `nomi run --watch` does not watch embedded files: a change to one is picked up at the next run.

### Frontend builds

A JavaScript frontend's static build is embedded and served with `http.file_server_embedded` ([Frontends](/frontends/#a-javascript-frontend)). Each bundler needs three settings to match: the folder it writes, which the `embed` names and `Files.sub` makes the served root; a dev proxy that forwards `/api` to the Nomi server, so the page calls the API on its own origin; and the paths of the files whose names carry a hash of their contents, which `immutable:` lists.

| Bundler | Output folder | Dev proxy | `immutable:` |
|---|---|---|---|
| Vite | `dist/` | `server.proxy` in `vite.config.ts` | `["assets/**"]` |
| webpack | `dist/` | `devServer.proxy` | `["assets"]`, with output named `assets/[name].[contenthash].js` |
| Angular | `dist/<project>/browser/` | `proxy.conf.json` | `["*-*.js", "*-*.css"]`, the hashed files at its root |
| Create React App | `build/` | `"proxy"` in `package.json` | `["static/**"]` |
| SvelteKit, static adapter | `build/` | `server.proxy`, as Vite | `["_app/immutable/**"]` |
| Next, `output: 'export'` in `next.config.js` | `out/` | none | `["_next/static/**"]` |

`http.file_server_embedded` sends a `.br` or `.gz` copy that sits beside a file to a browser that accepts it. In Vite, a compression plugin (`npm install -D vite-plugin-compression2`) writes those copies. A React app's `web/vite.config.ts`, with the proxy to a Nomi server on port 8080:

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

### Effect classes

A std host function that acts outside the program in a way some target cannot has an effect class: `files` for std::fs's, `processes` for std::process's, `network` for std::http's requests, `connect` for std::net's, std::tls's and std::udp's dials, a Unix socket's included, and std::net's lookups, and `listen` for a server, a listener or a bound datagram socket. Every target has `files`, a `js/wasm` module's in memory. A `js/wasm` module sends HTTP requests through the browser's `fetch` but cannot open other connections, start programs or serve; a `wasip1/wasm` module has none of the four. std::signals has no class: a program that receives signals builds for every target, and where none arrives (Windows, a browser, WASI) `signals.receiver` answers `Err(Unsupported)`, so a command line tool subscribes where it can.

`nomi build` walks what `main`, the boot and the exported functions can reach, and refuses a program that reaches a call of a class its target lacks. It writes nothing and lists each call in the program's own code that reaches the class, at that call: the std function it calls (`fs.read_text`), the host function that one reaches (`fs/host.read_text`), and the functions the call is reached through. A hint follows, to build for another target or pass `--allow-unsupported <class>`.

The walk counts a call in a branch the program never takes, so `--allow-unsupported <class>` (repeatable) builds anyway. Such a call returns `Err(io.Error.Unsupported{operation:, target:})` when it runs; it never traps. `--no-vm` names no target and is not checked.

A class is about what a target can do. What a project lets its program reach, on any target, is an [effects list](#effects-lists).

A `wasip1/wasm` build of a program that compiles or runs a WebAssembly module with std::wasm is refused too, with `` nomi build: this program uses WebAssembly modules, which no runner for wasip1/wasm has; build it for another target ``: no wasip1 runner links the WebAssembly runtime. On the other targets the runner a program is appended to has it, and a program that reaches neither std::wasm nor the network gets the smaller runner with neither.

`nomi build --web` of a program that reaches `files` prints one line, `` nomi build --web: this program uses files; in the browser it sees only the files run's "files" option gives it ``.

### Compiled programs and `nomi-vm`

`nomi build <file> --no-vm [-o <out>]` writes the compiled program alone (`<name>.nomic` in the working directory by default), for a `nomi-vm` that does not carry it, or for a reactor to load ([Calling exported functions](#calling-exported-functions)). It needs no `nomi-vm`, and the same file runs on every platform's `nomi-vm`.

- It records the program's exported functions, so a program with an `export fn` and no `fn main` builds too. `nomi-vm` and `nomi run` refuse to run such a program, with `` this program has no `fn main`; it is called through its exported functions ``, and exit 1.
- It refuses what `nomi build` refuses, with the same text.
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

`nomi build-vm [--target <os>/<arch>] [--compiler | --no-network] [-o <out>]` writes a copy of the `nomi-vm` that `nomi build` appends programs to, with no program: `nomi-vm` by default, with `.exe` or `.wasm` as the target needs. `--no-network` writes the smaller `nomi-vm` that `nomi build` uses for a program that makes no network or WebAssembly call: it runs any other program, and refuses one that reaches the network or a WebAssembly module before running it. It comes from where `nomi build` gets one. For `js/wasm` it also writes `wasm_exec.js` and `nomi-node.mjs` beside it, as `nomi build` does. For `wasip1/wasm` it writes the command module.

`nomi build-vm --reactor [--compiler | --no-network] [-o <out>]` writes `nomi-vm-reactor` with no program instead (`nomi-vm-reactor.wasm` by default), which takes its program through `nomi_load` ([Calling exported functions](#calling-exported-functions)). It writes `nomi-wasi.js` and `nomi-wasi.d.ts` beside it, the JavaScript host that runs it in a browser or Node, creating the folder they go in if it does not exist. The reactor exists only for `wasip1/wasm`, so that is its target, and `--target` with any other value is a usage error.

`nomi build-vm` takes no program file, and neither form takes `nomi build`'s `--no-vm`, `--web` or `--static`: a program with an `export fn` built with `nomi build --target wasip1/wasm` is already a reactor with that program inside it. `--compiler` writes the `nomi-vm` or reactor that links `std::compiler`'s hosts, which a compiled program importing `std::compiler` needs. It has no lean build, so `--compiler` and `--no-network` do not go together.

A `nomi-vm` with no program of its own takes one when it starts:

```console
$ nomi-vm <program.nomic> [args...]
$ nomi-vm [-] [args...]          # the .nomic file framed on stdin
$ nomi-vm --version
$ nomi-vm --help
```

Given a path, it reads the `.nomic` file there and passes the words after the path to the program as `startup.args`. Given no path, or `-`, it reads one `.nomic` file from the start of standard input, using the header's length, and reads nothing past its last byte. The rest of standard input is the program's own input, and the words after `-` are its arguments. So `cat app.nomic input.txt | nomi-vm` runs `app` over `input.txt`. Stdin is the way to hand a program to a host with no filesystem, such as a browser's WASI shim.

- A `wasip1/wasm` `nomi-vm` takes a path through a preopened directory (`wasmtime run --dir=. nomi-vm.wasm app.nomic`), and a `js/wasm` one under its loader reads a path through Node's `fs` (`node nomi-node.mjs nomi-vm.wasm app.nomic`). Either takes the program on stdin.
- A `nomi-vm` whose terminal is its standard input, or whose standard input is empty or not a `.nomic` file, prints its usage. A word starting with `-` other than these is a usage error.
- A `nomi-vm` that `nomi build` appended a program to runs that program and treats every argument as the program's, so `./app --version` passes `--version` to `app`.

`nomi-vm` refuses a compiled program whose format version differs from the one it reads, before it runs anything: `` app.nomic was written by nomi v0.0.1 in program format 17, and this nomi-vm (nomi v0.0.2) reads format 18 ``, followed by how to fix it. Programs written by different versions of `nomi` in the same format run. It also refuses a program with a flag it does not know, and a program importing `std::compiler` when it does not link that module's hosts. A damaged or truncated file is an error naming it. All of these, and every usage error, exit 1, as a failed program does.

`nomi run <file.nomic> [args...]` runs a compiled program in the `nomi` process, as `nomi-vm` would, with `std::compiler`'s hosts linked. `nomi run` recognizes a `.nomic` file by its first eight bytes, whatever its name.

### A program in the browser

`nomi build <file> --web [-o <dir>] [--static <dir>]` writes a folder that any static file host can serve, which runs the program's `main` in a browser. The default folder is `<name>-web` in the working directory. It holds the generated files and the author's static files:

| File | What it is |
|---|---|
| `app.wasm` | The `js/wasm` module `--target js/wasm` writes, with its memory capped at 1 GiB. |
| `nomi.js` | An ES module: the loader and its `run` function, with the js/wasm support file `wasm_exec.js` that its `nomi-vm` needs inside it. Each run starts this same file as its module worker. |
| `index.html` | A page that runs `main` with an output pane and an input line. |
| `locales/<tag>.nomil` | For a program that lists its locales, one file per listed locale `[i18n].web_embedded` leaves out: its CLDR data and translations, which the program loads with `Locale.load` ([Locales in the browser](#locales-in-the-browser)). |

`nomi.js` runs `wasm_exec.js` only where a program runs: in the worker, or on the page for a `worker: false` run. A page whose programs run in workers gets none of the globals `wasm_exec.js` sets (`Go`, and `fs`, `process` and `path` when missing). Since the worker is a file the host serves and not a `blob:` URL, a policy of `worker-src 'self'` allows it.

The static files are the files of a `web/` folder: the one at the root of the program's project, beside its `nomi.toml`, or, for a file outside a project, the one beside the file. `--static <dir>` names another folder in its place; it must exist, while a missing `web/` folder only means there are no static files.

- Every file under the folder is copied to the same path in the output, subfolders included (`web/img/logo.png` becomes `img/logo.png`), except files and folders whose names start with a dot.
- A static file wins over a generated one of the same name, so `web/index.html` replaces the generated page; such a page loads `nomi.js` and calls `run` itself.
- A static `app.wasm` or `nomi.js` is refused, naming the file: `` nomi build --web: web/app.wasm: the build writes app.wasm, so a static file cannot have that name ``. So is a static file in `locales/` ending in `.nomil`, which the build writes, and an output folder inside the static folder.
- A rebuild replaces only the generated files and the static files it copies. Anything else in the output folder stays, such as a host's `_headers`, and so does a file copied by an earlier build whose source has since been deleted from `web/`.
- A rebuild removes each locale file an earlier build wrote that it does not write. It also removes a `wasm_exec.js`, `nomi-web.js` or `nomi-worker.js` whose contents are exactly what an older `nomi build --web` wrote there, and leaves a file of that name with any other contents. A static file may have one of those names, and is copied like any other.

`--static` without `--web` is a usage error, and so is `--web` with a `--target` other than `js/wasm`. A program importing `std::compiler` builds, and its `app.wasm` is larger because its `nomi-vm` links the front end; `nomi build` says how to serve a module over 20 MB.

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

`run` fetches and compiles the module once per URL; every later run of it reuses the compiled module. Each run gets a fresh instance in its own worker, so a page stays responsive while a program loops, and `timeoutMs` and `signal` can stop it. The run then resolves with exit code 124 (timeout) or 130 (abort) and a `fatal` text. `worker: false` runs on the page's own thread, one run at a time; a timeout or abort then resolves the run and drops its output, but cannot stop the program.

- Output reaches the callbacks as written, so a prompt written with `io.write` and no newline appears before the program waits for input.
- Standard input is a string, served and then ended; an async function, asked for one line at a time, with `null` ending the input; or absent, so `io.read_line` returns `Ok(None)` at once.
- The environment holds what `env` gives plus `NOMI_COLOR=never`, unless `env` sets it, so diagnostics carry no colour codes. Nothing comes from the page's URL or cookies. Arguments and environment may be any size.

The exit code is `nomi run`'s: 0, an `Exit`'s code (spec §15 *Entry callbacks*), or 1 for a `main` returning any other `Err`, a trap or a failed assertion. A deadlock among the program's tasks is the program's fault, exit code 1, with its report (spec §16 *Deadlock*). When the runtime stops (an allocation past the 1 GiB cap, or every goroutine asleep in a wait that report does not cover), the run resolves with exit code 2 and the runtime's own line, such as `fatal error: out of memory`, as `fatal`.

The browser build's limits apply as under `--target js/wasm`: the program's file system is in memory ([std::fs](/reference/fs/)), a call nests at most 1,000 deep, and no signal arrives. The file system starts with the files `files` gives, by path from its working directory `/work`. The run resolves with `files`, every file in it when the program ended, by the same paths (absolute for a file outside `/work`); a run stopped by `timeoutMs` or `signal` has no `files`. The clock, `std::random`, tasks, channels and `sleep` work. Tasks share the worker's one thread, so a task that never yields starves the others.

`index.html` loads `nomi.js` with a `<script type="module" src>` and no inline script, so it works under a strict `script-src`. `nomi.js` mounts itself on every element with a `data-nomi-program="app.wasm"` attribute: an output pane with standard error in a distinct colour, an input line that is enabled while the program waits for a line (Enter sends it, Ctrl-D on an empty line ends the input), the exit status, and a button that runs the program again. A page that imports `run` itself needs no such element.

The page needs a server, since a browser does not fetch modules from `file:` URLs: `python3 -m http.server -d hello-web` serves one locally. Serve `app.wasm` as `Content-Type: application/wasm`, compressed with brotli or gzip (a hello world is 11 MB, 2 MB with brotli); Cloudflare Pages, Netlify and Vercel do both themselves. Without that content type the loader still works, compiling from the whole response instead of streaming it.

`nomi run --web [--port N] [--open] [--static <dir>] <file>` is the loop for working on such a program:

- It builds the generated files into a temporary directory and serves them, with the static files served from where they are, by the same rule and with the same precedence and refusals as `nomi build --web`.
- It serves on `http://localhost:<port>/`, on port 8000 or the next free one up to 8099 unless `--port` names one (`--port 0` takes any free port), with `app.wasm` as `application/wasm`, the locale files as `application/json`, and every response marked `Cache-Control: no-cache`. It prints the URL; `--open` also opens it in the default browser.
- It watches the entry, every file the program loads and the project's `nomi.toml`, polling them every 250 ms, and rebuilds once a burst of changes has settled. The page reloads itself after a successful rebuild. It polls the static folder too, and a change there reloads the page without a rebuild.
- A failed build is printed on the terminal and shown over the page, which keeps running the last build that succeeded until the error is fixed; when the first build fails, the page shows the error alone. A static file given a generated name while the server runs is reported the same way and not served.
- Every `.html` page served, the generated one or a static one, gets one more script, `/__nomi/dev.js`, added before its `</head>` (or at its end when it has none). It listens to the server-sent event stream `/__nomi/events`. Each event is `{"build": n, "static": m, "error": "..."}`: `n` counts the successful builds and `m` the changes to the static files, and a page reloads when either differs from the one it was served at. `nomi build --web` never writes any of this.
- Ctrl-C stops the server and removes the temporary directory.

The browser program takes no arguments, so a word after the file is a usage error. `--port`, `--open` and `--static` without `--web` are usage errors too, and so is `--watch` with `--web`, since `--web` already rebuilds on every change. Without `--web`, `nomi run` takes only `--watch` ([`--watch`](#running-again-on-every-change---watch)), `--profile` ([`--profile`](#where-the-time-goes---profile)) and `--offline` before the file, and every word after the file is the program's.

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
`nomi.js` with a typed `call` overload for each exported function, and a
declaration for each type the functions name. A type maps as
[`nomi types`](#typescript-types-nomi-types) maps it, except an `Int`, which
is `number | bigint`, since nomi.js hands JavaScript an Int past
`Number.MAX_SAFE_INTEGER` as a BigInt.

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
  | `nomi_call_with` | `(name_ptr, name_len, args_ptr, args_len, opts_ptr, opts_len: i32) -> i64` | `nomi_call` with options, a JSON object ([below](#calling-exported-functions)) |
  | `nomi_free` | `(ptr, len: i32)` | releases a buffer from `nomi_alloc` or a result another export returned |
  | `nomi_main` | `() -> i32` | runs `main` over WASI stdin, stdout and stderr and returns the exit status `nomi run` would exit with |
  | `nomi_load` | `(ptr, len: i32) -> i64` | boots the compiled program (`.nomic`) at `ptr` in a reactor that has none, or with `len` 0 the program built into the module; returns its result as `nomi_call` does |
  | `nomi_load_with` | `(ptr, len, opts_ptr, opts_len: i32) -> i64` | `nomi_load` with options, a JSON object ([below](#calling-exported-functions)) |

  Besides WASI's, it imports one function, which the runner must supply
  whatever the program declares: `nomi.host_call(key_ptr, key_len, args_ptr,
  args_len: i32) -> i64`, through which it answers the program's `host fn`s
  ([below](#calling-exported-functions)). A runner with none to answer
  passes `{ nomi: { host_call: () => 0n } }` beside the WASI imports.

The result is UTF-8 JSON: `{"ok": value}` on success, with `null` for a
`Unit` result, and `{"error": "diagnostic"}` on a trap, a `FromJson` failure
or an argument list that is not a JSON array of the right length. The module
stays usable after an error. A name the program does not export is an error
result naming the exported functions. `nomi_call` copies the name and the
arguments before it runs anything, so the runner may free them as soon as it
returns; a result stays where it is until `nomi_free` releases it.

A runner in JavaScript, after instantiating the module with its WASI
imports and `nomi.host_call`:

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

The program boots on the first `nomi_call` or `nomi_main`, and every later
call runs on that booted program, so a `once` value an earlier call forced is
not computed again. A boot that fails makes every `nomi_call` return its
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
bytes before it returns, boots the program, and returns like `nomi_call`:

- `{"ok": ["add(a: Int, b: Int) -> Int", ...]}` on success: the program's
  exported functions as `export fn` declares them, without `export fn`, in
  declaration order. A program with only a `main` returns `{"ok": []}`.
- `{"error": "..."}` when the bytes are not a `.nomic` file (`` the loaded
  program: not a compiled Nomi program (.nomic) ``), are cut short or carry
  bytes after the program, are in a program format this reactor does not
  read (the text `nomi-vm` gives, [above](#compiled-programs-and-nomi-vm)), import `std::compiler` in a reactor
  that does not link its hosts, hold neither a `fn main` nor an `export fn`,
  or the program's `boot` fails.

After a successful load, `nomi_call` and `nomi_main` work on the loaded
program exactly as on an embedded one. A failed load leaves the reactor with
no program, so the runner may load again. A reactor with no program returns
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
first `nomi_call` would, and returns as a load does: the exported functions'
signatures, or the boot's error. A runner calls it to learn what it can call
before it calls anything; calling it again returns the same. In a reactor
with no program it is the error result `` this reactor has no program built
in: pass it a compiled program (.nomic) ``, and the reactor stays ready for
a load.

**Options: the effects a script may have, and how long it may run.** A runner
that loads scripts it did not write passes options to `nomi_load_with` and
`nomi_call_with` as a JSON object in a buffer from `nomi_alloc`, such as
`{"std": ["std::io"], "timeout_ms": 500}`. An empty buffer, or `{}`, is no
options, so `nomi_load_with` with none is `nomi_load`. A key the export does
not take is an error result naming it: `` nomi_load_with has no option
"allow"; its options are "std", "host", "host_calls" and "timeout_ms" ``.

- `"std"` (`nomi_load_with`): the effects the program may have, named
  as an [effects list](#effects-lists)'s `std` names them (`std::io`, `std::fs`, `std::startup`). An empty array allows
  no effects; without the key these calls are not held. The load refuses,
  before the program runs anything, a program that has an effect
  the names do not allow from `main`, the boot or any exported function.
  The error result names each call at the file and line the program was
  built from, and says how it is reached:

  ```console
  greeter.nomi:4: this program has the effect std::io, and the host allows no effects
  greeter.nomi:4: help: reached from `hello`
  greeter.nomi:4: help: add "std::io" to the names the host passes to nomi_load_with's "std", or remove the call
  ```

  A name listed twice, a name of another form and a `host:` name are
  errors: a host function's key goes in `"host"`. So is a name that is no std
  module, in the words `nomi.toml`'s lists use: `` nomi_load_with's "std"
  names std::fss, which is not a std module (did you mean "std::fs"?) ``.
  A refused load leaves no program, so the runner may load again.
- `"host"` (`nomi_load_with`): the keys of the host functions linked into
  the runner that the program may call, each as its table registers it,
  without `host:` (`["lookup"]`). A refusal names such a call
  `host:lookup`. With `"std"` given and `"host"`
  not, no host function is allowed; `"host"` alone holds only host
  functions. A key that neither a linked table nor `"host_calls"` answers is
  an error.
- `"host_calls"` (`nomi_load_with`): the keys of the program's own `host
  fn`s the runner answers through `nomi.host_call` ([below](#calling-exported-functions)).
  Listing a key does not allow it: with `"std"` or `"host"` given, `"host"`
  must name it too.
- `"timeout_ms"` (both): a whole number of milliseconds above 0. On
  `nomi_load_with` it bounds the boot, and it is the bound of every later
  `nomi_call`, `nomi_call_with` and `nomi_main`. On `nomi_call_with` it
  bounds that call in place of the load's. A call past its bound stops: a
  computing program stops at its next call, loop iteration or iteration
  element, a waiting one wakes, and the tasks the call started stop with
  it. The call returns `` {"error": "the call to `forever` was stopped after
  500 ms, its timeout_ms"} ``, and `nomi_main` writes `` main was stopped
  after 500 ms, its timeout_ms `` to stderr and returns 1. A stopped load is
  `` the load was stopped after 500 ms, its timeout_ms `` and leaves no
  program.

After a stopped call the program stays loaded and takes more calls. The
stop unwinds only that call: what the boot built is untouched, a `once`
value the call was computing is computed again by the next call that reads
it, and a `once` value an earlier call computed is kept. The module reads
the clock as the program runs, so a loop that never waits is stopped too,
though the module has one thread. Options for a program built into the
module apply only when the call that boots it is an empty `nomi_load_with`;
after an earlier call has booted it, they are an error result.

**Host functions: a program that calls its runner.** A program's own `host
fn`s are answered by the runner that loads the reactor, through the
reactor's import `nomi.host_call(key_ptr, key_len, args_ptr, args_len: i32)
-> i64`, for the keys `nomi_load_with`'s `"host_calls"` lists. A key is the
one spec §19 *`host` declarations and embedders* gives each `host fn`:
`greet` in the entry file, `helpers/util.greet` for
`helpers/util.nomi`, a dependency's file under the dependency's name. The
reactor passes the UTF-8 key and the arguments as a JSON array, each
encoded by its type's `ToJson`, as an exported function's result is. The
runner writes its answer into a buffer it reserves with `nomi_alloc` from
inside the import, and returns where it is as `nomi_call` does; the
reactor reads the answer and releases the buffer. The answer is `{"ok":
value}`, decoded by the result type's `FromJson` (`null` for a `Unit`
result), or `{"error": "message"}`, which fails the Nomi call as a trap
with that message, at the call's line (`line 36: the host refused boom`).
That call returns its error result, and the program takes more calls. An
answer that is neither, or a value the result type does not accept, fails
the call the same way, saying so. Each crossing is the effect
`host:<key>`, held by `"host"` as a linked table's function is.

A runner in JavaScript answers them from a table of functions:

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

and then loads the program with `{"host_calls": ["greet"]}` as
`nomi_load_with`'s options. The load fails, before anything runs:

- for a `host fn` that no linked table answers and `"host_calls"` does not
  list, naming its key: `` no host table of this runner answers greet, a
  `host fn` the program declares; list its key in nomi_load_with's
  "host_calls" ``;
- for a listed one whose signature has no JSON form: a parameter whose
  type has no `ToJson`, such as a function value (a host here cannot call
  back into Nomi), an opaque `host type` or a stream, or a result whose type
  has no `FromJson`: `` nomi_load_with's "host_calls" lists "apply", and
  parameter `f` of host function `apply` cannot cross as JSON: `(Int) ->
  Int` does not implement `ToJson` ``. Such a function is answered only by
  a Go program that embeds Nomi.

A `host fn` that returns a `Result` takes it inside the envelope, as
`ToJson` writes one: `{"ok": {"Ok": value}}` or `{"ok": {"Err": error}}`.
The program handles that `Err` as a value, and `{"error": "message"}` still
traps.

The reactor has one thread, so the runner's function runs synchronously
inside the Nomi call, and nothing can stop it partway: a `"timeout_ms"`
bounds the Nomi code around it, not the host function. A key the program
declares no `host fn` for is not an error, so one runner can list every
function it has for every script it loads. `nomi build --no-vm` and `nomi
build --target wasip1/wasm` of a program with an `export fn` build a
program whose `host fn`s no host table answers, for its runner to answer;
`nomi check`, `nomi run` and `nomi test` refuse such a program, since they
have no host to answer them.

`nomi build-vm --reactor` also writes `nomi-wasi.js` beside the reactor,
with its TypeScript declarations in `nomi-wasi.d.ts`, and `nomi build
--target wasip1/wasm` writes both beside a reactor it embeds a program in: the toolchain's
JavaScript host for this ABI, an ES module that runs in browsers and in Node
with no WASI library. It
implements the WASI preview1 imports the reactor needs (no preopened
directories; an import it does not know returns `ENOSYS`) and
`nomi.host_call`, and over the ABI
above it offers `loadRuntime(reactor)`, which compiles the module once;
`runtime.load(nomic)`, which instantiates it, calls `nomi_load`, and resolves
to a program whose `exports` are the load's signatures and whose
`call(name, ...args)` JSON-encodes the arguments, calls `nomi_call`, and
resolves with `ok` or rejects with an `Error` carrying `error`; and
`runtime.run(nomic, {stdin:, args:, env:})`, which loads into a fresh instance,
calls `nomi_main`, and resolves with its exit status and output. Both take
`std`, `host` and `timeoutMs`, which they pass to `nomi_load_with` as
`"std"`, `"host"` and `"timeout_ms"`: `runtime.load(nomic, {std: [], timeoutMs: 500})`.
They also take `hosts`, an object from each `host fn`'s key to a function
that takes its arguments and returns its result as JSON values, whose keys
they pass as `"host_calls"` and which their `nomi.host_call` calls, a
thrown Error failing the Nomi call with its message:
`runtime.load(nomic, {hosts: {greet: (name) => "hi, " + name}})`. Without
`nomic`, `load()` and `run()` call `nomi_load` with length 0, so they use
the program built into the module, and reject with that error result on a
reactor that has none; a `.nomic` handed to a reactor with a program built
in rejects with the refusal above. A WASI call
is synchronous, so a sleeping task blocks the calling thread for its duration
(with `Atomics.wait` where the thread may block, by spinning elsewhere).

The reactor's exports are the same for every program: a program's own
function names travel as data, in `nomi_call`'s name, rather than as
per-function exports such as `guess(i32, i32) -> i32`.

### A program's locale data

A program carries the CLDR data of the locales its `nomi.toml` lists under
`[i18n].locales` ([`[i18n]`](#i18n)), and nothing else. A program with no
`[i18n]` table, a script outside any project among them, carries every locale
CLDR has data for. A locale the program does not list still formats, as its
nearest listed parent or else as the source locale, so it is not an error at
run time; a `Locale"…"` literal of one is a compile error.
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

`std::calendar` carries the IANA time zone database (≈ 450 KB), so zone
lookups (`America/New_York`, `Asia/Tokyo`, …) work on every host without
requiring system-provided tzdata.

#### Locales in the browser

A site may offer many locales while each reader reads one, so `nomi build
--web` does not put them all in `app.wasm`. It compiles in the locales
`[i18n].web_embedded` names (the source locale unless it says otherwise),
with CLDR's root, every listed locale's plural rules and grammatical
features, and every phrase's source text, and writes each other listed
locale to `locales/<tag>.nomil` beside it: that locale's numbers and lists
and its phrases' and nouns' translations, with those of each ancestor on
its fallback chain that is not embedded either, so one file serves the
locale (`pt-PT`'s holds `pt`'s too). Every locale is still checked when the
program is built. A program that lists no locales, a program with no
phrases that formats nothing, and every other kind of build carry every
locale in the program.

A program shows a page in a locale only after
[`Locale.load`](/reference/locale/#localeload) has fetched its file, which
the calling task waits for. A file another build wrote, such as one a
browser cached from the last deploy, is refused: the image and its files
share a build id. Anywhere but a browser build `Locale.load` returns at
once.

## When code cannot run yet

A program that reaches a function the compiler cannot compile yet fails before its first effect:

- `nomi run` prints an error at the code the compiler stopped at, with its source line and any hint, such as ``this call to `skip_odd` is not supported yet, so `fn evens` cannot run``, and exits 1. The last hint of every such error says the gap is in Nomi, not the program, and links the issue tracker.
- `nomi check` compiles the program the same way without running it and reports the same errors for everything `main` or a test reaches.
- `nomi test` runs every case it can and reports each case it cannot as `BLOCKED <file> :: <case> <path>:<line>:<col>: <message>`, with any hint indented on the line below. The location is the code the compiler stopped at, in the file that holds it: a module the test imports, or, for a standard library module's cases, the stdlib file whose function the case reaches. It appends `, K blocked` to its summary when K is not zero, and exits nonzero when anything failed or was blocked. A program the checker rejects is never blocked: `nomi test` fails the file with the errors `nomi check` reports, including an error in another standard library module that a stdlib module's cases reach.
- A run stopped by something else (a host function no host table answers, or a VM limit) prints `BLOCKED <file> [<function>] <reason>` lines instead.

Setting `NOMI_DEBUG_LOWERING=1` adds the compiler's own reason to each of these errors as a hint.

## Environment variables

These variables change what `nomi` does.

| Variable | What it does |
|---|---|
| `NOMI_ENV` | `nomi test` sets it to `test` unless it is set ([The test environment](#the-test-environment)). Programs read it from `startup.env`. |
| `NOMI_TOOL_PACKAGE` | `nomi tool <package> <name>` sets it to `<package>`, the name the project gives the tool's package, which the tool reads its table in `nomi.toml` by and imports the package by in code it generates ([Tools](#tools-nomi-tool)). |
| `NOMI_MAIN_PACKAGE` | `nomi tool` sets it to the project's directory, so the tool, and any process it starts, builds with the project's dependencies rather than the ones its own package's `nomi.toml` names. |
| `NOMI_COLOR` | `always` (or `1`, `true`, `yes`, `force`) colours diagnostics and test reports even into a pipe; `never` (or `0`, `false`, `no`) never colours them. Otherwise `NO_COLOR` or `TERM=dumb` turns colour off, `CLICOLOR_FORCE` turns it on, and colour goes only to a terminal. A program's `terminal.supports_color` follows the same rule. |
| `NOMI_DIAGNOSTICS` | `short` prints each diagnostic as one `path:line:col: message` line, for every command. |
| `NOMI_ALL_CALLS` | Any value prints every call `[effects]` lists refuse, not the first of each name, as `--all-calls` does ([Effects lists](#effects-lists)). |
| `LISTEN_PID`, `LISTEN_FDS`, `LISTEN_FDNAMES` | systemd's socket activation, which other service managers and restarting parents follow too. When `LISTEN_PID` is the process's id, `net.inherited_listeners` and `udp.inherited_sockets` take the `LISTEN_FDS` sockets passed from descriptor 3 on, named by `LISTEN_FDNAMES`, in a program `nomi run` runs or one `nomi build` wrote. Not on Windows. |
| `NOMI_HASH_SEED` | Fixes the seed a run keys its hashes by, which it otherwise draws at random when it starts: any text, and the same text gives every run the same `Hashable.hash` of a String, a collection or a struct. `nomi` and a built program both read it. Use it to reproduce a run that printed a hash; a program that keeps a seed fixed lets whoever knows it choose keys that collide. |
| `NOMI_DEBUG_LOWERING` | Any value but `0` adds the compiler's own reason to each "not supported yet" error ([When code cannot run yet](#when-code-cannot-run-yet)), which helps a report of the gap. |
| `NOMI_GO` | The `go` command, or a Go root, that a `nomi` built from source builds a `nomi-vm` with, ahead of the `go` on `PATH` ([Installing Nomi](install.md)). |
| `NOMI_COMPILER_SOURCE` | A checkout of the compiler that a `nomi` built from source builds a `nomi-vm` from, in place of the checkout or the version it was built from ([Installing Nomi](install.md)). |
| `NOMI_RUNNER_CACHE_ROOT` | The directory that holds the `nomi-vm` runners `nomi build` builds or downloads, in place of `<user cache>/nomi/builds`. The cache is safe to delete. |
| `NOMI_RUNNER_CACHE_MAX_ENTRIES` | How many runner directories that cache keeps before it removes the oldest (256 unless set); zero or less keeps every one. |
| `NOMI_PACKAGE_CACHE` | The directory, in place of `<user cache>/nomi/packages`, that holds each repository fetched for a project's dependencies, extracted at its tag ([Dependencies](#dependencies)). Its files are read-only. |
| `NOMI_STD_CACHE_ROOT` | The directory, in place of `<user cache>/nomi`, under which the language server writes the standard library's source for go-to-definition into std. |
| `NOMI_ARTIFACT_CACHE` | The directory, in place of `<user cache>/nomi/artifacts`, where `nomi` keeps what it compiles once and reads back in later runs: each program `nomi run`, `check`, `test` and `build` compiled ([The artifact cache](#the-artifact-cache)), the machine code of each WebAssembly module a program compiled, which a built program reads and writes too, and for a `nomi` built from source the standard library's interfaces and its lowering, which a release embeds instead. `off` keeps nothing. The cache is safe to delete. |
| `NOMI_DEBUG_ARTIFACT_CACHE` | `1` reports on stderr whether each command read a kept program, and why not, and whether it kept one. |
| `NOMI_PROFILE` | `1` profiles a run as `nomi run --profile` does and prints the table on standard error when it ends; a file name also writes the profile there in pprof's format, as `--profile=<file>` does ([`--profile`](#where-the-time-goes---profile)). It works for `nomi run` and for an executable `nomi build` wrote. |

## Editors and debugger

`nomi editor install <editor>` sets up an editor
([Setting up an editor](#setting-up-an-editor-nomi-editor)). Each
editor's README has the rest:
[VS Code](../editors/vscode/README.md),
[Neovim](../editors/nvim/README.md) and
[Helix](../editors/helix/README.md). The Zed extension is
[`editors/zed`](../editors/zed/extension.toml). An editor formats on save
with `nomi fmt` ([Formatting](#formatting)), reports what `nomi check`
reports ([Checking](#checking-nomi-check)) through the language server
`nomi lsp`, debugs through `nomi dap` ([Debugging](#debugging)), and a
coding agent asks the compiler through `nomi mcp`
([Coding agents](#coding-agents-nomi-mcp)).

### Setting up an editor: `nomi editor`

`nomi` carries each editor's Nomi support and writes it where the editor
reads it, so the editor's files match the compiler's version and need no
clone of the repository:

```console
$ nomi editor install helix
$ nomi editor install nvim
$ nomi editor install vscode
$ nomi editor install zed
```

| Editor | What `install` does |
|---|---|
| `helix` | Writes the queries and the grammar's C into Helix's configuration directory (`~/.config/helix`, `$XDG_CONFIG_HOME/helix`, or `%AppData%\helix` on Windows), and Nomi's entries into its `languages.toml` between `# BEGIN NOMI HELIX SUPPORT` and `# END NOMI HELIX SUPPORT`, with the grammar's source that copy. Then it builds only Nomi's grammar with `hx --grammar build` and runs `hx --health nomi`. |
| `nvim` | Writes the plugin into `site/pack/nomi/start/nomi` in Neovim's data directory (`~/.local/share/nvim`, `$XDG_DATA_HOME/nvim`, or `%LocalAppData%\nvim-data` on Windows), and compiles the parser into it with `$CC`, else `cc`. |
| `vscode` | Installs the extension with `code --install-extension`. |
| `zed` | Writes the extension into `extensions/installed/nomi` in Zed's data directory (`~/Library/Application Support/Zed` on macOS, `~/.local/share/zed` or `$XDG_DATA_HOME/zed` on Linux, `%LocalAppData%\Zed` on Windows). Restart Zed to load it. |

A release `nomi` carries the VS Code extension and the Zed extension's
compiled WebAssembly. A `nomi` built from source carries neither, since
they are built with Node and with Rust; from a clone, `make
install-vscode` and `make install-zed` build them and install them the
same way.

The Zed install writes the layout Zed unpacks a registry extension into.
That layout is Zed's own and not a documented interface, and whether
Zed's extension update check ever removes an extension its registry does
not list has not been checked. An `extensions/installed/nomi` that `nomi`
did not write, a dev extension's link or a registry install, is left alone
unless `install` is given `--force`.

Neovim and Helix wrap comments at `nomi fmt`'s 100 columns (`textwidth`
and `text-width`). A Zed extension cannot set a line length or indent
guides, so these go in Zed's `settings.json`: `"languages": { "Nomi": {
"preferred_line_length": 100 } }` for "editor: rewrap", and `"languages":
{ "Nomi": { "indent_guides": { "enabled": false } } }` to turn indent
guides off. Zed attaches a language server by language, not by path, so it
sends every TOML buffer to `nomi lsp`, which answers only a `nomi.toml` and
a package's phrase catalogs; `"languages": { "TOML": { "language_servers":
["!nomi", "..."] } }` stops that.

`nomi editor` with no words lists each editor, whether its support is
installed and from which version, and where it goes. `nomi editor
uninstall <editor>` removes what `install` wrote, and in Helix's
`languages.toml` only the marked block. `--dry-run` on either prints
every path it would write or remove and every command it would run, and
changes nothing. When a step cannot run, because `hx`, a C compiler or
`code` is missing, the rest is installed, the command says what is
missing and the command to run again, and it exits 1.

Each install writes `nomi-version` into the directory it owns. Run it
again after updating `nomi`.

### The language server

`nomi lsp` serves the Language Server Protocol on stdin and stdout until the
editor sends `exit` or closes its input. Every editor starts it from the
`nomi` on `PATH`, so it needs nothing else from the install. It
takes no argument but `--stdio`, the transport it always uses, which
VS Code's client adds. It logs to standard error.

It gives diagnostics, hover, go-to-definition, completion, rename
and code actions. Its diagnostics are `nomi check`'s, including code the
program reaches that type-checks but cannot be compiled to run yet, and it
reports each `todo` and `dbg` as a warning, and each string that writes
`$name` where a `name` is in scope, with a quick fix that writes `${name}`.

Go-to-definition into the standard library opens the module's file in its
source tree. Where there is none, as in an installed `nomi`, it opens a
read-only copy of this `nomi`'s standard library, written under `<user
cache>/nomi/std/<version>/` (`NOMI_STD_CACHE_ROOT` replaces `<user
cache>/nomi`). `nomi test`, `nomi check` and `nomi run` treat a file of
that copy as the module it is a copy of, as they do the module's file in
the source tree. A file of another version's copy, or one changed since it
was written, is no module of this `nomi`'s, and each refuses it in one error
that says so.

**`nomi.toml`.** Each editor sends a package's `nomi.toml` to the server,
which keeps the editor's TOML highlighting for it. The server checks the
program against the text as you type it, saved or not, and reports in the
file the errors `nomi check` reports there, each at its line: an unknown
table or key, a list name of the wrong form or listed twice, a std name
that is no std module with the nearest one, a name that is no package the
project needs or the project's own name among the dependencies, a key under
`[effects.self.modules]` that is no module of the project with the
nearest one, and `entries_required` naming a module or package with no entry. The
errors that need the program, such as a dependency with no entry, come
from the programs of the Nomi files you have open. Inside every std list
(`std` and `std_in_tests` in either table, and an entry under
`[effects.self.modules]` or `[effects.deps.packages]`), completion
offers the std modules. As a key
under `[effects.self.modules]` it offers each module of the project,
and under `[effects.deps.packages]` each dependency, with its whole
entry, the effects its own files have, written as the fix for a missing
entry writes it: `"pg" = ["std::fs"]`. The keys are the modules and packages the
programs load files from, as `entries_required` counts them, and one the
list or table already names is not offered. These come from the effects the
project's programs have, so they wait for every program to check. Hover
on a std name in a list shows the calls that list holds it for, as `nomi
effects --why` prints them, up to ten and a count of the rest: the
project's own calls under `[effects.self]`, the dependencies' under
`[effects.deps]`, and the module's or package's own under its entry. A name none
of them has says so, and find references lists the same calls. A
std name that `nomi effects --check` reports unused is a hint at the
name, saying why: nothing the list is for has the effect, only the tests
have it, or `std` already allows it to the tests. Its quick fix takes the
name out, or moves it to `std_in_tests` when only the tests have it.
Hover on a key under `[effects.self.modules]` or
`[effects.deps.packages]` shows the
effects that module's or package's own files have, as `nomi add` prints them. These come from
every program `nomi check` checks in the project, as the Nomi files are
saved, and wait for each of them to check. A module or package `entries_required`
finds with no entry has a quick fix that writes its entry with the
effects it has, as `nomi add` writes a dependency's. On a call
`nomi.toml`'s tables do not allow ([Effects lists](#effects-lists)),
the quick fix edits the key its help names: "Allow std::http for this
package in nomi.toml" adds to `[effects.self]`' `std`, "Allow std::http
for pricing in nomi.toml" to the entry of the project's module `pricing`,
"Allow std::http for fetch in nomi.toml" to the dependency's
entry, each to its table's `std` too when the call is over both, and "Allow std::fs
for dependencies in nomi.toml" to that `std` alone; each keeps the file's
comments as `nomi effects --write` does. The diagnostic clears once the edit is in the editor, before it is
saved. A call over a dependency's own list has no such fix, since the edit
is in that package. A refused call in a dependency's file is marked in the
file being checked at each of its calls that leads there, the places the
error's note names, with the call in the dependency as the mark's related
location and the same quick fix; the marks clear together. A file that
holds none of those places shows the error at its top.

**Hover.** On a keyword, hover shows the construct it starts, what it does
there and a link to the page that teaches it, following the place it
stands in: `as` in an import or a pattern, `case` as an expression or a
pipe stage, the function a `return` leaves, the iteration a `break`
leaves, what a `pub` publishes. Hover also states rules the text leaves
implicit:

- an `if` without `else` says it is a statement and when its branch runs;
- a binding's `else` says when it runs and whether it must leave;
- the `[` or `{` of a collection literal says which collection it builds
  and what expected it;
- a punned `name:` shows both the field or parameter it labels and the
  binding it reads, and go to definition on it offers both targets;
- a value that converts to a wider type (a `List<Option<Int>>` where a
  `List<Display>` is expected) ends with what the conversion does at run
  time: one pass over a collection's elements, one match of an Option,
  Result or enum, a rebuild field by field, or a wrapper around a
  function. A conversion that leaves the value as it is says nothing;
- a typed literal (`Html"<p>${name}</p>"`) says what type it is, that it
  is not a `String`, what builds it and what each `${}` takes, and a hover
  on a slot's expression ends with what that slot takes;
- a call that has an effect ([Effects lists](#effects-lists)) ends
  with the effect it has and whether `nomi.toml` allows it, naming the
  table: "Has effects: `std::http`, which `nomi.toml` allows in
  `[effects.self]`", "Has effects: `std::fs`, which `nomi.toml`
  allows in `[effects.deps]` and in the entry for
  `pricing`", or "Has effects: `std::fs`, which no table in
  `nomi.toml` holds". A read of a `once` whose initializer has
  an effect is such a place too: `io.stdout` ends with "Has effects:
  `std::io`", since its initializer asks for the standard output;
- a call that can act and has no effect a list names, since it acts only
  through a handle it is passed, ends with "Has effects on what it is
  passed.": `Writer.print(to, text)`, `Channel.send`, and a function that
  calls them with a writer it was given. A call through an interface
  value only some of whose implementations can act ends with "May have
  effects: `UserStore.find` is not pure in every implementation.";
- a function of the program or of a dependency, at its name and at each
  call of it, ends with the effects it can have, and so does a `once` of
  the program, at its name and at each read of it. One effect is said in
  full, through the functions on the way: "Has effects: `std::fs`
  through `read_settings`, which `nomi.toml` allows in
  `[effects.self]`". Several are kept short: each one the lists
  refuse is said in full, four at most, and the rest follow in one line
  by name, "Also has effects: `std::fs`, `std::io`"; `nomi effects --why`
  has the way to each. A call through an interface counts every
  implementation, as the check does. A generic function's name holds the
  effects any of its instances has, and a call of it those of that call's
  instance. A pure function has no line;
- the same places then say which app fields the function reads or
  replaces, itself or through what it calls: "Uses `App.limit`,
  `App.logger`", the first five by name and a count of the rest. An app
  field is an input, not an effect, so this line is information: no
  table holds it and no mark shows it. A function that uses none has no
  line.

These lines come from the last lowering of an open file whose program
holds the code, so they show in every file of the program and in a
dependency's file too, once the lowering the server runs in the
background after an open or an edit has finished. They cover every
function the program holds, not only those `main`, an export or a test
reaches, so a function you have just written and nothing calls yet shows
its effects too; its calls are held to the tables outside tests, and none
of them is an error, since nothing runs it. A generic function that no
call instantiates, and a function of an `impl` block over a generic type,
has its line from its body read for every type, as the check reads it,
private or not; a body the compiler cannot lower yet has none. The editors on nomi-lang.org show the same lines, from
a lowering of the block's program.

**Marks.** Bold marks a call the compiler cannot show is pure: running
it can do something besides compute its result. The server gives the
token the semantic token modifier `effect`: the name a call calls
(`read_text` in `fs.read_text(path)`, `print` in `Writer.print(to, text)`,
`report` in `report(io.stdout, 3)`), and the name of a function whose
body holds such a call, where it is declared, so the mark climbs from
the call through every caller up to `main`. It says the call can act,
not that it does: `report(buffer, 3)` with a writer into memory, as a
test passes, is marked as `report(io.stdout, 3)` is. A call is marked
when what it calls can reach a host function that is not pure, a write
through a handle it is passed included: the rule `nomi test` uses to
decide that a case is pure. A call through an interface value counts
every implementation the program has, and a call passed a function
counts that function (`Iter.each(xs, io.print)`). The hover says which
of three it is: "Has effects: `std::fs`." for an effect a list names,
"Has effects on what it is passed." for a call that acts only through a
handle or a function it is given, and "May have effects: `UserStore.find`
is not pure in every implementation." for a call through an interface
some of whose implementations are pure. A `dbg` is the call that prints:
its hover, after the keyword's text, says "Has effects: `dbg`.", with
nothing about the lists, which need no entry for it. Its keyword is not
drawn bold, since it already shows the effect on its line; a call of a
function that holds a `dbg` is, as is that function's name. An `assert`, a `refute`, a `todo`, an `unreachable` and a `?`
stop the run or leave the function and act on nothing, so they are not
marked. In `defer io.print("bye")` the mark is on `print`, as on any
call. A read is not marked, since it
gets a value rather than acting: a read of a `once` (`io.stdout`, whose
hover still names its effect), an app field, a parameter, and a function
named as a value. Neither is a `once`'s name; the call in its
initializer is. A qualifier such as `fs` is not marked. Effects lists
answer a different question: where a module or package may obtain access. One
whose entry is empty can still act through the handles it is passed, and
its calls are marked. The marks arrive with the hover lines, when the
background lowering finishes, and the server then asks the editor for the
tokens again. A program with a compile error in a function or a test keeps
the marks and lines of every function and test that checks. The one with
the error has none, and a call of it says nothing of its effects, since
they are not known until it compiles. An error outside every function and
test, such as in a type or an import, leaves no marks until it is fixed. Each editor extension draws them bold: Neovim with no
setting, VS Code in any theme that makes `markup.bold` bold, as the
default themes do, and Zed once semantic tokens are on, as for phrase
bodies below. Helix does not read semantic tokens. A theme that already
makes every function name bold shows no mark at a declaration; the hover
there still says what the function can do. To style the mark yourself, use the
modifier's name: in VS Code,
`"editor.semanticTokenColorCustomizations": { "rules": { "*.effect:nomi":
{ "underline": true } } }`; in Zed, a rule under `semantic_token_rules`
with `"token_modifiers": ["effect"]` and `"underline": true` or
`"font_weight": "bold"`; in Neovim, the highlight group
`@lsp.mod.effect.nomi`. The editors on nomi-lang.org draw the same marks
bold.

**Quick fixes.** They include:

- append `()` to a pipe stage that names a function without calling it;
- insert `then` before a lambda written as a pipe stage;
- insert `|> Display.to_string()` before a stage that takes a `String` when the piped value implements `Display`;
- put `|> Iter.take(todo)` before a terminal stage over an infinite iterator;
- write a tuple read by number by its ordinal, `t.0` as `t.first`;
- name a struct literal's values written without field names, in declaration order;
- add `else None` to an `if` without `else` whose branch is an `Option`, or write `_ = ` before a branch's value where a statement drops it;
- write missing functions, fields and `case` arms, each filled with `todo`, so the result checks;
- write a type parameter used once as a parameter's type, under one bound, as an interface value (`fn label<T>(value: T) where T: Named` becomes `fn label(value: Named)`), which the server reports as a hint.

**Refactors.** "Remove type annotation" is offered on a field or parameter
whose default already has the written type (`limit: Int = 10` becomes
`limit = 10`), and "Add type annotation" writes the type a default gives
(`limit = 10` becomes `limit: Int = 10`). Neither is offered as a fix or
run by fix-all.

### Coding agents: `nomi mcp`

`nomi mcp` serves the Model Context Protocol on stdin and stdout, so a
coding agent asks the compiler what an editor asks it. To add it to Claude
Code:

```console
$ claude mcp add nomi -- nomi mcp
```

Other clients take the same command in their MCP settings, as a `stdio`
server whose command is `nomi` and whose argument is `mcp`. The server
starts in the client's working directory, and a relative path in a tool's
arguments is read from there.

| Tool | Arguments | Returns |
|---|---|---|
| `check` | `path` | `nomi check --format json`'s records, as lists: `diagnostics` (each with its span, `hints` and any `fix`), `files` and `summary`, plus `ok`. |
| `type_at` | `file`, `line`, `col` | The hover text the language server shows there: a type, a signature, a doc comment. |
| `definition` | `file`, `line`, `col` | The `locations` the name there is declared at. |
| `references` | `file`, `line`, `col` | Every use of the name there, its declaration included. |
| `search_std` | `query`, `limit` | The standard library's public functions that match, each as a program calls it (`String.split`), with its signature and first doc paragraph. |
| `run_tests` | `path`, `line`, `all`, `changed`, `unchanged` | `nomi test --format json`'s records, with `all` and `changed` as `--all` and `--changed`: `tests` that ran or failed, each with `decided` and `pure`, `unchanged_tests`, the count of tests a stored pass answered (listed in `tests` too with `unchanged`), `files` that failed to load, `summary` with `ran`, the cases that ran, plus `ok`. |
| `tests_reaching` | `file`, `line`, `path`, `all`, `changed` | The tests under `path` (the working directory by default) whose code can run the function at that line, as `nomi test --reaching` finds and runs them: each with its file, line, name, outcome, `decided` and `pure`, then `summary` and `ok`. |
| `format` | `source` or `file` | The formatted text and whether it changed. It never writes the file. |
| `explain` | `code` | What a diagnostic's `code` means and how to fix it; with no code, the codes. |

A position is a 1-based line and a 1-based byte column, as `check` reports
them, and a location is a `file` with a span in the same form. A tool that
fails returns its error as text with `isError` set, for the agent to read.

A `search_std` query with `->` is a signature, such as `String, String ->
List<String>`. Its parameters match in any order, a parameter with a
default may be left out, and a single capital letter stands for any type:
`List<T> -> Option<T>` finds `List.first`, and so does `List<Int> ->
Option<Int>`. A query type also matches an interface it implements, so
`List<T> -> Int` finds `Iter.count`, whose parameter is an `Iter<T>`, and
`List<String> -> String` finds `String.join`; a result matches the
interface a query asks for. A refinement matches where its base is asked
for, so `List<T> -> Int` finds `Iter.count` though it returns a
`NonNegativeInt`. Exact matches come first, then generic ones, then ones
through an impl or a refinement's base. Any other query matches names and
the text of doc comments.

A test is `pure` when its result depends on its code alone ([Unchanged
tests](#unchanged-tests)), so after an edit that leaves its code as it was,
it passes as it did. A large suite answers mostly `unchanged_tests`, such
as 3 tests in `tests` and 4,213 unchanged, since an edit reaches few tests.

`check`, `run_tests` and `tests_reaching` run `nomi check` and `nomi test`
as child processes of the same `nomi`, and fetch the packages a project
needs as those commands do. The other tools run in process: the language server's
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
  `Account.deposit`, `Display.to_string for Card`), as code calls it
  (`Card.to_string`), `Display.to_string` for every impl, or file-qualified,
  and stop at its first statement in every instance of a generic function.
  The call stack, profiles, effect messages and `nomi effects` name an
  impl's function by its interface and the type it is for,
  `Display.to_string for Card`, and an inherent one as `Card.total`.
- **Exception stops.** A trap, a failed assertion and an `Err` returned from
  `main` stop by default; an uncaught task failure, where it reaches the
  `Task.await` that awaits it, can be turned on.
- **Stepping.** Step over, into and out, step into target (pick one call on
  the line, lambdas passed to `Iter` adapters included), pause, and restart
  frame. Stepping passes through the standard library unless the launch sets
  `justMyCode` to false.
- **Step back and reverse continue.** Going back replays the program from
  its start, supplying input, the clock, randomness and files from what the
  session recorded, so nothing is printed or read twice. A program that
  starts a task, a `concurrent` block, a supervisor or a channel cannot go
  back, and says why.
- **Inspecting.** The call stack shows a callback under the operation that
  called it (`main`, then `Iter.map`, then the lambda) and marks a frame a
  tail call replaced. Each frame's locals appear by their Nomi names, as a
  tree whose lists page. A struct's children are its fields, and a tuple's
  or a named tuple's are its items by the names that read them, `first`,
  `second` and on to `twentieth`; an item past the twentieth, which has no
  name, shows as `[20]`, its index counted from zero. A **Pipeline** scope shows each stage of the `|>`
  chain the frame is in, and inside `io.capture` or `io.replay` a
  **Captured output** scope shows what the code printed into the capture.
- **Evaluating.** Watches, hovers and the debug console evaluate Nomi in the
  selected frame. An expression with an effect (printing, files, the clock),
  or one that calls a function whose cost grows with its argument
  (`String.repeat`), is refused unless it ends with `// allow effects`, and
  a hover never runs one. An expression reads the app fields in force in the
  frame, which is no effect. Each evaluation stops after 1,000,000 steps or
  2 seconds.
- **Input and output.** With `console` set to `"integratedTerminal"` (the
  default for a program) the program reads and writes in the editor's
  terminal; with `"internalConsole"` (the default for tests) its output goes
  to the debug console and it reads end of input.

A launch configuration takes `mode` (`"run"` or `"test"`), `program`,
`line`, `args`, `cwd`, `env`, `console`, `justMyCode` and `stopOnEntry`;
the [VS Code README](../editors/vscode/README.md) has the table.

### Embedded files in the editor

Hover on an `embed` declaration's name, or on a use of it, shows its patterns, how many files and bytes it holds, and the first files with their sizes; hover on one pattern shows what that pattern matched. Go to definition on a pattern opens the file or folder it names, every match for a glob. A pattern that matches nothing, or that a rule refuses, is a diagnostic at the pattern: a warning when the path it names does not exist yet, an error otherwise. The server finds the files without reading them, and it rechecks the declaration when the declaring file changes, not when a file it embeds is added or removed.

### Phrase catalogs in the editor

The language server reports a catalog's errors in the catalog file, open or not, and rechecks when a catalog changes. Go to definition on a call of a phrase lands on its declaration, and go to implementations lists its catalog entries. Hover on a phrase shows its signature, its doc comment and its text in the source locale and in each listed and partial locale, naming the locale whose text a locale with no current translation shows. In a catalog it completes the file's phrase names in a table's header, the phrase's parameters after `$`, and a variant's keys: the plural categories the locale's rules select for the selector, the enum's variants, or the genders the module's nouns have; in a noun's entry, its `gender` and cases, the language's genders as its `gender`, and the cases after `case=`. Hover on a noun shows its gender and forms in each locale, and go to implementations lists its catalog entries. Its quick fixes add a listed locale's missing translation (the source text as `source`, and as `text` with every arm the locale needs), mark a stale translation current as `nomi phrases accept` does, add the plural, enum or gender arms a translation lacks, and add each form a noun lacks that the locale's phrases ask for, a copy of its nominative. An editor attaches the server to the TOML files under `[i18n].catalogs` and to `nomi.toml`; the server serves no other TOML file.

The server colors the MessageFormat 2 in a `phrase` or `noun` body and in a catalog's `source`, `text` and form strings with semantic tokens, since no editor ships an MF2 grammar: `.input`, `.local` and `.match` as keywords, `$count` as a parameter (a `.local` as a variable), `:integer` and the whole of `:nomi:noun` as functions, `count=` and `case=` as properties, keys such as `one` and `*` and name literals such as `genitive` as enum members, and the braces `{{`, `}}`, `{` and `}` as operators. The text between them keeps the string color. A body with a syntax error is colored up to the error. VS Code and Neovim show the tokens with no setting. Zed shows them only with semantic tokens on, `"languages": { "Nomi": { "semantic_tokens": "combined" } }` in its settings, and for catalogs the same under `"TOML"`. Helix does not read semantic tokens, so a body there stays one string.

Hover works inside that MessageFormat 2 too, in a body and in a catalog alike. A `$name` shows the parameter with its type and comment, a noun's `$count` what passes it, and a `.local` the expression it names. A function such as `:integer` or `:nomi:noun` shows what it writes, the types it takes, how it selects under `.match` and its options, and an option such as `case=` what it does, with the cases of the text's locale. A variant key shows its plural category in the text's locale (the source locale in a body, the catalog's in a catalog) with example numbers, `few` in Polish being 2–4, 22–24, 32–34, …; an exact key such as `0` matches only that number; a gender key names the gender; and `*` lists the categories or genders it covers that no arm names. `.input`, `.local` and `.match` show one line each.

On a `phrase` whose text has no `.match`, the refactor "Add plural forms for `$count`" is offered for each `Int` parameter, and on a `noun` for its `$count`. It rewrites the text to select on the parameter, with an arm for each plural category the source locale's rules give a whole number and the catch-all, each holding the old text for you to edit:

```nomi
phrase cart_items(count: Int) `
    .input {$count :integer}
    .match $count
    one {{{$count} items in your cart}}
    * {{{$count} items in your cart}}
    `
```

An existing `.input {$count ...}` stays. In a Polish project the arms are `one`, `few`, `many` and `*`. In a catalog the same refactor rewrites the translation or noun form under the cursor with that locale's categories.
