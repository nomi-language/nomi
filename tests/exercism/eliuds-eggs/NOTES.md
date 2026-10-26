# Eliud's Eggs

Subject: Numbers & Math

Count the 1 bits in the binary form of a number, without a built-in bit count.

Adapted from Exercism problem-specifications (MIT), exercises/eliuds-eggs.

## Choices

- The number is shifted right one bit at a time with `Iter.iterate(number, &Int.shift_right(&1, 1))`, cut at zero, and each step's low bit, `Int.bitwise_and(&1, 1)`, is summed. Every stage is one call, so each is a capture.
- The exercise forbids a built-in bit count; std has none to forbid.
- A negative number counts as 0 eggs, since the chain stops before its first step. The exercise only has non-negative inputs.
- The four cases are one `subtests` table.

## Friction

None.
