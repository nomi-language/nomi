package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/ir"
)

// setHost is one of std's Set operations over the membership map a Set
// record carries. An update answers a new Set and leaves the receiver as it
// was, because the map is persistent.
func setHost(key string) hostFn {
	return func(mach *Machine, _ ir.Pos, args []any) (_ any, err error) {
		defer recoverKey(&err)
		// One element's operation takes the machine's keys only when the
		// element could reach a declared record; a second Set operand is a
		// record, so a bulk operation takes them.
		var k *rt.Keys
		if len(args) == 2 {
			k = mach.keysOver(nil, args[1])
		}
		arity := 2
		switch key {
		case "Set.size":
			arity = 1
		case "Set.contains?", "Set.insert", "Set.remove", "Set.union", "Set.intersection", "Set.difference", "Set.subset?":
		default:
			return nil, fmt.Errorf("vm: unsupported set host %s", key)
		}
		if len(args) != arity {
			return nil, fmt.Errorf("vm: %s: expected %d operands, got %d", key, arity, len(args))
		}
		items, err := setItems(args[0])
		if err != nil {
			return nil, err
		}
		switch key {
		case "Set.size":
			return rtMapSize(items), nil
		case "Set.contains?":
			_, found := mapLookup(k, items, args[1])
			return found, nil
		case "Set.insert":
			return setFromMap(mapPut(k, items, args[1], true)), nil
		case "Set.remove":
			return setFromMap(mapRemove(k, items, args[1])), nil
		}
		other, err := setItems(args[1])
		if err != nil {
			return nil, err
		}
		switch key {
		case "Set.union":
			// Map.merge of the two membership maps: a's members in order,
			// then b's that a lacks.
			return setFromMap(rt.MapMerge(items, other, k.HashFunc(), k.EqualFunc())), nil
		case "Set.subset?":
			for _, e := range rt.MapEntries(items) {
				if _, found := mapLookup(k, other, e.Key); !found {
					return false, nil
				}
			}
			return true, nil
		}
		// intersection and difference filter a's members in a's order.
		keep := key == "Set.intersection"
		var out vmap
		for _, e := range rt.MapEntries(items) {
			if _, found := mapLookup(k, other, e.Key); found == keep {
				out = mapPut(k, out, e.Key, true)
			}
		}
		return setFromMap(out), nil
	}
}
