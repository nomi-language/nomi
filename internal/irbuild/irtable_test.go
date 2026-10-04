package irbuild

// Tests for the builder's half of the shared IR's type and declaration table
// (irtable.go).
//
// Three properties, all of which can fail:
//
//  1. THE SUBTYPING AND FIT RULE LIVES IN `internal/ir`, not here. A package
//     that built the table and kept scoring candidates itself would pass the
//     corpus while the IR declaration was only a receipt for a decision made
//     elsewhere. So no production source may name the builder's own subtyping
//     or fit rule, and the check is over the SOURCE because that is where the
//     claim lives.
//  2. NO DECL-TO-implItem MAP EXISTS. The realization travels beside the node;
//     a field of that shape anywhere in the package is an AST-keyed side table
//     under a new key.
//  3. IR TYPE IDENTITY IS KIND EQUALITY, for every kind and not only for the
//     well-formed ones, because candidate selection compares `kind` values
//     with `==` semantics.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// irEmitterRuleNames are the names a builder-side subtyping and candidate-fit
// rule would carry: the fit tier, its three levels, the two scoring entry
// points and the subtyping predicate. The rule lives in `internal/ir`.
//
// Exact identifiers, never prefixes. `widensAll` (collections.go) is a
// DIFFERENT question, the widest element kind a list literal's members share,
// asked through `embedsVariant`, and a prefix match would fail this test for
// it.
var irEmitterRuleNames = map[string]bool{
	"implFit":      true,
	"fitNone":      true,
	"fitWidened":   true,
	"fitExact":     true,
	"argsFit":      true,
	"argsFitLevel": true,
	"widens":       true,
}

// TestIRTable_NoSubtypingRuleLivesInTheEmitter is the behavioural test of the
// difference between a table and a receipt.
//
// The detector's positive is planted on a SYNTHETIC source, so it shows the
// detector works without requiring the defect to be present in production.
//
// THE DETECTOR IS DECLARATIONS AND DISPATCHED NAMES, not every call and not
// every identifier. Not every call, because `fitExact` is a constant and
// `implFit` a type, so a rule written as `var best implFit = fitExact` would
// pass a call-site check. Not every identifier either: `collections.go` writes
// `if _, _, widens := embedsVariant(want, e.k); !widens`, so `widens` is a
// LOCAL NAME in this package for the boolean the variant lookup returns, and a
// local is not the rule. Comments are not identifiers, so a comment naming
// the rule does not trip it.
func TestIRTable_NoSubtypingRuleLivesInTheEmitter(t *testing.T) {
	fset := token.NewFileSet()
	found := map[string][]string{}
	for _, name := range emitterGoFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, hit := range irRuleNamesIn(f) {
			found[filepath.Base(name)] = append(found[filepath.Base(name)], hit)
		}
	}
	for file, names := range found {
		t.Errorf("%s names the builder's own subtyping or fit rule %v. The rule is "+
			"`ir.Table.Accepts` over `ir.Table.Widens`, selected by "+
			"`ir.Table.SelectOverload`, and this package supplies only the candidate "+
			"SET. A second copy here makes the IR declaration a receipt for a decision "+
			"this package still makes.",
			file, names)
	}

	// PLANT THE POSITIVE, on a source this test owns rather than on production
	// state. Without it the loop above passes for a detector that finds nothing.
	const planted = "package irbuild\n\nfunc (g *gen) widens(want, have kind) bool { return want == have }\n"
	pf, err := parser.ParseFile(token.NewFileSet(), "planted.go", planted, 0)
	if err != nil {
		t.Fatalf("parse the planted source: %v", err)
	}
	if hits := irRuleNamesIn(pf); len(hits) == 0 {
		t.Fatal("the detector found nothing in a source that declares `widens`, so the " +
			"assertion above holds vacuously and would pass for a package that had put " +
			"the whole rule back")
	}

	// And the NEGATIVE half of the same detector: `widensAll` must not match, or
	// the test would fail production for a function that is not the rule.
	const near = "package irbuild\n\nfunc (g *gen) widensAll(items []expr, want kind) bool { return true }\n"
	nf, err := parser.ParseFile(token.NewFileSet(), "near.go", near, 0)
	if err != nil {
		t.Fatalf("parse the near-miss source: %v", err)
	}
	if hits := irRuleNamesIn(nf); len(hits) != 0 {
		t.Errorf("the detector matched %v in a source that declares only `widensAll`, "+
			"which is the element-kind question and not the subtyping rule", hits)
	}
}

// irRuleNamesIn is every DECLARATION of the rule in f, plus every name
// dispatched to it: a func, type or var/const declaration, a selector (`g.widens`),
// or a bare call (`widens(a, b)`).
func irRuleNamesIn(f *ast.File) []string {
	var out []string
	add := func(name string) {
		if irEmitterRuleNames[name] {
			out = append(out, name)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.FuncDecl:
			add(e.Name.Name)
		case *ast.TypeSpec:
			add(e.Name.Name)
		case *ast.ValueSpec:
			for _, id := range e.Names {
				add(id.Name)
			}
		case *ast.SelectorExpr:
			add(e.Sel.Name)
		case *ast.CallExpr:
			if id, isIdent := e.Fun.(*ast.Ident); isIdent {
				add(id.Name)
			}
		}
		return true
	})
	return out
}

// TestIRTable_NoDeclToRealizationMapExists pins the other half.
//
// The chosen `*ir.Decl` and the `*implItem` that realizes it are returned
// together from one call and paired in a local variable. A PERSISTENT map of
// that shape, on the gen or at package scope, is an AST-keyed side table with
// a new key, and it is what would let a consumer recover the
// realization from the node instead of from the selection.
//
// Checked over the source rather than over behaviour, because the defect is a
// FIELD nobody has to read for it to be wrong: the moment one exists, the
// pairing stops being local and the representation stops carrying the answer.
func TestIRTable_NoDeclToRealizationMapExists(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range emitterGoFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			m, isMap := n.(*ast.MapType)
			if !isMap {
				return true
			}
			if !irTableTypeMentions(m.Key, "Decl") && !irTableTypeMentions(m.Key, "Symbol") {
				return true
			}
			t.Errorf("%s declares a map keyed on an IR %s. The realization of a declaration "+
				"— the lowered function, its defaults, its declared parameters — "+
				"travels BESIDE the node, because every producer of a named-domain node is "+
				"the function that performed the selection. A map here is a side table "+
				"keyed on an IR entity instead of on an AST node.",
				filepath.Base(name), irTableTypeText(m.Key))
			return true
		})
	}
}

// emitterGoFiles are the builder package's non-test Go sources.
func emitterGoFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		t.Fatal("no non-test Go sources found; this test would pass vacuously")
	}
	return out
}

// TestIRTable_TypeIdentityIsKindEquality is the third property.
//
// irtable.go's header claims IR type identity and `kind` equality are the same
// relation by construction. That must hold for malformed kinds too: two EQUAL
// kinds are an exact fit before the subtyping relation is asked anything, so
// an `irTypeOf` that answered nil for a malformed kind would decline a
// parameter and an argument that are equal malformed kinds.
//
// Both directions are asserted, because either alone is satisfiable by a
// degenerate mapping. Equal kinds returning different pointers would make a
// candidate decline where kind equality chooses it; unequal kinds returning
// one pointer would make it choose where kind equality declines, which is the
// direction irtable.go's header calls the one that cannot happen silently.
func TestIRTable_TypeIdentityIsKindEquality(t *testing.T) {
	g := &gen{}
	if got := g.irTypeOf(kindInvalid); got != nil {
		t.Errorf("irTypeOf(kindInvalid) = %v, want nil: a refused operand is not a candidate, "+
			"which is what ir.Table.Accepts reads a nil argument type as", got)
	}

	// The three malformed shapes besides tagInvalid. Each is a bug in this
	// package rather than a program somebody wrote, and each must still intern,
	// because kind equality compares them with `==`.
	malformed := []struct {
		name string
		k    kind
	}{
		{"a named kind with no typeDef", kind{tag: tagNamed}},
		{"an existential with no ifaceDef", kind{tag: tagIface}},
		{"a type parameter with no typeParamDef", kind{tag: tagTypeParam}},
	}
	for _, m := range malformed {
		first := g.irTypeOf(m.k)
		if first == nil {
			t.Errorf("%s has no IR type, so kind equality and IR type identity are not the "+
				"same relation for it, and equal kinds must be an EXACT fit", m.name)
			continue
		}
		if again := g.irTypeOf(m.k); again != first {
			t.Errorf("%s interned twice to two types; equal kinds must be one type", m.name)
		}
	}

	// Distinctness across the shapes, which is the other direction.
	seen := map[*ir.Type]string{}
	for _, m := range malformed {
		ty := g.irTypeOf(m.k)
		if ty == nil {
			continue
		}
		if prev, dup := seen[ty]; dup {
			t.Errorf("%s and %s share one IR type; unequal kinds must be two types, or a "+
				"candidate is chosen where kind equality declines it", m.name, prev)
		}
		seen[ty] = m.name
	}

	// And a well-formed pair, so the test is not only about the malformed
	// forms: two kinds that differ only in their tag are two types, and one
	// kind asked twice is one type.
	if a, b := g.irTypeOf(kindInt), g.irTypeOf(kindString); a == nil || b == nil || a == b {
		t.Errorf("Int and String interned to %v and %v; two scalars are two types", a, b)
	}
	if a, b := g.irTypeOf(kindBool), g.irTypeOf(kindBool); a != b {
		t.Errorf("Bool interned twice to %v and %v", a, b)
	}
}

// irTableTypeMentions reports whether a type expression names sel as a
// selector — `ir.Decl` for "Decl", `*ir.Symbol` for "Symbol".
func irTableTypeMentions(e ast.Expr, sel string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if s, isSel := n.(*ast.SelectorExpr); isSel && s.Sel.Name == sel {
			if pkg, isIdent := s.X.(*ast.Ident); isIdent && pkg.Name == "ir" {
				found = true
			}
		}
		return true
	})
	return found
}

func irTableTypeText(e ast.Expr) string {
	if s, isSel := e.(*ast.StarExpr); isSel {
		return irTableTypeText(s.X)
	}
	if s, isSel := e.(*ast.SelectorExpr); isSel {
		return s.Sel.Name
	}
	return "entity"
}
