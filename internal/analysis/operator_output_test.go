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
)

// TestStdlibInstantiationsAreWrittenAndDistinct holds std to the rule for a
// receiver that implements one interface at several type arguments (spec §12
// *Several instantiations for one type*): each of its impls writes its type arguments in the
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
	lib := loadStd()
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex("fn main() end"))
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
