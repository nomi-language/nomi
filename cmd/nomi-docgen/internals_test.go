package main

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// The reference leaves out what a program cannot name: the internal std
// modules have no page, and std/phrase's page shows none of the private
// constructors a `phrase` declaration's function calls.
func TestReferenceLeavesOutStdInternals(t *testing.T) {
	for _, name := range generatedReferenceModules(t) {
		if name == "phrase_format" || name == "money_format" {
			t.Errorf("docgen writes a page for the internal module std/%s", name)
		}
	}
	lib := std.Load()
	page := renderModuleWithLinks(lib, "phrase", referenceLinksForModules(lib, referenceCandidates(lib)))
	if page == "" {
		t.Fatal("std/phrase renders no page")
	}
	for _, name := range []string{"compiled", "compiled_noun", "localized_variant", "Argument"} {
		if strings.Contains(page, "`"+name) || strings.Contains(page, "fn "+name+"(") {
			t.Errorf("std/phrase's page shows the internal %s:\n%s", name, page)
		}
	}
}
