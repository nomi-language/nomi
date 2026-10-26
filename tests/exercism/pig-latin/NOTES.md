# Pig Latin

Subject: Strings & Text

Translate English words to Pig Latin by moving each word's leading consonants to its end and adding "ay".

Adapted from Exercism problem-specifications (MIT), exercises/pig-latin.

## Choices

- The four rules become one count, `onset_length`: how many letters at the start of the word move. The word is then two `String.slice` calls and `"ay"`.
- The count walks the word's letters with list patterns. `["q", "u", .._]` is rule 3, `["x", "r", .._] or ["y", "t", .._]` is the `xr`/`yt` part of rule 1, and a leading `["y", ..rest]` makes `y` a consonant only as the first letter, which is rule 4 without a flag parameter.
- The vowels are a `once` Set, looked up with `Set.contains?` in a guard; `y` is among them because after the first letter it acts as one.
- The canonical groups are `group` blocks holding one `subtests` table each; the one-case "phrases are translated" group is a plain test.

## Friction

- [stdlib] No function gives the rest of a string from an index, so the tail is `String.slice(word, onset, String.length(word))`, or `word |> Iter.drop(onset) |> String.join()`. Rust writes `&word[onset..]`, Gleam `string.drop_start(word, onset)`, and Elixir `String.slice(word, onset..-1//1)`.
