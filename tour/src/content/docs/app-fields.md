---
title: "App Fields, Defer & Context"
description: "Give a whole program settings and services through app fields, replace one for part of the program with `with`, clean up with `defer`, and set deadlines with a context."
---

A program can define a function named `boot`. It builds a struct that holds
the things the program needs everywhere, such as settings or a logger, and
returns it in `Ok`. Any code can then read one of the struct's fields through
the struct's type name, as in `App.port`. These are called app fields.

The struct may have one field of type [`Context`](/reference/context/), with
any name you like. A context carries a deadline, explained
[below](#deadlines).

## The basic shape

```nomi-run
import std::io

struct App {
    port: Int
    context: Context
}

fn boot() -> Result<App, String> {
    Ok(App{port: 8080, context: Context.root()})
}

fn main() {
    io.print("listening on :${App.port}")
}
```
<!-- expect
listening on :8080
-->

`boot` always returns a `Result`: `Ok` with the struct, or `Err` when the
program cannot start, for example because a setting is missing or a database
does not open. The error type is yours to choose; `String` is the plainest.
A `boot` that cannot fail still returns `Result`, so adding a step that can
fail later does not change its signature, and `?` works in its body. A
`boot` that returns the struct without a `Result` is a compile error.

`boot` goes in the same file as `main`. This `boot` takes no parameter. To
read the environment or the command-line arguments, declare
`fn boot(startup: Startup)` instead. `startup.env` is a
`Map<String, String>` of environment variables, read with
`Map.get(startup.env, "APP_ENV")`. `startup.args` is a `List<String>` of the
arguments. `Startup` and `Context` are available everywhere, so you don't
import them.

App fields can't be read until `boot` has returned. So `boot` builds the
struct from its `startup` parameter, if it has one, and from the functions it
calls. If a helper builds part of the struct, pass it what it needs from
`startup`.

`boot` makes the context with `Context.root()`, or with a narrower context
made from that one, such as one with a deadline.

The struct's fields are the app fields, with the same types. Here,
`port: Int` makes `App.port` an `Int`. You can name the struct `App`,
`Config`, or anything else. Another file that reads its fields imports it
like any other type. The context field can have any name too:

```nomi
import std::duration.Duration

struct Server {
    port: Int
    execution: Context
}

fn boot() -> Result<Server, String> {
    Ok(Server{port: 8080, execution: Context.root()})
}

fn bounded_work() {
    with Server.execution = Context.with_timeout(Server.execution, Duration.seconds(3))
    serve(Server.port)
}
```

Nomi finds the context field by its type, not its name.

`main` runs with the struct its file's `boot` returns in `Ok`. A test runs with
the one its group's `boot` line returns (see
[Testing code that reads app fields](#testing-code-that-reads-app-fields)).
Code that reads `Server.port` must run under a `boot` that returns a
`Server`. Code that reads no app field runs under any `boot`, or none.

## `with` replaces a field for the rest of the block

`with App.field = value` replaces one app field from that line to the end of
the block it is in. Every function called in that part of the block sees the
new value.

The new value must have the field's type. If the field is an enum, you can
write just `.Variant`. To replace several fields, write several `with` lines.
Each line sees the values set by the lines above it.

`with` replaces whole fields of the app struct. To change one part of a
field, build an updated copy of it:
`with App.database = #{..App.database, host: "localhost"}`.

```nomi-run
import {
    std::log.{self, Logger}
    std::log_testing
}

struct App {
    logger: Logger
}

fn boot() -> Result<App, String> {
    Ok(App{logger: Logger.text(min: .Warn)})
}

fn main() {
    greet("World")
}

fn audit_greet(name: String) {
    with App.logger = Logger.with_fields(App.logger, [log.bool("audit", True)])
    greet(name)
}

fn greet(name: String) {
    log.info(App.logger, "hello", [log.string("name", name)])
}

group "greetings" {
    boot boot()

    test "only the audited greeting carries the audit field" {
        got = log_testing.run(fn logger -> {
            with App.logger = logger
            greet("World")
            audit_greet("Alice")
            greet("Bob")
        })

        fields = Iter.map(got.records, .fields) |> Iter.to_list()

        assert fields == [
            [log.string("name", "World")],
            [log.bool("audit", True), log.string("name", "Alice")],
            [log.string("name", "Bob")],
        ]
    }
}
```
<!-- expect
ok greetings / only the audited greeting carries the audit field
test result: ok. 1 passed, 0 failed
-->

When the block ends, the field gets its old value back. This happens however
the block ends: at its last line, through `return`, or through a `?`. So a
function you call can't change the fields you see.

To replace a field for only part of a function, move that part into a
function of its own, as `audit_greet` does here.

The app's [`std::log`](/reference/log/) `Logger` writes warnings and errors.
The test replaces it with a `log_testing` recorder, which keeps every record
as a value, so it can check that only Alice's greeting carries the `audit`
field.

A closure reads app fields when it is called, not when it is made. So a
closure returned from a function after a `with` line doesn't keep the
replacement:

```nomi-run
import std::io

struct App {
    greeting: String
}

fn boot() -> Result<App, String> {
    Ok(App{greeting: "hello"})
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
spawned ([Concurrency](/concurrency/)).

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- One field per line. A comma after the value is an error, and so is a `{`
  after it: `with` takes no block.
- The target must be an app field. `with Point.x = 1` is an error.
- `with` works in any block: an `if` or `case` arm, a lambda body, a test
  body, a group's `setup`. To limit it to part of a function, put it in a
  block statement or a helper.
- An app struct can't declare an inherent function or `once` with a
  field's name, since `App.name` always reads the field.

Spec: [`with`](/spec/program-execution/#with),
[Reading a field](/spec/program-execution/#reading-a-field).

</div>

## `defer` runs block-scoped cleanup

`defer` runs a call when the block it is in ends. Several deferred calls run
in reverse order, last one first:

```nomi-run
import std::io

fn main() {
    {
        io.print("open notes")
        defer io.print("close notes")
        io.print("open log")
        defer io.print("close log")
        io.print("body")
    }

    io.print("after the block")
}
```
<!-- expect
open notes
open log
body
close log
close notes
after the block
-->

`close log` runs before `close notes`, the reverse of the order they were
deferred in. Both run when the inner block ends, before the line after it.

Opening something can fail. When it returns a `Result`, open it with `?`
and then defer the cleanup:

```nomi
notes = fs.open("notes.txt")?

defer Reader.close_quietly(notes)
```

If opening fails, the `?` returns before the `defer` line runs, so there is
nothing to close. Calls deferred earlier in the block still run.
[Files](/files/#streams) has more on closing files.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- The function and its arguments are evaluated on the `defer` line, with
  the app fields in force there. Only the call waits.
- The deferred call must return `Unit`, so it can't report a failure. A
  cleanup that can fail handles it inside, as `Reader.close_quietly` does.
- Every way out of the block runs it: the end, `return`, `break`,
  `continue`, a `?` and a trap. A deferred call that traps doesn't stop the
  others or hide the failure that ended the block.
- A `defer` in a lambda runs when that call of the lambda ends, so one in
  an `Iter` callback runs once per round.
- A pending `defer` turns a tail call into an ordinary call
  ([Recursion and tail calls](/iteration-and-loops/#recursion-and-tail-calls)).

Spec: [`defer`](/spec/statements-and-control-flow/#defer),
[Cleanup and boot failure](/spec/program-execution/#cleanup-and-boot-failure).

</div>

## Cleanup and failure in `boot`

A `defer` written directly in the body of the program's `boot` runs after
`main` and the tasks it owns have finished. In a test, a `defer` in `boot`
runs when that test finishes. A `defer` inside a function `boot` calls, or
inside a nested block, runs when that function or block ends, as usual. So
if a helper opens something and returns it, `boot` must defer its cleanup.

`boot` fails by returning `Err`. Then `main` doesn't run. Any work already
started is shut down, the cleanup `boot` deferred runs, and the program
prints `error: ` and the error on stderr and exits with status 1, as when
`main` returns `Err`. The error prints through its `Display` impl when
it has one; a `String` prints as itself. A `boot` whose error is an
[`Exit`](/input-and-output/#exit-codes) prints its
message and exits with its code, as `main` would. A trap in `boot` stops the
program the same way a trap anywhere does.

```nomi-run error
import std::io

struct App {
    database: String
}

fn setting(startup: Startup, key: String) -> Result<String, String> {
    case Map.get(startup.env, key) {
        Some(value) -> Ok(value)
        None -> Err("${key} is not set")
    }
}

fn boot(startup: Startup) -> Result<App, String> {
    io.print("opening the log")
    defer io.print("closing the log")
    database = setting(startup, "TOUR_DATABASE_PATH")?
    Ok(App{database})
}

fn main() {
    io.print("using ${App.database}")
}
```
<!-- expect
opening the log
closing the log
error: TOUR_DATABASE_PATH is not set
-->

In a test, a `boot` that returns `Err` fails the test before its setup and
body run, and the cleanup `boot` deferred runs when the test ends (see
[A boot that fails](#a-boot-that-fails)).

## Settings per environment

Settings are ordinary code: field defaults for development, a function that
adjusts them for each environment, and `boot` reading the environment
variables that override them. `nomi test` sets `NOMI_ENV` to `test` unless it
is already set, so a program can pick its test settings by it:

```nomi-run
import std::io

struct Config {
    deployment: String = "dev"
    database_url: String = "postgres://localhost/myapp_dev"
    max_connections: Int = 5
}

fn configure(deployment: String) -> Config {
    base = Config{deployment}

    case deployment {
        "prod" ->
            Struct.update(base, #{max_connections: 20})

        "test" ->
            Struct.update(
                base,
                #{database_url: "postgres://localhost/myapp_test", max_connections: 2},
            )

        _ ->
            base
    }
}

fn boot(startup: Startup) -> Result<Config, String> {
    deployment = Map.get(startup.env, "NOMI_ENV") |> Maybe.with_default("dev")
    configured = configure(deployment)

    database_url =
        Map.get(startup.env, "DATABASE_URL") |> Maybe.with_default(configured.database_url)

    Ok(Struct.update(configured, #{database_url}))
}

fn main() {
    io.print("${Config.deployment}: ${Config.database_url}, ${Config.max_connections} connections")
}
```
<!-- expect
dev: postgres://localhost/myapp_dev, 5 connections
-->

Every variable the program reads is a `Map.get` in `boot`, so there is no
hidden mapping from the environment to the struct.

## Deadlines

A [`Context`](/reference/context/) carries a deadline. To set one for the
rest of a block, replace the context field:
`with App.context = Context.with_timeout(App.context, duration)`.
[Concurrency](/concurrency/#a-tasks-own-context) shows what a deadline stops
and how each task gets its own context.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A deadline can only get shorter. A child context with a later deadline
  than its parent keeps the parent's.
- A program with no `Context` field runs under a root context with no
  deadline. An app struct has at most one `Context` field, under any name.
- `boot` gets no context. It builds one with `Context.root()` or a
  deriver such as `Context.with_timeout(Context.root(), d)`.
- A context also carries values keyed by their type, for metadata such as a
  trace ID: `Context.with_value` adds one and `Context.value` reads it.

Spec: [Context](/spec/program-execution/#context),
[Context values](/spec/program-execution/#context-values).

</div>

## Testing code that reads app fields

Code that reads app fields, such as `Config.env`, needs a `boot` to run. In
a test, the group calls `boot` on a `boot` line, passing a `Startup` if
`boot` takes one. Code that reads no app field needs no `boot` line.

Each test starts fresh: `boot` runs, then the group's `setup`, then the
test's body.

```nomi-run
import std::io

struct Config {
    env: String
}

fn boot(startup: Startup) -> Result<Config, String> {
    Ok(Config{env: Map.get(startup.env, "APP_ENV") |> Maybe.with_default("dev")})
}

fn main() {
    io.print("env: ${Config.env}")
}

group "app-backed behavior" {
    boot boot(Startup{env: %{"APP_ENV" => "test"}})

    test "reads the booted field" {
        assert Config.env == "test"
    }

    test "a with line replaces a field for the rest of the body" {
        with Config.env = "staging"
        assert Config.env == "staging"
    }
}
```
<!-- expect
env: dev
ok app-backed behavior / reads the booted field
ok app-backed behavior / a with line replaces a field for the rest of the body
test result: ok. 2 passed, 0 failed
-->

The tests here are in the same file as `main`, so the `boot` line calls
`boot` by its bare name. `Startup`'s fields are empty unless you set them, so
the group sets only `env`.

Most tests need no startup input. If `boot` reads none, it takes no
parameter, and groups write `boot boot()`. If `boot` does read startup input,
give its parameter a default. Then a group that needs no input writes
`boot boot()`, and the others pass the inputs they want:

```nomi-run
import std::io

struct Config {
    verbose: Bool
}

fn boot(startup: Startup = Startup{}) -> Result<Config, String> {
    Ok(Config{verbose: "-v" in startup.args})
}

fn main() {
    io.print("verbose: ${Config.verbose}")
}

group "quiet by default" {
    boot boot()

    test "takes no arguments" {
        assert Config.verbose == False
    }
}

group "verbose" {
    boot boot(Startup{args: ["-v"]})

    test "reads -v" {
        assert Config.verbose
    }
}
```
<!-- expect
verbose: False
ok quiet by default / takes no arguments
ok verbose / reads -v
test result: ok. 2 passed, 0 failed
-->

When the program itself runs, `boot` still gets the real arguments. The
default is used only by a `boot` line that passes nothing.

In a larger project, tests live in their own file. They import the file that
holds `main` and call its boot, as `server.boot` below.

A `with` line in `setup` stays in force for the whole test, so `setup` is a
good place to swap in a fake. A `defer` in `boot` or `setup` runs when the
test finishes:

```nomi
// server_test.nomi
import {
    server
    server.App
    test_support
    test_support.FakeMailer
}

group "welcome email" {
    boot server.boot(test_support.startup())

    setup {
        with App.mailer = FakeMailer
        "ada@example.com"
    }

    test "sends through the fake", to {
        assert server.welcome(to)
    }
}
```

Here `test_support.startup()` builds the inputs this group needs. If
`server.nomi` declares
`pub fn boot(startup: Startup = Startup{}) -> Result<App, String>`, a group
that needs no input writes `boot server.boot()`.

### A boot that fails

A `boot` returns a `Result`, and a group's `boot` line takes it. When `boot`
returns `Err`, each test in the group fails before its setup and body run.
The report is the one a `?` gives when it ends a test early: the `boot`
line, and the `Err` it returned. The cleanup `boot` deferred still runs when
the test ends.

```nomi
group "without a database path" {
    boot server.boot(Startup{})

    test "never gets this far" {
        assert server.is_ready()
    }
}
```

If `server.boot` returns `Err("DATABASE_PATH is not set")` for an empty
`Startup`, `nomi test` reports:

```text
FAIL server_test.nomi :: without a database path / never gets this far
  line 2: test returned early
    boot server.boot(Startup{})
    returned:
      Err("DATABASE_PATH is not set")
```

A test that checks how `boot` fails calls the code `boot` calls, such as the
function that reads the settings, and asserts on its `Err`, since a group's
`boot` line runs only to start a test.

[Input & Output](/input-and-output/) is next. It prints, reads input and
sets the exit status.
