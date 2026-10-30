package vm

import (
	"errors"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/ir"
)

// The traps of a postfix `!` (internal/irbuild's unwrap.go). The builder
// reaches one only on the failure edge of the value's tag test, so the
// success path costs nothing here.

// bangFailedHost is `vm.bang_failed`: the expression under a `!` answered
// Err. Its operands are the expression's text and the error's text.
func bangFailedHost(_ *Machine, pos ir.Pos, args []any) (any, error) {
	if len(args) == 2 {
		source, ok1 := args[0].(string)
		text, ok2 := args[1].(string)
		if ok1 && ok2 {
			return nil, rt.BangFailedError(pos.Line(), source, text)
		}
	}
	return nil, errors.New("vm: vm.bang_failed takes the expression's text and the error's text")
}

// bangNoneHost is `vm.bang_none`: the expression under a `!` answered None.
// Its operand is the expression's text.
func bangNoneHost(_ *Machine, pos ir.Pos, args []any) (any, error) {
	if len(args) == 1 {
		if source, ok := args[0].(string); ok {
			return nil, rt.BangNoneError(pos.Line(), source)
		}
	}
	return nil, errors.New("vm: vm.bang_none takes the expression's text")
}
