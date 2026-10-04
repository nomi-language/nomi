package irbuild

import (
	"strings"
	"testing"
)

// TestGenericInstanceNomiNameIsTheBareName pins the one thing about an instance
// def that is a wrong ANSWER rather than a build error.
//
// `nomi` is what the derived inspector emits and what qualifiedNomiName
// module-qualifies. A `Box<Int>` renders as `Box{item: 7}`, so an instance's
// Nomi name is the bare declaration name. Spelling it `Box<Int>` here, which
// is what stdgenstruct.go chooses for the std family, would re-render every
// assertion operand and every dispatch identity.
//
// Read off the def rather than out of the emitted text, because the emitted text
// only shows it once an assertion FAILS: the fixture's golden record covers
// that direction and this covers the def.
func TestGenericInstanceNomiNameIsTheBareName(t *testing.T) {
	src := "pub struct Box<T> {\n  item: T\n}\n\n" +
		"fn peek(b: Box<Int>): Int {\n  b.item\n}\n"
	g := lowerableGen(t, src)
	inst := onlyGenericInstance(t, g)
	if inst.nomi != "Box" {
		t.Fatalf("instance Nomi name is %q, want %q: the derived inspector emits this string and "+
			"Debug renders `Box{item: 7}` for a Box<Int>, so any other spelling re-renders "+
			"every assertion operand", inst.nomi, "Box")
	}
}

// TestGenericInstanceTypeIDIsModuleQualified pins the dispatch identity.
//
// Every impl lookup that goes through rt keys on `rt.TypeID{Nomi: …}`, whose
// string is qualifiedNomiName's answer. An empty or bare name there misses every
// impl SILENTLY — Display renders the anonymous form, `==` degrades to a
// structural comparison, ordering traps — so it is asserted rather than
// inspected.
func TestGenericInstanceTypeIDIsModuleQualified(t *testing.T) {
	src := "pub struct Box<T> {\n  item: T\n}\n\n" +
		"fn peek(b: Box<Int>): Int {\n  b.item\n}\n"
	g := lowerableGen(t, src)
	inst := onlyGenericInstance(t, g)
	got := g.qualifiedNomiName(inst.nomi)
	if !strings.HasSuffix(got, ".Box") || got == ".Box" {
		t.Fatalf("the instance's dispatch identity is %q, want a module-qualified `<module>.Box`: "+
			"identity is module-qualified and every impl lookup keys on it, so a bare or empty "+
			"name misses every impl with no diagnostic", got)
	}
}

// TestGenericInstanceAtATypeParameterIsRefused pins the scope line: an
// instantiation at another declaration's type parameter.
//
// `Box<T>` inside a generic function has no concrete argument, so there is
// nothing to monomorphize at, and building an instance whose field kind is
// `tagTypeParam` would produce a field nobody designed a representation for.
//
// Called directly rather than driven from source. The source spelling,
// `fn peek<U>(b: Box<U>)`, never reaches this gate: generic.go refuses that
// declaration as `generic function` for its own reason (a bound-free type
// parameter mentioned inside a container is at the wrong depth). The gate is
// defensive, and a defensive guard can only be exercised at the seam it
// defends.
func TestGenericInstanceAtATypeParameterIsRefused(t *testing.T) {
	src := "pub struct Box<T> {\n  item: T\n}\n\n" +
		"fn peek(b: Box<Int>): Int {\n  b.item\n}\n"
	g := lowerableGen(t, src)
	tpl := g.genericTemplates["Box"]
	if tpl == nil {
		t.Fatal("no `Box` template, so this test has nothing to gate")
	}
	tp := typeParamKind(&typeParamDef{nomi: "U"})
	if k, built := g.genericInstance(tpl, []kind{tp}); built || k != kindInvalid {
		t.Fatalf("genericInstance built %s at a bare type parameter. There is no concrete argument "+
			"at that position, so the instance's `item` field would be a Go `any` — a "+
			"representation nothing in this package emits or reads", k.nomi())
	}
}

// TestGenericInstanceNeverSilentlyDropsAField is the second half of the
// resolveStructFields split, and it is a wrong-ANSWER guard rather than a
// missing-refusal one.
//
// resolveStructFields takes the set of type-parameter names whose mention makes
// an unrepresentable field kind somebody else's refusal. A DECLARATION passes its
// own parameters, so `struct Box<T> { item: T }` is not also reported under
// `non-scalar field type`. An INSTANCE must pass nil, because every parameter has
// been substituted and an invalid kind is a genuine gap.
//
// # The assertion is on the def
//
// "The file refuses" does not discriminate here: a witness field can refuse
// through a path that fires either way (a `Set<T>` field has
// `stdGenStructTypeOf` calling `g.reject` directly; a generic enum's own
// declaration is replayed by typeDecl). The invariant does: an instance's
// member count equals the template's, or the instance is not lowerable. So the
// guard reads `g.genericInstOrder` and the template's own declaration, and
// ignores the refusal list. The expected count comes from the template's
// declaration, never from the instance being judged.
//
// # Enums too
//
// A generic enum instance drops a variant by the same mechanism a struct drops
// a field (`resolveEnumVariants` `continue`s past a payload with no kind), and
// the consequence is worse: a missing variant is a missing tag, and the case
// tree then falls through to `rt.NoCaseMatch` on a value the program
// constructed. So the enum rows are here rather than in a parallel test that
// could drift.
func TestGenericInstanceNeverSilentlyDropsAField(t *testing.T) {
	// The unrepresentable member is `Literal<T, T>`, a generic interface
	// existential. A generic interface has no existential representation: the
	// declaration is refused `generic interface`, so typeOf answers kindInvalid
	// for `Literal<Int, Int>` and typeRefusal names it. If that type gains a
	// kind, this guard fails with its own message and a member with no
	// representation must be chosen instead.
	src := "import std/literals.Literal\n\n" +
		"pub struct Point {\n  x: Int\n}\n\n" +
		"pub struct Bag<T> {\n  marker: Literal<T, T>\n}\n\n" +
		"pub struct Box<T> {\n  item: T\n}\n\n" +
		"pub enum Holder<T> {\n  Held T\n  Nothing\n}\n\n" +
		"pub enum Sack<T> {\n  Stuff Literal<T, T>\n  Empty\n}\n\n" +
		"fn size(_b: Bag<Point>): Int {\n  0\n}\n\n" +
		"fn peek(b: Box<Int>): Int {\n  b.item\n}\n\n" +
		"fn hold(_h: Holder<Int>): Int {\n  0\n}\n\n" +
		"fn sack(_s: Sack<Point>): Int {\n  0\n}\n"
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	g := newGen(&p.Modules[0], "nomimod0", nil, nil, -1, nil)
	g.declareTypes()
	g.declareFuncs()
	g.emitModule(&p.Modules[0])
	if len(g.genericInstOrder) < 4 {
		t.Fatalf("built %d instances, want at least the representable `Box<Int>`/`Holder<Int>` and the "+
			"unrepresentable `Bag<Point>`/`Sack<Point>`: with fewer than all four this guard cannot tell a "+
			"dropped member from an instance that was never attempted", len(g.genericInstOrder))
	}
	sawUnlowerable, sawEnum := false, false
	for _, d := range g.genericInstOrder {
		want, got, member := 0, 0, "field"
		switch {
		case d.genericOf.structDecl() != nil:
			want, got = len(d.genericOf.structDecl().Fields), len(d.fields)
		case d.genericOf.enumDecl() != nil:
			want, got, member = len(d.genericOf.enumDecl().Variants), len(d.variants), "variant"
			sawEnum = true
		default:
			t.Fatalf("instance %s has a template whose declaration is neither a struct nor an enum", d.nomi)
		}
		if !d.lowerable {
			sawUnlowerable = true
			continue
		}
		if got != want {
			t.Errorf("lowerable instance %s (Nomi %s) has %d %ss, and its template declares %d. "+
				"A %s with no kind was DROPPED instead of refusing the instance, so the emitted "+
				"Go type is missing it and every read of it answers for a %s nobody declared",
				d.nomi, d.nomi, got, member, want, member, member)
		}
	}
	if !sawUnlowerable {
		t.Error("every instance was lowerable, so the unrepresentable `Literal<T, T>` member did not " +
			"produce one — this guard passed without exercising the branch it guards. If a " +
			"generic interface existential has gained a kind, pick a member with no " +
			"representation here. Do not delete the check")
	}
	if !sawEnum {
		t.Error("no ENUM instance was built, so the variant half of this guard ran over an empty " +
			"population and would pass with resolveEnumVariants deleted")
	}
}

// TestGenericInstanceIsInternedPerInstantiation pins that two mentions of one
// instantiation are ONE def and two instantiations are TWO.
//
// The first half is what makes an instance's Go type emitted once; the second is
// what stops `Box<Int>` and `Box<String>` sharing a layout. A key on the
// template alone would pass the first and silently fail the second, which is
// the failure mode sharedGenStructDefs' comment describes.
func TestGenericInstanceIsInternedPerInstantiation(t *testing.T) {
	src := "pub struct Box<T> {\n  item: T\n}\n\n" +
		"fn a(b: Box<Int>): Int {\n  b.item\n}\n\n" +
		"fn b(c: Box<Int>): Int {\n  c.item\n}\n\n" +
		"fn c(d: Box<String>): String {\n  d.item\n}\n"
	g := lowerableGen(t, src)
	if n := len(g.genericInstOrder); n != 2 {
		var got []string
		for _, d := range g.genericInstOrder {
			got = append(got, d.nomi)
		}
		t.Fatalf("built %d instances %v, want 2: Box<Int> is mentioned twice and must be interned, "+
			"and Box<String> must not share its def", n, got)
	}
	a, b := g.genericInstOrder[0], g.genericInstOrder[1]
	if a.fields[0].k == b.fields[0].k {
		t.Fatalf("Box<Int> and Box<String> share the field kind %s", a.fields[0].k.nomi())
	}
}

// --- helpers ----------------------------------------------------------------

// lowerableGen lowers src and returns the gen, failing with the refusals.
//
// The gen rather than a program's output, because several tests are about a
// def's fields (a name, an identity, an intern count), and none of those is
// visible in what a program prints.
func lowerableGen(t *testing.T, src string) *gen {
	t.Helper()
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	g := newGen(&p.Modules[0], "nomimod0", nil, nil, -1, nil)
	g.declareTypes()
	g.declareFuncs()
	if errs, _ := g.emitModule(&p.Modules[0]); len(errs) > 0 {
		var got []string
		for _, e := range errs {
			got = append(got, e.Construct+"("+e.Detail+")")
		}
		t.Fatalf("refused: %v", got)
	}
	return g
}

// onlyGenericInstance is the one instance the source built, or a failure naming
// how many there were.
func onlyGenericInstance(t *testing.T, g *gen) *typeDef {
	t.Helper()
	if len(g.genericInstOrder) != 1 {
		t.Fatalf("built %d generic instances, want exactly 1: the test's claim is about ONE def and "+
			"reading g.genericInstOrder[0] out of several would be reading whichever came first",
			len(g.genericInstOrder))
	}
	return g.genericInstOrder[0]
}
