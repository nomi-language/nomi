package lsp

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/projectgen"
	"github.com/nomi-language/nomi/vmhost"
)

// A document's program lowered from its analysis after an edit that
// changes only bodies lowers the edited file alone and links it with the
// kept objects of the others (vmhost's checklowering_perfile.go), and
// reports what lowering the text with its own front end reports,
// diagnostic for diagnostic. The documents are a generated project's
// entry and a leaf, each edited by projectgen's body edits, made and
// undone, and the entry given a planted decline and then cleared of it.
func TestLoweringPerFileMatchesTheSource(t *testing.T) {
	plantDecline(t)
	if testing.Short() {
		t.Skip("lowers a generated project; -short")
	}
	gen := projectgen.Generate(projectgen.Config{Seed: 3, Lines: 1500})
	dir := t.TempDir()
	if err := gen.Write(dir); err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	before := vmhost.AnalyzedRelowersForTest()
	lowered := 0
	check := func(key, text, step string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(key)+".nomi")
		uri := pathToURI(path)
		s.docs.Open(uri, text)
		a, latest := s.loweringAnalysis(uri, text)
		if !latest {
			t.Fatalf("%s: the opened text is not the document's latest", step)
		}
		got := loweringDiagnostics(path, text, a)
		want := loweringDiagnostics(path, text, nil)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s in %s: from the analysis\n%s\nfrom the source\n%s", step, key, renderDiagnostics(got), renderDiagnostics(want))
		}
		if a != nil {
			lowered++
		}
	}
	for _, kinds := range []struct {
		key   string
		kinds []projectgen.EditKind
	}{
		{gen.Entry, []projectgen.EditKind{projectgen.BodyEntry}},
		{gen.Leaf(), []projectgen.EditKind{projectgen.BodyLeaf, projectgen.BodyCallLeaf, projectgen.SignatureLeaf, projectgen.BodyLeaf}},
	} {
		orig := gen.Files[kinds.key]
		check(kinds.key, orig, "opened")
		for _, kind := range kinds.kinds {
			was := vmhost.AnalyzedRelowersForTest()
			for n := 0; n < 3; n++ {
				_, text := gen.Edit(kind, n)
				check(kinds.key, text, fmt.Sprintf("%s %d", kind, n))
			}
			// A signature edit lowers the file and the files that import
			// it file by file too.
			if vmhost.AnalyzedRelowersForTest() == was {
				t.Errorf("%s of %s was lowered whole every time", kind, kinds.key)
			}
			check(kinds.key, orig, string(kind)+", undone")
		}
	}
	// An impl added to a leaf, and taken away, lowers the leaf and the files
	// that see the impl with the entry's program file by file: the leaf's
	// text is on disk, where the source's lowering reads it.
	leaf := filepath.Join(dir, filepath.FromSlash(gen.Leaf())+".nomi")
	was := vmhost.AnalyzedRelowersForTest()
	for n := 0; n < 3; n++ {
		_, text := gen.Edit(projectgen.ImplLeaf, n)
		s.docs.Open(pathToURI(leaf), text)
		writeFile(t, leaf, text)
		check(gen.Entry, gen.Files[gen.Entry], fmt.Sprintf("the entry after %s %d", projectgen.ImplLeaf, n))
	}
	if vmhost.AnalyzedRelowersForTest()-was < 2 {
		t.Errorf("the entry was lowered whole after an impl added to a leaf (%d of 3 file by file)", vmhost.AnalyzedRelowersForTest()-was)
	}
	s.docs.Open(pathToURI(leaf), gen.Files[gen.Leaf()])
	writeFile(t, leaf, gen.Files[gen.Leaf()])
	check(gen.Entry, gen.Files[gen.Entry], "the entry after the leaf back")

	// A planted decline in the entry, which the kept lowering does not
	// record, is lowered whole and reported; so is its removal.
	entry := gen.Files[gen.Entry]
	blocked := strings.Replace(entry, "fn main()\n", "fn planted_decline(n: Int) -> Int\n    n\nend\n\nfn main()\n    _ = Iter.map([1], fn x -> planted_decline(x)) |> Iter.to_list()\n", 1)
	if blocked == entry {
		t.Fatal("the entry has no main to plant a decline before")
	}
	check(gen.Entry, blocked, "a planted decline")
	if diags := loweringDiagnostics(filepath.Join(dir, "main.nomi"), blocked, nil); len(diags) == 0 {
		t.Fatal("the planted decline is not reported, so the comparison says nothing about it")
	}
	_, text := gen.Edit(projectgen.BodyEntry, 0)
	check(gen.Entry, strings.Replace(blocked, "total = 0\n", "total = 1\n", 1), "a body edit beside the planted decline")
	check(gen.Entry, text, "the planted decline removed")
	check(gen.Entry, entry, "the entry back")
	if lowered == 0 {
		t.Fatal("no text was lowered from its analysis")
	}
	if vmhost.AnalyzedRelowersForTest() == before {
		t.Fatal("no lowering lowered only the edited file, so the comparisons say nothing about it")
	}
	t.Logf("%d texts lowered from their analysis, %d of them file by file", lowered, vmhost.AnalyzedRelowersForTest()-before)
}
