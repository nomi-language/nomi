# Variable Length Quantity

Subject: Encoding & Ciphers

Encode numbers as variable-length quantities, seven bits to a byte with the high bit marking that more follow, and decode them back.

Adapted from Exercism problem-specifications (MIT), exercises/variable-length-quantity.

## Choices

- A number's bytes are its base-128 digits from `Int.to_digits(n, 128)`, with the high bit set on every digit but the last, `byte ||| 0x80`. No loop shifts seven bits at a time.
- Decoding reads seven bits at a time, `value <<< 7 ||| Byte.to_int(byte &&& 0x7F)`, and a byte's continuation bit is `byte &&& 0x80`. The shifts and `&&&` bind like `*` and `|||` like `+`, as in Go, so neither needs parentheses.
- `decode` is one `Iter.map_reduce` whose state is the number read so far, `Some` while its bytes continue and `None` between numbers, and whose output is `Some(value)` at each number's last byte. `Maybe.values` keeps those, and a state still `Some` at the end is the incomplete sequence.
- The one error is `Err("incomplete sequence")`, the canonical message, as grains does: an `Error` enum would add a type for one variant.
- Bytes are `List<Byte>`, as the canonical `List<u8>` is, written in hex in the tests (`0x7F`), as the canonical data's comments ask of a track's tests. An Int literal where a `Byte` is expected is one, so the tables' types are written once on `cases` and every byte is a plain literal. Encoding turns each digit, always below 128, into a Byte with `Int.to_byte(digit)!`; decoding reads a byte's bits back with `Byte.to_int`.
- The encode and decode groups are one `subtests` table each, named by the description column.
