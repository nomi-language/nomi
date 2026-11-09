package rt

// Unit is Nomi's `Unit` — the type of `io.print(x)`, of a `fn` with no declared
// return, and of an `if` with no `else`.
//
// A zero-width struct: every Nomi expression has a value, and `if` is an
// expression, so every construct needs a destination of *some* type. struct{}
// occupies no space and is passed in no register.
type Unit struct{}
