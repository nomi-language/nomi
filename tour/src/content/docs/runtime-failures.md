---
title: What Can Fail at Run Time
description: Every way a Nomi program can stop while it runs, and the failures the compiler rules out before it runs.
---

Nomi aims for a program to stop only where its source says it may. The
compiler rules out the common failures before the program runs, and what is
left is written in the code or comes from a limit. This page lists every way
a run stops today.

## Where your code says so

A program stops where it asks to:

- A `!` on an `Err` or a `None` stops at the `!`'s line and quotes the
  expression ([Calls that cannot fail](/pattern-matching/#calls-that-cannot-fail)).
- An [`unreachable`](/functions-and-lambdas/#code-that-cannot-be-reached)
  that the program reaches stops with its reason.
- A [`todo`](/functions-and-lambdas/#unwritten-code) that the program
  reaches stops with its file and line. `nomi build` refuses a program that
  holds one.
- `Task.await` of a task that failed or was cancelled stops with the task's
  fault. `Task.outcome` waits the same way and returns how the task ended as
  a value instead ([When a task fails](/concurrency/#when-a-task-fails)).

```nomi-run error
fn half(n: Int) -> Result<Int, String>
    if n % 2 == 0 then
        Ok(n / 2)
    else
        Err("${n} is odd")
    end
end

fn main()
    dbg half(10)!
    dbg half(3)!
end
```
<!-- expect
dbg line 10: half(10)! = 5
line 11: `half(3)` failed: 3 is odd
-->

A stop is never caught inside the program. It ends the task it happens in.
In `main` it ends the program with status 1, and in a spawned task it is
the task's `Failed` outcome, which a supervisor may restart.

Write `!` where you know a fact the types cannot see, such as a key that came
from the map itself. Each place a program can stop is then written in its
source.

## Limits

Beyond those, a run stops when it:

- makes more than 100,000 nested calls. A tail call keeps no frame and does
  not count ([Recursion and tail calls](/iteration-and-loops/#recursion-and-tail-calls));
- runs out of memory;
- asks [`Int.pow`](/reference/int/#intpow) or `<<<` for one number too big for memory. An `Int`
  has no fixed size; the stop is at 2^34 bits, which is 2 GiB for a single number;
- asks for a `Decimal` whose scale is outside -2147483648 to 2147483647, or
  with more digits than a `Decimal` holds: a `*` whose operands' scales add
  past that range, a `/` whose quotient has too many digits, or a
  `Decimal.divide`, `round`, `round_exact`, `sqrt`, `sqrt_exact`, `pow` or
  `Float.to_decimal` whose scale is past it or whose answer needs one;
- asks [`String.repeat`](/reference/strings/#stringrepeat) for more copies
  than memory holds;
- consumes the whole of an iterator that never ends, such as one passed in
  as a parameter or held in a field. When the whole pipeline is written
  where it is consumed, that is a compile error instead
  ([Iter](/collections/#iter));
- closes a `once` cycle through a function value
  ([`once`](/bindings-and-expressions/#file-level-values-with-once));
- passes the deadline of the context in force while `main` waits on its
  tasks ([A task's own context](/concurrency/#a-tasks-own-context));
- reads, under `nomi run`, `nomi test` or the REPL, an embedded file whose
  contents changed on disk after the program was compiled
  ([Embedded files](/spec/typed-literals-and-keyword-declarations/#embedded-files)).

```nomi
fn depth(n: Int) -> Int
    if n == 0 then 0 else 1 + depth(n - 1)
end

fn main()
    dbg depth(1_000)
    dbg depth(200_000)
end
```

```console
$ nomi run main.nomi
dbg line 6: depth(1_000) = 1000
line 2: stack overflow: more than 100000 nested calls
```

The editors on this site run in your browser with a smaller limit, 1,000
nested calls, so this one is shown and not run.

A program that never finishes runs until something outside stops it.

## What the compiler rules out

These fail to compile, or answer with a value instead of stopping:

- There is no null. A value that may be missing is an
  [`Option`](/pattern-matching/#optiont-and-resultt-e).
- A [`case`](/pattern-matching/) must cover every value its subject can
  take.
- `/` and `%` take a non-zero divisor. A literal `0` is a compile error,
  and any other divisor is a `NonZeroInt`, or an `Int` that a check or a
  range proves is not zero
  ([Numbers](/scalars-and-strings/#numbers-booleans-operators)).
- An index or a lookup that can miss returns an `Option`: `Vector.get`,
  `Iter.get`, `Map.get` ([Collections](/collections/)).
- A slice clamps its bounds to the collection, so `List.slice`,
  `Vector.slice`, `String.slice` and `Bytes.slice` always answer.
- `Int` arithmetic does not overflow. An `Int` holds a whole number of any
  size.
- A value of a [subtype](/structs-enums-distinct/#subtypes) satisfies
  its rule. The compiler checks a literal, and a value known only at run
  time goes through a check such as `try_from`, which returns an `Option`.
- A field read off an enum needs that field in every variant
  ([Enums](/spec/types/#enums-algebraic-data-types)).
- `Sender.send` on a closed channel returns an `Err`
  ([Channels](/concurrency/#channels)).
- Nothing mutates. Bindings, structs and collections never change, so tasks
  share no mutable state.

A check narrows a binding. After `if d == 0 then return ...`, `d` is a
`NonZeroInt`. The compiler also follows ranges, so each `d` in `1..=n` is one
too, and `Int.modulo(n, 26)` and `Int.clamp(n, 0..=9)` are `NonNegativeInt`s.

```nomi-run
fn average(total: Int, count: Int) -> Option<Int>
    if count == 0 then return None
    Some(total / count)
end

fn divisors(n: Int) -> List<Int>
    1..=n
    |> Iter.filter(fn d -> n % d == 0)
    |> Iter.to_list()
end

fn main()
    dbg average(17, 4)
    dbg average(17, 0)
    dbg divisors(12)
end
```
<!-- expect
dbg line 13: average(17, 4) = Some(4)
dbg line 14: average(17, 0) = None
dbg line 15: divisors(12) = [1, 2, 3, 4, 6, 12]
-->

Without the check, `average` does not compile:

```nomi-run error
fn average(total: Int, count: Int) -> Int
    total / count
end

fn main()
    dbg average(17, 4)
end
```
<!-- expect
main.nomi:2:13: `/` divides by a NonZeroInt; `count` is an Int
main.nomi:2:13: help: check it first (`if count == 0 then ...`) or take a NonZeroInt
-->

The compiler proves a divisor is not zero from the checks and ranges already
in the code, with no solver and nothing to annotate.

## How that compares

Other languages draw these lines in other places:

| | Nomi | Elm | Gleam | Rust | Go |
| --- | --- | --- | --- | --- | --- |
| Integer division by zero | Compile error | Returns `0` | Returns `0` | Panic | Panic |
| Index past the end | `Option` | `Maybe` | No indexing | Panic | Panic |
| Integer overflow | None | Target decides | None on Erlang | Debug panic, release wraps | Wraps |
| A failed task | Trap when awaited | Typed error | Linked processes crash | `Err` from `join` | Panic ends the program |

- **Nomi** has no `xs[i]`: `Vector.get` and `Iter.get` return an `Option`,
  and an `Int` has no size limit. A task that returns an `Err` has not failed; that is its
  result. A task fails when its body traps. Every task is waited on, and
  how you wait decides: `Task.await` traps with the same fault, and
  `Task.outcome` returns the failure as a value.
- **Elm**'s `//` returns `0`, but `modBy 0 n` crashes. Its integers are
  exact within 32 bits; past that the compilation target decides.
- **Gleam**'s `/` returns `0`, and the separate function `int.divide`
  returns an `Error` instead. On JavaScript its `Int` is a
  64-bit float. A process affects no other unless they are linked:
  `process.spawn` links the new process to its parent, and
  `process.spawn_unlinked` does not.
- **Rust**'s `v[i]` panics and `get` returns an `Option`.
- **Go** refuses a constant zero divisor when compiling.
- **Calling the host language:** a Nomi program reaches code outside it only
  through the standard library, WebAssembly that std::wasm runs, and the
  host functions of a program that embeds it. Elm has no direct calls, only flags and ports. A Gleam external's type is trusted, and a wrong one
  can crash at run time.
