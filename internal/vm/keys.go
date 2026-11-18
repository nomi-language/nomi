package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// KEYS. A Map key, a Set element and `==` over a container compare and hash
// a value of a declared type through that type's own `Equatable` and
// `Hashable` impls, at any depth, which is what `==` on the value does
// (spec §Custom equality via `impl Equatable`). The modules record each
// hand-written body (ir.Module.ImplementEquatable, ImplementHashable); a
// derived impl is structural and is what rt answers without one. So:
//
//   - equality: a hand-written `equal` decides; otherwise structural, with
//     each nested value answered the same way.
//   - hashing: a hand-written `hash` decides; a type whose `equal` is
//     hand-written and whose Hashable is not hashes by its type name alone,
//     so two values its `equal` calls equal always share a bucket and the
//     `equal` decides between them; otherwise structural.
//
// rt's kernels take no error, so a fault inside a body unwinds to the
// operation that asked as a keyFailure and is returned from there as the
// fault it was (recoverKey).

// keyFailure carries a fault an Equatable or Hashable body raised out through
// rt's key kernels.
type keyFailure struct{ err error }

// keyCaller calls the recorded bodies for one operation: from an instruction
// on its activation fr, or from a host on the bound machine's host frame.
type keyCaller struct {
	m    *Machine
	fr   *frame
	keys rt.Keys
}

// keysFor is the rt.Keys an operation hashes and compares with, or nil when
// no linked module records a hand-written Equatable or Hashable, which is
// rt's structural path with nothing allocated. fr is the asking activation,
// or nil in a host.
func (m *Machine) keysFor(fr *frame) *rt.Keys {
	if m == nil || (len(m.equates) == 0 && len(m.hashes) == 0) {
		return nil
	}
	kc := &keyCaller{m: m, fr: fr}
	kc.keys.Hooks = kc
	return &kc.keys
}

// keysOver is keysFor when v, a key or an element about to be hashed or
// compared, could reach a declared record, and nil for a scalar, whose
// answer no impl changes.
func (m *Machine) keysOver(fr *frame, v any) *rt.Keys {
	switch v.(type) {
	case int64, *rt.BigInt, float64, bool, string, rt.Decimal, rt.Byte, rt.Bytes, rt.Unit:
		return nil
	}
	return m.keysFor(fr)
}

// runsKeyBodies reports whether executing in, an instruction of f, may hash
// or compare through keysFor and so run a recorded Equatable or Hashable
// body. That takes an operation that hashes or compares with keys: `==`
// over a container, variant, struct or interface value (compare), a Map or
// Set literal (records.go), the iteration stages that build or group by
// keys (iter.go), or a crossing into Go, whose host may (maphosts.go,
// sethosts.go). And, as keysOver answers at run time, it takes an operand
// that may hold a value of a declared type (mayHoldKeyed): a body runs only
// on a value of the type that declares it. The reachability walk (reach.go)
// asks it, so a refinement's rule that compares Ints, or passes an Int and
// a `fn (Int) -> Bool` to a host, reaches no `equal` of the program's.
func runsKeyBodies(f *ir.Func, in ir.Instr) bool {
	switch n := in.(type) {
	case *ir.Compare:
		if n.Ranked() {
			return false
		}
		switch n.Shape() {
		case ir.ValContainer, ir.ValVariant, ir.ValStruct, ir.ValIface:
		default:
			return false
		}
	case *ir.Make:
		if n.Kind() != ir.MakeMap && n.Kind() != ir.MakeSet {
			return false
		}
	case *ir.Iter:
		switch n.Op() {
		case ir.IterGroupBy, ir.IterToSet, ir.IterToMap, ir.IterChunkBy:
		default:
			return false
		}
	case *ir.Call:
		if !n.Crosses() {
			return false
		}
	default:
		return false
	}
	for _, t := range in.AppendUses(nil) {
		if mayHoldKeyed(f.TempType(t)) {
			return true
		}
	}
	return false
}

// mayHoldKeyed reports whether a value of t may be, or hold, a value of a
// declared type, whose Equatable or Hashable a module may write by hand: a
// struct, an enum, a distinct, an interface value, an erased or opaque
// value, or a composite or function over one (a function's result may be
// hashed, as group_by's key is). A nil type (none stated) may.
func mayHoldKeyed(t *ir.ValType) bool {
	if t == nil {
		return true
	}
	switch t.Kind() {
	case ir.KindTuple, ir.KindRecord, ir.KindList, ir.KindSet, ir.KindVector, ir.KindMap,
		ir.KindRange, ir.KindSeq, ir.KindFunc:
		for i := range t.NumElems() {
			if mayHoldKeyed(t.Elem(i)) {
				return true
			}
		}
		return t.Kind() == ir.KindFunc && t.Result() != nil && mayHoldKeyed(t.Result())
	case ir.KindNever:
		return false
	}
	return !scalarType(t)
}

func (kc *keyCaller) call(sym *ir.Symbol, args []any) any {
	owner := "a key kernel"
	if kc.fr != nil {
		owner = kc.fr.fn.Name()
	}
	f, err := kc.m.resolveFunc(sym, owner)
	if err != nil {
		panic(keyFailure{err})
	}
	var out any
	if kc.fr != nil {
		out, err = kc.m.callFrom(kc.fr, f, args)
	} else {
		out, err = kc.m.call(f, args)
	}
	if err != nil {
		panic(keyFailure{err})
	}
	return out
}

// EqualRecord implements rt.KeyHooks. Two boxes of one name compare by the
// impl recorded under that name (an `embeds` enum's), over the values
// inside.
func (kc *keyCaller) EqualRecord(a, b *rt.Record) (bool, bool) {
	sym := kc.m.equates[a.Desc.Name]
	if sym == nil || a.Desc.Name != b.Desc.Name {
		return false, false
	}
	out := kc.call(sym, []any{rt.Unbox(a), rt.Unbox(b)})
	eq, ok := asBool(out)
	if !ok {
		panic(keyFailure{fmt.Errorf("vm: Equatable impl %s returned %T, not a Bool", sym.Name(), out)})
	}
	return eq, true
}

// HashRecord implements rt.KeyHooks. A box hashes by the impl recorded
// under its name, over the value inside.
func (kc *keyCaller) HashRecord(r *rt.Record) (uint64, bool) {
	if sym := kc.m.hashes[r.Desc.Name]; sym != nil {
		out := kc.call(sym, []any{rt.Unbox(r)})
		if !rt.IsInt(out) {
			panic(keyFailure{fmt.Errorf("vm: Hashable impl %s returned %T, not an Int", sym.Name(), out)})
		}
		return rt.HashIntValue(out), true
	}
	if kc.m.equates[r.Desc.Name] != nil {
		return rt.HashString(r.Desc.Name), true
	}
	return 0, false
}

// hashValueHost is `vm.hash_value`, `Hashable.hash` of an interface value:
// the hash a Map key or Set element of interface type takes, so two values
// `==` calls equal hash alike.
func hashValueHost(mach *Machine, _ ir.Pos, args []any) (_ any, err error) {
	defer recoverKey(&err)
	if len(args) != 1 {
		return nil, fmt.Errorf("vm: vm.hash_value: expected 1 operand, got %d", len(args))
	}
	return int64(mach.keysOver(nil, args[0]).HashFunc()(args[0])), nil
}

// recoverKey turns a keyFailure unwinding out of a key kernel into the error
// it carries. Deferred by each operation that hashes or compares through
// keysFor: `defer recoverKey(&err)`.
func recoverKey(err *error) {
	if p := recover(); p != nil {
		if failure, ok := p.(keyFailure); ok {
			*err = failure.err
			return
		}
		panic(p)
	}
}

// Keyed map operations. k is keysFor's answer, nil for rt's structural path.

func mapOf(k *rt.Keys, entries []rt.MapEntry[any, any]) vmap {
	return rt.MapOf(k.HashFunc(), k.EqualFunc(), entries)
}

func mapPut(k *rt.Keys, m vmap, key, v any) vmap {
	return rt.MapPut(m, k.HashFunc(), k.EqualFunc(), key, v)
}

func mapLookup(k *rt.Keys, m vmap, key any) (any, bool) {
	return rt.MapLookup(m, k.HashFunc(), k.EqualFunc(), key)
}

func mapRemove(k *rt.Keys, m vmap, key any) vmap {
	return rt.MapRemove(m, k.HashFunc(), k.EqualFunc(), key)
}
