package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/ir"
)

// mapHost is `Map.get`, `Map.put` and `Map.size` over rt's persistent map,
// hashed and compared by the machine's key kernels (keys.go).
func mapHost(key string) hostFn {
	return func(mach *Machine, _ ir.Pos, args []any) (_ any, err error) {
		defer recoverKey(&err)
		arity := 1
		switch key {
		case "Map.get", "Map.remove", "Map.contains?", "Map.merge", "Map.map_values", "Map.map_keys":
			arity = 2
		case "Map.put":
			arity = 3
		case "Map.size", "Map.keys", "Map.values", "Map.map_next":
		default:
			return nil, fmt.Errorf("vm: unsupported map host %s", key)
		}
		if len(args) != arity {
			return nil, fmt.Errorf("vm: %s: expected %d operands, got %d", key, arity, len(args))
		}
		m, ok := args[0].(vmap)
		if !ok {
			return nil, fmt.Errorf("vm: %s: operand 1 is %T, want Map", key, args[0])
		}
		var k *rt.Keys
		if len(args) > 1 {
			k = mach.keysOver(nil, args[1])
		}
		switch key {
		case "Map.get":
			if v, found := mapLookup(k, m, args[1]); found {
				return some(v), nil
			}
			return noneValue, nil
		case "Map.put":
			return mapPut(k, m, args[1], args[2]), nil
		case "Map.remove":
			return mapRemove(k, m, args[1]), nil
		case "Map.contains?":
			_, found := mapLookup(k, m, args[1])
			return found, nil
		case "Map.merge":
			other, ok := args[1].(vmap)
			if !ok {
				return nil, fmt.Errorf("vm: %s: operand 2 is %T, want Map", key, args[1])
			}
			k = mach.keysFor(nil)
			return rt.MapMerge(m, other, k.HashFunc(), k.EqualFunc()), nil
		case "Map.keys":
			return rt.MapKeys(m), nil
		case "Map.map_next":
			// The first entry in insertion order and the map without it.
			es := rt.MapEntries(m)
			if len(es) == 0 {
				return noneValue, nil
			}
			return some(pair(pair(es[0].Key, es[0].Val), mapRemove(mach.keysOver(nil, es[0].Key), m, es[0].Key))), nil
		case "Map.values":
			return rt.MapValues(m), nil
		case "Map.map_values":
			return mach.mapMapValues(m, args[1])
		case "Map.map_keys":
			return mach.mapMapKeys(m, args[1])
		default:
			return rtMapSize(m), nil
		}
	}
}

// mapMapValues is `Map.map_values(m, f)` through rt.MapMapValuesCtl, with f a
// VM function value called on the machine. A callback lowered under the
// signalling form answers a control value for `break` and `continue`, which
// adapterOutcome reads; any other callback's answer is CtlEmit. The first
// fault stops the walk.
func (m *Machine) mapMapValues(src vmap, fn any) (any, error) {
	f, ok := fn.(*functionValue)
	if !ok || f == nil || f.arity != 1 {
		return nil, fmt.Errorf("vm: Map.map_values: operand 2 must be a unary function")
	}
	var callbackErr error
	k := m.keysFor(nil)
	out := rt.MapMapValuesCtl(m.hostFrame, src, k.HashFunc(), k.EqualFunc(), func(fr *rt.Frame, v any) (any, rt.Ctl) {
		got, err := m.apply(f, []any{v}, fr)
		if err != nil {
			callbackErr = err
			return nil, rt.CtlStop
		}
		return mapCtlOutcome(got)
	})
	if callbackErr != nil {
		return nil, callbackErr
	}
	return out, nil
}

// mapCtlOutcome is adapterOutcome for a map host's callback, which runs
// outside an iteration driver: a control value it cannot read is an error
// rather than an iteration failure.
func mapCtlOutcome(v any) (any, rt.Ctl) {
	if c, ok := v.(*ctlValue); ok {
		switch c.ctl {
		case ir.CtlSkip:
			return nil, rt.CtlSkip
		case ir.CtlEmitStop:
			return c.v, rt.CtlEmitStop
		case ir.CtlStop:
			return nil, rt.CtlStop
		}
	}
	return v, rt.CtlEmit
}

// mapMapKeys is `Map.map_keys(m, f)`: each entry put under f(key) in
// insertion order, so a colliding later key keeps the first position and
// takes the later value, as `Iter.to_map` does. A signalling callback's
// `continue` leaves the entry out, a bare `break` stops with the entries put
// so far, and `break k` puts this entry under k and then stops.
func (m *Machine) mapMapKeys(src vmap, fn any) (any, error) {
	f, ok := fn.(*functionValue)
	if !ok || f == nil || f.arity != 1 {
		return nil, fmt.Errorf("vm: Map.map_keys: operand 2 must be a unary function")
	}
	var out vmap
	keys := m.keysFor(nil)
	for _, e := range rt.MapEntries(src) {
		got, err := m.apply(f, []any{e.Key}, m.hostFrame)
		if err != nil {
			return nil, err
		}
		k, ctl := mapCtlOutcome(got)
		if ctl == rt.CtlEmit || ctl == rt.CtlEmitStop {
			out = mapPut(keys, out, k, e.Val)
		}
		if ctl == rt.CtlEmitStop || ctl == rt.CtlStop {
			break
		}
	}
	return out, nil
}
