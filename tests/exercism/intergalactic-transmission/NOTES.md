# Intergalactic Transmission

Subject: Encoding & Ciphers

Send a message seven bits to a byte, each byte ending in a parity bit that makes its count of ones even, and decode a transmission back, rejecting a byte whose parity is wrong.

Adapted from Exercism problem-specifications (MIT), exercises/intergalactic-transmission.

## Choices

- Both directions work on a stream of bits rather than shifting a carry between bytes: `bits` answers a byte's eight bits most significant first, and `from_bits` folds a list of bits back into a number. `transmit` is `Iter.flat_map(bits)`, `Iter.chunks(7)` and one byte per chunk; `receive` checks and strips each byte, `Iter.flatten`s the data bits and reads them back eight at a time.
- The last chunk of a message may be short. `transmit` shifts it left by what it lacks, which pads it with zeros, and its parity is the sum of its bits modulo 2, so padding needs no special case. `receive` drops a short last chunk, the padding the sender added.
- A byte with odd parity is `Err("wrong parity")`, the canonical message, as grains and variable-length-quantity do with their one error. `Result.collect` stops at the first bad byte.
- Bytes are `List<Int>` written in hex, as in variable-length-quantity, for the reason its Friction gives.
- The transmit and decode groups are one `subtests` table each, named by the description column.

## Friction

- [verbose] There are no bitwise operators, so reading bit `i` of a byte is `Int.bitwise_and(Int.shift_right(byte, 7 - i), 1)` and making room for the parity bit `Int.shift_left(data, 1)`, where Rust, Go and Python write `byte >> (7 - i) & 1` and `data << 1`.
