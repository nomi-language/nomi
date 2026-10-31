# Intergalactic Transmission

Subject: Encoding & Ciphers

Send a message seven bits to a byte, each byte ending in a parity bit that makes its count of ones even, and decode a transmission back, rejecting a byte whose parity is wrong.

Adapted from Exercism problem-specifications (MIT), exercises/intergalactic-transmission.

## Choices

- Both directions work on a stream of bits rather than shifting a carry between bytes: `bits` answers a byte's eight bits most significant first, and `from_bits` reads a list of bits back as a number, `Int.from_digits(bits, 2)!`, whose `None` would mean a digit other than 0 or 1. `transmit` is `Iter.flat_map(bits)`, `Iter.chunks(7)` and one byte per chunk; `receive` checks and strips each byte, `Iter.flatten`s the data bits and reads them back eight at a time.
- Bit `i` of a byte is `byte >>> (7 - i) &&& 1`, and a byte with room for its parity bit is `data <<< 1 + parity`: the shifts and `&&&` bind like `*`, as in Go, so only the shift count needs parentheses.
- The last chunk of a message may be short. `transmit` shifts it left by what it lacks, which pads it with zeros, and its parity is the sum of its bits modulo 2, so padding needs no special case. `receive` drops a short last chunk, the padding the sender added.
- A byte with odd parity is `Err("wrong parity")`, the canonical message, as grains and variable-length-quantity do with their one error. `Result.collect` stops at the first bad byte.
- Bytes are `List<Int>` written in hex, as in variable-length-quantity, for the reason its Friction gives.
- The transmit and decode groups are one `subtests` table each, named by the description column.

## Friction

None.
