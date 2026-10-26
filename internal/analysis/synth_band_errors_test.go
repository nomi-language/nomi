package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A program error the coherence checks anchor at a function of a
// synthesized impl is in the synthesized band, a line no one can navigate
// to. repointSynthBandErrors moves it to the declaration the impl was
// synthesized for, and leaves an error outside the band, or in a band slot
// no synthesized block of that file holds, where it is.
func TestRepointSynthBandErrors_MovesAnErrorInASynthesizedImplToItsDeclaration(t *testing.T) {
	base := synthSlot(synthOriginDecl, 2, synthKindAutoDebug)
	blk := &ast.ImplBlock{Line: base, Col: 1, SynthOriginLine: 4, SynthOriginCol: 6}
	files := func(each func(path string, nodes []ast.Node)) {
		each("main.nomi", []ast.Node{blk})
	}
	errs := []TypeError{
		{File: "main.nomi", Line: base + 3, Col: 1, Message: "orphan impl"},
		{File: "main.nomi", Line: 9, Col: 2, Message: "a source error"},
		{File: "other.nomi", Line: base + 3, Col: 1, Message: "another file's band"},
	}
	got := repointSynthBandErrors(errs, files)
	if got[0].Line != 4 || got[0].Col != 6 {
		t.Errorf("the error in the synthesized impl is at %d:%d, want the declaration's 4:6", got[0].Line, got[0].Col)
	}
	if got[1].Line != 9 || got[1].Col != 2 {
		t.Errorf("a source error moved to %d:%d", got[1].Line, got[1].Col)
	}
	if got[2].Line != base+3 {
		t.Errorf("an error in a slot of another file moved to %d", got[2].Line)
	}
	if errs[0].Line != base+3 {
		t.Error("repointSynthBandErrors changed its input")
	}
}
