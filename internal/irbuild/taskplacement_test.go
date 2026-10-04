package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestTask_PlacementDoesNotDependOnTheStdlibIndex holds that `taskPlacement`
// reads parameter names off the `taskFuncs` row and nothing else.
//
// The stdlib index cannot supply them everywhere. `lowerStdlibModule` lowers a
// module against the modules before it, which excludes the module itself, and
// `bindStdSiblings` skips a candidate that is neither settled nor rt-bound,
// which `Task.spawn_all` (an unsettled `pub host fn`) is. So inside std/tasks'
// own gen the index has no `Task.spawn_all` entry, and a placement that read it
// would work in a user module and refuse the `//!` prompt cases in
// std/tasks.nomi.
//
// taskPlacement takes no gen and no index, so the property holds by its
// signature; this checks it answers a real named-argument call and answers it
// the same way twice.
func TestTask_PlacementDoesNotDependOnTheStdlibIndex(t *testing.T) {
	// `xs |> Task.spawn_all(f, max_running: 2)` after the pipe rewrite: three
	// arguments, the third written by name onto the last slot.
	args := []ast.Node{
		&ast.Ident{Name: "xs"},
		&ast.Ident{Name: "f"},
		&ast.NamedArg{Name: "max_running", Value: &ast.IntLit{Value: 2}},
	}
	plan, ok := taskPlacement(taskFuncs["spawn_all"], args)
	if !ok {
		t.Fatal("taskPlacement declined `Task.spawn_all(xs, f, max_running: 2)`")
	}
	if got := plan.slots; len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("slots = %v, want [0 1 2]", got)
	}
	// Evaluation order: both positionals before the named one, which is
	// resolveArgs' partition and is observable whenever an argument has an
	// effect. See taskCallArgs.
	if got := plan.order; len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("order = %v, want [0 1 2]: every positional is drained before "+
			"any named argument", got)
	}
	if plan2, ok2 := taskPlacement(taskFuncs["spawn_all"], args); !ok2 ||
		plan2.slots[2] != plan.slots[2] {
		t.Error("two calls to taskPlacement disagreed, so it is reading state " +
			"from somewhere other than its arguments")
	}
}

// TestTask_EveryRowPlacesItsOwnNamedArgument walks the table rather than naming
// one method, so a row added without names fails here instead of refusing
// `named argument` at some call site.
func TestTask_EveryRowPlacesItsOwnNamedArgument(t *testing.T) {
	for method, fn := range taskFuncs {
		if len(fn.names) == 0 {
			t.Errorf("Task.%s has no parameter names, so a named argument to it "+
				"cannot be placed at all", method)
			continue
		}
		// One argument per parameter, the last written by name. Placement must
		// put every one on its own slot.
		args := make([]ast.Node, 0, len(fn.names))
		for i, n := range fn.names {
			if i == len(fn.names)-1 {
				args = append(args, &ast.NamedArg{Name: n, Value: &ast.IntLit{Value: 1}})
				continue
			}
			args = append(args, &ast.Ident{Name: "a"})
		}
		plan, ok := taskPlacement(fn, args)
		if !ok {
			t.Errorf("Task.%s: placement declined a call naming its own last "+
				"parameter %q", method, fn.names[len(fn.names)-1])
			continue
		}
		for i := range args {
			if plan.slots[i] != i {
				t.Errorf("Task.%s: argument %d landed on slot %d", method, i, plan.slots[i])
			}
		}
	}
}
