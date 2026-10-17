# Isogram

Decide whether a word or phrase repeats no letter, ignoring case; spaces and
hyphens may repeat.

Adapted from Exercism problem-specifications (MIT), exercises/isogram.

## Choices

- The letters are the lower-cased phrase's codepoints filtered by
  `Codepoint.letter?`, which keeps every Unicode letter and drops everything
  else. `String.find_all(Regex`\pL`)` keeps the same codepoints in one stage
  instead of three, but a reader has to know `\pL`, and the filter says
  "keep the letters" in words. Filtering out `" "` and `"-"` by hand matches
  the problem text but lets any other punctuation count as a letter.
- Repeats are detected by comparing the letter count with the size of the
  letter set, rather than by folding through a set and stopping at the first
  repeat. The inputs are words, so stopping early buys nothing.

## Friction

None.
