package vm

// `dbg` IN THE VM, AND THE TWO THINGS THAT ARE GENUINELY THIS ENGINE'S.
//
// THE LAYOUT IS NOT WRITTEN HERE. The layout and the colour rule are
// `rt.DbgText`'s, called with this machine's writer, which leaves ONE
// expression of the three shapes. A second expression could diverge
// unnoticed, and `rt/grapheme.go`'s header says why in general — "two
// implementations of a boundary rule that agree on the inputs a fixture names
// is a divergence waiting for the input it does not".
//
// # WHAT IS THIS ENGINE'S: THE WRITER THE COLOUR DECISION IS MADE ABOUT
//
// `rt.ColorEnabledFor` ends in `w.(*os.File)` plus a character-device test.
// `m.out` is a `*syncWriter`, whose `Destination` is the writer `New` was
// handed, and rt asks its question of that (rt.Destination), so a machine
// over `os.Stdout` colours `dbg` as `nomi run` does. The WRITE still goes
// through the wrapper:
//
//	the colour question  the DESTINATION, because that is what has a terminal
//	the bytes            the WRAPPER, because that is what serializes
//
// `dbg_test.go`'s TestVMDbg_ColourFollowsTheDestination reads both, with
// `/dev/null` as the character device that makes the difference observable
// without a terminal.
//
// # THE DEBUG RENDERING IS rt's
//
// `rt.DebugText` renders a value as the literal that builds it: scalars,
// collections, tuples, records, structs, enums and distincts. Before it
// does, it asks this machine's hook, which calls the Debug impl the
// `ir.Render` names for a nominal value's type (`debugImplHook`). A String is rt.StringLiteral, the literal
// `String.to_literal` writes, which the Row discipline (`rt.RowText`) writes
// too; internal/ir/render.go records where Row and Debug disagree, and
// rt/render_test.go pins each.

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// dbgKey is the printed name `internal/irbuild` interns `dbg`'s crossing under.
//
// DUPLICATED AS A STRING AND NOT IMPORTED, which is what `hosts`'s own comment
// already says about `io.print`: the map is "keyed on the callee's printed
// name", and WHICH Go function a crossing lands on "is a fact about this
// engine". `internal/vm` may not import `internal/irbuild` at all —
// `TestVM_ReadsTheIRAndNothingElse` fails if it does — so the agreement is
// checked by a test that runs a real lowering rather than by a shared constant.
const dbgKey = "dbg"

// dbgHost is `dbg`'s crossing: print, and answer Unit.
//
// TWO OPERANDS AND THE LINE OFF THE POSITION, which is the split `irdbg.go`
// argues for at the producer: the SOURCE TEXT cannot be re-derived by either
// consumer so it is an `ir.Const` operand, and the LINE is already on every
// node and mandatory, so a second copy could only disagree with the first.
//
// THE RESULT IS UNIT AND THE `dbg`'s VALUE IS NOT THIS CALL'S. `dbg` is
// transparent, and the producer answers it with the OPERAND's temporary —
// this destination is read by nothing, which is what `irCallDiscarded` reports
// on the other side.
func dbgHost(m *Machine, pos ir.Pos, args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: dbg: %d operand(s); the producer builds the "+
			"source text and the rendering and takes the line off the position",
			len(args))
	}
	src, isString := args[0].(string)
	if !isString {
		return nil, fmt.Errorf("vm: dbg: operand 0 is the operand's SOURCE TEXT and "+
			"arrived as %T; the producer builds an ir.Const String for it", args[0])
	}
	rendered, isString := args[1].(string)
	if !isString {
		return nil, fmt.Errorf("vm: dbg: operand 1 is the Debug RENDERING and arrived "+
			"as %T; the producer emits an ir.Render before the call", args[1])
	}
	// THE COLOUR DECISION IS MADE ABOUT THE DESTINATION AND THE BYTES GO
	// THROUGH THE WRAPPER, which is why `rt` answers the TEXT rather than
	// performing the write. rt asks about `m.out`'s Destination.
	//
	// ONE `Write`, which is the property `rt/dbg.go`'s "One Write" section
	// records and the reason a multi-line `dbg` cannot interleave here: the
	// text is composed first and `m.out` is the machine's single mutex, so
	// this print serializes against every `io.print` on the same machine.
	//
	// Inside a test case the line goes to the case's own output, which the
	// report prints under the case (rt.CaptureDbg).
	if rt.CaptureDbg(m.hostFrame, func() string { return rt.DbgReportText(pos.Line(), src, rendered) }) {
		return rt.Unit{}, nil
	}
	fmt.Fprint(m.out, rt.DbgText(m.out, pos.Line(), src, rendered))
	return rt.Unit{}, nil
}

// debugImplHook is renderHook for a Debug rendering. On an extended machine
// (Extend, the REPL's) a value none of the rendering's impls owns renders
// through the hand-written `impl Debug` the machine holds for its type
// (rowDebugs): the rendering was compiled by an earlier input, before a
// later one wrote that impl, and named no body for a type whose Debug was
// then its shape. A derived Debug renders as the shape does. A machine no
// later lowering extended renders as the rendering says.
func (m *Machine) debugImplHook(fr *frame, n *ir.Render) rt.DebugHook {
	var held map[string]*ir.Symbol
	if m.extended && len(m.rowDebugs) > 0 {
		held = m.rowDebugs
	}
	return m.renderHook(fr, n.PartImpls(), held, fr.fn.TempType(n.Src()))
}

// renderHook calls the impls a rendering by shape names for the struct,
// enum, marker, Bytes and host-handle values nested in its operand,
// preserving the caller's runtime frame. An impl is named by a type's name
// and instance key, a host handle's by its TypeName and a Bytes by
// rt.BytesTypeName. static is the operand's static type: each part's
// position below it has a static type too (partHook.Part), and where that
// type is a declared one the rendering names a body for, the part renders
// through that body, whatever the value's run-time type: a value of an
// `embeds` enum is the embedded value at run time, and only its position
// says it is the enum's. Where the position states no declared type (an
// interface value, an erased one) the value's run-time type selects the
// body. A value none of impls owns renders through the body held names for
// its type (implOf), if any, else by its shape.
func (m *Machine) renderHook(fr *frame, impls []ir.PartImpl, held map[string]*ir.Symbol, static *ir.ValType) rt.DebugHook {
	if len(impls) == 0 && len(held) == 0 {
		return nil
	}
	// Two instances of one generic type share the name, and each has its
	// own body. A value whose descriptor states no instance takes the
	// name's one body, when it has exactly one.
	t := &partTable{byType: make(map[renderKey]*ir.Symbol, len(impls)), byName: make(map[string]*ir.Symbol, len(impls)),
		bodies: make(map[string]int, len(impls)), held: held}
	for _, impl := range impls {
		k := renderKey{impl.Type, impl.Inst}
		if _, dup := t.byType[k]; !dup {
			t.bodies[impl.Type]++
		}
		t.byType[k] = impl.Fn
		t.byName[impl.Type] = impl.Fn
	}
	return partHook{m: m, fr: fr, table: t, static: static}
}

// renderKey names an impl a rendering carries: a type's name and instance key.
type renderKey struct{ name, inst string }

// partTable is the impls one rendering carries, and the bodies held names
// by type.
type partTable struct {
	byType map[renderKey]*ir.Symbol
	byName map[string]*ir.Symbol
	bodies map[string]int
	held   map[string]*ir.Symbol
}

// lookup is the body the table names for a value of the type name, at the
// instance inst, if any.
func (t *partTable) lookup(name, inst string) *ir.Symbol {
	if sym := t.byType[renderKey{name, inst}]; sym != nil {
		return sym
	}
	if t.bodies[name] == 1 {
		return t.byName[name]
	}
	return nil
}

// partHook is renderHook's hook at one position of the rendered value,
// whose static type is static (nil where it states none).
type partHook struct {
	m      *Machine
	fr     *frame
	table  *partTable
	static *ir.ValType
}

// Part implements rt.DebugHook: the hook at the position step below this
// one, whose static type is the part of this one's that step names.
func (h partHook) Part(step rt.Step) rt.DebugHook {
	h.static = staticPart(h.static, step)
	return h
}

// Render implements rt.DebugHook.
func (h partHook) Render(v any) (string, bool, error) {
	var typeName, inst string
	switch x := v.(type) {
	case *rt.Record:
		if x == nil {
			return "", false, nil
		}
		switch x.Desc.Kind {
		case rt.KindStruct, rt.KindEnum, rt.KindDistinct:
		case rt.KindBoxed:
			// A box names the static type its value had where it became
			// an interface value; an impl recorded under that name takes
			// the value inside.
			typeName = x.Desc.Name
			v = rt.Unbox(x)
		default:
			return "", false, nil
		}
		if x.Desc.Kind != rt.KindBoxed {
			typeName, inst = x.Desc.Name, x.Desc.Inst
		}
	case rt.HostHandle:
		typeName = x.TypeName
	case rt.Bytes:
		typeName = rt.BytesTypeName
	default:
		return "", false, nil
	}
	var sym *ir.Symbol
	if s := h.static; s != nil && s.Sym() != nil && (s.Kind() == ir.KindStruct || s.Kind() == ir.KindEnum || s.Kind() == ir.KindDistinct) {
		// The position's declared type names the body.
		sym = h.table.lookup(s.Sym().Name(), s.InstanceKey())
	}
	if sym == nil {
		sym = h.table.lookup(typeName, inst)
	}
	if sym == nil && h.table.held != nil {
		if inst != "" {
			sym = h.table.held[inst]
		}
		if sym == nil {
			sym = h.table.held[typeName]
		}
	}
	if sym == nil {
		return "", false, nil
	}
	m, fr := h.m, h.fr
	fn, err := m.resolveFunc(sym, fr.fn.Name())
	if err != nil {
		return "", true, err
	}
	if fn == fr.fn {
		// The rendering is the body of the impl it found: one that renders
		// its value by shape (a box's `Display.to_string` of a type whose
		// Display is derived, stdProgThunk), which renders the value so
		// rather than call itself.
		return "", false, nil
	}
	out, err := m.callFrom(fr, fn, []any{v})
	if err != nil {
		return "", true, err
	}
	text, ok := out.(string)
	if !ok {
		return "", true, fmt.Errorf("impl %s returned %T, not a String", sym.Name(), out)
	}
	return text, true, nil
}

// staticPart is the static type of the part step names of a value of
// static type t, or nil where t states none: a container's element, key
// or value, a tuple's slot, a struct's, record's or variant's field, a
// variant's payload, a distinct's inner value.
func staticPart(t *ir.ValType, step rt.Step) *ir.ValType {
	if t == nil {
		return nil
	}
	elem := func(i int) *ir.ValType {
		if i < t.NumElems() {
			return t.Elem(i)
		}
		return nil
	}
	field := func(fs []ir.Field, name string) *ir.ValType {
		for _, f := range fs {
			if f.Name == name {
				return f.Type
			}
		}
		return nil
	}
	switch step.Kind {
	case rt.StepElem:
		switch t.Kind() {
		case ir.KindList, ir.KindVector, ir.KindSet:
			return elem(0)
		}
	case rt.StepKey, rt.StepValue:
		if t.Kind() == ir.KindMap {
			if step.Kind == rt.StepKey {
				return elem(0)
			}
			return elem(1)
		}
	case rt.StepSlot:
		switch t.Kind() {
		case ir.KindTuple:
			return elem(step.Index)
		}
	case rt.StepField:
		switch t.Kind() {
		case ir.KindStruct:
			if l := t.Layout(); l != nil {
				return field(l.Fields, step.Name)
			}
		case ir.KindRecord:
			for i, n := range t.Names() {
				if n == step.Name {
					return elem(i)
				}
			}
		}
	case rt.StepPayload:
		l := t.Layout()
		if t.Kind() != ir.KindEnum || l == nil {
			return nil
		}
		for _, v := range l.Variants {
			if rt.ShortTypeName(v.Name) != rt.ShortTypeName(step.Name) {
				continue
			}
			if step.Field != "" {
				return field(v.Fields, step.Field)
			}
			if len(v.Fields) == 1 {
				return v.Fields[0].Type
			}
		}
	case rt.StepInner:
		if t.Kind() == ir.KindDistinct {
			return elem(0)
		}
	}
	return nil
}
