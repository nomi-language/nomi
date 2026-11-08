---
title: Coming from Python
description: What carries over from Python to Nomi, what differs, and the same program written in both.
---

Much of Python carries over: readable code with little punctuation, named
arguments and defaults, first-class functions, tuples you unpack, and string
interpolation. What differs most: every type is checked before the program
runs, values never change, blocks use braces rather than indentation, and a
failure is a value the function returns rather than an exception it raises.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## What differs

| Python | Nomi |
|---|---|
| `def area(w, h=1):` | `fn area(width: Int, height: Int = 1) -> Int { .. }`: parameter types are written, and braces hold the body |
| `x = 1`, then `x += 1` | `x = 1`, then `x = x + 1`. Values are immutable; the name is bound again. |
| `s.strip()`, `len(xs)` | `String.trim(s)`, `Iter.count(xs)`: functions are called through their owner |
| `[f(x) for x in xs if p(x)]` | `xs \|> Iter.filter(p) \|> Iter.map(f) \|> Iter.to_list()` |
| `{"a": 1}`, `{1, 2}` | `%{"a" => 1}`, `%[1, 2]` |
| `(a, b)`, `a, b = pair` | `(a, b)`, `(a, b) = pair` |
| `None` | `None`, but only in a `Maybe<T>`: a value that may be missing says so in its type |
| `raise ValueError(..)`, `try:` | Return `Result<T, E>`; a postfix `?` passes an `Err` up |
| `class User:` with `self.name` | `struct User { name: String }`, read as `user.name`; behavior goes in `impl User { .. }` |
| `f"{name} has {n}"` | `"${name} has ${n}"` |
| `for x in xs:`, `while` | `Iter.each`, a fold, recursion, or `Iter.loop` |
| `if xs:` on any value | `if` takes a `Bool`: `if Iter.is_empty(xs)`, `if count > 0` |
| `import json` | `import std::json` |
| `def test_x():` with pytest | `test "name" { assert .. }` beside the code |

`1 / 2` on two `Int`s is `0`; convert an operand with `Int.to_float` for a
`Float` result. Statements end at a newline, as in Python.

## The same program

```python
def word_lengths(text: str) -> dict[str, int]:
    return {w.lower(): len(w) for w in text.split() if len(w) > 2}

def average(numbers: list[int]) -> float | None:
    if not numbers:
        return None
    return sum(numbers) / len(numbers)
```

```nomi-run
// FILE: stats.nomi
fn word_lengths(text: String) -> Map<String, Int> {
    text
    |> String.words()
    |> Iter.filter(fn word -> String.length(word) > 2)
    |> Iter.map(fn word -> (String.lowercase(word), String.length(word)))
    |> Iter.to_map()
}

fn average(numbers: List<Int>) -> Maybe<Float> {
    if Iter.is_empty(numbers) {
        None
    } else {
        Some(Int.to_float(Iter.sum(numbers)) / Int.to_float(Iter.count(numbers)))
    }
}

// FILE: stats_test.nomi
import stats.{average, word_lengths}

test "the same program" {
    assert word_lengths("The cat sat on a Mat") == %{"the" => 3, "cat" => 3, "sat" => 3, "mat" => 3}
    assert average([1, 2, 3, 4]) == Some(2.5)
    assert average([]) == None
}
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

The comprehension becomes a pipe that reads in the order it runs: split,
filter, map, collect. `average` says in its type that it may have no result,
so a caller cannot forget the empty case.
