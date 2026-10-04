package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

func resultMapErrHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: Result.map_err: expected 2 operands, got %d", len(args))
	}
	v, ok := enumRecord(args[0])
	payload, populated := any(nil), false
	if ok {
		payload, populated = payloadOf(v)
	}
	if !ok || !isEnum(v, "results.Result") || (variantName(v) != "Ok" && variantName(v) != "Err") || !populated {
		return nil, fmt.Errorf("vm: Result.map_err: operand 1 must be a populated Result")
	}
	f, ok := args[1].(*functionValue)
	if !ok || f == nil || f.arity != 1 {
		return nil, fmt.Errorf("vm: Result.map_err: operand 2 must be a unary function")
	}
	var src rt.Result[any, any]
	if variantName(v) == "Ok" {
		src = rt.Ok[any, any](payload)
	} else {
		src = rt.Err[any, any](payload)
	}
	var callbackErr error
	result := rt.ResultMapErr(m.hostFrame, src, func(fr *rt.Frame, arg any) any {
		got, err := m.apply(f, []any{arg}, fr)
		callbackErr = err
		return got
	})
	if callbackErr != nil {
		return nil, callbackErr
	}
	if result.Tag == rt.TagOk {
		return okValue(result.Ok), nil
	}
	return errValue(result.Err), nil
}

// resultFromMaybeHost is `Result.from_maybe(m, e)`: Some(v) becomes Ok(v) and
// None becomes Err(e), through rt's function for it.
func resultFromMaybeHost(_ *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: Result.from_maybe: expected 2 operands, got %d", len(args))
	}
	payload, isSome, ok := maybeParts(args[0])
	if !ok {
		return nil, fmt.Errorf("vm: Result.from_maybe: operand 1 must be a Maybe")
	}
	src := rt.Maybe[any]{Tag: rt.TagNone}
	if isSome {
		src = rt.Some(payload)
	}
	result := rt.ResultFromMaybe(src, args[1])
	if result.Tag == rt.TagOk {
		return okValue(result.Ok), nil
	}
	return errValue(result.Err), nil
}
