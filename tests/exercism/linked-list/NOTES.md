# Linked List

Subject: Collections & Algorithms

Build a list that is read and written at both ends, push and pop at the back, unshift and shift at the front, that counts its values and deletes the first one equal to a given value.

Adapted from Exercism problem-specifications (MIT), exercises/linked-list.

## Choices

- Adapted: the exercise asks for a doubly linked list, whose nodes point both ways and are edited in place. Nomi values cannot change, so no node can point back at one built after it; the port keeps the operations and their cost instead, as a persistent double-ended list. Each operation answers a new list.
- `LinkedList<T>` is an opaque struct of two `List`s, the front values in order and the back values newest first, so both ends are a list head: `push`, `unshift` and a `pop` or `shift` that finds its end non-empty take constant time. An end that runs out reverses the other list once, which every value pays at most once on its way through.
- `pop` and `shift` answer `Maybe<#(T, LinkedList<T>)>`, the value and the list without it, with `None` for an empty list. The canonical data never empties a list too far, and the test file adds one case, marked not canonical, that does.
- `impl Iter<T> for LinkedList<T>` walks the front, then the back reversed, so the count is std's `Iter.count` and `delete` reads the values in order with `Iter.to_list`. `delete` takes out the first equal value with `List.remove` and answers the rest as one front list.
- The canonical cases are lists of operations, so the test file follows circular-buffer and simple-linked-list: each is a list of `Step`s (`Push(7)`, `Pop(7)`, `Shift(17)`, `Unshift(23)`, `Count(2)`, `Delete(83)`), `replay` runs them on an empty list with one `Iter.map_reduce`, and each row asserts `replay(steps) == steps`. Where a canonical pop or shift gives no expected value, its step names the value it takes.
- The 19 canonical cases are one `subtests` table named by the description column.

## Friction

None.
