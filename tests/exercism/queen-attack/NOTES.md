# Queen Attack

Place two queens on a chessboard and say whether they can attack each other.

Adapted from Exercism problem-specifications (MIT), exercises/queen-attack.

## Choices

- `Queen` is a `pub opaque struct`, so `Queen.new` is the only way to build one and an off-board queen cannot exist.
- `Queen.new` validates with a subject-less `case`, one arm per canonical error, and answers `Result<Queen, Error>`.
- `can_attack?` compares row and column distances: equal rows, equal columns or equal distances.
- The valid-position case is its own test (`assert Queen.new(2, 2)`), since an opaque `Queen` cannot be written as an expected value outside its file.
- The attack table builds each queen with `assert Ok(white) = Queen.new(...)`; `try` is not allowed in a `subtests` row, and the error saying so names this form.

## Friction

- [stdlib] No `Int.abs`, so the file has a `distance` helper. Rust has `i64::abs` and `abs_diff`, Elixir `abs/1`, Gleam `int.absolute_value`.
- [verbose] `nomi fmt` never breaks a `subtests` header (`emitSubtests` in `internal/format/emit.go`), so a row pattern that names every column runs past 100 columns. `subtests cases, (_description, (white_row, white_column), (black_row, black_column), expected) {` was 104 columns, and the solution shortened the names to `white_col` and `black_col`. Fixed: a header too long for its line breaks its tuple pattern one element per line, and the table uses the full names again.
