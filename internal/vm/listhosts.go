package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// listHost is `List.head`, `List.tail` and `List.concat` over rt's cons cells,
// run by rt's list kernels.
func listHost(key string) hostFn {
	return func(_ *Machine, _ ir.Pos, args []any) (any, error) {
		arity := 1
		if key == "List.concat" {
			arity = 2
		}
		if len(args) != arity {
			return nil, fmt.Errorf("vm: %s: expected %d operands, got %d", key, arity, len(args))
		}
		lists := make([]*list, arity)
		for i, arg := range args {
			xs, ok := arg.(*list)
			if !ok {
				return nil, fmt.Errorf("vm: %s: operand %d is %T, want List", key, i+1, arg)
			}
			lists[i] = xs
		}
		switch key {
		case "List.head":
			return maybeOf(rt.ListCellHead[any, list](lists[0])), nil
		case "List.tail":
			return maybeOf(rt.ListCellTail[any, list](lists[0])), nil
		case "List.concat":
			return rt.ListCellConcat[any, list](lists[0], lists[1]), nil
		}
		return nil, fmt.Errorf("vm: unsupported list host %s", key)
	}
}

// listRemoveAtHost is `List.remove_at`: rt.ListCellRemoveAt, whose trap on
// an index outside the list becomes this machine's fault here, as
// `Vector.at`'s does in vectorHost.
func listRemoveAtHost(_ *Machine, _ ir.Pos, args []any) (v any, err error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: List.remove_at: expected 2 operands, got %d", len(args))
	}
	xs, ok := args[0].(*list)
	if !ok {
		return nil, fmt.Errorf("vm: List.remove_at: operand 1 is %T, want List", args[0])
	}
	index, ok := args[1].(int64)
	if !ok {
		return nil, fmt.Errorf("vm: List.remove_at: operand 2 is %T, want Int", args[1])
	}
	defer recoverTrap(&err)
	return rt.ListCellRemoveAt[any, list](xs, index), nil
}

// recoverTrap turns rt's trap, raised by an rt kernel a host calls, into
// this machine's fault, as callAdapter does for an adapter's.
func recoverTrap(err *error) {
	if p := recover(); p != nil {
		if fault, ok := p.(*rt.Error); ok {
			*err = &Fault{err: fault}
			return
		}
		panic(p)
	}
}

// listContainsHost is `List.contains?`: rt's cell walk comparing each element
// with x as `==` does (keys.go), so a List of declared records reaches their
// own Equatable and every other element is compared in rt, with no Nomi call.
func listContainsHost(mach *Machine, _ ir.Pos, args []any) (_ any, err error) {
	defer recoverKey(&err)
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: List.contains?: expected 2 operands, got %d", len(args))
	}
	xs, ok := args[0].(*list)
	if !ok {
		return nil, fmt.Errorf("vm: List.contains?: operand 1 is %T, want List", args[0])
	}
	return rt.ListCellContains[any, list](xs, args[1], mach.keysOver(nil, args[1]).EqualFunc()), nil
}
