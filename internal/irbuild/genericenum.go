package irbuild

// A USER-DECLARED generic ENUM, MONOMORPHIZED per instantiation.
//
// generictype.go owns the mechanism, the intern table and the representation
// argument; this file owns the one thing an enum needs that a struct does not,
// which is WHERE THE TYPE ARGUMENTS COME FROM AT A CONSTRUCTION SITE.
//
// # Where an enum's type arguments come from
//
// A construction is built at the instance the checker typed it at
// (genericVariant): a payload-carrying variant's call at its instantiated
// result (`Wrapper.Wrapped(42)` is a `Wrapper<Int>`), a struct-shaped
// variant's literal at the literal's type (literalTypeArgs), and a
// payload-free variant (`Wrapper.Empty`) at the expected kind, or else at
// the instance the checker recorded for it (Symbol.VariantType). Nothing is
// solved from the payload's kind. A `case` pattern resolves through the
// subject's def, which is already the instance.
