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
// `m.out` is a `*syncWriter`, so asking about it answers FALSE for every
// destination including a terminal — a machine over `os.Stdout` would print
// `dbg` uncoloured where `nomi run` colours it. So the decision is made about
// `m.out.target()`, the writer `New` was handed, and the WRITE still goes
// through the wrapper. Two writers in one call, deliberately:
//
//	the colour question  the DESTINATION, because that is what has a terminal
//	the bytes            the WRAPPER, because that is what serializes
//
// `writer_test.go` reads both, with `/dev/null` as the character device that
// makes the difference observable without a terminal.
//
// # THE DEBUG RENDERING IS rt's
//
// `rt.DebugText` renders scalars, collections, tuples, anonymous records,
// distincts, Bool and the prelude's carrying enums structurally, and refuses
// by name what only a dispatched impl can render (a named struct, a user
// enum). Before it does either, it asks this machine's hook, which calls the
// Debug impl the `ir.Render` names for a nominal value's type
// (`debugImplHook`). A String is rt.DebugString: two escapes over grapheme
// clusters, which is where the Row discipline (`rt.RowText`, no escaping)
// disagrees with Debug; internal/ir/render.go records that disagreement and
// five others, and rt/render_test.go pins all six.

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
	// performing the write. `ColorEnabledFor` ends in `w.(*os.File)`, so
	// asking it about `m.out` — a `*syncWriter` — answers false for every
	// destination including a terminal.
	//
	// ONE `Write`, which is the property `rt/dbg.go`'s "One Write" section
	// records and the reason a multi-line `dbg` cannot interleave here: the
	// text is composed first and `m.out` is the machine's single mutex, so
	// this print serializes against every `io.print` on the same machine.
	fmt.Fprint(m.out, rt.DbgText(m.out.target(), pos.Line(), src, rendered))
	return rt.Unit{}, nil
}

// debugImplHook calls the Debug impls a rendering names for the struct, enum,
// marker and host-handle values nested in its operand, preserving the caller's
// runtime frame. A host handle is keyed by its TypeName, which is the same
// runtime type name (`regex.Regex`) the builder names the impl under.
func (m *Machine) debugImplHook(fr *frame, n *ir.Render) rt.DebugHook {
	impls := n.DebugImpls()
	if len(impls) == 0 {
		return nil
	}
	// An impl is selected by the value's runtime type name and instance key:
	// two instances of one generic type share the name, and each has its own
	// body. A value whose descriptor states no instance takes the name's one
	// body, when it has exactly one.
	type key struct{ name, inst string }
	byType := make(map[key]*ir.Symbol, len(impls))
	byName := make(map[string]*ir.Symbol, len(impls))
	bodies := make(map[string]int, len(impls))
	for _, impl := range impls {
		k := key{impl.Type, impl.Inst}
		if _, dup := byType[k]; !dup {
			bodies[impl.Type]++
		}
		byType[k] = impl.Fn
		byName[impl.Type] = impl.Fn
	}
	return func(v any) (string, bool, error) {
		var typeName, inst string
		switch x := v.(type) {
		case *rt.Record:
			if x == nil {
				return "", false, nil
			}
			switch x.Desc.Kind {
			case rt.KindStruct, rt.KindEnum, rt.KindDistinct:
			default:
				return "", false, nil
			}
			typeName, inst = x.Desc.Name, x.Desc.Inst
		case rt.HostHandle:
			typeName = x.TypeName
		default:
			return "", false, nil
		}
		sym, owned := byType[key{typeName, inst}]
		if !owned && bodies[typeName] == 1 {
			sym, owned = byName[typeName]
		}
		if !owned {
			return "", false, nil
		}
		fn, err := m.resolveFunc(sym, fr.fn.Name())
		if err != nil {
			return "", true, err
		}
		out, err := m.callFrom(fr, fn, []any{v})
		if err != nil {
			return "", true, err
		}
		text, ok := out.(string)
		if !ok {
			return "", true, fmt.Errorf("Debug impl %s returned %T, not a String", sym.Name(), out)
		}
		return text, true, nil
	}
}
