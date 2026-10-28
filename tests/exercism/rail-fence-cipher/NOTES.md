# Rail Fence Cipher

Subject: Encoding & Ciphers

Encode a message by writing it in a zig-zag across a number of rails and reading the rails off in turn, and decode it back.

Adapted from Exercism problem-specifications (MIT), exercises/rail-fence-cipher.

## Choices

- No fence is built. `zigzag(rails)` is the endless sequence of rails the letters fall on, `0, 1, 2, 1, 0, 1, ...`: the range down, the range back up from `Iter.reverse`, and `Iter.cycle`. `Iter.zip` with the message stops it at the message's end.
- `encode` pairs each letter with its rail and sorts by rail. `Iter.sort_by` is stable, so letters on one rail keep their order, which is the reading the cipher asks for.
- `decode` runs the same sort on the positions `0..n` instead of the letters. That answers which position of the message each letter of the ciphertext came from; pairing the positions with the ciphertext and sorting by position puts the letters back. Both directions share the one zig-zag, so neither has a rail-length calculation of its own.
- The tuples are read with `.0` and `.1` field accessors (style §21).
- The encode and decode cases are one `subtests` table each. One rail, which leaves the message as it is, is a test of its own, marked as not canonical.

## Friction

None.
