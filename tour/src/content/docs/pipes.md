---
title: Pipes
description: The `|>` operator threads a value through successive calls — once you have it, every Nomi program reads top-to-bottom.
---

`x |> f()` is `f(x)`. The piped value goes into the **first argument** of the
call on its right; any additional arguments come after. Pipes let you describe
a computation as a top-to-bottom flow rather than an inside-out chain of
nested calls — which is how most idiomatic Nomi reads.

## The pipe operator

The simplest pipe is one stage. When the call has more than one argument, the
piped value fills the first slot and the rest are written after:

```nomi-run
fn double(x: Int): Int {
    x * 2
}

fn add(x: Int, y: Int): Int {
    x + y
}

fn main(): Int {
    5 |> double() |> dbg

    5 |> add(3) |> dbg
}

```
<!-- expect
dbg line 10: 5 |> double() = 10
dbg line 12: 5 |> add(3) = 8
-->

Longer chains stack one stage per line — the data flows top-to-bottom and each
stage gets its own row:

```nomi-run
fn main(): List<Int> {
    [1, 2, 3]
    |> Iter.map(|n| n * n)
    |> Iter.to_list()
    |> dbg
}

```
<!-- expect
dbg line 5:
  [1, 2, 3]
  |> Iter.map(|n| n * n)
  |> Iter.to_list()
  = [1, 4, 9]
-->

## Pipes start from data

Pipes read most clearly when the chain *starts* from concrete data and ends at
where it's used. Compare:

```nomi-run
fn main(): Int {
    // inside-out
    dbg Iter.count(Iter.filter([1, 2, 3, 4, 5], |n| n > 2))

    // pipeline — data first, transformations next, output last
    [1, 2, 3, 4, 5]
    |> Iter.filter(|n| n > 2)
    |> Iter.count()
    |> dbg
}

```
<!-- expect
dbg line 3: Iter.count(Iter.filter([1, 2, 3, 4, 5], |n| n > 2)) = 3
dbg line 9:
  [1, 2, 3, 4, 5]
  |> Iter.filter(|n| n > 2)
  |> Iter.count()
  = 3
-->

Both produce `3`; only the second reads top-to-bottom.

## The `_` placeholder

The piped value goes into the first argument by default. To pipe into a
different position, write `_` where the piped value should land:

```nomi-run
fn divide(x: Int, y: Int): Int {
    x / y
}

fn main(): Int {
    10 |> divide(100, _) |> dbg
}

```
<!-- expect
dbg line 6: 10 |> divide(100, _) = 10
-->

That `_` becomes `divide(100, 10)`, which is `100 / 10`.

## Lambda stages

A pipe stage can be a lambda, for a small inline step. Its body ends at the
next `|>`; to pipe inside a lambda, give it a block body: `|xs| { xs |> ... }`.

```nomi-run
fn find_name(id: Int): Maybe<String> {
    case id {
        1 -> Some("Ada")
        _ -> None
    }
}

fn main(): Bool {
    1
    |> find_name()
    |> |name| name == Some("Ada")
    |> dbg
}
```
<!-- expect
dbg line 12:
  1
  |> find_name()
  |> |name| name == Some("Ada")
  = True
-->

## Keyword stages

Expression keywords can also be pipeline stages. Bare `dbg` inspects the
current value; [`try`](/pattern-matching/#the-try-keyword) unwraps
[`Maybe`](/reference/maybe/) and [`Result`](/reference/results/) stages; [assertions](/testing/#assertions) usually sit at the head of test
pipelines.

`try` short-circuits on `None` or `Err`:

```nomi-run
fn main(): Maybe<Int> {
    "  42  "
    |> String.trim()
    |> try String.to_int()
    |> Some()
    |> dbg
}

```
<!-- expect
dbg line 6:
  "  42  "
  |> String.trim()
  |> try String.to_int()
  |> Some()
  = Some(42)
-->

Assertions keep the subject visually complete:

```nomi-run
test "pipeline result is true" {
    assert "  Ada Lovelace  "
        |> String.trim()
        |> String.contains?("Ada")
}

```
<!-- expect
-->

`if` and `case` follow the same idea for branchy expressions:

```nomi-run
fn main(): String {
    "Ada"
    |> if String.contains?("A") {
            "initialed"
        } else {
            "plain"
        }
    |> dbg

    Some("Ada")
    |> case {
            .Some(name) -> "named ${name}"
            .None -> "missing"
        }
    |> dbg
}
```
<!-- expect
dbg line 8:
  "Ada"
  |> if String.contains?("A") {
          "initialed"
      } else {
          "plain"
      }
  = "initialed"
dbg line 15:
  Some("Ada")
  |> case {
          .Some(name) -> "named ${name}"
          .None -> "missing"
      }
  = "named Ada"
-->

The next chapter — [Scalars & Strings](/scalars-and-strings/) — uses pipes
from the first example onward.
