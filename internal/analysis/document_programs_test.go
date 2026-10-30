package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// writeProject writes p's files under a new directory and answers it.
func writeProject(tb testing.TB, p incrementalProject) string {
	tb.Helper()
	dir := tb.TempDir()
	for key, text := range p.texts {
		path := filepath.Join(dir, filepath.FromSlash(key)+".nomi")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return dir
}

// renderBuild renders what the language server publishes and serves from
// one document's build: its diagnostics in order, its syntax errors and
// repaired declarations, how many nodes it has, and whether it offers a
// program, with how many other files.
func renderBuild(b analysis.BuiltForTest) string {
	var s strings.Builder
	for _, e := range b.TypeErrors {
		fmt.Fprintf(&s, "diag %d:%d-%d:%d %s %v\n", e.Line, e.Col, e.EndLine, e.EndCol, e.Message, e.Hints)
		for _, r := range e.Related {
			fmt.Fprintf(&s, "  related %s %d:%d %s\n", filepath.Base(r.File), r.Line, r.Col, r.Message)
		}
	}
	fmt.Fprintf(&s, "syntax %d damaged %d nodes %d\n", b.Syntax, b.Damaged, b.Nodes)
	if b.Program {
		fmt.Fprintf(&s, "program with %d files, reaches entry %v\n", b.ProgramFiles, b.ReachesEntry)
	}
	return s.String()
}

// TestOpenDocumentProgramsMatchTheirOwnBuilds: an open document analyzed
// through the program kept for its root (buildOpen) gives the language
// server what analyzing the document on its own gave it (build): the same
// diagnostics in the same order, syntax errors, nodes, and offered program.
// Every file of every multi-file corpus project and of orderProject is
// opened alone in a manager of its own.
func TestOpenDocumentProgramsMatchTheirOwnBuilds(t *testing.T) {
	projects := []incrementalProject{orderIncremental()}
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	projects = append(projects, corpus...)
	for _, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			dir := writeProject(t, p)
			keys := make([]string, 0, len(p.texts))
			for key := range p.texts {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				uri := fileuri.FromPath(filepath.Join(dir, filepath.FromSlash(key)+".nomi"))
				own := renderBuild(analysis.BuildForTest(newManager(), uri, p.texts[key], false))
				kept := renderBuild(analysis.BuildForTest(newManager(), uri, p.texts[key], true))
				if own != kept {
					t.Errorf("%s: through its program the document gets\n%s\nand on its own\n%s", key, kept, own)
				}
			}
		})
	}
}

// TestOpenDocumentEditsMatchFreshBuilds: a language server's documents
// edited one step after another, each analysis rechecking the program kept
// for their root, get what a fresh analysis of the same texts on its own
// gives (build, in a manager that has analyzed nothing). Every file of the
// project is open and its text on disk too, so the two read the same texts.
// A body-only step rebuilds bodies alone, and no other step does.
func TestOpenDocumentEditsMatchFreshBuilds(t *testing.T) {
	type run struct {
		p     incrementalProject
		steps []editStep
	}
	order := orderIncremental()
	runs := []run{
		{layeredProject(), layeredEdits},
		{order, orderEdits},
		{order, genericEdits("main", order.texts["main"])},
		{order, genericEdits("shapes", order.texts["shapes"])},
		{rateProject(), rateEdits},
	}
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range corpus {
		if i%3 == 0 {
			runs = append(runs, run{p, genericEdits(p.entry, p.texts[p.entry])})
		}
	}
	for _, r := range runs {
		t.Run(r.p.name, func(t *testing.T) {
			dir := writeProject(t, r.p)
			uri := func(key string) string { return fileuri.FromPath(filepath.Join(dir, filepath.FromSlash(key)+".nomi")) }
			dm := newManager()
			cur := map[string]string{}
			for key, text := range r.p.texts {
				cur[key] = text
				dm.Open(uri(key), text)
			}
			for key := range r.p.texts {
				dm.UpdateImportEdges(uri(key))
			}
			for i, s := range r.steps {
				text, ok := s.change(cur[s.key], r.p.texts[s.key])
				if !ok {
					t.Fatalf("step %d (%s) does not apply to %s", i+1, s.kind, s.key)
				}
				cur[s.key] = text
				if err := os.WriteFile(fileuri.ToPath(uri(s.key)), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
				bodies, _, _, _ := dm.ProgramChecks()
				dm.Update(uri(s.key), text)
				after, _, _, _ := dm.ProgramChecks()
				if fast := after > bodies; fast != bodyOnly(s.kind) {
					t.Errorf("step %d (%s, editing %s): rebuilt bodies alone: %v", i+1, s.kind, s.key, fast)
				}
				// The edited document, and the open documents that import
				// it, which the server analyzes again after it.
				dm.UpdateImportEdges(uri(s.key))
				docs := []string{uri(s.key)}
				importers, _ := dm.PropagateChange(uri(s.key))
				for _, doc := range importers {
					docs = append(docs, doc.URI)
				}
				for _, u := range docs {
					key := strings.TrimSuffix(strings.TrimPrefix(u, fileuri.FromPath(dir)+"/"), ".nomi")
					installed := analysis.InstalledForTest(dm, u)
					got := renderBuild(installed)
					want := renderBuild(analysis.BuildForTest(newManager(), u, cur[key], false))
					if got != want {
						t.Fatalf("step %d (%s, editing %s): %s gets\n%s\nand a fresh analysis of it gives\n%s", i+1, s.kind, s.key, key, got, want)
					}
					// The program offered to a file an entry reaches, which
					// analyzing the file on its own never offers, is the one a
					// fresh program check offers.
					if fresh := analysis.BuildForTest(newManager(), u, cur[key], true); installed.MemberProgramFiles != fresh.MemberProgramFiles {
						t.Fatalf("step %d (%s, editing %s): %s is offered a program of %d files, and a fresh check of its program offers %d", i+1, s.kind, s.key, key, installed.MemberProgramFiles, fresh.MemberProgramFiles)
					}
				}
			}
		})
	}
}

// TestOpenDocumentAfterSeveralFilesChange: when files the open entry's
// program reads change on disk together, as a checkout changes them, the
// next analysis of the entry rechecks the program with all of them, and gets
// what a fresh analysis of the entry on its own gives. Changes to bodies
// alone rebuild those bodies; a changed signature checks the changed files
// and the files that import them.
func TestOpenDocumentAfterSeveralFilesChange(t *testing.T) {
	p := layeredProject()
	dir := writeProject(t, p)
	path := func(key string) string { return filepath.Join(dir, key+".nomi") }
	uri := fileuri.FromPath(path("main"))
	dm := newManager()
	dm.Open(uri, p.texts["main"])
	for i, change := range []struct {
		kind  string
		files map[string]string
		path  string
	}{
		{"three bodies", map[string]string{
			"view":    strings.Replace(p.texts["view"], `"${item.name}: `, `"item ${item.name}: `, 1),
			"palette": strings.Replace(p.texts["palette"], `"red"`, `"blue"`, 1),
		}, "bodies"},
		{"a signature and a body", map[string]string{
			"view": strings.Replace(p.texts["view"], "pub fn show(item: Item) -> String {", "pub fn show(item: Item, prefix: String = \"\") -> String {", 1),
		}, "dependents"},
		{"back", map[string]string{"view": p.texts["view"], "palette": p.texts["palette"]}, "dependents"},
	} {
		for key, text := range change.files {
			if err := os.WriteFile(path(key), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// The entry's own edit, a body, makes the analysis run.
		main := strings.Replace(p.texts["main"], `"anvil"`, fmt.Sprintf(`"anvil %d"`, i), 1)
		bodies, dependents, full, _ := dm.ProgramChecks()
		dm.Update(uri, main)
		afterBodies, afterDependents, afterFull, _ := dm.ProgramChecks()
		got := map[string]int64{"bodies": afterBodies - bodies, "dependents": afterDependents - dependents, "full": afterFull - full}
		if got[change.path] != 1 {
			t.Errorf("step %d (%s) went %v, want %s; whole programs because: %v", i+1, change.kind, got, change.path, dm.FullCheckReasons())
		}
		if got, want := renderBuild(analysis.InstalledForTest(dm, uri)), renderBuild(analysis.BuildForTest(newManager(), uri, main, false)); got != want {
			t.Fatalf("step %d (%s): the entry gets\n%s\nand a fresh analysis of it gives\n%s", i+1, change.kind, got, want)
		}
	}
}
