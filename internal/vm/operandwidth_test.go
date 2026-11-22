package vm

import (
	"strings"
	"testing"
)

// An instruction word holds its opcode in eight bits and its first operand
// in the other twenty-four, and a location its register in thirty bits. An
// operand past either is a compile panic, which compile turns into a body
// that reports an internal error (compilepanic.go), never an operand that
// silently names another register, bank or table entry.
func TestOperandWidth_AnOperandPastItsBitsPanics(t *testing.T) {
	panics := func(name string, f func()) {
		t.Helper()
		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("%s did not panic", name)
			}
			if s, _ := r.(string); !strings.Contains(s, "is past the") {
				t.Fatalf("%s panicked with %v", name, r)
			}
		}()
		f()
	}
	var cp compiler
	if got := cp.op(opMovS, maxOpOperand); got>>8 != maxOpOperand || opcode(got&0xff) != opMovS {
		t.Fatalf("op(opMovS, %d) = %#x", maxOpOperand, got)
	}
	panics("op past 24 bits", func() { cp.op(opMovS, maxOpOperand+1) })
	if l := mkLoc(bankS, locRegMask); l.bank() != bankS || l.reg() != locRegMask {
		t.Fatalf("mkLoc(bankS, %d) = bank %d, register %d", locRegMask, l.bank(), l.reg())
	}
	panics("mkLoc past 30 bits", func() { mkLoc(bankS, locRegMask+1) })
}
