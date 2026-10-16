# Isogram

Decide whether a word or phrase repeats no letter, ignoring case; spaces and
hyphens may repeat.

Adapted from Exercism problem-specifications (MIT), exercises/isogram.

## Choices

- The letters come from `String.find_all(Regex`\pL`)` on the lower-cased
  phrase, which keeps every Unicode letter and drops everything else. The
  alternative was filtering out `" "` and `"-"` by hand, which matches the
  problem text but lets any other punctuation count as a letter.
- Repeats are detected by comparing the letter count with the size of the
  letter set, rather than by folding through a set and stopping at the first
  repeat. The inputs are words, so stopping early buys nothing.

## Friction

- [stdlib] There is no letter test on a grapheme or a `Codepoint`, so asking
  "is this a letter" goes through a regex. Rust has `char::is_alphabetic`, Go
  `unicode.IsLetter`; Elixir and Gleam have no letter predicate either and
  reach for a regex. The regex works and is short, but a reader has to know
  `\pL`.
