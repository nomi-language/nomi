package vm

import (
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
	"testing"
)

func TestMarker_QualifiedIdentityAndZeroSizedValue(t *testing.T) {
	table := ir.NewTable()
	pos := ir.At("marker.nomi", 2, 1)
	f := ir.NewFunc(pos, "marker")
	for _, name := range []string{"first.Ready", "second.Ready"} {
		c := ir.NewMarker(pos, f.NewTemp(), table.Symbol(name, name))
		got, err := constValue(c)
		if err != nil {
			t.Fatal(err)
		}
		marker, ok := got.(*rt.Record)
		if !ok || marker.Desc.Kind != rt.KindDistinct || marker.Desc.Name != name || marker.NumFields() != 0 {
			t.Fatalf("%s: %#v", name, got)
		}
		text, err := debugText(got)
		if err != nil || text != "Ready" {
			t.Fatalf("%s: %q %v", name, text, err)
		}
	}
}
