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
