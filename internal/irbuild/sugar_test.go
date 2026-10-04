package irbuild

import (
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// Default parameter values, destructuring `fn` parameters, and argument slot
// placement.
//
// A fixture's output is held to its golden record, and a literal `want` says
// what that record must SAY, because a recording can be wrong.

// TestDefaults_CalleeScopeIsLoadBearing states which line of the fixture is the
// scope guard, and what breaking it looks like.
//
// A default is evaluated in the callee's environment, so `offset`'s `base()`
// reaches the module function even though the caller has an Int local of that
// name. If fillDefaults stopped swapping the scope stack, `base` would resolve
// to the caller's Int and the call would be refused as `call through a value` —
// so the mutation shows up as an UNSUPPORTED file rather than as a wrong number,
// which is why this guard is stated rather than left to an output comparison.
// Replacing the swap in fillDefaults with a pushScope() makes
// testdata/defaults.nomi report `unsupported: call through a value (base)`.
func TestDefaults_CalleeScopeIsLoadBearing(t *testing.T) {
	src := readFixture(t, "defaults.nomi")
	for _, needle := range []string{"b: Int = base()", "base = 1"} {
		if !strings.Contains(src, needle) {
			t.Fatalf("the callee-scope guard %q is gone from defaults.nomi; a default\n"+
				"resolved in the caller's scope would now lower", needle)
		}
	}
}

// TestArgSlots_BareNameRouting covers routesAsTrailingFunc's other arm
// directly, because a bare module-function reference is not yet an expression
// this builder lowers — so it cannot appear in a run fixture without
// refusing the file for an unrelated gap.
//
// The rule: route only when the LAST parameter takes a function and the slot the
// name would otherwise fill does not. Both halves matter, so both are asserted,
// and so is the no-op at full arity.
func TestArgSlots_BareNameRouting(t *testing.T) {
	name := &ast.Ident{Name: "double"}
	fn := kind{tag: tagFunc}
	cases := []struct {
		what   string
		args   []ast.Node
		params []kind
		want   int
	}{
		{"last takes a function and the name's own slot does not: routes",
			[]ast.Node{atomNode(), name}, []kind{kindInt, kindInt, fn}, 2},
		{"the name's own slot already takes a function: no move",
			[]ast.Node{atomNode(), name}, []kind{kindInt, fn, fn}, 1},
		{"the last parameter takes no function: no move",
			[]ast.Node{atomNode(), name}, []kind{kindInt, kindInt, kindInt}, 1},
		{"full arity: the move is a no-op by construction",
			[]ast.Node{atomNode(), name}, []kind{kindInt, fn}, 1},
		{"a lambda routes without consulting any type",
			[]ast.Node{atomNode(), &ast.Lambda{}}, []kind{kindInt, kindInt, kindInt}, 2},
		{"so does a block",
			[]ast.Node{&ast.Block{}}, []kind{kindInt, kindInt}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			// nil names: every case here is positional, and passing nil is also
			// the assertion that a caller with no name table available keeps
			// today's behaviour exactly.
			plan, ok := argSlotPlan(tc.args, tc.params, nil)
			if !ok {
				t.Fatalf("argSlotPlan declined a wholly positional call")
			}
			slots := plan.slots
			if got := slots[len(slots)-1]; got != tc.want {
				t.Fatalf("last argument landed in slot %d, want %d", got, tc.want)
			}
			// Every earlier argument keeps its position: only the trailing one
			// ever moves.
			for i := range len(slots) - 1 {
				if slots[i] != i {
					t.Fatalf("argument %d moved to slot %d; only the trailing one moves", i, slots[i])
				}
			}
			// With no named argument the partition is degenerate, so evaluation
			// order must be written order. This is the half that would silently
			// reorder every existing call if the partition were wrong.
			for n, i := range plan.order {
				if n != i {
					t.Fatalf("evaluation order %v is not written order; a wholly "+
						"positional call must not be reordered", plan.order)
				}
			}
		})
	}
}

// readFixture is a fixture's own source, for the guards that assert a
// load-bearing LINE is still in it. A fixture whose crucial case somebody
// simplified away passes every differential comparison it has left.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(fixture(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

// atomNode is a stand-in argument that is neither a lambda, a block nor a bare
// name, so it never routes.
func atomNode() ast.Node { return &ast.IntLit{Value: 1} }
