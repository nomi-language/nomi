# Anagram

Pick out the candidates that are anagrams of a word, ignoring case, where a word is not an anagram of itself.

Adapted from Exercism problem-specifications (MIT), exercises/anagram.

## Choices

- The anagram key is `Iter.sort(word)`: a `String` iterates by grapheme, so sorting it gives a `List<String>` to compare, with no codepoint conversion.
- Case folding is `String.to_lower` on both sides, once for the subject and once per candidate.
- All 18 current cases are one `subtests` table, since every row is the same call and comparison. The four cases that newer ones reimplement are left out.
- Each row carries its canonical description as its first column and is named by it (`subtests cases, #(description, subject, candidates, expected), description`), so a failing row reports under its description.

## Friction

- [feature] A `subtests` row has no name of its own. To report a failing row by its canonical description, the description has to ride along as a discarded tuple column (`#(_description, subject, candidates, expected)`). Go's `t.Run(name, ...)` and Rust's `rstest` `#[case::name]` give a row a name. Fixed: `subtests rows, pattern, name { ... }` names a row, and a failed row's report shows the name in place of the row.
