# PaaS I/O

Subject: Collections & Algorithms

Wrap a stream so that it reports what passed through it: the bytes read or written, and the number of read or write operations.

Adapted from Exercism problem-specifications (MIT), exercises/paasio.

## Choices

- Adapted: the exercise asks for a wrapper object around a file or a network connection that keeps running totals as it is used. Nomi values cannot change, so a meter is a value that pairs the stream with its `Stats` so far, and each `read` or `write` answers the meter after it; the caller threads it as it would thread any state. The stream underneath is std/io's `Reader` or `Writer`, which already moves on as it is read or written; std has no network connection, so files and sockets are both whatever `Reader` and `Writer` stand for.
- `Stats` is a struct of `bytes` and `operations` that both default to 0, and `MeteredReader` and `MeteredWriter` hold a stream and their `Stats`. Every read counts as an operation, the one that finds the end of the stream too, since it is a call the platform served; an error is passed up as the stream's `io.Error` and counts nothing.
- A meter is an ordinary value, so an earlier meter keeps the counts it had: the last test reads twice and checks all three meters.
- The exercise has no canonical data. The tests follow the other tracks' in spirit: reads and writes count bytes and operations, the data passes through unchanged, an error passes through. Reads go over `Reader.from_text`, writes to `io.stdout` inside `io.capture`, and the failing read over `Reader.from_fn`.

## Friction

- [feature] `Reader` and `Writer` are host types, not interfaces, so a meter cannot itself be a `Reader`: code that reads through `Reader.read_all_text` or `Reader.read_line` cannot be handed a `MeteredReader` and be counted. `Reader.from_fn` builds a `Reader` from a function, but a function cannot keep a count from one call to the next. Go's `io.Reader` and Rust's `Read` are interfaces a wrapper implements.
