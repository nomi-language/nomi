# Kindergarten Garden

Subject: Grids & Matrices

Read a diagram of two rows of plant cups and say which four plants each child of the class looks after.

Adapted from Exercism problem-specifications (MIT), exercises/kindergarten-garden.

## Choices

- Plants and students are enums. The class is fixed at twelve named children, so asking for a child who is not in it cannot be written, and `Garden.plants` needs no error.
- The diagram is parsed once into an opaque `Garden` by `Garden.parse`, which returns `None` for a letter that names no plant: `Maybe.collect()?` over the rows, then `Some(#Garden{rows})`. A diagram is data, so a bad one is an answer rather than a trap. The canonical data has no bad diagram; the test file adds one, marked as not canonical.
- A student's position is their index in `derive Variants`' list (`Iter.find_index`), so the alphabetical order lives only in the enum's declaration. Every student is in that list, so the search cannot miss: its `None` is `unreachable "every student is listed"` (style §28), a trap if the enum and the list ever disagree, rather than a default that would answer a wrong position.
- `Garden.plants` takes two cups from each row with `Iter.drop` and `Iter.take` under one `Iter.flat_map`.
- "partial garden" is a `group` of its two tests, the nested canonical group "multiple students for the same garden with three students" among them, since groups do not nest; "full garden" is a plain test. Where every row shares a diagram, the test parses it once with `?` before the table; where rows bring their own, the row parses it with `assert Some(garden) = ...`, since `?` cannot be used in a `subtests` body.
- Each table's first row qualifies its variants (`Student.Alice`, `[Plant.Radishes, ...]`), and the later rows write them bare.

## Friction

None.
