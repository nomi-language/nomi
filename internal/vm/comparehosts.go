package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/ir"
)

// containerCompareHost is `List.compare(a, b)` / `Vector.compare(a, b)`,
// std's lexicographic body: the first element pair the element comparator
// (the third operand) does not answer Equal decides, and a proper prefix is
// Less.
func containerCompareHost(key string) hostFn {
	return func(m *Machine, _ ir.Pos, args []any) (any, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("vm: %s: expected 3 operands, got %d", key, len(args))
		}
		cmp, ok := args[2].(*functionValue)
		if !ok || cmp == nil || cmp.arity != 2 {
			return nil, fmt.Errorf("vm: %s: operand 3 must be the element comparator", key)
		}
		var xs, ys []any
		for i, a := range args[:2] {
			var items []any
			switch v := a.(type) {
			case *list:
				items = listSlice(v)
			case rt.Vector[any]:
				for j := int64(0); j < rt.VectorLength(v); j++ {
					items = append(items, rt.VectorAt(v, j).Some)
				}
			case nil:
			default:
				return nil, fmt.Errorf("vm: %s: operand %d is %T", key, i+1, a)
			}
			if i == 0 {
				xs = items
			} else {
				ys = items
			}
		}
		for i := 0; i < len(xs) && i < len(ys); i++ {
			got, err := m.apply(cmp, []any{xs[i], ys[i]}, m.hostFrame)
			if err != nil {
				return nil, err
			}
			o, ok := orderingOf(got)
			if !ok {
				return nil, fmt.Errorf("vm: %s: the element comparator returned %T", key, got)
			}
			if o.Tag != rt.TagEqual {
				return got, nil
			}
		}
		switch {
		case len(xs) < len(ys):
			return orderingDesc.MakeVariant(0), nil
		case len(xs) > len(ys):
			return orderingDesc.MakeVariant(2), nil
		}
		return orderingDesc.MakeVariant(1), nil
	}
}
