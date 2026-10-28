# Lens Person

Subject: Collections & Algorithms

Read and change fields deep inside a nested record of a person, their birth and their address, without writing out every level by hand.

Adapted from Exercism problem-specifications (MIT), exercises/lens-person.

## Choices

- Adapted: the exercise asks for lenses, getter and setter pairs that compose, because in Haskell changing a field three records down means rebuilding each record on the way. Nomi's spread update does that rebuilding itself: `#{..person, address: #{.., street}}` replaces one nested field and keeps the rest, at any depth, and a field path such as `born.at.street` reads one. So the four functions are plain reads and updates, with no `Lens` type; a lens type would be a second spelling of what the update already says.
- The records are four structs, `Person`, `Name`, `Born` and `Address`, with the Haskell track's fields in snake case, and the birth day a std/calendar `Date`.
- `set_birth_month` answers a `Result`: `Date.new` refuses a day the new month does not have, where the Haskell track's `fromGregorian` clips it to the month's last day. The test file adds one case, marked not canonical, that sets February for a person born on the 31st.
- `rename_streets` takes the renaming as a function and applies it to both streets in one update with two nested spreads.
- The exercise has no canonical data. The tests follow the Haskell track's: one person, Jane Joanna Doe, as a `once`, with her birth day written as the checked literal ``Date`1970-03-02` ``, and one test per function. The setters also check that nothing else changed by comparing with a spread update of the original.

## Friction

None.
