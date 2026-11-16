---
title: "16. Repeating until done"
description: "First Steps, lesson 16: repeating a step until a condition is met, with Iter.loop and break."
---

To do something to every value in a list, you have the `Iter` functions:
`map`, `filter`, `sum`, `each`. Sometimes you don't have a list. You have
a starting value and a step, and you repeat the step until something is
true, without knowing how many times that will take. That is
`Iter.loop`:

```nomi-run
fn first_double_above(limit: Int) -> Int
    Iter.loop(1, fn n ->
        if n > limit then break n
        n * 2
    end)
end

fn main()
    dbg first_double_above(100)
end
```
<!-- expect
dbg line 9: first_double_above(100) = 128
-->

`Iter.loop` takes a starting value, here 1, and a function for one step.
The function gets the current value as `n`, and the value it gives back is
the next `n`: 1, 2, 4, 8 and so on. Two things in it are new:

- A function with no name can take several lines, like any other. This one
  ends with `end`, and the `)` after it closes `Iter.loop(`.
- `break n` stops the loop, and `n` is what `Iter.loop` gives back. The
  `if` before it has no `else`, because it doesn't choose a value: it only
  decides whether to stop. Here the loop stops at the first `n` past 100,
  which is 128.

Each step has to move toward the stop. A step that never gets there, such
as `n * n` starting from 1, which stays 1, repeats forever. The page stops
a program that runs too long and says "execution timed out".

## Your turn

`first_square_above` should give back the first square number greater than
`limit`. The square numbers are 1, 4, 9, 16, 25 and so on: `n * n` for
each `n`. Right now it gives back the first `n` greater than `limit`
instead of a square.

```nomi-run lesson
fn first_square_above(limit: Int) -> Int
    Iter.loop(1, fn n ->
        if n > limit then break n
        n + 1
    end)
end

// The checks below run when you press Check. You don't need to change them.
test "the first square above 50 is 64"
    assert first_square_above(50) == 64
end

test "the first square above 10 is 16"
    assert first_square_above(10) == 16
end

test "the first square above 0 is 1"
    assert first_square_above(0) == 1
end
```

```nomi-hint
Keep counting n up by 1, but test the square: stop when n * n > limit,
and break with n * n.
```

```nomi-solution
fn first_square_above(limit: Int) -> Int
    Iter.loop(1, fn n ->
        if n * n > limit then break n * n
        n + 1
    end)
end

// The checks below run when you press Check. You don't need to change them.
test "the first square above 50 is 64"
    assert first_square_above(50) == 64
end

test "the first square above 10 is 16"
    assert first_square_above(10) == 16
end

test "the first square above 0 is 1"
    assert first_square_above(0) == 1
end
```
