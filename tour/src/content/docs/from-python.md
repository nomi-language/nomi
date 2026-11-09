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

## Will trip you up

| Python | Nomi |
|---|---|
| `x = 1`, then `x += 1` | `x = 1`, then `x = x + 1`. Values are immutable; the name is bound again. |
| `s.strip()`, `len(xs)` | `String.trim(s)`, `Iter.count(xs)`: functions are called through their owner |
| `class User:` with `self.name` | `struct User { name: String }`, read as `user.name`; behavior goes in `impl User { .. }` |
| `None` | `None`, but only in a `Maybe<T>`: a value that may be missing says so in its type |
| `raise ValueError(..)`, `try:` | Return `Result<T, E>`; a postfix `?` passes an `Err` up |
| `if xs:` on any value | `if` takes a `Bool`: `if Iter.is_empty(xs)`, `if count > 0` |
| `match`, which does nothing when no case matches | `case`, which must cover every value; the checker rejects one that does not |
| `pair[0]` | `pair.first`, `pair.second`, ...: a tuple's items are read by name |
| `1 / 2` is `0.5`; `-7 // 2` is `-4`; `-7 % 3` is `2` | `/` on two `Int`s truncates toward zero: `1 / 2` is `0` and `-7 / 2` is `-3`. Convert an operand with `Int.to_float` for a `Float` result. `%` takes the sign of its left operand, so `-7 % 3` is `-1`; `Int.floor_div` and `Int.modulo` are Python's `//` and `%`. |
| `float(n)`, `int(s)` | `Int.to_float(n)`, `String.to_int(s)`, which returns a `Maybe`. Nomi's `x as T` converts nothing: it only states a type the value already fits (`[] as List<String>`). |
| `with open(path) as f:` | `defer Reader.close_quietly(r)` on the line after opening; the checker requires it for a resource |
| `for x in xs:`, `while` | `Iter.each`, a fold, recursion, or `Iter.loop` |
| A `create_app()` factory that sets `app.config` and calls `db.init_app(app)` | `fn boot() -> Result<App, String>` builds an `App` struct before `main` runs, and any function reads a field as `App.db`. A test replaces one with `with App.db = ..`. See [App Fields](/app-fields/). |

## Spelled differently

| Python | Nomi |
|---|---|
| `def area(w, h=1):` | `fn area(width: Int, height = 1) -> Int { .. }`: a parameter without a default writes its type, and braces hold the body |
| `[f(x) for x in xs if p(x)]` | `xs \|> Iter.filter(p) \|> Iter.map(f) \|> Iter.to_list()` |
| `{"a": 1}`, `{1, 2}` | `%{"a" => 1}`, `%[1, 2]` |
| `(a, b)`, `a, b = pair` | `(a, b)`, `(a, b) = pair` |
| `f"{name} has {n}"` | `"${name} has ${n}"` |
| `Decimal("1.50")` | `1.50d`, a `Decimal` literal. `+`, `-` and `*` are exact. `/` returns an exact quotient when it ends and otherwise rounds to 34 significant digits, half-even, where Python's default context keeps 28. `Decimal.divide(a, b, 2, RoundingMode.HalfEven)` rounds to a scale you name. See [Money](/money/). |
| `import json` | `import std::json` |
| `def test_x():` with pytest | `test "name" { assert .. }` beside the code |
| `@app.get("/users/<int:id>")` in Flask | `Route.to_with("GET /users/{id}", show)` with `fn show(Request, user: .{id: Int}) -> Response`. The handler gets `user.id` already parsed, `/users/ann` gets a 404, and the pattern is checked when the program is. See [HTTP](/http/#handlers-and-routes) and [Frontends](/frontends/). |

Statements end at a newline, as in Python.

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
