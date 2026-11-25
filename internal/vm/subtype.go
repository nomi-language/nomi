package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
)

// subtypeRuleHost is a generated `try_from`'s call of its subtype's
// rule (spec §4 *Subtypes*; irbuild's subtypeRule): the rule,
// a unary function, applied to the value. A rule that traps answers False,
// so `try_from` is None for the value, and the trap is dropped: nothing
// reports or records it. Only a fault is caught; a machine limit, a
// cancellation and a machine error still stop the call.
//
// An evaluation (Evaluate) lets the trap through, so the compile-time
// check of a subtype literal reports a rule that traps as an error
// carrying the trap's message (vmhost's checkSubtype).
func subtypeRuleHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: vm.when_rule: expected 2 operands, got %d", len(args))
	}
	rule, ok := args[0].(*functionValue)
	if !ok || rule == nil || rule.arity != 1 {
		return nil, fmt.Errorf("vm: vm.when_rule: operand 1 must be a unary function")
	}
	holds, err := m.apply(rule, []any{args[1]}, m.hostFrame)
	if err == nil {
		return holds, nil
	}
	if _, isFault := asFault(err); isFault && m.fuel == nil {
		if limit := m.tasks.reached(); limit != nil {
			return nil, limit
		}
		return false, nil
	}
	return nil, err
}
