package irbuild

import (
	"slices"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestLambdaParamNames_BlanksWhatCannotBeNamed states, as an executable claim,
// which parameters contribute a name.
func TestLambdaParamNames_BlanksWhatCannotBeNamed(t *testing.T) {
	lam := &ast.Lambda{Params: []ast.Param{
		{Name: "x"},
		{Name: "_"},
		{Name: "p", Destructure: &ast.StructPattern{}},
		{Name: "_label"},
	}}
	got := lambdaParamNames(lam, 4)
	want := []string{"x", "", "", "_label"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v: a bare discard or destructuring parameter cannot be "+
			"named at a call site, and blanking it is what stops two of them from "+
			"colliding in argSlotPlan's name table", got, want)
	}
}
