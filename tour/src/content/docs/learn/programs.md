---
title: "15. Programs that print"
description: "First Steps, lesson 15: printing with io.print, doing something for each value with Iter.each, and counting with ranges."
---

`dbg` is for you while you write code: it shows the code, its line and its
value. A program's output for the people who use it is printed with
`io.print`, which shows only the value:

```nomi-run
import std::io

fn main()
    io.print("Shopping list")
    ["eggs", "milk", "bread"] |> Iter.each(fn item -> io.print("- ${item}"))
end
```
<!-- expect
Shopping list
- eggs
- milk
- bread
-->

Two things are new here:

- `import std::io` at the top lets the file use `io`, Nomi's tools for
  input and output, and `io.print` prints one line. `Iter`, `String` and
  `Map` are in every file, but most code doesn't print, so a file that
  does asks for `io`.
- `Iter.each` calls its function once for each value, for what the
  function does rather than a value it gives back.

`1..=5` is a **range**, the numbers from 1 to 5 with both ends included. It
works with `Iter` functions like a list does:

```nomi-run
fn main()
    dbg 1..=5 |> Iter.map(fn n -> n * n) |> Iter.to_list()
end
```
<!-- expect
dbg line 2: 1..=5 |> Iter.map(fn n -> n * n) |> Iter.to_list() = [1, 4, 9, 16, 25]
-->

## Your turn

`times_table` should give back the lines of a times table, from 1 up to 5:
for 3 that is `"3 x 1 = 3"`, `"3 x 2 = 6"` and so on, up to
`"3 x 5 = 15"`. Right now it gives back only the first line.

A test can't see what a program prints, so the task builds the lines, and
a program prints them with `times_table(3) |> Iter.each(io.print)`.
`io.print` is passed by its name, as `String.uppercase` was in the lesson
on pipes.

```nomi-run lesson
fn times_table(by: Int) -> List<String>
    ["${by} x 1 = ${by}"]
end

// The checks below run when you press Check. You don't need to change them.
test "the table of 3"
    assert times_table(3) == ["3 x 1 = 3", "3 x 2 = 6", "3 x 3 = 9", "3 x 4 = 12", "3 x 5 = 15"]
end

test "the table of 10"
    assert times_table(10) == [
        "10 x 1 = 10",
        "10 x 2 = 20",
        "10 x 3 = 30",
        "10 x 4 = 40",
        "10 x 5 = 50",
    ]
end
```

```nomi-hint
Map the range 1..=5 to a line for each n: "${by} x ${n} = ${by * n}".
Then collect the lines with Iter.to_list().
```

```nomi-solution
fn times_table(by: Int) -> List<String>
    1..=5
    |> Iter.map(fn n -> "${by} x ${n} = ${by * n}")
    |> Iter.to_list()
end

// The checks below run when you press Check. You don't need to change them.
test "the table of 3"
    assert times_table(3) == ["3 x 1 = 3", "3 x 2 = 6", "3 x 3 = 9", "3 x 4 = 12", "3 x 5 = 15"]
end

test "the table of 10"
    assert times_table(10) == [
        "10 x 1 = 10",
        "10 x 2 = 20",
        "10 x 3 = 30",
        "10 x 4 = 40",
        "10 x 5 = 50",
    ]
end
```
