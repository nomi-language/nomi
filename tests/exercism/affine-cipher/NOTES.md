# Affine Cipher

Subject: Encoding & Ciphers

Encode and decode text with the affine cipher, which maps letter number `i` to `(a * i + b) mod 26`.

Adapted from Exercism problem-specifications (MIT), exercises/affine-cipher.

## Choices

- The key is a `pub struct Key { a, b }`, and the one error, an `a` that shares a factor with 26, is `Error.NotCoprime`.
- The modular inverse is found by search, `Iter.find` over `0..26` for the `x` with `a * x mod 26 == 1`. The search fails exactly when `a` is not coprime with 26, so one function both checks the key and gives the inverse; the extended Euclidean algorithm is not worth it for 26 candidates.
- `encode` checks the key with `_ = try inverse(key.a)` before it starts, since it does not need the inverse.
- `translate` takes the step as a function, `fn i -> key.a * i + key.b` or `fn y -> a_inverse * (y - key.b)`, so encoding and decoding share it. `Int.modulo` brings a negative result back into `0..26`, where `%` would leave it negative.
- Letters go to numbers through a map, a `once` that pipes `Iter.zip(alphabet, 0..letter_count)` into `Iter.to_map()`, written stacked to start from the pairs, and back with `Vector.at`, which cannot miss because the index is reduced mod 26.
- Each table names `Key` in its first row only (`#Key{a: 5, b: 7}`); the rows after it write `#{a: 15, b: 18}`, which the first row makes a `Key`, so the table needs no annotation.

## Friction

None.
