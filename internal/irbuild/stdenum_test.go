package irbuild

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/std"
)

// TestStdEnumTagsMatchRT holds the builder's tag assignment against the
// constants rt declares.
//
// Both are derived from std/comparable.nomi's declaration order and neither
// reads the other, so they can drift — and a permuted tag is a WRONG ANSWER
// rather than a compile error. Asserted absolutely, by name and by number,
// because the numbers are the thing.
// rtNamed reports whether t is a named top-level type declared in rt.
func rtNamed(t reflect.Type) bool {
	return t != nil && t.Name() != "" && t.PkgPath() == rtModulePath
}

func TestStdEnumTagsMatchRT(t *testing.T) {
	d := stdEnumDefs()[stdEnumOrdering]
	want := map[string]int{"Less": int(rt.TagLess), "Equal": int(rt.TagEqual), "Greater": int(rt.TagGreater)}
	if len(want) != len(d.variants) {
		t.Fatalf("%d variants, want %d", len(d.variants), len(want))
	}
	for _, v := range d.variants {
		if got, known := want[v.nomi]; !known {
			t.Errorf("variant %q is not one rt declares a tag for", v.nomi)
		} else if got != v.tag {
			t.Errorf("%s has tag %d, rt says %d", v.nomi, v.tag, got)
		}
	}
	// Tag 0 stays reserved invalid, which is what makes a Go zero value
	// detectably never-constructed. Asserted rather than inferred from the
	// numbering above.
	if rt.TagInvalid != 0 {
		t.Errorf("TagInvalid is %d, want 0", rt.TagInvalid)
	}
	for _, v := range d.variants {
		if v.tag == int(rt.TagInvalid) {
			t.Errorf("%s was assigned the reserved invalid tag", v.nomi)
		}
	}
}

// TestStdEnumShapeMatchesStdSource asserts the anchor IS built against the real
// std tree.
//
// stdEnumSpec.matches is a belief about what std/comparable.nomi declares, and
// a belief that stops holding produces NO anchor — every Ordering mention
// refuses again, loudly. That failure mode is correct but it must not pass
// unnoticed, which is what this test is for: it is prelude.go's
// TestPreludeShapeMatchesStdSource applied to this file's specs.
func TestStdEnumShapeMatchesStdSource(t *testing.T) {
	lib := std.Load()
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		// std.Load keys a module by its BARE name; the spec carries the
		// analyzer's Origin, which is the `std/`-prefixed build key.
		module := strings.TrimPrefix(s.origin, "std/")
		fa := lib.Files[module]
		if fa == nil {
			t.Fatalf("%s: std declares no module %q", s.nomi, module)
		}
		_, byName := stdEnumAnchors(fa)
		if _, anchored := byName[s.nomi]; !anchored {
			t.Errorf("%s.%s: no anchor built, so every mention refuses. "+
				"Either the declaration moved or its shape changed; compare stdEnumSpec.matches "+
				"against %s.nomi", s.origin, s.nomi, s.origin)
		}
	}
}

// TestStdEnumDefsArePackageNeutral is the guard on the precondition that lets
// these *typeDefs be process-wide at all.
//
// foreign.go's rule is that a shared def is wrong because its component kinds
// are interned in the OWNER's g.comps, so a component renders correctly in one
// gen and nowhere else. The exemption is therefore about
// RENDERING, and the test states it that way: every slot's kind must be
// package-neutral, and this family admits only SCALARS, which are interned
// nowhere and spelled the same everywhere.
//
// A slot count would be the wrong check: `calendar.Error` carries a String in
// every variant, and a String renders `string` in every gen. Package-neutrality
// is the property the argument needs, and it is stricter on the part that
// matters: a spec that acquired a `List<Point>` payload fails here.
func TestStdEnumDefsArePackageNeutral(t *testing.T) {
	for i, d := range stdEnumDefs() {
		s := &stdEnumSpecs[i]
		if len(d.fields) != 0 || len(d.components) != 0 {
			t.Errorf("%s: %d field(s), %d component(s) — an enum def may have neither",
				s.nomi, len(d.fields), len(d.components))
		}
		for _, slot := range d.slots {
			if !slot.k.packageNeutral() {
				t.Errorf("%s: slot %s holds %s, which does not render the same in every package",
					s.nomi, slot.k.nomi(), slot.k.nomi())
			}
			if slot.boxed {
				t.Errorf("%s: slot %s is boxed; a scalar payload can never reach its container",
					s.nomi, slot.k.nomi())
			}
		}
		for _, v := range d.variants {
			for _, p := range v.payloads {
				if p.slot < 0 || p.slot >= len(d.slots) {
					t.Errorf("%s.%s: payload slot %d is out of range", s.nomi, v.nomi, p.slot)
					continue
				}
				if d.slots[p.slot].k != p.k {
					t.Errorf("%s.%s: payload is %s but its slot holds %s",
						s.nomi, v.nomi, p.k.nomi(), d.slots[p.slot].k.nomi())
				}
			}
		}
		if !d.rtDeclared {
			t.Errorf("%s: not marked rtDeclared, so typeDecl would emit a second declaration", s.nomi)
		}
		if !d.isEnum || d.isDistinct {
			t.Errorf("%s: not an enum def", s.nomi)
		}
	}
}

// TestStdEnumSpecReachesTheRealRtType pins the pairing between the spec and rt
// through reflection rather than through a string, which is what makes a rename
// in rt a compile error here and a deletion a test failure.
func TestStdEnumSpecReachesTheRealRtType(t *testing.T) {
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		if !rtNamed(s.goType) {
			t.Fatalf("%s: goType %v is not a named top-level type in rt", s.nomi, s.goType)
		}
		f, found := s.goType.FieldByName(s.tagField)
		if !found {
			t.Fatalf("%s: rt type has no %s field", s.nomi, s.tagField)
		}
		if f.Type != reflect.TypeFor[uint8]() {
			t.Errorf("%s: %s is %v, want uint8", s.nomi, s.tagField, f.Type)
		}
		// Nothing beyond the tag and the slots the SPEC declares. An
		// unaccounted rt field is storage the builder never writes, which is a
		// silent representation split — preludeGoArgs' check, and the same
		// reason. TestStdEnumSlotsMatchRT names WHICH field; this one is the
		// arithmetic, kept because it fails even when a spec and a stray field
		// happen to share a name.
		slots := map[string]bool{}
		for _, v := range s.variants {
			if v.field != "" {
				slots[v.field] = true
			}
			// A payloadStructFields variant states its slots per FIELD rather
			// than in the singular `field`, so a guard that read only the
			// singular would count `Backoff`'s two as zero and demand a
			// one-field rt type.
			for _, f := range v.fields {
				slots[f.field] = true
			}
		}
		if n, want := s.goType.NumField(), 1+len(slots); n != want {
			t.Errorf("%s: rt type has %d fields, want %d (the tag plus %d payload slot(s))",
				s.nomi, n, want, len(slots))
		}
	}
}

// TestOrderingRankIsRtsOnlyTable pins the -1/0/1 mapping absolutely.
//
// These two functions are the one place the pairing is written, so there is
// nothing for it to agree with. Spelled out by name and number for the reason
// rt/prelude_test.go spells the tags out: a comparison between two readers
// cannot see a bug in code both of them share.
func TestOrderingRankIsRtsOnlyTable(t *testing.T) {
	cases := []struct {
		variant string
		tag     uint8
		rank    int64
	}{
		{"Less", rt.TagLess, -1},
		{"Equal", rt.TagEqual, 0},
		{"Greater", rt.TagGreater, 1},
	}
	for _, c := range cases {
		if got := rt.OrderingTag(c.variant); got != c.tag {
			t.Errorf("OrderingTag(%q) = %d, want %d", c.variant, got, c.tag)
		}
		if got := rt.OrderingRank(rt.Ordering{Tag: c.tag}); got != c.rank {
			t.Errorf("OrderingRank(tag %d) = %d, want %d", c.tag, got, c.rank)
		}
	}
	// An unknown variant and the zero value both rank 0. Pinned so a future
	// "be strict" change has to be a deliberate change rather than an
	// accident.
	if got := rt.OrderingTag("Sideways"); got != rt.TagInvalid {
		t.Errorf("OrderingTag of an unknown variant = %d, want TagInvalid", got)
	}
	if got := rt.OrderingRank(rt.Ordering{}); got != 0 {
		t.Errorf("OrderingRank of the zero value = %d, want 0", got)
	}
}

// TestStdEnumSpecsAreReachable holds the bar for the table: a row must be
// REACHED by something, not merely be representable.
//
// A registry row for a type nothing can name is scaffolding, and opaque.go
// states the same bar in prose while listing the three std opaque types it
// deliberately omits. This asserts it.
//
// An anchored type does not refuse, so asking "which corpus refusal names it"
// would answer none and the guard would be self-defeating. So the question
// asked instead is the one that survives
// lowering: does any corpus file MENTION the type at all, as a name the
// analyzer resolves to the spec's declaration?
//
// THREE ARMS, and the second and third exist because a type can be load-bearing
// in a program that never spells its name. See stdEnumGatesABoundSignature (a
// bound host signature's parameter or result) and
// stdEnumHeldByAReachedStructField (a field of a stdStructSpecs row the corpus
// does name). Both are seeded by arm 1's question asked of something else, so
// neither can certify a row nothing reaches.
func TestStdEnumSpecsAreReachable(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	_, programs := corpusAnalysis(t)
	found := map[string]string{}
	for _, prog := range programs {
		p := prog.Prog
		if p == nil {
			continue
		}
		for i := range p.Modules {
			m := &p.Modules[i]
			_, byName := stdEnumAnchors(m.FA)
			for name := range byName {
				if found[name] != "" {
					continue
				}
				// An anchor exists in every module that has the name in
				// SCOPE, which is nearly all of them; a mention is what makes
				// the row load-bearing. Read off the source text, because the
				// alternative — a refusal naming it — stops existing the
				// moment the row works.
				src, readErr := os.ReadFile(m.Path)
				if readErr != nil || !mentionsWord(string(src), name) {
					continue
				}
				found[name] = prog.Rel
			}
		}
	}
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		if found[s.nomi] != "" {
			t.Logf("%s.%s reached by %s", s.origin, s.nomi, found[s.nomi])
			continue
		}
		if via, gated := stdEnumGatesABoundSignature(s, programs); gated {
			t.Logf("%s.%s reached through %s's signature", s.origin, s.nomi, via)
			continue
		}
		if via, held := stdEnumHeldByAReachedStructField(i, programs); held {
			t.Logf("%s.%s reached as %s", s.origin, s.nomi, via)
			continue
		}
		t.Errorf("%s.%s: no corpus program mentions it, no bound std signature it gates is "+
			"reachable, and no reached stdlib struct holds it in a field — so the row is "+
			"scaffolding; delete it or name what reaches it", s.origin, s.nomi)
	}
}

// stdEnumGatesABoundSignature is the reachability guard's SECOND arm: a row that
// no corpus file mentions is still load-bearing when it appears in the projected
// signature of a registry-bound std host function whose RECEIVER the corpus
// does name.
//
// WHY THE ARM EXISTS, and why it is not a loophole. `Supervisor.new`'s
// declaration is
//
//	new(max_running: Int, shutdown_timeout: Duration, restart: Restart,
//	    backoff: Backoff, on_give_up: GiveUp): Supervisor
//
// and a signature is refused by its WIDEST parameter, not by the arguments a
// caller passes. So `GiveUp` and `Wait` and `FlushOutcome` gate all three
// supervisor corpus files while not one of them spells any of the three names.
//
// The arm keeps its teeth two ways. It requires a BOUND function, so a row for
// a type in some unbound declaration's signature does not qualify; and it
// requires the corpus to name that function's RECEIVER, which is what ties the
// row back to a program rather than to std's own source. A row satisfying
// neither is still an error.
func stdEnumGatesABoundSignature(s *stdEnumSpec, programs []corpusFile) (string, bool) {
	want := s.nomi
	for key, h := range stdlibHostFuncs {
		named := h.result.nomi() == want
		for _, p := range h.params {
			named = named || p.nomi() == want
		}
		if !named {
			continue
		}
		// `supervisors.Supervisor.new_exact` -> `Supervisor`. The receiver is
		// the middle segment of a std key; a free function has two segments and
		// yields "", which no corpus mention can match.
		parts := strings.Split(key, ".")
		if len(parts) != 3 {
			continue
		}
		if corpusMentions(programs, parts[1]) {
			return key, true
		}
	}
	return "", false
}

// stdEnumHeldByAReachedStructField is the reachability guard's THIRD arm: a row
// that no corpus file mentions is still load-bearing when a stdStructSpecs row
// the corpus DOES name holds it in a field.
//
// # Not a loophole, and the precedent is written down verbatim one file over
//
// stdstruct_test.go's own arm 4 states the standard for exactly this shape:
// std/assertions' `AssertionValue` and its two siblings "are reached only this
// way ... so the type is not scaffolding — it is storage the reached row CANNOT
// BE REPRESENTED WITHOUT, and deleting its spec would un-anchor
// `AssertionFailure` rather than save anything". That is true here in the
// strongest form: `stdStructSpec.matches` compares each field's kind against the
// spec, so a struct whose field names an enum with no row produces NO ANCHOR at
// all and every call site constructing it refuses.
//
// # What keeps it narrow
//
// The STRUCT row has to be named by a corpus program's own source, which is arm
// 1's question asked of the holder. So a chain seeded by nothing certifies
// nothing, and two rows that hold each other and nothing else cannot certify one
// another — the same property arm 4 is careful about. Deliberately NOT transitive
// beyond one hop: no enum row is held by a field of another enum row's
// payload, and an arm for a case that does not exist is a path with no member.
//
// Matching is by def POINTER through `kindMentionsDef`, so `Maybe<E>` and a
// bare `E` field both count and two same-shaped enums never can be
// confused for one.
func stdEnumHeldByAReachedStructField(i int, programs []corpusFile) (string, bool) {
	d := stdEnumDefs()[i]
	defs := stdStructDefs()
	for j := range stdStructSpecs {
		holder := &stdStructSpecs[j]
		if !corpusMentions(programs, holder.nomi) {
			continue
		}
		for _, f := range holder.fields {
			if kindMentionsDef(f.kindOf(defs), d, 8) {
				return holder.origin + "." + holder.nomi + "." + f.nomi + ": " +
					f.kindOf(defs).nomi(), true
			}
		}
	}
	return "", false
}

// corpusMentions reports whether any corpus module's source names word as a
// whole identifier.
func corpusMentions(programs []corpusFile, word string) bool {
	for _, prog := range programs {
		if prog.Prog == nil {
			continue
		}
		for i := range prog.Prog.Modules {
			src, err := os.ReadFile(prog.Prog.Modules[i].Path)
			if err == nil && mentionsWord(string(src), word) {
				return true
			}
		}
	}
	return false
}

// mentionsWord reports whether src names word as a whole identifier, so
// `Ordering` is not matched inside `Orderings`.
//
// The pattern is compiled once per word rather than once per call. The corpus
// reachability sweep asks this of every spec name for every module of every
// corpus program, and compiling the same handful of patterns tens of thousands
// of times would be most of that test's cost.
var mentionsWordRE sync.Map // word -> *regexp.Regexp

func mentionsWord(src, word string) bool {
	re, ok := mentionsWordRE.Load(word)
	if !ok {
		re, _ = mentionsWordRE.LoadOrStore(word, regexp.MustCompile(`\b`+regexp.QuoteMeta(word)+`\b`))
	}
	return re.(*regexp.Regexp).MatchString(src)
}

// TestOrderingIsNotDeclaredInAStdlibPackage is the buildTypes half of the
// single-declaration guarantee, checked where the risk actually lives.
//
// std/comparable.nomi DECLARES Ordering, so its own gen is the one place a
// shell would be minted. Without the adoption in buildTypes, typeOf inside that
// module answers `NomiT_Ordering`, a kind that is not the shared Ordering def.
// Asserted on the def a gen over that module actually holds.
func TestOrderingIsNotDeclaredInAStdlibPackage(t *testing.T) {
	lib := std.Load()
	const module = "comparable"
	fa := lib.Files[module]
	if fa == nil {
		t.Fatalf("std declares no %s", module)
	}
	g := newStdGen(module, module+".nomi", "nomistdX", lib.Nodes[module], fa, nil, nil, nil, nil)
	d := g.types["Ordering"]
	if d == nil {
		t.Fatal("the declaring module has no Ordering in its type table")
	}
	if d != stdEnumDefs()[stdEnumOrdering] {
		t.Fatalf("the declaring module minted its OWN def (%q); it must adopt the shared one", d.nomi)
	}
	// And the sibling module that only MENTIONS it must reach the same pointer,
	// since that identity is what a stdFunc's result kind is compared by.
	const user = "int"
	ufa := lib.Files[user]
	if ufa == nil {
		t.Fatalf("std declares no %s", user)
	}
	ug := newStdGen(user, user+".nomi", "nomistdY", lib.Nodes[user], ufa, nil, nil, nil, nil)
	got, found := ug.stdEnumNamed("Ordering")
	if !found || got != d {
		t.Fatalf("%s does not reach the same def for Ordering", user)
	}
}

// TestIntCompareIsLowered checks that the shared Ordering def admits the
// `compare` bodies. `Int.compare` is Nomi-bodied in std/int.nomi, and it lowers
// only because stdTypeKind admits `Ordering` as a return type; without the
// enum row it would refuse as `stdlib function outside the scalar subset`.
func TestIntCompareIsLowered(t *testing.T) {
	idx := stdlibLowering()
	for _, key := range []string{"int.Int.compare", "strings.String.compare", "float.Float.compare"} {
		f := idx.byKey[key]
		if f == nil {
			t.Errorf("%s: not in the stdlib index at all", key)
			continue
		}
		if !f.lowerable() {
			t.Errorf("%s: refused as %q", key, f.why)
			continue
		}
		if f.result != stdEnumKind(stdEnumOrdering) {
			t.Errorf("%s: result kind is %s, want the shared Ordering def", key, f.result.nomi())
		}
	}
}
