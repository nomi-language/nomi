package analysis_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// TestStdlibExternOperatorImplsAreUncheckedButCollisionFree guards a gap in
// detectImplCollisions.
//
// Many of std's operator impl items are `host fn`, so they are
// `*ast.ExternFunc` nodes and land in IfaceMethodImplExterns, a parallel
// index that detectImplCollisions does not walk. `std/calendar`'s
// `impl Add<Years, Date> for Date { host fn add(...) }` is one of them. Two
// extern impls at one (receiver, header) would be a duplicate with no
// compile-time check in front of them.
//
// The check does not cover the extern index because the FuncDef side is keyed
// on the qualified receiver (`calendar.Date`, filled by
// PopulateQualifiedReceivers), while ImplBlockReceiverExtern holds bare names.
// Extending the check to it as it stands would reject two extern impls for two
// same-named types in different modules. Closing the gap means qualifying
// extern receivers first.
//
// Until then this test guards the absence: a stdlib edit that introduces an
// extern duplicate fails here even though the analyzer would accept it.
func TestStdlibExternOperatorImplsAreUncheckedButCollisionFree(t *testing.T) {
	idx := stdlibProjectImplIndex(t)

	groups := map[string][]string{}
	externs := 0
	for _, iface := range []string{"Add", "Subtract", "Multiply", "Divide"} {
		for method, exts := range idx.IfaceMethodImplExterns[iface] {
			for _, ext := range exts {
				header := idx.ImplBlockInterfaceKeyExtern[ext]
				recv := idx.ImplBlockReceiverExtern[ext]
				if header == "" || recv == "" {
					continue
				}
				externs++
				key := iface + "." + method + " " + recv + " " + header
				groups[key] = append(groups[key], header)
			}
		}
	}

	t.Logf("stdlib EXTERN operator impls (invisible to detectImplCollisions): %d in %d "+
		"(method, receiver, header) groups", externs, len(groups))
	if externs == 0 {
		t.Fatal("no extern operator impls reached the index. Either the stdlib stopped writing " +
			"`host fn` operator items — in which case this hole is closed and the test should say " +
			"so — or the extern index shape changed and this measured nothing")
	}

	var collisions []string
	for key, headers := range groups {
		if len(headers) < 2 {
			continue
		}
		sort.Strings(headers)
		collisions = append(collisions, key+": "+strings.Join(headers, ", "))
	}
	sort.Strings(collisions)
	if len(collisions) > 0 {
		t.Fatalf("two stdlib EXTERN impls implement one instantiation for one receiver. "+
			"detectImplCollisions does not walk the extern index, so nothing rejects this at "+
			"compile time:\n  %s", strings.Join(collisions, "\n  "))
	}
}

// TestStdlibInstantiationsAreWrittenAndDistinct holds std to the rule for a
// receiver that implements one interface at several type arguments (spec,
// "Generic Interfaces"): each of its impls writes its type arguments in the
// header, and no two write the same ones. The check is the program's own
// (checkImplInstantiations and detectImplCollisions); this reads the index
// they read and lists any std receiver outside the rule by name.
func TestStdlibInstantiationsAreWrittenAndDistinct(t *testing.T) {
	idx := stdlibProjectImplIndex(t)

	// (interface, receiver) -> the distinct headers its impls write.
	headers := map[string]map[string]bool{}
	for iface, byMethod := range idx.IfaceMethodImpls {
		for _, fns := range byMethod {
			for _, fn := range fns {
				header, recv := idx.ImplBlockInterfaceKey[fn], idx.ImplBlockReceiver[fn]
				if header == "" || recv == "" {
					continue
				}
				key := iface + " for " + recv
				if headers[key] == nil {
					headers[key] = map[string]bool{}
				}
				headers[key][header] = true
			}
		}
	}
	var offenders []string
	several := 0
	for key, hs := range headers {
		if len(hs) < 2 {
			continue
		}
		several++
		for h := range hs {
			if !strings.Contains(h, "<") {
				offenders = append(offenders, fmt.Sprintf("%s: `%s` writes no type arguments", key, h))
			}
		}
	}
	sort.Strings(offenders)
	t.Logf("std (interface, receiver) pairs with several instantiations: %d", several)
	if several == 0 {
		t.Fatal("no std receiver implements an interface at several type arguments, so this test " +
			"measured nothing; std's operator ladders (`Add` for `Date`) are such receivers")
	}
	if len(offenders) > 0 {
		t.Fatalf("a std receiver implements an interface more than once with an impl that does not "+
			"write its type arguments:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// stdlibProjectImplIndex drives the real BuildProjectWithCache pipeline over a
// trivial entry program and returns the project impl index. The entry's own impl
// set is empty, so everything the index holds came from the stdlib fold.
func stdlibProjectImplIndex(t *testing.T) *analysis.ProjectImplIndex {
	t.Helper()
	lib := std.Load()
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex("fn main() {}"))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		return nil, nil
	}
	entryFA, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, "", loader,
	)
	if entryFA == nil || entryFA.Program().ImplIndex == nil {
		t.Fatal("BuildProjectWithCache produced no project impl index")
	}
	return entryFA.Program().ImplIndex
}
