# Book Store

Subject: Collections & Algorithms

Price a basket of books from a five-book series, where each group of different titles earns a discount that grows with the group, choosing the groups that cost least.

Adapted from Exercism problem-specifications (MIT), exercises/book-store.

## Choices

- Prices are whole cents in `Int`, as the canonical data gives them. Every group's price is a whole number of cents (`5 × 800 × 75 / 100`), so the integer division is exact.
- The groups come from the copies of each title, most copies first: `[6, 6, 6, 2, 2]` makes two groups of five (as many as the fifth title has copies) and four groups of three (the extra copies of the first three titles). `group_sizes` reads that off the differences between neighbouring counts with `Iter.zip` and `Iter.with_index`.
- Taking the largest groups is cheapest except for one exchange: a group of five and one of three cost 3000 + 2160 = 5160 cents, and two groups of four 5120. Each other exchange of one book between two groups costs more (a five and a two against a four and a three, 4520 against 4720, and so on down the table), so `total` prices the largest groups and takes off 40 cents for each five-and-three pair. A search over every grouping is also right, but it branches at every group it takes and needs a memo table, threaded through the recursion, to stay fast on the 22-book basket.
- `group_price` has an arm per discounted size and `_ -> 0` for the rest, a single book.
- The 18 canonical cases are one `subtests` table named by the description column.

## Friction

None.
