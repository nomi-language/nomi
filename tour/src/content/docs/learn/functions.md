---
title: "4. Functions"
description: "First Steps, lesson 4: writing a function, giving it inputs, and using one function inside another."
---

A **function** is a named piece of code you can use again. You give it
values, its **parameters**, and it gives back a value. Here is one:

```nomi-run
fn double(n: Int) -> Int
    n * 2
end

fn main()
    dbg double(5)
    dbg double(21)
end
```
<!-- expect
dbg line 6: double(5) = 10
dbg line 7: double(21) = 42
-->

Read the first line as: a function named `double` takes one Int, `n`,
and gives back an Int. The last line inside the function is what it gives
back.

To use a function, **call** it: write its name and the values in
parentheses, `double(5)`. A function can call other functions.

A function can take several lines to work out its answer, naming values
on the way as `main` did in the lesson on names. Only the last line is
given back:

```nomi-run
fn order_cost(price: Int, count: Int) -> Int
    items = price * count
    delivery = 5
    items + delivery
end

fn main()
    dbg order_cost(3, 4)
end
```
<!-- expect
dbg line 8: order_cost(3, 4) = 17
-->

## Your turn

`triple` should give back three times its number. Try writing it with
`double`: three times a number is double the number, plus the number once
more.

```nomi-run lesson
fn double(n: Int) -> Int
    n * 2
end

fn triple(n: Int) -> Int
    double(n)
end

// The checks below run when you press Check. You don't need to change them.
test "triple of 4 is 12"
    assert triple(4) == 12
end

test "triple of 10 is 30"
    assert triple(10) == 30
end
```

```nomi-hint
double(n) is two times n. Add n once more.
```

```nomi-solution
fn double(n: Int) -> Int
    n * 2
end

fn triple(n: Int) -> Int
    double(n) + n
end

// The checks below run when you press Check. You don't need to change them.
test "triple of 4 is 12"
    assert triple(4) == 12
end

test "triple of 10 is 30"
    assert triple(10) == 30
end
```
