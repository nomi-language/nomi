package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// A stdlib enum's variant imported WITHOUT its type name.
//
// `import std/decimal.RoundingMode.{Floor, HalfEven, Up}` binds three VARIANTS
// and does not bind `RoundingMode`. See stdenum.go's stdEnumSpecOfSymbol for
// why the variant's own resolution is a stronger anchor than a name lookup.

// TestStdEnumSpecOfSymbolChecksBothHalves tests the predicate DIRECTLY, and it
// is here because the end-to-end negative below does not reach it.
//
// A bare variant reference resolving to a USER enum is reachable only through
// a selective import from a SIBLING FILE, which AnalyzeSource cannot express: a
// local enum's variant must be qualified, and a qualified one resolves through
// g.types in lookupVariant and never arrives here. So no single-file program
// exercises the rejection path, and the Origin and shape checks need a direct
// test.
//
// Both halves are exercised by SUBSTITUTION against std's own declarations: the
// right shape under the wrong identity, and the right identity over the wrong
// shape. That is exactly the pair stdEnumSpecOfSymbol's two conditions decide,
// and neither is expressible end to end.
func TestStdEnumSpecOfSymbolChecksBothHalves(t *testing.T) {
	lib := std.Load()
	declOf := func(module, name string) *ast.EnumDef {
		t.Helper()
		fa := lib.Files[module]
		if fa == nil {
			t.Fatalf("std declares no module %q", module)
		}
		byDecl, _ := stdEnumAnchors(fa)
		for decl, i := range byDecl {
			if stdEnumSpecs[i].nomi == name {
				return decl
			}
		}
		t.Fatalf("no anchored declaration for %s in %s", name, module)
		return nil
	}
	roundingDecl := declOf("decimal", "RoundingMode")
	formDecl := declOf("strings", "NormalForm")

	cases := []struct {
		name   string
		sym    *analysis.Symbol
		decl   *ast.EnumDef
		anchor bool
	}{{
		// The control: std's own identity over std's own declaration.
		name:   "std identity over std declaration",
		sym:    &analysis.Symbol{Name: "HalfEven", Type: &analysis.EnumType{Origin: "std/decimal", Name: "RoundingMode"}},
		decl:   roundingDecl,
		anchor: true,
	}, {
		// WRONG IDENTITY, right shape. A user module declaring `RoundingMode`
		// with std's exact variants: the declaration passes every shape check
		// and the Origin is the only thing that says no. Without it a user's
		// value would lower as `rt.RoundingMode`.
		name:   "user origin over std's own declaration",
		sym:    &analysis.Symbol{Name: "HalfEven", Type: &analysis.EnumType{Origin: "main", Name: "RoundingMode"}},
		decl:   roundingDecl,
		anchor: false,
	}, {
		// Right identity, WRONG SHAPE. `NormalForm`'s declaration under
		// RoundingMode's identity: four variants where the spec says eight, so
		// the emitted tags would come from a table this declaration does not
		// have. Only the shape check refuses it.
		name:   "std identity over the wrong declaration",
		sym:    &analysis.Symbol{Name: "HalfEven", Type: &analysis.EnumType{Origin: "std/decimal", Name: "RoundingMode"}},
		decl:   formDecl,
		anchor: false,
	}, {
		// A name std has no row for at all.
		name:   "an unrelated enum",
		sym:    &analysis.Symbol{Name: "Red", Type: &analysis.EnumType{Origin: "main", Name: "Colour"}},
		decl:   roundingDecl,
		anchor: false,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got := stdEnumSpecOfSymbol(tc.sym, tc.decl)
			if got != tc.anchor {
				t.Fatalf("anchored=%v, want %v", got, tc.anchor)
			}
		})
	}
}
