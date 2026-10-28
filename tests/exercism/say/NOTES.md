# Say

Subject: Numbers & Math

Spell out a number from zero to 999,999,999,999 in English words.

Adapted from Exercism problem-specifications (MIT), exercises/say.

## Choices

- `say` returns `Result<String, Error>`, with `Error.OutOfRange` for a number below zero or above 999,999,999,999. The range check is the one place a number can fail, so the rest of the code works on numbers already in range.
- `words` is one recursive rule for every scale: find the largest of billion, million, thousand and hundred that the number reaches, say how many of it (`words(n / size)`), then say the rest when there is one. "Hundred" is a scale like the others, so "nine hundred eighty-seven billion" falls out of the same rule twice.
- Below a hundred, the words come from two `Vector` tables, `small` for 0 to 19 and `tens` by tens digit, joined with a hyphen when the ones digit is not zero. The tables are `once` bindings, read with `Vector.at` at an index the range check and the arithmetic keep in bounds (style §28).
- The scales are a list of `#(size, name)` tuples, largest first, so `Iter.find` with `n >= size` picks the largest one the number reaches.
- Large literals are written with digit separators, `987_654_321_123`, in the solution and the tests.
- The 19 canonical cases are one `subtests` table named by the description column; the out-of-range cases are rows whose expected value is an `Err`.

## Friction

None.

## Indexing

Three indexed reads.

| Read | Where | Result | Real use for a miss? |
|---|---|---|---|
| `Vector.at(small, n)` | `below_hundred`, `n < 20` | the word | No: `n` is from 0 to 19 |
| `Vector.at(tens, n / 10)` | `below_hundred`, twice | the word | No: `n` is from 20 to 99, so `n / 10` is from 2 to 9 |
| `Vector.at(small, n % 10)` | `below_hundred` | the word | No: a remainder by 10 is from 0 to 9 |
