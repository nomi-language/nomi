package vm

import (
	"fmt"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

type onceCell struct {
	body  *ir.Func
	value *rt.OnceCell[any]
	// forced holds the value once a force has returned it, so a read after
	// the first is a load: the bytecode's opOnce reads it without the
	// handler's recover and the cell's lineage walk. rt's cell stays the one
	// that decides the value and detects cycles.
	forced atomic.Pointer[any]
}

func (m *Machine) initOnceCells(entry *ir.Module, rest []*ir.Module) {
	m.onces = map[*ir.Symbol]*onceCell{}
	seen := map[*ir.Module]bool{}
	for _, mod := range append([]*ir.Module{entry}, rest...) {
		if mod == nil || seen[mod] {
			continue
		}
		seen[mod] = true
		for _, cell := range mod.Cells() {
			body := cell.Initializer()
			if body == nil {
				continue
			}
			if _, duplicate := m.onces[cell.Sym()]; duplicate {
				m.onces[cell.Sym()] = nil
				continue
			}
			m.onces[cell.Sym()] = &onceCell{body: body, value: rt.NewOnceCell[any](cell.Sym().Name())}
		}
	}
}

// A failed initializer must unwind through OnceCell.Get without caching a value.
type onceFailure struct{ err error }

func (m *Machine) forceOnce(fr *frame, n *ir.Ref) (err error) {
	cell, found := m.onces[n.Sym()]
	if !found {
		return fmt.Errorf("vm: %s: once %s has no retained initializer", fr.fn.Name(), n.Sym().Name())
	}
	if cell == nil {
		return fmt.Errorf("vm: once %s has multiple declarations", n.Sym().Name())
	}
	defer func() {
		if failure := recover(); failure != nil {
			switch failure := failure.(type) {
			case onceFailure:
				err = failure.err
			case *rt.Error:
				// rt's cycle trap: the program's fault, as an rt trap
				// anywhere else in an activation is.
				err = &Fault{err: failure}
			default:
				panic(failure)
			}
		}
	}()
	v := cell.value.Get(fr.runtime, func(runtime *rt.Frame) any {
		v, err := m.activate(cell.body, nil, nil, runtime, false, fr.depth, nil)
		if err != nil {
			panic(onceFailure{err})
		}
		return v
	})
	cell.forced.Store(&v)
	fr.write(n.Dst(), v)
	return nil
}
