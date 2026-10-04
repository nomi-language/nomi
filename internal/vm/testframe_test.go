package vm

import (
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// testFrame opens an activation of f with nothing written, for a test that
// drives one instruction's handler directly.
func testFrame(f *ir.Func, runtime *rt.Frame) *frame {
	m := &Machine{codes: &codeTable{}}
	c := m.compile(f)
	stk := &regStack{}
	fr := stk.newFrame()
	fr.c, fr.fn, fr.runtime, fr.testBody, fr.depth = c, f, runtime, false, 0
	fr.wb, fr.sb, fr.rb = stk.push(c)
	return fr
}
