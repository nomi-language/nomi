---
title: "App Fields, Defer & Context"
description: "Typed application fields, scoped overrides, startup inputs, and resource lifetimes."
---

Boot returns the application's initial environment as an ordinary struct.
Code reads one of its top-level fields by naming the type: `App.port`. An
application may declare one field of type [`Context`](/reference/context/);
its name is your choice.

## The basic shape

```nomi-run
import {
    std/io
}

struct App {
    port: Int
    context: Context
}

fn boot(): App {
    App{port: 8080, context: Context.root()}
}

fn main() {
    io.print("listening on :${App.port}")
}
```
<!-- expect
listening on :8080
-->

`Startup` and `Context` are in the prelude, so no imports are needed for either type.

`boot` lives in the entry file, beside `main`. A boot that needs no startup
input takes no parameter, as here; one that does is `fn boot(startup: Startup)`.
`Startup` supplies `env: Map<String, String>` and `args: List<String>` as
immutable snapshots, read with `Map.get(startup.env, "APP_ENV")`. Pass startup
inputs explicitly to helpers that construct the application value. Boot builds
its context with `Context.root()`, or derives a tighter one from it.

The returned struct defines the available application fields and their types.
Here, `port: Int` makes `App.port` an `Int`. The struct itself can be named
`App`, `Config`, or whatever suits your program; a file that reads its fields
imports it like any other type. The context field may have any name:

```nomi
struct Server {
    port: Int
    execution: Context
}

fn boot(): Server {
    Server{port: 8080, execution: Context.root()}
}

fn bounded_work() {
    with Server.execution = Context.with_timeout(Server.execution, Duration.seconds(3))
    serve(Server.port)
}
```

The runtime identifies the context field by its declared `Context` type.
A `main` runs under its file's boot, and each test under the boot its group
names; the boot must return the type every field read in the code it runs
names. Code that reads no application field runs under any boot, or none.

## `with` replaces a field for the rest of the block

The statement `with App.field = value` replaces one top-level application
field from that line to the end of the enclosing block, and the replacement
reaches every function called there. It is the same extent a `defer` written
on that line would have. The value keeps the field's declared type, so
`.Variant` shorthand works from the field's enum type. Several fields take
several `with` lines, and each line sees the ones above it. A nested field is
replaced by constructing an updated value, for example
`with App.database = {..App.database, host: "localhost"}`.

```nomi-run
import {
    std/io
}

interface Logger {
    fn log(logger: self, msg: String): Unit
}

type Stdout
impl Logger for Stdout {
    fn log(_logger: Stdout, msg: String): Unit {
        io.print("[log] ${msg}")
    }
}

struct PrefixedLogger { tag: String }
impl Logger for PrefixedLogger {
    fn log(logger: PrefixedLogger, msg: String): Unit {
        io.print("[${logger.tag}] ${msg}")
    }
}

struct App {
    logger: Logger
    context: Context
}

fn boot(): App {
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

The original value returns when the block exits, however it exits: at its
end, through `return`, or through `try`. A callee cannot override its caller's
environment. To limit an override to part of a function, move that part into a
helper, as `audit_greet` does here.

Captured ordinary values remain unchanged. A closure reads the fields in force
when it is called, so a closure returned from a function after a `with` line
does not carry the override:

```nomi-run
import std/io

struct App {
    greeting: String
}

fn boot(): App {
    App{greeting: "hello"}
}

fn greet(): String {
    "${App.greeting}, world"
}

fn quiet_greeter(): () -> String {
    with App.greeting = "psst"
    io.print(greet())
    || greet()
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

A spawned task is different: it takes a snapshot of the fields in force when it
is spawned.

## `defer` runs block-scoped cleanup

Deferred calls run in reverse registration order at the end of their block.

```nomi-run
import std/io

struct Conn {
    name: String
}

fn close(conn: Conn): Unit {
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

Acquisition failure is ordinary control flow: write
`db = try Sqlite.temp(); defer Sqlite.close(db)` when setup returns
`Result`. If the acquisition fails, the `try` returns before the `defer`
statement runs, and any earlier deferred calls in the same block still run
while the failure returns.

## Boot cleanup and initialization

Defers registered directly in entry-point boot's outer body live until main and
owned tasks finish. Test boot cleanup lives through the individual test. Helpers
and nested blocks retain ordinary block-scoped cleanup. A helper returning an
open resource leaves boot responsible for registering its cleanup.

Initialization failure skips main or the test's setup/body, shuts down started
work, and runs registered cleanup. The application fields are unavailable until
boot returns successfully, so boot works from its `startup` input, if it takes
one, and the helpers it calls while building the value.

## Context and concurrency

[`Context`](/reference/context/) carries a deadline and typed execution-scoped
values. Boot returns `Context.root()` or a tighter context derived from it.
Cancellation from the host or the test runner still reaches the program, and
the earliest inherited deadline remains effective.

An override such as
`with App.context = Context.with_timeout(App.context, duration)` bounds the
calls in the rest of its block. If the application calls the field `execution`, use
`App.execution` instead. The field name does not change deadline semantics.

A context deadline bounds blocking operations including timer sleeps, channel
operations, task waits, and supervisor flushes. See [Concurrency](/concurrency/)
for task ownership and cancellation, and [Testing](/testing/) for independent
per-test boot and clock selection.
