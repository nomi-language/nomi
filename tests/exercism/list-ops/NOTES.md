# List Ops

Implement append, concatenate, filter, length, map, both folds and reverse without the built-in list functions.

Adapted from Exercism problem-specifications (MIT), exercises/list-ops.

## Choices

- `foldl` is the one primitive, a tail-recursive `case` over `[]` and `[head, ..tail]`. `foldr` is `foldl` over the reversed list, and `reverse`, `length`, `concat`, `filter` and `map` are folds.
- `append` is direct recursion (`[head, ..append(tail, second)]`). The fold version, `foldr(first, second, |rest, item| [item, ..rest])`, is declined by the IR builder (the bug below).
- Both folds call `f(accumulator, item)`, the argument order the canonical data's reimplemented cases fix.
- The direction-dependent fold cases use `Float` lists, since `el / acc` on the canonical numbers is fractional and Nomi has one `/` per type.
- The functions are file-level and called `list_ops.map(...)`; the names do not clash with `Iter.map` because file qualification keeps them apart.
- The file comment is `//#`: a plain `//` comment above the first declaration is joined to it by `nomi fmt`.
- `concat([])` needs a typed empty list (`List<List<Int>>`), which the checker asks for by name; `append([], [])` and the other empty cases need nothing.
- The "list of nested lists" input is bound in four parts, because the literal is three lists deep and `nomi fmt` would lay it out one number per line.

## Friction

- [bug] A generic call is declined when a caller's type parameter appears inside the type argument for a callback's accumulator. `nomi run` of this program reports "this call to `call` is not supported yet, so `fn b` cannot run" (lowering reason "a direct callee that is not lowerable: call"); `e`, where the accumulator is `Int`, runs:

  ```nomi
  import std/io

  fn call<T, U>(item: T, initial: U, f: (U, T) -> U): U {
      f(initial, item)
  }

  fn b<E>(item: E, list: List<E>): List<E> {
      call(item, list, |rest, _item| rest)
  }

  fn e<E>(item: E, n: Int): Int {
      call(item, n, |rest, _item| rest)
  }

  fn main() {
      io.inspect(e(1, 3))
      io.inspect(b(1, [2]))
  }
  ```

  `Maybe<E>` in place of `List<E>` is declined the same way. The same call with `[]` instead of the parameter `list` runs, as does the non-generic `fn b(item: Int, list: List<Int>)`.
- [verbose] The depth-3 formatter rule (see robot-simulator) turns `[[[1], [2]], [[3]]]` into 16 lines.
- [idiom] A plain `//` file comment followed by a blank line and a `///` doc comment loses the blank line under `nomi fmt` and reads as part of the doc. `//#` keeps it, but `style.md` does not say that `//#` is the file-comment form for files outside `std/`.
