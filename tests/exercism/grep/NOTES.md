# Grep

Subject: Strings & Text

Search files for lines that contain a string, with grep's `-n`, `-l`, `-i`, `-v` and `-x` flags.

Adapted from Exercism problem-specifications (MIT), exercises/grep.

## Choices

- The files are read with std/fs's `fs.read_text` and split with `String.lines`. The tests never touch the disk: `grep_in_sandbox` runs each search inside `fs_testing.run` over a `Sandbox` holding the three canonical files, so std/fs reads them from an in-memory file system rooted at `/work`, and a relative name such as `iliad.txt` resolves there.
- The flags are an `Options` struct of five `Bool` fields that default to `False`, so a case writes only what it sets: `#{line_numbers: True, ignore_case: True}`. The order the canonical data lists flags in never matters, which a struct says and a list of flag strings would not.
- `grep` answers `Result<List<String>, io.Error>`. A file that cannot be read is the error `fs.read_text` gave, passed up with `try`; one file's error stops the search, through `Result.collect`. The canonical data has no such case; the test file adds one, marked not canonical, for a missing file.
- `matches?` lowers both sides for `-i`, compares the whole line or looks for the pattern in it for `-x`, and turns the answer over for `-v` with `found != options.invert`.
- `-l` answers the file's name once if any line matched and takes precedence over `-n`, as two canonical cases ask. With more than one file each line starts with its file's name.
- Each canonical group is a test with one `subtests` table named by the description column.

## Friction

- [feature] `Iter.flatten` named as a function value where a function of a `List<List<String>>` is expected does not check: `Result.map(found, Iter.flatten)` is "expected (List<List<String>>) -> U, got (Iter<Iter<U>>) -> List<U>". A function value's parameter takes a `List` where its declared type is an `Iter`, but not a `List` nested inside one, so `Iter.flatten`'s `Iter<Iter<U>>` refuses the inner lists. The error's help names the capture `&Iter.flatten(&1)`, whose parameter takes the expected type, as the lambda `fn lists -> Iter.flatten(lists)` does, and the solution writes it. Haskell's `concat` and Elixir's `&List.flatten/1` pass as values.
