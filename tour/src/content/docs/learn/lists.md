---
title: "8. Lists"
description: "First Steps, lesson 8: keeping many values in a list, and adding them up."
---

A **list** keeps several values in order. Write it with square brackets
and commas:

```nomi-run
fn main()
    scores = [7, 9, 4]
    dbg scores
    dbg Iter.count(scores)
    dbg Iter.sum(scores)
    dbg 9 in scores
end
```
<!-- expect
dbg line 3: scores = [7, 9, 4]
dbg line 4: Iter.count(scores) = 3
dbg line 5: Iter.sum(scores) = 20
dbg line 6: 9 in scores = True
-->

`Iter.count` counts the values in a list, and `Iter.sum` adds them up.
`Iter` is short for *iterate*, which means to go through values one at a
time, and it holds the functions that work through a list that way. `in`
asks whether a value is in a list, and answers with a Bool.

A list's type says what it holds: `List<Int>`, read "list of Int", is a
list of whole numbers.

## Your turn

`summary` gets a list of scores and should describe them in a line of
text: how many there are and their total, as in `"3 scores, total 20"`.
Right now it gives only how many.

```nomi-run lesson
fn summary(scores: List<Int>) -> String
    "${Iter.count(scores)} scores"
end

// The checks below run when you press Check. You don't need to change them.
test "three scores"
    assert summary([7, 9, 4]) == "3 scores, total 20"
end

test "no scores"
    assert summary([]) == "0 scores, total 0"
end
```

```nomi-hint
Add ", total " and then the sum, with a second ${...} around Iter.sum(scores).
```

```nomi-solution
fn summary(scores: List<Int>) -> String
    "${Iter.count(scores)} scores, total ${Iter.sum(scores)}"
end

// The checks below run when you press Check. You don't need to change them.
test "three scores"
    assert summary([7, 9, 4]) == "3 scores, total 20"
end

test "no scores"
    assert summary([]) == "0 scores, total 0"
end
```
