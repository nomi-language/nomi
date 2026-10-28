# Simple Linked List

Subject: Collections & Algorithms

Build a singly linked list that is last in, first out: count it, push and pop values, peek at the head, list its values and reverse it.

Adapted from Exercism problem-specifications (MIT), exercises/simple-linked-list.

## Choices

- `LinkedList<T>` is a generic enum, `Empty` or a `Node` with a `value` and the `next` list. Nomi values cannot change, so it is a persistent list: `push` answers a new head over the old list, `pop` answers the head's value and the list after it, and every list shares the nodes below its head with the list it came from.
- `pop` and `peek` answer `Result`, with `Error.EmptyList` for an empty list, the canonical "list is empty" error.
- `impl Iter<T> for LinkedList<T>` walks the list from the head, so counting and listing the values are std's `Iter.count` and `Iter.to_list`, called on the list from the tests, and `reverse` is `Iter.reduce(list, LinkedList.push, from: LinkedList.Empty)`. `Iter.count` walks the list each time, as a singly linked list without a stored length does.
- The canonical data has two mutually exclusive groups for the list's values, last in first out (`[3, 2, 1]` for a list built from `[1, 2, 3]`) and first in first out. This port takes the first, which is the order `pop` answers and `Iter` walks, and leaves out the three first-in-first-out cases.
- The canonical cases are lists of operations, so the test file follows circular-buffer: each is a list of `Step`s (`Count(2)`, `Pop(2)`, `PopFails`, `Push(3)`, `Peek(1)`, `PeekFails`, `ToList([3, 2, 1])`, `Reverse`), `replay` runs them on the list built from the case's initial values with one `Iter.map_reduce`, and each row asserts `replay(values, steps) == steps`.
- Each canonical group is a test holding one `subtests` table. Two cases that newer ones reimplement are left out: the first "Can push to an empty list", which the canonical data writes with no initial values at all, and the first "Reversed non-empty list is reversed", which the newer one extends with a count.

## Friction

None.
