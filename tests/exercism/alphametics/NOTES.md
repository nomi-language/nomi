# Alphametics

Subject: Collections & Algorithms

Solve a letter-sum puzzle such as `SEND + MORE == MONEY`: give each letter a different digit so the sum is true, with no word of more than one letter starting with zero.

Adapted from Exercism problem-specifications (MIT), exercises/alphametics.

## Choices

- `solve` answers `Maybe<Map<String, Int>>`, each letter's digit or `None`, as the canonical data maps letters to digits or gives `null`.
- The puzzle becomes one equation. Each letter's `weight` is the sum of its place values, positive in the addends and negative in the result (`S` in `SEND + MORE == MONEY` weighs 1000, `M` 1000 - 10000), so a solution is digits whose weighted sum is zero. The 199 addends of the largest case fold into ten weights before the search starts, and the search never reads the words again.
- The search is a depth-first backtrack over the letters, heaviest first. After each digit, `reachable?` checks that the letters left could still bring the total back to zero, at most nine times each positive weight up and nine times each negative weight down, and the search backs out when they cannot. Taking the heavy letters first makes that bound cut early: the 199-addend case visits about 2,800 assignments and runs in about 30 ms on the VM. An earlier version took the letters by their lowest column and checked the total modulo that column's place value, as a column-by-column solver does; in the 199-addend puzzle all ten letters end some word, so every letter sits in the units column, it could not prune until the last letter, and it took 4.7 seconds.
- A letter that leads a word of more than one letter is marked `leading` and never gets 0.
- `places` lists each letter of a word with its place value, from `Iter.reverse` and `Iter.with_index`, and `Iter.group_by` gathers a letter's uses into one `Letter`.
- The ten canonical cases are one `subtests` table named by the description column.

## Friction

- [stdlib] There is no `Iter.find_map` and no `Maybe.flatten`. `assign` maps each digit to the search below it, a `Maybe`, and wants the first `Some`, which is `Iter.find(Maybe.some?)` and then `Maybe.flat_map(fn found -> found)`. Rust has `Iterator::find_map`, Elixir `Enum.find_value` and Gleam `list.find_map`.
