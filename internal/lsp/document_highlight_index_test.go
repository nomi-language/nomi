package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// walkHighlights is documentHighlights as a walk of the whole analysis:
// every definition, reference and punned label tested against the target.
func walkHighlights(fa *analysis.FileAnalysis, sym *analysis.Symbol, path string) []string {
	targets := symbolTargets(sym, path)
	seen := map[protocol.Range]bool{}
	var out []string
	add := func(at analysis.Pos, name, kind string) {
		r := makeLocation("", at, name).Range
		if analysis.IsSynthesizedLine(at.Line) || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, fmt.Sprintf("%d:%d-%d %s", r.Start.Line, r.Start.Character, r.End.Character, kind))
	}
	for at, d := range fa.Definitions {
		if matchesAnyTarget(d, targets) {
			add(at, d.Name, "write")
		}
	}
	for at, r := range fa.References {
		if matchesAnyTarget(r, targets) {
			add(at, r.Name, "read")
		}
	}
	for at, f := range fa.PunnedFieldLabels {
		if matchesAnyTarget(f, targets) {
			add(at, f.Name, "read")
		}
	}
	sort.Strings(out)
	return out
}

// TestDocumentHighlight_IndexAgreesWithAWalk checks, at every definition
// and reference of a corpus file and a generated one, that the occurrence
// index finds the symbol FileAnalysis.SymbolAt finds and the highlights a
// walk of the whole analysis finds.
func TestDocumentHighlight_IndexAgreesWithAWalk(t *testing.T) {
	dir := filepath.Join("..", "..", "tests", "07-structs-and-enums", "struct_spread")
	corpus := map[string]string{}
	for _, name := range []string{"struct_spread_test.nomi", "test_helpers.nomi"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		corpus[name] = string(data)
	}
	projects := map[string]map[string]string{
		"generated.nomi":          {"generated.nomi": generatedFile(20)},
		"struct_spread_test.nomi": corpus,
	}
	for open, project := range projects {
		s, uri := openProject(t, project, open)
		fa := s.docs.Snapshot(uri).Analysis
		idx := newOccurrenceIndex(fa)
		path := uriToPath(uri)
		var positions []analysis.Pos
		for at := range fa.References {
			positions = append(positions, at)
		}
		for at := range fa.Definitions {
			positions = append(positions, at)
		}
		if len(positions) < 100 {
			t.Fatalf("%s: %d positions, want a file with symbols", open, len(positions))
		}
		checked := 0
		for _, at := range positions {
			// Where two tokens of one map cover a position, SymbolAt
			// picks either, in map order; skip those.
			if covering(fa.References, at) > 1 || covering(fa.References, at) == 0 && covering(fa.Definitions, at) > 1 {
				continue
			}
			checked++
			sym := idx.symbolAt(at)
			if want := fa.SymbolAt(at); sym != want {
				t.Fatalf("%s %v: index finds %v, SymbolAt %v", open, at, sym, want)
			}
			var got []string
			for _, h := range documentHighlights(idx, sym, path) {
				kind := "read"
				if *h.Kind == protocol.DocumentHighlightKindWrite {
					kind = "write"
				}
				got = append(got, fmt.Sprintf("%d:%d-%d %s", h.Range.Start.Line, h.Range.Start.Character, h.Range.End.Character, kind))
			}
			sort.Strings(got)
			if want := walkHighlights(fa, sym, path); strings.Join(got, ", ") != strings.Join(want, ", ") {
				t.Fatalf("%s %v (%s): got %v, want %v", open, at, sym.Name, got, want)
			}
		}
		if checked < len(positions)*3/4 {
			t.Errorf("%s: checked %d of %d positions", open, checked, len(positions))
		}
	}
}

// covering counts the tokens of m whose extent holds pos, as TokenAt
// measures them.
func covering(m map[analysis.Pos]*analysis.Symbol, pos analysis.Pos) int {
	n := 0
	for at, s := range m {
		width := s.Span
		if width <= 0 {
			width = len(s.Name)
		}
		if at.Line == pos.Line && pos.Col >= at.Col && pos.Col < at.Col+width {
			n++
		}
	}
	return n
}

// TestDocumentHighlight_WorkIsBoundedBySymbol counts the work of
// highlight requests: the occurrence index and the line index are built
// once per analyzed text, no request scans the text from its top, and the
// occurrences a request examines for a parameter are the same in a file
// of 10 functions and of 300.
func TestDocumentHighlight_WorkIsBoundedBySymbol(t *testing.T) {
	examined := map[int]int{}
	for _, n := range []int{10, 300} {
		src := generatedFile(n)
		s, uri := openProject(t, map[string]string{"main.nomi": src}, "main.nomi")
		// `users` in f0's signature.
		at := offsetPosition(src, strings.Index(src, "users"))
		params := &protocol.DocumentHighlightParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     at,
		}}
		scans := nthLineScans.Load()
		for range 5 {
			hs, err := s.textDocumentDocumentHighlight(nil, params)
			if err != nil {
				t.Fatal(err)
			}
			if len(hs) != 2 {
				t.Fatalf("%d functions: %d highlights of `users`, want 2", n, len(hs))
			}
		}
		if d := nthLineScans.Load() - scans; d != 0 {
			t.Errorf("%d functions: 5 requests scanned the text from the top %d times, want 0", n, d)
		}
		if s.occurrences.builds != 1 || s.lines.builds != 1 {
			t.Errorf("%d functions: built the occurrence index %d times and the line index %d times, want once each", n, s.occurrences.builds, s.lines.builds)
		}
		snap := s.docs.Snapshot(uri)
		idx := s.occurrences.get(uri, snap.Analysis)
		sym := idx.symbolAt(analysis.Pos{Line: int(at.Line) + 1, Col: int(at.Character) + 1})
		examined[n] = len(idx.candidates(symbolTargets(sym, uriToPath(uri))))
		// A new analysis gets a new index.
		s.docs.Open(uri, src+"\n")
		if _, err := s.textDocumentDocumentHighlight(nil, params); err != nil {
			t.Fatal(err)
		}
		if s.occurrences.builds != 2 {
			t.Errorf("%d functions: after an edit, %d occurrence index builds, want 2", n, s.occurrences.builds)
		}
	}
	if examined[10] != examined[300] || examined[10] == 0 {
		t.Errorf("examined %d occurrences in 10 functions and %d in 300, want the same nonzero count", examined[10], examined[300])
	}
}
