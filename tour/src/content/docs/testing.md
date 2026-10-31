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
import std/calendar.{Date, Error}

test "assert can match an error variant" {
    assert Err(Error.InvalidFormat(_)) = Date.parse("not-a-date")
}
```

When a `Result` or `Maybe` is only setup for the rest of the test, put `?`
after the call. If it is `Err` or `None`, the test fails there. Use a pattern
assertion instead when the result is the thing you are testing.

```nomi-run
import std/calendar.Date

test "formats parsed date" {
    d = Date.parse("2026-05-04")?

    assert Date.to_string(d) == "2026-05-04"
}
```

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

## Subtests

`subtests` runs the same check over a list of cases. Each case is a row.

```nomi-run
fn is_palindrome(text: String) -> Bool {
    String.reverse(text) == text
}

test "determines palindromes" {
    cases = [#("abba", True), #("abc", False), #("x", True)]

    subtests cases, #(line, known_palindrome) {
        assert is_palindrome(line) == known_palindrome
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

```console
FAIL palindrome_test.nomi :: determines palindromes
  subtest #("abc", True): line 9: assertion failed
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
    cases = [
        #("year divisible by 400 is leap year", 2000, True),
        #("year divisible by 100, not divisible by 400 in common year", 1900, False),
        #("year divisible by 4, not divisible by 100 in leap year", 1996, True),
    ]

    subtests cases, #(description, year, expected), description {
        assert is_leap_year(year) == expected
    }
}
```

A failed row's report shows its name in place of the row. If `is_leap_year`
returned `False` for 2000, the test would report:

```console
FAIL leap_test.nomi :: leap years
  subtest "year divisible by 400 is leap year": line 13: assertion failed
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
`subtests cases, #(a, b, sum), "${a} + ${b}" { ... }`. To leave a column out,
name it `_` as usual.

## Input and output

[`io.capture`](/reference/io/) runs a function and gives it the text you pass
as its input. It returns what the function returned, in `value`, and
everything it printed, in `output`. Pass it `main` to check what a program
prints for some input. For code that reads or writes files, `fs_testing.run`
does the same with a file system in memory, as
[Input & Output](/input-and-output/#testing-code-that-uses-files) shows.

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

A test sees only the value `setup` returns. Names bound inside the `setup`
block don't reach the tests.

The value can be anything. A test can bind a record with `#{name, role}`, a
tuple with `#(left, right)`, or the whole value with a name, as in
`test "loads rows", db { ... }`. The pattern has to match every value setup
can produce, so a pattern such as `Some(n)` is an error. Bind a name instead,
and match it in the test's body.

Groups don't nest. When two sets of tests need different setup, write two
groups side by side. Put the part they share in an ordinary function.

## Booting the application

A group can also start the application with a `boot` line;
[App Fields, Defer & Context](/capabilities-and-context/#testing-code-that-reads-app-fields)
shows how.

## Virtual time

A group can run its tests under a fake clock, so waiting takes no real time:
[Concurrency](/concurrency/#virtual-time) shows `clock Clock.Virtual`.

## A test that never finishes

`nomi test` gives each test 10 seconds. A test that is still running after
that is stopped and reported as failed. The report shows the line the test was
running and the calls that led to it. Then the next test runs as usual.

- The 10 seconds are real time, even in a group with a fake clock.
- They cover the whole test: its group's `clock`, `boot` and `setup` lines as
  well as the test's own block.
- `nomi test --timeout 30s` gives each test 30 seconds instead.
  `--timeout 0` removes the limit.
- A test run in the debugger, described next, has no limit, so pausing it
  never makes it fail.

## Debugging a test

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
