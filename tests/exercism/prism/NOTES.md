# Prism

Subject: Games & Simulation

Follow a laser beam through an array of prisms, each of which turns the beam by its own angle, and list the prisms it hits in order.

Adapted from Exercism problem-specifications (MIT), exercises/prism.

## Choices

- The start is a `Laser`, a point and a direction in degrees, and each prism a struct of its id, point and turning angle, all `Float`s, so the canonical integers are written `10.0`.
- `trace` follows the beam with tail recursion: `next_hit` finds the nearest prism ahead, and the beam continues from that prism's point with the prism's angle added, until nothing is ahead. The beam never hits the prism it is leaving, which `from` names; it may come back to it later, as "same prism twice" does.
- `next_hit` projects each prism onto the beam's direction: `along` is how far ahead it lies and `off` how far beside the line. A prism is on the line when `off` is less than a thousandth of `along`, that is when the beam would miss it by under a thousandth of a radian. The canonical data states no tolerance; an exact test misses prisms in the two floating-point cases, where coordinates rounded to a tenth lie a little off the computed line, and a tolerance relative to the distance holds near and far prisms to the same angle.
- The 14 canonical cases are one `subtests` table named by the description column, each row a start, its prisms and the expected ids.

## Friction

None.
