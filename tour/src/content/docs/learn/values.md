---
title: "1. Values and dbg"
description: "First Steps, lesson 1: what a value is, how to look at one with dbg, and your first check."
---

A program works with **values**: pieces of data such as the number `42`,
the text `"Hello"`, or `True`. Text goes between double quotes, so Nomi can
tell where it starts and ends.

`dbg`, short for *debug*, shows you a value while the program runs.
(A **bug** is a mistake in a program, and debugging is finding and fixing
one.) Change a value in the example below, and the output changes as you
type:

```nomi-run
fn main()
    dbg "Hello"
    dbg 40 + 2
    dbg 10 > 3
end
```
<!-- expect
dbg line 2: "Hello" = "Hello"
dbg line 3: 40 + 2 = 42
dbg line 4: 10 > 3 = True
-->

Each line of the output says which line of the program it came from, what
was written there, and the value it made. `40 + 2` makes `42`, and
`10 > 3` asks a question whose answer is `True`. A value written out in
full, like `"Hello"`, makes itself, so it shows up on both sides.

The lines around them hold the program together:

- `fn` starts a **function**, a named piece of code. The lesson on
  functions is about them.
- `main` is the function every program starts with. Nomi runs the lines
  inside `main`, top to bottom.
- `end` closes it. Everything between `fn main()` and `end` is the
  program.

Each task gives you a function that is almost finished.

## Your turn

The function `greeting` gives back a piece of text: `-> String` means
"gives back a String", and a **String** is Nomi's name for text. Right now
it gives back `"Hello, world"`. Make it give back `"Hello, Ada"` instead,
then press **Check**.

Under your code are the **checks**. Each `test` has a name in quotes, and
`assert` means "this must be true". `==` asks whether two values are
equal, so the one below says that `greeting()` must give back
`"Hello, Ada"`. Check runs them and tells you whether your code does what
they expect.

The line above the checks starts with `//`, which makes it a **comment**:
a note for people reading the code. Nomi skips it.

```nomi-run lesson
fn greeting() -> String
    "Hello, world"
end

// The checks below run when you press Check. You don't need to change them.
test "the greeting says hello to Ada"
    assert greeting() == "Hello, Ada"
end
```

```nomi-hint
Change only the text between the quotes on the second line. Keep the
quotes themselves: they are what make it text.
```

```nomi-solution
fn greeting() -> String
    "Hello, Ada"
end

// The checks below run when you press Check. You don't need to change them.
test "the greeting says hello to Ada"
    assert greeting() == "Hello, Ada"
end
```
