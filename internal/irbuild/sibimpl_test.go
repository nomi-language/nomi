package irbuild

import (
	"testing"
)

// The visibility fence, asserted on the INDEX rather than on a program.
//
// A non-`pub` inherent impl function is unreachable across a file boundary by
// the ANALYZER's rule, not by the builder's caution: on a two-file program,
// `Widget.hidden(w)` from a sibling is "type 'Widget' has no member 'hidden'",
// a front-end error, so no such call can ever reach the builder. That makes a
// call-site test impossible and the fence itself invisible, so the refusal is
// pinned where it is observable: on the entry the index builds for `Status.bare`, which main.nomi
// deliberately never calls.
//
// The `pub` ones are asserted beside it, because an index that refused
// everything would satisfy the negative half alone.
func TestSiblingImpl_PrivateInherentFunctionIsRefusedInTheIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	p, err := Analyze(fixture("sibimpl/main.nomi"))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	idx := implMemberIndexFor(t, p)
	for name, wantWhy := range map[string]string{
		"Status.bare":      "private sibling file impl function",
		"Status.active":    "",
		"Status.is_active": "",
		"Status.reason":    "",
		// An INTERFACE impl function carries no `pub` and must not be refused
		// for the lack of it.
		"Tag.inspect": "",
		// The SYNTHESIZED universal `Debug` derive, which carries no `pub`
		// either and whose block is `impl Debug for Status` — so it is an
		// interface impl and the fence must not claim it. main.nomi calls this
		// one, so a wrong answer here also shows up in the stdout pin.
		"Status.inspect": "",
	} {
		sites := idx[name]
		if len(sites) != 1 {
			t.Fatalf("%s: want exactly one indexed impl function, got %d", name, len(sites))
		}
		if got := sites[0].fn.why; got != wantWhy {
			t.Errorf("%s: why = %q, want %q", name, got, wantWhy)
		}
	}
}

// implMemberIndexFor rebuilds the program's cross-file impl-member index and
// re-keys it by RECEIVER NAME plus method name, which is what identifies an
// entry here. Not by method alone: the front end synthesizes a universal
// `impl Debug for T` for every declared type, so `inspect` alone names two
// entries in a three-declaration fixture and a method-keyed lookup cannot say
// which.
func implMemberIndexFor(t *testing.T, p *Program) map[string][]implMemberSite {
	t.Helper()
	reg := buildTypeRegistry(p)
	files := buildFileIndex(p, reg)
	gens := make([]*gen, len(p.Modules))
	for i := range p.Modules {
		gens[i] = newGen(&p.Modules[i], unitPackage(i), stdlibLowering(), files, i, reg)
	}
	reg.gens = gens
	for i := range gens {
		reg.ensureTypes(i)
	}
	files.resolveSignatures(gens)
	for _, g := range gens {
		g.declareFuncs()
	}
	files.resolveImplMembers(gens)
	out := map[string][]implMemberSite{}
	for k, sites := range files.implMembers {
		recv := "?"
		if o := reg.byDecl[k.recv]; o != nil {
			recv = o.nomi
		}
		out[recv+"."+k.method] = append(out[recv+"."+k.method], sites...)
	}
	if len(out) == 0 {
		t.Fatal("the index is empty, so nothing below asserts anything")
	}
	return out
}
