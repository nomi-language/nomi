package irbuild

import (
	"strings"
	"testing"
)

// Field access on a value whose static type does not name the storage.
//
// The one case is an enum: the static type names several variants and only
// the run-time tag says which one is here, so the read is a tag switch. An
// interface value and a type parameter have no fields, which the checker
// enforces.

// TestFieldRead_TheCheckerWallsOffThreeBackstops is the witness for the
// unreachable guards in `fieldAccess`: `field access on a scalar`,
// `field access on a distinct type`, and the residue of `field access on an
// enum`. They are unreachable because the checker walls them off, not because
// the builder is careful. That is a claim about another package, so it is
// asserted here: if the front end admits one of these, this test fails and
// names the backstop that would fire.
//
// Constructed rather than found, because a corpus cannot contain a program that
// does not type-check.
func TestFieldRead_TheCheckerWallsOffThreeBackstops(t *testing.T) {
	cases := []struct {
		backstop string
		src      string
		want     string
	}{
		{"field access on a scalar",
			"fn read(n: Int): Int {\n  n.x\n}\n",
			"Int has no field 'x'"},
		{"field access on a distinct type (over a scalar)",
			"type M Int\n\nfn read(m: M): Int {\n  m.n\n}\n",
			"'M' has no field 'n'"},
		{"field access on a distinct type (wrapping a struct)",
			"struct C {\n  r: Float\n}\n\ntype W C\n\nfn read(w: W): Float {\n  w.r\n}\n",
			"'W' has no field 'r'"},
		{"field access on an enum (no variant supplies the name)",
			"enum E {\n  V Int\n}\n\nfn read(e: E): Int {\n  e.V\n}\n",
			"enum 'E' has no field 'V'"},
		{"field access on an interface value",
			"fn read(d: Display): String {\n  d.name\n}\n",
			"interface 'Display' has no field 'name'"},
		{"field access on a bounded type parameter",
			"fn read<T>(x: T): String where T: Display {\n  x.name\n}\n",
			"type parameter `T` has no field 'name'"},
	}
	for _, tc := range cases {
		t.Run(tc.backstop, func(t *testing.T) {
			_, err := AnalyzeSource("main", tc.src)
			if err == nil {
				t.Fatalf("the front end ADMITS this, so irbuild's %q backstop is live and untested", tc.backstop)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rejected for another reason, so the wall is not the one this pins: %v", err)
			}
		})
	}
}
