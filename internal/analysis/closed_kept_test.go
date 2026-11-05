package analysis_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// renderClosed renders what a request reads of a closed file's analysis:
// its analysis dumped (DumpFileForTest: every reference, definition and
// expression type, its scopes and its diagnostics), its syntax errors and
// repaired declarations, and its nodes.
func renderClosed(snap *analysis.DocSnapshot, root string) string {
	if snap == nil {
		return "(none)\n"
	}
	s := fmt.Sprintf("syntax %d damaged %d nodes %d content %d\n", len(snap.Errors), len(snap.Damaged), len(snap.Nodes), len(snap.Content))
	if snap.Analysis == nil {
		return s + "(no analysis)\n"
	}
	dump := analysis.DumpFileForTest(snap.Analysis, analysis.ErrorsInFile(fileuri.ToPath(snap.URI), snap.TypeErrors()), root)
	// What a derive or a universal Debug impl synthesizes sits at lines no
	// request reaches, and a build of the file alone checks less of it
	// than its program's check does.
	var kept []string
	for _, line := range strings.Split(dump, "\n") {
		var kind string
		var at int
		if n, _ := fmt.Sscanf(line, "%s %d:", &kind, &at); n == 2 && analysis.IsSynthesizedLine(at) {
			continue
		}
		kept = append(kept, line)
	}
	return s + strings.Join(kept, "\n")
}

// closedDiff opens the files with keys open in a manager whose project
// is dir (each file's text on disk is texts'), analyzes them, and then
// answers each closed file of the project as a request does (Analyzed)
// and compares it with a fresh build of that file in a manager that has
// analyzed nothing. It answers the first difference, or "", and how many
// files the closed answers built.
func closedDiff(tb testing.TB, dm *analysis.DocumentManager, dir string, texts map[string]string, open map[string]bool) (string, int64) {
	tb.Helper()
	uri := func(key string) string { return fileuri.FromPath(filepath.Join(dir, filepath.FromSlash(key)+".nomi")) }
	keys := make([]string, 0, len(texts))
	for key := range texts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var built int64
	for _, key := range keys {
		if open[key] {
			continue
		}
		before := dm.FilesBuilt()
		release := dm.View()
		got := renderClosed(dm.Analyzed(uri(key)), dir)
		release()
		built += dm.FilesBuilt() - before
		want := renderClosed(newManager().Analyzed(uri(key)), dir)
		if got != want {
			return fmt.Sprintf("closed %s answered from the open documents' program:\n%s", key, dumpDiff(want, got)), built
		}
	}
	return "", built
}

// TestClosedFilesFromProgramsMatchTheirOwnBuilds: a closed file of the
// program kept for an open document is answered from that program
// (keptClosed), and what a request reads of it is what a build of the
// file on its own gives: every closed file of every multi-file corpus
// project, of orderProject, layeredProject and rateProject, with the
// entry open, and after each step of edits to the open entry, each
// written to disk as a save does.
func TestClosedFilesFromProgramsMatchTheirOwnBuilds(t *testing.T) {
	t.Parallel()
	projects := []incrementalProject{orderIncremental(), layeredProject(), rateProject()}
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	projects = append(projects, corpus...)
	var fromProgram int
	for _, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			dir := writeProject(t, p)
			uri := func(key string) string { return fileuri.FromPath(filepath.Join(dir, filepath.FromSlash(key)+".nomi")) }
			dm := newManager()
			dm.SetWorkspaceRoot(dir)
			dm.IndexWorkspace(context.Background())
			texts := map[string]string{}
			for key, text := range p.texts {
				texts[key] = text
			}
			open := map[string]bool{p.entry: true}
			dm.Open(uri(p.entry), texts[p.entry])
			steps := genericEdits(p.entry, texts[p.entry])
			for i := 0; i <= len(steps); i++ {
				if i > 0 {
					s := steps[i-1]
					text, ok := s.change(texts[s.key], p.texts[s.key])
					if !ok {
						continue
					}
					texts[s.key] = text
					if err := os.WriteFile(fileuri.ToPath(uri(s.key)), []byte(text), 0o644); err != nil {
						t.Fatal(err)
					}
					dm.Update(uri(s.key), text)
					// What the server does after an edit: the closed files'
					// cached analyses that read the edited file go.
					dm.UpdateImportEdges(uri(s.key))
					dm.PropagateChange(uri(s.key))
				}
				diff, built := closedDiff(t, dm, dir, texts, open)
				if diff != "" {
					t.Fatalf("after %d steps: %s", i, diff)
				}
				if built == 0 && len(texts) > 1 {
					fromProgram++
				}
			}
		})
	}
	if fromProgram == 0 {
		t.Error("no closed file was answered from a kept program")
	}
}
