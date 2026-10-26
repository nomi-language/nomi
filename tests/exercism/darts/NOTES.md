# Darts

Subject: Games & Simulation

Score a dart by how far from the centre of the target it lands.

Adapted from Exercism problem-specifications (MIT), exercises/darts.

## Choices

- The distance is compared squared, against 1, 25 and 100, so no square root is needed and a dart exactly on a circle (`0, 10`) scores as inside it with no rounding.
- The bands are `case` arms with `when` guards, checked from the inside out. An `if`/`else if` ladder was the other form; the arms line up the radii and their points.
- `score` takes two `Float`s and returns an `Int`. The canonical inputs that are whole numbers are written `9.0` in the tests, since an `Int` literal is not a `Float`.
- The 13 cases are one `subtests` table whose middle column is the `#(x, y)` point.

## Friction

None.
