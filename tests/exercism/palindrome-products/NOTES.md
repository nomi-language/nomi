# Palindrome Products

Subject: Numbers & Math

Find the smallest and the largest palindrome that is a product of two numbers in a range, with every pair of factors that makes it.

Adapted from Exercism problem-specifications (MIT), exercises/palindrome-products.

## Choices

- The search runs over palindromes, not over products. `halves` and `mirror` build every palindrome of a digit count from its first half (`123` makes `12321` and `123321`), in order, and the first one in the range of products that has a pair of factors in range is the answer. For the four-digit largest case the search tries 98 palindromes before `99000099`, where a walk down the products would test close to a million numbers for being palindromes.
- `factors` tries only the smaller factor, from `value / max` (rounded up) to the square root, so the larger one, `value / a`, is in range by construction. That is at most a few dozen divisions for the four-digit cases.
- `smallest` and `largest` are one `search` from either end, picked by a `Direction`. `in_order` gives a range's values from that end: the range itself, or `Iter.reverse` of it, a `List`. The two branches are different Iters, which join as one because `in_order` declares its result as `Iter<Int>`. The search drops the palindromes outside `min²` to `max²` from the near end and stops at the far one, so the same two steps serve both directions. The pipeline is lazy, so the search stops at the first palindrome with factors.
- A result is `Result<Maybe<Product>, Error>`: `Err(.MinAboveMax)` for the canonical error, `Ok(None)` for the canonical "empty result" (a `null` value with no factors), and a `Product` struct holding the value and its factor pairs otherwise. A `Product` with an empty factor list cannot be built by the search, so `Maybe` says "none" instead.
- `first_product` takes an `Iter<Int>`, so both directions share it.
- The canonical cases are two `subtests` tables, one per property, named by the description column and annotated so the `Ok` and `Err` rows share a type.

## Friction

- [stdlib] There is no descending range: `5..=1` is empty, and `Range.step_by(-1)` over it is empty too. The descending search reverses with `Iter.reverse`, which builds a list of every half of a digit count (9,000 for the four-digit case) before the lazy search reads the first few. Rust has `(1..=5).rev()`, Python `range(5, 0, -1)` and Elixir `5..1//-1`, all lazy.
