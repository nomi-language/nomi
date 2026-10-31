# Grep

Subject: Strings & Text

Search files for lines that contain a string, with grep's `-n`, `-l`, `-i`, `-v` and `-x` flags.

Adapted from Exercism problem-specifications (MIT), exercises/grep.

## Choices

- The files are read with std/fs's `fs.read_text` and split with `String.lines`. The tests never touch the disk: `grep_in_sandbox` runs each search inside `fs_testing.run` over a `Sandbox` holding the three canonical files, so std/fs reads them from an in-memory file system rooted at `/work`, and a relative name such as `iliad.txt` resolves there.
- The flags are a `Set<Flag>` of a `Flag` enum, so a case writes the flags it sets, as grep's command line does: `%[.LineNumbers, .IgnoreCase]`. The order the canonical data lists flags in never matters, which a set says and a list of flag strings would not. An `Options` struct of five `Bool` fields defaulting to `False` was the other form; the set is shorter in the tables, and the first row that names a flag writes `Flag.LineNumbers`, so neither table needs a type annotation. The solution asks `Set.contains(flags, .IgnoreCase)` where the struct read `options.ignore_case`.
- `grep` answers `Result<List<String>, io.Error>`. A file that cannot be read is the error `fs.read_text` gave, passed up with `?`; one file's error stops the search, through `Result.collect()?`. The canonical data has no such case; the test file adds one, marked not canonical, for a missing file.
- `matches` folds the case of both sides with `String.fold_case` for `-i`, compares the whole line or looks for the pattern in it for `-x`, and turns the answer over for `-v` with `found != Set.contains(flags, .Invert)`.
- `-l` answers the file's name once if any line matched and takes precedence over `-n`, as two canonical cases ask. With more than one file each line starts with its file's name.
- Each canonical group is a test with one `subtests` table named by the description column.
