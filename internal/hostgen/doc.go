// Package hostgen generates host-function adapters: Go source that calls a Go
// host function with rt values and no reflection. See nomi/hostadapt for the
// runtime half.
//
// Two inputs decide an adapter, and neither is restated:
//
//   - the Nomi `host fn` declaration, which says what the operands and the
//     result are (a Shape per position, with the declared struct identities);
//   - the Go function the binding table names, whose reflect.Type says what
//     the Go side takes and returns: the stdlib's tables
//     (internal/hostgen/stdtable) link each function into the generator.
//
// The generator pairs the two position by position under the projection
// rows below and refuses, by binding name, any pair it cannot convert. A
// refusal fails generation, so an unadaptable binding is a build-time error
// rather than a run-time one. Its only input is std: the rows are the ones
// std's bindings use, and the integer widths and Int ranges the
// internal/stdlibbindings package doc states, which the package's own
// tests generate.
//
// # Projection rows
//
// Each Nomi type crosses as these Go types, in both directions unless a row
// says otherwise:
//
//   - Int: rt.Int, an Int of any size, or a Go integer of a fixed width
//     (int8 to int64, uint16 to uint64, int, uint, uintptr). A parameter
//     takes a fixed width only when its declared range fits it (fits,
//     below); a result may be any width.
//   - Float: float64. Bool: bool. String: a Go string. Byte: a uint8.
//     Bytes: rt.Bytes or a byte slice.
//   - List<T>: a Go slice of T's Go type other than a byte, or *rt.List[T].
//   - Option<T>: rt.Option[T]; as a result, also *T, where nil is None.
//   - Result<T, E>: rt.Result[T, E]. A Go function whose last result is an
//     error answers `Result<_, String>`, the error's text as the Err.
//   - Map<String, V>: rt.Map[string, V].
//   - A tuple: several Go results, or an anonymous struct of fields F0, F1,
//     ... (tupleStruct).
//   - Unit: rt.Unit. As a result, any Go value: the Nomi side drops it.
//   - A struct: a Go struct of exported fields, each named the snake_case
//     of its Go name, exactly the declared fields.
//   - An enum: rt's tagged-struct encoding of it (rtshapes.go).
//   - A distinct type over a scalar: the scalar's Go type. Duration is
//     rt.Duration and Instant rt.Instant, 64 bits of nanoseconds,
//     saturated and clamped on the way in (hostadapt.DurationNanos,
//     hostadapt.InstantNanosClamped).
//   - A `host type`: the Go type its row registers, as a handle, or for an
//     rt-held type (rtHeld) the rt value itself.
//   - A function: a Go func that takes *rt.Frame first and answers one value
//     that is not an error (callback).
//
// A Go function whose first parameter is *rt.Frame is handed the caller's
// frame. A Go function answers at least one value.
//
// An Int position carries the range its declared type admits (IntRange),
// read off a refinement chain's `when` bounds and an intersection's members.
// A Nomi-to-Go Int position takes a fixed-width Go integer only when that
// range fits it (fits): std's `Instant.as_nanos` is an `Int64` and fills
// an int64; a plain `Int` does not, and its Go function takes rt.Int.
// Generation refuses a row that breaks the rule and names the type to
// declare. The adapter still narrows with a checked conversion, which a
// value the declared range admits always passes.
//
// # Register forms
//
// Each binding of at most hostadapt.MaxSlots operands is generated as its
// register form alone (regAdapter): the Go call over a hostadapt.Slots, an
// engine's registers as they are. A scalar operand is read unboxed and
// narrowed to its Go type, an Int into an rt.Int without a box; any other
// goes through the conversion an adapter over boxed values uses. The answer
// is written by the same rule, a tuple's components and an Option's or a
// Result's payload each into a slot; an answer of more than MaxSlots parts
// is refused. The form is a row of the file's regRows, which Bind hands to
// hostadapt.BindRows: it goes to Env.AddRegs with its operands' and its
// answer's slot types, and its adapter in Bind's table is the form over
// boxed values (hostadapt.Regs.Func), so a binding's conversions are
// written once. The boxed adapter is still built first, and dropped, so a
// binding is refused for what either form refuses. A binding of more
// operands keeps a boxed adapter of its own.
package hostgen
