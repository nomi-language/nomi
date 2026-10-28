# Simple Cipher

Subject: Encoding & Ciphers

Encode and decode text with a Vigenère cipher, moving each letter along the alphabet by the matching letter of a repeating key, and make a random key when none is given.

Adapted from Exercism problem-specifications (MIT), exercises/simple-cipher.

## Choices

- `Cipher` is an opaque struct holding the key. `Cipher.new(key)` takes a given key, and `Cipher.random(seed)` draws one of 100 lowercase letters with std/random and answers the cipher and the next seed. Nomi has no hidden random state: randomness is a `Seed` threaded through `Generator.step`, so the random cipher is a pure function of its seed.
- The key's letters are drawn by one generator, `Generator.int(0, 25)` mapped to the alphabet and repeated with `Generator.list(100)`. `Generator.uniform` would also do, but it takes the first choice apart from the rest.
- `encode` and `decode` are one `shift` with a direction of 1 or -1. The text is zipped with the key cycled by `Iter.cycle`, so a key shorter than the text repeats with no index arithmetic, and `Int.modulo` wraps a letter past `z` or before `a`.
- A letter's number is its codepoint less `a`'s, and the moved letter is read back from the `alphabet` vector, so no `Maybe` from `Int.to_codepoint` needs unwrapping. The canonical data gives only lowercase letters, which the doc comments state as the promise.
- The random-key cases run against a cipher made from a fixed seed, `Seed.from_int(2026)`, in a `group` whose `setup` builds it, so the tests are deterministic and still check the canonical properties (the key encodes `a`s to itself, decodes back, and is all lowercase letters) rather than a particular key.
- The substitution cases are two `subtests` tables, encode and decode, and one test for reversibility, since it calls both.

## Friction

None.
