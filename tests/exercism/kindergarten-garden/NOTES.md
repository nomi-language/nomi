# Kindergarten Garden

Subject: Grids & Matrices

Read a diagram of two rows of plant cups and say which four plants each child of the class looks after.

Adapted from Exercism problem-specifications (MIT), exercises/kindergarten-garden.

## Choices

- Plants and students are enums. The class is fixed at twelve named children, so asking for a child who is not in it cannot be written, and `Garden.plants` needs no error.
- The diagram is parsed once into an opaque `Garden` by `Garden.parse`, which returns `None` for a letter that names no plant. A diagram is data, so a bad one is an answer rather than a trap. The canonical data has no bad diagram; the test file adds one, marked as not canonical.
- A student's position is the number of students before them in `derive Variants`' list (`Iter.take_while` and `Iter.count`), so the alphabetical order lives only in the enum's declaration.
- `Garden.plants` takes two cups from each row with `Iter.drop` and `Iter.take` under one `Iter.flat_map`.
- The canonical groups are `group` blocks. The nested group, "multiple students for the same garden with three students", is a test of its own inside "partial garden", since groups do not nest. Where every row shares a diagram, the test parses it once with `try` before the table; where rows bring their own, the row parses it with `assert Some(garden) = ...`, since `try` cannot be used in a `subtests` body.
- Each table's first row qualifies its variants (`Student.Alice`, `[Plant.Radishes, ...]`), and the later rows write them bare.

## Friction

- [stdlib] There is no function for the position of an element, so `position` is `Student.variants() |> Iter.take_while(&(&1 != student)) |> Iter.count()`. Rust has `Iterator::position`, Elixir `Enum.find_index/2` and Go `slices.Index`.
