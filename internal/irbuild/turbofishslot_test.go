package irbuild

import (
	"testing"
)

// TestStdSlotFill_TheAccessorNameIsReadThroughTheCanonicalPointer is the
// snapshot property, the same one arityMin has.
//
// `bindStdSiblings` and `stdInstInvoke` take `view := *f` copies, some BEFORE
// the lowering pass runs. A frozen copy reports an accessor as absent for one
// that has since been built, which is a refusal where a lowering was
// available, the benign direction; but the same field read directly would also
// let a copy taken AFTER a failed lowering name an accessor that was never
// built. Both are settled by reading through canon.
func TestStdSlotFill_TheAccessorNameIsReadThroughTheCanonicalPointer(t *testing.T) {
	canonical := &stdFunc{key: "a.F.f", params: []kind{kindInt, kindInt}}
	view := *canonical
	view.canon = canonical
	if callDefaultFill(&view, 1) {
		t.Fatal("callDefaultFill answers true before any accessor was built; a call site would " +
			"name an accessor that does not exist")
	}
	canonical.defaultFill = []bool{false, true}
	if !callDefaultFill(&view, 1) {
		t.Fatal("callDefaultFill answers false through a snapshot taken before lowering; the value " +
			"is lowered on the canonical pointer, so the copy must not freeze it")
	}
	if callDefaultFill(&view, 5) {
		t.Fatal("callDefaultFill answers true for a slot past the parameter list")
	}
}
