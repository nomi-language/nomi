package vm

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// TestDispatchMiss_ReceiverOfDeclaredType decides a scalar operand's
// receiver both ways and leaves an untyped operand undecided. No dispatched
// call the builder makes today declares a concrete receiver (each is an
// interface value), so this is the only reading of the decided branch.
func TestDispatchMiss_ReceiverOfDeclaredType(t *testing.T) {
	cases := []struct {
		name        string
		declared    *ir.ValType
		receiver    any
		of, decided bool
	}{
		{"an Int operand holding an Int", ir.IntType, int64(1), true, true},
		{"an Int operand holding a String", ir.IntType, "s", false, true},
		{"a String operand holding a String", ir.StringType, "s", true, true},
		{"an untyped operand", nil, int64(1), false, false},
	}
	for _, c := range cases {
		miss := &DispatchMiss{Func: "f", Callee: "I.m", Received: "Int", Declared: c.declared, receiver: c.receiver}
		of, decided := miss.ReceiverOfDeclaredType()
		if of != c.of || decided != c.decided {
			t.Errorf("%s: answered (%v, %v), want (%v, %v)", c.name, of, decided, c.of, c.decided)
		}
	}
	want := "vm: f: I.m dispatches on Int, and this program's modules did not retain an implementation for it"
	if got := (&DispatchMiss{Func: "f", Callee: "I.m", Received: "Int"}).Error(); got != want {
		t.Errorf("the message is %q, want %q", got, want)
	}
}
