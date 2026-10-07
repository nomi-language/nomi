package analysis

import (
	"strings"
	"testing"
)

// A type declared in a block can be named by an annotation in that block's
// own statements, a binding's or a lambda parameter's, and the annotation is
// checked as one naming a module-level type is. Before, the checker's
// registry held no block-local type: a binding annotation was "unknown
// type", and a lambda parameter's annotation was dropped, so the parameter
// took any argument and its uses were typed Unit. An annotation naming no
// type at all is an error on a lambda parameter too.
func TestBlockLocalType_AnnotationsNameIt(t *testing.T) {
	const decls = "fn f(): Int {\n  type Meters Int\n  struct Box<T> {\n    v: T\n  }\n"
	rows := []struct {
		name, body, want string
	}{
		{"binding annotations", "  m: Meters = Meters(1)\n  Meters(n) = m\n  b: Box<Int> = Box{v: 2}\n  n + b.v\n}\n", ""},
		{"a lambda parameter", "  g = |m: Meters| m\n  Meters(n) = g(Meters(3))\n  n\n}\n", ""},
		{"a lambda parameter given another type", "  g = |m: Meters| m\n  Meters(n) = g(\"x\")\n  n\n}\n",
			"argument 1: expected Meters, got String"},
		{"a lambda parameter's use at another type", "  g = |m: Meters| m\n  s: String = g(Meters(1))\n  String.length(s)\n}\n",
			"type mismatch: expected String, got Meters"},
		{"a binding annotation at another type", "  b: Box<String> = Box{v: 2}\n  String.length(b.v)\n}\n", "Int"},
		{"a lambda parameter naming no type", "  g = |m: Nope| 1\n  g(1)\n}\n", "unknown type \"Nope\""},
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
