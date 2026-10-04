package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// operGen lowers src and returns the gen, so the impl TABLES can be read.
//
// Table inspection rather than lowered output: the claim under test is about
// which map a def is registered in, and any output would only ever be indirect
// evidence.
func operGen(t *testing.T, src string) (*gen, []Unsupported) {
	t.Helper()
	p, err := AnalyzeSource("operimpl_probe", src)
	if err != nil {
		t.Fatalf("the fixture does not analyze, so every assertion below would be vacuous: %v", err)
	}
	g := newGen(p.Entry(), "nomimod0", stdlibLowering(), nil, -1, nil)
	g.declareTypes()
	g.declareFuncs()
	errs, _ := g.emitModule(p.Entry())
	return g, errs
}

// The fixture the whole file is about: ONE receiver, THREE `Add` impls at three
// different right-hand types, which is the shape `implsByIface`'s key collapses.
const operThreeAdds = `type Day Int

fn day_value(day: Day): Int {
  Day(n) = day
  n
}

type Days Int

fn days_value(days: Days): Int {
  Days(n) = days
  n
}

type Weeks Int

fn weeks_value(weeks: Weeks): Int {
  Weeks(n) = weeks
  n
}

impl Add<Days, Day> for Day {
  fn add(lhs: Day, rhs: Days): Day {
    Day(day_value(lhs) + days_value(rhs))
  }
}

impl Add<Weeks, Day> for Day {
  fn add(lhs: Day, rhs: Weeks): Day {
    Day(day_value(lhs) + weeks_value(rhs) * 7)
  }
}

impl Subtract<Days, Day> for Day {
  fn subtract(lhs: Day, rhs: Days): Day {
    Day(day_value(lhs) - days_value(rhs))
  }
}
`

// TestOperImpl_OperatorImplsNeverEnterImplsByIface is the guard on operator
// impls having their own index, and it is checkable in one line rather than
// being a property of eight consultation sites.
//
// The alternative is widening `implsByIface`'s key with the interface's type
// arguments. `noEquatableImpl` reads `implsByIface["Equatable"][k]` to decide
// whether `==` may take the STRUCTURAL fallback, and under a widened key a
// lookup that lost its existential over the rhs axis answers "no impl" for a
// type that HAS one: a wrong answer on a comparison that lowers, not a
// refusal, and invisible to the corpus unless a fixture happens to compare two
// values of such a type.
//
// The second index cannot produce that failure BY CONSTRUCTION, and this is that
// construction asserted: no key of `implsByIface` names an operator interface,
// on a program that registers three of them.
func TestOperImpl_OperatorImplsNeverEnterImplsByIface(t *testing.T) {
	g, errs := operGen(t, operThreeAdds)
	if len(errs) > 0 {
		t.Fatalf("the fixture must lower for this assertion to be about anything: %v", errs)
	}
	for _, iface := range []string{"Add", "Subtract", "Multiply", "Divide"} {
		if byRecv, present := g.implsByIface[iface]; present {
			t.Errorf("implsByIface has a %q row (%d receiver(s)). The whole argument for a "+
				"second index is that every lookup in this map is unchanged; a row here voids "+
				"it, and noEquatableImpl fails to a WRONG ANSWER rather than to a build error",
				iface, len(byRecv))
		}
	}
	// The POSITIVE control, without which the loop above passes on a program
	// that registered nothing at all, which is how a broken `self` annotation
	// check stays hidden.
	if len(g.operImpls) != 3 {
		t.Fatalf("g.operImpls holds %d impl(s), want 3; the assertion above is vacuous "+
			"unless the three blocks were really admitted", len(g.operImpls))
	}
}

// TestOperImpl_ThreeAddsForOneReceiverAllSurvive holds that three `Add` impls
// for one receiver all lower, each with its own item.
//
// `implsByIface`'s duplicate arm marks the PRIOR def unlowerable as well as the
// arriving one, so keying operator impls there would lose all THREE.
func TestOperImpl_ThreeAddsForOneReceiverAllSurvive(t *testing.T) {
	g, errs := operGen(t, operThreeAdds)
	if len(errs) > 0 {
		t.Fatalf("lowering failed: %v", errs)
	}
	items := map[*implItem]string{}
	for _, d := range g.implOrder {
		if d.oper == nil {
			continue
		}
		if !d.lowerable {
			t.Errorf("%s is not lowerable: %s | %s", d.operLabel(), d.why, d.whyDetail)
			continue
		}
		it := d.items[d.oper.method]
		if it == nil {
			t.Errorf("%s has no %s item", d.operLabel(), d.oper.method)
			continue
		}
		items[it] = d.operLabel()
	}
	if len(items) != 3 {
		t.Fatalf("%d operator impl function(s), want 3: %v", len(items), items)
	}
}

// TestOperImpl_TheFourInterfacesAnchor is the POSITIVE control on the identity
// check, and it exists because that check fails CLOSED.
//
// An anchor that cannot be established declines, and declining refuses the
// operator impl without an error, so a broken identity check is SILENT. An
// `isSelfTypeExpr` that tested `*ast.SimpleType` when `self` is `*ast.SelfType`
// would make `matchesDecl` false for all four interfaces with no error
// anywhere. Only a positive assertion catches that.
func TestOperImpl_TheFourInterfacesAnchor(t *testing.T) {
	const src = `type Score Int

fn score_value(s: Score): Int {
  Score(n) = s
  n
}

impl Add<Score, Score> for Score {
  fn add(lhs: Score, rhs: Score): Score {
    Score(score_value(lhs) + score_value(rhs))
  }
}
`
	p, err := AnalyzeSource("operimpl_anchor", src)
	if err != nil {
		t.Fatalf("the fixture does not analyze: %v", err)
	}
	fa := p.Entry().FA
	if fa == nil || fa.ModuleScope == nil {
		t.Fatal("no module scope, so no anchor can be established and every row below is vacuous")
	}
	for _, s := range operIfaceSpecs {
		got, anchored := operIfaceAnchoredIn(fa, s.nomi)
		if !anchored {
			t.Errorf("%s did not anchor. Either std moved the declaration or the shape check "+
				"drifted; matchesDecl fails CLOSED, so this is the only thing that notices",
				s.nomi)
			continue
		}
		if got.module != s.module || got.method != s.method || got.op != s.op {
			t.Errorf("%s anchored the wrong spec: %+v", s.nomi, got)
		}
	}
	// The PAIRED NEGATIVE: a name that is not an operator interface must not
	// anchor, so the loop above is not passing because the function says yes to
	// everything.
	if s, anchored := operIfaceAnchoredIn(fa, "Display"); anchored {
		t.Errorf("Display anchored as an operator interface (%+v); the identity check "+
			"is answering on the name alone", s)
	}
}

// TestOperImpl_TheFourNamesAreReserved is the identity check's SECOND half, and
// it turns out to be a stronger fact than the check itself.
//
// `operIfaceAnchoredIn` requires the name to resolve through an IMPORT of the
// declaring std module, so a user's own `interface Add<R, O>` anchors nothing.
// MEASURED, and it makes that belt redundant: the analyzer REJECTS the
// declaration outright — "type name 'Add' is reserved by the language and cannot
// be redeclared as an interface". So the collision is unreachable at the front
// end for all four names, which is what makes `operIfaceByName` — a NAME-keyed
// map — safe.
//
// Asserted rather than assumed, on stdiface_test.go's footing: the safety of a
// name-keyed table rests on a collision being UNWRITABLE, and nothing else in
// this package would notice `checkReservedTypeName` losing a row.
func TestOperImpl_TheFourNamesAreReserved(t *testing.T) {
	for _, s := range operIfaceSpecs {
		t.Run(s.nomi, func(t *testing.T) {
			src := "pub interface " + s.nomi + "<Rhs, Out> {\n  fn " + s.method +
				"(lhs: self, rhs: Rhs): Out\n}\n"
			if _, err := AnalyzeSource("operimpl_reserved", src); err == nil {
				t.Errorf("the analyzer accepted `pub interface %s`. operIfaceByName is "+
					"name-keyed, so a declarable spec name is a reachable wrong answer "+
					"rather than a coverage gap — and the import check in "+
					"operIfaceAnchoredIn is then the only thing standing between a user's "+
					"interface and the operator dispatch route", s.nomi)
			}
		})
	}
}

// TestOperImpl_TheFourFrontEndFencesStillHold is the other half of the
// measurement above, and it is what makes those four clauses honest to keep.
//
// Each row is a shape `operShapeRefusal` guards against and the FRONT END
// currently rejects. If a row starts analyzing clean, the builder's clause
// becomes the live check — which is fine, and the point is that this test then
// fails and says so rather than the clause silently changing category.
func TestOperImpl_TheFourFrontEndFencesStillHold(t *testing.T) {
	const decls = `type Score Int
type Other Int

fn score_value(s: Score): Int {
  Score(n) = s
  n
}
`
	for _, row := range []struct{ name, impl string }{{
		"the rhs disagrees with the header",
		"impl Add<Score, Score> for Score {\n  fn add(lhs: Score, rhs: Other): Score { _ = rhs;\n" +
			"    Score(score_value(lhs))\n  }\n}\n",
	}, {
		"the result disagrees with the header",
		"impl Add<Score, Score> for Score {\n  fn add(lhs: Score, rhs: Score): Other { _ = rhs;\n" +
			"    Other(score_value(lhs))\n  }\n}\n",
	}, {
		"a second function in the block",
		"impl Add<Score, Score> for Score {\n" +
			"  fn add(lhs: Score, rhs: Score): Score { _ = rhs;\n    Score(score_value(lhs))\n  }\n" +
			"  fn extra(lhs: Score): Score {\n    lhs\n  }\n}\n",
	}, {
		"the wrong method name",
		"impl Add<Score, Score> for Score {\n  fn plus(lhs: Score, rhs: Score): Score { _ = rhs;\n" +
			"    Score(score_value(lhs))\n  }\n}\n",
	}} {
		t.Run(row.name, func(t *testing.T) {
			if _, err := AnalyzeSource("operimpl_fence", decls+row.impl); err == nil {
				t.Errorf("the front end ACCEPTS this shape, so operShapeRefusal's " +
					"matching clause is a live route rather than a fence. That is " +
					"not a failure of the builder — add a positive refusal assertion for it " +
					"beside TestOperImpl_AShapeMismatchIsRefusedByName and move this row out")
			}
		})
	}
}

// TestOperImpl_TwoImplsAtOneRhsAreAFrontEndError holds that `operKey`'s
// coherence rule is a FENCE: the front end rejects the shape first.
//
// `impl Add<Days, Day> for Day` beside `impl Add<Days, Days> for Day` differs
// only in the output. Both register under one runtime dispatch key, so the
// program cannot run. `analysis`' coherence key keys operator impls on
// (interface, receiver, RHS), via analysis.DispatchImplKey, so the program is a
// compile error and this shape cannot reach the builder from source.
//
// `operShapeRefusal`'s duplicate clause stays, on the same grounds as the four
// rows above: it is defence in depth for a front end that might change.
//
// The `Out` axis being excluded from the key is not a simplification; it is
// the spec's rule for every generic interface (docs/spec.md,
// "Generic Interfaces": the type parameter "is determined by each implementor …
// never written on the interface name where the implementor is already known"),
// and `Out` remains a real parameter in bound position (`where L: Add<R, Out>`).
func TestOperImpl_TwoImplsAtOneRhsAreAFrontEndError(t *testing.T) {
	const src = `type Day Int
type Days Int

fn day_value(d: Day): Int {
  Day(n) = d
  n
}

fn days_value(d: Days): Int {
  Days(n) = d
  n
}

impl Add<Days, Day> for Day {
  fn add(lhs: Day, rhs: Days): Day {
    Day(day_value(lhs) + days_value(rhs))
  }
}

impl Add<Days, Days> for Day {
  fn add(lhs: Day, rhs: Days): Days {
    Days(day_value(lhs) + days_value(rhs))
  }
}
`
	_, analyzeErr := AnalyzeSource("operimpl_outaxis", src)
	if analyzeErr == nil {
		t.Fatal("the front end ACCEPTS two operator impls differing only in Out. That " +
			"program has two bodies for one dispatch slot, so this is a defect, not " +
			"a relaxation — see analysis/impl_coherence.go's detectImplCollisions")
	}
	msg := analyzeErr.Error()
	for _, want := range []string{"Add<Days, Day>", "Add<Days, Days>", "OUTPUT type"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the front-end rejection does not name %q; the diagnostic is the "+
				"deliverable, not just the rejection: %s", want, msg)
		}
	}
}

// TestOperImpl_TheOperatorRouteClaimsOnlyTheFour keeps the admission narrow.
//
// A user GENERIC interface impl lowers through interface monomorphization
// (`ifaceTemplate` / `ifaceInstanceFor`), one arm above the operator admission.
// The operator route must claim ONLY the four anchored stdlib interfaces. A user generic
// interface must reach `g.operImpls` never, whatever else happens to it.
func TestOperImpl_TheOperatorRouteClaimsOnlyTheFour(t *testing.T) {
	const src = `interface Pair<A, B> {
  fn combine(v: self, other: A): B
}

type Score Int

impl Pair<Score, Score> for Score {
  fn combine(v: Score, other: Score): Score { _ = other;
    v
  }
}
`
	g, _ := operGen(t, src)
	if len(g.operImpls) != 0 {
		for k, d := range g.operImpls {
			t.Errorf("a user generic interface impl was registered in the operator index "+
				"as %+v (%s). The admission must require an ANCHORED stdlib operator "+
				"interface, not merely a generic header with two type arguments",
				k, d.operLabel())
		}
	}
	for _, d := range g.implOrder {
		if d.oper != nil {
			t.Errorf("%s carries an operator spec (%+v) and its interface is a user's own",
				typeText(d.decl.Interface), d.oper)
		}
	}
}

// TestOperImpl_TheSelfAnnotationIsItsOwnNode pins the node type isSelfTypeExpr
// tests for, because a mismatch there is invisible to every other check.
//
// A wrong node type there makes `matchesDecl` return false, which DECLINES
// without an error. So the failure mode is silent, and the reading that would have shown it if it were false is the
// same reading that shows it when it is true. Asserted against std's own
// declaration rather than against a hand-written one.
func TestOperImpl_TheSelfAnnotationIsItsOwnNode(t *testing.T) {
	p, err := AnalyzeSource("operimpl_selfnode", "type Score Int\n")
	if err != nil {
		t.Fatalf("the fixture does not analyze: %v", err)
	}
	fa := p.Entry().FA
	if fa == nil || fa.ModuleScope == nil {
		t.Fatal("no module scope")
	}
	sym := fa.ModuleScope.Lookup("Add")
	if sym == nil {
		t.Fatal("std/add's Add is not in module scope, so std moved and the anchor is stale")
	}
	res := sym
	for res.Resolved != nil {
		res = res.Resolved
	}
	decl, isDecl := res.Node.(*ast.InterfaceDef)
	if !isDecl || len(decl.Methods) != 1 {
		t.Fatalf("Add resolved to %T with an unexpected shape", res.Node)
	}
	if _, isSelf := decl.Methods[0].Params[0].TypeAnnotation.(*ast.SelfType); !isSelf {
		t.Fatalf("`self` in std's own `fn add(lhs: self, ...)` is %T, not *ast.SelfType. "+
			"isSelfTypeExpr tests for *ast.SelfType and would fail CLOSED, silently, "+
			"since a failed anchor declines rather than refusing",
			decl.Methods[0].Params[0].TypeAnnotation)
	}
}
