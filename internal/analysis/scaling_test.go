package analysis_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/projectgen"
)

// TestEditWorkStaysFlatAsAProjectGrows counts the work each kind of edit
// costs the language server's kept program (DocumentManager.RecheckEdits)
// in generated projects of about 1k and 10k lines (internal/projectgen):
// the files whose bodies CheckTypes walks. It counts work, not time.
//
//   - A body-only edit, in a leaf, a hub or the entry, re-checks the edited
//     file and nothing else, at every size.
//   - A signature edit re-checks the edited file and the files that import
//     it, directly or through others, and no more.
//
// Discovery walks the imports of the files a dependents recheck checks
// afresh and no others (ImportsWalksForTest), and a body edit through a
// language server's document reads no unchanged closed file from disk
// (DiskReadsForTest).
//
// It also counts the visits the program-wide passes make to project files
// (CountProgramWorkForTest). After a body-only edit they visit the edited
// file alone, and the recordings a dependents recheck scans grow at most
// linearly. It pins where the work still grows with the project: an impl
// added to a leaf takes a full check.
//
// The program before and after every edit must check with no diagnostic.
// The 50k-line project is in the benchmarks only (internal/scalebench).
func TestEditWorkStaysFlatAsAProjectGrows(t *testing.T) {
	work := map[projectgen.EditKind][]counts{}
	for _, lines := range []int{1000, 10000} {
		t.Run(fmt.Sprint(lines), func(t *testing.T) {
			p := projectgen.Generate(projectgen.Config{Seed: 1, Lines: lines})
			root := fmt.Sprintf("/nomi-scaling/%d", lines)
			texts := p.ProgramTexts()
			dm := newManager()
			prog := dm.CheckProgram(root, p.Entry, texts)
			requireClean(t, prog, "the generated program")
			all := len(prog.Files)
			for _, kind := range projectgen.EditKinds {
				key, text := p.Edit(kind, 0)
				if text == prog.Texts[key] {
					t.Fatalf("%s: the edit changes nothing in %s", kind, key)
				}
				var next *analysis.CheckedProgram
				var outcome analysis.RecheckOutcome
				var walked []*analysis.FileAnalysis
				var importWalks int64
				visits, scans := analysis.CountProgramWorkForTest(func() {
					walked = analysis.BodyChecksForTest(func() {
						importWalks = analysis.ImportsWalksForTest(func() {
							next, outcome = dm.RecheckEdits(prog, []analysis.ProgramEdit{{Key: key, Text: text}})
						})
					})
				})
				requireClean(t, next, fmt.Sprintf("the program after the %s", kind))
				got := checkedKeys(next, walked)
				dependents := p.Dependents(key)
				t.Logf("%-22s %-10s %-20s rechecked %3d of %3d files (%d import it); program-wide visits %d, recordings scanned %d, imports walked in %d files",
					kind, outcome.Path, key, len(got), all, len(dependents), visits, scans, importWalks)
				work[kind] = append(work[kind], counts{files: all, lines: p.Lines(), visits: visits, scans: scans})

				switch kind {
				case projectgen.BodyLeaf, projectgen.BodyHub, projectgen.BodyEntry:
					if outcome.Path != analysis.RecheckBodies || !sameKeys(got, []string{key}) {
						t.Errorf("%s in %s: want the bodies path re-checking %s alone, got %s re-checking %d files: %s",
							kind, key, key, outcome.Path, len(got), describeKeys(got))
					}
					if importWalks != 0 {
						t.Errorf("%s in %s: the bodies path walked the imports of %d files", kind, key, importWalks)
					}
				case projectgen.SignatureLeaf, projectgen.SignatureHub:
					// The hub, core/util, is imported directly or through
					// others by most files, so its set is most of the
					// program; still not core/ids, which it imports.
					want := append([]string{key}, dependents...)
					if len(want) >= all {
						t.Fatalf("%s: every file imports %s, and recheckDependents takes a full check then; the generator changed, and this case needs its own expectation", kind, key)
					}
					if outcome.Path != analysis.RecheckDependents || !sameKeys(got, want) {
						t.Errorf("%s in %s: want the dependents path re-checking %d files (%s), got %s re-checking %d: %s",
							kind, key, len(want), describeKeys(want), outcome.Path, len(got), describeKeys(got))
					}
					// Discovery walks the nodes of the files it loads, and
					// takes a kept file's imports from its analysis
					// (FileAnalysis.importPaths), kept across the body
					// edits before: one walk per file checked afresh.
					if importWalks != int64(len(want)) {
						t.Errorf("%s in %s: want the imports of the %d files checked afresh walked, got %d walks",
							kind, key, len(want), importWalks)
					}
				case projectgen.ImplLeaf:
					// FINDING, pinned: a new impl in a leaf that only the entry
					// imports changes the leaf's impls fingerprint, and
					// recheckDependents then takes a full check
					// (implsFingerprint), so the work grows with the project:
					// every file is re-checked where the leaf and the entry
					// would do. When a fix narrows it, expect the leaf and
					// its dependents here.
					if outcome.Path != analysis.RecheckFull || len(got) != all {
						t.Errorf("%s in %s: pinned as a full check of %d files, got %s re-checking %d; if an impl edit now re-checks less, update this case",
							kind, key, all, outcome.Path, len(got))
					}
				}
				prog = next
			}

			bodyEditsReadNoClosedFile(t, p)
		})
	}
	if t.Failed() {
		return
	}

	// A body-only edit reruns the program-wide reductions, but each keeps
	// what every file contributed (FinalizeCoherence's programDemands, and
	// programChecks.reduce's application-field roots) and reads again only
	// the file whose bodies were rebuilt: one visit, at every size. Before,
	// they visited every file twice (100 visits at 10k lines, 27 ms at
	// 50k).
	for _, kind := range []projectgen.EditKind{projectgen.BodyLeaf, projectgen.BodyHub, projectgen.BodyEntry} {
		for _, c := range work[kind] {
			if c.visits != 1 {
				t.Errorf("%s: want 1 program-wide visit, to the edited file, got %d for %d files",
					kind, c.visits, c.files)
			}
		}
	}

	// On the dependents path, laying the kept files' facts over the
	// stdlib's (programShare.layOver, mergeRecordings) and folding the
	// build's own files (buildProjectImplIndex) drop duplicate recordings
	// with a set, one lookup per recording, so the recordings scanned grow
	// no faster than the project: about 2.3k at 1k lines and 12k at 10k. A
	// scan of the list kept so far per recording grew with the square of
	// the project (2.2M at 10k lines, 50M at 50k).
	for _, kind := range []projectgen.EditKind{projectgen.SignatureLeaf, projectgen.SignatureHub} {
		c := work[kind]
		if len(c) != 2 || c[0].scans == 0 {
			t.Errorf("%s: want recordings scanned at both sizes, got %+v", kind, c)
			continue
		}
		lines := float64(c[1].lines) / float64(c[0].lines)
		scans := float64(c[1].scans) / float64(c[0].scans)
		if scans > 1.5*lines {
			t.Errorf("%s: the recordings scanned went from %d to %d as the project went from %d to %d lines, faster than the project grows",
				kind, c[0].scans, c[1].scans, c[0].lines, c[1].lines)
		}
	}
}

// bodyEditsReadNoClosedFile writes p to disk, its files last modified an
// hour ago, opens each file a body edit edits in a language server's
// document manager, and edits it once: the analysis compares every closed
// file of the kept program with the text it was checked from by its stat
// (readProjectFile), and reads none of them.
func bodyEditsReadNoClosedFile(t *testing.T, p *projectgen.Project) {
	t.Helper()
	dir := t.TempDir()
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	backdate(t, dir, time.Now().Add(-time.Hour))
	dm := newManager()
	dm.SetWorkspaceRoot(dir)
	for _, kind := range []projectgen.EditKind{projectgen.BodyLeaf, projectgen.BodyHub, projectgen.BodyEntry} {
		key, text := p.Edit(kind, 0)
		uri := "file://" + filepath.Join(dir, filepath.FromSlash(key)+".nomi")
		dm.Open(uri, p.Files[key])
		bodies, _, _, _ := dm.ProgramChecks()
		reads := analysis.DiskReadsForTest(func() { dm.Update(uri, text) })
		if after, _, _, _ := dm.ProgramChecks(); after != bodies+1 {
			t.Errorf("%s in %s: want the bodies path, got full checks because %v", kind, key, dm.FullCheckReasons())
		}
		if len(reads) > 0 {
			t.Errorf("%s in %s: the analysis read %d unchanged closed files, %s first", kind, key, len(reads), reads[0])
		}
	}
}

// backdate sets the modification time of every file under dir to at.
func backdate(t *testing.T, dir string, at time.Time) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Chtimes(path, at, at)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// requireClean fails t when p holds a diagnostic.
func requireClean(t *testing.T, p *analysis.CheckedProgram, what string) {
	t.Helper()
	for key, errs := range p.Syntax {
		if len(errs) > 0 {
			t.Fatalf("%s: %s has a syntax error: %v", what, key, errs[0])
		}
	}
	for key, errs := range p.Errors {
		if len(errs) > 0 {
			t.Fatalf("%s: %s has %d diagnostics, the first %d:%d: %s", what, key, len(errs), errs[0].Line, errs[0].Col, errs[0].Message)
		}
	}
}

// checkedKeys answers the module keys of the project files among walked,
// each once, in key order. Stdlib analyses have no key in p.
func checkedKeys(p *analysis.CheckedProgram, walked []*analysis.FileAnalysis) []string {
	keyOf := map[*analysis.FileAnalysis]string{}
	for key, fa := range p.Files {
		keyOf[fa] = key
	}
	seen := map[string]bool{}
	for _, fa := range walked {
		if key, ok := keyOf[fa]; ok {
			seen[key] = true
		}
	}
	out := make([]string, 0, len(seen))
	for key := range seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sameKeys(got, want []string) bool {
	w := append([]string(nil), want...)
	sort.Strings(w)
	return strings.Join(got, ",") == strings.Join(w, ",")
}

func describeKeys(keys []string) string {
	if len(keys) > 8 {
		return strings.Join(keys[:8], ", ") + fmt.Sprintf(" and %d more", len(keys)-8)
	}
	return strings.Join(keys, ", ")
}

// counts is the work one edit cost at one size.
type counts struct {
	files, lines  int
	visits, scans int64
}
