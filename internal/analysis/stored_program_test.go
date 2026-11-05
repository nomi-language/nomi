package analysis_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// renderOpen renders an open document's installed analysis as the
// language server reads it: its nodes, syntax errors and repaired
// declarations, and its analysis dumped. The program it offers is left
// out: a program opened from its record offers none (see
// stored_program.go).
func renderOpen(dm *analysis.DocumentManager, uri, root string) string {
	defer dm.View()()
	snap := dm.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		return "(no analysis)\n"
	}
	return renderClosed(snap, root)
}

// recordEdit is an edit recordDiff makes after the record is written: the
// text of the file with key replaced, on disk before the documents are
// opened (a record of the old texts must not answer), or in the open
// document after they are (the program is then checked whole).
type recordEdit struct {
	key, text string
	onDisk    bool
}

// recordDiff writes the record of p's program (as the server does after
// the entry's lowering finds nothing), makes edit when it is not nil,
// opens every file of p in key order in a manager that opens programs
// from their records and in one with the artifact cache off, and answers
// the first document whose analysis differs, or "". It reports whether a
// record was written.
func recordDiff(tb testing.TB, p incrementalProject, dir string, edit *recordEdit) (string, bool) {
	tb.Helper()
	uri := func(key string) string { return fileuri.FromPath(filepath.Join(dir, filepath.FromSlash(key)+".nomi")) }
	keys := []string{p.entry}
	texts := map[string]string{}
	for key, text := range p.texts {
		texts[key] = text
		if key != p.entry {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys[1:])
	writer := newManager()
	writer.SetWorkspaceRoot(dir)
	writer.Open(uri(p.entry), p.texts[p.entry])
	writer.AnalyzeLatest(context.Background(), uri(p.entry))
	writer.NoteLoweredClean(uri(p.entry), p.texts[p.entry])
	if _, stored := writer.StoredPrograms(); stored == 0 {
		return "", false
	}
	if edit != nil && edit.onDisk {
		texts[edit.key] = edit.text
		if err := os.WriteFile(fileuri.ToPath(uri(edit.key)), []byte(edit.text), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	open := func(dm *analysis.DocumentManager) {
		dm.SetWorkspaceRoot(dir)
		for _, key := range keys {
			dm.Open(uri(key), texts[key])
			dm.AnalyzeLatest(context.Background(), uri(key))
			dm.UpdateImportEdges(uri(key))
		}
	}
	stored := newManager()
	open(stored)
	opened, _ := stored.StoredPrograms()
	switch {
	case edit != nil && edit.onDisk && edit.text != p.texts[edit.key] && opened > 0:
		return fmt.Sprintf("a record of the texts before %s changed on disk was opened", edit.key), true
	case (edit == nil || !edit.onDisk) && opened == 0:
		return "the record written was not opened", true
	}
	was := os.Getenv(artifactcache.RootEnv)
	os.Setenv(artifactcache.RootEnv, "off")
	full := newManager()
	open(full)
	os.Setenv(artifactcache.RootEnv, was)
	if edit != nil && !edit.onDisk {
		for _, dm := range []*analysis.DocumentManager{stored, full} {
			dm.Update(uri(edit.key), edit.text)
			dm.UpdateImportEdges(uri(edit.key))
			dm.PropagateChange(uri(edit.key))
		}
	}
	for _, key := range keys {
		want, got := renderOpen(full, uri(key), dir), renderOpen(stored, uri(key), dir)
		if want != got {
			return fmt.Sprintf("%s opened from its record:\n%s", key, dumpDiff(want, got)), true
		}
	}
	return "", true
}

// TestOpenFromRecordMatchesAFullCheckDumps: every document of every
// multi-file corpus project and of orderProject, layeredProject and
// rateProject opened from its program's record has the analysis it has
// when its program is checked whole, dump for dump (DumpFileForTest), with
// the same diagnostics, nodes and offered program.
func TestOpenFromRecordMatchesAFullCheckDumps(t *testing.T) {
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	projects := []incrementalProject{orderIncremental(), layeredProject(), rateProject()}
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	projects = append(projects, corpus...)
	used := 0
	for _, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			dir := writeProject(t, p)
			diff, ok := recordDiff(t, p, dir, nil)
			if !ok {
				t.Skip("no record")
			}
			used++
			if diff != "" {
				t.Fatal(diff)
			}
		})
	}
	if used == 0 {
		t.Error("no program was opened from its record")
	}
}

// FuzzOpenFromRecordMatchesFull writes the record of a project of
// fuzzProjects, replaces one file's text with the input, on disk before
// the documents are opened or in the open document after, and compares
// every document opened from the record with the same document of a
// program checked whole (recordDiff). Run it with
//
//	go test ./internal/analysis -run '^$' -fuzz '^FuzzOpenFromRecordMatchesFull$' -fuzztime 5m -parallel 4
//
// Under plain `go test` it runs its seeds: each project's first file,
// with a body edit made in the open document and on disk.
func FuzzOpenFromRecordMatchesFull(f *testing.F) {
	projects := fuzzProjects(f)
	for i, p := range projects {
		if i%4 != 0 {
			continue
		}
		f.Add(uint(i), uint(0), p.texts[p.entry]+"\nfn zz_added() -> Int { 1 }\n", false)
		f.Add(uint(i), uint(0), p.texts[p.entry]+"\nfn zz_added() -> Int { 1 }\n", true)
	}
	f.Fuzz(func(t *testing.T, project, file uint, src string, onDisk bool) {
		if !utf8.ValidString(src) || len(src) > 2*fuzzSeedText {
			t.Skip()
		}
		t.Setenv(artifactcache.RootEnv, t.TempDir())
		p, key := projectFile(projects, project, file)
		dir := writeProject(t, p)
		if diff, _ := recordDiff(t, p, dir, &recordEdit{key: key, text: src, onDisk: onDisk}); diff != "" {
			t.Fatal(diff)
		}
	})
}
