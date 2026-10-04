// Package rt is Nomi's runtime library: the collections, checked arithmetic
// and its trap text, Float semantics, structural hashing, scalar rendering,
// the frame, concurrency, and the test reporter. The VM calls it, and the
// generated FFI wrapper links it.
//
// # It is a leaf
//
// rt imports only the Go standard library, one third-party module
// (github.com/rivo/uniseg, for grapheme clusters), and its own packages. It
// must never reach nomi/analysis, nomi/ast or nomi/parser: a VM runner that
// carries a program's bytecode has no front end in it, and rt is the library
// it links. rt shares a Go module with the compiler, so a new dependency of
// the module is not a dependency of rt unless rt imports it.
//
// This is enforced, not intended: see imports_test.go's
// TestRuntimeImportsOnlyItsAllowlist and internal/vm/runtime_deps_test.go's
// TestRuntimeArtifactLinksNoFrontEnd.
//
// # One implementation, not two agreeing ones
//
// rt owns the native-typed core (AddOverflows, OverflowError, FormatFloat over
// int64 and float64), and everything that needs one of those rules calls it.
// `9223372036854775807 + 1` therefore traps with byte-identical text wherever
// it happens because the same lines run, not because a test says so. One
// thing in this package is genuinely a second encoding of a rule stated
// elsewhere, and it is called out where it is defined: FormatBool (std
// derives "True"/"False" from a Nomi `impl Display`).
//
// # Calling convention
//
// Functions that run Nomi code take `fr *Frame` first (see frame.go). Runtime
// faults are panics carrying *Error, recovered at the VM's boundary.
package rt

// Unit is Nomi's `Unit` — the type of `io.print(x)`, of a `fn` with no declared
// return, and of an `if` with no `else`.
//
// A zero-width struct: every Nomi expression has a value, and `if` is an
// expression, so every construct needs a destination of *some* type. struct{}
// occupies no space and is passed in no register.
type Unit struct{}
