package rt

// std/random.Seed — the PRNG register — as a Go type.
//
// `pub opaque type Seed Int`, so this is int64 for rt/codepoint.go's reason
// exactly: the Nomi declaration names `Int` as the inner type, internal/irbuild's
// opaque spec asserts that inner kind, and a narrower Go type here would make
// the two disagree and silently truncate a `Seed(n)` built from an int64
// expression.
//
// # This file holds a type and no rules, deliberately
//
// std/random's generators are ordinary Nomi source over three externs
// (`below_state`, `unit_float_state`, `os_state`), all of which are Go
// functions in internal/stdrandom rather than language primitives. So
// there is nothing here for rt to implement, and adding a splitmix64 step
// beside the adapter's would be two implementations of one sequence — the
// divergence rt/opaque.go's header warns about, over a generator where
// disagreement is silent because both answers look random.
//
// What the row buys is what opaqueSpecs says a row is for: letting a signature
// name the type. `Seed.from_int(n: Int) -> Seed` is `Seed(n)` — a Nomi body that
// lowers the moment its result type is representable, needing no rt symbol at
// all.
type Seed int64
