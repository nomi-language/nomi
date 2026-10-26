---
title: "Input & Output"
description: "Print to standard output and standard error, read input, read the arguments and environment, exit with a status, and read and write files."
---

A program talks to the terminal through [`std/io`](/reference/io/): it
prints to standard output, reports problems on standard error, and reads
lines from standard input. When it ends, its exit status tells the shell
whether it worked. The second half of the chapter is files, through
[`std/fs`](/reference/fs/).

## Printing

`io.print` writes a value and a newline, `io.write` writes it with no
newline, and `io.inspect` writes it the way `dbg` shows values, as Nomi
code:

```nomi-run
import std/io

fn main() {
    io.print("hello")
    io.write("no newline, ")
    io.write("so this continues the line\n")
    io.inspect(#{name: "Ada", langs: ["en", "fr"]})
    io.print(#(1, "x"))
}
```
<!-- expect
hello
no newline, so this continues the line
#{langs: ["en", "fr"], name: "Ada"}
#(1, x)
-->

`io.print` and `io.write` take any value whose type has `Display`, and
`io.inspect` takes any value at all. The last line shows the difference: a
string inside a tuple prints as its text through `Display`, and as `"x"`
through `Debug`.

## Standard error

A program's output often goes to another program or a file. Warnings and
failures go to standard error instead, so they still reach the person at
the terminal. `io.eprint` and `io.ewrite` are `io.print` and `io.write` for
standard error.

`io.stdout`, `io.stderr` and `io.stdin` are the three streams as
values (constants, so no parentheses), for a function that takes where to
write. `Writer.print` and
`Writer.write` return a `Result`, because a stream can fail, for example
when the program reading it has stopped:

```nomi-run
import std/io.{self, Error, Writer}

/// Write a report to whichever stream the caller picks.
fn report(to: Writer, count: Int) -> Result<Unit, Error> {
    to |> Writer.print("${count} items")
}

fn main() -> Result<Unit, Error> {
    try report(io.stdout, 3)
    try report(io.stderr, 0)
    Ok(Unit)
}
```
<!-- expect
3 items
0 items
-->

In a terminal both lines appear, but `nomi run report.nomi > /dev/null`
shows only the second. This page shows both streams in one box.

## Reading input

`io.read_line` reads one line of standard input, without its newline. It
returns `Ok(Some(line))`, `Ok(None)` when the input has ended, or an `Err`
when reading fails. A loop over the input stops at `None`:

```nomi-run
import std/io

// The lines of standard input that are not blank, until it ends.
fn count_lines(count: Int) -> Result<Int, io.Error> {
    case try io.read_line() {
        Some("") -> count_lines(count)
        Some(_) -> count_lines(count + 1)
        None -> Ok(count)
    }
}

fn main() {
    run = io.capture("milk\n\neggs\nbread\n", fn -> count_lines(0))
    dbg run.value
}
```
<!-- expect
dbg line 14: run.value = Ok(3)
-->

A page has no keyboard, so `main` hands `count_lines` its input with
`io.capture`, as a test does. Run with `nomi run`, `count_lines(0)` would
read what you type, or a file sent with `nomi run count.nomi < list.txt`.

## Arguments and the environment

A program reads its command-line arguments and environment variables in
`boot`, from its [`Startup`](/capabilities-and-context/#the-basic-shape):
`startup.args` is a `List<String>`, and `startup.env` is a
`Map<String, String>`. `boot` keeps what the program needs in its app
struct:

```nomi-run
import std/io

struct App {
    args: List<String>
    context: Context
}

fn boot(startup: Startup) -> Result<App, String> {
    Ok(#App{args: startup.args, context: Context.root()})
}

fn main() {
    io.print("${Iter.count(App.args)} arguments: ${String.join(App.args, ", ")}")
}
```
<!-- expect
0 arguments: 
-->

`nomi run args.nomi a b` prints `2 arguments: a, b`. Only `boot` sees
`Startup`, so the rest of the program reads the environment through the app
struct, and a test can give it different arguments with its own `boot`.

## Exit codes

A program that returns from `main` exits with status 0. One whose `main`
returns an `Err` prints `error: ` and the error, and exits with status 1, as
[Pattern Matching](/pattern-matching/#the-try-keyword) shows.

To exit with another status, return [`Exit`](/reference/process/) from
`std/process`. Its `message` is printed to standard error as it is, with no
`error: ` before it, and its `code` is the exit status:

```nomi-run error
import {
    std/io
    std/process.Exit
}

fn main() -> Result<Unit, Exit> {
    io.eprint("checking the arguments")
    Err(#Exit{code: 2, message: "usage: count <file>"})
}
```
<!-- expect
checking the arguments
usage: count <file>
exit status 2
-->

In a terminal, this prints its two lines to standard error and exits with
status 2. The page shows the status under them. `code` defaults to 1 and
`message` to `""`, which prints nothing. A code outside 0 to 255 is a trap.
A `boot` that returns an `Exit` ends the program the same way, before `main`
runs.

There is no function that exits on the spot. Returning an `Exit`, through
`try` from deep in the program if needed, ends the program as returning from
`main` does: deferred calls run and supervised tasks finish first.

## Reading and writing files

[`std/fs`](/reference/fs/) reads and writes files by path, and a path is a
`String`. `fs.read_text` returns a whole file as text, and `fs.write_text`
replaces a file with new text. Both return a `Result`, since a file can be
missing or locked.

The examples on this page run against files that live in the page, not on
your disk. Each `// FILE:` section that is not Nomi code is a file the
program starts with, so you can edit `todo.txt` below and run it again:

```nomi-run
// FILE: main.nomi
import {
    std/fs
    std/io
}

fn main() -> Result<Unit, io.Error> {
    try fs.write_text("todo.txt", "milk\n", mode: .Append)
    io.write(try fs.read_text("todo.txt"))

    try fs.write_text("done.txt", "nothing yet\n")
    io.write(try fs.read_text("done.txt"))
    Ok(Unit)
}

// FILE: todo.txt
bread
```
<!-- expect
bread
milk
nothing yet
-->

`mode` says what to do with a file that is already there. The default,
`.Replace`, empties it first. `.Append` writes at its end, `.New` fails
with `AlreadyExists` instead of touching it, and `.ReplaceAtomically`
writes a temporary file and renames it over the old one, so a program that
reads the file never sees half of the new text. `fs.read` and `fs.write`
are the same pair for `Bytes`.

The error is an [`io.Error`](/reference/io/#error), an enum whose variants
name the path they are about. A program branches on the ones it expects,
and its `Display` text is a sentence for the rest:

```nomi-run
import {
    std/fs
    std/io
}

fn main() {
    case fs.read_text("settings.toml") {
        Ok(text) -> io.print(text)
        Err(#io.Error.NotFound{path}) -> io.print("no ${path}, so the defaults")
        Err(e) -> io.eprint("cannot read the settings: ${e}")
    }

    case fs.write_text("logs/today.txt", "started\n") {
        Ok(_) -> io.print("written")
        Err(e) -> io.print(e)
    }
}
```
<!-- expect
no settings.toml, so the defaults
logs: not found
-->

A write into a directory that does not exist is `NotFound`, and it names
the directory. `fs.create_dir("logs")` makes it.

## Lines

`fs.lines(path)` is an `Iter` over a file's lines, each without its line
ending. Each item is a `Result`, since a read can fail partway through.
`Result.collect` turns the items into one `Result` holding a list, which
stops at the first error:

```nomi-run
// FILE: main.nomi
import {
    std/fs
    std/io
}

fn count_errors(path: String) -> Result<Int, io.Error> {
    fs.lines(path)
    |> Iter.reduce(fn count = Ok(0), line -> {
        case line {
            Ok(text) when String.starts_with?(text, "ERROR") -> Result.map(count, fn n -> n + 1)
            Ok(_) -> count
            Err(e) -> break Err(e)
        }
    })
}

fn main() -> Result<Unit, io.Error> {
    lines = try Result.collect(fs.lines("server.log"))
    io.print("${Iter.count(lines)} lines")
    io.print("${try count_errors("server.log")} errors")
    io.print(Iter.first(fs.lines("server.log")))
    Ok(Unit)
}

// FILE: server.log
INFO started
ERROR disk full
INFO retrying
ERROR disk full
```
<!-- expect
4 lines
2 errors
Some(Ok(INFO started))
-->

`count_errors` folds over the file a line at a time, so a log of any size
fits in memory. Each run of `fs.lines` opens the file, reads it and closes
it, so running the same iterator twice reads the file twice, and if the
file changed in between, the second run sees the change. `Iter.first` stops
after one line and closes the file as it stops. `fs.chunks(path)` is the
same for `Bytes`, a piece at a time.

## Streams

A file you read or write a piece at a time is a stream. `fs.with_reader`
opens a file as a [`Reader`](/reference/io/#reader), runs a function with
it, and closes it however the function ends. `fs.with_writer` does the same
with a `Writer`. `Reader.copy_to` copies everything left in a reader into a
writer:

```nomi-run
// FILE: main.nomi
import {
    std/fs
    std/io.{self, Reader, Writer}
}

fn prepend_header(input: String, output: String) -> Result<Int, io.Error> {
    fs.with_reader(input, fn r -> {
        fs.with_writer(output, mode: .ReplaceAtomically, fn w -> {
            try Writer.write_text(w, "name,price\n")
            Reader.copy_to(r, w)
        })
    })
}

fn main() -> Result<Unit, io.Error> {
    copied = try prepend_header("prices.csv", "with-header.csv")
    io.print("copied ${copied} bytes")
    io.write(try fs.read_text("with-header.csv"))
    Ok(Unit)
}

// FILE: prices.csv
apple,3
pear,4
```
<!-- expect
copied 15 bytes
name,price
apple,3
pear,4
-->

A writer over a file keeps what you write in memory and sends it to the
file in large pieces, so writing to a disk can fail late, when the writer
closes. That is why `Writer.close` returns a `Result`, and why
`fs.with_writer` closes the writer itself and returns a failed close as its
error. When its function returns an `Err` instead, `with_writer` gives the
write up, and with `.ReplaceAtomically` the old file stays as it was.

A stream you hold past one function closes with `Reader.close` or
`Writer.close`. A `defer` cannot report an error, so the form for one is
`defer Reader.close_quietly(r)`.

A stream goes from the start of a file to its end. For reading and
writing anywhere in a file, such as a record in the middle of a database
file, `fs.with_file` opens it as an [`io.File`](/reference/io/#file), whose
`File.read_at`, `File.write_at` and `File.seek` take a position in bytes.

Standard input is a stream too, but one that can be read only once, so it
is not an `Iter`. A loop reads it a line at a time with `Iter.loop`, as a
channel is read:

```nomi-run
import std/io.{self, Reader}

fn count_lines(r: Reader) -> Result<Int, io.Error> {
    Iter.loop(fn count: Result<Int, io.Error> = Ok(0) -> {
        case Reader.read_line(r) {
            Ok(Some(_)) -> Result.map(count, fn n -> n + 1)
            Ok(None) -> break count
            Err(e) -> break Err(e)
        }
    })
}

fn main() {
    run = io.capture("one\ntwo\nthree\n", fn -> count_lines(io.stdin))
    io.print(run.value)
    io.print(count_lines(Reader.from_text("a\nb")))
}
```
<!-- expect
Ok(3)
Ok(2)
-->

`Reader.from_text` and `Reader.from_bytes` make a reader over data the
program already holds, which is how a test feeds a function that takes a
`Reader`.

## Directories and paths

`fs.list` returns a directory's entries sorted by name, so a listing is the
same on every computer. [`std/path`](/reference/path/) puts paths together
and takes them apart. It only reads the text, so it never fails:

```nomi-run read-only:photos/*
// FILE: main.nomi
import {
    std/fs
    std/io
    std/path
}

fn main() -> Result<Unit, io.Error> {
    try fs.create_dir("photos/2026/summer", parents: True)
    try fs.write_text(path.join("photos/2026", "notes.txt"), "trip\n")

    entries = try fs.list("photos")
    Iter.each(entries, fn e -> io.print("${e.path} ${Debug.inspect(e.kind)}"))

    io.print(path.extension("photos/beach.jpg"))
    io.print(path.base("photos/2026/notes.txt"))
    io.print(path.clean("photos/2026/../alps.jpg"))
    io.print(try fs.exists?("photos/2026/notes.txt"))
    Ok(Unit)
}

// FILE: photos/beach.jpg

// FILE: photos/alps.jpg
```
<!-- expect
photos/2026 Directory
photos/alps.jpg File
photos/beach.jpg File
Some(jpg)
notes.txt
photos/alps.jpg
True
-->

Paths use `/` on every operating system, Windows included. `fs.remove`
removes a file or an empty directory, `fs.remove_all` removes a directory
and everything in it, and `fs.rename`, `fs.copy` and `fs.metadata` do what
their names say. `fs.set_mode(path, 0o755)` changes a file's permissions.

A symlink is an entry that points to another path. `fs.create_symlink`
makes one and `fs.read_link` reads where it points. Reading a file
through a symlink reads what it points to, and so does `fs.metadata`;
`fs.link_metadata` describes the link itself. `fs.remove` removes the
link and leaves what it points to.

## Walking a tree

`fs.walk` goes through every entry under a directory, depth first and in
name order: each directory, then what is in it. It does not follow
symlinks, so it never leaves the tree. `fs.glob` returns the paths that
match a pattern, where `*` matches within one name and `**` any number of
directories:

```nomi-run read-only:notes/* read-only:notes/2026/*
// FILE: main.nomi
import {
    std/fs
    std/io
}

fn main() -> Result<Unit, io.Error> {
    entries = try Result.collect(fs.walk("notes"))
    Iter.each(entries, fn e -> io.print("${e.path} ${Debug.inspect(e.kind)}"))

    io.print(try fs.glob("notes/**/*.md"))
    Ok(Unit)
}

// FILE: notes/todo.md
milk

// FILE: notes/2026/trip.md
alps

// FILE: notes/2026/budget.csv
food,40
```
<!-- expect
notes/2026 Directory
notes/2026/budget.csv File
notes/2026/trip.md File
notes/todo.md File
[notes/2026/trip.md, notes/todo.md]
-->

Like `fs.lines`, a walk lists the directories again each time it runs. A
directory it cannot list is an `Err` among the entries, and the walk goes
on, so `Result.collect` stops at the first one and `Iter.filter` can skip
them.

## Keeping paths inside a directory

A path that comes from outside the program, such as a name in an archive
or a request, can reach any file: `../../etc/passwd`. `fs.within(dir, fn
-> ...)` runs a function with every file call in it confined to `dir`. A
relative path starts there, and a path that leads outside, by `..` or
through a symlink, is an error instead of a file:

```nomi-run read-only:private.txt
// FILE: main.nomi
import {
    std/fs
    std/io
}

fn page(name: String) -> Result<String, io.Error> {
    fs.within("site", fn -> fs.read_text(name)) |> Result.map(String.trim)
}

fn main() {
    io.print(page("index.txt"))
    io.print(page("../private.txt"))
}

// FILE: site/index.txt
home

// FILE: private.txt
secret
```
<!-- expect
Ok(home)
Err(invalid path "../private.txt": the path leads outside the directory fs.within confines calls to)
-->

On the disk `fs.within` is the operating system's own confinement (Go's
`os.Root`), so a symlink changed while the function runs cannot lead out
either.

## Testing code that uses files

A test should not depend on what is on the disk of the computer it runs
on. [`std/fs_testing`](/reference/fs_testing/) runs a function against a
sandbox: a file system in memory, made from the files you give it.
`fs_testing.run` returns the function's result and every file afterwards:

```nomi-run
import {
    std/fs
    std/fs_testing.{self, Failure, Sandbox}
    std/io
}

fn sort_file(input: String, output: String) -> Result<Int, io.Error> {
    lines = try Result.collect(fs.lines(input))
    sorted = Iter.sort(lines)
    try fs.write_text(output, String.join(sorted, "\n") + "\n", mode: .ReplaceAtomically)
    Ok(Iter.count(sorted))
}

test "sorts the lines into the output file" {
    ran = fs_testing.run(#Sandbox{files: %{"in.txt" => "pear\napple\n"}}, fn -> {
        sort_file("in.txt", "out.txt")
    })

    assert ran.value == Ok(2)
    assert fs_testing.text(ran, "/work/out.txt") == Some("apple\npear\n")
    assert ran.open == []
}

test "a full disk leaves the old output in place" {
    sandbox = #Sandbox{
        files: %{"in.txt" => "b\na\n", "out.txt" => "old\n"},
        failures: [
            #Failure{path: "out.txt", operation: .Write, error: #io.Error.NoSpace{path: "out.txt"}},
        ],
    }

    ran = fs_testing.run(sandbox, fn -> sort_file("in.txt", "out.txt"))

    assert ran.value == Err(#io.Error.NoSpace{path: "out.txt"})
    assert fs_testing.text(ran, "/work/out.txt") == Some("old\n")
}
```

A sandbox's working directory is `/work`, so `ran.files` names every file
by its full path. A `Failure` makes an operation on a path fail with the
error you pick, here the write that would fill the disk. `ran.open` lists
the files still open when the function returned, so `assert ran.open ==
[]` catches a reader you forgot to close. Tasks the function starts use the
same sandbox.

For code whose behavior depends on the real disk, `fs.with_temp_dir` makes
a fresh directory, runs a function with its path and removes it afterwards.
`fs_testing.conformance(dir)` checks that a directory behaves as std/fs
promises, and std's own tests run it against a temporary directory and
against a sandbox, which is how the sandbox is kept honest.

## Output in tests

`io.capture(input, fn -> ...)` runs a function with `input` as its standard
input, and returns its result with everything it printed, standard output
and standard error kept apart. [Testing](/testing/#input-and-output)
shows it, along with `io.replay` for a conversation of prompts and answers.

## Files in the browser

A web page has no disk, so a program built with `nomi build --web` gets a
file system in memory, as the examples on this page do. The page gives it
its files when it runs it, and gets every file back when it ends, as
[Building & Shipping](/building-and-shipping/#web-pages) shows.

[Concurrency](/concurrency/) is next. It runs several tasks at once.
