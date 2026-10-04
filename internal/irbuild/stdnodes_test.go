package irbuild

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestStdModuleView_PopulatesNodes checks that newStdGen carries the module's
// `nodes`. Without them every `g.nodes` reader in this package answers
// vacuously for every stdlib module, and nothing in the emitted output need
// show it: a vacuous answer can equal the true one for today's stdlib, which
// is not a guarantee for tomorrow's.
//
// POSITIVE and DISCRIMINATING rather than a presence check. It asserts that the
// gen's nodes ARE the module's (same length and same first element) for a
// module known to carry top-level declarations, so a `nodes` set to an empty
// non-nil slice (which would satisfy "not nil" perfectly) fails here.
func TestStdModuleView_PopulatesNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	lib := std.Load()
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	full := stdlibLowering()

	checked := 0
	for _, name := range names {
		nodes := lib.Nodes[name]
		fa := lib.Files[name]
		if len(nodes) == 0 || fa == nil {
			continue
		}
		v := stdModuleContext(name, strings.TrimPrefix(lib.FileURI(name), "file://"),
			stdPackage(sort.SearchStrings(names, name)), nodes, fa, full)
		g := v.gen(full)
		if len(g.nodes) != len(nodes) {
			t.Fatalf("std/%s: the gen carries %d nodes, the module has %d — every `g.nodes` reader in "+
				"this package (declaresIterImpl, appImplNames, declaresBoot, templateWall, the "+
				"generic-impl template collection, collectHostPkgs, declareOnces, typeParamNames, "+
				"nestedTypeKeys, tail.go's graph) answers about the wrong tree for this module",
				name, len(g.nodes), len(nodes))
		}
		if len(nodes) > 0 && g.nodes[0] != nodes[0] {
			t.Fatalf("std/%s: the gen's nodes are a different slice from the module's", name)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no stdlib module was checked, so this test asserts nothing")
	}
	t.Logf("%d stdlib modules: the gen's nodes are the module's", checked)
}
