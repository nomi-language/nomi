package lsp

import (
	"strings"
	"testing"
)

// A vector's named spread is an expression like any other: hover over it
// shows its type, and completion inside it offers the names in scope, first
// or last.
func TestVectorSpread_HoverAndCompletion(t *testing.T) {
	const decls = "struct Team {\n    names: Vector<String>\n}\n\n"
	for _, tc := range []struct{ name, body string }{
		{"first", `    _a = #[..▮names, "Noah"]`},
		{"last", `    _a = #["Noah", ..▮names]`},
	} {
		t.Run("hover "+tc.name, func(t *testing.T) {
			src := decls + "fn f(names: Vector<String>) {\n" + tc.body + "\n}\n"
			got := hoverText(t, "vector_spread_"+strings.ReplaceAll(tc.name, " ", "_"), src)
			if !strings.Contains(got, "names: Vector<String>") {
				t.Fatalf("hover = %q, want the spread's type", got)
			}
		})
	}
	for _, tc := range []struct{ name, body, want string }{
		{"a name, first", `    _a = #[..nam‸, "Noah"]`, "names"},
		{"a name, last", `    _a = #["Noah", ..nam‸]`, "names"},
		{"a field, first", `    _a = #[..t.‸, "Noah"]`, "names"},
		{"a field, last", `    _a = #["Noah", ..t.‸]`, "names"},
	} {
		t.Run("completion "+tc.name, func(t *testing.T) {
			src := decls + "fn f(names: Vector<String>, t: Team) {\n" + tc.body + "\n}\n"
			items := completeTriggered(t, src, "")
			if !hasLabel(items, tc.want) {
				labels := make([]string, len(items))
				for i, it := range items {
					labels[i] = it.Label
				}
				t.Fatalf("completion lacks %q: %v", tc.want, labels)
			}
		})
	}
}
