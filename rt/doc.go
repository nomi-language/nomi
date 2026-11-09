// Package rt is Nomi's runtime library: the collections, exact Int
// arithmetic and its trap text, Float semantics, structural hashing,
// equality, scalar rendering, the frame, the task and supervisor runtime,
// and the test reporter. The VM (internal/vm) runs over it, and built
// executables and the generated FFI wrapper link it. Its internal
// organization may change freely to serve the VM.
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
// rt owns the native-typed core (IntAdd and its siblings over Int values,
// DivByZeroError, FormatFloat over float64), and everything that needs one
// of those rules calls it. `9223372036854775807 + 1` is therefore
// 9223372036854775808 wherever it happens because the same lines run, not
// because a test says so. One thing in this package is genuinely a second
// encoding of a rule stated elsewhere, and it is called out where it is
// defined: FormatBool (std derives "True"/"False" from a Nomi `impl
// Display`).
//
// # Calling convention
//
// Functions that run Nomi code take `fr *Frame` first (see frame.go).
// Runtime faults are panics carrying *Error, recovered at the VM's boundary.
//
// A frame's Go context carries what the machine gives every frame: its
// locale data, phrase tables, streams, files and input. A supervised task's
// frame takes those values from the frame that spawned it and its
// cancellation from the supervisor (supervisor.go), so it outlives the
// request that spawned it and still reads the program's locale data rather
// than `cldrdata.Default`'s, which a runner leaves empty
// (TestSupervisorTaskReadsTheSpawnersContextValues, and vmhost's
// TestLocaleImage_ASupervisedTaskReadsTheProgramsLocales).
//
// # Int: exact at any size
//
// An `Int` holds a whole number of any size, and its arithmetic is exact. A
// value that fits in 64 bits stays as cheap as a plain 64-bit integer.
//
// A value that fits int64 is an `int64` everywhere: a [Value], a VM word
// register, a record's word slot. A value past int64's range is a
// [*BigInt], an immutable `math/big` integer (bigint.go). There is one
// canonical form: every operation and constructor returns an int64 when the
// value fits, so `big - big` that lands back in range is an int64 again,
// and a value that fits int64 is never a *BigInt. Two equal Ints therefore
// have the same Go type, and equality, hashing, ordering, Map keys and
// rendering compare like with like and need no normalization.
//
// A big value in a word slot occupies the word [BigWord] (2^63, the bits of
// `math.MinInt64`), and the value itself sits beside the word, where the
// collector can see it. In a record that is the record's big slots
// (record.go): references past the layout's own, one per word slot, which a
// record allocates only when it holds a big Int. A record that holds none
// keeps its plain layout, and code that writes an int64 word straight into
// a slot stays right, since a word of BigWord with no big slot beside it is
// its own int64, `math.MinInt64`. In the VM's registers it is the word
// bank's shadow (internal/vm package doc, "Big Ints in the word bank"),
// which also lists the representations not taken.
//
// A literal too long for int64 is carried through the AST and IR as a big
// constant, and the image encodes it as base-10 text.
//
// `derive Hashable`'s mixers and `Int.wrapping_add`, `wrapping_sub` and
// `wrapping_mul` ([IntWrapAdd] and its siblings, over [Wrap64]) return the low 64 bits of
// the exact result as a signed int64, so a hash stays a 64-bit number.
package rt
