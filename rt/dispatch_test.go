package rt

import (
	"strings"
	"testing"
	"unsafe"
)

// Two same-short-named types from two modules must be two identities, in the
// address the table keys on AND in the diagnostic string.
//
// This is `c2afa942` written as a test. There, `shapes.Point`-shaped names were
// cut at the last dot and two impls came to share one slot, resolved by Go map
// order. Here nothing is cut because nothing is a string: the key is the
// variable's address. The second assertion guards the hazard that fix
// introduces — a toolchain that merged two byte-identical package variables
// would undo it — and the third guards the one it replaced, since two
// package-level variables of a zero-sized type may legally share an address.
func TestTypeIDsAreDistinctAddresses(t *testing.T) {
	shapesPoint := TypeID{Nomi: "shapes.Point"}
	geometryPoint := TypeID{Nomi: "geometry.Point"}

	if &shapesPoint == &geometryPoint {
		t.Fatal("two TypeIDs share an address; every dispatch key would collapse")
	}
	if shapesPoint.Nomi == geometryPoint.Nomi {
		t.Fatalf("two distinct types carry byte-identical diagnostic names %q; "+
			"the IR builder must qualify by module", shapesPoint.Nomi)
	}
	if unsafe.Sizeof(TypeID{}) == 0 {
		t.Fatal("TypeID is zero-sized; Go may give two of them one address")
	}
}

// A name is never consulted. Two types whose diagnostic strings are identical —
// which is exactly what `ShortTypeName` produced when it cut at the last dot —
// still get their own slots and their own answers.
func TestDispatchIgnoresTheDiagnosticName(t *testing.T) {
	jsonErr := TypeID{Nomi: "DecodeError"}
	dynamicErr := TypeID{Nomi: "DecodeError"}

	m := NewMethod[func(*Frame) string]("Debug.inspect")
	m.Bind(&jsonErr, func(*Frame) string { return "json" })
	m.Bind(&dynamicErr, func(*Frame) string { return "dynamic" })

	if got := m.Get(Dyn{TID: &jsonErr})(nil); got != "json" {
		t.Fatalf("json slot resolved to %q", got)
	}
	if got := m.Get(Dyn{TID: &dynamicErr})(nil); got != "dynamic" {
		t.Fatalf("dynamic slot resolved to %q", got)
	}
}

// A duplicate binding is a panic, not an overwrite: an overwrite would hand the
// winner to package initialization order.
func TestDuplicateBindPanics(t *testing.T) {
	tid := TypeID{Nomi: "m.Dog"}
	m := NewMethod[func(*Frame) string]("Speech.speak")
	m.Bind(&tid, func(*Frame) string { return "first" })

	defer func() {
		r := recover()
		e, ok := r.(*Error)
		if !ok {
			t.Fatalf("duplicate bind panicked with %#v, want *rt.Error", r)
		}
		if !strings.Contains(e.Msg, "two implementations") || !strings.Contains(e.Msg, "m.Dog") {
			t.Fatalf("duplicate-bind message %q names neither the fault nor the type", e.Msg)
		}
	}()
	m.Bind(&tid, func(*Frame) string { return "second" })
	t.Fatal("a second binding for one type was accepted")
}

// An unbound type traps with the dispatch-miss wording rather than returning a
// zero-valued function.
func TestMissingImplTraps(t *testing.T) {
	tid := TypeID{Nomi: "m.Cat"}
	m := NewMethod[func(*Frame) string]("Speech.speak")

	defer func() {
		r := recover()
		e, ok := r.(*Error)
		if !ok {
			t.Fatalf("missing impl panicked with %#v, want *rt.Error", r)
		}
		want := "Speech.speak: no implementation for type 'm.Cat'"
		if e.Msg != want {
			t.Fatalf("trap text %q, want %q", e.Msg, want)
		}
	}()
	m.Get(Dyn{TID: &tid})
	t.Fatal("a missing implementation resolved")
}

// NoImplFor is the same fault reached from a statically resolved call site, and
// this pin is what keeps the two spellings ONE message.
//
// The pin is ABSOLUTE and not a `Contains`, because the caller that needs it is
// irbuild's `preludeHashCall` building an arm for a prelude type argument no
// program can solve — the text for `Hashable.hash` at Unit, in an arm that
// never runs. **No corpus run can see a drift there**: no value of an unsolved
// position exists in any program, so nothing ever reaches the arm and no
// fixture can record its output. This is the guard.
func TestNoImplForIsTheSameMessageAsAMissedDispatch(t *testing.T) {
	defer func() {
		r := recover()
		e, ok := r.(*Error)
		if !ok {
			t.Fatalf("NoImplFor panicked with %#v, want *rt.Error", r)
		}
		want := "Hashable.hash: no implementation for type 'Unit'"
		if e.Msg != want {
			t.Fatalf("trap text %q, want %q", e.Msg, want)
		}
	}()
	NoImplFor("Hashable.hash", "Unit")
	t.Fatal("NoImplFor returned")
}

// And the two really are one function rather than two literals that agree.
//
// A `Contains` would pass for a copy; this compares the WHOLE text produced by
// each route at one (key, type) pair, which is the only thing that fails when
// somebody corrects one site and not the other. Main asked for exactly this
// when the extraction was approved.
func TestMissedDispatchAndNoImplForShareOneFormat(t *testing.T) {
	tid := TypeID{Nomi: "m.Cat"}
	m := NewMethod[func(*Frame) string]("Speech.speak")

	var viaTable string
	func() {
		defer func() {
			if e, ok := recover().(*Error); ok {
				viaTable = e.Msg
			}
		}()
		m.Get(Dyn{TID: &tid})
	}()

	var viaCallSite string
	func() {
		defer func() {
			if e, ok := recover().(*Error); ok {
				viaCallSite = e.Msg
			}
		}()
		NoImplFor("Speech.speak", "m.Cat")
	}()

	if viaTable == "" || viaCallSite == "" {
		t.Fatalf("one route did not trap: table=%q callsite=%q", viaTable, viaCallSite)
	}
	if viaTable != viaCallSite {
		t.Fatalf("two encodings of one message: table=%q callsite=%q", viaTable, viaCallSite)
	}
}
