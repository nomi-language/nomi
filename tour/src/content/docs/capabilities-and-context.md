---
title: "App Fields, Defer & Context"
description: "Give a whole program settings and services through app fields, replace one for part of the program with `with`, clean up with `defer`, and set deadlines with a context."
---

A program can define a function named `boot`. It returns a struct that holds
the things the program needs everywhere, such as settings or a logger. Any
code can then read one of the struct's fields through the struct's type name,
as in `App.port`. These are called app fields.

The struct may have one field of type [`Context`](/reference/context/), with
any name you like. A context carries a deadline, explained
[below](#context-and-concurrency).

## The basic shape

```nomi-run
import {
    std/io
}

struct App {
    port: Int
    context: Context
}

fn boot() -> App {
    App{port: 8080, context: Context.root()}
}

fn main() {
    io.print("listening on :${App.port}")
}
```
<!-- expect
listening on :8080
-->

`Startup` and `Context` are available everywhere, so you don't import them.

`boot` goes in the same file as `main`. This `boot` takes no parameter. To
read the environment or the command-line arguments, declare
`fn boot(startup: Startup)` instead. `startup.env` is a
`Map<String, String>` of environment variables, read with
`Map.get(startup.env, "APP_ENV")`. `startup.args` is a `List<String>` of the
arguments. If a helper builds part of the struct, pass it what it needs from
`startup`.

`boot` makes the context with `Context.root()`, or with a narrower context
made from that one, such as one with a deadline.

The struct's fields are the app fields, with the same types. Here,
`port: Int` makes `App.port` an `Int`. You can name the struct `App`,
`Config`, or anything else. Another file that reads its fields imports it
like any other type. The context field can have any name too:

```nomi
struct Server {
    port: Int
    execution: Context
}

fn boot() -> Server {
    Server{port: 8080, execution: Context.root()}
}

fn bounded_work() {
    with Server.execution = Context.with_timeout(Server.execution, Duration.seconds(3))
    serve(Server.port)
}
```

Nomi finds the context field by its type, not its name.

`main` runs with the struct its file's `boot` returns. A test runs with the
one its group's `boot` line returns (see
[Testing](/testing/#booting-the-application)). Code that reads `Server.port`
must run under a `boot` that returns a `Server`. Code that reads no app field
runs under any `boot`, or none.

## `with` replaces a field for the rest of the block

`with App.field = value` replaces one app field from that line to the end of
the block it is in. Every function called in that part of the block sees the
new value.

The new value must have the field's type. If the field is an enum, you can
write just `.Variant`. To replace several fields, write several `with` lines.
Each line sees the values set by the lines above it.

`with` replaces whole fields of the app struct. To change one part of a
field, build an updated copy of it:
`with App.database = {..App.database, host: "localhost"}`.

```nomi-run
import {
    std/io
}

interface Logger {
    fn log(logger: self, msg: String) -> Unit
}

type Stdout
impl Logger for Stdout {
    fn log(_logger: Stdout, msg: String) -> Unit {
        io.print("[log] ${msg}")
    }
}

struct PrefixedLogger { tag: String }
impl Logger for PrefixedLogger {
    fn log(logger: PrefixedLogger, msg: String) -> Unit {
        io.print("[${logger.tag}] ${msg}")
    }
}

struct App {
    logger: Logger
    context: Context
}

fn boot() -> App {
    App{logger: Stdout, context: Context.root()}
}

fn main() {
    greet("World")
    audit_greet("Alice")
    greet("Bob")
}

fn audit_greet(name: String) {
    with App.logger = PrefixedLogger{tag: "audit"}
    greet(name)
}

fn greet(name: String) {
    Logger.log(App.logger, "hello, ${name}")
}
```
<!-- expect
[log] hello, World
[audit] hello, Alice
[log] hello, Bob
-->

When the block ends, the field gets its old value back. This happens however
the block ends: at its last line, through `return`, or through `try`. So a
function you call can't change the fields you see.

To replace a field for only part of a function, move that part into a
function of its own, as `audit_greet` does here.

A closure reads app fields when it is called, not when it is made. So a
closure returned from a function after a `with` line doesn't keep the
replacement:

```nomi-run
import std/io

struct App {
    greeting: String
}

fn boot() -> App {
    App{greeting: "hello"}
}

fn greet() -> String {
    "${App.greeting}, world"
}

fn quiet_greeter() -> () -> String {
    with App.greeting = "psst"
    io.print(greet())
    fn -> greet()
}

fn main() {
    later = quiet_greeter()
    io.print(later())
}
```
<!-- expect
psst, world
hello, world
-->

A task is different. It keeps the app fields that were in force when it was
spawned.

## `defer` runs block-scoped cleanup

`defer` runs a call when the block it is in ends. Several deferred calls run
in reverse order, last one first:

```nomi-run
import std/io

struct Conn {
    name: String
}

fn close(conn: Conn) -> Unit {
    io.print("close ${conn.name}")
}

fn main() {
    {
        first = Conn{name: "first"}
        defer close(first)

        second = Conn{name: "second"}
        defer close(second)

        io.print("body")
    }

    io.print("after")
}

```
<!-- expect
body
close second
close first
after
-->

Opening something can fail. When it returns a `Result`, open it with `try`
and then defer the cleanup: `db = try Sqlite.temp(); defer Sqlite.close(db)`.
If opening fails, `try` returns before the `defer` line runs, so there is
nothing to close. Calls deferred earlier in the block still run.

## Boot cleanup and initialization

A `defer` written directly in the body of the program's `boot` runs after
`main` and the tasks it owns have finished. In a test, a `defer` in `boot`
runs when that test finishes. A `defer` inside a function `boot` calls, or
inside a nested block, runs when that function or block ends, as usual. So
if a helper opens something and returns it, `boot` must defer its cleanup.

If `boot` fails, `main` doesn't run. In a test, the setup and body don't
run. Any work already started is shut down, and the deferred cleanup runs.

App fields can't be read until `boot` has returned. So `boot` builds the
struct from its `startup` parameter, if it has one, and from the functions
it calls.

## Context and concurrency

A [`Context`](/reference/context/) carries a deadline and values that apply to
the work done under it. `boot` returns `Context.root()`, or a narrower context
made from it. The program can still be cancelled from outside, for example by
the test runner, and the earliest deadline still applies.

To set a deadline for the rest of a block, replace the context field:
`with App.context = Context.with_timeout(App.context, duration)`. If the field
is named `execution`, write `App.execution` instead. The name makes no
difference.

The deadline limits anything that waits: timer sleeps, channel sends and
receives, waiting for a task, and flushing a supervisor. See
[Concurrency](/concurrency/) for how tasks are owned and cancelled, and
[Testing](/testing/) for how each test gets its own `boot` and clock.
