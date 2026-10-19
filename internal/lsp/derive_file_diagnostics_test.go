package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A derive in an imported file whose field type has no impl is published on
// that file, at the field. The entry's analysis carries it too (its program's
// coherence), but at a position in the other file, so the entry's own
// diagnostics leave it out rather than mark the entry's line 7.
func TestDeriveMissingImplIsPublishedOnTheFileWithTheDerive(t *testing.T) {
	files := map[string]string{
		"main.nomi": `import {
    std/io
    child.Holder
}

fn main() {
    io.inspect(#Holder{n: 1, o: #Opaque{f: fn x -> x}})
}
`,
		"child.nomi": `pub struct Opaque {
    f: (Int) -> Int
}

pub struct Holder {
    n: Int
    o: Opaque
}

derive Equatable for Holder
`,
	}
	s, mainURI := openProject(t, files, "main.nomi")
	childURI := strings.TrimSuffix(mainURI, "main.nomi") + "child.nomi"
	s.docs.Open(childURI, files["child.nomi"])
	missing := func(uri string) []protocol.Diagnostic {
		snap := s.docs.Snapshot(uri)
		if snap == nil || snap.Analysis == nil {
			t.Fatalf("%s: no analysis", uri)
		}
		var out []protocol.Diagnostic
		for _, d := range buildPublishedDiagnostics(snap.URI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors) {
			if strings.Contains(d.Message, "no impl of `Equatable` for `Opaque`") {
				out = append(out, d)
			}
		}
		return out
	}
	if ds := missing(mainURI); len(ds) != 0 {
		t.Errorf("main.nomi shows the missing impl of child.nomi's derive at line %d", ds[0].Range.Start.Line+1)
	}
	ds := missing(childURI)
	if len(ds) != 1 || ds[0].Range.Start.Line != 6 || ds[0].Range.Start.Character != 4 {
		t.Fatalf("child.nomi's missing impl: %+v, want one at 7:5", ds)
	}
}
