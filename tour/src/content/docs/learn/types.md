---
title: "3. Types"
description: "First Steps, lesson 3: the kinds of value, Int, Decimal, String and Bool, and how Nomi checks them before your code runs."
---

Every value has a **type**, the kind of value it is. These four come up
all the time:

- `Int`, a whole number: `3`, `-12`, `2026`. Short for *integer*.
- `Decimal`, a number with a decimal point, written with a `d` after it:
  `3.5d`, `19.99d`, `0.25d`.
- `String`, a piece of text: `"Ada"`, `"3"`.
- `Bool`, an answer to a yes-or-no question: `True` or `False`.

The type decides what a value can do. `+` adds two numbers. Given two
pieces of text, it joins them, so `"3" + "4"` is `"34"`:

```nomi-run
fn main()
    dbg 3 + 4
    dbg "3" + "4"
    dbg 19.99d * 3d
    dbg 10 > 3
end
```
<!-- expect
dbg line 2: 3 + 4 = 7
dbg line 3: "3" + "4" = "34"
dbg line 4: 19.99d * 3d = 59.97d
dbg line 5: 10 > 3 = True
-->

Nomi checks the types in your code before it runs any of it. Adding a
number to a piece of text makes no sense, so this program doesn't run at
all. The message says which two types met:

```nomi-run error
fn main()
    dbg 3 + "4"
end
```
<!-- expect
main.nomi:2:11: `+` needs two values of one type, and these are Int and String
-->

That's why a function's first line names types. In
`fn rectangle_area(width: Int, height: Int) -> Int` from the last lesson,
`width: Int` says `width` must be an Int, and `-> Int` says the function
gives back an Int. Nomi checks every call against that line, so
`rectangle_area("3", 4)` is caught before the program runs.

Whole numbers and decimals are different types too: `1 + 2.5d` is an
error. Write the whole number as a Decimal, `1d + 2.5d`.

Nomi has a second kind of decimal number, **Float**, written without the
`d`: `3.5`. It is fast and approximate, made for science and graphics, so
its answers can be a tiny bit off where a Decimal's are exact:

```nomi-run
fn main()
    dbg 0.1 + 0.2
    dbg 0.1d + 0.2d
end
```
<!-- expect
dbg line 2: 0.1 + 0.2 = 0.30000000000000004
dbg line 3: 0.1d + 0.2d = 0.3d
-->

For prices, measurements and the other numbers people read, use Decimal.

## Your turn

`midpoint` gets two Decimals and should give back the number halfway
between them: their sum divided by 2. Right now it gives back only the sum.

```nomi-run lesson
fn midpoint(a: Decimal, b: Decimal) -> Decimal
    a + b
end

// The checks below run when you press Check. You don't need to change them.
test "halfway between 3 and 4 is 3.5"
    assert midpoint(3d, 4d) == 3.5d
end

test "halfway between 1.25 and 1.75 is 1.5"
    assert midpoint(1.25d, 1.75d) == 1.5d
end
```

```nomi-hint
Divide the whole sum, so it needs parentheses. And Nomi won't divide a
Decimal by an Int: how do you write 2 as a Decimal?
```

```nomi-solution
fn midpoint(a: Decimal, b: Decimal) -> Decimal
    (a + b) / 2d
end

// The checks below run when you press Check. You don't need to change them.
test "halfway between 3 and 4 is 3.5"
    assert midpoint(3d, 4d) == 3.5d
end

test "halfway between 1.25 and 1.75 is 1.5"
    assert midpoint(1.25d, 1.75d) == 1.5d
end
```
