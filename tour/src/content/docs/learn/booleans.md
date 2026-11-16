---
title: "7. True and false"
description: "First Steps, lesson 7: Bool values, and joining questions with and, or and not."
---

A question such as `age >= 18` gives back a `Bool`: `True` or `False`.
Three words build bigger questions from smaller ones:

- `a and b` is `True` only when both are.
- `a or b` is `True` when either one is, or both.
- `not a` turns `True` into `False`, and `False` into `True`.

A cinema gives a discount to children under 12 and to people 65 or older:

```nomi-run
fn has_discount(age: Int) -> Bool
    age < 12 or age >= 65
end

fn main()
    dbg has_discount(8)
    dbg has_discount(30)
    dbg has_discount(70)
    dbg not has_discount(30)
end
```
<!-- expect
dbg line 6: has_discount(8) = True
dbg line 7: has_discount(30) = False
dbg line 8: has_discount(70) = True
dbg line 9: not has_discount(30) = True
-->

A function that answers a yes-or-no question gives back a `Bool`, and its
name often reads as the question: `has_discount`, `is_empty`. Its answer
can go straight into an `if`: `if has_discount(age) then 5 else 8`.

## Your turn

A roller coaster lets you ride when you are at least 8 years old and at
least 120 cm tall. `can_ride` should answer whether someone can. Right now
it checks only the age.

```nomi-run lesson
fn can_ride(age: Int, height: Int) -> Bool
    age >= 8 and height > 0
end

// The checks below run when you press Check. You don't need to change them.
test "old enough and tall enough"
    assert can_ride(10, 130)
end

test "old enough, but too short"
    assert not can_ride(10, 110)
end

test "tall enough, but too young"
    assert not can_ride(6, 130)
end
```

```nomi-hint
Keep age >= 8, and replace the second question with one about the height:
is it at least 120?
```

```nomi-solution
fn can_ride(age: Int, height: Int) -> Bool
    age >= 8 and height >= 120
end

// The checks below run when you press Check. You don't need to change them.
test "old enough and tall enough"
    assert can_ride(10, 130)
end

test "old enough, but too short"
    assert not can_ride(10, 110)
end

test "tall enough, but too young"
    assert not can_ride(6, 130)
end
```
