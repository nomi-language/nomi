package vm

import (
	"fmt"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// The app's Context runs on rt's context chain: a read answers the
// activation's active context and a rebind floors, enters and bounds it on
// the runtime frame, so blocking operations in the extent wait under its
// deadline. A Context is rt.Context itself, which is Opaque and renders
// `<context>`, so the generated adapters for `Context.with_timeout` and `Context.root` take and
// answer the machine's own value.

// A `Type<T>` witness is `rt.Type[any]` over T's `*rt.TypeID`. Its ADDRESS is the
// identity (rt/typewitness.go), so one name has one TypeID for the life of the
// process: the producer names a type by its declaration's qualified runtime
// name (`domain.Token`), and a primitive shares rt's canonical identity.
var typeIDs sync.Map // string -> *rt.TypeID

var primitiveTypeIDs = map[string]*rt.TypeID{
	"Int": &rt.TIDInt, "Float": &rt.TIDFloat, "String": &rt.TIDString, "Bool": &rt.TIDBool,
}

// typeID is the process-wide identity of the type named name.
func typeID(name string) *rt.TypeID {
	if tid, ok := primitiveTypeIDs[name]; ok {
		return tid
	}
	if tid, ok := typeIDs.Load(name); ok {
		return tid.(*rt.TypeID)
	}
	tid, _ := typeIDs.LoadOrStore(name, &rt.TypeID{Nomi: name})
	return tid.(*rt.TypeID)
}

// typeWitness is the `Type<T>` value for the type named name.
func typeWitness(name string) rt.Type[any] { return rt.Type[any]{TID: typeID(name)} }

// contextWithValueHost is `Context.with_value(c, value)`: a child context
// binding value under its type's identity, which the producer passes as a
// third operand, the witness of the argument's static type (rt.ContextWithValue
// says why the key is the call site's and not the value's).
func contextWithValueHost(_ *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("vm: Context.with_value: expected a context, a value and its witness, got %d operand(s)", len(args))
	}
	c, ok := args[0].(rt.Context)
	if !ok {
		return nil, fmt.Errorf("vm: Context.with_value: the receiver is %T, not a Context", args[0])
	}
	w, ok := args[2].(rt.Type[any])
	if !ok {
		return nil, fmt.Errorf("vm: Context.with_value: the key is %T, not a Type witness", args[2])
	}
	return rt.ContextWithValue(c, w.TID, args[1]), nil
}

// contextValueHost is `Context.value(c, value_type)`: the nearest binding for
// the witnessed type, as `Maybe<T>`.
func contextValueHost(_ *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: Context.value: expected a context and a witness, got %d operand(s)", len(args))
	}
	c, ok := args[0].(rt.Context)
	if !ok {
		return nil, fmt.Errorf("vm: Context.value: the receiver is %T, not a Context", args[0])
	}
	w, ok := args[1].(rt.Type[any])
	if !ok {
		return nil, fmt.Errorf("vm: Context.value: the witness is %T, not a Type witness", args[1])
	}
	return maybeOf(rt.ContextValue(c, w)), nil
}

// activeContext reads the Context field: the active execution context, as
// native `rt.ActiveContext(fr)` does.
func activeContext(fr *frame, n *ir.Ref) error {
	fr.write(n.Dst(), rt.ActiveContext(fr.runtime))
	return nil
}

// storeContext rebinds the Context field for the rest of the activation, as
// native appFieldSet does: the stored context is floored by the deadline in
// force, published under the field's name, made the active context, and its
// deadline bounds the frame until the activation's exit releases it.
func storeContext(fr *frame, n *ir.Store) error {
	v, err := fr.read(n.Src())
	if err != nil {
		return err
	}
	c, ok := v.(rt.Context)
	if !ok {
		return fmt.Errorf("vm: %s: %s stores %T, not a Context", fr.fn.Name(), n, v)
	}
	next := rt.ContextWithFloor(rt.ActiveContext(fr.runtime), c)
	runtime := rt.EnterScopedField(fr.runtime, strings.TrimPrefix(n.Sym().Name(), "$"), next)
	runtime = rt.EnterContext(runtime, next)
	runtime, release := rt.EnterDeadline(runtime, next)
	fr.runtime = runtime
	fr.releases = append(fr.releases, release)
	return nil
}
