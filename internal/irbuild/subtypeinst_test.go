package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An instance of a generic std function whose type parameter is bounded by
// an interface that makes a value of its type, at a subtype, is one of its
// own, with a linkage name of its own: `Json.decode_as<NonNegativeInt>` is
// not `Json.decode_as<Int>`, though the two have one kind. An instance
// whose parameter has no such bound is its base's: `Maybe.is_some` over a
// `Maybe<NonNegativeInt>` is the one over `Maybe<Int>`.
func TestSubtypeInstance_AReadOfASubtypeIsAnInstanceOfItsOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	lib := stdAnchorLib()
	intFile := lib.Files["int"]
	if intFile == nil {
		t.Fatal("the stdlib has no std/int analysis")
	}
	sym := resolvedScopeSymbol(intFile.ModuleScope, "NonNegativeInt")
	if sym == nil || !analysis.IsRefinement(sym.Type) {
		t.Fatal("std/int declares no subtype NonNegativeInt")
	}
	nonNeg := sym.Type

	s := newStdInstances(stdlibLowering(), newDeclineCensus(DeclineHooks{}))
	pair := func(id analysis.StdFunc, k kind, base, sub analysis.Type) (*stdInst, *stdInst) {
		t.Helper()
		f := s.stdTemplateOf(id)
		if f == nil {
			t.Fatalf("no std template %v", id)
		}
		tps, _ := s.stdTemplateParams(f)
		at, why := s.instantiateTyped(f, tps, []kind{k}, []analysis.Type{base}, nil)
		if at == nil {
			t.Fatalf("%v at its base does not build: %s", id, why)
		}
		read, why := s.instantiateTyped(f, tps, []kind{k}, []analysis.Type{sub}, nil)
		if read == nil {
			t.Fatalf("%v at the subtype does not build: %s", id, why)
		}
		return at, read
	}

	at, read := pair(analysis.StdFunc{Module: "std/json", Owner: "Json", Name: "decode_as"}, kindInt, analysis.TypeInt, nonNeg)
	if at == read {
		t.Fatal("Json.decode_as at NonNegativeInt is the instance at Int: its `T.from_json` would make an Int no rule of NonNegativeInt checked")
	}
	if at.reads != nil || read.reads == nil {
		t.Fatalf("reads: at Int %q, at NonNegativeInt %q; want none and one", at.reads, read.reads)
	}
	t.Logf("at Int %q, at NonNegativeInt %q", at.sym.Link(), read.sym.Link())
	if a, b := at.sym.Link(), read.sym.Link(); a == "" || a == b {
		t.Fatalf("linkage names %q and %q: the two instances must be named apart", a, b)
	}
	again, _ := s.instantiateTyped(read.f, read.tps, read.args, []analysis.Type{nonNeg}, nil)
	if again != read {
		t.Fatal("a second instance at NonNegativeInt is not the first")
	}

	someAt, someRead := pair(analysis.StdFunc{Module: "std/maybe", Owner: "Maybe", Name: "is_some"}, kindInt, analysis.TypeInt, nonNeg)
	if someAt != someRead || someRead.reads != nil {
		t.Fatal("Maybe.is_some at NonNegativeInt is an instance of its own, though its T has no bound that makes a value")
	}
}
