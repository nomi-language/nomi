# Circular Buffer

Subject: Collections & Algorithms

A fixed-capacity buffer that is read oldest first: writes fail when it is full, reads fail when it is empty, and an overwrite drops the oldest item to make room.

Adapted from Exercism problem-specifications (MIT), exercises/circular-buffer.

## Choices

- `Buffer<T>` is a value, an opaque struct of its capacity and its items oldest first. Each operation answers the buffer after it: `write` a `Result<Buffer<T>, Error>`, `read` a `Result<#(T, Buffer<T>), Error>` with the item and the rest, and `clear` and `overwrite` a `Buffer<T>`, since they cannot fail. There is no array and no read or write position to wrap around: the items are a `List`, and the ring is what the operations promise, not how the items are stored.
- The errors are an `Error` enum, `Full` and `Empty`.
- The operations are functions of `impl Buffer<T>`, called `Buffer.write(buffer, 1)` after `import circular_buffer.Buffer`. Each names the block's `T` in its signature and declares no type parameter of its own, so a call takes `T` from its buffer, or, for `Buffer.new(capacity)`, from the type the result is expected to have. `full?` is private to the block.
- The canonical cases are lists of operations, each with whether it should succeed. The test file turns each into a `Step` (`Write(1)`, `WriteFails(2)`, `Read(1)`, `ReadFails`, `Clear`, `Overwrite(3)`), and `replay` runs the steps on a buffer and answers the steps as they happened. A buffer that behaves answers the steps it was given, so each row asserts `replay(capacity, steps) == steps`, and a failure shows the list with the step that went another way.
- `replay` is one `Iter.map_reduce` whose state is the buffer and whose output is each step as it happened. A failed write or read keeps the buffer as it was.
- The 14 canonical cases are one `subtests` table named by the description column. The first row qualifies its first step, `Step.Write(1)`, and the steps after it are bare.

## Friction

None.
