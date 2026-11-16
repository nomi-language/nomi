---
title: "9. Pipes and map"
description: "First Steps, lesson 9: passing a value through steps with |>, and changing every value in a list with Iter.map."
---

`|>` is a **pipe**: it passes the value on its left into the function on
its right, as that function's first value. `scores |> Iter.sum()` means
the same as `Iter.sum(scores)`, and `3 |> rectangle_area(4)` the same as
`rectangle_area(3, 4)`.

Pipes let you write steps in the order they happen, one per line. A line
that starts with `|>` carries on the line above it:

```nomi-run
fn main()
    dbg [1, 2, 3]
    |> Iter.map(fn n -> n * 10)
    |> Iter.to_list()
end
```
<!-- expect
dbg line 2:
  [1, 2, 3]
  |> Iter.map(fn n -> n * 10)
  |> Iter.to_list()
  = [10, 20, 30]
-->

`Iter.map` changes every value with a function. `fn n -> n * 10` is a
function with no name: it takes `n` and gives back `n * 10`. You write it
right where it's needed, so it needs no name. When the function you need
already has a name, pass the name itself, without parentheses:
`Iter.map(String.uppercase)` changes every value with `String.uppercase`.

`Iter.map` gives back an **Iter**, a sequence of values that isn't a list
yet: it works each value out only when something asks for it.
`Iter.to_list` asks for all of them and collects them into a list. When a
function promises a `List`, end the pipe with `|> Iter.to_list()`.

## Your turn

`lengths` should give back how long each word of its list is, in the same
order. Right now it gives back how many words there are, in a list of one.

```nomi-run lesson
fn lengths(words: List<String>) -> List<Int>
    [Iter.count(words)]
end

// The checks below run when you press Check. You don't need to change them.
test "three words"
    assert lengths(["a", "map", "pipe"]) == [1, 3, 4]
end

test "no words"
    assert lengths([]) == []
end
```

```nomi-hint
Pipe words through Iter.map with String.length, which gives one word's
length, then through Iter.to_list().
```

```nomi-solution
fn lengths(words: List<String>) -> List<Int>
    words
    |> Iter.map(String.length)
    |> Iter.to_list()
end

// The checks below run when you press Check. You don't need to change them.
test "three words"
    assert lengths(["a", "map", "pipe"]) == [1, 3, 4]
end

test "no words"
    assert lengths([]) == []
end
```
