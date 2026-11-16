---
title: "10. Picking values with filter"
description: "First Steps, lesson 10: keeping only some values of a list with Iter.filter, and pipes of several steps."
---

`Iter.filter` keeps the values for which its function answers `True`, and
drops the rest:

```nomi-run
fn main()
    dbg [1, 2, 3, 4]
    |> Iter.filter(fn n -> n > 2)
    |> Iter.map(fn n -> n * 10)
    |> Iter.to_list()
end
```
<!-- expect
dbg line 2:
  [1, 2, 3, 4]
  |> Iter.filter(fn n -> n > 2)
  |> Iter.map(fn n -> n * 10)
  |> Iter.to_list()
  = [30, 40]
-->

`fn n -> n > 2` gives back whether `n` is greater than 2, so the filter
keeps 3 and 4. The next step, the `map`, sees only those two. A pipe can
have as many steps as you need, and each works on what the one above it
gave back.

## Your turn

`odd_squares` should keep only the odd numbers of its list, then multiply
each of those by itself. Right now it gives back the list unchanged.

A number is odd when dividing it by 2 leaves something over, so when
`n % 2 != 0`, with `%` from the lesson on names.

```nomi-run lesson
fn odd_squares(numbers: List<Int>) -> List<Int>
    numbers
end

// The checks below run when you press Check. You don't need to change them.
test "the odd numbers of 1 to 5, squared"
    assert odd_squares([1, 2, 3, 4, 5]) == [1, 9, 25]
end

test "a negative odd number"
    assert odd_squares([-3, 4]) == [9]
end

test "no odd numbers"
    assert odd_squares([2, 4]) == []
end
```

```nomi-hint
Three steps: Iter.filter with a function that asks whether n is odd, then
Iter.map with one that multiplies n by itself, then Iter.to_list().
```

```nomi-solution
fn odd_squares(numbers: List<Int>) -> List<Int>
    numbers
    |> Iter.filter(fn n -> n % 2 != 0)
    |> Iter.map(fn n -> n * n)
    |> Iter.to_list()
end

// The checks below run when you press Check. You don't need to change them.
test "the odd numbers of 1 to 5, squared"
    assert odd_squares([1, 2, 3, 4, 5]) == [1, 9, 25]
end

test "a negative odd number"
    assert odd_squares([-3, 4]) == [9]
end

test "no odd numbers"
    assert odd_squares([2, 4]) == []
end
```
