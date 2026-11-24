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
	for _, method := range mod.UnretainedImpls() {
		if m.unretainedImpls == nil {
			m.unretainedImpls = map[*ir.Symbol]bool{}
		}
		m.unretainedImpls[method] = true
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

// rowText is how v reads in a failed test's report, an assertion's `values:`
// row, a subtest's row and what ended a case early (returnedText):
// rt.RowText, with a value whose type has a hand-written `impl Debug`
// rendered by that impl at any depth, as `dbg` renders it. The impl is
// found and called as a Debug rendering's held impl is (renderHook): a box
// selects the impl recorded under its name and the impl receives the value
// inside. The impl runs as a callee of fr.
func (m *Machine) rowText(fr *frame, v any) (string, error) {
	if len(m.rowDebugs) == 0 {
		return rt.RowText(v), nil
	}
	return rt.RowTextWith(v, m.renderHook(fr, nil, m.rowDebugs, nil))
}

// displayErased renders a Display existential's value through its own impl:
// a declared type's retained body by the type name the value carries (a
// record's descriptor, a host handle's TypeName, rt.BytesTypeName for a
// Bytes), or a scalar's text, which is what each scalar's Display impl
// answers. A declared type with no retained body is one whose Display is
// derived, which renders by its shape, and so do a tuple, an anonymous
// record and a container (displayShape). Any other value is refused:
// rt.DisplayShapeText's text for it is not what its Display impl
// answers (`Bytes` reads `Bytes(2)`).
func (m *Machine) displayErased(fr *frame, v any) (string, error) {
	if r, boxed := v.(*rt.Record); boxed && r != nil && r.Desc.Kind == rt.KindBoxed {
		// A box names the static type it was held at: an `embeds` enum's
		// value renders through the enum's impl, recorded under that name,
		// and not the impl of the struct it embeds.
		v = rt.Unbox(v)
		if sym := m.displays[r.Desc.Name]; sym != nil {
			return m.displayCall(fr, sym, v)
		}
	}
	switch x := v.(type) {
	case int64, *rt.BigInt, float64, bool, string, rt.Decimal, rt.Byte:
		return rt.DisplayShapeText(x, nil)
	case *rt.Record:
		switch x.Desc.Kind {
		case rt.KindStruct, rt.KindEnum, rt.KindDistinct:
			if sym := implOf(m.displays, x); sym != nil {
				return m.displayCall(fr, sym, v)
			}
			return m.displayShape(fr, v, nil, nil)
		case rt.KindTuple, rt.KindAnon:
			return m.displayShape(fr, v, nil, nil)
		}
		return "", fmt.Errorf("vm: %s: Display over an existential holding %s, and this program's modules did not "+
			"retain its Display implementation", fr.fn.Name(), x.Desc.Name)
	case *list, rt.Vector[any], vmap:
		return m.displayShape(fr, v, nil, nil)
	case rt.Bytes:
		// A std type whose value is not a record names its impl by the
		// type it always is.
		return m.displayNamed(fr, rt.BytesTypeName, v)
	case rt.HostHandle:
		// A host value (a Regex) carries its type's name.
		return m.displayNamed(fr, x.TypeName, v)
	}
	return "", fmt.Errorf("vm: %s: Display over an existential holding %T is not implemented", fr.fn.Name(), v)
}

// displayNamed renders v through the recorded Display body of the type
// named name.
func (m *Machine) displayNamed(fr *frame, name string, v any) (string, error) {
	sym := m.displays[name]
	if sym == nil {
		return "", fmt.Errorf("vm: %s: Display over an existential holding %s, and this program's modules did not "+
			"retain its Display implementation", fr.fn.Name(), name)
	}
	return m.displayCall(fr, sym, v)
}

// displayCall renders v through the recorded Display body sym.
func (m *Machine) displayCall(fr *frame, sym *ir.Symbol, v any) (string, error) {
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

// displayShape is Display over a value the machine renders by its shape
// (rt.DisplayShapeText): a tuple, an anonymous record, a container, and a
// declared type whose Display is derived. A String inside is its text, not
// its literal. A nested value renders through the body impls names for the
// static type of its position below static (renderHook), the one the
// builder read from the record of the part's Display, else through the
// body the machine holds for its type (displays), as an interface value's
// held value does, else by its shape again. static is nil for a value whose
// static type the machine does not know (an interface value's).
func (m *Machine) displayShape(fr *frame, v any, impls []ir.PartImpl, static *ir.ValType) (string, error) {
	return rt.DisplayShapeText(v, m.renderHook(fr, impls, m.displays, static))
}

// implOf is the body a table of impls keyed by type name holds for record
// r: an instance of a generic type's own body, recorded under its instance
// key (rt.TypeDesc.Inst, `main.Box<Int>`), else its declaration's.
func implOf(byType map[string]*ir.Symbol, r *rt.Record) *ir.Symbol {
	if r.Desc.Inst != "" {
		if sym := byType[r.Desc.Inst]; sym != nil {
			return sym
		}
	}
	return byType[r.Desc.Name]
}

// DispatchMiss is a dispatched call whose receiver's run-time type has no
// impl among the program's retained modules.
type DispatchMiss struct {
	// Func is the function making the call, Callee the interface function.
	Func, Callee string
	// Received is the receiver's run-time type name, as the message prints
	// it.
	Received string
	// Declared is the stored type of the receiver operand at the call, or
	// nil for an untyped one.
	Declared *ir.ValType
	receiver any
}

func (e *DispatchMiss) Error() string {
	return fmt.Sprintf("vm: %s: %s dispatches on %s, and this program's modules did not "+
		"retain an implementation for it", e.Func, e.Callee, e.Received)
}

// ReceiverOfDeclaredType reports whether the receiver is a value of the
// operand's declared type, and whether that can be decided at all. It can
// for a scalar and for a struct, enum, tuple or distinct, compared by the
// type's run-time name. It cannot for an interface, an untyped operand or
// any other type, whose values are many types at run time.
//
// A receiver of its declared type whose type has no retained impl is a
// builder gap: the impl was not retained. A receiver of another type is a
// value the operand's type does not admit, which only a caller handing the
// function a made-up argument produces.
func (e *DispatchMiss) ReceiverOfDeclaredType() (of, decided bool) {
	t := e.Declared
	if t == nil {
		return false, false
	}
	v := e.receiver
	switch t.Kind() {
	case ir.KindBool:
		_, of = v.(bool)
	case ir.KindInt:
		of = rt.IsInt(v)
	case ir.KindFloat:
		_, of = v.(float64)
	case ir.KindByte:
		_, of = v.(rt.Byte)
	case ir.KindString:
		_, of = v.(string)
	case ir.KindBytes:
		_, of = v.(rt.Bytes)
	case ir.KindDecimal:
		_, of = v.(rt.Decimal)
	case ir.KindStruct, ir.KindEnum, ir.KindTuple, ir.KindDistinct:
		d := descOfType(t)
		if d == nil {
			return false, false
		}
		r, isRecord := v.(*rt.Record)
		of = isRecord && r != nil && r.Desc.Name == d.Name && r.Desc.Inst == d.Inst
	default:
		return false, false
	}
	return of, true
}

// dispatchTarget selects a dispatched call's body by the declared type of its
// receiver operand.
func (m *Machine) dispatchTarget(fr *frame, n *ir.Call, args []any) (*ir.Func, error) {
	recv := args[n.KeyAt()]
	typ := runtimeTypeName(recv)
	var impl *ir.Symbol
	if r, isRecord := recv.(*rt.Record); isRecord && r != nil {
		impl = implOf(m.dispatch[n.Callee()], r)
	} else {
		impl = m.dispatch[n.Callee()][typ]
	}
	if impl == nil {
		if r, isRecord := recv.(*rt.Record); isRecord && r != nil && r.Desc.Kind == rt.KindBoxed {
			typ = r.Desc.Name
		}
		miss := &DispatchMiss{Func: fr.fn.Name(), Callee: n.Callee().Name(), Received: typ, receiver: recv}
		if k := n.KeyAt(); k < n.NumArgs() {
			miss.Declared = fr.fn.TempType(n.Arg(k))
		}
		return nil, miss
	}
	fn, err := m.resolveFunc(impl, fr.fn.Name())
	if err != nil {
		return nil, err
	}
	if rt.Boxed(recv) {
		unboxSelves(fn, args)
	}
	return fn, nil
}

// unboxSelves opens the boxed existentials a dispatch selected fn for
// (rt.Box): each operand fn takes at a concrete type, the receiver among
// them, receives the value inside its box. An operand fn takes as an
// existential or an erased type parameter keeps its box, which a dispatch
// inside fn selects by.
func unboxSelves(fn *ir.Func, args []any) {
	params := fn.Params()
	for i, a := range args {
		if i >= len(params) || !rt.Boxed(a) {
			continue
		}
		if t := fn.TempType(params[i].Temp); t != nil && (t.Kind() == ir.KindIface || t.Kind() == ir.KindAny) {
			continue
		}
		args[i] = rt.Unbox(a)
	}
}
