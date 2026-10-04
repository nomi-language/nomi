package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// appFieldDefinitions drives the real definition entry point and returns
// every location it answers, sorted by line then column.
func appFieldDefinitions(t *testing.T, s *Server, uri string, pos protocol.Position) []protocol.Location {
	t.Helper()
	result, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos}})
	if err != nil {
		t.Fatalf("definition at L%dC%d: %v", pos.Line, pos.Character, err)
	}
	var locs []protocol.Location
	switch v := result.(type) {
	case []protocol.Location:
		locs = append(locs, v...)
	case *protocol.Location:
		if v != nil {
			locs = append(locs, *v)
		}
	}
	sort.Slice(locs, func(i, j int) bool {
		a, b := locs[i].Range.Start, locs[j].Range.Start
		return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
	})
	return locs
}

// wantDefinitions asserts the locations are exactly `want`, each landing on
// `name` at a 0-based {line, column}.
func wantDefinitions(t *testing.T, what string, locs []protocol.Location, uri string, name string, want ...[2]uint32) {
	t.Helper()
	if len(locs) != len(want) {
		t.Fatalf("%s: %d definitions %v, want %d at %v", what, len(locs), locs, len(want), want)
	}
	for i, at := range want {
		wantDefinitionAt(t, what, locs[i], uri, at[0], at[1], name)
	}
}

// referencesAt drives the real references entry point, declarations
// included, over src written as a project's main.nomi, and returns each
// location as "line:col-end" (0-based), sorted. It fails on a location in
// another file or a duplicate.
func referencesAt(t *testing.T, src string, pos protocol.Position) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(path)
	server := NewServer()
	server.docs.Open(uri, src)
	locs, err := server.textDocumentReferences(nil, &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos},
		Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatalf("references at L%dC%d: %v", pos.Line, pos.Character, err)
	}
	var got []string
	seen := map[string]bool{}
	for _, l := range locs {
		if string(l.URI) != uri {
			t.Errorf("reference in another file: %v", l)
		}
		s := itoa(l.Range.Start.Line) + ":" + itoa(l.Range.Start.Character) + "-" + itoa(l.Range.End.Character)
		if seen[s] {
			t.Errorf("duplicate reference %s", s)
		}
		seen[s] = true
		got = append(got, s)
	}
	sort.Strings(got)
	return got
}
