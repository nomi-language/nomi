# Grade School

Subject: Collections & Algorithms

Keep a school's roster: enroll each student in one grade, list the students in a grade, and list the whole school by grade and then by name.

Adapted from Exercism problem-specifications (MIT), exercises/grade-school.

## Choices

- `School` is an opaque struct over one `Map<String, Int>` from each name to its grade. A student is enrolled at most once in the whole school, so the name is the key, and the map refuses a second grade for a name by construction rather than by a check across grades.
- `School.add` returns `Result<School, Error>`, with `Error.AlreadyEnrolled` for a name already on the roster. The school is a value, so adding returns the new school; the canonical "add" cases' `true` and `false` are whether each `add` answered `Ok`.
- The roster is one `Iter.sort_by` with the tuple key `#(grade, name)` (style §32): tuples compare element by element, so the grade decides and the name breaks a tie.
- `School.grade` filters the map's pairs to the grade and sorts the names with `Iter.sort`.
- The tests build each school with an `enroll` helper, one `Iter.map_reduce` over the students whose state is the school and whose output is whether each `add` succeeded. A refused add keeps the school as it was, which is what the canonical cases expect of the roster after a duplicate.
- The three canonical properties are three `subtests` tables. The roster and grade tables are annotated, since their first rows hold empty lists that give the element types nothing. The three cases that newer ones reimplement are left out.

## Friction

None.
