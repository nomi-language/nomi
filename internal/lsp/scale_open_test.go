package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/projectgen"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestScaleOpen measures what opening a generated project costs the
// language server, and the requests a reader makes next, at about 50k
// lines (NOMI_SCALE_LINES picks another size):
//
//	NOMI_SCALE_LSP=1 go test ./internal/lsp -run TestScaleOpen -count=1 -v -timeout 0
//
// Each row is the time and the project files the document manager built
// from source (analysis.DocumentManager.FilesBuilt), and the heap the
// server holds after a collection once the entry is open.
func TestScaleOpen(t *testing.T) {
	if os.Getenv("NOMI_SCALE_LSP") == "" {
		t.Skip("set NOMI_SCALE_LSP=1 to measure opening a generated project")
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
	if os.Getenv("NOMI_SCALE_TYPO") != "" {
		// A project that reports something: its record keeps what each file
		// reports.
		key, text := gen.Edit(projectgen.TypoLeaf, 0)
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(key)+".nomi"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		gen.Files[key] = text
	}
	uriOf := func(key string) string {
		return fileuri.FromPath(filepath.Join(dir, filepath.FromSlash(key)+".nomi"))
	}
	entry := uriOf(gen.Entry)
	util := uriOf(gen.Hub())
	utilText := gen.Files[gen.Hub()]
	clamp := markerPosition(t, utilText, "pub fn clamp", len("pub fn "))
	call := regexp.MustCompile(`[a-z]+[0-9]+\.(f[0-9]+_[0-9]+)\(`).FindStringSubmatchIndex(gen.Files[gen.Entry])
	if call == nil {
		t.Fatal("the entry calls no unit")
	}
	hoverAt := offsetPosition(gen.Files[gen.Entry], call[2])

	type row struct {
		took  time.Duration
		files int64
		note  string
	}
	var order []string
	results := []map[string]row{{}, {}}
	session := func(n int) {
		res := results[n]
		run := func(s *Server, name string, f func() string) {
			if n == 0 {
				order = append(order, name)
			}
			runtime.GC()
			before := s.docs.FilesBuilt()
			start := time.Now()
			note := f()
			res[name] = row{time.Since(start), s.docs.FilesBuilt() - before, note}
		}
		var base, held runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&base)
		s := NewServer()
		s.docs.SetWorkspaceRoot(dir)
		run(s, "open the entry, first diagnostics", func() string {
			s.docs.Open(entry, gen.Files[gen.Entry])
			s.docs.AnalyzeLatest(context.Background(), entry)
			snap := s.docs.Snapshot(entry)
			opened, _ := s.docs.StoredPrograms()
			return fmt.Sprintf("%d diagnostics, %d opened from a record", len(snap.Analysis.TypeErrors), opened)
		})
		runtime.GC()
		runtime.ReadMemStats(&held)
		if n == 0 {
			order = append(order, "heap held after GC, the entry open")
		}
		res["heap held after GC, the entry open"] = row{note: fmt.Sprintf("%.0f MB", float64(int64(held.HeapAlloc)-int64(base.HeapAlloc))/1e6)}
		run(s, "hover on a call into a closed file", func() string {
			res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(entry)}, Position: hoverAt}})
			if err != nil || res == nil {
				t.Fatalf("hover: %v %v", res, err)
			}
			return ""
		})
		run(s, "definition into a closed file", func() string {
			locs := appFieldDefinitions(t, s, entry, hoverAt)
			return fmt.Sprintf("%d locations", len(locs))
		})
		run(s, "open core/util", func() string {
			s.docs.Open(util, utilText)
			s.docs.AnalyzeLatest(context.Background(), util)
			return ""
		})
		run(s, "references to util.clamp", func() string {
			locs, err := s.textDocumentReferences(nil, &protocol.ReferenceParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(util)}, Position: clamp},
				Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			return fmt.Sprintf("%d locations", len(locs))
		})
		run(s, "rename util.clamp", func() string {
			edit, err := s.textDocumentRename(nil, &protocol.RenameParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(util)}, Position: clamp},
				NewName:                    "clamp_to",
			})
			if err != nil || edit == nil {
				t.Fatalf("rename: %v", err)
			}
			n := 0
			for _, edits := range edit.Changes {
				n += len(edits)
			}
			return fmt.Sprintf("%d edits in %d files", n, len(edit.Changes))
		})
		run(s, "diagnostics of a closed leaf", func() string {
			snap := s.docs.AnalyzeClosed(uriOf(gen.Leaf()))
			return fmt.Sprintf("%d diagnostics", len(snap.TypeErrors()))
		})
		if n == 0 {
			run(s, "write the record (after a clean lowering)", func() string {
				s.docs.NoteLoweredClean(entry, gen.Files[gen.Entry], nil)
				_, stored := s.docs.StoredPrograms()
				return fmt.Sprintf("%d written", stored)
			})
		}
		run(s, "edit the entry's body", func() string {
			text := strings.Replace(gen.Files[gen.Entry], "total = 0", "total = 1", 1)
			s.docs.Update(entry, text)
			snap := s.docs.Snapshot(entry)
			return fmt.Sprintf("%d diagnostics", len(snap.Analysis.TypeErrors))
		})
	}
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	session(0)
	session(1)
	var b strings.Builder
	fmt.Fprintf(&b, "\n%d lines, %d files\n\n| operation | without a record | files built | | from the record | files built | |\n|---|---|---|---|---|---|---|\n", gen.Lines(), len(gen.Files)-len(gen.Tests))
	for _, name := range order {
		fmt.Fprintf(&b, "| %s |", name)
		for _, res := range results {
			r, ok := res[name]
			switch {
			case !ok:
				b.WriteString(" | | |")
			case r.took == 0:
				fmt.Fprintf(&b, " | | %s |", r.note)
			default:
				fmt.Fprintf(&b, " %.3f s | %d | %s |", r.took.Seconds(), r.files, r.note)
			}
		}
		b.WriteString("\n")
	}
	t.Log(b.String())
}

// markerPosition is the position of the byte after the first occurrence
// of marker in text, moved by skip bytes.
func markerPosition(t *testing.T, text, marker string, skip int) protocol.Position {
	t.Helper()
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("no %q", marker)
	}
	return offsetPosition(text, i+skip)
}
