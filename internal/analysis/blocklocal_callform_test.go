package analysis

import (
	"strings"
	"testing"
)

// The call form of a struct declared in a block is checked as the call form,
// as a module-level struct's is: a generic one's type arguments are solved
// from the record, so its field reads at the solved type. Before, the name
// was missing from the module's registry, the call was checked as an
// ordinary call, and `Cell({c: 1}).c` passed as any type.
func TestStructCallForm_BlockLocalStructIsTheCallForm(t *testing.T) {
	const decls = "fn f(): Int {\n  struct Cell<T> {\n    c: T\n  }\n  struct P {\n    c: Int\n  }\n"
	rows := []struct {
		name, body, want string
	}{
		{"generic, read at its type", "  Cell({c: 1}).c + P({c: 2}).c\n}\n", ""},
		{"generic, read at another type", "  s: String = Cell({c: 1}).c\n  String.length(s) + P({c: 2}).c\n}\n",
			"type mismatch: expected String, got Int"},
		{"generic, a field it does not have", "  Cell({d: 1}).c + P({c: 2}).c\n}\n", "Cell has no field 'd'"},
		{"plain, a field of the wrong type", "  Cell({c: 1}).c + P({c: \"x\"}).c\n}\n", "String"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got := strings.Join(errorTexts(t, decls+r.body), "\n")
			if r.want == "" {
				if got != "" {
					t.Fatalf("want accepted, got:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, r.want) {
				t.Fatalf("want an error containing %q, got:\n%s", r.want, got)
			}
		})
	}
}
