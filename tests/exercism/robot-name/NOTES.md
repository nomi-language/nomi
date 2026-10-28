# Robot Name

Subject: Games & Simulation

Give each robot off the factory floor a random name of two uppercase letters and three digits, give it a new one when it is reset, and never give two robots the same name.

Adapted from Exercism problem-specifications (MIT), exercises/robot-name.

## Choices

- Adapted: the exercise asks for a robot object that names itself on first use and whose name a reset wipes in place, with uniqueness kept across every robot in the program. Nomi values do not change and there is no global state, so a `Factory` value holds what the exercise keeps hidden: the names given so far and the random seed. `Factory.build` answers a robot and the factory after it, and `Factory.reset` answers the robot named afresh and the factory after it. A robot is a value whose name never changes, so "the name is the same each time" holds by construction. The exercise has no canonical data, so the tests are the instructions' rules.
- Randomness is std/random's: a `Seed` threaded through `Generator.step`. The tests make the factory from a fixed seed, `Seed.from_int(42)`, so they are deterministic, and check properties (the format, different names, a thousand distinct names, the same seed giving the same names) rather than particular names.
- A name is one draw, `Generator.int` below 676,000 (26 × 26 × 1000), read as two letters and three digits, so every name is equally likely with no retry inside a name.
- A drawn name the factory has given before is drawn again. Once all 676,000 names are given, `build` answers `Error.NamesExhausted` rather than looping. A reset never gives back the robot's old name, nor any name given before, so `reset` needs only the factory; it takes the robot to say which one is reset.
- The tests are a `group` whose `setup` makes the factory, one test per rule. `names` builds many robots with `Iter.map_reduce` over the factory; a factory that runs out within a thousand robots would be a bug, which its `unreachable` says.

## Friction

None.
