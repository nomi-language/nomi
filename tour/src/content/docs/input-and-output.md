---
title: "Input & Output"
description: "Print to standard output and standard error, read input, read the arguments and environment, exit with a status, and run other programs."
---

A program talks to the world outside it through [`std::io`](/reference/io/)
and [`std::process`](/reference/process/). It prints to standard output,
reports problems on standard error, reads standard input, reads its
arguments and environment, and runs other programs. When it ends, its exit
status tells the shell whether it worked. Reading and writing files is the
[Files](/files/) chapter, and [HTTP](/http/) covers sending requests and
serving them.

## Printing

`io.print` writes a value and a newline, `io.write` writes it with no
newline, and `io.inspect` writes it the way `dbg` shows values, as Nomi
code:

```nomi-run
import std::io

fn main()
    io.print("hello")
    io.write("no newline, ")
    io.write("so this continues the line\n")
    io.inspect({name: "Ada", langs: ["en", "fr"]})
    io.print((1, "x"))
end
```
<!-- expect
hello
no newline, so this continues the line
{langs: ["en", "fr"], name: "Ada"}
(1, x)
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

A function that leaves the choice of stream to its caller takes a
`Writer`. `io.stdout`, `io.stderr` and `io.stdin` are the three standard
streams as values. `Writer.print` and `Writer.write` return a `Result`,
because a stream can fail, for example when the program reading it has
stopped:

```nomi-run
import std::io.{self, Error, Writer}

/// Write a report to whichever stream the caller picks.
fn report(to: Writer, count: Int) -> Result<Unit, Error>
    to |> Writer.print("${count} items")
end

fn main() -> Result<Unit, Error>
    report(io.stdout, 3)?
    report(io.stderr, 0)?
    Ok(Unit)
end
```
<!-- expect
3 items
0 items
-->

In a terminal both lines appear, but `nomi run report.nomi > /dev/null`
shows only the second. This page shows both streams in one box.

A test checks what such a function writes with `Writer.collect(fn w ->
report(w, 3))`, which hands it a writer into memory and returns what it
wrote.

## Reading input

`io.read_line` reads one line of standard input, without its newline. It
returns `Ok(Some(line))`, `Ok(None)` when the input has ended, or an `Err`
when reading fails. A loop over the input stops at `None`:

```nomi-run
import std::io

// The lines of standard input that are not blank, until it ends.
fn count_lines(count: Int) -> Result<Int, io.Error>
    case io.read_line()?
        Some("") -> count_lines(count)
        Some(_) -> count_lines(count + 1)
        None -> Ok(count)
    end
end

fn main()
    run = io.capture("milk\n\neggs\nbread\n", fn -> count_lines(0))
    dbg run.value
end
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
import std::io

struct App
    args: List<String>
end

fn boot(startup: Startup) -> Result<App, String>
    Ok(App{args: startup.args})
end

fn main()
    io.print("${Iter.count(App.args)} arguments: ${String.join(App.args, ", ")}")
end
```
<!-- expect
0 arguments: 
-->

`nomi run args.nomi a b` prints `2 arguments: a, b`. Only `boot` sees
`Startup`, so the rest of the program reads the environment through the app
struct, and a test can give it different arguments on its group's `boot`
line.

## Exit codes

A program that returns from `main` exits with status 0. One whose `main`
returns an `Err` prints `error: ` and the error, and exits with status 1, as
[Pattern Matching](/pattern-matching/#passing-failures-up) shows.

To exit with another status, return [`Exit`](/reference/process/) from
`std::process`. Its `message` is printed to standard error as it is, with no
`error: ` before it, and its `code` is the exit status:

```nomi-run error
import
    std::io
    std::process.Exit
end

fn main() -> Result<Unit, Exit>
    io.eprint("checking the arguments")
    Err(Exit{code: 2, message: "usage: count <file>"})
end
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

## Running other programs

A program runs another, such as `git` or a formatter, with a
[`Command`](/reference/process/): the program's name and its arguments. No
shell reads them, so `"a b"` reaches the program as one argument and `*` is
not expanded. `process.output` runs the command to its end and returns an
`Output`: its `status`, and the bytes it wrote to `stdout` and `stderr`.

A program that runs and fails is not an error. Its exit code is in the
status, and `Status.is_success` says whether it was 0. An `Err` means the
program could not be started at all, such as `NotFound` when nothing on the
`PATH` has its name:

```nomi-run
import
    std::io
    std::process.{self, Command, Status}
    std::process::testing.{self, Answer}
end

/// The current git branch, or `None` when git fails, as it does outside a
/// repository.
fn current_branch() -> Result<Maybe<String>, io.Error>
    command = Command{program: "git", args: ["branch", "--show-current"]}
    out = process.output(command)?

    if Status.is_success(out.status) then
        Ok(Some(String.trim(Bytes.to_string(out.stdout)!)))
    else
        Ok(None)
    end
end

fn main()
    in_repo = testing.run([Answer{program: "git", stdout: "main\n"}], current_branch)
    dbg in_repo.value

    outside = testing.run(
        [Answer{program: "git", code: 128, stderr: "fatal: not a git repository\n"}],
        current_branch,
    )

    dbg outside.value
end
```
<!-- expect
dbg line 22: in_repo.value = Ok(Some("main"))
dbg line 29: outside.value = Ok(None)
-->

The page cannot start programs, so `main` runs `current_branch` under
[`std::process::testing`](/reference/process/testing/), which responds to each command
from a list of `Answer`s instead of starting it, as a test does. For the
same reason, `nomi build` refuses to build a program that starts others for
WebAssembly.

Two other functions run a command. `process.run` lets the program write
straight to your program's own output and returns only its status, as a
build step would. `process.spawn(command)` starts the program and returns
it as a `Child`, whose input you write and whose output you read as it
goes. `defer Child.kill(child)` on the next line kills it if it is still
running when the function ends.

[Concurrency](/concurrency/) is next. It runs several tasks at once.
