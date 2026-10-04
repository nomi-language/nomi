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
