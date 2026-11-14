---
title: "Files"
description: "Read and write files, a line or a piece at a time, list and walk directories, keep paths inside a directory, and test code that uses files."
---

## Reading and writing

[`std::fs`](/reference/fs/) reads and writes files by path, and a path is a
`String`. `fs.read_text` returns a whole file as text, and `fs.write_text`
replaces a file with new text. Both return a `Result`, since a file can be
missing or locked.

The examples on this page run against files that live in the page, not on
your disk. Each `// FILE:` section that is not Nomi code is a file the
program starts with, so you can edit `todo.txt` below and run it again:

```nomi-run
// FILE: main.nomi
import
    std::fs
    std::io
end

fn main() -> Result<Unit, io.Error>
    fs.write_text("todo.txt", "milk\n", mode: .Append)?
    io.write(fs.read_text("todo.txt")?)

    fs.write_text("done.txt", "nothing yet\n")?
    io.write(fs.read_text("done.txt")?)
    Ok(Unit)
end

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

The error is an [`io.Error`](/reference/io/#enum-error), an enum whose variants
name the path they are about. A program branches on the ones it expects,
and its `Display` text names the path and the problem for the rest:

```nomi-run
import
    std::fs
    std::io
end

fn main()
    case fs.read_text("settings.toml")
        Ok(text) -> io.print(text)
        Err(io.Error.NotFound{path:}) -> io.print("no ${path}, so the defaults")
        Err(e) -> io.eprint("cannot read the settings: ${e}")
    end

    case fs.write_text("logs/today.txt", "started\n")
        Ok(_)  -> io.print("written")
        Err(e) -> io.print(e)
    end
end
```
<!-- expect
no settings.toml, so the defaults
logs: not found
-->

A write into a directory that does not exist is `NotFound`, and it names
the directory. `fs.create_dir("logs")` makes it.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A relative path is read from the working directory. Under `nomi test` a
  test file's working directory is its module root, the nearest directory
  above it with a `nomi.toml`, so `fs.read_text("data/rates.csv")` reads
  the same file wherever the tests run.
- `std::fs::testing.run` and `fs.within` replace the file system for the
  function they run, and tasks it starts inherit the replacement.
- A browser build has no disk; its program gets a file system in memory
  ([Files in the browser](#files-in-the-browser)).

[`std::fs`](/reference/fs/) has every function and error, and the tooling
reference's [The test environment](/tooling/running-tests/#the-test-environment)
has the test rules.

</div>

## Lines

`fs.lines(path)` is an `Iter` over a file's lines, each without its line
ending. Each item is a `Result`, since a read can fail partway through.
`Result.collect` turns the items into one `Result` holding a list, which
stops at the first error. `Iter.try_reduce` folds them with a function that
returns a `Result`, and stops at the first `Err`:

```nomi-run
// FILE: main.nomi
import
    std::fs
    std::io
end

fn count_errors(path: String) -> Result<Int, io.Error>
    fs.lines(path)
    |> Iter.try_reduce(0, fn count, line ->
        if String.starts_with(line?, "ERROR") then
            Ok(count + 1)
        else
            Ok(count)
        end
    end)
end

fn main() -> Result<Unit, io.Error>
    lines = Result.collect(fs.lines("server.log"))?
    io.print("${Iter.count(lines)} lines")
    io.print("${count_errors("server.log")?} errors")
    io.print(Iter.first(fs.lines("server.log")))
    Ok(Unit)
end

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

`count_errors` folds over the file a line at a time, so it never holds the
whole file in memory. Each run of `fs.lines` opens the file, reads it and closes
it, so running the same iterator twice reads the file twice, and if the
file changed in between, the second run sees the change. `Iter.first` stops
after one line and closes the file as it stops. `fs.chunks(path)` is the
same for `Bytes`, a piece at a time.

## Streams

A file you read or write a piece at a time is a stream. `fs.open` opens a
file as a [`Reader`](/reference/io/#type-reader) and `fs.create` as a
`Writer`. Each is closed by a `defer` on the next line, so it is closed
however the function ends. `Reader.copy_to` copies everything left in a
reader into a writer:

```nomi-run
// FILE: main.nomi
import
    std::fs
    std::io.{self, Reader, Writer}
end

fn prepend_header(input: String, output: String) -> Result<Int, io.Error>
    r = fs.open(input)?
    defer Reader.close_quietly(r)

    w = fs.create(output, mode: .ReplaceAtomically)?
    defer Writer.discard(w)

    Writer.write_text(w, "name,price\n")?
    copied = Reader.copy_to(r, w)?
    Writer.close(w)?
    Ok(copied)
end

fn main() -> Result<Unit, io.Error>
    copied = prepend_header("prices.csv", "with-header.csv")?
    io.print("copied ${copied} bytes")
    io.write(fs.read_text("with-header.csv")?)
    Ok(Unit)
end

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
closes. That is why `Writer.close` returns a `Result`, and why the success
path ends with `Writer.close(w)?`: the close commits the write and reports
a failed close. A `defer` cannot return an error, so the deferred
`Writer.discard(w)` only gives the write up when the function leaves any
other way, by an `Err` or a trap, and with `.ReplaceAtomically` the old
file stays as it was. A discard after the close does nothing. A reader has
nothing to commit, so `Reader.close_quietly` closes it and ignores a failed
close.

The compiler checks both halves. Leave out a `defer`, and the error is
`` `r` is never released; write `defer Reader.close_quietly(r)` after it,
or return it ``; return `Ok` without the close, and it is `` `w` is
discarded here: this path returns without `Writer.close(w)?` ``. A
reader dropped unread, `_ = fs.open(path)?`, is an error too. The
editor's quick fix writes the missing lines
([Releasing resources](/spec/statements-and-control-flow/#releasing-resources)).

A stream goes from the start of a file to its end. For reading and
writing anywhere in a file, such as a record in the middle of a database
file, `fs.open_file` opens it as an [`io.File`](/reference/io/#type-file), whose
`File.read_at`, `File.write_at` and `File.seek` take a position in bytes.
Close it with `defer File.close_quietly(f)`.

Standard input is a stream too. `io.stdin` is a `Reader`, so a function
that takes a `Reader` reads standard input, a file or text alike.
`Reader.lines(r)` is a reader's lines as an `Iter`, read as the iterator
runs, so unlike `fs.lines` it runs only once. `io.lines()` is the lines of
standard input:

```nomi-run
import std::io.{self, Reader}

fn count_lines(r: Reader) -> Result<Int, io.Error>
    lines = Reader.lines(r) |> Result.collect()?
    Ok(Iter.count(lines))
end

fn main()
    run = io.capture("one\ntwo\nthree\n", fn -> count_lines(io.stdin))
    io.print(run.value)
    io.print(count_lines(Reader.from_text("a\nb")))
end
```
<!-- expect
Ok(3)
Ok(2)
-->

`Reader.from_text` and `Reader.from_bytes` make a reader over data the
program already holds, which is how a test feeds a function that takes a
`Reader`. [`std::io`](/reference/io/#type-reader) also has readers that make
their bytes as they are read.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A read that waits, on standard input or a slow stream, is a safe point. A
  task cancelled while it waits stops there, and its `defer` closes the
  stream.
- A call into Go that reads is not interrupted. The task stops once the
  call returns.
- `io.capture` and `io.replay` reach tasks the captured function starts:
  what a task prints goes to the capture, not the terminal.
- A `Writer` passed to Go is written through the same buffer as the
  program's own writes, so the two stay in order.

Spec: [Safe points](/spec/concurrency/#safe-points). [`std::io`](/reference/io/)
has the stream functions.

</div>

## Directories and paths

`fs.list` returns a directory's entries sorted by name, so a listing is the
same on every computer. [`std::path`](/reference/path/) puts paths together
and takes them apart. It only reads the text, so it never fails:

```nomi-run read-only:photos/*
// FILE: main.nomi
import
    std::fs
    std::io
    std::path
end

fn main() -> Result<Unit, io.Error>
    fs.create_dir("photos/2026/summer", parents: True)?
    fs.write_text(path.join("photos/2026", "notes.txt"), "trip\n")?

    entries = fs.list("photos")?
    Iter.each(entries, fn e -> io.print("${e.path} ${Debug.inspect(e.kind)}"))

    io.print(path.extension("photos/beach.jpg"))
    io.print(path.base("photos/2026/notes.txt"))
    io.print(path.clean("photos/2026/../alps.jpg"))
    io.print(fs.exists("photos/2026/notes.txt")?)
    Ok(Unit)
end

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

Paths use `/` on every operating system, Windows included.
[`std::fs`](/reference/fs/) also removes, renames and copies files, reads
their metadata and permissions, and makes and reads symlinks.

## Walking a tree

`fs.walk` goes through every entry under a directory, depth first and in
name order: each directory, then what is in it. It does not follow
symlinks, so it never leaves the tree. `fs.glob` returns the paths that
match a pattern, where `*` matches within one name and `**` any number of
directories:

```nomi-run read-only:notes/* read-only:notes/2026/*
// FILE: main.nomi
import
    std::fs
    std::io
end

fn main() -> Result<Unit, io.Error>
    entries = Result.collect(fs.walk("notes"))?
    Iter.each(entries, fn e -> io.print("${e.path} ${Debug.inspect(e.kind)}"))

    io.print(fs.glob("notes/**/*.md")?)
    Ok(Unit)
end

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
import
    std::fs
    std::io
end

fn page(name: String) -> Result<String, io.Error>
    fs.within("site", fn -> fs.read_text(name)) |> Result.map(String.trim)
end

fn main()
    io.print(page("index.txt"))
    io.print(page("../private.txt"))
end

// FILE: site/index.txt
home

// FILE: private.txt
secret
```
<!-- expect
Ok(home)
Err(invalid path "../private.txt": the path leads outside the directory fs.within confines calls to)
-->

On the disk the operating system enforces the confinement, so a symlink
changed while the function runs cannot lead out either.

## Testing code that uses files

A test should not depend on what is on the disk of the computer it runs
on. [`std::fs::testing`](/reference/fs/testing/) runs a function against a
sandbox: a file system in memory, made from the files you give it.
`std::fs::testing.run` returns the function's result and every file afterwards:

```nomi-run
import
    std::fs
    std::fs::testing.{self, Failure, Sandbox}
    std::io
end

fn sort_file(input: String, output: String) -> Result<Int, io.Error>
    lines = Result.collect(fs.lines(input))?
    sorted = Iter.sort(lines)
    fs.write_text(output, String.join(sorted, "\n") + "\n", mode: .ReplaceAtomically)?
    Ok(Iter.count(sorted))
end

test "sorts the lines into the output file"
    ran = testing.run(Sandbox{files: {"in.txt" => "pear\napple\n"}}, fn ->
        sort_file("in.txt", "out.txt")
    end)

    assert ran.value == Ok(2)
    assert testing.text(ran, "/work/out.txt") == Some("apple\npear\n")
    assert ran.open == []
end

test "a full disk leaves the old output in place"
    sandbox = Sandbox{
        files: {"in.txt" => "b\na\n", "out.txt" => "old\n"},
        failures: [
            Failure{path: "out.txt", operation: .Write, error: io.Error.NoSpace{path: "out.txt"}},
        ],
    }

    ran = testing.run(sandbox, fn -> sort_file("in.txt", "out.txt"))

    assert ran.value == Err(io.Error.NoSpace{path: "out.txt"})
    assert testing.text(ran, "/work/out.txt") == Some("old\n")
end
```
<!-- expect
ok sorts the lines into the output file
ok a full disk leaves the old output in place
test result: ok. 2 passed, 0 failed
-->

A sandbox's working directory is `/work`, so `ran.files` names every file
by its full path. A `Failure` makes an operation on a path fail with the
error you pick, here the write that would fill the disk. `ran.open` lists
the files still open when the function returned, so `assert ran.open ==
[]` catches a reader you forgot to close. Tasks the function starts use the
same sandbox.

For code whose behavior depends on the real disk, `fs.make_temp_dir` makes
a fresh directory, and `defer fs.remove_all_quietly(dir)` on the next line
removes it and everything in it when the function or test ends.

## Files in the browser

A web page has no disk, so a program built with `nomi build --web` gets a
file system in memory, as the examples on this page do. The page gives it
its files when it runs it, and gets every file back when it ends, as
[Building & Shipping](/building-and-shipping/#web-pages) shows.

[JSON & Dynamic](/json-and-dynamic/) is next. It reads data whose shape
comes from outside the program.
