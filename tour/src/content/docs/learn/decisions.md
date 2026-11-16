---
title: "6. Decisions"
description: "First Steps, lesson 6: choosing between two values with if, between more with case, and checking a number before dividing by it."
---

A program often has to choose. `if`, `then` and `else` choose between two
values:

```nomi-run
fn main()
    age = 15
    dbg if age >= 18 then "adult" else "young"
end
```
<!-- expect
dbg line 3: if age >= 18 then "adult" else "young" = "young"
-->

The part after `if` is a question whose answer is `True` or `False`. When
it is `True` you get the value after `then`; when it is `False`, the value
after `else`. The questions you can ask about numbers are `<` (less than),
`>` (greater than), `<=` and `>=` (or equal), `==` (equal) and `!=` (not
equal). Note the two `=` in `==`: one `=` gives a name a value.

To choose between more than two values, use `case`. Each line under it
is a question, `->`, and the value to give back when its answer is `True`.
Nomi asks them from the top and takes the first that is `True`. `_` stands
for "anything else", so the last line catches what the questions above it
didn't. `end` closes the `case`:

```nomi-run
fn size(n: Int) -> String
    case
        n < 10  -> "small"
        n < 100 -> "medium"
        _       -> "large"
    end
end

fn main()
    dbg size(5)
    dbg size(50)
    dbg size(500)
end
```
<!-- expect
dbg line 10: size(5) = "small"
dbg line 11: size(50) = "medium"
dbg line 12: size(500) = "large"
-->

`if` is also how you divide safely. Dividing a whole number by zero has no
answer, so when the Int you divide by could be zero, Nomi won't run your
code until you check it. Here `share` splits some sweets between children,
and nothing stops `children` from being 0:

```nomi-run error
fn share(sweets: Int, children: Int) -> Int
    sweets / children
end

fn main()
    dbg share(10, 3)
end
```
<!-- expect
main.nomi:2:14: `/` divides by a NonZeroInt; `children` is an Int
main.nomi:2:14: help: check it first (`if children == 0 then ...`) or take a NonZeroInt
-->

The message says `/` wants a **NonZeroInt**, a whole number that is never
zero. Its help says what to do: check it first.

```nomi-run
fn share(sweets: Int, children: Int) -> Int
    if children == 0 then 0 else sweets / children
end

fn main()
    dbg share(10, 3)
    dbg share(10, 0)
end
```
<!-- expect
dbg line 6: share(10, 3) = 3
dbg line 7: share(10, 0) = 0
-->

In the `else`, `children` can't be zero, and Nomi knows it, so the division
is allowed there. You choose the answer for when there is no one to share
with: here, 0 each. Nomi checks a Decimal you divide by in the same way.

## Your turn

`describe` gets a temperature in degrees. It should give back `"cold"` below
10, `"hot"` from 25 up, and `"mild"` in between. Right now it knows only
cold and mild.

```nomi-run lesson
fn describe(temperature: Int) -> String
    if temperature < 10 then "cold" else "mild"
end

// The checks below run when you press Check. You don't need to change them.
test "5 degrees is cold"
    assert describe(5) == "cold"
end

test "18 degrees is mild"
    assert describe(18) == "mild"
end

test "30 degrees is hot"
    assert describe(30) == "hot"
end
```

```nomi-hint
With three answers, this is a case, like size in the example: one line for
cold, one for hot, and _ for mild.
```

```nomi-solution
fn describe(temperature: Int) -> String
    case
        temperature < 10  -> "cold"
        temperature >= 25 -> "hot"
        _                 -> "mild"
    end
end

// The checks below run when you press Check. You don't need to change them.
test "5 degrees is cold"
    assert describe(5) == "cold"
end

test "18 degrees is mild"
    assert describe(18) == "mild"
end

test "30 degrees is hot"
    assert describe(30) == "hot"
end
```
