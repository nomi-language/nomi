package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// vectorHost is one of std's Vector operations over rt's vector windows. An
// update answers a new vector and leaves the receiver as it was. They are
// intrinsics rather than generated adapters because they are generic over the
// element: the machine's vectors and lists hold `any`, which no std
// declaration's type parameter describes to the generator.
func vectorHost(key string) hostFn {
	return func(_ *Machine, _ ir.Pos, args []any) (any, error) {
		arity := 2
		switch key {
		case "Vector.length", "Vector.next_item":
			arity = 1
		case "Vector.set":
			arity = 3
		case "Vector.at", "Vector.push", "Vector.concat":
		default:
			return nil, fmt.Errorf("vm: unsupported vector host %s", key)
		}
		if len(args) != arity {
			return nil, fmt.Errorf("vm: %s: expected %d operands, got %d", key, arity, len(args))
		}
		v, ok := args[0].(rt.Vector[any])
		if !ok {
			return nil, fmt.Errorf("vm: %s: operand 1 is %T, want Vector", key, args[0])
		}
		switch key {
		case "Vector.length":
			return rt.VectorLength(v), nil
		case "Vector.at":
			i, ok := args[1].(int64)
			if !ok {
				return nil, fmt.Errorf("vm: %s: operand 2 is %T, want Int", key, args[1])
			}
			return maybeOf(rt.VectorAt(v, i)), nil
		case "Vector.push":
			return rt.VectorPush(v, args[1]), nil
		case "Vector.next_item":
			// `Some((head, tail))`, or None when empty: rt.VectorNextItem's
			// answer with the pair as the machine's tuple.
			if rt.VectorLength(v) == 0 {
				return noneValue, nil
			}
			return some(pair(v.Items[v.Start], rt.VectorTail(v))), nil
		case "Vector.set":
			i, ok := args[1].(int64)
			if !ok {
				return nil, fmt.Errorf("vm: %s: operand 2 is %T, want Int", key, args[1])
			}
			return maybeOf(rt.VectorSet(v, i, args[2])), nil
		default:
			other, ok := args[1].(rt.Vector[any])
			if !ok {
				return nil, fmt.Errorf("vm: %s: operand 2 is %T, want Vector", key, args[1])
			}
			return rt.VectorConcat(v, other), nil
		}
	}
}
