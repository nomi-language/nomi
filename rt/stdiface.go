package rt

// The dispatch tables for the stdlib's own interfaces.
//
// # Why these live here and a user interface's do not
//
// A dispatch table is a package-level variable, and the rule internal/irbuild
// holds everywhere else is that it belongs to the package that declares the
// interface: one interface, one table, named from wherever (see
// internal/irbuild/existential.go). Two tables for one interface is two disjoint
// sets of implementations and whichever a site happened to probe would answer.
//
// A stdlib interface has no such package. `Display` is declared in
// `std/display.nomi`, and nothing in internal/irbuild declares a table for an
// `*ast.InterfaceDef` outside the program's own files. So the declaring
// package does not exist, and the rule has to be satisfied some other way.
//
// It is satisfied here, and this is the same answer opaque.go and stdenum.go
// give for a stdlib type: rt owns it. rt is linked exactly once into a
// process, so a variable here is reachable from every implementing module and
// there is exactly one of it.
// That is the property the rule is about; "declared beside the interface" is
// only the usual way to get it.
//
// # The Go type of each table is internal/irbuild's own derivation, checked
//
// F is what `internal/irbuild`'s `ifaceMethod.goFuncType()` renders for that
// method: `*Frame` first, every self-typed position erased to `any`, everything
// else native. Written out by hand here and derived there, so the two are
// independent encodings of one fact rather than one encoding read twice.
// TestStdIfaceTablesMatchTheEmittedSignature reads the F type back off each
// variable by reflection and compares. A std edit that changes a method's
// shape produces no anchor at all (stdIfaceSpec.matches), and a spec edit that
// disagrees with the variable here fails that test — so neither side can drift
// alone.
//
// # What is not here, and why the list is short
//
// A row is a variable in the runtime library, so it is a decision rather than
// a mechanical extension — the bar opaqueSpecs and preludeSpecs set. Only an
// interface internal/irbuild can give a complete method
// shape to belongs: no type parameters, no defaults, no host-backed methods,
// and every parameter and result representable. Everything else keeps refusing
// under `stdlib interface`, which is the honest answer and not a smaller one.
var (
	// Display.to_string(value: self): String
	MDisplayToString = NewMethod[func(*Frame, any) string]("Display.to_string")

	// Comparable.compare(a: self, b: self): Ordering
	//
	// Two self-typed positions and a non-scalar result, which is why it is
	// here beside Display rather than after it: a one-row table proves nothing
	// about generality, and a mechanism hardcoded to "one receiver, a string
	// back" passes every test Display alone can write.
	MComparableCompare = NewMethod[func(*Frame, any, any) Ordering]("Comparable.compare")

	// Debug.inspect(value: self): String
	//
	// The one universal interface in this list, and the property that makes it
	// different from the two above is worth stating where the variable is: the
	// front end synthesizes an `impl Debug for T` for every declared type
	// (`analysis.SynthesizeUniversalDebug`), so this table's population is not
	// "the types a program wrote an impl for" but "the types a program reached
	// an erased Debug call at". internal/irbuild binds it demand-driven for exactly
	// that reason; see internal/irbuild/stdiface.go.
	MDebugInspect = NewMethod[func(*Frame, any) string]("Debug.inspect")
)
