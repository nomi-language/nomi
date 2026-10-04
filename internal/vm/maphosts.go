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
		case "Map.get", "Map.remove", "Map.contains_key?", "Map.merge", "Map.map_values", "Map.map_keys":
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
		case "Map.contains_key?":
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

// mapMapValues is `Map.map_values(m, f)` through rt.MapMapValues, with f a VM
// function value called on the machine. The first fault stops the walk.
func (m *Machine) mapMapValues(src vmap, fn any) (any, error) {
	f, ok := fn.(*functionValue)
	if !ok || f == nil || f.arity != 1 {
		return nil, fmt.Errorf("vm: Map.map_values: operand 2 must be a unary function")
	}
	var callbackErr error
	k := m.keysFor(nil)
	out := rt.MapMapValues(m.hostFrame, src, k.HashFunc(), k.EqualFunc(), func(fr *rt.Frame, v any) any {
		if callbackErr != nil {
			return nil
		}
		got, err := m.apply(f, []any{v}, fr)
		if err != nil {
			callbackErr = err
		}
		return got
	})
	if callbackErr != nil {
		return nil, callbackErr
	}
	return out, nil
}

// mapMapKeys is `Map.map_keys(m, f)`: each entry put under f(key) in
// insertion order, so a colliding later key keeps the first position and
// takes the later value, as `Iter.to_map` does.
func (m *Machine) mapMapKeys(src vmap, fn any) (any, error) {
	f, ok := fn.(*functionValue)
	if !ok || f == nil || f.arity != 1 {
		return nil, fmt.Errorf("vm: Map.map_keys: operand 2 must be a unary function")
	}
	var out vmap
	keys := m.keysFor(nil)
	for _, e := range rt.MapEntries(src) {
		k, err := m.apply(f, []any{e.Key}, m.hostFrame)
		if err != nil {
			return nil, err
		}
		out = mapPut(keys, out, k, e.Val)
	}
	return out, nil
}
