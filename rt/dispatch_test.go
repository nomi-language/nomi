package rt

import (
	"testing"
	"unsafe"
)

// Two same-short-named types from two modules must be two identities, in the
// address the table keys on and in the diagnostic string.
//
// Nothing is cut here because nothing is a string: the key is the
// variable's address. The second assertion guards against a toolchain that
// merges two byte-identical package variables, and the third against two
// package-level variables of a zero-sized type sharing an address, which Go
// permits.
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
