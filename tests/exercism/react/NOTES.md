# React

Subject: Collections & Algorithms

Build a small reactive system: input cells hold values that can be set, compute cells hold functions of other cells that stay up to date, and callbacks report when a compute cell's value changes.

Adapted from Exercism problem-specifications (MIT), exercises/react.

## Choices

- Adapted: the exercise asks for cells that are objects updated in place and callbacks that are functions run for their effect when a value changes. Nomi values cannot change and a function cannot record that it ran, so the reactor is a value, `set_value` answers the reactor after the change, and a callback is a name: `set_value` also answers the callbacks that fire, each with its cell's new value, as a `Map<String, Int>`. The canonical "expect callbacks" and "expect callbacks not to be called" become that one map, compared whole.
- `Reactor` is an opaque struct of every cell's value by name, the set of input names, the compute cells in the order they were made, and each callback's cell. A compute cell may read only cells made before it, so the cells in order are already sorted by dependency: `set_value` recomputes them in one `Iter.reduce`, each exactly once, which is why a cell with two changed inputs fires its callbacks once. A callback fires when its cell's value before and after differ, so a recomputed cell whose value stays the same fires nothing.
- Every name a cell reads or a callback watches was checked when it was made, so the reactor reads values with `Map.get(...)!`, which traps on a missing key, rather than `Map.get`.
- A compute cell's function takes its inputs' values as a `Vector<Int>` in the order the cell names them, so the canonical `inputs[0] + inputs[1] * 10` is `fn v -> Vector.get(v, 0)! + Vector.get(v, 1)! * 10`.
- Making or setting a cell answers a `Result`: a name used twice, an unknown cell, and setting a compute cell are `Error` variants. The canonical data never makes these mistakes; the test file adds three tests, marked not canonical, for them. They assert with a pattern, `assert Err(Error.NotAnInput("output")) = ...`, since a reactor holds functions and so has no `==`.
- The canonical cases are cells and lists of operations, so the test file follows circular-buffer: each case lists its `Cell`s, which `build` makes in order, and its `Step`s (`Value`, `Set` with the callbacks it fires, `AddCallback`, `RemoveCallback`), which `replay` runs with one `Iter.map_reduce`; each row asserts `replay(reactor, steps) == Ok(steps)`. The 14 cases are one `subtests` table named by the description column.

## Friction

None.
