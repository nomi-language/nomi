package rt

import (
	"math"
	"math/big"
)

// Dynamic equality and hashing (spec §5 *Equality*). A Dynamic is a value,
// compared by its contents as a JSON value is:
//
//   - null equals null, a Bool a Bool of the same value, a String a String
//     of the same text;
//   - an Int equals an Int of the same value, whatever Go width each is
//     held at (an int8 1 and an int64 1, a uint64 past int64's range and
//     the same number); a Float equals a Float by EqFloat, a float32 widened
//     first. An Int never equals a Float, even of the same number: Nomi's
//     `1 == 1.0` is a type error, and dynamic's type vocabulary names them
//     apart (dynamicTypeName);
//   - an array equals an array of equal elements in the same order;
//   - an object equals an object with the same keys whose values are
//     equal, in any order.
//
// Any other Go value a host hands over is equal to nothing, itself
// included, as an opaque value is. Two equal values hash alike:
// DynamicHash folds an object's entries order-independently and hashes an
// Int by its value, not its width.

// DynamicEqual reports whether a and b hold equal contents.
func DynamicEqual(a, b Dynamic) bool { return dynamicEqual(a.Inner, b.Inner) }

// DynamicHash is the hash consistent with DynamicEqual.
func DynamicHash(d Dynamic) uint64 { return HashMix(0xd1a, dynamicHash(d.Inner)) }

// DynamicHashInt is `impl Hashable for Dynamic`'s `hash`: DynamicHash as
// an Int.
func DynamicHashInt(d Dynamic) int64 { return int64(DynamicHash(d)) }

// dynamicInt answers an integer of any Go width as an int64 when it fits,
// else as a big.Int (a uint64 past int64's range).
func dynamicInt(v any) (int64, *big.Int, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), nil, true
	case int8:
		return int64(x), nil, true
	case int16:
		return int64(x), nil, true
	case int32:
		return int64(x), nil, true
	case int64:
		return x, nil, true
	case uint:
		return dynamicUint(uint64(x))
	case uint8:
		return int64(x), nil, true
	case uint16:
		return int64(x), nil, true
	case uint32:
		return int64(x), nil, true
	case uint64:
		return dynamicUint(x)
	}
	return 0, nil, false
}

func dynamicUint(x uint64) (int64, *big.Int, bool) {
	if x <= math.MaxInt64 {
		return int64(x), nil, true
	}
	return 0, new(big.Int).SetUint64(x), true
}

func dynamicFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	}
	return 0, false
}

func dynamicEqual(a, b any) bool {
	if ai, abig, ok := dynamicInt(a); ok {
		bi, bbig, ok := dynamicInt(b)
		if !ok {
			return false
		}
		if abig != nil || bbig != nil {
			return abig != nil && bbig != nil && abig.Cmp(bbig) == 0
		}
		return ai == bi
	}
	if af, ok := dynamicFloat(a); ok {
		bf, ok := dynamicFloat(b)
		return ok && EqFloat(af, bf)
	}
	switch av := a.(type) {
	case nil:
		return b == nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !dynamicEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, x := range av {
			y, has := bv[k]
			if !has || !dynamicEqual(x, y) {
				return false
			}
		}
		return true
	}
	return false
}

func dynamicHash(v any) uint64 {
	if i, big, ok := dynamicInt(v); ok {
		if big != nil {
			return HashMix(0x3, HashString(big.String()))
		}
		return HashMix(0x3, HashInt(i))
	}
	if f, ok := dynamicFloat(v); ok {
		return HashMix(0x4, HashFloat(f))
	}
	switch x := v.(type) {
	case nil:
		return 0x1
	case bool:
		return HashMix(0x2, HashBoolVariant(x))
	case string:
		return HashMix(0x5, HashString(x))
	case []any:
		h := uint64(0x6)
		for _, e := range x {
			h = HashMix(h, dynamicHash(e))
		}
		return h
	case map[string]any:
		// A sum of the entries' hashes: the same whatever order Go's map
		// iteration takes.
		var sum uint64
		for k, e := range x {
			sum += HashMix(HashString(k), dynamicHash(e))
		}
		return HashMix(0x7, sum)
	}
	return 0
}
