---
title: Testing
description: Write tests with assertions, check what a program prints, share setup, control time, and debug a failing test.
---

A test is ordinary Nomi code. A `test` declaration gives a name and a block to
run. You can put tests next to the code they check, or in a file whose name
ends in `_test.nomi`.

`nomi test` runs them. Anything a test prints, `dbg` included, appears under
its `ok` or `FAIL` line. `nomi test --watch` runs the tests again each time
you save a `.nomi` file under the path, or a file the tests import.

## Assertions

`assert` checks that something is true. `refute` checks that it is false. When
one fails, the report shows the line and the values involved.

```nomi-run
test "arithmetic has the expected shape" {
    assert 1 + 1 == 2
    refute 2 * 2 == 5
}
```

You can also assert a [`Maybe`](/reference/maybe/) or a
[`Result`](/reference/results/) directly. `assert` passes on `Some` or `Ok`.
`refute` passes on `None` or `Err`. To get the value inside, match it with a
pattern, as the second line of each test below does.

```nomi-run
fn parse_id(text: String) -> Result<Int, String> {
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

`assert pattern = value` checks that the value matches the pattern. Patterns
work the same as in `case` arms. The names the pattern binds are available on
the following lines.

```nomi-run
test "assert can destructure expected shapes" {
    assert [left, right] = [10, 32]

    assert left + right == 42
}
```

This is a good way to check for a particular enum variant, such as one kind of
error:

```nomi-run
import {
    std/calendar.{Error}
    std/calendar.Date
}

test "assert can match an error variant" {
    assert Err(Error.InvalidFormat(_)) = Date"not-a-date"
}
```

When a value that might fail is only setup for the rest of the test, use
`try`. If it fails, the test fails. Use a pattern assertion instead when the
result is the thing you are testing.

```nomi-run
import std/calendar.Date

test "formats parsed date" {
    d = try Date.parse("2026-05-04")

    assert Date.to_string(d) == "2026-05-04"
}
```

## Attached Tests

A short test can sit right above the function it checks. Write each line of
it after `//!`. `nomi test` runs each group of `//!` lines as one test.

```nomi-run
//! assert answer() == 42
//! assert (answer() + 1) == 43
//
fn answer() -> Int {
    42
}

//! assert label("ready") == "[ready]"
//
fn label(text: String) -> String {
    "[" + text + "]"
}
```

An attached test can bind names before it asserts:

```nomi-run
//! value = parse_int("42")
//! assert value == Some(42)
//
fn parse_int(text: String) -> Maybe<Int> {
    String.to_int(text)
}
```

It can also hold several assertions:

```nomi-run
//! value = parse_int_again("42")
//! assert value == Some(42)
//! refute value == None
//
fn parse_int_again(text: String) -> Maybe<Int> {
    String.to_int(text)
}
```

## Pipelines

`assert` and `refute` can check the result of a pipeline. Put the keyword
before the pipeline's first value:

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

## Subtests

`subtests` runs the same check over a list of cases. Each case is a row.

```nomi-run
fn palindrome?(text: String) -> Bool {
    String.reverse(text) == text
}

test "determines palindromes" {
    cases = [#("abba", True), #("abc", False), #("x", True)]

    subtests cases, #(line, known_palindrome) {
        assert palindrome?(line) == known_palindrome
    }
}
```

The body runs once for each row, in order. The pattern after the comma names
the parts of the row. The rows can be a list, a range, a map's entries or any
other `Iter`.

A failed assertion ends its row, and the next row still runs. After the last
row, the test fails if any row failed. The report shows each failed row, then
how many failed. If the second row were `#("abc", True)`, the test above would
report:

```text
FAIL palindrome_test.nomi :: determines palindromes
  subtest ("abc", True): line 10: assertion failed
    assert palindrome?(line) == known_palindrome
    values:
      line
        = "abc"
      palindrome?(line)
        = False
      known_palindrome
        = True
  1 of 3 subtests failed
```

The whole statement is still one test. A row cannot `return`, `break` or
`try`: every row runs to its end.

### Naming Rows

A third part after the pattern names each row. It is any `String`, and it can
use the names the pattern binds. A table often carries a description column
for this:

```nomi-run
fn leap_year?(year: Int) -> Bool {
    year % 400 == 0 or (year % 4 == 0 and year % 100 != 0)
}

test "leap years" {
    cases = [
        #("year divisible by 400 is leap year", 2000, True),
        #("year divisible by 100, not divisible by 400 in common year", 1900, False),
        #("year divisible by 4, not divisible by 100 in leap year", 1996, True),
    ]

    subtests cases, #(description, year, expected), description {
        assert leap_year?(year) == expected
    }
}
```

A failed row's report shows its name in place of the row. If `leap_year?`
returned `False` for 2000, the test would report:

```text
FAIL leap_test.nomi :: leap years
  subtest "year divisible by 400 is leap year": line 13: assertion failed
    assert leap_year?(year) == expected
    values:
      year
        = 2000
      leap_year?(year)
        = False
      expected
        = True
  1 of 3 subtests failed
```

You can also build a name from the row, as in
`subtests cases, #(a, b, sum), "${a} + ${b}" { ... }`. To leave a column out,
name it `_` as usual.

## Input And Output

[`io.capture`](/reference/io/) runs a function and gives it the text you pass
as its input. It returns what the function returned, in `value`, and
everything it printed, in `output`. Pass it `main` to check what a program
prints for some input.

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

Inside the call, `io.read_line` reads the text you passed, one line at a
time. When the text runs out, it returns `Err("eof")`. `io.print`, `io.write`
and `io.inspect` write to `output` instead of the terminal. This includes
anything printed by tasks the function starts. `dbg` still prints to the
terminal, so you can debug the function as usual.

Some programs ask a question, read the answer, then ask another.
[`io.replay`](/reference/io/) tests such a program against a script of the
whole session:

- Output lines are what the program prints. Write them at the column of the
  closing `"""`.
- Input lines are what the user types. Indent them and start them with `> `.

`io.replay` gives the input lines to `io.read_line` in order. `assert` then
checks that the program printed each output line, at the right point.

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

Some programs print a prompt with no newline and then read, as
`io.write("Hello, Ada. Your age: ")` does here. In the script, the prompt is
an output line of its own, and the typed text goes on the input line below
it.

Every line that is not an input line is output, exactly as written. Spaces
past the closing `"""` column are part of the output. A `> ` prompt at the
start of a line is output too: write `>`, then the input line `    > north`
under it. If an indented output line starts with `> `, write a backslash
before the `>`, as in `    \> quoted`, so it is not read as input.

Trailing spaces and the final newline are ignored. An input line the program
never reads fails the test.

When a replay fails, the report shows a diff, with each line's number in the
file. A `-` line is in the script but the program didn't print it. A `+` line
is what the program printed instead. Above the diff, the script is shortened
to `""" lines 11-23 """`. To check the session some other way, use
`io.capture`: its result holds the session in this same form, in
`transcript`.

A program that reads nothing gets a script of output lines only. The second
line below expects one leading space.

```nomi-run
import std/io

fn main() {
    io.print("-10.0 C |   14.0 F")
    io.print(" -5.0 C |   23.0 F")
}

test "converts to Fahrenheit" {
    assert io.replay(
        """
        -10.0 C |   14.0 F
         -5.0 C |   23.0 F
        """,
        main,
    )
}
```
<!-- expect
-10.0 C |   14.0 F
 -5.0 C |   23.0 F
-->

## Groups And Setup

`group` gathers related tests under one name. A group can have a `setup`. It
runs before each test in the group and produces a value. A test receives that
value through a pattern after its name.

```nomi-run
group "user labels" {
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

A test sees only the value `setup` returns. Names bound inside the `setup`
block don't reach the tests.

The value can be anything. A test can bind a record with `{name, role}`, a
tuple with `#(left, right)`, or the whole value with a name, as in
`test "loads rows", db { ... }`. The pattern has to match every value setup
can produce, so a pattern such as `Some(n)` is an error. Bind a name instead,
and match it in the test's body.

Groups don't nest. When two sets of tests need different setup, write two
groups side by side. Put the part they share in an ordinary function.

## Booting The Application

Some code reads [application fields](/capabilities-and-context/), such as
`Config.env`. Those fields come from the program's `boot` function. To test
that code, the group calls `boot` on a `boot` line, passing a `Startup` if
`boot` takes one. Code that reads no application field needs no `boot` line.

Each test starts fresh. First the group's clock is chosen, then `boot` runs,
then `setup`, then the test's body.

```nomi-run
import std/io

struct Config {
    env: String
}

fn boot(startup: Startup) -> Config {
    Config{env: Map.get(startup.env, "APP_ENV") |> Maybe.with_default("dev")}
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
-->

The tests here are in the same file as `main`, so the `boot` line calls
`boot` by its bare name. `Startup`'s fields are empty unless you set them, so
the group sets only `env`. The `with` line in the second test is explained in
[App Fields, Defer & Context](/capabilities-and-context/#with-replaces-a-field-for-the-rest-of-the-block).

Most tests need no startup input. If `boot` reads none, it takes no
parameter, and groups write `boot boot()`. If `boot` does read startup input,
give its parameter a default. Then a group that needs no input writes
`boot boot()`, and the others pass the inputs they want:

```nomi-run
import std/io

struct Config {
    verbose: Bool
}

fn boot(startup: Startup = Startup{}) -> Config {
    Config{verbose: Iter.any?(startup.args, fn arg -> arg == "-v")}
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
`server.nomi` declares `pub fn boot(startup: Startup = Startup{}) -> App`, a group
that needs no input writes `boot server.boot()`.

## Virtual Time

A test that waits is slow, and on a busy machine it may fail by accident.
Add `clock Clock.Virtual` to a group to avoid both. Its tests run under a fake
clock. Whenever every task in the test is waiting, the clock jumps forward,
so waiting takes no real time.

```nomi
import {
    std/duration.Duration
    std/testing.Clock
    std/timer
}

group "retry policy" {
    clock Clock.Virtual

    test "gives up after backing off" {
        timer.sleep(Duration.minutes(5))
        assert True
    }
}
```

That test finishes at once. It also takes the same path every time, because
the clock only moves when the program is waiting.

`clock` takes an ordinary expression naming a
[`testing.Clock`](/reference/testing/) variant, so `Clock` has to be imported,
as above. If you import `std/testing` instead, write
`clock testing.Clock.Virtual`.

A group's `clock`, `boot` and `setup` lines can be in any order. They always
run clock first, then boot, then setup. `nomi fmt` moves them to the top of
the group in that order.

Tests outside a group with a `clock` line use the real clock.

Under the fake clock, a test that can never finish is reported as a deadlock.
It doesn't hang the run. So you can safely test calls that wait with no
timeout, such as [`Supervisor.flush`](/concurrency/#testing-background-work).

The fake clock is off by default because it changes how a test behaves:

- Waiting on something outside the program, such as reading input or a real
  network call, never lets the clock move. The test is reported as deadlocked.
- Background work still running when a test ends is an error. It does not
  carry on into the next test.
- `Instant.now` starts at midnight UTC on 1 January 2000.

The first two usually point to a problem in the test. Still, they change
behaviour, so each group opts in.

## Debugging A Test

In VS Code and Neovim (with nvim-dap), click Debug test beside a test. In
Zed, pick the test's debug entry. Helix starts one with `:debug-start`. Each
runs the test under `nomi dap`, Nomi's debugger. The
[editor support](https://github.com/nomi-language/nomi#editor-support)
section of the README has the setup.

Breakpoints, stepping and watches work as in any debugger. A few things are
particular to Nomi:

- A failed `assert`, or a runtime error such as dividing by zero, stops the
  test where it happened. Every binding is still there to read.
- Watches and the debug console run Nomi code in the paused frame. Values
  can't change, so looking at them can't disturb the test. An expression that
  would do something outside the program, such as printing or reading the
  clock, is refused unless it ends with `// allow effects`.
- The Pipeline scope shows the value each `|>` stage produced.
- In a frame inside `io.replay` or `io.capture`, you can see the output
  captured so far.
- Step back and reverse continue run the test again from the start. It gets
  the same input, clock readings and random numbers as the first time, and
  stops at the earlier point. You can't go back in a test that starts tasks.
