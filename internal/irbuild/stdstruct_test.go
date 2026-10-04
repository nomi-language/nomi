package irbuild

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestStdStructSpecsMatchStdSource asserts the anchors are really built from the
// std source in this build.
//
// Without it every guard in this file is vacuous in the failing direction: a
// spec that drifted from std produces NO anchor, so every mention refuses and
// the builder is simply less capable — which no other test here would notice.
// TestOpaqueSpecsMatchStdSource and TestPreludeShapeMatchesStdSource exist for
// the same reason.
func TestStdStructSpecsMatchStdSource(t *testing.T) {
	// FIRST, so a shifted index does not surface here as a complaint about a std
	// file the change never touched. See requireStdSpecIndices.
	requireStdSpecIndices(t)
	lib := std.Load()
	for i := range stdStructSpecs {
		s := &stdStructSpecs[i]
		module := strings.TrimPrefix(s.origin, "std/")
		fa := lib.Files[module]
		if fa == nil {
			t.Fatalf("%s: std declares no module %q", s.nomi, module)
		}
		_, byName := stdStructAnchors(fa)
		if _, anchored := byName[s.nomi]; !anchored {
			t.Errorf("%s.%s: no anchor built, so every mention refuses. "+
				"Either the declaration moved or its shape changed; compare stdStructSpec.matches "+
				"against %s.nomi", s.origin, s.nomi, s.origin)
		}
	}
}

// TestStdStructDefsArePackageNeutral guards the precondition that makes ONE
// process-wide *typeDef per spec sound, where foreign.go rejects shared defs in
// general.
//
// foreign.go's objection is about RENDERING — "a shared def's field kinds are
// interned in the OWNER's g.comps" — so the exemption is too: every field kind
// must render to the same Go-spelled `kind` text in every gen. This family
// admits only SCALARS, which is the strongest available form of that, and the
// check is the property rather than the restriction, so a future non-scalar
// field that happened to be neutral would not fail spuriously.
func TestStdStructDefsArePackageNeutral(t *testing.T) {
	for i, d := range stdStructDefs() {
		s := &stdStructSpecs[i]
		if !d.rtDeclared {
			t.Errorf("%s: not marked rtDeclared, so typeDecl would emit a second declaration", s.nomi)
		}
		if d.isEnum || d.isDistinct || len(d.variants) != 0 || len(d.slots) != 0 {
			t.Errorf("%s: not a plain struct def (enum=%v distinct=%v variants=%d slots=%d)",
				s.nomi, d.isEnum, d.isDistinct, len(d.variants), len(d.slots))
		}
		if !named(d).packageNeutral() {
			t.Errorf("%s: the def is not package-neutral, so sharing it across gens is unsound", s.nomi)
		}
		if len(d.fields) != len(s.fields) {
			t.Errorf("%s: def has %d fields, spec has %d", s.nomi, len(d.fields), len(s.fields))
			continue
		}
		for j, f := range d.fields {
			if !f.k.packageNeutral() {
				t.Errorf("%s.%s holds %s, which does not render the same in every package",
					s.nomi, f.nomi, f.k.nomi())
			}
			if f.boxed {
				t.Errorf("%s.%s is boxed; no field of this family can reach its own container at a fixed offset",
					s.nomi, f.nomi)
			}
			// The two default CHANNELS are mutually exclusive, and that is the
			// invariant litFieldValues rests on.
			//
			// `deflt` is std's AST, LOWERED at the construction site under
			// inDeclScope -- sound only while the declaring and constructing
			// scopes are the same one, which for a stdlib type they never are.
			// `stdDeflt` is a value stated in the builder's own vocabulary and
			// resolves no names. A field carrying BOTH would take the AST path
			// first, silently reintroducing the wrong-scope resolution the std
			// channel exists to avoid, and nothing else here would notice.
			if f.deflt != nil {
				t.Errorf("%s.%s carries an AST default; a stdlib field's default must go through stdDeflt",
					s.nomi, f.nomi)
			}
			// The def must agree with the SPEC about which fields default. A
			// row that grew a default the def never received would leave
			// `struct literal missing field` at a site std lets you omit; the
			// reverse would invent a value std never wrote.
			if (f.stdDeflt != nil) != (s.fields[j].deflt != nil) {
				t.Errorf("%s.%s: def default=%v but spec default=%v",
					s.nomi, f.nomi, f.stdDeflt != nil, s.fields[j].deflt != nil)
			}
		}
	}
}

// TestStdStructGoWidthMatchesTheDeclaredField checks each spec field's kind
// against the GO type. `matches` checks it against the NOMI declaration only.
//
// So `rt.DateTime{ InstantNanos int32 }` would pass every other guard in
// this package while silently truncating any instant past 2038 — the same shape
// as `type Duration int32` truncating past ~2.1 seconds. Read by reflection off
// rt, per field, in declaration order, so a reorder in rt fails too.
func TestStdStructGoWidthMatchesTheDeclaredField(t *testing.T) {
	for i := range stdStructSpecs {
		s := &stdStructSpecs[i]
		if !rtNamed(s.goType) {
			t.Fatalf("%s: goType %v is not a named top-level type in rt", s.nomi, s.goType)
		}
		if s.goType.Kind() != reflect.Struct {
			t.Fatalf("%s: rt type is %v, not a struct", s.nomi, s.goType.Kind())
		}
		if n := s.goType.NumField(); n != len(s.fields) {
			t.Errorf("%s: rt declares %d field(s), the spec %d; an unaccounted field is storage "+
				"the builder never fills", s.nomi, n, len(s.fields))
			continue
		}
		defs := stdStructDefs()
		for j, want := range s.fields {
			field := s.goType.Field(j)
			if got := kindOfGoType(field.Type); got != want.kindOf(defs) {
				t.Errorf("%s.%s: rt's Go type %v projects as %s, the spec declares %s — "+
					"a narrower Go type here is a silent truncation, not a compile error",
					s.nomi, want.nomi, field.Type, got.nomi(), want.kindOf(defs).nomi())
			}
		}
	}
}

// TestStdStructShapeCheckRejectsADriftedDeclaration is the negative half, and it
// is the half a name-keyed table would get wrong.
//
// `matches` exists to make a std edit produce NO anchor rather than lower
// against a layout nobody wrote, and a permuted or widened field is a wrong
// ANSWER rather than a Go compile error. Each case below is a real edit somebody
// could make to std/calendar.nomi.
func TestStdStructShapeCheckRejectsADriftedDeclaration(t *testing.T) {
	// The DateTime spec, and the declaration it describes.
	var spec *stdStructSpec
	for i := range stdStructSpecs {
		if stdStructSpecs[i].nomi == "DateTime" {
			spec = &stdStructSpecs[i]
		}
	}
	if spec == nil {
		t.Fatal("no DateTime spec; this test is about that row")
	}
	cases := []struct {
		name string
		src  string
	}{
		{"renamed field", "pub opaque struct DateTime {\n  instant_nanos: Int\n  tz: String\n}\n"},
		{"reordered fields", "pub opaque struct DateTime {\n  zone: String\n  instant_nanos: Int\n}\n"},
		{"widened field", "pub opaque struct DateTime {\n  instant_nanos: Float\n  zone: String\n}\n"},
		{"extra field", "pub opaque struct DateTime {\n  instant_nanos: Int\n  zone: String\n  cached: String\n}\n"},
		{"missing field", "pub opaque struct DateTime {\n  instant_nanos: Int\n}\n"},
		{"not opaque", "pub struct DateTime {\n  instant_nanos: Int\n  zone: String\n}\n"},
		// `not public` is NOT a drift row. Publicity is a VISIBILITY fact, not
		// a LAYOUT fact: a private declaration cannot be named by another
		// module, which makes its anchor unreachable from elsewhere rather than
		// wrong, and std/calendar's boundary carriers are private (see
		// stdStructSpec.matches). Every row here is a change that would make
		// the builder lower against the WRONG LAYOUT.
		{"generic", "pub opaque struct DateTime<T> {\n  instant_nanos: Int\n  zone: String\n}\n"},
		{"field default", "pub opaque struct DateTime {\n  instant_nanos: Int\n  zone: String = \"UTC\"\n}\n"},
	}
	// The control first, so a `matches` that rejected everything would fail here
	// rather than making every drift case pass vacuously.
	if !anchorsDateTime(t, spec, "pub opaque struct DateTime {\n  instant_nanos: Int\n  zone: String\n}\n") {
		t.Fatal("the spec's own shape does not anchor, so every case below would pass vacuously")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if anchorsDateTime(t, spec, c.src) {
				t.Fatalf("the drifted declaration still anchors; the builder would lower "+
					"against rt.DateTime's layout instead of the one std declares:\n%s", c.src)
			}
		})
	}
}

// anchorsDateTime reports whether `matches` accepts the `DateTime` declaration
// in src. Analysed rather than hand-built, so the AST is the parser's own —
// hand-building a StructDef would let a case pass by mis-modelling the node.
func anchorsDateTime(t *testing.T, spec *stdStructSpec, src string) bool {
	t.Helper()
	p, err := AnalyzeSource("main", src+"\nfn main() {\n  _ = 1\n}\n")
	if err != nil {
		// A shape the front end rejects outright cannot reach the builder, so it
		// is not anchored either. Reported rather than failed: `pub opaque struct
		// DateTime<T>` may or may not be a front-end error and this test is
		// about the builder's check.
		t.Logf("front end rejected the case (still not anchored): %v", err)
		return false
	}
	for _, m := range p.Modules {
		for _, n := range m.Nodes {
			sd, isStruct := n.(*ast.StructDef)
			if isStruct && sd.Name == "DateTime" && spec.matches(sd, stdAnchorsOf(m.FA)) {
				return true
			}
		}
	}
	return false
}

// goSignatureMentions reports whether any parameter or result of ft produces or
// consumes want, at any depth reachable through pointers and struct fields.
//
// By reflect.Type IDENTITY, so `rt.Result[rt.Hover, string]` mentions `rt.Hover`
// (its `Ok` field is that type) while nothing merely SPELLED like a prefix of it
// can match. The depth bound is what makes it total over rt's self-referential
// shapes: `*rt.List[T]`'s `Tail` is the same type as the cell it is in, so an
// unbounded walk would not terminate. Eight is well past the deepest instance rt
// declares and the bound is a termination guarantee, not a policy.
func goSignatureMentions(ft, want reflect.Type) bool {
	for i := range ft.NumIn() {
		if goTypeMentions(ft.In(i), want, 8) {
			return true
		}
	}
	for i := range ft.NumOut() {
		if goTypeMentions(ft.Out(i), want, 8) {
			return true
		}
	}
	return false
}

func goTypeMentions(t, want reflect.Type, depth int) bool {
	if t == want {
		return true
	}
	if t == nil || depth == 0 {
		return false
	}
	switch t.Kind() {
	case reflect.Pointer:
		return goTypeMentions(t.Elem(), want, depth-1)
	case reflect.Struct:
		for i := range t.NumField() {
			if goTypeMentions(t.Field(i).Type, want, depth-1) {
				return true
			}
		}
	}
	return false
}

// kindMentionsDef reports whether k is, or is built over, the def d.
//
// Arm 4's matcher, and it is over KINDS rather than Go types because that is
// what a spec's field carries: `List<AssertionValue>` is a *compKind whose one
// part is `named(AssertionValue's def)`, and identity is the def POINTER — the
// same rule everything else in this package compares nominal types by, so two
// same-shaped rows can never be confused for one.
//
// Depth-bounded for goTypeMentions' reason: a prelude instance's payload graph
// and a List's element are both walkable, and a self-referential shape would
// otherwise not terminate.
func kindMentionsDef(k kind, d *typeDef, depth int) bool {
	if depth == 0 {
		return false
	}
	if k.def == d {
		return true
	}
	if k.def != nil {
		for _, arg := range k.def.preludeArgs {
			if kindMentionsDef(arg, d, depth-1) {
				return true
			}
		}
	}
	if k.comp != nil {
		for _, p := range k.comp.parts {
			if kindMentionsDef(p, d, depth-1) {
				return true
			}
		}
	}
	return false
}

// TestStdStructSpecIndicesNameTheirRow pins the constants a row uses to reach
// another row.
//
// An index is a literal position in a slice, so inserting a row above one
// silently repoints every dependency — `AssertionFailure.values` would become a
// `List<AssertionBinding>` and still compile, still anchor nothing, and report
// only that a corpus file refuses over a type. Named here against each row's own
// Nomi name, so a reorder fails by name at the row that moved.
func TestStdStructSpecIndicesNameTheirRow(t *testing.T) {
	for _, off := range stdSpecIndexOffences() {
		t.Error(off)
	}
}

// stdSpecIndexOffences is every `stdSpec*` constant that does not name its
// row, as a message each.
//
// Extracted so the two guards that DEPEND on these indices can refuse first and
// name the real cause. Inserting a row mid-table shifts the constants, and
// without this check `TestStdStructSpecsMatchStdSource` and
// `TestStdStructSpecsAreReachable` fail first and blame `std/assertions.nomi`,
// a file the change never touched: a shifted index breaks the DEPENDENT rows
// before the constant that describes them.
func stdSpecIndexOffences() []string {
	var out []string
	for _, c := range []struct {
		i    int
		nomi string
	}{
		{stdSpecAssertionPipelineStage, "AssertionPipelineStage"},
		{stdSpecAssertionValue, "AssertionValue"},
		{stdSpecAssertionBinding, "AssertionBinding"},
		{stdSpecAssertionDetail, "AssertionDetail"},
		{stdSpecAssertionFailure, "AssertionFailure"},
	} {
		if c.i >= len(stdStructSpecs) {
			out = append(out, fmt.Sprintf("index %d is past the end of the table (%d rows)",
				c.i, len(stdStructSpecs)))
			continue
		}
		if got := stdStructSpecs[c.i].nomi; got != c.nomi {
			out = append(out, fmt.Sprintf("index %d is %s, the constant says %s; every row "+
				"naming it now points at the wrong layout", c.i, got, c.nomi))
		}
	}
	return out
}

// requireStdSpecIndices stops a dependent guard before it blames the wrong file.
//
// A row APPENDED at the end of stdStructSpecs never trips this; a row INSERTED
// does, and then every downstream failure is a symptom.
func requireStdSpecIndices(t *testing.T) {
	t.Helper()
	offences := stdSpecIndexOffences()
	if len(offences) == 0 {
		return
	}
	t.Fatalf("a stdSpec* constant does not name its row, so every guard below this one "+
		"reports a SYMPTOM — fix the indices first, or APPEND the new row instead of "+
		"inserting it:\n  %s", strings.Join(offences, "\n  "))
}

// TestGoTypeMentionsIsExactRatherThanByName keeps arm 3 of the reachability
// guard from becoming a substring match, which is the way it would go quietly
// wrong: a matcher that answered YES for every rt type would report every spec
// reached and the guard would assert nothing.
//
// Three claims. It finds a type nested inside a generic instantiation, it
// REFUSES a sibling rt struct of the same shape, and it terminates on rt's
// self-referential cons cell.
func TestGoTypeMentionsIsExactRatherThanByName(t *testing.T) {
	hover := reflect.TypeFor[rt.Hover]()
	diag := reflect.TypeFor[rt.Diagnostic]()
	// The rt-shaped signatures of `compiler.hover` and `compiler.check`.
	hoverSig := reflect.TypeFor[func(string) rt.Result[rt.Hover, string]]()
	checkSig := reflect.TypeFor[func(string) *rt.List[rt.Diagnostic]]()
	if !goSignatureMentions(hoverSig, hover) {
		t.Error("hover's Result<Hover, String> does not mention rt.Hover")
	}
	if goSignatureMentions(hoverSig, diag) {
		t.Error("hover mentions rt.Diagnostic, so the matcher answers yes for anything")
	}
	if !goSignatureMentions(checkSig, diag) {
		t.Error("check's List<Diagnostic> does not mention rt.Diagnostic")
	}
	if goSignatureMentions(checkSig, hover) {
		t.Error("check mentions rt.Hover")
	}
	// `*rt.List[rt.Diagnostic]`'s Tail is the same pointer type as the cell it
	// sits in, so an unbounded walk for a type that is NOT there would recurse
	// forever. Reaching this line at all is the assertion.
	if goTypeMentions(reflect.TypeFor[*rt.List[rt.Diagnostic]](), hover, 8) {
		t.Error("a List<Diagnostic> mentions rt.Hover")
	}
}
