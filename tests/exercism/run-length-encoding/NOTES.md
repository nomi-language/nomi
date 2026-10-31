# Run-Length Encoding

Subject: Encoding & Ciphers

Encode runs of repeated characters as a count and the character, and decode them back.

Adapted from Exercism problem-specifications (MIT), exercises/run-length-encoding.

## Choices

- `encode` is `Iter.chunk_by` on the string's graphemes, then each run written out by `encode_run`, then `String.join`.
- `decode` reads the runs with ``String.captures_all(Regex`(\d*)(\D)`)``: an optional count, then one character. The literal's type gives each match as a `#(String, String)`, since both groups take part in every match, so a destructuring lambda repeats the character `count` times, or once when the count group matched empty. The other way is an `Iter.reduce` over graphemes that threads `#(count, text)` and builds the count digit by digit; the pattern is clearer because it states the encoded format in one line, where the reducer spreads it over a tuple accumulator and an `if count == 0` fallback. The cost is that `\D` matches one code point, not one grapheme, so a run of a character written with a combining mark would decode wrongly; the canonical cases are all single code points.
- Each canonical group is a test named by the group's description, holding one `subtests` table; the round-trip case is a plain test.
- A run from `Iter.chunk_by` is never empty, so `encode_run` takes its letter with `List.head(run)!`, which traps rather than inventing a letter if that ever failed.
