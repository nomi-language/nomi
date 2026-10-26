# List Ops

Subject: Collections & Algorithms

Implement append, concatenate, filter, length, map, both folds and reverse without the built-in list functions.

Adapted from Exercism problem-specifications (MIT), exercises/list-ops.

## Choices

- `foldl` is the one primitive, a tail-recursive `case` over `[]` and `[head, ..tail]`. `foldr` is `foldl` over the reversed list, and `reverse`, `length`, `append`, `concat`, `filter` and `map` are folds.
- `append` is `foldr(first, second, fn rest, item -> [item, ..rest])`: the first list folded onto the second.
- Both folds call `f(accumulator, item)`, the argument order the canonical data's reimplemented cases fix.
- The direction-dependent fold cases use `Float` lists, since `el / acc` on the canonical numbers is fractional and Nomi has one `/` per type.
- The functions are file-level and called `list_ops.map(...)`; the names do not clash with `Iter.map` because file qualification keeps them apart.
- The file comment is `//#`, the file-comment form style §7 gives for every file: a plain `//` comment above the first declaration is joined to it by `nomi fmt`.
- `concat([])` needs a typed empty list (`List<List<Int>>`), which the checker asks for by name; `append([], [])` and the other empty cases need nothing.
- The "list of nested lists" input is one literal. It is three lists deep, so `nomi fmt` writes it one inner list per line, each on one line.

## Friction

None.
