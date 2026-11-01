---
title: "Input & Output"
description: "Print to standard output and standard error, read input, read the arguments and environment, and exit with a status."
---

A program talks to the terminal through [`std/io`](/reference/io/): it
prints to standard output, reports problems on standard error, and reads
lines from standard input. When it ends, its exit status tells the shell
whether it worked. Files are the Libraries chapter [Files](/files/).

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
    report(io.stdout, 3)?
    report(io.stderr, 0)?
    Ok(Unit)
}
```
<!-- expect
3 items
0 items
-->

In a terminal both lines appear, but `nomi run report.nomi > /dev/null`
shows only the second. This page shows both streams in one box.

A test checks what such a function writes with `Writer.collect(fn w ->
report(w, 3))`, which hands it a writer into memory and returns the bytes.
[`Writer.from_fn`](/reference/io/#type-writer) hands each write to a function
of yours instead.

## Reading input

`io.read_line` reads one line of standard input, without its newline. It
returns `Ok(Some(line))`, `Ok(None)` when the input has ended, or an `Err`
when reading fails. A loop over the input stops at `None`:

```nomi-run
import std/io

// The lines of standard input that are not blank, until it ends.
fn count_lines(count: Int) -> Result<Int, io.Error> {
    case io.read_line()? {
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
`boot`, from its [`Startup`](/app-fields/#the-basic-shape):
`startup.args` is a `List<String>`, and `startup.env` is a
`Map<String, String>`. `boot` keeps what the program needs in its app
struct:

```nomi-run
import std/io

struct App {
    args: List<String>
}

fn boot(startup: Startup) -> Result<App, String> {
    Ok(#App{args: startup.args})
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
[Pattern Matching](/pattern-matching/#passing-failures-up) shows.

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
`?` from deep in the program if needed, ends the program as returning from
`main` does: deferred calls run and supervised tasks finish first.

[Concurrency](/concurrency/) is next. It runs several tasks at once.
