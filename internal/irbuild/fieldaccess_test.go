package irbuild

import (
	"strings"
	"testing"
)

// Field access on a value whose static type does not name the storage.
//
// Two cases, refused under two keys, sharing only the guard site in
// `fieldAccess`:
//
//   - an enum. The static type names several variants and only the run-time tag
//     says which one is here, so the read is a tag switch.
//   - an existential. There is no tag: an `rt.Dyn` carries a *rt.TypeID, two
//     implementors are two layouts, and the read has to go through a table
//     keyed on identity. A bounded type parameter is the same case, because
//     dict.go lowers `T where T: H` to `existential(H)`.

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
		{"interface field requirement (a stdlib interface declares none)",
			"fn read(d: Display): String {\n  d.name\n}\n",
			"interface 'Display' has no field 'name'"},
		// A `field` requirement is a storage obligation and only a struct
		// declares storage, so `impl H for E` over an enum is a front-end
		// error (analysis/checker.go's validateImplBlockFieldRequirements).
		{"interface field requirement (an enum cannot satisfy one)",
			"interface H {\n  field name: String\n}\n\nenum E {\n  A {name: String}\n}\n\nimpl H for E\n\n" +
				"fn read(h: H): String {\n  h.name\n}\n",
			"'E' is an enum, so it declares no fields"},
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
