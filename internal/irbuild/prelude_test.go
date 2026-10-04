package irbuild

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"

	"github.com/nomi-language/nomi/rt"
)

// The prelude-enum fixtures and the two guards that hold the representation
// against the things it is derived FROM: the std declaration, and rt.
//
// The differential harness cannot see a bug in anything implemented once, and
// the Go types `Maybe` and `Result` lower to are hand-written in rt — so the
// fixtures below pin the reference TEXT as well as agreement, and the two
// guards assert absolute values rather than comparing two computations.

// TestPreludeShapeMatchesStdSource asserts the anchors are BUILT.
//
// prelude.go refuses to lower anything when std/maybe.nomi or std/results.nomi
// stops having the shape it assumes — different type parameters, a reordered
// variant, a payload that stops naming the type parameter it named. That is
// the safe failure, and it is silent: every prelude construct simply goes back
// to being refused. This is the test that makes it loud, and it also spells out
// the shape so a reader can check it against the two .nomi files by eye.
func TestPreludeShapeMatchesStdSource(t *testing.T) {
	g := preludeGenForTest(t)
	// payloads spells what the STD SOURCE declares, so a reader can check it
	// against maybe.nomi / results.nomi / literals.nomi by eye: a type
	// parameter's name, a concrete type's name, or "-" for a bare variant.
	// It is deliberately the source spelling rather than the spec's `param`
	// index, because that index cannot express a CONCRETE payload and the
	// distinction is the one Fragment introduced.
	want := map[string]struct {
		params   []string
		variants []string
		payloads []string
	}{
		"Maybe":  {params: []string{"T"}, variants: []string{"Some", "None"}, payloads: []string{"T", "-"}},
		"Result": {params: []string{"T", "E"}, variants: []string{"Ok", "Err"}, payloads: []string{"T", "E"}},
		// `Static`'s payload is `String` and NOT `T`. Getting this pair the
		// wrong way round is invisible at `Fragment<String>`, which is the only
		// instantiation the corpus contains — see TestFragmentLayoutMatchesRT.
		"Fragment": {params: []string{"T"}, variants: []string{"Static", "Dynamic"}, payloads: []string{"String", "T"}},
		// `Failed`'s payload is the NAMED `Failure`, not a scalar and not `T`.
		// It is the shape that needed a fourth payload encoding; see
		// taskoutcome.go.
		"Outcome": {params: []string{"T"}, variants: []string{"Completed", "Cancelled", "Failed"},
			payloads: []string{"T", "-", "Failure"}},
	}
	if len(want) != len(preludeSpecs) {
		t.Fatalf("this test spells out %d specs, preludeSpecs has %d — a spec nobody checks against std can lower against a layout nobody wrote",
			len(want), len(preludeSpecs))
	}
	if len(g.preludeByName) != len(want) {
		t.Fatalf("anchored %d prelude enums, want %d — std does not have the shape prelude.go assumes, so every construct over the missing one is silently refused",
			len(g.preludeByName), len(want))
	}
	for name, w := range want {
		a, found := g.preludeByName[name]
		if !found {
			t.Fatalf("no anchor for %s", name)
		}
		if len(a.decl.TypeParams) != len(w.params) {
			t.Fatalf("%s has %d type params, want %d", name, len(a.decl.TypeParams), len(w.params))
		}
		for i, p := range w.params {
			if a.decl.TypeParams[i].Name != p {
				t.Fatalf("%s type param %d is %q, want %q", name, i, a.decl.TypeParams[i].Name, p)
			}
		}
		for i, v := range w.variants {
			if a.decl.Variants[i].Name != v {
				t.Fatalf("%s variant %d is %q, want %q — tags are declaration order, so a reorder silently renumbers them",
					name, i, a.decl.Variants[i].Name, v)
			}
			// The std source's own payload spelling.
			got := "-"
			if st, isSimple := a.decl.Variants[i].DataTypeExpr.(*ast.SimpleType); isSimple {
				got = st.Name
			}
			if got != w.payloads[i] {
				t.Fatalf("std declares %s.%s carrying %q, this test expects %q", name, v, got, w.payloads[i])
			}
			// And the SPEC's classification of that same payload, which is the
			// half that decides the layout. A `fixed` payload must be the
			// concrete kind the source names; a `param` payload must index the
			// parameter the source names.
			vs := a.spec.variants[i]
			switch {
			case w.payloads[i] == "-":
				if vs.carries() {
					t.Fatalf("%s.%s is bare in std, the spec gives it a payload", name, v)
				}
			case slices.Contains(w.params, w.payloads[i]):
				if vs.param < 0 || a.spec.params[vs.param] != w.payloads[i] {
					t.Fatalf("%s.%s carries type parameter %q in std; the spec says param=%d fixed=%v",
						name, v, w.payloads[i], vs.param, vs.fixed.nomi())
				}
				if vs.fixed != kindInvalid {
					t.Fatalf("%s.%s is parametric but the spec also fixes it to %s", name, v, vs.fixed.nomi())
				}
			case vs.namedPayload != nil:
				// A NAMED concrete payload. Checked BEFORE the scalar arm
				// because `vs.fixed` is kindInvalid for one, so the scalar
				// comparison below would report a confusing "fixes it to <>"
				// for a spec that is perfectly correct.
				if vs.param >= 0 {
					t.Fatalf("%s.%s carries the named %q in std; the spec makes it type parameter %d",
						name, v, w.payloads[i], vs.param)
				}
				if vs.namedPayload.nomi != w.payloads[i] {
					t.Fatalf("%s.%s carries %q in std; the spec names %q",
						name, v, w.payloads[i], vs.namedPayload.nomi)
				}
				// The co-declaration is what licenses the name check inside
				// `matches`, so it is asserted here rather than trusted.
				if vs.namedPayload.origin != a.spec.origin {
					t.Fatalf("%s.%s's payload %s is declared in %q but %s is declared in %q — "+
						"matches compares the payload by NAME and only the co-declaration makes "+
						"that an identity check",
						name, v, vs.namedPayload.nomi, vs.namedPayload.origin, name, a.spec.origin)
				}
			default:
				if vs.param >= 0 {
					t.Fatalf("%s.%s carries the concrete %q in std; the spec makes it type parameter %d",
						name, v, w.payloads[i], vs.param)
				}
				if vs.fixed.nomi() != w.payloads[i] {
					t.Fatalf("%s.%s carries %q in std; the spec fixes it to %s", name, v, w.payloads[i], vs.fixed.nomi())
				}
			}
		}
	}
}

// TestPreludeLayoutMatchesRT holds the builder's belief about rt against rt.
//
// The builder's Go-spelled `expr` text names fields by TEXT — `Tag`,
// `Some`, `Ok`, `Err`, `Static`, `Dynamic` — and rt declares the struct those
// names have to hit. A rename on either side would make the spec disagree with
// rt, and nothing that runs a program would notice. This reflects over the real rt types instead, so all three
// are checked here.
//
// Every registered instantiation below is at DISTINCT Go type arguments, and
// for Fragment that is load-bearing rather than tidy. `rt.Fragment[string]` has
// two `string` payload fields, so at that instantiation a spec with `Static`
// and `Dynamic`'s payload kinds swapped satisfies every field-name, field-type
// and field-count check here. `rt.Fragment[int64]` does not: `Static` must be
// `string` and `Dynamic` `int64`, and the swap fails. The corpus contains no
// `Fragment` at anything but `String`, so this registration is the only place
// that mutation can be caught.
func TestPreludeLayoutMatchesRT(t *testing.T) {
	byName := map[string]reflect.Type{
		"Maybe":    reflect.TypeOf(rt.Maybe[int64]{}),
		"Result":   reflect.TypeOf(rt.Result[int64, string]{}),
		"Fragment": reflect.TypeOf(rt.Fragment[int64]{}),
		// `rt.Outcome[string]`, and the argument is DELIBERATELY not int64.
		// `Failed` carries `rt.Failure`, a struct whose only payload field is
		// a string, so at `T = string` a spec that confused the parametric
		// `Completed` with the named `Failed` could still satisfy a
		// field-type check. At `T = string` the two fields are `string` and
		// `rt.Failure` — different Go types — so they cannot be swapped
		// silently. This is Fragment's registration argument applied to the
		// parametric/NAMED mix rather than the parametric/scalar one.
		"Outcome": reflect.TypeOf(rt.Outcome[string]{}),
	}
	for i := range preludeSpecs {
		spec := &preludeSpecs[i]
		got, found := byName[spec.nomi]
		if !found {
			t.Fatalf("no rt type registered in this test for %s", spec.nomi)
		}
		if _, ok := got.FieldByName(spec.tagField); !ok {
			t.Fatalf("%s has no field %q, which is what the builder names the tag field", got, spec.tagField)
		}
		fields := 1
		for _, v := range spec.variants {
			if !v.carries() {
				continue
			}
			fields++
			f, ok := got.FieldByName(v.field)
			if !ok {
				t.Fatalf("%s has no field %q for variant %s", got, v.field, v.nomi)
			}
			if !f.IsExported() {
				t.Fatalf("%s.%s is unexported; generated code is in another package", got, v.field)
			}
			if v.namedPayload != nil {
				// A NAMED concrete payload: rt's field must be exactly the Go
				// type the spec holds. Compared as a reflect.Type and not
				// through kindOfGoType, because kindOfGoType answers
				// kindInvalid for a type it does not know and `v.fixed` is
				// ALSO kindInvalid here, so the scalar assertion below would
				// pass vacuously for every named payload.
				if f.Type != v.namedPayload.goType {
					t.Fatalf("%s.%s is %s, but the spec says %s.%s carries %s",
						got, v.field, f.Type, spec.nomi, v.nomi, v.namedPayload.goType)
				}
				continue
			}
			if v.param < 0 {
				// A CONCRETE payload: the spec fixed its type, so rt's field
				// has to have exactly that type. This is the assertion the
				// swapped-payload mutation fails, and it can only fail at an
				// instantiation where the concrete and parametric payloads
				// differ — see the registrations above.
				if k := kindOfGoType(f.Type); k != v.fixed {
					t.Fatalf("%s.%s is %s, but the spec fixes %s.%s to %s",
						got, v.field, f.Type, spec.nomi, v.nomi, v.fixed.nomi())
				}
			}
		}
		if got.NumField() != fields {
			t.Fatalf("%s has %d fields, the builder accounts for %d — an unaccounted field is storage the builder never fills",
				got, got.NumField(), fields)
		}
	}
	// rt's own constructors, spelled out rather than derived: these are the
	// values a generated `case` arm compares against.
	if rt.Some[int64](0).Tag != 1 || rt.None[int64]().Tag != 2 {
		t.Fatal("rt disagrees with Some=1 None=2")
	}
	if rt.Ok[int64, string](0).Tag != 1 || rt.Err[int64, string]("").Tag != 2 {
		t.Fatal("rt disagrees with Ok=1 Err=2")
	}
	if rt.TagStatic != 1 || rt.TagDynamic != 2 {
		t.Fatal("rt disagrees with Static=1 Dynamic=2")
	}

	// And the BUILDER's own assignment, against those same values. This is
	// the assertion that closes the loop, and it is one an output comparison
	// cannot make: a `Maybe` the builder constructs and a `Maybe` built by
	// rt.MapGet are one type, so if the two disagreed about which tag means
	// Some, a program would be right only when the value never crossed the
	// boundary, and silently wrong when it did.
	g := preludeGenForTest(t)
	m := g.preludeInstance(g.preludeByName["Maybe"], []kind{kindInt}).def
	if got := m.variant("Some").tag; got != int(rt.TagSome) {
		t.Fatalf("the builder assigns Some tag %d, rt says %d", got, rt.TagSome)
	}
	if got := m.variant("None").tag; got != int(rt.TagNone) {
		t.Fatalf("the builder assigns None tag %d, rt says %d", got, rt.TagNone)
	}
	r := g.preludeInstance(g.preludeByName["Result"], []kind{kindInt, kindString}).def
	if got := r.variant("Ok").tag; got != int(rt.TagOk) {
		t.Fatalf("the builder assigns Ok tag %d, rt says %d", got, rt.TagOk)
	}
	if got := r.variant("Err").tag; got != int(rt.TagErr) {
		t.Fatalf("the builder assigns Err tag %d, rt says %d", got, rt.TagErr)
	}
	f := g.preludeInstance(g.preludeByName["Fragment"], []kind{kindInt}).def
	if got := f.variant("Static").tag; got != int(rt.TagStatic) {
		t.Fatalf("the builder assigns Static tag %d, rt says %d", got, rt.TagStatic)
	}
	if got := f.variant("Dynamic").tag; got != int(rt.TagDynamic) {
		t.Fatalf("the builder assigns Dynamic tag %d, rt says %d", got, rt.TagDynamic)
	}
	// The payload KIND the builder gave each slot, at the instantiation where
	// the two differ. A swapped spec puts `int64` in Static and `string` in
	// Dynamic, and the corpus cannot see it.
	if got := f.variant("Static").payloads[0].k; got != kindString {
		t.Fatalf("Fragment<Int>.Static carries %s, want String", got.nomi())
	}
	if got := f.variant("Dynamic").payloads[0].k; got != kindInt {
		t.Fatalf("Fragment<Int>.Dynamic carries %s, want Int", got.nomi())
	}
	if int(rt.TagInvalid) != 0 {
		t.Fatal("tag 0 is not the reserved-invalid value")
	}
	for _, d := range []*typeDef{m, r, f} {
		seen := map[int]string{}
		for _, v := range d.variants {
			if v.tag == int(rt.TagInvalid) {
				t.Fatalf("%s.%s was assigned the reserved-invalid tag 0; a Go zero value would masquerade as it", d.nomi, v.nomi)
			}
			if prev, dup := seen[v.tag]; dup {
				t.Fatalf("%s.%s and %s.%s share tag %d", d.nomi, prev, d.nomi, v.nomi, v.tag)
			}
			seen[v.tag] = v.nomi
		}
	}
}

// TestPreludeInstancesAreDistinctTypes is the identity property, asserted on
// the builder's own table rather than on any rendered text.
//
// `Maybe<Int>` and `Maybe<String>` must be two *typeDef pointers, because
// kind equality is pointer equality and one pointer would make them one kind —
// which is sound for nothing. Two mentions of `Maybe<Int>` must be ONE pointer,
// or a value built at one mention would not be assignable at the other.
func TestPreludeInstancesAreDistinctTypes(t *testing.T) {
	g := preludeGenForTest(t)
	a := g.preludeByName["Maybe"]
	mi := g.preludeInstance(a, []kind{kindInt})
	mi2 := g.preludeInstance(a, []kind{kindInt})
	ms := g.preludeInstance(a, []kind{kindString})
	if mi != mi2 {
		t.Fatal("two mentions of Maybe<Int> are two kinds")
	}
	if mi == ms {
		t.Fatal("Maybe<Int> and Maybe<String> are one kind")
	}
	if got := mi.nomi(); got != "Maybe<Int>" {
		t.Fatalf("Maybe<Int> renders as %q", got)
	}
	if got := ms.nomi(); got != "Maybe<String>" {
		t.Fatalf("Maybe<String> reports itself as %q", got)
	}
	r := g.preludeByName["Result"]
	ris := g.preludeInstance(r, []kind{kindInt, kindString})
	rsi := g.preludeInstance(r, []kind{kindString, kindInt})
	if ris == rsi {
		t.Fatal("Result<Int, String> and Result<String, Int> are one kind")
	}
	// Both type arguments the same Go type: rt cannot dedup the slots, so the
	// two payloads must land in two DIFFERENT fields or Err would overwrite Ok.
	rii := g.preludeInstance(r, []kind{kindInt, kindInt})
	slots := rii.def.slots
	if len(slots) != 2 {
		t.Fatalf("Result<Int, Int> has slots %+v; the two payloads must not share a field", slots)
	}
}

// preludeGenForTest builds a gen over a trivial program that anchors every
// spec.
//
// `Maybe` and `Result` are in every file's scope whether it imports them or
// not; `Fragment` and `Outcome` are ordinary `pub enum`s in std/literals and
// std/tasks, so the program has to import them and MENTION them — an unused
// import is a front-end error. That asymmetry is not incidental: it is the one
// observable difference between the prelude proper and the other specs, and
// TestPreludeShapeMatchesStdSource asserts all FOUR anchor here so a spec that
// silently stops anchoring cannot pass as "nothing to check".
func preludeGenForTest(t *testing.T) *gen {
	t.Helper()
	const src = "import {\n  std/literals.Fragment\n  std/tasks.Outcome\n}\n\n" +
		"fn tag(_f: Fragment<String>): Int {\n  0\n}\n\n" +
		"fn ended(_o: Outcome<Int>): Int {\n  0\n}\n\n" +
		"fn main(): Unit {\n  if True { } \n}\n"
	p, err := AnalyzeSource("prelude_probe", src)
	if err != nil {
		t.Fatalf("analyzing the prelude program: %v", err)
	}
	// nomiPath, because a gen that lowers anything reaches `irPos` and
	// `ir.At` rejects a position with no file.
	g := &gen{nomiPath: "prelude_probe.nomi", fa: p.Modules[0].FA, types: map[string]*typeDef{}}
	g.loadPreludes()
	return g
}

// TestPreludeIdentityIsNotAName holds that a prelude type's identity is its
// declaration, not its name.
//
// A file that declares its OWN `Maybe` must not have it lowered to rt's. The
// declaration is refused for being generic, and the point of this test is that
// the refusal is about the user's type rather than a silent redirection to the
// prelude one — so the anchor must not resolve at all.
func TestPreludeIdentityIsNotAName(t *testing.T) {
	src := `enum Maybe<T> {
  Some T
  None
}

fn main(): Unit {
  if True { }
}
`
	p, err := AnalyzeSource("shadow_probe", src)
	if err != nil {
		// A redeclaration may be a front-end error, which is a stronger
		// version of the same guarantee.
		if strings.Contains(err.Error(), "Maybe") {
			return
		}
		t.Fatalf("analyzing: %v", err)
	}
	g := &gen{fa: p.Modules[0].FA, types: map[string]*typeDef{}}
	g.loadPreludes()
	a, found := g.preludeByName["Maybe"]
	if !found {
		return
	}
	// If an anchor exists it must be the STD declaration, never the local one.
	for _, n := range p.Modules[0].Nodes {
		if ed, isEnum := n.(*ast.EnumDef); isEnum && ed.Name == "Maybe" && ed == a.decl {
			t.Fatal("a locally declared Maybe was anchored as the prelude one")
		}
	}
}
