package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/projectgen"
	"github.com/nomi-language/nomi/vmhost"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestScaleEdits measures the first edit of each of projectgen's kinds in
// a generated project of about 50k lines (NOMI_SCALE_LINES picks another
// size), in a server that checked the program whole and in one that
// opened it from its record:
//
//	NOMI_SCALE_LSP=1 go test ./internal/lsp -run TestScaleEdits -count=1 -v -timeout 0
//
// Each session opens the entry, and the edited file when it is another,
// then makes one edit. A row is the edit's analysis, the propagation of
// the edit to the open documents that import the edited file, the edited
// document's lowering, the entry's lowering when the edited file is
// another, and references to util.clamp, each with the project files the
// document manager built from source and whether the edit was taken in the
// program opened from its record. NOMI_SCALE_EDITS=<kinds> measures only
// the kinds whose names it holds.
func TestScaleEdits(t *testing.T) {
	if os.Getenv("NOMI_SCALE_LSP") == "" {
		t.Skip("set NOMI_SCALE_LSP=1 to measure edits in a generated project")
	}
	lines := 50000
	if s := os.Getenv("NOMI_SCALE_LINES"); s != "" {
		fmt.Sscan(s, &lines)
	}
	gen := projectgen.Generate(projectgen.Config{Seed: 1, Lines: lines})
	dir := t.TempDir()
	if err := gen.Write(dir); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	t.Setenv(artifactcache.RootEnv, cacheDir)
	uriOf := func(key string) string {
		return fileuri.FromPath(filepath.Join(dir, filepath.FromSlash(key)+".nomi"))
	}
	entry := uriOf(gen.Entry)
	util := uriOf(gen.Hub())
	clamp := markerPosition(t, gen.Files[gen.Hub()], "pub fn clamp", len("pub fn "))

	// The record, written as a server writes it after a clean lowering.
	writer := NewServer()
	writer.docs.SetWorkspaceRoot(dir)
	writer.docs.Open(entry, gen.Files[gen.Entry])
	func() {
		defer writer.docs.View()()
		a, _ := writer.installedLoweringAnalysis(entry, gen.Files[gen.Entry])
		if diags := writer.safeLoweringDiagnostics(context.Background(), entry, gen.Files[gen.Entry], a); len(diags) > 0 || a == nil {
			t.Fatalf("the entry's lowering: %v, from the analysis %v", diags, a != nil)
		}
	}()
	writer.docs.NoteLoweredClean(entry, gen.Files[gen.Entry], analyzedlowering.Kept)
	if _, stored := writer.docs.StoredPrograms(); stored == 0 {
		t.Fatal("no record written")
	}

	type cell struct {
		took  time.Duration
		files int64
		note  string
	}
	kinds := []projectgen.EditKind{projectgen.BodyEntry, projectgen.BodyLeaf, projectgen.BodyCallLeaf, projectgen.SignatureLeaf, projectgen.ImplLeaf, projectgen.BodyHub, projectgen.SignatureHub, projectgen.TypoLeaf, projectgen.TypoEntry}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%d lines, %d files\n\n| edit | step | checked whole | files | from the record | files | |\n|---|---|---|---|---|---|---|\n", gen.Lines(), len(gen.Files)-len(gen.Tests))
	for _, kind := range kinds {
		if only := os.Getenv("NOMI_SCALE_EDITS"); only != "" && !strings.Contains(only, string(kind)) {
			continue
		}
		key, text := gen.Edit(kind, 0)
		doc := uriOf(key)
		var rows [2]map[string]cell
		var order []string
		for mode := range 2 {
			if mode == 0 {
				os.Setenv(artifactcache.RootEnv, "off")
			}
			s := NewServer()
			s.docs.SetWorkspaceRoot(dir)
			s.docs.Open(entry, gen.Files[gen.Entry])
			s.docs.AnalyzeLatest(context.Background(), entry)
			for _, u := range []string{doc, util} {
				if u != entry && !s.docs.IsOpen(u) {
					s.docs.Open(u, gen.Files[relKey(dir, u)])
					s.docs.AnalyzeLatest(context.Background(), u)
				}
			}
			s.docs.UpdateImportEdges(entry)
			s.docs.UpdateImportEdges(doc)
			if mode == 0 && doc != entry {
				// The entry's lowering before the edit, which a lowering from the
				// analysis after it starts from; a program opened from its record
				// starts from the record's.
				func() {
					defer s.docs.View()()
					a, _ := s.installedLoweringAnalysis(entry, gen.Files[gen.Entry])
					s.safeLoweringDiagnostics(context.Background(), entry, gen.Files[gen.Entry], a)
				}()
			}
			res := map[string]cell{}
			run := func(name string, f func() string) {
				if mode == 0 {
					order = append(order, name)
				}
				runtime.GC()
				before := s.docs.FilesBuilt()
				start := time.Now()
				note := f()
				res[name] = cell{time.Since(start), s.docs.FilesBuilt() - before, note}
			}
			run("the edit", func() string {
				was := s.docs.StoredEdits()
				s.docs.Update(doc, text)
				snap := s.docs.Snapshot(doc)
				if taken := s.docs.StoredEdits() - was; taken == 0 && mode == 1 {
					return fmt.Sprintf("%d diagnostics, none taken from the record: %v", len(snap.Analysis.TypeErrors), s.docs.FullCheckReasons())
				}
				return fmt.Sprintf("%d diagnostics, %d taken from the record", len(snap.Analysis.TypeErrors), s.docs.StoredEdits()-was)
			})
			run("propagation to the open importers", func() string {
				s.docs.UpdateImportEdges(doc)
				open, _ := s.docs.PropagateChange(doc)
				return fmt.Sprintf("%d open documents", len(open))
			})
			run("the edited document's lowering", func() string {
				if s.docs.StoredLoweredClean(doc, text) {
					return "answered by the record"
				}
				stored := s.docs.StoredLowering(doc, text)
				was := vmhost.StoredRelowersForTest()
				release := s.docs.View()
				a, _ := s.installedLoweringAnalysis(doc, text)
				if a == nil && stored != nil {
					a = &analyzedlowering.Analyzed{Stored: stored}
				}
				diags := s.safeLoweringDiagnostics(context.Background(), doc, text, a)
				release()
				return fmt.Sprintf("%d diagnostics, from the analysis %v, %d files lowered from the record's kept lowering", len(diags), a != nil, vmhost.StoredRelowersForTest()-was)
			})
			if doc != entry {
				run("the entry's lowering", func() string {
					// The edit saved: a lowering from the analysis reads only texts
					// that are on disk.
					path := filepath.Join(dir, filepath.FromSlash(key)+".nomi")
					if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
						t.Fatal(err)
					}
					defer os.WriteFile(path, []byte(gen.Files[key]), 0o644)
					entryText := gen.Files[gen.Entry]
					stored := s.docs.StoredLowering(entry, entryText)
					wasStored, wasAnalyzed := vmhost.StoredRelowersForTest(), vmhost.AnalyzedRelowersForTest()
					release := s.docs.View()
					a, _ := s.installedLoweringAnalysis(entry, entryText)
					if a == nil && stored != nil {
						a = &analyzedlowering.Analyzed{Stored: stored}
					}
					diags := s.safeLoweringDiagnostics(context.Background(), entry, entryText, a)
					release()
					return fmt.Sprintf("%d diagnostics, from the analysis %v, file by file %v", len(diags), a != nil,
						vmhost.StoredRelowersForTest() > wasStored || vmhost.AnalyzedRelowersForTest() > wasAnalyzed)
				})
			}
			run("references to util.clamp", func() string {
				locs, err := s.textDocumentReferences(nil, &protocol.ReferenceParams{
					TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(util)}, Position: clamp},
					Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
				})
				if err != nil {
					t.Fatal(err)
				}
				return fmt.Sprintf("%d locations", len(locs))
			})
			rows[mode] = res
			os.Setenv(artifactcache.RootEnv, cacheDir)
		}
		for _, name := range order {
			fmt.Fprintf(&b, "| %s | %s |", kind, name)
			for _, res := range rows {
				c := res[name]
				fmt.Fprintf(&b, " %.3f s | %d |", c.took.Seconds(), c.files)
			}
			if n := rows[0][name].note; n != rows[1][name].note && name == "the entry's lowering" {
				fmt.Fprintf(&b, " %s; checked whole: %s |\n", rows[1][name].note, n)
				continue
			}
			fmt.Fprintf(&b, " %s |\n", rows[1][name].note)
		}
	}
	t.Log(b.String())
}

// relKey is the module key of the file at uri under dir.
func relKey(dir, uri string) string {
	rel, _ := filepath.Rel(dir, fileuri.ToPath(uri))
	return strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
}
