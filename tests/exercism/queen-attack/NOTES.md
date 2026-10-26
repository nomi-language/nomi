# Queen Attack

Subject: Games & Simulation

Place two queens on a chessboard and say whether they can attack each other.

Adapted from Exercism problem-specifications (MIT), exercises/queen-attack.

## Choices

- `Queen` is a `pub opaque struct`, so `Queen.new` is the only way to build one and an off-board queen cannot exist.
- `Queen.new` validates with a subject-less `case`, one arm per canonical error, and answers `Result<Queen, Error>`.
- `can_attack?` compares row and column distances, each `Int.abs` of a difference: equal rows, equal columns or equal distances.
- The valid-position case is its own test (`assert Queen.new(2, 2)`), since an opaque `Queen` cannot be written as an expected value outside its file.
- The attack table builds each queen with `assert Ok(white) = Queen.new(...)`; `try` cannot be used in a `subtests` body, and the error saying so suggests this form.

## Friction

None.
