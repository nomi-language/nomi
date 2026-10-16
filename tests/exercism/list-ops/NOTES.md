# List Ops

Implement append, concatenate, filter, length, map, both folds and reverse without the built-in list functions.

Adapted from Exercism problem-specifications (MIT), exercises/list-ops.

## Choices

- `foldl` is the one primitive, a tail-recursive `case` over `[]` and `[head, ..tail]`. `foldr` is `foldl` over the reversed list, and `reverse`, `length`, `append`, `concat`, `filter` and `map` are folds.
- `append` is `foldr(first, second, |rest, item| [item, ..rest])`: the first list folded onto the second.
- Both folds call `f(accumulator, item)`, the argument order the canonical data's reimplemented cases fix.
- The direction-dependent fold cases use `Float` lists, since `el / acc` on the canonical numbers is fractional and Nomi has one `/` per type.
- The functions are file-level and called `list_ops.map(...)`; the names do not clash with `Iter.map` because file qualification keeps them apart.
- The file comment is `//#`: a plain `//` comment above the first declaration is joined to it by `nomi fmt`.
- `concat([])` needs a typed empty list (`List<List<Int>>`), which the checker asks for by name; `append([], [])` and the other empty cases need nothing.
- The "list of nested lists" input is bound in four parts, because the literal is three lists deep and `nomi fmt` would lay it out one number per line.

## Friction

- [verbose] The depth-3 formatter rule (see robot-simulator) turns `[[[1], [2]], [[3]]]` into 16 lines.
- [idiom] A plain `//` file comment followed by a blank line and a `///` doc comment loses the blank line under `nomi fmt` and reads as part of the doc. `//#` keeps it, but `style.md` does not say that `//#` is the file-comment form for files outside `std/`.
