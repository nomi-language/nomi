package vm

import (
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// A Unit renders as the literal `Unit`, bare and as a prelude payload, which
// is what `dbg Ok(Unit)` prints.
func TestVMDbg_UnitRendersAsTheLiteral(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{rt.Unit{}, "Unit"},
		{okValue(rt.Unit{}), "Ok(Unit)"},
		{some(rt.Unit{}), "Some(Unit)"},
		{outcomeDesc.MakeVariant(0, rt.Unit{}), "Completed(Unit)"},
		{rtTuple(rt.Unit{}, int64(1)), "(Unit, 1)"},
	} {
		got, err := debugText(tc.v)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("debugText = %q, want %q", got, tc.want)
		}
	}
}
