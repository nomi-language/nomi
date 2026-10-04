package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// A dispatched call selects its body by the declared type of its receiver
// operand. A VM value carries its own type name, and the modules list which retained body
// implements each interface method for each declared type.

// addImpls indexes one module's dispatch entries by method and type name.
func (m *Machine) addImpls(mod *ir.Module) {
	for _, impl := range mod.Impls() {
		if m.dispatch == nil {
			m.dispatch = map[*ir.Symbol]map[string]*ir.Symbol{}
		}
		byType := m.dispatch[impl.Method]
		if byType == nil {
			byType = map[string]*ir.Symbol{}
			m.dispatch[impl.Method] = byType
		}
		byType[impl.Type.Name()] = impl.Func
	}
	for _, impl := range mod.DisplayImpls() {
		if m.displays == nil {
			m.displays = map[string]*ir.Symbol{}
		}
		m.displays[impl.Type.Name()] = impl.Func
	}
	for _, impl := range mod.RowDebugImpls() {
		if m.rowDebugs == nil {
			m.rowDebugs = map[string]*ir.Symbol{}
		}
		m.rowDebugs[impl.Type.Name()] = impl.Func
	}
	for _, impl := range mod.EquatableImpls() {
		if m.equates == nil {
			m.equates = map[string]*ir.Symbol{}
		}
		m.equates[impl.Type.Name()] = impl.Func
	}
	for _, impl := range mod.HashableImpls() {
		if m.hashes == nil {
			m.hashes = map[string]*ir.Symbol{}
		}
		m.hashes[impl.Type.Name()] = impl.Func
	}
}

// rowText is how v reads in an assertion's `values:` row: rt.RowText, with a
// value whose type has a hand-written `impl Debug` rendered by that impl at
// any depth, as `dbg` renders it.
func (m *Machine) rowText(fr *frame, v any) (string, error) {
	if len(m.rowDebugs) == 0 {
		return rt.RowText(v), nil
	}
	return rt.RowTextWith(v, func(x any) (string, bool, error) {
		rec, isRecord := x.(*rt.Record)
		if !isRecord || rec == nil {
			return "", false, nil
		}
		sym := m.rowDebugs[rec.Desc.Name]
		if sym == nil {
			return "", false, nil
		}
		fn, err := m.resolveFunc(sym, fr.fn.Name())
		if err != nil {
			return "", true, err
		}
		out, err := m.callFrom(fr, fn, []any{x})
		if err != nil {
			return "", true, err
		}
		text, ok := out.(string)
		if !ok {
			return "", true, fmt.Errorf("Debug impl %s returned %T, not a String", sym.Name(), out)
		}
		return text, true, nil
	})
}

// displayErased renders a Display existential's value through its own impl:
// a declared type's retained body by the type name the value carries, or a
// scalar's text, which is what each scalar's Display impl answers. Any other
// value is refused: rt.DisplayText's structural text for it is not what its
// Display impl answers (`Bytes` reads `Bytes(2)`, a list renders its
// elements through Display).
func (m *Machine) displayErased(fr *frame, v any) (string, error) {
	switch x := v.(type) {
	case int64, float64, bool, string, rt.Decimal, rt.Byte:
		return rt.DisplayText(x), nil
	case *rt.Record:
		switch x.Desc.Kind {
		case rt.KindStruct, rt.KindEnum, rt.KindDistinct:
			sym := m.displays[x.Desc.Name]
			if sym == nil {
				break
			}
			fn, err := m.resolveFunc(sym, fr.fn.Name())
			if err != nil {
				return "", err
			}
			out, err := m.callFrom(fr, fn, []any{v})
			if err != nil {
				return "", err
			}
			text, ok := out.(string)
			if !ok {
				return "", fmt.Errorf("vm: %s: Display impl %s returned %T, not a String", fr.fn.Name(), sym.Name(), out)
			}
			return text, nil
		}
		return "", fmt.Errorf("vm: %s: Display over an existential holding %s, and this program's modules did not "+
			"retain its Display implementation", fr.fn.Name(), x.Desc.Name)
	}
	return "", fmt.Errorf("vm: %s: Display over an existential holding %T is not implemented", fr.fn.Name(), v)
}

// displayStructural is Display over a value whose type the builder rendered
// structurally (a tuple, an anonymous record, a container): its shape by
// rt.DisplayTextWith, and each nested value by Display — a declared type
// through its retained `impl Display`, anything else structurally again. A
// String inside a tuple is its text, not its literal.
func (m *Machine) displayStructural(fr *frame, v any) (string, error) {
	var failed error
	var elem func(any) string
	elem = func(x any) string {
		if failed != nil {
			return ""
		}
		if r, ok := x.(*rt.Record); ok {
			if sym := m.displays[r.Desc.Name]; sym != nil {
				s, err := m.displayErased(fr, x)
				if err != nil {
					failed = err
				}
				return s
			}
		}
		return rt.DisplayTextWith(x, elem)
	}
	s := rt.DisplayTextWith(v, elem)
	return s, failed
}

// dispatchTarget selects a dispatched call's body by the declared type of its
// receiver operand.
func (m *Machine) dispatchTarget(fr *frame, n *ir.Call, args []any) (*ir.Func, error) {
	typ := runtimeTypeName(args[n.KeyAt()])
	impl := m.dispatch[n.Callee()][typ]
	if impl == nil {
		return nil, fmt.Errorf("vm: %s: %s dispatches on %s, and this program's modules did not "+
			"retain an implementation for it", fr.fn.Name(), n.Callee().Name(), typ)
	}
	return m.resolveFunc(impl, fr.fn.Name())
}
