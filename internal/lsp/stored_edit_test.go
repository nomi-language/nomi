package lsp

import (
	"os"
	"slices"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/projectgen"
	"github.com/nomi-language/nomi/internal/rotation"
)

// answersInRecord makes answers leave out the requests that check a
// document opened from its record whole (CheckWhole): implementation, and
// references and rename of an interface method, so a test can keep the
// program opened from its record across edits.
var answersInRecord bool

// interfaceMethodAt reports whether the symbol at line:col of the open
// document at uri is an interface method.
func interfaceMethodAt(s *Server, uri string, line, col int) bool {
	defer s.docs.View()()
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		return false
	}
	sym := snap.Analysis.SymbolAt(analysis.Pos{Line: line, Col: col})
	if sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym != nil && sym.Kind == analysis.SymbolInterfaceMethod
}

// TestOpenFromRecordEditsMatchAFullCheck: after each of projectgen's edit
// kinds, made in a server that opened a generated project from its record
// and in one that checked it whole, and after its undoing, each lowering
// the open documents as the server does after an edit (lowerOpen), every
// request at every identifier of the open documents (the entry, the hub
// and a leaf) answers alike, references and rename over the closed files among
// them. Most edits must be taken in the program opened from the record.
// By default the project has about 400 lines and the requests are made at
// every fifth identifier (storedGenerated); NOMI_CORPUS_WALKS=full makes
// it 1,500 lines and asks at every identifier.
func TestOpenFromRecordEditsMatchAFullCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("runs every request over many documents, about 10 seconds; -short")
	}
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	answersInRecord = true
	defer func() { answersInRecord = false }()
	lines, every := storedGenerated(rotation.CorpusWalk(t, 1))
	gen := projectgen.Generate(projectgen.Config{Seed: 3, Lines: lines})
	dir := t.TempDir()
	if err := gen.Write(dir); err != nil {
		t.Fatal(err)
	}
	p := copyProject(t, dir, "generated")
	p.keys = []string{gen.Entry, gen.Hub(), gen.Leaf()}
	if !writeRecord(p) {
		t.Fatal("no record written")
	}
	full := openServerWithout(t, p, p.keys)
	stored := openServerFromRecord(p, p.keys)
	if opened, _ := stored.docs.StoredPrograms(); opened == 0 {
		t.Fatal("the record was not opened")
	}
	was := os.Getenv(artifactcache.RootEnv)
	steps := 0
	reachHovers := functionReachHovers.Load()
	// The typos leave something to report, which the program opened from
	// its record must word as the program checked whole does.
	kinds := append(slices.Clone(projectgen.EditKinds), projectgen.TypoLeaf, projectgen.TypoEntry)
	for _, kind := range kinds {
		key, text := gen.Edit(kind, 0)
		for _, step := range []struct{ what, text string }{{string(kind), text}, {string(kind) + ", undone", gen.Files[key]}} {
			uri := p.uri(key)
			taken := stored.docs.StoredEdits()
			for _, s := range []*Server{full, stored} {
				if s == full {
					os.Setenv(artifactcache.RootEnv, "off")
				}
				if !s.docs.IsOpen(uri) {
					s.docs.Open(uri, gen.Files[key])
				}
				s.docs.Update(uri, step.text)
				s.docs.UpdateImportEdges(uri)
				s.docs.PropagateChange(uri)
				lowerOpen(s, p, append(slices.Clone(p.keys), key))
				os.Setenv(artifactcache.RootEnv, was)
			}
			steps++
			typo := kind == projectgen.TypoLeaf || kind == projectgen.TypoEntry
			if typo && stored.docs.StoredEdits() == taken {
				t.Errorf("%s was not taken in the program opened from its record: %v", step.what, stored.docs.FullCheckReasons())
			}
			for _, k := range p.keys {
				want, got := answers(t, full, p, k, every), answers(t, stored, p, k, every)
				for i := range max(len(want), len(got)) {
					w, g := "(none)", "(none)"
					if i < len(want) {
						w = want[i]
					}
					if i < len(got) {
						g = got[i]
					}
					if w != g {
						t.Fatalf("after %s, %s opened from its record answers\n%s\nand checked whole\n%s", step.what, k, g, w)
					}
				}
			}
		}
	}
	if functionReachHovers.Load() == reachHovers {
		t.Error("no hover compared said what a function of the program reaches")
	}
	if taken := stored.docs.StoredEdits(); taken < int64(steps)/2 {
		t.Errorf("%d of %d edits were taken in the program opened from its record: %v", taken, steps, stored.docs.FullCheckReasons())
	}
	t.Logf("%d of %d edits taken from the record, %d closed files' references answered from it", stored.docs.StoredEdits(), steps, stored.docs.RefsFromRecord())
}
