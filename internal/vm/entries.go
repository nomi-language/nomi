package vm

// ENTRY COUNTS: how many times the machine entered each function's body.
//
// A machine opened WithEntryCounts compiles each function with an `opEnter`
// at pc 0, before its entry block, so a branch back to the entry block does
// not count again. Every way into a body starts there: a call from Go
// (activateSlot), a call from bytecode (exec), and a tail transfer, which
// resets pc to 0 in the frame it replaces. Parameters are written before the
// loop starts, so a call whose argument the parameter's register refuses
// (frame.bad) is not an entry.
//
// internal/irbuild's retained-function probe reads it: a function ran when
// the machine entered its body and the run did not end in a refusal. A
// machine without counting compiles no opEnter
// (TestEntries_AMachineWithoutCountingCompilesNoEnter).
//
// A counting machine also checks, at a call from Go, that each parameter of
// a scalar type receives a value of that type (checkParamKinds). A plain
// machine writes an int64 into a Float or Bool register and anything into a
// Decimal one, so a probe handing a body the wrong scalar would otherwise
// count as an entry.

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// WithEntryCounts makes m count entries into each function it compiles from
// now on. Call it before the machine runs anything: a function compiled
// earlier carries no counter.
func (m *Machine) WithEntryCounts() *Machine {
	m.countEntries = true
	return m
}

// Entries is how many times this machine, or any view of it made by
// WithLimits or Boot, has entered f's body. It is 0 for a function not yet
// compiled, and for every function on a machine without WithEntryCounts.
func (m *Machine) Entries(f *ir.Func) uint64 {
	s, ok := m.codes.slots.Load(f)
	if !ok {
		return 0
	}
	c := s.(*fnSlot).code.Load()
	if c == nil {
		return 0
	}
	return c.entries.Load()
}

// emitEnter is the counter at pc 0 of a function compiled for counting.
func (cp *compiler) emitEnter(f *ir.Func) {
	if !cp.m.countEntries {
		return
	}
	cp.at(f.Pos(), false, -1)
	cp.emit(cp.op(opEnter, 0))
}

// checkParamKinds refuses an argument arriving from Go whose Go type is not
// the one a parameter of a scalar type holds. Only a counting machine
// checks: the probe that counts entries hands bodies made-up arguments, and
// a program's own calls never pass a mistyped scalar.
func checkParamKinds(f *ir.Func, args []any) error {
	params := f.Params()
	for i, a := range args {
		ty := f.TempType(params[i].Temp)
		if ty == nil {
			continue
		}
		ok := true
		switch ty.Kind() {
		case ir.KindBool:
			_, ok = a.(bool)
		case ir.KindInt:
			_, ok = a.(int64)
		case ir.KindFloat:
			_, ok = a.(float64)
		case ir.KindByte:
			_, ok = a.(rt.Byte)
		case ir.KindString:
			_, ok = a.(string)
		case ir.KindBytes:
			_, ok = a.(rt.Bytes)
		case ir.KindDecimal:
			_, ok = a.(rt.Decimal)
		}
		if !ok {
			return fmt.Errorf("vm: %s: parameter %s is %s and received %T",
				f.Name(), params[i].Sym.Name(), ty, a)
		}
	}
	return nil
}
