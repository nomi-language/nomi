package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestNamedArg_ExplicitZeroIsNotAbsence is the mechanism's central claim, held
// at the unit level as well as through the VM, because it is the one a future
// reader is most likely to "simplify".
//
// Presence is decided by the `filled []string` slot vector, and a lowered
// expression's code text is never the empty string. So `opts(2, factor: 0)` fills its slot
// with the text `0` and the default is not applied. A zero-value test cannot
// distinguish "absent" from "explicitly the type's zero", and `0` / `""` /
// `False` / `0.0` are all reachable Nomi literals.
func TestNamedArg_ExplicitZeroIsNotAbsence(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	out := vmReference(fixture("named_args.nomi"))
	if !strings.Contains(out.stdout, "L0") {
		t.Fatalf("the VM no longer answers L0 for `opts(2, factor: 0)`, so the "+
			"fixture has stopped discriminating a presence mask from a zero-value "+
			"test. stdout=%q", out.stdout)
	}
	if strings.Contains(out.stdout, "L20") {
		t.Fatalf("`opts(2, factor: 0)` answered as though `factor` had DEFAULTED to 10. "+
			"That is the zero-value-test defect. stdout=%q", out.stdout)
	}
}

// TestNamedArg_SlotPlanMirrorsResolveArgs pins the three placement rules of
// Nomi's argument resolution, at the unit level, including the two that the
// corpus does not currently spell.
//
// Rules 1 and 2 are the ones a positional-only reading gets wrong, and neither
// is reachable from a fixture today: rule 1 needs a bare
// module-function reference (`unbound name` in this builder, see
// arg_slots.nomi's closing comment) and rule 2's suppression needs
// `Task.spawn_all`. So they are asserted directly rather than left uncovered.
func TestNamedArg_SlotPlanMirrorsResolveArgs(t *testing.T) {
	fn := kind{tag: tagFunc}
	named := func(name string) ast.Node { return &ast.NamedArg{Name: name, Value: atomNode()} }

	t.Run("a name places its argument regardless of written position", func(t *testing.T) {
		// order(b: _, _) — the named argument takes slot 1, the positional 0.
		args := []ast.Node{named("b"), atomNode()}
		plan, ok := argSlotPlan(args, []kind{kindInt, kindInt}, []string{"a", "b"})
		if !ok {
			t.Fatal("declined a call the checker accepts")
		}
		if plan.slots[0] != 1 || plan.slots[1] != 0 {
			t.Fatalf("slots = %v, want [1 0]: the name must decide, not the position", plan.slots)
		}
	})

	t.Run("EVALUATION ORDER is positionals then named, not written order", func(t *testing.T) {
		args := []ast.Node{named("b"), atomNode()}
		plan, ok := argSlotPlan(args, []kind{kindInt, kindInt}, []string{"a", "b"})
		if !ok {
			t.Fatal("declined a call the checker accepts")
		}
		// Written index 1 (the positional) is evaluated FIRST; written index 0
		// (the named) LAST; the fixture shows it.
		if len(plan.order) != 2 || plan.order[0] != 1 || plan.order[1] != 0 {
			t.Fatalf("order = %v, want [1 0]: resolveArgs drains every positional "+
				"before any named argument, so a named argument written FIRST is "+
				"evaluated LAST", plan.order)
		}
	})

	t.Run("rule 1: a positional AFTER a named argument skips the claimed slot", func(t *testing.T) {
		// each(items, opts: 8, handler) — `handler` is written third, slot 1 is
		// claimed by name, so it must land in slot 2 rather than colliding.
		args := []ast.Node{atomNode(), named("opts"), &ast.Lambda{}}
		plan, ok := argSlotPlan(args, []kind{kindInt, kindInt, fn}, []string{"items", "opts", "handler"})
		if !ok {
			t.Fatal("declined the options-then-callback shape the checker accepts")
		}
		if plan.slots[2] != 2 {
			t.Fatalf("the trailing positional landed in slot %d, want 2: a positional "+
				"written after a named argument skips name-claimed slots", plan.slots[2])
		}
	})

	t.Run("rule 1: a positional BEFORE a named argument does NOT skip", func(t *testing.T) {
		// add(1, a: 2) — the `1` came first and does mean slot 0, so the name
		// collides, which is an "already has a value" error. Declined.
		args := []ast.Node{atomNode(), named("a")}
		if _, ok := argSlotPlan(args, []kind{kindInt, kindInt}, []string{"a", "b"}); ok {
			t.Fatal("accepted `add(1, a: 2)`, which is a duplicate-slot error: a " +
				"positional written BEFORE a named argument must not skip past it")
		}
	})

	t.Run("rule 2: a name on the last parameter SUPPRESSES the trailing-lambda move", func(t *testing.T) {
		// Task.spawn_all(items, |x| f(x), max_running: 8) — the lambda is the
		// last positional, but the last slot is spoken for by name, so moving
		// it there would collide.
		args := []ast.Node{atomNode(), &ast.Lambda{}, named("max_running")}
		plan, ok := argSlotPlan(args, []kind{kindInt, fn, kindInt}, []string{"items", "body", "max_running"})
		if !ok {
			t.Fatal("declined a call the checker accepts")
		}
		if plan.slots[1] != 1 {
			t.Fatalf("the lambda moved to slot %d; a named argument targeting the "+
				"last parameter must suppress the trailing-lambda move, or the "+
				"two collide", plan.slots[1])
		}
	})

	t.Run("an unknown parameter name is declined, not placed", func(t *testing.T) {
		args := []ast.Node{named("nope")}
		if _, ok := argSlotPlan(args, []kind{kindInt}, []string{"a"}); ok {
			t.Fatal("accepted a name no parameter has; that is a front-end error and " +
				"this walk must not invent a slot for it")
		}
	})

	t.Run("no name table declines a named argument but not a positional one", func(t *testing.T) {
		// A synthesized signature records kinds only. Declining is what makes
		// directCall report `named argument` instead of guessing a slot.
		if _, ok := argSlotPlan([]ast.Node{named("a")}, []kind{kindInt}, nil); ok {
			t.Fatal("placed a named argument with no parameter names available")
		}
		if _, ok := argSlotPlan([]ast.Node{atomNode()}, []kind{kindInt}, nil); !ok {
			t.Fatal("a wholly positional call must be unaffected by a missing name table")
		}
	})
}
