package vm

import (
	"errors"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/ir"
)

// unreachableHost is `vm.unreachable`: a call the builder proved no
// execution reaches, a bound dispatch on a type argument it filled itself
// because the checker left it unconstrained. Arriving is rt's fault carrying
// the builder's account, never a value.
func unreachableHost(_ *Machine, pos ir.Pos, args []any) (any, error) {
	if len(args) == 1 {
		if reason, ok := args[0].(string); ok {
			return nil, rt.UnreachableError(pos.Line(), reason)
		}
	}
	return nil, errors.New("vm: vm.unreachable takes the builder's text")
}
