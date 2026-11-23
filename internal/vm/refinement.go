package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
)

// refinementRuleHost is a generated `try_from`'s call of its refinement's
// rule (spec §4 *Refinements*; irbuild's refinementRule): the rule,
// a unary function, applied to the value. A rule that traps answers False,
// so `try_from` is None for the value, and the trap is dropped: nothing
// reports or records it. Only a fault is caught; a machine limit, a
// cancellation and a machine error still stop the call.
//
// An evaluation (Evaluate) lets the trap through, so the compile-time
// check of a refinement literal reports a rule that traps as an error
// carrying the trap's message (vmhost's checkRefinement).
func refinementRuleHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: vm.refinement_rule: expected 2 operands, got %d", len(args))
	}
	rule, ok := args[0].(*functionValue)
	if !ok || rule == nil || rule.arity != 1 {
		return nil, fmt.Errorf("vm: vm.refinement_rule: operand 1 must be a unary function")
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
