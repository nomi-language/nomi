package analysis_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// The harness a program the language server opens from its record
// (stored_program.go) is held to: it must answer what the program checked
// whole answers. Its tests:
//
//   - TestOpenFromRecordMatchesAFullCheck (internal/lsp, about 15 seconds)
//     writes the record of every multi-file corpus project and a generated
//     one, opens every file in a server that reads the record and in one
//     with the artifact cache off, and compares every position request at
//     every identifier and every published per-document result. A closed
//     file's references must have been served from a record. Position
//     lookups must not depend on map order: document highlight takes the
//     innermost reference, as FileAnalysis.TokenAt does, and rename's
//     nameAt orders its candidates.
//   - TestOpenFromRecordMatchesAFullCheckDumps compares the installed
//     analyses dump for dump, over each corpus project also with a typo and
//     then a syntax error in its entry's first body; most of those, and the
//     project that embeds files, must be recorded.
//   - FuzzOpenFromRecordMatchesFull replaces one file's text with the input
//     after the record is written, on disk before the files are opened (the
//     record must not be used) or in the open document after, and compares
//     again. Its open-document edits take the next test's path:
//
//	go test ./internal/analysis -run '^$' -fuzz '^FuzzOpenFromRecordMatchesFull$' -fuzztime 5m -parallel 4
//
//   - TestOpenFromRecordEditsMatchAFullCheck (about 28 seconds) writes the
//     record of the texts before each step of the incremental harness's
//     edit sequences, opens every file from it and in a program checked
//     whole, makes the step in the open document and compares every
//     document dump for dump. The project with an application type
//     (app_field_permission) also moves a read by lines (which must be
//     taken), adds a read the entry's main reaches, a factory whose lambda
//     reads the application type, and a test with no boot that reads it. A
//     step must be taken in the program opened from the record unless it
//     changes an impl, the program has an application type, or the manager
//     names a diagnostic only the program checked whole words. A body-only
//     step must rebuild the installed analysis in place; some step that
//     leaves something to report must be taken; some open document must be
//     checked from its installed check (ChecksAfterEdits); and some edit
//     must take the missing-impl supply from the entry's check, each equal
//     to the one worked out from every file's interface
//     (CheckSuppliesForTest).
//   - TestOpenFromRecordEditsMatchAFullCheck (internal/lsp, about 210
//     seconds) makes each of projectgen's edit kinds and its undoing in a
//     server that opened a generated project from its record, with its
//     entry, hub and a leaf open, and in one that checked it whole, and
//     compares every request at every identifier except those that check
//     the document whole (implementation, and references to an interface
//     method), references and rename over the closed files among them.
//     Most edits must be taken from the record, and projectgen's typos
//     (TypoLeaf, TypoEntry) and their undoing each must.
//   - TestServerOpensAProgramFromItsRecord (internal/lsp) drives two
//     servers over the protocol: the first writes the record after its
//     lowering, and the second opens from it, building one file, takes its
//     lowering's result from the record, and takes its first edit in the
//     program opened from the record.
//   - TestStoredLoweringMatchesTheText and
//     TestStoredLoweringMatchesTheTextGenerated (internal/lsp) lower the
//     entry after edits from the record's kept lowering and require the
//     diagnostics a lowering of the text reports, and a lowering file by
//     file after each body edit.
//   - TestRelinkChecksTheImportersOfAOnceWhoseTypeChanged (cmd/nomi): the
//     interface hash leaves `once` values out, so an unannotated `once`
//     whose value changed looks like an edit that changes no interface. The
//     relink from file records, the server's lowering file by file and the
//     record's edits check and lower its importers again
//     (InferredOncesChanged).
//   - TestOpenFromRecordReadsEmbeddedFiles and TestOpenFromRecordReadsCatalogs
//     hold a record to the embedded files and catalogs it read.
//   - TestClosedFilesFromProgramsMatchTheirOwnBuilds (closed_kept_test.go)
//     holds each closed file a kept program serves to a build of the file
//     alone with the same documents open, after each of a sequence of saved
//     and unsaved edits to the entry, and requires some file served past an
//     unsaved edit (ClosedPastUnsavedEntry).

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
	diff, ok, _ := recordDiffTaken(tb, p, dir, edit)
	return diff, ok
}

// recordTaken is how a manager that opened from a record took an edit in
// an open document: whether it took it in the program opened from the
// record (stored_edit.go), and whether the program checked whole has
// nothing to report after it.
type recordTaken struct {
	fast, clean bool
	// inPlace says the edit rebuilt the edited document's installed
	// analysis in place.
	inPlace bool
	// checksAfter counts the open documents' checks the edit made from
	// their installed checks (CheckAfter).
	checksAfter int64
	// inRecord says the edited document was analyzed in the program opened
	// from the record, and clean refers to those documents alone.
	inRecord bool
	// reasons are why the manager checked whole programs.
	reasons map[string]int
}

// recordDiffTaken is recordDiff, answering besides how the edit was taken.
func recordDiffTaken(tb testing.TB, p incrementalProject, dir string, edit *recordEdit) (string, bool, recordTaken) {
	tb.Helper()
	var taken recordTaken
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
	writer.NoteLoweredClean(uri(p.entry), p.texts[p.entry], nil)
	if _, stored := writer.StoredPrograms(); stored == 0 {
		return "", false, taken
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
		return fmt.Sprintf("a record of the texts before %s changed on disk was opened", edit.key), true, taken
	case (edit == nil || !edit.onDisk) && opened == 0:
		return "the record written was not opened", true, taken
	}
	was := os.Getenv(artifactcache.RootEnv)
	os.Setenv(artifactcache.RootEnv, "off")
	full := newManager()
	open(full)
	os.Setenv(artifactcache.RootEnv, was)
	if edit != nil && !edit.onDisk {
		before := stored.StoredEdits()
		beforeInPlace := stored.StoredInPlace()
		beforeAfter := stored.ChecksAfterEdits()
		// The documents analyzed in the program opened from the record.
		inRecord := map[string]bool{}
		for _, key := range keys {
			if snap := stored.Snapshot(uri(key)); snap != nil && snap.FromRecord {
				inRecord[key] = true
			}
		}
		taken.inRecord = inRecord[edit.key]
		for _, dm := range []*analysis.DocumentManager{stored, full} {
			dm.Update(uri(edit.key), edit.text)
			dm.UpdateImportEdges(uri(edit.key))
			dm.PropagateChange(uri(edit.key))
		}
		taken.fast = stored.StoredEdits() > before
		taken.inPlace = stored.StoredInPlace() > beforeInPlace
		taken.checksAfter = stored.ChecksAfterEdits() - beforeAfter
		taken.reasons = stored.FullCheckReasons()
		if taken.reasons == nil {
			taken.reasons = map[string]int{}
		}
		taken.clean = true
		for key := range inRecord {
			if snap := full.Snapshot(uri(key)); snap == nil || len(snap.Errors) > 0 || len(snap.Damaged) > 0 || snap.Analysis == nil || len(snap.Analysis.TypeErrors) > 0 {
				taken.clean = false
				if snap != nil && snap.Analysis != nil && len(snap.Analysis.TypeErrors) > 0 {
					taken.reasons[key+": "+snap.Analysis.TypeErrors[0].Message]++
				}
			}
		}
	}
	for _, key := range keys {
		want, got := renderOpen(full, uri(key), dir), renderOpen(stored, uri(key), dir)
		if want != got {
			return fmt.Sprintf("%s opened from its record:\n%s", key, dumpDiff(want, got)), true, taken
		}
	}
	return "", true, taken
}

// TestOpenFromRecordEditsMatchAFullCheck: an edit made in an open document
// of a program opened from its record answers, in every open document,
// what the program checked whole answers (recordDiff), for each step of
// the incremental harness's edit sequences of orderProject, layeredProject
// and rateProject, of genericEdits over orderProject's files, and of a
// body edit of every file of every multi-file corpus project. Each step
// starts from a record of the texts before it. A step after which the
// program has nothing to report must be taken in the program opened from
// the record, with no check of the whole program, unless it changes an
// impl, which a document that does not import the edited file may see.
func TestOpenFromRecordEditsMatchAFullCheck(t *testing.T) {
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	type sequence struct {
		p     incrementalProject
		steps []editStep
	}
	order := orderIncremental()
	seqs := []sequence{{order, orderEdits}, {layeredProject(), layeredEdits}, {rateProject(), rateEdits}, {anonFieldsProject(), anonFieldsEdits}, {anonTypeFieldsProject(), anonTypeFieldsEdits}}
	for _, key := range sortedTextKeys(order) {
		seqs = append(seqs, sequence{order, genericEdits(key, order.texts[key])})
	}
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range corpus {
		var steps []editStep
		for _, key := range sortedTextKeys(p) {
			if line := firstBodyLine(p.texts[key]); line != "" {
				steps = append(steps, replace("body only, positions shift", key, line, line+"    // A comment.\n\n"))
				// Edits that leave something to report: a name nothing
				// declares, and a body that does not parse.
				steps = append(steps, replace("typo in a body", key, line, line+"    zz_misspelled\n"))
				steps = append(steps, replace("syntax error in a body", key, line, line+"    let = (\n"))
			}
		}
		if p.name == "15-app-and-defer/app_field_permission" {
			// A program with an application type: a read moved by lines,
			// which keeps the roots' walk (AppWalkSummary), a read added where
			// the entry's main reaches it, a function that reads the
			// application type called from a test with no boot, and a
			// factory whose lambda reads it.
			steps = append(steps,
				replace("body only, read moved", "imports_app", "pub fn read() -> String {\n", "pub fn read() -> String {\n\n    // The tag.\n"),
				replace("read added", "imports_app", "pub fn payload_of(value: Int) -> Int {\n    value\n", "pub fn payload_of(value: Int) -> Int {\n    _ = Settings.tag\n    value\n"),
				appendText("factory reads", "imports_app", "\npub fn reader() -> () -> String {\n    fn -> Settings.tag\n}\n"),
				appendText("test reads without a boot", "no_app_name", "\ntest \"reads\" {\n    assert read() == \"\"\n}\n"),
			)
		}
		seqs = append(seqs, sequence{p, steps})
	}
	fast, inPlace, total := 0, 0, 0
	reporting, reportingFast := 0, 0
	var checksAfter int64
	supplies, apart := analysis.CheckSuppliesForTest(func() {
		for _, seq := range seqs {
			texts := maps.Clone(seq.p.texts)
			for i, s := range seq.steps {
				text, ok := s.change(texts[s.key], seq.p.texts[s.key])
				if !ok {
					t.Fatalf("%s: step %d (%s) does not apply to %s", seq.p.name, i+1, s.kind, s.key)
				}
				p := seq.p
				p.texts = texts
				dir := writeProject(t, p)
				diff, recorded, taken := recordDiffTaken(t, p, dir, &recordEdit{key: s.key, text: text})
				at := fmt.Sprintf("%s: step %d (%s, editing %s)", p.name, i+1, s.kind, s.key)
				if diff != "" {
					t.Fatalf("%s: %s", at, diff)
				}
				if !strings.Contains(s.kind, "typo") && !strings.Contains(s.kind, "syntax error") {
					// The steps after an edit that leaves something to report start
					// from the texts before it, which have a record.
					texts = maps.Clone(texts)
					texts[s.key] = text
				}
				if !recorded {
					continue
				}
				total++
				if taken.fast {
					fast++
				}
				if taken.inPlace {
					inPlace++
				}
				checksAfter += taken.checksAfter
				// An unannotated `once` whose value changed checks the files that
				// import it again, so its file is checked anew too.
				once := strings.Contains(s.kind, "once")
				if bodyOnly(s.kind) && !once && taken.inRecord && taken.fast && !taken.inPlace {
					t.Errorf("%s changes only a body, but the installed analysis was not rebuilt in place: %v", at, taken.reasons)
				}
				impl := strings.Contains(s.kind, "impl") || strings.Contains(s.kind, "derive")
				// A program with an application type compares the walk its
				// application-field roots take of each checked file's bodies
				// (AppWalkSummary), which an edit that calls or reads something
				// new changes. A body edit that only moves positions keeps it,
				// and must be taken.
				appType := false
				for why := range taken.reasons {
					appType = appType || strings.Contains(why, "application type") && !bodyOnly(s.kind)
				}
				if taken.inRecord && !impl && !appType && !taken.fast && !reportsWhatNoFileWords(taken.reasons) {
					t.Errorf("%s was not taken in the program opened from its record: %v", at, taken.reasons)
				}
				if !taken.clean {
					reporting++
					if taken.fast {
						reportingFast++
					}
				}
			}
		}
	})
	t.Logf("%d of %d edits taken in the program opened from its record, %d of them in place; %d of the %d that leave something to report; %d open documents checked from their installed checks; %d supplies from the entry's check", fast, total, inPlace, reportingFast, reporting, checksAfter, supplies)
	if checksAfter == 0 {
		t.Error("no open document was checked again from its installed check")
	}
	if supplies == 0 {
		t.Error("no edit took the missing-impl supply from the entry's check")
	}
	if apart > 0 {
		t.Errorf("%d of %d supplies taken from the entry's check differ from the supply worked out from every file's interface", apart, supplies)
	}
	if fast == 0 {
		t.Error("no edit was taken in a program opened from its record")
	}
	if reportingFast == 0 {
		t.Error("no edit that leaves something to report was taken in a program opened from its record")
	}
}

// unusedImplImportProject imports a whole file no name uses, whose impl
// block for a type it does not declare the program does not use either:
// the program checked whole settles the import unused and reports it, and
// a document opened from the record must say the same (DumpFileForTest's
// "impl import ... unused").
func unusedImplImportProject() incrementalProject {
	return incrementalProject{name: "unused impl import", entry: "main", texts: map[string]string{
		"main": `import {
    std::io
    shout
}

fn main() {
    io.print("quiet")
}
`,
		"shout": `pub interface Shout {
    fn shout(self) -> String
}

impl Shout for Int {
    fn shout(n: Int) -> String {
        "${n}!"
    }
}
`,
	}}
}

// reportsWhatNoFileWords reports whether reasons, why a manager checked
// whole programs, name only what an edit in a program opened from its
// record leaves to the program checked whole: diagnostics the program
// words and no one file's check (its impls' errors and missing impls), a
// reduction's diagnostic in a file the edit does not check again, an
// application type, or an impl changed beside a diagnostic.
func reportsWhatNoFileWords(reasons map[string]int) bool {
	named := false
	for why := range reasons {
		why, ok := strings.CutPrefix(why, "from the record: ")
		if !ok {
			// Another check, or a document's first diagnostic, which
			// recordDiffTaken notes.
			continue
		}
		named = true
		switch {
		case strings.Contains(why, "about the program's impls"),
			strings.Contains(why, "find a missing impl"),
			strings.Contains(why, "which the edit does not check again"),
			strings.Contains(why, "application type"),
			strings.Contains(why, "the edit changed an impl"),
			strings.Contains(why, "declares keyword declarations"):
		default:
			return false
		}
	}
	return named
}

// sortedTextKeys answers p's module keys in order.
func sortedTextKeys(p incrementalProject) []string {
	keys := make([]string, 0, len(p.texts))
	for key := range p.texts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestOpenFromRecordMatchesAFullCheckDumps: every document of every
// multi-file corpus project and of orderProject, layeredProject,
// rateProject and unusedImplImportProject opened from its program's record has the analysis it has
// when its program is checked whole, dump for dump (DumpFileForTest), with
// the same diagnostics, nodes and offered program. Each corpus project is
// opened again with a typo and then a syntax error in its entry's first
// body, whose record keeps what each file reports: most of those must be
// recorded, and so must the project that embeds files.
func TestOpenFromRecordMatchesAFullCheckDumps(t *testing.T) {
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	projects := []incrementalProject{orderIncremental(), layeredProject(), rateProject(), unusedImplImportProject()}
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	projects = append(projects, corpus...)
	for _, p := range corpus {
		line := firstBodyLine(p.texts[p.entry])
		if line == "" {
			continue
		}
		for _, v := range []struct{ name, text string }{{"typo", "    zz_misspelled\n"}, {"syntax error", "    let = (\n"}} {
			q := p
			q.name = p.name + "/" + v.name
			q.texts = maps.Clone(p.texts)
			q.texts[p.entry] = strings.Replace(p.texts[p.entry], line, line+v.text, 1)
			projects = append(projects, q)
		}
	}
	used, reporting, reportingUsed := 0, 0, 0
	embedded := false
	for _, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			reports := strings.HasSuffix(p.name, "/typo") || strings.HasSuffix(p.name, "/syntax error")
			if reports {
				reporting++
			}
			dir := writeProject(t, p)
			diff, ok := recordDiff(t, p, dir, nil)
			if !ok {
				t.Skip("no record")
			}
			used++
			if reports {
				reportingUsed++
			}
			if p.name == "14-modules-and-packaging/embedded_files" {
				embedded = true
			}
			if diff != "" {
				t.Fatal(diff)
			}
		})
	}
	t.Logf("%d programs opened from their records, %d of the %d that report something", used, reportingUsed, reporting)
	if used == 0 {
		t.Error("no program was opened from its record")
	}
	if reportingUsed*2 < reporting {
		t.Errorf("%d of %d programs that report something were opened from their records", reportingUsed, reporting)
	}
	if !embedded {
		t.Error("the program that embeds files was not opened from its record")
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
