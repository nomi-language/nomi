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
//
// `Vector.set` traps on an index outside the vector. rt raises
// the trap with its text, and it becomes this machine's fault here, as an
// adapter's does in callAdapter.
func vectorHost(key string) hostFn {
	return func(_ *Machine, _ ir.Pos, args []any) (v any, err error) {
		arity := 2
		switch key {
		case "Vector.length", "Vector.next_item":
			arity = 1
		case "Vector.set":
			arity = 3
		case "Vector.get", "Vector.push", "Vector.concat":
		default:
			return nil, fmt.Errorf("vm: unsupported vector host %s", key)
		}
		if len(args) != arity {
			return nil, fmt.Errorf("vm: %s: expected %d operands, got %d", key, arity, len(args))
		}
		vec, ok := args[0].(rt.Vector[any])
		if !ok {
			return nil, fmt.Errorf("vm: %s: operand 1 is %T, want Vector", key, args[0])
		}
		var index int64
		if key == "Vector.get" || key == "Vector.set" {
			if !rt.IsInt(args[1]) {
				return nil, fmt.Errorf("vm: %s: operand 2 is %T, want Int", key, args[1])
			}
			index = rt.IntSaturated(args[1])
		}
		defer func() {
			if p := recover(); p != nil {
				if fault, ok := p.(*rt.Error); ok {
					v, err = nil, &Fault{err: fault}
					return
				}
				panic(p)
			}
		}()
		if key == "Vector.set" {
			if _, small := args[1].(int64); !small {
				// An index past int64's range is past any Vector; the fault
				// names the index itself, not its clamp.
				rt.Trap(rt.IndexOutOfRangeValueText("Vector", args[1], rt.VectorLength(vec)))
			}
		}
		switch key {
		case "Vector.length":
			return rt.VectorLength(vec), nil
		case "Vector.get":
			return maybeOf(rt.VectorGet(vec, index)), nil
		case "Vector.push":
			return rt.VectorPush(vec, args[1]), nil
		case "Vector.next_item":
			// `Some(#(head, tail))`, or None when empty: rt.VectorNextItem's
			// answer with the pair as the machine's tuple.
			if rt.VectorLength(vec) == 0 {
				return noneValue, nil
			}
			return some(pair(vec.Items[vec.Start], rt.VectorTail(vec))), nil
		case "Vector.set":
			return rt.VectorSet(vec, index, args[2]), nil
		default:
			other, ok := args[1].(rt.Vector[any])
			if !ok {
				return nil, fmt.Errorf("vm: %s: operand 2 is %T, want Vector", key, args[1])
			}
			return rt.VectorConcat(vec, other), nil
		}
	}
}

// vectorContainsHost is `Vector.contains`: rt's scan of the vector's window
// comparing each element with x as `==` does (keys.go), as listContainsHost
// walks a List.
func vectorContainsHost(mach *Machine, _ ir.Pos, args []any) (_ any, err error) {
	defer recoverKey(&err)
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: Vector.contains: expected 2 operands, got %d", len(args))
	}
	vec, ok := args[0].(rt.Vector[any])
	if !ok {
		return nil, fmt.Errorf("vm: Vector.contains: operand 1 is %T, want Vector", args[0])
	}
	return rt.VectorContains(vec, args[1], mach.keysOver(nil, args[1]).EqualFunc()), nil
}
