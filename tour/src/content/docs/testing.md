---
title: Testing
description: Write Nomi tests with test declarations, assertions, checks, setup, and ordinary language features.
---

Nomi tests are just Nomi code. A `test` declaration names a behavior and runs a
block. Put tests next to the code they exercise or in a conventional
`*_test.nomi` file, then run them with `nomi test`.

## Assertions

Use `assert` for the shape you expect and `refute` for the shape you expect not
to hold. They work with ordinary booleans, and failed assertions produce
source-located reports.

```nomi-run
test "arithmetic has the expected shape" {
    assert 1 + 1 == 2
    refute 2 * 2 == 5
}
```

[`Maybe`](/reference/maybe/) and [`Result`](/reference/results/) values can be asserted directly. `assert` checks success
shapes; `refute` checks failure shapes. These checks return the original value,
so payload extraction stays explicit.

```nomi-run
fn parse_id(text: String): Result<Int, String> {
    case String.to_int(text) {
        Some(id) -> Ok(id)
        None -> Err("expected a numeric id")
    }
}

test "assert checks successful results" {
    assert parse_id("42")
    assert Ok(id) = parse_id("42")

    assert id == 42
}

test "refute checks result errors" {
    refute parse_id("wat")
    assert Err(message) = parse_id("wat")

    assert message == "expected a numeric id"
}
```

You can also assert a pattern. Pattern assertions use the same pattern language
as `case` arms, which is useful when the expected shape matters and the bound
names are the values you want to inspect next.

```nomi-run
test "assert can destructure expected shapes" {
    assert [left, right] = [10, 32]

    assert left + right == 42
}
```

That same pattern form works well with enum variants because the whole expected
shape is visible at the assertion site.

```nomi-run
import {
    std/calendar.{Error}
    std/calendar.Date
}

test "assert can match an error variant" {
    assert Err(Error.InvalidFormat(_)) = Date"not-a-date"
}
```

Use `try` when a fallible value is setup for the rest of the test. Use a
pattern assertion when the returned shape is part of what the test is checking.

```nomi-run
import std/calendar.Date

test "formats parsed date" {
    d = try Date.parse("2026-05-04")

    assert Date.to_string(d) == "2026-05-04"
}
```

## Attached Tests

Use `//!` directly above a declaration when the test is a compact example of
that declaration's behavior. Each contiguous prompt group is a regular runnable
test under `nomi test`.

```nomi-run
//! assert answer() == 42
//! assert (answer() + 1) == 43
//
fn answer(): Int {
    42
}

//! assert label("ready") == "[ready]"
//
fn label(text: String): String {
    "[" + text + "]"
}
```

When setup is useful, put it in the attached test before the assertion.

```nomi-run
//! value = parse_int("42")
//! assert value == Some(42)
//
fn parse_int(text: String): Maybe<Int> {
    String.to_int(text)
}
```

One attached test may contain shared setup and multiple assertions.

```nomi-run
//! value = parse_int_again("42")
//! assert value == Some(42)
//! refute value == None
//
fn parse_int_again(text: String): Maybe<Int> {
    String.to_int(text)
}
```

## Pipelines

`assert` and `refute` can wrap pipelines. Put the keyword at the head so the
whole pipeline reads as the assertion subject.

```nomi-run
test "pipeline assertions validate transformed values" {
    assert "  ADA LOVELACE  "
        |> String.trim()
        |> String.to_lower()
        |> String.equal?("ada lovelace")

    refute "  GRACE HOPPER  "
        |> String.trim()
        |> String.to_lower()
        |> String.contains?("ada")
}
```

## Input And Output

[`io.capture`](/reference/io/) runs a function with the text you give it as
standard input and hands back what the function returned (`value`) and
everything it printed (`output`). Pass a program's `main` to check what the
program prints for a given input.

```nomi-run
import std/io

fn main() {
    case io.read_line() {
        Ok(name) -> io.print("hello, ${name}")
        Err(_) -> io.print("hello, stranger")
    }
}

test "greets the name it reads" {
    run = io.capture("Ada\n", main)

    assert run.output == "hello, Ada\n"
}

test "greets a stranger when there is no input" {
    assert io.capture("", main).output == "hello, stranger\n"
}
```
<!-- expect
hello, stranger
-->

Inside the call, `io.read_line` reads the given text a line at a time and
answers `Err("eof")` at its end, and `io.print`, `io.write` and `io.inspect`
write to `output` instead of the terminal. Tasks the function starts are
captured too. `dbg` still prints to the terminal, so you can debug a captured
function as usual.

A program that asks and answers in turn is easier to test as one
conversation. [`io.replay`](/reference/io/) takes a script of the session as
a terminal would show it, in two columns: lines starting with `> ` are what
the user types, and lines starting with two spaces are what the program
prints. It feeds the `>` lines to `io.read_line` in order, and `assert`
checks that the program printed the rest at the right points.

```nomi-run
import std/io

fn main() {
    io.print("What is your name?")
    case io.read_line() {
        Ok(name) -> {
            io.write("Hello, ${name}. Your age: ")
            case io.read_line() {
                Ok(age) -> io.print("${name} is ${age}")
                Err(_) -> io.print("no age")
            }
        }
        Err(_) -> io.print("hello, stranger")
    }
}

test "asks for a name, then an age" {
    assert io.replay(
        """
          What is your name?
        > Ada
          Hello, Ada. Your age:
        > 36
          Ada is 36
        """,
        main,
    )
}
```
<!-- expect
What is your name?
hello, stranger
-->

The `>` lines sit two columns left of the output, so the `"""` string keeps
the two spaces in front of each output line. When the program writes a
prompt without a newline before it reads, as `io.write("Hello, Ada. Your
age: ")` does, the prompt is an output line of its own and the typed text
goes on the `>` line below it. Output that starts with `>`, such as a `> `
prompt, is just output: `  >` above `> north`. Every line needs one of the
two marks, even in a script with no input, so for a program that reads
nothing compare `io.capture(input, main).output` instead. Trailing spaces
and the final newline don't count. A failed replay prints a line diff, `-`
for script lines the program did not produce and `+` for what it did
instead, and a `>` line the program never read fails it too. Each
`Captured` has the same view in `transcript`, if you want to check it
yourself.

## Groups And Setup

Use `tests` to group related cases. A group can define `setup`, which runs
before each of its tests and produces a value. A test receives that value
through a pattern after its name.

```nomi-run
tests "user labels" {
    setup {name: "Ada", role: "admin"}

    test "setup value is explicit", {name, role} {
        assert name == "Ada"
        assert role == "admin"
    }

    test "a test may ignore the setup value" {
        assert True
    }
}
```

Setup is for shared test values, not hidden globals. Bindings inside a setup
block do not reach the tests unless the block returns them. The value can be
anything: a record, a tuple bound with `(left, right)`, or a single value bound
with a name, as in `test "loads rows", db { ... }`. The pattern has to match
every value setup can produce, so a pattern such as `Some(n)` is an error; bind
a name and match it in the body.

Groups do not nest. When two sets of tests need different setup, write two
sibling groups and share the common part through an ordinary function.

## Booting The Application

When code under test reads [application fields](/capabilities-and-context/)
such as `Config.env`, the group calls the entry's boot on a `boot` line, with
the `Startup` it takes, if any. Each test runs fresh: the group's clock is
selected, the boot call runs, then `setup`, then the test's body. Code under
test that reads no application field needs no boot.

```nomi-run
import std/io

struct Config {
    env: String
}

fn boot(startup: Startup): Config {
    Config{env: Map.get(startup.env, "APP_ENV") |> Maybe.with_default("dev")}
}

fn main() {
    io.print("env: ${Config.env}")
}

tests "app-backed behavior" {
    boot boot(Startup{env: {"APP_ENV" => "test"}})

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
-->

This example keeps the tests beside `main` in one file, so the `boot` line
calls `boot` by its bare name. `Startup`'s fields default to empty, so the
test sets only `env`.

Most tests need no startup input. A boot that reads none declares no
parameter, and its groups write `boot boot()`. A boot that does read startup
can give its parameter a default, so the groups that need no input write
`boot boot()` and the others pass the inputs they set:

```nomi-run
import std/io

struct Config {
    verbose: Bool
}

fn boot(startup: Startup = Startup{}): Config {
    Config{verbose: Iter.any?(startup.args, |arg| arg == "-v")}
}

fn main() {
    io.print("verbose: ${Config.verbose}")
}

tests "quiet by default" {
    boot boot()

    test "takes no arguments" {
        assert Config.verbose == False
    }
}

tests "verbose" {
    boot boot(Startup{args: ["-v"]})

    test "reads -v" {
        assert Config.verbose
    }
}
```
<!-- expect
verbose: False
-->

`main` still receives the real program arguments; the default applies only to
a `boot` line that passes none.

In a project, tests live in their own file and reach the boot by importing the
entry file, as `server.boot` below. A `with` line in `setup` stays in force for
each test's body, which makes `setup` the place to install fakes. `defer` in
boot or setup runs when that test finishes:

```nomi
// server_test.nomi
import {
    server
    server.App
    test_support
    test_support.FakeMailer
}

tests "welcome email" {
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
`server.nomi` declares `pub fn boot(startup: Startup = Startup{}): App`, a group
that needs none writes `boot server.boot()`.

## Virtual Time

Tests that involve waiting are slow when they pass and unreliable when the
machine is busy. Declare `clock Clock.Virtual` on a group and its tests run under a
clock that jumps forward whenever every task in the test is blocked, so waiting
costs nothing.

```nomi
import std/testing.Clock

tests "retry policy" {
    clock Clock.Virtual

    test "gives up after backing off" {
        timer.sleep(Duration.minutes(5))
        assert True
    }
}
```

That test finishes immediately, and it finishes the same way every time — the
clock moves in response to the program rather than alongside it.

`clock` takes an ordinary expression naming a
[`testing.Clock`](/reference/testing/) variant, the same way `boot` and `setup`
take expressions — so the type has to be imported, exactly like any other
reference. Import the file API object instead and it reads
`clock testing.Clock.Virtual`. A group's `clock`, `boot` and `setup` lines may
be written in any order, since they always run clock first, then boot, then
setup; `nomi fmt` moves them to the top of the group in that order.

Tests outside any group that declares a `clock` use the real one, so nothing
changes for suites that never opt in.

A test that can never finish is reported as a deadlock instead of hanging the
run, which is what makes blocking calls without timeouts — like
[`Supervisor.flush`](/concurrency/#testing-background-work) — safe to write.

It is off by default because the virtual clock changes behaviour:

- Waiting on something outside the program, such as reading input or a real
  network call, never lets the clock move. The test is reported as deadlocked.
- Background work still running when a test ends is an error, not something
  that carries on into the next test.
- `Instant.now` starts at midnight on 1 January 2000.

The first two usually indicate a test worth fixing, but they are behaviour
changes, so you opt in per run.
