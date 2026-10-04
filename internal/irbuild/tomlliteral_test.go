package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// `std/toml` lowers. `Toml` is an opaqueSpecs row (see opaque.go on why the
// OPAQUE clause is not a representation boundary), and `Fragment<Display>` at a
// stdlib signature is admitted because an existential is package-neutral. That
// is safe only because the intern tables key on a kind's components rather than
// its rendered text: every existential renders alike, so a text key would merge
// `List<Display>` with `List<Comparable>`. erased_test.go carries that half.

// TestOpaqueSpecFormIsRequiredAndChecked pins a row's declaration `form`, in
// both directions.
//
// A row's `form` is what keeps the widening from being "this table now
// redirects any std declaration whose name matches". Two claims:
//
//   - Every row NAMES a clause. The zero value matches nothing, so a row added
//     without the field anchors nothing and every mention refuses — but a
//     reviewer should see the omission here rather than in a coverage drop.
//   - The clause is CHECKED against std. A declaration of the other clause
//     must not match, which is what makes a std edit from `pub type Toml
//     String` to `pub opaque type Toml String` produce no anchor instead of a
//     silently different use-site surface.
func TestOpaqueSpecFormIsRequiredAndChecked(t *testing.T) {
	lib := std.Load()
	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		if s.form == formUnset {
			t.Errorf("%s.%s: no declaration form; the zero value matches nothing, so this row is inert",
				s.origin, s.nomi)
			continue
		}
		module := strings.TrimPrefix(s.origin, "std/")
		fa := lib.Files[module]
		if fa == nil {
			t.Fatalf("%s: std has no module %q", s.nomi, module)
		}
		byDecl, byName := opaqueAnchors(fa)
		if _, anchored := byName[s.nomi]; !anchored {
			t.Fatalf("%s: no anchor built from %s", s.nomi, s.origin)
		}
		// The declaration std really carries must agree with the row's clause.
		var decl *ast.TypeDef
		for d, idx := range byDecl {
			if idx == i {
				decl = d
			}
		}
		if decl == nil {
			t.Fatalf("%s: anchored by name but not by declaration", s.nomi)
		}
		wantOpaque := s.form == formOpaque
		if decl.Opaque != wantOpaque {
			t.Errorf("%s: row says opaque=%t, std declares opaque=%t", s.nomi, wantOpaque, decl.Opaque)
		}

		// And the OTHER clause must not match, which is the half that makes
		// the field a check rather than a label.
		flipped := *decl
		flipped.Opaque = !decl.Opaque
		if s.matches(&flipped) {
			t.Errorf("%s: a `%s` declaration matched a row declared for the other clause; "+
				"a std edit to the clause would go unnoticed", s.nomi, clauseName(!wantOpaque))
		}
	}
}

func clauseName(opaque bool) string {
	if opaque {
		return "pub opaque type"
	}
	return "pub type"
}
