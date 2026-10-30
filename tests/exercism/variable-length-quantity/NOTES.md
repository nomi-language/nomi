# Variable Length Quantity

Subject: Encoding & Ciphers

Encode numbers as variable-length quantities, seven bits to a byte with the high bit marking that more follow, and decode them back.

Adapted from Exercism problem-specifications (MIT), exercises/variable-length-quantity.

## Choices

- A number's bytes are its base-128 digits from `Int.to_digits(n, 128)`, with the high bit set on every digit but the last. No loop shifts seven bits at a time.
- `decode` is one `Iter.map_reduce` whose state is the number read so far, `Some` while its bytes continue and `None` between numbers, and whose output is `Some(value)` at each number's last byte. `Maybe.values` keeps those, and a state still `Some` at the end is the incomplete sequence.
- The one error is `Err("incomplete sequence")`, the canonical message, as grains does: an `Error` enum would add a type for one variant.
- Bytes are `List<Int>`, written in hex in the tests (`0x7F`), as the canonical data's comments ask of a track's tests. See Friction for why not `Bytes`.
- The encode and decode groups are one `subtests` table each, named by the description column.

## Friction

- [feature] `Byte` has no literal and is not a number (`Byte.to_int` and `Int.to_byte`, which answers a `Maybe`), so a byte sequence in a test would be `Int.to_byte(0x81)?` per byte. The bytes are `List<Int>` instead, which also holds 300 or -1. Rust writes `[0x81u8, 0x00]`, Go `[]byte{0x81, 0x00}`, and Python `b"\x81\x00"`.
- [verbose] There are no bitwise operators, so a mask is `Int.bitwise_and(byte, 0x7F)` and a flag `Int.bitwise_or(digit, 0x80)`, where Rust, Go and Python write `byte & 0x7f` and `digit | 0x80`.
