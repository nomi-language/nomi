# Complex Numbers

Subject: Numbers & Math

Implement complex numbers: their parts, arithmetic, absolute value, conjugate and exponential, and arithmetic between a complex number and a real one.

Adapted from Exercism problem-specifications (MIT), exercises/complex-numbers.

## Choices

- `Complex` is an opaque struct of two `Float`s, built with `Complex.new(real, imaginary)` and read with `Complex.real` and `Complex.imaginary`. `derive Equatable` compares both parts exactly, which the cases with whole-number results use.
- The arithmetic is the operator interfaces, as rational-numbers does it: `impl Add<Complex, Complex> for Complex` and the same for `Subtract`, `Multiply` and `Divide`, so the tests write `z1 + z2`.
- The canonical cases that mix a real number with a complex one are four more impls on `Complex` with a `Float` right-hand side and four on `Float` with a `Complex` right-hand side, so `Complex.new(1.0, 2.0) + 5.0` and `5.0 + Complex.new(1.0, 2.0)` both work. Each lifts the `Float` with `Complex.from_real` and uses the complex operator.
- `exp` is `e^a * (cos(b) + i * sin(b))` from std/float's `exp`, `cos` and `sin`.
- The canonical data asks for a tolerance on results that are not whole numbers. `Complex.equal_within` holds both parts to it through `Float.equal_within`, and the division, exponential and mixed cases use it with `1e-12`.
- The canonical inputs write pi and e as the strings `"pi"` and `"e"`, and ln(2) as `"ln(2)"`. The tests write std/float's `Float.pi` and `Float.e`, and `Float.log(2.0)`.
- Each canonical group is a test with one `subtests` table; the four arithmetic tests sit in a `group "Arithmetic"`, as the canonical data nests them. The mixed cases are one table whose actual column is the expression itself (`5.0 * Complex.new(2.0, 5.0)`), since the rows mix operand types.

## Friction

None.
