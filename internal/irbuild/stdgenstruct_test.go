package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// The GENERIC stdlib struct family's spec guards.
//
// An end-to-end test of `Set` checks one row's OUTPUT and says nothing about
// whether the spec table still describes std. The guards here check the table,
// the shape check and the admission gate directly. The bound gate in
// `(*stdGenStructSpec).matches` in particular is asserted on constructed
// declarations (TestStdGenStructBoundGateIsConsulted), because a guard whose
// only witness is a spec row happening to declare a bound cannot distinguish a
// working gate from a deleted one.
//
// The monomorphic twins these are modelled on are in stdstruct_test.go, and each
// exists for the reason stated there: a spec that has drifted from std produces
// NO anchor, so every mention refuses and the builder is merely less capable,
// which no output-level test notices.

// TestStdGenStructSpecsMatchStdSource asserts the anchors are really built from
// the std source in this build.
//
// Without it, every claim the spec
// table makes about `std/sets.nomi` and `std/ranges.nomi` is unchecked in the
// failing direction: a renamed field, a reordered field, a changed bound or a
// moved declaration all produce a silent loss of capability.
func TestStdGenStructSpecsMatchStdSource(t *testing.T) {
	lib := std.Load()
	validated := stdGenStructValidated()
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		module := strings.TrimPrefix(s.origin, "std/")
		fa := lib.Files[module]
		if fa == nil {
			t.Fatalf("%s: std declares no module %q", s.nomi, module)
		}
		if !validated[i] {
			t.Errorf("%s.%s: the spec does not match std's declaration, so no anchor is built "+
				"and every mention refuses. Either the declaration moved or its shape changed; "+
				"compare stdGenStructSpec.matches against %s.nomi",
				s.origin, s.nomi, s.origin)
			continue
		}
		if _, anchored := stdGenStructAnchorsOf(fa)[s.nomi]; !anchored {
			t.Errorf("%s.%s: validated but not anchored in its OWN declaring module, so "+
				"stdGenStructDeclIn and stdGenStructValidated disagree about identity",
				s.origin, s.nomi)
		}
	}
}

// TestStdGenStructBoundGateIsConsulted checks that the bound gate ANSWERS, and
// it is written against CONSTRUCTED declarations rather than against std's.
//
// Why constructed: a guard that only reads std asserts what std happens to say,
// so it goes vacuous the moment a row's bound matches by luck. What has to be
// true is that a declaration whose bound differs from the spec's is refused, in
// both directions. Each case below is a wrong ANSWER rather than a compile error
// if admitted:
//
//	spec bound   decl bound                        must
//	none         none                              match    (Set)
//	none         where T: Comparable                REFUSE
//	Comparable   where T: Comparable                match    (Range)
//	Comparable   none                               REFUSE   a spec may not over-claim
//	Comparable   where T: Comparable and Discrete   REFUSE   widening
//	Comparable   where T: Hashable                  REFUSE   substitution
//
// The last three are the ones a `len(WhereClauses) > 0` gate could not express.
//
// # THE INLINE SPELLING IS NOT A PROGRAM
//
// The parser rejects an inline bound (`struct S<T: Comparable>`):
//
//	line 1: inline generic bounds are not supported; declare type parameters as
//	`<T>` and add a `where T: ...` clause after the signature
//
// boundsMatch still collects `TypeParams[i].Bounds`, deliberately: it is where
// the parser would put an inline bound if that restriction were lifted, and
// collecting both spellings into one set keeps the check independent of which
// one std uses. No source can populate that field, so no inline case is
// tested here.
func TestStdGenStructBoundGateIsConsulted(t *testing.T) {
	// Two real spec rows, so the cases below are about the gate and not about a
	// spec invented for the test. `Set` declares no bound and `Range` declares
	// exactly `Comparable`.
	set := specNamed(t, "Set")
	rng := specNamed(t, "Range")

	for _, c := range []struct {
		label string
		spec  *stdGenStructSpec
		src   string
		want  bool
	}{
		{"Set: std's own bound-free declaration", set,
			"pub opaque struct Set<T> {\n  items: Map<T, Bool>\n}\n", true},
		{"Set: a where clause the spec does not declare", set,
			"pub opaque struct Set<T> where T: Comparable {\n  items: Map<T, Bool>\n}\n", false},

		{"Range: std's own where clause", rng, rangeDecl("<T> where T: Comparable"), true},
		{"Range: the bound removed", rng, rangeDecl("<T>"), false},
		{"Range: the bound WIDENED", rng, rangeDecl("<T> where T: Comparable and Discrete"), false},
		{"Range: the bound SUBSTITUTED", rng, rangeDecl("<T> where T: Hashable"), false},
	} {
		decl := structDeclIn(t, c.src, c.spec.nomi)
		if got := c.spec.matches(decl); got != c.want {
			t.Errorf("%s: matches=%v, want %v.\n"+
				"The bound gate in (*stdGenStructSpec).matches is not deciding this. A gate that "+
				"admits a bound the spec did not declare lets the builder construct records "+
				"for a type whose methods it cannot resolve; one that rejects the bound the spec "+
				"DID declare silently removes the whole row's capability.", c.label, got, c.want)
		}
	}
}

// TestStdGenStructDefsArePackageNeutral guards the precondition that makes ONE
// process-wide *typeDef per INSTANTIATION sound.
//
// stdstruct_test.go's twin over the monomorphic family, and the property is the
// same one for the same reason — a shared def's fields must render to identical
// Go-spelled `kind` text in every gen. What differs is that here the instantiation
// supplies the field kinds, so the check has to be run at an ARGUMENT rather than
// read off the spec: `Int` for both rows, which is the argument the corpus
// reaches most and the one every other neutral argument behaves like.
func TestStdGenStructDefsArePackageNeutral(t *testing.T) {
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		k, shared := sharedGenStructInstance(s, []kind{kindInt})
		if !shared {
			t.Errorf("%s<Int> is not in the shared table, so two gens would build two "+
				"mutually unassignable Go types for one Nomi type", s.nomi)
			continue
		}
		d := k.def
		if !d.rtDeclared {
			t.Errorf("%s<Int>: not marked rtDeclared, so typeDecl would emit a second declaration",
				s.nomi)
		}
		if !k.packageNeutral() {
			t.Errorf("%s<Int>: the def is not package-neutral, so sharing it across gens is unsound",
				s.nomi)
		}
		if len(d.fields) != len(s.fields) {
			t.Errorf("%s<Int>: def has %d fields, spec has %d", s.nomi, len(d.fields), len(s.fields))
			continue
		}
		for j, f := range d.fields {
			if f.k == kindInvalid {
				t.Errorf("%s<Int>.%s has no kind, so the whole instantiation is a refusal",
					s.nomi, s.fields[j].nomi)
				continue
			}
			if !f.k.packageNeutral() {
				t.Errorf("%s<Int>.%s holds %s, which does not render the same in every package",
					s.nomi, s.fields[j].nomi, f.k.nomi())
			}
		}
	}
}

// TestStdGenStructDefaultGateIsConsulted asserts the field-DEFAULT agreement
// in matches(), in both directions and with a positive planted.
//
// A blanket refusal of any declared default and a working two-way check are
// indistinguishable from the false side alone: both answer
// `false` for a declaration carrying a default. So case 3 below is the plant —
// a spec that DOES declare the default must anchor — and it is the only case
// that can tell the two apart. Without it, deleting the whole clause and
// writing `return false` would pass this test.
//
// Built from the real `Set` row rather than an invented spec, so the cases are
// about the clause and not about a shape nothing else uses. `Set.items` is a
// `Map<T, Bool>`, so `= Map.empty()` is a default std COULD write and
// stdEmptyMapDefault is the matching row.
func TestStdGenStructDefaultGateIsConsulted(t *testing.T) {
	plain := *specNamed(t, "Set")
	// A copy of the row with a default on `items`. The fields slice is copied
	// too: the rows are package-level values and mutating the shared backing
	// array would leak into every other test in this package.
	defaulted := plain
	defaulted.fields = append([]stdGenStructField(nil), plain.fields...)
	defaulted.fields[0].deflt = stdEmptyMapDefault()
	// A spec whose default is a DIFFERENT expression, so the shape check has a
	// case of its own rather than riding on the presence check.
	wrongShape := plain
	wrongShape.fields = append([]stdGenStructField(nil), plain.fields...)
	wrongShape.fields[0].deflt = stdEmptyListDefault()

	const bare = "pub opaque struct Set<T> {\n  items: Map<T, Bool>\n}\n"
	const withDefault = "pub opaque struct Set<T> {\n  items: Map<T, Bool> = Map.empty()\n}\n"

	for _, c := range []struct {
		label string
		spec  *stdGenStructSpec
		src   string
		want  bool
	}{
		{"CONTROL: no spec default, no declared default", &plain, bare, true},
		{"FORWARD: std grew a default the spec does not know", &plain, withDefault, false},
		{"PLANT: spec and declaration both carry it", &defaulted, withDefault, true},
		{"REVERSE: the spec invents a default std does not declare", &defaulted, bare, false},
		{"SHAPE: both carry one and they are different expressions", &wrongShape, withDefault, false},
	} {
		decl := structDeclIn(t, c.src, "Set")
		if got := c.spec.matches(decl); got != c.want {
			t.Errorf("%s: matches=%v, want %v.\n"+
				"The field-default clause in (*stdGenStructSpec).matches is not deciding this. "+
				"A spec claiming a default std does not declare INVENTS a value at every "+
				"construction site that omits the field; a declaration carrying one the spec "+
				"does not know builds a zero value where the declaration evaluates an "+
				"expression. Neither is a compile error.", c.label, got, c.want)
		}
	}
}

// TestStdGenStructDefaultsReachTheDef asserts buildGenStructDef carries the
// spec's default onto the built field, through `stdDeflt` and never `deflt`.
//
// stdstruct_test.go's twin for the monomorphic family, and the exclusivity is
// the load-bearing half: `deflt` is an AST node types.go evaluates in the
// DECLARING module's scope, which across a module boundary is another file's
// scope entirely, and a field carrying both would silently take that path.
//
// THE TABLE LOOP ALONE IS VACUOUS FOR THE WIRING: with every shipping row's
// `deflt` nil, deleting `stdDeflt: f.deflt` from buildGenStructDef leaves the
// loop comparing nil against nil. The planted row below is what fails then.
func TestStdGenStructDefaultsReachTheDef(t *testing.T) {
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		d, built := buildGenStructDef(nil, s, []kind{kindInt})
		if !built {
			t.Errorf("%s<Int> has no def, so nothing about its defaults can be read", s.nomi)
			continue
		}
		for j := range d.fields {
			f := &d.fields[j]
			if f.deflt != nil {
				t.Errorf("%s.%s carries an AST default; a stdlib field's default must go "+
					"through stdDeflt, or it resolves its names in the CONSTRUCTING file",
					s.nomi, f.nomi)
			}
			if (f.stdDeflt != nil) != (s.fields[j].deflt != nil) {
				t.Errorf("%s.%s: def default=%v but spec default=%v",
					s.nomi, f.nomi, f.stdDeflt != nil, s.fields[j].deflt != nil)
			}
		}
	}
	// THE PLANT. A spec row that DOES carry a default, so the assignment in
	// buildGenStructDef has something to carry and its deletion is visible.
	planted := *specNamed(t, "Set")
	planted.fields = append([]stdGenStructField(nil), planted.fields...)
	planted.fields[0].deflt = stdEmptyMapDefault()
	d, built := buildGenStructDef(nil, &planted, []kind{kindInt})
	if !built {
		t.Fatal("the planted defaulted spec built no def, so the wiring cannot be read")
	}
	if d.fields[0].stdDeflt == nil {
		t.Error("buildGenStructDef dropped the spec's default: the def's field carries no " +
			"stdDeflt, so fillFieldDefaults would report `struct literal missing field` for " +
			"a field std declares a default for")
	}
}

// specNamed is the spec row for a Nomi name, by NAME rather than by index so a
// reorder of the table cannot repoint a case silently.
func specNamed(t *testing.T, nomi string) *stdGenStructSpec {
	t.Helper()
	for i := range stdGenStructSpecs {
		if stdGenStructSpecs[i].nomi == nomi {
			return &stdGenStructSpecs[i]
		}
	}
	t.Fatalf("no stdGenStructSpecs row named %q", nomi)
	return nil
}

// rangeDecl is `std/ranges.nomi`'s own Range declaration with its type-parameter
// list replaced, so each case below differs from std in exactly the bound.
//
// The FIELDS are std's verbatim, which matters: a case that also perturbed a
// field would fail for the field's reason and prove nothing about the gate.
func rangeDecl(params string) string {
	return "pub opaque struct Range" + params + " {\n" +
		"  start: T\n" +
		"  end: Maybe<T>\n" +
		"  inclusive: Bool\n" +
		"}\n"
}

// structDeclIn parses one source and answers the named struct's declaration.
//
// The PARSER rather than a hand-built ast.StructDef, because the two spellings of
// a bound are a parser distinction — `<T: C>` fills TypeParams[i].Bounds and
// `<T> where T: C` fills WhereClauses — and a hand-built node would encode this
// test's belief about which field each lands in rather than checking it.
func structDeclIn(t *testing.T, src, name string) *ast.StructDef {
	t.Helper()
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("declaration under test does not parse: %v", err)
	}
	for _, n := range nodes {
		if sd, ok := n.(*ast.StructDef); ok && sd.Name == name {
			return sd
		}
	}
	t.Fatalf("no struct %q in the parsed source", name)
	return nil
}
