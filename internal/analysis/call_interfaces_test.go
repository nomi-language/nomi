package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

const callInterfacesSrc = `interface Holds<T>
    fn holds(b: self, x: T) -> Bool
end

struct Box
    n: Int
end

impl Holds<Int> for Box
    fn holds(b: Box, x: Int) -> Bool
        b.n == x
    end
end

impl Holds<String> for Box
    fn holds(_: Box, x: String) -> Bool
        x == ""
    end
end

fn through_bound<C, T>(c: C, x: T) -> Bool where C: Holds<T>
    C.holds(c, x)
end

fn through_interface<C, T>(c: C, x: T) -> Bool where C: Holds<T>
    Holds.holds(c, x)
end

fn run() -> Bool
    Box.holds(Box{n: 1}, 1)
end
`

// TestCallInterfaces_RecordTheInstance: a call of an interface function
// the checker selects no impl for records the interface instance it goes
// through, whether it names the bound's type parameter (`C.holds`) or the
// interface (`Holds.holds`); a call with a selected impl records none.
func TestCallInterfaces_RecordTheInstance(t *testing.T) {
	fa, nodes := checkNodes(t, callInterfacesSrc)
	got := map[string]string{}
	for _, n := range nodes {
		fd, ok := n.(*ast.FuncDef)
		if !ok {
			continue
		}
		ast.Inspect(fd, func(m ast.Node) bool {
			call, ok := m.(*ast.Call)
			if !ok {
				return true
			}
			pos, named := calleeKey(call.Func)
			if !named {
				return true
			}
			if ref := fa.References[pos]; ref != nil {
				if ref.CallInterface != nil {
					got[fd.Name] = ref.CallInterface.String()
				} else {
					got[fd.Name] = "none"
				}
			}
			return true
		})
	}
	want := map[string]string{"through_bound": "Holds<T>", "through_interface": "Holds<T>", "run": "none"}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: CallInterface %q, want %q", name, got[name], w)
		}
	}
	if vs := VerifyElaboration(fa, nodes); len(vs) > 0 {
		t.Fatalf("violations: %v", vs)
	}
}
