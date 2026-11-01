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
<!-- expect
ok arithmetic has the expected shape
test result: ok. 1 passed, 0 failed
-->

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
<!-- expect
ok assert checks successful results
ok refute checks result errors
test result: ok. 2 passed, 0 failed
-->

`assert pattern = value` checks that the value matches the pattern. Patterns
work the same as in `case` arms. The names the pattern binds are available on
the following lines.

```nomi-run
test "assert can destructure expected shapes" {
    assert [left, right] = [10, 32]

    assert left + right == 42
}
```
<!-- expect
ok assert can destructure expected shapes
test result: ok. 1 passed, 0 failed
-->

This is a good way to check for a particular enum variant, such as one kind of
error:

```nomi-run
import std/calendar.{Date, Error}

test "assert can match an error variant" {
    assert Err(Error.InvalidFormat(_)) = Date.parse("not-a-date")
}
```
<!-- expect
ok assert can match an error variant
test result: ok. 1 passed, 0 failed
-->

When a `Result` or `Maybe` is only setup for the rest of the test, put `?`
after the call. If it is `Err` or `None`, the test fails there. Use a pattern
assertion instead when the result is the thing you are testing.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A test must hold at least one `assert`, `refute` or `subtests`.
- A failure report lists each operand under `values:`. A literal has no
  row, and values read structurally: strings are quoted and a type's
  `Display` is not used.
- When `assert a == b` fails on two strings and one has a line break, the
  report shows a line diff instead. The left side is the actual value and
  the right side the expected one.
- An assertion is a statement or a binding's value, never an operand:
  `not assert x` is an error that suggests `refute x`. In a lambda it is an
  error too.

Spec: [Assertions](/spec/tests/#assertions),
[Failure reports](/spec/tests/#failure-reports).

</div>

```nomi-run
import std/calendar.Date

test "formats parsed date" {
    d = Date.parse("2026-05-04")?

    assert Date.to_string(d) == "2026-05-04"
}
```
<!-- expect
ok formats parsed date
test result: ok. 1 passed, 0 failed
-->

## Attached tests

A short test can sit right above the function it checks. Write each line of
it after `//!`. `nomi test` runs each group of `//!` lines as one test. A
bare `//` line ends the test. `nomi test` doesn't need it, but it keeps the
test apart from the function's doc comment.

```nomi-run
//! assert answer() == 42
//! assert answer() + 1 == 43
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
<!-- expect
ok answer //! test lines 1-2
ok label //! test line 8
test result: ok. 2 passed, 0 failed
-->

An attached test can bind names before it asserts, and hold several
assertions:

```nomi-run
//! value = parse_int("42")
//! assert value == Some(42)
//! assert parse_int("forty") == None
//
fn parse_int(text: String) -> Maybe<Int> {
    String.to_int(text)
}
```
<!-- expect
ok parse_int //! test lines 1-3
test result: ok. 1 passed, 0 failed
-->

## Pipelines

`assert` and `refute` can check the result of a pipeline. Put the keyword
before the pipeline's first value:

```nomi-run
test "pipeline assertions validate transformed values" {
    assert "  ADA LOVELACE  "
        |> String.trim()
        |> String.lowercase()
        |> String.equal("ada lovelace")

    refute "  GRACE HOPPER  "
        |> String.trim()
        |> String.lowercase()
        |> String.contains("ada")
}
```
<!-- expect
ok pipeline assertions validate transformed values
test result: ok. 1 passed, 0 failed
-->

## Subtests

`subtests` runs the same check over a list of cases. Each case is a row.

```nomi-run
fn is_palindrome(text: String) -> Bool {
    String.reverse(text) == text
}

test "determines palindromes" {
    [#("abba", True), #("abc", False), #("x", True)]
    |> subtests #(line, known_palindrome) {
        assert is_palindrome(line) == known_palindrome
    }
}
```
<!-- expect
ok determines palindromes (3 cases)
test result: ok. 3 passed, 0 failed
-->

The body runs once for each row, in order. The pattern after `subtests` names
the parts of the row. The rows can be a list, a range, a map's entries or any
other `Iter`. The `|> subtests` stage ends its pipeline and stands as a
statement; `subtests cases, #(line, known_palindrome) { ... }` is the same
check with rows you bound to a name.

A failed assertion ends its row, and the next row still runs. After the last
row, the test fails if any row failed. The report shows each failed row, then
how many failed. If the second row were `#("abc", True)`, the test above would
report:

```console
FAIL palindrome_test.nomi :: determines palindromes
  subtest #("abc", True): line 8: assertion failed
    assert is_palindrome(line) == known_palindrome
    values:
      line
        = "abc"
      is_palindrome(line)
        = False
      known_palindrome
        = True
  1 of 3 subtests failed
```

Each row counts as a case in the totals, and passing rows are not listed:
`ok palindrome_test.nomi :: determines palindromes (3 cases)`. A row can't use
`return`, `break` or `?`, so every row runs to its end. To get a value out of
a `Result` in a row, use `assert Ok(value) = ...`.

### Naming rows

A third part after the pattern names each row. It is any `String`, and it can
use the names the pattern binds. A table often carries a description column
for this:

```nomi-run
fn is_leap_year(year: Int) -> Bool {
    year % 400 == 0 or (year % 4 == 0 and year % 100 != 0)
}

test "leap years" {
    [
        #("year divisible by 400 is leap year", 2000, True),
        #("year divisible by 100, not divisible by 400 in common year", 1900, False),
        #("year divisible by 4, not divisible by 100 in leap year", 1996, True),
    ]
    |> subtests #(description, year, expected), description {
        assert is_leap_year(year) == expected
    }
}
```
<!-- expect
ok leap years (3 cases)
test result: ok. 3 passed, 0 failed
-->

A failed row's report shows its name in place of the row. If `is_leap_year`
returned `False` for 2000, the test would report:

```console
FAIL leap_test.nomi :: leap years
  subtest "year divisible by 400 is leap year": line 12: assertion failed
    assert is_leap_year(year) == expected
    values:
      year
        = 2000
      is_leap_year(year)
        = False
      expected
        = True
  1 of 3 subtests failed
```

You can also build a name from the row, as in
`|> subtests #(a, b, sum), "${a} + ${b}" { ... }`. To leave a column out,
name it `_` as usual.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A column that only the row's name reads counts as used.
- A trap in a row is not caught. It fails the test, and the rows after it
  don't run. A name that traps fails its row only.
- `|> subtests` must be the last stage of a pipeline that stands as its
  own statement, since it has no value.
- `subtests` can't stand in a `setup` or inside another `subtests` body.

Spec: [Subtests](/spec/tests/#subtests).

</div>

## Input and output

[`io.capture`](/reference/io/) runs a function and gives it the text you pass
as its input. It returns what the function returned, in `value`, and
everything it printed, in `output`. Pass it `main` to check what a program
prints for some input. For code that reads or writes files, `fs_testing.run`
does the same with a file system in memory, as
[Files](/files/#testing-code-that-uses-files) shows.

```nomi-run
import std/io

fn main() {
    case io.read_line() {
        Ok(Some(name)) -> io.print("hello, ${name}")
        _ -> io.print("hello, stranger")
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
ok greets the name it reads
ok greets a stranger when there is no input
test result: ok. 2 passed, 0 failed
-->

Inside the call, `io.read_line` reads the text you passed, one line at a
time. When the text runs out, it returns `Ok(None)`. `io.print`, `io.write`
and `io.inspect` write to `output` instead of the terminal. This includes
anything printed by tasks the function starts. `dbg` still prints to the
terminal, so you can debug the function as usual.

What the function writes to standard error, with `io.eprint` and
`io.ewrite`, goes to `errors` instead, kept apart from `output`:

```nomi-run
import std/io

fn parse_count(text: String) -> Int {
    case String.to_int(text) {
        Some(n) ->
            n

        None -> {
            io.eprint("warning: ${text} is not a number, counting 0")
            0
        }
    }
}

test "a bad count warns on stderr" {
    run = io.capture("", fn -> parse_count("ten"))

    assert run.value == 0
    assert run.output == ""
    assert run.errors == "warning: ten is not a number, counting 0\n"
}
```
<!-- expect
ok a bad count warns on stderr
test result: ok. 1 passed, 0 failed
-->

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
        Ok(Some(name)) -> {
            io.write("Hello, ${name}. Your age: ")

            case io.read_line() {
                Ok(Some(age)) -> io.print("${name} is ${age}")
                _ -> io.print("no age")
            }
        }

        _ ->
            io.print("hello, stranger")
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
ok asks for a name, then an age
test result: ok. 1 passed, 0 failed
-->

Some programs print a prompt with no newline and then read, as
`io.write("Hello, Ada. Your age: ")` does here. In the script, the prompt is
an output line of its own, and the typed text goes on the input line below
it.

An input line the program never reads fails the test, and standard error is
not compared. The [`io.replay`](/reference/io/#ioreplay) reference has the
rest of the rules.

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
ok converts to Fahrenheit
test result: ok. 1 passed, 0 failed
-->

## Groups and setup

`group` gathers related tests under one name. A group can have a `setup`. It
runs before each test in the group and produces a value. A test receives that
value through a pattern after its name.

```nomi-run
group "user labels" {
    setup #{name: "Ada", role: "admin"}

    test "setup value is explicit", #{name, role} {
        assert name == "Ada"
        assert role == "admin"
    }

    test "a test may ignore the setup value" {
        assert True
    }
}
```
<!-- expect
ok user labels / setup value is explicit
ok user labels / a test may ignore the setup value
test result: ok. 2 passed, 0 failed
-->

A test sees only the value `setup` returns. Names bound inside the `setup`
block don't reach the tests.

The value can be anything. A test can bind a record with `#{name, role}`, a
tuple with `#(left, right)`, or the whole value with a name, as in
`test "loads rows", db { ... }`. The pattern has to match every value setup
can produce, so a pattern such as `Some(n)` is an error. Bind a name instead,
and match it in the test's body.

Groups don't nest. When two sets of tests need different setup, write two
groups side by side. Put the part they share in an ordinary function.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A group holds at most one `clock`, one `boot` and one `setup`. For each
  test they run in that order, wherever they are written, then the test's
  body. Nothing carries from one test to the next.
- A `defer` in the setup runs when the test it served finishes.
- A `with` line in the setup stays in force for the test's body, so setup
  is where a group installs fakes.
- A `?` or a failed assertion in the setup fails the test, and the body
  doesn't run.

Spec: [Groups](/spec/tests/#groups).

</div>

## Booting the application

A group can also start the application with a `boot` line;
[App Fields, Defer & Context](/app-fields/#testing-code-that-reads-app-fields)
shows how.

## Virtual time

A test that waits is slow, and on a busy machine it may fail by accident.
Add `clock Clock.Virtual` to a group to avoid both. Its tests run under a fake
clock. Whenever every task in the test is waiting, the clock jumps forward,
so waiting takes no real time.

```nomi-run
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
<!-- expect
ok retry policy / gives up after backing off
test result: ok. 1 passed, 0 failed
-->

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
timeout, such as `Supervisor.flush`.

The fake clock is off by default because it changes how a test behaves:

- Waiting on something outside the program, such as reading input or a real
  network call, never lets the clock move. The test is reported as deadlocked.
- Background work still running when a test ends is an error. It does not
  carry on into the next test.
- `Instant.now` starts at midnight UTC on 1 January 2000.

The first two usually point to a problem in the test. Still, they change
behaviour, so each group opts in.

## Long-running tests and the debugger

`nomi test` stops a test that runs longer than 10 seconds of real time, even
under a fake clock, and reports the line it was on and the calls that led
there. `--timeout` changes the limit
([Running tests](/tooling/running-tests/#timeouts)).

To step through a test, click Debug test beside it in VS Code or Neovim, or
pick its debug entry in Zed. The debugger stops where an assertion fails,
shows the value each `|>` stage produced, and can step back
([Debugging](/tooling/editors-and-debugger/#debugging)).

[Generics](/generics/) is next. It writes one function or type that works
with many types.
