package irbuild

import (
	"reflect"
	"sort"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// TestSharedPreludeInternKeysOnTheWholeInstantiation is the anti-collapse guard.
//
// The hazard is a plain map assignment on a key that does not distinguish its
// members, the same shape TestStdlibIndexKeysCarryTheirReceiver guards in the
// stdlib index. Here the consequence would be worse than lost coverage: a
// `Maybe<String>` def answering for `Maybe<Int>` is a wrong PAYLOAD TYPE.
//
// Three instantiations, one per neutral shape, asserted DISTINCT by pointer and
// by rendered Go type.
func TestSharedPreludeInternKeysOnTheWholeInstantiation(t *testing.T) {
	// Build the whole stdlib index first, so the emptiness assertion at the end
	// covers every instantiation std's own signatures reach rather than only the
	// three this test writes.
	stdlibLowering()
	maybe := &preludeSpecs[0]
	if maybe.nomi != "Maybe" {
		t.Fatalf("preludeSpecs[0] is %q, not Maybe — this test names the spec positionally", maybe.nomi)
	}
	ints, shared := sharedPreludeInstance(maybe, []kind{kindInt})
	if !shared {
		t.Fatal("Maybe<Int> was not interned as shared; its argument is a scalar")
	}
	strs, shared := sharedPreludeInstance(maybe, []kind{kindString})
	if !shared {
		t.Fatal("Maybe<String> was not interned as shared")
	}
	nz, shared := sharedPreludeInstance(maybe, []kind{opaqueKind(opaqueSpecIndex(t, "NonZeroInt"))})
	if !shared {
		t.Fatal("Maybe<NonZeroInt> was not interned as shared; an anchored opaque newtype is neutral")
	}
	seen := map[string]kind{}
	for _, k := range []kind{ints, strs, nz} {
		if prev, dup := seen[k.def.nomi]; dup {
			t.Fatalf("two instantiations are both spelled %q — the key collapsed: %s and %s",
				k.def.nomi, prev.def.nomi, k.def.nomi)
		}
		seen[k.def.nomi] = k
	}
	for _, want := range []string{"Maybe<Int>", "Maybe<String>", "Maybe<NonZeroInt>"} {
		if _, made := seen[want]; !made {
			t.Fatalf("no instantiation is spelled %q; got %v", want, sortedKeysOf(presence(seen)))
		}
	}
	// The same call twice is the SAME pointer, which is what makes a stdFunc's
	// result kind comparable against a call site's in another gen.
	again, _ := sharedPreludeInstance(maybe, []kind{kindInt})
	if again.def != ints.def {
		t.Fatal("Maybe<Int> interned twice; a stdFunc kind would not compare equal to a call site's")
	}
}

// TestPackageNeutralIsClosed pins the predicate that decides where a def or a
// *compKind lives, in both directions.
//
// The false rows matter more than the true ones. Structural kinds over
// neutral components (`List<Int>`, `Maybe<List<Int>>`, `(Int, Int)`,
// `(Int) -> Int`) are neutral, because sharedcomp.go interns them
// process-wide.
//
// What must stay false is anything naming a GENERATED package's type. That is
// the direction nothing else checks, and it is the one where a wrong answer is
// silent: `List<Point>` renders `*rt.List[NomiT_Point]`, and two files may each
// declare a `Point`. Those rows are below, and they are the point of the table.
func TestPackageNeutralIsClosed(t *testing.T) {
	g := preludeGenForTest(t)
	maybeInt := g.preludeInstance(g.preludeByName["Maybe"], []kind{kindInt})
	duration := opaqueKind(opaqueSpecIndex(t, "Duration"))

	for _, tc := range []struct {
		name string
		k    kind
		want bool
	}{
		{"Int", kindInt, true},
		{"String", kindString, true},
		{"Bool", kindBool, true},
		{"Float", kindFloat, true},
		{"Unit", kindUnit, true},
		{"an anchored opaque newtype", duration, true},
		{"Maybe<Int>", maybeInt, true},
		{"Maybe<Duration>", g.preludeInstance(g.preludeByName["Maybe"], []kind{duration}), true},
		{"Result<Maybe<Int>, String>", g.preludeInstance(g.preludeByName["Result"], []kind{maybeInt, kindString}), true},
		{"kindInvalid", kindInvalid, false},
		{"List<Int>", g.listKind(kindInt), true},
		{"Maybe<List<Int>>", g.preludeInstance(g.preludeByName["Maybe"], []kind{g.listKind(kindInt)}), true},
		{"(Int, Int)", g.tupleKind([]kind{kindInt, kindInt}), true},
		{"(Int) -> Int", g.funcKind([]kind{kindInt}, kindInt), true},
		{"a generated package's own type", named(localDefForTest()), false},
		{"List<that type>", g.listKind(named(localDefForTest())), false},
		{"(Int, that type)", g.tupleKind([]kind{kindInt, named(localDefForTest())}), false},
		{"Maybe<List<that type>>",
			g.preludeInstance(g.preludeByName["Maybe"], []kind{g.listKind(named(localDefForTest()))}), false},
	} {
		if got := tc.k.packageNeutral(); got != tc.want {
			t.Errorf("%s: packageNeutral() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// localDefForTest is a def belonging to a GENERATED package: not rtDeclared, not
// a prelude instance, so its Go name is one only its own package declares.
//
// A fresh pointer per call, deliberately. Two calls are two DECLARATIONS, which
// is the shape the negative rows are about — a Point in one file and a Point in
// another are two types however identically they are spelled.
func localDefForTest() *typeDef {
	return &typeDef{nomi: "Point", lowerable: true}
}

// TestSharedPreludeDefsAreNeverInPreludeOrder is the immutability claim, checked
// rather than argued.
//
// settleLowerable is the only writer of a *typeDef's `lowerable` bit after
// construction, and it walks `g.typeOrder` plus `g.preludeOrder`. A def reached
// from every gen must not be in either list, or one module's refusal would
// retract a type another module is using. A NON-neutral instance must still BE
// in the list, because `Maybe<Node>` really does have to be retracted when Node
// is refused.
func TestSharedPreludeDefsAreNeverInPreludeOrder(t *testing.T) {
	const src = "struct Node {\n  next: Maybe<Node>\n}\n\n" +
		"fn head(): Maybe<Int> {\n  None\n}\n\nfn main() {\n  _ = head()\n}\n"
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	g := newGen(p.Entry(), "nomimod0", nil, nil, -1, nil)
	g.declareTypes()
	g.declareFuncs()

	neutral, nonNeutral := 0, 0
	for _, d := range g.preludeOrder {
		if named(d).packageNeutral() {
			neutral++
			t.Errorf("neutral instance %s is in preludeOrder, where settleLowerable can retract it", d.nomi)
			continue
		}
		nonNeutral++
	}
	if nonNeutral == 0 {
		t.Fatal("no non-neutral instance was created, so this asserts nothing about the split")
	}
	if neutral != 0 {
		t.Fatalf("%d neutral instance(s) in preludeOrder", neutral)
	}
	// And the neutral one really was built by this program, so its absence above
	// is the split working rather than nothing having happened.
	if k, shared := sharedPreludeInstance(&preludeSpecs[0], []kind{kindInt}); !shared || k.def.nomi != "Maybe<Int>" {
		t.Fatalf("Maybe<Int> is not in the shared table: shared=%v", shared)
	}
}

// TestPreludeKindOfGoTypeReadsTheArgumentsOffTheFields is the registry
// projection, in both directions.
//
// The registry's whole property is that a signature is DERIVED from the Go
// function value rather than declared, so a renamed or reshaped rt type must
// answer kindInvalid rather than projecting onto a plausible wrong kind. The
// false rows are the ones that hold that: `rt.List[int64]` renders neutrally and
// must still not project, and a struct outside rt must not project however it is
// shaped.
func TestPreludeKindOfGoTypeReadsTheArgumentsOffTheFields(t *testing.T) {
	nzIdx := opaqueSpecIndex(t, "NonZeroInt")
	for _, tc := range []struct {
		t    reflect.Type
		want string
	}{
		{reflect.TypeFor[rt.Maybe[int64]](), "Maybe<Int>"},
		{reflect.TypeFor[rt.Maybe[string]](), "Maybe<String>"},
		{reflect.TypeFor[rt.Maybe[rt.NonZeroInt]](), "Maybe<NonZeroInt>"},
		{reflect.TypeFor[rt.Result[int64, string]](), "Result<Int, String>"},
		{reflect.TypeFor[rt.Result[rt.Maybe[int64], string]](), "Result<Maybe<Int>, String>"},
	} {
		k := preludeKindOfGoType(tc.t)
		if k == kindInvalid {
			t.Errorf("%s did not project", tc.t)
			continue
		}
		if k.def.nomi != tc.want {
			t.Errorf("%s projected as %q, want %q", tc.t, k.def.nomi, tc.want)
		}
		// The whole point of the shared table: the projected kind IS the one a
		// call site resolves an annotation to.
		if tc.t == reflect.TypeFor[rt.Maybe[rt.NonZeroInt]]() {
			want, _ := sharedPreludeInstance(&preludeSpecs[0], []kind{opaqueKind(nzIdx)})
			if k != want {
				t.Errorf("the registry's Maybe<NonZeroInt> is not the annotation's: %v vs %v", k.def, want.def)
			}
		}
	}
	for _, bad := range []reflect.Type{
		reflect.TypeFor[int64](),
		reflect.TypeFor[*rt.List[int64]](),
		reflect.TypeFor[rt.Duration](),
		reflect.TypeFor[struct {
			Tag  uint8
			Some int64
		}](),
	} {
		if k := preludeKindOfGoType(bad); k != kindInvalid {
			t.Errorf("%s projected as %q; only an rt prelude instance may", bad, k.def.nomi)
		}
	}
}

// opaqueSpecIndex resolves a spec by Nomi name, so a test names the type rather
// than a position in a slice that grows.
func opaqueSpecIndex(t *testing.T, nomi string) int {
	t.Helper()
	for i := range opaqueSpecs {
		if opaqueSpecs[i].nomi == nomi {
			return i
		}
	}
	t.Fatalf("no opaqueSpec for %q", nomi)
	return -1
}

func presence(m map[string]kind) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// sortedKeysOf is a set as a sorted slice, for a failure message that must be
// deterministic run to run.
func sortedKeysOf(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
